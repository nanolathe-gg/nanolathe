# Authored path benchmark: traffic and lifecycle cases

This catalog is an opt-in test workload for Nanolathe's current path and
movement implementation. It is **not** a retail executable observation or a
new gameplay rule. The fixture contract and runner fields are in
[PATH_BENCHMARK](PATH_BENCHMARK.md); the broader evaluation questions are in
[PATHFINDING_EVALUATION_PLAN](PATHFINDING_EVALUATION_PLAN.md). All cases use
synthetic flat terrain (height 20, sea level 0), retail unit definitions and
COBs, simulation/CRT seeds 7/11, ordinary session phases, and the requested
Modern or Strict 3.1 rule set. Void rectangles author corridor geometry before
session construction. No catalog entry is changed.

The case IDs and input traces are stable fixture names, not success criteria.
`pbMove` uses the human group command unless a case says otherwise; the runner
reads the installed group-adjusted destinations. A reporting radius measures
proximity and never substitutes for the order's own completion. Counts below
refer to commanded units unless passive blockers or factory products are named.

| IDs | Sizes | Authored conditions |
| --- | --- | --- |
| `traffic/open_flea`, `traffic/open_mixed`, `traffic/open_away` | 1/8/15/16/17/64/256 as listed in each case | Open eastbound and westbound clicks and four mixed retail ground types. Spawn facing is the retail create default; `open_away` changes travel direction without overriding heading. |
| `traffic/dense`, `traffic/spaced` | 8/16/64 each | Matched flea groups centered at `(40,40)` in the same 8-column layout and receiving the same `(102,48)` click. Footprints touch at stride 2; stride 4 leaves two free cells. This varies initial density without changing count, center, or command. |
| `traffic/heading_zero`, `traffic/heading_halfturn` | 1/8 | Same eastbound flea start and click with explicit initial heading 0 or 32768 installed consistently in unit, steering, and collision state before the first tick. |
| `traffic/control_flash`, `traffic/control_stump`, `traffic/control_ck`, `traffic/control_peep` | 1 each | A single retail type starts at the corresponding mixed or air/ground group cell and receives the same `(102,48)` click. These are per-type open controls; group offsets can alter an actual destination. The root runner has a flea control. |
| `traffic/choke_one`, `traffic/choke_two_opposing`, `traffic/choke_wide` | 1/8/16; 8/16/64; 8/16/64 | Full-height vertical void wall `x=[63,65)` with apertures two, four, or eight cells tall. A flea uses a 2×2 footprint, so those gaps admit one, two, or four footprints abreast. Opposing cohorts have separate owners and clicks; the nonlocal owner's orders enter through production queues. |
| `traffic/cross`, `traffic/merge`, `traffic/split`, `traffic/passing_bays` | 8/16/64 where listed | Perpendicular open crossing; two north/south inlet streams through a four-cell aperture; one stream splitting after the aperture; opposed traffic in a two-cell corridor with two 4×2 bays. Named half-open regions let the runner count passage. |
| `traffic/packed_goal`, `traffic/capacity_control`, `traffic/idle_blockers` | 8/16/64 where listed | Ordinary group click into open space; deliberately overfull assigned click at `(90,48)` in a two-cell entry lane; and 16 idle flea blockers around a destination. The capacity control is reachable from the west but cannot hold all actors on the assigned footprint at once. |
| `traffic/air_ground` | 8/16 | Alternating `armflea` and `armpeep` actors receive one click. Aircraft use their flight and occupancy path; ground congestion observations do not apply to them. |
| `traffic/waves` | 16/64/256/1500 | Three owners on a 192×160 map. Owner 0 uses human group Move; owners 1 and 2 receive explicit production order-queue Move records because human commands admit only the local owner. Every 150 ticks, each owner gets a long-distance reversal and its first actor gets a short request two cells from its then-current committed position. This is scripted multi-owner load, **not** the skirmish AI planner. |
| `lifecycle/stop_reverse`, `lifecycle/rapid_queue`, `lifecycle/patrol` | 8/16; 1/8; 8/16 | Stop at tick 45 then reverse at 50; replacement clicks at ticks 2/3 then queued waypoints at 4/5; and opposing crossing patrols. Queued/patrol radius visits do not imply order retirement. |
| `lifecycle/moving_targets` | 1 command group | Guard flea, attacking flash, and repairing commander target three moving units; targets receive move commands at tick 30. The damaged friendly target starts at half health. Goal proximity is to the initial target position and does not prove guard, attack, or repair completion. |
| `lifecycle/builder_work`, `lifecycle/factory_rally` | 8 crossing actors | A commander uses the human mobile-build command for an `armsolar` site while fleas cross; an `armlab` queues three `armflea` products and stores a rally marker amid crossing traffic. Player 0 starts with 100,000 metal/energy stock and installed storage bonus for these authored loads. Factory products are counted through production census, not predeclared as fixture actors. |
| `lifecycle/death_capture_transport` | 1 per lifecycle kind | Three moving fleas are respectively killed at tick 2, transferred from owner 0 to 1 at tick 3, and attached as cargo to a retail `armatlas` at tick 4. Death and capture mark their old handles as expected removals; cargo remains alive with no active goal. The captured replacement is recorded as a passive actor. Cargo attachment calls the production commit helper to isolate that lifecycle boundary, bypassing the approach/pickup admission path. |

