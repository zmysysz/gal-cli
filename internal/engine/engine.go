package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gal-cli/gal-cli/internal/agent"
	"github.com/gal-cli/gal-cli/internal/provider"
)

type Engine struct {
	Agent    *agent.Agent
	Provider provider.Provider
	Messages []provider.Message
}

func New(a *agent.Agent, p provider.Provider) *Engine {
	return &Engine{
		Agent:    a,
		Provider: p,
		Messages: []provider.Message{
			{Role: "system", Content: a.SystemPrompt},
		},
	}
}

func (e *Engine) ModelID() string {
	if i := strings.Index(e.Agent.CurrentModel, "/"); i >= 0 {
		return e.Agent.CurrentModel[i+1:]
	}
	return e.Agent.CurrentModel
}

func (e *Engine) Send(ctx context.Context, userMsg string, onText func(string)) error {
	return e.SendWithCallbacks(ctx, userMsg, onText, nil, nil)
}

func (e *Engine) SendWithCallbacks(ctx context.Context, userMsg string, onText func(string), onToolCall func(string), onToolResult func(string)) error {
	e.Messages = append(e.Messages, provider.Message{Role: "user", Content: userMsg})

	for {
		var fullContent string
		var toolCalls []provider.ToolCall

		err := e.Provider.ChatStream(ctx, e.ModelID(), e.Messages, e.Agent.ToolDefs, func(d provider.StreamDelta) {
			if d.Content != "" {
				fullContent += d.Content
				if onText != nil {
					onText(d.Content)
				}
			}
			if len(d.ToolCalls) > 0 {
				toolCalls = append(toolCalls, d.ToolCalls...)
			}
		})
		if err != nil {
			return err
		}

		if len(toolCalls) == 0 {
			e.Messages = append(e.Messages, provider.Message{Role: "assistant", Content: fullContent})
			return nil
		}

		e.Messages = append(e.Messages, provider.Message{Role: "assistant", ToolCalls: toolCalls})

		for _, tc := range toolCalls {
			if onToolCall != nil {
				onToolCall(tc.Function.Name)
			}

			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)

			result, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
			if err != nil {
				result = "error: " + err.Error()
			}

			if onToolResult != nil {
				preview := result
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				onToolResult(fmt.Sprintf("%s → %s", tc.Function.Name, preview))
			}

			e.Messages = append(e.Messages, provider.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
}

func (e *Engine) Clear() {
	e.Messages = []provider.Message{
		{Role: "system", Content: e.Agent.SystemPrompt},
	}
}

func (e *Engine) SwitchModel(model string) {
	e.Agent.CurrentModel = model
}
