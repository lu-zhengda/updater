// The menu bar app, launched with `updater menubar run`. It periodically runs
// the shared discovery/check pipeline in-process and shows available updates
// in a menu bar dropdown. Individual updates and "Update All" re-exec this
// same binary (`updater update --bundle-id ...`) so update behavior (backups,
// history, per-source actions) stays identical to the terminal experience.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/updater"
)

// extraPathDirs are prepended to PATH when missing. Under launchd the PATH is
// /usr/bin:/bin:/usr/sbin:/sbin, which would hide brew, mas, npm, uv, and cargo.
var extraPathDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// runMenubarApp starts the systray event loop. It blocks until Quit.
func runMenubarApp() error {
	ensurePath()
	systray.Run(menubarOnReady, func() {})
	return nil
}

// ensurePath prepends well-known tool directories missing from PATH.
func ensurePath() {
	path := os.Getenv("PATH")
	parts := filepath.SplitList(path)
	present := make(map[string]bool, len(parts))
	for _, p := range parts {
		present[p] = true
	}
	var prepend []string
	for _, d := range extraPathDirs {
		if !present[d] {
			prepend = append(prepend, d)
		}
	}
	if len(prepend) > 0 {
		os.Setenv("PATH", strings.Join(prepend, string(os.PathListSeparator))+string(os.PathListSeparator)+path)
	}
}

// menubarApp owns all menu state. The menu is fully rebuilt on every refresh
// via systray.ResetMenu; click-handler goroutines are scoped to a generation
// context so rebuilds never leak listeners.
type menubarApp struct {
	mu         sync.Mutex
	refreshing bool
	genCancel  context.CancelFunc
	lastCheck  time.Time
	notified   map[string]string // bundleID -> latest version already notified
}

func menubarOnReady() {
	systray.SetTemplateIcon(generateIcon(), generateIcon())
	systray.SetTooltip("Updater")

	app := &menubarApp{notified: map[string]string{}}
	app.rebuild(nil, "Checking for updates…")
	go app.refresh()

	go func() {
		for {
			// Re-read the interval each cycle so config changes apply without restart.
			time.Sleep(menubarCheckInterval())
			app.refresh()
		}
	}()
}

// menubarCheckInterval reads the configured schedule interval, defaulting sensibly.
func menubarCheckInterval() time.Duration {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return 6 * time.Hour
	}
	return time.Duration(cfg.ScheduleIntervalOrDefault()) * time.Hour
}

// refresh runs the full check pipeline and rebuilds the menu. Only one
// refresh runs at a time; extra requests are dropped.
func (m *menubarApp) refresh() {
	m.mu.Lock()
	if m.refreshing {
		m.mu.Unlock()
		return
	}
	m.refreshing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.refreshing = false
		m.mu.Unlock()
	}()

	m.rebuild(nil, "Checking for updates…")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	updatable, err := m.runCheck(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "check failed: %v\n", err)
		m.rebuild(nil, "Check failed — see log")
		return
	}

	m.mu.Lock()
	m.lastCheck = time.Now()
	m.mu.Unlock()

	m.notifyNewUpdates(updatable)
	m.rebuild(updatable, "")
}

// runCheck executes the shared discovery/check pipeline in-process.
func (m *menubarApp) runCheck(ctx context.Context) ([]*checker.UpdateResult, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	runner := newRunner()
	apps, err := discoverAll(ctx, cfg, runner)
	if err != nil {
		return nil, err
	}
	apps = updater.FilterIgnored(apps, cfg)

	checkers := updater.BuildCheckers(runner, cfg.ResolveGitHubToken())
	results := updater.CheckAll(ctx, apps, checkers, cfg.MaxConcurrentOrDefault())

	var updatable []*checker.UpdateResult
	for _, r := range results {
		if r.HasUpdate && r.Error == nil && !cfg.IsPinned(r.App.BundleID) {
			updatable = append(updatable, r)
		}
	}
	return updatable, nil
}

// notifyNewUpdates posts one macOS notification when updates appear that the
// user has not been notified about yet (per app+version).
func (m *menubarApp) notifyNewUpdates(updatable []*checker.UpdateResult) {
	m.mu.Lock()
	var fresh []string
	for _, r := range updatable {
		if m.notified[r.App.BundleID] != r.LatestVersion {
			m.notified[r.App.BundleID] = r.LatestVersion
			fresh = append(fresh, r.App.Name)
		}
	}
	m.mu.Unlock()

	if len(fresh) == 0 {
		return
	}
	msg := strings.Join(fresh, ", ")
	if len(fresh) > 3 {
		msg = fmt.Sprintf("%s and %d more", strings.Join(fresh[:3], ", "), len(fresh)-3)
	}
	title := fmt.Sprintf("%d update(s) available", len(fresh))
	script := fmt.Sprintf("display notification %q with title %q", msg, title)
	_ = exec.Command("osascript", "-e", script).Run()
}

