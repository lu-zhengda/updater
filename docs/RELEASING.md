# Releasing

Pushing a tag that matches `v*` runs [`.github/workflows/release.yml`](../.github/workflows/release.yml) on the GitHub-hosted `macos-latest` runner with cgo enabled. The release job uses the `release` GitHub Environment. GitHub waits for that environment's required reviewer before the job starts, and only then injects the environment secrets. The job then refuses to sign or run GoReleaser unless the tagged commit is already on `origin/main`. Branch pushes and pull requests do not start this job. It does not use a self-hosted runner, and it does not need tools installed on a personal Mac: Go and GoReleaser are set up in the job, and `codesign`, `notarytool`, `stapler`, `python3`, and `openssl` come from the GitHub-hosted image.

GoReleaser still builds from [`.goreleaser.yaml`](../.goreleaser.yaml): darwin amd64 and arm64, one universal binary. The universal-binary post hook then:

1. `scripts/mkapp.sh` assembles `dist/Updater.app` and, when `MACOS_SIGNING_IDENTITY` is set, signs the Mach-O and the bundle.
2. `scripts/notarize-app.sh` notarizes and staples that bundle when `NOTARIZE=1`.

Those hooks run before GoReleaser creates archives. The cask keeps using `HOMEBREW_TAP_TOKEN` and the `updater_<version>_darwin.tar.gz` archive. The checksum in the cask is the checksum of that archive, so it matches the stapled app inside it. The zip uploaded to the notary service is temporary and is not a release asset.

`make app` is unchanged for local development: it ad-hoc signs unless you export `MACOS_SIGNING_IDENTITY`.

## Environment

Create the `release` environment in the GitHub UI before the first tag push: [repository environments](https://github.com/lu-zhengda/updater/settings/environments). If the environment does not exist, the first workflow run that names it can create an empty one with no reviewer, and the signing secrets would be available without approval.

On that environment:

- **Required reviewers:** Zhengda Lu. One approval lets the job continue.
- **Prevent self-review:** leave this off. Zhengda is the only reviewer, so turning it on would mean the person who pushed the tag can never approve it.
- **Deployment branches and tags:** Selected branches and tags. Add a branch rule `main` and a tag rule `v*`. Name patterns are configured separately for branches and tags. The release workflow runs on tags, so the tag rule is what allows a `v*` push to request the environment. The `main` rule is for a job on `main` that uses the same environment, such as a dry-run.
- Leave the wait timer unset.

Every tag push, and every dry-run whose job uses `environment: release`, waits for Zhengda's approval before GitHub injects these secrets. Approval is the start of the job. The ancestry check and signing run only after that.

## Secrets

Store these as **environment secrets** on `release`, not as repository secrets. A repository secret is visible to every workflow. An environment secret is injected only into jobs that target `release`, and only after the reviewer approves.

| Secret | Format |
| --- | --- |
| `MACOS_CERT_P12_BASE64` | Base64 of the password-protected `.p12`, one line, no `data:` prefix |
| `MACOS_CERT_PASSWORD` | Password chosen when exporting the `.p12` |
| `APPSTORE_API_KEY_P8_BASE64` | Base64 of the `AuthKey_<KeyID>.p8` file (PKCS#8 PEM), one line |
| `APPSTORE_API_KEY_ID` | Key ID shown in App Store Connect (the filename suffix) |
| `APPSTORE_API_ISSUER_ID` | Issuer ID shown above the Team Keys table |
| `HOMEBREW_TAP_TOKEN` | Token that can push to `lu-zhengda/homebrew-tap`. Same environment as the signing secrets. |

The five signing secrets are the certificate, its password, the `.p8`, the key ID, and the issuer ID. `HOMEBREW_TAP_TOKEN` is the sixth environment secret. The names match the workflow. Do not rename them.

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

Create the `release` environment in the GitHub UI first. Then set the secrets on that environment. Each value is read from a file or from stdin, not from a command argument.

```sh
REPO=lu-zhengda/updater
ENV=release

base64 < /path/to/updater-developer-id.p12 | tr -d '\n' | gh secret set MACOS_CERT_P12_BASE64 --repo "$REPO" --env "$ENV"

gh secret set MACOS_CERT_PASSWORD --repo "$REPO" --env "$ENV" < /path/to/p12-password.txt

base64 < /path/to/AuthKey_XXXXXXXXXX.p8 | tr -d '\n' | gh secret set APPSTORE_API_KEY_P8_BASE64 --repo "$REPO" --env "$ENV"

gh secret set APPSTORE_API_KEY_ID --repo "$REPO" --env "$ENV" < /path/to/key-id.txt
gh secret set APPSTORE_API_ISSUER_ID --repo "$REPO" --env "$ENV" < /path/to/issuer-id.txt

gh secret set HOMEBREW_TAP_TOKEN --repo "$REPO" --env "$ENV" < /path/to/homebrew-tap-token.txt
```

Do not pass secret values as `gh` arguments (`--body`, inline strings, or command substitution in the argument list). Those show up in the process list and in shell history.

A trailing newline in the password, key ID, or issuer file is stripped in the workflow. The base64 commands above already remove the line wraps `base64` inserts.

After the environment secrets are set, delete the same names at repository level so other workflows cannot read them:

```sh
gh secret delete MACOS_CERT_P12_BASE64 --repo "$REPO"
gh secret delete MACOS_CERT_PASSWORD --repo "$REPO"
gh secret delete APPSTORE_API_KEY_P8_BASE64 --repo "$REPO"
gh secret delete APPSTORE_API_KEY_ID --repo "$REPO"
gh secret delete APPSTORE_API_ISSUER_ID --repo "$REPO"
gh secret delete HOMEBREW_TAP_TOKEN --repo "$REPO"
```

`gh secret delete` without `--env` removes the repository secret. It does not remove the environment secret. `gh secret delete` fails if that repository secret is already absent; skip a name that was never stored at repository level.

After `gh secret set` succeeds, delete the local password file and the `.p8` from machines that do not need them. Set `HOMEBREW_TAP_TOKEN` from the token file you already keep. Do not print it, and do not rotate it while moving it onto the environment.

## Cut a release

The tagged commit must already be on `main`. The workflow runs `git fetch origin main` and `git merge-base --is-ancestor "$GITHUB_SHA" origin/main` before it imports the certificate or starts GoReleaser. A tag whose commit is not on `main` fails that step. A prerelease tag such as `v1.2.3-rc.1` is held to the same check.

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The tag push asks for approval on the `release` environment. After approval, the job creates the GitHub Release and updates the cask. Do not create the release or the tap commit by hand.

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
