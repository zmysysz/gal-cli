package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/spf13/cobra"
)

var defaultGalYAML = `default_agent: default

providers:
  openai:
    type: openai
    api_key: ${OPENAI_API_KEY}
    base_url: https://api.openai.com/v1
    models:
      - gpt-4o
      - gpt-4o-mini
  anthropic:
    type: anthropic
    api_key: ${ANTHROPIC_API_KEY}
    base_url: https://api.anthropic.com
    models:
      - claude-sonnet-4-20250514
      - claude-haiku-4-20250414
  deepseek:
    type: openai
    api_key: ${DEEPSEEK_API_KEY}
    base_url: https://api.deepseek.com/v1
    models:
      - deepseek-chat
      - deepseek-reasoner
  zhipu:
    type: openai
    api_key: ${ZHIPU_API_KEY}
    base_url: https://open.bigmodel.cn/api/paas/v4
    models:
      - glm-4-plus
      - glm-4-flash
  ollama:
    type: openai
    base_url: http://localhost:11434/v1
    models:
      - llama3
      - qwen2
`

var defaultAgentYAML = `name: default
description: General-purpose assistant
system_prompt: |
  You are a helpful assistant.
  
  ## Interactive Input
  
  When you need information from the user, ALWAYS use the 'interactive' tool instead of 
  asking in text. This provides a better user experience.
  
  Use cases:
  - Passwords, API keys, tokens
  - File paths, configuration values
  - Choices and confirmations
  - Any information needed for commands (sudo password, SSH passphrase, etc.)
  
  CRITICAL: If a command requires interactive input (sudo password, SSH key passphrase, 
  database credentials), you MUST:
  1. Use 'interactive' tool to collect the information FIRST
  2. Then use the collected values in your bash command
  
  Example - sudo command:
  Step 1: interactive({"fields": [{"name": "password", "type": "interactive_input", 
          "interactive_type": "blank", "interactive_hint": "Enter sudo password", 
          "sensitive": true}]})
  Step 2: bash({"command": "echo $password | sudo -S apt install package"})
  
  ## Write Operation Confirmation
  
  Before performing write operations (file_write, file_edit, or bash commands that 
  modify files/system), use the 'interactive' tool to confirm:
  - Show what will be changed
  - Ask for confirmation with options: ["yes", "no", "trust (don't ask again)"]
  - Only proceed if user confirms "yes" or "trust"

models:
  - openai/gpt-4o
  - openai/gpt-4o-mini
  - anthropic/claude-sonnet-4-20250514
  - anthropic/claude-haiku-4-20250414
  - deepseek/deepseek-chat
  - deepseek/deepseek-reasoner
  - zhipu/glm-4-plus
  - zhipu/glm-4-flash
  - ollama/llama3
default_model: openai/gpt-4o

tools:
  - file_read
  - file_write
  - file_edit
  - file_list
  - grep
  - bash
  - interactive

skills: []

# mcps:
#   example:
#     url: https://mcp.example.com/rpc
#     headers:
#       Authorization: "Bearer ${MCP_TOKEN}"
`

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize default config in ~/.gal/",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := config.GalDir()
			agentsDir := filepath.Join(dir, "agents")
			skillsDir := filepath.Join(dir, "skills")
			os.MkdirAll(agentsDir, 0755)
			os.MkdirAll(skillsDir, 0755)

			galPath := filepath.Join(dir, "gal.yaml")
			if _, err := os.Stat(galPath); os.IsNotExist(err) {
				os.WriteFile(galPath, []byte(defaultGalYAML), 0644)
				fmt.Println("Created", galPath)
			} else {
				fmt.Println("Exists", galPath)
			}

			agentPath := filepath.Join(agentsDir, "default.yaml")
			if _, err := os.Stat(agentPath); os.IsNotExist(err) {
				os.WriteFile(agentPath, []byte(defaultAgentYAML), 0644)
				fmt.Println("Created", agentPath)
			} else {
				fmt.Println("Exists", agentPath)
			}

			fmt.Println("✅ GAL-CLI initialized at", dir)
			return nil
		},
	})
}
