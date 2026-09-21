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
	"path/filepath"
	"slices"
	"strings"
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
	calls                        []string
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
	case name == "/usr/bin/ditto":
		return nil, os.CopyFS(args[1], os.DirFS(args[0]))
	case key == "brew upgrade --cask "+updaterCask:
		writeSelfBundle(r.t, r.bundle, "2.0.0")
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
	if err, rolledBack := replaceUpdaterBundle(old, candidate, backup); err == nil || !rolledBack {
		t.Fatalf("failed swap did not roll back: %v, %t", err, rolledBack)
	}
	if info, err := readUpdaterBundle(old); err != nil || info.Version != "1.0.0" {
		t.Fatalf("lost previous app: %+v, %v", info, err)
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
