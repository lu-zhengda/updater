package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// brewInfoResponse represents the JSON response from `brew info --cask --json=v2`.
type brewInfoResponse struct {
	Casks []brewInfoCask `json:"casks"`
}

type brewInfoCask struct {
	Token   string `json:"token"`
	Version string `json:"version"`
}

// BrewInfoChecker checks for updates using `brew info --cask --json=v2`.
// Unlike BrewChecker, this works for any cask regardless of whether
// it was installed via brew.
type BrewInfoChecker struct {
	runner CmdRunner
}

// NewBrewInfoChecker creates a new BrewInfoChecker with the given command runner.
func NewBrewInfoChecker(runner CmdRunner) *BrewInfoChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &BrewInfoChecker{runner: runner}
}

// Name returns the checker's display name.
func (b *BrewInfoChecker) Name() string {
	return "brew-info"
}

// CanCheck returns true if the app has a cask name.
func (b *BrewInfoChecker) CanCheck(a *app.App) bool {
	return a.CaskName != ""
}

// Check runs `brew info --cask --json=v2 <cask-name>` and compares versions.
func (b *BrewInfoChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.CaskName == "" {
		return nil, fmt.Errorf("failed to check brew info: no cask name for %s", a.Name)
	}

	output, err := b.runner.Run(ctx, "brew", "info", "--cask", "--json=v2", a.CaskName)
	if err != nil {
		return nil, fmt.Errorf("failed to run brew info for %s: %w", a.CaskName, err)
	}

	latestVersion, err := parseBrewInfo(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse brew info for %s: %w", a.CaskName, err)
	}

	return &UpdateResult{
		App:            a,
		Source:         "brew-info",
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latestVersion),
	}, nil
}

// parseBrewInfo extracts the version from brew info JSON output.
// Handles composite versions like "4.60.1,218372" by taking the part before the comma.
func parseBrewInfo(data []byte) (string, error) {
	var resp brewInfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("failed to unmarshal brew info JSON: %w", err)
	}

	if len(resp.Casks) == 0 {
		return "", fmt.Errorf("no cask found in brew info response")
	}

	v := resp.Casks[0].Version
	if v == "" {
		return "", fmt.Errorf("empty version in brew info response")
	}

	// Strip composite build number suffix (e.g., "4.60.1,218372" → "4.60.1").
	if idx := strings.IndexByte(v, ','); idx != -1 {
		v = v[:idx]
	}

	return v, nil
}

// CaskExists checks whether a Homebrew cask exists by running
// `brew info --cask --json=v2 <name>` and checking the exit code.
func CaskExists(ctx context.Context, runner CmdRunner, name string) bool {
	_, err := runner.Run(ctx, "brew", "info", "--cask", "--json=v2", name)
	return err == nil
}
