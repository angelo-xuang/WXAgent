package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/llm"
)

// Message 消息结构
type Message struct {
	CreateTime    int64  `json:"create_time"`
	FormattedTime string `json:"formatted_time"`
	Type          string `json:"type"`
	StrContent    string `json:"str_content"`
	Sender        string `json:"sender,omitempty"`
	SenderName    string `json:"sender_name,omitempty"`
	SenderWxid    string `json:"sender_wxid,omitempty"`
	SessionName   string `json:"-"` // 添加会话名称字段，不输出到JSON
}

// Session 会话结构
type Session struct {
	SessionID   string    `json:"session_id"`
	SessionName string    `json:"session_name"`
	Messages    []Message `json:"messages"`
}

// SessionMessages 会话消息集合
type SessionMessages struct {
	SessionName string
	Messages    []Message
}

// Analyzer 分析器
type Analyzer struct {
	exportDir string
	llmClient *llm.Client
}

// New 创建分析器
func New(exportDir string, llmClient *llm.Client) *Analyzer {
	return &Analyzer{
		exportDir: exportDir,
		llmClient: llmClient,
	}
}

// AnalyzeOptions 分析选项
type AnalyzeOptions struct {
	StartDate    string   // 开始日期 YYYY-MM-DD
	EndDate      string   // 结束日期 YYYY-MM-DD
	SessionNames []string // 指定会话名称，为空则全部
	MaxTokens    int      // 每段最大token数，默认80000（约100K字符，留余量）
	OutputFile   string   // 输出文件路径
	ChunkPrompt  string   // 自定义分段提示词（可选）
	FinalPrompt  string   // 自定义汇总提示词（可选）
}

// AnalyzeResult 分析结果
type AnalyzeResult struct {
	TotalMessages int      `json:"total_messages"`
	TotalSessions int      `json:"total_sessions"`
	ChunkCount    int      `json:"chunk_count"`
	Summaries     []string `json:"summaries"`
	FinalSummary  string   `json:"final_summary"`
	OutputFile    string   `json:"output_file"`
	Logs          []string `json:"logs"` // 处理过程日志
}

// StreamEvent SSE 事件类型
type StreamEvent struct {
	Type    string `json:"type"`    // "log", "ai_chunk", "progress", "done", "error"
	Content string `json:"content"` // 内容
	Step    int    `json:"step"`    // 当前步骤 (1-6)
	Total   int    `json:"total"`   // 总步骤数
}

// StreamCallback 流式事件回调
type StreamCallback func(event StreamEvent)

// logBuffer 用于收集日志
type logBuffer struct {
	logs []string
}

func (lb *logBuffer) Log(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	lb.logs = append(lb.logs, msg)
	fmt.Println(msg) // 同时输出到控制台
}

