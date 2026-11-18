#!/bin/sh
# Proves verify-published-release.sh fails closed on a stale cask, a bad
# tarball URL, a non-200 download, and a body that is not larger than 1 MiB.
# Curl is a fixture. No network and no credentials.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
script=$root/scripts/verify-published-release.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

log=$tmp/curl.log
count=$tmp/curl.count
bin=$tmp/bin
mkdir -p "$bin"

cat >"$tmp/good.rb" <<'EOF'
cask "updater" do
  version "0.25.0"
  url "https://github.com/lu-zhengda/updater/releases/download/v#{version}/updater_#{version}_darwin.tar.gz"
end
EOF
cat >"$tmp/old.rb" <<'EOF'
cask "updater" do
  version "0.24.9"
  url "https://github.com/lu-zhengda/updater/releases/download/v#{version}/updater_#{version}_darwin.tar.gz"
end
EOF
cat >"$tmp/bad-url.rb" <<'EOF'
cask "updater" do
  version "0.25.0"
  url "https://github.com/lu-zhengda/updater/releases/download/v#{version}/not-the-asset.tar.gz"
end
EOF

cat >"$bin/curl" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FIX_LOG"
if [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ] || [ -n "${HOMEBREW_TAP_TOKEN:-}" ] || [ -n "${GITHUB_ENTERPRISE_TOKEN:-}" ]; then
	printf '%s\n' leak >>"$FIX_LOG"
	exit 9
fi
out=
prev=
for arg in "$@"; do
	if [ "$prev" = "-o" ]; then
		out=$arg
	fi
	prev=$arg
done
url=$prev
if [ -z "${out:-}" ]; then
	echo "fixture curl missing -o" >&2
	exit 2
fi
write_big() {
	dd if=/dev/zero of="$out" bs=1048577 count=1 >/dev/null 2>&1
	printf '%s' 200
}
case $url in
*Casks/updater.rb)
	case $FIX_MODE in
	cask-404)
		printf '%s' nope >"$out"
		printf '%s' 404
		;;
	retry-version)
		n=0
		if [ -f "$FIX_COUNT" ]; then
			n=$(cat "$FIX_COUNT")
		fi
		n=$((n + 1))
		printf '%s\n' "$n" >"$FIX_COUNT"
		if [ "$n" -eq 1 ]; then
			cp "$FIX_OLD" "$out"
		else
			cp "$FIX_GOOD" "$out"
		fi
		printf '%s' 200
		;;
	bad-version)
		cp "$FIX_OLD" "$out"
		printf '%s' 200
		;;
	bad-url)
		cp "$FIX_BAD_URL" "$out"
		printf '%s' 200
		;;
	*)
		cp "$FIX_GOOD" "$out"
		printf '%s' 200
		;;
	esac
	;;
*_darwin.tar.gz)
	case $FIX_MODE in
	tarball-404)
		printf '%s' missing >"$out"
		printf '%s' 404
		;;
	curl-fail)
		exit 28
		;;
	*)
		write_big
		;;
	esac
	;;
*.dmg)
	case $FIX_MODE in
	dmg-small)
		printf '%s' tiny >"$out"
		printf '%s' 200
		;;
	dmg-exact)
		dd if=/dev/zero of="$out" bs=1048576 count=1 >/dev/null 2>&1
		printf '%s' 200
		;;
	curl-fail)
		exit 28
		;;
	*)
		write_big
		;;
	esac
	;;
*)
	echo "fixture curl unexpected url" >&2
	exit 9
	;;
esac
EOF
chmod 755 "$bin/curl"

export PATH="$bin:$PATH"
export FIX_LOG=$log
export FIX_COUNT=$count
export FIX_GOOD=$tmp/good.rb
export FIX_OLD=$tmp/old.rb
export FIX_BAD_URL=$tmp/bad-url.rb

run() {
	name=$1
	mode=$2
	tag=$3
	want=$4
	needle=$5
	attempts=${6:-}
	rm -f "$log" "$count"
	export FIX_MODE=$mode
	set +e
	if [ -n "$attempts" ]; then
		VERIFY_PUBLISHED_ATTEMPTS=$attempts VERIFY_PUBLISHED_SLEEP=0 \
			GITHUB_TOKEN=super-secret GH_TOKEN=super-secret \
			HOMEBREW_TAP_TOKEN=tap-secret GITHUB_ENTERPRISE_TOKEN=super-secret \
			sh "$script" "$tag" >"$tmp/$name.out" 2>"$tmp/$name.err"
	else
		GITHUB_TOKEN=super-secret GH_TOKEN=super-secret \
			HOMEBREW_TAP_TOKEN=tap-secret GITHUB_ENTERPRISE_TOKEN=super-secret \
			sh "$script" "$tag" >"$tmp/$name.out" 2>"$tmp/$name.err"
	fi
	status=$?
	set -e
	if [ "$status" -ne "$want" ]; then
		echo "---- stdout ----" >&2
		cat "$tmp/$name.out" >&2
		echo "---- stderr ----" >&2
		cat "$tmp/$name.err" >&2
		fail "$name exited $status, want $want"
	fi
	if [ -n "$needle" ] && ! grep -F "$needle" "$tmp/$name.out" "$tmp/$name.err" >/dev/null; then
		echo "---- stdout ----" >&2
		cat "$tmp/$name.out" >&2
		echo "---- stderr ----" >&2
		cat "$tmp/$name.err" >&2
		fail "$name did not report: $needle"
	fi
	if grep -F 'super-secret' "$tmp/$name.out" "$tmp/$name.err" >/dev/null; then
		fail "$name printed a token"
	fi
	if [ -f "$log" ] && grep -F 'leak' "$log" >/dev/null; then
		fail "$name curl saw a token"
	fi
	if [ -f "$log" ] && grep -F 'super-secret' "$log" >/dev/null; then
		fail "$name put a token on the curl command line"
	fi
	echo "ok: $name"
}

