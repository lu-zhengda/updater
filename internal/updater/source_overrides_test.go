package updater

import (
	"context"
	"fmt"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

func TestEnrichApps_ExplicitGitHubOverride_PinsSourceAndProvenance(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.github": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "owner/repo",
			},
		},
	}
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.github",
			Source:   app.SourceUnknown,
			Version:  "1.2.3",
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceGitHub {
		t.Fatalf("Source = %q, want %q", got[0].Source, app.SourceGitHub)
	}
	if got[0].GitHubRepo != "owner/repo" {
		t.Fatalf("GitHubRepo = %q, want %q", got[0].GitHubRepo, "owner/repo")
	}
	if got[0].Version != "1.2.3" {
		t.Fatalf("Version = %q, want preserved metadata", got[0].Version)
	}
	if got[0].ResolvedSourceOverride == nil {
		t.Fatalf("ResolvedSourceOverride = nil, want payload")
	}
	if !got[0].SourceOverrideActive {
		t.Fatal("SourceOverrideActive = false, want true")
	}
	if got[0].SourceOverrideKind != string(config.SourceOverrideKindGitHub) {
		t.Fatalf("SourceOverrideKind = %q, want %q", got[0].SourceOverrideKind, config.SourceOverrideKindGitHub)
	}
}

func TestEnrichApps_ExplicitBrewOverride_UsesBrewInfoWhenNotInstalledViaBrew(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.brew": {
				Kind: config.SourceOverrideKindBrew,
				Cask: "visual-studio-code",
			},
		},
	}
	apps := []*app.App{
		{
			Name:     "Code",
			BundleID: "com.example.brew",
			Source:   app.SourceUnknown,
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithNoInstalledCasks())
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].CaskName != "visual-studio-code" {
		t.Fatalf("CaskName = %q, want %q", got[0].CaskName, "visual-studio-code")
	}
	if got[0].InstalledViaBrew {
		t.Fatal("InstalledViaBrew = true, want false")
	}
	if got[0].Source != app.SourceBrewInfo {
		t.Fatalf("Source = %q, want %q", got[0].Source, app.SourceBrewInfo)
	}
	if got[0].ResolvedSourceOverride == nil {
		t.Fatalf("ResolvedSourceOverride = nil, want payload")
	}
	if got[0].SourceOverrideKind != string(config.SourceOverrideKindBrew) {
		t.Fatalf("SourceOverrideKind = %q, want %q", got[0].SourceOverrideKind, config.SourceOverrideKindBrew)
	}
}

func TestEnrichApps_ExplicitBrewOverride_PinsBrewSourceWhenInstalledViaBrew(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.brew": {
				Kind: config.SourceOverrideKindBrew,
				Cask: "visual-studio-code",
			},
		},
	}
	apps := []*app.App{
		{
			Name:             "Code",
			BundleID:         "com.example.brew",
			Source:           app.SourceUnknown,
			InstalledViaBrew: true,
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithInstalledCasks("visual-studio-code"))
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceBrew {
		t.Fatalf("Source = %q, want %q", got[0].Source, app.SourceBrew)
	}
	if got[0].CaskName != "visual-studio-code" {
		t.Fatalf("CaskName = %q, want %q", got[0].CaskName, "visual-studio-code")
	}
}

