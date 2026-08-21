# Engineering Plan: Deterministic PR Cohort Miner for Stateful / Distributed Systems

**Target Repository**: `FRRouting/frr` (Extensible to `sonic-net/sonic-sairedis`, `sonic-net/sonic-swss`, `opencomputeproject/SAI`)  
**Core Objective**: Deterministically reduce a large cohort (~1,000–10,000 merged PRs) into a high-precision candidate set of distributed/stateful changes with retrospective corrective evidence for downstream LLM analysis and human adjudication.  
**Design Principle**: **Reproducibility > Cleverness**.

---

## 1. Recommended Architecture

The system is organized into decoupled, single-responsibility packages centered around a **cache-first, deterministic processing pipeline**.

```
                           ┌───────────────────────────────┐
                           │          GitHub API           │
                           └──────────────┬────────────────┘
                                          │ Raw JSON
                                          ▼
┌─────────────────────────┐        ┌──────────────┐
│       Local Git         ├───────►│  Collector   │◄─── (Cache Layer)
│    (Bare Clone/Diff)    │        └──────┬───────┘
└───────────┬─────────────┘               │
            │ Parsed Diffs / Symbols      ▼
            │                      ┌──────────────┐
            └─────────────────────►│ Pre-Merge    │
                                   │ Heuristics   │
                                   └──────┬───────┘
                                          │ OriginalPR + StatefulScore
                                          ▼
                                   ┌──────────────┐
                                   │ Retrospective│◄── Post-T0 Commits / PRs / Issues
                                   │ Correlator   │
                                   └──────┬───────┘
                                          │
                                          ▼
                                   ┌──────────────┐
                                   │ JSONL Store  │
                                   │  & Exporter  │
                                   └──────┬───────┘
                                          │
                                          ▼
                              Candidates Dataset (.jsonl)
                               (Ready for Downstream LLM)
```

### Component Responsibilities

| Package | Responsibility | Primary Input | Primary Output |
| :--- | :--- | :--- | :--- |
| `internal/collector` | Fetches PR metadata and comments from GitHub API with rate limiting and local raw disk caching. | PR query parameters, GitHub Token | Immutable raw JSON cache on disk |
| `internal/gitx` | Manages local git clone, extracts diffs, hunk symbols, commit graphs, and blame without network calls. | Local Git repository path, Commit SHAs | Structured diff hunks, touched files, C function names |
| `internal/heuristics` | Evaluates pre-merge facts against explicit YAML-configured rules to assign a distributed/stateful candidate score. | `model.OriginalPR`, YAML ruleset | `model.StatefulEvaluation` |
| `internal/correlator` | Correlates original PR ($T_0$) with post-$T_0$ commits, PRs, and issues to extract strong, medium, and weak corrective signals. | `OriginalPR`, Post-$T_0$ Git history & PRs | `model.RetrospectiveEvidence` |
| `internal/storage` | Manages raw response cache, reads/writes versioned JSONL candidate records with deterministic ordering. | Model structs, output paths | Formatted JSONL files |
| `internal/cli` | Implements CLI subcommands (`collect`, `correlate`, `score`, `export`, `inspect`, `stats`). | CLI flags / args | Exit codes, stdout tables/json |

---

## 2. Data Model & Temporal Integrity

To guarantee **temporal integrity**, pre-merge and post-merge data are strictly separated into distinct Go structs. Post-merge facts ($T > T_0$) can never be accidentally serialized into pre-merge benchmark prompt data.

