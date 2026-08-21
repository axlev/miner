package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"pr-analysis/internal/model"
)

func TestJSONLReadWrite(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "jsonl_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "candidates.jsonl")

	records := []model.PRCandidateRecord{
		{
			SchemaVersion: model.SchemaVersion,
			Original: model.OriginalPR{
				Number:   1002,
				Title:    "PR 1002",
				MergedAt: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			SchemaVersion: model.SchemaVersion,
			Original: model.OriginalPR{
				Number:   1001, // Notice: out of order
				Title:    "PR 1001",
				MergedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	if err := WriteJSONL(filePath, records); err != nil {
		t.Fatalf("WriteJSONL failed: %v", err)
	}

	loaded, err := ReadJSONL(filePath)
	if err != nil {
		t.Fatalf("ReadJSONL failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 records, got %d", len(loaded))
	}

	// Verify deterministic ascending sort
	if loaded[0].Original.Number != 1001 {
		t.Errorf("expected first record to be PR 1001, got %d", loaded[0].Original.Number)
	}
	if loaded[1].Original.Number != 1002 {
		t.Errorf("expected second record to be PR 1002, got %d", loaded[1].Original.Number)
	}
}

func TestDiskCache(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cache_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	cache := NewDiskCache(tempDir)
	repo := "FRRouting/frr"
	prNum := 14500

	if cache.HasPR(repo, prNum) {
		t.Errorf("cache should not have PR before write")
	}

	dummyJSON := []byte(`{"id": 14500, "title": "test pr"}`)
	if err := cache.WritePR(repo, prNum, dummyJSON); err != nil {
		t.Fatalf("WritePR failed: %v", err)
	}

	if !cache.HasPR(repo, prNum) {
		t.Errorf("cache should have PR after write")
	}

	var res struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := cache.ReadPR(repo, prNum, &res); err != nil {
		t.Fatalf("ReadPR failed: %v", err)
	}

	if res.ID != 14500 || res.Title != "test pr" {
		t.Errorf("unexpected decoded payload: %+v", res)
	}
}
