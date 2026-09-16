package cohort

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miner/internal/model"
)

// TestPrimarySubsystemIsCodeFirst is the rule the evaluator's read forced: a change
// whose topotests outnumber its code files is a change to the code, not to "tests".
func TestPrimarySubsystemIsCodeFirst(t *testing.T) {
	files := func(spec ...[2]any) []model.ChangedFile {
		var out []model.ChangedFile
		for _, s := range spec {
			out = append(out, model.ChangedFile{Path: s[0].(string), Additions: s[1].(int)})
		}
		return out
	}
	cases := []struct {
		name  string
		files []model.ChangedFile
		want  string
	}{
		{"code outweighed by tests in file count", files(
			[2]any{"ospfd/ospf_lsa.c", 4},
			[2]any{"tests/topotests/a/test_a.py", 30},
			[2]any{"tests/topotests/b/test_b.py", 30},
			[2]any{"tests/topotests/c/test_c.py", 30},
		), "ospfd"},
		{"two code subsystems, more lines wins", files(
			[2]any{"bgpd/bgp_fsm.c", 10},
			[2]any{"zebra/rib.c", 40},
			[2]any{"doc/user.rst", 100},
		), "zebra"},
		{"docs and tooling never win", files(
			[2]any{"lib/table.c", 1},
			[2]any{"doc/x.rst", 90},
			[2]any{"tools/y.sh", 90},
			[2]any{".github/workflows/ci.yml", 90},
		), "lib"},
		{"top-level build files are ancillary", files(
			[2]any{"configure.ac", 200},
			[2]any{"bgpd/bgp_route.c", 2},
		), "bgpd"},
		{"nothing but tests keys as tests", files(
			[2]any{"tests/topotests/a/test_a.py", 30},
		), "tests"},
		{"nothing but docs keys as docs", files(
			[2]any{"doc/user.rst", 5},
		), "doc"},
		{"no files", nil, "(none)"},
	}
	for _, c := range cases {
		if got := subsystemOf(c.files); got != c.want {
			t.Errorf("%s: subsystem = %q, want %q", c.name, got, c.want)
		}
	}
	// Ties between code groups break on name, never on map order.
	tied := files([2]any{"bgpd/a.c", 10}, [2]any{"zebra/b.c", 10})
	for i := 0; i < 20; i++ {
		if got := subsystemOf(tied); got != "bgpd" {
			t.Fatalf("tie broke to %q on iteration %d", got, i)
		}
	}
}

func TestNormalizeMatchKey(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "category,subsystem,size_band",
		"subsystem":                    "subsystem",
		"category,subsystem":           "category,subsystem",
		"size_band, category":          "category,size_band",
		"SUBSYSTEM,subsystem":          "subsystem",
		"subsystem,category,size_band": "category,subsystem,size_band",
	} {
		var key []string
		for _, k := range strings.Split(in, ",") {
			if k = strings.TrimSpace(k); k != "" {
				key = append(key, k)
			}
		}
		got, err := normalizeMatchKey(key)
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("%q -> %v (%v), want %q", in, got, err, want)
		}
	}
	if _, err := normalizeMatchKey([]string{"daemon"}); err == nil {
		t.Errorf("unknown key accepted")
	}
}

// TestMatchKeyRelaxationPairsAcrossBands: a positive with no same-cell negative is
// matched once size_band leaves the key, and the manifest says which key was used.
func TestMatchKeyRelaxationPairsAcrossBands(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 120, 0.7, 1, 0, 0, 400), // 51-200
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),  // 11-50, only negative
	}
	strict := options(t, "strict")
	if _, err := export(records, nil, strict); err == nil || !strings.Contains(err.Error(), "no matched pairs") {
		t.Fatalf("the full key must leave this positive unmatched: %v", err)
	}
	relaxed := options(t, "relaxed")
	relaxed.MatchKey = []string{"category", "subsystem"}
	m, err := export(records, nil, relaxed)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 || m.Pairs[0].Negative != 2 {
		t.Fatalf("pairs = %+v", m.Pairs)
	}
	if strings.Join(m.Matching.Key, ",") != "category,subsystem" || m.Matching.SubsystemRule != SubsystemRule {
		t.Errorf("matching block = %+v", m.Matching)
	}
	// The case still records its full cell, so the dropped field stays visible.
	cases := readCases(t, relaxed.Out)
	if cases[0].Cell.SizeBand != "51-200" || cases[1].Cell.SizeBand != "11-50" {
		t.Errorf("cells lost the unmatched field: %+v / %+v", cases[0].Cell, cases[1].Cell)
	}
}

