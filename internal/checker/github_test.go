package checker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestTransformGitHubVersion(t *testing.T) {
	for _, tc := range []struct {
		name, tag, pattern, replacement, want string
	}{
		{"numeric mapping", "0.1.33-alpha", `^0\.(\d+\.\d+)-alpha$`, "1.${1}", "1.1.33"},
		{"named capture", "app-2.3.4", `^app-(?P<version>\d+\.\d+\.\d+)$`, "${version}", "2.3.4"},
		{"unmatched", "1.1.33", `^0\.(.*)$`, "1.${1}", ""},
		{"partial match", "release-2.3.4", `\d+\.\d+\.\d+`, "${0}", ""},
		{"invalid regex", "1.2.3", "(", "${1}", ""},
		{"invalid result", "1.2.3", "(.*)", "invalid", ""},
		{"missing capture", "1.2.3", "(.*)", "${missing}", ""},
		{"empty replacement", "1.2.3", "(.*)", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := transformGitHubVersion(tc.tag, tc.pattern, tc.replacement)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestGitHubChecker_BundleVersion(t *testing.T) {
	for _, tc := range []struct {
		name, current, bundleVersion, readerError, wantError string
		wantUpdate, wantMajor                                bool
	}{
		{"newer", "1.1.32", "1.1.33", "", "", true, false},
		{"equal", "1.1.33", "1.1.33", "", "", false, false},
		{"older", "1.1.34", "1.1.33", "", "", false, false},
		{"major", "1.1.32", "2.1.33", "", "", true, true},
		{"inspection error", "1.1.32", "", "invalid bundle", "invalid bundle", false, false},
		{"empty version", "1.1.32", "", "", "bundle version is empty", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(GitHubRelease{TagName: "0.1.33-alpha", Assets: []GitHubAsset{
					{Name: "app-mac.pkg", DownloadURL: "https://example.com/app-mac.pkg"},
					{Name: "app-mac.zip", DownloadURL: "https://example.com/app-mac.zip", Digest: "sha256:expected"},
				}})
			}))
			defer ts.Close()
			g := NewGitHubChecker(ts.Client(), ts.URL, "")
			called := false
			g.ReadBundleVersion = func(ctx context.Context, downloadURL, digest, bundleID string) (string, error) {
				called = true
				if downloadURL != "https://example.com/app-mac.zip" || digest != "sha256:expected" || bundleID != "com.example.app" {
					t.Fatalf("wrong extraction inputs: %q %q %q", downloadURL, digest, bundleID)
				}
				if tc.readerError != "" {
					return "", errors.New(tc.readerError)
				}
				return tc.bundleVersion, nil
			}
			result, err := g.Check(context.Background(), &app.App{
				Name: "Example", BundleID: "com.example.app", Version: tc.current, GitHubRepo: "example/app",
				ResolvedSourceOverride: &app.SourceOverride{Kind: "github", VersionSource: "bundle"},
			})
			if !called {
				t.Fatal("bundle reader was not called")
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || result != nil {
					t.Fatalf("result = %#v, error = %v; want %q", result, err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.LatestVersion != tc.bundleVersion || result.CurrentVersion != tc.current || result.HasUpdate != tc.wantUpdate || result.IsMajorUpdate != tc.wantMajor {
				t.Fatalf("unexpected result: %#v", result)
			}
			if result.DownloadURL != "https://example.com/app-mac.zip" || result.DownloadDigest != "sha256:expected" {
				t.Fatalf("extraction changed the download: %#v", result)
			}
		})
	}
}

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
			name: "no mac asset",
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
			if got.DownloadURL != tt.want {
				t.Errorf("findMacAsset() = %s, want %s", got.DownloadURL, tt.want)
			}
		})
	}
}
