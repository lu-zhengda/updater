package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var (
	flagScheduleInterval int
	flagScheduleRemove   bool
	flagScheduleJSON     bool
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule periodic update checks with macOS notifications",
	RunE:  runSchedule,
}

func init() {
	scheduleCmd.Flags().IntVar(&flagScheduleInterval, "interval", 24, "check interval in hours")
	scheduleCmd.Flags().BoolVar(&flagScheduleRemove, "remove", false, "remove scheduled checks")
	scheduleCmd.Flags().BoolVar(&flagScheduleJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(scheduleCmd)
}

const plistLabel = "com.updater.check"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>notify</string>
		<string>--auto-update</string>
	</array>
	<key>StartInterval</key>
	<integer>{{.IntervalSeconds}}</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

type plistData struct {
	Label           string
	Binary          string
	IntervalSeconds int
	LogPath         string
}

// schedulePlistPath returns the path to the launchd plist file.
func schedulePlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

// scheduleExists reports whether the schedule plist is installed.
func scheduleExists() bool {
	p, err := schedulePlistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// installScheduleCore writes the launchd plist and loads it.
func installScheduleCore(ctx context.Context, runner checker.CmdRunner, hours int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	logPath := filepath.Join(home, "Library", "Logs", "updater-notify.log")

	data := plistData{
		Label:           plistLabel,
		Binary:          binary,
		IntervalSeconds: hours * 3600,
		LogPath:         logPath,
	}

	content, err := renderPlist(data)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents dir: %w", err)
	}

	// Unload existing if present.
	if _, err := os.Stat(plistPath); err == nil {
		_, _ = runner.Run(ctx, "launchctl", "unload", plistPath)
	}

	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write plist: %w", err)
	}

	if _, err := runner.Run(ctx, "launchctl", "load", plistPath); err != nil {
		return fmt.Errorf("failed to load plist: %w", err)
	}

	return nil
}

// removeScheduleCore unloads and deletes the launchd plist.
func removeScheduleCore(ctx context.Context, runner checker.CmdRunner) error {
	plistPath, err := schedulePlistPath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return nil
	}

	if _, err := runner.Run(ctx, "launchctl", "unload", plistPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: launchctl unload failed: %v\n", err)
	}

	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("failed to remove plist: %w", err)
	}

	return nil
}

func runSchedule(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	runner := newRunner()
	useJSON := jsonOutputEnabled(flagScheduleJSON)

	if flagScheduleRemove {
		if err := removeScheduleCore(ctx, runner); err != nil {
			return err
		}
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action": "remove",
				"status": "ok",
			})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Removed scheduled checks")
		return nil
	}

	if err := installScheduleCore(ctx, runner, flagScheduleInterval); err != nil {
		return err
	}

	// These can't fail here — installScheduleCore already validated them.
	plistPath, _ := schedulePlistPath()
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, "Library", "Logs", "updater-notify.log")

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"action":         "install",
			"status":         "ok",
			"interval_hours": flagScheduleInterval,
			"plist":          plistPath,
			"log":            logPath,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Scheduled update checks every %d hours\n", flagScheduleInterval)
	fmt.Fprintf(cmd.OutOrStdout(), "Plist: %s\n", plistPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Log: %s\n", logPath)
	return nil
}

// renderPlist renders the launchd plist from the template.
func renderPlist(data plistData) (string, error) {
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse plist template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render plist: %w", err)
	}
	return buf.String(), nil
}
