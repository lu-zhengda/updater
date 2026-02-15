package main

import (
	"fmt"
	"os"

	"github.com/luzhengda/updater/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage updater configuration",
}

var configExportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export current configuration to YAML",
	Long:  "Export current configuration to stdout, or to a file if specified.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigExport,
}

var configImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import and merge configuration from a YAML file",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigImport,
}

func init() {
	configCmd.AddCommand(configExportCmd)
	configCmd.AddCommand(configImportCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigExport(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if len(args) == 1 {
		if err := os.WriteFile(args[0], data, 0o644); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Config exported to %s\n", args[0])
		return nil
	}

	_, err = cmd.OutOrStdout().Write(data)
	return err
}

func runConfigImport(cmd *cobra.Command, args []string) error {
	importData, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("failed to read import file: %w", err)
	}

	var imported config.Config
	if err := yaml.Unmarshal(importData, &imported); err != nil {
		return fmt.Errorf("failed to parse import file: %w", err)
	}

	current, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load current config: %w", err)
	}

	merged := config.Merge(current, &imported)

	if err := merged.Save(config.DefaultPath()); err != nil {
		return fmt.Errorf("failed to save merged config: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Config imported and merged successfully.")
	return nil
}
