package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/lu-zhengda/updater/internal/checker"
)

// Metadata describes a backup.
type Metadata struct {
	AppName    string    `json:"app_name"`
	BundleID   string    `json:"bundle_id"`
	Version    string    `json:"version"`
	BackupPath string    `json:"backup_path"`
	BackupDate time.Time `json:"backup_date"`
	OrigPath   string    `json:"orig_path"`
}

// Manager handles app backups for rollback support.
type Manager struct {
	baseDir    string
	maxBackups int
	runner     checker.CmdRunner
}

// NewManager creates a new backup manager.
// baseDir is the root directory for storing backups (e.g. ~/.local/share/updater/backups).
func NewManager(baseDir string, maxBackups int, runner checker.CmdRunner) *Manager {
	if maxBackups <= 0 {
		maxBackups = 1
	}
	return &Manager{
		baseDir:    baseDir,
		maxBackups: maxBackups,
		runner:     runner,
	}
}

// DefaultBaseDir returns the default backup directory.
func DefaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "updater", "backups")
}

// Backup creates a backup of the app at appPath.
func (m *Manager) Backup(ctx context.Context, appName, bundleID, version, appPath string) error {
	timestamp := time.Now().Format("20060102-150405")
	safeName := sanitizeName(appName)
	backupDir := filepath.Join(m.baseDir, safeName, timestamp)

	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("failed to create backup dir: %w", err)
	}

	// Copy app bundle preserving code signatures.
	dest := filepath.Join(backupDir, filepath.Base(appPath))
	_, err := m.runner.Run(ctx, "cp", "-a", appPath, dest)
	if err != nil {
		return fmt.Errorf("failed to copy app for backup: %w", err)
	}

	// Write metadata.
	meta := Metadata{
		AppName:    appName,
		BundleID:   bundleID,
		Version:    version,
		BackupPath: dest,
		BackupDate: time.Now(),
		OrigPath:   appPath,
	}

	metaPath := filepath.Join(backupDir, "metadata.json")
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	// Prune old backups.
	m.pruneOldBackups(safeName)

	return nil
}

// Restore restores the most recent backup for the given app.
func (m *Manager) Restore(ctx context.Context, appName string) error {
	meta, err := m.latestBackup(appName)
	if err != nil {
		return err
	}

	// Verify backup exists.
	if _, err := os.Stat(meta.BackupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file missing: %s", meta.BackupPath)
	}

	// Stage beside the original so cp cannot nest the backup inside an existing
	// bundle. The original is removed only after the complete backup is staged.
	staging := meta.OrigPath + ".updater-rollback-staging"
	_, _ = m.runner.Run(ctx, "rm", "-rf", staging)
	if _, err = m.runner.Run(ctx, "cp", "-a", meta.BackupPath, staging); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}
	if _, err = m.runner.Run(ctx, "rm", "-rf", meta.OrigPath); err != nil {
		_, _ = m.runner.Run(ctx, "rm", "-rf", staging)
		return fmt.Errorf("failed to remove damaged app during rollback: %w", err)
	}
	if _, err = m.runner.Run(ctx, "mv", staging, meta.OrigPath); err != nil {
		return fmt.Errorf("failed to move restored app into place: %w", err)
	}

	return nil
}

// List returns all backups for the given app, sorted newest first.
func (m *Manager) List(appName string) ([]Metadata, error) {
	safeName := sanitizeName(appName)
	appDir := filepath.Join(m.baseDir, safeName)

	entries, err := os.ReadDir(appDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read backup dir: %w", err)
	}

	var backups []Metadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(appDir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue // skip incomplete backups
		}
		var meta Metadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		backups = append(backups, meta)
	}

	// Sort newest first.
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].BackupDate.After(backups[j].BackupDate)
	})

	return backups, nil
}

// HasBackup returns true if there is at least one backup for the app.
func (m *Manager) HasBackup(appName string) bool {
	backups, err := m.List(appName)
	if err != nil {
		return false
	}
	return len(backups) > 0
}

// latestBackup returns the most recent backup metadata for the app.
func (m *Manager) latestBackup(appName string) (*Metadata, error) {
	backups, err := m.List(appName)
	if err != nil {
		return nil, err
	}
	if len(backups) == 0 {
		return nil, fmt.Errorf("no backups found for %s", appName)
	}
	return &backups[0], nil
}

// pruneOldBackups removes old backups exceeding maxBackups for the app.
func (m *Manager) pruneOldBackups(safeName string) {
	appDir := filepath.Join(m.baseDir, safeName)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return
	}

	// Collect timestamp directories, sorted alphabetically (oldest first).
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)

	// Remove oldest until we're within limit.
	for len(dirs) > m.maxBackups {
		oldest := dirs[0]
		os.RemoveAll(filepath.Join(appDir, oldest))
		dirs = dirs[1:]
	}
}

// sanitizeName converts an app name to a safe directory name.
func sanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			return unicode.ToLower(r)
		default:
			return '-'
		}
	}, name)
	name = strings.Trim(name, ".-")
	if name == "" || name == "." || name == ".." {
		return "app"
	}
	return name
}
