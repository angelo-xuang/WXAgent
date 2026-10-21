package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/pipeline"
)

type Server struct {
	pipeline *pipeline.Pipeline
	mux      *http.ServeMux
}

func New(p *pipeline.Pipeline) *Server {
	s := &Server{
		pipeline: p,
		mux:      http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Run(addr string) error {
	log.Printf("Starting server on %s", addr)
	return (&http.Server{
		Addr: addr, Handler: s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		// Streaming responses may run for minutes; avoid a fixed WriteTimeout.
	}).ListenAndServe()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/status", s.handleStatus)
	s.mux.HandleFunc("/refresh-key", nativeOperation(runtime.GOOS, s.handleRefreshKey))
	s.mux.HandleFunc("/wx-key/launch", nativeOperation(runtime.GOOS, s.handleLaunchWxKey))
	s.mux.HandleFunc("/export", nativeOperation(runtime.GOOS, s.handleExport))
	s.mux.HandleFunc("/export/states", s.handleExportStates)
	s.mux.HandleFunc("/full-refresh", nativeOperation(runtime.GOOS, s.handleFullRefresh))
	s.mux.HandleFunc("/full-refresh/stream", nativeOperation(runtime.GOOS, s.handleFullRefreshStream))
	s.mux.HandleFunc("/sessions", s.handleListSessions)
	s.mux.HandleFunc("/sessions/describe", s.handleDescribeSession)
	s.mux.HandleFunc("/summary/run", s.handleRunSummary)
	s.mux.HandleFunc("/summary/run-stream", s.handleRunSummaryStream)
	s.mux.HandleFunc("/summary/history", s.handleSummaryHistory)
	s.mux.HandleFunc("/summary/history/content", s.handleSummaryHistoryContent)
	s.mux.HandleFunc("/summary/export", s.handleExportFiltered)
	s.mux.HandleFunc("/config", s.handleConfig)
	s.mux.HandleFunc("/config/reload", s.handleConfigReload)
	s.mux.HandleFunc("/llm/test", s.handleTestLLM)
	s.mux.HandleFunc("/analysis/sessions", s.handleAnalyzeSessions)
}

func nativeOperation(platform string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if platform != "windows" {
			http.Error(w, "此平台暂不支持原生微信数据接入，请配置已导出的聊天记录目录后分析", http.StatusNotImplemented)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.pipeline.GetStatus())
}

type RefreshKeyRequest struct {
	AutoLaunch   bool `json:"auto_launch"`
	WaitSeconds  int  `json:"wait_seconds"`
	PollInterval int  `json:"poll_interval"`
}

func (s *Server) handleRefreshKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RefreshKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Defaults
	if req.WaitSeconds == 0 {
		req.WaitSeconds = 120
	}
	if req.PollInterval == 0 {
		req.PollInterval = 5
	}

	key, err := s.pipeline.RefreshKey(req.AutoLaunch, req.WaitSeconds, req.PollInterval)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, key)
}

type LaunchWxKeyRequest struct {
	Wait      bool     `json:"wait"`
	ExtraArgs []string `json:"extra_args"`
}

func (s *Server) handleLaunchWxKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LaunchWxKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.pipeline.LaunchWxKey(req.ExtraArgs, req.Wait); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]any{"status": "launched", "waited": req.Wait})
}

