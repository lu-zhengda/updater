package checker

import (
	"context"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestSystemChecker_CanCheck(t *testing.T) {
	c := NewSystemChecker(nil)

	tests := []struct {
		name     string
		bundleID string
		want     bool
	}{
		{"macOS system app", "com.apple.macOS", true},
		{"regular app", "com.example.app", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &app.App{BundleID: tt.bundleID}
			if got := c.CanCheck(a); got != tt.want {
				t.Errorf("CanCheck() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSystemChecker_Check_UpdateAvailable(t *testing.T) {
	output := `Software Update found the following new or updated software:
* Label: macOS Sequoia 15.3.1
	Title: macOS Sequoia 15.3.1, Version: 15.3.1, Size: 1234567K, Recommended: YES, Action: restart,
`
	runner := &MockCmdRunner{Output: []byte(output)}
	c := NewSystemChecker(runner)

	a := &app.App{
		Name:     "macOS",
		BundleID: "com.apple.macOS",
		Version:  "15.2",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "15.3.1" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "15.3.1")
	}
	if result.Source != "system" {
		t.Errorf("Source = %q, want %q", result.Source, "system")
	}
}

func TestSystemChecker_Check_NoUpdate(t *testing.T) {
	output := `Software Update Tool
Finding available software
No new software available.
`
	runner := &MockCmdRunner{Output: []byte(output)}
	c := NewSystemChecker(runner)

	a := &app.App{
		Name:     "macOS",
		BundleID: "com.apple.macOS",
		Version:  "15.3.1",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}

func TestSystemChecker_Check_AlternativeFormat(t *testing.T) {
	output := `Software Update found the following new or updated software:
* macOS Ventura 13.6.4
`
	runner := &MockCmdRunner{Output: []byte(output)}
	c := NewSystemChecker(runner)

	a := &app.App{
		Name:     "macOS",
		BundleID: "com.apple.macOS",
		Version:  "13.5",
	}

	result, err := c.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "13.6.4" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "13.6.4")
	}
}

func TestParseSystemUpdates(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   *systemUpdate
	}{
		{
			name: "label format",
			output: `Software Update found the following new or updated software:
* Label: macOS Sequoia 15.3.1
	Title: macOS Sequoia 15.3.1, Version: 15.3.1, Size: 1234K`,
			want: &systemUpdate{label: "macOS Sequoia 15.3.1", version: "15.3.1"},
		},
		{
			name:   "star format",
			output: "* macOS Ventura 13.6.4",
			want:   &systemUpdate{label: "macOS Ventura 13.6.4", version: "13.6.4"},
		},
		{
			name:   "no update",
			output: "No new software available.",
			want:   nil,
		},
		{
			name:   "empty",
			output: "",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSystemUpdates(tt.output)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			if got.version != tt.want.version {
				t.Errorf("version = %q, want %q", got.version, tt.want.version)
			}
		})
	}
}

func TestExtractVersionFromLabel(t *testing.T) {
	tests := []struct {
		label string
		want  string
	}{
		{"macOS Sequoia 15.3.1", "15.3.1"},
		{"macOS Ventura 13.6.4", "13.6.4"},
		{"macOS", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := extractVersionFromLabel(tt.label)
			if got != tt.want {
				t.Errorf("extractVersionFromLabel(%q) = %q, want %q", tt.label, got, tt.want)
			}
		})
	}
}

func TestExtractVersionFromDetail(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "standard format",
			line: "	Title: macOS Sequoia 15.3.1, Version: 15.3.1, Size: 1234K, Recommended: YES",
			want: "15.3.1",
		},
		{
			name: "no version",
			line: "	Title: Something, Size: 1234K",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVersionFromDetail(tt.line)
			if got != tt.want {
				t.Errorf("extractVersionFromDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}
