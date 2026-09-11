# Live battle benchmark

This benchmark measures simulation **and** presentation together, through a real
window. To measure the authoritative tick on its own — no window, no renderer,
no audio device — use the displayless
[simulation-cost benchmark](SIM_BENCHMARK.md) instead; it takes the same host
lock, so the two never run at once.

Run from a worktree with the retail installation available. The default scene
is Great Divide, seed 7, 1920×1080, 160 armed mobile units and 16 buildings per side,
plus the normal starting commanders. Labs queue ten Peewees/AKs through normal
factory production. Units receive movement orders; COB, pathfinding, combat,
weapons, explosions, shake, construction and both renderers run production code.
The ten mobile types on each side include tanks, light/rocket/artillery kbots,
heavy assault units, fighters and bombers; unarmed Peeper/Fink scouts are gone.
Each army occupies eight columns and twenty rows at 48-pixel spacing, facing
across the same twenty movement lanes. This gives combat a tall front rather
than packing units into a short horizontal strip.

The fixture deterministically searches the map for the least height variation
across a 1600×1040 area covering both formations, their approach lanes and the
rear buildings. It tries centres every 32 pixels, checks every 16-pixel terrain vertex within
the area, rejects water, and keeps row-major
ties. This is a benchmark placement heuristic, not a retail passability rule;
normal pathfinding still handles trees and other blockers. A map without a dry
area large enough returns an error. The selected centre and sampled terrain
relief are recorded so a hillside cannot silently become the benchmark baseline.
Natural weapon impacts ignite the authored forest; ignition, smoke, spread and
burnout run ordinary feature behavior. Fire is not forced or refreshed by the
fixture, so custom maps, seeds or warmup durations need their census checked.

Building spacing derives from the largest authored footprint to keep neighboring
solar collectors out of factory yards. The fixture places the initial units directly; it is not a normal skirmish opening.

```
tools/battle-bench --battle-benchmark=/tmp/battle-classic --renderer=classic
tools/battle-bench --battle-benchmark=/tmp/battle-modern --renderer=modern
tools/battle-bench-report /tmp/battle-classic /tmp/battle-modern
```

Each output directory must be new. Use `--root` for another retail install,
`--seed` or `--map` for a different scenario, `--benchmark-frames` to change
180 measured draws, `--benchmark-pre-ticks` to change the default 300 simulation
ticks before the window opens (ten simulated seconds), and `--benchmark-factories=false` to disable factory orders. Assets are not embedded or committed. Run cases
sequentially without concurrent builds, tests or other performance workloads.

`--zoom 2` runs the same scene in the detail view: the same simulation from
twice the pixels (docs/DESIGN_GPU_RENDERER.md §14); `--zoom 1.5` is the 1.5×
step. The benchmark does not take the window's resolution default — unset is
native here, so a scene's scale is always the one on its command line. The
scale is applied after the scene's camera jump, about the viewport centre, so
the same army is framed;
`--auto-remaster=false` runs the detail view on nearest-doubled art instead of
the load-time remaster's. Both are recorded in `frames.json` as `zoom` and
`auto_remaster`, and a run is comparable only with another at the same scale —
the detail view is a different amount of pixel work, not a different scene. The
first `--zoom 2` run of a map pays the remaster (seconds, before the window
opens and outside every measurement); later runs read its cache.
The visible window runs with VSync at `--benchmark-tps` draws per second (30, the
retail cadence, by default; 60 an intermediate rate; 120 the Enhanced
presentation target) and continues when unfocused. At 30 and 60 one simulation
step runs per draw, so a 60 TPS run advances the battle twice as fast in wall
time. At 120 one authoritative step runs every fourth draw and the four frames
are presented at tick fractions 0, 1/4, 1/2 and 3/4, so the report measures the
interpolated presentation of docs/DESIGN_GPU_RENDERER.md §13.5 at the retail
simulation rate; only the modern renderer blends, classic keeps committed-tick
sampling at every rate. Compare runs at one rate.

