# Benchmark Miner Architecture and Export Contract Inspection

Inspection date: 2026-09-04

This report records a read-only inspection of the `benchmark-miner` repository. No mined datasets, oracle labels, expected findings, retrospective fixes outside repository source/tests, or paths outside this repository were inspected.

## Executive summary

The miner is a Go CLI that discovers merged pull requests, caches GitHub metadata, enriches records from a local Git repository, correlates later corrective commits, scores candidates, and exports either retrospective evidence or restricted prospective metadata.

The newer `prospective-export` command is the boundary `engine-runner` consumes. Its current reviewer-facing output is deliberately small and excludes known retrospective fields. **Updated 2026-09-09:** it now publishes the `reviewer/` + `control/` bundle `engine-runner` ingests, with a materialized source snapshot of the cutoff commit, the canonical original-change diff over the same two objects, an exhaustive checksum manifest, pinned schema versions, cross-identity validation, and pre-publication boundary validation, and stores evaluator-only files under a separate caller-supplied root — see §3 and §9/§10 for the item-by-item status. A bundle it produced was verified against the engine's own boundary validator (§8). The engine-facing side of the boundary is now settled against `repos/engine-runner/docs/prospective-bundle-contract.md`, which is normative for what the engine accepts and supersedes `system-design.md` §7 where they differ. `retrospective-export`'s own directory-level atomicity (§7 risk noted inline in §3) is unchanged.

The legacy `export` output is a research/candidate artifact, not a prospective benchmark input: it includes retrospective evidence and may use that evidence for filtering.

## 1. Responsibilities and execution flow

The executable entry point is `cmd/miner/main.go`, which calls `cli.Execute()`.

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
   - Materializes the cutoff commit's tree as a plain source snapshot and pairs it with the
     canonical diff taken over the same two commit objects.
   - Publishes an engine-ingestible `reviewer/` + `control/` bundle, separately from the
     selected correlated record and audit data.
9. `cohort-report` and `cohort-verify`
   - `cohort-report` summarizes scored candidates for case selection, grouped by
     subsystem, with a read-only export precheck. Evaluator-facing.
   - `cohort-verify` exports a shortlist for real and submits each bundle to
     `engine-runner`'s own boundary validator, so no case enters a cohort on a prediction.
10. `inspect` and `stats`
   - Produce console reports and do not write files themselves.

The maintained run script executes `collect -> correlate -> finalize-batches -> score -> export`.

## 2. CLI and package boundaries

The module is named `miner`. The sole executable is `cmd/miner`, matching every `README.md` usage example. A duplicate root `main.go` (byte-identical, tracked since the initial commit) existed until 2026-09-06 and has been removed; `run_frr_2024.sh` was updated to build `./cmd/miner` accordingly. The earlier claim in this report that `./cmd/miner` "does not exist" was incorrect.

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
- `cohort-report`
- `cohort-verify`

Important internal packages:

| Package | Responsibility |
| --- | --- |
| `internal/cli` | Command orchestration, flags, and console reporting |
| `internal/collector` | GitHub acquisition and cached-bundle-to-model conversion |
| `internal/gitx` | Git repository management, diffs, symbols, ancestry, blame, reversal, patch export, and source-snapshot materialization |
| `internal/correlator` | Retrospective evidence detection and relationship analysis |
| `internal/heuristics` | Prospective/stateful candidate scoring |
| `internal/model` | Shared JSONL record types |
| `internal/storage` | GitHub bundle cache and legacy JSONL/CSV/Markdown writers |
| `internal/batchstore` | Immutable correlation checkpoint files and manifests |
| `internal/prospectiveexport` | Reviewer-facing metadata allowlist, temporal validation, and engine-ingestible bundle assembly |
| `internal/retrospectiveexport` | Evaluator/adjudication evidence materialization |
| `internal/cohort` | Evaluator-facing cohort selection reports and shortlist verification |

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

**Updated 2026-09-09:** the exporter now publishes a bundle in the layout
`engine-runner` ingests (`docs/prospective-bundle-contract.md` in that repo, which is
normative for the engine and takes precedence over `system-design.md` §7 where they
differ). The decision recorded there is that the miner adapts to the engine.

