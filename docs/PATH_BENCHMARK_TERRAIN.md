# Terrain and naval path benchmark fixtures

These opt-in synthetic scenes measure Nanolathe with unmodified retail unit,
movement-class, COB, and feature definitions. Dimensions, positions, heights,
void walls, events, and observation regions are authored experiment inputs,
not retail map claims or new gameplay policy. Every case rebuilds from the
same inputs for the diagnostic and timing passes defined in
[PATH_BENCHMARK](PATH_BENCHMARK.md).

| Family | Fixed cases | Route question |
|---|---|---|
| Terrain | `terrain-slope-flea`, `terrain-slope-flash`, `terrain-slope-stump` | A 16-height face distinguishes the retail Flea's 32 slope limit from Flash/Stump's 15. North and south end gaps provide reachable detours. |
| Clutter | `terrain-clutter-mixed`, `terrain-clutter-wrecks` | Five spaced rows of retail trees/rocks or Flash wrecks. Wrecks are placed through the feature service, so layer restamps occur. |
| Mazes | `terrain-winding`, `terrain-branching-maze` | Twelve alternating bars force more than twenty turns. A separate seven-gate maze has four west-open, east-closed pockets pointing toward the goal, measured as wrong-branch regions. Its second wave is ordered at tick 1200. Windows are 9000 and 6000 ticks respectively. |
| Concave | `terrain-concave`, `terrain-concave-sealed` | A reachable U needs a detour out its south mouth. The sealed control is intentionally unreachable. |
| Dynamic | `dynamic-new-wreck`, `dynamic-reclaim-wreck`, `dynamic-close-reopen` | A real four-cell wind-generator wreck appears at tick 90; reclaim payout starts at tick 90 or 180 and the feature service owns the removal transition. Events call production feature services, including class-layer restamps. |
| Knowledge | `knowledge-pioneer-same`, `knowledge-no-pioneer`, `knowledge-allied`, `knowledge-profile`, `knowledge-reopened`, `knowledge-mapped-control`, `knowledge-forced-fog` | Later orders start at tick 600 after a pioneer is retired at tick 500, or after the matched no-pioneer interval. Owner one is allied; Flash changes the later profile; reclaim reopens a four-cell passage at tick 550. The last two cases control visibility. |
| Naval | `naval-strait-patrol`, `naval-strait-cruiser`, `naval-four-cell-patrol`, `naval-four-cell-cruiser`, `naval-shallow-surface`, `naval-shallow-sub`, `naval-islands`, `naval-shore-hover`, `naval-shore-amphibious`, `naval-mixed-locomotion` | Surface and submarine depth limits, footprint clearance, island detours, a graded shore crossing, and a mixed group whose ship has an intentionally impossible land goal. |

The knowledge pair matches the later order's global tick and removes the
pioneer from active movement before that order. The tick-510 event checks that
no corpse or other new feature changed the later route. The owner's learned grid remains;
this is an observational comparison, not an isolated proof that
learned terrain alone changes a trajectory. Knowledge regions count face and
end-detour visits by cohort. Primary knowledge cases begin with empty mapping
history and current sight enabled; observers discover cells through the usual
publisher. Zero learned blocks is a valid measured outcome. The fully mapped
control keeps the base mode. The separately named forced-fog case keeps current
sight disabled and resets history before ticks 1–20, deliberately testing reuse
after an early rejection. Each later-wave event selects its owner's human
slot and player control records at tick 600 before queuing the order; the allied case selects owner one,
and the matched controls explicitly reselect owner zero. The maze regions count entry into each wrong
pocket by the first and second waves. The four-cell cruiser and sealed-U cases
must be reported as intentional infeasibility, separate from reachable failures.

The winding maze has full-width north and south void borders. Its twelve bars
join alternating borders, forcing eleven side reversals and at least 22
right-angle turns on a legal route. The branching maze has the same bounded
alternating structure with four additional dead-end pockets.

The shore ramp rises ten height units per cell from depth 60 to dry land.
The shallow strip is depth 10: patrol boats require depth 3 and submarines
require depth 15 in the checked retail catalog. The narrow slit has four
passable anchor rows after derived-height sampling, so it distinguishes the
patrol boat's four-cell footprint from the cruiser's five-cell footprint.

Set `NANOLATHE_PATH_BENCH_TERRAIN_SMOKE=1` to build every supported size and
run 31 authoritative ticks. Add `NANOLATHE_PATH_BENCH_TERRAIN_FULL=1` to run
their full fixed windows and all event callbacks. Run with the retail asset
environment and `tools/host-run`, as in the fixture contract. Full benchmark
cost and outcome artifacts belong to the shared runner.
