package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

const testAppcastXML = `<?xml version="1.0" encoding="utf-8"?>
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
  </channel>
</rss>`

func TestSparkleChecker_Check(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testAppcastXML))
	}))
	defer ts.Close()

	checker := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "TestApp",
		Version: "1.0.0",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "2.0.0" {
		t.Errorf("expected LatestVersion 2.0.0, got %s", result.LatestVersion)
	}
	if result.DownloadURL != "https://example.com/app.dmg" {
		t.Errorf("expected DownloadURL https://example.com/app.dmg, got %s", result.DownloadURL)
	}
	if result.CurrentVersion != "1.0.0" {
		t.Errorf("expected CurrentVersion 1.0.0, got %s", result.CurrentVersion)
	}
	if result.ReleaseNotes != "https://example.com/notes.html" {
		t.Errorf("expected ReleaseNotes link, got %s", result.ReleaseNotes)
	}
	if result.Source != "sparkle" {
		t.Errorf("expected Source sparkle, got %s", result.Source)
	}
}

func TestSparkleChecker_NoUpdate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(testAppcastXML))
	}))
	defer ts.Close()

	checker := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "TestApp",
		Version: "2.0.0",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}

// testAppcastEnclosureAttrs mimics real-world feeds (iTerm2, PDF Expert) where
// version info is on enclosure attributes, not child elements.
const testAppcastEnclosureAttrs = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>TestApp Updates</title>
    <item>
      <title>Version 3.6.6</title>
      <sparkle:releaseNotesLink>https://example.com/notes.txt</sparkle:releaseNotesLink>
      <pubDate>Mon, 17 Nov 2025 18:53:41 -0800</pubDate>
      <sparkle:minimumSystemVersion>12.4</sparkle:minimumSystemVersion>
      <enclosure url="https://example.com/app-3.6.6.zip"
          sparkle:version="3.6.6"
          sparkle:shortVersionString="3.6.6"
          length="52976511"
          type="application/octet-stream" />
    </item>
  </channel>
</rss>`

func TestSparkleChecker_EnclosureAttributes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testAppcastEnclosureAttrs))
	}))
	defer ts.Close()

	checker := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "iTerm2",
		Version: "3.5.0",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "3.6.6" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.6.6")
	}
	if result.DownloadURL != "https://example.com/app-3.6.6.zip" {
		t.Errorf("DownloadURL = %q, want %q", result.DownloadURL, "https://example.com/app-3.6.6.zip")
	}
}

func TestSparkleChecker_CanCheck(t *testing.T) {
	checker := NewSparkleChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "sparkle app with feed URL",
			app:  &app.App{Source: app.SourceSparkle, FeedURL: "https://example.com/appcast.xml"},
			want: true,
		},
		{
			name: "MAS app",
			app:  &app.App{Source: app.SourceMAS},
			want: false,
		},
		{
			name: "sparkle source without feed URL",
			app:  &app.App{Source: app.SourceSparkle},
			want: false,
		},
		{
			name: "unknown source with feed URL",
			app:  &app.App{Source: app.SourceUnknown, FeedURL: "https://example.com/appcast.xml"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checker.CanCheck(tt.app)
			if got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}
