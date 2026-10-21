package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/dataset"
)

func TestReadOnlyPipelineDoesNotLoadSecretsOrCorruptLegacyState(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultPipeline(dir)
	cfg.StateFile = filepath.Join(dir, "state.json")
	os.WriteFile(cfg.StateFile, []byte(`broken state`), 0600)
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.QueryMessages(dataset.QueryOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.IndexStatus(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("read operations created files")
	}
}
func TestConfigUpdatePreservesUnknownFieldsAndRejectsRemovedSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{"llm":{"api_key":"old-secret"},"llm_model":"legacy","custom":{"keep":true},"wx_key_command":{"path":"helper","env":{"TOKEN":"hidden"}}}`
	os.WriteFile(path, []byte(original), 0600)
	cfg, err := config.LoadPipeline(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{{"llm": map[string]any{"model": "no"}}, {"summary_output": "no"}} {
		if _, err := p.UpdateConfig(values); err == nil {
			t.Fatal("removed setting accepted")
		}
	}
	if _, err := p.UpdateConfig(map[string]any{"database_path": "next/index.db", "wx_key_command": map[string]any{"path": "updated"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	if saved["llm"].(map[string]any)["api_key"] != "old-secret" || saved["custom"].(map[string]any)["keep"] != true || saved["wx_key_command"].(map[string]any)["env"].(map[string]any)["TOKEN"] != "hidden" {
		t.Fatal("unknown fields lost")
	}
	public, _ := json.Marshal(p.GetConfig())
	if strings.Contains(string(public), "old-secret") || strings.Contains(string(public), "TOKEN") || strings.Contains(string(public), "llm") {
		t.Fatalf("secret exposed: %s", public)
	}
	if p.Config.DatabasePath != filepath.Join(dir, "next", "index.db") {
		t.Fatal("live setting not applied")
	}
}
func TestFailedConfigWriteAndReloadPreserveLiveSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	cfg, _ := config.LoadPipeline(path)
	p, _ := New(cfg)
	os.WriteFile(path, []byte(`broken`), 0600)
	if _, err := p.UpdateConfig(map[string]any{"export_dir": "changed"}); err == nil {
		t.Fatal("invalid original accepted")
	}
	if _, err := p.ReloadConfig(); err == nil {
		t.Fatal("invalid reload accepted")
	}
	if p.Config != cfg {
		t.Fatal("failed update replaced live config")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "broken" {
		t.Fatal("original file overwritten")
	}
}
func TestConcurrentReadAndConfigurationReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	cfg, _ := config.LoadPipeline(path)
	p, _ := New(cfg)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p.GetStatus()
				p.GetConfig()
				if _, err := p.QueryMessages(dataset.QueryOptions{}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		if _, err := p.ReloadConfig(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
