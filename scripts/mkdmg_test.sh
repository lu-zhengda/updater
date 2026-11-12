#!/bin/sh
# Exercises scripts/mkdmg.sh with stand-in macOS tools, and with hdiutil
# when this machine has one. No Apple credentials and no notarization.
# local is a dash and bash builtin. This file is executed with sh.
# shellcheck shell=sh disable=SC3043
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/mkdmg.sh

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ok() {
	echo "ok: $*"
}

assert_file() {
	if [ ! -f "$1" ]; then
		fail "missing file $1"
	fi
}

assert_eq() {
	if [ "$1" != "$2" ]; then
		echo "got [$1] want [$2]" >&2
		fail "value mismatch"
	fi
}

refuse() {
	local name="$1"
	shift
	if "$@" >"$tmp/refuse.out" 2>"$tmp/refuse.err"; then
		cat "$tmp/refuse.out" >&2
		cat "$tmp/refuse.err" >&2
		fail "$name was expected to fail"
	fi
	ok "$name"
}

install_mocks() {
	local bin="$1"

	cat >"$bin/ditto" <<'EOF'
#!/bin/sh
set -eu
if [ "$#" -ne 2 ]; then
	echo "ditto mock expected 2 arguments" >&2
	exit 1
fi
mkdir -p "$(dirname "$2")"
cp -R "$1" "$2"
EOF

	cat >"$bin/hdiutil" <<'EOF'
#!/bin/sh
set -eu
log=$UPDATER_DMG_STATE/log
printf 'hdiutil' >>"$log"
for arg in "$@"; do
	printf ' %s' "$arg" >>"$log"
done
printf '\n' >>"$log"

if [ "${1:-}" = "create" ]; then
	src=
	prev=
	out=
	for arg in "$@"; do
		if [ "$prev" = "-srcfolder" ]; then
			src=$arg
		fi
		prev=$arg
		out=$arg
	done
	if [ -z "$src" ] || [ -z "$out" ]; then
		echo "hdiutil mock could not see -srcfolder or output" >&2
		exit 1
	fi
	rm -rf "$UPDATER_DMG_STATE/stage"
	mkdir -p "$UPDATER_DMG_STATE/stage"
	cp -R "$src/." "$UPDATER_DMG_STATE/stage/"
	printf 'fake-dmg\n' >"$out"
fi

if [ "${1:-}" = "attach" ]; then
	mnt=
	prev=
	for arg in "$@"; do
		if [ "$prev" = "-mountpoint" ]; then
			mnt=$arg
		fi
		prev=$arg
	done
	if [ -z "$mnt" ]; then
		echo "hdiutil mock could not see -mountpoint" >&2
		exit 1
	fi
	mkdir -p "$mnt"
	cp -R "$UPDATER_DMG_STATE/stage/." "$mnt/"
fi
EOF

	cat >"$bin/codesign" <<'EOF'
#!/bin/sh
set -eu
log=$UPDATER_DMG_STATE/log
printf 'codesign' >>"$log"
for arg in "$@"; do
	printf ' %s' "$arg" >>"$log"
done
printf '\n' >>"$log"
EOF

	cat >"$bin/spctl" <<'EOF'
#!/bin/sh
set -eu
log=$UPDATER_DMG_STATE/log
printf 'spctl' >>"$log"
for arg in "$@"; do
	printf ' %s' "$arg" >>"$log"
done
printf '\n' >>"$log"
EOF

	cat >"$bin/xcrun" <<'EOF'
#!/bin/sh
set -eu
log=$UPDATER_DMG_STATE/log
printf 'xcrun' >>"$log"
for arg in "$@"; do
	printf ' %s' "$arg" >>"$log"
done
printf '\n' >>"$log"

if [ "${1:-}" = "notarytool" ] && [ "${2:-}" = "submit" ]; then
	status=${MOCK_NOTARY_STATUS:-Accepted}
	printf '{"id":"test-submission","status":"%s"}\n' "$status"
fi

if [ "${1:-}" = "stapler" ] && [ "${2:-}" = "staple" ]; then
	count_file=$UPDATER_DMG_STATE/staple-count
	n=0
	if [ -f "$count_file" ]; then
		n=$(cat "$count_file")
	fi
	n=$((n + 1))
	printf '%s\n' "$n" >"$count_file"
	fails=${MOCK_STAPLE_FAILS:-0}
	if [ "$n" -le "$fails" ]; then
		echo "notary ticket not yet available" >&2
		exit 1
	fi
fi
EOF

	cat >"$bin/PlistBuddy" <<'EOF'
#!/bin/sh
set -eu
file=
for arg in "$@"; do
	file=$arg
done
python3 - "$file" <<'PY'
import re
import sys
text = open(sys.argv[1], encoding="utf-8").read()
match = re.search(
    r"<key>CFBundleShortVersionString</key>\s*<string>([^<]*)</string>",
    text,
)
if match is None:
    sys.exit(1)
sys.stdout.write(match.group(1))
PY
EOF

	chmod 755 "$bin/ditto" "$bin/hdiutil" "$bin/codesign" "$bin/spctl" "$bin/xcrun" "$bin/PlistBuddy"
}

