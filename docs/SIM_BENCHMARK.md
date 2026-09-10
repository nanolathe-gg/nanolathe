# Simulation-cost benchmark

The displayless companion to [the live battle benchmark](BATTLE_BENCHMARK.md).
That one measures a window and a renderer presenting a battle; this one
measures the authoritative tick and nothing else — no window, no renderer, no
audio device, no presentation consumer. Use it when the change is to the
simulation: movement, pathfinding, COB, weapons, damage, economy, construction,
the feature lifecycle, visibility, AI, or the publication boundary.

```
tools/sim-bench /tmp/sim-baseline --seed 7
```

The first positional value after `--sim-benchmark` is the output directory and
it must not already exist, the same convention the windowed benchmark uses, so
one comparison can never mix two runs' artifacts. Runs serialize with each
other and with the windowed benchmark through the shared benchmark lock
(`internal/platform/benchlock`); wait time is outside every measurement.

Build the binary once and run it directly when comparing, so compilation is not
inside the lock and not inside the numbers:

```
go build -o /tmp/nanolathe-headless ./cmd/nanolathe-headless
/tmp/nanolathe-headless --sim-benchmark=/tmp/sim-a --seed=7
/tmp/nanolathe-headless --sim-benchmark=/tmp/sim-b --seed=7
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
so all three are mutually hostile. Commander death is set to *continues*, so no
single commander loss can end the battle inside the measured window.

`ApplyDefaults` treats a zero `Side` as an absent value and installs `slot & 1`,
so a slot cannot ask for side 0 explicitly: the measured armies are CORE, ARM,
CORE. Both sides are exercised.

Each army is 250 units — 50 buildings and 200 mobiles — plus the commander:

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

Geometry: the fixture picks the battle centre by a deterministic flatness
search over the terrain (no random draw, no map iteration), then lays each
army's mobiles in a block 1050 world units from that centre at 120° spacing and
its buildings 2300 units out behind them. Each army's four factories carry a
twenty-deep production queue; each of its twelve mobile builders carries three
ordinary site-anchored build orders behind its own block. Every other mobile
gets a `Move_Ground` order onto the battle centre. Like the windowed
benchmark's, this fixture places its units directly; it is not a normal
skirmish opening.

The scripted moves only guarantee that the armies meet. The AI managers then
take over on their own schedule: the classifier assigns armed ground units to
the regroup records every thirty eligible manager entries, the wave task merges
regroup into wave and broadcasts attack orders every three hundred ticks, and
by roughly tick one thousand each army carries about 160 attack orders it
issued itself. The census records both, so a run says which is driving it.

Per-player starting stock is 32000 metal and 32000 energy so production and the
AI's own construction tasks proceed through the whole window instead of
stalling on an empty ledger. The configured unit limit is 400, which sizes the
unit pool at `limit × 10 + 1` records and leaves each army headroom above its
250 placed units for everything it builds.

## The window

One `Step` call per authoritative tick, so the once-per-pump executor tail runs
on the same cadence as the tick it follows. Warm-up is 1200 unmeasured ticks —
the AI's first classification, construction, resource and wave deadlines, and
the march that brings the armies into contact around tick one thousand. The
measured window is then 3000 ticks (100 s of game time) from battle onset
through sustained engagement, while all three armies are still at full
strength. `--warmup-ticks` and `--benchmark-ticks` move both.

CPU profiling starts after the warm-up, so `cpu.pprof` covers the measured
window only. `alloc-base.pprof` is written at the same instant and `alloc.pprof`
after the window closes; read the window's allocations as the delta:

```
go tool pprof -base OUT/alloc-base.pprof OUT/alloc.pprof
go tool pprof -top -cum OUT/cpu.pprof
```

`--benchmark-profiles=false` skips all three.

## Per-phase attribution

`Session.PhaseObserver` is a host-side boundary notification called once as each
of the twelve phases completes, carrying the phase name and the tick label. The
session never reads a clock; the **host** stamps the time
`[I6]`. The observer draws no random number and writes no authoritative field,
so a run with it installed produces the same tick sequence as one without —
which the report proves by carrying the same fingerprints. `--phase-timing=false`
leaves it nil, and the nil test is the whole cost when unset.

Everything after the last phase boundary — the sharing tail, result evaluation,
publication and the once-per-pump executor tail — is attributed to a final row
named `tail-sharing-result-publication`.

Thirteen wall-clock reads per tick is real overhead against a tick measured in
milliseconds; take the CPU profile with phase timing on or off consistently
across the runs being compared.

## Reading `sim-bench.json`

- `runtime` — Go version, platform, CPU count and the source revision. A binary
  built inside a git worktree carries no VCS stamp of its own, so `tools/sim-bench`
  passes `NANOLATHE_BENCH_REVISION`; set it yourself when running a prebuilt
  binary directly, or the field is empty.
- `scene` — version, map, terrain size, the chosen battle centre and its
  flatness score, and each army's placement and counts.
- `initial_fingerprint` / `warm_fingerprint` / `final_fingerprint` — the
  versioned partial state fingerprint at composition, at window open and at
  window close, plus the two RNG draw totals. Two runs of one binary at one
  seed must agree on all three; if they do not, the run is not comparable and
  the divergence is the finding.
- `ms_per_tick_p50/p95/p99/max`, `ticks_per_second`, `tick_ms` — the window's
  timings, and the whole per-tick series for a distribution or a stall hunt.
- `phases` — millseconds per tick and share of the window per phase.
- `gc` — collections, pause total, and allocated bytes and objects, every one a
  **delta between two `MemStats` reads** taken as the window opens and closes.
  The two heap figures are instantaneous gauges. `gc_cpu_fraction_lifetime` is
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

Compare the same scene version, map, seed, window and phase-timing setting, on
a quiet machine, from binaries built before the runs were queued. Report the
median, p95 and max together with the census, never a median alone: this scene's
cost falls as units die, so a run that killed more units looks faster.

A change that alters `final_fingerprint` altered the simulation, not only its
speed. That is a behaviour change and needs justifying as one, separately from
any timing it produced.

This is an opt-in development and regression probe, not a CI threshold. Keep
baseline outputs outside the repository.
