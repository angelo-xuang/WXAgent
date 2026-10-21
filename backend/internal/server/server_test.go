package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
	"github.com/billyoftea/wxagent/go_backend/internal/pipeline"
)

func TestNativeOperationsRejectUnsupportedPlatforms(t *testing.T) {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		called := false
		handler := nativeOperation(platform, func(w http.ResponseWriter, r *http.Request) { called = true })
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodPost, "/export", nil))
		if platform == "windows" && !called || platform != "windows" && (called || response.Code != http.StatusNotImplemented) {
			t.Fatalf("platform=%s called=%v code=%d", platform, called, response.Code)
		}
	}
}
func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultPipeline(dir)
	cfg.ConfigPath = filepath.Join(dir, "config.json")
	os.WriteFile(cfg.ConfigPath, []byte(`{"llm":{"api_key":"private"}}`), 0600)
	p, err := pipeline.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(p)
}
func request(s *Server, method, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	s.mux.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
	return response
}
func TestRemovedRoutesReturn404(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/summary/run", "/summary/run-stream", "/summary/history", "/summary/history/content", "/summary/export", "/llm/test", "/analysis/sessions", "/sessions/describe"} {
		if response := request(s, http.MethodPost, path, `{}`); response.Code != 404 {
			t.Fatalf("%s still accessible: %d", path, response.Code)
		}
	}
}
func TestQueryRoutesValidateInputAndNeverCreateDatabase(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/messages?start_date=bad", "/messages?start_date=2026-02-02&end_date=2026-02-01", "/messages?limit=-1", "/messages?offset=bad", "/stats?end_date=2026-02-30"} {
		if response := request(s, "GET", path, ""); response.Code != 400 {
			t.Fatalf("%s=%d %s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/health", "/status", "/index/status", "/sessions", "/messages", "/stats", "/config"} {
		if response := request(s, "GET", path, ""); response.Code != 200 {
			t.Fatalf("%s=%d %s", path, response.Code, response.Body.String())
		}
		if response := request(s, "POST", path, `{}`); response.Code != 405 {
			t.Fatalf("method not enforced %s=%d", path, response.Code)
		}
	}
	if _, err := os.Stat(s.pipeline.Config.DatabasePath); !os.IsNotExist(err) {
		t.Fatal("read routes created database")
	}
	status := request(s, "GET", "/status", "").Body.String()
	if strings.Contains(status, "private") || !strings.Contains(status, "query_messages") {
		t.Fatalf("status=%s", status)
	}
	public := request(s, "GET", "/config", "").Body.String()
	if strings.Contains(public, "llm") || strings.Contains(public, "private") {
		t.Fatalf("config exposes removed provider: %s", public)
	}
	if response := request(s, "PUT", "/config", `{"values":{"llm":{"model":"bad"}}}`); response.Code != 400 {
		t.Fatalf("legacy config update accepted: %d", response.Code)
	}
}
func TestHTTPImportAndIndexedMessages(t *testing.T) {
	s := testServer(t)
	source := s.pipeline.Config.ExportDir
	os.MkdirAll(source, 0700)
	os.WriteFile(filepath.Join(source, "chat.json"), []byte(`{"session_id":"test","session_name":"Demo","messages":[{"create_time":1767196800,"str_content":"你好","is_send":1}]}`), 0600)
	if response := request(s, "POST", "/index/import", `{}`); response.Code != 200 {
		t.Fatalf("import=%d %s", response.Code, response.Body.String())
	}
	response := request(s, "GET", "/messages?session_id=test&limit=1", "")
	var data struct {
		Total    int
		Messages []map[string]any
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Total != 1 || len(data.Messages) != 1 {
		t.Fatalf("messages=%s err=%v", response.Body.String(), err)
	}
	os.WriteFile(filepath.Join(source, "bad.json"), []byte(`broken`), 0600)
	if response := request(s, "POST", "/index/import", `{}`); response.Code != 500 || !strings.Contains(response.Body.String(), "bad.json") {
		t.Fatalf("bad import=%d %s", response.Code, response.Body.String())
	}
	// Query uses the durable index, so corrupt/missing source exports do not break browsing.
	if response := request(s, "GET", "/messages", ""); response.Code != 200 {
		t.Fatalf("index query read source files: %s", response.Body.String())
	}
}

func TestStreamingExportRejectsInvalidDatesBeforeHeaders(t *testing.T) {
	s := testServer(t)
	response := httptest.NewRecorder()
	s.handleFullRefreshStream(response, httptest.NewRequest("POST", "/full-refresh/stream", strings.NewReader(`{"export_args":["--start-date","invalid"]}`)))
	if response.Code != 400 || strings.Contains(response.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("invalid date started stream: %d %s", response.Code, response.Body.String())
	}
}
