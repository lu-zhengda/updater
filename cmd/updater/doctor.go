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
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/backup"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/luzhengda/updater/internal/history"
	"github.com/spf13/cobra"
)

var flagDoctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system health and dependencies",
	RunE:  runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&flagDoctorJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(doctorCmd)
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warning", "not_installed"
	Detail  string `json:"detail"`
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

	var stale []string

	for id := range cfg.GitHubMappings {
		if !bundleIDs[id] {
			stale = append(stale, fmt.Sprintf("github_mappings: %s", id))
		}
	}
	for id := range cfg.CaskMappings {
		if !bundleIDs[id] {
			stale = append(stale, fmt.Sprintf("cask_mappings: %s", id))
		}
	}
	for id := range cfg.Policies {
		if !bundleIDs[id] {
			stale = append(stale, fmt.Sprintf("policies: %s", id))
		}
	}
	for _, id := range cfg.PinnedApps {
		if !bundleIDs[id] {
			stale = append(stale, fmt.Sprintf("pinned_apps: %s", id))
		}
	}

	if len(stale) == 0 {
		return []doctorCheck{{Name: "Config validation", Status: "ok", Detail: "all mappings valid"}}
	}

	sort.Strings(stale) // deterministic output for tests
	detail := fmt.Sprintf("%d stale: %s", len(stale), strings.Join(stale, ", "))
	return []doctorCheck{{Name: "Config validation", Status: "warning", Detail: detail}}
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
