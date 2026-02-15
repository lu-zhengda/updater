package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

var flagDoctorJSON bool
var flagDoctorFix bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system health and dependencies",
	RunE:  runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&flagDoctorJSON, "json", false, "output as JSON")
	doctorCmd.Flags().BoolVar(&flagDoctorFix, "fix", false, "auto-fix issues where possible")
	rootCmd.AddCommand(doctorCmd)
}

type doctorCheck struct {
	Name    string        `json:"name"`
	Status  string        `json:"status"` // "ok", "warning", "not_installed"
	Detail  string        `json:"detail"`
	fixFn   func() string `json:"-"` // returns description of what was fixed; nil if no fix available
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	runner := &checker.RealCmdRunner{}
	var checks []doctorCheck

	// External tools.
	tools := []string{"brew", "mas", "osascript", "hdiutil", "ditto", "sw_vers"}
	for _, tool := range tools {
		checks = append(checks, checkTool(ctx, runner, tool))
	}

	// Config file.
	checks = append(checks, checkConfig())

	// Config validation (cross-reference mappings with discovered apps).
	checks = append(checks, checkConfigValidation()...)

	// Backup directory.
	checks = append(checks, checkBackups())

	// History file.
	checks = append(checks, checkHistory())

	// Schedule agent.
	checks = append(checks, checkScheduleAgent())

	// Menubar agent.
	checks = append(checks, checkMenubarAgent())

	// GitHub token.
	cfg, _ := config.Load(config.DefaultPath())
	if cfg != nil {
		checks = append(checks, checkGitHubToken(ctx, cfg))
	}

	// Network.
	checks = append(checks, checkNetwork())

	// Disk space.
	checks = append(checks, checkDiskSpace())

	// Brew index freshness.
	checks = append(checks, checkBrewFreshness(ctx, runner))

	// Sparkle feed connectivity.
	checks = append(checks, checkSparkleFeedConnectivity())

	if flagDoctorJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(checks)
	}

	for _, c := range checks {
		var prefix string
		switch c.Status {
		case "ok":
			prefix = "[ok]"
		case "warning":
			prefix = "[!!]"
		case "not_installed":
			prefix = "[--]"
		default:
			prefix = "[??]"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s)\n", prefix, c.Name, c.Detail)
	}

	if flagDoctorFix {
		for _, c := range checks {
			if c.Status == "warning" && c.fixFn != nil {
				desc := c.fixFn()
				fmt.Fprintf(cmd.OutOrStdout(), "[fix] %s: %s\n", c.Name, desc)
			}
		}
	}

	return nil
}

func checkTool(ctx context.Context, runner checker.CmdRunner, tool string) doctorCheck {
	output, err := runner.Run(ctx, "which", tool)
	if err != nil {
		return doctorCheck{Name: tool, Status: "warning", Detail: "not found"}
	}
	path := string(output)
	if len(path) > 0 && path[len(path)-1] == '\n' {
		path = path[:len(path)-1]
	}
	return doctorCheck{Name: tool, Status: "ok", Detail: path}
}

func checkConfig() doctorCheck {
	cfgPath := config.DefaultPath()
	_, err := config.Load(cfgPath)
	if err != nil {
		return doctorCheck{Name: "Config", Status: "warning", Detail: fmt.Sprintf("error: %v", err)}
	}
	if _, statErr := os.Stat(cfgPath); os.IsNotExist(statErr) {
		return doctorCheck{Name: "Config", Status: "ok", Detail: "using defaults"}
	}
	return doctorCheck{Name: "Config", Status: "ok", Detail: cfgPath}
}

// checkConfigValidation discovers apps and cross-references config entries.
func checkConfigValidation() []doctorCheck {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return []doctorCheck{{Name: "Config validation", Status: "warning", Detail: fmt.Sprintf("cannot load config: %v", err)}}
	}
	apps, err := discoverApps()
	if err != nil {
		return []doctorCheck{{Name: "Config validation", Status: "warning", Detail: fmt.Sprintf("cannot discover apps: %v", err)}}
	}
	return validateConfigMappings(cfg, apps)
}

