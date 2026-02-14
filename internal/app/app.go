package app

import "strings"

// Source identifies where an app was installed from.
type Source string

const (
	SourceMAS         Source = "mas"
	SourceSparkle     Source = "sparkle"
	SourceBrew        Source = "brew"
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
	FormulaName       string // Homebrew formula name (e.g. "node", "python@3.12")
	InstalledViaBrew  bool   // true only if actually installed via brew
	ElectronUpdateURL string // Generic update server base URL from app-update.yml
}

// ToCaskName converts an app display name to a likely Homebrew cask name.
func ToCaskName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ".", "")
	return name
}
