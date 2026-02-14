package checker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// BrewFormulaChecker checks for Homebrew formula updates.
type BrewFormulaChecker struct {
	runner   CmdRunner
	once     sync.Once
	outdated []brewOutdatedItem
	parseErr error
}

// NewBrewFormulaChecker creates a new BrewFormulaChecker with the given command runner.
// If runner is nil, a RealCmdRunner is used.
func NewBrewFormulaChecker(runner CmdRunner) *BrewFormulaChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &BrewFormulaChecker{runner: runner}
}

// Name returns the checker's display name.
func (b *BrewFormulaChecker) Name() string {
	return "formula"
}

// CanCheck returns true if the app is a brew formula.
func (b *BrewFormulaChecker) CanCheck(a *app.App) bool {
	return a.FormulaName != "" && a.Source == app.SourceBrewFormula
}

// loadOutdated fetches and caches the brew outdated formula list.
// Safe for concurrent use — the command runs at most once.
func (b *BrewFormulaChecker) loadOutdated(ctx context.Context) ([]brewOutdatedItem, error) {
	b.once.Do(func() {
		output, err := b.runner.Run(ctx, "brew", "outdated", "--formula", "--json")
		if err != nil {
			b.parseErr = fmt.Errorf("failed to run brew outdated --formula: %w", err)
			return
		}
		b.outdated, b.parseErr = parseBrewOutdated(output)
		if b.parseErr != nil {
			b.parseErr = fmt.Errorf("failed to parse brew outdated output: %w", b.parseErr)
		}
	})
	return b.outdated, b.parseErr
}

// Check looks up the app's formula in the cached outdated list.
func (b *BrewFormulaChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.FormulaName == "" {
		return nil, fmt.Errorf("failed to check formula update: no formula name for %s", a.Name)
	}

	items, err := b.loadOutdated(ctx)
	if err != nil {
		return nil, err
	}

	for _, item := range items {
		if item.Name == a.FormulaName {
			latestVersion := item.CurrentVersion
			return &UpdateResult{
				App:            a,
				Source:         "formula",
				CurrentVersion: a.Version,
				LatestVersion:  latestVersion,
				HasUpdate:      version.IsNewer(a.Version, latestVersion),
			}, nil
		}
	}

	// Formula not in outdated list — no update available.
	return &UpdateResult{
		App:            a,
		Source:         "formula",
		CurrentVersion: a.Version,
		LatestVersion:  a.Version,
		HasUpdate:      false,
	}, nil
}

// ListInstalledFormulae runs `brew list --formula --versions` and returns
// a map of formula name to installed version.
func ListInstalledFormulae(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "brew", "list", "--formula", "--versions")
	if err != nil {
		return nil, fmt.Errorf("failed to run brew list --formula --versions: %w", err)
	}

	formulae := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "name version [version...]" — take the last version (most recent).
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			formulae[parts[0]] = parts[len(parts)-1]
		}
	}
	return formulae, nil
}
