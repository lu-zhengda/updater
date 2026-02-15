package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var flagScanJSON bool

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Discover installed apps and their update sources",
	RunE:  runScan,
}

func init() {
	scanCmd.Flags().BoolVar(&flagScanJSON, "json", false, "output results as JSON")
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

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

	if flagScanJSON {
		entries := make([]scanEntry, len(apps))
		for i, a := range apps {
			entries[i] = scanEntry{
				Name:             a.Name,
				BundleID:         a.BundleID,
				Version:          a.Version,
				Source:           string(a.Source),
				FeedURL:          a.FeedURL,
				GitHubRepo:       a.GitHubRepo,
				CaskName:         a.CaskName,
				InstalledViaBrew: a.InstalledViaBrew,
			}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			return fmt.Errorf("failed to encode JSON: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%d apps discovered\n", len(apps))
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSOURCE\tBUNDLE ID")
	for _, a := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Version, a.Source, a.BundleID)
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps discovered\n", len(apps))
	return nil
}

// scanEntry is the JSON representation of a discovered app.
type scanEntry struct {
	Name             string `json:"name"`
	BundleID         string `json:"bundle_id"`
	Version          string `json:"version"`
	Source           string `json:"source"`
	FeedURL          string `json:"feed_url,omitempty"`
	GitHubRepo       string `json:"github_repo,omitempty"`
	CaskName         string `json:"cask_name,omitempty"`
	InstalledViaBrew bool   `json:"installed_via_brew"`
}
