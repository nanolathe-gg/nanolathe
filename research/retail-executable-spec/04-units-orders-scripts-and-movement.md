# Retail executable specification: units, orders, scripts, and movement

This document records the current clean-room behavioral contract recovered from the retail executable. It is deliberately written in terms of logical state and observable behavior. It does not reproduce executable addresses, binary offsets, decompiler names, or implementation code.

Evidence labels:

- **Established fact** means the behavior is directly supported by static control and data flow in the retail executable, or by a bounded call, read, or write census over it.
- **Supported inference** means the behavior follows from several direct observations, but one semantic link or caller remains unresolved.
- **Unknown** means that the available retail evidence does not close the behavior. An implementation must keep the gap explicit rather than inventing a rule.

The corrected notes and root-correction ledger take precedence over earlier notes. In particular, the 30 Hz tick, the current path-search reconstruction, the current COB drain root, and the current visibility/effect phase identities are used here. Earlier notes marked superseded or quarantined are not evidence. In-place corrections below carry audit notes naming the superseded reading: the code-9 last-record wait in section 3.3 [R-P0-01] and the UNIT_HEIGHT port in section 4.4 [R-P0-10].

## 1. Simulation prerequisites and phase contract

### 1.1 Logical time

**Established fact:** The authoritative simulation advances at 30 logical ticks per second. The frame dispatcher converts wall-clock time to a bounded number of logical ticks, with fractional carry. A frame can run at most five simulation ticks. Pausing produces no logical ticks. On single-player resume the stalled wall-clock anchor can yield one capped burst of up to five ticks; the excess integer time is dropped. The multiplayer budget keeps sampling time while paused and does not have that resume burst.

**Established fact:** The tick body is ordered and increments the global tick
before any phase runs [P0-09]. The twelve-phase tree (canonical order in
document 01 section 4.4) is, in plain order: network dispatch, per-unit sweep,
projectile, feature and fire, visibility, trigger poll, sharing, economy
settlement, wind and meteor, ten-vtable barrier, and presentation, with
wind and meteor and the per-player settlement coordinator (AI before deadline)
grouped as separate phases in that tree. The exact presentation barriers are
outside this document. The unit and projectile boundaries are authoritative
because they determine same-tick visibility and callback order.

**Established fact:** The unit sweep is deterministic: player slots are visited
in numeric order 0 through 9, and units are visited in ascending pool order with
no hidden map iteration and no generation-tagged handles [P0-09][P1-14].
Same-sweep visibility of a newly allocated unit depends on whether its
player and slot lie before or after the current scan position: already-visited
positions wait for the next sweep, while an unvisited position can still be
reached the same tick — create is visible to later same-tick readers via the
sparse set plus occupancy restamp, and a freed slot is immediately reusable via
lowest-free scan [P0-09]. A unit killed during projectile processing retains
enough state to be seen by later same-tick phases; final deletion is deferred
to cleanup through the central death handler [P0-09].

**Established fact:** Within one unit visit the sweep executes, in order: the
general unit update (which can queue deferred `SetDirection` and `SetSpeed`);
the weapon update for eligible players (which can queue deferred
`TargetCleared` and the `AimPrimary/Secondary/Tertiary` family, and whose fire
decisions reach the projectile creators that queue `FirePrimary/Secondary/
Tertiary` followed by `RockUnit`); the normal script drain with tick delta 1,
running all eight threads once and then one piece-interpolation pass; order
and construction work (primary pump head-blocking then secondary skip-not-due);
movement integration, whose medium classifier issues `StartMoving`,
`StopMoving`, `MoveRateN`, and then `setSFXoccupy` as immediate wake-flag
starts; and finally the slot-end death handling, which can run the synchronous
local `Killed` query (Q). Consequences: a deferred (D) callback queued before the
normal drain executes in that same visit (step 3 drains every active slot); a deferred callback queued after it
normally waits for the next visit, except that any later immediate-start
(I) callback on the same virtual machine performs an all-slot delta-0 drain that
can execute it earlier [P0-09]. Damage callbacks queued by the post-unit
projectile phase therefore normally land on the next visit, while damage packets
processed during event ingress (before the unit phase) can queue theirs in time for that tick's normal
pass. The four direct piece-pass call sites are the immediate branches of the zero-argument name-form adapter, the slot-based zero-argument helper, and the argument-carrying slot-form adapter (section 4.2), plus the ordinary per-tick drain; there is no direct piece-pass call from the general unit update (negative-bounded). Capture is synchronous before trigger polling, build-complete product
publication (GetBuilt) occurs before trigger polling but victory needs the next
30-tick poll, and kill notification goes through the central death handler
[P0-09]. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** The projectile phase captures its active-span count at
entry. A zero-burst weapon projectile spawned during the preceding unit sweep
is inside that captured span and is eligible to move and collide in the same
logical tick, subject to its family-specific launch and expiry state. A record
appended during projectile iteration, including a burst clone, lies outside the
captured span and first moves in the following tick. Projectiles created by
later phases likewise wait for the next projectile phase [P0-09].

### 1.2 Determinism and random state

**Established fact:** Simulation random choices use one global Park–Miller stream. There are no verified per-unit or per-player simulation streams. Therefore call order, pool iteration order, and branch parity are part of the contract.

**Established fact:** A separate C-runtime-style random path exists for non-simulation uses such as some wind or presentation work. It must not be silently substituted for the simulation stream.

**Unknown:** The complete lockstep packet, replay, state-hash, and resynchronization contract is outside the recovered unit/movement evidence. Do not assume that every transient presentation value participates in simulation state.

## 2. Player, unit, definition, and lifetime identities

### 2.1 Player slots

**Established fact:** The retail runtime has ten fixed player slots. Player records hold side, ally/autonomy state, resource state, unit slices, and visibility-related state. The unit sweep and several sharing paths iterate all ten slots in stable order.

**Established fact:** Hostility between two units is derived from a per-side
diplomacy byte on the acting player's record indexed by the TARGET's side:
byte zero means hostile, nonzero means friendly — side equality alone is
neither necessary nor sufficient [P0-R02 §2.4]. The resolver family applies
this in both its shapes. Full alliance semantics beyond this predicate are
not closed by the unit notes; ally checks must remain a dedicated predicate
rather than simple side equality. *(Upgraded from Supported inference — the
hostility half is direct; the alliance half stays open.)*

**Unknown:** The complete semantic mapping of player category values, autonomous/AI values, and ally masks is not recovered here. Lobby state and mission setup should not be treated as proof of strategic AI behavior.

### 2.2 Unit definitions

**Established fact:** Unit definitions are data-driven records compiled from the FBI/TDF catalog. The movement portion includes:

- maximum speed;
- acceleration and braking;
- turn rate;
- bank and pitch tuning;
- fallback move rates;
- maximum and minimum water depths;
- land and water slope limits;
- movement flags for ground, hover, floater, aircraft, upright, amphibious, and related behaviors;
- footprint dimensions and yard-map/build-site data;
- build distance and heading;
- three weapon-slot definitions and target-category masks;
- COB script binding and piece metadata.

The catalog also carries health, build time, build cost, work time, storage and resource values. Economy and construction are dependencies of the movement/order system, not redefined here.

**Established fact:** Movement profiles are compiled from MOVEINFO data. A profile contains half-footprint dimensions, land/water slope thresholds, maximum/minimum water depths, and a packed terrain layer. Profile-specific thresholds determine whether the same map cell is legal for a ground, ship, hover, or other mover.

### 2.3 Unit runtime state

**Established fact:** A live unit has a stable pool slot, definition identity, player/side identity, fixed-point world position, heading, velocity, health, construction remaining fraction, movement status, weapon-slot state, script instance, and order/waypoint state.

**Established fact:** Unit positions use fixed-point world coordinates. Heading is a 16-bit circular angle. Movement and script interpolation use integer arithmetic with truncation, not floating-point accumulation of a remainder.

**Established fact:** A unit's alive state and death-mark state are separate.
Lethal damage marks a unit for death. Local authoritative death runs the
synchronous Killed query; received-network death replay uses the asynchronous
Killed form instead. Final pool cleanup occurs later. A slot can be reused
after cleanup without a generation counter.

**Established fact:** Unit allocation scans for the lowest available slot, while slot zero is reserved as a null sentinel. There is no verified generation number. References represented by slot or pool identity can alias a later unit after reuse. Projectile and order references must follow the retail validation rules rather than assuming generational safety.

**Established fact (retail path, future parity only):** Save reconstruction preserves
unit slot identity and repairs cross-unit references after the units are
reconstructed. A failed unit allocation can cause the saved record to be
skipped. Nanolathe's active boundary is narrower: in-battle restoration is an
explicitly unsupported result, so this reconstruction behavior does not
authorize a live restore implementation ([PLAN_14 C10]; [INVARIANTS I13]).

**Established fact — pool capacity [P0-16]:** The physical pool capacity is
`(u16)catalogDefCount · 10 + 1` records of 0x118 bytes, allocated at battle
entry. The pool is laid out as per-player slices of `catalogDefCount`
records; slot 0 is the null sentinel. Allocation scans each player's slice
lowest-free and enforces the per-definition limit gate (the definition's
limit-enable bit plus its limit field). The mission `maxunits` field is NOT
read by the allocator — it does not bound allocation (bounded-negative,
2641-TU census) — and save restore verifies the forced slot. The earlier
"exact maximum unit count is not resolved" reading is superseded by this
derivation [R-P0-16].

### 2.3a Player-slice order at battle entry [R-P0-16-A]

**Established fact:** The pool initializer first forms a ten-element list in
logical player-slot order `0..9`, then assigns contiguous definition-count
sized slices to that list's resulting order. The comparator is mode-gated: in
mission mode `3`, it orders records by their unsigned 32-bit
`PlayerSortKey` in strict ascending order; in every other mission mode it
orders by the original logical player slot. Equal mode-3 keys retain the
original slot order. This is the complete comparator and gate; it runs once
before pool construction and is never re-applied during a running battle.

The element at sorted position `i` receives the slice
`1 + i·catalogDefCount` through `(i+1)·catalogDefCount`, while the logical
player named by that element owns the range. Slot zero remains the null
sentinel. A valid order is therefore a total permutation of `0..9`; malformed
or duplicate values are rejected before allocation. The exact provenance and
semantic name of `PlayerSortKey` are not needed by the comparator contract and
remain outside this section's scope. [R-P0-16]

### 2.3b Unit-initialization heading [R-P28-ANG-01R §2]

**Correction (2026-08-28).** A prior bounded allocator note called unit
allocation RNG-free because it inspected the outer allocation scan only. That
was incomplete: successful allocation enters the common initializer below,
which performs the heading draw (and a separate initialization draw). The
failure result remains RNG-free because it returns before that initializer.

**Established — one allocator initialization draw.** Every successful call to
the canonical unit allocator reaches the common position/state initializer.
That initializer reads the compiled `buildangle` value as an unsigned 16-bit
bound and invokes the global Park–Miller simulation stream once. The stream is
the single battle-wide state, seeded at battle entry from the low-plus-high
`QueryPerformanceCounter` value XORed with `0x66e29572` and forced odd; it is
not reseeded per unit or per player [01 §7.2]. If `b` is the
bound and `r` is the returned value, the sampler's result is in `[0,b)` for
`b >= 2`; for `b < 2` the helper returns zero without advancing the stream.
The heading written by the initializer is:

```text
r16 = sign_extend_16(r)
heading = uint16(r16 - 0x8000 - (uint16(buildangle) >> 1))
```

The signed 16-bit conversion of the random result is part of the arithmetic,
as is the final conversion to the 16-bit circular heading domain. With the
stock `buildangle=4096`, the possible initial headings are `30720..34815`
(inclusive), centered on `32768` (the 180-degree/south direction). With the
default `buildangle=0` (and likewise with a bound of one), the initialized
heading is `32768` and no angle draw advances the stream.

The same initializer then performs a separate full-domain simulation draw for
another unit-state field. That draw is not part of heading selection; callers
that model the common allocator must preserve its position in the global RNG
call order even though the field's semantic name is outside this finding.

**Lifecycle and writer census — Established.** The allocator is the only
heading writer in the common unit-creation path. Mission-unit creation invokes
the allocator first and then copies the authored mission placement angle over
the initialized heading [08 "Unit creation and InitialMission timing — Established"].
Factory products are allocated as nanoframes and retain the initialized
heading; the factory's `QueryBuildInfo` result supplies a position, not a
separate product-heading adjustment. Completion, `GetBuilt`, and the factory
queue do not rewrite that heading. Mobile movement may subsequently update a
unit's heading through its normal desired-heading integrator, but this is not
an angle-adjustment read.

The authored mission-placement record's facing angle is a separate writer: it
is copied over the initializer result after a mission unit is allocated. The
heading-form `StartBuilding` variant likewise carries the producer's current
heading to the producer's script as its first argument; it does not write the
new product's heading. Construction-command placement therefore has no
additional build-order heading adjustment. A mobile builder's heading toward
the selected build goal is produced by ordinary movement steering, not by
`buildangle`.

**Retail save-reconstruction finding (future parity only).** Save
reconstruction also invokes the allocator (including the forced-slot path),
consuming the initialization draws, and then restores the saved unit heading.
Thus a saved heading is authoritative and is not recomputed from `buildangle`;
a restored unit's RNG position still includes the allocator's draw sequence. A
failed limit/slot allocation returns before common initialization and consumes
no angle draw. Nanolathe currently returns an explicit unsupported result for
in-battle restoration, so this preserves the required future draw ordering but
does not authorize implementing a live restore path ([PLAN_14 C10];
[INVARIANTS I13]).

No slope/ground-alignment, construction-completion, renderer-only, or
`ovradjust` heading writer was found in the bounded census. Placement and
pathing use the axis-aligned footprint rectangle and do not consume or rotate
it from this initial heading. The separate producer-model transform question
at the factory exit remains the [R-P0-02] model/position residual, not an
additional product-angle rule.

### 2.4 Unit flags and state transitions

**Established fact:** Runtime flags cover alive, dying, being built,
activated/deactivated, blocked, movement tier, hover/medium band, a dedicated
stunned activation state, and target/weapon readiness. The death latch and
stun state are distinct. Several flags are edge-triggered into COB callbacks.

**Established fact:** Paralyzer damage can prepend a task to the primary
command list. The task clears weapon targets, sets the stunned state, and waits
on an absolute tick deadline. The primary list runner is blocked during the
wait, but damage, healing, death, economy settlement, upkeep, and automatic
healing are outside that wait. Later paralyzer hits can accumulate duration in
the waiting head task. Exact packet arithmetic and the duration cap belong to
document 06.

**Established fact:** The engine does not infer movement state from presentation. For example, StartMoving and StopMoving are emitted on state edges; MoveRate callbacks are emitted when the cached movement tier changes. The callback is part of the authoritative script interaction even when the renderer is not involved.

**Supported inference:** Some bit names in the decompilation notes are semantic aliases rather than retail labels. Preserve the raw state behavior and expose stable logical names only where the consumer graph is clear.

## 3. Orders, queues, and dispatch

### 3.1 Order descriptor table

**Established fact:** The engine registers a fixed table of **68 order
descriptors** during initialization, from four static template batches of 23,
22, 22, and 1 records. After every batch is appended the whole table is
re-sorted ascending by canonical command name using a case-sensitive byte
comparison, and **an order's numeric identity is its index in that sorted
table**. Because all four batches register before play begins, the identities
are stable for the whole session.

A descriptor is 25 bytes and carries:

- a **state label**, the string the interface and script vocabulary use for a
  unit currently running this order;
- the **handler**, called with the owning unit, the order record, and the bits
  that were satisfied this tick;
- an optional **presentation helper** run during goal resolution: none, goal
  resolve with acknowledgement text and rings, the same plus moving path
  markers, or a build-footprint marker;
- a small **class parameter** — bounded census over 3901 function boundaries
  found no reader, so store it opaque and do not branch on it
  `TODO(question)` [P0-07];
- an **acknowledgement group** index; two of the groups additionally draw the
  weapon area-of-effect, coverage radius, and attack-length rings when a global
  display option is set;
- a 32-bit **static gate mask**, copied into each order record at construction;
- the **canonical command name**, which is the sort key, the binary-search key,
  and the display name source.

One record carries the empty canonical name. It sorts to index zero, which is
also the identity the command resolver returns when a command is rejected, so
index zero is the reject sentinel.

The remaining 67 records, in sorted order, are:

| Command | State label | Class | Ack group | Gate mask |
|---|---|---:|---:|---:|
| `Activate` | Activate | 0x00 | 19 | 0x10060 |
| `AirStrike` | Airstrike | 0x08 | 2 | 0x600 |
| `AirToAir` | Engaging target | 0x08 | 1 | 0x200 |
| `AirToGround` | Engaging target | 0x08 | 1 | 0x200 |
| `AirToGroundHover` | Engaging target | 0x08 | 1 | 0x200 |
| `AttackSpecial` | Annihilating | 0x08 | 1 | 0x680 |
| `AttackUType` | Attacking | 0x00 | 19 | 0x4 |
| `Attack_Chase` | Attacking | 0x08 | 1 | 0x280 |
| `Attack_Kamikaze` | Attacking | 0x08 | 1 | 0x600 |
| `Attack_NoMove` | Attacking | 0x08 | 1 | 0x280 |
| `BeCarried` | Being transported | 0x00 | 19 | 0x24 |
| `BuildWeapon` | Nanolathing | 0x00 | 19 | 0xc0140 |
| `BuildingBuild` | Nanolathing | 0x00 | 19 | 0x10010c |
| `Capture` | Capturing | 0x08 | 4 | 0x200 |
| `Cloak_Off` | Decloaking | 0x00 | 19 | 0x10060 |
| `Cloak_On` | Cloaking | 0x00 | 19 | 0x10060 |
| `Deactivate` | Deactivate | 0x00 | 19 | 0x10060 |
| `Follow_Ground` | Guarding | 0x12 | 5 | 0x200 |
| `GetBuilt` | Under construction | 0x00 | 19 | 0x224 |
| `Ground_Pickup` | Loading | 0x08 | 12 | 0x200 |
| `Ground_Unload` | Unloading | 0x08 | 13 | 0x400 |
| `Guard_NoMove` | Ready | 0x00 | 19 | 0x20 |
| `HelpBuild` | Nanolathing | 0x18 | 6 | 0x100208 |
| `MakeSelectable` | Unit is available | 0x00 | 19 | 0x4 |
| `MobileBuild` | Nanolathing | 0x13 | 0 | 0x100508 |
| `Move_Ground` | Moving | 0x12 | 14 | 0x402 |
| `Paralyze` | Paralyzed | 0x00 | 19 | 0x24 |
| `Park` | Parking | 0x00 | 14 | 0x0 |
| `Patrol` | Patrolling | 0x12 | 7 | 0x412 |
| `QMove` | Ready with orders | 0x02 | 14 | 0x400 |
| `QPatrol` | Ready with orders | 0x02 | 7 | 0x400 |
| `Reclaim` | Reclaiming | 0x12 | 11 | 0x100800 |
| `ReclaimUnit` | Reclaiming | 0x12 | 11 | 0x100200 |
| `RepairPatrol` | Repair patrol | 0x12 | 7 | 0x412 |
| `RepairUnit` | Repairing | 0x12 | 6 | 0x100200 |
| `RepairUnitNoMove` | Repairing | 0x18 | 6 | 0x200 |
| `Resurrect` | Resurrecting | 0x12 | 11 | 0x200 |
| `SelfDestruct` | SELF DESTRUCT ENGAGED | 0x00 | 19 | 0x40040 |
| `SelfDestructFG` | SELF DESTRUCT ENGAGED | 0x00 | 19 | 0x0 |
| `SelfRepair` | Repairing | 0x00 | 19 | 0x1000204 |
| `Standby` | Standby | 0x10 | 15 | 0x20000 |
| `Standby_Mine` | Standby | 0x10 | 15 | 0x1020000 |
| `Standing_FireOrder` | Acknowledged | 0x00 | 19 | 0x10060 |
| `Standing_MoveOrder` | Acknowledged | 0x00 | 19 | 0x10060 |
| `Stop` | Stopping | 0x00 | 19 | 0x0 |
| `Suppress` | Suppressing fire | 0x08 | 1 | 0x410 |
| `Teleport` | Teleporting | 0x08 | 9 | 0x600 |
| `VTOL_Evade` | Evading | 0x00 | 19 | 0x0 |
| `VTOL_Follow` | Guarding | 0x02 | 5 | 0x200 |
| `VTOL_GetRepaired` | Under repair | 0x00 | 19 | 0x200 |
| `VTOL_HelpBuild` | Nanolathing | 0x08 | 6 | 0x100208 |
| `VTOL_LandIfCan` | Seeking to land | 0x00 | 19 | 0x400 |
| `VTOL_Landing` | Landing | 0x08 | 14 | 0x600 |
| `VTOL_MobileBuild` | Nanolathing | 0x03 | 0 | 0x100508 |
| `VTOL_Move` | Moving | 0x02 | 14 | 0x402 |
| `VTOL_Patrol` | Patrolling | 0x02 | 7 | 0x412 |
| `VTOL_Pickup` | Loading | 0x08 | 8 | 0x200 |
| `VTOL_Reclaim` | Reclaiming | 0x02 | 11 | 0x100800 |
| `VTOL_ReclaimUnit` | Reclaiming | 0x02 | 11 | 0x100200 |
| `VTOL_RepairPatrol` | Repair patrol | 0x02 | 7 | 0x412 |
| `VTOL_RepairUnit` | Repairing | 0x02 | 6 | 0x100200 |
| `VTOL_SeekAttack` | Seeking to attack | 0x00 | 19 | 0x600 |
| `VTOL_SeekGuard` | Seeking to guard | 0x00 | 19 | 0x600 |
| `VTOL_Standby` | Standby | 0x00 | 15 | 0x20000 |
| `VTOL_Unload` | Unloading | 0x08 | 9 | 0x400 |
| `Wait` | Waiting | 0x00 | 19 | 0x4 |
| `WaitForAttack` | Waiting for attack | 0x00 | 19 | 0x204 |

**Established fact:** Named bits of the gate mask are: 0x200 cleared when the
order is constructed without a target unit; 0x400 cleared when constructed
without a goal position; 0x4000 inherited from the current tail record on
enqueue; 0x40000 marks a record that belongs in the rear queue segment;
0x100000 marks the nanolathe/build-site class, tested by the guard-assist
branch; and 0x200000 marks a valid cached target position, written by the
goal-resolution helper. The remaining observed bits have no located consumer.

**Audit note — descriptor table verified from the static templates [R-DOC04-C]
(2026-08-27).** All four static template batches were located and every field of every
static descriptor was read byte-exactly; the table above was re-verified against that dump
with **zero differences across all 67 named descriptors in all four verified columns**
(state label, class parameter, acknowledgement group, static gate mask), and the sorted
identity was recomputed from a case-sensitive byte sort of the 68 canonical names
(the empty name at index 0, then the table's order exactly). The field layout is
confirmed as, in offset order within the 25-byte record: **state label pointer, handler,
presentation helper, class parameter (32-bit), acknowledgement group (one byte), static gate
mask, canonical name pointer** — the doc's field list above is complete but was not
previously ordered.

Batch insertion order (as compiled into the image, 23 + 22 + 22 named records):

- Batch 1: `Stop`, `Attack_NoMove`, `Activate`, `Deactivate`, `Cloak_On`, `Cloak_Off`,
  `Standing_MoveOrder`, `Standing_FireOrder`, `BuildingBuild`, `BuildWeapon`,
  `SelfDestruct`, `SelfDestructFG`, `Paralyze`, `GetBuilt`, `BeCarried`, `MakeSelectable`,
  `Wait`, `WaitForAttack`, `AttackUType`, `Guard_NoMove`, `SelfRepair`, `QMove`, `QPatrol`.
- Batch 2: `Standby`, `Standby_Mine`, `Move_Ground`, `Follow_Ground`, `Suppress`,
  `Attack_Chase`, `Attack_Kamikaze`, `AttackSpecial`, `Park`, `Patrol`, `Ground_Pickup`,
  `Ground_Unload`, `Teleport`, `MobileBuild`, `HelpBuild`, `RepairPatrol`, `RepairUnit`,
  `Capture`, `Resurrect`, `Reclaim`, `ReclaimUnit`, `RepairUnitNoMove`.
- Batch 3: `VTOL_Standby`, `VTOL_Move`, `VTOL_Landing`, `VTOL_Pickup`, `VTOL_Unload`,
  `VTOL_Follow`, `VTOL_Patrol`, `AirStrike`, `AirToAir`, `AirToGround`,
  `AirToGroundHover`, `VTOL_MobileBuild`, `VTOL_HelpBuild`, `VTOL_RepairPatrol`,
  `VTOL_RepairUnit`, `VTOL_Reclaim`, `VTOL_ReclaimUnit`, `VTOL_Evade`, `VTOL_SeekAttack`,
  `VTOL_SeekGuard`, `VTOL_GetRepaired`, `VTOL_LandIfCan`.
- Batch 4: the empty canonical name — no static image of this record exists in the read-only
  data (a zero-filled 25-byte template is indistinguishable from padding); that it is
  appended at registration rather than compiled in is **Supported inference**. The batch
  sizes 23/22/22/1 are unchanged.

The presentation-helper field takes exactly four identities across the 68: none; goal
resolve with acknowledgement text and rings (the attack, suppress, capture, pickup, unload,
teleport, and help-build families); the same plus moving path markers (the move, patrol,
repair-patrol, follow, repair-unit, reclaim-unit, and resurrect families); and a
build-footprint marker, carried by `MobileBuild` and `VTOL_MobileBuild` only. Acknowledgement
groups observed: 0, 1, 2, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 15, and 19 (groups 3, 10, and 16
through 18 are unused by the templates). Class parameter values observed: 0x00, 0x02, 0x03,
0x08, 0x10, 0x12, 0x13, 0x18 (the class parameter stays opaque `TODO(question)` [P0-07]).

Per-record resolution of those family names (same 2026-08-27 audit dump): the attack family
is the eight orders `Attack_NoMove`, `Attack_Chase`, `Attack_Kamikaze`, `AttackSpecial`,
`AirStrike`, `AirToAir`, `AirToGround`, and `AirToGroundHover` — `AttackUType` carries no
helper despite its name. `VTOL_Landing` carries the acknowledgement helper with the unload
family. `RepairUnitNoMove` carries the acknowledgement helper **without** path markers while
`RepairUnit` carries path markers. The path-marker side includes the queued variants `QMove`
and `QPatrol` with the move and patrol families, and the point order `Reclaim` alongside
`ReclaimUnit`. Identity census: 29 none, 19 acknowledgement, 17 acknowledgement plus path
markers, 2 build-footprint.

**Retained-opaque static gate-mask bits — Established census, no located reader.** The union
of the 68 static masks is bits 1-11, 16, 17, 18, 19, 20, and 24. Beyond the named bits above
(9, 10, 18, 20 static; 14 and 21 exist only at runtime and appear in no static mask), the
following static bits have no located consumer and must be stored opaque, not interpreted:
bit 1 (0x2 — `Move_Ground`, `Patrol`, `RepairPatrol`, `VTOL_Move`, `VTOL_Patrol`,
`VTOL_RepairPatrol`); bit 2 (0x4 — `MakeSelectable`, `Wait`, `AttackUType`,
`WaitForAttack`, `GetBuilt`, `BeCarried`, `Paralyze`, `SelfRepair`, `BuildingBuild`);
bit 3 (0x8 — the build family: `BuildingBuild`, `HelpBuild`, `MobileBuild`, `VTOL_HelpBuild`,
`VTOL_MobileBuild`); bit 4 (0x10 — `Suppress`, `Patrol`, `RepairPatrol`, `VTOL_Patrol`,
`VTOL_RepairPatrol`); bit 5 (0x20 — the cloak/standing family, `Guard_NoMove`, `Paralyze`,
`BeCarried`, `GetBuilt`); bit 6 (0x40 — the cloak/standing family, `BuildWeapon`,
`SelfDestruct`); bit 7 (0x80 — `Attack_NoMove`, `Attack_Chase`, `AttackSpecial`); bit 8
(0x100 — the cloak/standing family, `BuildingBuild`, `BuildWeapon`, `MobileBuild`,
`VTOL_MobileBuild`); bit 11 (0x800 — `Reclaim`); bit 16 (0x10000 — the cloak/standing
family, `BuildWeapon`); bit 17 (0x20000 — `Standby`, `Standby_Mine`); bit 19 (0x80000 —
`BuildWeapon` only, alongside its bit 18); bit 24 (0x1000000 — `Standby_Mine` only).

### 3.2 Order record

**Established fact:** An order instance is an 86-byte record holding: the
descriptor identity; a handler-private phase byte; the dynamic gate mask of
requirements currently awaited; a deadline tick, or -1 for none; the owning
unit; a target smart-reference that may be null; a goal position as three
16.16 fixed-point world values; a guard/fight anchor point as two 16-bit
values; a cached target position as two 16-bit values; three general
parameters; a copy of the descriptor's static gate mask; a creation-tick
snapshot; the link to the next record; and the accumulated satisfied-gate bits.

The three general parameters are reused per order family. For an attack they
are the weapon-slot or stance selection, an orbit substate, and the pursuit
leash with zero meaning unlimited. For a build they are the unit-definition or
template index and the remaining build count. For a guard the first is the
standoff radius. For a mobile build the third is a blocked-area retry counter.

A unit keeps two queue segments: a front queue and a rear segment, each with
its own anchor on the unit.

### 3.3 Queue pump

**Established fact:** The pump walks the queue from the front head each unit
tick and, for each record:

1. If the record's deadline has arrived, the deadline is cleared and the
   lowest satisfied bit is set — an expired wait counts as satisfied.
2. The satisfied set is the record's own satisfied bits combined with the
   unit's capability word, intersected with the record's dynamic gate mask.
3. **If the record has a nonzero gate mask and nothing in it is satisfied, the
   walk stops for this tick.** A blocked head therefore stalls every later
   order behind it, and rear-segment records sit behind any front blocker.
4. Otherwise the satisfied bits are consumed from both the unit's capability
   word and the record, the dynamic gate mask is cleared, and the handler runs.

The handler's return code drives the queue [P0-07][P0-08]:

| Code | Effect |
|---:|---|
| 0 | reset the phase to zero and continue walking |
| 1 | advance the phase by one and continue walking |
| 2, 4 | continue walking unchanged |
| 3 | set the lowest gate bit, set the deadline to the current tick plus 30 plus a random value below 15, and continue — range 30 to 44 [P0-08] |
| 5, 8 | unlink, free, and continue |
| 6 | move the record to the tail of its segment and continue (primary); in the secondary pump remove the single record and return without tail yield |
| 7 | free every record on both segments and return — this is cancel-all (primary); in the secondary pump remove the single record and return without cancel-all |
| 9 | set a completion flag; if the record is last, reset its phase and set the randomized deadline `tick + 30 + random below 30` (range 30 to 59) [R-P0-01]; otherwise unlink and free |
| above 9 | delegate to the order expiry helper and return — helper unlinks, cleans, and frees the single record with no random draw and no whole-queue cancel; whole-queue cancel is exclusively code 7 [P0-08] |

**Audit note — the code-9 last-record wait [R-P0-01]:** the earlier reading
[P0-08]/[SC8] and document 05's "Queue pumping and result codes" stated that
the code-9 last-record wait drew a random value below 15 like code 3 (range
30 to 44). The direct recovery in R-P0-01 (section 8.3) shows the code-9
last-record arm draws a random value below 30 — range 30 to 59 — and that only
the code-3 arm draws below 15. The 30-to-59 range is the contract; document
05's table is superseded by section 8.3. **Confirmed by pump re-export
(2026-08-26):** the primary pump's switch shows the code-3 arm loading the
bound 15 and the code-9 last-record arm loading the bound 30 into the SAME
shared draw epilogue (`gate |= 1`, `deadline = tick + draw + 30`); the two
corpus arms conflicted only because the bound is a register load, not a push,
and earlier censuses searched for a push. The code-9 non-last arm unlinks and
frees. The secondary pump draws only for its code-3 arm; its code-9 arm is a
plain remove, and the above-9 delegate never draws.

Consequences a reimplementation must preserve: one pump call can cascade a
record through several phases in the same tick until a waiting or blocked code
appears; a handler that returns an out-of-range phase code at or below 9
follows that code's row, while above 9 it takes the single-node expiry helper;
waits are quantized to 30 to 44 ticks for code 3 and 30 to 59 ticks for the
code-9 last-record wait [R-P0-01]; and goal writes and slot binds made by a
handler are visible to later handlers in the same cascade, while freed records
are invisible immediately. The movement and path services fill satisfied bits
between pump visits — a blocked record's gate can be satisfied from outside
the pump — and an arrival bit set during a unit's movement integration is
observed by that unit's NEXT pump visit, not the current one (one-tick
latency; sections 1.1, 8.3).

**Supported inference and Unknown — queue waits and producer assignment
[P0-07][P0-08]:** The bodies for interrupt wake bits 2 (cancel-current) and 8
(Construction stopped) are known — for the first, a metal refund of
trunc((1 − remaining)·cost) plus a cause-9 kill; for the second, a single
count decrement after which the restart path frees the node when the count is
exhausted — and both bodies are confirmed in the construction handler
(2026-08-26). The mask-2 delivery path is now established: node cleanup
invokes the handler with mask 2 whenever the removed record's state-mask byte
still has bit 1 set, so the producers of the cancel notification are exactly
the removal paths (counted cancel, non-queued purge, pump removals, death/
capture teardown). No instruction anywhere ORs bit 2 or bit 8 into a record's
satisfied word or the unit's capability word (bounded census of every writer
of both words over the whole instruction listing), so the Construction-stopped
WAKE-BIT producer remains `TODO(T25)` and must not be invented
[R-P0-09][R-P0-10] (full semantics in section 3.8); the flag-byte dispatcher
the audit once named as the interrupt site operates on the unit's
activation/building flag byte, not on order wake bits. The bounded census for
the small class parameter above (3901 boundaries) similarly
found no reader. All producers — HUD, AI, InitialMission, COB, network, rally
inheritance, and factory completion — enter through the common queue tail-append
path that coalesces only at the tail or inserts immediately after the active
marker; there is no separate producer-specific queue [P0-07].

