package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

func TestScanEntryJSON(t *testing.T) {
	// Verify scanEntry marshals correctly with omitempty.
	entry := scanEntry{
		Name:             "Firefox",
		BundleID:         "org.mozilla.firefox",
		Version:          "134.0",
		Source:           "brew",
		InstalledViaBrew: true,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"installed_via_brew":true`) {
		t.Error("expected installed_via_brew")
	}
	if strings.Contains(string(data), `"feed_url"`) {
		t.Error("empty feed_url should be omitted")
	}
	if strings.Contains(string(data), `"github_repo"`) {
		t.Error("empty github_repo should be omitted")
	}
	if strings.Contains(string(data), `"cask_name"`) {
		t.Error("empty cask_name should be omitted")
	}
}

func TestScanEntryJSON_SourceOverrideFields(t *testing.T) {
	entry := scanEntry{
		sourceOverrideJSON: sourceOverrideJSON{
			SourceOverride:     true,
			SourceOverrideKind: "github",
		},
		Name:     "Example App",
		BundleID: "com.example.app",
		Version:  "1.0.0",
		Source:   "github",
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"source_override":true`) {
		t.Fatalf("expected source_override field, got %s", data)
	}
	if !strings.Contains(string(data), `"source_override_kind":"github"`) {
		t.Fatalf("expected source_override_kind field, got %s", data)
	}
}

func TestLoadScanApps_AppliesEnrichmentOverrides(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "owner/repo",
			},
		},
	}

	apps, err := loadScanApps(context.Background(), cfg, &checker.MockCmdRunner{}, []*app.App{
		{Name: "Example", BundleID: "com.example.app", Source: app.SourceUnknown},
	}, nil)
	if err != nil {
		t.Fatalf("loadScanApps failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("got %d apps, want 1", len(apps))
	}
	if !apps[0].SourceOverrideActive {
		t.Fatalf("expected override-backed app, got %#v", apps[0])
	}
	if apps[0].Source != app.SourceGitHub {
		t.Fatalf("Source = %q, want %q", apps[0].Source, app.SourceGitHub)
	}
}
