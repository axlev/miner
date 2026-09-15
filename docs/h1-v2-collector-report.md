# reviewer-metadata/v2, collector half — status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the peer dispatch for the
collector half of the v2 decision (`docs/h1-pre-registration.md` §12 A2). Status terms
are `AGENTS.md` "Status vocabulary".

## Status per point

| # | Point | Status | Where |
|---|---|---|---|
| 1 | Fetch GraphQL `pullRequest.userContentEdits` per collected PR, paginated to completion; title stays on REST `renamed` events; sources recorded per field | **Verified** | `internal/collector/body_edits.go` (`FetchBodyEdits`, `bodyEditsQuery`); source note on `RawPRBundle` in `github_client.go`; tests `TestFetchBodyEditsPaginatesToCompletion` |
| 2 | Store beside `IssueEvents` with its own completeness flag; a partial page never leaves the flag true | **Verified** | `RawPRBundle.BodyEdits`, `BodyEditsComplete`, `BodyEditsFetchedAt`, `BodyEditsError`; `FetchBodyEdits` returns nothing on any failure; tests `TestFetchBodyEditsNeverReturnsAPartialList`, `TestFetchBodyEditsEmptyHistoryIsCompleteNotAbsent` |
| 3 | Fallback: bundle still written on failure, flag false, PR number logged, collect never aborted | **Verified** | `attachBodyEdits`; called from `FetchPRBundle`; tests `TestAttachBodyEditsRecordsFailureWithoutAborting`, `TestFetchBodyEditsFailsOnGraphQLErrorsAndMissingToken` |
| 4 | Per-PR refresh that fills in body edits without re-collecting comments/commits/events | **Verified** | `Collector.RefreshBodyEdits` (atomic replace via `DiskCache.ReplacePR`, `fetched_at` preserved); command `refresh-body-edits` accepting a comma list, a file of numbers, or a cohort manifest; tests `TestRefreshBodyEditsFillsInACachedBundleAndKeepsTheRest`, `TestRefreshBodyEditsRecordsFetchFailureAndRefusesUncached`, `TestParsePRSelectionAcceptsListFileAndManifest` |
| 5 | Bundle version bump; old bundle reads as flag false, no edits | **Verified** | `collector.BundleVersion = "1.1.0"`, `RawPRBundle.BundleVersion`; test `TestOldBundleLoadsAsUnknownHistory` |
| 6 | Tests: pagination, fetch failure, old-bundle load, refresh path; no network | **Verified** | `internal/collector/body_edits_test.go` (httptest GraphQL server), `internal/cli/refresh_body_edits_test.go` |

"Verified": `go vet ./...` and `go test ./...` pass on every package in this session.
Not Shipped until the commit is confirmed. Not Integrated: no real bundle has been
fetched with the new build, and the `prospectiveexport` half does not exist yet.

## Version impact

| Artifact | Before | After | Why |
|---|---|---|---|
| Cache bundle (`pr_<n>.json`) | unversioned (`1.0.0` by convention) | `bundle_version` `1.1.0` | additive body-edit section |

Not touched: `internal/prospectiveexport`, `internal/heuristics`, the bundle wire
contract, `reviewer-metadata/v1`, the pipeline record, the cohort artifacts.

## Limitations to state

- **Token access to `userContentEdits` is unverified offline.** GraphQL has no
  anonymous access, and this session has no token and makes no network calls. GitHub
  shows edit history publicly in the UI, so a plain token is expected to read it; if
  not, every bundle will carry `body_edits_complete: false` with the reason in
  `body_edits_error`, and the export half will report `omitted-unverifiable` for every
  body. The first real collect, or one `refresh-body-edits` on a known PR, settles it.
- **`body_edits_fetched_at` bounds the history.** Edits made after the fetch are not in
  the list. That is harmless for the v2 rule (last edit at or before `merged_at`, and
  `merged_at` precedes any fetch) and must not be "fixed" by re-fetching at export
  time; the doc comment on the field says why.
- **Two field histories, two sources.** The body's history is GraphQL; the title's is
  the REST `renamed` events with their own `issue_events_complete` flag. The export
  half must read each field's completeness flag, not one flag for both.
- **Labels and base ref still have no history.** §7 risk 6 is narrowed, not closed;
  neither field enters `reviewer/metadata.json`, so nothing leaks, but they remain
  current values in the evaluator record.
- **A refresh rewrites the bundle bytes.** `crossCheckIdentity` reads PR number,
  repository and SHAs from the bundle, none of which a refresh changes, but any
  artifact that hashed the old bundle bytes (`cache_bundle_sha256` in an evaluator
  audit file) will differ after a refresh. Refresh before exporting a cohort, not
  after.
