#!/bin/sh
# Prove a published Updater release on this Mac.
#
# Usage:
#   verify-updater.sh [--doctor] [version]
#
# version defaults to the latest non-draft, non-prerelease GitHub Release.
# Accepts 0.24.2 or v0.24.2. Each check prints PASS or FAIL and evidence.
# The process exits non-zero if any check fails.
#
# This script downloads published artifacts and may install the Homebrew cask.
# It does not sign, notarize, tag, or publish, and it does not read signing secrets.

set -eu

REPO=lu-zhengda/updater
TAP_REPO=lu-zhengda/homebrew-tap
TEAM_ID=XY7H3QGJ2T
CASK=lu-zhengda/tap/updater
INSTALLED_APP=/Applications/Updater.app

export PATH="/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"
export HOMEBREW_NO_AUTO_UPDATE=1
export HOMEBREW_NO_ENV_HINTS=1
export HOMEBREW_NO_INSTALL_UPGRADE=1

# Do not inherit or print signing and publish credentials.
unset GITHUB_TOKEN GH_TOKEN HOMEBREW_TAP_TOKEN || true
unset MACOS_CERT_P12_BASE64 MACOS_CERT_PASSWORD MACOS_SIGNING_IDENTITY MACOS_KEYCHAIN_PATH || true
unset APPSTORE_API_KEY_P8_BASE64 APPSTORE_API_KEY_ID APPSTORE_API_ISSUER_ID NOTARIZE || true

mode=run
if [ "${1:-}" = "--doctor" ]; then
	mode=doctor
	shift
fi
version_arg=${1:-}

work=
mountpoint=
evidence=
mounted_app=
app_snapshot_version=
tap_cask=
cask_backup=
pass=0
fail=0

say() {
	printf '%s\n' "$*" | tee -a "$evidence" >&2
}

say_file() {
	if [ -f "$1" ]; then
		sed 's/^/  /' "$1" | tee -a "$evidence" >&2
	fi
}

cleanup() {
	rc=$?
	set +e
	if [ -n "$mountpoint" ] && [ -d "$mountpoint" ]; then
		hdiutil detach "$mountpoint" -force >/dev/null 2>&1
		rmdir "$mountpoint" 2>/dev/null
	fi
	if [ -n "$cask_backup" ] && [ -f "$cask_backup" ] && [ -n "$tap_cask" ]; then
		cp "$cask_backup" "$tap_cask"
	fi
	if [ -n "$work" ] && [ -d "$work/pids-at-start" ] || [ -n "$work" ] && [ -f "$work/pids-at-start" ]; then
		quit_new_matching "Updater.app/Contents/MacOS/updater"
	fi
	if [ -n "$work" ] && [ -f "$work/helper-dirs-after" ] && [ -f "$work/helper-dirs-before" ]; then
		new_helpers=$(comm -13 "$work/helper-dirs-before" "$work/helper-dirs-after" || true)
		for dir in $new_helpers; do
			rm -rf "$dir"
		done
	fi
	if [ -n "$work" ] && [ -d "$work" ]; then
		rm -rf "$work"
	fi
	exit "$rc"
}

snapshot_pids() {
	ps -axo pid= | tr -d ' ' > "$work/pids-at-start"
}

pid_is_new() {
	if [ ! -f "$work/pids-at-start" ]; then
		return 0
	fi
	if grep -qx "$1" "$work/pids-at-start"; then
		return 1
	fi
	return 0
}

quit_new_matching() {
	needle=$1
	ps -axo pid=,command= | while IFS= read -r line; do
		case $line in
		*"$needle"*)
			set -- $line
			pid=$1
			if pid_is_new "$pid"; then
				kill -TERM "$pid" 2>/dev/null || true
			fi
			;;
		esac
	done
	sleep 1
	ps -axo pid=,command= | while IFS= read -r line; do
		case $line in
		*"$needle"*)
			set -- $line
			pid=$1
			if pid_is_new "$pid"; then
				if kill -0 "$pid" 2>/dev/null; then
					kill -KILL "$pid" 2>/dev/null || true
				fi
			fi
			;;
		esac
	done
}

require_tools() {
	missing=0
	for tool in gh curl shasum codesign stapler spctl lipo hdiutil xattr ditto tar python3 open uuidgen awk; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			say "missing tool: $tool"
			missing=1
		fi
	done
	if ! command -v brew >/dev/null 2>&1; then
		say "missing tool: brew"
		missing=1
	fi
	if [ ! -x /usr/libexec/PlistBuddy ]; then
		say "missing tool: /usr/libexec/PlistBuddy"
		missing=1
	fi
	uname_s=$(uname -s)
	say "uname=$uname_s"
	sw_vers | tee -a "$evidence" >&2
	if [ "$uname_s" != "Darwin" ]; then
		say "verification must run on macOS"
		missing=1
	fi
	return "$missing"
}

