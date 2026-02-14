package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/luzhengda/updater/internal/checker"
)

func TestSearchCasks(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{
			name:   "multiple results with header",
			output: "==> Casks\nfirefox\nfirefox-developer-edition\nfirefox-esr\n",
			want:   []string{"firefox", "firefox-developer-edition", "firefox-esr"},
		},
		{
			name:   "single result",
			output: "==> Casks\nvisual-studio-code\n",
			want:   []string{"visual-studio-code"},
		},
		{
			name:   "no results",
			output: "",
			want:   nil,
		},
		{
			name:   "results without header",
			output: "iterm2\nalacritty\n",
			want:   []string{"iterm2", "alacritty"},
		},
		{
			name:   "multiple headers",
			output: "==> Casks\nfirefox\n==> Formulae\nfirefox-bin\n",
			want:   []string{"firefox", "firefox-bin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &checker.MockCmdRunner{Output: []byte(tt.output)}
			got, err := searchCasks(context.Background(), runner, "test")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len(results) = %d, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("results[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}

func TestSearchCasks_Error(t *testing.T) {
	runner := &checker.MockCmdRunner{Err: fmt.Errorf("brew not found")}
	_, err := searchCasks(context.Background(), runner, "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSelectCask(t *testing.T) {
	matches := []string{"firefox", "firefox-esr", "firefox-developer-edition"}

	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"first", "1", 0, false},
		{"last", "3", 2, false},
		{"middle", "2", 1, false},
		{"zero", "0", 0, true},
		{"too high", "4", 0, true},
		{"non-numeric", "abc", 0, true},
		{"empty", "", 0, true},
		{"negative", "-1", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectCask(matches, tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("selectCask() = %d, want %d", got, tt.want)
			}
		})
	}
}
