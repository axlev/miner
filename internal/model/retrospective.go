package model

import "time"

// EvidenceScope categorizes the file paths modified by a later commit/PR.
type EvidenceScope string

const (
	ScopeCode      EvidenceScope = "code"
	ScopeTest      EvidenceScope = "test"
	ScopeDocs      EvidenceScope = "docs"
	ScopePackaging EvidenceScope = "packaging"
	ScopeMixed     EvidenceScope = "mixed"
	ScopeUnknown   EvidenceScope = "unknown"
)

// RetrospectiveEvidence captures post-merge signals (T > T0) and retrospective code relationships.
type RetrospectiveEvidence struct {
	SummaryRankScore    float64                      `json:"summary_rank_score"`
	StrongSignals       []StrongCorrectiveSignal     `json:"strong_signals"`
	MediumSignals       []MediumCorrectiveSignal     `json:"medium_signals"`
	WeakSignals         []WeakCorrectiveSignal       `json:"weak_signals"`
	CommitRelationships []CommitRelationshipEvidence `json:"commit_relationships,omitempty"`
	// UninspectedCommitCount is the number of post-merge commits in this record's
	// window that carried fix vocabulary but whose diff could not be inspected (a
	// git subprocess failed or timed out), so no medium or weak signal could have
	// been derived from them. A non-zero count means "no signals" is not "no
	// evidence". Only trustworthy when provenance.correlated_by is set.
	UninspectedCommitCount int `json:"uninspected_commit_count"`
	// UninspectedCommitSHAs lists the first MaxUninspectedCommitSHAs of those commits.
	UninspectedCommitSHAs []string `json:"uninspected_commit_shas,omitempty"`
}

// MaxUninspectedCommitSHAs bounds UninspectedCommitSHAs; the count is exact regardless.
const MaxUninspectedCommitSHAs = 20

// LineageEvidence captures whether removed pre-fix lines originated in the original PR.
type LineageEvidence struct {
	LaterRemovedLineCount     int      `json:"later_removed_line_count"`
	AnalyzedRemovedLineCount  int      `json:"analyzed_removed_line_count"`
	LinesBlamedToOriginalPR   int      `json:"lines_blamed_to_original_pr"`
	DirectLineageFraction     float64  `json:"direct_lineage_fraction"`
	MatchedOriginalCommitSHAs []string `json:"matched_original_commit_shas,omitempty"`
	Method                    string   `json:"method"` // "git_blame_range_porcelain"
}

// PathReversalOverlap records path-level inverse content overlap metrics.
type PathReversalOverlap struct {
	Path                                  string `json:"path"`
	OriginalAddedLines                    int    `json:"original_added_lines"`
	OriginalRemovedLines                  int    `json:"original_removed_lines"`
	LaterRemovedMatchingOriginalAdditions int    `json:"later_removed_matching_original_additions"`
	LaterAddedMatchingOriginalRemovals    int    `json:"later_added_matching_original_removals"`
	LineageSupportedRemovedOverlapCount   int    `json:"lineage_supported_removed_overlap_count"`
}

// ReversalEvidence captures whether a later patch reverses additions/removals made by the original PR.
type ReversalEvidence struct {
	FullReversePatchMatch                      bool                  `json:"full_reverse_patch_match"`
	ReversalType                               string                `json:"reversal_type"` // "none" | "partial_candidate" | "full"
	OriginalAddedLines                         int                   `json:"original_added_lines"`
	OriginalRemovedLines                       int                   `json:"original_removed_lines"`
	TotalOriginalSubstantiveLines              int                   `json:"total_original_substantive_lines"`
	LaterRemovedLinesMatchingOriginalAdditions int                   `json:"later_removed_lines_matching_original_additions"`
	LaterAddedLinesMatchingOriginalRemovals    int                   `json:"later_added_lines_matching_original_removals"`
	LineageSupportedRemovedOverlapCount        int                   `json:"lineage_supported_removed_overlap_count"`
	ReversedLineOverlapCount                   int                   `json:"reversed_line_overlap_count"`
	ReversedLineOverlapFraction                float64               `json:"reversed_line_overlap_fraction"`
	PathOverlaps                               []PathReversalOverlap `json:"path_overlaps,omitempty"`
	Method                                     string                `json:"method"` // "path_scoped_substantive_line_multiset"
}

// CommitRelationshipEvidence captures retrospective code relationship facts for a candidate later commit.
type CommitRelationshipEvidence struct {
	SourceRef      string            `json:"source_ref"`
	AnalysisStatus string            `json:"analysis_status"` // "complete" | "partial" | "skipped_merge" | "skipped_root" | "unavailable" | "error"
	AnalysisNote   string            `json:"analysis_note,omitempty"`
	Lineage        *LineageEvidence  `json:"lineage,omitempty"`
	Reversal       *ReversalEvidence `json:"reversal,omitempty"`
}

// StrongCorrectiveSignal represents unambiguous deterministic cross-references.
type StrongCorrectiveSignal struct {
	SignalType     string        `json:"signal_type"` // "FIXES_SHA", "FIXES_PR", "EXPLICIT_REVERT", "REGRESSION_MENTION"
	SourceType     string        `json:"source_type"` // "COMMIT", "PR", "ISSUE"
	SourceRef      string        `json:"source_ref"`  // Commit SHA, PR number, or Issue number
	Timestamp      time.Time     `json:"timestamp"`
	RawSnippet     string        `json:"raw_snippet"` // Exact matched regex line / text
	Confidence     float64       `json:"confidence"`  // e.g. 1.0
	EvidenceScope  EvidenceScope `json:"evidence_scope,omitempty"`
	LogicalPatchID string        `json:"logical_patch_id,omitempty"`
	ChangedPaths   []string      `json:"changed_paths,omitempty"`
}

// MediumCorrectiveSignal represents probable corrective linkage.
type MediumCorrectiveSignal struct {
	SignalType     string        `json:"signal_type"` // "SAME_FUNCTION_FIX", "LINKED_ISSUE_FIX", "REGRESSION_TEST_ADDED"
	SourceType     string        `json:"source_type"` // "COMMIT", "PR", "ISSUE"
	SourceRef      string        `json:"source_ref"`
	Timestamp      time.Time     `json:"timestamp"`
	FunctionOrPath string        `json:"function_or_path"`
	Context        string        `json:"context"` // Commit message snippet or reason
	EvidenceScope  EvidenceScope `json:"evidence_scope,omitempty"`
	LogicalPatchID string        `json:"logical_patch_id,omitempty"`
	ChangedPaths   []string      `json:"changed_paths,omitempty"`
}

// WeakCorrectiveSignal represents loose candidate signals.
type WeakCorrectiveSignal struct {
	SignalType    string        `json:"signal_type"` // "SAME_FILE_MODIFICATION", "SAME_SYMBOL_DIFFERENT_PATH"
	SourceRef     string        `json:"source_ref"`
	Timestamp     time.Time     `json:"timestamp"`
	FilePath      string        `json:"file_path"`
	Context       string        `json:"context"`
	EvidenceScope EvidenceScope `json:"evidence_scope,omitempty"`
}