bundle_version() {
	plist=$1/Contents/Info.plist
	if [ ! -f "$plist" ]; then
		return 1
	fi
	/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$plist"
}

version_less() {
	awk -v a="$1" -v b="$2" 'BEGIN {
		n = split(a, aa, ".")
		m = split(b, bb, ".")
		c = n
		if (m < n) c = m
		for (i = 1; i <= c; i++) {
			av = aa[i] + 0
			bv = bb[i] + 0
			if (av < bv) { print "yes"; exit }
			if (av > bv) { print "no"; exit }
		}
		if (n < m) print "yes"; else print "no"
	}'
}

normalize_version() {
	printf '%s\n' "${1#v}"
}

apply_quarantine() {
	target=$1
	stamp=$(printf '%x' "$(date +%s)")
	uuid=$(uuidgen | tr '[:upper:]' '[:lower:]')
	qvalue="0083;${stamp};Safari;${uuid}"
	xattr -w com.apple.quarantine "$qvalue" "$target"
	say "quarantine $(xattr -p com.apple.quarantine "$target") on $target"
}

sha256_file() {
	shasum -a 256 "$1" | awk '{print $1}'
}

checksum_for() {
	file=$1
	name=$2
	awk -v name="$name" '$2 == name { print $1; exit }' "$file"
}

download() {
	url=$1
	dest=$2
	curl -fsSL --retry 3 --retry-delay 2 -o "$dest" "$url"
}

record() {
	if [ "$1" -eq 0 ]; then
		say "PASS $2"
		pass=$((pass + 1))
	else
		say "FAIL $2"
		fail=$((fail + 1))
	fi
}

restore_errexit() {
	if [ "${saved_errexit:-0}" -eq 1 ]; then
		set -e
	else
		set +e
	fi
}

save_errexit() {
	saved_errexit=0
	case $- in
	*e*) saved_errexit=1 ;;
	esac
}

run_to() {
	out=$1
	shift
	save_errexit
	set +e
	"$@" >"$out.out" 2>"$out.err"
	run_to_status=$?
	restore_errexit
}

run_timed() {
	seconds=$1
	out=$2
	shift 2
	save_errexit
	"$@" >"$out.out" 2>"$out.err" &
	timed_pid=$!
	i=0
	while [ "$i" -lt "$seconds" ]; do
		if ! kill -0 "$timed_pid" 2>/dev/null; then
			set +e
			wait "$timed_pid"
			run_to_status=$?
			restore_errexit
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	kill -TERM "$timed_pid" 2>/dev/null || true
	sleep 1
	kill -KILL "$timed_pid" 2>/dev/null || true
	set +e
	wait "$timed_pid"
	run_to_status=124
	restore_errexit
}

receipt_version() {
	receipt=$1
	if [ ! -f "$receipt" ]; then
		return 1
	fi
	python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["source"]["version"])' "$receipt"
}

receipt_path() {
	prefix=$(brew --prefix)
	printf '%s\n' "$prefix/Caskroom/updater/.metadata/INSTALL_RECEIPT.json"
}

snapshot_helpers() {
	dest=$1
	: > "$dest"
	tmp_root=${TMPDIR:-/tmp}
	find "$tmp_root" /tmp -maxdepth 1 -name 'updater-self-helper-*' -print 2>/dev/null | sort -u > "$dest" || true
}

find_new_pid() {
	path=$1
	ps -axo pid=,command= | while IFS= read -r line; do
		case $line in
		*"$path"*)
			set -- $line
			if pid_is_new "$1"; then
				printf '%s\n' "$1"
				break
			fi
			;;
		esac
	done
}

wait_for_new_pid() {
	path=$1
	tries=$2
	i=0
	while [ "$i" -lt "$tries" ]; do
		pid=$(find_new_pid "$path")
		if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
			sleep 1
			pid2=$(find_new_pid "$path")
			if [ "$pid2" = "$pid" ]; then
				printf '%s\n' "$pid"
				return 0
			fi
		fi
		sleep 1
		i=$((i + 1))
	done
	return 1
}