// Analyze 执行分析
func (a *Analyzer) Analyze(ctx context.Context, opts AnalyzeOptions) (*AnalyzeResult, error) {
	lb := &logBuffer{}

	// 设置默认值
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 80000 // DeepSeek支持128K，设置80K留余量
	}
	if opts.OutputFile == "" {
		// 添加时间戳防止覆盖，并保存到 exportDir/analysis 目录中（analysis 在 export_dir 下）
		timestamp := time.Now().Format("20060102_150405")
		opts.OutputFile = filepath.Join(a.exportDir, "analysis", fmt.Sprintf("chat_analysis_%s.md", timestamp))
	}

	lb.Log("🔍 Step 1: 加载并合并所有JSON消息...")
	sessionMessagesList, err := a.loadAndMergeMessagesBySession(opts)
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}

	if len(sessionMessagesList) == 0 {
		return nil, fmt.Errorf("对应的时间点没有聊天记录，请确认已经进行增量读取与导出")
	}

	// 统计总消息数
	totalMessages := 0
	var sessionNames []string
	for _, sm := range sessionMessagesList {
		totalMessages += len(sm.Messages)
		sessionNames = append(sessionNames, sm.SessionName)
	}

	// 显示时间范围统计（取第一个和最后一个会话的时间）
	if len(sessionMessagesList) > 0 && len(sessionMessagesList[0].Messages) > 0 {
		firstMsg := sessionMessagesList[0].Messages[0]
		lastSession := sessionMessagesList[len(sessionMessagesList)-1]
		lastMsg := lastSession.Messages[len(lastSession.Messages)-1]

		firstTime, _ := time.Parse("2006-01-02 15:04:05", firstMsg.FormattedTime)
		lastTime, _ := time.Parse("2006-01-02 15:04:05", lastMsg.FormattedTime)

		lb.Log("  [DEBUG] 实际消息时间范围: %s 至 %s",
			firstTime.Format("2006-01-02 15:04:05"),
			lastTime.Format("2006-01-02 15:04:05"))
	}

	lb.Log("✓ 已加载 %d 条消息来自 %d 个会话", totalMessages, len(sessionNames))

	lb.Log("\n📅 Step 2: 按会话格式化消息...")
	formattedContent := a.formatMessagesBySession(sessionMessagesList)
	lb.Log("✓ 格式化完成，总字符数: %d", len(formattedContent))
	lb.Log("  [统计] 平均每条消息: %.1f 字符", float64(len(formattedContent))/float64(totalMessages))

	lb.Log("\n✂️  Step 3: 分段处理（考虑token限制）...")
	chunks := a.splitIntoChunks(formattedContent, opts.MaxTokens)
	lb.Log("✓ 分为 %d 段", len(chunks))
	lb.Log("  [统计] MaxTokens设置: %d, 每段最大字符数: %d", opts.MaxTokens, int(float64(opts.MaxTokens)/0.8))
	if len(chunks) > 1 {
		for i, chunk := range chunks {
			lb.Log("  [统计] 第 %d 段: %d 字符 (~%.0f tokens)", i+1, len(chunk), float64(len(chunk))*0.8)
		}
	}

	// 保存切片内容到txt文件（放在 export_dir/analysis 下，和分析报告放一起）
	chunksFile := filepath.Join(a.exportDir, "analysis", "chunks_preview.txt")
	if err := a.saveChunksToFile(chunksFile, chunks); err != nil {
		lb.Log("⚠️  保存切片预览失败: %v", err)
	} else {
		lb.Log("  [调试] 切片内容已保存到: %s", chunksFile)
	}

	lb.Log("\n🤖 Step 4: 逐段调用大模型进行总结...")
	var chunkSummaries []string
	for i, chunk := range chunks {
		lb.Log("  处理第 %d/%d 段...", i+1, len(chunks))
		summary, err := a.summarizeChunk(ctx, chunk, i+1, len(chunks), opts.ChunkPrompt)
		if err != nil {
			return nil, fmt.Errorf("summarize chunk %d: %w", i+1, err)
		}
		chunkSummaries = append(chunkSummaries, summary)
		lb.Log("  ✓ 完成第 %d 段 (输出 %d 字符)", i+1, len(summary))
	}

	lb.Log("\n🔄 Step 5: 合并所有总结生成最终报告...")
	finalSummary, err := a.mergeSummaries(ctx, chunkSummaries, opts.FinalPrompt)
	if err != nil {
		return nil, fmt.Errorf("merge summaries: %w", err)
	}
	lb.Log("✓ 合并完成 (输出 %d 字符)", len(finalSummary))

	lb.Log("\n💾 Step 6: 保存分析报告...")
	if err := a.saveReport(opts.OutputFile, sessionNames, totalMessages, opts.StartDate, opts.EndDate, chunkSummaries, finalSummary); err != nil {
		return nil, fmt.Errorf("save report: %w", err)
	}

	lb.Log("✓ 报告已保存到: %s", opts.OutputFile)

	return &AnalyzeResult{
		TotalMessages: totalMessages,
		TotalSessions: len(sessionNames),
		ChunkCount:    len(chunks),
		Summaries:     chunkSummaries,
		FinalSummary:  finalSummary,
		OutputFile:    opts.OutputFile,
		Logs:          lb.logs,
	}, nil
}

