package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", resolved, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("parse config %s: unexpected trailing data", resolved)
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

// PipelineConfig contains only local data and native component settings.
type PipelineConfig struct {
	ConfigPath       string
	DatabasePath     string
	WeChatDataPath   string
	ExportDir        string
	StateFile        string
	WxKeySharedPrefs string
	WxKeyCommand     CommandSpec
	EchotraceCommand CommandSpec
}

// DefaultPipeline does not discover accounts, read secrets, or create files.
func DefaultPipeline(baseDir string) *PipelineConfig {
	if absolute, err := filepath.Abs(baseDir); err == nil {
		baseDir = absolute
	}
	return &PipelineConfig{DatabasePath: filepath.Join(baseDir, "data", "wxagent.db"), ExportDir: filepath.Join(baseDir, "exports"), StateFile: filepath.Join(baseDir, ".wxagent", "state.json")}
}

func LoadPipeline(path string) (*PipelineConfig, error) {
	raw, err := Load(path)
	if err != nil {
		return nil, err
	}
	return FromData(raw.Path, raw.data)
}

// FromData validates a staged configuration before it can replace a file.
func FromData(path string, data map[string]any) (*PipelineConfig, error) {
	base := &Config{Path: path, dir: filepath.Dir(path), data: data}
	cfg := DefaultPipeline(base.dir)
	cfg.ConfigPath = path
	for key, target := range map[string]*string{"database_path": &cfg.DatabasePath, "export_dir": &cfg.ExportDir, "state_file": &cfg.StateFile, "wechat_data_path": &cfg.WeChatDataPath, "wx_key_shared_prefs": &cfg.WxKeySharedPrefs} {
		value, ok := data[key]
		if !ok || value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", key)
		}
		if strings.TrimSpace(text) == "" {
			if key == "database_path" || key == "export_dir" || key == "state_file" {
				continue
			}
			*target = ""
		} else {
			*target = base.ResolvePath(text)
		}
	}
	for _, key := range []string{"wx_key_command", "echotrace_command"} {
		if value, ok := data[key]; ok && value != nil {
			if _, ok := value.(map[string]any); !ok {
				return nil, fmt.Errorf("%s must be an object", key)
			}
		}
	}
	cfg.WxKeyCommand = parseCommand(base, data["wx_key_command"], CommandSpec{Cwd: base.dir})
	cfg.EchotraceCommand = parseCommand(base, data["echotrace_command"], CommandSpec{Cwd: base.dir})
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
