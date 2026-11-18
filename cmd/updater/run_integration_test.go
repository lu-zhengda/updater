package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

const integrationAppcastXML = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>TestApp Updates</title>
    <item>
      <title>Version 2.0.0</title>
      <sparkle:version>200</sparkle:version>
      <sparkle:shortVersionString>2.0.0</sparkle:shortVersionString>
      <pubDate>Mon, 05 Oct 2025 19:20:11 +0000</pubDate>
      <enclosure url="https://example.com/app.dmg" length="1234" type="application/octet-stream" />
    </item>
  </channel>
</rss>`

// withStubbedPipeline points the command entry points at a fake discovery
// result and mock command runner, restoring the real ones on cleanup.
func withStubbedPipeline(t *testing.T, apps []*app.App) {
	t.Helper()
	origDiscover, origRunner := discoverAll, newRunner
	t.Cleanup(func() { discoverAll, newRunner = origDiscover, origRunner })
	discoverAll = func(_ context.Context, _ *config.Config, _ checker.CmdRunner) ([]*app.App, error) {
		return apps, nil
	}
	newRunner = func() checker.CmdRunner { return &checker.MockCmdRunner{} }
}

// TestRunCheckEndToEnd drives the real `updater check` command path: config
// load, discovery (stubbed), checker construction, a live Sparkle HTTP check
// against a local server, and JSON encoding of the results.
func TestRunCheckEndToEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())       // isolate config read/write
	t.Setenv("UPDATER_AGENT_MODE", "1") // force JSON output
	t.Setenv("GITHUB_TOKEN", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(integrationAppcastXML))
	}))
	defer srv.Close()

	withStubbedPipeline(t, []*app.App{
		{Name: "TestApp", BundleID: "com.test.app", Version: "1.0.0", Source: app.SourceSparkle, FeedURL: srv.URL},
		{Name: "FreshApp", BundleID: "com.test.fresh", Version: "2.0.0", Source: app.SourceSparkle, FeedURL: srv.URL},
	})

	var buf bytes.Buffer
	checkCmd.SetOut(&buf)
	checkCmd.SetContext(context.Background())
	t.Cleanup(func() { checkCmd.SetOut(nil) })

	if err := runCheck(checkCmd, nil); err != nil {
		t.Fatalf("runCheck failed: %v", err)
	}

	var entries []checkEntry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("failed to parse check JSON output: %v\noutput: %s", err, buf.String())
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}

	byName := map[string]checkEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	stale := byName["TestApp"]
	if stale.Status != "major_update" && stale.Status != "update_available" {
		t.Errorf("TestApp status = %q, want an update status", stale.Status)
	}
	if stale.LatestVersion != "2.0.0" {
		t.Errorf("TestApp latest = %q, want 2.0.0", stale.LatestVersion)
	}
	if fresh := byName["FreshApp"]; fresh.Status != "ok" {
		t.Errorf("FreshApp status = %q, want ok", fresh.Status)
	}
}

// Issue #12: matching Sparkle builds and matching/older Office casks must stay
// out of both check's update statuses and update --all --dry-run's install plan.
func TestRunCommands_CurrentBuilds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATER_AGENT_MODE", "1")
	t.Setenv("GITHUB_TOKEN", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		short, build := "1.168.0", "88413"
		if r.URL.Path == "/dia" {
			short, build = "1.52.0", "88431"
		}
		fmt.Fprintf(w, `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:version>%s</sparkle:version><sparkle:shortVersionString>%s (%s)</sparkle:shortVersionString><enclosure url="https://example.com/app.zip" /></item></channel></rss>`, build, short, build)
	}))
	defer srv.Close()

	withStubbedPipeline(t, []*app.App{
		{Name: "Arc", Version: "1.168.0", Build: "88413", Source: app.SourceSparkle, FeedURL: srv.URL + "/arc"},
		{Name: "Dia", Version: "1.52.0", Build: "88431", Source: app.SourceSparkle, FeedURL: srv.URL + "/dia"},
		{Name: "Microsoft Excel", Version: "16.113.4", Build: "16.113.26100421", CaskName: "microsoft-excel"},
		{Name: "Microsoft Outlook", Version: "16.113.4", Build: "16.113.26100421", CaskName: "microsoft-outlook"},
	})
	newRunner = func() checker.CmdRunner {
		return &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
			"brew info --cask --json=v2 microsoft-excel":   {Output: []byte(`{"casks":[{"version":"16.113.26100421","url":"https://example.com/excel.pkg"}]}`)},
			"brew info --cask --json=v2 microsoft-outlook": {Output: []byte(`{"casks":[{"version":"16.113.26091740","url":"https://example.com/outlook.pkg"}]}`)},
		}}
	}

	var buf bytes.Buffer
	checkCmd.SetOut(&buf)
	checkCmd.SetContext(context.Background())
	t.Cleanup(func() { checkCmd.SetOut(nil) })
	if err := runCheck(checkCmd, nil); err != nil {
		t.Fatal(err)
	}
	var entries []checkEntry
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("invalid check output: %v\n%s", err, buf.String())
	}
	if len(entries) != 4 {
		t.Fatalf("got %d check results, want 4", len(entries))
	}
	for _, entry := range entries {
		if entry.Status != "ok" {
			t.Errorf("%s status = %s, want ok", entry.Name, entry.Status)
		}
	}

	origAll, origDryRun, origJSON := flagAll, flagDryRun, flagDryRunJSON
	flagAll, flagDryRun, flagDryRunJSON = true, true, true
	t.Cleanup(func() { flagAll, flagDryRun, flagDryRunJSON = origAll, origDryRun, origJSON })
	buf.Reset()
	updateCmd.SetOut(&buf)
	updateCmd.SetContext(context.Background())
	t.Cleanup(func() { updateCmd.SetOut(nil) })
	if err := runUpdate(updateCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("All apps are up to date")) {
		t.Fatalf("unexpected update plan for current builds: %s", buf.String())
	}
}

// TestRunUpdateBundleIDSelector verifies --bundle-id resolves the exact app
// and reports up-to-date without attempting an update when none is available.
func TestRunUpdateBundleIDSelector(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("UPDATER_AGENT_MODE", "0")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(integrationAppcastXML))
	}))
	defer srv.Close()

	withStubbedPipeline(t, []*app.App{
		{Name: "FreshApp", BundleID: "com.test.fresh", Version: "2.0.0", Source: app.SourceSparkle, FeedURL: srv.URL},
	})

	origBundleID := flagBundleID
	t.Cleanup(func() { flagBundleID = origBundleID })

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateCmd.SetContext(context.Background())
	t.Cleanup(func() { updateCmd.SetOut(nil) })

	flagBundleID = "com.test.fresh"
	if err := runUpdate(updateCmd, nil); err != nil {
		t.Fatalf("runUpdate failed: %v", err)
	}
	if got := buf.String(); !bytes.Contains([]byte(got), []byte("up to date")) {
		t.Errorf("expected up-to-date message, got: %q", got)
	}

	flagBundleID = "com.does.not.exist"
	if err := runUpdate(updateCmd, nil); err == nil {
		t.Error("expected error for unknown bundle ID, got nil")
	}
}

func TestRunUpdateReportsUnavailableAppStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	withStubbedPipeline(t, []*app.App{
		{Name: "Store App", BundleID: "com.test.store", Version: "1.0.0", Source: app.SourceMAS, CaskName: "store-app"},
	})
	storeErr := errors.New("App Store unavailable")
	newRunner = func() checker.CmdRunner {
		return &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
			"mas outdated":                         {Err: storeErr},
			"brew info --cask --json=v2 store-app": {Output: []byte(`{"casks":[{"version":"1.1.0","url":"https://example.com/app.zip"}]}`)},
		}}
	}
	originalBundleID, originalAll, originalDryRun := flagBundleID, flagAll, flagDryRun
	t.Cleanup(func() { flagBundleID, flagAll, flagDryRun = originalBundleID, originalAll, originalDryRun })
	var out, stderr bytes.Buffer
	updateCmd.SetOut(&out)
	updateCmd.SetErr(&stderr)
	updateCmd.SetContext(context.Background())
	t.Cleanup(func() { updateCmd.SetOut(nil); updateCmd.SetErr(nil) })
	flagBundleID, flagAll, flagDryRun = "com.test.store", false, true
	if err := runUpdate(updateCmd, nil); !errors.Is(err, storeErr) {
		t.Fatalf("expected the App Store error, got %v and output %q", err, out.String())
	}
	flagBundleID, flagAll = "", true
	out.Reset()
	if err := runUpdate(updateCmd, nil); err != nil {
		t.Fatal(err)
	}
	if out.String() != "No updates available from apps checked successfully.\n" || !bytes.Contains(stderr.Bytes(), []byte(storeErr.Error())) {
		t.Fatalf("batch reported %q and warnings %q", out.String(), stderr.String())
	}
}
