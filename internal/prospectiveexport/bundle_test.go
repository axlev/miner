package prospectiveexport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildBundle exports a clean bundle and returns its root.
func buildBundle(t *testing.T) (root string, warnings []string) {
	t.Helper()
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	root = filepath.Join(dir, "bundle")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: root, EvaluatorOut: filepath.Join(dir, "evaluator")}
	warnings, err := Export(context.Background(), opt)
	if err != nil {
		t.Fatal(err)
	}
	return root, warnings
}

// revalidate re-runs the bundle validator against a published bundle, reading the
// three miner-authored files back from disk.
func revalidate(t *testing.T, root string) ([]string, error) {
	t.Helper()
	meta, err := os.ReadFile(bundlePath(root, "reviewer/metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(bundlePath(root, "control/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(bundlePath(root, "control/checksums.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return validateBundle(root, meta, manifest, sums)
}

func TestValidateBundleAcceptsAPublishedBundle(t *testing.T) {
	root, warnings := buildBundle(t)
	if len(warnings) != 0 {
		t.Fatalf("clean bundle raised warnings: %v", warnings)
	}
	if _, err := revalidate(t, root); err != nil {
		t.Fatalf("a bundle the exporter just published does not revalidate: %v", err)
	}
}

func TestValidateBundleRejectsBoundaryViolations(t *testing.T) {
	cases := map[string]func(t *testing.T, root string){
		"file added to the snapshot but not the checksum manifest": func(t *testing.T, root string) {
			if err := os.WriteFile(bundlePath(root, "reviewer/repository/extra.txt"), []byte("stray\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"file listed in the checksum manifest but absent": func(t *testing.T, root string) {
			if err := os.Remove(bundlePath(root, "reviewer/repository/feature.txt")); err != nil {
				t.Fatal(err)
			}
		},
		"reviewer file modified after hashing": func(t *testing.T, root string) {
			if err := os.WriteFile(bundlePath(root, "reviewer/repository/base.txt"), []byte("tampered\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"unexpected entry under reviewer": func(t *testing.T, root string) {
			if err := os.WriteFile(bundlePath(root, "reviewer/notes.md"), []byte("notes\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"unexpected entry at the bundle root": func(t *testing.T, root string) {
			if err := os.MkdirAll(bundlePath(root, "oracle"), 0755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink in the snapshot": func(t *testing.T, root string) {
			if err := os.Symlink("base.txt", bundlePath(root, "reviewer/repository/link.txt")); err != nil {
				t.Fatal(err)
			}
		},
		"evaluator-only artifact name in the snapshot": func(t *testing.T, root string) {
			if err := os.WriteFile(bundlePath(root, "reviewer/repository/ground_truth.json"), []byte("{}\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"git metadata in the snapshot": func(t *testing.T, root string) {
			if err := os.MkdirAll(bundlePath(root, "reviewer/repository/objects"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bundlePath(root, "reviewer/repository/objects/pack"), []byte("x\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			root, _ := buildBundle(t)
			tamper(t, root)
			if _, err := revalidate(t, root); err == nil {
				t.Fatalf("validator accepted a bundle with: %s", name)
			}
		})
	}
}

// TestValidateBundleWarnsOnHighSignalSnapshotNames covers the one lexical rule the
// engine allows a protocol to waive: it is reported for a human to judge, not
// silently renamed or dropped, since the snapshot's vocabulary is upstream's.
func TestValidateBundleWarnsOnHighSignalSnapshotNames(t *testing.T) {
	root, _ := buildBundle(t)
	if err := os.WriteFile(bundlePath(root, "reviewer/repository/answer_key.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sums, err := buildChecksums(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundlePath(root, "control/checksums.sha256"), sums, 0644); err != nil {
		t.Fatal(err)
	}
	warnings, err := revalidate(t, root)
	if err != nil {
		t.Fatalf("an upstream source path tripping the lexical heuristic should warn, not fail: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "answer_key.go") {
		t.Fatalf("expected one warning naming answer_key.go, got %v", warnings)
	}
}

// TestReviewerTreeCarriesNoEvaluatorContent asserts the separation the whole export
// exists to maintain: nothing from the evaluator-only root appears under reviewer/.
func TestReviewerTreeCarriesNoEvaluatorContent(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	root := filepath.Join(dir, "bundle")
	evalOut := filepath.Join(dir, "evaluator")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: root, EvaluatorOut: evalOut}
	if _, err := Export(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
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
