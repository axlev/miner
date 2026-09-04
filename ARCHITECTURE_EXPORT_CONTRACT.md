# Benchmark Miner Architecture and Export Contract Inspection

Inspection date: 2026-09-04

This report records a read-only inspection of the `benchmark-miner` repository. No mined datasets, oracle labels, expected findings, retrospective fixes outside repository source/tests, or paths outside this repository were inspected.

## Executive summary

The miner is a Go CLI that discovers merged pull requests, caches GitHub metadata, enriches records from a local Git repository, correlates later corrective commits, scores candidates, and exports either retrospective evidence or restricted prospective metadata.

The newer `prospective-export` command is the appropriate starting boundary for `benchmark-engine`. Its current reviewer-facing output is deliberately small and excludes known retrospective fields. It is not yet a complete engine input contract because it supplies no code change, comparison commits, public manifest, or public artifact hashes, and it stores evaluator-only files under the same case root.

The legacy `export` output is a research/candidate artifact, not a prospective benchmark input: it includes retrospective evidence and may use that evidence for filtering.

## 1. Responsibilities and execution flow

The executable entry point is `main.go`, which calls `cli.Execute()`.

The implemented workflow is:

1. `collect`
   - Searches GitHub for merged PRs in monthly intervals.
   - Fetches PR details, comments, commits, and issue events into a local cache.
   - Reads base/head/merge identities from the cached GitHub PR response.
   - Uses a local Git repository to compute the merge-base PR diff (`base...head`) and derive changed paths and functions.
   - Writes initial `PRCandidateRecord` JSONL.
2. `rebuild-raw` (optional)
   - Reconstructs the exact PR membership of a seed JSONL using only cached bundles and local Git.
   - Fails if required cache entries or Git diffs are unavailable.
3. `correlate`
   - Loads commits from all local refs between the earliest PR merge and an observation cutoff.
   - For each PR, keeps commits strictly after that PR's merge time and at or before the observation cutoff.
   - Detects explicit fix/revert/regression references, same-function and same-file overlap, lineage, and reversal evidence.
   - Can write one JSONL or immutable resumable batches.
4. `finalize-batches`
   - Validates run identity, file hashes, partitions, observation cutoff, and PR coverage.
   - Produces a combined correlated JSONL without deleting batch checkpoints.
5. `score`
   - Applies YAML-configured path and keyword heuristics to `OriginalPR` fields.
   - Writes the `StatefulEvaluation` block.
6. `export`
   - Filters records by stateful score and optional retrospective signals.
   - Writes JSONL, Markdown, or CSV.
7. `retrospective-export`
   - Materializes all selected retrospective signals, commit relationships, commit metadata, and available patches for one PR.
8. `prospective-export`
   - Builds a closed reviewer metadata allowlist using cutoff-aware field decisions.
   - Writes reviewer-facing metadata separately from the selected correlated record and audit data.
9. `inspect` and `stats`
   - Produce console reports and do not write files themselves.

The maintained run script executes `collect -> correlate -> finalize-batches -> score -> export`.

## 2. CLI and package boundaries

The actual executable is the repository-root `main` package. The module is named `pr-analysis`. README commands referring to `./cmd/miner` are stale; that directory does not exist. `run_frr_2024.sh` correctly builds the repository root.

Registered commands:

- `collect`
- `rebuild-raw`
- `correlate`
- `finalize-batches`
- `score`
- `export`
- `inspect`
- `stats`
- `retrospective-export`
- `prospective-export`

Important internal packages:

