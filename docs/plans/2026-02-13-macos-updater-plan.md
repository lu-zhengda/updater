# macOS App Updater Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go CLI+TUI tool that discovers macOS apps, checks for updates from Sparkle/Homebrew/MAS/GitHub, and can update them interactively or automatically.

**Architecture:** Modular Go binary with Cobra CLI + Bubbletea TUI. Each update source implements a `Checker` interface. App discovery scans /Applications, parses Info.plist, classifies by source. Concurrent checking via errgroup.

**Tech Stack:** Go 1.25, Cobra, Bubbletea/Lipgloss/Bubbles, howett.net/plist, semver/v3

---

## Phase 1: Project Setup & App Discovery

### Task 1: Initialize Go module and project structure

**Files:**
- Create: `go.mod`
- Create: `cmd/updater/main.go`
- Create: `internal/app/app.go`

**Step 1: Initialize Go module**

Run: `go mod init github.com/luzhengda/updater`

**Step 2: Create main entry point**

Create `cmd/updater/main.go`:
```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "updater",
	Short: "macOS app update manager",
	Long:  "Discover installed macOS apps, check for updates, and update them from multiple sources.",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

**Step 3: Create App model**

Create `internal/app/app.go`:
```go
package app

// Source identifies how an app receives updates.
type Source string

const (
	SourceMAS     Source = "mas"
	SourceSparkle Source = "sparkle"
	SourceBrew    Source = "brew"
	SourceGitHub  Source = "github"
	SourceUnknown Source = "unknown"
)

// App represents an installed macOS application.
type App struct {
	Name      string
	BundleID  string
	Version   string // CFBundleShortVersionString
	Build     string // CFBundleVersion
	Path      string // Full path to .app bundle
	Source    Source
	// Sparkle-specific
	FeedURL string // SUFeedURL from Info.plist
	// Brew-specific
	CaskName string
	// MAS-specific
	MASID string
	// GitHub-specific
	GitHubRepo string // "owner/repo"
}
```

**Step 4: Install Cobra dependency and verify build**

Run: `go get github.com/spf13/cobra && go build ./cmd/updater/`
Expected: builds successfully

**Step 5: Commit**

```bash
git add go.mod go.sum cmd/ internal/
git commit -m "feat: initialize project with Cobra CLI and App model"
```

---

### Task 2: App discovery — scan /Applications and parse Info.plist

**Files:**
- Create: `internal/app/discovery.go`
- Create: `internal/app/discovery_test.go`

**Step 1: Write the failing test**

Create `internal/app/discovery_test.go`:
```go
package app_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"howett.net/plist"
)

