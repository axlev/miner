package contamkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/gitx"
	"miner/internal/storage"
)

// FixesNamespace is the cache namespace for everything fetched for keys files. It
// is separate from prs/ so the collect cache keeps its meaning: prs/ holds what was
// gathered for candidates at collect time; fixes/ holds post-cutoff material
// gathered per case.
const FixesNamespace = "fixes"

// GitHubFetcher answers from the cache first and the provider second, writing
// every provider answer back so a second run is offline and reproducible.
type GitHubFetcher struct {
	Client  *collector.GitHubClient
	Cache   *storage.DiskCache
	Repo    *gitx.Repository
	Owner   string
	Name    string
	Offline bool
}

func (g *GitHubFetcher) fullName() string { return g.Owner + "/" + g.Name }

func (g *GitHubFetcher) CommitMessage(ctx context.Context, sha string) (string, error) {
	if g.Repo == nil {
		return "", fmt.Errorf("no git mirror")
	}
	return g.Repo.CommitMessage(ctx, sha)
}

type cachedPRList struct {
	SHA       string    `json:"sha"`
	Numbers   []int     `json:"numbers"`
	FetchedAt time.Time `json:"fetched_at"`
}

func (g *GitHubFetcher) PullRequestsForCommit(ctx context.Context, sha string) ([]int, error) {
	name := "commit_" + sha + "_prs.json"
	var cached cachedPRList
	if g.Cache.HasFile(g.fullName(), FixesNamespace, name) {
		if err := g.Cache.ReadFile(g.fullName(), FixesNamespace, name, &cached); err == nil {
			return cached.Numbers, nil
		}
	}
	if g.Offline {
		return nil, fmt.Errorf("not cached and --offline")
	}
	var nums []int
	opt := &github.ListOptions{PerPage: 100}
	for {
		prs, resp, err := g.Client.Client.PullRequests.ListPullRequestsWithCommit(ctx, g.Owner, g.Name, sha, opt)
		if err != nil {
			return nil, err
		}
		for _, p := range prs {
			nums = append(nums, p.GetNumber())
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	if nums == nil {
		nums = []int{}
	}
	raw, _ := json.MarshalIndent(cachedPRList{SHA: sha, Numbers: nums, FetchedAt: time.Now().UTC()}, "", "  ")
	if err := g.Cache.WriteFile(g.fullName(), FixesNamespace, name, raw); err != nil {
		return nil, err
	}
	return nums, nil
}

func (g *GitHubFetcher) PullRequestBundle(ctx context.Context, number int) (*collector.RawPRBundle, error) {
	name := fmt.Sprintf("pr_%d.json", number)
	if g.Cache.HasFile(g.fullName(), FixesNamespace, name) {
		var b collector.RawPRBundle
		if err := g.Cache.ReadFile(g.fullName(), FixesNamespace, name, &b); err == nil && b.PR != nil {
			return &b, nil
		}
	}
	if g.Offline {
		return nil, fmt.Errorf("not cached and --offline")
	}
	b, raw, err := g.Client.FetchPRBundle(ctx, g.Owner, g.Name, number)
	if err != nil {
		return nil, err
	}
	if err := g.Cache.WriteFile(g.fullName(), FixesNamespace, name, raw); err != nil {
		return nil, err
	}
	return b, nil
}

type cachedIssue struct {
	Number    int                    `json:"number"`
	IsPR      bool                   `json:"is_pull_request"`
	Issue     *github.Issue          `json:"issue,omitempty"`
	Comments  []*github.IssueComment `json:"comments,omitempty"`
	Complete  bool                   `json:"comments_complete"`
	FetchedAt time.Time              `json:"fetched_at"`
}

func (g *GitHubFetcher) Issue(ctx context.Context, number int) (*IssueThread, bool, error) {
	name := fmt.Sprintf("issue_%d.json", number)
	var cached cachedIssue
	have := false
	if g.Cache.HasFile(g.fullName(), FixesNamespace, name) {
		if err := g.Cache.ReadFile(g.fullName(), FixesNamespace, name, &cached); err == nil {
			have = true
		}
	}
	if !have {
		if g.Offline {
			return nil, false, fmt.Errorf("not cached and --offline")
		}
		issue, _, err := g.Client.Client.Issues.Get(ctx, g.Owner, g.Name, number)
		if err != nil {
			return nil, false, err
		}
		cached = cachedIssue{Number: number, IsPR: issue.PullRequestLinks != nil, Issue: issue, FetchedAt: time.Now().UTC()}
		if !cached.IsPR {
			opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
			cached.Complete = true
			for {
				page, resp, err := g.Client.Client.Issues.ListComments(ctx, g.Owner, g.Name, number, opt)
				if err != nil {
					cached.Comments = nil
					cached.Complete = false
					break
				}
				cached.Comments = append(cached.Comments, page...)
				if resp == nil || resp.NextPage == 0 {
					break
				}
				opt.Page = resp.NextPage
			}
		}
		raw, _ := json.MarshalIndent(cached, "", "  ")
		if err := g.Cache.WriteFile(g.fullName(), FixesNamespace, name, raw); err != nil {
			return nil, false, err
		}
	}
	if cached.IsPR {
		return nil, true, nil
	}
	th := &IssueThread{Number: number, Complete: cached.Complete}
	if cached.Issue != nil {
		th.Title = cached.Issue.GetTitle()
		th.Body = cached.Issue.GetBody()
		th.HTMLURL = cached.Issue.GetHTMLURL()
		th.Author = cached.Issue.GetUser().GetLogin()
	}
	for _, c := range cached.Comments {
		th.Comments = append(th.Comments, IssueComment{HTMLURL: c.GetHTMLURL(), Author: c.GetUser().GetLogin(), CreatedAt: c.GetCreatedAt().Time, Body: c.GetBody()})
	}
	return th, false, nil
}

func (g *GitHubFetcher) OwnBundle(number int) (*collector.RawPRBundle, error) {
	if !g.Cache.HasPR(g.fullName(), number) {
		return nil, fmt.Errorf("PR #%d is not in the collect cache", number)
	}
	var b collector.RawPRBundle
	if err := g.Cache.ReadPR(g.fullName(), number, &b); err != nil {
		return nil, err
	}
	if b.PR == nil || b.PR.GetNumber() != number {
		return nil, fmt.Errorf("cached bundle does not describe PR #%d", number)
	}
	return &b, nil
}

// SplitRepo parses owner/repo.
func SplitRepo(full string) (string, string, error) {
	owner, name, ok := strings.Cut(full, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("repository %q is not owner/repo", full)
	}
	return owner, name, nil
}
