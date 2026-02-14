package main

import (
	"context"
	"fmt"
	"os"

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

	checkers := buildCheckers(runner)
	results := checkAll(ctx, apps, checkers)

	// Filter to apps with updates.
	var updatable []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate && r.Error == nil {
			updatable = append(updatable, r)
		}
	}

	if len(updatable) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All apps are up to date.")
		return nil
	}

	// If a specific app name was given, filter to just that app.
	if len(args) > 0 && !flagAll {
		name := args[0]
		var matched []*checker.UpdateResult
		for _, r := range updatable {
			if r.App.Name == name {
				matched = append(matched, r)
			}
		}
		if len(matched) == 0 {
			return fmt.Errorf("no update found for %q", name)
		}
		updatable = matched
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
		output, err := runner.Run(ctx, "brew", "upgrade", "--cask", r.App.CaskName)
		if err != nil {
			return fmt.Errorf("failed to run brew upgrade: %w", err)
		}
		fmt.Println(string(output))
		return nil

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
