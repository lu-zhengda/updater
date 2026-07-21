package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// DefaultCratesIOBaseURL is the public crates.io registry API endpoint.
const DefaultCratesIOBaseURL = "https://crates.io/api/v1/crates"

// cargoUserAgent identifies this client to crates.io. The registry rejects
// requests without a descriptive User-Agent (HTTP 403), per its crawler policy.
const cargoUserAgent = "updater (https://github.com/lu-zhengda/updater)"

// CargoChecker checks for updates to Rust binaries installed via `cargo install`.
// It resolves latest versions from the crates.io registry API rather than
// relying on cargo's CLI (which has no built-in "is there a newer version"
// query without third-party subcommands like cargo-update).
type CargoChecker struct {
	client     *http.Client
	cratesBase string
	mu         sync.Mutex
	cache      map[string]string
}

// NewCargoChecker creates a new CargoChecker.
// If client is nil, a client with a 10s timeout is used.
// If cratesBaseURL is empty, DefaultCratesIOBaseURL is used.
func NewCargoChecker(client *http.Client, cratesBaseURL string) *CargoChecker {
	client = hardenedHTTPClient(client, 10*time.Second)
	if cratesBaseURL == "" {
		cratesBaseURL = DefaultCratesIOBaseURL
	}
	return &CargoChecker{
		client:     client,
		cratesBase: cratesBaseURL,
		cache:      make(map[string]string),
	}
}

// Name returns the checker's display name.
func (c *CargoChecker) Name() string {
	return "cargo"
}

// CanCheck returns true if the app is a cargo-installed crate.
func (c *CargoChecker) CanCheck(a *app.App) bool {
	return a.CargoCrate != "" && a.Source == app.SourceCargo
}

// cratesIOResponse maps the fields we need from the crates.io crate metadata.
type cratesIOResponse struct {
	Crate struct {
		MaxStableVersion string `json:"max_stable_version"`
		NewestVersion    string `json:"newest_version"`
	} `json:"crate"`
}

// fetchLatestVersion queries crates.io for the latest version of crate.
// It prefers the latest stable release, falling back to the newest version
// (which may be a pre-release) when no stable release exists. Results are
// cached per-checker so repeated lookups hit the network only once.
func (c *CargoChecker) fetchLatestVersion(ctx context.Context, crate string) (string, error) {
	c.mu.Lock()
	if v, ok := c.cache[crate]; ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	url := fmt.Sprintf("%s/%s", strings.TrimRight(c.cratesBase, "/"), crate)
	if err := validateHTTPSURL(url); err != nil {
		return "", fmt.Errorf("refusing insecure crates.io URL for %s: %w", crate, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create crates.io request for %s: %w", crate, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cargoUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch crates.io metadata for %s: %w", crate, err)
	}
	defer resp.Body.Close()
	if err := validateHTTPSURL(resp.Request.URL.String()); err != nil {
		return "", fmt.Errorf("crates.io metadata redirected to an insecure URL for %s: %w", crate, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch crates.io metadata for %s: status %d", crate, resp.StatusCode)
	}

	data, err := readMetadataResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read crates.io metadata for %s: %w", crate, err)
	}
	var body cratesIOResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return "", fmt.Errorf("failed to parse crates.io metadata for %s: %w", crate, err)
	}

	latest := body.Crate.MaxStableVersion
	if latest == "" {
		latest = body.Crate.NewestVersion
	}
	if latest == "" {
		return "", fmt.Errorf("crates.io metadata for %s missing latest version", crate)
	}

	c.mu.Lock()
	c.cache[crate] = latest
	c.mu.Unlock()
	return latest, nil
}

// Check queries crates.io for the latest version of the crate.
func (c *CargoChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.CargoCrate == "" {
		return nil, fmt.Errorf("failed to check cargo update: no crate name for %s", a.Name)
	}

	latest, err := c.fetchLatestVersion(ctx, a.CargoCrate)
	if err != nil {
		return nil, err
	}

	return &UpdateResult{
		App:            a,
		Source:         "cargo",
		CurrentVersion: a.Version,
		LatestVersion:  latest,
		HasUpdate:      version.IsNewer(a.Version, latest),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

// ListInstalledCargoCrates runs `cargo install --list` and returns a map of
// crate name to installed version. The expected output format is:
//
//	bat v0.24.0:
//	    bat
//	cargo-edit v0.12.2:
//	    cargo-add
//	    cargo-upgrade
//	ripgrep v14.1.0:
//	    rg
//
// Crate lines are unindented and end with a colon; the indented lines beneath
// each describe the binaries it installs and are ignored. Crates installed from
// a git or local path source carry a trailing "(source)" annotation, which is
// stripped from the version.
func ListInstalledCargoCrates(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "cargo", "install", "--list")
	if err != nil {
		return nil, fmt.Errorf("failed to run cargo install --list: %w", err)
	}

	crates := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		if line == "" {
			continue
		}
		// Binary entries are indented beneath their crate header.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}

		name, ver, ok := parseCargoInstallLine(strings.TrimRight(line, " \t"))
		if !ok {
			continue
		}
		crates[name] = ver
	}
	return crates, nil
}

// parseCargoInstallLine extracts (name, version) from a crate header line like
// "ripgrep v14.1.0:" or "cargo-watch v8.4.0 (/path):". Returns ok=false when
// the line doesn't match the expected shape.
func parseCargoInstallLine(line string) (string, string, bool) {
	// Crate header lines always end with a colon.
	if !strings.HasSuffix(line, ":") {
		return "", "", false
	}
	line = strings.TrimSuffix(line, ":")

	// Use the last " v" so crate names containing "v" still parse.
	idx := strings.LastIndex(line, " v")
	if idx <= 0 {
		return "", "", false
	}
	name := strings.TrimSpace(line[:idx])
	ver := strings.TrimSpace(strings.TrimPrefix(line[idx+1:], "v"))

	// Strip any source annotation following the version (e.g. " (/path)" or
	// " (https://github.com/...)") that appears for git/path installs.
	if sp := strings.IndexAny(ver, " ("); sp >= 0 {
		ver = strings.TrimSpace(ver[:sp])
	}

	if name == "" || ver == "" {
		return "", "", false
	}
	// The version must start with a digit to qualify.
	if ver[0] < '0' || ver[0] > '9' {
		return "", "", false
	}
	return name, ver, true
}
