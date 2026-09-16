# H1 pre-registration

**Status:** frozen on commit. Changes after the first treatment-arm run require a new document (`h1-preregistration-v2.md`) stating what changed and why; this file is never edited in place after that point.

**Date frozen:** 2026-09-15
**Owner:** alex (decisions), `evaluator` role (labels and scoring)

## 1. Hypothesis

On a cohort of FRR changes merged in 2026 (window amended to 2024 and 2026H1, §12 A11), roughly half of which later caused a system-level escape, an A→B pipeline (discovery + substantiation) with a system-aware discovery prompt and read+grep tooling, given only review-time information, classifies each change as RISKY or CLEAN with a stated mechanism, and does so materially better than both a generic single-pass reviewer on the same model and a zero-LLM history baseline.

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

Significance is reported alongside but is not a pass criterion: PR-level exact permutation test over labellings, two-sided, on precision (which test applies to which comparison: §12 A8). Finding-level tests are not used (clustering — see `engine-runner/docs/discussion-brief-traffic-light.md`, retraction).

## 3. Cohort

- **Source (amended, §12 A11):** FRRouting/frr pull requests merged 2026-01-01 to 2026-06-30, collected fresh by `miner collect`. The reviewer model is `claude-opus-5`, training data cutoff **May 2026** (platform.claude.com models overview, fetched 2026-09-15; reliable-knowledge and training-data cutoffs are identical for this model). PR merge dates inside the window may fall before that cutoff — acceptable, since a model knowing the codebase is what a competent reviewer brings. What must post-date the cutoff is the *outcome*: **a positive is admissible only if its earliest fixing commit is dated on or after 2026-06-01.** Record the earliest fixing-commit date per positive in the cohort doc. Negatives are not gated by the cutoff (no fix to leak), but §8's post-hoc scan covers post-merge discussion for them. See §12 C1.
- **Size:** 40 cases, target 20 positives / 20 negatives. Minimum acceptable: 32 (16/16).
- **Positive (amended, §12 A9, A11, A12):** a later commit with strong corrective evidence (`Fixes:` SHA/PR, revert, or explicit regression attribution) pointing at the PR, *and* the evaluator classifies the escape as system-level under §7 — not `classical-memory-safety` unless it manifested as a runtime failure in a routing/state path. Selection reads retrospective evidence; that is legitimate for construction and means nobody who selected may tune `internal/heuristics`.
- **Negative (matching key amended, §12 A9):** no corrective evidence within the follow-up window (see §12 A1, A3 for what "no corrective evidence" requires), matched 1:1 to a positive on stateful-score category (HIGH/MEDIUM/LOW), subsystem (daemon), and changed-lines band (0, 1–10, 11–50, 51–200, 201–1000, 1001+), as the shipped `cohort-export` implements (see §12 C2). Each negative is read by the evaluator; the standard is the existing one — a construct a competent reviewer might flag where flagging would be wrong is preferred but not required. Security-adjacent code is excluded as a negative.
- **Known weakness, accepted:** follow-up for a June 2026 merge is under three months at collection time. Negatives from the late window are weaker evidence of cleanliness. Report negatives' follow-up length as a covariate; do not silently exclude late ones.
- **Freeze:** all 40 bundles pass `cohort-verify --bench-repo` before any arm runs. Cohort doc lists case IDs, class labels, and provenance hashes. Evaluator-only.

## 4. Arms

All three arms run over identical frozen `reviewer/` bundles.

| Arm | What it is | Model | Stages | Tools |
|---|---|---|---|---|
| **T** treatment | System-aware discovery prompt (invariants, consumers, config surfaces, restart paths, in-tree tests) → substantiation → recommended validation | Opus | A→B | Read, Grep, Glob |
| **G** generic | Single-pass reviewer, no domain content, no staging, prompt modelled on a general-purpose PR reviewer | Opus (same string) | A only | Read, Grep, Glob |
| **H** history (a floor, §12 A10) | Zero-LLM. Per-subsystem strong-signal defect density and churn over the 24 months before each case's cutoff, computed from pre-cutoff data only. RISKY if the touched subsystem's score is in the top tercile | — | — | — |

Stage 3 is not run in any arm. `pilot-v1` is untouched; T and G are new protocol versions with their own fingerprints.

## 5. Verdict mapping — fixed now

A case is **RISKY** in arms T and G iff at least one finding has severity ≥ `medium` and, in T, stage-B disposition `CONFIRMED` or `NARROWED`; in G, confidence ≥ 0.6. Otherwise CLEAN. `INCONCLUSIVE` and `REJECTED` do not count.

