package cohort

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"miner/internal/buildinfo"
	"miner/internal/collector"
	"miner/internal/model"
	"miner/internal/prospectiveexport"
	"miner/internal/storage"
)

// Schema version strings for the cohort artifacts. Both are new artifacts, not
// revisions of the pipeline record (model.SchemaVersion) or of any bundle entry.
//
// v2 (2026-09-15): cases carry uninspected_commit_count, correlation_counted and
// earliest_fix_date; the manifest records min_fix_date and the exclusion reasons
// uncounted_negative, uninspected_negative, no_fix_date and fix_before_min_date. A
// negative is now refused unless its record was correlated by a counting build and
// its count is zero.
//
// v3 (2026-09-15): when cohort-export is given the collect cache, each case carries
// the reviewer-metadata/v2 admission outcome and method for title and description
// (the same decision prospective-export makes), and the manifest counts them, so
// arms can be stratified on what they were actually shown. Absent when the cache
// was not given: never guessed.
const (
	CaseSchemaVersion     = "cohort-case/v3"
	ManifestSchemaVersion = "cohort-manifest/v3"

	// DefaultMinExposureDays is the pre-registered exposure floor
	// (docs/h1-pre-registration.md, "Exposure guard"). It is a policy value, not a
	// window the correlator applies: strong and medium signals are searched over the
	// whole [merged_at, observation_end] interval (internal/correlator/correlator.go,
	// CorrelatePR); only weak SAME_FILE_MODIFICATION is bounded, at 90 days.
	DefaultMinExposureDays = 180

	labelRisky = "RISKY"
	labelClean = "CLEAN"
)

// ExportOptions are the arguments of one cohort export. Every field that changes
// which records are selected is copied into the manifest, because two exports over
// the same candidates file with different arguments are different cohorts with
// identical per-record provenance.
type ExportOptions struct {
	Input           string
	Out             string
	Name            string
	MinScore        float64
	PositiveSignals string // "strong" or "any"
	MinExposureDays int
	Positives       []int // explicit positive shortlist; nil means every qualifying record
	// MinFixDate, when non-zero, admits a positive only if its earliest corrective
	// signal is at or after this instant (pre-registration §3: the fix must postdate
	// the treatment model's training cutoff). Recorded in the manifest.
	MinFixDate time.Time
	// CacheDir, when set, is the collect-time cache root; each case then records the
	// v2 admission outcome per field, computed at the case's merged_at.
	CacheDir string
}

// Cell is the matching key. A negative is only ever paired with a positive in the
// same cell, so the two classes cannot be told apart by heuristic band, location in
// the tree, or change size.
type Cell struct {
	Category  string `json:"category"`
	Subsystem string `json:"subsystem"`
	SizeBand  string `json:"size_band"`
}

// Case is one line of cohort.jsonl: the unmodified pipeline record wrapped with the
// sampler's label and the fields the evaluator needs to stratify or drop it.
//
// sampler_label is what the sampling rule says, not an adjudicated verdict. The
// adjudicated answer lives in the evaluator-only reason label
// (schemas/reason-label.schema.json) and may disagree.
type Case struct {
	SchemaVersion string `json:"schema_version"`
	SamplerLabel  string `json:"sampler_label"`
	PairID        string `json:"pair_id"`
	Repository    string `json:"repository"`
	PR            int    `json:"pr"`
	MatchedPR     int    `json:"matched_pr"`
	ExposureDays  int    `json:"exposure_days"`
	ChangedLines  int    `json:"changed_lines"`
	Cell          Cell   `json:"cell"`
	// CorrelationCounted is true when the record was correlated by a build that
	// counts uninspected commits (provenance.correlated_by set). When false the
	// count below is unknown, not zero, and the record cannot be a negative.
	CorrelationCounted     bool `json:"correlation_counted"`
	UninspectedCommitCount int  `json:"uninspected_commit_count"`
	// EarliestFixDate is the earliest corrective-signal timestamp (strong, or medium
	// when the positive tier is "any" and no strong signal exists). Absent for CLEAN.
	EarliestFixDate *time.Time `json:"earliest_fix_date,omitempty"`
	// Admission is the reviewer-metadata/v2 outcome and method per field ("title",
	// "description") at cutoff = merged_at, present only when the cache was given.
	Admission    map[string]prospectiveexport.FieldAdmission `json:"admission,omitempty"`
	RecordSHA256 string                                      `json:"record_sha256"`
	Record       model.PRCandidateRecord                     `json:"record"`
}

