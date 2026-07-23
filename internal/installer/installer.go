package installer

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/signing"
)

const maxDownloadSize int64 = 4 << 30 // 4 GiB

type artifactVerifier interface {
	VerifyReplacementApp(context.Context, string, string) error
	VerifyInstallerPackage(context.Context, string, string) error
}

type installError struct {
	err             error
	mayHaveModified bool
}

func (e *installError) Error() string { return e.err.Error() }
func (e *installError) Unwrap() error { return e.err }

// MayRequireRollback reports whether a failed install reached a point where
// the existing app may have been modified. Download, extraction, and security
// verification failures return false and must not touch a healthy app.
func MayRequireRollback(err error) bool {
	var installErr *installError
	return errors.As(err, &installErr) && installErr.mayHaveModified
}

// Installer downloads and installs macOS app updates.
type Installer struct {
	runner   checker.CmdRunner
	client   *http.Client
	verifier artifactVerifier
}

// New creates a new Installer.
func New(runner checker.CmdRunner, client *http.Client) *Installer {
	client = secureDownloadClientWith(client)
	return &Installer{runner: runner, client: client, verifier: signing.NewVerifier()}
}

// Install downloads the update from downloadURL and installs it, replacing the
// app at appPath. The format is detected from the URL or Content-Disposition header.
func (inst *Installer) Install(ctx context.Context, downloadURL, appPath, appName, expectedDigest string) error {
	tmpDir, err := os.MkdirTemp("", "updater-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localPath, err := inst.download(ctx, downloadURL, tmpDir, expectedDigest)
	if err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(localPath))
	switch ext {
	case ".dmg":
		return inst.installDMG(ctx, localPath, appPath, appName)
	case ".zip":
		return inst.installZIP(ctx, localPath, appPath, appName)
	case ".pkg":
		return inst.installPKG(ctx, localPath, appPath)
	default:
		return fmt.Errorf("unsupported file format: %s", ext)
	}
}

// download fetches a URL to a local file in destDir.
// Returns the local file path. Uses Content-Disposition for filename if available.
func (inst *Installer) download(ctx context.Context, rawURL, destDir, expectedDigest string) (string, error) {
	if err := validateSecureURL(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := inst.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: status %d", resp.StatusCode)
	}
	if err := validateSecureURL(resp.Request.URL.String()); err != nil {
		return "", fmt.Errorf("download redirected to an insecure URL: %w", err)
	}
	if resp.ContentLength > maxDownloadSize {
		return "", fmt.Errorf("download is too large: %d bytes exceeds %d-byte limit", resp.ContentLength, maxDownloadSize)
	}

	// Determine filename from Content-Disposition or URL path. Use the final
	// (post-redirect) URL: extension-less endpoints like a cask's
	// "/download/latest" often redirect to the actual artifact name.
	filename, err := filenameFromResponse(resp, resp.Request.URL.String())
	if err != nil {
		return "", err
	}
	localPath := filepath.Join(destDir, filename)
	if err := ensureContainedPath(destDir, localPath); err != nil {
		return "", err
	}

	f, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	hasher, err := digestHasher(expectedDigest)
	if err != nil {
		_ = os.Remove(localPath)
		return "", err
	}
	var writer io.Writer = f
	if hasher != nil {
		writer = io.MultiWriter(f, hasher)
	}
	written, err := io.Copy(writer, io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		_ = os.Remove(localPath)
		return "", fmt.Errorf("failed to write download: %w", err)
	}
	if written > maxDownloadSize {
		_ = os.Remove(localPath)
		return "", fmt.Errorf("download exceeds %d-byte limit", maxDownloadSize)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(localPath)
		return "", fmt.Errorf("failed to close download: %w", err)
	}
	if err := verifyDigest(expectedDigest, hasher); err != nil {
		_ = os.Remove(localPath)
		return "", err
	}

	return localPath, nil
}

// filenameFromResponse extracts the filename from the response headers or URL.
func filenameFromResponse(resp *http.Response, rawURL string) (string, error) {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		_, params, err := mime.ParseMediaType(cd)
		if err != nil {
			return "", fmt.Errorf("invalid Content-Disposition header: %w", err)
		}
		if name := params["filename"]; name != "" {
			if err := validateFilename(name); err != nil {
				return "", err
			}
			return name, nil
		}
	}
	// Fallback to URL path.
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}
	name := filepath.Base(parsed.Path)
	if name != "." && name != "/" && name != "" {
		if err := validateFilename(name); err != nil {
			return "", err
		}
		return name, nil
	}
	return "download", nil
}

// installDMG mounts a DMG, copies the .app to the target location, and detaches.
func (inst *Installer) installDMG(ctx context.Context, dmgPath, appPath, appName string) error {
	// Mount DMG silently.
	output, err := inst.runner.Run(ctx, "hdiutil", "attach", "-nobrowse", "-plist", dmgPath)
	if err != nil {
		return fmt.Errorf("failed to mount DMG: %w", err)
	}

	// Find mount point from output (look for /Volumes/).
	mountPoint := extractMountPoint(string(output))
	if mountPoint == "" {
		return fmt.Errorf("failed to find mount point in hdiutil output")
	}
	defer func() {
		_, _ = inst.runner.Run(ctx, "hdiutil", "detach", mountPoint, "-quiet")
	}()

	// Find the .app bundle in the mounted volume.
	appBundle := findAppInVolume(ctx, inst.runner, mountPoint, appName)
	if appBundle == "" {
		return fmt.Errorf("no .app bundle found in DMG for %s", appName)
	}

	if err := inst.verifier.VerifyReplacementApp(ctx, appPath, appBundle); err != nil {
		return fmt.Errorf("downloaded app failed security verification: %w", err)
	}

	return inst.replaceApp(ctx, appBundle, appPath)
}