// validateConfigMappings checks config entries against discovered app bundle IDs.
func validateConfigMappings(cfg *config.Config, apps []*app.App) []doctorCheck {
	bundleIDs := make(map[string]bool, len(apps))
	for _, a := range apps {
		bundleIDs[a.BundleID] = true
	}

	type staleEntry struct {
		label    string // e.g., "github_mappings: com.example.app"
		bundleID string
		kind     string // "github", "cask", "policy", "pinned"
	}
	var staleEntries []staleEntry

	for id := range cfg.GitHubMappings {
		if !bundleIDs[id] {
			staleEntries = append(staleEntries, staleEntry{
				label: fmt.Sprintf("github_mappings: %s", id), bundleID: id, kind: "github",
			})
		}
	}
	for id := range cfg.CaskMappings {
		if !bundleIDs[id] {
			staleEntries = append(staleEntries, staleEntry{
				label: fmt.Sprintf("cask_mappings: %s", id), bundleID: id, kind: "cask",
			})
		}
	}
	for id := range cfg.Policies {
		if !bundleIDs[id] {
			staleEntries = append(staleEntries, staleEntry{
				label: fmt.Sprintf("policies: %s", id), bundleID: id, kind: "policy",
			})
		}
	}
	for _, id := range cfg.PinnedApps {
		if !bundleIDs[id] {
			staleEntries = append(staleEntries, staleEntry{
				label: fmt.Sprintf("pinned_apps: %s", id), bundleID: id, kind: "pinned",
			})
		}
	}

	if len(staleEntries) == 0 {
		return []doctorCheck{{Name: "Config validation", Status: "ok", Detail: "all mappings valid"}}
	}

	labels := make([]string, len(staleEntries))
	for i, e := range staleEntries {
		labels[i] = e.label
	}
	sort.Strings(labels) // deterministic output for tests

	detail := fmt.Sprintf("%d stale: %s", len(staleEntries), strings.Join(labels, ", "))
	check := doctorCheck{Name: "Config validation", Status: "warning", Detail: detail}

	check.fixFn = func() string {
		fixCfg, err := config.Load(config.DefaultPath())
		if err != nil {
			return fmt.Sprintf("failed to load config: %v", err)
		}
		removed := 0
		for _, e := range staleEntries {
			switch e.kind {
			case "github":
				fixCfg.RemoveGitHubMapping(e.bundleID)
				removed++
			case "cask":
				fixCfg.RemoveCaskMapping(e.bundleID)
				removed++
			case "policy":
				fixCfg.RemovePolicy(e.bundleID)
				removed++
			case "pinned":
				fixCfg.Unpin(e.bundleID)
				removed++
			}
		}
		if err := fixCfg.Save(config.DefaultPath()); err != nil {
			return fmt.Sprintf("removed %d entries but failed to save: %v", removed, err)
		}
		return fmt.Sprintf("removed %d stale entries", removed)
	}

	return []doctorCheck{check}
}

func checkBackups() doctorCheck {
	baseDir := backup.DefaultBaseDir()
	info, err := os.Stat(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{Name: "Backups", Status: "ok", Detail: "no backups yet"}
		}
		return doctorCheck{Name: "Backups", Status: "warning", Detail: fmt.Sprintf("error: %v", err)}
	}
	if !info.IsDir() {
		return doctorCheck{Name: "Backups", Status: "warning", Detail: "path is not a directory"}
	}

	// Count backup subdirectories.
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return doctorCheck{Name: "Backups", Status: "warning", Detail: fmt.Sprintf("unreadable: %v", err)}
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}

	// Check writable by creating a temp file.
	tmpFile := filepath.Join(baseDir, ".doctor-write-test")
	if err := os.WriteFile(tmpFile, []byte("test"), 0o644); err != nil {
		return doctorCheck{Name: "Backups", Status: "warning", Detail: fmt.Sprintf("%d apps, not writable", count)}
	}
	os.Remove(tmpFile)

	return doctorCheck{Name: "Backups", Status: "ok", Detail: fmt.Sprintf("%d apps", count)}
}

func checkHistory() doctorCheck {
	histPath := history.DefaultPath()
	entries, err := history.List(histPath)
	if err != nil {
		return doctorCheck{Name: "History", Status: "warning", Detail: fmt.Sprintf("error: %v", err)}
	}
	if entries == nil {
		return doctorCheck{Name: "History", Status: "ok", Detail: "no history file yet"}
	}
	return doctorCheck{Name: "History", Status: "ok", Detail: fmt.Sprintf("%d entries", len(entries))}
}

func checkScheduleAgent() doctorCheck {
	p, err := schedulePlistPath()
	if err != nil {
		return doctorCheck{Name: "Schedule agent", Status: "warning", Detail: err.Error()}
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return doctorCheck{Name: "Schedule agent", Status: "not_installed", Detail: "not installed"}
	}
	if scheduleExists() {
		return doctorCheck{Name: "Schedule agent", Status: "ok", Detail: "installed"}
	}
	return doctorCheck{Name: "Schedule agent", Status: "warning", Detail: "plist exists but not loaded"}
}

