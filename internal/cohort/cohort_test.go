package cohort

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"miner/internal/model"
)

var mergedAt = time.Date(2024, 4, 10, 5, 22, 26, 0, time.UTC)

func record(pr int, subsystemFiles []string, strong, medium int, score float64) model.PRCandidateRecord {
	var files []model.ChangedFile
	for _, p := range subsystemFiles {
		files = append(files, model.ChangedFile{Path: p, Status: "modified", Additions: 7, Deletions: 3})
	}
	rec := model.PRCandidateRecord{}
	rec.Original.Number = pr
	rec.Original.Repository = "owner/repo"
	rec.Original.Title = "change in " + strings.Join(subsystemFiles, ", ")
	rec.Original.MergedAt = mergedAt
	rec.Original.ChangedFiles = files
	rec.Stateful.Score = score
	rec.Stateful.Category = "HIGH"
	rec.Retrospective.SummaryRankScore = score
	for i := 0; i < strong; i++ {
		rec.Retrospective.StrongSignals = append(rec.Retrospective.StrongSignals, model.StrongCorrectiveSignal{})
	}
	for i := 0; i < medium; i++ {
		rec.Retrospective.MediumSignals = append(rec.Retrospective.MediumSignals, model.MediumCorrectiveSignal{})
	}
	return rec
}

func TestSubsystemGroupingUsesTheDominantTopLevelPath(t *testing.T) {
	cases := []struct {
		files []string
		want  string
	}{
		{[]string{"bgpd/a.c", "bgpd/b.c", "lib/c.c"}, "bgpd"},
		{[]string{"lib/x.c"}, "lib"},
		{[]string{"README.md"}, "README.md"},
		{nil, "(none)"},
		// A tie resolves on name, so the report does not reorder between runs.
		{[]string{"zebra/a.c", "bgpd/b.c"}, "bgpd"},
		// A generic container is stepped through, or every Go path groups as "internal".
		{[]string{"internal/paging/a.go", "internal/paging/b.go"}, "internal/paging"},
		{[]string{"cmd/bench/main.go"}, "cmd/bench"},
		{[]string{"src/parser/lex.rs"}, "src/parser"},
		// ...but only when there is something beyond it to step to.
		{[]string{"internal/single.go"}, "internal"},
		// lib is a real subsystem in some C projects, so it is never stepped through.
		{[]string{"lib/table.c", "lib/vty.c"}, "lib"},
	}
	for _, c := range cases {
		var files []model.ChangedFile
		for _, p := range c.files {
			files = append(files, model.ChangedFile{Path: p})
		}
		if got := subsystemOf(files); got != c.want {
			t.Errorf("subsystemOf(%v) = %q, want %q", c.files, got, c.want)
		}
	}
}

// TestReportGroupsBySubsystemNotByRank is the property that makes the report useful
// for selection: a globally ranked list would put the three bgpd cases first and
// bury the only other subsystem, which is how a cohort ends up unable to
// discriminate.
func TestReportPluralizesSubsystemCount(t *testing.T) {
	one := Analyze(context.Background(), []model.PRCandidateRecord{record(1, []string{"bgpd/a.c"}, 1, 0, 0.5)}, "")
	if md := RenderMarkdown(one, false); !strings.Contains(md, "across 1 subsystem  ") {
		t.Errorf("singular count not pluralized correctly:\n%s", md)
	}
	two := Analyze(context.Background(), []model.PRCandidateRecord{
		record(1, []string{"bgpd/a.c"}, 1, 0, 0.5),
		record(2, []string{"zebra/b.c"}, 1, 0, 0.5),
	}, "")
	if md := RenderMarkdown(two, false); !strings.Contains(md, "across 2 subsystems") {
		t.Errorf("plural count wrong:\n%s", md)
	}
}