// replaceApp swaps the app at appPath with srcBundle via a staged copy.
// A plain `cp -a src dst` would copy src INTO dst when dst already exists
// (nesting the new bundle inside the old one), so stage next to the target
// on the same volume, remove the old bundle, and rename into place.
func (inst *Installer) replaceApp(ctx context.Context, srcBundle, appPath string) error {
	staging := appPath + ".updater-staging"
	_, _ = inst.runner.Run(ctx, "rm", "-rf", staging)

	if _, err := inst.runner.Run(ctx, "cp", "-a", srcBundle, staging); err != nil {
		_, _ = inst.runner.Run(ctx, "rm", "-rf", staging)
		return fmt.Errorf("failed to stage new app: %w", err)
	}
	if _, err := inst.runner.Run(ctx, "rm", "-rf", appPath); err != nil {
		_, _ = inst.runner.Run(ctx, "rm", "-rf", staging)
		return &installError{err: fmt.Errorf("failed to remove old app: %w", err), mayHaveModified: true}
	}
	if _, err := inst.runner.Run(ctx, "mv", staging, appPath); err != nil {
		return &installError{err: fmt.Errorf("failed to move new app into place: %w", err), mayHaveModified: true}
	}
	return nil
}

// extractMountPoint finds the /Volumes/... path in hdiutil output.
func extractMountPoint(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "/Volumes/") {
			return trimmed
		}
		// Also check inside plist string tags.
		if strings.Contains(trimmed, "/Volumes/") {
			start := strings.Index(trimmed, "/Volumes/")
			end := strings.Index(trimmed[start:], "<")
			if end > 0 {
				return trimmed[start : start+end]
			}
			return trimmed[start:]
		}
	}
	return ""
}

// findAppInVolume searches for a .app bundle in the mounted volume.
// Prefers matching by appName, falls back to first .app found.
func findAppInVolume(ctx context.Context, runner checker.CmdRunner, mountPoint, appName string) string {
	output, err := runner.Run(ctx, "ls", mountPoint)
	if err != nil {
		return ""
	}

	var firstApp string
	for _, entry := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		entry = strings.TrimSpace(entry)
		if !strings.HasSuffix(entry, ".app") {
			continue
		}
		fullPath := filepath.Join(mountPoint, entry)
		if firstApp == "" {
			firstApp = fullPath
		}
		// Prefer matching app name.
		base := strings.TrimSuffix(entry, ".app")
		if strings.EqualFold(base, appName) {
			return fullPath
		}
	}
	return firstApp
}

// installZIP extracts a ZIP and copies the .app to the target location.
func (inst *Installer) installZIP(ctx context.Context, zipPath, appPath, appName string) error {
	tmpDir, err := os.MkdirTemp("", "updater-zip-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract using ditto (preserves resource forks and code signatures).
	_, err = inst.runner.Run(ctx, "ditto", "-xk", zipPath, tmpDir)
	if err != nil {
		return fmt.Errorf("failed to extract ZIP: %w", err)
	}

	// Find .app in extracted contents.
	appBundle := findAppInVolume(ctx, inst.runner, tmpDir, appName)
	if appBundle == "" {
		return fmt.Errorf("no .app bundle found in ZIP for %s", appName)
	}

	if err := inst.verifier.VerifyReplacementApp(ctx, appPath, appBundle); err != nil {
		return fmt.Errorf("downloaded app failed security verification: %w", err)
	}

	return inst.replaceApp(ctx, appBundle, appPath)
}

// installPKG runs the macOS package installer (requires sudo).
func (inst *Installer) installPKG(ctx context.Context, pkgPath, appPath string) error {
	if err := inst.verifier.VerifyInstallerPackage(ctx, appPath, pkgPath); err != nil {
		return fmt.Errorf("downloaded package failed security verification: %w", err)
	}
	_, err := inst.runner.Run(ctx, "sudo", "installer", "-pkg", pkgPath, "-target", "/")
	if err != nil {
		return &installError{err: fmt.Errorf("failed to install PKG: %w", err), mayHaveModified: true}
	}
	return nil
}

func secureDownloadClientWith(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = 30 * time.Minute
	}
	previousRedirectPolicy := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if previousRedirectPolicy != nil {
			if err := previousRedirectPolicy(req, via); err != nil {
				return err
			}
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return validateSecureURL(req.URL.String())
	}
	return &clone
}

func validateSecureURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme == "https" && parsed.Hostname() != "" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("URL must use HTTPS")
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateFilename(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("unsafe download filename %q", name)
	}
	return nil
}

func ensureContainedPath(parent, child string) error {
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return fmt.Errorf("failed to resolve download directory: %w", err)
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return fmt.Errorf("failed to resolve download path: %w", err)
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("download path escapes temporary directory")
	}
	return nil
}

func digestHasher(expected string) (hash.Hash, error) {
	if expected == "" {
		return nil, nil
	}
	algorithm, _, ok := strings.Cut(expected, ":")
	if !ok {
		return nil, fmt.Errorf("invalid expected digest format")
	}
	switch strings.ToLower(algorithm) {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
}

func verifyDigest(expected string, hasher hash.Hash) error {
	if expected == "" {
		return nil
	}
	algorithm, value, _ := strings.Cut(expected, ":")
	var actual string
	switch strings.ToLower(algorithm) {
	case "sha256":
		actual = hex.EncodeToString(hasher.Sum(nil))
	case "sha512":
		actual = base64.StdEncoding.EncodeToString(hasher.Sum(nil))
	}
	if subtle.ConstantTimeCompare([]byte(actual), []byte(value)) != 1 {
		return fmt.Errorf("download digest mismatch")
	}
	return nil
}