func TestEnrichApps_ExplicitSparkleOverride_PinsFeedAndSource(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.sparkle": {
				Kind:       config.SourceOverrideKindSparkle,
				AppcastURL: "https://example.com/appcast.xml",
			},
		},
	}
	apps := []*app.App{
		{
			Name:     "Sparkle App",
			BundleID: "com.example.sparkle",
			Source:   app.SourceUnknown,
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceSparkle {
		t.Fatalf("Source = %q, want %q", got[0].Source, app.SourceSparkle)
	}
	if got[0].FeedURL != "https://example.com/appcast.xml" {
		t.Fatalf("FeedURL = %q, want %q", got[0].FeedURL, "https://example.com/appcast.xml")
	}
	if got[0].ResolvedSourceOverride == nil {
		t.Fatalf("ResolvedSourceOverride = nil, want payload")
	}
	if got[0].SourceOverrideKind != string(config.SourceOverrideKindSparkle) {
		t.Fatalf("SourceOverrideKind = %q, want %q", got[0].SourceOverrideKind, config.SourceOverrideKindSparkle)
	}
}

func TestEnrichApps_ReEnrichingSameApps_ResetsExplicitOverrideState(t *testing.T) {
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.app",
			Source:   app.SourceUnknown,
		},
	}

	firstCfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "explicit/repo",
			},
		},
	}

	got, err := EnrichApps(context.Background(), apps, firstCfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("first EnrichApps failed: %v", err)
	}
	if !got[0].SourceOverrideActive {
		t.Fatal("SourceOverrideActive = false after first enrich, want true")
	}
	if got[0].GitHubRepo != "explicit/repo" {
		t.Fatalf("GitHubRepo = %q after first enrich, want %q", got[0].GitHubRepo, "explicit/repo")
	}

	secondCfg := &config.Config{
		GitHubMappings: map[string]string{
			"com.example.app": "legacy/repo",
		},
	}

	got, err = EnrichApps(context.Background(), apps, secondCfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("second EnrichApps failed: %v", err)
	}

	if got[0].SourceOverrideActive {
		t.Fatalf("SourceOverrideActive = true after re-enrich, want false: %#v", got[0])
	}
	if got[0].SourceOverrideKind != "" {
		t.Fatalf("SourceOverrideKind = %q after re-enrich, want empty", got[0].SourceOverrideKind)
	}
	if got[0].ResolvedSourceOverride != nil {
		t.Fatalf("ResolvedSourceOverride = %#v after re-enrich, want nil", got[0].ResolvedSourceOverride)
	}
	if got[0].GitHubRepo != "legacy/repo" {
		t.Fatalf("GitHubRepo = %q after re-enrich, want %q", got[0].GitHubRepo, "legacy/repo")
	}
}

func TestEnrichApps_ReEnrichingSameApps_RemovesOldExplicitGitHubFields(t *testing.T) {
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.app",
			Source:   app.SourceUnknown,
		},
	}

	firstCfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "explicit/repo",
			},
		},
	}

	got, err := EnrichApps(context.Background(), apps, firstCfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("first EnrichApps failed: %v", err)
	}
	if got[0].Source != app.SourceGitHub || got[0].GitHubRepo != "explicit/repo" {
		t.Fatalf("first enrich = %#v, want github override applied", got[0])
	}

	got, err = EnrichApps(context.Background(), apps, &config.Config{}, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("second EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceUnknown {
		t.Fatalf("Source = %q after override removal, want %q", got[0].Source, app.SourceUnknown)
	}
	if got[0].GitHubRepo != "" {
		t.Fatalf("GitHubRepo = %q after override removal, want empty", got[0].GitHubRepo)
	}
	if got[0].SourceOverrideActive {
		t.Fatalf("SourceOverrideActive = true after override removal: %#v", got[0])
	}
	if got[0].ResolvedSourceOverride != nil {
		t.Fatalf("ResolvedSourceOverride = %#v after override removal, want nil", got[0].ResolvedSourceOverride)
	}
}

