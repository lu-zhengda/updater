package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var (
	flagCleanupDays   int
	flagCleanupDelete bool
	flagCleanupJSON   bool
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Find unused apps that can be removed",
	RunE:  runCleanup,
}

func init() {
	cleanupCmd.Flags().IntVar(&flagCleanupDays, "days", 90, "apps unused for this many days")
	cleanupCmd.Flags().BoolVar(&flagCleanupDelete, "delete", false, "move unused apps to Trash")
	cleanupCmd.Flags().BoolVar(&flagCleanupJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(cleanupCmd)
}

func runCleanup(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	useJSON := jsonOutputEnabled(flagCleanupJSON)

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	runner := &checker.RealCmdRunner{}
	cutoff := time.Now().AddDate(0, 0, -flagCleanupDays)

	var w *tabwriter.Writer
	if !useJSON {
		w = tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tLAST USED\tSIZE")
	}

	type cleanupEntry struct {
		Name        string `json:"name"`
		BundleID    string `json:"bundle_id"`
		Path        string `json:"path"`
		LastUsed    string `json:"last_used,omitempty"`
		Size        string `json:"size"`
		Deleted     bool   `json:"deleted,omitempty"`
		DeleteError string `json:"delete_error,omitempty"`
	}

	var unused []*app.App
	var entries []cleanupEntry
	for _, a := range apps {
		lastUsed, err := getLastUsedDate(ctx, runner, a.Path)
		if err != nil {
			continue // skip apps we can't query
		}
		if lastUsed.IsZero() || lastUsed.Before(cutoff) {
			size := getAppSize(ctx, runner, a.Path)
			lastStr := "never"
			if !lastUsed.IsZero() {
				lastStr = lastUsed.Format("2006-01-02")
			}
			if !useJSON {
				fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, lastStr, size)
			}
			entry := cleanupEntry{
				Name:     a.Name,
				BundleID: a.BundleID,
				Path:     a.Path,
				Size:     size,
			}
			if !lastUsed.IsZero() {
				entry.LastUsed = lastStr
			}
			entries = append(entries, entry)
			unused = append(unused, a)
		}
	}
	if !useJSON {
		w.Flush()
	}

	if !useJSON {
		fmt.Fprintf(os.Stderr, "\n%d apps unused for %d+ days\n", len(unused), flagCleanupDays)
	}

	deletedCount := 0
	deleteErrors := 0
	if flagCleanupDelete && len(unused) > 0 {
		for i, a := range unused {
			if !useJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "Moving %s to Trash...\n", a.Name)
			}
			if err := moveToTrash(ctx, runner, a); err != nil {
				if !useJSON {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
				}
				entries[i].DeleteError = err.Error()
				deleteErrors++
				continue
			}
			entries[i].Deleted = true
			deletedCount++
		}
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"days":          flagCleanupDays,
			"delete":        flagCleanupDelete,
			"unused_count":  len(unused),
			"deleted_count": deletedCount,
			"delete_errors": deleteErrors,
			"apps":          entries,
		})
	}

	return nil
}

// getLastUsedDate queries macOS Spotlight metadata for the last used date.
func getLastUsedDate(ctx context.Context, runner checker.CmdRunner, path string) (time.Time, error) {
	output, err := runner.Run(ctx, "mdls", "-name", "kMDItemLastUsedDate", path)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to query mdls: %w", err)
	}

	s := strings.TrimSpace(string(output))
	if strings.Contains(s, "(null)") {
		return time.Time{}, nil // never used
	}

	// Format: kMDItemLastUsedDate = 2024-01-15 10:30:00 +0000
	parts := strings.SplitN(s, "= ", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("unexpected mdls output: %s", s)
	}

	t, err := time.Parse("2006-01-02 15:04:05 +0000", strings.TrimSpace(parts[1]))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse date: %w", err)
	}
	return t, nil
}

// getAppSize returns a human-readable size string for the app bundle.
func getAppSize(ctx context.Context, runner checker.CmdRunner, path string) string {
	output, err := runner.Run(ctx, "du", "-sh", path)
	if err != nil {
		return "?"
	}
	// Output format: "123M\t/path/to/app"
	s := strings.TrimSpace(string(output))
	parts := strings.Fields(s)
	if len(parts) >= 1 {
		return parts[0]
	}
	return "?"
}

// moveToTrash moves an app to the Trash using Finder via osascript.
func moveToTrash(ctx context.Context, runner checker.CmdRunner, a *app.App) error {
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file "%s"`, a.Path)
	_, err := runner.Run(ctx, "osascript", "-e", script)
	if err != nil {
		return fmt.Errorf("failed to move %s to trash: %w", a.Name, err)
	}
	return nil
}
