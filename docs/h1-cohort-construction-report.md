# H1 cohort construction — status report

Date: 2026-09-15. Session: `miner-coder`, acting on the dispatch that accompanies
`docs/h1-pre-registration.md`. Status terms are those defined in `AGENTS.md`.

## Status per item

| # | Item | Status | Version impact |
|---|---|---|---|
| 1 | Negative-control sampler, first-class export mode, 1:1 matched | **Verified** | New artifacts `cohort-case/v1`, `cohort-manifest/v1`; pipeline record `1.0.0` untouched; bundle wire contract untouched |
| 2 | Exposure guard, same pass, exposure days per record | **Verified** | Same artifacts as item 1 |
| 3 | Oracle / reason-label schema, evaluator-only | **Implemented** (a schema has no behaviour to run; JSON validity checked) | New `reason-label/v1`, `reason-label-categories/v1`; nothing existing changed |
| 4 | Cohort manifest, same pass | **Verified** | Part of `cohort-manifest/v1` |
| 5 | AND/OR clarification | **Verified** from the code; documented in `README.md` and `docs/export-contract.md` §3 | None |
| — | HIGH/MEDIUM/LOW and score normalisation: documented, not refactored | **Verified** | None |

"Verified" means: `go vet ./...` and `go test ./...` pass in this session (every
package), and the command was run end to end on a synthetic candidates file, producing
the two files and refusing a second run into the same directory.

Nothing here is **Shipped**: no commit has been made. Nothing is **Integrated**: no
downstream arm has consumed a cohort yet, and no real candidates file was read (the
data-safety rule in `AGENTS.md`; no real data was requested).

## What was built

- `internal/cohort/export.go` — `cohort.Export`: classification, matching, provenance
  stamping, content hash, atomic write. `internal/cohort/export_test.go` — 12 tests.
- `internal/cli/cohort_export.go` — `miner cohort-export`, registered in `root.go`.
- `schemas/reason-label.schema.json`, `schemas/reason-label-categories.schema.json`.
- Docs: `README.md` (new Stage 3 section; filter semantics under `export`),
  `docs/export-contract.md` (§1, §2, §3 new subsection, §4 `score` row),
  `AGENTS.md` decisions log (two entries), `docs/h1-pre-registration.md` (one dated
  decision, see "Limitations" below).

## Item 5 — `--require-signals strong,medium`: AND or OR?

Neither. The flag does not exist. It appears only in `PLAN.md` line 346, alongside
`--min-stateful-score`, which also does not exist. The real flags on `export`
(`internal/cli/export.go` lines 94–96) are `--min-score`, `--require-fix-signal`, and
`--require-strong-fix`.

`--require-fix-signal` is **OR**: it calls `HasAnyFixSignal`, defined at
`internal/model/candidate.go` lines 22–24 as

```go
return len(r.Retrospective.StrongSignals) > 0 || len(r.Retrospective.MediumSignals) > 0
```

`--require-strong-fix` is strong-only. There is no way to express "strong AND medium"
from the CLI. `cohort-export --positive-signals any` reuses the same OR rule;
`strong` (the default) is strong-only.

## Limitations to state, not findings

1. **The dispatch's premise about correlator windows is wrong.** There is no 180-day
   function window. `CorrelatePR` (`internal/correlator/correlator.go` lines 257–263)
   admits every commit strictly after `merged_at` and at or before `observation_end`
   for strong and medium signals; only the weak `SAME_FILE_MODIFICATION` check is
   bounded, at 90 days (line 397). The 180-day window is in `PLAN.md` line 215 and was
   never implemented. The exposure floor of 180 days is therefore a **policy** value
   from the pre-registration, kept as the default, not a value the code determines.
   A dated decision recording this was appended to `docs/h1-pre-registration.md`'s
   decisions log, because that document says changes to it are dated decisions; the
   requirement itself was not changed. If alex prefers that entry to come from the
   pre-registration's owner instead, it is one block to remove.
2. **Weak signals depend on a Git handle at correlate time.** `SAME_FILE_MODIFICATION`
   needs a commit diff summary; if `correlate` ran without a usable local repository,
   weak signals are systematically absent and more records look zero-signal than
   should. The record does not say whether the handle was available, except
   indirectly via `commit_relationships[].analysis_status == "unavailable"` on records
   that had strong/medium links. The sampler cannot detect this. State it as a
   limitation of any cohort built from a correlate run whose Git availability is not
   independently known.
3. **Symbol-extraction misses inflate the CLEAN pool** — deferred by the dispatch, but
   it bears directly on item 1: `SAME_FUNCTION_FIX` needs `changed_function_locations`,
   so a PR whose functions were not extracted can only ever produce strong or weak
   evidence. A negative sampled from such a PR is weaker than one whose functions were
   extracted. Not measurable from the record.
4. **Matching is greedy, not optimal.** Positives are processed in ascending PR order
   and each takes the nearest unused negative in its cell. This is deterministic and
   documented in the manifest, but a different order could pair more positives when
   negatives are scarce in a cell. Reported as `unmatched_positives`, never hidden.
5. **Score band is `stateful.category`**, i.e. three bands from the config thresholds
   (0.60 / 0.30 by default). Two records in the same category can still be 0.30 apart
   in `LOW`. Nearest-score tie-breaking within the cell mitigates but does not bound
   this; the per-record `stateful.score` is in the output for the evaluator to check.
6. **The exposure floor applies to both classes.** This is a deliberate departure from
   the narrowest reading of the dispatch ("exclude PRs merged inside 180 days"), which
   could be read as negatives only. Applying it to negatives alone would make merge
   date predict the label. Positives excluded for this reason are counted under
   `exposure_below_minimum` in the manifest.
7. **Precision is conditional on the 1:1 ratio** (already in the pre-registration).
   The manifest's `unused_negative` count is the size of the zero-signal pool that was
   *not* used, which is the number the evaluator needs to say what the real base rate
   in the candidate set was.

## Not done, by instruction

Symbol-extraction measurement, rename/move following, score normalisation and the
HIGH/MEDIUM/LOW thresholds: not built, not planned. The thresholds and the
non-normalised `score` are now documented at `README.md` (under `export`) and
`docs/export-contract.md` §4 with file references; no code changed.

## Needs a decision

None of the five items required touching the frozen pilot-v1 protocol or the bundle
wire contract, so nothing is blocked on that. The one decision outstanding is the
standing one: whether to commit this work (then push).
