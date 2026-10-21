package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/atomicfile"
	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/dataset"
	"github.com/billyoftea/wxagent/go_backend/internal/echotrace"
	"github.com/billyoftea/wxagent/go_backend/internal/state"
	"github.com/billyoftea/wxagent/go_backend/internal/wxdb"
	"github.com/billyoftea/wxagent/go_backend/internal/wxkey"
)

type Pipeline struct {
	mu        sync.RWMutex
	nativeMu  sync.Mutex
	Config    *config.PipelineConfig
	State     *state.Store
	WxKey     *wxkey.Bridge
	Echotrace *echotrace.Runner
}

func New(cfg *config.PipelineConfig) (*Pipeline, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	store := state.New(cfg.StateFile)
	return &Pipeline{Config: cfg, State: store, WxKey: wxkey.New(cfg.WxKeySharedPrefs, cfg.WxKeyCommand), Echotrace: echotrace.New(cfg.EchotraceCommand)}, nil
}

func (p *Pipeline) configSnapshot() *config.PipelineConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Config
}
func (p *Pipeline) GetStatus() map[string]any {
	cfg := p.configSnapshot()
	index, indexErr := dataset.IndexStatus(cfg.DatabasePath)
	result := map[string]any{"index": index, "config_path": cfg.ConfigPath, "export_dir": cfg.ExportDir, "database_path": cfg.DatabasePath, "state_file": cfg.StateFile, "platform": runtime.GOOS, "capabilities": map[string]bool{"native_wechat": runtime.GOOS == "windows", "query_messages": true, "stats": true}}
	if indexErr != nil {
		result["index_error"] = indexErr.Error()
	}
	return result
}
func (p *Pipeline) ListSessions() ([]map[string]any, error) {
	return dataset.SessionsDatabase(p.configSnapshot().DatabasePath)
}
func (p *Pipeline) QueryMessages(opts dataset.QueryOptions) (*dataset.QueryResult, error) {
	return dataset.QueryDatabase(p.configSnapshot().DatabasePath, opts)
}
func (p *Pipeline) Stats(opts dataset.QueryOptions) (*dataset.StatsResult, error) {
	return dataset.StatsDatabase(p.configSnapshot().DatabasePath, opts)
}

func (p *Pipeline) RefreshKey(autoLaunch bool, waitSeconds, pollInterval int) (*wxkey.KeyPayload, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("native WeChat access is currently supported on Windows only")
	}
	if waitSeconds <= 0 || pollInterval <= 0 {
		return nil, &dataset.ValidationError{Message: "wait_seconds and poll_interval must be positive"}
	}
	p.mu.RLock()
	bridge := p.WxKey
	p.mu.RUnlock()
	payload, err := bridge.LoadKeys()
	if err == nil && payload.DbKey != "" {
		return payload, nil
	}
	if !autoLaunch {
		return nil, fmt.Errorf("key not found and auto_launch is false")
	}
	fmt.Fprintln(os.Stderr, "Key not found, launching configured key helper")
	if err := bridge.Launch(false); err != nil {
		return nil, fmt.Errorf("launch wx_key: %w", err)
	}
	timer := time.NewTimer(time.Duration(waitSeconds) * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Duration(pollInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for key")
		case <-ticker.C:
			payload, err := bridge.LoadKeys()
			if err == nil && payload.DbKey != "" {
				return payload, nil
			}
		}
	}
}

func (p *Pipeline) LaunchWxKey(extraArgs []string, wait bool) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("native WeChat access is currently supported on Windows only")
	}
	p.mu.RLock()
	bridge := p.WxKey
	p.mu.RUnlock()
	return bridge.Launch(wait)
}

func exportOptions(args []string) (string, string, string, error) {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		name := args[i]
		if name != "--wechat-dir" && name != "--start-date" && name != "--end-date" {
			return "", "", "", &dataset.ValidationError{Message: "unknown export argument: " + name}
		}
		i++
		if i >= len(args) {
			return "", "", "", &dataset.ValidationError{Message: "missing value for " + name}
		}
		values[name] = args[i]
	}
	_, _, err := dataset.DateRange(values["--start-date"], values["--end-date"])
	return values["--wechat-dir"], values["--start-date"], values["--end-date"], err
}

func (p *Pipeline) TriggerExport(extraArgs []string, silent bool) error {
	dir, start, end, err := exportOptions(extraArgs)
	if err != nil {
		return err
	}
	return p.ExportAuto(dir, start, end)
}

