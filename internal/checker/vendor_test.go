package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

type vendorTransport func(*http.Request) (*http.Response, error)

func (f vendorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func vendorTestClient(body string, status int, inspect func(*http.Request)) *http.Client {
	return &http.Client{Transport: vendorTransport(func(r *http.Request) (*http.Response, error) {
		if inspect != nil {
			inspect(r)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}

func vendorCodeApp(t *testing.T, quality string) *app.App {
	t.Helper()
	return &app.App{Name: "Code", BundleID: "com.microsoft.VSCode", Version: "1.139.0", Source: app.SourceElectron,
		ElectronUpdateURL: "https://update.code.visualstudio.com", ElectronUpdateFormat: "vscode", ElectronUpdateChannel: quality}
}

func TestVendorVSCodeChannelAndArchitecture(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		for _, quality := range []string{"stable", "insider"} {
			t.Run(arch+"/"+quality, func(t *testing.T) {
				setTestArchitecture(t, arch)
				a := vendorCodeApp(t, quality)
				platform, file := "darwin", "VSCode-darwin.zip"
				if arch == "arm64" {
					platform, file = "darwin-arm64", "VSCode-darwin-arm64.zip"
				}
				if quality == "insider" {
					a.BundleID = "com.microsoft.VSCodeInsiders"
				}
				digest := strings.Repeat("ab", 32)
				body := fmt.Sprintf(`{"productVersion":"1.140.0","version":"commit-sha-is-not-a-version","url":"https://vscode.download.prss.microsoft.com/%s","sha256hash":%q}`, file, digest)
				client := vendorTestClient(body, 200, func(r *http.Request) {
					if r.URL.String() != "https://update.code.visualstudio.com/api/update/"+platform+"/"+quality+"/latest" {
						t.Errorf("wrong endpoint: %s", r.URL)
					}
				})
				got, err := NewVendorChecker(client).Check(context.Background(), a)
				if err != nil {
					t.Fatal(err)
				}
				if !got.HasUpdate || got.LatestVersion != "1.140.0" || got.DownloadDigest != "sha256:"+digest || !strings.HasSuffix(got.DownloadURL, file) {
					t.Fatalf("incorrect release: %+v", got)
				}
				a.Version = "1.140.0"
				got, err = NewVendorChecker(client).Check(context.Background(), a)
				if err != nil || got.HasUpdate {
					t.Fatalf("current app offered update: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestVendorClaudeCurrentRelease(t *testing.T) {
	body := `{"currentRelease":"2.19675.0","releases":[
		{"version":"9.0.0","updateTo":{"version":"9.0.0","url":"https://downloads.claude.ai/future.zip"}},
		{"version":"2.19675.0","updateTo":{"version":"2.19675.0","url":"https://downloads.claude.ai/releases/darwin/universal/2.19675.0/Claude-release.zip","notes":"Release notes"}}]}`
	client := vendorTestClient(body, 200, func(r *http.Request) {
		if r.URL.String() != "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json" {
			t.Errorf("wrong endpoint: %s", r.URL)
		}
	})
	a := &app.App{Name: "Claude", BundleID: "com.anthropic.claudefordesktop", Version: "2.19674.0", Source: app.SourceElectron,
		ElectronUpdateURL: "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json", ElectronUpdateFormat: "squirrel"}
	got, err := NewVendorChecker(client).Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasUpdate || got.LatestVersion != "2.19675.0" || got.ReleaseNotes != "Release notes" || got.DownloadURL == "" {
		t.Fatalf("incorrect release: %+v", got)
	}
	a.Version = "2.19676.0"
	got, err = NewVendorChecker(client).Check(context.Background(), a)
	if err != nil || got.HasUpdate {
		t.Fatalf("newer installed app offered downgrade: %+v, %v", got, err)
	}
}

func TestVendorRejectsUnusableRelease(t *testing.T) {
	setTestArchitecture(t, "arm64")
	for _, tt := range []struct {
		name, id, body string
		status         int
	}{
		{"unavailable", "com.microsoft.VSCode", `{}`, 503},
		{"invalid JSON", "com.microsoft.VSCode", `invalid`, 200},
		{"no digest", "com.microsoft.VSCode", `{"productVersion":"1.140.0","url":"https://example.com/code.zip"}`, 200},
		{"HTTP download", "com.anthropic.claudefordesktop", `{"currentRelease":"2.0","releases":[{"version":"2.0","updateTo":{"version":"2.0","url":"http://example.com/Claude.zip"}}]}`, 200},
		{"wrong architecture", "com.anthropic.claudefordesktop", `{"currentRelease":"2.0","releases":[{"version":"2.0","updateTo":{"version":"2.0","url":"https://example.com/Claude-x64.zip"}}]}`, 200},
		{"missing selected release", "com.anthropic.claudefordesktop", `{"currentRelease":"2.0","releases":[]}`, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := vendorCodeApp(t, "stable")
			a.BundleID = tt.id
			if tt.id == "com.anthropic.claudefordesktop" {
				a.ElectronUpdateFormat = "squirrel"
				a.ElectronUpdateURL = "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json"
			}
			if _, err := NewVendorChecker(vendorTestClient(tt.body, tt.status, nil)).Check(context.Background(), a); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	client := vendorTestClient(`{}`, 200, func(*http.Request) { t.Error("unsupported channel must not make a request") })
	if _, err := NewVendorChecker(client).Check(context.Background(), vendorCodeApp(t, "custom")); err == nil {
		t.Fatal("expected channel error")
	}
}

func vendorFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func cursorVendorApp(base string) *app.App {
	return &app.App{
		Name: "Cursor", BundleID: app.CursorBundleID, Version: "3.31.0", Source: app.SourceElectron,
		ElectronUpdateURL: base, ElectronUpdateFormat: "cursor",
		ElectronProductVersion: "3.31.0", ElectronCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func TestVendorCursorUpdate(t *testing.T) {
	setTestArchitecture(t, "arm64")
	body := vendorFixture(t, "cursor-update.json")
	const version = "3.31.0"
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const platform = "darwin-arm64"
	wantPath := "/updates/api/update/" + platform + "/cursor/" + version + "/" + commit
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != wantPath {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(vendorFixture(t, "cursor-not-found.json"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	got, err := NewVendorChecker(srv.Client()).Check(context.Background(), cursorVendorApp(srv.URL+"/updates"))
	if err != nil {
		t.Fatal(err)
	}
	const zipURL = "https://downloads.cursor.com/production/0925c4b8226c56947c43f0b555fb98e187a7e029/darwin/arm64/Cursor-darwin-arm64.zip"
	if gotPath != wantPath {
		t.Fatalf("path %s, want %s", gotPath, wantPath)
	}
	if !got.HasUpdate || got.LatestVersion != "3.32.4" || got.DownloadURL != zipURL || got.DownloadDigest != "" {
		t.Fatalf("incorrect release: %+v", got)
	}
}

func TestVendorCursorCurrent(t *testing.T) {
	setTestArchitecture(t, "arm64")
	current := vendorFixture(t, "cursor-current.txt")
	const version = "3.32.4"
	const commit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const platform = "darwin-arm64"
	wantPath := "/api/update/" + platform + "/cursor/" + version + "/" + commit
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
		_, _ = w.Write(current)
	}))
	t.Cleanup(srv.Close)

	a := cursorVendorApp(srv.URL)
	a.Version = version
	a.ElectronProductVersion = version
	a.ElectronCommit = commit
	called := false
	checker := NewVendorChecker(srv.Client())
	checker.runner = CmdRunnerFunc(func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return nil, fmt.Errorf("cask lookup")
	})
	got, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != wantPath {
		t.Fatalf("path %s, want %s", gotPath, wantPath)
	}
	if got.HasUpdate || got.Error != nil || called {
		t.Fatalf("current app: %+v called=%v", got, called)
	}
}

func TestVendorCursorRequestPath(t *testing.T) {
	const version = "3.31.0"
	const commit = "cccccccccccccccccccccccccccccccccccccccc"
	for _, tc := range []struct {
		arch, platform string
	}{
		{"arm64", "darwin-arm64"},
		{"amd64", "darwin-x64"},
		{"universal", "darwin-universal"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			setTestArchitecture(t, tc.arch)
			wantPath := "/updates/api/update/" + tc.platform + "/cursor/" + version + "/" + commit
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			a := cursorVendorApp(srv.URL + "/updates")
			a.ElectronProductVersion = version
			a.ElectronCommit = commit
			_, _ = NewVendorChecker(srv.Client()).Check(context.Background(), a)
			if gotPath != wantPath {
				t.Fatalf("path %s, want %s", gotPath, wantPath)
			}
		})
	}
}

func TestVendorCursorUsesMatchingCaskDigest(t *testing.T) {
	setTestArchitecture(t, "arm64")
	body := vendorFixture(t, "cursor-update.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	sum := strings.Repeat("cd", 32)
	cask := fmt.Sprintf(`{"casks":[{"token":"cursor","version":"3.32.4","url":"https://downloads.cursor.com/production/0925c4b8226c56947c43f0b555fb98e187a7e029/darwin/arm64/Cursor-darwin-arm64.zip","sha256":"%s"}]}`, sum)
	v := NewVendorChecker(srv.Client())
	v.runner = &MockCmdRunner{Output: []byte(cask)}
	a := cursorVendorApp(srv.URL)
	a.CaskName = "cursor"
	got, err := v.Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasUpdate || got.LatestVersion != "3.32.4" || got.DownloadDigest != "sha256:"+sum {
		t.Fatalf("digest not taken from the matching cask: %+v", got)
	}

	v.runner = &MockCmdRunner{Output: []byte(`{"casks":[{"token":"cursor","version":"1.0.0","sha256":"` + sum + `","url":"https://example.com/old.zip"}]}`)}
	got, err = v.Check(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasUpdate || got.DownloadDigest != "" {
		t.Fatalf("unmatched cask digest applied: %+v", got)
	}
}

type CmdRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f CmdRunnerFunc) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestVendorFormatsHaveCapturedFixtures(t *testing.T) {
	setTestArchitecture(t, "arm64")
	t.Run("vscode", func(t *testing.T) {
		body := vendorFixture(t, "vscode-update.json")
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		a := vendorCodeApp(t, "stable")
		a.ElectronUpdateURL = srv.URL
		a.Version = "1.0.0"
		got, err := NewVendorChecker(srv.Client()).Check(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != "/api/update/darwin-arm64/stable/latest" || got.LatestVersion != "1.148.0" || !strings.Contains(got.DownloadURL, "VSCode-darwin-arm64.zip") || !strings.HasPrefix(got.DownloadDigest, "sha256:") {
			t.Fatalf("vscode fixture: path %s result %+v", gotPath, got)
		}
	})
	t.Run("squirrel", func(t *testing.T) {
		body := vendorFixture(t, "squirrel-releases.json")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/releases/darwin/universal/RELEASES.json" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		a := &app.App{Name: "Claude", BundleID: "com.anthropic.claudefordesktop", Version: "1.0.0", Source: app.SourceElectron,
			ElectronUpdateURL: srv.URL + "/releases/darwin/universal/RELEASES.json", ElectronUpdateFormat: "squirrel"}
		got, err := NewVendorChecker(srv.Client()).Check(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		if !got.HasUpdate || got.LatestVersion != "7.37095.0" || !strings.Contains(got.DownloadURL, "Claude-bc368c812ec26d3a5740519cc4d5cb69bbe43025.zip") {
			t.Fatalf("squirrel fixture: %+v", got)
		}
	})
	for _, name := range []string{"cursor-update.json", "cursor-current.txt", "cursor-not-found.json"} {
		if len(vendorFixture(t, name)) == 0 && name != "cursor-current.txt" {
			t.Fatalf("empty fixture %s", name)
		}
	}
}

func TestVendorDoesNotClaimManagedOrUnrelatedApps(t *testing.T) {
	v := NewVendorChecker(nil)
	for _, a := range []*app.App{
		{BundleID: "com.microsoft.VSCode", Source: app.SourceMAS},
		{BundleID: "com.anthropic.claudefordesktop", Source: app.SourceSetapp},
		{BundleID: "com.example.electron", Source: app.SourceElectron},
	} {
		if v.CanCheck(a) {
			t.Fatalf("unexpected vendor match: %+v", a)
		}
	}
}
