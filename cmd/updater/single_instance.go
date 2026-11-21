package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/config"
)

// errMenubarAlreadyRunning is returned when this process should exit because
// another menu bar instance owns the session. Callers treat it as success for
// the user (the existing instance was activated) and exit without starting systray.
var errMenubarAlreadyRunning = fmt.Errorf("another Updater menu bar instance is already running")

// otherMenubarInstance describes a running peer that may own the menu bar.
type otherMenubarInstance struct {
	PID            int
	BundleID       string
	IsAccessory    bool // NSApplicationActivationPolicyAccessory (LSUIElement menu bar)
	FromSameBundle bool
}

// shouldYieldToMenubarInstance decides whether this process should activate a
// peer and exit instead of opening a second menu bar icon.
//
// Rules:
//   - Ignore self (same PID).
//   - Prefer another instance of this bundle id that is still an accessory app
//     (the menu bar process). The updates window promotes itself to Regular and
//     must not be treated as the menu bar owner.
//   - When not running from a bundle, any other reported peer with a matching
//     empty-or-same identity yields (flock covers the common non-bundle case).
func shouldYieldToMenubarInstance(selfPID int, fromBundle bool, others []otherMenubarInstance) (yield bool, activatePID int) {
	for _, o := range others {
		if o.PID == 0 || o.PID == selfPID {
			continue
		}
		if fromBundle {
			if o.BundleID != "" && o.BundleID != app.UpdaterBundleID {
				continue
			}
			// Only yield to the accessory (menu bar) peer, not the Regular window.
			if !o.IsAccessory {
				continue
			}
			return true, o.PID
		}
		return true, o.PID
	}
	return false, 0
}

// ensureSingleMenubarInstance activates an existing menu bar owner and returns
// errMenubarAlreadyRunning when this process must exit. CLI subcommands never
// call this — only runMenubarApp does.
func ensureSingleMenubarInstance() error {
	selfPID := os.Getpid()
	fromBundle := runningFromAppBundle()

	if fromBundle {
		others := listOtherUpdaterInstances(selfPID)
		if yield, pid := shouldYieldToMenubarInstance(selfPID, true, others); yield {
			activateRunningApplication(pid)
			return errMenubarAlreadyRunning
		}
		return nil
	}

	held, err := tryAcquireMenubarLock()
	if err != nil {
		return fmt.Errorf("single-instance lock: %w", err)
	}
	if !held {
		return errMenubarAlreadyRunning
	}
	return nil
}

// menubarLockPath is the flock used when the binary is not inside an .app bundle.
func menubarLockPath() (string, error) {
	dir := filepath.Dir(config.DefaultPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "menubar.lock"), nil
}
