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

func TestInstalledChannelChoosesVendorOrHomebrew(t *testing.T) {
	for _, tc := range []struct {
		id, channel string
	}{
		{id: "com.microsoft.VSCode", channel: "stable"},
		{id: "com.microsoft.VSCodeInsiders", channel: "insider"},
		{id: "com.anthropic.claudefordesktop"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			desktop := func(brew bool) *app.App {
				a := &app.App{BundleID: tc.id, Source: app.SourceElectron, InstalledViaBrew: brew, GitHubRepo: "microsoft/vscode"}
				a.ElectronUpdateURL = "https://example.com/updates"
				a.ElectronUpdateFormat = "vscode"
				a.ElectronUpdateChannel = tc.channel
				if tc.id == "com.anthropic.claudefordesktop" {
					a.ElectronUpdateFormat = "squirrel"
					a.ElectronUpdateChannel = ""
				}
				return a
			}
			names := func(a *app.App) []string {
				var got []string
				for _, c := range checkersForApp(a, BuildCheckers(&checker.MockCmdRunner{}, "")) {
					got = append(got, c.Name())
				}
				return got
			}
			brewOwned := names(desktop(true))
			if len(brewOwned) != 2 || brewOwned[0] != "brew" || brewOwned[1] != "brew-info" {
				t.Fatalf("Homebrew-owned checkers = %v, want brew then brew-info", brewOwned)
			}
			standalone := names(desktop(false))
			if len(standalone) != 1 || standalone[0] != "vendor" {
				t.Fatalf("standalone checkers = %v, want the packaged vendor feed", standalone)
			}
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
