#!/bin/sh
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
checker=$root/scripts/cask-checksum.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

printf 'updater archive' >"$tmp/artifact"
sum=$(shasum -a 256 "$tmp/artifact" | awk '{print $1}')
cat >"$tmp/ok.rb" <<EOF
cask "updater" do
  version "0.24.2"
  sha256 "$sum"
  url "https://example.test/updater.tar.gz"
end
EOF
sh "$checker" "$tmp/ok.rb" "$tmp/artifact"

cat >"$tmp/bad.rb" <<EOF
cask "updater" do
  version "0.24.2"
  sha256 "dd2c5063562038470a31002eb202763fbe29083893fedbf5a98253b7cad757bd"
  url "https://example.test/updater.tar.gz"
end
EOF
if sh "$checker" "$tmp/bad.rb" "$tmp/artifact" >"$tmp/bad.out" 2>"$tmp/bad.err"; then
	fail "mismatched cask sha256 was accepted"
fi
grep -F 'does not match' "$tmp/bad.err" >/dev/null
echo "cask checksum tests ok"
