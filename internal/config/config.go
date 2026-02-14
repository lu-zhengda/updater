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
	ignoredSet     map[string]bool
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
	return &cfg, nil
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

// defaultConfig returns a Config with sensible zero values.
func defaultConfig() *Config {
	return &Config{
		ignoredSet: make(map[string]bool),
	}
}

// buildIgnoredSet populates the fast-lookup set from IgnoredApps.
func (c *Config) buildIgnoredSet() {
	c.ignoredSet = make(map[string]bool, len(c.IgnoredApps))
	for _, id := range c.IgnoredApps {
		c.ignoredSet[id] = true
	}
}
