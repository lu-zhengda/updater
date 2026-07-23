package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

// createFakeBackup manually creates a backup directory structure that
// backup.Manager.HasBackup/Restore can find.
func createFakeBackup(t *testing.T, baseDir, appName, appPath string) {
	t.Helper()
	safeName := appName // backup uses sanitizeName (lowercase)
	timestamp := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(baseDir, safeName, timestamp)
	os.MkdirAll(backupDir, 0o755)

	// Create fake backup app directory.
	backupApp := filepath.Join(backupDir, filepath.Base(appPath))
	os.MkdirAll(filepath.Join(backupApp, "Contents"), 0o755)
	os.WriteFile(filepath.Join(backupApp, "Contents", "Info.plist"), []byte("<plist/>"), 0o644)

	// Write metadata.
	meta := backup.Metadata{
		AppName:    appName,
		BundleID:   "com.test.app",
		Version:    "1.0",
		BackupPath: backupApp,
		BackupDate: time.Now(),
		OrigPath:   appPath,
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(backupDir, "metadata.json"), data, 0o644)
}

func TestRollback_SparkleInstallFails(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")
	appPath := filepath.Join(tmpDir, "TestApp.app")
	runner := &checker.MockCmdRunner{Output: nil}
	bm := backup.NewManager(backupDir, 1, runner)

	// Create fake backup structure.
	createFakeBackup(t, backupDir, "testapp", appPath)

	if !bm.HasBackup("TestApp") {
		t.Fatal("expected HasBackup=true after creating backup")
	}

	// Rollback should succeed (Restore calls cp -a which the mock runner ignores but returns nil).
	rolledBack := rollbackAfterFailedInstall(context.Background(), bm, "TestApp")
	if !rolledBack {
		t.Error("expected rollback to succeed")
	}
}

func TestRollback_NotTriggeredForBrew(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")
	runner := &checker.MockCmdRunner{
		Err: fmt.Errorf("brew upgrade failed"),
	}
	bm := backup.NewManager(backupDir, 1, runner)

	result := &checker.UpdateResult{
		App: &app.App{
			Name:             "Firefox",
			BundleID:         "org.mozilla.firefox",
			CaskName:         "firefox",
			InstalledViaBrew: true,
			Path:             "/Applications/Firefox.app",
		},
		Source:         "brew",
		CurrentVersion: "120.0",
		LatestVersion:  "121.0",
	}

	err, rolledBack := executeUpdate(context.Background(), result, runner, bm, nil)
	if err == nil {
		t.Error("expected error from brew upgrade")
	}
	if rolledBack {
		t.Error("brew failures should NOT trigger rollback")
	}
}

func TestRollback_RestoreFails(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")
	appPath := filepath.Join(tmpDir, "TestApp.app")
	runner := &checker.MockCmdRunner{
		Err: fmt.Errorf("cp failed"),
	}
	bm := backup.NewManager(backupDir, 1, runner)

	// Create backup structure, but runner will fail on Restore's cp.
	createFakeBackup(t, backupDir, "testapp", appPath)

	rolledBack := rollbackAfterFailedInstall(context.Background(), bm, "TestApp")
	if rolledBack {
		t.Error("expected rollback to fail when restore cp fails")
	}
}

func TestRollback_NoBackup(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backups")
	runner := &checker.MockCmdRunner{Output: nil}
	bm := backup.NewManager(backupDir, 1, runner)

	rolledBack := rollbackAfterFailedInstall(context.Background(), bm, "NonExistentApp")
	if rolledBack {
		t.Error("expected rollback to return false when no backup exists")
	}
}

func TestRollback_NilManager(t *testing.T) {
	rolledBack := rollbackAfterFailedInstall(context.Background(), nil, "TestApp")
	if rolledBack {
		t.Error("expected rollback to return false with nil manager")
	}
}

