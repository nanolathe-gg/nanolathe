# Headless pathfinding benchmark

This opt-in suite uses authored terrain and unmodified retail units, movement
classes and COB programs. It measures Nanolathe's current implementation; it is
not evidence of retail executable behavior. No pathfinding algorithm changes
are part of this work. Runs stay outside normal CI. The measured effects quoted
in [DESIGN_MOVEMENT_PATH](DESIGN_MOVEMENT_PATH.md) were produced with it.

## Run and compare

```sh
# Retail assets default to ~/TotalAnnihilation; --assets overrides them.
tools/path-bench --list
tools/path-bench /tmp/path-baseline --rules modern --sizes 1,8 --repeats 3
tools/path-bench /tmp/path-candidate --rules modern --sizes 1,8 --repeats 3
tools/path-bench compare /tmp/path-baseline /tmp/path-candidate > /tmp/path-comparison.md

# Narrow iteration or an extra CPU/allocation profile pass:
tools/path-bench /tmp/maze --cases 'maze|winding|concave' --rules modern --repeats 1 --profiles
```

Without filters the suite runs all supported sizes and all three reserved rule
sets, with three timed repeats per case. A custom registered `RuleSet` name can
be selected with `--rules`. The timed suite holds the benchmark lock, as the
battle benchmarks do, so it never overlaps another benchmark; verification
gates may run beside it. A new output directory is required; artifacts belong
outside the repository. `--ticks` is a fixture-debug override, not a comparable
replacement for the normal observation window.

The directory contains `manifest.json` (revision, host/runtime and settings),
`cases.json`, exact production Alt+drag inputs and separate assignment timings
in `formations.json`, per-case JSON reports, a compact `summary.md`, and a
completion marker. Each report includes the catalog/asset identity, terrain
hash, retail movement profiles, starts, scripted events, issued moves, cohorts,
raw tick samples and deterministic outcomes. An incomplete or skipped run
cannot become a baseline. `compare` refuses different scenarios, assets,
settings, fixture source, host/runtime, windows, missing cases or inconsistent
repeats (including a different repeat count). A code
revision may differ, as required for algorithm experiments. Input identity
excludes timing samples and resulting trajectories.

Diagnostic observation runs on its own rebuilt scene. Every timed repeat must
match its initial partial state, issued inputs, per-tick actor trajectory and
final partial fingerprint. These checks cover the named state, not a complete
desync proof. The diagnostic pass wraps the existing `path.Kernel` through the
session's `RuleSet` and counts searches, setup work and popped nodes; the timed
pass binds the original kernel without tracing. No new gameplay seam is added.

## Fast iteration

Use a focused selection while editing; reserve the full matrix for reviewing a
candidate. Measured on the baseline host, with complete scenario windows:

| Selection | Recorded execution time |
|---|---:|
| Full matrix, three modes and three timed repeats | 12m 52s wall time including setup |
| All Modern case/size combinations, three timed repeats | 4m 23s summed subtest time |
| Modern maze/winding/concave families, three timed repeats | 12.5s summed subtest time |
| Six-case iteration selection below, one timed repeat | 5.4s benchmark execution |

Subtest/execution figures exclude process startup, compilation and host-lock
waits. The six-case measurement took 30s wall time including startup and a
shared-lock wait; its two Go test processes reported 6.2s combined. Build-cache
misses and competing verification jobs add latency independently of the scenes.

```sh
tools/path-bench /tmp/path-iterate --rules modern --sizes 1,8 --repeats 1 \
  --cases '^(control_single_flea|formation_line_8|terrain-branching-maze|traffic/(open_mixed|choke_two_opposing))$'
```

This covers an open control, mixed units, opposing choke traffic, Alt+drag and
two maze group sizes. Add or substitute the family being changed, such as naval
or knowledge cases. For a single regression, select its exact ID and size;
most baseline combinations took 1–3s even with three timed repeats, before
startup/wait time. The 1,500-unit Modern case took 19s.