// createFakeApp creates a minimal .app bundle in a temp dir for testing.
func createFakeApp(t *testing.T, dir, name, bundleID, version string, mas bool, feedURL string) string {
	t.Helper()
	appDir := filepath.Join(dir, name+".app", "Contents")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	info := map[string]string{
		"CFBundleName":               name,
		"CFBundleIdentifier":         bundleID,
		"CFBundleShortVersionString": version,
		"CFBundleVersion":            version,
	}
	if feedURL != "" {
		info["SUFeedURL"] = feedURL
	}

	f, err := os.Create(filepath.Join(appDir, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := plist.NewEncoder(f).Encode(info); err != nil {
		t.Fatal(err)
	}

	if mas {
		receiptDir := filepath.Join(appDir, "_MASReceipt")
		if err := os.MkdirAll(receiptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(receiptDir, "receipt"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if feedURL != "" {
		fwDir := filepath.Join(appDir, "Frameworks", "Sparkle.framework")
		if err := os.MkdirAll(fwDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return filepath.Join(dir, name+".app")
}

func TestDiscoverApps(t *testing.T) {
	dir := t.TempDir()

	createFakeApp(t, dir, "TestApp", "com.test.app", "1.0.0", false, "")
	createFakeApp(t, dir, "MASApp", "com.test.masapp", "2.0.0", true, "")
	createFakeApp(t, dir, "SparkleApp", "com.test.sparkleapp", "3.0.0", false, "https://example.com/appcast.xml")

	apps, err := app.Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(apps) != 3 {
		t.Fatalf("expected 3 apps, got %d", len(apps))
	}

	// Build a map for easier assertions
	byName := make(map[string]*app.App)
	for _, a := range apps {
		byName[a.Name] = a
	}

	// Check MAS classification
	masApp := byName["MASApp"]
	if masApp == nil {
		t.Fatal("MASApp not found")
	}
	if masApp.Source != app.SourceMAS {
		t.Errorf("MASApp source = %q, want %q", masApp.Source, app.SourceMAS)
	}

	// Check Sparkle classification
	sparkleApp := byName["SparkleApp"]
	if sparkleApp == nil {
		t.Fatal("SparkleApp not found")
	}
	if sparkleApp.Source != app.SourceSparkle {
		t.Errorf("SparkleApp source = %q, want %q", sparkleApp.Source, app.SourceSparkle)
	}
	if sparkleApp.FeedURL != "https://example.com/appcast.xml" {
		t.Errorf("SparkleApp FeedURL = %q, want %q", sparkleApp.FeedURL, "https://example.com/appcast.xml")
	}

	// Check unknown classification
	testApp := byName["TestApp"]
	if testApp == nil {
		t.Fatal("TestApp not found")
	}
	if testApp.Source != app.SourceUnknown {
		t.Errorf("TestApp source = %q, want %q", testApp.Source, app.SourceUnknown)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/app/ -v -run TestDiscoverApps`
Expected: FAIL — `Discover` function not defined

**Step 3: Implement app discovery**

Create `internal/app/discovery.go`:
```go
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"
)

type infoPlist struct {
	Name        string `plist:"CFBundleName"`
	DisplayName string `plist:"CFBundleDisplayName"`
	BundleID    string `plist:"CFBundleIdentifier"`
	Version     string `plist:"CFBundleShortVersionString"`
	Build       string `plist:"CFBundleVersion"`
	FeedURL     string `plist:"SUFeedURL"`
}

// Discover scans the given directory for .app bundles and returns classified apps.
func Discover(dirs ...string) ([]*App, error) {
	var apps []*App
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".app") {
				continue
			}
			appPath := filepath.Join(dir, entry.Name())
			a, err := parseApp(appPath)
			if err != nil {
				continue // skip unparseable apps
			}
			apps = append(apps, a)
		}
	}
	return apps, nil
}

func parseApp(appPath string) (*App, error) {
	contentsDir := filepath.Join(appPath, "Contents")
	plistPath := filepath.Join(contentsDir, "Info.plist")

	f, err := os.Open(plistPath)
	if err != nil {
		return nil, fmt.Errorf("opening plist: %w", err)
	}
	defer f.Close()

	var info infoPlist
	if err := plist.NewDecoder(f).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding plist: %w", err)
	}

	name := info.DisplayName
	if name == "" {
		name = info.Name
	}
	if name == "" {
		// Fallback to directory name without .app suffix
		name = strings.TrimSuffix(filepath.Base(appPath), ".app")
	}

	a := &App{
		Name:     name,
		BundleID: info.BundleID,
		Version:  info.Version,
		Build:    info.Build,
		Path:     appPath,
		FeedURL:  info.FeedURL,
		Source:   SourceUnknown,
	}

	// Classify: MAS takes priority
	if isMASApp(contentsDir) {
		a.Source = SourceMAS
	} else if isSparkleApp(contentsDir, info.FeedURL) {
		a.Source = SourceSparkle
	}

	return a, nil
}

func isMASApp(contentsDir string) bool {
	receiptPath := filepath.Join(contentsDir, "_MASReceipt", "receipt")
	_, err := os.Stat(receiptPath)
	return err == nil
}

func isSparkleApp(contentsDir string, feedURL string) bool {
	if feedURL == "" {
		return false
	}
	sparkleDir := filepath.Join(contentsDir, "Frameworks", "Sparkle.framework")
	_, err := os.Stat(sparkleDir)
	return err == nil
}
```

**Step 4: Install plist dependency and run tests**

Run: `go get howett.net/plist && go test -race ./internal/app/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/app/ go.mod go.sum
git commit -m "feat: add app discovery with plist parsing and source classification"
```

---

## Phase 2: Version Comparison

### Task 3: Semantic version comparison

**Files:**
- Create: `internal/version/compare.go`
- Create: `internal/version/compare_test.go`

**Step 1: Write the failing test**

Create `internal/version/compare_test.go`:
```go
package version_test

import (
	"testing"

	"github.com/luzhengda/updater/internal/version"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"simple newer", "1.0.0", "1.1.0", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"older", "2.0.0", "1.0.0", false},
		{"patch update", "1.0.0", "1.0.1", true},
		{"major update", "1.9.9", "2.0.0", true},
		{"with v prefix", "v1.0.0", "v1.1.0", true},
		{"mixed prefix", "1.0.0", "v1.1.0", true},
		{"two-part version", "1.0", "1.1", true},
		{"build numbers", "100", "200", true},
		{"complex", "3.6.6", "3.7.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := version.IsNewer(tt.current, tt.latest)
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/version/ -v`
Expected: FAIL

**Step 3: Implement version comparison**

Create `internal/version/compare.go`:
```go
package version

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// IsNewer returns true if latest is a newer version than current.
func IsNewer(current, latest string) bool {
	current = normalize(current)
	latest = normalize(latest)

	cv, err1 := semver.NewVersion(current)
	lv, err2 := semver.NewVersion(latest)
	if err1 != nil || err2 != nil {
		// Fallback to string comparison if not valid semver
		return latest > current
	}
	return lv.GreaterThan(cv)
}

func normalize(v string) string {
	v = strings.TrimPrefix(v, "v")
	// Pad to 3-part semver if needed: "1.0" -> "1.0.0", "100" -> "100.0.0"
	parts := strings.Split(v, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	return strings.Join(parts, ".")
}
```

**Step 4: Install dependency and run tests**

Run: `go get github.com/Masterminds/semver/v3 && go test -race ./internal/version/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/version/ go.mod go.sum
git commit -m "feat: add semantic version comparison with normalization"
```

---

## Phase 3: Update Checkers

### Task 4: Checker interface and UpdateResult model

**Files:**
- Create: `internal/checker/checker.go`

**Step 1: Create checker interface**

Create `internal/checker/checker.go`:
```go
package checker

import (
	"context"

	"github.com/luzhengda/updater/internal/app"
)

// UpdateResult holds the result of an update check for a single app.
type UpdateResult struct {
	App            *app.App
	Source         string
	CurrentVersion string
	LatestVersion  string
	DownloadURL    string
	ReleaseNotes   string
	HasUpdate      bool
	Error          error
}

// Checker checks a single app for available updates.
type Checker interface {
	Name() string
	CanCheck(a *app.App) bool
	Check(ctx context.Context, a *app.App) (*UpdateResult, error)
}
```

**Step 2: Verify build**

Run: `go build ./internal/checker/`
Expected: builds successfully

**Step 3: Commit**

```bash
git add internal/checker/
git commit -m "feat: add Checker interface and UpdateResult model"
```

---

### Task 5: Sparkle appcast checker

**Files:**
- Create: `internal/checker/sparkle.go`
- Create: `internal/checker/sparkle_test.go`

**Step 1: Write the failing test**

Create `internal/checker/sparkle_test.go`:
```go
package checker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

const testAppcast = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>TestApp Updates</title>
    <item>
      <title>Version 2.0.0</title>
      <sparkle:version>200</sparkle:version>
      <sparkle:shortVersionString>2.0.0</sparkle:shortVersionString>
      <sparkle:releaseNotesLink>https://example.com/notes.html</sparkle:releaseNotesLink>
      <pubDate>Mon, 05 Oct 2025 19:20:11 +0000</pubDate>
      <enclosure url="https://example.com/app.dmg" length="1234" type="application/octet-stream" />
    </item>
    <item>
      <title>Version 1.5.0</title>
      <sparkle:version>150</sparkle:version>
      <sparkle:shortVersionString>1.5.0</sparkle:shortVersionString>
      <enclosure url="https://example.com/old.dmg" length="1000" type="application/octet-stream" />
    </item>
  </channel>
</rss>`

func TestSparkleChecker_Check(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testAppcast))
	}))
	defer srv.Close()

	c := checker.NewSparkleChecker(http.DefaultClient)
	a := &app.App{
		Name:    "TestApp",
		Version: "1.0.0",
		Source:  app.SourceSparkle,
		FeedURL: srv.URL + "/appcast.xml",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.0.0")
	}
	if result.DownloadURL != "https://example.com/app.dmg" {
		t.Errorf("DownloadURL = %q, want %q", result.DownloadURL, "https://example.com/app.dmg")
	}
}

func TestSparkleChecker_NoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testAppcast))
	}))
	defer srv.Close()

	c := checker.NewSparkleChecker(http.DefaultClient)
	a := &app.App{
		Name:    "TestApp",
		Version: "2.0.0",
		Source:  app.SourceSparkle,
		FeedURL: srv.URL + "/appcast.xml",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate=false for same version")
	}
}

func TestSparkleChecker_CanCheck(t *testing.T) {
	c := checker.NewSparkleChecker(http.DefaultClient)

	sparkleApp := &app.App{Source: app.SourceSparkle, FeedURL: "https://example.com/feed.xml"}
	if !c.CanCheck(sparkleApp) {
		t.Error("should be able to check sparkle app")
	}

	otherApp := &app.App{Source: app.SourceMAS}
	if c.CanCheck(otherApp) {
		t.Error("should not check MAS app")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/checker/ -v`
Expected: FAIL

**Step 3: Implement Sparkle checker**

Create `internal/checker/sparkle.go`:
```go
package checker

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// Appcast RSS feed structure for Sparkle.
type appcastRSS struct {
	XMLName xml.Name         `xml:"rss"`
	Channel appcastChannel   `xml:"channel"`
}

type appcastChannel struct {
	Items []appcastItem `xml:"item"`
}

type appcastItem struct {
	Title            string          `xml:"title"`
	Version          string          `xml:"version"`
	ShortVersion     string          `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
	BuildVersion     string          `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
	ReleaseNotesLink string          `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle releaseNotesLink"`
	Enclosure        appcastEnclosure `xml:"enclosure"`
}

type appcastEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type SparkleChecker struct {
	client *http.Client
}

func NewSparkleChecker(client *http.Client) *SparkleChecker {
	return &SparkleChecker{client: client}
}

func (c *SparkleChecker) Name() string { return "sparkle" }

func (c *SparkleChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceSparkle && a.FeedURL != ""
}

func (c *SparkleChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.FeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching appcast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("appcast returned status %d", resp.StatusCode)
	}

	var rss appcastRSS
	if err := xml.NewDecoder(resp.Body).Decode(&rss); err != nil {
		return nil, fmt.Errorf("decoding appcast: %w", err)
	}

	if len(rss.Channel.Items) == 0 {
		return &UpdateResult{
			App:            a,
			Source:         c.Name(),
			CurrentVersion: a.Version,
			HasUpdate:      false,
		}, nil
	}

	// First item is typically the latest
	latest := rss.Channel.Items[0]
	latestVersion := latest.ShortVersion
	if latestVersion == "" {
		latestVersion = latest.BuildVersion
	}

	return &UpdateResult{
		App:            a,
		Source:         c.Name(),
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    latest.Enclosure.URL,
		ReleaseNotes:   latest.ReleaseNotesLink,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
	}, nil
}
```

**Step 4: Run tests**

Run: `go test -race ./internal/checker/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/checker/sparkle.go internal/checker/sparkle_test.go
git commit -m "feat: add Sparkle appcast update checker"
```

---

### Task 6: Homebrew Cask checker

**Files:**
- Create: `internal/checker/brew.go`
- Create: `internal/checker/brew_test.go`

**Step 1: Write the failing test**

Create `internal/checker/brew_test.go`:
```go
package checker_test

import (
	"context"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

func TestBrewChecker_ParseOutdated(t *testing.T) {
	// Test parsing of brew outdated JSON output
	jsonOutput := `[
		{
			"name": "visual-studio-code",
			"installed_versions": "1.90.0",
			"current_version": "1.95.0"
		},
		{
			"name": "iterm2",
			"installed_versions": "3.5.0",
			"current_version": "3.6.6"
		}
	]`

	results := checker.ParseBrewOutdated([]byte(jsonOutput))
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results["visual-studio-code"].LatestVersion != "1.95.0" {
		t.Errorf("vscode latest = %q, want %q", results["visual-studio-code"].LatestVersion, "1.95.0")
	}
}

func TestBrewChecker_CanCheck(t *testing.T) {
	c := checker.NewBrewChecker(nil) // nil runner means no actual brew calls
	brewApp := &app.App{Source: app.SourceBrew, CaskName: "iterm2"}
	if !c.CanCheck(brewApp) {
		t.Error("should check brew app")
	}
	otherApp := &app.App{Source: app.SourceSparkle}
	if c.CanCheck(otherApp) {
		t.Error("should not check sparkle app")
	}
}

func TestBrewChecker_CheckWithMockRunner(t *testing.T) {
	outdatedJSON := `[{"name":"iterm2","installed_versions":"3.5.0","current_version":"3.6.6"}]`
	runner := &checker.MockCmdRunner{
		Output: []byte(outdatedJSON),
	}
	c := checker.NewBrewChecker(runner)
	a := &app.App{
		Name:     "iTerm",
		Version:  "3.5.0",
		Source:   app.SourceBrew,
		CaskName: "iterm2",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "3.6.6" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.6.6")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/checker/ -v -run TestBrew`
Expected: FAIL

**Step 3: Implement Homebrew checker**

Create `internal/checker/brew.go`:
```go
package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/luzhengda/updater/internal/app"
)

// CmdRunner abstracts command execution for testing.
type CmdRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// RealCmdRunner executes real commands.
type RealCmdRunner struct{}

func (r *RealCmdRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// MockCmdRunner returns predetermined output for testing.
type MockCmdRunner struct {
	Output []byte
	Err    error
}

func (m *MockCmdRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return m.Output, m.Err
}

type brewOutdatedEntry struct {
	Name              string `json:"name"`
	InstalledVersions string `json:"installed_versions"`
	CurrentVersion    string `json:"current_version"`
}

// BrewOutdatedResult holds parsed brew outdated info for one cask.
type BrewOutdatedResult struct {
	InstalledVersion string
	LatestVersion    string
}

// ParseBrewOutdated parses the JSON output of `brew outdated --cask --json`.
func ParseBrewOutdated(data []byte) map[string]*BrewOutdatedResult {
	var entries []brewOutdatedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	results := make(map[string]*BrewOutdatedResult, len(entries))
	for _, e := range entries {
		results[e.Name] = &BrewOutdatedResult{
			InstalledVersion: e.InstalledVersions,
			LatestVersion:    e.CurrentVersion,
		}
	}
	return results
}

type BrewChecker struct {
	runner CmdRunner
}

func NewBrewChecker(runner CmdRunner) *BrewChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &BrewChecker{runner: runner}
}

func (c *BrewChecker) Name() string { return "brew" }

func (c *BrewChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceBrew && a.CaskName != ""
}

func (c *BrewChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	output, err := c.runner.Run(ctx, "brew", "outdated", "--cask", "--greedy", "--json")
	if err != nil {
		return nil, fmt.Errorf("running brew outdated: %w", err)
	}

	results := ParseBrewOutdated(output)
	if r, ok := results[a.CaskName]; ok {
		return &UpdateResult{
			App:            a,
			Source:         c.Name(),
			CurrentVersion: a.Version,
			LatestVersion:  r.LatestVersion,
			HasUpdate:      true,
		}, nil
	}

	return &UpdateResult{
		App:            a,
		Source:         c.Name(),
		CurrentVersion: a.Version,
		HasUpdate:      false,
	}, nil
}

// ListInstalledCasks returns the list of installed Homebrew cask names.
func ListInstalledCasks(ctx context.Context, runner CmdRunner) (map[string]bool, error) {
	output, err := runner.Run(ctx, "brew", "list", "--cask")
	if err != nil {
		return nil, fmt.Errorf("listing casks: %w", err)
	}
	casks := make(map[string]bool)
	for _, line := range splitLines(output) {
		if line != "" {
			casks[line] = true
		}
	}
	return casks, nil
}

func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}
```

**Step 4: Run tests**

Run: `go test -race ./internal/checker/ -v -run TestBrew`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/checker/brew.go internal/checker/brew_test.go
git commit -m "feat: add Homebrew Cask update checker"
```

---

### Task 7: Mac App Store checker (via mas-cli)

**Files:**
- Create: `internal/checker/mas.go`
- Create: `internal/checker/mas_test.go`

**Step 1: Write the failing test**

Create `internal/checker/mas_test.go`:
```go
package checker_test

import (
	"context"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

func TestMASChecker_ParseOutdated(t *testing.T) {
	output := `441258766 Magnet (3.0.6 -> 3.0.7)
1176895641 Spark (3.27.8 -> 3.27.9)
`
	results := checker.ParseMASOutdated([]byte(output))
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if r, ok := results["441258766"]; !ok {
		t.Error("Magnet not found")
	} else if r.LatestVersion != "3.0.7" {
		t.Errorf("Magnet latest = %q, want %q", r.LatestVersion, "3.0.7")
	}
}

func TestMASChecker_ParseList(t *testing.T) {
	output := `441258766 Magnet          (3.0.7)
1176895641 Spark - Email App (3.27.9)
`
	results := checker.ParseMASList([]byte(output))
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if id, ok := results["com.crowdcafe.windowmagnet"]; ok {
		_ = id // MAS list maps name -> id, we test the parsing
	}
	// We actually parse it as id -> name since that's more useful
	if name, ok := results["441258766"]; !ok {
		t.Error("Magnet ID not found")
	} else if name != "Magnet" {
		t.Errorf("Magnet name = %q, want %q", name, "Magnet")
	}
}

func TestMASChecker_CanCheck(t *testing.T) {
	c := checker.NewMASChecker(nil)
	masApp := &app.App{Source: app.SourceMAS}
	if !c.CanCheck(masApp) {
		t.Error("should check MAS app")
	}
	otherApp := &app.App{Source: app.SourceBrew}
	if c.CanCheck(otherApp) {
		t.Error("should not check brew app")
	}
}

func TestMASChecker_CheckWithMock(t *testing.T) {
	runner := &checker.MockCmdRunner{
		Output: []byte("441258766 Magnet (3.0.6 -> 3.0.7)\n"),
	}
	c := checker.NewMASChecker(runner)
	a := &app.App{
		Name:    "Magnet",
		Version: "3.0.6",
		Source:  app.SourceMAS,
		MASID:   "441258766",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "3.0.7" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.0.7")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/checker/ -v -run TestMAS`
Expected: FAIL

**Step 3: Implement MAS checker**

Create `internal/checker/mas.go`:
```go
package checker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/luzhengda/updater/internal/app"
)

var masOutdatedRe = regexp.MustCompile(`^(\d+)\s+(.+?)\s+\((.+?)\s+->\s+(.+?)\)$`)
var masListRe = regexp.MustCompile(`^(\d+)\s+(.+?)\s+\((.+?)\)$`)

// MASOutdatedResult holds parsed mas outdated info.
type MASOutdatedResult struct {
	ID             string
	Name           string
	CurrentVersion string
	LatestVersion  string
}

// ParseMASOutdated parses output of `mas outdated`.
// Format: "441258766 Magnet (3.0.6 -> 3.0.7)"
func ParseMASOutdated(data []byte) map[string]*MASOutdatedResult {
	results := make(map[string]*MASOutdatedResult)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if matches := masOutdatedRe.FindStringSubmatch(line); matches != nil {
			results[matches[1]] = &MASOutdatedResult{
				ID:             matches[1],
				Name:           matches[2],
				CurrentVersion: matches[3],
				LatestVersion:  matches[4],
			}
		}
	}
	return results
}

// ParseMASList parses output of `mas list`.
// Format: "441258766 Magnet          (3.0.7)"
// Returns map of ID -> Name.
func ParseMASList(data []byte) map[string]string {
	results := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if matches := masListRe.FindStringSubmatch(line); matches != nil {
			results[matches[1]] = strings.TrimSpace(matches[2])
		}
	}
	return results
}

type MASChecker struct {
	runner CmdRunner
}

func NewMASChecker(runner CmdRunner) *MASChecker {
	if runner == nil {
		runner = &RealCmdRunner{}
	}
	return &MASChecker{runner: runner}
}

func (c *MASChecker) Name() string { return "mas" }

func (c *MASChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceMAS
}

func (c *MASChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	output, err := c.runner.Run(ctx, "mas", "outdated")
	if err != nil {
		return nil, fmt.Errorf("running mas outdated: %w", err)
	}

	results := ParseMASOutdated(output)

	// Try matching by MAS ID
	if a.MASID != "" {
		if r, ok := results[a.MASID]; ok {
			return &UpdateResult{
				App:            a,
				Source:         c.Name(),
				CurrentVersion: a.Version,
				LatestVersion:  r.LatestVersion,
				HasUpdate:      true,
			}, nil
		}
	}

	// Fallback: try matching by name
	for _, r := range results {
		if strings.EqualFold(r.Name, a.Name) {
			return &UpdateResult{
				App:            a,
				Source:         c.Name(),
				CurrentVersion: a.Version,
				LatestVersion:  r.LatestVersion,
				HasUpdate:      true,
			}, nil
		}
	}

	return &UpdateResult{
		App:            a,
		Source:         c.Name(),
		CurrentVersion: a.Version,
		HasUpdate:      false,
	}, nil
}
```

**Step 4: Run tests**

Run: `go test -race ./internal/checker/ -v -run TestMAS`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/checker/mas.go internal/checker/mas_test.go
git commit -m "feat: add Mac App Store update checker via mas-cli"
```

---

### Task 8: GitHub Releases checker

**Files:**
- Create: `internal/checker/github.go`
- Create: `internal/checker/github_test.go`

**Step 1: Write the failing test**

Create `internal/checker/github_test.go`:
```go
package checker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

const testGitHubRelease = `{
	"tag_name": "v2.0.0",
	"name": "Release 2.0.0",
	"body": "Bug fixes and improvements",
	"assets": [
		{
			"name": "TestApp-2.0.0-mac.dmg",
			"browser_download_url": "https://github.com/test/app/releases/download/v2.0.0/TestApp-2.0.0-mac.dmg",
			"content_type": "application/x-apple-diskimage"
		}
	]
}`

func TestGitHubChecker_Check(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/test/app/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testGitHubRelease))
	}))
	defer srv.Close()

	c := checker.NewGitHubChecker(http.DefaultClient, srv.URL)
	a := &app.App{
		Name:       "TestApp",
		Version:    "1.0.0",
		Source:     app.SourceGitHub,
		GitHubRepo: "test/app",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.0.0")
	}
	if result.DownloadURL == "" {
		t.Error("expected non-empty DownloadURL")
	}
}

func TestGitHubChecker_CanCheck(t *testing.T) {
	c := checker.NewGitHubChecker(http.DefaultClient, "")
	ghApp := &app.App{Source: app.SourceGitHub, GitHubRepo: "test/app"}
	if !c.CanCheck(ghApp) {
		t.Error("should check github app")
	}
	otherApp := &app.App{Source: app.SourceSparkle}
	if c.CanCheck(otherApp) {
		t.Error("should not check sparkle app")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/checker/ -v -run TestGitHub`
Expected: FAIL

**Step 3: Implement GitHub checker**

Create `internal/checker/github.go`:
```go
package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

const defaultGitHubAPI = "https://api.github.com"

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Name    string         `json:"name"`
	Body    string         `json:"body"`
	Assets  []githubAsset  `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

type GitHubChecker struct {
	client  *http.Client
	baseURL string
}

func NewGitHubChecker(client *http.Client, baseURL string) *GitHubChecker {
	if baseURL == "" {
		baseURL = defaultGitHubAPI
	}
	return &GitHubChecker{client: client, baseURL: baseURL}
}

func (c *GitHubChecker) Name() string { return "github" }

func (c *GitHubChecker) CanCheck(a *app.App) bool {
	return a.Source == app.SourceGitHub && a.GitHubRepo != ""
}

func (c *GitHubChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.baseURL, a.GitHubRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	downloadURL := findMacAsset(release.Assets)

	return &UpdateResult{
		App:            a,
		Source:         c.Name(),
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    downloadURL,
		ReleaseNotes:   release.Body,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
	}, nil
}

func findMacAsset(assets []githubAsset) string {
	macExtensions := []string{".dmg", ".pkg", "-mac.zip", "-macos.zip", "-darwin.zip", "_mac.zip", "-arm64.dmg"}
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		for _, ext := range macExtensions {
			if strings.HasSuffix(name, ext) || strings.Contains(name, "mac") || strings.Contains(name, "darwin") {
				return asset.BrowserDownloadURL
			}
		}
	}
	// Fallback: return first asset if any
	if len(assets) > 0 {
		return assets[0].BrowserDownloadURL
	}
	return ""
}
```

**Step 4: Run tests**

Run: `go test -race ./internal/checker/ -v -run TestGitHub`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/checker/github.go internal/checker/github_test.go
git commit -m "feat: add GitHub Releases update checker"
```

---

## Phase 4: Configuration

### Task 9: Config file loading

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luzhengda/updater/internal/config"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(cfgPath, []byte(`
ignored_apps:
  - com.example.ignored
github_mappings:
  com.microsoft.VSCode: "microsoft/vscode"
  com.googlecode.iterm2: "gnachman/iterm2"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !cfg.IsIgnored("com.example.ignored") {
		t.Error("expected com.example.ignored to be ignored")
	}
	if cfg.IsIgnored("com.example.other") {
		t.Error("expected com.example.other to not be ignored")
	}
	if repo := cfg.GitHubRepo("com.microsoft.VSCode"); repo != "microsoft/vscode" {
		t.Errorf("GitHub repo = %q, want %q", repo, "microsoft/vscode")
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("should not error for missing file: %v", err)
	}
	if cfg == nil {
		t.Fatal("should return default config")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -race ./internal/config/ -v`
Expected: FAIL

**Step 3: Implement config**

Create `internal/config/config.go`:
```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	IgnoredApps    []string          `yaml:"ignored_apps"`
	GitHubMappings map[string]string `yaml:"github_mappings"`

	ignoredSet map[string]bool
}

// DefaultPath returns the default config file path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "updater", "config.yaml")
}

// Load reads config from the given path. Returns default config if file doesn't exist.
func Load(path string) (*Config, error) {
	cfg := &Config{
		GitHubMappings: make(map[string]string),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.buildIgnoredSet()
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.buildIgnoredSet()
	return cfg, nil
}

func (c *Config) buildIgnoredSet() {
	c.ignoredSet = make(map[string]bool, len(c.IgnoredApps))
	for _, id := range c.IgnoredApps {
		c.ignoredSet[id] = true
	}
}

// IsIgnored returns true if the bundle ID is in the ignored list.
func (c *Config) IsIgnored(bundleID string) bool {
	return c.ignoredSet[bundleID]
}

// GitHubRepo returns the GitHub "owner/repo" for a bundle ID, if configured.
func (c *Config) GitHubRepo(bundleID string) string {
	return c.GitHubMappings[bundleID]
}
```

**Step 4: Install yaml dependency and run tests**

Run: `go get gopkg.in/yaml.v3 && go test -race ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: add YAML config with ignored apps and GitHub mappings"
```

---

## Phase 5: CLI Commands

### Task 10: `scan` command — list discovered apps

**Files:**
- Create: `cmd/updater/scan.go`

**Step 1: Implement scan command**

Create `cmd/updater/scan.go`:
```go
package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/luzhengda/updater/internal/app"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Discover installed apps and their update sources",
	RunE:  runScan,
}

func init() {
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	home, _ := os.UserHomeDir()
	apps, err := app.Discover("/Applications", home+"/Applications")
	if err != nil {
		return fmt.Errorf("discovering apps: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSOURCE\tBUNDLE ID")
	fmt.Fprintln(w, "----\t-------\t------\t---------")
	for _, a := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Version, a.Source, a.BundleID)
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\nFound %d apps\n", len(apps))
	return nil
}
```

**Step 2: Build and test manually**

Run: `go build ./cmd/updater/ && ./updater scan`
Expected: table of discovered apps

**Step 3: Commit**

```bash
git add cmd/updater/scan.go
git commit -m "feat: add scan command to list discovered apps"
```

---

### Task 11: `check` command — check for updates

**Files:**
- Create: `cmd/updater/check.go`
- Modify: `cmd/updater/main.go` (add config flag)

**Step 1: Add config flag to root command**

Update `cmd/updater/main.go` to add a `--config` flag and shared config loading.

**Step 2: Implement check command**

Create `cmd/updater/check.go`:
```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check all apps for available updates",
	RunE:  runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load config: %v\n", err)
		cfg, _ = config.Load("/dev/null/nonexistent")
	}

	home, _ := os.UserHomeDir()
	apps, err := app.Discover("/Applications", home+"/Applications")
	if err != nil {
		return fmt.Errorf("discovering apps: %w", err)
	}

	// Enrich apps with config data (GitHub mappings, brew cask names)
	ctx := context.Background()
	runner := &checker.RealCmdRunner{}
	enrichApps(ctx, apps, cfg, runner)

	// Filter ignored apps
	var filtered []*app.App
	for _, a := range apps {
		if !cfg.IsIgnored(a.BundleID) {
			filtered = append(filtered, a)
		}
	}

	// Create checkers
	httpClient := &http.Client{Timeout: 15 * time.Second}
	checkers := []checker.Checker{
		checker.NewSparkleChecker(httpClient),
		checker.NewBrewChecker(runner),
		checker.NewMASChecker(runner),
		checker.NewGitHubChecker(httpClient, ""),
	}

	// Check all apps concurrently
	results := checkAll(ctx, filtered, checkers)

	// Display results
	var updates []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate {
			updates = append(updates, r)
		}
	}

	if len(updates) == 0 {
		fmt.Println("All apps are up to date!")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCURRENT\tLATEST\tSOURCE")
	fmt.Fprintln(w, "----\t-------\t------\t------")
	for _, r := range updates {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.App.Name, r.CurrentVersion, r.LatestVersion, r.Source)
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "\n%d update(s) available\n", len(updates))

	return nil
}

func enrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) {
	// Add GitHub repos from config
	for _, a := range apps {
		if repo := cfg.GitHubRepo(a.BundleID); repo != "" {
			a.GitHubRepo = repo
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceGitHub
			}
		}
	}

	// Cross-reference with brew casks
	casks, err := checker.ListInstalledCasks(ctx, runner)
	if err == nil {
		for _, a := range apps {
			if a.Source == app.SourceUnknown {
				// Try to match app name to cask name
				caskName := app.ToCaskName(a.Name)
				if casks[caskName] {
					a.Source = app.SourceBrew
					a.CaskName = caskName
				}
			}
		}
	}
}

func checkAll(ctx context.Context, apps []*app.App, checkers []checker.Checker) []*checker.UpdateResult {
	var (
		mu      sync.Mutex
		results []*checker.UpdateResult
		wg      sync.WaitGroup
	)

	// Use a semaphore to limit concurrency
	sem := make(chan struct{}, 10)

	for _, a := range apps {
		for _, c := range checkers {
			if !c.CanCheck(a) {
				continue
			}
			wg.Add(1)
			go func(a *app.App, c checker.Checker) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				result, err := c.Check(ctx, a)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: %s check failed for %s: %v\n", c.Name(), a.Name, err)
					return
				}
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}(a, c)
			break // Only use the first matching checker per app
		}
	}

	wg.Wait()
	return results
}
```

**Step 3: Add ToCaskName helper to app package**

Add to `internal/app/app.go`:
```go
// ToCaskName converts an app display name to a likely Homebrew cask name.
// e.g., "Visual Studio Code" -> "visual-studio-code", "iTerm" -> "iterm2"
func ToCaskName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ".", "")
	return name
}
```

**Step 4: Build and test**

Run: `go build ./cmd/updater/ && ./updater check`
Expected: table of available updates (or "All apps up to date!")

**Step 5: Commit**

```bash
git add cmd/updater/check.go cmd/updater/main.go internal/app/app.go
git commit -m "feat: add check command with concurrent update checking"
```

---

### Task 12: `update` command — execute updates

**Files:**
- Create: `cmd/updater/update.go`

**Step 1: Implement update command**

Create `cmd/updater/update.go`:
```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var (
	updateAll  bool
	updateAuto bool
)