**Established fact:** Insertion is not a plain tail append. A new record whose
descriptor selects the front segment is inserted **immediately after the
currently active order** — exactly one record carries the active marker flag;
with no marked record it appends at the tail, so repeated interface adds still
produce first-in-first-out ordering. Issuing a non-queued order first purges
every unprotected queued record (records lacking the purge-survivor bit are
unlinked and freed), and issuing a front-segment record drops leading
auto/default records (those carrying the auto-op flag) wherever they live. A
record whose descriptor carries the rear-segment selection flag head-inserts at
the front of that segment instead, inheriting the old head's auto flag when
present. Idle default-operation records are created by the primary pump
itself — never by insertion — only when its list is empty, the owner player
state byte holds one of the two computer-player states, and the definition
names a default op; such a node is allocated in non-queued mode, constructed
with the auto flag, and head-inserted into the list the op's descriptor
selects (a secondary-class default op therefore lands in the rear segment).

In queue-modifier terms [P0-08]: a non-queued issue is **Replace** (purge
unprotected queued records, then insert the single record), a queued issue
including Shift-held is **Append** (insert after the active marker without
purging), Shift-queue (`QMove`/`QPatrol` family) is an alias with identical
mechanics except for the descriptor's queued class, and the pump's own
empty-list creation is **Internal-Auto** (head-insert with the auto flag
inherited from the old head). The descriptor's rear-segment gate flag selects
the list for every modifier. At runtime the `QMove`/`QPatrol` records share
one handler whose entire behavior is: set the deadline to the current tick
plus 60 and return the move-to-tail code — a pure 60-tick delayed tail-rotate
with no goal binding (2026-08-26).

**Established fact:** Counted adds coalesce only at the tail. A counted
insertion walks to the tail of the selected segment; when the tail matches
BOTH the operation selector and the type parameter of the request it adds the
count to that record's remaining-build-count parameter instead of allocating.
Distinct products are never merged, and an identical product separated from
the tail by other records is never merged — that tail simply does not match.

**Established fact:** Cancellation by negative count removes tail-most
matches. The walk remembers the LAST (tail-most) record matching the
operation selector and type parameter; a request smaller than that record's
remaining count merely subtracts, while an equal-or-larger request consumes
the remainder into the next iteration and frees the record. A freed non-head
record is marked with a **tombstone bit**: the tombstone exists precisely so
that the cleanup notice skips the weapon-target-clear step for records
cancelled out of mid-queue position.

**Established fact:** Record cleanup follows a strict order: restore the
record identity; if the record's state-block byte has bit 1 set, invoke the
operation handler with the cancel-notification mask; if the
StopBuilding-pending flag is set, emit `StopBuilding` plus its network event
and clear the flag; release the presentation payload; and ONLY for a
non-tombstoned record call the weapon-target-clear helper, which resets all
three weapon-slot build targets and fires `TargetCleared` for each that was
set. Because the tombstone test compares against the front anchor regardless
of which segment the removed record lived in, `BuildWeapon`/`SelfDestruct`
removals are effectively always tombstoned and never emit that notification.

### Closed — handler retry and pre-reject mapping [R-ORDER-02 §1] (2026-08-27)

This closure answers the open orders question that the per-handler
empty/stale/blocked/budget-delayed code mapping "remains inference": the
satisfied-bit producers, the pump visit rules, and every handler that carries
a retry budget have now been traced directly, on top of the handler state
machines already established in sections 8.3 and 10.2–10.3.

**Established — pre-reject exists only at issue time.** An order can be
rejected before entering a queue only by the command resolution and insertion
path of section 3.4 (capability gates, the empty-name reject sentinel, and
the non-queued purge). Neither pump nor any handler re-tests the descriptor's
capability gates afterwards: once admitted, a record leaves the queue only by
completion (code 5), a removal code (5/8, 9 on a non-last record, the
secondary pump's 6/7/9, or the above-9 expiry delegate), or cancel-all (7).
There is no later validity re-check that pre-empts a queued order; a stale
target or unreachable goal is handled by waiting, re-arming, or completing —
never by silent rejection.

**Established — the re-queue engines are exactly four.** A handler that does
not finish its order re-queues work through one of:

1. **Wait (code 3):** the pump sets the lowest gate bit and the deadline
   `tick + 30 + random below 15`; the handler runs again on expiry and
   re-tests. Unbounded — no wait counter exists in the pump.
2. **Re-arm (code 9 on the last record):** the pump sets the completion flag,
   resets the phase to zero, and sets the deadline `tick + 30 + random below
   30` (range 30 to 59). The next visit re-runs phase 0, which rebinds the
   movement goal or marker: the payload installers release the previous
   payload and wipe the record's satisfied bits 0x20 through 0x200 on every
   install, so a re-arm restarts with a clean satisfied word and a FRESH path
   request. This is the engine's only reset-and-retry loop, and it is
   unbounded (one random draw per cycle).
3. **Blocked-head stall:** a record whose armed gate is unsatisfied halts the
   walk with no deadline and no counter — the stall lasts exactly as long as
   no outside writer (movement, path service, or wake bit) raises a gate bit.
4. **Rotate (code 6):** re-queue at the tail — the patrol waypoint cycle and
   the 60-tick delayed tail-rotate of the queued-move family.

**Established — the secondary pump never delivers satisfied bits.** A
rear-segment record is dispatched only when its gate mask is empty or its
deadline has arrived (the deadline compare is unsigned, so the -1 sentinel
reads as not-due); the pump clears the gate and invokes the handler with an
EMPTY satisfied set. There is no expiry bit, no satisfied-word read, and no
capability-word consumption. Rear-segment orders (the stockpile and
self-destruct pair) therefore run purely on their own deadlines, and
movement or wake bits can never drive them.

**Established — handler-level rejections.** Cancel-all (7) is produced by
handler pre-checks: the ground move handler while the unit is attached to a
carrier, the air move handler when the unit is dead or cannot fly, both move
handlers when the record's phase byte is outside the machine (a corrupt phase
cancels the whole queue), the ground patrol handler when the unit is dead,
and the attack/guard machines documented in section 3.5 (disengaged stance,
orbit-substate overrun, out-of-range phase). Abandon (8) is produced by the
attack-chase leash/disengage family and by the mobile-build give-up below.
The traced rejection sites are these; the closure of ORD-04 does not depend
on the set being exhaustive, because the pump maps both codes to queue
effects (7 cancel-all, 8 plain removal) regardless of which handler emits
them.

**Established — the complete census of bounded retry behavior.** Exactly
five families carry a retry counter or budget; every other handler retries
unboundedly or waits on outside bits:

| Family | Budget | Arithmetic |
|---|---|---|
| `MobileBuild`, `VTOL_MobileBuild` | blocked-area give-up | a per-record counter (the record's progress field, zeroed by the handler's setup path): each blocked attempt notifies "Waiting for target area to clear", increments the counter, waits EXACTLY 30 ticks (no random draw), and continues while the counter is at most 10; the first blocked visit with the counter above 10 notifies "Target area was blocked" and returns 8 (remove). Eleven 30-tick waits, then give-up. |
| `Wait` | timeout drain | the scan variant subtracts `150 + random below 30` per failed scan until the budget is exhausted (section 3.8) |
| `BuildingBuild` | fixed retries | blocked exit retries in exactly 15 ticks; allocator failure in exactly 300 (section 3.8) |
| `BuildWeapon` | fixed waits | the stockpile machine's fixed 5/10/300-tick waits |
| VTOL landing | pad retry | the `0` / `30 + random below 15` retry protocol (section 10.2) |

**Established — the path-outcome mapping for the movement families.** The
movement layer raises satisfied bits through the record's movement-goal
payload: arrival raises 0x20, a route released before arrival raises 0x40,
and a goal-handle detach or rebind raises 0x80. The path request itself
reports route-found/no-route by raising 0x100/0x200 into the record's
satisfied word — but the move and patrol handlers arm the gate mask 0xE0
(bits 0x20/0x40/0x80) only, so the path-status bits are raised and then
MASKED OUT: they can never satisfy a movement record, and a route that has
not been turned into arrival-or-release motion satisfies nothing.

| Outcome | `Move_Ground` | `Patrol` | `VTOL_Move` | `VTOL_Patrol` |
|---|---|---|---|---|
| empty route / budget-delayed (nothing published yet) | blocked-head stall at gate 0xE0 — no timer, no draw; self-heals when the route publishes | same stall at the armed gate | same stall | same stall |
| stale (route released before arrival, 0x40, or handle detach/rebind, 0x80) | phase 1 → code 9: last record re-arms (phase 0 REBINDS the goal handle and re-issues the path request; wait 30–59; unbounded loop); non-last records are removed | phase 2 → code 6 (rotate to tail, phase reset to 1 — the waypoint cycle absorbs the release) | the raised bit satisfies the 0xE0 gate, so the machine advances a phase per satisfied visit; the fresh marker re-issues the approach each phase-0/1 visit | same phase-per-visit advance; the orbit re-arms its marker each cycle |
| blocked at destination (mover blocked flag) | no satisfied bit and no handler involvement — the mover caps speed and clamps (section 8.2) while the route keeps its remaining points; the order keeps waiting | same | same | same |
| arrival (0x20) | code 5, complete, with the move-complete notification | code 6 rotate to the next patrol point | phase 2 completes unconditionally on the first satisfied visit after the last re-arm | code 6 rotate; with no next point, an in-handler wait `tick + 30 + random below 30` (return 4) |

The movement families therefore have NO per-handler retry counters and no
pre-reject: an unreachable or budget-starved goal stalls or loops forever,
consuming one random draw per re-arm cycle, until the player cancels or the
goal becomes reachable. **Unknown:** whether the route-release event fires
for a route that was NEVER published (a goal the search cannot reach at all).
If it fires, the unreachable case is the 30–59-tick rebind loop; if not, it
is a silent indefinite stall with no draw. Settling it requires enumerating
the movement wrapper's per-tick states that invoke the release callback —
the wrapper state machine itself is the one untraced piece.

### Closed — cleanup callbacks: tombstone, TargetCleared, StopBuilding [R-ORDER-02 §2] (2026-08-27)

**Established — tombstone mechanics.** The tombstone bit is set at removal
time on every freed record EXCEPT one that is the primary segment's front
head at that moment; the comparison is always against the front segment's
anchor regardless of which segment the record occupies (all removal paths —
both pumps, the above-9 delegate, the insertion purge, the leading-auto drop,
and cancel-by-negative — share this shape). Consequences: the front record of
the primary queue is never tombstoned; every rear-segment record (only
`BuildWeapon` and `SelfDestruct` live there) is ALWAYS tombstoned when freed,
because it can never be the primary front head. The bit gates exactly one
cleanup step — the weapon-target clear below; the cancel notification and
the StopBuilding emission run regardless of it. The completion flag the pump
sets on code 9 is write-only state as far as the bounded constant census
reaches: no pump, cleanup, or traced handler body tests it, and the remaining
constant's sites belong to other subsystems' state.

**Established — cleanup order, refined.** The earlier text's "state-block
byte" is refined: the cancel-notification guard is the record's DYNAMIC GATE
mask — the same field the pump consumes — still holding bit 1 (value 2) at
removal; a record removed while waiting on that bit delivers the
cancel-current notification through its own handler. The presentation-payload
release also clears the owner unit's displayed-payload latch when that latch
points at the record's payload, before releasing the payload itself.

**Established — the TargetCleared contract.** The weapon-target-clear helper
invoked for a non-tombstoned record walks the three weapon slots in order.
Per slot, the notification fires only when BOTH: the slot's control byte has
bit 1 set (slot assigned) and bit 4 clear — bit 4 is then set — AND the
slot's target words are not already empty (16-bit target word non-zero, or
the 16-bit companion word not at its empty sentinel of -32768); the words are
then reset to 0 and -32768 and the engine arranges the owner's COB function
named `TargetCleared` with the seven arguments `(0, 0, 1, slotIndex, 0, 0,
0)`. The event is script-only — no network event accompanies it — and the
arrange is a no-op when the unit's script defines no such function. Two
sibling entry points serve mid-life clears by handlers: one with the mirror
guard (fires only while bit 4 is set, then clears bit 4), and one
unconditional. The pump itself uses the unconditional form when a dispatched
record's satisfied combination carries the 0x10000 bit (three unconditional
slot clears); **Unknown:** which handler arms that gate bit and what raises
the bit into the satisfied word.

**Established — the StopBuilding emission contract.** The
StopBuilding-pending flag has exactly one writer: the StartBuilding emitter,
which resolves the function named `StartBuilding` in the owning unit's COB
script, arranges it with the seven arguments `(0, 0, 1, value16, 0, 0, 0)`
where `value16` is the low 16 bits of the issuing record's identity
(Supported inference for the value's meaning — read directly off the
emitter, no retail script consumer traced), emits the matching network
event, and sets the flag on that record. Its nine call sites are exactly the
nanolathe/assist handlers: `MobileBuild`, `VTOL_MobileBuild`, `HelpBuild`,
`VTOL_HelpBuild`, `Capture` (two sites), `Reclaim`, `Resurrect`, and
`RepairUnit`. Cleanup emits the counterpart on EVERY removal path (all pump
codes, the insertion purge, cancel-by-negative, the expiry delegate): resolve
`StopBuilding` by name in the owner's script, arrange it with seven zero
arguments, emit its network event, and clear the flag. The emission is NOT
tombstone-gated. Interaction with the pump tables: a code-9 LAST-record
re-arm keeps the record, so its `StartBuilding` keeps running with no
`StopBuilding`; every actual removal (primary 5/8/9-non-last/7/above-9,
secondary 5/8/9/6/7, purge and cancel paths) emits it for a flagged record
before the tombstone-gated TargetCleared step runs.

### 3.4 Command resolution

**Established fact:** Player and network commands do not name descriptors
directly. A resolver takes a **command code from 1 to 14**, the acting unit,
an optional target, and an optional ground position, and produces a canonical
command *name*, which is then looked up to obtain the descriptor identity. A
failed capability gate produces the reject identity.

| Code | Meaning | Capability gate | Resolves to |
|---:|---|---|---|
| 1 | contextual, from a left-click with idle latch (the default left-button action when a selection exists) | delegates to the codes below | hostile and able to attack becomes an attack order; a damaged or unfinished friendly becomes repair or build assistance; a transportable target becomes a pickup; a followable target becomes follow; a feature at the position becomes resurrect when eligible, otherwise reclaim; otherwise a move |
| 2 | move | can-move | a dead unit target becomes a queued move; a hostile target with the capture or reclaim capability becomes capture or unit-reclaim; a friendly build target becomes build assistance or repair; a landing pad becomes landing; a carriable target becomes pickup; a followable target becomes follow; otherwise ground or air move |
| 3 | attack a unit | can-attack | suppression for the non-air special case; the four air-attack variants chosen by weapon and target class; the kamikaze variant for a unit flagged for it; the no-move variant for a structure; otherwise the chase attack |
| 4 | special attack | special-attack capability | the special attack |
| 5 | unload | can-unload | ground or air unload; a pad target becomes landing |
| 6 | load or pick up | target passes the carriable test | ground or air pickup |
| 7 | guard or follow | can-guard, target friendly | ground or air follow |
| 8 | assist or repair | target is reachable by a nanolathe | build assistance while the target is unfinished, otherwise repair |
| 9 | patrol | can-patrol | a patrol with no target becomes the queued patrol; a builder with the repair-patrol capability becomes the repair patrol; otherwise ground or air patrol |
| 10 | internal | — | **no resolver case exists**: neither resolver has a code-10 arm; both fall to their defaults — the identity-form resolver returns the `GetBuilt`-shaped identity 0x13, the name-form resolver writes an empty name (reject) — and no caller inside the bounded census emits code 10 [P0-R02] |
| 11 | teleport | none | teleport |
| 12 | reclaim or resurrect | can-reclaim | a wreck feature with the resurrect capability becomes resurrect; otherwise feature reclaim or unit reclaim, in the ground or air variant |
| 13 | capture | can-capture, target hostile and differently owned | capture |
| 14 | mobile build | the unit's build list is non-empty | ground or air mobile build |

Hostility comes from a per-side diplomacy byte on the acting unit's
definition, indexed by the target's side. VTOL versus ground variants are
chosen by the canfly flag on the acting unit's definition; construction work
uses the standard builder gates and stockpile `BuildWeapon` is capped at its
buildTime with cost deltas applied via the two-resource versus energy-only
gates [P0-07].

**Mouse-button assignment is closed.** Every world command — selection, move, attack, and the contextual delegation above (code 1) — is issued with the **left** mouse button; the **right** button never issues an order. A right click cancels the armed command latch (returning it to idle) or, when the latch is already idle, clears the current selection. The battle input pump routes left-button press and release through the single-click and drag-rectangle selection paths and the order dispatcher, while right-button press is routed exclusively to the cancellation path that returns the latch to idle and, when idle, performs the deselection branch. The cursor contract shows the same polarity: every latched shape's advertised action fires on left-click; the right-click column is empty or a transition back to the normal cursor [07 §8][07 §9].

### 3.5 Attack-chase states and guard assistance

**Attack-chase state machine [P0-07][P0-08].** Before its phase switch, the
chase-attack handler runs admission pre-checks in order: satisfied bits
indicating abandonment, a missing target, or a disengage bit combination return
the abandon code; and when a pursuit leash is authored, a horizontal distance
from the order's guard/fight anchor at or beyond the leash also abandons — this
leash is what makes the *Fight* command return to its post. The phases are:
admit (require ground unit, reset goal to own position, pick a weapon slot if
none stored), engage setup (range-gate, bind fire slots, single tick),
combat-maneuver orbit cycle, and re-engage (rebind on range or release the fire
slot, both waiting 30 ticks). The orbit cycle runs an eight-state substate
machine 0 through 8: approach at standoff distance; strafing steps that halve
the distance when vertical separation exceeds eight units; closer approaches at
half and zero standoff; then two banded-goal states (inner/outer radii at
standoff/half and double/half); then wrap to zero. Goal arrival within those
states uses strict thresholds: horizontal distance at or below two world units
and boundary absolute difference below three counts as arrived; otherwise the
handler re-issues a wait [P0-08]. A substate at or beyond nine, or any handler
phase beyond three, cancels the unit's entire queue via the cancel-all return
code.

### Closed — guard assistance retargeted and sized [R-UNIT-06 §1] (2026-08-28)

**Correction — supersedes this section's earlier "Guard assistance triggers
[P0-08]" paragraph.** That paragraph said the guard keeps a per-unit **dedup
array** (a "dedup latch") gating re-enqueue, labeled branch (a) *build assist*
toward the ward's construction op, described branch (b) as *acquiring*
candidates with id-latch dedup, and closed with "dedup latches prevent
immediate re-enqueue". The direct handler traces show there is **no dedup
array and no latch in either guard handler** — re-enqueue discipline comes
from the pump's deadline cadence and from satisfied-bit gates — branch (a) is
a **combat join** (attack the ward's enemy), and branch (b) **re-targets**
slots onto the ward's enemy rather than acquiring anything. The corrected
contract below is **Established** (direct, both the ground and air guard
handlers).

**Established — entry gates, ground guard, in order.** A missing ward
completes the order (code 5, not the abandon code); a guard that is itself
carried cancels its whole queue (code 7); a ward whose definition can fly
removes the order (code 8 — a ground guard follows only ground wards); a
phase byte beyond 1 cancels all (code 7).

**Established — admit phase (ground guard, phase 0).** Set the `Guarding`
state label; clear all three weapon-slot build targets (the weapon-clear walk
with its TargetCleared signals); compute the follow radius from the two
footprint-width words (the signed footprint words the transport size gate
also reads): `radius = (myFootWidth + wardFootWidth + 2) << 4`; draw **one**
simulation RNG draw of a full circle (`RNG(65536)`) for the anchor direction;
store the anchor offset triple as `(-sin(h)·radius, 0, -cos(h)·radius)`
through the shared fixed-point sine table with round-to-nearest, with the
radius scaled into 16.16 for the multiply — the resulting anchor band around
the ward is about `(sum + 2) · 16` world units. Advance to phase 1 (code 1),
which the same pump cascade re-enters immediately.

**Established — the assist evaluation (phase 1), top-down:**

1. **Combat join.** When the ward's engagement-target reference (a
   unit-reference link on the ward, see below) is set, the ward's owner's
   diplomacy byte toward the guard's side reads zero (allied), the satisfied
   bits include the guard's re-arm bit (bit value `0x10`, see below), and the
   ward target's definition is **not** in the guard's no-chase-category bit
   array, the guard resolves the **attack-a-unit** command (code 3 of the
   command resolver) against that target in queued mode and enqueues it at
   the queue tail; on a successful enqueue the record's dynamic gate clears
   and the handler returns the wait code. The queued-mode attack also bypasses
   the standing-order gates the non-queued attack issue applies.
2. **Slot re-target (auto-fire support).** Skipped entirely when the guard's
   standing-move bits (18–19) and standing-fire bits (20–21) of the status
   word are all clear. Per slot 0..2, requiring the slot assigned bit, the
   slot tracking bit, and a weapon whose command-fire-only definition bit is
   clear: resolve the slot's stored target; when the slot has **no target**,
   that target is **out of range**, or the target's definition **is** in the
   guard's per-slot bad-target-category bit array, the slot is **rebound onto
   the ward's engagement target** (the same unit branch 1 attacks). Slots
   already holding a legal in-range target are left alone. There is no
   acquisition and no dedup latch.
3. **Repair/assist the ward.** When the ward's health (signed word) compares
   below its definition's maximum-damage word and the guard's definition has
   the builder bit, resolve command code **8** (assist-or-repair: help-build
   while unfinished, the repair order otherwise) through the canfly-forking
   resolver against the ward itself; on a resolved name, clear the record's
   goal payload, allocate and tail-append the new record (target = ward),
   clear the dynamic gate, and return the wait code. An allocation failure
   returns the wait code with no insert.
4. **Join the ward's order.** When the ward's front order exists with a
   nonzero descriptor, **both** guard and ward definitions have the builder
   bit, the front order carries flag `0x100000`, and its target is not the
   guard itself: when the front order's descriptor is the guard-resolved
   mobile-build or factory-build descriptor, enqueue **help-build** (resolved
   through the canfly fork, so VTOL guards get the VTOL variant) toward the
   front order's target with the front order's goal position; when it is
   instead a payload-carrying queued order, enqueue a **copy** of that order —
   same descriptor, same target, same goal. Otherwise fall through.
5. **Follow maintenance.** Install a ground point goal at the ward's position
   plus the stored anchor offset; arm the record's own dynamic gate with the
   re-arm bits (`0x18`, values `0x08` and `0x10`); set the deadline to
   `tick + 30` **fixed** (no draw) through the shared deadline setter, which
   also arms gate bit `0x01` — so the record re-dispatches on the 30-tick
   deadline and on whichever re-arm event lands first; return the continue
   code. The anchor direction itself was drawn once at admit; maintenance
   draws nothing.

**Established — the engagement-target link.** The ward's engagement-target
reference is a unit-pointer link on the unit record, saved/restored by the
unit save block (its save field sits beside the carrier-link field) and
cleared by the per-unit tick refresh helper each tick. **Supported inference:**
it is the ward's current combat target — the consumers (attack join +
no-chase filter + slot re-target) admit no other reading. **Unknown:** which
producer sets it during ordinary play; a bounded census over the decompiled
function set and an instruction-pattern scan of the code sections found only
the initializer zero, the save pair, and the tick-clear write.

**Unknown — the guard's re-arm bit producers.** The guard arms its gate with
bit values `0x08` and `0x10` (step 5) and branch 1 consumes `0x10`. The
node-pending writers found anywhere are: the three goal-payload installers
(which only clear bits 5–9), the single satisfied-bit raiser whose callers
raise `0x20`/`0x40`/`0x80`/`0x100`/`0x200` only (bounded: all nine raiser call
sites), the node constructor (zero), and the pump's deadline expiry (bit 0).
No writer of `0x08`/`0x10` was located, so these belong to the same
unlocated-writer family as the mask-2/mask-8 interrupt bits and the
satisfied-bit-`0x10000` weapon-slot clear recorded in the Missing list. The
consumer semantics above are Established; the producers remain open.

**Established — air guard differences.** The VTOL follow handler adds an
interrupt-bit pre-check (satisfied bits `0x48` fail the order) and a
sentinel-sector diversion to a loiter path, but repeats branches 1–4
byte-equivalently; its follow maintenance installs airspace circling (radius
`0x80` arrival) and arms the same gate bits. The branch-1/branch-2 eligibility
chain (diplomacy, `0x10` gate, no-chase array, standing-order bits, per-slot
re-target) is identical.

**Audit note — attack-chase orbit substates (2026-08-26):** the chase handler
was re-exported and the orbit substate arithmetic is now direct, confirming
and refining the paragraph above: the leash test is a strict `leash <=
distance from the guard/fight anchor` removal (the anchor pair is the order's
stored 16-bit pair); the admit phase seeds the goal with the unit's own
position and picks the weapon slot; the engage-setup range-gates through the
weapon-class helper and binds two slots before arming the dynamic gate
`0x13808`; the orbit substate counter (0..8) drives: default states — a
vertical separation beyond exactly 8 world units rebinds the target with the
standoff HALVED (substate 6), otherwise the orbit binds a goal at the target
minus the standoff·direction step where the direction is the bearing to the
target minus 90 degrees plus a full-circle random draw (±90° orbit choice),
with radius parameter one QUARTER of the standoff; substate 5 halves the
standoff variable; substate 6 binds the target with radius parameter ZERO;
substates 7 and 8 bind the banded goal pairs `(standoff, standoff/2)` and
`(2·standoff, standoff)`; substate 8 wraps to 0. Re-engage re-range-gates and
rebinds on pass (gate `0x148e8`, 30-tick wait) or clears the slots on fail
(gate `0x100e8`, 30-tick wait). The only remaining inference is the standoff
VALUE itself (produced by the weapon-slot engagement-distance helper); every
ratio and threshold above is direct.

**Established fact — goal vtables and queue modifiers [P0-08]:** Leash and orbit
behaviour is per-order via virtual tables with strict arrival `dist <= 2` and
boundary `abs diff < 3`. Queue modifiers map as: Replace (non-queued) purges
unprotected records before inserting after the active marker; Append and
Shift-queue (including `QMove`/`QPatrol`) both insert after the active marker
without purging, differing only in descriptor class; Internal-Auto is the pump's
own empty-list head-insert that inherits the old head's auto flag. The rear flag
selects the segment for every modifier.

### 3.5 Construction and economy interaction boundary

**Established fact:** Construction and factory work use a fixed-size queue node
shared by both queue segments. Its logical fields are an operation
selector; state and state mask; next eligible tick; context and payload values;
target unit or definition; fixed-point target position; operation amount or
definition identifier; a phase timer; a retry/range value; queue flags and
retry mask; and the next-node link. **Factory production nodes ride the primary
list**: a direct dump of all four static descriptor template batches shows bit
18 (the rear-segment selection flag) carried by exactly two of the 68
descriptors — `BuildWeapon` (gate mask `0xc0140`) and `SelfDestruct`
(`0x40040`) — so there is no factory-specific queue. Products are serialized by
the primary pump's front-blocking rule alone, while the secondary segment —
exclusively `BuildWeapon`/`SelfDestruct` records — is pumped by a separate
front-to-back pass that skips not-yet-due records (later entries still run)
and resumes from its head after every handled result. That secondary pump has
no tail-yield and no cancel-all: result codes 6 and 7 both REMOVE the single
record and return, and codes 5, 8, 9, and above 9 unlink and free as usual.

**Established fact:** The unit sweep processes construction and factory queues
before the order/path service. Construction work consumes worker progress and
can update target remaining fraction, target health, flags, and callbacks.
Resource admission can deny work without changing the target.

**Established fact:** Repair, unit reclaim, feature reclaim, capture, and
resurrection are separate operation families. Feature reclaim removes a map
feature and returns its energy and metal values through builder accounting; it
does not use exactly the same per-tick unit-build helper.

**Established fact:** Build-site selection is separate from path search. It
enumerates perimeter candidates, rejects candidates through footprint and yard
placement validation, ranks a bounded candidate list, and supplies a point goal
to path search.

**Established fact:** Spawned units receive their `GetBuilt` order
synchronously during the builder's work phase: at the moment the nanoframe is
created the factory resolves the `GetBuilt` name and inserts a `GetBuilt`
record onto the PRODUCT's own primary queue in queued mode with zero count,
alongside registering the builder link and raising the StartBuilding edge.

**Established fact:** Rally inheritance is the product's `GetBuilt` handler
walking the BUILDER's primary queue front to back and re-enqueueing every
queued move or patrol record onto the product, copying the position payload
and preserving traversal order — patrol rallies reproduce as patrols, multiple
waypoints are inherited in queue order, and the factory itself never moves. If
nothing was inherited the product receives a queued `Park` order instead.
Standing-move bits (18–19) and standing-fire bits (20–21) copy from builder
to product only when both units carry the building-class/alive state bit
(bit 28) and NEITHER carries bit 14 (the auto flag, which also marks the
cloak/initial-posture posture the completion transition applies); the
experience word copies only for computer-player-owned builders (owner player
state byte value 1) [R-P0-09].
The full production lifecycle, refund arithmetic, and completion transition
belong to document 05. The complete GetBuilt state gates, retry timing,
completion-transition order, and same-tick publication windows are in section
3.8 [R-P0-09].

**Supported inference:** The construction operation table is data-driven and
maps an operation byte to handlers. The precise meaning of every operation byte
is not recovered, so an implementation should keep unknown operations
observable and reject or preserve them rather than assigning new behavior.
The handler SET itself is now closed: every one of the 68 descriptors'
handler identities is recovered from the descriptor table (2026-08-26), so
the residual is limited to the per-phase operation-byte values inside the
construction handler family.

**Unknown:** Exact resource carry and debt, queued admission ratios,
construction operation-byte meanings, and completion callback timing belong to
the economy and construction contract and remain dependencies here.

### 3.6 Mission InitialMission order scripts

**Established fact:** Mission records can carry an interned `InitialMission`
string. Its interpreter runs ONCE per game start, on the loading worker, after
ALL mission units exist — for mission-type-1 games and BetweenMissions
restores only; no other start path reaches it, and it registers with no tick
dispatcher. From the next simulation tick onward the ordinary order pump
consumes the pre-loaded queue exactly as if a player had issued the orders.
Document 08 owns mission-side storage and grammar; this section records only
the order-facing behavior.

**Established fact:** The string is a comma-separated token list processed
left to right by a dispatch table keyed on the leading letter, effectively
case-insensitive. Each queuing verb resolves its order through the descriptor
registry canonical-name lookup and appends one record in queued mode;
positional verbs first classify through the command resolver of section 3.4
(move, attack, unload, follow/guard, patrol), which picks ground or VTOL
variants from capabilities. Numeric coordinates parse as floats scaled by
`65536` into 16.16 world units; times scale by `30` into ticks.

| Verb | Effect |
|---|---|
| `m x,y` | move to x,y |
| `a x,y` | attack ground position (numeric form; suppresses the tail MakeSelectable) |
| `a name` | attack-by-unit-type against the named catalog type; unknown types queue nothing |
| `b name n x,y` | building build when the catalog type has an empty unit name, mobile build at x,y otherwise; count n |
| `bw n` | BuildWeapon stockpile count n |
| `d` | self-destruct via the `SelfDestructFG` front-gate descriptor |
| `g name` | guard the named spawned unit (Ident match first, then Unitname, case-insensitive); unresolved names queue nothing |
| `i name` | board/attach self into the named carrier via the immediate internal attach message — not a queued order |
| `o d1,d2` | writes two 2-bit fields of the unit flag word: bits 17–18 ← `d1 & 3`, bits 19–20 ← `d2 & 3`; no order queued and no issued marker |
| `p x,y,t` | patrol to x,y with t scaled by 30 as timeout ticks (suppresses the tail) |
| `s` | MakeSelectable (suppresses the tail) |
| `u x,y` | unload/transport-drop at x,y |
| `w secs[,n]` | Wait for secs×30 ticks carrying trailing integer n — the trailing integer is a wait-for-unit selector: the handler scans for a matching unit around the acting unit each visit and completes immediately on a match, otherwise it drains the timeout budget in chunks of 150 plus a random value below 30 and waits that long (2026-08-26) |
| `wa name` | WaitForAttack targeting the named unit, falling back to self when unresolved |

One dispatch quirk is part of the contract: an uppercase-led `W…` token
enters the BUILD block (its second character `w` selects BuildWeapon), so an
uppercase-led token can never be a plain Wait, and wait-for-attack must be
written lowercase-led as `wa`.

**Established fact:** Postlude: when at least one order was queued, bit 5 of
the unit's class/state word clears; and unless the script contained
numeric-form `a`, `p`, `d`, or `s`, a final `MakeSelectable` queues with zero
auxiliary arguments.

**Established fact:** Malformed input is silent: unknown verb letters,
digits, and punctuation are ignored with scanning resuming past the comma;
well-formed lookups that fail are no-ops (`wa` falls back to self);
move/patrol/unload/wait/flag tokens do not test their conversion counts, so
malformed numbers convert whatever the destination cells already held —
deterministic per stack state, undefined per language spec. A comma-free run
longer than 255 characters overflows the 256-byte tokenizer frame buffer;
retail accepts the risk and a clean implementation should clamp.

### 3.7 Selection, picking, command latches, and build-command UI semantics

