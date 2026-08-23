package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/version"
)

// PipxChecker checks for updates to applications installed with pipx.
type PipxChecker struct {
	pypi *UvChecker
}

// NewPipxChecker creates a pipx checker backed by the PyPI JSON API.
func NewPipxChecker(client *http.Client, pypiBaseURL string) *PipxChecker {
	return &PipxChecker{pypi: NewUvChecker(client, pypiBaseURL)}
}

func (p *PipxChecker) Name() string {
	return "pipx"
}

func (p *PipxChecker) CanCheck(a *app.App) bool {
	return a.PipxEnvironment != "" && a.PipxPackage != "" && a.Source == app.SourcePipx
}

func (p *PipxChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.PipxEnvironment == "" || a.PipxPackage == "" {
		return nil, fmt.Errorf("failed to check pipx update: incomplete pipx metadata for %s", a.Name)
	}

	// pipx deliberately skips pinned environments. VCS, URL, path, script, and
	// constrained installs do not have a safe, unambiguous PyPI target either.
	if a.PipxPinned || a.PipxNonRegistry {
		return &UpdateResult{
			App:            a,
			Source:         "pipx",
			CurrentVersion: a.Version,
			LatestVersion:  a.Version,
			HasUpdate:      false,
		}, nil
	}

	latest, err := p.pypi.fetchLatestVersion(ctx, a.PipxPackage)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{
		App:            a,
		Source:         "pipx",
		CurrentVersion: a.Version,
		LatestVersion:  latest,
		HasUpdate:      version.IsNewer(a.Version, latest),
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latest),
	}, nil
}

// PipxPackageInfo is the pipx metadata needed by discovery and updates.
type PipxPackageInfo struct {
	Environment string
	Package     string
	Version     string
	NonRegistry bool
	Pinned      bool
}

type pipxPackageRecord struct {
	Package        string `json:"package"`
	PackageOrURL   string `json:"package_or_url"`
	PackageVersion string `json:"package_version"`
	Pinned         bool   `json:"pinned"`
}

type pipxMetadata struct {
	MainPackage pipxPackageRecord `json:"main_package"`
}

type pipxVenvRecord struct {
	// Current pipx snapshots wrap the environment metadata. MainPackage also
	// accepts the older/direct shape so discovery remains backward compatible.
	Metadata    pipxMetadata      `json:"metadata"`
	MainPackage pipxPackageRecord `json:"main_package"`
}

// ListInstalledPipxPackages reads pipx's versioned machine-readable snapshot.
func ListInstalledPipxPackages(ctx context.Context, runner CmdRunner) (map[string]PipxPackageInfo, error) {
	output, err := runner.Run(ctx, "pipx", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("failed to run pipx list --json: %w", err)
	}

	var snapshot struct {
		Venvs map[string]pipxVenvRecord `json:"venvs"`
	}
	if err := json.Unmarshal(output, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to parse pipx list output: %w", err)
	}

	packages := make(map[string]PipxPackageInfo, len(snapshot.Venvs))
	for environment, venv := range snapshot.Venvs {
		main := venv.Metadata.MainPackage
		if main.Package == "" && main.PackageOrURL == "" {
			main = venv.MainPackage
		}
		pkg := main.Package
		if pkg == "" {
			// Scripts and URL-only installs can still be shown, but are marked as
			// non-registry so updater never invents a PyPI upgrade target.
			pkg = environment
		}
		packages[environment] = PipxPackageInfo{
			Environment: environment,
			Package:     pkg,
			Version:     main.PackageVersion,
			NonRegistry: !isPlainPipxRegistryPackage(main.Package, main.PackageOrURL),
			Pinned:      main.Pinned,
		}
	}
	return packages, nil
}

func isPlainPipxRegistryPackage(pkg, source string) bool {
	if pkg == "" {
		return false
	}
	if source == "" {
		return true
	}
	normalize := func(name string) string {
		name = strings.ToLower(strings.TrimSpace(name))
		return strings.NewReplacer("_", "-", ".", "-").Replace(name)
	}
	return normalize(pkg) == normalize(source)
}
