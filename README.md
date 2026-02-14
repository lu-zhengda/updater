# updater

A command-line tool for macOS that discovers installed applications, checks for available updates across multiple sources, and updates them — all from your terminal.

## What It Does

`updater` scans `/Applications` and `~/Applications`, identifies where each app gets its updates from, and checks for newer versions. It supports five update sources:

| Source | How It Works |
|--------|-------------|
| **Sparkle** | Reads the app's built-in appcast feed (used by iTerm2, Sublime Text, etc.) |
| **Homebrew Cask** | Checks `brew outdated` for apps installed via `brew install --cask` |
| **Mac App Store** | Runs `mas outdated` for App Store apps |
| **GitHub Releases** | Queries the GitHub API for the latest release |
| **Brew Info** | Falls back to `brew info --cask` for any app with a matching Homebrew cask, even if not installed via brew |

When a Sparkle feed is stale (returns an older version than what's installed), `updater` automatically falls through to the next available source.

For updates, it picks the right strategy per app:
- **Brew-installed apps** — runs `brew upgrade --cask` (quits the app first if running, reopens after)
- **Self-updating apps** (Chrome, 1Password, Notion, etc.) — opens the app so its built-in updater can run
- **Sparkle/GitHub apps** — opens the download URL in your browser
- **App Store apps** — runs `mas upgrade` or opens the App Store updates page

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

**Scan** your installed apps:

```
$ updater scan
NAME                  VERSION         SOURCE   BUNDLE ID
1Password             8.12.0          unknown  com.1password.1password
Alfred                5.7.2           unknown  com.runningwithcrayons.Alfred
Claude                1.1.2512        unknown  com.anthropic.claudefordesktop
Google Chrome         144.0.7559.133  unknown  com.google.Chrome
iTerm2                3.6.6           sparkle  com.googlecode.iterm2
Notion                7.2.1           unknown  notion.id
PDF Expert            3.11.1          sparkle  com.readdle.PDFExpert-Mac
Xcode                 26.2            mas      com.apple.dt.Xcode
...
```

**Check** for updates:

```
$ updater check
NAME                  CURRENT         LATEST         SOURCE     STATUS
1Password             8.12.0          8.12.2         brew-info  UPDATE AVAILABLE
Claude                1.1.2512        1.1.3189       brew-info  UPDATE AVAILABLE
Google Chrome         144.0.7559.133  145.0.7632.76  brew-info  UPDATE AVAILABLE
iTerm2                3.6.6           3.6.6          sparkle    ok
PDF Expert            3.11.1          3.11.1         brew-info  ok
Xcode                 26.2            26.2           mas        ok
...
```

**Update** a specific app:

```
$ updater update 1Password
Updating 1Password (8.12.0 -> 8.12.2) via brew-info...
  Opening 1Password for in-app update...
```

**Update all** apps with available updates:

```
$ updater update --all
```

**Interactive TUI** for browsing and updating:

```
$ updater ui
```

The TUI shows all checkable apps in a scrollable list. Use `j`/`k` to navigate, `Enter` to update the selected app, `a` to update all, `i` to ignore, `r` to refresh, `q` to quit.

## Configuration

Config file: `~/.config/updater/config.yaml`

```yaml
# Apps to skip (by bundle ID)
ignored_apps:
  - com.apple.Safari

# Map bundle IDs to GitHub repos for GitHub Releases checking
github_mappings:
  com.microsoft.VSCode: "microsoft/vscode"
  com.github.GitHubClient: "desktop/desktop"

# Map bundle IDs to Homebrew cask tokens (when the automatic name guess is wrong)
cask_mappings:
  com.readdle.PDFExpert-Mac: "pdf-expert"
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
    [Checking]   Try checkers in order: Sparkle > Brew > MAS > GitHub > Brew Info
        |         (fall through on stale feeds or errors)
        |
    [Update]     brew upgrade | open app | open download URL | mas upgrade
```

## Building

```sh
make build    # Build ./updater
make test     # Run tests with race detection
make clean    # Remove binary
```

## License

MIT
