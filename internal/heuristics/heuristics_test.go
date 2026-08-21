package heuristics

import (
	"path/filepath"
	"testing"
	"time"

	"pr-analysis/internal/config"
	"pr-analysis/internal/model"
)

func TestEvaluateStateful(t *testing.T) {
	cfgPath := filepath.Join("..", "..", "configs", "frr_heuristics.yaml")
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	engine, err := NewEngine(&cfg.Heuristics)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Case 1: High stateful candidate (bgp fsm)
	pr1 := &model.OriginalPR{
		Number:   14502,
		Title:    "bgpd: fix fsm transition on neighbor reset",
		Body:     "Handles graceful restart timer reset during session tear down.",
		MergedAt: time.Now(),
		ChangedFiles: []model.ChangedFile{
			{Path: "bgpd/bgp_fsm.c", Status: "modified", Additions: 20, Deletions: 5},
		},
		ChangedFunctions: []string{"bgp_fsm_change_status", "bgp_timer_set"},
	}

	eval1 := engine.Evaluate(pr1)
	if eval1.Category != "HIGH" {
		t.Errorf("expected HIGH category for PR 1, got %s (score: %f)", eval1.Category, eval1.Score)
	}
	if eval1.Score < 0.60 {
		t.Errorf("expected score >= 0.60, got %f", eval1.Score)
	}
	if len(eval1.MatchedSymbols) == 0 {
		t.Errorf("expected matched symbols for PR 1")
	}

	// Case 2: Low stateful candidate (docs or build script)
	pr2 := &model.OriginalPR{
		Number:   14503,
		Title:    "doc: fix typo in user guide",
		Body:     "Correct spelling in bgp overview chapter.",
		MergedAt: time.Now(),
		ChangedFiles: []model.ChangedFile{
			{Path: "doc/user/bgp.rst", Status: "modified", Additions: 1, Deletions: 1},
		},
	}

	eval2 := engine.Evaluate(pr2)
	if eval2.Category != "LOW" {
		t.Errorf("expected LOW category for PR 2, got %s (score: %f)", eval2.Category, eval2.Score)
	}
}
