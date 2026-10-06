package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

func TestVendorCheckerPrecedesVersionOnlySources(t *testing.T) {
	for _, id := range []string{"com.microsoft.VSCode", "com.microsoft.VSCodeInsiders", "com.anthropic.claudefordesktop"} {
		t.Run(id, func(t *testing.T) {
			a := &app.App{BundleID: id, Source: app.SourceElectron, CaskName: "test", InstalledViaBrew: true, GitHubRepo: "microsoft/vscode"}
			a.ElectronUpdateURL = "https://example.com/updates"
			a.ElectronUpdateFormat = "vscode"
			if id == "com.anthropic.claudefordesktop" {
				a.ElectronUpdateFormat = "squirrel"
			}
			for _, c := range BuildCheckers(&checker.MockCmdRunner{}, "") {
				if c.CanCheck(a) {
					if c.Name() != "vendor" {
						t.Fatalf("first source = %s; want native vendor feed before cask/GitHub metadata", c.Name())
					}
					return
				}
			}
			t.Fatal("no checker for vendor app")
		})
	}
}

func TestVersionOnlyGitHubReleaseFallsThroughToInstaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"1.140.0","assets":[]}`)
	}))
	defer server.Close()
	runner := &checker.MockCmdRunner{Output: []byte(`{"casks":[{"version":"1.140.0","url":"https://example.com/Code.zip"}]}`)}
	a := &app.App{Name: "Code", Version: "1.139.0", GitHubRepo: "microsoft/vscode", CaskName: "visual-studio-code"}
	r := CheckWithFallthrough(context.Background(), a, []checker.Checker{
		checker.NewGitHubChecker(server.Client(), server.URL, ""), checker.NewBrewInfoChecker(runner),
	})
	if r.Error != nil || r.Source != "brew-info" || !r.HasUpdate || r.DownloadURL == "" {
		t.Fatalf("version-only source hid installer: %+v", r)
	}
}

func TestExplicitOverrideStillWinsOverNativeFeed(t *testing.T) {
	a := &app.App{Source: app.SourceElectron, BundleID: "com.microsoft.VSCode",
		ElectronUpdateURL: "https://update.code.visualstudio.com", ElectronUpdateFormat: "vscode"}
	applyExplicitSourceOverride(a, &config.SourceOverrideConfig{Kind: config.SourceOverrideKindGitHub, Repo: "example/custom"})
	cs := checkersForApp(a, BuildCheckers(&checker.MockCmdRunner{}, ""))
	if len(cs) != 1 || cs[0].Name() != "github" {
		t.Fatalf("explicit source override was ignored: %v", cs)
	}
}
