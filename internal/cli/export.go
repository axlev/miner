package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"pr-analysis/internal/model"
	"pr-analysis/internal/storage"
)

var (
	exportInFlag         string
	exportOutFlag        string
	exportFormatFlag     string
	minStatefulScoreFlag float64
	requireFixSignalFlag bool
	requireStrongFixFlag bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Filter and export ranked benchmark candidate dataset",
	Long:  `Filters candidate records by heuristic score thresholds and retrospective fix signals, outputting a machine-readable JSONL dataset for downstream LLM evaluation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		inFile := "./data/candidates.jsonl"
		if exportInFlag != "" {
			inFile = exportInFlag
		}

		outFile := "./output/benchmark_candidates.jsonl"
		if exportOutFlag != "" {
			outFile = exportOutFlag
		}

		records, err := storage.ReadJSONL(inFile)
		if err != nil {
			return fmt.Errorf("failed to read records from %q: %w", inFile, err)
		}

		var filtered []model.PRCandidateRecord
		for _, rec := range records {
			if rec.Stateful.Score < minStatefulScoreFlag {
				continue
			}
			if requireStrongFixFlag && !rec.HasStrongFixSignal() {
				continue
			}
			if requireFixSignalFlag && !rec.HasAnyFixSignal() {
				continue
			}
			filtered = append(filtered, rec)
		}

		// Sort by Retrospective Rank Score descending, then Stateful Score descending
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].Retrospective.SummaryRankScore != filtered[j].Retrospective.SummaryRankScore {
				return filtered[i].Retrospective.SummaryRankScore > filtered[j].Retrospective.SummaryRankScore
			}
			if filtered[i].Stateful.Score != filtered[j].Stateful.Score {
				return filtered[i].Stateful.Score > filtered[j].Stateful.Score
			}
			return filtered[i].Original.Number < filtered[j].Original.Number
		})

		format := strings.ToLower(exportFormatFlag)
		switch format {
		case "markdown", "md":
			if err := storage.WriteMarkdownTable(outFile, filtered); err != nil {
				return fmt.Errorf("failed to export markdown report to %q: %w", outFile, err)
			}
		case "csv":
			if err := storage.WriteCSV(outFile, filtered); err != nil {
				return fmt.Errorf("failed to export CSV report to %q: %w", outFile, err)
			}
		default: // "jsonl"
			if err := storage.WriteJSONL(outFile, filtered); err != nil {
				return fmt.Errorf("failed to export JSONL candidates to %q: %w", outFile, err)
			}
		}

		fmt.Printf("Exported %d candidate PRs (from %d total) in %s format to %s\n",
			len(filtered), len(records), strings.ToUpper(format), outFile)

		return nil
	},
}

func init() {
	exportCmd.Flags().StringVarP(&exportInFlag, "in", "i", "./data/candidates.jsonl", "Input candidates JSONL file")
	exportCmd.Flags().StringVarP(&exportOutFlag, "out", "o", "./output/benchmark_candidates.jsonl", "Output filepath")
	exportCmd.Flags().StringVarP(&exportFormatFlag, "format", "f", "jsonl", "Output format: jsonl, markdown (or md), csv")
	exportCmd.Flags().Float64Var(&minStatefulScoreFlag, "min-score", 0.0, "Minimum stateful heuristic score threshold")
	exportCmd.Flags().BoolVar(&requireFixSignalFlag, "require-fix-signal", false, "Require at least one retrospective fix signal (strong or medium)")
	exportCmd.Flags().BoolVar(&requireStrongFixFlag, "require-strong-fix", false, "Require an unambiguous strong fix signal (Fixes:, revert, regression citation)")
}
