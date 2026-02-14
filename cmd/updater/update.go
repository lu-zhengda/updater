package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/backup"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/luzhengda/updater/internal/installer"
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

	formulaApps, fErr := discoverBrewFormulae(ctx, runner)
	if fErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not discover brew formulae: %v\n", fErr)
	} else {
		apps = append(apps, formulaApps...)
	}

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

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

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
		printCheckResults(cmd, updatable, cfg)
		return nil
	}

	// Create backup manager and installer for direct updates.
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)
	inst := installer.New(runner, nil)

	// Execute updates. When using --all, skip pinned apps unless explicitly named.
	isExplicit := len(args) > 0 && !flagAll
	for _, r := range updatable {
		if !isExplicit && cfg.IsPinned(r.App.BundleID) {
			fmt.Fprintf(cmd.OutOrStdout(), "Skipping %s (pinned)\n", r.App.Name)
			continue
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updating %s (%s -> %s) via %s...\n",
			r.App.Name, r.CurrentVersion, r.LatestVersion, r.Source)

		if err := executeUpdate(ctx, r, runner, bm, inst); errors.Is(err, checker.ErrOpenedExternally) {
			// Not an error — just opened externally for the user to handle.
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		}
	}

	return nil
}

// executeUpdate performs the actual update for a single app.
// It backs up the current version before updating when possible.
func executeUpdate(ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner, bm *backup.Manager, inst *installer.Installer) error {
	// Backup before update (non-fatal on failure).
	if bm != nil && r.App.Path != "" {
		if err := bm.Backup(ctx, r.App.Name, r.App.BundleID, r.CurrentVersion, r.App.Path); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: backup failed: %v\n", err)
		} else {
			fmt.Printf("  Backed up %s %s\n", r.App.Name, r.CurrentVersion)
		}
	}

	switch r.Source {
	case "brew":
		if r.App.CaskName == "" {
			return fmt.Errorf("no cask name for %s", r.App.Name)
		}
		if !r.App.InstalledViaBrew {
			openForSelfUpdate(ctx, r.App, runner)
			return checker.ErrOpenedExternally
		}
		return brewUpgrade(ctx, r.App, runner)

	case "brew-info":
		if r.App.InstalledViaBrew && r.App.CaskName != "" {
			return brewUpgrade(ctx, r.App, runner)
		}
		openForSelfUpdate(ctx, r.App, runner)
		return checker.ErrOpenedExternally

	case "mas":
		if r.App.MASID != "" {
			output, err := runner.Run(ctx, "mas", "upgrade", r.App.MASID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  mas upgrade failed, opening App Store: %v\n", err)
				_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
				return checker.ErrOpenedExternally
			}
			fmt.Println(string(output))
			return nil
		}
		_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
		return checker.ErrOpenedExternally

	case "formula":
		if r.App.FormulaName == "" {
			return fmt.Errorf("no formula name for %s", r.App.Name)
		}
		output, err := runner.Run(ctx, "brew", "upgrade", r.App.FormulaName)
		if err != nil {
			return fmt.Errorf("failed to upgrade formula %s: %w", r.App.FormulaName, err)
		}
		fmt.Println(string(output))
		return nil

	case "system":
		_, err := runner.Run(ctx, "open", "x-apple.systempreferences:com.apple.Software-Update-Settings.extension")
		if err != nil {
			return fmt.Errorf("failed to open Software Update settings: %w", err)
		}
		return checker.ErrOpenedExternally

	case "sparkle", "github":
		if r.DownloadURL == "" {
			fmt.Println("  No download URL available. Check the app for in-app updates.")
			return nil
		}

		// Try direct install if installer is available.
		if inst != nil && r.App.Path != "" {
			wasRunning := quitAppIfRunning(ctx, r.App, runner)
			err := inst.Install(ctx, r.DownloadURL, r.App.Path, r.App.Name)
			if err == nil {
				if wasRunning {
					fmt.Printf("  Reopening %s...\n", r.App.Name)
					_, _ = runner.Run(ctx, "open", "-a", r.App.Path)
				}
				return nil
			}
			fmt.Fprintf(os.Stderr, "  direct install failed, falling back to browser: %v\n", err)
		}

		// Fallback: open in browser.
		_, err := runner.Run(ctx, "open", r.DownloadURL)
		if err != nil {
			return fmt.Errorf("failed to open download URL: %w", err)
		}
		return checker.ErrOpenedExternally

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
func openForSelfUpdate(ctx context.Context, a *app.App, runner checker.CmdRunner) {
	_, _ = runner.Run(ctx, "open", "-a", a.Path)
}
