# Releasing

Pushing a tag that matches `v*` runs [`.github/workflows/release.yml`](../.github/workflows/release.yml) on the GitHub-hosted `macos-latest` runner with cgo enabled. The workflow imports the Developer ID certificate into a temporary keychain, runs GoReleaser, and publishes the existing Homebrew cask. Branch pushes and pull requests do not start this job. It does not use a self-hosted runner, and it does not need tools installed on a personal Mac: Go and GoReleaser are set up in the job, and `codesign`, `notarytool`, `stapler`, `python3`, and `openssl` come from the GitHub-hosted image.

GoReleaser still builds from [`.goreleaser.yaml`](../.goreleaser.yaml): darwin amd64 and arm64, one universal binary. The universal-binary post hook then:

1. `scripts/mkapp.sh` assembles `dist/Updater.app` and, when `MACOS_SIGNING_IDENTITY` is set, signs the Mach-O and the bundle.
2. `scripts/notarize-app.sh` notarizes and staples that bundle when `NOTARIZE=1`.

Those hooks run before GoReleaser creates archives. The cask keeps using `HOMEBREW_TAP_TOKEN` and the `updater_<version>_darwin.tar.gz` archive. The checksum in the cask is the checksum of that archive, so it matches the stapled app inside it. The zip uploaded to the notary service is temporary and is not a release asset.

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

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The tag push creates the GitHub Release and updates the cask. Do not create the release or the tap commit by hand.

The job fails if notarization's status is anything other than `Accepted`. On failure it prints `xcrun notarytool log` for that submission. After a successful notarization it runs `xcrun stapler staple`, `xcrun stapler validate`, and `spctl --assess --type execute -vv`.

## Signature

`scripts/mkapp.sh` signs `Updater.app/Contents/MacOS/updater` and then `Updater.app` with `--options runtime --timestamp` and [`packaging/entitlements.plist`](../packaging/entitlements.plist). The bundle is signed second so its seal covers the signed Mach-O. `--keychain` points at the temporary keychain when `MACOS_KEYCHAIN_PATH` is set.

Entitlements:

- `com.apple.security.cs.allow-jit` and `com.apple.security.cs.allow-unsigned-executable-memory` — the Go runtime, including the cgo systray and WebKit code, maps executable memory. The hardened runtime kills the process without these. The universal binary needs both: arm64 uses JIT mapping, amd64 uses writable executable pages.
- `com.apple.security.automation.apple-events` — notifications, moving an app to the Trash, and package-install prompts go through `osascript`. The hardened runtime blocks Apple Events without this entitlement.

Library validation stays on. The binary links Apple system frameworks (Cocoa, WebKit) and does not load a third-party dylib. `com.apple.security.get-task-allow` is not set; notarization rejects it.

The workflow's keychain steps:

- Create a keychain with a random password and keep it unlocked for the job.
- Import the `.p12` (certificate and private key).
- `security set-key-partition-list` so `codesign` can use the key without a prompt.
- `security list-keychains` so that keychain is the user search list, and make it the default keychain.
- Delete the `.p12` at the end of the import step.
- `security delete-keychain` and delete the `.p8` in an `always()` step, including when a later step fails.

Signing material is not printed. The random keychain password and the certificate password are passed to `::add-mask::`.
