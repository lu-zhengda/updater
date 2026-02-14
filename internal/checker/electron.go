package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
	"gopkg.in/yaml.v3"
)

// latestMacYML maps the fields from an Electron generic server's latest-mac.yml.
type latestMacYML struct {
	Version string `yaml:"version"`
	Path    string `yaml:"path"`
}

// ElectronChecker checks for updates via Electron generic update servers.
type ElectronChecker struct {
	client *http.Client
}

// NewElectronChecker creates a new ElectronChecker.
// If client is nil, http.DefaultClient is used.
func NewElectronChecker(client *http.Client) *ElectronChecker {
	if client == nil {
		client = http.DefaultClient
	}
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
	url := baseURL + "/latest-mac.yml"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", a.Name, err)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch update info for %s: %w", a.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch update info for %s: status %d", a.Name, resp.StatusCode)
	}

	const maxResponseSize = 1 << 20 // 1 MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
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
	if latest.Path != "" {
		downloadURL = baseURL + "/" + latest.Path
	}

	return &UpdateResult{
		App:            a,
		Source:         "electron",
		CurrentVersion: a.Version,
		LatestVersion:  latest.Version,
		DownloadURL:    downloadURL,
		HasUpdate:      version.IsNewer(a.Version, latest.Version),
	}, nil
}
