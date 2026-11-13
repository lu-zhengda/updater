---
name: verify-updater-formal
description: "Prove the Updater self-update helper leaves a signed Updater.app at the old or newer version. Use when changing self-upgrade, update.lock, caskVersionAllowed, recoverStrandedUpdater, downloadSelfArchive, checksumForAsset, VerifyReplacementApp, replaceUpdaterBundle, restoreUpdaterBundle, IsNewer, CleanTagVersion, or parseBrewInfo."
---

# Verify the Updater self-update

The helper behind `updater upgrade` (when the binary is inside `Updater.app`) and the menu-bar `update --bundle-id` is `applySelfUpgrade` in `cmd/updater/self_upgrade.go`. It takes `~/.config/updater/update.lock`, downloads the release for a non-cask install, checks the checksum and the Developer ID team, and replaces the bundle. A cask install does not copy a bundle into the Caskroom or rewrite the receipt. It holds `update.lock` across `brew upgrade --cask`, refuses a cask version lower than the target release or the installed app, and after brew returns checks the Developer ID team and the bundle version. Failure restores the ditto backup. The next run or launch restores that backup when the app is missing or unsigned.

A test checks cases someone wrote. This check covers every order of those steps inside the bounds below, and generated inputs for the parsers.

## Properties

Each property is one sentence. The harm is the sentence after it.

- **AlwaysPresent.** The install path has a complete Updater.app. Harm: a kill leaves the user with no app.
- **NoDowngrade.** That app is the original version or a newer one, never an older one. Harm: the user loses a version they already had.
- **NoBadInstall.** That app passed the checksum check and the Developer ID team check, or it is the app that was already installed. Harm: a bad download or a bad signature replaces the app.
- **MutualExclusion.** Two helpers do not both hold the exclusive `update.lock`. Harm: two replaces run at once.
- **LockSpansBrew.** A helper inside the replacement, including the cask-version guard and `brew upgrade`, holds that lock. Harm: brew runs without the lock, or the guard runs outside it.

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
| `model/direct.cfg` | pass: AlwaysPresent, NoDowngrade, NoBadInstall, MutualExclusion, LockSpansBrew | 5713 generated, 1728 distinct | 16 |
| `model/brew-ok.cfg` | pass, when `brew upgrade` either leaves the bundle untouched or installs the signed cask version the guard accepted | 2049 generated, 814 distinct | 18 |
| `model/brew-regress.cfg` | pass: the same properties, when `brew info` can report a version other than the GitHub tag | 3493 generated, 1458 distinct | 18 |

`direct.cfg` is the tarball helper: two helpers, versions 1, 2, and 3, download outcomes good / bad checksum / bad signature / version mismatch / network drop, the shared lock, and a kill from any phase. Checksum and signature run before `replaceUpdaterBundle`. The replace is one `renamex_np(RENAME_SWAP)`. A kill in any phase leaves version 1 or a newer signed version.

`brew-ok.cfg` is not a proof that Homebrew is atomic. It is the helper's own steps when the cask version is the fetched release and brew's result is already good.

`brew-regress.cfg` is the case the old model failed. Helper 1 can install cask version 2. Helper 2 fetched release 3. If `brew info` then reports version 1, `caskVersionAllowed` refuses and brew is not called. NoDowngrade holds. The trace that installs version 1 after version 2 is `mutate-skip-cask-version.cfg`.

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
| `mutate-skip-cask-version.cfg` and `caskVersionAllowed` returning true | NoDowngrade. After version 2 is committed, brew installs cask version 1. `TestSelfUpgradeRefusesLowCaskVersion` fails on the same removal. |
| `scripts/mutate.py` recovery returns before it looks for `Previous.app` | `TestRecoverStrandedUpdater`. The missing app stays missing. TLC does not turn green when this restore is removed: the empty path is still a state. |
| `scripts/mutate.py` `return lv.GreaterThan(cv) \|\| true` | `TestIsNewer/older_major` |

## Accepted on the cask path

These two counterexamples stay in the script. They must keep failing the named property. Deleting the config, or changing the property so TLC passes, hides a real state. `model/README.md` records the same boundary.

**Brew can leave no app.** `brew-partial.cfg`, invariant AlwaysPresent. Trace: read the installed app, fetch a newer release, skip the tarball (`isUpdaterCask`), take `update.lock`, recheck, the cask version is the release, ditto a backup, `brew upgrade` removes the bundle and does not return (`runner.Run` in `applySelfUpgrade`). A kill there does not run the in-process rollback. The install path is empty. The backup is still `.updater-self-*/Previous.app`. The next `self-upgrade` run and the next launch call `recoverStrandedUpdater` and put that backup back. The empty path is still a state, so AlwaysPresent does not hold there. That window is brew's own replace. The bytes brew installs are still covered by the cask sha256 and by Gatekeeper notarization. This model does not treat brew as atomic.

**Brew can leave an unchecked app.** `brew-unsigned.cfg`, invariant NoBadInstall. Brew returns a bundle, and the Developer ID and bundle-version checks run only after that return. A kill between those lines leaves the bundle. If the helper stays alive, `restoreUpdaterBundle` puts the backup back and the error is returned. The same bytes were checked by Homebrew's sha256 and by Gatekeeper notarization. NoBadInstall does not hold in the kill window. Do not weaken it.

## Coverage

Sync commit: `32db92c890bdd017550252225af5c7cb6e8853c4`. The covered Go at that commit is what this model and the fuzz describe. Move this hash forward only after you confirm the model and the fuzz still describe the code, and re-run the script.

| Code | Check |
| --- | --- |
| `applySelfUpgrade`, `downloadSelfArchive`, `extractUpdaterApp` version match, `updateActivityLock`, `caskVersionAllowed`, `readCaskVersion`, `recoverStrandedUpdater`, `verifySelfUpgrade`, `stopUpdaterUI`, `replaceUpdaterBundle`, `restoreUpdaterBundle` | `model/SelfUpdate.tla`. Each action names the function. |
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
- Homebrew's own download, the cask sha256 check, and Gatekeeper's notarization of the bottle. Those are why `brew-partial.cfg` and `brew-unsigned.cfg` are accepted boundaries rather than steps this helper can close. The helper still refuses a lower cask version before calling brew, and it still checks the bundle brew left behind when the helper is alive.

## Fuzz

`testing.F`, 10 seconds each in the script:

- `FuzzIsNewer` in `internal/version`
- `FuzzGitHubRelease` in `internal/checker` (release JSON and `CleanTagVersion`)
- `FuzzParseBrewInfo` in `internal/checker`
- `FuzzChecksumForAsset` in `cmd/updater`

Job `Test` runs `go test -race`, which includes the fuzz seeds and the regression tests. This script is CI job `formal` in `.github/workflows/ci.yml`. It runs on pull requests and on pushes to `main`, on `macos-latest`, with `timeout-minutes: 15`. TLC finishes in seconds, and fuzz is four targets at 10 seconds.

```yaml
formal:
  runs-on: macos-latest
  timeout-minutes: 15
  steps:
    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
    - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
      with:
        go-version: "1.27.x"
    - uses: actions/setup-java@77dd5b2ce225ea11469928ac317850245a74ae78 # v6.2.0
      with:
        distribution: temurin
        java-version: "21"
    - name: Formal check
      run: sh .cursor/skills/verify-updater-formal/scripts/run.sh
```
