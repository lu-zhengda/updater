package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

// mockChecker implements checker.Checker for testing.
type mockChecker struct {
	name     string
	canCheck func(*app.App) bool
	result   *checker.UpdateResult
	err      error
}

func (m *mockChecker) Name() string             { return m.name }
func (m *mockChecker) CanCheck(a *app.App) bool { return m.canCheck(a) }
func (m *mockChecker) Check(_ context.Context, a *app.App) (*checker.UpdateResult, error) {
	return m.result, m.err
}

func mockCaskApp(t *testing.T, token, bundle string) (root, installed string) {
	t.Helper()
	root = t.TempDir()
	installed = filepath.Join(t.TempDir(), bundle)
	link := filepath.Join(root, token, "1.0.0", bundle)
	for _, dir := range []string{installed, filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(installed, link); err != nil {
		t.Fatal(err)
	}
	return root, installed
}

// --- DiscoverBrewFormulae ---

func TestDiscoverAll_OptionalPackageManagers(t *testing.T) {
	// Exercise real executable lookup without depending on installed managers.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for _, tt := range []struct {
		name     string
		runner   checker.CmdRunner
		wantWarn bool
	}{
		{"absent", &checker.RealCmdRunner{}, false},
		{"wrapped absent", &checker.MockCmdRunner{Err: fmt.Errorf("wrapped: %w", &exec.Error{Name: "manager", Err: exec.ErrNotFound})}, false},
		{"command failure", &checker.MockCmdRunner{Err: &exec.ExitError{}}, true},
		{"permission denied", &checker.MockCmdRunner{Err: os.ErrPermission}, true},
		{"timeout", &checker.MockCmdRunner{Err: context.DeadlineExceeded}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			if err != nil {
				t.Fatal(err)
			}
			originalStderr := os.Stderr
			os.Stderr = stderr
			t.Cleanup(func() { os.Stderr = originalStderr; stderr.Close() })

			var warnings []string
			_, err = DiscoverAll(context.Background(), &config.Config{}, tt.runner, func(source string, _ error) {
				warnings = append(warnings, source)
			})
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if tt.wantWarn {
				want = []string{"brew formulae", "brew CLI casks", "npm packages", "pnpm packages", "pipx applications", "uv tools", "cargo crates"}
			}
			if !reflect.DeepEqual(warnings, want) {
				t.Errorf("warnings = %v, want %v", warnings, want)
			}
			output, err := os.ReadFile(stderr.Name())
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantWarn {
				if !strings.Contains(string(output), "warning: could not inspect Homebrew app ownership:") {
					t.Errorf("missing cask warning: %s", output)
				}
			} else if len(output) != 0 {
				t.Errorf("unexpected stderr: %s", output)
			}
		})
	}
}

func TestDiscoverBrewFormulae(t *testing.T) {
	tests := []struct {
		name      string
		output    []byte
		err       error
		wantCount int
		wantNames []string
		wantErr   bool
	}{
		{
			name:      "two formulae sorted alphabetically",
			output:    []byte("node 22.12.0\npython@3.12 3.12.8\n"),
			wantCount: 2,
			wantNames: []string{"node", "python@3.12"},
		},
		{
			name:      "single formula",
			output:    []byte("git 2.43.0\n"),
			wantCount: 1,
			wantNames: []string{"git"},
		},
		{
			name:      "empty output",
			output:    []byte(""),
			wantCount: 0,
		},
		{
			name:    "runner error",
			err:     fmt.Errorf("brew not installed"),
			wantErr: true,
		},
		{
			name:      "multiple versions per formula takes last",
			output:    []byte("node 20.0.0 22.12.0\n"),
			wantCount: 1,
			wantNames: []string{"node"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &checker.MockCmdRunner{Output: tt.output, Err: tt.err}
			apps, err := DiscoverBrewFormulae(context.Background(), runner)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(apps) != tt.wantCount {
				t.Fatalf("got %d apps, want %d", len(apps), tt.wantCount)
			}

			for i, wantName := range tt.wantNames {
				a := apps[i]
				if a.Name != wantName {
					t.Errorf("app[%d].Name = %q, want %q", i, a.Name, wantName)
				}
				if a.BundleID != "homebrew.formula."+wantName {
					t.Errorf("app[%d].BundleID = %q, want %q", i, a.BundleID, "homebrew.formula."+wantName)
				}
				if a.Source != app.SourceBrewFormula {
					t.Errorf("app[%d].Source = %q, want %q", i, a.Source, app.SourceBrewFormula)
				}
				if a.FormulaName != wantName {
					t.Errorf("app[%d].FormulaName = %q, want %q", i, a.FormulaName, wantName)
				}
				if !a.InstalledViaBrew {
					t.Errorf("app[%d].InstalledViaBrew = false, want true", i)
				}
			}
		})
	}
}

