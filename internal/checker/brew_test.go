package checker

import (
	"context"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestBrewChecker_ParseOutdated(t *testing.T) {
	jsonData := []byte(`[{"name":"visual-studio-code","installed_versions":"1.90.0","current_version":"1.95.0"}]`)

	items, err := parseBrewOutdated(jsonData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Name != "visual-studio-code" {
		t.Errorf("expected name visual-studio-code, got %s", item.Name)
	}
	if item.InstalledVersions != "1.90.0" {
		t.Errorf("expected installed version 1.90.0, got %s", item.InstalledVersions)
	}
	if item.CurrentVersion != "1.95.0" {
		t.Errorf("expected current version 1.95.0, got %s", item.CurrentVersion)
	}
}

func TestBrewChecker_CanCheck(t *testing.T) {
	checker := NewBrewChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "brew app with cask name",
			app:  &app.App{Source: app.SourceBrew, CaskName: "visual-studio-code"},
			want: true,
		},
		{
			name: "brew source without cask name",
			app:  &app.App{Source: app.SourceBrew},
			want: false,
		},
		{
			name: "non-brew app with cask name",
			app:  &app.App{Source: app.SourceSparkle, CaskName: "some-app"},
			want: true,
		},
		{
			name: "MAS app without cask name",
			app:  &app.App{Source: app.SourceMAS},
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
		Name:     "Visual Studio Code",
		Version:  "1.90.0",
		Source:   app.SourceBrew,
		CaskName: "visual-studio-code",
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
		Name:     "Visual Studio Code",
		Version:  "1.95.0",
		Source:   app.SourceBrew,
		CaskName: "visual-studio-code",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
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