var updateCmd = &cobra.Command{
	Use:   "update [app-name]",
	Short: "Update apps with available updates",
	Long:  "Update a specific app by name, or use --all to update everything.",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateAll, "all", false, "Update all apps with available updates")
	updateCmd.Flags().BoolVar(&updateAuto, "auto", false, "Unattended auto-update mode")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if !updateAll && len(args) == 0 {
		return fmt.Errorf("specify an app name or use --all")
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		cfg, _ = config.Load("/dev/null/nonexistent")
	}

	home, _ := os.UserHomeDir()
	apps, err := app.Discover("/Applications", home+"/Applications")
	if err != nil {
		return fmt.Errorf("discovering apps: %w", err)
	}

	ctx := context.Background()
	runner := &checker.RealCmdRunner{}
	enrichApps(ctx, apps, cfg, runner)

	httpClient := &http.Client{Timeout: 15 * time.Second}
	checkers := []checker.Checker{
		checker.NewSparkleChecker(httpClient),
		checker.NewBrewChecker(runner),
		checker.NewMASChecker(runner),
		checker.NewGitHubChecker(httpClient, ""),
	}

	// Filter to target apps
	var targets []*app.App
	if updateAll {
		for _, a := range apps {
			if !cfg.IsIgnored(a.BundleID) && a.Source != app.SourceUnknown {
				targets = append(targets, a)
			}
		}
	} else {
		name := strings.Join(args, " ")
		for _, a := range apps {
			if strings.EqualFold(a.Name, name) {
				targets = append(targets, a)
				break
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("app %q not found", name)
		}
	}

	// Check for updates
	results := checkAll(ctx, targets, checkers)

	updated := 0
	for _, r := range results {
		if !r.HasUpdate {
			continue
		}
		fmt.Printf("Updating %s: %s -> %s (%s)...\n", r.App.Name, r.CurrentVersion, r.LatestVersion, r.Source)

		if err := executeUpdate(ctx, r, runner); err != nil {
			fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			continue
		}
		fmt.Printf("  Done!\n")
		updated++
	}

	if updated == 0 {
		fmt.Println("No updates to install.")
	} else {
		fmt.Printf("\n%d app(s) updated.\n", updated)
	}
	return nil
}