**Established fact — drag selection and overlap picking [P1-14]:** Drag
endpoints are converted from world to presentation by subtracting the camera
position and adding the fixed view-pane origin offsets, then sorted
independently on each axis and tested inclusively as `min <= x <= max` on both
axes. Eligible units are visited in ascending pool order; for overlap, the
nearest unit in squared distance wins with strict `<` tie-break, so an equal
distance retains the lower slot. The eligibility predicate requires the active
flag, exact `1.0` health fraction, no disqualifying state reference, and either
no parent or a parent carrying the cargo-parent flag. Fog uses a word bit for
the local player and a byte per viewer; picking requires the local-player word
to be set for the cell, otherwise the unit is treated as absent for selection.

**Established fact — toggle and commit modifiers [P1-14]:** Drag toggle uses
bit 2 of the drag parameter word, giving a commit versus toggle truth table:
clear sets selected and clears outside via bulk pre-clear, set toggles selected
only inside and preserves outside. World-click commit (queued versus replace)
uses a held-key query in the style of `GetAsyncKeyState` for Shift, not the
drag word, so the two Shift sources can diverge. The bulk pre-clear clears
selected membership and the single-select identifier and sets the interface
dirty bit.

**Established fact — mixed selection and control groups [P1-14]:** Command
palette enable is the AND across the selected set — a button is enabled only
when every selected unit carries the capability bit, otherwise it is greyed.
Control-group assignment scans the local player's inclusive unit range in
ascending order; selected units receive the group value while unselected units
already carrying that value are cleared. Group recall takes a preserve argument
from the held Shift query; when clear it clears non-members, and a secondary
branch keyed on a matching unit that also carries the `0x80000000` flag filters
through the authored `CTRL_F` 256-bit type mask indexed by definition
identifier. Digit routing between build-page selection and group recall uses the
battle-mode flag and held Alt query, exactly as documented in the interface
contract.

**Established fact — command latches [P1-14]:** The armed-order latch holds
the order-dispatcher switch key. The GUI button dispatcher arms it by parsing the
button name in a fixed chain — STOP, then ATTACK, BLAST, DEFEND, REPAIR, PATROL,
RECLAIM, CAPTURE, UNLOAD, LOAD or PICKUP alias, and default MOVE — writing the
parsed value only when the button's gate is nonzero, otherwise writing idle.
Each armed write clears the placement-pending flag and plays the immediate-order
versus special-order cue. Two latch values are off-button producers via
placement preview, not via the button chain: TELEPORT and MOBILEBUILD are armed
by the mobile-build and teleport placement preview paths that test site validity
and choose the find-site versus too-far cursor. Escape clears a different latch
flag and returns the latch to idle.

**Established fact — build cancellation and queue modifier mapping [P1-14]:**
Build cancellation walks to the tail-most matching record for the operation and
type; a request smaller than that record's remaining count subtracts, otherwise
it consumes the remainder and frees the record, looping for the remainder.
A freed non-head record is marked with the tombstone bit so cleanup skips the
weapon-target-clear helper; because the tombstone test compares against the
front anchor regardless of segment, BuildWeapon and SelfDestruct removals are
effectively always tombstoned. In queue-modifier terms, a non-queued click is
Replace (purge unprotected records, then insert after the active marker), a
Shift-queued click is Append (insert after the active marker without purging),
Shift-queue family is an alias with identical mechanics, and the pump's own
empty-list creation is Internal-Auto head-insert inheriting the old head's auto
flag; the rear flag selects the list for every modifier.

**Established fact — attack ground versus unit target [P1-14]:** With the
ATTACK latch, the world-click issuer first picks the nearest eligible visible
unit; when a unit is hit and the acting unit's can-attack and hostility gates
pass, the resolver issues a unit-target attack (chase family with cached target
position). Otherwise the click becomes an attack-ground with a world ground
position payload. The BLAST latch (attack-special) always issues a ground
position regardless of any unit hit and draws the area-of-effect rings, while
ATTACK on a unit hit issues the chase orbit.

### 3.8 Factory completion, activation, and rally inheritance [R-P0-09]

**Established fact — product publication [R-P0-09]:** The factory's state-2
success path is ordered: synchronous QueryBuildInfo (output cell 0 seeded −1)
resolves the exit piece transform to a world position; the position is
converted to a product footprint anchor (section 6.3) and validated (section
6.4); the product is allocated at the resolved exit transform — not at the
derived anchor, section 6.3 — as a nanoframe with remaining fraction 1.0,
health 0, and build stance clear; the builder/product link is registered; the
factory's standing-order fields are copied into the product's state fields; a
GetBuilt record is enqueued on the product's PRIMARY queue; the factory's
StartBuilding edge is raised; the builder interface is refreshed; and the
factory node advances to its work state. The GetBuilt insertion precedes the
StartBuilding edge. StartBuilding is an edge callback — the argument-less
deferred form fires only when the cached building bit rises — and is distinct
from the slot-form StartBuilding used by construction-command dispatch, which
carries the producer heading as one unsigned 16-bit argument (section 5.3)
[R-P0-09][R-P0-10].

Allocation refusal (per-definition limit or pool exhaustion) occurs before the
product exists: it prints the established "Unable to create any more units"
message and retries in exactly 300 ticks. A blocked exit validation is a
different pre-allocation path — silent, retrying in exactly 15 ticks. Neither
retry changes the anchor/center ownership of section 6.3.

**Established fact — work-to-completion transition [R-P0-09]:** Accepted
construction work updates the remaining fraction and health through the shared
helper, which admits both resource demands together and stores
`newRemaining = clamp(oldRemaining − workerQuantum/buildTime, 0, 1)` with
`healthGain = trunc(maxHealth·oldRemaining) − trunc(maxHealth·newRemaining)`.
If resource admission rejects either demand, neither value changes and no
completion callback is emitted. If the accepted step reaches 0.0, the shared
helper synchronously invokes the product completion transition before
returning success; that transition can raise the product's deferred Activate
edge. The factory state machine then re-enters its completion state in the
same primary-pump pass.

The completion state follows the helper's product completion transition (and
any possible product `Activate`) with a strict callback/mutation order: the
factory `StopBuilding` falling edge is lowered first in state 4, then the
product completion transition is invoked idempotently again (its unchanged
edges do not re-fire), followed by clearing the presentation payload,
decrementing the queued product count once, refreshing the builder interface,
and restarting the factory node at state 0 — or freeing it when the count is
exhausted. The edge helper fires `StopBuilding` only on the falling edge;
unchanged state does not re-fire. **Correction
(2026-08-28):** the earlier wording that first placed the product completion
transition only in state 4 omitted the shared helper's zero-remaining call;
the helper call precedes the factory edge and state-4 bookkeeping.

The completion transition, gated on the recovered class preconditions
(building-class builder with a build list; building-class product), stores
remaining fraction 0.0, sets the product's completion flag (bit 13 of the
state word), clears the product's build/weapon auxiliary field, raises the
product's Activate edge when the product definition's capability word requests
activation (capability bit 18), and applies the cloak/initial-posture handling
when capability bit 24 requests it (writes the cloak/init byte value and sets
bit 14 of the state word). The normal completion transition is not the
cancel-current interrupt: cancel-current invokes the same transition before a
cause-9 kill and lowers the Activate AND StartBuilding edges together without
decrementing the queued count; construction-stopped (wake mask 8) instead
prints its message, decrements the count once, and leaves the node to restart
(the wake-bit bodies are in section 3.3) [R-P0-09][R-P0-10].

**Established fact — GetBuilt retry and rally inheritance [R-P0-09]:** The
product-side GetBuilt order is a real primary-queue node with three retry
states: while the product remains unfinished it re-arms itself — state 0
retries after 300 ticks, state 1 after 30 ticks, and state 2 blocks waiting on
its wake bit. Once the product's remaining fraction is 0.0 it walks the
builder's primary queue from front to back and, for every record whose
resolved operation is QMove or QPatrol, enqueues the corresponding operation
on the product with the record's saved position triple. Traversal order is
preserved — multiple rally points become multiple product orders in
factory-queue order, patrol remains patrol, and the factory itself never
moves. If no such record exists it enqueues a Park order instead; it then
returns result 5 and its own node is removed, so it is not left as a perpetual
completion watcher.

Standing-order inheritance is separately gated (summary in section 3.5): the
copy is allowed only when BOTH the product and the builder carry the
building-class/alive state bit (bit 28) and NEITHER carries bit 14 (the auto
flag, which also marks the cloak/initial-posture posture). When the guard
passes, standing-move bits 18–19 and standing-fire bits 20–21 copy from the
builder's state word to the product's; for a computer-owned builder (owner
player state byte value 1) the builder's experience word also copies. The
state-2 epilogue's own standing-field merge is the initial product-state copy;
the guarded GetBuilt block is the post-build gate — keep both stages distinct
rather than treating the product's initial flags as proof that GetBuilt has
already run.

**Established fact — same-tick product publication windows [R-P0-09]:** A
product allocated during the unit sweep exists before the projectile phase and
can be selected as a projectile target in that phase, even while its remaining
fraction is still 1.0. A product completed during the sweep has its completion
flag and remaining fraction visible immediately to later builders in the same
sweep. Occupancy and LOS publication happen after settlement in the same tick
and do not retroactively alter the earlier projectile phase. GetBuilt runs in
the same sweep when the product's slot sorts after the producing factory's
slot; a product that reuses an already-visited slot waits for the next tick.
The local player's victory/trigger poll rides that player's settlement
deadline: a due 30-tick poll can see the just-completed product in the same
tick, otherwise the trigger lags until the next due settlement.

**Established fact — repetition and queue cleanup [R-P0-09]:** Retail has no
repeat flag in the order-node descriptor or runtime flag word. Back-to-back
production comes from the counted node: tail-only coalescing adds to the
record's remaining count; each completion decrements it once; result 0
restarts state 0 in the same primary-pump pass; and the final state-0 count at
or below zero returns 5 and frees the node. There is no gap and no randomized
delay between successful products of a coalesced count. A factory death or
capture does not transfer queued nodes to the replacement in the bounded
census — the old list stays on the dead factory's order anchors — and the
exact allocator/slot-reuse cleanup when a dead factory slot is reused remains
TODO(question); do not invent reclamation or inheritance.

### OTA-FAC-01 movement boundary [R-FAC-01] (2026-08-28)

The factory-side findings above close publication and the `GetBuilt` rally
handoff, but they do not establish a separate post-completion egress order.
The reviewed production state machine has no release-specific node or direct
movement integration after allocation; it creates `GetBuilt` on the product's
primary queue, and that node waits for completion before copying queued move or
patrol rallies. `Park` is the no-rally fallback. Consequently, a product's
ordinary inherited rally is not evidence of a mandatory release segment or of
clearance from the producer footprint. The retail movement mechanism that
would account for a no-rally product leaving its factory, if one exists
outside this handler path, is **Unknown**.

The state-2 target is nevertheless **Established**: `QueryBuildInfo` supplies
the exit piece index, the piece transform is resolved with the factory origin,
and that world position is retained for allocation while a separately snapped
footprint anchor is validated. No independent factory-heading, yard-map,
model-extent, or fixed-cell offset is read by the factory handler.
**Correction (2026-08-28):** this sentence used to continue "Rotation
therefore enters through the authored piece transform and its hierarchy; the
stock authored transform for each rotated factory variant remains
**Unknown**". The premise holds but the conclusion did not: rotation enters
inside the shared piece locator, which folds the unit's committed orientation
into the model root node before rotating, so a rotated factory rotates its
exit point. The complete arithmetic is **Established** in [R-REV-02], and the
stock authored piece values are established by the census in [R-FAC-01B].

The pre-allocation collision and retry contract is **Established**: the
validator runs before a product exists, so it cannot be testing a
producer/product pair; a rejected footprint waits silently for exactly 15
ticks and has no timeout or force-placement. The allocation-failure branch is
separate and waits exactly 300 ticks with its established diagnostic. No
dedicated post-allocation producer/product exemption or blocked-release retry
was recovered. Whether a later movement path permits that overlap, and what
an indefinitely blocked release does, are **Unknown**. The occupancy-layer
inference and its limits remain in [R-P0-08-A §1]; it must not be turned into a
global collision bypass.

For aircraft, the generic VTOL contract is **Established**: the first VTOL
point/follow goal starts the ordinary velocity-limited climb, with no separate
takeoff callback in the movement handler. The factory path does not show a
factory-specific takeoff state before `GetBuilt`; the exact aircraft-factory
takeoff-before-rally sequence is therefore **Unknown**. The same boundary
applies to the no-stacking guarantee for multiple coalesced products: primary
queue serialization and per-product pre-allocation validation are established,
but occupancy is published later in the tick and the static evidence does not
prove that a second same-pass allocation cannot stack.

These are research boundaries, not replacements for the established factory
completion contract. Keep the unresolved release, collision, blocked-lane,
aircraft handoff, and multi-product questions as `TODO(question)` until
executable or authored-data evidence closes them. The
rotated-authored-transform question that used to appear in this list is
closed by [R-REV-02].

### OTA-FAC-01B targeted release-boundary pass [R-FAC-01B] (2026-08-28)

This is a correction and second pass over [R-FAC-01]. The earlier statement
that no stock COB transform census was available is superseded for the
authored exit-piece inputs only. A clean-room census of the original
`totala1.hpi` COB and 3DO assets establishes the following `QueryBuildInfo`
output-local-0 results and piece names. The callback indices were decoded
independently from each stock COB's `QueryBuildInfo` sequence (literal N is
popped into output local 0); they were not supplied by pairing the model
files. The translations are authored 3DO local coordinates before model-loader
half-turn normalization, not runtime world offsets ([fmt cob], [fmt 3do]).
Exact signed 16.16 numerators are retained below, with rounded decimals only
as a reading aid:

| factory | output local 0 | authored piece | local translation (signed 16.16; decimal) |
| --- | ---: | --- | --- |
| ARMLAB | 1 | `pad` | (32768, 26214, 229375); (0.5000, 0.4000, 3.5000) |
| CORLAB | 1 | `pad` | (0, 3276, 942080); (0.0000, 0.0500, 14.3750) |
| ARMVP | 1 | `pad` | (0, 32768, 655360); (0.0000, 0.5000, 10.0000) |
| CORVP | 2 | `pad` | (0, 27456, -1015603); (0.0000, 0.4189, -15.4969) |
| ARMAAP | 1 | `pad` | (68155, 15285, -1092098); (1.0400, 0.2332, -16.6641) |
| CORAAP | 1 | `pad` | (0, 77561, -1638400); (0.0000, 1.1835, -25.0000) |
| ARMSY | 6 | `slip` | (0, -573440, 0); (0.0000, -8.7500, 0.0000) |
| CORSY | 0 | `base` | (0, 0, 0); (0.0000, 0.0000, 0.0000) |

The five target boundaries now have these statuses:

1. **Target derivation and rally handoff — split Established/Unknown.** The
   output piece index and its authored hierarchy-composed transform are now
   Established inputs to the state-2 target formula in [04 §6.3]. For these
   eight selected pieces, the parent is the root `base` with zero translation,
   so the hierarchy-composed authored origin equals the listed piece-local
   translation; that equality is not a generic descendant rule. The engine
   still allocates at that resolved position, validates a separately snapped
   footprint anchor, and publishes a product-side `GetBuilt` node. `GetBuilt`
   waits for completion, then copies queued move/patrol rallies or falls back
   to `Park` ([04 §3.8]). Within the reviewed factory-handler call chain, no
   factory-owned post-completion egress order, clearance segment, or additional
   rally handoff was found; whether another movement path supplies one is
   **Unknown**.
2. **Producer/product collision exemption — Unknown after allocation.** The
   pre-allocation validator cannot see a product and receives no producer /
   product pair. The reviewed call path has no dedicated post-allocation
   exemption. A static call-chain capture that follows the first product move
   through occupancy and collision admission, or a retail trace with an exit
   deliberately overlapping its producer, is required to close this item.
3. **Blocked release and queue/build gating — split Established/Unknown.**
   Primary-queue front blocking, positive count gating, and the 15-tick blocked
   pre-allocation retry versus the 300-tick allocator retry are Established.
   No post-completion release lane was recovered in the reviewed factory
   handler call chain, so indefinite blocking of that hypothetical lane and
   any force-release policy remain **Unknown**.
   Same-pass coalesced products are serialized by the queue, but delayed
   occupancy publication does not establish a no-stacking guarantee.
4. **Aircraft takeoff — generic Established, factory ordering Unknown.** The
   generic VTOL mover starts its velocity-limited climb when its first flight
   point/follow goal is installed ([04 §10.1]). The reviewed factory-handler
   call chain shows no factory-specific takeoff state before `GetBuilt`; whether
   takeoff precedes rally handoff for an aircraft product is **Unknown**.
5. **Rotated transform Established; no-stacking still Unknown.** The stock
   authored piece index, name, and local translation are Established by the
   asset census above. **Correction (2026-08-28):** this item previously said
   "The exact runtime arithmetic that applies a rotated factory heading to
   those hierarchy translations … remain **Unknown**" and asked for a
   heading-matrix capture. That arithmetic is now Established in [R-REV-02]:
   the shared piece locator folds the unit's committed orientation into the
   model root node's angles before rotating, so no separate heading matrix
   exists to capture. The 3DO format's lack of an authored heading field
   ([fmt 3do]) is consistent with this — the heading is runtime unit state,
   not authored model data. A same-pass no-stacking guarantee remains
   **Unknown**; its decider is a blocked multi-product trace.

`TODO(question)`: capture a retail run with a blocked exit, a completed ground
product, and a completed aircraft product while recording the first movement,
occupancy, and rally events. This is the evidence needed to distinguish a
hidden release lane from ordinary `GetBuilt`/VTOL handling; do not implement
one from the unresolved inference.

`TODO(T25)`: the upstream producers of production-node wake mask 8
(Construction stopped) remain unlocated — the handler semantics are closed in
section 3.3 and above, and a bounded census of every writer of the record
satisfied word and the unit capability word over the whole instruction
listing (2026-08-26) finds no instruction that ORs bit 2 or bit 8 into
either; the mask-2 cancel notification is instead delivered by the node
cleanup path (section 3.3), whose producers are the ordinary removal paths.
The mask-8 wake-bit producer must not be invented.

## 4. COB loader, VM, threads, and script timing

### 4.1 Loading and binding

**Established fact:** A compiled script file is read whole into one allocation and relocated in place; its header, tables, and record layout are specified in document 02. The loader binds one compiled script object to each unit definition, caches it by file, and stamps it with the content checksum that save/load validation later compares.

**Established fact:** Each live unit receives a COB VM instance. The VM has eight execution threads, per-thread status/PC/stack/sleep/wait/caller/signal state, static variables, and per-piece animation state. Thread stack depth is limited to ten values by the retail layout; no safe expansion behavior is demonstrated.

**Established fact:** The loader allocates script statics and piece states when binding. Save/load validates the expected blob size and a cache/signature value. Static variables and piece animation state are represented in the save payload. Fractional movement deltas are not saved; the next tick resumes from integer state.

**Unknown:** Some anonymous save fields and exact failure behavior of allocation or corrupted piece indexes remain unresolved. Retail may abort through its allocator where a clean implementation should terminate the affected script deterministically.

### 4.2 Thread scheduler

**Established fact:** The unit phase drains each unit's eight script threads. The interpreter runs before per-piece interpolation in that unit's script drain. A move or turn issued by a script therefore affects the same tick's interpolation; a wait that becomes satisfied during interpolation is observed by the next drain.

**Established fact:** The engine reaches scripts through four adapter roots: the zero-argument name-form start, the argument-carrying name-form start, the argument-carrying slot-form start, and the name-form synchronous four-cell query (direct-static: the name-form roots resolve through the shared name-lookup helper and the slot-form roots through the shared slot-lookup helper). The zero-argument name-form start is a name-based zero-argument start (15 direct call sites); the argument-carrying name-form start is name-based, taking up to four arguments, resolving the name, then forwarding to the argument-carrying slot-form start (21 direct call sites); the argument-carrying slot-form start is slot-based, writing four physical cells then setting the logical top to `arity−1` (5 direct call sites: four producers outside the adapter roots plus the argument-carrying name-form forwarding); and the name-form synchronous query forwards to the query interpreter (14 direct call sites). A bounded call census over the code sections counts 55 direct calls to those four roots; one is the internal argument-carrying name-form→slot-form forwarding, so 54 are producer call sites outside the four roots. The adjacent slot-based zero-argument helper has no direct caller in the bounded census (negative-bounded). The fixed-name producer census yields 40 distinct case-sensitive callback names; the packet path `0x0E` (section 5.3) can additionally start an authored slot by index and is not limited to those 40 names. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Each VM has eight thread slots of `0xA4` bytes. Allocation takes the first inactive slot in ascending order. A new root thread begins runnable at the selected function entry with logical stack top `−1`, no completion receiver, and signal mask `1`; child script starts inherit their parent's current mask. There is no name-level or producer-level duplicate suppression in the adapters — repeated successful starts occupy independent slots, and any repeat suppression lives in the producers' own state caches. Invalid identity (name lookup `−1` or slot out of range) and a full eight-slot pool are the same allocation failure to the adapters: the zero-argument name-form start and the slot-based zero-argument helper return false without notifying a supplied receiver, while the argument-carrying slot-form start and the argument-carrying name-form start return false and, if a receiver is supplied, invoke it with `0`. `HitByWeapon` and `TakeDamage` are independent argument-carrying name-form starts and can fail separately; `Aim*` failures also deliver `0` via that path and therefore leave aim-ready clear (section 5.3). <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Entry modes are D, I, and Q. D (deferred) allocates and initializes a thread then returns without interpreting it. I (immediate) after allocation interprets all eight slots once with delta `0` in slot order, then runs one piece pass with delta `0` — a VM-wide drain, not a drain of only the new callback. Q (synchronous query) interprets only the newly allocated slot with delta `0`, snapshots up to four cells, and returns; it does not scan the other seven slots and does not run a piece pass. The zero-argument name-form start takes zero logical arguments; the argument-carrying slot-form start always writes four physical values to cells `0..3` then sets logical top to `arity−1`; the synchronous query roots treat null cell pointers as seeded zero and excluded from copy-back, while non-null cells are logically exposed. A Q callback is not guaranteed to have returned when the host snapshots it: if it sleeps, waits for move/turn, or waits for another script, the one-slot interpretation stops and the host copies the current four cells immediately; the blocked thread remains active and may resume in a later VM-wide or normal drain, but it has no completion receiver that can revise the already returned host values, so callers must pre-initialize outputs and handle partial results. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Thread states include idle, running, waiting for turn, waiting for move, sleeping, and waiting for a called script. Signal masks can terminate or suppress matching threads. Calls block the caller until the callee returns.

**Established fact:** `SET_SIGNAL_MASK` (`0x10068000`) replaces the current thread's mask with the popped value. Engine-created root threads begin with mask `1`. `SIGNAL` (`0x10067000`) pops a mask and scans all eight slots; every active thread whose mask intersects it is released, including the signalling thread itself, each decrementing the active count and waking every thread waiting for that slot. Signal termination never invokes a completion receiver; if the signalling thread is among the victims its interpretation stops. An explicit script `return` (`0x10065000`) pops the top value, delivers it to the thread's completion receiver when one is set, releases the slot, and wakes threads waiting for that slot. An invalid-opcode kill clears status and decrements active count but does not invoke a receiver. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Synchronous query helpers execute script logic without an ordinary tick delta and do not advance piece interpolation. Asynchronous callbacks allocate one of the eight thread slots and return if no slot is available. Section 4.3 gives the exact failure edge for every starter, including the two cases where arguments are left on the caller's stack.

**Established fact:** An engine callback started with the wake flag runs its new thread to its first yield inside the caller's context with a tick delta of zero, so no time passes and no interpolation happens for it.

### 4.3 Opcode encoding

A script instruction is one 32-bit word in the code array. Bit 28 is set on
every opcode; bits 16 through 23 select the operation; the low twelve bits are
ignored except by the push and pop families, where the low three bits are an
addressing mode. The dispatch key is the instruction word masked with
`0x100FF000`.

Dispatch is a compiler-generated binary search over signed ranges with equality
leaves, not a jump table. The range sentinels it compares against are never
themselves opcodes. A key matching none of the **57 dispatched values** falls
into the kill path: the thread's status is cleared, the instance's active
thread count is decremented, and the drain yields. There is no default handler
and no diagnostic.

Operands follow the opcode as further whole words, so the program counter is a
word index. Six operand shapes exist:

| Shape | Layout | Advance | Used by |
|---|---|---|---|
| A | opcode only | +1 | arithmetic, comparison, logic, random, the engine reads, sleep, signal, set-mask, allocate-local, discard, return |
| B | opcode, piece | +2 | show, hide, cache, don't-cache, the shade pair, don't-shadow, both effect opcodes, explode |
| C | opcode, piece, axis | +3 | move, turn, spin, stop-spin, move-now, turn-now, wait-for-turn, wait-for-move |
| D | opcode, target word | +2, or set absolutely | jump, jump-if-false |
| E | opcode, script id, argument count | +3 | start-script, call-script, the reserved pop-N |
| F | opcode with a mode in its low three bits, operand word | +2 | push constant, local, or static; pop local or static |

The piece index comes from the first operand word and the axis from the second.
A piece-state entry is addressed as `axis + piece * 19` words.

The stack effect, program-counter advance, engine port, and suspension
behavior below are established from the interpreter. Where a *name* rests on
weaker ground than the mechanism, that is said explicitly.

**Piece operations.** Piece and axis operands are separate words in the order
piece then axis.

| Opcode | Operation | Stack effect | PC advance | Suspends |
|---|---|---|---|---|
| `0x10001000` | move piece along an axis toward a target at a speed | pops speed and target | +3 | no |
| `0x10002000` | turn piece about an axis toward an angle at a speed | pops speed and target, target masked to 16 bits | +3 | no |
| `0x10003000` | spin piece continuously, with acceleration | pops speed and acceleration | +3 | no |
| `0x10004000` | stop spin with a deceleration | pops deceleration | +3 | no |
| `0x10005000` | **set** the piece's draw flag — show | none | +2 | no |
| `0x10006000` | **clear** the piece's draw flag — hide | none | +2 | no |
| `0x10007000` | **set** the piece's cache flag — cache | none | +2 | no |
| `0x10008000` | **clear** the piece's cache flag — don't cache | none | +2 | no |
| `0x10009000` | legacy two-argument effect; the unit adapter is an empty stub, so this is a **no-op on units** | pops two values | +2 | no |
| `0x1000a000` | disable shadow for piece | none | +2 | no |
| `0x1000b000` | set piece position on an axis immediately | pops target | +3 | no |
| `0x1000c000` | set piece angle on an axis immediately | pops target, masked to 16 bits | +3 | no |
| `0x1000d000` | **set** the piece's shading flag — shade | none | +2 | no |
| `0x1000e000` | **clear** the piece's shading flag — don't shade | none | +2 | no |
| `0x1000f000` | emit one-argument effect at piece | pops effect type | +2 | no |
| `0x10011000` | wait until the piece's turn on an axis completes | none | +3 | **yes** |
| `0x10012000` | wait until the piece's move on an axis completes | none | +3 | **yes** |
| `0x10013000` | sleep for an authored duration | pops duration | +1 | **yes** |
| `0x10071000` | explode piece | pops flags | +2 | no |

**Stack, locals, and statics.**

| Opcode | Operation | Stack effect | PC advance |
|---|---|---|---|
| `0x10021000` | push, sub-mode 1 pushes the following word as a constant, sub-mode 2 pushes a local by index, sub-mode 4 pushes a static by index; any other sub-mode pushes uninitialized scratch, because there is no default branch | +1 | +2 |
| `0x10022000` | allocate one local by raising the stack depth without initializing it | +1 | +1 |
| `0x10023000` | pop, sub-mode 2 stores into a local, sub-mode 4 stores into a static; **any other sub-mode pops nothing and simply advances** — it neither faults nor kills the thread | -1 | +2 |
| `0x10024000` | discard the top of stack | -1 | +1 |

**Arithmetic, random, and engine reads.** All advance the program counter by
one word.

| Opcode | Operation | Stack effect |
|---|---|---|
| `0x10031000` | add | -1 |
| `0x10032000` | subtract, second-popped minus top | -1 |
| `0x10033000` | multiply | -1 |
| `0x10034000` | signed divide, second-popped by top, with no zero guard | -1 |
| `0x10035000` | bitwise and (name conventional) | -1 |
| `0x10036000` | bitwise or | -1 |
| `0x10037000` | bitwise exclusive-or (name conventional) | -1 |
| `0x10038000` | bitwise not, in place | 0 |
| `0x10041000` | random in an inclusive range: pops low and high, pushes `low + random(high - low + 1)` from the simulation stream | -1 |
| `0x10042000` | engine read with one argument, remaining argument slots zero-filled | 0 |
| `0x10043000` | engine read with five arguments | -4 |
| `0x10044000` | engine read through the single-argument port | 0 |
| `0x10045000` | engine read with no argument | +1 |

**Comparisons and logic.** All advance the program counter by one word and
have a stack effect of -1, except the final unary form.

| Opcode | Operation |
|---|---|
| `0x10051000` | signed less-than |
| `0x10052000` | signed less-or-equal |
| `0x10053000` | signed greater-than |
| `0x10054000` | signed greater-or-equal |
| `0x10055000` | equal |
| `0x10056000` | not equal |
| `0x10057000` | logical and, yielding one or zero |
| `0x10058000` | logical or, yielding one or zero |
| `0x10059000` | word exclusive-or (name conventional; not a boolean operation) |
| `0x1005a000` | logical not, stack effect 0 |

**Control flow, signals, and engine writes.**

| Opcode | Operation | Stack effect | PC advance | Suspends |
|---|---|---|---|---|
| `0x10061000` | start a script on a new thread; the script index and argument count are operand words; arguments are copied from the caller's stack into the new thread in reverse and the caller's signal mask is inherited | minus the argument count | +3 | no |
| `0x10062000` | call a script and block until it returns | minus the argument count | +3, then blocks | **yes** |
| `0x10063000` | reserved: pops a count of values into a discarded temporary | minus the operand count | +3 | no |
| `0x10064000` | jump; the target word index is the operand | 0 | set to target | no |
| `0x10065000` | return; the popped value is delivered to the thread's completion callback when one is set, the thread is freed, and any thread blocked on it is woken | -1 | thread ends | **yes** |
| `0x10066000` | jump if the popped value is zero, otherwise continue | -1 | target or +2 | no |
| `0x10067000` | signal: kills every thread whose mask intersects the popped mask, waking anything blocked on them | -1 | +1 | only when it kills itself |
| `0x10068000` | set this thread's signal mask | -1 | +1 | no |
| `0x10082000` | engine write: pops a value and a value identifier | -2 | +1 | no |
| `0x10083000` | attach a unit to a piece | -3 | +1 | no |
| `0x10084000` | detach a unit | -1 | +1 | no |

**Piece flag polarity.** The three flag pairs above write bits 0, 1, and 2 of
one flags byte in the unit's **render piece record**, which is a separate array
from the script's own piece-animation state. The lower opcode of each pair sets
its bit and the higher clears it.

The polarity is settled by how that array is built at unit creation. The
allocation is zero-filled and then a fill pass walks the model hierarchy and,
per piece, sets bit 1 and bit 2 unconditionally and sets **bit 0 only when the
piece's model object has at least three vertices**. So a piece with real
geometry starts drawable and a bare attachment point does not, which makes
**bit 0 the draw flag**: set means drawn. A consumer that walks pieces confirms
it, skipping any piece whose bit 0 is clear.

Consequently the defaults are: drawn for a piece with geometry, cached, and
shaded — and the three "don't" opcodes are the ones that clear a default-set
bit. All three bit assignments and both directions are established.

### OTA-RND-02A script-side shading census [R-RND-02A]

**Established (correction history, clean-room translation):** an earlier
provisional render note used the opposite label for the bit-2 polarity. The
load-time fill and the unit adapters settle the contract used here: bit 2 set
means shading is enabled, `SHADE` sets it, and `DONT_SHADE` clears it. The
renderer interprets the cleared state as the identity `SHD` row 15
([03 §2.4.1]). This is a per-piece render-record flag, not an FBI definition
flag and not a script-wide mobile/building class rule.

The requested base-game COB census found no `SHADE` operation in any `Create`
callback. `DONT_SHADE` does occur in `Create`, addressing individual pieces:
none in ARMCOM, CORCOM, ARMPW, CORAK, ARMSTUMP, ARMFIG, or CORVAMP; 10 in
CORSOLAR; 15 in ARMLAB; 18 in CORLAB; 11 in ARMVP; 15 in CORVP; 11 in
ARMAAP; and 18 in CORAAP. The stock mobile scripts in this set therefore
leave their model-fill shade default in place, while structure scripts make
explicit per-piece exceptions. **Established (static bytecode census,
`totala1.hpi`):** no class-wide shade initializer was found in this bounded
corpus; no `SHADE` or `DONT_SHADE` occurs in the `Activate`/`Deactivate`
callbacks.

**The bit only matters on structures.** The renderer contract has since been
corrected ([R-RND-02A]): retail runs the shaded piece renderer only for a unit
whose definition authors `BMcode=0`, and only while the `Shading` display
option is on. The unshaded renderer every other unit takes reads this same
piece flags byte for the draw bit and the cache bit and **never reads bit 2**.
So `SHADE` and `DONT_SHADE` in a `BMcode=1` script write a bit that nothing
consumes — which is why every stock script in the census that uses the opcode
is a structure, and every mobile row is zero. The polarity above is unchanged
by that correction; only its reachability is. An implementation should still
write the bit faithfully from the opcode rather than dropping the write,
because a unit's `BMcode` is authored data and a mod may set it either way.

