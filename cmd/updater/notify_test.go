package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"gopkg.in/yaml.v3"
)

func TestBuildNotificationBody(t *testing.T) {
	tests := []struct {
		name    string
		results []*checker.UpdateResult
		want    string
	}{
		{
			name: "single app with versions",
			results: []*checker.UpdateResult{
				{App: &app.App{Name: "Firefox"}, CurrentVersion: "120.0", LatestVersion: "121.0"},
			},
			want: "Firefox (120.0\u2192121.0)",
		},
		{
			name: "multiple apps with versions",
			results: []*checker.UpdateResult{
				{App: &app.App{Name: "Firefox"}, CurrentVersion: "120.0", LatestVersion: "121.0"},
				{App: &app.App{Name: "Chrome"}, CurrentVersion: "144.0", LatestVersion: "145.0"},
			},
			want: "Firefox (120.0\u2192121.0), Chrome (144.0\u2192145.0)",
		},
		{
			name:    "truncation",
			results: generateLongResults(50),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNotificationBody(tt.results)
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

func TestBuildNotificationSubtitle(t *testing.T) {
	tests := []struct {
		name    string
		results []*checker.UpdateResult
		want    string
	}{
		{
			name: "no major updates",
			results: []*checker.UpdateResult{
				{App: &app.App{Name: "Firefox"}, IsMajorUpdate: false},
				{App: &app.App{Name: "Chrome"}, IsMajorUpdate: false},
			},
			want: "",
		},
		{
			name: "one major update",
			results: []*checker.UpdateResult{
				{App: &app.App{Name: "Firefox"}, IsMajorUpdate: true},
				{App: &app.App{Name: "Chrome"}, IsMajorUpdate: false},
			},
			want: "1 major update",
		},
		{
			name: "multiple major updates",
			results: []*checker.UpdateResult{
				{App: &app.App{Name: "Firefox"}, IsMajorUpdate: true},
				{App: &app.App{Name: "Chrome"}, IsMajorUpdate: true},
				{App: &app.App{Name: "Safari"}, IsMajorUpdate: false},
			},
			want: "2 major updates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNotificationSubtitle(tt.results)
			if got != tt.want {
				t.Errorf("buildNotificationSubtitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSendNotification(t *testing.T) {
	captureRunner := &captureNotifyRunner{}

	err := sendNotification(context.Background(), captureRunner, 3, "Firefox, Chrome, Safari", "")
	if err != nil {
		t.Fatalf("sendNotification failed: %v", err)
	}

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
	// No subtitle when empty.
	if strings.Contains(script, "subtitle") {
		t.Error("expected no subtitle when empty string passed")
	}
}

func TestSendNotification_WithSubtitle(t *testing.T) {
	captureRunner := &captureNotifyRunner{}

	err := sendNotification(context.Background(), captureRunner, 2, "Firefox, Chrome", "1 major update")
	if err != nil {
		t.Fatalf("sendNotification failed: %v", err)
	}

	script := captureRunner.args[1]
	if !strings.Contains(script, `subtitle "1 major update"`) {
		t.Errorf("expected subtitle in script, got: %s", script)
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

func generateLongResults(n int) []*checker.UpdateResult {
	results := make([]*checker.UpdateResult, n)
	for i := range results {
		results[i] = &checker.UpdateResult{
			App:            &app.App{Name: "ApplicationWithALongName"},
			CurrentVersion: "1.0",
			LatestVersion:  "2.0",
		}
	}
	return results
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

func TestSendInteractiveNotification(t *testing.T) {
	captureRunner := &captureNotifyRunner{}

	err := sendInteractiveNotification(context.Background(), captureRunner, 3, "Firefox, Chrome, Safari")
	if err != nil {
		t.Fatalf("sendInteractiveNotification failed: %v", err)
	}

	if captureRunner.name != "osascript" {
		t.Errorf("expected osascript, got %s", captureRunner.name)
	}
	if len(captureRunner.args) < 2 {
		t.Fatal("expected at least 2 args")
	}
	script := captureRunner.args[1]
	if !strings.Contains(script, "display dialog") {
		t.Error("expected 'display dialog' in interactive script")
	}
	if !strings.Contains(script, "Open Updater") {
		t.Error("expected 'Open Updater' button in script")
	}
	if !strings.Contains(script, "Dismiss") {
		t.Error("expected 'Dismiss' button in script")
	}
}

func TestRunNotify_AutoUpdateFlag(t *testing.T) {
	f := notifyCmd.Flags().Lookup("auto-update")
	if f == nil {
		t.Fatal("--auto-update flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("default = %q, want %q", f.DefValue, "false")
	}
}

func TestInteractiveNotificationsConfig(t *testing.T) {
	cfg := &config.Config{InteractiveNotifications: true}
	if !cfg.InteractiveNotifications {
		t.Error("expected InteractiveNotifications to be true")
	}

	// Verify YAML serialization
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "interactive_notifications: true") {
		t.Errorf("expected yaml to contain interactive_notifications, got: %s", string(data))
	}
}

func TestSendNotification_PassiveDefault(t *testing.T) {
	captureRunner := &captureNotifyRunner{}

	err := sendNotification(context.Background(), captureRunner, 1, "Firefox (120→121)", "")
	if err != nil {
		t.Fatalf("sendNotification failed: %v", err)
	}

	script := captureRunner.args[1]
	if !strings.Contains(script, "display notification") {
		t.Error("expected 'display notification' (passive) for default mode")
	}
	if strings.Contains(script, "display dialog") {
		t.Error("passive mode should NOT use 'display dialog'")
	}
}