func TestDiscoverBrewFormulae_VersionCorrectness(t *testing.T) {
	runner := &checker.MockCmdRunner{
		Output: []byte("node 20.0.0 22.12.0\npython@3.12 3.12.8\n"),
	}
	apps, err := DiscoverBrewFormulae(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("got %d apps, want 2", len(apps))
	}
	// node should take last version "22.12.0"
	if apps[0].Version != "22.12.0" {
		t.Errorf("node version = %q, want %q", apps[0].Version, "22.12.0")
	}
	if apps[1].Version != "3.12.8" {
		t.Errorf("python version = %q, want %q", apps[1].Version, "3.12.8")
	}
}

func TestTappedFormulaUsesQualifiedNameAndKeepsPackageID(t *testing.T) {
	ctx := context.Background()
	runner := &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
		"brew list --formula --versions":  {Output: []byte("camsnap 0.6.2\n")},
		"brew list --formula --full-name": {Output: []byte("steipete/tap/camsnap\n")},
		"brew outdated --formula --json":  {Output: []byte(`{"formulae":[{"name":"other/tap/camsnap","current_version":"9.0.0"},{"name":"steipete/tap/camsnap","current_version":"0.6.3"}]}`)},
	}}
	apps, err := DiscoverBrewFormulae(ctx, runner)
	if err != nil || len(apps) != 1 {
		t.Fatalf("formula discovery = %v, %v", apps, err)
	}
	a := apps[0]
	if a.BundleID != "homebrew.formula.camsnap" || a.Name != "camsnap" || a.FormulaName != "steipete/tap/camsnap" {
		t.Fatalf("unexpected package identity: %+v", a)
	}
	cfg := &config.Config{GitHubMappings: map[string]string{a.BundleID: "example/camsnap"}}
	if _, err = EnrichApps(ctx, apps, cfg, runner); err != nil {
		t.Fatal(err)
	}
	alternate := &mockChecker{name: "github", canCheck: func(a *app.App) bool { return a.GitHubRepo != "" },
		result: &checker.UpdateResult{Source: "github", LatestVersion: "0.7.0", HasUpdate: true, DownloadURL: "https://example.com/camsnap.zip"}}
	result := CheckWithFallthrough(ctx, a, []checker.Checker{alternate, checker.NewBrewFormulaChecker(runner)})
	if result.Error != nil || result.Source != "formula" || !result.HasUpdate || result.LatestVersion != "0.6.3" {
		t.Fatalf("formula check = %+v", result)
	}
}

func TestCLICasksHaveTheirOwnEntries(t *testing.T) {
	ctx := context.Background()
	caskroom, codexbar := mockCaskApp(t, "codexbar", "CodexBar.app")
	runner := &checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{
		"brew info --installed --cask --json=v2": {Output: []byte(`{"casks":[
			{"token":"codex","full_token":"owner/tap/codex","installed":"0.169.0","version":"0.170.0","artifacts":[{"binary":["bin/codex"]}]},
			{"token":"codexbar","full_token":"codexbar","installed":"1.0","artifacts":[{"binary":["bin/codexbar"]},{"app":["CodexBar.app"]}]},
			{"token":"isabelle","full_token":"isabelle","installed":"2025-2","artifacts":[{"artifact":["Isabelle2025-2.app",{"target":"/Applications/Isabelle2025-2.app"}],"target":"/Applications/Isabelle2025-2.app"},{"binary":["/Applications/Isabelle2025-2.app/bin/isabelle"]}]},
			{"token":"font-sample","full_token":"font-sample","installed":"1.0","artifacts":[{"font":["sample.ttf"]}]}
		]}`)},
		"brew --caskroom":                      {Output: []byte(caskroom)},
		"brew outdated --cask --greedy --json": {Output: []byte(`{"casks":[{"name":"codex","current_version":"0.170.0"}]}`)},
	}}
	cli, err := discoverBrewCLICasks(ctx, runner)
	if err != nil || len(cli) != 1 {
		t.Fatalf("CLI cask discovery = %v, %v", cli, err)
	}
	a := cli[0]
	if a.BundleID != "homebrew.cask.owner/tap/codex" || !IsPackageID(a.BundleID) || a.Path != "" || a.Version != "0.169.0" {
		t.Fatalf("unexpected CLI cask identity: %+v", a)
	}
	desktop := &app.App{Name: "ChatGPT", BundleID: "com.openai.codex", Path: "/Applications/ChatGPT.app", Source: app.SourceSparkle}
	hybrid := &app.App{Name: "CodexBar", BundleID: "test.codexbar", Path: codexbar, Source: app.SourceUnknown}
	if _, err := EnrichApps(ctx, append(cli, desktop, hybrid), &config.Config{}, runner); err != nil {
		t.Fatal(err)
	}
	if desktop.CaskName != "" || desktop.InstalledViaBrew || desktop.Source != app.SourceSparkle {
		t.Fatalf("desktop app was associated with the CLI cask: %+v", desktop)
	}
	if hybrid.CaskName != "codexbar" || !hybrid.InstalledViaBrew {
		t.Fatalf("desktop cask association was lost: %+v", hybrid)
	}
	result, err := checker.NewBrewChecker(runner).Check(ctx, a)
	if err != nil || !result.HasUpdate || result.LatestVersion != "0.170.0" {
		t.Fatalf("CLI cask check = %+v, %v", result, err)
	}
}

