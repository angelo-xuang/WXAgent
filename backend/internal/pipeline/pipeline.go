package pipeline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/analyzer"
	"github.com/billyoftea/wxagent/go_backend/internal/atomicfile"
	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/dataset"
	"github.com/billyoftea/wxagent/go_backend/internal/echotrace"
	"github.com/billyoftea/wxagent/go_backend/internal/llm"
	"github.com/billyoftea/wxagent/go_backend/internal/state"
	"github.com/billyoftea/wxagent/go_backend/internal/summarizer"
	"github.com/billyoftea/wxagent/go_backend/internal/wxdb"
	"github.com/billyoftea/wxagent/go_backend/internal/wxkey"
)

// StreamEvent 流式事件类型别名
type StreamEvent = analyzer.StreamEvent

type Pipeline struct {
	Config    *config.PipelineConfig
	State     *state.Store
	WxKey     *wxkey.Bridge
	Echotrace *echotrace.Runner
}

func New(cfg *config.PipelineConfig) (*Pipeline, error) {
	fmt.Printf("[DEBUG] State file path from config: %s\n", cfg.StateFile)
	s := state.New(cfg.StateFile)
	if err := s.Load(); err != nil {
		return nil, fmt.Errorf("load state without overwriting existing data: %w", err)
	}

	return &Pipeline{
		Config:    cfg,
		State:     s,
		WxKey:     wxkey.New(cfg.WxKeySharedPrefs, cfg.WxKeyCommand),
		Echotrace: echotrace.New(cfg.EchotraceCommand),
	}, nil
}

func (p *Pipeline) RefreshKey(autoLaunch bool, waitSeconds int, pollInterval int) (*wxkey.KeyPayload, error) {
	payload, err := p.WxKey.LoadKeys()
	if err == nil && payload.DbKey != "" {
		return payload, nil
	}

	if !autoLaunch {
		return nil, fmt.Errorf("key not found and auto_launch is false")
	}

	fmt.Println("⚠️ Key not found, launching wx_key...")
	if err := p.WxKey.Launch(false); err != nil {
		return nil, fmt.Errorf("launch wx_key: %w", err)
	}

	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	ticker := time.NewTicker(time.Duration(pollInterval) * time.Second)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		<-ticker.C
		payload, err := p.WxKey.LoadKeys()
		if err == nil && payload.DbKey != "" {
			fmt.Println("✅ Key detected")
			return payload, nil
		}
	}

	return nil, fmt.Errorf("timeout waiting for key")
}

func (p *Pipeline) TriggerExport(extraArgs []string, silent bool) error {
	// 使用 Go 原生导出，不再调用 echotrace GUI
	// 解析参数
	var wechatDir, startDate, endDate string
	for i := 0; i < len(extraArgs); i++ {
		switch extraArgs[i] {
		case "--wechat-dir":
			if i+1 < len(extraArgs) {
				wechatDir = extraArgs[i+1]
				i++
			}
		case "--start-date":
			if i+1 < len(extraArgs) {
				startDate = extraArgs[i+1]
				i++
			}
		case "--end-date":
			if i+1 < len(extraArgs) {
				endDate = extraArgs[i+1]
				i++
			}
		}
	}
	return p.ExportAuto(wechatDir, startDate, endDate)
}

// TriggerExportLegacy 保留原来的 echotrace GUI 调用（备用）
func (p *Pipeline) TriggerExportLegacy(extraArgs []string, silent bool) error {
	return p.Echotrace.Run(extraArgs, silent)
}

