package dataset

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "modernc.org/sqlite"
)

type IndexInfo struct {
	Exists       bool   `json:"exists"`
	DatabasePath string `json:"database_path"`
	Messages     int    `json:"messages"`
	Sessions     int    `json:"sessions"`
	Sources      int    `json:"sources"`
}

type ImportResult struct {
	Files          int    `json:"files"`
	ChangedFiles   int    `json:"changed_files"`
	UnchangedFiles int    `json:"unchanged_files"`
	Messages       int    `json:"messages"`
	Sessions       int    `json:"sessions"`
	DatabasePath   string `json:"database_path"`
}

const schema = `
CREATE TABLE IF NOT EXISTS sources(path TEXT PRIMARY KEY, hash TEXT NOT NULL, session_id TEXT NOT NULL, exported_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages(
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, display_name TEXT NOT NULL, local_id INTEGER NOT NULL,
 create_time INTEGER NOT NULL, formatted_time TEXT NOT NULL, type TEXT NOT NULL, content TEXT NOT NULL,
 is_send INTEGER NOT NULL, sender TEXT NOT NULL, sender_name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS source_messages(source_path TEXT NOT NULL, message_id TEXT NOT NULL, PRIMARY KEY(source_path,message_id));
CREATE INDEX IF NOT EXISTS messages_time ON messages(create_time,session_id,id);
CREATE INDEX IF NOT EXISTS messages_session_time ON messages(session_id,create_time,id);
CREATE INDEX IF NOT EXISTS source_messages_message ON source_messages(message_id);
`