// AnalyzeStream 执行分析并通过回调发送流式事件
func (a *Analyzer) AnalyzeStream(ctx context.Context, opts AnalyzeOptions, callback StreamCallback) (*AnalyzeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 辅助函数：发送日志事件
	sendLog := func(step int, format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		fmt.Println(msg) // 控制台输出
		if callback != nil {
			callback(StreamEvent{Type: "log", Content: msg, Step: step, Total: 6})
		}
	}

	// 辅助函数：发送 AI 输出事件
	sendAIChunk := func(step int, content string) {
		if callback != nil {
			callback(StreamEvent{Type: "ai_chunk", Content: content, Step: step, Total: 6})
		}
	}

	// 辅助函数：发送进度事件
	sendProgress := func(step int, current, total int) {
		if callback != nil {
			callback(StreamEvent{
				Type:    "progress",
				Content: fmt.Sprintf("%d/%d", current, total),
				Step:    step,
				Total:   6,
			})
		}
	}

	var logs []string
	logAndSend := func(step int, format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		logs = append(logs, msg)
		sendLog(step, msg)
	}

	// 设置默认值
	if opts.MaxTokens == 0 {
		opts.MaxTokens = 80000
	}
	if opts.OutputFile == "" {
		// 添加时间戳防止覆盖，并保存到 exportDir/analysis 目录中（analysis 在 export_dir 下）
		timestamp := time.Now().Format("20060102_150405")
		opts.OutputFile = filepath.Join(a.exportDir, "analysis", fmt.Sprintf("chat_analysis_%s.md", timestamp))
	}

	// Step 1: 加载消息
	logAndSend(1, "🔍 Step 1: 加载并合并所有JSON消息...")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sessionMessagesList, err := a.loadAndMergeMessagesBySession(opts)
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}

	if len(sessionMessagesList) == 0 {
		return nil, fmt.Errorf("对应的时间点没有聊天记录，请确认已经进行增量读取与导出")
	}

	totalMessages := 0
	var sessionNames []string
	for _, sm := range sessionMessagesList {
		totalMessages += len(sm.Messages)
		sessionNames = append(sessionNames, sm.SessionName)
	}

	logAndSend(1, "✓ 已加载 %d 条消息来自 %d 个会话", totalMessages, len(sessionNames))

	// Step 2: 格式化消息
	logAndSend(2, "\n📅 Step 2: 按会话格式化消息...")
	formattedContent := a.formatMessagesBySession(sessionMessagesList)
	logAndSend(2, "✓ 格式化完成，总字符数: %d", len(formattedContent))

	// Step 3: 分段
	logAndSend(3, "\n✂️  Step 3: 分段处理（考虑token限制）...")
	chunks := a.splitIntoChunks(formattedContent, opts.MaxTokens)
	logAndSend(3, "✓ 分为 %d 段", len(chunks))

	// Step 4: 逐段总结
	logAndSend(4, "\n🤖 Step 4: 逐段调用LLM进行总结...")
	var chunkSummaries []string
	for i, chunk := range chunks {
		logAndSend(4, "  处理第 %d/%d 段...", i+1, len(chunks))
		sendProgress(4, i+1, len(chunks))
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// 使用流式 API
		summary, err := a.summarizeChunkStream(ctx, chunk, i+1, len(chunks), opts.ChunkPrompt, func(content string) {
			sendAIChunk(4, content)
		})
		if err != nil {
			return nil, fmt.Errorf("summarize chunk %d: %w", i+1, err)
		}
		chunkSummaries = append(chunkSummaries, summary)
		logAndSend(4, "  ✓ 完成第 %d 段 (输出 %d 字符)", i+1, len(summary))
	}

	// Step 5: 合并总结
	logAndSend(5, "\n🔄 Step 5: 合并所有总结生成最终报告...")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	finalSummary, err := a.mergeSummariesStream(ctx, chunkSummaries, opts.FinalPrompt, func(content string) {
		sendAIChunk(5, content)
	})
	if err != nil {
		return nil, fmt.Errorf("merge summaries: %w", err)
	}
	logAndSend(5, "✓ 合并完成 (输出 %d 字符)", len(finalSummary))

	// Step 6: 保存报告
	logAndSend(6, "\n💾 Step 6: 保存分析报告...")
	// A provider may finish just as the user cancels. Do not publish that result
	// merely because the last network read happened to finish first.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.saveReport(opts.OutputFile, sessionNames, totalMessages, opts.StartDate, opts.EndDate, chunkSummaries, finalSummary); err != nil {
		return nil, fmt.Errorf("save report: %w", err)
	}
	logAndSend(6, "✓ 报告已保存到: %s", opts.OutputFile)

	// 发送完成事件
	if callback != nil {
		callback(StreamEvent{Type: "done", Content: "分析完成", Step: 6, Total: 6})
	}

	return &AnalyzeResult{
		TotalMessages: totalMessages,
		TotalSessions: len(sessionNames),
		ChunkCount:    len(chunks),
		Summaries:     chunkSummaries,
		FinalSummary:  finalSummary,
		OutputFile:    opts.OutputFile,
		Logs:          logs,
	}, nil
}

