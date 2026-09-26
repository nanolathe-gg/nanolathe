# Simulation speed experiment — 2026-09-25

This is an implementation experiment, not retail evidence. It records the first
simulation-speed optimization round, developed on `proto/sim-speed` from
`af768a7e` and approved for landing on 2026-09-25.

## What changed

Three small shared optimizations preserve the existing rule selection:

1. **Feature-index storage:** retain the sorted key and value buffers across
   feature additions/removals. Every rebuild still sorts the same anchors, and
   every tick still reconciles external terrain writes. Clear unused pointer
   slots so old records do not remain reachable. See
   [feature lookup ownership](DESIGN_ECONOMY_CONSTRUCTION.md).
2. **Frame publication:** omit feature radar contacts that every production
   minimap/strategic-icon consumer already ignores. Features still publish
   through `Frame.Features`; unit and projectile contacts are unchanged.
   This also removes stale helper code inconsistent with retail specification
   03 §3.9. Copy COB piece views directly into their destination to avoid
   intermediate large-struct copies. See
   [publication design](DESIGN_RUNTIME_DETERMINISM.md).
3. **Damage overrides:** search the private compiled key order directly instead
   of making a defensive slice copy per lookup. Keep the existing lower-bound
   ordering, live damage values, case comparisons, signed override and unsigned
   default semantics. Hand-authored Go fixtures keep their sorted-map fallback.
   See [damage design](DESIGN_WEAPONS_PROJECTILES.md).

These change storage and redundant work. They add no gameplay policy, RNG draws,
new dependencies, concurrency, or arithmetic changes. They execute under Strict
3.1, Community 3.9 and Modern alike. The internal radar diagnostic list becomes
smaller; this is an intentional publication change with no production consumer
for the removed entries.

## Workload and measurement

The matching baseline contains the benchmark-only commits `5a7d2e30` and
`b7e431b1` on top of `af768a7e`. The measured candidate is `f48efae5`, which adds
all three optimizations and the same harness. Later audit/test/documentation edits
do not alter the measured production code.

- Town & Country, seed 7, three mutually hostile computer armies.
- 334 placed units per army: 50 buildings and 284 mobiles, plus four original
  commanders: **1,006 units initially**. Tanks, kbots, artillery, aircraft and
  builders run normal movement, targeting, combat, construction and AI.
- Effective per-player limits are 400 in Strict and 1,500 in Community/Modern;
  each baseline/prototype pair uses the same resolved limit.
- Twelve factories retain ordinary production queues. Fifty-four mobile
  builders start ordinary construction orders. No mutators.
- 1,200 warm-up ticks, then **3,000 measured ticks** (100 simulation seconds).
- Go 1.25.0, Darwin/arm64, Apple M3 Pro, 12 logical CPUs, `GOMAXPROCS=2`.
- Prebuilt binaries run sequentially under the normal benchmark lock. Three
  paired rounds reverse baseline/candidate order in the middle round.
- Primary timing is process CPU across the whole window divided by completed
  ticks. Profiling, phase timing and per-tick thread CPU timing are disabled.
  Exact `MemStats` deltas supply bytes and objects per tick.

Background AI tournaments continued. Observed load averages exceeded 100.
Process CPU excludes time descheduled but remains sensitive to CPU frequency,
cache/memory contention and GC. Small CPU differences are therefore tentative.
Census and timing bookkeeping are included equally in both process windows.
This is not a quiet-host throughput claim or a prediction for every map.

The enlarged fixture relocates its existing passive human commander using the
ordinary placement API, before ticks begin, with no RNG. Otherwise the observer
can die and terminate an active three-army battle. The default 250-unit fixture
and its checked-in fingerprints remain unchanged. Enlarged geometry and the
relocation are recorded in scene metadata. See [benchmark contract](SIM_BENCHMARK.md).

The census proves sustained work rather than an idle unit count:

| Mode | Live units sampled | Moving units | Projectiles | Active builds | Producing factories | Deaths during window |
|---|---:|---:|---:|---:|---:|---:|
| Strict 3.1 | 982–1,018 | 276–801 | 5–46 | 19–37 | 12 | 108 |
| Community 3.9 | 962–1,025 | 439–802 | 23–44 | 28–47 | 12 | 146 |
| Modern | 796–1,002 | 140–600 | 39–88 | 23–42 | 12 | 285 |

These are seven census samples, not continuous extrema. Modern loses more units
because its rules produce a different battle; compare each mode with itself.
All modes retain about 6,200 terrain features, with burning trees and wrecks.

## Results

The exact allocation reduction is the clearest result. CPU savings below are
the median of three **paired percentage reductions**, not a ratio of unrelated
medians. Positive means less CPU.