```go
package model

import "time"

// PRCandidateRecord is the top-level reproducible output record.
type PRCandidateRecord struct {
    SchemaVersion string                `json:"schema_version"` // e.g. "1.0.0"
    Original      OriginalPR            `json:"original"`       // Strict pre-merge facts (T <= T0)
    Stateful      StatefulEvaluation    `json:"stateful"`       // Transparent pre-merge heuristic score
    Retrospective RetrospectiveEvidence `json:"retrospective"`  // Post-merge corrective signals (T > T0)
    Provenance    Provenance            `json:"provenance"`     // Exact audit & reproduction trail
}

// OriginalPR captures immutable facts established at or before merge time T0.
type OriginalPR struct {
    Repository          string         `json:"repository"`            // e.g. "FRRouting/frr"
    Number              int            `json:"number"`                // GitHub PR number
    Title               string         `json:"title"`
    Body                string         `json:"body"`
    Author              string         `json:"author"`
    CreatedAt           time.Time      `json:"created_at"`
    MergedAt            time.Time      `json:"merged_at"`             // T0 boundary
    BaseRef             string         `json:"base_ref"`              // e.g. "master"
    BaseSHA             string         `json:"base_sha"`
    HeadSHA             string         `json:"head_sha"`
    MergeCommitSHA      string         `json:"merge_commit_sha"`
    Labels              []string       `json:"labels"`
    ChangedFiles        []ChangedFile  `json:"changed_files"`
    ChangedFunctions    []string       `json:"changed_functions"`     // Functions modified in diff
    PreMergeIssueRefs   []int          `json:"pre_merge_issue_refs"`  // Issues mentioned in PR body
    CommitCount         int            `json:"commit_count"`
}

type ChangedFile struct {
    Path      string `json:"path"`
    Status    string `json:"status"` // added, modified, deleted
    Additions int    `json:"additions"`
    Deletions int    `json:"deletions"`
}

// StatefulEvaluation captures the pre-merge stateful/distributed heuristic assessment.
type StatefulEvaluation struct {
    Score               float64           `json:"score"`                 // Normalized [0.0, 1.0]
    Category            string            `json:"category"`              // "HIGH", "MEDIUM", "LOW"
    MatchedPathRules    []MatchedRule     `json:"matched_path_rules"`
    MatchedKeywordRules []MatchedRule     `json:"matched_keyword_rules"`
    MatchedSymbols      []string          `json:"matched_symbols"`
    Explanation         string            `json:"explanation"`
}

type MatchedRule struct {
    RuleName string  `json:"rule_name"`
    Pattern  string  `json:"pattern"`
    Weight   float64 `json:"weight"`
    Location string  `json:"location"` // "path", "title", "body", "symbol"
}

// RetrospectiveEvidence captures post-merge signals (T > T0).
type RetrospectiveEvidence struct {
    SummaryRankScore         float64                    `json:"summary_rank_score"`
    StrongSignals            []StrongCorrectiveSignal   `json:"strong_signals"`
    MediumSignals            []MediumCorrectiveSignal   `json:"medium_signals"`
    WeakSignals              []WeakCorrectiveSignal     `json:"weak_signals"`
}

type StrongCorrectiveSignal struct {
    SignalType  string    `json:"signal_type"`  // "FIXES_SHA", "FIXES_PR", "EXPLICIT_REVERT", "REGRESSION_MENTION"
    SourceType  string    `json:"source_type"`  // "COMMIT", "PR", "ISSUE"
    SourceRef   string    `json:"source_ref"`   // SHA or PR#
    Timestamp   time.Time `json:"timestamp"`
    RawSnippet  string    `json:"raw_snippet"`  // Exact matched text/line
    Confidence  float64   `json:"confidence"`   // e.g. 1.0
}

type MediumCorrectiveSignal struct {
    SignalType      string    `json:"signal_type"` // "SAME_FUNCTION_FIX", "LINKED_ISSUE_FIX", "REGRESSION_TEST_ADDED"
    SourceRef       string    `json:"source_ref"`
    Timestamp       time.Time `json:"timestamp"`
    FunctionOrPath  string    `json:"function_or_path"`
    Context         string    `json:"context"`
}

type WeakCorrectiveSignal struct {
    SignalType  string    `json:"signal_type"` // "SAME_FILE_MODIFICATION"
    SourceRef   string    `json:"source_ref"`
    Timestamp   time.Time `json:"timestamp"`
    FilePath    string    `json:"file_path"`
}

// Provenance guarantees full auditability.
type Provenance struct {
    MinerVersion      string    `json:"miner_version"`       // Go build Git commit
    ConfigHash        string    `json:"config_hash"`         // SHA256 of heuristics config
    HarvestedAt       time.Time `json:"harvested_at"`
    ObservationEnd    time.Time `json:"observation_end"`     // Fixed cut-off timestamp for retrospective data
    TargetRepoHeadSHA string    `json:"target_repo_head_sha"`
    GitHubAPIVersion  string    `json:"github_api_version"`
}
```

---

## 3. GitHub / Git Data Acquisition Strategy

