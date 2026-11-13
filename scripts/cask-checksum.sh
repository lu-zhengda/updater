#!/bin/sh
# Compare the sha256 in a generated Homebrew cask to the file it names.
# GoReleaser writes that hash. Rewriting it before brew install hides a miss.
# shellcheck shell=sh
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: cask-checksum.sh <cask.rb> <artifact>" >&2
	exit 2
fi

cask=$1
artifact=$2
want=$(awk -F '"' '$1 ~ /sha256 / { print $2; exit }' "$cask")
got=$(shasum -a 256 "$artifact" | awk '{print $1}')
if [ -z "$want" ] || [ "$want" != "$got" ]; then
	echo "cask sha256 [${want:-missing}] does not match $artifact [$got]" >&2
	exit 1
fi
echo "cask sha256 matches $artifact"