describe_dialogs() {
	cat >"$work/dialogs.applescript" <<'EOF'
tell application "System Events"
  set blob to ""
  set procs to {"CoreServicesUIAgent", "UserNotificationCenter", "Finder", "SecurityAgent"}
  repeat with pn in procs
    if exists process (pn as text) then
      tell process (pn as text)
        repeat with w in windows
          set wn to ""
          try
            set wn to name of w as text
          end try
          set blob to blob & pn & " | " & wn & linefeed
          try
            repeat with t in static texts of w
              set blob to blob & "  text: " & (value of t as text) & linefeed
            end repeat
          end try
        end repeat
      end tell
    end if
  end repeat
  if blob is "" then return "no Gatekeeper window on CoreServicesUIAgent, UserNotificationCenter, Finder, or SecurityAgent"
  return blob
end tell
EOF
	run_timed 8 "$work/dialogs" osascript "$work/dialogs.applescript"
	say "dialog probe exit=$run_to_status"
	say_file "$work/dialogs.out"
	say_file "$work/dialogs.err"
}

click_gatekeeper_open() {
	cat >"$work/click.applescript" <<'EOF'
tell application "System Events"
  set resultText to "no Updater download dialog"
  set procs to {"CoreServicesUIAgent", "UserNotificationCenter", "Finder", "SecurityAgent"}
  repeat with pn in procs
    if exists process (pn as text) then
      tell process (pn as text)
        repeat with w in windows
          set blob to ""
          try
            set blob to name of w as text
          end try
          try
            repeat with t in static texts of w
              set blob to blob & " " & (value of t as text)
            end repeat
          end try
          if blob contains "Updater" and blob contains "downloaded" then
            repeat with b in buttons of w
              if name of b is "Open" then
                click b
                return "clicked Open on " & pn & ": " & blob
              end if
            end repeat
            return "Updater download dialog had no Open button: " & blob
          end if
        end repeat
      end tell
    end if
  end repeat
  return resultText
end tell
EOF
	run_timed 8 "$work/click" osascript "$work/click.applescript"
	say "gatekeeper click exit=$run_to_status"
	say_file "$work/click.out"
	say_file "$work/click.err"
}

backup_installed_app() {
	app_snapshot_version=
	rm -rf "$work/backup"
	mkdir -p "$work/backup"
	if [ -d "$INSTALLED_APP" ]; then
		app_snapshot_version=$(bundle_version "$INSTALLED_APP" 2>/dev/null || printf '%s' unreadable)
		ditto "$INSTALLED_APP" "$work/backup/Updater.app"
		say "backed up $INSTALLED_APP version=$app_snapshot_version"
	else
		app_snapshot_version=missing
		say "no $INSTALLED_APP to back up"
	fi
}

installed_now() {
	if [ ! -d "$INSTALLED_APP" ]; then
		printf '%s\n' missing
		return 0
	fi
	bundle_version "$INSTALLED_APP" 2>/dev/null || printf '%s\n' unreadable
}

restore_installed_app_if_changed() {
	now=$(installed_now)
	before=${app_snapshot_version:-missing}
	if [ "$now" = "$before" ]; then
		return 0
	fi
	say "installed app changed from $before to $now; restoring backup"
	if [ -d "$work/backup/Updater.app" ]; then
		rm -rf "$INSTALLED_APP"
		ditto "$work/backup/Updater.app" "$INSTALLED_APP"
		say "restored $INSTALLED_APP"
	else
		say "no backup to restore"
		return 1
	fi
}

resolve_version() {
	if [ -n "$version_arg" ]; then
		version=$(normalize_version "$version_arg")
		case $version in
		"" | *[!0-9.]*)
			say "version must look like 0.24.2"
			return 1
			;;
		esac
		tag=v$version
		if ! gh api "repos/$REPO/releases/tags/$tag" >"$work/release.json" 2>"$work/release.err"; then
			say "GitHub Release $tag does not exist"
			say_file "$work/release.err"
			return 1
		fi
	else
		if ! gh api "repos/$REPO/releases/latest" >"$work/release.json" 2>"$work/release.err"; then
			say "could not read the latest GitHub Release"
			say_file "$work/release.err"
			return 1
		fi
		tag=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tag_name"])' "$work/release.json")
		version=$(normalize_version "$tag")
	fi
	say "version=$version tag=$tag"
	return 0
}

check_release_published() {
	python3 - "$work/release.json" <<'PY'
import json, sys
rel = json.load(open(sys.argv[1]))
draft = bool(rel.get("draft"))
pre = bool(rel.get("prerelease"))
print("tag=%s draft=%s prerelease=%s html_url=%s" % (
    rel.get("tag_name"), draft, pre, rel.get("html_url")))
names = [a.get("name") for a in rel.get("assets") or []]
print("assets=%s" % ", ".join(names))
sys.exit(0 if (not draft and not pre and rel.get("tag_name")) else 1)
PY
}

