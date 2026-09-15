package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/gitx"
	"miner/internal/historybaseline"
)

var historyBaselineFlags struct {
	input, gitDir, out, cutoff string
}

var historyBaselineCmd = &cobra.Command{
	Use:           "history-baseline",
	SilenceUsage:  true,
	SilenceErrors: true,
	Short:         "Arm H: flag each cohort case RISKY from pre-cutoff subsystem defect density and churn",
	Long: `The zero-LLM history baseline of the H1 experiment. For each case in a cohort.jsonl it
walks the base branch as it stood at merge (all ancestors of the merge commit's first
parent) over the 24 months before the case's cutoff, by committer date, computes per
subsystem the commits, churn lines, strong-signal corrective commits and their density,
scores each active subsystem (>= 20 commits) by the mean percentile rank of density and
churn, and flags the case RISKY when its primary subsystem (most changed lines in the PR)
is in the top tercile. Ties fall to the lower tercile. A primary subsystem with fewer
than 20 commits is CLEAN with the reason recorded; a history that cannot be resolved
yields risky: null with a reason.

Reads the mirror and the cohort file only: no provider, no correlated evidence, no
labels. Writes <out>/<case_id>.json (history-baseline/v1), deterministic up to
produced_at. Output is evaluator-only: it is an arm's verdict and must never reach a
reviewer arm or a prospective bundle.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := historyBaselineFlags
		cases, inputHash, err := historybaseline.ReadCases(f.input)
		if err != nil {
			return err
		}
		if len(cases) == 0 {
			return fmt.Errorf("no cases in %q", f.input)
		}
		var cutoff time.Time
		if f.cutoff != "" {
			t, err := time.Parse(time.RFC3339, f.cutoff)
			if err != nil {
				return fmt.Errorf("invalid --cutoff (RFC3339 required): %w", err)
			}
			cutoff = t
		}
		repo := gitx.OpenRepository(f.gitDir)
		var results []*historybaseline.Result
		risky, clean, null := 0, 0, 0
		for _, c := range cases {
			r, err := historybaseline.Build(context.Background(), repo, c, historybaseline.Options{Cutoff: cutoff, InputSHA256: inputHash})
			if err != nil {
				return fmt.Errorf("PR #%d: %w", c.PR, err)
			}
			switch {
			case r.Risky == nil:
				null++
			case *r.Risky:
				risky++
			default:
				clean++
			}
			results = append(results, r)
		}
		if err := historybaseline.WriteAll(f.out, results); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Wrote history baseline for %d cases to %s: %d RISKY, %d CLEAN, %d unresolved\n", len(results), f.out, risky, clean, null)
		return nil
	},
}

func init() {
	f := &historyBaselineFlags
	historyBaselineCmd.Flags().StringVarP(&f.input, "input", "i", "", "cohort.jsonl from cohort-export")
	historyBaselineCmd.Flags().StringVar(&f.gitDir, "git-dir", "", "Local Git mirror")
	historyBaselineCmd.Flags().StringVarP(&f.out, "out", "o", "", "New directory receiving one <case_id>.json per case")
	historyBaselineCmd.Flags().StringVar(&f.cutoff, "cutoff", "", "Pin every case to one RFC3339 cutoff (default: each PR's merged_at)")
	for _, name := range []string{"input", "git-dir", "out"} {
		_ = historyBaselineCmd.MarkFlagRequired(name)
	}
}
