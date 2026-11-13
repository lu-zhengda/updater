#!/bin/sh
# Write the shell completions the release tarball and Homebrew cask share.
#
# Usage: completions.sh <updater-binary> <output-dir>
#
# completions/ is gitignored. GoReleaser archives completions/* and the cask
# links completions/updater.bash, completions/updater.zsh, and
# completions/updater.fish. This has to run after the universal binary exists
# and before archives are built.
# shellcheck shell=sh
set -eu

bin=${1:?usage: completions.sh <updater-binary> <output-dir>}
out=${2:?usage: completions.sh <updater-binary> <output-dir>}

if [ ! -x "$bin" ]; then
	echo "updater binary is not executable: $bin" >&2
	exit 1
fi

mkdir -p "$out"
"$bin" completion bash >"$out/updater.bash"
"$bin" completion zsh >"$out/updater.zsh"
"$bin" completion fish >"$out/updater.fish"

for name in updater.bash updater.zsh updater.fish; do
	if [ ! -s "$out/$name" ]; then
		echo "completion file is empty: $out/$name" >&2
		exit 1
	fi
done
