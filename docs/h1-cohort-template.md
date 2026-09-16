# H1 cohort document — template

The frozen H1 cohort gets one document, written by whoever freezes it, following this
shape. It is the artifact a reader consults years later to know what the cohort *was*,
so it records the strata that make a result readable, not only the case list.

**This document is oracle-grade evaluator material.** It names which cases carry a
defect and describes each one. Under `system-design.md` §13 the roles permitted to hold
it are the miner runtime and the evaluator: not a reviewer, and not `coder-miner` or
`coder-engine-runner`. Keep it out of both repositories and out of any prospective
bundle root.

---

## 1. Identity

- Name, freeze date, and the `records_sha256` from `cohort-manifest.json`. That hash,
  not the file path, is what every arm's result binds to.
- One row per source from the manifest's `sources` array: label, `records_path`,
  `records_sha256`, `config_hash`, `target_repo_head_sha`, `harvested_at`,
  `observation_end`, `correlated_by`. Nothing is required to match across sources; the
  point of listing them is that a reader can tell which window a case came from.
- The exact `cohort-export` invocation, including `--match-key`, `--max-per-subsystem`
  and the `--positives` list. Two runs with different arguments over the same files are
  different cohorts.
- Miner commit and engine commit used for `cohort-verify`.

## 2. Selection

- How the positives were chosen: the sampler produced a pool, the evaluator read it,
  and **the read is the filter**. Say how many candidates were read and how many were
  admitted. On the first H1 pass that ratio was 10 of 16 for 2026H1 strong signals,
  with the rejections being backport-merge `Fixes:` references, feature-withdrawal
  reverts, `Fixes:` on build and doc changes, and fixes that existed only on unmerged
  pull-request heads. Record the equivalent counts and classes for this cohort.
- How the negatives were chosen from `negatives-<window>.jsonl`, and on what basis
  beyond the sampler's rule.
- Subsystem spread, against `--max-per-subsystem`. A cohort concentrated in one daemon
  measures that daemon.

## 3. Strata a reader must be able to condition on

These are not footnotes. Each one can move a per-arm number, and each is available
per case without opening the oracle.

| Stratum | Where it comes from | Why it matters |
| --- | --- | --- |
| `source` | cohort case | Two collection windows with different observation ends and repository states |
| `followup_days` | cohort case (`exposure_days`) | A CLEAN label from three months of follow-up is weaker than one from two years. A10 makes it a covariate, not a gate; report recall against it |
| `admission.title`, `admission.description` | cohort case, evaluator audit | A case whose body was edited after its merge is `omitted-post-cutoff-edit` and reaches every arm with no description. **Expect a real stratum of these in any window whose bundles were refreshed rather than collected fresh.** Do not smooth it over: report per-arm results with and without those cases |
| `correlation_counted`, `uninspected_commit_count` | cohort case | A negative from a run whose diffs failed is not a verified negative |
| contamination probe status | probe files, per case per model | A treatment result on a recalled case is not evidence |
| `cell.category`, `cell.subsystem`, `cell.size_band` | cohort case | The matching key may not have used all three; the unused fields still vary and can still explain a difference |

## 4. Arm H

Record that arm H is a **floor, not a competitor**, and why: its verdict is a property
of the subsystem over a 24-month window that differs between a positive and its matched
negative only by the two cutoffs. Under subsystem-matched pairing both members of a pair
get the same verdict by construction, so H's precision sits near the base rate and its
recall near 1.0. Report its numbers; do not read a treatment win over H as the headline
without saying this.

Also record, per case, `scored_subsystems` and `tercile3_count`. A case where
`tercile3_count` is 0 is one where H could not have said RISKY about anything.

## 5. Known limitations to restate

Carry these forward rather than rediscovering them:

- Strong-tier signals include false attributions the sampler cannot see: backport
  merges citing the PR, reverts that are feature withdrawals, `Fixes:` on build or doc
  changes, and fixes living only on unmerged `refs/pull/*` heads. The evaluator read is
  the only filter for these today (`docs/decisions.md`, 2026-09-16).
- Medium-tier `SAME_FUNCTION_FIX` attributes any later fix-vocabulary commit touching
  the same function; on the first pass, screening all 214 matchable medium candidates
  yielded two admitted positives.
- Symbol extraction has no measured coverage, so a missed function suppresses medium
  signals and inflates the CLEAN pool.
- Renames and moves are not followed across the correlation window.
- `stateful.score` is not normalised; it is a clamped sum of rule weights, and its
  three-level category is the matching band.

## 6. Reproducing

The exact commands, in order, from `<run-root>/evaluator/COMMANDS.md`: refresh bundles,
freeze, verify against a named engine commit, publish the bundles, keys, history
baseline, labels. Give each output's path and, where it has one, its schema version.