func checkMenubarAgent() doctorCheck {
	p, err := menubarPlistPath()
	if err != nil {
		return doctorCheck{Name: "Menubar agent", Status: "warning", Detail: err.Error()}
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return doctorCheck{Name: "Menubar agent", Status: "not_installed", Detail: "not installed"}
	}
	return doctorCheck{Name: "Menubar agent", Status: "ok", Detail: "installed"}
}

func checkGitHubToken(ctx context.Context, cfg *config.Config) doctorCheck {
	token := cfg.ResolveGitHubToken()
	if token == "" {
		return doctorCheck{Name: "GitHub token", Status: "not_installed", Detail: "not configured"}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return doctorCheck{Name: "GitHub token", Status: "warning", Detail: "failed to create request"}
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return doctorCheck{Name: "GitHub token", Status: "warning", Detail: "request failed"}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return doctorCheck{Name: "GitHub token", Status: "warning", Detail: "invalid token"}
	}

	var rateResp struct {
		Rate struct {
			Remaining int `json:"remaining"`
			Limit     int `json:"limit"`
		} `json:"rate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rateResp); err != nil {
		return doctorCheck{Name: "GitHub token", Status: "ok", Detail: "valid (could not read rate)"}
	}

	return doctorCheck{Name: "GitHub token", Status: "ok",
		Detail: fmt.Sprintf("%d/%d remaining", rateResp.Rate.Remaining, rateResp.Rate.Limit)}
}

func checkNetwork() doctorCheck {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com")
	if err != nil {
		return doctorCheck{Name: "Network", Status: "warning", Detail: "unreachable"}
	}
	resp.Body.Close()
	return doctorCheck{Name: "Network", Status: "ok", Detail: "reachable"}
}

func checkDiskSpace() doctorCheck {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return doctorCheck{Name: "Disk space", Status: "warning", Detail: fmt.Sprintf("error: %v", err)}
	}
	freeGB := float64(stat.Bavail) * float64(stat.Bsize) / (1 << 30)
	if freeGB < 1.0 {
		return doctorCheck{Name: "Disk space", Status: "warning", Detail: fmt.Sprintf("%.1f GB free (low)", freeGB)}
	}
	return doctorCheck{Name: "Disk space", Status: "ok", Detail: fmt.Sprintf("%.1f GB free", freeGB)}
}

func checkBrewFreshness(ctx context.Context, runner checker.CmdRunner) doctorCheck {
	output, err := runner.Run(ctx, "brew", "--cache")
	if err != nil {
		return doctorCheck{Name: "Brew index", Status: "warning", Detail: "brew not available"}
	}
	cacheDir := strings.TrimSpace(string(output))
	info, err := os.Stat(cacheDir)
	if err != nil {
		return doctorCheck{Name: "Brew index", Status: "ok", Detail: "no cache directory"}
	}
	age := time.Since(info.ModTime())
	days := int(age.Hours() / 24)
	if days > 7 {
		check := doctorCheck{
			Name: "Brew index", Status: "warning",
			Detail: fmt.Sprintf("stale (%d days since last update)", days),
		}
		check.fixFn = func() string {
			r := &checker.RealCmdRunner{}
			_, err := r.Run(context.Background(), "brew", "update")
			if err != nil {
				return fmt.Sprintf("brew update failed: %v", err)
			}
			return "ran brew update"
		}
		return check
	}
	return doctorCheck{Name: "Brew index", Status: "ok", Detail: fmt.Sprintf("fresh (%d days)", days)}
}

func checkSparkleFeedConnectivity() doctorCheck {
	apps, err := discoverApps()
	if err != nil {
		return doctorCheck{Name: "Sparkle feeds", Status: "warning", Detail: "cannot discover apps"}
	}
	var feedURLs []string
	for _, a := range apps {
		if a.FeedURL != "" && len(feedURLs) < 3 {
			feedURLs = append(feedURLs, a.FeedURL)
		}
	}
	if len(feedURLs) == 0 {
		return doctorCheck{Name: "Sparkle feeds", Status: "ok", Detail: "no feeds to check"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	reachable := 0
	for _, u := range feedURLs {
		resp, err := client.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				reachable++
			}
		}
	}
	if reachable == len(feedURLs) {
		return doctorCheck{Name: "Sparkle feeds", Status: "ok", Detail: fmt.Sprintf("%d/%d reachable", reachable, len(feedURLs))}
	}
	return doctorCheck{Name: "Sparkle feeds", Status: "warning", Detail: fmt.Sprintf("%d/%d reachable", reachable, len(feedURLs))}
}
