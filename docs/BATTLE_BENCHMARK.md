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
The visible window runs at 30 TPS with VSync and continues when unfocused.

`frames.json` records scene version, seed, map, renderer, display options, runtime
and build information, per-frame timings and feature census. `cpu.pprof`,
`alloc-base.pprof` and `alloc.pprof` cover the measurement window; inspect allocation
deltas with `go tool pprof -base OUTPUT/alloc-base.pprof OUTPUT/alloc.pprof`.
`battle.png` is captured after timing. `factory-blockers.json` reports foreign
movement-grid occupants in the factories' checked yard-opening cells. Factory
queue phase, stance and target are included in each census. Compare the same scene version/settings;
factory production changes RNG consumption and battle evolution, so old captures
and exact stall tick numbers are not the new scene's baseline.

There are 30 pre-window simulation steps and 60 warm-up draws. Exactly one
simulation step runs per draw. This isolates comparable tick sequences but does
not exercise the ordinary interactive catch-up scheduler or live user input.
The seed fixes simulation streams; authored content, settings and code revision
also matter. Camera origins and shake status are recorded with each census.

- `Step`: host viewer step, including simulation and publication.
- `Record`: draw preparation/recording; classic also includes CPU raster/replay.
- `CPURender`: classic preparation plus raster/replay, excluding GPU upload.
- `Submit`: CPU time issuing GPU execution/upload and final draw commands.
  It is not a GPU completion timestamp; do not add it to cadence as GPU work.
- `Cadence`: intervals measured after each simulation step, including pacing.
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