// Pair records one positive/negative match in the manifest.
type Pair struct {
	ID       string `json:"id"`
	Positive int    `json:"positive"`
	Negative int    `json:"negative"`
	Cell     Cell   `json:"cell"`
}

// Exclusions counts every input record that did not enter the cohort, by the first
// rule that removed it. The counts are part of the manifest so a cohort's size can
// be read against what it was drawn from.
type Exclusions struct {
	MissingDates         int `json:"missing_dates"`
	ExposureBelowMinimum int `json:"exposure_below_minimum"`
	BelowMinScore        int `json:"below_min_score"`
	PartialEvidence      int `json:"partial_evidence"`
	NotInPositiveList    int `json:"not_in_positive_list"`
	UncountedNegative    int `json:"uncounted_negative"`   // zero signals, but no correlated_by: unverifiable
	UninspectedNegative  int `json:"uninspected_negative"` // zero signals, but some diffs were not inspected
	NoFixDate            int `json:"no_fix_date"`
	FixBeforeMinDate     int `json:"fix_before_min_date"`
	UnmatchedPositive    int `json:"unmatched_positive"`
	UnusedNegative       int `json:"unused_negative"`
}

// Manifest identifies one cohort as a single object.
type Manifest struct {
	SchemaVersion   string    `json:"schema_version"`
	Name            string    `json:"name"`
	RecordCount     int       `json:"record_count"`
	PositiveCount   int       `json:"positive_count"`
	NegativeCount   int       `json:"negative_count"`
	RecordsSHA256   string    `json:"records_sha256"`
	ConfigHash      string    `json:"config_hash"`
	MinerVersion    string    `json:"miner_version"`
	TargetRepoHead  string    `json:"target_repo_head_sha"`
	ObservationEnd  time.Time `json:"observation_end"`
	ExporterVersion string    `json:"exporter_version"`
	Input           struct {
		Path        string `json:"path"`
		SHA256      string `json:"sha256"`
		RecordCount int    `json:"record_count"`
	} `json:"input"`
	Filters struct {
		MinScore        float64 `json:"min_score"`
		PositiveSignals string  `json:"positive_signals"`
		MinExposureDays int     `json:"min_exposure_days"`
		Positives       []int   `json:"positives"`
		MinFixDate      string  `json:"min_fix_date"` // RFC3339, or "" when not applied
	} `json:"filters"`
	Matching struct {
		ScoreBand  string `json:"score_band"`
		Subsystem  string `json:"subsystem"`
		SizeBand   string `json:"size_band"`
		Assignment string `json:"assignment"`
		Negative   string `json:"negative_rule"`
	} `json:"matching"`
	Pairs              []Pair     `json:"pairs"`
	UnmatchedPositives []int      `json:"unmatched_positives"`
	Exclusions         Exclusions `json:"exclusions"`
	// Admission summarises the per-case v2 outcomes: field -> outcome -> count. Absent
	// when the cache was not given.
	Admission map[string]map[string]int `json:"admission,omitempty"`
}

// sizeBands are the coarse changed-lines bands, closed at the top of each range.
// Boundaries are roughly logarithmic so that a 12-line and a 40-line change are
// peers while a 12-line and a 400-line change are not.
var sizeBands = []struct {
	upper int
	name  string
}{
	{0, "0"}, {10, "1-10"}, {50, "11-50"}, {200, "51-200"}, {1000, "201-1000"},
}

func sizeBandOf(lines int) string {
	for _, b := range sizeBands {
		if lines <= b.upper {
			return b.name
		}
	}
	return "1001+"
}

func changedLines(rec *model.PRCandidateRecord) int {
	n := 0
	for _, f := range rec.Original.ChangedFiles {
		n += f.Additions + f.Deletions
	}
	return n
}

