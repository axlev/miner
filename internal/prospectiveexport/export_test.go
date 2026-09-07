package prospectiveexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
)

var cutoff = time.Date(2024, 4, 10, 5, 22, 26, 0, time.UTC)

func bundle(updated time.Time) collector.RawPRBundle {
	title, body, base, msg := "cutoff title", "cutoff description", "master", "admissible message"
	return collector.RawPRBundle{PR: &github.PullRequest{Number: github.Int(42), Title: &title, Body: &body, UpdatedAt: &github.Timestamp{Time: updated}, MergedAt: &github.Timestamp{Time: cutoff}, Base: &github.PullRequestBranch{Ref: &base}, Commits: github.Int(1)}, Commits: []*github.RepositoryCommit{{Commit: &github.Commit{Message: &msg, Committer: &github.CommitAuthor{Date: &github.Timestamp{Time: cutoff.Add(-time.Hour)}}}}}, FetchedAt: cutoff.AddDate(1, 0, 0)}
}

// withIdentity attaches the repository/base/head identity fields that only Export's
// cross-identity check and canonical-patch resolution require; the Build()-only tests
// above don't need them.
func withIdentity(b collector.RawPRBundle, repository, baseSHA, headSHA string) collector.RawPRBundle {
	b.PR.Base.SHA = &baseSHA
	repo := repository
	b.PR.Base.Repo = &github.Repository{FullName: &repo}
	b.PR.Head = &github.PullRequestBranch{SHA: &headSHA}
	return b
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

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// buildRepo creates a two-commit linear history so the merge-base of baseSHA/headSHA is
// baseSHA itself, and the canonical change patch is exactly the head commit's diff.
func buildRepo(t *testing.T) (dir, baseSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "base.txt")
	runGit(t, dir, "commit", "-q", "-m", "base")
	baseSHA = runGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "feature.txt")
	runGit(t, dir, "commit", "-q", "-m", "feature")
	headSHA = runGit(t, dir, "rev-parse", "HEAD")
	return dir, baseSHA, headSHA
}

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type fixtureSet struct {
	repoDir, input, cache string
	baseSHA, headSHA      string
	record                string
}

