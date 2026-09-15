package contamkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/model"
	"miner/internal/prospectiveexport"
)

var merged = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// fake is an in-memory Fetcher; every map miss is an error, which is how a build
// records an incomplete source.
type fake struct {
	messages map[string]string
	prsFor   map[string][]int
	bundles  map[int]*collector.RawPRBundle
	issues   map[int]*IssueThread
	prNums   map[int]bool // issue numbers that are really PRs
	own      *collector.RawPRBundle
	ownErr   error
}

func (f *fake) CommitMessage(_ context.Context, sha string) (string, error) {
	if m, ok := f.messages[sha]; ok {
		return m, nil
	}
	return "", fmt.Errorf("unknown object")
}
func (f *fake) PullRequestsForCommit(_ context.Context, sha string) ([]int, error) {
	if n, ok := f.prsFor[sha]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("not cached and --offline")
}
func (f *fake) PullRequestBundle(_ context.Context, n int) (*collector.RawPRBundle, error) {
	if b, ok := f.bundles[n]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("HTTP 404")
}
func (f *fake) Issue(_ context.Context, n int) (*IssueThread, bool, error) {
	if f.prNums[n] {
		return nil, true, nil
	}
	if th, ok := f.issues[n]; ok {
		return th, false, nil
	}
	return nil, false, fmt.Errorf("HTTP 404")
}
func (f *fake) OwnBundle(int) (*collector.RawPRBundle, error) { return f.own, f.ownErr }

func comment(id int64, at time.Time, body string) *github.IssueComment {
	return &github.IssueComment{ID: github.Int64(id), Body: github.String(body), CreatedAt: &github.Timestamp{Time: at}, User: &github.User{Login: github.String("u")}, HTMLURL: github.String(fmt.Sprintf("https://x/c/%d", id))}
}

func positiveCase() Case {
	c := Case{SchemaVersion: "cohort-case/v3", SamplerLabel: "RISKY", Repository: "owner/repo", PR: 100}
	c.Record.Original.Number = 100
	c.Record.Original.Repository = "owner/repo"
	c.Record.Original.MergedAt = merged
	c.Record.Retrospective.StrongSignals = []model.StrongCorrectiveSignal{{SignalType: "FIXES_PR", SourceType: "COMMIT", SourceRef: shaA}}
	c.Record.Retrospective.MediumSignals = []model.MediumCorrectiveSignal{
		{SignalType: "SAME_FUNCTION_FIX", SourceType: "COMMIT", SourceRef: shaB},
		{SignalType: "SAME_FUNCTION_FIX", SourceType: "COMMIT", SourceRef: shaA}, // duplicate of the strong one
	}
	return c
}

func fullFake() *fake {
	own := &collector.RawPRBundle{
		PR:                     &github.PullRequest{Number: github.Int(100)},
		BundleVersion:          collector.BundleVersion,
		IssueCommentsComplete:  true,
		ReviewCommentsComplete: true,
		FetchedAt:              merged.AddDate(1, 0, 0),
		IssueComments: []*github.IssueComment{
			comment(1, merged.Add(-time.Hour), "pre-merge review chatter"),
			comment(2, merged.Add(48*time.Hour), "this broke warm restart, see CVE-2024-9999"),
		},
		ReviewComments: []*github.PullRequestComment{{ID: github.Int64(3), Body: github.String("post-merge inline"), CreatedAt: &github.Timestamp{Time: merged.Add(time.Hour)}}},
	}
	fixPR := &collector.RawPRBundle{
		PR:                     &github.PullRequest{Number: github.Int(200), Title: github.String("bgpd: fix crash after #100"), Body: github.String("Fixes #150 and cve-2024-0001."), HTMLURL: github.String("https://x/pull/200"), User: &github.User{Login: github.String("fixer")}},
		IssueCommentsComplete:  true,
		ReviewCommentsComplete: true,
		IssueComments:          []*github.IssueComment{comment(9, merged.Add(72*time.Hour), "LGTM")},
	}
	return &fake{
		messages: map[string]string{shaA: "bgpd: fix crash\n\nFixes: " + shaA[:12] + "\nCloses #150\nRelated #300", shaB: "bgpd: tidy up"},
		prsFor:   map[string][]int{shaA: {200, 100}, shaB: {200}},
		bundles:  map[int]*collector.RawPRBundle{200: fixPR},
		issues:   map[int]*IssueThread{150: {Number: 150, Title: "crash on warm restart", Body: "trace", HTMLURL: "https://x/issues/150", Complete: true, Comments: []IssueComment{{Body: "confirmed", CreatedAt: merged.Add(24 * time.Hour)}}}},
		prNums:   map[int]bool{300: true},
		own:      own,
	}
}

