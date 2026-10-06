package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/lu-zhengda/updater/internal/updater"
)

type liveInstallRunner struct{ checker.RealCmdRunner }

func (r *liveInstallRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "open" {
		return nil, fmt.Errorf("live validation must not open any app")
	}
	return r.RealCmdRunner.Run(ctx, name, args...)
}

// Opt-in: supply an older signed VS Code bundle. Only a temporary copy is updated.
func TestLiveVSCodeDirectInstall(t *testing.T) {
	source := os.Getenv("UPDATER_TEST_VSCODE_APP")
	if source == "" {
		t.Skip("set UPDATER_TEST_VSCODE_APP to an older signed VS Code bundle")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runner := &liveInstallRunner{}
	dir := t.TempDir()
	path := filepath.Join(dir, "Visual Studio Code.app")
	if _, err := runner.Run(ctx, "ditto", source, path); err != nil {
		t.Fatal(err)
	}
	apps, err := app.Discover(dir)
	if err != nil || len(apps) != 1 {
		t.Fatalf("Discover: %v", err)
	}
	a := apps[0]
	a.GitHubRepo = "microsoft/vscode" // regression: legacy config mapping
	r := updater.CheckWithFallthrough(ctx, a, buildCheckers(runner, ""))
	if r.Error != nil || !r.HasUpdate || r.Source != "vendor" || describeAction(r) != "direct install" {
		t.Fatalf("invalid update plan: %+v", r)
	}
	t.Logf("Installing signed %s -> %s into %s", r.CurrentVersion, r.LatestVersion, path)
	err, rolledBack := executeUpdate(ctx, r, runner, nil, installer.New(runner, nil))
	if err != nil || rolledBack {
		t.Fatalf("direct install: err=%v rollback=%v", err, rolledBack)
	}
	apps, err = app.Discover(dir)
	if err != nil || len(apps) != 1 || apps[0].Version != r.LatestVersion {
		t.Fatalf("installed version not updated: %+v err=%v", apps, err)
	}
	checked := updater.CheckWithFallthrough(ctx, apps[0], buildCheckers(runner, ""))
	if checked.Error != nil || checked.HasUpdate {
		t.Fatalf("update did not clear after rescan: %+v", checked)
	}
	t.Logf("Installed %s; rescan confirms no pending update. SHA-256, code signature, Team ID, native architecture and Gatekeeper checks passed.", apps[0].Version)
}