Keep normal tick windows: shortening a maze run can hide route continuation,
late arrivals or jams. One timed repeat is useful during debugging; use three
or more matching repeats when assessing performance, and the full mode/size
matrix before accepting a candidate. Profiles add another pass and are best
requested only for the case being investigated. These commands use the same
fixtures and diagnostic checks as the full suite; no fast-path simulation is
substituted. Compare runs with identical selections and repeat counts.

## What the measurements mean

- **Behavior:** observed reporting goals, first proximity tick, observed
  move-head disappearance, remaining orders, removal, raw and cell-level stalls,
  blocked unit-ticks, request polls, pending age, observed results, travel proxy,
  and visits/reentries to named regions, mapped/learned-terrain counts before events and at the end, and
  whole-unit census including factory products. Regions/cohorts expose wrong turns and
  direction fairness. Arrivals remain physical obstacles throughout the window.
- **Cost:** p50/p95/p99/max and all raw timings for the twelve authoritative
  phases, plus scripted event time separately. Allocation counters cover the
  whole window, including events and an allocation-free trajectory observer.
  Fixture/catalog construction, diagnostics and final hashing are excluded.
  Heap values are process-wide gauges, not memory owned exclusively by one case.
- **Scope:** no renderer, window, sound device, map asset or presentation
  publication. The outer sharing/result/community tail is also excluded. This
  isolates path/movement workloads; the existing [simulation battle benchmark](SIM_BENCHMARK.md)
  remains the integration/performance check before adopting an implementation.

`near_goal` means the actor entered its explicitly recorded reporting radius;
it does **not** prove that a repair, build, patrol, guard or transport operation
completed. `away_no_move_head` means it never entered that radius and a move
was absent at an observed tick, not a universal failure code. `pending`,
`superseded`, `cancelled`, and `removed` stay separate. `superseded` means a later reporting goal replaced this goal; a queued order
may still exist. Multiple commands for the same actor in one tick retain only
the last reporting goal; the input trace records every issue. Arrival percentiles include arrivals
only; always read them with the denominator. Stalls stop accumulating at first
proximity; travel is the sum of maximum-axis displacement, an integer distance
proxy rather than Euclidean route length. Pending age measures consecutive
observed pending ticks, not unobservable internal enqueue-to-admission latency.

All runs start at tick one after construction and a host GC. There is no
simulation warm-up to erase initial command cost. Fixed windows include the
idle tail after early arrivals. Repeats quantify host noise; lower time with
worse completion is not automatically an improvement. Per-type open controls
are available; compare matching routes and goals before interpreting a travel
ratio. Single-unit controls cannot predict congestion by multiplication.

`--profiles` adds a separate unreported-cost pass: `*.cpu.pprof` covers the
phase loop plus its host events/observer, and allocation snapshots support
`go tool pprof -base CASE.alloc-before.pprof CASE.alloc-after.pprof`.
Profiling does not contaminate the reported timing samples. Allocation profiles
are sampled and can lag garbage collection; a short-window difference may be
empty (as in the initial small-maze profile). Use the exact `AllocBytes` and
`Allocs` counters for allocation comparisons, not the sampled profile total.

## Thread CPU time, traces and comparison tools

Each report also records `TickCPUNS`, the per-tick CPU time of the thread that
ran the tick (the runner locks its goroutine to one OS thread while timing;
Unix hosts only, zero elsewhere). On a host shared with other work, wall-clock
tails move with that work while thread CPU time mostly does not, so compare
CPU figures, and compare candidates by alternating runs of the two builds
rather than by runs taken minutes apart.

Setting `NANOLATHE_PBTRACE=<dir>` writes, on the diagnostic pass, one JSON
lines trace per case, rule set and size: the terrain, then every unit's cell
position, owner, blocked flag and move-order flag every 15 ticks
(`NANOLATHE_PBTRACE_EVERY` changes the cadence), then the reporting goals.
`tools/path-bench-render TRACE OUT.png` draws a trace as a contact sheet
(Python with Pillow).

