package wxdb

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/billyoftea/wxagent/go_backend/internal/state"
	"github.com/billyoftea/wxagent/go_backend/internal/wxdb/decrypt"
	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

// ExportConfig 瀵煎嚭閰嶇疆
type ExportConfig struct {
	LogWriter     io.Writer    // Progress only; default stderr keeps CLI stdout machine-readable.
	WeChatDataDir string       // 寰俊鏁版嵁鐩綍
	DBKey         string       // 鏁版嵁搴撳瘑閽?
	OutputDir     string       // 杈撳嚭鐩綍
	Sessions      []string     // 瑕佸鍑虹殑浼氳瘽ID锛屼负绌哄垯瀵煎嚭鎵€鏈?
	StartDate     string       // 寮€濮嬫棩鏈?YYYY-MM-DD
	EndDate       string       // 缁撴潫鏃ユ湡 YYYY-MM-DD
	Incremental   bool         // 澧為噺瀵煎嚭
	StateStore    *state.Store // 状态存储（用于增量导出）
}

// Message models the subset of fields required for export.
type Message struct {
	CreateTime    int64   `json:"create_time"`
	FormattedTime string  `json:"formatted_time"` // 可读时间格式
	Type          string  `json:"type"`           // readable type label
	StrContent    string  `json:"str_content"`
	IsSend        int     `json:"is_send"`               // 1=自己发送, 0=接收
	Sender        *string `json:"sender,omitempty"`      // "自己" 或 "对方"，群聊中对方发送的消息不输出此字段
	SenderName    string  `json:"sender_name,omitempty"` // 发送者名称（群聊中使用）
	SenderWxid    string  `json:"sender_wxid,omitempty"` // 发送者wxid（群聊中使用）
	IsGroupChat   bool    `json:"-"`                     // 内部标记，不输出到JSON
}

// messageTypeMap maps message type codes to human-readable labels.
var messageTypeMap = map[int64]string{
	1:             "文本消息",
	3:             "图片消息",
	34:            "语音消息",
	42:            "名片消息",
	43:            "视频消息",
	47:            "动画表情",
	48:            "位置消息",
	49:            "分享链接",
	10000:         "系统消息",
	10002:         "撤回消息",
	244813135921:  "引用消息",
	17179869233:   "卡片式链接",
	21474836529:   "图文消息",
	154618822705:  "小程序分享",
	12884901937:   "音乐卡片",
	8594229559345: "红包卡片",
	81604378673:   "聊天记录合并转发",
	266287972401:  "拍一拍消息",
	8589934592049: "转账卡片",
	270582939697:  "视频号直播卡片",
	25769803825:   "文件消息",
	227633266737:  "接龙消息",
}

// stringPtr 返回字符串的指针（用于可选的 sender 字段）
func stringPtr(s string) *string {
	return &s
}

// Session holds session metadata.
type Session struct {
	SessionID    string    `json:"session_id"`
	SessionName  string    `json:"session_name"`
	Messages     []Message `json:"messages"`
	ExportTime   string    `json:"export_time"`
	MessageCount int       `json:"message_count"`
}

// ContactInfo 鑱旂郴浜轰俊鎭?
type ContactInfo struct {
	UsrName  string
	NickName string
}

// Exporter 寰俊鏁版嵁搴撳鍑哄櫒
type Exporter struct {
	config ExportConfig
}

// NewExporter 鍒涘缓瀵煎嚭鍣?
func NewExporter(config ExportConfig) *Exporter {
	return &Exporter{config: config}
}

