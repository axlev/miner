package batchstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"miner/internal/model"
	"miner/internal/storage"
)

func TestWriteJSONLNewAtomicAndHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "batch-0001.jsonl")
	records := []model.PRCandidateRecord{
		{SchemaVersion: model.SchemaVersion, Original: model.OriginalPR{Number: 2}},
		{SchemaVersion: model.SchemaVersion, Original: model.OriginalPR{Number: 1}},
	}

	if err := WriteJSONLNewAtomic(path, records); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := WriteJSONLNewAtomic(path, records); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	loaded, err := storage.ReadJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].Original.Number != 1 || loaded[1].Original.Number != 2 {
		t.Fatalf("unexpected deterministic output: %+v", loaded)
	}
	hash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != 64 {
		t.Fatalf("unexpected SHA-256 %q", hash)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".*.tmp-*")); len(matches) != 0 {
		t.Fatalf("temporary files remain: %v", matches)
	}
}

func TestWriteAndReadRunManifest(t *testing.T) {
	dir := t.TempDir()
	path := RunManifestPath(dir)
	want := RunManifest{
		SchemaVersion:    SchemaVersion,
		Repository:       "FRRouting/frr",
		InputSHA256:      "abc",
		InputRecordCount: 10,
		BatchSize:        4,
		BatchCount:       3,
		ObservationEnd:   time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
	}
	if err := WriteJSONNewAtomic(path, want); err != nil {
		t.Fatal(err)
	}
	var got RunManifest
	if err := ReadJSON(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Repository != want.Repository || got.BatchCount != want.BatchCount || !got.ObservationEnd.Equal(want.ObservationEnd) {
		t.Fatalf("manifest mismatch: got %+v want %+v", got, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
