package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/updater"
	versionpkg "github.com/lu-zhengda/updater/internal/version"
	"howett.net/plist"
)

func selfBundleFiles(t *testing.T, version string) map[string][]byte {
	t.Helper()
	info, err := plist.Marshal(map[string]string{
		"CFBundleIdentifier": app.UpdaterBundleID, "CFBundleExecutable": "updater", "CFBundleShortVersionString": version,
	}, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"Contents/Info.plist": info, "Contents/MacOS/updater": []byte("binary " + version), "Contents/Resources/marker": []byte("resource " + version)}
}

func writeSelfBundle(t *testing.T, bundle, version string) {
	t.Helper()
	for name, data := range selfBundleFiles(t, version) {
		path := filepath.Join(bundle, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

type selfUpgradeTestRunner struct {
	t                            *testing.T
	bundle                       string
	brew, fail, managed, stopped bool
	failRefresh                  bool
	failFetch                    bool
	diverged                     bool
	realGit                      bool
	tapRepo                      string
	refreshed                    bool
	caskVersion                  string
	staleCask                    string
	brewVersion                  string
	calls                        []string
}

func TestMain(m *testing.M) {
	if path := os.Getenv("UPDATER_LOCK_PROBE"); path != "" {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		f.Close()
		if err != nil {
			os.Exit(0)
		}
		os.Exit(1)
	}
	// macOS flock is per process, so a second open in this process does not
	// conflict. A child holds the stage lock the way the self-update helper does.
	if path := os.Getenv("UPDATER_STAGE_HOLD"); path != "" {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			os.Exit(2)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			os.Exit(2)
		}
		if _, err := os.Stdout.Write([]byte("held\n")); err != nil {
			os.Exit(2)
		}
		_ = os.Stdout.Sync()
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func probeUpdateLockHeld(t *testing.T) {
	t.Helper()
	path := filepath.Join(filepath.Dir(config.DefaultPath()), "update.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append([]string{"UPDATER_LOCK_PROBE=" + path}, os.Environ()...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("update.lock was not held: %v", err)
	}
}

func (r *selfUpgradeTestRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	if strings.Contains(key, "update-reset") || strings.Contains(key, " reset") || strings.Contains(key, "reset --hard") {
		r.t.Fatalf("tap refresh reset the checkout: %s", key)
	}
	switch {
	case key == "brew list --cask "+updaterCask:
		if r.brew {
			return []byte(r.bundle + "\n"), nil
		}
		return nil, errors.New("not installed via brew")
	case key == "brew --repository "+updaterTap:
		if r.failRefresh {
			return nil, errors.New("tap repository unavailable")
		}
		repo := r.tapRepo
		if repo == "" {
			repo = "/opt/homebrew/Library/Taps/lu-zhengda/homebrew-tap"
		}
		return []byte(repo + "\n"), nil
	case name == "git":
		return r.runGit(ctx, args)
	case strings.HasPrefix(key, "brew info --cask --json=v2 "):
		probeUpdateLockHeld(r.t)
		version := r.caskVersion
		if version == "" {
			version = "2.0.0"
		}
		if r.staleCask != "" && !r.refreshed {
			version = r.staleCask
		}
		return []byte(`{"casks":[{"token":"updater","version":"` + version + `"}]}`), nil
	case name == "/usr/bin/codesign":
		return nil, nil
	case name == "/usr/bin/ditto":
		return nil, os.CopyFS(args[1], os.DirFS(args[0]))
	case key == "brew upgrade --cask "+updaterCask:
		probeUpdateLockHeld(r.t)
		version := r.brewVersion
		if version == "" {
			version = "2.0.0"
		}
		writeSelfBundle(r.t, r.bundle, version)
		if r.fail {
			return nil, errors.New("brew failed after replacing files")
		}
		return nil, nil
	case name == "launchctl" && args[0] == "print":
		if r.managed {
			return []byte("\tprogram = " + filepath.Join(r.bundle, "Contents/MacOS/updater") + "\n"), nil
		}
		return nil, errors.New("not loaded")
	case name == "launchctl":
		return nil, nil
	case name == "/bin/ps":
		if r.stopped {
			return nil, nil
		}
		return []byte("123 " + filepath.Join(r.bundle, "Contents/MacOS/updater") + " menubar run\n"), nil
	case name == "/bin/kill":
		r.stopped = true
		return nil, nil
	case name == "/usr/bin/open":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command %s", key)
	}
}

func (r *selfUpgradeTestRunner) runGit(ctx context.Context, args []string) ([]byte, error) {
	if len(args) < 3 || args[0] != "-C" {
		return nil, fmt.Errorf("unexpected git %s", strings.Join(args, " "))
	}
	probeUpdateLockHeld(r.t)
	if r.realGit {
		cmd := exec.CommandContext(ctx, "git", args...)
		out, err := cmd.CombinedOutput()
		if err == nil && args[2] == "merge" {
			r.refreshed = true
		}
		return out, err
	}
	switch args[2] {
	case "rev-parse":
		return []byte("origin/main\n"), nil
	case "fetch":
		if r.failFetch {
			return nil, errors.New("fetch failed")
		}
		return nil, nil
	case "merge":
		if !slices.Contains(args, "--ff-only") {
			return nil, fmt.Errorf("tap merge must be fast-forward only: %s", strings.Join(args, " "))
		}
		if r.diverged {
			return []byte("fatal: Not possible to fast-forward, aborting.\n"), errors.New("exit status 128")
		}
		r.refreshed = true
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected git %s", strings.Join(args, " "))
	}
}

func assertNoTapReset(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call, "update-reset") || strings.Contains(call, " reset") || strings.Contains(call, "reset --hard") {
			t.Fatalf("tap refresh reset the checkout: %s in %v", call, calls)
		}
	}
}

func TestSelfUpgradeTransaction(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		brew, fail, rejectSignature, badDigest, managed, current bool
	}{
		{name: "direct complete bundle"},
		{name: "direct with login agent", managed: true},
		{name: "signature rejected before quit", rejectSignature: true},
		{name: "checksum rejected before quit", badDigest: true},
		{name: "brew cask", brew: true},
		{name: "brew failure rolls back", brew: true, fail: true, managed: true},
		{name: "local build of current release", current: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bundle := filepath.Join(t.TempDir(), "Updater.app")
			current := "1.0.0"
			if tc.current {
				current = "2.0.0-dirty"
			}
			writeSelfBundle(t, bundle, current)
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			for name, data := range selfBundleFiles(t, "2.0.0") {
				if err := tw.WriteHeader(&tar.Header{Name: "Updater.app/" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data))}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
			if tc.badDigest {
				digest = strings.Repeat("0", 64)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/checksums.txt" {
					fmt.Fprintln(w, digest+"  updater_2.0.0_darwin.tar.gz")
					return
				}
				_, _ = w.Write(archive.Bytes())
			}))
			defer server.Close()
			oldRelease, oldVerify := selfUpgradeRelease, verifySelfUpgrade
			t.Cleanup(func() { selfUpgradeRelease, verifySelfUpgrade = oldRelease, oldVerify })
			selfUpgradeRelease = func(string) (*checker.GitHubRelease, error) {
				return &checker.GitHubRelease{TagName: "v2.0.0", Assets: []checker.GitHubAsset{
					{Name: "updater_2.0.0_darwin.tar.gz", DownloadURL: server.URL + "/app.tar.gz", Digest: "sha256:" + digest},
					{Name: "checksums.txt", DownloadURL: server.URL + "/checksums.txt"},
				}}, nil
			}
			verified := false
			verifySelfUpgrade = func(_ context.Context, old, candidate string) error {
				verified = true
				if tc.rejectSignature {
					return errors.New("wrong signing identity")
				}
				for path, want := range map[string]string{old: current, candidate: "2.0.0"} {
					info, err := readUpdaterBundle(path)
					if err != nil || info.Version != want {
						t.Fatalf("verification path %s: %+v, %v", path, info, err)
					}
				}
				return nil
			}
			runner := &selfUpgradeTestRunner{t: t, bundle: bundle, brew: tc.brew, fail: tc.fail, managed: tc.managed}
			message, err := applySelfUpgrade(context.Background(), bundle, runner)
			wantError := tc.fail || tc.rejectSignature || tc.badDigest
			if (err != nil) != wantError {
				t.Fatalf("message %q, error %v", message, err)
			}
			want := "2.0.0"
			if wantError || tc.current {
				want = current
			}
			info, readErr := readUpdaterBundle(bundle)
			if readErr != nil || info.Version != want {
				t.Fatalf("installed bundle: %+v, %v; want %s", info, readErr, want)
			}
			resource, err := os.ReadFile(filepath.Join(bundle, "Contents/Resources/marker"))
			if err != nil || string(resource) != "resource "+want {
				t.Fatalf("resources were not replaced/restored: %s, %v", resource, err)
			}
			if (tc.rejectSignature || tc.badDigest || tc.current) && runner.stopped {
				t.Fatal("closed app before validating update")
			}
			if !wantError && !tc.current && !verified {
				t.Fatal("installed unverified bundle")
			}
			entries, err := history.List(history.DefaultPath())
			if err != nil {
				t.Fatal(err)
			}
			if tc.rejectSignature || tc.badDigest || tc.current {
				if len(entries) != 0 {
					t.Fatal("recorded install without attempting one")
				}
			} else if len(entries) != 1 || entries[0].Success != !tc.fail || entries[0].RolledBack != tc.fail {
				t.Fatalf("incorrect history: %+v", entries)
			}
			if runner.stopped {
				last := runner.calls[len(runner.calls)-1]
				if tc.managed && !strings.HasPrefix(last, "launchctl bootstrap ") {
					t.Fatalf("agent not restored: %v", runner.calls)
				}
				if !tc.managed && last != "/usr/bin/open "+bundle {
					t.Fatalf("app not restarted: %v", runner.calls)
				}
			}
		})
	}
}