// Export 鎵ц瀵煎嚭
func (e *Exporter) Export() error {
	// 1. 获取当前用户的 wxid（用于判断 is_sender）
	selfWxid := e.getSelfWxid()
	if selfWxid != "" {
		fmt.Fprintf(e.logWriter(), "✓ Self wxid: %s\n", selfWxid)
	} else {
		fmt.Fprintln(e.logWriter(), "⚠️ Could not determine self wxid, is_sender will always be 0")
	}

	// 2. 鏌ユ壘鎵€鏈?message_*.db 鏂囦欢
	messageDBDir := filepath.Join(e.config.WeChatDataDir, "db_storage", "message")
	if _, err := os.Stat(messageDBDir); os.IsNotExist(err) {
		return fmt.Errorf("message database directory not found: %s", messageDBDir)
	}

	// 鎵弿鎵€鏈?message_*.db 鏂囦欢
	entries, err := os.ReadDir(messageDBDir)
	if err != nil {
		return fmt.Errorf("read message directory: %w", err)
	}

	var messageDBs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 匹配 message_0.db, message_1.db, biz_message_0.db, media_0.db 等
		// 排除 fts（全文搜索）和 resource（资源）数据库
		// media_*.db 也包含 Msg_ 表，必须扫描
		isMessageDB := (strings.HasPrefix(name, "message_") || strings.HasPrefix(name, "biz_message_") || strings.HasPrefix(name, "media_")) &&
			strings.HasSuffix(name, ".db") &&
			!strings.Contains(name, "fts") &&
			!strings.Contains(name, "resource")

		if isMessageDB {
			messageDBs = append(messageDBs, filepath.Join(messageDBDir, name))
		}
	}

	if len(messageDBs) == 0 {
		return fmt.Errorf("no message database files found in %s", messageDBDir)
	}

	fmt.Fprintf(e.logWriter(), "Found %d message database file(s)\n", len(messageDBs))

	// 2. 澶勭悊姣忎釜鏁版嵁搴撴枃浠?
	allSessions := make(map[string]bool)
	wxidCache := make(map[string]string) // Msg_<md5(wxid)> -> wxid cache
	contactNames := e.loadContactNames()
	if len(contactNames) > 0 {
		fmt.Fprintf(e.logWriter(), "Loaded %d contact display names\n", len(contactNames))
	} else {
		fmt.Fprintln(e.logWriter(), "Loaded 0 contact display names (will fall back to wxid)")
	}

	for _, dbPath := range messageDBs {
		fmt.Fprintf(e.logWriter(), "\nProcessing: %s\n", filepath.Base(dbPath))

		// 瑙ｅ瘑鏁版嵁搴?
		tempDBPath := filepath.Join(os.TempDir(), fmt.Sprintf("wxagent_decrypted_%s_%d.db",
			filepath.Base(dbPath), time.Now().Unix()))
		defer os.Remove(tempDBPath)

		fmt.Fprintln(e.logWriter(), "  Decrypting...")
		decryptor := decrypt.NewV4Decryptor()
		ctx := context.Background()

		if err := decryptor.DecryptToFile(ctx, dbPath, e.config.DBKey, tempDBPath); err != nil {
			fmt.Fprintf(e.logWriter(), "  [!] Failed to decrypt: %v\n", err)
			continue
		}

		fmt.Fprintln(e.logWriter(), "  鉁?Decrypted")

		// 鎵撳紑瑙ｅ瘑鍚庣殑鏁版嵁搴?
		db, err := sql.Open("sqlite", tempDBPath)
		if err != nil {
			fmt.Fprintf(e.logWriter(), "  [!] Failed to open: %v\n", err)
			continue
		}

		// 构建 Msg_<md5(wxid)> -> wxid 映射并累积
		fmt.Fprintln(e.logWriter(), "  Building wxid mapping...")
		wxidMap, err := e.buildWxidMap(db)
		if err != nil {
			fmt.Fprintf(e.logWriter(), "  [!] Failed to build wxid map: %v\n", err)
		} else {
			for table, wxid := range wxidMap {
				if _, exists := wxidCache[table]; !exists {
					wxidCache[table] = wxid
				}
			}
			fmt.Fprintf(e.logWriter(), "  [+] Mapped %d sessions (total %d)\n", len(wxidMap), len(wxidCache))
		}

		// 鍒楀嚭鎵€鏈夎〃
		tables, err := e.listTables(db)
		if err != nil {
			db.Close()
			fmt.Fprintf(e.logWriter(), "  [!] Failed to list tables: %v\n", err)
			continue
		}
		fmt.Fprintf(e.logWriter(), "  Tables: %v\n", tables)

		// 鏌ヨ浼氳瘽鍒楄〃
		sessions, err := e.getSessions(db)
		if err != nil {
			db.Close()
			fmt.Fprintf(e.logWriter(), "  [!] Failed to get sessions: %v\n", err)
			continue
		}

		fmt.Fprintf(e.logWriter(), "  Found %d sessions\n", len(sessions))

		// 导出会话 - 注意：同一会话的消息可能分布在多个数据库文件中
		// 不再跳过已见过的会话，而是合并来自不同数据库的消息
		for _, sessionID := range sessions {
			if err := e.exportSession(db, sessionID, wxidCache, contactNames, selfWxid); err != nil {
				fmt.Fprintf(e.logWriter(), "  [!] Failed to export %s: %v\n", sessionID, err)
				continue
			}
			allSessions[sessionID] = true
		}

		db.Close()
	}

	fmt.Fprintf(e.logWriter(), "\n[OK] Exported %d unique sessions\n", len(allSessions))

	// 保存状态（用于增量导出）
	if e.config.Incremental && e.config.StateStore != nil {
		if err := e.config.StateStore.Save(); err != nil {
			fmt.Fprintf(e.logWriter(), "⚠️ Failed to save export state: %v\n", err)
		} else {
			fmt.Fprintln(e.logWriter(), "✓ Export state saved")
		}
	}

	return nil
}

func (e *Exporter) getSessions(db *sql.DB) ([]string, error) {
	// 濡傛灉鎸囧畾浜嗕細璇濆垪琛紝鐩存帴杩斿洖
	if len(e.config.Sessions) > 0 {
		return e.config.Sessions, nil
	}

	// 寰俊浣跨敤 Msg_{md5(wxid)} 浣滀负琛ㄥ悕锛屾墍浠ユ垜浠煡璇㈡墍鏈?Msg_ 寮€澶寸殑琛?
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Msg_%' ORDER BY name`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			continue
		}
		// 琛ㄥ悕鏍煎紡: Msg_<md5>锛屾垜浠殏鏃剁敤琛ㄥ悕浣滀负 session ID
		sessions = append(sessions, tableName)
	}

	return sessions, nil
}

