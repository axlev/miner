package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"pr-analysis/internal/batchstore"
	"pr-analysis/internal/model"
	"pr-analysis/internal/storage"
)

var (
	finalizeBatchesInFlag  string
	finalizeBatchesDirFlag string
	finalizeBatchesOutFlag string
)

var finalizeBatchesCmd = &cobra.Command{
	Use:   "finalize-batches",
	Short: "Validate batch checkpoints and optionally create one combined JSONL",
	Long:  `Validates run identity, hashes, ranges, cutoff consistency, uniqueness, and complete PR coverage before creating a new combined JSONL. Batch files remain unchanged and authoritative.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return finalizeBatchDirectory(finalizeBatchesInFlag, finalizeBatchesDirFlag, finalizeBatchesOutFlag)
	},
}

func finalizeBatchDirectory(inputFile, batchDir, outputFile string) error {
	if batchDir == "" {
		return fmt.Errorf("--batch-dir is required")
	}
	if _, err := os.Stat(outputFile); err == nil {
		return fmt.Errorf("refusing to replace existing output %q", outputFile)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output %q: %w", outputFile, err)
	}

	records, err := storage.ReadJSONL(inputFile)
	if err != nil {
		return fmt.Errorf("read input records: %w", err)
	}
	if len(records) == 0 {
		return fmt.Errorf("no records found in input file %q", inputFile)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Original.Number < records[j].Original.Number
	})
	if err := validateSortedInput(records); err != nil {
		return err
	}

	var run batchstore.RunManifest
	if err := batchstore.ReadJSON(batchstore.RunManifestPath(batchDir), &run); err != nil {
		return err
	}
	inputHash, err := batchstore.HashFile(inputFile)
	if err != nil {
		return err
	}
	if run.SchemaVersion != batchstore.SchemaVersion {
		return fmt.Errorf("unsupported batch schema version %q", run.SchemaVersion)
	}
	if run.InputSHA256 != inputHash {
		return fmt.Errorf("input SHA-256 does not match run manifest")
	}
	if run.InputRecordCount != len(records) {
		return fmt.Errorf("input record count %d does not match manifest count %d", len(records), run.InputRecordCount)
	}
	if run.BatchSize < 1 {
		return fmt.Errorf("invalid batch size %d in run manifest", run.BatchSize)
	}
	expectedBatchCount := (len(records) + run.BatchSize - 1) / run.BatchSize
	if run.BatchCount != expectedBatchCount {
		return fmt.Errorf("invalid batch partition in run manifest")
	}

	merged := make([]model.PRCandidateRecord, 0, len(records))
	totalStrong := 0
	totalMedium := 0
	for batchNumber := 1; batchNumber <= run.BatchCount; batchNumber++ {
		start := (batchNumber - 1) * run.BatchSize
		end := minInt(start+run.BatchSize, len(records))
		dataPath, metadataPath := batchstore.BatchPaths(batchDir, batchNumber)
		if !fileExists(dataPath) || !fileExists(metadataPath) {
			return fmt.Errorf("batch %d is incomplete", batchNumber)
		}
		batchRecords, counts, dataHash, err := validateBatchData(dataPath, records[start:end], run.ObservationEnd)
		if err != nil {
			return fmt.Errorf("validate batch %d: %w", batchNumber, err)
		}
		if err := validateBatchMetadata(metadataPath, dataPath, batchNumber, start, end, batchRecords, counts, dataHash); err != nil {
			return err
		}
		merged = append(merged, batchRecords...)
		totalStrong += counts.Strong
		totalMedium += counts.Medium
	}

	if len(merged) != len(records) {
		return fmt.Errorf("merged coverage %d does not match input count %d", len(merged), len(records))
	}
	for i := range merged {
		if merged[i].Original.Number != records[i].Original.Number {
			return fmt.Errorf("merged PR coverage mismatch at index %d", i)
		}
	}
	if err := batchstore.WriteJSONLNewAtomic(outputFile, merged); err != nil {
		return err
	}
	outputHash, err := batchstore.HashFile(outputFile)
	if err != nil {
		return err
	}
	fmt.Printf("Validated %d batches and %d unique PRs (%d strong, %d medium).\n", run.BatchCount, len(merged), totalStrong, totalMedium)
	fmt.Printf("Combined JSONL written to %s (sha256=%s). Batch checkpoints were retained.\n", outputFile, outputHash)
	return nil
}

func init() {
	finalizeBatchesCmd.Flags().StringVarP(&finalizeBatchesInFlag, "in", "i", "./data/raw_prs.jsonl", "Original input JSONL used to define batch membership")
	finalizeBatchesCmd.Flags().StringVar(&finalizeBatchesDirFlag, "batch-dir", "", "Directory containing run.json and batch checkpoints")
	finalizeBatchesCmd.Flags().StringVarP(&finalizeBatchesOutFlag, "out", "o", "./data/correlated.jsonl", "New combined JSONL output")
}
