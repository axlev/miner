package historybaseline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miner/internal/gitx"
	"miner/internal/model"
)

// fixture builds a synthetic mirror with three subsystems whose 24-month history
// is known exactly, then a "PR" merged at cutoff. Nothing here derives from any
// real repository.
type fixture struct {
	dir    string
	cutoff time.Time
	shas   map[string]string // label -> sha
}

func (f *fixture) git(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	cmd.Env = append(append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes n lines to <sub>/<name>.c and commits with the given author and
// committer dates.
func (f *fixture) commit(t *testing.T, sub, name string, lines int, msg string, author, committer time.Time) string {
	t.Helper()
	os.MkdirAll(filepath.Join(f.dir, sub), 0755)
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "line %d of %s at %s\n", i, name, committer.Format(time.RFC3339))
	}
	os.WriteFile(filepath.Join(f.dir, sub, name+".c"), []byte(sb.String()), 0644)
	f.git(t, nil, "add", ".")
	f.git(t, []string{"GIT_AUTHOR_DATE=" + author.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + committer.Format(time.RFC3339)}, "commit", "-q", "-m", msg)
	return f.git(t, nil, "rev-parse", "HEAD")
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{dir: t.TempDir(), cutoff: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), shas: map[string]string{}}
	f.git(t, nil, "init", "-q", "-b", "main")
	inWindow := func(monthsBack int) time.Time { return f.cutoff.AddDate(0, -monthsBack, 0) }
	// Before the window: must not count.
	f.commit(t, "bgpd", "old", 5, "bgpd: ancient", inWindow(30), inWindow(30))
	// bgpd: 25 commits, one Fixes: pointing at an earlier bgpd commit -> 1 defect via fixed_commit.
	var bgpdTarget string
	for i := 0; i < 24; i++ {
		sha := f.commit(t, "bgpd", fmt.Sprintf("f%d", i), 10, fmt.Sprintf("bgpd: change %d", i), inWindow(20-i%18), inWindow(20-i%18))
		if i == 3 {
			bgpdTarget = sha
		}
	}
	f.shas["bgpd_fix"] = f.commit(t, "bgpd", "fix", 4, "bgpd: fix crash\n\nFixes: "+bgpdTarget[:12], inWindow(2), inWindow(2))
	// zebra: 22 commits, big churn, three corrective commits with no resolvable target -> self.
	for i := 0; i < 19; i++ {
		f.commit(t, "zebra", fmt.Sprintf("z%d", i), 40, fmt.Sprintf("zebra: churn %d", i), inWindow(18-i%12), inWindow(18-i%12))
	}
	for i := 0; i < 3; i++ {
		f.shas[fmt.Sprintf("zebra_fix%d", i)] = f.commit(t, "zebra", fmt.Sprintf("zf%d", i), 3, fmt.Sprintf("zebra: fix leak %d\n\nFixes: 0123456789ab", i), inWindow(6-i), inWindow(6-i))
	}
	// lib: 5 commits -> low activity.
	for i := 0; i < 5; i++ {
		f.commit(t, "lib", fmt.Sprintf("l%d", i), 2, fmt.Sprintf("lib: small %d", i), inWindow(4), inWindow(4))
	}
	// Authored inside the window, committed after cutoff: must not count anywhere.
	f.commit(t, "bgpd", "late", 100, "bgpd: fix authored early committed late\n\nFixes: "+bgpdTarget[:12], inWindow(1), f.cutoff.Add(48*time.Hour))
	// The "merge commit": its first parent is the branch as it stood after the late commit.
	f.shas["merge"] = f.git(t, nil, "rev-parse", "HEAD")
	return f
}

func caseFor(f *fixture, pr int, files []model.ChangedFile) Case {
	var c Case
	c.SchemaVersion = "cohort-case/v3"
	c.Repository = "owner/repo"
	c.PR = pr
	c.Record.Original.Number = pr
	c.Record.Original.MergedAt = f.cutoff
	c.Record.Original.MergeCommitSHA = f.shas["merge"] // ^1 of a fake merge: we use HEAD itself as "merge" so ^1 is its parent
	c.Record.Original.ChangedFiles = files
	return c
}

func find(r *Result, name string) *Subsystem {
	for i := range r.Detail {
		if r.Detail[i].Name == name {
			return &r.Detail[i]
		}
	}
	return nil
}

