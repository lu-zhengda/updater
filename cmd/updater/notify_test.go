package main

import (
	"context"
	"strings"
	"testing"

	"github.com/luzhengda/updater/internal/checker"
)

func TestBuildNotificationBody(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{
			name:  "single app",
			names: []string{"Firefox"},
			want:  "Firefox",
		},
		{
			name:  "multiple apps",
			names: []string{"Firefox", "Chrome", "Safari"},
			want:  "Firefox, Chrome, Safari",
		},
		{
			name:  "truncation",
			names: generateLongNames(50),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNotificationBody(tt.names)
			if tt.name == "truncation" {
				if len(got) > 200 {
					t.Errorf("body length %d exceeds 200", len(got))
				}
				if !strings.HasSuffix(got, "...") {
					t.Error("expected truncated body to end with ...")
				}
			} else if got != tt.want {
				t.Errorf("buildNotificationBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendNotification(t *testing.T) {
	var gotArgs string
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{},
	}

	// Override to capture the osascript args.
	captureRunner := &captureNotifyRunner{}

	err := sendNotification(context.Background(), captureRunner, 3, "Firefox, Chrome, Safari")
	if err != nil {
		t.Fatalf("sendNotification failed: %v", err)
	}
	_ = gotArgs
	_ = runner

	// Verify the osascript was called.
	if captureRunner.name != "osascript" {
		t.Errorf("expected osascript, got %s", captureRunner.name)
	}
	if len(captureRunner.args) < 2 {
		t.Fatal("expected at least 2 args")
	}
	script := captureRunner.args[1]
	if !strings.Contains(script, "display notification") {
		t.Error("expected 'display notification' in script")
	}
	if !strings.Contains(script, "3 app update(s) available") {
		t.Errorf("expected title in script, got: %s", script)
	}
}

func TestEscapeAppleScript(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello`, `hello`},
		{`say "hi"`, `say \"hi\"`},
		{`path\to\file`, `path\\to\\file`},
	}
	for _, tt := range tests {
		got := escapeAppleScript(tt.input)
		if got != tt.want {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func generateLongNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = "ApplicationWithALongName"
	}
	return names
}

type captureNotifyRunner struct {
	name string
	args []string
}

func (r *captureNotifyRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = args
	return nil, nil
}
