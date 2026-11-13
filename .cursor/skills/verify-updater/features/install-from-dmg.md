# Install from the DMG

A person downloads the published disk image, macOS treats it as a browser download, and the Updater app inside is the signed, notarized, universal build for that version. Opening the app starts it.

## Sub-features

- `release-published` requires a GitHub Release that is not a draft and not a prerelease.
- `release-assets` requires `Updater-<version>.dmg`, `updater_<version>_darwin.tar.gz`, and `checksums.txt`.
- `release-checksums` requires the downloaded DMG and tarball to match `checksums.txt`.
- `dmg-trust` requires the quarantined DMG to pass `codesign --verify`, `stapler validate`, and `spctl --assess --type open --context context:primary-signature`, then mount.
- `app-trust` requires the mounted `Updater.app` to pass `codesign --verify --deep --strict`, `stapler validate`, and `spctl --assess --type execute`, with TeamIdentifier `XY7H3QGJ2T`, both `x86_64` and `arm64`, and `CFBundleShortVersionString` equal to the version.
- `launch` requires a quarantined copy of that app to start and then stop.

## How to get to it (user POV)

- Open the GitHub Release for the version and download `Updater-<version>.dmg`.
- Open the disk image and then open `Updater.app`.
- Drag `Updater.app` to Applications and open it from there. The harness uses a temp copy with the same quarantine flag instead of replacing `/Applications/Updater.app` for this journey.

## Driving it with verify-updater

Preconditions:

- Doctor has reported the macOS tools.
- `sh .cursor/skills/verify-updater/scripts/verify-updater.sh <version>` is the run. The commands below are what that script runs for this feature.

- **Resolve the Release.** `gh api repos/lu-zhengda/updater/releases/tags/v<version>`, or `releases/latest` when the version is omitted. `draft` and `prerelease` are false. `PASS release-published` prints the tag and asset names.
- **Download.** `curl -fsSL` writes `checksums.txt`, `Updater-<version>.dmg`, and `updater_<version>_darwin.tar.gz` from `https://github.com/lu-zhengda/updater/releases/download/v<version>/`. `shasum -a 256` matches the hash in `checksums.txt`. `PASS release-assets` and `PASS release-checksums`.
- **Quarantine the DMG.** `xattr -w com.apple.quarantine 0083;<hex-time>;Safari;<uuid> Updater-<version>.dmg`. `xattr -p com.apple.quarantine` prints that value back.
- **Assess the image.** `codesign --verify --verbose=2`, `stapler validate`, and `spctl --assess --type open --context context:primary-signature --verbose` all exit `0`. `PASS dmg-trust`.
- **Mount.** `hdiutil attach -nobrowse -readonly -mountpoint <dir> Updater-<version>.dmg`. `Updater.app` is at the volume root.
- **Assess the app.** On that bundle: `codesign --verify --deep --strict --verbose=2`, `stapler validate`, `spctl --assess --type execute --verbose`. `codesign -dvv` shows `TeamIdentifier=XY7H3QGJ2T`. `lipo -archs Contents/MacOS/updater` includes `x86_64` and `arm64`. `PlistBuddy` prints `CFBundleShortVersionString` equal to the version. `PASS app-trust`.
- **Launch a quarantined copy.** `ditto` the bundle to a temp `Updater.app`, set the same quarantine attribute, then `open -n` that bundle. A new process whose command contains that bundle's `Contents/MacOS/updater` stays running for a second. `SIGTERM` stops it. `PASS launch` includes the pid and the method.
- **Proof.** The transcript shows the three asset names, both hashes, the three DMG command exits, the team id, `lipo` arches, the plist version, and the launched pid.

## Gotchas

- v0.24.1 is an annotated tag with no GitHub Release and no DMG. `releases/tags/v0.24.1` failing is a FAIL, not a skipped asset check.
- `curl` does not set `com.apple.quarantine`. The script sets the flag itself. Removing it to make `spctl` or `hdiutil` succeed is a weakened check.
- The first LaunchServices open of a quarantined app can show a Gatekeeper dialog that System Events cannot click without Accessibility permission. The script tries `open -n`, then a timed AppleScript click of an **Open** button on an Updater "downloaded" dialog, then exec of the quarantined Mach-O with stdin closed. On the 2026-11-13 run, a trivial `osascript` returned immediately and the System Events probe was still running at 8 seconds, so the script killed it (exit 124) and did not click a dialog. If only the direct exec starts the process, `PASS launch` says the dialog was not clicked and names that substitute. If no process stays up, `launch` is FAIL.
- `ditto` keeps the signature. A plain `cp -R` can break it and produce a false FAIL.
- Do not `codesign -s` or `stapler staple` during this journey. Those belong to the release runner.