func writeFixture(t *testing.T, dir string) fixtureSet {
	t.Helper()
	repoDir, baseSHA, headSHA := buildRepo(t)
	future := strings.Repeat("a", 40)
	mergeSHA := strings.Repeat("c", 40)
	targetHead := strings.Repeat("d", 40)
	tmpl, err := os.ReadFile(filepath.Join("..", "..", "testdata", "prospectiveexport", "correlated_record.json.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	replacer := strings.NewReplacer("{{REPO}}", "owner/repo", "{{BASE_SHA}}", baseSHA, "{{HEAD_SHA}}", headSHA, "{{MERGE_SHA}}", mergeSHA, "{{FUTURE_SHA}}", future, "{{TARGET_HEAD_SHA}}", targetHead)
	record := strings.TrimRight(replacer.Replace(string(tmpl)), "\n")
	input := filepath.Join(dir, "correlated.jsonl")
	if err := os.WriteFile(input, []byte(record+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := withIdentity(bundle(cutoff.Add(-time.Hour)), "owner/repo", baseSHA, headSHA)
	cache := filepath.Join(dir, "cache.json")
	raw, _ := json.Marshal(b)
	if err := os.WriteFile(cache, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return fixtureSet{repoDir: repoDir, input: input, cache: cache, baseSHA: baseSHA, headSHA: headSHA, record: record}
}

func TestExportSeparatesEvaluatorDataAndIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	prospOuts := []string{filepath.Join(dir, "prospective-1"), filepath.Join(dir, "prospective-2")}
	evalOuts := []string{filepath.Join(dir, "evaluator-1"), filepath.Join(dir, "evaluator-2")}
	for i := range prospOuts {
		opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: prospOuts[i], EvaluatorOut: evalOuts[i]}
		if err := Export(context.Background(), opt); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(filepath.Join(prospOuts[0], "metadata.json"))
	b, _ := os.ReadFile(filepath.Join(prospOuts[1], "metadata.json"))
	if string(a) != string(b) {
		t.Fatal("normalized metadata differs")
	}
	manifestA, _ := os.ReadFile(filepath.Join(prospOuts[0], "manifest.json"))
	manifestB, _ := os.ReadFile(filepath.Join(prospOuts[1], "manifest.json"))
	if string(manifestA) != string(manifestB) {
		t.Fatal("manifest differs across identical runs; auto-generated case ID should be deterministic")
	}
	for _, future := range []string{"FUTURE_ONLY_FIX", strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)} {
		if strings.Contains(string(a), future) {
			t.Fatalf("future value leaked into metadata: %s", future)
		}
	}
	patch, err := os.ReadFile(filepath.Join(prospOuts[0], "change.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "feature.txt") {
		t.Fatalf("change.patch missing expected content: %s", patch)
	}

	var m Manifest
	if err := json.Unmarshal(manifestA, &m); err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != ManifestSchemaVersion {
		t.Fatalf("unexpected schema_version: %s", m.SchemaVersion)
	}
	if m.ComparisonBaseSHA != fx.baseSHA {
		t.Fatalf("expected comparison_base_sha %s, got %s", fx.baseSHA, m.ComparisonBaseSHA)
	}
	if m.HeadSHA != fx.headSHA {
		t.Fatalf("expected head_sha %s, got %s", fx.headSHA, m.HeadSHA)
	}
	if m.Artifacts["metadata.json"].SHA256 != sha256Hex(a) {
		t.Fatal("manifest metadata.json hash does not match on-disk bytes")
	}
	if m.Artifacts["change.patch"].SHA256 != sha256Hex(patch) {
		t.Fatal("manifest change.patch hash does not match on-disk bytes")
	}
	if !strings.Contains(m.CaseID, strings.TrimPrefix(m.CaseID, "case-")) || strings.Contains(m.CaseID, "42") {
		t.Fatalf("auto-generated case ID appears to disclose PR number: %s", m.CaseID)
	}

	eval, _ := os.ReadFile(filepath.Join(evalOuts[0], "correlated-report.json"))
	if string(eval) != fx.record+"\n" || !strings.Contains(string(eval), "FUTURE_ONLY_FIX") {
		t.Fatal("evaluator report was altered")
	}
	prospEntries, err := os.ReadDir(prospOuts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(prospEntries) != 3 {
		t.Fatalf("expected exactly manifest.json, metadata.json, change.patch; got %+v", prospEntries)
	}
	evalEntries, err := os.ReadDir(evalOuts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(evalEntries) != 2 {
		t.Fatalf("expected exactly correlated-report.json and metadata-export-audit.json; got %+v", evalEntries)
	}
}

func TestExportFailureWritesNoPartialCase(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	var b collector.RawPRBundle
	raw, _ := os.ReadFile(fx.cache)
	_ = json.Unmarshal(raw, &b)
	bad := "contains " + strings.Repeat("a", 40)
	b.PR.Title = &bad
	updated := cutoff.Add(-time.Hour)
	b.PR.UpdatedAt = &github.Timestamp{Time: updated}
	raw, _ = json.Marshal(b)
	_ = os.WriteFile(fx.cache, raw, 0644)
	prospOut := filepath.Join(dir, "prospective-3")
	evalOut := filepath.Join(dir, "evaluator-3")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: prospOut, EvaluatorOut: evalOut}
	if err := Export(context.Background(), opt); err == nil {
		t.Fatal("expected forbidden-value failure")
	}
	if _, err := os.Stat(prospOut); !os.IsNotExist(err) {
		t.Fatal("partially trusted prospective case was written")
	}
	if _, err := os.Stat(evalOut); !os.IsNotExist(err) {
		t.Fatal("partially trusted evaluator case was written")
	}
}

func TestExportRejectsMismatchedCacheIdentity(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)

	mutate := func(f func(b *collector.RawPRBundle)) string {
		var b collector.RawPRBundle
		raw, _ := os.ReadFile(fx.cache)
		_ = json.Unmarshal(raw, &b)
		f(&b)
		raw, _ = json.Marshal(b)
		path := filepath.Join(t.TempDir(), "cache.json")
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cases := map[string]string{
		"wrong PR number": mutate(func(b *collector.RawPRBundle) { b.PR.Number = github.Int(99) }),
		"wrong repository": mutate(func(b *collector.RawPRBundle) {
			other := "someone-else/other-repo"
			b.PR.Base.Repo.FullName = &other
		}),
		"wrong base SHA": mutate(func(b *collector.RawPRBundle) { other := strings.Repeat("9", 40); b.PR.Base.SHA = &other }),
		"wrong head SHA": mutate(func(b *collector.RawPRBundle) { other := strings.Repeat("9", 40); b.PR.Head.SHA = &other }),
	}
	for name, cachePath := range cases {
		t.Run(name, func(t *testing.T) {
			prospOut := filepath.Join(t.TempDir(), "prospective")
			evalOut := filepath.Join(t.TempDir(), "evaluator")
			opt := Options{CorrelatedInput: fx.input, CacheFile: cachePath, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: prospOut, EvaluatorOut: evalOut}
			if err := Export(context.Background(), opt); err == nil {
				t.Fatalf("%s: expected identity validation failure", name)
			}
			if _, err := os.Stat(prospOut); !os.IsNotExist(err) {
				t.Fatalf("%s: prospective case written despite identity mismatch", name)
			}
		})
	}
}

func TestExportRejectsExistingOutputRoots(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)

	prospOut := filepath.Join(dir, "prospective-exists")
	evalOut := filepath.Join(dir, "evaluator-exists")
	if err := os.MkdirAll(prospOut, 0755); err != nil {
		t.Fatal(err)
	}
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: prospOut, EvaluatorOut: evalOut}
	if err := Export(context.Background(), opt); err == nil {
		t.Fatal("expected refusal when prospective-out already exists")
	}
	if _, err := os.Stat(evalOut); !os.IsNotExist(err) {
		t.Fatal("evaluator-out was written even though prospective-out pre-existed")
	}
}

func TestFindRecordRejectsAmbiguousPRNumber(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	dup := fx.record // same PR number, could represent a different repository in general
	if err := os.WriteFile(fx.input, []byte(fx.record+"\n"+dup+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := findRecord(fx.input, 42); err == nil {
		t.Fatal("expected ambiguous PR number to be rejected")
	}
}

func TestGenerateCaseIDIsOpaqueAndDeterministic(t *testing.T) {
	id1 := GenerateCaseID("owner/repo", 15624, cutoff)
	id2 := GenerateCaseID("owner/repo", 15624, cutoff)
	if id1 != id2 {
		t.Fatalf("case ID generation is not deterministic: %s vs %s", id1, id2)
	}
	if strings.Contains(id1, "15624") || strings.Contains(id1, "owner") || strings.Contains(id1, "repo") {
		t.Fatalf("case ID discloses input identity: %s", id1)
	}
	other := GenerateCaseID("owner/repo", 15625, cutoff)
	if id1 == other {
		t.Fatal("case IDs for different PRs collided")
	}
}
