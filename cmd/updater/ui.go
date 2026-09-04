package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/lu-zhengda/updater/internal/tui"
	"github.com/lu-zhengda/updater/internal/updater"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch interactive TUI",
	RunE:  runUI,
}

var flagUIJSON bool

func init() {
	uiCmd.Flags().BoolVar(&flagUIJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(uiCmd)
}

func runUI(cmd *cobra.Command, _ []string) error {
	if jsonOutputEnabled(flagUIJSON) {
		return writeJSON(cmd, map[string]any{
			"status":  "unsupported",
			"command": "ui",
			"message": "interactive TUI is not available in JSON mode",
		})
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := newRunner()
	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	maxConc := cfg.MaxConcurrentOrDefault()
	rollbackManager := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsLimit(), runner)
	updateBackupManager := backupManagerForConfig(cfg, runner)
	inst := installer.New(runner, nil)

	pinnedIDs := make(map[string]bool, len(cfg.PinnedApps))
	for _, id := range cfg.PinnedApps {
		pinnedIDs[id] = true
	}

	// loadFn runs in the background — TUI launches instantly.
	loadFn := func(ctx context.Context) (*tui.LoadResult, error) {
		apps, err := discoverAll(ctx, cfg, runner)
		if err != nil {
			return nil, err
		}

		apps = filterIgnored(apps, cfg)

		// Filter out apps that no checker can handle.
		var checkable []*app.App
		for _, a := range apps {
			if a.Source != app.SourceUnknown || a.CaskName != "" {
				checkable = append(checkable, a)
			}
		}

		return &tui.LoadResult{Apps: checkable, PinnedIDs: pinnedIDs}, nil
	}

	checkFn := func(ctx context.Context, apps []*app.App, onResult func(*checker.UpdateResult)) []*checker.UpdateResult {
		return updater.CheckAllProgress(ctx, apps, checkers, maxConc, onResult)
	}

	updateFn := func(ctx context.Context, result *checker.UpdateResult) error {
		err, _ := executeUpdate(ctx, result, runner, updateBackupManager, inst)
		return err
	}

	scheduleFns := &tui.ScheduleFuncs{
		Check: func() tui.ScheduleStatus {
			return tui.ScheduleStatus{
				Enabled:       scheduleExists(),
				IntervalHours: cfg.ScheduleIntervalOrDefault(),
			}
		},
		Install: func(ctx context.Context, hours int) error {
			if err := installScheduleCore(ctx, runner, hours, false); err != nil {
				return err
			}
			cfg.ScheduleInterval = hours
			cfg.ScheduledAutoUpdate = false
			_ = cfg.Save(cfgPath)
			return nil
		},
		Remove: func(ctx context.Context) error {
			if err := removeScheduleCore(ctx, runner); err != nil {
				return err
			}
			cfg.ScheduledAutoUpdate = false
			return cfg.Save(cfgPath)
		},
	}

	rollbackFn := func(ctx context.Context, appName string) error {
		// Quit app if running.
		apps, _ := discoverApps()
		for _, a := range apps {
			if strings.EqualFold(a.Name, appName) {
				quitAppIfRunning(ctx, a, runner)
				break
			}
		}
		return rollbackManager.Restore(ctx, appName)
	}

	hasBackupFn := func(appName string) bool {
		return rollbackManager.HasBackup(appName)
	}

	historyFn := func() ([]history.Entry, error) {
		return history.List(history.DefaultPath())
	}
	model := tui.NewModel(loadFn, checkFn, updateFn, scheduleFns, cfg, cfgPath, rollbackFn, hasBackupFn, historyFn)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	return nil
}