func TestDiscoverUvTools(t *testing.T) {
	runner := &checker.MockCmdRunner{
		Output: []byte(`black v25.1.0
- black
- blackd
ruff v0.8.4
- ruff
`),
	}
	apps, err := DiscoverUvTools(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("got %d apps, want 2", len(apps))
	}
	// Sorted alphabetically.
	if apps[0].Name != "black" || apps[0].UvTool != "black" {
		t.Errorf("apps[0] = %+v, want black/black", apps[0])
	}
	if apps[0].Source != app.SourceUv {
		t.Errorf("apps[0].Source = %q, want %q", apps[0].Source, app.SourceUv)
	}
	if apps[0].BundleID != "uv.tool.black" {
		t.Errorf("apps[0].BundleID = %q, want uv.tool.black", apps[0].BundleID)
	}
	if apps[1].Name != "ruff" || apps[1].Version != "0.8.4" {
		t.Errorf("apps[1] = %+v, want ruff/0.8.4", apps[1])
	}
}

func TestDiscoverUvTools_RunnerError(t *testing.T) {
	runner := &checker.MockCmdRunner{Err: fmt.Errorf("uv not installed")}
	_, err := DiscoverUvTools(context.Background(), runner)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDiscoverPnpmPackages(t *testing.T) {
	runner := &checker.MockCmdRunner{Output: []byte(`[
		{"dependencies": {
			"typescript": {"version": "5.7.3"},
			"eslint": {"version": "9.17.0"}
		}}
	]`)}
	apps, err := DiscoverPnpmPackages(context.Background(), runner)
	if err != nil {
		t.Fatalf("DiscoverPnpmPackages() error = %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("got %d apps, want 2", len(apps))
	}
	if apps[0].Name != "eslint" || apps[0].BundleID != "pnpm.global.eslint" ||
		apps[0].Source != app.SourcePnpm || apps[0].PnpmPackage != "eslint" {
		t.Fatalf("apps[0] = %#v, want sorted pnpm eslint entry", apps[0])
	}
	if apps[1].Name != "typescript" || apps[1].Version != "5.7.3" {
		t.Fatalf("apps[1] = %#v, want typescript 5.7.3", apps[1])
	}
}

func TestDiscoverPipxPackages(t *testing.T) {
	runner := &checker.MockCmdRunner{Output: []byte(`{"venvs":{
		"ruff":{"metadata":{"main_package":{
			"package":"ruff","package_or_url":"ruff","package_version":"0.9.0","pinned":false
		}}}
	}}`)}
	apps, err := DiscoverPipxPackages(context.Background(), runner)
	if err != nil {
		t.Fatalf("DiscoverPipxPackages() error = %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("got %d apps, want 1", len(apps))
	}
	got := apps[0]
	if got.Name != "ruff" || got.BundleID != "pipx.venv.ruff" || got.Source != app.SourcePipx ||
		got.PipxEnvironment != "ruff" || got.PipxPackage != "ruff" || got.Version != "0.9.0" {
		t.Fatalf("pipx app = %#v", got)
	}
}

// --- EnrichApps ---

func TestEnrichApps_Phase1_GitHubMapping(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{
			"com.test.app": "owner/repo",
		},
	}
	apps := []*app.App{
		{Name: "TestApp", BundleID: "com.test.app", Source: app.SourceUnknown},
		{Name: "OtherApp", BundleID: "com.other.app", Source: app.SourceSparkle},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// App with config mapping should get GitHubRepo set and source changed.
	if result[0].GitHubRepo != "owner/repo" {
		t.Errorf("GitHubRepo = %q, want %q", result[0].GitHubRepo, "owner/repo")
	}
	if result[0].Source != app.SourceGitHub {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceGitHub)
	}

	// App without mapping should be unchanged.
	if result[1].GitHubRepo != "" {
		t.Errorf("OtherApp.GitHubRepo = %q, want empty", result[1].GitHubRepo)
	}
}

func TestEnrichApps_Phase1_GitHubMapping_NonUnknownSource(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{
			"com.sparkle.app": "owner/sparkle-repo",
		},
	}
	apps := []*app.App{
		{Name: "SparkleApp", BundleID: "com.sparkle.app", Source: app.SourceSparkle},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// GitHubRepo should be set but source should NOT change from sparkle.
	if result[0].GitHubRepo != "owner/sparkle-repo" {
		t.Errorf("GitHubRepo = %q, want %q", result[0].GitHubRepo, "owner/sparkle-repo")
	}
	if result[0].Source != app.SourceSparkle {
		t.Errorf("Source = %q, want %q (should not change)", result[0].Source, app.SourceSparkle)
	}
}

func TestEnrichApps_Phase2_CaskMapping(t *testing.T) {
	cfg := &config.Config{
		CaskMappings: map[string]string{
			"com.test.app2": "test-cask",
		},
	}
	caskroom, bundle := mockCaskApp(t, "test-cask", "TestApp2.app")
	apps := []*app.App{
		{Name: "TestApp2", BundleID: "com.test.app2", Path: bundle, Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte(caskroom)},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].CaskName != "test-cask" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "test-cask")
	}
	if !result[0].InstalledViaBrew {
		t.Error("InstalledViaBrew = false, want true")
	}
	if result[0].Source != app.SourceBrew {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceBrew)
	}
}