| Package | Responsibility |
| --- | --- |
| `internal/cli` | Command orchestration, flags, and console reporting |
| `internal/collector` | GitHub acquisition and cached-bundle-to-model conversion |
| `internal/gitx` | Git repository management, diffs, symbols, ancestry, blame, reversal, and patch export |
| `internal/correlator` | Retrospective evidence detection and relationship analysis |
| `internal/heuristics` | Prospective/stateful candidate scoring |
| `internal/model` | Shared JSONL record types |
| `internal/storage` | GitHub bundle cache and legacy JSONL/CSV/Markdown writers |
| `internal/batchstore` | Immutable correlation checkpoint files and manifests |
| `internal/prospectiveexport` | Reviewer-facing metadata allowlist and temporal validation |
| `internal/retrospectiveexport` | Evaluator/adjudication evidence materialization |

All Go packages are under `internal`, so the supported cross-repository boundary is currently files/JSON rather than a Go import API.

## 3. Output artifacts

### Collection and processing

```text
collect:
  <cache-dir>/github/<owner>_<repo>/prs/pr_<number>.json
  <git-dir>/                                  bare Git repository
  data/raw_prs.jsonl                          default JSONL output

rebuild-raw:
  data/frr_2024_raw_corrected_20260821.jsonl  default output

correlate, single file:
  data/correlated.jsonl

correlate, batched:
  <batch-dir>/run.json
  <batch-dir>/batch-0001.jsonl
  <batch-dir>/batch-0001.json
  ...

finalize-batches:
  data/correlated.jsonl

score:
  data/candidates.jsonl

export:
  output/benchmark_candidates.jsonl
  or a caller-selected JSONL, Markdown, or CSV path

inspect and stats:
  stdout only
```

`WriteJSONL` sorts records by PR number. The legacy export command first sorts by retrospective rank/stateful score, but then passes records to `WriteJSONL`, which re-sorts them by PR number; therefore the JSONL does not retain the stated ranking order.

### Retrospective case export

```text
<out>/
  source_record.json
  manifest.json
  signals/
    strong.json
    medium.json
    weak.json
    all.json
  relationships/
    commit_relationships.json
  commits/
    <candidate-sha>.json
  patches/
    <resolved-commit-sha>.patch
  logical_patches/
    <patch-id-or-hashed-id>/
      manifest.json
```

This exporter writes directly into its destination and is not atomic at the directory level. A failure can leave partial files, and existing files are not rejected consistently.

### Prospective case export

```text
<out>/
  prospective/
    metadata.json
  evaluator-only/
    correlated-report.json
    metadata-export-audit.json
```

This exporter builds a temporary directory, validates the output, refuses an existing destination, and publishes the complete case with one rename.

### Full repository run script

`run_frr_2024.sh` additionally creates:

- A copied heuristics configuration.
- The local Git mirror and GitHub cache.
- `logs/pipeline.log`.
- `miner_commit.txt`.
- `bin/miner`.
- Raw, correlated, and candidate JSONL files.
- Correlation batch files and manifests.
- Candidate JSONL, Markdown, and CSV reports.
- Stats and individual-PR inspection text files.
- `SHA256SUMS`.

## 4. Pipeline record schema

All JSONL processing stages use the same top-level structure and `1.0.0` schema version:

```text
schema_version
original
stateful
retrospective
provenance
```

### `original`

| Field | Meaning/source |
| --- | --- |
| `repository` | `owner/repo` assembled from collection options |
| `number` | GitHub PR number |
| `title` | Current title in cached GitHub PR response |
| `body` | Current body in cached GitHub PR response |
| `author` | GitHub user login |
| `created_at` | GitHub PR creation time |
| `merged_at` | GitHub merge time; treated as the PR's `T0` boundary |
| `base_ref` | Base branch name from the cached PR response |
| `base_sha` | Base branch SHA from the cached PR response |
| `head_sha` | PR head SHA from the cached PR response |
| `merge_commit_sha` | GitHub merge commit SHA |
| `commit_shas` | SHAs returned by the cached PR commit list |
| `labels` | Current labels from the cached PR response |
| `changed_files` | Paths/status/counts from local `git diff --numstat -M base...head` |
| `changed_functions` | Flat list of functions parsed from diff hunks |
| `changed_function_locations` | Path/function pairs parsed from diff hunks |
| `pre_merge_issue_refs` | Numeric references extracted from the cached PR body |
| `commit_count` | GitHub PR commit count |
| `commit_messages` | Messages returned in the cached PR commit list |

