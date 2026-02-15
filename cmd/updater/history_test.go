package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

func writeTestHistory(t *testing.T, entries []history.Entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}
	return path
}

func sampleEntries(n int) []history.Entry {
	names := []string{"Firefox", "Chrome", "Slack", "VSCode", "iTerm2"}
	entries := make([]history.Entry, n)
	for i := range n {
		entries[i] = history.Entry{
			AppName:     names[i%len(names)],
			BundleID:    "com.test." + names[i%len(names)],
			FromVersion: "1.0",
			ToVersion:   "2.0",
			Source:      "sparkle",
			Timestamp:   time.Date(2025, 1, 1+i, 12, 0, 0, 0, time.UTC),
			Success:     true,
		}
	}
	return entries
}

func runHistoryWithPath(t *testing.T, path string, limit int, asJSON bool) string {
	t.Helper()

	origDefault := history.DefaultPath
	history.DefaultPath = func() string { return path }
	defer func() { history.DefaultPath = origDefault }()

	origLimit := flagHistoryLimit
	origJSON := flagHistoryJSON
	defer func() {
		flagHistoryLimit = origLimit
		flagHistoryJSON = origJSON
	}()
	flagHistoryLimit = limit
	flagHistoryJSON = asJSON

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := runHistory(cmd, nil); err != nil {
		t.Fatalf("runHistory: %v", err)
	}
	return buf.String()
}

func TestRunHistory_JSON(t *testing.T) {
	entries := sampleEntries(3)
	path := writeTestHistory(t, entries)

	out := runHistoryWithPath(t, path, 0, true)

	var got []history.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 entries, got %d", len(got))
	}
}

func TestRunHistory_JSON_Empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	out := runHistoryWithPath(t, path, 20, true)

	var got []history.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestRunHistory_JSON_WithLimit(t *testing.T) {
	entries := sampleEntries(5)
	path := writeTestHistory(t, entries)

	out := runHistoryWithPath(t, path, 2, true)

	var got []history.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, out)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d", len(got))
	}
}
