// Package prospectiveexport builds contamination-safe reviewer metadata.
package prospectiveexport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/gitx"
)

const SchemaVersion = "1.0.0"

// ControlManifestSchemaVersion pins the engine-only routing and identity contract.
// It lives in control/, which is never mounted to a reasoner.
const ControlManifestSchemaVersion = "engine-manifest/v1"

// MetadataSchemaVersion pins the reviewer-visible metadata contract. It is carried
// inside metadata.json itself, so a metadata file separated from its bundle is still
// self-identifying.
const MetadataSchemaVersion = "reviewer-metadata/v1"

// SnapshotFormat names the shape of reviewer/repository: a plain directory tree,
// not a Git repository and not a patch to be applied.
const SnapshotFormat = "directory-snapshot/v1"

// ControlManifest is the engine-only descriptor of a prospective bundle. It carries
// the repository/commit identity that metadata.json's reviewer allowlist deliberately
// omits; being under control/ it is unreachable from a reasoner.
type ControlManifest struct {
	SchemaVersion   string `json:"schema_version"`
	CaseID          string `json:"case_id"`
	Repository      string `json:"repository"`
	CutoffTimestamp string `json:"cutoff_timestamp"`
	// BaseCommit is the locally resolved Git merge-base of the PR's base and head:
	// the commit the admissible diff is taken from.
	BaseCommit string `json:"base_commit"`
	// CutoffCommit is the commit whose tree is materialized at reviewer/repository
	// and whose content the diff's post-image describes.
	CutoffCommit   string `json:"cutoff_commit"`
	SnapshotFormat string `json:"snapshot_format"`
}

var fullSHAPattern = func(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// GenerateCaseID derives a deterministic, opaque case identifier from case-defining inputs.
// It never embeds the PR number or repository as a literal, human-readable substring, closing
// the "case ID discloses PR number" risk of caller-supplied identifiers like "case-15624".
func GenerateCaseID(repository string, pr int, cutoff time.Time) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s#%d@%s", repository, pr, cutoff.UTC().Format(time.RFC3339Nano))))
	return "case-" + hex.EncodeToString(h[:])[:16]
}

// Metadata is the complete and intentionally small reviewer-facing schema.
type Metadata struct {
	SchemaVersion   string   `json:"schema_version"`
	Repository      string   `json:"repository"`
	Title           *string  `json:"title,omitempty"`
	Description     *string  `json:"description,omitempty"`
	CutoffTimestamp string   `json:"cutoff_timestamp"`
	BaseBranch      *string  `json:"base_branch,omitempty"`
	CommitMessages  []string `json:"commit_messages,omitempty"`
}

type FieldDecision struct {
	Included             bool     `json:"included"`
	Source               string   `json:"source,omitempty"`
	SourceTimestamps     []string `json:"source_timestamps,omitempty"`
	ValidAt              string   `json:"valid_at,omitempty"`
	ReconstructionMethod string   `json:"reconstruction_method,omitempty"`
	Reason               string   `json:"reason,omitempty"`
}

type Audit struct {
	SchemaVersion     string                   `json:"schema_version"`
	CaseID            string                   `json:"case_id"`
	PRNumber          int                      `json:"pr_number"`
	Cutoff            string                   `json:"cutoff_timestamp"`
	InputRecordSHA256 string                   `json:"input_record_sha256"`
	CacheBundleSHA256 string                   `json:"cache_bundle_sha256"`
	Fields            map[string]FieldDecision `json:"fields"`
	Validation        string                   `json:"validation"`
}

