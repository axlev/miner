package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"miner/internal/storage"
)

// fakeGraphQL serves userContentEdits pages. pages[i] is the node list for page i;
// failAt (1-based) makes that page return HTTP 500; errorsAt makes it return a
// GraphQL errors array with HTTP 200.
type fakeGraphQL struct {
	pages    [][]map[string]any
	failAt   int
	errorsAt int
	calls    int32
	lastAuth string
	lastVars map[string]any
}

func (f *fakeGraphQL) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&f.calls, 1))
		f.lastAuth = r.Header.Get("Authorization")
		var req graphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		f.lastVars = req.Variables
		if n == f.failAt {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if n == f.errorsAt {
			fmt.Fprint(w, `{"data":null,"errors":[{"message":"Resource not accessible by integration","type":"FORBIDDEN"}]}`)
			return
		}
		page := n - 1
		if page >= len(f.pages) {
			t.Errorf("page %d requested but only %d exist", n, len(f.pages))
			http.Error(w, "no such page", 500)
			return
		}
		resp := map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"userContentEdits": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": page < len(f.pages)-1, "endCursor": fmt.Sprintf("c%d", n)},
				"nodes":    f.pages[page],
			}}}}}
		json.NewEncoder(w).Encode(resp)
	}
}

func node(edited string, editor string) map[string]any {
	return map[string]any{"createdAt": "2024-01-01T00:00:00Z", "editedAt": edited, "updatedAt": edited, "deletedAt": nil, "editor": map[string]any{"login": editor}, "diff": "-a\n+b"}
}

func clientFor(srv *httptest.Server, token string) *GitHubClient {
	return &GitHubClient{Client: github.NewClient(nil), Token: token, HTTP: srv.Client(), GraphQLURL: srv.URL}
}

