# Item 3b — contamination-keys export: status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the peer dispatch for the
per-case contamination keys consumed by `engine-runner`'s post-hoc scan, as scoped and
adjusted in session (split from 3a; `fixing_shas` shape frozen; tier in a sibling
field). Status terms are `AGENTS.md` "Status vocabulary".

## Status

| Point | Status | Where |
|---|---|---|
| Keys file per case: `fixing_shas` (frozen plain list, all tiers), `fixing_pr_numbers` (never own PR), `cve_ids`, verbatim `discussion`, `fixing_commits` (tier, source, matched_by), `provenance` | **Verified** | `internal/contamkeys/keys.go` (`Build`); `schemas/contamination-keys.schema.json`; tests `keys_test.go` |
| Negatives get a file: empty fixing lists, own post-merge comments | **Verified** | `TestBuildNegativeHasEmptyFixesAndOwnDiscussion` |
| Fixing-PR resolution from commit SHAs; fixing PR bodies, comments, review comments; referenced issues (`#N` rule); CVE regex | **Verified** | `Build` steps 2–4, 6; `GitHubFetcher` (`github.go`) using `ListPullRequestsWithCommit`, `FetchPRBundle`, `Issues.Get`/`ListComments`; tests `github_test.go` against a local REST fake |
| `fixes/` cache namespace; second run offline; `--offline` refuses uncached fetches | **Verified** | `FixesNamespace`; `DiskCache.FilePath/HasFile/WriteFile/ReadFile`; `TestGitHubFetcherCachesUnderFixesAndThenRunsOffline` (zero requests on the offline run) |
| Case ids match the bundles (`GenerateCaseID`, cutoff = `merged_at` unless `--cutoff`) | **Verified** | `Build`; `TestBuildAssemblesKeysForAPositive`, `TestBuildRejectsShortSHAsAndMissingCutoff` |
| Unfetchable source recorded in `provenance.incomplete`, file still written, command exits non-zero if any incomplete | **Verified** | `TestBuildRecordsEveryIncompleteSourceAndStillWrites`; `internal/cli/contamination_keys.go` |
| Output directory atomic and immutable; duplicate case id refused | **Verified** | `WriteAll`; `TestWriteAllIsImmutableAndAtomic` |
| Evaluator-only rule stated in the schema and docs | **Implemented** | schema description; `README.md`; `docs/export-contract.md` §3 |

"Verified": `go vet ./...` and `go test ./...` pass on every package in this session.
Nothing here has run against the provider: the REST fake exercises the same client
paths, and the first real run will be the first provider contact.

## Version impact

| Artifact | Version | Note |
|---|---|---|
| Keys file | `contamination-keys/v1` | new, evaluator-only |
| Cache | new `fixes/` namespace beside `prs/` | additive; bundle version unchanged (1.2.0) |
| `gitx.Repository.CommitMessage`, `storage.DiskCache.{FilePath,HasFile,WriteFile,ReadFile}` | — | small additive helpers |

Not touched: bundles, contracts, records, cohort artifacts, heuristics.

## Limitations to state

- **Own-PR discussion is as of the collect-time bundle**, not a live fetch. Comments
  posted after the collect are absent; `provenance.notes` records the bundle's fetch
  time. For the 2026 collect this is moot (the collect is the observation), and for a
  case whose bundle is below 1.2.0 the own-PR comments are flagged incomplete because
  that bundle may hold only the first page.
- **Issue threads are only those referenced by `#N`** in fixing commit messages or
  fixing PR text. Issues linked by other means (timeline cross-references, "closes"
  keywords in unrelated commits) are not gathered. Stated rule; the timeline API is a
  later fetch if a scan ever needs it.
- **`ListPullRequestsWithCommit` reports PRs on the default branch's history** for the
  commit; a fix that landed by a route GitHub does not associate (cherry-picked SHAs on
  release branches, squash-merged with a rewritten SHA) resolves to no PR, and that is
  recorded as an empty list, not as incomplete — the provider answered. The commit
  message is still quoted.
- **Review summaries are not fetched**, only inline review comments (same gap as 3a).
- **`cve_ids` are regex hits, not verified advisories.** A CVE mentioned in passing
  ("unlike CVE-…") is included. The scan is meant to over-include.
- **Deleted or edited comments** are whatever the provider returns at fetch time; there
  is no edit history for comments.
- **Cost.** Per positive: one call per fixing SHA, one bundle fetch per fixing PR (six
  REST calls plus one GraphQL), and one or two per referenced issue. For 40 cases with a
  handful of fixes each this is a few hundred calls, well inside an hourly budget; all
  cached after the first run.