// loadAndMergeMessages 加载并合并所有消息
func (a *Analyzer) loadAndMergeMessages(opts AnalyzeOptions) ([]Message, []string, error) {
	// 优先查找 exportDir/chat_history/*.json；为兼容旧版也同时检查 exportDir/*.json
	files, err := filepath.Glob(filepath.Join(a.exportDir, "chat_history", "*.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("glob export dir (chat_history): %w", err)
	}
	// 如果没有找到任何文件，也尝试根目录下的 json（向后兼容）
	if len(files) == 0 {
		rootFiles, err2 := filepath.Glob(filepath.Join(a.exportDir, "*.json"))
		if err2 != nil {
			return nil, nil, fmt.Errorf("glob export dir: %w", err2)
		}
		files = rootFiles
	}

	allowedSessions := make(map[string]bool)
	for _, s := range opts.SessionNames {
		allowedSessions[s] = true
	}

	var allMessages []Message
	sessionSet := make(map[string]bool)

	for _, file := range files {
		// 跳过状态文件
		if strings.HasSuffix(file, ".export_state") {
			continue
		}

		session, err := loadSession(file)
		if err != nil {
			fmt.Printf("⚠️  跳过文件 %s: %v\n", filepath.Base(file), err)
			continue
		}

		// 过滤公众号（session_id 以 gh_ 开头）
		if strings.HasPrefix(session.SessionID, "gh_") {
			continue
		}

		// 过滤会话
		if len(allowedSessions) > 0 && !allowedSessions[session.SessionName] {
			continue
		}

		// 过滤并收集消息
		hasValidMessages := false
		for _, msg := range session.Messages {
			// 使用 formatted_time 进行时间过滤
			if msg.FormattedTime == "" {
				continue
			}

			// 提取日期部分 "2025-11-28 13:37:27" -> "2025-11-28"
			msgDate := msg.FormattedTime
			if len(msgDate) >= 10 {
				msgDate = msgDate[:10]
			}

			// 时间过滤（字符串比较）
			if opts.StartDate != "" && msgDate < opts.StartDate {
				continue
			}
			if opts.EndDate != "" && msgDate > opts.EndDate {
				continue
			}

			// 清理消息内容
			msg.StrContent = a.cleanMessageContent(msg)
			allMessages = append(allMessages, msg)
			hasValidMessages = true
		}

		// 只有当会话有有效消息时才加入统计
		if hasValidMessages {
			sessionSet[session.SessionName] = true
		}
	}

	// 按时间排序
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].CreateTime < allMessages[j].CreateTime
	})

	sessionNames := make([]string, 0, len(sessionSet))
	for name := range sessionSet {
		sessionNames = append(sessionNames, name)
	}
	sort.Strings(sessionNames)

	return allMessages, sessionNames, nil
}

