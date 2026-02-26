package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gal-cli",
	Short: "GAL-CLI — Multi-agent CLI tool",
	Long: `GAL-CLI — A lightweight, extensible multi-agent CLI tool for LLM workflows.

Features:
  • Multi-agent with on-the-fly switching
  • Universal provider support (OpenAI, Anthropic, DeepSeek, Ollama, Xiaomi)
  • Extensible via Skills and MCP servers
  • Interactive and non-interactive modes
  • Session management with auto-save
  • Smart context compression

Quick Start:
  gal-cli init                    # initialize config
  gal-cli chat                    # start interactive chat
  gal-cli chat -m "hello"         # non-interactive mode
  gal-cli chat --help             # see all options

Examples:
  # Interactive mode
  gal-cli chat -a coder
  gal-cli chat --session abc123

  # Non-interactive mode
  gal-cli chat -m "explain this code"
  echo "test" | gal-cli chat -m -
  gal-cli chat -m @prompt.txt > output.txt`,
	CompletionOptions: cobra.CompletionOptions{HiddenDefaultCmd: true},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
