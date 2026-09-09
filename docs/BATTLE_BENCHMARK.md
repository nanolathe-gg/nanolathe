# Live battle benchmark

Run from a worktree with the retail installation available. The default scene
is Ashap Plateau, seed 7, 1920×1080, 80 mobile units and 16 buildings per side,
plus the normal starting commanders. Labs queue ten Peewees/AKs through normal
factory production. Units receive movement orders; COB, pathfinding, combat,
weapons, explosions, shake, construction and both renderers run production code.
Building spacing derives from the largest authored footprint to keep neighboring
solar collectors out of factory yards. The fixture places the initial units directly; it is not a normal skirmish opening.

```
tools/battle-bench --battle-benchmark=/tmp/battle-classic --renderer=classic
tools/battle-bench --battle-benchmark=/tmp/battle-modern --renderer=modern
tools/battle-bench-report /tmp/battle-classic /tmp/battle-modern
```

Each output directory must be new. Use `--root` for another retail install,
`--seed` or `--map` for a different scenario, `--benchmark-frames` to change
180 measured draws, and `--benchmark-factories=false` for the earlier battle
without factory orders. Assets are not embedded or committed. Run cases
sequentially without concurrent builds, tests or other performance workloads.

`--zoom 2` runs the same scene in the detail view: the same simulation from
twice the pixels (docs/DESIGN_GPU_RENDERER.md §14). The scale is applied after
the scene's camera jump, about the viewport centre, so the same army is framed;
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
and build information, per-frame timings and feature census. `cpu.pprof`,
`alloc-base.pprof` and `alloc.pprof` cover the measurement window; inspect allocation
deltas with `go tool pprof -base OUTPUT/alloc-base.pprof OUTPUT/alloc.pprof`.
`battle.png` is captured after timing. `factory-blockers.json` reports foreign
movement-grid occupants in the factories' checked yard-opening cells. Factory
queue phase, stance and target are included in each census. Compare the same scene version/settings;
factory production changes RNG consumption and battle evolution, so old captures
and exact stall tick numbers are not the new scene's baseline.

There are 30 pre-window simulation steps and 60 warm-up draws. At 30 and 60 TPS
exactly one simulation step runs per draw; at 120 one runs every fourth draw.
This isolates comparable tick sequences but does
not exercise the ordinary interactive catch-up scheduler or live user input.
The seed fixes simulation streams; authored content, settings and code revision
also matter. Camera origins and shake status are recorded with each census.

- `Step`: host viewer step, including simulation and publication.
- `Record`: draw preparation/recording; classic also includes CPU raster/replay.
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
Check that the census actually contains projectiles, effects, nanoframes,
nanolathe events and shake. The factories can be blocked or starved according
to ordinary gameplay; no animation or nanoframe is synthesized for the benchmark.
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
