package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
)

func TestBackupAndRestore(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")
	appDir := filepath.Join(tmpDir, "Applications")

	// Create a fake app bundle.
	appPath := filepath.Join(appDir, "Test.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Info.plist"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Runner that simulates cp -a by doing a real copy.
	runner := &testCpRunner{t: t}

	mgr := NewManager(baseDir, 2, runner)

	// Create backup.
	err := mgr.Backup(context.Background(), "Test", "com.example.test", "1.0", appPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup exists.
	if !mgr.HasBackup("Test") {
		t.Fatal("expected HasBackup to return true")
	}

	// List backups.
	backups, err := mgr.List("Test")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}
	if backups[0].Version != "1.0" {
		t.Errorf("version = %q, want %q", backups[0].Version, "1.0")
	}
	if backups[0].AppName != "Test" {
		t.Errorf("app name = %q, want %q", backups[0].AppName, "Test")
	}

	// Damage the installed app, then verify rollback restores the backup rather
	// than nesting it inside the existing bundle.
	if err := os.WriteFile(filepath.Join(appPath, "Info.plist"), []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Restore(context.Background(), "Test"); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(appPath, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "v1" {
		t.Fatalf("restored content = %q, want v1", restored)
	}
	if _, err := os.Stat(filepath.Join(appPath, "Test.app")); !os.IsNotExist(err) {
		t.Fatal("rollback nested the app bundle inside the existing bundle")
	}
}

func TestHasBackup_NoBackups(t *testing.T) {
	mgr := NewManager(t.TempDir(), 1, &checker.MockCmdRunner{})
	if mgr.HasBackup("NonExistent") {
		t.Error("expected HasBackup to return false for non-existent app")
	}
}

func TestPruneOldBackups(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")
	appBackupDir := filepath.Join(baseDir, "test")

	// Create 3 "backup" directories.
	for _, ts := range []string{"20240101-100000", "20240102-100000", "20240103-100000"} {
		dir := filepath.Join(appBackupDir, ts)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewManager(baseDir, 2, &checker.MockCmdRunner{})
	mgr.pruneOldBackups("test")

	entries, err := os.ReadDir(appBackupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after pruning, got %d", len(entries))
	}
	// Oldest should be removed.
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	for _, n := range names {
		if n == "20240101-100000" {
			t.Error("expected oldest backup to be pruned")
		}
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My App", "my-app"},
		{"PDF Expert", "pdf-expert"},
		{"App/Name", "app-name"},
		{"simple", "simple"},
		{"..", "app"},
		{"../../escape", "escape"},
		{"App\\Name", "app-name"},
		{"\x00control", "control"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBackup_CpFails(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")
	appPath := filepath.Join(tmpDir, "Test.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MockCmdRunner{Err: fmt.Errorf("disk full")}
	mgr := NewManager(baseDir, 2, runner)

	err := mgr.Backup(context.Background(), "Test", "com.example.test", "1.0", appPath)
	if err == nil {
		t.Fatal("expected error when cp fails")
	}
	if want := "failed to copy app for backup"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want containing %q", err.Error(), want)
	}
}

func TestRestore_NoBackups(t *testing.T) {
	mgr := NewManager(t.TempDir(), 1, &checker.MockCmdRunner{})
	err := mgr.Restore(context.Background(), "NonExistent")
	if err == nil {
		t.Fatal("expected error for non-existent app")
	}
	if want := "no backups found"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want containing %q", err.Error(), want)
	}
}

func TestRestore_BackupFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")

	// Create a valid metadata.json pointing to a missing backup path.
	safeName := sanitizeName("TestApp")
	backupDir := filepath.Join(baseDir, safeName, "20240101-100000")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := Metadata{
		AppName:    "TestApp",
		BundleID:   "com.example.test",
		Version:    "1.0",
		BackupPath: filepath.Join(backupDir, "TestApp.app"), // does not exist
		BackupDate: time.Now(),
		OrigPath:   "/Applications/TestApp.app",
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(backupDir, "metadata.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(baseDir, 2, &checker.MockCmdRunner{})
	err := mgr.Restore(context.Background(), "TestApp")
	if err == nil {
		t.Fatal("expected error for missing backup file")
	}
	if want := "backup file missing"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want containing %q", err.Error(), want)
	}
}

func TestRestore_CpFails(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")

	// Create valid backup with actual file present.
	safeName := sanitizeName("TestApp")
	backupDir := filepath.Join(baseDir, safeName, "20240101-100000")
	backupApp := filepath.Join(backupDir, "TestApp.app")
	if err := os.MkdirAll(backupApp, 0o755); err != nil {
		t.Fatal(err)
	}

	meta := Metadata{
		AppName:    "TestApp",
		BundleID:   "com.example.test",
		Version:    "1.0",
		BackupPath: backupApp,
		BackupDate: time.Now(),
		OrigPath:   "/Applications/TestApp.app",
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(backupDir, "metadata.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &checker.MockCmdRunner{Err: fmt.Errorf("permission denied")}
	mgr := NewManager(baseDir, 2, runner)
	err := mgr.Restore(context.Background(), "TestApp")
	if err == nil {
		t.Fatal("expected error when cp fails")
	}
	if want := "failed to restore backup"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want containing %q", err.Error(), want)
	}
}

func TestList_CorruptMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")

	safeName := sanitizeName("TestApp")
	backupDir := filepath.Join(baseDir, safeName, "20240101-100000")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write invalid JSON.
	if err := os.WriteFile(filepath.Join(backupDir, "metadata.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(baseDir, 2, &checker.MockCmdRunner{})
	backups, err := mgr.List("TestApp")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups for corrupt metadata, got %d", len(backups))
	}
}

func TestList_MissingMetadataFile(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")

	// Create dir without metadata.json.
	safeName := sanitizeName("TestApp")
	backupDir := filepath.Join(baseDir, safeName, "20240101-100000")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(baseDir, 2, &checker.MockCmdRunner{})
	backups, err := mgr.List("TestApp")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups when metadata.json missing, got %d", len(backups))
	}
}

func TestList_SortOrder(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "backups")
	safeName := sanitizeName("TestApp")

	// Create backups with different timestamps. BackupDate controls sort order.
	timestamps := []struct {
		dir     string
		date    time.Time
		version string
	}{
		{"20240101-100000", time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), "1.0"},
		{"20240103-100000", time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC), "3.0"},
		{"20240102-100000", time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC), "2.0"},
	}

	for _, ts := range timestamps {
		dir := filepath.Join(baseDir, safeName, ts.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := Metadata{
			AppName:    "TestApp",
			BundleID:   "com.example.test",
			Version:    ts.version,
			BackupPath: filepath.Join(dir, "TestApp.app"),
			BackupDate: ts.date,
			OrigPath:   "/Applications/TestApp.app",
		}
		data, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewManager(baseDir, 5, &checker.MockCmdRunner{})
	backups, err := mgr.List("TestApp")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("expected 3 backups, got %d", len(backups))
	}
	// Newest first.
	if backups[0].Version != "3.0" {
		t.Errorf("first backup version = %q, want %q", backups[0].Version, "3.0")
	}
	if backups[1].Version != "2.0" {
		t.Errorf("second backup version = %q, want %q", backups[1].Version, "2.0")
	}
	if backups[2].Version != "1.0" {
		t.Errorf("third backup version = %q, want %q", backups[2].Version, "1.0")
	}
}

func TestDefaultBaseDir(t *testing.T) {
	dir := DefaultBaseDir()
	if dir == "" {
		t.Fatal("expected non-empty default base dir")
	}
	if !strings.HasSuffix(dir, filepath.Join(".local", "share", "updater", "backups")) {
		t.Errorf("DefaultBaseDir() = %q, want suffix %q", dir, ".local/share/updater/backups")
	}
}

func TestNewManager_DefaultMaxBackups(t *testing.T) {
	mgr := NewManager(t.TempDir(), 0, &checker.MockCmdRunner{})
	if mgr.maxBackups != 1 {
		t.Errorf("maxBackups = %d, want 1 (clamped from 0)", mgr.maxBackups)
	}

	mgr2 := NewManager(t.TempDir(), -5, &checker.MockCmdRunner{})
	if mgr2.maxBackups != 1 {
		t.Errorf("maxBackups = %d, want 1 (clamped from -5)", mgr2.maxBackups)
	}
}

// testCpRunner simulates the filesystem commands used by backup and restore.
type testCpRunner struct {
	t *testing.T
}

func (r *testCpRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "cp" && len(args) >= 2 {
		// Simple directory copy for testing.
		src := args[len(args)-2]
		dst := args[len(args)-1]
		return nil, copyDir(src, dst)
	}
	if name == "rm" && len(args) >= 1 {
		return nil, os.RemoveAll(args[len(args)-1])
	}
	if name == "mv" && len(args) == 2 {
		return nil, os.Rename(args[0], args[1])
	}
	return nil, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
