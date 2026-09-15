# reviewer-metadata/v2, export half — status report

Date: 2026-09-15. Session: `miner-coder`. Work order: the peer dispatch for the
`prospectiveexport` half of v2, with the two readings and the method addition agreed in
session. Status terms are `AGENTS.md` "Status vocabulary".

## Status per point

| # | Point | Status | Where |
|---|---|---|---|
| 1 | Per-field admission for `title` and `description`: admitted iff the field's own last edit is at or before the cutoff, from its own history and own completeness flag; `updated_at` never consulted | **Verified** | `internal/prospectiveexport/v2.go` (`AdmitTitle`, `AdmitDescription`, `BuildV2`); tests `v2_test.go` (`TestV2TitleOutcomes`, `TestV2DescriptionOutcomes`, `TestBuildV2IgnoresUpdatedAt`) |
| 2 | Three outcomes per field (`admitted` / `omitted-post-cutoff-edit` / `omitted-unverifiable`), plus method (`current` / `reconstructed`), recorded in the evaluator audit and the cohort manifest | **Verified** | audit: `Audit.MetadataVersion`, `Audit.Admission`, `FieldDecision.Outcome` (`export.go`); cohort: `cohort-export --cache-dir` → `Case.Admission`, `Manifest.Admission` (`internal/cohort/export.go`, `annotateAdmission`); tests `v2_export_test.go`, `internal/cohort/admission_test.go` |
| 3 | `reviewer-metadata/v2` contract; contract doc updated; v1 selectable so pilot bundles stay reproducible | **Verified** | `MetadataSchemaVersionV2`; `--metadata-version` on `prospective-export` and `cohort-verify` (CLI default v2, library default v1); `schemas/reviewer-metadata.schema.json` (enum of both), `docs/export-contract.md` §5, §9; test `TestExportMetadataVersionSelectsTheContract` (default and v1 unchanged, v2 selected, unknown refused before writing) |
| 4 | Tests: four combinations per field; old bundle yields `omitted-unverifiable` for body and the existing title logic for title | **Verified** | `TestV2DescriptionOutcomes` (never-edited, edited-before, edited-at, edited-after, incomplete, old bundle, deleted record, undated, created-after); `TestV2TitleOutcomes` (never-renamed, renamed-before, renamed-after → reconstructed, two renames, incomplete, broken chain, undated rename, created-after); `TestBuildV2OldBundleOmitsBodyAsUnverifiable` |
| — | `internal/heuristics`, `internal/gitx`, labels/base-ref history | untouched, out of scope | `git status` |

"Verified": `go vet ./...` and `go test ./...` pass on every package in this session.
Not Shipped until the commit is confirmed. Not Integrated: the engine's validator has
not yet been changed to accept the v2 string, and no real bundle has been exported
under v2.

## Version impact

| Artifact | Before | After | Why |
|---|---|---|---|
| `reviewer/metadata.json` | `reviewer-metadata/v1` | `v1` or `v2`, selected | **contract-affecting**: a second pinned version string; identical field list and types; the engine must accept both |
| Evaluator audit (`metadata-export-audit.json`) | — | `metadata_version`, `admission`, per-field `outcome` | additive, evaluator-only |
| Cohort case / manifest | `cohort-case/v2`, `cohort-manifest/v2` | `v3` / `v3` | optional `admission` per case and counts per manifest, present only with `--cache-dir` |

Not touched: `engine-manifest/v1`, `control/`, the bundle layout, checksums, the
pipeline record, the cache bundle (1.1.0 from the collector half).

## The contract diff, for the engine

`reviewer/metadata.json`:

- `schema_version` may now be `"reviewer-metadata/v2"` as well as `"reviewer-metadata/v1"`.
- No other change: same closed key set (`schema_version`, `repository`,
  `cutoff_timestamp`, `title`, `description`, `base_branch`, `commit_messages`), same
  types, same omission semantics (an inadmissible field is absent, never null).
- What v2 means: `title` and `description` were admitted from each field's own edit
  history (title: complete REST rename events, reversed to the cutoff title when
  renamed later; description: complete body edit history with last edit at or before
  the cutoff), never from the PR-level `updated_at`. `base_branch` and
  `commit_messages` follow the v1 rules.

Nothing under `control/` changes.

## Limitations to state

- **The pilot's title omissions are still a hypothesis.** Under v2 a title is
  unverifiable when the bundle's issue-event history is incomplete. If the pilot cache
  predates the issue-event fetch, `refresh-body-edits` does not fix titles (it fetches
  body edits only); those bundles need a re-collect of the PR, or a title-events
  refresh that does not exist yet. Read `fields.title.reason` in the pilot audits before
  deciding whether one is needed.
- **A reconstructed title is the title at cutoff, not the title the reviewer's PR
  page showed at review time** if the rename happened between review and merge. That
  is the pre-registration's own definition ("as of the cutoff") and is recorded as
  `method: reconstructed` so it can be stratified.
- **Body admission is all-or-nothing.** A body edited after the cutoff is omitted
  entirely even if the edit was a typo fix; the edit `diff` field is stored but not
  used to reconstruct. Reconstruction from diffs is possible in principle and is not
  attempted, by decision (unreliable, and the field is optional in the API).
- **`admission` in the cohort manifest is computed at `merged_at`**, the default cutoff
  of `cohort-verify`. If a cohort is exported with an explicit `--cutoff`, the audit is
  the authority for that export and may differ from the manifest.
- **Engine acceptance is pending.** Until `boundaryvalidator` accepts the v2 string,
  `cohort-verify --bench-repo` will fail every v2 bundle at `pinned_schema_versions`.
  Use `--metadata-version v1` for any verification run before the engine change lands.
