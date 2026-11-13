package main

import (
	"archive/tar"
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
	caskVersion                  string
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
	os.Exit(m.Run())
}

func probeUpdateLockHeld(t *testing.T) {
	t.Helper()
	path := filepath.Join(filepath.Dir(config.DefaultPath()), "update.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append([]string{"UPDATER_LOCK_PROBE=" + path}, os.Environ()...)
	if err := cmd.Run(); err != nil {
		t.Fatalf("update.lock was not held across brew: %v", err)
	}
}

func (r *selfUpgradeTestRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, key)
	switch {
	case key == "brew list --cask "+updaterCask:
		if r.brew {
			return []byte(r.bundle + "\n"), nil
		}
		return nil, errors.New("not installed via brew")
	case strings.HasPrefix(key, "brew info --cask --json=v2 "):
		version := r.caskVersion
		if version == "" {
			version = "2.0.0"
		}
		return []byte(`{"casks":[{"token":"updater","version":"` + version + `"}]}`), nil
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
			_, err := applySelfUpgrade(context.Background(), bundle, runner)
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
			if err == nil || upgraded || readErr != nil || info.Version != tc.installed {
				t.Fatalf("err=%v upgraded=%t bundle=%+v read=%v", err, upgraded, info, readErr)
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
