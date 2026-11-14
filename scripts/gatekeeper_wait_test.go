package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifyUpdaterGatekeeperSelfTest(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("verify-updater.sh is the macOS harness")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(wd)
	cmd := exec.Command("sh", filepath.Join(root, ".cursor/skills/verify-updater/scripts/verify-updater.sh"), "--self-test")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("gatekeeper self-test: %v", err)
	}
}