type Options struct {
	CorrelatedInput string
	CacheFile       string
	// Repo is the local Git repository used to resolve the canonical merge-base and
	// produce the original-change patch. Required.
	Repo   string
	PR     int
	Cutoff time.Time
	// CaseID overrides the auto-generated opaque case identifier. Leave empty to have
	// Export generate one via GenerateCaseID.
	CaseID string
	// ProspectiveOut and EvaluatorOut are separate, caller-supplied output roots. Keeping
	// them separate (rather than subdirectories of one shared root) means a consumer that
	// recursively ingests ProspectiveOut can never traverse into evaluator-only artifacts.
	ProspectiveOut string
	EvaluatorOut   string
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// findRecord returns the single record matching pr. It fails closed on zero or more than
// one match: a duplicate PR number anywhere in the input (whether same-repository duplicate
// or a mixed-repository collision) is ambiguous, so it is rejected rather than resolved by
// returning the first numeric match.
func findRecord(path string, pr int) ([]byte, map[string]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open correlated input: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	line := 0
	type match struct {
		lineNo int
		raw    []byte
		root   map[string]json.RawMessage
		repo   string
	}
	var matches []match
	for s.Scan() {
		line++
		raw := append([]byte(nil), s.Bytes()...)
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, nil, fmt.Errorf("parse correlated line %d: %w", line, err)
		}
		var original struct {
			Number     int    `json:"number"`
			Repository string `json:"repository"`
		}
		if err := json.Unmarshal(root["original"], &original); err != nil {
			return nil, nil, fmt.Errorf("parse original line %d: %w", line, err)
		}
		if original.Number == pr {
			matches = append(matches, match{lineNo: line, raw: raw, root: root, repo: original.Repository})
		}
	}
	if err := s.Err(); err != nil {
		return nil, nil, err
	}
	if len(matches) == 0 {
		return nil, nil, fmt.Errorf("PR #%d not found in %q", pr, path)
	}
	if len(matches) > 1 {
		lines := make([]string, len(matches))
		for i, m := range matches {
			lines[i] = fmt.Sprintf("line %d (%s)", m.lineNo, m.repo)
		}
		return nil, nil, fmt.Errorf("PR #%d is ambiguous in %q: %d matching records (%s)", pr, path, len(matches), strings.Join(lines, ", "))
	}
	return matches[0].raw, matches[0].root, nil
}

func decisionNo(reason string) FieldDecision { return FieldDecision{Included: false, Reason: reason} }

// Build derives only values whose temporal provenance is sufficient.
func Build(bundle collector.RawPRBundle, repository string, cutoff time.Time) (Metadata, map[string]FieldDecision, error) {
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
	meta := Metadata{SchemaVersion: MetadataSchemaVersion, Repository: repository, CutoffTimestamp: cutoff.Format(time.RFC3339Nano)}
	decisions := map[string]FieldDecision{}
	decisions["repository"] = FieldDecision{Included: true, Source: "correlated original.repository", ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: "stable repository identity"}
	decisions["cutoff_timestamp"] = FieldDecision{Included: true, Source: "export argument", ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: "explicit benchmark cutoff"}

	updated := bundle.PR.GetUpdatedAt().Time
	unchangedAtCutoff := !updated.IsZero() && !updated.After(cutoff)
	if unchangedAtCutoff {
		t := bundle.PR.GetTitle()
		meta.Title = &t
		decisions["title"] = currentDecision(updated, "provider updated_at proves response value was already final at cutoff")
	} else if title, times, ok := reconstructTitle(bundle.PR.GetTitle(), bundle.IssueEvents, bundle.IssueEventsComplete, cutoff); ok {
		meta.Title = &title
		decisions["title"] = FieldDecision{Included: true, Source: "cached current title plus complete provider rename history", SourceTimestamps: times, ValidAt: cutoff.Format(time.RFC3339Nano), ReconstructionMethod: "reverse complete post-cutoff rename chain"}
	} else {
		decisions["title"] = decisionNo("exact cutoff title cannot be established from current response and complete rename history")
	}
	if unchangedAtCutoff {
		d := bundle.PR.GetBody()
		meta.Description = &d
		decisions["description"] = currentDecision(updated, "provider updated_at proves response value was already final at cutoff")
	} else {
		decisions["description"] = decisionNo("provider exposes no complete body edit history; exact cutoff description cannot be reconstructed")
	}
	if unchangedAtCutoff {
		b := bundle.PR.GetBase().GetRef()
		if b != "" {
			meta.BaseBranch = &b
			decisions["base_branch"] = currentDecision(updated, "provider updated_at proves response value was already final at cutoff")
		} else {
			decisions["base_branch"] = decisionNo("base branch is empty")
		}
	} else {
		decisions["base_branch"] = decisionNo("exact cutoff base branch cannot be established from current response")
	}

	merged := bundle.PR.GetMergedAt().Time
	if merged.IsZero() || merged.After(cutoff) {
		decisions["commit_messages"] = decisionNo("PR commit membership was not frozen by a merge no later than cutoff")
	} else if bundle.PR.GetCommits() != len(bundle.Commits) {
		decisions["commit_messages"] = decisionNo("cached commit list is incomplete or contradictory")
	} else {
		msgs := make([]string, 0, len(bundle.Commits))
		timestamps := []string{}
		valid := true
		for _, c := range bundle.Commits {
			if c == nil || c.Commit == nil || c.Commit.Message == nil {
				valid = false
				break
			}
			dt := commitTime(c)
			if dt.IsZero() || dt.After(cutoff) {
				valid = false
				break
			}
			msgs = append(msgs, c.GetCommit().GetMessage())
			timestamps = append(timestamps, dt.UTC().Format(time.RFC3339Nano))
		}
		if valid {
			meta.CommitMessages = msgs
			decisions["commit_messages"] = FieldDecision{Included: true, Source: "complete cached PR commit list", SourceTimestamps: timestamps, ValidAt: merged.UTC().Format(time.RFC3339Nano), ReconstructionMethod: "membership frozen by merge at or before cutoff; every commit timestamp is admissible"}
		} else {
			decisions["commit_messages"] = decisionNo("a cached commit lacks a message/timestamp or has a timestamp after cutoff")
		}
	}
	return meta, decisions, nil
}

