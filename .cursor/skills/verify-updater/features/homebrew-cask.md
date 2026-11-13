# Homebrew cask

A person installs Updater with Homebrew and later upgrades an older install to the published version. The cask in `lu-zhengda/homebrew-tap` names that version and the sha256 of `updater_<version>_darwin.tar.gz`.

## Sub-features

- `cask-metadata` requires GitHub `Casks/updater.rb` on `main` to use the release version and the tarball sha256, and the Homebrew tap checkout to match that file.
- `brew-upgrade` requires `brew upgrade --cask lu-zhengda/tap/updater` to move an older Homebrew install onto the release. The app's `CFBundleShortVersionString` and the cask receipt version both end on the release.
- `brew-install` requires `brew install --cask lu-zhengda/tap/updater` to exit `0` with that same app version and receipt.

## How to get to it (user POV)

- `brew install --cask lu-zhengda/tap/updater` from the tap README.
- `brew upgrade --cask lu-zhengda/tap/updater` when an older cask install is present.

## Driving it with verify-updater

Preconditions:

- The release tarball for this version has already matched `checksums.txt`.
- `lu-zhengda/tap` is tapped in Homebrew.
- `/Applications/Updater.app` is backed up with `ditto` before these commands. A failed command that removes or replaces it is restored from that backup.
- `HOMEBREW_NO_AUTO_UPDATE=1` so this run does not fetch other taps.

- **Read the cask.** `gh api -H "Accept: application/vnd.github.raw" repos/lu-zhengda/homebrew-tap/contents/Casks/updater.rb`. The `version` string equals the release. The `sha256` equals `shasum -a 256` of the downloaded tarball and the hash in `checksums.txt`. `cmp` the file with `$(brew --repository lu-zhengda/tap)/Casks/updater.rb`. `PASS cask-metadata`.
- **Upgrade from older.** Read `$(brew --prefix)/Caskroom/updater/.metadata/INSTALL_RECEIPT.json` field `source.version` and the installed app's `CFBundleShortVersionString`. At least one must be older than the release. If both are already the release and `brew info --cask lu-zhengda/tap/updater` exits `0`, the script checks out an older `Casks/updater.rb` from that tap's git history, installs it, restores the published cask file, then upgrades. `brew upgrade --cask lu-zhengda/tap/updater` exits `0`. The app version and the receipt are the release. `PASS brew-upgrade`.
- **Install.** `brew install --cask lu-zhengda/tap/updater` exits `0`. The app version and the receipt are the release. `PASS brew-install`.
- **Proof.** The transcript includes the cask version, both sha256 strings, the receipt before and after upgrade, and both brew exit codes.

## Gotchas

- The generated cask can contain both `depends_on macos: :tahoe` and `depends_on :macos`. Current Homebrew then refuses to load the file and points at line 20. `brew info`, `brew install`, and `brew upgrade` fail before changing the app. That is a FAIL of `brew-install` and `brew-upgrade`. Do not edit the cask during verification to get a pass.
- Do not run a bare `brew upgrade --cask`. That upgrades every outdated cask on the machine.
- `brew install` of an already-installed cask can exit `0` without changing the receipt. The check still requires the receipt version to equal the release, so a no-op on an older receipt is FAIL.
- Swapping `Casks/updater.rb` is only for the downgrade path, only when the published cask loads, and the script copies the published file back before `brew upgrade`. A crash still restores it from the temp copy.
- The cask's `preflight` deletes `Updater.app` from the app directory. A failed install after that point is why the backup exists.
