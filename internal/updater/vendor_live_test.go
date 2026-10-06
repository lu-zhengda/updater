package updater

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
)

// Opt-in: exercises public vendor services without changing installed apps.
func TestLiveVendorFeeds(t *testing.T) {
	if os.Getenv("UPDATER_LIVE_TESTS") != "1" {
		t.Skip("set UPDATER_LIVE_TESTS=1 to query vendor services")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	apps, err := app.Discover("/Applications")
	if err != nil {
		t.Fatal(err)
	}
	apps = append(apps, &app.App{Name: "Claude", BundleID: "com.anthropic.claudefordesktop", Version: "2.19674.0", Source: app.SourceElectron,
		ElectronUpdateURL: "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json", ElectronUpdateFormat: "squirrel"})
	checked := 0
	for _, a := range apps {
		want := "vendor"
		switch a.BundleID {
		case "com.openai.codex":
			want = "sparkle"
		case "com.microsoft.VSCode", "com.anthropic.claudefordesktop":
		default:
			continue
		}
		// Mirror the user's legacy VS Code mapping; it must not hide the vendor artifact.
		if a.BundleID == "com.microsoft.VSCode" {
			a.GitHubRepo = "microsoft/vscode"
		}
		r := CheckWithFallthrough(ctx, a, BuildCheckers(&checker.MockCmdRunner{}, ""))
		if r.Error != nil || r.Source != want || r.DownloadURL == "" {
			t.Fatalf("%s: %+v", a.Name, r)
		}
		t.Logf("%s: source=%s installed=%s latest=%s update=%v artifact=%s", a.Name, r.Source, r.CurrentVersion, r.LatestVersion, r.HasUpdate, r.DownloadURL)
		checked++
	}
	if checked != 3 {
		t.Fatalf("expected installed Codex, VS Code and synthetic Claude; checked %d", checked)
	}
}