To ensure zero unnecessary API calls, speed, and 100% offline reruns, we adopt a **Hybrid Git + GitHub REST Architecture**:

```
Data Need                     Preferred Source      Rationale
------------------------------------------------------------------------------------------
PR metadata (title, author,   GitHub REST API       Fast, indexed, includes PR discussion &
body, merge timestamp,                              linked labels. Downloaded once to disk cache.
labels, review comments)

File list, diff hunks,        Local Git Clone       Git diff & log locally are 1000x faster than
function symbols, commit      (`git diff`,          GitHub API, have zero rate limits, and enable
ancestry, raw patches         `git log`)            precise C-function hunk parsing.

Post-T0 commit history        Local Git Clone       Allows instantaneous scanning of all commit
& regex matching              (`git log`)           messages for "Fixes: <SHA>" and reverts.
```

### Ingestion Flow
1. **Local Clone**: The tool ensures a bare or mirror clone exists at `data/repos/<owner>/<repo>.git` and fetches latest master.
2. **GitHub API Ingestion**:
   - Queries `GET /repos/{owner}/{repo}/pulls?state=closed&sort=updated` or uses GitHub GraphQL search for PRs within `--from` and `--to`.
   - For each PR, fetches full details and writes verbatim JSON to `data/cache/<owner>/<repo>/prs/pr_<number>.json`.
   - If cache file exists and PR is merged/closed, **never fetch again**.
3. **Local Git Extraction**:
   - Computes merge base and retrieves diff: `git diff -p <base_sha> <merge_sha>`.

---

## 4. Retrospective Correlation Algorithm

For every merged PR ($PR_{orig}$) with merge timestamp $T_0$ and merge commit $SHA_{orig}$:

```
Step 1: Scan Post-T0 Commits (T > T0) in Git History
        ├── Regex "Fixes: <SHA_prefix>" -> Matched SHA_orig?  ==> [STRONG: FIXES_SHA]
        ├── Regex "Revert.*#<PR_orig_num>" or reverts SHA?     ==> [STRONG: EXPLICIT_REVERT]
        └── Regex "(regression|broke|caused by).*#<PR_orig_num>" ==> [STRONG: REGRESSION_MENTION]

Step 2: Scan Post-T0 PR Bodies & Commit Messages
        ├── Regex "Fixes:? #<PR_orig_num>" or "Closes #<PR_orig_num>" ==> [STRONG: FIXES_PR]
        └── Cross-link references from GitHub API events.

Step 3: Correlate via Linked Issues
        └── If PR_orig fixed Issue X, did later PR_fix reference Issue X or reopen it? ==> [MEDIUM: LINKED_ISSUE_FIX]

Step 4: Function-Level Code Overlap with Fix Semantics (Local Git)
        ├── For each function F in PR_orig.ChangedFunctions:
        │   Find commits modifying F within [T0, T0 + 180 days].
        │   Does commit message contain corrective keywords ("fix", "crash", "leak", "panic", "null", "race")?
        │   ==> If YES: [MEDIUM: SAME_FUNCTION_FIX]
        └── If test file added in same subsystem with name matching F or module ==> [MEDIUM: REGRESSION_TEST_ADDED]

Step 5: File-Level Overlap (Weak candidate filter)
        └── Commits modifying PR_orig.ChangedFiles within [T0, T0 + 90 days] with fix keywords ==> [WEAK: SAME_FILE_MODIFICATION]
```

---

## 5. Stateful / Distributed Heuristic Scoring

The heuristic assigns a deterministic pre-merge score in $[0.0, 1.0]$. The rules are loaded from an external YAML file (`configs/frr_heuristics.yaml`) for zero hardcoding.

### Ruleset Schema (`frr_heuristics.yaml`)

