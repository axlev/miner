# H1 fitness review — miner (coder-miner)

Date: 2026-09-15. Reviewed from the working tree at `9cfde8e` plus the uncommitted
cohort-construction change set (`docs/h1-cohort-construction-report.md`); the tree is
byte-identical to what the pending commit would contain. Read-only pass: no code was
written. Line numbers refer to that tree. Status terms are `AGENTS.md` lines 169–179.

Scope: `internal/`, `schemas/`, `docs/`, `README.md`, `AGENTS.md` of this checkout only.
Nothing under `repos/engine-runner` was read (AGENTS.md lines 125–126), and no mined
data was opened.

---

## 1. Gap table

| Requirement | Status | Evidence |
|---|---|---|
| M1 Positive-label provenance: corrective patterns enumerable, tiers and windows documented | **Present** — patterns exist and are tested; no false-attribution measurement exists | `internal/correlator/regex_patterns.go` 13–29; windows: `correlator.go` 257–263 (unbounded to `observation_end`), 397 (90-day weak); tests `correlator_test.go` 75, 251, 559 |
| M1 Lineage/reversal check available to confirm a strong pointer | **Implemented**, not consumed by cohort selection | `correlator.go` 495–560 (`enrichRelationships`); `model/retrospective.go` 28–36 (`LineageEvidence`); nothing in `internal/cohort/export.go` reads `CommitRelationships` except to require it empty for negatives (178–182) |
| M2 Negative sampler: zero signals at every tier | **Verified** | `internal/cohort/export.go` 178–182; test `export_test.go` 85–120 |
| M2 1:1 matching on category, subsystem, size band | **Verified** | `export.go` 52–56 (`Cell`), 291–301, 318–350, 374–384 (`closer`); tests 172–206, 208–244 |
| M2 First-class mode, not an inverted filter | **Verified** | separate command `internal/cli/cohort_export.go` 20–91; `export` untouched (`internal/cli/export.go` 40–52) |
| M2 Exposure floor on both classes, exposure emitted per record | **Verified** | `export.go` 166–172, 268–280 (floor applied before class is decided), 71 (`exposure_days`); test 138–170 |
| M2(iii) Detectability of a correlate run whose per-commit diffs failed | **Absent** | `correlator.go` 275–282 swallows `CommitDiff` errors; no counter, no manifest field (`internal/cli/correlate.go` 266, 283–325) |
| M3 Reason-label schema, evaluator-only, verdict distinct from sampler label | **Implemented** (schema; JSON validity checked; no runtime to verify) | `schemas/reason-label.schema.json` 5, 27–31, 62–68; `schemas/reason-label-categories.schema.json` 5, 16–21 |
| M3 Miner never populates the label | **Verified** by absence | one hit for `reason-label` under `internal/`, a doc comment (`internal/cohort/export.go` 63) |
| M3 Closed failure-class list | **Absent by design** (starts empty; built during first ten labels) | `reason-label-categories.schema.json` 5, 16–21 |
| M4 System-level (not lint-class) positive filter | **Absent** | no code; ingredients: `model/retrospective.go` 71–80 (`EvidenceScope`, `ChangedPaths`), `correlator.go` 162–210 (`ClassifyEvidenceScope`, topotest paths at 171) |
| M5 Review-time freeze of `reviewer/` | **Verified** in-repo; engine-side validation of pilot bundles is **documented**, not re-run here | diff `prospectiveexport/export.go` 598 + `gitx/diff.go` 102–112; snapshot 659; allowlist 77–85, 307, 325–332; commit rule 231–259, 421–440; `docs/export-contract.md` 562–568 |
| M6 Edited-after-merge body omitted | **Implemented** (tested) | `export.go` 200–218; test `export_test.go` 79 |
| M7 Per-case contamination probe protocol and status field | **Absent** | no schema, no field; `docs/h1-pre-registration.md` 49–59 states the requirement |
| M8 History baseline features (pre-cutoff density, churn) | **Absent**; ingredients **Present** | subsystem grouping `internal/cohort/report.go` 62–91; bounded log `gitx/repo.go` 87–100; per-commit paths `repo.go` 162–241; heuristics are static weights, not history (`configs/frr_heuristics.yaml` 13–43, `heuristics/engine.go` 64–79) |
| M9 40 verified bundles | **Absent**; pipeline **Verified** on ten (pilot) | `docs/frr-pilot-v1-cohort.md` 3–4; `internal/cohort/verify.go` 74–149 |
| Cohort manifest as one dataset identity | **Verified** | `export.go` 100–133, 424–453; tests 246–278, 280–323 |
| Pipeline record unchanged by cohort work | **Verified** | `internal/model/candidate.go` 4 (`1.0.0`); cohort wraps the record, `export.go` 64–76 |
| Pilot-v1 protocol and bundle wire contract untouched | **Verified** | `git status`: no change under `internal/prospectiveexport/`, `docs/frr-pilot-v1-cohort.md`, `schemas/prospective-manifest.schema.json`, `schemas/reviewer-metadata.schema.json` |

Doc/code mismatches found while filling the table are in §3.

---

## 2. Contradictions in `AGENTS.md`

**C1 — mirror deleted vs mirror maintained (the one the dispatch names).**

- Lines 68–69: "`engine-runner` is the single authority on admissibility, and this repo keeps
  no copy of its rules." Lines 75–80: "A mirror existed until 2026-09-10 and was deleted …
  Do not reintroduce one."
- Lines 102–104: "The boundary rules are mirrored across two repos, and nothing detects
  drift. … `validateBundle` and its constants in `internal/prospectiveexport/bundle.go`
  (`reviewerAllowedEntries`, `gitMetadataNames`, `oracleShapedSubstrings`,
  `snapshotOracleBasenames`, `snapshotOracleSubstrings`) … are a hand-maintained copy".
