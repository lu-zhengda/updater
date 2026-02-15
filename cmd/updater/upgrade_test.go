package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/checker"
)

func TestIsBrewInstall(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"opt homebrew", "/opt/homebrew/Cellar/updater/1.0/bin/updater", true},
		{"usr local cellar", "/usr/local/Cellar/updater/1.0/bin/updater", true},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/updater", true},
		{"usr local bin", "/usr/local/bin/updater", false},
		{"home binary", "/Users/user/bin/updater", false},
		{"tmp", "/tmp/updater", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBrewInstall(tt.path); got != tt.want {
				t.Errorf("isBrewInstall(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFetchLatestRelease_Success(t *testing.T) {
	release := checker.GitHubRelease{
		TagName: "v1.2.3",
		Name:    "Release 1.2.3",
		Body:    "Bug fixes",
		Assets: []checker.GitHubAsset{
			{Name: "updater-darwin-arm64", DownloadURL: "https://example.com/download"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer ts.Close()

	got, err := fetchLatestReleaseFrom(ts.URL, "", ts.Client())
	if err != nil {
		t.Fatalf("fetchLatestReleaseFrom failed: %v", err)
	}
	if got.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want %q", got.TagName, "v1.2.3")
	}
	if len(got.Assets) != 1 {
		t.Errorf("got %d assets, want 1", len(got.Assets))
	}
}

func TestFetchLatestRelease_RateLimited(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	_, err := fetchLatestReleaseFrom(ts.URL, "", ts.Client())
	if err == nil {
		t.Fatal("expected error for rate limited response")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %q, want containing 'rate limited'", err.Error())
	}
}

func TestFetchLatestRelease_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := fetchLatestReleaseFrom(ts.URL, "", ts.Client())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %q, want containing 'status 500'", err.Error())
	}
}

func TestFetchLatestRelease_WithToken(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(checker.GitHubRelease{TagName: "v1.0.0"})
	}))
	defer ts.Close()

	_, err := fetchLatestReleaseFrom(ts.URL, "test-token-123", ts.Client())
	if err != nil {
		t.Fatalf("fetchLatestReleaseFrom failed: %v", err)
	}
	if gotAuth != "Bearer test-token-123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token-123")
	}
}

func TestDownloadFile_Success(t *testing.T) {
	content := "binary-content-here"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer ts.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "download-*")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpFile.Close()

	err = downloadFileWith(tmpFile, ts.URL+"/binary", "", ts.Client())
	if err != nil {
		t.Fatalf("downloadFileWith failed: %v", err)
	}

	// Read back and verify.
	tmpFile.Close()
	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestDownloadFile_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "download-*")
	if err != nil {
		t.Fatal(err)
	}
	defer tmpFile.Close()

	err = downloadFileWith(tmpFile, ts.URL+"/missing", "", ts.Client())
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("error = %q, want containing 'status 404'", err.Error())
	}
}
