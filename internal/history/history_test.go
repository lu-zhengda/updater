package history

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppend_NewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	entry := Entry{
		AppName:     "Firefox",
		BundleID:    "org.mozilla.firefox",
		FromVersion: "120.0",
		ToVersion:   "121.0",
		Source:      "sparkle",
		Timestamp:   time.Now(),
		Success:     true,
	}

	if err := Append(path, entry); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].AppName != "Firefox" {
		t.Errorf("AppName = %q, want %q", entries[0].AppName, "Firefox")
	}
	if entries[0].ToVersion != "121.0" {
		t.Errorf("ToVersion = %q, want %q", entries[0].ToVersion, "121.0")
	}
}

func TestAppend_Existing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	entry1 := Entry{
		AppName:     "Firefox",
		BundleID:    "org.mozilla.firefox",
		FromVersion: "120.0",
		ToVersion:   "121.0",
		Source:      "sparkle",
		Timestamp:   time.Now().Add(-time.Hour),
		Success:     true,
	}
	entry2 := Entry{
		AppName:     "Chrome",
		BundleID:    "com.google.Chrome",
		FromVersion: "144.0",
		ToVersion:   "145.0",
		Source:      "brew",
		Timestamp:   time.Now(),
		Success:     true,
	}

	if err := Append(path, entry1); err != nil {
		t.Fatalf("first Append failed: %v", err)
	}
	if err := Append(path, entry2); err != nil {
		t.Fatalf("second Append failed: %v", err)
	}

	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].AppName != "Firefox" {
		t.Errorf("first entry AppName = %q, want %q", entries[0].AppName, "Firefox")
	}
	if entries[1].AppName != "Chrome" {
		t.Errorf("second entry AppName = %q, want %q", entries[1].AppName, "Chrome")
	}
}

func TestList_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")
	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil for missing file, got %v", entries)
	}
}

func TestList_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := List(path)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestAppend_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	roDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(roDir, 0o555); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(roDir, "subdir", "history.json")
	entry := Entry{
		AppName:   "Test",
		Timestamp: time.Now(),
	}

	err := Append(path, entry)
	if err == nil {
		t.Fatal("expected error when parent dir is read-only")
	}
}

func TestAppend_MultipleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	for i := 0; i < 5; i++ {
		entry := Entry{
			AppName:     fmt.Sprintf("App%d", i),
			FromVersion: fmt.Sprintf("%d.0", i),
			ToVersion:   fmt.Sprintf("%d.1", i),
			Source:      "brew",
			Timestamp:   time.Now(),
			Success:     true,
		}
		if err := Append(path, entry); err != nil {
			t.Fatalf("Append #%d failed: %v", i, err)
		}
	}

	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	for i, e := range entries {
		want := fmt.Sprintf("App%d", i)
		if e.AppName != want {
			t.Errorf("entry[%d].AppName = %q, want %q", i, e.AppName, want)
		}
	}
}

func TestAppend_RolledBackField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	entry := Entry{
		AppName:     "Firefox",
		BundleID:    "org.mozilla.firefox",
		FromVersion: "121.0",
		ToVersion:   "120.0",
		Source:      "sparkle",
		Timestamp:   time.Now(),
		Success:     false,
		RolledBack:  true,
	}

	if err := Append(path, entry); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].RolledBack {
		t.Error("expected RolledBack = true, got false")
	}
	if entries[0].Success {
		t.Error("expected Success = false, got true")
	}
}

func TestList_EmptyArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := List(path)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if entries == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestList_ReadError(t *testing.T) {
	dir := t.TempDir()
	// Pass a directory path instead of a file — os.ReadFile will fail.
	_, err := List(dir)
	if err == nil {
		t.Fatal("expected error when path is a directory")
	}
}

func TestAppend_CorruptExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	// Write corrupt JSON so that List (called inside Append) fails.
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := Entry{AppName: "Test", Timestamp: time.Now()}
	err := Append(path, entry)
	if err == nil {
		t.Fatal("expected error when existing file has corrupt JSON")
	}
}

func TestAppend_WriteError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "history.json")

	// Make the directory read-only so MkdirAll succeeds (dir exists)
	// but WriteFile fails (no write permission).
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Restore permissions so t.TempDir() cleanup succeeds.
		os.Chmod(dir, 0o755)
	})

	entry := Entry{AppName: "Test", Timestamp: time.Now()}
	err := Append(target, entry)
	if err == nil {
		t.Fatal("expected error when directory is not writable")
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Fatal("expected non-empty default path")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
}
