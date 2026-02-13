package agent

import (
	"fmt"
	"strings"

	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/gal-cli/gal-cli/internal/provider"
	"github.com/gal-cli/gal-cli/internal/skill"
	"github.com/gal-cli/gal-cli/internal/tool"
)

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

	// assemble system prompt: base + skill prompts
	var sb strings.Builder
	sb.WriteString(conf.SystemPrompt)

	for _, sName := range conf.Skills {
		dir, err := skill.Resolve(sName)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", conf.Name, err)
		}
		s, err := skill.Load(dir)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", conf.Name, err)
		}
		sb.WriteString("\n\n## Skill: " + s.Name + "\n")
		sb.WriteString(s.Prompt)
		skill.RegisterScripts(s, reg)
	}
	a.SystemPrompt = sb.String()

	// collect tool defs: built-in (filtered) + skill scripts
	a.ToolDefs = reg.GetDefs(conf.Tools)
	// add skill script defs
	for _, sName := range conf.Skills {
		dir, _ := skill.Resolve(sName)
		s, _ := skill.Load(dir)
		a.ToolDefs = append(a.ToolDefs, s.ScriptDefs...)
	}

	return a, nil
}
