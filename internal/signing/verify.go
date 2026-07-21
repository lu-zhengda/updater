package signing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// CodeIdentity is the stable Apple signing identity used to decide whether a
// downloaded update is allowed to replace an installed code object.
type CodeIdentity struct {
	Identifier string
	TeamID     string
}

type commandFunc func(context.Context, string, ...string) ([]byte, error)

// Verifier validates downloaded macOS code against the identity of the code it
// is replacing. The installed app or executable is the local trust anchor.
type Verifier struct {
	run commandFunc
}

// NewVerifier returns a verifier backed by the macOS security command-line
// tools. It is intentionally strict: ad-hoc and unsigned installed code cannot
// authorize unattended replacement.
func NewVerifier() *Verifier {
	return &Verifier{run: runCombined}
}

// VerifyReplacementApp requires a valid, notarized candidate app with the same
// bundle identifier and Developer Team ID as the installed app.
func (v *Verifier) VerifyReplacementApp(ctx context.Context, installedApp, candidateApp string) error {
	if err := requireRegularBundle(candidateApp); err != nil {
		return err
	}

	installed, err := v.inspectCode(ctx, installedApp)
	if err != nil {
		return fmt.Errorf("failed to inspect installed app signature: %w", err)
	}
	if err := requireDeveloperIdentity(installed); err != nil {
		return fmt.Errorf("installed app cannot authorize direct updates: %w", err)
	}

	if output, err := v.run(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=2", candidateApp); err != nil {
		return commandError("candidate app has an invalid code signature", output, err)
	}
	candidate, err := v.inspectCode(ctx, candidateApp)
	if err != nil {
		return fmt.Errorf("failed to inspect candidate app signature: %w", err)
	}
	if candidate.Identifier != installed.Identifier {
		return fmt.Errorf("candidate bundle identifier %q does not match installed identifier %q", candidate.Identifier, installed.Identifier)
	}
	if candidate.TeamID != installed.TeamID {
		return fmt.Errorf("candidate Team ID %q does not match installed Team ID %q", candidate.TeamID, installed.TeamID)
	}

	if output, err := v.run(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=4", candidateApp); err != nil {
		return commandError("candidate app is not accepted by Gatekeeper", output, err)
	}
	return nil
}

// VerifyInstallerPackage requires a notarized Developer ID Installer package
// issued to the same team as the app being updated.
func (v *Verifier) VerifyInstallerPackage(ctx context.Context, installedApp, pkgPath string) error {
	installed, err := v.inspectCode(ctx, installedApp)
	if err != nil {
		return fmt.Errorf("failed to inspect installed app signature: %w", err)
	}
	if err := requireDeveloperIdentity(installed); err != nil {
		return fmt.Errorf("installed app cannot authorize package updates: %w", err)
	}

	output, err := v.run(ctx, "/usr/sbin/pkgutil", "--check-signature", pkgPath)
	if err != nil {
		return commandError("installer package signature is invalid", output, err)
	}
	pkgTeamID := parseInstallerTeamID(string(output))
	if pkgTeamID == "" {
		return fmt.Errorf("installer package is not signed with a Developer ID Installer certificate")
	}
	if pkgTeamID != installed.TeamID {
		return fmt.Errorf("installer package Team ID %q does not match installed Team ID %q", pkgTeamID, installed.TeamID)
	}

	if output, err := v.run(ctx, "/usr/sbin/spctl", "--assess", "--type", "install", "--verbose=4", pkgPath); err != nil {
		return commandError("installer package is not accepted by Gatekeeper", output, err)
	}
	return nil
}

// VerifyReplacementExecutable requires a valid candidate executable signed by
// the same Developer Team and with the same signing identifier as the running
// executable.
func (v *Verifier) VerifyReplacementExecutable(ctx context.Context, installedExecutable, candidateExecutable string) error {
	installed, err := v.inspectCode(ctx, installedExecutable)
	if err != nil {
		return fmt.Errorf("failed to inspect installed executable signature: %w", err)
	}
	if err := requireDeveloperIdentity(installed); err != nil {
		return fmt.Errorf("installed executable cannot authorize self-upgrade: %w", err)
	}

	if output, err := v.run(ctx, "/usr/bin/codesign", "--verify", "--strict", "--verbose=2", candidateExecutable); err != nil {
		return commandError("downloaded executable has an invalid code signature", output, err)
	}
	candidate, err := v.inspectCode(ctx, candidateExecutable)
	if err != nil {
		return fmt.Errorf("failed to inspect downloaded executable signature: %w", err)
	}
	if candidate.Identifier != installed.Identifier {
		return fmt.Errorf("downloaded executable identifier %q does not match installed identifier %q", candidate.Identifier, installed.Identifier)
	}
	if candidate.TeamID != installed.TeamID {
		return fmt.Errorf("downloaded executable Team ID %q does not match installed Team ID %q", candidate.TeamID, installed.TeamID)
	}
	return nil
}

func (v *Verifier) inspectCode(ctx context.Context, path string) (CodeIdentity, error) {
	output, err := v.run(ctx, "/usr/bin/codesign", "--display", "--verbose=4", path)
	if err != nil {
		return CodeIdentity{}, commandError("codesign inspection failed", output, err)
	}
	identity := parseCodeIdentity(string(output))
	if identity.Identifier == "" {
		return CodeIdentity{}, fmt.Errorf("code signature has no identifier")
	}
	return identity, nil
}

func requireDeveloperIdentity(identity CodeIdentity) error {
	if identity.Identifier == "" {
		return fmt.Errorf("code signature has no identifier")
	}
	if identity.TeamID == "" || identity.TeamID == "not set" {
		return fmt.Errorf("code is not signed by an Apple Developer team")
	}
	return nil
}

func requireRegularBundle(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect candidate app: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("candidate app must not be a symbolic link")
	}
	if !info.IsDir() {
		return fmt.Errorf("candidate app is not a directory bundle")
	}
	return nil
}

func parseCodeIdentity(output string) CodeIdentity {
	var identity CodeIdentity
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Identifier="):
			identity.Identifier = strings.TrimPrefix(line, "Identifier=")
		case strings.HasPrefix(line, "TeamIdentifier="):
			identity.TeamID = strings.TrimPrefix(line, "TeamIdentifier=")
		}
	}
	return identity
}

var installerTeamIDPattern = regexp.MustCompile(`(?m)^\s*1\.\s+Developer ID Installer:.*\(([A-Z0-9]{10})\)\s*$`)

func parseInstallerTeamID(output string) string {
	match := installerTeamIDPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func commandError(message string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", message, err)
	}
	return fmt.Errorf("%s: %s: %w", message, detail, err)
}

func runCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