asset_present() {
	name=$1
	python3 - "$work/release.json" "$name" <<'PY'
import json, sys
rel = json.load(open(sys.argv[1]))
want = sys.argv[2]
ok = any(a.get("name") == want for a in rel.get("assets") or [])
sys.exit(0 if ok else 1)
PY
}

check_release_assets() {
	dmg_name=Updater-$version.dmg
	tar_name=updater_${version}_darwin.tar.gz
	ok=0
	for name in "$dmg_name" "$tar_name" checksums.txt; do
		if asset_present "$name"; then
			say "asset present: $name"
		else
			say "asset missing: $name"
			ok=1
		fi
	done
	return "$ok"
}

check_checksums() {
	base=https://github.com/$REPO/releases/download/v$version
	mkdir -p "$work/assets"
	if ! download "$base/checksums.txt" "$work/assets/checksums.txt"; then
		say "download failed: checksums.txt"
		return 1
	fi
	say "checksums.txt:"
	say_file "$work/assets/checksums.txt"
	ok=0
	for name in "Updater-$version.dmg" "updater_${version}_darwin.tar.gz"; do
		if ! download "$base/$name" "$work/assets/$name"; then
			say "download failed: $name"
			ok=1
			continue
		fi
		expected=$(checksum_for "$work/assets/checksums.txt" "$name")
		actual=$(sha256_file "$work/assets/$name")
		say "$name expected=$expected actual=$actual"
		if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
			ok=1
		fi
	done
	return "$ok"
}

check_dmg_trust() {
	dmg=$work/assets/Updater-$version.dmg
	if [ ! -f "$dmg" ]; then
		say "DMG was not downloaded"
		return 1
	fi
	apply_quarantine "$dmg"
	ok=0
	run_to "$work/dmg-codesign" codesign --verify --verbose=2 "$dmg"
	say "codesign --verify exit=$run_to_status"
	say_file "$work/dmg-codesign.out"
	say_file "$work/dmg-codesign.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	run_to "$work/dmg-stapler" stapler validate "$dmg"
	say "stapler validate exit=$run_to_status"
	say_file "$work/dmg-stapler.out"
	say_file "$work/dmg-stapler.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	run_to "$work/dmg-spctl" spctl --assess --type open --context context:primary-signature --verbose "$dmg"
	say "spctl --assess --type open --context context:primary-signature exit=$run_to_status"
	say_file "$work/dmg-spctl.out"
	say_file "$work/dmg-spctl.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	mountpoint=$(mktemp -d "${TMPDIR:-/tmp}/verify-updater-mount.XXXXXX")
	run_to "$work/hdiutil" hdiutil attach -nobrowse -readonly -mountpoint "$mountpoint" "$dmg"
	say "hdiutil attach exit=$run_to_status"
	say_file "$work/hdiutil.out"
	say_file "$work/hdiutil.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
		mountpoint=
		return "$ok"
	fi
	if [ -d "$mountpoint/Updater.app" ]; then
		mounted_app=$mountpoint/Updater.app
		say "mounted app=$mounted_app"
	else
		say "Updater.app missing at DMG root"
		find "$mountpoint" -maxdepth 3 -name 'Updater.app' -print | tee -a "$evidence"
		ok=1
	fi
	return "$ok"
}

check_app_trust() {
	if [ -z "$mounted_app" ] || [ ! -d "$mounted_app" ]; then
		say "no mounted Updater.app"
		return 1
	fi
	ok=0
	bin=$mounted_app/Contents/MacOS/updater
	run_to "$work/app-codesign" codesign --verify --deep --strict --verbose=2 "$mounted_app"
	say "codesign --verify --deep --strict exit=$run_to_status"
	say_file "$work/app-codesign.out"
	say_file "$work/app-codesign.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	run_to "$work/app-stapler" stapler validate "$mounted_app"
	say "stapler validate exit=$run_to_status"
	say_file "$work/app-stapler.out"
	say_file "$work/app-stapler.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	run_to "$work/app-spctl" spctl --assess --type execute --verbose "$mounted_app"
	say "spctl --assess --type execute exit=$run_to_status"
	say_file "$work/app-spctl.out"
	say_file "$work/app-spctl.err"
	if [ "$run_to_status" -ne 0 ]; then
		ok=1
	fi
	run_to "$work/app-display" codesign -dvv "$mounted_app"
	say_file "$work/app-display.out"
	say_file "$work/app-display.err"
	team=$(awk -F= '/^TeamIdentifier=/{print $2; exit}' "$work/app-display.err" "$work/app-display.out")
	say "TeamIdentifier=$team expected=$TEAM_ID"
	if [ "$team" != "$TEAM_ID" ]; then
		ok=1
	fi
	if [ ! -x "$bin" ]; then
		say "missing executable $bin"
		ok=1
	else
		run_to "$work/lipo" lipo -archs "$bin"
		say "lipo -archs exit=$run_to_status"
		say_file "$work/lipo.out"
		say_file "$work/lipo.err"
		archs=$(awk 'NR==1 { print }' "$work/lipo.out")
		case " $archs " in
		*" x86_64 "* )
			;;
		*)
			say "missing x86_64"
			ok=1
			;;
		esac
		case " $archs " in
		*" arm64 "* )
			;;
		*)
			say "missing arm64"
			ok=1
			;;
		esac
	fi
	got=$(bundle_version "$mounted_app" 2>/dev/null || printf '%s' missing)
	say "CFBundleShortVersionString=$got expected=$version"
	if [ "$got" != "$version" ]; then
		ok=1
	fi
	return "$ok"
}

