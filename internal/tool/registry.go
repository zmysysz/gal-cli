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
	"time"

	"github.com/gal-cli/gal-cli/internal/provider"
)

type Handler func(ctx context.Context, args map[string]any) (string, error)

type Registry struct {
	tools    map[string]Handler
	toolDefs map[string]provider.ToolDef
	readonly map[string]bool
}

func NewRegistry() *Registry {
	r := &Registry{
		tools:    make(map[string]Handler),
		toolDefs: make(map[string]provider.ToolDef),
		readonly: make(map[string]bool),
	}
	r.registerBuiltins()
	return r
}

func (r *Registry) Register(def provider.ToolDef, h Handler) {
	r.tools[def.Name] = h
	r.toolDefs[def.Name] = def
}

func (r *Registry) RegisterReadOnly(def provider.ToolDef, h Handler) {
	r.Register(def, h)
	r.readonly[def.Name] = true
}

func (r *Registry) IsReadOnly(name string) bool {
	return r.readonly[name]
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
	r.registerHTTP()
	r.registerPatch()

	// file_read
	r.RegisterReadOnly(provider.ToolDef{
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
		lines := strings.Count(string(data), "\n") + 1
		size := len(data)
		return fmt.Sprintf("[read %s: %d lines, %d bytes]\n%s", p, lines, size, string(data)), nil
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
		// check if file exists for diff
		oldData, readErr := os.ReadFile(p)
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			return "", err
		}
		lines := strings.Count(content, "\n") + 1
		if readErr != nil {
			return fmt.Sprintf("created %s (%d lines, %d bytes)", p, lines, len(content)), nil
		}
		result := fmt.Sprintf("wrote %s (%d lines, %d bytes)", p, lines, len(content))
		if diff := FormatDiff(string(oldData), content); diff != "" {
			result += "\n" + diff
		}
		return result, nil
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

		if err := os.WriteFile(p, []byte(strings.Join(result, "\n")), 0644); err != nil {
			return "", err
		}
		oldChunk := strings.Join(lines[startLine-1:endLine], "\n")
		newLines := strings.Count(content, "\n") + 1
		replaced := endLine - startLine + 1
		msg := fmt.Sprintf("edited %s: replaced lines %d-%d (%d lines) with %d lines", p, startLine, endLine, replaced, newLines)
		if diff := FormatDiff(oldChunk, content); diff != "" {
			msg += "\n" + diff
		}
		return msg, nil
	})

	// file_list
	r.RegisterReadOnly(provider.ToolDef{
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
			return fmt.Sprintf("%s: empty directory", p), nil
		}
		return fmt.Sprintf("[%s: %d entries]\n%s", p, count, sb.String()), nil
	})

	// grep
	r.RegisterReadOnly(provider.ToolDef{
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
			return fmt.Sprintf("no matches for '%s' in %s", pattern, p), nil
		}
		return fmt.Sprintf("[%d matches for '%s' in %s]\n%s", matches, pattern, p, sb.String()), nil
	})

	// bash
	r.Register(provider.ToolDef{
		Name:        "bash",
		Description: "Execute a bash command and return its output. For commands requiring passwords (sudo, ssh), use the 'interactive' tool to collect the password first, then use 'sudo -S' or 'sshpass'. For interactive editors (vim, nano), use file_write/file_edit tools instead. Commands timeout after 30 seconds.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Bash command to execute"},
			},
			"required": []string{"command"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		command, _ := args["command"].(string)
		
		// Check for interactive commands
		trimmedCmd := strings.TrimSpace(command)
		interactiveCmds := []string{"vim", "vi", "nano", "emacs", "top", "htop", "less", "more"}
		for _, icmd := range interactiveCmds {
			if strings.HasPrefix(trimmedCmd, icmd+" ") || trimmedCmd == icmd {
				return "", fmt.Errorf("interactive command '%s' not supported - use file_write/file_edit for editing, or run command manually", icmd)
			}
		}
		
		// Check for sudo without -S flag
		if strings.Contains(trimmedCmd, "sudo ") && !strings.Contains(trimmedCmd, "sudo -S") && !strings.Contains(trimmedCmd, "NOPASSWD") {
			return "", fmt.Errorf("sudo requires password - use 'interactive' tool to collect password, then use 'echo $password | sudo -S command'")
		}
		
		// Add timeout
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		
		cmd := exec.CommandContext(ctx, "bash", "-c", command)
		
		// Capture output for non-interactive commands
		out, err := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timeout after 30 seconds - may be waiting for input")
		}
		if err != nil {
			return fmt.Sprintf("[exit %s]\n%s", err.Error(), string(out)), nil
		}
		if len(out) == 0 {
			return "(no output)", nil
		}
		return string(out), nil
	})

	// interactive
	r.Register(provider.ToolDef{
		Name:        "interactive",
		Description: "Collect user input interactively. Use this when you need information from the user instead of asking in text. CRITICAL: If a bash command requires interactive input (sudo password, SSH passphrase, database credentials), you MUST use this tool FIRST to collect the information, then use the values in your command. Examples: (1) sudo: collect password with this tool, then use 'echo $password | sudo -S command'. (2) SSH key: collect key_type and passphrase, then use in ssh-keygen. IMPORTANT: Before performing write operations, dangerous operations, privacy-related actions, or system modifications (file_write, file_edit, bash commands that modify files/system/network), you MUST use this tool to get user confirmation with options [\"yes\", \"no\", \"trust\"]. Only proceed if user selects \"yes\" or \"trust\". If \"trust\" is selected, you may skip confirmation for similar operations in this conversation. You can request multiple fields at once, and the user will be prompted for each one sequentially. Returns a JSON object with all collected values.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"fields": map[string]any{
					"type":        "array",
					"description": "List of fields to collect from the user",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{
								"type":        "string",
								"description": "Field identifier (used as key in result)",
							},
							"type": map[string]any{
								"type":        "string",
								"description": "Must be 'interactive_input' to trigger interactive collection",
								"enum":        []string{"interactive_input"},
							},
							"interactive_type": map[string]any{
								"type":        "string",
								"description": "Input type: 'blank' for free text, 'select' for choosing from options",
								"enum":        []string{"blank", "select"},
							},
							"interactive_hint": map[string]any{
								"type":        "string",
								"description": "Prompt text shown to the user",
							},
							"options": map[string]any{
								"type":        "array",
								"description": "Available choices (required for 'select' type)",
								"items":       map[string]any{"type": "string"},
							},
							"sensitive": map[string]any{
								"type":        "boolean",
								"description": "Whether this is sensitive data like passwords (shows 🔒 indicator)",
							},
						},
						"required": []string{"name", "type", "interactive_type", "interactive_hint"},
					},
				},
			},
			"required": []string{"fields"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		// This is a placeholder - the actual interactive collection
		// is handled by the engine when it detects type="interactive_input"
		return "interactive input collected", nil
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
