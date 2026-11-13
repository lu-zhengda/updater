#!/bin/sh
# Build a GoReleaser snapshot and install that tarball with the generated cask.
#
# Confirms the archive contains the completion files the cask links, then
# installs and uninstalls a renamed copy from a throwaway tap. The installed
# Updater.app and its Homebrew receipt are restored if a link has to be moved
# aside. Nothing is signed, notarized, tagged, or published.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
cd "$root"

if ! command -v brew >/dev/null 2>&1; then
	echo "brew is required" >&2
	exit 1
fi

export HOMEBREW_NO_AUTO_UPDATE=1
export HOMEBREW_NO_ENV_HINTS=1
export HOMEBREW_NO_INSTALL_CLEANUP=1

if ! command -v goreleaser >/dev/null 2>&1; then
	if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
		brew install goreleaser
	else
		echo "goreleaser is required" >&2
		exit 1
	fi
fi

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ok() {
	echo "ok: $*"
}

tmp=$(mktemp -d)
appdir=$tmp/Applications
save=$tmp/save
mkdir -p "$appdir" "$save"
tap_name=localtest/updaterproof
prefix=$(brew --prefix)
installed_app=/Applications/Updater.app
receipt=$prefix/Caskroom/updater/.metadata/INSTALL_RECEIPT.json

app_version() {
	if [ ! -d "$installed_app" ]; then
		printf '%s\n' missing
		return 0
	fi
	/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$installed_app/Contents/Info.plist" 2>/dev/null || printf '%s\n' unreadable
}

receipt_version() {
	if [ ! -f "$receipt" ]; then
		printf '%s\n' missing
		return 0
	fi
	python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source"]["version"])' "$receipt" 2>/dev/null || printf '%s\n' unreadable
}

host_before_app=$(app_version)
host_before_receipt=$(receipt_version)

link_paths="
$prefix/bin/updater
$prefix/etc/bash_completion.d/updater
$prefix/share/fish/vendor_completions.d/updater.fish
$prefix/share/zsh/site-functions/_updater
"

save_links() {
	n=0
	for path in $link_paths; do
		n=$((n + 1))
		printf '%s\n' "$path" >"$save/$n.path"
		if [ -L "$path" ]; then
			echo symlink >"$save/$n.kind"
			readlink "$path" >"$save/$n.target"
		elif [ -e "$path" ]; then
			echo file >"$save/$n.kind"
			cp -R "$path" "$save/$n.data"
		else
			echo absent >"$save/$n.kind"
		fi
	done
	printf '%s\n' "$n" >"$save/count"
}

move_links_aside() {
	n=$(cat "$save/count")
	i=1
	while [ "$i" -le "$n" ]; do
		path=$(cat "$save/$i.path")
		kind=$(cat "$save/$i.kind")
		if [ "$kind" != absent ] && [ -e "$path" ]; then
			rm -rf "$path"
		fi
		i=$((i + 1))
	done
}

restore_links() {
	if [ ! -f "$save/count" ]; then
		return 0
	fi
	n=$(cat "$save/count")
	i=1
	while [ "$i" -le "$n" ]; do
		path=$(cat "$save/$i.path")
		kind=$(cat "$save/$i.kind")
		rm -rf "$path"
		case $kind in
		symlink)
			ln -s "$(cat "$save/$i.target")" "$path"
			;;
		file)
			cp -R "$save/$i.data" "$path"
			;;
		esac
		i=$((i + 1))
	done
}

cleanup() {
	set +e
	if [ -n "${tap_name:-}" ]; then
		brew uninstall --cask "$tap_name/updaterproof" >/dev/null 2>&1
		brew untap "$tap_name" >/dev/null 2>&1
	fi
	restore_links
	rm -rf "$tmp"
}
trap cleanup EXIT

save_links
move_links_aside

env -u GITHUB_TOKEN -u GH_TOKEN -u HOMEBREW_TAP_TOKEN \
	-u NOTARIZE -u MACOS_SIGNING_IDENTITY \
	-u MACOS_CERT_P12_BASE64 -u MACOS_CERT_PASSWORD \
	-u APPSTORE_API_KEY_P8_BASE64 -u APPSTORE_API_KEY_ID \
	-u APPSTORE_API_ISSUER_ID -u APPSTORE_API_KEY_PATH \
	-u MACOS_KEYCHAIN_PATH \
	goreleaser release --snapshot --clean --skip=publish