The raw original diff is used during collection/correlation but is not serialized in this record.

### `stateful`

| Field | Meaning |
| --- | --- |
| `score` | Heuristic points capped by configured `max_cap` |
| `raw_points` | Uncapped heuristic points |
| `category` | `HIGH`, `MEDIUM`, or `LOW` |
| `matched_path_rules` | Triggered path rules |
| `matched_keyword_rules` | Triggered title/body/symbol/commit-message rules |
| `matched_symbols` | Symbols matched by keyword rules |
| `explanation` | Human-readable rule summary |

Each matched rule records its name, regex pattern, weight, match location, and matched value.

### `retrospective`

- `summary_rank_score`
- `strong_signals`
- `medium_signals`
- `weak_signals`
- `commit_relationships`

Strong signals include explicit `Fixes` SHA/PR references, reverts, and regression mentions. Medium signals currently come from strict same-path/same-function fix overlap. Weak signals include recent same-file overlap and compatibility fallbacks.

Signals can contain later commit identity, timestamp, exact matching text/context, scope, logical patch identity, and changed paths. Commit relationships can contain lineage/blame evidence and forward/reverse patch-overlap metrics.

### `provenance`

| Field | Meaning/source |
| --- | --- |
| `miner_version` | Intended miner build identity; currently hard-coded as `v1.0.0` |
| `config_hash` | SHA-256 of the loaded YAML configuration |
| `harvested_at` | Current UTC time during collect/rebuild |
| `observation_end` | Retrospective evidence cutoff; correlation overwrites it with the requested cutoff |
| `target_repo_head_sha` | Local repository `HEAD` at collect/rebuild time |
| `github_api_version` | Hard-coded `2022-11-28` identifier |

## 5. Reviewer-facing prospective metadata

The complete public allowlist is:

| Field | Inclusion rule |
| --- | --- |
| `repository` | Required; copied from the selected correlated record's `original.repository` |
| `cutoff_timestamp` | Required; copied from the explicit `--cutoff` argument |
| `title` | Current cached title when PR `updated_at <= cutoff`; otherwise reconstructed by reversing a complete post-cutoff rename chain; omitted if uncertain |
| `description` | Current cached body only when PR `updated_at <= cutoff`; omitted otherwise because no complete body edit history is available |
| `base_branch` | Current cached base ref only when PR `updated_at <= cutoff`; omitted otherwise |
| `commit_messages` | Included only when merge occurred by cutoff, cached list length equals declared PR commit count, and every commit has a message and timestamp no later than cutoff |

Reviewer metadata currently omits:

- Schema version.
- Case ID.
- PR number.
- Base/head/merge commit identities.
- Resolved merge base.
- Changed paths/functions.
- Original patch/diff.
- Public artifact hashes.

Per-field source, timestamps, validity, reconstruction method, and omission reason are recorded only in evaluator-only `metadata-export-audit.json`.

## 6. Identity, cutoff, diff, and later-fix behavior

### Repository and PR identity

- Primary repository identity is an `owner/repo` string.
- Collection accepts repository identity from CLI/config.
- Correlation defaults to the first input record's repository and permits a `--repo` override.
- Correlation does not validate every record against the chosen repository.
- Both case exporters locate a record by numeric PR number alone.
- `prospective-export` does not verify that the cache bundle's PR number/repository matches the selected correlated record.

### Base/head and original diff

- `base_sha`, `head_sha`, and `merge_commit_sha` come from the cached current GitHub PR response.
- Original change analysis uses `git diff -p -M base...head`, which resolves the comparison via Git merge-base semantics.
- The resolved merge-base SHA is not stored.
- The raw original diff is not included in the pipeline record or prospective export.

