package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

const testReleaseJSON = `{
  "tag_name": "v2.0.0",
  "name": "Release 2.0.0",
  "body": "Bug fixes",
  "assets": [
    {
      "name": "TestApp-2.0.0-mac.dmg",
      "browser_download_url": "https://github.com/test/app/releases/download/v2.0.0/TestApp-2.0.0-mac.dmg"
    }
  ]
}`

func TestGitHubChecker_Check(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/test/app/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testReleaseJSON))
	}))
	defer ts.Close()

	checker := NewGitHubChecker(ts.Client(), ts.URL)
	a := &app.App{
		Name:       "TestApp",
		Version:    "1.0.0",
		Source:     app.SourceGitHub,
		GitHubRepo: "test/app",
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
	if result.CurrentVersion != "1.0.0" {
		t.Errorf("expected CurrentVersion 1.0.0, got %s", result.CurrentVersion)
	}
	if result.DownloadURL != "https://github.com/test/app/releases/download/v2.0.0/TestApp-2.0.0-mac.dmg" {
		t.Errorf("unexpected DownloadURL: %s", result.DownloadURL)
	}
	if result.ReleaseNotes != "Bug fixes" {
		t.Errorf("expected ReleaseNotes 'Bug fixes', got %s", result.ReleaseNotes)
	}
	if result.Source != "github" {
		t.Errorf("expected Source github, got %s", result.Source)
	}
}

func TestGitHubChecker_CheckNoUpdate(t *testing.T) {
	releaseJSON := `{
		"tag_name": "v2.0.0",
		"name": "Release 2.0.0",
		"body": "Bug fixes",
		"assets": []
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(releaseJSON))
	}))
	defer ts.Close()

	checker := NewGitHubChecker(ts.Client(), ts.URL)
	a := &app.App{
		Name:       "TestApp",
		Version:    "2.0.0",
		Source:     app.SourceGitHub,
		GitHubRepo: "test/app",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}

func TestGitHubChecker_CanCheck(t *testing.T) {
	checker := NewGitHubChecker(nil, "")

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "GitHub app with repo",
			app:  &app.App{Source: app.SourceGitHub, GitHubRepo: "owner/repo"},
			want: true,
		},
		{
			name: "GitHub source without repo",
			app:  &app.App{Source: app.SourceGitHub},
			want: false,
		},
		{
			name: "non-GitHub app with repo",
			app:  &app.App{Source: app.SourceSparkle, GitHubRepo: "owner/repo"},
			want: true,
		},
		{
			name: "MAS app",
			app:  &app.App{Source: app.SourceMAS},
			want: false,
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

func TestGitHubChecker_FindMacAsset(t *testing.T) {
	tests := []struct {
		name   string
		assets []githubAsset
		want   string
	}{
		{
			name: "dmg with mac in name",
			assets: []githubAsset{
				{Name: "app-linux.tar.gz", DownloadURL: "https://example.com/linux.tar.gz"},
				{Name: "app-mac.dmg", DownloadURL: "https://example.com/mac.dmg"},
			},
			want: "https://example.com/mac.dmg",
		},
		{
			name: "pkg with darwin in name",
			assets: []githubAsset{
				{Name: "app-darwin.pkg", DownloadURL: "https://example.com/darwin.pkg"},
			},
			want: "https://example.com/darwin.pkg",
		},
		{
			name: "zip with macos in name",
			assets: []githubAsset{
				{Name: "app-macos.zip", DownloadURL: "https://example.com/macos.zip"},
			},
			want: "https://example.com/macos.zip",
		},
		{
			name:   "no mac asset",
			assets: []githubAsset{
				{Name: "app-linux.tar.gz", DownloadURL: "https://example.com/linux.tar.gz"},
			},
			want: "",
		},
		{
			name:   "empty assets",
			assets: nil,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findMacAsset(tt.assets)
			if got != tt.want {
				t.Errorf("findMacAsset() = %s, want %s", got, tt.want)
			}
		})
	}
}
