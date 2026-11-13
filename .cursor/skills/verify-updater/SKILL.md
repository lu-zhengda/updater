---
name: verify-updater
description: "Prove a published Updater macOS release installs and updates on a real Mac: GitHub Release artifacts, a quarantined DMG, Homebrew cask install and upgrade, and in-app self-update. Use after every publish, or for a version such as 0.24.2."
---

# Verify Updater

Prove the published app, not a local build. `make app` ad-hoc signs a developer bundle and is not this proof. The harness is `scripts/verify-updater.sh`. It talks to `lu-zhengda/updater` GitHub Releases and `lu-zhengda/homebrew-tap` `Casks/updater.rb`.

Run it on a Mac. Do not sign, notarize, tag, or publish from that Mac. Do not read signing secrets. The script unsets `GITHUB_TOKEN`, `HOMEBREW_TAP_TOKEN`, and the Apple signing and notarization variables so child processes do not inherit them.

A tag without a Release fails. v0.24.1 is the example.

## Launch

There is no local server. Launch means the published `Updater.app`.

```sh
sh .cursor/skills/verify-updater/scripts/verify-updater.sh
sh .cursor/skills/verify-updater/scripts/verify-updater.sh 0.24.2
```

The version argument is `0.24.2` or `v0.24.2`. Omitted, the script uses the latest GitHub Release that is not a draft and not a prerelease.

The script is ready when it prints `RESULT pass=N fail=M` and exits. Exit `0` means every check passed. Any FAIL is exit `1`.

`updater upgrade` from an app bundle schedules a helper and returns immediately with `self-update started; Updater will restart when ready`. The helper downloads `updater_<version>_darwin.tar.gz` for a copy that is not the Homebrew cask, checks the checksum and the Developer ID team, and replaces that bundle. A cask install runs `brew upgrade --cask lu-zhengda/tap/updater` instead. The menu bar **Update** action and `updater update --bundle-id com.updater.app` call the same helper. The harness drives `upgrade` on a temp copy of the previous published bundle so it does not replace `/Applications/Updater.app` as the self-update proof.

The menu-bar app starts when the binary is launched with no arguments and stdin is not a terminal (`cmd/updater/main.go`).

## Doctor

```sh
sh .cursor/skills/verify-updater/scripts/verify-updater.sh --doctor
```

Doctor is read-only. It checks macOS and the tools the harness uses (`gh`, `curl`, `codesign`, `stapler`, `spctl`, `lipo`, `hdiutil`, `xattr`, `ditto`, `brew`, `python3`, `PlistBuddy`). It prints `brew info --cask lu-zhengda/tap/updater` and does not install, mount, or launch. Exit `1` only when a tool or macOS is missing. A cask that Homebrew cannot load is still worth driving: the brew checks record that failure.

Run doctor before a full proof, and again after a failed run whose process or mount was left behind. One full proof at a time. The self-update helper takes `~/.config/updater/update.lock`, and the log and history files are shared with the real app.

## Drive

The feature map in `features/` is the source for the user journeys. The script runs them in this order:

1. `release-published`, `release-assets`, `release-checksums`
2. `dmg-trust`, `app-trust`, `launch`
3. `self-update` on a temp copy of the previous published tarball
4. `cask-metadata`, `brew-upgrade`, `brew-install`

`brew upgrade` runs before `brew install` so an older Homebrew receipt is still in place for the upgrade. The install and upgrade commands are only:

```sh
brew upgrade --cask lu-zhengda/tap/updater
brew install --cask lu-zhengda/tap/updater
```

`HOMEBREW_NO_AUTO_UPDATE=1` is set so Homebrew does not fetch other taps. The script does not upgrade other casks or formulae.

Downloads use `curl -fsSL` from `https://github.com/lu-zhengda/updater/releases/download/v<version>/`. Quarantine is applied after download, before Gatekeeper checks:

```sh
xattr -w com.apple.quarantine "0083;<unix-time-hex>;Safari;<uuid>" <file>
```

That is the browser-download flag. `curl` does not set it.

Do not delete a FAIL by skipping a command or editing the cask under test. If Homebrew cannot load `Casks/updater.rb`, `brew-install` and `brew-upgrade` fail.

## Evidence

Every check prints `PASS <name>` or `FAIL <name>` after the command output that justifies it. The same text is appended to:

```text
~/Library/Logs/verify-updater/<UTC-timestamp>.log
```

Override the directory with `VERIFY_UPDATER_EVIDENCE_DIR`. Cleanup must leave that file in place.

The proof is the published bytes and the processes and brew receipt they produce. A local `make app` bundle, a draft release, and a tag with no Release do not count.

## Cleanup

The script traps exit and:

- force-detaches the DMG mount it created
- sends SIGTERM, then SIGKILL, only to Updater processes that were not running when the script started and whose command line matches the temp bundle or `updater-self-helper-`
- deletes helper directories created during the run
- deletes its temp work directory
- restores `/Applications/Updater.app` from a `ditto` backup if a failed self-update or brew command changed or removed it
- restores `Casks/updater.rb` in the Homebrew tap if a downgrade swapped it

It does not delete `~/Library/Logs/verify-updater/`, `~/.config/updater/logs/self-update.log`, or `~/.local/share/updater/history.json`. A real self-update appends to those two files. It does not uninstall unrelated brew packages.

A successful brew upgrade may leave the target version installed from the cask. That is the intended end state. If brew cannot load the cask, the previously installed app is left as it was.

## Helpers

`scripts/verify-updater.sh` is the only helper. Invoke it as shown under Launch. `--doctor` is the read-only check. Do not reimplement the checks ad hoc during a proof run.

## Maintain

When the app's install or update path changes, update this skill with `/maintain-verification-skill`. A behavior the map describes that the release no longer has is a product failure, not a reason to soften the check.