All built-in events run before their numbered tick. Every builder is a fresh
session; event closures capture only that builder's actors. The traces record
exact handles and goals at issue time, including the short-wave coordinates
computed from committed positions. The catalog intentionally contains no
claimed arrival tick, collision outcome, or expected path success.

The smoke checks run 32 traffic or 40 lifecycle phase cycles per case in both
modes and assert that the intended initial order is present. With
`NANOLATHE_PATH_BENCH_SMOKE_FULL=1`, they include every supported size, run
the first sustained-wave event through tick 152, and run stop/reverse through
tick 52. A smoke measures nothing, so it takes no host lock; use the reference
install:

```sh
NANOLATHE_RETAIL_ASSETS=/Users/daniel/TotalAnnihilation \
GOMAXPROCS=2 NANOLATHE_PATH_BENCH_SMOKE=1 \
go test -p 2 -tags 'pathbench retail' ./internal/session \
  -run '^TestPathBench(Traffic|Lifecycle)Smoke$' -count=1
```

Fixture limits remain visible in the output. Synthetic void walls are not
wrecks, trees, construction yards, or dynamic map edits. The catalog does not
cover naval depth transitions, feature reclamation, fog knowledge, formation
stroke assignment, a genuine AI planner wave, end-to-end transport pickup or shipyard exit. The terrain/knowledge/naval and
formation catalogs cover those environments and gestures;
`path_bench_transport_test.go` adds transport pickup/unload and shipyard exit. No real-map placement is required for this suite. Direct nonlocal-owner order submissions in the
opposing, crossing, and wave cases skip human local-owner selection and the AI
planner. A factory
product may be absent from the predeclared actor array even while it contributes
to occupancy, so the runner's production census is necessary to interpret its
load. Cases with an impossible capacity label must be reported separately from
reachable failure rates.

The `transport-pickup-unload` and `transport-crowded-unload` cases use a retail
Atlas and Flea, ordinary pickup (tick 1) and replacement unload (tick 600), with a
2400-tick observation window. The crowded case places 25 idle Fleas around the
unload point. Final carrier linkage and movement mode distinguish passing over
a goal from actually unloading. `naval-shipyard-rally` queues three patrol boats
at a retail shipyard with eight existing boats crossing its approach. All-unit
census and final remaining construction fractions expose actual production.

The 1500-actor wave case fills three owners' 500-record pools. It is opt-in like
the rest of the suite. Wave scripts include a short command relative to each
probe's current position; if an algorithm changes that position, the actual
command trace also changes. The strict comparison guard rejects that pairing
as different input, while separate summaries remain useful.

## Wrecks over live units

The `wreck/*` family (`path_bench_wreck_test.go`) stamps wrecks over live
ground units with production services and then orders the survivors across
open ground. It exercises
[Modern wedge escape](DESIGN_MOVEMENT_PATH.md#modern-wedge-escape) and makes
no retail claim: the stamp tests no unit occupancy, so these states are
reachable in play, but the layouts are authored.

| IDs | Sizes | Authored conditions |
| --- | --- | --- |
| `wreck/jam_corpses` | 16/64, 1,500 ticks | A touching block of 2×2 units at `(12+2i,30+2j)`: Arm Jammers where `i%3==1 && j%2==1`, Flashes elsewhere. At tick 50 the Jammers die through the session's ordinary death path — ordinary damage kind, health −1, the lowest Killed severity — and each leaves its 3×3 `armjam_dead` at its committed anchor (checked at tick 51), over the west column, north row or north-west cell of the Flashes east, south and south-east of it. The survivors are ordered 44 cells east at tick 70. |
| `wreck/wreck_over` | 8/32, 1,200 ticks | A group of `corak` at `(12+3i,30+3j)` with one-cell gaps. At tick 50 the feature service stamps each unit's own 2×2 `corak_dead` exactly over every unit with `i+j` even — the end state of a unit dying inside another. The group is ordered 44 cells east at tick 70. |

The orders come at tick 70 because a fresh order's zero request stamp comes
due only once the 60-tick throttle has run from tick 0: an earlier order
leaves every unit on its synthetic line until tick 60, long enough to walk off
a partly covered footprint before any search runs, which a battle never
allows. The Jammer is the only ground unit in the reference install whose
corpse is larger than its movement footprint. The wedged units are the ones
whose committed footprint covers a cell the commit's static test rejects;
`session.TestPathBenchWreckEscape` (opt-in `pathbench` build) locks one relationship on
`wreck/wreck_over` with eight units — none of the four wedged units leaves
under Strict 3.1 or `modern-no-wedge`, and all four leave under Modern.