func TestSelfUpgradeGuardrails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, version := range []string{"v1.2.3-dirty", "v1.2.3-5-gabc123", "v1.2.3-5-gabc123-dirty"} {
		if got := versionpkg.ReleaseVersion(version); got != "v1.2.3" {
			t.Fatalf("development version %s became %s", version, got)
		}
	}
	if versionpkg.ReleaseVersion("1.2.3-rc.1") != "1.2.3-rc.1" {
		t.Fatal("discarded real prerelease")
	}
	stage := t.TempDir()
	old, candidate, backup := filepath.Join(stage, "Updater.app"), filepath.Join(stage, "missing.app"), filepath.Join(stage, "Previous.app")
	writeSelfBundle(t, old, "1.0.0")
	apps, err := app.Discover(stage)
	if err != nil || len(apps) != 1 || apps[0].GitHubRepo != "lu-zhengda/updater" {
		t.Fatalf("Updater lacks its own release source: %+v, %v", apps, err)
	}
	apps[0].Version = "2.0.0-dirty"
	result := updater.CheckWithFallthrough(context.Background(), apps[0], []checker.Checker{&menubarPackageChecker{source: app.SourceGitHub}})
	if result.HasUpdate {
		t.Fatal("local build was offered its matching release")
	}
	if err, rolledBack := replaceUpdaterBundle(old, candidate, backup); err == nil || rolledBack {
		t.Fatalf("missing candidate displaced the installed app: %v, rolledBack=%t", err, rolledBack)
	}
	if info, err := readUpdaterBundle(old); err != nil || info.Version != "1.0.0" {
		t.Fatalf("lost previous app: %+v, %v", info, err)
	}
	if _, statErr := os.Stat(backup); !os.IsNotExist(statErr) {
		t.Fatalf("backup appeared without a swap: %v", statErr)
	}
	alias := filepath.Join(t.TempDir(), "Updater.app")
	if err := os.Symlink(old, alias); err != nil {
		t.Fatal(err)
	}
	runner := &checker.MockCmdRunner{Output: []byte(alias + "\n")}
	if !isUpdaterCask(context.Background(), old, runner) || isUpdaterCask(context.Background(), filepath.Join(stage, "Other.app"), runner) {
		t.Fatal("incorrect cask ownership")
	}
	executable := "/Applications/Updater.app/Contents/MacOS/updater"
	processes := "101 " + executable + " menubar run\n102 " + executable + " window\n103 " + executable + " update --all\n104 /tmp/updater self-upgrade /Applications/Updater.app\n105 " + executable + "\n"
	if got := updaterUIPIDs(processes, executable); !slices.Equal(got, []string{"101", "102", "105"}) {
		t.Fatalf("unsafe process selection: %v", got)
	}
	shared, err := updateActivityLock(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if lock, err := updateActivityLock(ctx, true); err == nil {
		lock.Close()
		t.Fatal("self-update did not wait for active batch")
	}
	shared.Close()
	exclusive, err := updateActivityLock(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	exclusive.Close()
}

func TestReplaceUpdaterBundleSwaps(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "Updater.app")
	candidate := filepath.Join(dir, "Candidate.app")
	backup := filepath.Join(dir, "Previous.app")
	writeSelfBundle(t, installed, "1.0.0")
	writeSelfBundle(t, candidate, "2.0.0")
	if err, rolledBack := replaceUpdaterBundle(installed, candidate, backup); err != nil || rolledBack {
		t.Fatalf("swap: %v, rolledBack=%t", err, rolledBack)
	}
	got, err := readUpdaterBundle(installed)
	if err != nil || got.Version != "2.0.0" {
		t.Fatalf("installed: %+v, %v", got, err)
	}
	prev, err := readUpdaterBundle(backup)
	if err != nil || prev.Version != "1.0.0" {
		t.Fatalf("previous: %+v, %v", prev, err)
	}
	if _, err := os.Stat(candidate); !os.IsNotExist(err) {
		t.Fatalf("candidate path still occupied: %v", err)
	}

	// Parking the previous app is not a rollback. The app now at the installed
	// path stays there when the backup directory cannot be created.
	nested := filepath.Join(dir, "missing", "Previous.app")
	if err, rolledBack := replaceUpdaterBundle(installed, backup, nested); err != nil || rolledBack {
		t.Fatalf("swap with unparkable backup: %v, rolledBack=%t", err, rolledBack)
	}
	got, err = readUpdaterBundle(installed)
	if err != nil || got.Version != "1.0.0" {
		t.Fatalf("installed after unparkable swap: %+v, %v", got, err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("backup created: %v", err)
	}
}

func TestRestoreUpdaterBundleSwaps(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "Updater.app")
	backup := filepath.Join(dir, "Previous.app")
	failed := filepath.Join(dir, "Failed.app")
	writeSelfBundle(t, bundle, "2.0.0")
	writeSelfBundle(t, backup, "1.0.0")
	rolledBack, keepRecovery, err := restoreUpdaterBundle(bundle, backup, failed)
	if err != nil || !rolledBack || keepRecovery {
		t.Fatalf("restore: rolledBack=%t keep=%t err=%v", rolledBack, keepRecovery, err)
	}
	got, readErr := readUpdaterBundle(bundle)
	if readErr != nil || got.Version != "1.0.0" {
		t.Fatalf("restored: %+v, %v", got, readErr)
	}
	bad, readErr := readUpdaterBundle(failed)
	if readErr != nil || bad.Version != "2.0.0" {
		t.Fatalf("failed install: %+v, %v", bad, readErr)
	}
	if _, statErr := os.Stat(backup); !os.IsNotExist(statErr) {
		t.Fatalf("backup path still occupied: %v", statErr)
	}

	writeSelfBundle(t, bundle, "2.0.0")
	// A new failed-app path: the old two-rename rollback would move the live
	// bundle there before noticing the backup is missing.
	rolledBack, keepRecovery, err = restoreUpdaterBundle(bundle, filepath.Join(dir, "missing.app"), filepath.Join(dir, "Failed-again.app"))
	if err == nil || rolledBack || !keepRecovery {
		t.Fatalf("missing backup: rolledBack=%t keep=%t err=%v", rolledBack, keepRecovery, err)
	}
	got, readErr = readUpdaterBundle(bundle)
	if readErr != nil || got.Version != "2.0.0" {
		t.Fatalf("bundle moved without a backup: %+v, %v", got, readErr)
	}
}

