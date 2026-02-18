package updater

import (
	"context"
	"fmt"
	"runtime"
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

func (m *mockChecker) Name() string                { return m.name }
func (m *mockChecker) CanCheck(a *app.App) bool     { return m.canCheck(a) }
func (m *mockChecker) Check(_ context.Context, a *app.App) (*checker.UpdateResult, error) {
	return m.result, m.err
}

// --- DiscoverBrewFormulae ---

func TestDiscoverBrewFormulae(t *testing.T) {
	tests := []struct {
		name       string
		output     []byte
		err        error
		wantCount  int
		wantNames  []string
		wantErr    bool
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
			"brew list --cask": {Output: []byte("")},
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
			"brew list --cask": {Output: []byte("")},
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
	apps := []*app.App{
		{Name: "TestApp2", BundleID: "com.test.app2", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Output: []byte("test-cask\n")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result[0].CaskName != "test-cask" {
		t.Errorf("CaskName = %q, want %q", result[0].CaskName, "test-cask")
	}
	// Since cask is in installed list, InstalledViaBrew should be true.
	if !result[0].InstalledViaBrew {
		t.Error("InstalledViaBrew = false, want true")
	}
	if result[0].Source != app.SourceBrew {
		t.Errorf("Source = %q, want %q", result[0].Source, app.SourceBrew)
	}
}

func TestEnrichApps_Phase3_HeuristicCask(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{Name: "Firefox", BundleID: "org.mozilla.firefox", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Output: []byte("firefox\ngoogle-chrome\n")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ToCaskName("Firefox") = "firefox", which is in the installed cask list.
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

func TestEnrichApps_Phase3_ElectronFallback(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{Name: "My Electron App", BundleID: "com.electron.app", Source: app.SourceElectron},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Output: []byte("my-electron-app\n")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Electron app with no update URL or GitHub repo should get heuristic cask name.
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

func TestEnrichApps_Phase3_ElectronWithUpdateURL_NoFallback(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{
			Name:              "Electron With URL",
			BundleID:          "com.electron.withurl",
			Source:            app.SourceElectron,
			ElectronUpdateURL: "https://update.example.com",
		},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Output: []byte("electron-with-url\n")},
		},
	}

	result, err := EnrichApps(context.Background(), apps, cfg, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Electron app with update URL should NOT get cask fallback.
	if result[0].CaskName != "" {
		t.Errorf("CaskName = %q, want empty (electron with URL should not get fallback)", result[0].CaskName)
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
			"brew list --cask":                        {Output: []byte("")},
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
			"brew list --cask": {Output: []byte("")}, // not installed
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
			"brew list --cask": {Output: []byte("")},
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

func TestEnrichApps_BrewListError(t *testing.T) {
	cfg := &config.Config{}
	apps := []*app.App{
		{Name: "TestApp", BundleID: "com.test.app", Source: app.SourceUnknown},
	}
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Err: fmt.Errorf("brew not installed")},
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
			"brew list --cask":                             {Output: []byte("")},
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
			"brew list --cask": {Output: []byte("")}, // not installed via brew
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
	brewInfoChecker := BuildCheckers(&checker.MockCmdRunner{}, "")[8] // last checker
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
			"brew list --cask": {Output: []byte("")},
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
	brewInfoChecker := BuildCheckers(&checker.MockCmdRunner{}, "")[8]
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
			"brew list --cask": {Output: []byte("")},
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
	brewInfoChecker := BuildCheckers(&checker.MockCmdRunner{}, "")[8]
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

	if len(checkers) != 9 {
		t.Fatalf("got %d checkers, want 9", len(checkers))
	}

	expectedNames := []string{
		"sparkle",
		"brew",
		"mas",
		"github",
		"system",
		"formula",
		"electron",
		"managed",
		"brew-info",
	}

	for i, want := range expectedNames {
		if checkers[i].Name() != want {
			t.Errorf("checkers[%d].Name() = %q, want %q", i, checkers[i].Name(), want)
		}
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
				Source:         "mock",
				LatestVersion:  "9.0",
				HasUpdate:      true,
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
