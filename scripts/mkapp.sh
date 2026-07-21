#!/bin/sh
# Assemble Updater.app from a built updater binary.
#
# Usage: scripts/mkapp.sh <updater-binary> <version> <output-dir>
# Produces <output-dir>/Updater.app with the binary, Info.plist, and icon.
#
# Set MACOS_SIGNING_IDENTITY to an Apple-issued signing identity for a stable
# designated requirement across builds. Local builds fall back to ad-hoc
# bundle signing so the executable, Info.plist, and resources still form one
# coherent code object.
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

BUNDLE_ID="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$APP/Contents/Info.plist")"
SIGNING_IDENTITY="${MACOS_SIGNING_IDENTITY:--}"

if [ "$SIGNING_IDENTITY" = "-" ]; then
	/usr/bin/codesign \
		--force \
		--sign - \
		--identifier "$BUNDLE_ID" \
		"$APP"
	SIGNING_DESCRIPTION="ad-hoc"
else
	/usr/bin/codesign \
		--force \
		--sign "$SIGNING_IDENTITY" \
		--identifier "$BUNDLE_ID" \
		--options runtime \
		--timestamp \
		"$APP"
	SIGNING_DESCRIPTION="$SIGNING_IDENTITY"
fi

/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP"
echo "built $APP (version ${VERSION#v}, signed: $SIGNING_DESCRIPTION)"
