# H1 — pre-registration (superseded)

> **Superseded 2026-09-15 by [`h1-preregistration.md`](h1-preregistration.md),
> which is canonical.** This file is retained because it carries dated
> decisions and verified findings the canonical document does not yet include;
> each is listed in the reconciliation note at the end. Do not cite this file
> for thresholds, arms, or cohort window — cite the canonical one.

This document supersedes the staged-review question in `engine-runner`'s
`system-design.md` §15 as the target both repositories optimise for. It is a
pre-registration: thresholds, cohort design and analysis are fixed here before
the cohort is built, and a result is read against what this document says, not
against what looks good afterwards. Changes to it are dated decisions, not
edits.

## Hypothesis

On a curated set of C/C++ systems-software changes, roughly half of which
later caused a **system-level escape** — a defect that passed review and CI and
surfaced in system test, warm-restart/upgrade, scale, interop, or the field — a
pipeline of **LLM reasoning with domain-directed discovery**, given only
review-time information, classifies each change as `RISKY` or `CLEAN` with a
stated system-level reason, at precision and recall materially above both:

- **(A)** a generic single-pass LLM reviewer with no domain content, and
- **(B)** a zero-LLM history baseline that flags by subsystem defect density
  and churn as of the change's cutoff.

"Materially above" is defined in *Thresholds*. It must hold on the same frozen
cases, on both the verdict and on **reason match** — the stated mechanism
corresponds to what the later fix actually repaired.

H1 is **per-change**, not per-finding. Recall is reported **by failure class**,
because the expected outcome is heterogeneous: ordering, config-interaction,
error-path and cross-module-invariant defects may be diff-predictable;
timing-dependent races and resource exhaustion at scale probably are not.

## Decisions log

**2026-09-15 — H1 is narrowed to the LLM component.** The treatment is LLM
reasoning with domain-directed discovery, which is what engine-runner's
discovery-prompt review item already describes. Deterministic analysis
(call-graph reachability, lock/unlock pairing, init-order) and the
domain-invariant library are **dropped from H1's definition**, not scoped out
in a preamble. *Rationale:* deferred pending the H1 result; no pilot evidence
that either moves reason-match. If H1 beats both baselines we revisit whether
deterministic checks improve reason-match; if it does not, two builds were
saved.

**2026-09-15 — the miner's heuristics package is not deterministic
change-risk analysis.** `internal/heuristics` is cohort *selection* at file
and keyword granularity, and the YAML ruleset is not an invariant library. No
H1 work may treat the miner as having covered that ground.

**2026-09-15 — contamination standard is the probe, not recency.** The
recency route (post-cutoff fixes only) is structurally unavailable —
`engine-runner/docs/contamination-probe-pilot-v1.md` lines 19–33: the usable
window is 3.5 months and 0 of 1,667 candidates qualify. Every H1 case is
probed on each arm's model and carries per-case probe status. **The treatment
arm must run on a model that passes the Heartbleed false-negative guard.**
Contamination in a baseline inflates the baseline, which is conservative;
contamination in the treatment passes H1 spuriously. Opus passes the guard;
sonnet and haiku do not (their nulls are unmeasured); Fable and Astra are
untested and need the guard run before either is a treatment model. The 2026
collect, where it happens, is for **scale**, not for contamination.

**2026-09-15 — threshold is +15 points over each baseline, cohort is 40
cases, and the statistical ceiling of that size is stated rather than hidden.**
See *Thresholds*.

**2026-09-15 — the exposure floor is a policy value, not a correlator window
(miner, from the code).** *Exposure guard* above says the correlator windows are
180 days (function) and 90 days (file). The code does not have a 180-day window:
`CorrelatePR` (`internal/correlator/correlator.go`) searches every post-merge
commit up to `observation_end` for strong and medium signals, and only the weak
`SAME_FILE_MODIFICATION` check is bounded, at 90 days; the 180-day function
window is in `PLAN.md` only and was never implemented. The requirement stands
unchanged — 180 days is kept as the pre-registered minimum exposure and is the
default of `cohort-export --min-exposure-days` — but its justification is that a
short search is a weak CLEAN label, not that the search is truncated at 180
days. One consequence: a negative's evidence-free interval is its full
`exposure_days`, which is emitted per record, so the evaluator can stratify on
the actual length rather than assume 180.