func (p *Pipeline) RunSummary(startDate, endDate string, incremental bool, questions []string) (map[string]any, error) {
	// 1. Build Dataset
	builder := dataset.NewBuilder(p.Config.ExportDir, p.State)

	// Determine allowed sessions from config if not empty
	// allowedSessions := p.Config.TargetSessions
	var allowedSessions []string

	fmt.Println("🔍 Building dataset...")
	result, err := builder.Build(startDate, endDate, incremental, allowedSessions)
	if err != nil {
		return nil, fmt.Errorf("build dataset: %w", err)
	}

	if len(result.Dataset) == 0 {
		return map[string]any{
			"status":  "no_data",
			"message": "No messages found to summarize",
		}, nil
	}

	fmt.Printf("📊 Found %d sessions with new messages.\n", len(result.Dataset))

	// 2. Initialize LLM & Summarizer
	llmClient := llm.NewClient(p.Config.LLM)
	summ := summarizer.New(llmClient)

	// 3. Summarize each session
	summaries := make(map[string]string)
	ctx := context.Background()

	for sessionName, messages := range result.Dataset {
		fmt.Printf("🤖 Summarizing %s (%d messages)...\n", sessionName, len(messages))
		summary, err := summ.SummarizeSession(ctx, sessionName, messages)
		if err != nil {
			return nil, fmt.Errorf("summarize %s: %w", sessionName, err)
		}
		summaries[sessionName] = summary
	}

	// 4. Save results
	outputFile := filepath.Join(p.Config.ExportDir, "summary_result_go.md")
	if err := saveSummaryMarkdown(outputFile, summaries); err != nil {
		return nil, fmt.Errorf("save summary: %w", err)
	}

	// 5. Save state
	if incremental {
		updates := make(map[string]state.SessionState, len(result.Stats))
		for _, stat := range result.Stats {
			updates[stat.SessionID] = state.SessionState{LastTimestamp: stat.LastTimestamp, LastCount: stat.Messages}
		}
		if err := p.State.CommitSummaries(updates); err != nil {
			return nil, fmt.Errorf("save summary progress: %w", err)
		}
	}

	return map[string]any{
		"status":      "success",
		"output_file": outputFile,
		"stats":       result.Stats,
	}, nil
}

func saveSummaryMarkdown(path string, summaries map[string]string) error {
	var sb strings.Builder
	sb.WriteString("# Chat Summaries\n\n")
	sb.WriteString(fmt.Sprintf("Generated at: %s\n\n", time.Now().Format(time.DateTime)))

	names := make([]string, 0, len(summaries))
	for name := range summaries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		summary := summaries[name]
		sb.WriteString(fmt.Sprintf("## %s\n\n", name))
		sb.WriteString(summary)
		sb.WriteString("\n\n---\n\n")
	}

	return atomicfile.Write(path, []byte(sb.String()), 0600)
}

func (p *Pipeline) GetStatus() map[string]any {
	return map[string]any{
		"config_path": p.Config.ConfigPath,
		"export_dir":  p.Config.ExportDir,
		"state_file":  p.Config.StateFile,
		"platform":    runtime.GOOS,
		"capabilities": map[string]bool{
			"native_wechat":   runtime.GOOS == "windows",
			"analyze_exports": true,
			"filtered_export": false,
		},
	}
}

func (p *Pipeline) LaunchWxKey(extraArgs []string, wait bool) error {
	return p.WxKey.Launch(wait)
}

func (p *Pipeline) RunFullRefresh(autoLaunchKey bool, waitSeconds, pollInterval int, exportArgs []string, logCallback func(string)) (map[string]any, error) {
	// 1. Refresh Key
	key, err := p.RefreshKey(autoLaunchKey, waitSeconds, pollInterval)
	if err != nil {
		return nil, fmt.Errorf("refresh key: %w", err)
	}

	// 2. 使用 Go 原生导出 (ExportAuto)，不再调用 echotrace GUI
	// 解析 exportArgs 获取可能的参数
	var wechatDir, startDate, endDate string
	for i := 0; i < len(exportArgs); i++ {
		switch exportArgs[i] {
		case "--wechat-dir":
			if i+1 < len(exportArgs) {
				wechatDir = exportArgs[i+1]
				i++
			}
		case "--start-date":
			if i+1 < len(exportArgs) {
				startDate = exportArgs[i+1]
				i++
			}
		case "--end-date":
			if i+1 < len(exportArgs) {
				endDate = exportArgs[i+1]
				i++
			}
		}
	}

	logText, exportErr := p.captureLogs(func() error {
		return p.ExportAuto(wechatDir, startDate, endDate)
	}, logCallback)

	// 统计导出结果
	sessions, _ := p.ListSessions()

	// 计算总消息数
	totalMessages := 0
	for _, s := range sessions {
		// 尝试多种类型，因为 JSON 解析可能返回不同类型
		if count, ok := s["messages"].(int); ok {
			totalMessages += count
		} else if count, ok := s["messages"].(int64); ok {
			totalMessages += int(count)
		} else if count, ok := s["messages"].(float64); ok {
			totalMessages += int(count)
		}
	}

	// 返回前端期望的格式
	result := map[string]any{
		"status":        "success",
		"key":           key,
		"export":        exportErr == nil, // 前端检查这个字段
		"dataset_ready": len(sessions) > 0,
		"sessions":      len(sessions),
		"messages":      totalMessages,
	}

	if exportErr != nil {
		result["export_error"] = exportErr.Error()
	}
	if logs := splitLogs(logText); len(logs) > 0 {
		result["logs"] = logs
	}

	return result, nil
}

