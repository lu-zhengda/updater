# updater

A command-line tool for macOS that discovers installed applications, checks for available updates across multiple sources, and updates them — all from your terminal.

## What It Does

`updater` scans `/Applications` and `~/Applications`, identifies where each app gets its updates from, and checks for newer versions. It supports six update sources:

| Source | How It Works |
|--------|-------------|
| **Sparkle** | Reads the app's built-in appcast feed (used by iTerm2, Sublime Text, etc.) |
| **Homebrew Cask** | Checks `brew outdated` for apps installed via `brew install --cask` |
| **Mac App Store** | Runs `mas outdated` for App Store apps |
| **GitHub Releases** | Queries the GitHub API for the latest release |
| **Brew Info** | Falls back to `brew info --cask` for any app with a matching Homebrew cask, even if not installed via brew |
| **System** | Checks `softwareupdate -l` for macOS system updates |

When a Sparkle feed is stale (returns an older version than what's installed), `updater` automatically falls through to the next available source.

For updates, it picks the right strategy per app:
- **Brew-installed apps** — runs `brew upgrade --cask` (quits the app first if running, reopens after)
- **Sparkle/GitHub apps** — downloads and installs DMG/ZIP/PKG directly, or opens the download URL as fallback
- **Self-updating apps** (Chrome, 1Password, Notion, etc.) — opens the app so its built-in updater can run
- **App Store apps** — runs `mas upgrade` or opens the App Store updates page
- **macOS system updates** — opens System Settings > Software Update

## Install

### From source (requires Go 1.21+)

```sh
git clone https://github.com/lu-zhengda/updater.git
cd updater
make install PREFIX=~/.local
```

This builds a release binary and installs it to `~/.local/bin/updater`. Make sure `~/.local/bin` is in your `PATH`:

```sh
# Add to your ~/.zshrc or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
```

To install system-wide instead:

```sh
sudo make install
```

This installs to `/usr/local/bin/updater`.

### Dependencies

`updater` works best with these tools installed (all optional):

```sh
brew install mas   # for Mac App Store update checking
```

Homebrew itself is needed for `brew` and `brew-info` sources. If brew or mas aren't installed, those sources are skipped gracefully.

## Quick Start

Running `updater` with no arguments launches the interactive TUI:

```
$ updater
```

The TUI shows all checkable apps in a scrollable list with real-time update checking.

| Key | Action |
|-----|--------|
| `j`/`k` or arrows | Navigate (wraps around) |
| `Enter` | Update selected app |
| `a` | Update all |
| `d` | Show release notes |
| `p` | Pin/unpin (skip during update all) |
| `i` | Ignore/unignore |
| `r` | Refresh |
| `q` | Quit |

### CLI Commands

**Scan** your installed apps:

```
$ updater scan
NAME                  VERSION         SOURCE   BUNDLE ID
1Password             8.12.0          unknown  com.1password.1password
iTerm2                3.6.6           sparkle  com.googlecode.iterm2
Xcode                 26.2            mas      com.apple.dt.Xcode
macOS                 26.2            system   com.apple.macOS
...
```

**Check** for updates:

```
$ updater check
NAME                  CURRENT         LATEST         SOURCE     STATUS
1Password             8.12.0          8.12.2         homebrew   UPDATE AVAILABLE
iTerm2                3.6.6           3.6.6          sparkle    ok
macOS                 26.2            26.3           system     UPDATE AVAILABLE
Xcode                 26.2            26.2           app store  ok
...
```

**Update** a specific app:

```
$ updater update 1Password
Updating 1Password (8.12.0 -> 8.12.2) via brew-info...
```

**Update all** apps with available updates:

```
$ updater update --all
```

**JSON output** for scripting:

```
$ updater outdated
[{"name":"1Password","bundle_id":"com.1password.1password","current_version":"8.12.0","latest_version":"8.12.2","source":"brew-info","download_url":"..."}]
```

**Pin** an app to skip it during `update --all`:

```
$ updater pin 1Password
$ updater unpin 1Password
```

**Install** a new app via Homebrew:

```
$ updater install firefox
```

**Find unused apps:**

```
$ updater cleanup --days 90
$ updater cleanup --days 90 --delete   # move to Trash
```

**Rollback** to a previous version:

```
$ updater rollback 1Password
```

**Schedule** automatic update checks with macOS notifications:

```
$ updater schedule --interval 24   # check every 24 hours
$ updater schedule --remove        # remove scheduled check
```

## Configuration

Config file: `~/.config/updater/config.yaml`

```yaml
# Apps to skip entirely (by bundle ID)
ignored_apps:
  - com.apple.Safari

# Apps to show but skip during "update --all"
pinned_apps:
  - com.google.Chrome

# Map bundle IDs to GitHub repos for GitHub Releases checking
github_mappings:
  com.microsoft.VSCode: "microsoft/vscode"
  com.github.GitHubClient: "desktop/desktop"

# Map bundle IDs to Homebrew cask tokens (when the automatic name guess is wrong)
cask_mappings:
  com.readdle.PDFExpert-Mac: "pdf-expert"

# GitHub API token for higher rate limits (also reads GITHUB_TOKEN env var)
github_token: "ghp_..."

# Max concurrent update checks (default: 10)
max_concurrent: 10

# Number of app backups to keep per app (default: 1)
max_backups: 1
```

### When do you need `cask_mappings`?

Most of the time, you don't. `updater` guesses the cask name from the app's display name (e.g., "Google Chrome" becomes `google-chrome`) and verifies it exists. Add an explicit mapping only when the guess is wrong — for example, if an app is named "Docker" but its cask is `docker-desktop`.

## How It Works

```
/Applications/*.app
        |
    [Discovery]  Parse Info.plist for name, version, bundle ID, Sparkle feed URL
        |
    [Enrichment] Cross-reference with config mappings, brew casks, brew info
        |
    [Checking]   Try checkers in order: Sparkle > Brew > MAS > GitHub > System > Brew Info
        |         (fall through on stale feeds or errors)
        |
    [Update]     brew upgrade | direct install | open app | open download URL | mas upgrade
        |
    [Backup]     Back up current version before updating (configurable retention)
```

## Building

```sh
make build    # Build ./updater
make test     # Run tests with race detection
make clean    # Remove binary
```

## License

MIT