| Mode | CPU reduction, median (range) | Allocated KiB/tick, baseline → prototype | Bytes saved | Objects/tick, baseline → prototype |
|---|---:|---:|---:|---:|
| Strict 3.1 | +5.6% (+2.1 to +20.2%) | 33.36 → 25.73 | 22.9% | 275.9 → 275.3 |
| Community 3.9 | -0.5% (-1.7 to +0.7%) | 46.51 → 39.80 | 14.4% | 421.5 → 421.0 |
| Modern | +4.6% (-0.1 to +8.2%) | 100.70 → 81.14 | 19.4% | 663.9 → 641.8 |

Raw process CPU milliseconds per tick, in chronological paired rounds:

| Mode | Round 1 B → P | Round 2 B → P | Round 3 B → P |
|---|---:|---:|---:|
| Strict 3.1 | 5.159 → 5.049 | 6.251 → 5.901 | 5.786 → 4.616 |
| Community 3.9 | 5.823 → 5.855 | 6.579 → 6.532 | 5.686 → 5.784 |
| Modern | 7.147 → 6.821 | 6.883 → 6.321 | 5.959 → 5.962 |


Elapsed tick distributions below include scheduling delays and are retained for
diagnosis. They are not the primary loaded-host speed measurement. Values are
p50 / p95 / maximum milliseconds.

| Mode / round | Baseline wall ms | Prototype wall ms |
|---|---:|---:|
| Strict / 1 | 4.83 / 9.10 / 36.68 | 4.53 / 8.80 / 20.51 |
| Strict / 2 | 6.75 / 10.36 / 26.11 | 6.35 / 9.74 / 25.29 |
| Strict / 3 | 5.94 / 9.99 / 20.60 | 3.75 / 8.52 / 16.44 |
| Community / 1 | 5.49 / 10.64 / 30.92 | 5.23 / 10.86 / 26.56 |
| Community / 2 | 6.49 / 12.00 / 35.55 | 6.60 / 11.77 / 33.16 |
| Community / 3 | 4.85 / 10.30 / 37.82 | 5.27 / 10.23 / 31.97 |
| Modern / 1 | 7.32 / 12.29 / 43.02 | 7.15 / 11.83 / 34.25 |
| Modern / 2 | 6.90 / 12.35 / 42.12 | 5.97 / 11.04 / 28.18 |
| Modern / 3 | 5.33 / 10.52 / 28.73 | 5.46 / 10.39 / 27.57 |

Community CPU is effectively unchanged. Strict and Modern show a possible modest
win, but the large spread—especially Strict round 3—prevents a precise throughput
claim. The allocation reduction is consistent across repetitions. Most eliminated
feature-index allocations were large arrays, so byte savings exceed object-count
savings.

**Recommendation:** keep these small changes after review. They reduce allocation
pressure in every mode and remove redundant shared work with limited complexity.
Do not present them as a major speed breakthrough. Repeat on a quiet host before
publishing a firm CPU-speedup figure or pursuing a larger cache/invalidation design.

## Focused allocation checks

The feature churn benchmark removes/reinserts one anchor in a 4,096-feature
forest, taking an ordered snapshot after each mutation. It changes from
**131,072 bytes and 4 allocations per operation to zero** once capacity is warm.
Three loaded-host samples were 660–795 microseconds before and 460–568 after;
the allocation result is the stronger evidence.

The compiled uppercase damage-lookup fixture changes from **80 bytes / 5
allocations to 24 bytes / 3 allocations** per lookup. Its three samples were
279–303 ns before and 157–203 ns after. Lowercase compiled lookups allocate zero.
The remaining uppercase allocations come from the preserved case conversion;
this experiment does not change comparison semantics to chase them.

These microbenchmarks explain mechanisms; their percentages are not whole-tick
speedups. The combined match comparison cannot attribute its CPU delta to each
individual optimization.

## Verification and limits

All nine unprofiled A/B pairs completed all 3,000 measured ticks in `Battle(6)`.
Each pair matched scene/catalog/rule metadata, initial/warm/final partial
fingerprints, simulation and CRT RNG draw totals, all seven census samples and
every tick's path-restamp count. A separate Modern pair with profiling enabled
also matched. No simulation fingerprint constants were changed. The existing
I1 source-audit digest for `sortedInstanceKeys` was updated after ownership and
ordering review; its map-range exception and sorting remain unchanged.

The existing `initial_fingerprint` is taken before fixture armies are added;
it does not independently validate fixture placement. Warm/final fingerprints
and census do exercise the fixture. Fingerprints remain deliberately partial,
so these checks are strong regression evidence, not proof for every possible
state. The prototype adds targeted checks for ownership across buffer rebuilds,
ordered removals, duplicate damage-key winners, live damage edits and feature
publication. Independent review approved the buffer ownership and measurement
windows.

