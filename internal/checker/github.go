package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
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
}

// GitHubChecker checks for updates via GitHub Releases API.
type GitHubChecker struct {
	client  *http.Client
	baseURL string
	token   string
}

// NewGitHubChecker creates a new GitHubChecker.
// If client is nil, http.DefaultClient is used.
// If baseURL is empty, the default GitHub API URL is used.
// If token is non-empty, it is sent as a Bearer token in requests.
func NewGitHubChecker(client *http.Client, baseURL, token string) *GitHubChecker {
	if client == nil {
		client = http.DefaultClient
	}
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch release for %s: status %d", a.Name, resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release for %s: %w", a.Name, err)
	}

	latestVersion := CleanTagVersion(release.TagName)
	downloadURL := findMacAsset(release.Assets)

	return &UpdateResult{
		App:            a,
		Source:         "github",
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    downloadURL,
		ReleaseNotes:   release.Body,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latestVersion),
	}, nil
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
func findMacAsset(assets []GitHubAsset) string {
	for _, asset := range assets {
		nameLower := strings.ToLower(asset.Name)
		if hasMacExtension(nameLower) && hasMacKeyword(nameLower) {
			return asset.DownloadURL
		}
	}
	return ""
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
