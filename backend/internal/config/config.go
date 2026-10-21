package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the parsed wx_agent configuration file and helper accessors.
type Config struct {
	Path string
	dir  string
	data map[string]any
}

// Load reads the configuration JSON located at path. When path is empty it
// defaults to config.json in the current directory.
func Load(path string) (*Config, error) {
	if path == "" {
		path = "config.json"
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", resolved, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", resolved, err)
	}
	if data == nil {
		return nil, fmt.Errorf("parse config %s: expected a JSON object", resolved)
	}
	return &Config{
		Path: resolved,
		dir:  filepath.Dir(resolved),
		data: data,
	}, nil
}

func resolvePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty path")
	}
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path), nil
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

// Data returns the raw config map (read-only). Callers should copy before modifying.
func (c *Config) Data() map[string]any {
	clone := make(map[string]any, len(c.data))
	for k, v := range c.data {
		clone[k] = v
	}
	return clone
}

// RedactedCopy returns a copy of the config map with sensitive fields masked.
func (c *Config) RedactedCopy() map[string]any {
	clone := deepCopy(c.data)
	redactKey(clone, "llm_api_key")
	if llm, ok := clone["llm"].(map[string]any); ok {
		redactKey(llm, "api_key")
	}
	return clone
}

func deepCopy(src map[string]any) map[string]any {
	raw, err := json.Marshal(src)
	if err != nil {
		return map[string]any{}
	}
	var dst map[string]any
	if err := json.Unmarshal(raw, &dst); err != nil {
		return map[string]any{}
	}
	return dst
}

func redactKey(m map[string]any, key string) {
	if _, ok := m[key]; ok {
		m[key] = "******"
	}
}

// ResolvePath converts a config path (which may be relative or contain ~) into
// an absolute path anchored at the config file directory.
func (c *Config) ResolvePath(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return ""
	}
	text = expandHome(text)
	if filepath.IsAbs(text) {
		return filepath.Clean(text)
	}
	return filepath.Clean(filepath.Join(c.dir, text))
}

func (c *Config) ExportDir() string {
	return c.ResolvePath(c.data["export_dir"])
}

func (c *Config) SummaryOutput() string {
	return c.ResolvePath(c.data["summary_output"])
}

func (c *Config) SummaryHistoryDir() string {
	return c.ResolvePath(c.data["summary_history_dir"])
}

func (c *Config) WxKeyPrefs() string {
	return c.ResolvePath(c.data["wx_key_shared_prefs"])
}

func (c *Config) WechatDataPath() string {
	return c.ResolvePath(c.data["wechat_data_path"])
}

func (c *Config) EchotraceCommandPath() string {
	if spec, ok := c.data["echotrace_command"].(map[string]any); ok {
		return c.ResolvePath(spec["path"])
	}
	return ""
}

