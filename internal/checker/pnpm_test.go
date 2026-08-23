package checker

import (
	"context"
	"fmt"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestPnpmChecker(t *testing.T) {
	runner := &MultiMockCmdRunner{Responses: map[string]MockResponse{
		"pnpm outdated -g --format json": {
			Output: []byte(`{"typescript":{"current":"5.0.0","wanted":"5.0.0","latest":"5.7.3"}}`),
		},
	}}
	checker := NewPnpmChecker(runner)
	installed := &app.App{
		Name:        "typescript",
		Version:     "5.0.0",
		Source:      app.SourcePnpm,
		PnpmPackage: "typescript",
	}

	if checker.Name() != "pnpm" {
		t.Fatalf("Name() = %q, want pnpm", checker.Name())
	}
	if !checker.CanCheck(installed) {
		t.Fatal("CanCheck() = false, want true")
	}
	if checker.CanCheck(&app.App{Source: app.SourceNpm, PnpmPackage: "typescript"}) {
		t.Fatal("CanCheck() accepted a non-pnpm source")
	}

	result, err := checker.Check(context.Background(), installed)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.HasUpdate || result.LatestVersion != "5.7.3" || result.Source != "pnpm" {
		t.Fatalf("Check() result = %#v, want pnpm update to 5.7.3", result)
	}
}

func TestPnpmCheckerPackageNotOutdated(t *testing.T) {
	checker := NewPnpmChecker(&MockCmdRunner{Output: []byte(`{}`)})
	installed := &app.App{
		Name:        "eslint",
		Version:     "9.0.0",
		Source:      app.SourcePnpm,
		PnpmPackage: "eslint",
	}

	result, err := checker.Check(context.Background(), installed)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.HasUpdate || result.LatestVersion != "9.0.0" {
		t.Fatalf("Check() result = %#v, want no update", result)
	}
}

func TestPnpmCheckerFallbackToView(t *testing.T) {
	runner := &MultiMockCmdRunner{Responses: map[string]MockResponse{
		"pnpm outdated -g --format json": {
			Err: fmt.Errorf("pnpm outdated failed"),
		},
		"pnpm view eslint version --json": {
			Output: []byte(`"9.17.0"`),
		},
	}}
	checker := NewPnpmChecker(runner)
	installed := &app.App{
		Name:        "eslint",
		Version:     "9.0.0",
		Source:      app.SourcePnpm,
		PnpmPackage: "eslint",
	}

	result, err := checker.Check(context.Background(), installed)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.HasUpdate || result.LatestVersion != "9.17.0" {
		t.Fatalf("Check() result = %#v, want fallback update to 9.17.0", result)
	}
}

func TestPnpmCheckerRejectsMissingPackageName(t *testing.T) {
	checker := NewPnpmChecker(&MockCmdRunner{})
	if _, err := checker.Check(context.Background(), &app.App{Name: "missing", Source: app.SourcePnpm}); err == nil {
		t.Fatal("Check() error = nil, want missing package error")
	}
}

func TestListInstalledPnpmPackages(t *testing.T) {
	output := []byte(`[
		{
			"path": "/tmp/pnpm/global/v11",
			"dependencies": {
				"typescript": {"version": "5.7.3"},
				"@scope/tool": {"version": "2.4.1"},
				"missing-version": {}
			}
		}
	]`)
	packages, err := ListInstalledPnpmPackages(context.Background(), &MockCmdRunner{Output: output})
	if err != nil {
		t.Fatalf("ListInstalledPnpmPackages() error = %v", err)
	}
	if len(packages) != 2 || packages["typescript"] != "5.7.3" || packages["@scope/tool"] != "2.4.1" {
		t.Fatalf("ListInstalledPnpmPackages() = %#v", packages)
	}
}

func TestListInstalledPnpmPackagesErrors(t *testing.T) {
	if _, err := ListInstalledPnpmPackages(context.Background(), &MockCmdRunner{Err: fmt.Errorf("pnpm missing")}); err == nil {
		t.Fatal("runner error was not returned")
	}
	if _, err := ListInstalledPnpmPackages(context.Background(), &MockCmdRunner{Output: []byte(`not json`)}); err == nil {
		t.Fatal("JSON parse error was not returned")
	}
}
