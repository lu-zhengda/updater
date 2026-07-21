package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/signing"
	versionpkg "github.com/lu-zhengda/updater/internal/version"
	"github.com/spf13/cobra"
)

const (
	maxSelfUpgradeArchiveSize int64 = 512 << 20 // 512 MiB
	maxSelfUpgradeBinarySize  int64 = 256 << 20 // 256 MiB
	maxChecksumFileSize       int64 = 1 << 20   // 1 MiB
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Update the updater itself to the latest version",
	RunE:  runUpgrade,
}

var flagUpgradeJSON bool

func init() {
	upgradeCmd.Flags().BoolVar(&flagUpgradeJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	useJSON := jsonOutputEnabled(flagUpgradeJSON)
	ctx := cmd.Context()

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
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"status":      "homebrew_install",
				"instruction": "brew upgrade lu-zhengda/tap/updater",
			})
		}
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
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"status":          "up_to_date",
				"current_version": version,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Already up to date (%s)\n", version)
		return nil
	}

	// Releases contain one universal macOS archive. The checksum file is
	// downloaded separately, and the extracted executable must also match the
	// currently installed Developer ID identity.
	assetName := fmt.Sprintf("updater_%s_darwin.tar.gz", latestVersion)
	var archiveAsset, checksumAsset checker.GitHubAsset
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			archiveAsset = asset
		}
		if asset.Name == "checksums.txt" {
			checksumAsset = asset
		}
	}
	if archiveAsset.DownloadURL == "" {
		return fmt.Errorf("no matching asset %q in release %s", assetName, release.TagName)
	}
	if checksumAsset.DownloadURL == "" {
		return fmt.Errorf("release %s has no checksums.txt asset", release.TagName)
	}

	checksums, err := downloadBytes(checksumAsset.DownloadURL, token, maxChecksumFileSize)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}
	expectedSHA, err := checksumForAsset(checksums, assetName)
	if err != nil {
		return err
	}
	if archiveAsset.Digest != "" && archiveAsset.Digest != "sha256:"+expectedSHA {
		return fmt.Errorf("GitHub asset digest does not match checksums.txt")
	}

	dir := filepath.Dir(execPath)
	archiveFile, err := os.CreateTemp(dir, ".updater-upgrade-*.tar.gz")
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s (try sudo)", dir)
		}
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	archivePath := archiveFile.Name()
	defer os.Remove(archivePath)

	actualSHA, err := downloadFileAndHash(archiveFile, archiveAsset.DownloadURL, token, maxSelfUpgradeArchiveSize)
	if err != nil {
		archiveFile.Close()
		return err
	}
	if err := archiveFile.Close(); err != nil {
		return fmt.Errorf("failed to close downloaded archive: %w", err)
	}
	if actualSHA != expectedSHA {
		return fmt.Errorf("downloaded archive checksum mismatch")
	}

	candidate, err := os.CreateTemp(dir, ".updater-candidate-*")
	if err != nil {
		return fmt.Errorf("failed to create candidate executable: %w", err)
	}
	candidatePath := candidate.Name()
	defer os.Remove(candidatePath)
	if err := extractUpdaterBinary(archivePath, candidate); err != nil {
		candidate.Close()
		return err
	}
	if err := candidate.Close(); err != nil {
		return fmt.Errorf("failed to close candidate executable: %w", err)
	}

	// Make executable.
	if err := os.Chmod(candidatePath, 0o755); err != nil {
		return fmt.Errorf("failed to chmod: %w", err)
	}
	if err := signing.NewVerifier().VerifyReplacementExecutable(ctx, execPath, candidatePath); err != nil {
		return fmt.Errorf("downloaded updater failed security verification: %w", err)
	}

	// Atomic replace.
	if err := os.Rename(candidatePath, execPath); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied replacing %s (try sudo)", execPath)
		}
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"status":       "updated",
			"from_version": version,
			"to_version":   latestVersion,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated updater: %s -> %s\n", version, latestVersion)
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
		secureUpgradeClient(),
	)
}

