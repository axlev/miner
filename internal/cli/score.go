package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"pr-analysis/internal/config"
	"pr-analysis/internal/heuristics"
	"pr-analysis/internal/storage"
)

var (
	scoreInFlag  string
	scoreOutFlag string
)

var scoreCmd = &cobra.Command{
	Use:   "score",
	Short: "Calculate stateful/distributed heuristic scores for PRs",
	Long:  `Evaluates pre-merge facts against the YAML heuristics configuration, calculating transparent scores and match explanations.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load heuristics config from %q: %w", configFile, err)
		}

		engine, err := heuristics.NewEngine(&cfg.Heuristics)
		if err != nil {
			return fmt.Errorf("failed to initialize heuristics engine: %w", err)
		}

		inFile := "./data/correlated.jsonl"
		if scoreInFlag != "" {
			inFile = scoreInFlag
		}

		outFile := "./data/candidates.jsonl"
		if scoreOutFlag != "" {
			outFile = scoreOutFlag
		}

		records, err := storage.ReadJSONL(inFile)
		if err != nil {
			return fmt.Errorf("failed to read records from %q: %w", inFile, err)
		}

		highCount, medCount, lowCount := 0, 0, 0

		for i := range records {
			records[i].Stateful = engine.Evaluate(&records[i].Original)
			records[i].Provenance.ConfigHash = cfg.ConfigHash

			switch records[i].Stateful.Category {
			case "HIGH":
				highCount++
			case "MEDIUM":
				medCount++
			default:
				lowCount++
			}
		}

		if err := storage.WriteJSONL(outFile, records); err != nil {
			return fmt.Errorf("failed to write scored records to %q: %w", outFile, err)
		}

		fmt.Printf("Scored %d PRs: %d HIGH (>= %.2f), %d MEDIUM (>= %.2f), %d LOW\nSaved to %s\n",
			len(records), highCount, cfg.Heuristics.Scoring.HighThreshold,
			medCount, cfg.Heuristics.Scoring.MediumThreshold, lowCount, outFile)

		return nil
	},
}

func init() {
	scoreCmd.Flags().StringVarP(&scoreInFlag, "in", "i", "./data/correlated.jsonl", "Input JSONL file")
	scoreCmd.Flags().StringVarP(&scoreOutFlag, "out", "o", "./data/candidates.jsonl", "Output JSONL file")
}
