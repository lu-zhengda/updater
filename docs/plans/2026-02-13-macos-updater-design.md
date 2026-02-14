# macOS App Updater — Design Document

**Date:** 2026-02-13
**Status:** Approved

## Overview

A CLI + TUI tool written in Go that discovers installed macOS applications, detects available updates from multiple sources, and can update them automatically or interactively.

## Goals

1. Scan `/Applications` and `~/Applications` for installed apps
2. Classify apps by update source (Mac App Store, Sparkle, Homebrew Cask, GitHub)
3. Check all apps for available updates concurrently
4. Provide both CLI commands and an interactive TUI (Bubbletea)
5. Support manual trigger and unattended auto-update modes

## Architecture

```
cmd/updater/main.go          # Entry point, Cobra root command
internal/
  app/
    discovery.go              # Scan directories, parse Info.plist
    app.go                    # App model and classification
  checker/
    checker.go                # Checker interface
    sparkle.go                # Sparkle appcast feed checker
    brew.go                   # Homebrew Cask checker
    mas.go                    # Mac App Store checker (via mas-cli)
    github.go                 # GitHub Releases API checker
    registry.go               # Registry of all checkers
  updater/
    updater.go                # Orchestrates update execution
    download.go               # Download + install DMG/ZIP
    brew_updater.go           # brew upgrade --cask
    mas_updater.go            # mas upgrade
  version/
    compare.go                # Semantic version comparison
  config/
    config.go                 # User configuration (ignored apps, etc.)
  tui/
    tui.go                    # Bubbletea main model
    table.go                  # App table component
    styles.go                 # Lipgloss styles
```

## App Discovery

- Scan `/Applications` and `~/Applications` for `.app` bundles
- Parse `Contents/Info.plist` using howett.net/plist:
  - `CFBundleName` / `CFBundleDisplayName`
  - `CFBundleIdentifier`
  - `CFBundleShortVersionString` (display version)
  - `CFBundleVersion` (build number)
  - `SUFeedURL` (Sparkle feed URL)

### Classification Priority

1. **Mac App Store**: `Contents/_MASReceipt/receipt` exists
2. **Homebrew Cask**: Listed in `brew list --cask` output
3. **Sparkle**: Has `Sparkle.framework` in Frameworks dir AND `SUFeedURL` in plist
4. **GitHub**: Bundle ID matches known pattern or manual config mapping
5. **Unknown**: No detected update mechanism

## Update Checkers

### Interface

```go
type Checker interface {
    Name() string
    CanCheck(app *app.App) bool
    Check(ctx context.Context, a *app.App) (*UpdateResult, error)
}

type UpdateResult struct {
    App            *app.App
    Source         string
    CurrentVersion string
    LatestVersion  string
    DownloadURL    string
    ReleaseNotes   string
    HasUpdate      bool
}
```

### Sparkle Checker

- Read `SUFeedURL` from app's Info.plist
- HTTP GET the appcast.xml feed
- Parse RSS/XML, extract latest `<item>`:
  - `sparkle:shortVersionString` or `sparkle:version`
  - `enclosure url` for download
  - `sparkle:releaseNotesLink` for notes
- Compare with app's current version

### Homebrew Cask Checker

- Run `brew outdated --cask --greedy --json`
- Parse JSON output for app names and versions
- Cross-reference with discovered apps by name/path

### Mac App Store Checker

- Run `mas outdated` to get list of outdated MAS apps
- Parse output: `<id> <name> (<current> -> <latest>)`
- Match by app ID from MAS receipt

### GitHub Releases Checker

- Requires config mapping: bundle ID -> GitHub repo
- Query `https://api.github.com/repos/{owner}/{repo}/releases/latest`
- Extract `tag_name`, strip version prefix
- Find macOS asset (.dmg, .zip, .pkg) in assets array

## CLI Commands

| Command | Description |
|---------|-------------|
| `updater scan` | List all discovered apps with their source classification |
| `updater check` | Check all apps for available updates |
| `updater update <app>` | Update a specific app by name |
| `updater update --all` | Update all apps with available updates |
| `updater update --auto` | Unattended mode for automation |
| `updater ui` | Launch interactive TUI |

## TUI Design

- Full-screen Bubbletea application
- Table columns: Name, Current, Latest, Source, Status
- Color coding: green=up-to-date, yellow=update available, red=error, gray=ignored
- Key bindings: j/k=navigate, Enter=update selected, a=update all, i=ignore, r=refresh, q=quit
- Spinner during update checks
- Status bar at bottom with help text

## Update Execution

- **Sparkle/GitHub**: Download DMG/ZIP -> mount/extract -> copy .app to /Applications
- **Homebrew Cask**: `brew upgrade --cask <name>`
- **Mac App Store**: `mas upgrade <id>` (or open App Store if mas unavailable)

## Configuration

YAML config at `~/.config/updater/config.yaml`:

```yaml
ignored_apps:
  - com.example.AppToIgnore
github_mappings:
  com.mitchellh.ghostty: "ghostty-org/ghostty"
  com.visualstudio.code: "microsoft/vscode"
auto_update:
  enabled: false
  schedule: daily
```

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — TUI styling
- `github.com/charmbracelet/bubbles` — TUI components
- `howett.net/plist` — Apple plist parsing
- `github.com/Masterminds/semver/v3` — version comparison

## External Tool Dependencies

- `brew` (Homebrew) — optional, for cask checking/updating
- `mas` (mas-cli) — optional, for App Store checking/updating
