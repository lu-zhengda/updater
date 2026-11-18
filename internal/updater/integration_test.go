package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestStandaloneSparkleStaleKeepsChannel(t *testing.T) {
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
	if result.Error == nil || result.Source != "sparkle" || result.HasUpdate {
		t.Fatalf("expected stale Sparkle error, got %+v", result)
	}
}

func TestFallthrough_UnclassifiedAppWithGitHubMapping_ToBrewInfo(t *testing.T) {
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
		Name:     "MissingApp",
		BundleID: "com.example.missing-app",
		Version:  "1.0.0",
		Source:   app.SourceUnknown,
	}
	cfg := &config.Config{
		GitHubMappings: map[string]string{a.BundleID: "example/missing-app"},
		CaskMappings:   map[string]string{a.BundleID: "missing-app"},
	}
	if _, err := EnrichApps(context.Background(), []*app.App{a}, cfg, mockRunnerWithNoBrew()); err != nil {
		t.Fatal(err)
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

func TestAppStoreReceiptKeepsChannel(t *testing.T) {
	ctx := context.Background()
	caskroom, bundle := mockCaskApp(t, "example", "Example.app")
	contents := filepath.Join(bundle, "Contents")
	receiptDir := filepath.Join(contents, "_MASReceipt")
	if err := os.MkdirAll(receiptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptDir, "receipt"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		json.NewEncoder(w).Encode(checker.GitHubRelease{TagName: "v1.2.0", Assets: []checker.GitHubAsset{
			{Name: "example-mac.zip", DownloadURL: "https://example.com/example-mac.zip"},
		}})
	}))
	defer server.Close()
	info := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.app</string>
<key>CFBundleShortVersionString</key><string>1.0.0</string>
<key>SUFeedURL</key><string>%s/appcast.xml</string>
</dict></plist>`, server.URL)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}

	storeErr := errors.New("mas unavailable")
	for _, tc := range []struct {
		name         string
		mas          checker.MockResponse
		override     bool
		wantSource   string
		wantVersion  string
		wantUpdate   bool
		wantRequests int32
	}{
		{name: "current", wantSource: "mas", wantVersion: "1.0.0"},
		{name: "update", mas: checker.MockResponse{Output: []byte("123 Example (1.0.0 -> 1.1.0)\n")}, wantSource: "mas", wantVersion: "1.1.0", wantUpdate: true},
		{name: "unavailable", mas: checker.MockResponse{Err: storeErr}, wantSource: "mas"},
		{name: "explicit switch", mas: checker.MockResponse{Err: storeErr}, override: true, wantSource: "github", wantVersion: "1.2.0", wantUpdate: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)
			apps, err := app.Discover(filepath.Dir(bundle))
			if err != nil || len(apps) != 1 || apps[0].Source != app.SourceMAS {
				t.Fatalf("expected an App Store install, got %+v, %v", apps, err)
			}
			runner := &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
				"brew --caskroom":                      {Output: []byte(caskroom)},
				"brew outdated --cask --greedy --json": {Output: []byte(`[{"name":"example","current_version":"1.3.0"}]`)},
				"mas outdated":                         tc.mas,
			}}
			cfg := &config.Config{
				GitHubMappings: map[string]string{"com.example.app": "example/app"},
				CaskMappings:   map[string]string{"com.example.app": "example"},
			}
			if tc.override {
				cfg.SourceOverrides = map[string]*config.SourceOverrideConfig{
					"com.example.app": {Kind: config.SourceOverrideKindGitHub, Repo: "example/app"},
				}
			}
			if _, err := EnrichApps(ctx, apps, cfg, runner); err != nil {
				t.Fatal(err)
			}
			checks := BuildCheckers(runner, "")
			for i, c := range checks {
				switch c.Name() {
				case "sparkle":
					checks[i] = checker.NewSparkleChecker(server.Client())
				case "github":
					checks[i] = checker.NewGitHubChecker(server.Client(), server.URL, "")
				}
			}
			results := CheckAll(ctx, apps, checks, 1)
			if len(results) != 1 {
				t.Fatalf("expected one result, got %d", len(results))
			}
			r := results[0]
			wantErr := tc.mas.Err
			if tc.override {
				wantErr = nil
			}
			if r.Source != tc.wantSource || r.LatestVersion != tc.wantVersion || r.HasUpdate != tc.wantUpdate ||
				!errors.Is(r.Error, wantErr) || r.SourceOverrideActive != tc.override || requests.Load() != tc.wantRequests {
				t.Fatalf("unexpected update route %+v, requests to alternate sources %d", r, requests.Load())
			}
		})
	}
}

func TestHomebrewKeepsChannelForExactBundle(t *testing.T) {
	caskroom, bundle := mockCaskApp(t, "example", "Example.app")
	standalone := filepath.Join(t.TempDir(), "Example.app")
	if err := os.Mkdir(standalone, 0o755); err != nil {
		t.Fatal(err)
	}
	sparkle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item>
<enclosure url="https://example.com/Example.dmg" sparkle:shortVersionString="1.1.0" sparkle:version="110" />
</item></channel></rss>`)
	}))
	defer sparkle.Close()
	apps := []*app.App{
		{Name: "Example", Path: bundle, Source: app.SourceSparkle, FeedURL: sparkle.URL, Version: "1.0.0"},
		{Name: "Example", Path: standalone, Source: app.SourceSparkle, FeedURL: sparkle.URL, Version: "1.0.0"},
	}
	runner := &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
		"brew --caskroom":                      {Output: []byte(caskroom)},
		"brew outdated --cask --greedy --json": {Err: errors.New("brew outdated unavailable")},
		"brew info --cask --json=v2 example":   {Output: []byte(`{"casks":[{"version":"1.2.0","url":"https://example.com/cask.zip"}]}`)},
	}}
	ctx := context.Background()
	if _, err := EnrichApps(ctx, apps, &config.Config{}, runner); err != nil {
		t.Fatal(err)
	}
	checks := []checker.Checker{checker.NewSparkleChecker(sparkle.Client()), checker.NewBrewChecker(runner), checker.NewBrewInfoChecker(runner)}
	managed := CheckWithFallthrough(ctx, apps[0], checks)
	if managed.Error != nil || managed.Source != "brew-info" || !managed.HasUpdate || !managed.App.InstalledViaBrew {
		t.Fatalf("cask should fall back within Homebrew, got %+v", managed)
	}
	direct := CheckWithFallthrough(ctx, apps[1], checks)
	if direct.Error != nil || direct.Source != "sparkle" || !direct.HasUpdate || direct.App.InstalledViaBrew {
		t.Fatalf("standalone copy should stay on its feed, got %+v", direct)
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
