# Simulation-cost benchmark

The displayless companion to [the live battle benchmark](BATTLE_BENCHMARK.md).
That one measures a window and a renderer presenting a battle; this one
measures authoritative ticks with no window, renderer, audio device or
presentation consumer. It reports elapsed tick time and process CPU consumption
across the measured window, with optional thread CPU time per tick. Use it when
the change is to the simulation: movement, pathfinding, COB, weapons, damage,
economy, construction,
the feature lifecycle, visibility, AI, or the publication boundary.

```
tools/sim-bench /tmp/sim-baseline --seed 7
```

The first positional value after `--sim-benchmark` is the output directory and
it must not already exist, the same convention the windowed benchmark uses, so
one comparison can never mix two runs' artifacts. Runs serialize with each
other and with the windowed benchmark through the shared benchmark lock
(`internal/platform/benchlock`); wait time is outside every measurement.

`tools/sim-bench` defaults to a quick sample: the same 1,200 warm-up ticks,
then 300 measured ticks with profiles disabled. That is 1,500 total ticks
instead of 4,200 (64% fewer steps), measuring combat onset rather than the full
sustained battle. Inspect its census; a shorter window is not evidence about
late combat. `tools/sim-bench --full OUT` retains the 3,000-tick profiled window.
Explicit options after OUT override the quick defaults. The native command's
defaults remain unchanged.

The wrapper defaults to two Go runtime workers and two build jobs (override
`GOMAXPROCS` / `NANOLATHE_TEST_P`). Compilation takes the benchmark lock; the
native benchmark acquires it again for setup and measurement. Verification
gates do not take it and may run beside a benchmark.
Record and match the runtime worker setting when comparing. These limits reduce
contention; they do not guarantee quiet-machine timings.

Build the binary once and run it directly when comparing; use `tools/host-run`
for compilation so it cannot overlap another benchmark:

```
GOMAXPROCS=2 tools/host-run go build -p 2 -o /tmp/nanolathe-headless ./cmd/nanolathe-headless
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/sim-a --seed=7
GOMAXPROCS=2 /tmp/nanolathe-headless --sim-benchmark=/tmp/sim-b --seed=7
```

## The scene

Scene version 1, on **Town & Country** (540×540 cells, 8640×8640 world units).
Of the stock maps large enough for three 250-unit armies, this is the one whose
flattest three-army triangle scores a perfect placement heuristic *and* carries
a full feature table — about six thousand plots, two thirds of them flammable —
so the armies march on traversable ground while feature obstruction, wreckage
and fire are exercised at the same time. A map with a bare feature table
measures a materially cheaper publication.

Four setup slots. Retail's skirmish lobby refuses a setup with no human
("There must be at least one player and one computer opponent",
`[08 "Skirmish configuration"]`), so slot 0 is a human placeholder in an ally
group of its own that owns nothing but the commander battle entry stamps for it
and issues no command for the whole run. It is also the local/viewing owner.
Slots 1..3 are the three measured computer armies, each in its own ally group,
so all three are mutually hostile. Commander death is set to *continues*, so
losing a computer commander does not eliminate its remaining army. The human placeholder
still loses if its sole unit dies.

`ApplyDefaults` treats a zero `Side` as an absent value and installs `slot & 1`,
so a slot cannot ask for side 0 explicitly: the measured armies are CORE, ARM,
CORE. Both sides are exercised.

Each army defaults to 250 units — 50 buildings and 200 mobiles — plus the
commander:

| Role | ARM | CORE | Per team |
|---|---|---|---|
| solar collector | armsolar | corsolar | 14 |
| wind generator | armwin | corwin | 6 |
| metal maker | armmakr | cormakr | 6 |
| kbot lab | armlab | corlab | 2 |
| vehicle plant | armvp | corvp | 1 |
| aircraft plant | armap | corap | 1 |
| defence tower | armllt | corllt | 12 |
| radar | armrad | corrad | 4 |
| metal storage | armmstor | cormstor | 2 |
| energy storage | armestor | corestor | 2 |
| medium tank | armstump | corraid | 28 |
| assault tank | armflash | corlevlr | 28 |
| scout tank | armfav | corfav | 24 |
| light kbot | armpw | corak | 28 |
| rocket kbot | armrock | corstorm | 24 |
| artillery kbot | armham | corthud | 24 |
| fighter | armfig | corveng | 14 |
| bomber | armthund | corshad | 12 |
| scout aircraft | armpeep | corfink | 6 |
| construction kbot | armck | corck | 6 |
| construction vehicle | armcv | corcv | 6 |

`--sim-benchmark-army-size=334` places 334 units per computer army, plus
its commander: 1,006 initial units including the idle human commander. The
accepted range is 250..1000, and the army plus its commander must fit the
**resolved** per-player unit limit. Larger Strict fixtures therefore need a
larger `--unit-limit`; Community and Modern follow their resolved feature table.
All sizes retain the fifty buildings. Mobile roles scale in the table order:
for each cumulative default count, take `count × (army size − 50) / 200` with
integer truncation, then subtract the preceding quota. This assigns every
mobile, including rounding remainders. The default reproduces scene version 1
exactly; compare only matching `scene.army_size`, `scene.mobile_roster` and
`scene.passive_human`.