func currentDecision(updated time.Time, method string) FieldDecision {
	return FieldDecision{Included: true, Source: "cached provider pull request", SourceTimestamps: []string{updated.UTC().Format(time.RFC3339Nano)}, ValidAt: updated.UTC().Format(time.RFC3339Nano), ReconstructionMethod: method}
}

func commitTime(c *github.RepositoryCommit) time.Time {
	if c.Commit.Committer != nil && !c.Commit.Committer.GetDate().Time.IsZero() {
		return c.Commit.Committer.GetDate().Time
	}
	if c.Commit.Author != nil {
		return c.Commit.Author.GetDate().Time
	}
	return time.Time{}
}

func reconstructTitle(current string, events []*github.IssueEvent, complete bool, cutoff time.Time) (string, []string, bool) {
	if !complete {
		return "", nil, false
	}
	renames := []*github.IssueEvent{}
	for _, e := range events {
		if e != nil && e.GetEvent() == "renamed" && e.Rename != nil {
			if e.CreatedAt == nil {
				return "", nil, false
			}
			renames = append(renames, e)
		}
	}
	sort.Slice(renames, func(i, j int) bool { return renames[i].GetCreatedAt().Time.After(renames[j].GetCreatedAt().Time) })
	title := current
	times := []string{}
	for _, e := range renames {
		at := e.GetCreatedAt().Time
		if !at.After(cutoff) {
			continue
		}
		if e.Rename.GetTo() != title || e.Rename.GetFrom() == "" {
			return "", nil, false
		}
		title = e.Rename.GetFrom()
		times = append(times, at.UTC().Format(time.RFC3339Nano))
	}
	return title, times, true
}

var allowedKeys = map[string]bool{"schema_version": true, "repository": true, "title": true, "description": true, "cutoff_timestamp": true, "base_branch": true, "commit_messages": true}

