package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	apps, err = enrichApps(ctx, apps, cfg, runner)
	if err != nil {
		return err
	}

	apps = filterIgnored(apps, cfg)

	checkers := buildCheckers(runner)
	results := checkAll(ctx, apps, checkers)

	printCheckResults(cmd, results)
	return nil
}

// discoverApps scans /Applications and ~/Applications for installed apps.
func discoverApps() ([]*app.App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	dirs := []string{
		"/Applications",
		filepath.Join(home, "Applications"),
	}

	apps, err := app.Discover(dirs...)
	if err != nil {
		return nil, fmt.Errorf("failed to discover apps: %w", err)
	}

	return apps, nil
}

// enrichApps applies GitHub mappings from config and cross-references with
// installed brew casks to enrich app metadata.
func enrichApps(ctx context.Context, apps []*app.App, cfg *config.Config, runner checker.CmdRunner) ([]*app.App, error) {
	// Apply GitHub repo mappings from config.
	for _, a := range apps {
		if repo := cfg.GitHubRepo(a.BundleID); repo != "" {
			a.GitHubRepo = repo
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceGitHub
			}
		}
	}

	// Cross-reference with installed brew casks.
	casks, err := checker.ListInstalledCasks(ctx, runner)
	if err != nil {
		// Non-fatal: brew may not be installed.
		fmt.Fprintf(os.Stderr, "warning: could not list brew casks: %v\n", err)
		return apps, nil
	}

	for _, a := range apps {
		caskName := app.ToCaskName(a.Name)
		if casks[caskName] {
			a.CaskName = caskName
			if a.Source == app.SourceUnknown {
				a.Source = app.SourceBrew
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
func buildCheckers(runner checker.CmdRunner) []checker.Checker {
	return []checker.Checker{
		checker.NewSparkleChecker(nil),
		checker.NewBrewChecker(runner),
		checker.NewMASChecker(runner),
		checker.NewGitHubChecker(nil, ""),
	}
}

// checkAll runs update checks on all apps concurrently.
// It uses a semaphore to limit concurrency to 10 goroutines.
func checkAll(ctx context.Context, apps []*app.App, checkers []checker.Checker) []*checker.UpdateResult {
	const maxConcurrency = 10

	var (
		mu      sync.Mutex
		results []*checker.UpdateResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrency)
	)

	for _, a := range apps {
		// Find the first matching checker for this app.
		var ch checker.Checker
		for _, c := range checkers {
			if c.CanCheck(a) {
				ch = c
				break
			}
		}
		if ch == nil {
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore slot

		go func(a *app.App, ch checker.Checker) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore slot

			result, err := ch.Check(ctx, a)
			if err != nil {
				mu.Lock()
				results = append(results, &checker.UpdateResult{
					App:            a,
					Source:         ch.Name(),
					CurrentVersion: a.Version,
					Error:          err,
				})
				mu.Unlock()
				return
			}

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(a, ch)
	}

	wg.Wait()
	return results
}

// printCheckResults prints a table of check results to stdout.
func printCheckResults(cmd *cobra.Command, results []*checker.UpdateResult) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tCURRENT\tLATEST\tSOURCE")

	updateCount := 0
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(w, "%s\t%s\terror\t%s\n", r.App.Name, r.CurrentVersion, r.Source)
			continue
		}
		status := r.LatestVersion
		if r.HasUpdate {
			updateCount++
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.App.Name, r.CurrentVersion, status, r.Source)
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps checked, %d updates available\n", len(results), updateCount)
}