func (p *Pipeline) captureLogs(fn func() error, logCallback func(string)) (string, error) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			buf.WriteString(line + "\n")
			fmt.Fprintln(origStdout, line)
			if logCallback != nil {
				logCallback(line)
			}
		}
		close(done)
	}()

	runErr := fn()

	_ = w.Close()
	os.Stdout = origStdout
	<-done

	return buf.String(), runErr
}

func splitLogs(raw string) []string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if cleaned == "" {
		return nil
	}
	return strings.Split(cleaned, "\n")
}

func (p *Pipeline) ListSessions() ([]map[string]any, error) {
	builder := dataset.NewBuilder(p.Config.ExportDir, p.State)
	return builder.ListSessionsMetadata()
}

func (p *Pipeline) ExportAuto(wechatDir, startDate, endDate string) error {
	// 获取密钥
	keyPayload, err := p.WxKey.LoadKeys()
	if err != nil || keyPayload.DbKey == "" {
		fmt.Println("⚠️ Database key not found, refreshing...")
		keyPayload, err = p.RefreshKey(true, 120, 5)
		if err != nil {
			return fmt.Errorf("get database key: %w", err)
		}
	}

	fmt.Printf("✓ Using database key: %s...\n", keyPayload.DbKey[:16])

	wechatDir = strings.TrimSpace(wechatDir)
	if wechatDir != "" {
		resolved, err := p.resolveWeChatDataDir(wechatDir)
		if err != nil {
			return fmt.Errorf("invalid --wechat-dir %s: %w", wechatDir, err)
		}
		wechatDir = resolved
	} else if strings.TrimSpace(p.Config.WeChatDataPath) != "" {
		configured := strings.TrimSpace(p.Config.WeChatDataPath)
		resolved, err := p.resolveWeChatDataDir(configured)
		if err != nil {
			fmt.Printf("⚠️ Configured wechat_data_path invalid (%s): %v\n", configured, err)
		} else {
			wechatDir = resolved
		}
	}

	// 自动检测微信数据目录
	if wechatDir == "" {
		wechatDir, err = p.detectWeChatDataDir()
		if err != nil {
			return fmt.Errorf("detect WeChat data directory: %w", err)
		}
	}

	fmt.Printf("✓ WeChat data directory: %s\n", wechatDir)

	// 创建导出器
	exporter := wxdb.NewExporter(wxdb.ExportConfig{
		WeChatDataDir: wechatDir,
		DBKey:         keyPayload.DbKey,
		OutputDir:     p.Config.ExportDir,
		StartDate:     startDate,
		EndDate:       endDate,
		Incremental:   false, // 默认完全导出，不使用增量模式
		StateStore:    p.State,
	})

	// 执行导出
	if err := exporter.Export(); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	fmt.Println("✓ Export completed successfully")
	return nil
}

func (p *Pipeline) resolveWeChatDataDir(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("wechat directory is empty")
	}

	path := expandHome(trimmed)
	if !filepath.IsAbs(path) {
		base := strings.TrimSpace(p.Config.ConfigPath)
		if base != "" {
			path = filepath.Join(filepath.Dir(base), path)
		}
	}
	path = filepath.Clean(path)

	stat, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	if !stat.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}

	// If the user already selected an account directory, it will contain db_storage.
	dbStoragePath := filepath.Join(path, "db_storage")
	if stat, err := os.Stat(dbStoragePath); err == nil && stat.IsDir() {
		return path, nil
	}

	// Otherwise treat it as a base directory (e.g. xwechat_files / WeChat Files) and locate an account folder.
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("read directory %s: %w", path, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		accountName := entry.Name()
		if accountName == "All Users" || accountName == "Applet" {
			continue
		}

		accountDir := filepath.Join(path, accountName)
		dbStoragePath := filepath.Join(accountDir, "db_storage")
		if stat, err := os.Stat(dbStoragePath); err == nil && stat.IsDir() {
			return accountDir, nil
		}
	}

	return "", fmt.Errorf("no WeChat account directory found under %s (expected <account>\\\\db_storage)", path)
}

func expandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	trimmed := strings.TrimPrefix(path, "~")
	return filepath.Join(home, trimmed)
}

