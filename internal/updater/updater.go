package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

// DiscoverApps scans /Applications and ~/Applications for installed apps.
// It also adds a synthetic macOS system entry for system update checks.
func DiscoverApps() ([]*app.App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dirs := []string{
		"/Applications",
		filepath.Join(home, "Applications"),
		"/Applications/Setapp",
		filepath.Join(home, "Applications", "Setapp"),
	}

	apps, err := app.Discover(dirs...)
	if err != nil {
		return nil, fmt.Errorf("failed to discover apps: %w", err)
	}

	// Add synthetic macOS system entry.
	macOSApp := MacOSSystemApp()
	if macOSApp != nil {
		apps = append(apps, macOSApp)
	}

	return apps, nil
}

// MacOSSystemApp creates a synthetic App entry for macOS system updates.
func MacOSSystemApp() *app.App {
	runner := &checker.RealCmdRunner{}
	output, err := runner.Run(context.Background(), "sw_vers", "-productVersion")
	if err != nil {
		return nil
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return nil
	}
	return &app.App{
		Name:     "macOS",
		BundleID: "com.apple.macOS",
		Version:  version,
		Source:   app.SourceSystem,
	}
}

// DiscoverBrewFormulae creates synthetic App entries for each installed Homebrew formula.
func DiscoverBrewFormulae(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
	formulae, err := checker.ListInstalledFormulae(ctx, runner)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(formulae))
	for name := range formulae {
		names = append(names, name)
	}
	sort.Strings(names)

	apps := make([]*app.App, 0, len(formulae))
	for _, name := range names {
		apps = append(apps, &app.App{
			Name:             name,
			BundleID:         "homebrew.formula." + name,
			Version:          formulae[name],
			Source:           app.SourceBrewFormula,
			FormulaName:      name,
			InstalledViaBrew: true,
		})
	}
	return apps, nil
}

// DiscoverUvTools creates synthetic App entries for each tool installed via `uv tool install`.
func DiscoverUvTools(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
	tools, err := checker.ListInstalledUvTools(ctx, runner)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	apps := make([]*app.App, 0, len(tools))
	for _, name := range names {
		apps = append(apps, &app.App{
			Name:     name,
			BundleID: "uv.tool." + name,
			Version:  tools[name],
			Source:   app.SourceUv,
			UvTool:   name,
		})
	}
	return apps, nil
}

// DiscoverNpmPackages creates synthetic App entries for each globally installed npm package.
func DiscoverNpmPackages(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
	packages, err := checker.ListInstalledNpmPackages(ctx, runner)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)

	apps := make([]*app.App, 0, len(packages))
	for _, name := range names {
		apps = append(apps, &app.App{
			Name:       name,
			BundleID:   "npm.global." + name,
			Version:    packages[name],
			Source:     app.SourceNpm,
			NpmPackage: name,
		})
	}
	return apps, nil
}

// DiscoverCargoCrates creates synthetic App entries for each crate installed via `cargo install`.
func DiscoverCargoCrates(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
	crates, err := checker.ListInstalledCargoCrates(ctx, runner)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(crates))
	for name := range crates {
		names = append(names, name)
	}
	sort.Strings(names)

	apps := make([]*app.App, 0, len(crates))
	for _, name := range names {
		apps = append(apps, &app.App{
			Name:       name,
			BundleID:   "cargo.crate." + name,
			Version:    crates[name],
			Source:     app.SourceCargo,
			CargoCrate: name,
		})
	}
	return apps, nil
}