func TestMaxPerSubsystemCapsPositives(t *testing.T) {
	var records []model.PRCandidateRecord
	for i := 1; i <= 4; i++ { // four bgpd positives, four bgpd negatives
		records = append(records, cohortRecord(100+i, "bgpd", 20, 0.7, 1, 0, 0, 400))
		records = append(records, cohortRecord(200+i, "bgpd", 20, 0.7, 0, 0, 0, 400))
	}
	records = append(records, cohortRecord(300, "zebra", 20, 0.7, 1, 0, 0, 400))
	records = append(records, cohortRecord(301, "zebra", 20, 0.7, 0, 0, 0, 400))

	opt := options(t, "capped")
	opt.MaxPerSubsystem = 2
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	perSub := map[string]int{}
	for _, p := range m.Pairs {
		perSub[p.Cell.Subsystem]++
	}
	if perSub["bgpd"] != 2 || perSub["zebra"] != 1 {
		t.Errorf("pairs per subsystem = %v, want bgpd 2 and zebra 1", perSub)
	}
	if m.Exclusions.OverSubsystemQuota != 2 || m.Filters.MaxPerSubsystem != 2 {
		t.Errorf("quota not recorded: exclusions %+v filters %+v", m.Exclusions, m.Filters)
	}
	// An explicit list that exceeds the cap fails rather than being trimmed.
	over := options(t, "over")
	over.MaxPerSubsystem = 2
	over.Positives = []int{101, 102, 103}
	if _, err := export(records, nil, over); err == nil || !strings.Contains(err.Error(), "max-per-subsystem") {
		t.Errorf("explicit list over the cap: err = %v", err)
	}
	if _, statErr := os.Lstat(over.Out); !os.IsNotExist(statErr) {
		t.Errorf("output written despite the refusal")
	}
	// At the cap, the same list is accepted.
	ok := options(t, "ok")
	ok.MaxPerSubsystem = 2
	ok.Positives = []int{101, 102}
	if _, err := export(records, nil, ok); err != nil {
		t.Errorf("list at the cap must be accepted: %v", err)
	}
}

