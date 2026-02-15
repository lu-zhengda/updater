package checker

import (
	"context"
	"fmt"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestBrewFormulaChecker_Name(t *testing.T) {
	c := NewBrewFormulaChecker(nil)
	if got := c.Name(); got != "formula" {
		t.Errorf("Name() = %q, want %q", got, "formula")
	}
}

func TestBrewFormulaChecker_LoadOutdatedJSONParseError(t *testing.T) {
	runner := &MockCmdRunner{Output: []byte(`not json`)}
	c := NewBrewFormulaChecker(runner)

	a := &app.App{
		Name:        "node",
		Version:     "20.0.0",
		Source:      app.SourceBrewFormula,
		FormulaName: "node",
	}

	_, err := c.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error for invalid JSON from loadOutdated, got nil")
	}
}

func TestBrewFormulaChecker_CanCheck(t *testing.T) {
	c := NewBrewFormulaChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "formula app",
			app:  &app.App{Source: app.SourceBrewFormula, FormulaName: "node"},
			want: true,
		},
		{
			name: "formula source without formula name",
			app:  &app.App{Source: app.SourceBrewFormula},
			want: false,
		},
		{
			name: "brew cask app",
			app:  &app.App{Source: app.SourceBrew, CaskName: "firefox", InstalledViaBrew: true},
			want: false,
		},
		{
			name: "unknown source with formula name",
			app:  &app.App{Source: app.SourceUnknown, FormulaName: "git"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.CanCheck(tt.app)
			if got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrewFormulaChecker_Check(t *testing.T) {
	tests := []struct {
		name          string
		app           *app.App
		output        []byte
		err           error
		wantUpdate    bool
		wantLatest    string
		wantErr       bool
	}{
		{
			name: "outdated formula found",
			app: &app.App{
				Name:        "node",
				Version:     "20.0.0",
				Source:      app.SourceBrewFormula,
				FormulaName: "node",
			},
			output:     []byte(`[{"name":"node","installed_versions":"20.0.0","current_version":"22.12.0"}]`),
			wantUpdate: true,
			wantLatest: "22.12.0",
		},
		{
			name: "formula not in outdated list",
			app: &app.App{
				Name:        "git",
				Version:     "2.47.1",
				Source:      app.SourceBrewFormula,
				FormulaName: "git",
			},
			output:     []byte(`[{"name":"node","installed_versions":"20.0.0","current_version":"22.12.0"}]`),
			wantUpdate: false,
			wantLatest: "2.47.1",
		},
		{
			name: "empty JSON array",
			app: &app.App{
				Name:        "ffmpeg",
				Version:     "7.0",
				Source:      app.SourceBrewFormula,
				FormulaName: "ffmpeg",
			},
			output:     []byte(`[]`),
			wantUpdate: false,
			wantLatest: "7.0",
		},
		{
			name:    "missing formula name",
			app:     &app.App{Name: "mystery", Source: app.SourceBrewFormula},
			wantErr: true,
		},
		{
			name: "brew command error",
			app: &app.App{
				Name:        "node",
				Version:     "20.0.0",
				Source:      app.SourceBrewFormula,
				FormulaName: "node",
			},
			err:     fmt.Errorf("brew not found"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &MockCmdRunner{Output: tt.output, Err: tt.err}
			c := NewBrewFormulaChecker(runner)

			result, err := c.Check(context.Background(), tt.app)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.HasUpdate != tt.wantUpdate {
				t.Errorf("HasUpdate = %v, want %v", result.HasUpdate, tt.wantUpdate)
			}
			if result.LatestVersion != tt.wantLatest {
				t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, tt.wantLatest)
			}
			if result.Source != "formula" {
				t.Errorf("Source = %q, want %q", result.Source, "formula")
			}
		})
	}
}

func TestListInstalledFormulae(t *testing.T) {
	tests := []struct {
		name    string
		output  []byte
		err     error
		want    map[string]string
		wantErr bool
	}{
		{
			name:   "normal output",
			output: []byte("node 22.12.0\ngit 2.47.1\nffmpeg 7.1\n"),
			want: map[string]string{
				"node":   "22.12.0",
				"git":    "2.47.1",
				"ffmpeg": "7.1",
			},
		},
		{
			name:   "versioned formula name",
			output: []byte("python@3.12 3.12.8\nnode 22.12.0\n"),
			want: map[string]string{
				"python@3.12": "3.12.8",
				"node":        "22.12.0",
			},
		},
		{
			name:   "multiple versions listed picks last",
			output: []byte("node 20.0.0 22.12.0\n"),
			want: map[string]string{
				"node": "22.12.0",
			},
		},
		{
			name:   "malformed line with no version",
			output: []byte("node\ngit 2.47.1\n"),
			want: map[string]string{
				"git": "2.47.1",
			},
		},
		{
			name:   "empty output",
			output: []byte(""),
			want:   map[string]string{},
		},
		{
			name:    "brew error",
			err:     fmt.Errorf("brew not found"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &MockCmdRunner{Output: tt.output, Err: tt.err}
			got, err := ListInstalledFormulae(context.Background(), runner)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d formulae, want %d", len(got), len(tt.want))
			}
			for name, wantVer := range tt.want {
				if gotVer, ok := got[name]; !ok {
					t.Errorf("missing formula %q", name)
				} else if gotVer != wantVer {
					t.Errorf("formula %q version = %q, want %q", name, gotVer, wantVer)
				}
			}
		})
	}
}
