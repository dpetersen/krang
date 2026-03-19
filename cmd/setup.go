package cmd

import (
	"fmt"

	"github.com/dpetersen/krang/internal/hooks"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install Claude Code hooks for krang",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := hooks.Install(); err != nil {
			return fmt.Errorf("installing hooks: %w", err)
		}
		fmt.Println("Krang hooks installed into ~/.claude/settings.json")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