func TestEnrichApps_Phase3_InstalledCask(t *testing.T) {
	cfg := &config.Config{}
	caskroom, bundle := mockCaskApp(t, "firefox", "Firefox.app")
	apps := []*app.App{
		{Name: "Firefox", BundleID: "org.mozilla.firefox", Path: bundle, Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte(caskroom)},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].CaskName != "firefox" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "firefox")
	}
	if !result[0].InstalledViaBrew {
		t.Error("InstalledViaBrew = false, want true")
	}
	if result[0].Source != app.SourceBrew {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceBrew)
	}
}

func TestEnrichApps_Phase3_InstalledElectronCask(t *testing.T) {
	cfg := &config.Config{}
	caskroom, bundle := mockCaskApp(t, "my-electron-app", "My Electron App.app")
	apps := []*app.App{
		{Name: "My Electron App", BundleID: "com.electron.app", Path: bundle, Source: app.SourceElectron},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte(caskroom)},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].CaskName != "my-electron-app" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "my-electron-app")
	}
	if !result[0].InstalledViaBrew {
		t.Error("InstalledViaBrew = false, want true")
	}
	// Source should remain SourceElectron (only SourceUnknown changes to SourceBrew).
	if result[0].Source != app.SourceElectron {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceElectron)
	}
}

func TestEnrichApps_Phase3_ElectronWithUpdateURL_DetectsInstalledCask(t *testing.T) {
	cfg := &config.Config{}
	caskroom, bundle := mockCaskApp(t, "electron-with-url", "Electron With URL.app")
	apps := []*app.App{
		{
			Name:              "Electron With URL",
			BundleID:          "com.electron.withurl",
			Source:            app.SourceElectron,
			Path:              bundle,
			ElectronUpdateURL: "https://update.example.com",
		},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte(caskroom)},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].CaskName != "electron-with-url" || !result[0].InstalledViaBrew {
		t.Errorf("expected installed cask for Electron app with update URL, got %+v", result[0])
	}
}

func TestEnrichApps_Phase4_CaskProbe_NotFound(t *testing.T) {
	cfg := &config.Config{}
	// Path set so CaskCandidates produces a predictable, controlled set:
	// basename "Unknown App" → "unknown-app", display "Unknown App" → "unknown-app" (dedup),
	// bundle last "app" → "app", bundle second-to-last "unknown" → "unknown".
	apps := []*app.App{
		{
			Name:     "Unknown App",
			BundleID: "com.unknown.app",
			Source:   app.SourceUnknown,
			Path:     "/Applications/Unknown App.app",
		},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom":                        {Output: []byte("")},
			"brew info --cask --json=v2 unknown-app": {Err: fmt.Errorf("exit 1")},
			"brew info --cask --json=v2 app":         {Err: fmt.Errorf("exit 1")},
			"brew info --cask --json=v2 unknown":     {Err: fmt.Errorf("exit 1")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All candidates failed → CaskName should remain empty.
	if result[0].CaskName != "" {
		t.Errorf("CaskName = %q, want empty (all cask probes failed)", result[0].CaskName)
	}
}

func TestEnrichApps_Phase4_CaskProbe_Found(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{Name: "Discoverable App", BundleID: "com.discoverable.app", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")}, // not installed
			"brew info --cask --json=v2 discoverable-app": {Output: []byte(`{"casks":[{"token":"discoverable-app"}]}`)},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CaskExists returns true → CaskName should remain.
	if result[0].CaskName != "discoverable-app" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "discoverable-app")
	}
}

func TestEnrichApps_Phase4_ConfigCaskMapping_SkipsProbe(t *testing.T) {
	cfg := &config.Config{
		CaskMappings: map[string]string{
			"com.mapped.app": "custom-cask",
		},
	}
	apps := []*app.App{
		{Name: "Mapped App", BundleID: "com.mapped.app", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
			// No brew info response — probe should be skipped.
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Config-mapped cask should be preserved even without probe.
	if result[0].CaskName != "custom-cask" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "custom-cask")
	}
}

func TestEnrichApps_CaskroomError(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{Name: "TestApp", BundleID: "com.test.app", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Err: fmt.Errorf("brew not installed")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Non-fatal: apps returned unchanged.
	if len(result) != 1 {
		t.Fatalf("got %d apps, want 1", len(result))
	}
	if result[0].CaskName != "" {
		t.Errorf("CaskName = %q, want empty (brew not available)", result[0].CaskName)
	}
}

func TestEnrichApps_Phase4_ElectronProbe(t *testing.T) {
	cfg := &config.Config{}
	// CaskCandidates for this app (Path set for predictability):
	// basename "Electron No Meta" → "electron-no-meta",
	// display same → dedup,
	// bundle last "nometa" → "nometa",
	// bundle second-to-last "electron" → "electron".
	apps := []*app.App{
		{
			Name:     "Electron No Meta",
			BundleID: "com.electron.nometa",
			Source:   app.SourceElectron,
			Path:     "/Applications/Electron No Meta.app",
			// No ElectronUpdateURL, no GitHubRepo
		},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
			"brew info --cask --json=v2 electron-no-meta": {Err: fmt.Errorf("not found")},
			"brew info --cask --json=v2 nometa":           {Err: fmt.Errorf("not found")},
			"brew info --cask --json=v2 electron":         {Err: fmt.Errorf("not found")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All candidates failed → CaskName remains empty.
	if result[0].CaskName != "" {
		t.Errorf("CaskName = %q, want empty (all probes failed)", result[0].CaskName)
	}
}

func TestEnrichApps_Phase4_SparkleProbe(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{
			Name:     "PDF Expert",
			BundleID: "com.readdle.PDFExpert-Mac",
			Source:   app.SourceSparkle,
			Path:     "/Applications/PDF Expert.app",
			FeedURL:  "https://downloads.example.com/appcast.xml",
		},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")}, // not installed via brew
			"brew info --cask --json=v2 pdf-expert": {
				Output: []byte(`{"casks":[{"token":"pdf-expert","version":"3.12.0"}]}`),
			},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].CaskName != "pdf-expert" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "pdf-expert")
	}
	// Still not brew-installed, so source should remain sparkle.
	if result[0].Source != app.SourceSparkle {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceSparkle)
	}
	if result[0].InstalledViaBrew {
		t.Error("InstalledViaBrew = true, want false")
	}
}

