package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"pr-analysis/internal/storage"
)

var (
	inspectInFlag string
	inspectPRFlag int
	inspectJSON   bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect detailed pre-merge facts and retrospective evidence for a PR",
	Long:  `Displays a comprehensive breakdown of an individual PR candidate including pre-merge diff facts, heuristic rules triggered, and retrospective corrective signals.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if inspectPRFlag <= 0 {
			return fmt.Errorf("--pr <number> is required")
		}

		inFile := "./data/candidates.jsonl"
		if inspectInFlag != "" {
			inFile = inspectInFlag
		}

		records, err := storage.ReadJSONL(inFile)
		if err != nil {
			return fmt.Errorf("failed to read records from %q: %w", inFile, err)
		}

		for _, rec := range records {
			if rec.Original.Number == inspectPRFlag {
				if inspectJSON {
					data, _ := json.MarshalIndent(rec, "", "  ")
					fmt.Println(string(data))
					return nil
				}

				fmt.Println("================================================================================")
				fmt.Printf("PR #%d: %s\n", rec.Original.Number, rec.Original.Title)
				fmt.Printf("Repository: %s | Author: %s | Merged: %s (T0)\n",
					rec.Original.Repository, rec.Original.Author, rec.Original.MergedAt.Format("2006-01-02 15:04:05 UTC"))
				fmt.Printf("Base SHA: %s | Merge SHA: %s\n", rec.Original.BaseSHA, rec.Original.MergeCommitSHA)
				fmt.Println("--------------------------------------------------------------------------------")
				fmt.Printf("Stateful/Distributed Score: %.2f (%s)\n", rec.Stateful.Score, rec.Stateful.Category)
				fmt.Printf("Explanation: %s\n", rec.Stateful.Explanation)
				if len(rec.Stateful.MatchedSymbols) > 0 {
					fmt.Printf("Matched C Symbols: %v\n", rec.Stateful.MatchedSymbols)
				}
				fmt.Println("--------------------------------------------------------------------------------")
				fmt.Printf("Changed Files (%d): \n", len(rec.Original.ChangedFiles))
				for _, f := range rec.Original.ChangedFiles {
					fmt.Printf("  [%s] %s (+%d/-%d)\n", f.Status, f.Path, f.Additions, f.Deletions)
				}
				if len(rec.Original.ChangedFunctions) > 0 {
					fmt.Printf("Enclosing Changed Functions (%d): %v\n", len(rec.Original.ChangedFunctions), rec.Original.ChangedFunctions)
				}
				fmt.Println("--------------------------------------------------------------------------------")
				fmt.Printf("Retrospective Corrective Evidence (Summary Rank: %.2f):\n", rec.Retrospective.SummaryRankScore)
				if len(rec.Retrospective.StrongSignals) == 0 && len(rec.Retrospective.MediumSignals) == 0 && len(rec.Retrospective.WeakSignals) == 0 {
					fmt.Println("  (No retrospective signals detected)")
				}
				for _, s := range rec.Retrospective.StrongSignals {
					fmt.Printf("  [STRONG: %s] %s on %s\n    Snippet: %s\n",
						s.SignalType, s.SourceRef, s.Timestamp.Format("2006-01-02"), s.RawSnippet)
				}
				for _, m := range rec.Retrospective.MediumSignals {
					fmt.Printf("  [MEDIUM: %s] %s on %s (Function: %s)\n    Context: %s\n",
						m.SignalType, m.SourceRef, m.Timestamp.Format("2006-01-02"), m.FunctionOrPath, m.Context)
				}
				for _, w := range rec.Retrospective.WeakSignals {
					fmt.Printf("  [WEAK: %s] %s on %s (File: %s)\n",
						w.SignalType, w.SourceRef, w.Timestamp.Format("2006-01-02"), w.FilePath)
				}
				fmt.Println("================================================================================")
				return nil
			}
		}

		return fmt.Errorf("PR #%d not found in %q", inspectPRFlag, inFile)
	},
}

func init() {
	inspectCmd.Flags().StringVarP(&inspectInFlag, "in", "i", "./data/candidates.jsonl", "Input candidates JSONL file")
	inspectCmd.Flags().IntVarP(&inspectPRFlag, "pr", "p", 0, "PR number to inspect")
	inspectCmd.Flags().BoolVar(&inspectJSON, "json", false, "Output verbatim JSON instead of formatted text")
}
