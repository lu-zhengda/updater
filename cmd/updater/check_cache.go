package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lu-zhengda/updater/internal/checker"
)

// checkCache is the last completed check, persisted so the updates window
// can show something immediately while a fresh check runs.
type checkCache struct {
	CheckedAt time.Time    `json:"checked_at"`
	Entries   []cacheEntry `json:"entries"`
}

type cacheEntry struct {
	Name     string `json:"name"`
	BundleID string `json:"bundle_id"`
	Current  string `json:"current_version"`
	Latest   string `json:"latest_version,omitempty"`
	Source   string `json:"source"`
	Status   string `json:"status"` // update_available | ok | error | pinned
	Error    string `json:"error,omitempty"`
}

func checkCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "updater", "last-check.json")
}

// cacheEntriesFromResults converts check results, marking pinned apps.
func cacheEntriesFromResults(results []*checker.UpdateResult, isPinned func(string) bool) []cacheEntry {
	entries := make([]cacheEntry, 0, len(results))
	for _, r := range results {
		e := cacheEntry{
			Name:     r.App.Name,
			BundleID: r.App.BundleID,
			Current:  r.CurrentVersion,
			Latest:   r.LatestVersion,
			Source:   r.Source,
		}
		switch {
		case r.Error != nil:
			e.Status = "error"
			e.Error = r.Error.Error()
		case r.HasUpdate && isPinned(r.App.BundleID):
			e.Status = "pinned"
		case r.HasUpdate:
			e.Status = "update_available"
		default:
			e.Status = "ok"
		}
		entries = append(entries, e)
	}
	return entries
}

func writeCheckCache(entries []cacheEntry) {
	path := checkCachePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(checkCache{CheckedAt: time.Now(), Entries: entries})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func readCheckCache() *checkCache {
	path := checkCachePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}
