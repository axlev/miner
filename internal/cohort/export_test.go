package cohort

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miner/internal/model"
)

var observationEnd = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// cohortRecord synthesizes one scored, correlated record with uniform provenance.
// merged is expressed as days before observationEnd so exposure is explicit.
func cohortRecord(pr int, subsystem string, lines int, score float64, strong, medium, weak int, daysBeforeEnd int) model.PRCandidateRecord {
	rec := model.PRCandidateRecord{SchemaVersion: model.SchemaVersion}
	rec.Original.Number = pr
	rec.Original.Repository = "owner/repo"
	rec.Original.Title = "change"
	rec.Original.MergedAt = observationEnd.Add(-time.Duration(daysBeforeEnd) * 24 * time.Hour)
	rec.Original.ChangedFiles = []model.ChangedFile{{Path: subsystem + "/a.c", Status: "modified", Additions: lines, Deletions: 0}}
	rec.Stateful.Score = score
	switch {
	case score >= 0.6:
		rec.Stateful.Category = "HIGH"
	case score >= 0.3:
		rec.Stateful.Category = "MEDIUM"
	default:
		rec.Stateful.Category = "LOW"
	}
	for i := 0; i < strong; i++ {
		rec.Retrospective.StrongSignals = append(rec.Retrospective.StrongSignals, model.StrongCorrectiveSignal{SignalType: "FIXES_PR"})
	}
	for i := 0; i < medium; i++ {
		rec.Retrospective.MediumSignals = append(rec.Retrospective.MediumSignals, model.MediumCorrectiveSignal{SignalType: "SAME_FUNCTION_FIX"})
	}
	for i := 0; i < weak; i++ {
		rec.Retrospective.WeakSignals = append(rec.Retrospective.WeakSignals, model.WeakCorrectiveSignal{SignalType: "SAME_FILE_MODIFICATION"})
	}
	if strong+medium > 0 {
		rec.Retrospective.SummaryRankScore = 1
	}
	if strong+medium > 0 {
		for i := range rec.Retrospective.StrongSignals {
			rec.Retrospective.StrongSignals[i].Timestamp = rec.Original.MergedAt.Add(time.Duration(30+i) * 24 * time.Hour)
		}
		for i := range rec.Retrospective.MediumSignals {
			rec.Retrospective.MediumSignals[i].Timestamp = rec.Original.MergedAt.Add(time.Duration(60+i) * 24 * time.Hour)
		}
	}
	rec.Provenance = model.Provenance{
		MinerVersion:      "abc123",
		ConfigHash:        "cfg",
		HarvestedAt:       observationEnd,
		ObservationEnd:    observationEnd,
		TargetRepoHeadSHA: "head",
		GitHubAPIVersion:  "2022-11-28",
		CorrelatedBy:      "build-counting",
	}
	return rec
}

func TestNegativesRequireACountedCleanCorrelation(t *testing.T) {
	uncounted := cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400)
	uncounted.Provenance.CorrelatedBy = ""
	uninspected := cohortRecord(3, "bgpd", 20, 0.7, 0, 0, 0, 400)
	uninspected.Retrospective.UninspectedCommitCount = 1
	uninspected.Retrospective.UninspectedCommitSHAs = []string{"deadbeef"}
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		uncounted,
		uninspected,
		cohortRecord(4, "bgpd", 20, 0.7, 0, 0, 0, 400), // counted, zero: the only real negative
	}
	opt := options(t, "out")
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Negative != 4 {
		t.Fatalf("pairs = %+v, want 1 matched to 4", m.Pairs)
	}
	if m.Exclusions.UncountedNegative != 1 || m.Exclusions.UninspectedNegative != 1 {
		t.Errorf("exclusions = %+v, want uncounted 1 and uninspected 1", m.Exclusions)
	}
	cases := readCases(t, opt.Out)
	if !cases[0].CorrelationCounted || cases[0].UninspectedCommitCount != 0 || !cases[1].CorrelationCounted {
		t.Errorf("cases should carry the count for both classes: %+v / %+v", cases[0], cases[1])
	}
	if cases[0].EarliestFixDate == nil || !cases[0].EarliestFixDate.Equal(records[0].Retrospective.StrongSignals[0].Timestamp) {
		t.Errorf("positive earliest_fix_date = %v", cases[0].EarliestFixDate)
	}
	if cases[1].EarliestFixDate != nil {
		t.Errorf("negative must not carry earliest_fix_date: %v", cases[1].EarliestFixDate)
	}
	if cases[0].SchemaVersion != CaseSchemaVersion || m.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("schema versions %q / %q", cases[0].SchemaVersion, m.SchemaVersion)
	}
}

