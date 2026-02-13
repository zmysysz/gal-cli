package cmd

import (
	"fmt"

	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}

	agentCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := config.ListAgents()
			if err != nil {
				return err
			}
			for _, n := range names {
				a, _ := config.LoadAgent(n)
				desc := ""
				if a != nil {
					desc = a.Description
				}
				fmt.Printf("  %-15s %s\n", n, desc)
			}
			return nil
		},
	})

	agentCmd.AddCommand(&cobra.Command{
		Use:   "show [name]",
		Short: "Show agent config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := config.LoadAgent(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Name:          %s\n", a.Name)
			fmt.Printf("Description:   %s\n", a.Description)
			fmt.Printf("Default Model: %s\n", a.DefaultModel)
			fmt.Printf("Models:        %v\n", a.Models)
			fmt.Printf("Tools:         %v\n", a.Tools)
			fmt.Printf("Skills:        %v\n", a.Skills)
			fmt.Printf("MCPs:          %v\n", a.MCPs)
			return nil
		},
	})

	rootCmd.AddCommand(agentCmd)
}