`tools/path-bench-variants DIR` prints one table row per case, size and rule set
(arrivals, pending, blocked ticks, arrival ticks, wall and CPU tick percentiles,
searches, pending age). `tools/path-bench-winloss DIR BASE CAND[,CAND…]`
lists the cases each candidate gains or loses against a base rule set, with the
corpus totals excluding the scripted waves.

## Comparison rule sets

The suite registers rule sets for attributing a change to a Modern pathfinding
policy (`internal/session/path_bench_before_test.go`): `modern-no-pathfinding`
switches every one off (bounded path work, group destination slots, allied
pass-through, unreachable moves, jam release and route straightening), and
`modern-no-bound`, `-no-slots`, `-no-pass`, `-no-unreach`, `-no-jam` and
`-no-straighten` each switch off one. A new Modern pathfinding policy adds its
own `-no-` set and joins `modern-no-pathfinding`.

Scenario details: [terrain, naval and knowledge](PATH_BENCHMARK_TERRAIN.md),
[traffic and lifecycle](PATH_BENCHMARK_TRAFFIC.md), and
[Alt+drag](PATH_BENCHMARK_FORMATIONS.md). The `avoid/*` family
(`path_bench_avoid_test.go`) adds friendly traffic against friendly traffic:
two same-owner groups swapping sides in open ground (16 and 64) and through
eight- and four-cell apertures, mixed profiles swapping sides (32), a group
crossing an idle friendly army (16 and 64), a hostile head-on control (16 and
64), and 128–256-unit open groups at three start offsets. These are authored development
experiments, not approvals for gameplay changes.

## Fixture contract

All suite files carry `pathbench && retail` build tags. The shared helper lives
in `internal/session/path_bench_test.go`; scenario files register `pbCase`
values through `pbRegister` during test initialization. This is a test catalog,
not a second gameplay registry. `pbNew` selects existing `Session.SetRules`.
Owner zero is the single local human; other active slots have computer control
records but no AI managers. Multi-owner requests are scripted. Knowledge cases
that change the human owner update both its player record and input adapter.

A case has a stable ID, family, description, supported sizes, fixed tick window,
and `Build(t, rules, size) *pbScene`. Rebuilds must produce identical initial
state. Builders use `pbTerrain` (dimensions in cells), `pbNew`, and `pbAdd`
(retail unit key, cohort, owner, world-center cell); catalogs are immutable. Spawns
must be terrain-passable and non-overlapping, using the resolved movement
footprint and production anchor quantizer before occupancy is stamped. Width and length must fit all
footprints; scenario counts mean commanded units unless explicitly documented.
`pbCell` converts a cell to its world center. `pbWall` authors void rectangles
before session construction; it is not a feature-service replacement.

`pbMove` issues the ordinary player group command; `assigned=true` is reserved
for per-unit assigned destinations or explicitly labeled capacity controls.
The runner captures actual group-adjusted goals. `pbMoveWorld` accepts raw
16.16 destinations. Actors retain handles, cohort, reporting goal and reporting
radius (default 16 world units). This radius defines an observational metric,
not the simulation's order completion rule. Passive obstacles have no active
goal. `ExpectedRemoval` labels an intentional lifecycle removal.

Events run before their numbered tick; labels and `Inputs` must describe exact
parameters and intended changes. Event callbacks use production services.
`Regions` are half-open cell rectangles for cohort entry/occupancy measurements.
`Notes` state intentionally impossible destinations and fixture limitations.
No callback uses wall-clock time or nondeterministic iteration for decisions.

The runner will rebuild each scene for diagnostic and timing passes. Case
builders must not retain mutable state across calls or mutate retail content.
The authoritative twelve-phase cycle is measured without window, audio,
rendering, or tick-end presentation publication. Events and observation are
outside tick timing. Separate diagnostic work is excluded from cost runs.
