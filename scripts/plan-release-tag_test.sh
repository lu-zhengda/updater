#!/bin/sh
# Exercises scripts/plan-release-tag.sh against temporary repositories.
# local is a dash and bash builtin. This file is executed with sh.
# shellcheck shell=sh disable=SC3043
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/plan-release-tag.sh
unset GIT_DIR GIT_WORK_TREE || true

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
repo_n=0

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ok() {
	echo "ok: $*"
}

new_repo() {
	repo_n=$((repo_n + 1))
	repo=$tmp/repo-$repo_n
	git init -b main "$repo" >/dev/null
	git -C "$repo" config user.name "Test User"
	git -C "$repo" config user.email "test@example.com"
	git -C "$repo" config commit.gpgsign false
	git -C "$repo" config tag.gpgsign false
	git -C "$repo" commit --allow-empty -m init >/dev/null
}

commit_empty() {
	git -C "$1" commit --allow-empty -m "$2" >/dev/null
	git -C "$1" rev-parse HEAD
}

run_plan() {
	local repo="$1"
	local out="$2"
	shift 2
	: > "$out"
	(
		cd "$repo"
		env "$@" GITHUB_OUTPUT="$out" sh "$script"
	)
}

assert_output() {
	local out="$1"
	local key="$2"
	local want="$3"
	local got
	got=$(grep "^${key}=" "$out" | head -n 1 | cut -d= -f2-)
	if [ "$got" != "$want" ]; then
		echo "output ${key}: got [${got}] want [${want}]" >&2
		echo "---- output file ----" >&2
		cat "$out" >&2
		fail "output mismatch"
	fi
}

assert_no_key() {
	local out="$1"
	local key="$2"
	if grep -q "^${key}=" "$out"; then
		cat "$out" >&2
		fail "did not expect ${key} in output"
	fi
}

# --- pure helpers ---

if ! (
	# shellcheck disable=SC1090
	. "$script"
	is_stable_tag v0.23.0 &&
		is_stable_tag v0.0.0 &&
		is_stable_tag v10.20.30 &&
		! is_stable_tag v0.23.0-rc.1 &&
		! is_stable_tag v0.23 &&
		! is_stable_tag v1.2 &&
		! is_stable_tag v0.01.0 &&
		! is_stable_tag v0.23.0.1 &&
		! is_stable_tag video &&
		[ "$(bump_version v0.23.0 patch)" = "v0.23.1" ] &&
		[ "$(bump_version v0.23.0 minor)" = "v0.24.0" ] &&
		[ "$(bump_version v0.23.0 major)" = "v1.0.0" ] &&
		[ "$(bump_version v0.20.3 patch)" = "v0.20.4" ] &&
		[ "$(bump_version v0.20.3 minor)" = "v0.21.0" ] &&
		[ "$(bump_version v1.2.3 major)" = "v2.0.0" ]
); then
	fail "version helpers"
fi
ok "version helpers"

new_repo
git -C "$repo" tag v0.9.0
git -C "$repo" tag v0.10.0
git -C "$repo" tag v0.23.0
git -C "$repo" tag v0.24.0-rc.1
git -C "$repo" tag v1.2
git -C "$repo" tag video-export
git -C "$repo" tag v0.01.0
latest=$(
	# shellcheck disable=SC1090
	. "$script"
	cd "$repo"
	select_latest_tag
)
if [ "$latest" != "v0.23.0" ]; then
	fail "latest stable tag was ${latest}"
fi
ok "latest stable tag ignores prerelease and non-semver names"

# --- no semver label is a success and creates nothing ---

new_repo
git -C "$repo" tag v0.23.0
before=$(git -C "$repo" tag -l | sort)
out=$tmp/out-none
run_plan "$repo" "$out" PR_LABELS= PLAN_RELEASE_PUSH=0 MERGE_SHA="$(git -C "$repo" rev-parse HEAD)"
assert_output "$out" publish false
assert_no_key "$out" tag
after=$(git -C "$repo" tag -l | sort)
if [ "$before" != "$after" ]; then
	fail "no-label run changed tags"
fi
ok "no semver label does not publish"

new_repo
out=$tmp/out-docs
run_plan "$repo" "$out" PR_LABELS='documentation,bug' PLAN_RELEASE_PUSH=0 MERGE_SHA="$(git -C "$repo" rev-parse HEAD)"
assert_output "$out" publish false
ok "unrelated labels do not publish"

new_repo
out=$tmp/out-glob
run_plan "$repo" "$out" PR_LABELS='*' PLAN_RELEASE_PUSH=0 MERGE_SHA="$(git -C "$repo" rev-parse HEAD)"
assert_output "$out" publish false
ok "glob label does not publish"

# --- one label bumps from the latest stable tag ---

