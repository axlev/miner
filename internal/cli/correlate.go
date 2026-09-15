package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/batchstore"
	"miner/internal/buildinfo"
	"miner/internal/config"
	"miner/internal/correlator"
	"miner/internal/gitx"
	"miner/internal/model"
	"miner/internal/storage"
)

var (
	correlateInFlag             string
	correlateOutFlag            string
	correlateRepoFlag           string
	correlateGitDirFlag         string
	correlateObservationEndFlag string
	correlateWorkersFlag        int
	correlateBatchDirFlag       string
	correlateBatchSizeFlag      int
)

var correlateCmd = &cobra.Command{
	Use:   "correlate",
	Short: "Extract retrospective corrective signals from git history",
	Long:  `Scans post-merge commit history and issues up to observation-end to correlate original PRs with Fixes:, reverts, regression mentions, and symbol overlaps. Use --batch-dir for memory-bounded, resumable correlation.`,
	RunE:  runCorrelateCommand,
}

type correlationCounts struct {
	Strong      int
	Medium      int
	Uninspected int // sum of per-record uninspected commit counts
}

func runCorrelateCommand(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg := &config.Config{}
	if _, err := os.Stat(configFile); err == nil {
		loaded, err := config.LoadConfig(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config %q: %w", configFile, err)
		}
		cfg = loaded
	}

	inFile := correlateInFlag
	outFile := correlateOutFlag
	records, err := storage.ReadJSONL(inFile)
	if err != nil {
		return fmt.Errorf("failed to read input records from %q: %w", inFile, err)
	}
	if len(records) == 0 {
		return fmt.Errorf("no records found in input file %q", inFile)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Original.Number < records[j].Original.Number
	})
	if err := validateSortedInput(records); err != nil {
		return err
	}

	repo := records[0].Original.Repository
	if correlateRepoFlag != "" {
		repo = correlateRepoFlag
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo %q", repo)
	}
	owner, repoName := parts[0], parts[1]

	gitDir := filepath.Join("./data/cache", "repos", owner, repoName+".git")
	if cfg.Mining.GitDir != "" {
		gitDir = cfg.Mining.GitDir
	}
	if correlateGitDirFlag != "" {
		gitDir = correlateGitDirFlag
	}
	gitRepo := gitx.OpenRepository(gitDir)

	obsEnd, err := correlationObservationEnd(cfg)
	if err != nil {
		return err
	}

	earliestT0 := records[0].Original.MergedAt
	for i := 1; i < len(records); i++ {
		if records[i].Original.MergedAt.Before(earliestT0) {
			earliestT0 = records[i].Original.MergedAt
		}
	}

	fmt.Printf("Scanning post-merge git log from %s up to observation end %s...\n",
		earliestT0.Format("2006-01-02"), obsEnd.Format("2006-01-02"))
	commits, err := gitRepo.CommitsAfter(ctx, earliestT0, obsEnd)
	if err != nil {
		return fmt.Errorf("failed to scan git commits from %s: %w", gitDir, err)
	}
	fmt.Printf("Loaded %d post-merge commits.\n", len(commits))

	workers := correlateWorkersFlag
	if workers == 0 {
		if correlateBatchDirFlag != "" {
			workers = minInt(runtime.NumCPU(), 4)
		} else {
			workers = runtime.NumCPU()
		}
	}
	if workers < 1 {
		return fmt.Errorf("--workers must be greater than zero")
	}

	if correlateBatchDirFlag != "" {
		if cmd.Flags().Changed("out") {
			return fmt.Errorf("--out cannot be used with --batch-dir; use finalize-batches when a combined file is needed")
		}
		return runBatchedCorrelation(ctx, records, commits, gitRepo, repo, inFile, cfg.ConfigHash, obsEnd, workers)
	}
	if correlateBatchSizeFlag != 50 {
		return fmt.Errorf("--batch-size requires --batch-dir")
	}

	fmt.Printf("Correlating %d PRs using %d parallel workers...\n", len(records), workers)
	corr := correlator.NewCorrelator(gitRepo)
	counts := correlateRecordSet(ctx, corr, records, commits, obsEnd, workers, "")
	if err := storage.WriteJSONL(outFile, records); err != nil {
		return fmt.Errorf("failed to write correlated records to %q: %w", outFile, err)
	}
	fmt.Printf("Successfully correlated %d PRs (%d with strong fix signals, %d with medium signals, %d uninspected commit diffs)\nSaved to %s\n",
		len(records), counts.Strong, counts.Medium, counts.Uninspected, outFile)
	return nil
}

