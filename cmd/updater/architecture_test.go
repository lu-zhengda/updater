package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

func TestNativeSuggestionOutput(t *testing.T) {
	r := &checker.UpdateResult{App: &app.App{Name: "Notion", BundleID: "notion.id"}, Source: "electron", CurrentVersion: "7.34.0", LatestVersion: "7.34.0", HasUpdate: true, NativeUpgrade: true}
	results := []*checker.UpdateResult{r}
	cfg := &config.Config{}
	if e := toCheckEntries(results, cfg)[0]; !e.NativeUpgrade || e.Status != "update_available" {
		t.Fatalf("missing CLI suggestion: %+v", e)
	}
	if e := cacheEntriesFromResults(results, cfg.IsPinned)[0]; !e.NativeUpgrade || e.Status != "update_available" {
		t.Fatalf("missing window suggestion: %+v", e)
	}
	if !toOutdatedEntries(results)[0].NativeUpgrade {
		t.Fatal("missing outdated suggestion")
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	printCheckResults(cmd, results, cfg)
	if !strings.Contains(out.String(), "ARM VERSION AVAILABLE") {
		t.Fatalf("missing visible suggestion: %s", out.String())
	}
	cfg.Pin("notion.id")
	if e := cacheEntriesFromResults(results, cfg.IsPinned)[0]; e.Status != "pinned" {
		t.Fatalf("pin ignored: %+v", e)
	}
	if describeAction(r) != "Install ARM version" {
		t.Fatal("incorrect migration action")
	}
	if !strings.Contains(buildNotificationBody(results), "ARM version available") {
		t.Fatal("missing notification suggestion")
	}
}

type nativeReinstallRunner struct{ called bool }

func (r *nativeReinstallRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "pgrep" {
		return nil, fmt.Errorf("not running")
	}
	if name != "brew" || strings.Join(args, " ") != "reinstall --cask notion" {
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
	r.called = true
	return nil, nil
}
func TestNativeSuggestionReinstallsBrewApp(t *testing.T) {
	runner := &nativeReinstallRunner{}
	r := &checker.UpdateResult{App: &app.App{Name: "Notion", Path: "/Applications/Notion.app", CaskName: "notion", InstalledViaBrew: true}, Source: "brew-info", NativeUpgrade: true, HasUpdate: true}
	if err, _ := executeUpdate(context.Background(), r, runner, nil, nil); err != nil || !runner.called {
		t.Fatalf("native reinstall not executed: %v", err)
	}
}
func TestNativeSuggestionExcludedFromScheduledUpdates(t *testing.T) {
	runner := &nativeReinstallRunner{}
	old := newRunner
	newRunner = func() checker.CmdRunner { return runner }
	t.Cleanup(func() { newRunner = old })
	r := &checker.UpdateResult{App: &app.App{Name: "Notion", CaskName: "notion", InstalledViaBrew: true}, Source: "brew-info", NativeUpgrade: true, HasUpdate: true}
	updated, failed := autoUpdateAfterNotify(context.Background(), &config.Config{}, []*checker.UpdateResult{r})
	if runner.called || len(updated) > 0 || len(failed) > 0 {
		t.Fatal("architecture suggestion was auto-installed")
	}
}
