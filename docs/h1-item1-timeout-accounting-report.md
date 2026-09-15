# H1 migration item 1 — timeout accounting: status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the peer dispatch accepted after
`docs/h1-fitness-review-miner.md` §4 item 1, points 1–5 plus the widened re-correlate
set. Status terms are `AGENTS.md` "Status vocabulary".

## Status per point

| # | Point | Status | Where |
|---|---|---|---|
| 1 | Per-record `uninspected_commit_count` and bounded `uninspected_commit_shas`, counted inside `CorrelatePR` for commits that passed the fix-vocabulary pre-filter and whose diff could not be inspected; record schema 1.0.0 → 1.1.0, additive | **Verified** | `internal/model/retrospective.go` (fields, cap 20), `internal/model/candidate.go` (`1.1.0`), `internal/correlator/correlator.go` (`getSummary` sets `diffFailed`; `countIfUninspected` per commit); tests `internal/correlator/uninspected_test.go` |
| 2 | `correlated_by` in provenance; batch `RunManifest` records the correlating build | **Verified** | `internal/model/provenance.go`; `internal/cli/correlate.go` (`correlateRecordSet` stamps it and the schema version); `internal/batchstore/batchstore.go` (`CorrelatorVersion` on run and batch manifests, schema 1.1.0); `compareRunManifests` and `validateBatchMetadata` refuse a different build; `finalize-batches` validates against the run's build |
| 3 | Batch manifest carries the run-level total | **Verified** | `BatchManifest.UninspectedCommitCount` = sum of per-record counts, recomputed and checked on resume and finalize; single-file mode prints the total |
| 4 | `cohort-export` copies the count into the case (`cohort-case/v2`), carries it for positives, refuses a negative with a non-zero count, fails closed on absence | **Verified** | `internal/cohort/export.go` (`correlationCounted`, exclusions `uncounted_negative` / `uninspected_negative`); tests `TestNegativesRequireACountedCleanCorrelation`, `TestUncountedPositiveIsAdmittedAndFlagged` |
| 4a | `earliest_fix_date` on the case and `--min-fix-date` as a manifest-recorded filter (`cohort-manifest/v2`) | **Verified** | `earliestFixDate` (strong; medium fallback under `any`); test `TestMinFixDateAdmitsOnlyLateFixes` |
| 5 | `merge-correlated`: replace by PR number, refuse on any provenance mismatch, write a flip report | **Verified** | `internal/correlatemerge/merge.go`, `internal/cli/merge_correlated.go`; tests `internal/correlatemerge/merge_test.go`; end-to-end on synthetic files (merge, report, refusal to overwrite) |
| — | Partial re-correlate run on the real candidates file | **Not started** — evaluator-side; needs real data access this role does not have | see "What the run looks like" |

"Verified": `go vet ./...` and `go test ./...` pass on every package in this session, and
`merge-correlated` was run end to end on synthetic input. Points 1–5 are **Shipped** as
`df9f619` on `main`; nothing is Integrated (no real record has been correlated by the
new build yet).

## Version impact

| Artifact | Before | After | Why |
|---|---|---|---|
| Pipeline record | `1.0.0` | `1.1.0` | additive fields; `correlate` now stamps `schema_version` on the records it writes |
| Batch run/batch manifests | `1.0.0` | `1.1.0` | new fields; a run cannot be resumed or finalized across builds |
| Cohort case | `cohort-case/v1` | `v2` | `correlation_counted`, `uninspected_commit_count`, `earliest_fix_date` |
| Cohort manifest | `cohort-manifest/v1` | `v2` | `min_fix_date` filter, four new exclusion reasons, stricter negative rule |
| Flip report | — | `flip-report/v1` | new |

Not touched: the bundle wire contract, `reviewer-metadata/v1`, `engine-manifest/v1`, the
pilot-v1 protocol, matching criteria, the exposure floor.

## What the run looks like (evaluator side)

1. Build the new binary. Select the re-correlate set from the current candidates file:
   records with zero signals at every tier, plus positives with any non-merge strong
   signal at `evidence_scope: unknown`. Write them to a subset JSONL (a `jq` filter; no
   command was added for this, deliberately — selection is the evaluator's, and the
   merge refuses anything that is not a strict re-correlation of a base record).
2. `miner correlate` over the subset with the same `--observation-end`, config and
   mirror, into a fresh `--batch-dir`.
3. `miner finalize-batches` → subset file.
4. `miner merge-correlated --base <full correlated> --subset <subset> --out <merged>
   --report <flips>`.
5. `miner score` over the merged file; `miner cohort-export` over the result.

The flip report's `from_none / subset_none_before` is the number the pre-registration
carries.

## Limitations to state

- **Counting is per record and depends on load.** The diff cache evicts on failure, so a
  commit that timed out for one record may succeed for the next. Two runs of the same
  subset can report different counts under different load; the count is a fact about
  the run that produced the record, which is why `correlated_by` and the batch
  manifests pin the build and the per-batch total. It is not a property of the commit.
- **Only vocabulary-filtered commits are counted.** A commit with no fix vocabulary is
  never inspected and never counted, by design: it could not have produced a signal.
- **Strong signals with a failed diff still exist**, at `evidence_scope: unknown` with
  no `changed_paths` (`correlator.go`, `makeSignal`). That is why the widened
  re-correlate set includes such positives: the class is right, the M4 inputs are not.
- **Old records are not retroactively countable.** A record without `correlated_by`
  cannot be a negative; the only remedy is re-correlation. The existing 2024 FRR
  candidates file has none. This is **not on H1's critical path**: the H1 cohort is a
  fresh 2026 collect (pre-registration §3) whose `correlate` runs on the 1.1.0 build,
  so every record carries the count from the start. The run recipe above matters only
  if the pilot cohort is to be rescored under the new rule, which is a separate
  decision.
- **`--min-fix-date` records an argument, not a fact about any model.** The
  pre-registration's rule rests on a training-cutoff claim the miner cannot check.
- **`earliest_fix_date` uses the signal timestamp the correlator stored**, which is the
  commit's author date (`gitx/repo.go`, `%aI`), not when it landed on a branch. Author
  date is never later than commit date, so a fix that passes the `--min-fix-date` gate
  on author date also passes on landing date: the choice is conservative for validity.
  Its cost is yield, not correctness — a fix authored in May and landed in June is
  excluded. State it as a yield cost in the cohort document.