func (e *Exporter) exportSession(db *sql.DB, tableName string, wxidMap map[string]string, contactNames map[string]string, selfWxid string) error {
	// tableName 格式: Msg_<md5(wxid)>
	// 从wxidMap获取wxid
	sessionID := wxidMap[tableName]
	if sessionID == "" {
		sessionID = tableName // 降级使用表名
	}

	// 判断是否为群聊
	isGroupChat := strings.HasSuffix(sessionID, "@chatroom")

	// 获取当前用户在 Name2Id 表中的 rowid（用于判断 is_sender）
	// 这是 echotrace 的关键逻辑：real_sender_id 是 Name2Id 的 rowid
	var myRowid int64 = -1
	if selfWxid != "" {
		row := db.QueryRow("SELECT rowid FROM Name2Id WHERE user_name = ?", selfWxid)
		if err := row.Scan(&myRowid); err == nil {
			fmt.Fprintf(e.logWriter(), "  [DEBUG] 当前用户 %s 在 Name2Id 中的 rowid: %d\n", selfWxid, myRowid)
		}
	}

	// 获取显示名称
	sessionName := sessionID
	if name := contactNames[sessionID]; name != "" {
		sessionName = name
	}

	// 如果还是没找到名称，可能是因为sessionID有@chatroom后缀
	// 尝试移除后缀再查找
	if sessionName == sessionID && strings.HasSuffix(sessionID, "@chatroom") {
		baseID := strings.TrimSuffix(sessionID, "@chatroom")
		if name := contactNames[baseID]; name != "" {
			sessionName = name
		}
	}

	// 增量导出：获取上次导出的最后时间戳
	lastTimestamp := int64(0)
	if e.config.Incremental && e.config.StateStore != nil {
		lastTimestamp = e.config.StateStore.LastTimestamp(sessionID)
		if lastTimestamp > 0 {
			fmt.Fprintf(e.logWriter(), "  [Incremental] Session %s: last timestamp %d\n", sessionID, lastTimestamp)
		}
	}

	// 构建查询，增量模式下只查询新消息
	// 注意：bytes_extra 字段可能不存在，所以我们不直接查询它
	var query string
	if lastTimestamp > 0 {
		query = fmt.Sprintf(`
			SELECT 
				create_time,
				local_type,
				message_content,
				compress_content,
				real_sender_id
			FROM %s
			WHERE create_time > %d
			ORDER BY create_time ASC
		`, tableName, lastTimestamp)
	} else {
		query = fmt.Sprintf(`
			SELECT 
				create_time,
				local_type,
				message_content,
				compress_content,
				real_sender_id
			FROM %s
			ORDER BY create_time ASC
		`, tableName)
	}

	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	maxTimestamp := lastTimestamp

	for rows.Next() {
		var msg Message
		var realSenderID sql.NullInt64
		var msgType int64
		var rawContent sql.RawBytes
		var compressedContent sql.RawBytes

		if err := rows.Scan(&msg.CreateTime, &msgType, &rawContent, &compressedContent, &realSenderID); err != nil {
			continue
		}
		decodedContent := e.decodeMessageContent(cloneBytes(rawContent), cloneBytes(compressedContent), msgType)

		// 格式化时间戳为可读格式
		msg.FormattedTime = time.Unix(msg.CreateTime, 0).Format("2006-01-02 15:04:05")

		// Translate message type to a readable label
		msg.Type = getMessageTypeName(msgType)

		// 问题2：先处理群聊消息中的发送者信息（在解析内容之前）
		// 因为某些消息类型（如引用消息、图文消息）会在 parseMessageContent 中
		// 提取标题并移除原始的 wxid 前缀，导致后续无法提取发送者信息
		var extractedContent string // 提取的纯内容（移除 wxid 前缀）
		if isGroupChat && decodedContent != "" {
			// 解析群聊消息格式: "wxid:\n消息内容"
			senderWxid, content := e.parseGroupMessage(decodedContent)
			if senderWxid != "" {
				msg.SenderWxid = senderWxid
				extractedContent = content // 保存移除 wxid 后的内容
				msg.IsGroupChat = true

				// 获取发送者名称
				if name := contactNames[senderWxid]; name != "" {
					msg.SenderName = name
				} else {
					msg.SenderName = senderWxid
				}

				// 群聊中判断是否是自己发的
				// 只有自己发的消息才设置 sender 字段
				if selfWxid != "" && senderWxid == selfWxid {
					msg.Sender = stringPtr("自己")
					msg.IsSend = 1
				}
				// 对方发送的消息不设置 sender 字段（保持为 nil）
			}
		}

		// 解析消息内容 - 根据消息类型处理
		// 对于群聊消息，如果已经提取了 wxid，使用移除前缀后的内容
		// 否则使用原始内容（parseMessageContent 会处理 wxid 移除）
		contentToParse := decodedContent
		if extractedContent != "" {
			contentToParse = extractedContent
		}
		msg.StrContent = e.parseMessageContent(msgType, contentToParse, isGroupChat, selfWxid)
		if msgType == 1 && e.isXMLContent(decodedContent) {
			msg.Type = "图文消息"
		}

		// 问题3：判断 sender（参考 echotrace 的实现）
		// 对于非群聊消息，或者群聊中未提取到 wxid 的消息
		// real_sender_id 是 Name2Id 表中的 rowid
		// 如果 real_sender_id == 当前用户的rowid，则是自己发的
		if msg.Sender == nil {
			if realSenderID.Valid && myRowid >= 0 {
				if realSenderID.Int64 == myRowid {
					// 自己发的消息
					msg.IsSend = 1
					if !isGroupChat {
						// 私聊中自己的消息：sender_name = "自己"
						msg.SenderName = "自己"
					} else {
						// 群聊中自己的消息：sender = "自己"
						msg.Sender = stringPtr("自己")
					}
				} else {
					if !isGroupChat {
						// 私聊中对方的消息：sender_name = session_name（对方名称）
						msg.SenderName = sessionName
					}
					// 群聊中对方的消息不设置 sender（保持为 nil）
				}
			} else if !realSenderID.Valid {
				// real_sender_id 为 NULL 的情况
				// 在私聊中，NULL 通常表示是自己发的
				// 在群聊中，NULL 表示系统消息或特殊消息
				if !isGroupChat {
					msg.SenderName = "自己"
					msg.IsSend = 1
				}
				// 群聊中 NULL 的情况不设置 sender
			}
		}

		// 处理特殊类型消息
		if msg.Type == "未知" {
			msg.StrContent = "[不支持的消息类型]"
		}

		// 处理未知类型消息，统一转换为微信小程序
		if strings.HasPrefix(msg.Type, "未知类型") {
			msg.Type = "微信小程序"
			msg.StrContent = "微信小程序"
		}

		messages = append(messages, msg)

		// 跟踪最大时间戳
		if msg.CreateTime > maxTimestamp {
			maxTimestamp = msg.CreateTime
		}
	}

	if len(messages) == 0 {
		// 没有新消息
		if lastTimestamp > 0 {
			fmt.Fprintf(e.logWriter(), "  [OK] %s: No new messages (incremental)\n", sessionID)
		}
		return nil
	}

	// 合并来自不同数据库的消息
	// 注意：同一会话的消息可能分布在多个数据库文件中（如 message_0.db, message_1.db 等）
	// 因此需要检查输出文件是否已存在，如果存在则合并消息
	fileName := sanitizeFilename(sessionName)
	if fileName == "" {
		fileName = sessionID
	} else if fileName != sessionID && sessionID != "" {
		fileName = fmt.Sprintf("%s_%s", fileName, sessionID)
	}
	// 保存到导出目录下的 chat_history 子目录，便于前端统一读取历史记录
	outputPath := filepath.Join(e.config.OutputDir, "chat_history", fileName+".json")

	existingSession, err := e.loadExistingSession(outputPath)
	if err == nil && existingSession != nil && len(existingSession.Messages) > 0 {
		// 合并消息：使用 map 去重（按 create_time 去重）
		existingMsgMap := make(map[int64]bool)
		for _, msg := range existingSession.Messages {
			existingMsgMap[msg.CreateTime] = true
		}

		// 只添加新消息（不在已有消息中的）
		newMessages := make([]Message, 0)
		for _, msg := range messages {
			if !existingMsgMap[msg.CreateTime] {
				newMessages = append(newMessages, msg)
			}
		}

		// 合并并按时间排序
		allMessages := append(existingSession.Messages, newMessages...)
		// 按 create_time 排序
		sort.Slice(allMessages, func(i, j int) bool {
			return allMessages[i].CreateTime < allMessages[j].CreateTime
		})
		messages = allMessages

		fmt.Fprintf(e.logWriter(), "  [MERGE] %s: %d existing + %d new = %d total\n",
			sessionID, len(existingSession.Messages), len(newMessages), len(messages))
	}

	// Build JSON output
	session := Session{
		SessionID:    sessionID,
		SessionName:  sessionName,
		Messages:     messages,
		ExportTime:   time.Now().Format(time.RFC3339),
		MessageCount: len(messages),
	}

	// 保存到文件（outputPath 已在上面定义）
	if err := e.saveSession(session, outputPath); err != nil {
		return err
	}

	// 更新状态（记录最新的时间戳）
	if e.config.Incremental && e.config.StateStore != nil && maxTimestamp > 0 {
		e.config.StateStore.UpdateSession(sessionID, maxTimestamp, len(messages))
	}

	fmt.Fprintf(e.logWriter(), "  [OK] Exported %s: %d messages\n", sessionID, len(messages))
	return nil
}

