#!/bin/sh
# Proves release-preflight.sh fails when the plist version disagrees with
# the tag, and when notarization validation fails. No Apple credentials.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/release-preflight.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

tools=$tmp/tools
mkdir -p "$tools"
cat >"$tools/codesign" <<'EOF'
#!/bin/sh
printf '%s\n' "codesign $*" >>"$PREFLIGHT_LOG"
exit "${PREFLIGHT_CODESIGN_EXIT:-0}"
EOF
cat >"$tools/spctl" <<'EOF'
#!/bin/sh
printf '%s\n' "spctl $*" >>"$PREFLIGHT_LOG"
exit 0
EOF
cat >"$tools/xcrun" <<'EOF'
#!/bin/sh
printf '%s\n' "xcrun $*" >>"$PREFLIGHT_LOG"
if [ "${1:-}" = "stapler" ] && [ "${2:-}" = "validate" ]; then
	exit "${PREFLIGHT_STAPLER_EXIT:-0}"
fi
exit 0
EOF
cat >"$tools/PlistBuddy" <<'EOF'
#!/bin/sh
# PlistBuddy -c 'Print :Key' file
key=${2#Print :}
plist=$3
awk -v key="$key" '
	index($0, "<key>" key "</key>") {
		line = $0
		sub(/.*<key>/, "", line)
		sub(/.*<string>/, "", line)
		sub(/<\/string>.*/, "", line)
		if (line != $0) {
			print line
			exit
		}
	}
' "$plist"
EOF
chmod 755 "$tools/codesign" "$tools/spctl" "$tools/xcrun" "$tools/PlistBuddy"

make_app() {
	version=$1
	app=$tmp/Updater.app
	rm -rf "$app"
	mkdir -p "$app/Contents/MacOS"
	cat >"$app/Contents/MacOS/updater" <<EOF
#!/bin/sh
echo "updater version $version"
EOF
	chmod 755 "$app/Contents/MacOS/updater"
	cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleShortVersionString</key><string>$version</string>
<key>CFBundleVersion</key><string>$version</string>
</dict></plist>
EOF
	printf 'dmg' >"$tmp/Updater.dmg"
}

export PREFLIGHT_LOG=$tmp/log
export UPDATER_PREFLIGHT_TEST=1
export UPDATER_PREFLIGHT_TOOLS=$tools

make_app 1.2.3
if sh "$script" "$tmp/Updater.app" "$tmp/Updater.dmg" 9.9.9 >"$tmp/mismatch.out" 2>"$tmp/mismatch.err"; then
	fail "plist/tag mismatch was accepted"
fi
grep -F 'tag=9.9.9' "$tmp/mismatch.err" >/dev/null
echo "ok: version mismatch fails"

: >"$PREFLIGHT_LOG"
unset NOTARIZE || true
make_app 1.2.3
sh "$script" "$tmp/Updater.app" "$tmp/Updater.dmg" v1.2.3
if grep -F 'stapler validate' "$PREFLIGHT_LOG" >/dev/null; then
	fail "stapler ran without NOTARIZE=1"
fi
echo "ok: unsigned local build skips staple and spctl"

: >"$PREFLIGHT_LOG"
export NOTARIZE=1
export PREFLIGHT_STAPLER_EXIT=1
if sh "$script" "$tmp/Updater.app" "$tmp/Updater.dmg" 1.2.3 >"$tmp/staple.out" 2>"$tmp/staple.err"; then
	fail "rejected staple was accepted"
fi
echo "ok: stapler validate failure stops the preflight"

: >"$PREFLIGHT_LOG"
export PREFLIGHT_STAPLER_EXIT=0
sh "$script" "$tmp/Updater.app" "$tmp/Updater.dmg" 1.2.3
grep -F 'codesign --verify --deep --strict --verbose=2' "$PREFLIGHT_LOG" >/dev/null ||
	fail "codesign --verify --deep --strict was not run"
grep -F 'spctl -a -vvv -t exec' "$PREFLIGHT_LOG" >/dev/null ||
	fail "spctl -a -vvv -t exec was not run"
grep -F 'xcrun stapler validate' "$PREFLIGHT_LOG" >/dev/null ||
	fail "stapler validate was not run"
count=$(grep -c -F 'xcrun stapler validate' "$PREFLIGHT_LOG" || true)
[ "$count" -eq 2 ] || fail "expected staple checks for the app and the DMG, got $count"
echo "ok: notarized preflight checks the app and the DMG"

unset NOTARIZE PREFLIGHT_STAPLER_EXIT
echo "release preflight tests ok"
