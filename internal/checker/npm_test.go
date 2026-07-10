package checker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestNpmChecker_Name(t *testing.T) {
	c := NewNpmChecker(nil)
	if got := c.Name(); got != "npm" {
		t.Errorf("Name() = %q, want %q", got, "npm")
	}
}

func TestNpmChecker_CanCheck(t *testing.T) {
	c := NewNpmChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "npm package",
			app:  &app.App{Source: app.SourceNpm, NpmPackage: "typescript"},
			want: true,
		},
		{
			name: "npm source without package name",
			app:  &app.App{Source: app.SourceNpm},
			want: false,
		},
		{
			name: "brew formula",
			app:  &app.App{Source: app.SourceBrewFormula, FormulaName: "node"},
			want: false,
		},
		{
			name: "unknown source with npm package name",
			app:  &app.App{Source: app.SourceUnknown, NpmPackage: "eslint"},
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

func TestNpmChecker_Check(t *testing.T) {
	tests := []struct {
		name       string
		app        *app.App
		output     []byte
		err        error
		wantUpdate bool
		wantLatest string
		wantErr    bool
	}{
		{
			name: "outdated package found",
			app: &app.App{
				Name:       "typescript",
				Version:    "5.0.0",
				Source:     app.SourceNpm,
				NpmPackage: "typescript",
			},
			output:     []byte(`{"typescript":{"current":"5.0.0","wanted":"5.7.3","latest":"5.7.3"}}`),
			wantUpdate: true,
			wantLatest: "5.7.3",
		},
		{
			name: "package not in outdated list",
			app: &app.App{
				Name:       "eslint",
				Version:    "9.0.0",
				Source:     app.SourceNpm,
				NpmPackage: "eslint",
			},
			output:     []byte(`{"typescript":{"current":"5.0.0","wanted":"5.7.3","latest":"5.7.3"}}`),
			wantUpdate: false,
			wantLatest: "9.0.0",
		},
		{
			name: "empty JSON object",
			app: &app.App{
				Name:       "prettier",
				Version:    "3.0.0",
				Source:     app.SourceNpm,
				NpmPackage: "prettier",
			},
			output:     []byte(`{}`),
			wantUpdate: false,
			wantLatest: "3.0.0",
		},
		{
			name:    "missing package name",
			app:     &app.App{Name: "mystery", Source: app.SourceNpm},
			wantErr: true,
		},
		{
			name: "npm outdated exits 1 with valid JSON",
			app: &app.App{
				Name:       "typescript",
				Version:    "5.0.0",
				Source:     app.SourceNpm,
				NpmPackage: "typescript",
			},
			output:     []byte(`{"typescript":{"current":"5.0.0","wanted":"5.7.3","latest":"5.7.3"}}`),
			err:        fmt.Errorf("exit status 1"),
			wantUpdate: true,
			wantLatest: "5.7.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &MockCmdRunner{Output: tt.output, Err: tt.err}
			c := NewNpmChecker(runner)

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
			if result.Source != "npm" {
				t.Errorf("Source = %q, want %q", result.Source, "npm")
			}
		})
	}
}

func TestNpmChecker_FallbackToNpmView(t *testing.T) {
	// When npm outdated returns an error JSON, the checker should fall back
	// to npm view <pkg> version for individual packages.
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"npm outdated -g --json": {
				Output: []byte(`{"error":{"code":"ENOVERSIONS","summary":"No versions available for foo","detail":""}}`),
				Err:    fmt.Errorf("exit status 1"),
			},
			"npm view typescript version --json": {
				Output: []byte(`"5.7.3"`),
			},
		},
	}
	c := NewNpmChecker(runner)

	a := &app.App{
		Name:       "typescript",
		Version:    "5.0.0",
		Source:     app.SourceNpm,
		NpmPackage: "typescript",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUpdate {
		t.Error("HasUpdate = false, want true")
	}
	if result.LatestVersion != "5.7.3" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "5.7.3")
	}
}

