package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/config"
	versionpkg "github.com/lu-zhengda/updater/internal/version"
)

// errMenubarAlreadyRunning is returned when this process should exit because
// another menu bar instance owns the session. Callers treat it as success for
// the user (the existing instance was activated) and exit without starting systray.
var errMenubarAlreadyRunning = fmt.Errorf("another Updater menu bar instance is already running")

// errMenubarStaleQuit is returned when this process must exit because it is
// stale (DMG/temp/missing executable) while a preferred instance owns the
// menu bar, or because its own executable path no longer exists.
var errMenubarStaleQuit = fmt.Errorf("this Updater menu bar instance is stale and must exit")

// menubarTerminateTimeout is how long a takeover waits for a graceful quit
// before force-terminating the peer.
const menubarTerminateTimeout = 2 * time.Second

// menubarInstanceOrigin classifies where a menubar executable lives.
type menubarInstanceOrigin int

const (
	originStale menubarInstanceOrigin = iota
	originOther
	originInstalled
)

// otherMenubarInstance describes a running peer that may own the menu bar.
type otherMenubarInstance struct {
	PID         int
	BundleID    string
	Path        string
	Version     string
	Exists      bool
	IsAccessory bool // NSApplicationActivationPolicyAccessory (LSUIElement menu bar)
}

// menubarSelf describes this process for the pure decision function.
type menubarSelf struct {
	PID     int
	Path    string
	Version string
	Exists  bool
}

// menubarAction is what ensureSingle / the periodic reclaim check should do.
type menubarAction int

const (
	menubarActionContinue menubarAction = iota
	menubarActionYield
	menubarActionTakeover
	menubarActionQuitStale
)

// menubarDecision is the pure outcome of comparing this process to peers.
type menubarDecision struct {
	Action        menubarAction
	ActivatePID   int
	TerminatePIDs []int
}

type menubarCompareResult int

const (
	menubarWeWin menubarCompareResult = iota
	menubarTheyWin
	menubarTie
)

// classifyMenubarPath reports whether path is an installed copy, a stale
// location (DMG, temp, or missing), or somewhere else (dev build, Caskroom
// resolved path that is not under Applications, etc.).
//
// home is os.UserHomeDir(); tempDir is os.TempDir(). Both may be empty in tests.
func classifyMenubarPath(path, home, tempDir string, exists bool) menubarInstanceOrigin {
	if !exists || strings.TrimSpace(path) == "" {
		return originStale
	}
	clean := filepath.Clean(path)

	if isPathUnder(clean, "/Applications") {
		return originInstalled
	}
	if home != "" && isPathUnder(clean, filepath.Join(home, "Applications")) {
		return originInstalled
	}
	// Homebrew may resolve the executable under Caskroom while the user-facing
	// bundle is the /Applications symlink. Treat that as installed.
	if strings.Contains(clean, "/Caskroom/updater/") {
		return originInstalled
	}

	if isPathUnder(clean, "/Volumes") {
		return originStale
	}
	if isMacOSVarFoldersTemp(clean) {
		return originStale
	}
	if isPathUnder(clean, "/tmp") || isPathUnder(clean, "/private/tmp") {
		return originStale
	}
	if tempDir != "" && isPathUnder(clean, filepath.Clean(tempDir)) {
		return originStale
	}
	return originOther
}

func isPathUnder(path, root string) bool {
	if root == "" {
		return false
	}
	cleanRoot := filepath.Clean(root)
	if path == cleanRoot {
		return true
	}
	prefix := cleanRoot + string(os.PathSeparator)
	return strings.HasPrefix(path, prefix)
}

// isMacOSVarFoldersTemp matches /var/folders/<vol>/<id>/T/... and the
// /private/var/folders/... form used after realpath.
func isMacOSVarFoldersTemp(path string) bool {
	for _, marker := range []string{"/private/var/folders/", "/var/folders/"} {
		idx := strings.Index(path, marker)
		if idx < 0 {
			continue
		}
		rest := path[idx+len(marker):]
		parts := strings.Split(rest, string(os.PathSeparator))
		if len(parts) >= 3 && parts[2] == "T" {
			return true
		}
	}
	return false
}

// compareMenubarPair decides which of two instances should own the menu bar.
// Strictly newer version wins; otherwise installed > other > stale. Equal
// rank is a tie (newcomer yields so two installed same-version copies keep
// today's single-instance behavior).
func compareMenubarPair(self menubarSelf, selfOrigin menubarInstanceOrigin, other otherMenubarInstance, otherOrigin menubarInstanceOrigin) menubarCompareResult {
	if versionpkg.IsNewer(other.Version, self.Version) {
		return menubarWeWin
	}
	if versionpkg.IsNewer(self.Version, other.Version) {
		return menubarTheyWin
	}
	if selfOrigin > otherOrigin {
		return menubarWeWin
	}
	if otherOrigin > selfOrigin {
		return menubarTheyWin
	}
	return menubarTie
}

