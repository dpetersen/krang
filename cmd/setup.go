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
	fmt.Println("Krang setup will create/update the following files:")
	fmt.Println()
	fmt.Println("  1. ~/.config/krang/hooks/relay.sh")
	fmt.Println("     A small bash script that forwards Claude Code hook events to krang's")
	fmt.Println("     local HTTP server. It reads the port from a state file and POSTs event")
	fmt.Println("     JSON to localhost. Only runs when KRANG_STATEFILE is set, which krang")
	fmt.Println("     does for sessions it launches — standalone Claude sessions are unaffected.")
	fmt.Println()
	fmt.Println("  2. ~/.claude/settings.json")
	fmt.Println("     Adds global command hook entries for 13 Claude Code lifecycle events")
	fmt.Println("     (SessionStart, Stop, PermissionRequest, tool use, etc.) that invoke the")
	fmt.Println("     relay script above. These are global hooks, so they fire for all Claude")
	fmt.Println("     sessions — but the relay script exits immediately when KRANG_STATEFILE is")
	fmt.Println("     not set, so Claude sessions run outside of krang are not affected.")
	fmt.Println("     Existing non-krang hooks are preserved. Re-running setup won't duplicate")
	fmt.Println("     entries.")
	fmt.Println()
	fmt.Println("  3. ~/.config/krang/config.yaml (first run only)")
	fmt.Println("     Krang configuration file for sandbox profiles, theme, and other settings.")
	fmt.Println("     Skipped if this file already exists.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Continue? [Y/n] ")
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Println("Setup cancelled.")
		return nil
	}

	fmt.Println()

	if err := hooks.Install(); err != nil {
		return fmt.Errorf("installing hooks: %w", err)
	}
	fmt.Println("  ✓ Relay script written to ~/.config/krang/hooks/relay.sh")
	fmt.Println("  ✓ Hook entries added to ~/.claude/settings.json")

	configPath := config.Path()
	if _, err := config.Load(configPath); err == nil {
		fmt.Printf("  ✓ Config already exists at %s (skipped)\n", configPath)
	} else {
		var cfg config.Config
		if err := config.Write(configPath, cfg); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
		fmt.Printf("  ✓ Config written to %s\n", configPath)
	}

	return nil
}
