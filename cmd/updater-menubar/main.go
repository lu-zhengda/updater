package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/luzhengda/updater/internal/updater"
)

const checkInterval = 1 * time.Hour

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(generateIcon())
	systray.SetTooltip("Updater")

	mCheckNow := systray.AddMenuItem("Check Now", "Check for updates now")
	systray.AddSeparator()
	mNoUpdates := systray.AddMenuItem("No updates", "No updates available")
	mNoUpdates.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Updater")

	// Track dynamic app menu items.
	var (
		mu       sync.Mutex
		appItems []*systray.MenuItem
	)

	// clearAppItems removes all dynamically added app menu items.
	clearAppItems := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, item := range appItems {
			item.Hide()
		}
		appItems = nil
	}

	// checkForUpdates runs update checks and populates the menu.
	checkForUpdates := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		cfg, err := config.Load(config.DefaultPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
			return
		}

		runner := &checker.RealCmdRunner{}

		apps, err := updater.DiscoverApps()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to discover apps: %v\n", err)
			return
		}

		formulaApps, err := updater.DiscoverBrewFormulae(ctx, runner)
		if err == nil {
			apps = append(apps, formulaApps...)
		}

		apps, err = updater.EnrichApps(ctx, apps, cfg, runner)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to enrich apps: %v\n", err)
			return
		}

		apps = updater.FilterIgnored(apps, cfg)

		checkers := updater.BuildCheckers(runner, cfg.ResolveGitHubToken())
		results := updater.CheckAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

		// Collect updatable results.
		var updatable []*checker.UpdateResult
		for _, r := range results {
			if r.HasUpdate && r.Error == nil && !cfg.IsPinned(r.App.BundleID) {
				updatable = append(updatable, r)
			}
		}

		// Update menu.
		clearAppItems()
		mu.Lock()
		defer mu.Unlock()

		if len(updatable) == 0 {
			systray.SetTitle("")
			mNoUpdates.Show()
			mNoUpdates.SetTitle("No updates")
			return
		}

		mNoUpdates.Hide()
		systray.SetTitle(fmt.Sprintf("%d", len(updatable)))

		for _, r := range updatable {
			label := fmt.Sprintf("%s (%s \u2192 %s)", r.App.Name, r.CurrentVersion, r.LatestVersion)
			item := systray.AddMenuItem(label, fmt.Sprintf("Update %s", r.App.Name))
			appItems = append(appItems, item)

			// Launch update in background when clicked.
			go func(appName string, menuItem *systray.MenuItem) {
				for range menuItem.ClickedCh {
					cmd := exec.Command("updater", "update", appName)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					if err := cmd.Run(); err != nil {
						fmt.Fprintf(os.Stderr, "update %s failed: %v\n", appName, err)
					}
				}
			}(r.App.Name, item)
		}
	}

	// Initial check.
	go checkForUpdates()

	// Handle events.
	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-mCheckNow.ClickedCh:
				go checkForUpdates()
			case <-ticker.C:
				go checkForUpdates()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func onExit() {
	// Cleanup if needed.
}
