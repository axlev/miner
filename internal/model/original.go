package model

import "time"

// OriginalPR captures immutable facts established at or before merge time T0.
// No post-merge data should ever be added to this struct to prevent temporal data leakage.
type OriginalPR struct {
	Repository        string        `json:"repository"`
	Number            int           `json:"number"`
	Title             string        `json:"title"`
	Body              string        `json:"body"`
	Author            string        `json:"author"`
	CreatedAt         time.Time     `json:"created_at"`
	MergedAt          time.Time     `json:"merged_at"` // T0 boundary
	BaseRef           string        `json:"base_ref"`
	BaseSHA           string        `json:"base_sha"`
	HeadSHA           string        `json:"head_sha"`
	MergeCommitSHA    string        `json:"merge_commit_sha"`
	CommitSHAs        []string      `json:"commit_shas,omitempty"`
	Labels            []string      `json:"labels"`
	ChangedFiles      []ChangedFile `json:"changed_files"`
	ChangedFunctions  []string      `json:"changed_functions"`    // Enclosing C functions modified in diff
	PreMergeIssueRefs []int         `json:"pre_merge_issue_refs"` // Issues referenced in PR description
	CommitCount       int           `json:"commit_count"`
	CommitMessages    []string      `json:"commit_messages,omitempty"`
}

// ChangedFile captures file-level diff statistics for an original PR.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // added, modified, deleted, renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}
