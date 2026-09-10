package prospectiveexport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildBundle exports a clean bundle and returns its root.
func buildBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	root := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: root, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestChecksumsCoverExactlyTheReviewerTree pins the artifact the miner is responsible
// for producing. The engine verifies this file in both directions, so generating it
// correctly is a production concern that stays here even though validating it does not.
func TestChecksumsCoverExactlyTheReviewerTree(t *testing.T) {
	root := buildBundle(t)
	sums, err := buildChecksums(root)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Fatalf("malformed checksum line %q", line)
		}
		if !strings.HasPrefix(parts[1], ReviewerDir+"/") {
			t.Fatalf("checksum path %q is not relative to the bundle root", parts[1])
		}
		listed[parts[1]] = true
	}
	files, err := reviewerFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(listed) {
		t.Fatalf("checksum manifest lists %d files, reviewer tree holds %d", len(listed), len(files))
	}
	for _, rel := range files {
		if !listed[rel] {
			t.Fatalf("checksum manifest omits %s", rel)
		}
	}

	// A file added after the manifest was written must change what buildChecksums
	// produces, or the manifest could not describe the tree exactly.
	if err := os.WriteFile(bundlePath(root, "reviewer/repository/extra.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := buildChecksums(root)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == string(sums) {
		t.Fatal("buildChecksums ignored a new file in the reviewer tree")
	}
}

// TestReviewerFilesSkipsSymlinks pins the agreement with the engine's checksum check,
// which builds its own file set from regular files only. A symlink listed in
// control/checksums.sha256 would be read as "listed but absent" and fail the bundle.
func TestReviewerFilesSkipsSymlinks(t *testing.T) {
	root := buildBundle(t)
	before, err := reviewerFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("base.txt", bundlePath(root, "reviewer/repository/link.txt")); err != nil {
		t.Fatal(err)
	}
	after, err := reviewerFiles(root)
	if err != nil {
		t.Fatalf("a symlink in the reviewer tree was reported as irregular: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("symlink changed the checksummed file set: %d -> %d", len(before), len(after))
	}
	for _, rel := range after {
		if strings.HasSuffix(rel, "link.txt") {
			t.Fatalf("symlink %s was included in the checksummed set", rel)
		}
	}
}

// TestEvaluatorArtifactNamesAreBlocked covers the one contamination check the engine
// explicitly delegates to the miner. The engine's in-snapshot heuristic omits the
// ground_truth fragment on purpose, so nothing downstream catches this.
func TestEvaluatorArtifactNamesAreBlocked(t *testing.T) {
	for _, name := range []string{"ground_truth.json", "oracle.json", "expected_findings.json", "answer_key.json", "retrospective.json", "GROUND_TRUTH.JSON"} {
		t.Run(name, func(t *testing.T) {
			root := buildBundle(t)
			if err := os.WriteFile(bundlePath(root, "reviewer/repository/"+name), []byte("{}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			err := checkEvaluatorArtifactNames(root)
			if err == nil {
				t.Fatalf("%s reached reviewer-visible space unchallenged", name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(name)) {
				t.Fatalf("error does not name the offending file: %v", err)
			}
		})
	}
}

func TestEvaluatorArtifactCheckAcceptsOrdinarySource(t *testing.T) {
	root := buildBundle(t)
	// Names that legitimately appear in real repositories and must not be blocked:
	// the engine's blunt substring heuristic would flag some of these, which is why
	// the miner matches exact basenames instead.
	for _, name := range []string{"solution.go", "verdict.py", "oracle_dialect.go", "ground_truth_loader.py", "OracleConnection.java"} {
		if err := os.WriteFile(bundlePath(root, "reviewer/repository/"+name), []byte("x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkEvaluatorArtifactNames(root); err != nil {
		t.Fatalf("ordinary source filenames were rejected: %v", err)
	}
}

// TestExportRejectsEvaluatorArtifactInSnapshot proves the check is wired into the
// export path and that a rejected case publishes nothing.
func TestExportRejectsEvaluatorArtifactInSnapshot(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	// Commit an evaluator-shaped filename into the head tree.
	if err := os.WriteFile(filepath.Join(fx.repoDir, "ground_truth.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, fx.repoDir, "add", "ground_truth.json")
	runGit(t, fx.repoDir, "commit", "-q", "-m", "leak")
	head := runGit(t, fx.repoDir, "rev-parse", "HEAD")
	retargetFixture(t, fx, head)

	root := filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: root, EvaluatorOut: filepath.Join(dir, "evaluator")}
	if err := Export(context.Background(), opt); err == nil {
		t.Fatal("a snapshot containing ground_truth.json was published")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("a bundle was published despite the rejection")
	}
}

// TestReviewerTreeCarriesNoEvaluatorContent asserts the separation the whole export
// exists to maintain: nothing from the evaluator-only root appears under reviewer/.
func TestReviewerTreeCarriesNoEvaluatorContent(t *testing.T) {
	root := buildBundle(t)
	files, err := reviewerFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range files {
		body, err := os.ReadFile(bundlePath(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range []string{"FUTURE_ONLY_FIX", "retrospective", "strong_signals", "merge_commit_sha", strings.Repeat("a", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)} {
			if strings.Contains(string(body), leak) {
				t.Fatalf("%s carries evaluator-only content %q", rel, leak)
			}
		}
	}
}
