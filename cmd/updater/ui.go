package main

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/backup"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/luzhengda/updater/internal/installer"
	"github.com/luzhengda/updater/internal/tui"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch interactive TUI",
	RunE:  runUI,
}

func init() {
	rootCmd.AddCommand(uiCmd)
}

func runUI(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := &checker.RealCmdRunner{}
	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	maxConc := cfg.MaxConcurrentOrDefault()
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)
	inst := installer.New(runner, nil)

	pinnedIDs := make(map[string]bool, len(cfg.PinnedApps))
	for _, id := range cfg.PinnedApps {
		pinnedIDs[id] = true
	}

	// loadFn runs in the background — TUI launches instantly.
	loadFn := func(ctx context.Context) (*tui.LoadResult, error) {
		apps, err := discoverApps()
		if err != nil {
			return nil, err
		}

		apps, err = enrichApps(ctx, apps, cfg, runner)
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

	checkFn := func(ctx context.Context, apps []*app.App) []*checker.UpdateResult {
		return checkAll(ctx, apps, checkers, maxConc)
	}

	updateFn := func(ctx context.Context, result *checker.UpdateResult) error {
		return executeUpdate(ctx, result, runner, bm, inst)
	}

	model := tui.NewModel(loadFn, checkFn, updateFn)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	return nil
}
