# updater

[![CI](https://github.com/lu-zhengda/updater/actions/workflows/ci.yml/badge.svg)](https://github.com/lu-zhengda/updater/actions/workflows/ci.yml)
[![Release](https://github.com/lu-zhengda/updater/actions/workflows/release.yml/badge.svg)](https://github.com/lu-zhengda/updater/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/lu-zhengda/updater?sort=semver)](https://github.com/lu-zhengda/updater/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/lu-zhengda/updater)](https://github.com/lu-zhengda/updater/blob/main/go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/lu-zhengda/updater/blob/main/LICENSE)

`updater` is a macOS app + CLI/TUI that discovers installed apps, checks for updates across multiple ecosystems, and applies the right update action per app.

Installing gives you both entry points from one binary:

- **Updater.app** (default) — a menu bar app in `/Applications` that checks periodically, shows an update count, notifies about new updates, and updates apps from the dropdown. It launches automatically after install/upgrade.
- **`updater` CLI/TUI** — the same engine on your PATH for terminal use, scripting, and agents.

## Requirements

- macOS (the one truly non-negotiable requirement)
- Homebrew (recommended)
- `mas` for Mac App Store checks (installed automatically with the Homebrew cask)

## Install (Recommended)

Install from Homebrew tap:

```sh
brew install --cask lu-zhengda/tap/updater
updater --version
```

This installs `Updater.app` to `/Applications` (and launches it), puts the
`updater` CLI on your PATH, and includes `mas` automatically as a cask
dependency. Upgrades refresh both, since they are the same binary.

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
| Sparkle | Appcast feed from app metadata | Direct DMG/ZIP/PKG install when possible, otherwise opens download URL |
| Homebrew cask | `brew outdated --cask --greedy --json` | `brew upgrade --cask <token>` |
| Homebrew formula | `brew outdated --formula --json` | `brew upgrade <formula>` |
| Mac App Store | `mas outdated` | `mas upgrade <id>` or opens App Store updates |
| GitHub Releases | GitHub Releases API | Direct install when possible, otherwise opens release asset URL |
| Electron generic | `latest-mac.yml` from update server | Direct install when possible, otherwise opens app |
| Brew-info fallback | `brew info --cask --json=v2` | If brew-installed: `brew upgrade --cask`; otherwise opens app |
| npm globals | `npm outdated -g --json` | `npm install -g <pkg>@latest` |
| uv tools | `uv tool list` + PyPI JSON | `uv tool upgrade <tool>` |
| cargo crates | `cargo install --list` + crates.io API | `cargo install <crate>` |
| macOS system | `softwareupdate -l` | Opens Software Update settings |

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
updater schedule --remove
```

## Menu Bar App (Updater.app)

`Updater.app` is the default entry point: opening it (Finder, Spotlight, or
the automatic post-install launch) runs the menu bar app. It periodically
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

Building the app bundle from source:

```sh
make app          # produces ./Updater.app
make install-app  # copies it to /Applications and launches it
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
  com.microsoft.VSCode: "microsoft/vscode"

cask_mappings:
  com.readdle.PDFExpert-Mac: "pdf-expert"

github_token: "ghp_..."
max_concurrent: 10
max_backups: 1
interactive_notifications: true
```

Notes:

- `GITHUB_TOKEN` environment variable overrides `github_token`.
- `cask_mappings` are only needed when automatic cask token detection is wrong.
- Use `updater config export` and `updater config import <file>` to move config between machines.

## Safety Model

- `--dry-run` prints the exact planned actions without making changes.
- Backups are created before install-based updates when app paths are available.
- Failed direct installs attempt automatic rollback from backup.
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