// exposureDays is how long the correlator could have looked for corrective evidence
// after the merge. It fails closed: a record without both timestamps, or whose
// observation end precedes its merge, has no exposure and is not a usable case of
// either class.
func exposureDays(rec *model.PRCandidateRecord) (int, bool) {
	merged, end := rec.Original.MergedAt, rec.Provenance.ObservationEnd
	if merged.IsZero() || end.IsZero() || end.Before(merged) {
		return 0, false
	}
	return int(end.Sub(merged).Hours() / 24), true
}

// isZeroSignal is the first half of the CLEAN rule: nothing at any tier. Absence of
// strong and medium alone is not enough (docs/h1-pre-registration.md, "Negatives").
// A non-zero rank score with no signals would mean the record's own fields disagree,
// and such a record is not trusted as a negative either.
func isZeroSignal(rec *model.PRCandidateRecord) bool {
	r := &rec.Retrospective
	return len(r.StrongSignals) == 0 && len(r.MediumSignals) == 0 && len(r.WeakSignals) == 0 &&
		len(r.CommitRelationships) == 0 && r.SummaryRankScore == 0
}

// correlationCounted reports whether the record's uninspected-commit count exists at
// all. Records correlated before builds recorded provenance.correlated_by have no
// count, and "no signals" from such a run cannot be told apart from "diffs failed".
func correlationCounted(rec *model.PRCandidateRecord) bool {
	return rec.Provenance.CorrelatedBy != ""
}

// earliestFixDate is the earliest corrective-signal timestamp: over strong signals,
// or over medium signals when the tier is "any" and there is no strong signal.
func earliestFixDate(rec *model.PRCandidateRecord, tier string) (time.Time, bool) {
	var best time.Time
	for _, s := range rec.Retrospective.StrongSignals {
		if !s.Timestamp.IsZero() && (best.IsZero() || s.Timestamp.Before(best)) {
			best = s.Timestamp
		}
	}
	if best.IsZero() && tier == "any" {
		for _, m := range rec.Retrospective.MediumSignals {
			if !m.Timestamp.IsZero() && (best.IsZero() || m.Timestamp.Before(best)) {
				best = m.Timestamp
			}
		}
	}
	return best, !best.IsZero()
}

