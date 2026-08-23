package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// PnpmChecker checks for updates to globally installed pnpm packages.
type PnpmChecker struct {
	runner        CmdRunner
	once          sync.Once
	outdated      map[string]pnpmOutdatedEntry
	outdatedReady bool
}

type pnpmOutdatedEntry struct {
	Current string `json:"current"`
	Wanted  string `json:"wanted"`
	Latest  string `json:"latest"`
}

// NewPnpmChecker creates a new PnpmChecker with the given command runner.
func NewPnpmChecker(runner CmdRunner) *PnpmChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &PnpmChecker{runner: runner}
}

func (p *PnpmChecker) Name() string {
	return "pnpm"
}

func (p *PnpmChecker) CanCheck(a *app.App) bool {
	return a.PnpmPackage != "" && a.Source == app.SourcePnpm
}

// loadOutdated fetches and caches pnpm's global outdated-package JSON.
func (p *PnpmChecker) loadOutdated(ctx context.Context) {
	p.once.Do(func() {
		output, err := p.runner.Run(ctx, "pnpm", "outdated", "-g", "--format", "json")
		if err != nil && len(output) == 0 {
			return
		}
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" || trimmed == "{}" {
			p.outdated = make(map[string]pnpmOutdatedEntry)
			p.outdatedReady = true
			return
		}

		var raw map[string]json.RawMessage
		if jsonErr := json.Unmarshal([]byte(trimmed), &raw); jsonErr != nil {
			return
		}

		p.outdated = make(map[string]pnpmOutdatedEntry)
		for name, msg := range raw {
			var entry pnpmOutdatedEntry
			if jsonErr := json.Unmarshal(msg, &entry); jsonErr == nil && entry.Latest != "" {
				p.outdated[name] = entry
			}
		}
		p.outdatedReady = true
	})
}

func (p *PnpmChecker) viewLatestVersion(ctx context.Context, pkg string) (string, error) {
	output, err := p.runner.Run(ctx, "pnpm", "view", pkg, "version", "--json")
	if err != nil {
		return "", fmt.Errorf("pnpm view %s version: %w", pkg, err)
	}

	var latest string
	if jsonErr := json.Unmarshal(output, &latest); jsonErr != nil {
		latest = strings.TrimSpace(string(output))
		if latest == "" || strings.ContainsAny(latest, "\r\n") {
			return "", fmt.Errorf("pnpm view returned invalid version for %s", pkg)
		}
	}
	if latest == "" {
		return "", fmt.Errorf("pnpm view returned empty version for %s", pkg)
	}
	return latest, nil
}

func (p *PnpmChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.PnpmPackage == "" {
		return nil, fmt.Errorf("failed to check pnpm update: no package name for %s", a.Name)
	}

	p.loadOutdated(ctx)
	if p.outdatedReady {
		if entry, ok := p.outdated[a.PnpmPackage]; ok {
			return &UpdateResult{
				App:            a,
				Source:         "pnpm",
				CurrentVersion: a.Version,
				LatestVersion:  entry.Latest,
				HasUpdate:      version.IsNewer(a.Version, entry.Latest),
				IsMajorUpdate:  version.IsMajorUpgrade(a.Version, entry.Latest),
			}, nil
		}
		return &UpdateResult{
			App:            a,
			Source:         "pnpm",
			CurrentVersion: a.Version,
			LatestVersion:  a.Version,
			HasUpdate:      false,
		}, nil
	}

	latest, err := p.viewLatestVersion(ctx, a.PnpmPackage)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{
		App:            a,
		Source:         "pnpm",
		CurrentVersion: a.Version,
		LatestVersion:  latest,
		HasUpdate:      version.IsNewer(a.Version, latest),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

// ListInstalledPnpmPackages returns globally installed pnpm package versions.
func ListInstalledPnpmPackages(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "pnpm", "list", "-g", "--depth=0", "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to run pnpm list -g --depth=0 --json: %w", err)
	}

	var projects []struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(output, &projects); err != nil {
		return nil, fmt.Errorf("failed to parse pnpm list output: %w", err)
	}

	packages := make(map[string]string)
	for _, project := range projects {
		for name, info := range project.Dependencies {
			if info.Version != "" {
				packages[name] = info.Version
			}
		}
	}
	return packages, nil
}