type TriggerExportRequest struct {
	ExtraArgs []string `json:"extra_args"`
	Silent    bool     `json:"silent"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TriggerExportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.pipeline.TriggerExport(req.ExtraArgs, req.Silent); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]any{"success": true})
}

type FullRefreshRequest struct {
	AutoLaunchKey bool     `json:"auto_launch_key"`
	WaitSeconds   int      `json:"wait_seconds"`
	PollInterval  int      `json:"poll_interval"`
	ExportArgs    []string `json:"export_args"`
}

func (s *Server) handleFullRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req FullRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Defaults
	if req.WaitSeconds == 0 {
		req.WaitSeconds = 90
	}
	if req.PollInterval == 0 {
		req.PollInterval = 3
	}

	res, err := s.pipeline.RunFullRefresh(req.AutoLaunchKey, req.WaitSeconds, req.PollInterval, req.ExportArgs, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, res)
}

// SSE 导出流式端点：实时推送日志
func (s *Server) handleFullRefreshStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req FullRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.WaitSeconds == 0 {
		req.WaitSeconds = 90
	}
	if req.PollInterval == 0 {
		req.PollInterval = 3
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	sendEvent := func(eventType string, data any) {
		jsonData, _ := json.Marshal(data)
		w.Write([]byte("event: " + eventType + "\n"))
		w.Write([]byte("data: " + string(jsonData) + "\n\n"))
		flusher.Flush()
	}

	result, err := s.pipeline.RunFullRefresh(
		req.AutoLaunchKey,
		req.WaitSeconds,
		req.PollInterval,
		req.ExportArgs,
		func(line string) {
			sendEvent("log", map[string]string{"content": line})
		},
	)
	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	sendEvent("result", result)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.pipeline.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, sessions)
}

type SummarizeRequest struct {
	StartDate    string   `json:"start_date"`
	EndDate      string   `json:"end_date"`
	Incremental  bool     `json:"incremental"`
	Questions    []string `json:"questions"`
	SaveMarkdown bool     `json:"save_markdown"`
	Mode         string   `json:"mode"`     // "merged" (跨会话合并) 或 "per_session" (逐会话)
	Sessions     []string `json:"sessions"` // 可选：指定会话列表
	MaxTokens    int      `json:"max_tokens"`
	ChunkPrompt  string   `json:"chunk_prompt"`
	FinalPrompt  string   `json:"final_prompt"`
}

func (s *Server) handleRunSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SummarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 默认使用 merged 模式（跨会话合并分析）
	if req.Mode == "" || req.Mode == "merged" {
		// 使用跨会话合并分析
		log.Println("🔍 使用跨会话合并分析模式...")

		// 设置默认 MaxTokens
		maxTokens := req.MaxTokens
		if maxTokens == 0 {
			maxTokens = 80000 // DeepSeek 支持 128K，设置 80K 留余量
		}

		result, err := s.pipeline.AnalyzeChat(req.StartDate, req.EndDate, req.Sessions, maxTokens, "", req.ChunkPrompt, req.FinalPrompt)
		if err != nil {
			log.Printf("❌ 跨会话分析失败: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("✅ 跨会话分析完成: %d 条消息, %d 个会话, %d 个分段",
			result.TotalMessages, result.TotalSessions, result.ChunkCount)

		// 返回与前端兼容的格式
		jsonResponse(w, map[string]any{
			"status":         "success",
			"mode":           "merged",
			"content":        result.FinalSummary,
			"output_path":    result.OutputFile,
			"total_messages": result.TotalMessages,
			"total_sessions": result.TotalSessions,
			"chunk_count":    result.ChunkCount,
			"summaries":      result.Summaries,
			"logs":           result.Logs,
		})
		return
	}

	// per_session 模式：逐会话分析（原有逻辑）
	log.Println("📝 使用逐会话分析模式...")
	res, err := s.pipeline.RunSummary(req.StartDate, req.EndDate, req.Incremental, req.Questions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, res)
}

// handleRunSummaryStream SSE 流式分析端点
func (s *Server) handleRunSummaryStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SummarizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// 辅助函数：发送 SSE 事件
	sendEvent := func(eventType string, data any) {
		jsonData, _ := json.Marshal(data)
		w.Write([]byte("event: " + eventType + "\n"))
		w.Write([]byte("data: " + string(jsonData) + "\n\n"))
		flusher.Flush()
	}

	log.Println("🔍 [SSE] 开始流式分析...")

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 80000
	}

	// 使用流式分析
	result, err := s.pipeline.AnalyzeChatStream(
		r.Context(),
		req.StartDate, req.EndDate, req.Sessions, maxTokens, "", req.ChunkPrompt, req.FinalPrompt,
		func(event pipeline.StreamEvent) {
			sendEvent(event.Type, map[string]any{
				"content": event.Content,
				"step":    event.Step,
				"total":   event.Total,
			})
		},
	)

	if err != nil {
		log.Printf("❌ [SSE] 流式分析失败: %v", err)
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}

	// 发送最终结果
	sendEvent("result", map[string]any{
		"status":         "success",
		"mode":           "merged",
		"content":        result.FinalSummary,
		"output_path":    result.OutputFile,
		"total_messages": result.TotalMessages,
		"total_sessions": result.TotalSessions,
		"chunk_count":    result.ChunkCount,
	})

	log.Printf("✅ [SSE] 流式分析完成: %d 条消息, %d 个会话",
		result.TotalMessages, result.TotalSessions)
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding response: %v", err)
	}
}

func (s *Server) handleExportStates(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.pipeline.ListExportStates()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"data": sessions})
}

func (s *Server) handleDescribeSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		File      string `json:"file"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := s.pipeline.DescribeSession(req.File, req.StartDate, req.EndDate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, result)
}

func (s *Server) handleSummaryHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if n, err := json.Number(limitStr).Int64(); err == nil {
			limit = int(n)
		}
	}

	history, err := s.pipeline.ListSummaryHistory(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"data": history})
}

func (s *Server) handleSummaryHistoryContent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path parameter required", http.StatusBadRequest)
		return
	}

	content, err := s.pipeline.ReadSummaryHistory(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"content": content})
}

func (s *Server) handleExportFiltered(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Selected   []string `json:"selected"`
		Files      []string `json:"files"`
		StartDate  string   `json:"start_date"`
		EndDate    string   `json:"end_date"`
		OutputDir  string   `json:"output_dir"`
		SingleFile bool     `json:"single_file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := s.pipeline.ExportFiltered(req.Selected, req.Files, req.StartDate, req.EndDate, req.OutputDir, req.SingleFile)
	if err != nil {
		if errors.Is(err, pipeline.ErrFilteredExportNotImplemented) {
			http.Error(w, err.Error(), http.StatusNotImplemented)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, result)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config := s.pipeline.GetConfig()
		jsonResponse(w, config)
	case http.MethodPut:
		var req struct {
			Values map[string]any `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.pipeline.UpdateConfig(req.Values)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonResponse(w, result)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result, err := s.pipeline.ReloadConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, result)
}

func (s *Server) handleTestLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		BaseURL string `json:"base_url"`
		Model   string `json:"model"`
		APIKey  string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := s.pipeline.TestLLM(req.BaseURL, req.Model, req.APIKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, result)
}

type AnalyzeSessionsRequest struct {
	StartDate    string   `json:"start_date"`
	EndDate      string   `json:"end_date"`
	TopN         int      `json:"top_n"`
	SessionTypes []string `json:"session_types"`
}

func (s *Server) handleAnalyzeSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AnalyzeSessionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.TopN == 0 {
		req.TopN = 10
	}

	result, err := s.pipeline.AnalyzeSessions(req.StartDate, req.EndDate, req.TopN, req.SessionTypes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, result)
}
