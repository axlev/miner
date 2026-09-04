package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"pr-analysis/internal/prospectiveexport"
)

var prospectiveExportFlags struct {
	input, cacheFile, cutoff, caseID, out string
	pr                                    int
}

var prospectiveExportCmd = &cobra.Command{
	Use:   "prospective-export",
	Short: "Export contamination-safe prospective reviewer metadata",
	RunE: func(cmd *cobra.Command, args []string) error {
		f := prospectiveExportFlags
		cutoff, err := time.Parse(time.RFC3339, f.cutoff)
		if err != nil {
			return fmt.Errorf("invalid --cutoff (RFC3339 required): %w", err)
		}
		if err := prospectiveexport.Export(prospectiveexport.Options{CorrelatedInput: f.input, CacheFile: f.cacheFile, PR: f.pr, Cutoff: cutoff, CaseID: f.caseID, Out: f.out}); err != nil {
			return err
		}
		fmt.Printf("Exported prospective case %s to %s\n", f.caseID, f.out)
		return nil
	},
}

func init() {
	f := &prospectiveExportFlags
	prospectiveExportCmd.Flags().StringVarP(&f.input, "input", "i", "", "Evaluator correlated JSONL input")
	prospectiveExportCmd.Flags().StringVar(&f.cacheFile, "cache-file", "", "Cached provider PR bundle")
	prospectiveExportCmd.Flags().IntVarP(&f.pr, "pr", "p", 0, "PR number to select")
	prospectiveExportCmd.Flags().StringVar(&f.cutoff, "cutoff", "", "Benchmark cutoff timestamp (RFC3339)")
	prospectiveExportCmd.Flags().StringVar(&f.caseID, "case-id", "", "Neutral case identifier")
	prospectiveExportCmd.Flags().StringVarP(&f.out, "out", "o", "", "New case output directory")
	for _, name := range []string{"input", "cache-file", "pr", "cutoff", "case-id", "out"} {
		_ = prospectiveExportCmd.MarkFlagRequired(name)
	}
}
