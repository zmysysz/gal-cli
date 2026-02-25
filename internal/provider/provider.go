package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type StreamDelta struct {
	Content   string     // text chunk
	ToolCalls []ToolCall // tool call chunks
	Done      bool
}

type Provider interface {
	ChatStream(ctx context.Context, model string, messages []Message, tools []ToolDef, onDelta func(StreamDelta)) error
}

// doWithRetry sends an HTTP request with one retry on 429 or 5xx.
func doWithRetry(req *http.Request, payload []byte) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		resp.Body.Close()
		time.Sleep(2 * time.Second)
		req.Body = io.NopCloser(bytes.NewReader(payload))
		return http.DefaultClient.Do(req)
	}
	return resp, nil
}
