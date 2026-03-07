package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/metrics"
	"github.com/spf13/cobra"
)

var writeClipboard = clipboard.WriteAll
var appendMetric = metrics.Append
var metricPath = metrics.DefaultPath

type checkShareSummary struct {
	CheckedApps       int
	UpdatesAvailable  int
	MajorUpdates      int
	ErrorCount        int
	TopUpdatedAppName []string
	Message           string
}

func maybeShareCheckResults(cmd *cobra.Command, results []*checker.UpdateResult, cfg *config.Config, useJSON, shareEnabled bool) {
	if !shareEnabled {
		return
	}

	summary := buildCheckShareSummary(results, cfg)
	recordShareEvent("share_clicked", summary, "")

	if err := writeClipboard(summary.Message); err != nil {
		recordShareEvent("share_copy_failed", summary, err.Error())
		fmt.Fprintf(os.Stderr, "share: failed to copy summary to clipboard: %v\n", err)
		if useJSON {
			fmt.Fprintf(os.Stderr, "share summary: %s\n", summary.Message)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\nShare this update summary:\n%s\n", summary.Message)
		}
		return
	}

	recordShareEvent("share_copied", summary, "")
	if useJSON {
		fmt.Fprintln(os.Stderr, "share: summary copied to clipboard")
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), "\nShare summary copied to clipboard.")
}

func buildCheckShareSummary(results []*checker.UpdateResult, cfg *config.Config) checkShareSummary {
	summary := checkShareSummary{
		CheckedApps: len(results),
	}

	for _, r := range results {
		if r.Error != nil {
			summary.ErrorCount++
			continue
		}
		if !r.HasUpdate {
			continue
		}
		if cfg != nil && cfg.IsPinned(r.App.BundleID) {
			continue
		}

		summary.UpdatesAvailable++
		if r.IsMajorUpdate {
			summary.MajorUpdates++
		}
		if len(summary.TopUpdatedAppName) < 3 {
			summary.TopUpdatedAppName = append(summary.TopUpdatedAppName, r.App.Name)
		}
	}

	summary.Message = renderShareMessage(summary)
	return summary
}

func renderShareMessage(summary checkShareSummary) string {
	const cta = "Install: brew install --cask lu-zhengda/tap/updater"

	if summary.UpdatesAvailable == 0 {
		return fmt.Sprintf(
			"I just checked %d macOS apps with updater: everything is up to date. %s",
			summary.CheckedApps,
			cta,
		)
	}

	msg := fmt.Sprintf(
		"I just checked %d macOS apps with updater: %d update(s) available",
		summary.CheckedApps,
		summary.UpdatesAvailable,
	)
	if summary.MajorUpdates > 0 {
		msg += fmt.Sprintf(" (%d major)", summary.MajorUpdates)
	}
	if len(summary.TopUpdatedAppName) > 0 {
		msg += fmt.Sprintf(". Top updates: %s", strings.Join(summary.TopUpdatedAppName, ", "))
	}
	msg += ". " + cta
	return msg
}

func recordShareEvent(name string, summary checkShareSummary, failureReason string) {
	if err := appendMetric(metricPath(), metrics.Event{
		Name:             name,
		Timestamp:        time.Now(),
		CheckedApps:      summary.CheckedApps,
		UpdatesAvailable: summary.UpdatesAvailable,
		MajorUpdates:     summary.MajorUpdates,
		ErrorCount:       summary.ErrorCount,
		FailureReason:    failureReason,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to persist %s metric: %v\n", name, err)
	}
}
