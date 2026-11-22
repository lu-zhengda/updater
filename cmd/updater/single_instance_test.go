package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestClassifyMenubarPath(t *testing.T) {
	const home = "/Users/test"
	const tempDir = "/var/folders/xx/yy/T"
	tests := []struct {
		name   string
		path   string
		exists bool
		want   menubarInstanceOrigin
	}{
		{name: "missing", path: "/Applications/Updater.app/Contents/MacOS/updater", exists: false, want: originStale},
		{name: "empty", path: "", exists: true, want: originStale},
		{name: "applications", path: "/Applications/Updater.app/Contents/MacOS/updater", exists: true, want: originInstalled},
		{name: "user applications", path: "/Users/test/Applications/Updater.app/Contents/MacOS/updater", exists: true, want: originInstalled},
		{name: "caskroom", path: "/opt/homebrew/Caskroom/updater/0.25.2/Updater.app/Contents/MacOS/updater", exists: true, want: originInstalled},
		{name: "dmg volumes", path: "/Volumes/Updater/Updater.app/Contents/MacOS/updater", exists: true, want: originStale},
		{name: "var folders T", path: "/private/var/folders/xx/yy/T/updater-test/Updater.app/Contents/MacOS/updater", exists: true, want: originStale},
		{name: "tmp", path: "/tmp/Updater.app/Contents/MacOS/updater", exists: true, want: originStale},
		{name: "os tempdir", path: "/var/folders/xx/yy/T/copy/Updater.app/Contents/MacOS/updater", exists: true, want: originStale},
		{name: "dev checkout", path: "/Users/test/src/updater/Updater.app/Contents/MacOS/updater", exists: true, want: originOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyMenubarPath(tt.path, home, tempDir, tt.exists)
			if got != tt.want {
				t.Fatalf("classifyMenubarPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDecideMenubarInstance(t *testing.T) {
	const (
		selfPID = 100
		home    = "/Users/test"
		tempDir = "/var/folders/xx/yy/T"
		v252    = "0.25.2"
		v253    = "0.25.3"
	)
	installed := "/Applications/Updater.app/Contents/MacOS/updater"
	userInstalled := "/Users/test/Applications/Updater.app/Contents/MacOS/updater"
	dmg := "/Volumes/Updater/Updater.app/Contents/MacOS/updater"
	tempExe := "/private/var/folders/xx/yy/T/gone/Updater.app/Contents/MacOS/updater"
	deleted := "/private/var/folders/aa/bb/T/deleted/Updater.app/Contents/MacOS/updater"

	tests := []struct {
		name       string
		self       menubarSelf
		others     []otherMenubarInstance
		incumbent  bool
		wantAction menubarAction
		wantTerm   []int
		wantActPID int
	}{
		{
			name:       "no peers",
			self:       menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			wantAction: menubarActionContinue,
		},
		{
			name: "installed vs DMG takes over",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 200, BundleID: app.UpdaterBundleID, Path: dmg, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{200},
		},
		{
			name: "installed vs temp takes over",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 201, BundleID: app.UpdaterBundleID, Path: tempExe, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{201},
		},
		{
			name: "installed vs deleted path takes over",
			self: menubarSelf{PID: selfPID, Path: userInstalled, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 202, BundleID: app.UpdaterBundleID, Path: deleted, Version: v252, Exists: false, IsAccessory: true,
			}},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{202},
		},
		{
			name: "newer vs older takes over",
			self: menubarSelf{PID: selfPID, Path: dmg, Version: v253, Exists: true},
			others: []otherMenubarInstance{{
				PID: 203, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{203},
		},
		{
			name: "same installed twice newcomer yields",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 204, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionYield,
			wantActPID: 204,
		},
		{
			name:      "same installed twice incumbent continues",
			incumbent: true,
			self:      menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 204, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionContinue,
		},
		{
			name: "both stale newcomer yields",
			self: menubarSelf{PID: selfPID, Path: dmg, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 205, BundleID: app.UpdaterBundleID, Path: tempExe, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionQuitStale,
			wantActPID: 205,
		},
		{
			name:      "both stale incumbent continues",
			incumbent: true,
			self:      menubarSelf{PID: selfPID, Path: dmg, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 205, BundleID: app.UpdaterBundleID, Path: tempExe, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionContinue,
		},
		{
			name: "stale yields to installed",
			self: menubarSelf{PID: selfPID, Path: dmg, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 206, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionQuitStale,
			wantActPID: 206,
		},
		{
			name:      "stale incumbent quits when installed appears",
			incumbent: true,
			self:      menubarSelf{PID: selfPID, Path: dmg, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 206, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionQuitStale,
			wantActPID: 206,
		},
		{
			name: "own path missing quits",
			self: menubarSelf{PID: selfPID, Path: deleted, Version: v252, Exists: false},
			others: []otherMenubarInstance{{
				PID: 207, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionQuitStale,
		},
		{
			name:       "own path missing alone quits",
			self:       menubarSelf{PID: selfPID, Path: deleted, Version: v252, Exists: false},
			wantAction: menubarActionQuitStale,
		},
		{
			name: "ignores self pid",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: selfPID, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionContinue,
		},
		{
			name: "does not yield to Regular window peer",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 300, BundleID: app.UpdaterBundleID, Path: installed, Version: v252, Exists: true, IsAccessory: false,
			}},
			wantAction: menubarActionContinue,
		},
		{
			name: "ignores other bundle ids",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 400, BundleID: "com.other.app", Path: installed, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionContinue,
		},
		{
			name: "older installed yields to newer",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 208, BundleID: app.UpdaterBundleID, Path: dmg, Version: v253, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionYield,
			wantActPID: 208,
		},
		{
			name:      "incumbent installed takes over stale peers",
			incumbent: true,
			self:      menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{{
				PID: 210, BundleID: app.UpdaterBundleID, Path: dmg, Version: v252, Exists: true, IsAccessory: true,
			}},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{210},
		},
		{
			name: "prefers accessory when window and menubar exist",
			self: menubarSelf{PID: selfPID, Path: installed, Version: v252, Exists: true},
			others: []otherMenubarInstance{
				{PID: 301, BundleID: app.UpdaterBundleID, Path: dmg, Version: v252, Exists: true, IsAccessory: false},
				{PID: 209, BundleID: app.UpdaterBundleID, Path: dmg, Version: v252, Exists: true, IsAccessory: true},
			},
			wantAction: menubarActionTakeover,
			wantTerm:   []int{209},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideMenubarInstance(tt.self, tt.others, home, tempDir, tt.incumbent)
			if got.Action != tt.wantAction {
				t.Fatalf("Action = %v, want %v (decision=%+v)", got.Action, tt.wantAction, got)
			}
			if got.ActivatePID != tt.wantActPID {
				t.Fatalf("ActivatePID = %d, want %d", got.ActivatePID, tt.wantActPID)
			}
			if len(got.TerminatePIDs) != len(tt.wantTerm) {
				t.Fatalf("TerminatePIDs = %v, want %v", got.TerminatePIDs, tt.wantTerm)
			}
			for i := range tt.wantTerm {
				if got.TerminatePIDs[i] != tt.wantTerm[i] {
					t.Fatalf("TerminatePIDs = %v, want %v", got.TerminatePIDs, tt.wantTerm)
				}
			}
		})
	}
}

func TestMenubarLockPathUnderConfigDir(t *testing.T) {
	path, err := menubarLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || filepath.Base(path) != "menubar.lock" {
		t.Fatalf("unexpected lock path %q", path)
	}
}

func TestTryAcquireMenubarLockExclusive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// config.DefaultPath uses HOME/.config/updater/config.yaml; lock lives beside it.
	held, err := tryAcquireMenubarLock()
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("expected first acquire to succeed")
	}
	t.Cleanup(func() {
		if menubarLockFile != nil {
			_ = menubarLockFile.Close()
			menubarLockFile = nil
		}
	})
	path, err := menubarLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}
