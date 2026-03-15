package updater

import (
	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/config"
)

func applyExplicitSourceOverrides(apps []*app.App, cfg *config.Config) {
	for _, a := range apps {
		if a == nil {
			continue
		}

		a.ResetEnrichmentState()

		if cfg == nil {
			continue
		}

		applyExplicitSourceOverride(a, cfg.SourceOverride(a.BundleID))
	}
}

func applyExplicitSourceOverride(a *app.App, override *config.SourceOverrideConfig) {
	if override == nil || !eligibleForExplicitSourceOverride(a) {
		return
	}

	a.ResolvedSourceOverride = &app.SourceOverride{
		Kind:       string(override.Kind),
		Repo:       override.Repo,
		AppcastURL: override.AppcastURL,
		Cask:       override.Cask,
	}
	a.SourceOverrideActive = true
	a.SourceOverrideKind = string(override.Kind)

	switch override.Kind {
	case config.SourceOverrideKindGitHub:
		a.GitHubRepo = override.Repo
		a.Source = app.SourceGitHub
	case config.SourceOverrideKindSparkle:
		a.FeedURL = override.AppcastURL
		a.Source = app.SourceSparkle
	case config.SourceOverrideKindBrew:
		a.CaskName = override.Cask
		updateExplicitBrewOverrideSource(a)
	}
}

func eligibleForExplicitSourceOverride(a *app.App) bool {
	if a == nil {
		return false
	}

	return a.Source != app.SourceBrewFormula && a.Source != app.SourceSystem
}

func hasExplicitSourceOverride(a *app.App) bool {
	return a != nil && a.SourceOverrideActive
}

func hasExplicitBrewOverride(a *app.App) bool {
	return hasExplicitSourceOverride(a) && a.SourceOverrideKind == string(config.SourceOverrideKindBrew)
}

func updateExplicitBrewOverrideSource(a *app.App) {
	if !hasExplicitBrewOverride(a) {
		return
	}

	if a.InstalledViaBrew {
		a.Source = app.SourceBrew
		return
	}

	a.Source = app.SourceBrewInfo
}
