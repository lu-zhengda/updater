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

// EnrichApps applies config mappings and cross-references with brew casks
// to enrich app metadata. It sets CaskName and InstalledViaBrew, and probes
// brew info for unknown apps to discover available casks.
func EnrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) ([]*app.App, error) {
	// Phase 1: Apply GitHub repo mappings from config.
	for _, a := range apps {
		if repo := cfg.GitHubRepo(a.BundleID); repo != "" {
			a.GitHubRepo = repo
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceGitHub
			}
		}
	}

	// Phase 2: Apply cask mappings from config (bundleID → cask token).
	for _, a := range apps {
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
		// Compute candidates for apps that have no checker-matchable metadata:
		// SourceUnknown and SourceElectron without a native update URL or GitHub repo.
		needsCaskFallback := a.Source == app.SourceUnknown ||
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
		checker.NewManagedChecker(),
		checker.NewBrewInfoChecker(runner), // fallback: any app with a CaskName
	}
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
		// Check if any checker can handle this app.
		hasChecker := false
		for _, c := range checkers {
			if c.CanCheck(a) {
				hasChecker = true
				break
			}
		}
		if !hasChecker {
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(a *app.App) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			result := CheckWithFallthrough(ctx, a, checkers)

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
			staleCount++
			continue // stale feed, try next checker
		}

		return result
	}

	// All checkers failed or were stale — return error result from last attempt.
	if lastErr != nil {
		return &checker.UpdateResult{
			App:            a,
			Source:         lastSource,
			CurrentVersion: a.Version,
			Error:          lastErr,
		}
	}

	msg := fmt.Sprintf("no checker could provide a result for %s", a.Name)
	if staleCount > 0 {
		msg = fmt.Sprintf("all %d source(s) returned stale data for %s", staleCount, a.Name)
	}
	return &checker.UpdateResult{
		App:            a,
		Source:         "unknown",
		CurrentVersion: a.Version,
		Error:          fmt.Errorf("%s", msg),
	}
}
