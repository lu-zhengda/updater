package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Policy constants for per-app update policies.
const (
	PolicyAuto       = "auto"
	PolicyManual     = "manual"
	PolicyNotifyOnly = "notify-only"
)

// Config holds the user configuration for the updater.
type Config struct {
	IgnoredApps              []string                         `yaml:"ignored_apps"`
	GitHubMappings           map[string]string                `yaml:"github_mappings"`
	CaskMappings             map[string]string                `yaml:"cask_mappings"`
	SourceOverrides          map[string]*SourceOverrideConfig `yaml:"source_overrides,omitempty"`
	GitHubToken              string                           `yaml:"github_token"`
	MaxConcurrent            int                              `yaml:"max_concurrent"`
	PinnedApps               []string                         `yaml:"pinned_apps"`
	MaxBackups               int                              `yaml:"max_backups"`
	ScheduleOffered          bool                             `yaml:"schedule_offered"`
	ScheduleInterval         int                              `yaml:"schedule_interval"`
	LastChecked              time.Time                        `yaml:"last_checked,omitempty"`
	Policies                 map[string]string                `yaml:"policies,omitempty"` // bundleID → "auto"|"manual"|"notify-only"
	InteractiveNotifications bool                             `yaml:"interactive_notifications"`
	ignoredSet               map[string]bool                  `yaml:"-"`
	pinnedSet                map[string]bool                  `yaml:"-"`
}

// DefaultPath returns the default config file path (~/.config/updater/config.yaml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "updater", "config.yaml")
}

// Parse decodes config YAML and applies config-local validation.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}
	if err := validateSourceOverrides(cfg.SourceOverrides); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg.buildIgnoredSet()
	cfg.buildPinnedSet()
	return &cfg, nil
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

	return Parse(data)
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

// SourceOverride returns the source override for the given bundle ID, if configured.
func (c *Config) SourceOverride(bundleID string) *SourceOverrideConfig {
	if c.SourceOverrides == nil {
		return nil
	}
	return c.SourceOverrides[bundleID]
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

// Ignore adds a bundle ID to the ignored list.
func (c *Config) Ignore(bundleID string) {
	if c.IsIgnored(bundleID) {
		return
	}
	c.IgnoredApps = append(c.IgnoredApps, bundleID)
	if c.ignoredSet == nil {
		c.ignoredSet = make(map[string]bool)
	}
	c.ignoredSet[bundleID] = true
}

// Unignore removes a bundle ID from the ignored list.
func (c *Config) Unignore(bundleID string) {
	if !c.IsIgnored(bundleID) {
		return
	}
	delete(c.ignoredSet, bundleID)
	filtered := make([]string, 0, len(c.IgnoredApps))
	for _, id := range c.IgnoredApps {
		if id != bundleID {
			filtered = append(filtered, id)
		}
	}
	c.IgnoredApps = filtered
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

// Policy returns the update policy for the given bundle ID.
// Returns empty string if no policy is set (defaults to normal behavior).
func (c *Config) Policy(bundleID string) string {
	if c.Policies == nil {
		return ""
	}
	return c.Policies[bundleID]
}

// SetPolicy sets the update policy for the given bundle ID.
func (c *Config) SetPolicy(bundleID, policy string) {
	if c.Policies == nil {
		c.Policies = make(map[string]string)
	}
	c.Policies[bundleID] = policy
}

// RemovePolicy removes the update policy for the given bundle ID.
func (c *Config) RemovePolicy(bundleID string) {
	delete(c.Policies, bundleID)
}

// RemoveGitHubMapping removes the GitHub mapping for the given bundle ID.
func (c *Config) RemoveGitHubMapping(bundleID string) {
	delete(c.GitHubMappings, bundleID)
}

// RemoveCaskMapping removes the cask mapping for the given bundle ID.
func (c *Config) RemoveCaskMapping(bundleID string) {
	delete(c.CaskMappings, bundleID)
}

// Merge merges an imported config into the current one and returns the result.
// Lists are unioned (deduplicated), maps are merged (imported overrides current),
// and non-zero scalars from imported override current values.
func Merge(current, imported *Config) *Config {
	result := *current // shallow copy

	// Union string slices with deduplication.
	result.IgnoredApps = unionStrings(current.IgnoredApps, imported.IgnoredApps)
	result.PinnedApps = unionStrings(current.PinnedApps, imported.PinnedApps)

	// Merge maps: imported overrides current.
	result.GitHubMappings = mergeMaps(current.GitHubMappings, imported.GitHubMappings)
	result.CaskMappings = mergeMaps(current.CaskMappings, imported.CaskMappings)
	result.SourceOverrides = cloneSourceOverrides(current.SourceOverrides)
	for bundleID, override := range imported.SourceOverrides {
		if result.SourceOverrides == nil {
			result.SourceOverrides = make(map[string]*SourceOverrideConfig, len(imported.SourceOverrides))
		}
		result.SourceOverrides[bundleID] = override.clone()
	}
	result.Policies = mergeMaps(current.Policies, imported.Policies)

	// Non-zero scalar overrides.
	if imported.GitHubToken != "" {
		result.GitHubToken = imported.GitHubToken
	}
	if imported.MaxConcurrent > 0 {
		result.MaxConcurrent = imported.MaxConcurrent
	}
	if imported.MaxBackups > 0 {
		result.MaxBackups = imported.MaxBackups
	}
	if imported.ScheduleInterval > 0 {
		result.ScheduleInterval = imported.ScheduleInterval
	}

	result.buildIgnoredSet()
	result.buildPinnedSet()
	return &result
}

// unionStrings returns the union of two string slices, preserving order and removing duplicates.
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var result []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// mergeMaps merges two string maps. Values from b override values from a.
func mergeMaps(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	result := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
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