func correlationObservationEnd(cfg *config.Config) (time.Time, error) {
	var obsEnd time.Time
	if cfg.Mining.ObservationEnd != nil {
		obsEnd = *cfg.Mining.ObservationEnd
	}
	if correlateObservationEndFlag != "" {
		parsed, err := time.Parse("2006-01-02", correlateObservationEndFlag)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, correlateObservationEndFlag)
		}
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --observation-end: %w", err)
		}
		obsEnd = parsed
	}
	if obsEnd.IsZero() {
		obsEnd = time.Now().UTC()
	}
	return obsEnd.UTC(), nil
}

func runBatchedCorrelation(
	ctx context.Context,
	records []model.PRCandidateRecord,
	commits []gitx.CommitLogEntry,
	gitRepo *gitx.Repository,
	repository string,
	inputFile string,
	configHash string,
	obsEnd time.Time,
	workers int,
) error {
	if correlateBatchSizeFlag < 1 {
		return fmt.Errorf("--batch-size must be greater than zero")
	}
	if err := validateBatchObservationEnd(records, obsEnd); err != nil {
		return err
	}

	inputHash, err := batchstore.HashFile(inputFile)
	if err != nil {
		return err
	}
	observedHead, err := gitRepo.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("read repository HEAD: %w", err)
	}
	targetHead, err := consistentTargetHead(records)
	if err != nil {
		return err
	}

	batchCount := (len(records) + correlateBatchSizeFlag - 1) / correlateBatchSizeFlag
	expectedRun := batchstore.RunManifest{
		SchemaVersion:          batchstore.SchemaVersion,
		Repository:             repository,
		InputFile:              filepath.Base(inputFile),
		InputSHA256:            inputHash,
		InputRecordCount:       len(records),
		BatchSize:              correlateBatchSizeFlag,
		BatchCount:             batchCount,
		ObservationEnd:         obsEnd,
		ConfigSHA256:           configHash,
		InputTargetRepoHeadSHA: targetHead,
		ObservedRepoHeadSHA:    observedHead,
		InitialWorkers:         workers,
		CorrelatorVersion:      buildinfo.MinerVersion(),
		CreatedAt:              time.Now().UTC(),
	}
	if err := prepareBatchRun(correlateBatchDirFlag, expectedRun); err != nil {
		return err
	}

	fmt.Printf("Correlating %d PRs as %d batches of up to %d using %d workers.\n",
		len(records), batchCount, correlateBatchSizeFlag, workers)

	for batchNumber := 1; batchNumber <= batchCount; batchNumber++ {
		start := (batchNumber - 1) * correlateBatchSizeFlag
		end := minInt(start+correlateBatchSizeFlag, len(records))
		expected := records[start:end]
		dataPath, metadataPath := batchstore.BatchPaths(correlateBatchDirFlag, batchNumber)
		dataExists := fileExists(dataPath)
		metadataExists := fileExists(metadataPath)

		if metadataExists && !dataExists {
			return fmt.Errorf("batch %d metadata exists but data file is missing", batchNumber)
		}
		if dataExists {
			batchRecords, counts, dataHash, err := validateBatchData(dataPath, expected, obsEnd)
			if err != nil {
				return fmt.Errorf("validate existing batch %d: %w", batchNumber, err)
			}
			if metadataExists {
				if err := validateBatchMetadata(metadataPath, dataPath, batchNumber, start, end, batchRecords, counts, dataHash, expectedRun.CorrelatorVersion); err != nil {
					return err
				}
			} else {
				metadata := makeBatchManifest(dataPath, batchNumber, start, end, batchRecords, counts, dataHash, expectedRun.CorrelatorVersion)
				if err := batchstore.WriteJSONNewAtomic(metadataPath, metadata); err != nil {
					return fmt.Errorf("recover metadata for batch %d: %w", batchNumber, err)
				}
				fmt.Printf("Recovered checkpoint metadata for batch %d/%d.\n", batchNumber, batchCount)
			}
			fmt.Printf("Verified existing batch %d/%d (%d PRs); skipping.\n", batchNumber, batchCount, len(batchRecords))
			continue
		}

		batchRecords := append([]model.PRCandidateRecord(nil), expected...)
		fmt.Printf("Starting batch %d/%d (PR indexes %d..%d).\n", batchNumber, batchCount, start, end-1)
		corr := correlator.NewCorrelator(gitRepo)
		counts := correlateRecordSet(ctx, corr, batchRecords, commits, obsEnd, minInt(workers, len(batchRecords)), fmt.Sprintf("Batch %d: ", batchNumber))

		if err := batchstore.WriteJSONLNewAtomic(dataPath, batchRecords); err != nil {
			return fmt.Errorf("write batch %d: %w", batchNumber, err)
		}
		dataHash, err := batchstore.HashFile(dataPath)
		if err != nil {
			return err
		}
		metadata := makeBatchManifest(dataPath, batchNumber, start, end, batchRecords, counts, dataHash, expectedRun.CorrelatorVersion)
		if err := batchstore.WriteJSONNewAtomic(metadataPath, metadata); err != nil {
			return fmt.Errorf("write metadata for batch %d: %w", batchNumber, err)
		}
		fmt.Printf("Completed batch %d/%d: %d PRs, sha256=%s.\n", batchNumber, batchCount, len(batchRecords), dataHash)

		batchRecords = nil
		corr = nil
		runtime.GC()
		debug.FreeOSMemory()
	}

	fmt.Printf("All %d batch checkpoints are complete in %s. They remain the canonical resumable artifacts.\n", batchCount, correlateBatchDirFlag)
	fmt.Println("Run finalize-batches only if a single JSONL file is needed by downstream commands.")
	return nil
}