func executeUpdate(ctx context.Context, r *checker.UpdateResult, runner checker.CmdRunner) error {
	switch r.Source {
	case "brew":
		_, err := runner.Run(ctx, "brew", "upgrade", "--cask", r.App.CaskName)
		return err
	case "mas":
		if r.App.MASID != "" {
			_, err := runner.Run(ctx, "mas", "upgrade", r.App.MASID)
			return err
		}
		// Open App Store updates page as fallback
		_, err := runner.Run(ctx, "open", "macappstore://showUpdatesPage")
		return err
	case "sparkle", "github":
		if r.DownloadURL == "" {
			return fmt.Errorf("no download URL available")
		}
		fmt.Printf("  Download: %s\n", r.DownloadURL)
		fmt.Println("  (Auto-install for DMG/ZIP coming soon. Opening in browser...)")
		_, err := runner.Run(ctx, "open", r.DownloadURL)
		return err
	default:
		return fmt.Errorf("unsupported source: %s", r.Source)
	}
}
```

**Step 2: Build and test**

Run: `go build ./cmd/updater/ && ./updater update --help`
Expected: help output for update command

**Step 3: Commit**

```bash
git add cmd/updater/update.go
git commit -m "feat: add update command with brew/mas/sparkle/github support"
```

---

## Phase 6: Interactive TUI

### Task 13: Bubbletea TUI — app table with update checking

**Files:**
- Create: `internal/tui/tui.go`
- Create: `internal/tui/styles.go`
- Create: `cmd/updater/ui.go`

**Step 1: Create TUI styles**

Create `internal/tui/styles.go`:
```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("170")).
		MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)

	upToDateStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("34"))

	updateAvailStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("214"))

	errorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("196"))

	masStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("75"))

	sparkleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("215"))

	brewStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("34"))

	githubStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	statusBarStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		MarginTop(1)

	spinnerStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("170"))
)
```

**Step 2: Create main TUI model**

Create `internal/tui/tui.go`:
```go
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

