package signing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodeIdentity(t *testing.T) {
	output := `Executable=/Applications/Test.app/Contents/MacOS/Test
Identifier=com.example.test
TeamIdentifier=AB12CD34EF
Authority=Developer ID Application: Example (AB12CD34EF)
`
	got := parseCodeIdentity(output)
	if got.Identifier != "com.example.test" || got.TeamID != "AB12CD34EF" {
		t.Fatalf("parseCodeIdentity() = %#v", got)
	}
}

func TestParseInstallerTeamID(t *testing.T) {
	output := `Package "Test.pkg":
   Status: signed by a developer certificate issued by Apple for distribution
   Certificate Chain:
    1. Developer ID Installer: Example Corp (AB12CD34EF)
    2. Developer ID Certification Authority
`
	if got := parseInstallerTeamID(output); got != "AB12CD34EF" {
		t.Fatalf("parseInstallerTeamID() = %q, want AB12CD34EF", got)
	}
}

func TestParseInstallerTeamIDRejectsNonDeveloperID(t *testing.T) {
	if got := parseInstallerTeamID("1. Apple Development: Example (AB12CD34EF)"); got != "" {
		t.Fatalf("parseInstallerTeamID() = %q, want empty", got)
	}
}

func TestVerifyReplacementAppRejectsDifferentTeam(t *testing.T) {
	candidate := filepath.Join(t.TempDir(), "Candidate.app")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	verifier := &Verifier{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/bin/codesign" && len(args) > 0 && args[0] == "--verify" {
			return nil, nil
		}
		path := args[len(args)-1]
		if path == "/Applications/Installed.app" {
			return []byte("Identifier=com.example.app\nTeamIdentifier=AB12CD34EF\n"), nil
		}
		if path == candidate {
			return []byte("Identifier=com.example.app\nTeamIdentifier=ZZ98YY76XX\n"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}

	err := verifier.VerifyReplacementApp(context.Background(), "/Applications/Installed.app", candidate)
	if err == nil || !strings.Contains(err.Error(), "does not match installed Team ID") {
		t.Fatalf("expected Team ID mismatch, got %v", err)
	}
}

func TestVerifyReplacementAppAcceptsMatchingNotarizedIdentity(t *testing.T) {
	candidate := filepath.Join(t.TempDir(), "Candidate.app")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	verifier := &Verifier{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "/usr/bin/codesign":
			if len(args) > 0 && args[0] == "--verify" {
				return nil, nil
			}
			return []byte("Identifier=com.example.app\nTeamIdentifier=AB12CD34EF\n"), nil
		case "/usr/sbin/spctl":
			return []byte("accepted\nsource=Notarized Developer ID"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	}}

	if err := verifier.VerifyReplacementApp(context.Background(), "/Applications/Installed.app", candidate); err != nil {
		t.Fatalf("expected matching notarized app to pass: %v", err)
	}
}
