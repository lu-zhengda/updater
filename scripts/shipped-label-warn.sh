#!/bin/sh
# Warn when a pull request changes shipped code and has no semver label.
# The release workflow treats that as success and publishes nothing.
# This is an annotation, not a failure and not an approval gate.
#
# Usage: shipped-label-warn.sh <changed-files>
# PR_LABELS is a comma-separated label list. Exit is always 0.
# shellcheck shell=sh
set -eu

files=${1:-}
labels=${PR_LABELS:-}
shipped_list=${TMPDIR:-/tmp}/shipped-label-warn.$$
: >"$shipped_list"
trap 'rm -f "$shipped_list"' EXIT

shipped=0
if [ -n "$files" ] && [ -f "$files" ]; then
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		case $path in
		*_test.go | *.md | .github/workflows/ci.yml | .cursor/* | AGENTS.md | scripts/*_test.sh | scripts/cask-shape.sh | scripts/cask-checksum.sh | scripts/orphan-release-tags.sh | scripts/shipped-label-warn.sh | scripts/known-orphan-tags.txt)
			;;
		*.go | go.mod | go.sum | .goreleaser.yaml | Makefile | packaging/* | scripts/mkapp.sh | scripts/mkdmg.sh | scripts/notarize-app.sh | scripts/completions.sh | scripts/release-preflight.sh | .github/workflows/release-publish.yml | .github/workflows/release-on-merge.yml | .github/workflows/release.yml)
			shipped=1
			printf '%s\n' "$path" >>"$shipped_list"
			;;
		esac
	done <"$files"
fi

semver=$(printf '%s\n' "$labels" | tr ',' '\n' | grep -c -E '^semver:(patch|minor|major)$' || true)
if [ "$shipped" -eq 1 ] && [ "$semver" -eq 0 ]; then
	echo "::warning::This pull request changes shipped code and has no semver label, so merging it publishes nothing. Add exactly one of semver:patch, semver:minor, or semver:major."
	echo "warning: shipped files have no semver label:"
	cat "$shipped_list"
fi
exit 0