func TestSelfUpgradeRefusesLowCaskVersion(t *testing.T) {
	for _, tc := range []struct {
		name, installed, cask string
		allow                 bool
	}{
		{name: "lower than installed and target", installed: "2.0.0", cask: "1.0.0"},
		{name: "newer than installed but lower than target", installed: "2.0.0", cask: "2.5.0"},
		{name: "matches target", installed: "2.0.0", cask: "3.0.0", allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bundle := filepath.Join(t.TempDir(), "Updater.app")
			writeSelfBundle(t, bundle, tc.installed)
			oldRelease, oldVerify := selfUpgradeRelease, verifySelfUpgrade
			t.Cleanup(func() { selfUpgradeRelease, verifySelfUpgrade = oldRelease, oldVerify })
			selfUpgradeRelease = func(string) (*checker.GitHubRelease, error) {
				return &checker.GitHubRelease{TagName: "v3.0.0"}, nil
			}
			verifySelfUpgrade = func(context.Context, string, string) error { return nil }
			runner := &selfUpgradeTestRunner{t: t, bundle: bundle, brew: true, caskVersion: tc.cask, brewVersion: tc.cask}
			message, err := applySelfUpgrade(context.Background(), bundle, runner)
			upgraded := false
			for _, call := range runner.calls {
				if strings.HasPrefix(call, "brew upgrade ") {
					upgraded = true
				}
			}
			info, readErr := readUpdaterBundle(bundle)
			if tc.allow {
				if err != nil || !upgraded || readErr != nil || info.Version != tc.cask {
					t.Fatalf("err=%v upgraded=%t bundle=%+v read=%v", err, upgraded, info, readErr)
				}
				return
			}
			const want = "Homebrew doesn't have Updater 3.0.0 yet; try again in a few minutes"
			if err != nil || message != want || upgraded || readErr != nil || info.Version != tc.installed {
				t.Fatalf("message=%q err=%v upgraded=%t bundle=%+v read=%v", message, err, upgraded, info, readErr)
			}
		})
	}
}

