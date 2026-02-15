package main

import (
	"encoding/json"
	"strings"
	"testing"
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
