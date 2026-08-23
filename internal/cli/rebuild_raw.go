package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"pr-analysis/internal/collector"
	"pr-analysis/internal/config"
	"pr-analysis/internal/storage"
)

var (
	rebuildSeedFlag           string
	rebuildCacheDirFlag       string
	rebuildGitDirFlag         string
	rebuildOutFlag            string
	rebuildRepoFlag           string
	rebuildObservationEndFlag string
)

var rebuildRawCmd = &cobra.Command{
	Use:   "rebuild-raw",
	Short: "Rebuild raw OriginalPR records offline from cache and local git repo",
	Long: `Rebuilds OriginalPR records strictly using local disk-cached PR bundles and local Git mirror,
without making GitHub requests or network Git fetches. Uses repaired merge-base PR diffing and
extracts path-associated function locations.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()

		if rebuildSeedFlag == "" {
			return fmt.Errorf("--seed flag is required (e.g. data/frr_2024_raw.jsonl)")
		}

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

		// Determine repository
		repo := cfg.Mining.Repository
		if rebuildRepoFlag != "" {
			repo = rebuildRepoFlag
		}

		// Determine cache dir
		cacheDir := "./data/cache"
		if cfg.Mining.CacheDir != "" {
			cacheDir = cfg.Mining.CacheDir
		}
		if rebuildCacheDirFlag != "" {
			cacheDir = rebuildCacheDirFlag
		}

		// Determine git dir
		gitDir := "./data/repos/FRRouting/frr.git"
		if cfg.Mining.GitDir != "" {
			gitDir = cfg.Mining.GitDir
		}
		if rebuildGitDirFlag != "" {
			gitDir = rebuildGitDirFlag
		}

		// Determine output file
		outFile := "./data/frr_2024_raw_corrected_20260821.jsonl"
		if rebuildOutFlag != "" {
			outFile = rebuildOutFlag
		}

		// Determine observation end
		var obsEndDate time.Time
		if cfg.Mining.ObservationEnd != nil {
			obsEndDate = *cfg.Mining.ObservationEnd
		}
		if rebuildObservationEndFlag != "" {
			parsed, err := time.Parse(time.RFC3339, rebuildObservationEndFlag)
			if err != nil {
				parsed, err = time.Parse("2006-01-02", rebuildObservationEndFlag)
			}
			if err != nil {
				return fmt.Errorf("invalid --observation-end date format: %w", err)
			}
			obsEndDate = parsed
		}

		opts := collector.RebuildRawOptions{
			SeedFile:       rebuildSeedFlag,
			CacheDir:       cacheDir,
			GitDir:         gitDir,
			Repo:           repo,
			ObservationEnd: obsEndDate,
		}

		records, summary, err := collector.RebuildRawOffline(ctx, opts, cfg)
		if err != nil {
			return fmt.Errorf("offline rebuild failed: %w", err)
		}

		if err := storage.WriteJSONL(outFile, records); err != nil {
			return fmt.Errorf("failed to save rebuilt JSONL output to %q: %w", outFile, err)
		}

		fmt.Println("============================================================")
		fmt.Println("Raw Rebuild Validation Summary")
		fmt.Println("============================================================")
		fmt.Printf("Seed Count:                          %d\n", summary.SeedCount)
		fmt.Printf("Rebuilt Count:                       %d\n", summary.RebuiltCount)
		fmt.Printf("Missing Cache Count:                 %d\n", summary.MissingCacheCount)
		fmt.Printf("Diff Error Count:                    %d\n", summary.DiffErrorCount)
		fmt.Printf("Records with Changed Functions:      %d\n", summary.RecordsWithChangedFunctions)
		fmt.Printf("Records with Path Function Locs:     %d\n", summary.RecordsWithPathLocations)
		fmt.Println("============================================================")
		fmt.Printf("Successfully wrote %d rebuilt records to %s\n", len(records), outFile)

		return nil
	},
}

func init() {
	rebuildRawCmd.Flags().StringVar(&rebuildSeedFlag, "seed", "", "Path to seed raw PR JSONL file (required)")
	rebuildRawCmd.Flags().StringVar(&rebuildCacheDirFlag, "cache-dir", "", "Path to local cache directory")
	rebuildRawCmd.Flags().StringVar(&rebuildGitDirFlag, "git-dir", "", "Path to local git bare clone")
	rebuildRawCmd.Flags().StringVarP(&rebuildOutFlag, "out", "o", "./data/frr_2024_raw_corrected_20260821.jsonl", "Output JSONL filepath")
	rebuildRawCmd.Flags().StringVar(&rebuildRepoFlag, "repo", "", "Target repository (e.g. FRRouting/frr)")
	rebuildRawCmd.Flags().StringVar(&rebuildObservationEndFlag, "observation-end", "", "Observation cutoff timestamp")
}
