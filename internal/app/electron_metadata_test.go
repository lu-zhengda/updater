package app

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeCodexArchive(t *testing.T, path, feed string) {
	t.Helper()
	pkg, _ := json.Marshal(map[string]string{"codexSparkleFeedUrl": feed})
	writeElectronArchive(t, path, "package.json", pkg)
}

func writeElectronArchive(t *testing.T, path, name string, pkg []byte) {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"files": map[string]any{
		name: map[string]any{"size": len(pkg), "offset": "0"},
	}})
	// ASAR uses a size pickle followed by a pickle containing the JSON string.
	headerSize := (8 + len(header) + 3) &^ 3
	data := make([]byte, 8+headerSize)
	binary.LittleEndian.PutUint32(data[0:4], 4)
	binary.LittleEndian.PutUint32(data[4:8], uint32(headerSize))
	binary.LittleEndian.PutUint32(data[8:12], uint32(headerSize-4))
	binary.LittleEndian.PutUint32(data[12:16], uint32(len(header)))
	copy(data[16:], header)
	if err := os.WriteFile(path, append(data, pkg...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverCodexPackagedSparkleFeed(t *testing.T) {
	for _, feed := range []string{
		"https://persistent.oaistatic.com/codex-app-prod/appcast.xml",
		"https://persistent.oaistatic.com/codex-app-beta/appcast.xml",
	} {
		t.Run(feed, func(t *testing.T) {
			dir := t.TempDir()
			p := createFakeApp(t, dir, "Renamed Codex", plistData{BundleID: "com.openai.codex", ShortVersionString: "26.930.41038"}, false, false)
			resources := filepath.Join(p, "Contents", "Resources")
			if err := os.MkdirAll(resources, 0o755); err != nil {
				t.Fatal(err)
			}
			// The installed app has a renamed framework, so discovery must also
			// recognize its archive without Electron Framework.framework.
			writeCodexArchive(t, filepath.Join(resources, "app.asar"), feed)
			apps, err := Discover(dir)
			if err != nil || len(apps) != 1 {
				t.Fatalf("Discover: %v, %v", apps, err)
			}
			if apps[0].Source != SourceSparkle || apps[0].FeedURL != feed {
				t.Fatalf("Codex's packaged feed was missed: source=%s feed=%q", apps[0].Source, apps[0].FeedURL)
			}
		})
	}
}

func TestDiscoverEmbeddedFeedForUnrelatedApp(t *testing.T) {
	for _, packed := range []bool{true, false} {
		dir := t.TempDir()
		p := createFakeApp(t, dir, "Another App", plistData{BundleID: "org.example.another"}, false, false)
		resources := filepath.Join(p, "Contents", "Resources")
		if err := os.MkdirAll(filepath.Join(resources, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(p, "Contents", "Frameworks", "Electron Framework.framework"), 0o755); err != nil {
			t.Fatal(err)
		}
		pkg := []byte(`{"anotherSparkleFeedURL":"https://example.com/beta/appcast.xml"}`)
		if packed {
			writeElectronArchive(t, filepath.Join(resources, "app.asar"), "package.json", pkg)
		} else if err := os.WriteFile(filepath.Join(resources, "app", "package.json"), pkg, 0o644); err != nil {
			t.Fatal(err)
		}
		apps, err := Discover(dir)
		if err != nil || len(apps) != 1 || apps[0].Source != SourceSparkle || apps[0].FeedURL != "https://example.com/beta/appcast.xml" {
			t.Fatalf("packed=%v apps=%+v err=%v", packed, apps, err)
		}
	}
}

func TestEmbeddedSparkleMetadataRejectsAmbiguousOrInsecureFeeds(t *testing.T) {
	for _, pkg := range []string{
		`{"sparkleFeedUrl":"http://example.com/appcast.xml"}`,
		`{"prodSparkleFeedUrl":"https://example.com/stable.xml","betaSparkleFeedUrl":"https://example.com/beta.xml"}`,
		`{"sparkleFeedUrl":42}`,
	} {
		contents := t.TempDir()
		resources := filepath.Join(contents, "Resources")
		if err := os.MkdirAll(resources, 0o755); err != nil {
			t.Fatal(err)
		}
		writeElectronArchive(t, filepath.Join(resources, "app.asar"), "package.json", []byte(pkg))
		a := &App{Source: SourceElectron}
		enrichElectronMetadata(contents, a)
		if a.FeedURL != "" || a.Source != SourceElectron {
			t.Fatalf("unsafe feed accepted: %+v", a)
		}
	}
}

func TestDiscoverNativeElectronProtocols(t *testing.T) {
	for _, id := range []string{"com.microsoft.VSCode", "org.example.codefork", "com.anthropic.claudefordesktop"} {
		dir := t.TempDir()
		p := createFakeApp(t, dir, "Native App", plistData{BundleID: id}, false, false)
		if err := os.MkdirAll(filepath.Join(p, "Contents", "Frameworks", "Electron Framework.framework"), 0o755); err != nil {
			t.Fatal(err)
		}
		if id != "com.anthropic.claudefordesktop" {
			resources := filepath.Join(p, "Contents", "Resources")
			if err := os.MkdirAll(resources, 0o755); err != nil {
				t.Fatal(err)
			}
			writeElectronArchive(t, filepath.Join(resources, "app.asar"), "product.json", []byte(`{"updateUrl":"https://example.com/updates","quality":"insider"}`))
		}
		apps, err := Discover(dir)
		if err != nil || len(apps) != 1 {
			t.Fatalf("Discover: %v", err)
		}
		a := apps[0]
		if id == "com.anthropic.claudefordesktop" {
			if a.ElectronUpdateFormat != "squirrel" || a.ElectronUpdateURL != "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json" {
				t.Fatalf("Claude feed: %+v", a)
			}
		} else if a.ElectronUpdateFormat != "vscode" || a.ElectronUpdateChannel != "insider" || a.ElectronUpdateURL != "https://example.com/updates" {
			t.Fatalf("product feed: %+v", a)
		}
	}
}

func TestASARRejectsInvalidLengths(t *testing.T) {
	for _, corrupt := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:12] },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:8], ^uint32(0)); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[12:16], ^uint32(0)); return b },
		func(b []byte) []byte { return b[:len(b)-1] },
	} {
		p := filepath.Join(t.TempDir(), "app.asar")
		writeCodexArchive(t, p, "https://example.com/appcast.xml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, corrupt(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readASARJSON(p, "package.json"); err == nil {
			t.Fatal("corrupt archive accepted")
		}
	}
}