// An uncounted positive is informational, never refused: a strong signal does not
// depend on the diff, so an old correlation still proves RISKY.
func TestUncountedPositiveIsAdmittedAndFlagged(t *testing.T) {
	pos := cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400)
	pos.Provenance.CorrelatedBy = ""
	records := []model.PRCandidateRecord{pos, cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400)}
	opt := options(t, "out")
	if _, err := Export(records, nil, opt); err != nil {
		t.Fatal(err)
	}
	cases := readCases(t, opt.Out)
	if cases[0].CorrelationCounted {
		t.Errorf("positive should be flagged as uncounted")
	}
}

func TestMinFixDateAdmitsOnlyLateFixes(t *testing.T) {
	late := cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400)
	late.Retrospective.StrongSignals[0].Timestamp = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	early := cohortRecord(3, "bgpd", 20, 0.7, 1, 0, 0, 400)
	early.Retrospective.StrongSignals[0].Timestamp = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	undated := cohortRecord(5, "bgpd", 20, 0.7, 1, 0, 0, 400)
	undated.Retrospective.StrongSignals[0].Timestamp = time.Time{}
	records := []model.PRCandidateRecord{
		late, early, undated,
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),
		cohortRecord(4, "bgpd", 20, 0.7, 0, 0, 0, 400),
		cohortRecord(6, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "out")
	opt.MinFixDate = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 {
		t.Fatalf("pairs = %+v, want only PR 1", m.Pairs)
	}
	if m.Exclusions.FixBeforeMinDate != 1 || m.Exclusions.NoFixDate != 1 {
		t.Errorf("exclusions = %+v", m.Exclusions)
	}
	if m.Filters.MinFixDate != "2026-06-01T00:00:00Z" {
		t.Errorf("min_fix_date not recorded: %q", m.Filters.MinFixDate)
	}
	for _, prs := range [][]int{{3}, {5}} {
		o := options(t, fmt.Sprintf("explicit-%d", prs[0]))
		o.MinFixDate = opt.MinFixDate
		o.Positives = prs
		if _, err := Export(records, nil, o); err == nil {
			t.Errorf("explicit positive %v should fail closed under min-fix-date", prs)
		}
	}
	// The medium fallback applies only under "any".
	medOnly := cohortRecord(7, "zebra", 20, 0.7, 0, 1, 0, 400)
	medOnly.Retrospective.MediumSignals[0].Timestamp = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	o := options(t, "any")
	o.PositiveSignals = "any"
	o.MinFixDate = opt.MinFixDate
	m2, err := Export([]model.PRCandidateRecord{medOnly, cohortRecord(8, "zebra", 20, 0.7, 0, 0, 0, 400)}, nil, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Pairs) != 1 {
		t.Errorf("medium-only positive under any should be admitted by its medium timestamp: %+v", m2)
	}
}

func options(t *testing.T, name string) ExportOptions {
	t.Helper()
	return ExportOptions{
		Input:           "candidates.jsonl",
		Out:             filepath.Join(t.TempDir(), name),
		Name:            "test-cohort",
		PositiveSignals: "strong",
		MinExposureDays: DefaultMinExposureDays,
	}
}

func readCases(t *testing.T, out string) []Case {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, "cohort.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []Case
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("bad case line %q: %v", line, err)
		}
		cases = append(cases, c)
	}
	return cases
}