func (p *Pipeline) detectWeChatDataDir() (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get user home: %w", err)
	}

	// 微信数据可能的位置
	possiblePaths := []string{
		// 常见路径0: <User>\\xwechat_files (新版微信，用户可能改到用户目录根下)
		filepath.Join(userHome, "xwechat_files"),
		// 常见路径1: Documents\xwechat_files (新版微信)
		filepath.Join(userHome, "Documents", "xwechat_files"),
		// 常见路径1b: <User>\\WeChat Files (旧版微信，用户可能改到用户目录根下)
		filepath.Join(userHome, "WeChat Files"),
		// 常见路径2: Documents\WeChat Files (旧版微信)
		filepath.Join(userHome, "Documents", "WeChat Files"),
		// 常见路径3: AppData\Local\WeChat\WeChat Files
		filepath.Join(userHome, "AppData", "Local", "WeChat", "WeChat Files"),
	}

	fmt.Println("🔍 Searching for WeChat data directory...")

	for _, basePath := range possiblePaths {
		fmt.Printf("  Checking: %s\n", basePath)

		wechatDir, err := p.resolveWeChatDataDir(basePath)
		if err != nil {
			continue
		}
		fmt.Printf("  ✓ Found: %s\n", wechatDir)
		return wechatDir, nil
	}

	return "", fmt.Errorf("WeChat data directory not found. Please specify with --wechat-dir.\n\nTried locations:\n  - %s",
		strings.Join(possiblePaths, "\n  - "))
}

func (p *Pipeline) AnalyzeChat(startDate, endDate string, sessionNames []string, maxTokens int, outputFile string, chunkPrompt, finalPrompt string) (*analyzer.AnalyzeResult, error) {
	// 创建 LLM 客户端
	llmClient := llm.NewClient(p.Config.LLM)

	// 创建分析器
	a := analyzer.New(p.Config.ExportDir, llmClient)

	// 执行分析
	ctx := context.Background()
	result, err := a.Analyze(ctx, analyzer.AnalyzeOptions{
		StartDate:    startDate,
		EndDate:      endDate,
		SessionNames: sessionNames,
		MaxTokens:    maxTokens,
		OutputFile:   outputFile,
		ChunkPrompt:  chunkPrompt,
		FinalPrompt:  finalPrompt,
	})

	if err != nil {
		return nil, fmt.Errorf("analyze chat: %w", err)
	}

	// 确保分析结果文件也出现在历史目录（便于前端读取）
	historyDir := p.Config.SummaryHistoryDir
	if historyDir == "" {
		// 与用户约定：analysis 文件夹位于 export_dir/analysis
		historyDir = filepath.Join(p.Config.ExportDir, "analysis")
	}
	// 如果输出文件存在且不在历史目录中，则复制一份到历史目录
	if result.OutputFile != "" {
		destDir := historyDir
		if err := os.MkdirAll(destDir, 0755); err == nil {
			base := filepath.Base(result.OutputFile)
			destination := filepath.Join(destDir, base)
			// 只有当源和目标不同且目标不存在时才复制
			if !strings.HasPrefix(result.OutputFile, destDir) {
				if _, err := os.Stat(destination); os.IsNotExist(err) {
					data, err := os.ReadFile(result.OutputFile)
					if err == nil {
						_ = os.WriteFile(destination, data, 0644)
					}
				}
			}
		}
	}

	return result, nil
}

// AnalyzeChatStream 流式分析聊天记录，通过 callback 发送事件
func (p *Pipeline) AnalyzeChatStream(ctx context.Context, startDate, endDate string, sessionNames []string, maxTokens int, outputFile string, chunkPrompt, finalPrompt string, callback analyzer.StreamCallback) (*analyzer.AnalyzeResult, error) {
	llmClient := llm.NewClient(p.Config.LLM)
	a := analyzer.New(p.Config.ExportDir, llmClient)

	result, err := a.AnalyzeStream(ctx, analyzer.AnalyzeOptions{
		StartDate:    startDate,
		EndDate:      endDate,
		SessionNames: sessionNames,
		MaxTokens:    maxTokens,
		OutputFile:   outputFile,
		ChunkPrompt:  chunkPrompt,
		FinalPrompt:  finalPrompt,
	}, callback)

	if err != nil {
		return nil, fmt.Errorf("analyze chat stream: %w", err)
	}

	return result, nil
}