`frames.json` records scene version, seed, map, renderer, view scale, display options, runtime
and build information, per-frame timings and feature census. Modern rows also
carry the executor's per-frame counters, including `Phases`, `Passes`, `Draws`,
`Vertices`, and `PointPixels`/`PointQuads`/`PointPlanes` — the screen pixels the
frame's lit point batches covered, the device quads they compiled into and the
lit point plane regions they committed through
(docs/DESIGN_GPU_RENDERER.md §13.8). The three together say whether the point
layer is paying per pixel or per plane, which is the first thing to check when
modern `Submit` moves. They also carry the persistent model slot counters of
docs/DESIGN_GPU_RENDERER.md §13.12 — `SlotsReused`, `SlotsRasterized`,
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

There are 300 pre-window simulation steps and 60 renderer warm-up draws by
default. The pre-window steps run without drawing or wall-clock pacing; sound
and status events are drained each step to avoid replaying the whole lead-in on
the first displayed frame. Renderer warmup remains necessary to populate draw
caches before profiling. Use `--benchmark-pre-ticks=0` to inspect the opening;
keep the lead-in fixed when comparing revisions rather than waiting for a
variable combat trigger. At 30 and 60 TPS
exactly one simulation step runs per draw; at 120 one runs every fourth draw.
This isolates comparable tick sequences but does
not exercise the ordinary interactive catch-up scheduler or live user input.
The seed fixes simulation streams; authored content, settings and code revision
also matter. Camera origins and shake status are recorded with each census.

Scene version 4 has a different workload from the earlier Ashap scene. The
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

- `Step`: host viewer step, including simulation and publication.
- `Record`: draw preparation/recording on the **game goroutine**; classic also
  includes CPU raster/replay. At 120 TPS the modern path runs the record/submit
  pipeline of docs/DESIGN_GPU_RENDERER.md §13.10, which records the next frame
  during the previous frame's flush and present, so `Record` there is the wait
  to join that record plus any synchronous re-record — microseconds on a hit,
  the whole record on a miss. It is the critical-path figure either way, and
  the one to compare against a run without the pipeline.
- `PreRecord`: the wall time the consumed pre-record spent on the pipeline
  goroutine, overlapped with the previous frame's flush. It is work that
  happened, not work on the critical path: it is the number to compare against
  an earlier run's `Record`, and it is absent from a run with no pipeline hits.
- `Hit`: whether this frame presented a pre-recorded list. The report prints the
  hit share and splits `Record` and `Cadence` by it. At 120 TPS the ceiling is
  75% — the fourth draw of each group publishes a tick, which a pre-record may
  not cross — so a lower share means predictions are missing, and a share of
  zero means the pipeline is not running (30 and 60 TPS step on every draw, so
  they never launch one).
- `CPURender`: classic preparation plus raster/replay, excluding GPU upload.
- `Submit`: CPU time issuing GPU execution/upload and final draw commands.
  It is not a GPU completion timestamp; do not add it to cadence as GPU work.
- `Cadence`: intervals measured after each simulation step, including pacing.
  With VSync the cadence quantizes to whole display refreshes, so a run whose
  CPU and GPU work fit the period sits at the floor (33.3 ms at 30, 16.7 ms at
  60, 8.3 ms at 120) and one that does not alternates between one and two
  refreshes. The
  report's "on cadence" share is the fraction of frames at the floor; it is the
  first figure to compare when the medians are at the floor already.
- Modern is GPU-bound before it is CPU-bound: a 60 TPS run whose `Submit` is
  well under the period and whose cadence still leaves the floor is waiting on
  the device, and the executor's pass count (destination switches) is the cost
  to cut, not fragment work.
- Readback, PNG encoding and profile finalization are outside measured frame work.
  Profile/counter setup can affect the first measured cadence sample.

For changes to simulation, movement, construction, model/effect presentation,
or renderer storage/batching, run both renderers and review timings and captures.
Check that the census actually contains sprite features, in-view burning trees,
moving/damaged units, projectiles, effects, nanoframes,
nanolathe events and shake. The factories can be blocked or starved according
to ordinary gameplay; no animation or nanoframe is synthesized for the benchmark.
For performance decisions, repeat matching runs and compare their spread;
one six-second measurement is a diagnostic sample. Keep 30/60/120 TPS and
zoom comparisons separate: they cover different presentation work and tick
windows. A longer `--benchmark-frames` run is useful for cache pressure and
burnout, but verify the battle has not become quiet by its end.
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
