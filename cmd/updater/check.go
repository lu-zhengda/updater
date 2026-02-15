package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/tui"
	"github.com/lu-zhengda/updater/internal/updater"
	"github.com/spf13/cobra"
)

var flagVerbose bool
var flagCheckJSON bool

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check installed apps for available updates",
	RunE:  runCheck,
}

func init() {
	checkCmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "show release notes for available updates")
	checkCmd.Flags().BoolVar(&flagCheckJSON, "json", false, "output results as JSON")
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
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

	formulaApps, err := discoverBrewFormulae(ctx, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not discover brew formulae: %v\n", err)
	} else {
		apps = append(apps, formulaApps...)
	}

	apps, err = enrichApps(ctx, apps, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	if flagCheckJSON {
		entries := toCheckEntries(results, cfg)
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
		updateCount := 0
		for _, e := range entries {
			if e.Status == "update_available" || e.Status == "major_update" {
				updateCount++
			}
		}
		fmt.Fprintf(os.Stderr, "%d apps checked, %d updates available\n", len(entries), updateCount)
		cfg.LastChecked = time.Now()
		_ = cfg.Save(config.DefaultPath())
		return nil
	}

	printCheckResults(cmd, results, cfg)

	cfg.LastChecked = time.Now()
	_ = cfg.Save(config.DefaultPath())
	return nil
}

// Thin wrappers delegating to internal/updater for reuse by other binaries.

func discoverApps() ([]*app.App, error) {
	return updater.DiscoverApps()
}

func discoverBrewFormulae(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
	return updater.DiscoverBrewFormulae(ctx, runner)
}

func enrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) ([]*app.App, error) {
	return updater.EnrichApps(ctx, apps, cfg, runner)
}

func filterIgnored(apps []*app.App, cfg *config.Config) []*app.App {
	return updater.FilterIgnored(apps, cfg)
}

func buildCheckers(runner checker.CmdRunner, githubToken string) []checker.Checker {
	return updater.BuildCheckers(runner, githubToken)
}

func checkAll(ctx context.Context, apps []*app.App, checkers []checker.Checker, maxConcurrency int) []*checker.UpdateResult {
	return updater.CheckAll(ctx, apps, checkers, maxConcurrency)
}

// printCheckResults prints a table of check results to stdout.
func printCheckResults(cmd *cobra.Command, results []*checker.UpdateResult, cfg *config.Config) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCURRENT\tLATEST\tSOURCE\tSTATUS")

	updateCount := 0
	errCount := 0
	pinnedCount := 0
	for _, r := range results {
		src := cliSourceName(r.Source)
		if r.Error != nil {
			errCount++
			fmt.Fprintf(w, "%s\t%s\t-\t%s\tERROR: %v\n", r.App.Name, r.CurrentVersion, src, r.Error)
			continue
		}
		if r.HasUpdate && cfg.IsPinned(r.App.BundleID) {
			pinnedCount++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tPINNED\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		} else if r.HasUpdate && r.IsMajorUpdate {
			updateCount++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tMAJOR UPDATE\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		} else if r.HasUpdate {
			updateCount++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tUPDATE AVAILABLE\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tok\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		}
		// Show release notes inline when --verbose and an update is available.
		if flagVerbose && r.HasUpdate && r.ReleaseNotes != "" {
			notes := r.ReleaseNotes
			if r.Source == "sparkle" {
				notes = tui.StripHTML(notes)
			}
			for _, line := range strings.Split(notes, "\n") {
				fmt.Fprintf(w, "  \t \t \t \t%s\n", strings.TrimSpace(line))
			}
		}
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps checked, %d updates available", len(results), updateCount)
	if pinnedCount > 0 {
		fmt.Fprintf(os.Stderr, ", %d pinned", pinnedCount)
	}
	if errCount > 0 {
		fmt.Fprintf(os.Stderr, ", %d errors", errCount)
	}
	fmt.Fprintln(os.Stderr)
}

// cliSourceName returns a user-friendly display name for a source.
func cliSourceName(source string) string {
	switch source {
	case "mas":
		return "app store"
	case "brew", "brew-info":
		return "homebrew"
	case "formula":
		return "formula"
	case "electron", "setapp", "toolbox", "adobe":
		return source
	default:
		return source
	}
}

// checkEntry is the JSON representation of a check result.
type checkEntry struct {
	Name           string `json:"name"`
	BundleID       string `json:"bundle_id"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	DownloadURL    string `json:"download_url,omitempty"`
	ReleaseNotes   string `json:"release_notes,omitempty"`
	Error          string `json:"error,omitempty"`
}

// toCheckEntries converts check results to JSON-serializable entries.
func toCheckEntries(results []*checker.UpdateResult, cfg *config.Config) []checkEntry {
	var entries []checkEntry
	for _, r := range results {
		e := checkEntry{
			Name:           r.App.Name,
			BundleID:       r.App.BundleID,
			CurrentVersion: r.CurrentVersion,
			LatestVersion:  r.LatestVersion,
			Source:         r.Source,
			DownloadURL:    r.DownloadURL,
			ReleaseNotes:   r.ReleaseNotes,
		}
		switch {
		case r.Error != nil:
			e.Status = "error"
			e.Error = r.Error.Error()
		case r.HasUpdate && cfg.IsPinned(r.App.BundleID):
			e.Status = "pinned"
		case r.HasUpdate && r.IsMajorUpdate:
			e.Status = "major_update"
		case r.HasUpdate:
			e.Status = "update_available"
		default:
			e.Status = "ok"
		}
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []checkEntry{}
	}
	return entries
}
