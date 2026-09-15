package prospectiveexport

import (
	"fmt"
	"time"

	"miner/internal/collector"
)

// MetadataSchemaVersionV2 pins the second reviewer-metadata contract. The wire
// shape is identical to v1 — same closed field list, same types — and only the
// admission rule for title and description differs: v2 admits a field when that
// field's own edit history proves its last edit was at or before the cutoff, and
// never consults the PR-level updated_at, which any post-merge comment or label
// advances. v1 stays selectable so the pilot bundles remain reproducible.
const MetadataSchemaVersionV2 = "reviewer-metadata/v2"

// Metadata version selectors accepted by Options.MetadataVersion.
const (
	MetadataVersionV1 = "v1"
	MetadataVersionV2 = "v2"
)

// Per-field admission outcomes recorded in the evaluator audit and, when
// cohort-export is given the cache, in the cohort manifest. Exactly one applies to
// each of title and description under v2.
const (
	OutcomeAdmitted             = "admitted"
	OutcomeOmittedPostCutoff    = "omitted-post-cutoff-edit"
	OutcomeOmittedUnverifiable  = "omitted-unverifiable"
	titleMethodCurrent          = "no rename after cutoff in complete issue-event history; current title is the cutoff title"
	titleMethodReconstructed    = "reverse complete post-cutoff rename chain"
	descriptionMethodNeverEdit  = "complete body edit history is empty; current body is the cutoff body"
	descriptionMethodLastEdit   = "complete body edit history; last edit at or before cutoff"
	provenanceSourceIssueEvents = "cached provider issue events (renamed)"
	provenanceSourceBodyEdits   = "cached provider body edit history (userContentEdits)"
)

// Admission methods, recorded beside the outcome so an arm's results can be
// stratified on whether an admitted title is the current text or a reconstruction,
// without opening the evaluator audit.
const (
	MethodCurrent       = "current"
	MethodReconstructed = "reconstructed"
)

// FieldAdmission is the per-field admission record carried by the audit and the
// cohort manifest: the outcome, and for an admitted field how its value was
// established.
type FieldAdmission struct {
	Outcome string `json:"outcome"`
	Method  string `json:"method,omitempty"`
}

// FieldOutcome is one field's admission result with the value it admits.
type FieldOutcome struct {
	Outcome string
	// MethodToken is MethodCurrent or MethodReconstructed when admitted.
	MethodToken string
	// Value is the admitted value; meaningful only when Outcome is OutcomeAdmitted.
	Value string
	// Method and Timestamps feed the audit's FieldDecision.
	Method     string
	Source     string
	Timestamps []string
	Reason     string
}

// AdmitTitle decides the title under v2. The REST "renamed" events carry every
// title change with its time, so a post-cutoff rename is not a reason to omit: the
// chain is reversed to the title as it stood at cutoff. Omission is only ever for
// lack of proof: incomplete events, a rename without a timestamp, a chain that does
// not connect to the current title, or a PR that did not yet exist at cutoff.
func AdmitTitle(b collector.RawPRBundle, cutoff time.Time) FieldOutcome {
	if b.PR == nil {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "cached pull request is missing"}
	}
	if created := b.PR.GetCreatedAt().Time; created.IsZero() || created.After(cutoff) {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "pull request creation time is missing or after cutoff"}
	}
	if !b.IssueEventsComplete {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "issue-event history is incomplete; renames after cutoff cannot be ruled out"}
	}
	title, times, ok := reconstructTitle(b.PR.GetTitle(), b.IssueEvents, true, cutoff)
	if !ok {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "rename history does not connect to the current title or a rename lacks a timestamp"}
	}
	if len(times) == 0 {
		return FieldOutcome{Outcome: OutcomeAdmitted, MethodToken: MethodCurrent, Value: title, Method: titleMethodCurrent, Source: provenanceSourceIssueEvents}
	}
	return FieldOutcome{Outcome: OutcomeAdmitted, MethodToken: MethodReconstructed, Value: title, Method: titleMethodReconstructed, Source: provenanceSourceIssueEvents, Timestamps: times}
}