// TestMultiSourceCohort is the shape the H1 cohort needs: two collection windows,
// each with its own provenance, in one cohort with one identity.
func TestMultiSourceCohort(t *testing.T) {
	older := []model.PRCandidateRecord{
		cohortRecord(10, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(11, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	for i := range older { // a different window: its own config, head, harvest, cutoff
		older[i].Provenance.ConfigHash = "cfg-2024"
		older[i].Provenance.TargetRepoHeadSHA = "head-2024"
		older[i].Provenance.ObservationEnd = observationEnd.AddDate(-1, 0, 0)
		older[i].Original.MergedAt = older[i].Provenance.ObservationEnd.AddDate(0, 0, -400)
	}
	newer := []model.PRCandidateRecord{
		cohortRecord(20, "zebra", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(21, "zebra", 20, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "multi")
	opt.Sources = []Source{
		{Label: "2024", Path: "a/candidates.jsonl", Records: older, Bytes: []byte("a")},
		{Label: "2026H1", Path: "b/candidates.jsonl", Records: newer, Bytes: []byte("b")},
	}
	m, err := Export(opt)
	if err != nil {
		t.Fatalf("a cohort spanning two windows must be allowed: %v", err)
	}
	if len(m.Sources) != 2 || m.Sources[0].Label != "2024" || m.Sources[1].Label != "2026H1" {
		t.Fatalf("sources = %+v", m.Sources)
	}
	if m.Sources[0].ConfigHash != "cfg-2024" || m.Sources[1].ConfigHash != "cfg" {
		t.Errorf("each source keeps its own provenance: %+v", m.Sources)
	}
	if m.Sources[0].CaseCount != 2 || m.Sources[1].CaseCount != 2 || m.RecordCount != 4 {
		t.Errorf("case counts = %+v", m.Sources)
	}
	// Identity is the digest over the per-source digests, in order.
	want := digest([]byte(m.Sources[0].RecordsSHA256 + "\n" + m.Sources[1].RecordsSHA256 + "\n"))
	if m.RecordsSHA256 != want {
		t.Errorf("records_sha256 is not the digest over the per-source digests")
	}
	for _, c := range readCases(t, opt.Out) {
		if (c.PR < 20 && c.Source != "2024") || (c.PR >= 20 && c.Source != "2026H1") {
			t.Errorf("PR %d carries source %q", c.PR, c.Source)
		}
	}
	// Uniformity still bites inside a source.
	bad := options(t, "bad")
	broken := append([]model.PRCandidateRecord(nil), older...)
	broken[1].Provenance.ConfigHash = "other"
	bad.Sources = []Source{{Label: "2024", Path: "a", Records: broken, Bytes: []byte("a")}}
	if _, err := Export(bad); err == nil || !strings.Contains(err.Error(), "config_hash") {
		t.Errorf("within-source uniformity must still be enforced: %v", err)
	}
	// A PR present in two sources is ambiguous, not a duplicate to resolve.
	dup := options(t, "dup")
	dup.Sources = []Source{
		{Label: "a", Path: "a", Records: older, Bytes: []byte("a")},
		{Label: "b", Path: "b", Records: older, Bytes: []byte("b")},
	}
	if _, err := Export(dup); err == nil || !strings.Contains(err.Error(), "appears in both") {
		t.Errorf("cross-source duplicate: %v", err)
	}
	// Labels identify a case's origin, so they must be unique.
	same := options(t, "same")
	same.Sources = []Source{
		{Label: "x", Path: "a", Records: older, Bytes: []byte("a")},
		{Label: "x", Path: "b", Records: newer, Bytes: []byte("b")},
	}
	if _, err := Export(same); err == nil || !strings.Contains(err.Error(), "share the label") {
		t.Errorf("duplicate label: %v", err)
	}
}

func TestManifestVersionAndShape(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "shape")
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != "cohort-manifest/v4" || CaseSchemaVersion != "cohort-case/v4" {
		t.Errorf("versions %q / %q", m.SchemaVersion, CaseSchemaVersion)
	}
	raw, err := os.ReadFile(filepath.Join(opt.Out, "cohort-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"schema_version", "name", "records_sha256", "sources", "filters", "matching", "pairs", "exclusions"} {
		if _, ok := top[k]; !ok {
			t.Errorf("manifest lacks %q", k)
		}
	}
	// The provenance the pre-registration names now lives per source.
	src := top["sources"].([]any)[0].(map[string]any)
	for _, k := range []string{"config_hash", "miner_version", "target_repo_head_sha", "observation_end", "correlated_by", "records_sha256"} {
		if _, ok := src[k]; !ok {
			t.Errorf("source lacks %q", k)
		}
	}
}

// TestFallbackSubsystemMatching is A9(vi): a positive with no negative in its full
// key pairs within its subsystem instead, on nearest category then nearest size
// band, and the pair says so rather than passing as a proper match.
func TestFallbackSubsystemMatching(t *testing.T) {
	// bgpd has only MEDIUM negatives, so a HIGH bgpd positive cannot match on the
	// full key — the real shape of PR 21982 in the H1 cohort.
	high := cohortRecord(1, "bgpd", 20, 0.9, 1, 0, 0, 400) // HIGH
	records := []model.PRCandidateRecord{
		high,
		cohortRecord(2, "bgpd", 500, 0.4, 0, 0, 0, 400), // MEDIUM, far band
		cohortRecord(3, "bgpd", 20, 0.4, 0, 0, 0, 400),  // MEDIUM, same band: the pick
		cohortRecord(4, "bgpd", 20, 0.1, 0, 0, 0, 400),  // LOW, same band: further category
	}
	strict := options(t, "strict")
	strict.MatchKey = []string{"category", "subsystem"}
	if _, err := export(records, nil, strict); err == nil || !strings.Contains(err.Error(), "no matched pairs") {
		t.Fatalf("without the fallback this positive must stay unmatched: %v", err)
	}

	opt := options(t, "fallback")
	opt.MatchKey = []string{"category", "subsystem"}
	opt.FallbackSubsystem = true
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 1 || m.Pairs[0].Negative != 3 {
		t.Fatalf("pairs = %+v; want PR 1 paired with the nearest-category, same-band negative 3", m.Pairs)
	}
	p := m.Pairs[0]
	if strings.Join(p.MatchKeyUsed, ",") != "subsystem" || m.FallbackPairs != 1 {
		t.Errorf("a relaxed pair must say so: match_key_used=%v fallback_pairs=%d", p.MatchKeyUsed, m.FallbackPairs)
	}
	if p.Cell.Category != "HIGH" || p.NegativeCell.Category != "MEDIUM" {
		t.Errorf("both cells must be visible on the pair: %+v / %+v", p.Cell, p.NegativeCell)
	}
	if !m.Filters.FallbackSubsystem || m.Matching.Fallback == "" {
		t.Errorf("the fallback must be recorded in the manifest: %+v %q", m.Filters, m.Matching.Fallback)
	}

	// A proper match is never overridden by the fallback.
	proper := append([]model.PRCandidateRecord{}, records...)
	proper = append(proper, cohortRecord(5, "bgpd", 20, 0.9, 0, 0, 0, 400)) // HIGH, same band
	opt2 := options(t, "proper")
	opt2.MatchKey = []string{"category", "subsystem"}
	opt2.FallbackSubsystem = true
	m2, err := export(proper, nil, opt2)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Pairs) != 1 || m2.Pairs[0].Negative != 5 || m2.FallbackPairs != 0 {
		t.Errorf("a full-key match must win: pairs=%+v fallback=%d", m2.Pairs, m2.FallbackPairs)
	}
	if strings.Join(m2.Pairs[0].MatchKeyUsed, ",") != "category,subsystem" {
		t.Errorf("match_key_used = %v", m2.Pairs[0].MatchKeyUsed)
	}
}

// TestFallbackCannotInventANegative: a subsystem with no negative at all leaves the
// positive unmatched, and an explicit list says why.
func TestFallbackCannotInventANegative(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bfdd", 20, 0.9, 1, 0, 0, 400), // no bfdd negative exists
		cohortRecord(2, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(3, "bgpd", 20, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "none")
	opt.MatchKey = []string{"category", "subsystem"}
	opt.FallbackSubsystem = true
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Positive != 2 {
		t.Fatalf("pairs = %+v", m.Pairs)
	}
	if len(m.UnmatchedPositives) != 1 || m.UnmatchedPositives[0] != 1 || m.FallbackPairs != 0 {
		t.Errorf("the bfdd positive must stay unmatched: %v fallback=%d", m.UnmatchedPositives, m.FallbackPairs)
	}
	explicit := options(t, "explicit")
	explicit.MatchKey = []string{"category", "subsystem"}
	explicit.FallbackSubsystem = true
	explicit.Positives = []int{1}
	if _, err := export(records, nil, explicit); err == nil || !strings.Contains(err.Error(), "holds no zero-signal PR at all") {
		t.Errorf("an explicit unmatchable positive must fail closed and say why: %v", err)
	}
}

// TestExplicitNegativesAreTheWholePool: when the evaluator names the negatives, the
// sampler chooses among exactly those and every one of them must be used.
func TestExplicitNegativesAreTheWholePool(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400), // the sampler would prefer this
		cohortRecord(3, "bgpd", 25, 0.7, 0, 0, 0, 400), // the evaluator picks this
	}
	sampled := options(t, "sampled")
	m0, err := export(records, nil, sampled)
	if err != nil {
		t.Fatal(err)
	}
	if m0.Pairs[0].Negative != 2 {
		t.Fatalf("precondition: the sampler prefers PR 2, got %d", m0.Pairs[0].Negative)
	}

	opt := options(t, "explicit")
	opt.Negatives = []int{3}
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Negative != 3 {
		t.Fatalf("the evaluator's pick must win over the sampler's: %+v", m.Pairs)
	}
	if len(m.Filters.Negatives) != 1 || m.Filters.Negatives[0] != 3 {
		t.Errorf("the explicit list must be recorded: %v", m.Filters.Negatives)
	}
	if m.Exclusions.NotInNegativeList != 1 {
		t.Errorf("the unpicked negative must be counted, not silently dropped: %+v", m.Exclusions)
	}
}

func TestExplicitNegativesFailClosed(t *testing.T) {
	base := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),  // positive
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400),  // clean negative
		cohortRecord(3, "bgpd", 20, 0.7, 0, 0, 1, 400),  // weak signal: not clean
		cohortRecord(4, "zebra", 20, 0.7, 0, 0, 0, 400), // clean, other subsystem
	}
	uncounted := cohortRecord(5, "bgpd", 20, 0.7, 0, 0, 0, 400)
	uncounted.Provenance.CorrelatedBy = ""
	uninspected := cohortRecord(6, "bgpd", 20, 0.7, 0, 0, 0, 400)
	uninspected.Retrospective.UninspectedCommitCount = 2
	records := append(append([]model.PRCandidateRecord{}, base...), uncounted, uninspected)

	for _, c := range []struct {
		name string
		negs []int
		want string
	}{
		{"names a positive", []int{1, 2}, "carries corrective evidence"},
		{"names a weak-signal record", []int{3}, "weak corrective evidence"},
		{"names an uncounted record", []int{5}, "no provenance.correlated_by"},
		{"names an uninspected record", []int{6}, "could not be inspected"},
		{"names an absent PR", []int{99}, "not in the input"},
		{"leaves one unpaired", []int{2, 4}, "left unpaired"},
	} {
		opt := options(t, strings.ReplaceAll(c.name, " ", "-"))
		opt.Negatives = c.negs
		_, err := export(records, nil, opt)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
		if _, statErr := os.Lstat(opt.Out); !os.IsNotExist(statErr) {
			t.Errorf("%s: output written despite the refusal", c.name)
		}
	}
}