func isPositive(rec *model.PRCandidateRecord, tier string) bool {
	switch tier {
	case "strong":
		return rec.HasStrongFixSignal()
	default: // "any": strong OR medium, the same rule as export --require-fix-signal
		return rec.HasAnyFixSignal()
	}
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Export builds the cohort and writes it. The output directory must not exist; it
// is populated under a temporary name and renamed into place, so a failure leaves
// nothing at Out.
func Export(records []model.PRCandidateRecord, inputBytes []byte, opt ExportOptions) (*Manifest, error) {
	if opt.Out == "" {
		return nil, fmt.Errorf("output directory is required")
	}
	if opt.PositiveSignals != "strong" && opt.PositiveSignals != "any" {
		return nil, fmt.Errorf("--positive-signals must be \"strong\" or \"any\", got %q", opt.PositiveSignals)
	}
	if opt.MinExposureDays < 0 {
		return nil, fmt.Errorf("--min-exposure-days must not be negative")
	}
	if _, err := os.Lstat(opt.Out); err == nil {
		return nil, fmt.Errorf("output directory %q already exists; cohorts are immutable, choose a new path", opt.Out)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	cases, manifest, err := build(records, opt)
	if err != nil {
		return nil, err
	}
	manifest.Input.Path = opt.Input
	manifest.Input.SHA256 = digest(inputBytes)
	manifest.Input.RecordCount = len(records)

	if opt.CacheDir != "" {
		if err := annotateAdmission(cases, manifest, opt.CacheDir); err != nil {
			return nil, err
		}
	}

	if err := write(opt.Out, cases, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// annotateAdmission reads each case's cache bundle and records the v2 admission
// outcome per field at cutoff = merged_at. A missing or unreadable bundle fails the
// export: a cohort that claims to record admission must record it for every case.
func annotateAdmission(cases []Case, m *Manifest, cacheDir string) error {
	cache := storage.NewDiskCache(cacheDir)
	m.Admission = map[string]map[string]int{}
	for i := range cases {
		c := &cases[i]
		var bundle collector.RawPRBundle
		if err := cache.ReadPR(c.Repository, c.PR, &bundle); err != nil {
			return fmt.Errorf("PR #%d: cache bundle needed for admission outcomes: %w", c.PR, err)
		}
		if bundle.PR == nil || bundle.PR.GetNumber() != c.PR {
			return fmt.Errorf("PR #%d: cache bundle does not describe this PR", c.PR)
		}
		c.Admission = prospectiveexport.Admission(bundle, c.Record.Original.MergedAt.UTC())
		for field, a := range c.Admission {
			if m.Admission[field] == nil {
				m.Admission[field] = map[string]int{}
			}
			m.Admission[field][a.Outcome]++
		}
	}
	return nil
}

type classified struct {
	rec      *model.PRCandidateRecord
	exposure int
	lines    int
	cell     Cell
	fixDate  *time.Time
}

func build(records []model.PRCandidateRecord, opt ExportOptions) ([]Case, *Manifest, error) {
	m := &Manifest{SchemaVersion: ManifestSchemaVersion, Name: opt.Name, ExporterVersion: buildinfo.MinerVersion()}
	m.Filters.MinScore = opt.MinScore
	m.Filters.PositiveSignals = opt.PositiveSignals
	m.Filters.MinExposureDays = opt.MinExposureDays
	if !opt.MinFixDate.IsZero() {
		m.Filters.MinFixDate = opt.MinFixDate.UTC().Format(time.RFC3339)
	}
	m.Filters.Positives = append([]int{}, opt.Positives...)
	sort.Ints(m.Filters.Positives)
	m.Matching.ScoreBand = "stateful.category as scored (config high_threshold/medium_threshold over the max_cap-capped score)"
	m.Matching.Subsystem = "most common leading path component of changed_files, stepping through generic containers"
	m.Matching.SizeBand = "additions+deletions over changed_files: 0, 1-10, 11-50, 51-200, 201-1000, 1001+"
	m.Matching.Assignment = "positives in ascending PR order; each takes the unused same-cell negative with the nearest stateful score, then nearest changed lines, then lowest PR"
	m.Matching.Negative = "zero strong, medium and weak signals, no commit relationships, summary_rank_score 0, correlated by a counting build (provenance.correlated_by set) with uninspected_commit_count 0"
	m.Pairs = []Pair{}
	m.UnmatchedPositives = []int{}

	explicit := map[int]bool{}
	for _, pr := range opt.Positives {
		explicit[pr] = true
	}

	seen := map[int]bool{}
	var positives []classified
	negatives := map[Cell][]classified{}
	for i := range records {
		rec := &records[i]
		pr := rec.Original.Number
		if seen[pr] {
			return nil, nil, fmt.Errorf("PR #%d appears more than once in the input; refusing to choose", pr)
		}
		seen[pr] = true

		exp, ok := exposureDays(rec)
		if !ok {
			if explicit[pr] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but has no usable merged_at/observation_end", pr)
			}
			m.Exclusions.MissingDates++
			continue
		}
		if exp < opt.MinExposureDays {
			if explicit[pr] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but its exposure is %d days, below the %d-day minimum", pr, exp, opt.MinExposureDays)
			}
			m.Exclusions.ExposureBelowMinimum++
			continue
		}
		if rec.Stateful.Score < opt.MinScore {
			if explicit[pr] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but its stateful score %.4f is below --min-score %.4f", pr, rec.Stateful.Score, opt.MinScore)
			}
			m.Exclusions.BelowMinScore++
			continue
		}
		c := classified{rec: rec, exposure: exp, lines: changedLines(rec)}
		c.cell = Cell{Category: rec.Stateful.Category, Subsystem: subsystemOf(rec.Original.ChangedFiles), SizeBand: sizeBandOf(c.lines)}
		switch {
		case isZeroSignal(rec):
			if explicit[pr] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but has no corrective evidence at any tier", pr)
			}
			switch {
			case !correlationCounted(rec):
				m.Exclusions.UncountedNegative++
			case rec.Retrospective.UninspectedCommitCount > 0:
				m.Exclusions.UninspectedNegative++
			default:
				negatives[c.cell] = append(negatives[c.cell], c)
			}
		case isPositive(rec, opt.PositiveSignals):
			if len(explicit) > 0 && !explicit[pr] {
				m.Exclusions.NotInPositiveList++
				continue
			}
			if fix, ok := earliestFixDate(rec, opt.PositiveSignals); ok {
				fix = fix.UTC()
				c.fixDate = &fix
			}
			if !opt.MinFixDate.IsZero() {
				switch {
				case c.fixDate == nil:
					if explicit[pr] {
						return nil, nil, fmt.Errorf("PR #%d was listed as a positive but its corrective signals carry no timestamp", pr)
					}
					m.Exclusions.NoFixDate++
					continue
				case c.fixDate.Before(opt.MinFixDate):
					if explicit[pr] {
						return nil, nil, fmt.Errorf("PR #%d was listed as a positive but its earliest fix (%s) predates --min-fix-date %s", pr, c.fixDate.Format("2006-01-02"), opt.MinFixDate.UTC().Format("2006-01-02"))
					}
					m.Exclusions.FixBeforeMinDate++
					continue
				}
			}
			positives = append(positives, c)
		default:
			if explicit[pr] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but its evidence does not meet --positive-signals=%s", pr, opt.PositiveSignals)
			}
			m.Exclusions.PartialEvidence++
		}
	}
	for pr := range explicit {
		if !seen[pr] {
			return nil, nil, fmt.Errorf("PR #%d was listed as a positive but is not in the input", pr)
		}
	}

	sort.Slice(positives, func(i, j int) bool { return positives[i].rec.Original.Number < positives[j].rec.Original.Number })
	for _, list := range negatives {
		sort.Slice(list, func(i, j int) bool { return list[i].rec.Original.Number < list[j].rec.Original.Number })
	}

	var cases []Case
	used := map[int]bool{}
	for _, p := range positives {
		best := -1
		pool := negatives[p.cell]
		for i, n := range pool {
			if used[n.rec.Original.Number] {
				continue
			}
			if best < 0 || closer(p, n, pool[best]) {
				best = i
			}
		}
		if best < 0 {
			if explicit[p.rec.Original.Number] {
				return nil, nil, fmt.Errorf("PR #%d was listed as a positive but no zero-signal PR matches its cell %+v", p.rec.Original.Number, p.cell)
			}
			m.UnmatchedPositives = append(m.UnmatchedPositives, p.rec.Original.Number)
			m.Exclusions.UnmatchedPositive++
			continue
		}
		n := pool[best]
		used[n.rec.Original.Number] = true
		id := fmt.Sprintf("pair-%04d", len(m.Pairs)+1)
		m.Pairs = append(m.Pairs, Pair{ID: id, Positive: p.rec.Original.Number, Negative: n.rec.Original.Number, Cell: p.cell})
		pc, err := newCase(p, labelRisky, id, n.rec.Original.Number)
		if err != nil {
			return nil, nil, err
		}
		nc, err := newCase(n, labelClean, id, p.rec.Original.Number)
		if err != nil {
			return nil, nil, err
		}
		cases = append(cases, pc, nc)
	}
	for _, list := range negatives {
		for _, n := range list {
			if !used[n.rec.Original.Number] {
				m.Exclusions.UnusedNegative++
			}
		}
	}

	m.PositiveCount = len(m.Pairs)
	m.NegativeCount = len(m.Pairs)
	m.RecordCount = len(cases)
	if err := stampProvenance(m, cases); err != nil {
		return nil, nil, err
	}
	return cases, m, nil
}

