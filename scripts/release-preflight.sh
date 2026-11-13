#!/bin/sh
# Last check before GoReleaser archives, checksums, and publishes.
#
# The version argument is the tag GoReleaser is releasing. Info.plist and
# `updater --version` must use that same string. When NOTARIZE=1, the app
# and the DMG must already be signed, notarized, and stapled. This runs
# inside the universal-binary post hook, so a failure stops the release
# before the GitHub Release and the Homebrew tap update.
#
# UPDATER_PREFLIGHT_TEST=1 reads codesign, spctl, and xcrun from
# UPDATER_PREFLIGHT_TOOLS. The release job does not set it.
# shellcheck shell=sh
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: release-preflight.sh <Updater.app> <Updater.dmg> <version>" >&2
	exit 2
fi

APP=$1
DMG=$2
VERSION=${3#v}

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

resolve_command() {
	_resolve_name=$1
	_resolve_rest=${PATH:-}
	while :; do
		case $_resolve_rest in
		*:*)
			_resolve_dir=${_resolve_rest%%:*}
			_resolve_rest=${_resolve_rest#*:}
			;;
		*)
			_resolve_dir=$_resolve_rest
			_resolve_rest=
			;;
		esac
		if [ -z "$_resolve_dir" ]; then
			_resolve_dir=.
		fi
		if [ -f "$_resolve_dir/$_resolve_name" ] && [ -x "$_resolve_dir/$_resolve_name" ]; then
			printf '%s\n' "$_resolve_dir/$_resolve_name"
			return 0
		fi
		if [ -z "$_resolve_rest" ]; then
			break
		fi
	done
	return 1
}

if [ "${UPDATER_PREFLIGHT_TEST:-}" = "1" ]; then
	: "${UPDATER_PREFLIGHT_TOOLS:?UPDATER_PREFLIGHT_TOOLS is required when UPDATER_PREFLIGHT_TEST=1}"
	CODESIGN=$UPDATER_PREFLIGHT_TOOLS/codesign
	XCRUN=$UPDATER_PREFLIGHT_TOOLS/xcrun
	SPCTL=$UPDATER_PREFLIGHT_TOOLS/spctl
	PLISTBUDDY=$UPDATER_PREFLIGHT_TOOLS/PlistBuddy
else
	CODESIGN=/usr/bin/codesign
	PLISTBUDDY=/usr/libexec/PlistBuddy
	XCRUN=$(resolve_command xcrun || true)
	SPCTL=$(resolve_command spctl || true)
fi

for tool in "$CODESIGN" "$PLISTBUDDY"; do
	if [ ! -x "$tool" ]; then
		fail "required tool is not executable: $tool"
	fi
done

if [ ! -d "$APP" ] || [ ! -x "$APP/Contents/MacOS/updater" ]; then
	fail "not an Updater.app bundle: $APP"
fi
if [ ! -f "$DMG" ]; then
	fail "DMG was not built: $DMG"
fi

short=$("$PLISTBUDDY" -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")
bundle=$("$PLISTBUDDY" -c 'Print :CFBundleVersion' "$APP/Contents/Info.plist")
if [ "$short" != "$VERSION" ] || [ "$bundle" != "$VERSION" ]; then
	fail "plist version short=$short bundle=$bundle tag=$VERSION"
fi
reported=$("$APP/Contents/MacOS/updater" --version 2>&1 || true)
case $reported in
*"$VERSION"*) ;;
*)
	fail "updater --version [$reported] does not contain $VERSION"
	;;
esac

"$CODESIGN" --verify --deep --strict --verbose=2 "$APP"

if [ "${NOTARIZE:-}" != "1" ]; then
	echo "release preflight ok (notarization checks skipped)"
	exit 0
fi

if [ ! -x "$XCRUN" ] || [ ! -x "$SPCTL" ]; then
	fail "xcrun and spctl are required when NOTARIZE=1"
fi

"$SPCTL" -a -vvv -t exec "$APP"
"$XCRUN" stapler validate "$APP"
"$CODESIGN" --verify --verbose=2 "$DMG"
"$XCRUN" stapler validate "$DMG"
"$SPCTL" --assess --type open --context context:primary-signature -vv "$DMG"
echo "release preflight ok"
