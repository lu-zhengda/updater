package installer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/luzhengda/updater/internal/checker"
)

func TestFilenameFromResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentDisp string
		url         string
		want        string
	}{
		{
			name:        "from content-disposition",
			contentDisp: `attachment; filename="App-2.0.dmg"`,
			url:         "https://example.com/download",
			want:        "App-2.0.dmg",
		},
		{
			name:        "from content-disposition unquoted",
			contentDisp: `attachment; filename=App-2.0.zip`,
			url:         "https://example.com/download",
			want:        "App-2.0.zip",
		},
		{
			name:        "from URL",
			contentDisp: "",
			url:         "https://example.com/releases/App-2.0.dmg",
			want:        "App-2.0.dmg",
		},
		{
			name:        "from URL with query",
			contentDisp: "",
			url:         "https://example.com/releases/App.pkg?token=abc",
			want:        "App.pkg",
		},
		{
			name:        "fallback",
			contentDisp: "",
			url:         "https://example.com/",
			want:        "download",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tt.contentDisp != "" {
				resp.Header.Set("Content-Disposition", tt.contentDisp)
			}
			got := filenameFromResponse(resp, tt.url)
			if got != tt.want {
				t.Errorf("filenameFromResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractMountPoint(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "simple volume path",
			output: "/Volumes/MyApp",
			want:   "/Volumes/MyApp",
		},
		{
			name:   "plist format",
			output: "<string>/Volumes/MyApp 2.0</string>",
			want:   "/Volumes/MyApp 2.0",
		},
		{
			name:   "multiline with volume",
			output: "some preamble\n/dev/disk5\n/Volumes/MyApp\n",
			want:   "/Volumes/MyApp",
		},
		{
			name:   "no volume",
			output: "some output without volume info",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMountPoint(tt.output)
			if got != tt.want {
				t.Errorf("extractMountPoint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindAppInVolume(t *testing.T) {
	tests := []struct {
		name    string
		ls      string
		appName string
		want    string
	}{
		{
			name:    "exact match",
			ls:      "MyApp.app\nREADME.txt\n",
			appName: "MyApp",
			want:    "/vol/MyApp.app",
		},
		{
			name:    "case insensitive match",
			ls:      "myapp.app\n",
			appName: "MyApp",
			want:    "/vol/myapp.app",
		},
		{
			name:    "fallback to first app",
			ls:      "Other.app\n",
			appName: "MyApp",
			want:    "/vol/Other.app",
		},
		{
			name:    "no apps",
			ls:      "README.txt\nLICENSE\n",
			appName: "MyApp",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &checker.MockCmdRunner{Output: []byte(tt.ls)}
			got := findAppInVolume(context.Background(), runner, "/vol", tt.appName)
			if got != tt.want {
				t.Errorf("findAppInVolume() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDownload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="test.dmg"`)
		w.Write([]byte("fake dmg content"))
	}))
	defer ts.Close()

	runner := &checker.MockCmdRunner{}
	inst := New(runner, ts.Client())

	tmpDir := t.TempDir()
	path, err := inst.download(context.Background(), ts.URL+"/download", tmpDir)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Verify content was written.
	content, err := readFile(path)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(content) != "fake dmg content" {
		t.Errorf("content = %q, want %q", string(content), "fake dmg content")
	}
}

func TestDownload_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	inst := New(&checker.MockCmdRunner{}, ts.Client())
	_, err := inst.download(context.Background(), ts.URL+"/missing", t.TempDir())
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestInstallFormatDetection(t *testing.T) {
	// Verify that Install dispatches to the correct handler based on extension.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app.dmg":
			w.Write([]byte("dmg"))
		case r.URL.Path == "/app.zip":
			w.Write([]byte("zip"))
		case r.URL.Path == "/app.exe":
			w.Write([]byte("exe"))
		}
	}))
	defer ts.Close()

	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			// DMG commands will fail — that's fine, we're testing format detection.
		},
	}

	inst := New(runner, ts.Client())

	// Unsupported format should error.
	err := inst.Install(context.Background(), ts.URL+"/app.exe", "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported file format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
