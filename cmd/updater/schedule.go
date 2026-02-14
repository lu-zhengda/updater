package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/luzhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var (
	flagScheduleInterval int
	flagScheduleRemove   bool
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Schedule periodic update checks with macOS notifications",
	RunE:  runSchedule,
}

func init() {
	scheduleCmd.Flags().IntVar(&flagScheduleInterval, "interval", 24, "check interval in hours")
	scheduleCmd.Flags().BoolVar(&flagScheduleRemove, "remove", false, "remove scheduled checks")
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

func runSchedule(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	runner := &checker.RealCmdRunner{}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")

	if flagScheduleRemove {
		return removeSchedule(ctx, runner, plistPath, cmd)
	}

	return installSchedule(ctx, runner, plistPath, home, cmd)
}

func installSchedule(ctx context.Context, runner checker.CmdRunner, plistPath, home string, cmd *cobra.Command) error {
	// Find the updater binary path.
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	logPath := filepath.Join(home, "Library", "Logs", "updater-notify.log")

	data := plistData{
		Label:           plistLabel,
		Binary:          binary,
		IntervalSeconds: flagScheduleInterval * 3600,
		LogPath:         logPath,
	}

	// Render plist.
	content, err := renderPlist(data)
	if err != nil {
		return err
	}

	// Ensure LaunchAgents directory exists.
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

	fmt.Fprintf(cmd.OutOrStdout(), "Scheduled update checks every %d hours\n", flagScheduleInterval)
	fmt.Fprintf(cmd.OutOrStdout(), "Plist: %s\n", plistPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Log: %s\n", data.LogPath)
	return nil
}

func removeSchedule(ctx context.Context, runner checker.CmdRunner, plistPath string, cmd *cobra.Command) error {
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		fmt.Fprintln(cmd.OutOrStdout(), "No scheduled checks found")
		return nil
	}

	if _, err := runner.Run(ctx, "launchctl", "unload", plistPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: launchctl unload failed: %v\n", err)
	}

	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("failed to remove plist: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Removed scheduled checks")
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