make_app() {
	local app="$1"
	local version="$2"
	mkdir -p "$app/Contents/MacOS"
	printf '#!/bin/sh\nexit 0\n' >"$app/Contents/MacOS/updater"
	chmod 755 "$app/Contents/MacOS/updater"
	cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.updater.app</string>
	<key>CFBundleName</key>
	<string>Updater</string>
	<key>CFBundleExecutable</key>
	<string>updater</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${version}</string>
	<key>CFBundleVersion</key>
	<string>${version}</string>
</dict>
</plist>
EOF
}

new_case() {
	local version="$1"
	case_n=$((case_n + 1))
	state=$tmp/case-$case_n
	bin=$state/bin
	out=$state/out
	app=$state/src/Updater.app
	rm -rf "$state"
	mkdir -p "$bin" "$out"
	install_mocks "$bin"
	make_app "$app" "$version"
	: >"$state/log"
}

reset_env() {
	unset NOTARIZE || true
	unset MACOS_SIGNING_IDENTITY || true
	unset MACOS_KEYCHAIN_PATH || true
	unset APPSTORE_API_KEY_PATH || true
	unset APPSTORE_API_KEY_ID || true
	unset APPSTORE_API_ISSUER_ID || true
	unset MOCK_NOTARY_STATUS || true
	unset MOCK_STAPLE_FAILS || true
	unset UPDATER_DMG_STAPLE_SLEEP || true
}

# --- release config still publishes this exact filename ---

goreleaser=$root/.goreleaser.yaml
if ! grep -F 'sh scripts/mkdmg.sh dist/Updater.app "{{ .Version }}" dist' "$goreleaser" >/dev/null; then
	fail "goreleaser does not call mkdmg.sh with the release version"
fi
glob_count=$(grep -F -c 'glob: ./dist/Updater-{{ .Version }}.dmg' "$goreleaser" || true)
assert_eq "$glob_count" "2"
if ! grep -F 'id: updater-archive' "$goreleaser" >/dev/null; then
	fail "archive id changed"
fi
if ! grep -F -- '- tar.gz' "$goreleaser" >/dev/null; then
	fail "tarball archive format changed"
fi
if ! grep -F 'ids:' -A 2 "$goreleaser" | grep -F 'updater-archive' >/dev/null; then
	fail "cask no longer selects the tarball archive"
fi
ok "goreleaser filename contract"

# --- argument checks ---

refuse "usage" sh "$script"
refuse "bad version" sh "$script" "$tmp/missing.app" '1.2.3/evil' "$tmp/out"

# --- mock packaging ---

case_n=0

new_case 1.2.3
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state"
if sh "$script" "$tmp/does-not-exist.app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"; then
	fail "missing app was accepted"
fi
if [ -s "$state/log" ]; then
	cat "$state/log" >&2
	fail "missing app invoked hdiutil or codesign"
fi
ok "missing app"

new_case 1.2.3
reset_env
if env -u APPSTORE_API_KEY_PATH -u APPSTORE_API_KEY_ID -u APPSTORE_API_ISSUER_ID \
	-u MACOS_SIGNING_IDENTITY -u MACOS_KEYCHAIN_PATH \
	UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state" NOTARIZE=1 \
	sh "$script" "$app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"; then
	echo "---- stdout ----" >&2
	cat "$state/stdout" >&2
	echo "---- stderr ----" >&2
	cat "$state/stderr" >&2
	fail "notarization without credentials was accepted"
