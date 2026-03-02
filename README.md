# updater

`updater` is a macOS CLI/TUI that discovers installed apps, checks for updates across multiple ecosystems, and applies the right update action per app.

## Requirements

- macOS
- Homebrew (recommended)
- `mas` for Mac App Store checks (installed automatically with the Homebrew cask)

## Install (Recommended)

Install from Homebrew tap:

```sh
brew install --cask lu-zhengda/tap/updater
updater --version
```

This is the easiest path and includes `mas` automatically as a cask dependency.

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
| macOS system | `softwareupdate -l` | Opens Software Update settings |

Also detected (for visibility): Setapp, JetBrains Toolbox, and Adobe apps.

## Command Guide

Core update workflow:

```sh
updater scan
updater check
updater update "1Password"
updater update --all
updater update --all --auto
updater update --all --dry-run
```

Scripting/JSON:

```sh
updater scan --json
updater check --json
updater outdated
updater history --json
updater doctor --json
```

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
