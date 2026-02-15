package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var outdatedCmd = &cobra.Command{
	Use:   "outdated",
	Short: "List outdated apps as JSON",
	RunE:  runOutdated,
}

func init() {
	rootCmd.AddCommand(outdatedCmd)
}

// outdatedEntry is the JSON representation of an outdated app.
type outdatedEntry struct {
	Name           string `json:"name"`
	BundleID       string `json:"bundle_id"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Source         string `json:"source"`
	DownloadURL    string `json:"download_url,omitempty"`
}

func runOutdated(cmd *cobra.Command, _ []string) error {
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

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	entries := toOutdatedEntries(results)

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	fmt.Fprintf(os.Stderr, "%d outdated apps\n", len(entries))
	return nil
}

// toOutdatedEntries filters check results to only those with available updates
// and converts them to JSON-serializable entries.
func toOutdatedEntries(results []*checker.UpdateResult) []outdatedEntry {
	var entries []outdatedEntry
	for _, r := range results {
		if r.Error != nil || !r.HasUpdate {
			continue
		}
		entries = append(entries, outdatedEntry{
			Name:           r.App.Name,
			BundleID:       r.App.BundleID,
			CurrentVersion: r.CurrentVersion,
			LatestVersion:  r.LatestVersion,
			Source:         r.Source,
			DownloadURL:    r.DownloadURL,
		})
	}
	if entries == nil {
		entries = []outdatedEntry{}
	}
	return entries
}