func TestExecuteUpdate_BrewInfoFallsBackToSelfUpdateWithoutInstaller(t *testing.T) {
	runner := &checker.MockCmdRunner{}
	result := &checker.UpdateResult{
		App: &app.App{
			Name:     "Visual Studio Code",
			CaskName: "visual-studio-code",
			Path:     "/Applications/Visual Studio Code.app",
		},
		Source:         "brew-info",
		CurrentVersion: "1.129.0",
		LatestVersion:  "1.130.0",
		DownloadURL:    "https://example.com/vscode.zip",
	}

	err, rolledBack := executeUpdate(context.Background(), result, runner, nil, nil)
	if !errors.Is(err, checker.ErrOpenedExternally) {
		t.Errorf("expected ErrOpenedExternally fallback, got %v", err)
	}
	if rolledBack {
		t.Error("fallback to self-update should not report a rollback")
	}
}

func TestCaskDirectInstall(t *testing.T) {
	tests := []struct {
		name   string
		result *checker.UpdateResult
		want   bool
	}{
		{
			name: "brew-info app not managed by brew with URL and path",
			result: &checker.UpdateResult{
				App:         &app.App{CaskName: "vscode", Path: "/Applications/Code.app"},
				Source:      "brew-info",
				DownloadURL: "https://example.com/code.zip",
			},
			want: true,
		},
		{
			name: "brew-managed app uses brew upgrade",
			result: &checker.UpdateResult{
				App:         &app.App{CaskName: "vscode", InstalledViaBrew: true, Path: "/Applications/Code.app"},
				Source:      "brew-info",
				DownloadURL: "https://example.com/code.zip",
			},
			want: false,
		},
		{
			name: "no download URL",
			result: &checker.UpdateResult{
				App:    &app.App{CaskName: "vscode", Path: "/Applications/Code.app"},
				Source: "brew-info",
			},
			want: false,
		},
		{
			name: "other source",
			result: &checker.UpdateResult{
				App:         &app.App{Path: "/Applications/Code.app"},
				Source:      "sparkle",
				DownloadURL: "https://example.com/code.zip",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := caskDirectInstall(tt.result); got != tt.want {
				t.Errorf("caskDirectInstall() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDescribeAction(t *testing.T) {
	tests := []struct {
		name   string
		result *checker.UpdateResult
		want   string
	}{
		{
			name: "brew cask installed via brew",
			result: &checker.UpdateResult{
				App:    &app.App{CaskName: "firefox", InstalledViaBrew: true},
				Source: "brew",
			},
			want: "brew upgrade --cask firefox",
		},
		{
			name: "brew not installed via brew",
			result: &checker.UpdateResult{
				App:    &app.App{CaskName: "firefox", InstalledViaBrew: false},
				Source: "brew",
			},
			want: "open app for self-update",
		},
		{
			name: "brew-info installed via brew",
			result: &checker.UpdateResult{
				App:    &app.App{CaskName: "iterm2", InstalledViaBrew: true},
				Source: "brew-info",
			},
			want: "brew upgrade --cask iterm2",
		},
		{
			name: "brew-info not installed via brew",
			result: &checker.UpdateResult{
				App:    &app.App{InstalledViaBrew: false},
				Source: "brew-info",
			},
			want: "open app for self-update",
		},
		{
			name: "brew-info not installed via brew with download URL",
			result: &checker.UpdateResult{
				App:         &app.App{CaskName: "visual-studio-code", InstalledViaBrew: false, Path: "/Applications/Visual Studio Code.app"},
				Source:      "brew-info",
				DownloadURL: "https://example.com/vscode.zip",
			},
			want: "direct install",
		},
		{
			name: "mas with MASID",
			result: &checker.UpdateResult{
				App:    &app.App{MASID: "441258766"},
				Source: "mas",
			},
			want: "mas upgrade 441258766",
		},
		{
			name: "mas without MASID",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "mas",
			},
			want: "open App Store",
		},
		{
			name: "formula",
			result: &checker.UpdateResult{
				App:    &app.App{FormulaName: "node"},
				Source: "formula",
			},
			want: "brew upgrade node",
		},
		{
			name: "system",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "system",
			},
			want: "open Software Update",
		},
		{
			name: "sparkle with download URL and path",
			result: &checker.UpdateResult{
				App:         &app.App{Path: "/Applications/App.app"},
				Source:      "sparkle",
				DownloadURL: "https://example.com/app.dmg",
			},
			want: "direct install",
		},
		{
			name: "sparkle without download URL",
			result: &checker.UpdateResult{
				App:    &app.App{Path: "/Applications/App.app"},
				Source: "sparkle",
			},
			want: "open download URL",
		},
		{
			name: "github with download URL and path",
			result: &checker.UpdateResult{
				App:         &app.App{Path: "/Applications/App.app"},
				Source:      "github",
				DownloadURL: "https://github.com/owner/repo/releases/download/v1.0/app.dmg",
			},
			want: "direct install",
		},
		{
			name: "github without download URL",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "github",
			},
			want: "open download URL",
		},
		{
			name: "electron with download URL and path",
			result: &checker.UpdateResult{
				App:         &app.App{Path: "/Applications/App.app"},
				Source:      "electron",
				DownloadURL: "https://example.com/app.dmg",
			},
			want: "direct install",
		},
		{
			name: "electron without download URL",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "electron",
			},
			want: "open app for self-update",
		},
		{
			name: "setapp",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "setapp",
			},
			want: "open Setapp",
		},
		{
			name: "toolbox",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "toolbox",
			},
			want: "open JetBrains Toolbox",
		},
		{
			name: "adobe",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "adobe",
			},
			want: "open Adobe Creative Cloud",
		},
		{
			name: "unknown source",
			result: &checker.UpdateResult{
				App:    &app.App{},
				Source: "something-else",
			},
			want: "unsupported source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeAction(tt.result)
			if got != tt.want {
				t.Errorf("describeAction() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintDryRun_Table(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	cfg := &config.Config{}

	updatable := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox", CaskName: "firefox", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "120.0",
			LatestVersion:  "121.0",
		},
		{
			App:            &app.App{Name: "Keynote", BundleID: "com.apple.iWork.Keynote", MASID: "409183694"},
			Source:         "mas",
			CurrentVersion: "13.0",
			LatestVersion:  "14.0",
		},
	}

	err := printDryRun(cmd, updatable, false, cfg, false)
	if err != nil {
		t.Fatalf("printDryRun() error = %v", err)
	}

	out := buf.String()

	// Verify header columns.
	for _, header := range []string{"APP", "FROM", "TO", "SOURCE", "ACTION"} {
		if !strings.Contains(out, header) {
			t.Errorf("output missing header %q", header)
		}
	}

	// Verify content rows.
	if !strings.Contains(out, "Firefox") {
		t.Error("output missing Firefox")
	}
	if !strings.Contains(out, "120.0") {
		t.Error("output missing version 120.0")
	}
	if !strings.Contains(out, "121.0") {
		t.Error("output missing version 121.0")
	}
	if !strings.Contains(out, "brew upgrade --cask firefox") {
		t.Error("output missing brew upgrade action")
	}
	if !strings.Contains(out, "Keynote") {
		t.Error("output missing Keynote")
	}
	if !strings.Contains(out, "mas upgrade 409183694") {
		t.Error("output missing mas upgrade action")
	}
	if !strings.Contains(out, "DRY RUN") {
		t.Error("output missing DRY RUN header")
	}
	if !strings.Contains(out, "2 update(s) would be applied") {
		t.Error("output missing update count summary")
	}
}

