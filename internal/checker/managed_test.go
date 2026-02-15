package checker

import (
	"context"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestManagedChecker_CanCheck(t *testing.T) {
	c := NewManagedChecker()

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "setapp app",
			app:  &app.App{Source: app.SourceSetapp},
			want: true,
		},
		{
			name: "toolbox app",
			app:  &app.App{Source: app.SourceToolbox},
			want: true,
		},
		{
			name: "adobe app",
			app:  &app.App{Source: app.SourceAdobe},
			want: true,
		},
		{
			name: "unknown app",
			app:  &app.App{Source: app.SourceUnknown},
			want: false,
		},
		{
			name: "sparkle app",
			app:  &app.App{Source: app.SourceSparkle},
			want: false,
		},
		{
			name: "electron app",
			app:  &app.App{Source: app.SourceElectron},
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

func TestManagedChecker_Check(t *testing.T) {
	c := NewManagedChecker()

	tests := []struct {
		name       string
		app        *app.App
		wantSource string
	}{
		{
			name:       "setapp returns no update",
			app:        &app.App{Name: "Paste", Version: "3.0.0", Source: app.SourceSetapp},
			wantSource: "setapp",
		},
		{
			name:       "toolbox returns no update",
			app:        &app.App{Name: "IntelliJ IDEA", Version: "2026.1", Source: app.SourceToolbox},
			wantSource: "toolbox",
		},
		{
			name:       "adobe returns no update",
			app:        &app.App{Name: "Adobe Photoshop", Version: "26.0", BundleID: "com.adobe.Photoshop", Source: app.SourceAdobe},
			wantSource: "adobe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := c.Check(context.Background(), tt.app)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.HasUpdate {
				t.Error("expected HasUpdate to be false")
			}
			if result.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", result.Source, tt.wantSource)
			}
			if result.CurrentVersion != tt.app.Version {
				t.Errorf("CurrentVersion = %q, want %q", result.CurrentVersion, tt.app.Version)
			}
			if result.LatestVersion != tt.app.Version {
				t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, tt.app.Version)
			}
		})
	}
}

func TestManagedChecker_Name(t *testing.T) {
	c := NewManagedChecker()
	if c.Name() != "managed" {
		t.Errorf("Name() = %q, want %q", c.Name(), "managed")
	}
}