// loadExistingSession 从文件加载已有的会话数据（用于增量导出合并）
func (e *Exporter) loadExistingSession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}

	return &session, nil
}

func (e *Exporter) saveSession(session Session, path string) error {
	// 纭繚杈撳嚭鐩綍瀛樺湪
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	// 鍒涘缓鏂囦欢
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// 浣跨敤 Encoder 鍐欏叆 JSON,淇濈暀涓枃瀛楃涓嶈浆涔?
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(session)
}

func (e *Exporter) listTables(db *sql.DB) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			continue
		}
		tables = append(tables, tableName)
	}

	return tables, nil
}

// buildWxidMap 浠?Name2Id 琛ㄦ瀯寤鸿〃鍚嶅埌wxid鐨勬槧灏?
// 琛ㄥ悕鏍煎紡: Msg_<MD5(wxid)>

// loadContactNames loads wxid -> display name (remark/nickname/alias) from contact databases.
func (e *Exporter) loadContactNames() map[string]string {
	results := make(map[string]string)
	if e.config.WeChatDataDir == "" {
		return results
	}

	_ = filepath.WalkDir(e.config.WeChatDataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if !strings.HasSuffix(lower, ".db") {
			return nil
		}
		if !strings.Contains(lower, "contact") {
			return nil
		}
		e.loadContactFile(path, results)
		return nil
	})
	return results
}

