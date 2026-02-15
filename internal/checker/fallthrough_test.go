package checker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/updater"
)

func TestFallthrough_SparkleStale_ToBrewInfo(t *testing.T) {
	// Sparkle: returns stale version (1.0 < installed 2.0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		xml := `<?xml version="1.0"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel><item>
    <enclosure url="https://example.com/v1.dmg" sparkle:shortVersionString="1.0.0" sparkle:version="100" />
  </item></channel>
</rss>`
		w.Write([]byte(xml))
	}))
	defer ts.Close()

	// App with feed URL pointing to stale Sparkle AND a CaskName for BrewInfo fallback
	a := &app.App{
		Name:     "FallthroughApp",
		Version:  "2.0.0",
		FeedURL:  ts.URL,
		CaskName: "fallthrough-app",
		Source:   app.SourceSparkle,
	}

	// Mock BrewInfoChecker: `brew info --cask --json=v2 fallthrough-app` returns newer version
	mockRunner := &checker.MockCmdRunner{
		Output: []byte(`{"casks":[{"version":"3.0.0","token":"fallthrough-app"}]}`),
	}

	checkers := []checker.Checker{
		checker.NewSparkleChecker(ts.Client()),
		checker.NewBrewInfoChecker(mockRunner),
	}

	result := updater.CheckWithFallthrough(context.Background(), a, checkers)
	if result.Source != "brew-info" {
		t.Errorf("Source = %q, want %q (should fall through from stale sparkle)", result.Source, "brew-info")
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true from BrewInfo fallback")
	}
	if result.LatestVersion != "3.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.0.0")
	}
}
