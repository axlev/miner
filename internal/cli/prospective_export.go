package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/prospectiveexport"
)

var prospectiveExportFlags struct {
	input, cacheFile, repo, cutoff, caseID, prospectiveOut, evaluatorOut string
	pr                                                                   int
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
		opt := prospectiveexport.Options{
			CorrelatedInput: f.input,
			CacheFile:       f.cacheFile,
			Repo:            f.repo,
			PR:              f.pr,
			Cutoff:          cutoff,
			CaseID:          f.caseID,
			ProspectiveOut:  f.prospectiveOut,
			EvaluatorOut:    f.evaluatorOut,
		}
		warnings, err := prospectiveexport.Export(cmd.Context(), opt)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
		}
		fmt.Printf("Exported prospective bundle to %s (evaluator-only artifacts at %s)\n", f.prospectiveOut, f.evaluatorOut)
		return nil
	},
}

func init() {
	f := &prospectiveExportFlags
	prospectiveExportCmd.Flags().StringVarP(&f.input, "input", "i", "", "Evaluator correlated JSONL input")
	prospectiveExportCmd.Flags().StringVar(&f.cacheFile, "cache-file", "", "Cached provider PR bundle")
	prospectiveExportCmd.Flags().StringVar(&f.repo, "repo", "", "Local Git repository used to resolve the canonical change patch")
	prospectiveExportCmd.Flags().IntVarP(&f.pr, "pr", "p", 0, "PR number to select")
	prospectiveExportCmd.Flags().StringVar(&f.cutoff, "cutoff", "", "Benchmark cutoff timestamp (RFC3339)")
	prospectiveExportCmd.Flags().StringVar(&f.caseID, "case-id", "", "Case identifier override; a neutral opaque ID is generated when omitted")
	prospectiveExportCmd.Flags().StringVar(&f.prospectiveOut, "prospective-out", "", "New output directory for the engine-ingestible prospective bundle (reviewer/ + control/)")
	prospectiveExportCmd.Flags().StringVar(&f.evaluatorOut, "evaluator-out", "", "New output directory for evaluator-only artifacts")
	for _, name := range []string{"input", "cache-file", "repo", "pr", "cutoff", "prospective-out", "evaluator-out"} {
		_ = prospectiveExportCmd.MarkFlagRequired(name)
	}
}