// AdmitDescription decides the body under v2 from the bundle's body edit history.
// The body cannot be reconstructed, so a post-cutoff edit means omission; a history
// that is not complete as of the fetch — the bundle predates 1.1.0, the fetch
// failed, or an edit record was deleted — means the answer is unknown, which is
// recorded as unverifiable rather than guessed from updated_at.
func AdmitDescription(b collector.RawPRBundle, cutoff time.Time) FieldOutcome {
	if b.PR == nil {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "cached pull request is missing"}
	}
	if created := b.PR.GetCreatedAt().Time; created.IsZero() || created.After(cutoff) {
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "pull request creation time is missing or after cutoff"}
	}
	if !b.BodyEditsComplete {
		reason := "body edit history is not complete"
		switch {
		case b.BundleVersion == "":
			reason += " (bundle predates 1.1.0; run refresh-body-edits)"
		case b.BodyEditsError != "":
			reason += ": " + b.BodyEditsError
		}
		return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: reason}
	}
	var last time.Time
	var times []string
	for _, e := range b.BodyEdits {
		if e.DeletedAt != nil {
			return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "an edit record was deleted; the history has a gap"}
		}
		if e.EditedAt.IsZero() {
			return FieldOutcome{Outcome: OutcomeOmittedUnverifiable, Reason: "an edit record lacks a timestamp"}
		}
		times = append(times, e.EditedAt.UTC().Format(time.RFC3339Nano))
		if e.EditedAt.After(last) {
			last = e.EditedAt
		}
	}
	if last.After(cutoff) {
		return FieldOutcome{Outcome: OutcomeOmittedPostCutoff, Reason: fmt.Sprintf("body was edited at %s, after cutoff", last.UTC().Format(time.RFC3339Nano)), Timestamps: times}
	}
	if len(b.BodyEdits) == 0 {
		return FieldOutcome{Outcome: OutcomeAdmitted, MethodToken: MethodCurrent, Value: b.PR.GetBody(), Method: descriptionMethodNeverEdit, Source: provenanceSourceBodyEdits, Timestamps: []string{b.BodyEditsFetchedAt.UTC().Format(time.RFC3339Nano)}}
	}
	return FieldOutcome{Outcome: OutcomeAdmitted, MethodToken: MethodCurrent, Value: b.PR.GetBody(), Method: descriptionMethodLastEdit, Source: provenanceSourceBodyEdits, Timestamps: times}
}

// Admission returns the v2 outcome and method per field, for recording in a cohort
// manifest. It is the same decision Export makes; the cohort tool calls it so the
// manifest and the audit can never disagree.
func Admission(b collector.RawPRBundle, cutoff time.Time) map[string]FieldAdmission {
	t, d := AdmitTitle(b, cutoff), AdmitDescription(b, cutoff)
	return map[string]FieldAdmission{
		"title":       {Outcome: t.Outcome, Method: t.MethodToken},
		"description": {Outcome: d.Outcome, Method: d.MethodToken},
	}
}

func decisionFrom(o FieldOutcome, cutoff time.Time) FieldDecision {
	if o.Outcome != OutcomeAdmitted {
		return FieldDecision{Included: false, Reason: o.Reason, Outcome: o.Outcome}
	}
	return FieldDecision{Included: true, Source: o.Source, SourceTimestamps: o.Timestamps, ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: o.Method, Outcome: OutcomeAdmitted}
}