// fetchLatestReleaseFrom fetches a GitHub release from the given URL using the
// provided token and HTTP client. Extracted for testability.
func fetchLatestReleaseFrom(url, token string, client *http.Client) (*checker.GitHubRelease, error) {
	if err := validateUpgradeURL(url); err != nil {
		return nil, err
	}
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
	if err := validateUpgradeURL(resp.Request.URL.String()); err != nil {
		return nil, fmt.Errorf("release API redirected to an insecure URL: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API rate limited (status %d); set GITHUB_TOKEN to increase limits", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch latest release: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read release: %w", err)
	}
	if int64(len(body)) > maxChecksumFileSize {
		return nil, fmt.Errorf("release response exceeds %d-byte limit", maxChecksumFileSize)
	}
	var release checker.GitHubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}
	return &release, nil
}

// downloadFile downloads the given URL into the provided file.
func downloadFile(dst *os.File, url, token string) error {
	return downloadFileWith(dst, url, token, secureUpgradeClient())
}

// downloadFileWith downloads the given URL into the provided file using the
// specified HTTP client. Extracted for testability.
func downloadFileWith(dst *os.File, url, token string, client *http.Client) error {
	_, err := downloadFileAndHashWith(dst, url, token, maxSelfUpgradeArchiveSize, client)
	return err
}

func downloadFileAndHash(dst *os.File, rawURL, token string, limit int64) (string, error) {
	return downloadFileAndHashWith(dst, rawURL, token, limit, secureUpgradeClient())
}

func downloadFileAndHashWith(dst *os.File, rawURL, token string, limit int64, client *http.Client) (string, error) {
	if err := validateUpgradeURL(rawURL); err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download asset: %w", err)
	}
	defer resp.Body.Close()
	if err := validateUpgradeURL(resp.Request.URL.String()); err != nil {
		return "", fmt.Errorf("asset redirected to an insecure URL: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download asset: status %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return "", fmt.Errorf("download exceeds %d-byte limit", limit)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(dst, hasher), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("failed to write downloaded asset: %w", err)
	}
	if written > limit {
		return "", fmt.Errorf("download exceeds %d-byte limit", limit)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func downloadBytes(rawURL, token string, limit int64) ([]byte, error) {
	tmp, err := os.CreateTemp("", "updater-metadata-*")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := downloadFileAndHash(tmp, rawURL, token, limit); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func checksumForAsset(data []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != assetName {
			continue
		}
		checksum := strings.ToLower(fields[0])
		if len(checksum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 checksum for %s", assetName)
		}
		for _, r := range checksum {
			if !strings.ContainsRune("0123456789abcdef", r) {
				return "", fmt.Errorf("invalid SHA-256 checksum for %s", assetName)
			}
		}
		return checksum, nil
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", assetName)
}

func extractUpdaterBinary(archivePath string, dst *os.File) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open downloaded archive: %w", err)
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("failed to open downloaded gzip archive: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return fmt.Errorf("downloaded archive does not contain updater")
		}
		if err != nil {
			return fmt.Errorf("failed to read downloaded archive: %w", err)
		}
		if header.Name != "updater" {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("updater archive entry is not a regular file")
		}
		if header.Size <= 0 || header.Size > maxSelfUpgradeBinarySize {
			return fmt.Errorf("updater archive entry has invalid size %d", header.Size)
		}
		written, err := io.CopyN(dst, tarReader, header.Size)
		if err != nil {
			return fmt.Errorf("failed to extract updater executable: %w", err)
		}
		if written != header.Size {
			return fmt.Errorf("failed to extract complete updater executable")
		}
		return nil
	}
}

func validateUpgradeURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme == "https" && parsed.Hostname() != "" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || strings.HasPrefix(host, "127.")) {
		return nil
	}
	return fmt.Errorf("URL must use HTTPS")
}

func secureUpgradeClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateUpgradeURL(req.URL.String())
		},
	}
}
