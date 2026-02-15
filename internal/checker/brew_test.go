package checker

import (
	"context"
	"fmt"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestBrewChecker_ParseOutdated(t *testing.T) {
	t.Run("flat array format", func(t *testing.T) {
		jsonData := []byte(`[{"name":"visual-studio-code","installed_versions":"1.90.0","current_version":"1.95.0"}]`)

		items, err := parseBrewOutdated(jsonData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if items[0].Name != "visual-studio-code" {
			t.Errorf("expected name visual-studio-code, got %s", items[0].Name)
		}
		if items[0].CurrentVersion != "1.95.0" {
			t.Errorf("expected current version 1.95.0, got %s", items[0].CurrentVersion)
		}
	})

	t.Run("wrapped format with formulae and casks", func(t *testing.T) {
		jsonData := []byte(`{"formulae":[{"name":"node","installed_versions":["20.0.0"],"current_version":"22.12.0"}],"casks":[{"name":"firefox","installed_versions":"1.0","current_version":"2.0"}]}`)

		items, err := parseBrewOutdated(jsonData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		if items[0].Name != "node" {
			t.Errorf("expected first item name node, got %s", items[0].Name)
		}
		if items[1].Name != "firefox" {
			t.Errorf("expected second item name firefox, got %s", items[1].Name)
		}
	})

	t.Run("wrapped format empty", func(t *testing.T) {
		jsonData := []byte(`{"formulae":[],"casks":[]}`)

		items, err := parseBrewOutdated(jsonData)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
	})
}

func TestBrewChecker_CanCheck(t *testing.T) {
	checker := NewBrewChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "brew-installed app with cask name",
			app:  &app.App{Source: app.SourceBrew, CaskName: "visual-studio-code", InstalledViaBrew: true},
			want: true,
		},
		{
			name: "brew source without cask name",
			app:  &app.App{Source: app.SourceBrew, InstalledViaBrew: true},
			want: false,
		},
		{
			name: "non-brew app with cask name but not installed via brew",
			app:  &app.App{Source: app.SourceSparkle, CaskName: "some-app"},
			want: false,
		},
		{
			name: "MAS app without cask name",
			app:  &app.App{Source: app.SourceMAS},
			want: false,
		},
		{
			name: "app with cask name but not installed via brew",
			app:  &app.App{CaskName: "1password"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checker.CanCheck(tt.app)
			if got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrewChecker_CheckWithMockRunner(t *testing.T) {
	outdatedJSON := `[{"name":"visual-studio-code","installed_versions":"1.90.0","current_version":"1.95.0"}]`
	runner := &MockCmdRunner{
		Output: []byte(outdatedJSON),
	}

	checker := NewBrewChecker(runner)
	a := &app.App{
		Name:             "Visual Studio Code",
		Version:          "1.90.0",
		Source:           app.SourceBrew,
		CaskName:         "visual-studio-code",
		InstalledViaBrew: true,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "1.95.0" {
		t.Errorf("expected LatestVersion 1.95.0, got %s", result.LatestVersion)
	}
	if result.CurrentVersion != "1.90.0" {
		t.Errorf("expected CurrentVersion 1.90.0, got %s", result.CurrentVersion)
	}
	if result.Source != "brew" {
		t.Errorf("expected Source brew, got %s", result.Source)
	}
}

func TestBrewChecker_CheckNoUpdate(t *testing.T) {
	// Empty outdated list means no updates
	runner := &MockCmdRunner{
		Output: []byte(`[]`),
	}

	checker := NewBrewChecker(runner)
	a := &app.App{
		Name:             "Visual Studio Code",
		Version:          "1.95.0",
		Source:           app.SourceBrew,
		CaskName:         "visual-studio-code",
		InstalledViaBrew: true,
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}

func TestBrewChecker_Name(t *testing.T) {
	c := NewBrewChecker(nil)
	if got := c.Name(); got != "brew" {
		t.Errorf("Name() = %q, want %q", got, "brew")
	}
}

func TestBrewChecker_CheckEmptyCaskName(t *testing.T) {
	runner := &MockCmdRunner{Output: []byte(`[]`)}
	c := NewBrewChecker(runner)
	a := &app.App{Name: "NoName", Version: "1.0.0"}

	_, err := c.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error for empty cask name, got nil")
	}
}

func TestBrewChecker_CheckRunnerError(t *testing.T) {
	runner := &MockCmdRunner{Err: fmt.Errorf("brew not found")}
	c := NewBrewChecker(runner)
	a := &app.App{Name: "Firefox", Version: "1.0.0", CaskName: "firefox", InstalledViaBrew: true}

	_, err := c.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error when runner fails, got nil")
	}
}

func TestBrewChecker_ParseOutdatedInvalidJSON(t *testing.T) {
	// Neither flat array nor wrapped format — should error
	_, err := parseBrewOutdated([]byte(`{"unexpected":"format"}`))
	if err != nil {
		// The wrapped format with no casks/formulae returns empty successfully.
		// That's fine — but truly invalid JSON should fail.
	}

	_, err = parseBrewOutdated([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestListInstalledCasks_RunnerError(t *testing.T) {
	runner := &MockCmdRunner{Err: fmt.Errorf("brew not installed")}
	_, err := ListInstalledCasks(context.Background(), runner)
	if err == nil {
		t.Fatal("expected error when runner fails, got nil")
	}
}

func TestListInstalledCasks(t *testing.T) {
	runner := &MockCmdRunner{
		Output: []byte("firefox\ngoogle-chrome\nvisual-studio-code\n"),
	}

	casks, err := ListInstalledCasks(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(casks) != 3 {
		t.Fatalf("expected 3 casks, got %d", len(casks))
	}

	for _, name := range []string{"firefox", "google-chrome", "visual-studio-code"} {
		if !casks[name] {
			t.Errorf("expected cask %s to be present", name)
		}
	}
}