// loadAndMergeMessagesBySession 按会话加载并合并消息
func (a *Analyzer) loadAndMergeMessagesBySession(opts AnalyzeOptions) ([]SessionMessages, error) {
	// 优先查找 exportDir/chat_history/*.json；为兼容旧版也同时检查 exportDir/*.json
	files, err := filepath.Glob(filepath.Join(a.exportDir, "chat_history", "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob export dir (chat_history): %w", err)
	}
	if len(files) == 0 {
		rootFiles, err2 := filepath.Glob(filepath.Join(a.exportDir, "*.json"))
		if err2 != nil {
			return nil, fmt.Errorf("glob export dir: %w", err2)
		}
		files = rootFiles
	}

	allowedSessions := make(map[string]bool)
	for _, s := range opts.SessionNames {
		allowedSessions[s] = true
	}

	var sessionMessagesList []SessionMessages

	for _, file := range files {
		// 跳过状态文件
		if strings.HasSuffix(file, ".export_state") {
			continue
		}

		session, err := loadSession(file)
		if err != nil {
			fmt.Printf("⚠️  跳过文件 %s: %v\n", filepath.Base(file), err)
			continue
		}

		// 过滤公众号（session_id 以 gh_ 开头）
		if strings.HasPrefix(session.SessionID, "gh_") {
			continue
		}

		// 过滤会话
		if len(allowedSessions) > 0 && !allowedSessions[session.SessionName] {
			continue
		}

		// 过滤并收集消息
		var validMessages []Message
		for _, msg := range session.Messages {
			// 使用 formatted_time 进行时间过滤
			if msg.FormattedTime == "" {
				continue
			}

			// 提取日期部分 "2025-11-28 13:37:27" -> "2025-11-28"
			msgDate := msg.FormattedTime
			if len(msgDate) >= 10 {
				msgDate = msgDate[:10]
			}

			// 时间过滤（字符串比较）
			if opts.StartDate != "" && msgDate < opts.StartDate {
				continue
			}
			if opts.EndDate != "" && msgDate > opts.EndDate {
				continue
			}

			// 清理消息内容
			msg.StrContent = a.cleanMessageContent(msg)
			msg.SessionName = session.SessionName // 添加会话名称
			validMessages = append(validMessages, msg)
		}

		// 只有当会话有有效消息时才加入
		if len(validMessages) > 0 {
			// 按时间排序该会话的消息
			sort.Slice(validMessages, func(i, j int) bool {
				return validMessages[i].FormattedTime < validMessages[j].FormattedTime
			})

			sessionMessagesList = append(sessionMessagesList, SessionMessages{
				SessionName: session.SessionName,
				Messages:    validMessages,
			})
		}
	}

	// 按会话名称排序（可选）
	sort.Slice(sessionMessagesList, func(i, j int) bool {
		return sessionMessagesList[i].SessionName < sessionMessagesList[j].SessionName
	})

	return sessionMessagesList, nil
}

