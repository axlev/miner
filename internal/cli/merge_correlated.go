package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"miner/internal/batchstore"
	"miner/internal/correlatemerge"
	"miner/internal/storage"
)

var mergeCorrelatedFlags struct {
	base, subset, out, report string
}

var mergeCorrelatedCmd = &cobra.Command{
	Use:           "merge-correlated",
	SilenceUsage:  true,
	SilenceErrors: true,
	Short:         "Splice a re-correlated subset back into the full correlated set, with a flip report",
	Long: `Replaces records in --base by the same-numbered records in --subset and writes the
result to --out, plus a flip report to --report saying which records changed evidence
class and to what.

This is how a partial re-correlation (the zero-signal pool, and positives whose fix diff
failed) gets back into one file for score and cohort-export. It never adds or removes
records. It fails closed if a subset record is not in the base, describes a different
original change, disagrees with the base record's provenance identity, or was not
produced by a build that counts uninspected commit diffs.

The flip report's from_none / subset_none_before ratio is the measured rate at which
"no signals" was really "diffs failed", which the H1 pre-registration records.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := mergeCorrelatedFlags
		base, err := storage.ReadJSONL(f.base)
		if err != nil {
			return fmt.Errorf("read base: %w", err)
		}
		subset, err := storage.ReadJSONL(f.subset)
		if err != nil {
			return fmt.Errorf("read subset: %w", err)
		}
		if len(base) == 0 || len(subset) == 0 {
			return fmt.Errorf("base and subset must both be non-empty")
		}
		merged, report, err := correlatemerge.Merge(base, subset)
		if err != nil {
			return err
		}
		if err := batchstore.WriteJSONLNewAtomic(f.out, merged); err != nil {
			return err
		}
		if err := batchstore.WriteJSONNewAtomic(f.report, report); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Merged %d of %d records (correlated by %s): %d unchanged, %d flipped, %d of %d zero-signal records gained evidence; %d uninspected commit diffs remain\n",
			report.SubsetCount, report.BaseCount, report.CorrelatedBy, report.Unchanged, report.Flipped, report.FromNone, report.SubsetNoneBefore, report.UninspectedAfter)
		fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s and %s\n", f.out, f.report)
		return nil
	},
}

func init() {
	f := &mergeCorrelatedFlags
	mergeCorrelatedCmd.Flags().StringVar(&f.base, "base", "", "Full correlated JSONL")
	mergeCorrelatedCmd.Flags().StringVar(&f.subset, "subset", "", "Re-correlated subset JSONL")
	mergeCorrelatedCmd.Flags().StringVarP(&f.out, "out", "o", "", "New merged JSONL (must not exist)")
	mergeCorrelatedCmd.Flags().StringVar(&f.report, "report", "", "New flip report JSON (must not exist)")
	for _, name := range []string{"base", "subset", "out", "report"} {
		_ = mergeCorrelatedCmd.MarkFlagRequired(name)
	}
}