**Thread state words.** A thread's status word encodes its state in the high
byte, with a sub-state in the next nibble for the waiting family: idle,
running, waiting for a turn, waiting for a move, sleeping, and blocked on a
called script. A waiting thread wakes when the per-piece word its wait names —
the move-speed word or the turn-speed word — reads zero; those are the same
words the interpolator clears on arrival. A blocked caller is woken by the
callee's return, or by a signal that kills the callee; both wake paths flip the
caller straight to running, and a killed thread frees its slot immediately
while transitively waking anything blocked on it within the same scan.

**Argument passing and failure edges.**

- A thread's local *i* is window word *i* of its frame, and the expression
  stack grows in the same window above the frame. Compiled prologues emit one
  allocate-local per parameter as well as one per declared variable, so both
  ways of starting a thread produce identical local addressing.
- Engine-started threads always have four argument words written, with unused
  ones left as garbage above the frame, and the depth set to one below the
  argument count. Script-started threads receive exactly the popped argument
  count, last popped landing highest, and start at an empty depth.
- **`start-script` with no free thread slot, or a bad script id, does not pop
  its arguments.** The issuing thread simply continues past the instruction
  with the arguments still on its stack. There is no callback and no fault.
- **`call-script` with no free slot likewise retains its arguments**, records a
  wait slot of -1, and blocks anyway. Nothing ever scans for slot -1, so the
  caller sleeps until a matching signal kills it. This is a real
  wedged-thread leak and an implementation should reproduce it rather than
  repair it.
- The engine's name-form starter returns failure silently when the name is
  unknown or the pool is full. The id-form starter, on a full pool, invokes the
  supplied completion callback with zero and returns failure.
- A synchronous query with a full pool returns failure and leaves its outputs
  untouched, so callers must pre-initialize them. On success it pushes its four
  inputs, forces the depth to three, runs the interpreter inline, and copies the
  first four window words back out. **If the queried script sleeps or waits, the
  query simply returns whatever the window then holds** — queries must not
  sleep, and retail does not enforce it.

**Arithmetic domain.**

- There is one divide opcode. It is a signed 32-bit division truncating toward
  zero, with **no divisor-zero check and no minimum-integer overflow check**;
  either raises a processor fault and terminates the process.
- **There is no modulo opcode and there are no shift opcodes** anywhere in the
  dispatch space. An implementation that exposes either to scripts is inventing
  surface the retail engine does not have.
- Addition, subtraction, and multiplication wrap silently in 32 bits.
  Comparisons are signed, except equality and inequality, which compare bit
  patterns.

**Unknowns in this encoding.** The reserved opcode `0x10063000` has an
established interpreter behavior — pop the operand count of values into a
discarded temporary and continue — but no producer in shipped content; an
implementation must reproduce the pop-and-continue rather than faulting on
encounter. The bitwise and/or/exclusive-or/not opcodes are confirmed as plain
32-bit word operations, and word exclusive-or is a raw `a^b`, **not**
booleanized to one or zero. The legacy two-argument effect opcode binds a
return-stub adapter on units, so it is a no-op there; shipped content contains
no instance of it, the shade-set opcode, either bitwise exclusive-or form,
either cargo query, or the reserved pop-N. A divide by zero or integer overflow
raises the processor divide fault and kills the retail process outright —
document this as policy rather than silently repairing it.

### 4.4 Engine ports

Both value-reading opcodes route into one switch over an identifier from 1 to
20; an identifier outside that range reads zero. The value-writing opcode
routes into a parallel switch over the same identifiers, and an identifier with
no write arm only sets the unit's script-touched marker. Every write arm sets
that marker in addition to its own effect. The two remaining read opcodes are
transport queries and are not part of this table.

| Id | Name | Read | Write |
|---:|---|---|---|
| 1 | activation | the unit's activation bit | drives the **activation edge machine**, which fires the Activate and Deactivate callbacks re-entrantly |
| 2 | standing move orders | a **two-bit** field, values 0 to 3 | ignored |
| 3 | standing fire orders | a two-bit field, values 0 to 3 | ignored |
| 4 | health | current health times 100 divided by the definition's maximum damage, as an unsigned division, giving 0 to 100 | ignored |
| 5 | in build stance | a flag bit | sets the bit from the low bit of the value |
| 6 | busy | a flag bit | sets the bit from the low bit of the value |
| 7 | piece position XZ | the piece's world transform packed with Z in the high half and X in the low half | — |
| 8 | piece position Y | the piece's world transform Y | — |
| 9 | unit position XZ | another unit's packed X and Z, selected by identifier through the unit table, gated on that unit being alive; identifier zero or a dead unit reads zero | — |
| 10 | unit position Y | the same unit's Y, same gates | — |
| 11 | unit height | the definition height of the unit selected by the identifier, via the same unit-table lookup and alive gate as ports 9 and 10; identifier zero — including the zero-filled argument slot of a bare `get` — reads zero. **Audit note:** this corrects the earlier "own definition's height value" wording [R-P0-10] | — |
| 12 | relative bearing | unpacks the argument into two signed 16.16 halves, takes the arc tangent, then **subtracts the unit's own heading**, truncated to 16 bits | — |
| 13 | distance | the hypotenuse of the unpacked halves, truncated | — |
| 14 | arc tangent | the arc tangent of the two arguments, low 16 bits | — |
| 15 | hypotenuse | the hypotenuse of the two arguments, truncated | — |
| 16 | ground height | the world height query at the packed coordinates, shifted into 16.16 | — |
| 17 | build percent left | from the remaining-build fraction *f*: zero when *f* is exactly zero, otherwise `1 - trunc(f * -99.0)` | ignored |
| 18 | yard open | a flag bit | runs the yard-occupancy update |
| 19 | bugger off | a plain flag bit, not a queued request | sets the bit from the low bit of the value |
| 20 | armored | a flag bit | drives the armor edge event |

The two angle ports are the **only** place in the port arithmetic that rounds
rather than truncates: the arc tangent is scaled by 65,536 divided by two pi
and converted under round-to-nearest. Everything else truncates.

The transport queries are: one that pops a unit identifier, walks the unit's
own cargo list, and pushes one or zero; and one that pushes the first cargo
identifier or zero. Neither appears in shipped content.

**Attach** resolves the cargo identifier through the unit table with the alive
gate, requires the candidate's carrier field to be empty or already this unit,
and then binds it to the named piece. **Drop** requires the candidate's carrier
to be this unit, asks the placement service for permission, and then unbinds
it using the reserved "no piece" index.

**The one-argument effect opcode is presentation only.** It is gated on the
local player being able to see the unit, touches no simulation state, and
dispatches on the effect type. Vector types 0 through 5 are piece-direction
effects: 0 and 1 form the wake pair, 2 and 3 a thrust-class pair, and 4 and 5
repeat that pair with the direction and position arguments swapped. Point
types use the piece world position: `0x101` spawns white smoke, `0x102` black
smoke, and `0x103` sub-bubbles with the spawn height forced to the water-line
height. Every other type — vector types from 6 upward, `0x100` itself, and
everything from `0x104` up — falls through with no case and is ignored.

### 4.5 The explode opcode

The explode opcode takes a flags word from the stack and a piece from its
operand. It never yields and completes inside the drain.

Unless the flags request bitmap-only, it builds a debris record from the unit
and piece and spawns physical debris. **Its random draws are part of the
authoritative stream and their order is fixed**: three draws bounded at 3,000
for the horizontal velocity words, one bounded at 40 for a vertical word, one
bounded at 10 whose result is immediately overwritten and therefore dead, and a
second draw bounded at 40. An implementation must make all six draws in that
order, including the dead one, to stay in step.

Independently of that branch, each set bitmap flag spawns one effect from a
fixed six-entry table, in ascending bit order, so multiple flags produce
multiple sequential effects. The bitmap branch runs even when bitmap-only
suppressed the physical branch.

The opcode applies exactly what the script asks for. Death severity is not
gated here: authored death scripts branch on their severity argument
themselves, and the engine has no generic central explosion that consumes
severity.

### 4.6 Piece arithmetic and interpolation

**Established fact — angle domain and spin marker:** Each piece has independent per-axis move, turn, spin and acceleration state addressed as `axis + piece*19` words (see §4.3), plus per-piece busy and global dirty flags. Valid angles are 16-bit, `0x10000` per circle (`0x8000` is 180°); every angular store masks `&0xffff` and every lerp commit masks and wraps `&0xffff` with `+0x10000` wrap. The value `0xffffffff` is an out-of-band sentinel meaning continuous spin, never a valid angle, written only by `spin` (`0x10003000`) and tested by the interpolator's rotation block.

**Established fact — tick denominator:** The VM tick denominator is the constant 30, read at VM zero-init from the engine's tick-rate global and written once at process startup from the fixed 30-tick configuration. It is copied to the VM's own tick-denominator field and immutable after. It is not derived per-tick from the wall-clock or game-speed budget. A bounded scan finds no zero guard before the four divide sites (move, turn, spin, stop-spin); a synthetic zero denominator would raise the processor divide fault. Sleep uses multiply `denom*ms` not divide and is not affected.

**Established fact — division and remainder:** Every `speed/denom`, `accel/denom` and `decel/denom` uses signed division truncating toward zero (positive denominator); remainder is discarded with no carry between ticks. Thus `−100/30` is `−3`, not `−4`. Sleep timer is `(denom * ms)/1000` trunc toward zero, denominator positive, also discarding remainder.

**Established fact — move and turn arrival:** Move and positional turn snap on inclusive arrival: if `cur+step` equals or overshoots `target` the interpolator snaps to `target` and clears the axis busy word, otherwise it steps and keeps the piece dirty. Turn chooses direction by shortest-arc: `delta = tgt − cur` (wrapped and sign-extended), `perTick = trunc(speedRaw/30)`; if `delta==0` perTick is 0, otherwise if `abs(delta) > 0x8000` the sign is flipped. The `> 0x8000` test is strict; a tie at exactly `0x8000` (opposite angles) does not flip and retains the script's sign — deterministic for both directions.

**Established fact — spin accel and stop-spin:** Spin (`0x10003000`) writes the spin target speed as `trunc(speed/30)` and the spin acceleration as `trunc(accel/30)`, and marks the turn-target/marker word with the `0xffffffff` sentinel; if `accel/30 == 0` the spin current speed is set to the target immediately (fast path). Otherwise the interpolator's acceleration-ramp block adds the spin acceleration to the spin current speed each tick and clamps inclusively: when `accel<0` and `curSpd <= tgtSpd`, or `accel>0` and `curSpd >= tgtSpd`, it snaps to `tgtSpd` and clears the spin acceleration (overshoot or exact hit both clamp). Stop-spin (`0x10004000`) writes the spin target speed to 0 and the spin acceleration to `−trunc(decel/30)`; if that is 0 it clears the spin current speed immediately (stop-now). Spin never completes on its own; it is terminated only by stop-spin. Stop-spin does not set the per-piece busy flag or the global dirty flag — it relies on the prior spin's dirty to ensure the next interpolator pass processes the negative ramp.

**Established fact — zero-speed and zero-decel:** If `|speedRaw| < 30` then `perTick == 0` (`|decel|<30` likewise 0). The interpreter still writes the per-piece busy flag and the global dirty flag for move/turn/spin, but the interpolator's busy-word guards (`move-speed word != 0`, `turn-speed word != 0`) are false so no motion occurs; the per-piece reduction clears dirty on the next tick. A `wait-for-move` polling the move-speed word or `wait-for-turn` polling the turn-speed word therefore wakes immediately (does not block) when the issued speed truncated to zero. Spin with zero speed and zero accel still dirties for one tick via the fast path then clears; a stopped spin (spin current speed 0, marker `0xffffffff`, spin acceleration 0) has no motion and is cleared as idle.

**Established fact — dirty lifecycle:** The piece interpolator clears the global dirty flag at entry, then per piece clears the per-piece busy flag at the start of the piece scan and re-sets it to 1 if any axis remains busy after processing the move block, the acceleration-ramp block and the rotation block. The epilogue OR-reduces: if any piece's busy flag is 1, the global dirty flag is set to 1. The interpreter's `move`, `turn`, and `spin` set both the per-piece busy flag and the global dirty flag; `move-now`, `turn-now`, and `stop-spin` do not set dirty and instead zero the busy words (the move-speed word, the turn-speed word, the spin-acceleration word) and commit immediately via the model adapter's set-position/set-angle. A retarget while active overwrites the target and recomputes sign/delta from the physical get-position/get-angle at issuance.

**Established fact — sleep:** Sleep converts the script duration as `trunc(denom * milliseconds / 1000)` with `denom==30` and stores it as the thread's timer. On every later entry the guard subtracts the tick delta first and wakes only when the result is at or below zero. **A sleep therefore occupies its truncated tick count plus exactly one guard decrement, so its minimum latency is one tick, not zero** — a sleep of 33 ms truncates to 0 and wakes on the next drain's guard; 34 ms truncates to 1. A zero or negative duration behaves the same. Engine wake passes run all eight slots with delta 0, so they never advance a timer, but they do wake any thread whose timer is already at or below zero.

**Established fact — drain and lerp order:** Per-unit tick entry drains `for slot 0..7: interpreter(this,slot,delta)` then `interpolator(this,delta)` with the same `delta` (retail passes 1; synchronous helpers pass 0 so lerp is skipped). The interpolator is a no-op when `delta==0` or `global dirty==0` or pieceCount==0. Thus a `move` issued in the drain moves the piece by `step = perTick*delta` in the **same** tick's trailing lerp; a `wait` that snaps during that lerp wakes only at the **next** tick's early guard (one-tick latency). A synchronous query nests a full interpreter run inside the caller at delta 0; re-entrancy is possible and retail places no guard against querying a unit mid-drain.

**Established fact — immediate commit and slot-order wake:** `move-now` (`0x1000b000`) and `turn-now` (`0x1000c000`) zero the move-speed/turn-speed/spin-acceleration busy words, write the target, and commit immediately via the model adapter's set-position/set-angle inside the drain — visible to later script slots and later simulation phases the same tick. A `wait-for-move`/`wait-for-turn` wakes when its polled busy word reads zero. For immediate ops the wake is slot-ordered: if issuer slot `i` < waiter slot `j`, the waiter's early guard has not yet run and sees zero same tick; if `i > j` the waiter already yielded and wakes next tick. A different piece/axis has no effect. The drain has no second scan after the lerp, so this asymmetry is contract.

**Established fact — save and load:** The COB/piece save writer emits `0x528` bytes for the thread pool (eight `0xA4`-byte thread slots plus eight pool bytes) plus `numStatics*4` plus `pieceCount*0x6c` bytes for pieces, each piece `0x6c` = 27 dwords covering per-axis move target, move speed, turn target/marker, turn speed, spin target, spin acceleration, plus the current position via the model adapter's get-position and the current angle via get-angle and a 24-byte pad of three anonymous snapshot slots. The loader gates exact size `0x528 + numStatics*4 + pieceCount*0x6c` (else fail 0), checks the VM's cache/signature value mismatch (fail 0), and on success forces the global dirty flag and the per-piece busy flags so the next interpolator pass resumes in-flight animation after one tick. Fractional deltas are not saved; the scheduler carry is saved separately. A failed piece allocation is skipped; TDF dispatch is quiescent during the save.

**Supported inference (medium confidence) — axis mapping and retarget:** Model-coordinate handedness and any one-time 3DO conversion belong to model loading, not per-tick COB arithmetic. The script axis mapping `axis + piece*19` is passed directly to the model adapter's get-position/get-angle/set-position/set-angle with no sign inversion or axis remap at those call sites — adapter identity per-tick.

**Closed — the anonymous snapshot dwords (2026-08-26):** the three per-piece
snapshot slots at the piece wire offsets `0x54..0x60` are uninitialized-local
leaks copied to the piece save image at save time — they carry no engine
semantics, which is exactly why no consumer exists [P1-13]. Store them
verbatim for byte-exact save reproduction; do not interpret them.

**Closed — invalid piece index fault policy (2026-08-26):** the interpreter
was re-exported and the piece-op family contains NO comparison of the piece
index against the piece count anywhere — an out-of-range index is an
out-of-bounds access (undefined behavior in retail), not a thread kill. The
interpreter's thread-kill path is reached only from an UNRECOGNIZED opcode
value in any family and from the thread-return opcode. The earlier
bounded-negative ("no bounds check found in the bounded export census;
corpus guarantees validity") stands, with the mechanism now pinned: Nanolathe
must either validate the index itself or treat the operand as engine-guaranteed
valid — retail provides no check to clone. The prior note reading "bad piece
index kills the thread" was a misattribution of the unknown-opcode kill path
[p1-11] and is superseded here.

**Unknown — allocation-failure deterministic fault policy:** where retail
would abort through its allocator's abort path remains `TODO(question)`; the
thread-pool-full semantics (argument retention, wedged wait slot) are
established in section 4.3.

**Closed — zero-denominator defence (2026-08-26):** the four divide sites are
unguarded signed divides in the interpreter as well; a synthetic zero
denominator raises the processor divide fault and terminates retail. The
"must be a guarded thread-kill, not a trap" sentence above is the Nanolathe
divergence note and stays as written; the retail contract is "the process
dies".

### 4.7 Engine port write semantics, the factory stance handshake, and thread-start masks [R-P0-10]

**Established fact — write dispatch [R-P0-10]:** The engine-write opcode binds
exactly six write arms — ports 1, 5, 6, 18, 19, 20. There is no STANDGROUND,
no WEAPON1/2/3, and no CLOAKED write port; an identifier without a write arm
only sets the unit's script-touched marker, and every write arm sets that
marker in addition to its own effect (section 4.4). The compiled form pushes
the identifier first and the value second, so the value sits on top of the
stack, and the opcode pops the value first and the identifier last. A census
of shipped scripts (armlab, armcom, and every write site in each) shows the
identical `push <identifier>; push <value>; set` layout; the compiler's
`set INBUILDSTANCE to 1` is exactly `push 5; push 1; set`. A census of shipped
scripts exercises all six arms and reads only ports 4, 17, and 18.

**Audit note (2026-08-26):** the earlier wording — "the compiled form writes
the value first and the identifier last" — was read as describing the
instruction-stream order and implemented by popping the identifier first. That
inverted every engine write on retail content: `set INBUILDSTANCE to 1`
became a port-1 write of value 5, the stance never rose, and every factory
stalled in production state 1. The asset census settles the order: identifier
pushed first, value on top, opcode consumes value then identifier. The
opcode-table row "pops a value and a value identifier" is consistent with
either reading and is not the deciding evidence.

Per-port write effects:

- port 1 (activation): routes into the shared activation edge machine with
  mask 1;
- port 5 (in-build-stance): writes bit 0 of the stance byte from the low bit
  of the value;
- port 6 (busy): writes bit 1 of the same byte from the low bit;
- port 18 (yard open): a gated admission — on pass it commits bit 2 of the
  same byte from the low bit, sets the occupancy-dirty bit (bit 27 of the
  state word), recomputes occupancy, and refreshes the footprint words;
- port 19 (bugger off): writes bit 3 of the same byte from the low bit;
- port 20 (armored): routes into the shared edge machine with mask 2, flipping
  the armored bit; the damage paths consult the bit directly and there is no
  callback.

The unit keeps two port-relevant state bytes: one holds bits 0 = activated,
1 = armored, 2 = engine-driven (cloak family; its rising edge releases cargo
and notifies presentation codes 0xe/0xf), and 3 = building; the other holds
bits 0..3 = in-build-stance, busy, yard-open, bugger-off.

**Established fact — activation edge machine and engine drivers [R-P0-10]:**
The shared edge machine computes the old and new state, writes back, and on a
change fires: Activate plus the code-3 notification on bit-0 rising;
Deactivate plus code 4 on falling; StartBuilding/StopBuilding on the
building-bit edges; cargo release plus presentation codes 0xe/0xf around the
bit-2 edges; always the interface refresh, and — when the owner player state
byte is 1 or 2 — a network event carrying the state byte and the unit
identifier. Callbacks run re-entrantly, but the diff is computed before they
fire, so a callback that writes port 1 terminates immediately: its own edge is
now a no-op. The engine itself raises and lowers activation — scripts are not
the only writers: a non-empty production queue raises it and an empty one
lowers it and drops the node; accepting an attack or build order on an
on/offable definition (capability bit 11) raises it; the post-completion
transition raises it when capability bit 18 is set; the low-power manager
lowers it on a failing resource test and re-raises it on recovery behind one
inclusive random draw below 5 from the simulation stream; the cancel-current
interrupt clears the activation and building bits together; and the COB write
port is the same machine, re-entrant.

**Established fact — factory stance handshake [R-P0-10]:** The production node
state machine is: state 0 — building-class gate on the factory's own state
word; a pending count above zero raises activation and advances, otherwise
activation lowers and the node is dropped. State 1 — a pure level test of the
in-build-stance bit: while the bit is clear the node is marked waiting and
stays, re-polled on every pump visit with no deadline set — the wait lasts
until a script sets the bit or the order is cancelled or interrupted, and an
unbound write port therefore deadlocks the factory exactly as observed; when
set, it advances. State 2 — synchronous QueryBuildInfo (cell 0 seeded −1),
exit-piece world position, footprint snap, placement validation: a blocked
exit retries in exactly 15 ticks with no product, sound, or placement event;
success allocates the nanoframe at the resolved exit transform (section 6.3),
copies the factory's standing-order bits into the product state word, inserts
GetBuilt, and fires the StartBuilding edge; allocator failure prints "Unable
to create any more units" and retries in exactly 300 ticks. State 3 — work
ticks through the shared construction helper (document 05). When work stores
zero remaining, that helper first runs the product completion transition and
possible `Activate`; state 4 then lowers the factory `StopBuilding` edge,
invokes the product completion transition idempotently a second time, performs
count bookkeeping, and restarts at state 0 in the same pass. Cancel masks: the
kill-frame path refunds metal by trunc((1 − remaining)·cost), kills the frame
with kind-9 damage 30000, and clears the activation and building bits
together; the Construction-stopped path decrements once and keeps the node
[R-P0-09].

**Established — stock factory callback choreography [P28-FAC-01R].** The
factory COBs keep door/stance control separate from the production edge
callbacks. The argument-less `StartBuilding` callback contains only the
authored pad spin; it has no `wait-for-turn`, `wait-for-move`, or sleep. The
matching `StopBuilding` callback only issues the pad stop-spin and returns.
Consequently the completion state's deferred `StopBuilding` start is not a
door-close operation. Its normal execution in the next script drain stops the
pad animation, while the factory may already have restarted state 0 for a
queued product or may have lowered activation for an empty queue.

For the stock lab/factory callback template, an activation edge starts
`RequestState(0)` after signalling the state-transition mask. That state
transition calls `Go`; `Go` runs the authored activation animation, calls
`OpenYard`, and only after both return writes `INBUILDSTANCE = 1`. A
deactivation edge first signals the same mask, sets its own mask, sleeps for
the authored 5000 ms (the immutable 30 Hz VM converts this to a 150-tick
timer), and then starts `RequestState(1)`. `RequestState(1)` calls `Stop`;
`Stop` writes `INBUILDSTANCE = 0`, calls `CloseYard`, then runs the authored
deactivation animation and caches the pieces. These are script waits, not
factory-node deadlines. The 5000 ms sleep begins when the deferred
`Deactivate` callback actually reaches the normal drain, not when the engine
lowers the activation edge.

`OpenYard` and `CloseYard` write the `YARD_OPEN` port and poll its resulting
level. If the gated port write has not taken effect, either script sets the
`BUGGER_OFF` port, sleeps for authored 1500 ms (45 VM timer ticks), and
retries. Both successful branches clear `BUGGER_OFF` before returning. Thus a
successful close has no additional engine timer, while a denied yard
transition can add one or more script retry sleeps. The compiled `ARMLAB`
script's activation and deactivation animations contain authored sleeps of
998 ms, 1008 ms, and 48 ms (29, 30, and 1 VM timer ticks); `CORLAB` has
different timings. Exact pose and duration remain data of the selected COB,
not a universal factory constant. COB sleep wake-up and piece interpolation
follow section 4.2 and section 4.6.

This callback path establishes why `StopBuilding` and `Deactivate` can be
queued in that order when the final count is exhausted: state 4 lowers the
building edge first, then the same-pass state-0 count test lowers activation.
Both starts are deferred and therefore normally execute in allocation order
at the next normal drain (`StopBuilding`, then `Deactivate`). When a counted
next product remains, state 0 raises no new activation edge, so no
`Deactivate` callback is issued; a successful state-2 restart can raise the
next `StartBuilding` edge in the same pump pass, leaving the two pad callbacks
ordered `StopBuilding`, then `StartBuilding` in the next drain.

**Established fact — read-port confirmations [R-P0-10]:** Reads route through
a five-slot form; the one-argument form pops the identifier off the script
stack and zero-fills the four argument slots, the five-argument form pops the
identifier plus four arguments. The reads confirm section 4.4's table: health
is the signed 16-bit health times 100, unsigned-divided by the definition's
maximum damage (0..100 domain); build-percent-left tests the float remaining
fraction against 0.0 and −99.0 with the section 4.4 formula; the piece-position
query packs one component from the high half of one piece-position word and
the other from the low half of a second word — the packing formula names raw
record words, not axes (open question below); the arc-tangent port is the only
rounding port. The port-11 correction is recorded in the section 4.4 table.

**Established fact — thread-start signal mask [R-P0-10]:** The slot allocator
seeds mask 1 on every successful allocation; the name- and id-form starters
never touch the mask afterward. Inside the interpreter, both script-starting
opcodes overwrite the allocated child's mask with the parent's current mask
after copying arguments; the signal opcode scans masks; set-signal-mask
replaces the issuing thread's. Hence every engine-started root — Create,
Killed, Activate, the Aim family, the Fire family, and the movement callbacks
— runs with mask 1 until the script changes it. This confirms section 4.2 and
refutes the "mask zero" report in the COB format document, which already
flagged itself as an unprobed community report.

**Open questions [R-P0-10]:** `TODO(question)` — the consumer of the
script-touched marker is unlocated: every write arm sets it and no reviewed
reader consumes it (write-only in the bounded census; store it opaque).
`TODO(question)` — the semantic NAME of the busy bit beyond the transport
scripts that write it; the admission half is closed: the nine-gate transport
admission predicate performs no busy-bit test (bounded census of the
admission predicate) [P1-05]. `TODO(question)` — the name of the engine-driven
cloak-family bit (bit 2 of the first state byte) and its writers outside the
edge machine (naming-only; the edge behavior is established). `TODO(question)`
— the axis naming of the packed position
halves: whether the word section 4.4 calls Z is Z (as asserted there) or X (as
an external host convention states) needs one controlled probe. `TODO(question)`
— the exact yard-character class matrix inside the yard-open admission gate
defers to the TNT yard-map format document rather than re-deriving byte masks.
`TODO(question)` — whether mission or third-party content depends on a bare
`get UNIT_HEIGHT` returning nonzero (retail returns 0 with a zero-filled
argument slot); answerable by an asset census of shipped scripts, not
recorded yet (see the lane report).

**Closed — placement bit-0 alias and mode matrix (2026-08-26):** the
footprint validator was re-exported and its mode branches are direct: mode 0
runs the occupancy rejections unconditionally; a nonzero mode runs them only
when the authoritative visibility/occupancy predicate passes — the cell's
LOS word tested for the LOCAL (human) player's team bit, or, under the
footprint-overlay global mode, the overlay character map's byte at the cell
being nonzero. The player alias is therefore the local player's team bit, not
the placing player's; section 6.4's bit-0 row is updated accordingly, and the
branch must still not be equated with the presentation fog surface.

### Closed — VM initialization and engine-call frames [R-COB-01 §1] (2026-08-28)

**Established — VM instance construction.** Constructing a unit's VM clears
only each thread's status word and the instance's active-thread counter, and
latches the tick denominator once from the engine's tick-rate global. Nothing
else in the eight thread records is initialized: program counter, depth, timer,
signal mask, receiver word and the ten window words of an unallocated slot
hold whatever the instance's memory held. Every field that matters is (re)seeded
at thread allocation or thread start, so the construction-time state is
unobservable except through the window words described below.

**Established — program bind and statics.** Binding a compiled program to the
VM allocates the per-piece animation array and the script statics array from
the engine's tagged allocator (the retail pool tags are `Object States` and
`Static Varibles`, quoted verbatim). The piece-animation array is zero-filled
by the bind — every piece starts with all animation words zero. The script
statics array is **not** initialized by the bind: no zeroing pass runs over it.
**Unknown:** the initial content the allocator itself provides (a recycled
block may carry a previous tenant's values) — a bounded trace of the tagged
allocator found no zeroing on either its cache or fresh paths, but the
allocator is shared engine infrastructure and not fully traced. Shipped
scripts write statics before reading them, so the observable behavior of stock
content is insensitive to this gap. Nanolathe should zero statics at bind and
record that as an I11-style determinism divergence, not as retail behavior.

**Established — thread allocation.** Allocating a thread validates the script
identity against the program's script count (a negative or out-of-range index
is the same allocation failure as a full pool), takes the lowest slot whose
status is idle, and seeds: status running, program counter at the script's
entry word, logical stack top −1, no completion receiver, signal mask 1,
active count incremented. The ten window words are not cleared.

**Established — the engine-call argument area and garbage propagation.**
The argument-carrying starter always writes **four physical cells** (window
words 0..3) from its caller's four arguments and only then sets the logical
top to `arity−1`. Consequences:

- For an arity below four, window words `arity..3` hold the caller's filler
  values, not memory garbage. Every traced producer passes explicit zeros in
  those filler cells (bounded census of the producer call sites: `Killed`,
  `SetDirection`, `SetSpeed`, `setSFXoccupy`, the `Aim*` family, `RockUnit`,
  `HitByWeapon`, `TakeDamage`, `TargetCleared`). A script whose declared
  locals alias those cells therefore reads zeros from an engine start.
