package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/cohort"
	"miner/internal/storage"
)

var cohortExportFlags struct {
	inputs                                                      []string
	out, name, positiveSignals, positives, minFixDate, cacheDir string
	matchKey                                                    string
	fallbackSubsystem                                           bool
	minScore                                                    float64
	minExposureDays, maxPerSubsystem                            int
}

// parseSource accepts "label=path" or a bare path, whose label is then the file's
// parent directory name, or its stem when that is not informative. A label names
// the source in the manifest and on every case drawn from it.
func parseSource(arg string) (cohort.Source, error) {
	label, path := "", arg
	if i := strings.Index(arg, "="); i > 0 && !strings.Contains(arg[:i], "/") {
		label, path = arg[:i], arg[i+1:]
	}
	if label == "" {
		label = filepath.Base(filepath.Dir(path))
		if label == "." || label == "/" || label == "data" || label == "" {
			label = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cohort.Source{}, fmt.Errorf("read source %q: %w", path, err)
	}
	recs, err := storage.ReadJSONL(path)
	if err != nil {
		return cohort.Source{}, fmt.Errorf("read source %q: %w", path, err)
	}
	if len(recs) == 0 {
		return cohort.Source{}, fmt.Errorf("source %q has no records", path)
	}
	return cohort.Source{Label: label, Path: path, Records: recs, Bytes: raw}, nil
}

var cohortExportCmd = &cobra.Command{
	Use:           "cohort-export",
	SilenceUsage:  true,
	SilenceErrors: true, // Execute already prints the error once
	Short:         "Sample a 1:1 matched positive/negative cohort with an exposure guard",
	Long: `Builds a labelled cohort for the H1 experiment (docs/h1-pre-registration.md) from a
scored candidates file and writes it, with a manifest, into a new directory.

Positives are records with corrective evidence at the tier --positive-signals names.
Negatives are records with zero corrective signals at every tier — strong, medium and
weak — and each is matched 1:1 to one positive on stateful category, subsystem and a
coarse changed-lines band, so the two classes cannot be told apart by score, location
in the tree, or change size. A positive with no same-cell negative is dropped and
listed in the manifest; if --positives names it explicitly, the export fails instead.

Negatives are additionally refused unless their record was correlated by a build that
counts uninspected commit diffs (provenance.correlated_by set) and that count is zero:
"no signals" from a run whose diffs failed is not "no evidence". Positives carry the
count for information. --min-fix-date, when given, admits a positive only if its earliest
corrective signal is on or after that date, and each case records earliest_fix_date.

Both classes must have been merged at least --min-exposure-days before the records'
observation_end. "No evidence found" in a short window is a weak CLEAN label, and
applying the floor to one class only would let merge date predict the label. Exposure
is written per record.

Output is evaluator-facing: cohort.jsonl embeds the full pipeline record, retrospective
evidence included, and must never be given to a reviewer or wired into an engine-visible
path. The manifest is the cohort's identity: it carries the record-content hash, the
shared provenance, and every filter argument, because the same input under different
arguments is a different cohort.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := cohortExportFlags
		var sources []cohort.Source
		for _, in := range f.inputs {
			src, err := parseSource(in)
			if err != nil {
				return err
			}
			sources = append(sources, src)
		}
		if len(sources) == 0 {
			return fmt.Errorf("--input is required (repeat it for a multi-source cohort)")
		}
		var positives []int
		for _, part := range strings.Split(f.positives, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("invalid --positives entry %q: %w", part, err)
			}
			positives = append(positives, n)
		}
		var minFixDate time.Time
		if f.minFixDate != "" {
			t, err := time.Parse("2006-01-02", f.minFixDate)
			if err != nil {
				return fmt.Errorf("invalid --min-fix-date (YYYY-MM-DD required): %w", err)
			}
			minFixDate = t.UTC()
		}
		var matchKey []string
		for _, k := range strings.Split(f.matchKey, ",") {
			if k = strings.TrimSpace(k); k != "" {
				matchKey = append(matchKey, k)
			}
		}
		opt := cohort.ExportOptions{
			Sources:           sources,
			Out:               f.out,
			Name:              f.name,
			MinScore:          f.minScore,
			PositiveSignals:   strings.ToLower(f.positiveSignals),
			MinExposureDays:   f.minExposureDays,
			Positives:         positives,
			MinFixDate:        minFixDate,
			CacheDir:          f.cacheDir,
			MatchKey:          matchKey,
			MaxPerSubsystem:   f.maxPerSubsystem,
			FallbackSubsystem: f.fallbackSubsystem,
		}
		m, err := cohort.Export(opt)
		if err != nil {
			return err
		}
		total := 0
		for _, src := range sources {
			total += len(src.Records)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Wrote cohort %q: %d cases (%d pairs) from %d candidates across %d source(s) to %s\n",
			m.Name, m.RecordCount, len(m.Pairs), total, len(sources), f.out)
		fmt.Fprintf(cmd.OutOrStdout(), "records_sha256 %s\n", m.RecordsSHA256)
		if m.FallbackPairs > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "%d of %d pairs matched on subsystem alone; they are not category-balanced and must be reported as a stratum\n", m.FallbackPairs, len(m.Pairs))
		}
		if len(m.UnmatchedPositives) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped %d positives with no same-cell negative: %v\n", len(m.UnmatchedPositives), m.UnmatchedPositives)
		}
		return nil
	},
}

func init() {
	f := &cohortExportFlags
	cohortExportCmd.Flags().StringArrayVarP(&f.inputs, "input", "i", nil, "Scored candidate JSONL input as [label=]path; repeat for a multi-source cohort")
	cohortExportCmd.Flags().StringVar(&f.matchKey, "match-key", "", "Cell fields a negative must share with its positive: any subset of category,subsystem,size_band (default all three)")
	cohortExportCmd.Flags().BoolVar(&f.fallbackSubsystem, "fallback-subsystem", false, "Pair a positive that has no negative in its full-key cell on subsystem alone, nearest category then nearest size band; such pairs record match_key_used [subsystem] and are counted as fallback_pairs")
	cohortExportCmd.Flags().IntVar(&f.maxPerSubsystem, "max-per-subsystem", 0, "Cap positives per subsystem (0 = no cap); an explicit --positives list exceeding it fails the export")
	cohortExportCmd.Flags().StringVarP(&f.out, "out", "o", "", "New directory receiving cohort.jsonl and cohort-manifest.json")
	cohortExportCmd.Flags().StringVar(&f.name, "name", "", "Cohort name recorded in the manifest (e.g. frr-2026-cohort-v1)")
	cohortExportCmd.Flags().Float64Var(&f.minScore, "min-score", 0.0, "Minimum stateful score, applied to both classes")
	cohortExportCmd.Flags().StringVar(&f.positiveSignals, "positive-signals", "strong", "Evidence tier that qualifies a positive: strong, or any (strong OR medium)")
	cohortExportCmd.Flags().IntVar(&f.minExposureDays, "min-exposure-days", cohort.DefaultMinExposureDays, "Minimum days between merged_at and observation_end, both classes")
	cohortExportCmd.Flags().StringVar(&f.cacheDir, "cache-dir", "", "Collect-time cache root; when set, each case records the reviewer-metadata/v2 admission outcome per field")
	cohortExportCmd.Flags().StringVar(&f.minFixDate, "min-fix-date", "", "Admit a positive only if its earliest corrective signal is on or after this date (YYYY-MM-DD)")
	cohortExportCmd.Flags().StringVar(&f.positives, "positives", "", "Comma-separated explicit positive shortlist; each must qualify and match, or the export fails")
	for _, name := range []string{"input", "out", "name"} {
		_ = cohortExportCmd.MarkFlagRequired(name)
	}
}
