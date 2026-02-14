package main

import (
	"strings"
	"testing"
)

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
