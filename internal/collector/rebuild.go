package collector

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"miner/internal/config"
	"miner/internal/gitx"
	"miner/internal/model"
	"miner/internal/storage"
)

// RebuildRawOptions defines parameters for offline PR reconstruction.
type RebuildRawOptions struct {
	SeedFile       string
	CacheDir       string
	GitDir         string
	Repo           string
	ObservationEnd time.Time
}

// RebuildRawSummary captures validation metrics for the rebuild operation.
type RebuildRawSummary struct {
	SeedCount                   int
	RebuiltCount                int
	MissingCacheCount           int
	DiffErrorCount              int
	RecordsWithChangedFunctions int
	RecordsWithPathLocations    int
}

// RebuildRawOffline rebuilds OriginalPR records strictly from local cache and bare Git repository.
// It executes 0 network calls and fails explicitly if cache or diffs are unavailable.
func RebuildRawOffline(ctx context.Context, opts RebuildRawOptions, cfg *config.Config) ([]model.PRCandidateRecord, *RebuildRawSummary, error) {
	summary := &RebuildRawSummary{}

	// 1. Read seed file to get exact cohort membership
	if opts.SeedFile == "" {
		return nil, summary, fmt.Errorf("seed file path is required")
	}
	seedRecords, err := storage.ReadJSONL(opts.SeedFile)
	if err != nil {
		return nil, summary, fmt.Errorf("failed to read seed JSONL from %q: %w", opts.SeedFile, err)
	}
	if len(seedRecords) == 0 {
		return nil, summary, fmt.Errorf("seed JSONL %q contains 0 records", opts.SeedFile)
	}

	// 2. Extract, deduplicate, and sort PR numbers deterministically
	prMap := make(map[int]bool)
	var uniquePRs []int
	detectedRepo := ""
	for _, rec := range seedRecords {
		num := rec.Original.Number
		if num > 0 && !prMap[num] {
			prMap[num] = true
			uniquePRs = append(uniquePRs, num)
		}
		if detectedRepo == "" && rec.Original.Repository != "" {
			detectedRepo = rec.Original.Repository
		}
	}
	sort.Ints(uniquePRs)
	summary.SeedCount = len(uniquePRs)
	if summary.SeedCount == 0 {
		return nil, summary, fmt.Errorf("no valid PR numbers found in seed file %q", opts.SeedFile)
	}

	// Determine repository
	repo := opts.Repo
	if repo == "" {
		if cfg != nil && cfg.Mining.Repository != "" {
			repo = cfg.Mining.Repository
		} else if detectedRepo != "" {
			repo = detectedRepo
		} else {
			repo = "FRRouting/frr"
		}
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return nil, summary, fmt.Errorf("invalid repository format %q (expected owner/repo)", repo)
	}
	owner, repoName := parts[0], parts[1]

	// Determine observation end
	obsEnd := opts.ObservationEnd
	if obsEnd.IsZero() {
		if cfg != nil && cfg.Mining.ObservationEnd != nil {
			obsEnd = *cfg.Mining.ObservationEnd
		} else if len(seedRecords) > 0 && !seedRecords[0].Provenance.ObservationEnd.IsZero() {
			obsEnd = seedRecords[0].Provenance.ObservationEnd
		} else {
			obsEnd = time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
		}
	}

	// Determine config hash
	configHash := ""
	if cfg != nil {
		configHash = cfg.ConfigHash
	} else if len(seedRecords) > 0 {
		configHash = seedRecords[0].Provenance.ConfigHash
	}

	// 3. Open local Git repository without fetching
	if opts.GitDir == "" {
		return nil, summary, fmt.Errorf("git-dir is required")
	}
	gitRepo := gitx.OpenRepository(opts.GitDir)
	headSHA, err := gitRepo.HeadSHA(ctx)
	if err != nil {
		return nil, summary, fmt.Errorf("local git repository unavailable at %q: %w", opts.GitDir, err)
	}

	// 4. Initialize Disk Cache
	cache := storage.NewDiskCache(opts.CacheDir)
	col := NewCollector(nil, cache, gitRepo)

	var records []model.PRCandidateRecord
	var missingPRs []int
	var diffErrorPRs []int

	for _, prNum := range uniquePRs {
		if !cache.HasPR(repo, prNum) {
			summary.MissingCacheCount++
			missingPRs = append(missingPRs, prNum)
			continue
		}

		var bundle RawPRBundle
		if err := cache.ReadPR(repo, prNum, &bundle); err != nil || bundle.PR == nil {
			summary.MissingCacheCount++
			missingPRs = append(missingPRs, prNum)
			continue
		}

		baseSHA := bundle.PR.GetBase().GetSHA()
		headSHAVal := bundle.PR.GetHead().GetSHA()
		if baseSHA == "" || headSHAVal == "" {
			summary.DiffErrorCount++
			diffErrorPRs = append(diffErrorPRs, prNum)
			continue
		}

		diffRes, err := gitRepo.DiffPR(ctx, baseSHA, headSHAVal)
		if err != nil {
			summary.DiffErrorCount++
			diffErrorPRs = append(diffErrorPRs, prNum)
			continue
		}

		orig := col.buildOriginalPR(ctx, owner, repoName, &bundle)
		orig.ChangedFiles = diffRes.Files
		orig.ChangedFunctions = diffRes.Symbols
		orig.ChangedFunctionLocations = diffRes.FunctionLocations

		if len(orig.ChangedFunctions) > 0 {
			summary.RecordsWithChangedFunctions++
		}
		if len(orig.ChangedFunctionLocations) > 0 {
			summary.RecordsWithPathLocations++
		}

		provenance := model.Provenance{
			MinerVersion:      "v1.0.0",
			ConfigHash:        configHash,
			HarvestedAt:       time.Now().UTC(),
			ObservationEnd:    obsEnd,
			TargetRepoHeadSHA: headSHA,
			GitHubAPIVersion:  "2022-11-28",
		}

		records = append(records, model.PRCandidateRecord{
			SchemaVersion: model.SchemaVersion,
			Original:      orig,
			Provenance:    provenance,
		})
	}

	summary.RebuiltCount = len(records)

	// Strict error validation: fail if any cache or diff failed
	if len(missingPRs) > 0 {
		return nil, summary, fmt.Errorf("missing or unreadable cache for %d PRs (first 10: %v)", len(missingPRs), truncateInts(missingPRs, 10))
	}
	if len(diffErrorPRs) > 0 {
		return nil, summary, fmt.Errorf("diff calculation failed for %d PRs (first 10: %v)", len(diffErrorPRs), truncateInts(diffErrorPRs, 10))
	}
	if summary.RebuiltCount != summary.SeedCount {
		return nil, summary, fmt.Errorf("output membership count mismatch: seed has %d PRs, but rebuilt %d", summary.SeedCount, summary.RebuiltCount)
	}

	// Deterministic sorting by PR number ascending
	sort.Slice(records, func(i, j int) bool {
		return records[i].Original.Number < records[j].Original.Number
	})

	return records, summary, nil
}

func truncateInts(nums []int, limit int) []int {
	if len(nums) <= limit {
		return nums
	}
	return nums[:limit]
}
