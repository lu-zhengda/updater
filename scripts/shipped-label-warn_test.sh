#!/bin/sh
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/shipped-label-warn.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

run() {
	name=$1
	list=$2
	labels=$3
	want=$4
	printf '%s\n' "$list" >"$tmp/files"
	set +e
	PR_LABELS=$labels sh "$script" "$tmp/files" >"$tmp/$name.out" 2>"$tmp/$name.err"
	status=$?
	set -e
	if [ "$status" -ne 0 ]; then
		fail "$name exited $status"
	fi
	if [ "$want" = warn ]; then
		grep -F '::warning::' "$tmp/$name.out" >/dev/null || fail "$name did not warn"
	elif grep -F '::warning::' "$tmp/$name.out" >/dev/null; then
		cat "$tmp/$name.out" >&2
		fail "$name warned"
	fi
	echo "ok: $name"
}

run shipped-unlabeled $'.goreleaser.yaml\ndocs/RELEASING.md' '' warn
run docs-only $'docs/lessons.md\n.cursor/skills/verify-updater/SKILL.md' '' quiet
run labeled $'scripts/mkdmg.sh' 'semver:patch' quiet
run tests-only $'scripts/mkdmg_test.sh\ninternal/checker/github_test.go' '' quiet
run release-workflow $'.github/workflows/release-publish.yml' '' warn

echo "shipped label warning tests ok"