```text
<prospective-out>/     (the directory passed to the engine's `bench -bundle`)
  reviewer/            everything a reasoner may ever see
    repository/        materialized source snapshot of cutoff_commit
    diff.patch         the admissible diff
    metadata.json      reviewer-visible metadata
  control/             engine-only; never mounted to a reasoner
    manifest.json      routing and identity
    checksums.sha256   integrity over every reviewer/ file

<evaluator-out>/       (evaluator-only; must never be exposed to an engine)
  correlated-report.json
  metadata-export-audit.json
```

`reviewer/` and `control/` are separate because the engine's context builder
allow-list-copies only the three named entries under `reviewer/`; `control/` is not on
that path at all, so there is no filter to bypass.

`reviewer/repository/` is a plain directory tree of regular files written straight from
the Git object database (`Repository.MaterializeTree`, `internal/gitx/snapshot.go`) —
no checkout, no working tree, no `.git`. It is the tree of `cutoff_commit`, and
`reviewer/diff.patch` is the canonical `git diff` from `base_commit` to that same
`cutoff_commit`, so the diff's post-image *is* the snapshot. That correspondence is a
property of how the two are produced rather than a reconciliation step, which matters
because nothing downstream re-checks it: the engine validates that every evidence
citation names a file present in `reviewer/repository/` with an in-range line span, so
a snapshot that did not correspond would send reviewers to locations that fail
validation after a stage had already been paid for. A tree entry that cannot be
published as a plain regular file — a symlink, a submodule gitlink, or a path segment a
consumer reads as Git metadata — fails the export rather than being skipped, because
skipping one would silently break exactly that correspondence.

`control/manifest.json` conforms to `schemas/prospective-manifest.schema.json` and pins
`engine-manifest/v1`: an opaque miner-generated `case_id`, `repository`,
`cutoff_timestamp`, the locally resolved `base_commit` (Git merge-base of the PR's base
and head, not merely the cached provider value), `cutoff_commit`, and
`snapshot_format`. `control/checksums.sha256` is one `sha256sum`-format line per regular
file under `reviewer/` — digest, two spaces, path relative to the bundle root — and must
describe exactly that set of files. It replaces the previous
`manifest.json.artifacts` hashes and is strictly stronger, because it is exhaustive in
both directions: a file present but unlisted is as much a signal as a digest mismatch,
which is how a stray evaluator-only file is caught even when its name looks innocent.

`reviewer/metadata.json` conforms to `schemas/reviewer-metadata.schema.json`: the same
closed reviewer allowlist as before, plus a pinned
`"schema_version": "reviewer-metadata/v1"`. The miner's own instinct was that version
information belongs in the manifest; the engine requires it in the metadata file anyway,
so that a metadata file separated from its bundle is still self-identifying. It still
never carries `base_commit`/`cutoff_commit`.

Before publication the exporter re-derives the consumer's admissibility rules against
the built bundle (`validateBundle`, `internal/prospectiveexport/bundle.go`) as a second
pass over the bytes on disk, not an assertion about the values used to write them.
Layout, irregular files, Git metadata, pinned schema versions, and checksum agreement
are errors. The lexical oracle-name heuristic applied *inside* the source snapshot is a
warning only: that vocabulary belongs to the upstream repository, and the engine's
protocol carries a waiver for exactly those paths, so they are reported for a human to
judge rather than renamed or dropped. Exact evaluator-artifact basenames
(`oracle.json`, `ground_truth.json`, …) remain errors even inside the snapshot —
`ground_truth` is deliberately excluded from the fragments the engine matches there,
because ML repositories use the term legitimately, which leaves that exact-filename case
to the miner.

Both output roots are fully built and validated in temporary directories before either
is published; each root refuses to overwrite an existing destination, and a validation
failure (including cross-identity validation between the cache bundle, the selected
correlated record, and the local Git repository — see §7 risk 5) leaves neither root
written.

### Cohort selection and verification

