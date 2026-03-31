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
		if err := runSetup(); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println("Setup complete. Launching krang...")
		fmt.Println()
		return runTUI(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup() error {
	fmt.Println("Krang setup will:")
	fmt.Println("  1. Add hook entries to ~/.claude/settings.json. These hooks only activate")
	fmt.Println("     when KRANG_STATEFILE is set, which krang does automatically for Claude")
	fmt.Println("     sessions it launches — standalone Claude sessions are unaffected.")
	fmt.Println("  2. Write a relay script to ~/.config/krang/hooks/relay.sh")
	fmt.Println("  3. Configure a sandbox command that krang uses when launching Claude tasks")
	fmt.Println("     (does not affect Claude sessions started outside of krang)")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Continue? [Y/n] ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Println("Setup cancelled.")
		return nil
	}

	if err := hooks.Install(); err != nil {
		return fmt.Errorf("installing hooks: %w", err)
	}
	fmt.Println("Hooks installed into ~/.claude/settings.json")

	configPath := config.Path()
	if cfg, err := config.Load(configPath); err == nil {
		fmt.Printf("Config already exists at %s\n", configPath)
		if len(cfg.Sandboxes) > 0 {
			for name, profile := range cfg.Sandboxes {
				fmt.Printf("  sandbox %q: %s\n", name, profile.Command)
			}
			if cfg.DefaultSandbox != "" {
				fmt.Printf("  default_sandbox: %s\n", cfg.DefaultSandbox)
			}
		} else {
			fmt.Println("  sandboxes: (none — no sandboxing)")
		}
		return nil
	}

	return configureSandbox(reader, configPath)
}

func configureSandbox(reader *bufio.Reader, configPath string) error {
	fmt.Println()
	fmt.Println("Krang wraps Claude with a sandbox command for security.")
	fmt.Println("Enter the command that should prefix 'claude' when launching tasks.")
	fmt.Println("Examples: safehouse, sandvault run, safehouse --append-profile foo.sb")
	fmt.Println()
	fmt.Print("Sandbox command (empty for no sandbox): ")

	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}
	sandboxCommand := strings.TrimSpace(input)

	var cfg config.Config
	if sandboxCommand == "" {
		fmt.Println("Warning: no sandbox configured. Claude will run without sandboxing.")
	} else {
		cfg = config.Config{
			Sandboxes: map[string]config.SandboxProfile{
				"default": {Type: "command", Command: sandboxCommand},
			},
			DefaultSandbox: "default",
		}
	}
	if err := config.Write(configPath, cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("Config written to %s\n", configPath)
	if sandboxCommand != "" {
		fmt.Printf("  sandbox_command: %s\n", sandboxCommand)
	}

	return nil
}
