// Package dataset reads exported chat JSON without modifying source files.
package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// All calendar filters use the same UTC+8 clock on every operating system.
var calendarZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type QueryOptions struct {
	Sessions                    []string
	StartDate, EndDate, Keyword string
	Offset, Limit               int
}

type Message struct {
	SessionID     string `json:"session_id"`
	DisplayName   string `json:"display_name"`
	LocalID       int64  `json:"local_id"`
	CreateTime    int64  `json:"create_time"`
	FormattedTime string `json:"formatted_time"`
	Type          string `json:"type"`
	Content       string `json:"content"`
	IsSend        int    `json:"is_send"`
	Sender        string `json:"sender"`
	SenderName    string `json:"sender_name"`
}

type QueryResult struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	Offset   int       `json:"offset"`
	Limit    int       `json:"limit"`
}

type SessionStats struct {
	SessionID        string `json:"session_id"`
	DisplayName      string `json:"display_name"`
	Messages         int    `json:"messages"`
	SentMessages     int    `json:"sent_messages"`
	ReceivedMessages int    `json:"received_messages"`
	FirstMessage     int64  `json:"first_message"`
	LastMessage      int64  `json:"last_message"`
}

type StatsResult struct {
	TotalMessages    int            `json:"total_messages"`
	SessionCount     int            `json:"session_count"`
	SentMessages     int            `json:"sent_messages"`
	ReceivedMessages int            `json:"received_messages"`
	ByType           map[string]int `json:"by_type"`
	Sessions         []SessionStats `json:"sessions"`
}

// DateRange rejects invalid dates and inverted intervals; the end is inclusive.
func DateRange(start, end string) (int64, int64, error) {
	var first, last int64
	for _, input := range []struct {
		label, value string
		target       *int64
	}{{"start_date", start, &first}, {"end_date", end, &last}} {
		if input.value == "" {
			continue
		}
		t, err := time.ParseInLocation("2006-01-02", input.value, calendarZone)
		if err != nil || t.Format("2006-01-02") != input.value {
			return 0, 0, &ValidationError{input.label + " must be YYYY-MM-DD"}
		}
		*input.target = t.Unix()
	}
	if start != "" && end != "" && first > last {
		return 0, 0, &ValidationError{"start_date must not be after end_date"}
	}
	if end != "" {
		last += 24*60*60 - 1
	}
	return first, last, nil
}

func Validate(opts QueryOptions) error {
	if opts.Offset < 0 || opts.Limit < 0 {
		return &ValidationError{"offset and limit must be non-negative"}
	}
	_, _, err := DateRange(opts.StartDate, opts.EndDate)
	return err
}

type chatLog struct {
	id, exported string
	messages     []Message
}

// decode accepts both session/camelCase and EchoTrace/snake_case exports.
func decode(path string, data []byte) (*chatLog, error) {
	var raw struct {
		Session struct {
			WxID        string `json:"wxid"`
			DisplayName string `json:"displayName"`
			Nickname    string `json:"nickname"`
			Remark      string `json:"remark"`
		} `json:"session"`
		SessionID   string `json:"session_id"`
		SessionName string `json:"session_name"`
		ExportTime  string `json:"export_time"`
		Metadata    struct {
			LastExportTime string `json:"lastExportTime"`
		} `json:"exportMetadata"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	id := first(raw.SessionID, raw.Session.WxID)
	if id == "" || len(raw.Messages) == 0 || string(raw.Messages) == "null" {
		return nil, fmt.Errorf("parse %s: expected session ID and messages array", path)
	}
	name := first(raw.SessionName, raw.Session.DisplayName, raw.Session.Remark, raw.Session.Nickname, id)
	var messages []struct {
		LocalID           int64  `json:"localId"`
		LocalIDAlt        int64  `json:"local_id"`
		CreateTime        int64  `json:"createTime"`
		CreateTimeAlt     int64  `json:"create_time"`
		FormattedTime     string `json:"formattedTime"`
		FormattedTimeAlt  string `json:"formatted_time"`
		Type              string `json:"type"`
		Content           string `json:"content"`
		StrContent        string `json:"str_content"`
		IsSend            *int   `json:"isSend"`
		IsSendAlt         *int   `json:"is_send"`
		SenderUsername    string `json:"senderUsername"`
		SenderDisplayName string `json:"senderDisplayName"`
		Sender            string `json:"sender"`
		SenderName        string `json:"sender_name"`
		SenderWxid        string `json:"sender_wxid"`
	}
	if err := json.Unmarshal(raw.Messages, &messages); err != nil {
		return nil, fmt.Errorf("parse messages in %s: %w", path, err)
	}
	log := &chatLog{id: id, exported: first(raw.ExportTime, raw.Metadata.LastExportTime), messages: make([]Message, 0, len(messages))}
	for index, msg := range messages {
		ts := msg.CreateTime
		if ts == 0 {
			ts = msg.CreateTimeAlt
		}
		formatted := first(msg.FormattedTime, msg.FormattedTimeAlt)
		if ts == 0 && formatted != "" {
			parsed, err := time.ParseInLocation("2006-01-02 15:04:05", formatted, calendarZone)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, formatted)
			}
			if err != nil {
				return nil, fmt.Errorf("parse %s message %d: invalid formatted time %q", path, index+1, formatted)
			}
			ts = parsed.Unix()
		}
		if ts == 0 {
			return nil, fmt.Errorf("parse %s message %d: missing message timestamp", path, index+1)
		}
		if formatted == "" && ts != 0 {
			formatted = time.Unix(ts, 0).In(calendarZone).Format("2006-01-02 15:04:05")
		}
		localID := msg.LocalID
		if localID == 0 {
			localID = msg.LocalIDAlt
		}
		isSend := 0
		if msg.IsSend != nil {
			isSend = *msg.IsSend
		} else if msg.IsSendAlt != nil {
			isSend = *msg.IsSendAlt
		}
		log.messages = append(log.messages, Message{SessionID: id, DisplayName: name, LocalID: localID, CreateTime: ts, FormattedTime: formatted, Type: msg.Type, Content: first(msg.Content, msg.StrContent), IsSend: isSend, Sender: first(msg.SenderUsername, msg.SenderWxid, msg.Sender), SenderName: first(msg.SenderDisplayName, msg.SenderName, msg.Sender)})
	}
	return log, nil
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Only explicit export JSONs are read. The dedicated chat_history directory wins
// when it contains JSON; hidden state files are never treated as conversations.
func files(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, &ValidationError{"export directory is empty"}
	}
	read := func(path string) ([]string, error) {
		entries, err := os.ReadDir(path)
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read export directory %s: %w", path, err)
		}
		result := []string{}
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				result = append(result, filepath.Join(path, entry.Name()))
			}
		}
		return result, nil
	}
	result, err := read(filepath.Join(dir, "chat_history"))
	if err != nil || len(result) > 0 {
		return result, err
	}
	return read(dir)
}
