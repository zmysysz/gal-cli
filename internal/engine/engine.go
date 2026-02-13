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

// ModelID returns the model part after the provider prefix (e.g. "gpt-4o" from "openai/gpt-4o").
func (e *Engine) ModelID() string {
	if i := strings.Index(e.Agent.CurrentModel, "/"); i >= 0 {
		return e.Agent.CurrentModel[i+1:]
	}
	return e.Agent.CurrentModel
}

func (e *Engine) Send(ctx context.Context, userMsg string, onText func(string)) error {
	e.Messages = append(e.Messages, provider.Message{Role: "user", Content: userMsg})

	for {
		var fullContent string
		var toolCalls []provider.ToolCall
		err := e.Provider.ChatStream(ctx, e.ModelID(), e.Messages, e.Agent.ToolDefs, func(d provider.StreamDelta) {
			if d.Content != "" {
				fullContent += d.Content
				onText(d.Content)
			}
			if len(d.ToolCalls) > 0 {
				toolCalls = append(toolCalls, d.ToolCalls...)
			}
		})
		if err != nil {
			return err
		}

		if len(toolCalls) == 0 {
			// no tool calls — done
			e.Messages = append(e.Messages, provider.Message{Role: "assistant", Content: fullContent})
			return nil
		}

		// assistant message with tool calls
		e.Messages = append(e.Messages, provider.Message{Role: "assistant", ToolCalls: toolCalls})

		// execute each tool call
		for _, tc := range toolCalls {
			onText(fmt.Sprintf("\n🔧 Calling: %s\n", tc.Function.Name))
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)

			result, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
			if err != nil {
				result = "error: " + err.Error()
			}

			// show truncated result so user sees something
			preview := result
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			onText(fmt.Sprintf("📎 Result: %s\n", preview))

			e.Messages = append(e.Messages, provider.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
		// loop back to LLM with tool results
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
