package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/go-github/v62/github"
	"golang.org/x/oauth2"
)

// GitHubClient provides rate-limit-aware access to GitHub's REST API.
type GitHubClient struct {
	Client *github.Client
	Token  string
}

// NewGitHubClient creates an authenticated or unauthenticated GitHub client.
func NewGitHubClient(token string) *GitHubClient {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	var tc *http.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		tc = oauth2.NewClient(context.Background(), ts)
	}

	client := github.NewClient(tc)
	return &GitHubClient{
		Client: client,
		Token:  token,
	}
}

// CheckRateLimit queries current remaining rate limit quota.
func (c *GitHubClient) CheckRateLimit(ctx context.Context) (*github.Rate, error) {
	limits, _, err := c.Client.RateLimit.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check rate limits: %w", err)
	}
	return limits.GetCore(), nil
}

// RawPRBundle contains the full verbatim API responses for a PR.
type RawPRBundle struct {
	PR             *github.PullRequest          `json:"pull_request"`
	IssueComments  []*github.IssueComment       `json:"issue_comments,omitempty"`
	ReviewComments []*github.PullRequestComment `json:"review_comments,omitempty"`
	Commits        []*github.RepositoryCommit   `json:"commits,omitempty"`
	FetchedAt      time.Time                    `json:"fetched_at"`
}

// FetchPRBundle downloads all relevant metadata and comments for a PR.
func (c *GitHubClient) FetchPRBundle(ctx context.Context, owner, repo string, prNumber int) (*RawPRBundle, []byte, error) {
	pr, resp, err := c.Client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch PR #%d: %w", prNumber, err)
	}

	// Fetch issue comments
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	issueComments, _, _ := c.Client.Issues.ListComments(ctx, owner, repo, prNumber, opt)

	// Fetch commits in PR
	commitOpt := &github.ListOptions{PerPage: 100}
	commits, _, _ := c.Client.PullRequests.ListCommits(ctx, owner, repo, prNumber, commitOpt)

	bundle := &RawPRBundle{
		PR:            pr,
		IssueComments: issueComments,
		Commits:       commits,
		FetchedAt:     time.Now().UTC(),
	}

	_ = resp // used for headers if needed
	rawJSON, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal raw PR bundle: %w", err)
	}

	return bundle, rawJSON, nil
}
