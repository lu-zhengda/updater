#!/bin/sh
# Fail when a vX.Y.Z tag has no GitHub Release.
#
# The greatest unreleased tag is ignored: release-on-merge creates the tag
# before the Release exists. Every older unreleased tag is a failed publish
# that was left behind. v0.24.1 is listed in scripts/known-orphan-tags.txt
# because that tag stays.
#
# Tests set ORPHAN_TAGS and ORPHAN_RELEASES to newline-separated tag names
# and ORPHAN_ALLOWLIST to a file. Without those, this reads origin and GitHub.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
allowlist=${ORPHAN_ALLOWLIST:-$root/scripts/known-orphan-tags.txt}
repo=${ORPHAN_REPO:-lu-zhengda/updater}

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

max_version() {
	printf '%s\n' "$1" | sed '/^$/d' | while IFS= read -r tag; do
		printf '%s\t%s\n' "${tag#v}" "$tag"
	done | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1 | awk -F '	' '{print $2}'
}

greater() {
	top=$(max_version "$(printf '%s\n%s\n' "$1" "$2")")
	[ "$top" = "$1" ] && [ "$1" != "$2" ]
}

read_allowlist() {
	if [ ! -f "$allowlist" ]; then
		return 0
	fi
	sed -e 's/#.*//' -e '/^[[:space:]]*$/d' "$allowlist"
}

if [ -n "${ORPHAN_TAGS:-}" ]; then
	tags=$(printf '%s\n' "$ORPHAN_TAGS" | sed '/^$/d')
	releases=$(printf '%s\n' "${ORPHAN_RELEASES:-}" | sed '/^$/d')
else
	tags=$(git ls-remote --tags "https://github.com/$repo.git" |
		awk '{print $2}' |
		sed -n 's#refs/tags/\(v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)$#\1#p' |
		sort -u)
	releases=$(gh api --paginate "repos/$repo/releases?per_page=100" --jq '.[].tag_name' | sed '/^$/d')
fi

allowed=$(read_allowlist || true)
unreleased=$(printf '%s\n' "$tags" | while IFS= read -r tag; do
	[ -n "$tag" ] || continue
	if printf '%s\n' "$releases" | grep -Fxq "$tag"; then
		continue
	fi
	if printf '%s\n' "$allowed" | grep -Fxq "$tag"; then
		continue
	fi
	printf '%s\n' "$tag"
done)

if [ -z "$unreleased" ]; then
	echo "every tag has a Release or is allowlisted"
	exit 0
fi

max_unreleased=$(max_version "$unreleased")
max_released=$(max_version "$releases")
if [ -n "$max_released" ] && greater "$max_unreleased" "$max_released"; then
	unreleased=$(printf '%s\n' "$unreleased" | grep -Fxv "$max_unreleased" || true)
fi

if [ -z "$unreleased" ]; then
	echo "unreleased $max_unreleased is the newest tag; publish may still be running"
	exit 0
fi

echo "tags with no GitHub Release:" >&2
printf '%s\n' "$unreleased" >&2
fail "a tag was published without a Release"
