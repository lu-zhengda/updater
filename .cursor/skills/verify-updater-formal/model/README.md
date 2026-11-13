# Self-update model

TLC checks `SelfUpdate.tla`. One step is one step in `cmd/updater/self_upgrade.go`. Versions are 1, 2, and 3. The direct tarball path and the Homebrew cask path are the same module with different constants.

Brew owns a cask install. The helper does not copy a bundle into the Caskroom and does not rewrite the receipt. It holds `update.lock` from `updateActivityLock` through `brew upgrade --cask`, the Developer ID check, and the bundle-version check. Before brew, `caskVersionAllowed` refuses a cask version lower than the GitHub target or the installed app. After brew returns, a failed check calls `restoreUpdaterBundle`. `recoverStrandedUpdater` runs on the next `self-upgrade` and on launch when the app is missing or unsigned and `Previous.app` is still in the stage directory.

## Properties that hold

TLC 2.19, one worker, no symmetry.

| Config | What it is | States | Depth |
| --- | --- | --- | --- |
| `direct.cfg` | Tarball helper | 5713 generated, 1728 distinct | 16 |
| `brew-ok.cfg` | Cask version is the fetched release, and brew either leaves the bundle or installs that signed version | 2049 generated, 814 distinct | 18 |
| `brew-regress.cfg` | `brew info` can report a version other than the GitHub tag | 3493 generated, 1458 distinct | 18 |

On `direct.cfg`, these hold: AlwaysPresent, NoDowngrade, NoBadInstall, MutualExclusion, LockSpansBrew.

On the cask path, `brew-ok.cfg` and `brew-regress.cfg` both pass the same five:

- **AlwaysPresent.** The install path has a complete Updater.app, for a brew that either does not change the bundle or installs the cask version the guard accepted.
- **NoDowngrade.** That app is the original version or a newer one. This holds when the cask version can differ from the GitHub tag. A reported version of 1 is refused while the app is 2 and the fetched release is 3. Brew is not called.
- **NoBadInstall.** The app at the path passed the checksum and Developer ID checks, or it is the app that was already installed, for those same brew results. The post-check rolls a failed signature or a low bundle version back to the ditto backup when the helper is still alive.
- **MutualExclusion.** Two helpers do not both hold `update.lock`.
- **LockSpansBrew.** The helper holds that lock through the cask-version guard, the ditto, `brew upgrade`, and the post-checks.

## Accepted boundaries

These configs must keep violating the named property. They are not open bugs to be deleted, and the invariants are not weakened to make them pass. Homebrew's cask sha256 and Gatekeeper notarization still apply to the bytes brew installs. They do not make brew's replace atomic with this process.

| Config | Property that fails | State |
| --- | --- | --- |
| `brew-partial.cfg` | AlwaysPresent | `brew upgrade` removes the bundle and does not return. A kill of the helper does not run the in-process rollback. The path is empty. The backup remains. |
| `brew-unsigned.cfg` | NoBadInstall | Brew has returned a bundle and the helper has not yet run the Developer ID and bundle-version checks. A kill in that window leaves the bundle. |

`recoverStrandedUpdater` runs after the helper is dead or idle. It puts the backup back. That is a later state. The empty path and the unchecked bundle are still states, so AlwaysPresent and NoBadInstall do not hold there. Removing the restore does not change those TLC results. The Go test `TestRecoverStrandedUpdater` is what fails when the restore is removed.

## Mutation that reopens the downgrade

`mutate-skip-cask-version.cfg` sets `SkipCaskVersion`. NoDowngrade fails. 5019 generated, 2217 distinct, depth 16.

The trace is the old counterexample. Helper 1 fetches release 2, the guard accepts cask version 2, and brew installs it (`trusted` becomes 2). Helper 2 fetches release 3. With the guard removed it accepts cask version 1 and brew installs 1. `appVer` is 1 and `trusted` is 2.

`caskVersionAllowed` returning true without the comparison fails `TestSelfUpgradeRefusesLowCaskVersion` for the same reason: `brew upgrade` runs.
