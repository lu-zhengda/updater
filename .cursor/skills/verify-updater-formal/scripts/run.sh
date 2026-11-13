#!/bin/sh
# Prove the self-update safety properties and show each one failing on a
# broken variant. TLC is tla2tools 1.7.4. Fuzz time is bounded.
set -eu
cd "$(CDPATH= cd -- "$(dirname "$0")/../../../../" && pwd)"
root=$(pwd)
model=$root/.cursor/skills/verify-updater-formal/model
cache=$root/.cursor/skills/verify-updater-formal/.cache
jar=$cache/tla2tools.jar
jar_sha=936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88
fuzz_time=${FUZZ_TIME:-10s}

if [ -n "${JAVA:-}" ]; then
  :
elif java -version >/dev/null 2>&1; then
  JAVA=java
elif [ -x /opt/homebrew/opt/openjdk/bin/java ]; then
  JAVA=/opt/homebrew/opt/openjdk/bin/java
else
  echo "Java 11 or later is required to run TLC." >&2
  exit 1
fi

mkdir -p "$cache"
if [ ! -f "$jar" ] || ! shasum -a 256 "$jar" | grep -q "$jar_sha"; then
  curl -fsSL -o "$jar" https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar
fi
echo "$jar_sha  $jar" | shasum -a 256 -c -

tlc() {
  cfg=$1
  want=$2
  meta=$(mktemp -d)
  set +e
  "$JAVA" -XX:+UseParallelGC -cp "$jar" tlc2.TLC \
    -workers 1 -cleanup -metadir "$meta" \
    -config "$model/$cfg" "$model/SelfUpdate.tla" >"$meta/out" 2>&1
  code=$?
  set -e
  states=$(sed -n 's/^\([0-9][0-9]*\) states generated, \([0-9][0-9]*\) distinct states found.*/\1 generated, \2 distinct/p' "$meta/out")
  depth=$(sed -n 's/The depth of the complete state graph search is \([0-9][0-9]*\).*/depth \1/p' "$meta/out")
  if [ "$want" = pass ]; then
    if [ "$code" -ne 0 ]; then
      echo "TLC $cfg expected pass, exit $code" >&2
      cat "$meta/out" >&2
      exit 1
    fi
    echo "PASS $cfg ($states, $depth)"
    return
  fi
  if [ "$code" -eq 0 ] || ! grep -q "Invariant $want is violated" "$meta/out"; then
    echo "TLC $cfg expected $want to fail, exit $code" >&2
    cat "$meta/out" >&2
    exit 1
  fi
  echo "CAUGHT $cfg ($want; $states, $depth)"
}

echo "== TLC mutations (each property must fail) =="
tlc mutate-two-rename.cfg AlwaysPresent
tlc brew-two-rename.cfg AlwaysPresent
tlc mutate-skip-checksum.cfg NoBadInstall
tlc mutate-skip-signature.cfg NoBadInstall
tlc mutate-skip-newer.cfg NoDowngrade
tlc mutate-skip-relock.cfg NoDowngrade
tlc mutate-skip-lock.cfg NoDowngrade
tlc mutate-skip-version.cfg NoDowngrade
tlc brew-partial.cfg AlwaysPresent
tlc brew-unsigned.cfg NoBadInstall
tlc brew-regress.cfg NoDowngrade

echo "== TLC on the code as it is =="
tlc direct.cfg pass
tlc brew-ok.cfg pass

echo "== Go tests for the same properties =="
go test -count=1 -timeout 180s \
  -run 'TestIsNewer|TestCleanTagVersionPrefixes|TestParseBrewInfo|TestChecksumForAsset|TestReplaceUpdaterBundle|TestRestoreUpdaterBundle|TestSelfUpgradeGuardrails|TestSelfUpgradeTransaction' \
  ./internal/version/ ./internal/checker/ ./cmd/updater/

echo "== Go mutations (the test must fail, then the tree is restored) =="
python3 "$root/.cursor/skills/verify-updater-formal/scripts/mutate.py"

echo "== Bounded fuzz =="
go test -fuzz=FuzzIsNewer -fuzztime="$fuzz_time" ./internal/version/
go test -fuzz=FuzzGitHubRelease -fuzztime="$fuzz_time" ./internal/checker/
go test -fuzz=FuzzParseBrewInfo -fuzztime="$fuzz_time" ./internal/checker/
go test -fuzz=FuzzChecksumForAsset -fuzztime="$fuzz_time" ./cmd/updater/

echo "formal check passed"
