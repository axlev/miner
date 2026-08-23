package correlator

import "time"

const (
	// MaxRemovedLinesPerCommit caps the maximum removed lines analyzed for direct lineage per later commit.
	MaxRemovedLinesPerCommit = 500

	// MaxChangedPathsPerCommit caps the maximum changed paths analyzed for blame per later commit.
	MaxChangedPathsPerCommit = 20

	// MaxBlameRangesPerPath caps the maximum distinct contiguous ranges blamed per file path.
	MaxBlameRangesPerPath = 20

	// GitCommandTimeout is the per-command timeout for blame and diff-tree subprocesses.
	GitCommandTimeout = 15 * time.Second
)
