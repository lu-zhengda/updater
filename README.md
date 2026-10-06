# updater

[![CI](https://github.com/lu-zhengda/updater/actions/workflows/ci.yml/badge.svg)](https://github.com/lu-zhengda/updater/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/lu-zhengda/updater?sort=semver)](https://github.com/lu-zhengda/updater/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lu-zhengda/updater)](https://github.com/lu-zhengda/updater/blob/main/go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/lu-zhengda/updater/blob/main/LICENSE)

`updater` is a macOS app + CLI/TUI that discovers installed apps, checks for updates across multiple ecosystems, and applies the right update action per app.

Installing gives you both entry points from one binary:

- **Updater.app** (default) — a menu bar app in `/Applications` that checks periodically, shows an update count, notifies about new updates, and updates apps from the dropdown. Open it from Applications after install/upgrade.
- **`updater` CLI/TUI** — the same engine on your PATH for terminal use, scripting, and agents.

## Requirements

- macOS (the one truly non-negotiable requirement)
- Homebrew (recommended)
- `mas` for Mac App Store checks (installed automatically with the Homebrew cask)

### macOS 27 verification

Verified locally on macOS 27.0 (build 26A428), Apple silicon, with the macOS
27.0 SDK: race tests, `go vet`, app bundle build/signature verification, CLI
discovery, and the native updates window completing a check. Sparkle feeds
respect macOS version limits even when no release supports the current OS.
Installing updates, notifications, and login startup still require separate
end-to-end verification on macOS 27.

## Install (Recommended)

Install from Homebrew tap:

```sh
brew install --cask lu-zhengda/tap/updater
updater --version
```

This installs `Updater.app` to `/Applications`, puts the
`updater` CLI on your PATH, and includes `mas` automatically as a cask
dependency. Upgrades refresh both, since they are the same binary. Open
`Updater.app` from Applications after installing.

Upgrade later:

```sh
brew upgrade --cask lu-zhengda/tap/updater
```

## Quick Start

```sh
# 1) Validate environment and dependencies
updater doctor

# 2) Discover installed apps and detected sources
updater scan

# 3) Check available updates
updater check

# 4) Preview update actions without changing anything
updater update --all --dry-run

# 5) Apply updates
updater update --all
```

Launch the interactive TUI:

```sh
updater
```

## What It Supports

| Source | How updates are checked | Update behavior |
| --- | --- | --- |
| Sparkle | HTTPS appcast from Info.plist or embedded Electron package metadata (including Codex) | Installs notarized updates matching the installed app's bundle and Developer Team identity |
| Homebrew cask | `brew outdated --cask --greedy --json` | `brew upgrade --cask <token>` |
| Homebrew formula | `brew outdated --formula --json` | `brew upgrade <formula>` |
| Mac App Store | `mas outdated` | `mas upgrade <id>` or opens App Store updates |
| GitHub Releases | GitHub Releases API | Verifies release digest and Apple identity before direct install |
| Electron generic | HTTPS macOS update feed (including Notion’s ARM channel) | Verifies SHA-512 and Apple identity before direct install |
| Native Electron services | VS Code-compatible update APIs and Squirrel.Mac release indexes (including Claude desktop) | Downloads the native artifact, verifies any supplied checksum and Apple identity, and installs directly |
| Brew-info fallback | `brew info --cask --json=v2` | If brew-installed: `brew upgrade --cask`; otherwise verifies and installs the cask artifact directly |
| npm globals | `npm outdated -g --json` | `npm install -g <pkg>@latest` |
| pnpm globals | `pnpm outdated -g --format json` | `pnpm update -g --latest <pkg>` |
| pipx applications | `pipx list --json` + PyPI JSON | `pipx upgrade <environment>` |
| uv tools | `uv tool list` + PyPI JSON | `uv tool upgrade <tool>` |
| cargo crates | `cargo install --list` + crates.io API | `cargo install <crate>` |
| macOS system | `softwareupdate -l` | Opens Software Update settings |

Direct downloads prefer the Mac’s native architecture, then universal builds. Explicitly incompatible builds are excluded. Before replacing an app from a DMG or ZIP, the updater also checks that its executable supports the native architecture, even when filenames omit it.

Electron discovery reads structured metadata from `Resources/app` and `app.asar`,
including archives with unpacked metadata files and apps with renamed frameworks.
Embedded `sparkleFeedUrl` fields (including app-prefixed names such as
`codexSparkleFeedUrl`) retain the packaged release channel; conflicting feed URLs
are not guessed. VS Code-compatible `product.json` update services retain their
`stable` or `insider` channel. Claude's release-index address is supplied by a
bundle-ID adapter; the release-index parser is shared. These feeds are checked
before Homebrew and legacy GitHub mappings, so a version-only release cannot hide
the vendor's installer. Explicit `source_overrides` still take precedence.

Failed direct installs open the app or download page for manual updating and
show the original failure alongside the handoff. The handoff is not recorded as
a completed installation. Cancelled installs and failed replacements that could
not be rolled back do not launch an external updater. Sources that only support
an external updater still offer that manual action. Apps with
authenticated or undocumented update protocols require a compatible adapter;
Updater does not infer endpoints from arbitrary executable code.

On Apple Silicon, Intel-only apps are offered an **Install ARM version** action when an update source advertises a native or universal build at the same or a newer version. These suggestions are excluded from unattended auto-updates; pins and ignore settings still apply. Unlabeled downloads do not trigger a suggestion.

Also detected (for visibility): Setapp, JetBrains Toolbox, and Adobe apps.

## Command Guide

Core update workflow:

```sh
updater scan
updater check
updater check --share
updater update "1Password"
updater update --bundle-id com.1password.1password
updater update --all
updater update --all --auto
updater update --all --dry-run
```

Scripting/JSON:

```sh
updater scan --json
updater check --json
updater outdated --json
updater history --json
updater doctor --json
updater update --all --dry-run --json
```

Agent mode:

- All commands support `--json`.
- When `updater` detects it is running inside Codex or Claude Code, JSON output is enabled automatically.
- Override behavior with `UPDATER_AGENT_MODE=0` (force off) or `UPDATER_AGENT_MODE=1` (force on).
- `updater update --json` is supported for dry-run planning (`--dry-run`).

Management:

```sh
updater pin "Google Chrome"
updater unpin "Google Chrome"
updater policy "Google Chrome" manual
updater install firefox
updater rollback "Firefox"
updater cleanup --days 90
updater cleanup --days 90 --delete
```

Automation:

```sh
updater schedule --interval 24
updater schedule --interval 24 --auto-update  # explicit opt-in
updater schedule --remove
```

## Menu Bar App (Updater.app)

`Updater.app` is the default entry point: opening it (Finder, Spotlight, or
a manual launch after installation) runs the menu bar app. It periodically
runs the same discovery/check pipeline as the CLI, shows an update count in
the menu bar, posts a macOS notification when new updates appear, and lets
you update a single app or everything from the dropdown (updates go through
the regular update path, so backups/history behave identically).

"Open Updater…" in the dropdown opens the app's built-in updates window — a
native window with the full app list (like the TUI, but part of the app):
cached results appear instantly, a fresh check streams in with progress, and
each row can be updated, pinned, or ignored. Everything runs in-process; the
window never shells out to the terminal.

The dropdown has a "Start at Login" toggle; the same LaunchAgent can be
managed from the CLI:

```sh
updater menubar           # keep it running now and at login
updater menubar --remove  # stop and remove the login item
updater menubar run       # run the menu bar app in the foreground (debugging)
```

The check interval follows `schedule_interval` from the config (same setting
used by `updater schedule`).

Updater appears in the same update list as other apps. Use its **Update** action,
**Update All**, or **YOLO Mode** to install a new version. Internally, a helper waits
for active update batches to finish, updates the complete app, and restarts it.
`updater upgrade` also supports app-bundle installations, including the Homebrew
CLI link. Homebrew cask installations are upgraded through Homebrew;
direct installations verify the release checksum,
signature, notarization, and app identity before replacement. A failed replacement
restores the previous app. Results appear in update history and notifications;
helper details are logged to `~/.config/updater/logs/self-update.log`.

Enable **Preferences › YOLO Mode** to automatically install available updates
after menu bar checks, including major versions. It is off by default and also
applies to scheduled checks. Pins, ignored apps, manual/notify-only policies,
download verification, and the exclusion of ARM migration suggestions still
apply. Apps requiring an external updater remain available for manual action.
Successful installs appear in history; failed installs remain available to retry.

Building the app bundle from source:

```sh
make app          # produces ./Updater.app
make install-app  # copies it to /Applications and launches it
```

Local app bundles are ad-hoc signed as a complete bundle. To preserve macOS
privacy and filesystem grants across rebuilt versions, sign with an
Apple-issued identity:

```sh
MACOS_SIGNING_IDENTITY="Apple Development: Your Name (TEAMID)" make app
```

## Configuration

Config file path:

```text
~/.config/updater/config.yaml
```

Example:

```yaml
ignored_apps:
  - com.apple.Safari

pinned_apps:
  - com.google.Chrome

# auto | manual | notify-only
policies:
  com.microsoft.VSCode: auto
  com.google.Chrome: manual

github_mappings:
  com.example.MyApp: "example/my-app"

cask_mappings:
  com.readdle.PDFExpert-Mac: "pdf-expert"

max_concurrent: 10
max_backups: 0
yolo_mode: false
interactive_notifications: true
```

Notes:

- Set `GITHUB_TOKEN` in the environment when authenticated GitHub API access is needed. A legacy `github_token` config value is still accepted, but the config is stored with owner-only permissions.
- `cask_mappings` are only needed when automatic cask token detection is wrong.
- Backups are disabled by default because app bundles can be large. Enable **Preferences › Back Up Before Updates** in the menu bar app, or set `max_backups` to a positive per-app retention count. Set it to `0` to disable backups.
- Use `updater config export` and `updater config import <file>` to move config between machines.

## Safety Model

- `--dry-run` prints the exact planned actions without making changes.
- When backups are enabled, they are created before install-based updates when app paths are available.
- Failed direct installs attempt automatic rollback when a backup is available.
- Downloaded apps must be valid, notarized, and match the installed bundle ID and Developer Team ID. Installer packages must be notarized and signed by that same team.
- Scheduled checks notify only by default; unattended installation requires `schedule --auto-update` (non-major updates) or YOLO Mode (including major updates).
- Pinned apps are skipped in `update --all`.
- `policy` lets you force per-app behavior (`auto`, `manual`, `notify-only`).

## Build From Source

Requires Go `1.25.7+`.

```sh
git clone https://github.com/lu-zhengda/updater.git
cd updater
make install PREFIX=~/.local
```

If needed, add to `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

If building from source and you want Mac App Store checks, install `mas`:

```sh
brew install mas
```

## Developer Commands

```sh
make build
make test
make clean
```

## License

MIT
