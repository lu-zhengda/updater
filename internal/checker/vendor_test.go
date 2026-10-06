package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