func TestSelfUpgradeBrewRestoresAfterBadResult(t *testing.T) {
	for _, tc := range []struct {
		name, written string
		reject        bool
	}{
		{name: "bundle version lower than target", written: "2.0.0"},
		{name: "signature rejected", written: "3.0.0", reject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bundle := filepath.Join(t.TempDir(), "Updater.app")
			writeSelfBundle(t, bundle, "1.0.0")
			oldRelease, oldVerify := selfUpgradeRelease, verifySelfUpgrade
			t.Cleanup(func() { selfUpgradeRelease, verifySelfUpgrade = oldRelease, oldVerify })
			selfUpgradeRelease = func(string) (*checker.GitHubRelease, error) {
				return &checker.GitHubRelease{TagName: "v3.0.0"}, nil
			}
			verifySelfUpgrade = func(context.Context, string, string) error {
				if tc.reject {
					return errors.New("wrong signing identity")
				}
				return nil
			}
			runner := &selfUpgradeTestRunner{t: t, bundle: bundle, brew: true, caskVersion: "3.0.0", brewVersion: tc.written}
			_, err := applySelfUpgrade(context.Background(), bundle, runner)
			if err == nil {
				t.Fatal("accepted brew result")
			}
			info, readErr := readUpdaterBundle(bundle)
			if readErr != nil || info.Version != "1.0.0" {
				t.Fatalf("backup was not restored: %+v, %v", info, readErr)
			}
			entries, histErr := history.List(history.DefaultPath())
			if histErr != nil || len(entries) != 1 || !entries[0].RolledBack || entries[0].Success {
				t.Fatalf("history: %+v, %v", entries, histErr)
			}
		})
	}
}