// EnrichApps applies explicit source overrides, config mappings, and
// cross-references with brew casks to enrich app metadata. It sets CaskName and
// InstalledViaBrew, and probes brew info for eligible apps to discover
// available casks.
func EnrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) ([]*app.App, error) {
	// Phase 0: Apply explicit source overrides before legacy mappings or brew heuristics.
	applyExplicitSourceOverrides(apps, cfg)

	// Phase 1: Apply GitHub repo mappings from config.
	for _, a := range apps {
		if hasExplicitSourceOverride(a) {
			continue
		}
		if repo := cfg.GitHubRepo(a.BundleID); repo != "" {
			a.GitHubRepo = repo
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceGitHub
			}
		}
	}

	// Phase 2: Apply cask mappings from config (bundleID → cask token).
	for _, a := range apps {
		if hasExplicitSourceOverride(a) {
			continue
		}
		if token := cfg.CaskToken(a.BundleID); token != "" {
			a.CaskName = token
		}
	}

	// Phase 3: Cross-reference with installed brew casks.
	casks, err := checker.ListInstalledCasks(ctx, runner)
	if err != nil {
		// Non-fatal: brew may not be installed.
		fmt.Fprintf(os.Stderr, "warning: could not list brew casks: %v\n", err)
		return apps, nil
	}

	// appCandidates holds the ordered cask-token candidate lists for apps that
	// need Phase 4 probing (i.e. not resolved via the installed-cask list).
	appCandidates := map[*app.App][]string{}

	for _, a := range apps {
		if a.CaskName != "" && casks[a.CaskName] {
			a.InstalledViaBrew = true
			if hasExplicitBrewOverride(a) {
				a.Source = app.SourceBrew
			} else if a.Source == app.SourceUnknown {
				a.Source = app.SourceBrew
			}
		}

		if hasExplicitSourceOverride(a) {
			continue
		}

		// Compute cask-token candidates for apps that may need brew-info fallback:
		// - SourceUnknown apps
		// - SourceSparkle apps (Sparkle feeds can become stale)
		// - SourceElectron apps without a native update URL or GitHub repo
		//
		// This keeps behavior explicit and avoids probing managed/system-only sources.
		needsCaskFallback := a.Source == app.SourceUnknown ||
			a.Source == app.SourceSparkle ||
			(a.Source == app.SourceElectron && a.ElectronUpdateURL == "" && a.GitHubRepo == "")
		if a.CaskName == "" && needsCaskFallback {
			candidates := app.CaskCandidates(a)
			// Try each candidate against the installed cask list.  The multi-signal
			// strategy handles apps whose display name diverges from their cask token
			// (e.g. VSCode: "Code" vs "visual-studio-code"; GitHub Desktop:
			// "GitHub Desktop" vs "github") without requiring a hardcoded map.
			for _, cand := range candidates {
				if casks[cand] {
					a.CaskName = cand
					break
				}
			}
			// Queue remaining candidates for Phase 4 probing if still unresolved.
			if a.CaskName == "" && len(candidates) > 0 {
				appCandidates[a] = candidates
			}
		}

		if a.CaskName != "" && casks[a.CaskName] {
			a.InstalledViaBrew = true
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceBrew
			}
		}
	}

	// Phase 4: For apps still without a CaskName, probe brew info for each
	// candidate in priority order, stopping at the first confirmed cask.
	// Apps are probed concurrently; candidates within each app are tried
	// sequentially to preserve priority ordering.
	// Skip apps whose CaskName was set by a config mapping (user's explicit choice).
	type probeItem struct {
		app        *app.App
		candidates []string
	}
	type probeResult struct {
		app      *app.App
		caskName string // empty if no candidate was found
	}

	var toProbe []probeItem
	for a, candidates := range appCandidates {
		if cfg.CaskToken(a.BundleID) != "" {
			continue // preserve user-configured cask mapping, no probe needed
		}
		toProbe = append(toProbe, probeItem{app: a, candidates: candidates})
	}

	if len(toProbe) > 0 {
		results := make(chan probeResult, len(toProbe))
		sem := make(chan struct{}, 8) // limit concurrent brew info calls
		for _, item := range toProbe {
			sem <- struct{}{}
			go func(item probeItem) {
				defer func() { <-sem }()
				found := ""
				for _, cand := range item.candidates {
					if checker.CaskExists(ctx, runner, cand) {
						found = cand
						break
					}
				}
				results <- probeResult{app: item.app, caskName: found}
			}(item)
		}
		for range toProbe {
			r := <-results
			r.app.CaskName = r.caskName
		}
	}

	return apps, nil
}

