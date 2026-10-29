package signing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/architecture"
)

func TestParseCodeIdentity(t *testing.T) {
	output := `Executable=/Applications/Test.app/Contents/MacOS/Test
Identifier=com.example.test
TeamIdentifier=AB12CD34EF
Authority=Developer ID Application: Example (AB12CD34EF)
`
	got := parseCodeIdentity(output)
	if got.Identifier != "com.example.test" || got.TeamID != "AB12CD34EF" || got.Executable != "/Applications/Test.app/Contents/MacOS/Test" {
		t.Fatalf("parseCodeIdentity() = %#v", got)
	}
}

func TestVerifyReplacementAppWithoutBundleExecutable(t *testing.T) {
	for _, failAt := range []string{"", "signature", "architecture", "gatekeeper"} {
		t.Run(failAt, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "Muse.app")
			binary := filepath.Join(candidate, "Contents", "MacOS", "Muse")
			if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, nil, 0o755); err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.EvalSymlinks(binary)
			if err != nil {
				t.Fatal(err)
			}
			architectureChecked, gatekeeperChecked := false, false
			v := &Verifier{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				switch name {
				case "/usr/bin/codesign":
					if args[0] == "--verify" && failAt == "signature" {
						return nil, fmt.Errorf("modified code")
					}
					return []byte("Identifier=com.meta.endo\nTeamIdentifier=V9WTTPBFK9\nExecutable=" + binary + "\n"), nil
				case "/usr/bin/plutil":
					return nil, fmt.Errorf("no value at key path CFBundleExecutable")
				case "/usr/bin/lipo":
					architectureChecked = true
					if args[len(args)-1] != resolved {
						t.Fatalf("architecture check used %q, want signed executable %q", args[len(args)-1], resolved)
					}
					if failAt == "architecture" {
						return nil, fmt.Errorf("missing native slice")
					}
					return nil, nil
				case "/usr/sbin/spctl":
					gatekeeperChecked = true
					if failAt == "gatekeeper" {
						return nil, fmt.Errorf("not notarized")
					}
					return nil, nil
				default:
					return nil, fmt.Errorf("unexpected command %s", name)
				}
			}}
			err = v.VerifyReplacementApp(context.Background(), "/Applications/Muse.app", candidate)
			if (err != nil) != (failAt != "") {
				t.Fatalf("unexpected verification result: %v", err)
			}
			if failAt == "" && (!architectureChecked || !gatekeeperChecked) {
				t.Fatal("missing executable metadata must not skip architecture or Gatekeeper checks")
			}
		})
	}
}

func TestSignedExecutableFallbackRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Muse.app")
	macOS := filepath.Join(bundle, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "external")
	if err := os.WriteFile(outside, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(macOS, "Muse")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	v := &Verifier{run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("missing plist entry")
	}}
	for _, path := range []string{"", outside, link, "relative/path", macOS} {
		if got, err := v.appExecutable(context.Background(), bundle, CodeIdentity{Executable: path}); err == nil {
			t.Fatalf("accepted unsafe executable %q as %q", path, got)
		}
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
		case "/usr/bin/plutil":
			return []byte("Candidate"), nil
		case "/usr/bin/lipo":
			return nil, nil
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

func TestVerifyReplacementAppRejectsWrongArchitecture(t *testing.T) {
	candidate := filepath.Join(t.TempDir(), "Candidate.app")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	checked := false
	verifier := &Verifier{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "/usr/bin/codesign":
			return []byte("Identifier=com.example.app\nTeamIdentifier=AB12CD34EF\n"), nil
		case "/usr/bin/plutil":
			return []byte("Candidate"), nil
		case "/usr/bin/lipo":
			checked = true
			wantArch := architecture.Native()
			if wantArch == "amd64" {
				wantArch = "x86_64"
			}
			if len(args) != 3 || args[0] != "-verify_arch" || args[1] != wantArch || args[2] != filepath.Join(candidate, "Contents", "MacOS", "Candidate") {
				t.Fatalf("unexpected binary check: %v", args)
			}
			return nil, fmt.Errorf("missing native architecture")
		default:
			t.Fatalf("must stop before Gatekeeper after architecture failure: %s", name)
			return nil, nil
		}
	}}
	err := verifier.VerifyReplacementApp(context.Background(), "/Applications/Installed.app", candidate)
	if !checked || err == nil || !strings.Contains(err.Error(), "does not support native architecture") {
		t.Fatalf("wrong architecture must be rejected: %v", err)
	}
}
