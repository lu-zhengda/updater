package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/lu-zhengda/updater/internal/updater"
)

// Opt-in: use the official 0.1.32-alpha app; only a temporary copy is updated.
func TestLiveNuvioDirectInstall(t *testing.T) {
	source := os.Getenv("UPDATER_TEST_NUVIO_APP")
	if source == "" {
		t.Skip("set UPDATER_TEST_NUVIO_APP to Nuvio's official 0.1.32-alpha bundle")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runner := &liveInstallRunner{}
	dir := t.TempDir()
	target := filepath.Join(dir, "Nuvio.app")
	if _, err := runner.Run(ctx, "ditto", source, target); err != nil {
		t.Fatal(err)
	}
	discover := func() *app.App {
		t.Helper()
		apps, err := app.Discover(dir)
		if err != nil || len(apps) != 1 || apps[0].BundleID != "com.nuvio.media.desktop" {
			t.Fatalf("Discover: apps=%+v error=%v", apps, err)
		}
		return apps[0]
	}
	a := discover()
	if a.Version != "1.1.32" {
		t.Fatalf("fixture version = %q, want 1.1.32", a.Version)
	}
	cfg, err := config.Parse([]byte(`source_overrides:
  com.nuvio.media.desktop:
    kind: github
    repo: NuvioMedia/NuvioDesktop
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := updater.EnrichApps(ctx, []*app.App{a}, cfg, runner); err != nil {
		t.Fatal(err)
	}
	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	before := updater.CheckWithFallthrough(ctx, a, checkers)
	if before.Error != nil || before.HasUpdate || before.LatestVersion != "0.1.33-alpha" {
		t.Fatalf("could not reproduce the tag mismatch: %+v", before)
	}
	t.Logf("Reproduced: installed %s, release tag %s, HasUpdate=%v", before.CurrentVersion, before.LatestVersion, before.HasUpdate)

	cfg.SourceOverrides[a.BundleID].VersionSource = "bundle"
	if _, err := updater.EnrichApps(ctx, []*app.App{a}, cfg, runner); err != nil {
		t.Fatal(err)
	}
	result := updater.CheckWithFallthrough(ctx, a, checkers)
	if result.Error != nil || !result.HasUpdate || result.LatestVersion != "1.1.33" || describeAction(result) != "direct install" {
		t.Fatalf("bundle version did not produce the expected update plan: %+v", result)
	}
	t.Logf("Bundle mode: %s -> %s, action=%s", result.CurrentVersion, result.LatestVersion, describeAction(result))
	err, rolledBack := executeUpdate(ctx, result, runner, nil, installer.New(runner, nil))
	installed := discover()
	if err != nil || rolledBack {
		t.Logf("After unsuccessful installation: installed version=%s, rollback=%v", installed.Version, rolledBack)
		if installed.Version != "1.1.32" {
			t.Fatal("failed installation changed the existing app")
		}
		if _, verifyErr := runner.Run(ctx, "codesign", "--verify", "--deep", "--strict", target); verifyErr != nil {
			t.Fatalf("failed installation damaged the existing app: %v", verifyErr)
		}
		t.Fatalf("direct installation did not complete: %v", err)
	}
	if installed.Version != "1.1.33" {
		t.Fatalf("installed version = %q, want 1.1.33", installed.Version)
	}
	if _, err := updater.EnrichApps(ctx, []*app.App{installed}, cfg, runner); err != nil {
		t.Fatal(err)
	}
	checked := updater.CheckWithFallthrough(ctx, installed, checkers)
	if checked.Error != nil || checked.HasUpdate {
		t.Fatalf("update did not clear after rescan: %+v", checked)
	}
	t.Log("Installed 1.1.33; rescan confirms no pending update. Digest, signing identity, architecture and Gatekeeper checks passed.")
}