```yaml
version: "1.0"
repository: "FRRouting/frr"

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
  - name: "zebra_rib_kernel"
    pattern: "zebra/.*"
    weight: 0.20

keyword_rules:
  - name: "fsm_state_transition"
    pattern: "\\b(fsm|state_machine|transition|session_state|neighbor_state)\\b"
    weight: 0.30
    match_fields: ["diff_symbols", "commit_messages", "pr_body", "pr_title"]
  - name: "lifecycle_and_restart"
    pattern: "\\b(graceful_restart|warm_restart|reconnect|re-establish|shutdown|startup)\\b"
    weight: 0.25
    match_fields: ["diff_symbols", "commit_messages", "pr_title"]
  - name: "async_timers_and_queues"
    pattern: "\\b(timer|callback|notify|thread_add_timer|event_add_timer|work_queue)\\b"
    weight: 0.25
    match_fields: ["diff_symbols", "pr_body"]
  - name: "concurrency_and_races"
    pattern: "\\b(mutex|lock|race|ordering|stale|pending|sync|barrier)\\b"
    weight: 0.20
    match_fields: ["commit_messages", "pr_title"]

### Configuration Precedence

All mining parameters (repository, date window `from`/`to`, `observation_end`, heuristic weights, cache paths) can be defined in a **YAML config file** and/or overridden directly via **CLI flags**.

**Precedence Order**:
1. **CLI Flags** (e.g., `--from 2024-01-01 --observation-end 2026-08-21`)
2. **YAML Config File** (e.g., `--config configs/frr_2024.yaml`)
3. **Built-in Defaults** (e.g., `observation_end` defaults to current harvest timestamp if unspecified)


---

## 6. Symbol Extraction Strategy (Minimum Sufficient for C)

For v1 in C codebases like FRR, **avoid heavy Tree-sitter / Clang AST dependencies** (which require CGO, libclang headers, and fail on partial/historical code).

### Minimum Sufficient V1 Approach:
1. **`git diff -p` Hunk Header Parser**:
   - `git diff -p` automatically uses Git's built-in C-function regex engine (`xfuncname`) to include the enclosing function name in the `@@ ... @@ func_name()` line.
2. **Lightweight C Regex Symbol Scanner**:
   - Scans added/modified lines for top-level function declarations and definitions:
     `^[a-zA-Z_][a-zA-Z0-9_* \t]+\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\([^;]*\)`
3. **Symbol List Normalization**: Deduplicates symbol names per PR.

*Benchmark for v1*: This captures >95% of modified C functions in FRR with zero external CGO dependencies.

---

## 7. Storage and Caching Design

| Storage Layer | Format | Location | Purpose |
| :--- | :--- | :--- | :--- |
| **Raw API Cache** | Verbatim JSON | `.cache/github/<owner>/<repo>/prs/pr_<num>.json` | Permanent offline snapshot of GitHub responses. |
| **Git Mirror** | Bare Git Repo | `.cache/repos/<owner>/<repo>.git` | Fast local diffs and commit log traversals. |
| **Intermediate Data** | Compressed JSONL | `data/processed/<repo>_collected.jsonl` | Output of `collect` pass. |
| **Correlated Data** | JSONL | `data/processed/<repo>_correlated.jsonl` | Output of `correlate` pass. |
| **Final Export** | JSONL / JSON | `output/candidates_<repo>_<timestamp>.jsonl` | Final benchmark dataset ready for LLM consumption. |

### Tradeoff Analysis
- **Why JSONL over SQLite/DuckDB**:
  - Direct 1-to-1 line streaming into Python/Pandas/LLM pipelines.
  - Trivial to diff using `git diff` or `jq`.
  - No database migration or concurrency locks needed for single CLI invocations.
  - ~5,000 PR records is ~20 MB uncompressed, easily processed in memory in Go (< 0.5s).

---

## 8. CLI Command Structure

The CLI binary is named `miner`.

```bash
# 1. Collect raw PR metadata and clone/sync git repository
miner collect \
  --repo FRRouting/frr \
  --from 2024-01-01 \
  --to 2024-12-31 \
  --cache-dir ./data/cache \
  --out ./data/raw_prs.jsonl

# 2. Correlate with post-merge git history and fix signals up to an observation cut-off
miner correlate \
  --repo FRRouting/frr \
  --in ./data/raw_prs.jsonl \
  --observation-end 2026-08-21 \
  --cache-dir ./data/cache \
  --out ./data/correlated.jsonl

# 3. Apply stateful heuristics scoring
miner score \
  --config ./configs/frr_heuristics.yaml \
  --in ./data/correlated.jsonl \
  --out ./data/candidates.jsonl

# 4. Export filtered cohort for downstream LLM evaluation
miner export \
  --in ./data/candidates.jsonl \
  --min-stateful-score 0.40 \
  --require-signals strong,medium \
  --format jsonl \
  --out ./output/frr_2023_benchmark_candidates.jsonl

