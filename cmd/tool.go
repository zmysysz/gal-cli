package cmd

import (
	"fmt"
	"strings"

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
				desc := d.Description
				if i := strings.IndexAny(desc, ".\n"); i > 0 {
					desc = desc[:i]
				}
				fmt.Printf("  %-12s %s\n", d.Name, desc)
			}
		},
	})
}
