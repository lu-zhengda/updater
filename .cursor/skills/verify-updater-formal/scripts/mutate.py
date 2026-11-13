#!/usr/bin/env python3
"""Break one check at a time and require the Go test to fail, then restore."""

import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[4]

mutations = [
    {
        "name": "two os.Rename instead of RENAME_SWAP",
        "file": "cmd/updater/self_upgrade.go",
        "old": """func replaceUpdaterBundle(bundle, candidate, backup string) (error, bool) {
	if err := unix.RenamexNp(candidate, bundle, unix.RENAME_SWAP); err != nil {
		return err, false
	}
	if err := os.Rename(candidate, backup); err != nil {
		return nil, false
	}
	return nil, false
}""",
        "new": """func replaceUpdaterBundle(bundle, candidate, backup string) (error, bool) {
	if err := os.Rename(bundle, backup); err != nil {
		return err, false
	}
	if err := os.Rename(candidate, bundle); err != nil {
		restoreErr := os.Rename(backup, bundle)
		return errors.Join(err, restoreErr), restoreErr == nil
	}
	return nil, false
}""",
        "args": [
            "go", "test", "-count=1", "-timeout", "180s",
            "-run", "TestSelfUpgradeGuardrails|TestReplaceUpdaterBundleSwaps",
            "./cmd/updater/",
        ],
    },
    {
        "name": "two os.Rename rollback",
        "file": "cmd/updater/self_upgrade.go",
        "old": """		if swapErr := unix.RenamexNp(backup, bundle, unix.RENAME_SWAP); swapErr != nil {
			return false, true, swapErr
		}
		if moveErr := os.Rename(backup, failed); moveErr != nil {
			return true, true, nil
		}
		return true, false, nil""",
        "new": """		if moveErr := os.Rename(bundle, failed); moveErr != nil {
			return false, true, moveErr
		}
		if restoreErr := os.Rename(backup, bundle); restoreErr != nil {
			return false, true, restoreErr
		}
		return true, false, nil""",
        "args": [
            "go", "test", "-count=1", "-timeout", "180s",
            "-run", "TestRestoreUpdaterBundleSwaps",
            "./cmd/updater/",
        ],
    },
    {
        "name": "strip v before version-",
        "file": "internal/checker/github.go",
        "old": """	prefixes := []string{"version-", "version/", "release-", "release/", "ver-", "ver/", "v"}
	for _, p := range prefixes {
		if after, ok := strings.CutPrefix(tag, p); ok {
			return after
		}
	}
	return tag""",
        "new": """	prefixes := []string{"v", "release-", "release/", "ver-", "ver/", "version-", "version/"}
	result := tag
	for _, p := range prefixes {
		result = strings.TrimPrefix(result, p)
	}
	return result""",
        "args": [
            "go", "test", "-count=1", "-timeout", "60s",
            "-run", "TestCleanTagVersionPrefixes",
            "./internal/checker/",
        ],
    },
    {
        "name": "keep an empty cask version after a comma",
        "file": "internal/checker/brew_info.go",
        "old": """	if idx := strings.IndexByte(v, ','); idx != -1 {
		v = v[:idx]
	}
	if v == "" {
		return brewCaskArtifact{}, fmt.Errorf("empty version in brew info response")
	}""",
        "new": """	if v == "" {
		return brewCaskArtifact{}, fmt.Errorf("empty version in brew info response")
	}
	if idx := strings.IndexByte(v, ','); idx != -1 {
		v = v[:idx]
	}""",
        "args": [
            "go", "test", "-count=1", "-timeout", "60s",
            "-run", "TestParseBrewInfo/comma_only",
            "./internal/checker/",
        ],
    },
    {
        "name": "accept a non-hex checksum",
        "file": "cmd/updater/upgrade.go",
        "old": """		for _, r := range checksum {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return "", fmt.Errorf("invalid SHA-256 checksum for %s", assetName)
			}
		}
		return checksum, nil""",
        "new": """		return checksum, nil""",
        "args": [
            "go", "test", "-count=1", "-timeout", "180s",
            "-run", "TestChecksumForAssetRejectsInvalid",
            "./cmd/updater/",
        ],
    },
    {
        "name": "treat every semver as newer",
        "file": "internal/version/compare.go",
        "old": "\t\treturn lv.GreaterThan(cv)\n",
        "new": "\t\treturn lv.GreaterThan(cv) || true\n",
        "args": [
            "go", "test", "-count=1", "-timeout", "60s",
            "-run", "TestIsNewer/older_major",
            "./internal/version/",
        ],
    },
    {
        "name": "accept a cask version lower than the target",
        "file": "cmd/updater/self_upgrade.go",
        "old": """func caskVersionAllowed(cask, target, installed string) bool {
	target = versionpkg.ReleaseVersion(target)
	installed = versionpkg.ReleaseVersion(installed)
	if versionpkg.IsNewer(cask, target) || versionpkg.IsNewer(cask, installed) {
		return false
	}
	return true
}""",
        "new": """func caskVersionAllowed(cask, target, installed string) bool {
	return true
}""",
        "args": [
            "go", "test", "-count=1", "-timeout", "180s",
            "-run", "TestSelfUpgradeRefusesLowCaskVersion",
            "./cmd/updater/",
        ],
    },
    {
        "name": "leave a missing app unrestored",
        "file": "cmd/updater/self_upgrade.go",
        "old": """	backup, err := findUpdaterBackup(bundle)
	if err != nil {
		return false, err
	}
	stages, err := listSelfStages(bundle)""",
        "new": """	return false, nil
	backup, err := findUpdaterBackup(bundle)
	if err != nil {
		return false, err
	}
	stages, err := listSelfStages(bundle)""",
        "args": [
            "go", "test", "-count=1", "-timeout", "180s",
            "-run", "TestRecoverStrandedUpdater",
            "./cmd/updater/",
        ],
    },
]


def main() -> int:
    failed = False
    for spec in mutations:
        path = root / spec["file"]
        original = path.read_text()
        if spec["old"] not in original:
            print(f"mutation {spec['name']}: current code does not contain the guarded snippet", file=sys.stderr)
            return 1
        path.write_text(original.replace(spec["old"], spec["new"], 1))
        try:
            result = subprocess.run(spec["args"], cwd=root)
        finally:
            path.write_text(original)
        if result.returncode == 0:
            print(f"mutation {spec['name']} was not caught", file=sys.stderr)
            failed = True
        else:
            print(f"CAUGHT go {spec['name']} (test exit {result.returncode})")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
