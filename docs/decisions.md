# Decisions log

Every dated decision this repository has made, newest first: what was chosen, why, and what
it rules out. `AGENTS.md` names this file as a source of truth and points here instead of
carrying the log, so it stays out of every session's starting context; read it before
changing anything it constrains, and append new entries at the top of the list below.

- 2026-09-16: The engine stopped failing on identifier SHAPES in `reviewer-metadata/v2`
  text (`engine-runner` c6a8d14) and the guarantee moved to an exact keys-based
  preflight, `contamscan -mode inputs`. The validator runs on the path a reviewer's
  bundle takes, so it has no contamination keys and can only match a shape; under v2
  the miner admits title and description only when their edit history proves them
  pre-merge, and pre-merge prose legitimately carries commit SHAs and PR numbers. Eight
  of the first forty H1 bundles were that shape, 38 warnings in all — 21 of them one
  backport description citing every commit it carries. None matched its own case's
  fixing identifiers, checked both by the preflight and independently here against
  `fixing_shas` and `fixing_pr_numbers`. Consequence for this repo: a cohort is not
  publishable on a clean `cohort-verify` alone. The preflight is a precondition, and a
  case with no keys file is unchecked rather than clean.
- 2026-09-16: The preflight is run over a staged reviewer-only copy, and publication is
  a rename of that same directory. Two reasons, both learned by hitting them. Inputs
  mode enumerates every `case-*` directory under `-cohort` and reports one with no keys
  as unchecked, so pointing it at the export directory — which holds 40
  `case-<id>-evaluator-only` siblings — fails by construction. And checking one copy
  then publishing a second re-derived copy leaves nothing proving the two agree;
  renaming the checked tree makes the published bytes the checked bytes.
- 2026-09-16: `cohort-verify` now carries the validator's recorded warnings into its
  report (`fcda503`). It parsed only `violations`, and `runBench` returned as soon as
  the process exited zero, so a passing bundle's report was never opened and every
  warning was discarded. That was cosmetic until identifier hits became warnings;
  after c6a8d14 the report was the only place those findings survived, and it was
  dropping them. A warning never changes a verdict.
- 2026-09-16: `refresh-bundles` re-fetches a whole cache bundle rather than patching
  sections. The 2024 cache was collected 2026-08-23, before issue events, body edits
  and paginated comments existed, so under `reviewer-metadata/v2` every 2024 case
  would have been `omitted-unverifiable` on both title and description — half the H1
  cohort with no reviewer text. Patching would have needed three more refreshers and
  still left `bundle_version` wrong; one re-fetch through the collect path fixes all
  of it, and the new bundle's own `fetched_at` and `bundle_version` are its
  provenance, so nothing is copied out of the root-owned pilot cache. Stated plainly
  because it is easy to mistake for a fudge: re-fetching recovers the *evidence*, not
  the text. Today's rename history reconstructs the title as it stood at any past
  cutoff, and today's body-edit history proves whether the body was edited after the
  merge — a body that was stays inadmissible, and those cases remain a visible
  stratum rather than being quietly admitted. `refresh-body-edits` is kept, not
  deleted: removing shipped surface mid-cohort is its own risk.
- 2026-09-16: Cohort sampler v4 for pre-registration A9-A12. Three changes in one
  version bump. (a) **Code-first subsystem keying**, shared by the sampler and the
  history baseline (`cohort.PrimarySubsystem`): a change's subsystem is the code group
  with the most changed *lines*, ignoring tests, docs, tooling and repository-level
  build files. The old rule counted files and keyed a one-line `ospfd` fix carrying
  twenty topotests as `tests`; the evaluator read of the first H1 cohort found it in 4
  of 10 admitted positives, where it would have matched those positives against
  test-only negatives and made arm H score the test tree. Made the rule outright
  rather than a flag, because the pilot cohort is frozen by PR number and nothing
  re-derives it. (b) **`--match-key`** selects which cell fields a negative must share
  (default unchanged); the full cell stays on every case so a dropped field remains
  visible. (c) **`--max-per-subsystem`**, positives only, refusing an explicit
  `--positives` list that exceeds it rather than trimming it silently — a quota that
  silently drops a case the evaluator chose would falsify the read.
- 2026-09-16: A cohort may draw on more than one collection window
  (`cohort-manifest/v4`). The alternative was splicing two records files into one,
  which the provenance uniformity check would have had to be relaxed to allow;
  instead the check stays exactly as strict, applied *within* a source, and the
  manifest records a `sources` entry per input with that window's own identity while
  each case names its `source`. `records_sha256` becomes the digest over the
  per-source digests in order, each still computed over that source's selected
  record hashes — so cohort identity remains a property of what was selected, not of
  the files it came from, which is the property the manifest exists to provide.
