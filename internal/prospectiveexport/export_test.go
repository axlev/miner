package prospectiveexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/gitx"
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
	if m.SchemaVersion != MetadataSchemaVersion {
		t.Fatalf("reviewer metadata schema_version is %q, want %q", m.SchemaVersion, MetadataSchemaVersion)
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
	cases := [][]byte{
		[]byte(`{"schema_version":"reviewer-metadata/v1","repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z","new_provider_field":"FUTURE_ONLY"}`),
		[]byte(`{"schema_version":"reviewer-metadata/v1","repository":{"raw_provider_payload":true},"cutoff_timestamp":"2024-04-10T05:22:26Z"}`),
		[]byte(`{"schema_version":"reviewer-metadata/v1","repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z","title":"future-sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`),
		[]byte(`{"repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z"}`),
		[]byte(`{"schema_version":"reviewer-metadata/v2","repository":"o/r","cutoff_timestamp":"2024-04-10T05:22:26Z"}`),
	}
	for i, data := range cases {
		forbidden := []string(nil)
		if i == 2 {
			forbidden = []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		}
		if err := ValidateNormalized(data, cutoff, forbidden, MetadataSchemaVersion); err == nil {
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
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "nested.txt"), []byte("nested\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base modified by the change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "feature.txt", "pkg/nested.txt", "base.txt")
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

// bundlePath joins a bundle-root-relative slash path onto root.
func bundlePath(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

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
	a, _ := os.ReadFile(bundlePath(prospOuts[0], "reviewer/metadata.json"))
	b, _ := os.ReadFile(bundlePath(prospOuts[1], "reviewer/metadata.json"))
	if string(a) != string(b) {
		t.Fatal("normalized metadata differs")
	}
	manifestA, _ := os.ReadFile(bundlePath(prospOuts[0], "control/manifest.json"))
	manifestB, _ := os.ReadFile(bundlePath(prospOuts[1], "control/manifest.json"))
	if string(manifestA) != string(manifestB) {
		t.Fatal("manifest differs across identical runs; auto-generated case ID should be deterministic")
	}
	sumsA, _ := os.ReadFile(bundlePath(prospOuts[0], "control/checksums.sha256"))
	sumsB, _ := os.ReadFile(bundlePath(prospOuts[1], "control/checksums.sha256"))
	if string(sumsA) != string(sumsB) {
		t.Fatal("checksum manifest is not deterministic")
	}
	for _, future := range []string{"FUTURE_ONLY_FIX", strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)} {
		if strings.Contains(string(a), future) {
			t.Fatalf("future value leaked into metadata: %s", future)
		}
	}
	patch, err := os.ReadFile(bundlePath(prospOuts[0], "reviewer/diff.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "feature.txt") {
		t.Fatalf("diff.patch missing expected content: %s", patch)
	}

	var m ControlManifest
	if err := json.Unmarshal(manifestA, &m); err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != ControlManifestSchemaVersion {
		t.Fatalf("unexpected schema_version: %s", m.SchemaVersion)
	}
	if m.SnapshotFormat != SnapshotFormat {
		t.Fatalf("unexpected snapshot_format: %s", m.SnapshotFormat)
	}
	if m.BaseCommit != fx.baseSHA {
		t.Fatalf("expected base_commit %s, got %s", fx.baseSHA, m.BaseCommit)
	}
	if m.CutoffCommit != fx.headSHA {
		t.Fatalf("expected cutoff_commit %s, got %s", fx.headSHA, m.CutoffCommit)
	}
	if strings.Contains(m.CaseID, "42") {
		t.Fatalf("auto-generated case ID appears to disclose PR number: %s", m.CaseID)
	}

	eval, _ := os.ReadFile(filepath.Join(evalOuts[0], "correlated-report.json"))
	if string(eval) != fx.record+"\n" || !strings.Contains(string(eval), "FUTURE_ONLY_FIX") {
		t.Fatal("evaluator report was altered")
	}
	evalEntries, err := os.ReadDir(evalOuts[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(evalEntries) != 2 {
		t.Fatalf("expected exactly correlated-report.json and metadata-export-audit.json; got %+v", evalEntries)
	}
}

// TestExportProducesEngineIngestibleLayout pins the layout, naming, and pinned
// schema versions the engine's ingest contract requires.
func TestExportProducesEngineIngestibleLayout(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	out := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: out, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err != nil {
		t.Fatal(err)
	}

	top, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range top {
		if !e.IsDir() {
			t.Fatalf("bundle root holds a non-directory entry %q", e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "control,reviewer" {
		t.Fatalf("bundle root is %v, want exactly control/ and reviewer/", names)
	}

	reviewer, err := os.ReadDir(filepath.Join(out, "reviewer"))
	if err != nil {
		t.Fatal(err)
	}
	names = names[:0]
	for _, e := range reviewer {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "diff.patch,metadata.json,repository" {
		t.Fatalf("reviewer/ is %v, want exactly the three allow-listed entries", names)
	}

	// The snapshot is a plain tree of regular files, with no Git metadata anywhere.
	if err := filepath.WalkDir(bundlePath(out, "reviewer/repository"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Git metadata names, spelled out here rather than referenced from a shared
		// table: this test asserts a property of the published bundle, and reusing a
		// production constant would make it pass whenever that constant was emptied.
		switch d.Name() {
		case ".git", ".gitmodules", "packed-refs", "HEAD", "ORIG_HEAD", "FETCH_HEAD",
			"MERGE_HEAD", "shallow", "objects", "refs", "reflogs", "worktrees", "alternates":
			t.Fatalf("snapshot carries Git metadata at %s", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			t.Fatalf("snapshot carries an irregular file at %s (%s)", path, info.Mode().Type())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var meta map[string]any
	raw, err := os.ReadFile(bundlePath(out, "reviewer/metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["schema_version"] != MetadataSchemaVersion {
		t.Fatalf("reviewer metadata schema_version is %v, want %q", meta["schema_version"], MetadataSchemaVersion)
	}
	for _, forbidden := range []string{"merged", "state", "merged_at", "closed_at", "fix_commit", "head_sha", "comparison_base_sha", "base_commit", "cutoff_commit"} {
		if _, ok := meta[forbidden]; ok {
			t.Fatalf("outcome-carrying field %q reached reviewer metadata", forbidden)
		}
	}
}

// TestChecksumsDescribeExactlyTheReviewerTree covers both directions the engine
// checks: an unlisted file and a listed-but-absent one are equally violations, and
// every digest must match the bytes on disk.
func TestChecksumsDescribeExactlyTheReviewerTree(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	out := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: out, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err != nil {
		t.Fatal(err)
	}

	sums, err := os.ReadFile(bundlePath(out, "control/checksums.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatalf("malformed checksum line %q", line)
		}
		if !strings.HasPrefix(parts[1], "reviewer/") {
			t.Fatalf("checksum path %q is not relative to the bundle root", parts[1])
		}
		// sha256sum format is digest, two spaces, path.
		if !strings.Contains(line, parts[0]+"  "+parts[1]) {
			t.Fatalf("checksum line %q is not in sha256sum format", line)
		}
		listed[parts[1]] = parts[0]
	}
	for _, required := range []string{"reviewer/diff.patch", "reviewer/metadata.json", "reviewer/repository/base.txt", "reviewer/repository/feature.txt", "reviewer/repository/pkg/nested.txt"} {
		if _, ok := listed[required]; !ok {
			t.Fatalf("checksum manifest omits %s", required)
		}
	}

	present := map[string]bool{}
	if err := filepath.WalkDir(filepath.Join(out, "reviewer"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		present[rel] = true
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if listed[rel] != sha256Hex(body) {
			t.Fatalf("%s: digest %s does not match the manifest's %s", rel, sha256Hex(body), listed[rel])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(present) != len(listed) {
		t.Fatalf("checksum manifest lists %d files but the reviewer tree holds %d", len(listed), len(present))
	}
}

// TestSnapshotCorrespondsToDiff is the check nothing downstream performs: applying
// reviewer/diff.patch to the base tree must reproduce reviewer/repository byte for
// byte. A snapshot that does not correspond to the diff sends reviewers to line
// numbers that fail citation validation after a stage has already been paid for.
func TestSnapshotCorrespondsToDiff(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	out := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: out, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	var manifest ControlManifest
	raw, err := os.ReadFile(bundlePath(out, "control/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	// Materialize the base_commit tree, apply the published patch to it, and compare.
	applied := filepath.Join(dir, "applied")
	repo := gitx.OpenRepository(fx.repoDir)
	if _, err := repo.MaterializeTree(context.Background(), manifest.BaseCommit, applied); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "apply", "--verbose", bundlePath(out, "reviewer/diff.patch"))
	cmd.Dir = applied
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("diff.patch does not apply to the base tree: %s: %v", outBytes, err)
	}

	want := treeContents(t, bundlePath(out, "reviewer/repository"))
	got := treeContents(t, applied)
	if len(want) != len(got) {
		t.Fatalf("snapshot has %d files, applying the diff to the base tree yields %d", len(want), len(got))
	}
	for path, body := range want {
		if got[path] != body {
			t.Fatalf("%s differs between the snapshot and the applied diff", path)
		}
	}
}

// treeContents reads a directory tree into a map of slash path to content.
func treeContents(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestExportRejectsUnmaterializableTreeEntries covers the entries that cannot be
// published as plain regular files. Skipping any of them would silently break the
// correspondence TestSnapshotCorrespondsToDiff asserts, so the export fails closed.
// retargetFixture re-points a fixture's correlated record and cache bundle at a new
// head commit, so identity cross-checks still agree after the test repo gains a commit.
func retargetFixture(t *testing.T, fx fixtureSet, head string) {
	t.Helper()
	record := strings.ReplaceAll(fx.record, fx.headSHA, head)
	if err := os.WriteFile(fx.input, []byte(record+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var b collector.RawPRBundle
	raw, _ := os.ReadFile(fx.cache)
	_ = json.Unmarshal(raw, &b)
	b.PR.Head.SHA = &head
	raw, _ = json.Marshal(b)
	if err := os.WriteFile(fx.cache, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestExportPublishesAdmissibleSymlinks covers the boundary engine-runner moved on
// 2026-09-10: an in-tree relative link is admissible and must survive into the bundle
// as a link, not be dereferenced. Dereferencing would put identical content at two
// paths, and a diff touching the target would then no longer reproduce the snapshot.
func TestExportPublishesAdmissibleSymlinks(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	if err := os.MkdirAll(filepath.Join(fx.repoDir, "shared"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.repoDir, "shared", "conf"), []byte("shared config\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// A link to a file, and a link to a directory: both shapes FRR actually uses.
	if err := os.Symlink("../shared/conf", filepath.Join(fx.repoDir, "pkg", "conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared", filepath.Join(fx.repoDir, "shared-alias")); err != nil {
		t.Fatal(err)
	}
	runGit(t, fx.repoDir, "add", "-A")
	runGit(t, fx.repoDir, "commit", "-q", "-m", "add shared config and links")
	retargetFixture(t, fx, runGit(t, fx.repoDir, "rev-parse", "HEAD"))

	out := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: out, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err != nil {
		t.Fatalf("an admissible in-tree symlink blocked the export: %v", err)
	}

	for _, rel := range []string{"reviewer/repository/pkg/conf", "reviewer/repository/shared-alias"} {
		info, err := os.Lstat(bundlePath(out, rel))
		if err != nil {
			t.Fatalf("%s missing from the snapshot: %v", rel, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s was dereferenced into a regular file; the link relationship was lost", rel)
		}
	}

	// The link must not appear in the checksum manifest: the engine builds its file
	// set from regular files only, so a listed link reads as "listed but absent".
	sums, err := os.ReadFile(bundlePath(out, "control/checksums.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"reviewer/repository/pkg/conf", "reviewer/repository/shared-alias"} {
		if strings.Contains(string(sums), rel) {
			t.Fatalf("%s was listed in control/checksums.sha256", rel)
		}
	}
	if !strings.Contains(string(sums), "reviewer/repository/shared/conf") {
		t.Fatal("the link's target is missing from the checksum manifest")
	}
}

func TestExportRejectsUnmaterializableTreeEntries(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"symlink escaping the tree": func(t *testing.T, dir string) {
			if err := os.Symlink("../outside.txt", filepath.Join(dir, "escape.txt")); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "escape.txt")
		},
		"absolute symlink": func(t *testing.T, dir string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(dir, "abs.txt")); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "abs.txt")
		},
		"dangling symlink": func(t *testing.T, dir string) {
			if err := os.Symlink("nonexistent.txt", filepath.Join(dir, "dangling.txt")); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "dangling.txt")
		},
		"symlink chain": func(t *testing.T, dir string) {
			if err := os.Symlink("base.txt", filepath.Join(dir, "first.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("first.txt", filepath.Join(dir, "second.txt")); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "first.txt", "second.txt")
		},
		"git metadata name": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/master\n"), 0644); err != nil {
				t.Fatal(err)
			}
			runGit(t, dir, "add", "HEAD")
		},
	}
	for name, taint := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fx := writeFixture(t, dir)
			taint(t, fx.repoDir)
			runGit(t, fx.repoDir, "commit", "-q", "-m", "taint")
			headSHA := runGit(t, fx.repoDir, "rev-parse", "HEAD")

			retargetFixture(t, fx, headSHA)

			prospOut := filepath.Join(dir, "bundle")
			opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: prospOut, EvaluatorOut: filepath.Join(dir, "evaluator")}
			if err := Export(context.Background(), opt); err == nil {
				t.Fatalf("%s was materialized into the snapshot instead of failing the export", name)
			}
			if _, err := os.Stat(prospOut); !os.IsNotExist(err) {
				t.Fatal("a bundle was published despite an inadmissible tree entry")
			}
		})
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
