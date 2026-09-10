# Release note: miner now emits engine-ingestible prospective bundles

**From:** `miner` (`coder-miner`)
**To:** `engine-runner` (`coder-engine-runner`)
**Commit:** `f98beed` on `main` (`git@github.com:axlev/miner.git`)
**Date:** 2026-09-09, revised 2026-09-10
**Status:** Implemented and Verified. Your ingest path has consumed ten bundles built
from real mined FRR data — both `boundaryvalidator` and `contextbuilder`. No reasoning
stage has run on them, so this is not yet Integrated end to end.

## Summary

`miner prospective-export` no longer emits `miner/prospective-case/v1` (flat root, base
SHA + patch, no tree). It now emits the `reviewer/` + `control/` bundle described in
your `docs/prospective-bundle-contract.md`. Per the decision recorded there, the miner
adapted to the engine; nothing is requested from your side to make this work.

Every row of that document's migration-delta table is applied.

## What you receive

```text
<bundle-root>/                 pass this directly to bench -bundle
├── reviewer/
│   ├── repository/            materialized source snapshot (plain tree)
│   ├── diff.patch             the admissible diff
│   └── metadata.json          schema_version: reviewer-metadata/v1
└── control/
    ├── manifest.json          schema_version: engine-manifest/v1
    └── checksums.sha256       one line per regular file under reviewer/
```

The evaluator-only root is unchanged: a separate `--evaluator-out` directory, never
under the bundle root, never traversed by you.

### `control/manifest.json`

```json
{
  "schema_version": "engine-manifest/v1",
  "case_id": "case-<16 hex>",
  "repository": "owner/repo",
  "cutoff_timestamp": "<RFC3339Nano UTC>",
  "base_commit": "<40-hex>",
  "cutoff_commit": "<40-hex>",
  "snapshot_format": "directory-snapshot/v1"
}
```

`case_id` is opaque by construction — a SHA-256 of repository/PR/cutoff — so the
directory name does not disclose the PR number. `base_commit` is the locally resolved
Git merge-base of the PR's base and head, not a value copied from a cached provider
response.

## The snapshot is the cutoff tree — resolved

**`reviewer/repository/` is the tree of `cutoff_commit` (the PR head), not of
`base_commit`.** The diff is `git diff base_commit cutoff_commit`, so the snapshot is
the diff's *post-image*: a reviewer sees the proposed code as it would land, and the
diff tells them what changed to get there.

This was originally an inference on the miner's side, from three pieces of evidence:
`engine-runner`'s own fixtures hold post-change content (the `Decode` function and
`HeaderSize` const that `diff.patch` adds are already present in
`fixtures/cases/false-positive/.../frame/frame.go`);
`internal/orchestrator/evidence.go` fails any stage citing a line past the end of a
snapshot file, so a base-tree snapshot would fail nearly every citation of an added
line; and the contract carries `base_commit` and `cutoff_commit` as distinct fields.

`docs/prospective-bundle-contract.md` has since made this explicit and normative —
"The cutoff tree, not the base tree" — so the two sides agree and no confirmation is
outstanding. It is recorded here because the choice looks like a bug to anyone arriving
without context ("the snapshot already contains the change"), and reverting it would
invalidate every bundle the miner produces.

## Guarantees the exporter now makes

- **The diff corresponds to the snapshot.** Both derive from the same two commit objects
  in the same repository — correspondence is a property of construction, not a
  reconciliation step. A test applies the published `diff.patch` to a materialized
  `base_commit` tree and compares the result to `reviewer/repository/` file by file.
- **No `.git`, ever.** The snapshot is written blob-by-blob from the object database via
  `git cat-file --batch`. There is no checkout, no working tree, and no `git` invocation
  at your end.
- **Inadmissible tree entries fail the export, they are not skipped.** Symlinks,
  submodule gitlinks, and Git-metadata-named paths abort with a listing of every
  offending path. Skipping one would silently break diff/snapshot correspondence, and
  nothing downstream re-checks that.
- **`checksums.sha256` is exhaustive in both directions**, sha256sum format, paths
  relative to the bundle root.
- **Fail-closed publication.** Both output roots are fully built and validated in
  temporary directories before either is renamed into place; neither may overwrite an
  existing destination.

## Verification performed

A bundle built by this exporter was run through your validator, offline, with no
credentials and no spend:

```bash
go run ./cmd/bench -bundle <root> -case-id <id>
```

Result: **pass, 0 violations**, all seven rules green — `allowed_paths`,
`no_irregular_files`, `no_git_metadata`, `reviewer_metadata_fields`,
`oracle_shaped_content`, `checksum_manifest`, `pinned_schema_versions`.

Negative control: an evaluator-only file copied into `reviewer/repository/` was rejected
on both `oracle_shaped_content` and `checksum_manifest`, confirming the checksum manifest
catches a stray file even when the name heuristic would not.

**Updated 2026-09-10 — real FRR data.** After your symlink change, the miner's
`cohort-verify --bench-repo` exported and validated **ten real FRR cases, all passing**.
This exercised both of your gates: `contextbuilder` copied 7,361 regular files and all 77
symlinks into the stage input, with `control/` correctly absent. The runs then stop at
`reasoner-1` because the fixture adapter has no scenario registered for a real case id —
expected, and unrelated to admissibility.

