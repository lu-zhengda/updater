package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
	"gopkg.in/yaml.v3"
)

// latestMacYML maps the fields from an Electron generic server's latest-mac.yml.
type latestMacYML struct {
	Version string `yaml:"version"`
	Path    string `yaml:"path"`
	SHA512  string `yaml:"sha512"`
}

// ElectronChecker checks for updates via Electron generic update servers.
type ElectronChecker struct {
	client *http.Client
}

// NewElectronChecker creates a new ElectronChecker.
// If client is nil, a hardened client with a 30-second timeout is used.
func NewElectronChecker(client *http.Client) *ElectronChecker {
	client = hardenedHTTPClient(client, 30*time.Second)
	return &ElectronChecker{client: client}
}

// Name returns the checker's display name.
func (e *ElectronChecker) Name() string {
	return "electron"
}

// CanCheck returns true if the app has an Electron update URL.
func (e *ElectronChecker) CanCheck(a *app.App) bool {
	return a.ElectronUpdateURL != "" && a.Source == app.SourceElectron
}

// Check fetches latest-mac.yml from the app's update server and compares versions.
func (e *ElectronChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	baseURL := strings.TrimRight(a.ElectronUpdateURL, "/")
	if err := validateHTTPSURL(baseURL); err != nil {
		return nil, fmt.Errorf("refusing insecure Electron update URL for %s: %w", a.Name, err)
	}
	metadataURL := baseURL + "/latest-mac.yml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", a.Name, err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch update info for %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if err := validateHTTPSURL(resp.Request.URL.String()); err != nil {
		return nil, fmt.Errorf("Electron update metadata redirected to an insecure URL for %s: %w", a.Name, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch update info for %s: status %d", a.Name, resp.StatusCode)
	}

	body, err := readMetadataResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read update info for %s: %w", a.Name, err)
	}

	var latest latestMacYML
	if err := yaml.Unmarshal(body, &latest); err != nil {
		return nil, fmt.Errorf("failed to parse update info for %s: %w", a.Name, err)
	}

	if latest.Version == "" {
		return nil, fmt.Errorf("no version found in update info for %s", a.Name)
	}

	var downloadURL string
	var downloadDigest string
	if latest.Path != "" {
		base, err := url.Parse(baseURL + "/")
		if err != nil {
			return nil, fmt.Errorf("failed to parse update URL for %s: %w", a.Name, err)
		}
		reference, err := url.Parse(latest.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to parse download path for %s: %w", a.Name, err)
		}
		resolved := base.ResolveReference(reference)
		if !strings.EqualFold(resolved.Scheme, base.Scheme) || !strings.EqualFold(resolved.Host, base.Host) {
			return nil, fmt.Errorf("download URL for %s escapes configured update origin", a.Name)
		}
		downloadURL = resolved.String()
		if latest.SHA512 != "" {
			downloadDigest = "sha512:" + latest.SHA512
		}
	}

	return &UpdateResult{
		App:            a,
		Source:         "electron",
		CurrentVersion: a.Version,
		LatestVersion:  latest.Version,
		DownloadURL:    downloadURL,
		DownloadDigest: downloadDigest,
		HasUpdate:      version.IsNewer(a.Version, latest.Version),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest.Version),
	}, nil
}