func TestRecoverStrandedUpdater(t *testing.T) {
	for _, tc := range []struct {
		name, live        string
		missing, unsigned bool
		want              string
	}{
		{name: "missing app", missing: true, want: "1.0.0"},
		{name: "unsigned app", live: "9.0.0", unsigned: true, want: "1.0.0"},
		{name: "signed app", live: "2.0.0", want: "2.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			parent := t.TempDir()
			bundle := filepath.Join(parent, "Updater.app")
			if !tc.missing {
				writeSelfBundle(t, bundle, tc.live)
			}
			writeSelfBundle(t, filepath.Join(parent, ".updater-self-stranded", "Previous.app"), "1.0.0")
			runner := &recoverRunner{signed: !tc.unsigned && !tc.missing}
			restored, err := recoverStrandedUpdater(context.Background(), bundle, runner)
			if err != nil {
				t.Fatal(err)
			}
			if restored != (tc.want != tc.live) {
				t.Fatalf("restored=%t, want version %s", restored, tc.want)
			}
			info, readErr := readUpdaterBundle(bundle)
			if readErr != nil || info.Version != tc.want {
				t.Fatalf("bundle: %+v, %v; want %s", info, readErr, tc.want)
			}
			if tc.missing && len(runner.calls) != 0 {
				t.Fatalf("signature check on a missing app: %v", runner.calls)
			}
		})
	}
}

type recoverRunner struct {
	signed bool
	calls  []string
}

func (r *recoverRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "/usr/bin/codesign" {
		if r.signed {
			return nil, nil
		}
		return []byte("code object is not signed at all"), errors.New("exit status 1")
	}
	return nil, fmt.Errorf("unexpected command %s", name)
}

