package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAI struct {
	APIKey  string
	BaseURL string
	Timeout time.Duration
	Retries int
	Debug   DebugFunc
}

// idleTimeoutReader wraps a reader and returns an error if no data is read within the timeout.
// It uses a dedicated buffer to avoid data races when the underlying Read outlives the timeout.
type idleTimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	buf     []byte // internal buffer for safe async reads
	n       int    // valid bytes in buf
}

func (itr *idleTimeoutReader) Read(p []byte) (int, error) {
	// If we have buffered data from a previous async read, return it first
	if itr.n > 0 {
		n := copy(p, itr.buf[:itr.n])
		itr.n = 0
		return n, nil
	}

	type result struct {
		n   int
		err error
	}
	if itr.buf == nil || len(itr.buf) < len(p) {
		itr.buf = make([]byte, len(p))
	}
	ch := make(chan result, 1)
	go func() {
		n, err := itr.r.Read(itr.buf[:len(p)])
		ch <- result{n, err}
	}()
	select {
	case res := <-ch:
		// Copy from internal buffer to caller's buffer
		copy(p[:res.n], itr.buf[:res.n])
		return res.n, res.err
	case <-time.After(itr.timeout):
		// Close the underlying reader to unblock the goroutine
		itr.r.Close()
		return 0, fmt.Errorf("stream idle timeout (%s without data)", itr.timeout)
	}
}

func (o *OpenAI) ChatStream(ctx context.Context, model string, messages []Message, tools []ToolDef, onDelta func(StreamDelta)) error {
	// Convert messages to map format, ensuring content is omitted when empty and tool_calls present
	msgs := make([]map[string]any, len(messages))
	for i, m := range messages {
		msg := map[string]any{"role": m.Role, "content": m.Content}
		if m.Content == "" && (m.Role == "assistant" || m.Role == "tool") {
			msg["content"] = nil
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		if m.ToolCallID != "" {
			msg["tool_call_id"] = m.ToolCallID
		}
		msgs[i] = msg
	}

	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
	}
	if len(tools) > 0 {
		funcs := make([]map[string]any, len(tools))
		for i, t := range tools {
			funcs[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		body["tools"] = funcs
	}

	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := doWithRetry(req, payload, o.Debug, o.Timeout, o.Retries)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		if o.Debug != nil {
			o.Debug("API ERROR BODY: %s", string(b))
		}
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(b))
	}

	const streamIdleTimeout = 300 * time.Second // 5 min idle = dead stream (generous for reasoning models)

	scanner := bufio.NewScanner(&idleTimeoutReader{r: resp.Body, timeout: streamIdleTimeout})
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB lines
	// accumulate tool calls across chunks
	tcAcc := map[int]*ToolCall{}
	chunkCount := 0
	hasContent := false
	lastChunkTime := time.Now()

	for scanner.Scan() {
		now := time.Now()
		idle := now.Sub(lastChunkTime)
		lastChunkTime = now

		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if o.Debug != nil && idle > 5*time.Second {
			o.Debug("STREAM CHUNK %d: idle=%.1fs", chunkCount+1, idle.Seconds())
		}
		chunkCount++
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			if o.Debug != nil {
				o.Debug("STREAM DONE: %d chunks received", chunkCount)
			}
			// flush accumulated tool calls
			if len(tcAcc) > 0 {
				var tcs []ToolCall
				for _, tc := range tcAcc {
					tcs = append(tcs, *tc)
				}
				onDelta(StreamDelta{ToolCalls: tcs, Done: true})
			} else {
				onDelta(StreamDelta{Done: true})
			}
			return nil
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			hasContent = true
			onDelta(StreamDelta{Content: delta.Content})
		}
		for _, tc := range delta.ToolCalls {
			hasContent = true
			if _, ok := tcAcc[tc.Index]; !ok {
				tcAcc[tc.Index] = &ToolCall{Type: "function"}
			}
			acc := tcAcc[tc.Index]
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Function.Name = tc.Function.Name
			}
			acc.Function.Arguments += tc.Function.Arguments
		}
	}
	if o.Debug != nil {
		totalIdle := time.Since(lastChunkTime)
		o.Debug("STREAM END: scanner finished, %d chunks, hasContent=%v, finalIdle=%.1fs, err=%v", chunkCount, hasContent, totalIdle.Seconds(), scanner.Err())
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read error after %d chunks: %w", chunkCount, err)
	}
	// Check if stream ended without [DONE] — likely a broken connection
	if chunkCount > 0 {
		return fmt.Errorf("stream ended without [DONE] after %d chunks (connection may have dropped)", chunkCount)
	}
	if !hasContent {
		return fmt.Errorf("empty response from API (%d chunks parsed)", chunkCount)
	}
	return nil
}
