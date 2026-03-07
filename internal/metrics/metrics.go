package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event records a single product metric event.
type Event struct {
	Name             string    `json:"name"`
	Timestamp        time.Time `json:"timestamp"`
	CheckedApps      int       `json:"checked_apps,omitempty"`
	UpdatesAvailable int       `json:"updates_available,omitempty"`
	MajorUpdates     int       `json:"major_updates,omitempty"`
	ErrorCount       int       `json:"error_count,omitempty"`
	FailureReason    string    `json:"failure_reason,omitempty"`
}

// DefaultPath returns the default metrics file path (~/.local/share/updater/metrics.json).
// It is a variable so tests can override it.
var DefaultPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "updater", "metrics.json")
}

// Append adds an event to the metrics file at path.
// Creates the file and parent directories if they don't exist.
func Append(path string, event Event) error {
	events, err := List(path)
	if err != nil {
		return err
	}

	events = append(events, event)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create metrics dir: %w", err)
	}

	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metrics: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write metrics: %w", err)
	}

	return nil
}

// List returns all metric events from the file at path.
// Returns nil (not an error) if the file does not exist.
// Returns an error if the file exists but contains invalid JSON.
func List(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read metrics: %w", err)
	}

	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("failed to parse metrics: %w", err)
	}
	return events, nil
}
