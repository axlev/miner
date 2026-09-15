package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BundleVersion is the cache bundle schema version written by this build.
//
// 1.1.0 (2026-09-15): additive. Bundles carry the PR body's edit history
// (BodyEdits, BodyEditsComplete, BodyEditsFetchedAt, BodyEditsError) so the
// prospective exporter can decide per field whether the body's last edit was at or
// before the cutoff. A bundle without bundle_version predates this: it deserializes
// with BodyEditsComplete false and no edits, which is the correct reading of it —
// the history is unknown, not empty.
const BundleVersion = "1.1.0"

// DefaultGraphQLURL is GitHub's GraphQL endpoint.
const DefaultGraphQLURL = "https://api.github.com/graphql"

// bodyEditsPageSize is the GraphQL connection page size for userContentEdits.
const bodyEditsPageSize = 100

// BodyEdit is one entry of the PR body's edit history, from GraphQL
// pullRequest.userContentEdits. Only the body is covered here; title edits come
// from the REST "renamed" issue events already in RawPRBundle.IssueEvents, and each
// field's history has exactly one source so a consumer never has to reconcile two.
type BodyEdit struct {
	CreatedAt time.Time  `json:"created_at"`
	EditedAt  time.Time  `json:"edited_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Editor    string     `json:"editor,omitempty"`
	// Diff is the edit's diff as GitHub reports it, when the API returns one.
	Diff string `json:"diff,omitempty"`
}

// graphQLRequest and graphQLResponse are the minimal wire shapes for one query.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type graphQLResponse struct {
	Data struct {
		Repository *struct {
			PullRequest *struct {
				UserContentEdits struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						CreatedAt time.Time  `json:"createdAt"`
						EditedAt  time.Time  `json:"editedAt"`
						UpdatedAt time.Time  `json:"updatedAt"`
						DeletedAt *time.Time `json:"deletedAt"`
						Editor    *struct {
							Login string `json:"login"`
						} `json:"editor"`
						Diff *string `json:"diff"`
					} `json:"nodes"`
				} `json:"userContentEdits"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"errors"`
}

const bodyEditsQuery = `query($owner: String!, $name: String!, $number: Int!, $first: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      userContentEdits(first: $first, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { createdAt editedAt updatedAt deletedAt editor { login } diff }
      }
    }
  }
}`

// FetchBodyEdits returns the complete edit history of a PR's body, paginated to the
// end. Any failure — no token, transport, non-200, a GraphQL errors array, a missing
// repository or PR, or a page that cannot be parsed — returns an error and no edits:
// a partial list is never returned, because a consumer reading it as complete would
// admit a body whose later edit sits on the page that was never fetched.
func (c *GitHubClient) FetchBodyEdits(ctx context.Context, owner, repo string, prNumber int) ([]BodyEdit, error) {
	if c.Token == "" {
		return nil, fmt.Errorf("GraphQL requires a token and none is configured")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	url := c.GraphQLURL
	if url == "" {
		url = DefaultGraphQLURL
	}

	edits := []BodyEdit{}
	var after *string
	for page := 1; ; page++ {
		vars := map[string]any{"owner": owner, "name": repo, "number": prNumber, "first": bodyEditsPageSize, "after": after}
		body, err := json.Marshal(graphQLRequest{Query: bodyEditsQuery, Variables: vars})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graphql page %d: %w", page, err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("graphql page %d: read: %w", page, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("graphql page %d: HTTP %d: %s", page, resp.StatusCode, firstLine(string(raw)))
		}
		var parsed graphQLResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("graphql page %d: parse: %w", page, err)
		}
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("graphql page %d: %s (%s)", page, parsed.Errors[0].Message, parsed.Errors[0].Type)
		}
		if parsed.Data.Repository == nil || parsed.Data.Repository.PullRequest == nil {
			return nil, fmt.Errorf("graphql page %d: repository or pull request not found", page)
		}
		conn := parsed.Data.Repository.PullRequest.UserContentEdits
		for _, n := range conn.Nodes {
			e := BodyEdit{CreatedAt: n.CreatedAt, EditedAt: n.EditedAt, UpdatedAt: n.UpdatedAt, DeletedAt: n.DeletedAt}
			if n.Editor != nil {
				e.Editor = n.Editor.Login
			}
			if n.Diff != nil {
				e.Diff = *n.Diff
			}
			edits = append(edits, e)
		}
		if !conn.PageInfo.HasNextPage {
			return edits, nil
		}
		if conn.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("graphql page %d: hasNextPage without endCursor", page)
		}
		cursor := conn.PageInfo.EndCursor
		after = &cursor
	}
}

// attachBodyEdits fills the body-edit section of a bundle from one fetch attempt.
// On failure the bundle stays valid: no edits, BodyEditsComplete false, the reason in
// BodyEditsError, and a log line naming the PR. A collect is never aborted over it;
// the export half's "omitted-unverifiable" outcome exists for exactly this case and
// depends on the flag being honest.
func (c *GitHubClient) attachBodyEdits(ctx context.Context, owner, repo string, prNumber int, bundle *RawPRBundle) {
	bundle.BundleVersion = BundleVersion
	bundle.BodyEditsFetchedAt = time.Now().UTC()
	edits, err := c.FetchBodyEdits(ctx, owner, repo, prNumber)
	if err != nil {
		bundle.BodyEdits = nil
		bundle.BodyEditsComplete = false
		bundle.BodyEditsError = err.Error()
		fmt.Printf("Warning: PR #%d: body edit history not fetched (%v); bundle written with body_edits_complete=false\n", prNumber, err)
		return
	}
	bundle.BodyEdits = edits
	bundle.BodyEditsComplete = true
	bundle.BodyEditsError = ""
}

// RefreshBodyEdits fills in the body-edit section of an already cached bundle
// without re-fetching comments, commits, or issue events, and rewrites the bundle
// atomically. FetchedAt is preserved: it still describes when the REST sections were
// taken. It returns whether the history is now complete, and an error only when the
// bundle cannot be read or written — a fetch failure is recorded in the bundle and
// reported as complete=false, matching collect-time behaviour.
func (c *Collector) RefreshBodyEdits(ctx context.Context, owner, repo string, prNumber int) (complete bool, err error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)
	if !c.cache.HasPR(fullName, prNumber) {
		return false, fmt.Errorf("PR #%d is not in the cache; refresh only fills in cached bundles, run collect first", prNumber)
	}
	var bundle RawPRBundle
	if err := c.cache.ReadPR(fullName, prNumber, &bundle); err != nil {
		return false, err
	}
	if bundle.PR == nil || bundle.PR.GetNumber() != prNumber {
		return false, fmt.Errorf("PR #%d: cached bundle does not describe this PR", prNumber)
	}
	c.client.attachBodyEdits(ctx, owner, repo, prNumber, &bundle)
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return false, err
	}
	if err := c.cache.ReplacePR(fullName, prNumber, raw); err != nil {
		return false, err
	}
	return bundle.BodyEditsComplete, nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
