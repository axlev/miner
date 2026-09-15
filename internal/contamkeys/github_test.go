package contamkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v62/github"
	"miner/internal/collector"
	"miner/internal/gitx"
	"miner/internal/storage"
)

// restFake serves the endpoints GitHubFetcher uses and counts calls so the cache
// can be shown to make a second run offline.
type restFake struct{ calls int32 }

func (f *restFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.calls, 1)
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/commits/"+shaA+"/pulls"):
			json.NewEncoder(w).Encode([]map[string]any{{"number": 200}, {"number": 100}})
		case strings.HasSuffix(p, "/pulls/200"):
			json.NewEncoder(w).Encode(map[string]any{"number": 200, "title": "fix", "body": "Fixes #150", "html_url": "https://x/pull/200", "commits": 0})
		case strings.HasSuffix(p, "/issues/200/comments"), strings.HasSuffix(p, "/pulls/200/comments"), strings.HasSuffix(p, "/pulls/200/commits"), strings.HasSuffix(p, "/issues/200/events"):
			json.NewEncoder(w).Encode([]any{})
		case strings.HasSuffix(p, "/graphql"):
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"userContentEdits":{"pageInfo":{"hasNextPage":false,"endCursor":""},"nodes":[]}}}}}`)
		case strings.HasSuffix(p, "/issues/150"):
			json.NewEncoder(w).Encode(map[string]any{"number": 150, "title": "crash", "body": "trace", "html_url": "https://x/issues/150"})
		case strings.HasSuffix(p, "/issues/150/comments"):
			json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "body": "confirmed", "html_url": "https://x/c/1"}})
		case strings.HasSuffix(p, "/issues/300"):
			json.NewEncoder(w).Encode(map[string]any{"number": 300, "title": "a pr", "pull_request": map[string]any{"url": "https://x/pull/300"}})
		default:
			t.Errorf("unexpected request %s", p)
			http.NotFound(w, r)
		}
	}
}

func newFetcher(t *testing.T, srv *httptest.Server, cacheDir string, offline bool) *GitHubFetcher {
	gh := github.NewClient(srv.Client())
	base, _ := url.Parse(srv.URL + "/")
	gh.BaseURL = base
	client := &collector.GitHubClient{Client: gh, Token: "tok", HTTP: srv.Client(), GraphQLURL: srv.URL + "/graphql"}
	// A one-commit mirror so CommitMessage has something real to read.
	repoDir := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(repoDir, 0755)
	for _, args := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}, {"commit", "--allow-empty", "-m", "bgpd: fix crash\n\nCloses #150"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return &GitHubFetcher{Client: client, Cache: storage.NewDiskCache(cacheDir), Repo: gitx.OpenRepository(filepath.Join(repoDir, ".git")), Owner: "owner", Name: "repo", Offline: offline}
}

func TestGitHubFetcherCachesUnderFixesAndThenRunsOffline(t *testing.T) {
	f := &restFake{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	cacheDir := t.TempDir()
	fx := newFetcher(t, srv, cacheDir, false)
	ctx := context.Background()

	nums, err := fx.PullRequestsForCommit(ctx, shaA)
	if err != nil || len(nums) != 2 {
		t.Fatalf("prs for commit: %v %v", nums, err)
	}
	b, err := fx.PullRequestBundle(ctx, 200)
	if err != nil || b.PR.GetNumber() != 200 || !b.IssueCommentsComplete {
		t.Fatalf("bundle: %+v %v", b, err)
	}
	th, isPR, err := fx.Issue(ctx, 150)
	if err != nil || isPR || th.Title != "crash" || len(th.Comments) != 1 || !th.Complete {
		t.Fatalf("issue: %+v %v %v", th, isPR, err)
	}
	if _, isPR, err := fx.Issue(ctx, 300); err != nil || !isPR {
		t.Fatalf("issue 300 should be reported as a PR: %v %v", isPR, err)
	}
	for _, name := range []string{"commit_" + shaA + "_prs.json", "pr_200.json", "issue_150.json", "issue_300.json"} {
		if !fx.Cache.HasFile("owner/repo", FixesNamespace, name) {
			t.Errorf("not cached: %s", name)
		}
	}
	if fx.Cache.HasPR("owner/repo", 200) {
		t.Errorf("fixing PR must not be written into the collect cache namespace")
	}
	calls := atomic.LoadInt32(&f.calls)

	// Second run, offline: every answer comes from the cache; no request is made.
	off := newFetcher(t, srv, cacheDir, true)
	if nums, err := off.PullRequestsForCommit(ctx, shaA); err != nil || len(nums) != 2 {
		t.Errorf("offline prs: %v %v", nums, err)
	}
	if b, err := off.PullRequestBundle(ctx, 200); err != nil || b.PR.GetNumber() != 200 {
		t.Errorf("offline bundle: %v", err)
	}
	if th, _, err := off.Issue(ctx, 150); err != nil || th.Title != "crash" {
		t.Errorf("offline issue: %v", err)
	}
	if _, err := off.PullRequestsForCommit(ctx, shaB); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Errorf("uncached fetch must be refused offline: %v", err)
	}
	if atomic.LoadInt32(&f.calls) != calls {
		t.Errorf("offline run made %d requests", atomic.LoadInt32(&f.calls)-calls)
	}
}

func TestGitHubFetcherReadsCommitMessagesFromTheMirrorAndOwnBundleFromTheCache(t *testing.T) {
	srv := httptest.NewServer((&restFake{}).handler(t))
	defer srv.Close()
	cacheDir := t.TempDir()
	fx := newFetcher(t, srv, cacheDir, true)
	head, _ := exec.Command("git", "--git-dir", fx.Repo.RepoDir, "rev-parse", "HEAD").Output()
	msg, err := fx.CommitMessage(context.Background(), strings.TrimSpace(string(head)))
	if err != nil || !strings.Contains(msg, "Closes #150") {
		t.Errorf("commit message: %q %v", msg, err)
	}
	if _, err := fx.OwnBundle(100); err == nil || !strings.Contains(err.Error(), "not in the collect cache") {
		t.Errorf("missing own bundle: %v", err)
	}
	raw, _ := json.Marshal(collector.RawPRBundle{PR: &github.PullRequest{Number: github.Int(100)}})
	fx.Cache.WritePR("owner/repo", 100, raw)
	if b, err := fx.OwnBundle(100); err != nil || b.PR.GetNumber() != 100 {
		t.Errorf("own bundle: %v", err)
	}
}
