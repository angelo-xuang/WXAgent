package dataset

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportBothFormatsIdempotentAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "chat_history")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	// A malformed root file must not override dedicated chat_history exports.
	fixture(t, dir, "ignored.json", `broken`)
	a := fixture(t, source, "a.json", `{"session":{"wxid":"first","displayName":"Same name"},"messages":[{"localId":1,"createTime":1767196800,"content":"Plan alpha","isSend":1,"type":"text","senderUsername":"me"},{"localId":2,"createTime":1767283199,"content":"beta","isSend":0,"type":"image"}]}`)
	b := fixture(t, source, "b.json", `{"session_id":"second","session_name":"Same name","messages":[{"create_time":1767196800,"str_content":"你好","is_send":1,"type":"text","sender_name":"张三"},{"create_time":1767196800,"str_content":"你好","is_send":1,"type":"text","sender_name":"张三"},{"formatted_time":"2026-01-02 00:00:00","str_content":"next day","is_send":0,"type":"text"}]}`)
	originalA, _ := os.ReadFile(a)
	originalB, _ := os.ReadFile(b)
	db := filepath.Join(dir, "data", "index.db")
	first, err := Import(dir, db)
	if err != nil {
		t.Fatal(err)
	}
	if first.ChangedFiles != 2 || first.Messages != 5 || first.Sessions != 2 {
		t.Fatalf("first import=%+v", first)
	}
	second, err := Import(dir, db)
	if err != nil || second.ChangedFiles != 0 || second.UnchangedFiles != 2 || second.Messages != 5 {
		t.Fatalf("reimport=%+v, %v", second, err)
	}
	records, err := QueryDatabase(db, QueryOptions{StartDate: "2026-01-01", EndDate: "2026-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	if records.Total != 4 || len(records.Messages) != 4 {
		t.Fatalf("inclusive UTC+8 day=%+v", records)
	}
	page, err := QueryDatabase(db, QueryOptions{Sessions: []string{"first"}, Keyword: "ALPHA", Offset: 0, Limit: 1})
	if err != nil || page.Total != 1 || page.Messages[0].IsSend != 1 {
		t.Fatalf("filter=%+v,%v", page, err)
	}
	sessions, err := SessionsDatabase(db)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("same names merged: %v,%v", sessions, err)
	}
	stats, err := StatsDatabase(db, QueryOptions{Limit: 1, Offset: 100})
	if err != nil || stats.TotalMessages != 5 || stats.SentMessages != 3 || stats.ReceivedMessages != 2 || stats.ByType["text"] != 4 {
		t.Fatalf("stats=%+v,%v", stats, err)
	}
	// Source removal never changes an already built index, including read queries.
	afterA, _ := os.ReadFile(a)
	afterB, _ := os.ReadFile(b)
	if !bytes.Equal(originalA, afterA) || !bytes.Equal(originalB, afterB) {
		t.Fatal("source exports changed")
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	again, err := Import(dir, db)
	if err != nil || again.Messages != 5 {
		t.Fatalf("missing source deleted records: %+v,%v", again, err)
	}
}

func TestChangedSourceUpsertsAndBatchFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"local_id":1,"create_time":10,"str_content":"old"}]}`)
	fixture(t, dir, "b.json", `{"session_id":"two","messages":[{"create_time":20,"str_content":"old b"}]}`)
	if _, err := Import(dir, db); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"local_id":1,"create_time":10,"str_content":"updated"},{"local_id":2,"create_time":11,"str_content":"new"}]}`)
	fixture(t, dir, "b.json", `bad json`)
	if _, err := Import(dir, db); err == nil || !strings.Contains(err.Error(), "b.json") {
		t.Fatalf("bad file was not reported: %v", err)
	}
	prior, err := QueryDatabase(db, QueryOptions{})
	if err != nil || prior.Total != 2 || prior.Messages[0].Content != "old" {
		t.Fatalf("failed batch changed database: %+v,%v", prior, err)
	}
	fixture(t, dir, "b.json", `{"session_id":"two","messages":[{"create_time":20,"str_content":"old b"}]}`)
	next, err := Import(dir, db)
	if err != nil || next.ChangedFiles != 1 || next.Messages != 3 {
		t.Fatalf("incremental=%+v,%v", next, err)
	}
	updated, _ := QueryDatabase(db, QueryOptions{Sessions: []string{"one"}})
	if updated.Messages[0].Content != "updated" {
		t.Fatalf("local ID not upserted: %+v", updated)
	}
}

