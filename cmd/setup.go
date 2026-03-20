package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/dpetersen/krang/internal/config"
	"github.com/dpetersen/krang/internal/hooks"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install Claude Code hooks and configure krang",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := hooks.Install(); err != nil {
			return fmt.Errorf("installing hooks: %w", err)
		}
		fmt.Println("Krang hooks installed into ~/.claude/settings.json")

		configPath := config.Path()
		if _, err := config.Load(configPath); err == nil {
			cfg, _ := config.Load(configPath)
			fmt.Printf("Config already exists at %s\n", configPath)
			if cfg.SandboxCommand != "" {
				fmt.Printf("  sandbox_command: %s\n", cfg.SandboxCommand)
			} else {
				fmt.Println("  sandbox_command: (none — no sandboxing)")
			}
			return nil
		}

		fmt.Println()
		fmt.Println("Krang wraps Claude with a sandbox command for security.")
		fmt.Println("Enter the command that should prefix 'claude' when launching tasks.")
		fmt.Println("Examples: safehouse, sandvault run, safehouse --append-profile foo.sb")
		fmt.Println()
		fmt.Print("Sandbox command (empty for no sandbox): ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		sandboxCommand := strings.TrimSpace(input)

		if sandboxCommand == "" {
			fmt.Println("Warning: no sandbox configured. Claude will run without sandboxing.")
		}

		cfg := config.Config{SandboxCommand: sandboxCommand}
		if err := config.Write(configPath, cfg); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Printf("Config written to %s\n", configPath)
		if sandboxCommand != "" {
			fmt.Printf("  sandbox_command: %s\n", sandboxCommand)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