// cleanMessageContent 清理消息内容
func (a *Analyzer) cleanMessageContent(msg Message) string {
	content := msg.StrContent

	// 处理不支持的消息类型
	if msg.Type == "系统消息" {
		return "[不支持的消息类型]"
	}
	if msg.Type == "表情" || msg.Type == "动画表情" {
		return "[表情消息]"
	}
	if msg.Type == "图片消息" {
		return "[图片]"
	}
	if msg.Type == "语音消息" {
		return "[语音]"
	}
	if msg.Type == "视频消息" {
		return "[视频]"
	}
	if msg.Type == "文件消息" {
		return "[文件]"
	}
	if msg.Type == "位置消息" {
		return "[位置]"
	}
	if msg.Type == "名片消息" {
		return "[名片]"
	}

	// 处理接龙消息等特殊类型
	if strings.Contains(msg.Type, "未知消息类型") {
		// 尝试解析BytesExtra中的接龙内容
		// 这里可能需要根据实际情况调整
		if content != "" {
			return content
		}
		return "[特殊消息]"
	}

	return content
}

// formatMessages 格式化消息列表
func (a *Analyzer) formatMessages(messages []Message) string {
	var sb strings.Builder

	for _, msg := range messages {
		// 格式：时间 | 发送者 | 消息内容
		sender := msg.SenderName
		if sender == "" {
			sender = msg.Sender
		}
		if sender == "" {
			sender = "未知"
		}

		timeStr := msg.FormattedTime
		if timeStr == "" && msg.CreateTime > 0 {
			t := time.Unix(msg.CreateTime, 0)
			timeStr = t.Format("2006-01-02 15:04:05")
		}

		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", timeStr, sender, msg.StrContent))
	}

	return sb.String()
}

// formatMessagesBySession 按会话格式化消息列表
func (a *Analyzer) formatMessagesBySession(sessionMessagesList []SessionMessages) string {
	var sb strings.Builder

	for _, sm := range sessionMessagesList {
		// 会话标题
		sb.WriteString(fmt.Sprintf("\n========== %s ==========\n\n", sm.SessionName))

		// 该会话的所有消息
		for _, msg := range sm.Messages {
			sender := msg.SenderName
			if sender == "" {
				sender = msg.Sender
			}
			if sender == "" {
				sender = "未知"
			}

			timeStr := msg.FormattedTime
			if timeStr == "" && msg.CreateTime > 0 {
				t := time.Unix(msg.CreateTime, 0)
				timeStr = t.Format("2006-01-02 15:04:05")
			}

			sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", timeStr, sender, msg.StrContent))
		}

		sb.WriteString("\n") // 会话之间空一行
	}

	return sb.String()
}

