package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/storage"
)

// restFake serves the four REST endpoints FetchPRBundle calls, with Link-header
// pagination on comments and review comments, and lets a test break one page.
type restFake struct {
	issuePages  int
	reviewPages int
	failIssue   int // 1-based page that returns 500; 0 = never
	failReview  int
}

func (f *restFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		link := func(total int) {
			if page < total {
				u := *r.URL
				q := u.Query()
				q.Set("page", fmt.Sprint(page+1))
				u.RawQuery = q.Encode()
				w.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, u.String()))
			}
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			json.NewEncoder(w).Encode(map[string]any{"number": 7, "title": "t", "body": "b", "commits": 0})
		case strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			if page == f.failIssue {
				http.Error(w, "boom", 500)
				return
			}
			link(f.issuePages)
			json.NewEncoder(w).Encode([]map[string]any{{"id": page, "body": fmt.Sprintf("issue comment page %d", page)}})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/comments"):
			if page == f.failReview {
				http.Error(w, "boom", 500)
				return
			}
			link(f.reviewPages)
			json.NewEncoder(w).Encode([]map[string]any{{"id": page, "body": fmt.Sprintf("review comment page %d", page)}})
		case strings.HasSuffix(r.URL.Path, "/pulls/7/commits"):
			json.NewEncoder(w).Encode([]any{})
		case strings.HasSuffix(r.URL.Path, "/issues/7/events"):
			json.NewEncoder(w).Encode([]any{})
		case strings.HasSuffix(r.URL.Path, "/graphql"):
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"userContentEdits":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func restClient(srv *httptest.Server) *GitHubClient {
	gh := github.NewClient(srv.Client())
	base, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = base
	return &GitHubClient{Client: gh, Token: "tok", HTTP: srv.Client(), GraphQLURL: srv.URL + "/graphql"}
}

func TestFetchPRBundlePaginatesDiscussionToCompletion(t *testing.T) {
	f := &restFake{issuePages: 3, reviewPages: 2}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	b, raw, err := restClient(srv).FetchPRBundle(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.IssueComments) != 3 || !b.IssueCommentsComplete {
		t.Errorf("issue comments: %d, complete=%v; want 3 pages merged and complete", len(b.IssueComments), b.IssueCommentsComplete)
	}
	if len(b.ReviewComments) != 2 || !b.ReviewCommentsComplete {
		t.Errorf("review comments: %d, complete=%v; want 2 pages merged and complete", len(b.ReviewComments), b.ReviewCommentsComplete)
	}
	if b.BundleVersion != BundleVersion || !b.BodyEditsComplete {
		t.Errorf("bundle version/body edits: %q %v", b.BundleVersion, b.BodyEditsComplete)
	}
	if !strings.Contains(string(raw), `"review_comments_complete": true`) || !strings.Contains(string(raw), `"issue_comments_complete": true`) {
		t.Errorf("flags not serialized: %s", raw)
	}
}

func TestFetchPRBundleDiscardsPartialDiscussionOnFailure(t *testing.T) {
	f := &restFake{issuePages: 3, reviewPages: 2, failIssue: 2, failReview: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	b, _, err := restClient(srv).FetchPRBundle(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatalf("a discussion failure must not fail the bundle: %v", err)
	}
	if b.IssueComments != nil || b.IssueCommentsComplete {
		t.Errorf("issue comments after a page-2 failure must be absent and flagged: %d complete=%v", len(b.IssueComments), b.IssueCommentsComplete)
	}
	if b.ReviewComments != nil || b.ReviewCommentsComplete {
		t.Errorf("review comments after a failure must be absent and flagged")
	}
}

// A bundle written before 1.2.0 carries its first-page comment list and no flags:
// it must load with both completeness flags false.
func TestOldBundleCommentsAreNotComplete(t *testing.T) {
	old := `{"bundle_version":"1.1.0","pull_request":{"number":5},"issue_comments":[{"id":1,"body":"only page"}]}`
	var b RawPRBundle
	if err := json.Unmarshal([]byte(old), &b); err != nil {
		t.Fatal(err)
	}
	if b.IssueCommentsComplete || b.ReviewCommentsComplete || len(b.IssueComments) != 1 {
		t.Errorf("old bundle: %+v", b)
	}
}

// TestRefreshBundleReplacesAStaleBundleWholesale is the path for a pre-1.2.0 cache:
// a bundle with no rename history and no body edits is replaced by a complete one,
// in place, at the current version.
func TestRefreshBundleReplacesAStaleBundleWholesale(t *testing.T) {
	dir := t.TempDir()
	cache := storage.NewDiskCache(dir)
	stale := `{"pull_request":{"number":7,"title":"t"},"issue_comments":[{"id":1,"body":"one page"}],"fetched_at":"2026-08-23T15:59:01Z"}`
	if err := cache.WritePR("o/r", 7, []byte(stale)); err != nil {
		t.Fatal(err)
	}
	f := &restFake{issuePages: 2, reviewPages: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	coll := NewCollector(restClient(srv), cache, nil)

	b, err := coll.RefreshBundle(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if b.BundleVersion != BundleVersion || !b.IssueEventsComplete || !b.BodyEditsComplete || !b.IssueCommentsComplete || !b.ReviewCommentsComplete {
		t.Errorf("refreshed bundle is not complete at the current version: %+v", b)
	}
	var got RawPRBundle
	if err := cache.ReadPR("o/r", 7, &got); err != nil {
		t.Fatal(err)
	}
	if got.BundleVersion != BundleVersion || len(got.IssueComments) != 2 || len(got.ReviewComments) != 1 {
		t.Errorf("cache still holds the stale bundle: %+v", got)
	}
	if got.FetchedAt.Year() != time.Now().UTC().Year() {
		t.Errorf("fetched_at was not renewed: %v", got.FetchedAt)
	}
	// No temporary sibling is left behind.
	entries, _ := os.ReadDir(filepath.Dir(cache.PRCachePath("o/r", 7)))
	if len(entries) != 1 {
		t.Errorf("cache dir holds %d entries, want 1", len(entries))
	}
	// A PR absent from the cache is created, not refused.
	if _, err := coll.RefreshBundle(context.Background(), "o", "r", 7); err != nil {
		t.Errorf("second refresh: %v", err)
	}
}
