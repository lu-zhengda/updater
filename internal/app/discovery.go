package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"howett.net/plist"
)

// infoPlist maps the fields we read from an app's Info.plist.
type infoPlist struct {
	BundleName         string `plist:"CFBundleName"`
	BundleDisplayName  string `plist:"CFBundleDisplayName"`
	BundleID           string `plist:"CFBundleIdentifier"`
	ShortVersionString string `plist:"CFBundleShortVersionString"`
	BundleVersion      string `plist:"CFBundleVersion"`
	FeedURL            string `plist:"SUFeedURL"`
}

// Discover scans the given directories for .app bundles, parses their
// Info.plist files, and classifies them by install source.
// Directories that don't exist are silently skipped.
func Discover(dirs ...string) ([]*App, error) {
	var apps []*App

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
		}

		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".app") {
				continue
			}
			// Check if entry is a directory (or a symlink to a directory).
			if !entry.IsDir() {
				if entry.Type()&os.ModeSymlink == 0 {
					continue
				}
				// Symlink: check if target is a directory.
				target := filepath.Join(dir, entry.Name())
				info, err := os.Stat(target) // follows symlinks
				if err != nil || !info.IsDir() {
					continue
				}
			}

			appPath := filepath.Join(dir, entry.Name())
			a, err := parseApp(appPath)
			if err != nil {
				// Skip apps we can't parse rather than failing entirely.
				continue
			}

			apps = append(apps, a)
		}
	}

	return apps, nil
}

// parseApp reads an .app bundle and returns a populated App struct.
func parseApp(appPath string) (*App, error) {
	contentsDir := filepath.Join(appPath, "Contents")
	plistPath := filepath.Join(contentsDir, "Info.plist")

	info, err := readInfoPlist(plistPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read Info.plist for %s: %w", appPath, err)
	}

	name := info.BundleDisplayName
	if name == "" {
		name = info.BundleName
	}
	if name == "" {
		// Fall back to the directory name without .app extension.
		name = strings.TrimSuffix(filepath.Base(appPath), ".app")
	}

	a := &App{
		Name:     name,
		BundleID: info.BundleID,
		Version:  info.ShortVersionString,
		Build:    info.BundleVersion,
		Path:     appPath,
		FeedURL:  info.FeedURL,
		Source:   classifySource(appPath, contentsDir, info),
	}

	if a.Source == SourceElectron {
		enrichElectronApp(contentsDir, a)
	}

	return a, nil
}

// readInfoPlist decodes an Info.plist file.
func readInfoPlist(path string) (*infoPlist, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open plist: %w", err)
	}
	defer f.Close()

	var info infoPlist
	decoder := plist.NewDecoder(f)
	if err := decoder.Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode plist: %w", err)
	}

	return &info, nil
}

// classifySource determines the install source of an app.
func classifySource(appPath, contentsDir string, info *infoPlist) Source {
	// Check for Mac App Store receipt first.
	receiptPath := filepath.Join(contentsDir, "_MASReceipt", "receipt")
	if _, err := os.Stat(receiptPath); err == nil {
		return SourceMAS
	}

	// Setapp: app is inside a Setapp directory.
	if isSetappPath(appPath) {
		return SourceSetapp
	}

	// JetBrains Toolbox: resolve symlinks, check if target is in Toolbox directory.
	if isToolboxPath(appPath) {
		return SourceToolbox
	}

	// Check for Sparkle framework + SUFeedURL.
	sparkleDir := filepath.Join(contentsDir, "Frameworks", "Sparkle.framework")
	if _, err := os.Stat(sparkleDir); err == nil && info.FeedURL != "" {
		return SourceSparkle
	}

	// Electron: has Electron Framework but no Sparkle.
	electronFramework := filepath.Join(contentsDir, "Frameworks", "Electron Framework.framework")
	if _, err := os.Stat(electronFramework); err == nil {
		return SourceElectron
	}

	// Adobe CC: non-MAS app with com.adobe.* bundle ID.
	if strings.HasPrefix(info.BundleID, "com.adobe.") {
		return SourceAdobe
	}

	return SourceUnknown
}

// isSetappPath checks if the app's parent directory is named "Setapp".
func isSetappPath(appPath string) bool {
	return filepath.Base(filepath.Dir(appPath)) == "Setapp"
}

// isToolboxPath resolves symlinks and checks if the target is in a JetBrains Toolbox directory.
func isToolboxPath(appPath string) bool {
	resolved, err := filepath.EvalSymlinks(appPath)
	if err != nil {
		return false
	}
	return strings.Contains(resolved, "JetBrains/Toolbox/apps")
}

// appUpdateYML maps the fields we read from app-update.yml.
type appUpdateYML struct {
	Provider string `yaml:"provider"`
	Owner    string `yaml:"owner"`
	Repo     string `yaml:"repo"`
	URL      string `yaml:"url"`
}

// enrichElectronApp reads Contents/Resources/app-update.yml and enriches the app
// with GitHub repo or generic update URL information.
func enrichElectronApp(contentsDir string, a *App) {
	ymlPath := filepath.Join(contentsDir, "Resources", "app-update.yml")
	data, err := os.ReadFile(ymlPath)
	if err != nil {
		return // no app-update.yml, stays as SourceElectron
	}

	var cfg appUpdateYML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return
	}

	switch cfg.Provider {
	case "github":
		if cfg.Owner != "" && cfg.Repo != "" {
			a.GitHubRepo = cfg.Owner + "/" + cfg.Repo
			a.Source = SourceGitHub
		}
	case "generic":
		if strings.HasPrefix(cfg.URL, "https://") {
			a.ElectronUpdateURL = cfg.URL
		}
	}
}
