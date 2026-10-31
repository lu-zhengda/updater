package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

func TestPackagePinAndPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATER_AGENT_MODE", "1")
	const id = "npm.global.@nanonets/graft"
	orig := discoverInstalled
	t.Cleanup(func() { discoverInstalled = orig })
	discoverInstalled = func(context.Context) ([]*app.App, error) {
		return []*app.App{
			{Name: "@nanonets/graft", BundleID: id},
			{Name: "httpie", BundleID: "homebrew.formula.httpie"},
			{Name: "httpie", BundleID: "uv.tool.httpie"},
		}, nil
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runPin(cmd, []string{"httpie"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("pinning duplicate package name: got %v", err)
	}
	if err := runPin(cmd, []string{"homebrew.formula.httpie"}); err != nil {
		t.Fatal(err)
	}
	if err := runPin(cmd, []string{"@nanonets/graft"}); err != nil {
		t.Fatal(err)
	}
	if err := runPolicy(cmd, []string{id, "manual"}); err != nil {
		t.Fatal(err)
	}
	if err := runUnpin(cmd, []string{"npm.global.@nanonets/gra-ft"}); err == nil {
		t.Fatal("a misspelled package ID removed a pin")
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil || !cfg.IsPinned(id) || cfg.Policy(id) != config.PolicyManual {
		t.Fatalf("package settings were not saved: cfg=%+v err=%v", cfg, err)
	}
	if err := runUnpin(cmd, []string{"@nanonets/graft"}); err != nil {
		t.Fatal(err)
	}
	if err := runPin(cmd, []string{id}); err != nil {
		t.Fatal(err)
	}

	discoverInstalled = func(context.Context) ([]*app.App, error) {
		t.Fatal("stored ID triggered discovery")
		return nil, nil
	}
	out.Reset()
	if err := runUnpin(cmd, []string{id}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status   string
		BundleID string `json:"bundle_id"`
		Changed  bool
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "unpinned" || result.BundleID != id || !result.Changed {
		t.Fatalf("unexpected unpin output: %s", &out)
	}
	if err := runPolicy(cmd, []string{id, "clear"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(config.DefaultPath())
	if err != nil || cfg.IsPinned(id) || cfg.Policy(id) != "" || !cfg.IsPinned("homebrew.formula.httpie") || cfg.IsPinned("uv.tool.httpie") {
		t.Fatalf("wrong settings after clearing by ID: cfg=%+v err=%v", cfg, err)
	}
}
