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
	Agent        *agent.Agent
	Provider     provider.Provider
	Messages     []provider.Message
	ContextLimit int
	Debug        bool
	debugFile    *os.File
	debugTurn    int
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

	// wire debug logger to provider
	dbg := provider.DebugFunc(e.debugLog)
	switch p := e.Provider.(type) {
	case *provider.OpenAI:
		p.Debug = dbg
	case *provider.Anthropic:
		p.Debug = dbg
	}
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
	return e.SendWithInteractive(ctx, userMsg, onText, onToolCall, onToolResult, nil)
}

// InteractiveInputRequest represents a request for user input
type InteractiveInputRequest struct {
	Name             string   `json:"name"`
	InteractiveType  string   `json:"interactive_type"`  // "blank" or "select"
	InteractiveHint  string   `json:"interactive_hint"`
	Options          []string `json:"options,omitempty"` // for select type
	Sensitive        bool     `json:"sensitive,omitempty"`
}

// SendWithInteractive adds support for interactive input collection
func (e *Engine) SendWithInteractive(ctx context.Context, userMsg string, onText func(string), onToolCall func(string), onToolResult func(string), onInteractive func([]InteractiveInputRequest) (map[string]string, error)) error {
	e.debugTurn++
	turn := e.debugTurn
	round := 0

	e.Messages = append(e.Messages, provider.Message{Role: "user", Content: userMsg})
	e.debugLog("========== TURN %d ==========", turn)
	e.debugLog("USER: %s", userMsg)

	const maxRounds = 50

	for {
		round++
		if round > maxRounds {
			return fmt.Errorf("agentic loop exceeded %d rounds, stopping", maxRounds)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
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

		// Check if any tool calls are 'interactive' tool
		var interactiveRequests []InteractiveInputRequest
		var interactiveToolIndex int = -1
		
		for i, tc := range toolCalls {
			// Check if this is the 'interactive' tool
			if tc.Function.Name == "interactive" {
				var args map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
				
				// Extract fields array
				if fieldsRaw, ok := args["fields"].([]any); ok {
					for _, fieldRaw := range fieldsRaw {
						if fieldMap, ok := fieldRaw.(map[string]any); ok {
							// Check if type is "interactive_input"
							if typeVal := getStringField(fieldMap, "type"); typeVal == "interactive_input" {
								req := InteractiveInputRequest{
									Name:            getStringField(fieldMap, "name"),
									InteractiveType: getStringField(fieldMap, "interactive_type"),
									InteractiveHint: getStringField(fieldMap, "interactive_hint"),
									Sensitive:       getBoolField(fieldMap, "sensitive"),
								}
								
								// Extract options for select type
								if opts, ok := fieldMap["options"].([]any); ok {
									for _, opt := range opts {
										if s, ok := opt.(string); ok {
											req.Options = append(req.Options, s)
										}
									}
								}
								
								interactiveRequests = append(interactiveRequests, req)
							}
						}
					}
					interactiveToolIndex = i
					break // Only process first interactive tool call
				}
			}
		}
		
		// If we have interactive requests and a handler, collect input
		var interactiveResults map[string]string
		if len(interactiveRequests) > 0 && onInteractive != nil {
			var err error
			interactiveResults, err = onInteractive(interactiveRequests)
			if err != nil {
				return err
			}
		}

		// Process all tool calls — readonly tools run in parallel, others serial
		type toolResult struct {
			index  int
			result string
		}

		// Identify readonly batch (only if ALL are readonly and no interactive)
		allReadOnly := interactiveToolIndex < 0
		if allReadOnly {
			for _, tc := range toolCalls {
				if !e.Agent.Registry.IsReadOnly(tc.Function.Name) {
					allReadOnly = false
					break
				}
			}
		}

		results := make([]string, len(toolCalls))

		if allReadOnly && len(toolCalls) > 1 {
			// parallel execution
			ch := make(chan toolResult, len(toolCalls))
			for i, tc := range toolCalls {
				if onToolCall != nil {
					onToolCall(tc.Function.Name)
				}
				go func(idx int, tc provider.ToolCall) {
					var args map[string]any
					json.Unmarshal([]byte(tc.Function.Arguments), &args)
					e.debugLog("TOOL_CALL[parallel]: %s args=%s", tc.Function.Name, tc.Function.Arguments)
					res, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
					if err != nil {
						res = "error: " + err.Error()
					}
					ch <- toolResult{idx, res}
				}(i, tc)
			}
			for range toolCalls {
				tr := <-ch
				results[tr.index] = tr.result
			}
		} else {
			// serial execution
			for i, tc := range toolCalls {
				if onToolCall != nil {
					onToolCall(tc.Function.Name)
				}

				var args map[string]any
				json.Unmarshal([]byte(tc.Function.Arguments), &args)

				e.debugLog("TOOL_CALL: %s args=%s", tc.Function.Name, tc.Function.Arguments)

				if i == interactiveToolIndex && interactiveResults != nil {
					resultJSON, _ := json.Marshal(interactiveResults)
					results[i] = string(resultJSON)
				} else {
					res, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
					if err != nil {
						res = "error: " + err.Error()
					}
					results[i] = res
				}
			}
		}

		// Emit results and append messages
		for i, tc := range toolCalls {
			result := results[i]
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

// estimateTokens estimates token count from character length.
func estimateTokens(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return int(float64(total) / 2.5)
}

// NeedsCompression returns true if estimated tokens exceed the context limit.
func (e *Engine) NeedsCompression() bool {
	if e.ContextLimit <= 0 {
		return false
	}
	return estimateTokens(e.Messages) > e.ContextLimit
}

// Compress summarizes old messages to reduce context size.
// onStatus is called with status text (e.g. for TUI display).
func (e *Engine) Compress(ctx context.Context, onStatus func(string)) error {
	if !e.NeedsCompression() {
		return nil
	}
	if onStatus != nil {
		onStatus("compressing context...")
	}
	defer func() {
		if onStatus != nil {
			onStatus("")
		}
	}()

	// skip system message at index 0
	msgs := e.Messages[1:]
	targetTokens := int(float64(e.ContextLimit) * 0.8)

	// find compress boundary: accumulate from oldest, respect tool_call groups
	accum := 0
	cutIdx := 0 // index in msgs (not e.Messages)
	for cutIdx < len(msgs) {
		m := msgs[cutIdx]
		mtokens := int(float64(len(m.Content)) / 2.5)
		for _, tc := range m.ToolCalls {
			mtokens += int(float64(len(tc.Function.Name)+len(tc.Function.Arguments)) / 2.5)
		}

		if accum+mtokens > targetTokens {
			break
		}
		accum += mtokens
		cutIdx++

		// if this was an assistant with tool_calls, include all following tool results
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for cutIdx < len(msgs) && msgs[cutIdx].Role == "tool" {
				tm := msgs[cutIdx]
				accum += int(float64(len(tm.Content)) / 2.5)
				cutIdx++
			}
		}
	}

	if cutIdx == 0 {
		return nil
	}

	compressZone := msgs[:cutIdx]
	keepZone := msgs[cutIdx:]

	// build compression request (isolated from conversation)
	compressMessages := []provider.Message{
		{Role: "system", Content: "Summarize the following conversation concisely, preserving key decisions, code changes, file paths, and technical details. Output in the same language as the conversation."},
	}
	// pack compress zone as a single user message
	var sb strings.Builder
	for _, m := range compressZone {
		switch {
		case m.Role == "user":
			sb.WriteString("User: " + m.Content + "\n\n")
		case m.Role == "assistant" && m.Content != "":
			sb.WriteString("Assistant: " + m.Content + "\n\n")
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				sb.WriteString(fmt.Sprintf("Assistant called tool %s(%s)\n", tc.Function.Name, tc.Function.Arguments))
			}
			sb.WriteString("\n")
		case m.Role == "tool":
			preview := m.Content
			if len(preview) > 500 {
				preview = preview[:500] + "...(truncated)"
			}
			sb.WriteString("Tool result: " + preview + "\n\n")
		}
	}
	compressMessages = append(compressMessages, provider.Message{Role: "user", Content: sb.String()})

	e.debugLog("COMPRESS: zone=%d msgs, keep=%d msgs, estimated_tokens=%d", len(compressZone), len(keepZone), accum)

	// call LLM for summary
	var summary string
	err := e.Provider.ChatStream(ctx, e.ModelID(), compressMessages, nil, func(d provider.StreamDelta) {
		summary += d.Content
	})
	if err != nil {
		e.debugLog("COMPRESS ERROR: %v", err)
		return err
	}

	e.debugLog("COMPRESS DONE: summary=%d chars", len(summary))

	// rebuild messages: system + compressed summary + keep zone
	newMessages := []provider.Message{
		e.Messages[0], // original system prompt
		{Role: "system", Content: "[Compressed context from earlier conversation]\n" + summary},
	}
	newMessages = append(newMessages, keepZone...)
	e.Messages = newMessages

	return nil
}

// Helper functions for extracting fields from map[string]any
func getStringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