func (e *Exporter) loadContactFile(path string, results map[string]string) {
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("wxagent_contact_%d.db", time.Now().UnixNano()))
	defer os.Remove(tempPath)

	decryptor := decrypt.NewV4Decryptor()
	ctx := context.Background()
	if err := decryptor.DecryptToFile(ctx, path, e.config.DBKey, tempPath); err != nil {
		return
	}

	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return
	}
	defer db.Close()

	tables := []string{"contact", "Contact"}
	for _, table := range tables {
		rows, err := db.Query(fmt.Sprintf("SELECT username, remark, nick_name, alias FROM %s", table))
		if err != nil {
			continue
		}
		for rows.Next() {
			var username, remark, nick, alias sql.NullString
			if err := rows.Scan(&username, &remark, &nick, &alias); err != nil {
				continue
			}
			user := strings.TrimSpace(username.String)
			if user == "" {
				continue
			}
			display := firstNonEmpty(strings.TrimSpace(remark.String), strings.TrimSpace(nick.String), strings.TrimSpace(alias.String))
			if display == "" {
				continue
			}
			if _, exists := results[user]; !exists {
				results[user] = display
			}
		}
		rows.Close()
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func (e *Exporter) buildWxidMap(db *sql.DB) (map[string]string, error) {
	wxidMap := make(map[string]string)

	// 浠嶯ame2Id鑾峰彇鎵€鏈墂xid
	rows, err := db.Query(`SELECT user_name FROM Name2Id`)
	if err != nil {
		return nil, fmt.Errorf("query Name2Id: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var wxid string
		if err := rows.Scan(&wxid); err != nil {
			continue
		}

		// 璁＄畻MD5
		hash := md5.Sum([]byte(wxid))
		md5Str := hex.EncodeToString(hash[:])
		tableName := "Msg_" + md5Str

		wxidMap[tableName] = wxid
	}

	return wxidMap, nil
}

// getMessageTypeName converts the numeric message type into a readable label.
func getMessageTypeName(typeCode int64) string {
	if name, ok := messageTypeMap[typeCode]; ok {
		return name
	}
	return fmt.Sprintf("\u672a\u77e5\u7c7b\u578b(%d)", typeCode)
}

// sanitizeFilename removes Windows-invalid characters from a name used for output files.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"<", "_",
		">", "_",
		":", "_",
		"\"", "_",
		"/", "_",
		"\\", "_",
		"|", "_",
		"?", "_",
		"*", "_",
	)
	clean := strings.TrimSpace(replacer.Replace(name))
	return clean
}

// getSelfWxid 获取当前用户的 wxid
func (e *Exporter) getSelfWxid() string {
	// 尝试从配置文件或数据库中获取当前用户的 wxid
	// 方法1: 从 db_storage/account 目录下的数据库中查找
	accountDBDir := filepath.Join(e.config.WeChatDataDir, "db_storage", "account")

	entries, err := os.ReadDir(accountDBDir)
	if err != nil {
		fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Failed to read account dir: %v, trying fallback method\n", err)
		// 不要直接返回,继续尝试方法2
	} else {
		// account 目录存在,尝试读取数据库
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
				continue
			}

			dbPath := filepath.Join(accountDBDir, entry.Name())
			tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("wxagent_account_%d.db", time.Now().UnixNano()))
			defer os.Remove(tempPath)

			decryptor := decrypt.NewV4Decryptor()
			ctx := context.Background()
			if err := decryptor.DecryptToFile(ctx, dbPath, e.config.DBKey, tempPath); err != nil {
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Failed to decrypt %s: %v\n", entry.Name(), err)
				continue
			}

			db, err := sql.Open("sqlite", tempPath)
			if err != nil {
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Failed to open decrypted db: %v\n", err)
				continue
			}

			// 尝试查询账号信息
			var wxid string
			err = db.QueryRow("SELECT user_name FROM account LIMIT 1").Scan(&wxid)
			db.Close()

			if err == nil && wxid != "" {
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Found wxid from account db: %s\n", wxid)
				return wxid
			} else if err != nil {
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Query failed: %v\n", err)
			}
		}
	}

	// 方法2: 从数据目录名推断（通常格式为 wxid_xxx 或 wxid_xxx_yyy）
	dirName := filepath.Base(e.config.WeChatDataDir)
	fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Trying to extract wxid from directory name: %s\n", dirName)

	// 移除可能的后缀（如 _937e）
	if strings.HasPrefix(dirName, "wxid_") {
		// 尝试提取 wxid（移除最后的 _xxx 后缀）
		parts := strings.Split(dirName, "_")
		if len(parts) >= 2 {
			// 如果最后一部分是纯数字/字母且长度较短,可能是账号后缀,去掉
			// 否则保留完整的 wxid
			lastPart := parts[len(parts)-1]
			if len(lastPart) <= 6 && !strings.Contains(lastPart, "22") {
				// 去掉最后一部分(通常是随机后缀)
				wxid := strings.Join(parts[:len(parts)-1], "_")
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Using wxid from directory name (trimmed suffix): %s\n", wxid)
				return wxid
			} else {
				// 保留完整目录名作为 wxid
				fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Using full directory name as wxid: %s\n", dirName)
				return dirName
			}
		}
	}

	fmt.Fprintf(e.logWriter(), "[DEBUG getSelfWxid] Failed to determine wxid\n")
	return ""
}

