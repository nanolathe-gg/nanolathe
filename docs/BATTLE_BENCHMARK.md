# Live battle benchmark

This benchmark measures simulation **and** presentation together, through a real
window. To measure the authoritative tick on its own — no window, no renderer,
no audio device — use the displayless
[simulation-cost benchmark](SIM_BENCHMARK.md) instead; it takes the same host
lock, so the two never run at once.

Run from a worktree with the retail installation available. The wrapper bounds
Go runtime workers and build jobs to two, honoring `GOMAXPROCS` and
`NANOLATHE_TEST_P`. Compilation and native benchmark execution each take the
host lock exclusively, so they wait for running verification gates and gates
wait for them ([ARCHITECTURE §6](ARCHITECTURE.md#6-verification)). Match the runtime
worker setting as well as scene metadata when comparing runs. The existing
180-frame measurement is already six seconds at 30 draws/s; retain its warm-up
and census rather than shortening it for every change. Authoritative-tick-only
changes use the quick simulation benchmark instead of both windowed renderers.

The benchmark uses **Expanded Confluence**, seed 7, at 1920×1080. Its shoreline
anchor at (7296, 10592) frames sea, beach and forested land; 29.3% of the installed
map's terrain samples lie below sea level. This is the sole battle fixture
(scene version 5), replacing the Great Divide scene. It requires this map;
`--map` cannot substitute an unmeasured coast.

Each side stages 120 land units, 24 naval units (including eight submarines),
16 aircraft and 20 buildings, plus the normal distant starting commanders.
Four producing labs per side queue ten Peewees/AKs with inherited patrol rallies.
The four water structures per side are a tidal generator, underwater metal
storage, floating rocket tower and torpedo launcher. The land roster includes
Merls, mobile artillery, Fidos, heavy tanks and Core rocket units; battleships,
cruisers and destroyers add offshore fire. Submarines occupy the leading naval
columns so surface ships stopping to fire do not block their approach.

Aircraft start at their authored cruise altitude through `CreateWithMoverMode`
with airborne mode 2, then receive `VTOL_Patrol` orders. The existing movement
bootstrap installs the matching flight and occupancy state; no takeoff wait or
flight-physics change is needed [04 §10.1][04 R-AIR-01 §6]. Half start on return
legs over the opposing formation. Land and naval units also patrol through the
opposing formation instead of becoming idle at a one-way destination. Normal
combat and AI may replace those orders, and flight turning radii can take
survivors outside the viewport.

Normal player visibility remains enabled. Aircraft LOS reveals the fight while
leaving fog around its edges, exercising exploration and current-visibility
presentation along with water, beach effects and reflections. Nothing forces
firing, feature ignition, resources, health or synthetic effects. Natural weapon
impacts ignite the authored trees; ignition, smoke, spread and burnout run
ordinary feature behavior.

Buildings are placed first. Every ground/water slot searches a deterministic
192-pixel neighbourhood on a 16-pixel grid, checks the compiled footprint,
terrain profile and building yard, and reserves its rectangle against overlap.
A missing definition or unplaceable slot fails the run rather than silently
reducing the workload. Ships use authored waterline draft [04 R-MOV-01 §9].
This setup is a benchmark fixture, not a new gameplay placement rule.

Census fields record surviving airborne units, surface ships, submarines and
water buildings, plus each group's in-view anchors. Submarines are the explicitly
staged ARM/Core submarine cohort, not a guessed geometric depth threshold.
`in_view_units`, `in_view_projectiles` and `in_view_effects` are also projected
anchor counts, not pixel coverage, fog visibility or proof of a reflection.
`view_fog_cells`, `fogged_view_cells` and `unexplored_view_cells` count projected
fog-cell centres inside the viewport, a coverage proxy rather than exact pixels.
Inspect captures and activity as well as frame times. Preserve matching scene,
camera, draw rate, zoom, lead-in, visibility and display settings for regression
comparisons. Comparisons with archived Great Divide captures measure different
workloads, not an isolated cost of water; this larger map also has a different
map-wide feature load and fewer burning trees in the measured view.

## Running and reading the benchmark

```
tools/battle-bench --battle-benchmark=/tmp/battle-classic --renderer=classic
tools/battle-bench --battle-benchmark=/tmp/battle-modern --renderer=modern
tools/battle-bench-report /tmp/battle-classic /tmp/battle-modern
```

Each output directory must be new. Use `--root` for another retail install,
`--seed` for a different deterministic battle, `--benchmark-frames` to change
180 measured draws, `--benchmark-pre-ticks` to change the default 300 simulation
ticks before the window opens (ten simulated seconds), and `--benchmark-factories=false` to disable factory orders. Assets are not embedded or committed. Run cases
sequentially without concurrent builds, tests or other performance workloads.

`--zoom 2` runs the same scene in the detail view: the same simulation from
twice the linear magnification (docs/DESIGN_GPU_RENDERER.md §14). Classic accepts only
`--zoom 1` or `--zoom 2`; modern also accepts arbitrary fractional factors.
Use native and detail for the shared renderer comparison. Unset zoom is native
1× for both renderers, matching windowed play at every resolution. A scene's
scale is therefore explicit or native. The scale is applied after the scene's
camera jump, about the viewport centre, so the same army is framed;
`--auto-remaster=false` runs the detail view on nearest-doubled art instead of
the load-time remaster's. Both are recorded in `frames.json` as `zoom` and
`auto_remaster`, and a run is comparable only with another at the same scale —
the detail view is a different amount of pixel work, not a different scene. The
first `--zoom 2` run of a map pays the remaster (seconds, before the window
opens and outside every measurement); later runs read its cache.

Benchmark version 2 uses `--benchmark-tps` as the target draw rate: 30 by default,
or 60 or 120. The simulation rate is fixed at 30 ticks per simulated second,
with one authoritative step per 1, 2 or 4 draws respectively. Modern interpolates
at 60 and 120 draws/s, using tick fractions 0 and 1/2, or 0, 1/4, 1/2 and 3/4;
classic holds the committed tick on the intervening draws. Both run the same
authoritative tick sequence. If drawing falls behind the target, simulation
advances more slowly in wall time.

The visible window keeps VSync enabled and continues when unfocused. Ebitengine
runs with `SetTPS(SyncWithFPS)`; `Update` only checks completion and errors,
and every `Draw` callback executes benchmark work. An explicit host deadline
pacer waits before that work and rebases after an overrun, avoiding catch-up
bursts. The target rate is a host work cadence, not a guarantee of display
scanout timing. Compare runs at the same target rate.

This changes the earlier 60 TPS benchmark, which advanced one simulation tick
per draw and therefore ran the battle twice as fast as a 30 TPS run. Earlier
runs also gated measured draws through Ebitengine's update scheduler. Version 2
separates that scheduler from the benchmark's pacing; its timings are not
directly comparable with version 1, independently of `scene_version`.

`frames.json` records benchmark and scene versions, seed, map, renderer, view
scale, gameplay mode, display options, runtime and build information, per-frame timings and
feature census. Version 2 metadata includes `benchmark_version=2`,
`simulation_tps=30`, `draws_per_tick=TPS/30`, `warmup_draws=TPS*2`,
`pacing="draw-deadline"`, `gpu_timing_available=false` and
`present_timing_available=false`. Modern rows also
carry the executor's per-frame counters, including `Phases`, `Passes`, `Draws`,
`Vertices`, and `PointPixels`/`PointQuads`/`PointPlanes` — the screen pixels the
frame's lit point batches covered, the device quads they compiled into and the
lit point plane regions they committed through
(docs/DESIGN_GPU_RENDERER.md §13.8). The three together say whether the point
layer is paying per pixel or per plane, which is the first thing to check when
modern `Submit` moves. They also carry the model lane counters of
docs/DESIGN_GPU_RENDERER.md §22 — `DirectSubjects`, `DirectShadows`,
`DirectOverflow`, `DirectAtlasRows` (over every page), `DirectPages` (a
second page opening is the first sign a detail-view scene has outgrown one)
and `Silhouettes` (shadows committed from their body's own raster, within
`Shadows`; `DirectShadows` is then the structures' projected shadows alone)
— and, from older runs, the retired slot
stage's `SlotsReused`, `SlotsRasterized`,
`SlotsResident`, `SlotEvictions` and `SlotOverflows` — which the report
summarizes as a reuse share: how many of the frame's model subjects kept the
slot an earlier frame rasterized rather than being analysed and drawn again.
`ShadowSlotsReused` and `ShadowSlotsRasterized` split the shadow lane out of the
first two (§13.12 "Shadows — contract P4") and the report gives it a share of its
own: shadows are about two fifths of a battle frame's subjects, so a collapse
there is the first thing to check when the overall share falls.
`RasterPixels` and `SlotPages` count only what this frame rasterized, so they
fall with that share. A reuse share that collapses, or a rising eviction or
overflow count, means the page is under pressure and the frame is paying the
cold cost again. `cpu.pprof`,
`alloc-base.pprof` and `alloc.pprof` cover the measurement window; inspect allocation
deltas with `go tool pprof -base OUTPUT/alloc-base.pprof OUTPUT/alloc.pprof`.
`battle.png` is captured after timing. `factory-blockers.json` reports foreign
movement-grid occupants in the factories' checked yard-opening cells. Factory
queue phase, stance and target are included in each census. Compare the same scene version/settings;
factory production changes RNG consumption and battle evolution, so old captures
and exact stall tick numbers are not the new scene's baseline.

The saved diagnostics support offline investigation of the measured workload:

- `cpu.pprof` samples CPU execution during the window. Flat function costs
  attribute samples to that function; cumulative costs include callees. Summing
  cumulative costs across callers and callees double-counts the same samples.
- `alloc-base.pprof` and `alloc.pprof` are cumulative allocation profiles at the
  window boundaries. Subtract the baseline to inspect allocation space and
  object counts attributable to the window. A GC outside each boundary flushes
  Go's delayed profile data, including runs with no GC during measurement.
  These sampled deltas also include profile setup/teardown allocations;
  `memory.json` and `frames.json` retain the exact measured counter deltas.
  Neither gives exact allocation accounting per unit or effect.
- `heap.pprof` samples the live Go heap after measurement and a forced GC.
  That collection is outside timing. The profile describes retained memory at
  this later point, not all allocations made during the measured window.
- `memory.json` records `runtime.MemStats` before and after measurement and an
  `after_gc` snapshot. GPU image memory before and after the window is reported
  separately from Go memory; it is not GPU execution timing. Window counters
  are captured before final dump/profile-output allocations. The post-GC
  snapshot precedes final profile and JSON writes.
- `goroutine.txt` is an end snapshot of goroutine stacks. It can show what was
  waiting at that instant, but does not measure CPU time or wait duration.
- `state-start/` and `state-end/` contain `session.json`, `units.jsonl` and the
  existing session debug snapshots. The start snapshot follows renderer warmup
  and precedes the memory/profiling baseline; the end snapshot follows
  measurement. Unit records come from `Session.VisitDebugUnits`, including
  orders, COB and movement state. These are endpoints, not a complete history
  of intervening transitions: an unchanged order or position alone does not
  prove that a unit stalled throughout the window.
- `phases.json` records measured simulation tick count and per-phase total,
  mean and maximum elapsed time. The observer runs only during measurement;
  its clock reads at phase boundaries add overhead. `phase_timing=true` in
  metadata identifies that instrumentation, and comparisons must use matching
  instrumentation settings.

Run the offline analyzer against saved directories without launching a battle:

```
tools/battle-bench-analyze /tmp/battle-classic /tmp/battle-modern
```

It writes `analysis.json` and `analysis.md` for each input directory, combining
timing/workload summaries, CPU flat and cumulative function costs, allocation
space/object deltas via `pprof -base`, heap in-use data, and phase/state
summaries. For direct profile inspection:

```
go tool pprof -top OUTPUT/cpu.pprof
go tool pprof -top -cum OUTPUT/cpu.pprof
go tool pprof -top -sample_index=alloc_space -base OUTPUT/alloc-base.pprof OUTPUT/alloc.pprof
go tool pprof -top -sample_index=alloc_objects -base OUTPUT/alloc-base.pprof OUTPUT/alloc.pprof
go tool pprof -top -sample_index=inuse_space OUTPUT/heap.pprof
```

Analysis values repeat exactly for the same saved inputs. This makes a saved
run reproducible to inspect; timings and sampled profiles naturally vary
between new measurements. Endpoint comparisons and profile samples remain
diagnostic evidence, not exact per-entity cost attribution or proof of a stall.

There are 300 pre-window simulation steps by default, followed by two simulated
seconds of renderer warmup: 60, 120 or 240 draws at the selected target rate,
always advancing 60 authoritative ticks. The pre-window steps run without
drawing or wall-clock pacing; sound and status events are drained each step to
avoid replaying the whole lead-in on
the first displayed frame. Renderer warmup remains necessary to populate draw
caches before profiling. Use `--benchmark-pre-ticks=0` to inspect the opening;
keep the lead-in fixed when comparing revisions rather than waiting for a
variable combat trigger. Version 2 steps on phase zero of each draw group and
records `SimulationStep` and `TickPhase` in every row. This isolates comparable
tick sequences but does not exercise the ordinary interactive catch-up scheduler
or live user input.
The seed fixes simulation streams; authored content, settings and code revision
also matter. Camera origins and shake status are recorded with each census.

Scene version 5 has a different workload from the earlier Great Divide and Ashap scenes. The
report warns when scene metadata differs across input directories, and when
measured combat, sprite features, fire or requested construction is absent.
`features` and `sprite_features` count live committed features across the map;
`burning_features` counts burning sprite features. The `in_view_sprite_features`
and `in_view_burning_features` counters count projected feature anchors inside
the battle viewport. They are geometric workload proxies, not pixel counts or
proof of visibility through fog and overlapping sprites; inspect `battle.png`
as well. `moving_units` counts surviving instances whose committed position changed since
the previous simulation tick and `damaged_units` counts
complete units below maximum health. Missing counters in older reports are
unknown, not zero.

- `Step`: host viewer step, including simulation and publication. In version 2
  this is zero on nonstep draws; the report computes its statistics only from
  rows with `SimulationStep=true`.
- `Record`: draw preparation/recording on the **game goroutine**; classic also
  includes CPU raster/replay. At 60 and 120 draws/s the modern path runs the
  record/submit pipeline of docs/DESIGN_GPU_RENDERER.md §13.10, which records the next frame
  during the previous frame's flush and present, so `Record` there is the wait
  to join that record plus any synchronous re-record — microseconds on a hit,
  the whole record on a miss. It is the critical-path figure either way, and
  the one to compare against a run without the pipeline.
- `PreRecord`: the wall time the consumed pre-record spent on the pipeline
  goroutine, overlapped with the previous frame's flush. It is work that
  happened, not work on the critical path: it is the number to compare against
  an earlier run's `Record`, and it is absent from a run with no pipeline hits.
- `Hit`: whether this frame presented a pre-recorded list. The report prints the
  hit share and splits `Record` and valid `Cadence` intervals by it. The potential
  hit share is 50% at 60 draws/s and 75% at 120: phase zero publishes a tick,
  which a pre-record may not cross. Actual hits depend on prediction validity
  and completion. At 30 draws/s every draw steps, so there are no pipeline hits.
- `CPURender`: classic preparation plus raster/replay, excluding GPU upload.
- `Submit`: host CPU time enqueueing rendering/upload and final draw commands.
  Ebitengine can defer driver encoding and submission until after this callback;
  this does not measure GPU execution or completion.
- `DrawWork`: the callback's complete host work after pacing, including census
  collection and pre-record launch as well as the step, record and submit work.
- `PaceWait`: intentional host pacing sleep before this draw's work.
- `OutsideDraw`: elapsed time from the previous callback's return to this
  callback's entry. It includes deferred driver work, queue and display waits,
  and host scheduling. It is not GPU duration.
- `Cadence`: version 2 intervals between consecutive starts of work, after
  pacing and before simulation. Approximately,
  `Cadence[i] = DrawWork[i-1] + OutsideDraw[i] + PaceWait[i]`.
  The first measured row has `CadenceValid=false`: its interval crosses the
  profiling setup boundary and is excluded from both `Cadence` and
  `OutsideDraw` statistics, including the pipeline splits. Its other timings
  and census remain included. No artificial zero interval is used. Older
  reports without this flag retain all their original interval samples; their
  cadence timestamps were taken after simulation.

The report's target share counts valid intervals no longer than 105% of the
target period (33.3, 16.7 or 8.3 ms before that tolerance). Host cadence
throughput is the valid interval count divided by their total duration. Neither
quantity measures actual display scanout. With only one measured row, version 2
has no valid cadence interval and reports those statistics as unavailable.

GPU execution and actual presentation timestamps are unavailable through the
public Ebitengine API used here; the report states that explicitly. Small
`Submit` values plus long cadence intervals do not establish a GPU bottleneck:
deferred CPU driver work, scheduling and display pacing can also occupy that
time. Separating these causes requires a driver/presentation timeline and GPU
timestamps from external instrumentation. Readback, PNG encoding and profile
finalization remain outside measured draw work.

For changes to simulation, movement, construction, model/effect presentation,
or renderer storage/batching, run both renderers and review timings and captures.
Check that the census actually contains sprite features, in-view burning trees,
moving/damaged units, projectiles, effects, nanoframes,
nanolathe events and shake. The factories can be blocked or starved according
to ordinary gameplay; no animation or nanoframe is synthesized for the benchmark.
For performance decisions, repeat matching runs and compare their spread;
one short measurement is a diagnostic sample. Keep benchmark versions,
30/60/120 draw targets and zoom comparisons separate. With the default 180
measured draws, version 2 covers 180, 90 or 45 authoritative ticks respectively;
the nominal measured durations are six, three or 1.5 seconds. Matching tick
windows across rates requires proportionally more draws, but the presentation
work still differs. A longer `--benchmark-frames` run is useful for cache
pressure and burnout, but verify the battle has not become quiet by its end.
This is an opt-in development/regression probe, not a timing threshold in CI.
Keep baseline outputs outside the repository and report median, p95 and maxima
alongside the exact workload. Pixel equality across CPU/GPU is not required;
compare each renderer against its own baseline when no visual change is intended.

Benchmark invocations automatically serialize for the same OS user on this
host, across worktrees and output directories, including direct binary and
`go run` launches. A busy invocation prints one waiting message and blocks
before opening assets or creating its output directory. The lock covers setup,
warm-up, measurement and artifact writing. OS file locking releases it on exit
or crash; the persistent `nanolathe/battle-bench.lock` file in the user cache
is not a stale-lock marker and must not be deleted while runs are queued.
Wait time is outside all measurements. Lock acquisition does not promise FIFO
ordering among several waiters. Normal gameplay does not take the lock.

Compilation by `go run` precedes executable startup and is outside the lock.
Build binaries before queueing runs when comparing performance, and avoid
unrelated builds or other performance workloads during measurement. Older
binaries without locking cannot participate in this serialization.
