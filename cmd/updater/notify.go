package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/spf13/cobra"
)

var (
	flagInteractive bool
	flagAutoUpdate  bool
	flagNotifyJSON  bool
)

var notifyCmd = &cobra.Command{
	Use:    "notify",
	Short:  "Check for updates and send a macOS notification",
	Hidden: true, // Called by launchd, not directly by users
	RunE:   runNotify,
}

func init() {
	notifyCmd.Flags().BoolVar(&flagInteractive, "interactive", false, "show dialog with action buttons")
	notifyCmd.Flags().BoolVar(&flagAutoUpdate, "auto-update", false, "automatically install safe updates after notification")
	notifyCmd.Flags().BoolVar(&flagNotifyJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(notifyCmd)
}

func runNotify(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	useJSON := jsonOutputEnabled(flagNotifyJSON)

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := newRunner()

	apps, err := discoverAll(ctx, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	// Collect updatable apps (including notify-only policy apps).
	var updatable []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate && r.Error == nil && !cfg.IsPinned(r.App.BundleID) {
			updatable = append(updatable, r)
		}
	}

	if len(updatable) == 0 {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"updates_available": 0,
				"notified":          false,
			})
		}
		return nil // silent exit
	}

	body := buildNotificationBody(updatable)
	subtitle := buildNotificationSubtitle(updatable)

	interactive := (flagInteractive || cfg.InteractiveNotifications) && !cfg.YOLOMode
	var notifyErr error
	if interactive {
		notifyErr = sendInteractiveNotification(ctx, runner, len(updatable), body)
	} else {
		notifyErr = sendNotification(ctx, runner, len(updatable), body, subtitle)
	}

	updated, failed := []string{}, []string{}
	autoUpdateEnabled := cfg.YOLOMode || (flagAutoUpdate && cfg.ScheduledAutoUpdate)
	if autoUpdateEnabled {
		updated, failed = autoUpdateAfterNotify(ctx, cfg, updatable)
	}
	if notifyErr != nil {
		return notifyErr
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"updates_available": len(updatable),
			"interactive":       interactive,
			"auto_update":       autoUpdateEnabled,
			"updated":           updated,
			"failed":            failed,
			"notified":          true,
		})
	}
	return nil
}

// autoUpdateAfterNotify applies available unattended updates for the menu bar
// and scheduled checks. YOLO Mode also includes major versions. Successful
// installs clear HasUpdate so callers display and cache the remaining updates.
func autoUpdateAfterNotify(ctx context.Context, cfg *config.Config, updatable []*checker.UpdateResult) ([]string, []string) {
	activity, err := updateActivityLock(ctx, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot start automatic updates: %v\n", err)
		return nil, nil
	}
	defer activity.Close()
	runner := newRunner()
	bm := backupManagerForConfig(cfg, runner)
	inst := installer.New(runner, nil)

	autoSkipSources := map[string]bool{
		"system": true, "setapp": true, "toolbox": true, "adobe": true,
	}

	var updated, failed []string
	for _, r := range updatable {
		if ctx.Err() != nil {
			break
		}
		if !r.HasUpdate || r.Error != nil || cfg.IsIgnored(r.App.BundleID) || cfg.IsPinned(r.App.BundleID) ||
			(r.IsMajorUpdate && !cfg.YOLOMode) || r.NativeUpgrade || autoSkipSources[r.Source] {
			continue
		}
		policy := cfg.Policy(r.App.BundleID)
		if policy == config.PolicyManual || policy == config.PolicyNotifyOnly {
			continue
		}
		// Only attempt actions that can install an update without user input.
		action := describeAction(r)
		if strings.HasPrefix(action, "open ") || action == "unsupported source" {
			continue
		}
		updateErr, rolledBack := executeUpdate(ctx, r, runner, bm, inst)
		if errors.Is(updateErr, checker.ErrUpdateScheduled) {
			continue // The helper records and announces the actual result.
		}
		_ = history.Append(history.DefaultPath(), history.Entry{
			AppName:     r.App.Name,
			BundleID:    r.App.BundleID,
			FromVersion: r.CurrentVersion,
			ToVersion:   r.LatestVersion,
			Source:      r.Source,
			Timestamp:   time.Now(),
			Success:     updateErr == nil,
			RolledBack:  rolledBack,
		})
		if updateErr == nil {
			updated = append(updated, r.App.Name)
			r.HasUpdate = false
			r.CurrentVersion = r.LatestVersion
		} else {
			failed = append(failed, r.App.Name)
			if !errors.Is(updateErr, checker.ErrOpenedExternally) {
				fmt.Fprintf(os.Stderr, "auto-update %s failed: %v\n", r.App.Name, updateErr)
			}
		}
	}

	if len(updated)+len(failed) > 0 {
		var parts []string
		if len(updated) > 0 {
			parts = append(parts, "Updated: "+strings.Join(updated, ", "))
		}
		if len(failed) > 0 {
			parts = append(parts, "Needs attention: "+strings.Join(failed, ", "))
		}
		script := fmt.Sprintf(`display notification "%s" with title "Updater"`, escapeAppleScript(strings.Join(parts, ". ")))
		_, _ = runner.Run(ctx, "osascript", "-e", script)
	}
	return updated, failed
}

