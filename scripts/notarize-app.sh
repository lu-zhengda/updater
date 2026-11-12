#!/bin/sh
# Notarize and staple an already-signed Updater.app.
#
# No-op unless NOTARIZE=1, so local builds and `goreleaser check` do not need
# Apple credentials. The release workflow sets NOTARIZE=1.
#
# Required when NOTARIZE=1:
#   APPSTORE_API_KEY_PATH      AuthKey .p8 file (PKCS#8 PEM)
#   APPSTORE_API_KEY_ID        App Store Connect key ID
#   APPSTORE_API_ISSUER_ID     App Store Connect issuer ID
#
# GoReleaser runs this from the universal-binary post hook, after mkapp.sh and
# before archives. The tarball the Homebrew cask checksums therefore contains
# the stapled bundle. The zip submitted to notarytool is temporary.
set -eu

if [ "${NOTARIZE:-}" != "1" ]; then
	echo "notarization skipped"
	exit 0
fi

APP="${1:?usage: notarize-app.sh <Updater.app>}"

# Secret files sometimes pick up a trailing newline. Trim those without
# printing the values. Base64 secrets are decoded before this script runs.
APPSTORE_API_KEY_ID="$(printf '%s' "${APPSTORE_API_KEY_ID:-}")"
APPSTORE_API_ISSUER_ID="$(printf '%s' "${APPSTORE_API_ISSUER_ID:-}")"
export APPSTORE_API_KEY_ID APPSTORE_API_ISSUER_ID

: "${APPSTORE_API_KEY_PATH:?APPSTORE_API_KEY_PATH is required when NOTARIZE=1}"
: "${APPSTORE_API_KEY_ID:?APPSTORE_API_KEY_ID is required when NOTARIZE=1}"
: "${APPSTORE_API_ISSUER_ID:?APPSTORE_API_ISSUER_ID is required when NOTARIZE=1}"

if [ ! -d "$APP" ]; then
	echo "app bundle not found: $APP" >&2
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

WORKDIR="$(mktemp -d)"
cleanup() {
	rm -rf "$WORKDIR"
}
trap cleanup EXIT

ZIP="$WORKDIR/Updater.zip"
SUBMIT_JSON="$WORKDIR/submit.json"
SUBMIT_ERR="$WORKDIR/submit.err"

# ditto keeps the code signature; zip(1) can strip it.
/usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"

set +e
xcrun notarytool submit "$ZIP" \
	--key "$APPSTORE_API_KEY_PATH" \
	--key-id "$APPSTORE_API_KEY_ID" \
	--issuer "$APPSTORE_API_ISSUER_ID" \
	--wait \
	--timeout 20m \
	--output-format json \
	>"$SUBMIT_JSON" 2>"$SUBMIT_ERR"
SUBMIT_STATUS=$?
set -e

# The JSON result has no secrets. Show it, then the log on failure.
if [ -s "$SUBMIT_JSON" ]; then
	cat "$SUBMIT_JSON"
fi
if [ -s "$SUBMIT_ERR" ]; then
	cat "$SUBMIT_ERR" >&2
fi

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

SUBMISSION_ID="$(json_field "$SUBMIT_JSON" id)"
STATUS="$(json_field "$SUBMIT_JSON" status)"

if [ "$SUBMIT_STATUS" -ne 0 ] || [ "$STATUS" != "Accepted" ]; then
	echo "notarization failed (status: ${STATUS:-unknown})" >&2
	if [ -n "$SUBMISSION_ID" ]; then
		xcrun notarytool log "$SUBMISSION_ID" \
			--key "$APPSTORE_API_KEY_PATH" \
			--key-id "$APPSTORE_API_KEY_ID" \
			--issuer "$APPSTORE_API_ISSUER_ID" || true
	fi
	exit 1
fi

# Accepted can land a few seconds before the ticket is downloadable.
stapled=0
attempt=0
while [ "$attempt" -lt 5 ]; do
	if xcrun stapler staple "$APP"; then
		stapled=1
		break
	fi
	attempt=$((attempt + 1))
	if [ "$attempt" -lt 5 ]; then
		sleep 15
	fi
done
if [ "$stapled" -ne 1 ]; then
	echo "stapler staple failed" >&2
	exit 1
fi

xcrun stapler validate "$APP"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$APP"
spctl --assess --type execute -vv "$APP"
echo "notarized and stapled $APP"
