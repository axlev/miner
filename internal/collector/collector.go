package collector

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/google/go-github/v62/github"
	"pr-analysis/internal/config"
	"pr-analysis/internal/gitx"
	"pr-analysis/internal/model"
	"pr-analysis/internal/storage"
)

var (
	// Issue reference regex in PR body e.g. "fixes #1234", "closes #5678", "issue #999"
	issueRefRegex = regexp.MustCompile(`(?i)(?:fixes|closes|resolves|issue|#)\s*#?([0-9]+)`)
)

// Collector coordinates fetching historical PRs, caching, and local git enrichment.
type Collector struct {
	client  *GitHubClient
	cache   *storage.DiskCache
	gitRepo *gitx.Repository
}

// NewCollector initializes a Collector instance.
func NewCollector(client *GitHubClient, cache *storage.DiskCache, gitRepo *gitx.Repository) *Collector {
	return &Collector{
		client:  client,
		cache:   cache,
		gitRepo: gitRepo,
	}
}

// CollectOptions defines query constraints for PR collection.
type CollectOptions struct {
	Owner          string
	Repo           string
	From           time.Time
	To             time.Time
	MaxPRs         int
	ObservationEnd time.Time
}

// CollectMinedPRs fetches all merged PRs within the date range, using cache where available.
func (c *Collector) CollectMinedPRs(ctx context.Context, opts CollectOptions, cfg *config.Config) ([]model.PRCandidateRecord, error) {
	fullName := fmt.Sprintf("%s/%s", opts.Owner, opts.Repo)
	fmt.Printf("Collecting all merged PRs for %s (Merged between %s and %s)...\n",
		fullName, opts.From.Format("2006-01-02"), opts.To.Format("2006-01-02"))

	targetPRMap := make(map[int]bool)
	var targetPRNumbers []int

	// Break date range into monthly intervals to prevent hitting GitHub's 1000-result search limit
	from := opts.From
	if from.IsZero() {
		from = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	to := opts.To
	if to.IsZero() {
		to = time.Now().UTC()
	}

	curStart := from
	for curStart.Before(to) {
		curEnd := curStart.AddDate(0, 1, 0).Add(-time.Second)
		if curEnd.After(to) {
			curEnd = to
		}

		query := fmt.Sprintf("repo:%s is:pr is:merged merged:%s..%s",
			fullName, curStart.Format("2006-01-02"), curEnd.Format("2006-01-02"))

		page := 1
		for {
			searchOpts := &github.SearchOptions{
				ListOptions: github.ListOptions{
					Page:    page,
					PerPage: 100,
				},
			}

			result, resp, err := c.client.Client.Search.Issues(ctx, query, searchOpts)
			if err != nil {
				return nil, fmt.Errorf("GitHub search failed for %q (page %d): %w", query, page, err)
			}

			for _, issue := range result.Issues {
				num := issue.GetNumber()
				if !targetPRMap[num] {
					targetPRMap[num] = true
					targetPRNumbers = append(targetPRNumbers, num)
				}
				if opts.MaxPRs > 0 && len(targetPRNumbers) >= opts.MaxPRs {
					break
				}
			}

			if opts.MaxPRs > 0 && len(targetPRNumbers) >= opts.MaxPRs {
				break
			}
			if resp.NextPage == 0 {
				break
			}
			page = resp.NextPage
		}

		if opts.MaxPRs > 0 && len(targetPRNumbers) >= opts.MaxPRs {
			break
		}

		curStart = curEnd.Add(time.Second)
	}

	fmt.Printf("Identified %d total merged PRs in date window.\n", len(targetPRNumbers))

	headSHA := ""
	if c.gitRepo != nil {
		headSHA, _ = c.gitRepo.HeadSHA(ctx)
	}

	var records []model.PRCandidateRecord

	for i, prNum := range targetPRNumbers {
		bundle, err := c.getOrFetchPR(ctx, opts.Owner, opts.Repo, prNum)
		if err != nil {
			fmt.Printf("Warning: error getting PR #%d: %v (skipping)\n", prNum, err)
			continue
		}

		pr := bundle.PR
		if pr.MergedAt == nil {
			continue
		}

		orig := c.buildOriginalPR(ctx, opts.Owner, opts.Repo, bundle)

		provenance := model.Provenance{
			MinerVersion:      "v1.0.0",
			ConfigHash:        cfg.ConfigHash,
			HarvestedAt:       time.Now().UTC(),
			ObservationEnd:    opts.ObservationEnd,
			TargetRepoHeadSHA: headSHA,
			GitHubAPIVersion:  "2022-11-28",
		}

		record := model.PRCandidateRecord{
			SchemaVersion: model.SchemaVersion,
			Original:      orig,
			Provenance:    provenance,
		}

		records = append(records, record)

		if (i+1)%50 == 0 || i+1 == len(targetPRNumbers) {
			fmt.Printf("Processed %d/%d PRs...\n", i+1, len(targetPRNumbers))
		}
	}

	return records, nil
}

func (c *Collector) getOrFetchPR(ctx context.Context, owner, repo string, prNum int) (*RawPRBundle, error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)

	if c.cache.HasPR(fullName, prNum) {
		var bundle RawPRBundle
		if err := c.cache.ReadPR(fullName, prNum, &bundle); err == nil && bundle.PR != nil {
			return &bundle, nil
		}
	}

	bundle, rawJSON, err := c.client.FetchPRBundle(ctx, owner, repo, prNum)
	if err != nil {
		return nil, err
	}

	if err := c.cache.WritePR(fullName, prNum, rawJSON); err != nil {
		fmt.Printf("Warning: failed to write cache for PR #%d: %v\n", prNum, err)
	}

	return bundle, nil
}

