# Release note: miner now emits engine-ingestible prospective bundles

**From:** `miner` (`coder-miner`)
**To:** `engine-runner` (`coder-engine-runner`)
**Commit:** `342918e` on `main` (`git@github.com:axlev/miner.git`)
**Date:** 2026-09-09
**Status:** Implemented and Verified. Not yet Integrated — no engine run has consumed a
bundle built from real mined data.

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

## The one semantic decision you should confirm

**`reviewer/repository/` is the tree of `cutoff_commit` (the PR head), not of
`base_commit`.** The diff is `git diff base_commit cutoff_commit`, so the snapshot is
the diff's *post-image*: a reviewer sees the proposed code as it would land, and the
diff tells them what changed to get there.

I chose this over a base-tree snapshot on three pieces of evidence in your repo:

1. Your own fixtures hold post-change content. In
   `fixtures/cases/false-positive/prospective/reviewer/repository/frame/frame.go`, the
   `Decode` function and `HeaderSize` const that `diff.patch` *adds* are already present
   in the snapshot.
2. `internal/orchestrator/evidence.go` fails a stage when a citation names a line outside
   the snapshot file. A base-tree snapshot would fail every citation of an added line —
   which is most of what reviewer-2 cites.
3. Your contract's prose says "source as of the cutoff", and its manifest carries
   `base_commit` and `cutoff_commit` as distinct fields, which only makes sense if the
   snapshot is one of them and the diff spans both.

If your intent was the base tree, this is a small change on my side, but it would
contradict (1) and (2) above, so please push back explicitly rather than assuming.

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

`go vet ./...` and `go test ./...` pass on the miner side.

## The mirror, and how it breaks

`miner`'s exporter runs its own pre-publication check (`validateBundle`,
`internal/prospectiveexport/bundle.go`) that **duplicates the rules in
`engine-runner/internal/boundaryvalidator`**. It exists so a bad bundle fails at export
time instead of downstream. It is a convenience, not an authority: the engine's
validator is the only verdict that counts, and its `boundary-validation.json` is the
artifact of record.

That duplication has no automatic consistency check, and it cannot have one. Go forbids
importing another module's `internal/` packages across a module boundary, so `miner`
(module `miner`) cannot link `engine-runner`'s validator (module
`github.com/axlev/engine-runner`) even in a test. Verification has to shell out to
`cmd/bench`.

So the failure mode is silent and one-directional: **if a rule in `boundaryvalidator`
is tightened, added, or renamed, the miner's mirror keeps passing bundles the engine will
reject.** Nothing detects it. There is no shared memory between the two coding roles —
by design, per `system-design.md` §14 — so no session is notified, and a future session
of either role starts with no recollection of this pairing existing.

The only durable channel is source control. Both repositories' `AGENTS.md` should
therefore carry the standing rule, and this note records it too:

> Changing the boundary rules on one side without the other silently breaks the pairing.
> `engine-runner/internal/boundaryvalidator` is authoritative; `miner`'s `validateBundle`
> mirrors it. A change to either must be mirrored in the same change, and a bundle must
> be re-verified against `cmd/bench` afterwards.

Concretely, when `boundaryvalidator` changes: update `validateBundle` and the constants
beside it (`reviewerAllowedEntries`, `gitMetadataNames`, `oracleShapedSubstrings`,
`snapshotOracleBasenames`, `snapshotOracleSubstrings`, the pinned schema-version
constants in `internal/prospectiveexport/export.go`), then re-run the verification in
"Verification performed" above.

## Three things worth knowing

1. **`ground_truth` is handled on my side.** Your in-snapshot check deliberately omits
   the `ground_truth` fragment because ML repos use the term legitimately. I treat
   `ground_truth.json` as an exact evaluator-artifact basename and error on it, so that
   gap is covered — but it is covered by miner policy, not by your validator. If you ever
   ingest bundles from another producer, that hole is still open.

2. **I warn where you waive.** The lexical oracle-name heuristic inside
   `reviewer/repository/` produces warnings on stderr, not export failures. That
   vocabulary belongs to the upstream repository and your protocol has
   `WaivedOracleShapedPaths` for exactly those paths; blocking there would be the miner
   overriding your policy. Expect that on real repositories, `solution` and `verdict` will
   fire on legitimate source — you will need protocol waivers, and I will report the paths
   rather than renaming anything.

3. **Snapshot size is now a real cost.** The bundle carries a full source tree rather than
   a SHA. For a large repository at a deep commit this is materially bigger than the old
   flat export. If you want the snapshot pruned to some subset, that is a contract change
   and needs to be specified on your side — I will not narrow it unilaterally, because a
   pruned tree would break citation validation for any file a reviewer legitimately
   consults.

## Not covered by this change

- No engine run has yet consumed a bundle built from **real** mined data — the
  verification above used a synthetic repository. Status is Verified, not Integrated.
- The oracle/evaluator bundle format is untouched by this work.
- Contamination risks 1, 2, 6, 7, 9, and 10 in the miner's `docs/export-contract.md` §7
  remain open; risk 8 ("no prospective code snapshot is supplied") is closed by this
  change.

## Reference

- Miner-side contract record: `docs/export-contract.md` §3, §9, §10
- Command reference: `README.md`, "Export a Prospective Bundle"
- Schemas: `schemas/prospective-manifest.schema.json`,
  `schemas/reviewer-metadata.schema.json`
