package main

import (
	"fmt"
	"strings"

	"github.com/lu-zhengda/updater/internal/config"
	"github.com/spf13/cobra"
)

var policyCmd = &cobra.Command{
	Use:   "policy <app-name> <auto|manual|notify-only|clear>",
	Short: "Set update policy for an app",
	Long: `Set per-app update policies to control how updates are handled.

Policies:
  auto         Always update automatically (--auto and --all)
  manual       Only update when explicitly named
  notify-only  Show in notifications but skip in --all and --auto
  clear        Remove the policy (revert to default behavior)`,
	Args: cobra.MinimumNArgs(2),
	RunE: runPolicy,
}

func init() {
	rootCmd.AddCommand(policyCmd)
}

func runPolicy(cmd *cobra.Command, args []string) error {
	appName := joinAppNameArgs(args[:len(args)-1])
	policy := strings.ToLower(args[len(args)-1])

	validPolicies := map[string]bool{
		config.PolicyAuto: true, config.PolicyManual: true, config.PolicyNotifyOnly: true, "clear": true,
	}
	if !validPolicies[policy] {
		return fmt.Errorf("invalid policy %q: must be auto, manual, notify-only, or clear", policy)
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Find app by name.
	apps, err := discoverApps()
	if err != nil {
		return err
	}

	selected, err := resolveAppSelection(apps, appName)
	if err != nil {
		return fmt.Errorf("%w. Run 'updater scan' to see available apps", err)
	}
	bundleID := selected.BundleID

	if policy == "clear" {
		cfg.RemovePolicy(bundleID)
		fmt.Fprintf(cmd.OutOrStdout(), "Cleared policy for %s\n", selected.Name)
	} else {
		cfg.SetPolicy(bundleID, policy)
		fmt.Fprintf(cmd.OutOrStdout(), "Set policy for %s to %s\n", selected.Name, policy)
	}

	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}
