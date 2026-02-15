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
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Find unused apps that can be removed",
	RunE:  runCleanup,
}

func init() {
	cleanupCmd.Flags().IntVar(&flagCleanupDays, "days", 90, "apps unused for this many days")
	cleanupCmd.Flags().BoolVar(&flagCleanupDelete, "delete", false, "move unused apps to Trash")
	rootCmd.AddCommand(cleanupCmd)
}

func runCleanup(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	runner := &checker.RealCmdRunner{}
	cutoff := time.Now().AddDate(0, 0, -flagCleanupDays)

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tLAST USED\tSIZE")

	var unused []*app.App
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
			fmt.Fprintf(w, "%s\t%s\t%s\n", a.Name, lastStr, size)
			unused = append(unused, a)
		}
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps unused for %d+ days\n", len(unused), flagCleanupDays)

	if flagCleanupDelete && len(unused) > 0 {
		for _, a := range unused {
			fmt.Fprintf(cmd.OutOrStdout(), "Moving %s to Trash...\n", a.Name)
			if err := moveToTrash(ctx, runner, a); err != nil {
				fmt.Fprintf(os.Stderr, "  error: %v\n", err)
			}
		}
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
