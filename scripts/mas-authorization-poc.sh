#!/bin/sh
# Run from the signed-in Mac App Store user's macOS session.
set -eu
PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH

if [ "$#" -ne 3 ]; then
    printf 'Usage: %s APP_STORE_ID "/Applications/App.app" EXPECTED_VERSION\n' "$0" >&2
    exit 2
fi
app_id=$1
app=$2
expected=$3
case "$app_id" in ''|0*|*[!0-9]*) echo "Expected a numeric App Store ID" >&2; exit 2;; esac
if [ "$(id -u)" = 0 ] || [ "$(id -u)" != "$(stat -f %u /dev/console)" ]; then
    echo "Run this as the user signed in to the Mac desktop" >&2
    exit 1
fi

version() { /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist"; }
if [ ! -f "$app/Contents/_MASReceipt/receipt" ] ||
    [ "$(mdls -raw -name kMDItemAppStoreAdamID "$app")" != "$app_id" ]; then
    echo "Bundle and App Store ID do not match" >&2
    exit 1
fi
before=$(version)
if [ "$before" = "$expected" ]; then
    echo "Choose an app with an update available" >&2
    exit 1
fi

case "$(uname -m)" in
    arm64)
        package_hash=bc218a854c85d9e1c95496a96b26287bb66056b95688b90f91d04fa62ed21b75
        binary_size=10923456
        binary_hash=1a9ec80879b029b67fe3de485203722cfdbee8589fdd70d3bf3c3df59c9f32c9
        ;;
    x86_64)
        package_hash=9bca1de1fb6ea19c9e0b9d7b7c85249020c9ed65a9d434c2ef40896a2f3395f9
        binary_size=11518752
        binary_hash=7bdd3967f62f5439fdc4ad3dd3a1d0972cb43ba09fd736bc58720121467c07ee
        ;;
    *) echo "Unsupported architecture" >&2; exit 1;;
esac
stage=$(mktemp -d "${TMPDIR:-/tmp}/updater-mas-poc.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
curl -fLsS -o "$stage/mas.pkg" "https://github.com/mas-cli/mas/releases/download/v7.0.0/mas-7.0.0-$(uname -m).pkg"
printf '%s  %s\n' "$package_hash" "$stage/mas.pkg" | shasum -a 256 -c -
pkgutil --expand-full "$stage/mas.pkg" "$stage/expanded"
binary="$stage/expanded/mas.pkg/Payload/usr/local/opt/mas/libexec/bin/mas"

# Pass the fixed program as an argument so root never opens a script from the checkout.
payload=$(cat <<'ROOT'
set -eu
umask 077
dir=$(mktemp -d /private/var/tmp/updater-mas-poc.XXXXXXXX)
label=com.updater.mas-poc.${dir##*.}
job=system/$label
loaded=
cleanup() {
    if [ -n "$loaded" ] && ! launchctl bootout "$job" && launchctl print "$job" >/dev/null 2>&1; then
        echo "Could not unload $job; staging at $dir" >&2
        exit 1
    fi
    rm -rf "$dir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
uid=$(stat -f %u /dev/console)
[ "$uid" = "$4" ]
gid=$(id -g "$uid")
sudo -n -u "#$uid" -- /usr/bin/head -c "$(($5 + 1))" -- "$1" > "$dir/mas"
chmod 500 "$dir/mas"
printf '%s  %s\n' "$2" "$dir/mas" | shasum -a 256 -c -

cat > "$dir/job.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>$label</string>
<key>RunAtLoad</key><true/>
<key>ProgramArguments</key><array>
<string>/bin/sh</string><string>-c</string>
<string>if test "\$(/usr/bin/stat -f %u /dev/console)" = "$uid"; then
    /bin/launchctl asuser $uid /usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin SUDO_UID=$uid SUDO_GID=$gid MAS_NO_AUTO_INDEX=1 "$dir/mas" update $3
    status=\$?
else
    echo "The Mac desktop user changed"
    status=1
fi
echo "\$status" > "$dir/pending"
/bin/mv "$dir/pending" "$dir/result"
exit "\$status"</string>
</array>
<key>StandardOutPath</key><string>$dir/output</string>
<key>StandardErrorPath</key><string>$dir/errors</string>
</dict></plist>
PLIST
plutil -lint "$dir/job.plist"
for run in 1 2; do
    loaded=1
    launchctl bootstrap system "$dir/job.plist"
    remaining=480
    while [ ! -f "$dir/result" ] && [ "$remaining" -gt 0 ]; do
        sleep 1
        remaining=$((remaining - 1))
    done
    launchctl bootout "$job"
    loaded=
    for log in "$dir/output" "$dir/errors"; do
        if [ -f "$log" ]; then cat "$log"; fi
    done
    if [ ! -f "$dir/result" ]; then
        echo "Run $run timed out" >&2
        exit 1
    fi
    status=$(cat "$dir/result")
    echo "Run $run exited $status"
    [ "$status" = 0 ] || exit 1
    if [ "$run" = 1 ] && ! grep -q '==> Updated ' "$dir/output"; then
        echo "mas did not report an installed update" >&2
        exit 1
    fi
    rm "$dir/output" "$dir/errors" "$dir/result"
done
ROOT
)

printf 'Requesting one administrator approval to test two background runs for %s (%s → %s).\n' "$app" "$before" "$expected"
result=$(/usr/bin/osascript - "$payload" "$binary" "$binary_hash" "$app_id" "$(id -u)" "$binary_size" <<'APPLESCRIPT'
on run argv
    set commandText to "/usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin /bin/sh -c " & quoted form of item 1 of argv & " mas-poc"
    repeat with argumentValue in items 2 thru 6 of argv
        set commandText to commandText & " " & quoted form of (contents of argumentValue)
    end repeat
    try
        return (do shell script (commandText & " 2>&1") with prompt "Allow the Updater proof of concept to test one App Store app update?" with administrator privileges without altering line endings) & linefeed & "MAS_POC_COMPLETED"
    on error detail number status
        return detail & linefeed & "macOS error " & status
    end try
end run
APPLESCRIPT
)
printf '%s\n' "$result"
case "$result" in *MAS_POC_COMPLETED) ;; *) exit 1;; esac

after=$(version)
if [ "$after" != "$expected" ] || [ ! -f "$app/Contents/_MASReceipt/receipt" ]; then
    printf 'FAIL: expected %s with an App Store receipt, found %s\n' "$expected" "$after" >&2
    exit 1
fi
printf 'PASS: installed version changed from %s to %s and the App Store receipt exists.\n' "$before" "$after"