// --- EnrichApps: multi-signal cask resolution ---

// TestEnrichApps_VSCode_GetsCaskViaBasename verifies that an app whose display
// name ("Code") does not match its cask token ("visual-studio-code") is still
// resolved correctly because the .app bundle basename ("Visual Studio Code")
// produces the right token via ToCaskName.
func TestEnrichApps_VSCode_GetsCaskViaBasename(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{
			// Mirrors real VSCode: display name "Code", but .app file is
			// "Visual Studio Code.app" → basename candidate resolves correctly.
			Name:     "Code",
			BundleID: "com.microsoft.VSCode",
			Source:   app.SourceElectron,
			Path:     "/Applications/Visual Studio Code.app",
			// No ElectronUpdateURL, no GitHubRepo (generic provider in app-update.yml)
		},
	}
	// Candidates: "visual-studio-code" (basename), "code" (display),
	//             "vscode" (last segment), "microsoft" (second-to-last).
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")}, // not installed via brew
			// "visual-studio-code" probe succeeds → should be selected.
			"brew info --cask --json=v2 visual-studio-code": {
				Output: []byte(`{"casks":[{"token":"visual-studio-code","version":"1.87.0"}]}`),
			},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].CaskName != "visual-studio-code" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "visual-studio-code")
	}
	// BrewInfoChecker should now be able to handle this app.
	brewInfoChecker := checker.NewBrewInfoChecker(&checker.MockCmdRunner{})
	if !brewInfoChecker.CanCheck(result[0]) {
		t.Error("BrewInfoChecker.CanCheck = false, want true (app has CaskName)")
	}
}

// TestEnrichApps_GitHubDesktop_GetsCaskViaBundleIDSegment verifies that GitHub
// Desktop — whose display name ("GitHub Desktop") and bundle basename both
// produce "github-desktop" while the actual cask token is "github" — is
// resolved via the bundle ID second-to-last segment ("com.github.GitHubClient"
// → "github").
func TestEnrichApps_GitHubDesktop_GetsCaskViaBundleIDSegment(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{
			Name:     "GitHub Desktop",
			BundleID: "com.github.GitHubClient",
			Source:   app.SourceElectron,
			Path:     "/Applications/GitHub Desktop.app",
		},
	}
	// Candidates: "github-desktop" (basename), "github-desktop" (display, dedup),
	//             "githubclient" (last segment), "github" (second-to-last).
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
			// "github-desktop" and "githubclient" don't exist as casks.
			"brew info --cask --json=v2 github-desktop": {Err: fmt.Errorf("exit 1")},
			"brew info --cask --json=v2 githubclient":   {Err: fmt.Errorf("exit 1")},
			// "github" is the real cask token.
			"brew info --cask --json=v2 github": {
				Output: []byte(`{"casks":[{"token":"github","version":"3.4.5"}]}`),
			},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].CaskName != "github" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "github")
	}
	brewInfoChecker := checker.NewBrewInfoChecker(&checker.MockCmdRunner{})
	if !brewInfoChecker.CanCheck(result[0]) {
		t.Error("BrewInfoChecker.CanCheck = false, want true (app has CaskName)")
	}
}

