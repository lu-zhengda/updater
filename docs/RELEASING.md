# Releasing

Publishing a GitHub Release and updating the Homebrew cask always runs the same GitHub-hosted `macos-latest` job, [`.github/workflows/release-publish.yml`](../.github/workflows/release-publish.yml), with cgo enabled. That job imports the Developer ID certificate into a temporary keychain, runs GoReleaser, and publishes the existing Homebrew cask. It does not use a self-hosted runner, and it does not need tools installed on a personal Mac: Go and GoReleaser are set up in the job, and `codesign`, `notarytool`, `stapler`, `hdiutil`, `ditto`, `python3`, and `openssl` come from the GitHub-hosted image.

Two workflows call that job:

- [`.github/workflows/release-on-merge.yml`](../.github/workflows/release-on-merge.yml) runs when a pull request is merged to `main`. It reads the pull request labels, and when exactly one semver label is present it creates an annotated tag on the merge commit and then calls the publish job.
- [`.github/workflows/release.yml`](../.github/workflows/release.yml) runs when a tag matching `v*` is pushed. A tag pushed from a clone still publishes this way.

`GITHUB_TOKEN` cannot start the tag workflow by pushing a tag. The merge workflow therefore calls the publish job with `workflow_call` instead of waiting for that push. Signing, notarization, and the Homebrew cask update stay in the publish job on both paths. Secret names are unchanged.

A branch push, a closed pull request that was not merged, and a push straight to `main` with no merged pull request do not publish. The decision uses the pull request's labels, which a bare push does not have.

Merge publishes and manual tag publishes share one concurrency group. A second publish waits. It does not cancel a run that is already signing, and it does not replace a tag.

GoReleaser still builds from [`.goreleaser.yaml`](../.goreleaser.yaml): darwin amd64 and arm64, one universal binary. The universal-binary post hook then:

1. `scripts/mkapp.sh` assembles `dist/Updater.app` and, when `MACOS_SIGNING_IDENTITY` is set, signs the Mach-O and the bundle.
2. `scripts/notarize-app.sh` notarizes and staples that bundle when `NOTARIZE=1`.
3. `scripts/mkdmg.sh` builds `dist/Updater-<version>.dmg` from that app, signs the disk image, and, when `NOTARIZE=1`, notarizes and staples the image.

Those hooks finish before GoReleaser creates archives or checksums. The first hook, `scripts/completions.sh`, writes `completions/updater.bash`, `completions/updater.zsh`, and `completions/updater.fish` from the universal binary. `completions/` is gitignored, so without that hook the archive glob `completions/*` matches nothing and the cask's completion links fail install. The macOS CI job runs `scripts/cask-archive-proof.sh`, which installs a snapshot of that tarball with the generated cask and checks the app, the `updater` link, and all three completions. The cask keeps using `HOMEBREW_TAP_TOKEN` and the `updater_<version>_darwin.tar.gz` archive. The checksum in the cask is the checksum of that archive, so it matches the stapled app inside it. `checksums.txt` lists that archive and `Updater-<version>.dmg`. The zip uploaded for the app notarization is temporary and is not a release asset. The DMG is a release asset, named the same way as the hand-cut `Updater-0.23.0.dmg`.

GoReleaser v2.20.1 has no Homebrew field for a macOS version. Its cask template always adds `depends_on :macos` when the cask has macOS packages and no Linux packages. Current Homebrew refuses to load that line together with a second `depends_on macos:` stanza. The cask `custom_block` defines `depends_on` so the bare call becomes `depends_on macos: :tahoe` (macOS 26) and formula dependencies still pass through. Signing and notarization are unchanged.

GoReleaser v2.20.1's documented `dmg` section is Pro-only. The open-source `goreleaser` binary this workflow runs has no DMG pipe, and that Pro section packages a binary or a Pro app bundle rather than the already stapled `dist/Updater.app`. `scripts/mkdmg.sh` is the step that publishes the image.

