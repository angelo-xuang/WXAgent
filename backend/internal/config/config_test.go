package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeDefaultsIgnoreLegacyFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"llm":{"api_key":"secret"},"summary_output":"old.md","export_dir":null}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPipeline(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePath != filepath.Join(dir, "data", "wxagent.db") || cfg.ExportDir != filepath.Join(dir, "exports") || cfg.WeChatDataPath != "" || cfg.WxKeySharedPrefs != "" || cfg.WxKeyCommand.Path != "" {
		t.Fatalf("unsafe defaults: %+v", cfg)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("load created files")
	}
}
func TestLoadRejectsInvalidConfig(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"database_path":123}`, `{"wx_key_command":"bad"}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPipeline(path); err == nil {
			t.Fatalf("invalid config accepted: %s", raw)
		}
	}
}
func TestConfiguredPathsResolveAgainstConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"database_path":"db/chat.db","wx_key_command":{"path":"helper.exe","args":["--quiet"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPipeline(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePath != filepath.Join(dir, "db", "chat.db") || cfg.WxKeyCommand.Path != filepath.Join(dir, "helper.exe") {
		t.Fatalf("paths=%+v", cfg)
	}
}
