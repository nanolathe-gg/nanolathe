# Approved review fixes — execution state

Historical task record: the captures and measurements below describe the builds
tested at the time, including the former fixed 1.5× view. Current camera and
benchmark settings are in [DESIGN_GPU_RENDERER](docs/DESIGN_GPU_RENDERER.md)
and [BATTLE_BENCHMARK](docs/BATTLE_BENCHMARK.md).

User authorization: complete every item in the recommended overnight queue and
important additional P2 list from the validated 2026-09-12 review. This plan is
implementation/task state, not retail evidence. Starting main: `f2af3fa`.
Orchestrator owns this plan and shared design/research updates unless explicitly
reassigned. Never modify the shared main checkout except the reviewed Git merge.

## Scope and progress

| Unit | IDs | Status |
|---|---|---|
| Skirmish composition | SC-01 | complete; reviewed and final gates passed |
| Campaign entry, identity and continuation | SC-02, FE-01, FE-02, RS-01, SC-08 | complete; reviewed and final gates passed |
| AI draw ordering | SC-03, SC-05 | complete; reviewed and final gates passed |
| Orders and input | HI-01/XC-01, HI-02, HI-03, HI-04, UO-01, UO-12, UO-13 | complete; reviewed and final gates passed |
| Input companions | HI-05, HI-08, HI-10, HI-13 | complete; reviewed and final gates passed |
| Visibility | WV-01, WV-02, WV-03 | complete; reviewed and final gates passed |
| Economy integration | EC-01, EC-05 | complete; reviewed and final gates passed |
| Movement and transport | MP-01, MP-02, MP-03, MP-12 | complete; reviewed and final gates passed |
| Mission completion | FE-07, PC-02 | complete; reviewed and final gates passed |
| Settings preservation | CV-15 | complete; reviewed and final gates passed |
| Frontend callbacks, restart, failures | FE-04, FE-05, FE-06, FE-09, FE-08, FE-12 | complete; reviewed and final gates passed |
| Missing-art diagnostics | PC-04, PC-14 | complete; reviewed and final gates passed |
| Fog resource stability | GP-01, GP-02 | complete; reviewed and final gates passed |
| Narrow allocation reductions | MP-08, UO-05, CV-10 | complete; reviewed and final gates passed |
| Guard and clean-room hygiene | XC-04, WP-04 | complete; reviewed and final gates passed |

Explicitly excluded from the approved queue: speculative negative-Give/capture
guards, broad malformed-format fallback, untraced palette/audio cadence changes,
AI tuning to an arbitrary victory deadline, and broad dead-code cleanup. The
validated report's optional stretch list was not part of the final requested
queue. Existing independent bugs are reported rather than silently expanding it.

## Public API and ownership contracts

- **Campaign:** selected side must cross fresh-battle composition explicitly,
  including whether it was supplied. Zero is a valid Arm side, not absence.
  Constructors without a selection retain documented authored-side resolution
  or Unknown. Persisted per-player side and commander identity must agree before
  entry priming; a restored session is authoritative for shell continuation.
- **Input:** the relevant interface polarity must reach order resolution through
  session/command-owned data, never a mutable process-global option. Cursor and
  command dispatch use one logical selection. Preserve all existing explicit
  command inputs, deterministic command ordering and the mapped/reclaimable
  feature gate. Establish the precise timer/fallback clauses before depending
  on them.
- **Movement:** requested mode and committed mirror remain distinct until the
  ordinary validator accepts. Rejected commits preserve committed occupancy,
  callbacks and post-correction semantics. Add no undocumented retry strategy.
- **Visibility:** direct and committed-frame predicates use the same researched
  origin/extents; retain drawing coordinates separately. Snapshot remains owned
  by the tick publication boundary.
- **Diagnostics/performance:** bounded retained diagnostic data, no tick logging,
  no speculative recorder side effects; stable immutable art identity; preserve
  lookup/queue order and ownership when removing copies.
