package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var notifyCmd = &cobra.Command{
	Use:    "notify",
	Short:  "Check for updates and send a macOS notification",
	Hidden: true, // Called by launchd, not directly by users
	RunE:   runNotify,
}

func init() {
	rootCmd.AddCommand(notifyCmd)
}

func runNotify(cmd *cobra.Command, _ []string) error {
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

	// Count updatable apps.
	var updatable []string
	for _, r := range results {
		if r.HasUpdate && r.Error == nil && !cfg.IsPinned(r.App.BundleID) {
			updatable = append(updatable, r.App.Name)
		}
	}

	if len(updatable) == 0 {
		return nil // silent exit
	}

	body := buildNotificationBody(updatable)
	return sendNotification(ctx, runner, len(updatable), body)
}

// buildNotificationBody creates the notification body text, truncating at 200 chars.
func buildNotificationBody(names []string) string {
	body := strings.Join(names, ", ")
	if len(body) > 200 {
		body = body[:197] + "..."
	}
	return body
}

// sendNotification sends a macOS notification via osascript.
func sendNotification(ctx context.Context, runner checker.CmdRunner, count int, body string) error {
	title := fmt.Sprintf("%d app update(s) available", count)
	script := fmt.Sprintf(`display notification "%s" with title "%s"`,
		escapeAppleScript(body), escapeAppleScript(title))

	_, err := runner.Run(ctx, "osascript", "-e", script)
	if err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}
	return nil
}

// escapeAppleScript escapes double quotes and backslashes for AppleScript strings.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
