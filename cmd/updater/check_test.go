package main

import (
	"fmt"
	"testing"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
)

func TestToCheckEntries_UpdateAvailable(t *testing.T) {
	cfg := &config.Config{}
	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox", Version: "133.0"},
		Source:         "brew",
		CurrentVersion: "133.0",
		LatestVersion:  "134.0",
		HasUpdate:      true,
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != "update_available" {
		t.Errorf("Status = %q, want %q", e.Status, "update_available")
	}
	if e.Name != "Firefox" {
		t.Errorf("Name = %q, want %q", e.Name, "Firefox")
	}
	if e.BundleID != "org.mozilla.firefox" {
		t.Errorf("BundleID = %q, want %q", e.BundleID, "org.mozilla.firefox")
	}
	if e.CurrentVersion != "133.0" {
		t.Errorf("CurrentVersion = %q, want %q", e.CurrentVersion, "133.0")
	}
	if e.LatestVersion != "134.0" {
		t.Errorf("LatestVersion = %q, want %q", e.LatestVersion, "134.0")
	}
	if e.Source != "brew" {
		t.Errorf("Source = %q, want %q", e.Source, "brew")
	}
}

func TestToCheckEntries_MajorUpdate(t *testing.T) {
	cfg := &config.Config{}
	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "Node", BundleID: "org.nodejs.node", Version: "20.0"},
		Source:         "formula",
		CurrentVersion: "20.0",
		LatestVersion:  "22.0",
		HasUpdate:      true,
		IsMajorUpdate:  true,
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Status != "major_update" {
		t.Errorf("Status = %q, want %q", entries[0].Status, "major_update")
	}
}

func TestToCheckEntries_Pinned(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pin("com.example.pinned")

	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "PinnedApp", BundleID: "com.example.pinned", Version: "1.0"},
		Source:         "sparkle",
		CurrentVersion: "1.0",
		LatestVersion:  "2.0",
		HasUpdate:      true,
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Status != "pinned" {
		t.Errorf("Status = %q, want %q", entries[0].Status, "pinned")
	}
}

func TestToCheckEntries_OK(t *testing.T) {
	cfg := &config.Config{}
	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "Safari", BundleID: "com.apple.Safari", Version: "17.0"},
		Source:         "system",
		CurrentVersion: "17.0",
		LatestVersion:  "17.0",
		HasUpdate:      false,
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Status != "ok" {
		t.Errorf("Status = %q, want %q", entries[0].Status, "ok")
	}
}

func TestToCheckEntries_Error(t *testing.T) {
	cfg := &config.Config{}
	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "BrokenApp", BundleID: "com.example.broken", Version: "1.0"},
		Source:         "sparkle",
		CurrentVersion: "1.0",
		Error:          fmt.Errorf("feed unavailable"),
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != "error" {
		t.Errorf("Status = %q, want %q", e.Status, "error")
	}
	if e.Error != "feed unavailable" {
		t.Errorf("Error = %q, want %q", e.Error, "feed unavailable")
	}
}

func TestToCheckEntries_Empty(t *testing.T) {
	cfg := &config.Config{}
	entries := toCheckEntries(nil, cfg)

	if entries == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestToCheckEntries_DownloadURL(t *testing.T) {
	cfg := &config.Config{}
	results := []*checker.UpdateResult{{
		App:            &app.App{Name: "Sketch", BundleID: "com.bohemiancoding.sketch3", Version: "99.0"},
		Source:         "sparkle",
		CurrentVersion: "99.0",
		LatestVersion:  "100.0",
		HasUpdate:      true,
		DownloadURL:    "https://download.sketch.com/sketch-100.zip",
		ReleaseNotes:   "Bug fixes and performance improvements",
	}}

	entries := toCheckEntries(results, cfg)

	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.DownloadURL != "https://download.sketch.com/sketch-100.zip" {
		t.Errorf("DownloadURL = %q, want %q", e.DownloadURL, "https://download.sketch.com/sketch-100.zip")
	}
	if e.ReleaseNotes != "Bug fixes and performance improvements" {
		t.Errorf("ReleaseNotes = %q, want %q", e.ReleaseNotes, "Bug fixes and performance improvements")
	}
}