### Cutoffs

These timestamps have distinct meanings:

- Mining `from`/`to`: selects PRs by merge date.
- `OriginalPR.merged_at`: per-PR `T0` boundary.
- `provenance.observation_end`: last allowed retrospective evidence time.
- Prospective `--cutoff`: explicit benchmark-view timestamp, independent of the observation cutoff.

### Later fixes

- Git history is loaded using `git log --all`, not a traversal explicitly rooted at the recorded target HEAD.
- The log is bounded by earliest PR merge and observation end.
- Per PR, commits at/before merge and after observation end are rejected.
- The stored commit `Date` is parsed from `%aI` (author date), although the surrounding semantics call it a commit date.
- Strong/medium candidate commits are enriched with lineage and reversal analysis.
- Retrospective export resolves candidate SHAs and writes commit metadata and canonical patches. Merge patches use first-parent diff; ordinary/root commits use `git show`.

## 7. Plausible contamination paths

The following are verified risks or contract weaknesses:

1. **Legacy candidate exports contain retrospective evidence.** They must not be supplied to a prospective reviewer or model.
2. **Legacy candidate selection can depend on later fixes.** This is valid for benchmark construction, but the selected record is evaluator material.
3. **Prospective and evaluator-only files share one case root.** Recursive ingestion of the case directory exposes the correlated report.
4. **A case ID can disclose the PR number.** Validation checks path safety, not neutrality; the README example uses `case-<PR number>`.
5. **Record/cache identity is not cross-checked.** A wrong cache file or mixed-repository input can combine one repository identity with another PR's text.
6. **Collection treats current cached PR fields as `OriginalPR`.** Title, body, labels, and base ref may have changed after merge. The specialized prospective exporter screens only title/body/base branch.
7. **Forbidden-value validation is exact-string based.** It checks selected full SHAs, snippets, contexts, and notes, but not short SHAs, PR references, paraphrases, or future values absent from those fields.
8. **No prospective code snapshot is supplied.** If the engine independently uses a current checkout, it may observe post-cutoff code.
9. **Repository HEAD is recorded but does not constrain retrospective traversal.** `git log --all` can inspect commits on any local ref.
10. **The cache is not literally verbatim HTTP responses.** It is a newly marshalled composite `RawPRBundle` assembled from several API calls.
11. **Hard-coded miner version weakens provenance.** The record does not currently identify the actual miner source commit.

The current prospective allowlist and atomic publication are useful safeguards, but filesystem separation and identity validation remain necessary.

## 8. Existing tests and fixtures

Existing coverage includes:

- Prospective inclusion/omission decisions.
- Title reconstruction through complete rename history.
- Omission of ambiguous post-cutoff title/body state.
- Pre-merge omission of state and commit messages.
- Closed-schema and forbidden-value validation.
- Deterministic reviewer metadata.
- Reviewer/evaluator directory separation.
- No published partial prospective case after validation failure.
- Retrospective signal preservation, grouping, deduplication, missing commits, relationships, and deterministic patches.
- JSONL serialization and deterministic PR-number ordering.
- Cache read/write behavior.
- Batch resume, sidecar recovery, finalization, and tamper rejection.
- Heuristic, correlator, diff, hunk, lineage, and reversal behavior.

No checked-in `testdata`, JSON fixture, diff fixture, or golden-file directory was found. Most tests synthesize data and Git repositories in temporary directories. A few integration tests conditionally use repository-local `data/` caches/repositories and skip when those are absent.

Important missing tests:

- Wrong cache PR number or repository.
- Mixed-repository and duplicate-PR inputs.
- Cutoff before PR creation and inconsistent cutoff/merge state.
- Stale or internally inconsistent cached snapshots.
- Short-SHA and PR-number leakage.
- Opaque case-ID enforcement.
- Recursive validation of every engine-visible path/file.
- Public manifest compatibility and artifact hash validation.
- Original patch and comparison-base correctness.
- Legacy JSONL/Markdown/CSV export contract tests.
- Retrospective atomicity and overwrite behavior.
- Fully synthetic end-to-end prospective package generation.

