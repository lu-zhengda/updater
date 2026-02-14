package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// brewOutdatedItem represents a single entry from `brew outdated --cask --greedy --json`.
type brewOutdatedItem struct {
	Name              string `json:"name"`
	InstalledVersions string `json:"installed_versions"`
	CurrentVersion    string `json:"current_version"`
}

// BrewChecker checks for Homebrew Cask updates.
type BrewChecker struct {
	runner CmdRunner
}

// NewBrewChecker creates a new BrewChecker with the given command runner.
// If runner is nil, a RealCmdRunner is used.
func NewBrewChecker(runner CmdRunner) *BrewChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &BrewChecker{runner: runner}
}

// Name returns the checker's display name.
func (b *BrewChecker) Name() string {
	return "brew"
}

// CanCheck returns true if the app was installed via brew and has a cask name.
func (b *BrewChecker) CanCheck(a *app.App) bool {
	return a.CaskName != "" && a.InstalledViaBrew
}

// Check runs `brew outdated --cask --greedy --json` and looks for the app's cask.
func (b *BrewChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.CaskName == "" {
		return nil, fmt.Errorf("failed to check brew update: no cask name for %s", a.Name)
	}

	output, err := b.runner.Run(ctx, "brew", "outdated", "--cask", "--greedy", "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to run brew outdated: %w", err)
	}

	items, err := parseBrewOutdated(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse brew outdated output: %w", err)
	}

	for _, item := range items {
		if item.Name == a.CaskName {
			latestVersion := item.CurrentVersion
			return &UpdateResult{
				App:            a,
				Source:         "brew",
				CurrentVersion: a.Version,
				LatestVersion:  latestVersion,
				HasUpdate:      version.IsNewer(a.Version, latestVersion),
			}, nil
		}
	}

	// Cask not in outdated list — no update available.
	return &UpdateResult{
		App:            a,
		Source:         "brew",
		CurrentVersion: a.Version,
		LatestVersion:  a.Version,
		HasUpdate:      false,
	}, nil
}

// parseBrewOutdated parses the JSON output of `brew outdated --cask --greedy --json`.
func parseBrewOutdated(data []byte) ([]brewOutdatedItem, error) {
	var items []brewOutdatedItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("failed to unmarshal brew outdated JSON: %w", err)
	}
	return items, nil
}

// ListInstalledCasks runs `brew list --cask` and returns a set of installed cask names.
func ListInstalledCasks(ctx context.Context, runner CmdRunner) (map[string]bool, error) {
	output, err := runner.Run(ctx, "brew", "list", "--cask")
	if err != nil {
		return nil, fmt.Errorf("failed to run brew list --cask: %w", err)
	}

	casks := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			casks[line] = true
		}
	}
	return casks, nil
}
