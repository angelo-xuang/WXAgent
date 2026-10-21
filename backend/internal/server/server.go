package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/dataset"
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
	s.mux.HandleFunc("/messages", s.handleMessages)
	s.mux.HandleFunc("/stats", s.handleStats)
	s.mux.HandleFunc("/index/status", s.handleIndexStatus)
	s.mux.HandleFunc("/index/import", s.handleImport)
	s.mux.HandleFunc("/config", s.handleConfig)
	s.mux.HandleFunc("/config/reload", s.handleConfigReload)
}

func nativeOperation(platform string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if platform != "windows" {
			http.Error(w, "此平台暂不支持原生微信数据接入，请配置已导出的聊天记录目录后查询", http.StatusNotImplemented)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
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
		requestError(w, err)
		return
	}

	jsonResponse(w, map[string]any{"status": "ready", "timestamp": key.Timestamp})
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
		requestError(w, err)
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
		requestError(w, err)
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
		requestError(w, err)
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

	if req.WaitSeconds < 0 || req.PollInterval < 0 {
		requestError(w, &dataset.ValidationError{Message: "wait_seconds and poll_interval must be positive"})
		return
	}
	if err := pipeline.ValidateExportArgs(req.ExportArgs); err != nil {
		requestError(w, err)
		return
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
	if !method(w, r, http.MethodGet) {
		return
	}
	sessions, err := s.pipeline.ListSessions()
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, sessions)
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Error encoding response: %v", err)
	}
}

func (s *Server) handleExportStates(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	sessions, err := s.pipeline.ListExportStates()
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, map[string]any{"data": sessions})
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
			requestError(w, err)
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
		requestError(w, err)
		return
	}
	jsonResponse(w, result)
}

func (s *Server) Handler() http.Handler { return s.mux }

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	return false
}
func requestError(w http.ResponseWriter, err error) {
	var invalid *dataset.ValidationError
	status := http.StatusInternalServerError
	if errors.As(err, &invalid) {
		status = http.StatusBadRequest
	}
	http.Error(w, err.Error(), status)
}
func queryOptions(r *http.Request) (dataset.QueryOptions, error) {
	query := r.URL.Query()
	opts := dataset.QueryOptions{StartDate: query.Get("start_date"), EndDate: query.Get("end_date"), Keyword: query.Get("keyword")}
	for _, value := range query["session_id"] {
		for _, id := range strings.Split(value, ",") {
			if id != "" {
				opts.Sessions = append(opts.Sessions, id)
			}
		}
	}
	for key, target := range map[string]*int{"offset": &opts.Offset, "limit": &opts.Limit} {
		if value := query.Get(key); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil {
				return opts, &dataset.ValidationError{Message: key + " must be an integer"}
			}
			*target = number
		}
	}
	return opts, dataset.Validate(opts)
}
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	opts, err := queryOptions(r)
	if err != nil {
		requestError(w, err)
		return
	}
	data, err := s.pipeline.QueryMessages(opts)
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, data)
}
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	opts, err := queryOptions(r)
	if err != nil {
		requestError(w, err)
		return
	}
	data, err := s.pipeline.Stats(opts)
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, data)
}
func (s *Server) handleIndexStatus(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	data, err := s.pipeline.IndexStatus()
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, data)
}
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var req struct {
		ExportDir string `json:"export_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := s.pipeline.Import(req.ExportDir)
	if err != nil {
		requestError(w, err)
		return
	}
	jsonResponse(w, data)
}
