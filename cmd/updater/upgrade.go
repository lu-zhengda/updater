package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/luzhengda/updater/internal/checker"
	versionpkg "github.com/luzhengda/updater/internal/version"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Update the updater itself to the latest version",
	RunE:  runUpgrade,
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	// Detect current binary path.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	// Detect Homebrew installation.
	if isBrewInstall(execPath) {
		fmt.Fprintln(cmd.OutOrStdout(), "Installed via Homebrew. Run: brew upgrade lu-zhengda/tap/updater")
		return nil
	}

	// Fetch latest release from GitHub.
	token := os.Getenv("GITHUB_TOKEN")
	release, err := fetchLatestRelease(token)
	if err != nil {
		return err
	}

	latestVersion := checker.CleanTagVersion(release.TagName)

	if !versionpkg.IsNewer(version, latestVersion) {
		fmt.Fprintf(cmd.OutOrStdout(), "Already up to date (%s)\n", version)
		return nil
	}

	// Find matching asset for current architecture.
	assetName := "updater-darwin-" + runtime.GOARCH
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.DownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no matching asset %q in release %s", assetName, release.TagName)
	}

	// Download to temp file in same directory (ensures same filesystem for atomic rename).
	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, "updater-upgrade-*")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s (try sudo)", dir)
		}
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // clean up on failure

	if err := downloadFile(tmpFile, downloadURL, token); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	// Make executable.
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("failed to chmod: %w", err)
	}

	// Atomic replace.
	if err := os.Rename(tmpPath, execPath); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied replacing %s (try sudo)", execPath)
		}
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated updater: %s → %s\n", version, latestVersion)
	return nil
}

// isBrewInstall returns true if the binary path is inside a Homebrew prefix.
func isBrewInstall(path string) bool {
	return strings.HasPrefix(path, "/opt/homebrew/") ||
		strings.HasPrefix(path, "/usr/local/Cellar/") ||
		strings.HasPrefix(path, "/home/linuxbrew/")
}

// fetchLatestRelease fetches the latest release from the updater's GitHub repo.
func fetchLatestRelease(token string) (*checker.GitHubRelease, error) {
	return fetchLatestReleaseFrom(
		"https://api.github.com/repos/lu-zhengda/updater/releases/latest",
		token,
		http.DefaultClient,
	)
}

// fetchLatestReleaseFrom fetches a GitHub release from the given URL using the
// provided token and HTTP client. Extracted for testability.
func fetchLatestReleaseFrom(url, token string, client *http.Client) (*checker.GitHubRelease, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API rate limited (status %d); set GITHUB_TOKEN to increase limits", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch latest release: status %d", resp.StatusCode)
	}

	var release checker.GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}
	return &release, nil
}

// downloadFile downloads the given URL into the provided file.
func downloadFile(dst *os.File, url, token string) error {
	return downloadFileWith(dst, url, token, http.DefaultClient)
}

// downloadFileWith downloads the given URL into the provided file using the
// specified HTTP client. Extracted for testability.
func downloadFileWith(dst *os.File, url, token string, client *http.Client) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download asset: status %d", resp.StatusCode)
	}

	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("failed to write downloaded binary: %w", err)
	}
	return nil
}
