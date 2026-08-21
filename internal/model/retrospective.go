package model

import "time"

// RetrospectiveEvidence captures post-merge signals (T > T0).
type RetrospectiveEvidence struct {
	SummaryRankScore float64                  `json:"summary_rank_score"`
	StrongSignals    []StrongCorrectiveSignal `json:"strong_signals"`
	MediumSignals    []MediumCorrectiveSignal `json:"medium_signals"`
	WeakSignals      []WeakCorrectiveSignal   `json:"weak_signals"`
}

// StrongCorrectiveSignal represents unambiguous deterministic cross-references.
type StrongCorrectiveSignal struct {
	SignalType string    `json:"signal_type"` // "FIXES_SHA", "FIXES_PR", "EXPLICIT_REVERT", "REGRESSION_MENTION"
	SourceType string    `json:"source_type"` // "COMMIT", "PR", "ISSUE"
	SourceRef  string    `json:"source_ref"`  // Commit SHA, PR number, or Issue number
	Timestamp  time.Time `json:"timestamp"`
	RawSnippet string    `json:"raw_snippet"` // Exact matched regex line / text
	Confidence float64   `json:"confidence"`  // e.g. 1.0
}

// MediumCorrectiveSignal represents probable corrective linkage.
type MediumCorrectiveSignal struct {
	SignalType     string    `json:"signal_type"` // "SAME_FUNCTION_FIX", "LINKED_ISSUE_FIX", "REGRESSION_TEST_ADDED"
	SourceType     string    `json:"source_type"` // "COMMIT", "PR", "ISSUE"
	SourceRef      string    `json:"source_ref"`
	Timestamp      time.Time `json:"timestamp"`
	FunctionOrPath string    `json:"function_or_path"`
	Context        string    `json:"context"` // Commit message snippet or reason
}

// WeakCorrectiveSignal represents loose candidate signals.
type WeakCorrectiveSignal struct {
	SignalType string    `json:"signal_type"` // "SAME_FILE_MODIFICATION"
	SourceRef  string    `json:"source_ref"`
	Timestamp  time.Time `json:"timestamp"`
	FilePath   string    `json:"file_path"`
	Context    string    `json:"context"`
}
