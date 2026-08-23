package cli

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var (
	configFile string
	verbose    bool
)

// RootCmd is the base command for the miner CLI.
var RootCmd = &cobra.Command{
	Use:   "miner",
	Short: "Deterministic PR Cohort Miner for Stateful / Distributed Systems",
	Long: `A reproducible research tool to harvest, score, and correlate historical pull requests
for distributed/state-machine defects with retrospective corrective evidence.`,
}

func init() {
	// Automatically load .env file if present
	_ = godotenv.Load()

	RootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "configs/frr_heuristics.yaml", "Path to YAML configuration file")
	RootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")

	RootCmd.AddCommand(collectCmd)
	RootCmd.AddCommand(rebuildRawCmd)
	RootCmd.AddCommand(correlateCmd)
	RootCmd.AddCommand(finalizeBatchesCmd)
	RootCmd.AddCommand(scoreCmd)
	RootCmd.AddCommand(exportCmd)
	RootCmd.AddCommand(inspectCmd)
	RootCmd.AddCommand(statsCmd)
}

// Execute runs the root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
