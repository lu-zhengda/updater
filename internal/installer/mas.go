package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

const masRelease = "7.0.0"

type masArtifact struct {
	arch, packageSHA, binarySHA, binarySize string
}

var masArtifacts = map[string]masArtifact{
	"arm64": {"arm64", "bc218a854c85d9e1c95496a96b26287bb66056b95688b90f91d04fa62ed21b75", "1a9ec80879b029b67fe3de485203722cfdbee8589fdd70d3bf3c3df59c9f32c9", "10923456"},
	"amd64": {"x86_64", "9bca1de1fb6ea19c9e0b9d7b7c85249020c9ed65a9d434c2ef40896a2f3395f9", "7bdd3967f62f5439fdc4ad3dd3a1d0972cb43ba09fd736bc58720121467c07ee", "11518752"},
}

// UpdateAppStoreApp runs a single manual App Store update and verifies the installed version.
func (inst *Installer) UpdateAppStoreApp(ctx context.Context, target *app.App, latest string) (string, error) {
	if !inst.AllowAuthorizationPrompt {
		return "", fmt.Errorf("App Store update requires manual authorization")
	}
	id, err := strconv.ParseUint(target.MASID, 10, 64)
	if err != nil || id == 0 {
		return "", fmt.Errorf("no valid App Store ID for %s", target.Name)
	}
	metadataID, err := inst.runner.Run(ctx, "/usr/bin/mdls", "-raw", "-name", "kMDItemAppStoreAdamID", target.Path)
	if err != nil || strings.TrimSpace(string(metadataID)) != target.MASID {
		return "", fmt.Errorf("could not confirm the App Store ID for %s", target.Name)
	}
	before, err := target.InstalledVersion()
	if err != nil {
		return "", err
	}
	if version.IsNewerOrEqual(latest, before) {
		return "", fmt.Errorf("%s is already at version %s", target.Name, before)
	}

	artifact, ok := masArtifacts[runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("App Store update is unsupported on %s", runtime.GOARCH)
	}
	stage, err := os.MkdirTemp("", "updater-mas-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	url := fmt.Sprintf("https://github.com/mas-cli/mas/releases/download/v%s/mas-%s-%s.pkg", masRelease, masRelease, artifact.arch)
	pkg, err := inst.download(ctx, url, stage, "sha256:"+artifact.packageSHA)
	if err != nil {
		return "", fmt.Errorf("could not prepare mas: %w", err)
	}
	expanded := filepath.Join(stage, "expanded")
	if _, err := inst.runner.Run(ctx, "/usr/sbin/pkgutil", "--expand-full", pkg, expanded); err != nil {
		return "", fmt.Errorf("could not extract mas: %w", err)
	}
	binary := filepath.Join(expanded, "mas.pkg/Payload/usr/local/opt/mas/libexec/bin/mas")
	updateErr := inst.authorizeMASUpdate(ctx, target, binary, artifact)
	after, err := target.InstalledVersion()
	if updateErr != nil {
		if err == nil && version.IsNewer(before, after) {
			return "", fmt.Errorf("%s is now at version %s, but %w", target.Name, after, updateErr)
		}
		return "", updateErr
	}
	if err != nil {
		return "", fmt.Errorf("could not verify App Store update: %w", err)
	}
	if !version.IsNewer(before, after) {
		return "", fmt.Errorf("mas finished, but %s did not advance (%s → %s)", target.Name, before, after)
	}
	if !version.IsNewerOrEqual(latest, after) {
		return "", fmt.Errorf("mas installed version %s, but version %s remains pending", after, latest)
	}
	return after, nil
}

func (inst *Installer) authorizeMASUpdate(ctx context.Context, target *app.App, binary string, artifact masArtifact) error {
	out, err := inst.runner.Run(ctx, "/usr/bin/osascript", "-e", authorizeMASScript, masUpdateCommand,
		binary, artifact.binarySHA, artifact.binarySize, strconv.Itoa(os.Getuid()), target.MASID, target.Name)
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return fmt.Errorf("could not authorize App Store update: %s", detail)
		}
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			return fmt.Errorf("could not authorize App Store update: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return fmt.Errorf("could not authorize App Store update: %w", err)
	}
	result := strings.TrimSpace(string(out))
	status, detail, _ := strings.Cut(result, ":")
	switch status {
	case "ok":
		return nil
	case "-128", "-60006":
		return fmt.Errorf("App Store update cancelled")
	default:
		if detail == "" {
			detail = result
		}
		return fmt.Errorf("App Store update failed: %s", strings.TrimSpace(detail))
	}
}

const authorizeMASScript = `on run argv
	set commandText to "/usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin /bin/sh -c " & quoted form of (item 1 of argv) & " updater-mas"
	repeat with argumentValue in items 2 thru 6 of argv
		set commandText to commandText & " " & quoted form of (contents of argumentValue)
	end repeat
	set explanation to "Updater wants to install the App Store update for " & item 7 of argv & "."
	try
		do shell script (commandText & " 2>&1") with prompt explanation with administrator privileges without altering line endings
		return "ok"
	on error detail number status
		return (status as text) & ": " & detail
	end try
end run`

// Stage the pinned Mach-O before executing it as root. The source is opened as the console user.
const masUpdateCommand = `set -eu
umask 077
uid=$(/usr/bin/stat -f %u /dev/console)
if [ "$uid" = 0 ] || [ "$uid" != "$4" ]; then
	echo "Run Updater from the signed-in Mac desktop."
	exit 1
fi
gid=$(/usr/bin/id -g "$uid")
dir=$(/usr/bin/mktemp -d /private/var/tmp/updater-mas.XXXXXXXX)
trap '/bin/rm -rf "$dir"' EXIT
/usr/bin/sudo -n -u "#$uid" -- /usr/bin/perl -MFcntl=O_RDONLY,O_NONBLOCK,O_NOFOLLOW -e '
	sysopen(STDIN, $ARGV[0], O_RDONLY | O_NONBLOCK | O_NOFOLLOW) && -f STDIN
		or die "Could not open the staged mas executable";
	exec "/usr/bin/head", "-c", $ARGV[1] or die "Could not copy mas";
' -- "$1" "$(($3 + 1))" > "$dir/mas"
printf '%s  %s\n' "$2" "$dir/mas" | /usr/bin/shasum -a 256 -c -
/bin/chmod 500 "$dir/mas"
/bin/launchctl asuser "$uid" /usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin SUDO_UID="$uid" SUDO_GID="$gid" MAS_NO_AUTO_INDEX=1 "$dir/mas" update "$5"`
