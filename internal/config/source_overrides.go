package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SourceOverrideKind selects which updater source to use for a bundle ID.
type SourceOverrideKind string

const (
	SourceOverrideKindGitHub  SourceOverrideKind = "github"
	SourceOverrideKindSparkle SourceOverrideKind = "sparkle"
	SourceOverrideKindBrew    SourceOverrideKind = "brew"
)

// SourceOverrideConfig pins a bundle ID to a specific source configuration.
type SourceOverrideConfig struct {
	Kind       SourceOverrideKind `yaml:"kind"`
	Repo       string             `yaml:"repo,omitempty"`
	AppcastURL string             `yaml:"appcast_url,omitempty"`
	Cask       string             `yaml:"cask,omitempty"`
}

func (s *SourceOverrideConfig) UnmarshalYAML(node *yaml.Node) error {
	type rawSourceOverrideConfig SourceOverrideConfig

	var raw rawSourceOverrideConfig
	if err := node.Decode(&raw); err != nil {
		return err
	}

	allowedFields := sourceOverrideAllowedFields(raw.Kind)
	if len(allowedFields) == 0 {
		return fmt.Errorf("unknown source_overrides kind %q", raw.Kind)
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		field := node.Content[i].Value
		if !allowedFields[field] {
			return fmt.Errorf("unexpected field %q for source_overrides kind %q", field, raw.Kind)
		}
	}

	switch raw.Kind {
	case SourceOverrideKindGitHub:
		if !validGitHubRepo(raw.Repo) {
			return fmt.Errorf("github repo must match owner/repo")
		}
	case SourceOverrideKindSparkle:
		if !validSparkleAppcastURL(raw.AppcastURL) {
			return fmt.Errorf("sparkle appcast_url must start with https://")
		}
	case SourceOverrideKindBrew:
		if strings.TrimSpace(raw.Cask) == "" {
			return fmt.Errorf("brew cask must be non-empty")
		}
	}

	*s = SourceOverrideConfig(raw)
	return nil
}

func sourceOverrideAllowedFields(kind SourceOverrideKind) map[string]bool {
	switch kind {
	case SourceOverrideKindGitHub:
		return map[string]bool{
			"kind": true,
			"repo": true,
		}
	case SourceOverrideKindSparkle:
		return map[string]bool{
			"kind":        true,
			"appcast_url": true,
		}
	case SourceOverrideKindBrew:
		return map[string]bool{
			"kind": true,
			"cask": true,
		}
	default:
		return nil
	}
}

func validGitHubRepo(repo string) bool {
	if strings.TrimSpace(repo) != repo {
		return false
	}

	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}

	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || strings.ContainsAny(part, " \t\r\n") {
			return false
		}
	}

	return true
}

func validSparkleAppcastURL(appcastURL string) bool {
	return strings.HasPrefix(appcastURL, "https://")
}

func validateSourceOverrides(overrides map[string]*SourceOverrideConfig) error {
	for bundleID, override := range overrides {
		if override == nil {
			return fmt.Errorf("source_overrides entry must be a non-null mapping for %q", bundleID)
		}
	}

	return nil
}

func cloneSourceOverrides(overrides map[string]*SourceOverrideConfig) map[string]*SourceOverrideConfig {
	if len(overrides) == 0 {
		return nil
	}

	cloned := make(map[string]*SourceOverrideConfig, len(overrides))
	for bundleID, override := range overrides {
		cloned[bundleID] = override.clone()
	}
	return cloned
}

func (s *SourceOverrideConfig) clone() *SourceOverrideConfig {
	if s == nil {
		return nil
	}

	cloned := *s
	return &cloned
}
