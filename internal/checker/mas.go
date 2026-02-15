package checker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
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

// CanCheck returns true if the app is from the Mac App Store.
func (m *MASChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceMAS
}

// Check runs `mas outdated` and looks for the app by MASID or name.
func (m *MASChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	output, err := m.runner.Run(ctx, "mas", "outdated")
	if err != nil {
		return nil, fmt.Errorf("failed to run mas outdated: %w", err)
	}

	items, err := parseMASOutdated(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse mas outdated output: %w", err)
	}

	// Match by MASID first, then by name
	for _, item := range items {
		if a.MASID != "" && item.ID == a.MASID {
			a.MASID = item.ID // ensure MASID is set for updates
			return &UpdateResult{
				App:            a,
				Source:         "mas",
				CurrentVersion: a.Version,
				LatestVersion:  item.LatestVersion,
				HasUpdate:      version.IsNewer(a.Version, item.LatestVersion),
				IsMajorUpdate:  version.IsMajorUpgrade(a.Version, item.LatestVersion),
			}, nil
		}
	}

	// Fallback: match by name (case-insensitive, partial match)
	for _, item := range items {
		if strings.EqualFold(item.Name, a.Name) || strings.Contains(strings.ToLower(item.Name), strings.ToLower(a.Name)) {
			a.MASID = item.ID // populate MASID for future use
			return &UpdateResult{
				App:            a,
				Source:         "mas",
				CurrentVersion: a.Version,
				LatestVersion:  item.LatestVersion,
				HasUpdate:      version.IsNewer(a.Version, item.LatestVersion),
				IsMajorUpdate:  version.IsMajorUpgrade(a.Version, item.LatestVersion),
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
