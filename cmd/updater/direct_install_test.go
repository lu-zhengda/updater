package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/installer"
)

type directInstallRunner struct {
	opened   bool
	openArgs []string
	openErr  error
}

func (r *directInstallRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "pgrep" {
		return nil, errors.New("not running")
	}
	if name == "open" {
		r.opened = true
		r.openArgs = append([]string(nil), args...)
		return nil, r.openErr
	}
	return nil, nil
}

func TestDirectInstallFailureOpensManualFallbackAndReportsCause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	for _, source := range []string{"brew-info", "electron", "sparkle", "github", "vendor"} {
		t.Run(source, func(t *testing.T) {
			runner := &directInstallRunner{}
			r := &checker.UpdateResult{App: &app.App{Name: "Code", BundleID: "com.microsoft.VSCode", Path: t.TempDir() + "/Code.app"}, Source: source, DownloadURL: srv.URL + "/Code.zip"}
			err, _ := executeUpdate(context.Background(), r, runner, nil, installer.New(runner, srv.Client()))
			if err == nil || errors.Is(err, checker.ErrOpenedExternally) || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "for manual update") || !runner.opened {
				t.Fatalf("expected manual handoff with original failure: err=%v opened=%v", err, runner.opened)
			}
			want := "-a " + r.App.Path
			if source == "sparkle" || source == "github" {
				want = r.DownloadURL
			}
			if strings.Join(runner.openArgs, " ") != want {
				t.Fatalf("fallback opened %v, want %s", runner.openArgs, want)
			}
		})
	}
}

func TestDirectInstallReportsFailedManualHandoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	runner := &directInstallRunner{openErr: errors.New("Launch Services unavailable")}
	r := &checker.UpdateResult{App: &app.App{Name: "Code", Path: t.TempDir() + "/Code.app"}, Source: "vendor", DownloadURL: srv.URL + "/Code.zip"}
	err, _ := executeUpdate(context.Background(), r, runner, nil, installer.New(runner, srv.Client()))
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "Launch Services unavailable") || strings.Contains(err.Error(), "opened app") {
		t.Fatalf("expected both install and handoff errors: %v", err)
	}
}

func TestCancelledDirectInstallDoesNotOpenManualFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &directInstallRunner{}
	r := &checker.UpdateResult{App: &app.App{Name: "Code", Path: t.TempDir() + "/Code.app"}, Source: "vendor", DownloadURL: "https://example.com/Code.zip"}
	err, _ := tryDirectInstall(ctx, r, runner, nil, installer.New(runner, nil))
	if !errors.Is(err, context.Canceled) || runner.opened {
		t.Fatalf("cancelled install opened fallback: err=%v opened=%v", err, runner.opened)
	}
}

func TestVersionOnlyReleaseCannotReportInstalled(t *testing.T) {
	r := &checker.UpdateResult{App: &app.App{Name: "Code"}, Source: "github", HasUpdate: true, LatestVersion: "1.140.0"}
	err, _ := executeUpdate(context.Background(), r, &directInstallRunner{}, nil, nil)
	if err == nil || errors.Is(err, checker.ErrOpenedExternally) {
		t.Fatalf("version-only release reported success: %v", err)
	}
}
