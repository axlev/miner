package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete YAML configuration file.
type Config struct {
	Mining     MiningConfig     `yaml:"mining"`
	Heuristics HeuristicsConfig `yaml:"heuristics"`
	ConfigHash string           `yaml:"-"` // Calculated SHA256 of the YAML file
}

// MiningConfig contains mining timeframes, repository info, and directories.
type MiningConfig struct {
	Repository     string     `yaml:"repository"`
	From           *time.Time `yaml:"from,omitempty"`
	To             *time.Time `yaml:"to,omitempty"`
	ObservationEnd *time.Time `yaml:"observation_end,omitempty"`
	CacheDir       string     `yaml:"cache_dir"`
	GitDir         string     `yaml:"git_dir"`
	Token          string     `yaml:"token,omitempty"`
}

// HeuristicsConfig defines rules and weights for stateful/distributed scoring.
type HeuristicsConfig struct {
	Version      string        `yaml:"version"`
	Repository   string        `yaml:"repository"`
	PathRules    []PathRule    `yaml:"path_rules"`
	KeywordRules []KeywordRule `yaml:"keyword_rules"`
	Scoring      ScoringConfig `yaml:"scoring"`
}

// PathRule defines a subsystem/path weight.
type PathRule struct {
	Name    string  `yaml:"name"`
	Pattern string  `yaml:"pattern"`
	Weight  float64 `yaml:"weight"`
}

// KeywordRule defines a keyword match in symbols, commit messages, or PR text.
type KeywordRule struct {
	Name        string   `yaml:"name"`
	Pattern     string   `yaml:"pattern"`
	Weight      float64  `yaml:"weight"`
	MatchFields []string `yaml:"match_fields"` // "diff_symbols", "commit_messages", "pr_body", "pr_title"
}

// ScoringConfig defines scoring thresholds.
type ScoringConfig struct {
	HighThreshold   float64 `yaml:"high_threshold"`
	MediumThreshold float64 `yaml:"medium_threshold"`
	MaxCap          float64 `yaml:"max_cap"`
}

// LoadConfig reads a YAML config file, computes its SHA256 hash, and unmarshals it.
func LoadConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	hash := sha256.Sum256(data)
	cfg := &Config{
		ConfigHash: hex.EncodeToString(hash[:]),
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config %q: %w", filePath, err)
	}

	// Apply default scoring caps if not specified
	if cfg.Heuristics.Scoring.MaxCap == 0 {
		cfg.Heuristics.Scoring.MaxCap = 1.0
	}
	if cfg.Heuristics.Scoring.HighThreshold == 0 {
		cfg.Heuristics.Scoring.HighThreshold = 0.60
	}
	if cfg.Heuristics.Scoring.MediumThreshold == 0 {
		cfg.Heuristics.Scoring.MediumThreshold = 0.30
	}

	return cfg, nil
}
