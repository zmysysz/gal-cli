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

type Anthropic struct {
	APIKey  string
	BaseURL string
}

func (a *Anthropic) ChatStream(ctx context.Context, model string, messages []Message, tools []ToolDef, onDelta func(StreamDelta)) error {
	// separate system message
	var system string
	var msgs []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		msg := map[string]any{"role": m.Role}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var content []map[string]any
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				json.Unmarshal([]byte(tc.Function.Arguments), &input)
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": input,
				})
			}
			msg["content"] = content
		} else if m.Role == "tool" {
			msg["role"] = "user"
			msg["content"] = []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			}}
		} else {
			msg["content"] = m.Content
		}
		msgs = append(msgs, msg)
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"stream":     true,
		"messages":   msgs,
	}
	if system != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		var defs []map[string]any
		for _, t := range tools {
			defs = append(defs, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Parameters,
			})
		}
		body["tools"] = defs
	}

	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", a.BaseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var currentToolID, currentToolName, currentToolArgs string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				currentToolID = event.ContentBlock.ID
				currentToolName = event.ContentBlock.Name
				currentToolArgs = ""
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				onDelta(StreamDelta{Content: event.Delta.Text})
			} else if event.Delta.Type == "input_json_delta" {
				currentToolArgs += event.Delta.PartialJSON
			}
		case "content_block_stop":
			if currentToolID != "" {
				tc := ToolCall{ID: currentToolID}
				tc.Function.Name = currentToolName
				tc.Function.Arguments = currentToolArgs
				onDelta(StreamDelta{ToolCalls: []ToolCall{tc}})
				currentToolID = ""
			}
		case "message_stop":
			onDelta(StreamDelta{Done: true})
			return nil
		}
	}
	return scanner.Err()
}
