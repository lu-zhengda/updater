package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")

	first := Event{
		Name:             "share_clicked",
		Timestamp:        time.Now().Add(-time.Minute),
		CheckedApps:      42,
		UpdatesAvailable: 5,
		MajorUpdates:     1,
	}
	second := Event{
		Name:             "share_copied",
		Timestamp:        time.Now(),
		CheckedApps:      42,
		UpdatesAvailable: 5,
	}

	if err := Append(path, first); err != nil {
		t.Fatalf("Append first event: %v", err)
	}
	if err := Append(path, second); err != nil {
		t.Fatalf("Append second event: %v", err)
	}

	events, err := List(path)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Name != "share_clicked" {
		t.Errorf("events[0].Name = %q, want %q", events[0].Name, "share_clicked")
	}
	if events[1].Name != "share_copied" {
		t.Errorf("events[1].Name = %q, want %q", events[1].Name, "share_copied")
	}
}

func TestListMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	events, err := List(path)
	if err != nil {
		t.Fatalf("List missing file: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events for missing file, got %#v", events)
	}
}

func TestListCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o644); err != nil {
		t.Fatalf("Write corrupt metrics file: %v", err)
	}

	if _, err := List(path); err == nil {
		t.Fatal("expected parse error for corrupt metrics file")
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Fatal("DefaultPath should not be empty")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("DefaultPath should be absolute, got %q", path)
	}
}
