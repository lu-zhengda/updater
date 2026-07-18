package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// DefaultPyPIBaseURL is the public PyPI JSON metadata endpoint.
const DefaultPyPIBaseURL = "https://pypi.org/pypi"

// UvChecker checks for updates to Python tools installed via `uv tool install`.
// It resolves latest versions from PyPI's JSON metadata API rather than relying
// on uv's CLI (which has no JSON output or dry-run for upgrades).
type UvChecker struct {
	client      *http.Client
	pypiBaseURL string
	mu          sync.Mutex
	cache       map[string]string
}

// NewUvChecker creates a new UvChecker.
// If client is nil, a client with a 10s timeout is used.
// If pypiBaseURL is empty, DefaultPyPIBaseURL is used.
func NewUvChecker(client *http.Client, pypiBaseURL string) *UvChecker {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if pypiBaseURL == "" {
		pypiBaseURL = DefaultPyPIBaseURL
	}
	return &UvChecker{
		client:      client,
		pypiBaseURL: pypiBaseURL,
		cache:       make(map[string]string),
	}
}

// Name returns the checker's display name.
func (u *UvChecker) Name() string {
	return "uv"
}

// CanCheck returns true if the app is a uv-installed tool.
func (u *UvChecker) CanCheck(a *app.App) bool {
	return a.UvTool != "" && a.Source == app.SourceUv
}

// pypiResponse maps the fields we need from PyPI's JSON metadata.
type pypiResponse struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
}

// fetchLatestVersion queries PyPI for the latest version of pkg.
// Results are cached per-checker so multiple apps backed by the same package
// (e.g. mypy + mypyc share the mypy distribution name) hit PyPI only once.
func (u *UvChecker) fetchLatestVersion(ctx context.Context, pkg string) (string, error) {
	u.mu.Lock()
	if v, ok := u.cache[pkg]; ok {
		u.mu.Unlock()
		return v, nil
	}
	u.mu.Unlock()

	url := fmt.Sprintf("%s/%s/json", strings.TrimRight(u.pypiBaseURL, "/"), pkg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create PyPI request for %s: %w", pkg, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch PyPI metadata for %s: %w", pkg, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch PyPI metadata for %s: status %d", pkg, resp.StatusCode)
	}

	var body pypiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("failed to parse PyPI metadata for %s: %w", pkg, err)
	}
	if body.Info.Version == "" {
		return "", fmt.Errorf("PyPI metadata for %s missing latest version", pkg)
	}

	u.mu.Lock()
	u.cache[pkg] = body.Info.Version
	u.mu.Unlock()
	return body.Info.Version, nil
}

// Check queries PyPI for the latest version of the tool's distribution.
// Tools installed from git/path/url sources have no PyPI release to compare
// against, so they report up to date rather than a 404 error.
func (u *UvChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.UvTool == "" {
		return nil, fmt.Errorf("failed to check uv update: no tool name for %s", a.Name)
	}

	if a.UvNonRegistry {
		return &UpdateResult{
			App:            a,
			Source:         "uv",
			CurrentVersion: a.Version,
			LatestVersion:  a.Version,
			HasUpdate:      false,
		}, nil
	}

	latest, err := u.fetchLatestVersion(ctx, a.UvTool)
	if err != nil {
		return nil, err
	}

	return &UpdateResult{
		App:            a,
		Source:         "uv",
		CurrentVersion: a.Version,
		LatestVersion:  latest,
		HasUpdate:      version.IsNewer(a.Version, latest),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

// ListInstalledUvTools runs `uv tool list` and returns a map of tool name to
// installed version. The expected output format is:
//
//	black v25.1.0
//	- black
//	- blackd
//	ruff v0.8.4
//	- ruff
//
// Lines beginning with "-" describe executables exposed by the preceding tool
// and are ignored. Warning lines (e.g. "warning: Tool ... environment not
// found") are skipped so transient uv issues don't block discovery.
func ListInstalledUvTools(ctx context.Context, runner CmdRunner) (map[string]string, error) {
	output, err := runner.Run(ctx, "uv", "tool", "list")
	if err != nil {
		return nil, fmt.Errorf("failed to run uv tool list: %w", err)
	}

	tools := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Executable entries are indented with a leading "-".
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		// Skip warnings, errors, or any line that doesn't look like "name vX.Y.Z".
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "warning:") || strings.HasPrefix(lower, "error:") {
			continue
		}
		// "No tools installed" or similar informational lines.
		if !strings.Contains(trimmed, " v") {
			continue
		}

		name, ver, ok := parseUvToolLine(trimmed)
		if !ok {
			continue
		}
		tools[name] = ver
	}
	return tools, nil
}

// NonRegistryUvTools returns the set of tools (by name) whose own requirement
// was installed from a non-registry source (git/path/url/editable) according
// to uv's receipt files. Errors are treated as "registry" — the PyPI check
// will surface any real problem.
func NonRegistryUvTools(ctx context.Context, runner CmdRunner, tools map[string]string) map[string]bool {
	output, err := runner.Run(ctx, "uv", "tool", "dir")
	if err != nil {
		return nil
	}
	dir := strings.TrimSpace(string(output))
	if dir == "" {
		return nil
	}

	nonRegistry := make(map[string]bool)
	for name := range tools {
		data, err := os.ReadFile(filepath.Join(dir, name, "uv-receipt.toml"))
		if err != nil {
			continue
		}
		if uvToolFromNonRegistrySource(string(data), name) {
			nonRegistry[name] = true
		}
	}
	return nonRegistry
}

// uvToolFromNonRegistrySource reports whether the receipt's requirement entry
// for the tool itself carries a git/path/url/editable source. Receipt entries
// look like:
//
//	requirements = [
//	    { name = "agent-reach", git = "https://github.com/x/y.git?rev=abc" },
//	    { name = "browser-cookie3" },
//	]
//
// Entries never contain nested braces, so scanning brace groups is sufficient.
func uvToolFromNonRegistrySource(receipt, tool string) bool {
	nameKey := `name = "` + strings.ToLower(strings.ReplaceAll(tool, "_", "-")) + `"`
	rest := receipt
	for {
		start := strings.Index(rest, "{")
		if start < 0 {
			return false
		}
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			return false
		}
		entry := rest[start : start+end]
		rest = rest[start+end+1:]

		normalized := strings.ToLower(strings.ReplaceAll(entry, "_", "-"))
		if !strings.Contains(normalized, nameKey) {
			continue
		}
		return strings.Contains(entry, "git =") ||
			strings.Contains(entry, "path =") ||
			strings.Contains(entry, "url =") ||
			strings.Contains(entry, "editable =")
	}
}

// parseUvToolLine extracts (name, version) from a line like "black v25.1.0".
// Returns ok=false when the line doesn't match the expected shape.
func parseUvToolLine(line string) (string, string, bool) {
	// Use the last " v" so package names containing "v" still parse.
	idx := strings.LastIndex(line, " v")
	if idx <= 0 {
		return "", "", false
	}
	name := strings.TrimSpace(line[:idx])
	ver := strings.TrimSpace(strings.TrimPrefix(line[idx+1:], "v"))
	if name == "" || ver == "" {
		return "", "", false
	}
	// The remainder must start with a digit to qualify as a version.
	if ver[0] < '0' || ver[0] > '9' {
		return "", "", false
	}
	return name, ver, true
}
