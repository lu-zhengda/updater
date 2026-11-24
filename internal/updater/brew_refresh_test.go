package updater

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
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