check_launch() {
	if [ -z "$mounted_app" ] || [ ! -d "$mounted_app" ]; then
		say "no mounted app to launch"
		return 1
	fi
	launch=$work/launch/Updater.app
	mkdir -p "$work/launch"
	ditto "$mounted_app" "$launch"
	apply_quarantine "$launch"
	bin=$launch/Contents/MacOS/updater
	method=
	pid=

	run_timed 20 "$work/open" open -n "$launch"
	say "open -n exit=$run_to_status"
	say_file "$work/open.out"
	say_file "$work/open.err"
	if pid=$(wait_for_new_pid "$bin" 12); then
		method="open -n"
	else
		describe_dialogs
		click_gatekeeper_open
		if pid=$(wait_for_new_pid "$bin" 12); then
			method="open -n after clicking the Gatekeeper Open button"
		else
			say "Gatekeeper first-launch dialog was not completed headlessly."
			say "Checked instead: start the quarantined Mach-O directly. stdin is not a terminal, so the bundle takes the menu-bar path."
			"$bin" </dev/null >"$work/direct.out" 2>"$work/direct.err" &
			sleep 1
			if pid=$(wait_for_new_pid "$bin" 12); then
				method="direct exec of the quarantined binary; the LaunchServices first-open dialog was not clicked"
			fi
			say "direct exec stderr:"
			say_file "$work/direct.err"
		fi
	fi
	if [ -z "$method" ] || [ -z "$pid" ]; then
		say "quarantined app process did not stay running"
		return 1
	fi
	say "running pid=$pid method=$method"
	ps -p "$pid" -o pid=,command= | tee -a "$evidence"
	kill -TERM "$pid" 2>/dev/null || true
	i=0
	while [ "$i" -lt 5 ]; do
		if ! kill -0 "$pid" 2>/dev/null; then
			say "quit pid=$pid with SIGTERM"
			say "launch method: $method"
			return 0
		fi
		sleep 1
		i=$((i + 1))
	done
	kill -KILL "$pid" 2>/dev/null || true
	say "quit pid=$pid with SIGKILL after SIGTERM"
	say "launch method: $method"
	return 0
}

check_cask_metadata() {
	if ! gh api -H "Accept: application/vnd.github.raw" "repos/$TAP_REPO/contents/Casks/updater.rb" >"$work/cask.rb" 2>"$work/cask.err"; then
		say "could not read $TAP_REPO Casks/updater.rb"
		say_file "$work/cask.err"
		return 1
	fi
	cask_version=$(awk -F'"' '$1 ~ /version / { print $2; exit }' "$work/cask.rb")
	cask_sha=$(awk -F'"' '$1 ~ /sha256 / { print $2; exit }' "$work/cask.rb")
	tar_name=updater_${version}_darwin.tar.gz
	actual=
	if [ -f "$work/assets/$tar_name" ]; then
		actual=$(sha256_file "$work/assets/$tar_name")
	fi
	listed=$(checksum_for "$work/assets/checksums.txt" "$tar_name")
	say "cask version=$cask_version expected=$version"
	say "cask sha256=$cask_sha"
	say "tarball sha256=$actual checksums.txt=$listed"
	ok=0
	if [ "$cask_version" != "$version" ] || [ -z "$cask_sha" ] || [ "$cask_sha" != "$actual" ] || [ "$cask_sha" != "$listed" ]; then
		ok=1
	fi
	tap=$(brew --repository lu-zhengda/tap 2>/dev/null || true)
	tap_cask=
	if [ -n "$tap" ] && [ -f "$tap/Casks/updater.rb" ]; then
		if cmp -s "$tap/Casks/updater.rb" "$work/cask.rb"; then
			say "brew tap cask matches GitHub main"
		else
			say "brew tap cask differs from GitHub main ($tap/Casks/updater.rb)"
			ok=1
		fi
	else
		say "lu-zhengda/tap is not checked out for Homebrew"
		ok=1
	fi
	return "$ok"
}

