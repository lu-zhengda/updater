#!/bin/sh
# Choose a release tag from the pull request's semver labels.
#
# PR_LABELS          Comma-separated label names from the pull request.
# MERGE_SHA          Merge commit to tag. Must be HEAD when a release is published.
# PLAN_RELEASE_PUSH  1 (default) pushes the tag to origin. 0 keeps it local.
# GITHUB_OUTPUT      When set, receives publish=true|false and, when publishing, tag.
#
# No semver label exits 0 without a tag. More than one of semver:patch,
# semver:minor, and semver:major exits 1. Exactly one bumps the latest stable
# vX.Y.Z tag. An existing tag, or a merge commit that already has any tag,
# exits 1. The push refspec is not a force update.
set -eu

write_output() {
	key=$1
	value=$2
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		printf '%s=%s\n' "$key" "$value" >> "$GITHUB_OUTPUT"
	fi
}

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

is_stable_tag() {
	case $1 in
	v*.*.*) ;;
	*)
		return 1
		;;
	esac
	rest=${1#v}
	major=${rest%%.*}
	rest=${rest#*.}
	minor=${rest%%.*}
	patch=${rest#*.}
	case $patch in
	*.*)
		return 1
		;;
	esac
	is_component "$major" && is_component "$minor" && is_component "$patch"
}

read_labels() {
	semver_count=0
	semver_bump=
	labels=${PR_LABELS:-}
	if [ -z "$labels" ]; then
		return 0
	fi
	saved_ifs=$IFS
	IFS=','
	set -f
	for raw in $labels; do
		label=$(printf '%s' "$raw" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
		case $label in
		semver:patch | semver:minor | semver:major)
			semver_count=$((semver_count + 1))
			semver_bump=${label#semver:}
			;;
		esac
	done
	set +f
	IFS=$saved_ifs
}

select_latest_tag() {
	tags=$(git tag -l --sort=-v:refname)
	if [ -z "$tags" ]; then
		return 1
	fi
	found=
	saved_ifs=$IFS
	# Command substitution would strip this newline, so keep it literal.
	IFS='
'
	set -f
	for name in $tags; do
		if is_stable_tag "$name"; then
			found=$name
			break
		fi
	done
	set +f
	IFS=$saved_ifs
	if [ -z "$found" ]; then
		return 1
	fi
	printf '%s\n' "$found"
}

bump_version() {
	current=$1
	kind=$2
	rest=${current#v}
	major=${rest%%.*}
	rest=${rest#*.}
	minor=${rest%%.*}
	patch=${rest#*.}
	case $kind in
	major)
		major=$((major + 1))
		minor=0
		patch=0
		;;
	minor)
		minor=$((minor + 1))
		patch=0
		;;
	patch)
		patch=$((patch + 1))
		;;
	*)
		echo "unknown bump kind" >&2
		return 1
		;;
	esac
	printf 'v%s.%s.%s\n' "$major" "$minor" "$patch"
}

refuse_existing_tag() {
	tag=$1
	if git show-ref --verify --quiet "refs/tags/${tag}"; then
		echo "tag ${tag} already exists; refusing to move it" >&2
		exit 1
	fi
}

main() {
	read_labels
	if [ "$semver_count" -eq 0 ]; then
		echo "no semver label; not publishing"
		write_output publish false
		exit 0
	fi
	if [ "$semver_count" -ne 1 ] || [ -z "$semver_bump" ]; then
		echo "refusing to publish: ${semver_count} semver labels (need exactly one of semver:patch, semver:minor, semver:major)" >&2
		exit 1
	fi

	if [ -z "${MERGE_SHA:-}" ]; then
		echo "MERGE_SHA is empty; refusing to publish" >&2
		exit 1
	fi
	if ! git cat-file -e "${MERGE_SHA}^{commit}" 2>/dev/null; then
		echo "MERGE_SHA is not a commit; refusing to publish" >&2
		exit 1
	fi
	target=$(git rev-parse "${MERGE_SHA}^{commit}")
	head=$(git rev-parse HEAD)
	if [ "$head" != "$target" ]; then
		echo "refusing to tag ${target}; checkout is ${head}" >&2
		exit 1
	fi

	do_push=${PLAN_RELEASE_PUSH:-1}
	if [ "$do_push" != "0" ] && [ "$do_push" != "1" ]; then
		echo "PLAN_RELEASE_PUSH must be 0 or 1" >&2
		exit 1
	fi
	if [ "$do_push" = "1" ]; then
		if ! git remote get-url origin >/dev/null 2>&1; then
			echo "origin is required to push the release tag" >&2
			exit 1
		fi
		GIT_TERMINAL_PROMPT=0
		export GIT_TERMINAL_PROMPT
		git fetch origin --tags
	fi

	if ! latest=$(select_latest_tag); then
		echo "no stable vX.Y.Z tag to bump; refusing to publish" >&2
		exit 1
	fi
	tag=$(bump_version "$latest" "$semver_bump")
	if ! is_stable_tag "$tag"; then
		echo "refusing to create unexpected tag" >&2
		exit 1
	fi
	refuse_existing_tag "$tag"

	existing=$(git tag --points-at "$target")
	if [ -n "$existing" ]; then
		echo "merge commit ${target} is already tagged; refusing to add another tag" >&2
		printf '%s\n' "$existing" >&2
		exit 1
	fi

	git config user.name "github-actions[bot]"
	git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
	git config tag.gpgsign false
	git tag -a "$tag" "$target" -m "Release $tag"
	if [ "$(git cat-file -t "$tag")" != "tag" ]; then
		echo "refusing to publish a lightweight tag" >&2
		exit 1
	fi
	if [ "$(git rev-parse "${tag}^{commit}")" != "$target" ]; then
		echo "annotated tag does not point at the merge commit" >&2
		exit 1
	fi

	if [ "$do_push" = "1" ]; then
		# Explicit refspec. A rejected update fails the run.
		git push origin "refs/tags/${tag}:refs/tags/${tag}"
		remote_line=$(git ls-remote origin "refs/tags/${tag}")
		remote_sha=$(printf '%s\n' "$remote_line" | cut -f1)
		local_sha=$(git rev-parse "$tag")
		if [ -z "$remote_sha" ] || [ "$remote_sha" != "$local_sha" ]; then
			echo "remote tag ${tag} does not match the annotated tag" >&2
			exit 1
		fi
	fi

	echo "tagged ${tag} at ${target} (${semver_bump} from ${latest})"
	write_output publish true
	write_output tag "$tag"
}

case ${0##*/} in
plan-release-tag.sh)
	main "$@"
	;;
esac