Tests were not executed during this inspection because the request prohibited creating files; Go tests create temporary/build-cache artifacts.

## 9. Recommended stable contract for benchmark-engine

`benchmark-engine` should consume one versioned, immutable, self-contained prospective directory. It should never consume a `PRCandidateRecord`, candidate JSONL, retrospective directory, or parent directory containing evaluator-only files.

Recommended minimum:

```text
prospective/
  manifest.json
  metadata.json
  change.patch
```

Recommended `manifest.json`:

```json
{
  "schema_version": "benchmark-miner/prospective-case/v1",
  "case_id": "opaque-generated-id",
  "repository": "owner/repo",
  "cutoff_timestamp": "2024-04-10T05:22:26Z",
  "comparison_base_sha": "40-character merge-base SHA",
  "head_sha": "40-character PR-head SHA",
  "artifacts": {
    "metadata.json": {"sha256": "..."},
    "change.patch": {"sha256": "..."}
  }
}
```

`metadata.json` should retain the current six-field allowlist. `change.patch` should be the canonical merge-base-to-head PR diff. The merge commit SHA, PR number, retrospective signals/rank, observation end, later repository HEAD, and mining heuristic details should remain outside the engine-visible package.

## 10. Smallest likely miner changes

These are recommendations, not current behavior:

1. Add a versioned public manifest and canonical original-change patch to `prospective-export`.
2. Resolve and record the actual comparison merge-base and head SHA.
3. Validate that the selected record, cache bundle, and local Git repository identify the same repository and PR.
4. Reject duplicate `(repository, PR)` records instead of returning the first numeric match.
5. Generate an opaque case ID inside the miner rather than accepting a PR-derived public identifier.
6. Emit prospective and evaluator artifacts to separate caller-supplied roots, or make the default command emit only prospective files.
7. Hash every public artifact and validate the complete public directory against an exact filename and schema allowlist.
8. Record the actual miner Git/build identity.
9. Use positive cutoff provenance as the primary validation mechanism; retain secret/forbidden-string scanning only as defense in depth.

## Proposed miner-engine interface

`benchmark-miner` produces an immutable directory conforming to `benchmark-miner/prospective-case/v1`. `benchmark-engine` accepts exactly that directory, verifies the manifest version and artifact SHA-256 hashes, checks out `comparison_base_sha`, applies `change.patch` or verifies `head_sha`, and uses only `metadata.json` as natural-language context. Retrospective/evaluator artifacts are generated into a separate location that the engine process cannot traverse.

## Source index

- CLI registration and root: `internal/cli/root.go`
- Collection flow: `internal/cli/collect.go`, `internal/collector/collector.go`, `internal/collector/github_client.go`
- Offline rebuild: `internal/cli/rebuild_raw.go`, `internal/collector/rebuild.go`
- Correlation and batches: `internal/cli/correlate.go`, `internal/cli/finalize_batches.go`, `internal/batchstore/batchstore.go`
- Git operations: `internal/gitx/repo.go`, `internal/gitx/diff.go`, `internal/gitx/ancestry.go`, `internal/gitx/reversal.go`, `internal/gitx/export.go`
- Scoring: `internal/cli/score.go`, `internal/heuristics/engine.go`
- Models: `internal/model/*.go`
- Legacy formats: `internal/cli/export.go`, `internal/storage/jsonl.go`, `internal/storage/export_format.go`
- Prospective export: `internal/cli/prospective_export.go`, `internal/prospectiveexport/export.go`
- Retrospective export: `internal/cli/retrospective_export.go`, `internal/retrospectiveexport/export.go`
- Tests: corresponding `*_test.go` files under `internal/`