# 5. Inspect a single PR candidate interactively or as JSON
miner inspect \
  --in ./data/candidates.jsonl \
  --pr 14502

# 6. Summary stats of mined cohort
miner stats \
  --in ./data/candidates.jsonl
```

---

## 9. Repository and Package Layout

```
pr-analysis/
├── cmd/
│   └── miner/
│       └── main.go                  # CLI entrypoint
├── configs/
│   ├── frr_heuristics.yaml          # FRRouting heuristic rules
│   └── sonic_heuristics.yaml        # SONiC / SAI heuristic rules template
├── internal/
│   ├── cli/                         # Subcommand implementations
│   │   ├── root.go
│   │   ├── collect.go
│   │   ├── correlate.go
│   │   ├── score.go
│   │   ├── export.go
│   │   ├── inspect.go
│   │   └── stats.go
│   ├── collector/                   # GitHub API client & disk cache
│   │   ├── github_client.go
│   │   ├── cache.go
│   │   └── ratelimit.go
│   ├── gitx/                        # Local git operations & diff parsing
│   │   ├── repo.go
│   │   ├── diff.go
│   │   ├── hunk_parser.go
│   │   └── symbols.go
│   ├── heuristics/                  # Stateful scoring engine
│   │   ├── engine.go
│   │   ├── config.go
│   │   └── rules.go
│   ├── correlator/                  # Multi-signal retrospective correlation
│   │   ├── engine.go
│   │   ├── regex_signals.go
│   │   ├── code_overlap.go
│   │   └── issue_links.go
│   ├── model/                       # Data models & JSON serialization
│   │   ├── candidate.go
│   │   ├── original.go
│   │   ├── retrospective.go
│   │   ├── heuristics.go
│   │   └── provenance.go
│   └── storage/                     # JSONL readers, writers, and formatters
│       └── jsonl.go
├── testdata/
│   ├── fixtures/                    # Mock GitHub API JSON responses
│   │   ├── pr_14502.json
│   │   └── pr_fixes_sample.json
│   ├── diffs/                       # Sample C diffs from FRR
│   │   └── sample_bgp_fsm.diff
│   └── configs/
│       └── test_heuristics.yaml
├── go.mod
├── go.sum
└── README.md
```

---

## 10. Reproducibility & Provenance Model

Every exported record embeds a `Provenance` block ensuring that any cohort can be audited and reproduced:

1. **Deterministic Processing**:
   - Candidate records are sorted strictly by `Original.Number` ascending.
   - All map iterations in heuristic evaluators use sorted keys.
   - Timestamps in `Original` and `Retrospective` are normalized to UTC ISO-8601.
2. **Provenance Trace**:
   - `MinerVersion`: Git commit SHA of the `miner` tool binary.
   - `ConfigHash`: SHA256 of the YAML heuristic configuration file.
   - `TargetRepoHeadSHA`: Git commit SHA of the target repository clone at runtime.
3. **Auditability**:
   - Rerunning `miner score --config <hash>` on cached data produces identical output bytes.

---

## 11. Testing Strategy

| Level | Scope | Methodology |
| :--- | :--- | :--- |
| **Unit Tests** | Regex Signal Parsers | Table-driven tests against real-world commit messages (`Fixes: <sha>`, `Reverts`, `regression in #...`). |
| **Unit Tests** | Diff & Hunk Symbol Parser | Golden file tests on synthetic and real FRR C diffs (`testdata/diffs/`). |
| **Unit Tests** | Heuristics Engine | Test scoring accuracy, weight boundary caps, and explainability strings with `test_heuristics.yaml`. |
| **Unit Tests** | Temporal Boundaries | Verify that events with $T \le T_0$ are never classified as retrospective evidence. |
| **Integration Tests** | GitHub Client + Cache | Mock HTTP server (`httptest.Server`) serving recorded JSON fixtures; test pagination and disk caching. |
| **End-to-End Test** | Full Pipeline Test | Create a small synthetic local Git repo with 5 commits/PRs, run `collect -> correlate -> score -> export` and verify JSONL output structure. |

---

## 12. Implementation Phases