func TestReportGroupsBySubsystemNotByRank(t *testing.T) {
	records := []model.PRCandidateRecord{
		record(1, []string{"bgpd/a.c"}, 3, 0, 0.9),
		record(2, []string{"bgpd/b.c"}, 2, 0, 0.8),
		record(3, []string{"bgpd/c.c"}, 1, 0, 0.7),
		record(4, []string{"zebra/d.c"}, 0, 1, 0.2),
	}
	rows := Analyze(context.Background(), records, "")
	SortRows(rows)
	var order []int
	for _, r := range rows {
		order = append(order, r.PR)
	}
	// bgpd sorts before zebra, and within bgpd the strongest evidence leads.
	want := []int{1, 2, 3, 4}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("row order = %v, want %v", order, want)
		}
	}
	md := RenderMarkdown(rows, false)
	bgpd, zebra := strings.Index(md, "## `bgpd`"), strings.Index(md, "## `zebra`")
	if bgpd < 0 || zebra < 0 {
		t.Fatalf("report is missing subsystem headings:\n%s", md)
	}
	if !strings.Contains(md, "Evaluator-facing") {
		t.Error("report does not carry its evaluator-facing warning")
	}
	if !strings.Contains(md, "not run (pass `--repo` to enable)") {
		t.Error("report claims a precheck it did not run")
	}
}

func TestSignalTierReportsStrongestPresent(t *testing.T) {
	cases := []struct {
		strong, medium, weak int
		want                 string
	}{
		{2, 5, 9, "strong x2"},
		{0, 3, 9, "medium x3"},
		{0, 0, 4, "weak x4"},
		{0, 0, 0, "none"},
	}
	for _, c := range cases {
		r := Row{Strong: c.strong, Medium: c.medium, Weak: c.weak}
		if got := r.SignalTier(); got != c.want {
			t.Errorf("SignalTier(%d,%d,%d) = %q, want %q", c.strong, c.medium, c.weak, got, c.want)
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2024-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2024-01-01T00:00:00Z")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

// buildRepo creates base and head commits, optionally adding a symlink to the head
// tree so the inadmissible-entry path can be exercised.
func buildRepo(t *testing.T, withSymlink bool) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "user.email", "t@example.com")
	if err := os.MkdirAll(filepath.Join(dir, "bgpd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bgpd", "fsm.c"), []byte("int main(void){return 0;}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")
	base = runGit(t, dir, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "bgpd", "fsm.c"), []byte("int main(void){return 1;}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if withSymlink {
		if err := os.Symlink("fsm.c", filepath.Join(dir, "bgpd", "alias.c")); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "head")
	head = runGit(t, dir, "rev-parse", "HEAD")
	return dir, base, head
}

func TestProbeReportsSnapshotSizeWithoutWriting(t *testing.T) {
	dir, base, head := buildRepo(t, false)
	rec := record(1, []string{"bgpd/fsm.c"}, 1, 0, 0.9)
	rec.Original.BaseSHA, rec.Original.HeadSHA = base, head

	before := treeFileCount(t, dir)
	rows := Analyze(context.Background(), []model.PRCandidateRecord{rec}, dir)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if !r.Exportable {
		t.Fatalf("clean case reported as blocked: %s", r.ExportBlocker)
	}
	if r.SnapshotFiles != 1 || r.SnapshotBytes <= 0 {
		t.Fatalf("snapshot not sized: files=%d bytes=%d", r.SnapshotFiles, r.SnapshotBytes)
	}
	if after := treeFileCount(t, dir); after != before {
		t.Fatalf("the precheck wrote to the repository: %d files before, %d after", before, after)
	}
}

// treeFileCount counts working-tree files, to assert the precheck is read-only.
func treeFileCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	if err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProbeBlocksCasesThatCannotExport(t *testing.T) {
	symlinkDir, symBase, symHead := buildRepo(t, true)
	cleanDir, cleanBase, _ := buildRepo(t, false)

	cases := map[string]struct {
		dir, base, head string
		wantFragment    string
	}{
		"symlink in the head tree": {symlinkDir, symBase, symHead, "symlink"},
		"missing commit":           {cleanDir, cleanBase, strings.Repeat("b", 40), "merge-base"},
		"no identity in record":    {cleanDir, "", "", "base_sha"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := record(1, []string{"bgpd/fsm.c"}, 1, 0, 0.9)
			rec.Original.BaseSHA, rec.Original.HeadSHA = c.base, c.head
			rows := Analyze(context.Background(), []model.PRCandidateRecord{rec}, c.dir)
			if rows[0].Exportable {
				t.Fatalf("%s was reported as exportable", name)
			}
			if !strings.Contains(rows[0].ExportBlocker, c.wantFragment) {
				t.Fatalf("blocker %q does not mention %q", rows[0].ExportBlocker, c.wantFragment)
			}
		})
	}
}

func TestCSVRoundTripsEveryRow(t *testing.T) {
	records := []model.PRCandidateRecord{
		record(1, []string{"bgpd/a.c"}, 1, 0, 0.9),
		record(2, []string{"zebra/b.c"}, 0, 2, 0.4),
	}
	rows := Analyze(context.Background(), records, "")
	path := filepath.Join(t.TempDir(), "out.csv")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RenderCSV(f, rows); err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header plus 2 rows, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[0], "pr,repository,subsystem,") {
		t.Fatalf("unexpected header: %s", lines[0])
	}
}