// decodeMessageContent 统一解码 message_content / compress_content 字段
// 策略：对于文本/XML类消息，优先使用 message_content（包含原始文本）
//
//	只有 message_content 为空时，才尝试解压 compress_content
func (e *Exporter) decodeMessageContent(rawContent []byte, compressedContent []byte, msgType int64) string {
	// 对于文本类消息（type 1）和富文本消息（图文、链接等），优先使用 message_content
	// 因为这些消息的原始内容（XML/纯文本）通常存储在 message_content 中
	// compress_content 可能包含序列化的元数据（如标签信息等），会导致乱码
	isTextOrRichText := msgType == 1 || // 文本消息
		msgType == 49 || // 分享链接
		msgType == 17179869233 || // 卡片式链接
		msgType == 154618822705 || // 小程序分享
		msgType == 244813135921 // 引用消息
		// NOTE: 图文消息(21474836529)不在此列表,它使用compress_content

	if isTextOrRichText {
		// 文本类消息：优先使用 message_content
		if len(rawContent) > 0 {
			// 注意：某些文本消息的 message_content 也可能是 zstd 压缩的
			// 所以也需要用 tryDecodeBinaryContent 处理
			decoded := e.tryDecodeBinaryContent(rawContent)
			if decoded != "" {
				return decoded
			}
		}
		// message_content 为空时才尝试 compress_content
		if len(compressedContent) > 0 {
			if decoded := e.tryDecodeBinaryContent(compressedContent); decoded != "" {
				return decoded
			}
		}
	} else {
		// 多媒体消息：优先使用 compress_content（可能是 zstd 压缩的二进制）
		if len(compressedContent) > 0 {
			if decoded := e.tryDecodeBinaryContent(compressedContent); decoded != "" {
				return decoded
			}
		}
		// compress_content 为空时才使用 message_content
		// 注意：message_content 也可能是二进制(zstd压缩)，所以也要用 tryDecodeBinaryContent
		if len(rawContent) > 0 {
			return e.tryDecodeBinaryContent(rawContent)
		}
	}

	return ""
}

var (
	zstdDecoder     *zstd.Decoder
	zstdDecoderOnce sync.Once
	zstdDecoderErr  error
)

func getZstdDecoder() (*zstd.Decoder, error) {
	zstdDecoderOnce.Do(func() {
		zstdDecoder, zstdDecoderErr = zstd.NewReader(nil)
	})
	return zstdDecoder, zstdDecoderErr
}

func (e *Exporter) tryDecodeBinaryContent(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// 先尝试 zstd 解压
	if isZstdMagic(data) {
		if decoder, err := getZstdDecoder(); err == nil {
			if decoded, err := decoder.DecodeAll(data, nil); err == nil {
				return e.decodeBytesToString(decoded)
			}
		}
	}

	// 直接按 UTF-8/容错方式解码
	return e.decodeBytesToString(data)
}

func isZstdMagic(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	magic := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	return magic == 0xFD2FB528 || magic == 0x28B52FFD
}

func (e *Exporter) decodeBytesToString(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if utf8.Valid(data) {
		return string(data)
	}

	// 尝试容错 UTF-8 解码
	utf8Decoded := string(bytes.Runes(data))
	replacementCount := strings.Count(utf8Decoded, "\ufffd")
	if len(utf8Decoded) > 0 && replacementCount < len(utf8Decoded)/5 {
		// 替换符占比 <20%，去掉替换符后返回
		return strings.ReplaceAll(utf8Decoded, "\ufffd", "")
	}

	// 如果UTF-8解码失败太多，尝试提取可读片段
	extracted := e.extractReadableUTF8(data)
	if extracted != "" {
		return extracted
	}

	// 最后尝试 Latin1 解码 (ISO-8859-1) - 与echotrace一致
	// Latin1将每个字节直接映射为Unicode码点0-255
	latin1Decoded := make([]rune, len(data))
	for i, b := range data {
		latin1Decoded[i] = rune(b)
	}
	return string(latin1Decoded)
}

// extractReadableUTF8 尝试从混合数据中提取可读的UTF-8文本片段
func (e *Exporter) extractReadableUTF8(data []byte) string {
	var segments []string
	var currentSegment []byte

	for i := 0; i < len(data); {
		// 尝试解码一个UTF-8字符
		r, size := utf8.DecodeRune(data[i:])

		if r == utf8.RuneError && size == 1 {
			// 解码失败，保存当前段落
			if len(currentSegment) >= 10 { // 只保留长度>=10的片段
				segments = append(segments, string(currentSegment))
			}
			currentSegment = currentSegment[:0]
			i++
			continue
		}

		// 检查是否是可显示字符、中文或常用空白字符
		if (r >= 32 && r < 127 && r != 127) || // ASCII可见字符
			(r >= 0x4E00 && r <= 0x9FFF) || // 中文常用字
			(r >= 0x3000 && r <= 0x303F) || // 中文标点
			r == '\t' || r == '\n' || r == '\r' || r == ' ' {
			currentSegment = append(currentSegment, data[i:i+size]...)
		} else {
			// 遇到不可见控制字符，结束当前段落
			if len(currentSegment) >= 10 {
				segments = append(segments, string(currentSegment))
			}
			currentSegment = currentSegment[:0]
		}

		i += size
	}

	// 处理最后的段落
	if len(currentSegment) >= 10 {
		segments = append(segments, string(currentSegment))
	}

	// 如果有多个段落，用空格连接
	if len(segments) > 0 {
		// 取最长的段落，或者连接所有段落
		if len(segments) == 1 {
			return strings.TrimSpace(segments[0])
		}
		// 多个段落时，找最长的
		maxLen := 0
		maxIdx := 0
		for i, seg := range segments {
			if len(seg) > maxLen {
				maxLen = len(seg)
				maxIdx = i
			}
		}
		// 如果最长段落明显比其他长，就只用它
		if maxLen > 50 {
			return strings.TrimSpace(segments[maxIdx])
		}
		// 否则连接所有段落
		return strings.TrimSpace(strings.Join(segments, " "))
	}

	return ""
}

