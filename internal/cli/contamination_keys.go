package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"miner/internal/collector"
	"miner/internal/contamkeys"
	"miner/internal/gitx"
	"miner/internal/storage"
)

var contaminationKeysFlags struct {
	input, cacheDir, gitDir, out, cutoff, token string
	offline                                     bool
}

var contaminationKeysCmd = &cobra.Command{
	Use:           "contamination-keys",
	SilenceUsage:  true,
	SilenceErrors: true,
	Short:         "Write per-case contamination keys (fixing commits, PRs, CVEs, post-merge discussion) for the engine's scan",
	Long: `For every case in a cohort.jsonl, writes <out>/<case_id>.json: the fixing commit SHAs
(all tiers, from the record's strong and medium signals), the PRs that carried those
commits, any CVE identifiers the fixes mention, and the verbatim post-merge discussion —
fixing commit messages, fixing PR titles/bodies/comments/review comments, issues the fixes
reference by #N, and comments on the case's own PR dated after the cutoff. Negatives get a
file too, with empty fixing lists and their own post-merge comments.

Everything fetched is written under <cache-dir>/github/<owner>_<repo>/fixes/ so a second
run is offline and reproducible (--offline refuses any uncached fetch). The case id is
derived exactly as prospective-export derives it (repository, PR, cutoff = merged_at
unless --cutoff), so file names match the bundles.

A source that cannot be fetched never aborts the run: it is listed under
provenance.incomplete in that case's file, and the command exits non-zero if any file is
incomplete, so a partial keys set cannot be mistaken for a complete one.

OUTPUT IS ORACLE-GRADE, EVALUATOR-ONLY MATERIAL. It names the fix and quotes the
discussion of the defect. Never place it under a prospective bundle root or give it to a
reviewer or either coder role.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := contaminationKeysFlags
		cases, err := contamkeys.ReadCases(f.input)
		if err != nil {
			return err
		}
		if len(cases) == 0 {
			return fmt.Errorf("no cases in %q", f.input)
		}
		var cutoff time.Time
		if f.cutoff != "" {
			t, err := time.Parse(time.RFC3339, f.cutoff)
			if err != nil {
				return fmt.Errorf("invalid --cutoff (RFC3339 required): %w", err)
			}
			cutoff = t
		}
		token := f.token
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		cache := storage.NewDiskCache(f.cacheDir)
		client := collector.NewGitHubClient(token)
		var repo *gitx.Repository
		if f.gitDir != "" {
			repo = gitx.OpenRepository(f.gitDir)
		}

		var keys []*contamkeys.Keys
		incomplete := 0
		fetchers := map[string]*contamkeys.GitHubFetcher{}
		for _, c := range cases {
			fx, ok := fetchers[c.Repository]
			if !ok {
				owner, name, err := contamkeys.SplitRepo(c.Repository)
				if err != nil {
					return err
				}
				fx = &contamkeys.GitHubFetcher{Client: client, Cache: cache, Repo: repo, Owner: owner, Name: name, Offline: f.offline}
				fetchers[c.Repository] = fx
			}
			k, err := contamkeys.Build(context.Background(), c, fx, contamkeys.Options{Cutoff: cutoff, Offline: f.offline})
			if err != nil {
				return fmt.Errorf("PR #%d: %w", c.PR, err)
			}
			if !k.Complete() {
				incomplete++
			}
			keys = append(keys, k)
		}
		if err := contamkeys.WriteAll(f.out, keys); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Wrote contamination keys for %d cases to %s: %d complete, %d incomplete\n", len(keys), f.out, len(keys)-incomplete, incomplete)
		if incomplete > 0 {
			return fmt.Errorf("%d of %d keys files are incomplete (see provenance.incomplete in each)", incomplete, len(keys))
		}
		return nil
	},
}

func init() {
	f := &contaminationKeysFlags
	contaminationKeysCmd.Flags().StringVarP(&f.input, "input", "i", "", "cohort.jsonl from cohort-export")
	contaminationKeysCmd.Flags().StringVar(&f.cacheDir, "cache-dir", "", "Collect-time cache root; fetched material is cached under fixes/")
	contaminationKeysCmd.Flags().StringVar(&f.gitDir, "git-dir", "", "Local Git mirror, for fixing commit messages")
	contaminationKeysCmd.Flags().StringVarP(&f.out, "out", "o", "", "New directory receiving one <case_id>.json per case")
	contaminationKeysCmd.Flags().StringVar(&f.cutoff, "cutoff", "", "Pin every case to one RFC3339 cutoff (default: each PR's merged_at)")
	contaminationKeysCmd.Flags().StringVar(&f.token, "token", "", "GitHub token (default: GITHUB_TOKEN)")
	contaminationKeysCmd.Flags().BoolVar(&f.offline, "offline", false, "Refuse any fetch that is not already cached")
	for _, name := range []string{"input", "cache-dir", "git-dir", "out"} {
		_ = contaminationKeysCmd.MarkFlagRequired(name)
	}
}
