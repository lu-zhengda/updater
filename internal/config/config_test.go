package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	content := `ignored_apps:
  - com.example.ignored
  - com.example.also-ignored
github_mappings:
  com.microsoft.VSCode: "microsoft/vscode"
  com.example.app: "example/app"
cask_mappings:
  com.docker.docker: "docker"
  com.1password.1password: "1password"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify IgnoredApps
	if len(cfg.IgnoredApps) != 2 {
		t.Fatalf("expected 2 ignored apps, got %d", len(cfg.IgnoredApps))
	}

	// Verify IsIgnored
	if !cfg.IsIgnored("com.example.ignored") {
		t.Error("expected com.example.ignored to be ignored")
	}
	if !cfg.IsIgnored("com.example.also-ignored") {
		t.Error("expected com.example.also-ignored to be ignored")
	}
	if cfg.IsIgnored("com.example.not-ignored") {
		t.Error("expected com.example.not-ignored to not be ignored")
	}

	// Verify GitHubMappings
	if len(cfg.GitHubMappings) != 2 {
		t.Fatalf("expected 2 github mappings, got %d", len(cfg.GitHubMappings))
	}

	// Verify GitHubRepo
	if got := cfg.GitHubRepo("com.microsoft.VSCode"); got != "microsoft/vscode" {
		t.Errorf("GitHubRepo(com.microsoft.VSCode) = %q, want %q", got, "microsoft/vscode")
	}
	if got := cfg.GitHubRepo("com.example.app"); got != "example/app" {
		t.Errorf("GitHubRepo(com.example.app) = %q, want %q", got, "example/app")
	}
	if got := cfg.GitHubRepo("com.unknown.app"); got != "" {
		t.Errorf("GitHubRepo(com.unknown.app) = %q, want empty", got)
	}

	// Verify CaskMappings
	if len(cfg.CaskMappings) != 2 {
		t.Fatalf("expected 2 cask mappings, got %d", len(cfg.CaskMappings))
	}

	// Verify CaskToken
	if got := cfg.CaskToken("com.docker.docker"); got != "docker" {
		t.Errorf("CaskToken(com.docker.docker) = %q, want %q", got, "docker")
	}
	if got := cfg.CaskToken("com.1password.1password"); got != "1password" {
		t.Errorf("CaskToken(com.1password.1password) = %q, want %q", got, "1password")
	}
	if got := cfg.CaskToken("com.unknown.app"); got != "" {
		t.Errorf("CaskToken(com.unknown.app) = %q, want empty", got)
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load should not error for missing file, got: %v", err)
	}

	// Should return a usable default config
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.IsIgnored("com.example.anything") {
		t.Error("default config should not ignore any app")
	}
	if got := cfg.GitHubRepo("com.example.anything"); got != "" {
		t.Errorf("default config GitHubRepo should be empty, got %q", got)
	}
	if got := cfg.CaskToken("com.example.anything"); got != "" {
		t.Errorf("default config CaskToken should be empty, got %q", got)
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.IsIgnored("com.example.anything") {
		t.Error("empty config should not ignore any app")
	}
	if got := cfg.GitHubRepo("com.example.anything"); got != "" {
		t.Errorf("empty config GitHubRepo should be empty, got %q", got)
	}
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if p == "" {
		t.Fatal("DefaultPath() returned empty string")
	}
	if filepath.Base(p) != "config.yaml" {
		t.Errorf("DefaultPath() should end with config.yaml, got %s", filepath.Base(p))
	}
}