**Added 2026-09-09.** Two evaluator-facing commands support choosing benchmark cases.

`cohort-report` writes a single Markdown or CSV report and nothing else. Its export
precheck resolves the merge-base and walks the head tree; both are read-only, and a test
asserts the working tree is unchanged by a run. Rows are grouped by subsystem rather than
globally ranked, because a cohort drawn from the top of a ranking tends to be one bug
class in one subsystem and cannot discriminate across the axes `system-design.md` §15
Milestone 4 requires. A generic container directory (`internal/`, `src/`, `pkg/`,
`cmd/`) is stepped through when grouping so a Go repository does not collapse into one
heading.

`cohort-verify` writes one real prospective bundle per shortlisted PR under its output
directory, plus the matching evaluator-only roots, then optionally runs `engine-runner`'s
validator against each. It exists because the precheck is a prediction: a case that looks
exportable can still fail on something the prediction cannot see, and discovering that
after a cohort is frozen — or after a paid run has started — is the failure it prevents.
A case whose validation was not run is reported as `not run`, never as passing, so
unchecked and checked-and-clean cannot be confused.

Validation shells out to `go run ./cmd/bench` in a caller-supplied `--bench-repo`. That
is not a shortcut: `engine-runner` is a separate Go module and its validator lives under
`internal/`, which Go forbids importing across module boundaries. Without `--bench-repo`
nothing is validated and the exact command per bundle is printed instead, so this repo
never assumes a sibling checkout exists.

Both outputs are evaluator-facing and legitimately carry retrospective signal counts and
ranking (§7 risk 2). Neither is ever written into a prospective bundle.

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
| `miner_version` | **Updated 2026-09-06:** resolved at build time from Go's embedded VCS metadata (`internal/buildinfo.MinerVersion`) — the actual Git revision, suffixed `-dirty` for an uncommitted tree, or `unknown` if no VCS revision was embedded (e.g. outside a Git checkout, a shallow clone, or `-buildvcs=false`). No longer a hard-coded `v1.0.0`. |
| `config_hash` | SHA-256 of the loaded YAML configuration |
| `harvested_at` | Current UTC time during collect/rebuild |
| `observation_end` | Retrospective evidence cutoff; correlation overwrites it with the requested cutoff |
| `target_repo_head_sha` | Local repository `HEAD` at collect/rebuild time |
| `github_api_version` | Hard-coded `2022-11-28` identifier |

## 5. Reviewer-facing prospective metadata

The complete public allowlist is:

| Field | Inclusion rule |
| --- | --- |
| `schema_version` | Required; always the pinned literal `reviewer-metadata/v1`. Added 2026-09-09 at the engine's request: it pins the metadata contract independently of `control/manifest.json`, so a metadata file separated from its bundle is still self-identifying |
| `repository` | Required; copied from the selected correlated record's `original.repository` |
| `cutoff_timestamp` | Required; copied from the explicit `--cutoff` argument |
| `title` | Current cached title when PR `updated_at <= cutoff`; otherwise reconstructed by reversing a complete post-cutoff rename chain; omitted if uncertain |
| `description` | Current cached body only when PR `updated_at <= cutoff`; omitted otherwise because no complete body edit history is available |
| `base_branch` | Current cached base ref only when PR `updated_at <= cutoff`; omitted otherwise |
| `commit_messages` | Included only when merge occurred by cutoff, cached list length equals declared PR commit count, and every commit has a message and timestamp no later than cutoff |

Reviewer metadata (`reviewer/metadata.json`) carries a pinned `schema_version` and
otherwise still omits, by design, all of the following — they are published, but under
`control/` or as a sibling reviewer artifact, as described in §3:

- Case ID. *(`control/manifest.json`, opaque and miner-generated — see §10 item 5)*
- Resolved merge base and head SHA. *(`control/manifest.json` as `base_commit`/`cutoff_commit`)*
- Artifact hashes. *(`control/checksums.sha256`, covering every file under `reviewer/`)*
- Original patch/diff. *(`reviewer/diff.patch`, a sibling reviewer artifact)*

