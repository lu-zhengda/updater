package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig_SourceOverrides(t *testing.T) {
	t.Run("valid github sparkle and brew overrides load successfully", func(t *testing.T) {
		cfg := loadConfigFromYAML(t, `source_overrides:
  com.example.github:
    kind: github
    repo: cli/cli
  com.example.sparkle:
    kind: sparkle
    appcast_url: https://example.com/appcast.xml
  com.example.brew:
    kind: brew
    cask: iterm2
`)

		if got := cfg.SourceOverride("com.example.github"); got == nil {
			t.Fatal("expected github source override to load")
		} else {
			if got.Kind != SourceOverrideKindGitHub {
				t.Errorf("Kind = %q, want %q", got.Kind, SourceOverrideKindGitHub)
			}
			if got.Repo != "cli/cli" {
				t.Errorf("Repo = %q, want %q", got.Repo, "cli/cli")
			}
		}

		if got := cfg.SourceOverride("com.example.sparkle"); got == nil {
			t.Fatal("expected sparkle source override to load")
		} else {
			if got.Kind != SourceOverrideKindSparkle {
				t.Errorf("Kind = %q, want %q", got.Kind, SourceOverrideKindSparkle)
			}
			if got.AppcastURL != "https://example.com/appcast.xml" {
				t.Errorf("AppcastURL = %q, want %q", got.AppcastURL, "https://example.com/appcast.xml")
			}
		}

		if got := cfg.SourceOverride("com.example.brew"); got == nil {
			t.Fatal("expected brew source override to load")
		} else {
			if got.Kind != SourceOverrideKindBrew {
				t.Errorf("Kind = %q, want %q", got.Kind, SourceOverrideKindBrew)
			}
			if got.Cask != "iterm2" {
				t.Errorf("Cask = %q, want %q", got.Cask, "iterm2")
			}
		}
	})

	tests := []struct {
		name        string
		content     string
		wantMessage string
	}{
		{
			name: "invalid github repo fails",
			content: `source_overrides:
  com.example.github:
    kind: github
    repo: cli
`,
			wantMessage: "github repo must match owner/repo",
		},
		{
			name: "unknown kind fails",
			content: `source_overrides:
  com.example.unknown:
    kind: gitlab
`,
			wantMessage: "unknown source_overrides kind",
		},
		{
			name: "invalid sparkle url fails",
			content: `source_overrides:
  com.example.sparkle:
    kind: sparkle
    appcast_url: ftp://example.com/appcast.xml
`,
			wantMessage: "sparkle appcast_url must start with http:// or https://",
		},
		{
			name: "empty brew cask fails",
			content: `source_overrides:
  com.example.brew:
    kind: brew
    cask: ""
`,
			wantMessage: "brew cask must be non-empty",
		},
		{
			name: "unexpected field on a kind fails",
			content: `source_overrides:
  com.example.github:
    kind: github
    repo: cli/cli
    appcast_url: https://example.com/appcast.xml
`,
			wantMessage: "unexpected field",
		},
		{
			name: "null source override entry fails",
			content: `source_overrides:
  com.example.null: null
`,
			wantMessage: "source_overrides entry must be a non-null mapping",
		},
		{
			name: "blank source override entry fails",
			content: `source_overrides:
  com.example.blank:
`,
			wantMessage: "source_overrides entry must be a non-null mapping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfigFromYAMLWithError(t, tt.content)
			if err == nil {
				t.Fatal("expected Load to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Load error = %q, want substring %q", err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestMerge_SourceOverrides(t *testing.T) {
	current := &Config{
		SourceOverrides: map[string]*SourceOverrideConfig{
			"com.example.github": {
				Kind: SourceOverrideKindGitHub,
				Repo: "old/repo",
			},
			"com.example.sparkle": {
				Kind:       SourceOverrideKindSparkle,
				AppcastURL: "https://old.example.com/appcast.xml",
			},
		},
	}
	current.buildIgnoredSet()
	current.buildPinnedSet()

	imported := &Config{
		SourceOverrides: map[string]*SourceOverrideConfig{
			"com.example.github": {
				Kind: SourceOverrideKindGitHub,
				Repo: "new/repo",
			},
			"com.example.brew": {
				Kind: SourceOverrideKindBrew,
				Cask: "iterm2",
			},
		},
	}

	result := Merge(current, imported)

	if got := result.SourceOverride("com.example.github"); got == nil {
		t.Fatal("expected github override to exist after merge")
	} else if got.Repo != "new/repo" {
		t.Errorf("Repo = %q, want %q", got.Repo, "new/repo")
	}

	if got := result.SourceOverride("com.example.sparkle"); got == nil {
		t.Fatal("expected sparkle override to be preserved after merge")
	} else if got.AppcastURL != "https://old.example.com/appcast.xml" {
		t.Errorf("AppcastURL = %q, want %q", got.AppcastURL, "https://old.example.com/appcast.xml")
	}

	if got := result.SourceOverride("com.example.brew"); got == nil {
		t.Fatal("expected brew override to be added after merge")
	} else if got.Cask != "iterm2" {
		t.Errorf("Cask = %q, want %q", got.Cask, "iterm2")
	}
}

func loadConfigFromYAML(t *testing.T, content string) *Config {
	t.Helper()

	cfg, err := loadConfigFromYAMLWithError(t, content)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	return cfg
}

func loadConfigFromYAMLWithError(t *testing.T, content string) (*Config, error) {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	return Load(cfgPath)
}
