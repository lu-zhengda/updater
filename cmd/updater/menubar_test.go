package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRenderMenubarPlist_LoginItemShape(t *testing.T) {
	content, err := renderMenubarPlist(menubarPlistData{
		Label:   menubarPlistLabel,
		Binary:  "/Applications/Updater.app/Contents/MacOS/updater",
		LogPath: "/tmp/updater-menubar.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "<string>com.updater.menubar</string>") {
		t.Fatal("expected LaunchAgent label com.updater.menubar")
	}
	if !strings.Contains(content, "<string>menubar</string>") || !strings.Contains(content, "<string>run</string>") {
		t.Fatal("expected ProgramArguments menubar run")
	}
	// RunAtLoad must stay so the next login starts the agent; KeepAlive restarts crashes.
	if !strings.Contains(content, "<key>RunAtLoad</key>") || !strings.Contains(content, "<true/>") {
		t.Fatal("expected RunAtLoad true for next-login start")
	}
	if !strings.Contains(content, "<key>KeepAlive</key>") {
		t.Fatal("expected KeepAlive")
	}
}

// recordingRunner captures launchctl (and any other) invocations for assertions.
type recordingRunner struct {
	mu   sync.Mutex
	cmds []string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil, nil
}

func (r *recordingRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.cmds))
	copy(out, r.cmds)
	return out
}

func TestInstallMenubarAgent_EnableDoesNotLoadOrOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	// loadNow false is what Start at Login / `updater menubar` use.
	if err := installMenubarAgent(context.Background(), runner, false); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", menubarPlistLabel+".plist")
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "<key>RunAtLoad</key>") {
		t.Fatal("plist must keep RunAtLoad for the next login")
	}

	for _, cmd := range runner.commands() {
		if strings.Contains(cmd, "launchctl load") || strings.Contains(cmd, "launchctl bootstrap") ||
			strings.Contains(cmd, "launchctl kickstart") || strings.HasPrefix(cmd, "open ") {
			t.Fatalf("enable must not start the agent now; got %q", cmd)
		}
	}
}

func TestInstallMenubarAgent_LoadNowCallsLaunchctlLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	if err := installMenubarAgent(context.Background(), runner, true); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", menubarPlistLabel+".plist")
	var sawLoad bool
	for _, cmd := range runner.commands() {
		if cmd == "launchctl load "+plistPath {
			sawLoad = true
		}
		if strings.HasPrefix(cmd, "open ") {
			t.Fatalf("must not open the app on install; got %q", cmd)
		}
	}
	if !sawLoad {
		t.Fatalf("loadNow true should call launchctl load; got %#v", runner.commands())
	}
}

func TestInstallMenubarAgent_NoUnloadWhenNotLoading(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(agents, menubarPlistLabel+".plist")
	if err := os.WriteFile(plistPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	if err := installMenubarAgent(context.Background(), runner, false); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range runner.commands() {
		if strings.Contains(cmd, "launchctl") {
			t.Fatalf("register-without-load must not touch launchctl; got %q", cmd)
		}
	}
	raw, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "old" || !strings.Contains(string(raw), "RunAtLoad") {
		t.Fatal("expected rewritten plist with RunAtLoad")
	}
}

func TestToggleLoginItemUsesRegisterWithoutLoad(t *testing.T) {
	// Document the contract used by toggleLoginItem: installMenubarAgent(..., false).
	// A fake systray checkbox is not required; the loadNow flag is the regression seam.
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &recordingRunner{}
	if err := installMenubarAgent(context.Background(), runner, false); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range runner.commands() {
		if strings.Contains(cmd, "load") && strings.Contains(cmd, "launchctl") {
			t.Fatalf("toggle path must not launchctl load: %q", cmd)
		}
	}
}
