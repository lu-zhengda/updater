#!/bin/sh
# Exercises scripts/pr-standard.sh and the CI trigger contract.
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/pr-standard.sh
ci=$root/.github/workflows/ci.yml

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ok() {
	echo "ok: $*"
}

run_check() {
	if sh "$script"; then
		return 0
	fi
	return 1
}

good_body='## Why

The gate was missing.

## What changed

One check.

## Blast Radius

CI only.

## Verification

sh scripts/pr-standard_test.sh

Fixes ZHE-135'

PR_AUTHOR= PR_TITLE='ci(lint): add the gates' PR_BODY="$good_body" run_check || fail "valid pull request text"
ok "valid title and body"

PR_AUTHOR='dependabot[bot]' PR_TITLE='bump something' PR_BODY='' run_check || fail "dependabot should be exempt"
ok "dependabot[bot] is exempt"

if PR_AUTHOR= PR_TITLE='add the gates' PR_BODY="$good_body" run_check; then
	fail "title without type and scope should fail"
fi
ok "loose title fails"

if PR_AUTHOR= PR_TITLE='wip(lint): add the gates' PR_BODY="$good_body" run_check; then
	fail "unknown type should fail"
fi
ok "unknown type fails"

if PR_AUTHOR= PR_TITLE='ci(lint): add the gates' PR_BODY='## Why

## What changed

## Blast Radius

## Verification' run_check; then
	fail "missing Fixes line should fail"
fi
ok "missing Fixes line fails"

long=$(printf '%s\n' "$good_body")
i=0
while [ "$i" -lt 30 ]; do
	long="${long}
extra"
	i=$((i + 1))
done
if PR_AUTHOR= PR_TITLE='ci(lint): add the gates' PR_BODY="$long" run_check; then
	fail "a body over 40 lines should fail"
fi
ok "long body fails"

grep -q 'types: \[opened, synchronize, reopened, edited\]' "$ci" || fail "pull_request types drifted"
grep -q 'branches: \[main\]' "$ci" || fail "CI branches are not main"
if grep -Eq '^[[:space:]]*(schedule|cron):' "$ci"; then
	fail "CI must not declare a schedule"
fi
grep -q "github.event_name == 'pull_request'" "$ci" || fail "PR text check is not limited to pull requests"
if grep -q 'github.event.action ==' "$ci"; then
	fail "PR text check must run on every pull_request event, including reopened"
fi
grep -q 'golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a' "$ci" || fail "golangci-lint action pin drifted"
grep -q 'scripts/comment-ratchet.sh' "$ci" || fail "comment ratchet is not in CI"
grep -q 'scripts/pr-standard.sh' "$ci" || fail "PR standard check is not in CI"
jobs=$(awk '
	/^jobs:/ { in_jobs = 1; next }
	in_jobs && /^[^ ]/ { in_jobs = 0 }
	in_jobs && /^  [a-z0-9-]+:$/ { n++ }
	END { print n + 0 }
' "$ci")
if [ "$jobs" != 2 ]; then
	fail "CI job count is $jobs, want the existing two jobs"
fi
ok "CI trigger and job contract"

echo "pr-standard tests passed"