- Code: none of those identifiers exist. `bundle.go` defines only
  `evaluatorArtifactBasenames` (63), `buildChecksums` (128) and
  `checkEvaluatorArtifactNames` (153).
- Dated decision: line 279, 2026-09-10, "Deleted the miner's copy of `engine-runner`'s
  boundary rules". This wins.

Proposed replacement for lines 102–115 (keep the bullet, rewrite it; nothing else deleted):

> - **No boundary rule is mirrored here, and that is deliberate (decision 2026-09-10).**
>   `engine-runner/internal/boundaryvalidator` is the only definition of what the engine
>   accepts, and `internal/contextbuilder` the only definition of what reaches a stage.
>   Go forbids importing another module's `internal/` packages and no test can compare
>   two rule tables, which is why a copy was tried for one day and removed. If the engine
>   tightens, adds, or renames a rule, this repo learns of it only by running
>   `miner cohort-verify --bench-repo <engine-runner>` over real bundles; do that after
>   any change on either side. The two things in `bundle.go` that look rule-like are not
>   rules: `buildChecksums` produces an artifact the engine verifies, and
>   `evaluatorArtifactBasenames` closes a gap the engine delegates on purpose.

**C2 — verbatim duplicates.** Lines 52–55 repeat at 86–89 (fail closed); 56–57 at 90–91
(closed schemas); 58–67 at 92–101 (frozen wire contract). Byte-identical. Keep the first
occurrence of each; delete 86–101. No rule is lost.

**C3 — §9–§10 "unimplemented" vs implemented.**

- Line 14–16: "`docs/export-contract.md` — the authoritative record of … the enumerated
  contamination risks (§7) and recommended-but-unimplemented engine interface (§9–§10)."
- `docs/export-contract.md` 587: "## 9. Stable contract for `engine-runner` — Implemented
  2026-09-09"; 649: "## 10. Smallest likely miner changes — status as of 2026-09-09".
- Same file, lines 207–210: "The engine-facing contract itself is implemented and verified".
- Dated decision: line 296 (2026-09-09). Wins.

Proposed line 14–16 text: "…the enumerated contamination risks (§7) and the engine
interface as implemented on 2026-09-09 (§9–§10; §10 is a per-item status log, not a
to-do list)."

**C4 — Communication rule cites §9/§10 as target state.** Lines 184–186: "especially when
citing `docs/export-contract.md` §9/§10 or `system-design.md`, both of which describe
target/recommended states, not current behavior." Same conflict as C3. Proposed: "…when
citing `system-design.md`, which describes target architecture, or `PLAN.md`; §9/§10 of
`docs/export-contract.md` describe implemented behavior with dated status."

**C5 — role statement names the superseded target.** Lines 3–6 define the role against
"the end-to-end code-review benchmark system described in
`repos/engine-runner/docs/system-design.md`", and line 40–41 lists the owned commands
without `cohort-export`. `docs/h1-pre-registration.md` 3–5 supersedes §15 as the target.
Not a contradiction with a dated decision inside `AGENTS.md`, but the dispatch asks for an
H1 statement; proposed text is at the end of this section.

**C6 — 2026-09-09 decision superseded by 2026-09-10, both still in the log.** Line 313–319
("Split pre-publication bundle validation … lexical oracle-name heuristic … is a warning")
describes `validateBundle`, removed at line 279. A log may keep both; the reader needs a
pointer. Proposed: prefix line 313 with "*(superseded 2026-09-10, next entry but three)*".

**Proposed H1 statement**, as a new first section after the role paragraph (line 9):

> ## Current target
>
> The target this repo optimises for is H1 as pre-registered in
> `docs/h1-pre-registration.md`. It supersedes `system-design.md` §15's staged-review
> milestones for everything after 2026-09-15. Read it before planning any work; the
> pre-registration fixes the thresholds, the 40-case 1:1 cohort, and the analysis in
> advance, and a change to any of those is a dated decision in that document, not an
> edit. The miner's part is cohort construction — positives with system-level escapes,
> matched zero-signal negatives, the exposure guard, the cohort manifest, and the
> evaluator-only reason-label schema — not deterministic analysis, which H1 excludes by
> decision.

And line 40–41: "…human-facing selection reports (including `cohort-report`,
`cohort-verify`, and the H1 sampler `cohort-export`, all in `internal/cohort`)".

---

## 3. Stale narrative