func TestFetchBodyEditsPaginatesToCompletion(t *testing.T) {
	f := &fakeGraphQL{pages: [][]map[string]any{
		{node("2024-02-01T00:00:00Z", "alice"), node("2024-02-02T00:00:00Z", "bob")},
		{node("2024-03-01T00:00:00Z", "carol")},
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	edits, err := clientFor(srv, "tok").FetchBodyEdits(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 3 || edits[0].Editor != "alice" || edits[2].Editor != "carol" || edits[2].Diff != "-a\n+b" {
		t.Errorf("edits = %+v", edits)
	}
	if f.calls != 2 {
		t.Errorf("calls = %d, want 2", f.calls)
	}
	if f.lastAuth != "Bearer tok" {
		t.Errorf("auth header = %q", f.lastAuth)
	}
	if f.lastVars["after"] != "c1" || f.lastVars["number"] != float64(7) {
		t.Errorf("second page variables = %v", f.lastVars)
	}
}

func TestFetchBodyEditsNeverReturnsAPartialList(t *testing.T) {
	f := &fakeGraphQL{pages: [][]map[string]any{{node("2024-02-01T00:00:00Z", "alice")}, {node("2024-03-01T00:00:00Z", "bob")}}, failAt: 2}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	edits, err := clientFor(srv, "tok").FetchBodyEdits(context.Background(), "o", "r", 7)
	if err == nil || edits != nil {
		t.Fatalf("mid-pagination failure must return no edits: edits=%v err=%v", edits, err)
	}
	if !strings.Contains(err.Error(), "page 2") {
		t.Errorf("error should name the failing page: %v", err)
	}
}

func TestFetchBodyEditsFailsOnGraphQLErrorsAndMissingToken(t *testing.T) {
	f := &fakeGraphQL{pages: [][]map[string]any{{}}, errorsAt: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	if _, err := clientFor(srv, "tok").FetchBodyEdits(context.Background(), "o", "r", 7); err == nil || !strings.Contains(err.Error(), "FORBIDDEN") {
		t.Errorf("errors array not surfaced: %v", err)
	}
	if _, err := clientFor(srv, "").FetchBodyEdits(context.Background(), "o", "r", 7); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("missing token should fail before any request: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("the tokenless call must not reach the server; calls = %d", f.calls)
	}
}

func TestFetchBodyEditsEmptyHistoryIsCompleteNotAbsent(t *testing.T) {
	f := &fakeGraphQL{pages: [][]map[string]any{{}}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	var bundle RawPRBundle
	clientFor(srv, "tok").attachBodyEdits(context.Background(), "o", "r", 7, &bundle)
	if !bundle.BodyEditsComplete || bundle.BodyEditsError != "" || bundle.BundleVersion != BundleVersion || bundle.BodyEditsFetchedAt.IsZero() {
		t.Errorf("a never-edited body is a complete, empty history: %+v", bundle)
	}
}

func TestAttachBodyEditsRecordsFailureWithoutAborting(t *testing.T) {
	f := &fakeGraphQL{pages: [][]map[string]any{{}}, failAt: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	bundle := RawPRBundle{BodyEdits: []BodyEdit{{Editor: "stale"}}, BodyEditsComplete: true}
	clientFor(srv, "tok").attachBodyEdits(context.Background(), "o", "r", 7, &bundle)
	if bundle.BodyEditsComplete || bundle.BodyEdits != nil || !strings.Contains(bundle.BodyEditsError, "HTTP 500") {
		t.Errorf("failure must clear the section and record why: %+v", bundle)
	}
}

// An old bundle has no body-edit fields at all. Loading it must read as "history
// unknown", never as "never edited".
func TestOldBundleLoadsAsUnknownHistory(t *testing.T) {
	old := `{"pull_request":{"number":5,"title":"t","body":"b"},"issue_events_complete":true,"fetched_at":"2026-09-01T00:00:00Z"}`
	var bundle RawPRBundle
	if err := json.Unmarshal([]byte(old), &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.BundleVersion != "" || bundle.BodyEditsComplete || bundle.BodyEdits != nil || !bundle.BodyEditsFetchedAt.IsZero() {
		t.Errorf("old bundle must have no history: %+v", bundle)
	}
	if !bundle.IssueEventsComplete {
		t.Errorf("unrelated sections must still load")
	}
}

func TestRefreshBodyEditsFillsInACachedBundleAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	cache := storage.NewDiskCache(dir)
	fetched := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := RawPRBundle{
		PR:                  &github.PullRequest{Number: github.Int(5), Title: github.String("t"), Body: github.String("b")},
		IssueComments:       []*github.IssueComment{{Body: github.String("keep me")}},
		Commits:             []*github.RepositoryCommit{{SHA: github.String("abc")}},
		IssueEvents:         []*github.IssueEvent{{Event: github.String("renamed")}},
		IssueEventsComplete: true,
		FetchedAt:           fetched,
	}
	raw, _ := json.Marshal(old)
	if err := cache.WritePR("o/r", 5, raw); err != nil {
		t.Fatal(err)
	}

	f := &fakeGraphQL{pages: [][]map[string]any{{node("2024-02-01T00:00:00Z", "alice")}}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	coll := NewCollector(clientFor(srv, "tok"), cache, nil)
	complete, err := coll.RefreshBodyEdits(context.Background(), "o", "r", 5)
	if err != nil || !complete {
		t.Fatalf("refresh: complete=%v err=%v", complete, err)
	}

	var got RawPRBundle
	if err := cache.ReadPR("o/r", 5, &got); err != nil {
		t.Fatal(err)
	}
	if !got.BodyEditsComplete || len(got.BodyEdits) != 1 || got.BodyEdits[0].Editor != "alice" || got.BundleVersion != BundleVersion {
		t.Errorf("history not filled in: %+v", got)
	}
	if !got.FetchedAt.Equal(fetched) {
		t.Errorf("fetched_at must be preserved: %v", got.FetchedAt)
	}
	if got.BodyEditsFetchedAt.Before(fetched) || got.BodyEditsFetchedAt.IsZero() {
		t.Errorf("body_edits_fetched_at must be the refresh time: %v", got.BodyEditsFetchedAt)
	}
	if len(got.IssueComments) != 1 || got.IssueComments[0].GetBody() != "keep me" || len(got.Commits) != 1 || len(got.IssueEvents) != 1 || !got.IssueEventsComplete {
		t.Errorf("other sections must be untouched: %+v", got)
	}
	// No temporary sibling is left behind.
	entries, _ := os.ReadDir(filepath.Dir(cache.PRCachePath("o/r", 5)))
	if len(entries) != 1 {
		t.Errorf("cache dir should hold exactly the bundle, got %d entries", len(entries))
	}
}

func TestRefreshBodyEditsRecordsFetchFailureAndRefusesUncached(t *testing.T) {
	dir := t.TempDir()
	cache := storage.NewDiskCache(dir)
	raw, _ := json.Marshal(RawPRBundle{PR: &github.PullRequest{Number: github.Int(5)}})
	cache.WritePR("o/r", 5, raw)

	f := &fakeGraphQL{pages: [][]map[string]any{{}}, errorsAt: 1}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	coll := NewCollector(clientFor(srv, "tok"), cache, nil)
	complete, err := coll.RefreshBodyEdits(context.Background(), "o", "r", 5)
	if err != nil || complete {
		t.Fatalf("fetch failure is recorded, not returned: complete=%v err=%v", complete, err)
	}
	var got RawPRBundle
	cache.ReadPR("o/r", 5, &got)
	if got.BodyEditsComplete || !strings.Contains(got.BodyEditsError, "FORBIDDEN") || got.PR.GetNumber() != 5 {
		t.Errorf("bundle after failed refresh: %+v", got)
	}
	if _, err := coll.RefreshBodyEdits(context.Background(), "o", "r", 6); err == nil || !strings.Contains(err.Error(), "not in the cache") {
		t.Errorf("uncached PR must be refused: %v", err)
	}
}
