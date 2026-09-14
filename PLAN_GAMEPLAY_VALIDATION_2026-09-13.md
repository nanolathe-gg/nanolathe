# Gameplay validation — 2026-09-13

Follow-up to `PLAN_AI_FOLLOWUPS_2026-09-13.md`, authorized for a developed
battle save/load check, campaign continuation through the native interface,
the unresolved Two Continents seed-1 aircraft attacks, and EC-08 HUD timing.

## Scope and ownership

- Root: native save/load and campaign UI, integration and review.
- Aircraft investigation: isolated `air-trace-20260913` worktree; trace orders,
  flight, weapon execution, projectiles and damage before changing behavior.
- Resource display: isolated `hud-rates-20260913` worktree; verify the existing
  30-tick latch against the saved display deadline before changing behavior.
- Save fixtures: isolated `save-fixtures-20260913` worktree; prepare developed
  battles through ordinary engine progression and diagnose continuation.

The existing HUD already samples rates every 30 ticks. EC-08's remaining
question is the saved deadline and strict comparison, not a missing latch.
Economy settlement and displayed stock smoothing are separate contracts.

## Findings and validation

- EC-08: the existing latch refreshed eagerly on first draw and measured its
  next interval from the sampled tick. The corrected presentation owner uses
  the saved viewing-player deadline, strict unsigned comparison and one prior
  deadline increment per presented frame. Speculative rendering cannot advance
  it; save projection copies owned deadlines without changing live economy.
- Campaign results: the local victory latch was correct, but a raw player slot
  was compared with a host team identifier, marking the winner's score row as
  a loss. Campaign results now share the existing score-team identity mapping.
- Aircraft: detailed traces distinguish hits and regeneration from the sparse
  full-health snapshots. Following that investigation into bombing runs found
  premature target binding during preparation and incorrect weapon-slot scope.
  The correction inhibits all slots during preparation, binds only the primary
  slot at release, and clears that target on bomber break-off without changing
  slot control or Aim state. The baseline encounter dropped 75 bombs before
  release; regression tests exercise silence before release and firing after it.
  The corrected fighter encounter still lands hits, which later heal. Its
  changed timeline has no observed bomber attack through tick 109,000; this is
  a bounded observation, not proof of every later attack or eventual victory.
- Developed battle: a naturally reached stock checkpoint at tick 47,410 has
  72 live units, five factories, four nanoframes, attack/build orders and a live
  projectile. The rendered UI loaded it, wrote a new save, reloaded that save,
  restored pause, resumed combat and reached the natural defeat screen.
- A stock campaign checkpoint reaches its authored victory after restore.
  The integrated candidate reached the victory screen through the actual
  rendered UI. The UI then saved a between-missions bank, loaded it into mission
  two's briefing, and started that mission successfully. The written bank has
  the first mission marked `W` and `BetweenMissions = 1`.
- The next mission's UI save verifies the HUD handoff through the actual
  writer: saved global tick 85, paused, with display deadline 90. The campaign
  identity and first mission's completion mark remain intact.

The aircraft unit's sequential simulation-cost comparison used the same
scene/catalog and initial fingerprint. Median tick cost was 2.499 → 2.508 ms,
p95 5.447 → 4.875 ms. Later census/RNG state differs because firing changed,
so these figures do not establish a same-workload speedup. Focused regressions,
both worker gates, and independent fast/retail reviews pass. The final combined
production tree includes main's asset-loader changes through `1e1383ad` and
passed independent `tools/check` and `tools/check-retail` in the assigned review
worktree. Review included the full changes, arithmetic comparisons against the
owning evidence, message lifecycle verification and clean-room/invariant checks.
The external artifact directory holds the individual and integrated gate logs;
post-landing verification uses `postlanding-check.log` and
`postlanding-check-retail.log` alongside this UI evidence.

The UI pass also found old battle captions and F3 source handles crossing
battle boundaries. Retail's common entry path resets only the message-ring
cursors. The fix applies that reset on every successful installation, preserves
hidden record flags as the retail poster does, and limits column drawing to
committed battle frames. A second rendered campaign run confirms the loaded
briefing and next mission no longer carry the old attack caption. Regression
tests cover reused unit handles and paused battle message display. The results
screen was also painting the old caption over its scoreboard; retail uses a
separate outcome surface without that column. The terminal-frame exclusion
was verified in a final rendered victory/results replay.

Both live battle renderers completed sequential 180-frame checks at 1920×1080
with the same scene and simulation census: 327–340 units, 27–59 projectiles,
9–15 burning features and eight active factory builds. Captures were inspected.
Classic host cadence met the 35 ms tolerance in 179/179 valid intervals; modern
in 178/179. Their metal-glint setting differs by renderer, so their timing
figures are not presented as a like-for-like renderer comparison.

Native computer-use text shortcuts worked, but pointer/navigation events were
not reliably admitted through that automation path. The Ebitengine VM host
driver exercises the unmodified application with platform input and inspected
rendered frames. One campaign load exceeded the initial ten-second driver
timeout under heavy concurrent verification; the subsequent isolated load
with a longer driver timeout completed. Neither observation establishes a
gameplay implementation defect.

Diagnostic saves, traces, screenshots and gate logs remain outside
the repository under `/private/tmp/nanolathe-save-playtest-artifacts`,
`/private/tmp/nanolathe-air-trace-artifacts` and
`/private/tmp/nanolathe-hud-rates-artifacts`.
