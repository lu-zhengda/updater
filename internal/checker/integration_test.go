package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestRealCmdRunner_Run(t *testing.T) {
	r := &RealCmdRunner{}
	output, err := r.Run(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.TrimSpace(string(output))
	if got != "hello" {
		t.Errorf("output = %q, want %q", got, "hello")
	}
}

func TestRealCmdRunner_RunError(t *testing.T) {
	r := &RealCmdRunner{}
	_, err := r.Run(context.Background(), "nonexistent-command-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent command, got nil")
	}
}

func TestMultiMockCmdRunner_Run(t *testing.T) {
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"brew outdated --cask --greedy --json": {
				Output: []byte(`[{"name":"firefox","current_version":"2.0"}]`),
			},
			"mas outdated": {
				Output: []byte("441258766 Magnet (3.0.6 -> 3.0.7)\n"),
			},
			"failing-cmd": {
				Err: fmt.Errorf("command failed"),
			},
		},
	}

	t.Run("matching key with args", func(t *testing.T) {
		output, err := runner.Run(context.Background(), "brew", "outdated", "--cask", "--greedy", "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(output), "firefox") {
			t.Errorf("expected output to contain 'firefox', got %q", string(output))
		}
	})

	t.Run("matching key without extra args", func(t *testing.T) {
		output, err := runner.Run(context.Background(), "mas", "outdated")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(string(output), "Magnet") {
			t.Errorf("expected output to contain 'Magnet', got %q", string(output))
		}
	})

	t.Run("matching key with error", func(t *testing.T) {
		_, err := runner.Run(context.Background(), "failing-cmd")
		if err == nil {
			t.Fatal("expected error for failing command, got nil")
		}
	})

	t.Run("no match falls back to nil", func(t *testing.T) {
		output, err := runner.Run(context.Background(), "unknown-command", "arg1")
		if err != nil {
			t.Fatalf("expected nil error for unmatched key, got %v", err)
		}
		if output != nil {
			t.Errorf("expected nil output for unmatched key, got %q", string(output))
		}
	})

	t.Run("command with no args", func(t *testing.T) {
		// Test that a key with just the name (no args) works.
		output, err := runner.Run(context.Background(), "failing-cmd")
		if err == nil {
			t.Fatal("expected error")
		}
		_ = output
	})
}

func TestSparkle_EndToEnd(t *testing.T) {
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		xml := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <item>
      <title>Version 3.0.0</title>
      <enclosure url="` + serverURL + `/download/app.dmg" length="5000" type="application/octet-stream"
        sparkle:version="300" sparkle:shortVersionString="3.0.0" />
    </item>
  </channel>
</rss>`
		w.Write([]byte(xml))
	}))
	defer ts.Close()
	serverURL = ts.URL

	sc := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "IntegrationApp",
		Version: "2.0.0",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := sc.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("SparkleChecker.Check failed: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "3.0.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.0.0")
	}
	if result.DownloadURL != serverURL+"/download/app.dmg" {
		t.Errorf("DownloadURL = %q, want %q", result.DownloadURL, serverURL+"/download/app.dmg")
	}
	if result.Source != "sparkle" {
		t.Errorf("Source = %q, want %q", result.Source, "sparkle")
	}
}

func TestGitHub_EndToEnd(t *testing.T) {
	release := GitHubRelease{
		TagName: "v4.2.0",
		Name:    "Release 4.2.0",
		Body:    "Bug fixes and improvements",
		Assets: []GitHubAsset{
			{Name: "app-linux-amd64.tar.gz", DownloadURL: "https://example.com/linux.tar.gz"},
			{Name: "app-macos-arm64.dmg", DownloadURL: "https://example.com/mac.dmg"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/app/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer ts.Close()

	gc := NewGitHubChecker(ts.Client(), ts.URL, "")
	a := &app.App{
		Name:       "TestApp",
		Version:    "4.0.0",
		Source:     app.SourceGitHub,
		GitHubRepo: "example/app",
	}

	result, err := gc.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("GitHubChecker.Check failed: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "4.2.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "4.2.0")
	}
	if result.DownloadURL != "https://example.com/mac.dmg" {
		t.Errorf("DownloadURL = %q, want %q", result.DownloadURL, "https://example.com/mac.dmg")
	}
	if result.ReleaseNotes != "Bug fixes and improvements" {
		t.Errorf("ReleaseNotes = %q, want %q", result.ReleaseNotes, "Bug fixes and improvements")
	}
}

func TestSparkle_MultipleItems_OSFiltering(t *testing.T) {
	orig := getMacOSVersionFn
	getMacOSVersionFn = func() string { return "15.0" }
	defer func() { getMacOSVersionFn = orig }()

	xml := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <item>
      <title>Version 4.0 (macOS 16+)</title>
      <sparkle:minimumSystemVersion>16.0</sparkle:minimumSystemVersion>
      <enclosure url="https://example.com/v4.dmg" sparkle:shortVersionString="4.0.0" sparkle:version="400" />
    </item>
    <item>
      <title>Version 3.0 (macOS 14+)</title>
      <sparkle:minimumSystemVersion>14.0</sparkle:minimumSystemVersion>
      <sparkle:maximumSystemVersion>15.99</sparkle:maximumSystemVersion>
      <enclosure url="https://example.com/v3.dmg" sparkle:shortVersionString="3.0.0" sparkle:version="300" />
    </item>
    <item>
      <title>Version 2.0 (macOS 12+)</title>
      <sparkle:minimumSystemVersion>12.0</sparkle:minimumSystemVersion>
      <sparkle:maximumSystemVersion>14.99</sparkle:maximumSystemVersion>
      <enclosure url="https://example.com/v2.dmg" sparkle:shortVersionString="2.0.0" sparkle:version="200" />
    </item>
  </channel>
</rss>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(xml))
	}))
	defer ts.Close()

	sc := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "MultiItemApp",
		Version: "2.0.0",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := sc.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	// macOS 15.0 should pick v3.0 (min 14.0, max 15.99), not v4.0 (min 16.0)
	if result.LatestVersion != "3.0.0" {
		t.Errorf("LatestVersion = %q, want %q (should filter by OS)", result.LatestVersion, "3.0.0")
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
}
