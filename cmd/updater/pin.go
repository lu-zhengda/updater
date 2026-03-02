package main

import (
	"fmt"

	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var pinCmd = &cobra.Command{
	Use:   "pin <app-name>",
	Short: "Pin an app to prevent automatic updates",
	Long:  "Pinned apps show update status but are skipped during 'update --all'.",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <app-name>",
	Short: "Unpin an app to allow automatic updates",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runUnpin,
}

var (
	flagPinJSON   bool
	flagUnpinJSON bool
)

func init() {
	pinCmd.Flags().BoolVar(&flagPinJSON, "json", false, "output as JSON")
	unpinCmd.Flags().BoolVar(&flagUnpinJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(pinCmd)
	rootCmd.AddCommand(unpinCmd)
}

func runPin(cmd *cobra.Command, args []string) error {
	name := joinAppNameArgs(args)
	cfgPath := config.DefaultPath()
	useJSON := jsonOutputEnabled(flagPinJSON)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	selected, err := resolveAppSelection(apps, name)
	if err != nil {
		return fmt.Errorf("%w. Run 'updater scan' to see available apps", err)
	}
	bundleID := selected.BundleID

	if cfg.IsPinned(bundleID) {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action":    "pin",
				"status":    "already_pinned",
				"changed":   false,
				"app":       selected.Name,
				"bundle_id": bundleID,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is already pinned\n", selected.Name)
		return nil
	}

	cfg.Pin(bundleID)
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"action":    "pin",
			"status":    "pinned",
			"changed":   true,
			"app":       selected.Name,
			"bundle_id": bundleID,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Pinned %s (%s)\n", selected.Name, bundleID)
	return nil
}

func runUnpin(cmd *cobra.Command, args []string) error {
	name := joinAppNameArgs(args)
	cfgPath := config.DefaultPath()
	useJSON := jsonOutputEnabled(flagUnpinJSON)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	selected, err := resolveAppSelection(apps, name)
	if err != nil {
		return fmt.Errorf("%w. Run 'updater scan' to see available apps", err)
	}
	bundleID := selected.BundleID

	if !cfg.IsPinned(bundleID) {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action":    "unpin",
				"status":    "not_pinned",
				"changed":   false,
				"app":       selected.Name,
				"bundle_id": bundleID,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is not pinned\n", selected.Name)
		return nil
	}

	cfg.Unpin(bundleID)
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"action":    "unpin",
			"status":    "unpinned",
			"changed":   true,
			"app":       selected.Name,
			"bundle_id": bundleID,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Unpinned %s (%s)\n", selected.Name, bundleID)
	return nil
}
