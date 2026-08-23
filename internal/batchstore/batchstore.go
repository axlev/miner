package batchstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"pr-analysis/internal/model"
	"pr-analysis/internal/storage"
)

const SchemaVersion = "1.0.0"

// RunManifest fixes the identity and partitioning of a resumable correlation run.
// It is immutable after creation; each completed batch has its own sidecar manifest.
type RunManifest struct {
	SchemaVersion          string    `json:"schema_version"`
	Repository             string    `json:"repository"`
	InputFile              string    `json:"input_file"`
	InputSHA256            string    `json:"input_sha256"`
	InputRecordCount       int       `json:"input_record_count"`
	BatchSize              int       `json:"batch_size"`
	BatchCount             int       `json:"batch_count"`
	ObservationEnd         time.Time `json:"observation_end"`
	ConfigSHA256           string    `json:"config_sha256"`
	InputTargetRepoHeadSHA string    `json:"input_target_repo_head_sha"`
	ObservedRepoHeadSHA    string    `json:"observed_repo_head_sha"`
	InitialWorkers         int       `json:"initial_workers"`
	CreatedAt              time.Time `json:"created_at"`
}

// BatchManifest describes one immutable, independently verifiable checkpoint.
type BatchManifest struct {
	SchemaVersion     string    `json:"schema_version"`
	BatchNumber       int       `json:"batch_number"`
	StartIndex        int       `json:"start_index"`
	EndIndexExclusive int       `json:"end_index_exclusive"`
	FirstPRNumber     int       `json:"first_pr_number"`
	LastPRNumber      int       `json:"last_pr_number"`
	RecordCount       int       `json:"record_count"`
	StrongSignalCount int       `json:"strong_signal_count"`
	MediumSignalCount int       `json:"medium_signal_count"`
	DataFile          string    `json:"data_file"`
	DataSHA256        string    `json:"data_sha256"`
	CompletedAt       time.Time `json:"completed_at"`
}

func RunManifestPath(dir string) string {
	return filepath.Join(dir, "run.json")
}

func BatchPaths(dir string, batchNumber int) (dataPath, manifestPath string) {
	base := fmt.Sprintf("batch-%04d", batchNumber)
	return filepath.Join(dir, base+".jsonl"), filepath.Join(dir, base+".json")
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q for hashing: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteJSONNewAtomic writes a new JSON file without replacing an existing artifact.
func WriteJSONNewAtomic(path string, value any) error {
	if err := ensureNewTarget(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		tmp.Close()
		return fmt.Errorf("encode %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish %q: %w", path, err)
	}
	return nil
}

// WriteJSONLNewAtomic writes a new JSONL artifact without replacing an existing one.
func WriteJSONLNewAtomic(path string, records []model.PRCandidateRecord) error {
	if err := ensureNewTarget(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temporary file for %q: %w", path, err)
	}
	defer os.Remove(tmpPath)

	if err := storage.WriteJSONL(tmpPath, records); err != nil {
		return err
	}
	f, err := os.OpenFile(tmpPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open temporary JSONL for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync temporary JSONL: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary JSONL: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish %q: %w", path, err)
	}
	return nil
}

func ReadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	return nil
}

func ensureNewTarget(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to replace existing artifact %q", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect target %q: %w", path, err)
	}
	return nil
}