Still omitted everywhere in the prospective bundle (unchanged):

- PR number.
- Merge commit identity.
- Changed paths/functions.

Per-field source, timestamps, validity, reconstruction method, and omission reason are recorded only in evaluator-only `metadata-export-audit.json`.

## 6. Identity, cutoff, diff, and later-fix behavior

### Repository and PR identity

- Primary repository identity is an `owner/repo` string.
- Collection accepts repository identity from CLI/config.
- Correlation defaults to the first input record's repository and permits a `--repo` override.
- Correlation does not validate every record against the chosen repository.
- **Updated 2026-09-06:** `prospective-export` now rejects an input where the selected PR
  number matches more than one record (`internal/prospectiveexport.findRecord`), and
  validates that the cache bundle's PR number, repository (`base.repo.full_name`), and
  base/head SHAs match the selected correlated record before proceeding
  (`internal/prospectiveexport.crossCheckIdentity`). `retrospective-export` still locates
  a record by numeric PR number alone without a duplicate check.

### Base/head and original diff

- `base_sha`, `head_sha`, and `merge_commit_sha` come from the cached current GitHub PR response.
- Original change analysis uses `git diff -p -M base...head`, which resolves the comparison via Git merge-base semantics.
- The resolved merge-base SHA is not stored in the pipeline record; `prospective-export` resolves it independently and records it as `control/manifest.json`'s `base_commit` (§3).
- The raw original diff is not included in the pipeline record. `prospective-export` publishes it as `reviewer/diff.patch`, regenerated from the two commit objects rather than carried through the record.

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

1. **Legacy candidate exports contain retrospective evidence.** They must not be supplied to a prospective reviewer or model. *(unchanged; out of scope for prospective-export)*
2. **Legacy candidate selection can depend on later fixes.** This is valid for benchmark construction, but the selected record is evaluator material. *(unchanged)*
3. **~~Prospective and evaluator-only files share one case root.~~** **Fixed 2026-09-06:** `prospective-export` now writes to two independent caller-supplied roots (`--prospective-out`, `--evaluator-out`); there is no longer a shared parent directory to recursively over-ingest.
4. **~~A case ID can disclose the PR number.~~** **Fixed 2026-09-06:** `--case-id` is optional; the default is an opaque, miner-generated `case-<16 hex chars>` (SHA-256 of repository/PR/cutoff) that does not embed the PR number as a literal substring. A caller may still supply an explicit ID (still validated as a neutral path component), so this risk returns if a caller deliberately overrides it with a PR-derived string.
5. **~~Record/cache identity is not cross-checked.~~** **Fixed 2026-09-06:** `crossCheckIdentity` rejects a mismatch between the cache bundle's PR number, repository, and base/head SHA and the selected correlated record's, before any output is written.
6. **Collection treats current cached PR fields as `OriginalPR`.** Title, body, labels, and base ref may have changed after merge. The specialized prospective exporter screens only title/body/base branch. *(unchanged)*
7. **Forbidden-value validation is exact-string based.** It checks selected full SHAs, snippets, contexts, and notes, but not short SHAs, PR references, paraphrases, or future values absent from those fields. *(unchanged; see §10 item 9 — this remains intentional defense-in-depth behind positive cutoff-provenance validation, not the primary gate)*
8. **~~No prospective code snapshot is supplied.~~** **Fixed 2026-09-09:** `reviewer/repository/` is a materialized directory tree of `cutoff_commit`, written from the object database. The engine no longer needs a clone, a checkout, or a patch-application step, so there is no path by which it could observe post-cutoff code from its own working tree.
9. **Repository HEAD is recorded but does not constrain retrospective traversal.** `git log --all` can inspect commits on any local ref. *(unchanged; retrospective-export concern, not prospective-export)*
10. **The cache is not literally verbatim HTTP responses.** It is a newly marshalled composite `RawPRBundle` assembled from several API calls. *(unchanged)*
11. **~~Hard-coded miner version weakens provenance.~~** **Fixed 2026-09-06:** `provenance.miner_version` now resolves the actual build's Git revision via `internal/buildinfo.MinerVersion` (Go's embedded VCS metadata) instead of a fixed `v1.0.0` string.

