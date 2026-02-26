package cmd

import (
	"fmt"

	"github.com/gal-cli/gal-cli/internal/session"
	"github.com/spf13/cobra"
)

func init() {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "Manage sessions",
	}

	sessionCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all saved sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			session.Cleanup()
			sessions, err := session.List()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("No sessions.")
				return nil
			}
			for _, s := range sessions {
				fmt.Printf("  %-8s  %-12s  %-30s  %s  (%d msgs)\n",
					s.ID, s.Agent, s.Model,
					s.UpdatedAt.Format("2006-01-02 15:04"),
					len(s.Messages))
			}
			return nil
		},
	})

	sessionCmd.AddCommand(&cobra.Command{
		Use:   "show [id]",
		Short: "Show session metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := session.Load(args[0])
			if err != nil {
				return fmt.Errorf("session not found: %s", args[0])
			}
			fmt.Printf("ID:         %s\n", s.ID)
			fmt.Printf("Agent:      %s\n", s.Agent)
			fmt.Printf("Model:      %s\n", s.Model)
			fmt.Printf("Created:    %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("Updated:    %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("Messages:   %d\n", len(s.Messages))
			return nil
		},
	})

	sessionCmd.AddCommand(&cobra.Command{
		Use:   "rm [id]",
		Short: "Delete a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := session.Remove(args[0]); err != nil {
				return fmt.Errorf("session not found: %s", args[0])
			}
			fmt.Printf("Deleted session %s\n", args[0])
			return nil
		},
	})

	rootCmd.AddCommand(sessionCmd)
}
