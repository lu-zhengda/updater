package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

type staleBrewRunner struct {
	refreshes      atomic.Int32
	lookups        atomic.Int32
	offline        bool
	unblockRefresh chan struct{}
}

func (r *staleBrewRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name + " " + strings.Join(args, " ") {
	case "brew update --quiet":
		r.refreshes.Add(1)
		if r.unblockRefresh != nil {
			<-r.unblockRefresh
		}
		if r.offline {
			return nil, errors.New("tap unavailable")
		}
		return nil, nil
	case "brew outdated --cask --greedy --json", "brew outdated --formula --json":
		r.lookups.Add(1)
		if r.refreshes.Load() == 0 {
			return []byte("[]"), nil
		}
		return []byte(`[{"name":"owner/tap/tool","current_version":"2.1.0"}]`), nil
	case "brew info --cask --json=v2 owner/tap/tool":
		r.lookups.Add(1)
		return []byte(`{"casks":[{"version":"2.1.0"}]}`), nil
	default:
		return nil, errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
	}
}

func TestHomebrewMetadataRefresh(t *testing.T) {
	const token = "owner/tap/tool"
	apps := []*app.App{
		{Source: app.SourceBrew, CaskName: token, InstalledViaBrew: true, Version: "2.0.1"},
		{Source: app.SourceBrewFormula, FormulaName: token, Version: "2.0.1"},
		{Source: app.SourceBrewInfo, CaskName: token, Version: "2.0.1"},
	}
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "")
	runner := &staleBrewRunner{}
	for run := int32(1); run <= 2; run++ {
		results := CheckAll(t.Context(), apps, BuildCheckers(runner, ""), len(apps))
		if len(results) != len(apps) || runner.refreshes.Load() != run {
			t.Fatalf("run %d: %d results, %d refreshes", run, len(results), runner.refreshes.Load())
		}
		for _, result := range results {
			if result.Error != nil || !result.HasUpdate || result.LatestVersion != "2.1.0" {
				t.Fatalf("run %d: %+v", run, result)
			}
		}
	}

	runner = &staleBrewRunner{offline: true}
	current := &app.App{Source: app.SourceBrewFormula, FormulaName: token, Version: "2.1.0"}
	results := CheckAll(t.Context(), append(apps, current), BuildCheckers(runner, ""), len(apps))
	for _, result := range results {
		if result.App == current {
			if result.Error == nil || !strings.Contains(result.Error.Error(), "tap unavailable") {
				t.Fatalf("reported current after a failed refresh: %+v", result)
			}
		} else if result.Error != nil || !result.HasUpdate {
			t.Fatalf("lost known upgrade after a failed refresh: %+v", result)
		}
	}
	if runner.refreshes.Load() != 1 {
		t.Fatalf("failed refresh ran %d times", runner.refreshes.Load())
	}

	runner = &staleBrewRunner{}
	managed := &app.App{Source: app.SourceSetapp}
	result := CheckWithFallthrough(t.Context(), managed, BuildCheckers(runner, ""))
	if result.Error != nil || runner.refreshes.Load() != 0 {
		t.Fatalf("managed app triggered Homebrew: %+v", result)
	}
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "1")
	result = CheckWithFallthrough(t.Context(), apps[0], BuildCheckers(runner, ""))
	if result.Error != nil || result.HasUpdate || runner.refreshes.Load() != 0 || runner.lookups.Load() != 1 {
		t.Fatalf("expected local metadata when refresh disabled: %+v", result)
	}
}

func TestHomebrewRefreshLeavesSlotsForOtherChecks(t *testing.T) {
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "")
	runner := &staleBrewRunner{unblockRefresh: make(chan struct{})}
	apps := []*app.App{
		{Source: app.SourceBrewFormula, FormulaName: "owner/tap/tool"},
		{Source: app.SourceBrew, CaskName: "owner/tap/tool", InstalledViaBrew: true},
		{Source: app.SourceSetapp},
	}
	independent := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		CheckAllProgress(t.Context(), apps, BuildCheckers(runner, ""), 2, func(r *checker.UpdateResult) {
			if r.App.Source == app.SourceSetapp {
				close(independent)
			}
		})
	}()
	defer func() { close(runner.unblockRefresh); <-done }()
	select {
	case <-independent:
	case <-time.After(2 * time.Second):
		t.Fatal("independent check blocked behind Homebrew refresh")
	}
}

func TestNewCaskInStaleTap(t *testing.T) {
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "")
	r := &newCaskRunner{}
	a := &app.App{Name: "NewTool", BundleID: "com.example.newtool", Source: app.SourceUnknown, Version: "1.0.0"}
	apps, err := enrichApps(t.Context(), []*app.App{a}, &config.Config{}, r, true)
	if err != nil {
		t.Fatal(err)
	}
	results := CheckAll(t.Context(), apps, BuildCheckers(r, ""), 2)
	if len(results) != 1 || results[0].Error != nil || !results[0].HasUpdate || results[0].LatestVersion != "2.0.0" || results[0].DownloadURL != "https://example.org/newtool.dmg" || r.refreshes != 1 {
		t.Fatalf("new cask missing on first check: %v, refreshes=%d", results, r.refreshes)
	}
}

func TestMissingCaskRemainsAbsent(t *testing.T) {
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "")
	r := &newCaskRunner{}
	a := &app.App{Name: "NoCask", Source: app.SourceUnknown, Version: "1.0.0"}
	apps, err := enrichApps(t.Context(), []*app.App{a}, &config.Config{}, r, true)
	if err != nil {
		t.Fatal(err)
	}
	if results := CheckAll(t.Context(), apps, BuildCheckers(r, ""), 2); len(results) != 0 || r.refreshes != 1 {
		t.Fatalf("unmatched cask: %v, refreshes=%d", results, r.refreshes)
	}
}

type newCaskRunner struct{ refreshes int }

func (r *newCaskRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	switch name + " " + strings.Join(args, " ") {
	case "brew --caskroom":
		return nil, nil
	case "brew update --quiet":
		r.refreshes++
		return nil, nil
	case "brew info --cask --json=v2 newtool":
		if r.refreshes > 0 {
			return []byte(`{"casks":[{"token":"newtool","version":"2.0.0","url":"https://example.org/newtool.dmg","artifacts":[{"app":["NewTool.app"]}]}]}`), nil
		}
	}
	return nil, errors.New("cask unavailable")
}

func TestHomebrewRefreshLockContention(t *testing.T) {
	t.Setenv("HOMEBREW_NO_AUTO_UPDATE", "")
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("BREW_TEST_STATE", dir)
	const script = `#!/bin/sh
case "$1" in
update)
 if [ ! -f "$BREW_TEST_STATE/busy" ]; then
   touch "$BREW_TEST_STATE/busy"
   echo 'Another ` + "`brew update`" + ` process is already running.' >&2
   exit 1
 fi
 touch "$BREW_TEST_STATE/fresh"
 ;;
outdated)
 if [ -f "$BREW_TEST_STATE/fresh" ]; then
   echo '[{"name":"owner/tap/tool","current_version":"2.1.0"}]'
 else
   echo '[]'
 fi
 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	a := &app.App{Source: app.SourceBrewFormula, FormulaName: "owner/tap/tool", Version: "2.0.1"}
	got := CheckWithFallthrough(t.Context(), a, BuildCheckers(&checker.RealCmdRunner{}, ""))
	if got.Error != nil || got.LatestVersion != "2.1.0" || !got.HasUpdate {
		t.Fatalf("overlapping check: %+v", got)
	}
}
