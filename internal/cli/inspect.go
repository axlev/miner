package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"miner/internal/storage"
)

var (
	inspectInFlag   string
	inspectPRFlag   int
	inspectJSONFlag bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Quickly inspect a specific candidate PR's facts and retrospective signals",
	RunE: func(cmd *cobra.Command, args []string) error {
		if inspectPRFlag == 0 {
			return fmt.Errorf("must specify --pr <number>")
		}

		inFile := "./data/candidates.jsonl"
		if inspectInFlag != "" {
			inFile = inspectInFlag
		}

		record, err := storage.FindPRInJSONL(inFile, inspectPRFlag)
		if err != nil {
			return err
		}

		if inspectJSONFlag {
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}

		// Print human-readable formatted summary
		r := record
		fmt.Println(strings.Repeat("=", 80))
		fmt.Printf("PR #%d: %s\n", r.Original.Number, r.Original.Title)
		fmt.Printf("Repository: %s | Author: %s | Merged: %s\n",
			r.Original.Repository, r.Original.Author, r.Original.MergedAt.Format("2006-01-02 15:04:05 UTC"))
		fmt.Printf("Base SHA: %s | Merge SHA: %s | Head SHA: %s\n",
			r.Original.BaseSHA, r.Original.MergeCommitSHA, r.Original.HeadSHA)
		fmt.Println(strings.Repeat("-", 80))
		fmt.Printf("Stateful/Distributed Score: %.2f (%s)\n", r.Stateful.Score, r.Stateful.Category)
		fmt.Printf("Explanation: %s\n", r.Stateful.Explanation)
		if len(r.Stateful.MatchedSymbols) > 0 {
			fmt.Printf("Matched C Symbols: %v\n", r.Stateful.MatchedSymbols)
		}
		fmt.Println(strings.Repeat("-", 80))
		fmt.Printf("Changed Files (%d):\n", len(r.Original.ChangedFiles))
		for i, cf := range r.Original.ChangedFiles {
			if i >= 10 {
				fmt.Printf("  ... and %d more files\n", len(r.Original.ChangedFiles)-10)
				break
			}
			fmt.Printf("  [%s] %s (+%d/-%d)\n", cf.Status, cf.Path, cf.Additions, cf.Deletions)
		}
		if len(r.Original.ChangedFunctions) > 0 {
			funcPreview := r.Original.ChangedFunctions
			if len(funcPreview) > 8 {
				funcPreview = funcPreview[:8]
			}
			fmt.Printf("Enclosing Changed Functions (%d): %v\n",
				len(r.Original.ChangedFunctions), funcPreview)
		}
		fmt.Println(strings.Repeat("-", 80))
		fmt.Printf("Retrospective Corrective Evidence (Summary Rank: %.2f):\n", r.Retrospective.SummaryRankScore)
		if len(r.Retrospective.StrongSignals) == 0 && len(r.Retrospective.MediumSignals) == 0 && len(r.Retrospective.WeakSignals) == 0 {
			fmt.Println("  No post-merge bugfix signals detected in observation window.")
		}
		for _, s := range r.Retrospective.StrongSignals {
			fmt.Printf("  🔴 [STRONG: %s] %s on %s\n    Context: %s\n",
				s.SignalType, s.SourceRef, s.Timestamp.Format("2006-01-02"), s.RawSnippet)
		}
		for _, m := range r.Retrospective.MediumSignals {
			fmt.Printf("  🟡 [MEDIUM: %s] %s on %s (Function: %s)\n    Context: %s\n",
				m.SignalType, m.SourceRef, m.Timestamp.Format("2006-01-02"), m.FunctionOrPath, m.Context)
		}
		for _, w := range r.Retrospective.WeakSignals {
			fmt.Printf("  ⚪ [WEAK: %s] %s on %s (File: %s)\n    Context: %s\n",
				w.SignalType, w.SourceRef, w.Timestamp.Format("2006-01-02"), w.FilePath, w.Context)
		}
		fmt.Println(strings.Repeat("=", 80))

		return nil
	},
}

func init() {
	inspectCmd.Flags().StringVarP(&inspectInFlag, "in", "i", "./data/candidates.jsonl", "Input candidates JSONL file")
	inspectCmd.Flags().IntVarP(&inspectPRFlag, "pr", "p", 0, "PR number to inspect")
	inspectCmd.Flags().BoolVar(&inspectJSONFlag, "json", false, "Output verbatim JSON instead of formatted text")
}