- Window words above word 3 are untouched stale slot memory for every engine
  start, and ALL window words are stale for the zero-argument starts
  (`Create`, `Activate`/`Deactivate`, the `StartBuilding`/`StopBuilding` edge
  forms, `Fire*`, `StartMoving`/`StopMoving`/`MoveRate*`). A script that reads
  a local it never assigned receives the previous tenant's bytes of that
  thread slot (or the instance allocation's raw content). This is the retail
  garbage-propagation contract: bounded and producer-determined for words 0..3,
  genuinely indeterminate above them.

**Established — blocked synchronous-query lifecycle.** The synchronous
four-cell query forces the thread's receiver to none, writes each non-null
input cell into the next window word (null cells are seeded with a literal
zero and excluded from copy-back), forces the logical top to 3, runs the
interpreter on that one slot with delta 0 — no other slot and no piece pass —
and then copies window words 0..3 back to the non-null output cells. The
thread is **not** freed or reset after copy-back: a script that slept, waited
for a piece, or blocked on a call stays allocated in its yielded state and may
resume in a later normal or wake drain, but it has no receiver and cannot
revise the values the host already copied. A query whose name does not resolve
or whose pool is full returns failure with the outputs untouched.

**Established — full-pool and failure edges (reconfirmed).** `start-script`
with no free slot or a bad script id does not pop its arguments and continues;
`call-script` under the same conditions retains its arguments, records the
wait-slot sentinel −1, and blocks anyway — wedged until a matching signal
kills it. The name-form starters resolve names by a linear, case-sensitive,
first-match scan of the program's name table and fail silently on miss or full
pool (the argument-carrying form invoking a supplied receiver with 0). These
reconfirm section 4.3; no new edge was found.

**Established — unknown addressing modes and divide (reconfirmed with
mechanism).** Push with an addressing mode other than constant/local/static
pushes the interpreter's uninitialized scratch (indeterminate per execution);
pop with a mode other than local/static pops nothing and advances. The divide
opcode is an unguarded signed divide: a zero divisor or the minimum-integer
overflow case raises the processor divide fault and terminates the process.

**Established — initial piece draw/shade state and the shadow closure.** The
load-time fill walk (section 4.3's polarity paragraph) is confirmed at the
adapter level: per piece it sets the cache bit unconditionally, sets the draw
bit only when the model object has at least three vertices (explicitly
clearing it otherwise), and sets the shade bit unconditionally; the show/hide,
cache and shade adapter pairs write exactly those three bits of the render
piece record's flags word. The **disable-shadow opcode binds an empty adapter
on units** — it has no effect and there is no script-visible per-piece shadow
state anywhere on the unit side. Shadow rendering is a renderer concern
(document 03); no initial shadow derivation exists to reproduce. The unit
adapter's legacy two-argument effect binding is likewise an empty stub, as
already recorded in section 4.3.

**Closed — UNIT-04, missing or empty COB program.** The definition loader
builds the script path from the unit name, and a missing or unreadable script
file makes the loader store a **null program pointer** and continue — no
diagnostic, no substitution, and the definition is accepted (the FBI step of
the same loader is likewise existence-gated with a silent skip; document 02
owns that half). At unit creation a null program takes an explicit scriptless
branch: **no VM instance is allocated** (the unit's VM reference stays null),
the render-piece table is still built from the model in its program-less form,
and no `Create` is started. Retail therefore neither rejects the unit nor
crashes at creation nor substitutes a program — it creates a scriptless unit
whose pieces render and animate never. **Unknown (the crash-policy residual):**
engine callback producers load the unit's VM reference and call the starters
directly; the general unit update's `SetDirection`/`SetSpeed` site has no null
test in the traced window, so a scriptless unit reaching that block (its
definition float gate above zero and the global mover-active flag set) would
dereference null in retail. Stock content never exercises this — every shipped
definition has a script — so whether retail faults there, and what a robust
implementation should do instead, is `TODO(question)`; the settling probe is a
synthetic scriptless definition with a forced mover-active tick.

### Closed — VM random-draw census [R-COB-01 §2] (2026-08-28)

**Established — exactly two dispatched opcodes consume draws, both from the
global simulation stream.** Document 01 owns the streams; this census covers
opcode execution only (engine-side producers such as the order pump's wait
draws, the low-power manager's re-arm draw, automatic target acquisition, and
projectile spray are outside it and carry their own draw order).

| Opcode | Draws | Stream | Bound and order |
|---|---|---|---|
| `0x10041000` random | 0 or 1 | simulation | one draw bounded by `high − low + 1`; **a bound below 2 consumes no draw** and the result is the low value unchanged — `random(x, x)` is draw-free |
| `0x10071000` explode | 0 or 6 | simulation | six draws in fixed order bounded 3000, 3000, 3000, 40, 10, 40 — the fifth (bound 10) is dead, its stored result overwritten by the sixth; **zero draws when the flags word requests bitmap-only** |

The explode order reconfirms section 4.5 and pins the stream and the
bitmap-only suppression. The random opcode's zero-bound short-circuit is new:
the simulation stream's bounded sampler returns zero without advancing its
seed whenever the bound is below 2, so a script looping over `random(x, x)`
consumes no stream state.

**Established — no other consumer inside opcode execution.** A bounded census
of the entire unit-adapter surface the opcodes can reach — every engine-port
read and write arm including the activation edge machine, attach and detach,
the one-argument effect dispatcher, the piece position/angle getters and
setters, and the piece-flag adapters — found no call into either random stream.
The CRT stream is never touched from opcode execution. Re-entrant callbacks
fired by a port write (the activation edge family) are deferred starts that
allocate without interpreting, so they consume nothing either.

## 5. Engine-to-COB callbacks

### 5.1 Lifecycle and damage callbacks

**Established fact:** Engine-driven callbacks include:

- Create, once when the unit is created;
- Killed, synchronously for local authoritative death and asynchronously for
  received-network death replay, with cause/severity gates;
- HitByWeapon and TakeDamage on the damage families that enter the normal
  callback pair;
- Activate and Deactivate;
- StartBuilding and StopBuilding;
- TargetCleared, carrying the weapon-slot identity.

Callbacks are issued at the state transition that caused them, not deferred to
the renderer. Mobile/hover lethal, healing, and paralyzer paths do not all use
the normal HitByWeapon/TakeDamage pair; document 06 owns those gates.

**Established fact:** Lifecycle modes are fixed: `Create` (only when the definition has compiled COB, and VM/script/piece state is attached first) is I (immediate: all eight slots delta 0 plus one piece pass, one start per unit init); `Activate`/`Deactivate`/`StartBuilding` (the building-bit edge helper)/`StopBuilding` are D (deferred) gated on cached-bit rising/falling edges with producer-side suppression of unchanged state; `QueryBuildInfo` (the placement-time call) is Q (one-slot snapshot, no piece pass) with cell 0 starting at `−1` (cells 1..3 at `0`) consumed as piece index transformed to a world build position and network-emitted; the slot-form `StartBuilding` variant is D via direct slot start (see 5.3). <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** `TargetCleared` carries the zero-based weapon slot (0–2)
and is emitted only when a stored target is actually cleared: the slot's
commanded heading/pitch words must be non-default (`heading != 0` or
`pitch != 0x8000`) and are reset to `0`/`0x8000` by the clearing itself. Each
clearing site also resolves the StartBuilding name and discards the result —
that lookup is not a start — and no completion receiver exists for this
callback.

**Established fact:** Fixed-point trigonometry for callback arguments shares
one 512-entry word sine table whose entry *i* is
`round(8192 · sin(i·2π/512))`; the cosine of an angle reads the same table a
quarter turn ahead of the phase, and products round to nearest before
truncation.

**Established fact:** On a normal-kind damage packet (kind `1`) applied to an active,
not-yet-dying victim, health (the signed 16-bit health field) is subtracted FIRST (`-= dmg`), then `HitByWeapon` starts
deferred (mode D, arity 2, receiver null) with two arguments `(cos(dir)·400, sin(dir)·400)` where `dir`
is the packet direction byte shifted left by eight into the 65536-domain (`dir = byte << 8`), resolved through the shared 512-entry sine table with round-to-nearest (same helpers as `RockUnit` but positive signs, radius 400), and
`TakeDamage` starts independently immediately after (mode D, arity 1, receiver null) with one argument, the
post-hit health percentage `clamp(health·100/maxHealth, 0, 100)` (health signed 16-bit, maxHealth the definition's maximum-damage field, an unsigned division of the product with explicit `<0→0`, `>100→100` clamps) computed as
an unsigned division of the product. Either starter can fail separately (invalid name or full pool). Heal (`0`) and paralyze (`2`) and non-normal kinds skip this pair entirely (the paralyze kind builds a paralyze order instead); lethal damage against a movement-category-1/2 victim sets the death latch (OR `0x4000` into the unit's death-latch word) and returns without any callbacks. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Local authoritative death runs a synchronous four-cell
`Killed` query with outputs severity and variant before the death packet is
built:

```
severity = ((−health · 100) / maxHealth + priorSample) / 2   // unsigned divide
severity = clamp(severity, 1, 100)
```

where `priorSample` is the unit's prior-severity-sample byte. Cause overrides, in evaluation
order: cause byte `7` yields severity 0 with variant 1 and no query; cause
`4`, `5`, or `9` — or a still-positive health value — yields severity 0 with
variant 0 and no query; otherwise the query runs and the variant cell is NOT
pre-initialized (a script that assigns it has that value copied back; an
absent assignment serializes runtime stack history). After any query, a
nonzero remaining-work fraction forces the variant to 0. The death packet
packs the severity byte plus `(cause << 4) | (variant & 0xf)`.
Received-network death replay instead starts the asynchronous one-argument
`Killed` with the signed packet severity byte, only when that byte is
positive. The severity input byte is maintained by the unit tick: every 30
ticks it recomputes `clamp(health·100/maxHealth, 0, 100)`, stores it as the
current severity-sample byte, and shifts the previous sample into the prior-sample byte — the severity input is
therefore the PREVIOUS 30-tick-window health percentage.

### Closed — the activation/production/yard edge machine [R-UNIT-06 §2] (2026-08-28)

**Established — one byte, one machine.** All of the Activate/Deactivate,
StartBuilding/StopBuilding edge-form callbacks and the yard-open edge ride a
single engine-state byte on the unit record, written only through one edge
machine. The machine takes a bit mask and an on/off value, computes the new
byte (`byte | mask` or `byte & ~mask`), stores it, and — only when the byte
actually changed — derives rising and falling edges from the old/new
difference. Producer-side suppression of unchanged state is exactly this
change test; there is no per-callback suppression beyond it. Established bit
assignments: bit 0 = activated (the on/off state port 1 writes); bit 2 =
yard-open; bit 3 = building/production. The remaining bits of the byte are
written through the same machine by their producers and are otherwise opaque.

**Established — edge effects, in evaluation order.** The state byte is
written FIRST; the callbacks are started immediately after but in deferred
mode (they execute in the visit's normal script drain, per [R-COB-02 §2]):

- Rising bit 0: start the zero-argument `Activate` (deferred), then emit the
  engine notification code 3.
- Falling bit 0: start `Deactivate` (deferred), then notification code 4.
- Rising bit 3: start `StartBuilding` (deferred, zero-argument edge form).
- Falling bit 3: start `StopBuilding` (deferred).
- Rising bit 2: emit notification code 14, then walk the unit's registered
  waiter list — each entry carrying a callable object is invoked with the
  constant `0x10000` (the yard-open wake).
- Falling bit 2: notification code 15.
- On any edge: if the unit belongs to the local player and is selected, mark
  the battle-interface dirty bit (presentation-only).
- On any edge, for a computer-player-owned unit: emit network event type
  `0x11` carrying the unit's slot identity and the NEW engine-state byte.

The callbacks fire AFTER the state mutation in the same visit: the byte the
machine leaves behind is already visible to any code that runs between the
machine and the next script drain, while the scripts themselves observe the
new state one drain later unless a wake flush intervenes.

**Established — the producer set.** The machine's call sites span: the order
handlers (Stop, the cloak pair, the factory production state machine — which
raises the building bit on production start after inserting the product's
GetBuilt record), the load/unload/air executors (a load's first phase raises
Activate and detaches a carried carrier through the same machine), the
construction work helper, the mover-mode force helper inside movement
integration, the per-player economy settlement pass (activation toggles), the
COB port-1 write arm (so scripts and engine producers share one edge
semantics), and the damage/capture funnel — whose site re-asserts the CURRENT
byte as the mask with the "on" value, a resync pattern that converts any
external byte mutation into proper edges. Bounded census: forty-five call
sites in the code sections.

**Established — the severity sample is two bytes.** The unit tick's 30-tick
health sampler keeps a CURRENT and a PRIOR byte, shifting current into prior
at each boundary; the local `Killed` severity query consumes the PRIOR byte
(the section above already states the shift; this records that the two bytes
are distinct storage, so an implementation keeping a single field delivers a
one-window-newer severity input than retail).

### 5.2 Movement and medium callbacks

**Established fact:** Edge-triggered callbacks include StartMoving, StopMoving, MoveRate1, MoveRate2, MoveRate3, and setSFXoccupy with a medium-band value. Start/stop callbacks are issued when movement transitions; rate callbacks are issued on movement-tier changes; occupancy callbacks are issued only when the computed band changes.

**Established fact:** The movement tier is a signed, inclusive threshold
classification of one 32-bit magnitude word against two definition
thresholds (the tier classifier runs inside the movement integration): category 1 iff `magnitude <= thresholdLower`; category 2 iff `thresholdLower < magnitude <=
thresholdUpper`; category 3 above `thresholdUpper` — all comparisons signed 32-bit, with the
category-1 bound inclusive and the category-2 upper bound inclusive. Category 0 overrides
when the mover inhibit bit (bit 2 of the mover's state byte) is set, the unit is attached to a carrier (the carrier field is nonzero), or both magnitude words (the 32-bit magnitude word and its adjacent word) are zero. The category is cached in
two bits (bits 2–3 of the movement-mode word) and an unchanged category emits
nothing. On change: into category 0 from nonzero issues `StopMoving` (I); into a
nonzero category from 0 issues `StartMoving` FIRST (I) and then the matching
`MoveRateN` (I); other nonzero-to-nonzero changes issue only `MoveRateN` (I). All are
immediate wake-flag starts via the zero-argument name-form adapter (wake 1), so the `StartMoving` drain — all eight slots at
delta 0 plus one piece pass — forms a barrier between it and the `MoveRateN`
that follows; the cache update (the two-bit category shifted left by two into its field) is the final write. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** `setSFXoccupy` is a five-value classifier (run after the rate classifier inside the movement integration, mode I, arity 1, spelling exact with lower-case `s`). Occupancy is `0` when the current movement mode (the low two bits of the movement-mode word) is not `1` or `2`. In those modes the classifier compares signed Y (the signed high word of the unit's 16.16 Y), the map water level (`wt`), the definition's waterline byte (`wl`) and the definition's model-bottom signed word (`mb`), yielding `4` if `wy > wt`; otherwise starting from the cached band it can yield `1` when `wy - wt > -5`, `2` when `wl + wy == wt`, and `3` when `mb + wy < wt`, with later tests overriding earlier ones (`1→2→3`). The value is cached and emitted via the argument-carrying name-form adapter only on change (cell 0 = band `0..4`). <!-- source orchestration-research-cob-callbacks.md -->

**Supported inference:** Wake effects and medium bands are script-authored behavior. The engine does not need a separate hard-coded wake renderer to reproduce the callback contract.

### 5.3 Weapon and query callbacks

**Established fact:** The engine invokes `QueryPrimary`/`QuerySecondary`/`QueryTertiary` (Q, cell 0 = `0`), `AimFromPrimary`/`AimFromSecondary`/`AimFromTertiary` (Q, cell 0 = `−1`; if still `−1` the engine falls back to the matching `Query*` with default `0`), `SweetSpot` (Q, cell 0 = `0` transformed through the selected piece), and `QueryNanoPiece` (Q, cell 0 = `0` → world nano origin) synchronously (mode Q, cells 1..3 null → seeded 0 and excluded from copy-back). `AimPrimary`/`AimSecondary`/`AimTertiary` are asynchronous (D) with heading/pitch arguments. Successful normal, ballistic, and vertical-launch weapon spawners invoke `FirePrimary/Secondary/Tertiary` (D, 0 cells via the zero-argument name-form adapter) and `RockUnit` (D, 2 cells via the argument-carrying name-form adapter); the dropped-family inline allocator does not. Burst clones do not rerun the root Fire/Rock callbacks. A Q script that sleeps/waits leaves the host holding the partial cell-0 value (section 4.2) — the blocked thread lingers with no receiver that can revise it. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** `AimPrimary`/`AimSecondary`/`AimTertiary` carry unsigned
16-bit heading then unsigned 16-bit pitch (arity 2, issued by the weapon update). Two issue forms exist,
both preceded by clearing the slot's aim-state word to `0` and both storing the
commanded angles in the weapon-slot record's commanded heading and commanded pitch words, where the three weapon-slot records are addressed at a fixed stride from the unit record for slot `k=0..2`. The ballistic default computes
relative heading as bearing-to-target minus unit heading and pitch from the
ballistic solver; **a solver returning the `-0x8000` pitch sentinel suppresses
the Aim start entirely**, otherwise the start carries `heading & 0xffff` then
`pitch & 0xffff` via the argument-carrying name-form adapter forwarding to the argument-carrying slot-form adapter (mode D, receiver the slot's completion-receiver word). The fixed-forward branch — the weapon-slot record's flag byte bit 4 set,
the aim issue bit clear, and no live tracked target (target inactive or the
slot's adjacent status word nonzero) — starts with `(0, 0)`, the fixed-forward
heading. After either start the engine emits a network event packet type `0x10`
`{u16 unitId, u16 slot, u8 arity=2, heading, pitch}` behind the aim event's global option bit (mask `1`)
and sets the weapon flags byte bit 0 (the issue bit). <!-- source orchestration-research-cob-callbacks.md -->

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Established fact:** `StartBuilding` has an argument-less edge form, issued
on the cached building-bit rising edge (mode D, 0 cells), and a slot-form heading variant that
resolves the name to a weapon slot through the shared name-lookup helper and starts THAT slot directly via the argument-carrying slot-form adapter (mode D, receiver null) with one
argument `heading & 0xffff` (the low 16 bits of the producer heading), plus its network event.
Four weapon-target routines call the `StartBuilding` name lookup while clearing targets but discard the result and are not producers.

**Correction — the slot-form heading variant does not write the
StopBuilding-pending flag (2026-08-28, superseding this section's earlier
text and [R-COB-02 §1]'s initial reading).** This section previously said the
slot-form heading variant also sets the production record's
StopBuilding-pending flag `0x400000`. The direct immediate-operand census of
[R-ORDER-02 §2] finds exactly one writer of that flag on order records — the
order-record emission helper with its nine handler call sites (MobileBuild,
HelpBuild, Capture ×2, Reclaim, Resurrect, RepairUnit, VTOL_MobileBuild,
VTOL_HelpBuild), which arranges the name-form arguments
`(0, 0, 1, recordPointer & 0xffff, 0, 0)` — and no other writer anywhere in
the code sections. The construction-command slot-form heading variant is not
among those call sites; it starts the slot and emits its network event
without touching the flag. Cleanup's flag consumption contract is unchanged
(section 3.3). <!-- source orchestration-research-cob-callbacks.md -->

### Closed — the StartBuilding argument vector and the stock-script census [R-UNIT-06 §4] (2026-08-28)

**Correction — the record-form StartBuilding payload arrives as the FIRST
script argument, not the fourth (supersedes [R-ORDER-02 §2]'s arrange
description as quoted above and the Missing-list entry derived from it).**
The earlier reading described the emission helper as arranging
`(0, 0, 1, recordPointer & 0xffff, 0, 0)` and placed the record-pointer value
"fourth". That tuple is the raw PUSH sequence of the starter call, not the
script-visible argument vector. The starter's actual parameter map is
(receiver, wake, arity, cell0, cell1, cell2, cell3): the receiver and wake
consume the first two pushed zeros, the arity is the pushed `1`, and the four
pushed cells `(recordPointer & 0xffff, 0, 0, 0)` are written **in order into
window words 0..3** — four unconditional writes regardless of arity, with the
logical top set to `arity − 1`. So the record-form emission delivers:

- arity 1, wake clear (deferred), receiver none;
- window word 0 = the low 16 bits of the issuing order-record pointer (the
  FIRST script argument — the same slot the heading-form StartBuilding uses
  for its heading);
- window words 1..3 = 0.

The same cell map holds for the other arranged callbacks: `TargetCleared`
carries the slot index in window word 0 (fillers 0, arity 1), and the network
mirror of a script-call emission carries (unit, callback identity, arity, the
four cells). Two implementation consequences: a script's `StartBuilding(
heading)` first parameter IS the record-pointer value on the nine
nanolathe/assist paths, and an arrange vector shaped `(0, 0, 1, payload, ...)`
would put a spurious `1` in word 2 and the payload in word 3 — neither
matches retail.

**Established — stock-script census (the settled TODO(question)).** A full
decode of every shipped COB (835 scripts across the retail archives — base,
expansion, patch, mission and tactic packs) locates 133 `StartBuilding`
functions. Scanning each body for local reads:

- **Zero** scripts read or write the fourth argument cell (window word 3).
- 49 scripts read the FIRST argument — the shipped mobile-builder and
  commander build scripts among them: they store it (into a static consumed
  as the build/turret heading) or turn a piece to it directly; the ten
  commander-family scripts additionally read the second argument (their
  second declared parameter).
- The remaining 84 bodies read no arguments at all.

**Verdict.** The record pointer's low 16 bits are consumed by stock content —
as a build-heading ANGLE in the 65536 domain, wherever the record-form
producer fires (the nine nanolathe/assist paths). A placeholder of `0` is
therefore NOT observationally equivalent on stock content: commander torsos
and mobile-builder heading statics would take heading 0 instead of the
pointer-derived value. An implementation cannot reproduce the value
deterministically from a pointer (retail reads the low half of a heap
address); the honest contract is: the first argument carries the issuing
order record's identity value `& 0xffff`, and retail's observable use of it
is as an arbitrary-but-stable per-record angle. Any Nanolathe substitution
must be recorded as a divergence, not silently zeroed. The former
Missing-list question "consumers of the fourth argument" is closed by this
census; the residual question becomes the semantic NAME of that
first-argument value (Established behavior, unresolved friendly name).

**Established fact:** Engine-issued value callbacks convert exactly:
`SetDirection` (issued by the general unit update, guarded on a definition float `>0.0` and a nonzero
global mover-active flag) passes a zero-extended 16-bit direction word from the global direction word;
`SetSpeed` from the general unit update (immediately after) passes the signed global speed word shifted left by four (arithmetic, ×16);
the second `SetSpeed`, from the footprint path (guarded on a different definition float `>0.0`), passes the unsigned 16-bit sum over covered footprint
cells of each occupying unit's size byte plus one — semantic unit not
established; and `SetMaxReloadTime` (issued after `Create` so it lands outside
Create's own immediate drain), scans all three weapon slots for the maximum
authored reload field and reports `trunc(maxReload · 1000 / 30)` (multiply, then signed divide by 30 truncating toward zero) — reload ticks
converted to milliseconds. None of these adapters deduplicates; suppression
can only live in the producers. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** Query seeds are exact: the synchronous four-output
`QueryTransport` (mode Q) pre-seeds output cell 0 to `-1` (remaining outputs passed null, seeded 0 by the query interpreter and excluded from copy-back), so a missing script leaves `-1`, the
root-piece fallback, which is later consumed as the attachment piece index.
Every air-transport selection site seeds all four `QueryLandingPad` outputs
to `-1`, tests candidate pieces in cell order `0..3` against validity/availability,
accepts the first pass, and keeps `-1` otherwise; some paths query the
carrier first, then the transported unit. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** The aim-ready handshake: the producer clears the slot's aim-state word to `0`, stores heading/pitch in the slot's commanded heading and commanded pitch words, and starts `Aim*` deferred (mode D) with the embedded completion receiver (the slot's completion-receiver word); immediately after it sets the weapon flags byte bit 0 (the issue bit) and emits the type `0x10` network packet (see above). The issue bit clears when target acquisition fails (an AND-clearing of the flags byte bit 0) and gates re-issue (a new `Aim*` starts only while bit 0 is clear). The argument-carrying slot-form adapter stores the receiver in the thread's receiver slot; on pool exhaustion or invalid identity it invokes the non-null receiver's closure with value `0`, while an explicit script `return` (`0x10065000`) pops the top value and invokes the receiver's closure with that value — the receiver object is re-read at return time. Signal termination (`0x10067000`) and abnormal termination (invalid opcode kill) never invoke a receiver. A zero delivery has no effect while any NONZERO delivery marks the weapon aim-ready — so name absence, thread-pool exhaustion (both deliver `0` via the argument-carrying slot-form adapter), or an authored zero return each leave the weapon unable to fire. The fire path entered with the issue bit set additionally consults a per-weapon permission function referenced by the weapon-slot record before firing; the issue bit alone authorizes nothing. <!-- source orchestration-research-cob-callbacks.md -->

**Established fact:** The run-script network dispatch (incoming packet case
`0xE`) resolves the `u16` unit identifier at packet offset +1 through the unit
table (requiring the active bit), then starts an authored function with:
identity = SIGNED 16-bit script slot from packet +3 (negative or out-of-range
rejected); receiver = none; deferred mode; arity = unsigned byte from packet
+5; and four 32-bit dwords from packets +6/+10/+14/+18. All four physical
values are always written to window words 0–3 but only the byte arity sets
the logical top (`depth = arity − 1`). There is no fixed callback name for
this path.

**Closed — the Aim* receiver closure (2026-08-26):** the completion receiver
the producer passes to the name-form starter is a pointer to the weapon-slot
record itself (the slot's completion-receiver word). The starter stores the
receiver in the VM thread slot and pushes the argument cells; when the script
name is absent it invokes the receiver's closure immediately with value 0.
The interpreter's return opcode pops the script's return value and, when the
thread's receiver word is non-null, invokes `(*(*receiver))(value)` — the
receiver is re-read at return time and its first word is called with the
returned cell. The "exact write" is therefore that invocation: zero has no
effect, any nonzero value marks the aim ready — the zero/nonzero grant is now
derived from the returned cell directly, not carried. The residual narrows to
the identity of the closure target (the dword at the receiver word and the
function it points to): a bounded indexed-store census of the whole
instruction listing finds no indexed writer of the receiver word (the
per-slot init writes only the commanded heading/pitch words), so that target
is presumably written by the per-definition slot fill with a non-indexed
pattern — keep `TODO(question)` R-1 for it (coordinated with document 06
section 3.4, which carries the same residual). Behavior when all eight script
slots are occupied is established in section 4.3 and differs by starter
(deferred `Aim*`/`HitByWeapon`/`TakeDamage` deliver `0` through the receiver
path; `Create` via the zero-argument name-form adapter has no receiver).
<!-- source orchestration-research-cob-callbacks.md -->

### 5.4 Same-tick callback windows [GAP T15]

**Established fact [GAP T15]:** Within one unit visit the sweep executes, in
order: the general unit update, which can queue deferred `SetDirection` and
`SetSpeed`; the weapon update for eligible players, which can queue deferred
`TargetCleared` and the `AimPrimary/Secondary/Tertiary` family, and whose fire
decisions reach the projectile creators that queue
`FirePrimary/Secondary/Tertiary` followed by `RockUnit`; the normal script
drain with tick delta 1, running all eight threads once and then one
piece-interpolation pass; order and construction work (primary pump
head-blocking then secondary skip-not-due); movement integration, whose medium
classifier issues `StartMoving`, `StopMoving`, `MoveRateN`, and then
`setSFXoccupy` as immediate wake-flag starts; and finally the slot-end death
handling, which can run the synchronous local `Killed` query. The authoritative
sweep order lives in section 1.1; this section records the callback-window
consequences that other documents cite as [GAP T15].

A deferred callback queued before the normal drain executes in that same
visit; one queued after it normally waits for the next visit, except that any
later immediate-start callback on the same virtual machine performs an
all-slot delta-0 drain that can execute it earlier. Damage callbacks queued by
the post-unit projectile phase normally land on the next visit, while damage
packets processed during event ingress before the unit phase can queue theirs
in time for that tick's normal pass. A same-tick window also separates the
pump from the movement service: an arrival or movement-callback bit set during
movement integration is observed by the next pump visit, not the current one
(section 8.3); and a product published during the sweep is visible to the
projectile phase and to later same-sweep builders, while occupancy and LOS
publication happen after settlement (section 3.8).

### Closed — vertical-slice callback and port mappings [R-COB-02 §1] (2026-08-28)

**Established — issue shapes.** Every callback below was re-traced at its
producer this round; mode letters are section 4.2's. "Immediate" means the
starter's wake flag was set: the allocation is followed inline by an
all-eight-slot delta-0 interpreter pass plus one piece pass. "Deferred" means
allocation only; the thread first runs in the visit's normal drain, or in the
all-slot drain of any later immediate start on the same VM.

| Callback | Producer site | Mode / wake | Argument cells (cell 0 first) | Receiver | Return consumption |
|---|---|---|---|---|---|
| `Create` | unit creation, after the program bind and render-piece table | D + **wake 1** (immediate) | none (zero-argument start; window stale) | none | ignored |
| `SetMaxReloadTime` | unit creation, issued after `Create` so it lands outside Create's own drain | D | 1: `trunc(maxReload · 1000 / 30)` over the three slots | none | ignored |
| `Activate` / `Deactivate` | activation edge machine (engine writers and the port-1 write) | D (deferred) | none | none | ignored; the engine notification codes 3/4 follow each |
| `StartBuilding` (edge form) | building-bit rising edge of the same machine | D (deferred) | none | none | ignored |
| `StopBuilding` (edge form) | building-bit falling edge | D (deferred) | none | none | ignored |
| `StartBuilding` (heading form) | production start on a weapon-capable unit | D (deferred), direct slot start | 1: `heading & 0xffff` | none | ignored |
| `QueryBuildInfo` | factory production state 2, placement time | Q (synchronous) | 1 output: cell 0 **seeded −1** by the caller (cells 1..3 null → seeded 0, no copy-back) | none | cell 0 consumed as the exit piece index → world build position; failure leaves the seed untouched |
| `SetDirection` | general unit update (definition float gate > 0 and global mover-active) | D (deferred) | 1: the global direction value, zero-extended 16-bit; fillers 0 | none | ignored |
| `SetSpeed` | general unit update, immediately after `SetDirection` | D (deferred) | 1: the signed global speed value × 16; fillers 0 | none | ignored |
| `StartMoving` | movement integration, tier 0 → nonzero, issued FIRST | D + **wake 1** (immediate) | none | none | ignored |
| `StopMoving` | movement integration, tier nonzero → 0 | D + **wake 1** (immediate) | none | none | ignored |
| `MoveRate1`/`MoveRate2`/`MoveRate3` | movement integration, tier change (nonzero→nonzero emits only these) | D + **wake 1** (immediate), one per change | none | none | ignored |
| `setSFXoccupy` | movement integration, after the rate classifier, on band change only | D + **wake 1** (immediate) | 1: occupancy band 0..4; fillers 0 | none | ignored |
| `AimPrimary`/`AimSecondary`/`AimTertiary` | weapon update (ballistic/turret solver or fixed-forward branch) | D (deferred) | 2: `heading & 0xffff`, `pitch & 0xffff` (fixed-forward issues `(0, 0)`); fillers 0; a solver pitch of the −0x8000 sentinel suppresses the start | **the weapon slot's completion-receiver word** | the only grant path — see below |
| `FirePrimary`/`FireSecondary`/`FireTertiary` | weapon spawner after successful root allocation and the start sound | D (deferred) | none (zero-argument start) | none | ignored |
| `RockUnit` | same spawner, immediately after the matching `Fire*` | D (deferred) | 2: `−cos(rel) · 800`, `−sin(rel) · 800` with `rel` = commanded barrel heading − unit heading through the shared 512-entry table, round-to-nearest; fillers 0 | none | ignored |
| `HitByWeapon` | normal-kind damage packet, after health subtraction | D (deferred) | 2: `cos(dir) · 400`, `sin(dir) · 400`, `dir` = packet direction byte shifted into the 65536 domain; fillers 0 | none | ignored |
| `TakeDamage` | same packet path, started independently after `HitByWeapon` | D (deferred) | 1: post-hit health percentage `clamp(health·100/maxHealth, 0, 100)`, unsigned division; fillers 0 | none | ignored |
| `Killed` (local authoritative) | slot-end death handling, before the death packet is built | Q (synchronous, 4 cells) | outputs severity and variant; cause 7 → severity 0 variant 1 without a query; causes 4/5/9 or positive health → severity 0 variant 0 without a query; otherwise cell 1 (variant) is NOT pre-initialized | none | severity and variant consumed into the death packet; a nonzero remaining-work fraction forces variant 0 after any query |
| `Killed` (network death replay) | unit finalization, only when the packet severity byte is positive | D + **wake 1** (immediate) | 1: the signed packet severity byte; fillers 0 | none | ignored |
| `TargetCleared` | weapon update, when a stored commanded target is actually cleared | D (deferred) | 1: the zero-based weapon slot; fillers 0 | none | ignored; each clearing site resolves the `StartBuilding` name and discards the result |
| `QueryTransport` | transport attachment | Q (synchronous) | 1 output: cell 0 **seeded −1**, others null | none | cell 0 = attachment piece; −1 survives as the root-piece fallback |
| `QueryLandingPad` | air landing selection | Q (synchronous) | 4 outputs, **all seeded −1** | none | first candidate piece 0..3 to pass validity/availability is accepted; all −1 keeps the order alive for retry |
| `QueryNanoPiece` | nanolathe emission setup (pipeline owned by document 03) | Q (synchronous) | 1 output: cell 0 seeded 0 | none | cell 0 = the nano origin piece, transformed to a world point |
| `QueryPrimary`/`QuerySecondary`/`QueryTertiary`, `AimFrom*`, `SweetSpot` | weapon aiming/fire point resolution | Q (synchronous) | 1 output: cell 0 seeded 0 (`AimFrom*` seeded −1 with fallback to `Query*` at 0) | none | cell 0 = muzzle/aim piece |

**Established — the slice's port usage.** All twenty engine ports are
tabulated in section 4.4 and their write arms in section 4.7; the vertical
slice exercises: reads — port 4 (health percent), port 17 (build percent
left), port 18 (yard open); writes — port 1 (activation, through the edge
machine), port 5 (in-build stance), port 6 (busy), port 18 (yard open,
admission-gated), port 19 (bugger off), port 20 (armored). The compiled
write form pushes the identifier first and the value second.

**Established — the Aim-ready grant rule, confirmed, with the producer census.
** Aim-ready is granted **only** by a nonzero value delivered through the
completion receiver: the interpreter's return opcode pops the script's return
cell and, when the thread's receiver word is non-null, invokes the receiver's
closure with that cell — zero has no effect, any nonzero value marks the
weapon aim-ready. Every other event on the path delivers zero or nothing:
name absence and pool exhaustion invoke the receiver with 0 through the
starter's failure path; signal termination and the invalid-opcode kill never
invoke a receiver; the fixed-forward `(0, 0)` branch and the type-0x10 network
emission are start producers, not grant producers; the issue bit is written by
the producer (set after the start, cleared on target-acquisition failure) and
gates re-issue, not readiness. A bounded census found **no additional
producer** of the grant: no other writer of the aim-ready state exists in the
traced weapon-update and starter paths. One auxiliary helper exists that scans
the eight threads and clears a receiver word matching a supplied pointer, but
it has **no located caller** in a bounded relative-call census of the code
sections — recorded as an orphan; it does not grant anything. The identity of
the closure target behind the receiver word remains `TODO(question)` R-1
(section 5.3); the grant rule itself no longer depends on it.

### Closed — same-tick callback integration trace spec [R-COB-02 §2] (2026-08-28)

**Established — composed order of one unit visit.** The unit sweep visits
players in ascending slot order and, within a player, units in ascending slot
order (immediate reuse of freed slots applies). A unit whose alive bit is set
is visited even when its death latch is already set — the latch suppresses
nothing until the visit's final step. One visit executes, in order:

1. **General unit update.** May run the activation edge machine (deferred
   `Activate`/`Deactivate`/`StartBuilding`/`StopBuilding`) and, behind the
   definition-float and global mover-active gates, queue deferred
   `SetDirection` then `SetSpeed`.
2. **Weapon update**, slots 0..2 ascending. Reload decrement; target
   resolution may emit deferred `TargetCleared`; automatic acquisition may
   run; aim issue clears the slot's aim-state word, stores the commanded
   heading/pitch, starts deferred `Aim*` with the slot receiver, then sets
   the issue bit and emits the aim network event; the fire decision reaches
   the spawner, which allocates the root projectile, plays the start sound,
   queues deferred `Fire*` then `RockUnit`, runs the smoke gate, and then
   applies reload, ammunition, and resource debit.
3. **Normal COB drain, delta 1.** If any thread is active: the interpreter
   runs all eight slots in ascending slot order, then one piece-interpolation
   pass runs. **Every deferred callback queued in steps 1–2 executes here**,
   ordered by thread slot (allocation order), not by queueing order.
4. **Orders and build work.** The primary pump (head-blocking,
   restart-from-head) then the secondary pump (front-to-back, skipping
   not-due records). Handlers may queue further deferred callbacks — those
   wait for the next visit's normal drain unless a later immediate start
   flushes them (see barriers). Construction completion can create a unit,
   whose `Create` drain runs inline at the creation site.
5. **Movement integration.** The rate classifier computes the category
   (0 overrides: inhibit bit, attached carrier, or both magnitude words zero;
   else 1 up to the lower definition threshold inclusive, 2 up to the upper
   threshold inclusive, 3 above; cached, unchanged emits nothing). On change:
   into category 0 from nonzero issues `StopMoving`; into a nonzero category
   from 0 issues `StartMoving` FIRST and then the matching `MoveRateN`; other
   nonzero-to-nonzero changes issue only `MoveRateN` — all immediate starts.
   The medium classifier then runs and emits `setSFXoccupy` (immediate) on a
   band change only; its cache write is the step's last effect.
6. **Slot-end death handling.** If the death latch is set: the synchronous
   local `Killed` query runs (severity/variant, with the cause bypasses of
   section 5.1), then finalization — bookkeeping, score, the corpse
   placement, cargo detachment, the deferred-start `Killed` for network
   replay (immediate drain inline), and the free that makes the slot
   immediately reusable.

**Established — barriers and flush points.**

- The **normal drain** (step 3) is the barrier that executes everything
  deferred queued in steps 1–2.
- Every **immediate (wake) start** — `Create`, `SetMaxReloadTime` at creation,
  `StartMoving`/`StopMoving`/`MoveRateN`, `setSFXoccupy`, the network-replay
  `Killed` — performs an all-eight-slot delta-0 interpreter pass plus one
  piece pass inside the caller's context before returning. Any deferred
  callback queued earlier on that VM and not yet executed therefore runs at
  that flush point; this is the only way a step-4 deferral can run in the
  same visit.
- A **synchronous query** runs its single slot inline with delta 0 — no other
  slot, no piece pass — and copies its cells back immediately; a query that
  yields leaves its thread allocated (section 4.2, [R-COB-01 §1]).
- **Piece interpolation** runs exactly once per normal drain and once per
  immediate drain (delta 0), never around queries.
- **Cross-visit windows:** callbacks queued by the post-unit projectile phase
  (`HitByWeapon`/`TakeDamage` from impacts) land on the **next** visit's
  normal drain; the same-tick exception is damage applied during event
  ingress before the unit phase. An arrival bit raised during movement
  integration is observed by the next pump visit, not the current one
  (section 8.3).

**Fixture notes for a trace test.** A deterministic fixture needs: (a) the
per-visit step order above with one unit per step boundary; (b) two callbacks
of different modes queued in one step to observe the slot-order (not
queue-order) execution in step 3; (c) a deferred callback queued in step 4
plus a category change in step 5 to observe the wake-flush pulling it into
the same visit; (d) a `sleep 0` thread observing the one-tick minimum across
a normal drain; (e) a latched unit that still fires in steps 2–5 of its death
visit and frees at step 6; (f) draw accounting: any `random` opcode with an
equal low/high consumes nothing, and a bitmap-only `explode` consumes nothing,
against the census in [R-COB-01 §2].

### P28 construction-KBot initial-pose boundary [R-P28-COB-01R] (2026-08-28)

**Established — retail creation has no asset-specific settling pass.** The
common allocator binds the selected model and COB, builds the strict piece
map, starts `Create` once in immediate mode, and performs the all-eight-slot
delta-zero drain plus one piece pass described in [R-COB-02 §2]. Scenario
placement and factory-product allocation both use that common initializer.
Factory allocation subsequently creates `GetBuilt` on the product and raises
`StartBuilding` on the **factory**; it does not issue `StartBuilding` on the
new product. The ordinary unit visit and tick-end publication are the only
later execution/publication boundaries. No ARMCK-specific pose write or extra
pre-publication COB drain was found or is authorized.

**Established — stock ARMCK asset identity and strict piece relation.** In the
reference install the resolved definition and model identity are `ARMCK`, and
the selected script is `scripts/armck.cob` (the winning provider in that
install is `rev31.gp3`). Its twelve COB pieces map by exact authored name to
the depth-first 3DO table as follows; this is a name link, not a positional
guess:

| COB order | Piece | Model order |
|---:|---|---:|
| 0 | `nanospray` | 10 |
| 1 | `turret` | 4 |
| 2 | `rfoot` | 2 |
| 3 | `lfoot` | 3 |
| 4 | `pelvis` | 1 |
| 5 | `lflap` | 5 |
| 6 | `rflap` | 6 |
| 7 | `guncover` | 11 |
| 8 | `nozzle` | 9 |
| 9 | `arms` | 7 |
| 10 | `nanobody2` | 8 |
| 11 | `ground` | 0 |

The stock `StartBuilding` and `StopBuilding` entries are later builder-state
callbacks. They are not part of factory-product creation. They enter through
the established deferred callback path, and their piece targets and waits are
authored by this COB; they must not be replaced with engine-side piece values.

**Established — Nanolathe route diagnosis, not retail pose evidence.** With
`NANOLATHE_TA_ROOT` selecting the reference assets, the production strict
binder starts `Create` exactly once and performs exactly one delta-zero drain.
Immediately afterward its twelve VM transforms are zero, their model-derived
draw flags are set, and `InBuildStance` and `Busy` are clear. The callback
lifecycle trace observes `Create` finishing at the next normal drain, followed
by deferred `StartBuilding` and `StopBuilding` start/finish pairs when those
edges are deliberately exercised. Mission reconstruction publishes a copied
version of that VM state; later live-piece mutation does not change the
committed frame. A real ARMLAB state-2 allocation resolves the same ARMCK
model/COB/piece map and the same post-Create values in a distinct VM; it does
not alias or copy the factory's VM, stance, busy flag, or building edge. Only
the factory receives the production `StartBuilding` edge.

**Unknown — exact retail first committed visual pose and delta-zero
sufficiency.** The facts above close the Nanolathe route and eliminate wrong
asset resolution, loose piece linking, repeated `Create`, product/factory VM
aliasing, inherited product stance, and live-state publication as explanations
for the observed initial pose. They do **not** prove that the reference
screenshot's first visible ARMCK corresponds to the post-delta-zero values.
There is no retail trace pairing scenario creation and factory creation with
the exact first committed indexed frame, and the reference screenshot lacks
the scenario, tick, and allocation provenance needed to infer that boundary.
The allocator-provided initial content of COB statics also remains Unknown in
the COB Missing list below. Therefore this research does not establish whether
delta zero alone is sufficient for retail visual parity or whether authored
waiting work must advance before the relevant publication. Do not change
creation timing and do not force closed-looking piece values from appearance.

**Settling probe.** At one scenario-created and one ARMLAB-produced ARMCK,
record the selected definition/model/COB, strict piece map, `Create` start and
finish, all twelve piece transforms and draw flags (a) before `Create`, (b)
after its immediate delta-zero drain, (c) after each normal drain until the
first publication, and (d) from that immutable committed frame. Capture the
same frame's indexed ARMCK region and callback/order state, including product
`GetBuilt` and factory `StartBuilding`. Repeating with a known initialized COB
static arena would distinguish scheduling from initial-storage provenance.
That paired retail trace, not visual plausibility, decides whether a timing
change is implementable.

## 6. Terrain and movement prerequisites

### 6.1 Terrain classification

**Established fact:** Movement profiles are movement class records compiled from `CLASS` sections. Each class reads eight keys in parse order — `FootPrintX`, `FootPrintZ`, `MaxWaterDepth`, `MinWaterDepth`, `MaxSlope`, `BadSlope`, `MaxWaterSlope`, `BadWaterSlope` — where `FootPrintX/Z` default 0, depth and slope fields default to the class's prior value (preserved; the prior value is the startup template's — see [R-DOC04-A] below), and `BadSlope`/`BadWaterSlope` default to half (`>>1`) of the `MaxSlope`/`MaxWaterSlope` value just read. Three unsigned-byte clamps then run unconditionally on every class: MaxWaterSlope caps MaxSlope, the resulting MaxSlope caps BadSlope, and MaxWaterSlope caps BadWaterSlope.

**Closed — the startup pool template and the MaxSlope=0 paradox [R-DOC04-A] (2026-08-27).**
The prior reading — "the pool has no template writer: it is loader-zero-filled, the first class
omitting `MaxWaterSlope` receives 0, its `MaxSlope` clamps to 0, and the shipped file must
order a MaxWaterSlope-authoring class first so the self-carry propagates 255" — is
**superseded**. That reading was correct that the loader performs no fill and that each
record's prior bytes are its OWN (there is no cross-class propagation: each `CLASS_n` index
owns one record slot), but wrong that the prior is zero. A one-time startup initializer
registered in the executable's startup function table fills all 32 records BEFORE the first
parse, and the bounded writer census that falsified it anchored its scan on the pool base
while the initializer writes through a base-plus-displacement anchor — the census window, not
the executable, missed it.

**Established [R-DOC04-A]:** before any parse, every record holds: null name, `FootPrintX/Z`
0, `MaxWaterDepth` 10000, `MinWaterDepth` −10000, and **`MaxSlope` = `BadSlope` =
`MaxWaterSlope` = `BadWaterSlope` = 255**; the record tail (map dimensions, layer pointer,
revision watermark) is zero. The identical values back the per-unit scratch profile used when
an FBI `movementclass` name does not resolve (document 02 §5) — unauthored means unlimited.

**Consequence — Established:** an omitted key carries the TEMPLATE value, not zero. Every
stock class that omits `MaxWaterSlope` compiles to `MaxWaterSlope` 255, so the unconditional
first clamp is the identity and **compiled `MaxSlope` equals the authored value** (stock:
KBOTSS2 32, KBOTSF2 11, TANKDS2 32, TANKDH3 15, TANKSH3/TANKSH2 15, TANKBH3 15,
TANKHOVER3/4 12). The `MaxSlope = 0` paradox is dissolved: no stock class compiles to a zero
slope limit. Likewise every land class that omits `MinWaterDepth` carries −10000 (the
shallow-depth gate can never fire) and every class that omits `MaxWaterDepth` carries 10000
(boats: no depth ceiling); stock boats rely on both template depths. **Nanolathe's SC5
gated-clamp divergence (docs/SPEC_CONFLICTS.md SC5) is removable**: retail is reproduced
exactly by the startup template pre-fill plus the unconditional clamps, with no gate on key
presence. Document 02's R-CONTENT-01 consequence paragraph and SC5's decision note need the
same correction (content owners; this document's §6.1 text is the reference form).

**Closed — template-writer question (2026-08-26, superseded by [R-DOC04-A] 2026-08-27):**
that note's reading — no template writer, BSS-zero priors, stock file ordering as the escape,
gated clamps retained — is superseded by [R-DOC04-A] above. Its loader-side findings (no fill
in the MOVEINFO loader itself; per-record self-carry defaults; unconditional clamps) remain
established and are unchanged.

**Established fact:** The map terrain grid uses fixed-size attribute cells. The plot expansion derives per-cell MinHeight and MaxHeight as the minimum and maximum of up to four height bytes (cell, east, south, southeast, with edge guards) — slope is computed from these derived values, not a single sample. Height queries use bilinear interpolation of the four corner heights with low-four-bit fractions and signed-bias correction.

**Established fact:** A movement profile classifies a footprint rectangle against map bounds, blocking features, terrain height span, sea level, slope, and water-depth thresholds. The footprint validator aggregates `min of mins` and `max of maxes` across the rectangle, selects the water-vs-land slope branch by whether the footprint is entirely above water (sea level at or below the footprint minimum chooses movement class MaxSlope, otherwise MaxWaterSlope), and tests passability with strict `<` (`slope == limit` and depth == limit pass). `BadSlope` and `BadWaterSlope` do not block in the validator — they are soft cost tiers.

The classifier yields three terrain states:

- blocked;
- passable but steep/edge-conditioned;
- clear.

The path reader adds a fourth state for building occupancy. The steep and clear values are both passable to the current search expansion; the notes do not establish a separate per-edge cost for them.

**Established fact:** Water legality is folded into profile thresholds by comparing terrain against sea level. There is no independently proven water-cost table in the path expansion.

**Established fact:** A packed two-bit terrain layer is stamped per movement class, not per
unit: at map load every named class record is extended with the map dimensions, a heap layer
of `ceil(height/16) × width` dwords, and the class's classifier is run over every attribute
cell. Packing is 2 bits per cell: the dword at index `(z>>4)·width + x` holds cells
`z & ~15 .. z | 15` of column `x`, cell `z` in shift `(z & 15)·2`. Dynamic placement/removal
causes rectangle restamping and revision updates. Building occupancy is an overlay tested
separately from the packed terrain value.

**Established — the per-cell passability classifier [R-DOC04-B] (2026-08-27).** One
comparison chain produces the 2-bit value, and it is the contract both for the map-load stamp
(single-cell form) and for rectangle restamps (footprint form over the same record fields).
Per attribute cell, in order, all comparisons on the derived 2×2 heights `hmin`/`hmax` (the
per-cell derived minimum/maximum of §6.1's plot expansion):

1. Feature gate: a resolved blocking feature on the cell blocks (the feature word's blocking
   flag); a stale feature identity blocks; void cells block; no feature passes.
2. Occupant-age gate: a cell's mobile occupant whose last occupancy-commit tick predates the
   class record's revision watermark blocks. The watermark is zero until a request revision
   arms it (the request revision pass below), so the MAP-LOAD stamp never blocks on
   occupants — the static layer is terrain and features only.
3. Deep gate (signed 32-bit): blocked iff `hmin < SeaLevel − MaxWaterDepth`, both depths
   taken as sign-extended 16-bit record fields.
4. Shallow gate (signed 32-bit): blocked iff `hmax > SeaLevel − MinWaterDepth`.
5. Medium split (unsigned byte): land iff `hmin >= SeaLevel`.
6. Slope tier (unsigned byte, `slope = hmax − hmin`): `slope <= BadSlope` (land) or
   `slope <= BadWaterSlope` (water) → **3 clear**; `slope > MaxSlope` (land) or
   `slope > MaxWaterSlope` (water) → **0 blocked**; otherwise → **1 steep**. Equality with
   the bad threshold is clear; equality with the max threshold is steep, not blocked.

Values are 0 blocked, 1 steep, 3 clear; value 2 never occurs in a stamped layer. After the
per-cell stamp, a two-direction contagion pass demotes any clear (3) cell that borders a
non-clear cell to 1 — the layer marks passable cells edging an obstruction as the steep tier.

With the stock compiled classes of [R-DOC04-A] this yields the expected medium behavior:
land classes block water deeper than their authored `MaxWaterDepth` (KBOTSS2 12, TANKDS2 100)
and are never depth-blocked on land (template `MinWaterDepth` −10000 puts the shallow ceiling
above any terrain); boats carry the template `MaxWaterDepth` 10000 (no deep gate) and block
unless the whole footprint is submerged past authored `MinWaterDepth` (BOATD3/BOATD6 15,
BOATS4/5/6 3); spiders (`MaxSlope` 255) climb any slope.

**Established — path-search consumption [R-DOC04-B] (2026-08-27).** A path request binds its
unit's movement-class record by reference (through the unit's mover), so all requests of one
class share that record, its stamped layer, and its revision watermark. The search's
passability test returns: out-of-bounds → 0; the requester's bit
absent in the coarse owner/building-mask word (one 16-bit word per 2×2-cell block, bit per
player slot) → 2; otherwise the packed terrain value. EVERY consumer of the test — the A\*
expansion and all greedy-ray probes — treats the result as passable iff it is nonzero:
**only the terrain value 0 hard-blocks**; steep (1), owner-mask miss (2), and clear (3) all
expand. The owner/building-mask word therefore does not hard-block the A\*; hard building
blocking for movement happens at the movement commit validator (§8.2).

**Established — the request revision pass [R-DOC04-B] (2026-08-27).** Before expanding, the
request init revises the bound class record and its shared layer: the class record's revision
watermark is set to `max(tick, 30) − 30` (the first revision arms it; later revisions advance
it in 30-tick steps), every unit carrying the building-class/alive state bit (bit 28) whose
last occupancy-commit tick falls in the previous watermark window has its footprint rectangle
re-stamped into the layer, and the requesting unit's own commit tick is refreshed. Because
the record and layer are shared by all requests of the class, one request's revision is
observed by the next. Combined with the classifier's occupant-age gate (step 2 above), the
effect is: units that committed occupancy within the last 30 ticks do not block the layer
(their footprints are re-stamped and pass the gate), while a mobile occupant whose commit
tick predates the watermark blocks any cell it occupies that is re-stamped afterwards. This
is the dynamic-blocker channel of §7.4 ("passability is rechecked lazily") — existing heap
entries are re-tested against the revised layer on expansion.

### 6.2 Footprints and yard maps

**Established fact:** Footprint dimensions are baked into profile terrain stamps. The path search validates the unit's center cell; it does not sweep a footprint at each path edge. Placement validation separately uses the unit yard map and footprint rectangle.

**Established fact:** This explains why a path can be geometrically valid while a later placement validator rejects the exact build position. The two checks must remain separate: path search validates the unit's center cell against the pre-stamped terrain and building-mask layers, while placement separately walks the footprint rectangle with the yard map and the aggregate gates — both sides are direct, so this paragraph is upgraded from Supported inference.

### 6.3 Build footprint anchor and model center [R-P0-02]

**Established fact [R-P0-02]:** Retail keeps a building's footprint anchor —
the snapped rectangle origin in map-cell coordinates — its footprint rectangle
— the authored width/depth rectangle beginning at that origin — and its
center/model position as distinct values. World cell size is `1 << 20`
fixed-point units (16 map pixels) and half a cell is `1 << 19`.

For a picked world coordinate `p` and footprint extent `f` (width or depth),
the snapped anchor cell is:

```text
anchorCell = (p - (f << 19) + (1 << 19)) >> 20
```

with a signed arithmetic shift; the half-cell term implements round-to-nearest
at the footprint's half extent. The footprint rectangle is
`[anchorCell, anchorCell + f)` in each axis, and placement and occupancy
validation use this rectangle, not the visual model center. The mobile
build-order path then writes the order's world position as:

```text
modelWorld = (f + 2*anchorCell) * (1 << 19)
```

which is exactly `anchorWorld + f*cellSize/2`: the unfinished mobile-built
unit is centered on the footprint while its occupancy rectangle remains
anchored at the snapped origin. The same formula applies to every extent —
there is no separate odd/even compatibility branch. The half-extent term
produces the visible bias: extent 1 centers at anchor + 0.5 cells, extent 2 at
anchor + 1.0, extent 3 at anchor + 1.5, extent 4 at anchor + 2.0. For odd
extents the picked coordinate behaves like the ordinary cell choice after the
half-cell terms cancel; for even extents the footprint straddles a cell
boundary and the nearest valid rectangle can move when the pick crosses the
corresponding half-cell boundary. This is a property of the formula, not a
post-placement visual adjustment. The ghost rectangle is generated from the
anchor and extent — left/top edges at `anchor * 16`, right/bottom at
`(anchor + extent) * 16`, with the camera and viewport offsets applied — so it
must never be reconstructed from a model sprite's apparent center.

**Established fact — factory product placement [R-P0-02]:** The factory
production path has two coordinate products: QueryBuildInfo resolves a piece
transform plus the factory origin and stores that world position on the
production order; the validator separately derives an anchor with the same
half-extent formula and validates the footprint rectangle at that anchor. On
successful validation the allocator creates the product at the original stored
QueryBuildInfo position, NOT at the derived snapped anchor: the factory
product's model/unit center is the resolved exit transform, while the
occupancy/legal-placement rectangle is the separately computed snapped
rectangle. An implementation must preserve both values — it must not
substitute the rectangle origin, nor assume every authored exit transform is
exactly the geometric center. The blocked-exit retry (exactly 15 ticks,
silent) and allocator-failure retry (exactly 300 ticks) do not change this
ownership (sections 3.8, 4.7).

The stock callback piece indices, names, and authored local translations are
now recorded in [R-FAC-01B].

**Correction — the runtime transform is no longer open [R-REV-02].** This
subsection previously carried a `TODO(question)` asking "whether the runtime's
rotated heading transform applies those hierarchy translations with any
additional factory-specific arithmetic". There is no factory-specific
arithmetic and no separate heading matrix: the shared piece locator itself
folds the unit's committed orientation into the model root node before
rotating. The complete algorithm is stated in [R-REV-02] below. The allocator
still preserves the resolved transform, and the separately snapped footprint
anchor remains validation-only.

### 6.3.1 OTA-REV-02 exit-piece locator transform [R-REV-02] (2026-08-28)

This closes the runtime transform that section 6.3, [R-FAC-01B §5] and
[05 "Rotated factories — split Established/Unknown"] recorded as **Unknown**.
It is an independent static re-derivation of the synchronous piece-locator
chain that the factory state-2 handler calls with the `QueryBuildInfo` piece
index. The same locator serves the other synchronous piece queries, so the
arithmetic below is not factory-specific.

**Locator algorithm — Established (direct-static).**

```text
locate(unit, pieceIndex) -> offset from the unit origin
    if unit is absent, the unit has no loaded model,
       pieceIndex < 0, or pieceIndex >= model piece count:
        return (0, 0, 0)

    node = piece[pieceIndex]
    v    = translation(node) + translation(node.loadedPiece)

    for p = parent(node); p != null; p = parent(p):
        a = angles(p)                      # three signed 16-bit turn values
        if parent(p) == null:              # p is the model root
            a = a + unitOrientation        # componentwise 16-bit wrapping add
        v = rotate(v, a)
        v = v + translation(p) + translation(p.loadedPiece)

    return (v.x, v.y, -v.z)
```

The factory helper then adds the factory unit's committed world X/Y/Z to that
offset componentwise. The loop starts at the *parent* of the selected piece,
so the selected piece's own angles never rotate its own translation.

`rotate(v, a)` applies three two-coordinate rotations in this chronological
order, each writing both coordinates of its pair back before the next runs:

1. `a[0]` on the `(x, y)` pair — rotation about Z
2. `a[2]` on the `(y, z)` pair — rotation about X
3. `a[1]` on the `(x, z)` pair — rotation about Y

The subscripts are **element indices into the three-value angle triple**, not
byte offsets into a record. Each two-coordinate rotation is

```text
theta = a[i] * 2*pi / 65536
p' = round(p*cos(theta) - q*sin(theta))
q' = round(p*sin(theta) + q*cos(theta))
```

computed in x87 extended precision from a stored double constant whose value
is exactly `2*pi / 65536`. `round` is an x87 store-to-integer under the
retail default control word — **round-to-nearest**, not the truncating
`__ftol` conversion used for ordinary integer casts ([01 §8]). Rounding is
applied once per axis pass, so the value entering each of the three rotations
is already an integral 16.16 quantity. A zero angle short-circuits: the pair
is left exactly unchanged rather than run through sine and cosine.

**Factory heading — Established, and an earlier reading corrected.** Section
6.3 previously said "No independent factory-heading, yard-map, model-extent,
or fixed-cell offset is read by the factory handler. Rotation therefore
enters through the authored piece transform and its hierarchy", and
[R-FAC-01B §5] recorded "the exact runtime arithmetic that applies a rotated
factory heading to those hierarchy translations" as **Unknown**. The premise
was right and the conclusion was wrong. No separate heading term is applied
*by the factory handler* — but the locator it calls folds the unit's three
committed orientation words into the model root node's angles before
rotating. A rotated factory therefore does rotate its exit point, through the
arithmetic above; no heading matrix is missing.

Because the fold happens only at the root node and the walk begins at the
selected piece's parent, a unit whose selected exit piece **is** the model
root never enters the loop and never picks up the unit orientation: its exit
offset is that piece's translation, unrotated. Among the eight stock rows
censused in [R-FAC-01B] this applies to CORSY alone (selected piece `/base`);
the seven rows selecting `/base/pad` or `/base/slip` have the root `base` as
their parent and do rotate with the factory. Whether any non-stock content
selects a root piece here is not surveyed.

**Sign convention — Established.** The locator negates accumulated Z exactly
once, as its output conversion, and passes X and Y through unchanged.
Combined with the load-time half-turn that negates X and Z of every model
vertex and every parent translation ([03 §2.4]), an authored translation
`(x, y, z)` on a root-parented piece reaches the caller as `(-x, y, z)` plus
the unit origin — the same net signs [03 §2.4] records for its muzzle-flare
example. This statement is specific to this locator's data flow and must not
be carried to the renderer's vertex projection or to the hover-pick
projection, which have their own conventions ([03 §2.4–§2.5],
[07 R-REV-01 §3]).

**Per-node translation sum — Established, with one Supported inference.**
Every node in the walk contributes **two** translations, not one: the
translation stored on the runtime node itself and the translation of the
loaded model piece that node references. The same pairing applies to the
selected piece before the walk begins. That the node-side translation and the
node-side angle triple are the runtime piece offset and rotation written by
the COB `move`/`turn` opcodes (section 5) is a **Supported inference** — it
explains why a muzzle query follows an animated turret, and why the unit
orientation is added into that same triple at the root — but the writer trace
from those opcodes into these fields was not re-derived here; that trace is
the decider.

**Still Unknown.** Nothing above establishes a post-completion release
target, a producer/product collision exemption, an aircraft
takeoff-before-rally ordering, or a same-pass no-stacking guarantee; those
remain open exactly as stated in [R-FAC-01] and [R-FAC-01B]. Whether a stock
rotated producer's exit transform happens to coincide with its footprint's
geometric center is also still unestablished, and is now a question about the
authored models rather than about the runtime arithmetic.

### 6.4 Placement footprint legality [R-P0-08]

**Established fact — class split [R-P0-08]:** Placement is not one universal
"is this cell clear?" predicate. The shared entry first checks that the packed
footprint rectangle is within the map, then dispatches by the produced
definition's class: a building-class definition uses its compiled yard-map
bytes plus the aggregate slope/height/water/geothermal validator; a mobile
definition uses the inline footprint terrain loop, testing feature blocking,
unit occupancy, and water/depth/height/slope only when the caller's
terrain-check mode requests them (recovered mode value 1; other modes add no
second terrain-legality rule after the bounds check). The class branch belongs
to the product being placed, not to whether the builder is a factory — stock
factories can have CanMove set.

The factory exit path is: QueryBuildInfo (seed cell −1, synchronous) → resolve
the exit piece transform to a world position → convert to a packed footprint
anchor with the half-extent bias of section 6.3 → map-bounds check →
class-specific footprint validation → allocate the product only on success. A
blocked factory exit schedules an exact 15-tick retry before allocation,
emitting no product, sound, or placement event.

**Supported inference — exit spots overlap the producing factory's own body,
so finished buildings cannot live on the unit-stomp occupancy shorts
[R-P0-08-A §1] (2026-08-27).** Stock factory COBs author their build-info door
piece inside their own footprint, so the snapped exit rectangle always
intersects cells the factory itself covers — yet stock factories produce. Two
consequences follow for the bits-1–2 occupant test above: the occupants it
reads are the plot's mobile stomp/unstomp shorts ([02 "Terrain file"]), which
finished buildings never write; and building blocking is a separate layer
(the building-mask layer named in §6.2). An earlier Nanolathe implementation
kept a completed product's construction reservation stamped on those same
shorts, and its own factory then failed exit validation in the silent 15-tick
blocked-revalidation loop forever — every first product of every stock lab.
The retention was corrected forward: frame reservations release at completion
and completed buildings register in a structures registry that placement
consults instead. Self identity passed to the validator exempts only the
producing factory or walking builder; foreign stamps still block silently.

`TODO(question)`: the exit caller's terrain-check mode value is unresolved —
the inline aggregate gates (slope/height/water, recovered mode value 1)
need not run at exits at all. Nanolathe currently runs factory exit-spot
validation outside those aggregates (`PlacementQuery.SkipTerrainAggregates`)
while mobile builder site builds keep them; a targeted executable trace of
the mode argument at the production state machine's validation call would
settle it.

**Established fact — yard control bytes [R-P0-08]:** The compiled yard-map
characters are:

| Character | Byte | Character | Byte |
|---|---|---:|---|---:|
| `.` | 0x00 | `c` | 0x2d |
| `C` | 0x35 | `f` | 0x6f |
| `G` | 0x8f | `o` | 0x2f |
| `O` | 0x2b | `w` | 0x37 |
| `Y` | 0x31 | `y` | 0x29 |

The parser is not a one-character-to-one-cell contract: characters outside the
table advance the source string without consuming a footprint cell (spaces in
stock maps therefore disappear); when the source reaches its final character
the pointer parks there and that character repeats for remaining cells;
characters beyond the final footprint cell are ignored. An implementation must
preserve these rules rather than reject a yard map whose authored length
differs from the packed extents.

Each covered cell's yard byte gates independent tests:

| Yard bit | Gate | Meaning |
|---:|---|---|
| 0 | enemy-visibility/occupancy branch | tests the authoritative visibility/occupancy predicate only when the mode and bit branch request it; mode 0 applies the occupancy rejections unconditionally, a nonzero mode gates them on the LOCAL player's team bit in the cell's LOS word (or, under the footprint-overlay global, the overlay char at the cell) — the alias and mode matrix are closed in section 4.7 |
| 1–2 | unit occupancy | a nonzero occupant other than the passed self identity rejects |
| 3 | slope aggregate participation | contributes the cell's low and high terrain heights to the footprint aggregate |
| 4 | height aggregate participation | contributes the high terrain height to the separate height maximum |
| 5 | blocking-feature-free | rejects a resolved feature only when its authored blocking flag is set |
| 6 | indestructible-feature test | rejects a resolved feature carrying the authored indestructible flag |
| 7 | geothermal requirement | requires at least one qualifying geothermal feature somewhere in the footprint |

Bit 5 is not "any feature is blocking": metal deposits are authored with
blocking clear and are legal beneath an extractor's `o` cells, while trees and
rocks with blocking set remain blockers. Bit 6 reads the feature definition's
indestructible flag (catalog flag-word mask 0x0200), not its reclaimable flag.
These flags are resolved through the feature cell's live identity, including
the signed fringe-anchor hop: a fringe cell whose hop finds no live feature is
not blocking; an out-of-range feature identity blocks for bit 5 and satisfies
neither bit 6 nor bit 7. The bit-0 branch is an authoritative
visibility/occupancy check, not a minimap or fog-presentation lookup — the
placement predicate reuses the gameplay predicate family selected by its mode,
with the local player's team bit as the alias (closed in section 4.7); do not
equate it with the presentation fog surface.

**Established fact — footprint aggregates and strict comparisons [R-P0-08]:**
For yard bytes that request terrain sampling the validator maintains
`minLow = 255`, `maxHigh = 0`, `bit4Max = 0`. Bit 3 updates minLow from the
cell's low terrain height and maxHigh from its high terrain height; bit 4
updates bit4Max from the high height. The aggregate is consumed once, after
the full footprint walk:

```text
if geothermalRequired && !geothermalFound:      reject
if maxHigh < minLow:                            // no bit-3 sample
    siteHeight = SeaLevel - waterline
else:
    if maxHigh - minLow > MaxSlope:             reject
    siteHeight = minLow
if bit4Max > siteHeight:                        reject
if minLow < SeaLevel - MaxWaterDepth:           reject
if max(maxHigh, bit4Max) > SeaLevel - MinWaterDepth: reject
publish siteHeight; accept
```

All rejections are strict: a slope exactly equal to MaxSlope, a low height
exactly equal to `SeaLevel - MaxWaterDepth`, or a high height exactly equal to
`SeaLevel - MinWaterDepth` passes. The no-bit-3 branch uses the definition's
waterline against sea level rather than inventing a terrain sample. The
published site height is consumed by the build ghost and the resulting order —
it is not a renderer-only value.

The geothermal condition is an existence test, not a count or registry: any
covered yard byte with bit 7 requires any covered resolved feature whose
catalog geothermal flag is set; the first qualifying feature satisfies it. The
effect helper associated with geothermal visuals is not a registry and must
not be used as a placement side channel.

**Established fact — mobile terrain validator [R-P0-08]:** Mobile products do
not use a building yard map. After the bounds and mode gates, the inline
footprint loop checks the covered cells for feature blocking and unit
occupancy, then aggregates the movement-profile terrain values; the profile
derives the footprint minimum and maximum from the plot cell's four-corner
heights, not one arbitrary corner (section 6.1). Water/slope selection is
based on the entire footprint: if `SeaLevel <= footprintMin` the slope limit
is MaxSlope (entirely above water), otherwise MaxWaterSlope. Rejection is a
strict greater-than test (acceptance is at-or-below): slope equal to the limit
and depth equal to the limit are legal, consistent with section 6.1's equality
boundary. BadSlope and BadWaterSlope are soft cost tiers and never reject
placement. The inline mobile path is active only for terrain-check mode 1;
factory production supplies its state/mode pair and a null self identity, so a
foreign occupant cannot be excused by self-identity. The exact
out-of-map/mode behavior outside the production path remains `TODO(question)`.

**Established fact — order and side effects [R-P0-08]:** The behaviorally
important order is: compute the packed footprint anchor → reject a rectangle
outside the map → walk the cells in stable row-major order (occupancy/
visibility bits; feature resolution and yard bits 5/6/7; bit-3/bit-4 aggregate
updates) → geothermal existence gate → slope/height/water aggregate gates →
publish siteHeight and accept. The validator reads terrain, feature, and
occupancy state; it does not clear a vent, reserve a unit slot, alter metal,
consume RNG, or publish a nanolathe event. Factory allocation and construction
work occur only after success; there is no pathfinding and no random
relocation fallback inside the validator.

## 7. Ground path search

### 7.1 Grid and passability

**Established fact:** Ground path search uses the TNT attribute-cell lattice,
with one cell covering 16 map pixels. Waypoints are generated from cell
coordinates plus the movement profile's half-footprint bias.

**Established fact:** The eight neighbor directions are visited in this order: north, northwest, west, southwest, south, southeast, east, northeast. The first expansion is deliberately wide: it attempts nine entries, which covers all eight directions plus one harmless duplicate. Subsequent expansions use a directed five-entry fan centered on the parent travel direction.

**Closed — fan geometry (2026-08-26):** the expansion loop is
`for u in -k..k: expand(cell, u & 7)` with the fan half-width k at the
request's fan field, seeded to 4 at request setup and rewritten to 2 after
the first fan. The first expansion therefore tries the directions
`(c-4, c-3, ..., c+4)` mod 8 — the duplicated direction is the direction four
steps from the fan centre (the reverse of travel, 180° back), attempted FIRST
and LAST; later expansions are five unique directions `(c-2..c+2)`. The fan
centre c is the current record's stored direction; for the START record the
request setup derives it from the unit's current heading quantized to eight
sectors (`(heading + 0x1000) >> 13 & 7`), NOT north as a file hypothesis once
suggested. Heap tie-breaks therefore depend on: the reverse direction being
expanded twice (first and last) in the first fan, the strict-less heap
comparison, and the equal-g no-reparent rule of section 7.2.

**Established fact:** Diagonal movement is endpoint-only: only the destination cell's stamped terrain and building mask are tested, not both cardinal corner cells. A footprint is not swept during expansion because the profile stamp already marked cells that would intersect static blockers. The test's exact encoding and its only-blocking value are in [R-DOC04-B]: only the stamped terrain value 0 blocks; the owner/building-mask miss value is traversable to the expansion.

**Closed — duplicate suppression ordering (2026-08-26):** a neighbour that is
already open is re-visited, the new cost is computed FIRST, and the parent/
direction are updated only when the new cost is STRICTLY lower; equal-cost
re-visits never re-parent (matching the heap's strict-less ordering). There
is no suppression before cost computation.

**Closed — greedy-ray meet rule (2026-08-26):** the ray is FORWARD-ONLY from
the start toward the goal — there is no reverse search and no bidirectional
meet. It terminates when it reaches a goal-flagged cell (return 0), when its
scaled-heuristic budget reaches 0 (return 0), or when a full wall-follow
sweep returns to the starting cell with the same direction (return the best
scaled h seen, which seeds the A* threshold); the returned value is the
minimum scaled h along the ray. Section 7.2's "bidirectional" wording is
superseded.

**Closed — debt-array layout (2026-08-26):** the per-player budget state is
two 10-entry globals indexed by player slot — cumulative step counters and
per-player budgets, the latter recomputed every 150 ticks from the counters
as `counter / divisor` tiered to `requestBase × {6, 3, 1}` — plus a
per-request 10-word accumulator array that receives `totalSteps /
playerCount` per tick and whose SUM is the scheduler's per-call step budget.
There is no per-player-direction debt. The heuristic-scale settings string
value remains a data question (section 7.2, `TODO(question)`).

### 7.2 A* state and costs

**Established fact:** The search maintains open/closed status, parent direction, a heap, and a packed coordinate per node. The heap key is `f = g + h`. Equal keys preserve insertion order because the heap compares strictly less, not less-or-equal. Equal `g` values do not replace an existing parent.

**Established fact:** Cardinal steps cost 16 and diagonal steps cost 22. A direction-change table adds turn penalties of 0, 40, 60, 80, 100, 80, 60, and 40 indexed by the raw fan offset — equivalently `(candidate − current) & 7` — so the table IS a turn-difference table (label now Established, not inference). A fixed penalty of 30 is added to EVERY neighbour in the live main-loop expansion path (the only code shape where the term is absent has no callers in the bounded census), and the short-run penalty of 75 applies when the candidate direction is not straight and the parent chain's straight-run length is below five; the run counter resets to 1 on any turn and increments on straight steps. The earlier reading "the fixed initial penalty of 30 applies while the heap holds at most one entry (the start expansion)" is superseded: the watched register is a zero constant at the call site, so the term is unconditional on the live path.

**Supported inference:** The eight-entry table is a turn-difference table, not a terrain-state table. The symmetric values and the absence of a terrain branch in the expansion support that interpretation. *(This label is now Established — the table is indexed by the raw fan offset, i.e. the direction difference; the sentence is kept for continuity.)*

**Established fact:** The search is a weighted A\*. Every heuristic value
passes through one scaling pipeline: `hScaled = (h · scale) >> 16`, with the
product formed as a full signed 64-bit multiplication and arithmetically
shifted; there is no floating point anywhere in the search. The scale is the
SAME per-player quantum that sizes scheduler slices, recomputed every
150-tick replenish from request pressure as `base × {6, 3, 1}` for pressure
tiers below 1, below 2, and at or above 2 respectively (tier = pending
per-player pressure divided by a unit-cap divisor). The base is
`(int)(atof(settingsString) · 65536.0)`, taken once from one settings string
at settings-application time, so the effective h weight relative to g is the
parsed value times {6, 3, 1}. Scaling never changes which cells read as
"close" (an h of 0 stays 0); it only re-ranks. h is evaluated exactly once
per allocated node and is NOT recomputed on relaxation — relaxation adjusts
`f` by the g delta alone. Because the same quantum sizes work slices and
weights the heuristic, a busier player both gets fewer pops and searches more
greedily.

**Established fact:** Goal objects form a four-family virtual family, each
supplying a start predicate, a cell enumerator, and the heuristic:

- **Point/radius goals** use an inflated octile to the center cell,
  `h = 18·max(|dx|,|dz|) + 7·min(|dx|,|dz|)`, clamped to zero within an
  authored radius R (`h = max(oct − R, 0)`); arrival is a squared-distance
  test against a `>>4`-quantized radius.
- **Annulus (stand-off) goals** use the same inflated octile with a V-shaped
  zero band: h is zero inside `[inner, outer]`, rises as `oct − outer`
  outward, and as `inner − oct` toward the center — cells closer than `inner`
  are penalized, matching an arrival band that also rejects too-close cells.
- **Rectangle-perimeter goals** are exact outside the rectangle — the
  admissible octile `16·max(dx,dz) + 6·min(dx,dz)` to the rectangle — and
  inside measure `16 · min(distance to each edge)` back out; enumerated goal
  cells are exactly the rectangle border, where h is 0, and arrival requires
  lying on that border.
- **Base/restored-from-save goals** have an identically-zero heuristic and a
  null start predicate, giving pure-g (Dijkstra) behavior whose acceptance is
  decided solely by the enumerated cells.

**Supported inference:** The {18, 7} form exceeds the true minimal geometric
cost `16·max + 6·min` by roughly 12.5–13.6 percent depending on run shape —
an intentionally inadmissible weighted octile that speeds the search and
biases straighter paths, partially masked because real paths also pay the
turn/first-step penalties. The point/annulus/rectangle semantic labels are
inferred from geometry and constructor argument shapes; every formula and
constant is direct.

**Established fact:** Arrival tolerance uses a write-once threshold. Before
seeding, a greedy FORWARD-ONLY ray walk stores the minimum scaled h seen
along its frontier into a single slot that is never updated again. During
expansion, an opened neighbor whose scaled h is at or below that threshold
receives open-plus-goal status, and popping such a node terminates the search
and reconstructs — so EVERY opened cell within the tolerance region is an
acceptable route endpoint, not just enumerated goal cells. Enumerated goal
cells are additionally marked directly; enumeration is bounds-checked and
tracks the cell nearest the start by squared distance for the ray-check
direction choice. (The ray is forward-only; section 7.1's closure note
supersedes the earlier "bidirectional" wording.)

**Established fact:** Early exits, in order after goal enumeration: a nonzero
start-satisfies-goal predicate publishes an empty route with completion
status `0x100` ("already satisfied") and stops; an out-of-bounds start cell
notifies status `0x200` and publishes empty; a ray that CONNECTS start to a
goal notifies `0x100` but the search is still seeded and runs; and when the
ray's best scaled h is at or above the start cell's own scaled h, the engine
notifies `0x200` and publishes empty WITHOUT seeding — the A\* starts only
when the ray proved a strictly closer frontier exists. These statuses are
path-request results reported to the requester — they are never interpreted as
queue-order completion [R-P0-01] (section 8.3).

### 7.3 Scheduler budget, publication, and route storage

**Established fact:** Path work is budgeted. A global scheduler counter replenishes every 150 ticks. Per-player quanta use six-times, three-times, and one-times weighting based on scheduler state. Each active request is limited to 100 heap pops per scheduler call.

**Established fact:** Requests are full-or-empty. A route is published only after a goal is reached and reconstructed. Budget exhaustion leaves the heap and request active for later ticks; it does not publish the best partial prefix. Heap exhaustion publishes an empty route.

**Established fact:** Search allocation initializes the request, clears visitation state, enumerates goals, picks the nearest goal for heuristic setup, validates the start, and can perform a direct ray shortcut. Invalid starts and unreachable goals report failure through order-layer status and receive an empty route. Request init also runs the class-layer revision pass of [R-DOC04-B] (§6.1) before any expansion.

**Established fact:** Route reconstruction walks predecessor directions
backward from the goal cell, storing the packed cell into a 64-entry ring at
`index & 63` each time the direction CHANGES (wraparound overwrites the
oldest), then appends the start cell. Emission walks masked indices downward —
newest first — converts each cell to signed world coordinates using the
request's half-footprint bias, and publishes `min(directionChanges + 1, 64)`
points; more than 63 direction changes therefore survive as the most recent
63 change-points plus the start cell.

**Established fact:** The common publisher clamps any count above 20 to 20
before anything else. A nonempty publication writes the count, copies the
packed 4-byte X/Z points, sets the active bit, and sets the dirty bit. A ZERO
publication — invalid start, init failure, immediately rejected request, or
heap exhaustion — clears the active bit and sets the dirty bit but writes
NEITHER the count NOR the point array: backing bytes of a previous route stay
physically present, merely inactive. Queries report the active bit only, so
an implementation may invalidate without eagerly zeroing.

**Established fact:** Waypoint pruning: when the stored count exceeds one,
the mover compares its signed integer position against stored point index 1;
if `dx² + dz² <= 25` the later points shift down one slot, the count
decrements, the active bit clears when fewer than two points remain, and the
dirty bit sets. The comparison runs in the whole-world-unit domain: the
mover's signed integer position (the high words of its 16.16 position)
against the signed 16-bit stored route points. The threshold is therefore 25
square world units — a radius of 5 whole units, i.e. 5/16 of a map cell — not
five cells and not fixed-point pixels; do not convert it to `5*16*65536`
without separate evidence. Pruning is a route-consumption operation: it
decides when the next waypoint can be discarded, never when the owning order
completes [R-P0-01] (section 8.3).

**Established fact:** Save representation: an inactive route serializes a
2-bit count of zero; an active route serializes `min(count, 3)` in two bits
followed by exactly that many signed 16-bit X/Z pairs read from the point
array upward.

**Established fact:** The route export helper is NOT an active-route
predicate: for each requested index below the stored count it reads that
point, and for excess indices it REPEATS THE LAST STORED POINT; it performs
no active-bit check, and a zero count selects index −1, reading adjacent
non-point fields rather than yielding an empty result. Callers must gate on
the active bit themselves.

### 7.4 Goals, build sites, and revalidation

**Established fact:** Goal objects enumerate one or more goal cells and supply
start/neighbor heuristic functions. The four families of section 7.2
enumerate, respectively: the single packed center cell; a single cell biased
along z by `(inner + outer) / 32` toward the far side of the stand-off ring
(bias intent inferred, arithmetic direct); exactly the rectangle border; and,
for restored-from-save goals, an internally stored list. A radius-unit
mismatch is real and established as a dual-unit contract: the annulus h
clamps compare RAW authored radii while its arrival predicate uses
`>>4`-quantized squared radii — two unit systems coexist in one family and
must be reproduced as-is, not "fixed" [P0-13 A19].

**Established fact:** Build-site generation enumerates perimeter candidates around a footprint, filters by range and placement validation, sorts a bounded list of candidates, and passes a selected point goal into path search.

**Mobile-build walk target [R-P0-19]:** Nanolathe drives the mobile-build
walk with a rectangle-perimeter goal around the product footprint expanded
outward by the builder's footprint half-extents, so a builder resting its
centre on the expanded border clears the product footprint. The builder stops
when its nano piece is within nanolathe reach of the footprint's nearest edge
and the order reports its approach complete, so the mover no longer keeps
steering at the build-site anchor. The exact retail perimeter-candidate
ranking and expansion remain `TODO(question)`; the half-extent expansion is the
placeholder that reproduces the established stop-outside-the-footprint
outcome.

**Established fact:** Dynamic blockers update a profile revision. Existing heap entries are not eagerly purged; passability is rechecked lazily when a node is expanded. This can turn a previously open node into a blocked one without rebuilding the whole heap.

### 7.5 Smoothing

**Established fact:** Route reconstruction removes collinear points and then performs bidirectional ray checks. Each intermediate cell must pass the same passability test. A shortcut is accepted for legality; the smoother does not compare the shortcut's integrated cost against the original route.

## 8. Ground steering and occupancy

### 8.1 Desired heading and speed

**Established fact:** The mover selects a waypoint, computes a desired heading, wraps the heading on a 16-bit circle, and clamps heading change by the definition's turn rate. The pending heading and dirty movement flag are updated before integration.

**Established fact:** Acceleration versus braking is selected from the angle-to-waypoint and current speed. Speed is clamped to the definition's maximum. There is no normal reverse-speed branch.

**Established fact:** Ground speed is capped by an exact pitch table and by a
below-water half-speed branch before integration. The signed pitch is
arithmetic-shifted right by 11 and clamped to `[-5,+5]`; that index selects a
signed byte from the table `25, 55, 70, 85, 100, 100, 75, 50, 25, 20, 15` in index
order `-5` through `+5`. The pitch cap is `table[index] * MaxVelocity / 100`
with signed truncation toward zero, so level pitch permits 100 percent and the
two sides are asymmetric. If the unit's signed integer height (`(Y>>16)` as a
signed 16-bit value, i.e. the signed high word of its 16.16 Y) is below the
sea-level byte and `def.flags & 0x81000 == 0` — neither `canhover` (`0x1000`)
nor `floater` (`0x80000`) is set — the cap is halved. The final integrator then
applies terrain height, gravity and lean, and the fixed-point position commit.

**Established fact:** Waypoint lookahead, vertical tolerance, and arrival tolerance are fixed-point thresholds. A blocked mover reduces its next-step cap. Waypoint consumption and path publication are synchronous with the mover tick.

### 8.2 Static and mobile collision

**Established fact:** Static collision uses an axis-aligned footprint rectangle. Path search and movement commit use the same footprint/profile family but at different points: path search uses pre-stamped static cells, while movement commit checks the current rectangle.

**Established fact:** Mobile occupancy is committed synchronously in sweep order. A unit that claims a cell first can prevent a later unit from entering. A vacated cell can be reused earlier in the same sweep. Head-on swaps block; no special simultaneous swap resolution was found. The sweep finishes one unit's clear/commit/stamp sequence before advancing to the next slot, so a later unit immediately observes earlier same-tick occupancy mutations.

**Established fact:** Each mover tick proposes a new X/Z by adding velocity to position and quantizes the proposed footprint anchor with signed arithmetic and the instance's packed half-cell bias. If the resulting cell pair and proposed mover mode equal the committed cached pair and mode, the engine takes a same-cell fast path: it commits the proposed transform and dirty state without calling the footprint validator or restamping occupancy. For a cross-cell or mode-changing proposal in an active local simulation the engine calls the footprint validator once and rewrites the mover blocked flag with its result.

**Established fact:** The footprint validator scans the proposed rectangle row-major — Z outer, X inner — and returns immediately on the first rejecting per-cell predicate; aggregate height, depth, and slope gates run after the scan. A blocked result does not try X-only or Z-only movement and does not move another unit: it caps scalar speed at movement definition MaxVelocity/2 if higher, recomputes horizontal velocity at that capped speed and current heading via the fixed sine/cosine table with rounding, clamps X and Z against the old footprint's centre within half-cell minus one (`±524287`, half-cell `524288` minus one) and commits the clamped self position, marking transform dirty without clearing or restamping occupancy.

**Established fact:** A successful result clears the old footprint, commits X/Y/Z, packed anchor, and low mode bits, stamps the new footprint, marks transform dirty, and calls the coverage wrapper that updates visibility for the local player. The clear-then-stamp sequence finishes before the next unit slot is visited, so a vacated cell is reusable in the same tick — a pipeline of one cell per tick — and head-on swaps where each proposes the other's cell both block, with no reservation or simultaneous-swap resolution. There is no second validator call for axis sliding and no collision-candidate list or candidate cap.

**Established fact (negative-bounded):** No mass-weighted pushing, impulse-based movement resolver, axis-slide resolver, automatic repath timeout, or yielding/wait-queue was recovered within the bounded mover call graph. Absence is contract: a blocked mover only receives the half-speed clamp and dirty clamp above; it does not push, slide, or wait.

**Correction — mobile occupancy and path search [R-DOC04-D] (2026-08-27).** The earlier
sentence "mobile occupancy is never part of path search, directly or via a revision" is
**superseded**; its bounded claim was scanned over the request setup, scheduler, and
expansion bodies alone and missed that the request-setup's init calls the request revision
pass (documented in [R-DOC04-B]), which walks the unit pool and re-stamps recently-committed
footprints into the class layer. The corrected contract: the SCHEDULER and EXPANSION
functions contain no mobile-pool reference (that bounded observation stands for those two
bodies); the mobile channel into search is the request-init revision plus the classifier's
occupant-age gate — units that committed within the last 30 ticks do not block the layer,
while older occupants block cells they occupy that are re-stamped afterwards. The rest of
this paragraph (commit validator reads the cell's mobile-occupancy count; mobile units are
hard blockers at the movement commit stage) is unchanged.

### OTA-MOV-02A dynamic-blocker boundary [R-MOV-02A] (2026-08-28)

This targeted pass audits the exact collision and replan boundary requested by
Phase C. It corrects no positive behavior in [R-DOC04-D]; it makes the
negative evidence and the separation between path service and final movement
commit explicit. All negative findings below are bounded to the recovered
movement sweep, mover fan-in, ground commit, footprint validator, path-request
initializer/scheduler/expansion, and ground-order completion call chains.

**Final commit — Established.** A cross-cell ground proposal is validated
against the current destination footprint. Any nonzero mobile occupant other
than the requester rejects the proposal; the bounded predicate has no branch
for stationary versus moving occupants, owner allegiance, order priority,
heading, velocity, or projected destination. Thus a stationary friendly, a
moving friendly, and an enemy are all hard blockers at this commit boundary
when the same occupancy predicate applies. A same-cell proposal takes the
fast path and does not revalidate occupancy.

**Sweep ownership and encounter outcomes — Established.** The live sweep is
deterministic by player slot and then unit pool slot. A successful unit clears
its old footprint and stamps its new one before the next unit is visited, so a
later unit can reuse a cell vacated by an earlier unit. A head-on swap where
each unit proposes the other's currently occupied cell leaves both blocked:
neither can clear first. For a same-destination group, the first unit that
successfully commits claims the cell and a later unit sees that occupancy in
the same sweep. Perpendicular crossing uses the same sequential rule. No
mass, unit-priority, order-priority, or allegiance-based yield owner was
recovered.

**Blocked response and retries — Established within the commit chain.** A
rejected proposal does not push, reverse, sidestep, rotate for clearance,
submit a new path request, or enter a wait queue. It leaves occupancy
unchanged, caps speed at half the movement definition's maximum, recomputes
velocity along the current heading, and clamps the requester's position inside
its old footprint. No blocked-tick counter, collision retry delay, or random
draw is present; the mover encounters the same commit boundary on its next
ordinary tick. The path service's 100-pop budget and later-tick resume are not
collision retries. The ground order remains active until its ordinary goal
satisfied-bit handshake; a blocked mover does not complete merely by stopping.
The separate last-record route-release rearm wait (`30 + RNG(30)`) belongs to
order/path status handling, not to a mobile collision.

**Search versus commit — Established, with a bounded distinction.** A path
request's initializer revises the shared movement-class layer and re-stamps
recently committed mobile footprints. The occupant-age gate can make an older
occupant block a re-stamped search cell while a recent occupant remains
nonblocking there; existing heap entries are rechecked lazily when expanded.
The scheduler and A* expansion themselves read no mobile-unit identity or
velocity and do not project a blocker's destination. The blanket SC22 wording
that search “checks only terrain/features” and that “mobile occupancy is
ignored at search time” is overbroad for the retail contract: it describes a
Nanolathe implementation decision and omits the request-initialization
revision channel established in [R-DOC04-B]. The corrected rule is that
mobile occupancy is not a permanent map-load A* wall, but a request may
temporarily observe re-stamped older occupants through the age gate and lazy
per-node recheck. The final occupancy validator remains authoritative and may
reject a route that was legal during search. A path no-route status and its
order-layer response are distinct from a successful route later blocked at
commit.

**Yield/replan/priority — Unknown outside the bound.** No outer caller in the
reviewed set adds a dynamic yield, retarget, sidestep, reverse, or automatic
replan policy. This is a bounded negative, not proof that an unrecovered UI,
network, or other movement caller cannot do so. Closing that residual requires
a retail trace recording both units' slots/owners, proposed and committed
cells, headings, speeds, route revisions, and order status through stationary,
moving, head-on, crossing, and same-destination encounters.

### 8.3 Final-order arrival and the satisfied-bit handshake [R-P0-01]

**Established fact [R-P0-01]:** Retail keeps three notions that must not be
collapsed into one distance test: path-search termination and publication
(section 7); consumption of an already published route (section 7.3); and
completion of the order that owns the route. Route pruning and the arrival
predicate below are both computed in the same mover visit, but pruning never
writes the order's satisfied word: consuming the whole route does NOT complete
the order — only the tile-versus-goal-cell test does — and conversely the
order completes while route points remain if the mover's committed tile lands
within threshold of the goal cell.

**Established fact — the arrival predicate:** The ground mover's arrival test
is a planar cell-domain comparison between the unit's committed tile (the
cached cell written by every occupancy commit) and the goal cell packed in the
movement-goal handle:

```text
dx = tileX - goalCellX
dz = tileZ - goalCellZ
arrived  <=>  dx*dx + dz*dz <= thresholdSquared
```

with signed 32-bit squares and an inclusive comparison. There is no Y term, no
heading term, no speed or settling term, and a blocked mover is not exempt.
The goal cell is derived from the order's world goal with the same
half-footprint-bias conversion the occupancy commit uses for its cached tile:

```text
cell = (goalWorld - bias*0x80000 + 0x80000) >> 20
```

an arithmetic shift, with bias the footprint half-extent pair. The threshold
is `floor(radiusParam/16)` squared.

**Correction — radiusParam provenance (audit note):** the earlier text read
"for HUD/AI-issued ground moves radiusParam is the definition's SightDistance
plus 4, so the predicate reads `dist² <= floor((SightDistance + 4)/16)²`" and
left QMove/Patrol provenance TODO(question). Direct static re-trace of the
ground move handler supersedes it: the Move_Ground phase-0 handler binds the
goal handle with the order node's radius field plus 4, and that field is zero
at order creation, so **HUD/AI-issued ground moves bind radiusParam 4 and the
threshold is `floor(4/16)² = 0` — the order completes only when the committed
tile equals the goal cell**. The sight-distance reads in the traced handler
region feed range/acquire paths, never the goal handle; the earlier
SightDistance+4 attribution was a misassociation.

**Closed — patrol radii and the VTOL radius (2026-08-26):** ground Patrol's
phase-1 bind passes radiusParam ZERO (threshold `floor(0/16)² = 0`,
tile-equality arrival), and the patrol phase-2 arrival rotates the record to
the tail (result 6) and resets to phase 1 — the patrol "waypoint rotate".
The earlier "Patrol-family substates bind other radii (halved or zero) whose
exact per-substate values remain TODO(question)" is closed: the ground
patrol radius is zero; the air patrol builds its path marker with the
arrival-radius flag 336 (`0x150`) — a fourth radius alongside the
48/128/320 family of section 10.1 — and orbits the goal at a 20-world-unit
offset with a random enemy-pick landing fallback when hostile units are
within 0xf00. The VTOL_Move arrival threshold attribution below is also
corrected: the handler reads the cruise-altitude field HALVED for the marker
height offset only; no kamikaze read exists in the handler. The goal-handle
threshold family `floor(radiusParam/16)²` therefore applies to the ground
handlers (Move_Ground, Patrol) only; the VTOL family uses the marker's
hypot-based arrival test of section 10.1 (see C5 closure below).

**Established fact — satisfied-bit notification:** On arrival the movement
layer ORs bit 0x20 into the order record's accumulated satisfied word. The
same word carries: 0x100/0x200 for path request done / no-path (written by the
path service through the goal-handle slot — section 7.2's early exits); 0x40
for route released before arrival; and 0x80 for
goal-handle detach or rebind.

**Closed — 0x40/0x80 retry interplay (2026-08-26):** the ground-move handler
tests only 0x20; when 0x40 or 0x80 reaches its completion phase the handler
returns result 9, and for the LAST record that re-arms the record (phase
reset to zero, gate re-armed, wait `30 + RNG(30)`) — the next pump visit
re-runs phase 0, which rebinds the goal handle and re-arms the 0xE0 gate. It
is re-arm, not drop; non-last records are unlinked and freed by the pump, so
a released route in the middle of the queue removes that record.

**Established fact — pump gate halt:** The primary pump combines the record's
satisfied word with the unit's capability word, masked by the record's dynamic
gate; when the gate is nonzero and the combination is zero it halts
head-blocking for that unit (section 3.3). The movement and path services
therefore fill satisfied bits between pump visits, and the pump itself never
runs movement. An arrival bit set during a unit's movement integration is
observed by that unit's NEXT pump visit — one-tick latency, part of the sweep
contract (sections 1.1, 5.4).

**Established fact — Move_Ground completion:** The handler is a two-phase
state machine. Phase 0 rejects with the cancel-all return code while the unit
is attached to a carrier; otherwise it binds a movement-goal handle for the
order's goal position (a 20-byte pool allocation holding the goal cell as
signed 16-bit X/Z, the radius parameter, and the squared threshold), arms the
record's dynamic gate mask 0xE0 (bits 0x20, 0x40, 0x80 — arrival, route
released, handle teardown), and advances. Phase 1, on the next pump visit with
a nonzero combination: if bit 0x20 is set it issues the move-complete
acknowledgement notification and returns result 5 — the pump unlinks and frees
the record, ORDER COMPLETE; otherwise it returns 9 — dropped while further
records follow, else the record resets its phase and waits `30 + RNG(30)`
ticks (range 30 to 59).

**Closed — VTOL_MOVE phase trigger and C5 mechanism split (2026-08-26):** the
VTOL_MOVE handler is a THREE-phase machine, not a two-phase one: phase 0
(alive + canfly gate) clears weapon slots, detaches from a carrier, raises
the activation edge, builds the 0x36-byte path marker on the unit's position
with the cruise-altitude field halved as the marker height offset, installs
it, and arms gate 0xE0; phase 1 clears the weapon targets, RE-SNAPS the goal
to the half-cell grid relative to the unit (`newGoal = (unitPos + 2 ·
quantize(goal − unitPos)) << 16` per axis), builds and installs a fresh
marker, re-arms 0xE0, and advances; phase 2 completes unconditionally
(acknowledgement only when the record is last) with result 5. The trigger
that advances the handler from its phase-1 wait to the terminal phase is
therefore the state machine itself: each visit requires a nonzero gate
combination, and phase 2 runs on the first satisfied visit after the
phase-1 re-arm. The earlier reading — "a radius parameter of the
definition's kamikaze distance field clamped to at least 16, threshold
`floor(max(kamikaze,16)/16)²`" — is superseded: the handler contains no
kamikaze read; the marker's height offset is `cruisealt/2`, and arrival is
the marker's hypot test of section 10.1. This closes C5: the two arrival
mechanisms split by ORDER FAMILY — ground orders (Move_Ground, Patrol) use
the 20-byte goal handle with the tile-threshold predicate; VTOL orders
(VTOL_Move, VTOL_Patrol) use the 0x36-byte path marker with the hypot
predicate (radii 48/128/320, or 336 for patrol, default 0.5 world units).
The two mechanisms never cross order families.

**Audit note — completion-wait ranges [R-P0-01]:** the code-9 last-record
wait is `30 + RNG(30)` (range 30 to 59), NOT `30 + RNG(15)`: only the code-3
arm draws below 15. The code-9 row of the section 3.3 table is corrected in
place, superseding the earlier [P0-08]/[SC8] reading and document 05's "Queue
pumping and result codes" table.

**Established fact — local settling family (steering only):** The ground
mover has a speed-derived settling threshold for steering and braking:
threshold 65536 (one 16.16 world unit) when `(speed & ~3) < 262144` (below
four units), otherwise `speed >> 2` with signed truncation; the vertical
comparison is strict (`-threshold < dy < threshold`). This family belongs to
waypoint/steering logic only — it never appears in the order-completion chain
— and blocked movement halves speed and clamps the resulting displacement
without a replan, yield, or queue transition (section 8.2's blocked-mover
contract).

**Closed — compact ground controller (2026-08-26):** the per-unit sweep
gates the order pumps, the movement tick, and the height snap on the owner
player's state byte being 1 or 2; state-3 owners' units receive the script
drain, the stun/paralyze timers, and the death check but NO order pump and NO
movement integration, so their orders cannot complete through the sweep
because the whole pump+mover block is skipped. The player phase additionally
treats state 3 with the watch-mode ("You're out — Continue Watching?")
branch, so state 3 reads as the eliminated/defeated watch player state
(Supported inference for the label; the skip itself is direct). The earlier
wording — "wires its per-tick hook to a plain return, so the arrival
notification is not wired there; how that population's ground orders complete
is TODO(question)" — is superseded: the mechanism is the sweep's state-gate,
not a hook, and the population is the watch/defeated one, whose units are
inert in the sweep by design.

## 9. Hover, floaters, and amphibious behavior

### 9.1 Medium bands

**Established fact:** The live mover's low two bits are a runtime movement mode
mirrored into the unit, not a static family: `1` is stopped/parked (the helper
that writes `1` zeroes velocity and runs lean decay) and `2` is active
locomotion (the flight integrator runs only for `2` and the pickup validator
rejects candidates whose mode is `2`). Modes `0` and `3` are preserved through
save and load but have no ordinary bounded gameplay producer and are used only
as `0`-mappings in the classifier below.

**Established fact:** `setSFXoccupy` is a five-value classifier with sequential overwrite and edge-triggered caching. It is computed from the committed mover-mode mirror, signed integer height `wy` (the signed high word of 16.16 Y, same domain as the sea-level byte `wt`), authored waterline byte `wl`, signed model-bottom word `mb`, and the cached prior band. All comparisons are signed integers in height-byte units, not 16.16 world units:

```
if mode not in {1,2}: band = 0
else if wy > wt:      band = 4
else:
    band = cachedBand
    if wy - wt > -5:  band = 1
    if wl + wy == wt: band = 2
    if mb + wy < wt:  band = 3
```

The three underwater tests are ordered overwrites `1→2→3` — `3` wins if both `2` and `3` hold — not exclusive branches; if none matches the cached band is retained. Band `4` is strictly above water; `1` is the shoreline skirt within five units above water; `2` is draft exactly at surface; `3` is model bottom below water. When `band != cachedBand` the engine starts one-argument asynchronous `setSFXoccupy` with `band` and updates the cache; otherwise no callback is emitted. The classifier runs once per mover tick after the occupancy commit and before the stopped-state Y correction.

**Established fact:** Hover and floater units reuse the ground integrator and terrain validator — the same heading clamp, acceleration/brake choice, pitch-table cap, and footprint validator as ground — not a separate hover controller. Water depth, slope, and sea-level tests remain profile-driven via the movement class. Wake is not an engine GAF: the engine emits only the band change; shipped hover scripts gate wake effects on bands `2` or `3` and spawn them via `emit-sfx` types `2` through `5` from dedicated `wake` pieces (single-vertex pieces), with no engine wake renderer.

### 9.2 Flags and damage

**Established fact:** Definition bits and fields participate as: `canhover` is bit 12, `floater` is bit 19, `upright` is bit 20, `amphibious` is bit 21, `hoverattack` is bit 27. `canhover` excludes the unit from water-damage and participates in the stopped-state Y pre-gate; `floater` selects the ship surface clamp at `waterline + sea level`; `upright` keeps the model vertical and computes Y as `max(terrain, sea level minus waterline)`; `hoverattack` selects the gunship attack variant, not hover locomotion. The `amphibious` bit is parsed and stored but has no reader in the bounded recovered movement, medium, targeting, or transport code — parser-only in that bound (bounded absence, not whole-executable impossibility). Shipped amphibious-looking behavior compensates via movement class `TANKHOVER3` values (movement class MaxSlope 12, MaxWaterSlope 255, no depth limits) and the `canhover`/`upright`/`waterline`/`modelBottom`/`movementclass` fields, not via the amphibious bit. Other medium fields are `waterline` byte (draft for band `2`), `movementclass` name resolved to profile, and `modelBottom` signed word (threshold for band `3`).

**Established fact:** Water damage is evaluated per unit before movement, once
per tick when `globalTick % 30 == 0`, only when the owning player's class is `1`
or `2` and both mission fields `waterdoesdamage` and `waterdamage` are nonzero,
and only when the unit's signed integer height is at or below the sea-level
byte and `canhover` is clear. `floater` and `amphibious` are not additional
immunity tests at this call site. Qualifying units receive `amount =
mission.waterdamage` as damage type `0xB` with a null attacker through the
standard damage funnel; blast falloff applies with the stored blast distance
(zero outside AOE, so multiplier 1.0 for non-AOE water damage), but type `0xB`
emits neither `HitByWeapon` nor `TakeDamage` COB callbacks and never enters the
feature-class effect block. Lethal `0xB` sets the normal death-pending state.
The shared funnel arithmetic inside that packet is owned by document 06 and
applies to water damage like any other non-heal packet: definition-scaled
armor reduction gated on bit 1 of the victim's instance armor byte
(mask `0x02`) with the strictly-below-`30000` amount guard, then the
veterancy tier factor `((25 − tier) · amount · 4) / 100` with
`tier = min(kills / 5, 5)` read from the victim's credited-kill counter — so
a veteran victim takes REDUCED water damage — and each credited kill
increments the killer's credited-kill counter.

**Supported inference:** The engine does not need a separate hard-coded wake
renderer to reproduce the callback contract; wake effects and medium bands in
shipped content are script-authored behavior gated on the bands above.

**Reconciliation with document 03 (2026-08-26):** document 03 section 5.7
describes engine-side "wake rectangles" produced from mover bounds and filled
with a palette tint under a fog gate. The two claims are reconciled as
complementary presentation layers, not competitors: the script-emitted
`emit-sfx` wake effects (types 2 through 5, spawned by shipped hover scripts
via the band classifier) are the authoritative medium-band behavior this
document owns, while the mover-bound rectangles of document 03 are a
presentation-side artifact keyed on mover bounds and bands with no
authoritative simulation role. Neither document asserts the other's mechanism
in its own section; this paragraph records the agreed split (ships/sea
rectangles live in document 03, hover wake effects live here). Document 03
section 11's open "wake rectangle interpolation" item remains open there.

**Unknown:** Complete wake and SFX-piece mapping for every band transition
(the script-emitted wake effects are established as spawner-driven in
section 5.2; the presentation-side wake rectangles of document 03 section 5.7
are that document's artifact — see the reconciliation note above),
sea-floor following, and wake rectangle interpolation beyond the band
arithmetic; the semantic label of mover modes `0` and `3`.

## 10. VTOL and flight

### 10.1 Shared mover and the can-fly integrator

**Established fact:** The mover fan-in selects a can-fly branch, but both ground and VTOL paths share speed, acceleration, turn, waypoint publication, final position commit, medium-band bookkeeping, and movement callbacks. No separate global VTOL physics loop was found. The flight integrator itself is exact:

**Established fact:** The flight integrator runs only when the mover's low
mode bits equal 2 — active locomotion. Any other mode assigns zero to all
three velocity components, the scalar speed word, and the 16-bit turn-residual
word, and performs no further flight update; there is no partial flight step.
The integrator's fixed↔float conversions and its distance floor are stored
executable constants — float `1/65536`, double `65536`, and float `8` live in
the constant pool — not derived values.

**Established fact:** For mode 2, before any command input is used, each
velocity component decays:

```
decay = 0x10000 − trunc_signed((Acceleration << 16) / MaxVelocity)
v     := trunc_signed((v · decay) >> 16)      // per component x, y, z
```

The division has NO zero-divisor guard: valid flight data must author a
nonzero MaxVelocity, and a zero divisor terminates retail outright.

**Established fact:** Brake shaping crosses into float: with `h =
hypot(vx,vz)/65536` and `b = BrakeRate/65536`, only under the STRICT
comparison `h > b` (equality skips the whole block) both horizontal components
scale by `trunc((b/h)·65536)` via `>>16`, and then the excess `q =
trunc((h−b)·65536)` is subtracted per-axis through fixed-point sine/cosine of
the heading using the shared table of section 5.1. Implementations needing
bit-exactness must keep this mixed fixed/float instruction order, not one
algebraically rearranged float expression.

**Established fact:** Vertical control is a sentinel-gated clamp. The sentinel is the unit's sector-list head field (the head pointer of the unit's occupancy sector list) compared by full 32-bit pointer equality against a global sector sentinel. Writers are only on footprint stamp: going out of bounds writes the global sentinel, otherwise writes the computed sector address; the value therefore flips only on a successful stamp or an OOB transition and persists across ticks. Readers — the flight integrator, the occupancy clear, and the follow-altitude helper — all perform the same full-pointer compare; when the unit's sector head equals the global sentinel the vertical assignment is SKIPPED entirely and vertical velocity keeps its damped value, with no limit computed.

**Established fact:** With `dy = unitY − targetY` (both 16.16) and current scalar speed: the per-tick vertical limit is `yLimit = 65536 (0x10000, one world unit) if (speed masked with ~3) < 262144 (0x40000, four units) else speed arithmetic-shifted right by two (speed/4)` and velocity is assigned directly:

```
if   dy <= -yLimit: vy = +yLimit
elif dy <  yLimit:  vy = -dy        // exact final snap onto the command altitude (vy = -dy)
else:               vy = -yLimit
```

Takeoff, climb, and descend are therefore velocity-limited, never Y
teleportation; the limit floor is one 16.16 world unit while the middle-branch
snap can be sub-unit; rising and descending share one rule; none of the air
service radii participates here.

**Established fact:** Heading integration is independent of the vertical
block: `err = (int16)(targetHeading − heading)`; a zero error zeroes the turn
residual WITHOUT setting the transform-dirty bit; otherwise the error clamps
to ±TurnRate (unsigned 16-bit definition field), writes the residual, adds to
the heading, and sets the transform-dirty bit.

**Established fact:** Horizontal acceleration uses post-decay/post-brake
velocities against the command targets:

```
dx  = unitX − targetX ; dz = unitZ − targetZ          // fixed point
dvx = vx − targetVx   ; dvz = vz − targetVz
d   = max(hypot(dx,dz)/65536, 8.0)                    // floor exactly 8 wu
k   = -sqrt((2·a)/d)                                  // a = Acceleration/65536
ax  = (dx·k − dvx)/65536 ; az = (dz·k − dvz)/65536
if hypot(ax,az) > a: ax *= a/hypot(ax,az); az *= a/hypot(ax,az)   // cap at Acceleration
vx += trunc(ax·65536) ; vz += trunc(az·65536)
```

The acceleration vector is capped at the authored Acceleration magnitude, and
the distance floor is exactly 8.0 float world units.

**Established fact:** Scalar speed is recomputed as the FULL 3-D magnitude
`trunc(sqrt(vx² + vy² + vz²))` — not the horizontal hypotenuse — feeding the
pitch/bank residuals: the new-minus-old velocity vector captured after the
commit drives the lean-decay / velocity-delta / gravity pipeline that writes
bank from the bank-scale-scaled X residual and pitch from the
pitch-scale-scaled Z residual into the two visual angle words.

**Established fact:** Cruise altitude for point and follow commands is `targetY = (max(sea level, terrain height at target XZ) + signed offset) × 65536` capped at `0x1FF0000` (about 511 world units), where terrain height is the bilinear four-corner query and the offset is `cruisealt` (full altitude) or `cruisealt/2` for the initial climb, or the negated attach-piece world Y for a hanging cargo. Sea level is the terrain header byte; terrain height is sampled at the cursor or at the followed unit's piece world position; there is no lower clamp. Arrival radii are horizontal and strict: explicit air arrivals test `hypot(dx,dz) < radius` with radii 48, 128, or 320 world units depending on order (flagged via the arrival-radius field), while the default test is `dx*dx+dz*dz <= 0.25` (0.5 world units) and for explicit-altitude commands also `|dy| < 65537` (one world unit plus one subunit). The radius-flag family now includes a fourth value, 336, used by the air patrol orbit (section 8.3). The marker machinery behind this paragraph — the 0x36-byte path marker, its arrival test (hypot with the 0.5-world-unit default and the flag-driven radius), the cruise-altitude height setter, and the goal re-snap — is established direct (2026-08-26); the order-family split between this hypot mechanism and the ground tile-threshold mechanism of section 8.3 is closed there (C5).

**Supported inference:** Can-fly movers bypass some ordinary ground footprint checks during travel, but air order admission and landing-pad checks still use separate validators.

### 10.2 Air orders

**Established fact:** Air order dispatch classifies move, attack, DGun, load/unload/pickup, follow/help/repair, patrol, hold, teleport, reclaim/resurrect, capture, and mobile-build cases. Weapon flags and attack-run/hover-attack data choose bomber, fighter, gunship, transport, or related behavior.

**Established fact:** Transport service lifecycle is exact for admission, carry,
unload, pads, and death:

*Admission.* `carrier, candidate` is admitted only if, in this order, none of these nine rejects fires: 1) candidate `cantbetransported` set; 2) carrier lacks `canload`; 3) carried-count (entries in the carrier cargo list whose parent equals the carrier) reaches carrier `transportcapacity` (count, not summed sizes; unauthored `0` therefore blocks loading); 4) carrier `transportsize` below candidate `FootPrintX` (signed compare, FootPrintX is the movement class footprint width); 5) candidate has no mover; 6) candidate committed mover mode is active locomotion (mode 2, moving) — moving cargo is rejected; 7) ground carrier (`canfly` clear) with candidate `MinWaterDepth >= 0`; 8) candidate `Y + modelTop` at or below `sea level × 65536` (submerged); 9) candidate landed-float field not exactly `0.0` (still under construction). Missing `transportcapacity` and `transportsize` default to `0`. The effective boarding range is the first enabled weapon slot's `range` (scanned via the weapon-slot enabled flag); shipped unarmed fallback is weapon record `0` (`NOWEAPON`, Range 16), so shipped unarmed pickup range is `16`. Ownership or alliance is not tested in this predicate, and the two command resolvers contain no alliance gate either (bounded-negative within them), so whether allied cross-owner commands are permitted remains an upstream command-layer question left open (`TODO(question)`).

*Load executor entry gates.* Independent of admission, every phase of the
canonical load executor re-checks four gates in order before doing work: the
order's target reference must be non-null; the executor flags word must hold
none of mask `0x10048`; the target's Y plus its definition's model
total-height value must be SIGNED greater than sea level shifted into 16.16; and the carrier's
cargo-list head must be null — the air-carrier executor requires an EMPTY
cargo list even though general admission only compares count against
capacity. Gate failures one and two share the `Transport mission failed`
terminal with result code 8; gate three emits that same message directly,
also code 8; gate four returns code 8 with NO message.

*Load phase table.* The load executor dispatches on the order's phase byte:

| Phase | Operations | Result |
|---:|---|---:|
| 0 | Require a live carrier mover and `canfly` (else 7). Size gate: the target's cached footprint-X WORD, compared signed, must be at or below the carrier definition's `transportsize` BYTE zero-extended; otherwise emit `Unit is too heavy to transport` and return 8. Set status message `Loading`; notify carrier state 3; detach the carrier from ITS own parent when carried; raise Activate; force mover mode 2 from mode 1; queue a point command at the carrier's current X/Z with altitude `cruisealt/2` (signed, round toward zero) and NO arrival radius; status bits `|= 0xE0`. | 1 |
| 1 | Queue the follow command toward the target with the full `cruisealt` altitude offset and horizontal arrival radius `0x30`; status `= 0x100E8`. | 1 |
| 2 | Status `Preparing for transport`. Pre-seed the first `QueryTransport` output to `-1` and run the synchronous four-output query (unanswered outputs read 0, so the observed seed is `[-1, 0, 0, 0]`; a missing script leaves `-1`, the root-piece fallback). Retain output 0 as the attach piece; status `= 0x100E8`. | 1 |
| 3 | Start asynchronous one-argument `BeginTransport` with the exact 32-bit value of the target definition's model total-height field — the height dword the engine derives from the 3DO bounds at definition load, not an authored FBI key (supersedes this row's earlier "model-top value" wording; see [R-UNIT-06 §3]) — mirrored through the network forwarder; fetch the selected piece's world transform; construct the cargo follow order with altitude offset = NEGATED integer part of that piece's world Y — the cargo hangs below the piece; status `= 0x100EA`. | 1 |
| 4 interrupted | Interrupt-flag combination present (`flags & 0x42`): start the deferred zero-argument `EndTransport` and return WITHOUT attaching. | 8 |
| 4 success | Attach the target to the carrier on the queried piece; emit event code 12; queue the climb-away point command at the carrier's current X/Z with altitude `cruisealt`, no radius; status `|= 0xE0`. | 1 |
| 5 | No work. | 5 |
| other | No work. | 7 |

Successful-load callback order is exactly `QueryTransport` (synchronous) →
`BeginTransport` (asynchronous) → attachment → event code 12. No successful
load runs `EndTransport`: that callback fires on later release or on the
phase-4 interruption edge only. The `BeginTransport` argument is the
definition's model total-height dword, NOT the attach-piece Y (supersedes the
earlier "model-top field" wording — see [R-UNIT-06 §3]); the negated
attach-piece Y value belongs solely to the cargo follow-order hang height of
phase 3.

*Load and carry.* A successful air load performs synchronous
`QueryTransport` with four outputs (first cell retained as attach piece) and
then asynchronous one-argument `BeginTransport` with the exact 32-bit value of
the target definition's model total-height field (see [R-UNIT-06 §3]), mirrored
through the network forwarder;
attachment uses the queried piece index. The carried-unit branch at the top of
the occupancy commit slaves cargo each tick to the named attach piece's world
transform, copies piece heading/pitch and carrier velocity/speed (zeroed if the
carrier has no mover), applies the floater deck-height clamp from the **cargo's**
waterline and sea level, and returns before ordinary footprint validation,
occupancy stamping, or coverage update; cargo still participates in the sweep
but does not integrate its own movement.

*Unload.* The canonical unload executor returns done (result 5) immediately
when the cargo list is already empty, then dispatches on the order's phase
byte. Phase 0 requires a live `canfly` carrier mover (else 7), announces
`Unloading`, records the cargo reference, and queues a point command toward
the stored drop point with altitude `cruisealt` AND horizontal arrival radius
`0x140`. Phase 1 converts the drop point to a footprint anchor using the
cargo's packed footprint dimensions and validates the cargo definition
through the standard placement validator in mode 1: failure emits
`Unable to unload unit` and returns 9; success queues the lowering command at
the same X/Z with signed altitude offset = the cargo definition's
model-bottom value and no radius. Phase 2 REVALIDATES — a second anchor
recompute plus validator call before release: an unload interrupt flag
returns 9 BEFORE that second validation; a failed revalidation emits the same
message and returns 9; success starts the deferred zero-argument
`EndTransport` FIRST, then detaches the cargo (reserved no-piece index), then
constructs the climb-away point command at the CARRIER's current X/Z with
altitude `cruisealt` — release order is exactly callback → detach →
climb-away construction. Phase 3 emits event code 13 with no text payload and
finishes. The placement validator therefore runs once before the final
lowering command and again immediately before detach (double validation).

*Landing pads.* `QueryLandingPad` is a synchronous four-output query on the target script; candidates are tried strictly in order `0` through `3` and the first piece that is not carried and not already assigned to another unit (any unit whose attach-piece field equals the candidate) wins. With no pad the loiter/spiral heading step is used; no free pad among those tried keeps the order alive for a next-tick retry or the `30+rand(15)` delayed retry, while the established `Landing aborted - all pads are occupied` and `Landing failed` branches are distinct.

*Carrier death.* A dying cargo first detaches from its carrier. If the dying
unit is a carrier, for each cargo head it applies `30000` damage through the
normal funnel with type `3` when `(deathSeverity & 0xF0)==0x30` otherwise type
`6`, credits the carrier's recorded killer, and detaches after each
application.

End transport writes cruise altitude and changes transport state only on the
paths above; landing-pad queries first use the script query and then apply the
fallback pad selection and failure handling described.

### Closed — attachment state values and transport callback encoding [R-UNIT-06 §3] (2026-08-28)

**Established — the attachment/detachment helper is the sole linkage writer.**
One helper maintains the carrier/cargo linkage for every caller (transport
load, unload, carrier-death cascade, save reconstruction, and the factory
product's builder link): given (child, parent, piece, mode) it validates the
child (alive, not building-class, no existing carrier) and the parent (alive,
not self, uncarried), then — attach — records the piece on the child, links
the child as the new HEAD of the parent's cargo list (each child's sibling
link pointing at the previous head, so the list is LIFO and the unload release
detaches the most recently attached cargo first), and — detach — clears the
child's parent, sibling, and piece fields. After either half it overwrites the
child's committed mover-mode pair with the request's mode value (unload
release passes the parked mode; a load's self-detach of a carried carrier
passes the take-off mode; the ordinary attach passes none/mode 0; the
movement-mode force helper writes the same field directly). It then wakes the
"becarried" re-arm path for computer-owned children whose parent is not an
airbase (which purges the carried unit's queue through the ordinary cleanup),
and finally deselects the child if it has become ineligible.

**Established — the status word's transport/attachment bits.** Two bits of
the unit status word are transport-relevant:

- Bit `0x20000` — "carried without a piece link". SET by the attachment half
  when the attach piece is the reserved no-piece index (piece `0xFF`: cargo
  riding the carrier without following a piece — the sea-transport
  re-attach idiom lands here), CLEARED by any attach with a real piece and by
  every detach.
- Bit `0x40000000` — a static mirror of the definition's `isairbase` flag,
  written once by the unit initializer (cleared, then re-derived from the
  definition bit) and never touched afterwards. It is NOT a dynamic transport
  bit. Its consumer is the shared selection-eligibility predicate: a unit
  with a parent reference is selectable only when the parent's status word
  carries this bit — so cargo aboard an ordinary transport is not selectable,
  while a child attached to an airbase (a landed pad guest, or a factory
  product only if the factory definition is an airbase) is. This closes the
  open question about the parent-status clause of the eligibility predicate.

The attachment linkage fields themselves (parent reference, cargo-list head,
sibling link, attach piece) are plain fields owned exclusively by the helper
above; the save block serializes the parent and the piece and the
reconstruction recursion restores them.

**Established — which script the transport callbacks run on.** Every
transport callback — `QueryTransport`, `BeginTransport`, `EndTransport`,
`TransportPickup`, `TransportDrop` — runs on the CARRIER's (executing unit's)
script. The cargo's script receives nothing on these paths.

**Correction — the `BeginTransport` argument is the cargo definition's model
TOTAL-HEIGHT dword.** Previous text (this section's phase-3 row and the two
"Load and carry"/callback-order paragraphs, and the earlier transport packet)
called it "the target definition's model-top value". There is no authored
model-top field: the value is the dword the engine derives from the 3DO model
bounds at definition load — the model's total height (max-Y), stored beside
the min-Y bound and the height-minus-min derivative. The same dword feeds the
load entry gate's "Y plus model height above sea level" test. The p1-05-era
"signed short, sign-extended" reading is also wrong — the full dword is
passed.

**Established — callback argument cells.** The engine's arrange call carries
(receiver, wake, arity, and four argument cells) and the four cells are
written into the callee's window words 0..3 unconditionally, with the logical
top set to `arity − 1` — so a cell beyond the arity is still physically
present to the script. Applied to the transport family:

- `QueryTransport`: synchronous four-output query; cell 0 pre-seeded `-1` and
  copied back into the transport order's retained attach-piece field;
  remaining cells null (seeded 0, no copy-back).
- `BeginTransport`: arity 1, cell 0 = the cargo definition's model
  total-height dword, fillers 0; wake flag set (the start performs the
  immediate all-slot drain barrier).
- `EndTransport`: zero-argument, deferred; issued at the load-interrupted
  edge and, on successful unload release, BEFORE the detach.
- `TransportPickup` (sea/hover pickup): arity 1, cell 0 = the cargo's stable
  unit identity (its pool slot id), fillers 0, wake flag set; the engine also
  emits notification event 12 right after.
- `TransportDrop` (sea/hover drop): arity 1, cell 0 = the cargo's stable unit
  identity, cell 1 = the packed drop point (destination X truncated to whole
  world units in the high half, destination Z integer part in the low half),
  remaining cells 0 — the position cell is physically present even though the
  arity byte says one argument.
- The network mirror of a script-call emission carries (unit, callback
  identity, arity, the four cells) — the same cell vector the script sees.

**Established — the executors' "status bits" are gate re-arms.** The
load/unload phase-table writes rendered as `status |= 0xE0` /
`= 0x100E8` / `= 0x100EA` are writes to the ORDER RECORD's dynamic gate word,
not to any unit status word: the executor re-arms its own record's gate with
the movement-service satisfied-bit values (`0x20`/`0x40`/`0x80`/`0x100`/
`0x200`) so the record re-dispatches when the queued movement reports
arrival, release, rebind, or the air-marker conditions. The goal-payload
installers clear exactly those bits when a new goal is installed, which is
why every rebind starts with a clean satisfied word.

### 10.3 Patrol and air construction orbit

**Established fact:** Air construction orbit is an exact geometric recurrence,
not a fixed indexed array of stations. The air-build executor first queues an
initial self-XZ climb order at `cruisealt/2`, approaches the site with
descent-rate word equal to authored `builddistance`, and, while in the build
state, on every tick satisfying `globalTick % 150 == 0` recomputes the heading
from the builder's current position to the site, adds signed `0xDB6E` (`-9362`,
about `-51.43` degrees), and creates the next waypoint at `builddistance << 16`
from the site's center using the retail fixed-point trigonometric helpers. Build
power for that tick is applied as definition work value divided by 30. This
recurrence from the current bearing yields about seven stations geometrically,
but the station count is a consequence, not an authored constant, and exact
travel time between generated waypoints beyond the recurrence remains an
independent question.

**Supported inference:** The recurrence itself is the contract; treating the
seven-station observation as an exact array size would misstate the executable
behavior. The per-waypoint travel time is a consequence of the integrator
(computable, not an authored constant), so the §11 bullet on it is closed as a
consequence, not a separate contract [movement/13].

**Unknown:** Ground-following and sea behavior while orbiting, pad reservation
beyond the landing-pad selection described in 10.2, carrier collision beyond
the ordinary shared-mover rules, and the order-layer command-target supply for
each non-construction air class beyond the established altitude authority and
integrator arithmetic of section 10.1.

## 11. Evidence basis and correction boundaries

The movement and script sections above were derived only from these areas of the retail executable:

- the tick-phase, wall-clock, entity-identity, queue, and pool machinery;
- the ground-path search, locomotion, collision, hover, flight, transport, patrol, and air-construction movers;
- the script loader, interpreter, thread scheduler, engine ports, callbacks, and piece animation;
- the construction and factory handlers, for order and queue interaction only;
- the computer-player and mission entry points, for player and mission
  boundaries and the InitialMission order preload of section 3.6 only.

Function identities that a later re-derivation corrected — in particular the mistaken strategic-planner, visibility-writer, and construction-helper identifications — are not used as evidence.

## Missing and unknown

### Simulation and identity

- The placement validator's caller-mode value at the factory exit-spot call
  (whether the inline aggregate terrain gates run there at all) — see
  [R-P0-08-A §1] in section 6.4.

- Complete lockstep packet ordering, replay state, state-hash contents, and resynchronization behavior.
- Complete serialization of transient order queues, script callbacks, and
  medium state; pending path-request state is byte-exact via the goal
  handle's save slot (0x36-byte marker image, two-bit route count,
  active-bit gating) and pending order state is saved in full on BOTH queue
  segments by the unit save writer (front and rear heads walked, each node
  serialized with its owner and slot-derived name, 2026-08-26); the restore
  side of pending script state remains partial [P1-13].
- Exact same-tick visibility for every possible unit creation caller
  (factory-product publication windows are established in section 3.8
  [R-P0-09]); slot-relative reuse and order-created units beyond the factory
  path remain open.
- **OTA-FAC-01 / OTA-FAC-01B / P28-FAC-01R
  [R-FAC-01][R-FAC-01B][R-FAC-01R]:** the engine-side completion order and
  stock callback choreography are established: StopBuilding is deferred before
  the same-pass count test, an empty count then defers Deactivate, and stock
  Deactivate waits 5000 ms before RequestState(1) drives Stop/CloseYard and
  the selected COB's authored close animation. No separate post-completion
  factory egress order, producer/product collision exemption, blocked-release
  policy, or aircraft takeoff-before-rally transition was recovered. Generic
  VTOL takeoff, QueryBuildInfo target derivation, primary-queue gating, and
  the pre-allocation retry split are established. The stock exit-piece
  indices, names, and authored 3DO local translations are established by the
  asset census, and the exit-piece locator's runtime transform — including
  the unit-orientation fold at the model root, the rotation order, the
  round-to-nearest per-axis narrowing, and the single output Z negation — is
  established in [R-REV-02]. Multiple-product no-stacking remains Unknown.
  These residuals block only release/egress behavior; the narrower
  idle-closure path is implementable from the established callback sequence
  and authored COB waits.
- Complete player category, side, ally, autonomy, and strategic-AI semantics.

### Orders and queues

- Per-phase operation-byte VALUES inside the construction/factory handler
  family (the 68-descriptor handler set itself is closed — every handler
  identity is recovered from the descriptor table, 2026-08-26).
- Consumers for the descriptor class parameter and for the unnamed gate-mask
  bits — the retained-opaque static bits are enumerated with their carrying
  orders in the [R-DOC04-C] audit note (§3.1): bits 1-8, 11, 16, 17, 19, and
  24; bits 14 and 21 exist only at runtime (enqueue inheritance and cached
  target position) and appear in no static mask.
- Where the interface and network layers replace or cancel the front order,
  which the queue pump itself never does; the mask-2 cancel notification
  itself is delivered by the node cleanup path (section 3.3, 2026-08-26).
- Exact attack, reclaim, guard, and patrol goal predicates: the attack-chase
  orbit substate arithmetic is now direct (leash test, 8-world-unit vertical
  threshold, quarter/half/zero standoff binds, the two banded pairs, the
  ±90° randomized orbit direction) with the standoff VALUE itself still
  inference (produced by the weapon-slot engagement-distance helper); the
  patrol radii are closed in section 8.3; the guard assistance branches,
  their eligibility comparisons, and the admit-phase anchor draw are closed
  in [R-UNIT-06 §1] — remaining there: the producers of the guard's re-arm
  gate bits `0x08`/`0x10` and of the ward's engagement-target link (below).
- Order behavior when a path is empty, stale, blocked, or budget-delayed:
  closed in [R-ORDER-02 §1] (2026-08-27) — the per-family result-code
  mapping, the masked-out path-status bits, and the bounded-retry census are
  established there. Remaining: whether the route-release event fires for a
  route that was never published (unreachable goal as rebind loop versus
  silent stall), which needs the movement wrapper's per-tick release-callback
  states.
- Consumers of the `StartBuilding` script event's fourth argument: CLOSED by
  [R-UNIT-06 §4] — the premise was wrong (the record-pointer value arrives in
  the FIRST argument cell, not the fourth; no stock script touches a fourth
  cell, and 49 stock scripts consume the first cell as a build-heading angle).
  Residual: a friendly semantic name for that first-argument value.
  Still open in the same cluster: the semantic name of the weapon-slot
  control byte's bit 4 (set by the cleanup-variant clear, cleared by the
  mid-life variant); the producer pair behind the pump's satisfied-bit-0x10000
  weapon-slot clear; and the producers of the guard re-arm gate bits `0x08`/
  `0x10` ([R-UNIT-06 §1]) — all one unlocated-writer family.
- The producer that sets a unit's engagement-target link (the ward-side
  reference both guard handlers attack toward, restored by save, cleared by
  the per-tick refresh): consumer semantics are Supported inference
  ([R-UNIT-06 §1]); the writer was not located in the bounded decompiled set
  or by instruction-pattern scan.
- Emission frequency of the nine nanolathe/assist StartBuilding sites
  ([R-ORDER-02 §2]): the tracer pins the call sites to the handlers but not
  their reachability per visit. Nanolathe places the emission once per record
  activation (the MobileBuild success path; the Reclaim setup visit), reading
  the flag's one-counterpart-per-record cleanup contract as once per
  activation; whether retail re-arranges the emitter on later work visits is
  untraced. Settling it requires the per-visit control flow of one handler
  (Reclaim is the cheapest: a cadence machine with visits every two ticks).
- Exact construction/economy carry and worktime-under-one-tick behavior
  (document 05); completion ordering and the completion/activation/rally
  callbacks are established in section 3.8 [R-P0-09].

### COB

- `TODO(question)` [R-P28-COB-01R] — the exact first committed retail ARMCK
  pose and whether its authored waiting `Create` work advances before that
  publication. The common immediate delta-zero drain, stock asset/piece link,
  and Nanolathe's three creation routes are established, but only the paired
  retail settling probe in [R-P28-COB-01R] can authorize a timing change.
- Reserved opcode `0x10063000`: behavior when a synthetic or corrupted script
  emits it (count exceeding the window depth reads stale window words —
  undefined behavior, not a kill).
- Allocation failure and corrupted-save fault policy — where retail would
  abort through its allocator's abort path remains `TODO(question)`; the
  invalid-piece-index half is closed (no bounds check in the interpreter;
  out-of-range index is out-of-bounds access, not a kill — the kill path is
  unknown-opcode and thread-return only, 2026-08-26) and the stack-overflow
  half is closed (push and pop have no depth guard either — overflow writes
  past the ten-word window, undefined behavior, not a kill; Nanolathe
  bounds-checks as its sanctioned divergence, 2026-08-28 [R-COB-01 §1]).
- The `Aim*` ready-writer closure: the receiver protocol and the write it
  performs are closed in section 5.3 (return-opcode invocation of the
  receiver with the script's returned cell); the identity of the closure
  target (the dword at the receiver word) remains `TODO(question)` R-1,
  coordinated with document 06 section 3.4; a receiver-clearing helper that
  scans the eight threads has no located caller (bounded relative-call
  census, 2026-08-28 [R-COB-02 §1]) and grants nothing.
- Script statics are not initialized by the program bind (piece animation
  state is zero-filled, statics are not — [R-COB-01 §1]); the initial content
  the tagged allocator itself provides is Unknown, insensitive for stock
  content because shipped scripts write before read.
- The crash policy for a scriptless unit (null compiled program) whose update
  path runs: one traced producer site lacks a null-VM guard, so retail would
  fault there; stock content never exercises it — `TODO(question)`, settle
  with a synthetic scriptless definition ([R-COB-01 §1], UNIT-04).
- The script-touched marker consumer, the busy-bit semantic NAME (the
  transport-admission half is closed: no busy-bit test in the nine gates),
  the engine-driven cloak-family bit name, the packed position-half axis
  naming, the yard-open admission class matrix, and bare-`get UNIT_HEIGHT`
  dependency — TODO(question) in section 4.7 [R-P0-10].
- Semantic unit of the footprint-path `SetSpeed` argument, and serialization
  of the unassigned `Killed` variant cell when the script neither assigns it
  nor the work-fraction gate forces zero.
- Unnamed save fields and complete restore behavior for pending calls, waits, and signal masks.

### Terrain and pathfinding

- Retail default content of the settings string feeding the heuristic-weight
  parse (the ×65536 fixed-point form and {6, 3, 1} tiers are established), and
  any runtime surface that rewrites that base besides settings application.
- Class-D restored-from-save goal usage frequency, and whether loaded saves
  ever re-issue fresh point/annulus/rectangle goals replacing them.
- Exact order-layer identity of each point-goal wrapper call site beyond the
  classified exemplars; heuristic family, formulas, scaling, threshold, and
  goal constructors are established in section 7.2.
- Exact semantics of terrain state one versus clear state three beyond
  passability: the expansion applies no per-edge terrain-state cost (the
  neighbour cost is step cost plus turn penalty plus the fixed 30 plus the
  short-run 75 only — bounded-negative, 2026-08-26).
- Full static feature/yard/owner-mask interaction.
- `TODO(question)` — the blocker channel for BUILDING footprints in the
  per-class passability layer: building occupancy changes restamp the layers
  ([R-DOC04-B]), but neither classifier form tests any building-state byte,
  and the owner/building-mask miss value (2) is traversable to the search
  expansion, so whether (and how) a building hard-blocks a path request
  before the movement-commit validator rejects it is unresolved. The
  expansion/commit split of §8.2 is the observed behavior; the layer's role
  in it is the open half.
- Exact out-of-bounds goal handling for every order type.
- Heap OOM policy and integer overflow behavior; route caps are established
  (20 published points, 64-point reconstruction ring, 3 saved waypoints).

### Ground movement

- **OTA-MOV-02A [R-MOV-02A]:** stationary versus moving mobile blockers,
  head-on and same-destination outcomes, sequential sweep ownership, the
  final-commit half-speed/clamp response, absence of collision retry counters,
  and the occupant-age/request-revision interaction are established within
  their stated call-chain bounds. Any outer yield owner, retarget, sidestep,
  reverse, wait-queue, or automatic replan outside that bound remains Unknown
  and requires the focused retail encounter trace specified in [R-MOV-02A].
- Collision behavior beyond that bounded multi-unit commit path, including
  untraced feature/building/map-boundary interactions, remains open.

### Hover and VTOL

- Semantic names and ordinary gameplay producers of mover modes `0` and `3`
  (save and load preserve them but no ordinary bounded producer was found);
  the mover's signed integer height provenance is established as the signed
  high word of its 16.16 Y.
- Semantic name and domain of the global sentinel compared against the unit's
  sector-list head field, which bypasses the flight vertical assignment
  entirely (bypass behavior itself is established in section 10.1).
- Sea-floor behavior and detailed wake and SFX-piece mapping beyond the exact
  `setSFXoccupy` five-band classifier; the presentation-side wake rectangles
  of document 03 are that document's artifact under the reconciliation note
  in section 9.2; the band thresholds, their height-byte domain, overwrite
  order, mode dependency, and edge-triggered delivery are established.
- Can-fly terrain bypass conditions for every order beyond the established
  flight selection in the mover fan-in.
- Cruise-altitude reference details beyond the established order-goal `Y`
  authority (upper cap `0x1FF0000`, sources `cruisealt`, `cruisealt/2`,
  `modelBottom`, and negated attach-piece Y; the cruise-altitude field read
  by the VTOL handlers is pinned, 2026-08-26), landing and descent arrival
  radii (`0x30`, `0x80`, `0x140` are horizontal arrival radii, not descent
  rates; the patrol orbit adds a 0x150 radius), pad reservation beyond the
  four-candidate `QueryLandingPad` selection and the `0`/`30+rand(15)`
  retry protocol (the rand(15) arm is the code-3 wait, confirmed; the
  code-9 arm is rand(30) — section 3.3), and carrier collision beyond the
  ordinary shared-mover commit rules; the load/unload executor entry gates,
  six-phase tables, statuses, and event codes 12/13 are established in
  section 10.2.
- Air patrol orbit admission beyond the established `150`-tick recurrence at
  `builddistance << 16` with offset `0xDB6E` and build power `work/30` per
  tick; exact travel time between generated construction waypoints is a
  consequence of the integrator, not an authored constant (section 10.3), and
  construction target-eligibility gates.