// TestEnrichApps_GenericElectron_GetsCaskViaDisplayName verifies that a
// generic Electron app whose display name happens to match its cask token
// directly is resolved on the second candidate (display name), proving the
// mechanism is not special-cased to specific apps.
func TestEnrichApps_GenericElectron_GetsCaskViaDisplayName(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{
			// App whose .app filename differs from display name in a way that
			// doesn't match the cask, but the display name does.
			// e.g. the bundle is stored as "Acme Helper.app" but the cask is "acme".
			Name:     "Acme",
			BundleID: "com.acme.acmeapp",
			Source:   app.SourceElectron,
			Path:     "/Applications/Acme Helper.app",
		},
	}
	// Candidates: "acme-helper" (basename), "acme" (display), "acmeapp" (last),
	//             "acme" (second-to-last, dedup with display).
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew --caskroom": {Output: []byte("")},
			// basename "acme-helper" does not exist as a cask.
			"brew info --cask --json=v2 acme-helper": {Err: fmt.Errorf("exit 1")},
			// display-name candidate "acme" is the correct cask.
			"brew info --cask --json=v2 acme": {
				Output: []byte(`{"casks":[{"token":"acme","version":"2.1.0"}]}`),
			},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result[0].CaskName != "acme" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "acme")
	}
	brewInfoChecker := checker.NewBrewInfoChecker(&checker.MockCmdRunner{})
	if !brewInfoChecker.CanCheck(result[0]) {
		t.Error("BrewInfoChecker.CanCheck = false, want true (app has CaskName)")
	}
}

// --- FilterIgnored ---

func TestFilterIgnored(t *testing.T) {
	tests := []struct {
		name      string
		apps      []*app.App
		ignored   []string
		wantCount int
		wantNames []string
	}{
		{
			name: "filters ignored apps",
			apps: []*app.App{
				{Name: "Keep1", BundleID: "com.keep1"},
				{Name: "Ignore1", BundleID: "com.ignore1"},
				{Name: "Keep2", BundleID: "com.keep2"},
				{Name: "Ignore2", BundleID: "com.ignore2"},
			},
			ignored:   []string{"com.ignore1", "com.ignore2"},
			wantCount: 2,
			wantNames: []string{"Keep1", "Keep2"},
		},
		{
			name: "no ignored apps",
			apps: []*app.App{
				{Name: "App1", BundleID: "com.app1"},
				{Name: "App2", BundleID: "com.app2"},
			},
			ignored:   nil,
			wantCount: 2,
			wantNames: []string{"App1", "App2"},
		},
		{
			name:      "empty apps list",
			apps:      []*app.App{},
			ignored:   []string{"com.something"},
			wantCount: 0,
		},
		{
			name: "all ignored",
			apps: []*app.App{
				{Name: "App1", BundleID: "com.app1"},
			},
			ignored:   []string{"com.app1"},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				IgnoredApps: tt.ignored,
			}
			// Build the internal ignored set by saving and reloading-style init.
			// Use a helper approach: create config with buildIgnoredSet called.
			// Since buildIgnoredSet is unexported, we use Load with a temp file or
			// create a config that has the ignoredSet pre-populated.
			// Actually, IsIgnored checks ignoredSet which is nil unless built.
			// For testing, we can use the Load function or just create directly.
			// Let's use a workaround: save to temp, load back.
			if len(tt.ignored) > 0 {
				tmpDir := t.TempDir()
				path := tmpDir + "/config.yaml"
				if err := cfg.Save(path); err != nil {
					t.Fatalf("failed to save config: %v", err)
				}
				loaded, err := config.Load(path)
				if err != nil {
					t.Fatalf("failed to load config: %v", err)
				}
				cfg = loaded
			}

			result := FilterIgnored(tt.apps, cfg)
			if len(result) != tt.wantCount {
				t.Fatalf("got %d apps, want %d", len(result), tt.wantCount)
			}
			for i, wantName := range tt.wantNames {
				if result[i].Name != wantName {
					t.Errorf("result[%d].Name = %q, want %q", i, result[i].Name, wantName)
				}
			}
		})
	}
}

// --- BuildCheckers ---

func TestBuildCheckers(t *testing.T) {
	runner := &checker.MockCmdRunner{}
	checkers := BuildCheckers(runner, "test-token")

	if len(checkers) != 15 {
		t.Fatalf("got %d checkers, want 15", len(checkers))
	}

	expectedNames := []string{
		"sparkle",
		"vendor",
		"brew",
		"mas",
		"github",
		"system",
		"formula",
		"electron",
		"npm",
		"pnpm",
		"pipx",
		"uv",
		"cargo",
		"managed",
		"brew-info",
	}

	for i, want := range expectedNames {
		if checkers[i].Name() != want {
			t.Errorf("checkers[%d].Name() = %q, want %q", i, checkers[i].Name(), want)
		}
	}
	if checkers[4].(*checker.GitHubChecker).ReadBundleVersion == nil {
		t.Fatal("GitHub checker has no bundle version reader")
	}
}

