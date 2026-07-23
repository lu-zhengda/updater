package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// brewInfoRetryDelay is the pause before retrying a failed brew info call.
// A var so tests can shorten it.
var brewInfoRetryDelay = 500 * time.Millisecond

// brewInfoResponse represents the JSON response from `brew info --cask --json=v2`.
type brewInfoResponse struct {
	Casks []brewInfoCask `json:"casks"`
}

type brewInfoCask struct {
	Token   string `json:"token"`
	Version string `json:"version"`
	URL     string `json:"url"`
	Sha256  string `json:"sha256"`
}

// brewCaskArtifact is the update-relevant data parsed from brew info output.
type brewCaskArtifact struct {
	Version        string
	DownloadURL    string
	DownloadDigest string
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
		// brew fails transiently when several brew processes contend for its
		// locks/cache during a concurrent check run; retry once after a pause.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(brewInfoRetryDelay):
		}
		output, err = b.runner.Run(ctx, "brew", "info", "--cask", "--json=v2", a.CaskName)
		if err != nil {
			return nil, fmt.Errorf("failed to run brew info for %s: %w", a.CaskName, err)
		}
	}

	artifact, err := parseBrewInfo(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse brew info for %s: %w", a.CaskName, err)
	}

	return &UpdateResult{
		App:            a,
		Source:         "brew-info",
		CurrentVersion: a.Version,
		LatestVersion:  artifact.Version,
		HasUpdate:      version.IsNewer(a.Version, artifact.Version),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, artifact.Version),
		DownloadURL:    artifact.DownloadURL,
		DownloadDigest: artifact.DownloadDigest,
	}, nil
}

// parseBrewInfo extracts the version and download artifact from brew info JSON
// output. Handles composite versions like "4.60.1,218372" by taking the part
// before the comma. Casks declaring `sha256 :no_check` yield an empty digest.
func parseBrewInfo(data []byte) (brewCaskArtifact, error) {
	var resp brewInfoResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return brewCaskArtifact{}, fmt.Errorf("failed to unmarshal brew info JSON: %w", err)
	}

	if len(resp.Casks) == 0 {
		return brewCaskArtifact{}, fmt.Errorf("no cask found in brew info response")
	}

	cask := resp.Casks[0]
	v := cask.Version
	if v == "" {
		return brewCaskArtifact{}, fmt.Errorf("empty version in brew info response")
	}

	// Strip composite build number suffix (e.g., "4.60.1,218372" → "4.60.1").
	if idx := strings.IndexByte(v, ','); idx != -1 {
		v = v[:idx]
	}

	artifact := brewCaskArtifact{Version: v, DownloadURL: cask.URL}
	if cask.Sha256 != "" && cask.Sha256 != "no_check" {
		artifact.DownloadDigest = "sha256:" + cask.Sha256
	}
	return artifact, nil
}

// CaskExists checks whether a Homebrew cask exists by running
// `brew info --cask --json=v2 <name>` and checking the exit code.
func CaskExists(ctx context.Context, runner CmdRunner, name string) bool {
	_, err := runner.Run(ctx, "brew", "info", "--cask", "--json=v2", name)
	return err == nil
}
