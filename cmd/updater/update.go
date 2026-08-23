package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/spf13/cobra"
)

var (
	flagAll        bool
	flagAuto       bool
	flagDryRun     bool
	flagDryRunJSON bool
	flagBundleID   string
)

var updateCmd = &cobra.Command{
	Use:   "update [app-name]",
	Short: "Update apps with available updates",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&flagAll, "all", false, "update all apps with available updates")
	updateCmd.Flags().BoolVar(&flagAuto, "auto", false, "unattended mode (no prompts)")
	updateCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "show what would be updated without making changes")
	updateCmd.Flags().BoolVar(&flagDryRunJSON, "json", false, "output dry run results as JSON (requires --dry-run)")
	updateCmd.Flags().StringVar(&flagBundleID, "bundle-id", "", "select the app to update by exact bundle ID instead of name")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if flagDryRunJSON && !flagDryRun {
		return fmt.Errorf("--json requires --dry-run flag")
	}

	ctx := cmd.Context()

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

	// If a specific app selector was given, resolve it before checking.
	// --bundle-id is an exact, unambiguous selector (names can collide);
	// it takes precedence over a name argument.
	var targetApp *app.App
	if flagBundleID != "" && !flagAll {
		for _, a := range apps {
			if a.BundleID == flagBundleID {
				targetApp = a
				break
			}
		}
		if targetApp == nil {
			return fmt.Errorf("no app with bundle ID %q found. Run 'updater scan' to see available apps", flagBundleID)
		}
	} else if len(args) > 0 && !flagAll {
		query := joinAppNameArgs(args)
		targetApp, err = resolveAppSelection(apps, query)
		if err != nil {
			return fmt.Errorf("%w. Run 'updater scan' to see available apps", err)
		}
	}

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	// Filter to apps with updates.
	var updatable []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate && r.Error == nil {
			updatable = append(updatable, r)
		}
	}

	// Auto mode: filter to safe, unattended updates only.
	if flagAuto {
		autoSkipSources := map[string]bool{
			"system": true, "setapp": true, "toolbox": true, "adobe": true,
		}
		var autoUpdatable []*checker.UpdateResult
		for _, r := range updatable {
			if cfg.IsPinned(r.App.BundleID) {
				continue
			}
			if r.IsMajorUpdate {
				continue
			}
			if autoSkipSources[r.Source] {
				continue
			}
			policy := cfg.Policy(r.App.BundleID)
			if policy == config.PolicyManual || policy == config.PolicyNotifyOnly {
				continue
			}
			autoUpdatable = append(autoUpdatable, r)
		}
		updatable = autoUpdatable
	}

	// If a specific app selector was given, filter to just that app.
	if targetApp != nil && !flagAll && !flagAuto {
		var matched []*checker.UpdateResult
		for _, r := range updatable {
			if targetApp.BundleID != "" && r.App.BundleID == targetApp.BundleID {
				matched = append(matched, r)
				continue
			}
			if strings.EqualFold(r.App.Name, targetApp.Name) {
				matched = append(matched, r)
			}
		}
		if len(matched) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s is up to date.\n", targetApp.Name)
			return nil
		}
		updatable = matched
	} else if len(updatable) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "All apps are up to date.")
		return nil
	} else if !flagAll && !flagAuto {
		// Without --all/--auto and without a specific app name, show what's available.
		fmt.Fprintf(cmd.OutOrStdout(), "%d updates available. Use --all to update all, or specify an app name.\n", len(updatable))
		printCheckResults(cmd, updatable, cfg)
		return nil
	}

	// Create backup manager and installer for direct updates.
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)
	inst := installer.New(runner, nil)

	// Execute updates. When using --all, skip pinned apps unless explicitly named.
	isExplicit := targetApp != nil && !flagAll

	if flagDryRun {
		return printDryRun(cmd, updatable, isExplicit, cfg, jsonOutputEnabled(flagDryRunJSON))
	}

	// Split updates into sequential and parallel groups.
	sequentialSources := map[string]bool{
		"brew": true, "brew-info": true, "mas": true, "formula": true,
	}
	instantSources := map[string]bool{
		"system": true, "setapp": true, "toolbox": true, "adobe": true,
	}

	var sequential, parallel, instant []*checker.UpdateResult
	for _, r := range updatable {
		if !isExplicit && cfg.IsPinned(r.App.BundleID) {
			fmt.Fprintf(cmd.OutOrStdout(), "Skipping %s (pinned)\n", r.App.Name)
			continue
		}
		if !isExplicit && cfg.Policy(r.App.BundleID) == config.PolicyNotifyOnly {
			fmt.Fprintf(cmd.OutOrStdout(), "Skipping %s (notify-only)\n", r.App.Name)
			continue
		}
		switch {
		// Direct cask installs never invoke brew, so they don't need the
		// sequential phase that guards brew's shared locks.
		case sequentialSources[r.Source] && !caskDirectInstall(r):
			sequential = append(sequential, r)
		case instantSources[r.Source]:
			instant = append(instant, r)
		default:
			parallel = append(parallel, r)
		}
	}

	// Phase 1: Sequential updates (brew, mas, formula — shared locks).
	for _, r := range sequential {
		performUpdate(cmd, ctx, r, runner, bm, inst)
	}

	// Phase 2: Parallel updates (sparkle, github, electron — independent).
	if len(parallel) > 0 {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 3) // limit concurrent installs
		for _, r := range parallel {
			wg.Add(1)
			sem <- struct{}{}
			go func(r *checker.UpdateResult) {
				defer wg.Done()
				defer func() { <-sem }()
				performUpdate(cmd, ctx, r, runner, bm, inst)
			}(r)
		}
		wg.Wait()
	}

	// Phase 3: Instant actions (system, setapp, toolbox, adobe — just open).
	for _, r := range instant {
		performUpdate(cmd, ctx, r, runner, bm, inst)
	}

	return nil
}