func openDatabase(path string, write bool) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, &ValidationError{"database path is empty"}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !write {
		if _, err := os.Stat(absolute); os.IsNotExist(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	filePath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" {
		filePath = "/" + filePath
	}
	uri := url.URL{Scheme: "file", Path: filePath}
	query := uri.Query()
	if !write {
		query.Set("mode", "ro")
	}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	if !write {
		if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
			db.Close()
			return nil, err
		}
	}
	if !write {
		if _, err := databaseIdentity(db, false); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func IndexStatus(path string) (*IndexInfo, error) {
	info := &IndexInfo{DatabasePath: path}
	db, err := openDatabase(path, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return info, nil
	}
	defer db.Close()
	if err := db.QueryRow("SELECT COUNT(*),COUNT(DISTINCT session_id) FROM messages").Scan(&info.Messages, &info.Sessions); err != nil {
		return nil, fmt.Errorf("read index %s: %w", path, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sources").Scan(&info.Sources); err != nil {
		return nil, err
	}
	info.Exists = true
	return info, nil
}

// Import appends or updates archived records in one transaction. Truncated or
// missing source files never delete history; only explicit future maintenance
// commands may remove indexed records.
func Import(exportDir, databasePath string) (result *ImportResult, err error) {
	info, err := os.Stat(exportDir)
	if err != nil {
		return nil, fmt.Errorf("read export directory: %w", err)
	}
	if !info.IsDir() {
		return nil, &ValidationError{"export directory must be a directory"}
	}
	paths, err := files(exportDir)
	if err != nil {
		return nil, err
	}
	_, beforeErr := os.Stat(databasePath)
	db, err := openDatabase(databasePath, true)
	if err != nil {
		return nil, err
	}
	defer func() {
		db.Close()
		// SQLite rolls a failed first transaction back to an empty file. Remove only
		// that empty file, never a pre-existing database or a nonempty committed one.
		if err != nil && os.IsNotExist(beforeErr) {
			if info, statErr := os.Stat(databasePath); statErr == nil && info.Size() == 0 {
				_ = os.Remove(databasePath)
			}
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	initialize, err := databaseIdentity(tx, true)
	if err != nil {
		return nil, err
	}
	if initialize {
		if _, err := tx.Exec(schema); err != nil {
			return nil, err
		}
		if _, err := tx.Exec("PRAGMA application_id=1465401671; PRAGMA user_version=1;"); err != nil {
			return nil, err
		}
	}
	result = &ImportResult{Files: len(paths), DatabasePath: databasePath}
	upsert, err := tx.Prepare(`INSERT INTO messages(id,session_id,display_name,local_id,create_time,formatted_time,type,content,is_send,sender,sender_name)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id,display_name=excluded.display_name,local_id=excluded.local_id,
 create_time=excluded.create_time,formatted_time=excluded.formatted_time,type=excluded.type,content=excluded.content,is_send=excluded.is_send,sender=excluded.sender,sender_name=excluded.sender_name`)
	if err != nil {
		return nil, err
	}
	defer upsert.Close()
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		var oldHash string
		err = tx.QueryRow("SELECT hash FROM sources WHERE path=?", absolute).Scan(&oldHash)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if oldHash == hash {
			result.UnchangedFiles++
			continue
		}
		// Decode the exact bytes hashed above, one changed source at a time.
		log, err := decode(path, data)
		if err != nil {
			return nil, err
		}
		result.ChangedFiles++
		occurrences := map[string]int{}
		for _, msg := range log.messages {
			id := messageKey(msg)
			if msg.LocalID == 0 {
				occurrences[id]++
				id = fmt.Sprintf("%s:%d", id, occurrences[id])
			}
			if _, err := upsert.Exec(id, msg.SessionID, msg.DisplayName, msg.LocalID, msg.CreateTime, msg.FormattedTime, msg.Type, msg.Content, msg.IsSend, msg.Sender, msg.SenderName); err != nil {
				return nil, err
			}
			if _, err := tx.Exec("INSERT OR IGNORE INTO source_messages(source_path,message_id) VALUES(?,?)", absolute, id); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec("INSERT INTO sources(path,hash,session_id,exported_at) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash,session_id=excluded.session_id,exported_at=excluded.exported_at", absolute, hash, log.id, log.exported); err != nil {
			return nil, err
		}
	}
	if err := tx.QueryRow("SELECT COUNT(*),COUNT(DISTINCT session_id) FROM messages").Scan(&result.Messages, &result.Sessions); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

type rowQuerier interface{ QueryRow(string, ...any) *sql.Row }

// Refuse unrelated databases and unknown future schema versions. Loading a
// database must never silently downgrade its schema or overwrite its metadata.
func databaseIdentity(q rowQuerier, allowEmpty bool) (bool, error) {
	var version, applicationID int
	if err := q.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return false, err
	}
	if err := q.QueryRow("PRAGMA application_id").Scan(&applicationID); err != nil {
		return false, err
	}
	if version == 1 && applicationID == 1465401671 {
		return false, nil
	}
	if version == 0 && applicationID == 0 && allowEmpty {
		var count int
		if err := q.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
			return false, err
		}
		if count == 0 {
			return true, nil
		}
	}
	return false, fmt.Errorf("unsupported or unrelated index database (schema=%d, application=%d)", version, applicationID)
}

// Identity excludes display names, which can change when contacts are renamed.
// Without a local ID or sender ID, identical records use their per-file ordinal.
func messageKey(msg Message) string {
	var parts []any
	if msg.LocalID != 0 {
		parts = []any{msg.SessionID, msg.LocalID, msg.CreateTime}
	} else {
		parts = []any{msg.SessionID, msg.CreateTime, msg.Type, msg.Content, msg.IsSend, msg.Sender}
	}
	raw, _ := json.Marshal(parts)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func sqlFilter(opts QueryOptions) (string, []any, error) {
	if err := Validate(opts); err != nil {
		return "", nil, err
	}
	clauses := []string{"1=1"}
	args := []any{}
	if len(opts.Sessions) > 0 {
		marks := make([]string, len(opts.Sessions))
		for i, id := range opts.Sessions {
			marks[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "session_id IN ("+strings.Join(marks, ",")+")")
	}
	start, end, _ := DateRange(opts.StartDate, opts.EndDate)
	if opts.StartDate != "" {
		clauses = append(clauses, "create_time>=?")
		args = append(args, start)
	}
	if opts.EndDate != "" {
		clauses = append(clauses, "create_time<=?")
		args = append(args, end)
	}
	if opts.Keyword != "" {
		clauses = append(clauses, "instr(lower(content),lower(?))>0")
		args = append(args, opts.Keyword)
	}
	return strings.Join(clauses, " AND "), args, nil
}

func QueryDatabase(path string, opts QueryOptions) (*QueryResult, error) {
	where, args, err := sqlFilter(opts)
	if err != nil {
		return nil, err
	}
	result := &QueryResult{Messages: []Message{}, Offset: opts.Offset, Limit: opts.Limit}
	db, err := openDatabase(path, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return result, nil
	}
	defer db.Close()
	// A read transaction keeps count and page consistent with concurrent imports.
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT COUNT(*) FROM messages WHERE "+where, args...).Scan(&result.Total); err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit == 0 {
		limit = -1
	}
	args = append(args, limit, opts.Offset)
	rows, err := tx.Query("SELECT session_id,display_name,local_id,create_time,formatted_time,type,content,is_send,sender,sender_name FROM messages WHERE "+where+" ORDER BY create_time,session_id,local_id,id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.SessionID, &msg.DisplayName, &msg.LocalID, &msg.CreateTime, &msg.FormattedTime, &msg.Type, &msg.Content, &msg.IsSend, &msg.Sender, &msg.SenderName); err != nil {
			return nil, err
		}
		result.Messages = append(result.Messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func SessionsDatabase(path string) ([]map[string]any, error) {
	result := []map[string]any{}
	db, err := openDatabase(path, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return result, nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT m.session_id,MIN(m.display_name),COUNT(*),COALESCE((SELECT MAX(exported_at) FROM sources s WHERE s.session_id=m.session_id),'')
 FROM messages m GROUP BY m.session_id ORDER BY MIN(m.display_name),m.session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, exported string
		var count int
		if err := rows.Scan(&id, &name, &count, &exported); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"session_id": id, "display_name": name, "messages": count, "last_export_time": exported, "file": ""})
	}
	return result, rows.Err()
}

func StatsDatabase(path string, opts QueryOptions) (*StatsResult, error) {
	where, args, err := sqlFilter(opts)
	if err != nil {
		return nil, err
	}
	result := &StatsResult{ByType: map[string]int{}, Sessions: []SessionStats{}}
	db, err := openDatabase(path, false)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return result, nil
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT session_id,MIN(display_name),COUNT(*),SUM(CASE WHEN is_send=1 THEN 1 ELSE 0 END),MIN(create_time),MAX(create_time) FROM messages WHERE "+where+" GROUP BY session_id ORDER BY session_id", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var stat SessionStats
		if err := rows.Scan(&stat.SessionID, &stat.DisplayName, &stat.Messages, &stat.SentMessages, &stat.FirstMessage, &stat.LastMessage); err != nil {
			rows.Close()
			return nil, err
		}
		stat.ReceivedMessages = stat.Messages - stat.SentMessages
		result.TotalMessages += stat.Messages
		result.SentMessages += stat.SentMessages
		result.ReceivedMessages += stat.ReceivedMessages
		result.Sessions = append(result.Sessions, stat)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result.SessionCount = len(result.Sessions)
	rows, err = tx.Query("SELECT CASE WHEN type='' THEN 'unknown' ELSE type END,COUNT(*) FROM messages WHERE "+where+" GROUP BY CASE WHEN type='' THEN 'unknown' ELSE type END", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var count int
		if err := rows.Scan(&label, &count); err != nil {
			return nil, err
		}
		result.ByType[label] = count
	}
	return result, rows.Err()
}
