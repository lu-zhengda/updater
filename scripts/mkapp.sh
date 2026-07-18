#!/bin/sh
# Assemble Updater.app from a built updater binary.
#
# Usage: scripts/mkapp.sh <updater-binary> <version> <output-dir>
# Produces <output-dir>/Updater.app with the binary, Info.plist, and icon.
set -eu

BIN="$1"
VERSION="$2"
OUT="$3"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

APP="$OUT/Updater.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$BIN" "$APP/Contents/MacOS/updater"
chmod 755 "$APP/Contents/MacOS/updater"
cp "$ROOT/packaging/Updater.icns" "$APP/Contents/Resources/Updater.icns"
sed "s/__VERSION__/${VERSION#v}/g" "$ROOT/packaging/Info.plist" > "$APP/Contents/Info.plist"

plutil -lint "$APP/Contents/Info.plist" >/dev/null
echo "built $APP (version ${VERSION#v})"