func TestSQLFailureRollsBackSourcesAndMessagesTogether(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"local_id":1,"create_time":1,"str_content":"original"}]}`)
	if _, err := Import(dir, dbPath); err != nil {
		t.Fatal(err)
	}
	db, err := openDatabase(dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TRIGGER abort_test BEFORE INSERT ON messages WHEN new.content='fail' BEGIN SELECT RAISE(ABORT,'simulated disk constraint'); END`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"local_id":1,"create_time":1,"str_content":"changed"}]}`)
	fixture(t, dir, "b.json", `{"session_id":"two","messages":[{"create_time":2,"str_content":"fail"}]}`)
	if _, err := Import(dir, dbPath); err == nil {
		t.Fatal("SQL failure was not returned")
	}
	prior, err := QueryDatabase(dbPath, QueryOptions{})
	if err != nil || prior.Total != 1 || prior.Messages[0].Content != "original" {
		t.Fatalf("rollback=%+v,%v", prior, err)
	}
	info, err := IndexStatus(dbPath)
	if err != nil || info.Sources != 1 {
		t.Fatalf("source manifest changed: %+v,%v", info, err)
	}
}

func TestReadOnlyMissingDatabaseAndValidation(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "absent", "index.db")
	info, err := IndexStatus(db)
	if err != nil || info.Exists {
		t.Fatalf("missing index=%+v,%v", info, err)
	}
	if data, err := QueryDatabase(db, QueryOptions{}); err != nil || data.Total != 0 || data.Messages == nil {
		t.Fatalf("empty query=%+v,%v", data, err)
	}
	if _, err := SessionsDatabase(db); err != nil {
		t.Fatal(err)
	}
	if _, err := StatsDatabase(db, QueryOptions{}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("read query created data files")
	}
	for _, opts := range []QueryOptions{{StartDate: "bad"}, {EndDate: "2026-02-30"}, {StartDate: "2026-02-02", EndDate: "2026-02-01"}, {Offset: -1}, {Limit: -1}} {
		if _, err := QueryDatabase(db, opts); err == nil {
			t.Fatalf("invalid query accepted: %+v", opts)
		}
	}
	if _, err := Import(filepath.Join(root, "missing"), db); err == nil {
		t.Fatal("missing source directory accepted")
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatal("failed import created database")
	}
}

func TestStablePaginationAndLiteralKeyword(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"local_id":3,"create_time":1,"str_content":"100%_literal"},{"local_id":1,"create_time":1,"str_content":"first"},{"local_id":2,"create_time":1,"str_content":"second"}]}`)
	if _, err := Import(dir, db); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		page, err := QueryDatabase(db, QueryOptions{Offset: i, Limit: 1})
		if err != nil || page.Total != 3 || len(page.Messages) != 1 || page.Messages[0].LocalID != int64(i+1) {
			t.Fatalf("page %d=%+v,%v", i, page, err)
		}
	}
	data, err := QueryDatabase(db, QueryOptions{Keyword: "%_"})
	if err != nil || data.Total != 1 {
		t.Fatalf("keyword used wildcards: %+v,%v", data, err)
	}
	data, err = QueryDatabase(db, QueryOptions{Offset: 100})
	if err != nil || data.Total != 3 || len(data.Messages) != 0 {
		t.Fatalf("offset=%+v,%v", data, err)
	}
}

