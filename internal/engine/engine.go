package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gal-cli/gal-cli/internal/agent"
	"github.com/gal-cli/gal-cli/internal/provider"
)

type Engine struct {
	Agent    *agent.Agent
	Provider provider.Provider
	Messages []provider.Message
	Debug    bool
	debugFile *os.File
	debugTurn int
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

func (e *Engine) InitDebug() {
	if e.debugFile != nil {
		return
	}
	name := fmt.Sprintf("/tmp/gal-debug-%s.log", time.Now().Format("20060102-150405"))
	f, err := os.Create(name)
	if err != nil {
		return
	}
	e.debugFile = f
	fmt.Fprintf(os.Stderr, "🐛 Debug log: %s\n", name)
}

func (e *Engine) debugLog(format string, args ...any) {
	if e.debugFile == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(e.debugFile, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

func (e *Engine) debugJSON(label string, v any) {
	if e.debugFile == nil {
		return
	}
	b, _ := json.Marshal(v)
	go e.debugLog("%s:\n%s", label, string(b))
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
	e.debugTurn++
	turn := e.debugTurn
	round := 0

	e.Messages = append(e.Messages, provider.Message{Role: "user", Content: userMsg})
	e.debugLog("========== TURN %d ==========", turn)
	e.debugLog("USER: %s", userMsg)

	for {
		round++
		var fullContent string
		var toolCalls []provider.ToolCall

		e.debugLog("--- turn %d / round %d --- model=%s messages=%d", turn, round, e.Agent.CurrentModel, len(e.Messages))
		e.debugJSON(fmt.Sprintf("REQUEST turn %d / round %d", turn, round), map[string]any{
			"model":    e.ModelID(),
			"messages": e.Messages,
			"tools":    e.Agent.ToolDefs,
		})

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
			e.debugLog("ERROR turn %d / round %d: %v", turn, round, err)
			return err
		}

		if len(toolCalls) == 0 {
			e.Messages = append(e.Messages, provider.Message{Role: "assistant", Content: fullContent})
			e.debugLog("RESPONSE turn %d / round %d: text (%d chars)", turn, round, len(fullContent))
			return nil
		}

		e.Messages = append(e.Messages, provider.Message{Role: "assistant", ToolCalls: toolCalls})
		e.debugLog("RESPONSE turn %d / round %d: %d tool calls", turn, round, len(toolCalls))

		for _, tc := range toolCalls {
			if onToolCall != nil {
				onToolCall(tc.Function.Name)
			}

			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)

			e.debugLog("TOOL_CALL: %s args=%s", tc.Function.Name, tc.Function.Arguments)

			result, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
			if err != nil {
				result = "error: " + err.Error()
			}

			e.debugLog("TOOL_RESULT: %s (%d chars)", tc.Function.Name, len(result))

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

func (e *Engine) Close() {
	if e.debugFile != nil {
		e.debugFile.Close()
	}
}
