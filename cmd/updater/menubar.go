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
	flagMenubarRemove bool
	flagMenubarJSON   bool
)

var menubarCmd = &cobra.Command{
	Use:   "menubar",
	Short: "Install or remove the menu bar app LaunchAgent",
	Long: "Manages a launchd LaunchAgent that keeps the menu bar app running.\n" +
		"The agent runs `updater menubar run` from this same binary.",
	RunE: runMenubar,
}

var menubarRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the menu bar app in the foreground",
	Long:  "Runs the menu bar app. Blocks until Quit is chosen from the menu. Used by the LaunchAgent; also handy for debugging.",
	RunE: func(_ *cobra.Command, _ []string) error {
		return runMenubarApp()
	},
}

func init() {
	menubarCmd.Flags().BoolVar(&flagMenubarRemove, "remove", false, "remove the menu bar agent")
	menubarCmd.Flags().BoolVar(&flagMenubarJSON, "json", false, "output as JSON")
	menubarCmd.AddCommand(menubarRunCmd)
	rootCmd.AddCommand(menubarCmd)
}

const menubarPlistLabel = "com.updater.menubar"

const menubarPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>menubar</string>
		<string>run</string>
	</array>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

type menubarPlistData struct {
	Label   string
	Binary  string
	LogPath string
}

func runMenubar(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	runner := newRunner()
	useJSON := jsonOutputEnabled(flagMenubarJSON)

	if flagMenubarRemove {
		if err := removeMenubarAgent(ctx, runner); err != nil {
			return err
		}
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action": "remove",
				"status": "ok",
			})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Removed menu bar agent")
		return nil
	}

	if err := installMenubarAgent(ctx, runner); err != nil {
		return err
	}

	plistPath, _ := menubarPlistPath()
	if useJSON {
		return writeJSON(cmd, map[string]any{
			"action": "install",
			"status": "ok",
			"plist":  plistPath,
		})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Menu bar agent installed")
	fmt.Fprintf(cmd.OutOrStdout(), "Plist: %s\n", plistPath)
	return nil
}

func menubarPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", menubarPlistLabel+".plist"), nil
}

func installMenubarAgent(ctx context.Context, runner checker.CmdRunner) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", menubarPlistLabel+".plist")

	// The agent runs `<this binary> menubar run`.
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate own executable: %w", err)
	}

	logPath := filepath.Join(home, "Library", "Logs", "updater-menubar.log")

	data := menubarPlistData{
		Label:   menubarPlistLabel,
		Binary:  binary,
		LogPath: logPath,
	}

	content, err := renderMenubarPlist(data)
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

func removeMenubarAgent(ctx context.Context, runner checker.CmdRunner) error {
	plistPath, err := menubarPlistPath()
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

func renderMenubarPlist(data menubarPlistData) (string, error) {
	tmpl, err := template.New("menubar-plist").Parse(menubarPlistTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse plist template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to render plist: %w", err)
	}
	return buf.String(), nil
}
