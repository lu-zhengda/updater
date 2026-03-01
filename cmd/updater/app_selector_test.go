package main

import (
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
)

func TestJoinAppNameArgs(t *testing.T) {
	got := joinAppNameArgs([]string{"PDF", "Expert"})
	if got != "PDF Expert" {
		t.Fatalf("joinAppNameArgs() = %q, want %q", got, "PDF Expert")
	}
}

func TestResolveAppSelection_GenericAliases(t *testing.T) {
	apps := []*app.App{
		{
			Name:     "PDF Expert",
			BundleID: "com.readdle.PDFExpert-Mac",
			Path:     "/Applications/PDF Expert.app",
		},
		{
			Name:     "GitHub Desktop",
			BundleID: "com.github.GitHubClient",
			Path:     "/Applications/GitHub Desktop.app",
		},
		{
			Name:     "Code",
			BundleID: "com.microsoft.VSCode",
			Path:     "/Applications/Visual Studio Code.app",
		},
	}

	tests := []struct {
		query string
		want  string
	}{
		{query: "PDF Expert", want: "PDF Expert"},
		{query: "pdf-expert", want: "PDF Expert"},
		{query: "github", want: "GitHub Desktop"},
		{query: "GitHub Desktop", want: "GitHub Desktop"},
		{query: "visual-studio-code", want: "Code"},
		{query: "vscode", want: "Code"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got, err := resolveAppSelection(apps, tt.query)
			if err != nil {
				t.Fatalf("resolveAppSelection() error = %v", err)
			}
			if got.Name != tt.want {
				t.Fatalf("resolveAppSelection() = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestResolveAppSelection_Ambiguous(t *testing.T) {
	apps := []*app.App{
		{Name: "My App", BundleID: "com.acme.myapp", Path: "/Applications/My App.app"},
		{Name: "My-App", BundleID: "com.acme2.my_app", Path: "/Applications/My-App.app"},
	}

	_, err := resolveAppSelection(apps, "myapp")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
}

func TestResolveAppSelection_NotFound(t *testing.T) {
	apps := []*app.App{{Name: "Firefox", BundleID: "org.mozilla.firefox", Path: "/Applications/Firefox.app"}}

	_, err := resolveAppSelection(apps, "non-existent-app")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