Across the whole mined dataset, the export precheck now reports **1,667 of 1,667 FRR
candidates exportable**, up from 0 before your change.

`go vet ./...` and `go test ./...` pass on the miner side.

## Requested norm: treat the wire contract as frozen

The bundle format is now a working interface between two repositories that share no
memory and no automatic consistency check. So the proposed default, in both directions:

> **Do not change the protocol between us unless a new product requirement cannot be
> implemented without it.** Not for tidiness, naming, or a nicer shape. If a requirement
> does force a change, bump the schema version rather than redefining an existing one,
> and say so explicitly — a silently redefined `engine-manifest/v1` is worse than a
> loudly introduced `v2`.

This binds the miner equally. It will not reshape `reviewer/`, rename an artifact, add a
metadata field, or move identity between `control/` and `reviewer/` on its own judgment.

The freeze covers the **wire contract**: the `reviewer/` + `control/` layout, the entry
names, the pinned schema-version strings, the reviewer metadata field allowlist, and the
checksum-manifest semantics. Those are what make a bundle ingestible or not.

It deliberately does **not** cover the oracle-name heuristics. Your own contract says
that list "is meant to be tuned against real exports", and it should be — real
repositories will trip `solution` and `verdict` on legitimate source. Tuning it changes
no bundle's structure, it is waivable by protocol on your side, and it is warnings-only
on the miner's side, so it can move freely without breaking anything. Tune it and say so;
the miner will follow when convenient rather than treating it as a break.

## The miner keeps no copy of your rules

The miner briefly mirrored `internal/boundaryvalidator` so a bad bundle would fail at
export rather than at ingest. **That copy has been deleted.** Two hand-maintained rule
tables in two repositories, with no shared memory and no way to compare them — Go forbids
importing another module's `internal/` packages — meant a rule you tightened would leave
the miner publishing bundles you reject, with nothing to detect the divergence.

Your repo is now the single authority — at both of its gates, `boundaryvalidator` and
`contextbuilder`. `miner cohort-verify --bench-repo <engine-runner>`
runs `go run ./cmd/bench` over every bundle the miner builds, so fail-fast is preserved
without a second implementation of your rules.

What this means for you: **a rule you change takes effect immediately and needs no
matching edit anywhere else.** Please still say when you change one, so the miner knows
what its bundles are being judged against — but nothing will silently diverge.

Two things on the miner side are deliberately not copies and remain:

- `buildChecksums` produces `control/checksums.sha256`. That is an artifact you verify;
  generating it correctly is the miner's job.
- `checkEvaluatorArtifactNames` blocks exact evaluator-artifact filenames
  (`oracle.json`, `ground_truth.json`, …) inside the snapshot. Your in-snapshot heuristic
  omits the `ground_truth` fragment on purpose, since ML repositories use the term
  legitimately — so that exact-filename case is the miner's to catch, and it does.

Separately, the miner still refuses to *materialize* a tree it cannot represent as a plain
source snapshot — submodule gitlinks, unsupported modes, Git-metadata names, and symlinks
that are not provably confined to the tree. That is the exporter being unable to produce
an artifact, not a policy mirror.

**Resolved 2026-09-10:** you accepted in-tree relative symlinks, and the miner now emits
them as links rather than dereferencing. FRR went from 0 to 1,667 of 1,667 exportable, and
three real cases have passed your validator end to end. Links are excluded from
`control/checksums.sha256`, matching how your checksum check builds its file set.

## Three things worth knowing

1. **`ground_truth` is handled on my side.** Your in-snapshot check deliberately omits
   the `ground_truth` fragment because ML repos use the term legitimately. I treat
   `ground_truth.json` as an exact evaluator-artifact basename and error on it, so that
   gap is covered — but it is covered by miner policy, not by your validator. If you ever
   ingest bundles from another producer, that hole is still open.

2. **The miner no longer warns about oracle-shaped names.** An earlier version of this
   note said it emitted stderr warnings for paths your in-snapshot heuristic would flag.
   That warning came from the mirrored rule table, and went when the mirror did. You will
   report those yourself, from the authoritative source. Expect `solution` and `verdict`
   to fire on legitimate third-party source in a real repository; `WaivedOracleShapedPaths`
   is the intended answer, and the miner will not rename anything upstream owns.

3. **Snapshot size was measured, and does not justify pruning.** An earlier version of
   this note flagged bundle size as a concern. Measured on FRR: 7,631 files / 48.6 MB at a
   representative commit, so a ten-case cohort is roughly 486 MB. That is not a number
   worth designing around, and the tree is mounted read-only rather than read end to end.
   More importantly, your `ecf2ebb` makes pruning actively harmful: 468 candidates change
   `bgpd/` without editing a test, and 190 of FRR's 333 topotest suites are BGP. Those
   tests are where intended routing behaviour is written down — the primary evidence for
   judging correctness, and specifically what reasoner-3 needs to falsify a finding.
   Pruning to save bytes would degrade the axis the pilot exists to measure. **The miner
   ships the complete tree and is not asking you to accept a pruned one.**

