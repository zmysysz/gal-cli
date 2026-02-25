package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/gal-cli/gal-cli/internal/provider"
	"github.com/gal-cli/gal-cli/internal/skill"
	"github.com/gal-cli/gal-cli/internal/tool"
)

const lazyThreshold = 1024 // bytes

type Agent struct {
	Conf         *config.AgentConf
	CurrentModel string
	SystemPrompt string // assembled prompt (base + skills)
	ToolDefs     []provider.ToolDef
	Registry     *tool.Registry
}

func Build(conf *config.AgentConf, reg *tool.Registry) (*Agent, error) {
	a := &Agent{
		Conf:         conf,
		CurrentModel: conf.DefaultModel,
		Registry:     reg,
	}

	var sb strings.Builder
	sb.WriteString(conf.SystemPrompt)

	// load all skills, split into eager/lazy
	type loadedSkill struct {
		s   *skill.Skill
		dir string
	}
	var lazySkills []loadedSkill

	for _, sName := range conf.Skills {
		dir, err := skill.Resolve(sName)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", conf.Name, err)
		}
		s, err := skill.Load(dir)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", conf.Name, err)
		}

		if len(s.Prompt) < lazyThreshold {
			// eager: inject full content
			sb.WriteString("\n\n## Skill: " + s.Name + "\n")
			sb.WriteString(s.Prompt)
		} else {
			// lazy: inject name + first line only
			lazySkills = append(lazySkills, loadedSkill{s: s, dir: dir})
		}

		// scripts are always registered
		skill.RegisterScripts(s, reg)
	}

	// add lazy skill summaries + register load_skills tool
	if len(lazySkills) > 0 {
		sb.WriteString("\n\n## Available Skills (use load_skills tool to read full documentation before using these skills)\n")
		skillMap := make(map[string]*skill.Skill)
		for _, ls := range lazySkills {
			meta := parseFrontmatter(ls.s.Prompt)
			name := meta["name"]
			if name == "" {
				name = ls.s.Name
			}
			desc := meta["description"]
			if desc == "" {
				desc = "No description"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s [requires load_skills to view full documentation]\n", name, desc))
			skillMap[ls.s.Name] = ls.s
		}

		reg.Register(provider.ToolDef{
			Name:        "load_skills",
			Description: "Load full SKILL.md documentation for one or more skills. Use this when you need detailed instructions for a skill.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"names": map[string]any{
						"type":        "string",
						"description": "Comma-separated skill names to load (e.g. \"deploy,translate\")",
					},
				},
				"required": []string{"names"},
			},
		}, func(_ context.Context, args map[string]any) (string, error) {
			names, _ := args["names"].(string)
			var result strings.Builder
			for _, name := range strings.Split(names, ",") {
				name = strings.TrimSpace(name)
				s, ok := skillMap[name]
				if !ok {
					result.WriteString(fmt.Sprintf("## %s\nSkill not found.\n\n", name))
					continue
				}
				result.WriteString(fmt.Sprintf("## Skill: %s\n%s\n\n", name, s.Prompt))
			}
			return result.String(), nil
		})
	}

	a.SystemPrompt = sb.String()

	// collect tool defs: built-in (filtered) + all registered (includes skill scripts + load_skills)
	a.ToolDefs = reg.GetDefs(conf.Tools)
	for _, sName := range conf.Skills {
		dir, _ := skill.Resolve(sName)
		s, _ := skill.Load(dir)
		a.ToolDefs = append(a.ToolDefs, s.ScriptDefs...)
	}
	// add load_skills if registered
	if len(lazySkills) > 0 {
		a.ToolDefs = append(a.ToolDefs, reg.GetDefs([]string{"load_skills"})...)
	}

	return a, nil
}

// parseFrontmatter extracts YAML frontmatter (between --- delimiters) as key-value pairs.
func parseFrontmatter(content string) map[string]string {
	m := make(map[string]string)
	if !strings.HasPrefix(content, "---") {
		return m
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return m
	}
	for _, line := range strings.Split(content[3:3+end], "\n") {
		if i := strings.Index(line, ":"); i > 0 {
			m[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return m
}
