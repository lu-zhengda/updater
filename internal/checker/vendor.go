package checker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/architecture"
	"github.com/lu-zhengda/updater/internal/version"
)

// VendorChecker handles Electron apps whose native update services do not use
// electron-builder's latest-mac.yml or GitHub release assets.
type VendorChecker struct {
	client *http.Client
	runner CmdRunner
}

func NewVendorChecker(client *http.Client) *VendorChecker {
	return &VendorChecker{client: hardenedHTTPClient(client, 30*time.Second)}
}

func (v *VendorChecker) Name() string { return "vendor" }

// WithCask lets a vendor check copy a Homebrew cask digest when the cask version matches.
func (v *VendorChecker) WithCask(runner CmdRunner) *VendorChecker {
	v.runner = runner
	return v
}

func (v *VendorChecker) CanCheck(a *app.App) bool {
	if a.Source != app.SourceElectron || a.ElectronUpdateURL == "" {
		return false
	}
	switch a.ElectronUpdateFormat {
	case "vscode", "squirrel", "cursor":
		return true
	}
	return false
}

func (v *VendorChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	var latest, download, digest, notes string
	switch a.ElectronUpdateFormat {
	case "cursor":
		return v.checkCursor(ctx, a)
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

func cursorPlatform(arch string) string {
	switch arch {
	case "arm64":
		return "darwin-arm64"
	case "amd64":
		return "darwin-x64"
	default:
		return "darwin-universal"
	}
}

func cursorInstalledVersion(a *app.App) string {
	if a.ElectronProductVersion != "" {
		return a.ElectronProductVersion
	}
	return a.Version
}

func (v *VendorChecker) checkCursor(ctx context.Context, a *app.App) (*UpdateResult, error) {
	installed := cursorInstalledVersion(a)
	if installed == "" || a.ElectronCommit == "" {
		return nil, fmt.Errorf("cursor update is missing a version or commit")
	}
	platform := cursorPlatform(architecture.Native())
	endpoint := strings.TrimRight(a.ElectronUpdateURL, "/") + "/api/update/" + platform + "/cursor/" + url.PathEscape(installed) + "/" + url.PathEscape(a.ElectronCommit)
	status, data, err := v.getRelease(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return &UpdateResult{
			App: a, Source: "vendor", CurrentVersion: a.Version, LatestVersion: a.Version,
		}, nil
	}
	var release struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, err
	}
	if release.Name == "" || release.URL == "" {
		return nil, fmt.Errorf("no installable vendor release for %s", a.Name)
	}
	if err := validateHTTPSURL(release.URL); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(release.URL)
	if err != nil || !hasMacExtension(strings.ToLower(parsed.Path)) || architecture.Score(parsed.Path, architecture.Native()) == 0 {
		return nil, fmt.Errorf("incompatible vendor download for %s", a.Name)
	}
	return &UpdateResult{
		App: a, Source: "vendor", CurrentVersion: a.Version, LatestVersion: release.Name,
		DownloadURL: release.URL, DownloadDigest: v.matchingCaskDigest(ctx, a, release.Name),
		HasUpdate:     version.IsNewer(a.Version, release.Name),
		IsMajorUpdate: version.IsMajorUpgrade(a.Version, release.Name),
	}, nil
}

func (v *VendorChecker) matchingCaskDigest(ctx context.Context, a *app.App, latest string) string {
	if v.runner == nil || latest == "" {
		return ""
	}
	tokens := []string{}
	if a.CaskName != "" {
		tokens = append(tokens, a.CaskName)
	} else {
		tokens = append(tokens, a.PendingCaskTokens...)
		if a.ElectronUpdateFormat == "cursor" {
			tokens = append(tokens, "cursor")
		}
	}
	seen := map[string]struct{}{}
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		output, err := v.runner.Run(ctx, "brew", "info", "--cask", "--json=v2", token)
		if err != nil {
			continue
		}
		artifact, err := parseBrewInfo(output)
		if err != nil || strings.TrimPrefix(artifact.Version, "v") != strings.TrimPrefix(latest, "v") {
			continue
		}
		return artifact.DownloadDigest
	}
	return ""
}

func (v *VendorChecker) getRelease(ctx context.Context, endpoint string) (int, []byte, error) {
	if err := validateHTTPSURL(endpoint); err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validateHTTPSURL(resp.Request.URL.String()); err != nil {
		return 0, nil, err
	}
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, fmt.Errorf("vendor update service returned status %d", resp.StatusCode)
	}
	data, err := readMetadataResponse(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// ReleaseVendorFailure logs a vendor-feed failure and clears it so the app is
// retried on the next scan instead of shown as a failed check.
func (r *UpdateResult) ReleaseVendorFailure() {
	if r == nil || r.Error == nil || !r.App.QuietVendorCheck() {
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "vendor check failed for %s: %v\n", r.App.Name, r.Error)
	r.Error = nil
	r.HasUpdate = false
	if r.LatestVersion == "" {
		r.LatestVersion = r.CurrentVersion
	}
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
