package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/metrics"
	"github.com/spf13/cobra"
)

func TestBuildCheckShareSummary(t *testing.T) {
	cfg := &config.Config{}
	cfg.Pin("com.example.pinned")

	results := []*checker.UpdateResult{
		{
			App:            &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox"},
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
			HasUpdate:      true,
		},
		{
			App:            &app.App{Name: "Node", BundleID: "org.node"},
			CurrentVersion: "20.0",
			LatestVersion:  "22.0",
			HasUpdate:      true,
			IsMajorUpdate:  true,
		},
		{
			App:       &app.App{Name: "PinnedApp", BundleID: "com.example.pinned"},
			HasUpdate: true,
		},
		{
			App:   &app.App{Name: "Broken"},
			Error: errors.New("feed unavailable"),
		},
	}

	summary := buildCheckShareSummary(results, cfg)

	if summary.CheckedApps != 4 {
		t.Fatalf("CheckedApps = %d, want 4", summary.CheckedApps)
	}
	if summary.UpdatesAvailable != 2 {
		t.Fatalf("UpdatesAvailable = %d, want 2", summary.UpdatesAvailable)
	}
	if summary.MajorUpdates != 1 {
		t.Fatalf("MajorUpdates = %d, want 1", summary.MajorUpdates)
	}
	if summary.ErrorCount != 1 {
		t.Fatalf("ErrorCount = %d, want 1", summary.ErrorCount)
	}
	if len(summary.TopUpdatedAppName) != 2 {
		t.Fatalf("TopUpdatedAppName len = %d, want 2", len(summary.TopUpdatedAppName))
	}
	if summary.TopUpdatedAppName[0] != "Firefox" || summary.TopUpdatedAppName[1] != "Node" {
		t.Fatalf("TopUpdatedAppName = %#v, want [Firefox Node]", summary.TopUpdatedAppName)
	}
	if !strings.Contains(summary.Message, "2 update(s) available") {
		t.Fatalf("summary message = %q, want update count", summary.Message)
	}
}

func TestRenderShareMessageUpToDate(t *testing.T) {
	msg := renderShareMessage(checkShareSummary{
		CheckedApps:      17,
		UpdatesAvailable: 0,
	})

	if !strings.Contains(msg, "everything is up to date") {
		t.Fatalf("msg = %q, want up-to-date text", msg)
	}
	if !strings.Contains(msg, "brew install --cask lu-zhengda/tap/updater") {
		t.Fatalf("msg = %q, want install CTA", msg)
	}
}

func TestMaybeShareCheckResultsSuccess(t *testing.T) {
	var (
		copiedText string
		events     []metrics.Event
	)

	origClipboard := writeClipboard
	origAppend := appendMetric
	origPath := metricPath
	defer func() {
		writeClipboard = origClipboard
		appendMetric = origAppend
		metricPath = origPath
	}()

	writeClipboard = func(text string) error {
		copiedText = text
		return nil
	}
	appendMetric = func(_ string, event metrics.Event) error {
		events = append(events, event)
		return nil
	}
	metricPath = func() string { return "/tmp/metrics-test.json" }

	results := []*checker.UpdateResult{
		{
			App:       &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox"},
			HasUpdate: true,
		},
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	maybeShareCheckResults(cmd, results, &config.Config{}, false, true)

	if copiedText == "" {
		t.Fatal("expected clipboard text to be written")
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Name != "share_clicked" {
		t.Fatalf("events[0].Name = %q, want share_clicked", events[0].Name)
	}
	if events[1].Name != "share_copied" {
		t.Fatalf("events[1].Name = %q, want share_copied", events[1].Name)
	}
	if !strings.Contains(out.String(), "copied to clipboard") {
		t.Fatalf("output = %q, want clipboard success state", out.String())
	}
}

func TestMaybeShareCheckResultsClipboardFailure(t *testing.T) {
	var events []metrics.Event

	origClipboard := writeClipboard
	origAppend := appendMetric
	origPath := metricPath
	defer func() {
		writeClipboard = origClipboard
		appendMetric = origAppend
		metricPath = origPath
	}()

	writeClipboard = func(string) error {
		return errors.New("clipboard unavailable")
	}
	appendMetric = func(_ string, event metrics.Event) error {
		events = append(events, event)
		return nil
	}
	metricPath = func() string { return "/tmp/metrics-test.json" }

	results := []*checker.UpdateResult{
		{
			App:       &app.App{Name: "Firefox", BundleID: "org.mozilla.firefox"},
			HasUpdate: true,
		},
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	maybeShareCheckResults(cmd, results, &config.Config{}, false, true)

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	if events[0].Name != "share_clicked" {
		t.Fatalf("events[0].Name = %q, want share_clicked", events[0].Name)
	}
	if events[1].Name != "share_copy_failed" {
		t.Fatalf("events[1].Name = %q, want share_copy_failed", events[1].Name)
	}
	if !strings.Contains(out.String(), "Share this update summary:") {
		t.Fatalf("output = %q, want fallback share text", out.String())
	}
}
