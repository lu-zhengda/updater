package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

type selectorUpdateRunner struct {
	checker.MultiMockCmdRunner
	mu       sync.Mutex
	installs []string
}

func (r *selectorUpdateRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "pgrep" {
		return nil, fmt.Errorf("app is not running")
	}
	if name == "brew" && len(args) > 0 && args[0] == "upgrade" {
		r.mu.Lock()
		r.installs = append(r.installs, strings.Join(args, " "))
		r.mu.Unlock()
	}
	return r.MultiMockCmdRunner.Run(ctx, name, args...)
}

func setupSelectorUpdate(t *testing.T, cfg *config.Config) (*cobra.Command, *bytes.Buffer, *selectorUpdateRunner, []*app.App) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATER_AGENT_MODE", "0")
	t.Setenv("GITHUB_TOKEN", "")
	if err := cfg.Save(config.DefaultPath()); err != nil {
		t.Fatal(err)
	}
	apps := []*app.App{
		{Name: "Microsoft Teams", BundleID: "com.microsoft.teams2", Version: "1.0.0", CaskName: "microsoft-teams", InstalledViaBrew: true},
		{Name: "LibreOffice", BundleID: "org.libreoffice", Version: "1.0.0", CaskName: "libreoffice", InstalledViaBrew: true},
	}
	withStubbedPipeline(t, apps)
	runner := &selectorUpdateRunner{MultiMockCmdRunner: checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
		"brew outdated --cask --greedy --json": {Output: []byte(`[{"name":"microsoft-teams","current_version":"1.1.0"},{"name":"libreoffice","current_version":"1.1.0"}]`)},
	}}}
	newRunner = func() checker.CmdRunner { return runner }
	oldAll, oldAuto, oldDryRun, oldJSON, oldBundleID := flagAll, flagAuto, flagDryRun, flagDryRunJSON, flagBundleID
	t.Cleanup(func() {
		flagAll, flagAuto, flagDryRun, flagDryRunJSON, flagBundleID = oldAll, oldAuto, oldDryRun, oldJSON, oldBundleID
	})
	flagAll, flagAuto, flagDryRun, flagDryRunJSON, flagBundleID = false, true, false, false, ""
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	return cmd, out, runner, apps
}

// Issue #13: --auto must preserve an explicit selector in both the preview and
// the install path. All external commands and discovered apps are fake.
func TestRunUpdateAutoSelector(t *testing.T) {
	for _, selector := range []string{"name", "unquoted name", "bundle ID", "bundle ID with duplicate name", "bundle ID takes precedence"} {
		for _, mode := range []string{"install", "dry run", "JSON dry run"} {
			t.Run(selector+"/"+mode, func(t *testing.T) {
				cmd, out, runner, apps := setupSelectorUpdate(t, &config.Config{})
				args := []string{"Microsoft Teams"}
				switch selector {
				case "unquoted name":
					args = []string{"Microsoft", "Teams"}
				case "bundle ID", "bundle ID with duplicate name", "bundle ID takes precedence":
					flagBundleID = apps[0].BundleID
					args = nil
					if selector == "bundle ID with duplicate name" {
						apps[1].Name = apps[0].Name
					}
					if selector == "bundle ID takes precedence" {
						args = []string{"LibreOffice"}
					}
				}
				flagDryRun = mode != "install"
				flagDryRunJSON = mode == "JSON dry run"
				if err := runUpdate(cmd, args); err != nil {
					t.Fatal(err)
				}
				if flagDryRun {
					if len(runner.installs) != 0 {
						t.Fatalf("dry run attempted installs: %v", runner.installs)
					}
					if flagDryRunJSON {
						var entries []dryRunEntry
						if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
							t.Fatalf("invalid JSON: %v\n%s", err, out.String())
						}
						if len(entries) != 1 || entries[0].Action != "brew upgrade --cask microsoft-teams" {
							t.Fatalf("unexpected update plan: %+v", entries)
						}
					} else if !strings.Contains(out.String(), "1 update(s) would be applied") || strings.Contains(out.String(), "libreoffice") {
						t.Fatalf("unexpected update plan: %s", out.String())
					}
				} else if !slices.Equal(runner.installs, []string{"upgrade --cask microsoft-teams"}) {
					t.Fatalf("unexpected installs: %v", runner.installs)
				}
				entries, err := history.List(history.DefaultPath())
				if err != nil {
					t.Fatal(err)
				}
				if flagDryRun && len(entries) != 0 {
					t.Fatalf("dry run wrote update history: %+v", entries)
				}
				if !flagDryRun && (len(entries) != 1 || entries[0].BundleID != apps[0].BundleID || !entries[0].Success) {
					t.Fatalf("unexpected update history: %+v", entries)
				}
			})
		}
	}
}

