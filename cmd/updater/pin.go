package main

import (
	"fmt"
	"strings"

	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var pinCmd = &cobra.Command{
	Use:   "pin <app-name>",
	Short: "Pin an app to prevent automatic updates",
	Long:  "Pinned apps show update status but are skipped during 'update --all'.",
	Args:  cobra.ExactArgs(1),
	RunE:  runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <app-name>",
	Short: "Unpin an app to allow automatic updates",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnpin,
}

func init() {
	rootCmd.AddCommand(pinCmd)
	rootCmd.AddCommand(unpinCmd)
}

func runPin(cmd *cobra.Command, args []string) error {
	name := args[0]
	cfgPath := config.DefaultPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	// Find app by name to resolve bundle ID.
	var bundleID string
	for _, a := range apps {
		if strings.EqualFold(a.Name, name) {
			bundleID = a.BundleID
			break
		}
	}
	if bundleID == "" {
		return fmt.Errorf("app %q not found. Run 'updater scan' to see available apps", name)
	}

	if cfg.IsPinned(bundleID) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is already pinned\n", name)
		return nil
	}

	cfg.Pin(bundleID)
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Pinned %s (%s)\n", name, bundleID)
	return nil
}

func runUnpin(cmd *cobra.Command, args []string) error {
	name := args[0]
	cfgPath := config.DefaultPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	var bundleID string
	for _, a := range apps {
		if strings.EqualFold(a.Name, name) {
			bundleID = a.BundleID
			break
		}
	}
	if bundleID == "" {
		return fmt.Errorf("app %q not found. Run 'updater scan' to see available apps", name)
	}

	if !cfg.IsPinned(bundleID) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s is not pinned\n", name)
		return nil
	}

	cfg.Unpin(bundleID)
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Unpinned %s (%s)\n", name, bundleID)
	return nil
}
