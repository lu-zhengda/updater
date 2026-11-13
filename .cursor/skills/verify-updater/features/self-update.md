# Update Updater

Updater can replace itself. An older signed copy, asked to upgrade, ends on the published version without a person clicking the menu bar.

## Sub-features

- `older-bundle` installs the newest older published Release that has `updater_<version>_darwin.tar.gz`, checks that archive's checksum, and reads a signed `Updater.app` from it.
- `self-update` runs that bundle's `upgrade` command and waits until its `CFBundleShortVersionString` is the target version.

## How to get to it (user POV)

- In the menu bar, choose **Update** on the Updater row, **Update All**, or turn on YOLO Mode. Those call the same helper as the CLI and are not driven headlessly.
- Run `updater upgrade` from `Updater.app/Contents/MacOS/updater`.
- Run `updater update --bundle-id com.updater.app`. That scans installed apps and then starts the same helper. The harness does not drive it in the same run, because it would take the same `update.lock`.

## Driving it with verify-updater

Preconditions:

- The target Release exists and its tarball checksum has been checked.
- `/Applications/Updater.app` was backed up. This journey must not change that copy. If it does, the check fails and the backup is restored.
- No other verification run holds `~/.config/updater/update.lock`.

- **Pick the older Release.** List non-draft, non-prerelease tags. Keep the newest version older than the target that publishes `updater_<version>_darwin.tar.gz`. For a target of `0.24.2` that is `0.24.0`. v0.24.1 has no Release, so it is not a candidate.
- **Install that bundle in temp.** `curl` the tarball and `checksums.txt`, match `shasum -a 256`, and `tar -xzf` it. `Updater.app` is at the archive root. `codesign -dvv` is recorded. `CFBundleShortVersionString` is the older version.
- **Upgrade.** Run `<temp>/Updater.app/Contents/MacOS/updater upgrade`. A bundle install schedules a detached helper (`self-upgrade <bundle>`) and prints `self-update started; Updater will restart when ready`. Poll the temp bundle's plist for up to 180 seconds.
- **Confirm.** The temp bundle's `CFBundleShortVersionString` equals the target. New lines in `~/.config/updater/logs/self-update.log` are part of the transcript. `/Applications/Updater.app` is unchanged. `PASS self-update`.
- **Stop the helper.** SIGTERM, then SIGKILL if needed, for new processes whose command contains the temp bundle or `updater-self-helper-`. Remove helper directories created during the run.

## Gotchas

- `updater upgrade` returns before the helper finishes. Exiting `0` with the scheduled message is not the proof. The plist version is the proof.
- A Homebrew cask path runs `brew upgrade --cask lu-zhengda/tap/updater` inside the helper. The temp bundle is not the cask, so this journey uses the tarball replacement path. The Homebrew feature covers the cask path.
- The helper refuses the replacement unless the old app has a Developer ID team and the downloaded app matches that team, bundle id, and Gatekeeper's execute assessment. An ad-hoc older bundle failing here is a FAIL, not a prompt to disable the check.
- The helper appends `~/.config/updater/logs/self-update.log` and `~/.local/share/updater/history.json`. Cleanup does not delete them.
- The menu-bar click, the updates window, and YOLO Mode are not headless. Say they were not driven. Do not report `PASS` for them because `upgrade` succeeded.
- `upgrade` on a loose binary under `/opt/homebrew` prints `Installed via Homebrew` and does not replace the app. Drive the `Updater.app` bundle, not that symlink.