// splitIntoChunks 分段
func (a *Analyzer) splitIntoChunks(content string, maxTokens int) []string {
	// 更准确的估计：1个字符约等于0.8个token（针对中英混合文本）
	// maxTokens = 300000 -> 约 375000 字符
	maxChars := int(float64(maxTokens) / 0.8)

	if len(content) <= maxChars {
		return []string{content}
	}

	var chunks []string
	lines := strings.Split(content, "\n")

	var currentChunk strings.Builder
	currentSize := 0

	for _, line := range lines {
		lineSize := len(line) + 1 // +1 for newline

		if currentSize+lineSize > maxChars && currentSize > 0 {
			// 当前块已满，保存并开始新块
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
			currentSize = 0
		}

		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
		currentSize += lineSize
	}

	// 添加最后一块
	if currentSize > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// summarizeChunk ?????? (Map ??)
func (a *Analyzer) summarizeChunk(ctx context.Context, chunk string, chunkNum, totalChunks int, promptOverride string) (string, error) {
	prompt := buildChunkPrompt(promptOverride, chunk, chunkNum, totalChunks)

	messages := []llm.ChatMessage{
		{
			Role:    "system",
			Content: "??????????????????????????????????????????????????????????????",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	return a.llmClient.ChatCompletion(ctx, messages)
}

// summarizeChunkStream ??????????? (Map ??)
func (a *Analyzer) summarizeChunkStream(ctx context.Context, chunk string, chunkNum, totalChunks int, promptOverride string, callback func(string)) (string, error) {
	prompt := buildChunkPrompt(promptOverride, chunk, chunkNum, totalChunks)

	messages := []llm.ChatMessage{
		{
			Role:    "system",
			Content: "??????????????????????????????????????????????????????????????",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	return a.llmClient.ChatCompletionWithCallback(ctx, messages, callback)
}

func combineSummariesForPrompt(summaries []string) string {
	formattedSummaries := make([]string, 0, len(summaries))
	for idx, summary := range summaries {
		formattedSummaries = append(formattedSummaries, fmt.Sprintf("[Batch %d]\n%s", idx+1, strings.TrimSpace(summary)))
	}
	return strings.Join(formattedSummaries, "\n\n---\n\n")
}

const defaultChunkPromptTemplate = `请分别总结以下微信群聊天记录的主要内容和讨论话题。
注意：以下文本包含来自不同群聊的消息，每个群聊用【群聊：群名】的格式标记，请按群聊分别总结。

{{content}}

要求：
1. 用中文总结
2. 分聊天对象进行总结，同一个群聊或者同一个聊天记录放在一起总结。
3. 总结出主要话题和讨论内容（用编号列出，并附上时间和讨论人（如果必要））
4. 提取出重要信息（如招聘信息、活动信息等），并注意引用原文！
5. 概括参与者的主要观点或反应
6. 标出最活跃的话题和讨论热度`

const defaultFinalPromptTemplate = `{{intro}}

{{summaries}}

要求：
1. 按群聊/话题重新组织内容，去掉重复叙述，但要保留所有关键细节、时间、人物与数量。
2. 识别跨批次连续的讨论并合并，补全上下文。
3. 输出 Markdown，包含：概览、按群聊的详细总结、关键行动项/待办/风险。`

func buildChunkPrompt(override, chunk string, chunkNum, totalChunks int) string {
	template := strings.TrimSpace(override)
	if template == "" {
		template = defaultChunkPromptTemplate
	}

	values := map[string]string{
		"{{chunk_num}}":   fmt.Sprintf("%d", chunkNum),
		"{{chunk_total}}": fmt.Sprintf("%d", totalChunks),
		"{{content}}":     chunk,
		"{{chunk}}":       chunk,
	}
	prompt := replacePlaceholders(template, values)

	if !strings.Contains(prompt, chunk) {
		prompt = prompt + "\n\n" + chunk
	}
	return prompt
}

func buildFinalPrompt(override, combinedSummaries string, summaryCount int) string {
	template := strings.TrimSpace(override)
	if template == "" {
		template = defaultFinalPromptTemplate
	}

	introText := "以下是分片总结，请按要求输出最终报告。"
	if summaryCount == 1 {
		introText = "以下是一条聊天总结，请整理为结构化最终报告："
	}

	values := map[string]string{
		"{{summaries}}":     combinedSummaries,
		"{{summary_count}}": fmt.Sprintf("%d", summaryCount),
		"{{intro}}":         introText,
		"{{chunks_count}}":  fmt.Sprintf("%d", summaryCount),
		"{{chunk_count}}":   fmt.Sprintf("%d", summaryCount),
		"{{segment_count}}": fmt.Sprintf("%d", summaryCount),
		"{{section_count}}": fmt.Sprintf("%d", summaryCount),
	}
	prompt := replacePlaceholders(template, values)

	if !strings.Contains(prompt, combinedSummaries) {
		prompt = prompt + "\n\n" + combinedSummaries
	}
	return prompt
}

func replacePlaceholders(template string, values map[string]string) string {
	result := template
	for key, val := range values {
		result = strings.ReplaceAll(result, key, val)
	}
	return result
}

// mergeSummaries ?????? (Reduce ??)
// ??????????AI ???????
func (a *Analyzer) mergeSummaries(ctx context.Context, summaries []string, promptOverride string) (string, error) {
	combinedSummaries := combineSummariesForPrompt(summaries)
	prompt := buildFinalPrompt(promptOverride, combinedSummaries, len(summaries))

	messages := []llm.ChatMessage{
		{
			Role:    "system",
			Content: "?????????/?????????????????????????????",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	return a.llmClient.ChatCompletion(ctx, messages)
}

// mergeSummariesStream ????????? (Reduce ??)
func (a *Analyzer) mergeSummariesStream(ctx context.Context, summaries []string, promptOverride string, callback func(string)) (string, error) {
	combinedSummaries := combineSummariesForPrompt(summaries)
	prompt := buildFinalPrompt(promptOverride, combinedSummaries, len(summaries))

	messages := []llm.ChatMessage{
		{
			Role:    "system",
			Content: "?????????/?????????????????????????????",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	}

	return a.llmClient.ChatCompletionWithCallback(ctx, messages, callback)
}

// saveReport 保存报告
func (a *Analyzer) saveReport(outputFile string, sessionNames []string, totalMessages int, startDate, endDate string, chunkSummaries []string, finalSummary string) error {
	var sb strings.Builder

	// 标题
	sb.WriteString("# 微信聊天记录分析报告\n\n")

	// 生成时间
	sb.WriteString(fmt.Sprintf("**生成时间**: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 统计信息
	sb.WriteString("## 统计信息\n\n")
	sb.WriteString(fmt.Sprintf("- **分析消息数**: %d 条\n", totalMessages))
	sb.WriteString(fmt.Sprintf("- **涉及会话数**: %d 个\n", len(sessionNames)))
	sb.WriteString(fmt.Sprintf("- **分段数量**: %d 段\n\n", len(chunkSummaries)))

	// 会话列表
	sb.WriteString("### 涉及会话\n\n")
	for _, name := range sessionNames {
		sb.WriteString(fmt.Sprintf("- %s\n", name))
	}
	sb.WriteString("\n")

	// 时间范围
	sb.WriteString(fmt.Sprintf("- **时间范围**: %s 至 %s\n\n",
		startDate, endDate))

	sb.WriteString("---\n\n")

	// 最终总结
	sb.WriteString("## 总结报告\n\n")
	sb.WriteString(finalSummary)
	sb.WriteString("\n\n---\n\n")

	// 分段总结详情
	sb.WriteString("## 附录：分段总结详情\n\n")
	sb.WriteString(fmt.Sprintf("> 本次分析共分为 **%d** 个段落进行处理\n\n", len(chunkSummaries)))

	for i, summary := range chunkSummaries {
		sb.WriteString(fmt.Sprintf("### 分段总结 %d/%d\n\n", i+1, len(chunkSummaries)))
		sb.WriteString(summary)
		sb.WriteString("\n\n")
		if i < len(chunkSummaries)-1 {
			sb.WriteString("---\n\n")
		}
	}

	// 确保目录存在（将保存到 exportDir/analysis/...）
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(outputFile, []byte(sb.String()), 0644)
}

// saveChunksToFile 保存切片内容到txt文件（用于调试）
func (a *Analyzer) saveChunksToFile(outputFile string, chunks []string) error {
	var sb strings.Builder

	sb.WriteString("=== 微信聊天记录切片预览 ===\n\n")
	sb.WriteString(fmt.Sprintf("生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("总段数: %d\n\n", len(chunks)))
	sb.WriteString(strings.Repeat("=", 80) + "\n\n")

	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("### 第 %d/%d 段 ###\n", i+1, len(chunks)))
		sb.WriteString(fmt.Sprintf("字符数: %d\n", len(chunk)))
		sb.WriteString(fmt.Sprintf("估算tokens: ~%.0f\n\n", float64(len(chunk))*0.8))
		sb.WriteString(strings.Repeat("-", 80) + "\n")
		sb.WriteString(chunk)
		sb.WriteString("\n")
		sb.WriteString(strings.Repeat("=", 80) + "\n\n")
	}

	// 确保目录存在（放在 export_dir/analysis）
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(outputFile, []byte(sb.String()), 0644)
}

// loadSession 加载会话
func loadSession(path string) (*Session, error) {
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