// CommandSpec describes an external executable call.
type CommandSpec struct {
	Path string            `json:"path"`
	Args []string          `json:"args"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// Exec returns the resolved executable path relative to the config directory.
func (c CommandSpec) Exec(baseDir string) string {
	expanded := expandHome(c.Path)
	if expanded == "" {
		return ""
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(filepath.Join(baseDir, expanded))
}

// PipelineConfig is the strongly typed configuration consumed by the Go backend.
type PipelineConfig struct {
	ConfigPath        string
	WeChatDataPath    string
	ExportDir         string
	SummaryOutput     string
	SummaryHistoryDir string
	StateFile         string
	WxKeySharedPrefs  string
	WxKeyCommand      CommandSpec
	EchotraceCommand  CommandSpec
	CustomQuestions   []string
	QuestionsFile     string
	LLM               LLMConfig
	MaxTokens         int
	OverlapTokens     int
	APIMaxTokens      int
	MapConcurrency    int
	QAContextTokens   int
	QAOverlapTokens   int
	QAMaxBatches      int
}

// LLMConfig defines how to reach the model provider.
type LLMConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	Timeout     time.Duration
}

// LoadPipeline resolves config.json into PipelineConfig with sane defaults.
func LoadPipeline(path string) (*PipelineConfig, error) {
	raw, err := Load(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(raw.Path)

	getPath := func(key string, fallback string) string {
		if val, ok := raw.data[key]; ok && val != nil {
			if text := strings.TrimSpace(fmt.Sprint(val)); text != "" {
				if resolved := raw.ResolvePath(val); resolved != "" {
					return resolved
				}
			}
		}
		if fallback != "" {
			return raw.ResolvePath(fallback)
		}
		return ""
	}

	exportDir := getPath("export_dir", "../output_test")
	summaryOutput := getPath("summary_output", "../summary_result.md")
	summaryHistory := raw.ResolvePath(raw.data["summary_history_dir"])
	if summaryHistory == "" {
		base := summaryOutput
		if filepath.Ext(base) != "" {
			base = filepath.Dir(base)
		}
		summaryHistory = filepath.Join(base, "summary_history")
	}
	stateFile := getPath("state_file", "../.wx_agent_state.json")
	wechatDataPath := getPath("wechat_data_path", "")
	wxKeyPrefs := getPath("wx_key_shared_prefs", "~/AppData/Roaming/com.example/wx_key/shared_preferences.json")

	wxKeyCmd := parseCommand(raw, raw.data["wx_key_command"], CommandSpec{Path: "wx_agent/bin/wx_key/wx_key.exe", Cwd: dir})
	echotraceCmd := parseCommand(raw, raw.data["echotrace_command"], CommandSpec{Path: "wx_agent/bin/echotrace/echotrace.exe", Cwd: dir})

	cfg := &PipelineConfig{
		ConfigPath:        raw.Path,
		WeChatDataPath:    wechatDataPath,
		ExportDir:         exportDir,
		SummaryOutput:     summaryOutput,
		SummaryHistoryDir: summaryHistory,
		StateFile:         stateFile,
		WxKeySharedPrefs:  wxKeyPrefs,
		WxKeyCommand:      wxKeyCmd,
		EchotraceCommand:  echotraceCmd,
		CustomQuestions:   stringSlice(raw.data["custom_questions"]),
		QuestionsFile:     raw.ResolvePath(raw.data["questions_file"]),
		LLM:               parseLLM(raw),
		MaxTokens:         intOrDefault(raw.data["max_tokens"], 60000),
		OverlapTokens:     intOrDefault(raw.data["overlap_tokens"], 800),
		APIMaxTokens:      intOrDefault(raw.data["api_max_tokens"], 98304),
		MapConcurrency:    intOrDefault(raw.data["map_concurrency"], 3),
		QAContextTokens:   intOrDefault(raw.data["qa_context_tokens"], 20000),
		QAOverlapTokens:   intOrDefault(raw.data["qa_overlap_tokens"], 400),
		QAMaxBatches:      intOrDefault(raw.data["qa_max_batches"], 4),
	}

	if cfg.WxKeyCommand.Env == nil {
		cfg.WxKeyCommand.Env = map[string]string{}
	}
	if cfg.EchotraceCommand.Env == nil {
		cfg.EchotraceCommand.Env = map[string]string{}
	}

	return cfg, nil
}

func parseCommand(base *Config, value any, fallback CommandSpec) CommandSpec {
	result := fallback
	if data, ok := value.(map[string]any); ok {
		if path, ok := data["path"].(string); ok && strings.TrimSpace(path) != "" {
			result.Path = base.ResolvePath(path)
		}
		if args := stringSlice(data["args"]); args != nil {
			result.Args = args
		}
		if cwd, ok := data["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			result.Cwd = base.ResolvePath(cwd)
		}
		if envMap, ok := data["env"].(map[string]any); ok {
			env := make(map[string]string, len(envMap))
			for key, val := range envMap {
				env[key] = fmt.Sprint(val)
			}
			result.Env = env
		}
	}
	if result.Env == nil {
		result.Env = map[string]string{}
	}
	return result
}

func parseLLM(cfg *Config) LLMConfig {
	nested, _ := cfg.data["llm"].(map[string]any)
	if nested == nil {
		nested = map[string]any{}
	}
	baseURL := firstNonEmpty(
		fmt.Sprint(cfg.data["llm_base_url"]),
		fmt.Sprint(nested["base_url"]),
		os.Getenv("LLM_BASE_URL"),
		os.Getenv("WX_AGENT_API_BASE"),
		os.Getenv("ARK_API_BASE"),
	)
	apiKey := firstNonEmpty(
		fmt.Sprint(cfg.data["llm_api_key"]),
		fmt.Sprint(nested["api_key"]),
		os.Getenv("LLM_API_KEY"),
		os.Getenv("WX_AGENT_API_KEY"),
		os.Getenv("ARK_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	)
	model := firstNonEmpty(
		fmt.Sprint(cfg.data["llm_model"]),
		fmt.Sprint(nested["model"]),
		os.Getenv("LLM_MODEL"),
		"deepseek-v3-1-terminus",
	)
	temp := floatOrDefault(cfg.data["llm_temperature"], floatOrDefault(nested["temperature"], 0.7))
	// 默认超时设为 300 秒（5分钟），大文本 AI 总结需要较长时间
	timeoutSeconds := floatOrDefault(cfg.data["llm_timeout"], floatOrDefault(nested["timeout"], 300))

	return LLMConfig{
		BaseURL:     strings.TrimSpace(baseURL),
		APIKey:      strings.TrimSpace(apiKey),
		Model:       strings.TrimSpace(model),
		Temperature: temp,
		Timeout:     time.Duration(timeoutSeconds * float64(time.Second)),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" && trimmed != "<nil>" {
			return trimmed
		}
	}
	return ""
}

func intOrDefault(value any, fallback int) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return parsed
		}
	}
	return fallback
}

func floatOrDefault(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSlice(value any) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text == "" {
			return nil
		}
		return []string{text}
	}
}
