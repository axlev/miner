package correlator

import (
	"context"
	"testing"
	"time"

	"pr-analysis/internal/gitx"
	"pr-analysis/internal/model"
)

func TestCorrelatePRSignals(t *testing.T) {
	t0 := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	obsEnd := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)

	orig := &model.OriginalPR{
		Number:         14000,
		Title:          "ospfd: optimize lsa flooding",
		MergedAt:       t0,
		MergeCommitSHA: "abcdef11223344556677889900aabbccddeeff00",
		ChangedFunctions: []string{"ospf_lsa_flood"},
		ChangedFiles: []model.ChangedFile{
			{Path: "ospfd/ospf_lsa.c", Status: "modified"},
		},
	}

	postCommits := []gitx.CommitLogEntry{
		{
			SHA:         "1111222233334444555566667777888899990000",
			Date:        t0.Add(5 * 24 * time.Hour),
			Subject:     "ospfd: fix memory leak in flooding",
			FullMessage: "ospfd: fix memory leak in flooding\n\nFixes: abcdef11223344 (\"ospfd: optimize lsa flooding\")\nReported by CI.",
		},
		{
			SHA:         "2222333344445555666677778888999900001111",
			Date:        t0.Add(15 * 24 * time.Hour),
			Subject:     "ospfd: fix regression in ospf_lsa_flood",
			FullMessage: "ospfd: fix regression in ospf_lsa_flood\n\nRegression introduced in #14000 when handling empty neighbors.",
		},
		{
			SHA:         "3333444455556666777788889999000011112222",
			Date:        t0.Add(-2 * 24 * time.Hour), // Before T0 (must be ignored)
			Subject:     "Fixes: abcdef11223344",
			FullMessage: "Fixes: abcdef11223344",
		},
	}

	correlator := NewCorrelator(nil)
	evidence := correlator.CorrelatePR(context.Background(), orig, postCommits, obsEnd)

	if len(evidence.StrongSignals) < 2 {
		t.Fatalf("expected at least 2 strong signals, got %d", len(evidence.StrongSignals))
	}

	hasFixesSHA := false
	hasRegression := false
	for _, s := range evidence.StrongSignals {
		if s.SignalType == "FIXES_SHA" {
			hasFixesSHA = true
		}
		if s.SignalType == "REGRESSION_MENTION" {
			hasRegression = true
		}
	}

	if !hasFixesSHA {
		t.Errorf("missing expected FIXES_SHA strong signal")
	}
	if !hasRegression {
		t.Errorf("missing expected REGRESSION_MENTION strong signal")
	}

	if evidence.SummaryRankScore != 1.0 {
		t.Errorf("expected summary rank score 1.0, got %f", evidence.SummaryRankScore)
	}
}