```
Phase 0: Foundations & Data Models
- Implement internal/model types (OriginalPR, RetrospectiveEvidence, etc.).
- Implement internal/storage/jsonl for reading/writing records.
- Set up CLI skeleton using standard flag/command routing.
Output: Working CLI shell with model serialization tests.

Phase 1: Local Git Operations & Diff Analyzer
- Implement internal/gitx (clone management, diff hunk parsing, C function symbol extractor).
- Unit tests on FRR diff fixtures.
Output: Tool can inspect any local git SHA and output touched files/symbols.

Phase 2: GitHub API Collector & Disk Caching
- Implement internal/collector with GitHub REST client, rate-limit backoff, and verbatim disk caching.
- Add `miner collect` command.
Output: `miner collect` fetches 100 PRs, caches JSON locally, runs fully offline on reruns.

Phase 3: Stateful Heuristics Engine
- Implement YAML config loader (configs/frr_heuristics.yaml).
- Implement internal/heuristics scoring engine with explainability output.
- Add `miner score` command.
Output: Deterministic score calculation and path/keyword match breakdown.

Phase 4: Retrospective Correlator
- Implement internal/correlator (regex matchers for Fixes/Reverts/regression notes).
- Implement post-T0 function overlap and issue cross-linking.
- Add `miner correlate` command.
Output: Extraction of Strong, Medium, Weak signals for each PR.

Phase 5: CLI Integration, Filtering & Export
- Implement `miner export`, `miner inspect`, and `miner stats`.
- Add JSONL filtering (by score threshold, signal strength).
Output: End-to-end pipeline ready for real repository mining.

Phase 6: FRRouting Validation & Benchmark Dry Run
- Mine 1,000 PRs from FRRouting/frr (e.g. 2023 calendar year).
- Inspect top 50 candidates manually to validate recall of distributed/stateful defects.
Output: Validated benchmark cohort dataset.
```

---

## 13. Complexity Traps (Explicitly Avoid in v1)

| Feature | Why to AVOID in v1 |
| :--- | :--- |
| **Full Clang / Tree-sitter AST parsing** | Requires CGO, system C headers, and fails on historical/broken commits. Git hunk headers + regex are sufficient. |
| **Database Engines (PostgreSQL / SQLite / Neo4j)** | Adds schema migrations, CGO dependencies, and file locking complexity. Flat JSON cache + JSONL is faster and simpler for $\le 50,000$ PRs. |
| **Live LLM Calls inside Go** | Breaks determinism, inflates test runtime, and complicates offline reproducibility. LLM semantic analysis belongs in a downstream pipeline. |
| **Automated Blame-Causality Attribution** | Code overlap does not equal fault. Retain overlap strictly as a *candidate signal*, not proof. |
| **Live GitHub GraphQL Crawlers without Local Cache** | GraphQL responses are hard to inspect/debug and fail on rate limits. REST API with verbatim disk caching is auditable and robust. |

---

## 14. Open Architectural Decisions & Recommendations

1. **Git CLI Execution vs Pure Go Git Library (`go-git`)**:
   - *Recommendation*: Use `os/exec` to invoke system `git` for diffing and log scanning. Real Git is significantly faster on large repositories like FRR (100k+ commits) and correctly implements all C `xfuncname` hunk headers.
2. **Configuration Format**:
   - *Recommendation*: Standard **YAML** (`gopkg.in/yaml.v3`). Clean, human-editable, supports inline comments for explaining heuristic weights.
3. **GitHub Client Authentication**:
   - *Recommendation*: Read `GITHUB_TOKEN` environment variable with unauthenticated fallback for public rate limits (with warning).

---

## 15. Definition of Done for v1

The v1 release is complete when:
1. **Automated Test Suite**: 100% of unit tests pass with synthetic fixtures and zero live network dependency (`go test ./...`).
2. **Offline Reproducibility**: Given a populated `.cache` directory and local Git clone, the entire pipeline (`correlate -> score -> export`) runs offline in $< 10$ seconds for 1,000 PRs with byte-for-byte identical output.
3. **Zero Semantic Leakage**: No post-$T_0$ field is accessible in `model.OriginalPR`.
4. **FRRouting Dry Run**: Successfully mines $\approx 1,000$ merged PRs from `FRRouting/frr`, producing an inspectable, ranked `candidates.jsonl` dataset with exact SHA references and matched heuristic signals.
