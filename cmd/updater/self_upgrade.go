package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/lu-zhengda/updater/internal/history"
	"github.com/lu-zhengda/updater/internal/signing"
	versionpkg "github.com/lu-zhengda/updater/internal/version"
	"github.com/spf13/cobra"
	"howett.net/plist"
)

const updaterCask = "lu-zhengda/tap/updater"

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "self-upgrade <app-path>", Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if self, err := os.Executable(); err == nil {
				dir := filepath.Dir(self)
				if bundle := updaterBundlePath(self); bundle != "" {
					dir = filepath.Dir(bundle)
				}
				if filepath.Dir(dir) == filepath.Clean(os.TempDir()) && strings.HasPrefix(filepath.Base(dir), "updater-self-helper-") {
					defer os.RemoveAll(dir)
				}
			}
			ensurePath()
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
			defer cancel()
			runner := newRunner()
			message, err := applySelfUpgrade(ctx, args[0], runner)
			if err != nil {
				message = "Self-update failed: " + err.Error()
			}
			fmt.Fprintln(os.Stderr, message)
			script := fmt.Sprintf(`display notification "%s" with title "Updater"`, escapeAppleScript(message))
			_, _ = runner.Run(context.Background(), "osascript", "-e", script)
			return err
		},
	})
}

func updaterBundlePath(executable string) string {
	if bundle, ok := strings.CutSuffix(executable, "/Contents/MacOS/updater"); ok && strings.HasSuffix(bundle, ".app") {
		return bundle
	}
	return ""
}

// A private copy keeps the helper alive when Homebrew replaces the app. Copy
// the whole bundle to preserve the executable's sealed Info.plist/resources.
// The helper has its own session, context, and log.
var startSelfUpgrade = func(ctx context.Context, bundle string) error {
	if _, err := readUpdaterBundle(bundle); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "updater-self-helper-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	helper := filepath.Join(dir, "updater")
	source, destination := self, helper
	if bundle := updaterBundlePath(self); bundle != "" {
		source, destination = bundle, filepath.Join(dir, "Updater.app")
		helper = filepath.Join(destination, "Contents", "MacOS", "updater")
	}
	if _, err = newRunner().Run(ctx, "/usr/bin/ditto", source, destination); err != nil {
		return fmt.Errorf("failed to copy self-update helper: %w", err)
	}
	logDir := filepath.Join(filepath.Dir(config.DefaultPath()), "logs")
	if err = os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(logDir, "self-update.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(helper, "self-upgrade", bundle)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("failed to start self-update helper: %w", err)
	}
	go func() { _ = cmd.Wait(); _ = os.RemoveAll(dir) }()
	return nil
}

