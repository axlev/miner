package retrospectiveexport

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
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

func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "-qam", "candidate one\n\nFixes: historical detail\nSigned-off-by: Test <test@example.com>")
	one := git(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("unrelated-looking\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "b.txt")
	git(t, dir, "commit", "-q", "-m", "candidate two")
	two := git(t, dir, "rev-parse", "HEAD")
	return dir, one, two
}

func writeInput(t *testing.T, path, one, two string) []byte {
	t.Helper()
	missing := strings.Repeat("f", 40)
	logical := strings.Repeat("1", 40)
	tmpl, err := os.ReadFile(filepath.Join("..", "..", "testdata", "retrospectiveexport", "correlated_record.json.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	replacer := strings.NewReplacer("{{ONE}}", one, "{{TWO}}", two, "{{MISSING}}", missing, "{{LOGICAL}}", logical)
	record := strings.TrimRight(replacer.Replace(string(tmpl)), "\n")
	other, err := os.ReadFile(filepath.Join("..", "..", "testdata", "retrospectiveexport", "other_record.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := append(append([]byte(nil), other...), []byte(record+"\n")...)
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	return []byte(record)
}

func decodeFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

func TestExportCompletenessGroupingDedupRelationshipsAndMissing(t *testing.T) {
	repo, one, two := fixture(t)
	input := filepath.Join(t.TempDir(), "in.jsonl")
	selected := writeInput(t, input, one, two)
	out := filepath.Join(t.TempDir(), "retrospective")
	if err := Export(context.Background(), Options{Input: input, Repo: repo, Out: out, PR: 15624, ExportedAt: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	source, _ := os.ReadFile(filepath.Join(out, "source_record.json"))
	if string(source) != string(selected)+"\n" {
		t.Fatal("source record was not preserved byte-for-byte")
	}
	var all []signalExport
	decodeFile(t, filepath.Join(out, "signals/all.json"), &all)
	if len(all) != 4 {
		t.Fatalf("got %d signals", len(all))
	}
	if !strings.Contains(string(all[0].RawSignal), "strong_only_field") {
		t.Fatal("raw signal field lost")
	}
	entries, _ := os.ReadDir(filepath.Join(out, "commits"))
	if len(entries) != 3 {
		t.Fatalf("expected one file per unique candidate commit, got %d", len(entries))
	}
	if _, err := os.Stat(filepath.Join(out, "patches", one+".patch")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "patches", two+".patch")); err != nil {
		t.Fatal(err)
	}
	var ce commitExport
	decodeFile(t, filepath.Join(out, "commits", one+".json"), &ce)
	if len(ce.SignalReferences) != 2 {
		t.Fatalf("dedup mapping lost: %+v", ce.SignalReferences)
	}
	if !strings.Contains(ce.Metadata.Message, "Signed-off-by:") {
		t.Fatal("complete message trailer lost")
	}
	missing := strings.Repeat("f", 40)
	decodeFile(t, filepath.Join(out, "commits", missing+".json"), &ce)
	if ce.MaterializationStatus != "missing" {
		t.Fatalf("missing status: %s", ce.MaterializationStatus)
	}
	logical := strings.Repeat("1", 40)
	var lm map[string]any
	decodeFile(t, filepath.Join(out, "logical_patches", logical, "manifest.json"), &lm)
	if len(lm["source_refs"].([]any)) != 2 {
		t.Fatal("logical patch did not retain both SHAs")
	}
	var rels []json.RawMessage
	decodeFile(t, filepath.Join(out, "relationships/commit_relationships.json"), &rels)
	if len(rels) != 2 || !strings.Contains(string(rels[0]), "nested_metric") {
		t.Fatal("relationships were not preserved")
	}
	var m manifest
	decodeFile(t, filepath.Join(out, "manifest.json"), &m)
	if m.SignalCounts["total"] != 4 || m.MaterializedCommitCount != 2 || m.MissingCommitCount != 1 {
		t.Fatalf("bad counts: %+v", m)
	}
}

func TestExportLookupAndPatchDeterminism(t *testing.T) {
	repo, one, two := fixture(t)
	input := filepath.Join(t.TempDir(), "in.jsonl")
	writeInput(t, input, one, two)
	if err := Export(context.Background(), Options{Input: input, Repo: repo, Out: filepath.Join(t.TempDir(), "missing"), PR: 999}); err == nil || !strings.Contains(err.Error(), "PR #999 not found") {
		t.Fatalf("unclear lookup error: %v", err)
	}
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	fixed := time.Unix(0, 0)
	for _, out := range []string{a, b} {
		if err := Export(context.Background(), Options{Input: input, Repo: repo, Out: out, PR: 15624, ExportedAt: fixed}); err != nil {
			t.Fatal(err)
		}
	}
	pa, _ := os.ReadFile(filepath.Join(a, "patches", one+".patch"))
	pb, _ := os.ReadFile(filepath.Join(b, "patches", one+".patch"))
	if string(pa) != string(pb) {
		t.Fatal("patch output is not deterministic")
	}
}
