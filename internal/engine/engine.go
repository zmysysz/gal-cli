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
	Agent           *agent.Agent
	Provider        provider.Provider
	Messages        []provider.Message
	ContextLimit    int
	Debug           bool
	debugFile       *os.File
	debugTurn       int
	sensitiveValues []string // values to mask in display/logs
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
	s := string(b)
	for _, sv := range e.sensitiveValues {
		s = strings.ReplaceAll(s, sv, "********")
	}
	go e.debugLog("%s:\n%s", label, s)
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
	// Clean up any incomplete tool_call sequences from previous cancelled requests
	e.cleanIncompleteToolCalls()

	e.debugTurn++
	turn := e.debugTurn
	round := 0

	snapshot := len(e.Messages) // rollback point on failure
	e.Messages = append(e.Messages, provider.Message{Role: "user", Content: userMsg})
	e.debugLog("========== TURN %d ==========", turn)
	e.debugLog("USER: %s", userMsg)

	rollback := func() {
		e.Messages = e.Messages[:snapshot]
		e.debugLog("ROLLBACK: messages restored to %d", snapshot)
	}

	const maxRounds = 50

	for {
		round++
		if round > maxRounds {
			rollback()
			return fmt.Errorf("agentic loop exceeded %d rounds, stopping", maxRounds)
		}
		if ctx.Err() != nil {
			rollback()
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
			rollback()
			return err
		}

		if len(toolCalls) == 0 {
			e.Messages = append(e.Messages, provider.Message{Role: "assistant", Content: fullContent})
			e.debugLog("RESPONSE turn %d / round %d: text (%d chars)", turn, round, len(fullContent))
			if fullContent == "" {
				rollback()
				return fmt.Errorf("empty response from %s (no content, no tool calls, round %d)", e.Agent.CurrentModel, round)
			}
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
							req := InteractiveInputRequest{
								Name:            getStringField(fieldMap, "name"),
								InteractiveType: getStringField(fieldMap, "interactive_type"),
								InteractiveHint: getStringField(fieldMap, "interactive_hint"),
								Sensitive:       getBoolField(fieldMap, "sensitive"),
							}
							if req.InteractiveType == "" {
								req.InteractiveType = "blank"
							}
							if req.InteractiveHint == "" {
								req.InteractiveHint = req.Name
							}
							
							// Extract options for select type
							if opts, ok := fieldMap["options"].([]any); ok {
								for _, opt := range opts {
									if s, ok := opt.(string); ok {
										req.Options = append(req.Options, s)
									}
								}
								if req.InteractiveType == "blank" && len(req.Options) > 0 {
									req.InteractiveType = "select"
								}
							}
							
							interactiveRequests = append(interactiveRequests, req)
						}
					}
					interactiveToolIndex = i
					break // Only process first interactive tool call
				}
			}
		}
		
		// If we have interactive requests and a handler, collect input
		var interactiveResults map[string]string
		var sensitiveKeys map[string]bool
		if len(interactiveRequests) > 0 && onInteractive != nil {
			var err error
			interactiveResults, err = onInteractive(interactiveRequests)
			if err != nil {
				rollback()
				return err
			}
			// Track which fields are sensitive for masking in display/logs
			sensitiveKeys = make(map[string]bool)
			for _, req := range interactiveRequests {
				if req.Sensitive {
					sensitiveKeys[req.Name] = true
					if v := interactiveResults[req.Name]; v != "" {
						e.sensitiveValues = append(e.sensitiveValues, v)
					}
				}
			}
		}

		// Process all tool calls — readonly tools run in parallel, others serial
		type toolResult struct {
			index   int
			result  string
			elapsed time.Duration
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

		results := make([]toolResult, len(toolCalls))

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
					start := time.Now()
					res, err := e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
					elapsed := time.Since(start)
					if err != nil {
						res = "error: " + err.Error()
					}
					ch <- toolResult{idx, res, elapsed}
				}(i, tc)
			}
			for range toolCalls {
				tr := <-ch
				results[tr.index] = tr
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

				start := time.Now()
				var res string
				if i == interactiveToolIndex && interactiveResults != nil {
					resultJSON, _ := json.Marshal(interactiveResults)
					res = string(resultJSON)
				} else {
					var err error
					res, err = e.Agent.Registry.Execute(ctx, tc.Function.Name, args)
					if err != nil {
						res = "error: " + err.Error()
					}
				}
				results[i] = toolResult{i, res, time.Since(start)}
			}
		}

		// Emit results and append messages
		for i, tc := range toolCalls {
			tr := results[i]

			// Build masked version for display/logs if this is interactive with sensitive fields
			displayResult := tr.result
			if i == interactiveToolIndex && len(sensitiveKeys) > 0 {
				var parsed map[string]any
				if json.Unmarshal([]byte(tr.result), &parsed) == nil {
					for k := range sensitiveKeys {
						if _, ok := parsed[k]; ok {
							parsed[k] = "********"
						}
					}
					if masked, err := json.Marshal(parsed); err == nil {
						displayResult = string(masked)
					}
				}
			}

			e.debugLog("TOOL_RESULT: %s (%d chars, %v) %s", tc.Function.Name, len(tr.result), tr.elapsed, displayResult)

			if onToolResult != nil {
				preview := displayResult
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				onToolResult(fmt.Sprintf("%s → %s (%.1fs)", tc.Function.Name, preview, tr.elapsed.Seconds()))
			}

			e.Messages = append(e.Messages, provider.Message{
				Role:       "tool",
				Content:    tr.result,
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

// cleanIncompleteToolCalls strips trailing incomplete tool_call sequences
// (assistant with tool_calls not followed by matching tool results).
func (e *Engine) cleanIncompleteToolCalls() {
	for len(e.Messages) > 0 {
		last := e.Messages[len(e.Messages)-1]
		if last.Role == "assistant" && last.Content != "" && len(last.ToolCalls) == 0 {
			break
		}
		if last.Role == "user" || last.Role == "system" {
			break
		}
		if last.Role == "tool" || (last.Role == "assistant" && len(last.ToolCalls) > 0) {
			e.debugLog("CLEAN: removing trailing %s message", last.Role)
			e.Messages = e.Messages[:len(e.Messages)-1]
			continue
		}
		break
	}
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
