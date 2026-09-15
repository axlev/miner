# Item 3a — collector discussion fetch: status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the collector half split out of
the contamination-keys export (3b), to land before the 2026 collect. Status terms are
`AGENTS.md` "Status vocabulary".

## Status

| Point | Status | Where |
|---|---|---|
| Own-PR conversation comments paginated to completion, with `issue_comments_complete` | **Verified** | `internal/collector/github_client.go` (`fetchAllIssueComments`); test `TestFetchPRBundlePaginatesDiscussionToCompletion` |
| Own-PR inline review comments fetched, paginated, with `review_comments_complete` | **Verified** | `fetchAllReviewComments`; same test |
| A page failure discards the partial list, sets the flag false, logs the PR, never fails the bundle | **Verified** | `TestFetchPRBundleDiscardsPartialDiscussionOnFailure` |
| Bundle version `1.1.0` → `1.2.0`; older bundles read as incomplete | **Verified** | `collector.BundleVersion`; `TestOldBundleCommentsAreNotComplete` |
| Tests against a local REST fake with `Link`-header pagination; no network | **Verified** | `internal/collector/discussion_test.go` |

"Verified": `go vet ./...` and `go test ./...` pass on every package in this session.
Not Shipped until the commit is confirmed. Not Integrated: no real bundle has been
fetched with this build.

## Version impact

| Artifact | Before | After | Why |
|---|---|---|---|
| Cache bundle | `1.1.0` | `1.2.0` | additive: two completeness flags, review comments populated |

Nothing else touched: no export, no contract, no record, no cohort artifact.

## Limitations to state

- **Old caches are not repaired.** A bundle below 1.2.0 keeps whatever first page of
  comments it had and both flags false. There is no comment refresh command; the
  contamination-keys export (3b) will treat such a case's own-PR discussion as
  incomplete and say so per file. The 2026 collect writes 1.2.0 from the start, so
  this affects only the 2024 cache.
- **Review comments are the inline kind only.** Review *summaries* (the top-level
  text of an approve/request-changes review, `pulls/{n}/reviews`) are a third
  endpoint and are not fetched; if the scan needs them, that is another paginated
  fetch of the same shape.
- **Completeness is as of `fetched_at`.** Comments posted after the collect are not in
  the bundle, and for the discussion scan that is the wrong direction (later comments
  are the ones that describe the outcome). 3b's own fetches must not rely on the
  bundle for post-merge discussion when the bundle predates the observation end;
  the per-file provenance should carry the fetch time.