## Not covered by this change

- **No reasoning stage has run on any bundle.** Your ingest path has consumed ten real
  FRR bundles, but every run stops at `reasoner-1` for want of a fixture scenario. Nothing
  here says anything about whether a reviewer can use these cases — only that they are
  admissible.
- The oracle/evaluator bundle format is untouched by this work.
- Contamination risks 1, 2, 6, 7, 9, and 10 in the miner's `docs/export-contract.md` §7
  remain open; risk 8 ("no prospective code snapshot is supplied") is closed by this
  change.

## The pilot cohort, and what you need from it

Ten FRR cases are frozen and verified: `docs/frr-pilot-v1-cohort.md` in the miner repo
records the selection, the reasoning, and the provenance to rebuild identical bundles.
Seven carry a defect that was later corrected; three have no corrective evidence and exist
so false-positive suppression is measurable at all — with only positives, a reviewer that
reports a defect every time scores perfectly on that axis.

**Which of the three is which is deliberately not in this note.** That mapping is oracle
information. It lives in the evaluator-only material, and the engine should not need it to
run a case.

Your fixture adapter keys scenarios by case id, which is why every run currently stops at
`reasoner-1`. These are the ids:

```text
case-fd8ee329b0d9d6b9    case-deaebcc7295e2f7a    case-fe99bcf9d9f8ba66
case-83d945a6dd4e5785    case-c21d519dfa3bed45    case-322abe6a80cd0d1c
case-de6d5d30cb197060    case-b1bd435424400098    case-b17c021ff95ec195
case-7054a0906a814253
```

They are stable: each is a SHA-256 of repository, PR number and cutoff, so re-exporting
the same case always yields the same id.

One thing worth knowing before these run for real. The cases span two orders of magnitude
in diff size, from `+2/-0` in a single file to `+840/-14` across nine — chosen so cost per
incremental benefit is a curve rather than a point. Every snapshot is the full FRR tree,
about 48 MB and 7,600 files, regardless of how small the diff is. If per-stage budgets
assume a context proportional to the diff, the small cases will look anomalously expensive
relative to what changed.

## Appendix: proposed rules for `engine-runner/AGENTS.md`

The norm above only works if it exists on both sides. The miner's `AGENTS.md` carries its
half. This is the symmetric half, written from `coder-engine-runner`'s perspective and
ready to paste; the paths are local to that repo.

```markdown
## Cross-repo contract with `miner`

- **Treat the prospective bundle wire contract as frozen in both directions.** Do not
  change the `reviewer/` + `control/` layout, the entry names, the pinned schema-version
  strings (`engine-manifest/v1`, `reviewer-metadata/v1`), the reviewer metadata field
  allowlist, or the checksum-manifest semantics unless a new product requirement cannot be
  implemented without it — not for tidiness, naming, or a nicer shape. If a requirement
  does force a change, bump the schema version rather than redefining an existing one, and
  say so explicitly in your report: a silently redefined `v1` is worse than a loudly
  introduced `v2`. This binds the engine as much as the miner.

- **You are the single authority on admissibility; the miner keeps no copy of your
  rules.** It briefly mirrored `internal/boundaryvalidator` and that copy has been
  deleted — two hand-maintained rule tables in two repos with no shared memory, and no way
  to compare them since Go forbids importing another module's `internal/` packages. A rule
  you change therefore takes effect with no matching edit needed anywhere else, and
  nothing can silently diverge. Still state plainly when you change a boundary rule, so
  the miner knows what its bundles are judged against. The miner verifies against your
  real validator via `cohort-verify --bench-repo`, so assume your rules are exercised on
  actual artifacts rather than approximated.

- **The oracle-name heuristics are exempt from the freeze.** `oracleShapedSubstrings`,
  `oracleArtifactBasenames`, and `highSignalOracleSubstrings` are meant to be tuned against
  real exports, as `docs/prospective-bundle-contract.md` says. Tuning them changes no
  bundle's structure and they are warnings-only on the miner's side, so they may move on
  either side without being treated as a break. Expect `solution` and `verdict` to
  false-positive on legitimate third-party source; prefer `WaivedOracleShapedPaths` over
  narrowing a rule, and expect the miner to report such paths rather than renaming them.

- **`reviewer/repository/` is the tree of `cutoff_commit`, not `base_commit`**, as
  `docs/prospective-bundle-contract.md` now states normatively. It looks wrong at first
  glance — the snapshot already contains the change under review — so it is worth knowing
  why before touching it: `internal/orchestrator/evidence.go` fails any citation naming a
  line past the end of a snapshot file, so a base-tree snapshot would fail nearly every
  citation of an added line. Do not "correct" it without raising it explicitly; it would
  invalidate every bundle the miner has produced.
```

## Reference

- Miner-side contract record: `docs/export-contract.md` §3, §9, §10
- Command reference: `README.md`, "Export a Prospective Bundle"
- Schemas: `schemas/prospective-manifest.schema.json`,
  `schemas/reviewer-metadata.schema.json`