func TestEnrichApps_ReEnrichingSameApps_SwitchesOverrideKindsWithoutLeakingFields(t *testing.T) {
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.app",
			Source:   app.SourceUnknown,
		},
	}

	firstCfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "explicit/repo",
			},
		},
	}

	got, err := EnrichApps(context.Background(), apps, firstCfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("first EnrichApps failed: %v", err)
	}
	if got[0].Source != app.SourceGitHub || got[0].GitHubRepo != "explicit/repo" {
		t.Fatalf("first enrich = %#v, want github override applied", got[0])
	}

	secondCfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind:       config.SourceOverrideKindSparkle,
				AppcastURL: "https://example.com/appcast.xml",
			},
		},
	}

	got, err = EnrichApps(context.Background(), apps, secondCfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("second EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceSparkle {
		t.Fatalf("Source = %q after override switch, want %q", got[0].Source, app.SourceSparkle)
	}
	if got[0].FeedURL != "https://example.com/appcast.xml" {
		t.Fatalf("FeedURL = %q after override switch, want %q", got[0].FeedURL, "https://example.com/appcast.xml")
	}
	if got[0].GitHubRepo != "" {
		t.Fatalf("GitHubRepo = %q after override switch, want empty", got[0].GitHubRepo)
	}
	if got[0].SourceOverrideKind != string(config.SourceOverrideKindSparkle) {
		t.Fatalf("SourceOverrideKind = %q after override switch, want %q", got[0].SourceOverrideKind, config.SourceOverrideKindSparkle)
	}
}

func TestEnrichApps_LegacyMappings_DoNotSetOverrideProvenance(t *testing.T) {
	cfg := &config.Config{
		GitHubMappings: map[string]string{
			"com.example.app": "owner/repo",
		},
		CaskMappings: map[string]string{
			"com.other.app": "other-cask",
		},
	}
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.app",
			Source:   app.SourceUnknown,
		},
		{
			Name:     "Other",
			BundleID: "com.other.app",
			Source:   app.SourceUnknown,
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithInstalledCasks("other-cask"))
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	for _, a := range got {
		if a.SourceOverrideActive {
			t.Fatalf("legacy mappings must not set override provenance: %#v", a)
		}
		if a.ResolvedSourceOverride != nil {
			t.Fatalf("legacy mappings must not set override payload: %#v", a)
		}
		if a.SourceOverrideKind != "" {
			t.Fatalf("legacy mappings must not set override kind: %#v", a)
		}
	}
}

func TestEnrichApps_ExplicitOverrideWinsOverLegacyMappings(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"com.example.app": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "explicit/repo",
			},
		},
		GitHubMappings: map[string]string{
			"com.example.app": "legacy/repo",
		},
		CaskMappings: map[string]string{
			"com.example.app": "legacy-cask",
		},
	}
	apps := []*app.App{
		{
			Name:     "Example",
			BundleID: "com.example.app",
			Source:   app.SourceUnknown,
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithInstalledCasks("legacy-cask"))
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceGitHub {
		t.Fatalf("Source = %q, want %q", got[0].Source, app.SourceGitHub)
	}
	if got[0].GitHubRepo != "explicit/repo" {
		t.Fatalf("GitHubRepo = %q, want %q", got[0].GitHubRepo, "explicit/repo")
	}
	if got[0].CaskName != "" {
		t.Fatalf("CaskName = %q, want empty because explicit github override wins", got[0].CaskName)
	}
	if got[0].SourceOverrideKind != string(config.SourceOverrideKindGitHub) {
		t.Fatalf("SourceOverrideKind = %q, want %q", got[0].SourceOverrideKind, config.SourceOverrideKindGitHub)
	}
}

