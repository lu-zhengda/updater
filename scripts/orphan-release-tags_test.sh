#!/bin/sh
# v0.24.1 is the real tag that has no Release. After v0.24.2 existed, that
# tag was no longer the in-progress publish. The checker must fail on that
# pair unless the tag is allowlisted, and must pass once it is.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
checker=$root/scripts/orphan-release-tags.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

run() {
	name=$1
	ORPHAN_TAGS=$2
	ORPHAN_RELEASES=$3
	ORPHAN_ALLOWLIST=$4
	shift 4
	export ORPHAN_TAGS ORPHAN_RELEASES ORPHAN_ALLOWLIST
	set +e
	sh "$checker" >"$tmp/$name.out" 2>"$tmp/$name.err"
	status=$?
	set -e
	unset ORPHAN_TAGS ORPHAN_RELEASES ORPHAN_ALLOWLIST
	if [ "$status" -ne "$1" ]; then
		echo "exit $status want $1" >&2
		cat "$tmp/$name.out" "$tmp/$name.err" >&2
		fail "$name"
	fi
	if [ -n "${2:-}" ] && ! grep -F "$2" "$tmp/$name.out" "$tmp/$name.err" >/dev/null; then
		cat "$tmp/$name.out" "$tmp/$name.err" >&2
		fail "$name missing $2"
	fi
	echo "ok: $name"
}

printf '%s\n' v0.24.1 >"$tmp/allow.txt"
: >"$tmp/empty"
run historical-orphan 'v0.24.0
v0.24.1
v0.24.2' 'v0.24.0
v0.24.2' "$tmp/empty" 1 "v0.24.1"
run allowlisted 'v0.24.0
v0.24.1
v0.24.2' 'v0.24.0
v0.24.2' "$tmp/allow.txt" 0 "allowlisted"
run in-progress 'v0.24.0
v0.24.1' 'v0.24.0' "$tmp/empty" 0 "newest tag"
run new-orphan 'v0.24.3
v0.24.5
v0.24.6' 'v0.24.3
v0.24.6' "$tmp/allow.txt" 1 "v0.24.5"

if [ -f "$root/scripts/known-orphan-tags.txt" ] &&
	grep -Fxq 'v0.24.1' "$root/scripts/known-orphan-tags.txt"; then
	echo "ok: allowlist names v0.24.1"
else
	fail "known-orphan-tags.txt dropped v0.24.1"
fi

echo "orphan release tag tests ok"