// closer reports whether candidate a is a better match for p than b: nearest
// stateful score, then nearest change size, then lowest PR number.
func closer(p, a, b classified) bool {
	da, db := absf(a.rec.Stateful.Score-p.rec.Stateful.Score), absf(b.rec.Stateful.Score-p.rec.Stateful.Score)
	if da != db {
		return da < db
	}
	la, lb := absi(a.lines-p.lines), absi(b.lines-p.lines)
	if la != lb {
		return la < lb
	}
	return a.rec.Original.Number < b.rec.Original.Number
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func absi(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func newCase(c classified, label, pairID string, matched int) (Case, error) {
	raw, err := json.Marshal(c.rec)
	if err != nil {
		return Case{}, fmt.Errorf("marshal PR #%d: %w", c.rec.Original.Number, err)
	}
	return Case{
		SchemaVersion:          CaseSchemaVersion,
		SamplerLabel:           label,
		PairID:                 pairID,
		Repository:             c.rec.Original.Repository,
		PR:                     c.rec.Original.Number,
		MatchedPR:              matched,
		ExposureDays:           c.exposure,
		ChangedLines:           c.lines,
		Cell:                   c.cell,
		CorrelationCounted:     correlationCounted(c.rec),
		UninspectedCommitCount: c.rec.Retrospective.UninspectedCommitCount,
		EarliestFixDate:        c.fixDate,
		RecordSHA256:           digest(raw),
		Record:                 *c.rec,
	}, nil
}

// stampProvenance copies the per-record provenance identity onto the manifest and
// computes the content hash. Every selected record must carry the same identity: a
// cohort assembled from two correlation runs, or two configurations, has no single
// provenance to report, and is refused rather than stamped with the first one seen.
func stampProvenance(m *Manifest, cases []Case) error {
	if len(cases) == 0 {
		return fmt.Errorf("no matched pairs: %d positives had no same-cell negative (exclusions: %+v)", len(m.UnmatchedPositives), m.Exclusions)
	}
	first := cases[0].Record
	m.ConfigHash = first.Provenance.ConfigHash
	m.MinerVersion = first.Provenance.MinerVersion
	m.TargetRepoHead = first.Provenance.TargetRepoHeadSHA
	m.ObservationEnd = first.Provenance.ObservationEnd
	repo := first.Original.Repository
	hashes := make([]string, 0, len(cases))
	for _, c := range cases {
		p := c.Record.Provenance
		switch {
		case p.ConfigHash != m.ConfigHash:
			return fmt.Errorf("PR #%d has config_hash %q, PR #%d has %q; a cohort must come from one configuration", c.PR, p.ConfigHash, first.Original.Number, m.ConfigHash)
		case p.MinerVersion != m.MinerVersion:
			return fmt.Errorf("PR #%d has miner_version %q, PR #%d has %q; a cohort must come from one miner build", c.PR, p.MinerVersion, first.Original.Number, m.MinerVersion)
		case p.TargetRepoHeadSHA != m.TargetRepoHead:
			return fmt.Errorf("PR #%d has target_repo_head_sha %q, PR #%d has %q; a cohort must come from one repository state", c.PR, p.TargetRepoHeadSHA, first.Original.Number, m.TargetRepoHead)
		case !p.ObservationEnd.Equal(m.ObservationEnd):
			return fmt.Errorf("PR #%d has observation_end %s, PR #%d has %s; a cohort must share one observation end", c.PR, p.ObservationEnd, first.Original.Number, m.ObservationEnd)
		case c.Repository != repo:
			return fmt.Errorf("PR #%d is from %q, PR #%d from %q; a cohort must come from one repository", c.PR, c.Repository, first.Original.Number, repo)
		}
		hashes = append(hashes, c.RecordSHA256)
	}
	sort.Strings(hashes)
	m.RecordsSHA256 = digest([]byte(strings.Join(hashes, "\n") + "\n"))
	return nil
}

func write(out string, cases []Case, m *Manifest) error {
	parent := filepath.Dir(out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".cohort-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}

	var lines strings.Builder
	for _, c := range cases {
		b, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("marshal case PR #%d: %w", c.PR, err)
		}
		lines.Write(b)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(tmp, "cohort.jsonl"), []byte(lines.String()), 0o644); err != nil {
		return err
	}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "cohort-manifest.json"), append(mb, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}
