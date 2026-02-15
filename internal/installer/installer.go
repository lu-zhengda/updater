package installer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lu-zhengda/updater/internal/checker"
)

// Installer downloads and installs macOS app updates.
type Installer struct {
	runner checker.CmdRunner
	client *http.Client
}

// New creates a new Installer.
func New(runner checker.CmdRunner, client *http.Client) *Installer {
	if client == nil {
		client = http.DefaultClient
	}
	return &Installer{runner: runner, client: client}
}

// Install downloads the update from downloadURL and installs it, replacing the
// app at appPath. The format is detected from the URL or Content-Disposition header.
func (inst *Installer) Install(ctx context.Context, downloadURL, appPath, appName string) error {
	tmpDir, err := os.MkdirTemp("", "updater-install-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localPath, err := inst.download(ctx, downloadURL, tmpDir)
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
		return inst.installPKG(ctx, localPath)
	default:
		return fmt.Errorf("unsupported file format: %s", ext)
	}
}

// download fetches a URL to a local file in destDir.
// Returns the local file path. Uses Content-Disposition for filename if available.
func (inst *Installer) download(ctx context.Context, url, destDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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

	// Determine filename from Content-Disposition or URL path.
	filename := filenameFromResponse(resp, url)
	localPath := filepath.Join(destDir, filename)

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("failed to write download: %w", err)
	}

	return localPath, nil
}

// filenameFromResponse extracts the filename from the response headers or URL.
func filenameFromResponse(resp *http.Response, url string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		// Parse: attachment; filename="App-1.0.dmg"
		for _, part := range strings.Split(cd, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "filename=") {
				name := strings.TrimPrefix(part, "filename=")
				name = strings.Trim(name, `"`)
				if name != "" {
					return name
				}
			}
		}
	}
	// Fallback to URL path.
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		// Strip query parameters.
		if idx := strings.Index(name, "?"); idx >= 0 {
			name = name[:idx]
		}
		if name != "" {
			return name
		}
	}
	return "download"
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

	// Remove quarantine attribute.
	_, _ = inst.runner.Run(ctx, "xattr", "-rd", "com.apple.quarantine", appBundle)

	// Copy app to target location (replace existing).
	appDir := filepath.Dir(appPath)
	appBase := filepath.Base(appPath)
	_, err = inst.runner.Run(ctx, "cp", "-a", appBundle, filepath.Join(appDir, appBase))
	if err != nil {
		return fmt.Errorf("failed to copy app: %w", err)
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

	// Remove quarantine attribute.
	_, _ = inst.runner.Run(ctx, "xattr", "-rd", "com.apple.quarantine", appBundle)

	// Copy to target.
	_, err = inst.runner.Run(ctx, "cp", "-a", appBundle, appPath)
	if err != nil {
		return fmt.Errorf("failed to copy app: %w", err)
	}

	return nil
}

// installPKG runs the macOS package installer (requires sudo).
func (inst *Installer) installPKG(ctx context.Context, pkgPath string) error {
	_, err := inst.runner.Run(ctx, "sudo", "installer", "-pkg", pkgPath, "-target", "/")
	if err != nil {
		return fmt.Errorf("failed to install PKG: %w", err)
	}
	return nil
}