func readUpdaterBundle(bundle string) (*app.App, error) {
	if !filepath.IsAbs(bundle) || !strings.HasSuffix(bundle, ".app") {
		return nil, fmt.Errorf("invalid Updater app path %q", bundle)
	}
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return nil, err
	}
	var info struct {
		ID         string `plist:"CFBundleIdentifier"`
		Version    string `plist:"CFBundleShortVersionString"`
		Executable string `plist:"CFBundleExecutable"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.ID != app.UpdaterBundleID || info.Executable != "updater" || info.Version == "" {
		return nil, fmt.Errorf("%s is not an Updater app bundle", bundle)
	}
	return &app.App{Name: "Updater", BundleID: info.ID, Version: info.Version, Path: bundle}, nil
}

// Homebrew's Caskroom app is a symlink to the actual appdir (which need not be
// /Applications). Resolving it avoids mistaking another Updater copy for it.
func isUpdaterCask(ctx context.Context, bundle string, runner checker.CmdRunner) bool {
	output, err := runner.Run(ctx, "brew", "list", "--cask", updaterCask)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		path := strings.TrimSpace(line)
		if !strings.HasSuffix(path, "/Updater.app") {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil && resolved == target {
			return true
		}
	}
	return false
}

// Shared for an update/batch; exclusive while replacing Updater and restarting
// its UI. flock also covers separate menu bar, window, and scheduled processes.
// ponytail: one activity lock for all apps; split by app if waits become costly.
func updateActivityLock(ctx context.Context, exclusive bool) (*os.File, error) {
	dir := filepath.Dir(config.DefaultPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

var selfUpgradeRelease = fetchLatestRelease
var verifySelfUpgrade = func(ctx context.Context, installed, candidate string) error {
	return signing.NewVerifier().VerifyReplacementApp(ctx, installed, candidate)
}

func applySelfUpgrade(ctx context.Context, bundle string, runner checker.CmdRunner) (message string, err error) {
	installed, err := readUpdaterBundle(bundle)
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return "", err
	}
	release, err := selfUpgradeRelease(cfg.ResolveGitHubToken())
	if err != nil {
		return "", err
	}
	latest := checker.CleanTagVersion(release.TagName)
	if !versionpkg.IsNewer(versionpkg.ReleaseVersion(installed.Version), latest) {
		return "Updater is already up to date (" + installed.Version + ")", nil
	}

	stage, err := os.MkdirTemp(filepath.Dir(bundle), ".updater-self-*")
	if err != nil {
		return "", fmt.Errorf("cannot stage self-update beside app: %w", err)
	}
	// Keep the old app only if rollback itself fails.
	keepRecovery := false
	defer func() {
		if !keepRecovery {
			_ = os.RemoveAll(stage)
		}
	}()
	brew := isUpdaterCask(ctx, bundle, runner)
	candidate := filepath.Join(stage, "Updater.app")
	if !brew {
		archive, downloadErr := downloadSelfArchive(release, cfg.ResolveGitHubToken(), stage)
		if downloadErr != nil {
			return "", downloadErr
		}
		if err = extractUpdaterApp(archive, stage); err != nil {
			return "", err
		}
		info, readErr := readUpdaterBundle(candidate)
		if readErr != nil {
			return "", readErr
		}
		if info.Version != latest {
			return "", fmt.Errorf("downloaded bundle version %s does not match release %s", info.Version, latest)
		}
	}

	lock, err := updateActivityLock(ctx, true)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	// A second helper may have finished while this one was downloading.
	installed, err = readUpdaterBundle(bundle)
	if err != nil {
		return "", err
	}
	if !versionpkg.IsNewer(versionpkg.ReleaseVersion(installed.Version), latest) {
		return "Updater is already up to date (" + installed.Version + ")", nil
	}
	if !brew {
		if err = verifySelfUpgrade(ctx, bundle, candidate); err != nil {
			return "", err
		}
	}
	backup := filepath.Join(stage, "Previous.app")
	if brew {
		if _, err = runner.Run(ctx, "/usr/bin/ditto", bundle, backup); err != nil {
			return "", err
		}
	}
	// Suspend the matching login agent so it cannot respawn midway through a swap.
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	service := domain + "/" + menubarPlistLabel
	executable := filepath.Join(bundle, "Contents", "MacOS", "updater")
	output, _ := runner.Run(ctx, "launchctl", "print", service)
	managed := strings.Contains(string(output), "program = "+executable+"\n")
	if managed {
		if _, err = runner.Run(ctx, "launchctl", "bootout", service); err != nil {
			return "", err
		}
	}
	defer func() {
		// Use a fresh context so a timed-out installation still restores the UI.
		restartCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if managed {
			path, pathErr := menubarPlistPath()
			if pathErr == nil {
				_, restartErr := runner.Run(restartCtx, "launchctl", "bootstrap", domain, path)
				if restartErr == nil {
					return
				}
			}
		}
		_, restartErr := runner.Run(restartCtx, "/usr/bin/open", bundle)
		if restartErr != nil {
			err = errors.Join(err, fmt.Errorf("could not restart Updater: %w", restartErr))
		}
	}()
	if err = stopUpdaterUI(ctx, executable, runner); err != nil {
		return "", err
	}
	rolledBack := false
	defer func() {
		_ = history.Append(history.DefaultPath(), history.Entry{
			AppName: "Updater", BundleID: app.UpdaterBundleID, FromVersion: installed.Version,
			ToVersion: latest, Source: "self", Timestamp: time.Now(), Success: err == nil, RolledBack: rolledBack,
		})
	}()
	if brew {
		_, err = runner.Run(ctx, "brew", "upgrade", "--cask", updaterCask)
		if err == nil {
			var updated *app.App
			updated, err = readUpdaterBundle(bundle)
			if err == nil && !versionpkg.IsNewer(versionpkg.ReleaseVersion(installed.Version), updated.Version) {
				err = fmt.Errorf("Homebrew has not installed a newer Updater release yet")
			}
			if err == nil {
				err = verifySelfUpgrade(ctx, backup, bundle)
			}
		}
		if err != nil {
			// Preserve any failed installation before restoring the complete old app.
			if _, statErr := os.Lstat(bundle); statErr == nil {
				if moveErr := os.Rename(bundle, filepath.Join(stage, "Failed.app")); moveErr != nil {
					keepRecovery = true
					return "", errors.Join(err, fmt.Errorf("recovery copy remains at %s: %w", backup, moveErr))
				}
			}
			restoreErr := os.Rename(backup, bundle)
			rolledBack = restoreErr == nil
			keepRecovery = !rolledBack
			return "", errors.Join(err, restoreErr)
		}
	} else {
		err, rolledBack = replaceUpdaterBundle(bundle, candidate, backup)
		if err != nil {
			_, backupErr := os.Stat(backup)
			keepRecovery = backupErr == nil
			return "", err
		}
	}
	return "Updated Updater to " + latest, nil
}

func replaceUpdaterBundle(bundle, candidate, backup string) (error, bool) {
	if err := os.Rename(bundle, backup); err != nil {
		return err, false
	}
	if err := os.Rename(candidate, bundle); err != nil {
		restoreErr := os.Rename(backup, bundle)
		return errors.Join(err, restoreErr), restoreErr == nil
	}
	return nil, false
}

// Match executable plus UI arguments, never a substring of the helper's argv.
// CLI/TUI jobs can finish using their mapped executable after the bundle swap.
func updaterUIPIDs(output, executable string) []string {
	var pids []string
	for _, line := range strings.Split(output, "\n") {
		pid, command, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(pid)
		if err != nil || n <= 1 {
			continue
		}
		command = strings.TrimSpace(command)
		if command == executable || command == executable+" menubar run" || command == executable+" window" {
			pids = append(pids, pid)
		}
	}
	return pids
}

func stopUpdaterUI(ctx context.Context, executable string, runner checker.CmdRunner) error {
	for i := 0; i < 50; i++ {
		output, err := runner.Run(ctx, "/bin/ps", "-axo", "pid=,args=")
		if err != nil {
			return err
		}
		pids := updaterUIPIDs(string(output), executable)
		if len(pids) == 0 {
			return nil
		}
		if i == 0 {
			_, _ = runner.Run(ctx, "/bin/kill", append([]string{"-TERM"}, pids...)...)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("Updater is still running; close its windows and try again")
}

func downloadSelfArchive(release *checker.GitHubRelease, token, dir string) (string, error) {
	name := fmt.Sprintf("updater_%s_darwin.tar.gz", checker.CleanTagVersion(release.TagName))
	var archive, checksums checker.GitHubAsset
	for _, asset := range release.Assets {
		if asset.Name == name {
			archive = asset
		}
		if asset.Name == "checksums.txt" {
			checksums = asset
		}
	}
	if archive.DownloadURL == "" || checksums.DownloadURL == "" {
		return "", fmt.Errorf("release is missing %s or checksums.txt", name)
	}
	data, err := downloadBytes(checksums.DownloadURL, token, maxChecksumFileSize)
	if err != nil {
		return "", err
	}
	expected, err := checksumForAsset(data, name)
	if err != nil {
		return "", err
	}
	if archive.Digest != "" && archive.Digest != "sha256:"+expected {
		return "", fmt.Errorf("GitHub digest differs from checksums.txt")
	}
	f, err := os.CreateTemp(dir, "release-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer f.Close()
	// The caller owns a successful download; remove partial/invalid archives.
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(f.Name())
		}
	}()
	actual, err := downloadFileAndHash(f, archive.DownloadURL, token, maxSelfUpgradeArchiveSize)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("downloaded archive checksum mismatch")
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	complete = true
	return f.Name(), nil
}

// The release bundle has only directories and regular files. Reject links,
// traversal, duplicates and oversized entries before writing untrusted data.
func extractUpdaterApp(archivePath, dir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			_, err = readUpdaterBundle(filepath.Join(dir, "Updater.app"))
			return err
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if !filepath.IsLocal(name) || strings.Contains("/"+name+"/", "/../") {
			return fmt.Errorf("unsafe archive path %q", h.Name)
		}
		if name != "Updater.app" && !strings.HasPrefix(name, "Updater.app/") {
			continue
		}
		path := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > maxSelfUpgradeBinarySize || total > maxSelfUpgradeArchiveSize-h.Size {
				return fmt.Errorf("archive bundle is too large")
			}
			total += h.Size
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(out, tr, h.Size)
			closeErr := out.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %q", h.Name)
		}
	}
}
