package checker

import (
	"context"
	"fmt"
	"strings"

	"github.com/luzhengda/updater/internal/app"
)

// SystemChecker checks for macOS system updates via `softwareupdate -l`.
type SystemChecker struct {
	runner CmdRunner
}

// NewSystemChecker creates a new SystemChecker.
func NewSystemChecker(runner CmdRunner) *SystemChecker {
	return &SystemChecker{runner: runner}
}

// Name returns the checker's display name.
func (s *SystemChecker) Name() string {
	return "system"
}

// CanCheck returns true only for the synthetic macOS system "app".
func (s *SystemChecker) CanCheck(a *app.App) bool {
	return a.BundleID == "com.apple.macOS"
}

// Check queries `softwareupdate -l` for available macOS updates.
func (s *SystemChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	output, err := s.runner.Run(ctx, "softwareupdate", "-l")
	if err != nil {
		return nil, fmt.Errorf("failed to check system updates: %w", err)
	}

	update := parseSystemUpdates(string(output))
	if update == nil {
		return &UpdateResult{
			App:            a,
			Source:         "system",
			CurrentVersion: a.Version,
			LatestVersion:  a.Version,
			HasUpdate:      false,
		}, nil
	}

	return &UpdateResult{
		App:            a,
		Source:         "system",
		CurrentVersion: a.Version,
		LatestVersion:  update.version,
		ReleaseNotes:   update.label,
		HasUpdate:      true,
	}, nil
}

// systemUpdate represents a parsed macOS system update.
type systemUpdate struct {
	label   string
	version string
}

// parseSystemUpdates extracts macOS update info from softwareupdate output.
// Example output:
//
//	Software Update found the following new or updated software:
//	* Label: macOS Sequoia 15.3.1
//	  Title: macOS Sequoia 15.3.1, Version: 15.3.1, Size: 1234K, Recommended: YES, Action: restart,
func parseSystemUpdates(output string) *systemUpdate {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for "* Label: macOS ..."
		if strings.HasPrefix(trimmed, "* Label: macOS") {
			label := strings.TrimPrefix(trimmed, "* Label: ")

			// Try to extract version from the next line or label itself.
			version := extractVersionFromLabel(label)
			if version == "" && i+1 < len(lines) {
				version = extractVersionFromDetail(lines[i+1])
			}

			return &systemUpdate{label: label, version: version}
		}
		// Alternative format: "* macOS Sequoia 15.3.1"
		if strings.HasPrefix(trimmed, "* macOS") {
			label := strings.TrimPrefix(trimmed, "* ")
			version := extractVersionFromLabel(label)
			return &systemUpdate{label: label, version: version}
		}
	}
	return nil
}

// extractVersionFromLabel extracts a version number from a label like "macOS Sequoia 15.3.1".
func extractVersionFromLabel(label string) string {
	parts := strings.Fields(label)
	if len(parts) >= 3 {
		// Last part should be the version number.
		candidate := parts[len(parts)-1]
		if looksLikeVersion(candidate) {
			return candidate
		}
	}
	return ""
}

// extractVersionFromDetail extracts version from a detail line like
// "Title: macOS Sequoia 15.3.1, Version: 15.3.1, Size: ..."
func extractVersionFromDetail(line string) string {
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "Version:") {
			return strings.TrimSpace(strings.TrimPrefix(part, "Version:"))
		}
	}
	return ""
}

// looksLikeVersion returns true if the string looks like a version number (starts with a digit).
func looksLikeVersion(s string) bool {
	return len(s) > 0 && s[0] >= '0' && s[0] <= '9'
}
