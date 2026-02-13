package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/gal-cli/gal-cli/internal/agent"
	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/gal-cli/gal-cli/internal/engine"
	"github.com/gal-cli/gal-cli/internal/provider"
	"github.com/gal-cli/gal-cli/internal/tool"
	"github.com/spf13/cobra"
)

func init() {
	var agentName string
	chatCmd := &cobra.Command{
		Use:   "chat",
		Short: "Start interactive chat",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(agentName)
		},
	}
	chatCmd.Flags().StringVarP(&agentName, "agent", "a", "", "Agent name (default: from config)")
	rootCmd.AddCommand(chatCmd)
}

func runChat(agentName string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("run 'gal init' first: %w", err)
	}

	if agentName == "" {
		agentName = cfg.DefaultAgent
	}

	reg := tool.NewRegistry()
	eng, err := buildEngine(cfg, agentName, reg)
	if err != nil {
		return err
	}

	fmt.Printf("🤖 Agent: %s | Model: %s\n", eng.Agent.Conf.Name, eng.Agent.CurrentModel)
	fmt.Println("Type /help for commands, /quit to exit\n")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     filepath.Join(config.GalDir(), "history"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return err
	}
	defer rl.Close()

	for {
		line, err := rl.Readline()
		if err == readline.ErrInterrupt || err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// handle slash commands
		if strings.HasPrefix(input, "/") {
			done, err := handleCommand(input, eng, cfg, reg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			}
			if done {
				return nil
			}
			continue
		}

		// send to LLM
		if err := eng.Send(context.Background(), input, func(text string) {
			fmt.Print(text)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "\n❌ %v\n", err)
		}
		fmt.Println() // ensure newline after response
	}
	return nil
}

func handleCommand(input string, eng *engine.Engine, cfg *config.Config, reg *tool.Registry) (quit bool, err error) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/quit", "/exit":
		fmt.Println("Bye!")
		return true, nil

	case "/clear":
		eng.Clear()
		fmt.Println("🗑️  Conversation cleared")

	case "/help":
		fmt.Println(`Commands:
  /agent list          List agents
  /agent <name>        Switch agent
  /model list          List models for current agent
  /model <name>        Switch model
  /clear               Clear conversation
  /quit                Exit`)

	case "/agent":
		if len(parts) < 2 {
			fmt.Printf("Current: %s\n", eng.Agent.Conf.Name)
			return false, nil
		}
		if parts[1] == "list" {
			names, err := config.ListAgents()
			if err != nil {
				return false, err
			}
			for _, n := range names {
				marker := "  "
				if n == eng.Agent.Conf.Name {
					marker = "▶ "
				}
				fmt.Printf("%s%s\n", marker, n)
			}
			return false, nil
		}
		// switch agent
		newEng, err := buildEngine(cfg, parts[1], reg)
		if err != nil {
			return false, err
		}
		*eng = *newEng
		fmt.Printf("🔄 Switched to agent: %s (model: %s)\n", eng.Agent.Conf.Name, eng.Agent.CurrentModel)

	case "/model":
		if len(parts) < 2 {
			fmt.Printf("Current: %s\n", eng.Agent.CurrentModel)
			return false, nil
		}
		if parts[1] == "list" {
			for _, m := range eng.Agent.Conf.Models {
				marker := "  "
				if m == eng.Agent.CurrentModel {
					marker = "▶ "
				}
				fmt.Printf("%s%s\n", marker, m)
			}
			return false, nil
		}
		eng.SwitchModel(parts[1])
		fmt.Printf("🔄 Model: %s\n", eng.Agent.CurrentModel)

	default:
		fmt.Printf("Unknown command: %s (type /help)\n", cmd)
	}
	return false, nil
}

func buildEngine(cfg *config.Config, agentName string, reg *tool.Registry) (*engine.Engine, error) {
	agentConf, err := config.LoadAgent(agentName)
	if err != nil {
		return nil, err
	}

	a, err := agent.Build(agentConf, reg)
	if err != nil {
		return nil, err
	}

	// resolve provider from model name (format: provider/model)
	parts := strings.SplitN(a.CurrentModel, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected provider/model)", a.CurrentModel)
	}
	providerName := parts[0]
	pConf, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	var p provider.Provider
	switch pConf.Type {
	case "anthropic":
		p = &provider.Anthropic{
			APIKey:  os.ExpandEnv(pConf.APIKey),
			BaseURL: pConf.BaseURL,
		}
	default: // "openai" or empty
		p = &provider.OpenAI{
			APIKey:  os.ExpandEnv(pConf.APIKey),
			BaseURL: pConf.BaseURL,
		}
	}

	return engine.New(a, p), nil
}