func TestSelfUpgradeRefreshesStaleTap(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		failRefresh, diverged bool
		failFetch             bool
		stale, live           string
		wantUpgrade           bool
	}{
		// brew info reports the installed cask until the fast-forward, then the release.
		{name: "stale tap then release", stale: "0.24.5", live: "0.24.6", wantUpgrade: true},
		{name: "refresh failure still reads a current cask", failRefresh: true, live: "2.0.0", wantUpgrade: true},
		{name: "offline fetch leaves a stale cask waiting", failFetch: true, stale: "0.24.5", live: "0.24.5"},
		{name: "diverged tap keeps local commits", diverged: true, stale: "0.24.5", live: "0.24.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bundle := filepath.Join(t.TempDir(), "Updater.app")
			writeSelfBundle(t, bundle, "0.24.5")
			oldRelease, oldVerify := selfUpgradeRelease, verifySelfUpgrade
			t.Cleanup(func() { selfUpgradeRelease, verifySelfUpgrade = oldRelease, oldVerify })
			tag := "v0.24.6"
			if tc.live == "2.0.0" {
				tag = "v2.0.0"
			}
			selfUpgradeRelease = func(string) (*checker.GitHubRelease, error) {
				return &checker.GitHubRelease{TagName: tag}, nil
			}
			verifySelfUpgrade = func(context.Context, string, string) error { return nil }
			var tapHead string
			runner := &selfUpgradeTestRunner{
				t: t, bundle: bundle, brew: true, failRefresh: tc.failRefresh, failFetch: tc.failFetch, diverged: tc.diverged,
				caskVersion: tc.live, staleCask: tc.stale, brewVersion: tc.live,
			}
			if tc.diverged {
				runner.tapRepo, tapHead = divergedTapCheckout(t)
				runner.realGit = true
			}
			message, err := applySelfUpgrade(context.Background(), bundle, runner)
			assertNoTapReset(t, runner.calls)
			var infoAt, mergeAt = -1, -1
			upgraded := false
			for i, call := range runner.calls {
				switch {
				case strings.Contains(call, "merge --ff-only"):
					mergeAt = i
				case strings.HasPrefix(call, "brew info "):
					infoAt = i
				case strings.HasPrefix(call, "brew upgrade "):
					upgraded = true
				}
			}
			switch {
			case tc.failRefresh && mergeAt >= 0:
				t.Fatalf("refresh continued after the repository lookup failed: %v", runner.calls)
			case tc.failFetch && mergeAt >= 0:
				t.Fatalf("merged after fetch failed: %v", runner.calls)
			case !tc.failRefresh && !tc.failFetch && (mergeAt < 0 || infoAt < mergeAt):
				t.Fatalf("fast-forward must finish before brew info: %v", runner.calls)
			}
			if tc.wantUpgrade && !tc.failRefresh && !tc.failFetch {
				fetch := "git -C /opt/homebrew/Library/Taps/lu-zhengda/homebrew-tap fetch --quiet origin main"
				if !slices.Contains(runner.calls, fetch) {
					t.Fatalf("fetch did not follow the tracked branch: %v", runner.calls)
				}
			}
			info, readErr := readUpdaterBundle(bundle)
			if tc.wantUpgrade {
				if err != nil || !upgraded || readErr != nil || info.Version != tc.live {
					t.Fatalf("message=%q err=%v upgraded=%t bundle=%+v read=%v calls=%v", message, err, upgraded, info, readErr, runner.calls)
				}
				return
			}
			want := "Homebrew doesn't have Updater 0.24.6 yet; try again in a few minutes"
			if err != nil || message != want || upgraded || readErr != nil || info.Version != "0.24.5" {
				t.Fatalf("message=%q err=%v upgraded=%t bundle=%+v read=%v", message, err, upgraded, info, readErr)
			}
			if tc.diverged {
				fetch := "git -C " + runner.tapRepo + " fetch --quiet origin main"
				if !slices.Contains(runner.calls, fetch) {
					t.Fatalf("fetch did not follow the tracked branch: %v", runner.calls)
				}
				if got := gitOutput(t, runner.tapRepo, "rev-parse", "HEAD"); got != tapHead {
					t.Fatalf("diverged tap HEAD changed from %s to %s", tapHead, got)
				}
				body, readErr := os.ReadFile(filepath.Join(runner.tapRepo, "LOCAL"))
				if readErr != nil || string(body) != "keep me\n" {
					t.Fatalf("local tap edit: %q %v", body, readErr)
				}
			}
		})
	}
}

