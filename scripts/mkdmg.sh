#!/bin/sh
# Build a Finder disk image from an already-signed Updater.app.
#
# Usage: scripts/mkdmg.sh <Updater.app> <version> <output-dir>
# Produces <output-dir>/Updater-<version>.dmg
#
# GoReleaser calls this from the universal-binary post hook after
# notarize-app.sh. The open-source goreleaser binary has no DMG pipe;
# its documented dmg section is Pro-only and would not package this
# already stapled bundle. The image is a release asset next to the
# existing tarball. The Homebrew cask keeps using the tarball.
#
# The app is copied with ditto so its signature and staple stay intact.
# The volume is named Updater and contains Updater.app plus a symlink
# to /Applications.
#
# MACOS_SIGNING_IDENTITY signs the disk image with a secure timestamp.
# A disk image is not an executable, so this signature does not use the
# hardened runtime or the app entitlements. MACOS_KEYCHAIN_PATH selects
# the temporary keychain from the release workflow.
#
# NOTARIZE=1 submits the DMG itself to notarytool, staples the ticket
# onto the image, and checks Gatekeeper. The App Store Connect variables
# are the same ones notarize-app.sh uses. Notarization is skipped unless
# NOTARIZE=1, so local builds and goreleaser check do not need Apple
# credentials.
#
# xcrun and spctl are resolved from PATH, the same way notarize-app.sh
# runs those commands. The release image does not provide executable
# copies at /usr/bin/xcrun and /usr/bin/spctl (spctl is /usr/sbin/spctl,
# and xcrun follows the active developer directory). Requiring those
# absolute paths aborted the v0.24.1 DMG hook after notarization had
# already succeeded.
#
# UPDATER_DMG_TEST=1 is a test seam. The release job does not set it.
#
# hdiutil create/attach/detach can fail with "Resource busy" on shared
# GitHub macOS runners (Spotlight, diskarbitrationd, a prior image still
# attached). Those three operations retry up to 5 times with increasing
# sleep; the last detach uses -force. UPDATER_DMG_HDIUTIL_SLEEP and
# UPDATER_DMG_HDIUTIL_ATTEMPTS are test seams (defaults: 1s base / 5).
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: mkdmg.sh <Updater.app> <version> <output-dir>" >&2
	exit 1
fi

APP=$1
VERSION=$2
OUT=$3

