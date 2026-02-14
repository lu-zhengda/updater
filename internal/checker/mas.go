package checker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// masOutdatedRegex matches lines from `mas outdated` output.
// Format: "441258766 Magnet (3.0.6 -> 3.0.7)"
var masOutdatedRegex = regexp.MustCompile(`^(\d+)\s+(.+?)\s+\((.+?)\s+->\s+(.+?)\)$`)

// masOutdatedItem represents a single outdated app from `mas outdated`.
type masOutdatedItem struct {
	ID             string
	Name           string
	CurrentVersion string
	LatestVersion  string
}

// MASChecker checks for Mac App Store updates via mas-cli.
type MASChecker struct {
	runner CmdRunner
}

// NewMASChecker creates a new MASChecker with the given command runner.
// If runner is nil, a RealCmdRunner is used.
func NewMASChecker(runner CmdRunner) *MASChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &MASChecker{runner: runner}
}

// Name returns the checker's display name.
func (m *MASChecker) Name() string {
	return "mas"
}

// CanCheck returns true if the app is from the Mac App Store and has a MASID.
func (m *MASChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceMAS && a.MASID != ""
}

// Check runs `mas outdated` and looks for the app by MASID.
func (m *MASChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.MASID == "" {
		return nil, fmt.Errorf("failed to check MAS update: no MASID for %s", a.Name)
	}

	output, err := m.runner.Run(ctx, "mas", "outdated")
	if err != nil {
		return nil, fmt.Errorf("failed to run mas outdated: %w", err)
	}

	items, err := parseMASOutdated(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse mas outdated output: %w", err)
	}

	for _, item := range items {
		if item.ID == a.MASID {
			return &UpdateResult{
				App:            a,
				Source:         "mas",
				CurrentVersion: a.Version,
				LatestVersion:  item.LatestVersion,
				HasUpdate:      version.IsNewer(a.Version, item.LatestVersion),
			}, nil
		}
	}

	// App not in outdated list — no update available.
	return &UpdateResult{
		App:            a,
		Source:         "mas",
		CurrentVersion: a.Version,
		LatestVersion:  a.Version,
		HasUpdate:      false,
	}, nil
}

// parseMASOutdated parses the text output of `mas outdated`.
func parseMASOutdated(data []byte) ([]masOutdatedItem, error) {
	var items []masOutdatedItem
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := masOutdatedRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		items = append(items, masOutdatedItem{
			ID:             matches[1],
			Name:           matches[2],
			CurrentVersion: matches[3],
			LatestVersion:  matches[4],
		})
	}

	return items, nil
}
