package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/cohort"
	"miner/internal/model"
	"miner/internal/storage"
)

var cohortReportFlags struct {
	input, repo, out, format string
}

var cohortReportCmd = &cobra.Command{
	Use:   "cohort-report",
	Short: "Build an evaluator-facing report for choosing benchmark cases",
	Long: `Summarizes every scored candidate for cohort selection: heuristic score, retrospective
signal tier, change size, subsystem, and — with --repo — whether the case looks exportable.

Rows are grouped by subsystem rather than globally ranked, because a cohort taken from the
top of a ranking tends to be one bug class in one subsystem and cannot discriminate across
the axes a pilot cohort has to cover.

The exportability column is a prediction. Confirm a shortlist with cohort-verify, which
performs the real export and runs the engine's own boundary validator.

Output is evaluator-facing: it shows retrospective evidence and ranking, and must never be
given to a reviewer or wired into an engine-visible path.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cohortReportFlags
		records, err := storage.ReadJSONL(f.input)
		if err != nil {
			return fmt.Errorf("read candidates: %w", err)
		}
		if len(records) == 0 {
			return fmt.Errorf("no records in %q", f.input)
		}
		rows := cohort.Analyze(cmd.Context(), records, f.repo)

		switch strings.ToLower(f.format) {
		case "csv":
			out := os.Stdout
			if f.out != "" {
				file, err := os.Create(f.out)
				if err != nil {
					return err
				}
				defer file.Close()
				out = file
			}
			if err := cohort.RenderCSV(out, rows); err != nil {
				return err
			}
		default:
			md := cohort.RenderMarkdown(rows, f.repo != "")
			if f.out == "" {
				fmt.Fprint(cmd.OutOrStdout(), md)
			} else if err := os.WriteFile(f.out, []byte(md), 0644); err != nil {
				return err
			}
		}
		if f.out != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Wrote cohort report for %d candidates to %s\n", len(rows), f.out)
		}
		return nil
	},
}

var cohortVerifyFlags struct {
	inputs                                                       []string
	repo, cacheDir, out, cutoff, benchRepo, prs, metadataVersion string
}

var cohortVerifyCmd = &cobra.Command{
	Use: "cohort-verify",
	// A non-ready cohort is a reported result, not a usage mistake; without this
	// cobra prints the flag list over the report the command just wrote.
	SilenceUsage: true,
	Short:        "Prove a shortlisted cohort by exporting it and validating every bundle",
	Long: `Runs the real prospective export for each shortlisted PR, then — with --bench-repo —
submits each resulting bundle to engine-runner's own boundary validator.

This exists because cohort-report's exportability column is a prediction. Nothing should
enter a cohort on a prediction: a case that looks fine can still fail on something the
prediction cannot see, and finding that out after a paid run has started is the failure
this pass prevents.

Without --bench-repo no validation is performed and the exact command for each bundle is
printed instead. The engine is a separate Go module whose validator lives under internal/,
which Go forbids importing across module boundaries, so validation must shell out — this
repo does not assume a sibling checkout exists.

Each case is pinned to its own merged_at unless --cutoff overrides every case at once.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cohortVerifyFlags
		var records []model.PRCandidateRecord
		seen := map[int]string{}
		for _, in := range f.inputs {
			path := in
			if i := strings.Index(in, "="); i > 0 && !strings.Contains(in[:i], "/") {
				path = in[i+1:]
			}
			recs, err := storage.ReadJSONL(path)
			if err != nil {
				return fmt.Errorf("read candidates: %w", err)
			}
			for _, r := range recs {
				if prev, dup := seen[r.Original.Number]; dup {
					return fmt.Errorf("PR #%d appears in both %q and %q; refusing to choose", r.Original.Number, prev, path)
				}
				seen[r.Original.Number] = path
			}
			records = append(records, recs...)
		}
		if len(records) == 0 {
			return fmt.Errorf("--input is required (repeat it for a cohort spanning more than one window)")
		}
		var prs []int
		for _, part := range strings.Split(f.prs, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("invalid --prs entry %q: %w", part, err)
			}
			prs = append(prs, n)
		}
		if len(prs) == 0 {
			return fmt.Errorf("--prs is required (comma-separated PR numbers)")
		}
		opt := cohort.VerifyOptions{
			Records:         records,
			PRs:             prs,
			Repo:            f.repo,
			CacheDir:        f.cacheDir,
			OutDir:          f.out,
			BenchRepo:       f.benchRepo,
			MetadataVersion: f.metadataVersion,
		}
		if f.cutoff != "" {
			t, err := time.Parse(time.RFC3339, f.cutoff)
			if err != nil {
				return fmt.Errorf("invalid --cutoff (RFC3339 required): %w", err)
			}
			opt.Cutoff = t
		}
		results, err := cohort.Verify(cmd.Context(), opt)
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), cohort.RenderVerification(results, f.benchRepo != ""))

		blocked := 0
		for _, r := range results {
			if !r.OK() {
				blocked++
			}
		}
		if blocked > 0 {
			return fmt.Errorf("%d of %d cases are not cohort-ready", blocked, len(results))
		}
		return nil
	},
}

func init() {
	r := &cohortReportFlags
	cohortReportCmd.Flags().StringVarP(&r.input, "input", "i", "./data/candidates.jsonl", "Scored candidate JSONL input")
	cohortReportCmd.Flags().StringVar(&r.repo, "repo", "", "Local Git repository; enables the export precheck")
	cohortReportCmd.Flags().StringVarP(&r.out, "out", "o", "", "Output file (default: stdout)")
	cohortReportCmd.Flags().StringVar(&r.format, "format", "markdown", "Output format: markdown or csv")

	v := &cohortVerifyFlags
	cohortVerifyCmd.Flags().StringArrayVarP(&v.inputs, "input", "i", nil, "Scored candidate JSONL input as [label=]path; repeat once per window for a multi-source cohort")
	cohortVerifyCmd.Flags().StringVar(&v.repo, "repo", "", "Local Git repository the snapshot and diff are built from")
	cohortVerifyCmd.Flags().StringVar(&v.cacheDir, "cache-dir", "", "Collect-time cache root holding per-PR provider bundles")
	cohortVerifyCmd.Flags().StringVarP(&v.out, "out", "o", "", "New directory receiving one bundle per case")
	cohortVerifyCmd.Flags().StringVar(&v.cutoff, "cutoff", "", "Pin every case to one RFC3339 cutoff (default: each PR's merged_at)")
	cohortVerifyCmd.Flags().StringVar(&v.benchRepo, "bench-repo", "", "engine-runner checkout; when set, each bundle is validated by the engine")
	cohortVerifyCmd.Flags().StringVar(&v.prs, "prs", "", "Comma-separated shortlist of PR numbers")
	cohortVerifyCmd.Flags().StringVar(&v.metadataVersion, "metadata-version", "v2", "Reviewer-metadata contract: v2 (per-field edit history) or v1 (updated_at rule, pilot reproduction)")
	for _, name := range []string{"repo", "cache-dir", "out", "prs"} {
		_ = cohortVerifyCmd.MarkFlagRequired(name)
	}
}
