package cmd

import (
	"fmt"

	"github.com/gal-cli/gal-cli/internal/tool"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "tool list",
		Short: "List all built-in tools",
		Run: func(cmd *cobra.Command, args []string) {
			reg := tool.NewRegistry()
			for _, d := range reg.GetDefs(nil) {
				fmt.Printf("  %-12s %s\n", d.Name, d.Description)
			}
		},
	})
}
