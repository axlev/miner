package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPRCandidateRecordSerialization(t *testing.T) {
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	later := time.Date(2024, 7, 1, 10, 0, 0, 0, time.UTC)

	record := PRCandidateRecord{
		SchemaVersion: SchemaVersion,
		Original: OriginalPR{
			Repository:     "FRRouting/frr",
			Number:         14502,
			Title:          "bgpd: fix fsm transition on neighbor reset",
			Body:           "This patch updates bgp_fsm.c to handle graceful restart correctly.",
			Author:         "dev-user",
			CreatedAt:      now.Add(-24 * time.Hour),
			MergedAt:       now,
			BaseRef:        "master",
			BaseSHA:        "abcdef1234567890",
			HeadSHA:        "123456abcdef7890",
			MergeCommitSHA: "deadbeefcafebabe111122223333444455556666",
			Labels:         []string{"bgp", "bugfix"},
			ChangedFiles: []ChangedFile{
				{Path: "bgpd/bgp_fsm.c", Status: "modified", Additions: 15, Deletions: 4},
			},
			ChangedFunctions:  []string{"bgp_fsm_change_status", "bgp_stop"},
			PreMergeIssueRefs: []int{14500},
			CommitCount:       1,
		},
		Stateful: StatefulEvaluation{
			Score:     0.85,
			RawPoints: 1.20,
			Category:  "HIGH",
			MatchedPathRules: []MatchedRule{
				{RuleName: "core_fsm_daemon", Pattern: "(bgpd|bfdd)/.*", Weight: 0.35, Location: "path", Matched: "bgpd/bgp_fsm.c"},
			},
			MatchedKeywordRules: []MatchedRule{
				{RuleName: "fsm_transition", Pattern: "fsm", Weight: 0.30, Location: "title", Matched: "fsm"},
			},
			MatchedSymbols: []string{"bgp_fsm_change_status"},
			Explanation:    "Matched daemon bgpd (0.35) and keyword fsm (0.30)",
		},
		Retrospective: RetrospectiveEvidence{
			SummaryRankScore: 1.0,
			StrongSignals: []StrongCorrectiveSignal{
				{
					SignalType: "FIXES_SHA",
					SourceType: "COMMIT",
					SourceRef:  "fadecafe99998888777766665555444433332222",
					Timestamp:  later,
					RawSnippet: "Fixes: deadbeefcafebabe (\"bgpd: fix fsm transition on neighbor reset\")",
					Confidence: 1.0,
				},
			},
		},
		Provenance: Provenance{
			MinerVersion:      "v0.1.0-abc1234",
			ConfigHash:        "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			HarvestedAt:       now,
			ObservationEnd:    later.Add(30 * 24 * time.Hour),
			TargetRepoHeadSHA: "9999888877776666555544443333222211110000",
			GitHubAPIVersion:  "2022-11-28",
		},
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("failed to marshal PRCandidateRecord: %v", err)
	}

	var decoded PRCandidateRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal PRCandidateRecord: %v", err)
	}

	if decoded.Original.Number != 14502 {
		t.Errorf("expected PR number 14502, got %d", decoded.Original.Number)
	}

	if !decoded.HasStrongFixSignal() {
		t.Errorf("expected HasStrongFixSignal() to be true")
	}

	if !decoded.HasAnyFixSignal() {
		t.Errorf("expected HasAnyFixSignal() to be true")
	}
}
