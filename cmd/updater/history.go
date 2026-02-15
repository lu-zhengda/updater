package main

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/luzhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

var flagHistoryLimit int

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show update history",
	RunE:  runHistory,
}

func init() {
	historyCmd.Flags().IntVarP(&flagHistoryLimit, "limit", "n", 20, "maximum number of entries to show")
	rootCmd.AddCommand(historyCmd)
}

func runHistory(cmd *cobra.Command, args []string) error {
	entries, err := history.List(history.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to read history: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No update history yet.")
		return nil
	}

	// Sort newest first.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	// Apply limit.
	if flagHistoryLimit > 0 && len(entries) > flagHistoryLimit {
		entries = entries[:flagHistoryLimit]
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DATE\tAPP\tFROM\tTO\tSOURCE\tSTATUS")
	for _, e := range entries {
		status := "ok"
		if !e.Success {
			status = "FAILED"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Timestamp.Format("2006-01-02 15:04"),
			e.AppName,
			e.FromVersion,
			e.ToVersion,
			e.Source,
			status,
		)
	}
	w.Flush()

	return nil
}