// CheckFunc is the function signature for checking updates.
type CheckFunc func(ctx context.Context, apps []*app.App) []*checker.UpdateResult

// UpdateFunc is the function signature for executing an update.
type UpdateFunc func(ctx context.Context, result *checker.UpdateResult) error

type row struct {
	app    *app.App
	result *checker.UpdateResult
}

type Model struct {
	apps      []*app.App
	rows      []row
	cursor    int
	checking  bool
	updating  map[int]bool
	width     int
	height    int
	spinner   spinner.Model
	checkFn   CheckFunc
	updateFn  UpdateFunc
	statusMsg string
}

type checkDoneMsg struct {
	results []*checker.UpdateResult
}

type updateDoneMsg struct {
	index int
	err   error
}

func NewModel(apps []*app.App, checkFn CheckFunc, updateFn UpdateFunc) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	rows := make([]row, len(apps))
	for i, a := range apps {
		rows[i] = row{app: a}
	}

	return Model{
		apps:     apps,
		rows:     rows,
		checking: true,
		updating: make(map[int]bool),
		spinner:  s,
		checkFn:  checkFn,
		updateFn: updateFn,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.startCheck(),
	)
}

func (m Model) startCheck() tea.Cmd {
	return func() tea.Msg {
		results := m.checkFn(context.Background(), m.apps)
		return checkDoneMsg{results: results}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			return m, m.updateSelected()
		case "a":
			return m, m.updateAll()
		case "r":
			m.checking = true
			m.statusMsg = ""
			return m, tea.Batch(m.spinner.Tick, m.startCheck())
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case checkDoneMsg:
		m.checking = false
		resultMap := make(map[string]*checker.UpdateResult)
		for _, r := range msg.results {
			resultMap[r.App.BundleID] = r
		}
		for i, r := range m.rows {
			if result, ok := resultMap[r.app.BundleID]; ok {
				m.rows[i].result = result
			}
		}
		updateCount := 0
		for _, r := range m.rows {
			if r.result != nil && r.result.HasUpdate {
				updateCount++
			}
		}
		m.statusMsg = fmt.Sprintf("Check complete. %d update(s) available.", updateCount)

	case updateDoneMsg:
		delete(m.updating, msg.index)
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Error updating %s: %v", m.rows[msg.index].app.Name, msg.err)
		} else {
			m.statusMsg = fmt.Sprintf("Updated %s!", m.rows[msg.index].app.Name)
			if m.rows[msg.index].result != nil {
				m.rows[msg.index].result.HasUpdate = false
			}
		}
	}

	return m, nil
}