- Editing workers receive exclusive concrete file lists. Shared docs/research
  remain orchestrator-owned; findings outside ownership are reported and any
  dependent implementation waits for the owning research update.

## Verification and landing

1. Establish clean fast/retail baseline and matching simulation/classic/modern
   benchmark artifacts outside the repository. Read the installed Ebitengine
   headless skill before captures. Serialize benchmarks on a quiet host.
2. Iterate with affected contract tests. Each worker commits one bounded unit
   and reports the diff/evidence; workers do not merge main or land their work.
3. Orchestrator reads each diff and arithmetic contracts, reviews clean-room and
   invariants, integrates main, runs tools/check and tools/check-retail plus the
   applicable design/visual/performance gates, then lands reviewed work.
4. Repeat required gates after landing. If main advances, integrate and recheck.
   Remove only our landed worktrees/branches. Do not alter foreign work.
5. A precise unresolved question is recorded in code and its owning research
   Unknown list. Do not call dependent behavior implemented or its gate passed.

## Evidence log

- Initial main/worktree status clean at `f2af3fa`.
- Baseline fast and retail gates started; outputs outside repository.
- Baselines, commits, reviews, remaining questions and final gates are appended
  here at phase boundaries so continuation does not depend on conversation memory.

- Baseline fast and retail gates passed (retail rerun needed network access for
  pinned lint dependencies). Logs: `/private/tmp/nanolathe-overnight-baseline-*.log`.
- Quiet-host baseline simulation and classic/modern runs completed at f2af3fa.
  Artifacts: `/private/tmp/nanolathe-overnight-base-{sim,classic,modern}`.
  Simulation median 1.366 ms/tick, p95 2.135 ms/tick. Both renderer captures
  visually inspected; matching scene/census, visible armies/fire/factory builds.
  Keep recorded display settings for final matching runs (gamma 8, zoom 1).
- HI-02/UO-14 dependencies established by private static trace. Independently
  worded owning research and input design committed 640dadc; docs tests passed.
- Campaign, movement and input units active in isolated worktrees; all released
  from baseline timing barrier. Root owns shared docs/research and landing.

- Integrated concurrent main dc13789 without discarding its playtest corrections.
- Campaign ea79dd4 and movement 3972e78 reviewed and integrated. Campaign fast/retail
  gates passed; movement’s remaining admission fixture is owned by the input unit.
- Root economy, settings, audio, guard/hygiene and weapon lookup fixes are committed.
  Affected package tests passed. Missing-art diagnostics and shared design updates
  are under final review. Input, visibility and fog have passed fast checks; final
  unit retail gates and integrated reviewer gates follow.

## Final review and validation

- Every approved unit is implemented and its production diff reviewed. The
  visibility, input and renderer review included the cited arithmetic and
  comparison boundaries. The final source scan found no added executable
  addresses, generated analysis names, sim clocks, nondeterministic map walks
  or module dependencies. Expanded typed guards pass.
- Input review caught and corrected the feature/unit overlap gate and the
  full-width model-top water test. Integrated main's HUD reset helper retains
  group clearing while the input fix clears placement/latch state coherently.
- Campaign visual review exposed missing mission-list painting, wrong outcome
  title assets/anchor, the final-row painter boundary, initial scroll clamping,
  and the Core briefing background key. These were corrected in `88fc3ce` after
  verifying the owning research. Arm/Core first and last pages, marks,
  selection, titles and selected briefings were inspected. Captures and the
  reusable fixture live in `/private/tmp/nanolathe-overnight-campaign-visual`.
- Production callback tests exercise Arm, Core, Missions and MAPNAMES plus an
  unavailable MAINMENU at startup. A real Two Continents opening now contains
  only one commander for each configured active player, with no units owned by
  inactive slots (`/private/tmp/nanolathe-overnight-two-continents.json`).
- Concurrent main through `02467fc` was integrated, preserving the separate
  activation-queue, model-binding and unavailable-Intro work. Final fast, retail
  (including pinned lint) and Metal device gates passed on the integrated
  branch `609fbe3`. Logs: `/private/tmp/nanolathe-overnight-final-check.log`,
  `/private/tmp/nanolathe-overnight-final-check-retail.log`, and
  `/private/tmp/nanolathe-overnight-final-device.log`.

