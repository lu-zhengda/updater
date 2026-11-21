package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestShouldYieldToMenubarInstance(t *testing.T) {
	const self = 100
	tests := []struct {
		name       string
		fromBundle bool
		others     []otherMenubarInstance
		wantYield  bool
		wantPID    int
	}{
		{
			name:       "no peers",
			fromBundle: true,
			wantYield:  false,
		},
		{
			name:       "ignores self",
			fromBundle: true,
			others:     []otherMenubarInstance{{PID: self, BundleID: app.UpdaterBundleID, IsAccessory: true}},
			wantYield:  false,
		},
		{
			name:       "yields to accessory peer with same bundle id",
			fromBundle: true,
			others: []otherMenubarInstance{{
				PID: 200, BundleID: app.UpdaterBundleID, IsAccessory: true, FromSameBundle: true,
			}},
			wantYield: true,
			wantPID:   200,
		},
		{
			name:       "does not yield to Regular window peer",
			fromBundle: true,
			others: []otherMenubarInstance{{
				PID: 300, BundleID: app.UpdaterBundleID, IsAccessory: false, FromSameBundle: true,
			}},
			wantYield: false,
		},
		{
			name:       "ignores other bundle ids",
			fromBundle: true,
			others:     []otherMenubarInstance{{PID: 400, BundleID: "com.other.app", IsAccessory: true}},
			wantYield:  false,
		},
		{
			name:       "non-bundle yields to any other pid",
			fromBundle: false,
			others:     []otherMenubarInstance{{PID: 500}},
			wantYield:  true,
			wantPID:    500,
		},
		{
			name:       "prefers accessory when both window and menubar exist",
			fromBundle: true,
			others: []otherMenubarInstance{
				{PID: 301, BundleID: app.UpdaterBundleID, IsAccessory: false},
				{PID: 201, BundleID: app.UpdaterBundleID, IsAccessory: true},
			},
			wantYield: true,
			wantPID:   201,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotYield, gotPID := shouldYieldToMenubarInstance(self, tt.fromBundle, tt.others)
			if gotYield != tt.wantYield || gotPID != tt.wantPID {
				t.Fatalf("shouldYieldToMenubarInstance() = (%v, %d), want (%v, %d)",
					gotYield, gotPID, tt.wantYield, tt.wantPID)
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
