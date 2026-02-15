package history

import (
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

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Fatal("expected non-empty default path")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
}
