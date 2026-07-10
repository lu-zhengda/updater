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

// NpmChecker checks for updates to globally installed npm packages.
type NpmChecker struct {
	runner        CmdRunner
	once          sync.Once
	outdated      map[string]npmOutdatedEntry
	outdatedReady bool // true when loadOutdated produced usable data
}

// npmOutdatedEntry represents a single package in `npm outdated -g --json` output.
type npmOutdatedEntry struct {
	Current string `json:"current"`
	Wanted  string `json:"wanted"`
	Latest  string `json:"latest"`
}

// NewNpmChecker creates a new NpmChecker with the given command runner.
// If runner is nil, a RealCmdRunner is used.
func NewNpmChecker(runner CmdRunner) *NpmChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &NpmChecker{runner: runner}
}

// Name returns the checker's display name.
func (n *NpmChecker) Name() string {
	return "npm"
}

// CanCheck returns true if the app is a globally installed npm package.
func (n *NpmChecker) CanCheck(a *app.App) bool {
	return a.NpmPackage != "" && a.Source == app.SourceNpm
}

// loadOutdated fetches and caches the npm outdated output.
// Safe for concurrent use — the command runs at most once.
// Sets outdatedReady to true only when the result is usable.
func (n *NpmChecker) loadOutdated(ctx context.Context) {
	n.once.Do(func() {
		output, err := n.runner.Run(ctx, "npm", "outdated", "-g", "--json")
		// npm outdated exits with code 1 when packages are outdated,
		// but still produces valid JSON on stdout.
		if err != nil && len(output) == 0 {
			return
		}
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" || trimmed == "{}" {
			n.outdated = make(map[string]npmOutdatedEntry)
			n.outdatedReady = true
			return
		}

		// npm may return an error JSON like {"error":{"code":"...","summary":"..."}}
		// instead of the expected outdated map when a package has registry issues
		// (e.g. private/delisted packages). Detect and skip error responses.
		var raw map[string]json.RawMessage
		if jsonErr := json.Unmarshal([]byte(trimmed), &raw); jsonErr != nil {
			return // unparseable — fall back to per-package checks
		}
		if _, hasErr := raw["error"]; hasErr && len(raw) == 1 {
			return // pure error response — fall back to per-package checks
		}

		n.outdated = make(map[string]npmOutdatedEntry)
		for name, msg := range raw {
			if name == "error" {
				continue
			}
			var entry npmOutdatedEntry
			if jsonErr := json.Unmarshal(msg, &entry); jsonErr == nil && entry.Latest != "" {
				n.outdated[name] = entry
			}
		}
		n.outdatedReady = true
	})
}

// viewLatestVersion queries the npm registry for a single package's latest version.
func (n *NpmChecker) viewLatestVersion(ctx context.Context, pkg string) (string, error) {
	output, err := n.runner.Run(ctx, "npm", "view", pkg, "version", "--json")
	if err != nil {
		return "", fmt.Errorf("npm view %s version: %w", pkg, err)
	}
	// npm historically returned a JSON string like "5.7.3", while npm 12
	// may wrap the same value in a one-element array. Accept both forms.
	var ver string
	if jsonErr := json.Unmarshal(output, &ver); jsonErr != nil {
		var versions []string
		if arrayErr := json.Unmarshal(output, &versions); arrayErr == nil {
			for i := len(versions) - 1; i >= 0; i-- {
				if versions[i] != "" {
					ver = versions[i]
					break
				}
			}
		} else {
			// Some versions of npm return the version without JSON quotes.
			// Only accept a single-line fallback so command output can never
			// inject additional rows into terminal renderers.
			ver = strings.TrimSpace(string(output))
			if strings.ContainsAny(ver, "\r\n") {
				return "", fmt.Errorf("npm view returned invalid version for %s", pkg)
			}
		}
	}
	if ver == "" {
		return "", fmt.Errorf("npm view returned empty version for %s", pkg)
	}
	return ver, nil
}

// Check looks up the app's npm package in the cached outdated list.
// If npm outdated failed (e.g. due to private packages), it falls back
// to querying the registry for the individual package.
func (n *NpmChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.NpmPackage == "" {
		return nil, fmt.Errorf("failed to check npm update: no package name for %s", a.Name)
	}

	n.loadOutdated(ctx)

	// If npm outdated produced usable data, use it.
	if n.outdatedReady {
		if entry, ok := n.outdated[a.NpmPackage]; ok {
			return &UpdateResult{
				App:            a,
				Source:         "npm",
				CurrentVersion: a.Version,
				LatestVersion:  entry.Latest,
				HasUpdate:      version.IsNewer(a.Version, entry.Latest),
				IsMajorUpdate:  version.IsMajorUpgrade(a.Version, entry.Latest),
			}, nil
		}
		// Package not in outdated list — no update available.
		return &UpdateResult{
			App:            a,
			Source:         "npm",
			CurrentVersion: a.Version,
			LatestVersion:  a.Version,
			HasUpdate:      false,
		}, nil
	}

	// Fallback: npm outdated was unusable, check this package individually.
	latest, err := n.viewLatestVersion(ctx, a.NpmPackage)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{
		App:            a,
		Source:         "npm",
		CurrentVersion: a.Version,
		LatestVersion:  latest,
		HasUpdate:      version.IsNewer(a.Version, latest),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

// ListInstalledNpmPackages runs `npm list -g --json --depth=0` and returns
// a map of package name to installed version.
func ListInstalledNpmPackages(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "npm", "list", "-g", "--json", "--depth=0")
	if err != nil {
		return nil, fmt.Errorf("failed to run npm list -g --json --depth=0: %w", err)
	}

	var result struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse npm list output: %w", err)
	}

	packages := make(map[string]string, len(result.Dependencies))
	for name, info := range result.Dependencies {
		if info.Version != "" {
			packages[name] = info.Version
		}
	}
	return packages, nil
}
