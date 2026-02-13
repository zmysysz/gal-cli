package provider

import "context"

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
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
