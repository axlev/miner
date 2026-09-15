package model

import "time"

// Provenance guarantees auditability and exact reproduction.
type Provenance struct {
	MinerVersion      string    `json:"miner_version"`        // Go build Git commit / version
	ConfigHash        string    `json:"config_hash"`          // SHA256 of the YAML configuration used
	HarvestedAt       time.Time `json:"harvested_at"`         // Timestamp when raw data was gathered
	ObservationEnd    time.Time `json:"observation_end"`      // Strict cut-off date for retrospective evidence
	TargetRepoHeadSHA string    `json:"target_repo_head_sha"` // HEAD SHA of the target repository
	GitHubAPIVersion  string    `json:"github_api_version"`   // GitHub API version header (e.g. 2022-11-28)
	// CorrelatedBy is the build (buildinfo.MinerVersion) that last correlated this
	// record. Empty means the record was correlated before builds recorded it, so
	// its uninspected-commit count is absent rather than zero.
	CorrelatedBy string `json:"correlated_by,omitempty"`
}
