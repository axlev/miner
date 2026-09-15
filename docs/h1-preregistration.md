# H1 pre-registration

**Status:** frozen on commit. Changes after the first treatment-arm run require a new document (`h1-preregistration-v2.md`) stating what changed and why; this file is never edited in place after that point.

**Date frozen:** 2026-09-15
**Owner:** alex (decisions), `evaluator` role (labels and scoring)

## 1. Hypothesis

On a cohort of FRR changes merged in 2026, roughly half of which later caused a system-level escape, an A→B pipeline (discovery + substantiation) with a system-aware discovery prompt and read+grep tooling, given only review-time information, classifies each change as RISKY or CLEAN with a stated mechanism, and does so materially better than both a generic single-pass reviewer on the same model and a zero-LLM history baseline.

## 2. Pass / fail — fixed now

H1 **passes** if all three hold on the frozen cohort:

| Criterion | Threshold |
|---|---|
| Reason match on treatment RISKY true positives | ≥ 60% judged `MECHANISM` |
| Recommended-validation hit rate on treatment RISKY true positives | ≥ 50% |
| Case-level precision of RISKY, treatment vs. each baseline | ≥ +15 points over both |

H1 **fails** if any criterion is missed.

Reported, not pass/fail: case-level recall of RISKY for all three arms, overall and by failure class (§7); cost per case and wall-clock latency per case for arms T and G, taken from run manifests; `LOCALITY_ONLY` rate. Recall is the safety measure; it is demoted here because the first commercial use is a gate inside a change loop, where a false flag costs more than a miss. It stays in every figure.

A failed H1 is then read by failure class to see whether a narrower hypothesis survives; that reading is exploratory and is reported as such, not as a pass.

Significance is reported alongside but is not a pass criterion: PR-level exact permutation test over labellings, two-sided, on precision. Finding-level tests are not used (clustering — see `engine-runner/docs/discussion-brief-traffic-light.md`, retraction).

## 3. Cohort

- **Source:** FRRouting/frr pull requests merged 2026-01-01 to 2026-06-30, collected fresh by `miner collect`. This window post-dates the reviewer model's training cutoff by construction; verify the cutoff date of the exact model string used and record it in the cohort doc.
- **Size:** 40 cases, target 20 positives / 20 negatives. Minimum acceptable: 32 (16/16).
- **Positive:** a later commit with strong corrective evidence (`Fixes:` SHA/PR, revert, or explicit regression attribution) pointing at the PR, *and* the evaluator classifies the escape as system-level under §7 — not `classical-memory-safety` unless it manifested as a runtime failure in a routing/state path. Selection reads retrospective evidence; that is legitimate for construction and means nobody who selected may tune `internal/heuristics`.
- **Negative:** no corrective evidence within the follow-up window, matched to a positive on subsystem (daemon), diff size band (±1 order of magnitude in +/− lines), and merge month. Each negative is read by the evaluator; the standard is the existing one — a construct a competent reviewer might flag where flagging would be wrong is preferred but not required. Security-adjacent code is excluded as a negative.
- **Known weakness, accepted:** follow-up for a June 2026 merge is under three months at collection time. Negatives from the late window are weaker evidence of cleanliness. Report negatives' follow-up length as a covariate; do not silently exclude late ones.
- **Freeze:** all 40 bundles pass `cohort-verify --bench-repo` before any arm runs. Cohort doc lists case IDs, class labels, and provenance hashes. Evaluator-only.

## 4. Arms

All three arms run over identical frozen `reviewer/` bundles.

| Arm | What it is | Model | Stages | Tools |
|---|---|---|---|---|
| **T** treatment | System-aware discovery prompt (invariants, consumers, config surfaces, restart paths, in-tree tests) → substantiation → recommended validation | Opus | A→B | Read, Grep, Glob |
| **G** generic | Single-pass reviewer, no domain content, no staging, prompt modelled on a general-purpose PR reviewer | Opus (same string) | A only | Read, Grep, Glob |
| **H** history | Zero-LLM. Per-subsystem strong-signal defect density and churn over the 24 months before each case's cutoff, computed from pre-cutoff data only. RISKY if the touched subsystem's score is in the top tercile | — | — | — |

