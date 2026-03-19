package cmd

import (
	"fmt"

	"github.com/dpetersen/krang/internal/hooks"
	"github.com/spf13/cobra"
)

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Remove krang hooks from Claude Code settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := hooks.Uninstall(); err != nil {
			return fmt.Errorf("removing hooks: %w", err)
		}
		fmt.Println("Krang hooks removed from ~/.claude/settings.json")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(teardownCmd)
}