func TestShortenedSnapshotRetainsArchivedRecords(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "chat.json", `{"session_id":"one","messages":[{"create_time":1,"str_content":"same"},{"create_time":1,"str_content":"same"},{"create_time":2,"str_content":"later"}]}`)
	if _, err := Import(dir, db); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "chat.json", `{"session_id":"one","messages":[{"create_time":1,"str_content":"same"}]}`)
	result, err := Import(dir, db)
	if err != nil || result.Messages != 3 {
		t.Fatalf("shortened source deleted archive: %+v,%v", result, err)
	}
	if err := os.Remove(filepath.Join(dir, "chat.json")); err != nil {
		t.Fatal(err)
	}
	result, err = Import(dir, db)
	if err != nil || result.Messages != 3 {
		t.Fatalf("removed source deleted archive: %+v,%v", result, err)
	}
}

func TestUnrelatedOrFutureDatabaseIsNeverRewritten(t *testing.T) {
	for _, setup := range []string{`CREATE TABLE personal_notes(note TEXT); INSERT INTO personal_notes VALUES('keep')`, `PRAGMA application_id=1465401671; PRAGMA user_version=99;`} {
		dir := t.TempDir()
		dbPath := filepath.Join(t.TempDir(), "index.db")
		fixture(t, dir, "chat.json", `{"session_id":"one","messages":[]}`)
		db, err := openDatabase(dbPath, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(setup); err != nil {
			t.Fatal(err)
		}
		db.Close()
		original, _ := os.ReadFile(dbPath)
		if _, err := Import(dir, dbPath); err == nil {
			t.Fatal("unrelated or newer database accepted")
		}
		after, _ := os.ReadFile(dbPath)
		if !bytes.Equal(original, after) {
			t.Fatal("refused database changed")
		}
		if _, err := IndexStatus(dbPath); err == nil {
			t.Fatal("bad index status reported success")
		}
	}
}

func TestFirstImportFailureLeavesNoPartialDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "a.json", `{"session_id":"one","messages":[{"create_time":1,"str_content":"valid"}]}`)
	fixture(t, dir, "b.json", `invalid`)
	if _, err := Import(dir, db); err == nil {
		t.Fatal("bad first import accepted")
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("partial database left behind: %v", err)
	}
}

func TestContactRenameUpdatesMetadataWithoutDuplicatingIdentity(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(t.TempDir(), "index.db")
	fixture(t, dir, "chat.json", `{"session_id":"room","session_name":"Old room","messages":[{"create_time":10,"str_content":"hello","sender_wxid":"stable-id","sender_name":"Old name"},{"create_time":11,"str_content":"no sender id","sender_name":"Before"}]}`)
	if _, err := Import(dir, db); err != nil {
		t.Fatal(err)
	}
	fixture(t, dir, "chat.json", `{"session_id":"room","session_name":"New room","messages":[{"create_time":10,"str_content":"hello","sender_wxid":"stable-id","sender_name":"New name"},{"create_time":11,"str_content":"no sender id","sender_name":"After"}]}`)
	imported, err := Import(dir, db)
	if err != nil || imported.Messages != 2 {
		t.Fatalf("rename duplicated messages: %+v,%v", imported, err)
	}
	records, err := QueryDatabase(db, QueryOptions{})
	if err != nil || records.Messages[0].SenderName != "New name" || records.Messages[1].SenderName != "After" || records.Messages[0].DisplayName != "New room" {
		t.Fatalf("metadata was not updated: %+v,%v", records, err)
	}
}

func TestMalformedMessageRecordsAreReported(t *testing.T) {
	for _, messages := range []string{`[null]`, `[{}]`, `null`, `"not an array"`, `[{"formatted_time":"bad date"}]`} {
		dir := t.TempDir()
		db := filepath.Join(t.TempDir(), "index.db")
		fixture(t, dir, "chat.json", `{"session_id":"one","messages":`+messages+`}`)
		if _, err := Import(dir, db); err == nil || !strings.Contains(err.Error(), "chat.json") {
			t.Fatalf("malformed message accepted: %s, %v", messages, err)
		}
	}
}
