package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"miner/internal/collector"
	"miner/internal/storage"
)

var refreshBodyEditsFlags struct {
	repo, cacheDir, prs, token string
}

var refreshBodyEditsCmd = &cobra.Command{
	Use:           "refresh-body-edits",
	SilenceUsage:  true,
	SilenceErrors: true,
	Short:         "Fill in the PR body's edit history on already cached bundles",
	Long: `For each PR, fetches the body's edit history (GraphQL userContentEdits) and rewrites
the cached bundle with that section filled in. Comments, commits, and issue events are
not re-fetched, and fetched_at is preserved: this is the per-PR re-fetch that lets a
cache collected before bundle 1.1.0 serve reviewer-metadata/v2 without a full re-collect.

A fetch failure is recorded in the bundle (body_edits_complete=false, body_edits_error)
and reported; it never aborts the run. The command exits non-zero if any PR ended
incomplete, so a cohort refresh cannot be mistaken for a clean one.

--prs accepts a comma-separated list of PR numbers, a path to a file with one number
per line, or a path to a cohort-manifest.json, whose pairs are refreshed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := refreshBodyEditsFlags
		parts := strings.Split(f.repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("--repo must be owner/repo")
		}
		prs, err := parsePRSelection(f.prs)
		if err != nil {
			return err
		}
		if len(prs) == 0 {
			return fmt.Errorf("--prs selected no PRs")
		}
		token := f.token
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		client := collector.NewGitHubClient(token)
		coll := collector.NewCollector(client, storage.NewDiskCache(f.cacheDir), nil)

		incomplete := 0
		for _, pr := range prs {
			complete, err := coll.RefreshBodyEdits(context.Background(), parts[0], parts[1], pr)
			if err != nil {
				return fmt.Errorf("PR #%d: %w", pr, err)
			}
			if !complete {
				incomplete++
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshed body edit history for %d PRs in %s: %d complete, %d incomplete\n",
			len(prs), f.cacheDir, len(prs)-incomplete, incomplete)
		if incomplete > 0 {
			return fmt.Errorf("%d of %d PRs have incomplete body edit history (see body_edits_error in each bundle)", incomplete, len(prs))
		}
		return nil
	},
}

// parsePRSelection accepts the forms the cohort tools produce: a comma list, a file
// of one PR per line, or a cohort manifest whose pairs name the cohort's PRs.
func parsePRSelection(arg string) ([]int, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return nil, fmt.Errorf("--prs is required")
	}
	set := map[int]bool{}
	add := func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid PR number %q", s)
		}
		set[n] = true
		return nil
	}
	if st, err := os.Stat(arg); err == nil && !st.IsDir() {
		data, err := os.ReadFile(arg)
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "{") {
			var m struct {
				SchemaVersion string `json:"schema_version"`
				Pairs         []struct {
					Positive int `json:"positive"`
					Negative int `json:"negative"`
				} `json:"pairs"`
			}
			if err := json.Unmarshal(data, &m); err != nil {
				return nil, fmt.Errorf("parse %q as a cohort manifest: %w", arg, err)
			}
			if !strings.HasPrefix(m.SchemaVersion, "cohort-manifest/") {
				return nil, fmt.Errorf("%q is not a cohort manifest (schema_version %q)", arg, m.SchemaVersion)
			}
			for _, p := range m.Pairs {
				set[p.Positive] = true
				set[p.Negative] = true
			}
		} else {
			for _, line := range strings.Split(trimmed, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if err := add(line); err != nil {
					return nil, fmt.Errorf("%s: %w", arg, err)
				}
			}
		}
	} else {
		for _, part := range strings.Split(arg, ",") {
			if err := add(part); err != nil {
				return nil, err
			}
		}
	}
	prs := make([]int, 0, len(set))
	for n := range set {
		prs = append(prs, n)
	}
	sort.Ints(prs)
	return prs, nil
}

func init() {
	f := &refreshBodyEditsFlags
	refreshBodyEditsCmd.Flags().StringVar(&f.repo, "repo", "", "Repository as owner/repo")
	refreshBodyEditsCmd.Flags().StringVar(&f.cacheDir, "cache-dir", "", "Collect-time cache root")
	refreshBodyEditsCmd.Flags().StringVar(&f.prs, "prs", "", "PR numbers (comma list), a file of one per line, or a cohort-manifest.json")
	refreshBodyEditsCmd.Flags().StringVar(&f.token, "token", "", "GitHub token (default: GITHUB_TOKEN)")
	for _, name := range []string{"repo", "cache-dir", "prs"} {
		_ = refreshBodyEditsCmd.MarkFlagRequired(name)
	}
}
