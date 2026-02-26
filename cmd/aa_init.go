package cmd

import "os"

// This file is named "aa_init.go" to ensure it initializes before other files
// in the cmd package (Go processes files in alphabetical order within a package).
// This sets TERM before lipgloss styles are created in chat.go.
func init() {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" || term == "linux" || term == "vt100" {
		os.Setenv("TERM", "xterm-256color")
	}
	if os.Getenv("COLORTERM") == "" {
		os.Setenv("COLORTERM", "truecolor")
	}
}
