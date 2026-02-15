package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
)

func TestCheckTool_Present(t *testing.T) {
	runner := &checker.MockCmdRunner{Output: []byte("/usr/bin/brew\n")}
	c := checkTool(context.Background(), runner, "brew")
	if c.Status != "ok" {
		t.Errorf("Status = %q, want %q", c.Status, "ok")
	}
	if c.Detail != "/usr/bin/brew" {
		t.Errorf("Detail = %q, want %q", c.Detail, "/usr/bin/brew")
	}
}

func TestCheckTool_Missing(t *testing.T) {
	runner := &checker.MockCmdRunner{Err: os.ErrNotExist}
	c := checkTool(context.Background(), runner, "mas")
	if c.Status != "warning" {
		t.Errorf("Status = %q, want %q", c.Status, "warning")
	}
	if c.Detail != "not found" {
		t.Errorf("Detail = %q, want %q", c.Detail, "not found")
	}
}

func TestCheckHistory_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	entries := []history.Entry{
		{AppName: "Firefox", Success: true},
		{AppName: "Chrome", Success: true},
	}
	data, _ := json.Marshal(entries)
	os.WriteFile(path, data, 0o644)

	orig := history.DefaultPath
	history.DefaultPath = func() string { return path }
	defer func() { history.DefaultPath = orig }()

	c := checkHistory()
	if c.Status != "ok" {
		t.Errorf("Status = %q, want %q", c.Status, "ok")
	}
	if !strings.Contains(c.Detail, "2 entries") {
		t.Errorf("Detail = %q, want to contain '2 entries'", c.Detail)
	}
}

func TestCheckHistory_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")
	os.WriteFile(path, []byte("{bad json"), 0o644)

	orig := history.DefaultPath
	history.DefaultPath = func() string { return path }
	defer func() { history.DefaultPath = orig }()

	c := checkHistory()
	if c.Status != "warning" {
		t.Errorf("Status = %q, want %q", c.Status, "warning")
	}
}

func TestDoctor_JSONOutput(t *testing.T) {
	// Test that the JSON output format is valid by running checkTool directly.
	runner := &checker.MockCmdRunner{Output: []byte("/usr/bin/brew\n")}
	checks := []doctorCheck{
		checkTool(context.Background(), runner, "brew"),
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(checks); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got []doctorCheck
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 check, got %d", len(got))
	}
	if got[0].Name != "brew" {
		t.Errorf("Name = %q, want %q", got[0].Name, "brew")
	}
}

func TestDoctor_AllToolsPresent(t *testing.T) {
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"which brew":      {Output: []byte("/opt/homebrew/bin/brew\n")},
			"which mas":       {Output: []byte("/usr/local/bin/mas\n")},
			"which osascript": {Output: []byte("/usr/bin/osascript\n")},
			"which hdiutil":   {Output: []byte("/usr/bin/hdiutil\n")},
			"which ditto":     {Output: []byte("/usr/bin/ditto\n")},
			"which sw_vers":   {Output: []byte("/usr/bin/sw_vers\n")},
		},
	}

	tools := []string{"brew", "mas", "osascript", "hdiutil", "ditto", "sw_vers"}
	for _, tool := range tools {
		c := checkTool(context.Background(), runner, tool)
		if c.Status != "ok" {
			t.Errorf("checkTool(%q) Status = %q, want %q", tool, c.Status, "ok")
		}
	}
}

func TestValidateConfigMappings_AllValid(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{"com.example.app": "owner/repo"},
		CaskMappings:   map[string]string{"com.example.app": "my-cask"},
	}
	apps := []*app.App{
		{BundleID: "com.example.app"},
	}
	checks := validateConfigMappings(cfg, apps)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "ok" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "ok")
	}
	if checks[0].Detail != "all mappings valid" {
		t.Errorf("Detail = %q, want %q", checks[0].Detail, "all mappings valid")
	}
}

func TestValidateConfigMappings_StaleGitHub(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{"com.stale.app": "owner/repo"},
	}
	apps := []*app.App{
		{BundleID: "com.other.app"},
	}
	checks := validateConfigMappings(cfg, apps)
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
	if checks[0].Status != "warning" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "warning")
	}
	if !strings.Contains(checks[0].Detail, "github_mappings: com.stale.app") {
		t.Errorf("Detail should mention stale github_mappings, got: %s", checks[0].Detail)
	}
}

func TestValidateConfigMappings_StalePinned(t *testing.T) {
	cfg := &config.Config{
		PinnedApps: []string{"com.stale.pinned"},
	}
	apps := []*app.App{
		{BundleID: "com.other.app"},
	}
	checks := validateConfigMappings(cfg, apps)
	if checks[0].Status != "warning" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "warning")
	}
	if !strings.Contains(checks[0].Detail, "pinned_apps: com.stale.pinned") {
		t.Errorf("Detail should mention stale pinned_apps, got: %s", checks[0].Detail)
	}
}

func TestValidateConfigMappings_MultipleStale(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{"com.stale.gh": "owner/repo"},
		Policies:       map[string]string{"com.stale.policy": config.PolicyManual},
	}
	apps := []*app.App{
		{BundleID: "com.valid.app"},
	}
	checks := validateConfigMappings(cfg, apps)
	if checks[0].Status != "warning" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "warning")
	}
	if !strings.Contains(checks[0].Detail, "2 stale") {
		t.Errorf("Detail should say '2 stale', got: %s", checks[0].Detail)
	}
}

func TestValidateConfigMappings_EmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{{BundleID: "com.example.app"}}
	checks := validateConfigMappings(cfg, apps)
	if checks[0].Status != "ok" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "ok")
	}
}

func TestValidateConfigMappings_NoApps(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{"com.stale.app": "owner/repo"},
	}
	checks := validateConfigMappings(cfg, nil)
	if checks[0].Status != "warning" {
		t.Errorf("Status = %q, want %q", checks[0].Status, "warning")
	}
}
