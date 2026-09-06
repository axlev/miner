package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"miner/internal/batchstore"
	"miner/internal/gitx"
	"miner/internal/model"
	"miner/internal/storage"
)

func TestBatchedCorrelationResumesAndRecoversSidecar(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "repo")
	runGitTest(t, "init", repoDir)
	runGitTest(t, "-C", repoDir, "config", "user.email", "test@example.com")
	runGitTest(t, "-C", repoDir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "README"), []byte("fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, "-C", repoDir, "add", "README")
	runGitTest(t, "-C", repoDir, "commit", "-m", "fixture")
	head := strings.TrimSpace(runGitTest(t, "-C", repoDir, "rev-parse", "HEAD"))

	cutoff := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	records := []model.PRCandidateRecord{{
		SchemaVersion: model.SchemaVersion,
		Original: model.OriginalPR{
			Repository: "FRRouting/frr",
			Number:     100,
			MergedAt:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		Provenance: model.Provenance{ObservationEnd: cutoff, TargetRepoHeadSHA: head},
	}}
	inputPath := filepath.Join(dir, "raw.jsonl")
	if err := storage.WriteJSONL(inputPath, records); err != nil {
		t.Fatal(err)
	}
	batchDir := filepath.Join(dir, "batches")
	oldDir, oldSize := correlateBatchDirFlag, correlateBatchSizeFlag
	correlateBatchDirFlag, correlateBatchSizeFlag = batchDir, 1
	defer func() {
		correlateBatchDirFlag, correlateBatchSizeFlag = oldDir, oldSize
	}()

	repo := gitx.OpenRepository(repoDir)
	if err := runBatchedCorrelation(context.Background(), records, nil, repo, "FRRouting/frr", inputPath, "config", cutoff, 1); err != nil {
		t.Fatalf("first batch run failed: %v", err)
	}
	dataPath, metadataPath := batchstore.BatchPaths(batchDir, 1)
	firstHash, err := batchstore.HashFile(dataPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := runBatchedCorrelation(context.Background(), records, nil, repo, "FRRouting/frr", inputPath, "config", cutoff, 1); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	secondHash, err := batchstore.HashFile(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatal("resume rewrote completed batch")
	}

	if err := os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	if err := runBatchedCorrelation(context.Background(), records, nil, repo, "FRRouting/frr", inputPath, "config", cutoff, 1); err != nil {
		t.Fatalf("sidecar recovery failed: %v", err)
	}
	if !fileExists(metadataPath) {
		t.Fatal("sidecar was not recovered")
	}
}

func TestFinalizeBatchDirectory(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "raw.jsonl")
	batchDir := filepath.Join(dir, "batches")
	outputPath := filepath.Join(dir, "correlated.jsonl")
	cutoff := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)

	input := makeTestBatchRecords(cutoff)
	if err := storage.WriteJSONL(inputPath, input); err != nil {
		t.Fatal(err)
	}
	inputHash, err := batchstore.HashFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	run := batchstore.RunManifest{
		SchemaVersion:    batchstore.SchemaVersion,
		Repository:       "FRRouting/frr",
		InputSHA256:      inputHash,
		InputRecordCount: len(input),
		BatchSize:        2,
		BatchCount:       2,
		ObservationEnd:   cutoff,
	}
	if err := batchstore.WriteJSONNewAtomic(batchstore.RunManifestPath(batchDir), run); err != nil {
		t.Fatal(err)
	}

	sort.Slice(input, func(i, j int) bool { return input[i].Original.Number < input[j].Original.Number })
	for batchNumber := 1; batchNumber <= 2; batchNumber++ {
		start := (batchNumber - 1) * 2
		end := minInt(start+2, len(input))
		batchRecords := input[start:end]
		dataPath, metadataPath := batchstore.BatchPaths(batchDir, batchNumber)
		if err := batchstore.WriteJSONLNewAtomic(dataPath, batchRecords); err != nil {
			t.Fatal(err)
		}
		hash, err := batchstore.HashFile(dataPath)
		if err != nil {
			t.Fatal(err)
		}
		counts := correlationCounts{}
		metadata := makeBatchManifest(dataPath, batchNumber, start, end, batchRecords, counts, hash)
		if err := batchstore.WriteJSONNewAtomic(metadataPath, metadata); err != nil {
			t.Fatal(err)
		}
	}

	if err := finalizeBatchDirectory(inputPath, batchDir, outputPath); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	merged, err := storage.ReadJSONL(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 3 || merged[0].Original.Number != 10 || merged[2].Original.Number != 30 {
		t.Fatalf("unexpected merged coverage: %+v", merged)
	}
	if err := finalizeBatchDirectory(inputPath, batchDir, outputPath); err == nil {
		t.Fatal("expected existing output refusal")
	}
}

func TestFinalizeRejectsTamperedBatch(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "raw.jsonl")
	batchDir := filepath.Join(dir, "batches")
	outputPath := filepath.Join(dir, "correlated.jsonl")
	cutoff := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	input := makeTestBatchRecords(cutoff)[:2]
	if err := storage.WriteJSONL(inputPath, input); err != nil {
		t.Fatal(err)
	}
	inputHash, _ := batchstore.HashFile(inputPath)
	run := batchstore.RunManifest{SchemaVersion: batchstore.SchemaVersion, InputSHA256: inputHash, InputRecordCount: 2, BatchSize: 2, BatchCount: 1, ObservationEnd: cutoff}
	if err := batchstore.WriteJSONNewAtomic(batchstore.RunManifestPath(batchDir), run); err != nil {
		t.Fatal(err)
	}
	dataPath, metadataPath := batchstore.BatchPaths(batchDir, 1)
	if err := batchstore.WriteJSONLNewAtomic(dataPath, input); err != nil {
		t.Fatal(err)
	}
	hash, _ := batchstore.HashFile(dataPath)
	metadata := makeBatchManifest(dataPath, 1, 0, 2, input, correlationCounts{}, hash)
	if err := batchstore.WriteJSONNewAtomic(metadataPath, metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath, []byte("tampered\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := finalizeBatchDirectory(inputPath, batchDir, outputPath); err == nil {
		t.Fatal("expected tampered batch rejection")
	}
}

func makeTestBatchRecords(cutoff time.Time) []model.PRCandidateRecord {
	return []model.PRCandidateRecord{
		{SchemaVersion: model.SchemaVersion, Original: model.OriginalPR{Number: 30}, Provenance: model.Provenance{ObservationEnd: cutoff}},
		{SchemaVersion: model.SchemaVersion, Original: model.OriginalPR{Number: 10}, Provenance: model.Provenance{ObservationEnd: cutoff}},
		{SchemaVersion: model.SchemaVersion, Original: model.OriginalPR{Number: 20}, Provenance: model.Provenance{ObservationEnd: cutoff}},
	}
}

func runGitTest(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
