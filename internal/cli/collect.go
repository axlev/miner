package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"pr-analysis/internal/collector"
	"pr-analysis/internal/config"
	"pr-analysis/internal/gitx"
	"pr-analysis/internal/storage"
)

var (
	repoFlag           string
	fromFlag           string
	toFlag             string
	observationEndFlag string
	cacheDirFlag       string
	gitDirFlag         string
	tokenFlag          string
	outFlag            string
	maxPRsFlag         int
)

var collectCmd = &cobra.Command{
	Use:   "collect",
	Short: "Harvest raw PR metadata, diffs, and comments",
	Long:  `Downloads PR metadata from GitHub API with verbatim local caching, syncs local Git repository, and generates raw PR JSONL records.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		// Load YAML config if present
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

		// Apply CLI overrides with precedence: CLI > YAML > Defaults
		repo := cfg.Mining.Repository
		if repoFlag != "" {
			repo = repoFlag
		}
		if repo == "" {
			return fmt.Errorf("--repo or mining.repository in config is required (e.g. FRRouting/frr)")
		}

		parts := strings.Split(repo, "/")
		if len(parts) != 2 {
			return fmt.Errorf("invalid repository %q (expected owner/repo format)", repo)
		}
		owner, repoName := parts[0], parts[1]

		cacheDir := "./data/cache"
		if cfg.Mining.CacheDir != "" {
			cacheDir = cfg.Mining.CacheDir
		}
		if cacheDirFlag != "" {
			cacheDir = cacheDirFlag
		}

		gitDir := filepath.Join(cacheDir, "repos", owner, repoName+".git")
		if cfg.Mining.GitDir != "" {
			gitDir = cfg.Mining.GitDir
		}
		if gitDirFlag != "" {
			gitDir = gitDirFlag
		}

		token := os.Getenv("GITHUB_TOKEN")
		if cfg.Mining.Token != "" {
			token = cfg.Mining.Token
		}
		if tokenFlag != "" {
			token = tokenFlag
		}

		var fromDate, toDate, obsEndDate time.Time
		if cfg.Mining.From != nil {
			fromDate = *cfg.Mining.From
		}
		if fromFlag != "" {
			parsed, err := time.Parse("2006-01-02", fromFlag)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, fromFlag)
			}
			if err != nil {
				return fmt.Errorf("invalid --from date format: %w", err)
			}
			fromDate = parsed
		}

		if cfg.Mining.To != nil {
			toDate = *cfg.Mining.To
		}
		if toFlag != "" {
			parsed, err := time.Parse("2006-01-02", toFlag)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, toFlag)
			}
			if err != nil {
				return fmt.Errorf("invalid --to date format: %w", err)
			}
			toDate = parsed
		}

		if cfg.Mining.ObservationEnd != nil {
			obsEndDate = *cfg.Mining.ObservationEnd
		}
		if observationEndFlag != "" {
			parsed, err := time.Parse("2006-01-02", observationEndFlag)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, observationEndFlag)
			}
			if err != nil {
				return fmt.Errorf("invalid --observation-end date format: %w", err)
			}
			obsEndDate = parsed
		}
		if obsEndDate.IsZero() {
			obsEndDate = time.Now().UTC()
		}

		outFile := "./data/raw_prs.jsonl"
		if outFlag != "" {
			outFile = outFlag
		}

		// 1. Ensure local Git mirror exists
		remoteURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repoName)
		gitRepo, err := gitx.EnsureClone(ctx, gitDir, remoteURL)
		if err != nil {
			fmt.Printf("Warning: failed to clone/open git repo: %v (continuing with API metadata only)\n", err)
		}

		// 2. Initialize GitHub client and disk cache
		ghClient := collector.NewGitHubClient(token)
		rate, err := ghClient.CheckRateLimit(ctx)
		if err == nil {
			fmt.Printf("GitHub API Status: %d/%d requests remaining (Resets in %v)\n",
				rate.Remaining, rate.Limit, time.Until(rate.Reset.Time).Round(time.Minute))
		} else {
			fmt.Printf("Note: Running without authenticated rate limit check (%v)\n", err)
		}

		cache := storage.NewDiskCache(cacheDir)
		col := collector.NewCollector(ghClient, cache, gitRepo)

		opts := collector.CollectOptions{
			Owner:          owner,
			Repo:           repoName,
			From:           fromDate,
			To:             toDate,
			MaxPRs:         maxPRsFlag,
			ObservationEnd: obsEndDate,
		}

		records, err := col.CollectMinedPRs(ctx, opts, cfg)
		if err != nil {
			return fmt.Errorf("collection failed: %w", err)
		}

		if err := storage.WriteJSONL(outFile, records); err != nil {
			return fmt.Errorf("failed to save JSONL output to %q: %w", outFile, err)
		}

		fmt.Printf("Successfully harvested and saved %d PR records to %s\n", len(records), outFile)
		return nil
	},
}

func init() {
	collectCmd.Flags().StringVar(&repoFlag, "repo", "", "Target repository (e.g. FRRouting/frr)")
	collectCmd.Flags().StringVar(&fromFlag, "from", "", "Mining start date (YYYY-MM-DD)")
	collectCmd.Flags().StringVar(&toFlag, "to", "", "Mining end date (YYYY-MM-DD)")
	collectCmd.Flags().StringVar(&observationEndFlag, "observation-end", "", "Observation cutoff timestamp for retrospective data")
	collectCmd.Flags().StringVar(&cacheDirFlag, "cache-dir", "", "Path to local cache directory")
	collectCmd.Flags().StringVar(&gitDirFlag, "git-dir", "", "Path to local git bare clone")
	collectCmd.Flags().StringVar(&tokenFlag, "token", "", "GitHub Personal Access Token")
	collectCmd.Flags().StringVarP(&outFlag, "out", "o", "./data/raw_prs.jsonl", "Output JSONL filepath")
	collectCmd.Flags().IntVar(&maxPRsFlag, "max", 0, "Maximum number of PRs to harvest (0 for unlimited)")
}
