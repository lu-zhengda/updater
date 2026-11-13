package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSnapshotCaskInstallsCompletions runs on the macOS CI job. It builds a
// GoReleaser snapshot and brew-installs the generated cask, so a tarball that
// omits a file the cask links fails before publish. Local go test skips it.
func TestSnapshotCaskInstallsCompletions(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("snapshot cask install runs on the macOS CI job")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(wd)
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "cask-archive-proof.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("cask archive proof: %v", err)
	}
}