fi
if [ -s "$state/log" ]; then
	cat "$state/log" >&2
	fail "missing credentials still built a dmg"
fi
if grep -F 'SUPER_SECRET_TEST_KEY' "$state/stdout" "$state/stderr" >/dev/null 2>&1; then
	fail "secret material was printed"
fi
ok "notarize requires credentials"

new_case 1.2.3
key=$state/AuthKey.p8
printf '%s\n' 'SUPER_SECRET_TEST_KEY' >"$key"
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state" NOTARIZE=1
export APPSTORE_API_KEY_PATH="$key" APPSTORE_API_KEY_ID=KEYID APPSTORE_API_ISSUER_ID=ISSUER
export MACOS_SIGNING_IDENTITY='Developer ID Application: Test (TEAMID)'
if sh "$script" "$app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"; then
	fail "non-PEM key was accepted"
fi
if grep -F 'SUPER_SECRET_TEST_KEY' "$state/stdout" "$state/stderr" >/dev/null; then
	fail "key file body was printed"
fi
if [ -s "$state/log" ]; then
	fail "invalid key still built a dmg"
fi
ok "notarize rejects a non-PEM key without printing it"

new_case 1.2.3
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state" NOTARIZE=1
export APPSTORE_API_KEY_PATH="$state/missing-key.p8" APPSTORE_API_KEY_ID=KEYID APPSTORE_API_ISSUER_ID=ISSUER
export MACOS_SIGNING_IDENTITY='-'
if sh "$script" "$app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"; then
	fail "ad-hoc identity was accepted for notarization"
fi
ok "notarize rejects an ad-hoc identity"

new_case 1.2.3
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state"
sh "$script" "$app" v1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"
assert_file "$out/Updater-1.2.3.dmg"
if [ -e "$out/Updater-v1.2.3.dmg" ]; then
	fail "version prefix was kept in the filename"
fi
if ! grep -F 'dmg signing skipped' "$state/stdout" >/dev/null; then
	fail "expected signing to be skipped"
fi
if ! grep -F 'dmg notarization skipped' "$state/stdout" >/dev/null; then
	fail "expected notarization to be skipped"
fi
if grep -F -- '--sign' "$state/log" >/dev/null; then
	cat "$state/log" >&2
	fail "unsigned run signed the dmg"
fi
if grep -F 'xcrun' "$state/log" >/dev/null; then
	fail "unsigned run called xcrun"
fi
if ! grep -F -- '-volname Updater' "$state/log" >/dev/null; then
	fail "volume name was not Updater"
fi
if ! grep -F -- '-format UDZO' "$state/log" >/dev/null; then
	fail "image format was not UDZO"
fi
if ! grep -F -- '-fs APFS' "$state/log" >/dev/null; then
	fail "filesystem was not APFS"
fi
if [ ! -d "$state/stage/Updater.app/Contents/MacOS" ]; then
	fail "staged image is missing Updater.app"
fi
if [ ! -L "$state/stage/Applications" ]; then
	fail "staged image is missing the Applications symlink"
fi
assert_eq "$(readlink "$state/stage/Applications")" "/Applications"
ok "unsigned image layout and filename"

new_case 4.5.6
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state"
export MACOS_SIGNING_IDENTITY='Developer ID Application: Test (TEAMID)'
export MACOS_KEYCHAIN_PATH="$state/test.keychain-db"
sh "$script" "$app" 4.5.6 "$out" >"$state/stdout" 2>"$state/stderr"
assert_file "$out/Updater-4.5.6.dmg"
if ! grep -F 'signed ' "$state/stdout" >/dev/null; then
	fail "expected the dmg to be signed"
fi
sign_line=$(grep -F -- '--sign' "$state/log" || true)
if [ -z "$sign_line" ]; then
	cat "$state/log" >&2
	fail "codesign was not asked to sign"
fi
if ! printf '%s\n' "$sign_line" | grep -F -- '--timestamp' >/dev/null; then
	fail "dmg signature has no timestamp"