Enlarged fixtures also relocate the existing passive human commander before
any tick. The default map start can put that sole human unit in the enlarged
battle's path, ending the session while the computer armies are still active.
The fixture scans land on a 128-unit lattice, 256 units inside the map edges,
and chooses the valid free commander footprint furthest from its nearest
opponent, including the other original commanders. Ties keep the first point
in Z/X order; placement fails if no point is at least 2300 world units from
every opponent. The ordinary movement placement API commits the position,
recorded in `scene.passive_human`. It draws no RNG, adds no units and grants no
immunity: all ordinary damage and result rules still apply. The 250-unit default
retains its original commander position and fingerprints.

The larger fixtures retain the default placement heuristic, pitches, columns,
and orders; extra mobiles deepen the existing blocks. At large sizes those
blocks can overlap terrain obstacles or other blocks beyond the area sampled
by the default flatness heuristic. Review the census for the resulting workload
rather than assuming it has the default scene's travel and combat timing.

Geometry: the fixture picks the battle centre by a deterministic flatness
search over the terrain (no random draw, no map iteration), then lays each
army's mobiles in a block 1050 world units from that centre at 120° spacing and
its buildings 2300 units out behind them. Each army's four factories carry a
twenty-deep production queue; each mobile builder (twelve at the default size)
carries three ordinary site-anchored build orders behind its own block. Every other mobile
gets a `Move_Ground` order onto the battle centre. Like the windowed
benchmark's, this fixture places its units directly; it is not a normal
skirmish opening.

The scripted moves only guarantee that the armies meet. The AI managers then
take over on their own schedule: the classifier assigns armed ground units to
the regroup records every thirty eligible manager entries, the wave task merges
regroup into wave and broadcasts attack orders every three hundred ticks, and
by roughly tick one thousand each army carries about 160 attack orders it
issued itself. The census records both, so a run says which is driving it.

