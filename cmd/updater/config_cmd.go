package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lu-zhengda/updater/internal/config"
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

var flagConfigJSON bool

func init() {
	configCmd.PersistentFlags().BoolVar(&flagConfigJSON, "json", false, "output as JSON")
	configCmd.AddCommand(configExportCmd)
	configCmd.AddCommand(configImportCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigExport(cmd *cobra.Command, args []string) error {
	useJSON := jsonOutputEnabled(flagConfigJSON)

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if len(args) == 1 {
		var data []byte
		if useJSON {
			data, err = json.MarshalIndent(cfg, "", "  ")
		} else {
			data, err = yaml.Marshal(cfg)
		}
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		if err := os.WriteFile(args[0], data, 0o644); err != nil {
			return fmt.Errorf("failed to write file: %w", err)
		}

		if useJSON {
			return writeJSON(cmd, map[string]any{
				"status": "exported",
				"format": "json",
				"path":   args[0],
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Config exported to %s\n", args[0])
		return nil
	}

	if useJSON {
		return writeJSON(cmd, cfg)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	_, err = cmd.OutOrStdout().Write(data)
	return err
}

func runConfigImport(cmd *cobra.Command, args []string) error {
	useJSON := jsonOutputEnabled(flagConfigJSON)

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

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"status": "imported",
			"path":   args[0],
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Config imported and merged successfully.")
	return nil
}
