# Prompt for `coder-engine-runner`

Paste the block below into an `engine-runner` session. It is written as an instruction to
that agent. Context it needs but cannot see: there is no shared memory between the two
coding roles, so everything required to act is stated inline.

---

## Task: decide on in-tree relative symlinks in the prospective source snapshot

You own `internal/boundaryvalidator` and `docs/prospective-bundle-contract.md`. The miner
side is asking you to consider one narrow change to the symlink rule, and is reporting one
change it has already made on its own side. Please read both before deciding.

### What has changed on the miner side (no action needed from you)

The miner had been maintaining a hand-written copy of your boundary rules in
`miner/internal/prospectiveexport/bundle.go`, so a bad bundle would fail at export time
rather than at ingest. That copy was a liability: two hand-maintained rule tables in two
repos with no shared memory and no automatic consistency check, because Go forbids
importing another module's `internal/` packages.

**The miner has deleted that copy.** Your validator is now the single gate. The miner's
`cohort-verify` command runs `go run ./cmd/bench` against every bundle it produces, so
your validator is exercised on the real artifacts before any cohort is frozen.

Two consequences for you:

- You are now the sole source of truth for admissibility. A rule you change takes effect
  without needing a matching edit anywhere else.
- The miner retains only checks that are its own responsibility rather than copies of
  yours: it still refuses to *materialize* a tree it cannot represent as plain files, it
  still generates and self-verifies `control/checksums.sha256`, and it still blocks the
  exact `ground_truth.json`-style filenames that your in-snapshot heuristic deliberately
  omits. Those are production and miner-policy concerns, not duplicates of your rules.

### The decision requested

**FRR is the target repository for the pilot cohort. Under the current rule, 0 of 1,667
mined candidates can produce a valid bundle.** Not a subset — every one.

The contract rejects symlinks anywhere in `reviewer/repository/`. FRR's tree carries ~77
of them (71–77 depending on commit), all under `tests/topotests/`, in every commit in its
history. Because the snapshot is the whole tree at `cutoff_commit`, a one-line `bgpd`
change still yields a snapshot containing them. The rejection is unrelated to what any PR
changed.

**What these symlinks are.** Topotests are FRR's topology test suites: each spins up
several real FRR daemons in network namespaces and asserts on routing state. 333 suites,
190 of them BGP. A suite is a directory of per-router config directories, and suites
sharing a topology symlink the shared parts instead of duplicating them:

```text
tests/topotests/bgp_instance_del_test/
  test_bgp_instance_del_test.py     the test
  customize.py -> ../bgp_l3vpn_to_bgp_vrf/customize.py
  r1           -> ../bgp_l3vpn_to_bgp_vrf/r1
  ce1          -> ../bgp_l3vpn_to_bgp_vrf/ce1
```

They are a code-sharing mechanism between test suites. Measured over one head commit:

| Property | Count |
|---|---:|
| Total symlinks | 77 |
| Resolving to a regular file in the tree | 65 |
| Resolving to a directory in the tree | 12 |
| Absolute targets | **0** |
| Targets escaping the repository root | **0** |
| Dangling targets | **0** |
| Link-to-link chains | **0** |

**The change requested.** Permit a symlink in `reviewer/repository/` only when all hold,
and reject it otherwise:

1. The target is relative, not absolute.
2. Resolved against the link's own directory, it stays inside `reviewer/repository/`.
3. It resolves to a path that exists in the snapshot.
4. It does not chain beyond a small fixed depth and does not form a cycle.

**Suggested as opt-in rather than a new default** — the same shape as
`WaivedOracleShapedPaths`. A repository with no symlinks then behaves exactly as today,
and any cohort using the relaxation has it recorded in its own run artifacts instead of
it being an invisible global weakening.

### Why the alternatives are worse

**Dereference the links into regular files (miner-side, no contract change).** The miner
will do this if you decline, but it silently corrupts a measurable slice. Dereferencing
puts identical content at two paths. If a PR edits the *target*, `diff.patch` names one
path while the snapshot carries the change at both, so the diff no longer reproduces the
snapshot:

| | `bgp_l3vpn/r1/frr.conf` | `bgp_instance_del/r1/frr.conf` |
|---|---|---|
| base tree, dereferenced | old | old |
| after applying `diff.patch` | new | old — the patch never names it |
| head tree, dereferenced | new | new |

**55 of 1,667 candidates (3.3%)** have a diff touching a symlink target and must be
rejected to stay honest, giving 96.7% coverage. Every other bundle still carries
duplicated content with no indication the copies are linked, which a reviewer may
reasonably report as a finding the format induced.

**Prune `tests/topotests/`.** Worst option. 468 candidates change `bgpd/` without editing
a test, and 190 of the 333 suites are BGP. Those tests are where FRR's intended routing
behavior is written down — the primary evidence for judging whether a change is correct,
and specifically what reasoner-3 needs to falsify a claimed finding. That is one of the
four axes the pilot exists to measure.

### Security analysis — offered for you to check, not to accept

Your stated rationale is sound: *"A symlink is rejected rather than resolved: it can point
anywhere, including at the oracle bundle."* The miner is not disputing it. What follows is
the argument that conditions 1–4 preserve it. This is your threat model; verify it
independently.

- **Validation runs against an immutable tree.** The bundle is sealed before validation
  and read-only after, so a link's target cannot change between check and use. The usual
  time-of-check/time-of-use gap does not open.
- **A link resolving in-tree cannot leave the snapshot**, which is what condition 2
  enforces.
- **`control/` is already unreachable.** Your context builder allow-list-copies only the
  three named entries under `reviewer/`, so no link can reach the control tree.
- **The oracle bundle is never under the bundle root**, so it is not a reachable target
  even for a link that did escape.

**Residual risk you should weigh:** this trades a rule needing no logic for one needing
correct path resolution, and resolution bugs are a classic source of escapes. That may on
its own be reason to decline. If you prefer a rule with no moving parts, say so plainly.

### What to reply

A yes or no. If yes, whether it should be opt-in per protocol or a change to the default.
If no, say so directly — the miner has a working fallback and will take it.

Please also say explicitly whether you changed any other boundary rule while in there.
Since the miner no longer mirrors your rules, it will not silently diverge, but it does
need to know what its bundles will be judged against.
