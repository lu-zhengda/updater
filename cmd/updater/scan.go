package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/luzhengda/updater/internal/checker"
	"github.com/spf13/cobra"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Discover installed apps and their update sources",
	RunE:  runScan,
}

func init() {
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	apps, err := discoverApps()
	if err != nil {
		return err
	}

	runner := &checker.RealCmdRunner{}
	formulaApps, fErr := discoverBrewFormulae(ctx, runner)
	if fErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not discover brew formulae: %v\n", fErr)
	} else {
		apps = append(apps, formulaApps...)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tSOURCE\tBUNDLE ID")
	for _, a := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Version, a.Source, a.BundleID)
	}
	w.Flush()

	fmt.Fprintf(os.Stderr, "\n%d apps discovered\n", len(apps))
	return nil
}
