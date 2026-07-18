package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:               "updater",
	Short:             "macOS app update manager",
	Long:              "Discover installed macOS apps, check for updates, and update them from multiple sources.",
	Version:           version,
	CompletionOptions: cobra.CompletionOptions{},
	// Launch TUI when invoked with no subcommand.
	RunE: runUI,
}

// runningFromAppBundle reports whether this process was launched from inside
// a macOS .app bundle (Updater.app) rather than as a plain CLI binary.
func runningFromAppBundle() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

func main() {
	// Launched as Updater.app (Finder, `open`, login item): run the menu bar
	// app. The terminal binary keeps its CLI/TUI behavior. LaunchServices may
	// pass a legacy -psn_* process serial argument; treat that as no args.
	if runningFromAppBundle() && (len(os.Args) <= 1 || strings.HasPrefix(os.Args[1], "-psn")) {
		if err := runMenubarApp(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