The prospective allowlist, cross-identity validation, opaque case IDs, and separated
output roots close risks 3, 4, 5, and 11 for `prospective-export`; the materialized
snapshot closes risk 8. Risks 1, 2, 6, 7, 9, and 10 remain and are out of scope for
this round of changes; see §10 for the item-by-item mapping to `AGENTS.md`'s backlog.

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

**Updated 2026-09-06:** `testdata/retrospectiveexport/` and `testdata/prospectiveexport/`
now hold small checked-in JSON/JSONL fixture templates (correlated-record templates with
`{{PLACEHOLDER}}` tokens substituted at test time with dynamically generated Git SHAs).
Git repository synthesis itself remains in-test, following the pre-existing convention,
since a checked-in `.git` fixture would be an opaque binary blob rather than a reviewable
fixture. This covers the two case-exporter test areas; other packages still synthesize
data inline and were not migrated in this round.

Added 2026-09-09 for the engine-ingestible bundle layout:

- Bundle layout, naming, and pinned schema versions (`TestExportProducesEngineIngestibleLayout`).
- Checksum-manifest exactness in both directions, sha256sum format, and bundle-root-relative paths (`TestChecksumsDescribeExactlyTheReviewerTree`, `TestValidateBundleRejectsBoundaryViolations`).
- Diff/snapshot correspondence, by applying the published `reviewer/diff.patch` to a materialized `base_commit` tree and comparing the result to `reviewer/repository/` (`TestSnapshotCorrespondsToDiff`).
- Fail-closed rejection of symlinks and Git-metadata-named paths in the head tree (`TestExportRejectsUnmaterializableTreeEntries`).
- Warn-not-fail behavior for the waivable lexical heuristic inside the snapshot (`TestValidateBundleWarnsOnHighSignalSnapshotNames`).
- Absence of evaluator-only content anywhere under `reviewer/` (`TestReviewerTreeCarriesNoEvaluatorContent`).
- Reviewer-metadata `schema_version` pinning, including a missing and a wrong version (`TestStrictValidationRejectsUnknownNestedAndForbidden`).

A bundle produced by `prospective-export` was additionally run through the engine's own
boundary validator (`go run ./cmd/bench -bundle … -case-id …` in `repos/engine-runner`,
which is deterministic, offline, and spends nothing) and passed all seven rules:
`allowed_paths`, `no_irregular_files`, `no_git_metadata`, `reviewer_metadata_fields`,
`oracle_shaped_content`, `checksum_manifest`, `pinned_schema_versions`. A negative
control — an evaluator-only file copied into `reviewer/repository/` — was rejected by
that validator on both `oracle_shaped_content` and `checksum_manifest`.

Previously-missing tests now covered (2026-09-06):

- Wrong cache PR number, repository, base SHA, or head SHA (`TestExportRejectsMismatchedCacheIdentity`).
- Duplicate/ambiguous PR number in the correlated input (`TestFindRecordRejectsAmbiguousPRNumber`).
- Opaque case-ID determinism and non-disclosure (`TestGenerateCaseIDIsOpaqueAndDeterministic`).
- Manifest artifact hash and directory-allowlist validation, and comparison-base/head-SHA correctness (`TestExportSeparatesEvaluatorDataAndIsDeterministic`, `TestMergeBaseAndCanonicalChangePatch`).
- Existing-output-root refusal for each of the two separated roots independently (`TestExportRejectsExistingOutputRoots`).

Still missing (not scheduled in this round):

- Cutoff before PR creation and inconsistent cutoff/merge state.
- Stale or internally inconsistent cached snapshots.
- Short-SHA and PR-number leakage (forbidden-value scanning still only covers full-length values).
- `retrospective-export`'s own duplicate-PR-number handling (only `prospective-export` was hardened this round).
- Legacy JSONL/Markdown/CSV export contract tests.
- Retrospective atomicity and overwrite behavior (`retrospective-export` still writes directly into its destination; see §3).