func TestNegativesRequireZeroSignalsAtEveryTier(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400), // positive
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 1, 400), // weak only: not a negative
		cohortRecord(3, "bgpd", 20, 0.7, 0, 1, 0, 400), // medium only: not a negative under strong
		cohortRecord(4, "bgpd", 20, 0.7, 0, 0, 0, 400), // the only real negative
	}
	opt := options(t, "out")
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 || m.Pairs[0].Negative != 4 {
		t.Fatalf("pairs = %+v, want 1 matched to 4", m.Pairs)
	}
	if m.Exclusions.PartialEvidence != 2 {
		t.Errorf("partial_evidence = %d, want 2 (weak-only and medium-only)", m.Exclusions.PartialEvidence)
	}
	cases := readCases(t, opt.Out)
	if len(cases) != 2 || cases[0].SamplerLabel != "RISKY" || cases[1].SamplerLabel != "CLEAN" {
		t.Fatalf("cases = %+v", cases)
	}
	if cases[0].MatchedPR != 4 || cases[1].MatchedPR != 1 || cases[0].PairID != cases[1].PairID {
		t.Errorf("pair linkage wrong: %+v / %+v", cases[0], cases[1])
	}
	if cases[0].ExposureDays != 400 || cases[1].ExposureDays != 400 {
		t.Errorf("exposure_days = %d/%d, want 400", cases[0].ExposureDays, cases[1].ExposureDays)
	}
	if cases[0].SchemaVersion != CaseSchemaVersion || m.SchemaVersion != ManifestSchemaVersion {
		t.Errorf("schema versions %q / %q", cases[0].SchemaVersion, m.SchemaVersion)
	}
	// The embedded record is the pipeline record, untouched.
	if cases[0].Record.Original.Number != 1 || len(cases[0].Record.Retrospective.StrongSignals) != 1 {
		t.Errorf("embedded record altered: %+v", cases[0].Record)
	}
}

func TestMediumCountsAsPositiveOnlyUnderAny(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 0, 1, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "out")
	opt.PositiveSignals = "any"
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 {
		t.Fatalf("pairs = %+v", m.Pairs)
	}
}

func TestExposureFloorAppliesToBothClasses(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),
		cohortRecord(3, "zebra", 20, 0.7, 1, 0, 0, 100), // recent positive: excluded
		cohortRecord(4, "zebra", 20, 0.7, 0, 0, 0, 100), // recent negative: excluded
		cohortRecord(5, "zebra", 20, 0.7, 1, 0, 0, 179), // one day short
		cohortRecord(6, "zebra", 20, 0.7, 0, 0, 0, 180), // exactly at the floor: kept
	}
	missing := cohortRecord(7, "bgpd", 20, 0.7, 0, 0, 0, 400)
	missing.Provenance.ObservationEnd = time.Time{}
	inverted := cohortRecord(8, "bgpd", 20, 0.7, 0, 0, 0, 400)
	inverted.Provenance.ObservationEnd = inverted.Original.MergedAt.Add(-time.Hour)
	records = append(records, missing, inverted)

	opt := options(t, "out")
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 || m.Pairs[0].Negative != 2 {
		t.Fatalf("pairs = %+v", m.Pairs)
	}
	if m.Exclusions.ExposureBelowMinimum != 3 {
		t.Errorf("exposure_below_minimum = %d, want 3", m.Exclusions.ExposureBelowMinimum)
	}
	if m.Exclusions.MissingDates != 2 {
		t.Errorf("missing_dates = %d, want 2 (zero and inverted observation_end)", m.Exclusions.MissingDates)
	}
	if m.Exclusions.UnusedNegative != 1 {
		t.Errorf("unused_negative = %d, want 1 (PR 6 has no positive left in zebra)", m.Exclusions.UnusedNegative)
	}
}

func TestMatchingStaysInsideTheCellAndPrefersNearestScore(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(10, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(11, "bgpd", 20, 0.62, 0, 0, 0, 400),  // same cell, further score
		cohortRecord(12, "bgpd", 20, 0.71, 0, 0, 0, 400),  // same cell, nearest score: chosen
		cohortRecord(13, "zebra", 20, 0.70, 0, 0, 0, 400), // other subsystem
		cohortRecord(14, "bgpd", 500, 0.70, 0, 0, 0, 400), // other size band
		cohortRecord(15, "bgpd", 20, 0.40, 0, 0, 0, 400),  // other category
		cohortRecord(16, "bgpd", 20, 0.70, 1, 0, 0, 400),  // second positive takes the remaining one
		cohortRecord(17, "bgpd", 20, 0.70, 1, 0, 0, 400),  // third positive: nothing left
	}
	opt := options(t, "out")
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	want := []Pair{
		{ID: "pair-0001", Positive: 10, Negative: 12, Cell: Cell{"HIGH", "bgpd", "11-50"}},
		{ID: "pair-0002", Positive: 16, Negative: 11, Cell: Cell{"HIGH", "bgpd", "11-50"}},
	}
	if len(m.Pairs) != len(want) {
		t.Fatalf("pairs = %+v, want %+v", m.Pairs, want)
	}
	for i := range want {
		if m.Pairs[i] != want[i] {
			t.Errorf("pair %d = %+v, want %+v", i, m.Pairs[i], want[i])
		}
	}
	if len(m.UnmatchedPositives) != 1 || m.UnmatchedPositives[0] != 17 {
		t.Errorf("unmatched = %v, want [17]", m.UnmatchedPositives)
	}
	if m.Exclusions.UnusedNegative != 3 {
		t.Errorf("unused_negative = %d, want 3", m.Exclusions.UnusedNegative)
	}
}

