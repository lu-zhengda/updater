package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestResolveGitHubToken(t *testing.T) {
	t.Run("env takes precedence", func(t *testing.T) {
		cfg := &Config{GitHubToken: "from-config"}
		t.Setenv("GITHUB_TOKEN", "from-env")
		if got := cfg.ResolveGitHubToken(); got != "from-env" {
			t.Errorf("ResolveGitHubToken() = %q, want %q", got, "from-env")
		}
	})

	t.Run("falls back to config", func(t *testing.T) {
		cfg := &Config{GitHubToken: "from-config"}
		t.Setenv("GITHUB_TOKEN", "")
		if got := cfg.ResolveGitHubToken(); got != "from-config" {
			t.Errorf("ResolveGitHubToken() = %q, want %q", got, "from-config")
		}
	})

	t.Run("empty when neither set", func(t *testing.T) {
		cfg := &Config{}
		t.Setenv("GITHUB_TOKEN", "")
		if got := cfg.ResolveGitHubToken(); got != "" {
			t.Errorf("ResolveGitHubToken() = %q, want empty", got)
		}
	})
}

func TestMaxConcurrentOrDefault(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"zero returns default", 0, 10},
		{"negative returns default", -5, 10},
		{"positive returns value", 20, 20},
		{"one returns one", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{MaxConcurrent: tt.value}
			if got := cfg.MaxConcurrentOrDefault(); got != tt.want {
				t.Errorf("MaxConcurrentOrDefault() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMaxBackupsOrDefault(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"zero returns default", 0, 1},
		{"negative returns default", -1, 1},
		{"positive returns value", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{MaxBackups: tt.value}
			if got := cfg.MaxBackupsOrDefault(); got != tt.want {
				t.Errorf("MaxBackupsOrDefault() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPinUnpin(t *testing.T) {
	cfg := defaultConfig()

	// Pin an app.
	cfg.Pin("com.example.app")
	if !cfg.IsPinned("com.example.app") {
		t.Error("expected com.example.app to be pinned")
	}
	if len(cfg.PinnedApps) != 1 {
		t.Errorf("expected 1 pinned app, got %d", len(cfg.PinnedApps))
	}

	// Pin same app again (idempotent).
	cfg.Pin("com.example.app")
	if len(cfg.PinnedApps) != 1 {
		t.Errorf("duplicate pin: expected 1 pinned app, got %d", len(cfg.PinnedApps))
	}

	// Unpin.
	cfg.Unpin("com.example.app")
	if cfg.IsPinned("com.example.app") {
		t.Error("expected com.example.app to not be pinned after unpin")
	}
	if len(cfg.PinnedApps) != 0 {
		t.Errorf("expected 0 pinned apps, got %d", len(cfg.PinnedApps))
	}

	// Unpin non-existent (no-op).
	cfg.Unpin("com.example.nonexistent")
}

func TestConfigSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "sub", "config.yaml")

	cfg := defaultConfig()
	cfg.GitHubToken = "test-token"
	cfg.MaxConcurrent = 5
	cfg.MaxBackups = 3
	cfg.Pin("com.example.pinned")
	cfg.IgnoredApps = []string{"com.example.ignored"}
	cfg.buildIgnoredSet()

	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.GitHubToken != "test-token" {
		t.Errorf("GitHubToken = %q, want %q", loaded.GitHubToken, "test-token")
	}
	if loaded.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", loaded.MaxConcurrent)
	}
	if loaded.MaxBackups != 3 {
		t.Errorf("MaxBackups = %d, want 3", loaded.MaxBackups)
	}
	if !loaded.IsPinned("com.example.pinned") {
		t.Error("expected com.example.pinned to be pinned after reload")
	}
	if !loaded.IsIgnored("com.example.ignored") {
		t.Error("expected com.example.ignored to be ignored after reload")
	}
}

func TestScheduleIntervalOrDefault(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"zero returns default", 0, 24},
		{"negative returns default", -1, 24},
		{"positive returns value", 12, 12},
		{"48 returns 48", 48, 48},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ScheduleInterval: tt.value}
			if got := cfg.ScheduleIntervalOrDefault(); got != tt.want {
				t.Errorf("ScheduleIntervalOrDefault() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestScheduleFieldsPersist(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := defaultConfig()
	cfg.ScheduleOffered = true
	cfg.ScheduleInterval = 12

	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !loaded.ScheduleOffered {
		t.Error("expected ScheduleOffered to be true after reload")
	}
	if loaded.ScheduleInterval != 12 {
		t.Errorf("ScheduleInterval = %d, want 12", loaded.ScheduleInterval)
	}
}

func TestLastCheckedPersists(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	now := time.Now().Truncate(time.Second)
	cfg := defaultConfig()
	cfg.LastChecked = now

	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !loaded.LastChecked.Equal(now) {
		t.Errorf("LastChecked = %v, want %v", loaded.LastChecked, now)
	}
}

func TestLastCheckedOmittedWhenZero(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := defaultConfig()
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if strings.Contains(string(data), "last_checked") {
		t.Error("expected last_checked to be omitted when zero")
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

func TestMerge_EmptyImport(t *testing.T) {
	current := &Config{
		IgnoredApps:    []string{"com.example.a"},
		GitHubMappings: map[string]string{"com.x": "x/repo"},
		GitHubToken:    "token-1",
		MaxConcurrent:  5,
	}
	current.buildIgnoredSet()
	current.buildPinnedSet()

	imported := &Config{}

	result := Merge(current, imported)
	if len(result.IgnoredApps) != 1 || result.IgnoredApps[0] != "com.example.a" {
		t.Errorf("IgnoredApps = %v, want [com.example.a]", result.IgnoredApps)
	}
	if result.GitHubToken != "token-1" {
		t.Errorf("GitHubToken = %q, want %q", result.GitHubToken, "token-1")
	}
	if result.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", result.MaxConcurrent)
	}
}

func TestMerge_ListsUnion(t *testing.T) {
	current := &Config{
		IgnoredApps: []string{"com.a", "com.b"},
		PinnedApps:  []string{"com.x"},
	}
	current.buildIgnoredSet()
	current.buildPinnedSet()

	imported := &Config{
		IgnoredApps: []string{"com.b", "com.c"}, // com.b is duplicate
		PinnedApps:  []string{"com.y"},
	}

	result := Merge(current, imported)

	if len(result.IgnoredApps) != 3 {
		t.Fatalf("IgnoredApps length = %d, want 3", len(result.IgnoredApps))
	}
	// Expect: com.a, com.b, com.c (union, deduplicated)
	want := map[string]bool{"com.a": true, "com.b": true, "com.c": true}
	for _, id := range result.IgnoredApps {
		if !want[id] {
			t.Errorf("unexpected ignored app: %q", id)
		}
	}

	if len(result.PinnedApps) != 2 {
		t.Fatalf("PinnedApps length = %d, want 2", len(result.PinnedApps))
	}
	if !result.IsPinned("com.x") || !result.IsPinned("com.y") {
		t.Error("expected both com.x and com.y to be pinned")
	}
}

func TestMerge_MapsOverride(t *testing.T) {
	current := &Config{
		GitHubMappings: map[string]string{
			"com.a": "old/repo",
			"com.b": "keep/this",
		},
		CaskMappings: map[string]string{
			"com.x": "cask-x",
		},
	}
	current.buildIgnoredSet()
	current.buildPinnedSet()

	imported := &Config{
		GitHubMappings: map[string]string{
			"com.a": "new/repo", // override
			"com.c": "add/new", // new entry
		},
		CaskMappings: map[string]string{
			"com.y": "cask-y", // new entry
		},
	}

	result := Merge(current, imported)

	if got := result.GitHubRepo("com.a"); got != "new/repo" {
		t.Errorf("GitHubRepo(com.a) = %q, want %q", got, "new/repo")
	}
	if got := result.GitHubRepo("com.b"); got != "keep/this" {
		t.Errorf("GitHubRepo(com.b) = %q, want %q", got, "keep/this")
	}
	if got := result.GitHubRepo("com.c"); got != "add/new" {
		t.Errorf("GitHubRepo(com.c) = %q, want %q", got, "add/new")
	}
	if got := result.CaskToken("com.x"); got != "cask-x" {
		t.Errorf("CaskToken(com.x) = %q, want %q", got, "cask-x")
	}
	if got := result.CaskToken("com.y"); got != "cask-y" {
		t.Errorf("CaskToken(com.y) = %q, want %q", got, "cask-y")
	}
}

func TestMerge_ScalarOverride(t *testing.T) {
	current := &Config{
		GitHubToken:      "old-token",
		MaxConcurrent:    5,
		MaxBackups:       2,
		ScheduleInterval: 24,
	}
	current.buildIgnoredSet()
	current.buildPinnedSet()

	imported := &Config{
		GitHubToken:      "new-token",
		MaxConcurrent:    20,
		ScheduleInterval: 0, // zero → should NOT override
	}

	result := Merge(current, imported)

	if result.GitHubToken != "new-token" {
		t.Errorf("GitHubToken = %q, want %q", result.GitHubToken, "new-token")
	}
	if result.MaxConcurrent != 20 {
		t.Errorf("MaxConcurrent = %d, want 20", result.MaxConcurrent)
	}
	if result.MaxBackups != 2 {
		t.Errorf("MaxBackups = %d, want 2 (preserved from current)", result.MaxBackups)
	}
	if result.ScheduleInterval != 24 {
		t.Errorf("ScheduleInterval = %d, want 24 (zero import should not override)", result.ScheduleInterval)
	}
}