## 9. Stable contract for `engine-runner` — Implemented 2026-09-09

`engine-runner` consumes one versioned, immutable, self-contained prospective bundle.
It must never consume a `PRCandidateRecord`, candidate JSONL, retrospective directory,
or parent directory containing evaluator-only files.

The normative definition lives in `repos/engine-runner/docs/prospective-bundle-contract.md`
and is derived from what that repo's `internal/boundaryvalidator` and
`internal/contextbuilder` actually enforce. Where `system-design.md` §7 differs, the
bundle contract is the accurate one. The recommendation this section previously carried
(`miner/prospective-case/v1`: a flat root with `manifest.json`, `metadata.json`, and
`change.patch`) was superseded on 2026-09-09; the decision taken was that the miner
adapts to the engine.

Implemented, produced at `--prospective-out`:

```text
<bundle-root>/
  reviewer/
    repository/
    diff.patch
    metadata.json
  control/
    manifest.json
    checksums.sha256
```

Implemented `control/manifest.json` (see `schemas/prospective-manifest.schema.json`):

```json
{
  "schema_version": "engine-manifest/v1",
  "case_id": "opaque-generated-id",
  "repository": "owner/repo",
  "cutoff_timestamp": "2024-04-10T05:22:26Z",
  "base_commit": "40-character merge-base SHA",
  "cutoff_commit": "40-character PR-head SHA",
  "snapshot_format": "directory-snapshot/v1"
}
```

`reviewer/metadata.json` retains the reviewer allowlist and adds a pinned
`schema_version`. The merge commit SHA, PR number, retrospective signals/rank,
observation end, later repository HEAD, and mining heuristic details remain outside the
bundle entirely; `base_commit`/`cutoff_commit` are inside it but under `control/`, which
the engine never passes to a reasoner.

Migration delta applied on 2026-09-09:

| Previous miner output | Now |
| --- | --- |
| flat root | `reviewer/` + `control/` split |
| `change.patch` | `reviewer/diff.patch` |
| `comparison_base_sha` + patch, no tree | `reviewer/repository/` materialized snapshot |
| `manifest.json` at engine-visible root | `control/manifest.json`, engine-only |
| `manifest.json.artifacts` hashes | `control/checksums.sha256` |
| `metadata.json` with no `schema_version` | `schema_version: reviewer-metadata/v1` |
| `metadata.json` at root | `reviewer/metadata.json` |

The evaluator-only root is unchanged: separately supplied, never under the bundle root,
never traversed by the engine.

## 10. Smallest likely miner changes — status as of 2026-09-09

Items 1-9 record the 2026-09-06 round; several describe artifacts the 2026-09-09 bundle
migration then replaced, and are marked inline. Items 10-13 are that migration.

1. **Implemented, then superseded 2026-09-09.** A versioned public manifest and the canonical original-change patch were added to `prospective-export`. The patch remains (`Repository.CanonicalChangePatch`), now published as `reviewer/diff.patch`; `internal/prospectiveexport.Manifest` was replaced by `ControlManifest` at `control/manifest.json` (item 12).
2. **Implemented.** `Repository.MergeBase` resolves the actual comparison merge-base locally via `git merge-base`; both it and the head SHA are recorded in `control/manifest.json` (as `base_commit`/`cutoff_commit`) and cross-checked against the cache bundle.
3. **Implemented.** `crossCheckIdentity` validates that the selected record, cache bundle, and (implicitly, via successful patch resolution) local Git repository identify the same repository, PR number, and base/head commits.
4. **Implemented.** `findRecord` now collects every line matching the requested PR number and rejects the export if more than one match exists, instead of returning the first.
5. **Implemented.** `GenerateCaseID` derives an opaque `case-<16 hex>` identifier from a SHA-256 of repository/PR/cutoff; `--case-id` is now optional and defaults to this.
6. **Implemented.** `prospective-export` now takes `--prospective-out` and `--evaluator-out` as two independent, separately-atomic output roots instead of one shared root with two subdirectories.
7. **Implemented, then superseded 2026-09-09.** `ValidateManifest` recomputed every artifact's SHA-256 and `validateProspectiveDirectory` enforced a filename allowlist on the flat directory. Both were replaced by `validateBundle` and `control/checksums.sha256` (items 11 and 13), which are exhaustive over the whole `reviewer/` tree rather than over a fixed list of three names. This remains native Go structural validation against the same shape documented in `schemas/*.json`, not a runtime JSON-Schema library evaluation — no new dependency was added.
8. **Implemented.** `internal/buildinfo.MinerVersion` records the actual build's Git revision (via Go's embedded VCS metadata) in `provenance.miner_version`, replacing the hard-coded `v1.0.0`.
9. **Already implemented by design; not a code change this round.** `Build` (`internal/prospectiveexport/export.go`) only ever sets a `Metadata` field when its `FieldDecision.Included` is positively proven from cutoff-admissible provenance — a field is never included and *then* screened out by forbidden-value scanning. `ValidateNormalized`'s forbidden-string check runs strictly after and in addition to this, as defense in depth. `TestBuildOmitsAmbiguousEditedText` demonstrates this directly by asserting omission at the `Build` level, without invoking forbidden-value scanning at all.


