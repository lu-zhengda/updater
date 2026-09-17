package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
)

func setTestArchitecture(t *testing.T, arch string) {
	t.Helper()
	old := architecture.Native
	architecture.Native = func() string { return arch }
	t.Cleanup(func() { architecture.Native = old })
}

func TestElectronArchitecture(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			setTestArchitecture(t, arch)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `version: 2.0.0
files:
 - url: app-x64.zip
   sha512: intel-digest
 - url: app-universal.zip
   sha512: universal-digest
 - url: app-arm64.zip
   sha512: arm-digest
path: app-x64.zip
sha512: legacy-digest
`)
			}))
			defer server.Close()
			got, err := NewElectronChecker(server.Client()).Check(context.Background(), &app.App{Name: "Test", Version: "1.0.0", ElectronUpdateURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			file, digest := "app-arm64.zip", "arm-digest"
			if arch == "amd64" {
				file, digest = "app-x64.zip", "intel-digest"
			}
			if got.DownloadURL != server.URL+"/"+file || got.DownloadDigest != "sha512:"+digest {
				t.Fatalf("wrong artifact or digest: %+v", got)
			}
		})
	}
}

func TestNotionArchitectureChannel(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			setTestArchitecture(t, arch)
			want := "/arm64-mac.yml"
			if arch == "amd64" {
				want = "/latest-mac.yml"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != want {
					t.Errorf("requested %s, want %s", r.URL.Path, want)
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, "version: 7.34.0\npath: Notion.zip\n")
			}))
			defer server.Close()
			_, err := NewElectronChecker(server.Client()).Check(context.Background(), &app.App{Name: "Notion", BundleID: "notion.id", ElectronUpdateURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRejectIncompatibleElectronFiles(t *testing.T) {
	setTestArchitecture(t, "arm64")
	for _, metadata := range []string{
		"version: 2\npath: app-x64.zip\n",
		"version: 2\nfiles:\n - url: app-x64.zip\npath: app-universal.zip\n",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, metadata) }))
		_, err := NewElectronChecker(server.Client()).Check(context.Background(), &app.App{ElectronUpdateURL: server.URL})
		server.Close()
		if err == nil {
			t.Fatal("accepted Intel-only file on ARM")
		}
	}
}

func TestGitHubAndSparkleArchitecture(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			setTestArchitecture(t, arch)
			native, wrong := "arm64", "x64"
			if arch == "amd64" {
				native, wrong = wrong, native
			}
			assets := []GitHubAsset{
				{Name: "app-mac-" + wrong + ".zip"}, {Name: "app-mac.zip"},
				{Name: "app-mac-universal.zip"}, {Name: "app-mac-" + native + ".zip"},
			}
			if got := findMacAsset(assets); got.Name != assets[3].Name {
				t.Fatalf("wrong GitHub asset: %+v", got)
			}
			if got := findMacAsset(assets[:3]); got.Name != assets[2].Name {
				t.Fatalf("expected universal fallback, got %+v", got)
			}
			if got := findMacAsset(assets[:2]); got.Name != assets[1].Name {
				t.Fatalf("expected unlabeled fallback, got %+v", got)
			}
			if got := findMacAsset(assets[:1]); got != (GitHubAsset{}) {
				t.Fatal("selected incompatible GitHub asset")
			}
			items := []sparkleItem{
				{Version: "3.0", Enclosure: sparkleEnclosure{URL: "https://example.com/app-" + wrong + ".zip"}},
				{Version: "2.0", Enclosure: sparkleEnclosure{URL: "https://example.com/app-universal.zip"}},
				{Version: "2.0", Enclosure: sparkleEnclosure{URL: "https://example.com/app-" + native + ".zip"}},
			}
			if got := findBestItem(items, "15.0"); got != items[2] {
				t.Fatalf("wrong Sparkle item: %+v", got)
			}
			if got := findBestItem(items[:1], "15.0"); got != (sparkleItem{}) {
				t.Fatal("selected incompatible Sparkle item")
			}
		})
	}
}
