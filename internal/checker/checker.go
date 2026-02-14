package checker

import (
	"context"
	"os/exec"

	"github.com/luzhengda/updater/internal/app"
)

// UpdateResult holds the outcome of checking a single app for updates.
type UpdateResult struct {
	App            *app.App
	Source         string
	CurrentVersion string
	LatestVersion  string
	DownloadURL    string
	ReleaseNotes   string
	HasUpdate      bool
	Error          error
}

// Checker is implemented by each update source (Sparkle, Brew, MAS, GitHub).
type Checker interface {
	Name() string
	CanCheck(a *app.App) bool
	Check(ctx context.Context, a *app.App) (*UpdateResult, error)
}

// CmdRunner abstracts shell command execution for testability.
type CmdRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// RealCmdRunner executes real shell commands.
type RealCmdRunner struct{}

// Run executes a command and returns its combined output.
func (r *RealCmdRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// MockCmdRunner returns canned responses for testing.
type MockCmdRunner struct {
	Output []byte
	Err    error
}

// Run returns the pre-configured output and error.
func (m *MockCmdRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return m.Output, m.Err
}