| Where | Says | Actually | Live document for the fact |
|---|---|---|---|
| `docs/export-contract.md` 11 | `prospective-export` performs "pre-publication boundary validation" | removed 2026-09-10 | same file §10 item 14 (line 671); `AGENTS.md` 279 |
| `docs/export-contract.md` 3 and title | "Inspection date: 2026-09-04", "Inspection" | the file is the living contract with dated updates through 2026-09-15 | itself; `AGENTS.md` 14 names it authoritative |
| `docs/export-contract.md` 555, 558 | cites `TestValidateBundleRejectsBoundaryViolations`, `TestValidateBundleWarnsOnHighSignalSnapshotNames` | neither exists (`grep` over `internal/`); bundle tests are `bundle_test.go` 27–159 | `internal/prospectiveexport/bundle_test.go` |
| `docs/export-contract.md` 277–278 | selection axes are those "`system-design.md` §15 Milestone 4 requires" | target is H1 | `docs/h1-pre-registration.md` 3–5, 122–170 |
| `README.md` 13 | "Verbatim JSON caching of GitHub API responses" | cache is a re-marshalled composite | `docs/export-contract.md` 518 (§7 risk 10) |
| `README.md` 291 | "Two commands support picking the cases" | three since 2026-09-15 (`cohort-export`, README 349–408) | `README.md` 349–408 (introduced by the same change set; inconsistency is mine) |
| `AGENTS.md` 40–41 | `internal/cohort` = `cohort-report`/`cohort-verify` | also `cohort-export` | `docs/export-contract.md` 52–58, 95 |
| `AGENTS.md` 102–115 | mirror is hand-maintained | deleted | `AGENTS.md` 279 (see C1) |
| `AGENTS.md` 14–16, 184–186 | §9–§10 "recommended", "target" | implemented | `docs/export-contract.md` 587, 649 (see C3, C4) |
| `docs/frr-pilot-v1-cohort.md` 6 | "per `system-design.md` §15 Milestone 4" | true of the pilot when frozen; not the target for any new cohort | `docs/h1-pre-registration.md` 3–5. The pilot doc itself must not be edited (frozen, line 3) — the pointer belongs in the H1 doc or `AGENTS.md` |
| `docs/prospective-bundle-v1-release-note.md` 7–9 | "not yet Integrated end to end" | unverifiable from this repo; may be stale | `engine-runner` run results (not readable by this role) |
| `PLAN.md` 215, 346 | 180-day function window; `--require-signals` | never implemented | `internal/correlator/correlator.go` 257–263, 397; `internal/cli/export.go` 94–96; `AGENTS.md` 31–32 already says never cite `PLAN.md` |
| `AGENTS.md` 155 | "no Makefile/CI config … as of this writing" | still true (no `Makefile`, no `.github/`) | — (accurate; listed because "as of this writing" is undated) |

---

## 4. Migration plan

Ordered by validity of the H1 test first, cost second. "FP" = fingerprint-affecting
(changes which records a cohort contains or how it is identified); "CA" =
contract-affecting (changes an artifact another component ingests). Either means a new
version string, not an edit.