// TestVerifyReportsUnvalidatedAsUnvalidated is the property that keeps this pass
// honest: with no engine checkout wired in, a case that exported cleanly must not
// read as though it had been validated.
func TestVerifyReportsUnvalidatedAsUnvalidated(t *testing.T) {
	dir, base, head := buildRepo(t, false)
	rec := record(42, []string{"bgpd/fsm.c"}, 1, 0, 0.9)
	rec.Original.BaseSHA, rec.Original.HeadSHA = base, head

	work := t.TempDir()
	cache := filepath.Join(work, "cache")
	if err := os.MkdirAll(filepath.Join(cache, "github", "owner_repo", "prs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeCache(t, cachePath(cache, "owner/repo", 42), base, head)

	results, err := Verify(context.Background(), VerifyOptions{
		Records:  []model.PRCandidateRecord{rec},
		PRs:      []int{42},
		Repo:     dir,
		CacheDir: cache,
		OutDir:   filepath.Join(work, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Exported || r.Err != "" {
		t.Fatalf("export failed: %+v", r)
	}
	if r.Validated {
		t.Fatal("case reports Validated with no engine checkout supplied")
	}
	if r.ValidationRun {
		t.Fatal("case reports validation as run when it was not")
	}
	if !strings.Contains(r.BenchCommand, "-bundle") || !strings.Contains(r.BenchCommand, r.CaseID) {
		t.Fatalf("bench command is not runnable: %q", r.BenchCommand)
	}
	// The bundle must actually be on disk in the engine's layout.
	for _, rel := range []string{"reviewer/diff.patch", "reviewer/metadata.json", "control/manifest.json", "control/checksums.sha256"} {
		if _, err := os.Stat(filepath.Join(r.BundlePath, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("verified bundle is missing %s: %v", rel, err)
		}
	}
	md := RenderVerification(results, false)
	if !strings.Contains(md, "NOT run") {
		t.Errorf("summary does not say validation was skipped:\n%s", md)
	}
	if !strings.Contains(md, r.BenchCommand) {
		t.Errorf("summary omits the command needed to finish the job:\n%s", md)
	}
}

func TestVerifyReportsMissingAndUnexportableCases(t *testing.T) {
	dir, base, head := buildRepo(t, false)
	rec := record(42, []string{"bgpd/fsm.c"}, 1, 0, 0.9)
	rec.Original.BaseSHA, rec.Original.HeadSHA = base, head

	work := t.TempDir()
	cache := filepath.Join(work, "cache")
	if err := os.MkdirAll(filepath.Join(cache, "github", "owner_repo", "prs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeCache(t, cachePath(cache, "owner/repo", 42), base, head)

	results, err := Verify(context.Background(), VerifyOptions{
		Records:  []model.PRCandidateRecord{rec},
		PRs:      []int{42, 999}, // 999 is not in the input at all
		Repo:     dir,
		CacheDir: cache,
		OutDir:   filepath.Join(work, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].OK() != true {
		t.Fatalf("valid case reported as blocked: %+v", results[0])
	}
	if results[1].OK() {
		t.Fatal("a PR absent from the input was reported as cohort-ready")
	}
	if !strings.Contains(results[1].Err, "not present") {
		t.Fatalf("unhelpful error for a missing PR: %q", results[1].Err)
	}
}

// writeCache writes a provider bundle consistent with the record, so the exporter's
// cross-identity check passes.
func writeCache(t *testing.T, path, base, head string) {
	t.Helper()
	bundle := map[string]any{
		"pull_request": map[string]any{
			"number":     42,
			"title":      "cutoff title",
			"body":       "cutoff description",
			"updated_at": mergedAt.Add(-time.Hour).Format(time.RFC3339),
			"merged_at":  mergedAt.Format(time.RFC3339),
			"commits":    0,
			"base": map[string]any{
				"ref":  "main",
				"sha":  base,
				"repo": map[string]any{"full_name": "owner/repo"},
			},
			"head": map[string]any{"sha": head},
		},
		"fetched_at": mergedAt.AddDate(1, 0, 0).Format(time.RFC3339),
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCachePathMatchesCollectLayout(t *testing.T) {
	got := cachePath("/data/cache", "FRRouting/frr", 15624)
	want := filepath.Join("/data/cache", "github", "FRRouting_frr", "prs", "pr_15624.json")
	if got != want {
		t.Fatalf("cachePath = %q, want %q", got, want)
	}
}

// TestFindValidationReportReadsTheEngineVerdict covers the failure path through this
// package's own code. A bench run can exit non-zero for reasons that have nothing to
// do with admissibility (no fixture scenario, no adapter, no credentials), so the
// validator's report is consulted rather than the process exit code — and a genuine
// boundary rejection has to surface as one.
func TestFindValidationReportReadsTheEngineVerdict(t *testing.T) {
	root := t.TempDir()
	caseID := "case-0123456789abcdef"
	dir := filepath.Join(root, "20260909T000000Z-"+caseID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "boundary-validation.json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"schema_version":"boundary-validation/v1","result":"pass","checks":[]}`)
	rep, err := findValidationReport(root, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != "pass" {
		t.Fatalf("expected pass, got %q", rep.Result)
	}

	write(`{"result":"fail","violations":[
	  {"rule":"oracle_shaped_content","path":"reviewer/repository/oracle.json","detail":"..."},
	  {"rule":"checksum_manifest","path":"reviewer/repository/oracle.json","detail":"..."}]}`)
	rep, err = findValidationReport(root, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Result != "fail" || len(rep.Violations) != 2 {
		t.Fatalf("failing report not parsed: %+v", rep)
	}
	if rep.Violations[0].Rule != "oracle_shaped_content" || rep.Violations[0].Path == "" {
		t.Fatalf("violation detail lost: %+v", rep.Violations[0])
	}

	// A report for a different case must not be picked up.
	if _, err := findValidationReport(root, "case-ffffffffffffffff"); err == nil {
		t.Fatal("a report belonging to another case was accepted")
	}
	// An absent report is an error, never a silent pass.
	if _, err := findValidationReport(t.TempDir(), caseID); err == nil {
		t.Fatal("a missing report was not reported as an error")
	}
}

// TestCaseResultOKTreatsUnvalidatedHonestly pins the distinction the summary table
// depends on: not-yet-checked and checked-and-failed must never collapse together.
func TestCaseResultOKTreatsUnvalidatedHonestly(t *testing.T) {
	cases := []struct {
		name string
		r    CaseResult
		want bool
	}{
		{"exported, validation not attempted", CaseResult{Exported: true}, true},
		{"exported and validated", CaseResult{Exported: true, ValidationRun: true, Validated: true}, true},
		{"exported but validation failed", CaseResult{Exported: true, ValidationRun: true}, false},
		{"export failed", CaseResult{Err: "boom"}, false},
	}
	for _, c := range cases {
		if got := c.r.OK(); got != c.want {
			t.Errorf("%s: OK() = %v, want %v", c.name, got, c.want)
		}
	}

	// A failed validation must be visible as a failure in the rendered summary.
	md := RenderVerification([]CaseResult{{PR: 7, Exported: true, ValidationRun: true, Err: "boundary validation failed: oracle_shaped_content (reviewer/x)"}}, true)
	if !strings.Contains(md, "**FAIL**") || !strings.Contains(md, "oracle_shaped_content") {
		t.Errorf("failed validation is not visible in the summary:\n%s", md)
	}
	if !strings.Contains(md, "0 of 1 cases usable") {
		t.Errorf("summary miscounts a failed case:\n%s", md)
	}
}
