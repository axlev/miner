package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		if len(records) == 0 {
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
		fmt.Printf("Scanned %d post-merge commits.\n", len(commits))

		corr := correlator.NewCorrelator(gitRepo)

		strongCount := 0
		mediumCount := 0

		for i := range records {
			records[i].Retrospective = corr.CorrelatePR(ctx, &records[i].Original, commits, obsEnd)
			records[i].Provenance.ObservationEnd = obsEnd

			if len(records[i].Retrospective.StrongSignals) > 0 {
				strongCount++
			} else if len(records[i].Retrospective.MediumSignals) > 0 {
				mediumCount++
			}
		}

		if err := storage.WriteJSONL(outFile, records); err != nil {
			return fmt.Errorf("failed to write correlated records to %q: %w", outFile, err)
		}

		fmt.Printf("Successfully correlated %d PRs (%d with strong fix signals, %d with medium signals)\nSaved to %s\n",
			len(records), strongCount, mediumCount, outFile)

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
