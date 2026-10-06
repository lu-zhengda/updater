package app

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Discover feeds from structured, packaged metadata, even when the framework or
// app is renamed. This works for apps beyond Codex without scanning executable
// code for URLs or guessing a stable feed for beta installations.
func enrichElectronMetadata(contentsDir string, a *App) {
	data, _ := readElectronJSON(contentsDir, "package.json")
	var pkg map[string]json.RawMessage
	_ = json.Unmarshal(data, &pkg)
	feed := a.FeedURL
	ambiguous := false
	if feed == "" {
		for key, raw := range pkg {
			// Includes sparkleFeedUrl and app-prefixed fields such as
			// codexSparkleFeedUrl. Conflicting channel URLs are not guessed.
			if !strings.HasSuffix(strings.ToLower(key), "sparklefeedurl") {
				continue
			}
			var candidate string
			if json.Unmarshal(raw, &candidate) != nil || !secureFeedURL(candidate) {
				continue
			}
			if feed != "" && feed != candidate {
				ambiguous = true
			}
			feed = candidate
		}
	}
	if feed != "" && !ambiguous && secureFeedURL(feed) {
		a.FeedURL = feed
		a.Source = SourceSparkle
		return
	}
	if a.ElectronUpdateURL != "" {
		return
	}
	data, _ = readElectronJSON(contentsDir, "product.json")
	var product struct {
		URL     string `json:"updateUrl"`
		Quality string `json:"quality"`
	}
	if json.Unmarshal(data, &product) == nil && secureFeedURL(product.URL) &&
		(product.Quality == "stable" || product.Quality == "insider") {
		a.ElectronUpdateURL = product.URL
		a.ElectronUpdateFormat = "vscode"
		a.ElectronUpdateChannel = product.Quality
		return
	}
	// Claude's Squirrel feed is configured in executable code, not a metadata
	// file. Only its discovery is app-specific; parsing and installation use
	// the same RELEASES.json protocol as other Squirrel.Mac apps.
	if a.BundleID == "com.anthropic.claudefordesktop" {
		a.ElectronUpdateURL = "https://downloads.claude.ai/releases/darwin/universal/RELEASES.json"
		a.ElectronUpdateFormat = "squirrel"
	}
}

func secureFeedURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func readElectronJSON(contentsDir, name string) ([]byte, error) {
	resources := filepath.Join(contentsDir, "Resources")
	if data, err := readASARJSON(filepath.Join(resources, "app.asar"), name); err == nil {
		return data, nil
	}
	f, err := os.Open(filepath.Join(resources, "app", name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readSmallJSON(f)
}

func readSmallJSON(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("Electron metadata exceeds 1 MiB")
	}
	return data, err
}

// Read only a root JSON file from an ASAR, without extracting the archive
// or scanning hundreds of megabytes of executable code. All lengths and offsets
// come from the bundle and are bounded before allocating or reading.
func readASARJSON(path, name string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var prefix [16]byte
	if _, err := io.ReadFull(f, prefix[:]); err != nil {
		return nil, err
	}
	headerSize := int64(binary.LittleEndian.Uint32(prefix[4:8]))
	jsonSize := int64(binary.LittleEndian.Uint32(prefix[12:16]))
	if binary.LittleEndian.Uint32(prefix[:4]) != 4 || headerSize < 8 || headerSize > 16<<20 ||
		jsonSize == 0 || jsonSize > headerSize-8 || 8+headerSize > stat.Size() {
		return nil, fmt.Errorf("invalid ASAR header")
	}
	header := make([]byte, jsonSize)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, err
	}
	var index struct {
		Files map[string]struct {
			Size     int64  `json:"size"`
			Offset   string `json:"offset"`
			Unpacked bool   `json:"unpacked"`
		} `json:"files"`
	}
	if err := json.Unmarshal(header, &index); err != nil {
		return nil, err
	}
	entry, ok := index.Files[name]
	if !ok || entry.Size <= 0 || entry.Size > 1<<20 {
		return nil, fmt.Errorf("missing or invalid ASAR metadata")
	}
	if entry.Unpacked {
		unpacked, err := os.Open(filepath.Join(path+".unpacked", name))
		if err != nil {
			return nil, err
		}
		defer unpacked.Close()
		return readSmallJSON(unpacked)
	}
	offset, err := strconv.ParseInt(entry.Offset, 10, 64)
	available := stat.Size() - 8 - headerSize
	if err != nil || offset < 0 || entry.Size > available || offset > available-entry.Size {
		return nil, fmt.Errorf("invalid ASAR metadata offset")
	}
	data := make([]byte, entry.Size)
	_, err = f.ReadAt(data, 8+headerSize+offset)
	return data, err
}
