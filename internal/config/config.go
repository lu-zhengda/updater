package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the user configuration for the updater.
type Config struct {
	IgnoredApps    []string          `yaml:"ignored_apps"`
	GitHubMappings map[string]string `yaml:"github_mappings"`
	CaskMappings   map[string]string `yaml:"cask_mappings"`
	GitHubToken    string            `yaml:"github_token"`
	MaxConcurrent  int               `yaml:"max_concurrent"`
	PinnedApps       []string          `yaml:"pinned_apps"`
	MaxBackups       int               `yaml:"max_backups"`
	ScheduleOffered  bool              `yaml:"schedule_offered"`
	ScheduleInterval int               `yaml:"schedule_interval"`
	ignoredSet       map[string]bool   `yaml:"-"`
	pinnedSet        map[string]bool   `yaml:"-"`
}

// DefaultPath returns the default config file path (~/.config/updater/config.yaml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "updater", "config.yaml")
}

// Load reads a YAML config file from path. If the file does not exist,
// a default (empty) config is returned without error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg.buildIgnoredSet()
	cfg.buildPinnedSet()
	return &cfg, nil
}

// Save writes the config to a YAML file at path.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// IsIgnored reports whether the given bundle ID is in the ignored list.
func (c *Config) IsIgnored(bundleID string) bool {
	if c.ignoredSet == nil {
		return false
	}
	return c.ignoredSet[bundleID]
}

// GitHubRepo returns the GitHub "owner/repo" mapping for the given bundle ID,
// or an empty string if no mapping exists.
func (c *Config) GitHubRepo(bundleID string) string {
	if c.GitHubMappings == nil {
		return ""
	}
	return c.GitHubMappings[bundleID]
}

// CaskToken returns the Homebrew cask token for the given bundle ID,
// or an empty string if no mapping exists.
func (c *Config) CaskToken(bundleID string) string {
	if c.CaskMappings == nil {
		return ""
	}
	return c.CaskMappings[bundleID]
}

// ResolveGitHubToken returns the GitHub API token, preferring the
// GITHUB_TOKEN environment variable over the config file value.
func (c *Config) ResolveGitHubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return c.GitHubToken
}

// MaxConcurrentOrDefault returns MaxConcurrent if set to a positive value,
// otherwise returns 10 as the default.
func (c *Config) MaxConcurrentOrDefault() int {
	if c.MaxConcurrent > 0 {
		return c.MaxConcurrent
	}
	return 10
}

// MaxBackupsOrDefault returns MaxBackups if set to a positive value,
// otherwise returns 1 as the default.
func (c *Config) MaxBackupsOrDefault() int {
	if c.MaxBackups > 0 {
		return c.MaxBackups
	}
	return 1
}

// ScheduleIntervalOrDefault returns ScheduleInterval if set to a positive value,
// otherwise returns 24 as the default (hours).
func (c *Config) ScheduleIntervalOrDefault() int {
	if c.ScheduleInterval > 0 {
		return c.ScheduleInterval
	}
	return 24
}

// IsPinned reports whether the given bundle ID is in the pinned list.
func (c *Config) IsPinned(bundleID string) bool {
	if c.pinnedSet == nil {
		return false
	}
	return c.pinnedSet[bundleID]
}

// Pin adds a bundle ID to the pinned list.
func (c *Config) Pin(bundleID string) {
	if c.IsPinned(bundleID) {
		return
	}
	c.PinnedApps = append(c.PinnedApps, bundleID)
	if c.pinnedSet == nil {
		c.pinnedSet = make(map[string]bool)
	}
	c.pinnedSet[bundleID] = true
}

// Unpin removes a bundle ID from the pinned list.
func (c *Config) Unpin(bundleID string) {
	if !c.IsPinned(bundleID) {
		return
	}
	delete(c.pinnedSet, bundleID)
	filtered := make([]string, 0, len(c.PinnedApps))
	for _, id := range c.PinnedApps {
		if id != bundleID {
			filtered = append(filtered, id)
		}
	}
	c.PinnedApps = filtered
}

// defaultConfig returns a Config with sensible zero values.
func defaultConfig() *Config {
	return &Config{
		ignoredSet: make(map[string]bool),
		pinnedSet:  make(map[string]bool),
	}
}

// buildIgnoredSet populates the fast-lookup set from IgnoredApps.
func (c *Config) buildIgnoredSet() {
	c.ignoredSet = make(map[string]bool, len(c.IgnoredApps))
	for _, id := range c.IgnoredApps {
		c.ignoredSet[id] = true
	}
}

// buildPinnedSet populates the fast-lookup set from PinnedApps.
func (c *Config) buildPinnedSet() {
	c.pinnedSet = make(map[string]bool, len(c.PinnedApps))
	for _, id := range c.PinnedApps {
		c.pinnedSet[id] = true
	}
}
