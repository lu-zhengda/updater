package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/luzhengda/updater/internal/app"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Discover installed apps and their update sources",
	RunE:  runScan,
}

func init() {
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	dirs := []string{
		"/Applications",
		filepath.Join(home, "Applications"),
	}

	apps, err := app.Discover(dirs...)
	if err != nil {
		return fmt.Errorf("failed to discover apps: %w", err)
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
