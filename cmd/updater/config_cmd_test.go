package main

import (
	"os"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/config"
	"gopkg.in/yaml.v3"
)

func TestConfigExport_MarshalRoundtrip(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{"com.test": "owner/repo"},
		PinnedApps:     []string{"com.pinned"},
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(data), "com.test") {
		t.Error("expected github mapping in output")
	}
	if !strings.Contains(string(data), "com.pinned") {
		t.Error("expected pinned app in output")
	}

	// Verify round-trip
	var decoded config.Config
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.GitHubMappings["com.test"] != "owner/repo" {
		t.Errorf("github mapping = %q, want %q", decoded.GitHubMappings["com.test"], "owner/repo")
	}
}

func TestConfigImport_WriteRead(t *testing.T) {
	tmpFile := t.TempDir() + "/test-config.yaml"

	cfg := &config.Config{
		PinnedApps:     []string{"com.app1"},
		GitHubMappings: map[string]string{"com.app2": "owner/repo"},
	}
	data, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Read back
	readData, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	var imported config.Config
	if err := yaml.Unmarshal(readData, &imported); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(imported.PinnedApps) != 1 || imported.PinnedApps[0] != "com.app1" {
		t.Errorf("pinned apps = %v, want [com.app1]", imported.PinnedApps)
	}
}

func TestConfigMerge_ImportOverrides(t *testing.T) {
	current := &config.Config{
		GitHubMappings: map[string]string{"com.existing": "old/repo"},
		PinnedApps:     []string{"com.pinned1"},
	}
	imported := &config.Config{
		GitHubMappings: map[string]string{"com.existing": "new/repo", "com.new": "new/other"},
		PinnedApps:     []string{"com.pinned2"},
	}

	merged := config.Merge(current, imported)

	// Map: imported overrides
	if merged.GitHubMappings["com.existing"] != "new/repo" {
		t.Errorf("existing mapping should be overridden, got %q", merged.GitHubMappings["com.existing"])
	}
	if merged.GitHubMappings["com.new"] != "new/other" {
		t.Error("new mapping should be added")
	}
	// List: union + dedup
	hasPinned1, hasPinned2 := false, false
	for _, p := range merged.PinnedApps {
		if p == "com.pinned1" {
			hasPinned1 = true
		}
		if p == "com.pinned2" {
			hasPinned2 = true
		}
	}
	if !hasPinned1 || !hasPinned2 {
		t.Errorf("pinned should be union, got %v", merged.PinnedApps)
	}
}