**2026-09-15 — a zero-signal record is a negative only if its correlate run
recorded no swallowed diff failures.** The miner's fitness review established
(and citation verification confirmed) that the silent failure in correlation is
not a missing Git handle — that aborts — but `CommitDiff`'s four 15-second
subprocess timeouts (`internal/gitx/repo.go`, `limits.go`), which `getSummary`
(`internal/correlator/correlator.go:275-282`) swallows. Under load a record can
lose weak and medium signals with only a strong hit leaving a trace. So
"zero signals at every tier" is necessary but not sufficient for `CLEAN`: it
can be a load artifact. **No cohort may be frozen until per-record diff-failure
accounting exists** (miner migration item 1) and every negative shows zero
swallowed failures. The history baseline (b) consumes the same correlate
output and inherits the same precondition.

**2026-09-15 — "review-time information" has been narrower than assumed, and
the admission rule is now a pre-cohort decision.** On the ten pilot bundles,
**1 of 10 `reviewer/metadata.json` files carries a description and 1 of 10
carries a title**; all 10 carry commit messages. Nine of ten bundles gave every
arm exactly the diff, the snapshot, and the PR's commit messages. Cause: with
cutoff = `merged_at`, any post-merge comment or label bumps `updated_at` past
the cutoff and the description is dropped (`prospectiveexport/export.go:201,
217`); the title is dropped too unless `IssueEventsComplete` is true
(`:278-280`). Consequences: every pilot result was produced under this
condition and should be read that way; and H1's phrase "given only
review-time information" is currently defined by an admission rule nobody
chose. **The reviewer-metadata/v2 decision — whether and how to admit
pre-cutoff title and body — is made before the H1 cohort freezes, not after,
and whichever way it goes, the per-case admission outcome is recorded in the
cohort manifest so arms can be stratified on it.**

## Arms

| arm | what it is | LLM | domain content |
|---|---|---|---|
| treatment | LLM reasoning with domain-directed discovery | yes | yes |
| baseline A | generic single-pass reviewer | yes | none |
| baseline B | history: subsystem defect density and churn at cutoff | **no** | n/a |

All three arms face the **same frozen cases**. Baseline B computes only from
pre-cutoff data.

## Thresholds

**Product threshold, pre-registered: the treatment exceeds each baseline by
≥ +15 points** on precision, on recall, and on reason-match. Paired on cases;
significance by **PR-level exact permutation** — the finding-level Fisher test
was retracted for clustering on the pilot cohort and is not used.

**Statistical ceiling at 40 cases (20 positive / 20 negative), stated in
advance:**

| metric | base | +15 pts is | McNemar, fully one-directional |
|---|---|---|---|
| precision | 40 cases | 6 cases | p ≈ 0.03 — **can confirm** |
| recall | 20 positives | 3 cases | p ≈ 0.25 — **cannot** |
| reason-match | ≤ 20 positives | 3 cases | p ≈ 0.25 — **cannot** |

So at n=40, a +15 result on precision is reportable as supported. A +15 result
on recall or reason-match is reported as **"consistent with H1, underpowered at
this cohort size"** — not as supported and not as refuted. Expansion to 80
cases (40/40), at which +15 becomes 6 cases on every metric, is **decided
after** the 40-case result, not before. A result short of +15 on any metric is
"not supported at this cohort size", not "refuted".

**Primary comparison, if only one reaches threshold:** treatment vs baseline A
on reason-match. That is the claim closest to the product's value and the one
the pilot judge already measures.

**The 1:1 positive/negative ratio inflates precision relative to the real base
rate of risky changes.** Accepted, because all three arms face the same cohort
and the threshold is a delta, not an absolute. **Precision is reported as
conditional on the 1:1 ratio.**

