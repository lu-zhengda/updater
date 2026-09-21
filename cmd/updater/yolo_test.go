package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
)

type yoloRunner struct {
	checker.MultiMockCmdRunner
	calls []string
}

func (r *yoloRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return r.MultiMockCmdRunner.Run(ctx, name, args...)
}

func TestYOLOModeChecks(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		notify     bool
		fail       bool
		wantUpdate bool
	}{
		{name: "default off"},
		{name: "explicit off", yaml: "yolo_mode: false"},
		{name: "enabled includes major", yaml: "yolo_mode: true", wantUpdate: true},
		{name: "failure stays available", yaml: "yolo_mode: true", fail: true, wantUpdate: true},
		{name: "pinned", yaml: "yolo_mode: true\npinned_apps: [formula.node]"},
		{name: "ignored", yaml: "yolo_mode: true\nignored_apps: [formula.node]"},
		{name: "manual policy", yaml: "yolo_mode: true\npolicies: {formula.node: manual}"},
		{name: "notify-only policy", yaml: "yolo_mode: true\npolicies: {formula.node: notify-only}"},
		{name: "schedule consent alone does not enable menu bar updates", yaml: "scheduled_auto_update: true"},
		{name: "scheduled default off", notify: true},
		{name: "scheduled YOLO without legacy flag", yaml: "yolo_mode: true\ninteractive_notifications: true", notify: true, wantUpdate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("UPDATER_AGENT_MODE", "0")
			cfg, err := config.Parse([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if tc.yaml != "" {
				if err := cfg.Save(config.DefaultPath()); err != nil {
					t.Fatal(err)
				}
			}
			withStubbedPipeline(t, []*app.App{{Name: "node", BundleID: "formula.node", Version: "1.0.0", Source: app.SourceBrewFormula, FormulaName: "node"}})
			runner := &yoloRunner{MultiMockCmdRunner: checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
				"brew outdated --formula --json": {Output: []byte(`[{"name":"node","installed_versions":"1.0.0","current_version":"2.0.0"}]`)},
			}}}
			if tc.fail {
				runner.Responses["brew upgrade node"] = checker.MockResponse{Err: errors.New("install failed")}
			}
			newRunner = func() checker.CmdRunner { return runner }

			if tc.notify {
				oldAuto, oldInteractive, oldJSON := flagAutoUpdate, flagInteractive, flagNotifyJSON
				flagAutoUpdate, flagInteractive, flagNotifyJSON = false, false, false
				t.Cleanup(func() { flagAutoUpdate, flagInteractive, flagNotifyJSON = oldAuto, oldInteractive, oldJSON })
				notifyCmd.SetContext(context.Background())
				notifyCmd.SetOut(io.Discard)
				t.Cleanup(func() { notifyCmd.SetOut(nil) })
				if err := runNotify(notifyCmd, nil); err != nil {
					t.Fatal(err)
				}
				if cfg.YOLOMode && strings.Contains(strings.Join(runner.calls, "\n"), "display dialog") {
					t.Fatal("YOLO Mode must not wait for an interactive dialog")
				}
			} else {
				menu := &menubarApp{}
				remaining, err := menu.runCheck(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				wantRemaining := 1
				if cfg.IsIgnored("formula.node") || cfg.IsPinned("formula.node") || (tc.wantUpdate && !tc.fail) {
					wantRemaining = 0
				}
				if len(remaining) != wantRemaining {
					t.Fatalf("remaining updates = %d, want %d", len(remaining), wantRemaining)
				}
				if tc.wantUpdate && !tc.fail {
					cache := readCheckCache()
					if cache == nil || len(cache.Entries) != 1 || cache.Entries[0].Status != "ok" || cache.Entries[0].Current != "2.0.0" {
						t.Fatalf("cache does not reflect successful update: %+v", cache)
					}
				}
			}
			if got := slices.Contains(runner.calls, "brew upgrade node"); got != tc.wantUpdate {
				t.Fatalf("update attempted = %t, want %t; commands: %v", got, tc.wantUpdate, runner.calls)
			}
			entries, err := history.List(history.DefaultPath())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantUpdate {
				if len(entries) != 1 || entries[0].Success == tc.fail || entries[0].FromVersion != "1.0.0" || entries[0].ToVersion != "2.0.0" {
					t.Fatalf("incorrect update history: %+v", entries)
				}
			} else if len(entries) != 0 {
				t.Fatalf("unexpected update history: %+v", entries)
			}
		})
	}
}

func TestYOLOModeLeavesManualActionsAvailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runner := &yoloRunner{MultiMockCmdRunner: checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
		"mas upgrade 123": {Err: errors.New("App Store sign-in required")},
	}}}
	old := newRunner
	newRunner = func() checker.CmdRunner { return runner }
	t.Cleanup(func() { newRunner = old })
	results := []*checker.UpdateResult{
		{App: &app.App{Name: "MAS app", MASID: "123"}, Source: "mas", HasUpdate: true},
		{App: &app.App{Name: "ARM app", FormulaName: "arm"}, Source: "formula", HasUpdate: true, NativeUpgrade: true},
		{App: &app.App{Name: "Manual download"}, Source: "github", HasUpdate: true},
		{App: &app.App{Name: "macOS"}, Source: "system", HasUpdate: true},
		{App: &app.App{Name: "Failed check", FormulaName: "failed"}, Source: "formula", HasUpdate: true, Error: errors.New("check failed")},
	}
	updated, failed := autoUpdateAfterNotify(context.Background(), &config.Config{YOLOMode: true}, results)
	if len(updated) != 0 || len(failed) != 1 || failed[0] != "MAS app" {
		t.Fatalf("incorrect result: updated %v, failed %v", updated, failed)
	}
	for _, r := range results {
		if !r.HasUpdate {
			t.Errorf("%s was incorrectly marked updated", r.App.Name)
		}
	}
	if len(runner.calls) != 3 { // mas attempt, App Store fallback, completion notification
		t.Fatalf("unexpected update attempts: %v", runner.calls)
	}
}
