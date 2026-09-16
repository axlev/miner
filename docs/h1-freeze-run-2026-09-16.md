# H1 freeze run — 2026-09-16

What was run, what it produced, and what a later session needs to know. **No labels here.**
Which cases carry a defect is oracle-grade and lives only in the run root's cohort document
(`system-design.md` §13); this file stays label-free so it can live in the repository.

## Result

The cohort `frr-2026-h1-v1` is frozen and published: 40 cases, 20 pairs,
`records_sha256 b2279b9cab3e522c7c02cf0da9e480f6ca4090afe0d20f5a18a0d6712cd9d96f`.

| Step | Outcome |
| --- | --- |
| refresh-bundles | skipped; already refreshed under a marker |
| cohort-export | skipped; the cohort was already frozen and was not rebuilt |
| contamination-keys | 40 files, 39 complete, 224 fixing SHAs |
| cohort-verify (engine `c6a8d14`) | 40 of 40 usable |
| contamination preflight | 40 scanned, 0 failed, 0 unchecked |
| publish | 40 reviewer-only case directories |
| history-baseline | 40 files, 38 RISKY, 2 CLEAN, 0 unresolved |
| evaluator inputs | 120 files: 20 probes, 40 keys, 40 history, 20 fix patches |

Sources: 2024 (25 cases, config `bc963f2a`, observation end 2026-08-21) and 2026H1
(15 cases, config `aa785dbd`, observation end 2026-09-15). Pairs by subsystem: bgpd 8,
zebra 5, lib 3, isisd 2, ospfd 1, bfdd 1; 4 fallback pairs, 0 unmatched. Admission was
`admitted` on both title and description for all 40 cases.

## The admissibility change this run depended on

`engine-runner` c6a8d14 made two changes together, and the cohort is only publishable
because both landed. The validator now RECORDS identifier hits in `reviewer-metadata/v2`
text instead of failing them, and `contamscan -mode inputs` performs the exact check
against a case's own contamination keys.

Before that change, 8 of 40 bundles failed boundary validation on `oracle_shaped_content`
in `reviewer/metadata.json`. After it, those 8 produce 38 recorded warnings — 29
commit-like hex strings, 8 PR references, 1 vocabulary hit — and all 40 cases pass. Two
cases account for 31 of the 38: one description is a backport listing that cites every
commit it carries.

None of the 38 matched strings is one of its own case's fixing identifiers. That was
checked twice and independently: by the engine's preflight with the keys loaded, and here
against each case's `fixing_shas` and `fixing_pr_numbers`.

**The preflight is a precondition, not a step.** A clean `cohort-verify` no longer implies
a clean bundle, because the rule that used to reject a leaked fixing SHA is now a warning.
A case with no keys file is unchecked, not clean.

## Two traps worth not rediscovering

**Point the preflight at a reviewer-only tree.** Inputs mode enumerates every `case-*`
directory under `-cohort` and reports one without a keys file as unchecked, exiting
non-zero. `cohort-verify --out` also writes 40 `case-<id>-evaluator-only` siblings, so
aiming the check at that directory fails by construction. Stage the reviewer-only set,
check the staged tree, and publish by renaming it — then the published bytes are the
checked bytes rather than a second re-derived copy.

**`cohort-case/v4` carries no `case_id`.** Case identity is derived
(`prospectiveexport.GenerateCaseID`, a hash of repository, PR and cutoff) and appears only
in the artifacts built from it. Anything joining cohort records to bundles must derive it
or read it from the keys files, which carry both it and the PR.

## Gaps left open

- Probes cover the 20 positives and no negative. That is by construction — a probe asks
  whether a model already recalls a defect — and the evaluator-inputs manifest records it
  as `probe_coverage` with a flag that goes false if a positive ever lacks one.
- One case's keys are incomplete: a bare number in its text parsed as an issue reference
  that does not exist and 404s. Recorded in that file's `provenance.incomplete`; its fixing
  SHAs and PR numbers are unaffected.
- The fix patches published for the section 6 judge use the evaluator's verified
  `fixing_shas_real` for 5 cases and the keyed `fixing_shas`, restricted to
  master/stable-reachable commits, for the other 15. The keyed set is every attributed
  commit including medium-tier ones, so it is broader than "the fix"; `MANIFEST.json`
  records which source each case used.
- `docs/frr-2026-h1-v1-verify.md`-style verification output produced before `fcda503`
  shows no warnings column. Re-running verification would rebuild every bundle, so the
  published set was left as verified.
