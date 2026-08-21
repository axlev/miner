package config

import (
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	configPath := filepath.Join("..", "..", "configs", "frr_heuristics.yaml")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config from %s: %v", configPath, err)
	}

	if cfg.Mining.Repository != "FRRouting/frr" {
		t.Errorf("expected repository FRRouting/frr, got %s", cfg.Mining.Repository)
	}

	if cfg.ConfigHash == "" {
		t.Errorf("expected non-empty config hash")
	}

	if len(cfg.Heuristics.PathRules) == 0 {
		t.Errorf("expected path rules to be populated")
	}

	if len(cfg.Heuristics.KeywordRules) == 0 {
		t.Errorf("expected keyword rules to be populated")
	}

	if cfg.Heuristics.Scoring.MaxCap != 1.0 {
		t.Errorf("expected max cap 1.0, got %f", cfg.Heuristics.Scoring.MaxCap)
	}
}