tarball=$(find dist -maxdepth 1 -name 'updater_*_darwin.tar.gz' | head -n 1)
if [ -z "$tarball" ] || [ ! -f "$tarball" ]; then
	fail "snapshot tarball was not written"
fi
for name in completions/updater.bash completions/updater.zsh completions/updater.fish; do
	if ! tar -tzf "$tarball" | grep -qx "$name"; then
		echo "tarball entries:" >&2
		tar -tzf "$tarball" >&2
		fail "tarball is missing $name"
	fi
done
ok "tarball contains the three completion files"

sha=$(shasum -a 256 "$tarball" | awk '{print $1}')
abs=$(cd "$(dirname "$tarball")" && pwd)/$(basename "$tarball")
file_url="file://${abs}"

cask_src=dist/homebrew/Casks/updater.rb
if [ ! -f "$cask_src" ]; then
	fail "generated cask is missing"
fi
mkdir -p "$tmp/tap/Casks"
awk -v sha="$sha" -v url="$file_url" '
	$0 ~ /^cask "updater" do$/ { print "cask \"updaterproof\" do"; next }
	$0 ~ /^[[:space:]]*sha256 "/ { sub(/sha256 "[^"]*"/, "sha256 \"" sha "\""); print; next }
	$0 ~ /^[[:space:]]*url "/ { sub(/url "[^"]*"/, "url \"" url "\""); print; next }
	{ print }
' "$cask_src" >"$tmp/tap/Casks/updaterproof.rb"

git -C "$tmp/tap" init -b main >/dev/null
git -C "$tmp/tap" config user.email "test@example.com"
git -C "$tmp/tap" config user.name "Test"
git -C "$tmp/tap" config commit.gpgsign false
git -C "$tmp/tap" add Casks/updaterproof.rb
git -C "$tmp/tap" commit -m "cask" >/dev/null
brew untap "$tap_name" >/dev/null 2>&1 || true
brew tap --force "$tap_name" "$tmp/tap" >/dev/null
brew trust "$tap_name" >/dev/null

brew install --cask --appdir "$appdir" "$tap_name/updaterproof"

proof_app=$appdir/Updater.app
if [ ! -d "$proof_app" ]; then
	fail "cask install did not land Updater.app in $appdir"
fi
proof_version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$proof_app/Contents/Info.plist")
cask_version=$(awk -F'"' '$1 ~ /version / { print $2; exit }' "$tmp/tap/Casks/updaterproof.rb")
if [ "$proof_version" != "$cask_version" ]; then
	fail "installed app version $proof_version != cask version $cask_version"
fi
ok "app landed at $proof_version"

bin_link=$prefix/bin/updater
bin_target=$(readlink "$bin_link" || true)
case $bin_target in
"$proof_app/Contents/MacOS/updater")
	ok "updater binary link points at the proof app"
	;;
*)
	fail "updater binary link is '$bin_target'"
	;;
esac

for path in \
	"$prefix/etc/bash_completion.d/updater" \
	"$prefix/share/zsh/site-functions/_updater" \
	"$prefix/share/fish/vendor_completions.d/updater.fish"
do
	if [ ! -s "$path" ]; then
		fail "completion did not land: $path"
	fi
	ok "completion landed: $path"
done

brew uninstall --cask "$tap_name/updaterproof"
if [ -e "$proof_app" ]; then
	fail "uninstall left $proof_app"
fi
ok "brew uninstall removed the proof app"

restore_links
trap - EXIT
brew untap "$tap_name" >/dev/null 2>&1 || true
rm -rf "$tmp"

host_after_app=$(app_version)
host_after_receipt=$(receipt_version)
if [ "$host_after_app" != "$host_before_app" ] || [ "$host_after_receipt" != "$host_before_receipt" ]; then
	fail "host Updater changed from app=$host_before_app receipt=$host_before_receipt to app=$host_after_app receipt=$host_after_receipt"
fi
ok "host Updater unchanged app=$host_after_app receipt=$host_after_receipt"
echo "cask archive proof ok"
