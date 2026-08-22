package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"pr-analysis/internal/config"
	"pr-analysis/internal/correlator"
	"pr-analysis/internal/gitx"
	"pr-analysis/internal/storage"
)

var (
	correlateInFlag             string
	correlateOutFlag            string
	correlateRepoFlag           string
	correlateGitDirFlag         string
	correlateObservationEndFlag string
)

var correlateCmd = &cobra.Command{
	Use:   "correlate",
	Short: "Extract retrospective corrective signals from git history",
	Long:  `Scans post-merge commit history and issues up to observation-end to correlate original PRs with Fixes:, reverts, regression mentions, and symbol overlaps.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Load config if available
		var cfg *config.Config
		if _, err := os.Stat(configFile); err == nil {
			loaded, err := config.LoadConfig(configFile)
			if err != nil {
				return fmt.Errorf("failed to load config %q: %w", configFile, err)
			}
			cfg = loaded
		} else {
			cfg = &config.Config{}
		}

		inFile := "./data/raw_prs.jsonl"
		if correlateInFlag != "" {
			inFile = correlateInFlag
		}

		outFile := "./data/correlated.jsonl"
		if correlateOutFlag != "" {
			outFile = correlateOutFlag
		}

		records, err := storage.ReadJSONL(inFile)
		if err != nil {
			return fmt.Errorf("failed to read input records from %q: %w", inFile, err)
		}
		totalRecords := len(records)
		if totalRecords == 0 {
			return fmt.Errorf("no records found in input file %q", inFile)
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
				return fmt.Errorf("invalid --observation-end: %w", err)
			}
			obsEnd = parsed
		}
		if obsEnd.IsZero() {
			obsEnd = time.Now().UTC()
		}

		// Find earliest T0 across all PRs
		earliestT0 := records[0].Original.MergedAt
		for _, r := range records {
			if r.Original.MergedAt.Before(earliestT0) {
				earliestT0 = r.Original.MergedAt
			}
		}

		fmt.Printf("Scanning post-merge git log from %s up to observation end %s...\n",
			earliestT0.Format("2006-01-02"), obsEnd.Format("2006-01-02"))

		commits, err := gitRepo.CommitsAfter(ctx, earliestT0, obsEnd)
		if err != nil {
			return fmt.Errorf("failed to scan git commits from %s: %w", gitDir, err)
		}
		fmt.Printf("Loaded %d post-merge commits.\n", len(commits))

		corr := correlator.NewCorrelator(gitRepo)

		numWorkers := runtime.NumCPU()
		if numWorkers < 1 {
			numWorkers = 4
		}
		fmt.Printf("Correlating %d PRs using %d parallel workers...\n", totalRecords, numWorkers)

		var processedCount int64
		var strongCount int64
		var mediumCount int64

		jobs := make(chan int, totalRecords)
		var wg sync.WaitGroup

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					evidence := corr.CorrelatePR(ctx, &records[idx].Original, commits, obsEnd)
					records[idx].Retrospective = evidence
					records[idx].Provenance.ObservationEnd = obsEnd

					if len(evidence.StrongSignals) > 0 {
						atomic.AddInt64(&strongCount, 1)
					} else if len(evidence.MediumSignals) > 0 {
						atomic.AddInt64(&mediumCount, 1)
					}

					cur := atomic.AddInt64(&processedCount, 1)
					if cur%100 == 0 || int(cur) == totalRecords {
						fmt.Printf("Correlated %d/%d PRs (%5.1f%%)...\n",
							cur, totalRecords, float64(cur)/float64(totalRecords)*100)
					}
				}
			}()
		}

		for i := 0; i < totalRecords; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		if err := storage.WriteJSONL(outFile, records); err != nil {
			return fmt.Errorf("failed to write correlated records to %q: %w", outFile, err)
		}

		fmt.Printf("Successfully correlated %d PRs (%d with strong fix signals, %d with medium signals)\nSaved to %s\n",
			totalRecords, strongCount, mediumCount, outFile)

		return nil
	},
}

func init() {
	correlateCmd.Flags().StringVarP(&correlateInFlag, "in", "i", "./data/raw_prs.jsonl", "Input JSONL file")
	correlateCmd.Flags().StringVarP(&correlateOutFlag, "out", "o", "./data/correlated.jsonl", "Output JSONL file")
	correlateCmd.Flags().StringVar(&correlateRepoFlag, "repo", "", "Target repository (defaults to repo in input JSONL)")
	correlateCmd.Flags().StringVar(&correlateGitDirFlag, "git-dir", "", "Path to local git bare clone")
	correlateCmd.Flags().StringVar(&correlateObservationEndFlag, "observation-end", "", "Observation cut-off timestamp")
}
