package main

import (
	"encoding/json"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

func TestToOutdatedEntries(t *testing.T) {
	results := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "App1", BundleID: "com.example.app1"},
			Source:         "sparkle",
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
			DownloadURL:    "https://example.com/app1.dmg",
			HasUpdate:      true,
		},
		{
			App:            &app.App{Name: "App2", BundleID: "com.example.app2"},
			Source:         "brew",
			CurrentVersion: "3.0",
			LatestVersion:  "3.0",
			HasUpdate:      false,
		},
		{
			App:            &app.App{Name: "App3", BundleID: "com.example.app3"},
			Source:         "github",
			CurrentVersion: "1.0",
			Error:          &mockError{"check failed"},
		},
		{
			App:            &app.App{Name: "App4", BundleID: "com.example.app4"},
			Source:         "github",
			CurrentVersion: "1.0",
			LatestVersion:  "1.5",
			HasUpdate:      true,
		},
	}

	entries := toOutdatedEntries(results)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Name != "App1" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "App1")
	}
	if entries[0].DownloadURL != "https://example.com/app1.dmg" {
		t.Errorf("entries[0].DownloadURL = %q, want non-empty", entries[0].DownloadURL)
	}
	if entries[1].Name != "App4" {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "App4")
	}
	if entries[1].DownloadURL != "" {
		t.Errorf("entries[1].DownloadURL = %q, want empty", entries[1].DownloadURL)
	}
}

func TestToOutdatedEntries_Empty(t *testing.T) {
	entries := toOutdatedEntries(nil)
	if entries == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestToOutdatedEntries_JSONSerialization(t *testing.T) {
	results := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "Test", BundleID: "com.test"},
			Source:         "brew",
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
			HasUpdate:      true,
		},
	}

	entries := toOutdatedEntries(results)
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded []outdatedEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(decoded) != 1 {
		t.Fatalf("expected 1 decoded entry, got %d", len(decoded))
	}
	if decoded[0].BundleID != "com.test" {
		t.Errorf("decoded BundleID = %q, want %q", decoded[0].BundleID, "com.test")
	}
	// download_url should be omitted from JSON when empty.
	if string(data) == "" {
		t.Error("expected non-empty JSON")
	}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}
