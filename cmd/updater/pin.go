package main

import (
	"fmt"

	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var pinCmd = &cobra.Command{
	Use:   "pin <name-or-id>",
	Short: "Pin an app to prevent automatic updates",
	Long:  "Pinned apps show update status but are skipped during 'update --all'.",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <name-or-id>",
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

	name, bundleID, err := resolveConfigTarget(cmd.Context(), name, false)
	if err != nil {
		return err
	}

	if cfg.IsPinned(bundleID) {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action":    "pin",
				"status":    "already_pinned",
				"changed":   false,
				"app":       name,
				"bundle_id": bundleID,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is already pinned\n", name)
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
			"app":       name,
			"bundle_id": bundleID,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Pinned %s (%s)\n", name, bundleID)
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

	name, bundleID, err := resolveConfigTarget(cmd.Context(), name, cfg.IsPinned(name))
	if err != nil {
		return err
	}

	if !cfg.IsPinned(bundleID) {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"action":    "unpin",
				"status":    "not_pinned",
				"changed":   false,
				"app":       name,
				"bundle_id": bundleID,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s is not pinned\n", name)
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
			"app":       name,
			"bundle_id": bundleID,
		})
	}

	if name == bundleID {
		fmt.Fprintf(cmd.OutOrStdout(), "Unpinned %s\n", bundleID)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Unpinned %s (%s)\n", name, bundleID)
	}
	return nil
}
