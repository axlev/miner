# FRR pilot-v1 cohort

**Status:** frozen 2026-09-10. All ten cases exported and passed `engine-runner`'s
boundary validation.

This is the case list for the first cohort, per `system-design.md` §15 Milestone 4
("Run 5-10 cases without per-case prompt tuning"). It records what was selected, why,
and enough provenance to rebuild the identical bundles.

**Evaluator-facing — and that is narrower than "not for reviewers".** This document names
which cases carry a defect and which do not, and describes each defect. That is oracle
content. Under `system-design.md` §13 the roles permitted to hold it are the miner runtime
and the evaluator; `coder-miner` and `coder-engine-runner` both have "Oracle O: No", so it
should not be circulated to either coding agent, not only kept from reviewers. An earlier
version of this line said merely "must never reach a reviewer", which reads as permitting
exactly that circulation.

## Reproducing

Case IDs are a SHA-256 of repository, PR number, and cutoff, so the same inputs always
produce the same identifiers. Re-running with the provenance below yields byte-identical
case IDs; the bundles themselves are reproducible up to Git object content.

```bash
go run ./cmd/miner cohort-verify \
  --input /home/alex/data/FRR20260821/data/candidates.jsonl \
  --repo /home/alex/data/FRR20260821/data/repos/FRRouting/frr.git \
  --cache-dir /home/alex/data/FRR20260821/cache \
  --prs 16738,16141,16467,17134,16194,15632,15624,17298,16155,15208 \
  --out output/frr-pilot-cohort \
  --bench-repo /home/alex/repos/engine-runner
```

Each case is pinned to its own `merged_at`; no `--cutoff` override is used. Bundles land
in a gitignored working directory and are not committed — they are ~75 MB each and fully
derived from the inputs below.

### Provenance

| Input | Value |
|---|---|
| miner commit (exporter code) | `40fd728d24056006dd98163068cd66fbd8dd7185` |
| engine-runner commit | `254a7009fbe6354b19e0da0ddd1e149ebc475f6a` |
| candidates file | `/home/alex/data/FRR20260821/data/candidates.jsonl` |
| candidates SHA-256 | `7f44b454f05e69c4a854dbd3dec894e92657b8b50a31ddb4fe97675c48a287fb` |
| candidate records | 1667 |
| dataset built by miner | `d5cae4aacf89e5e28d68a71bb96f6630cc8c6d07` |
| Git mirror | `/home/alex/data/FRR20260821/data/repos/FRRouting/frr.git` |

If the candidates SHA-256 no longer matches, the selection below was made against
different data and the rationale may no longer hold.

## The cases

### Positives — a defect was introduced and later corrected

| PR | Case ID | Subsystem | Files | +/- | Strong | Cutoff |
|---|---|---|---:|---|---:|---|
| 16738 | `case-fd8ee329b0d9d6b9` | lib | 1 | +10/-4 | 5 | 2024-09-19T20:24:19Z |
| 16141 | `case-deaebcc7295e2f7a` | nhrpd | 1 | +2/-2 | 2 | 2024-06-01T14:05:09Z |
| 17134 | `case-83d945a6dd4e5785` | ospfd | 1 | +2/-0 | 1 | 2024-10-18T11:55:46Z |
| 15632 | `case-322abe6a80cd0d1c` | zebra | 1 | +61/-37 | 1 | 2024-03-30T20:38:13Z |
| 16467 | `case-fe99bcf9d9f8ba66` | isisd | 5 | +31/-25 | 3 | 2024-07-26T13:29:38Z |
| 16194 | `case-c21d519dfa3bed45` | bgpd | 8 | +145/-1 | 3 | 2024-06-18T13:57:00Z |
| 15624 | `case-de6d5d30cb197060` | bgpd | 9 | +300/-142 | 3 | 2024-04-10T05:22:26Z |

- **16738** `lib` — stdout attached to a child process regardless of the `--log` setting.
  Highest signal density in the set relative to its size.
- **16141** `nhrpd` — core dump on shutdown. A crash, in a two-line diff.
- **17134** `ospfd` — `ospf_asbr_status` not updated by `no area nssa`. The smallest
  positive at two added lines; the cheapest possible discovery, and informative if missed.
- **15632** `zebra` — malformed JSON for multiple VRFs. Single file, substantial rewrite.
- **16467** `isisd` — flex-algo asla built incorrectly at init. Mid-size, multi-file.
- **16194** `bgpd` — a session started despite the BFD profile being in shutdown state.
  Eight files, almost pure addition.
- **15624** `bgpd` — EVPN install-event handling through the zebra client. The largest
  positive, and the strongest substantiation test: the defect spans `bgp_evpn.c`,
  `bgp_evpn_mh.c` and `bgp_zebra.c`, so finding it means following state across module
  boundaries rather than reading one function. Selected by the user rather than by the
  criteria below.

### Negatives — no corrective evidence found

| PR | Case ID | Subsystem | Files | +/- | Heuristic score | Cutoff |
|---|---|---|---:|---|---:|---|
| 17298 | `case-3a74a3a25bda9041` | isisd | 1 | +15/-9 | 0.55 | 2024-10-29T18:40:53Z |
| 16155 | `case-9ba8fca704a95e70` | zebra | 1 | +9/-6 | 0.45 | 2024-06-05T13:47:44Z |
| 15208 | `case-c96a113c3301c4fa` | pimd | 1 | +2/-4 | 0.45 | 2024-01-24T13:29:27Z |

