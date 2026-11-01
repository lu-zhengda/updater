package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
	"github.com/lu-zhengda/updater/internal/version"
)

const defaultGitHubAPI = "https://api.github.com"

// GitHubRelease represents a GitHub release from the API.
type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Body    string        `json:"body"`
	Assets  []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a downloadable asset in a GitHub release.
type GitHubAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Digest      string `json:"digest"`
}

// GitHubChecker checks for updates via GitHub Releases API.
type GitHubChecker struct {
	client  *http.Client
	baseURL string
	token   string
	// ReadBundleVersion inspects a release asset without installing it.
	ReadBundleVersion func(ctx context.Context, downloadURL, digest, bundleID string) (string, error)
}

// NewGitHubChecker creates a new GitHubChecker.
// If client is nil, a hardened client with a 30-second timeout is used.
// If baseURL is empty, the default GitHub API URL is used.
// If token is non-empty, it is sent as a Bearer token in requests.
func NewGitHubChecker(client *http.Client, baseURL, token string) *GitHubChecker {
	client = hardenedHTTPClient(client, 30*time.Second)
	if baseURL == "" {
		baseURL = defaultGitHubAPI
	}
	return &GitHubChecker{client: client, baseURL: baseURL, token: token}
}

// Name returns the checker's display name.
func (g *GitHubChecker) Name() string {
	return "github"
}

// CanCheck returns true if the app has a GitHubRepo set.
func (g *GitHubChecker) CanCheck(a *app.App) bool {
	return a.GitHubRepo != ""
}

// Check queries the GitHub Releases API for the latest release.
func (g *GitHubChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.GitHubRepo == "" {
		return nil, fmt.Errorf("failed to check GitHub update: no repo for %s", a.Name)
	}

	url := fmt.Sprintf("%s/repos/%s/releases/latest", g.baseURL, a.GitHubRepo)
	if err := validateHTTPSURL(url); err != nil {
		return nil, fmt.Errorf("refusing insecure GitHub API URL for %s: %w", a.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", a.Name, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release for %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if err := validateHTTPSURL(resp.Request.URL.String()); err != nil {
		return nil, fmt.Errorf("GitHub API redirected to an insecure URL for %s: %w", a.Name, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch release for %s: status %d", a.Name, resp.StatusCode)
	}

	body, err := readMetadataResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read release for %s: %w", a.Name, err)
	}
	var release GitHubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("failed to parse release for %s: %w", a.Name, err)
	}

	latestVersion := CleanTagVersion(release.TagName)
	asset := findMacAsset(release.Assets)
	if override := a.ResolvedSourceOverride; override != nil && override.Kind == "github" {
		switch override.VersionSource {
		case "", "tag":
			if override.VersionPattern != "" || override.VersionReplacement != "" {
				latestVersion, err = transformGitHubVersion(latestVersion, override.VersionPattern, override.VersionReplacement)
			}
		case "bundle":
			var bundleAssets []GitHubAsset
			for _, candidate := range release.Assets {
				name := strings.ToLower(candidate.Name)
				if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".dmg") {
					bundleAssets = append(bundleAssets, candidate)
				}
			}
			asset = findMacAsset(bundleAssets)
			if asset.DownloadURL == "" {
				return nil, fmt.Errorf("no compatible macOS ZIP or DMG asset to read bundle version for %s", a.Name)
			}
			if g.ReadBundleVersion == nil {
				return nil, fmt.Errorf("bundle version reader is unavailable for %s", a.Name)
			}
			latestVersion, err = g.ReadBundleVersion(ctx, asset.DownloadURL, asset.Digest, a.BundleID)
			if err == nil && strings.TrimSpace(latestVersion) == "" {
				err = fmt.Errorf("bundle version is empty")
			}
		default:
			err = fmt.Errorf("unknown version_source %q", override.VersionSource)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to resolve GitHub version for %s: %w", a.Name, err)
		}
	}

	return &UpdateResult{
		App:            a,
		Source:         "github",
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    asset.DownloadURL,
		DownloadDigest: asset.Digest,
		ReleaseNotes:   release.Body,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latestVersion),
	}, nil
}

func transformGitHubVersion(tag, pattern, replacement string) (string, error) {
	if strings.TrimSpace(pattern) == "" || strings.TrimSpace(replacement) == "" {
		return "", fmt.Errorf("version_pattern and version_replacement must both be non-empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid version_pattern: %w", err)
	}
	match := re.FindStringSubmatchIndex(tag)
	if match == nil || match[0] != 0 || match[1] != len(tag) {
		return "", fmt.Errorf("version_pattern does not match the full release tag %q", tag)
	}
	transformed := string(re.ExpandString(nil, replacement, tag, match))
	if _, err := semver.NewVersion(transformed); err != nil {
		return "", fmt.Errorf("version_replacement produced invalid version %q: %w", transformed, err)
	}
	return transformed, nil
}

// CleanTagVersion strips common tag prefixes from GitHub release tags.
// e.g., "v1.0.0" -> "1.0.0", "release-3.5.4" -> "3.5.4", "release/3.5.4" -> "3.5.4"
func CleanTagVersion(tag string) string {
	prefixes := []string{"v", "release-", "release/", "ver-", "ver/", "version-", "version/"}
	result := tag
	for _, p := range prefixes {
		result = strings.TrimPrefix(result, p)
	}
	return result
}

// macExtensions are file extensions commonly used for macOS installers.
var macExtensions = []string{".dmg", ".pkg", ".zip"}

// macKeywords are substrings that indicate a macOS-specific asset.
var macKeywords = []string{"mac", "darwin", "macos", "osx"}

// findMacAsset searches the release assets for a macOS download.
// It looks for assets with macOS file extensions (.dmg, .pkg, .zip) that
// also contain a macOS keyword (mac, darwin, macos, osx) in their name.
func findMacAsset(assets []GitHubAsset) GitHubAsset {
	var best GitHubAsset
	bestScore := 0
	for _, asset := range assets {
		nameLower := strings.ToLower(asset.Name)
		if hasMacExtension(nameLower) && hasMacKeyword(nameLower) {
			if score := architecture.Score(nameLower, architecture.Native()); score > bestScore {
				best, bestScore = asset, score
			}
		}
	}
	return best
}

func hasMacExtension(name string) bool {
	for _, ext := range macExtensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func hasMacKeyword(name string) bool {
	for _, kw := range macKeywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}
