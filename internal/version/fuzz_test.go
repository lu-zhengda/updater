package version

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

// FuzzIsNewer checks the comparison the self-update uses to refuse a downgrade.
// When both sides parse as semver after normalize, IsNewer matches that order.
// A version is not newer than itself, and both directions are not true.
func FuzzIsNewer(f *testing.F) {
	f.Add("1.0.0", "1.2.3")
	f.Add("2.0.0", "1.0.0")
	f.Add("v1.2.3", "1.2.3")
	f.Add("1.0.0-rc.1", "1.0.0")
	f.Add("1.0.0", "1.0.0-rc.2")
	f.Add("1.10.0", "1.9.0")
	f.Add("", "1.0.0")
	f.Add("1.0.0", "")
	f.Add("1.2.3-5-gabcdef", "1.2.3")
	f.Add("100", "99")
	f.Fuzz(func(t *testing.T, current, latest string) {
		if len(current) > 80 || len(latest) > 80 {
			t.Skip()
		}
		newer := IsNewer(current, latest)
		if current == latest && newer {
			t.Fatalf("IsNewer(%q, %q) = true", current, latest)
		}
		if newer && IsNewer(latest, current) {
			t.Fatalf("both directions newer: %q and %q", current, latest)
		}
		cv, errC := semver.NewVersion(normalize(current))
		lv, errL := semver.NewVersion(normalize(latest))
		if errC == nil && errL == nil && newer != lv.GreaterThan(cv) {
			t.Fatalf("IsNewer(%q, %q) = %v, semver %s > %s = %v", current, latest, newer, lv, cv, lv.GreaterThan(cv))
		}
	})
}