func correlateRecordSet(
	ctx context.Context,
	corr *correlator.Correlator,
	records []model.PRCandidateRecord,
	commits []gitx.CommitLogEntry,
	obsEnd time.Time,
	workers int,
	progressPrefix string,
) correlationCounts {
	var processedCount int64
	var strongCount int64
	var mediumCount int64
	var uninspectedCount int64
	correlatedBy := buildinfo.MinerVersion()
	jobs := make(chan int, len(records))
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				evidence := corr.CorrelatePR(ctx, &records[idx].Original, commits, obsEnd)
				records[idx].Retrospective = evidence
				records[idx].Provenance.ObservationEnd = obsEnd
				records[idx].Provenance.CorrelatedBy = correlatedBy
				records[idx].SchemaVersion = model.SchemaVersion
				atomic.AddInt64(&uninspectedCount, int64(evidence.UninspectedCommitCount))
				if len(evidence.StrongSignals) > 0 {
					atomic.AddInt64(&strongCount, 1)
				} else if len(evidence.MediumSignals) > 0 {
					atomic.AddInt64(&mediumCount, 1)
				}
				cur := atomic.AddInt64(&processedCount, 1)
				if cur%25 == 0 || int(cur) == len(records) {
					fmt.Printf("%sCorrelated %d/%d PRs (%5.1f%%)...\n",
						progressPrefix, cur, len(records), float64(cur)/float64(len(records))*100)
				}
			}
		}()
	}
	for i := range records {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return correlationCounts{Strong: int(strongCount), Medium: int(mediumCount), Uninspected: int(uninspectedCount)}
}

