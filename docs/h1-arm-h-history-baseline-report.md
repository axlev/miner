# Arm H — history baseline: status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the peer dispatch for
`history-baseline`, operationalising pre-registration §4, with the four defaults
confirmed in session. Status terms are `AGENTS.md` "Status vocabulary".

## Status

| Point | Status | Where |
|---|---|---|
| Pre-cutoff history: all ancestors of `<merge_commit>^1`, committer date in [cutoff − 24 months, cutoff) | **Verified** | `gitx.Repository.HistoryWalk` (one `git log --numstat` walk), `ResolveCommit`; test `TestBaselineFeaturesAndVerdict` (a commit authored inside the window but committed after cutoff is excluded; pre-window commits excluded) |
| Subsystem = the sampler's key; primary = most changed lines, ties to higher score | **Verified** | `cohort.SubsystemOfPaths` exported; `Build`; tests |
| Strong-tier corrective detection reusing the correlator's matchers; attribution `fixed_commit` / `fixed_pr_merge` / `self` recorded per defect | **Verified** | `correlator.StrongCorrectiveRefs`; `Build` step 2; test asserts one `fixed_commit` and three `self` |
| Features `commits`, `churn_lines`, `defects`, `density`; score = mean mid-rank percentile; terciles strictly-below; `min_commits` 20 → `low_activity` CLEAN | **Verified** | `percentile`, `tercile`; tests `TestBaselineTopTercileIsRiskyWithThreeActiveSubsystems`, `TestBaselineLowActivityAndUnresolvable`, `TestTercileTiesFallLower` |
| Contract fields verbatim; extras under other keys; `input_sha256`; `case_id` via `GenerateCaseID` | **Verified** | `Result`; `schemas/history-baseline.schema.json`; `TestBaselineIsDeterministicAndImmutable` checks the contract keys |
| Deterministic up to `produced_at`; output directory atomic and immutable | **Verified** | `WriteAll`; same test |
| Synthetic fixture, three subsystems, never FRR-derived | **Verified** | `baseline_test.go` `newFixture` |

"Verified": `go vet ./...` and `go test ./...` pass in this session. Not run on a real
cohort yet: the 2026 data does not exist until the collect runs.

## Version impact

| Artifact | Version | Note |
|---|---|---|
| Baseline file | `history-baseline/v1` | new, evaluator-only |
| `gitx.HistoryWalk`, `gitx.ResolveCommit`, `cohort.SubsystemOfPaths`, `correlator.StrongCorrectiveRefs` | — | additive exports; no behaviour change to existing callers |

## Limitations to state

- **A many-way tie at the top of the scored subsystems can leave no subsystem in
  tercile 3.** The strictly-below rule makes ties fall lower, as specified; with three
  scored subsystems two of which tie at the top, both land in tercile 2 and the case is
  CLEAN. On FRR with dozens of active subsystems this is unlikely to bind; on a small
  window it can. Recorded in `rule.tercile`.
- **Fixed-PR attribution depends on GitHub's merge-commit subject** ("Merge pull
  request #N"). A PR merged by squash or rebase has no such commit in the walk; its
  number cannot be resolved offline and the defect attributes to the corrective commit
  itself (`self`). Recorded per defect.
- **A corrective commit whose fixed SHA is outside the window still attributes to that
  SHA's subsystem**, provided it resolves in the mirror and its files are known from the
  walk; if the fixed commit predates the window its subsystem is unknown to the walk and
  attribution falls to `self`. This is a slight under-attribution for old defects and is
  the conservative direction.
- **`min_commits` = 20 within 24 months** is the frozen threshold; on FRR most daemons
  clear it easily, `tools/`, `doc/` and small libraries may not, and such a case is
  CLEAN by rule with the reason written.
- **`churn_lines` counts every commit touching the subsystem**, merges included, so a
  merge commit's numstat (which git reports against the first parent) can double-count
  lines already counted on the side branch. This inflates churn uniformly across
  subsystems and does not change ranks much, but it is not deduplicated.