### Performance evidence

All runs used the retail install and sequential prebuilt binaries. Live battle
settings were Great Divide scene 4, seed 7, 1920×1080, 30 draws/s, 300 pre-ticks,
60 warmup draws and 180 measured draws. Classic and modern captures at 1×,
1.5× and 2× were inspected beside their baselines. Armies, fire, projectiles,
effects and eight factory nanoframes remained present; the measured live-unit
range was 319–338. Metadata and captures are retained in
`/private/tmp/nanolathe-overnight-{base,final}-*`.

The original `f2af3fa` baseline preceded concurrent main changes. A follow-up
pair against `e5f85be`, with the same settings, resolved an apparent modern
median slowdown. These are host measurements, not isolated GPU timings:

| Probe | Main median / p95 / max (ms) | Candidate median / p95 / max (ms) |
|---|---|---|
| Displayless simulation tick | 1.570 / 2.343 / 6.046 | 1.473 / 2.332 / 8.972 |
| Modern 1.5× draw work | 10.549 / 11.686 / 12.067 | 9.127 / 11.817 / 19.405 |
| Modern 2× draw work | 10.162 / 11.440 / 13.298 | 10.132 / 11.377 / 29.154 |

Simulation allocations fell from 167,239 to 159,335 bytes/tick and 1,476 to
1,345 objects/tick. Modern measured allocation fell from 1.791 to 1.681 MB/frame
at 1.5× and 1.566 to 1.444 MB/frame at 2×. The candidate's repeated simulation
fingerprints and both RNG draw counts agree exactly. Main and candidate battle
outcomes differ under the corrected AI/movement contracts: simulation window
open/close live-unit counts are 716/513 versus 699/433, with 311 versus 386
cumulative deaths at close. Both retain combat and construction, but this
workload difference prevents attributing the tick-time change purely to speed.
The occasional maximum-time spikes and run-to-run spread also prevent a
universal improvement claim. Full profiles and distributions remain in
`/private/tmp/nanolathe-overnight-{main,paired-final}-*`.

The standard frozen-list capture profiler failed in Metal drawable acquisition
on both candidate and unchanged main; logs are
`/private/tmp/nanolathe-overnight-frozen-{mid,main-mid}.log`. This reproduces the
existing capture-tool limitation documented in DESIGN_GPU_RENDERER. The fog
correctness fixtures separately replay one frozen list without readback,
verify stable source/atlas counts, and compare pixels before and after reset.
A verification-only Go build overlay made the profiler window visible and
VSync-enabled, preserving the frozen recording and replay logic. All six runs
passed on Apple M3 Pro/Metal with the same isolated settings, 1920×1080,
eight warmup and 180 measured frames. Baseline/candidate captures are
byte-identical at 1×, 1.5× and 2×; the fog edge was visually inspected at each.
CPU submission median/p95/max was 0.508/0.794/0.870 → 0.524/0.818/0.870 ms
at native, 0.929/1.303/1.362 → 0.181/0.555/0.892 ms at 1.5×, and
0.617/1.187/1.508 → 0.230/0.489/0.538 ms at 2×. Median paced cadence
remained approximately 8.33 ms. This validates repeated frozen-list execution
and the zoom-path improvement on this host, not isolated GPU cost or general
frame rate. Reproduction overlays, settings, metadata, full timing tails and
captures remain in `/private/tmp/nanolathe-fog-frozen-review`; production
capture code was unchanged. The standard hidden profiler's pre-existing
Metal failure remains a tooling limitation, not an uncompleted queue item.

The original validated assessment is preserved at
`/private/tmp/nanolathe-overnight-review-artifacts/REVIEW_2026-09-12_VALIDATED.md`.
Its recommended scope is closed by the units above. The original report's
excluded and optional stretch items remain separate from this completed queue.
