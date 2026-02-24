package main

import (
	"os"

	"github.com/gal-cli/gal-cli/cmd"
)

func main() {
	if os.Getenv("TERM") == "" {
		os.Setenv("TERM", "xterm-256color")
	}
	cmd.Execute()
}