func TestRunUpdateAutoSelectorExcluded(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, want string
		major            bool
		native           bool
		system           bool
		current          bool
	}{
		{name: "pinned", yaml: "pinned_apps: [com.microsoft.teams2]", want: "pinned"},
		{name: "manual policy", yaml: "policies: {com.microsoft.teams2: manual}", want: "manual policy"},
		{name: "notify-only policy", yaml: "policies: {com.microsoft.teams2: notify-only}", want: "notify-only policy"},
		{name: "major update", major: true, want: "major update"},
		{name: "major update even with YOLO", yaml: "yolo_mode: true", major: true, want: "major update"},
		{name: "ARM migration", native: true, want: "ARM migration"},
		{name: "external source", system: true, want: "system source"},
		{name: "up to date", current: true, want: "is up to date"},
	} {
		for _, selector := range []string{"name", "bundle ID"} {
			for _, dryRun := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/dry-run=%t", tc.name, selector, dryRun), func(t *testing.T) {
					if tc.native && architecture.Native() != "arm64" {
						t.Skip("ARM migration suggestions require Apple Silicon")
					}
					cfg, err := config.Parse([]byte(tc.yaml))
					if err != nil {
						t.Fatal(err)
					}
					cmd, out, runner, apps := setupSelectorUpdate(t, cfg)
					if tc.major {
						apps[0].Version = "0.9.0"
					}
					if tc.current {
						apps[0].Version = "1.1.0"
					}
					if tc.native {
						apps[0].IntelOnly = true
						apps[0].InstalledViaBrew = false
						runner.Responses["brew info --cask --json=v2 microsoft-teams"] = checker.MockResponse{Output: []byte(`{"casks":[{"version":"1.0.0","url":"https://example.com/teams-arm64.dmg"}]}`)}
					}
					if tc.system {
						*apps[0] = app.App{Name: "macOS", BundleID: "com.apple.macOS", Version: "27.0", Source: app.SourceSystem}
						runner.Responses["softwareupdate -l"] = checker.MockResponse{Output: []byte("* Label: macOS 27.1\n")}
					}
					args := []string{apps[0].Name}
					if selector == "bundle ID" {
						args = nil
						flagBundleID = apps[0].BundleID
					}
					flagDryRun = dryRun
					if err := runUpdate(cmd, args); err != nil {
						t.Fatal(err)
					}
					if len(runner.installs) != 0 {
						t.Fatalf("excluded selector updated unrelated apps: %v", runner.installs)
					}
					if !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), "LibreOffice") || (!tc.current && strings.Contains(out.String(), "up to date")) {
						t.Fatalf("expected selected app status %q, got %q", tc.want, out.String())
					}
				})
			}
		}
	}
}

func TestRunUpdateAutoSelectorSkippedJSON(t *testing.T) {
	for _, agentMode := range []bool{false, true} {
		t.Run(fmt.Sprintf("agent-mode=%t", agentMode), func(t *testing.T) {
			cmd, out, runner, apps := setupSelectorUpdate(t, &config.Config{PinnedApps: []string{"com.microsoft.teams2"}})
			var diagnostics bytes.Buffer
			cmd.SetErr(&diagnostics)
			flagBundleID, flagDryRun, flagDryRunJSON = apps[0].BundleID, true, !agentMode
			if agentMode {
				t.Setenv("UPDATER_AGENT_MODE", "1")
			}
			if err := runUpdate(cmd, nil); err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out.String()) != "[]" || !strings.Contains(diagnostics.String(), "Skipping Microsoft Teams (pinned; --auto)") {
				t.Fatalf("expected an empty JSON plan with a skip diagnostic, got stdout %q and stderr %q", out.String(), diagnostics.String())
			}
			if len(runner.installs) != 0 {
				t.Fatalf("skipped selector attempted installs: %v", runner.installs)
			}
		})
	}
}

func TestRunUpdateSelectorWithoutAuto(t *testing.T) {
	cmd, _, runner, _ := setupSelectorUpdate(t, &config.Config{PinnedApps: []string{"com.microsoft.teams2"}})
	flagAuto = false
	if err := runUpdate(cmd, []string{"Microsoft Teams"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(runner.installs, []string{"upgrade --cask microsoft-teams"}) {
		t.Fatalf("explicit update should still allow a pinned app without --auto: %v", runner.installs)
	}
}

func TestRunUpdateAutoWithoutSelector(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%t", all), func(t *testing.T) {
			cmd, _, runner, _ := setupSelectorUpdate(t, &config.Config{})
			flagAll = all
			if err := runUpdate(cmd, nil); err != nil {
				t.Fatal(err)
			}
			slices.Sort(runner.installs)
			if !slices.Equal(runner.installs, []string{"upgrade --cask libreoffice", "upgrade --cask microsoft-teams"}) {
				t.Fatalf("unexpected bulk installs: %v", runner.installs)
			}
		})
	}
}