## Cohort design

**Positives.** Changes with strong corrective evidence whose escape was
system-level, not lint-class. Selection heuristic and its human-review step are
a review deliverable (miner item M4).

**Negatives — a blocker, not an improvement.** Every current export path
selects *for* corrective evidence, so the pilot cohort is structurally RISKY
and an arm that says "risky" to everything scores perfectly. Precision and
recall are undefined without negatives.

- Sampled from PRs with **zero** corrective signals of any tier — strong,
  medium, *and* weak. Absence of strong and medium alone is not enough.
- **Matched 1:1** against the positives on stateful-score band, subsystem, and a
  coarse changed-lines size band. Unmatched negatives are as bad as none: if
  CLEAN cases are systematically smaller or elsewhere in the tree, the
  experiment measures a size classifier.
- Implemented as a **first-class export mode**, not an inverted filter.

**Exposure guard.** `CLEAN` means "no corrective evidence found by
`observation_end`". A PR merged shortly before `observation_end` has had little
time for corrective evidence to appear, so its `CLEAN` label is weak. The
correlator itself searches every post-merge commit up to `observation_end` for
strong and medium signals and bounds only the weak file-overlap check, at 90
days — see the 2026-09-15 decision above; the 180-day figure is a
pre-registered policy floor, not a search window. Two requirements, same code path as the
negative sampler: **exclude** PRs merged inside the longest window of
`observation_end`, and **record exposure days per record** so the evaluator can
stratify or drop. `ObservationEnd` currently sits in `Provenance`; per-PR
exposure is not derivable without recomputation and must be emitted.

**Oracle and reason label.** `RetrospectiveEvidence` holds raw signals — SHAs,
snippets, signal types — which is *input to* adjudication, not the adjudicated
answer. No adjudicated field exists anywhere in `internal/model/` or
`schemas/` (verified 2026-09-15). Minimum viable schema, evaluator-only:
defect mechanism as free text; one subsystem tag; one invariant-or-mechanism
category from a **closed list built while labelling the first ten cases**, not
designed upfront; adjudicator identity; date.

**Cohort manifest.** `Provenance` is per-record; nothing identifies "FRR 2026
cohort v1" as one object. The case set is an input to every arm, so the arm
fingerprint needs one stable dataset identity. Emit alongside the JSONL: record
count, sorted hash of record contents, `ConfigHash`, `MinerVersion`,
`TargetRepoHeadSHA`, `ObservationEnd`, and **the export filter arguments** —
two `--min-stateful-score` values over the same cache produce different cohorts
with identical per-record provenance.

## Build order

Cohort construction precedes the migration plan. A plan that mostly touches
`gitx`, `collector`, or the heuristics engine has misread the priority.

1. Negative-control sampler as a first-class export mode, with 1:1 matching.
2. Exposure guard, in the same pass — same code path.
3. Oracle / reason-label schema. Can run in parallel with labelling.
4. Cohort manifest, in the same export pass. Roughly twenty lines.
5. One clarification from the deferred group: whether `--require-signals
   strong,medium` is **AND** or **OR**. One line; very different cohorts.

Then the fitness review (peers author, a workflow verifies every cited
file:line), then the migration plan.

## Explicitly deferred — not built, not planned

- **Symbol-extraction measurement.** The ">95% of modified C functions" claim
  has no method behind it, and `ChangedFunctions` feeds `SAME_FUNCTION_FIX`, so
  misses suppress medium signals and inflate the CLEAN class. Real; will surface
  during labelling if severe.
- **Rename and move following** across the 180-day window. Documented as a
  known limitation.
- **Score normalisation to [0,1] and the HIGH/MEDIUM/LOW thresholds.** Both
  undefined, and normalisation is fingerprint-affecting since
  `--min-stateful-score` is cohort-defining. Document what the code does; do not
  refactor.

## What Step 0 established about the plan document