fi
if ! printf '%s\n' "$sign_line" | grep -F -- "--keychain $state/test.keychain-db" >/dev/null; then
	fail "dmg signature did not use the temporary keychain"
fi
if printf '%s\n' "$sign_line" | grep -F -- '--options' >/dev/null; then
	fail "dmg signature used executable runtime options"
fi
if printf '%s\n' "$sign_line" | grep -F -- '--entitlements' >/dev/null; then
	fail "dmg signature used app entitlements"
fi
if ! printf '%s\n' "$sign_line" | grep -F -- "$out/Updater-4.5.6.dmg" >/dev/null; then
	fail "codesign target was not the dmg"
fi
if grep -F 'xcrun' "$state/log" >/dev/null; then
	fail "signing without NOTARIZE called notarytool"
fi
ok "signed image uses a timestamp and the temporary keychain"

new_case 1.2.3
key=$state/AuthKey.p8
printf '%s\n' '-----BEGIN PRIVATE KEY-----' 'SUPER_SECRET_TEST_KEY' '-----END PRIVATE KEY-----' >"$key"
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state" NOTARIZE=1
export UPDATER_DMG_STAPLE_SLEEP=0 MOCK_STAPLE_FAILS=2
export APPSTORE_API_KEY_PATH="$key"
APPSTORE_API_KEY_ID=$(printf 'KEYID\n')
APPSTORE_API_ISSUER_ID=$(printf 'ISSUER\n')
export APPSTORE_API_KEY_ID APPSTORE_API_ISSUER_ID
export MACOS_SIGNING_IDENTITY='Developer ID Application: Test (TEAMID)'
export MACOS_KEYCHAIN_PATH="$state/test.keychain-db"
sh "$script" "$app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"
assert_file "$out/Updater-1.2.3.dmg"
if grep -F 'SUPER_SECRET_TEST_KEY' "$state/stdout" "$state/stderr" >/dev/null; then
	fail "notarization printed the API key"
fi
if ! grep -F 'notarized and stapled ' "$state/stdout" >/dev/null; then
	cat "$state/stdout" >&2
	fail "expected a stapled dmg"
fi
submit_line=$(grep -F 'xcrun notarytool submit' "$state/log" || true)
if [ -z "$submit_line" ]; then
	cat "$state/log" >&2
	fail "notarytool submit was not called"
fi
if ! printf '%s\n' "$submit_line" | grep -F -- "$out/Updater-1.2.3.dmg" >/dev/null; then
	fail "notarytool did not submit the dmg"
fi
if printf '%s\n' "$submit_line" | grep -F 'Updater.zip' >/dev/null; then
	fail "dmg notarization zipped the image"
fi
if ! grep -F 'xcrun stapler staple' "$state/log" >/dev/null; then
	fail "stapler staple was not called"
fi
if ! grep -F 'xcrun stapler validate' "$state/log" >/dev/null; then
	fail "stapler validate was not called"
fi
if ! grep -F 'spctl --assess --type open --context context:primary-signature' "$state/log" >/dev/null; then
	cat "$state/log" >&2
	fail "disk image was not assessed as an openable image"
fi
if ! grep -F 'spctl --assess --type execute' "$state/log" >/dev/null; then
	fail "app inside the image was not assessed"
fi
staple_count=$(cat "$state/staple-count")
if [ "$staple_count" -lt 3 ]; then
	fail "staple retry did not run (count $staple_count)"
fi
ok "notarize submits, retries staple, and assesses the image"

new_case 1.2.3
key=$state/AuthKey.p8
printf '%s\n' '-----BEGIN PRIVATE KEY-----' 'SUPER_SECRET_TEST_KEY' '-----END PRIVATE KEY-----' >"$key"
reset_env
export UPDATER_DMG_TEST=1 UPDATER_DMG_TOOLS="$bin" UPDATER_DMG_STATE="$state" NOTARIZE=1
export MOCK_NOTARY_STATUS=Invalid
export APPSTORE_API_KEY_PATH="$key" APPSTORE_API_KEY_ID=KEYID APPSTORE_API_ISSUER_ID=ISSUER
export MACOS_SIGNING_IDENTITY='Developer ID Application: Test (TEAMID)'
if sh "$script" "$app" 1.2.3 "$out" >"$state/stdout" 2>"$state/stderr"; then
	fail "rejected notarization was accepted"
