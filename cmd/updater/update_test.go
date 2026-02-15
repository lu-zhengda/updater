package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/backup"
	"github.com/luzhengda/updater/internal/checker"
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
