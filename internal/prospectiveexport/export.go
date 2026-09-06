// Package prospectiveexport builds contamination-safe reviewer metadata.
package prospectiveexport

import (
	"bufio"
	"bytes"
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
)

const SchemaVersion = "1.0.0"

// Metadata is the complete and intentionally small reviewer-facing schema.
type Metadata struct {
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
	PR              int
	Cutoff          time.Time
	CaseID          string
	Out             string
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func findRecord(path string, pr int) ([]byte, map[string]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open correlated input: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	line := 0
	for s.Scan() {
		line++
		raw := append([]byte(nil), s.Bytes()...)
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, nil, fmt.Errorf("parse correlated line %d: %w", line, err)
		}
		var original struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal(root["original"], &original); err != nil {
			return nil, nil, fmt.Errorf("parse original line %d: %w", line, err)
		}
		if original.Number == pr {
			return raw, root, nil
		}
	}
	if err := s.Err(); err != nil {
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("PR #%d not found in %q", pr, path)
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
	meta := Metadata{Repository: repository, CutoffTimestamp: cutoff.Format(time.RFC3339Nano)}
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

var allowedKeys = map[string]bool{"repository": true, "title": true, "description": true, "cutoff_timestamp": true, "base_branch": true, "commit_messages": true}

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

// Export atomically creates separated prospective and evaluator-only artifacts.
func Export(opt Options) error {
	if opt.PR <= 0 || opt.CorrelatedInput == "" || opt.CacheFile == "" || opt.Out == "" || opt.CaseID == "" {
		return fmt.Errorf("correlated input, cache file, pr, cutoff, case-id, and out are required")
	}
	if strings.ContainsAny(opt.CaseID, "/\\") || opt.CaseID == "." || opt.CaseID == ".." {
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
	}
	if err := json.Unmarshal(root["original"], &original); err != nil {
		return err
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
	parent := filepath.Dir(opt.Out)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".prospective-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if _, err := os.Stat(opt.Out); err == nil {
		return fmt.Errorf("output %q already exists; refusing to mix or overwrite case artifacts", opt.Out)
	} else if !os.IsNotExist(err) {
		return err
	}
	prospectiveDir := filepath.Join(tmp, "prospective")
	evaluatorDir := filepath.Join(tmp, "evaluator-only")
	if err := os.MkdirAll(prospectiveDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(evaluatorDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(prospectiveDir, "metadata.json"), normalized, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(evaluatorDir, "correlated-report.json"), append(append([]byte(nil), raw...), '\n'), 0644); err != nil {
		return err
	}
	audit := Audit{SchemaVersion: SchemaVersion, CaseID: opt.CaseID, PRNumber: opt.PR, Cutoff: opt.Cutoff.UTC().Format(time.RFC3339Nano), InputRecordSHA256: digest(raw), CacheBundleSHA256: digest(cacheRaw), Fields: decisions, Validation: "passed"}
	ab, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(evaluatorDir, "metadata-export-audit.json"), append(ab, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, opt.Out)
}
