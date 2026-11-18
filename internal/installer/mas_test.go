package installer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

type masTestTransport func(*http.Request) (*http.Response, error)

func (f masTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type masTestRunner func(string, ...string) ([]byte, error)

func (f masTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return f(name, args...)
}

func TestManualAppStoreUpdate(t *testing.T) {
	const archive = "test mas archive"
	original := masArtifacts[runtime.GOARCH]
	artifact := original
	artifact.packageSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(archive)))
	masArtifacts[runtime.GOARCH] = artifact
	t.Cleanup(func() { masArtifacts[runtime.GOARCH] = original })

	for _, test := range []struct {
		name, id, status, installed, wantError string
		prompt                                 bool
	}{
		{name: "advanced", id: "123", status: "ok", installed: "2.1", prompt: true},
		{name: "same version", id: "123", status: "ok", wantError: "did not advance (1.0 → 1.0)", prompt: true},
		{name: "partially advanced", id: "123", status: "ok", installed: "1.5", wantError: "version 2.0 remains pending", prompt: true},
		{name: "failed after install", id: "123", status: "1: could not copy receipt", installed: "2.0", wantError: "now at version 2.0, but App Store update failed: could not copy receipt", prompt: true},
		{name: "declined", id: "123", status: "-128: User canceled", wantError: "cancelled", prompt: true},
		{name: "mas failure", id: "123", status: "1: App Store sign-in required", wantError: "App Store sign-in required", prompt: true},
		{name: "wrong app ID", id: "456", wantError: "could not confirm the App Store ID"},
		{name: "option as ID", id: "--force", wantError: "no valid App Store ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &app.App{Name: "Test App", BundleID: "com.example.app", Path: filepath.Join(t.TempDir(), "Test App.app"), MASID: test.id}
			contents := filepath.Join(target.Path, "Contents")
			if err := os.MkdirAll(contents, 0o755); err != nil {
				t.Fatal(err)
			}
			writeVersion := func(version string) {
				t.Helper()
				plist := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.app</string><key>CFBundleShortVersionString</key><string>%s</string></dict></plist>`, version)
				if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeVersion("1.0")

			prompted := false
			runner := masTestRunner(func(name string, args ...string) ([]byte, error) {
				switch name {
				case "/usr/bin/mdls":
					return []byte("123"), nil
				case "/usr/sbin/pkgutil":
					return nil, nil
				case "/usr/bin/osascript":
					if prompted || len(args) != 9 || args[7] != "123" || args[8] != "Test App" || args[4] != artifact.binarySHA {
						t.Fatalf("unexpected authorization call: %v", args)
					}
					prompted = true
					if test.installed != "" {
						writeVersion(test.installed)
					}
					return []byte(test.status), nil
				default:
					t.Fatalf("unexpected command: %s", name)
					return nil, nil
				}
			})
			client := &http.Client{Transport: masTestTransport(func(req *http.Request) (*http.Response, error) {
				if !test.prompt || req.URL.Host != "github.com" || !strings.HasSuffix(req.URL.Path, "/mas-"+masRelease+"-"+artifact.arch+".pkg") {
					t.Fatalf("unexpected download: %s", req.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(archive)), Header: make(http.Header), Request: req}, nil
			})}
			inst := New(runner, client)
			inst.AllowAuthorizationPrompt = true
			got, err := inst.UpdateAppStoreApp(context.Background(), target, "2.0")
			if !test.prompt && prompted {
				t.Fatal("unexpected authorization prompt")
			}
			switch {
			case test.wantError == "" && (err != nil || got != test.installed || !prompted):
				t.Fatalf("UpdateAppStoreApp = %q, %v, prompted=%v", got, err, prompted)
			case test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)):
				t.Fatalf("UpdateAppStoreApp error = %v, want %q", err, test.wantError)
			}
		})
	}
}