func (p *Pipeline) ListExportStates() ([]map[string]any, error) {
	// Scan export directory and collect session metadata with export state
	sessions, err := p.ListSessions()
	if err != nil {
		return nil, err
	}

	// Enhance with state information
	for i := range sessions {
		sessionID := fmt.Sprintf("%v", sessions[i]["session_id"])
		if sessionID != "" {
			lastTS := p.State.LastTimestamp(sessionID)
			if lastTS > 0 {
				sessions[i]["last_export_time"] = time.Unix(lastTS, 0).Format(time.RFC3339)
				sessions[i]["state_file_exists"] = true
			}
		}
	}

	return sessions, nil
}

func (p *Pipeline) DescribeSession(file, startDate, endDate string) (map[string]any, error) {
	// Read and analyze a specific session file
	// This is a placeholder - implement based on your needs
	return map[string]any{
		"file":       file,
		"start_date": startDate,
		"end_date":   endDate,
		"message":    "Session description not yet implemented",
	}, nil
}

func (p *Pipeline) summaryHistoryDirs() []string {
	historyDir := p.Config.SummaryHistoryDir
	dirsToScan := []string{}
	if historyDir != "" {
		// 配置了自定义历史目录，优先使用，同时兼容默认目录
		dirsToScan = append(dirsToScan, historyDir)
	}
	// 默认目录始终兜底，保证能找到 output/analysis 下的历史文件
	dirsToScan = append(dirsToScan,
		filepath.Join(p.Config.ExportDir, "analysis"),
		filepath.Join(p.Config.ExportDir, "chat_history", "analysis"),
	)
	return dirsToScan
}

func (p *Pipeline) ListSummaryHistory(limit int) ([]map[string]any, error) {

	seen := make(map[string]bool)
	history := make([]map[string]any, 0)

	for _, dir := range p.summaryHistoryDirs() {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			// 如果目录不存在或无法读取，跳过而不是直接报错（保持鲁棒性）
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".md") && !strings.HasSuffix(strings.ToLower(name), ".txt") {
				continue
			}

			fullPath := filepath.Join(dir, name)
			if seen[fullPath] {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			history = append(history, map[string]any{
				"name":     name,
				"path":     fullPath,
				"size":     info.Size(),
				"modified": info.ModTime().Format(time.RFC3339),
			})
			seen[fullPath] = true

		}
	}
	// Apply the limit after sorting across all history directories.
	sort.SliceStable(history, func(i, j int) bool {
		left, _ := time.Parse(time.RFC3339, history[i]["modified"].(string))
		right, _ := time.Parse(time.RFC3339, history[j]["modified"].(string))
		return left.After(right)
	})
	if limit > 0 && len(history) > limit {
		history = history[:limit]
	}

	return history, nil
}

func (p *Pipeline) ReadSummaryHistory(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve history file: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	if ext != ".md" && ext != ".txt" {
		return "", fmt.Errorf("history files must be Markdown or text")
	}
	for _, dir := range p.summaryHistoryDirs() {
		rootPath, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootPath, absPath)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		// OpenRoot prevents symlink traversal outside the allowed directory,
		// including a symlink changed between validation and the file open.
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			continue
		}
		file, err := root.Open(rel)
		root.Close()
		if err != nil {
			return "", fmt.Errorf("open history file: %w", err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("history path is not a regular file")
		}
		content, err := io.ReadAll(file)
		if err != nil {
			return "", fmt.Errorf("read history file: %w", err)
		}
		return string(content), nil
	}
	return "", fmt.Errorf("history file is outside configured history directories")
}

var ErrFilteredExportNotImplemented = errors.New("筛选导出尚未实现，未生成或修改任何文件")

func (p *Pipeline) ExportFiltered(selected, files []string, startDate, endDate, outputDir string, singleFile bool) (map[string]any, error) {
	return nil, ErrFilteredExportNotImplemented
}

func (p *Pipeline) GetConfig() map[string]any {
	return map[string]any{
		"path": p.Config.ConfigPath,
		"data": map[string]any{
			"wechat_data_path":    p.Config.WeChatDataPath,
			"export_dir":          p.Config.ExportDir,
			"summary_output":      p.Config.SummaryOutput,
			"summary_history_dir": p.Config.SummaryHistoryDir,
			"state_file":          p.Config.StateFile,
			"llm": map[string]any{
				"base_url": p.Config.LLM.BaseURL,
				"model":    p.Config.LLM.Model,
				"api_key":  p.Config.LLM.APIKey,
				"temperature": func() float64 {
					if p.Config.LLM.Temperature == 0 {
						return 0.7
					}
					return p.Config.LLM.Temperature
				}(),
			},
		},
	}
}