The ordinary live battle benchmark also ran once per build with each renderer:
Expanded Confluence, scene 5, Modern gameplay, 1920×1080, native zoom, 30 TPS,
300 pre-window ticks, 60 warm-up draws and 180 measured draws. Both renderer
captures were visually inspected and are **byte-for-byte identical** to their
matching baseline PNG. All eight files at each of `state-start` and `state-end`,
every frame census and every renderer counter also match. The scene retains
302–323 units, 182–212 moving units, 7–8 active builds, 5,559–5,564 features and
1–7 burning features across the window.

| Renderer | Step p50 / p95 ms, baseline → prototype | Draw work p50 / p95 ms, baseline → prototype |
|---|---:|---:|
| Classic | 7.221 / 9.909 → 6.796 / 9.303 | 38.836 / 48.413 → 39.239 / 48.381 |
| Modern | 7.645 / 10.082 → 6.989 / 9.378 | 26.310 / 31.830 → 24.314 / 30.349 |

These single-pair wall timings are regression diagnostics under contention,
not a separate renderer-speedup claim. Captures show intact units, trees,
factory spray, projectiles, explosions, coast, fog and minimap.

`tools/check` and the short `tools/check-retail` gate both pass, including
staticcheck/deadcode, architecture and clean-room guards, the three reserved
modes' 6,000-tick skirmish and 1,500-tick default-fixture fingerprint locks,
asset-backed contracts and the real GPU device fixtures. The 54,000-tick
`--full` acceptance tier was not run. The new 1,006-unit A/B trajectories are
additional evidence, not replacements for those existing locks.

## Remaining opportunities

The exploratory profile made frame publication the largest named cumulative
cost (roughly one third of samples), with feature input refresh and piece copies
inside it. Feature-grid reconciliation and live-unit snapshot allocation also
remain visible. A separate allocation profile exposed the feature-index
rebuilds fixed here. In the final profiled Modern pair, `sortedInstanceKeys`
drops from 55.42 MB of sampled window allocations to none. The candidate's
remaining largest allocation sites include `World.AppendLive` (47.86 MB),
`NodeStore.allocFresh` (44.88 MB), and `stripTable.append` (31.97 MB). These are
sampled profile estimates, not exact byte accounting. Candidate frame
publication remains 30.3% of cumulative CPU samples, so it is still worth
investigating; this optimization did not remove the whole publication cost.

The next candidates deserve focused ownership audits:

- Reuse caller-owned live-unit snapshots in remaining hot `World.Iter` callers.
  `AppendLive` already supports this. A single global scratch slice would be
  unsafe where callbacks or nested walks retain their snapshot.
- Reduce escaping closures in Community off-map aircraft scans. This can also
  benefit Modern, but does not help Strict and must preserve canonical bucket
  order, collision admission and first-victim selection.
- Investigate reusable path-node storage after auditing search-session lifetime.
  Reuse must retain all queue ordering, tie decisions and work-budget accounting;
  changing the path policy would invalidate this comparison.
- Inspect strip-object and effect-view allocation with their lifetime and RNG
  contracts in hand. Their progression cannot simply be culled when offscreen.

Skipping feature-grid reconciliation based only on obstacle revision is **not
safe**: that revision does not describe every external plot write or nonblocking
feature replacement. A fuller invalidation contract must precede such a change.
Likewise, caching whole feature views needs complete mutation ownership; the
current inputs are publicly mutable. This prototype avoids both assumptions.

## Reproduction

Build matching binaries before the run sequence, from the baseline and candidate
worktrees, using `GOMAXPROCS=2 GOFLAGS=-trimpath tools/host-run go build -p 2`.
For each mode (`strict-3.1`, `community-3.9`, `modern`), alternate the two binaries:

```sh
GOMAXPROCS=2 NANOLATHE_BENCH_REVISION=<revision> <binary> \
  --sim-benchmark=<new-output-directory> \
  --sim-benchmark-army-size=334 --seed=7 --gameplay=<mode> \
  --warmup-ticks=1200 --benchmark-ticks=3000 \
  --phase-timing=false --thread-cpu-timing=false --benchmark-profiles=false
```

Require all 3,000 ticks, `Battle(6)` state, identical scene/catalog/rule metadata,
all three partial fingerprints, both RNG totals, census samples and restamp
series. Never pool results from different instrumentation settings.

Per-tick thread CPU sampling is now optional: Darwin clock calls accounted for
about 12% of samples in an exploratory instrumented run. Process-window CPU reads
avoid that per-tick perturbation. Allocation profiles now force a GC outside
measurement at each profile boundary, fixing Go's delayed allocation samples
when a measured window contains few automatic collections.

Raw JSON, logs, profiles, comparison scripts and captures are outside the repo
at `/private/tmp/nanolathe-sim-speed/`. The report retains aggregate evidence;
those temporary files are not a durable archive and contain no committed retail
assets.