- 2026-09-16: First H1 data run (FRRouting/frr, merges 2026-01-01..2026-06-30, 1,501
  PRs, run root `/home/alex/data/FRR2026H1`). The collector hit GitHub's hourly
  limit at 1,229 PRs and *skipped* the rest instead of waiting, so the pipeline ran on
  into correlate over a partial window twice before the cause was clear; both runs
  were stopped before any batch completed and restarted cache-first after the real
  reset (read from the rate_limit endpoint, not from the error text, whose reset
  time proved stale). Fixed in the collector: a primary or secondary rate limit now
  sleeps until the provider's reset and retries the same PR once. Yield of the
  provisional cohort under the frozen arguments: 16 strong-signal PRs, 7 after the
  2026-06-01 fix-date gate, 3 matched pairs, 4 unmatched for want of a same-cell
  negative — below the pre-registered 16/16; not widened, taken to alex. Two
  findings recorded for the pre-registration: arm H cannot discriminate inside a
  subsystem-matched pair (its verdict is a subsystem property, so positive and
  negative get the same one), and the medium tier holds 244 candidates past the
  fix-date gate if the tier decision changes.
- 2026-09-15: Arm H (`history-baseline`) is built in the miner, per alex. Two
  helpers were exported rather than duplicated so H and the rest of the pipeline
  cannot drift: `cohort.SubsystemOfPaths` (the sampler's matching key) and
  `correlator.StrongCorrectiveRefs` (the strong-tier regexes without a target).
  Committer date, not author date, bounds the window: it is when a commit existed on
  the branch. Ties fall to the lower tercile via a strictly-below fraction, which
  means a many-way tie at the top can leave no subsystem in tercile 3 — accepted, and
  recorded in the rule text, because promoting ties would flag on a coin toss. The
  2026 run gets its own root (`/home/alex/data/FRR2026H1`) with a fresh mirror cloned
  from github.com rather than a fetch into the pilot's mirror, so the pilot's
  provenance stays byte-identical; the pilot's directories are root-owned and
  unwritable by this user in any case.
- 2026-09-15: Item 3b, `contamination-keys`. A separate command rather than part of
  `cohort-export`, because it needs the provider and the mirror while `cohort-export`
  is offline and deterministic; folding it in would have made `records_sha256` depend
  on a token. `fixing_shas` keeps the plain-list shape the engine's scanner reads;
  tier and matching signal live in a sibling field the scanner ignores. Medium-tier
  fixes are included, flagged, so the scan catches recall of any related fix. Issue
  threads are limited to issues referenced by `#N` in fixing commit messages and
  fixing PR text — the only complete source without the timeline API — and that is
  stated as the rule. Own-PR post-merge discussion comes from the collect-time bundle
  (the audited source) with its fetch time recorded, not from a live fetch; comments
  posted after the collect are therefore absent, which for the 2026 collect is moot.
  Every provider answer is cached under `fixes/`, separate from `prs/`, so the collect
  cache keeps its meaning and a second run is offline. A source that cannot be
  fetched is recorded per file rather than failing the run: partial keys with a
  stated gap beat no keys.
- 2026-09-15: Item 3a, collector discussion fetch (bundle `1.1.0` -> `1.2.0`).
  Scoping the contamination-keys export found that the collector stored only the
  first page of a PR's conversation comments with the fetch error discarded, and never
  populated `review_comments` at all. Both now paginate to completion with their own
  completeness flags, on the same discipline as issue events and body edits: a
  partial list is discarded, never stored as whole. Split out of the keys export and
  landed first because it is a collect-time fetch and the 2026 collect must capture
  it from the start; the keys command (3b) reads only post-cutoff material and can
  follow the collect. No refresh path for comments was added; an old cache's comment
  lists read as incomplete and stay that way until the PR is re-collected.
- 2026-09-15: reviewer-metadata/v2, export half. A second pinned version string with
  an identical wire shape; only the admission rule for `title` and `description`
  changes, from the PR-level `updated_at` (which any post-merge comment advances and
  which dropped 9 of 10 pilot descriptions) to each field's own edit history. Title
  keeps reconstruction: a post-cutoff rename is reversed, not a reason to omit, and
  the method (`current`/`reconstructed`) is recorded beside the outcome so arms can
  be stratified on it. Body cannot be reconstructed, so a post-cutoff edit omits and
  an incomplete history is `omitted-unverifiable`, never guessed. `base_branch` and
  `commit_messages` keep the v1 rules (no per-field history). v1 stays selectable and
  is the library default, so the pilot path is byte-identical; the CLI defaults to
  v2. `cohort-export --cache-dir` records the same per-field decision in
  `cohort-case/v3` / `cohort-manifest/v3` at cutoff = `merged_at`, computed by the
  function the exporter uses, so manifest and audit cannot disagree. This is a
  contract change the engine must accept (both version strings); recorded in
  `docs/export-contract.md` §5 and §9 and `schemas/reviewer-metadata.schema.json`.
- 2026-09-15: reviewer-metadata/v2, collector half. Cache bundle `1.0.0` -> `1.1.0`
  (additive): the PR body's edit history from GraphQL `userContentEdits`, with
  `body_edits_complete`, `body_edits_fetched_at` and `body_edits_error`. Chose a raw
  POST through the oauth2 client already held by the REST client over a GraphQL
  library, so no dependency was added for one query. A partial page is never stored:
  the fetch returns nothing on any failure, the flag stays false and the reason is
  recorded, and a collect is never aborted over it, because the export half's
  "omitted-unverifiable" outcome depends on the flag being honest. Title edits stay on
  the REST `renamed` issue events so each field has one history source. Added
  `refresh-body-edits` (per-PR, atomic replace, `fetched_at` preserved) because A2
  promised a per-PR re-fetch rather than a full re-collect. `body_edits_fetched_at` is
  the instant the history is complete *as of*; the exporter must read the stored
  history, not re-fetch, so the audit trail stays a fact about the cache. The
  `prospectiveexport` half is a separate item and was not touched.
- 2026-09-15: Migration item 1 (timeout accounting). The correlator now counts, per
  record, the post-merge commits with fix vocabulary whose diff it could not read, and
  stamps `provenance.correlated_by`; record schema `1.0.0` -> `1.1.0`, batch manifests
  `1.0.0` -> `1.1.0` (they record the correlating build and refuse to resume or
  finalize across builds), `cohort-case/v1` -> `v2`, `cohort-manifest/v1` -> `v2`.
  Chose counting over making a failed diff fatal: a single slow object under NumCPU
  workers would abort hours of resumable work, while refusing at selection time costs
  nothing. `cohort-export` refuses a negative whose record is uncounted or has a
  non-zero count; positives are never refused on it because strong signals do not
  depend on the diff. Added `merge-correlated` because batch correlation is keyed to
  one input file and a partial re-correlate has no way back into the full set; its
  flip report is the measured artifact rate the pre-registration will carry.
  `--min-fix-date` and `earliest_fix_date` implement the pre-registration's
  post-training-cutoff rule for positives; the miner records the date, it cannot
  verify any model's cutoff.
- 2026-09-15: Applied the `AGENTS.md` corrections from `docs/h1-fitness-review-miner.md`
  §2: the stale "boundary rules are mirrored" bullet (it named `validateBundle` and five
  constants deleted on 2026-09-10) was rewritten in favour of that decision, three
  verbatim duplicate bullets were removed, the two lines calling `docs/export-contract.md`
  §9–§10 "unimplemented"/"target" were corrected to match the contract's own dated
  headings, `cohort-export` was added to the owned commands, and a "Current target"
  section pointing at `docs/h1-pre-registration.md` was added. No boundary rule was
  removed. Citation verification then showed the rewrite had produced a second
  "not mirrored" bullet beside the existing single-authority one; the two were
  collapsed into the existing bullet.
- 2026-09-15: Built the H1 negative-control sampler as a new command, `cohort-export`
  (`internal/cohort/export.go`), rather than as flags on `export`, because every
  existing export path selects *for* corrective evidence and an inverted filter would
  still be one. Negatives require zero signals at every tier; matching is 1:1 on
  category, subsystem, and size band; the exposure floor applies to *both* classes so
  merge date cannot predict the label. The output wraps the unmodified pipeline record
  in a `cohort-case/v1` line instead of adding fields to `PRCandidateRecord`, so the
  `1.0.0` record schema and every existing reader are untouched — the cohort is a new
  artifact with its own version. Two premises in the dispatch were checked against the
  code and found wrong, and are recorded in `docs/export-contract.md` §3: there is no
  180-day correlator window (strong/medium are searched to `observation_end`; only the
  weak file check is 90-day-bounded), and `--require-signals`/`--min-stateful-score`
  do not exist (`--require-fix-signal` is OR). The 180-day floor is kept as the
  pre-registered policy value, defaulted in code, not as a code-derived window.
- 2026-09-15: The reason-label schema (`schemas/reason-label.schema.json`,
  `schemas/reason-label-categories.schema.json`) is evaluator-only by construction and
  the miner never populates it. `verdict` is deliberately distinct from the cohort's
  `sampler_label`: the sampler applies a rule over raw signals, the adjudicator reads
  the evidence, and a disagreement is a finding about the sampler to be recorded, not
  reconciled. The category list is a separate append-only document because the
  pre-registration says it is built during the first ten labels, so a schema that
  enumerated values would be designing it up front.
- 2026-09-10: Two corrections from `coder-engine-runner` over the new direct session
  channel. (a) A run stopping at `reasoner-1` is the *expected and correct* result of
  `cohort-verify`, not a shortfall: `bench` defaults to the deterministic fixture adapter,
  a real case id has no canned scenario, and the failure occurs *after* `FreshWorkspace`
  and `contextbuilder.Prepare` succeed — reaching it is what proves both gates passed.
  Never register fixture scenarios for real case ids; that manufactures fake reviews of
  real cases. Earlier wording in the release note implied the opposite and has been fixed.
  (b) `docs/frr-pilot-v1-cohort.md` had been shared with `coder-engine-runner`, which §13
  gives no oracle access. Its own warning line said only "must never reach a reviewer",
  which reads as permitting circulation to a coding agent; the label now names §13 and both
  coder roles. Distribution of that document is alex's call, not this session's.
- 2026-09-10: Reselected the cohort's three negative controls after the user asked how
  they had been picked. The honest answer was that they came from eleven arbitrarily
  sampled rows, not the full population of 360, and that a claim written into the cohort
  doc ("highest heuristic score of any zero-signal candidate") was false — it ranked 7th.
  Redone by ranking all 360 and *reading the diffs*: a negative control has to contain a
  construct that genuinely looks like a defect and is not one, which cannot be judged from
  file counts and scores. Also rejected PR 17345 despite it fixing the "all negatives are
  single-file" gap, because it rewrites a `memcmp` over authentication secrets and a
  reviewer flagging non-constant-time comparison would arguably be right — scoring that as
  a false positive would mismeasure the axis. Security-adjacent code rarely makes a safe
  negative control.
- 2026-09-10: Froze the FRR pilot-v1 cohort at ten cases (`docs/frr-pilot-v1-cohort.md`),
  all verified through both engine gates. Seven positives, three negatives. The negatives
  are structural, not filler: Milestone 4 has to measure false-positive suppression, and
  with only positives a reviewer that always reports a defect scores perfectly. Selection
  spans seven subsystems and two orders of magnitude in diff size rather than following a
  global rank, whose top is almost entirely `bgpd`. Selection read retrospective evidence,
  which is legitimate for benchmark construction but means `internal/heuristics` scoring
  must not be tuned by anyone who selected from this data.
- 2026-09-10: `engine-runner` accepted the request to admit in-tree relative symlinks
  (`checkSnapshotSymlink` in its `boundaryvalidator`), making FRR exportable: 1,667 of
  1,667 candidates, up from 0. It went further than asked on two points — the relaxation
  is the default rather than an opt-in protocol flag, and symlink chains are rejected
  outright instead of followed to a depth limit ("a rule with no traversal loop has no
  traversal bug"). `gitx.ListTree` now mirrors that boundary in *behavior*, deciding what
  the exporter may emit, and `MaterializeTree` writes links as links. Dereferencing was
  rejected: it puts identical content at two paths, so a diff touching a symlink target
  no longer reproduces the snapshot — 55 of 1,667 FRR candidates.
- 2026-09-10: Symlinks are excluded from `control/checksums.sha256`. The engine builds
  its comparison set from regular files only, so a listed link reads as "listed but
  absent" and fails the bundle. Verified against a real FRR bundle: 7,361 regular files,
  7,363 checksum lines (those plus `diff.patch` and `metadata.json`), 77 links unlisted.
- 2026-09-10: Deleted the miner's copy of `engine-runner`'s boundary rules
  (`validateBundle` and its rule tables in `internal/prospectiveexport/bundle.go`), making
  the engine's validator the single gate. The mirror had been added the previous day to
  fail fast at export; within a day it was already the thing needing a documentation rule
  to keep it honest, which is the signal that the duplication — not the drift — was the
  problem. `cohort-verify --bench-repo` runs the real validator over every bundle, so
  fail-fast is preserved without a second implementation. Kept `buildChecksums`
  (production, not validation) and `evaluatorArtifactBasenames` (a gap the engine
  deliberately delegates: its in-snapshot heuristic omits `ground_truth` because ML repos
  use the term legitimately, so nothing else catches a literal `ground_truth.json`).
  `Export` no longer returns advisory warnings, since the only source of them was the
  mirrored heuristic; the engine reports those itself.
- 2026-09-09: GitHub repo renamed `benchmark-miner` -> `miner` (by the user, via the GitHub
  UI; `gh` is not authenticated on this machine, so it could not be done from a session) and
  `origin` re-pointed to `git@github.com:axlev/miner.git`. This closes the last straggler from
  the 2026-09-06 rename, which had covered the directory, module, and binary but not the
  remote. GitHub redirects the old name indefinitely, so any older clone still works.
- 2026-09-09: `prospective-export` migrated from the flat `miner/prospective-case/v1` layout to
  the `reviewer/` + `control/` bundle defined by `repos/engine-runner/docs/prospective-bundle-contract.md`,
  which the engine can actually ingest. The substantial part is `reviewer/repository/`: a
  materialized source snapshot written from the Git object database
  (`internal/gitx/snapshot.go`), replacing "base SHA plus a patch, no tree". Chose the tree of
  `cutoff_commit` (the PR head) rather than the merge-base tree, on the evidence of the engine's
  own fixtures (`fixtures/cases/*/prospective/reviewer/repository/` hold post-change content)
  and of `internal/orchestrator/evidence.go`, which validates every review citation against a
  file in the snapshot with an in-range line span — a base-tree snapshot would fail every
  citation of an added line. The bundle contract's prose ("source as of the cutoff") and its
  distinct `base_commit`/`cutoff_commit` manifest fields agree; the summary phrase "checking out
  the base commit" in the task prompt does not, and was not followed. Flagged to the user.
- 2026-09-09: Made unmaterializable tree entries (symlinks, submodule gitlinks, Git-metadata
  names) fail the export rather than be skipped. Skipping one would leave `reviewer/diff.patch`
  describing files the snapshot does not have, and nothing downstream re-checks that
  correspondence — the engine only discovers it when a reviewer cites a line that fails
  validation, after that stage has been paid for.
- 2026-09-09 *(superseded 2026-09-10 by the "Deleted the miner's copy" entry above)*: Split pre-publication bundle validation by whether the engine can waive the rule.
  Layout, irregular files, Git metadata, pinned schema versions, and checksum agreement are
  errors. The lexical oracle-name heuristic inside `reviewer/repository/` is a warning, because
  that vocabulary belongs to the upstream repository and the engine's protocol has a waiver for
  exactly those paths; blocking there would be the miner overriding the engine's policy. Exact
  evaluator-artifact basenames stay errors even inside the snapshot, including
  `ground_truth.json`, which the engine deliberately excludes from its in-snapshot fragment list.
- 2026-09-06: Repo renamed from `benchmark-miner` to `miner` and `ARCHITECTURE_EXPORT_CONTRACT.md`
  moved to `docs/export-contract.md`, to match the target layout in `system-design.md` §4.1.
- 2026-09-06: Chose a single `AGENTS.md` (imported by a one-line `CLAUDE.md`) over a separate
  Claude Code `--agent` persona file, for simplicity and cross-provider portability (Codex reads
  `AGENTS.md` natively). This drops session-level enforcement (`permissionMode`, tool
  restrictions) in favor of one source of truth; revisit if that enforcement is ever needed.
- 2026-09-06: Go module renamed from `pr-analysis` to `miner` to match the repo directory and
  target naming. All internal import paths updated (`pr-analysis/internal/...` →
  `miner/internal/...`) across all 30 affected `.go` files, plus `go.mod`. Verified with
  `go build ./...`, `go vet ./...`, and `go test ./...` — all pass. `README.md`'s clone
  instruction (`cd pr-analysis` → `cd miner`) and `docs/export-contract.md` §2 updated to match;
  `PLAN.md` left untouched as historical design rationale, not a living reference.
- 2026-09-06: Removed the duplicate root `main.go`, keeping `cmd/miner/main.go` as the sole
  entry point. Chose `cmd/miner` over root because `README.md` documents `./cmd/miner` in all 13
  usage examples, versus a single build line in `run_frr_2024.sh` that used root — that one line
  was updated (`go build -o bin/miner .` → `go build -o bin/miner ./cmd/miner`) instead of
  rewriting the README. Verified: root `go build .` now correctly fails (no Go files), `go build
  ./cmd/miner` succeeds, full `go test ./...` still passes.
- 2026-09-06: This repo's mess (duplicate entry points, a stale/inaccurate inspection report,
  no `AGENTS.md`) traces to tool handoffs: initially built with Antigravity, then modified with
  Codex (see `.codex/`), now under Claude Code. Noted here so future inconsistencies aren't a
  surprise — check facts rather than assuming prior tooling left things consistent.