func (p *Pipeline) ExportAuto(wechatDir, startDate, endDate string) error {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	return p.exportAuto(wechatDir, startDate, endDate, os.Stderr)
}

func (p *Pipeline) exportAuto(wechatDir, startDate, endDate string, writer io.Writer) error {
	if _, _, err := dataset.DateRange(startDate, endDate); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("native WeChat access is currently supported on Windows only; query exported JSON on this platform")
	}
	p.mu.RLock()
	cfg, store, bridge := p.Config, p.State, p.WxKey
	p.mu.RUnlock()
	if err := store.Load(); err != nil {
		return fmt.Errorf("load state without overwriting existing data: %w", err)
	}
	payload, err := bridge.LoadKeys()
	if err != nil || payload.DbKey == "" {
		payload, err = p.RefreshKey(true, 120, 5)
		if err != nil {
			return fmt.Errorf("get database key: %w", err)
		}
	}
	wechatDir = strings.TrimSpace(wechatDir)
	if wechatDir != "" {
		wechatDir, err = p.resolveWeChatDataDir(wechatDir)
	} else if cfg.WeChatDataPath != "" {
		wechatDir, err = p.resolveWeChatDataDir(cfg.WeChatDataPath)
	} else {
		wechatDir, err = p.detectWeChatDataDir()
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(writer, "Exporting local WeChat data")
	exporter := wxdb.NewExporter(wxdb.ExportConfig{WeChatDataDir: wechatDir, DBKey: payload.DbKey, OutputDir: cfg.ExportDir, StartDate: startDate, EndDate: endDate, Incremental: false, StateStore: store, LogWriter: writer})
	if err := exporter.Export(); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}
	if _, err := dataset.Import(cfg.ExportDir, cfg.DatabasePath); err != nil {
		return fmt.Errorf("index exported records: %w", err)
	}
	fmt.Fprintln(writer, "Export completed and records indexed")
	return nil
}

type eventWriter struct {
	buffer   bytes.Buffer
	callback func(string)
	lines    []string
}

func (w *eventWriter) Write(data []byte) (int, error) {
	_, _ = os.Stderr.Write(data)
	for _, part := range bytes.SplitAfter(data, []byte{'\n'}) {
		w.buffer.Write(part)
		if len(part) > 0 && part[len(part)-1] == '\n' {
			line := strings.TrimRight(w.buffer.String(), "\r\n")
			w.buffer.Reset()
			w.lines = append(w.lines, line)
			if w.callback != nil {
				w.callback(line)
			}
		}
	}
	return len(data), nil
}
func (p *Pipeline) RunFullRefresh(autoLaunchKey bool, waitSeconds, pollInterval int, exportArgs []string, callback func(string)) (map[string]any, error) {
	dir, start, end, err := exportOptions(exportArgs)
	if err != nil {
		return nil, err
	}
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	key, err := p.RefreshKey(autoLaunchKey, waitSeconds, pollInterval)
	if err != nil {
		return nil, err
	}
	logs := &eventWriter{callback: callback}
	if err := p.exportAuto(dir, start, end, logs); err != nil {
		return nil, err
	}
	stats, err := p.Stats(dataset.QueryOptions{})
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "success", "key_ready": true, "key_timestamp": key.Timestamp, "export": true, "dataset_ready": stats.TotalMessages > 0, "sessions": stats.SessionCount, "messages": stats.TotalMessages, "logs": logs.lines}, nil
}

func (p *Pipeline) ListExportStates() ([]map[string]any, error) {
	p.mu.RLock()
	cfg, store := p.Config, p.State
	p.mu.RUnlock()
	if err := store.Load(); err != nil {
		return nil, err
	}
	sessions, err := dataset.SessionsDatabase(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if timestamp := store.LastTimestamp(session["session_id"].(string)); timestamp > 0 {
			session["last_export_time"] = time.Unix(timestamp, 0).UTC().Format(time.RFC3339)
			session["state_file_exists"] = true
		}
	}
	return sessions, nil
}

