#!/bin/sh
# Proves cask-shape.sh passes the current config and fails on the configs
# that shipped. shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
checker=$root/scripts/cask-shape.sh
config=$root/.goreleaser.yaml
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

sh "$checker" "$config"

expect_fail() {
	name=$1
	needle=$2
	if sh "$checker" "$tmp/$name.yaml" >"$tmp/$name.out" 2>"$tmp/$name.err"; then
		fail "$name was accepted"
	fi
	if ! grep -F "$needle" "$tmp/$name.err" >/dev/null; then
		cat "$tmp/$name.err" >&2
		fail "$name did not report: $needle"
	fi
	echo "ok: $name"
}

python3 - "$tmp" "$config" <<'PY'
import pathlib, sys
tmp, src = sys.argv[1:]
text = pathlib.Path(src).read_text()

def write(name, body):
    pathlib.Path(tmp, name).write_text(body)

write("loose.yaml", text.replace(
    '- "#{appdir}/Updater.app/Contents/MacOS/updater"',
    "- updater",
    1,
))
write("system-command.yaml", text.replace(
    'remove "Updater.app", base: :appdir, recursive: true',
    'system_command "/bin/rm", args: ["-rf", "#{appdir}/Updater.app"], sudo: false',
    1,
))
write("no-remove.yaml", text.replace(
    '            remove "Updater.app", base: :appdir, recursive: true\n',
    "",
    1,
))
write("quarantine.yaml", text.replace(
    'homebrew_casks:',
    'homebrew_casks:\n    # com.apple.quarantine\n',
    1,
))
write("no-cgo.yaml", text.replace("- CGO_ENABLED=1\n", "", 1))
write("collision.yaml", text.replace(
    "    ids:\n      - updater\n",
    "    ids:\n      - updater\n      - updater-menubar\n    allow_different_binary_count: true\n",
    1,
))
write("no-completions.yaml", text.replace("      - completions/*\n", "", 1))
PY

expect_fail loose "loose binary"
expect_fail system-command "system_command"
expect_fail no-remove "remove"
expect_fail quarantine "quarantine"
expect_fail no-cgo "CGO_ENABLED=1"
expect_fail collision "collided"
expect_fail no-completions "completions"

echo "cask shape tests ok"
