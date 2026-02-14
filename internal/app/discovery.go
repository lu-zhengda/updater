package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
			if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".app") {
				continue
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
		Source:   classifySource(contentsDir, info),
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
func classifySource(contentsDir string, info *infoPlist) Source {
	// Check for Mac App Store receipt first.
	receiptPath := filepath.Join(contentsDir, "_MASReceipt", "receipt")
	if _, err := os.Stat(receiptPath); err == nil {
		return SourceMAS
	}

	// Check for Sparkle framework + SUFeedURL.
	sparkleDir := filepath.Join(contentsDir, "Frameworks", "Sparkle.framework")
	if _, err := os.Stat(sparkleDir); err == nil && info.FeedURL != "" {
		return SourceSparkle
	}

	return SourceUnknown
}
