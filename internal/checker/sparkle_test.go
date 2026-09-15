package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestSparkleChecker_Name(t *testing.T) {
	c := NewSparkleChecker(nil)
	if got := c.Name(); got != "sparkle" {
		t.Errorf("Name() = %q, want %q", got, "sparkle")
	}
}

func TestSparkleChecker_CheckErrorPaths(t *testing.T) {
	t.Run("empty feed URL", func(t *testing.T) {
		c := NewSparkleChecker(nil)
		a := &app.App{Name: "TestApp", Version: "1.0.0"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for empty feed URL, got nil")
		}
	})

	t.Run("invalid feed URL", func(t *testing.T) {
		c := NewSparkleChecker(nil)
		a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: "://bad-url"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for invalid feed URL, got nil")
		}
	})

	t.Run("server gone (connection refused)", func(t *testing.T) {
		// Create a server, get its URL, then close it immediately.
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		feedURL := ts.URL
		ts.Close()

		c := NewSparkleChecker(ts.Client())
		a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: feedURL}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error when server is gone, got nil")
		}
	})

	t.Run("non-200 status code", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		c := NewSparkleChecker(ts.Client())
		a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: ts.URL}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for non-200 status, got nil")
		}
	})

	t.Run("invalid XML body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("this is not XML"))
		}))
		defer ts.Close()

		c := NewSparkleChecker(ts.Client())
		a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: ts.URL}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for invalid XML, got nil")
		}
	})

	t.Run("empty items in feed", func(t *testing.T) {
		emptyXML := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>Empty Feed</title>
  </channel>
</rss>`
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(emptyXML))
		}))
		defer ts.Close()

		c := NewSparkleChecker(ts.Client())
		a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: ts.URL}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for empty feed items, got nil")
		}
	})
}

func TestSparkleChecker_MacOS27Compatibility(t *testing.T) {
	orig := getMacOSVersionFn
	getMacOSVersionFn = func() string { return "27.0" }
	t.Cleanup(func() { getMacOSVersionFn = orig })

	for _, tt := range []struct {
		name, limits string
		wantUpdate   bool
	}{
		{"older macOS only", "<sparkle:maximumSystemVersion>26.99</sparkle:maximumSystemVersion>", false},
		{"future macOS only", "<sparkle:minimumSystemVersion>28.0</sparkle:minimumSystemVersion>", false},
		{"macOS 27 minimum", "<sparkle:minimumSystemVersion>27.0</sparkle:minimumSystemVersion>", true},
		{"macOS 27 maximum", "<sparkle:maximumSystemVersion>27.0</sparkle:maximumSystemVersion>", true},
		{"unrestricted", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item>` + tt.limits +
					`<enclosure url="https://example.com/v2.dmg" sparkle:shortVersionString="2.0.0" /></item></channel></rss>`))
			}))
			defer ts.Close()
			a := &app.App{Name: "TestApp", Version: "1.0.0", FeedURL: ts.URL}
			result, err := NewSparkleChecker(ts.Client()).Check(context.Background(), a)
			if err != nil {
				t.Fatal(err)
			}
			if result.HasUpdate != tt.wantUpdate {
				t.Fatalf("HasUpdate = %v, want %v", result.HasUpdate, tt.wantUpdate)
			}
			if !tt.wantUpdate && (result.DownloadURL != "" || result.StaleSource || result.LatestVersion != a.Version) {
				t.Fatalf("incompatible feed must not offer a download or trigger stale-source fallback: %+v", result)
			}
			if tt.wantUpdate && (result.LatestVersion != "2.0.0" || result.DownloadURL == "") {
				t.Fatalf("compatible release missing: %+v", result)
			}
		})
	}
}

func TestFindBestItem_MaxSystemVersionFilters(t *testing.T) {
	// Item with maxSystemVersion lower than current OS should be filtered out.
	orig := getMacOSVersionFn
	getMacOSVersionFn = func() string { return "16.0" }
	defer func() { getMacOSVersionFn = orig }()

	items := []sparkleItem{
		{
			Title:            "Version 2.0 (old macOS only)",
			MaxSystemVersion: "14.99",
			Enclosure: sparkleEnclosure{
				ShortVersionString: "2.0.0",
				URL:                "https://example.com/v2.dmg",
			},
		},
		{
			Title: "Version 3.0 (universal)",
			Enclosure: sparkleEnclosure{
				ShortVersionString: "3.0.0",
				URL:                "https://example.com/v3.dmg",
			},
		},
	}

	result := findBestItem(items, getMacOSVersionFn())
	// Item 1 (v2.0) has maxSystemVersion=14.99, we're on 16.0, so it should be skipped.
	// Item 2 (v3.0) has no restrictions, should be selected.
	if result.Enclosure.ShortVersionString != "3.0.0" {
		t.Errorf("expected v3.0.0 (maxSystemVersion filtered v2.0.0), got %q", result.Enclosure.ShortVersionString)
	}
}

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

func TestSparkleChecker_StaleFeed(t *testing.T) {
	// Simulate PDF Expert scenario: installed v3.11.1 but feed returns v2.5.22.
	staleXML := `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <item>
      <title>Version 2.5.22</title>
      <enclosure url="https://example.com/app-2.5.22.dmg"
          sparkle:shortVersionString="2.5.22"
          sparkle:version="2522"
          length="1234"
          type="application/octet-stream" />
    </item>
  </channel>
</rss>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(staleXML))
	}))
	defer ts.Close()

	checker := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "PDF Expert",
		Version: "3.11.1",
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false (feed is stale)")
	}
	if !result.StaleSource {
		t.Error("expected StaleSource to be true (feed version 2.5.22 < installed 3.11.1)")
	}
	if result.LatestVersion != "2.5.22" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.5.22")
	}
}

func TestSparkleChecker_NotStaleWhenUpToDate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(testAppcastXML))
	}))
	defer ts.Close()

	checker := NewSparkleChecker(ts.Client())
	a := &app.App{
		Name:    "TestApp",
		Version: "2.0.0", // same as feed
		Source:  app.SourceSparkle,
		FeedURL: ts.URL,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StaleSource {
		t.Error("expected StaleSource to be false when versions match")
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
