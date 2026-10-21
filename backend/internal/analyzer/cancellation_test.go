package analyzer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/llm"
)

func TestCancelImmediatelyBeforeSavingDoesNotWriteReport(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"mock summary\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer provider.Close()
	dir := t.TempDir()
	fixture := `{"session_id":"test","session_name":"Test","messages":[{"formatted_time":"2026-01-01 12:00:00","str_content":"Sample"}]}`
	if err := os.WriteFile(filepath.Join(dir, "chat.json"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "report.md")
	a := New(dir, llm.NewClient(config.LLMConfig{BaseURL: provider.URL, Timeout: time.Second}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := false
	result, err := a.AnalyzeStream(ctx, AnalyzeOptions{OutputFile: output}, func(event StreamEvent) {
		if event.Step == 6 && event.Type == "log" {
			cancel()
		}
		completed = completed || event.Type == "done"
	})
	if !errors.Is(err, context.Canceled) || result != nil || completed {
		t.Fatalf("cancelled analysis reported success: result=%v, error=%v, done=%v", result, err, completed)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected map and reduce to finish before cancellation, got %d calls", calls.Load())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("cancelled analysis wrote a report: %v", err)
	}
}
