package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var (
	flagAll  bool
	flagAuto bool
)

var updateCmd = &cobra.Command{
	Use:   "update [app-name]",
	Short: "Update apps with available updates",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&flagAll, "all", false, "update all apps with available updates")
	updateCmd.Flags().BoolVar(&flagAuto, "auto", false, "unattended mode (no prompts)")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	runner := &checker.RealCmdRunner{}
	apps, err = enrichApps(ctx, apps, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	// If a specific app name was given, verify it exists before checking.
	if len(args) > 0 && !flagAll {
		name := args[0]
		found := false
		for _, a := range apps {
			if strings.EqualFold(a.Name, name) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("app %q not found. Run 'updater scan' to see available apps", name)
		}
	}

	checkers := buildCheckers(runner)
	results := checkAll(ctx, apps, checkers)

	// Filter to apps with updates.
	var updatable []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate && r.Error == nil {
			updatable = append(updatable, r)
		}
	}

	// If a specific app name was given, filter to just that app.
	if len(args) > 0 && !flagAll {
		name := args[0]
		var matched []*checker.UpdateResult
		for _, r := range updatable {
			if strings.EqualFold(r.App.Name, name) {
				matched = append(matched, r)
			}
		}
		if len(matched) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date.\n", name)
			return nil
		}
		updatable = matched
	} else if len(updatable) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All apps are up to date.")
		return nil
	} else if !flagAll {
		// Without --all and without a specific app name, show what's available.
		fmt.Fprintf(cmd.OutOrStdout(), "%d updates available. Use --all to update all, or specify an app name.\n", len(updatable))
		printCheckResults(cmd, updatable)
		return nil
	}

	// Execute updates.
	for _, r := range updatable {
		fmt.Fprintf(cmd.OutOrStdout(), "Updating %s (%s -> %s) via %s...\n",
			r.App.Name, r.CurrentVersion, r.LatestVersion, r.Source)

		if err := executeUpdate(ctx, r, runner); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		}
	}

	return nil
}

// executeUpdate performs the actual update for a single app.
func executeUpdate(ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner) error {
	switch r.Source {
	case "brew":
		if r.App.CaskName == "" {
			return fmt.Errorf("no cask name for %s", r.App.Name)
		}
		if !r.App.InstalledViaBrew {
			return openForSelfUpdate(ctx, r.App, runner)
		}
		return brewUpgrade(ctx, r.App, runner)

	case "brew-info":
		if r.App.InstalledViaBrew && r.App.CaskName != "" {
			return brewUpgrade(ctx, r.App, runner)
		}
		return openForSelfUpdate(ctx, r.App, runner)

	case "mas":
		if r.App.MASID != "" {
			output, err := runner.Run(ctx, "mas", "upgrade", r.App.MASID)
			if err != nil {
				// Fallback: open Mac App Store updates page.
				fmt.Fprintf(os.Stderr, "  mas upgrade failed, opening App Store: %v\n", err)
				_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
				return nil
			}
			fmt.Println(string(output))
			return nil
		}
		// No MASID — open App Store updates page.
		fmt.Println("  Opening Mac App Store updates page...")
		_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
		return nil

	case "sparkle", "github":
		if r.DownloadURL != "" {
			fmt.Printf("  Download: %s\n", r.DownloadURL)
			_, err := runner.Run(ctx, "open", r.DownloadURL)
			if err != nil {
				return fmt.Errorf("failed to open download URL: %w", err)
			}
			return nil
		}
		fmt.Println("  No download URL available. Check the app for in-app updates.")
		return nil

	default:
		return fmt.Errorf("unsupported update source: %s", r.Source)
	}
}

// brewUpgrade quits the app if running, runs brew upgrade, and reopens it.
func brewUpgrade(ctx context.Context, a *app.App, runner checker.CmdRunner) error {
	wasRunning := quitAppIfRunning(ctx, a, runner)

	output, err := runner.Run(ctx, "brew", "upgrade", "--cask", a.CaskName)
	if err != nil {
		// Reopen if we quit it but upgrade failed.
		if wasRunning {
			_, _ = runner.Run(ctx, "open", "-a", a.Path)
		}
		return fmt.Errorf("failed to run brew upgrade: %w", err)
	}
	fmt.Println(string(output))

	if wasRunning {
		fmt.Printf("  Reopening %s...\n", a.Name)
		_, _ = runner.Run(ctx, "open", "-a", a.Path)
	}
	return nil
}

// quitAppIfRunning checks if an app is running and gracefully quits it.
// Returns true if the app was running and was quit.
func quitAppIfRunning(ctx context.Context, a *app.App, runner checker.CmdRunner) bool {
	// Check if the app process is running by matching its bundle path.
	if _, err := runner.Run(ctx, "pgrep", "-f", a.Path); err != nil {
		return false // not running
	}

	fmt.Printf("  Quitting %s...\n", a.Name)
	_, _ = runner.Run(ctx, "osascript", "-e",
		fmt.Sprintf(`tell application "%s" to quit`, a.Name))

	// Wait for the process to exit (poll up to 5 seconds).
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if _, err := runner.Run(ctx, "pgrep", "-f", a.Path); err != nil {
			return true // process exited
		}
	}

	// Still running after 5s — force kill as last resort.
	fmt.Printf("  Force quitting %s...\n", a.Name)
	_, _ = runner.Run(ctx, "pkill", "-f", a.Path)
	time.Sleep(1 * time.Second)
	return true
}

// openForSelfUpdate opens an app so it can self-update via its built-in updater.
func openForSelfUpdate(ctx context.Context, a *app.App, runner checker.CmdRunner) error {
	fmt.Printf("  Opening %s for in-app update...\n", a.Name)
	_, err := runner.Run(ctx, "open", "-a", a.Path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", a.Name, err)
	}
	return nil
}
