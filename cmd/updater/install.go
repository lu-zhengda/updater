package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install <name>",
	Short: "Install an app via Homebrew Cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runInstall,
}

var flagInstallJSON bool

func init() {
	installCmd.Flags().BoolVar(&flagInstallJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	query := args[0]
	runner := &checker.RealCmdRunner{}
	useJSON := jsonOutputEnabled(flagInstallJSON)

	matches, err := searchCasks(ctx, runner, query)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"query":   query,
				"status":  "no_match",
				"matches": []string{},
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "No casks found matching %q\n", query)
		return nil
	}

	var selected string
	if len(matches) == 1 {
		selected = matches[0]
	} else {
		if useJSON {
			return writeJSON(cmd, map[string]any{
				"query":   query,
				"status":  "multiple_matches",
				"matches": matches,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Multiple casks found:\n")
		for i, m := range matches {
			fmt.Fprintf(cmd.OutOrStdout(), "  %d) %s\n", i+1, m)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Select a cask (1-%d): ", len(matches))

		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		idx, err := selectCask(matches, strings.TrimSpace(line))
		if err != nil {
			return err
		}
		selected = matches[idx]
	}

	if !useJSON {
		fmt.Fprintf(cmd.OutOrStdout(), "Installing %s...\n", selected)
	}
	output, err := runner.Run(ctx, "brew", "install", "--cask", selected)
	if err != nil {
		return fmt.Errorf("failed to install %s: %w", selected, err)
	}

	if useJSON {
		return writeJSON(cmd, map[string]any{
			"query":    query,
			"selected": selected,
			"status":   "installed",
			"output":   strings.TrimSpace(string(output)),
		})
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(output))
	return nil
}

// searchCasks runs `brew search --cask <query>` and parses the results.
func searchCasks(ctx context.Context, runner checker.CmdRunner, query string) ([]string, error) {
	output, err := runner.Run(ctx, "brew", "search", "--cask", query)
	if err != nil {
		return nil, fmt.Errorf("failed to search casks: %w", err)
	}

	var results []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip header lines like "==> Casks"
		if strings.HasPrefix(line, "==>") {
			continue
		}
		results = append(results, line)
	}
	return results, nil
}

// selectCask parses a user input string as a 1-based index into the matches list.
// Returns the 0-based index.
func selectCask(matches []string, input string) (int, error) {
	n, err := strconv.Atoi(input)
	if err != nil || n < 1 || n > len(matches) {
		return 0, fmt.Errorf("invalid selection: %q (must be 1-%d)", input, len(matches))
	}
	return n - 1, nil
}