func (p *Pipeline) UpdateConfig(values map[string]any) (map[string]any, error) {
	// Stage changes on a separate config; failed persistence must leave the live
	// process using its original settings.
	stagedConfig := *p.Config
	staged := *p
	staged.Config = &stagedConfig
	if err := staged.applyConfig(values); err != nil {
		return nil, err
	}
	p.Config = staged.Config
	p.State = staged.State
	return map[string]any{
		"status": "success", "message": "Configuration updated",
	}, nil
}

func (p *Pipeline) applyConfig(values map[string]any) error {
	previousStatePath := p.Config.StateFile
	// Update configuration values
	// This is a simplified implementation - you may want to add validation
	if v, ok := values["wechat_data_path"].(string); ok {
		p.Config.WeChatDataPath = p.resolvePathRelativeToConfig(v)
	}
	if v, ok := values["export_dir"].(string); ok {
		p.Config.ExportDir = p.resolvePathRelativeToConfig(v)
	}
	if v, ok := values["summary_output"].(string); ok {
		p.Config.SummaryOutput = p.resolvePathRelativeToConfig(v)
	}
	if v, ok := values["summary_history_dir"].(string); ok {
		p.Config.SummaryHistoryDir = p.resolvePathRelativeToConfig(v)
	}
	if v, ok := values["state_file"].(string); ok {
		p.Config.StateFile = p.resolvePathRelativeToConfig(v)
	}

	// Update LLM config
	if llmData, ok := values["llm"].(map[string]any); ok {
		if v, ok := llmData["base_url"].(string); ok {
			p.Config.LLM.BaseURL = v
		}
		if v, ok := llmData["model"].(string); ok {
			p.Config.LLM.Model = v
		}
		if v, ok := llmData["api_key"].(string); ok {
			p.Config.LLM.APIKey = v
		}
		if v, ok := llmData["temperature"].(float64); ok {
			p.Config.LLM.Temperature = v
		}
		if v, ok := llmData["timeout"].(float64); ok {
			p.Config.LLM.Timeout = time.Duration(v * float64(time.Second))
		}
	}

	if p.Config.StateFile != previousStatePath {
		stagedState := state.New(p.Config.StateFile)
		if err := stagedState.Load(); err != nil {
			return fmt.Errorf("load updated state file: %w", err)
		}
		p.State = stagedState
	}
	if err := p.saveConfigToFile(values); err != nil {
		return err
	}
	return nil
}

func (p *Pipeline) resolvePathRelativeToConfig(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	path := expandHome(trimmed)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := strings.TrimSpace(p.Config.ConfigPath)
	if base == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(base), path))
}

func (p *Pipeline) saveConfigToFile(values map[string]any) error {
	if strings.TrimSpace(p.Config.ConfigPath) == "" {
		return fmt.Errorf("no config path set")
	}

	raw, err := os.ReadFile(p.Config.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", p.Config.ConfigPath, err)
	}

	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("parse config %s: %w", p.Config.ConfigPath, err)
	}
	if data == nil {
		return fmt.Errorf("configuration must be a JSON object")
	}

	for key, value := range values {
		if key == "llm" {
			updates, ok := value.(map[string]any)
			if !ok {
				continue
			}
			existing, _ := data["llm"].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
				data["llm"] = existing
			}
			for nestedKey, nestedValue := range updates {
				existing[nestedKey] = nestedValue
				// Legacy flat settings otherwise override updated nested settings
				// when loading the saved configuration after a restart.
				delete(data, "llm_"+nestedKey)
			}
			continue
		}
		data[key] = value
	}

	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := atomicfile.Write(p.Config.ConfigPath, encoded, 0600); err != nil {
		return fmt.Errorf("write config %s: %w", p.Config.ConfigPath, err)
	}
	return nil
}

func (p *Pipeline) ReloadConfig() (map[string]any, error) {
	if p.Config.ConfigPath == "" {
		return nil, fmt.Errorf("no config path set")
	}

	newConfig, err := config.LoadPipeline(p.Config.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("reload config: %w", err)
	}

	// Build and validate the dependent state and runners before publishing the
	// new configuration. Updating Config alone leaves them on the old paths.
	staged, err := New(newConfig)
	if err != nil {
		return nil, fmt.Errorf("reload dependencies: %w", err)
	}
	p.Config = staged.Config
	p.State = staged.State
	p.WxKey = staged.WxKey
	p.Echotrace = staged.Echotrace

	return map[string]any{
		"status":  "success",
		"message": "Configuration reloaded",
	}, nil
}

