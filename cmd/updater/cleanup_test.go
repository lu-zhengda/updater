package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/checker"
)

func TestGetLastUsedDate(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
		isZero  bool
		wantDay string
	}{
		{
			name:    "valid date",
			output:  "kMDItemLastUsedDate = 2024-06-15 10:30:00 +0000",
			wantDay: "2024-06-15",
		},
		{
			name:   "null value",
			output: "kMDItemLastUsedDate = (null)",
			isZero: true,
		},
		{
			name:    "unexpected format",
			output:  "something weird",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &checker.MockCmdRunner{Output: []byte(tt.output)}
			got, err := getLastUsedDate(context.Background(), runner, "/Applications/Test.app")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.isZero {
				if !got.IsZero() {
					t.Errorf("expected zero time, got %v", got)
				}
				return
			}
			if got.Format("2006-01-02") != tt.wantDay {
				t.Errorf("date = %s, want %s", got.Format("2006-01-02"), tt.wantDay)
			}
		})
	}
}

func TestGetLastUsedDate_RunnerError(t *testing.T) {
	runner := &checker.MockCmdRunner{Err: fmt.Errorf("command failed")}
	_, err := getLastUsedDate(context.Background(), runner, "/Applications/Test.app")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetAppSize(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
		want   string
	}{
		{
			name:   "normal output",
			output: "156M\t/Applications/Test.app",
			want:   "156M",
		},
		{
			name:   "gigabyte size",
			output: "1.2G\t/Applications/Big.app",
			want:   "1.2G",
		},
		{
			name: "error",
			err:  fmt.Errorf("failed"),
			want: "?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &checker.MockCmdRunner{Output: []byte(tt.output), Err: tt.err}
			got := getAppSize(context.Background(), runner, "/Applications/Test.app")
			if got != tt.want {
				t.Errorf("getAppSize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMoveToTrash(t *testing.T) {
	runner := &checker.MultiMockCmdRunner{
		Responses: map[string]checker.MockResponse{
			`osascript -e tell application "Finder" to delete POSIX file "/Applications/Test.app"`: {
				Output: nil,
			},
		},
	}

	a := &app.App{Name: "Test", Path: "/Applications/Test.app"}
	err := moveToTrash(context.Background(), runner, a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMoveToTrash_Error(t *testing.T) {
	runner := &checker.MockCmdRunner{Err: fmt.Errorf("osascript failed")}
	a := &app.App{Name: "Test", Path: "/Applications/Test.app"}
	err := moveToTrash(context.Background(), runner, a)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGetLastUsedDate_CutoffLogic(t *testing.T) {
	// Verify date comparison logic used in runCleanup.
	cutoff := time.Now().AddDate(0, 0, -90)
	old := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Now().Add(-24 * time.Hour)

	if !old.Before(cutoff) {
		t.Error("2023-01-01 should be before 90-day cutoff")
	}
	if recent.Before(cutoff) {
		t.Error("yesterday should not be before 90-day cutoff")
	}
}
