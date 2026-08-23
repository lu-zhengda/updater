package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

type menubarPackageChecker struct {
	source app.Source
}

func (c *menubarPackageChecker) Name() string { return string(c.source) }
func (c *menubarPackageChecker) CanCheck(a *app.App) bool {
	return a.Source == c.source
}
func (c *menubarPackageChecker) Check(_ context.Context, a *app.App) (*checker.UpdateResult, error) {
	return &checker.UpdateResult{
		App:            a,
		Source:         string(c.source),
		CurrentVersion: a.Version,
		LatestVersion:  "2.0.0",
		HasUpdate:      true,
	}, nil
}

func TestMenubarRunCheckIncludesPnpmAndPipx(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	originalDiscoverAll := discoverAll
	originalNewRunner := newRunner
	originalBuildCheckers := menubarBuildCheckers
	t.Cleanup(func() {
		discoverAll = originalDiscoverAll
		newRunner = originalNewRunner
		menubarBuildCheckers = originalBuildCheckers
	})

	discoverAll = func(_ context.Context, _ *config.Config, _ checker.CmdRunner) ([]*app.App, error) {
		return []*app.App{
			{Name: "typescript", BundleID: "pnpm.global.typescript", Version: "1.0.0", Source: app.SourcePnpm, PnpmPackage: "typescript"},
			{Name: "black", BundleID: "pipx.venv.black", Version: "1.0.0", Source: app.SourcePipx, PipxEnvironment: "black", PipxPackage: "black"},
		}, nil
	}
	newRunner = func() checker.CmdRunner { return &checker.MockCmdRunner{} }
	menubarBuildCheckers = func(checker.CmdRunner, string) []checker.Checker {
		return []checker.Checker{
			&menubarPackageChecker{source: app.SourcePnpm},
			&menubarPackageChecker{source: app.SourcePipx},
		}
	}

	menu := &menubarApp{}
	updates, err := menu.runCheck(context.Background())
	if err != nil {
		t.Fatalf("runCheck() error = %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("runCheck() returned %d updates, want pnpm and pipx", len(updates))
	}
	sources := map[string]bool{}
	for _, update := range updates {
		sources[update.Source] = true
	}
	if !sources["pnpm"] || !sources["pipx"] {
		t.Fatalf("runCheck() sources = %#v, want pnpm and pipx", sources)
	}
	if menu.appCount != 2 {
		t.Fatalf("menubar appCount = %d, want 2", menu.appCount)
	}
}

func TestEnsurePathIncludesPnpmAndPipxLocations(t *testing.T) {
	home := t.TempDir()
	pythonBin := filepath.Join(home, "Library", "Python", "3.13", "bin")
	if err := os.MkdirAll(pythonBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	ensurePath()
	pathEntries := map[string]bool{}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		pathEntries[entry] = true
	}
	for _, expected := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "Library", "pnpm"),
		filepath.Join(home, "Library", "pnpm", "bin"),
		filepath.Join(home, ".local", "share", "pnpm"),
		pythonBin,
	} {
		if !pathEntries[expected] {
			t.Errorf("PATH missing %s", expected)
		}
	}
}
