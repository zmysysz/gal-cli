package skill

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gal-cli/gal-cli/internal/provider"
	"github.com/gal-cli/gal-cli/internal/tool"
)

type Skill struct {
	Name       string
	Dir        string
	Prompt     string   // content of SKILLS.md
	ScriptDefs []provider.ToolDef
}

// Load loads a skill from the given directory.
func Load(dir string) (*Skill, error) {
	name := filepath.Base(dir)
	s := &Skill{Name: name, Dir: dir}

	// load SKILLS.md or SKILL.md
	mdPath := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, fmt.Errorf("skill %s: missing SKILL.md: %w", name, err)
	}
	s.Prompt = string(data)

	// discover scripts
	scriptsDir := filepath.Join(dir, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		// no scripts dir is ok, skill might be prompt-only
		return s, nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		scriptName := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		toolName := fmt.Sprintf("skill:%s:%s", name, scriptName)
		s.ScriptDefs = append(s.ScriptDefs, provider.ToolDef{
			Name:        toolName,
			Description: fmt.Sprintf("Run %s script from skill %s", scriptName, name),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "Input to pass to the script via stdin"},
					"args":  map[string]any{"type": "string", "description": "Command-line arguments"},
				},
			},
		})
	}
	return s, nil
}

// Resolve finds a skill directory by name, searching local then global paths.
func Resolve(name string) (string, error) {
	// project-local
	local := filepath.Join("skills", name)
	if info, err := os.Stat(local); err == nil && info.IsDir() {
		return local, nil
	}
	// user-global
	home, _ := os.UserHomeDir()
	global := filepath.Join(home, ".gal", "skills", name)
	if info, err := os.Stat(global); err == nil && info.IsDir() {
		return global, nil
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

// RegisterScripts registers all skill scripts as tools in the registry.
func RegisterScripts(s *Skill, reg *tool.Registry) {
	scriptsDir := filepath.Join(s.Dir, "scripts")
	for _, def := range s.ScriptDefs {
		scriptFile := strings.TrimPrefix(def.Name, fmt.Sprintf("skill:%s:", s.Name))
		// find the actual file with extension
		entries, _ := os.ReadDir(scriptsDir)
		var fullPath string
		for _, e := range entries {
			if strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())) == scriptFile {
				fullPath = filepath.Join(scriptsDir, e.Name())
				break
			}
		}
		if fullPath == "" {
			continue
		}
		fp := fullPath // capture
		reg.Register(def, func(ctx context.Context, args map[string]any) (string, error) {
			input, _ := args["input"].(string)
			cmdArgs, _ := args["args"].(string)
			var parts []string
			if cmdArgs != "" {
				parts = strings.Fields(cmdArgs)
			}
			cmd := exec.CommandContext(ctx, fp, parts...)
			if input != "" {
				cmd.Stdin = strings.NewReader(input)
			}
			cmd.Dir = s.Dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return string(out) + "\n" + err.Error(), nil
			}
			return string(out), nil
		})
	}
}
