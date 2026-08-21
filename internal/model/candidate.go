package model

// SchemaVersion defines the current schema version for machine-readable exports.
const SchemaVersion = "1.0.0"

// PRCandidateRecord represents a single mined PR with pre-merge facts,
// heuristic assessment, retrospective evidence, and complete provenance.
type PRCandidateRecord struct {
	SchemaVersion string                `json:"schema_version"`
	Original      OriginalPR            `json:"original"`
	Stateful      StatefulEvaluation    `json:"stateful"`
	Retrospective RetrospectiveEvidence `json:"retrospective"`
	Provenance    Provenance            `json:"provenance"`
}

// HasStrongFixSignal returns true if the candidate has at least one strong retrospective fix signal.
func (r *PRCandidateRecord) HasStrongFixSignal() bool {
	return len(r.Retrospective.StrongSignals) > 0
}

// HasAnyFixSignal returns true if strong or medium retrospective signals exist.
func (r *PRCandidateRecord) HasAnyFixSignal() bool {
	return len(r.Retrospective.StrongSignals) > 0 || len(r.Retrospective.MediumSignals) > 0
}