check_brew_install() {
	say "command: brew install --cask $CASK"
	run_to "$work/brew-install" brew install --cask "$CASK"
	say "exit=$run_to_status"
	say_file "$work/brew-install.out"
	say_file "$work/brew-install.err"
	now=$(installed_now)
	receipt=$(receipt_path)
	got_receipt=$(receipt_version "$receipt" 2>/dev/null || printf '%s' missing)
	say "app version after install=$now receipt=$got_receipt"
	if [ "$run_to_status" -ne 0 ] || [ "$now" != "$version" ] || [ "$got_receipt" != "$version" ]; then
		return 1
	fi
	return 0
}

install_older_cask() {
	tap=$(brew --repository lu-zhengda/tap 2>/dev/null || true)
	if [ -z "$tap" ] || [ ! -d "$tap/.git" ] || [ ! -f "$tap/Casks/updater.rb" ]; then
		say "lu-zhengda/tap is not a git checkout; cannot install an older cask"
		return 1
	fi
	if ! git -C "$tap" diff --quiet -- Casks/updater.rb || ! git -C "$tap" diff --cached --quiet -- Casks/updater.rb; then
		say "Casks/updater.rb has uncommitted changes; not swapping it"
		return 1
	fi
	tap_cask=$tap/Casks/updater.rb
	cask_backup=$work/published-cask.rb
	cp "$tap_cask" "$cask_backup"
	git -C "$tap" log --format=%H -- Casks/updater.rb >"$work/cask-revs.txt"
	chosen=
	while IFS= read -r rev; do
		[ -n "$rev" ] || continue
		git -C "$tap" show "$rev:Casks/updater.rb" >"$work/candidate-cask.rb"
		cand=$(awk -F'"' '$1 ~ /version / { print $2; exit }' "$work/candidate-cask.rb")
		if [ -z "$cand" ] || [ "$(version_less "$cand" "$version")" != yes ]; then
			continue
		fi
		cp "$work/candidate-cask.rb" "$tap_cask"
		run_to "$work/candidate-info" brew info --cask "$CASK"
		if [ "$run_to_status" -eq 0 ]; then
			chosen=$cand
			say "older loadable cask version=$cand rev=$rev"
			break
		fi
		say "cask revision $rev version=$cand does not load"
		say_file "$work/candidate-info.err"
	done <"$work/cask-revs.txt"
	if [ -z "$chosen" ]; then
		cp "$cask_backup" "$tap_cask"
		say "no older cask revision loads in this Homebrew"
		return 1
	fi
	say "command: brew uninstall --cask $CASK"
	run_to "$work/brew-uninstall-old" brew uninstall --cask "$CASK"
	say "uninstall exit=$run_to_status"
	say_file "$work/brew-uninstall-old.out"
	say_file "$work/brew-uninstall-old.err"
	say "command: brew install --cask $CASK (tap cask temporarily $chosen)"
	run_to "$work/brew-install-old" brew install --cask "$CASK"
	say "old install exit=$run_to_status"
	say_file "$work/brew-install-old.out"
	say_file "$work/brew-install-old.err"
	cp "$cask_backup" "$tap_cask"
	got=$(receipt_version "$(receipt_path)" 2>/dev/null || printf '%s' missing)
	say "after older install app=$(installed_now) receipt=$got"
	if [ "$run_to_status" -ne 0 ] || [ "$got" != "$chosen" ]; then
		return 1
	fi
	return 0
}

check_brew_upgrade() {
	receipt=$(receipt_path)
	before_receipt=$(receipt_version "$receipt" 2>/dev/null || printf '%s' missing)
	before_app=$(installed_now)
	say "before upgrade app=$before_app receipt=$before_receipt target=$version"
	older=no
	if [ "$before_receipt" != missing ] && [ "$(version_less "$before_receipt" "$version")" = yes ]; then
		older=yes
	fi
	if [ "$before_app" != missing ] && [ "$before_app" != unreadable ] && [ "$(version_less "$before_app" "$version")" = yes ]; then
		older=yes
	fi
	if [ "$older" = no ]; then
		say "Homebrew is already at the target version; installing an older published cask first"
		run_to "$work/brew-info-before-upgrade" brew info --cask "$CASK"
		say "brew info --cask exit=$run_to_status"
		say_file "$work/brew-info-before-upgrade.err"
		if [ "$run_to_status" -eq 0 ] && install_older_cask; then
			older=yes
			before_receipt=$(receipt_version "$receipt" 2>/dev/null || printf '%s' missing)
			before_app=$(installed_now)
			say "downgraded to app=$before_app receipt=$before_receipt"
		else
			say "could not install an older cask before upgrade"
		fi
	fi
	say "command: brew upgrade --cask $CASK"
	run_to "$work/brew-upgrade" brew upgrade --cask "$CASK"
	say "exit=$run_to_status"
	say_file "$work/brew-upgrade.out"
	say_file "$work/brew-upgrade.err"
	now=$(installed_now)
	got_receipt=$(receipt_version "$receipt" 2>/dev/null || printf '%s' missing)
	say "app version after upgrade=$now receipt=$got_receipt"
	if [ "$older" != yes ] || [ "$run_to_status" -ne 0 ] || [ "$now" != "$version" ] || [ "$got_receipt" != "$version" ]; then
		return 1
	fi
	return 0
}

