// The built-in updates window (`updater window`), spawned from the menu bar
// app's "Open Updater…" item. A native WKWebView window showing the full
// update list — the app's equivalent of the TUI. It renders the cached last
// check instantly, streams a fresh check, and can update, pin, or ignore
// apps in-process using the same pipeline as the CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/installer"
	"github.com/lu-zhengda/updater/internal/updater"
	"github.com/spf13/cobra"
	webview "github.com/webview/webview_go"
)

var windowCmd = &cobra.Command{
	Use:    "window",
	Short:  "Open the Updater window",
	Hidden: true, // launched by the menu bar app; not a CLI surface
	RunE: func(_ *cobra.Command, _ []string) error {
		return runWindow()
	},
}

func init() {
	rootCmd.AddCommand(windowCmd)
}

// windowState holds live results between a refresh and update actions.
type windowState struct {
	mu      sync.Mutex
	results map[string]*checker.UpdateResult // bundleID -> live result
}

func runWindow() error {
	ensurePath()

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Updater")
	w.SetSize(760, 560, webview.HintNone)
	// Activate once the run loop is going — calls before Run() are dropped.
	w.Dispatch(func() { activateWindow(w.Window()) })

	state := &windowState{results: map[string]*checker.UpdateResult{}}

	eval := func(js string) {
		w.Dispatch(func() { w.Eval(js) })
	}
	push := func(fn string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		eval(fn + "(" + string(data) + ")")
	}

	// goInit returns the cached last check for instant first paint.
	_ = w.Bind("goInit", func() map[string]any {
		cache := readCheckCache()
		if cache == nil {
			return map[string]any{"entries": []cacheEntry{}, "checkedAt": ""}
		}
		return map[string]any{
			"entries":   cache.Entries,
			"checkedAt": cache.CheckedAt.Format("15:04"),
		}
	})

	// goRefresh runs the full pipeline, streaming progress to the page.
	_ = w.Bind("goRefresh", func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			cfg, err := config.Load(config.DefaultPath())
			if err != nil {
				push("onError", err.Error())
				return
			}
			runner := newRunner()
			apps, err := discoverAll(ctx, cfg, runner)
			if err != nil {
				push("onError", err.Error())
				return
			}
			apps = filterIgnored(apps, cfg)
			push("onProgress", map[string]int{"done": 0, "total": len(apps)})

			checkers := buildCheckers(runner, cfg.ResolveGitHubToken())
			var done int
			results := updater.CheckAllProgress(ctx, apps, checkers, cfg.MaxConcurrentOrDefault(), func(*checker.UpdateResult) {
				done++ // serialized by CheckAllProgress
				if done%5 == 0 {
					push("onProgress", map[string]int{"done": done, "total": len(apps)})
				}
			})

			state.mu.Lock()
			state.results = map[string]*checker.UpdateResult{}
			for _, r := range results {
				state.results[r.App.BundleID] = r
			}
			state.mu.Unlock()

			entries := cacheEntriesFromResults(results, cfg.IsPinned)
			writeCheckCache(entries)
			push("onResults", map[string]any{
				"entries":   entries,
				"checkedAt": time.Now().Format("15:04"),
			})
		}()
	})

	// goUpdate updates one app (by bundle ID) from the live results.
	_ = w.Bind("goUpdate", func(bundleID string) {
		go func() {
			ok, msg := updateFromWindow(state, bundleID)
			push("onUpdateDone", map[string]any{"bundleId": bundleID, "ok": ok, "message": msg})
		}()
	})

	// goPin / goUnpin / goIgnore mutate the shared config.
	_ = w.Bind("goPin", func(bundleID string) { windowMutateConfig(func(c *config.Config) { c.Pin(bundleID) }) })
	_ = w.Bind("goUnpin", func(bundleID string) { windowMutateConfig(func(c *config.Config) { c.Unpin(bundleID) }) })
	_ = w.Bind("goIgnore", func(bundleID string) { windowMutateConfig(func(c *config.Config) { c.Ignore(bundleID) }) })

	w.SetHtml(windowHTML)
	w.Run()
	return nil
}

// updateFromWindow performs one update using the live result set. Returns
// success and a short message.
func updateFromWindow(state *windowState, bundleID string) (bool, string) {
	state.mu.Lock()
	result := state.results[bundleID]
	state.mu.Unlock()
	if result == nil {
		return false, "refresh before updating"
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return false, err.Error()
	}
	runner := newRunner()
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)
	inst := installer.New(runner, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	updateErr, rolledBack := executeUpdate(ctx, result, runner, bm, inst)
	_ = history.Append(history.DefaultPath(), history.Entry{
		AppName:     result.App.Name,
		BundleID:    result.App.BundleID,
		FromVersion: result.CurrentVersion,
		ToVersion:   result.LatestVersion,
		Source:      result.Source,
		Timestamp:   time.Now(),
		Success:     updateErr == nil || errors.Is(updateErr, checker.ErrOpenedExternally),
		RolledBack:  rolledBack,
	})

	if errors.Is(updateErr, checker.ErrOpenedExternally) {
		return true, "opened externally"
	}
	if updateErr != nil {
		return false, updateErr.Error()
	}
	return true, "updated to " + result.LatestVersion
}

func windowMutateConfig(fn func(*config.Config)) {
	path := config.DefaultPath()
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		return
	}
	fn(cfg)
	if err := cfg.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "failed to save config: %v\n", err)
	}
}