func TestPrintDryRun_JSON(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	cfg := &config.Config{}

	updatable := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox", CaskName: "firefox", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "120.0",
			LatestVersion:  "121.0",
		},
	}

	err := printDryRun(cmd, updatable, false, cfg, true)
	if err != nil {
		t.Fatalf("printDryRun() error = %v", err)
	}

	var entries []dryRunEntry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("failed to parse JSON output: %v\n%s", err, buf.String())
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.App != "Firefox" {
		t.Errorf("App = %q, want %q", e.App, "Firefox")
	}
	if e.From != "120.0" {
		t.Errorf("From = %q, want %q", e.From, "120.0")
	}
	if e.To != "121.0" {
		t.Errorf("To = %q, want %q", e.To, "121.0")
	}
	if e.Source != "brew" {
		t.Errorf("Source = %q, want %q", e.Source, "brew")
	}
	if e.Action != "brew upgrade --cask firefox" {
		t.Errorf("Action = %q, want %q", e.Action, "brew upgrade --cask firefox")
	}
}

func TestPrintDryRunJSON_ExplicitOverrideSerializesProvenance(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	if err := printDryRunJSON(cmd, []*checker.UpdateResult{{
		App:                  &app.App{Name: "Example"},
		Source:               "github",
		CurrentVersion:       "1.0.0",
		LatestVersion:        "1.1.0",
		HasUpdate:            true,
		SourceOverrideActive: true,
		SourceOverrideKind:   "github",
	}}); err != nil {
		t.Fatal(err)
	}

	var entries []dryRunEntry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("failed to parse JSON output: %v\n%s", err, buf.String())
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].SourceOverride || entries[0].SourceOverrideKind != "github" {
		t.Fatalf("expected override metadata in dry-run json, got %#v", entries[0])
	}
}

