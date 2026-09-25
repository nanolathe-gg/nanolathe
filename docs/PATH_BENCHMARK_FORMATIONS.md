# Alt+drag formation benchmark fixtures

These are authored command experiments for the headless path benchmark. They
exercise the pure Modern command producer described in
[DESIGN_INTERFACE_HUD_INPUT §3.11](DESIGN_INTERFACE_HUD_INPUT.md#311-modern-drag-commands)
and feed its assigned destinations to the authoritative session. They do not
claim retail input or movement behavior. The benchmark contract is in
[PATH_BENCHMARK](PATH_BENCHMARK.md); the scenario motivation is in
[PATHFINDING_EVALUATION_PLAN](PATHFINDING_EVALUATION_PLAN.md#alt-drag-and-physically-possible-expectations).

The export test calls production `dragSamplePath` and
`dragAssignDestinations` from `battle_drag_geometry.go`. It can be run without
the window package or a display:

```sh
env GOMAXPROCS=2 \
  NANOLATHE_PATH_BENCH_FORMATIONS=/private/tmp/nanolathe-formations.json \
  go test -tags 'pathbench retail' \
  cmd/nanolathe/battle_drag_geometry.go \
  cmd/nanolathe/path_bench_formation_test.go \
  -run '^TestPathBenchFormationExport$' -count=1
```

The session suite reads the same file at initialization. A direct smoke of
every case, including three authoritative ticks and the exact assigned orders,
is:

```sh
env GOMAXPROCS=2 \
  NANOLATHE_RETAIL_ASSETS=/Users/daniel/TotalAnnihilation \
  NANOLATHE_PATH_BENCH_FORMATIONS=/private/tmp/nanolathe-formations.json \
  NANOLATHE_PATH_BENCH_FORMATION_SMOKE=1 \
  go test -tags 'pathbench retail' ./internal/session \
  -run '^TestPathBenchFormationSmoke$' -count=1
```

The same command with `NANOLATHE_PATH_BENCH_FORMATION_WINDOW=1` and
`-run '^TestPathBenchFormationLineWindow$' -v` checks that every actor in the
eight-unit straight line completes its Move order within the fixture's full
tick window and enters its reporting radius. This is a workload sanity check,
not a retail timing claim.

The outer runner generates this file before launching the session suite. It
retains the file outside the repository with the results. Both tests require
`pathbench && retail` tags; the generator only needs production geometry, and
the session smoke needs the retail asset install. `GOCACHE` may be set to a
writable external directory on a restricted host.

## Version 1 JSON

The top-level object has `version: 1` and `cases`, an array of records. Each
record has these fields with exact JSON names and units:

| Field | Meaning |
|---|---|
| `id`, `family`, `description` | Stable scenario ID, `alt_drag`, and intent. The session case ID is `formation_<id>_<size>`. |
| `size`, `ticks` | Commanded actor count and fixed observation window in simulation ticks. |
| `terrain`, `width_cells`, `height_cells` | Authored terrain variant and dimensions in 16 world unit cells. |
| `actors` | Ordered records `{key, start_cell: [x,z], assigned_world: [x,z]}`. Keys resolve to unmodified retail definitions. Coordinates are signed whole units. Actor order is the command producer's input order. |
| `stroke_world` | Ordered path vertices in whole world coordinates. These are the authored gesture trace, with both endpoints. |
| `sampled_world` | Arc-length sampled points returned by the production sampler, before assignment. |
| `assignment_ns` | Five raw host elapsed nanosecond samples of production assignment. Input construction and JSON export are outside each sample. |
| `assignment_allocs`, `assignment_bytes` | Allocation count per call from `testing.AllocsPerRun(1)` and allocated bytes from the first timed call's `runtime.MemStats` delta. They are host diagnostics, separate from simulation tick cost. |
| `total_direct_distance_world` | Sum of Euclidean actor-to-assigned-goal distances in world units. This is the producer's straight-line assignment objective, not a route estimate. |
| `duplicate_goals` | Number of assigned goals with exact duplicate world coordinates after their first occurrence. |
| `identity_sha256` | SHA-256 of Go `encoding/json` marshaling of `{Actors, Stroke, Sampled}` using the schema types in the producer and consumer; timing is excluded. |

At scene construction, each actor is placed through `pbAdd`, then receives
one `pbMoveWorld` order with `AssignedPosition=true` and the raw 16.16
conversion of `assigned_world`. The session validates count, identity, terrain
bounds, legal starts, and expected endpoint passability. Its `Inputs` also
records pairwise overlap of assigned goal footprints using the retail unit
movement profiles and the production collision anchor quantization, and the
count of terrain-impassable assigned goals. The straight and long lines require
zero such overlap. These are
diagnostics: a duplicate point or overlapping footprint does not imply two
units can occupy it. The stroke defines destinations, not routes.
The session's deterministic `Inputs` records geometry, goals, identity and
terrain statistics. Host assignment samples stay in the exported JSON and do
not enter the input hash, so identical command traces compare across runs.

## Case selection and controls

`line` and `reverse` have the same unit starts and identical assigned goals
at sizes 8, 64, and 256; only stroke order differs. The generator asserts this
pairing. `curve` and `zigzag` exercise arc-length sampling at 8 and 64.
`rotated_crossing` and `crossed` change the initial placement while retaining
a line destination. `long_line` spreads destinations farther apart.
`mixed_footprint` alternates retail `armflea` and `armflash` at sizes 8 and 64.

`short_overfull` intentionally compresses every group onto a two-cell stroke;
its destination capacity is insufficient. `blocked_endpoint`,
`water_endpoint`, and `slope_endpoint` put the release endpoint on an authored
impassable patch; the session asserts that at least one assigned goal is
impassable. The water case uses retail `armflash` on land above a deep water
patch. `choke_reform` places a wall with one opening between the starting group
and the destination line, including a 256-unit case. A blocked or overfull
control must be reported separately from reachable failures. The synthetic
terrain contains no features, fog-driven command gate, or user interface
capture state.

The producer fixture starts after the UI has captured a valid move gesture and
selected mobile actors. It exercises production sampling and assignment, but
does not drive mouse hit testing, screen-to-world projection, selection,
modifiers, queue overlays, cancellation, or rendering. These cases use Modern
command geometry; the benchmark may run their resulting authoritative orders
under more than one gameplay ruleset to compare movement behavior.