func (e *Exporter) isXMLContent(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	return (strings.Contains(lower, "<msg>") ||
		strings.Contains(lower, "<appmsg") ||
		strings.Contains(lower, "<xml") ||
		strings.Contains(lower, "<?xml") ||
		strings.Contains(lower, "<title>"))
}

// decodeHTMLEntities 解码HTML实体字符（参考echotrace实现）
func (e *Exporter) decodeHTMLEntities(input string) string {
	if input == "" {
		return input
	}

	result := input
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", "\"")
	result = strings.ReplaceAll(result, "&apos;", "'")
	result = strings.ReplaceAll(result, "&#x20;", " ")
	result = strings.ReplaceAll(result, "&#x0A;", "\n")
	result = strings.ReplaceAll(result, "&#x0D;", "\r")

	return result
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}

// parseGroupMessage 解析群聊消息，提取发送者wxid和消息内容
// 群聊消息格式: "wxid:\n消息内容"
func (e *Exporter) parseGroupMessage(content string) (wxid string, message string) {
	// 查找第一个冒号和换行符的位置
	colonIdx := strings.Index(content, ":")
	if colonIdx == -1 {
		return "", content
	}

	newlineIdx := strings.Index(content, "\n")
	if newlineIdx == -1 || newlineIdx <= colonIdx {
		return "", content
	}

	// 提取可能的 wxid
	possibleWxid := strings.TrimSpace(content[:colonIdx])

	// 验证是否是有效的 wxid 格式
	// wxid 通常以 wxid_ 开头，或者是纯数字（QQ号），或者包含 @
	// 重要：必须是可打印的ASCII字符，不包含Unicode替代字符(\ufffd)
	if len(possibleWxid) > 0 && !strings.Contains(possibleWxid, "\ufffd") {
		if strings.HasPrefix(possibleWxid, "wxid_") ||
			strings.Contains(possibleWxid, "@") ||
			isNumeric(possibleWxid) {
			// 额外验证：wxid 不应该以特殊符号开头
			firstChar := possibleWxid[0]
			if (firstChar >= 'a' && firstChar <= 'z') ||
				(firstChar >= 'A' && firstChar <= 'Z') ||
				(firstChar >= '0' && firstChar <= '9') {
				return possibleWxid, strings.TrimSpace(content[newlineIdx+1:])
			}
		}
	}

	return "", content
}

// isNumeric 检查字符串是否全为数字
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// parseMessageContent parses message content based on message type, following echotrace's approach
func (e *Exporter) parseMessageContent(msgType int64, content string, isGroupChat bool, selfWxid string) string {
	// Decode HTML entities first (like echotrace does)
	decoded := e.decodeHTMLEntities(content)
	hasXML := e.isXMLContent(decoded)

	// Direct parsing by message type (no binary garbage check)
	switch msgType {
	case 10000: // 系统消息
		// 系统消息通常是加群、退群等操作通知
		return "[系统消息]"

	case 47: // 动画表情
		return "[表情消息]"

	case 3: // 图片
		return "[图片消息]"

	case 34: // 语音
		return "[语音消息]"

	case 43: // 视频
		return "[视频消息]"

	case 48: // 位置
		return "[位置消息]"

	case 42: // 名片
		return "[名片消息]"

	case 49: // Link/article
		return e.extractXMLTitle(decoded, "[链接]")

	case 17179869233: // Mini program
		return e.extractXMLTitle(decoded, "[小程序]")

	case 21474836529: // Rich text / Graph message
		return e.extractXMLTitle(decoded, "[图文消息]")

	case 227633266737: // Solitaire message
		extracted := e.extractSolitaireContent(decoded)
		if extracted != "" {
			return extracted
		}
		return "[接龙消息]"

	case 154618822705: // 小程序分享
		return e.extractXMLTitle(decoded, "[小程序]")

	case 12884901937: // 音乐卡片
		return "[音乐]"

	case 8594229559345: // 红包卡片
		return "[红包]"

	case 81604378673: // 聊天记录合并转发
		return "[聊天记录]"

	case 266287972401: // 拍一拍消息
		return "[拍一拍]"

	case 8589934592049: // 转账卡片
		return "[转账]"

	case 270582939697: // 视频号直播卡片
		return "[视频号直播]"

	case 25769803825: // 文件消息
		return e.extractXMLTitle(decoded, "[文件]")

	case 244813135921: // 引用消息
		return e.extractQuoteContent(decoded)

	case 1: // 文本消息
		// 如果是XML内容，按特殊消息/图文消息处理
		if hasXML {
			if title := e.extractXMLTitle(decoded, ""); title != "" {
				return fmt.Sprintf("[图文] %s", title)
			}
			if cdata := e.extractCDATA(decoded); cdata != "" {
				return cdata
			}
			return "[图文消息]"
		}
		// 普通文本消息，直接清理并返回
		// 注意：调用处已经移除了群聊消息的 wxid 前缀
		return e.cleanTextContent(decoded)

	default:
		// 未知类型消息
		if len(decoded) > 0 && !strings.Contains(decoded, "�") {
			return e.cleanTextContent(decoded)
		}
		return "[不支持的消息类型]"
	}
}

