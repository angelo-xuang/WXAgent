package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/pipeline"
)

func TestNativeOperationsRejectUnsupportedPlatforms(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			called := false
			handler := nativeOperation(platform, func(w http.ResponseWriter, r *http.Request) { called = true })
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodPost, "/full-refresh", strings.NewReader(`{}`)))
			if platform == "windows" && !called || platform != "windows" && (called || response.Code != http.StatusNotImplemented) {
				t.Fatalf("platform %s: called=%v, code=%d", platform, called, response.Code)
			}
		})
	}
}

func TestSummaryRequestCancellationStopsProviderAndDoesNotWriteReport(t *testing.T) {
	providerStarted := make(chan struct{})
	providerCancelled := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": waiting\n\n")
		w.(http.Flusher).Flush()
		close(providerStarted)
		select {
		case <-r.Context().Done():
			close(providerCancelled)
		case <-time.After(5 * time.Second):
			// Bound cleanup even if cancellation propagation regresses.
		}
	}))
	defer provider.Close()
	dir := t.TempDir()
	fixture := `{"session_id":"test","session_name":"Test","messages":[{"formatted_time":"2026-01-01 12:00:00","str_content":"Sample"}]}`
	if err := os.WriteFile(filepath.Join(dir, "chat.json"), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(&pipeline.Pipeline{Config: &config.PipelineConfig{ExportDir: dir, LLM: config.LLMConfig{BaseURL: provider.URL, Timeout: 10 * time.Second}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/summary/run-stream", strings.NewReader(`{}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		s.mux.ServeHTTP(response, request)
		close(finished)
	}()
	select {
	case <-providerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("analysis did not reach the mock provider")
	}
	cancel()
	select {
	case <-providerCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP cancellation did not cancel the model request")
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled summary handler did not return")
	}
	if strings.Contains(response.Body.String(), "event: result") || strings.Contains(response.Body.String(), "event: done") {
		t.Fatalf("cancelled analysis emitted completion: %s", response.Body.String())
	}
	reports, err := filepath.Glob(filepath.Join(dir, "analysis", "*.md"))
	if err != nil || len(reports) != 0 {
		t.Fatalf("cancelled analysis wrote reports: %v, %v", reports, err)
	}
}

func TestFilteredExportDoesNotReportFalseSuccess(t *testing.T) {
	s := New(&pipeline.Pipeline{})
	response := httptest.NewRecorder()
	s.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/summary/export", strings.NewReader(`{"selected":["example"]}`)))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