func TestBuildAssemblesKeysForAPositive(t *testing.T) {
	k, err := Build(context.Background(), positiveCase(), fullFake(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !k.Complete() {
		t.Fatalf("expected complete, got %v", k.Provenance.Incomplete)
	}
	if k.CaseID != prospectiveexport.GenerateCaseID("owner/repo", 100, merged) {
		t.Errorf("case id must match the bundle's: %s", k.CaseID)
	}
	if strings.Join(k.FixingSHAs, ",") != shaA+","+shaB {
		t.Errorf("fixing_shas = %v", k.FixingSHAs)
	}
	if len(k.FixingCommits) != 2 || k.FixingCommits[0].Tier != "strong" || k.FixingCommits[1].Tier != "medium" || k.FixingCommits[0].MatchedBy != "FIXES_PR" {
		t.Errorf("fixing_commits = %+v", k.FixingCommits)
	}
	if len(k.FixingPRNumbers) != 1 || k.FixingPRNumbers[0] != 200 {
		t.Errorf("fixing_pr_numbers = %v (own PR must be excluded)", k.FixingPRNumbers)
	}
	if strings.Join(k.CVEIDs, ",") != "CVE-2024-0001,CVE-2024-9999" {
		t.Errorf("cve_ids = %v", k.CVEIDs)
	}
	kinds := map[string]int{}
	bodies := map[string]bool{}
	for _, d := range k.Discussion {
		kinds[d.Kind]++
		bodies[d.Body] = true
	}
	want := map[string]int{"fixing_commit_message": 2, "fixing_pr_title": 1, "fixing_pr_body": 1, "fixing_pr_comment": 1, "issue_title": 1, "issue_body": 1, "issue_comment": 1, "own_pr_comment": 1, "own_pr_review_comment": 1}
	for kind, n := range want {
		if kinds[kind] != n {
			t.Errorf("discussion kind %s: %d, want %d", kind, kinds[kind], n)
		}
	}
	if bodies["pre-merge review chatter"] {
		t.Errorf("a comment before the cutoff is not post-merge discussion")
	}
	if !bodies["this broke warm restart, see CVE-2024-9999"] || !bodies["confirmed"] {
		t.Errorf("post-merge bodies missing: %v", bodies)
	}
	// #300 is a PR reference that no fixing commit belongs to: not an issue thread.
	for _, d := range k.Discussion {
		if strings.Contains(d.Source, "300") {
			t.Errorf("PR reference treated as an issue: %+v", d)
		}
	}
	if k.Provenance.SamplerLabel != "RISKY" || k.Provenance.CohortExport != "cohort-case/v3" || !strings.Contains(k.Provenance.Notes, "as of the collect-time bundle") {
		t.Errorf("provenance = %+v", k.Provenance)
	}
	// Deterministic: a second build (same inputs) differs only in produced_at.
	k2, _ := Build(context.Background(), positiveCase(), fullFake(), Options{})
	k.Provenance.ProducedAt, k2.Provenance.ProducedAt = "", ""
	a, _ := json.Marshal(k)
	b, _ := json.Marshal(k2)
	if string(a) != string(b) {
		t.Errorf("build is not deterministic")
	}
}

func TestBuildNegativeHasEmptyFixesAndOwnDiscussion(t *testing.T) {
	c := positiveCase()
	c.SamplerLabel = "CLEAN"
	c.Record.Retrospective = model.RetrospectiveEvidence{}
	k, err := Build(context.Background(), c, fullFake(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(k.FixingSHAs) != 0 || len(k.FixingPRNumbers) != 0 || len(k.FixingCommits) != 0 {
		t.Errorf("negative must have empty fixing lists: %+v", k)
	}
	if len(k.Discussion) != 2 || !k.Complete() {
		t.Errorf("negative discussion = %+v incomplete=%v", k.Discussion, k.Provenance.Incomplete)
	}
	if strings.Join(k.CVEIDs, ",") != "CVE-2024-9999" {
		t.Errorf("cve from own post-merge comment expected: %v", k.CVEIDs)
	}
}

func TestBuildRecordsEveryIncompleteSourceAndStillWrites(t *testing.T) {
	f := fullFake()
	f.messages = map[string]string{}             // mirror lacks both commits
	f.prsFor = map[string][]int{shaA: {200}}     // shaB unresolvable
	f.bundles[200].IssueCommentsComplete = false // truncated fixing PR comments
	f.own.IssueCommentsComplete = false          // old own bundle
	delete(f.issues, 150)                        // referenced issue unfetchable
	k, err := Build(context.Background(), positiveCase(), f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if k.Complete() {
		t.Fatal("expected incomplete")
	}
	joined := strings.Join(k.Provenance.Incomplete, "\n")
	for _, want := range []string{"commit message " + shaA, "commit message " + shaB, "pull requests for commit " + shaB, "fixing PR #200 comments", "own PR comments", "referenced issue #150"} {
		if !strings.Contains(joined, want) {
			t.Errorf("incomplete list lacks %q:\n%s", want, joined)
		}
	}
	// What could be established still is.
	if len(k.FixingPRNumbers) != 1 || len(k.FixingSHAs) != 2 {
		t.Errorf("partial keys lost: %+v", k)
	}
}

func TestBuildRejectsShortSHAsAndMissingCutoff(t *testing.T) {
	c := positiveCase()
	c.Record.Retrospective.StrongSignals[0].SourceRef = "abc123"
	k, err := Build(context.Background(), c, fullFake(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sha := range k.FixingSHAs {
		if len(sha) != 40 {
			t.Errorf("non-full SHA in fixing_shas: %q", sha)
		}
	}
	if k.Complete() || !strings.Contains(strings.Join(k.Provenance.Incomplete, ""), "not a full SHA") {
		t.Errorf("short SHA must be recorded as incomplete: %v", k.Provenance.Incomplete)
	}
	c = positiveCase()
	c.Record.Original.MergedAt = time.Time{}
	if _, err := Build(context.Background(), c, fullFake(), Options{}); err == nil {
		t.Errorf("missing merged_at and no --cutoff must fail")
	}
	k, _ = Build(context.Background(), c, fullFake(), Options{Cutoff: merged})
	if k == nil || k.CaseID != prospectiveexport.GenerateCaseID("owner/repo", 100, merged) {
		t.Errorf("explicit cutoff should drive the case id")
	}
}

func TestWriteAllIsImmutableAndAtomic(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "keys")
	k, _ := Build(context.Background(), positiveCase(), fullFake(), Options{})
	if err := WriteAll(out, []*Keys{k}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, k.CaseID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAll(out, []*Keys{k}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write must be refused: %v", err)
	}
	dup := filepath.Join(dir, "dup")
	if err := WriteAll(dup, []*Keys{k, k}); err == nil || !strings.Contains(err.Error(), "two cases") {
		t.Errorf("duplicate case id must be refused: %v", err)
	}
	if _, err := os.Stat(dup); !os.IsNotExist(err) {
		t.Errorf("failed write left a directory")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary directories left behind: %v", entries)
	}
}

func TestReadCasesRejectsNonCohortInput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.jsonl")
	os.WriteFile(p, []byte(`{"schema_version":"1.1.0","original":{"number":1}}`+"\n"), 0644)
	if _, err := ReadCases(p); err == nil || !strings.Contains(err.Error(), "not a cohort case") {
		t.Errorf("candidates file accepted as cohort: %v", err)
	}
	os.WriteFile(p, []byte(`{"schema_version":"cohort-case/v3","sampler_label":"CLEAN","repository":"o/r","pr":7,"record":{"original":{"number":7}}}`+"\n"), 0644)
	cases, err := ReadCases(p)
	if err != nil || len(cases) != 1 || cases[0].PR != 7 || cases[0].Record.Original.Number != 7 {
		t.Errorf("cases = %+v err = %v", cases, err)
	}
}
