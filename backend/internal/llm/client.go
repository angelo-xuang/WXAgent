package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/billyoftea/wxagent/go_backend/internal/config"
)

type Client struct {
	cfg    config.LLMConfig
	client *http.Client
}

func NewClient(cfg config.LLMConfig) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300 * time.Second
	}
	return &Client{cfg: cfg, client: &http.Client{}}
}

// sanitizeAPIKey removes characters that cannot occur in an HTTP header.
func sanitizeAPIKey(key string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(key))
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Error *APIError `json:"error,omitempty"`
}

type StreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *APIError `json:"error,omitempty"`
}

type StreamCallback func(chunk string)

// ChatCompletionStream keeps console output for command-line callers while
// sharing request, timeout and SSE parsing with the desktop streaming client.
func (c *Client) ChatCompletionStream(ctx context.Context, messages []ChatMessage) (string, error) {
	result, err := c.ChatCompletionWithCallback(ctx, messages, func(chunk string) {
		fmt.Print(chunk)
	})
	fmt.Println()
	return result, err
}

func (c *Client) ChatCompletionWithCallback(ctx context.Context, messages []ChatMessage, callback StreamCallback) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	jsonBody, err := json.Marshal(ChatRequest{
		Model: c.cfg.Model, Messages: messages, Temperature: c.cfg.Temperature, Stream: true,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	baseURL := c.cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey := sanitizeAPIKey(c.cfg.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api error (status %d): %s", resp.StatusCode, body)
	}
	return readStream(resp.Body, callback)
}

// readStream processes complete SSE events, including multi-line data fields.
// A dropped connection or provider error must not become a successful summary.
func readStream(body io.Reader, callback StreamCallback) (string, error) {
	reader := bufio.NewReader(body)
	var content strings.Builder
	var data []string
	consume := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload == "[DONE]" {
			if content.Len() == 0 {
				return false, errors.New("provider returned an empty response")
			}
			return true, nil
		}
		var chunk StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return false, fmt.Errorf("decode stream event: %w", err)
		}
		if chunk.Error != nil {
			return false, fmt.Errorf("api stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			return false, nil // Usage and heartbeat events carry no text.
		}
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			content.WriteString(choice.Delta.Content)
			if callback != nil {
				callback(choice.Delta.Content)
			}
		}
		if choice.FinishReason != "" {
			if choice.FinishReason != "stop" {
				return false, fmt.Errorf("provider stopped before completing the response: %s", choice.FinishReason)
			}
			if content.Len() == 0 {
				return false, errors.New("provider returned an empty response")
			}
			return true, nil
		}
		return false, nil
	}

	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if line == "" || readErr != nil {
			done, err := consume()
			if err != nil || done {
				return content.String(), err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return content.String(), errors.New("stream ended before a completion event")
			}
			return content.String(), fmt.Errorf("read stream: %w", readErr)
		}
	}
}

func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	return c.ChatCompletionStream(ctx, messages)
}