func TestPrintDryRun_Empty(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	cfg := &config.Config{}
	cfg.Pin("com.test.pinned")

	// All apps are pinned — nothing to update.
	updatable := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "PinnedApp", BundleID: "com.test.pinned"},
			Source:         "brew",
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
		},
	}

	err := printDryRun(cmd, updatable, false, cfg, false)
	if err != nil {
		t.Fatalf("printDryRun() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "nothing to update") {
		t.Errorf("expected 'nothing to update' message, got: %s", out)
	}
}

func TestPrintDryRun_SkipsPinned(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	cfg := &config.Config{}
	cfg.Pin("com.test.pinned")

	updatable := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "PinnedApp", BundleID: "com.test.pinned", CaskName: "pinned", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
		},
		{
			App:            &app.App{Name: "NormalApp", BundleID: "com.test.normal", CaskName: "normal", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "3.0",
			LatestVersion:  "4.0",
		},
	}

	err := printDryRun(cmd, updatable, false, cfg, false)
	if err != nil {
		t.Fatalf("printDryRun() error = %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "PinnedApp") {
		t.Error("pinned app should be excluded from dry run output")
	}
	if !strings.Contains(out, "NormalApp") {
		t.Error("normal app should be included in dry run output")
	}
	if !strings.Contains(out, "1 update(s) would be applied") {
		t.Errorf("expected 1 update count, got: %s", out)
	}
}

func TestPrintDryRun_SkipsNotifyOnly(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	cfg := &config.Config{}
	cfg.SetPolicy("com.test.notify", config.PolicyNotifyOnly)

	updatable := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "NotifyApp", BundleID: "com.test.notify", CaskName: "notify", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
		},
		{
			App:            &app.App{Name: "AutoApp", BundleID: "com.test.auto", CaskName: "auto", InstalledViaBrew: true},
			Source:         "brew",
			CurrentVersion: "5.0",
			LatestVersion:  "6.0",
		},
	}

	err := printDryRun(cmd, updatable, false, cfg, false)
	if err != nil {
		t.Fatalf("printDryRun() error = %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "NotifyApp") {
		t.Error("notify-only app should be excluded from dry run output")
	}
	if !strings.Contains(out, "AutoApp") {
		t.Error("auto app should be included in dry run output")
	}
}

func TestRunUpdate_JSONRequiresDryRun(t *testing.T) {
	// Save and restore global flags.
	origJSON := flagDryRunJSON
	origDryRun := flagDryRun
	defer func() {
		flagDryRunJSON = origJSON
		flagDryRun = origDryRun
	}()

	flagDryRunJSON = true
	flagDryRun = false

	cmd := &cobra.Command{
		RunE: runUpdate,
	}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error when --json used without --dry-run")
	}
	if !strings.Contains(err.Error(), "--json requires --dry-run") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPerformUpdate_ExplicitOverridePrintsCanonicalSourceLabel(t *testing.T) {
	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	origHistoryPath := history.DefaultPath
	history.DefaultPath = func() string { return filepath.Join(t.TempDir(), "history.json") }
	defer func() { history.DefaultPath = origHistoryPath }()

	performUpdate(cmd, context.Background(), &checker.UpdateResult{
		App:                  &app.App{Name: "Example"},
		Source:               "github",
		CurrentVersion:       "1.0.0",
		LatestVersion:        "1.1.0",
		SourceOverrideActive: true,
		SourceOverrideKind:   "github",
	}, &checker.MockCmdRunner{}, nil, nil)

	if !strings.Contains(buf.String(), "Updating Example (1.0.0 -> 1.1.0) via github (override)...") {
		t.Fatalf("expected live update line to show canonical override source, got %q", buf.String())
	}
}