func TestExportIsDeterministicUnderInputOrder(t *testing.T) {
	forward := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.65, 0, 0, 0, 400),
		cohortRecord(3, "bgpd", 20, 0.68, 0, 0, 0, 400),
		cohortRecord(4, "bgpd", 20, 0.66, 1, 0, 0, 400),
	}
	reversed := []model.PRCandidateRecord{forward[3], forward[2], forward[1], forward[0]}

	a, b := options(t, "a"), options(t, "b")
	ma, err := Export(forward, []byte("x"), a)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := Export(reversed, []byte("x"), b)
	if err != nil {
		t.Fatal(err)
	}
	if ma.RecordsSHA256 != mb.RecordsSHA256 {
		t.Errorf("records hash differs by input order: %s vs %s", ma.RecordsSHA256, mb.RecordsSHA256)
	}
	ja, _ := os.ReadFile(filepath.Join(a.Out, "cohort.jsonl"))
	jb, _ := os.ReadFile(filepath.Join(b.Out, "cohort.jsonl"))
	if string(ja) != string(jb) {
		t.Errorf("cohort.jsonl differs by input order")
	}
	// The manifests differ only in the output path they were written to, which is
	// not recorded, so they must be byte-identical.
	fa, _ := os.ReadFile(filepath.Join(a.Out, "cohort-manifest.json"))
	fb, _ := os.ReadFile(filepath.Join(b.Out, "cohort-manifest.json"))
	if string(fa) != string(fb) {
		t.Errorf("manifest differs by input order:\n%s\n---\n%s", fa, fb)
	}
}

// TestFilterArgumentsAreCohortIdentity is the reason the manifest exists: two
// exports over one candidates file can select different records with identical
// per-record provenance, and only the manifest tells them apart.
func TestFilterArgumentsAreCohortIdentity(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.70, 0, 0, 0, 400),
		cohortRecord(3, "bgpd", 20, 0.40, 1, 0, 0, 400),
		cohortRecord(4, "bgpd", 20, 0.40, 0, 0, 0, 400),
	}
	loose, strict := options(t, "loose"), options(t, "strict")
	strict.MinScore = 0.5
	ml, err := Export(records, nil, loose)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := Export(records, nil, strict)
	if err != nil {
		t.Fatal(err)
	}
	if len(ml.Pairs) != 2 || len(ms.Pairs) != 1 {
		t.Fatalf("pairs: loose %d, strict %d", len(ml.Pairs), len(ms.Pairs))
	}
	if ml.RecordsSHA256 == ms.RecordsSHA256 {
		t.Errorf("different cohorts share a records hash")
	}
	if ml.ConfigHash != ms.ConfigHash || ml.MinerVersion != ms.MinerVersion || !ml.ObservationEnd.Equal(ms.ObservationEnd) {
		t.Errorf("per-record provenance should be identical across the two cohorts")
	}
	if ms.Filters.MinScore != 0.5 || ml.Filters.MinScore != 0 {
		t.Errorf("filter arguments not recorded: %+v / %+v", ml.Filters, ms.Filters)
	}
	if ms.Exclusions.BelowMinScore != 2 {
		t.Errorf("below_min_score = %d, want 2", ms.Exclusions.BelowMinScore)
	}
}