// rebuild replaces the entire menu. updatable is the current update list;
// status, when non-empty, is shown as a disabled line instead of app items.
func (m *menubarApp) rebuild(updatable []*checker.UpdateResult, status string) {
	m.mu.Lock()
	if m.genCancel != nil {
		m.genCancel() // stop click listeners from the previous menu generation
	}
	gen, cancel := context.WithCancel(context.Background())
	m.genCancel = cancel
	lastCheck := m.lastCheck
	m.mu.Unlock()

	systray.ResetMenu()

	checkNow := systray.AddMenuItem("Check Now", "Check for updates now")
	onClick(gen, checkNow, func() { go m.refresh() })

	if !lastCheck.IsZero() {
		item := systray.AddMenuItem("Last checked: "+lastCheck.Format("15:04"), "")
		item.Disable()
	}
	systray.AddSeparator()

	switch {
	case status != "":
		systray.SetTitle("")
		item := systray.AddMenuItem(status, "")
		item.Disable()
	case len(updatable) == 0:
		systray.SetTitle("")
		item := systray.AddMenuItem("Everything up to date", "")
		item.Disable()
	default:
		systray.SetTitle(fmt.Sprintf("%d", len(updatable)))
		items := make([]*systray.MenuItem, len(updatable))
		for i, r := range updatable {
			label := fmt.Sprintf("%s  %s → %s", r.App.Name, r.CurrentVersion, r.LatestVersion)
			items[i] = systray.AddMenuItem(label, "Update "+r.App.Name)
			r, item := r, items[i]
			onClick(gen, item, func() {
				go func() {
					m.runUpdate(r.App.Name, r.App.BundleID, item)
					m.refresh()
				}()
			})
		}
		if len(updatable) > 1 {
			all := systray.AddMenuItem(fmt.Sprintf("Update All (%d)", len(updatable)), "Update all listed apps")
			onClick(gen, all, func() {
				go func() {
					all.Disable()
					all.SetTitle("Updating…")
					for i, r := range updatable {
						m.runUpdate(r.App.Name, r.App.BundleID, items[i])
					}
					m.refresh()
				}()
			})
		}
	}

	systray.AddSeparator()
	login := systray.AddMenuItemCheckbox("Start at Login", "Keep the menu bar app running via launchd", loginItemInstalled())
	onClick(gen, login, func() { go toggleLoginItem(login) })

	quit := systray.AddMenuItem("Quit", "Quit Updater")
	onClick(gen, quit, systray.Quit)
}

// onClick invokes fn on every click until the menu generation is replaced.
func onClick(gen context.Context, item *systray.MenuItem, fn func()) {
	go func() {
		for {
			select {
			case <-gen.Done():
				return
			case <-item.ClickedCh:
				fn()
			}
		}
	}()
}

// runUpdate updates a single app by re-executing this binary, reflecting
// progress in the menu item title. Apps are targeted by bundle ID (exact)
// with the name used only for display.
func (m *menubarApp) runUpdate(name, bundleID string, item *systray.MenuItem) {
	item.Disable()
	item.SetTitle("Updating " + name + "…")

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot locate own executable: %v\n", err)
		item.SetTitle("✗ " + name + " (failed)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "update", "--bundle-id", bundleID)
	cmd.Stdout = os.Stderr // keep CLI output in our log, off the notification path
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "update %s failed: %v\n", name, err)
		item.SetTitle("✗ " + name + " (failed)")
		return
	}
	item.SetTitle("✓ " + name + " (updated)")
}

// loginItemInstalled reports whether the LaunchAgent plist exists.
func loginItemInstalled() bool {
	path, err := menubarPlistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// toggleLoginItem installs or removes the LaunchAgent in-process — the agent
// lifecycle lives in this same binary (see menubar.go).
func toggleLoginItem(item *systray.MenuItem) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	runner := newRunner()

	if loginItemInstalled() {
		if err := removeMenubarAgent(ctx, runner); err != nil {
			fmt.Fprintf(os.Stderr, "failed to remove login item: %v\n", err)
			return
		}
		item.Uncheck()
		return
	}
	if err := installMenubarAgent(ctx, runner); err != nil {
		fmt.Fprintf(os.Stderr, "failed to install login item: %v\n", err)
		return
	}
	item.Check()
}