func prepareBatchRun(dir string, expected batchstore.RunManifest) error {
	manifestPath := batchstore.RunManifestPath(dir)
	if fileExists(manifestPath) {
		var actual batchstore.RunManifest
		if err := batchstore.ReadJSON(manifestPath, &actual); err != nil {
			return err
		}
		if err := compareRunManifests(actual, expected); err != nil {
			return fmt.Errorf("cannot resume batch directory %q: %w", dir, err)
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect batch directory %q: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("batch directory %q is not empty and has no run.json", dir)
	}
	return batchstore.WriteJSONNewAtomic(manifestPath, expected)
}

func compareRunManifests(actual, expected batchstore.RunManifest) error {
	switch {
	case actual.SchemaVersion != expected.SchemaVersion:
		return fmt.Errorf("schema version changed: %q != %q", actual.SchemaVersion, expected.SchemaVersion)
	case actual.Repository != expected.Repository:
		return fmt.Errorf("repository changed: %q != %q", actual.Repository, expected.Repository)
	case actual.InputSHA256 != expected.InputSHA256:
		return fmt.Errorf("input SHA-256 changed")
	case actual.InputRecordCount != expected.InputRecordCount:
		return fmt.Errorf("input record count changed: %d != %d", actual.InputRecordCount, expected.InputRecordCount)
	case actual.BatchSize != expected.BatchSize || actual.BatchCount != expected.BatchCount:
		return fmt.Errorf("batch partition changed")
	case !actual.ObservationEnd.Equal(expected.ObservationEnd):
		return fmt.Errorf("observation cutoff changed: %s != %s", actual.ObservationEnd, expected.ObservationEnd)
	case actual.ConfigSHA256 != expected.ConfigSHA256:
		return fmt.Errorf("configuration SHA-256 changed")
	case actual.InputTargetRepoHeadSHA != expected.InputTargetRepoHeadSHA:
		return fmt.Errorf("input target repository HEAD changed")
	case actual.ObservedRepoHeadSHA != expected.ObservedRepoHeadSHA:
		return fmt.Errorf("observed repository HEAD changed")
	case actual.CorrelatorVersion != expected.CorrelatorVersion:
		return fmt.Errorf("correlator build changed: %q != %q; a run's batches must all come from one build", actual.CorrelatorVersion, expected.CorrelatorVersion)
	}
	return nil
}

func validateSortedInput(records []model.PRCandidateRecord) error {
	for i, record := range records {
		if record.Original.Number <= 0 {
			return fmt.Errorf("input record %d has invalid PR number %d", i, record.Original.Number)
		}
		if i > 0 && records[i-1].Original.Number == record.Original.Number {
			return fmt.Errorf("input contains duplicate PR #%d", record.Original.Number)
		}
	}
	return nil
}

func validateBatchObservationEnd(records []model.PRCandidateRecord, obsEnd time.Time) error {
	for _, record := range records {
		if !record.Provenance.ObservationEnd.IsZero() && !record.Provenance.ObservationEnd.Equal(obsEnd) {
			return fmt.Errorf("PR #%d has observation cutoff %s, not requested cutoff %s",
				record.Original.Number, record.Provenance.ObservationEnd, obsEnd)
		}
	}
	return nil
}

func consistentTargetHead(records []model.PRCandidateRecord) (string, error) {
	head := records[0].Provenance.TargetRepoHeadSHA
	for _, record := range records[1:] {
		if record.Provenance.TargetRepoHeadSHA != head {
			return "", fmt.Errorf("input contains inconsistent target repository HEAD SHAs")
		}
	}
	return head, nil
}

func validateBatchData(path string, expected []model.PRCandidateRecord, obsEnd time.Time) ([]model.PRCandidateRecord, correlationCounts, string, error) {
	actual, err := storage.ReadJSONL(path)
	if err != nil {
		return nil, correlationCounts{}, "", err
	}
	if len(actual) != len(expected) {
		return nil, correlationCounts{}, "", fmt.Errorf("record count %d != expected %d", len(actual), len(expected))
	}
	counts := correlationCounts{}
	for i := range actual {
		if actual[i].Original.Number != expected[i].Original.Number {
			return nil, correlationCounts{}, "", fmt.Errorf("record %d is PR #%d, expected PR #%d", i, actual[i].Original.Number, expected[i].Original.Number)
		}
		if !actual[i].Provenance.ObservationEnd.Equal(obsEnd) {
			return nil, correlationCounts{}, "", fmt.Errorf("PR #%d has incorrect observation cutoff", actual[i].Original.Number)
		}
		if len(actual[i].Retrospective.StrongSignals) > 0 {
			counts.Strong++
		} else if len(actual[i].Retrospective.MediumSignals) > 0 {
			counts.Medium++
		}
		counts.Uninspected += actual[i].Retrospective.UninspectedCommitCount
	}
	hash, err := batchstore.HashFile(path)
	if err != nil {
		return nil, correlationCounts{}, "", err
	}
	return actual, counts, hash, nil
}

func makeBatchManifest(path string, batchNumber, start, end int, records []model.PRCandidateRecord, counts correlationCounts, dataHash, correlatorVersion string) batchstore.BatchManifest {
	return batchstore.BatchManifest{
		SchemaVersion:          batchstore.SchemaVersion,
		BatchNumber:            batchNumber,
		StartIndex:             start,
		EndIndexExclusive:      end,
		FirstPRNumber:          records[0].Original.Number,
		LastPRNumber:           records[len(records)-1].Original.Number,
		RecordCount:            len(records),
		StrongSignalCount:      counts.Strong,
		MediumSignalCount:      counts.Medium,
		UninspectedCommitCount: counts.Uninspected,
		CorrelatorVersion:      correlatorVersion,
		DataFile:               filepath.Base(path),
		DataSHA256:             dataHash,
		CompletedAt:            time.Now().UTC(),
	}
}

func validateBatchMetadata(path, dataPath string, batchNumber, start, end int, records []model.PRCandidateRecord, counts correlationCounts, dataHash, correlatorVersion string) error {
	var metadata batchstore.BatchManifest
	if err := batchstore.ReadJSON(path, &metadata); err != nil {
		return err
	}
	expected := makeBatchManifest(dataPath, batchNumber, start, end, records, counts, dataHash, correlatorVersion)
	switch {
	case metadata.SchemaVersion != expected.SchemaVersion:
		return fmt.Errorf("batch %d schema version mismatch", batchNumber)
	case metadata.BatchNumber != expected.BatchNumber || metadata.StartIndex != expected.StartIndex || metadata.EndIndexExclusive != expected.EndIndexExclusive:
		return fmt.Errorf("batch %d range metadata mismatch", batchNumber)
	case metadata.FirstPRNumber != expected.FirstPRNumber || metadata.LastPRNumber != expected.LastPRNumber || metadata.RecordCount != expected.RecordCount:
		return fmt.Errorf("batch %d PR coverage metadata mismatch", batchNumber)
	case metadata.StrongSignalCount != expected.StrongSignalCount || metadata.MediumSignalCount != expected.MediumSignalCount:
		return fmt.Errorf("batch %d signal counts mismatch", batchNumber)
	case metadata.UninspectedCommitCount != expected.UninspectedCommitCount:
		return fmt.Errorf("batch %d uninspected commit count mismatch", batchNumber)
	case metadata.CorrelatorVersion != expected.CorrelatorVersion:
		return fmt.Errorf("batch %d was correlated by build %q, this build is %q", batchNumber, metadata.CorrelatorVersion, expected.CorrelatorVersion)
	case metadata.DataFile != expected.DataFile || metadata.DataSHA256 != expected.DataSHA256:
		return fmt.Errorf("batch %d data identity mismatch", batchNumber)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	correlateCmd.Flags().StringVarP(&correlateInFlag, "in", "i", "./data/raw_prs.jsonl", "Input JSONL file")
	correlateCmd.Flags().StringVarP(&correlateOutFlag, "out", "o", "./data/correlated.jsonl", "Single output JSONL file (non-batch mode only)")
	correlateCmd.Flags().StringVar(&correlateRepoFlag, "repo", "", "Target repository (defaults to repo in input JSONL)")
	correlateCmd.Flags().StringVar(&correlateGitDirFlag, "git-dir", "", "Path to local git bare clone")
	correlateCmd.Flags().StringVar(&correlateObservationEndFlag, "observation-end", "", "Observation cut-off timestamp")
	correlateCmd.Flags().IntVar(&correlateWorkersFlag, "workers", 0, "Parallel workers (batch mode defaults to min(CPUs, 4))")
	correlateCmd.Flags().StringVar(&correlateBatchDirFlag, "batch-dir", "", "Directory for immutable resumable batch checkpoints")
	correlateCmd.Flags().IntVar(&correlateBatchSizeFlag, "batch-size", 50, "PRs per checkpoint when --batch-dir is used")
}