// extractCDATA extracts CDATA content from XML (case-insensitive)
func (e *Exporter) extractCDATA(xml string) string {
	lowerXml := strings.ToLower(xml)
	startTag := "<![cdata["
	endTag := "]]>"

	startIdx := strings.Index(lowerXml, startTag)
	if startIdx == -1 {
		return ""
	}

	actualStart := startIdx + len(startTag)
	endIdx := strings.Index(lowerXml[actualStart:], endTag)
	if endIdx == -1 {
		return ""
	}

	// 从原始XML中提取内容（保持原始大小写）
	content := xml[actualStart : actualStart+endIdx]
	return strings.TrimSpace(e.cleanTextContent(content))
}

// extractXMLTitle 从XML内容中提取title标签（大小写不敏感，参考echotrace）
func (e *Exporter) extractXMLTitle(content string, fallback string) string {
	// 先进行HTML实体解码
	decodedContent := e.decodeHTMLEntities(content)

	// 转为小写进行大小写不敏感搜索
	lowerContent := strings.ToLower(decodedContent)
	startTag := "<title>"
	endTag := "</title>"

	// 查找 <title> 标签（大小写不敏感）
	titleStart := strings.Index(lowerContent, startTag)
	if titleStart == -1 {
		// 尝试查找 <title><![CDATA[ 格式
		cdataStartTag := "<title><![cdata["
		cdataEndTag := "]]></title>"

		titleStart = strings.Index(lowerContent, cdataStartTag)
		if titleStart != -1 {
			titleEnd := strings.Index(lowerContent[titleStart:], cdataEndTag)
			if titleEnd != -1 {
				// 从原始内容中提取（保持原始大小写）
				actualStart := titleStart + len(cdataStartTag)
				actualEnd := titleStart + titleEnd
				if actualEnd <= len(decodedContent) {
					title := decodedContent[actualStart:actualEnd]
					cleaned := e.cleanTextContent(title)
					if cleaned != "" {
						return cleaned
					}
				}
			}
		}
		return fallback
	}

	// 查找结束标签
	titleEnd := strings.Index(lowerContent[titleStart:], endTag)
	if titleEnd == -1 {
		return fallback
	}

	// 从原始内容中提取标题（保持原始大小写和字符）
	actualStart := titleStart + len(startTag)
	actualEnd := titleStart + titleEnd
	if actualEnd > len(decodedContent) {
		return fallback
	}

	title := decodedContent[actualStart:actualEnd]

	// 移除CDATA标记（使用正则表达式，更健壮）
	title = strings.ReplaceAll(title, "<![CDATA[", "")
	title = strings.ReplaceAll(title, "]]>", "")
	// 也处理小写和大小写混合的情况
	title = strings.ReplaceAll(title, "<![cdata[", "")
	title = strings.ReplaceAll(title, "]]>", "")

	cleaned := e.cleanTextContent(title)
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

// extractSolitaireContent 提取接龙消息的内容
func (e *Exporter) extractSolitaireContent(content string) string {
	// 接龙消息的格式通常在XML的title或appmsg标签中
	// 先尝试提取title
	title := e.extractXMLTitle(content, "")
	if title != "" && title != "[接龙消息]" {
		return title
	}

	// 尝试查找接龙的文本内容（通常在<content>或<![CDATA[中）
	cdataStart := strings.Index(content, "<![CDATA[")
	if cdataStart != -1 {
		cdataEnd := strings.Index(content[cdataStart:], "]]>")
		if cdataEnd != -1 {
			text := content[cdataStart+9 : cdataStart+cdataEnd]
			cleaned := e.cleanTextContent(text)
			// 接龙内容通常包含"接龙"字样
			if strings.Contains(cleaned, "接龙") || strings.Contains(cleaned, "确认") {
				return cleaned
			}
		}
	}

	return ""
}

// extractQuoteContent 提取引用消息的内容
func (e *Exporter) extractQuoteContent(content string) string {
	// 引用消息通常有实际的文本内容和被引用的内容
	// 尝试提取主要文本
	title := e.extractXMLTitle(content, "")
	if title != "" && title != "[引用消息]" {
		return title
	}

	// 如果提取失败，返回默认标签
	return "[引用消息]"
}

// cleanTextContent 清理文本内容，移除不可打印字符
func (e *Exporter) cleanTextContent(content string) string {
	if content == "" {
		return ""
	}

	// 移除wxid前缀（如果存在）
	wxidPattern := "wxid_"
	colonIdx := strings.Index(content, ":")
	if colonIdx != -1 && colonIdx < 50 {
		prefix := content[:colonIdx]
		if strings.HasPrefix(prefix, wxidPattern) || strings.Contains(prefix, "@") {
			newlineIdx := strings.Index(content, "\n")
			if newlineIdx != -1 && newlineIdx > colonIdx {
				content = content[newlineIdx+1:]
			}
		}
	}

	return strings.TrimSpace(content)
}

func (e *Exporter) logWriter() io.Writer {
	if e.config.LogWriter != nil {
		return e.config.LogWriter
	}
	return os.Stderr
}