func divergedTapCheckout(t *testing.T) (string, string) {
	t.Helper()
	origin := t.TempDir()
	gitRun(t, origin, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "add", "README")
	gitRun(t, origin, "commit", "-m", "base")
	local := t.TempDir()
	cmd := exec.Command("git", "clone", origin, local)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(local, "LOCAL"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, local, "add", "LOCAL")
	gitRun(t, local, "commit", "-m", "local edit")
	head := gitOutput(t, local, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(origin, "README"), []byte("upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "add", "README")
	gitRun(t, origin, "commit", "-m", "upstream")
	return local, head
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitOutput(t, dir, args...)
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.email=updater@localhost", "-c", "user.name=Updater"}
	cmd := exec.Command("git", append(base, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCleanStaleSelfStages(t *testing.T) {
	t.Run("signed app drops backups and leftovers", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		parent := t.TempDir()
		bundle := filepath.Join(parent, "Updater.app")
		writeSelfBundle(t, bundle, "2.0.0")
		writeStageFile(t, parent, ".updater-self-843450262", "")
		writeStageFile(t, parent, ".updater-self-3414085168", "release-old.tar.gz")
		writeSelfBundle(t, filepath.Join(parent, ".updater-self-prev", "Previous.app"), "1.0.0")
		busy := filepath.Join(parent, ".updater-self-busy")
		if err := os.MkdirAll(busy, 0o755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(busy, selfStageLockName)
		if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		holder := exec.Command(os.Args[0], "-test.run=^$")
		holder.Env = append(os.Environ(), "UPDATER_STAGE_HOLD="+lockPath)
		holderOut, err := holder.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}()
		if line, err := bufio.NewReader(holderOut).ReadString('\n'); err != nil || line != "held\n" {
			t.Fatalf("stage holder: %q %v", line, err)
		}
		restored, err := recoverStrandedUpdater(context.Background(), bundle, &recoverRunner{signed: true})
		if err != nil || restored {
			t.Fatalf("restored=%t err=%v", restored, err)
		}
		info, readErr := readUpdaterBundle(bundle)
		if readErr != nil || info.Version != "2.0.0" {
			t.Fatalf("bundle: %+v, %v", info, readErr)
		}
		if _, statErr := os.Stat(busy); statErr != nil {
			t.Fatalf("in-use stage was removed: %v", statErr)
		}
		for _, name := range []string{".updater-self-843450262", ".updater-self-3414085168", ".updater-self-prev"} {
			if _, statErr := os.Lstat(filepath.Join(parent, name)); !os.IsNotExist(statErr) {
				t.Fatalf("%s still present: %v", name, statErr)
			}
		}
	})

	t.Run("missing app keeps a restorable backup and drops junk", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		parent := t.TempDir()
		bundle := filepath.Join(parent, "Updater.app")
		writeStageFile(t, parent, ".updater-self-843450262", "")
		writeStageFile(t, parent, ".updater-self-3414085168", "release-old.tar.gz")
		if err := os.MkdirAll(filepath.Join(parent, ".updater-self-bogus", "Previous.app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, ".updater-self-bogus", "Previous.app", "note"), []byte("no"), 0o644); err != nil {
			t.Fatal(err)
		}
		older := filepath.Join(parent, ".updater-self-old", "Previous.app")
		newer := filepath.Join(parent, ".updater-self-new", "Previous.app")
		writeSelfBundle(t, older, "1.0.0")
		writeSelfBundle(t, newer, "1.1.0")
		oldTime := time.Now().Add(-2 * time.Hour)
		newTime := time.Now().Add(-time.Hour)
		if err := os.Chtimes(older, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(newer, newTime, newTime); err != nil {
			t.Fatal(err)
		}
		restored, err := recoverStrandedUpdater(context.Background(), bundle, &recoverRunner{signed: false})
		if err != nil || !restored {
			t.Fatalf("restored=%t err=%v", restored, err)
		}
		info, readErr := readUpdaterBundle(bundle)
		if readErr != nil || info.Version != "1.1.0" {
			t.Fatalf("bundle: %+v, %v", info, readErr)
		}
		kept, readErr := readUpdaterBundle(older)
		if readErr != nil || kept.Version != "1.0.0" {
			t.Fatalf("older backup: %+v, %v", kept, readErr)
		}
		for _, name := range []string{".updater-self-843450262", ".updater-self-3414085168", ".updater-self-bogus", ".updater-self-new"} {
			if _, statErr := os.Lstat(filepath.Join(parent, name)); !os.IsNotExist(statErr) {
				t.Fatalf("%s still present: %v", name, statErr)
			}
		}
	})

	t.Run("unsigned app is restored and junk is removed", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		parent := t.TempDir()
		bundle := filepath.Join(parent, "Updater.app")
		writeSelfBundle(t, bundle, "9.0.0")
		writeStageFile(t, parent, ".updater-self-3414085168", "release-old.tar.gz")
		writeSelfBundle(t, filepath.Join(parent, ".updater-self-prev", "Previous.app"), "1.0.0")
		restored, err := recoverStrandedUpdater(context.Background(), bundle, &recoverRunner{signed: false})
		if err != nil || !restored {
			t.Fatalf("restored=%t err=%v", restored, err)
		}
		info, readErr := readUpdaterBundle(bundle)
		if readErr != nil || info.Version != "1.0.0" {
			t.Fatalf("bundle: %+v, %v", info, readErr)
		}
		if _, statErr := os.Lstat(filepath.Join(parent, ".updater-self-3414085168")); !os.IsNotExist(statErr) {
			t.Fatalf("tarball stage still present: %v", statErr)
		}
	})

	t.Run("self-upgrade removes leftovers before an up-to-date return", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		parent := t.TempDir()
		bundle := filepath.Join(parent, "Updater.app")
		writeSelfBundle(t, bundle, "1.0.0")
		writeStageFile(t, parent, ".updater-self-843450262", "")
		writeStageFile(t, parent, ".updater-self-3414085168", "release-old.tar.gz")
		oldRelease := selfUpgradeRelease
		t.Cleanup(func() { selfUpgradeRelease = oldRelease })
		selfUpgradeRelease = func(string) (*checker.GitHubRelease, error) {
			return &checker.GitHubRelease{TagName: "v1.0.0"}, nil
		}
		message, err := applySelfUpgrade(context.Background(), bundle, &selfUpgradeTestRunner{t: t, bundle: bundle})
		if err != nil || message != "Updater is already up to date (1.0.0)" {
			t.Fatalf("message=%q err=%v", message, err)
		}
		for _, name := range []string{".updater-self-843450262", ".updater-self-3414085168"} {
			if _, statErr := os.Lstat(filepath.Join(parent, name)); !os.IsNotExist(statErr) {
				t.Fatalf("%s still present: %v", name, statErr)
			}
		}
	})
}

func writeStageFile(t *testing.T, parent, dir, name string) {
	t.Helper()
	path := filepath.Join(parent, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if name == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelfUpgradeArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, header := range []*tar.Header{
		{Name: "Updater.app/../outside", Typeflag: tar.TypeReg},
		{Name: "/Updater.app/Contents/Info.plist", Typeflag: tar.TypeReg},
		{Name: "Updater.app/Contents/MacOS/updater", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
		{Name: "Updater.app/Contents/MacOS/updater", Typeflag: tar.TypeLink, Linkname: "outside"},
		{Name: "Updater.app/large", Typeflag: tar.TypeReg, Size: maxSelfUpgradeBinarySize + 1},
	} {
		t.Run(header.Name+fmt.Sprint(header.Typeflag), func(t *testing.T) {
			var data bytes.Buffer
			gz := gzip.NewWriter(&data)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			_ = tw.Close()
			_ = gz.Close()
			path := filepath.Join(t.TempDir(), "release.tar.gz")
			if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractUpdaterApp(path, t.TempDir()); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}

func TestSelfUpdateRoutesThroughHelper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldStart, oldRunner := startSelfUpgrade, newRunner
	t.Cleanup(func() { startSelfUpgrade, newRunner = oldStart, oldRunner })
	var targets []string
	startSelfUpgrade = func(_ context.Context, target string) error { targets = append(targets, target); return nil }
	runner := &yoloRunner{}
	newRunner = func() checker.CmdRunner { return runner }
	r := &checker.UpdateResult{App: &app.App{Name: "Updater", BundleID: app.UpdaterBundleID, Path: "/Applications/Updater.app"}, Source: "github", HasUpdate: true}
	if err, _ := executeUpdate(context.Background(), r, runner, nil, nil); !errors.Is(err, checker.ErrUpdateScheduled) {
		t.Fatal(err)
	}
	updated, failed := autoUpdateAfterNotify(context.Background(), &config.Config{YOLOMode: true}, []*checker.UpdateResult{r})
	if len(updated)+len(failed) != 0 || !r.HasUpdate || len(targets) != 2 || len(runner.calls) != 0 {
		t.Fatalf("incorrect queued result: updated %v, failed %v, targets %v, commands %v", updated, failed, targets, runner.calls)
	}
	entries, err := history.List(history.DefaultPath())
	if err != nil || len(entries) != 0 {
		t.Fatalf("recorded success before helper completed: %v, %v", entries, err)
	}
}

func TestSelfUpgradeReleaseArtifact(t *testing.T) {
	archive := os.Getenv("UPDATER_TEST_RELEASE_ARCHIVE")
	if archive == "" {
		t.Skip("set UPDATER_TEST_RELEASE_ARCHIVE to a published release tarball")
	}
	dir := t.TempDir()
	if err := extractUpdaterApp(archive, dir); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "Updater.app")
	if err := verifySelfUpgrade(context.Background(), bundle, bundle); err != nil {
		t.Fatal(err)
	}
}
