package tui

import (
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
)

func TestRenderRowKeepsMetadataOnOneLine(t *testing.T) {
	a := &app.App{
		Name:    "scoped\npackage",
		Version: "1.0.0\rinstalled",
		Source:  app.SourceNpm,
	}
	m := Model{
		width: 120,
		rows: []row{{
			app:     a,
			checked: true,
			result: &checker.UpdateResult{
				App:           a,
				Source:        "npm",
				LatestVersion: "[\n  \"2.0.0\"\n]",
				HasUpdate:     true,
			},
		}},
		updating: make(map[int]bool),
		ignored:  make(map[int]bool),
		pinned:   make(map[int]bool),
	}

	got := m.renderRow(0, false, false)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("renderRow() produced multiple terminal lines: %q", got)
	}
}
