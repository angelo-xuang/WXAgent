package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingAndNullPathsUseDefaults(t *testing.T) {
	for _, input := range []string{`{}`, `{"export_dir":null,"summary_history_dir":null,"questions_file":null,"echotrace_command":{"args":["--quiet"]}}`} {
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadPipeline(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.WeChatDataPath != "" || cfg.QuestionsFile != "" {
				t.Fatalf("missing optional paths must stay empty: %+v", cfg)
			}
			wantHistory := filepath.Join(filepath.Dir(cfg.SummaryOutput), "summary_history")
			if cfg.SummaryHistoryDir != wantHistory {
				t.Fatalf("history = %q, want %q", cfg.SummaryHistoryDir, wantHistory)
			}
			if strings.Contains(cfg.ExportDir, "<nil>") || strings.Contains(cfg.EchotraceCommand.Path, "<nil>") || cfg.EchotraceCommand.Cwd != dir {
				t.Fatalf("null overwrote configured defaults: %+v", cfg)
			}
		})
	}
}

func TestLoadRejectsNullDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`null`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a configuration object error")
	}
}