// BuildV2 is Build under the reviewer-metadata/v2 rule. base_branch and
// commit_messages follow the v1 rules unchanged: neither has a per-field history.
func BuildV2(bundle collector.RawPRBundle, repository string, cutoff time.Time) (Metadata, map[string]FieldDecision, error) {
	if repository == "" {
		return Metadata{}, nil, fmt.Errorf("repository provenance is missing")
	}
	if cutoff.IsZero() {
		return Metadata{}, nil, fmt.Errorf("cutoff is required")
	}
	if bundle.PR == nil {
		return Metadata{}, nil, fmt.Errorf("cached pull request is missing")
	}
	cutoff = cutoff.UTC()
	meta := Metadata{SchemaVersion: MetadataSchemaVersionV2, Repository: repository, CutoffTimestamp: cutoff.Format(time.RFC3339Nano)}
	decisions := map[string]FieldDecision{}
	decisions["repository"] = FieldDecision{Included: true, Source: "correlated original.repository", ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: "stable repository identity"}
	decisions["cutoff_timestamp"] = FieldDecision{Included: true, Source: "export argument", ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: "explicit benchmark cutoff"}

	title := AdmitTitle(bundle, cutoff)
	decisions["title"] = decisionFrom(title, cutoff)
	if title.Outcome == OutcomeAdmitted {
		v := title.Value
		meta.Title = &v
	}
	desc := AdmitDescription(bundle, cutoff)
	decisions["description"] = decisionFrom(desc, cutoff)
	if desc.Outcome == OutcomeAdmitted {
		v := desc.Value
		meta.Description = &v
	}

	buildBaseBranch(bundle, cutoff, &meta, decisions)
	buildCommitMessages(bundle, cutoff, &meta, decisions)
	return meta, decisions, nil
}

// buildBaseBranch applies the v1 rule: the base ref has no edit history, so it is
// admitted only when the PR-level updated_at proves the whole response was final at
// cutoff.
func buildBaseBranch(bundle collector.RawPRBundle, cutoff time.Time, meta *Metadata, decisions map[string]FieldDecision) {
	updated := bundle.PR.GetUpdatedAt().Time
	if !updated.IsZero() && !updated.After(cutoff) {
		if b := bundle.PR.GetBase().GetRef(); b != "" {
			meta.BaseBranch = &b
			decisions["base_branch"] = currentDecision(updated, "provider updated_at proves response value was already final at cutoff")
		} else {
			decisions["base_branch"] = decisionNo("base branch is empty")
		}
		return
	}
	decisions["base_branch"] = decisionNo("exact cutoff base branch cannot be established from current response")
}

// buildCommitMessages applies the v1 rule: membership frozen by a merge no later
// than cutoff, complete cached list, every commit timestamp admissible.
func buildCommitMessages(bundle collector.RawPRBundle, cutoff time.Time, meta *Metadata, decisions map[string]FieldDecision) {
	merged := bundle.PR.GetMergedAt().Time
	if merged.IsZero() || merged.After(cutoff) {
		decisions["commit_messages"] = decisionNo("PR commit membership was not frozen by a merge no later than cutoff")
		return
	}
	if bundle.PR.GetCommits() != len(bundle.Commits) {
		decisions["commit_messages"] = decisionNo("cached commit list is incomplete or contradictory")
		return
	}
	msgs := make([]string, 0, len(bundle.Commits))
	timestamps := []string{}
	for _, c := range bundle.Commits {
		if c == nil || c.Commit == nil || c.Commit.Message == nil {
			decisions["commit_messages"] = decisionNo("a cached commit lacks a message/timestamp or has a timestamp after cutoff")
			return
		}
		dt := commitTime(c)
		if dt.IsZero() || dt.After(cutoff) {
			decisions["commit_messages"] = decisionNo("a cached commit lacks a message/timestamp or has a timestamp after cutoff")
			return
		}
		msgs = append(msgs, c.GetCommit().GetMessage())
		timestamps = append(timestamps, dt.UTC().Format(time.RFC3339Nano))
	}
	meta.CommitMessages = msgs
	decisions["commit_messages"] = FieldDecision{Included: true, Source: "complete cached PR commit list", SourceTimestamps: timestamps, ValidAt: merged.UTC().Format(time.RFC3339Nano), ReconstructionMethod: "membership frozen by merge at or before cutoff; every commit timestamp is admissible"}
}

// metadataSchemaFor maps a version selector to its pinned schema string.
func metadataSchemaFor(version string) (string, error) {
	switch version {
	case "", MetadataVersionV1:
		return MetadataSchemaVersion, nil
	case MetadataVersionV2:
		return MetadataSchemaVersionV2, nil
	default:
		return "", fmt.Errorf("unknown metadata version %q (want %q or %q)", version, MetadataVersionV1, MetadataVersionV2)
	}
}
