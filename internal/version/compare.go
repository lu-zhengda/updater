package version

import (
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// IsNewerOrEqual reports whether version is >= threshold.
func IsNewerOrEqual(threshold, version string) bool {
	if version == threshold {
		return true
	}
	return IsNewer(threshold, version) || version == threshold
}

// IsNewer reports whether latest is a newer version than current.
// It handles semver, two-part versions, plain build numbers, and
// v-prefixed versions.
func IsNewer(current, latest string) bool {
	if latest == "" {
		return false
	}
	if current == "" {
		return true
	}

	currentNorm := normalize(current)
	latestNorm := normalize(latest)

	cv, errC := semver.NewVersion(currentNorm)
	lv, errL := semver.NewVersion(latestNorm)

	if errC == nil && errL == nil {
		return lv.GreaterThan(cv)
	}

	// Fallback: try plain string/numeric comparison.
	return stringFallback(current, latest)
}

// normalize strips a leading 'v' and pads a version to at least 3 parts
// so that the semver library can parse it.
func normalize(v string) string {
	v = strings.TrimPrefix(v, "v")

	// If it's a pure integer (build number), return as x.0.0
	if _, err := strconv.Atoi(v); err == nil {
		return v + ".0.0"
	}

	parts := strings.SplitN(v, ".", 4)
	switch len(parts) {
	case 1:
		return v + ".0.0"
	case 2:
		return v + ".0"
	default:
		return v
	}
}

// IsMajorUpgrade reports whether latest has a higher major version than current.
// Returns false for non-semver versions, pure build numbers, or when latest is not newer.
func IsMajorUpgrade(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}

	// Pure integer build numbers (e.g., "100" → "200") are not real major upgrades.
	if _, err := strconv.Atoi(strings.TrimPrefix(current, "v")); err == nil {
		return false
	}

	cv, errC := semver.NewVersion(normalize(current))
	lv, errL := semver.NewVersion(normalize(latest))

	if errC != nil || errL != nil {
		return false
	}

	return lv.Major() > cv.Major()
}

// stringFallback compares versions that semver can't parse by splitting
// on '.' and comparing each numeric segment left to right.
func stringFallback(current, latest string) bool {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	cp := strings.Split(current, ".")
	lp := strings.Split(latest, ".")

	// Pad to equal length
	for len(cp) < len(lp) {
		cp = append(cp, "0")
	}
	for len(lp) < len(cp) {
		lp = append(lp, "0")
	}

	for i := range cp {
		cn, errC := strconv.Atoi(cp[i])
		ln, errL := strconv.Atoi(lp[i])
		if errC != nil || errL != nil {
			// Non-numeric segment: compare lexicographically
			if lp[i] > cp[i] {
				return true
			}
			if lp[i] < cp[i] {
				return false
			}
			continue
		}
		if ln > cn {
			return true
		}
		if ln < cn {
			return false
		}
	}
	return false
}