func (p *Pipeline) GetConfig() map[string]any {
	cfg := p.configSnapshot()
	return map[string]any{"path": cfg.ConfigPath, "data": map[string]any{"export_dir": cfg.ExportDir, "database_path": cfg.DatabasePath, "state_file": cfg.StateFile, "wechat_data_path": cfg.WeChatDataPath, "wx_key_shared_prefs": cfg.WxKeySharedPrefs, "wx_key_command": map[string]any{"path": cfg.WxKeyCommand.Path, "args": cfg.WxKeyCommand.Args, "cwd": cfg.WxKeyCommand.Cwd}, "echotrace_command": map[string]any{"path": cfg.EchotraceCommand.Path, "args": cfg.EchotraceCommand.Args, "cwd": cfg.EchotraceCommand.Cwd}}}
}

func (p *Pipeline) UpdateConfig(values map[string]any) (map[string]any, error) {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Config.ConfigPath == "" {
		return nil, &dataset.ValidationError{Message: "no configuration file selected; create one with wxagent init"}
	}
	if len(values) == 0 {
		return nil, &dataset.ValidationError{Message: "values must contain a supported setting"}
	}
	allowed := map[string]bool{"database_path": true, "export_dir": true, "state_file": true, "wechat_data_path": true, "wx_key_shared_prefs": true, "wx_key_command": true, "echotrace_command": true}
	for key := range values {
		if !allowed[key] {
			return nil, &dataset.ValidationError{Message: "unsupported setting: " + key}
		}
	}
	raw, err := config.Load(p.Config.ConfigPath)
	if err != nil {
		return nil, err
	}
	data := raw.Data()
	for key, value := range values {
		if key == "wx_key_command" || key == "echotrace_command" {
			update, ok := value.(map[string]any)
			if !ok {
				return nil, &dataset.ValidationError{Message: key + " must be an object"}
			}
			existing, _ := data[key].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
			}
			for nested, val := range update {
				existing[nested] = val
			}
			data[key] = existing
		} else {
			data[key] = value
		}
	}
	cfg, err := config.FromData(raw.Path, data)
	if err != nil {
		return nil, &dataset.ValidationError{Message: err.Error()}
	}
	staged, err := New(cfg)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := atomicfile.Write(raw.Path, append(encoded, '\n'), 0600); err != nil {
		return nil, fmt.Errorf("save configuration: %w", err)
	}
	p.Config, p.State, p.WxKey, p.Echotrace = staged.Config, staged.State, staged.WxKey, staged.Echotrace
	return map[string]any{"status": "success"}, nil
}

func (p *Pipeline) ReloadConfig() (map[string]any, error) {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Config.ConfigPath == "" {
		return nil, &dataset.ValidationError{Message: "no configuration file selected"}
	}
	cfg, err := config.LoadPipeline(p.Config.ConfigPath)
	if err != nil {
		return nil, err
	}
	staged, err := New(cfg)
	if err != nil {
		return nil, err
	}
	p.Config, p.State, p.WxKey, p.Echotrace = staged.Config, staged.State, staged.WxKey, staged.Echotrace
	return map[string]any{"status": "success"}, nil
}

func (p *Pipeline) resolveWeChatDataDir(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("wechat directory is empty")
	}

	path := expandHome(trimmed)
	if !filepath.IsAbs(path) {
		base := strings.TrimSpace(p.configSnapshot().ConfigPath)
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

	fmt.Fprintln(os.Stderr, "🔍 Searching for WeChat data directory...")

	for _, basePath := range possiblePaths {
		fmt.Fprintf(os.Stderr, "  Checking: %s\n", basePath)

		wechatDir, err := p.resolveWeChatDataDir(basePath)
		if err != nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ Found: %s\n", wechatDir)
		return wechatDir, nil
	}

	return "", fmt.Errorf("WeChat data directory not found. Please specify with --wechat-dir.\n\nTried locations:\n  - %s",
		strings.Join(possiblePaths, "\n  - "))
}

func (p *Pipeline) Import(exportDir string) (*dataset.ImportResult, error) {
	p.nativeMu.Lock()
	defer p.nativeMu.Unlock()
	cfg := p.configSnapshot()
	if exportDir == "" {
		exportDir = cfg.ExportDir
	}
	return dataset.Import(exportDir, cfg.DatabasePath)
}
func (p *Pipeline) IndexStatus() (*dataset.IndexInfo, error) {
	return dataset.IndexStatus(p.configSnapshot().DatabasePath)
}

// ValidateExportArgs checks native request filters before a streaming response starts.
func ValidateExportArgs(args []string) error { _, _, _, err := exportOptions(args); return err }
