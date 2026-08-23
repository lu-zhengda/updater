package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestPipxChecker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/black/json" {
			t.Fatalf("PyPI path = %q, want /black/json", r.URL.Path)
		}
		fmt.Fprint(w, `{"info":{"version":"25.1.0"}}`)
	}))
	defer server.Close()

	checker := NewPipxChecker(server.Client(), server.URL)
	installed := &app.App{
		Name:            "black",
		Version:         "24.10.0",
		Source:          app.SourcePipx,
		PipxEnvironment: "black",
		PipxPackage:     "black",
	}
	if checker.Name() != "pipx" || !checker.CanCheck(installed) {
		t.Fatalf("pipx checker identity or CanCheck failed")
	}

	result, err := checker.Check(context.Background(), installed)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.HasUpdate || result.LatestVersion != "25.1.0" || result.Source != "pipx" {
		t.Fatalf("Check() result = %#v, want pipx update to 25.1.0", result)
	}
}

func TestPipxCheckerSkipsUnsafeSources(t *testing.T) {
	checker := NewPipxChecker(nil, "")
	for _, installed := range []*app.App{
		{Name: "pinned", Version: "1.0", Source: app.SourcePipx, PipxEnvironment: "pinned", PipxPackage: "pinned", PipxPinned: true},
		{Name: "vcs", Version: "1.0", Source: app.SourcePipx, PipxEnvironment: "vcs", PipxPackage: "vcs", PipxNonRegistry: true},
	} {
		result, err := checker.Check(context.Background(), installed)
		if err != nil {
			t.Fatalf("Check(%s) error = %v", installed.Name, err)
		}
		if result.HasUpdate || result.LatestVersion != installed.Version {
			t.Fatalf("Check(%s) = %#v, want safely skipped", installed.Name, result)
		}
	}
}

func TestListInstalledPipxPackages(t *testing.T) {
	output := []byte(`{
		"pipx_spec_version": "0.1",
		"venvs": {
			"black": {"metadata": {"main_package": {
				"package": "black", "package_or_url": "black", "package_version": "24.10.0", "pinned": false
			}}},
			"ruff-old": {"metadata": {"main_package": {
				"package": "ruff", "package_or_url": "ruff==0.8.0", "package_version": "0.8.0", "pinned": true
			}}}
		}
	}`)
	packages, err := ListInstalledPipxPackages(context.Background(), &MockCmdRunner{Output: output})
	if err != nil {
		t.Fatalf("ListInstalledPipxPackages() error = %v", err)
	}
	black := packages["black"]
	if black.Package != "black" || black.Version != "24.10.0" || black.NonRegistry || black.Pinned {
		t.Fatalf("black metadata = %#v", black)
	}
	ruff := packages["ruff-old"]
	if ruff.Package != "ruff" || !ruff.NonRegistry || !ruff.Pinned {
		t.Fatalf("ruff metadata = %#v", ruff)
	}
}

func TestListInstalledPipxPackagesSupportsDirectMetadataShape(t *testing.T) {
	output := []byte(`{"venvs":{"ruff":{"main_package":{
		"package":"ruff","package_or_url":"ruff","package_version":"0.9.0"
	}}}}`)
	packages, err := ListInstalledPipxPackages(context.Background(), &MockCmdRunner{Output: output})
	if err != nil {
		t.Fatalf("ListInstalledPipxPackages() error = %v", err)
	}
	if packages["ruff"].Version != "0.9.0" || packages["ruff"].NonRegistry {
		t.Fatalf("direct metadata = %#v", packages["ruff"])
	}
}

func TestListInstalledPipxPackagesErrors(t *testing.T) {
	if _, err := ListInstalledPipxPackages(context.Background(), &MockCmdRunner{Err: fmt.Errorf("pipx missing")}); err == nil {
		t.Fatal("runner error was not returned")
	}
	if _, err := ListInstalledPipxPackages(context.Background(), &MockCmdRunner{Output: []byte(`not json`)}); err == nil {
		t.Fatal("JSON parse error was not returned")
	}
}

func TestIsPlainPipxRegistryPackage(t *testing.T) {
	if !isPlainPipxRegistryPackage("my_tool", "my-tool") {
		t.Fatal("normalized PyPI package was not recognized")
	}
	for _, source := range []string{"my-tool==1.0", "git+https://example.com/tool.git", "/tmp/tool"} {
		if isPlainPipxRegistryPackage("my-tool", source) {
			t.Fatalf("unsafe source %q was recognized as a plain registry package", source)
		}
	}
}
