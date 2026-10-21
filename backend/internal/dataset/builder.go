package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/state"
)

// Message 统一的消息结构
type Message struct {
	LocalID           int64  `json:"localId"`
	CreateTime        int64  `json:"createTime,omitempty"`
	CreateTimeAlt     int64  `json:"create_time,omitempty"` // echotrace 格式
	FormattedTime     string `json:"formattedTime,omitempty"`
	FormattedTimeAlt  string `json:"formatted_time,omitempty"` // echotrace 格式
	Type              string `json:"type"`
	LocalType         int    `json:"localType"`
	Content           string `json:"content,omitempty"`
	StrContent        string `json:"str_content,omitempty"` // 兼容 echotrace 格式
	IsSend            int    `json:"isSend"`
	SenderUsername    string `json:"senderUsername,omitempty"`
	SenderDisplayName string `json:"senderDisplayName,omitempty"`
	SenderName        string `json:"sender_name,omitempty"` // 兼容 echotrace 格式
	SenderWxid        string `json:"sender_wxid,omitempty"` // 兼容 echotrace 格式
}

// GetContent 获取消息内容，兼容两种格式
func (m *Message) GetContent() string {
	if m.Content != "" {
		return m.Content
	}
	return m.StrContent
}

// GetSenderName 获取发送者名称，兼容两种格式
func (m *Message) GetSenderName() string {
	if m.SenderDisplayName != "" {
		return m.SenderDisplayName
	}
	if m.SenderName != "" {
		return m.SenderName
	}
	if m.SenderUsername != "" {
		return m.SenderUsername
	}
	return m.SenderWxid
}

// GetCreateTime 获取创建时间戳，兼容两种格式
func (m *Message) GetCreateTime() int64 {
	if m.CreateTime > 0 {
		return m.CreateTime
	}
	if m.CreateTimeAlt > 0 {
		return m.CreateTimeAlt
	}
	return 0
}

// GetFormattedTime 获取格式化时间，兼容两种格式
func (m *Message) GetFormattedTime() string {
	if m.FormattedTime != "" {
		return m.FormattedTime
	}
	return m.FormattedTimeAlt
}

