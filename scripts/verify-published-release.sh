#!/bin/sh
# Fail unless the published Homebrew cask and GitHub Release assets match
# the tag that just shipped.
#
# The cask version in lu-zhengda/homebrew-tap Casks/updater.rb must equal
# the tag without its leading v. The cask tarball URL and
# Updater-<version>.dmg must each download anonymously with HTTP 200 and
# a body larger than 1 MiB (1048576 bytes).
#
# GitHub's contents API advertises a 60s shared cache, and release assets
# redirect through a CDN. This retries at most 7 times, sleeping 10s
# between tries (60s of waiting), then fails. VERIFY_PUBLISHED_ATTEMPTS
# (1-7) and VERIFY_PUBLISHED_SLEEP (0, 5, or 10) exist so the self-test
# can shrink that bound. The release job does not set them.
#
# curl is invoked with an empty token environment, -q, and https only.
# Signing secrets and HOMEBREW_TAP_TOKEN are not read.
# shellcheck shell=sh
set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: verify-published-release.sh <vX.Y.Z>" >&2
	exit 2
fi

attempts=${VERIFY_PUBLISHED_ATTEMPTS:-7}
sleep_seconds=${VERIFY_PUBLISHED_SLEEP:-10}
case $attempts in
1 | 2 | 3 | 4 | 5 | 6 | 7) ;;
*)
	echo "VERIFY_PUBLISHED_ATTEMPTS must be 1-7" >&2
	exit 2
	;;
esac
case $sleep_seconds in
0 | 5 | 10) ;;
*)
	echo "VERIFY_PUBLISHED_SLEEP must be 0, 5, or 10" >&2
	exit 2
	;;
esac

min_bytes=1048576
cask_url=https://api.github.com/repos/lu-zhengda/homebrew-tap/contents/Casks/updater.rb

is_component() {
	case $1 in
	"" | *[!0-9]*)
		return 1
		;;
	0 | [1-9] | [1-9][0-9]*)
		return 0
		;;
	*)
		return 1
		;;
	esac
}

tag=$1
case $tag in
v*) ;;
*)
	echo "tag must look like vX.Y.Z" >&2
	exit 2
	;;
esac
version=${tag#v}
case $version in
*.*.*) ;;
*)
	echo "tag must look like vX.Y.Z" >&2
	exit 2
	;;
esac
case $version in
*.*.*.*)
	echo "tag must look like vX.Y.Z" >&2
	exit 2
	;;
esac
major=${version%%.*}
rest=${version#*.}
minor=${rest%%.*}
patch=${rest#*.}
if ! is_component "$major" || ! is_component "$minor" || ! is_component "$patch"; then
	echo "tag must look like vX.Y.Z" >&2
	exit 2
fi

tarball_url="https://github.com/lu-zhengda/updater/releases/download/v${version}/updater_${version}_darwin.tar.gz"
dmg_url="https://github.com/lu-zhengda/updater/releases/download/v${version}/Updater-${version}.dmg"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

anonymous_curl() {
	env -u GITHUB_TOKEN -u GH_TOKEN -u GITHUB_ENTERPRISE_TOKEN -u HOMEBREW_TAP_TOKEN \
		curl -q "$@"
}

# fetch <label> <dest> <max-seconds> <url> [curl args...]
# Prints one failure line and returns 1 unless the final HTTP status is 200.
fetch() {
	_label=$1
	_dest=$2
	_max_time=$3
	_url=$4
	shift 4
	: >"$_dest"
	_code=$(
		anonymous_curl -sS -L --proto '=https' --tlsv1.2 --retry 0 \
			--max-time "$_max_time" -o "$_dest" -w '%{http_code}' \
			"$@" -- "$_url"
	) && _status=0 || _status=$?
	if [ "$_status" -ne 0 ]; then
		echo "attempt $attempt/$attempts: $_label curl exit $_status" >&2
		return 1
	fi
	_code=$(printf '%s' "$_code" | tr -d '[:space:]')
	if [ "$_code" != "200" ]; then
		echo "attempt $attempt/$attempts: $_label HTTP ${_code:-none}" >&2
		return 1
	fi
	return 0
}

last_size=
check_asset() {
	_label=$1
	_dest=$2
	_url=$3
	if ! fetch "$_label" "$_dest" 60 "$_url"; then
		return 1
	fi
	last_size=$(wc -c <"$_dest" | tr -d '[:space:]')
	case $last_size in
	"" | *[!0-9]*)
		echo "attempt $attempt/$attempts: $_label size unreadable" >&2
		return 1
		;;
	esac
	if [ "$last_size" -le "$min_bytes" ]; then
		echo "attempt $attempt/$attempts: $_label HTTP 200 size $last_size, want > $min_bytes (1 MiB)" >&2
		return 1
	fi
	return 0
}

verify_once() {
	if ! fetch cask "$work/cask.rb" 20 "$cask_url" \
		-H "Accept: application/vnd.github.raw" \
		-H "Cache-Control: no-cache"; then
		return 1
	fi
	found=$(awk -F '"' '$1 ~ /^[[:space:]]*version / { print $2; exit }' "$work/cask.rb" | tr -d '\r')
	if [ "$found" != "$version" ]; then
		echo "attempt $attempt/$attempts: cask version [${found:-missing}] != $version" >&2
		return 1
	fi
	got_url=$(awk -F '"' '$1 ~ /^[[:space:]]*url / { print $2; exit }' "$work/cask.rb" | tr -d '\r')
	got_url=$(printf '%s' "$got_url" | sed "s/#{version}/${version}/g")
	if [ "$got_url" != "$tarball_url" ]; then
		echo "attempt $attempt/$attempts: cask tarball url [${got_url:-missing}] != $tarball_url" >&2
		return 1
	fi
	if ! check_asset tarball "$work/tarball" "$tarball_url"; then
		return 1
	fi
	tarball_bytes=$last_size
	if ! check_asset dmg "$work/dmg" "$dmg_url"; then
		return 1
	fi
	dmg_bytes=$last_size
	echo "PASS verify-published-release version=$version tarball_bytes=$tarball_bytes dmg_bytes=$dmg_bytes"
	return 0
}

attempt=1
while :; do
	if verify_once; then
		exit 0
	fi
	if [ "$attempt" -ge "$attempts" ]; then
		echo "FAIL verify-published-release after $attempts attempts" >&2
		exit 1
	fi
	echo "attempt $attempt/$attempts failed; retrying in ${sleep_seconds}s" >&2
	if [ "$sleep_seconds" -gt 0 ]; then
		sleep "$sleep_seconds"
	fi
	attempt=$((attempt + 1))
done
