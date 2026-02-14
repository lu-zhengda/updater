package main

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
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

func runUI(cmd *cobra.Command, _ []string) error {
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
	apps, err = enrichApps(ctx, apps, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	// Filter out apps with unknown source — they are not checkable.
	var checkable []*app.App
	for _, a := range apps {
		if a.Source != app.SourceUnknown {
			checkable = append(checkable, a)
		}
	}

	checkers := buildCheckers(runner)

	checkFn := func(ctx context.Context, apps []*app.App) []*checker.UpdateResult {
		return checkAll(ctx, apps, checkers)
	}

	updateFn := func(ctx context.Context, result *checker.UpdateResult) error {
		return executeUpdate(ctx, result, runner)
	}

	model := tui.NewModel(checkable, checkFn, updateFn)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("failed to run TUI: %w", err)
	}

	return nil
}