func TestEnrichApps_ExplicitSourceOverrides_DoNotMutateSyntheticEntries(t *testing.T) {
	cfg := &config.Config{
		SourceOverrides: map[string]*config.SourceOverrideConfig{
			"homebrew.formula.node": {
				Kind: config.SourceOverrideKindGitHub,
				Repo: "owner/node",
			},
			"com.apple.macOS": {
				Kind:       config.SourceOverrideKindSparkle,
				AppcastURL: "https://example.com/macos.xml",
			},
		},
	}
	apps := []*app.App{
		{
			Name:             "node",
			BundleID:         "homebrew.formula.node",
			Source:           app.SourceBrewFormula,
			FormulaName:      "node",
			InstalledViaBrew: true,
		},
		{
			Name:     "macOS",
			BundleID: "com.apple.macOS",
			Source:   app.SourceSystem,
			Version:  "14.0",
		},
	}

	got, err := EnrichApps(context.Background(), apps, cfg, mockRunnerWithNoBrew())
	if err != nil {
		t.Fatalf("EnrichApps failed: %v", err)
	}

	if got[0].Source != app.SourceBrewFormula || got[0].FormulaName != "node" {
		t.Fatalf("formula entry mutated unexpectedly: %#v", got[0])
	}
	if got[0].SourceOverrideActive || got[0].ResolvedSourceOverride != nil || got[0].SourceOverrideKind != "" {
		t.Fatalf("formula entry must not become override-backed: %#v", got[0])
	}
	if got[1].Source != app.SourceSystem || got[1].FeedURL != "" {
		t.Fatalf("system entry mutated unexpectedly: %#v", got[1])
	}
	if got[1].SourceOverrideActive || got[1].ResolvedSourceOverride != nil || got[1].SourceOverrideKind != "" {
		t.Fatalf("system entry must not become override-backed: %#v", got[1])
	}
}

func TestCheckWithFallthrough_ExplicitGitHubOverride_DoesNotFallThroughOnError(t *testing.T) {
	a := &app.App{
		Name:                 "Example",
		Version:              "1.0.0",
		Source:               app.SourceGitHub,
		GitHubRepo:           "owner/repo",
		SourceOverrideActive: true,
		SourceOverrideKind:   string(config.SourceOverrideKindGitHub),
		ResolvedSourceOverride: &app.SourceOverride{
			Kind: string(config.SourceOverrideKindGitHub),
			Repo: "owner/repo",
		},
	}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "github",
			canCheck: func(*app.App) bool { return true },
			err:      fmt.Errorf("boom"),
		},
		&mockChecker{
			name:     "brew-info",
			canCheck: func(*app.App) bool { return true },
			result: &checker.UpdateResult{
				Source:        "brew-info",
				LatestVersion: "2.0.0",
				HasUpdate:     true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected github error without fallthrough")
	}
	if result.Source != "github" {
		t.Fatalf("Source = %q, want %q", result.Source, "github")
	}
	if !result.SourceOverrideActive || result.SourceOverrideKind != string(config.SourceOverrideKindGitHub) {
		t.Fatalf("expected override provenance on result, got %#v", result)
	}
}

