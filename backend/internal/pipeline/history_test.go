package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
)

func TestHistoryReadsOnlyAllowedFiles(t *testing.T) {
	base := t.TempDir()
	history := filepath.Join(base, "history")
	if err := os.Mkdir(history, 0700); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{Config: &config.PipelineConfig{SummaryHistoryDir: history, ExportDir: filepath.Join(base, "exports")}}
	inside := filepath.Join(history, "summary.md")
	outside := filepath.Join(base, "private.md")
	for _, path := range []string{inside, outside, filepath.Join(history, "secret.json")} {
		if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if text, err := p.ReadSummaryHistory(inside); err != nil || text != "sample" {
		t.Fatalf("valid history read = %q, %v", text, err)
	}
	for _, path := range []string{outside, filepath.Join(history, "..", "private.md"), filepath.Join(history, "secret.json"), filepath.Join(base, "history-other", "summary.md")} {
		if _, err := p.ReadSummaryHistory(path); err == nil {
			t.Errorf("unexpected access to %q", path)
		}
	}
	link := filepath.Join(history, "linked.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink check unavailable on this host: %v", err)
	} else if _, err := p.ReadSummaryHistory(link); err == nil {
		t.Fatal("symlink escaped history directory")
	}
}

func TestHistoryLimitReturnsNewestAcrossDirectories(t *testing.T) {
	base := t.TempDir()
	custom := filepath.Join(base, "custom")
	export := filepath.Join(base, "export")
	latest := filepath.Join(export, "analysis", "z-newest.md")
	for i, path := range []string{filepath.Join(custom, "a-oldest.md"), latest} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sample"), 0600); err != nil {
			t.Fatal(err)
		}
		modified := time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	p := &Pipeline{Config: &config.PipelineConfig{SummaryHistoryDir: custom, ExportDir: export}}
	history, err := p.ListSummaryHistory(1)
	if err != nil || len(history) != 1 || history[0]["path"] != latest {
		t.Fatalf("history = %v, error = %v", history, err)
	}
}