Each was chosen by **reading its diff**, not by inferring from size and score. The test
applied: would a competent reviewer plausibly flag this, and would flagging it be wrong?

- **17298** `isisd` — restructures `else if (old_state == ISIS_ADJ_UP)` into
  `else { if (old_state == ISIS_ADJ_UP) { ... } ... }`, which widens the scope of the
  `if (new_state == ISIS_ADJ_DOWN)` block below: it now runs in every non-first-branch
  case rather than only when the adjacency was up. Reads exactly like an accidental
  brace-scope error. It is the intended memory-leak fix. Ranks 2nd of 360 by heuristic
  score.
- **16155** `zebra` — `basename(strdupa(netnspath))`, holding the result in a pointer used
  later. A stack-allocated copy passed to a function permitted to modify its argument is a
  textbook lifetime-and-aliasing hazard. It is the correct GCC14 fix.
- **15208** `pimd` — `yang_dnode_get_pimaddr(&source_addr, args->dnode, "./source-addr")`
  becomes `(..., NULL)`. Passing NULL where a path string was reads as a null dereference.
  It is itself the fix for a crash.

## Why the cohort is shaped this way

**Three negatives are load-bearing.** Milestone 4's exit criterion requires results to
distinguish false-positive suppression. If every case contains a defect, a reviewer that
reports one every time scores perfectly, and that axis measures nothing. The negatives are
chosen to bait: each contains a construct that genuinely looks like a defect — a widened
branch scope, a stack-buffer lifetime hazard, a NULL passed where a string was — and is
not one.

**What was rejected matters as much as what was chosen.** PR 17345 (`nhrpd`, 13 files) was
the right size to give the negatives a large case, but it rewrites a `memcmp` over
authentication secrets. A reviewer flagging non-constant-time comparison of a secret would
arguably be *correct*, so scoring it as a false positive would mismeasure the very axis
these cases exist to measure. A negative control must be one where flagging it is
genuinely wrong; security-adjacent code rarely qualifies. The other large zero-signal
candidates were license headers, Docker packaging, or test data — nothing a reviewer would
flag, so they measure nothing either.

**Size spans two orders of magnitude**, from `+2/-0` in one file (17134) to `+300/-142`
across nine (15624), which is what makes cost per incremental benefit measurable rather
than a single point. Note the positives carry that spread and the negatives do not — see
the caveat below.

**Seven subsystems**, so no single bug class dominates. Selection deliberately did not
follow a global ranking: the top of a ranking by signal strength is overwhelmingly
`bgpd`, and a cohort drawn that way cannot discriminate across the axes being measured.

**bgpd is 2 of 10 (20%)** against its 35% share of all candidates, after 15624 was added
and 16219 (`bgpd`, 1 file, +10/-7) dropped to hold the cohort at ten. 16219 was the least
distinctive of the `bgpd` cases once 15624 covered that subsystem with more substance.

**Negative subsystems deliberately overlap with positive ones** (`isisd` and `zebra`
appear in both). If the negatives lived only in subsystems that never appear as positives,
subsystem would become a proxy for the answer.

## Caveats

**All three negatives are single-file.** No larger candidate was both good bait and
safely wrong-to-flag — see the 17345 rejection above. False-positive rate on large diffs,
where it is plausibly highest, is therefore not measured by this cohort.

**"No corrective evidence" is not proof of correctness.** It means the correlator found
no corrective commit within the observation window (dataset built 2026-08-21, cases from
2024, so roughly two years). A missed correlation and a genuinely clean change are
indistinguishable here. The negatives are the best available control, not a guarantee,
and a reviewer finding a real defect in one of them is a legitimate outcome rather than
automatically a false positive.

**15624 carries unusual retrospective churn** — 105 medium and 38 weak signals beyond its
3 strong, an order of magnitude more than any other case. The surrounding code kept moving
afterwards, so "was this specific defect found" is harder to score cleanly here than
elsewhere in the set.

**15624 has been used as the worked example** throughout `README.md` and prior
development, so it is the least fresh case in the cohort.

**Selection saw the retrospective evidence.** That is legitimate for benchmark
construction (`docs/export-contract.md` §7 risk 2), but it means `internal/heuristics`
scoring must not be tuned by anyone who selected from this data — see `AGENTS.md`, "Never
use retrospective data to construct reviewer-facing content, even indirectly."

## Verification

**Negatives reselected 2026-09-10.** An earlier version used 15082, 17190 and 15227,
chosen from eleven arbitrarily-sampled rows rather than the full population of 360, and
described as more principled than the process was. That version also asserted 17190 had
the highest heuristic score of any zero-signal candidate; it ranks 7th. The current three
were selected by ranking all 360 and reading the diffs.

All ten exported and passed boundary validation via
`cohort-verify --bench-repo /home/alex/repos/engine-runner` on 2026-09-10, exercising both
of the engine's containment gates (`boundaryvalidator` and `contextbuilder`).

**Stopping at `reasoner-1` is the expected result and is the proof, not a shortfall.**
`bench` defaults to the deterministic fixture adapter, which replays canned responses keyed
by case id; a real case id has none, so the run fails at the adapter — *after*
`FreshWorkspace` and `contextbuilder.Prepare` have both succeeded. Reaching that failure is
what demonstrates both gates passed. Do not register fixture scenarios for real case ids:
that would manufacture fake reviews of real cases. A genuine review requires pointing
`-agents` at a vendor arm (`configs/agents/{haiku,sonnet,opus}.yaml` in `engine-runner`),
which spends money and is not what an admissibility check should do.
