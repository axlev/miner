package prospectiveexport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"pr-analysis/internal/collector"
)

var cutoff = time.Date(2024, 4, 10, 5, 22, 26, 0, time.UTC)

func bundle(updated time.Time) collector.RawPRBundle {
	title, body, base, msg := "cutoff title", "cutoff description", "master", "admissible message"
	return collector.RawPRBundle{PR: &github.PullRequest{Number: github.Int(42), Title: &title, Body: &body, UpdatedAt: &github.Timestamp{Time: updated}, MergedAt: &github.Timestamp{Time: cutoff}, Base: &github.PullRequestBranch{Ref: &base}, Commits: github.Int(1)}, Commits: []*github.RepositoryCommit{{Commit: &github.Commit{Message: &msg, Committer: &github.CommitAuthor{Date: &github.Timestamp{Time: cutoff.Add(-time.Hour)}}}}}, FetchedAt: cutoff.AddDate(1, 0, 0)}
}

func TestBuildNeverChangedExportsProvenFields(t *testing.T) {
	b := bundle(cutoff.Add(-time.Hour))
	m, d, err := Build(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title == nil || *m.Title != "cutoff title" || m.Description == nil || *m.Description != "cutoff description" || m.BaseBranch == nil || len(m.CommitMessages) != 1 {
		t.Fatalf("missing admissible fields: %+v", m)
	}
	for _, f := range []string{"title", "description", "base_branch", "commit_messages"} {
		if !d[f].Included {
			t.Fatalf("%s omitted: %+v", f, d[f])
		}
	}
}

func TestBuildReconstructsTitleFromCompleteHistory(t *testing.T) {
	b := bundle(cutoff.Add(time.Hour))
	current := "future-only title"
	b.PR.Title = &current
	b.IssueEventsComplete = true
	b.IssueEvents = []*github.IssueEvent{{Event: github.String("renamed"), CreatedAt: &github.Timestamp{Time: cutoff.Add(time.Minute)}, Rename: &github.Rename{From: github.String("cutoff title"), To: &current}}}
	m, d, err := Build(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title == nil || *m.Title != "cutoff title" {
		t.Fatalf("not reconstructed: %+v", m)
	}
	if d["title"].ReconstructionMethod != "reverse complete post-cutoff rename chain" {
		t.Fatal("missing reconstruction audit")
	}
	if m.Description != nil {
		t.Fatal("post-cutoff body leaked")
	}
}

func TestBuildOmitsAmbiguousEditedText(t *testing.T) {
	for _, complete := range []bool{false, true} {
		b := bundle(cutoff.Add(time.Hour))
		future := "FUTURE_ONLY_TITLE"
		b.PR.Title = &future
		b.IssueEventsComplete = complete
		if complete {
			b.IssueEvents = []*github.IssueEvent{{Event: github.String("renamed"), CreatedAt: &github.Timestamp{Time: cutoff.Add(time.Minute)}, Rename: &github.Rename{From: github.String("unknown"), To: github.String("different chain")}}}
		}
		m, d, err := Build(b, "owner/repo", cutoff)
		if err != nil {
			t.Fatal(err)
		}
		if m.Title != nil || d["title"].Included {
			t.Fatal("ambiguous title leaked")
		}
		if m.Description != nil || d["description"].Included {
			t.Fatal("ambiguous description leaked")
		}
	}
}

func TestPreMergeCutoffOmitsStateAndCommits(t *testing.T) {
	b := bundle(cutoff.Add(-time.Hour))
	b.PR.MergedAt = &github.Timestamp{Time: cutoff.Add(time.Hour)}
	m, d, err := Build(b, "owner/repo", cutoff)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	for _, sentinel := range []string{"merged", "merge_commit_sha", "head_sha", "state"} {
		if strings.Contains(string(raw), sentinel) {
			t.Fatalf("state leaked: %s", raw)
		}
	}
	if len(m.CommitMessages) != 0 || d["commit_messages"].Included {
		t.Fatal("pre-merge membership treated as proven")
	}
}

func TestStrictValidationRejectsUnknownNestedAndForbidden(t *testing.T) {
	cases := [][]byte{[]byte(`{"repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z","new_provider_field":"FUTURE_ONLY"}`), []byte(`{"repository":{"raw_provider_payload":true},"cutoff_timestamp":"2024-04-10T05:22:26Z"}`), []byte(`{"repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z","title":"future-sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}
	for i, data := range cases {
		forbidden := []string(nil)
		if i == 2 {
			forbidden = []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		}
		if err := ValidateNormalized(data, cutoff, forbidden); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func writeFixture(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	future := strings.Repeat("a", 40)
	record := `{"original":{"repository":"owner/repo","number":42,"head_sha":"` + strings.Repeat("b", 40) + `","merge_commit_sha":"` + strings.Repeat("c", 40) + `"},"stateful":{},"retrospective":{"strong_signals":[{"source_ref":"` + future + `","raw_snippet":"FUTURE_ONLY_FIX"}],"medium_signals":[],"weak_signals":[],"commit_relationships":[{"source_ref":"` + future + `","analysis_status":"complete"}]},"provenance":{"target_repo_head_sha":"` + strings.Repeat("d", 40) + `"}}`
	input := filepath.Join(dir, "correlated.jsonl")
	if err := os.WriteFile(input, []byte(record+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := bundle(cutoff.Add(-time.Hour))
	cache := filepath.Join(dir, "cache.json")
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(cache, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return input, cache, record
}

func TestExportSeparatesEvaluatorDataAndIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	input, cache, record := writeFixture(t, dir)
	outs := []string{filepath.Join(dir, "case-001"), filepath.Join(dir, "case-002")}
	for _, out := range outs {
		if err := Export(Options{CorrelatedInput: input, CacheFile: cache, PR: 42, Cutoff: cutoff, CaseID: filepath.Base(out), Out: out}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(filepath.Join(outs[0], "prospective", "metadata.json"))
	b, _ := os.ReadFile(filepath.Join(outs[1], "prospective", "metadata.json"))
	if string(a) != string(b) {
		t.Fatal("normalized metadata differs")
	}
	for _, future := range []string{"FUTURE_ONLY_FIX", strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)} {
		if strings.Contains(string(a), future) {
			t.Fatalf("future value leaked: %s", future)
		}
	}
	eval, _ := os.ReadFile(filepath.Join(outs[0], "evaluator-only", "correlated-report.json"))
	if string(eval) != record+"\n" || !strings.Contains(string(eval), "FUTURE_ONLY_FIX") {
		t.Fatal("evaluator report was altered")
	}
	entries, err := os.ReadDir(filepath.Join(outs[0], "prospective"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "metadata.json" {
		t.Fatalf("non-neutral reviewer artifacts: %+v", entries)
	}
}

func TestExportFailureWritesNoPartialCase(t *testing.T) {
	dir := t.TempDir()
	input, cache, _ := writeFixture(t, dir)
	var b collector.RawPRBundle
	raw, _ := os.ReadFile(cache)
	_ = json.Unmarshal(raw, &b)
	bad := "contains " + strings.Repeat("a", 40)
	b.PR.Title = &bad
	updated := cutoff.Add(-time.Hour)
	b.PR.UpdatedAt = &github.Timestamp{Time: updated}
	raw, _ = json.Marshal(b)
	_ = os.WriteFile(cache, raw, 0644)
	out := filepath.Join(dir, "case-003")
	if err := Export(Options{CorrelatedInput: input, CacheFile: cache, PR: 42, Cutoff: cutoff, CaseID: "case-003", Out: out}); err == nil {
		t.Fatal("expected forbidden-value failure")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("partially trusted case was written")
	}
}