previous_version() {
	gh api "repos/$REPO/releases?per_page=30" --jq '.[] | select(.draft==false and .prerelease==false) | .tag_name' >"$work/tags.txt"
	best=
	while IFS= read -r tag; do
		[ -n "$tag" ] || continue
		ver=$(normalize_version "$tag")
		if [ "$(version_less "$ver" "$version")" != yes ]; then
			continue
		fi
		if ! gh api "repos/$REPO/releases/tags/v$ver" --jq '.assets[].name' | grep -qx "updater_${ver}_darwin.tar.gz"; then
			say "skip v$ver: no updater_${ver}_darwin.tar.gz"
			continue
		fi
		if [ -z "$best" ] || [ "$(version_less "$best" "$ver")" = yes ]; then
			best=$ver
		fi
	done <"$work/tags.txt"
	if [ -z "$best" ]; then
		return 1
	fi
	printf '%s\n' "$best"
}

check_self_update() {
	prev=$(previous_version) || {
		say "no older published release with a darwin tarball"
		return 1
	}
	say "older published release=$prev"
	base=https://github.com/$REPO/releases/download/v$prev
	mkdir -p "$work/old"
	if ! download "$base/checksums.txt" "$work/old/checksums.txt"; then
		say "download failed: v$prev checksums.txt"
		return 1
	fi
	tar_name=updater_${prev}_darwin.tar.gz
	if ! download "$base/$tar_name" "$work/old/$tar_name"; then
		say "download failed: $tar_name"
		return 1
	fi
	expected=$(checksum_for "$work/old/checksums.txt" "$tar_name")
	actual=$(sha256_file "$work/old/$tar_name")
	say "older tarball expected=$expected actual=$actual"
	if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
		return 1
	fi
	mkdir -p "$work/old/root"
	tar -xzf "$work/old/$tar_name" -C "$work/old/root"
	old_app=$work/old/root/Updater.app
	if [ ! -d "$old_app" ]; then
		say "older tarball has no Updater.app"
		find "$work/old/root" -maxdepth 2 -print | tee -a "$evidence"
		return 1
	fi
	old_ver=$(bundle_version "$old_app" 2>/dev/null || printf '%s' missing)
	say "older CFBundleShortVersionString=$old_ver"
	run_to "$work/old-codesign" codesign -dvv "$old_app"
	say_file "$work/old-codesign.err"
	old_team=$(awk -F= '/^TeamIdentifier=/{print $2; exit}' "$work/old-codesign.err" "$work/old-codesign.out")
	say "older TeamIdentifier=$old_team"
	if [ "$old_ver" = "$version" ]; then
		say "older bundle is already $version"
		return 1
	fi
	log=$HOME/.config/updater/logs/self-update.log
	before_bytes=0
	if [ -f "$log" ]; then
		before_bytes=$(wc -c <"$log" | tr -d ' ')
	fi
	say "self-update log bytes before=$before_bytes path=$log"
	snapshot_helpers "$work/helper-dirs-before"
	bin=$old_app/Contents/MacOS/updater
	say "command: $bin upgrade"
	"$bin" upgrade >"$work/upgrade.out" 2>"$work/upgrade.err" &
	upgrade_pid=$!
	deadline=$(( $(date +%s) + 180 ))
	got=$old_ver
	while [ "$(date +%s)" -lt "$deadline" ]; do
		got=$(bundle_version "$old_app" 2>/dev/null || printf '%s' missing)
		if [ "$got" = "$version" ]; then
			break
		fi
		if ! kill -0 "$upgrade_pid" 2>/dev/null; then
			wait "$upgrade_pid" || true
			upgrade_pid=
			# The bundle command schedules a helper and returns. Keep polling.
		fi
		sleep 2
	done
	if [ -n "${upgrade_pid:-}" ] && kill -0 "$upgrade_pid" 2>/dev/null; then
		kill -TERM "$upgrade_pid" 2>/dev/null || true
		sleep 1
		kill -KILL "$upgrade_pid" 2>/dev/null || true
	fi
	say "upgrade stdout:"
	say_file "$work/upgrade.out"
	say "upgrade stderr:"
	say_file "$work/upgrade.err"
	if [ -f "$log" ]; then
		say "self-update log delta:"
		tail -c +"$((before_bytes + 1))" "$log" | sed 's/^/  /' | tee -a "$evidence"
	else
		say "self-update log was not created"
	fi
	say "bundle version after upgrade=$got expected=$version"
	snapshot_helpers "$work/helper-dirs-after"
	quit_new_matching "$old_app"
	quit_new_matching "updater-self-helper-"
	if [ "$got" != "$version" ]; then
		return 1
	fi
	installed_after=$(installed_now)
	if [ "$installed_after" != "${app_snapshot_version:-missing}" ]; then
		say "self-update changed $INSTALLED_APP from $app_snapshot_version to $installed_after"
		restore_installed_app_if_changed || true
		return 1
	fi
	return 0
}

