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

var flagMenubarRemove bool

var menubarCmd = &cobra.Command{
	Use:   "menubar",
	Short: "Install or remove the menu bar agent LaunchAgent",
	Long:  "Manages a launchd LaunchAgent that keeps the updater-menubar process running.",
	RunE:  runMenubar,
}

func init() {
	menubarCmd.Flags().BoolVar(&flagMenubarRemove, "remove", false, "remove the menu bar agent")
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
	</array>
	<key>KeepAlive</key>
	<true/>
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
	runner := &checker.RealCmdRunner{}

	if flagMenubarRemove {
		if err := removeMenubarAgent(ctx, runner); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Removed menu bar agent")
		return nil
	}

	if err := installMenubarAgent(ctx, runner); err != nil {
		return err
	}

	plistPath, _ := menubarPlistPath()
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

	// Find the updater-menubar binary. Look next to the current executable first,
	// then fall back to PATH.
	binary, err := findMenubarBinary()
	if err != nil {
		return err
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

// findMenubarBinary locates the updater-menubar binary.
// First checks alongside the current executable, then the PATH.
func findMenubarBinary() (string, error) {
	execPath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(execPath)
		candidate := filepath.Join(dir, "updater-menubar")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Fall back to PATH lookup.
	path, err := lookPath("updater-menubar")
	if err != nil {
		return "", fmt.Errorf("updater-menubar binary not found; build it with: go build ./cmd/updater-menubar/")
	}
	return path, nil
}

// lookPath wraps exec.LookPath for testability.
var lookPath = func(name string) (string, error) {
	// Search PATH manually to avoid importing os/exec in production
	// (os/exec is only needed for LookPath here).
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
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