func TestNpmChecker_FallbackToNpmViewArray(t *testing.T) {
	// npm 12 may wrap the version in an array, even for a single package.
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"npm outdated -g --json": {
				Output: []byte(`not json`),
			},
			"npm view @scope/tool version --json": {
				Output: []byte("[\n  \"2.4.1\"\n]\n"),
			},
		},
	}
	c := NewNpmChecker(runner)
	a := &app.App{
		Name:       "@scope/tool",
		Version:    "2.3.0",
		Source:     app.SourceNpm,
		NpmPackage: "@scope/tool",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LatestVersion != "2.4.1" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "2.4.1")
	}
	if strings.ContainsAny(result.LatestVersion, "\r\n") {
		t.Errorf("LatestVersion %q contains a line break", result.LatestVersion)
	}
}

func TestNpmChecker_FallbackNoOutput(t *testing.T) {
	// When npm outdated returns no output at all, fall back to npm view.
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"npm outdated -g --json": {
				Err: fmt.Errorf("exit status 1"),
			},
			"npm view eslint version --json": {
				Output: []byte(`"9.17.0"`),
			},
		},
	}
	c := NewNpmChecker(runner)

	a := &app.App{
		Name:       "eslint",
		Version:    "9.0.0",
		Source:     app.SourceNpm,
		NpmPackage: "eslint",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUpdate {
		t.Error("HasUpdate = false, want true")
	}
	if result.LatestVersion != "9.17.0" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "9.17.0")
	}
}

func TestNpmChecker_FallbackViewError(t *testing.T) {
	// When both npm outdated and npm view fail, return error.
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"npm outdated -g --json": {
				Output: []byte(`{"error":{"code":"ENOVERSIONS","summary":"No versions available","detail":""}}`),
				Err:    fmt.Errorf("exit status 1"),
			},
			"npm view private-pkg version --json": {
				Err: fmt.Errorf("exit status 1"),
			},
		},
	}
	c := NewNpmChecker(runner)

	a := &app.App{
		Name:       "private-pkg",
		Version:    "1.0.0",
		Source:     app.SourceNpm,
		NpmPackage: "private-pkg",
	}

	_, err := c.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNpmChecker_LoadOutdatedJSONParseError(t *testing.T) {
	// When npm outdated returns invalid JSON, fall back to npm view.
	runner := &MultiMockCmdRunner{
		Responses: map[string]MockResponse{
			"npm outdated -g --json": {
				Output: []byte(`not json`),
			},
			"npm view typescript version --json": {
				Output: []byte(`"5.7.3"`),
			},
		},
	}
	c := NewNpmChecker(runner)

	a := &app.App{
		Name:       "typescript",
		Version:    "5.0.0",
		Source:     app.SourceNpm,
		NpmPackage: "typescript",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUpdate {
		t.Error("HasUpdate = false, want true")
	}
	if result.LatestVersion != "5.7.3" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "5.7.3")
	}
}

func TestListInstalledNpmPackages(t *testing.T) {
	tests := []struct {
		name    string
		output  []byte
		err     error
		want    map[string]string
		wantErr bool
	}{
		{
			name: "normal output",
			output: []byte(`{
				"dependencies": {
					"typescript": {"version": "5.7.3"},
					"eslint": {"version": "9.17.0"},
					"prettier": {"version": "3.4.2"}
				}
			}`),
			want: map[string]string{
				"typescript": "5.7.3",
				"eslint":     "9.17.0",
				"prettier":   "3.4.2",
			},
		},
		{
			name:   "empty dependencies",
			output: []byte(`{"dependencies": {}}`),
			want:   map[string]string{},
		},
		{
			name:   "no dependencies key",
			output: []byte(`{}`),
			want:   map[string]string{},
		},
		{
			name:    "npm error",
			err:     fmt.Errorf("npm not found"),
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			output:  []byte(`not json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &MockCmdRunner{Output: tt.output, Err: tt.err}
			got, err := ListInstalledNpmPackages(context.Background(), runner)
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
				t.Fatalf("got %d packages, want %d", len(got), len(tt.want))
			}
			for name, wantVer := range tt.want {
				if gotVer, ok := got[name]; !ok {
					t.Errorf("missing package %q", name)
				} else if gotVer != wantVer {
					t.Errorf("package %q version = %q, want %q", name, gotVer, wantVer)
				}
			}
		})
	}
}