Each computer army starts with 32000 metal and 32000 energy so production and
the AI's own construction tasks proceed through the window instead of stalling
on an empty ledger. The passive human starts with 1000 of each resource.
The configured unit setting is 400. Strict uses that setting; Community and
Modern use the resolved feature table
([DESIGN_COMMUNITY_PATCH §3](DESIGN_COMMUNITY_PATCH.md#3-the-feature-table)),
whose mainline unit limit is 1500. The effective limit sizes the unit pool at
`limit × 10 + 1` records and is recorded in the scene. Use the same effective
limit when comparing revisions; `--gameplay-feature=unitLimit=400` selects the
400-unit layout under Community or Modern.

## The window

One `Step` call per authoritative tick, so the once-per-pump executor tail runs
on the same cadence as the tick it follows. Warm-up is 1200 unmeasured ticks —
the AI's first classification, construction, resource and wave deadlines, and
the march that brings the armies into contact around tick one thousand. The
native/full measured window is then 3000 ticks (100 s of game time) from battle onset
through sustained engagement, while all three armies remain active.
`--warmup-ticks` and `--benchmark-ticks` move both.

CPU profiling starts after the warm-up, so `cpu.pprof` covers the measured
window plus its immediate host bookkeeping. `alloc-base.pprof` is written
before that window and `alloc.pprof` after it closes. Each allocation profile
first forces a completed garbage collection so the sampled allocation records
include recent allocations even when no automatic collection ran during the
window. These forced collections are outside wall timing, process/thread CPU
measurements, CPU profiling and the `MemStats` delta. Profiling therefore starts
from a freshly collected heap; match the `profiles` setting across comparisons.
The allocation profiles include host census and profiler allocations as well
as the simulation, and are sampled estimates rather than exact `MemStats`
counters. Read their difference:

```
go tool pprof -base OUT/alloc-base.pprof OUT/alloc.pprof
go tool pprof -top -cum OUT/cpu.pprof
```

`--benchmark-profiles=false` skips all three profiles and those forced
collections. Process CPU measurements and `MemStats` deltas still run.

The process CPU clock covers the entire measured loop, including concurrent GC,
profiler workers, interior census samples and timing bookkeeping. Its
`ms_per_tick` is the process CPU delta divided by the number of completed ticks.
Thread CPU sampling is disabled by default: its clock reads can materially
perturb the workload, particularly on Darwin. `--thread-cpu-timing=true` enables
it for separate diagnostic runs. That clock brackets each tick on one pinned
OS thread. It includes the host clocks and synchronous GC assists on that
thread, excludes time when that thread is descheduled, and excludes concurrent
GC or profiler work on other threads. It also excludes between-tick census work.
The default process-only run neither pins the thread nor samples a per-tick CPU
clock. Use process CPU with thread sampling and profiling disabled for primary
comparisons; optional thread distributions measure a different host workload.
CPU clocks are supported on Darwin and Linux. Disabled thread timing, other
hosts and failed clock reads report `available: false` with a reason and omit
the numeric measurement; a failed thread sample discards the whole thread series.

## Per-phase attribution

`Session.PhaseObserver` is a host-side boundary notification called once as each
of the twelve phases completes, carrying the phase name and the tick label. The
session never reads a clock; the **host** stamps the time
`[I6]`. The observer draws no random number and writes no authoritative field,
so a run with it installed produces the same tick sequence as one without —
matching partial fingerprints provide regression evidence for that contract.
`--phase-timing=false` leaves it nil, and the nil test is the whole cost when unset.

Everything after the last phase boundary — the sharing tail, result evaluation,
publication and the once-per-pump executor tail — is attributed to a final row
named `tail-sharing-result-publication`.

Wall-clock reads, and two additional thread CPU reads when enabled, add
measurement overhead against a tick measured in milliseconds. Match the
phase-timing and thread CPU settings across the runs being compared.

## Reading `sim-bench.json`

- `runtime` — Go version, platform, CPU count and the source revision. A binary
  built inside a git worktree carries no VCS stamp of its own, so `tools/sim-bench`
  passes `NANOLATHE_BENCH_REVISION`; set it yourself when running a prebuilt
  binary directly, or the field is empty.
- `scene` — version, map, terrain size, the chosen battle centre and its
  flatness score, army size and scaled mobile roster, and each army's placement
  and counts.
- `initial_fingerprint` / `warm_fingerprint` / `final_fingerprint` — the
  versioned partial state fingerprint before fixture armies and relocation are
  applied, at window open and at window close, plus the two RNG draw totals.
  Two runs of one binary at one seed must agree on all three; if they do not, the run is not comparable and
  the divergence is the finding.
- `ms_per_tick_p50/p95/p99/max`, `ticks_per_second`, `tick_ms` — the window's
  timings, and the whole per-tick series for a distribution or a stall hunt.
- `process_cpu` / `thread_cpu` — availability, CPU milliseconds and CPU
  milliseconds per tick. Process CPU includes every thread across the window;
  thread CPU sums only the individual tick intervals on the pinned host thread.
  `tick_thread_cpu_ms` parallels `tick_ms` when thread CPU is available.
  `thread_cpu_timing` records whether the diagnostic sampling was requested;
  otherwise `thread_cpu.unavailable_reason` is `disabled`.
- `phases` — millseconds per tick and share of the window per phase.
- `gc` — collections, pause total, and allocated bytes and objects, every one a
  **delta between two `MemStats` reads** taken as the window opens and closes.
  The heap figures are instantaneous gauges. `gc_cpu_fraction_lifetime` is
  the exception its name declares: the runtime keeps that fraction over the
  whole process and it cannot be differenced, so it is not the window's GC
  share — read that from `runtime.gcBgMarkWorker` in `cpu.pprof`.
- `tick_restamps` / `restamps` — a per-tick series parallel to `tick_ms`
  counting end-to-end movement class-layer rebuilds
  (`movement.(*System).ClassLayerFullStamps`, a plain counter the host samples
  between ticks; it allocates nothing, reads no clock and the simulation never
  sees it), and a summary relating them to the slow ticks. A full rebuild
  reclassifies every attribute cell of a layer, so it is the first thing to
  check when the p95 leaves the p50 behind: the summary says how many ticks
  over the threshold carried one and whether any rebuild landed in a fast tick.
- `census` — samples at window open, five interior points and window close.
  Each carries live units, in-flight projectiles, active effects, fragments,
  strip objects, feature count and how many are burning, published events,
  build progressions and cumulative deaths; and per army the live count split
  into mobiles and buildings, how many are moving, how many hold a path
  request, nanoframes under construction, order-queue depth split into attack,
  move and build intents, factory production and queue depth, kills, losses,
  both stocks, and the AI manager's ten task-group member counts and its task
  deadlines.

The census is the evidence that the run measured what it claims. A window whose
armies are idle, whose teams never met, or whose factories are all starved is
not this benchmark's workload, and its timings mean something else.

## Comparing runs

The [September 2026 speed experiment](SIM_SPEED_EXPERIMENT.md) records a
1,006-unit comparison across all three reserved modes, including loaded-host
CPU limitations and allocation measurements.

Compare the same scene version, army size and roster, map, seed, effective unit
limit, runtime worker count, window, profiling, phase-timing and thread CPU
sampling settings, from binaries built before the runs were queued. Prefer a quiet machine. When other
work is running, alternate the two binaries under similar load and report both
process CPU per tick alongside wall time. Compare thread CPU distributions only
between runs that both opted into that additional instrumentation.
CPU clocks remove scheduling waits, but contention for caches, memory bandwidth
and CPU frequency still affects the work; they do not make a loaded machine
identical to a quiet one. Report the median, p95 and max together with the
census, never a median alone: this scene's cost falls as units die, so a run that
killed more units looks faster.

A change that alters `final_fingerprint` altered the simulation, not only its
speed. That is a behaviour change and needs justifying as one, separately from
any timing it produced.

This is an opt-in development and regression probe, not a CI threshold. Keep
baseline outputs outside the repository.
