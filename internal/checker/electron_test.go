package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestElectronChecker_CanCheck(t *testing.T) {
	c := NewElectronChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "electron app with update URL",
			app:  &app.App{Source: app.SourceElectron, ElectronUpdateURL: "https://update.example.com"},
			want: true,
		},
		{
			name: "electron app without update URL",
			app:  &app.App{Source: app.SourceElectron},
			want: false,
		},
		{
			name: "non-electron source with update URL",
			app:  &app.App{Source: app.SourceUnknown, ElectronUpdateURL: "https://update.example.com"},
			want: false,
		},
		{
			name: "github source",
			app:  &app.App{Source: app.SourceGitHub},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.CanCheck(tt.app)
			if got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestElectronChecker_Check(t *testing.T) {
	tests := []struct {
		name        string
		appVersion  string
		response    string
		statusCode  int
		wantUpdate  bool
		wantVersion string
		wantErr     bool
	}{
		{
			name:       "update available",
			appVersion: "1.0.0",
			response: `version: 2.0.0
files:
  - url: TestApp-2.0.0-mac.zip
    sha512: abc123
path: TestApp-2.0.0-mac.zip
sha512: abc123
releaseDate: '2026-02-14T10:00:00.000Z'
`,
			statusCode:  200,
			wantUpdate:  true,
			wantVersion: "2.0.0",
		},
		{
			name:       "up to date",
			appVersion: "2.0.0",
			response: `version: 2.0.0
path: TestApp-2.0.0-mac.zip
`,
			statusCode:  200,
			wantUpdate:  false,
			wantVersion: "2.0.0",
		},
		{
			name:        "server error",
			appVersion:  "1.0.0",
			response:    "Internal Server Error",
			statusCode:  500,
			wantErr:     true,
			wantVersion: "",
		},
		{
			name:        "malformed YAML",
			appVersion:  "1.0.0",
			response:    "{{not yaml at all",
			statusCode:  200,
			wantErr:     true,
			wantVersion: "",
		},
		{
			name:        "empty version",
			appVersion:  "1.0.0",
			response:    "path: TestApp.zip\n",
			statusCode:  200,
			wantErr:     true,
			wantVersion: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/latest-mac.yml" {
					t.Errorf("unexpected path: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer ts.Close()

			c := NewElectronChecker(ts.Client())
			a := &app.App{
				Name:              "TestApp",
				Version:           tt.appVersion,
				Source:            app.SourceElectron,
				ElectronUpdateURL: ts.URL,
			}

			result, err := c.Check(context.Background(), a)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.HasUpdate != tt.wantUpdate {
				t.Errorf("HasUpdate = %v, want %v", result.HasUpdate, tt.wantUpdate)
			}
			if result.LatestVersion != tt.wantVersion {
				t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, tt.wantVersion)
			}
			if result.Source != "electron" {
				t.Errorf("Source = %q, want %q", result.Source, "electron")
			}
		})
	}
}

func TestElectronChecker_DownloadURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("version: 2.0.0\npath: TestApp-2.0.0-mac.zip\n"))
	}))
	defer ts.Close()

	c := NewElectronChecker(ts.Client())
	a := &app.App{
		Name:              "TestApp",
		Version:           "1.0.0",
		Source:            app.SourceElectron,
		ElectronUpdateURL: ts.URL,
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := ts.URL + "/TestApp-2.0.0-mac.zip"
	if result.DownloadURL != want {
		t.Errorf("DownloadURL = %q, want %q", result.DownloadURL, want)
	}
}

func TestElectronChecker_PropagatesSHA512(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("version: 2.0.0\npath: TestApp.zip\nsha512: c2lnbmF0dXJl\n"))
	}))
	defer ts.Close()

	result, err := NewElectronChecker(ts.Client()).Check(context.Background(), &app.App{
		Name: "TestApp", Version: "1.0.0", Source: app.SourceElectron, ElectronUpdateURL: ts.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DownloadDigest != "sha512:c2lnbmF0dXJl" {
		t.Fatalf("DownloadDigest = %q", result.DownloadDigest)
	}
}

func TestElectronChecker_Name(t *testing.T) {
	c := NewElectronChecker(nil)
	if c.Name() != "electron" {
		t.Errorf("Name() = %q, want %q", c.Name(), "electron")
	}
}
