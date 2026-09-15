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

- **Source:** FRRouting/frr pull requests merged 2026-01-01 to 2026-06-30, collected fresh by `miner collect`. This window post-dates the reviewer model's training cutoff by construction; verify the cutoff date of the exact model string used and record it in the cohort doc (see §12 C1 — the requirement is on fixing-commit dates, and the window may need to move).
- **Size:** 40 cases, target 20 positives / 20 negatives. Minimum acceptable: 32 (16/16).
- **Positive:** a later commit with strong corrective evidence (`Fixes:` SHA/PR, revert, or explicit regression attribution) pointing at the PR, *and* the evaluator classifies the escape as system-level under §7 — not `classical-memory-safety` unless it manifested as a runtime failure in a routing/state path. Selection reads retrospective evidence; that is legitimate for construction and means nobody who selected may tune `internal/heuristics`.
- **Negative:** no corrective evidence within the follow-up window (see §12 A1, A3 for what "no corrective evidence" requires), matched to a positive on subsystem (daemon), diff size band (±1 order of magnitude in +/− lines), and merge month (see §12 C2 — the shipped matcher differs). Each negative is read by the evaluator; the standard is the existing one — a construct a competent reviewer might flag where flagging would be wrong is preferred but not required. Security-adjacent code is excluded as a negative.
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

## 12. Amendments before the first arm run

Per the status line, in-place amendment is permitted until the first treatment-arm run; after that, changes go to `h1-preregistration-v2.md`. Each entry is dated. Sections 1–11 above are the frozen v1 text; where an amendment changes their meaning, the section carries an inline pointer here.

### Amendments — verified findings, carried forward 2026-09-15

**A1. Diff-failure accounting is a precondition for admitting any negative (amends §3).** The silent failure in correlation is `CommitDiff`'s four 15-second subprocess timeouts (`internal/gitx/repo.go`, `limits.go`), swallowed by `getSummary` (`internal/correlator/correlator.go:275-282`). Under load a record can lose weak and medium signals with only a strong hit leaving a trace, so a zero-signal record can be a load artifact. No cohort freezes until per-record accounting exists and every negative shows zero swallowed failures. Arm H consumes the same correlate output and inherits the precondition. Confirmed by citation verification against the checkout.

**A2. The metadata admission rule is decided before freeze, not after (amends §1's "review-time information").** On the ten pilot bundles, 1 of 10 `reviewer/metadata.json` files carried a description and 1 of 10 a title; all 10 carried commit messages. Cause: with cutoff = `merged_at`, post-merge activity bumps `updated_at` past the cutoff and the description is dropped (`prospectiveexport/export.go:201, 217`); the title is dropped too unless `IssueEventsComplete` is set (`:278-280`). Every pilot result was produced under that condition. Whether and how to admit pre-cutoff title and body is a `reviewer-metadata/v2` decision made before the H1 cohort freezes; whichever way it goes, per-case admission is recorded in the cohort manifest so arms can be stratified on it.

**A3. "No corrective evidence" means zero signals at every tier — strong, medium, and weak (amends §3).** Absence of strong and medium alone is not enough. The shipped `cohort-export` enforces this.

**A4. The exposure floor is a policy value, not a correlator search window.** Strong and medium signals are searched to `observation_end`; only the weak file-overlap check is bounded, at 90 days. `PLAN.md:215`'s 180-day function window was never implemented. §3's follow-up-window language describes how long evidence had to appear, not how far the correlator looked.

**A5. What n=40 can and cannot show, stated in advance (informs §2).** Significance is non-criterial, but the ceiling bounds how a result is read. At 20 positives under a fully one-directional McNemar: +15 on precision (6 cases) can reach p ≈ 0.03; +15 on recall or reason-match (3 cases) cannot. A recall result at +15 is "consistent, underpowered," not a miss.

### Conflicts — open, owner's decision

**C1. The contamination window (§3).** §3 says the Jan–Jun 2026 collect "post-dates the reviewer model's training cutoff by construction." The requirement is that *fixing commits* post-date the cutoff, not that PR merge dates do — a January PR whose fix landed in March is contaminated even though the PR is inside the window. And if the exact Opus string's cutoff is mid-2026, Jan–Jun PRs do not post-date it. The verification §3 already calls for decides whether the window holds or must shift later. **Open until the cutoff date is recorded.**

**C2. Negative matching criteria (§3).** §3 matches on subsystem, size band, and merge month. The shipped `cohort-export` (`ac05526`) matches on stateful-score band, subsystem, and size band — it does not match on merge month and does match on stateful band. Either the code changes (`cohort-case/v2`) or §3 does. **Open; not resolvable by editing this document alone.**