Stage 3 is not run in any arm. `pilot-v1` is untouched; T and G are new protocol versions with their own fingerprints.

## 5. Verdict mapping — fixed now

A case is **RISKY** in arms T and G iff at least one finding has severity ≥ `medium` and, in T, stage-B disposition `CONFIRMED` or `NARROWED`; in G, confidence ≥ 0.6. Otherwise CLEAN. `INCONCLUSIVE` and `REJECTED` do not count.

A continuous risk score (max over findings of severity-weight × confidence) is also recorded for ROC reporting, but the pre-registered comparison uses the binary rule above.

## 6. Reason match

For each RISKY true positive in T and G, the `internal/judge` two-call procedure compares the case's highest-ranked finding to the materialised fixing diff. Verdicts `MECHANISM` / `LOCALITY_ONLY` / `NONE` / `TOO_VAGUE` as currently defined. Only `MECHANISM` counts toward the 60% criterion. `LOCALITY_ONLY` rate is reported as the rationalisation tell. Judge is in-family (Opus); this is stated on every figure. Cross-vendor re-judging is desirable, not required for this pre-registration.

**Recommended-validation hit rate.** For each RISKY true positive in T, the report's recommended tests or scenarios are compared to the fixing commit. A hit is: at least one named test file, topotest directory, or scenario was touched or added by the fixing commit, or names a test that the fixing commit's own description says was run. Matching is by path or explicit name, performed by the evaluator, recorded per case with the matched path. No judge call is used for this measure.

## 7. Failure-class taxonomy

Assigned by the evaluator to every positive, from the fixing commit and its discussion, before any arm runs. Closed enum:

`ordering-state-machine` · `cross-module-invariant` · `config-interaction` · `error-path-lifecycle` · `restart-upgrade` · `protocol-parsing` · `classical-memory-safety` · `timing-race` · `resource-exhaustion` · `other`

Recall is reported per class. Pre-stated expectation, recorded so it can be wrong: T recall is highest on the first five classes and lowest on `timing-race` and `resource-exhaustion`.

## 8. Contamination controls

- Cases post-date the model cutoff (§3).
- Post-hoc scan over every sealed `review-*.json` for: the fixing commit SHA (any prefix ≥ 7), the fixing PR number, CVE identifiers, and phrases lifted from post-merge discussion. Any hit voids the case for that arm and is reported.
- The planted-`CLAUDE.md` steering test is re-run once under the T tool grant before the arm starts.

## 9. Budget and stopping

- Estimated: T ≈ $250–350, G ≈ $80–120, H ≈ $0, judge ≈ $30. Cap: $600 total.
- Circuit breakers at ~5× expected per-stage spend. A case whose winning attempt followed a cap kill is flagged `retried` in results and reported separately; it is not silently counted clean.
- Stop early only for a harness defect, never for the result looking good or bad at n<40.

## 10. What this does not claim

- Nothing about generalisation beyond FRR.
- Nothing about reviewer behaviour, willingness to pay, or the velocity effect of a gate inside a change loop — the cost and latency figures are inputs to that question, not an answer to it.
- Nothing about stage 3.
- A pass is evidence the approach is worth a customer retrospective, not evidence of a product.

## 11. Roles

- `coder-miner`: collect, correlate, export, matching procedure. No oracle beyond existing grants.
- `coder-engine-runner`: protocol versions, verdict schema, arms T/G/H plumbing, contamination scan. No oracle.
- `evaluator`: cohort selection, class labels, negative-control reading, case-level scoring, this document's results section. Holds the oracle. Never edits prompts, adapters, or `reviewer/` exports.
