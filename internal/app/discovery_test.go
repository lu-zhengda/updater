package app

import (
	"os"
	"path/filepath"
	"testing"

	"howett.net/plist"
)

// plistData holds the Info.plist fields we care about.
type plistData struct {
	BundleName         string `plist:"CFBundleName"`
	BundleDisplayName  string `plist:"CFBundleDisplayName"`
	BundleID           string `plist:"CFBundleIdentifier"`
	ShortVersionString string `plist:"CFBundleShortVersionString"`
	BundleVersion      string `plist:"CFBundleVersion"`
	FeedURL            string `plist:"SUFeedURL,omitempty"`
}

// createFakeApp builds a minimal .app bundle in dir and returns its path.
func createFakeApp(t *testing.T, dir, name string, info plistData, mas, sparkle bool) string {
	t.Helper()

	appDir := filepath.Join(dir, name+".app")
	contentsDir := filepath.Join(appDir, "Contents")

	if err := os.MkdirAll(contentsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write Info.plist
	plistFile, err := os.Create(filepath.Join(contentsDir, "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	defer plistFile.Close()

	encoder := plist.NewEncoder(plistFile)
	encoder.Indent("\t")
	if err := encoder.Encode(info); err != nil {
		t.Fatal(err)
	}

	// MAS receipt
	if mas {
		receiptDir := filepath.Join(contentsDir, "_MASReceipt")
		if err := os.MkdirAll(receiptDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(receiptDir, "receipt"), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Sparkle framework
	if sparkle {
		sparkleDir := filepath.Join(contentsDir, "Frameworks", "Sparkle.framework")
		if err := os.MkdirAll(sparkleDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return appDir
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()

	// Create a MAS app
	createFakeApp(t, dir, "Pages", plistData{
		BundleName:         "Pages",
		BundleDisplayName:  "Pages",
		BundleID:           "com.apple.iWork.Pages",
		ShortVersionString: "14.0",
		BundleVersion:      "7040",
	}, true, false)

	// Create a Sparkle app with SUFeedURL
	createFakeApp(t, dir, "iTerm", plistData{
		BundleName:         "iTerm2",
		BundleDisplayName:  "iTerm",
		BundleID:           "com.googlecode.iterm2",
		ShortVersionString: "3.5.0",
		BundleVersion:      "350",
		FeedURL:            "https://iterm2.com/appcasts/final3.xml",
	}, false, true)

	// Create a plain/unknown app (no MAS receipt, no Sparkle framework)
	createFakeApp(t, dir, "MyApp", plistData{
		BundleName:         "MyApp",
		BundleID:           "com.example.myapp",
		ShortVersionString: "1.0.0",
		BundleVersion:      "1",
	}, false, false)

	// Create an app with no display name to test directory name fallback
	createFakeApp(t, dir, "NoName", plistData{
		BundleID:           "com.example.noname",
		ShortVersionString: "0.1.0",
		BundleVersion:      "1",
	}, false, false)

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(apps) != 4 {
		t.Fatalf("expected 4 apps, got %d", len(apps))
	}

	// Build a lookup by bundle ID for easy assertions
	byID := make(map[string]*App)
	for _, a := range apps {
		byID[a.BundleID] = a
	}

	// MAS app
	masApp, ok := byID["com.apple.iWork.Pages"]
	if !ok {
		t.Fatal("MAS app not found")
	}
	if masApp.Source != SourceMAS {
		t.Errorf("expected source %q, got %q", SourceMAS, masApp.Source)
	}
	if masApp.Version != "14.0" {
		t.Errorf("expected version 14.0, got %s", masApp.Version)
	}
	if masApp.Build != "7040" {
		t.Errorf("expected build 7040, got %s", masApp.Build)
	}

	// Sparkle app
	sparkleApp, ok := byID["com.googlecode.iterm2"]
	if !ok {
		t.Fatal("Sparkle app not found")
	}
	if sparkleApp.Source != SourceSparkle {
		t.Errorf("expected source %q, got %q", SourceSparkle, sparkleApp.Source)
	}
	if sparkleApp.FeedURL != "https://iterm2.com/appcasts/final3.xml" {
		t.Errorf("expected feed URL, got %q", sparkleApp.FeedURL)
	}
	if sparkleApp.Version != "3.5.0" {
		t.Errorf("expected version 3.5.0, got %s", sparkleApp.Version)
	}

	// Unknown app
	unknownApp, ok := byID["com.example.myapp"]
	if !ok {
		t.Fatal("Unknown app not found")
	}
	if unknownApp.Source != SourceUnknown {
		t.Errorf("expected source %q, got %q", SourceUnknown, unknownApp.Source)
	}
	if unknownApp.Name != "MyApp" {
		t.Errorf("expected name MyApp, got %s", unknownApp.Name)
	}

	// No-name app should fall back to directory name
	noNameApp, ok := byID["com.example.noname"]
	if !ok {
		t.Fatal("NoName app not found")
	}
	if noNameApp.Name != "NoName" {
		t.Errorf("expected name NoName (from dir), got %s", noNameApp.Name)
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	dir := t.TempDir()

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestDiscoverNonExistentDir(t *testing.T) {
	apps, err := Discover("/nonexistent/path")
	if err != nil {
		t.Fatalf("Discover should not error for nonexistent dirs, got: %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(apps))
	}
}

func TestDiscoverMultipleDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	createFakeApp(t, dir1, "App1", plistData{
		BundleName:         "App1",
		BundleID:           "com.example.app1",
		ShortVersionString: "1.0.0",
		BundleVersion:      "1",
	}, false, false)

	createFakeApp(t, dir2, "App2", plistData{
		BundleName:         "App2",
		BundleID:           "com.example.app2",
		ShortVersionString: "2.0.0",
		BundleVersion:      "2",
	}, false, false)

	apps, err := Discover(dir1, dir2)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %d", len(apps))
	}
}

func TestClassifySource_Electron(t *testing.T) {
	dir := t.TempDir()

	// Create an Electron app (has Electron Framework, no Sparkle, no MAS)
	appPath := createFakeApp(t, dir, "Notion", plistData{
		BundleName:         "Notion",
		BundleID:           "notion.id",
		ShortVersionString: "3.0.0",
		BundleVersion:      "300",
	}, false, false)

	// Add Electron Framework
	electronDir := filepath.Join(appPath, "Contents", "Frameworks", "Electron Framework.framework")
	if err := os.MkdirAll(electronDir, 0o755); err != nil {
		t.Fatal(err)
	}

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Source != SourceElectron {
		t.Errorf("expected source %q, got %q", SourceElectron, apps[0].Source)
	}
}

func TestClassifySource_Adobe(t *testing.T) {
	dir := t.TempDir()

	createFakeApp(t, dir, "Photoshop", plistData{
		BundleName:         "Adobe Photoshop 2026",
		BundleID:           "com.adobe.Photoshop",
		ShortVersionString: "26.0",
		BundleVersion:      "26.0.0",
	}, false, false)

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Source != SourceAdobe {
		t.Errorf("expected source %q, got %q", SourceAdobe, apps[0].Source)
	}
}

func TestIsSetappPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Applications/Setapp/Paste.app", true},
		{"/Users/test/Applications/Setapp/CleanMyMac.app", true},
		{"/Applications/Firefox.app", false},
		{"/Applications/SetappHelper.app", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isSetappPath(tt.path); got != tt.want {
				t.Errorf("isSetappPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsToolboxPath(t *testing.T) {
	// Create a fake Toolbox target directory
	tmpDir := t.TempDir()
	toolboxTarget := filepath.Join(tmpDir, "JetBrains", "Toolbox", "apps", "IDEA", "ch-0", "241.0", "IntelliJ IDEA.app")
	if err := os.MkdirAll(toolboxTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink pointing to the Toolbox target
	symlinkPath := filepath.Join(tmpDir, "IntelliJ IDEA.app")
	if err := os.Symlink(toolboxTarget, symlinkPath); err != nil {
		t.Fatal(err)
	}

	if !isToolboxPath(symlinkPath) {
		t.Error("expected isToolboxPath to be true for symlink to Toolbox dir")
	}

	// Non-Toolbox path should return false
	regularPath := filepath.Join(tmpDir, "regular.app")
	if err := os.MkdirAll(regularPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if isToolboxPath(regularPath) {
		t.Error("expected isToolboxPath to be false for regular path")
	}
}

func TestEnrichElectronApp_GitHub(t *testing.T) {
	dir := t.TempDir()

	appPath := createFakeApp(t, dir, "MyElectron", plistData{
		BundleName:         "MyElectron",
		BundleID:           "com.example.electron",
		ShortVersionString: "1.0.0",
		BundleVersion:      "1",
	}, false, false)

	// Add Electron Framework
	contentsDir := filepath.Join(appPath, "Contents")
	electronDir := filepath.Join(contentsDir, "Frameworks", "Electron Framework.framework")
	if err := os.MkdirAll(electronDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write app-update.yml with GitHub provider
	resourceDir := filepath.Join(contentsDir, "Resources")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "provider: github\nowner: myorg\nrepo: myapp\n"
	if err := os.WriteFile(filepath.Join(resourceDir, "app-update.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	a := apps[0]
	if a.Source != SourceGitHub {
		t.Errorf("expected source %q, got %q", SourceGitHub, a.Source)
	}
	if a.GitHubRepo != "myorg/myapp" {
		t.Errorf("expected GitHubRepo %q, got %q", "myorg/myapp", a.GitHubRepo)
	}
}

func TestEnrichElectronApp_Generic(t *testing.T) {
	dir := t.TempDir()

	appPath := createFakeApp(t, dir, "Notion", plistData{
		BundleName:         "Notion",
		BundleID:           "notion.id",
		ShortVersionString: "3.0.0",
		BundleVersion:      "300",
	}, false, false)

	// Add Electron Framework
	contentsDir := filepath.Join(appPath, "Contents")
	electronDir := filepath.Join(contentsDir, "Frameworks", "Electron Framework.framework")
	if err := os.MkdirAll(electronDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write app-update.yml with generic provider
	resourceDir := filepath.Join(contentsDir, "Resources")
	if err := os.MkdirAll(resourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "provider: generic\nurl: https://desktop-release.notion-static.com\n"
	if err := os.WriteFile(filepath.Join(resourceDir, "app-update.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	a := apps[0]
	if a.Source != SourceElectron {
		t.Errorf("expected source %q, got %q", SourceElectron, a.Source)
	}
	if a.ElectronUpdateURL != "https://desktop-release.notion-static.com" {
		t.Errorf("expected ElectronUpdateURL %q, got %q", "https://desktop-release.notion-static.com", a.ElectronUpdateURL)
	}
}

func TestEnrichElectronApp_NoYML(t *testing.T) {
	dir := t.TempDir()

	appPath := createFakeApp(t, dir, "PlainElectron", plistData{
		BundleName:         "PlainElectron",
		BundleID:           "com.example.plain",
		ShortVersionString: "1.0.0",
		BundleVersion:      "1",
	}, false, false)

	// Add Electron Framework but no app-update.yml
	electronDir := filepath.Join(appPath, "Contents", "Frameworks", "Electron Framework.framework")
	if err := os.MkdirAll(electronDir, 0o755); err != nil {
		t.Fatal(err)
	}

	apps, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	a := apps[0]
	if a.Source != SourceElectron {
		t.Errorf("expected source %q, got %q", SourceElectron, a.Source)
	}
	if a.ElectronUpdateURL != "" {
		t.Errorf("expected empty ElectronUpdateURL, got %q", a.ElectronUpdateURL)
	}
	if a.GitHubRepo != "" {
		t.Errorf("expected empty GitHubRepo, got %q", a.GitHubRepo)
	}
}

func TestClassifySource_SetappInDiscover(t *testing.T) {
	// Create a Setapp-style directory structure
	dir := t.TempDir()
	setappDir := filepath.Join(dir, "Setapp")
	if err := os.MkdirAll(setappDir, 0o755); err != nil {
		t.Fatal(err)
	}

	createFakeApp(t, setappDir, "Paste", plistData{
		BundleName:         "Paste",
		BundleID:           "com.widetechnologies.paste",
		ShortVersionString: "3.0.0",
		BundleVersion:      "300",
	}, false, false)

	apps, err := Discover(setappDir)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Source != SourceSetapp {
		t.Errorf("expected source %q, got %q", SourceSetapp, apps[0].Source)
	}
}

func TestToCaskName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Visual Studio Code", "visual-studio-code"},
		{"Google Chrome", "google-chrome"},
		{"iTerm2", "iterm2"},
		{"Firefox", "firefox"},
		{"Arc Browser", "arc-browser"},
		{"1Password 7", "1password-7"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ToCaskName(tt.input)
			if got != tt.want {
				t.Errorf("ToCaskName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
