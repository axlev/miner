// Package correlatemerge splices a re-correlated subset of records back into the
// full correlated set and reports what changed.
//
// It exists because batch correlation is keyed to one input file: re-correlating
// only the records that need it (the zero-signal pool, and positives whose fix
// diff failed) produces a second file that has to be merged by PR number before
// scoring. The merge is fail-closed on identity — same repository, same original
// change, same observation end, configuration and repository state — and the flip
// report it writes is the measured rate at which "no signals" turned out to be
// "diffs failed", which the H1 pre-registration carries as a stated number.
package correlatemerge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"miner/internal/model"
)

// ReportSchemaVersion identifies the flip report.
const ReportSchemaVersion = "flip-report/v1"

// Class is the strongest evidence tier a record carries.
func Class(rec *model.PRCandidateRecord) string {
	r := &rec.Retrospective
	switch {
	case len(r.StrongSignals) > 0:
		return "strong"
	case len(r.MediumSignals) > 0:
		return "medium"
	case len(r.WeakSignals) > 0:
		return "weak"
	default:
		return "none"
	}
}

// Flip is one record whose class changed.
type Flip struct {
	PR                int    `json:"pr"`
	From              string `json:"from"`
	To                string `json:"to"`
	UninspectedBefore int    `json:"uninspected_before"`
	UninspectedAfter  int    `json:"uninspected_after"`
}

// Transition counts one from→to class pair across the subset.
type Transition struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// Report is the flip report written beside the merged file.
type Report struct {
	SchemaVersion string `json:"schema_version"`
	BaseCount     int    `json:"base_count"`
	SubsetCount   int    `json:"subset_count"`
	// CorrelatedBy is the single build that produced every subset record.
	CorrelatedBy string `json:"correlated_by"`
	Unchanged    int    `json:"unchanged"`
	Flipped      int    `json:"flipped"`
	// FromNone is the number of subset records that were zero-signal before and
	// carry some signal after: the timeout-artifact rate's numerator.
	FromNone int `json:"from_none"`
	// SubsetNoneBefore is that rate's denominator.
	SubsetNoneBefore int `json:"subset_none_before"`
	// UninspectedAfter is the sum of per-record counts over the subset after merge.
	UninspectedAfter int          `json:"uninspected_after"`
	Transitions      []Transition `json:"transitions"`
	Flips            []Flip       `json:"flips"`
}

// Merge replaces base records by PR number with their subset counterparts. Every
// subset record must exist in base, describe the same original change, share the
// base record's provenance identity, and have been correlated by one counting build.
// The returned records are sorted by PR number.
func Merge(base, subset []model.PRCandidateRecord) ([]model.PRCandidateRecord, *Report, error) {
	index := map[int]int{}
	for i := range base {
		pr := base[i].Original.Number
		if _, dup := index[pr]; dup {
			return nil, nil, fmt.Errorf("base contains PR #%d more than once", pr)
		}
		index[pr] = i
	}
	report := &Report{SchemaVersion: ReportSchemaVersion, BaseCount: len(base), SubsetCount: len(subset), Transitions: []Transition{}, Flips: []Flip{}}
	merged := make([]model.PRCandidateRecord, len(base))
	copy(merged, base)
	seen := map[int]bool{}
	transitions := map[[2]string]int{}
	for i := range subset {
		sub := &subset[i]
		pr := sub.Original.Number
		if seen[pr] {
			return nil, nil, fmt.Errorf("subset contains PR #%d more than once", pr)
		}
		seen[pr] = true
		bi, ok := index[pr]
		if !ok {
			return nil, nil, fmt.Errorf("subset PR #%d is not in the base file; a merge only replaces, it never adds", pr)
		}
		old := &base[bi]
		if err := sameIdentity(old, sub); err != nil {
			return nil, nil, fmt.Errorf("PR #%d: %w", pr, err)
		}
		if sub.Provenance.CorrelatedBy == "" {
			return nil, nil, fmt.Errorf("PR #%d: subset record has no provenance.correlated_by; it was not produced by a counting build", pr)
		}
		if report.CorrelatedBy == "" {
			report.CorrelatedBy = sub.Provenance.CorrelatedBy
		} else if report.CorrelatedBy != sub.Provenance.CorrelatedBy {
			return nil, nil, fmt.Errorf("PR #%d was correlated by %q, earlier subset records by %q; one build per merge", pr, sub.Provenance.CorrelatedBy, report.CorrelatedBy)
		}

		from, to := Class(old), Class(sub)
		if from == "none" {
			report.SubsetNoneBefore++
		}
		if from != to {
			report.Flipped++
			if from == "none" {
				report.FromNone++
			}
			transitions[[2]string{from, to}]++
			report.Flips = append(report.Flips, Flip{PR: pr, From: from, To: to, UninspectedBefore: old.Retrospective.UninspectedCommitCount, UninspectedAfter: sub.Retrospective.UninspectedCommitCount})
		} else {
			report.Unchanged++
		}
		report.UninspectedAfter += sub.Retrospective.UninspectedCommitCount
		merged[bi] = *sub
	}
	for k, n := range transitions {
		report.Transitions = append(report.Transitions, Transition{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(report.Transitions, func(i, j int) bool {
		a, b := report.Transitions[i], report.Transitions[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	sort.Slice(report.Flips, func(i, j int) bool { return report.Flips[i].PR < report.Flips[j].PR })
	sort.Slice(merged, func(i, j int) bool { return merged[i].Original.Number < merged[j].Original.Number })
	return merged, report, nil
}

// sameIdentity fails closed unless the two records are re-correlations of the same
// change under the same run identity. The original block must be byte-identical
// once serialized; provenance must agree on everything a correlate run does not
// legitimately rewrite.
func sameIdentity(old, sub *model.PRCandidateRecord) error {
	if old.Original.Repository != sub.Original.Repository {
		return fmt.Errorf("repository %q != %q", sub.Original.Repository, old.Original.Repository)
	}
	oj, err := json.Marshal(old.Original)
	if err != nil {
		return err
	}
	sj, err := json.Marshal(sub.Original)
	if err != nil {
		return err
	}
	if !bytes.Equal(oj, sj) {
		return fmt.Errorf("original change differs from the base record; a merge only replaces retrospective evidence")
	}
	op, sp := old.Provenance, sub.Provenance
	switch {
	case !op.ObservationEnd.Equal(sp.ObservationEnd):
		return fmt.Errorf("observation_end %s != %s", sp.ObservationEnd, op.ObservationEnd)
	case op.ConfigHash != sp.ConfigHash:
		return fmt.Errorf("config_hash differs")
	case op.TargetRepoHeadSHA != sp.TargetRepoHeadSHA:
		return fmt.Errorf("target_repo_head_sha differs")
	case op.MinerVersion != sp.MinerVersion:
		return fmt.Errorf("miner_version (collect build) differs")
	case !op.HarvestedAt.Equal(sp.HarvestedAt):
		return fmt.Errorf("harvested_at differs; the subset was not derived from this base")
	}
	return nil
}
