package checker

import (
	"context"
	"fmt"
	"testing"

	"github.com/luzhengda/updater/internal/app"
)

func TestMASChecker_Name(t *testing.T) {
	c := NewMASChecker(nil)
	if got := c.Name(); got != "mas" {
		t.Errorf("Name() = %q, want %q", got, "mas")
	}
}

func TestMASChecker_CheckRunnerError(t *testing.T) {
	runner := &MockCmdRunner{Err: fmt.Errorf("mas not found")}
	c := NewMASChecker(runner)
	a := &app.App{Name: "Magnet", Version: "3.0.6", Source: app.SourceMAS, MASID: "441258766"}

	_, err := c.Check(context.Background(), a)
	if err == nil {
		t.Fatal("expected error when runner fails, got nil")
	}
}

func TestMASChecker_ParseOutdated(t *testing.T) {
	output := "441258766 Magnet (3.0.6 -> 3.0.7)\n1176895641 Spark (3.27.8 -> 3.27.9)\n"

	items, err := parseMASOutdated([]byte(output))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	tests := []struct {
		idx            int
		wantID         string
		wantName       string
		wantCurrent    string
		wantLatest     string
	}{
		{0, "441258766", "Magnet", "3.0.6", "3.0.7"},
		{1, "1176895641", "Spark", "3.27.8", "3.27.9"},
	}

	for _, tt := range tests {
		item := items[tt.idx]
		if item.ID != tt.wantID {
			t.Errorf("item[%d] ID = %s, want %s", tt.idx, item.ID, tt.wantID)
		}
		if item.Name != tt.wantName {
			t.Errorf("item[%d] Name = %s, want %s", tt.idx, item.Name, tt.wantName)
		}
		if item.CurrentVersion != tt.wantCurrent {
			t.Errorf("item[%d] CurrentVersion = %s, want %s", tt.idx, item.CurrentVersion, tt.wantCurrent)
		}
		if item.LatestVersion != tt.wantLatest {
			t.Errorf("item[%d] LatestVersion = %s, want %s", tt.idx, item.LatestVersion, tt.wantLatest)
		}
	}
}

func TestMASChecker_CanCheck(t *testing.T) {
	checker := NewMASChecker(nil)

	tests := []struct {
		name string
		app  *app.App
		want bool
	}{
		{
			name: "MAS app with ID",
			app:  &app.App{Source: app.SourceMAS, MASID: "441258766"},
			want: true,
		},
		{
			name: "MAS source without ID",
			app:  &app.App{Source: app.SourceMAS},
			want: true,
		},
		{
			name: "non-MAS app",
			app:  &app.App{Source: app.SourceSparkle},
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

func TestMASChecker_CheckWithMock(t *testing.T) {
	masOutput := "441258766 Magnet (3.0.6 -> 3.0.7)\n1176895641 Spark (3.27.8 -> 3.27.9)\n"
	runner := &MockCmdRunner{
		Output: []byte(masOutput),
	}

	checker := NewMASChecker(runner)
	a := &app.App{
		Name:    "Magnet",
		Version: "3.0.6",
		Source:  app.SourceMAS,
		MASID:   "441258766",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "3.0.7" {
		t.Errorf("expected LatestVersion 3.0.7, got %s", result.LatestVersion)
	}
	if result.CurrentVersion != "3.0.6" {
		t.Errorf("expected CurrentVersion 3.0.6, got %s", result.CurrentVersion)
	}
	if result.Source != "mas" {
		t.Errorf("expected Source mas, got %s", result.Source)
	}
}

func TestMASChecker_CheckByName(t *testing.T) {
	masOutput := "441258766 Magnet (3.0.6 -> 3.0.7)\n"
	runner := &MockCmdRunner{
		Output: []byte(masOutput),
	}

	checker := NewMASChecker(runner)
	a := &app.App{
		Name:    "Magnet",
		Version: "3.0.6",
		Source:  app.SourceMAS,
		// No MASID set — should match by name
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasUpdate {
		t.Error("expected HasUpdate to be true")
	}
	if result.LatestVersion != "3.0.7" {
		t.Errorf("LatestVersion = %q, want %q", result.LatestVersion, "3.0.7")
	}
	// Verify MASID was populated
	if a.MASID != "441258766" {
		t.Errorf("MASID = %q, want %q", a.MASID, "441258766")
	}
}

func TestMASChecker_CheckNoUpdate(t *testing.T) {
	// Empty output means no updates
	runner := &MockCmdRunner{
		Output: []byte(""),
	}

	checker := NewMASChecker(runner)
	a := &app.App{
		Name:    "Magnet",
		Version: "3.0.7",
		Source:  app.SourceMAS,
		MASID:   "441258766",
	}

	result, err := checker.Check(context.Background(), a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasUpdate {
		t.Error("expected HasUpdate to be false")
	}
}
