#!/bin/sh
# Compare Go comment counts with the committed baseline.
# When BASE_SHA has a baseline, a count may not rise above that revision.
set -eu

cd -- "$(dirname "$0")/.."
baseline=internal/tools/commentcheck/baseline.txt
base_sha=${BASE_SHA:-}
zero=0000000000000000000000000000000000000000
tmp=

cleanup() {
	if [ -n "$tmp" ]; then
		rm -f "$tmp"
	fi
}
trap cleanup EXIT

if [ -n "$base_sha" ] && [ "$base_sha" != "$zero" ] && git cat-file -e "${base_sha}:${baseline}" 2>/dev/null; then
	tmp=$(mktemp)
	git show "${base_sha}:${baseline}" > "$tmp"
	go run ./internal/tools/commentcheck -baseline "$baseline" -ratchet "$tmp"
else
	go run ./internal/tools/commentcheck -baseline "$baseline"
fi
