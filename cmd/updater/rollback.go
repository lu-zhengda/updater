package main

import (
	"fmt"
	"strings"

	"github.com/luzhengda/updater/internal/backup"
	"github.com/luzhengda/updater/internal/checker"
	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var rollbackCmd = &cobra.Command{
	Use:   "rollback <app-name>",
	Short: "Restore an app to its previous version",
	Args:  cobra.ExactArgs(1),
	RunE:  runRollback,
}

func init() {
	rootCmd.AddCommand(rollbackCmd)
}

func runRollback(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	name := args[0]

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	runner := &checker.RealCmdRunner{}
	bm := backup.NewManager(backup.DefaultBaseDir(), cfg.MaxBackupsOrDefault(), runner)

	// Verify backup exists.
	backups, err := bm.List(name)
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}
	if len(backups) == 0 {
		return fmt.Errorf("no backups found for %q", name)
	}

	latest := backups[0]
	fmt.Fprintf(cmd.OutOrStdout(), "Restoring %s to version %s (backed up %s)...\n",
		latest.AppName, latest.Version, latest.BackupDate.Format("2006-01-02 15:04"))

	// Quit the app if running.
	apps, err := discoverApps()
	if err == nil {
		for _, a := range apps {
			if strings.EqualFold(a.Name, name) {
				quitAppIfRunning(ctx, a, runner)
				break
			}
		}
	}

	if err := bm.Restore(ctx, name); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully restored %s to version %s\n", latest.AppName, latest.Version)
	return nil
}