// ValidateNormalized enforces the closed reviewer schema on serialized bytes.
func ValidateNormalized(data []byte, cutoff time.Time, forbidden []string) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var meta Metadata
	if err := dec.Decode(&meta); err != nil {
		return fmt.Errorf("strict prospective schema: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple JSON values or trailing content")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		if !allowedKeys[k] {
			return fmt.Errorf("unknown prospective field %q", k)
		}
		if k != "commit_messages" && len(v) > 0 && (v[0] == '{' || v[0] == '[') {
			return fmt.Errorf("nested raw payload rejected in %q", k)
		}
	}
	if meta.SchemaVersion != MetadataSchemaVersion {
		return fmt.Errorf("reviewer metadata schema_version is %q, want the pinned %q", meta.SchemaVersion, MetadataSchemaVersion)
	}
	if meta.Repository == "" || meta.CutoffTimestamp == "" {
		return fmt.Errorf("repository and cutoff_timestamp are required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, meta.CutoffTimestamp)
	if err != nil {
		return fmt.Errorf("invalid cutoff_timestamp: %w", err)
	}
	if !parsed.Equal(cutoff.UTC()) {
		return fmt.Errorf("output cutoff does not match requested cutoff")
	}
	for _, s := range forbidden {
		if len(s) >= 7 && bytes.Contains(data, []byte(s)) {
			return fmt.Errorf("prospective output contains forbidden current/future value %q", s)
		}
	}
	return nil
}

var manifestAllowedKeys = map[string]bool{"schema_version": true, "case_id": true, "repository": true, "cutoff_timestamp": true, "base_commit": true, "cutoff_commit": true, "snapshot_format": true}

// ValidateControlManifest enforces the closed engine-only manifest schema. Artifact
// integrity is no longer carried here: control/checksums.sha256 covers every file
// under reviewer/, which is strictly stronger because it is exhaustive in both
// directions — a file present but unlisted is as much a signal as a digest mismatch.
func ValidateControlManifest(data []byte, cutoff time.Time) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m ControlManifest
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("strict manifest schema: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("multiple JSON values or trailing content")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k := range raw {
		if !manifestAllowedKeys[k] {
			return fmt.Errorf("unknown manifest field %q", k)
		}
	}
	if m.SchemaVersion != ControlManifestSchemaVersion {
		return fmt.Errorf("control manifest schema_version is %q, want the pinned %q", m.SchemaVersion, ControlManifestSchemaVersion)
	}
	if m.SnapshotFormat != SnapshotFormat {
		return fmt.Errorf("unsupported snapshot_format %q", m.SnapshotFormat)
	}
	if m.CaseID == "" || m.Repository == "" || m.CutoffTimestamp == "" {
		return fmt.Errorf("case_id, repository, and cutoff_timestamp are required")
	}
	if !fullSHAPattern(m.BaseCommit) || !fullSHAPattern(m.CutoffCommit) {
		return fmt.Errorf("base_commit and cutoff_commit must be 40-character hex SHAs")
	}
	parsed, err := time.Parse(time.RFC3339Nano, m.CutoffTimestamp)
	if err != nil {
		return fmt.Errorf("invalid cutoff_timestamp: %w", err)
	}
	if !parsed.Equal(cutoff.UTC()) {
		return fmt.Errorf("manifest cutoff does not match requested cutoff")
	}
	return nil
}

func validateDecisions(decisions map[string]FieldDecision, cutoff time.Time) error {
	for field, d := range decisions {
		if !d.Included {
			continue
		}
		if d.Source == "" || d.ReconstructionMethod == "" || d.ValidAt == "" {
			return fmt.Errorf("included field %q lacks provenance", field)
		}
		validAt, err := time.Parse(time.RFC3339Nano, d.ValidAt)
		if err != nil {
			return fmt.Errorf("field %q has invalid validity timestamp: %w", field, err)
		}
		if validAt.After(cutoff) {
			return fmt.Errorf("field %q was not valid by cutoff", field)
		}
	}
	return nil
}