// --- CheckAll ---

func TestCheckAll(t *testing.T) {
	always := func(_ *app.App) bool { return true }
	never := func(_ *app.App) bool { return false }

	apps := []*app.App{
		{Name: "App1", BundleID: "com.app1", Version: "1.0"},
		{Name: "App2", BundleID: "com.app2", Version: "2.0"},
		{Name: "App3", BundleID: "com.app3", Version: "3.0"},
	}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "mock",
			canCheck: always,
			result: &checker.UpdateResult{
				Source:        "mock",
				LatestVersion: "9.0",
				HasUpdate:     true,
			},
		},
	}

	results := CheckAll(context.Background(), apps, checkers, 2)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// Verify all results have the mock source.
	for _, r := range results {
		if r.Source != "mock" {
			t.Errorf("result for %s has Source = %q, want %q", r.App.Name, r.Source, "mock")
		}
	}

	// Test with no compatible checker — app should be skipped.
	noCheckerApps := []*app.App{
		{Name: "Skipped", BundleID: "com.skipped", Version: "1.0"},
	}
	noResults := CheckAll(context.Background(), noCheckerApps, []checker.Checker{
		&mockChecker{name: "mock", canCheck: never},
	}, 5)
	if len(noResults) != 0 {
		t.Errorf("got %d results, want 0 (no compatible checker)", len(noResults))
	}
}