func (c *Collector) buildOriginalPR(ctx context.Context, owner, repo string, bundle *RawPRBundle) model.OriginalPR {
	pr := bundle.PR
	fullName := fmt.Sprintf("%s/%s", owner, repo)

	var labels []string
	for _, l := range pr.Labels {
		if l.Name != nil {
			labels = append(labels, *l.Name)
		}
	}

	var commitMsgs []string
	var commitSHAs []string
	for _, commit := range bundle.Commits {
		if commit.SHA != nil && *commit.SHA != "" {
			commitSHAs = append(commitSHAs, *commit.SHA)
		}
		if commit.Commit != nil && commit.Commit.Message != nil {
			commitMsgs = append(commitMsgs, *commit.Commit.Message)
		}
	}

	// Extract issue references from PR body
	issueMap := make(map[int]bool)
	matches := issueRefRegex.FindAllStringSubmatch(pr.GetBody(), -1)
	for _, m := range matches {
		if len(m) > 1 {
			if num, err := strconv.Atoi(m[1]); err == nil {
				issueMap[num] = true
			}
		}
	}
	var preMergeIssues []int
	for num := range issueMap {
		preMergeIssues = append(preMergeIssues, num)
	}

	baseSHA := pr.GetBase().GetSHA()
	headSHA := pr.GetHead().GetSHA()
	mergeSHA := pr.GetMergeCommitSHA()

	var changedFiles []model.ChangedFile
	var changedFunctions []string
	var changedLocations []model.ChangedFunctionLocation

	// If local git clone is available, compute high-accuracy merge-base diff stats and C symbols
	if c.gitRepo != nil && baseSHA != "" && headSHA != "" {
		if diffRes, err := c.gitRepo.DiffPR(ctx, baseSHA, headSHA); err == nil {
			changedFiles = diffRes.Files
			changedFunctions = diffRes.Symbols
			changedLocations = diffRes.FunctionLocations
		}
	}

	return model.OriginalPR{
		Repository:               fullName,
		Number:                   pr.GetNumber(),
		Title:                    pr.GetTitle(),
		Body:                     pr.GetBody(),
		Author:                   pr.GetUser().GetLogin(),
		CreatedAt:                pr.GetCreatedAt().Time,
		MergedAt:                 pr.GetMergedAt().Time,
		BaseRef:                  pr.GetBase().GetRef(),
		BaseSHA:                  baseSHA,
		HeadSHA:                  headSHA,
		MergeCommitSHA:           mergeSHA,
		CommitSHAs:               commitSHAs,
		Labels:                   labels,
		ChangedFiles:             changedFiles,
		ChangedFunctions:         changedFunctions,
		ChangedFunctionLocations: changedLocations,
		PreMergeIssueRefs:        preMergeIssues,
		CommitCount:              pr.GetCommits(),
		CommitMessages:           commitMsgs,
	}
}
