package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestChecksumForAsset(t *testing.T) {
	want := strings.Repeat("a", 64)
	data := []byte(want + "  updater_1.2.3_darwin.tar.gz\n")
	got, err := checksumForAsset(data, "updater_1.2.3_darwin.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("checksum = %q, want %q", got, want)
	}
}

func TestExtractUpdaterBinary(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)
	payload := []byte("signed updater")
	if err := tw.WriteHeader(&tar.Header{Name: "updater", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}

	dst, err := os.CreateTemp(t.TempDir(), "candidate-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := extractUpdaterBinary(archivePath, dst); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("extracted payload = %q", got)
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