A continuous risk score (max over findings of severity-weight × confidence) is also recorded for ROC reporting, but the pre-registered comparison uses the binary rule above.

Severity weights: §12 A6.

## 6. Reason match

For each RISKY true positive in T and G, the `internal/judge` two-call procedure compares the case's highest-ranked finding to the materialised fixing diff. Verdicts `MECHANISM` / `LOCALITY_ONLY` / `NONE` / `TOO_VAGUE` as currently defined. Only `MECHANISM` counts toward the 60% criterion. `LOCALITY_ONLY` rate is reported as the rationalisation tell. Judge is in-family (Opus); this is stated on every figure. Cross-vendor re-judging is desirable, not required for this pre-registration.

**Recommended-validation hit rate.** For each RISKY true positive in T, the report's recommended tests or scenarios are compared to the fixing commit. A hit is: at least one named test file, topotest directory, or scenario was touched or added by the fixing commit, or names a test that the fixing commit's own description says was run. Matching is by path or explicit name, performed by the evaluator, recorded per case with the matched path. No judge call is used for this measure.

## 7. Failure-class taxonomy

Assigned by the evaluator to every positive, from the fixing commit and its discussion, before any arm runs. Closed enum:

`ordering-state-machine` · `cross-module-invariant` · `config-interaction` · `error-path-lifecycle` · `restart-upgrade` · `protocol-parsing` · `classical-memory-safety` · `timing-race` · `resource-exhaustion` · `other`

Recall is reported per class. Pre-stated expectation, recorded so it can be wrong: T recall is highest on the first five classes and lowest on `timing-race` and `resource-exhaustion`.

## 8. Contamination controls

- Cases post-date the model cutoff (§3). *Withdrawn by §12 A11: replaced by per-positive probes A and B; cutoff recorded as a covariate.*
- Post-hoc scan over every sealed `review-*.json` for: the fixing commit SHA (any prefix ≥ 7), the fixing PR number, CVE identifiers, and phrases lifted from post-merge discussion (defined in §12 A7). Any hit voids the case for that arm and is reported.
- The planted-`CLAUDE.md` steering test is re-run once under the T tool grant before the arm starts.

## 9. Budget and stopping

- Estimated: T ≈ $250–350, G ≈ $80–120, H ≈ $0, judge ≈ $30. Cap: $600 total (raised to $750 and probes added, §12 A11).
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

