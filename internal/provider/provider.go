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

// DebugFunc is an optional debug logger that providers can use.
type DebugFunc func(format string, args ...any)

// doWithRetry sends an HTTP request with one retry on 429 or 5xx.
func doWithRetry(req *http.Request, payload []byte, dbg DebugFunc) (*http.Response, error) {
	if dbg != nil {
		dbg("HTTP %s %s (%d bytes)", req.Method, req.URL.String(), len(payload))
		dbg("Request Headers: %v", req.Header)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if dbg != nil {
			dbg("HTTP ERROR: %v", err)
		}
		return nil, err
	}
	if dbg != nil {
		dbg("HTTP RESPONSE: %d %s", resp.StatusCode, resp.Status)
		dbg("Response Content-Encoding: %s", resp.Header.Get("Content-Encoding"))
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		resp.Body.Close()
		if dbg != nil {
			dbg("HTTP RETRY: waiting 2s then retrying...")
		}
		time.Sleep(2 * time.Second)
		req.Body = io.NopCloser(bytes.NewReader(payload))
		resp, err = http.DefaultClient.Do(req)
		if dbg != nil && err == nil {
			dbg("HTTP RETRY RESPONSE: %d %s", resp.StatusCode, resp.Status)
		}
		return resp, err
	}
	return resp, nil
}
