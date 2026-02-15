package checker

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

	"github.com/luzhengda/updater/internal/app"
)

// ErrOpenedExternally is returned by executeUpdate when the update action
// opens an external app or settings pane instead of performing the update directly.
var ErrOpenedExternally = errors.New("opened externally")

// UpdateResult holds the outcome of checking a single app for updates.
type UpdateResult struct {
	App            *app.App
	Source         string
	CurrentVersion string
	LatestVersion  string
	DownloadURL    string
	ReleaseNotes   string
	HasUpdate      bool
	IsMajorUpdate  bool // true when latest has a higher major version
	StaleSource    bool // true when the source's latest is older than installed
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

// Run executes a command and returns its stdout. Stderr is suppressed
// to prevent it from leaking into TUI output.
func (r *RealCmdRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = io.Discard // suppress stderr to avoid TUI corruption
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

// MultiMockCmdRunner returns different responses based on the command.
// Keys are "name arg1 arg2 ..." strings.
type MultiMockCmdRunner struct {
	Responses map[string]MockResponse
}

// MockResponse holds a single command's output and error.
type MockResponse struct {
	Output []byte
	Err    error
}

// Run looks up the command key and returns its pre-configured response.
// Falls back to empty output and nil error if no match is found.
func (m *MultiMockCmdRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + strings.Join(args, " ")
	}
	if resp, ok := m.Responses[key]; ok {
		return resp.Output, resp.Err
	}
	return nil, nil
}
