package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
)

func TestReadStream(t *testing.T) {
	for _, tc := range []struct {
		name, input, want, errText string
	}{
		{"normal", "data:{\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\r\n\r\ndata: [DONE]\n\n", "你好", ""},
		{"multiline", "data: {\"choices\":\n" + "data: [{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n", "ok", ""},
		{"provider error", "data: {\"error\":{\"message\":\"quota exhausted\"}}\n\n", "", "quota exhausted"},
		{"invalid JSON", "data: {broken}\n\n", "", "decode stream"},
		{"empty", "data: [DONE]\n\n", "", "empty response"},
		{"disconnected", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "partial", "before a completion"},
		{"token limit", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\n", "partial", "length"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chunks strings.Builder
			got, err := readStream(strings.NewReader(tc.input), func(chunk string) { chunks.WriteString(chunk) })
			if got != tc.want || chunks.String() != tc.want {
				t.Fatalf("result = %q, callback = %q, want %q", got, chunks.String(), tc.want)
			}
			if tc.errText == "" && err != nil || tc.errText != "" && (err == nil || !strings.Contains(err.Error(), tc.errText)) {
				t.Fatalf("error = %v, expected %q", err, tc.errText)
			}
		})
	}
}

func TestStreamSupportsLargeEvents(t *testing.T) {
	want := strings.Repeat("内容", 40000)
	payload, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": want}, "finish_reason": "stop"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := readStream(strings.NewReader("data: "+string(payload)+"\n\n"), nil)
	if err != nil || got != want {
		t.Fatalf("large stream failed: length = %d, error = %v", len(got), err)
	}
}

func TestConfiguredTimeoutCancelsStalledStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": waiting\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{BaseURL: server.URL, Timeout: 50 * time.Millisecond})
	_, err := client.ChatCompletionWithCallback(context.Background(), nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected configured timeout, got %v", err)
	}
}