// buildNotificationBody creates the notification body text showing version transitions,
// truncating at 200 chars.
func buildNotificationBody(results []*checker.UpdateResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.NativeUpgrade {
			parts = append(parts, r.App.Name+" (ARM version available)")
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s\u2192%s)", r.App.Name, r.CurrentVersion, r.LatestVersion))
		}
	}
	body := strings.Join(parts, ", ")
	if len(body) > 200 {
		body = body[:197] + "..."
	}
	return body
}

// buildNotificationSubtitle returns a subtitle highlighting major updates.
// Returns empty string if no major updates are present.
func buildNotificationSubtitle(results []*checker.UpdateResult) string {
	var majorCount int
	for _, r := range results {
		if r.IsMajorUpdate {
			majorCount++
		}
	}
	switch majorCount {
	case 0:
		return ""
	case 1:
		return "1 major update"
	default:
		return fmt.Sprintf("%d major updates", majorCount)
	}
}

// sendNotification sends a macOS notification via osascript.
// subtitle is optional; if non-empty it is included in the notification.
func sendNotification(ctx context.Context, runner checker.CmdRunner, count int, body, subtitle string) error {
	title := fmt.Sprintf("%d app update(s) available", count)
	script := fmt.Sprintf(`display notification "%s" with title "%s"`,
		escapeAppleScript(body), escapeAppleScript(title))
	if subtitle != "" {
		script = fmt.Sprintf(`display notification "%s" with title "%s" subtitle "%s"`,
			escapeAppleScript(body), escapeAppleScript(title), escapeAppleScript(subtitle))
	}

	_, err := runner.Run(ctx, "osascript", "-e", script)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	return nil
}

// sendInteractiveNotification shows a dialog with action buttons via osascript.
func sendInteractiveNotification(ctx context.Context, runner checker.CmdRunner, count int, body string) error {
	title := fmt.Sprintf("%d update(s) available", count)

	binaryPath, err := os.Executable()
	if err != nil {
		binaryPath = "updater"
	}

	script := fmt.Sprintf(
		`set answer to display dialog "%s" with title "%s" buttons {"Dismiss", "Open Updater"} default button "Dismiss" giving up after 30
if button returned of answer is "Open Updater" then
    do shell script "%s ui &>/dev/null &"
end if`,
		escapeAppleScript(body),
		escapeAppleScript(title),
		escapeAppleScript(binaryPath),
	)

	_, err = runner.Run(ctx, "osascript", "-e", script)
	if err != nil {
		return fmt.Errorf("failed to show interactive notification: %w", err)
	}
	return nil
}

// escapeAppleScript escapes double quotes and backslashes for AppleScript strings.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