| # | Change | Effort | FP / CA | Unblocks |
|---|---|---|---|---|
| 1 | **Diff-summary failure accounting.** Add a failure counter and SHA list to `Correlator` at the swallow point (`correlator.go` 275–282, error path 77–80 of `getCommitDiff`), write it into the batch manifest (`internal/cli/correlate.go` 266, `batchstore`) and the single-file run's stdout; have `cohort-export` refuse or warn when the input's weak-signal record fraction is implausibly low (record it in the manifest). | 0.5 d | batch manifest: additive field (its own version, `batchstore`); cohort manifest: new field → `cohort-manifest/v2` | Trusting the CLEAN pool (M2 iii) |
| 2 | **Lineage-supported positives.** `cohort-export --positive-lineage` requiring at least one strong signal whose `commit_relationships` entry has `lineage.lines_blamed_to_original_pr > 0` or `reversal.reversal_type != "none"` (`model/retrospective.go` 28–36, 48–62). Recorded as a filter argument. | 1 d | FP: new filter → `cohort-manifest/v2` (same bump as #1) | Positives whose fix demonstrably touches the change (M1) |
| 3 | **System-level advisory flag + human read protocol (M4).** Compute, per positive, whether any strong signal's `changed_paths` include `tests/topotests/` or its `evidence_scope` is `test`/`mixed` (`correlator.go` 171, `retrospective.go` 78–79), and whether the fix subject matches a system-level vocabulary (crash, restart, upgrade, reload, scale, interop). Emit as an advisory column in `cohort-report` and a field in each cohort case; the adjudicator answers `system_level_escape` in the reason label (already in `reason-label.schema.json` 47–50). | 1 d code + human read, see §M4 | cohort case: new field → `cohort-case/v2` | Positives that match H1's definition (line 12–16 of the pre-registration) |
| 4 | **Build the 40-case cohort.** `cohort-export --positives <20 human-approved>` over the real candidates file, then `cohort-verify --prs <40> --bench-repo`. Run by whoever holds evaluator access, not `coder-miner` (AGENTS.md 116–118). | 0.5 d machine; human read in M9 | none (produces artifacts) | Every arm |
| 5 | **Contamination-probe schema (M7).** `schemas/contamination-probe.schema.json` (`contamination-probe/v1`), evaluator-only, keyed by `(repository, pr, cohort_records_sha256, model)`; the protocol as a doc section. Running it is not miner work. | 0.5 d | new evaluator-only artifact | Treatment-model admissibility; per-case probe status the pre-registration requires (line 52–53) |
| 6 | **History-baseline feature extractor (M8).** New command over the local mirror: for each case, `CommitsAfter(cutoff−W, cutoff)` (`gitx/repo.go` 87–100 already takes both bounds), `CommitDiff` for paths (162–241), aggregate per `subsystemOf` (`cohort/report.go` 62–91): commits, fix-keyword commits (`HasFixKeywords`, `regex_patterns.go` 124), lines churned. Output `history-features/v1`, evaluator-only, never in a bundle. | 2 d | new artifact; baseline B's fingerprint includes `W` and the formula | Baseline (b) |
| 7 | **Docs and `AGENTS.md` reconciliation** per §2 and §3. | 0.5 d | none | Nothing technical; prevents the next session repeating this review |
| 8 | **Label checker** (optional): `miner label-check` validating a labels JSONL against the two schemas and the cohort's `records_sha256`. | 0.5 d | none | Cheap consistency for M3; can be skipped for n=40 |
| 9 | **Description admission rate.** Read `metadata-export-audit.json` across the 40 evaluator roots to count how many bundles carry `description`. If most omit it (see §M6), decide whether arms review title+diff only or whether the admission rule changes. | 0.5 d | *Needs a decision* if the rule changes: `reviewer-metadata/v1` is frozen (`AGENTS.md` 58–67) → would be `reviewer-metadata/v2`. Stop there. | Knowing what the arms actually see |

Not in the plan by instruction: symbol-extraction measurement, rename/move following,
score normalisation, deterministic analysis, invariant library.

---

## 5. Three highest-impact changes

1. **Label validity: #1 diff-failure accounting plus #2 lineage-supported positives.**
   Without them the ground truth is noisy in both directions: a CLEAN case may be a
   RISKY case whose fix commit timed out in `CommitDiff` (`gitx/repo.go` 168, 193, 204,
   220 — four 15-second timeouts per commit, `correlator/limits.go` 16), and a RISKY case
   may rest on a `Fixes:` pointer to a commit that never touched the change. Every arm is
   scored against that truth, so a +15 delta is neither confirmable nor refutable: the
   result is "not reportable" rather than "not supported".
2. **#3 system-level filter and human read.** H1's positives are defined by *where the
   escape surfaced* (pre-registration 12–16), and reason-match is the primary comparison
   (113–115). With lint-class positives in the set, the treatment's "system-level reason"
   has nothing to match, so the primary comparison measures against the wrong target; the
   result answers a different hypothesis than the one pre-registered.
3. **#5 contamination probe.** The pre-registration says contamination in the treatment
   "passes H1 spuriously" (56–57). Without a per-case probe status, a positive treatment
   result cannot be distinguished from recall of the fix, and is worth nothing as
   evidence for H1; a negative result is still informative.

Baseline (b) (#6) is a close fourth: without it H1 as written cannot be tested at all,
only the treatment-vs-(a) half. It is ranked below the three because its absence makes
the result *incomplete*, whereas the three above make it *wrong*.

---

## 6. What must not change

- **+15 on each metric, 40 cases at 20/20, matched 1:1, PR-level exact permutation,
  precision reported conditional on the ratio** (`docs/h1-pre-registration.md` 91–121).
  Pre-registered; changing any after seeing data is the thing pre-registration exists to
  prevent.
- **Exposure floor 180 days as a policy value** (pre-registration 65–79;
  `internal/cohort/export.go` 24–29). It is the pre-registered CLEAN standard, not a
  measured window; loosening it after seeing how many negatives survive is tuning the
  label.
- **Negative rule: zero signals at every tier** (pre-registration 133–134; `export.go`
  178–182). Weak-only records are not CLEAN, whatever the pool size.
- **`stateful.score` semantics** (`heuristics/engine.go` 150–160; documented
  `docs/export-contract.md` 401). Cohort-defining via `--min-score` and the matching band;
  a change re-fingerprints every cohort.
- **No retrospective-tuned heuristics, and no tuning by anyone who read the cohort**
  (`AGENTS.md` 127–128, 258–265). Otherwise selection and treatment share information.
- **Pipeline record `1.0.0` untouched** (`internal/model/candidate.go` 4). Cohort fields
  live in the wrapper (`export.go` 64–76); adding them to the record would silently
  re-version every reader.
- **Bundle wire contract frozen** (`AGENTS.md` 58–67; `docs/export-contract.md` 587–640):
  `reviewer/` + `control/`, entry names, `reviewer-metadata/v1`, `engine-manifest/v1`,
  checksum semantics (`prospectiveexport/export.go` 24–37, 307, 354).
- **Reviewer allowlist and positive provenance gating** (`export.go` 77–85, 184–261,
  402–419; `allowedKeys` 307). A field enters `reviewer/metadata.json` only when proven
  valid at cutoff; the forbidden-value scan (346–350) is defense in depth, never the gate.
- **Snapshot = tree of `cutoff_commit`, diff = merge-base → head over the same objects**
  (`export.go` 590–601, 659; `gitx/diff.go` 102–112). Correspondence is by construction.
- **Two output roots, both atomic, neither pre-existing** (`export.go` 625–634, 704–711).
- **Opaque case IDs** (`export.go` 68–74).
- **Engine as the single admissibility authority; no rule mirror** (`AGENTS.md` 68–85,
  279–290).
- **Pilot-v1 cohort frozen** (`docs/frr-pilot-v1-cohort.md` 3). The H1 cohort is a new
  artifact; the pilot is not edited into it.
- **Cohort artifacts immutable and deterministic** (`export.go` 208–212, 456–489; tests
  `export_test.go` 208–244, 361–391). A re-run with the same inputs and arguments must
  reproduce `records_sha256`.
- **Evaluator-only status of `cohort.jsonl`, reason labels, categories, probe results and
  history features.** None may reach `reviewer/` or either coder role
  (`docs/frr-pilot-v1-cohort.md` 10–16; `reason-label.schema.json` 5).

---

## 7. What I did not verify

- Anything in `repos/engine-runner`: the validator's current rules, the contamination
  probe pilot document, the claim that opus passes the Heartbleed guard and sonnet/haiku
  do not, and that 0 of 1,667 candidates have a post-cutoff fix. All are cited from
  `docs/h1-pre-registration.md` 49–59 only.
- Any real data. Population figures (1,667 candidates, 360 zero-signal, 48.6 MB / 7,631
  files per snapshot) are from `AGENTS.md` 247–257, 266–274 and
  `docs/prospective-bundle-v1-release-note.md` 200–203, 257–260, not re-measured.
- That GitHub bumps `updated_at` on every body edit. The admission rule depends on it
  (`export.go` 200–201).
- How many pilot bundles actually carried `description` (plan item #9).
- Wall-clock per case for `cohort-verify --bench-repo`; the estimates in §M9 are
  reasoning from artifact sizes, not measurements.
- That the local mirror's `git log --all` (`gitx/repo.go` 93) does not include refs
  carrying post-cutoff commits with pre-cutoff author dates; relevant to plan item #6.
- The false-attribution rates in §M1 are judgments from the regexes and window logic,
  not measured on any dataset.

---

## Role items in detail

### M1 — positive label provenance

Patterns, all in `internal/correlator/regex_patterns.go` and applied in
`correlator.go` `CorrelatePR` (215–493):

| Tier | Signal | Pattern / rule | Window | Plausible false-attribution rate for the introducing commit |
|---|---|---|---|---|
| strong | `FIXES_SHA` | line 13; SHA matched by ≥6-hex prefix against merge commit, head, and every PR-branch commit (`correlator.go` 220–231; `matchSHAList` 33–49) | any post-merge commit ≤ `observation_end` (257–263) | Regex error ≈ 0 (must prefix-match a real target SHA). Author error — a `Fixes:` tag naming the wrong commit — is the whole risk; unmeasured, plausibly low single digits % |
| strong | `FIXES_PR` | line 17: `(fixes\|fixed-by\|closes\|resolves\|reverts) #N` | same | Low: numbers are unique across issues and PRs, so `#N` is the PR or nothing. Residual: "reverts #N" partial reverts and "fixes #N" used loosely ("follow-up to") |
| strong | `EXPLICIT_REVERT` | lines 21–22, `MatchRevert` 82–102 | same | Low; a revert is a revert. A revert-and-redo (revert for unrelated breakage, re-land next day) is a *true* revert but not a system-level escape — an M4 concern, not a false pointer |
| strong | `REGRESSION_MENTION` | line 26, `MatchRegressionMentions` 105–121; hex-word captures must prefix-match a target SHA | same | Low for SHA forms. `caused by #N` / `regression in #N` are the loosest: a message discussing PR N as context, not cause |
| medium | `SAME_FUNCTION_FIX` | any non-merge commit whose message contains a fix keyword *anywhere* (`hasFixCitation` 268–274, or `HasFixKeywords` on the subject, 349) and touches the same `(path, function)` (351–379) | **unbounded** to `observation_end`; `PLAN.md` 215's 180 days was never implemented | **High and exposure-dependent.** Hot functions accumulate unrelated "fix" commits over a 1–2-year window; for FRR's BGP core plausibly a majority of medium hits are unrelated. Not used by `cohort-export --positive-signals strong` (default); `any` admits it |
| weak | `SAME_FILE_MODIFICATION` | fix keyword + same file (396–413) | 90 days (397) | Very high as attribution; used only to *exclude* negatives, which is the conservative direction |
| weak | `LEGACY_FLAT_FUNCTION_MATCH` | function name (≥6 chars) as a substring of the message (381–393, 415–427) | unbounded | High; same use |

`summary_rank_score` (474–484) is 1.0 for any strong lead, so it carries no confidence
gradation within strong.

**Propagation of a wrong `Fixes:` pointer.** The pointer becomes a `strong_signals`
entry (`model/retrospective.go` 68–80) with `source_ref` = the citing commit;
`retrospective-export` materializes that commit's metadata and patch into the oracle
directory (`docs/export-contract.md` 141–160), and the reason label's `evidence_refs`
would cite it. Downstream: (a) the case is labelled RISKY though the change may have
been clean, inflating false negatives for every arm equally; (b) the adjudicator's
`mechanism` describes what *that* commit repaired, so reason-match for the treatment is
scored against the wrong mechanism — this hits the primary comparison directly.

**Cheapest mitigation, already computed and unused:** `commit_relationships` carries
`lineage.lines_blamed_to_original_pr` and `reversal.reversal_type` for every strong or
medium source (`correlator.go` 495–560; `retrospective.go` 28–62). A strong signal whose
relationship shows zero blamed lines and no reversal is a pointer to a commit that did
not touch what the PR wrote. Plan item #2 makes that a positive-selection filter; until
then it is a column the human read (M4) should look at.

### M2 — negative-control construction, verified

Verification against `docs/h1-pre-registration.md` 129–151:

- "zero corrective signals of any tier — strong, medium, *and* weak": `export.go`
  178–182 also requires no `commit_relationships` and `summary_rank_score == 0`. Test
  `export_test.go` 85–120 shows weak-only and medium-only records excluded. ✔
- "Matched 1:1 … on stateful-score band, subsystem, and a coarse changed-lines size band":
  `Cell` 52–56; assignment 318–350; each negative used once (`used` map 321–342). ✔
- "first-class export mode, not an inverted filter": `internal/cli/cohort_export.go`;
  `export` unchanged. ✔
- Exposure floor on both classes: the check at 268–280 precedes classification at
  291–307, so it cannot apply to one class only. Test 138–170 excludes a recent positive
  and a recent negative. ✔ (The pre-registration's wording "exclude PRs merged inside
  the longest window" does not say "negatives only"; applying it to both is the
  reading that keeps merge date from predicting the label.)

Limitations:

**(i) Greedy matching.** Acceptable for 40. The manifest reports `unmatched_positives`
(`export.go` 131, 335–338), so a suboptimal assignment is visible, not hidden; with a
zero-signal pool in the hundreds (`AGENTS.md` 247–248 names 360 zero-signal candidates
in the pilot population) and 20 positives, exhausting a cell is unlikely. Fix only if a
real run reports unmatched positives whose cells still have unused negatives, which the
`unused_negative` count would show.

**(ii) Category as the score band.** Acceptable for 40, with a check. The band matters
only insofar as `stateful.score` predicts the label, and the score is a static path/keyword
rule sum (`heuristics/engine.go` 64–147), not a defect model; nearest-score selection
inside the cell (374–384) narrows the pair gap further. The check: from `cohort.jsonl`,
the median per-pair |Δscore| — every case carries `record.stateful.score`. If it exceeds
about 0.10, add a finer band (fingerprint-affecting; `cohort-manifest/v2`). Not worth
building before seeing the number.

**(iii) Records that look zero-signal because diffs failed.** The dispatch's framing is
slightly off and the real mechanism is worse. A completely absent Git handle does *not*
produce silent zero-signal records: `correlate` always opens the repository
(`internal/cli/correlate.go` 93) and aborts if the commit log cannot be read (109–112).
What does happen silently is per-commit: `CommitDiff` runs four git subprocesses each
under a 15-second timeout (`gitx/repo.go` 168–170, 193–196, 204–207, 220–224;
`correlator/limits.go` 16), and `getSummary` discards any error and returns nil
(`correlator.go` 275–282). For that commit no medium or weak signal can be emitted;
a strong signal still can, but with `evidence_scope: unknown` and `logical_patch_id:
sha:…` (283–286). A record with no strong hit and only failed-diff commits in its window
is indistinguishable from a true negative. Concurrency (`correlate.go` 298–318, workers =
NumCPU) makes timeouts more likely under load, and nothing counts them.

Cheapest check, and where it belongs:

1. *Truth at the source (plan #1):* a counter and SHA list on `Correlator`, incremented
   in `getCommitDiff`'s error path (77–80), written to the batch manifest by
   `makeBatchManifest` (`correlate.go` 266) and printed for single-file runs. Zero
   failures is then a recorded fact of the correlate run, not an assumption.
2. *Consumption-side guard, until #1 lands:* in `cohort-export`, record in the manifest
   the fraction of input records with at least one weak signal, and refuse (or require
   `--allow-low-weak-coverage`) below a floor. Rationale: `fixKeywordRegex`
   (`regex_patterns.go` 29) matches a large share of ordinary commit subjects, so a run
   whose diffs worked yields weak `SAME_FILE_MODIFICATION` hits on a substantial fraction
   of records; a run whose diffs largely failed yields almost none. The floor is
   empirical and should be set from a run known good. This belongs in `cohort-export`
   because that is where "zero signals means clean" is consumed.

Also worth stating as a limitation of any cohort: the strong-signal `evidence_scope:
unknown` marker (283–286) is the only per-record trace of a failed diff, and only for
records that had a strong hit.

### M3 — failure-class taxonomy, verified

- Evaluator-only by construction: `reason-label.schema.json` 5 names §13 and both coder
  roles; the categories file (5) inherits the rule. Neither is referenced by any exporter;
  the only Go mention is a comment (`internal/cohort/export.go` 63). The miner cannot
  populate what it never reads. ✔
- `verdict` distinct from `sampler_label`: 27–31 states the two may disagree and that a
  disagreement is a finding about the sampler, recorded in `notes`; the cohort case
  doc comment (`export.go` 58–63) says the same from the other side. ✔
- Mechanism/category required only for RISKY and forbidden for CLEAN: 62–68. ✔
- Binding to the exact record set: `cohort_records_sha256` required (13, 21–25). ✔

**Building the list during the first ten cases.** The risk the dispatch names is real:
a list frozen after ten cases may lack a class that case 25 needs, forcing either a
misfile or an unfreeze. Two things reduce it, one is missing:

- Present: the categories document records `introduced_by_pr` and `frozen`
  (`reason-label-categories.schema.json` 16–21, 27–32) and requires labels adjudicated
  before the freeze to be re-checked (18–20). So the list can be reopened as a dated
  decision without losing the audit trail.
- Present: `diff_predictable` per category (33–37) carries the pre-registration's prior,
  which is what per-class recall is reported against (pre-registration 27–30).
- Missing, and recommended: seed the list *provisionally* from the six classes the
  pre-registration already names at 28–30 (ordering, config-interaction, error-path,
  cross-module-invariant, timing-dependent race, resource exhaustion at scale), marked
  as introduced by no case, then let the first ten confirm, split or rename them. That
  is not "designing upfront": the classes are already in the pre-registration, and the
  first-ten rule then governs refinement rather than invention.

Consequence for the result: the category does not enter the primary comparison.
Reason-match is judged on `mechanism` (free text) against the fix (`reason-label` 32–36;
pre-registration 24–26), so category inconsistency degrades only the per-class recall
breakdown, which at three or four cases per class is reported as underpowered regardless
(pre-registration 106–111). Acceptable for 40.

### M4 — system-level filter

Signals that exist:

- `labels` (`collector.go` 193–198): current labels, not merge-time labels (§7 risk 6,
  `export-contract.md` 514). Usable as a hint only; FRR labels are not defect-class labels.
- Fix-commit paths and scope: each strong/medium signal carries `changed_paths` and
  `evidence_scope` (`retrospective.go` 71–80); `ClassifyEvidenceScope` files
  `tests/topotests/…` under `test` (`correlator.go` 171). A fix that adds or changes a
  topotest is a fix whose defect was reproducible at system level — that is the strongest
  cheap proxy in the record.
- Fix-commit text: only the matched line (`raw_snippet`) is in the record; the full
  message and patch are in `retrospective-export`'s output (`export-contract.md`
  141–160), which the adjudicator reads anyway.
- Files touched by the original change: daemon paths (`configs/frr_heuristics.yaml`
  13–25) say *where*, not whether the escape was system-level.

Cheapest heuristic (plan #3), advisory, evaluator-facing: mark a positive
`system_level_candidate` when (a) any strong signal has `evidence_scope` in `{test,
mixed}` or a `changed_paths` entry under `tests/topotests/`, or (b) the strong signal's
`raw_snippet` or the fix subject (from `retrospective-export`) matches a small vocabulary
— crash, abort, assert, restart, reload, upgrade, converge, scale, interop, timeout,
stuck. Expect high precision on (a) and modest recall; (b) trades the other way.

Human step it needs: for each candidate positive, the adjudicator opens the
`retrospective-export` directory, reads the fix commit and its test change, and answers
three things in the reason label: `system_level_escape` (47–50), `mechanism`, and
`category`. A positive whose fix is lint-class, a revert-and-redo, or a doc/packaging
change (`evidence_scope` `docs`/`packaging`) is rejected and the sampler re-run with
`--positives`. Budget in §M9.

### M5 — review-time freeze of `reviewer/`

- **Diff at cutoff:** `reviewer/diff.patch` is `git diff merge-base..head`
  (`export.go` 598; `gitx/diff.go` 102–112), both objects fixed at merge; the cutoff
  cannot move them.
- **Snapshot of `cutoff_commit`:** `MaterializeTree(HeadSHA)` (659); `TestSnapshotCorrespondsToDiff`
  (`export_test.go` 441) applies the patch to the base tree and compares.
- **Metadata allowlist:** struct 77–85; `allowedKeys` 307; unknown or nested keys rejected
  325–332; pinned version 333–335; cutoff equality 339–345.
- **Commit-message admission:** included only if merged at or before cutoff, the cached
  list length equals the PR's declared count, and every commit's committer (else author)
  date is at or before cutoff (231–259, `commitTime` 267–275); re-validated 421–440.

One reviewer-visible datum worth naming, not a leak of outcome: `cutoff_timestamp`
(= `merged_at` under `cohort-verify`'s default, `verify.go` 101–104) tells the model when
the change landed. It reveals nothing about the fix, but it does let a model date the
code, which matters for the contamination probe (M7), not for the freeze.

Open risks, `docs/export-contract.md` 509–519:

| Risk | Can it leak outcome into `reviewer/`? | Reasoning |
|---|---|---|
| 6 (current cached fields as `OriginalPR`) | **Only through `title`**, and only if a rename event is missing while `IssueEventsComplete` is true; body and base branch are omitted outright when `updated_at > cutoff` (200–218). Labels never enter metadata (77–85). Otherwise evaluator-only: the stale `body`/`labels` sit in the record and feed the *heuristic score* (`engine.go` 102), i.e. cohort selection, not the reviewer | |
| 7 (exact-string forbidden values) | **No**, given the positive gate: every included field is proven valid at cutoff (402–419) before the scan runs (586). The scan catches nothing the gate did not already exclude; its weakness is a weaker second net, not a hole in the first | |
| 9 (`git log --all` unconstrained by HEAD) | **No** for `reviewer/`: it is built from two commit objects (598, 659), not from a log. Affects correlation (which refs the fix search sees) → evaluator only | |
| 10 (cache is a re-marshalled composite) | **No** outcome leak; it weakens *provenance* (the audit cannot show the exact HTTP bytes). The composite's `updated_at` is what the gate reads (200), so an inconsistent composite could omit more, never admit more | |

### M6 — PR-body leakage

Rule: `unchangedAtCutoff := !updated.IsZero() && !updated.After(cutoff)`
(`export.go` 201). `description` is included only under that condition (212–215) and
otherwise omitted with the reason "provider exposes no complete body edit history"
(217). There is no reconstruction path for the body, unlike the title (206–208), so an
edited-after-merge body is handled by omission, never by screening. `validateDecisions`
(402–419) then re-checks that every included field's `valid_at` is at or before cutoff.
Test: `TestBuildOmitsAmbiguousEditedText` (`export_test.go` 79).

Unverifiable from this repo:

- That GitHub's PR `updated_at` advances on every body edit (it is documented to, but
  the repo cannot prove it, and the cache is a composite — risk 10).
- The practical admission rate. `updated_at` also advances on comments, labels, and the
  merge itself, and the default cutoff is `merged_at`. A PR with any activity after
  merge — a "thanks", a backport label — loses its description. I would expect most
  real cases to omit it; plan item #9 measures this from the audit files and, if
  confirmed, the arms are effectively reviewing title + diff + commit messages. Whether
  that is acceptable or the rule should admit a body when the *last* post-cutoff event
  is provably not an edit is a **decision**, because it changes `reviewer-metadata/v1`.
- The evaluator-side twin of the risk: `original.body` in the record is the *current*
  body (`collector.go` 247) and the heuristic reads it (`engine.go` 102). A body edited
  after merge to say "caused a crash on warm restart" can raise `stateful.score` and thus
  the case's band. That is selection contamination, not reviewer leakage; it is why the
  matching band is category, and why nothing about cohort selection may be described as
  "review-time only".

### M7 — contamination probe protocol

Miner-side facts: nothing exists. `Provenance` (`model/provenance.go`) has no probe
field; the cohort case (`export.go` 64–76) has none; the pre-registration requires a
per-case status on each arm's model (52–53).

Proposed protocol, evaluator-only, per case × per arm model (baseline (b) has no model):

- **Probe A, symptom → recall.** Give the model repository name and a one-line symptom
  written by the adjudicator from the oracle (no PR number, no date, no diff). Ask which
  change caused it and which commit fixed it. Score: `recall` if it names the PR, fix
  commit, or the exact function and mechanism; else `none`.
- **Probe B, real diff → recognition.** Give `reviewer/diff.patch` and `title` exactly as
  the arm would see them. Ask whether the model has seen this change and what happened
  after it merged. Score: `recognition` if it describes the later fix or its symptom;
  `none` otherwise.
- **Heartbleed false-negative guard**, once per model, not per case: the Heartbleed diff
  presented as a review-time change must come back RISKY with the bounds-check reason.
  A model that returns CLEAN cannot be a treatment model (pre-registration 53–58).
- **Fabricated-defect false-positive guard**, once per model: a synthetic diff with a
  plausible planted history that never happened; the model is asked Probe B. A model that
  "recognises" the fabricated aftermath has an unmeasured fabrication rate, and its
  Probe B results are not interpretable.

Per-case status: one document per `(repository, pr, cohort_records_sha256, model)` in a
new `schemas/contamination-probe.schema.json` (`contamination-probe/v1`), fields `model`,
`probe_a`, `probe_b`, `status ∈ {clean, recall, recognition, unmeasured}`, `run_at`,
`operator`; guard results in a sibling per-model document. Lives beside the reason labels,
never in a bundle and never in the cohort JSONL (which arms may be given as case lists).

Cost, order of magnitude: 40 cases × 2 probes × 2 models (treatment, baseline (a)) = 160
calls, each roughly a diff plus a short prompt; well under an hour of wall time and tens
of dollars at current prices. Guards: four calls per new model plus the one-time authoring
of the fabricated fixture. Which models pass the Heartbleed guard is a claim of the
pre-registration (55–58: opus passes; sonnet and haiku do not; Fable and Astra untested)
that this repo cannot verify. A 2026 collect, if proposed, is for **scale** only
(pre-registration 58–59); it does not replace the probe.

### M8 — history baseline data

What the miner already computes:

- Subsystem of a change: `subsystemOf` (`cohort/report.go` 62–91), deterministic, the
  same key the sampler matches on.
- Change size: additions/deletions per PR (`report.go` 130–133).
- A bounded commit log with both ends: `CommitsAfter(ctx, t0, observationEnd)`
  (`gitx/repo.go` 87–100) emits `--since`/`--until`, so `CommitsAfter(ctx, cutoff−W,
  cutoff)` is the pre-cutoff window with no new Git code.
- Per-commit changed paths and fix vocabulary: `CommitDiff` (162–241), `HasFixKeywords`
  (`regex_patterns.go` 124–126).

What is *not* history: `internal/heuristics` is static rule weights (`engine.go` 64–147;
`frr_heuristics.yaml` 13–43) with no time dimension; the pre-registration's decision at
44–47 already says so.

New (plan #6): per case, per subsystem, over `[cutoff−W, cutoff]`: commit count,
fix-keyword commit count, lines churned; density = fix commits / commits. Baseline (b)
flags RISKY when the case's subsystem density or churn is above a cohort-wide quantile
computed *only from the same pre-cutoff features*.

Boundary so that features are pre-cutoff only:

- Time: `--until=cutoff` (`repo.go` 99) plus the same explicit re-check the correlator
  makes (`correlator.go` 261) on `commit.Date`, which is the author date (`%aI`, `repo.go`
  95; `export-contract.md` 501). Author date can precede the commit's landing; a commit
  authored before cutoff but landed after is a small leak of *history*, not of the case's
  outcome, and is acceptable to state as a limitation.
- Refs: `git log --all` (93). Acceptable for the same reason, stated.
- Data: the extractor must read only the Git mirror and the case's `merged_at`; it must
  not read `retrospective` or any label. Output is `history-features/v1`, evaluator-only,
  keyed by case and bound to `cohort_records_sha256`.

### M9 — from 10 to 40 verified bundles

Steps: `cohort-export` (seconds; pure in-memory over the candidates file) →
`cohort-verify --prs <40 from the manifest> --bench-repo …` (`verify.go` 74–149: one
`prospective-export` per case materializing the full tree, then one `go run ./cmd/bench`
per bundle, 185–188).

- **Machine.** Per case: materialize ~7,600 files / ~48 MB (`release note` 200–203,
  259–260), then the engine's `bench`, which copies the bundle into a fresh workspace and
  runs both gates. Estimate 1–2 minutes per case → 1–1.5 hours for 40. The `go run` in
  `runBench` rebuilds nothing after the first call (Go build cache).
- **Storage.** Bundles: 40 × ~48.6 MB ≈ 2 GB. The dispatch's ~75 MB/bundle figure is
  higher than the measured 48.6 MB; if it includes the engine workspace copy, plan on
  ~100 MB per case, ≈ 4 GB total. Evaluator roots are small (a record and an audit file).
- **Human, positives (M4).** To net 20 system-level positives expect to read more than
  20; the pilot's own reselection (`AGENTS.md` 247–257) shows rejection is normal. Budget
  30–40 candidate reads at 15–30 minutes each with `retrospective-export` output in hand:
  1.5–2.5 days of adjudicator time. Produces the `--positives` list and the first
  reason labels.
- **Human, negatives.** The pre-registration's negatives are rule-sampled, so the read
  is a check, not a selection: confirm nothing obvious was missed and that the change is
  not trivially CLEAN (whitespace, docs). 20 × ~15 minutes ≈ half a day. If the pilot's
  standard is applied (a negative must contain a construct that looks like a defect and
  is not one, `AGENTS.md` 249–253), that is a stronger criterion than the pre-registration
  states and would need a dated decision; it is not what `cohort-export` samples.
- **Iterations.** Each rejection after a read means a re-run of `cohort-export` with an
  updated `--positives` list, which re-matches negatives (the manifest changes;
  `records_sha256` changes). Freeze only after the last human pass; run `cohort-verify`
  once on the frozen list.

The ten pilot cases are not automatically part of the 40: the pilot protocol is frozen
(`docs/frr-pilot-v1-cohort.md` 3) and its negatives were chosen by reading, not by the
zero-signal rule. Whether pilot cases may be *re-sampled into* the H1 cohort when they
satisfy its rules is a decision for the pre-registration's owner; the sampler neither
includes nor excludes them specially.