abs_path() {
	case $1 in
	/*)
		printf '%s\n' "$1"
		;;
	*)
		printf '%s\n' "$(pwd)/$1"
		;;
	esac
}

APP=$(abs_path "$APP")
OUT=$(abs_path "$OUT")
VERSION=${VERSION#v}

case $VERSION in
"" | *[!A-Za-z0-9._+-]*)
	echo "refusing version" >&2
	exit 1
	;;
esac

# First executable PATH entry for $1, skipping non-executable files.
# A bare `xcrun` in notarize-app.sh does the same search: a missing or
# non-executable /usr/bin copy does not hide a later match.
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

if [ "${UPDATER_DMG_TEST:-}" = "1" ]; then
	: "${UPDATER_DMG_TOOLS:?UPDATER_DMG_TOOLS is required when UPDATER_DMG_TEST=1}"
	HDIUTIL=$UPDATER_DMG_TOOLS/hdiutil
	DITTO=$UPDATER_DMG_TOOLS/ditto
	CODESIGN=$UPDATER_DMG_TOOLS/codesign
	PLISTBUDDY=$UPDATER_DMG_TOOLS/PlistBuddy
	staple_sleep=${UPDATER_DMG_STAPLE_SLEEP:-15}
	hdiutil_busy_sleep=${UPDATER_DMG_HDIUTIL_SLEEP:-0}
	# Search the tools directory first so an executable mock wins over a
	# system xcrun. A missing or non-executable file there still falls
	# through to PATH.
	_tool_path=$UPDATER_DMG_TOOLS:${PATH:-}
else
	HDIUTIL=/usr/bin/hdiutil
	DITTO=/usr/bin/ditto
	CODESIGN=/usr/bin/codesign
	PLISTBUDDY=/usr/libexec/PlistBuddy
	staple_sleep=15
	# Increasing wait between transient Resource busy retries on the
	# GitHub-hosted runner (Spotlight / diskarbitrationd / prior attach).
	hdiutil_busy_sleep=${UPDATER_DMG_HDIUTIL_SLEEP:-1}
	_tool_path=${PATH:-}
fi
# Bounded retries for hdiutil create/attach/detach Resource busy only.
hdiutil_max_attempts=${UPDATER_DMG_HDIUTIL_ATTEMPTS:-5}

XCRUN=$(PATH="$_tool_path" resolve_command xcrun || true)
SPCTL=$(PATH="$_tool_path" resolve_command spctl || true)

for tool in "$HDIUTIL" "$DITTO" "$CODESIGN" "$PLISTBUDDY"; do
	if [ ! -x "$tool" ]; then
		echo "required tool is not executable: $tool" >&2
		exit 1
	fi
done

SIGNING_IDENTITY=${MACOS_SIGNING_IDENTITY:-}
if [ "$SIGNING_IDENTITY" = "-" ]; then
	SIGNING_IDENTITY=
fi

DMG="$OUT/Updater-$VERSION.dmg"
STAGE=
MNT=
WORKDIR=

# True when hdiutil output names a transient volume lock.
hdiutil_output_is_busy() {
	grep -Fiq 'Resource busy' "$1"
}

# Run hdiutil with bounded retries on Resource busy only. Other failures
# abort immediately for create/attach. For detach, earlier attempts retry
# only on busy; the final attempt always uses -force (same as the old
# detach || detach -force cleanup path).
hdiutil_retry() {
	_hdi_op=$1
	shift
	_hdi_attempt=1
	_hdi_out=
	while [ "$_hdi_attempt" -le "$hdiutil_max_attempts" ]; do
		_hdi_out=$(mktemp)
		_hdi_status=0
		if [ "$_hdi_op" = "detach" ] && [ "$_hdi_attempt" -eq "$hdiutil_max_attempts" ]; then
			set +e
			"$HDIUTIL" detach -force "$@" >"$_hdi_out" 2>&1
			_hdi_status=$?
			set -e
		else
			set +e
			"$HDIUTIL" "$_hdi_op" "$@" >"$_hdi_out" 2>&1
			_hdi_status=$?
			set -e
		fi
		if [ "$_hdi_status" -eq 0 ]; then
			cat "$_hdi_out"
			rm -f "$_hdi_out"
			return 0
		fi
		cat "$_hdi_out" >&2
		_hdi_busy=0
		if hdiutil_output_is_busy "$_hdi_out"; then
			_hdi_busy=1
		fi
		rm -f "$_hdi_out"
		if [ "$_hdi_attempt" -ge "$hdiutil_max_attempts" ]; then
			if [ "$_hdi_busy" -eq 1 ]; then
				echo "hdiutil $_hdi_op: Resource busy after $hdiutil_max_attempts attempts" >&2
			fi
			return "$_hdi_status"
		fi
		if [ "$_hdi_busy" -eq 1 ]; then
			_hdi_sleep=$((hdiutil_busy_sleep * _hdi_attempt))
			echo "hdiutil $_hdi_op: Resource busy; retrying in ${_hdi_sleep}s (attempt $_hdi_attempt/$hdiutil_max_attempts)" >&2
			if [ "$_hdi_sleep" -gt 0 ]; then
				sleep "$_hdi_sleep"
			fi
			_hdi_attempt=$((_hdi_attempt + 1))
			continue
		fi
		# Non-busy create/attach failures are final. Detach skips ahead to
		# the forced last attempt.
		if [ "$_hdi_op" != "detach" ]; then
			return "$_hdi_status"
		fi
		_hdi_attempt=$hdiutil_max_attempts
	done
	return 1
}

# Best-effort: stop Spotlight from holding a staging dir or mount.
disable_spotlight() {
	if [ -z "${1:-}" ] || [ ! -d "$1" ]; then
		return 0
	fi
	if command -v mdutil >/dev/null 2>&1; then
		mdutil -i off "$1" >/dev/null 2>&1 || true
	fi
}

cleanup() {
	if [ -n "${MNT:-}" ] && [ -d "${MNT}" ]; then
		if [ -e "${MNT}/Updater.app" ] || [ -L "${MNT}/Applications" ]; then
			hdiutil_retry detach "${MNT}" >/dev/null 2>&1 || true
		fi
		rm -rf "${MNT}" || true
	fi
	if [ -n "${STAGE:-}" ]; then
		rm -rf "${STAGE}" || true
	fi
	if [ -n "${WORKDIR:-}" ]; then
		rm -rf "${WORKDIR}" || true
	fi
}
trap cleanup EXIT

# exit, not return. macOS /bin/sh is bash 3.2: a failing ${var:?} inside a
# function returns from the function, and set -e does not then abort the
# script when that function was called from an if body.
require_nonempty() {
	if [ -z "$2" ]; then
		echo "$1 is required when NOTARIZE=1" >&2
		exit 1
	fi
}

require_notarize_credentials() {
	APPSTORE_API_KEY_ID=$(printf '%s' "${APPSTORE_API_KEY_ID:-}")
	APPSTORE_API_ISSUER_ID=$(printf '%s' "${APPSTORE_API_ISSUER_ID:-}")
	export APPSTORE_API_KEY_ID APPSTORE_API_ISSUER_ID

	require_nonempty APPSTORE_API_KEY_PATH "${APPSTORE_API_KEY_PATH:-}"
	require_nonempty APPSTORE_API_KEY_ID "$APPSTORE_API_KEY_ID"
	require_nonempty APPSTORE_API_ISSUER_ID "$APPSTORE_API_ISSUER_ID"
	require_nonempty MACOS_SIGNING_IDENTITY "$SIGNING_IDENTITY"

	if [ ! -x "$XCRUN" ] || [ ! -x "$SPCTL" ]; then
		echo "xcrun and spctl are required when NOTARIZE=1" >&2
		exit 1
	fi
	if [ ! -f "$APPSTORE_API_KEY_PATH" ]; then
		echo "App Store Connect API key file not found" >&2
		exit 1
	fi
	if ! grep -q -- "-----BEGIN PRIVATE KEY-----" "$APPSTORE_API_KEY_PATH"; then
		echo "APPSTORE_API_KEY_P8_BASE64 did not decode to a PKCS#8 PEM private key" >&2
		exit 1
	fi
	if ! command -v python3 >/dev/null 2>&1; then
		echo "python3 is required to read the notarytool result" >&2
		exit 1
	fi
}

json_field() {
	python3 -c 'import json, sys
path, field = sys.argv[1], sys.argv[2]
try:
    with open(path) as fh:
        data = json.load(fh)
except Exception:
    raise SystemExit(0)
value = data.get(field, "")
if not isinstance(value, str):
    value = "" if value is None else str(value)
sys.stdout.write(value)' "$1" "$2"
}

detach_mount() {
	if [ -z "${MNT:-}" ]; then
		return 0
	fi
	if [ -d "$MNT" ]; then
		if [ -e "$MNT/Updater.app" ] || [ -L "$MNT/Applications" ]; then
			hdiutil_retry detach "$MNT" >/dev/null 2>&1 || true
		fi
		rm -rf "$MNT" || true
	fi
	MNT=
}

check_mounted() {
	require_staple=$1
	MNT=$(mktemp -d)
	hdiutil_retry attach -readonly -nobrowse -noautoopen -mountpoint "$MNT" "$DMG"
	disable_spotlight "$MNT"

	if [ ! -d "$MNT/Updater.app/Contents/MacOS" ] || [ ! -f "$MNT/Updater.app/Contents/MacOS/updater" ]; then
		echo "dmg is missing Updater.app" >&2
		exit 1
	fi
	if [ ! -L "$MNT/Applications" ]; then
		echo "dmg is missing the /Applications symlink" >&2
		exit 1
	fi
	link=$(readlink "$MNT/Applications")
	if [ "$link" != "/Applications" ]; then
		echo "Applications symlink target is wrong" >&2
		exit 1
	fi

	got=$("$PLISTBUDDY" -c 'Print :CFBundleShortVersionString' "$MNT/Updater.app/Contents/Info.plist")
	if [ "$got" != "$VERSION" ]; then
		echo "bundle version does not match the dmg version" >&2
		exit 1
	fi

	"$CODESIGN" --verify --deep --strict --verbose=2 "$MNT/Updater.app"
	if [ "$require_staple" = "1" ]; then
		"$XCRUN" stapler validate "$MNT/Updater.app"
		"$SPCTL" --assess --type execute -vv "$MNT/Updater.app"
	fi
	detach_mount
}

sign_dmg() {
	if [ -n "${MACOS_KEYCHAIN_PATH:-}" ]; then
		"$CODESIGN" \
			--force \
			--sign "$SIGNING_IDENTITY" \
			--keychain "$MACOS_KEYCHAIN_PATH" \
			--timestamp \
			"$DMG"
	else
		"$CODESIGN" \
			--force \
			--sign "$SIGNING_IDENTITY" \
			--timestamp \
			"$DMG"
	fi
	"$CODESIGN" --verify --verbose=2 "$DMG"
}

notarize_dmg() {
	WORKDIR=$(mktemp -d)
	SUBMIT_JSON=$WORKDIR/submit.json
	SUBMIT_ERR=$WORKDIR/submit.err

	set +e
	"$XCRUN" notarytool submit "$DMG" \
		--key "$APPSTORE_API_KEY_PATH" \
		--key-id "$APPSTORE_API_KEY_ID" \
		--issuer "$APPSTORE_API_ISSUER_ID" \
		--wait \
		--timeout 20m \
		--output-format json \
		>"$SUBMIT_JSON" 2>"$SUBMIT_ERR"
	submit_status=$?
	set -e

	if [ -s "$SUBMIT_JSON" ]; then
		cat "$SUBMIT_JSON"
	fi
	if [ -s "$SUBMIT_ERR" ]; then
		cat "$SUBMIT_ERR" >&2
	fi

	submission_id=$(json_field "$SUBMIT_JSON" id)
	status=$(json_field "$SUBMIT_JSON" status)

	if [ "$submit_status" -ne 0 ] || [ "$status" != "Accepted" ]; then
		echo "dmg notarization failed (status: ${status:-unknown})" >&2
		if [ -n "$submission_id" ]; then
			"$XCRUN" notarytool log "$submission_id" \
				--key "$APPSTORE_API_KEY_PATH" \
				--key-id "$APPSTORE_API_KEY_ID" \
				--issuer "$APPSTORE_API_ISSUER_ID" || true
		fi
		exit 1
	fi

	stapled=0
	attempt=0
	while [ "$attempt" -lt 5 ]; do
		if "$XCRUN" stapler staple "$DMG"; then
			stapled=1
			break
		fi
		attempt=$((attempt + 1))
		if [ "$attempt" -lt 5 ]; then
			sleep "$staple_sleep"
		fi
	done
	if [ "$stapled" -ne 1 ]; then
		echo "stapler staple failed for dmg" >&2
		exit 1
	fi

	"$XCRUN" stapler validate "$DMG"
	"$CODESIGN" --verify --verbose=2 "$DMG"
	"$SPCTL" --assess --type open --context context:primary-signature -vv "$DMG"
	echo "notarized and stapled $DMG"
}

if [ ! -d "$APP/Contents/MacOS" ] || [ ! -f "$APP/Contents/MacOS/updater" ]; then
	echo "not an Updater.app bundle: $APP" >&2
	exit 1
fi
if [ ! -f "$APP/Contents/Info.plist" ]; then
	echo "missing Info.plist" >&2
	exit 1
fi

if [ "${NOTARIZE:-}" = "1" ]; then
	require_notarize_credentials
	"$XCRUN" stapler validate "$APP"
fi

STAGE=$(mktemp -d)
"$DITTO" "$APP" "$STAGE/Updater.app"
ln -s /Applications "$STAGE/Applications"
"$CODESIGN" --verify --deep --strict --verbose=2 "$STAGE/Updater.app"
disable_spotlight "$STAGE"

mkdir -p "$OUT"
rm -f "$DMG"
hdiutil_retry create \
	-volname "Updater" \
	-srcfolder "$STAGE" \
	-ov \
	-format UDZO \
	-fs APFS \
	"$DMG"
"$HDIUTIL" verify "$DMG"

if [ "${NOTARIZE:-}" = "1" ]; then
	check_mounted 1
else
	check_mounted 0
fi

if [ -n "$SIGNING_IDENTITY" ]; then
	sign_dmg
	echo "signed $DMG"
else
	if [ "${NOTARIZE:-}" = "1" ]; then
		echo "MACOS_SIGNING_IDENTITY is required when NOTARIZE=1" >&2
		exit 1
	fi
	echo "dmg signing skipped"
fi

if [ "${NOTARIZE:-}" = "1" ]; then
	notarize_dmg
	check_mounted 1
else
	echo "dmg notarization skipped"
fi

if [ ! -f "$DMG" ]; then
	echo "dmg was not written" >&2
	exit 1
fi
echo "built $DMG"
