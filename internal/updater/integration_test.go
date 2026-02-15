package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

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