expect_bump() {
	local label="$1"
	local want="$2"
	local sha
	local out
	new_repo
	git -C "$repo" tag v0.9.0
	git -C "$repo" tag v0.23.0
	git -C "$repo" tag v0.24.0-rc.1
	sha=$(commit_empty "$repo" merge)
	out=$tmp/out-$label
	run_plan "$repo" "$out" PR_LABELS="$label,enhancement" PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"
	assert_output "$out" publish true
	assert_output "$out" tag "$want"
	if [ "$(git -C "$repo" cat-file -t "$want")" != "tag" ]; then
		fail "$want is not annotated"
	fi
	if [ "$(git -C "$repo" rev-parse "${want}^{commit}")" != "$sha" ]; then
		fail "$want does not point at the merge commit"
	fi
	if [ "$(git -C "$repo" tag -l --format='%(contents:subject)' "$want")" != "Release $want" ]; then
		fail "$want message"
	fi
	if [ "$(git -C "$repo" tag -l --format='%(taggername)' "$want")" != "github-actions[bot]" ]; then
		fail "$want tagger"
	fi
	ok "$label creates annotated $want"
}

expect_bump semver:patch v0.23.1
expect_bump semver:minor v0.24.0
expect_bump semver:major v1.0.0

new_repo
git -C "$repo" tag v0.9.0
git -C "$repo" tag v0.10.0
sha=$(commit_empty "$repo" merge)
out=$tmp/out-sort
run_plan "$repo" "$out" PR_LABELS='semver:patch' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"
assert_output "$out" tag v0.10.1
ok "version sort picks v0.10.0 over v0.9.0"

# --- fail closed ---

new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
out=$tmp/out-two
if run_plan "$repo" "$out" PR_LABELS='semver:minor,semver:patch' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"; then
	fail "two semver labels should fail"
fi
if git -C "$repo" rev-parse -q --verify refs/tags/v0.24.0 >/dev/null; then
	fail "two labels created a tag"
fi
ok "two semver labels fail closed"

new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
out=$tmp/out-three
if run_plan "$repo" "$out" PR_LABELS='semver:major, semver:minor, semver:patch' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"; then
	fail "three semver labels should fail"
fi
ok "three semver labels fail closed"

new_repo
git -C "$repo" tag v0.23.0
sha=$(git -C "$repo" rev-parse HEAD)
out=$tmp/out-already
if run_plan "$repo" "$out" PR_LABELS='semver:patch' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"; then
	fail "already-tagged commit should fail"
fi
if git -C "$repo" rev-parse -q --verify refs/tags/v0.23.1 >/dev/null; then
	fail "already-tagged commit created the next tag"
fi
ok "already-tagged merge commit fails closed"

new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
out=$tmp/out-not-head
parent=$(git -C "$repo" rev-parse HEAD^)
if run_plan "$repo" "$out" PR_LABELS='semver:minor' PLAN_RELEASE_PUSH=0 MERGE_SHA="$parent"; then
	fail "tagging a commit that is not HEAD should fail"
fi
ok "refuses to tag a commit that is not checked out"

new_repo
sha=$(commit_empty "$repo" merge)
out=$tmp/out-notag
if run_plan "$repo" "$out" PR_LABELS='semver:minor' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"; then
	fail "missing base tag should fail"
fi
ok "no stable tag fails closed"

new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
out=$tmp/out-empty-sha
if run_plan "$repo" "$out" PR_LABELS='semver:minor' PLAN_RELEASE_PUSH=0 MERGE_SHA=; then
	fail "empty MERGE_SHA should fail"
fi
ok "empty MERGE_SHA fails closed"

new_repo
git -C "$repo" tag v0.24.0
kept=$(git -C "$repo" rev-parse v0.24.0)
sha=$(commit_empty "$repo" merge)
out=$tmp/out-keep
run_plan "$repo" "$out" PR_LABELS='semver:patch' PLAN_RELEASE_PUSH=0 MERGE_SHA="$sha"
assert_output "$out" tag v0.24.1
if [ "$(git -C "$repo" rev-parse v0.24.0)" != "$kept" ]; then
	fail "existing tag was moved"
fi
ok "creating the next tag does not move the previous tag"

new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
# The guard used when the computed name is already present.
if (
	# shellcheck disable=SC1090
	. "$script"
	cd "$repo"
	git tag v0.24.0 "$sha"
	refuse_existing_tag v0.24.0
); then
	fail "existing computed tag should be refused"
fi
ok "existing computed tag is refused"

# --- push to a local origin, and do not force past a rejection ---

