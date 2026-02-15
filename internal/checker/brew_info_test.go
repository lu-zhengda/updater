package checker

import (
	"context"
	"fmt"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestBrewInfoChecker_CanCheck(t *testing.T) {
	checker := NewBrewInfoChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "app with cask name",
			app:  &app.App{CaskName: "1password"},
			want: true,
		},
		{
			name: "app without cask name",
			app:  &app.App{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checker.CanCheck(tt.app); got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrewInfoChecker_Check(t *testing.T) {
	jsonResp := `{"casks":[{"token":"1password","version":"8.10.60"}]}`
	runner := &MockCmdRunner{Output: []byte(jsonResp)}

	checker := NewBrewInfoChecker(runner)
	a := &app.App{
		Name:     "1Password",
		Version:  "8.10.50",
		CaskName: "1password",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "8.10.60" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "8.10.60")
	}
	if result.Source != "brew-info" {
		t.Errorf("Source = %q, want %q", result.Source, "brew-info")
	}
	if result.CurrentVersion != "8.10.50" {
		t.Errorf("CurrentVersion = %q, want %q", result.CurrentVersion, "8.10.50")
	}
}

func TestBrewInfoChecker_CheckNoUpdate(t *testing.T) {
	jsonResp := `{"casks":[{"token":"1password","version":"8.10.50"}]}`
	runner := &MockCmdRunner{Output: []byte(jsonResp)}

	checker := NewBrewInfoChecker(runner)
	a := &app.App{
		Name:     "1Password",
		Version:  "8.10.50",
		CaskName: "1password",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}

func TestBrewInfoChecker_CompositeVersion(t *testing.T) {
	jsonResp := `{"casks":[{"token":"docker","version":"4.60.1,218372"}]}`
	runner := &MockCmdRunner{Output: []byte(jsonResp)}

	checker := NewBrewInfoChecker(runner)
	a := &app.App{
		Name:     "Docker",
		Version:  "4.59.0",
		CaskName: "docker",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.LatestVersion != "4.60.1" {
		t.Errorf("LatestVersion = %q, want %q (should strip composite build)", result.LatestVersion, "4.60.1")
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
}

func TestBrewInfoChecker_CaskNotFound(t *testing.T) {
	runner := &MockCmdRunner{
		Err: fmt.Errorf("exit status 1"),
	}

	checker := NewBrewInfoChecker(runner)
	a := &app.App{
		Name:     "Unknown App",
		Version:  "1.0.0",
		CaskName: "nonexistent-cask",
	}

	_, err := checker.Check(context.Background(), a)
	if err == nil {
		t.Error("expected error for nonexistent cask")
	}
}

func TestParseBrewInfo(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    string
		wantErr bool
	}{
		{
			name: "simple version",
			data: `{"casks":[{"token":"firefox","version":"134.0"}]}`,
			want: "134.0",
		},
		{
			name: "composite version",
			data: `{"casks":[{"token":"docker","version":"4.60.1,218372"}]}`,
			want: "4.60.1",
		},
		{
			name:    "empty casks array",
			data:    `{"casks":[]}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			data:    `not json`,
			wantErr: true,
		},
		{
			name:    "empty version",
			data:    `{"casks":[{"token":"test","version":""}]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBrewInfo([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Errorf("parseBrewInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseBrewInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaskExists(t *testing.T) {
	t.Run("cask exists", func(t *testing.T) {
		runner := &MockCmdRunner{Output: []byte(`{"casks":[{"token":"firefox"}]}`)}
		if !CaskExists(context.Background(), runner, "firefox") {
			t.Error("expected CaskExists to return true")
		}
	})

	t.Run("cask does not exist", func(t *testing.T) {
		runner := &MockCmdRunner{Err: fmt.Errorf("exit status 1")}
		if CaskExists(context.Background(), runner, "nonexistent") {
			t.Error("expected CaskExists to return false")
		}
	})
}

func TestBrewInfoChecker_Name(t *testing.T) {
	checker := NewBrewInfoChecker(nil)
	if got := checker.Name(); got != "brew-info" {
		t.Errorf("Name() = %q, want %q", got, "brew-info")
	}
}

func TestBrewInfoChecker_CheckEmptyCaskName(t *testing.T) {
	runner := &MockCmdRunner{Output: []byte(`{}`)}
	checker := NewBrewInfoChecker(runner)
	a := &app.App{Name: "NoName", Version: "1.0.0"}

	_, err := checker.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error for empty cask name, got nil")
	}
}
