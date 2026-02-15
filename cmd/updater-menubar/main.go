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

	// Track dynamic app menu items and updatable results.
	var (
		mu        sync.Mutex
		appItems  []*systray.MenuItem
		updatable []*checker.UpdateResult
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
	var checkForUpdates func()
	checkForUpdates = func() {
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
		var newUpdatable []*checker.UpdateResult
		for _, r := range results {
			if r.HasUpdate && r.Error == nil && !cfg.IsPinned(r.App.BundleID) {
				newUpdatable = append(newUpdatable, r)
			}
		}

		// Update menu.
		clearAppItems()
		mu.Lock()
		defer mu.Unlock()

		updatable = newUpdatable

		if len(updatable) == 0 {
			systray.SetTitle("")
			mNoUpdates.Show()
			mNoUpdates.SetTitle("No updates")
			return
		}

		mNoUpdates.Hide()
		systray.SetTitle(fmt.Sprintf("%d", len(updatable)))

		// Per-app menu items (indexed so Update All can reference them).
		perAppItems := make([]*systray.MenuItem, 0, len(updatable))
		for _, r := range updatable {
			label := fmt.Sprintf("%s (%s \u2192 %s)", r.App.Name, r.CurrentVersion, r.LatestVersion)
			item := systray.AddMenuItem(label, fmt.Sprintf("Update %s", r.App.Name))
			appItems = append(appItems, item)
			perAppItems = append(perAppItems, item)

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

		// Separator and "Update All" item.
		sep := systray.AddMenuItem("", "")
		sep.Disable()
		appItems = append(appItems, sep)

		updateAllItem := systray.AddMenuItem(
			fmt.Sprintf("Update All (%d)", len(updatable)),
			"Update all available apps",
		)
		appItems = append(appItems, updateAllItem)

		// Snapshot updatable names and their menu items for the goroutine.
		type appSnapshot struct {
			name     string
			menuItem *systray.MenuItem
		}
		snapshot := make([]appSnapshot, len(updatable))
		for i, r := range updatable {
			snapshot[i] = appSnapshot{name: r.App.Name, menuItem: perAppItems[i]}
		}

		go func() {
			for range updateAllItem.ClickedCh {
				updateAllItem.Disable()
				updateAllItem.SetTitle("Updating...")

				for _, s := range snapshot {
					s.menuItem.SetTitle(fmt.Sprintf("Updating %s...", s.name))

					cmd := exec.Command("updater", "update", s.name)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					if err := cmd.Run(); err != nil {
						fmt.Fprintf(os.Stderr, "update %s failed: %v\n", s.name, err)
						s.menuItem.SetTitle(fmt.Sprintf("\u2717 %s (failed)", s.name))
					} else {
						s.menuItem.SetTitle(fmt.Sprintf("\u2713 %s (updated)", s.name))
					}
				}

				updateAllItem.SetTitle("Done!")
				time.Sleep(2 * time.Second)
				go checkForUpdates()
				return // Stop listening after one Update All cycle.
			}
		}()
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