// FilterIgnored removes apps that are in the config's ignore list.
func FilterIgnored(apps []*app.App, cfg *config.Config) []*app.App {
	var filtered []*app.App
	for _, a := range apps {
		if !cfg.IsIgnored(a.BundleID) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// BuildCheckers creates all checkers with their dependencies.
// Order matters for fallthrough: most accurate first, broadest fallback last.
func BuildCheckers(runner checker.CmdRunner, githubToken string) []checker.Checker {
	return []checker.Checker{
		checker.NewSparkleChecker(nil),
		checker.NewBrewChecker(runner),
		checker.NewMASChecker(runner),
		checker.NewGitHubChecker(nil, "", githubToken),
		checker.NewSystemChecker(runner),
		checker.NewBrewFormulaChecker(runner),
		checker.NewElectronChecker(nil),
		checker.NewNpmChecker(runner),
		checker.NewUvChecker(nil, ""),
		checker.NewCargoChecker(nil, ""),
		checker.NewManagedChecker(),
		checker.NewBrewInfoChecker(runner), // fallback: any app with a CaskName
	}
}

func checkersForApp(a *app.App, all []checker.Checker) []checker.Checker {
	if a == nil || !a.SourceOverrideActive {
		return all
	}

	var filtered []checker.Checker
	for _, c := range all {
		switch a.SourceOverrideKind {
		case string(config.SourceOverrideKindGitHub):
			if c.Name() == string(config.SourceOverrideKindGitHub) {
				filtered = append(filtered, c)
			}
		case string(config.SourceOverrideKindSparkle):
			if c.Name() == string(config.SourceOverrideKindSparkle) {
				filtered = append(filtered, c)
			}
		case string(config.SourceOverrideKindBrew):
			if c.Name() == "brew" || c.Name() == string(app.SourceBrewInfo) {
				filtered = append(filtered, c)
			}
		}
	}

	return filtered
}

func effectiveResultSource(a *app.App, fallback string) string {
	if fallback != "" {
		return fallback
	}
	if a == nil {
		return "unknown"
	}
	if a.Source != "" && a.Source != app.SourceUnknown {
		return string(a.Source)
	}
	if a.SourceOverrideKind != "" {
		return a.SourceOverrideKind
	}
	return "unknown"
}

func withOverrideProvenance(result *checker.UpdateResult, a *app.App) *checker.UpdateResult {
	if result == nil || a == nil {
		return result
	}

	// Checkers may reuse result structs across calls in tests or callers; copy
	// before stamping app-specific provenance to avoid cross-call mutation.
	resultCopy := *result
	if resultCopy.App == nil {
		resultCopy.App = a
	}
	resultCopy.SourceOverrideActive = a.SourceOverrideActive
	resultCopy.SourceOverrideKind = a.SourceOverrideKind
	return &resultCopy
}

func noCheckerResult(a *app.App) *checker.UpdateResult {
	source := "unknown"
	if a != nil && a.SourceOverrideActive {
		source = effectiveResultSource(a, "")
	}

	return withOverrideProvenance(&checker.UpdateResult{
		App:            a,
		Source:         source,
		CurrentVersion: a.Version,
		Error:          fmt.Errorf("no checker could provide a result for %s", a.Name),
	}, a)
}

// CheckAll runs update checks on all apps concurrently.
// It uses a semaphore to limit concurrency to maxConcurrency goroutines.
// If a checker returns a stale result or an error, the next compatible checker is tried.
func CheckAll(ctx context.Context, apps []*app.App, checkers []checker.Checker, maxConcurrency int) []*checker.UpdateResult {
	if maxConcurrency <= 0 {
		maxConcurrency = 10
	}

	var (
		mu      sync.Mutex
		results []*checker.UpdateResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrency)
	)

	for _, a := range apps {
		appCheckers := checkersForApp(a, checkers)

		// Check if any checker can handle this app.
		hasChecker := false
		for _, c := range appCheckers {
			if c.CanCheck(a) {
				hasChecker = true
				break
			}
		}
		if !hasChecker {
			if a != nil && a.SourceOverrideActive {
				mu.Lock()
				results = append(results, noCheckerResult(a))
				mu.Unlock()
			}
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(a *app.App) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			result := CheckWithFallthrough(ctx, a, appCheckers)

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(a)
	}

	wg.Wait()
	return results
}

// CheckWithFallthrough tries checkers in order, falling through on stale results or errors.
func CheckWithFallthrough(ctx context.Context, a *app.App, checkers []checker.Checker) *checker.UpdateResult {
	checkers = checkersForApp(a, checkers)

	var lastErr error
	var lastSource string
	var staleCount int

	for _, c := range checkers {
		if !c.CanCheck(a) {
			continue
		}

		result, err := c.Check(ctx, a)
		if err != nil {
			lastErr = err
			lastSource = c.Name()
			continue // try next checker
		}

		if result.StaleSource {
			lastSource = result.Source
			staleCount++
			continue // stale feed, try next checker
		}

		return withOverrideProvenance(result, a)
	}

	// All checkers failed or were stale — return error result from last attempt.
	if lastErr != nil {
		return withOverrideProvenance(&checker.UpdateResult{
			App:            a,
			Source:         effectiveResultSource(a, lastSource),
			CurrentVersion: a.Version,
			Error:          lastErr,
		}, a)
	}

	if len(checkers) == 0 {
		return noCheckerResult(a)
	}

	msg := fmt.Sprintf("no checker could provide a result for %s", a.Name)
	if staleCount > 0 {
		msg = fmt.Sprintf("all %d source(s) returned stale data for %s", staleCount, a.Name)
	}
	source := "unknown"
	if a != nil && a.SourceOverrideActive {
		source = effectiveResultSource(a, lastSource)
	}
	return withOverrideProvenance(&checker.UpdateResult{
		App:            a,
		Source:         source,
		CurrentVersion: a.Version,
		Error:          fmt.Errorf("%s", msg),
	}, a)
}
