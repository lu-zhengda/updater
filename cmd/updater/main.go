package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Set via -ldflags at build time.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "updater",
	Short:   "macOS app update manager",
	Long:    "Discover installed macOS apps, check for updates, and update them from multiple sources.",
	Version: version,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