func TestBaselineFeaturesAndVerdict(t *testing.T) {
	f := newFixture(t)
	repo := gitx.OpenRepository(filepath.Join(f.dir, ".git"))
	c := caseFor(f, 1, []model.ChangedFile{{Path: "zebra/rib.c", Additions: 30, Deletions: 5}, {Path: "bgpd/x.c", Additions: 3}})
	r, err := Build(context.Background(), repo, c, Options{InputSHA256: strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	// The walk starts at merge^1, which excludes the "late" commit and everything after cutoff.
	if r.MergeBase == "" || r.Reason != "" {
		t.Fatalf("unexpected reason %q merge_base %q", r.Reason, r.MergeBase)
	}
	bgpd, zebra, lib := find(r, "bgpd"), find(r, "zebra"), find(r, "lib")
	if bgpd == nil || zebra == nil || lib == nil {
		t.Fatalf("detail = %+v", r.Detail)
	}
	if bgpd.Commits != 25 || bgpd.Defects != 1 || zebra.Commits != 22 || zebra.Defects != 3 || lib.Commits != 5 {
		t.Errorf("features: bgpd %+v zebra %+v lib %+v", *bgpd, *zebra, *lib)
	}
	if !lib.LowActivity || lib.Score != nil {
		t.Errorf("lib must be low_activity and unscored: %+v", *lib)
	}
	if zebra.ChurnLines <= bgpd.ChurnLines {
		t.Errorf("zebra churn %d should exceed bgpd %d", zebra.ChurnLines, bgpd.ChurnLines)
	}
	// Attribution: bgpd's fix resolves to a bgpd commit; zebra's do not resolve -> self.
	byMode := map[string]int{}
	for _, d := range r.Defects {
		byMode[d.AttributedBy]++
		if d.SHA == f.shas["bgpd_fix"] && (d.AttributedBy != "fixed_commit" || d.Subsystem != "bgpd") {
			t.Errorf("bgpd fix attribution = %+v", d)
		}
	}
	if byMode["fixed_commit"] != 1 || byMode["self"] != 3 {
		t.Errorf("attribution modes = %v", byMode)
	}
	// Primary subsystem is zebra (35 lines vs 3); with two active subsystems zebra's
	// density (3/22) and churn both exceed bgpd's, so p = 1/2 -> tercile 2, not top.
	if r.Subsystem != "zebra" || r.Risky == nil || *r.Risky || r.Tercile != 2 {
		t.Errorf("verdict: subsystem %q risky %v tercile %d score %v", r.Subsystem, r.Risky, r.Tercile, r.Score)
	}
	if r.Rule.DateField != "committer" || r.Rule.MinCommits != 20 || r.Rule.WindowMonths != 24 {
		t.Errorf("rule not frozen: %+v", r.Rule)
	}
	if r.WalkCommits != 25+22+5 {
		t.Errorf("walk_commits = %d, want 52 (no pre-window, no post-cutoff)", r.WalkCommits)
	}
	// Two scored subsystems, and with two the top one sits at p = 1/2: no tercile 3,
	// which the case must say about itself.
	if r.ScoredSubsystems != 2 || r.Tercile3Count != 0 {
		t.Errorf("scored_subsystems=%d tercile3_count=%d, want 2/0", r.ScoredSubsystems, r.Tercile3Count)
	}
}

func TestBaselineTopTercileIsRiskyWithThreeActiveSubsystems(t *testing.T) {
	f := newFixture(t)
	// Promote lib to active with 20 quiet commits so three subsystems are scored:
	// bgpd (1 defect, mid churn), zebra (3 defects, high churn), lib (0 defects, low churn).
	for i := 0; i < 20; i++ {
		f.commit(t, "lib", fmt.Sprintf("q%d", i), 1, fmt.Sprintf("lib: quiet %d", i), f.cutoff.AddDate(0, -3, 0), f.cutoff.AddDate(0, -3, 0))
	}
	f.shas["merge"] = f.git(t, nil, "rev-parse", "HEAD")
	repo := gitx.OpenRepository(filepath.Join(f.dir, ".git"))
	for _, tc := range []struct {
		path    string
		risky   bool
		tercile int
	}{
		{"zebra/a.c", true, 3},
		{"bgpd/a.c", false, 2},
		{"lib/a.c", false, 1},
	} {
		c := caseFor(f, 2, []model.ChangedFile{{Path: tc.path, Additions: 1}})
		r, err := Build(context.Background(), repo, c, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if r.Risky == nil || *r.Risky != tc.risky || r.Tercile != tc.tercile {
			t.Errorf("%s: risky %v tercile %d (detail %+v)", tc.path, r.Risky, r.Tercile, r.Detail)
		}
		if r.ScoredSubsystems != 3 || r.Tercile3Count != 1 {
			t.Errorf("%s: scored_subsystems=%d tercile3_count=%d, want 3/1", tc.path, r.ScoredSubsystems, r.Tercile3Count)
		}
	}
}

func TestBaselineLowActivityAndUnresolvable(t *testing.T) {
	f := newFixture(t)
	repo := gitx.OpenRepository(filepath.Join(f.dir, ".git"))
	c := caseFor(f, 3, []model.ChangedFile{{Path: "lib/x.c", Additions: 9}})
	r, err := Build(context.Background(), repo, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Risky == nil || *r.Risky || !strings.Contains(r.Reason, "low_activity") || r.Score != nil {
		t.Errorf("low activity: risky %v reason %q score %v", r.Risky, r.Reason, r.Score)
	}
	c = caseFor(f, 4, []model.ChangedFile{{Path: "docs/new.md", Additions: 9}})
	r, _ = Build(context.Background(), repo, c, Options{})
	if r.Risky == nil || *r.Risky || !strings.Contains(r.Reason, "no commits in the window") {
		t.Errorf("untouched subsystem: %v %q", r.Risky, r.Reason)
	}
	c = caseFor(f, 5, []model.ChangedFile{{Path: "bgpd/x.c", Additions: 1}})
	c.Record.Original.MergeCommitSHA = strings.Repeat("f", 40)
	r, _ = Build(context.Background(), repo, c, Options{})
	if r.Risky != nil || !strings.Contains(r.Reason, "does not resolve") {
		t.Errorf("unresolvable merge: risky %v reason %q", r.Risky, r.Reason)
	}
	c.Record.Original.MergeCommitSHA = ""
	r, _ = Build(context.Background(), repo, c, Options{})
	if r.Risky != nil || !strings.Contains(r.Reason, "no merge_commit_sha") {
		t.Errorf("missing merge sha: %v %q", r.Risky, r.Reason)
	}
}

func TestTercileTiesFallLower(t *testing.T) {
	scores := []float64{0.2, 0.5, 0.5, 0.9, 0.9, 0.9}
	// Strictly-below fractions: 0.2 -> 0 (tercile 1); 0.5 -> 1/6 (tercile 1);
	// 0.9 -> 3/6 = 0.5 (tercile 2). A three-way tie at the top of six therefore
	// reaches only the second tercile: ties never promote, they fall lower.
	if tercile(scores, 0.2) != 1 || tercile(scores, 0.5) != 1 || tercile(scores, 0.9) != 2 {
		t.Errorf("terciles: %d %d %d", tercile(scores, 0.2), tercile(scores, 0.5), tercile(scores, 0.9))
	}
	// Distinct scores split cleanly: 0.1 -> 1, 0.5 -> 2, 0.9 -> 3.
	if d := []float64{0.1, 0.5, 0.9}; tercile(d, 0.1) != 1 || tercile(d, 0.5) != 2 || tercile(d, 0.9) != 3 {
		t.Errorf("distinct terciles wrong")
	}
	// Two equal top scores among three: p = 1/3 for both -> tercile 2, so a tie at the
	// top boundary never promotes both to 3.
	if tercile([]float64{0.1, 0.7, 0.7}, 0.7) != 2 {
		t.Errorf("tie at the top must fall lower")
	}
	if p := percentile([]float64{1, 2, 2, 3}, 2); p != 0.5 {
		t.Errorf("percentile = %v, want 0.5", p)
	}
}

func TestBaselineIsDeterministicAndImmutable(t *testing.T) {
	f := newFixture(t)
	repo := gitx.OpenRepository(filepath.Join(f.dir, ".git"))
	c := caseFor(f, 6, []model.ChangedFile{{Path: "zebra/a.c", Additions: 1}})
	a, _ := Build(context.Background(), repo, c, Options{InputSHA256: "x"})
	b, _ := Build(context.Background(), repo, c, Options{InputSHA256: "x"})
	a.Provenance.ProducedAt, b.Provenance.ProducedAt = "", ""
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Errorf("not deterministic")
	}
	out := filepath.Join(t.TempDir(), "h")
	if err := WriteAll(out, []*Result{a}); err != nil {
		t.Fatal(err)
	}
	if err := WriteAll(out, []*Result{a}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second write must be refused: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, a.CaseID+".json"))
	var top map[string]any
	json.Unmarshal(raw, &top)
	for _, k := range []string{"schema_version", "case_id", "risky", "score", "subsystem", "tercile", "rule", "provenance"} {
		if _, ok := top[k]; !ok {
			t.Errorf("contract key %q missing", k)
		}
	}
}
