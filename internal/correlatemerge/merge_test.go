package correlatemerge

import (
	"strings"
	"testing"
	"time"

	"miner/internal/model"
)

var obsEnd = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func rec(pr int, strong, medium, weak, uninspected int, correlatedBy string) model.PRCandidateRecord {
	r := model.PRCandidateRecord{SchemaVersion: "1.0.0"}
	r.Original.Number = pr
	r.Original.Repository = "owner/repo"
	r.Original.Title = "t"
	r.Original.MergedAt = obsEnd.Add(-300 * 24 * time.Hour)
	for i := 0; i < strong; i++ {
		r.Retrospective.StrongSignals = append(r.Retrospective.StrongSignals, model.StrongCorrectiveSignal{SignalType: "FIXES_PR"})
	}
	for i := 0; i < medium; i++ {
		r.Retrospective.MediumSignals = append(r.Retrospective.MediumSignals, model.MediumCorrectiveSignal{SignalType: "SAME_FUNCTION_FIX"})
	}
	for i := 0; i < weak; i++ {
		r.Retrospective.WeakSignals = append(r.Retrospective.WeakSignals, model.WeakCorrectiveSignal{SignalType: "SAME_FILE_MODIFICATION"})
	}
	r.Retrospective.UninspectedCommitCount = uninspected
	r.Provenance = model.Provenance{MinerVersion: "collect1", ConfigHash: "cfg", HarvestedAt: obsEnd, ObservationEnd: obsEnd, TargetRepoHeadSHA: "head", CorrelatedBy: correlatedBy}
	return r
}

func TestMergeReplacesAndReportsFlips(t *testing.T) {
	base := []model.PRCandidateRecord{
		rec(3, 1, 0, 0, 0, ""), // untouched positive
		rec(1, 0, 0, 0, 0, ""), // zero-signal, will gain weak
		rec(2, 0, 0, 0, 0, ""), // zero-signal, stays zero but is now counted
		rec(4, 0, 0, 1, 0, ""), // weak, re-correlated to medium
	}
	subset := []model.PRCandidateRecord{
		rec(4, 0, 1, 0, 0, "b2"),
		rec(1, 0, 0, 1, 0, "b2"),
		rec(2, 0, 0, 0, 2, "b2"),
	}
	merged, report, err := Merge(base, subset)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 || merged[0].Original.Number != 1 || merged[3].Original.Number != 4 {
		t.Fatalf("merged order/coverage wrong: %d records", len(merged))
	}
	if merged[0].Provenance.CorrelatedBy != "b2" || merged[2].Provenance.CorrelatedBy != "" {
		t.Errorf("replacement wrong: PR1 correlated_by=%q, PR3 correlated_by=%q", merged[0].Provenance.CorrelatedBy, merged[2].Provenance.CorrelatedBy)
	}
	if report.SubsetCount != 3 || report.Flipped != 2 || report.Unchanged != 1 || report.FromNone != 1 || report.SubsetNoneBefore != 2 || report.UninspectedAfter != 2 || report.CorrelatedBy != "b2" {
		t.Errorf("report = %+v", report)
	}
	if len(report.Flips) != 2 || report.Flips[0].PR != 1 || report.Flips[0].From != "none" || report.Flips[0].To != "weak" || report.Flips[1].PR != 4 || report.Flips[1].To != "medium" {
		t.Errorf("flips = %+v", report.Flips)
	}
	if len(report.Transitions) != 2 || report.Transitions[0] != (Transition{"none", "weak", 1}) || report.Transitions[1] != (Transition{"weak", "medium", 1}) {
		t.Errorf("transitions = %+v", report.Transitions)
	}
}

func TestMergeFailsClosed(t *testing.T) {
	base := []model.PRCandidateRecord{rec(1, 0, 0, 0, 0, ""), rec(2, 1, 0, 0, 0, "")}
	cases := map[string]struct {
		subset  []model.PRCandidateRecord
		wantErr string
	}{
		"not in base": {[]model.PRCandidateRecord{rec(9, 0, 0, 0, 0, "b2")}, "not in the base file"},
		"uncounted":   {[]model.PRCandidateRecord{rec(1, 0, 0, 0, 0, "")}, "no provenance.correlated_by"},
		"two builds":  {[]model.PRCandidateRecord{rec(1, 0, 0, 0, 0, "b2"), rec(2, 1, 0, 0, 0, "b3")}, "one build per merge"},
		"duplicate":   {[]model.PRCandidateRecord{rec(1, 0, 0, 0, 0, "b2"), rec(1, 0, 0, 0, 0, "b2")}, "more than once"},
		"other original": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Original.Title = "edited"
			return []model.PRCandidateRecord{r}
		}(), "original change differs"},
		"other obs end": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Provenance.ObservationEnd = obsEnd.Add(time.Hour)
			return []model.PRCandidateRecord{r}
		}(), "observation_end"},
		"other config": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Provenance.ConfigHash = "x"
			return []model.PRCandidateRecord{r}
		}(), "config_hash"},
		"other head": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Provenance.TargetRepoHeadSHA = "x"
			return []model.PRCandidateRecord{r}
		}(), "target_repo_head_sha"},
		"other collect": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Provenance.MinerVersion = "x"
			return []model.PRCandidateRecord{r}
		}(), "miner_version"},
		"other harvest": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Provenance.HarvestedAt = obsEnd.Add(time.Hour)
			return []model.PRCandidateRecord{r}
		}(), "harvested_at"},
		"other repository": {func() []model.PRCandidateRecord {
			r := rec(1, 0, 0, 0, 0, "b2")
			r.Original.Repository = "o/x"
			return []model.PRCandidateRecord{r}
		}(), "repository"},
	}
	for name, c := range cases {
		_, _, err := Merge(base, c.subset)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want containing %q", name, err, c.wantErr)
		}
	}
	if _, _, err := Merge([]model.PRCandidateRecord{rec(1, 0, 0, 0, 0, ""), rec(1, 0, 0, 0, 0, "")}, []model.PRCandidateRecord{rec(1, 0, 0, 0, 0, "b2")}); err == nil || !strings.Contains(err.Error(), "base contains") {
		t.Errorf("duplicate base: err = %v", err)
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	base := []model.PRCandidateRecord{rec(1, 0, 0, 0, 0, "")}
	subset := []model.PRCandidateRecord{rec(1, 0, 0, 1, 0, "b2")}
	if _, _, err := Merge(base, subset); err != nil {
		t.Fatal(err)
	}
	if base[0].Provenance.CorrelatedBy != "" || len(base[0].Retrospective.WeakSignals) != 0 {
		t.Errorf("base was mutated: %+v", base[0])
	}
}
