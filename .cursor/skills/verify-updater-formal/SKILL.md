---
name: verify-updater-formal
description: "Prove the Updater self-update helper leaves a signed Updater.app at the old or newer version. Use when changing self-upgrade, update.lock, downloadSelfArchive, checksumForAsset, VerifyReplacementApp, replaceUpdaterBundle, restoreUpdaterBundle, IsNewer, CleanTagVersion, or parseBrewInfo."
---

# Verify the Updater self-update

The helper behind `updater upgrade` (when the binary is inside `Updater.app`) and the menu-bar `update --bundle-id` is `applySelfUpgrade` in `cmd/updater/self_upgrade.go`. It takes `~/.config/updater/update.lock`, downloads the release for a non-cask install, checks the checksum and the Developer ID team, and replaces the bundle. A cask install runs `brew upgrade --cask` and then checks the result.

A test checks cases someone wrote. This check covers every order of those steps inside the bounds below, and generated inputs for the parsers.

## Properties

Each property is one sentence. The harm is the sentence after it.

- **AlwaysPresent.** The install path has a complete Updater.app. Harm: a kill leaves the user with no app.
- **NoDowngrade.** That app is the original version or a newer one, never an older one. Harm: the user loses a version they already had.
- **NoBadInstall.** That app passed the checksum check and the Developer ID team check, or it is the app that was already installed. Harm: a bad download or a bad signature replaces the app.
- **MutualExclusion.** Two helpers do not both hold the exclusive `update.lock`. Harm: two replaces run at once.

## Run

```sh
sh .cursor/skills/verify-updater-formal/scripts/run.sh
```