Added 2026-09-09, for engine ingestibility:

10. **Implemented.** `Repository.MaterializeTree`/`ListTree` (`internal/gitx/snapshot.go`) write the tree of `cutoff_commit` as a plain directory of regular files, reading blobs straight from the object database via a streamed `git cat-file --batch`. Symlinks, submodule gitlinks, and Git-metadata-named path segments fail the export rather than being skipped.
11. **Implemented.** `control/checksums.sha256` replaces `manifest.json.artifacts`; `validateBundle` (`internal/prospectiveexport/bundle.go`) recomputes it from disk and requires exact agreement in both directions.
12. **Implemented.** `Metadata.SchemaVersion` pins `reviewer-metadata/v1` and `ValidateNormalized` rejects any other value; `ControlManifest` pins `engine-manifest/v1` and `ValidateControlManifest` replaces `ValidateManifest`.
13. **Implemented.** `validateBundle` re-derives the engine's admissibility rules against the built bundle before publication, erroring on the unwaivable rules and warning on the lexical oracle-name heuristic inside the snapshot, whose vocabulary is upstream's.

## Proposed miner-engine interface

**Superseded 2026-09-09.** The original proposal — an immutable directory conforming to `benchmark-miner/prospective-case/v1`, from which the engine would check out `comparison_base_sha` and apply `change.patch` — was replaced by the bundle contract in §9. The engine no longer checks out or applies anything: it mounts `reviewer/repository/` read-only, so there is no post-mount step that could reintroduce contamination after validation ran. Retrospective/evaluator artifacts are still generated into a separate location the engine process cannot traverse.

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
- Cohort selection and verification: `internal/cli/cohort.go`, `internal/cohort/report.go`, `internal/cohort/verify.go` *(added 2026-09-09)*
- Retrospective export: `internal/cli/retrospective_export.go`, `internal/retrospectiveexport/export.go`
- Build identity: `internal/buildinfo/buildinfo.go` *(added 2026-09-06)*
- Merge-base/canonical patch resolution: `internal/gitx/diff.go` (`MergeBase`, `CanonicalChangePatch`) *(added 2026-09-06)*
- Source snapshot materialization: `internal/gitx/snapshot.go` (`ListTree`, `MaterializeTree`) *(added 2026-09-09)*
- Bundle layout, checksum manifest, and pre-publication boundary validation: `internal/prospectiveexport/bundle.go` *(added 2026-09-09)*
- Normative engine ingest contract: `repos/engine-runner/docs/prospective-bundle-contract.md`
- Public schemas: `schemas/prospective-manifest.schema.json`, `schemas/reviewer-metadata.schema.json`, `schemas/oracle-manifest.schema.json` *(added 2026-09-06)*
- Checked-in test fixtures: `testdata/prospectiveexport/`, `testdata/retrospectiveexport/` *(added 2026-09-06)*
- Tests: corresponding `*_test.go` files under `internal/`
