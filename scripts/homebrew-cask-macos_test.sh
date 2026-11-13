#!/bin/sh
# Loads the cask custom_block the way GoReleaser emits it.
# Current Homebrew must require macOS >= 26, and must still reject a cask
# that has both a bare depends_on :macos and depends_on macos: :tahoe.
# shellcheck shell=sh
set -eu

root=$(cd -- "$(dirname "$0")/.." && pwd)
config=$root/.goreleaser.yaml

export HOMEBREW_NO_AUTO_UPDATE=1
export HOMEBREW_NO_ENV_HINTS=1

tmp=$(mktemp -d)
tap_name=localtest/updaterproof
cleanup() {
	brew untap "$tap_name" >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ok() {
	echo "ok: $*"
}

grep -F "super(macos: :tahoe)" "$config" >/dev/null || fail "cask custom_block does not map depends_on :macos to Tahoe"
if grep -E '^[[:space:]]*depends_on macos:' "$config" >/dev/null; then
	fail "custom_block still contains a second depends_on macos stanza"
fi
ok "goreleaser config keeps a single macOS requirement path"

awk '
	$0 == "    custom_block: |" { grab=1; next }
	grab && $0 ~ /^    [^ ]/ { exit }
	grab {
		sub(/^      /, "  ")
		print
	}
' "$config" >"$tmp/custom_block.rb"
if ! grep -F "super(macos: :tahoe)" "$tmp/custom_block.rb" >/dev/null; then
	fail "could not read custom_block from .goreleaser.yaml"
fi

write_body() {
	dest=$1
	macos_stanzas=$2
	{
		echo 'cask "updaterproof" do'
		cat "$tmp/custom_block.rb"
		cat <<'EOF'
  version "0.24.2"
  sha256 "dd2c5063562038470a31002eb202763fbe29083893fedbf5a98253b7cad757bd"

  url "https://github.com/lu-zhengda/updater/releases/download/v#{version}/updater_#{version}_darwin.tar.gz"
  name "Updater proof"
  desc "Load test for the Updater cask requirement"
  homepage "https://github.com/lu-zhengda/updater"

EOF
		printf '%s\n' "$macos_stanzas"
		echo 'end'
	} >"$dest"
}

mkdir -p "$tmp/tap/Casks"
git -C "$tmp/tap" init -b main >/dev/null
git -C "$tmp/tap" config user.email "test@example.com"
git -C "$tmp/tap" config user.name "Test"
git -C "$tmp/tap" config commit.gpgsign false
write_body "$tmp/tap/Casks/updaterproof.rb" "  depends_on :macos"
git -C "$tmp/tap" add Casks/updaterproof.rb
git -C "$tmp/tap" commit -m "cask" >/dev/null
brew tap --force "$tap_name" "$tmp/tap" >/dev/null
brew trust "$tap_name" >/dev/null
tap_root=$(brew --repository "$tap_name")

install_case() {
	name=$1
	src=$2
	cp "$src" "$tap_root/Casks/updaterproof.rb"
	set +e
	brew info --cask "$tap_name/updaterproof" >"$tmp/$name.info" 2>"$tmp/$name.err"
	status=$?
	set -e
	return "$status"
}

cat >"$tmp/broken.rb" <<'EOF'
cask "updaterproof" do
  version "0.24.2"
  sha256 "dd2c5063562038470a31002eb202763fbe29083893fedbf5a98253b7cad757bd"

  url "https://github.com/lu-zhengda/updater/releases/download/v#{version}/updater_#{version}_darwin.tar.gz"
  name "Updater proof"
  desc "Load test for the Updater cask requirement"
  homepage "https://github.com/lu-zhengda/updater"

  depends_on macos: :tahoe
  depends_on :macos

  app "Updater.app"
end
EOF
if install_case broken "$tmp/broken.rb"; then
	fail "Homebrew loaded a cask with both macOS stanzas"
fi
grep -F "depends_on :macos" "$tmp/broken.err" >/dev/null
ok "brew info rejects both macOS stanzas"

write_body "$tmp/fixed.rb" "  depends_on :macos"
if ! install_case fixed "$tmp/fixed.rb"; then
	echo "brew info failed:" >&2
	cat "$tmp/fixed.info" "$tmp/fixed.err" >&2
	fail "Homebrew did not load the custom_block cask"
fi
grep -F "Required: macOS >= 26" "$tmp/fixed.info" >/dev/null
ok "brew info requires macOS >= 26"

brew audit --cask "$tap_name/updaterproof"
ok "brew audit"
set +e
brew style "$tap_name/updaterproof" >"$tmp/style.out" 2>"$tmp/style.err"
style_status=$?
set -e
if [ "$style_status" -eq 0 ]; then
	ok "brew style"
else
	echo "brew style exit=$style_status"
	cat "$tmp/style.out"
fi

echo "homebrew cask macOS requirement ok"
