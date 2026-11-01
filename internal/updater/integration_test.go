package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/installer"
)

func TestIntegration_GitHubVersionOverride(t *testing.T) {
	for _, tc := range []struct {
		name, fields  string
		wantDownloads int32
	}{
		{"bundle", "version_source: bundle", 1},
		{"tag", "version_source: tag\n    version_pattern: '^0\\.(\\d+\\.\\d+)-alpha$'\n    version_replacement: '1.${1}'", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var archive bytes.Buffer
			zw := zip.NewWriter(&archive)
			entry, err := zw.Create("Renamed.app/Contents/Info.plist")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string><key>CFBundleShortVersionString</key><string>1.1.33</string></dict></plist>`)); err != nil {
				t.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(archive.Bytes())
			digest := fmt.Sprintf("sha256:%x", sum)
			var downloads atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/app-mac.zip" {
					downloads.Add(1)
					w.Write(archive.Bytes())
					return
				}
				json.NewEncoder(w).Encode(checker.GitHubRelease{TagName: "v0.1.33-alpha", Assets: []checker.GitHubAsset{
					{Name: "app-mac.zip", DownloadURL: server.URL + "/app-mac.zip", Digest: digest},
				}})
			}))
			defer server.Close()
			cfg, err := config.Parse([]byte("source_overrides:\n  com.example.app:\n    kind: github\n    repo: example/app\n    " + tc.fields + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			a := &app.App{Name: "Example", BundleID: "com.example.app", Version: "1.1.32", Source: app.SourceUnknown}
			runner := mockRunnerWithNoBrew()
			if _, err := EnrichApps(context.Background(), []*app.App{a}, cfg, runner); err != nil {
				t.Fatal(err)
			}
			g := checker.NewGitHubChecker(server.Client(), server.URL, "")
			g.ReadBundleVersion = installer.New(runner, server.Client()).ReadBundleVersion
			result := CheckWithFallthrough(context.Background(), a, []checker.Checker{g})
			if result.Error != nil || !result.HasUpdate || result.IsMajorUpdate || result.LatestVersion != "1.1.33" || result.CurrentVersion != "1.1.32" || !result.SourceOverrideActive {
				t.Fatalf("unexpected result: %#v", result)
			}
			if result.DownloadURL != server.URL+"/app-mac.zip" || result.DownloadDigest != digest {
				t.Fatalf("changed asset identity: %#v", result)
			}
			if downloads.Load() != tc.wantDownloads {
				t.Fatalf("downloads = %d, want %d", downloads.Load(), tc.wantDownloads)
			}

			// Reusing a discovered app after config changes must not retain the strategy.
			cfg.SourceOverrides["com.example.app"] = &config.SourceOverrideConfig{Kind: config.SourceOverrideKindGitHub, Repo: "example/app"}
			if _, err := EnrichApps(context.Background(), []*app.App{a}, cfg, runner); err != nil {
				t.Fatal(err)
			}
			result = CheckWithFallthrough(context.Background(), a, []checker.Checker{g})
			if result.Error != nil || result.HasUpdate || result.LatestVersion != "0.1.33-alpha" || downloads.Load() != tc.wantDownloads {
				t.Fatalf("disabled strategy still applied: %#v", result)
			}
		})
	}
}

func TestFallthrough_SparkleStale_ToBrewInfo(t *testing.T) {
	// Sparkle feed returns a version older than installed (stale).
	staleXML := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <item>
      <enclosure url="https://example.com/old.dmg" sparkle:shortVersionString="2.0.0" sparkle:version="200" />
    </item>
  </channel>
</rss>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(staleXML))
	}))
	defer ts.Close()

	// BrewInfo mock returns version 4.0.0
	runner := &checker.MockCmdRunner{
		Output: []byte(`{"casks":[{"token":"test-app","version":"4.0.0"}]}`),
	}

	a := &app.App{
		Name:     "StaleApp",
		Version:  "3.0.0", // installed v3, sparkle says v2 (stale)
		Source:   app.SourceSparkle,
		FeedURL:  ts.URL,
		CaskName: "test-app",
	}

	checkers := []checker.Checker{
		checker.NewSparkleChecker(ts.Client()),
		checker.NewBrewInfoChecker(runner),
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "brew-info" {
		t.Errorf("Source = %q, want %q (should fallthrough from stale sparkle)", result.Source, "brew-info")
	}
	if result.LatestVersion != "4.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "4.0.0")
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
}

func TestFallthrough_GitHub404_ToBrewInfo(t *testing.T) {
	// GitHub returns 404.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()

	// BrewInfo mock returns version 2.0.0
	runner := &checker.MockCmdRunner{
		Output: []byte(`{"casks":[{"token":"missing-app","version":"2.0.0"}]}`),
	}

	a := &app.App{
		Name:       "MissingApp",
		Version:    "1.0.0",
		Source:     app.SourceGitHub,
		GitHubRepo: "example/missing-app",
		CaskName:   "missing-app",
	}

	checkers := []checker.Checker{
		checker.NewGitHubChecker(ts.Client(), ts.URL, ""),
		checker.NewBrewInfoChecker(runner),
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "brew-info" {
		t.Errorf("Source = %q, want %q (should fallthrough from GitHub 404)", result.Source, "brew-info")
	}
	if result.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.0.0")
	}
}

func TestIntegration_ExplicitGitHubOverride_SuppressesFallbackOnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()

	runner := &checker.MockCmdRunner{
		Output: []byte(`{"casks":[{"token":"missing-app","version":"2.0.0"}]}`),
	}

	a := &app.App{
		Name:                 "MissingApp",
		Version:              "1.0.0",
		Source:               app.SourceGitHub,
		GitHubRepo:           "example/missing-app",
		CaskName:             "missing-app",
		SourceOverrideActive: true,
		SourceOverrideKind:   "github",
	}

	checkers := []checker.Checker{
		checker.NewGitHubChecker(ts.Client(), ts.URL, ""),
		checker.NewBrewInfoChecker(runner),
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatalf("expected pinned github error result, got %#v", result)
	}
	if result.Source != "github" {
		t.Fatalf("Source = %q, want %q", result.Source, "github")
	}
	if result.LatestVersion != "" {
		t.Fatalf("LatestVersion = %q, want empty because brew-info fallback must be suppressed", result.LatestVersion)
	}
	if !result.SourceOverrideActive || result.SourceOverrideKind != "github" {
		t.Fatalf("expected override provenance on result, got %#v", result)
	}
}