// TestExplicitNegativesCannotHideAFullKeyMatch: a list that omits the one negative
// sharing the positive's full key must not silently produce a fallback pair.
func TestExplicitNegativesCannotHideAFullKeyMatch(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.9, 1, 0, 0, 400), // HIGH
		cohortRecord(2, "bgpd", 20, 0.9, 0, 0, 0, 400), // HIGH: the full-key match
		cohortRecord(3, "bgpd", 20, 0.4, 0, 0, 0, 400), // MEDIUM
	}
	opt := options(t, "hidden")
	opt.MatchKey = []string{"category", "subsystem"}
	opt.FallbackSubsystem = true
	opt.Negatives = []int{3}
	_, err := export(records, nil, opt)
	if err == nil || !strings.Contains(err.Error(), "left out of the negatives list") {
		t.Errorf("err = %v, want a refusal naming the omitted full-key negative", err)
	}
	// Listing the full-key negative pairs normally, with no fallback.
	ok := options(t, "listed")
	ok.MatchKey = []string{"category", "subsystem"}
	ok.FallbackSubsystem = true
	ok.Negatives = []int{2}
	m, err := export(records, nil, ok)
	if err != nil {
		t.Fatal(err)
	}
	if m.FallbackPairs != 0 || m.Pairs[0].Negative != 2 {
		t.Errorf("pairs = %+v fallback = %d", m.Pairs, m.FallbackPairs)
	}
}

