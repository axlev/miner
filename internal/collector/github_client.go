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
	BundleVersion  string                       `json:"bundle_version,omitempty"`
	PR             *github.PullRequest          `json:"pull_request"`
	IssueComments  []*github.IssueComment       `json:"issue_comments,omitempty"`
	ReviewComments []*github.PullRequestComment `json:"review_comments,omitempty"`
	Commits        []*github.RepositoryCommit   `json:"commits,omitempty"`
	IssueEvents    []*github.IssueEvent         `json:"issue_events,omitempty"`
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

	// Fetch issue comments
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	issueComments, _, _ := c.Client.Issues.ListComments(ctx, owner, repo, prNumber, opt)

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
		PR:                  pr,
		IssueComments:       issueComments,
		Commits:             commits,
		IssueEvents:         issueEvents,
		IssueEventsComplete: eventsComplete,
		FetchedAt:           time.Now().UTC(),
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
