package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// brewOutdatedItem represents a single entry from `brew outdated --json`.
type brewOutdatedItem struct {
	Name              string          `json:"name"`
	InstalledVersions json.RawMessage `json:"installed_versions"`
	CurrentVersion    string          `json:"current_version"`
}

// brewOutdatedWrapper is the top-level object from `brew outdated --json` (Homebrew 4.x+).
type brewOutdatedWrapper struct {
	Formulae []brewOutdatedItem `json:"formulae"`
	Casks    []brewOutdatedItem `json:"casks"`
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
		if item.Name == a.CaskName || item.Name == path.Base(a.CaskName) || path.Base(item.Name) == a.CaskName {
			latestVersion := item.CurrentVersion
			// Homebrew detects changes to CLI casks that version alone cannot (e.g. :latest).
			hasUpdate := version.IsNewer(a.Version, latestVersion) || (a.IsBrewCLICask() && latestVersion != "")
			return &UpdateResult{
				App:            a,
				Source:         "brew",
				CurrentVersion: a.Version,
				LatestVersion:  latestVersion,
				HasUpdate:      hasUpdate,
				IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latestVersion),
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

// parseBrewOutdated parses the JSON output of `brew outdated --json`.
// Handles both the flat array format and the wrapped {"formulae":[],"casks":[]} format (Homebrew 4.x+).
func parseBrewOutdated(data []byte) ([]brewOutdatedItem, error) {
	// Try flat array first (legacy format / test mocks).
	var items []brewOutdatedItem
	if err := json.Unmarshal(data, &items); err == nil {
		return items, nil
	}

	// Try wrapped format (Homebrew 4.x+).
	var wrapper brewOutdatedWrapper
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("failed to unmarshal brew outdated JSON: %w", err)
	}
	// Merge formulae and casks into a single list.
	all := make([]brewOutdatedItem, 0, len(wrapper.Formulae)+len(wrapper.Casks))
	all = append(all, wrapper.Formulae...)
	all = append(all, wrapper.Casks...)
	return all, nil
}

// InstalledCaskApps maps the resolved paths of installed cask app artifacts to their tokens.
func InstalledCaskApps(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "brew", "--caskroom")
	if err != nil {
		return nil, fmt.Errorf("failed to locate Homebrew Caskroom: %w", err)
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return nil, nil
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("invalid Homebrew Caskroom path %q", root)
	}
	casks := make(map[string]string)
	// Homebrew stages each app at <token>/<version>/<app>.app, linking moved apps.
	err = fs.WalkDir(os.DirFS(root), ".", func(rel string, entry fs.DirEntry, err error) error {
		if err != nil {
			if rel == "." && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		depth := strings.Count(rel, "/")
		if depth == 2 && filepath.Ext(entry.Name()) == ".app" {
			resolved, err := filepath.EvalSymlinks(filepath.Join(root, rel))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err == nil {
				token := path.Dir(path.Dir(rel))
				if previous, ok := casks[resolved]; ok && previous != token {
					token = ""
				}
				casks[resolved] = token
			}
		}
		if entry.IsDir() && (depth >= 2 || (rel != "." && strings.HasPrefix(entry.Name(), "."))) {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to inspect Homebrew Caskroom: %w", err)
	}
	return casks, nil
}
