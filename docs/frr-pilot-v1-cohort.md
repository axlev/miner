# FRR pilot-v1 cohort

**Status:** frozen 2026-09-10. All ten cases exported and passed `engine-runner`'s
boundary validation.

This is the case list for the first cohort, per `system-design.md` §15 Milestone 4
("Run 5-10 cases without per-case prompt tuning"). It records what was selected, why,
and enough provenance to rebuild the identical bundles.

**Evaluator-facing.** The rationale below cites retrospective signal counts, which is
legitimate for choosing benchmark cases and must never reach a reviewer.

## Reproducing

Case IDs are a SHA-256 of repository, PR number, and cutoff, so the same inputs always
produce the same identifiers. Re-running with the provenance below yields byte-identical
case IDs; the bundles themselves are reproducible up to Git object content.

```bash
go run ./cmd/miner cohort-verify \
  --input /home/alex/data/FRR20260821/data/candidates.jsonl \
  --repo /home/alex/data/FRR20260821/data/repos/FRRouting/frr.git \
  --cache-dir /home/alex/data/FRR20260821/cache \
  --prs 16738,16141,16467,17134,16194,15632,15624,15082,17190,15227 \
  --out output/frr-pilot-cohort \
  --bench-repo /home/alex/repos/engine-runner
```

Each case is pinned to its own `merged_at`; no `--cutoff` override is used. Bundles land
in a gitignored working directory and are not committed — they are ~75 MB each and fully
derived from the inputs below.

### Provenance

| Input | Value |
|---|---|
| miner commit | `40fd728d24056006dd98163068cd66fbd8dd7185` |
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
| 15082 | `case-b1bd435424400098` | lib/include | 9 | +840/-14 | 0.20 | 2024-05-28T21:01:20Z |
| 17190 | `case-b17c021ff95ec195` | isisd | 2 | +38/-31 | 0.45 | 2024-10-29T14:08:29Z |
| 15227 | `case-7054a0906a814253` | bgpd | 3 | +86/-116 | 0.35 | 2024-01-25T07:55:08Z |

- **15082** — Linux `IFF_LOWER_UP` flag handling. A large change to flag semantics that
  looks risky and was not corrected.
- **17190** — `show isis vrf all summary json`. The highest stateful risk score of any
  candidate with no corrective evidence: the heuristic expected a defect, the history
  disagrees.
- **15227** — titled "Some fixes", net deletion of 30 lines, no follow-up in two years.

## Why the cohort is shaped this way

**Three negatives are load-bearing.** Milestone 4's exit criterion requires results to
distinguish false-positive suppression. If every case contains a defect, a reviewer that
reports one every time scores perfectly, and that axis measures nothing. The negatives
are chosen to bait rather than to be obviously safe: substantial diffs with non-trivial
heuristic scores, not documentation changes no reviewer would flag.

**Size spans two orders of magnitude**, from `+2/-0` to `+840/-14`, which is what makes
cost per incremental benefit measurable rather than a single point.

**Seven subsystems**, so no single bug class dominates. Selection deliberately did not
follow a global ranking: the top of a ranking by signal strength is overwhelmingly
`bgpd`, and a cohort drawn that way cannot discriminate across the axes being measured.

**bgpd is 3 of 10 (30%)** against its 35% share of all candidates — close to
representative, after 15624 was added and 16219 (`bgpd`, 1 file, +10/-7) dropped to hold
the cohort at ten. 16219 was the least distinctive of the four `bgpd` cases once 15624
covered the same subsystem with more substance.

## Caveats

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

All ten exported and passed boundary validation via
`cohort-verify --bench-repo /home/alex/repos/engine-runner` on 2026-09-10, exercising both
of the engine's containment gates (`boundaryvalidator` and `contextbuilder`). The runs
proceed to the reasoner stage and stop there because the fixture adapter has no scenario
registered for a real case id, which is expected and unrelated to bundle admissibility.
