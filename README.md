# PR Cohort Miner for Stateful & Distributed Systems

A deterministic, reproducible research tool written in Go to mine historical Pull Requests from infrastructure software repositories (such as `FRRouting/frr`, `sonic-net/sonic-sairedis`, `opencomputeproject/SAI`).

The miner identifies candidates involving **state-machine / distributed lifecycle behaviors** and correlates original PRs ($T \le T_0$) with **post-merge retrospective corrective evidence** ($T > T_0$), creating machine-readable JSONL datasets for downstream LLM analysis.

---

## Key Principles & Design

1. **Reproducibility > Cleverness**: The entire methodology is under source control with deterministic sorting and explicit provenance tracking (Miner git commit, heuristics config hash, observation cutoff).
2. **Temporal Integrity**: Strict separation between pre-merge facts (`OriginalPR`) and post-merge retrospective signals (`RetrospectiveEvidence`). Post-merge facts can never leak into pre-merge prompt structures.
3. **Cache-First Architecture**: Verbatim JSON caching of GitHub API responses and local bare Git clones. Once collected, correlation, scoring, and exporting run 100% offline in seconds with zero API rate-limit cost.
4. **Transparent Heuristics**: Pure YAML-configured scoring rules (`configs/frr_heuristics.yaml`) with inspectable match explanations.

---

## Installation & Build

Requires **Go 1.22+** and local `git`.

```bash
# Clone and build binary
git clone <this-repo>
cd miner

# Build miner binary
go build -o bin/miner.exe ./cmd/miner
```

---

## Workflow Guide

### 1. Configuration (Optional)

