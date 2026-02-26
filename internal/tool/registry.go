package tool

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// file_read
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

	// file_write
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
		if idx := strings.LastIndex(p, "/"); idx > 0 {
			os.MkdirAll(p[:idx], 0755)
		}
		return "ok", os.WriteFile(p, []byte(content), 0644)
	})

	// file_edit
	r.Register(provider.ToolDef{
		Name:        "file_edit",
		Description: "Edit a file by replacing lines between start_line and end_line (1-based, inclusive) with new content. More efficient than file_write for partial edits.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "File path to edit"},
				"start_line": map[string]any{"type": "integer", "description": "First line to replace (1-based)"},
				"end_line":   map[string]any{"type": "integer", "description": "Last line to replace (1-based, inclusive)"},
				"content":    map[string]any{"type": "string", "description": "Replacement content (replaces lines start_line through end_line)"},
			},
			"required": []string{"path", "start_line", "end_line", "content"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		p, _ := args["path"].(string)
		startLine := toInt(args["start_line"])
		endLine := toInt(args["end_line"])
		content, _ := args["content"].(string)

		if startLine < 1 || endLine < startLine {
			return "", fmt.Errorf("invalid line range: %d-%d", startLine, endLine)
		}

		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		lines := strings.Split(string(data), "\n")
		if startLine > len(lines) {
			return "", fmt.Errorf("start_line %d exceeds file length %d", startLine, len(lines))
		}
		if endLine > len(lines) {
			endLine = len(lines)
		}

		var result []string
		result = append(result, lines[:startLine-1]...)
		result = append(result, content)
		result = append(result, lines[endLine:]...)

		return "ok", os.WriteFile(p, []byte(strings.Join(result, "\n")), 0644)
	})

	// file_list
	r.Register(provider.ToolDef{
		Name:        "file_list",
		Description: "List directory contents as a tree. Returns file/directory names with indentation showing structure.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory path to list"},
				"depth": map[string]any{"type": "integer", "description": "Max depth to recurse (default 3)"},
			},
			"required": []string{"path"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		p, _ := args["path"].(string)
		maxDepth := toInt(args["depth"])
		if maxDepth <= 0 {
			maxDepth = 3
		}

		var sb strings.Builder
		count := 0
		maxEntries := 500

		var walk func(dir string, prefix string, depth int)
		walk = func(dir string, prefix string, depth int) {
			if depth > maxDepth || count >= maxEntries {
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if count >= maxEntries {
					sb.WriteString(prefix + "... (truncated)\n")
					return
				}
				name := e.Name()
				// skip common noise
				if name == ".git" || name == "node_modules" || name == "__pycache__" || name == ".DS_Store" {
					continue
				}
				if e.IsDir() {
					sb.WriteString(prefix + name + "/\n")
					count++
					walk(filepath.Join(dir, name), prefix+"  ", depth+1)
				} else {
					sb.WriteString(prefix + name + "\n")
					count++
				}
			}
		}

		walk(p, "", 1)
		if count == 0 {
			return "empty directory", nil
		}
		return sb.String(), nil
	})

	// grep
	r.Register(provider.ToolDef{
		Name:        "grep",
		Description: "Search for a text pattern in files. Returns matching lines with file path and line number. Searches recursively by default.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Text pattern to search for (substring match, case-insensitive)"},
				"path":    map[string]any{"type": "string", "description": "File or directory to search in"},
				"include": map[string]any{"type": "string", "description": "File glob filter (e.g. \"*.go\", \"*.py\"). Optional."},
			},
			"required": []string{"pattern", "path"},
		},
	}, func(_ context.Context, args map[string]any) (string, error) {
		pattern, _ := args["pattern"].(string)
		p, _ := args["path"].(string)
		include, _ := args["include"].(string)
		patternLower := strings.ToLower(pattern)

		var sb strings.Builder
		matches := 0
		maxMatches := 100

		info, err := os.Stat(p)
		if err != nil {
			return "", err
		}

		searchFile := func(fpath string) {
			if matches >= maxMatches {
				return
			}
			if include != "" {
				matched, _ := filepath.Match(include, filepath.Base(fpath))
				if !matched {
					return
				}
			}
			f, err := os.Open(fpath)
			if err != nil {
				return
			}
			defer f.Close()

			scanner := bufio.NewScanner(f)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				if strings.Contains(strings.ToLower(line), patternLower) {
					sb.WriteString(fmt.Sprintf("%s:%d: %s\n", fpath, lineNum, line))
					matches++
					if matches >= maxMatches {
						sb.WriteString("... (truncated at 100 matches)\n")
						return
					}
				}
			}
		}

		if !info.IsDir() {
			searchFile(p)
		} else {
			filepath.Walk(p, func(fpath string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					name := fi.Name()
					if name == ".git" || name == "node_modules" || name == "__pycache__" || name == "vendor" {
						return filepath.SkipDir
					}
					return nil
				}
				searchFile(fpath)
				if matches >= maxMatches {
					return filepath.SkipAll
				}
				return nil
			})
		}

		if matches == 0 {
			return "no matches found", nil
		}
		return sb.String(), nil
	})

	// bash
	r.Register(provider.ToolDef{
		Name:        "bash",
		Description: "Execute a bash command and return its output. For commands requiring interactive input (passwords, confirmations), provide the command to the user to run manually instead of executing it directly.",
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
		
		// Capture output for non-interactive commands
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out) + "\n" + err.Error(), nil
		}
		return string(out), nil
	})
}

// toInt converts a JSON number (float64) or string to int.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
