package updater

import (
	"context"
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
