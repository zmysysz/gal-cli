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
  - http
  - file_patch

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
