package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
)

// Opt-in: exercise real vendor DMGs against temporary copies of installed
// apps. Never quit, launch, or replace the user's installed applications.
func TestLiveDesktopDMGInstall(t *testing.T) {
	dir := os.Getenv("UPDATER_TEST_DESKTOP_DMGS")
	if dir == "" {
		t.Skip("set UPDATER_TEST_DESKTOP_DMGS to a directory containing googlechrome-diagnostic.dmg and muse-diagnostic.dmg")
	}
	for _, tc := range []struct{ name, dmg string }{
		{"Google Chrome", "googlechrome-diagnostic.dmg"},
		{"Muse", "muse-diagnostic.dmg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			runner := &checker.RealCmdRunner{}
			installed := filepath.Join("/Applications", tc.name+".app")
			target := filepath.Join(t.TempDir(), tc.name+".app")
			if _, err := runner.Run(ctx, "ditto", installed, target); err != nil {
				t.Fatal(err)
			}
			inst := New(runner, nil)
			if err := inst.installDMG(ctx, filepath.Join(dir, tc.dmg), target, tc.name); err != nil {
				t.Fatal(err)
			}
			// Recheck the final bundle after the staged copy is renamed into place.
			if err := inst.verifier.VerifyReplacementApp(ctx, installed, target); err != nil {
				t.Fatal(err)
			}
			version, err := runner.Run(ctx, "/usr/bin/plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", filepath.Join(target, "Contents", "Info.plist"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Installed %s %s in a temporary directory; signature, identity, native architecture and Gatekeeper checks passed", tc.name, strings.TrimSpace(string(version)))
		})
	}
}
