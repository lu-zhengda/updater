package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lu-zhengda/updater/internal/backup"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var flagRollbackAll bool

var rollbackCmd = &cobra.Command{
	Use:   "rollback [app-name]",
	Short: "Restore an app to its previous version",
	Args:  cobra.ArbitraryArgs,
	RunE:  runRollback,
}

func init() {
	rollbackCmd.Flags().BoolVar(&flagRollbackAll, "all", false, "rollback all apps with backups")
	rootCmd.AddCommand(rollbackCmd)
}

func runRollback(cmd *cobra.Command, args []string) error {
	if flagRollbackAll {
		if len(args) > 0 {
			return errors.New("cannot specify app name with --all")
		}
		return rollbackAll(cmd)
	}
	if len(args) == 0 {
		return errors.New("app name required (or use --all)")
	}
	return rollbackSingle(cmd, joinAppNameArgs(args))
}

func rollbackSingle(cmd *cobra.Command, query string) error {
	ctx := cmd.Context()

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := &checker.RealCmdRunner{}
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)

	backupName := query
	var targetAppName string

	// Resolve to discovered app name when possible so aliases/cask-style inputs
	// can still find the right backup directory.
	apps, appErr := discoverApps()
	if appErr == nil {
		if selected, err := resolveAppSelection(apps, query); err == nil {
			backupName = selected.Name
			targetAppName = selected.Name
		}
	}

	// Verify backup exists.
	backups, err := bm.List(backupName)
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}
	if len(backups) == 0 && backupName != query {
		// Backward-compatible fallback for backups created under raw query name.
		backups, err = bm.List(query)
		if err != nil {
			return fmt.Errorf("failed to list backups: %w", err)
		}
		backupName = query
	}
	if len(backups) == 0 {
		return fmt.Errorf("no backups found for %q", query)
	}

	latest := backups[0]
	fmt.Fprintf(cmd.OutOrStdout(), "Restoring %s to version %s (backed up %s)...\n",
		latest.AppName, latest.Version, latest.BackupDate.Format("2006-01-02 15:04"))

	// Quit the app if running.
	if appErr == nil {
		for _, a := range apps {
			if strings.EqualFold(a.Name, targetAppName) {
				quitAppIfRunning(ctx, a, runner)
				break
			}
		}
	}

	if err := bm.Restore(ctx, backupName); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully restored %s to version %s\n", latest.AppName, latest.Version)
	return nil
}

func rollbackAll(cmd *cobra.Command) error {
	ctx := cmd.Context()
	w := cmd.OutOrStdout()

	baseDir := backup.DefaultBaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "No backups found.")
			return nil
		}
		return fmt.Errorf("failed to read backup directory: %w", err)
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := &checker.RealCmdRunner{}
	bm := backup.NewManager(baseDir, cfg.MaxBackupsOrDefault(), runner)

	// Discover installed apps once for quit-if-running lookups.
	installedApps, _ := discoverApps()

	var restored, failed int

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()

		backups, err := bm.List(dirName)
		if err != nil || len(backups) == 0 {
			continue
		}

		latest := backups[0]
		fmt.Fprintf(w, "Restoring %s to version %s...\n", latest.AppName, latest.Version)

		// Quit the app if running.
		for _, a := range installedApps {
			if strings.EqualFold(a.Name, latest.AppName) {
				quitAppIfRunning(ctx, a, runner)
				break
			}
		}

		if err := bm.Restore(ctx, dirName); err != nil {
			fmt.Fprintf(w, "  Failed to restore %s: %v\n", latest.AppName, err)
			failed++
			continue
		}
		restored++
	}

	if restored == 0 && failed == 0 {
		fmt.Fprintln(w, "No backups found.")
		return nil
	}

	fmt.Fprintf(w, "Rolled back %d app(s), %d failed.\n", restored, failed)
	return nil
}
