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

// GitHubClient provides rate-limit-aware access to GitHub's REST API, and the
// authenticated HTTP client for the one GraphQL query the miner makes.
type GitHubClient struct {
	Client *github.Client
	Token  string
	// HTTP is the authenticated client used for GraphQL; nil means http.DefaultClient.
	HTTP *http.Client
	// GraphQLURL overrides DefaultGraphQLURL, for tests.
	GraphQLURL string
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
		HTTP:   tc,
	}
}

// fetchAllIssueComments pages through the PR's conversation comments. On any page
// error the partial list is discarded and complete is false, mirroring issue events.
func (c *GitHubClient) fetchAllIssueComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.IssueComment, bool) {
	var all []*github.IssueComment
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, resp, err := c.Client.Issues.ListComments(ctx, owner, repo, prNumber, opt)
		if err != nil {
			fmt.Printf("Warning: PR #%d: issue comments not fetched to completion (%v); bundle written with issue_comments_complete=false\n", prNumber, err)
			return nil, false
		}
		all = append(all, page...)
		if resp == nil || resp.NextPage == 0 {
			return all, true
		}
		opt.Page = resp.NextPage
	}
}

// fetchAllReviewComments pages through the PR's inline review comments with the
// same discipline.
func (c *GitHubClient) fetchAllReviewComments(ctx context.Context, owner, repo string, prNumber int) ([]*github.PullRequestComment, bool) {
	var all []*github.PullRequestComment
	opt := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		page, resp, err := c.Client.PullRequests.ListComments(ctx, owner, repo, prNumber, opt)
		if err != nil {
			fmt.Printf("Warning: PR #%d: review comments not fetched to completion (%v); bundle written with review_comments_complete=false\n", prNumber, err)
			return nil, false
		}
		all = append(all, page...)
		if resp == nil || resp.NextPage == 0 {
			return all, true
		}
		opt.Page = resp.NextPage
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
//
// Field history sources, one per field: the title's edits are the REST "renamed"
// entries in IssueEvents; the body's edits are BodyEdits from GraphQL. Nothing else
// in the bundle carries per-field edit times.
type RawPRBundle struct {
	// BundleVersion is BundleVersion at write time; empty on bundles written before
	// 1.1.0, which therefore have no body-edit history rather than an empty one.
	BundleVersion string              `json:"bundle_version,omitempty"`
	PR            *github.PullRequest `json:"pull_request"`
	// IssueComments are the PR's conversation comments, every page. Before bundle
	// 1.2.0 only the first page was stored and a fetch error was discarded, so an
	// old bundle's list may be truncated: IssueCommentsComplete is false on it.
	IssueComments         []*github.IssueComment `json:"issue_comments,omitempty"`
	IssueCommentsComplete bool                   `json:"issue_comments_complete,omitempty"`
	// ReviewComments are the PR's inline review comments, every page. Never
	// populated before 1.2.0.
	ReviewComments         []*github.PullRequestComment `json:"review_comments,omitempty"`
	ReviewCommentsComplete bool                         `json:"review_comments_complete,omitempty"`
	Commits                []*github.RepositoryCommit   `json:"commits,omitempty"`
	IssueEvents            []*github.IssueEvent         `json:"issue_events,omitempty"`
	// IssueEventsComplete is true only when every issue event page was fetched.
	IssueEventsComplete bool      `json:"issue_events_complete,omitempty"`
	FetchedAt           time.Time `json:"fetched_at"`
	// BodyEdits is the PR body's edit history; BodyEditsComplete is true only when
	// every page was fetched, so a consumer never reads a truncated list as whole.
	BodyEdits         []BodyEdit `json:"body_edits,omitempty"`
	BodyEditsComplete bool       `json:"body_edits_complete,omitempty"`
	// BodyEditsFetchedAt is the instant the history is complete *as of*. An edit made
	// after it cannot be in the list, and that is fine for the exporter's rule: it
	// asks whether the body's last edit is at or before the cutoff, the cutoff is at
	// or before merged_at, and merged_at precedes any fetch. Do not "fix" this by
	// re-fetching at export time; the exporter must read the history the collect
	// stored, so the audit trail stays a fact about the cache.
	BodyEditsFetchedAt time.Time `json:"body_edits_fetched_at,omitempty"`
	// BodyEditsError records why the history is absent when BodyEditsComplete is false.
	BodyEditsError string `json:"body_edits_error,omitempty"`
}

// FetchPRBundle downloads all relevant metadata and comments for a PR.
func (c *GitHubClient) FetchPRBundle(ctx context.Context, owner, repo string, prNumber int) (*RawPRBundle, []byte, error) {
	pr, resp, err := c.Client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch PR #%d: %w", prNumber, err)
	}

	// Discussion is fetched to completion or not at all: the contamination scan
	// reads these bodies, and a truncated list read as whole would miss exactly the
	// later comments that describe the outcome.
	issueComments, issueCommentsComplete := c.fetchAllIssueComments(ctx, owner, repo, prNumber)
	reviewComments, reviewCommentsComplete := c.fetchAllReviewComments(ctx, owner, repo, prNumber)

	// Fetch commits in PR
	commitOpt := &github.ListOptions{PerPage: 100}
	commits, _, _ := c.Client.PullRequests.ListCommits(ctx, owner, repo, prNumber, commitOpt)

	// Title rename events are the only provider history that can reconstruct an
	// earlier title. Completeness is explicit so consumers fail closed.
	var issueEvents []*github.IssueEvent
	eventsComplete := true
	eventOpts := &github.ListOptions{PerPage: 100}
	for {
		events, eventResp, eventErr := c.Client.Issues.ListIssueEvents(ctx, owner, repo, prNumber, eventOpts)
		if eventErr != nil {
			eventsComplete = false
			issueEvents = nil
			break
		}
		issueEvents = append(issueEvents, events...)
		if eventResp.NextPage == 0 {
			break
		}
		eventOpts.Page = eventResp.NextPage
	}

	bundle := &RawPRBundle{
		PR:                     pr,
		IssueComments:          issueComments,
		IssueCommentsComplete:  issueCommentsComplete,
		ReviewComments:         reviewComments,
		ReviewCommentsComplete: reviewCommentsComplete,
		Commits:                commits,
		IssueEvents:            issueEvents,
		IssueEventsComplete:    eventsComplete,
		FetchedAt:              time.Now().UTC(),
	}
	// The body's edit history is the only per-field record of when the description
	// last changed. A failure here is recorded, never fatal.
	c.attachBodyEdits(ctx, owner, repo, prNumber, bundle)

	_ = resp // used for headers if needed
	rawJSON, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal raw PR bundle: %w", err)
	}

	return bundle, rawJSON, nil
}