fi
if ! grep -F 'xcrun notarytool log test-submission' "$state/log" >/dev/null; then
	cat "$state/log" >&2
	fail "rejected notarization did not request the notary log"
fi
if grep -F 'SUPER_SECRET_TEST_KEY' "$state/stdout" "$state/stderr" >/dev/null; then
	fail "rejected notarization printed the API key"
fi
if grep -F 'xcrun stapler staple' "$state/log" >/dev/null; then
	fail "rejected notarization stapled the image"
fi
ok "rejected notarization fails and requests the log"

# --- real hdiutil, still without a signing identity or notarization ---

if [ ! -x /usr/bin/hdiutil ] || [ ! -x /usr/bin/ditto ] || [ ! -x /usr/bin/codesign ]; then
	echo "skip real dmg: hdiutil, ditto, or codesign is not on this machine"
	ok "mock dmg packaging"
	exit 0
fi

real=$tmp/real
mkdir -p "$real/out"
make_app "$real/Updater.app" 7.8.9
cp /usr/bin/true "$real/Updater.app/Contents/MacOS/updater"
chmod 755 "$real/Updater.app/Contents/MacOS/updater"
/usr/bin/codesign --force --sign - --identifier com.updater.app \
	"$real/Updater.app/Contents/MacOS/updater"
/usr/bin/codesign --force --sign - --identifier com.updater.app \
	"$real/Updater.app"

reset_env
unset UPDATER_DMG_TEST || true
unset UPDATER_DMG_TOOLS || true
unset UPDATER_DMG_STATE || true
if ! sh "$script" "$real/Updater.app" v7.8.9 "$real/out" >"$real/stdout" 2>"$real/stderr"; then
	cat "$real/stdout" >&2
	cat "$real/stderr" >&2
	fail "real hdiutil packaging failed"
fi
dmg=$real/out/Updater-7.8.9.dmg
assert_file "$dmg"
size=$(wc -c <"$dmg" | tr -d ' ')
if [ "$size" -gt 30000000 ]; then
	fail "real dmg is $size bytes; the Applications symlink may have been followed"
fi
if /usr/bin/codesign --verify "$dmg" >/dev/null 2>&1; then
	fail "real dmg was signed without an identity"
fi

mnt=$(mktemp -d)
detach_real() {
	if [ -n "${mnt:-}" ] && { [ -d "$mnt/Updater.app" ] || [ -L "$mnt/Applications" ]; }; then
		/usr/bin/hdiutil detach "$mnt" >/dev/null 2>&1 ||
			/usr/bin/hdiutil detach -force "$mnt" >/dev/null 2>&1 || true
	fi
	if [ -n "${mnt:-}" ]; then
		rm -rf "$mnt"
	fi
	mnt=
}
trap 'detach_real; rm -rf "$tmp"' EXIT
if ! /usr/bin/hdiutil attach -readonly -nobrowse -noautoopen -mountpoint "$mnt" "$dmg" >"$real/attach" 2>&1; then
	cat "$real/attach" >&2
	detach_real
	fail "could not attach the real dmg"
fi
if [ ! -d "$mnt/Updater.app" ] || [ ! -L "$mnt/Applications" ]; then
	detach_real
	fail "real dmg layout is missing the app or the Applications symlink"
fi
if [ "$(readlink "$mnt/Applications")" != "/Applications" ]; then
	detach_real
	fail "real Applications symlink target is wrong"
fi
if ! /usr/bin/codesign --verify --deep --strict --verbose=2 "$mnt/Updater.app" >"$real/verify" 2>&1; then
	cat "$real/verify" >&2
	detach_real
	fail "app signature did not survive the real dmg"
fi
volume=$(/usr/sbin/diskutil info "$mnt" | sed -n 's/^[[:space:]]*Volume Name:[[:space:]]*//p' | head -n 1)
detach_real
assert_eq "$volume" "Updater"
ok "real hdiutil image preserves the app, symlink, and volume name"

ok "dmg packaging"