func TestCheckAll_DefaultConcurrency(t *testing.T) {
	apps := []*app.App{
		{Name: "App1", BundleID: "com.app1", Version: "1.0"},
	}
	checkers := []checker.Checker{
		&mockChecker{
			name:     "mock",
			canCheck: func(_ *app.App) bool { return true },
			result:   &checker.UpdateResult{Source: "mock", LatestVersion: "2.0"},
		},
	}

	// maxConcurrency=0 should default to 10 and not panic.
	results := CheckAll(context.Background(), apps, checkers, 0)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestCheckAll_NegativeConcurrency(t *testing.T) {
	apps := []*app.App{
		{Name: "App1", BundleID: "com.app1", Version: "1.0"},
	}
	checkers := []checker.Checker{
		&mockChecker{
			name:     "mock",
			canCheck: func(_ *app.App) bool { return true },
			result:   &checker.UpdateResult{Source: "mock", LatestVersion: "2.0"},
		},
	}

	// maxConcurrency=-1 should default to 10 and not panic.
	results := CheckAll(context.Background(), apps, checkers, -1)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestCheckAll_EmptyApps(t *testing.T) {
	checkers := []checker.Checker{
		&mockChecker{
			name:     "mock",
			canCheck: func(_ *app.App) bool { return true },
			result:   &checker.UpdateResult{Source: "mock"},
		},
	}
	results := CheckAll(context.Background(), nil, checkers, 5)
	if len(results) != 0 {
		t.Errorf("got %d results, want 0 for empty apps", len(results))
	}
}

// --- CheckWithFallthrough ---

func TestCheckWithFallthrough_Success(t *testing.T) {
	a := &app.App{Name: "TestApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "first",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "first",
				CurrentVersion: "1.0",
				LatestVersion:  "2.0",
				HasUpdate:      true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "first" {
		t.Errorf("Source = %q, want %q", result.Source, "first")
	}
	if result.LatestVersion != "2.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.0")
	}
}

func TestCheckWithFallthrough_AllError(t *testing.T) {
	a := &app.App{Name: "ErrorApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "failing-checker",
			canCheck: func(_ *app.App) bool { return true },
			err:      fmt.Errorf("network timeout"),
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Source != "failing-checker" {
		t.Errorf("Source = %q, want %q", result.Source, "failing-checker")
	}
	if result.CurrentVersion != "1.0" {
		t.Errorf("CurrentVersion = %q, want %q", result.CurrentVersion, "1.0")
	}
}

func TestCheckWithFallthrough_MultipleErrors_LastErrorReturned(t *testing.T) {
	a := &app.App{Name: "MultiErrorApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "checker-1",
			canCheck: func(_ *app.App) bool { return true },
			err:      fmt.Errorf("first error"),
		},
		&mockChecker{
			name:     "checker-2",
			canCheck: func(_ *app.App) bool { return true },
			err:      fmt.Errorf("second error"),
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Source != "checker-2" {
		t.Errorf("Source = %q, want %q (should be last checker)", result.Source, "checker-2")
	}
}

func TestCheckWithFallthrough_AllStale(t *testing.T) {
	a := &app.App{Name: "StaleApp", Version: "3.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "stale-1",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "stale-1",
				CurrentVersion: "3.0",
				LatestVersion:  "2.0",
				StaleSource:    true,
			},
		},
		&mockChecker{
			name:     "stale-2",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "stale-2",
				CurrentVersion: "3.0",
				LatestVersion:  "1.0",
				StaleSource:    true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected error for all-stale, got nil")
	}
	expected := "all 2 source(s) returned stale data for StaleApp"
	if result.Error.Error() != expected {
		t.Errorf("Error = %q, want %q", result.Error.Error(), expected)
	}
	if result.Source != "unknown" {
		t.Errorf("Source = %q, want %q", result.Source, "unknown")
	}
}

func TestCheckWithFallthrough_NoChecker(t *testing.T) {
	a := &app.App{Name: "Orphan", Version: "1.0"}

	// No checker can handle this app.
	checkers := []checker.Checker{
		&mockChecker{
			name:     "incompatible",
			canCheck: func(_ *app.App) bool { return false },
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected error for no-checker, got nil")
	}
	expected := "no checker could provide a result for Orphan"
	if result.Error.Error() != expected {
		t.Errorf("Error = %q, want %q", result.Error.Error(), expected)
	}
	if result.Source != "unknown" {
		t.Errorf("Source = %q, want %q", result.Source, "unknown")
	}
}

func TestCheckWithFallthrough_EmptyCheckers(t *testing.T) {
	a := &app.App{Name: "NoCheckers", Version: "1.0"}

	result := CheckWithFallthrough(context.Background(), a, nil)
	if result.Error == nil {
		t.Fatal("expected error for empty checkers, got nil")
	}
	expected := "no checker could provide a result for NoCheckers"
	if result.Error.Error() != expected {
		t.Errorf("Error = %q, want %q", result.Error.Error(), expected)
	}
}

func TestCheckWithFallthrough_ErrorThenSuccess(t *testing.T) {
	a := &app.App{Name: "RecoverApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "failing",
			canCheck: func(_ *app.App) bool { return true },
			err:      fmt.Errorf("temporary error"),
		},
		&mockChecker{
			name:     "working",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "working",
				CurrentVersion: "1.0",
				LatestVersion:  "2.0",
				HasUpdate:      true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "working" {
		t.Errorf("Source = %q, want %q", result.Source, "working")
	}
}

func TestCheckWithFallthrough_StaleThenSuccess(t *testing.T) {
	a := &app.App{Name: "StaleRecoverApp", Version: "2.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "stale",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:         a,
				Source:      "stale",
				StaleSource: true,
			},
		},
		&mockChecker{
			name:     "fresh",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "fresh",
				CurrentVersion: "2.0",
				LatestVersion:  "3.0",
				HasUpdate:      true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "fresh" {
		t.Errorf("Source = %q, want %q", result.Source, "fresh")
	}
}

func TestCheckWithFallthrough_SkipsIncompatible(t *testing.T) {
	a := &app.App{Name: "PartialApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "incompatible",
			canCheck: func(_ *app.App) bool { return false },
		},
		&mockChecker{
			name:     "compatible",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:            a,
				Source:         "compatible",
				CurrentVersion: "1.0",
				LatestVersion:  "2.0",
				HasUpdate:      true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}
	if result.Source != "compatible" {
		t.Errorf("Source = %q, want %q", result.Source, "compatible")
	}
}

func TestCheckWithFallthrough_MixedStaleAndError(t *testing.T) {
	a := &app.App{Name: "MixedApp", Version: "1.0"}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "stale-checker",
			canCheck: func(_ *app.App) bool { return true },
			result: &checker.UpdateResult{
				App:         a,
				Source:      "stale-checker",
				StaleSource: true,
			},
		},
		&mockChecker{
			name:     "error-checker",
			canCheck: func(_ *app.App) bool { return true },
			err:      fmt.Errorf("api error"),
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected error, got nil")
	}
	// Last error should win over stale.
	if result.Source != "error-checker" {
		t.Errorf("Source = %q, want %q", result.Source, "error-checker")
	}
}

// --- MacOSSystemApp ---

func TestMacOSSystemApp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}

	macOS := MacOSSystemApp()
	if macOS == nil {
		t.Fatal("MacOSSystemApp() returned nil on macOS")
	}
	if macOS.BundleID != "com.apple.macOS" {
		t.Errorf("BundleID = %q, want %q", macOS.BundleID, "com.apple.macOS")
	}
	if macOS.Version == "" {
		t.Error("Version is empty")
	}
	if macOS.Source != app.SourceSystem {
		t.Errorf("Source = %q, want %q", macOS.Source, app.SourceSystem)
	}
	if macOS.Name != "macOS" {
		t.Errorf("Name = %q, want %q", macOS.Name, "macOS")
	}
}

// --- DiscoverApps ---

func TestDiscoverApps(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}

	apps, err := DiscoverApps()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// /Applications should have at least a few apps on any macOS machine.
	if len(apps) == 0 {
		t.Fatal("expected at least one app, got 0")
	}

	// Verify macOS system entry is included.
	var foundMacOS bool
	for _, a := range apps {
		if a.BundleID == "com.apple.macOS" {
			foundMacOS = true
			if a.Source != app.SourceSystem {
				t.Errorf("macOS entry Source = %q, want %q", a.Source, app.SourceSystem)
			}
			break
		}
	}
	if !foundMacOS {
		t.Error("macOS system entry not found in discovered apps")
	}
}
