#!/bin/sh
# Fail if .goreleaser.yaml would regenerate a Homebrew cask or archive that
# already shipped broken. Each check is a config that reached a tag.
# shellcheck shell=sh
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: cask-shape.sh <goreleaser.yaml>" >&2
	exit 2
fi

config=$1
if [ ! -f "$config" ]; then
	echo "missing $config" >&2
	exit 1
fi

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

archives=$(awk '
	$0 ~ /^archives:/ { grab=1 }
	grab && $0 ~ /^checksum:/ { exit }
	grab { print }
' "$config")
homebrew=$(awk '
	$0 ~ /^homebrew_casks:/ { grab=1 }
	grab { print }
' "$config")

printf '%s\n' "$archives" | grep -F 'id: updater-archive' >/dev/null ||
	fail "the cask archive id updater-archive is missing"
printf '%s\n' "$archives" | grep -F 'allow_different_binary_count' >/dev/null &&
	fail "updater-archive allows a second binary; v0.6.0 collided on the archive name" || true
printf '%s\n' "$archives" | grep -F 'updater-menubar' >/dev/null &&
	fail "updater-menubar is back in the cask archive" || true
printf '%s\n' "$archives" | grep -F 'completions/*' >/dev/null ||
	fail "updater-archive does not pack completions/*"

# The archive's ids list is only the updater build. A second id is the
# v0.6.0 pair that produced one archive name for two binaries.
ids=$(printf '%s\n' "$archives" | awk '
	$0 ~ /^  - id: updater-archive$/ { grab=1; next }
	grab && $0 ~ /^  - id:/ { exit }
	grab && $0 ~ /^    ids:$/ { inids=1; next }
	inids && $0 ~ /^      - / { print; next }
	inids { exit }
')
[ "$ids" = "      - updater" ] ||
	fail "updater-archive must contain only the updater build, found: ${ids:-nothing}"

grep -F 'CGO_ENABLED=1' "$config" >/dev/null ||
	fail "CGO_ENABLED=1 is missing; the menu bar build failed the v0.6.0 release without cgo"

printf '%s\n' "$homebrew" | grep -F 'updater-archive' >/dev/null ||
	fail "homebrew_casks does not select updater-archive"
printf '%s\n' "$homebrew" | grep -F 'system_command' >/dev/null &&
	fail "cask hook uses system_command; Homebrew 7 rejected that install hook" || true
printf '%s\n' "$homebrew" | grep -F 'com.apple.quarantine' >/dev/null &&
	fail "cask strips quarantine; that hid the unstapled CLI instead of shipping the stapled app" || true
printf '%s\n' "$homebrew" | grep -E '^[[:space:]]*- (updater|updater-menubar)$' >/dev/null &&
	fail "cask links a loose binary; Gatekeeper killed that quarantined Mach-O in 0.16.0" || true
printf '%s\n' "$homebrew" | grep -F '#{appdir}/Updater.app/Contents/MacOS/updater' >/dev/null ||
	fail "cask binary is not the executable inside Updater.app"
printf '%s\n' "$homebrew" | grep -F 'remove "Updater.app"' >/dev/null ||
	fail "cask is missing remove \"Updater.app\"; Homebrew aborted when an app was already in the app dir"

echo "cask shape ok"
