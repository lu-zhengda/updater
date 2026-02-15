package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestGitHubChecker_Name(t *testing.T) {
	c := NewGitHubChecker(nil, "", "")
	if got := c.Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
}

func TestGitHubChecker_CheckErrorPaths(t *testing.T) {
	t.Run("empty GitHub repo", func(t *testing.T) {
		c := NewGitHubChecker(nil, "", "")
		a := &app.App{Name: "TestApp", Version: "1.0.0"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for empty GitHubRepo, got nil")
		}
	})

	t.Run("server gone (connection refused)", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		baseURL := ts.URL
		ts.Close()

		c := NewGitHubChecker(ts.Client(), baseURL, "")
		a := &app.App{Name: "TestApp", Version: "1.0.0", GitHubRepo: "test/app"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error when server is gone, got nil")
		}
	})

	t.Run("non-200 status code", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer ts.Close()

		c := NewGitHubChecker(ts.Client(), ts.URL, "")
		a := &app.App{Name: "TestApp", Version: "1.0.0", GitHubRepo: "test/app"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for non-200 status, got nil")
		}
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("not json"))
		}))
		defer ts.Close()

		c := NewGitHubChecker(ts.Client(), ts.URL, "")
		a := &app.App{Name: "TestApp", Version: "1.0.0", GitHubRepo: "test/app"}

		_, err := c.Check(context.Background(), a)
		if err == nil {
			t.Fatal("expected error for invalid JSON, got nil")
		}
	})
}

func TestHasMacKeyword_OSX(t *testing.T) {
	if !hasMacKeyword("app-osx-arm64.dmg") {
		t.Error("expected hasMacKeyword to return true for 'osx' keyword")
	}
}

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

	checker := NewGitHubChecker(ts.Client(), ts.URL, "")
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

	checker := NewGitHubChecker(ts.Client(), ts.URL, "")
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
	checker := NewGitHubChecker(nil, "", "")

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

func TestGitHubChecker_AuthorizationHeader(t *testing.T) {
	var gotAuthHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testReleaseJSON))
	}))
	defer ts.Close()

	t.Run("with token", func(t *testing.T) {
		gotAuthHeader = ""
		c := NewGitHubChecker(ts.Client(), ts.URL, "my-secret-token")
		a := &app.App{Name: "TestApp", Version: "1.0.0", GitHubRepo: "test/app"}
		_, err := c.Check(context.Background(), a)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuthHeader != "Bearer my-secret-token" {
			t.Errorf("expected Authorization 'Bearer my-secret-token', got %q", gotAuthHeader)
		}
	})

	t.Run("without token", func(t *testing.T) {
		gotAuthHeader = ""
		c := NewGitHubChecker(ts.Client(), ts.URL, "")
		a := &app.App{Name: "TestApp", Version: "1.0.0", GitHubRepo: "test/app"}
		_, err := c.Check(context.Background(), a)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuthHeader != "" {
			t.Errorf("expected no Authorization header, got %q", gotAuthHeader)
		}
	})
}

func TestGitHubChecker_FindMacAsset(t *testing.T) {
	tests := []struct {
		name   string
		assets []GitHubAsset
		want   string
	}{
		{
			name: "dmg with mac in name",
			assets: []GitHubAsset{
				{Name: "app-linux.tar.gz", DownloadURL: "https://example.com/linux.tar.gz"},
				{Name: "app-mac.dmg", DownloadURL: "https://example.com/mac.dmg"},
			},
			want: "https://example.com/mac.dmg",
		},
		{
			name: "pkg with darwin in name",
			assets: []GitHubAsset{
				{Name: "app-darwin.pkg", DownloadURL: "https://example.com/darwin.pkg"},
			},
			want: "https://example.com/darwin.pkg",
		},
		{
			name: "zip with macos in name",
			assets: []GitHubAsset{
				{Name: "app-macos.zip", DownloadURL: "https://example.com/macos.zip"},
			},
			want: "https://example.com/macos.zip",
		},
		{
			name:   "no mac asset",
			assets: []GitHubAsset{
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
