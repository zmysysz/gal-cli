package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gal-cli/gal-cli/internal/provider"
)

type Handler func(ctx context.Context, args map[string]any) (string, error)

type Registry struct {
	tools    map[string]Handler
	toolDefs map[string]provider.ToolDef
}

func NewRegistry() *Registry {
	r := &Registry{
		tools:    make(map[string]Handler),
		toolDefs: make(map[string]provider.ToolDef),
	}
	r.registerBuiltins()
	return r
}

func (r *Registry) Register(def provider.ToolDef, h Handler) {
	r.tools[def.Name] = h
	r.toolDefs[def.Name] = def
}

func (r *Registry) GetDefs(names []string) []provider.ToolDef {
	if len(names) == 0 {
		// return all
		defs := make([]provider.ToolDef, 0, len(r.toolDefs))
		for _, d := range r.toolDefs {
			defs = append(defs, d)
		}
		return defs
	}
	var defs []provider.ToolDef
	for _, n := range names {
		if d, ok := r.toolDefs[n]; ok {
			defs = append(defs, d)
		}
	}
	return defs
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	h, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return h(ctx, args)
}

func (r *Registry) registerBuiltins() {
	r.Register(provider.ToolDef{
		Name:        "file_read",
		Description: "Read the contents of a file at the given path",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "File path to read"},
			},
			"required": []string{"path"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		p, _ := args["path"].(string)
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		return string(data), nil
	})

	r.Register(provider.ToolDef{
		Name:        "file_write",
		Description: "Write content to a file at the given path, creating directories as needed",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path to write"},
				"content": map[string]any{"type": "string", "description": "Content to write"},
			},
			"required": []string{"path", "content"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		p, _ := args["path"].(string)
		content, _ := args["content"].(string)
		if dir := strings.TrimRight(p, "/"); dir != p {
			os.MkdirAll(dir, 0755)
		}
		if idx := strings.LastIndex(p, "/"); idx > 0 {
			os.MkdirAll(p[:idx], 0755)
		}
		return "ok", os.WriteFile(p, []byte(content), 0644)
	})

	r.Register(provider.ToolDef{
		Name:        "bash",
		Description: "Execute a bash command and return its output",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		command, _ := args["command"].(string)
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out) + "\n" + err.Error(), nil // return output even on error
		}
		return string(out), nil
	})
}