The script needs Java 11 or later and network access the first time, so it can download tla2tools 1.7.4 (sha256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`). It runs TLC, the Go tests, a broken variant of each guarded check, and `go test -fuzz` for 10 seconds per target. `FUZZ_TIME=5s` shortens the fuzz. Exit 0 means the passing configs passed and every broken variant failed the property named in the script.

Read every TLC result with `/statechart`. Do not copy that skill's commands into this one. A counterexample is a bug in the Go only after you replay it on the functions named in `model/SelfUpdate.tla`. If a step in the trace cannot happen in those functions, the model is wrong. Fix the model. Do not weaken a property or delete a step to make TLC pass.

`/maintain-formal-verification-skill` is how to bring this check back in line after edits that did not re-run it.

## What passed

TLC 2.19, one worker, no symmetry.

| Config | Result | States | Depth |
| --- | --- | --- | --- |
| `model/direct.cfg` | pass: AlwaysPresent, NoDowngrade, NoBadInstall, MutualExclusion | 5713 generated, 1728 distinct | 16 |
| `model/brew-ok.cfg` | pass, only if `brew upgrade` either leaves the bundle untouched or installs a newer signed app | 1585 generated, 572 distinct | 16 |

`direct.cfg` is the tarball helper: two helpers, versions 1, 2, and 3, download outcomes good / bad checksum / bad signature / version mismatch / network drop, the shared lock, and a kill from any phase. Checksum and signature run before `replaceUpdaterBundle`. The replace is one `renamex_np(RENAME_SWAP)`. A kill in any phase leaves version 1 or a newer signed version.

`brew-ok.cfg` is not a proof that Homebrew is atomic. It is the helper's own steps when brew's result is already good.

## Broken variants that must fail

| Config or Go mutation | Property that fails |
| --- | --- |
| `mutate-two-rename.cfg` and the two-`os.Rename` body of `replaceUpdaterBundle` | AlwaysPresent. After the checksum and the Developer ID check, the first rename empties the install path. |
| `brew-two-rename.cfg` and the two-`os.Rename` body of `restoreUpdaterBundle` | AlwaysPresent. The failed bundle is moved aside before the backup is moved back. |
| `mutate-skip-checksum.cfg` and a checksum parser that accepts non-hex | NoBadInstall |
| `mutate-skip-signature.cfg` | NoBadInstall. `VerifyReplacementApp` is skipped and the swap still runs. |
| `mutate-skip-newer.cfg`, `mutate-skip-relock.cfg` | NoDowngrade. The second helper installs release 2 after the first has installed release 3. |
| `mutate-skip-lock.cfg` | NoDowngrade. Both helpers pass the recheck while the app is still version 1, one installs 3, the other then installs 2. |
| `mutate-skip-version.cfg` | NoDowngrade. An archive whose plist version is 1 is installed over version 2. |
| `scripts/mutate.py` `return lv.GreaterThan(cv) \|\| true` | `TestIsNewer/older_major` |

## Findings still open

These are counterexamples in the current Go. They are not fixed. Do not delete the configs to make the script pass.

**Brew can leave no app.** `brew-partial.cfg`, invariant AlwaysPresent. Trace: read the installed app, fetch a newer release, skip the tarball download (`isUpdaterCask`), take `update.lock`, recheck, ditto a backup, `brew upgrade` removes the bundle and does not return. A kill of the helper in `runner.Run` (`self_upgrade.go` brew upgrade) does not run the rollback. The install path is empty. The backup is still in the stage directory.

**Brew can leave an unchecked app.** `brew-unsigned.cfg`, invariant NoBadInstall. The same steps, and `brew upgrade` returns with a bundle that has not passed `verifySelfUpgrade`. The Developer ID check runs only after brew returns (`self_upgrade.go` after the upgrade). A kill between those two lines leaves that bundle.

**A later brew upgrade can install an older app.** `brew-regress.cfg`, invariant NoDowngrade. Helper 1 fetches release 2 and brew installs it. Helper 2 fetches release 3, so the recheck (`IsNewer`) allows it, and brew then installs version 1. The GitHub tag and the cask version are not the same number. The helper notices only after brew returns, and rolls back only if it is still alive.

The fix is not a one-line change: brew replaces the bundle before this process can check it. Do not paper over it by modeling brew as atomic.

## Coverage

Sync commit: `ddd85b566130b8e084045fd5b20b9e93e333de4e`. The covered Go at that commit is what this model and the fuzz describe. Move this hash forward only after you confirm the model and the fuzz still describe the code, and re-run the script.

| Code | Check |
| --- | --- |
| `applySelfUpgrade`, `downloadSelfArchive`, `extractUpdaterApp` version match, `updateActivityLock`, `verifySelfUpgrade`, `stopUpdaterUI`, `replaceUpdaterBundle`, `restoreUpdaterBundle` | `model/SelfUpdate.tla`. Each action names the function. |
| `replaceUpdaterBundle` / `restoreUpdaterBundle` now use `RENAME_SWAP` | fix `5164435ff3f215e192badb0cde5445174423b031` |
| `CleanTagVersion` | fix `a0545e4f57fc074142dcc734e41e6cc21b9dd4f4`. Fuzz `FuzzGitHubRelease`. |
| `parseBrewInfo` | fix `ddd85b566130b8e084045fd5b20b9e93e333de4e`. Fuzz `FuzzParseBrewInfo`. |
| `IsNewer` | `FuzzIsNewer`. Semver after `normalize` matches `semver.GreaterThan`. Not both directions. Not newer than itself. |
| `checksumForAsset` | `FuzzChecksumForAsset` |

Not in the model, and not claimed:

- Tar path traversal and the 512 MiB cap (`TestSelfUpgradeArchiveRejectsUnsafeEntries`).
- The bytes of a codesign or `spctl` transcript. The Developer ID gate is one step, `VerifyReplacementApp`.
- `runUpgrade` on a loose binary. That path is one `os.Rename` of a file, not this helper, and it does not take `update.lock`.
- Menu-bar restart, history, and logs.
- `ReleaseVersion` on a stack of git-describe suffixes. Bundle versions are one `CFBundleShortVersionString`.
- Versions other than 1, 2, and 3. Fuzz covers the string comparison; the model covers the order of the steps.
- Homebrew's own download. The open findings above are that gap.

## Fuzz

`testing.F`, 10 seconds each in the script:

- `FuzzIsNewer` in `internal/version`
- `FuzzGitHubRelease` in `internal/checker` (release JSON and `CleanTagVersion`)
- `FuzzParseBrewInfo` in `internal/checker`
- `FuzzChecksumForAsset` in `cmd/updater`

CI job `formal` runs this script. Job `Test` runs `go test -race`, which includes the fuzz seeds and the regression tests.