func (m Model) updateSelected() tea.Cmd {
	i := m.cursor
	r := m.rows[i]
	if r.result == nil || !r.result.HasUpdate || m.updating[i] {
		return nil
	}
	m.updating[i] = true
	return func() tea.Msg {
		err := m.updateFn(context.Background(), r.result)
		return updateDoneMsg{index: i, err: err}
	}
}

func (m Model) updateAll() tea.Cmd {
	var cmds []tea.Cmd
	for i, r := range m.rows {
		if r.result != nil && r.result.HasUpdate && !m.updating[i] {
			m.updating[i] = true
			idx := i
			result := r.result
			cmds = append(cmds, func() tea.Msg {
				err := m.updateFn(context.Background(), result)
				return updateDoneMsg{index: idx, err: err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("macOS App Updater"))
	b.WriteString("\n\n")

	if m.checking {
		b.WriteString(m.spinner.View() + " Checking for updates...\n\n")
	}

	// Header
	header := fmt.Sprintf("  %-30s %-15s %-15s %-10s %s", "NAME", "CURRENT", "LATEST", "SOURCE", "STATUS")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Rows
	visibleRows := m.height - 8
	if visibleRows < 5 {
		visibleRows = 20
	}
	start := 0
	if m.cursor >= visibleRows {
		start = m.cursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(m.rows) {
		end = len(m.rows)
	}

	for i := start; i < end; i++ {
		r := m.rows[i]
		latest := "-"
		status := ""
		sourceStyled := ""

		switch r.app.Source {
		case app.SourceMAS:
			sourceStyled = masStyle.Render("mas")
		case app.SourceSparkle:
			sourceStyled = sparkleStyle.Render("sparkle")
		case app.SourceBrew:
			sourceStyled = brewStyle.Render("brew")
		case app.SourceGitHub:
			sourceStyled = githubStyle.Render("github")
		default:
			sourceStyled = "unknown"
		}

		if m.updating[i] {
			status = spinnerStyle.Render("updating...")
		} else if r.result != nil {
			if r.result.HasUpdate {
				latest = r.result.LatestVersion
				status = updateAvailStyle.Render("UPDATE")
			} else {
				latest = r.app.Version
				status = upToDateStyle.Render("ok")
			}
			if r.result.Error != nil {
				status = errorStyle.Render("error")
			}
		} else if !m.checking {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("skipped")
		}

		line := fmt.Sprintf("  %-30s %-15s %-15s %-10s %s",
			truncate(r.app.Name, 28),
			truncate(r.app.Version, 13),
			truncate(latest, 13),
			sourceStyled,
			status,
		)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	// Status bar
	b.WriteString("\n")
	if m.statusMsg != "" {
		b.WriteString(statusBarStyle.Render(m.statusMsg))
		b.WriteString("\n")
	}
	b.WriteString(statusBarStyle.Render("j/k: navigate | enter: update | a: update all | r: refresh | q: quit"))

	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
```

**Step 3: Create UI command**

Create `cmd/updater/ui.go`:
```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/luzhengda/updater/internal/tui"
	"github.com/spf13/cobra"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch interactive TUI",
	RunE:  runUI,
}

func init() {
	rootCmd.AddCommand(uiCmd)
}

func runUI(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		cfg, _ = config.Load("/dev/null/nonexistent")
	}

	home, _ := os.UserHomeDir()
	apps, err := app.Discover("/Applications", home+"/Applications")
	if err != nil {
		return fmt.Errorf("discovering apps: %w", err)
	}

	ctx := context.Background()
	runner := &checker.RealCmdRunner{}
	enrichApps(ctx, apps, cfg, runner)

	// Filter ignored and unknown
	var filtered []*app.App
	for _, a := range apps {
		if !cfg.IsIgnored(a.BundleID) && a.Source != app.SourceUnknown {
			filtered = append(filtered, a)
		}
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	checkers := []checker.Checker{
		checker.NewSparkleChecker(httpClient),
		checker.NewBrewChecker(runner),
		checker.NewMASChecker(runner),
		checker.NewGitHubChecker(httpClient, ""),
	}

	checkFn := func(ctx context.Context, apps []*app.App) []*checker.UpdateResult {
		return checkAll(ctx, apps, checkers)
	}
	updateFn := func(ctx context.Context, result *checker.UpdateResult) error {
		return executeUpdate(ctx, result, runner)
	}

	model := tui.NewModel(filtered, checkFn, updateFn)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
```

**Step 4: Install dependencies and build**

Run:
```bash
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/lipgloss
go get github.com/charmbracelet/bubbles
go build ./cmd/updater/
```
Expected: builds successfully

**Step 5: Test TUI manually**

Run: `./updater ui`
Expected: interactive TUI with app list, update checking

**Step 6: Commit**

```bash
git add internal/tui/ cmd/updater/ui.go go.mod go.sum
git commit -m "feat: add interactive Bubbletea TUI with app table and update actions"
```

---

## Phase 7: Integration Testing

### Task 14: End-to-end verification

**Step 1: Test scan command against real system**

Run: `./updater scan`
Verify: Shows real apps with correct classification

**Step 2: Test check command**

Run: `./updater check`
Verify: Checks Sparkle feeds, shows updates if available

**Step 3: Test TUI**

Run: `./updater ui`
Verify: Interactive table, can navigate, shows update status

**Step 4: Test with a real Sparkle app (iTerm)**

iTerm2 has Sparkle with feed URL `https://iterm2.com/appcasts/final_modern.xml`.
Verify the checker correctly parses the feed and detects the version.

**Step 5: Test brew checker**

Run: `brew outdated --cask --greedy`
Compare output with `./updater check` results for brew apps.

**Step 6: Final commit**

```bash
git add -A
git commit -m "test: verify end-to-end integration with real apps"
```
