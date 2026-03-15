package app

import (
	"path/filepath"
	"strings"
)

// Source identifies where an app was installed from.
type Source string

const (
	SourceMAS         Source = "mas"
	SourceSparkle     Source = "sparkle"
	SourceBrew        Source = "brew"
	SourceBrewInfo    Source = "brew-info"
	SourceGitHub      Source = "github"
	SourceBrewFormula Source = "formula"
	SourceSystem      Source = "system"
	SourceElectron    Source = "electron"
	SourceSetapp      Source = "setapp"
	SourceToolbox     Source = "toolbox"
	SourceAdobe       Source = "adobe"
	SourceUnknown     Source = "unknown"
)

// App represents an installed macOS application.
type App struct {
	Name             string
	BundleID         string
	Version          string // CFBundleShortVersionString
	Build            string // CFBundleVersion
	Path             string
	Source           Source
	FeedURL          string // SUFeedURL
	CaskName         string
	MASID            string
	GitHubRepo       string // "owner/repo"
	FormulaName      string // Homebrew formula name (e.g. "node", "python@3.12")
	InstalledViaBrew bool   // true only if actually installed via brew
	// Explicit override provenance is populated only from source_overrides.
	ResolvedSourceOverride *SourceOverride
	SourceOverrideActive   bool
	SourceOverrideKind     string
	ElectronUpdateURL      string // Generic update server base URL from app-update.yml
	enrichmentBaseline     *enrichmentBaseline
}

// ToCaskName converts an app display name to a likely Homebrew cask name.
func ToCaskName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ".", "")
	return name
}

// CaskCandidates returns an ordered, deduplicated list of Homebrew cask token
// candidates for an app, drawn from multiple naming signals:
//
//  1. App bundle basename (the .app filename on disk, without the extension).
//     This is the highest-confidence signal: Homebrew cask tokens typically
//     follow the same macOS app-bundle naming convention, so "Visual Studio
//     Code.app" → "visual-studio-code" resolves correctly even though the
//     bundle's display name is just "Code".
//
//  2. Display name via ToCaskName — the existing heuristic, reliable for simple
//     single-word names like "Firefox" or "Slack".
//
//  3. Bundle ID last segment via ToCaskName (e.g. "myapp" from
//     "com.company.MyApp") — useful when the product name is in the identifier.
//
//  4. Bundle ID second-to-last segment via ToCaskName (e.g. "github" from
//     "com.github.GitHubClient") — catches apps whose cask token matches the
//     organisation name rather than the product name.
//
// Callers should probe candidates in order and use the first one that Homebrew
// confirms.  Config-level CaskMappings always take priority over this list.
func CaskCandidates(a *App) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	// 1. Bundle basename — highest confidence.
	if a.Path != "" {
		base := strings.TrimSuffix(filepath.Base(a.Path), ".app")
		add(ToCaskName(base))
	}

	// 2. Display name heuristic.
	add(ToCaskName(a.Name))

	// 3 & 4. Bundle ID segments.
	if a.BundleID != "" {
		parts := strings.Split(a.BundleID, ".")
		n := len(parts)
		if n >= 1 {
			add(ToCaskName(parts[n-1])) // last: often the product name
		}
		if n >= 3 {
			add(ToCaskName(parts[n-2])) // second-to-last: org / company
		}
	}

	return out
}
