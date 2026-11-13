# Updater release verification map

This directory is the maintained source for proving that a person can install and update the published Updater app. Read the index, then use the matching feature file.

## Baseline preconditions

- A Mac with the tools named in the skill's Doctor section.
- `gh` can read public `lu-zhengda/updater` and `lu-zhengda/homebrew-tap`.
- The version under test is a published GitHub Release, not only a git tag.
- One verification run at a time. Self-update uses `~/.config/updater/update.lock`.
- Do not sign, notarize, tag, or publish from this Mac.

## Driving conventions

- Run `sh .cursor/skills/verify-updater/scripts/verify-updater.sh <version>`.
- The script prints `PASS` or `FAIL` and the command output for each check.
- A FAIL stays a FAIL. Do not edit the cask, strip quarantine, or skip a command to turn it into a pass.
- The menu-bar Update click is not driven. `menu-update` runs the command that click executes. Do not report the click as verified because that command moved the version.

## Proof and skip reporting

- Capture the command, its exit code, and the resulting version, receipt, or process.
- The transcript path is `~/Library/Logs/verify-updater/<UTC-timestamp>.log`. Cleanup leaves it there.
- Report an entry point that was not driven, and why.

## Feature entry contract

Each feature file has an H1, one paragraph, then four H2 sections: `Sub-features`, `How to get to it (user POV)`, `Driving it with verify-updater`, and `Gotchas`.

## Features

- [Install from the DMG](./install-from-dmg.md) covers the Release assets, the quarantined disk image, the app inside it, and launching that app.
- [Homebrew cask](./homebrew-cask.md) covers the tap cask checksum and `brew upgrade` / `brew install` of `lu-zhengda/tap/updater`.
- [Update Updater](./self-update.md) covers `updater upgrade` and the menu-bar `update --bundle-id` command moving an older signed bundle to the published version.
