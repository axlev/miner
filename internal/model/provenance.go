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
}
