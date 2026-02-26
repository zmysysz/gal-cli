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
)

type OpenAI struct {
	APIKey  string
	BaseURL string
	Debug   DebugFunc
}

func (o *OpenAI) ChatStream(ctx context.Context, model string, messages []Message, tools []ToolDef, onDelta func(StreamDelta)) error {
	body := map[string]any{
		"model":    model,
		"messages": messages,
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

	resp, err := doWithRetry(req, payload, o.Debug)
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

	scanner := bufio.NewScanner(resp.Body)
	// accumulate tool calls across chunks
	tcAcc := map[int]*ToolCall{}
	chunkCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
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
			onDelta(StreamDelta{Content: delta.Content})
		}
		for _, tc := range delta.ToolCalls {
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
		o.Debug("STREAM END: scanner finished, %d chunks, err=%v", chunkCount, scanner.Err())
	}
	return scanner.Err()
}