// decideMenubarInstance is the pure single-instance policy.
//
// Inputs are this process's path/version/exists and each peer's
// path/version/exists (plus accessory/bundle filters). home and tempDir
// classify paths the same way on every platform so tests stay hermetic.
//
// incumbent is true for a process that already owns the menu bar (periodic
// reclaim). A newcomer (incumbent=false) yields on a tie so two installed
// same-version copies keep today's behavior; an incumbent ignores ties so it
// does not quit when it sees itself reflected, or another equal peer briefly.
func decideMenubarInstance(self menubarSelf, others []otherMenubarInstance, home, tempDir string, incumbent bool) menubarDecision {
	if !self.Exists {
		return menubarDecision{Action: menubarActionQuitStale}
	}
	selfOrigin := classifyMenubarPath(self.Path, home, tempDir, self.Exists)

	var terminate []int
	var preferredPID int
	var tiePID int

	for _, o := range others {
		if o.PID == 0 || o.PID == self.PID {
			continue
		}
		if o.BundleID != "" && o.BundleID != app.UpdaterBundleID {
			continue
		}
		// Only the accessory (menu bar) peer owns the icon; the Regular
		// updates window must not displace or be displaced as the owner.
		if !o.IsAccessory {
			continue
		}

		otherOrigin := classifyMenubarPath(o.Path, home, tempDir, o.Exists)
		switch compareMenubarPair(self, selfOrigin, o, otherOrigin) {
		case menubarWeWin:
			terminate = append(terminate, o.PID)
		case menubarTheyWin:
			if preferredPID == 0 {
				preferredPID = o.PID
			}
		case menubarTie:
			if tiePID == 0 {
				tiePID = o.PID
			}
		}
	}

	if preferredPID != 0 {
		// A strictly preferred peer owns the session. Stale selves quit as
		// stale; installed-but-older selves yield and activate the winner.
		if selfOrigin == originStale {
			return menubarDecision{Action: menubarActionQuitStale, ActivatePID: preferredPID}
		}
		return menubarDecision{Action: menubarActionYield, ActivatePID: preferredPID}
	}
	if !incumbent && tiePID != 0 && len(terminate) == 0 {
		if selfOrigin == originStale {
			return menubarDecision{Action: menubarActionQuitStale, ActivatePID: tiePID}
		}
		return menubarDecision{Action: menubarActionYield, ActivatePID: tiePID}
	}
	if len(terminate) > 0 {
		return menubarDecision{Action: menubarActionTakeover, TerminatePIDs: terminate}
	}
	return menubarDecision{Action: menubarActionContinue}
}

// ensureSingleMenubarInstance activates an existing menu bar owner and returns
// errMenubarAlreadyRunning when this process must exit. CLI subcommands never
// call this — only runMenubarApp does.
func ensureSingleMenubarInstance() error {
	return applyMenubarInstanceDecision(true)
}

// reclaimOrYieldMenubar is the periodic check for a running menu bar process.
// A stale instance quits when an installed copy is present or its executable
// vanishes; an installed copy asks stale peers to quit.
func reclaimOrYieldMenubar() error {
	return applyMenubarInstanceDecision(false)
}

func applyMenubarInstanceDecision(startup bool) error {
	selfPID := os.Getpid()
	fromBundle := runningFromAppBundle()

	if !fromBundle {
		if !startup {
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

	selfPath, err := os.Executable()
	if err != nil {
		selfPath = ""
	}
	_, statErr := os.Stat(selfPath)
	self := menubarSelf{
		PID:     selfPID,
		Path:    selfPath,
		Version: version,
		Exists:  selfPath != "" && statErr == nil,
	}

	others := enrichMenubarPeers(listOtherUpdaterInstances(selfPID))
	home, _ := os.UserHomeDir()
	decision := decideMenubarInstance(self, others, home, os.TempDir(), !startup)

	switch decision.Action {
	case menubarActionContinue:
		return nil
	case menubarActionYield:
		activateRunningApplication(decision.ActivatePID)
		return errMenubarAlreadyRunning
	case menubarActionQuitStale:
		if decision.ActivatePID != 0 {
			activateRunningApplication(decision.ActivatePID)
		}
		return errMenubarStaleQuit
	case menubarActionTakeover:
		for _, pid := range decision.TerminatePIDs {
			terminateMenubarInstance(pid, menubarTerminateTimeout)
		}
		return nil
	}
	return nil
}

// enrichMenubarPeers fills Path/Version/Exists for peers reported by AppKit.
func enrichMenubarPeers(others []otherMenubarInstance) []otherMenubarInstance {
	for i := range others {
		path := others[i].Path
		if path == "" {
			continue
		}
		_, err := os.Stat(path)
		others[i].Exists = err == nil
		if others[i].Version == "" {
			others[i].Version = versionFromExecutablePath(path)
		}
	}
	return others
}

func versionFromExecutablePath(exePath string) string {
	bundle := updaterBundlePath(exePath)
	if bundle == "" {
		return ""
	}
	info, err := readUpdaterBundle(bundle)
	if err != nil {
		return ""
	}
	return info.Version
}

// menubarLockPath is the flock used when the binary is not inside an .app bundle.
func menubarLockPath() (string, error) {
	dir := filepath.Dir(config.DefaultPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "menubar.lock"), nil
}
