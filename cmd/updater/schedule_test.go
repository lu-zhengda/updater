package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/checker"
)

func TestSchedulePlistPath(t *testing.T) {
	p, err := schedulePlistPath()
	if err != nil {
		t.Fatalf("schedulePlistPath() error: %v", err)
	}
	if !strings.HasSuffix(p, "Library/LaunchAgents/com.updater.check.plist") {
		t.Errorf("unexpected plist path: %s", p)
	}
}

func TestScheduleExists_NotInstalled(t *testing.T) {
	// By default in test, the plist won't exist (unless the user has it installed).
	// This test just ensures the function doesn't panic.
	_ = scheduleExists()
}

func TestRenderPlist(t *testing.T) {
	data := plistData{
		Label:           "com.updater.check",
		Binary:          "/usr/local/bin/updater",
		IntervalSeconds: 86400,
		LogPath:         "/tmp/updater.log",
	}

	content, err := renderPlist(data)
	if err != nil {
		t.Fatalf("renderPlist failed: %v", err)
	}

	// Verify it's valid XML-ish.
	if !strings.Contains(content, "<?xml version=") {
		t.Error("expected XML declaration")
	}
	if !strings.Contains(content, "<string>com.updater.check</string>") {
		t.Error("expected label in plist")
	}
	if !strings.Contains(content, "<string>/usr/local/bin/updater</string>") {
		t.Error("expected binary path in plist")
	}
	if !strings.Contains(content, "<string>notify</string>") {
		t.Error("expected notify argument in plist")
	}
	if !strings.Contains(content, "<integer>86400</integer>") {
		t.Error("expected interval in plist")
	}
	if !strings.Contains(content, "<true/>") {
		t.Error("expected RunAtLoad in plist")
	}
}

func TestRenderPlist_DifferentInterval(t *testing.T) {
	data := plistData{
		Label:           "com.updater.check",
		Binary:          "/opt/updater",
		IntervalSeconds: 3600, // 1 hour
		LogPath:         "/tmp/log",
	}

	content, err := renderPlist(data)
	if err != nil {
		t.Fatalf("renderPlist failed: %v", err)
	}

	if !strings.Contains(content, "<integer>3600</integer>") {
		t.Error("expected 3600 second interval")
	}
}

func TestRenderPlist_AutoUpdateIsOptIn(t *testing.T) {
	base := plistData{
		Label:           "com.updater.check",
		Binary:          "/opt/updater",
		IntervalSeconds: 3600,
		LogPath:         "/tmp/log",
	}
	content, err := renderPlist(base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "--auto-update") {
		t.Fatal("scheduled checks must not auto-update unless explicitly enabled")
	}

	base.AutoUpdate = true
	content, err = renderPlist(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "--auto-update") {
		t.Fatal("expected --auto-update for explicit opt-in")
	}
}

func TestRenderPlistEscapesPaths(t *testing.T) {
	content, err := renderPlist(plistData{
		Label:           plistLabel,
		Binary:          "/Applications/A&B/updater",
		IntervalSeconds: 3600,
		LogPath:         "/tmp/<updater>.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "A&amp;B") || !strings.Contains(content, "&lt;updater&gt;") {
		t.Fatalf("expected XML-escaped paths, got %s", content)
	}
}

func TestInstallScheduleRejectsInvalidInterval(t *testing.T) {
	for _, hours := range []int{0, -1, 8761} {
		if err := installScheduleCore(context.Background(), &checker.MockCmdRunner{}, hours, false); err == nil {
			t.Fatalf("expected interval %d to be rejected", hours)
		}
	}
}
