package prospectiveexport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
)

// v2Bundle is a 1.1.0 bundle with complete histories and no edits after creation.
// updated_at is deliberately AFTER the cutoff: under v2 it must not matter.
func v2Bundle() collector.RawPRBundle {
	b := bundle(cutoff.Add(48 * time.Hour))
	b.PR.CreatedAt = &github.Timestamp{Time: cutoff.Add(-72 * time.Hour)}
	b.IssueEventsComplete = true
	b.BundleVersion = collector.BundleVersion
	b.BodyEditsComplete = true
	b.BodyEdits = []collector.BodyEdit{}
	b.BodyEditsFetchedAt = cutoff.Add(30 * 24 * time.Hour)
	return b
}

func edit(at time.Time) collector.BodyEdit {
	return collector.BodyEdit{CreatedAt: at, EditedAt: at, UpdatedAt: at, Editor: "someone"}
}

func TestV2DescriptionOutcomes(t *testing.T) {
	cases := map[string]struct {
		mutate  func(b *collector.RawPRBundle)
		outcome string
		method  string
		reason  string
	}{
		"never edited, complete": {func(b *collector.RawPRBundle) {}, OutcomeAdmitted, MethodCurrent, ""},
		"edited before cutoff": {func(b *collector.RawPRBundle) {
			b.BodyEdits = []collector.BodyEdit{edit(cutoff.Add(-time.Hour)), edit(cutoff.Add(-2 * time.Hour))}
		}, OutcomeAdmitted, MethodCurrent, ""},
		"edited exactly at cutoff": {func(b *collector.RawPRBundle) { b.BodyEdits = []collector.BodyEdit{edit(cutoff)} }, OutcomeAdmitted, MethodCurrent, ""},
		"edited after cutoff": {func(b *collector.RawPRBundle) {
			b.BodyEdits = []collector.BodyEdit{edit(cutoff.Add(-time.Hour)), edit(cutoff.Add(time.Minute))}
		}, OutcomeOmittedPostCutoff, "", "after cutoff"},
		"history incomplete": {func(b *collector.RawPRBundle) { b.BodyEditsComplete = false; b.BodyEditsError = "HTTP 500" }, OutcomeOmittedUnverifiable, "", "HTTP 500"},
		"old bundle":         {func(b *collector.RawPRBundle) { b.BundleVersion = ""; b.BodyEditsComplete = false; b.BodyEdits = nil }, OutcomeOmittedUnverifiable, "", "predates 1.1.0"},
		"deleted edit record": {func(b *collector.RawPRBundle) {
			d := cutoff
			b.BodyEdits = []collector.BodyEdit{{EditedAt: cutoff.Add(-time.Hour), DeletedAt: &d}}
		}, OutcomeOmittedUnverifiable, "", "deleted"},
		"edit without timestamp": {func(b *collector.RawPRBundle) { b.BodyEdits = []collector.BodyEdit{{Editor: "x"}} }, OutcomeOmittedUnverifiable, "", "lacks a timestamp"},
		"created after cutoff":   {func(b *collector.RawPRBundle) { b.PR.CreatedAt = &github.Timestamp{Time: cutoff.Add(time.Hour)} }, OutcomeOmittedUnverifiable, "", "after cutoff"},
	}
	for name, c := range cases {
		b := v2Bundle()
		c.mutate(&b)
		got := AdmitDescription(b, cutoff)
		if got.Outcome != c.outcome || got.MethodToken != c.method {
			t.Errorf("%s: outcome=%q method=%q, want %q/%q (reason %q)", name, got.Outcome, got.MethodToken, c.outcome, c.method, got.Reason)
		}
		if c.reason != "" && !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: reason %q should mention %q", name, got.Reason, c.reason)
		}
		if got.Outcome == OutcomeAdmitted && got.Value != "cutoff description" {
			t.Errorf("%s: admitted value %q", name, got.Value)
		}
	}
}

func TestV2TitleOutcomes(t *testing.T) {
	rename := func(at time.Time, from, to string) *github.IssueEvent {
		return &github.IssueEvent{Event: github.String("renamed"), CreatedAt: &github.Timestamp{Time: at}, Rename: &github.Rename{From: github.String(from), To: github.String(to)}}
	}
	cases := map[string]struct {
		mutate  func(b *collector.RawPRBundle)
		outcome string
		method  string
		value   string
	}{
		"never renamed, complete": {func(b *collector.RawPRBundle) {}, OutcomeAdmitted, MethodCurrent, "cutoff title"},
		"renamed before cutoff": {func(b *collector.RawPRBundle) {
			b.IssueEvents = []*github.IssueEvent{rename(cutoff.Add(-time.Hour), "old", "cutoff title")}
		}, OutcomeAdmitted, MethodCurrent, "cutoff title"},
		"renamed after cutoff": {func(b *collector.RawPRBundle) {
			b.PR.Title = github.String("later title")
			b.IssueEvents = []*github.IssueEvent{rename(cutoff.Add(time.Hour), "cutoff title", "later title")}
		}, OutcomeAdmitted, MethodReconstructed, "cutoff title"},
		"two renames after cutoff": {func(b *collector.RawPRBundle) {
			b.PR.Title = github.String("third")
			b.IssueEvents = []*github.IssueEvent{rename(cutoff.Add(time.Hour), "cutoff title", "second"), rename(cutoff.Add(2*time.Hour), "second", "third")}
		}, OutcomeAdmitted, MethodReconstructed, "cutoff title"},
		"events incomplete": {func(b *collector.RawPRBundle) { b.IssueEventsComplete = false }, OutcomeOmittedUnverifiable, "", ""},
		"broken chain": {func(b *collector.RawPRBundle) {
			b.PR.Title = github.String("later title")
			b.IssueEvents = []*github.IssueEvent{rename(cutoff.Add(time.Hour), "unknown", "different")}
		}, OutcomeOmittedUnverifiable, "", ""},
		"rename without timestamp": {func(b *collector.RawPRBundle) {
			e := rename(cutoff.Add(time.Hour), "cutoff title", "x")
			e.CreatedAt = nil
			b.PR.Title = github.String("x")
			b.IssueEvents = []*github.IssueEvent{e}
		}, OutcomeOmittedUnverifiable, "", ""},
		"created after cutoff": {func(b *collector.RawPRBundle) { b.PR.CreatedAt = &github.Timestamp{Time: cutoff.Add(time.Hour)} }, OutcomeOmittedUnverifiable, "", ""},
	}
	for name, c := range cases {
		b := v2Bundle()
		c.mutate(&b)
		got := AdmitTitle(b, cutoff)
		if got.Outcome != c.outcome || got.MethodToken != c.method || got.Value != c.value {
			t.Errorf("%s: outcome=%q method=%q value=%q, want %q/%q/%q (reason %q)", name, got.Outcome, got.MethodToken, got.Value, c.outcome, c.method, c.value, got.Reason)
		}
	}
}