func TestCheckWithFallthrough_ExplicitSparkleOverride_DoesNotFallThroughOnStale(t *testing.T) {
	a := &app.App{
		Name:                 "Example",
		Version:              "2.0.0",
		Source:               app.SourceSparkle,
		FeedURL:              "https://example.com/appcast.xml",
		CaskName:             "example",
		SourceOverrideActive: true,
		SourceOverrideKind:   string(config.SourceOverrideKindSparkle),
		ResolvedSourceOverride: &app.SourceOverride{
			Kind:       string(config.SourceOverrideKindSparkle),
			AppcastURL: "https://example.com/appcast.xml",
		},
	}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "sparkle",
			canCheck: func(*app.App) bool { return true },
			result: &checker.UpdateResult{
				Source:      "sparkle",
				StaleSource: true,
			},
		},
		&mockChecker{
			name:     "brew-info",
			canCheck: func(*app.App) bool { return true },
			result: &checker.UpdateResult{
				Source:        "brew-info",
				LatestVersion: "3.0.0",
				HasUpdate:     true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result.Error == nil {
		t.Fatal("expected sparkle stale result without brew-info fallthrough")
	}
	if result.Source != "sparkle" {
		t.Fatalf("Source = %q, want %q", result.Source, "sparkle")
	}
	if !result.SourceOverrideActive || result.SourceOverrideKind != string(config.SourceOverrideKindSparkle) {
		t.Fatalf("expected override provenance on result, got %#v", result)
	}
}

func TestCheckWithFallthrough_CopiesOverrideProvenanceToResult(t *testing.T) {
	a := &app.App{
		Name:                 "Example",
		Version:              "1.0.0",
		Source:               app.SourceBrew,
		CaskName:             "example",
		InstalledViaBrew:     true,
		SourceOverrideActive: true,
		SourceOverrideKind:   string(config.SourceOverrideKindBrew),
	}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "brew",
			canCheck: func(*app.App) bool { return true },
			result: &checker.UpdateResult{
				Source:         "brew",
				CurrentVersion: "1.0.0",
				LatestVersion:  "1.1.0",
				HasUpdate:      true,
			},
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if !result.SourceOverrideActive || result.SourceOverrideKind != string(config.SourceOverrideKindBrew) {
		t.Fatalf("expected override provenance on result, got %#v", result)
	}
}

func TestCheckWithFallthrough_DoesNotMutateSharedCheckerResult(t *testing.T) {
	shared := &checker.UpdateResult{
		Source:         "github",
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.1.0",
		HasUpdate:      true,
	}
	a := &app.App{
		Name:                 "Example",
		Version:              "1.0.0",
		Source:               app.SourceGitHub,
		GitHubRepo:           "owner/repo",
		SourceOverrideActive: true,
		SourceOverrideKind:   string(config.SourceOverrideKindGitHub),
	}

	checkers := []checker.Checker{
		&mockChecker{
			name:     "github",
			canCheck: func(*app.App) bool { return true },
			result:   shared,
		},
	}

	result := CheckWithFallthrough(context.Background(), a, checkers)
	if result == shared {
		t.Fatal("expected result to be copied before provenance is stamped")
	}
	if result.App != a {
		t.Fatalf("result.App = %#v, want checked app", result.App)
	}
	if !result.SourceOverrideActive || result.SourceOverrideKind != string(config.SourceOverrideKindGitHub) {
		t.Fatalf("expected override provenance on copied result, got %#v", result)
	}
	if shared.App != nil || shared.SourceOverrideActive || shared.SourceOverrideKind != "" {
		t.Fatalf("shared checker result was mutated: %#v", shared)
	}
}

func TestCheckAll_ExplicitOverrideWithoutCompatibleChecker_ReturnsErrorResult(t *testing.T) {
	a := &app.App{
		Name:                 "Example",
		Version:              "1.0.0",
		Source:               app.SourceGitHub,
		SourceOverrideActive: true,
		SourceOverrideKind:   string(config.SourceOverrideKindGitHub),
	}

	results := CheckAll(context.Background(), []*app.App{a}, []checker.Checker{
		&mockChecker{
			name:     "sparkle",
			canCheck: func(*app.App) bool { return true },
		},
	}, 1)

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Error == nil {
		t.Fatalf("expected deterministic override-backed error result, got %#v", results[0])
	}
	if results[0].Source != "github" {
		t.Fatalf("Source = %q, want %q", results[0].Source, "github")
	}
	if !results[0].SourceOverrideActive || results[0].SourceOverrideKind != string(config.SourceOverrideKindGitHub) {
		t.Fatalf("expected override provenance on result, got %#v", results[0])
	}
}

func mockRunnerWithNoBrew() checker.CmdRunner {
	return mockRunnerWithInstalledCasks()
}

func mockRunnerWithNoInstalledCasks() checker.CmdRunner {
	return mockRunnerWithInstalledCasks()
}

func mockRunnerWithInstalledCasks(casks ...string) checker.CmdRunner {
	output := []byte("")
	if len(casks) > 0 {
		line := ""
		for i, cask := range casks {
			if i > 0 {
				line += "\n"
			}
			line += cask
		}
		output = []byte(line + "\n")
	}

	return &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			"brew list --cask": {Output: output},
		},
	}
}