func (p *Pipeline) TestLLM(baseURL, model, apiKey string) (map[string]any, error) {
	// Create a test LLM config
	testConfig := config.LLMConfig{
		BaseURL:     baseURL,
		Model:       model,
		APIKey:      apiKey,
		Temperature: 0.7,
		Timeout:     30 * time.Second,
	}

	// Use existing config values if not provided
	if testConfig.BaseURL == "" {
		testConfig.BaseURL = p.Config.LLM.BaseURL
	}
	if testConfig.Model == "" {
		testConfig.Model = p.Config.LLM.Model
	}
	if testConfig.APIKey == "" {
		testConfig.APIKey = p.Config.LLM.APIKey
	}

	client := llm.NewClient(testConfig)
	ctx := context.Background()

	// Send a simple test message
	response, err := client.ChatCompletion(ctx, []llm.ChatMessage{
		{Role: "user", Content: "Hello! Please respond with 'OK' if you receive this message."},
	})

	if err != nil {
		return map[string]any{
			"status":  "failed",
			"error":   err.Error(),
			"message": "大模型服务器连接失败",
		}, nil
	}

	return map[string]any{
		"status":   "success",
		"response": response,
		"message":  "大模型服务器连接成功",
	}, nil
}

func (p *Pipeline) AnalyzeSessions(startDate, endDate string, topN int, sessionTypes []string) (map[string]any, error) {
	// 直接从 JSON 文件读取完整的消息数据进行统计
	files, err := filepath.Glob(filepath.Join(p.Config.ExportDir, "chat_history", "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob export dir (chat_history): %w", err)
	}
	if len(files) == 0 {
		files, err = filepath.Glob(filepath.Join(p.Config.ExportDir, "*.json"))
		if err != nil {
			return nil, fmt.Errorf("glob export dir: %w", err)
		}
	}

	// 解析日期范围
	var startTs, endTs int64
	if startDate != "" {
		if t, err := time.Parse("2006-01-02", startDate); err == nil {
			startTs = t.Unix()
		}
	}
	if endDate != "" {
		if t, err := time.Parse("2006-01-02", endDate); err == nil {
			endTs = t.Add(24*time.Hour - time.Second).Unix() // 当天结束
		}
	}

	// 统计数据结构
	type SessionStats struct {
		SessionID        string
		DisplayName      string
		SessionType      string
		TotalMessages    int
		TextMessages     int
		ImageMessages    int
		VoiceMessages    int
		VideoMessages    int
		OtherMessages    int
		SentMessages     int
		ReceivedMessages int
		FirstMessageTime int64
		LastMessageTime  int64
		ActiveDays       map[string]bool // 活跃日期集合
	}

	sessionStats := make(map[string]*SessionStats)
	hourlyDistribution := make(map[int]int)     // 24小时分布
	weekdayDistribution := make(map[int]int)    // 星期分布
	monthlyDistribution := make(map[string]int) // 月份分布

	// 初始化小时分布
	for i := 0; i < 24; i++ {
		hourlyDistribution[i] = 0
	}
	// 初始化星期分布 (1-7, 1=周一)
	for i := 1; i <= 7; i++ {
		weekdayDistribution[i] = 0
	}

	totalMessages := 0
	groupCount := 0
	privateCount := 0

	for _, file := range files {
		if strings.HasSuffix(file, ".export_state") {
			continue
		}

		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}

		var session struct {
			SessionID   string `json:"session_id"`
			SessionName string `json:"session_name"`
			Messages    []struct {
				CreateTime    int64  `json:"create_time"`
				FormattedTime string `json:"formatted_time"`
				Type          string `json:"type"`
				StrContent    string `json:"str_content"`
				IsSend        int    `json:"is_send"`
				Sender        string `json:"sender,omitempty"`
				SenderName    string `json:"sender_name,omitempty"`
			} `json:"messages"`
		}

		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		// 过滤公众号
		if strings.HasPrefix(session.SessionID, "gh_") {
			continue
		}

		sessionID := session.SessionID
		sessionType := "private"
		if strings.Contains(sessionID, "@chatroom") {
			sessionType = "group"
		}

		// 过滤会话类型
		if len(sessionTypes) > 0 {
			found := false
			for _, st := range sessionTypes {
				if st == sessionType {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// 初始化或获取会话统计
		stats, exists := sessionStats[sessionID]
		if !exists {
			stats = &SessionStats{
				SessionID:   sessionID,
				DisplayName: session.SessionName,
				SessionType: sessionType,
				ActiveDays:  make(map[string]bool),
			}
			sessionStats[sessionID] = stats

			if sessionType == "group" {
				groupCount++
			} else {
				privateCount++
			}
		}

		// 遍历消息进行统计
		for _, msg := range session.Messages {
			// 时间过滤
			if startTs > 0 && msg.CreateTime < startTs {
				continue
			}
			if endTs > 0 && msg.CreateTime > endTs {
				continue
			}

			totalMessages++
			stats.TotalMessages++

			// 消息类型统计
			switch {
			case msg.Type == "文本消息" || msg.Type == "文本":
				stats.TextMessages++
			case msg.Type == "图片消息" || msg.Type == "图片":
				stats.ImageMessages++
			case msg.Type == "语音消息" || msg.Type == "语音":
				stats.VoiceMessages++
			case msg.Type == "视频消息" || msg.Type == "视频":
				stats.VideoMessages++
			default:
				stats.OtherMessages++
			}

			// 发送/接收统计
			if msg.IsSend == 1 {
				stats.SentMessages++
			} else {
				stats.ReceivedMessages++
			}

			// 时间范围统计
			if stats.FirstMessageTime == 0 || msg.CreateTime < stats.FirstMessageTime {
				stats.FirstMessageTime = msg.CreateTime
			}
			if msg.CreateTime > stats.LastMessageTime {
				stats.LastMessageTime = msg.CreateTime
			}

			// 活跃天统计
			msgTime := time.Unix(msg.CreateTime, 0)
			dateStr := msgTime.Format("2006-01-02")
			stats.ActiveDays[dateStr] = true

			// 时间分布统计
			hour := msgTime.Hour()
			hourlyDistribution[hour]++

			weekday := int(msgTime.Weekday())
			if weekday == 0 {
				weekday = 7 // 周日转为7
			}
			weekdayDistribution[weekday]++

			monthStr := msgTime.Format("2006-01")
			monthlyDistribution[monthStr]++
		}
	}

	// 排序并获取 Top N
	var allSessions []*SessionStats
	for _, s := range sessionStats {
		allSessions = append(allSessions, s)
	}

	// 按消息数排序
	for i := 0; i < len(allSessions)-1; i++ {
		for j := 0; j < len(allSessions)-i-1; j++ {
			if allSessions[j].TotalMessages < allSessions[j+1].TotalMessages {
				allSessions[j], allSessions[j+1] = allSessions[j+1], allSessions[j]
			}
		}
	}

	// 获取 Top N
	if topN > len(allSessions) {
		topN = len(allSessions)
	}
	topSessions := allSessions[:topN]

	// 转换为 map 格式
	topMaps := make([]map[string]any, 0, len(topSessions))
	for _, s := range topSessions {
		topMaps = append(topMaps, map[string]any{
			"session_id":        s.SessionID,
			"display_name":      s.DisplayName,
			"session_type":      s.SessionType,
			"messages":          s.TotalMessages,
			"text_messages":     s.TextMessages,
			"image_messages":    s.ImageMessages,
			"voice_messages":    s.VoiceMessages,
			"video_messages":    s.VideoMessages,
			"other_messages":    s.OtherMessages,
			"sent_messages":     s.SentMessages,
			"received_messages": s.ReceivedMessages,
			"active_days":       len(s.ActiveDays),
			"first_message":     s.FirstMessageTime,
			"last_message":      s.LastMessageTime,
		})
	}

	// 转换时间分布为数组格式（前端图表需要）
	hourlyArray := make([]int, 24)
	for i := 0; i < 24; i++ {
		hourlyArray[i] = hourlyDistribution[i]
	}

	return map[string]any{
		"top": topMaps,
		"breakdown": map[string]any{
			"group":   groupCount,
			"private": privateCount,
		},
		"total_messages":       totalMessages,
		"session_count":        len(sessionStats),
		"hourly_distribution":  hourlyArray,
		"weekday_distribution": weekdayDistribution,
		"monthly_distribution": monthlyDistribution,
		"window": map[string]any{
			"start": startDate,
			"end":   endDate,
		},
	}, nil
}
