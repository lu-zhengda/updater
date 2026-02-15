package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry records a single app update event.
type Entry struct {
	AppName     string    `json:"app_name"`
	BundleID    string    `json:"bundle_id"`
	FromVersion string    `json:"from_version"`
	ToVersion   string    `json:"to_version"`
	Source      string    `json:"source"`
	Timestamp   time.Time `json:"timestamp"`
	Success     bool      `json:"success"`
	RolledBack  bool      `json:"rolled_back,omitempty"`
}

// DefaultPath returns the default history file path (~/.local/share/updater/history.json).
// It is a variable so tests can override it.
var DefaultPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "updater", "history.json")
}

// Append adds an entry to the history file at path.
// Creates the file and parent directories if they don't exist.
func Append(path string, entry Entry) error {
	entries, err := List(path)
	if err != nil {
		return err
	}

	entries = append(entries, entry)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create history dir: %w", err)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write history: %w", err)
	}

	return nil
}

// List returns all history entries from the file at path.
// Returns nil (not an error) if the file does not exist.
// Returns an error if the file exists but contains invalid JSON.
func List(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read history: %w", err)
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse history: %w", err)
	}

	return entries, nil
}
