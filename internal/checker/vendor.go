package checker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
	"github.com/lu-zhengda/updater/internal/version"
)

// VendorChecker handles Electron apps whose native update services do not use
// electron-builder's latest-mac.yml or GitHub release assets.
type VendorChecker struct{ client *http.Client }

func NewVendorChecker(client *http.Client) *VendorChecker {
	return &VendorChecker{client: hardenedHTTPClient(client, 30*time.Second)}
}

func (v *VendorChecker) Name() string { return "vendor" }

func (v *VendorChecker) CanCheck(a *app.App) bool {
	if a.Source != app.SourceElectron || a.ElectronUpdateURL == "" {
		return false
	}
	switch a.ElectronUpdateFormat {
	case "vscode", "squirrel":
		return true
	}
	return false
}

func (v *VendorChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	var latest, download, digest, notes string
	switch a.ElectronUpdateFormat {
	case "vscode":
		// The installed product specifies its service and channel. Never point
		// Insiders (or a custom channel) at the stable release service.
		if a.ElectronUpdateChannel != "stable" && a.ElectronUpdateChannel != "insider" {
			return nil, fmt.Errorf("unsupported VS Code update channel %q", a.ElectronUpdateChannel)
		}
		platform := "darwin"
		if architecture.Native() == "arm64" {
			platform = "darwin-arm64"
		}
		endpoint := strings.TrimRight(a.ElectronUpdateURL, "/") + "/api/update/" + platform + "/" + a.ElectronUpdateChannel + "/latest"
		var release struct {
			Version string `json:"productVersion"`
			URL     string `json:"url"`
			SHA256  string `json:"sha256hash"`
		}
		if err := v.fetch(ctx, endpoint, &release); err != nil {
			return nil, err
		}
		latest, download = release.Version, release.URL
		hash, err := hex.DecodeString(release.SHA256)
		if err != nil || len(hash) != 32 {
			return nil, fmt.Errorf("VS Code update has no valid SHA-256 digest")
		}
		digest = "sha256:" + strings.ToLower(release.SHA256)
	case "squirrel":
		var index struct {
			Current  string `json:"currentRelease"`
			Releases []struct {
				Version string `json:"version"`
				Update  struct {
					Version string `json:"version"`
					URL     string `json:"url"`
					Notes   string `json:"notes"`
				} `json:"updateTo"`
			} `json:"releases"`
		}
		if err := v.fetch(ctx, a.ElectronUpdateURL, &index); err != nil {
			return nil, err
		}
		// Respect the server's selected release, rather than offering a staged
		// future release that happens to be present in the index.
		for _, r := range index.Releases {
			if index.Current != "" && r.Version == index.Current && r.Update.Version == index.Current {
				latest, download, notes = r.Update.Version, r.Update.URL, r.Update.Notes
				break
			}
		}
	default:
		return nil, fmt.Errorf("unsupported vendor app %s", a.BundleID)
	}
	if latest == "" || download == "" {
		return nil, fmt.Errorf("no installable vendor release for %s", a.Name)
	}
	if err := validateHTTPSURL(download); err != nil {
		return nil, err
	}
	u, err := url.Parse(download)
	if err != nil || !hasMacExtension(strings.ToLower(u.Path)) || architecture.Score(u.Path, architecture.Native()) == 0 {
		return nil, fmt.Errorf("incompatible vendor download for %s", a.Name)
	}
	return &UpdateResult{
		App: a, Source: "vendor", CurrentVersion: a.Version, LatestVersion: latest,
		DownloadURL: download, DownloadDigest: digest, ReleaseNotes: notes,
		HasUpdate: version.IsNewer(a.Version, latest), IsMajorUpdate: version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

func (v *VendorChecker) fetch(ctx context.Context, endpoint string, value any) error {
	if err := validateHTTPSURL(endpoint); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := validateHTTPSURL(resp.Request.URL.String()); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vendor update service returned status %d", resp.StatusCode)
	}
	data, err := readMetadataResponse(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