// executeUpdate performs the actual update for a single app.
// It backs up the current version before updating when possible.
// Returns the error (if any) and whether a rollback was performed.
func executeUpdate(ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner, bm *backup.Manager, inst *installer.Installer) (error, bool) {
	// Backup before update (non-fatal on failure).
	if bm != nil && r.App.Path != "" {
		if err := bm.Backup(ctx, r.App.Name, r.App.BundleID, r.CurrentVersion, r.App.Path); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: backup failed: %v\n", err)
		} else {
			fmt.Printf("  Backed up %s %s\n", r.App.Name, r.CurrentVersion)
		}
	}

	switch r.Source {
	case "brew":
		if r.App.CaskName == "" {
			return fmt.Errorf("no cask name for %s", r.App.Name), false
		}
		if !r.App.InstalledViaBrew {
			openForSelfUpdate(ctx, r.App, runner)
			return checker.ErrOpenedExternally, false
		}
		return brewUpgrade(ctx, r.App, runner), false

	case "brew-info":
		if r.App.InstalledViaBrew && r.App.CaskName != "" {
			return brewUpgrade(ctx, r.App, runner), false
		}
		// Not brew-managed: install the cask's artifact directly instead of
		// relying on the app's own updater.
		if r.DownloadURL != "" && inst != nil && r.App.Path != "" {
			err, rolledBack, done := tryDirectInstall(ctx, r, runner, bm, inst, "opening app for self-update")
			if done {
				return err, rolledBack
			}
		}
		openForSelfUpdate(ctx, r.App, runner)
		return checker.ErrOpenedExternally, false

	case "mas":
		if r.App.MASID != "" {
			output, err := runner.Run(ctx, "mas", "upgrade", r.App.MASID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  mas upgrade failed, opening App Store: %v\n", err)
				_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
				return checker.ErrOpenedExternally, false
			}
			fmt.Println(string(output))
			return nil, false
		}
		_, _ = runner.Run(ctx, "open", "macappstore://showUpdatesPage")
		return checker.ErrOpenedExternally, false

	case "formula":
		if r.App.FormulaName == "" {
			return fmt.Errorf("no formula name for %s", r.App.Name), false
		}
		output, err := runner.Run(ctx, "brew", "upgrade", r.App.FormulaName)
		if err != nil {
			return fmt.Errorf("failed to upgrade formula %s: %w", r.App.FormulaName, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "npm":
		if r.App.NpmPackage == "" {
			return fmt.Errorf("no npm package name for %s", r.App.Name), false
		}
		output, err := runner.Run(ctx, "npm", "install", "-g", r.App.NpmPackage+"@latest")
		if err != nil {
			return fmt.Errorf("failed to update npm package %s: %w", r.App.NpmPackage, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "pnpm":
		if r.App.PnpmPackage == "" {
			return fmt.Errorf("no pnpm package name for %s", r.App.Name), false
		}
		output, err := runner.Run(ctx, "pnpm", "update", "-g", "--latest", r.App.PnpmPackage)
		if err != nil {
			return fmt.Errorf("failed to update pnpm package %s: %w", r.App.PnpmPackage, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "pipx":
		if r.App.PipxEnvironment == "" {
			return fmt.Errorf("no pipx environment name for %s", r.App.Name), false
		}
		if r.App.PipxPinned || r.App.PipxNonRegistry {
			return fmt.Errorf("pipx environment %s is pinned or not a plain PyPI install", r.App.PipxEnvironment), false
		}
		output, err := runner.Run(ctx, "pipx", "upgrade", r.App.PipxEnvironment)
		if err != nil {
			return fmt.Errorf("failed to update pipx environment %s: %w", r.App.PipxEnvironment, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "uv":
		if r.App.UvTool == "" {
			return fmt.Errorf("no uv tool name for %s", r.App.Name), false
		}
		// `uv tool upgrade` honors the version constraint from install time.
		// A receipt pinned to an exact version would silently no-op, so
		// reinstall at the target version instead.
		uvArgs := []string{"tool", "upgrade", r.App.UvTool}
		if r.App.UvPinned && r.LatestVersion != "" {
			uvArgs = []string{"tool", "install", fmt.Sprintf("%s==%s", r.App.UvTool, r.LatestVersion)}
		}
		output, err := runner.Run(ctx, "uv", uvArgs...)
		if err != nil {
			return fmt.Errorf("failed to upgrade uv tool %s: %w", r.App.UvTool, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "cargo":
		if r.App.CargoCrate == "" {
			return fmt.Errorf("no cargo crate name for %s", r.App.Name), false
		}
		// `cargo install <crate>` upgrades in place when a newer version exists.
		output, err := runner.Run(ctx, "cargo", "install", r.App.CargoCrate)
		if err != nil {
			return fmt.Errorf("failed to upgrade cargo crate %s: %w", r.App.CargoCrate, err), false
		}
		fmt.Println(string(output))
		return nil, false

	case "system":
		_, err := runner.Run(ctx, "open", "x-apple.systempreferences:com.apple.Software-Update-Settings.extension")
		if err != nil {
			return fmt.Errorf("failed to open Software Update settings: %w", err), false
		}
		return checker.ErrOpenedExternally, false

	case "sparkle", "github":
		if r.DownloadURL == "" {
			fmt.Println("  No download URL available. Check the app for in-app updates.")
			return nil, false
		}

		// Try direct install if installer is available.
		if inst != nil && r.App.Path != "" {
			err, rolledBack, done := tryDirectInstall(ctx, r, runner, bm, inst, "falling back to browser")
			if done {
				return err, rolledBack
			}
		}

		// Fallback: open in browser.
		_, err := runner.Run(ctx, "open", r.DownloadURL)
		if err != nil {
			return fmt.Errorf("failed to open download URL: %w", err), false
		}
		return checker.ErrOpenedExternally, false

	case "electron":
		// Try direct install from ElectronUpdateURL if available.
		if r.DownloadURL != "" && inst != nil && r.App.Path != "" {
			err, rolledBack, done := tryDirectInstall(ctx, r, runner, bm, inst, "opening app for self-update")
			if done {
				return err, rolledBack
			}
		}
		// Fallback: open app for self-update.
		_, _ = runner.Run(ctx, "open", "-a", r.App.Path)
		return checker.ErrOpenedExternally, false

	case "setapp":
		_, _ = runner.Run(ctx, "open", "-a", "/Applications/Setapp.app")
		return checker.ErrOpenedExternally, false

	case "toolbox":
		_, _ = runner.Run(ctx, "open", "-a", "JetBrains Toolbox")
		return checker.ErrOpenedExternally, false

	case "adobe":
		_, _ = runner.Run(ctx, "open", "-a", "/Applications/Adobe Creative Cloud/Adobe Creative Cloud.app")
		return checker.ErrOpenedExternally, false

	default:
		return fmt.Errorf("unsupported update source: %s", r.Source), false
	}
}

// performUpdate prints the update status, executes the update, and records the result in history.
func performUpdate(cmd *cobra.Command, ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner, bm *backup.Manager, inst *installer.Installer) {
	fmt.Fprintf(cmd.OutOrStdout(), "Updating %s (%s -> %s) via %s...\n",
		r.App.Name, r.CurrentVersion, r.LatestVersion, canonicalSourceLabel(r.Source, r.SourceOverrideActive))

	updateErr, rolledBack := executeUpdate(ctx, r, runner, bm, inst)
	if errors.Is(updateErr, checker.ErrOpenedExternally) {
		// Not an error.
	} else if updateErr != nil {
		fmt.Fprintf(os.Stderr, "  error: %v\n", updateErr)
	}

	_ = history.Append(history.DefaultPath(), history.Entry{
		AppName:     r.App.Name,
		BundleID:    r.App.BundleID,
		FromVersion: r.CurrentVersion,
		ToVersion:   r.LatestVersion,
		Source:      r.Source,
		Timestamp:   time.Now(),
		Success:     updateErr == nil || errors.Is(updateErr, checker.ErrOpenedExternally),
		RolledBack:  rolledBack,
	})
}

// tryDirectInstall downloads r.DownloadURL and installs it over r.App.Path,
// quitting the app first and reopening it afterwards. done reports whether the
// update finished (success, or a failure that was rolled back from backup);
// when done is false the caller should fall back to its source-specific action.
func tryDirectInstall(ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner, bm *backup.Manager, inst *installer.Installer, fallbackNote string) (err error, rolledBack, done bool) {
	wasRunning := quitAppIfRunning(ctx, r.App, runner)
	err = inst.Install(ctx, r.DownloadURL, r.App.Path, r.App.Name, r.DownloadDigest)
	if err == nil {
		if wasRunning {
			fmt.Printf("  Reopening %s...\n", r.App.Name)
			_, _ = runner.Run(ctx, "open", "-a", r.App.Path)
		}
		return nil, false, true
	}
	mayRequireRollback := installer.MayRequireRollback(err)
	if mayRequireRollback {
		rolledBack = rollbackAfterFailedInstall(ctx, bm, r.App.Name)
	}
	if wasRunning && (!mayRequireRollback || rolledBack) {
		_, _ = runner.Run(ctx, "open", "-a", r.App.Path)
	}
	fmt.Fprintf(os.Stderr, "  direct install failed, %s: %v\n", fallbackNote, err)
	return err, rolledBack, rolledBack
}

// rollbackAfterFailedInstall attempts to restore an app from backup after a failed install.
func rollbackAfterFailedInstall(ctx context.Context, bm *backup.Manager, appName string) bool {
	if bm == nil || !bm.HasBackup(appName) {
		return false
	}
	fmt.Fprintf(os.Stderr, "  Attempting rollback for %s...\n", appName)
	if err := bm.Restore(ctx, appName); err != nil {
		fmt.Fprintf(os.Stderr, "  Rollback failed: %v\n", err)
		return false
	}
	fmt.Printf("  Rolled back %s to previous version\n", appName)
	return true
}

// brewUpgrade quits the app if running, runs brew upgrade, and reopens it.
func brewUpgrade(ctx context.Context, a *app.App, runner checker.CmdRunner) error {
	wasRunning := quitAppIfRunning(ctx, a, runner)

	output, err := runner.Run(ctx, "brew", "upgrade", "--cask", a.CaskName)
	if err != nil {
		// Reopen if we quit it but upgrade failed.
		if wasRunning {
			_, _ = runner.Run(ctx, "open", "-a", a.Path)
		}
		return fmt.Errorf("failed to run brew upgrade: %w", err)
	}
	fmt.Println(string(output))

	if wasRunning {
		fmt.Printf("  Reopening %s...\n", a.Name)
		_, _ = runner.Run(ctx, "open", "-a", a.Path)
	}
	return nil
}

// quitAppIfRunning checks if an app is running and gracefully quits it.
// Returns true if the app was running and was quit.
func quitAppIfRunning(ctx context.Context, a *app.App, runner checker.CmdRunner) bool {
	// Check if the app process is running by matching its bundle path.
	if _, err := runner.Run(ctx, "pgrep", "-f", a.Path); err != nil {
		return false // not running
	}

	fmt.Printf("  Quitting %s...\n", a.Name)
	_, _ = runner.Run(ctx, "osascript", "-e",
		fmt.Sprintf(`tell application "%s" to quit`, a.Name))

	// Wait for the process to exit (poll up to 5 seconds).
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if _, err := runner.Run(ctx, "pgrep", "-f", a.Path); err != nil {
			return true // process exited
		}
	}

	// Still running after 5s — force kill as last resort.
	fmt.Printf("  Force quitting %s...\n", a.Name)
	_, _ = runner.Run(ctx, "pkill", "-f", a.Path)
	time.Sleep(1 * time.Second)
	return true
}

// caskDirectInstall reports whether a cask-sourced update will be applied by
// downloading the cask's artifact directly rather than via brew or self-update.
func caskDirectInstall(r *checker.UpdateResult) bool {
	return r.Source == "brew-info" && !r.App.InstalledViaBrew &&
		r.DownloadURL != "" && r.App.Path != ""
}

// openForSelfUpdate opens an app so it can self-update via its built-in updater.
func openForSelfUpdate(ctx context.Context, a *app.App, runner checker.CmdRunner) {
	_, _ = runner.Run(ctx, "open", "-a", a.Path)
}

// describeAction returns a human-readable action string for each update source.
func describeAction(r *checker.UpdateResult) string {
	switch r.Source {
	case "brew", "brew-info":
		if r.App.InstalledViaBrew && r.App.CaskName != "" {
			return fmt.Sprintf("brew upgrade --cask %s", r.App.CaskName)
		}
		if caskDirectInstall(r) {
			return "direct install"
		}
		return "open app for self-update"
	case "mas":
		if r.App.MASID != "" {
			return fmt.Sprintf("mas upgrade %s", r.App.MASID)
		}
		return "open App Store"
	case "formula":
		return fmt.Sprintf("brew upgrade %s", r.App.FormulaName)
	case "npm":
		return fmt.Sprintf("npm install -g %s@latest", r.App.NpmPackage)
	case "pnpm":
		return fmt.Sprintf("pnpm update -g --latest %s", r.App.PnpmPackage)
	case "pipx":
		return fmt.Sprintf("pipx upgrade %s", r.App.PipxEnvironment)
	case "uv":
		if r.App.UvPinned && r.LatestVersion != "" {
			return fmt.Sprintf("uv tool install %s==%s (pinned install)", r.App.UvTool, r.LatestVersion)
		}
		return fmt.Sprintf("uv tool upgrade %s", r.App.UvTool)
	case "cargo":
		return fmt.Sprintf("cargo install %s", r.App.CargoCrate)
	case "system":
		return "open Software Update"
	case "sparkle", "github":
		if r.DownloadURL != "" && r.App.Path != "" {
			return "direct install"
		}
		return "open download URL"
	case "electron":
		if r.DownloadURL != "" && r.App.Path != "" {
			return "direct install"
		}
		return "open app for self-update"
	case "setapp":
		return "open Setapp"
	case "toolbox":
		return "open JetBrains Toolbox"
	case "adobe":
		return "open Adobe Creative Cloud"
	default:
		return "unsupported source"
	}
}

// printDryRun displays what updates would be applied without making changes.
func printDryRun(cmd *cobra.Command, updatable []*checker.UpdateResult, isExplicit bool, cfg *config.Config, asJSON bool) error {
	var planned []*checker.UpdateResult
	for _, r := range updatable {
		if !isExplicit && cfg.IsPinned(r.App.BundleID) {
			continue
		}
		if !isExplicit && cfg.Policy(r.App.BundleID) == config.PolicyNotifyOnly {
			continue
		}
		planned = append(planned, r)
	}

	if len(planned) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "DRY RUN — nothing to update.")
		return nil
	}

	if asJSON {
		return printDryRunJSON(cmd, planned)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "DRY RUN — no changes will be made\n\n")
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tFROM\tTO\tSOURCE\tACTION")
	for _, r := range planned {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.App.Name, r.CurrentVersion, r.LatestVersion, canonicalSourceLabel(r.Source, r.SourceOverrideActive), describeAction(r))
	}
	w.Flush()
	fmt.Fprintf(cmd.OutOrStdout(), "\n%d update(s) would be applied.\n", len(planned))
	return nil
}

// dryRunEntry represents a single entry in the JSON dry-run output.
type dryRunEntry struct {
	sourceOverrideJSON
	App    string `json:"app"`
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"`
	Action string `json:"action"`
}

// printDryRunJSON outputs planned updates as a JSON array.
func printDryRunJSON(cmd *cobra.Command, planned []*checker.UpdateResult) error {
	entries := make([]dryRunEntry, len(planned))
	for i, r := range planned {
		entries[i] = dryRunEntry{
			sourceOverrideJSON: sourceOverrideFieldsFromResult(r),
			App:                r.App.Name,
			From:               r.CurrentVersion,
			To:                 r.LatestVersion,
			Source:             r.Source,
			Action:             describeAction(r),
		}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}