**A2. The metadata admission rule is decided before freeze, not after (amends §1's "review-time information").** On the ten pilot bundles, 1 of 10 `reviewer/metadata.json` files carried a description and 1 of 10 a title; all 10 carried commit messages. Cause: with cutoff = `merged_at`, post-merge activity bumps `updated_at` past the cutoff and the description is dropped (`prospectiveexport/export.go:201, 217`); the title is dropped too unless `IssueEventsComplete` is set (`:278-280`). Every pilot result was produced under that condition. **Decided 2026-09-15: admit title and body if pre-cutoff.** "Pre-cutoff" means the field's *last edit* is on or before `merged_at` — a body written at PR open and never touched is admitted; a body edited after merge to describe the regression is not. The current rule drops the field whenever the PR's `updated_at` is past the cutoff, which any post-merge comment or label triggers; v2 must establish the field's own edit time (GitHub exposes body and title edit history) and fall back to omission when that cannot be established. Per-case admission outcome — `admitted` / `omitted-post-cutoff-edit` / `omitted-unverifiable` — is recorded in the cohort manifest so arms can be stratified on it. This is `reviewer-metadata/v2`: contract- and fingerprint-affecting, and cross-repo — the miner's `prospectiveexport` emits it and the engine's `boundaryvalidator` must accept it.

**Scoped 2026-09-15, from the collector code.** The cache holds the PR object, issue comments, PR commits, and REST issue events (`internal/collector/github_client.go:62-102`); no GraphQL, no `userContentEdits`, no timeline API. So: **title** edit history is already present — REST issue events carry `renamed` events, which `reconstructTitle` walks (`prospectiveexport/export.go:277-305`). **Body** edit history is not — GitHub exposes it only via GraphQL `userContentEdits`, and no per-field last-edit time for the body can be derived from what is cached. v2 therefore splits: a **collector half** (GraphQL `userContentEdits` on the PR, stored with its own completeness flag) that **must precede the 2026 collect** so the new bundles carry it from the start, and a **`prospectiveexport` half** that can follow miner item 1. For any already-cached cohort PR it is a per-PR re-fetch, not a full re-collect.

**Correction to the pilot narrative above.** The issue-events fetch and `IssueEventsComplete` were added on 2026-09-06. A cache bundle fetched before that has no issue events and the flag false, so `reconstructTitle` returns not-ok even with zero renames and the title is omitted as *unverifiable* — not because of a post-merge edit. If the pilot cache predates 2026-09-06, most of the nine omitted titles are cache age, and a re-fetch of those ten bundles alone would restore them under v1's existing rule. The body omissions stand regardless, since body history was never fetched. Which reason applied per case is in each evaluator root's `metadata-export-audit.json` (`fields.title.reason`); unread.

**A3. "No corrective evidence" means zero signals at every tier — strong, medium, and weak (amends §3).** Absence of strong and medium alone is not enough. The shipped `cohort-export` enforces this.

**A4. The exposure floor is a policy value, not a correlator search window.** Strong and medium signals are searched to `observation_end`; only the weak file-overlap check is bounded, at 90 days. `PLAN.md:215`'s 180-day function window was never implemented. §3's follow-up-window language describes how long evidence had to appear, not how far the correlator looked.

**A5. What n=40 can and cannot show, stated in advance (informs §2).** Significance is non-criterial, but the ceiling bounds how a result is read. At 20 positives under a fully one-directional McNemar: +15 on precision (6 cases) can reach p ≈ 0.03; +15 on recall or reason-match (3 cases) cannot. A recall result at +15 is "consistent, underpowered," not a miss.

### Amendments — operational definitions the build required, 2026-09-15

Each of these fills a gap where §1–11 named a quantity without defining it. None is criterial; each is recorded in the output that applies it, so a result says which definition produced it.

**A6. Severity weights for the continuous risk score (defines §5's "severity-weight").** `low` 0.25 · `medium` 0.5 · `high` 0.75 · `critical` 1.0, linear, so ROC ordering is by severity and then by confidence within severity. Used only for ROC reporting; the pass/fail comparison is the binary rule in §5 and is unaffected. Until registered here the scorer emitted `risk_score: null` with the omission stated.

**A7. "Phrases lifted from post-merge discussion" (defines §8's fourth scan rule).** A hit is a run of **8 consecutive tokens** (lower-cased, split on non-alphanumerics) shared between any string in a sealed review and any post-merge discussion body in the case's contamination keys, **excluding** every such run that also occurs in material the reviewer was shown: `reviewer/diff.patch`, `reviewer/metadata.json`, and every repository file the review cites. Windows over `evidence[].excerpt` are not scanned by this rule (excerpts are verbatim file copies by contract; the SHA, PR and CVE rules still scan them). A window counts only if **at least 3 of its 8 tokens fall outside every excluded span** — a reviewer sentence that bridges two legitimately quoted lines with its own connector is not a lift; a lifted 8-token phrase adjacent to a quote still is. Consecutive matching windows collapse into one hit. Any hit voids the run for that arm; the scanner never un-voids, and the matched text is in the report. Parameters (8, 3, the exclusion sources) are recorded in every scan output.

**A8. Which permutation test is which (amends §2's "PR-level exact permutation test over labellings").** §2's wording describes the pilot's test, which asks whether one arm beats chance. The pre-registered pairwise comparisons (T−G, T−H on precision and recall) use a **paired test**: the null is that each case's two verdicts are exchangeable; discordant cases are enumerated exactly up to 20, seeded Monte Carlo (200 000 draws, seed recorded) above; one- and two-sided p both reported. This is the exact form of the McNemar reasoning A5 already used. Per arm versus chance, the label-permutation distribution of the case-level verdict × label table under fixed margins is **Fisher's exact test**, case-level (the pilot's clustering objection does not apply — the unit is the case), two-sided, one per arm; precision and recall are not separate tests under relabelling, they are the same association. Both are reported; neither is criterial.

### Amendments — cohort supply, 2026-09-16 (owner decision "go (e)")

Context, measured: the fresh 2026-01-01..06-30 collect (1,501 PRs) holds 16 strong-signal PRs; the ≥ 2026-06-01 fix-date gate (C1) leaves 7; 1:1 matching under C2's key leaves 3 pairs. An evaluator read of 214 medium-tier candidates found 2 real positives. Across the 2024 window (62 strong) and 2026H1 (16), 68 of 78 fixes land before 2026-06-01. The gate, not the sampler or the matching, capped n. Run report: `docs/h1-data-run-2026-09-15.md`; evaluator reads under the 2026 data root.

**A9. Positive definition, matching key, subsystem quota (amends §3).** (i) A positive is a PR whose corrective evidence the evaluator **confirms by reading the fixing commit(s)**: the fix corrects a defect the PR introduced or newly exposed, and the escape is system-level under §7. The sampler's tier (strong, or medium `SAME_FUNCTION_FIX`) selects the *pool*; it is not the definition — 3 of 7 strong-tier positives in 2026H1 were sampler artefacts (a backport merge, a feature withdrawal), and 2 of 214 medium-tier candidates were real. (ii) Matching key relaxed to **(stateful category, subsystem)**; the changed-lines band is recorded as a covariate, not matched (`cohort-export --match-key`, cohort-manifest/v4). (iii) **No subsystem supplies more than 8 of the 20 positives.** (iv) Negative reads and the security-adjacent exclusion are unchanged. (v) **Subsystem keying is code-first** (added 2026-09-16 after the evaluator found topotest-heavy PRs keyed `tests`): the primary subsystem is the code subsystem with the most changed lines, ignoring `tests/`, `doc/`, `tools/`, `.github/` and top-level build files; a PR touching nothing else keeps `tests`/`doc`. Applies to positives, negatives and arm H alike; recorded in the cohort manifest as `subsystem_rule`.

**A10. Arm H is a floor, not a competitor (amends §4).** Under subsystem matching, positive and negative share a subsystem and near-identical 24-month windows, so H gives both the same verdict by construction (6 of 6 provisional cases RISKY). H's precision is pinned near the base rate and its recall near 1.0. T−H is reported as "does the treatment beat knowing the subsystem", which is a floor; the pairwise criterion in §2 is carried by T−G. `history-baseline/v1` records `scored_subsystems` and `tercile3_count` per case.

**A11. Contamination control: the fix-date gate is withdrawn; the probe standard replaces it (reverses C1; amends §1, §3, §8, §9).** (i) Positives are admissible regardless of fixing-commit date. (ii) Every positive is probed before any arm runs, per `engine-runner/docs/contamination-probe-pilot-v1.md`: **Probe A** — recall from an evaluator-written symptom description, no repository mounted; **Probe B** — recognition from the real `reviewer/diff.patch`, no repository. A probe *hit* (the model names the fixing change, its mechanism-specific fix, the PR number, SHA, or CVE) **voids the case for all arms**; probe results are sealed (`contamination-probe/v1`) and consumed by the scorer like §8's post-hoc scan. Probe prompts are evaluator-authored and oracle-grade. (iii) Source windows: **FRRouting/frr merged 2024-01-01..2024-12-31 (the existing 1,667-PR dataset, re-correlated on the current build so negatives are counted) and 2026-01-01..06-30**; 2025 is collected only if the evaluator's read yields fewer than 16 admitted positives. §1's "merged in 2026" reads "merged in 2024 or 2026H1". (iv) **The ten pilot-v1 cases are excluded from the pool** — they informed the treatment prompt's lens design. (v) Budget: probes ≈ $1–2 per positive; §9's cap becomes **$750** (estimate, not bill: the credential is a subscription token, `cost_usd` is the CLI's estimate). (vi) Model cutoff (May 2026) is recorded as a covariate per positive: fix before/after cutoff.

**A12. Two rulings applying §3 as written, recorded so they bind.** (i) A fixing commit that exists only on an unmerged `refs/pull/*` head is **not corrective evidence**; the correlator currently counts such commits (~25% of fixing SHAs in the medium tier, 1 of 7 strong positives in 2026H1) and a `--require-reachable` filter is backlog. A case whose only fix is unmerged is not admitted; it is re-admitted if the fix merges before freeze. (ii) A security-adjacent negative is excluded even when a reviewer flagging it would be wrong (i.e., even when it is a *good* negative) — §3's rule stands; changing it would be a v2 decision.

### Conflicts — resolved 2026-09-15

**C1. The contamination window (§3) — resolved by amending §3.** §3 originally said the Jan–Jun 2026 collect "post-dates the reviewer model's training cutoff by construction." Verified against the live models overview (fetched 2026-09-15): `claude-opus-5`'s training data cutoff is **May 2026**, so five of the window's six months precede it. That was the wrong test anyway — a PR in training data is acceptable; a *fixing commit* in training data is not. §3 now admits a positive only if its earliest fixing commit is dated ≥ 2026-06-01, records that date per positive, and notes that negatives are not gated but are covered by §8's discussion scan. Yield becomes a count from the collect rather than an assumption.

**C2. Negative matching criteria (§3) — resolved by amending §3 to match the shipped code.** The shipped `cohort-export` (`ac05526`) matches on stateful-score category, subsystem, and changed-lines band; §3 said subsystem, size band, and merge month. Owner's decision: the document follows the code. §3 now states the shipped criteria and the exact bands. Merge month is not a matching key; the exposure guard (A1, A4) is what controls for follow-up length.
