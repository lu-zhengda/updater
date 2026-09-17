package updater

import (
	"context"
	"fmt"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
	"github.com/lu-zhengda/updater/internal/checker"
)

func TestNativeUpgradeSuggestion(t *testing.T) {
	old := architecture.Native
	t.Cleanup(func() { architecture.Native = old })
	for _, tc := range []struct {
		name, host, current, latest, asset string
		intel, want                        bool
	}{
		{"same version ARM", "arm64", "2.0", "2.0", "app-arm64.zip", true, true},
		{"same version universal", "arm64", "2.0", "2.0", "app-universal.zip", true, true},
		{"newer ARM", "arm64", "2.0", "3.0", "app-arm64.zip", true, true},
		{"no downgrade", "arm64", "3.0", "2.0", "app-arm64.zip", true, false},
		{"unknown download", "arm64", "2.0", "2.0", "app.zip", true, false},
		{"intel download", "arm64", "2.0", "2.0", "app-x64.zip", true, false},
		{"already native", "arm64", "2.0", "2.0", "app-arm64.zip", false, false},
		{"intel hardware", "amd64", "2.0", "2.0", "app-arm64.zip", true, false},
		{"unknown version", "arm64", "", "2.0", "app-arm64.zip", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			architecture.Native = func() string { return tc.host }
			a := &app.App{Name: "Test", Version: tc.current, IntelOnly: tc.intel}
			original := &checker.UpdateResult{App: a, Source: "electron", CurrentVersion: tc.current, LatestVersion: tc.latest, DownloadURL: "https://example.com/" + tc.asset}
			c := &mockChecker{name: "electron", canCheck: func(*app.App) bool { return true }, result: original}
			got := CheckWithFallthrough(context.Background(), a, []checker.Checker{c})
			if got.NativeUpgrade != tc.want || got.HasUpdate != tc.want {
				t.Fatalf("unexpected suggestion: %+v", got)
			}
			if original.NativeUpgrade || original.HasUpdate {
				t.Fatal("mutated checker's original result")
			}
		})
	}
}

func TestNativeUpgradeFallthrough(t *testing.T) {
	old := architecture.Native
	architecture.Native = func() string { return "arm64" }
	t.Cleanup(func() { architecture.Native = old })
	a := &app.App{Name: "Test", Version: "2.0", IntelOnly: true}
	upToDate := &mockChecker{name: "brew", canCheck: func(*app.App) bool { return true }, result: &checker.UpdateResult{App: a, Source: "brew", CurrentVersion: "2.0", LatestVersion: "2.0"}}
	native := &mockChecker{name: "electron", canCheck: func(*app.App) bool { return true }, result: &checker.UpdateResult{App: a, Source: "electron", CurrentVersion: "2.0", LatestVersion: "2.0", DownloadURL: "https://example.com/app-arm64.zip"}}
	got := CheckWithFallthrough(context.Background(), a, []checker.Checker{upToDate, native})
	if !got.NativeUpgrade || got.Source != "electron" {
		t.Fatalf("missed native fallback: %+v", got)
	}
	native.err = fmt.Errorf("offline")
	got = CheckWithFallthrough(context.Background(), a, []checker.Checker{upToDate, native})
	if got.Error != nil || got.Source != "brew" || got.HasUpdate {
		t.Fatalf("lost original valid result: %+v", got)
	}
}