type SessionInfo struct {
	WxID        string `json:"wxid"`
	Nickname    string `json:"nickname"`
	Remark      string `json:"remark"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
}

type ExportMetadata struct {
	TotalMessageCount int64  `json:"totalMessageCount"`
	LastExportTime    string `json:"lastExportTime"`
}

// ChatLog 原有格式（Go 后端生成）
type ChatLog struct {
	Session        SessionInfo    `json:"session"`
	Messages       []Message      `json:"messages"`
	ExportMetadata ExportMetadata `json:"exportMetadata"`
}

// EchoTraceChatLog echotrace 导出格式
type EchoTraceChatLog struct {
	SessionID   string    `json:"session_id"`
	SessionName string    `json:"session_name"`
	Messages    []Message `json:"messages"`
}

type Builder struct {
	exportDir string
	state     *state.Store
}

func NewBuilder(exportDir string, s *state.Store) *Builder {
	return &Builder{
		exportDir: exportDir,
		state:     s,
	}
}

type BuildResult struct {
	Dataset map[string][]string // SessionName -> []MessageContent
	Stats   []SessionStat
}

type SessionStat struct {
	SessionID     string `json:"session_id"`
	DisplayName   string `json:"display_name"`
	Messages      int    `json:"messages"`
	LastTimestamp int64  `json:"last_timestamp"`
}

func (b *Builder) Build(startDate, endDate string, incremental bool, allowedSessions []string) (*BuildResult, error) {
	// 优先查找 exportDir/chat_history/*.json，向后兼容地回退到 exportDir/*.json
	files, err := filepath.Glob(filepath.Join(b.exportDir, "chat_history", "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob export dir (chat_history): %w", err)
	}
	if len(files) == 0 {
		rootFiles, err2 := filepath.Glob(filepath.Join(b.exportDir, "*.json"))
		if err2 != nil {
			return nil, fmt.Errorf("glob export dir: %w", err2)
		}
		files = rootFiles
	}

	startTs, endTs := parseDateRange(startDate, endDate)
	allowedSet := make(map[string]bool)
	for _, s := range allowedSessions {
		allowedSet[s] = true
	}

	dataset := make(map[string][]string)
	stats := []SessionStat{}

	for _, file := range files {
		if strings.HasSuffix(file, ".export_state") {
			continue
		}

		log, err := loadChatLog(file)
		if err != nil {
			// Skip malformed files
			continue
		}

		sessionName := log.Session.DisplayName
		if sessionName == "" {
			sessionName = log.Session.Nickname
		}
		if sessionName == "" {
			sessionName = "Unknown Session"
		}

		if len(allowedSet) > 0 && !allowedSet[sessionName] {
			continue
		}

		lastProcessedTs := int64(0)
		if incremental {
			lastProcessedTs = b.state.LastSummaryTimestamp(log.Session.WxID)
		}

		filteredMsgs := []string{}
		maxTs := lastProcessedTs

		for _, msg := range log.Messages {
			msgTime := msg.GetCreateTime() // 使用兼容方法获取时间戳
			if msgTime <= lastProcessedTs {
				continue
			}
			if startTs > 0 && msgTime < startTs {
				continue
			}
			if endTs > 0 && msgTime > endTs {
				continue
			}

			if msgTime > maxTs {
				maxTs = msgTime
			}

			formatted := formatMessage(msg)
			filteredMsgs = append(filteredMsgs, formatted)
		}

		if len(filteredMsgs) > 0 {
			dataset[sessionName] = append(dataset[sessionName], filteredMsgs...)
			stats = append(stats, SessionStat{
				SessionID:     log.Session.WxID,
				DisplayName:   sessionName,
				Messages:      len(filteredMsgs),
				LastTimestamp: maxTs,
			})
		}
	}

	return &BuildResult{
		Dataset: dataset,
		Stats:   stats,
	}, nil
}

func loadChatLog(path string) (*ChatLog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 先读取为通用 map 来判断格式
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	// 检查是否是 echotrace 格式（包含 session_id 字段）
	if _, hasSessionID := raw["session_id"]; hasSessionID {
		var echoLog EchoTraceChatLog
		if err := json.Unmarshal(data, &echoLog); err != nil {
			return nil, err
		}
		// 转换为统一格式
		return convertEchoTraceLog(&echoLog), nil
	}

	// 原有格式
	var log ChatLog
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, err
	}
	return &log, nil
}

// convertEchoTraceLog 将 echotrace 格式转换为统一的 ChatLog 格式
func convertEchoTraceLog(echoLog *EchoTraceChatLog) *ChatLog {
	log := &ChatLog{
		Session: SessionInfo{
			WxID:        echoLog.SessionID,
			DisplayName: echoLog.SessionName,
			Nickname:    echoLog.SessionName,
		},
		Messages: echoLog.Messages,
	}

	// 统计消息数量和最后导出时间
	log.ExportMetadata.TotalMessageCount = int64(len(echoLog.Messages))
	if len(echoLog.Messages) > 0 {
		// 获取最后一条消息的时间
		lastMsg := echoLog.Messages[len(echoLog.Messages)-1]
		if lastMsg.FormattedTime != "" {
			log.ExportMetadata.LastExportTime = lastMsg.FormattedTime
		} else if lastMsg.CreateTime > 0 {
			log.ExportMetadata.LastExportTime = time.Unix(lastMsg.CreateTime, 0).Format("2006-01-02 15:04:05")
		}
	}

	return log
}

func parseDateRange(start, end string) (int64, int64) {
	var s, e int64
	if start != "" {
		if t, err := time.Parse("2006-01-02", start); err == nil {
			s = t.Unix()
		}
	}
	if end != "" {
		if t, err := time.Parse("2006-01-02", end); err == nil {
			// End of the day
			e = t.Add(24*time.Hour).Unix() - 1
		}
	}
	return s, e
}

func formatMessage(msg Message) string {
	sender := msg.GetSenderName()
	if sender == "" {
		sender = "未知"
	}

	content := msg.GetContent()
	msgType := msg.Type

	// 根据消息类型处理内容
	if msgType == "图片消息" || msg.LocalType == 3 {
		content = "[图片]"
	} else if msgType == "语音消息" || msg.LocalType == 34 {
		content = "[语音]"
	} else if msgType == "视频消息" || msg.LocalType == 43 {
		content = "[视频]"
	} else if msgType == "表情" || msgType == "表情消息" || msgType == "动画表情" {
		content = "[表情]"
	} else if msgType == "系统消息" {
		content = "[系统消息]"
	}

	// 使用兼容方法获取时间
	timeStr := msg.GetFormattedTime()
	if timeStr == "" {
		createTime := msg.GetCreateTime()
		if createTime > 0 {
			timeStr = time.Unix(createTime, 0).Format("2006-01-02 15:04:05")
		}
	}

	return fmt.Sprintf("[%s] %s: %s", timeStr, sender, content)
}

// ListSessionsMetadata returns basic info about all exported sessions
func (b *Builder) ListSessionsMetadata() ([]map[string]any, error) {
	// 优先查找 exportDir/chat_history/*.json，向后兼容地回退到 exportDir/*.json
	files, err := filepath.Glob(filepath.Join(b.exportDir, "chat_history", "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		rootFiles, err2 := filepath.Glob(filepath.Join(b.exportDir, "*.json"))
		if err2 != nil {
			return nil, err2
		}
		files = rootFiles
	}

	results := []map[string]any{}
	for _, file := range files {
		if strings.HasSuffix(file, ".export_state") {
			continue
		}
		// 跳过状态文件
		baseName := filepath.Base(file)
		if strings.HasPrefix(baseName, ".") {
			continue
		}

		log, err := loadChatLog(file)
		if err != nil {
			fmt.Printf("Warning: failed to load %s: %v\n", file, err)
			continue
		}

		// 使用前端期望的字段名
		displayName := log.Session.DisplayName
		if displayName == "" {
			displayName = log.Session.Nickname
		}
		if displayName == "" {
			displayName = log.Session.WxID
		}

		results = append(results, map[string]any{
			"session_id":       log.Session.WxID,                     // 前端期望 session_id
			"display_name":     displayName,                          // 前端期望 display_name
			"messages":         log.ExportMetadata.TotalMessageCount, // 前端期望 messages
			"last_export_time": log.ExportMetadata.LastExportTime,
			"file":             baseName,
		})
	}

	// Sort by display_name
	sort.Slice(results, func(i, j int) bool {
		n1, _ := results[i]["display_name"].(string)
		n2, _ := results[j]["display_name"].(string)
		return n1 < n2
	})

	return results, nil
}