// TestBuildV2IgnoresUpdatedAt is the point of v2: the PR-level updated_at, which any
// post-merge comment advances, must not decide either field.
func TestBuildV2IgnoresUpdatedAt(t *testing.T) {
	b := v2Bundle() // updated_at is after cutoff
	m, d, err := BuildV2(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != MetadataSchemaVersionV2 {
		t.Errorf("schema_version %q", m.SchemaVersion)
	}
	if m.Title == nil || *m.Title != "cutoff title" || m.Description == nil || *m.Description != "cutoff description" {
		t.Fatalf("v2 must admit both fields from their histories: %+v", m)
	}
	if d["title"].Outcome != OutcomeAdmitted || d["description"].Outcome != OutcomeAdmitted {
		t.Errorf("audit outcomes: %+v / %+v", d["title"], d["description"])
	}
	// base_branch keeps the v1 rule and is omitted here because updated_at is late.
	if m.BaseBranch != nil || d["base_branch"].Included {
		t.Errorf("base_branch has no history and must follow the v1 rule: %+v", d["base_branch"])
	}
	if len(m.CommitMessages) != 1 {
		t.Errorf("commit messages unchanged under v2: %+v", m.CommitMessages)
	}
	// Same bundle under v1: the description is omitted because updated_at is past
	// cutoff (v1 has no body history to consult); the title survives only through
	// v1's own complete-rename-history path. The new bundle fields change nothing.
	m1, _, err := Build(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if m1.Description != nil {
		t.Errorf("v1 must still omit the description on updated_at: %+v", m1)
	}
	b1 := b
	b1.IssueEventsComplete = false
	m1, _, _ = Build(b1, "owner/repo", cutoff)
	if m1.Title != nil || m1.Description != nil {
		t.Errorf("v1 with incomplete events must omit both: %+v", m1)
	}
	// The v2 wire shape has no field v1 lacks.
	raw, _ := json.Marshal(m)
	var keys map[string]any
	json.Unmarshal(raw, &keys)
	for k := range keys {
		if !allowedKeys[k] {
			t.Errorf("v2 emitted a key outside the v1 allowlist: %q", k)
		}
	}
	if err := ValidateNormalized(append(raw, '\n'), cutoff, nil, MetadataSchemaVersionV2); err != nil {
		t.Errorf("v2 output fails validation: %v", err)
	}
	if err := ValidateNormalized(append(raw, '\n'), cutoff, nil, MetadataSchemaVersion); err == nil {
		t.Errorf("a v2 file must not validate as v1")
	}
}

func TestBuildV2OldBundleOmitsBodyAsUnverifiable(t *testing.T) {
	b := bundle(cutoff.Add(-time.Hour)) // pre-1.1.0 shape: no body history at all
	b.PR.CreatedAt = &github.Timestamp{Time: cutoff.Add(-72 * time.Hour)}
	b.IssueEventsComplete = true
	m, d, err := BuildV2(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != nil || d["description"].Outcome != OutcomeOmittedUnverifiable {
		t.Errorf("old bundle body must be unverifiable, not admitted on updated_at: %+v", d["description"])
	}
	if m.Title == nil || d["title"].Outcome != OutcomeAdmitted {
		t.Errorf("title still follows the rename history: %+v", d["title"])
	}
	a := Admission(b, cutoff)
	if a["title"].Outcome != OutcomeAdmitted || a["title"].Method != MethodCurrent || a["description"].Outcome != OutcomeOmittedUnverifiable {
		t.Errorf("Admission = %+v", a)
	}
}

func TestMetadataSchemaFor(t *testing.T) {
	for in, want := range map[string]string{"": MetadataSchemaVersion, "v1": MetadataSchemaVersion, "v2": MetadataSchemaVersionV2} {
		got, err := metadataSchemaFor(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q (%v), want %q", in, got, err, want)
		}
	}
	if _, err := metadataSchemaFor("v9"); err == nil {
		t.Errorf("unknown version accepted")
	}
}
