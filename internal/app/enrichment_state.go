package app

type enrichmentBaseline struct {
	Source           Source
	FeedURL          string
	CaskName         string
	GitHubRepo       string
	InstalledViaBrew bool
}

// DiscoveredSource returns the source before configuration and cask enrichment.
func (a *App) DiscoveredSource() Source {
	if a.enrichmentBaseline != nil {
		return a.enrichmentBaseline.Source
	}
	return a.Source
}

// ResetEnrichmentState restores fields that EnrichApps mutates back to their
// pre-enrichment values, then clears explicit override provenance.
func (a *App) ResetEnrichmentState() {
	if a == nil {
		return
	}

	if a.enrichmentBaseline == nil {
		a.enrichmentBaseline = &enrichmentBaseline{
			Source:           a.Source,
			FeedURL:          a.FeedURL,
			CaskName:         a.CaskName,
			GitHubRepo:       a.GitHubRepo,
			InstalledViaBrew: a.InstalledViaBrew,
		}
	}

	a.Source = a.enrichmentBaseline.Source
	a.FeedURL = a.enrichmentBaseline.FeedURL
	a.CaskName = a.enrichmentBaseline.CaskName
	a.PendingCaskTokens = nil
	a.GitHubRepo = a.enrichmentBaseline.GitHubRepo
	a.InstalledViaBrew = a.enrichmentBaseline.InstalledViaBrew
	a.ResolvedSourceOverride = nil
	a.SourceOverrideActive = false
	a.SourceOverrideKind = ""
}