// TestVetoedNegativesAreRecordedNotJustDropped: a vetoed record never enters the
// pool, the manifest says which were vetoed, and a PR in both lists is a
// contradiction rather than a precedence puzzle.
func TestVetoedNegativesAreRecordedNotJustDropped(t *testing.T) {
	records := []model.PRCandidateRecord{
		cohortRecord(1, "bgpd", 20, 0.7, 1, 0, 0, 400),
		cohortRecord(2, "bgpd", 20, 0.7, 0, 0, 0, 400), // the sampler's preference
		cohortRecord(3, "bgpd", 25, 0.7, 0, 0, 0, 400),
	}
	opt := options(t, "veto")
	opt.ExcludedNegatives = []int{2}
	m, err := export(records, nil, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pairs) != 1 || m.Pairs[0].Negative != 3 {
		t.Fatalf("the vetoed negative must not be used: %+v", m.Pairs)
	}
	if m.Exclusions.VetoedNegative != 1 {
		t.Errorf("the veto must be counted: %+v", m.Exclusions)
	}
	if len(m.Filters.ExcludedNegatives) != 1 || m.Filters.ExcludedNegatives[0] != 2 {
		t.Errorf("the veto list must be recorded: %v", m.Filters.ExcludedNegatives)
	}
	both := options(t, "both")
	both.Negatives = []int{2}
	both.ExcludedNegatives = []int{2}
	if _, err := export(records, nil, both); err == nil || !strings.Contains(err.Error(), "both the negatives list and the veto list") {
		t.Errorf("a PR in both lists must fail: %v", err)
	}
}