grep -F 'VERIFY_PUBLISHED_ATTEMPTS:-7' "$script" >/dev/null || fail "attempt bound drifted"
grep -F 'VERIFY_PUBLISHED_SLEEP:-10' "$script" >/dev/null || fail "sleep bound drifted"

run success pass v0.25.0 0 "PASS verify-published-release version=0.25.0"
grep -F -- '-q -sS -L --proto =https' "$log" >/dev/null || fail "success curl was not anonymous https"
grep -F 'Cache-Control: no-cache' "$log" >/dev/null || fail "cask request does not bypass cache"
grep -F 'https://api.github.com/repos/lu-zhengda/homebrew-tap/contents/Casks/updater.rb' "$log" >/dev/null || fail "cask url drifted"
grep -F 'https://github.com/lu-zhengda/updater/releases/download/v0.25.0/updater_0.25.0_darwin.tar.gz' "$log" >/dev/null || fail "tarball url drifted"
grep -F 'https://github.com/lu-zhengda/updater/releases/download/v0.25.0/Updater-0.25.0.dmg' "$log" >/dev/null || fail "dmg url drifted"
grep -F 'Authorization' "$log" >/dev/null && fail "curl was given an Authorization header"
cask_calls=$(grep -c 'Casks/updater.rb' "$log" || true)
[ "$cask_calls" -eq 1 ] || fail "success fetched the cask $cask_calls times"

run stale-cask bad-version v0.25.0 1 "cask version [0.24.9] != 0.25.0" 2
grep -F 'updater_0.25.0_darwin.tar.gz' "$log" >/dev/null && fail "stale cask still downloaded a tarball"
cask_calls=$(grep -c 'Casks/updater.rb' "$log" || true)
[ "$cask_calls" -eq 2 ] || fail "stale cask tried $cask_calls times, want 2"
grep -F 'FAIL verify-published-release after 2 attempts' "$tmp/stale-cask.err" >/dev/null || fail "stale cask did not exhaust retries"

run bad-url bad-url v0.25.0 1 "not-the-asset.tar.gz" 1
grep -F 'Updater-0.25.0.dmg' "$log" >/dev/null && fail "bad cask url still downloaded the dmg"

run tarball-404 tarball-404 v0.25.0 1 "tarball HTTP 404" 1
grep -F 'Updater-0.25.0.dmg' "$log" >/dev/null && fail "failed tarball still downloaded the dmg"

run dmg-small dmg-small v0.25.0 1 "dmg HTTP 200 size 4, want > 1048576" 1
run dmg-exact dmg-exact v0.25.0 1 "dmg HTTP 200 size 1048576, want > 1048576" 1

run retry retry-version v0.25.0 0 "PASS verify-published-release version=0.25.0" 3
cask_calls=$(grep -c 'Casks/updater.rb' "$log" || true)
[ "$cask_calls" -eq 2 ] || fail "retry fetched the cask $cask_calls times, want 2"
grep -F 'retrying in 0s' "$tmp/retry.err" >/dev/null || fail "retry did not record the bounded wait"

run cask-404 cask-404 v0.25.0 1 "cask HTTP 404" 1
run curl-fail curl-fail v0.25.0 1 "tarball curl exit 28" 1

run bad-attempts pass v0.25.0 2 "VERIFY_PUBLISHED_ATTEMPTS must be 1-7" 8
[ ! -f "$log" ] || fail "rejected attempt bound still ran curl"

run bad-tag pass v1.2 2 "tag must look like vX.Y.Z"
[ ! -f "$log" ] || fail "rejected tag still ran curl"

run bare-tag pass 0.25.0 2 "tag must look like vX.Y.Z"
[ ! -f "$log" ] || fail "bare version still ran curl"

VERIFY_PUBLISHED_SLEEP=11 \
	GITHUB_TOKEN=super-secret \
	sh "$script" v0.25.0 >"$tmp/sleep.out" 2>"$tmp/sleep.err" && fail "sleep 11 was accepted"
grep -F 'VERIFY_PUBLISHED_SLEEP must be 0, 5, or 10' "$tmp/sleep.err" >/dev/null || fail "sleep bound message missing"
grep -F 'super-secret' "$tmp/sleep.out" "$tmp/sleep.err" >/dev/null && fail "sleep rejection printed a token"

echo "verify-published-release tests ok"