remote=$tmp/remote.git
git init --bare -b main "$remote" >/dev/null
new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
git -C "$repo" remote add origin "$remote"
git -C "$repo" push origin main >/dev/null
git -C "$repo" push origin refs/tags/v0.23.0:refs/tags/v0.23.0 >/dev/null
out=$tmp/out-push
run_plan "$repo" "$out" PR_LABELS='semver:minor' PLAN_RELEASE_PUSH=1 MERGE_SHA="$sha"
assert_output "$out" tag v0.24.0
local_obj=$(git -C "$repo" rev-parse v0.24.0)
remote_obj=$(git ls-remote "$remote" refs/tags/v0.24.0 | cut -f1)
if [ "$local_obj" != "$remote_obj" ]; then
	fail "pushed tag object does not match"
fi
if [ "$(git -C "$repo" rev-parse 'v0.24.0^{commit}')" != "$sha" ]; then
	fail "pushed tag does not point at the merge commit"
fi
ok "push creates the annotated tag on origin"

remote=$tmp/remote-reject.git
git init --bare -b main "$remote" >/dev/null
new_repo
git -C "$repo" tag v0.23.0
sha=$(commit_empty "$repo" merge)
git -C "$repo" remote add origin "$remote"
git -C "$repo" push origin main >/dev/null
git -C "$repo" push origin refs/tags/v0.23.0:refs/tags/v0.23.0 >/dev/null
hook=$remote/hooks/pre-receive
cat > "$hook" << 'EOF'
#!/bin/sh
echo "rejected by test hook" >&2
exit 1
EOF
chmod +x "$hook"
out=$tmp/out-reject
if run_plan "$repo" "$out" PR_LABELS='semver:patch' PLAN_RELEASE_PUSH=1 MERGE_SHA="$sha"; then
	fail "rejected push should fail the planner"
fi
if git ls-remote "$remote" refs/tags/v0.23.1 | grep -q .; then
	fail "rejected push updated the remote"
fi
if [ "$(git ls-remote "$remote" refs/tags/v0.23.0 | cut -f1)" != "$(git -C "$repo" rev-parse v0.23.0)" ]; then
	fail "rejected push moved the existing tag"
fi
ok "rejected push fails and does not update the remote"

# --- workflow contract ---

wf=$root/.github/workflows
for f in release.yml release-publish.yml release-on-merge.yml; do
	if [ ! -f "$wf/$f" ]; then
		fail "missing $f"
	fi
done
grep -q 'tags:' "$wf/release.yml" || fail "release.yml missing tag trigger"
grep -q 'v\*' "$wf/release.yml" || fail "release.yml missing v* filter"
grep -q 'workflow_call:' "$wf/release-publish.yml" || fail "publish workflow is not callable"
grep -q 'fetch-depth: 0' "$wf/release-publish.yml" || fail "publish checkout is not full history"
grep -q 'macos-latest' "$wf/release-publish.yml" || fail "publish job left macos-latest"
grep -q 'goreleaser/goreleaser-action@a4f02ab5355a589de60d917a659ce617ed19b3de' "$wf/release-publish.yml" || fail "GoReleaser action pin changed"
grep -q 'types: \[closed\]' "$wf/release-on-merge.yml" || fail "merge workflow is not pull_request closed"
grep -q 'pull_request:' "$wf/release-on-merge.yml" || fail "merge workflow is not a pull_request workflow"
if grep -n '^[[:space:]]*environment:' "$wf/release.yml" "$wf/release-publish.yml" "$wf/release-on-merge.yml"; then
	fail "release workflows must not set a GitHub Environment"
fi
if grep -n -- '--force' "$script" "$wf/release.yml" "$wf/release-publish.yml" "$wf/release-on-merge.yml"; then
	fail "release automation contains --force"
fi
if grep -n 'secrets: inherit' "$wf/release.yml" "$wf/release-publish.yml" "$wf/release-on-merge.yml"; then
	fail "release workflows must pass the existing secret names explicitly"
fi
allowed=$(
	printf '%s\n' \
		APPSTORE_API_KEY_ID \
		APPSTORE_API_KEY_P8_BASE64 \
		APPSTORE_API_ISSUER_ID \
		GITHUB_TOKEN \
		HOMEBREW_TAP_TOKEN \
		MACOS_CERT_PASSWORD \
		MACOS_CERT_P12_BASE64 | LC_ALL=C sort
)
found=$(grep -hEo 'secrets\.[A-Z0-9_]+' "$wf/release.yml" "$wf/release-publish.yml" "$wf/release-on-merge.yml" | sed 's/^secrets\.//' | LC_ALL=C sort -u)
if [ "$found" != "$allowed" ]; then
	echo "secret names:" >&2
	printf '%s\n' "$found" >&2
	fail "unexpected secret name"
fi
# release-dry-run.yml is still on main until PR 25 deletes it. This branch
# must not add another copy under a new name or change that file.
if git diff --name-only "$(git merge-base HEAD origin/main 2>/dev/null || echo HEAD)" HEAD -- .github/workflows/release-dry-run.yml | grep -q .; then
	fail "this branch changes release-dry-run.yml; PR 25 owns that file"
fi
ok "workflow contract"

echo "all release planner tests passed"