You can define mining parameters and heuristic rules in a YAML file (e.g. [`configs/frr_heuristics.yaml`](file:///C:/workspace/pr-analysis/configs/frr_heuristics.yaml)):

```yaml
mining:
  repository: "FRRouting/frr"
  from: 2024-01-01T00:00:00Z
  to: 2024-12-31T23:59:59Z
  observation_end: 2026-08-21T00:00:00Z
  cache_dir: "./data/cache"
  git_dir: "./data/repos/FRRouting/frr.git"

heuristics:
  path_rules:
    - name: "core_fsm_daemon"
      pattern: "(bgpd|bfdd)/.*"
      weight: 0.35
    - name: "routing_protocol_daemon"
      pattern: "(ospfd|ospf6d|isisd|pimd)/.*"
      weight: 0.25
    - name: "event_and_ipc_lib"
      pattern: "lib/(event|zclient|frrevent|hook).*"
      weight: 0.30

  keyword_rules:
    - name: "fsm_state_transition"
      pattern: "(?i)\\b(fsm|state_machine|transition|session_state|neighbor_state)\\b"
      weight: 0.30
      match_fields: ["diff_symbols", "commit_messages", "pr_body", "pr_title"]
    - name: "lifecycle_and_restart"
      pattern: "(?i)\\b(graceful_restart|warm_restart|reconnect|re-establish|shutdown|startup)\\b"
      weight: 0.25
      match_fields: ["diff_symbols", "commit_messages", "pr_title"]
```

---

### 2. Step-by-Step CLI Pipeline

#### Step 1: Collect Raw PRs
Harvests merged PRs within the date window and saves immutable JSON to disk cache:
```bash
# Using config defaults:
go run ./cmd/miner collect

# Or with explicit CLI overrides:
go run ./cmd/miner collect \
  --repo FRRouting/frr \
  --from 2024-01-01 \
  --to 2024-12-31 \
  --observation-end 2026-08-21 \
  --out ./data/raw_prs.jsonl
```

#### Step 2: Retrospective Correlation
Scans post-merge Git commit history up to `--observation-end` for `Fixes: <SHA>`, reverts, regression citations, and code-line relationships.

For research runs, use resumable batches. Each batch is an immutable JSONL file with a SHA-256 sidecar; caches are released between batches to bound memory. A matching `run.json` prevents accidental resume with a different input, cutoff, configuration, repository HEAD, or partition:

```bash
go run ./cmd/miner correlate \
  --in ./data/raw_prs.jsonl \
  --git-dir ./data/repos/FRRouting/frr.git \
  --observation-end 2026-08-21T00:00:00Z \
  --batch-dir ./data/correlation_batches_20260821 \
  --batch-size 50 \
  --workers 4
```

Rerun the same command after interruption. Verified completed batches are skipped. Batch files remain the canonical checkpoint artifacts and can be retained indefinitely.

The existing scorer currently accepts one JSONL file. Only when that convenience file is needed, validate complete coverage and create it without deleting the batches:

```bash
go run ./cmd/miner finalize-batches \
  --in ./data/raw_prs.jsonl \
  --batch-dir ./data/correlation_batches_20260821 \
  --out ./data/correlated.jsonl
```

For small inputs, single-file correlation remains available:

```bash
go run ./cmd/miner correlate \
  --in ./data/raw_prs.jsonl \
  --out ./data/correlated.jsonl
```

#### Step 3: Heuristic Scoring
Evaluates pre-merge facts against the YAML ruleset:
```bash
go run ./cmd/miner score \
  --config ./configs/frr_heuristics.yaml \
  --in ./data/correlated.jsonl \
  --out ./data/candidates.jsonl
```

#### Step 4: Inspect Cohort Statistics
Prints statistical breakdown of scores, signals, and subsystems:
```bash
go run ./cmd/miner stats --in ./data/candidates.jsonl
```

#### Step 5: Inspect Individual PR Candidates
Inspects the complete pre-merge facts, enclosing C functions, and retrospective evidence for a specific PR:
```bash
go run ./cmd/miner inspect --in ./data/candidates.jsonl --pr 14502
```

#### Step 6: Export Filtered Candidate Cohort
Filters for high-confidence candidates and outputs to your preferred format (**JSONL**, **Markdown report**, or **CSV spreadsheet**):

```bash
# 1. Export as human-readable Markdown report:
go run ./cmd/miner export \
  --in ./data/candidates.jsonl \
  --format markdown \
  --out ./output/candidate_report.md

# 2. Export as CSV (for Excel / Google Sheets):
go run ./cmd/miner export \
  --in ./data/candidates.jsonl \
  --format csv \
  --out ./output/candidates.csv

# 3. Export as filtered JSONL for downstream LLM evaluation:
go run ./cmd/miner export \
  --in ./data/candidates.jsonl \
  --format jsonl \
  --min-score 0.30 \
  --require-fix-signal \
  --out ./output/frr_2024_benchmark_candidates.jsonl
```

#### Export Retrospective Evidence for Adjudication

Materialize every retrospective signal, commit relationship, candidate commit, and
available patch for one PR. This output is retrospective-only and is not used by
prospective benchmark conditions.

```bash
go run ./cmd/miner retrospective-export \
  --input ./output/frr_2024_benchmark_candidates.jsonl \
  --pr 15624 \
  --repo ~/frr \
  --out ~/benchmarks/cases/15624/retrospective
```

The output contains `source_record.json`, normalized signal and relationship JSON,
one metadata file and canonical patch per candidate commit, logical-patch manifests,
and a top-level provenance and artifact-hash manifest. Missing Git objects are
recorded and do not abort the export.

#### Export a Prospective Bundle

Create an engine-ingestible prospective bundle in one caller-supplied root, and the
corresponding evaluator-only artifacts in a separate root:

```bash
go run ./cmd/miner prospective-export \
  --input ./rebuilt_20260821/output/frr_2024_benchmark_candidates.jsonl \
  --cache-file ./rebuilt_20260821/cache/github/FRRouting_frr/prs/pr_15624.json \
  --repo ~/frr \
  --pr 15624 \
  --cutoff 2024-04-10T05:22:26Z \
  --prospective-out ./rebuilt_20260821/prospective/<generated-case-id> \
  --evaluator-out ./rebuilt_20260821/evaluator-only/case-15624
```

`--case-id` is optional; when omitted, the miner generates an opaque identifier
(`case-<16 hex chars>`, a SHA-256 of repository/PR/cutoff) instead of a caller-supplied,
PR-derived string like `case-15624`, so the directory name itself does not disclose the
PR number.

`--prospective-out` receives the bundle root that `engine-runner` accepts as
`bench -bundle`:

```text
<prospective-out>/
  reviewer/                 everything a reasoner may ever see
    repository/             materialized source snapshot of cutoff_commit
    diff.patch              the admissible diff
    metadata.json           reviewer-visible metadata
  control/                  engine-only; never mounted to a reasoner
    manifest.json           routing and identity
    checksums.sha256        integrity over every reviewer/ file
```

`reviewer/repository/` is a plain directory tree of regular files, written straight
from the Git object database — there is no checkout, no `.git`, and no post-mount
step that could reintroduce contamination after validation. It is the tree of
`cutoff_commit`, and `reviewer/diff.patch` is `git diff <base_commit> <cutoff_commit>`
over the same two objects, so the diff describes exactly the files in the snapshot.
Any tree entry that cannot be published as a regular file — a symlink, a submodule
gitlink, or a name a consumer reads as Git metadata — fails the export rather than
being skipped, because skipping it would silently break that correspondence.

`control/manifest.json` conforms to `schemas/prospective-manifest.schema.json` and
pins `engine-manifest/v1`: an opaque `case_id`, `repository`, `cutoff_timestamp`, the
locally resolved `base_commit` (Git merge-base of the PR's base and head, not merely
the cached provider value), `cutoff_commit`, and `snapshot_format`.
`control/checksums.sha256` is one `sha256sum`-format line per regular file under
`reviewer/`, with paths relative to the bundle root; it must describe exactly that
set of files, so a stray file is caught even when its name looks innocent.

`reviewer/metadata.json` conforms to `schemas/reviewer-metadata.schema.json`: the
same closed reviewer allowlist as before plus a pinned
`"schema_version": "reviewer-metadata/v1"`, and never `base_commit`/`cutoff_commit`.

Before publishing, the miner re-derives the consumer's admissibility rules against the
built bundle. Layout, irregular files, Git metadata, pinned schema versions, and
checksum agreement fail the export. The lexical oracle-name heuristic applied inside
the source snapshot only warns on stderr: that vocabulary belongs to the upstream
repository, and the engine's protocol has a waiver for exactly those paths, so they are
reported for a human to judge rather than renamed or dropped.

`--evaluator-out` receives `correlated-report.json` (the complete selected
correlated record) and `metadata-export-audit.json` (per-field provenance for every
metadata.json decision). It is never under the bundle root and the engine never
traverses it.

The cache bundle's PR number, repository, and base/head SHAs must match the
selected correlated record's; a mismatch fails the export closed rather than
silently combining identities from two different inputs. Neither output root may
already exist, and both roots are fully built and validated before either is
published, so a failure never leaves a partial or mixed case behind.

To check a bundle against the engine before handing it over, from the `engine-runner`
checkout:

```bash
go run ./cmd/bench -bundle <prospective-out> -case-id <case-id>
```

Boundary validation is deterministic, offline, and needs no credentials; a rejected
bundle runs no stages and writes `boundary-validation.json` naming every rule that
tripped and the path that tripped it.

#### Choose and Verify a Benchmark Cohort

Two commands support picking the cases a benchmark run will use. Both are
**evaluator-facing**: they show retrospective signal strength and ranking, which is
legitimate for choosing cases but must never be given to a reviewer or wired into an
engine-visible path.

**Stage 1 — pick.** Summarize every scored candidate:

```bash
go run ./cmd/miner cohort-report \
  --input ./output/frr_2024_benchmark_candidates.jsonl \
  --repo ~/frr \
  --out ./output/cohort-report.md
```

Rows carry heuristic score, retrospective signal tier, change size, subsystem, snapshot
size, and — with `--repo` — whether the case looks exportable. `--format csv` writes the
same rows for a spreadsheet.

Output is grouped by subsystem rather than globally ranked, and that is deliberate: a
cohort taken from the top of a ranking tends to be one bug class in one subsystem, which
cannot distinguish review discovery from substantiation from false-positive suppression.
Pick *across* groups. A generic container directory (`internal/`, `src/`, `pkg/`,
`cmd/`) is stepped through when grouping, so a Go repository does not collapse into one
heading.

The exportability column is read-only and writes nothing — it resolves the merge-base and
walks the head tree. It is a **prediction, not a proof**.

**Stage 2 — prove.** Nothing should enter a cohort on a prediction:

```bash
go run ./cmd/miner cohort-verify \
  --input ./output/frr_2024_benchmark_candidates.jsonl \
  --repo ~/frr \
  --cache-dir ./rebuilt_20260821/cache \
  --prs 15624,15701,15733 \
  --out ./output/cohort-bundles \
  --bench-repo ~/repos/engine-runner
```

This runs the real prospective export for each shortlisted PR and then submits each
resulting bundle to `engine-runner`'s own boundary validator, reporting a per-case
pass/fail table. Each case is pinned to its own `merged_at` unless `--cutoff` overrides
every case at once; per-PR provider bundles are resolved beneath `--cache-dir` using the
`collect` layout.

`--bench-repo` is optional. Without it no validation runs, every case is reported as
`not run` rather than as passing, and the exact `bench` command for each bundle is
printed for you to run by hand. The engine is a separate Go module whose validator lives
under `internal/`, which Go forbids importing across module boundaries — so validation
must shell out, and this repo does not assume a sibling checkout exists.

A case that exports cleanly but fails boundary validation is reported as **FAIL**, and
the command exits non-zero if any case is not cohort-ready. Snapshot paths that merely
trip the engine's oracle-name heuristic are reported as warnings, not failures: that
vocabulary belongs to the upstream repository and needs a protocol waiver on the engine
side, not a rename here.

---

## Data Schema Reference

Every record in the output JSONL file adheres to the following typed schema.
`provenance.miner_version` is resolved at build time from Go's embedded VCS metadata
(the actual Git revision the binary was built from, suffixed `-dirty` for an
uncommitted tree, or `unknown` if no VCS revision was embedded — e.g. outside a Git
checkout, a shallow clone, or `-buildvcs=false`) rather than a fixed string; the
value below is illustrative only. See also `schemas/` for the
prospective-bundle control manifest, reviewer-metadata, and oracle (retrospective)
manifest contracts.

```json
{
  "schema_version": "1.0.0",
  "original": {
    "repository": "FRRouting/frr",
    "number": 14502,
    "title": "bgpd: fix fsm transition on neighbor reset",
    "body": "Handles graceful restart timer reset during session tear down.",
    "author": "dev-user",
    "created_at": "2024-06-14T10:00:00Z",
    "merged_at": "2024-06-15T12:00:00Z",
    "base_sha": "abcdef1234567890",
    "head_sha": "123456abcdef7890",
    "merge_commit_sha": "deadbeefcafebabe111122223333444455556666",
    "labels": ["bgp", "bugfix"],
    "changed_files": [
      { "path": "bgpd/bgp_fsm.c", "status": "modified", "additions": 15, "deletions": 4 }
    ],
    "changed_functions": ["bgp_fsm_change_status", "bgp_stop"]
  },
  "stateful": {
    "score": 0.85,
    "category": "HIGH",
    "matched_path_rules": [
      { "rule_name": "core_fsm_daemon", "weight": 0.35, "location": "path", "matched": "bgpd/bgp_fsm.c" }
    ],
    "matched_keyword_rules": [
      { "rule_name": "fsm_state_transition", "weight": 0.30, "location": "title", "matched": "fsm" }
    ],
    "matched_symbols": ["bgp_fsm_change_status"],
    "explanation": "path:core_fsm_daemon (+0.35), title:fsm_state_transition (+0.30)"
  },
  "retrospective": {
    "summary_rank_score": 1.0,
    "strong_signals": [
      {
        "signal_type": "FIXES_SHA",
        "source_type": "COMMIT",
        "source_ref": "fadecafe99998888777766665555444433332222",
        "timestamp": "2024-07-01T10:00:00Z",
        "raw_snippet": "Fixes: deadbeefcafebabe (\"bgpd: fix fsm transition on neighbor reset\")",
        "confidence": 1.0
      }
    ],
    "medium_signals": [],
    "weak_signals": []
  },
  "provenance": {
    "miner_version": "v1.0.0",
    "config_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "harvested_at": "2026-08-21T12:00:00Z",
    "observation_end": "2026-08-21T00:00:00Z",
    "target_repo_head_sha": "9999888877776666555544443333222211110000",
    "github_api_version": "2022-11-28"
  }
}
```

---

## Running Tests

```bash
# Run all unit tests with full coverage
go test -v ./...
```