func validateCommitMessages(meta Metadata, bundle collector.RawPRBundle, cutoff time.Time) error {
	if len(meta.CommitMessages) == 0 {
		return nil
	}
	if bundle.PR == nil || bundle.PR.GetMergedAt().Time.IsZero() || bundle.PR.GetMergedAt().Time.After(cutoff) {
		return fmt.Errorf("commit membership is not frozen at cutoff")
	}
	if len(meta.CommitMessages) != len(bundle.Commits) || bundle.PR.GetCommits() != len(bundle.Commits) {
		return fmt.Errorf("commit message set is incomplete")
	}
	for i, c := range bundle.Commits {
		if c == nil || c.Commit == nil || c.Commit.Message == nil || commitTime(c).IsZero() || commitTime(c).After(cutoff) {
			return fmt.Errorf("commit message %d lacks admissible provenance", i)
		}
		if meta.CommitMessages[i] != c.GetCommit().GetMessage() {
			return fmt.Errorf("commit message %d does not match admissible commit", i)
		}
	}
	return nil
}

func forbiddenValues(root map[string]json.RawMessage) []string {
	vals := map[string]bool{}
	var original map[string]json.RawMessage
	_ = json.Unmarshal(root["original"], &original)
	for _, k := range []string{"head_sha", "merge_commit_sha"} {
		var s string
		_ = json.Unmarshal(original[k], &s)
		if s != "" {
			vals[s] = true
		}
	}
	var prov map[string]json.RawMessage
	_ = json.Unmarshal(root["provenance"], &prov)
	var s string
	_ = json.Unmarshal(prov["target_repo_head_sha"], &s)
	if s != "" {
		vals[s] = true
	}
	var wrapper map[string]json.RawMessage
	_ = json.Unmarshal(root["retrospective"], &wrapper)
	for _, key := range []string{"strong_signals", "medium_signals", "weak_signals"} {
		var a []struct {
			SourceRef  string `json:"source_ref"`
			RawSnippet string `json:"raw_snippet"`
			Context    string `json:"context"`
		}
		_ = json.Unmarshal(wrapper[key], &a)
		for _, x := range a {
			if len(x.SourceRef) == 40 {
				vals[x.SourceRef] = true
			}
			if len(x.RawSnippet) >= 7 {
				vals[x.RawSnippet] = true
			}
			if len(x.Context) >= 7 {
				vals[x.Context] = true
			}
		}
	}
	var rel []struct {
		SourceRef    string `json:"source_ref"`
		AnalysisNote string `json:"analysis_note"`
	}
	_ = json.Unmarshal(wrapper["commit_relationships"], &rel)
	for _, x := range rel {
		if len(x.SourceRef) == 40 {
			vals[x.SourceRef] = true
		}
		if len(x.AnalysisNote) >= 7 {
			vals[x.AnalysisNote] = true
		}
	}
	out := []string{}
	for v := range vals {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// crossCheckIdentity fails closed unless the cache bundle and the selected correlated
// record agree on repository, PR number, and base/head commit identity. Without this,
// a wrong cache file or a mixed-repository input could combine one repository's identity
// with another PR's text (docs/export-contract.md §7 risk 5).
func crossCheckIdentity(bundle collector.RawPRBundle, pr int, repository, baseSHA, headSHA string) error {
	if bundle.PR == nil {
		return fmt.Errorf("cached pull request is missing")
	}
	if bundle.PR.GetNumber() != pr {
		return fmt.Errorf("cache bundle PR number %d does not match selected record PR #%d", bundle.PR.GetNumber(), pr)
	}
	bundleRepo := bundle.PR.GetBase().GetRepo().GetFullName()
	if bundleRepo == "" || bundleRepo != repository {
		return fmt.Errorf("cache bundle repository identity %q does not match selected record repository %q", bundleRepo, repository)
	}
	bundleBase := bundle.PR.GetBase().GetSHA()
	bundleHead := bundle.PR.GetHead().GetSHA()
	if baseSHA == "" || bundleBase == "" || bundleBase != baseSHA {
		return fmt.Errorf("cache bundle base_sha %q does not match selected record base_sha %q", bundleBase, baseSHA)
	}
	if headSHA == "" || bundleHead == "" || bundleHead != headSHA {
		return fmt.Errorf("cache bundle head_sha %q does not match selected record head_sha %q", bundleHead, headSHA)
	}
	return nil
}

// Export builds a prospective bundle and a separate evaluator-only directory, each
// published atomically to its own caller-supplied root.
//
// The bundle conforms to the engine's ingest contract: a reviewer/ tree holding the
// materialized source snapshot, the admissible diff, and reviewer-visible metadata,
// and a control/ tree holding engine-only routing identity and integrity. The two
// are separate directories because the engine allow-list-copies only the three named
// entries under reviewer/, so control/ cannot reach a reasoner even by accident.
//
// Admissibility is decided by engine-runner's boundary validator, not here: the miner
// no longer keeps a copy of those rules. Use `miner cohort-verify --bench-repo` to run
// the real validator against a built bundle.
func Export(ctx context.Context, opt Options) error {
	if opt.PR <= 0 || opt.CorrelatedInput == "" || opt.CacheFile == "" || opt.Repo == "" || opt.ProspectiveOut == "" || opt.EvaluatorOut == "" {
		return fmt.Errorf("correlated input, cache file, repo, pr, cutoff, prospective-out, and evaluator-out are required")
	}
	if opt.CaseID != "" && (strings.ContainsAny(opt.CaseID, "/\\") || opt.CaseID == "." || opt.CaseID == "..") {
		return fmt.Errorf("case-id must be a neutral path component")
	}
	raw, root, err := findRecord(opt.CorrelatedInput, opt.PR)
	if err != nil {
		return err
	}
	cacheRaw, err := os.ReadFile(opt.CacheFile)
	if err != nil {
		return fmt.Errorf("read cache bundle: %w", err)
	}
	var bundle collector.RawPRBundle
	if err := json.Unmarshal(cacheRaw, &bundle); err != nil {
		return fmt.Errorf("parse cache bundle: %w", err)
	}
	var original struct {
		Repository string `json:"repository"`
		BaseSHA    string `json:"base_sha"`
		HeadSHA    string `json:"head_sha"`
	}
	if err := json.Unmarshal(root["original"], &original); err != nil {
		return err
	}
	if err := crossCheckIdentity(bundle, opt.PR, original.Repository, original.BaseSHA, original.HeadSHA); err != nil {
		return fmt.Errorf("prospective identity validation failed: %w", err)
	}

	meta, decisions, err := Build(bundle, original.Repository, opt.Cutoff)
	if err != nil {
		return err
	}
	if err := validateDecisions(decisions, opt.Cutoff.UTC()); err != nil {
		return fmt.Errorf("prospective provenance validation failed: %w", err)
	}
	if err := validateCommitMessages(meta, bundle, opt.Cutoff.UTC()); err != nil {
		return fmt.Errorf("prospective commit validation failed: %w", err)
	}
	normalized, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	normalized = append(normalized, '\n')
	if err := ValidateNormalized(normalized, opt.Cutoff, forbiddenValues(root)); err != nil {
		return fmt.Errorf("prospective validation failed: %w", err)
	}

	// The diff and the snapshot are derived from the same two commit objects in the
	// same repository: the patch's post-image is the tree of original.HeadSHA, and
	// reviewer/repository is that same tree materialized. Correspondence is therefore
	// a property of how they are produced, not a reconciliation step that could drift
	// — which matters because nothing downstream re-checks that the diff describes the
	// snapshot, and a reviewer citing a line the snapshot does not have fails a stage
	// that has already been paid for.
	repo := gitx.OpenRepository(opt.Repo)
	patch, mergeBase, err := repo.CanonicalChangePatch(ctx, original.BaseSHA, original.HeadSHA)
	if err != nil {
		return fmt.Errorf("resolve canonical change patch: %w", err)
	}

	caseID := opt.CaseID
	if caseID == "" {
		caseID = GenerateCaseID(original.Repository, opt.PR, opt.Cutoff)
	}
	manifest := ControlManifest{
		SchemaVersion:   ControlManifestSchemaVersion,
		CaseID:          caseID,
		Repository:      original.Repository,
		CutoffTimestamp: opt.Cutoff.UTC().Format(time.RFC3339Nano),
		BaseCommit:      mergeBase,
		CutoffCommit:    original.HeadSHA,
		SnapshotFormat:  SnapshotFormat,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := ValidateControlManifest(manifestBytes, opt.Cutoff); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}

	if _, err := os.Stat(opt.ProspectiveOut); err == nil {
		return fmt.Errorf("prospective output %q already exists; refusing to mix or overwrite case artifacts", opt.ProspectiveOut)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(opt.EvaluatorOut); err == nil {
		return fmt.Errorf("evaluator output %q already exists; refusing to mix or overwrite case artifacts", opt.EvaluatorOut)
	} else if !os.IsNotExist(err) {
		return err
	}

	prospectiveParent := filepath.Dir(opt.ProspectiveOut)
	if err := os.MkdirAll(prospectiveParent, 0755); err != nil {
		return err
	}
	prospectiveTmp, err := os.MkdirTemp(prospectiveParent, ".prospective-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(prospectiveTmp)
	// MkdirTemp creates 0700; the published bundle root is bind-mounted read-only
	// into a stage container that does not run as this user, so it has to be
	// traversable.
	if err := os.Chmod(prospectiveTmp, 0755); err != nil {
		return err
	}
	reviewerTmp := filepath.Join(prospectiveTmp, ReviewerDir)
	controlTmp := filepath.Join(prospectiveTmp, ControlDir)
	if err := os.MkdirAll(reviewerTmp, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(controlTmp, 0755); err != nil {
		return err
	}
	if _, err := repo.MaterializeTree(ctx, original.HeadSHA, filepath.Join(reviewerTmp, SnapshotDir)); err != nil {
		return fmt.Errorf("materialize source snapshot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(reviewerTmp, MetadataFile), normalized, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(reviewerTmp, DiffFile), patch, 0644); err != nil {
		return err
	}
	checksums, err := buildChecksums(prospectiveTmp)
	if err != nil {
		return fmt.Errorf("build checksum manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(controlTmp, ChecksumsFile), checksums, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(controlTmp, ControlManifestFile), manifestBytes, 0644); err != nil {
		return err
	}
	if err := checkEvaluatorArtifactNames(prospectiveTmp); err != nil {
		return fmt.Errorf("prospective bundle rejected: %w", err)
	}

	evaluatorParent := filepath.Dir(opt.EvaluatorOut)
	if err := os.MkdirAll(evaluatorParent, 0755); err != nil {
		return err
	}
	evaluatorTmp, err := os.MkdirTemp(evaluatorParent, ".evaluator-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(evaluatorTmp)
	if err := os.WriteFile(filepath.Join(evaluatorTmp, "correlated-report.json"), append(append([]byte(nil), raw...), '\n'), 0644); err != nil {
		return err
	}
	audit := Audit{SchemaVersion: SchemaVersion, CaseID: caseID, PRNumber: opt.PR, Cutoff: opt.Cutoff.UTC().Format(time.RFC3339Nano), InputRecordSHA256: digest(raw), CacheBundleSHA256: digest(cacheRaw), Fields: decisions, Validation: "passed"}
	ab, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	ab = append(ab, '\n')
	if err := os.WriteFile(filepath.Join(evaluatorTmp, "metadata-export-audit.json"), ab, 0644); err != nil {
		return err
	}

	// Both roots are fully built and validated before either rename, bounding the
	// partial-publication window to the two filesystem rename calls themselves.
	if err := os.Rename(prospectiveTmp, opt.ProspectiveOut); err != nil {
		return err
	}
	if err := os.Rename(evaluatorTmp, opt.EvaluatorOut); err != nil {
		return err
	}
	return nil
}
