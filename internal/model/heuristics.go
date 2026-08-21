package model

// StatefulEvaluation captures the pre-merge stateful/distributed heuristic assessment.
type StatefulEvaluation struct {
	Score               float64       `json:"score"`                 // Normalized score [0.0, 1.0]
	RawPoints           float64       `json:"raw_points"`            // Uncapped points total
	Category            string        `json:"category"`              // "HIGH", "MEDIUM", "LOW"
	MatchedPathRules    []MatchedRule `json:"matched_path_rules"`
	MatchedKeywordRules []MatchedRule `json:"matched_keyword_rules"`
	MatchedSymbols      []string      `json:"matched_symbols"`
	Explanation         string        `json:"explanation"`
}

// MatchedRule explains a specific rule triggering a score increment.
type MatchedRule struct {
	RuleName string  `json:"rule_name"`
	Pattern  string  `json:"pattern"`
	Weight   float64 `json:"weight"`
	Location string  `json:"location"` // "path", "symbol", "title", "body", "commit_msg"
	Matched  string  `json:"matched"`  // The specific token or path matched
}
