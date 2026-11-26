#!/bin/sh
# Check a pull request title and body.
# PR_TITLE, PR_BODY, and PR_AUTHOR come from the workflow event.
# dependabot[bot] is exempt.
set -eu

author=${PR_AUTHOR:-}
if [ "$author" = "dependabot[bot]" ]; then
	echo "pr-standard: dependabot[bot] is exempt"
	exit 0
fi

title=$(printf '%s' "${PR_TITLE:-}" | tr -d '\r')
body=$(printf '%s' "${PR_BODY-}" | tr -d '\r')
types='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
title_re="^(${types})\\([0-9A-Za-z._/-]+\\): [^[:space:]].*$"

fail() {
	echo "pr-standard: $*" >&2
	exit 1
}

printf '%s\n' "$title" | grep -Eq "$title_re" || fail "title must match type(scope): subject"

lines=$(printf '%s' "$body" | awk 'END { print NR + 0 }')
if [ "$lines" -gt 40 ]; then
	fail "body has $lines lines, the maximum is 40"
fi

require_line() {
	printf '%s\n' "$body" | grep -Eq "^$1$" || fail "body is missing a line matching $1"
}

require_line '## Why'
require_line '## What changed'
require_line '## Blast Radius'
require_line '## Verification'
require_line 'Fixes ZHE-[0-9]+'

echo "pr-standard: ok"
