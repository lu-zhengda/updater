package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luzhengda/updater/internal/checker"
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

// testCpRunner simulates cp -a for tests. Other commands are no-ops.
type testCpRunner struct {
	t *testing.T
}

func (r *testCpRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	// We only need to handle the cp command for backup tests.
	if name == "cp" && len(args) >= 2 {
		// Simple directory copy for testing.
		src := args[len(args)-2]
		dst := args[len(args)-1]
		return nil, copyDir(src, dst)
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