func TestExplicitPositivesFailClosed(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.70, 0, 0, 0, 400),
		cohortRecord(3, "zebra", 20, 0.70, 1, 0, 0, 400), // no zebra negative exists
		cohortRecord(4, "bgpd", 20, 0.70, 0, 0, 1, 400),  // weak-only
		cohortRecord(5, "bgpd", 20, 0.70, 1, 0, 0, 50),   // too recent
	}
	cases := []struct {
		positives []int
		wantErr   string
	}{
		{[]int{3}, "no zero-signal PR matches its cell"},
		{[]int{4}, "does not meet --positive-signals=strong"},
		{[]int{2}, "no corrective evidence at any tier"},
		{[]int{5}, "below the 180-day minimum"},
		{[]int{99}, "not in the input"},
	}
	for _, c := range cases {
		opt := options(t, "out")
		opt.Positives = c.positives
		_, err := Export(records, nil, opt)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("positives %v: err = %v, want containing %q", c.positives, err, c.wantErr)
		}
		if _, statErr := os.Lstat(opt.Out); !os.IsNotExist(statErr) {
			t.Errorf("positives %v: output written despite failure", c.positives)
		}
	}

	// A valid explicit list narrows the positives and is recorded in the manifest.
	opt := options(t, "ok")
	opt.Positives = []int{1}
	m, err := Export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 || m.Exclusions.NotInPositiveList != 1 {
		t.Errorf("manifest = %+v", m)
	}
	if len(m.Filters.Positives) != 1 || m.Filters.Positives[0] != 1 {
		t.Errorf("explicit list not recorded: %v", m.Filters.Positives)
	}
}

func TestNonUniformProvenanceIsRefused(t *testing.T) {
	base := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.70, 0, 0, 0, 400),
	}
	mutations := map[string]func(r *model.PRCandidateRecord){
		"config_hash":          func(r *model.PRCandidateRecord) { r.Provenance.ConfigHash = "other" },
		"miner_version":        func(r *model.PRCandidateRecord) { r.Provenance.MinerVersion = "other" },
		"target_repo_head_sha": func(r *model.PRCandidateRecord) { r.Provenance.TargetRepoHeadSHA = "other" },
		"observation_end":      func(r *model.PRCandidateRecord) { r.Provenance.ObservationEnd = observationEnd.Add(time.Hour) },
		"repository":           func(r *model.PRCandidateRecord) { r.Original.Repository = "other/repo" },
	}
	for field, mutate := range mutations {
		records := []model.PRCandidateRecord{base[0], base[1]}
		mutate(&records[1])
		opt := options(t, field)
		_, err := Export(records, nil, opt)
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: err = %v, want a refusal naming the field", field, err)
		}
		if _, statErr := os.Lstat(opt.Out); !os.IsNotExist(statErr) {
			t.Errorf("%s: output written despite failure", field)
		}
	}
}

func TestDuplicatePRIsRefused(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(1, "bgpd", 20, 0.70, 0, 0, 0, 400),
	}
	if _, err := Export(records, nil, options(t, "out")); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("err = %v", err)
	}
}

func TestOutputIsImmutableAndAtomic(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.70, 0, 0, 0, 400),
	}
	opt := options(t, "out")
	if _, err := Export(records, nil, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(records, nil, opt); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second export over an existing directory: err = %v", err)
	}
	// A failing export leaves neither the directory nor a temporary sibling.
	failing := options(t, "fail")
	failing.Positives = []int{99}
	if _, err := Export(records, nil, failing); err == nil {
		t.Fatal("expected failure")
	}
	entries, err := os.ReadDir(filepath.Dir(failing.Out))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("parent not clean after failure: %v", entries)
	}
	// And a successful one leaves no temporary sibling either.
	entries, _ = os.ReadDir(filepath.Dir(opt.Out))
	if len(entries) != 1 {
		t.Errorf("parent holds %d entries after success, want 1", len(entries))
	}
}

func TestNoPairsIsAnError(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.70, 1, 0, 0, 400),
		cohortRecord(2, "zebra", 20, 0.70, 0, 0, 0, 400),
	}
	opt := options(t, "out")
	if _, err := Export(records, nil, opt); err == nil || !strings.Contains(err.Error(), "no matched pairs") {
		t.Errorf("err = %v", err)
	}
	if _, statErr := os.Lstat(opt.Out); !os.IsNotExist(statErr) {
		t.Errorf("output written despite failure")
	}
}

func TestSizeBands(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1-10", 10: "1-10", 11: "11-50", 50: "11-50", 51: "51-200", 200: "51-200", 201: "201-1000", 1000: "201-1000", 1001: "1001+", 50000: "1001+"}
	for lines, want := range cases {
		if got := sizeBandOf(lines); got != want {
			t.Errorf("sizeBandOf(%d) = %q, want %q", lines, got, want)
		}
	}
}