`make app` is unchanged for local development: it ad-hoc signs unless you export `MACOS_SIGNING_IDENTITY`.

## Secrets

| Secret | Format |
| --- | --- |
| `MACOS_CERT_P12_BASE64` | Base64 of the password-protected `.p12`, one line, no `data:` prefix |
| `MACOS_CERT_PASSWORD` | Password chosen when exporting the `.p12` |
| `APPSTORE_API_KEY_P8_BASE64` | Base64 of the `AuthKey_<KeyID>.p8` file (PKCS#8 PEM), one line |
| `APPSTORE_API_KEY_ID` | Key ID shown in App Store Connect (the filename suffix) |
| `APPSTORE_API_ISSUER_ID` | Issuer ID shown above the Team Keys table |
| `HOMEBREW_TAP_TOKEN` | Existing token that can push to `lu-zhengda/homebrew-tap`. Not part of signing. |

The `.p8` secret is base64, not the raw PEM. A one-line secret is harder to corrupt when it is piped into `gh secret set`.

The signing identity is not a secret. The workflow uses `Developer ID Application: Zhengda Lu (XY7H3QGJ2T)`.

The temporary keychain password is generated in the job (`openssl rand`) and masked. It is not a repository secret.

## Export the Developer ID certificate

1. Open Keychain Access.
2. Select the **login** keychain and the **My Certificates** category.
3. Find **Developer ID Application: Zhengda Lu (XY7H3QGJ2T)**.
4. Expand the certificate and select both the certificate and its private key.
5. Choose **File → Export Items…**.
6. Set the file format to **Personal Information Exchange (.p12)** and save it.
7. Choose an export password and store that password in a file. macOS also asks for the login keychain password so it can read the private key.

Confirm the `.p12` contains a private key before encoding it (Keychain Access shows a key under the certificate, and `security import` on another Mac can list it). A certificate without its key cannot sign.

## Create the App Store Connect API key

1. Open [App Store Connect → Users and Access → Integrations → Team Keys](https://appstoreconnect.apple.com/access/integrations/api).
2. Generate an API key with the **Developer** role. A name such as `updater-notarization` is enough.
3. Download `AuthKey_<KeyID>.p8`. Apple allows that download once.
4. Copy the **Key ID** and the **Issuer ID**.

`notarytool` reads this key with `--key`, `--key-id`, and `--issuer`. The workflow writes the decoded `.p8` to a file under `$RUNNER_TEMP`, uses it only for `notarytool`, and deletes it in an `always()` step. It does not store a notarytool keychain profile.

## Set the secrets

Run these from any directory. Each value is read from a file or from stdin, not from a command argument.

```sh
REPO=lu-zhengda/updater

base64 < /path/to/updater-developer-id.p12 | tr -d '\n' | gh secret set MACOS_CERT_P12_BASE64 --repo "$REPO"

gh secret set MACOS_CERT_PASSWORD --repo "$REPO" < /path/to/p12-password.txt

base64 < /path/to/AuthKey_XXXXXXXXXX.p8 | tr -d '\n' | gh secret set APPSTORE_API_KEY_P8_BASE64 --repo "$REPO"

gh secret set APPSTORE_API_KEY_ID --repo "$REPO" < /path/to/key-id.txt
gh secret set APPSTORE_API_ISSUER_ID --repo "$REPO" < /path/to/issuer-id.txt
```

Do not pass secret values as `gh` arguments (`--body`, inline strings, or command substitution in the argument list). Those show up in the process list and in shell history.

A trailing newline in the password, key ID, or issuer file is stripped in the workflow. The base64 commands above already remove the line wraps `base64` inserts.

After `gh secret set` succeeds, delete the local password file and the `.p8` from machines that do not need them. `HOMEBREW_TAP_TOKEN` is already set; do not rotate it while enabling signing.

## Cut a release

Add exactly one of these labels to the pull request before it is merged to `main`. The bump is standard SemVer from the latest stable `vX.Y.Z` tag in the repository at merge time. Pre-release tags are not a base version. The source tree is not edited; GoReleaser reads the version from the tag.

| Label | Bump | From `v0.23.0` |
| --- | --- | --- |
| `semver:patch` | patch | `v0.23.1` |
| `semver:minor` | minor | `v0.24.0` |
| `semver:major` | major | `v1.0.0` |

The example column is that rule applied to `v0.23.0`. The base at merge time is whatever the latest stable tag is then.

- No semver label: the workflow succeeds and does not create a tag or a release. Docs-only and workflow-only pull requests stay unlabeled.
- More than one semver label: the workflow fails and does not guess a bump.
- The merge commit already has a tag, or the computed tag already exists: the workflow fails and does not move or replace the tag.
- The new tag is an annotated `vX.Y.Z` tag on the merge commit. A squash merge tags the squash commit on `main`.

Other labels may sit beside the one semver label. They do not change the bump.

If the tag is created and the publish job then fails, re-run that failed job. It checks out the existing tag and publishes. Re-running the whole merge workflow stops, because the tag already exists and the workflow will not move it.

### Manual tag

A `v*` tag pushed from a clone still runs the same publish job, including a version the labels would not choose:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

Do not create the GitHub Release or the tap commit by hand. The publish job does both. The app and the DMG are separate notarizations. The job fails unless both statuses are `Accepted`. On failure it prints `xcrun notarytool log` for that submission. After the app is accepted it staples the bundle and runs `xcrun stapler validate` and `spctl --assess --type execute -vv`. After the DMG is accepted it staples the image and runs `xcrun stapler validate`, `codesign --verify`, and `spctl --assess --type open --context context:primary-signature`.

Secret names, the signing identity, and the notarization inputs are unchanged.

## Signature

`scripts/mkapp.sh` signs `Updater.app/Contents/MacOS/updater` and then `Updater.app` with `--options runtime --timestamp` and [`packaging/entitlements.plist`](../packaging/entitlements.plist). The bundle is signed second so its seal covers the signed Mach-O. `--keychain` points at the temporary keychain when `MACOS_KEYCHAIN_PATH` is set.

Entitlements:

- `com.apple.security.cs.allow-jit` and `com.apple.security.cs.allow-unsigned-executable-memory` — the Go runtime, including the cgo systray and WebKit code, maps executable memory. The hardened runtime kills the process without these. The universal binary needs both: arm64 uses JIT mapping, amd64 uses writable executable pages.
- `com.apple.security.automation.apple-events` — notifications, moving an app to the Trash, and package-install prompts go through `osascript`. The hardened runtime blocks Apple Events without this entitlement.

Library validation stays on. The binary links Apple system frameworks (Cocoa, WebKit) and does not load a third-party dylib. `com.apple.security.get-task-allow` is not set; notarization rejects it.

`scripts/mkdmg.sh` copies that stapled app with `ditto`, adds a symlink to `/Applications`, and creates a UDZO APFS image with volume name `Updater`. It signs the image with `codesign --force --sign <identity> --timestamp`. A disk image is not an executable, so this signature does not use the hardened runtime or the app entitlements. `--keychain` is the same temporary keychain. When `NOTARIZE=1`, the script submits the DMG itself to `notarytool` (it is not zipped), staples the ticket onto the image, and mounts the image to check that the app inside still verifies, is stapled, and passes `spctl --assess --type execute`. The app and the image are separate notarizations, so a download of either one can be checked offline.

The workflow's keychain steps:

- Create a keychain with a random password and keep it unlocked for the job.
- Import the `.p12` (certificate and private key).
- `security set-key-partition-list` so `codesign` can use the key without a prompt.
- `security list-keychains` so that keychain is the user search list, and make it the default keychain.
- Delete the `.p12` at the end of the import step.
- `security delete-keychain` and delete the `.p8` in an `always()` step, including when a later step fails.

Signing material is not printed. The random keychain password and the certificate password are passed to `::add-mask::`.
