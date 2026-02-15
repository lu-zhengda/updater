package installer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestInstallDMG_Success(t *testing.T) {
	dmgPath := filepath.Join(t.TempDir(), "test.dmg")
	if err := os.WriteFile(dmgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	mountPoint := "/Volumes/TestApp"
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("hdiutil attach -nobrowse -plist %s", dmgPath): {
				Output: []byte(mountPoint + "\n"),
			},
			"ls " + mountPoint: {
				Output: []byte("TestApp.app\nREADME.txt\n"),
			},
			fmt.Sprintf("xattr -rd com.apple.quarantine %s", filepath.Join(mountPoint, "TestApp.app")): {},
			fmt.Sprintf("cp -a %s %s", filepath.Join(mountPoint, "TestApp.app"), "/Applications/TestApp.app"): {},
			"hdiutil detach " + mountPoint + " -quiet": {},
		},
	}

	inst := New(runner, nil)
	err := inst.installDMG(context.Background(), dmgPath, "/Applications/TestApp.app", "TestApp")
	if err != nil {
		t.Fatalf("installDMG failed: %v", err)
	}
}

func TestInstallDMG_MountFails(t *testing.T) {
	dmgPath := filepath.Join(t.TempDir(), "test.dmg")
	if err := os.WriteFile(dmgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("hdiutil attach -nobrowse -plist %s", dmgPath): {
				Err: fmt.Errorf("corrupt DMG"),
			},
		},
	}

	inst := New(runner, nil)
	err := inst.installDMG(context.Background(), dmgPath, "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error when mount fails")
	}
	if !strings.Contains(err.Error(), "failed to mount DMG") {
		t.Errorf("error = %q, want containing 'failed to mount DMG'", err.Error())
	}
}

func TestInstallDMG_NoMountPoint(t *testing.T) {
	dmgPath := filepath.Join(t.TempDir(), "test.dmg")
	if err := os.WriteFile(dmgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("hdiutil attach -nobrowse -plist %s", dmgPath): {
				Output: []byte("no volume info here\n"),
			},
		},
	}

	inst := New(runner, nil)
	err := inst.installDMG(context.Background(), dmgPath, "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error when no mount point found")
	}
	if !strings.Contains(err.Error(), "failed to find mount point") {
		t.Errorf("error = %q, want containing 'failed to find mount point'", err.Error())
	}
}

func TestInstallDMG_NoAppInVolume(t *testing.T) {
	dmgPath := filepath.Join(t.TempDir(), "test.dmg")
	if err := os.WriteFile(dmgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	mountPoint := "/Volumes/TestApp"
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("hdiutil attach -nobrowse -plist %s", dmgPath): {
				Output: []byte(mountPoint + "\n"),
			},
			"ls " + mountPoint: {
				Output: []byte("README.txt\nLICENSE\n"),
			},
			"hdiutil detach " + mountPoint + " -quiet": {},
		},
	}

	inst := New(runner, nil)
	err := inst.installDMG(context.Background(), dmgPath, "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error when no .app found in volume")
	}
	if !strings.Contains(err.Error(), "no .app bundle found in DMG") {
		t.Errorf("error = %q, want containing 'no .app bundle found in DMG'", err.Error())
	}
}

func TestInstallZIP_Success(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	if err := os.WriteFile(zipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	// installZIP creates its own tmpDir internally, so we need a broader mock strategy.
	// The ditto and ls commands will use the internally-created tmpDir, which we can't predict.
	// Instead, test via the internal method with a known path by mocking at a higher level.
	// We'll rely on MultiMockCmdRunner's fallback (nil error) for unknown commands.
	runner := &installZIPMockRunner{appName: "TestApp"}

	inst := New(runner, nil)
	err := inst.installZIP(context.Background(), zipPath, "/Applications/TestApp.app", "TestApp")
	if err != nil {
		t.Fatalf("installZIP failed: %v", err)
	}
}

func TestInstallZIP_DittoFails(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	if err := os.WriteFile(zipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &dittoFailRunner{}
	inst := New(runner, nil)
	err := inst.installZIP(context.Background(), zipPath, "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error when ditto fails")
	}
	if !strings.Contains(err.Error(), "failed to extract ZIP") {
		t.Errorf("error = %q, want containing 'failed to extract ZIP'", err.Error())
	}
}

func TestInstallZIP_NoAppFound(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test.zip")
	if err := os.WriteFile(zipPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &noAppZIPRunner{}
	inst := New(runner, nil)
	err := inst.installZIP(context.Background(), zipPath, "/Applications/Test.app", "Test")
	if err == nil {
		t.Fatal("expected error when no .app found")
	}
	if !strings.Contains(err.Error(), "no .app bundle found in ZIP") {
		t.Errorf("error = %q, want containing 'no .app bundle found in ZIP'", err.Error())
	}
}

func TestInstallPKG_Success(t *testing.T) {
	pkgPath := filepath.Join(t.TempDir(), "test.pkg")
	if err := os.WriteFile(pkgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("sudo installer -pkg %s -target /", pkgPath): {},
		},
	}

	inst := New(runner, nil)
	err := inst.installPKG(context.Background(), pkgPath)
	if err != nil {
		t.Fatalf("installPKG failed: %v", err)
	}
}

func TestInstallPKG_Fails(t *testing.T) {
	pkgPath := filepath.Join(t.TempDir(), "test.pkg")
	if err := os.WriteFile(pkgPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			fmt.Sprintf("sudo installer -pkg %s -target /", pkgPath): {
				Err: fmt.Errorf("installer failed"),
			},
		},
	}

	inst := New(runner, nil)
	err := inst.installPKG(context.Background(), pkgPath)
	if err == nil {
		t.Fatal("expected error when PKG install fails")
	}
	if !strings.Contains(err.Error(), "failed to install PKG") {
		t.Errorf("error = %q, want containing 'failed to install PKG'", err.Error())
	}
}

// installZIPMockRunner handles the dynamic tmpDir that installZIP creates internally.
// It responds to ditto (no-op success), ls (returns appName.app), and cp/xattr (no-op).
type installZIPMockRunner struct {
	appName string
}

func (r *installZIPMockRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "ditto":
		return nil, nil
	case "ls":
		return []byte(r.appName + ".app\n"), nil
	case "cp", "xattr":
		return nil, nil
	}
	return nil, nil
}

// dittoFailRunner fails on ditto commands.
type dittoFailRunner struct{}

func (r *dittoFailRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "ditto" {
		return nil, fmt.Errorf("ditto: extraction failed")
	}
	return nil, nil
}

// noAppZIPRunner succeeds on ditto but returns no .app in ls output.
type noAppZIPRunner struct{}

func (r *noAppZIPRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "ls" {
		return []byte("README.txt\nLICENSE\n"), nil
	}
	return nil, nil
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
