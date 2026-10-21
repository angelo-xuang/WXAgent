package pipeline

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/state"
)

func TestFailedSummaryRemainsRetryableAndPreservesExistingFiles(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "simulated provider failure", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"完整总结\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer provider.Close()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	store := state.New(statePath)
	// Export progress must not make the summary pipeline skip these messages.
	store.UpdateSession("session-1", 1700000000, 1)
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	chat := `{"session":{"wxid":"session-1","displayName":"测试会话"},"messages":[{"createTime":1700000000,"content":"测试消息"}]}`
	if err := os.WriteFile(filepath.Join(dir, "chat.json"), []byte(chat), 0600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(dir, "summary_result_go.md")
	if err := os.WriteFile(outputPath, []byte("previous summary"), 0600); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{State: store, Config: &config.PipelineConfig{ExportDir: dir, LLM: config.LLMConfig{BaseURL: provider.URL, Timeout: time.Second}}}
	if _, err := p.RunSummary("", "", true, nil); err == nil {
		t.Fatal("provider failure must not report summary success")
	}
	if store.LastSummaryTimestamp("session-1") != 0 {
		t.Fatal("failed summary advanced in-memory progress")
	}
	afterState, _ := os.ReadFile(statePath)
	output, _ := os.ReadFile(outputPath)
	if string(afterState) != string(originalState) || string(output) != "previous summary" {
		t.Fatal("failed summary changed saved state or prior result")
	}
	fail.Store(false)
	if result, err := p.RunSummary("", "", true, nil); err != nil || result["status"] != "success" {
		t.Fatalf("retry = %v, %v", result, err)
	}
	reloaded := state.New(statePath)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if reloaded.LastSummaryTimestamp("session-1") != 1700000000 || reloaded.LastTimestamp("session-1") != 1700000000 {
		t.Fatal("summary and export progress were not saved independently")
	}
	if result, err := p.RunSummary("", "", true, nil); err != nil || result["status"] != "no_data" {
		t.Fatalf("completed messages were not skipped: %v, %v", result, err)
	}
}

func TestConfigSaveFailureDoesNotChangeLiveSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte(`{"llm":`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.PipelineConfig{ConfigPath: path, ExportDir: "original", LLM: config.LLMConfig{Model: "original-model"}}
	p := &Pipeline{Config: cfg}
	_, err := p.UpdateConfig(map[string]any{"export_dir": "changed", "llm": map[string]any{"model": "changed-model"}})
	if err == nil || p.Config != cfg || p.Config.ExportDir != "original" || p.Config.LLM.Model != "original-model" {
		t.Fatalf("failed update modified live settings: %+v, %v", p.Config, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("failed update overwrote the original configuration")
	}
}

func TestUpdatedLLMSettingSurvivesReloadWithLegacyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"llm_model":"legacy-model","keep":"untouched"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadPipeline(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{Config: cfg}
	if _, err := p.UpdateConfig(map[string]any{"llm": map[string]any{"model": "updated-model"}}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadPipeline(path)
	if err != nil || reloaded.LLM.Model != "updated-model" {
		t.Fatalf("updated settings did not survive reload: %+v, %v", reloaded, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"untouched"`) {
		t.Fatal("unrelated setting changed")
	}
}

func TestCorruptStateIsNotSilentlyDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	original := []byte(`{"sessions":{"existing":{"last_timestamp":99}},"meta":`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(&config.PipelineConfig{StateFile: path}); err == nil {
		t.Fatal("corrupt state must require repair instead of being overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("original state file was changed")
	}
}

func TestReloadConfigSwitchesDependenciesOnlyAfterValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	stateA := filepath.Join(dir, "state-a.json")
	stateB := filepath.Join(dir, "state-b.json")
	originalA := `{"sessions":{"loaded-a":{"last_timestamp":10}}}`
	for path, data := range map[string]string{
		configPath: `{"state_file":"state-a.json"}`,
		stateA:     originalA,
		stateB:     `{"sessions":{"loaded-b":{"last_timestamp":20}}}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadPipeline(configPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	previousState, previousKey, previousRunner := p.State, p.WxKey, p.Echotrace
	if err := os.WriteFile(configPath, []byte(`{"state_file":"state-b.json","wx_key_command":{"path":"new-key"},"echotrace_command":{"path":"new-exporter"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if p.Config.StateFile != stateB || p.State == previousState || p.WxKey == previousKey || p.Echotrace == previousRunner || p.State.LastTimestamp("loaded-b") != 20 {
		t.Fatal("reloaded paths were not applied to all dependent components")
	}
	p.State.UpdateSession("new-progress", 30, 1)
	if err := p.State.Save(); err != nil {
		t.Fatal(err)
	}
	afterA, err := os.ReadFile(stateA)
	if err != nil || string(afterA) != originalA {
		t.Fatalf("reloaded pipeline still wrote to the old state: %q, %v", afterA, err)
	}
	reloadedB := state.New(stateB)
	if err := reloadedB.Load(); err != nil || reloadedB.LastTimestamp("new-progress") != 30 {
		t.Fatalf("new progress was not saved to state B: %v", err)
	}

	previousConfig := p.Config
	previousState, previousKey, previousRunner = p.State, p.WxKey, p.Echotrace
	if err := os.WriteFile(stateA, []byte(`{"invalid":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"state_file":"state-a.json"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReloadConfig(); err == nil {
		t.Fatal("expected a failure while loading corrupt replacement state")
	}
	if p.Config != previousConfig || p.State != previousState || p.WxKey != previousKey || p.Echotrace != previousRunner {
		t.Fatal("failed reload partially replaced live components")
	}
}
