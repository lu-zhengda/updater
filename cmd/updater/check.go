package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check installed apps for available updates",
	RunE:  runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	runner := &checker.RealCmdRunner{}

	formulaApps, err := discoverBrewFormulae(ctx, runner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not discover brew formulae: %v\n", err)
	} else {
		apps = append(apps, formulaApps...)
	}

	apps, err = enrichApps(ctx, apps, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
	results := checkAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	printCheckResults(cmd, results, cfg)
	return nil
}

// discoverApps scans /Applications and ~/Applications for installed apps.
// It also adds a synthetic macOS system entry for system update checks.
func discoverApps() ([]*app.App, error) {
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
	macOSApp := macOSSystemApp()
	if macOSApp != nil {
		apps = append(apps, macOSApp)
	}

	return apps, nil
}

// macOSSystemApp creates a synthetic App entry for macOS system updates.
func macOSSystemApp() *app.App {
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

// discoverBrewFormulae creates synthetic App entries for each installed Homebrew formula.
func discoverBrewFormulae(ctx context.Context, runner checker.CmdRunner) ([]*app.App, error) {
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

// enrichApps applies config mappings and cross-references with brew casks
// to enrich app metadata. It sets CaskName and InstalledViaBrew, and probes
// brew info for unknown apps to discover available casks.
func enrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) ([]*app.App, error) {
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

	for _, a := range apps {
		// If no cask name from config, try heuristic only for unknown-source apps.
		if a.CaskName == "" && a.Source == app.SourceUnknown {
			a.CaskName = app.ToCaskName(a.Name)
		}

		if a.CaskName != "" && casks[a.CaskName] {
			a.InstalledViaBrew = true
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceBrew
			}
		}
	}

	// Phase 4: For apps still unknown, probe brew info to discover available casks.
	// Skip apps whose cask name came from config (user's explicit mapping).
	// Probes run concurrently to avoid sequential ~0.8s per app delays.
	type probeResult struct {
		app   *app.App
		found bool
	}
	var toProbe []*app.App
	for _, a := range apps {
		if a.Source != app.SourceUnknown || a.CaskName == "" {
			continue
		}
		if cfg.CaskToken(a.BundleID) != "" {
			continue // preserve user-configured cask mapping
		}
		toProbe = append(toProbe, a)
	}

	if len(toProbe) > 0 {
		results := make(chan probeResult, len(toProbe))
		sem := make(chan struct{}, 8) // limit concurrent brew info calls
		for _, a := range toProbe {
			sem <- struct{}{}
			go func(a *app.App) {
				defer func() { <-sem }()
				results <- probeResult{app: a, found: checker.CaskExists(ctx, runner, a.CaskName)}
			}(a)
		}
		for range toProbe {
			r := <-results
			if !r.found {
				r.app.CaskName = ""
			}
		}
	}

	return apps, nil
}

// filterIgnored removes apps that are in the config's ignore list.
func filterIgnored(apps []*app.App, cfg *config.Config) []*app.App {
	var filtered []*app.App
	for _, a := range apps {
		if !cfg.IsIgnored(a.BundleID) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// buildCheckers creates all checkers with their dependencies.
// Order matters for fallthrough: most accurate first, broadest fallback last.
func buildCheckers(runner checker.CmdRunner, githubToken string) []checker.Checker {
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

// checkAll runs update checks on all apps concurrently.
// It uses a semaphore to limit concurrency to maxConcurrency goroutines.
// If a checker returns a stale result or an error, the next compatible checker is tried.
func checkAll(ctx context.Context, apps []*app.App, checkers []checker.Checker, maxConcurrency int) []*checker.UpdateResult {
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

			result := checkWithFallthrough(ctx, a, checkers)

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(a)
	}

	wg.Wait()
	return results
}

// checkWithFallthrough tries checkers in order, falling through on stale results or errors.
func checkWithFallthrough(ctx context.Context, a *app.App, checkers []checker.Checker) *checker.UpdateResult {
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

// printCheckResults prints a table of check results to stdout.
func printCheckResults(cmd *cobra.Command, results []*checker.UpdateResult, cfg *config.Config) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCURRENT\tLATEST\tSOURCE\tSTATUS")

	updateCount := 0
	errCount := 0
	pinnedCount := 0
	for _, r := range results {
		src := cliSourceName(r.Source)
		if r.Error != nil {
			errCount++
			fmt.Fprintf(w, "%s\t%s\t-\t%s\tERROR: %v\n", r.App.Name, r.CurrentVersion, src, r.Error)
			continue
		}
		if r.HasUpdate && cfg.IsPinned(r.App.BundleID) {
			pinnedCount++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tPINNED\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		} else if r.HasUpdate {
			updateCount++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tUPDATE AVAILABLE\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\tok\n", r.App.Name, r.CurrentVersion, r.LatestVersion, src)
		}
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps checked, %d updates available", len(results), updateCount)
	if pinnedCount > 0 {
		fmt.Fprintf(os.Stderr, ", %d pinned", pinnedCount)
	}
	if errCount > 0 {
		fmt.Fprintf(os.Stderr, ", %d errors", errCount)
	}
	fmt.Fprintln(os.Stderr)
}

// cliSourceName returns a user-friendly display name for a source.
func cliSourceName(source string) string {
	switch source {
	case "mas":
		return "app store"
	case "brew", "brew-info":
		return "homebrew"
	case "formula":
		return "formula"
	case "electron", "setapp", "toolbox", "adobe":
		return source
	default:
		return source
	}
}