`PLAN.md` describes `PRCandidateRecord` and the collect → correlate → score
pipeline, and is correct about the record. It contains no mention of bundles,
oracle or evaluator-only material, prospective export, or freezing — all of
which exist in code (`internal/prospectiveexport`,
`internal/retrospectiveexport`, `schemas/`). The live document for output
artifacts is `docs/export-contract.md` §3 and §9, not `PLAN.md` §3–4. The
code's own oracle-manifest schema states that no dedicated oracle-export
command exists yet and that it documents current behaviour, not a target.

## Reconciliation note — 2026-09-15

`h1-preregistration.md` is canonical. This section records what this file
carries that the canonical one does not, and where the two disagree. Items
under *carried forward* are verified findings that should land in the
canonical document as dated amendments before any cohort freezes; items under
*conflicts* are decisions for the owner.

### Carried forward — verified, absent from canonical

1. **Diff-failure accounting is a precondition for admitting any negative.**
   `CommitDiff`'s four 15-second subprocess timeouts are swallowed by
   `getSummary` (`correlator.go:275-282`), so a zero-signal record can be a
   load artifact. Confirmed by citation verification. No cohort may freeze
   until per-record accounting exists and every negative shows zero swallowed
   failures. The canonical §3 negative rule ("no corrective evidence within the
   follow-up window") needs this precondition stated, and the history baseline
   H inherits it.
2. **The metadata admission rule is decided before freeze, not after.** On the
   ten pilot bundles, 1 of 10 carried a description and 1 of 10 a title
   (`export.go:201, 217, 278-280`). Every pilot arm ran under that condition.
   Canonical §3's "given only review-time information" is currently defined by
   an unchosen rule. Per-case admission outcome should be recorded in the
   cohort manifest.
3. **"Zero signals" means zero at every tier — strong, medium, *and* weak.**
   The shipped `cohort-export` enforces this; canonical §3 says only "no
   corrective evidence," which is ambiguous about weak signals.
4. **The 180-day exposure floor is a policy value, not a correlator window.**
   Strong and medium signals are searched to `observation_end`; only the weak
   file check is 90-day-bounded. `PLAN.md:215`'s 180-day window was never
   implemented. Canonical §3's follow-up-window language should not be read as
   describing the search.
5. **What n=40 can and cannot show, stated in advance.** Canonical §2 makes
   significance non-criterial, which is right, but the ceiling still bounds
   how a result is read: at 20 positives, +15 on precision (6 cases) can reach
   p ≈ 0.03 under a fully one-directional McNemar; +15 on recall or
   reason-match (3 cases) cannot. Worth carrying so a "consistent, underpowered"
   recall result is not read as a miss.

### Conflicts — owner's decision

- **Contamination standard.** This file: per-case probe, treatment model must
  pass the Heartbleed guard. Canonical §3/§8: recency via a fresh 2026 collect.
  Recency is the stronger standard and the fresh collect makes it available
  (the probe doc's "unavailable" was about the 2024 data). **Canonical
  supersedes** — with one check the canonical text defers: the requirement is
  that *fixing commits* post-date the model cutoff, not that PR merge dates do.
  A Jan–Jun 2026 PR whose fix landed before the model's cutoff is contaminated
  even though the PR is inside the window. §3 says to verify the exact model
  string's cutoff; that verification decides whether the window holds.
- **Negative matching criteria.** This file and the *shipped* `cohort-export`
  (`ac05526`): stateful-score band + subsystem + size band. Canonical §3:
  subsystem + size band + merge month. The code does not match on merge month
  and does match on stateful band. Either the code changes (`cohort-case/v2`)
  or §3 does. Not resolvable by editing a document alone.
- **Recall as a pass criterion.** This file: +15 on recall. Canonical §2 (as
  amended): recall reported, not criterial. **Canonical supersedes.**
- **Treatment model guard.** This file: any guard-passing model. Canonical §4:
  Opus. Compatible; under recency the guard is moot. **Canonical supersedes.**

Everything else in this file — the decisions log, build order, deferred list,
and the Step 0 note on `PLAN.md` — is process record and stays here.