main() {
	evidence_dir=${VERIFY_UPDATER_EVIDENCE_DIR:-$HOME/Library/Logs/verify-updater}
	mkdir -p "$evidence_dir"
	evidence=$evidence_dir/$(date -u +%Y%m%dT%H%M%SZ).log
	: > "$evidence"
	work=$(mktemp -d "${TMPDIR:-/tmp}/verify-updater.XXXXXX")
	trap cleanup EXIT INT TERM
	snapshot_pids
	say "evidence=$evidence"
	say "work=$work"
	say "CHECK doctor"
	if require_tools; then
		say "PASS doctor"
	else
		say "FAIL doctor"
		exit 1
	fi
	if [ "$mode" = doctor ]; then
		if [ -d "$INSTALLED_APP" ]; then
			say "installed $(bundle_version "$INSTALLED_APP" 2>/dev/null || echo unreadable) at $INSTALLED_APP"
		fi
		brew info --cask "$CASK" >"$work/doctor-brew.out" 2>"$work/doctor-brew.err" || true
		say "brew info --cask $CASK:"
		say_file "$work/doctor-brew.out"
		say_file "$work/doctor-brew.err"
		say "doctor only; no release checks were run"
		trap - EXIT INT TERM
		rm -rf "$work"
		work=
		exit 0
	fi

	say "CHECK release-published"
	if ! resolve_version; then
		say "FAIL release-published"
		fail=$((fail + 1))
		say "RESULT pass=$pass fail=$fail"
		exit 1
	fi
	set +e
	check_release_published >"$work/published.txt"
	published_status=$?
	set -e
	say_file "$work/published.txt"
	record "$published_status" release-published

	say "CHECK release-assets"
	set +e
	check_release_assets
	assets_status=$?
	set -e
	record "$assets_status" release-assets

	say "CHECK release-checksums"
	set +e
	check_checksums
	sum_status=$?
	set -e
	record "$sum_status" release-checksums

	say "CHECK dmg-trust"
	set +e
	check_dmg_trust
	dmg_status=$?
	set -e
	record "$dmg_status" dmg-trust

	say "CHECK app-trust"
	set +e
	check_app_trust
	app_status=$?
	set -e
	record "$app_status" app-trust

	say "CHECK launch"
	set +e
	check_launch
	launch_status=$?
	set -e
	record "$launch_status" launch

	backup_installed_app

	say "CHECK self-update"
	set +e
	check_self_update
	self_status=$?
	set -e
	if [ "$self_status" -ne 0 ]; then
		restore_installed_app_if_changed || true
	fi
	record "$self_status" self-update

	say "CHECK cask-metadata"
	set +e
	check_cask_metadata
	cask_status=$?
	set -e
	record "$cask_status" cask-metadata

	say "CHECK brew-upgrade"
	set +e
	check_brew_upgrade
	upgrade_status=$?
	set -e
	if [ "$upgrade_status" -ne 0 ]; then
		restore_installed_app_if_changed || true
	fi
	record "$upgrade_status" brew-upgrade

	say "CHECK brew-install"
	set +e
	check_brew_install
	install_status=$?
	set -e
	if [ "$install_status" -ne 0 ]; then
		restore_installed_app_if_changed || true
	fi
	record "$install_status" brew-install

	say "RESULT pass=$pass fail=$fail"
	say "evidence=$evidence"
	if [ "$fail" -ne 0 ]; then
		exit 1
	fi
	exit 0
}

main
