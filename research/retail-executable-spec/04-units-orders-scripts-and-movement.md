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
before any phase runs [P0-09]. [01 §4.4] owns the global twelve-phase
order and the post-phase-12 sub-tick tail; this document does not restate them.
It owns phase 2's deterministic unit sweep and per-unit micro-order because
those boundaries determine same-tick unit visibility and callback order.

**Correction (2026-08-29).** The previous summary grouped visibility, trigger
polling, sharing, economy settlement, and presentation as separate global
phases. That contradicted the established dispatcher graph in [01 §4.4], so
the duplicate global summary was removed and only phase 2's owned ordering is
retained below.

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
[P0-09].

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

**Established fact:** Save reconstruction preserves
unit slot identity and repairs cross-unit references after the units are
reconstructed. A failed unit allocation can cause the saved record to be
skipped.

**Established fact — pool capacity [P0-16]:** The physical pool capacity is
`(u16)catalogDefCount · 10 + 1` records of 0x118 bytes, allocated at battle
entry. The pool is laid out as per-player slices of `catalogDefCount`
records; slot 0 is the null sentinel. Allocation scans each player's slice
lowest-free and enforces the per-definition limit gate (the definition's
limit-enable bit plus its limit field). The mission `maxunits` field is NOT
read by the allocator — it does not bound allocation (bounded-negative,
2641-TU census) — and save restore verifies the forced slot. The earlier
"exact maximum unit count is not resolved" reading is superseded by this
derivation [P0-16].

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
remain outside this section's scope. [P0-16]

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

**Retail save-reconstruction finding.** Save
reconstruction also invokes the allocator (including the forced-slot path),
consuming the initialization draws, and then restores the saved unit heading.
Thus a saved heading is authoritative and is not recomputed from `buildangle`;
a restored unit's RNG position still includes the allocator's draw sequence. A
failed limit/slot allocation returns before common initialization and consumes
no angle draw.

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
re-sorted ascending by canonical command name using the **case-insensitive**
string comparison — the same comparator the name lookup binary-searches with
`[R-STANCE-01 §9]` (§3.4) — and **an order's numeric identity is its index in
that sorted table**. Because all four batches register before play begins, the
identities are stable for the whole session.

**Correction (2026-08-29, RWU-04-2).** The sentence above previously said the
sort used a **case-sensitive byte comparison**, and the table below listed
`AttackSpecial`, `AttackUType`, `Attack_Chase`, `Attack_Kamikaze`,
`Attack_NoMove` as identities 6–10 and `BuildWeapon`, `BuildingBuild` as 12–13.
That ordering came from `[R-DOC04-C]`'s recomputation rather than from the
runtime table, and it is wrong: `[R-STANCE-01 §9]` traced the registration
routine's comparator and it is the case-insensitive compare, under which the
underscore sorts below every letter instead of between the upper- and
lower-case ranges. The permutation below is copied from that finding, not
re-derived — identities 6–10 become `Attack_Chase`, `Attack_Kamikaze`,
`Attack_NoMove`, `AttackSpecial`, `AttackUType`, and 12–13 become
`BuildingBuild`, `BuildWeapon`. Nothing else moves: the empty name still sorts
to index 0 and remains the reject sentinel, `GetBuilt` is `0x13` under either
order, and every per-descriptor payload column is unchanged. `[R-DOC04-C]`'s
audit note below retains its own wording; its "recomputed from a case-sensitive
byte sort" clause is superseded by this correction.

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
| `Attack_Chase` | Attacking | 0x08 | 1 | 0x280 |
| `Attack_Kamikaze` | Attacking | 0x08 | 1 | 0x600 |
| `Attack_NoMove` | Attacking | 0x08 | 1 | 0x280 |
| `AttackSpecial` | Annihilating | 0x08 | 1 | 0x680 |
| `AttackUType` | Attacking | 0x00 | 19 | 0x4 |
| `BeCarried` | Being transported | 0x00 | 19 | 0x24 |
| `BuildingBuild` | Nanolathing | 0x00 | 19 | 0x10010c |
| `BuildWeapon` | Nanolathing | 0x00 | 19 | 0xc0140 |
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
- Batch 4: the empty canonical name. *Corrected 2026-08-29 ([03 R-AUD-02],
  registrar row of its trail):* this bullet previously said "no static image of this
  record exists in the read-only data … appended at registration is Supported
  inference". A static record **does** exist in read-only data — label `Ready`, ack
  group 15, mask 0, empty canonical name — and the registrar inserts it; Established.
  The batch sizes 23/22/22/1 are unchanged.

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
following static bits have no located consumer and must be stored opaque, not interpreted
(two of them have since been located — bit 2 is the purge-survivor bit, [R-MOV-03 §6];
bit 7 is read by the under-attack notice, [06 R-WPN-04 §2] — and are kept in the list
only so the census stays auditable):
bit 1 (0x2 — `Move_Ground`, `Patrol`, `RepairPatrol`, `VTOL_Move`, `VTOL_Patrol`,
`VTOL_RepairPatrol`); bit 2 (0x4 — `MakeSelectable`, `Wait`, `AttackUType`,
`WaitForAttack`, `GetBuilt`, `BeCarried`, `Paralyze`, `SelfRepair`, `BuildingBuild`;
**located:** the purge-survivor bit of section 3.3, [R-MOV-03 §6]);
bit 3 (0x8 — the build family: `BuildingBuild`, `HelpBuild`, `MobileBuild`, `VTOL_HelpBuild`,
`VTOL_MobileBuild`); bit 4 (0x10 — `Suppress`, `Patrol`, `RepairPatrol`, `VTOL_Patrol`,
`VTOL_RepairPatrol`); bit 5 (0x20 — the cloak/standing family, `Guard_NoMove`, `Paralyze`,
`BeCarried`, `GetBuilt`); bit 6 (0x40 — the cloak/standing family, `BuildWeapon`,
`SelfDestruct`); bit 7 (0x80 — `Attack_NoMove`, `Attack_Chase`, `AttackSpecial`;
**located:** the damage dispatcher's under-attack notice reads it on the victim's front
primary order and stays silent while it is set, [06 R-WPN-04 §2]); bit 8
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

**Established — field roles the handler bodies pin down (2026-08-29,
RWU-04-11).** The per-visit contracts of §3.9 read and write these fields, and
reading them across the whole handler set settles four points the list above
left open:

* The **guard/fight anchor pair** is stored in **whole world units** — the high
  halves of a 16.16 X and Z — and every consumer sign-extends both terms as
  16-bit values before subtracting ([R-STANCE-01 §4]). It is written by the
  auto-engage issuer's maneuver arm and by the guard's admit phase; no handler
  writes it from a goal position.
* The **three general parameters** carry more roles than the sentence above
  lists. The first is also the product definition index for a mobile build, the
  stance value for the two standing-order writers, the timeout budget in ticks
  for `Wait`, the stun credit in ticks for `Paralyze`, and the remaining work
  amount for the reclaim family. The second is also the scan radius for `Wait`,
  the self-destruct countdown state, and the effect-cadence accumulator for
  reclaim. The third is also the `MobileBuild` blocked-area retry counter and
  the pursuit leash of the repair family, which enforces it with exactly the
  chase attack's arithmetic.
* The **static-mask copy** carries two runtime bits that no static descriptor
  mask sets: a one-shot **caption-pending** flag that the shared caption clear
  tests and clears, and the **StopBuilding-pending** flag of [R-ORDER-02 §2].
  The auto/default-operation flag is inherited on insertion, and the
  rear-segment flag selects the segment.
* The **satisfied/pending word** is a field distinct from the dynamic gate mask:
  the gate says which bits the record is *waiting for*; the pending word
  accumulates which have *arrived*. Every goal installer wipes the five
  movement pending bits as its last act, so a re-armed record always starts a
  fresh path request with a clean pending word (§3.3).

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

### Correction — the pending word's bits, and who arms them [R-ORD-01 §0] (2026-08-29)

**What the earlier text said.** This section called the record's accumulated
word "the accumulated satisfied-gate bits" and named no bit but the pump's own
bit 0 (deadline expiry). The bit meanings were stated only in passing, in three
places that did not agree on scope: [R-ORDER-02 §1]'s path-outcome table gave
`0x20`/`0x40`/`0x80` as movement outcomes; [R-PATH-01 §9] added `0x100`/`0x200`
as search notifications; [R-UNIT-06 §1] listed `0x08`/`0x10` as guard re-arm
bits with unlocated producers. Nothing said which handlers *arm* which bits, so
a reimplementation had no way to know whether a bit it raised would ever be
consumed. That is the gap this correction closes; nothing previously stated is
withdrawn.

**Established — the five movement bits, one list.** The goal object holds a
reference to the order record that created it, and the movement and path layers
OR these bits into that record's pending word [R-PATH-01 §9]:

| Bit | Raised when | Raised by |
|---:|---|---|
| `0x20` | the follower observes the unit has reached the goal | route follower |
| `0x40` | an empty route is published while the unit is **not** at the goal — the "cannot get there" signal | route publisher |
| `0x80` | a previous goal object is released | payload installers |
| `0x100` | the request's start is already satisfied, or the pre-search ray connected | path search |
| `0x200` | the request's start is out of bounds, or the ray did not connect | path search |

All five live in bits 5 through 9, and **every** goal installer — point,
annulus, rectangle, and the prebuilt-payload variant — clears exactly those
five bits on the record as its last act before publishing the new payload. A
handler that installs a goal therefore cannot see a stale outcome from the
previous one, and the re-arm loop of [R-ORDER-02 §1] is clean by construction.

**Established — the gate is what makes a bit matter.** A pending bit reaches
the handler only through the pump's intersection with the record's dynamic gate
mask (step 2 above). The masks handlers actually arm, across the whole
descriptor table, are drawn from:

| Gate bit | Consumers observed | Note |
|---:|---|---|
| `0x1` | every deadline; the shared deadline setter always ORs it | the pump sets it on expiry |
| `0x2` | cancel-current notification | producer is a removal path, not a bit writer ([R-ORDER-02 §2]) |
| `0x4` | the `INBUILDSTANCE` wait of the work orders (§3.9) and the wait/select family | |
| `0x8` | interrupt/abandon; paired with `0x10` by the guards | producer unlocated |
| `0x10` | guard re-arm; also the second interrupt bit | producer unlocated |
| `0x20` `0x40` `0x80` | the movement outcomes above | the move, patrol, park and work families arm `0xE0`; several work handlers arm `0xE8` (adding `0x8`) |
| `0x100` `0x200` | path-status notifications | **no handler arms them**: they are raised and then masked out of every satisfied set ([R-ORDER-02 §1]) |
| `0x400` | patrol/suppress waypoint rotate | |
| `0x800` `0x1000` `0x2000` `0x4000` | the attack family's engage/disengage combination (`0x11808`, `0x13808`, `0x148e8`, `0x100e8`) and the guard's `0x7008` | |
| `0x8000` | the under-construction wait of `GetBuilt` | |
| `0x10000` | the pump's three unconditional weapon-slot clears | see below |

**Established — which handlers arm gate bit `0x10000`.** The Missing list asked
"which handler arms that gate bit". The armers are the combat and work
families, not one handler: `Standby` and `Standby_Mine` arm it alone on both
their phases; `Attack_NoMove` arms it inside `0x11808`; `Attack_Chase` inside
`0x13808`, `0x148e8` and `0x100e8`; `Guard_NoMove` tests it in its own
pre-check; `ReclaimUnit`, `Reclaim` and their VTOL twins arm it inside
`0x100e8`/`0x10008`. Every one of those handlers *also* treats the bit as a
reason to end the order — the pre-checks of §3.9 test `0x10008` or `0x10808`
and return the completion code — so the pump's slot clear and the handler's
own exit are two halves of one "this order was interrupted" event. **Still
Unknown:** what writes bit 16 into the *unit capability word* that the pump
intersects with. *Decider:* static trace of the writers of that word; the
bounded census of [R-UNIT-06 §1] found none, and it must not be invented.

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

### Closed — the idle-queue refill from `defaultmissiontype` (2026-08-29)

**Established ([02 R-KEYS-01 §1]).** When a unit's order queue is empty, its
owner's controller type is 1 or 2, and the definition's compiled
`defaultmissiontype` code is non-zero, the primary queue pump allocates a
fresh order record carrying that mission code and pushes it as the unit's
standing task; a zero code (empty or unrecognised name) leaves the unit idle.
The name is resolved through the same mission-type vocabulary as
`InitialMission` (§3.6). This is the only reader of the key; doc 02 owns the
parse.

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

### Correction — the descriptor table is sorted case-insensitively [R-STANCE-01 §9] (2026-08-29)

**What was said.** §3.1 states that after every template batch is appended
"the whole table is re-sorted ascending by canonical command name using a
**case-sensitive** byte comparison", and its audit note [R-DOC04-C] says the
sorted identity was **recomputed** from a case-sensitive byte sort of the 68
names rather than dumped from the runtime table.

**Why it was wrong.** Both halves of the pair were traced while following this
section's name lookup. The registration routine appends the batch and then
sorts the whole array with a comparator that is the C runtime's
**case-insensitive** string compare — the same function the binary search below
uses. A case-sensitive sort paired with a case-insensitive `lower_bound` would
be internally inconsistent, and the inconsistency is not hypothetical: over a
case-sensitively sorted table the traced search cannot find `Attack_Chase`,
`Attack_Kamikaze`, `Attack_NoMove`, `AttackSpecial` or `BuildWeapon` at all,
which would disable the chase attack the whole of §3.5 describes.

**What changes.** Everything except the order of seven adjacent entries. Under
a case-insensitive comparison the underscore (byte `0x5f`) sorts **below** every
letter instead of between the upper- and lower-case ranges, so the `Attack*`
and `Build*` clusters invert:

| Identity | §3.1's recomputed name | Traced (case-insensitive) name |
|---:|---|---|
| 6 | `AttackSpecial` | `Attack_Chase` |
| 7 | `AttackUType` | `Attack_Kamikaze` |
| 8 | `Attack_Chase` | `Attack_NoMove` |
| 9 | `Attack_Kamikaze` | `AttackSpecial` |
| 10 | `Attack_NoMove` | `AttackUType` |
| 12 | `BuildWeapon` | `BuildingBuild` |
| 13 | `BuildingBuild` | `BuildWeapon` |

No other identity moves; the empty name still sorts to index 0 and stays the
reject sentinel, and `GetBuilt` is index `0x13` under either order, so the
`GetBuilt`-shaped default identity of code 10 above is unaffected. §3.1's field
census, batch sizes, and per-descriptor payloads are all unaffected — only its
"case-sensitive" sentence and the ordering of those seven rows. **The owner of
§3.1 should carry the correction into its table**; it is recorded here because
the lookup this section describes is where it was traced.

**Established — the lookup itself.** Given a canonical name the engine runs an
ordinary `lower_bound` binary search over the sorted table with that same
case-insensitive comparator and returns the index, or the reject sentinel 0
when the search lands on a non-match. Callers therefore need not match the
table's spelling: the interface transmits `STANDING_FIREORDER` and
`STANDING_MOVEORDER` in upper case and they resolve to the descriptors named
`Standing_FireOrder` and `Standing_MoveOrder` [R-STANCE-01 §2].

### 3.4a Standing orders

Two per-unit fields decide what a unit does *on its own initiative*: the
**standing fire order** (bits 20–21 of the unit state word) and the **standing
move order** (bits 18–19). They are read by the COB ports 2 and 3
[04 §4.4][R-COB-03 §3], written by two order handlers, seeded from the
definition, inherited by factory products [04 §3.8], carried through save, and
overwritten wholesale for computer players by the AI classifier
[08 R-AI-01 §10]. The consumers are enumerated under [R-STANCE-01 §3] and
[R-STANCE-01 §4] in section 3.5.

### Closed — stance values, labels, and the two interface words [R-STANCE-01 §1] (2026-08-29)

**Established — the three values of each field, named from the stock button
artwork.** Both fields hold `0`, `1` or `2` in play. The battle side panel's
two stance buttons are ordinary GUI buttons (`[fmt gui]` id 1) whose *status*
word selects a frame of a GAF entry of the same name, and the engine writes the
stance value straight into that status word, so the frame index **is** the
value. An asset census of the stock `anims/commongui.gaf` entries `ARMFIREORD`
and `ARMMOVEORD` (six frames each, decoded through the retail palette) reads:

| Value | Fire-order button | Move-order button |
|---:|---|---|
| 0 | `HOLD FIRE` | `HOLD POSITION` |
| 1 | `RETURN FIRE` | `MANEUVER` |
| 2 | `FIRE AT WILL` | `ROAM` |
| 3 | `FIRE ORDERS` (mixed selection) | `MOVE ORDERS` (mixed selection) |
| 4, 5 | unlabelled plate | unlabelled plate |

The naming is independently confirmed by behavior: value 0 is the only value
that suppresses every autonomous engagement, value 1 retaliates but never
hunts, value 2 hunts; and on the move side value 1 is the only value that reads
`maneuverleashlength` [R-STANCE-01 §3][R-STANCE-01 §4]. The Cavedog gadgets
are `ARMFIREORD`/`CORFIREORD` (quick key `f`, ASCII 102) and
`ARMMOVEORD`/`CORMOVEORD` (quick key `v`, ASCII 118) in
`guis/<side>gen.gui`; neither carries a `text=` or `stages=` field, so the
labels live only in the artwork.

**Established — the interface keeps its own state, in two different words.**
The panel does not read the unit fields at click time. It keeps a **three-bit**
copy of each stance in engine-root interface state: the fire stance in bits
12–14 of one sixteen-bit word and the move stance in bits 0–2 of the **next**
word — two different words, not two fields of one. *Correction:* the RWU-00-4
string triage recorded both as fields of a single word; the traced handler
reads them from two adjacent words. The same second word also carries the cloak
pair (bits 3–4), the on/off pair (bits 5–6), and the enable bits for the
`MOVE`, `STOP`, `ATTACK` and `DEFEND` buttons (bits 7, 8, 9, 10).

Three bits are needed because the field carries two sentinels the unit field
cannot hold:

* **3 — mixed.** At least two selected units that accept this stance disagree.
* **4 — not applicable.** No selected unit accepts this stance.

**Established — how the panel word is recomputed.** The aggregate
command-state refresh walks the selected units in unit-pool order (selection
bit of the unit state word, non-zero definition index) and, for each unit whose
definition carries the matching accept flag [R-STANCE-01 §5], folds its field
into an accumulator that starts at `4`: the first eligible unit replaces the
`4`; any later unit whose value differs sets the accumulator to `3`; equal
values leave it alone. The accumulator is then deposited into the panel word.

**Established — repaint.** On repaint, a panel value of `4` grays the gadget
(setting only its grayed flag; the status word keeps whatever frame it last
showed), and any other value is written into the gadget's status word and the
gadget is redrawn. Value `3` therefore renders as the generic
`FIRE ORDERS` / `MOVE ORDERS` plate, and pressing a button showing `3` sends
`0` [R-STANCE-01 §2].

**Established — the in-battle developer overlay.** The overlay line
`MOVEORD: %d FIREORD: %d` is printed only while exactly one unit is
single-selected, and it prints **that unit's** two-bit fields — `(state >> 18)
& 3` and `(state >> 20) & 3` — not the panel's three-bit words. *Correction:*
the RWU-00-4 triage read the overlay as naming the panel words.

### Closed — the writer chain from button to unit field [R-STANCE-01 §2] (2026-08-29)

**Established — one click, in order.** The battle panel's stance handler
resolves the pressed gadget by substring match against a fixed chain —
`MOVEORD`, `FIREORD`, `STATUS`, `ONOFF`, `CLOAK` — so the stock names
`ARMMOVEORD`/`ARMFIREORD` match on their suffix. For a stance gadget it then,
in this order:

1. reads the current three-bit panel field and computes the next value:
   `0 → 1`, `1 → 2`, `2 → 0`, **`3 → 0`** (the mixed sentinel cycles to hold),
   and value `4` matches no arm, so a not-applicable field is left untouched —
   the gadget is grayed anyway;
2. **transmits** the command — canonical name `STANDING_MOVEORDER` or
   `STANDING_FIREORDER` — through the ordinary selection broadcast
   [R-STANCE-01 §5] with the new value as the command's parameter;
3. writes the new value back into the panel field;
4. plays the cue `setmoveorders` or `setfireorders` (the on/off and cloak arms
   of the same handler play `specialorders`) and marks the panel dirty.

**Established — the command is authoritative for state; the local write is
display only.** Step 3 touches nothing but the interface word, and the next
aggregate refresh recomputes that word from the selection
[R-STANCE-01 §1]. Every change to a unit's field goes through the order
handler in step 2.

**Established — the two order handlers.** `Standing_MoveOrder` and
`Standing_FireOrder` are ordinary descriptors (state label `Acknowledged`,
class `0x00`, acknowledgement group 19, static gate mask `0x10060` — see
§3.1). Each takes the order's first general parameter, masks it to **two
bits**, and deposits it:

```
move:  state = (state & ~(3 << 18)) | ((param & 3) << 18)
fire:  state = (state & ~(3 << 20)) | ((param & 3) << 20)
```

Both return the completion code 5, so the record is consumed the tick it runs.
Because the panel only ever sends `0`, `1` or `2`, the value `3` is
representable in the unit field but unreachable from the interface.

**Established — the fire handler's extra effect.** When the **unmasked**
parameter is exactly `0` or exactly `1` — that is, on a transition to hold fire
or return fire — the fire handler additionally walks the three weapon slots and,
for every slot whose autonomous-targeting bit (bit 4 of the slot control byte)
is set, clears that slot's stored target: the target id is zeroed, the second
target word is set to `0x8000`, the slot's `StartBuilding` emission is killed,
and `TargetCleared` is raised with the slot index [04 §5.4]. The autonomous
bit itself is **not** cleared. The move handler has no such effect.

**Unknown — the fire handler's parameter test is on the unmasked value.** The
test compares the whole 32-bit parameter against `0` and `1`, while the
deposit masks to two bits, so a parameter of `4` would set the field to `0`
*without* clearing the slots. No producer sends such a value; whether that is
deliberate is undecidable from the trace. *Decider:* none needed for a faithful
clone — reproduce the unmasked test.

### Closed — which units in a selection accept a standing order [R-STANCE-01 §5] (2026-08-29)

**Established — two definition flags gate both the button and the command.**
The FBI keys `mobilestandorders` and `firestandorders` (`[fmt fbi]`) parse into
two bits of one definition flags word: `mobilestandorders` admits the standing
**move** order, `firestandorders` admits the standing **fire** order. They are
read in exactly two places, and both are interface/command sites, never
simulation:

* the aggregate refresh, which skips a selected unit when computing the panel
  field for a stance its definition does not accept [R-STANCE-01 §1];
* the selection broadcast, which — after resolving the command name — skips a
  selected unit when the name is `Standing_FireOrder` and the unit's definition
  lacks `firestandorders`, or the name is `Standing_MoveOrder` and it lacks
  `mobilestandorders`.

**Established — every other selected unit receives it.** The broadcast walks
the *local player's whole unit slice in ascending pool order* at the fixed
unit-record stride and submits to every unit carrying the selection bit. The
broadcast's "leader" exclusion — the single-selected unit, skipped for commands
that need a target — is inactive here, because it is armed only for descriptors
whose static gate mask carries the target-required bit `0x200`, and both
standing descriptors' masks are `0x10060`. The centroid and formation-offset
arm of the broadcast is likewise inactive, because a standing order carries no
ground position. A unit whose definition lacks the matching flag is skipped
even when it is selected, so one click can leave a mixed selection **still**
mixed at the unit level.

### Closed — defaults, factory inheritance, and save [R-STANCE-01 §6] (2026-08-29)

**Established — creation.** Both fields are seeded at unit creation from one
packed definition byte — bits 0–1 for move, bits 2–3 for fire — which the FBI
reader fills from `standingmoveorder` and `standingfireorder`, **each with a
parsed default of 2** [R-COB-03 §3][fmt fbi]. A definition that authors neither
key therefore starts its units at **fire at will** and **roam**.

**Established — factory products.** The product's initial state merge and the
later guarded `GetBuilt` copy are two distinct stages, both owned by §3.8: the
copy from builder to product is allowed only when both units carry the
building-class/alive state bit and neither carries the auto flag, and it copies
bits 18–19 and 20–21 together [R-P0-09][05 "Rally inheritance"]. Nothing in
the standing-order path adds a gate of its own.

**Established — save and restore.** Both fields survive save/load. The unit
save block packs the state word's flag groups into one word in which the
standing-move field sits six bits above bit 18 and the standing-fire field six
bits above bit 20; the restore path rebuilds each group with its own mask from
that word shifted right by six. Save-record layout belongs to doc 08.

**Asset census (stock install, all 284 files the mounted `units/` directory
resolves to, 2026-08-29).** What retail content actually authors, so
the parsed defaults are rarely what a stock unit starts with:

| Key | Authored values |
|---|---|
| `standingmoveorder` | `1` ×168, `0` ×6, `2` ×1; never `3` |
| `standingfireorder` | `2` ×152, `0` ×17; never `1`, never `3` |
| `mobilestandorders` | `1` ×168, `0` ×6 |
| `firestandorders` | `1` ×161, `0` ×14 |
| `canstop` | `1` ×205, `0` ×12 |
| `maneuverleashlength` | `640` ×119, `1280` ×31, `30` ×1, `10` ×1 |
| `attackrunlength` | `100` ×2, `120`, `180`, `220`, `290` (six definitions) |

So a stock mobile unit is authored **maneuver + fire at will**, not the parsed
default **roam + fire at will**; the value `3` is not only unreachable from the
interface, it is unauthored. A census settles what content authors, never what
the engine computes.

**Established — the AI overwrite.** For a computer player the classifier
rewrites both fields of every eligible unit every 30 manager entries: standing
move to `1` (maneuver) when the definition's `cancapture` flag is set and to
`2` (roam) otherwise, and standing fire to `2` (fire at will) unconditionally
[08 R-AI-01 §10]. Any stance a script, a capture, or a save left behind is
overwritten on the next sweep.

### Closed — standing orders and the simulation RNG [R-STANCE-01 §7] (2026-08-29)

**Established — the fields themselves draw nothing.** Neither order handler,
neither definition gate, neither interface path, and no consumer enumerated in
[R-STANCE-01 §3] or [R-STANCE-01 §4] draws from either random stream while
reading or writing a standing-order field.

**Established — but the stance changes the draw *order*.** Five idle handlers
run the opportunity scan of [R-STANCE-01 §3] and take a different exit
depending on whether it issued an order; three of the no-issue exits draw:

| Handler | Scan issued an order | Scan issued nothing |
|---|---|---|
| `Patrol` | clear the gate, set phase 1, return the retry code; no draw | one draw of bound 30, deadline `tick + draw + 30` |
| `Standby` | complete the record (code 5); no draw | one draw of bound 30, deadline `tick + draw + 30` |
| `VTOL_Patrol` | clear the gate, return the retry code; no draw | deadline `tick + 30` **fixed**, no draw |
| `VTOL_Standby` | clear the gate, set phase 0, return the retry code; no draw | return the wait code with no deadline write and no draw |
| `VTOL_SeekAttack` | complete the record; no draw | builds its orbit waypoint (one conditional draw of bound `0x2000` for the orbit angle when the interrupt bits are set), then one draw of bound 30, deadline `tick + draw + 30` |

A stance that admits the scan therefore both consumes the acquisition draws of
[06 §3.2] and, on the ticks the scan succeeds, *skips* the re-poll draw.
Standing orders are consequently RNG-order-relevant even though they draw
nothing themselves, and a clone must evaluate the gates in the traced order.

### Closed — `canstop` has no simulation reader [R-STANCE-01 §8] (2026-08-29)

**Established.** The FBI key `canstop` (`[fmt fbi]`) parses into a third bit of
the same definition flags word as `mobilestandorders` and `firestandorders`.
A census over the whole decompiled function set finds exactly **one** reader:
the aggregate command-state refresh, which sets the `STOP` button's enable bit
in the interface word when any selected unit's definition carries it; the
repaint then grays the `STOP` gadget when that bit is clear. The `Stop` order
handler does not read it, and no simulation path does. `canstop` is a
**button-availability flag only**: an order that reaches the `Stop` handler by
any other route (a script, a hotkey, the AI, a mission trigger) executes
regardless of the key.

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
   word are all clear. *(Corrected by [R-STANCE-01 §3]: the traced gate reads
   the standing-fire field only; the standing-move field is not read in either
   guard handler.)* Per slot 0..2, requiring the slot assigned bit, the
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

### Closed — the standing-fire gates, site by site [R-STANCE-01 §3] (2026-08-29)

**Established — the reader census, and its bound.** A whole-image scan of the
disassembled code for the only two masks that isolate the standing-order fields
(bits 18–19 and bits 20–21), together with every shift-and idiom that could
extract them, finds the readers listed here and in [R-STANCE-01 §4] and no
others. The bound is: a reader that tested one bit of a pair in isolation with a
bare single-bit constant would not be caught, because those constants are also
common 16.16 distances; every site actually found masks the pair.

**Established — the shared auto-engage issuer, and its two gates.** All
autonomous engagement funnels through one issuer, taking `(unit, target,
force)`. Its admission, in order:

1. `unit == target` → refuse;
2. standing **move** field is `0` and `force` is `0` → refuse;
3. standing **fire** field is `0` and `force` is `0` → refuse;
4. resolve command code 3 (attack a unit) through the resolver of §3.4; an
   empty name → refuse.

So **hold position refuses autonomous engagement exactly as hold fire does**:
either zero is enough to stop the unit acting on its own. The `force` argument
bypasses both gates and is passed by exactly one caller family — the two guard
handlers' combat join ([R-UNIT-06 §1] branch 1) — which is what "the queued-mode
attack bypasses the standing-order gates" in that section means at the
instruction level.

On success the issuer builds one or two order records and inserts them with the
**Internal-Auto head insert** of the "goal vtables and queue modifiers"
paragraph below (front or rear segment per the new record's rear flag,
inheriting the displaced head's auto flag). The two-record maneuver form is in
[R-STANCE-01 §4].

**Established — the opportunity scan is the fire-at-will-only path.** A
two-line helper returns a target only when the standing fire field reads
**exactly 2**, in which case it runs the shared unit-level target
search — the order-work acquisition path of [06 §3.2], which builds its
candidate list per call — with its range argument taken from the definition's
`sightdistance` read as a signed 16-bit value; otherwise it returns nothing
without searching. Its callers are the idle/loiter arms of `Patrol`,
`Standby`, `Standby_Mine`, `VTOL_Standby`, `VTOL_Patrol` and `VTOL_SeekAttack`,
each of which feeds the returned target straight into the auto-engage issuer
above with `force = 0`.

**This is the only behavioral difference between return fire and fire at
will.** Both values pass every `!= 0` gate listed here; only `2` opens the scan, and
`0` closes every one of them.

**Established — retaliation ("return fire") is one site in the damage path.**
For every damaged unit the damage-intake path runs one reaction site
[08 R-AI-01 §11]. Read in stance terms, and with the parts that are not
computer-player-specific:

* the victim must be fully built, its definition **armed** or `kamikaze`, its
  owner a controlled player, and the attacker known and not allied;
* it first tries the auto-engage issuer with `force = 0` — so a hold-fire or
  hold-position victim gets no counter-order — and only when the victim has no
  front order (or the front order's gate mask carries the interruptible bit)
  and the attacker's type is in neither the no-chase nor the bad-target
  category set [06 §3.2];
* if no order was issued **and** the standing fire field is non-zero, each of
  the three weapon slots that is present and enabled is offered the attacker,
  subject to the slot's admission predicate.

"Being attacked" is therefore a **per-damage-event edge, not a state**: the
record consulted is the attacker reference the damage packet carries, the
reaction is evaluated once per damage application, and there is no retaliation
timer, latch, or expiry anywhere in the path. A return-fire unit that is hit
once and whose attacker then dies simply stops having a target when the ordinary
retention scan drops it [06 §3.2].

**Established — the guard's slot re-target gates on the fire field alone.**
*Correction:* [R-UNIT-06 §1] step 2 says the slot re-target step is "skipped
entirely when the guard's standing-move bits (18–19) and standing-fire bits
(20–21) of the status word are all clear". The instruction is a test of the
standing-**fire** mask only; the standing-move field is not read anywhere in
either guard handler. The corrected gate is: **skipped when the guard's
standing fire field is zero**. Everything else in that step — the per-slot
assigned/tracking bits, the command-fire-only exclusion, the rebind conditions
— is unchanged, and the air guard is identical.

**Established — the four air-attack handlers.** `AirStrike`, `AirToAir`,
`AirToGround` and `AirToGroundHover` share one epilogue: when the order is
taken down by the abandon/interrupt satisfied-bit combination, the handler
removes it — but first, **if this record is the last one in its queue segment
and the unit's standing fire field is non-zero**, it enqueues a
`VTOL_SeekAttack` record carrying the dead order's target and cached goal. A
hold-fire aircraft whose attack run is interrupted therefore falls idle, while a
return-fire or fire-at-will aircraft enters the seek-attack loiter.

**Established — `Standby_Mine`.** The mine's idle phase calls the opportunity
scan (so it acts only at fire at will), and when the scan returns a target
whose state-word low two bits equal `1` — and, redundantly, the mine's own
standing fire field is non-zero — the mine resolves the canonical name
`SELFDESTRUCT`, allocates an order record with the first general parameter set
to 1, inserts it on itself, and completes the standby record. **Unknown:** what
the target state word's low two bits mean; §2.4 does not enumerate them.
*Decider:* static trace of the writers of those two bits.

**Established — the AI paths.** The computer player's order dispatcher and its
weapon-maintenance sweep both require the standing fire field to read exactly
`2`, which the classifier guarantees for the computer player's own units
[08 R-AI-01 §10][08 R-AI-01 §15]. A structurally identical sibling of the
dispatcher exists with no caller and no code-pointer reference and is dead.

**Established — the bounded negative that matters.** No weapon-slot processing,
aim, reload, shot-admission, or projectile path reads either standing-order
field. Hold fire does **not** disarm a slot: it clears the autonomous slots'
current targets once, at the moment the order runs [R-STANCE-01 §2], and then
suppresses every path that would give the slot a new one. A slot target
installed by a manual order, by a script, or by the guard's forced join is
still aimed and still fired regardless of the stance.

### Closed — the standing-move gates, the chase leash, and the return to post [R-STANCE-01 §4] (2026-08-29)

**Established — maneuver is the two-record form of the auto-engage issuer.**
When the standing **move** field reads exactly `1` and `force` is `0`, the
issuer builds *two* records instead of one, and because both go in through the
head insert the resulting queue order is **attack first, then the return move**:

1. resolve command code 2 (move) with the goal set to the unit's **own current
   position**, and head-insert that record with no leash and no anchor;
2. resolve command code 3 (attack) and head-insert that record with
   * the third general parameter — the pursuit leash — set to the definition's
     `maneuverleashlength` read as an unsigned 16-bit value, and
   * the order's guard/fight anchor pair set to the unit's own position in
     **whole world units**: the high halves of the 16.16 X and Z, stored as
     two signed 16-bit values.

For any other move value — and for every forced call, including a maneuver
unit's forced guard join — the issuer builds the single attack record with
**leash 0 and no anchor**. Leash `0` means unlimited (§3.2). So:

| Standing move | Autonomous engagement |
|---|---|
| 0 hold position | refused outright |
| 1 maneuver | chase, leashed to `maneuverleashlength` from where the unit stood, then a queued move back to that spot |
| 2 roam | chase, unlimited, no return move |
| 3 | behaves as roam (unreachable from the interface) |

**Established — how the leash is enforced.** The `Attack_Chase` handler tests
it before its phase switch, on every dispatch, and only when the leash is
non-zero:

```
dx = (int16)unitX_wholeUnits - (int16)order.anchorX
dz = (int16)unitZ_wholeUnits - (int16)order.anchorZ
d  = trunc(hypot((double)dx, (double)dz))       // C hypot, __ftol truncation
if (leash <= d) -> abandon the order (completion code 5)
```

Both terms are whole world units, the subtraction is on sign-extended 16-bit
values, the distance is a double `hypot` truncated toward zero by the standard
float-to-long conversion, and the comparison is **inclusive** (`leash <= d`
abandons; `d == leash - 1` continues). This refines the "strict `leash <=
distance from the guard/fight anchor` removal" sentence of §3.5 with the exact
widths and the truncation. Nothing rounds. When the record is abandoned, the
return move queued behind it becomes the front order — that is the whole of the
"return to post" behavior; there is no separate homing state.

**Established — the repair patrol has its own three-armed issuer.** A second
issuer, used only by `RepairPatrol` and `VTOL_RepairPatrol`, resolves command
code 8 (assist or repair) and branches on the standing **move** field with
three arms instead of two — and its arms are *not* the same as the attack
issuer's:

| Standing move | Records queued (head insert, so this is the execution order) | Leash source |
|---|---|---|
| 0 hold position | assist, then a move back to the unit's own position; anchor written | definition `sightdistance`, read as a **signed** 16-bit value |
| 1 maneuver | assist, then a move back to the unit's own position; anchor written | definition `maneuverleashlength`, unsigned 16-bit |
| 2 roam | assist only | 0 (unlimited) |
| 3 | nothing is queued; the issuer refuses | — |

A non-zero `force` argument makes this issuer refuse in every arm. Note that,
unlike the attack issuer, **hold position does not refuse here**: a
hold-position repair patrol still leaves its post to assist, bounded by its own
sight distance.

**Established — `attackrunlength` is not part of this machinery.** A whole-image
reader census of the definition field the FBI key `attackrunlength` fills finds
exactly four readers: two inside the `AirStrike` handler (where it extends a
computed approach distance by a whole-unit addend), the definition-clone
routine, and the HUD's `attack length` range ring [07 §6]. No standing-order,
chase, or leash path reads it. The leash of the chase attack is
`maneuverleashlength` only.

**Established — the two ring overlays name the same fields.** The HUD's
labelled range rings read `maneuverleashlength` for the ring labelled
`maneuver` and `attackrunlength` for the ring labelled `attack length`, from
the unit definition, with no stance test [07 R-P0-11 §3].

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

### Closed — the product is cargo: attach at allocation [R-FAC-02 §1] (2026-08-29)

This closure and the six that follow are the RWU-04-10 pass over factory
egress. They re-read the factory handler's state-2 epilogue, the attach/detach
commit it calls, the occupancy commit's carried branch, the completion
transition, the product-side queue, and the placement validator's mode
argument, and they re-verify the candidate claims of the unmerged
`ota-fac-01d` / `ota-fac-01e` branches (verdicts in §7). Every claim is a
direct static trace unless a sentence says otherwise. Vocabulary is that of
[R-COLL-01 §1] (*cell*, *ground word*, *self identity*, *size pair*, *cached
cell pair*), of [R-ORD-01 §1] (record fields, deadline setter, goal
installers) and of §3.3 (the pump).

**Established — the product is attached to the factory as cargo, in the same
handler visit that allocates it.** [R-ORD-01 §5] recorded the phase-2 step
"attach it as cargo-style carried product" without a contract; this is it.
After the nanoframe is created (mode 1, stamped in the ground plane at the
resolved exit position — [R-COLL-01 §4] "writer census"), the factory handler
calls the shared **attach/detach commit** that transports use ([R-COB-03 §5])
with the factory as carrier and the `QueryBuildInfo` piece index as the hang
piece. The commit's gates are: the cargo is alive and **not building-class**
(flags bit 29 clear — a product whose definition has `bmcode 0` is never
attached and simply stands where it was allocated); the cargo carries nothing
itself; the carrier is alive, is not the cargo, and is not itself carried. It
then builds a seven-byte kind-10 session event `{kind 10, cargo identity,
carrier identity, piece, mode}` with piece = the exit piece index truncated to
one byte and mode = 1, submits it to the session stream, and applies it
locally at once:

1. the cargo is unlinked from its sector bucket (it is no longer in any
   bucket; the overlap scan of [R-COLL-01 §4] reaches it through the
   carrier's cargo list);
2. the cargo's **hang-piece byte** is set to the event's piece byte, its
   carrier pointer to the factory, and it is pushed onto the front of the
   factory's cargo list;
3. flags bit 17 is set iff the piece byte is `0xff` (the detach sentinel) —
   so it is **clear** for a factory product;
4. the cargo mover's mode bits (state byte bits 0–1) are set to the event's
   mode, i.e. **1, grounded**, for every product including aircraft;
5. if the owner is in player state 1 or 2 and the carrier's definition lacks
   `isairbase` (capability bit 9 — the FBI key's only reader on this path), the
   cargo's primary queue is **flushed** — every record whose static mask lacks
   bit 2 (`0x4`, §3.1) is unlinked and freed — and a `BeCarried` record is
   inserted at the **head** of its primary queue. A factory has no
   `isairbase`, and a freshly allocated product's queue is empty, so the net
   effect is exactly one `BeCarried` at the head. (`isairbase` carriers skip
   this, which is how a landed aircraft keeps its orders.)
6. the cargo's selection bit is dropped under the sweep's selection predicate
   (presentation only).

The factory handler then copies its standing bits 18–21 onto the product and
inserts `GetBuilt` **queued** ([R-P0-09]), so the product's primary queue at
the end of the visit is, front to back, `[BeCarried, GetBuilt]`. The
`BeCarried` record is the release lane that [R-FAC-01] and [R-FAC-01B §1]
could not find: it is not a factory-owned node and it is not in the factory
handler — it is a side effect of the attach event.

**Established — the piece byte is signed on the read side.** The commit's
carried branch and the orientation copy (§2) read the hang-piece byte back as
a **signed** 8-bit value. An exit piece index of 128 or more therefore reaches
the locator as a negative index, which returns the zero offset ([R-REV-02]):
such a product hangs at the factory origin, not at its pad. Piece 255 is
indistinguishable from the detach sentinel. Stock models are far below this
bound ([R-FAC-01B]); the edge is recorded because an implementation that
widens the byte would place non-stock content differently from retail.

### Closed — where the nanoframe sits, every tick [R-FAC-02 §2] (2026-08-29)

**Established — the carried branch owns the product's position, orientation
and velocity while it is under construction.** The unit sweep runs both order
pumps and the mover tick for every live unit of a state-1/2 owner with no
carried-or-unfinished exemption, so the nanoframe's mover tick runs from the
tick after allocation. Its commit ([R-COLL-01 §1] "the carried branch") does,
per tick:

```text
hang     = factory.xyz + locate(factory, hangPiece)     # [R-REV-02] locator,
                                                         # the state-2 target formula
if product definition is a floater (capability bit 19):
        hang.y = max(hang.y, (waterline·65535 + seaLevel) << 16)   # [R-AIR-01 §9]
carriedPositionSetter(product, hang, mover.mode)         # [R-COLL-01 §4]
product.roll    = factory.roll    + a[0](hangPiece)      # 16-bit wrapping adds
product.heading = factory.heading + a[1](hangPiece)
product.pitch   = factory.pitch   + a[2](hangPiece)
product.velocity, speed = factory mover's, or 0          # a building has no mover
transform-dirty cleared
```

`a[i](hangPiece)` is the hang piece's **own** runtime angle triple, indexed as
in [R-REV-02] (`a[0]` about Z, `a[1]` about Y — the heading — `a[2]` about
X); unlike the position, the orientation copy walks no hierarchy and folds no
ancestor. A product's release heading is therefore the factory's heading plus
whatever the script has turned the pad piece by — rotated factory variants
release rotated products through the same fold [R-REV-02] applies to the
position.

The carried-position setter is the commit's success branch without the
validator: same cell and mode → write XYZ only; otherwise clear the old
footprint, write XYZ, cell pair and mode, **stamp** the new footprint with the
overlap protocol, and publish LOS. A nanoframe therefore holds its ground-word
footprint at the pad for the whole build, and follows an animated pad piece
cell for cell. Because the position is rewritten from the factory every tick,
nothing the steering step computed for the product survives; a product cannot
drift, be pushed, or be teleported while carried.

### Closed — release is the detach at completion [R-FAC-02 §3] (2026-08-29)

**Established — the completion transition detaches the product, in this
order.** The shared work helper's zero-remaining call and the factory's
state-4 re-invocation both run the completion transition ([R-P0-09]); its
body, at implementable precision:

1. clear the product's build/weapon auxiliary field ([R-P0-09]); remaining
   fraction := 0.0; flags bit 13 (complete) set;
2. if the owner is in state 1 or 2: a **non-building** product with a carrier
   is detached by the attach/detach commit with carrier null, piece `0xff`,
   mode 1; a building-class product instead refreshes the builder GUI;
3. capability bit 18 → raise the product's `Activate` edge;
4. the local player's queue-count label refresh;
5. capability bit 24 → cloak/initial-posture byte 7 and flags bit 14;
6. owner in state 1 or 2 → the kind-18 builder/product link event
   `{kind 18, product identity, builder identity}` to the owner's event sink;
7. either unit selected → HUD dirty.

The detach applies the kind-10 event with carrier null: the product is
unlinked from the factory's cargo list, its hang-piece byte becomes `0xff`,
flags bit 17 and the carrier pointer are cleared, it is pushed onto the front
of its **current** sector bucket (the bucket its committed position selects,
[R-COLL-01 §4]), and its mover mode is set to 1. **No clear, no stamp and no
position write happen at detach**: the product keeps the cached cell pair and
the ground-word footprint the carried setter last wrote, at the pad, with the
orientation of §2. The second invocation from state 4 finds no carrier and
skips step 2; the other steps re-run idempotently as [R-P0-09] already says.
The `StopBuilding` falling edge precedes that second call ([R-P0-09]).

**Established — the two abnormal ends.** Cancel-current ([R-ORD-01 §5]) runs
the same completion transition — so the product is detached — and then kills
it with damage cause 9. A factory that dies or is freed runs unit
finalisation, which kills every unit on its cargo list with 30000 damage
(cause 3 when the death record's kind nibble is 3, else cause 6 — doc 06 owns
the cause table) and detaches each; a dying factory therefore never leaves a
free-standing nanoframe on its pad, and a dying carried product is detached
before its own finalisation continues. A product freed by pool exhaustion or
limit never existed (the 300-tick retry of [R-P0-09]).

### Closed — the order form after release, and its latency [R-FAC-02 §4] (2026-08-29)

**Established — the product's first order is `BeCarried`, its second is
`GetBuilt`, and the rally or `Park` is appended by `GetBuilt`.** Neither the
factory nor the product installs any goal before `GetBuilt` runs; the
ota-fac-01d claim "no factory or `GetBuilt` writer installs a ground goal
before the inherited rally" is confirmed for goals, but the queue is not empty
before the rally — it holds the two records above.

`BeCarried` ([R-ORD-01 §2]): carrier null → complete; phase 0 releases the
slots and advances; phase 1 sets deadline 10 and holds. `GetBuilt`
([R-ORD-01 §5]): while the remaining fraction is non-zero, phase 0 sets
deadline 300, phase 1 deadline 30, phase 2 (on its own expiry) deadline 11
plus the negative work step; each of those arms ORs `0x8000` into the gate.
When the remaining fraction is 0.0 on **any** visit, whatever the phase, it
refreshes the builder interface flag and, if the product has a mover and the
record's builder reference is live, walks the builder's primary queue from
the front: a record whose name is `QMove` is resolved as command 2 (move) and
one named `QPatrol` as command 9 (patrol) against the product with the
record's goal triple and inserted **queued** on the product, in walk order;
then the standing-bit copy under the bit-28 / bit-14 guard of §3.8 (with the
experience word for a computer-owned builder); then, if nothing was inserted,
`Park` is inserted queued. It returns complete (5). A product without a mover
(a building-class product) gets nothing.

**Established — the pump gates that set the handoff latency.** The primary
pump (§3.3) stops its walk at the first record whose gate is non-zero and
whose satisfied set is empty; a *hold* (code 2) does **not** stop the walk —
the next record is visited in the same pass. The satisfied set is the
record's pending bits ORed with the unit's 16-bit pending word, and the only
writers of that unit word in the whole export OR in bit 2 (the COB set-port
paths of §4.7); nothing ever raises bit 0 or `0x8000` there. Consequently
`GetBuilt` is woken **only by its own deadline**, and:

- while carried, `GetBuilt` runs only on a tick on which `BeCarried`'s 10-tick
  deadline has just expired **and** its own deadline has expired (the walk
  otherwise stops at `BeCarried`);
- after detach, `BeCarried` completes on its next expiry — at most 10 ticks
  later — is unlinked, and the walk continues to `GetBuilt`, which still runs
  only when its own deadline has expired: at most 300 ticks after its phase-0
  visit, 30 after phase 1, 11 after phase 2. The rally or `Park` is appended
  on that visit and, being queued behind a record the pump has just freed, is
  dispatched in the **same** pass (codes 5/8 continue the walk).

Composing the two direct traces: from the attach tick `t0`, `BeCarried`
expiries fall on `t0 + 1 + 10k`, `GetBuilt`'s phase-0 and phase-1 deadlines
on `t0 + 301` and `t0 + 331`, and its phase-2 decay visits every **20** ticks
from `t0 + 351` (an 11-tick deadline consumed on the next 10-tick `BeCarried`
expiry). A product whose build finishes inside the first 300 ticks after
attach therefore stands complete on the pad until `t0 + 301` before it
receives any movement order; one finishing later waits at most 11 ticks plus
the `BeCarried` alignment. This composition is Established from the two
traces; the observable pad dwell it predicts is the natural retail
confirmation and is listed in §8. It also answers, for factory products, the
[R-ORD-01 §5] question "what suppresses this decay while a builder is
working": nothing — the nanoframe decays by `11 / buildcostenergy` of its
remaining fraction on every 20-tick decay visit from `t0 + 351`, in
competition with construction, until completion.

**Established — the release target.** With a rally, the product's release
target is the rally's own goal triple in the resolved move/patrol handler
(§8.3, [R-ORD-01 §4]) — the factory contributes no offset. Without one, `Park`
([R-ORD-01 §2]) installs a **rectangle goal** centred on the product's own
committed cell: with `s = FootPrintX` (from the unit's size pair) plus 3 when
the product's movement-class `MinWaterDepth` word is non-negative, the
rectangle's origin is `(cellX − 4s, cellZ − 3s)` and its size `(8s, 6s)` cells,
where `cellX/Z = position >> 20` (whole units to cells, arithmetic shift, no
footprint bias); gate `0xE0`; the ground path search treats it as a
rectangle-perimeter goal (§7.2: the admissible cells are exactly the
rectangle border, and arrival requires lying on it). A ground product with no
rally therefore starts at the centre of an `8s × 6s`-cell rectangle and walks
to its nearest border cell — at least `3s` cells (`48·s` world units) from
its pad — and completes on arrival, or as soon as any record is queued behind
it. This border walk is what carries a no-rally product off its factory; it
is not an egress offset and the factory contributes nothing to it. **Note for
§3.9's owner:** [R-ORD-01 §2] describes the `+3` condition as "the
definition's yard-map width word"; the word read is the movement-class
`MinWaterDepth` (template default −10000, so land classes get no `+3`; ship
classes with a non-negative minimum depth do).

**Established — aircraft.** For a `canfly` product, `Park` sets the goal to
the product's own position and re-identifies itself as `VTOL_Move` with a
*restart*, so the air move handler runs in the same cascade ([R-AIR-01 §6]).
The aircraft leaves the pad through the ordinary velocity-limited climb of
§10.1 from mode 1 (the detach sets grounded). There is no factory-specific
takeoff state; the ota-fac-01e claim to that effect is confirmed. What the
climb's first-goal geometry is for a zero-length move is §10's contract, not
this section's.

### Closed — producer/product collision: the yard map is the exemption [R-FAC-02 §5] (2026-08-29)

**Established — the state-2 validator receives mode 1.** The factory handler
passes its own flags-word mode mirror as the validator's mode argument
([R-ORD-01 §5]). The allocator sets that mirror from its seventh argument and
**every** allocation call site in the export (eleven) passes the literal 1
— the build handlers, the mission spawner, the commander respawn, the unload
and transfer paths — except the save loader, which passes the saved mode bits
(doc 08). A building-class unit never
owns a mover (the allocator constructs one only for `bmcode 1`), and every
other writer of the mirror is a mover-side path, so a factory's mirror stays
at 1 for its whole life. The validator's mode-1 arm is therefore the one that
runs at the exit ([R-COLL-01 §2]): the per-cell ground-word test with self
identity 0, the feature-blocking test, and the depth/slope gates against the
**product's** definition. This closes the "caller-mode value the placement
validator receives at the factory exit-spot call" item that [R-P0-08-A §1]
and the tail carried as `TODO(question)`, in the direction opposite to that
paragraph's hope: the depth and slope gates **do** run at the exit, per cell,
against the product's definition (a Nanolathe placement query that skips them
at factory exits diverges from retail — see §7).

**Established — no producer/product exemption exists; the yard map does the
work.** With self identity 0, *any* non-zero ground word blocks the state-2
test, including the factory's own building stamp. A factory stamps yard cells
`o`, `f`, `w`, `G` always, `c`/`C` only while its yard is **closed**, and `O`
only while it is **open** ([R-COLL-01 §4]). So the exit footprint validates
only when every cell under it is unstamped — `Y`, `y`, `.` cells, or `c`/`C`
cells released by a yard-open port write (§4.7 port 18) before phase 2. The
factory handler itself never touches the yard; the script does, and the
`INBUILDSTANCE` wait of phase 1 is where a stock script's yard-open lands
(whether every stock factory script opens its yard before entering the build
stance is authored data, listed in §8). A factory whose pad cells are `c`
with its yard closed at phase 2 retries silently every 15 ticks until the
script opens it — the "blocked exit" retry of [R-P0-09] with no timeout.

Once allocated, the product holds those cells itself (creation stamp, then
the carried setter, §2). The factory's later stamps meet it through the
**overlap protocol** of [R-COLL-01 §4] — host/intruder bits, the cell keeps
the product — and never fail. When the product finally commits a cross-cell
move, its clear runs the overlap scan, and the factory's restamp reclaims any
released cell its current yard state selects. Nothing compares the producer
and product identities anywhere on this path; the ota-fac-01d claim "no
reviewed writer compares producer and product handles" is confirmed, and the
`TODO(question)` that asked for an exemption is closed negatively.

**Established — the yard-state admission gate.** The yard-open port write is
refused, with nothing written and no restamp, unless the unit's cached cell
pair is positive on both axes, the footprint is inside the map, and **every**
cell that the *requested* state would select (bit 1 or bit 8 of the yard byte
for open, bit 2 for closed) holds a ground word that is 0 or the factory's
own identity. A factory therefore cannot close its yard while a released
product still stands on a `c`/`C` cell, and cannot open it while a foreign
unit stands on an `O` cell; the script's `set YARD_OPEN` is silently ignored
in that tick and whether it retries is authored behavior.

### Closed — no-stacking and the blocked exit [R-FAC-02 §6] (2026-08-29)

**Established — two products never stack, and the mechanism is the validator
seeing the first product's stamp.** The counted node serializes products
(state 0 → 1 → 2 per product, [R-P0-09]); a second product's phase-2 test runs
only after the first has been detached (state 4 → restart) and re-enters the
`INBUILDSTANCE` wait. By then the first product holds the ground words of its
footprint at the pad (§2, §3). The second test, with self identity 0, finds a
non-zero word and fails → deadline 15, silent, no counter, no timeout. It
succeeds on the first 15-tick visit after the first product's cross-cell
commit has cleared those cells — or, for an aircraft product, after its mode
change to airborne has moved its stamp to the air plane ([R-COLL-01 §4]). A
product that never moves (no path, `I can't get there`, a `Park` it completes
in place because a queued record sits behind it) blocks its factory
indefinitely; retail has no push, no stacking, no force-placement and no
allocation elsewhere. The [R-FAC-01B §5] concern that "occupancy is published
later in the tick and … a second same-pass allocation" could stack is moot:
the first product is stamped in the allocation call itself, before the
handler returns.

**Established — a blocked released product is an ordinary blocked mover.**
After detach the product is a mode-1 ground unit at the pad with a goal (§4).
A foreign unit on its route is met by the commit's blocked branch — the
half-`MaxVelocity` speed cap and the half-cell clamp around the old
footprint centre ([R-COLL-01 §1]) — and by the follower's 60-tick repath
throttle ([R-MOV-01 §7]); an empty route publishes `I can't get there`
([R-COLL-01 §6]). There is no release-specific wait, retry cadence or timer.
While the product stands on `c`/`C` cells the factory's yard cannot close
(§5); while it stands on any exit cell the next product cannot be allocated
(above). Those two gates are the whole of retail's blocked-release policy.

### Corrections and branch verdicts [R-FAC-02 §7] (2026-08-29)

* [R-FAC-01] said "No dedicated post-allocation producer/product exemption or
  blocked-release retry was recovered. Whether a later movement path permits
  that overlap, and what an indefinitely blocked release does, are
  **Unknown**." Closed: there is no exemption (§5); the product is carried
  and holds its own cells; an indefinitely blocked product blocks the factory
  (§6).
* [R-FAC-01] and [R-FAC-01B §1] said no release lane, clearance segment or
  additional order exists "within the reviewed factory-handler call chain".
  True of the handler; wrong as a conclusion — the attach event inserts
  `BeCarried` at the head of the product's queue (§1), and that record's
  10-tick gate sets the handoff latency (§4).
* [R-FAC-01B §2] "Producer/product collision exemption — Unknown after
  allocation" → closed negatively (§5).
* [R-FAC-01B §3] "indefinite blocking of that hypothetical lane and any
  force-release policy remain Unknown" → closed (§6): no force release.
* [R-FAC-01B §5] and [R-REV-02] "A same-pass no-stacking guarantee remains
  Unknown; its decider is a blocked multi-product trace" → closed by static
  trace (§6); the retail trace is now confirmation, not decider.
* The `TODO(question)` under [R-FAC-01B] asking for a retail capture "to
  distinguish a hidden release lane from ordinary `GetBuilt`/VTOL handling"
  is closed for the ground and rally questions; its aircraft-ordering part
  survives in §8.
* [R-P0-08-A §1] (§6.4, Supported inference) explained stock production by
  "finished buildings never write the stomp shorts" and left the exit mode
  value `TODO(question)`. [R-COLL-01 §8] already withdrew the first half
  (the building stamp writes the ground word). The mechanism is §5: the
  exit's `c`/`C` cells are released by the yard-open port write before phase
  2 runs, and the mode is 1, so the per-cell terrain gates run at the exit.
  Nanolathe's `PlacementQuery.SkipTerrainAggregates` at factory exits is
  therefore a divergence to fix forward (cross-doc need; §6.4's owner).
* `ota-fac-01d` (doc 05 text): "no factory or `GetBuilt` writer installs a
  ground goal" — confirmed for goals; "no reviewed writer compares producer
  and product handles" — confirmed; "no release state" — superseded by the
  `BeCarried` record it did not see; "product-side link cleanup Unknown" —
  not traced here, still open.
* `ota-fac-01e` (doc 04 text): "the locator reads no unit heading and does
  not apply a factory-heading rotation" — **refuted**; the locator folds the
  unit's three orientation words into the root node's angles before rotating,
  as [R-REV-02] on `main` already records (re-verified here, and the same
  fold is used by the orientation copy of §2). "No factory-specific takeoff
  state before `GetBuilt`" — confirmed (§4). "Runtime piece-angle
  initialization for rotated variants Unknown" — not traced here.

### R-FAC-02 §8 — what this unit leaves open

* Whether every stock factory script opens its yard before it enters the
  build stance (so that the state-2 test never idles on the factory's own
  `c`/`C` stamp) · authored data · decider: a census of the stock factory
  COBs' `Activate` / `StartBuilding` bodies for the yard-open port write.
* The aircraft product's first-goal geometry and climb after `Park` →
  `VTOL_Move` at its own position · §10 · decider: the `VTOL_Move` zero-length
  goal path in [R-AIR-01 §6] read for a grounded start.
* The lifetime and cleanup of the product-side builder link after `GetBuilt`
  (the kind-18 event's consumer) · §3.8 · decider: trace the event sink's
  kind-18 handler and the unit finalisation's reference walk.
* Retail confirmation of the composed handoff latency of §4 (a build finishing
  under 300 ticks after attach dwells on the pad until tick 301) · §3.8 ·
  decider: one timed retail observation; the static composition stands
  regardless.

### 3.9 Order handler bodies, per visit [R-ORD-01]

This section gives every ground and generic order handler in the descriptor
table (§3.1) a per-visit contract: the pre-checks it runs before its phase
switch, what it validates about its target and goal, the distance test it
applies, what it writes to the unit and to its own record, the code it
returns, the COB callbacks it arranges, every simulation-RNG draw, and every
retail diagnostic string it emits. The pump's mapping of return codes is in
§3.3 and [R-ORDER-02 §1] and is not restated: *complete* below means code 5,
*abandon* 8, *cancel-all* 7, *wait* 3, *re-arm* 9, *rotate* 6, *hold* 2 or 4,
*advance* 1, *restart* 0. Handlers already closed elsewhere are cited, not
re-derived; where a trace contradicts existing text the contradiction is
written as a Correction quoting the old text. The air-only handlers of batch 3
are covered by [R-AIR-01 §6–§9] and §10.2–10.3; the five VTOL work twins are
[R-ORD-01 §7] below (2026-08-29, RWU-04-4). Everything in
this section is **Established** by direct trace of the handler bodies unless a
sentence says otherwise. (2026-08-29, RWU-04-11.)

### Closed — the shared vocabulary every handler body uses [R-ORD-01 §1] (2026-08-29)

**Record fields.** The handler receives the owning unit, its order record, and
the satisfied combination (§3.3 step 4). It reads and writes the record fields
of §3.2: the phase byte, the dynamic gate, the deadline, the target
smart-reference, the goal triple, the anchor pair, the three general
parameters (called *p1*, *p2*, *p3* below), the static-mask copy, the pending
word, and the goal payload. The record constructor zeroes the dynamic gate and
the pending word, so a freshly inserted record is dispatched on its very next
pump visit with an empty satisfied set.

**The RNG draw.** Every draw below is the simulation RNG (§1.2) taken as
`state mod n`. The draw helper **returns 0 without advancing the state when
`n` is below 2**; a handler that computes `n` from a distance or a list length
therefore consumes no random state when that value collapses to 0 or 1. A
reimplementation must reproduce this or its draw sequence diverges.

**The deadline setter** stores `current tick + n` and ORs bit 0 into the
dynamic gate; every "deadline n" below implies that OR (§3.3 step 1 then
delivers bit 0 on expiry).

**The caption clear.** A record whose static-mask copy carries the runtime
caption-pending bit (§3.2) has it cleared by a one-shot helper that also emits
status kind 5 (`ok`) on the owner with an optional text; handlers call it with
no text ("caption clear") or with a state text ("caption clear with
*Repairing*").

**The status emitter** takes the unit, a status kind, and an optional text. It
does nothing unless the unit belongs to the local player, carries the
alive/building-class bit 28, and does **not** carry the auto flag bit 14. When
no text is given it substitutes the kind's default display text from a fixed
table of 23 kinds; kinds with no default text emit no caption (the kind still
reaches presentation, which owns the sound side — doc 07). The table, kind →
(sound name, default text), verbatim:

| Kind | Sound | Default text | Kind | Sound | Default text |
|---:|---|---|---:|---|---|
| 1 | `select` | — | 13 | `unload` | — |
| 2 | `underattack` | `Under Attack` | 14 | `cloak` | `Cloaked` |
| 3 | `activate` | — | 15 | `uncloak` | `Visible` |
| 4 | `deactivate` | — | 16 | `capture` | — |
| 5 | `ok` | — | 17 | `count5` | `five` |
| 6 | `arrived` | `Arrived` | 18 | `count4` | `four` |
| 7 | `cant` | `Cannot Comply` | 19 | `count3` | `three` |
| 8 | `unitcomplete` | `Nanolathe Complete` | 20 | `count2` | `two` |
| 9 | `build` | — | 21 | `count1` | `one` |
| 10 | `repair` | — | 22 | `count0` | `zero` |
| 11 | `working` | — | 23 | `canceldestruct` | `Self destruct terminated` |
| 12 | `load` | — | | | |

Every explicit text a handler passes is quoted verbatim in its contract below
(including the trailing periods and the misspelling that retail ships).

**Goal installers.** Four helpers install the record's goal payload: a
**point** goal at a position with an arrival radius, an **annulus** goal at a
position with outer and inner radii, a **rectangle** goal from a packed
footprint-cell origin and packed cell size, and a payload release. All four
first release the previous payload (raising pending `0x80`, [R-ORD-01 §0]),
skip the install entirely — release only — when the owner's definition has
the `canfly` bit, and finish by clearing pending bits `0x20`–`0x200`. The
ground goal-handle arithmetic behind the point goal is [R-P0-01] §8.3; the
rectangle goal's cell origin is computed by the footprint snap below.

**The footprint snap.** A unit's committed footprint cell for an axis is
`(pos − foot·2^19 + 2^19) >> 20` (arithmetic shift) where `foot` is the
definition's footprint size in cells for that axis (`FootPrintX`,
`FootPrintZ`; the unit keeps a copy of the pair), i.e. the cell containing
the footprint's minimum edge. The reverse, used to centre a goal on a
footprint, is `pos = (foot + 2·cell) · 2^19`.

The unit's copied pair is a **size**: the unit constructor copies the
definition's footprint-size pair into it, and every consumer (the placement
snap, the reach tests, `Park`'s parking rectangle, the guard's follow radius)
reads it as cells of footprint. The committed cell pair — "the packed
half-cell footprint bias" of §8 — is the separate field the occupancy commit
writes.

**The reach test** shared by `MobileBuild`, `RepairUnit`, `Capture`, and the
reclaim family is, in whole world units, `trunc(hypot(dx, dz)) −
trunc(8·hypot(myFootX, myFootZ)) − trunc(8·hypot(theirFootX, theirFootZ))`
compared with the definition's `builddistance`; `dx`/`dz` are the 16.16
centre differences, the hypot is computed in double precision and truncated
toward zero, and the whole-unit part of the first term is taken by shifting.
"In reach" is `≤ builddistance`; the two footprint terms are the units'
(for a mobile build, the product definition's) footprint sizes. `ReclaimUnit`
uses a different test, given in its contract.

**Weapon-slot helpers.** *Inhibit slot k* sets the slot's control-byte bit 4
and clears its target; *release slot k* clears that bit and clears the
target; both fire `TargetCleared` under the guard of [R-ORDER-02 §2] and take
`k = 3` to mean all three slots in order 0, 1, 2. *Bind slot k to unit* stores
the target's unit id word with the unit-companion marker and clears bits
10–14 of the unit's slot-status word; *bind slot k to position* stores the
whole-unit X and Z (a Z of exactly −32768 is nudged to −32767 so it cannot
read as the empty sentinel) and clears the same bits. *Read slot k target*
yields the unit only while the companion carries the unit marker and the id
is nonzero. The default slot pick returns 0 when slot 0's control byte has
bit 1 set, else 1 when slot 1's has it, else the value of slot 2's bit 1.

**The head insert.** A handler that spawns a new record inserts it at the
**front** of the segment the record's rear-segment flag selects — the new
record becomes the head and the old head becomes its next link, inheriting
the old head's auto flag `0x4000` — so the spawned order runs before the
spawning one resumes. The insert helper is invoked even when the record
allocation failed, with a null record, and dereferences it unconditionally;
retail relies on the record pool never being exhausted at these sites.

**The `INBUILDSTANCE` wait.** The work handlers share a helper that returns
*advance* (1) when the unit's build-stance byte (the COB `INBUILDSTANCE`
port, [R-COB-03 §3]) is set, and otherwise writes the dynamic gate to
`extra | 0x4` and returns *hold* (2). The `extra` per caller is given below.

**The `StartBuilding` emitter** is [R-ORDER-02 §2]'s: it arranges
`StartBuilding(0, 0, 1, value16, 0, 0, 0)` and sets the StopBuilding-pending
flag. **Correction to [R-ORDER-02 §2]:** that closure said `value16` "is the
low 16 bits of the issuing record's identity (Supported inference)". Every
call site passes `bearing(unit → target) − unit heading` as a 16-bit angle —
the build heading relative to the unit — which is the value the 49 stock
scripts consume as an angle ([R-UNIT-06 §4]); the record identity is not
involved.

**The spray effect.** Work handlers draw the nanolathe spray from the
owner's `QueryNanoPiece` result (the piece's world position, resolved
through the script's query port) to the target's bounding box
(position plus the definition's model minimum and maximum triple, or, for a
feature, its footprint box), effect kind 6. It is presentation only (doc 03).
The unit's *nanolathe-active stamp* (a tick value on the unit) is written
beside it — `tick + 150` by the repair pair, `tick + 300` by the build and
feature-reclaim family, `tick + 900` by `Capture` and `ReclaimUnit` — and is
read by presentation, not by any handler.

**The work-amount seed** used by the reclaim family with a scale `k` is
`max(1, trunc(workertime · ((experience + 5) / 5) · targetMaxDamage · k /
(max(targetBuildCostMetal, 10) · 300)))` with the division and the `/ 5`
integer, the product formed in 64-bit, and the final quotient a float
truncated toward zero.

### Closed — the trivial, standing, and wait handlers [R-ORD-01 §2] (2026-08-29)

| Handler | Contract |
|---|---|
| `Stop` | Caption clear; clear the three weapon-slot targets unconditionally (the unconditional entry of [R-ORDER-02 §2]); if the unit's committed mover mode is **airborne** (`2`, [R-MOV-01 §8]) and its definition has `canfly`, spawn `VTOL_LandIfCan` (target none, goal = own position, p1..p3 = 0) at the head. Complete. |
| `MakeSelectable` | Clear state-word bit 15 and set bit 5. Complete. |
| `Activate` / `Deactivate` | If the definition has `onoffable`, raise / lower edge bit 0 of the unit's edge byte (which arranges `Activate` / `Deactivate` and emits status 3 / 4, [R-UNIT-06 §2]). Complete either way. |
| `Cloak_On` / `Cloak_Off` | If the definition's capability word has the *can-cloak* bit — derived by the FBI parser as `cloakcost > 0`, not from `init_cloaked` — set / clear state-word bit 11. Complete either way. No callback, no caption: the *Cloaked* / *Visible* captions come from the edge machine's bit 2, which this handler does not touch. |
| `Standing_MoveOrder` / `Standing_FireOrder` | [R-STANCE-01 §2]. Complete. |
| `AttackSpecial` | Resolve command code 3 (attack a unit, §3.4) against the record's target with no position, re-identify **this record** as the resolved descriptor (keeping mask bits `0x600`), set p1 = 2, return *hold* (2). The record runs the resolved attack handler from its next visit with the weapon-slot selection 2. |
| `QMove` / `QPatrol` | Deadline 60, *rotate*. No goal, no target use: a 60-tick delayed tail rotate ([R-ORDER-02 §1]). |
| `WaitForAttack` | Target null → complete. Phase 0: gate = `0x18`; advance. Phase 1: complete. Other phase: cancel-all. (Wakes on target loss, bit `0x8`, or on the unlocated bit `0x10`.) |
| `BeCarried` | If the unit's carrier link is null → complete. Phase 0: release all slots; advance. Phase 1: deadline 10; hold. Other: cancel-all. A carried unit therefore re-checks its carrier link every ~10 ticks. |
| `Paralyze` | p1 is the stun credit in ticks. p1 = 0 → lower edge bit 4 of the edge byte (stun off); complete. Else clamp p1 to 1800, release all slots, clear the three slot targets unconditionally, release the goal payload, deadline = p1, p1 = 0, raise edge bit 4 (stun on); advance. Phase 1 on expiry sees p1 = 0 and completes. Later paralyzer hits add to p1 of the waiting head record (§2.4; doc 06 owns the packet arithmetic). |
| `Wait` | p1 = timeout budget in ticks, p2 = scan radius. With p2 ≠ 0 (every phase): enumerate the target registry within p2 of the unit for the unit's side (inclusive `d² ≤ r²` in whole units); any hit → complete; else if p1 < 1 → complete; else draw `r = RNG(30)`, `p1 −= r + 150`, deadline `r + 150`, hold. With p2 = 0: phase 0 deadline = p1, advance; phase 1 complete; other cancel-all. |
| `SelfDestruct` / `SelfDestructFG` | Shared body. p2's high nibble marks initialisation: when clear, p2 = the definition's `selfdestructcountdown` (3-bit field, default 5) with the marker. If p1 = 0 and the countdown field is nonzero: with the satisfied set lacking bit 1 (cancel-current), let `n` = the remaining count; if n = 0 set p1 = 1 else store n − 1; emit status kind `22 − n` (`five` … `zero`; n ≥ 6 indexes past the six-entry table and is prevented only by the parser's 3-bit field, values 6 and 7 being **Unknown** — decider: the FBI parser's clamp); deadline `RNG(15)` when n was 0, else 30; gate |= `0x2`; advance. With bit 1 present (cancelled): if the unit lacks auto flag 14 emit status 23 (`Self destruct terminated`); complete. Otherwise (countdown finished, or the definition has no countdown): apply 30000 damage to itself with damage cause 3; complete. The record lives on the rear segment ([R-ORDER-02 §1]), so only its own deadline and the cancel path drive it. |
| `SelfRepair` | Target (the repairer) null → status 7 with `Repair aborted.`; abandon. Phase 0: target definition must have `builder` (else cancel-all); target must be complete (remaining fraction 0.0) and activated (edge bit 0) → release all slots, advance; else abandon. Phase 1: if own health ≥ own `maxdamage` → advance; else stamp nanolathe-active `tick + 150` on itself, run the repair step (doc 05: the repairer's per-tick heal against this unit); when it did work, draw the spray from the **target's** nano piece to **this unit's** box; deadline 1; gate |= `0x8`; hold. Phase 2: status 10 with `Unit repaired`; complete. Other: cancel-all. |
| `Teleport` | Single visit. For every live unit other than itself whose position lies inside this unit's model bounding box (position plus the definition's min/max triple, inclusive on all three axes): its new position is `goal + (its position − my position)`; emit the teleport effect (kind 5, duration 30) from old to new, then place it there through the position setter (re-registers occupancy when the footprint cell changes). Complete. The teleporter itself never moves. |
| `Park` | Phase 0: no mover reference → cancel-all. With `canfly`: goal = own position, re-identify the record as `VTOL_Move`, *restart* (the air move runs in the same cascade). Else `s = FootPrintX` (+3 when the movement class's `MinWaterDepth` word is non-negative — template default −10000, so land classes get no `+3`; corrected 2026-08-29 per [R-FAC-02 §7], previously "the definition's yard-map width word"); install a rectangle goal with origin `(cellX − 4s, cellZ − 3s)` and size `(8s, 6s)` in cells, where cellX/Z are the unit's whole-unit position shifted to cells; gate = `0xE0`; advance. Phase 1: satisfied `0x20` → complete; a record behind it exists → complete; else deadline 30, *restart*. Other: cancel-all. |

### Closed — the combat handlers [R-ORD-01 §3] (2026-08-29)

**`Attack_NoMove`.** Pre-check: target null, or satisfied ∩ `0x10008` (target
lost, or target cloaked — §6 below) → complete. Phase 0: caption clear;
advance. Phase 1: release slot 0, bind slot 0 to the target, gate =
`0x11808`; advance. Phase 2 (reached when any of those bits arrive): inhibit
all slots; *re-arm* (9). Other: cancel-all. The unit never moves; the weapon
layer (doc 06) fires from the bound slot.

**`Attack_Chase`.** Pre-checks, in order: satisfied `0x800` → complete;
target null → complete; satisfied ∩ `0x10008` → complete; when p3 (the leash)
is nonzero and `trunc(hypot(myWholeX − anchorX, myWholeZ − anchorZ)) ≥ p3`
→ complete (the return-to-post of [R-STANCE-01 §4]; the anchor pair is the
whole-unit pair of §3.2). Phase 0: requires a mover reference, no `canfly`,
and state-word bit 31 (else cancel-all); caption clear; goal = own position;
p2 = 0; if p1 (the slot) is 0 take the default slot pick; advance. Phase 1:
release the payload; satisfied ∩ `0x3000` → advance; the shot-admission gate
(doc 06 §3.1) for slot p1 fails → advance; else release slots 0 and 2, bind
slot p1 to the target, gate = `0x13808`; hold. Phase 2 (maneuver): let `d` =
the slot's engagement distance (doc 06). By p2: **0** → point goal at the
target radius `d`, p2 = 1. **1–4** → if `|myY − targetY| > 8` world units:
point goal radius `d/2`, p2 = 6; else draw `a = bearing(target → me) − 0x4000
+ RNG(0x8000)` and install a point goal at `target − d·(sin a, 0, cos a)`
(fixed-point sine/cosine helpers) with radius `d/4` rounded toward zero —
**p2 unchanged**. **5** → point goal radius `d/2`, p2 = 6. **6** → point goal
at the target radius 0, p2 = 7. **7** → annulus (outer `d`, inner `d/2`),
p2 = 8. **8** → annulus (outer `2d`, inner `d`), p2 = 0. **≥ 9** → cancel-all.
All arms advance. Phase 3: satisfied ∩ `0x40E0` → phase = 1, return 4 (hold
with the phase already reset); else if the shot gate passes: release slots 0
and 2, bind slot p1, gate = `0x148E8`, deadline 30, hold; else inhibit all,
gate = `0x100E8`, deadline 30, hold. Other phase: cancel-all.

**Correction to §3.5's "Attack-chase state machine".** That paragraph
describes "an eight-state substate machine 0 through 8: approach at standoff
distance; strafing steps that halve the distance when vertical separation
exceeds eight units; closer approaches at half and zero standoff; then two
banded-goal states". The substates exist, but the strafe arm (1–4) never
increments p2, and nothing else writes p2 but the vertical-separation jump to
6 and the wrap to 0: the reachable cycle is **0 → 1 (repeated strafes) → 6 →
7 → 8 → 0**, entered at 6 only through the `> 8`-unit vertical jump.
Substates 2, 3, 4, and 5 are dead under this handler. The "strict thresholds"
sentence describes the goal handle's own arrival predicate (§8.3), not a
handler test.

**`Attack_Kamikaze`.** Pre-check: satisfied ∩ `0x10008` → complete. Every
visit with a target: goal = target position. Phase 0: carried → cancel-all;
caption clear; point goal at the goal with radius `max(16,
kamikazedistance)`; deadline 60; gate |= `0xE0`; advance. Phase 1: satisfied
`0x20` → status 6 (`Arrived`), spawn `SelfDestruct` with p1 = 1 (no
countdown: immediate 30000 self-damage, cause 3) at the head, complete;
satisfied `0x40` → abandon; else phase = 0, hold (the goal is re-issued on
the next visit — deadline expiry or `0x80`). Other: cancel-all.

**`AttackUType`.** p1 = the definition index to hunt. Phase 0: definition
must have `canattack` (else cancel-all); deadline `RNG(90) + 1`; advance.
Phase 1: scan every live unit from the second slot on whose definition index
equals p1 and whose owner is hostile to mine (my side's diplomacy byte
toward its owner reads 0); score each as `d² − RNG(d²/2)` with `d²` the
whole-unit squared planar distance (64-bit squares shifted down); keep the
lowest score (ties → later unit). None → complete. Else resolve command code
3 against it, spawn the resolved attack record (target = it) at the head,
*restart*. The hunt record therefore stays behind the spawned attack and
re-scans `RNG(90)+1` ticks after each one ends. Other phase: cancel-all.
Note the per-candidate draw with `n = d²/2`, subject to the `n < 2` rule.

**`Suppress`** (fire at a position). Pre-check: satisfied `0x800` → complete.
Phase 0: `canfly` → abandon; caption clear; p2 = the engagement distance of
slot p1; advance. Phase 1: p1 = 2 → release all slots, bind slot 2 to the
goal position; else release slots 0 and 1 and bind both to the goal; gate =
`0x1C00`; advance. Phase 2: inhibit all; satisfied `0x400` → phase = 1,
*rotate*; else with a mover: p2 < 1 → *re-arm*; point goal at the goal
radius p2, gate = `0xE0`, `p2 −= RNG(engagementDistance(p1) / 3)`, phase = 1,
return 4 — each wake walks the unit closer by a random fraction of a third of
its range. No mover → *re-arm*. Other: cancel-all.

**`Guard_NoMove`** (stationary guard). Pre-check: satisfied ∩ `0x10008` →
phase = 3, hold (jump to the scan). Phase 0: inhibit all; deadline 30;
advance. Phase 1: read slot 0's target as a unit and bind the record's
smart-reference to it; if it exists and carries bit 28: goal = its position,
release slot 0, bind slot 0 to it, p1 = 0, `p2 = RNG(3) + 3`; advance. Else
deadline 30; hold. Phase 2: `p1 = satisfied has 0x4000 ? 0 : p1 + 1`; if
`p1 ≤ p2` and the shot gate admits the target: gate |= `0x7008`, hold; else
draw `RNG(100)`: below 80 → p1 = 0, advance (to the scan); else *restart*.
Phase 3: enumerate the target registry within 640 world units of the
record's goal for my side; a hit → pick index `RNG(count)`, bind the
smart-reference and slot 0 to it, phase = 1, hold; none → *restart*. Other:
cancel-all. Two draws per wake at most, three per scan.

**`Standby`.** Phase 0: no mover reference → cancel-all; inhibit all; gate |=
`0x10000`; deadline 1; advance. Phase 1: run the opportunity scan
([R-STANCE-01 §3]: only while the fire stance is *fire at will*); a target
found and the auto-engage issuer succeeds → complete; else gate |= `0x10000`,
deadline `30 + RNG(30)`, hold. Other: cancel-all.

**`Standby_Mine`.** As `Standby` except: phase 0 also requires state-word bit
29 (else cancel-all); phase 1 requires the scanned target's committed mover
mode to be **grounded** (`1`) and my own fire stance nonzero, and then spawns
`SelfDestruct` with p1 = 1 (immediate) at the head and completes.

**`Follow_Ground`** is [R-UNIT-06 §1] as corrected by [R-STANCE-01 §3]; the
trace here agrees with every gate, the follow-radius arithmetic (both terms
are the footprint-size words of the correction above), the one `RNG(65536)`
draw, and the fixed 30-tick deadline. **Correction to [R-UNIT-06 §1].** Its
branches 1, 3, and 4 say the guard "enqueues it at the queue tail",
"tail-append[s] the new record", and "enqueue[s] help-build". All three go
through the head insert of §1 (branch 1 through the auto-engage issuer with
its force flag set, which bypasses the stance gates and inserts a leash-0
attack at the head): the spawned order becomes the **front** record and the
guard record waits behind it, resuming when it is gone. The "queued mode"
wording for branch 1 describes the force flag, not a queued insertion.

### Closed — the ground movement handlers [R-ORD-01 §4] (2026-08-29)

**`Move_Ground`.** Phase 0: carried → cancel-all; caption clear; point goal
at the record's goal with radius `(int16)payloadType + 4` — 4 for every
interface- or AI-issued move; gate = `0xE0`; advance. Phase 1: satisfied
`0x20` → status 6 (`Arrived`), complete; else *re-arm* (the last record
rebinds from phase 0 after 30–59 ticks, [R-ORDER-02 §1]). Other: cancel-all.

**`Patrol`.** Phase 0: dead → cancel-all; run the **patrol-chain setup**:
walk the front segment for a record whose static-mask copy carries bit 15;
when none does, allocate a record of this same descriptor with goal = the
unit's current position and append it at the **tail** (the return-to-start
waypoint); in every case set bit 15 on this record. Deadline 1; advance.
Phase 1: clear the three slot targets; point goal at the goal with radius 0;
deadline 15; gate = `0xE0`; advance. Phase 2: satisfied ∩ `0xE0` → phase = 1,
*rotate*; a next patrol record exists → gate = 0, phase = 1, *wait*; else
deadline `30 + RNG(30)`, phase = 1, return 4. Other: cancel-all. Bit 15 of
the static-mask copy is therefore the runtime *patrol-chain member* flag,
set only here and read only by this setup.

**`RepairPatrol`.** Phase 0: with a target, goal = its position; run the
patrol-chain setup above; advance. Phase 1: satisfied ∩ `0xE0` → *rotate*.
Point goal at the goal radius 16; deadline 60; gate |= `0xE0`. Then, only
when the player's energy is at least 20 % of energy storage: enumerate units
within `sightdistance` of the unit through the repair-candidate filter — the
gather helper itself draws `sim(n)` three times for the energy-need list and
three times for the metal-need list, each triple only when that list is
non-empty (best-scored of three random picks; corrected 2026-08-29 against
[01 R-DET-01 §6], the earlier text implied the single pick below was the only
draw) — then pick
index `RNG(count)`, and when its owner is **not** hostile to mine (diplomacy
byte nonzero) resolve command code 8 (assist or repair) against it; when
resolvable and the issue helper accepts it → *rotate*, else *wait*. Then when
both energy and metal are at least 20 % of their storages → hold. Otherwise
scan features within `sightdistance` for the nearest energy-bearing and the
nearest metal-bearing reclaimable feature (both with their values); none →
hold. In order: a metal feature exists and metal < 20 % of storage → spawn
`Reclaim` on it; else if no energy feature or energy ≥ 20 %: a metal feature
whose value fits under storage → spawn `Reclaim` on it, no metal feature →
hold, otherwise the energy feature's value does not fit → hold, else spawn
`Reclaim` on the energy feature; else (energy feature and energy < 20 %) spawn
`Reclaim` on the energy feature. Every spawn releases this record's payload,
inserts the reclaim (goal = the feature position) at the head, clears this
record's gate, and returns *wait*. Other phase: cancel-all. The player
resource fields are identified by the pairing of the energy gate with the
repair scan and of the metal gate with the metal-feature reclaim
(**Supported inference** for the labels; the arithmetic is Established).

### Closed — the work handlers [R-ORD-01 §5] (2026-08-29)

The build family shares one pre-check pair: satisfied bit 1 (cancel-current,
[R-ORDER-02 §2]) refreshes the builder interface and completes; for
`MobileBuild` satisfied bit 3 (`0x8`) instead emits status 7 with
`Construction terminated`, refreshes, and abandons. The work step every build
handler runs is the shared helper of §3.8 / doc 05 with a quantum of
`workertime / 30` (integer division, then float). Every handler below emits
`StartBuilding` exactly once per pass through its in-reach phase; it is
re-emitted only after a *restart* (code 0) sends the record back through that
phase, which the out-of-reach arms of `ReclaimUnit`, `RepairUnit`, and
`Capture` do after their own `StopBuilding` — this settles the "re-arms the
emitter on later work visits" question of [R-ORDER-02 §2]: not per visit,
only per restart.

**`MobileBuild`.** p1 = product definition index, p3 = blocked-area retry
counter. Phase 0: snap the goal X/Z onto the product footprint (§1: `cell =
snap(goal)`, `goal = (foot + 2·cell)·2^19`); p3 = 0; rectangle goal at that
cell with the product's footprint size; gate = `0xE0`; advance. Phase 1:
when satisfied has `0x40`, run the reach test against the product footprint;
out of reach → status 7 `I can't reach the construction site`, abandon.
Then validate placement of the product footprint at the snapped cell
(§6.4): legal → release all slots, prepare the site, create the product as a
nanoframe (owner = mine, remaining fraction 1.0), bind it as the target;
created → status 9 with `Starting construction`, refresh the interface,
insert `GetBuilt` on the product in queued mode, emit `StartBuilding(bearing
− heading)`, advance; not created → status 7 `Unable to create any more
units`, deadline 300, hold. Illegal → p3 = 0: status 7 `Waiting for target
area to clear`; p3 > 10: status 7 `Target area was blocked`, abandon; then
p3 += 1, deadline 30, hold. Phase 2: `INBUILDSTANCE` wait, extra `0xA`.
Phase 3: work step; when it did work draw the spray; stamp `tick + 300`;
product unfinished → deadline 1, gate |= `0xA`, hold; finished → advance.
Phase 4: status 8 with `Building complete`; complete. Other: cancel-all.

**Correction to [R-ORDER-02 §1]'s retry table.** It says "each blocked
attempt notifies *Waiting for target area to clear*". The caption is emitted
only on the **first** blocked attempt (counter 0); attempts 1–10 are silent,
and the eleventh over-limit visit emits *Target area was blocked*. The
counts and the fixed 30-tick wait stand.

**`HelpBuild`.** Target null → status 7 `Construction terminated`, abandon.
Phase 0: mover and `builder` required (else cancel-all); `half = trunc(16 ·
sqrt(FootPrintX² + 2·FootPrintZ)) / 2` (signed halving) from **my own**
footprint — the asymmetric radicand is what retail computes; annulus goal at
the target's position with outer `builddistance + half`, inner `half`; gate
= `0xE8`; advance. Phase 1: satisfied `0x40` → status 7 `I can't get there`,
abandon; target complete → complete; release all slots, emit `StartBuilding`,
refresh; advance. Phase 2: `INBUILDSTANCE` wait, extra `0xA`. Phase 3: work
step, spray, stamp `tick + 300`; unfinished → deadline 1, gate |= `0xA`,
hold; else advance. Phase 4: status 8 `Building complete`; gate |= `0x2`;
complete — the gate bit makes cleanup deliver the cancel-current wake through
this handler, which is how the builder interface refresh runs on removal.

**`RepairUnit`.** Target null → status 7 `Repairs unsuccessful.`, complete.
Leash pre-check exactly as `Attack_Chase` (p3, anchor pair; over the leash →
complete). Target's mover mode not grounded (`≠ 1`) → same caption,
complete. Phase 0: mover, `builder`, target complete (else cancel-all);
caption clear with `Repairing`; advance. Phase 1: satisfied `0x40` →
abandon; reach test against the target; out of reach → rectangle goal on the
target's footprint, deadline `30 + RNG(30)`, gate |= `0xE8`, hold (the phase
stays 1, so the goal is re-issued on every wake); in reach → release all
slots, `StartBuilding`; advance. Phase 2: `INBUILDSTANCE` wait, extra `0x8`.
Phase 3: target health ≥ maxdamage → advance; target state-word bits 2–3 set
→ `StopBuilding` (mid-life), deadline 15, *restart*; else stamp `tick + 150`,
repair step (doc 05), spray on success, deadline 1, gate |= `0x8`, hold.
Phase 4: status 10 `Unit repaired`; complete. Other: cancel-all.

**`RepairUnitNoMove`.** Target null → status 7 `Repairs unsuccessful.`,
complete. Phase 0: `builder` required (else cancel-all); target complete and
**this unit** activated (edge bit 0) → release all slots, advance; else
abandon. Phase 1: target health ≥ maxdamage → advance; target bits 2–3 set →
advance; else stamp, repair step, spray, deadline 1, gate |= `0x8`, hold.
Phase 2: status 10 `Unit repaired`; complete. Other: cancel-all.

**`ReclaimUnit`.** Pre-check: target null or satisfied ∩ `0x10008` →
complete. Phase 0: mover and `canreclamate` required — else status 7
`Reclamation failed`, cancel-all; the reclaim-admission test (`canreclamate`
on me, target's mover mode not airborne, target's definition **without**
`cancapture` — commanders cannot be reclaimed) → caption clear with
`Reclaiming`, release all slots, advance; fails → status 7 `That unit cannot
be reclaimed` then status 7 `Reclamation failed`, abandon. Phase 1: not yet
arrived (`0x20` absent) → rectangle goal on the target footprint, gate |=
`0x100E8`, deadline 15, p1 = work-amount seed with k = 15, p2 = 0, hold;
arrived → advance. Phase 2: satisfied `0x40` → *re-arm*; else `StartBuilding`,
advance. Phase 3: `INBUILDSTANCE` wait, extra `0x10008`. Phase 4: status 11
(`working`, no text); advance. Phase 5: reach test `dx² + dz² ≤ (builddistance
+ targetModelRadius)²` in whole units, with the target's model radius the
whole part of the definition's `(Xextent + Zextent)/3` word, and the
admission test again: both pass → if p2 > 14 apply p1 damage to the target
with cause 5 and p2 = 0; stamp `tick + 900`; spray; deadline 2; p2 += 2; hold
— one reclaim bite every 16 ticks. Either fails → deadline 15,
`StopBuilding`, *restart*. Other: cancel-all.

**`Reclaim`** (feature). Every visit: resolve the feature at the goal cell
(the feature grid, with the parent lookup for a multi-cell feature's non-origin
cells); none → status 7 `Reclamation failed`, abandon; feature not
reclaimable → abandon. Phase 0: mover and `canreclamate` → rectangle goal on
the feature's footprint (origin cell, size), gate = `0xE0`, advance; else
cancel-all. Phase 1: satisfied `0x40` → abandon; `p1 = trunc(15 + (metal +
energy) / 2)` from the feature definition's values (the remaining work in
ticks); form the spray target at the footprint centre with `Y = terrain
height there + RNG(featureHeightByte)` (the byte's TDF key is **Unknown** —
decider: the feature parser's key list); `StartBuilding(bearing − heading)`
toward it; advance. Phase 2: `INBUILDSTANCE` wait, extra 0. Phases 3 and 4
share one body, phase 3 first emitting status 11 (`working`, no text) on
every visit it stays in: deadline 2; `p1 −= 2`; p1 > 0 → stamp `tick + 300`;
p1 > 15 → draw the spray twice to the feature box; hold. p1 ≤ 0 → advance.
Phase 5: finish the reclaim (remove the feature and credit its values —
doc 05); complete. Other: cancel-all.

**`Resurrect`.** Phases 0–5 begin with the feature lookup of `Reclaim`
(none → status 7 `Resurrection failed`, abandon; not reclaimable → abandon).
Phase 0: mover and `canresurrect` → rectangle goal on the footprint, gate =
`0xE0`, advance; else cancel-all. Phase 1: `0x40` → abandon; `StartBuilding`
toward the same random-height centre as `Reclaim`; advance. Phase 2:
`INBUILDSTANCE` wait, extra 0. Phase 3: copy the feature's name up to its
first `_` and look the unit definition up by that name; found → p1 = its
index, `p2 = trunc(0.3 · buildtime / (workertime / 30))` with the inner
division integer (the resurrection spray ticks), status 11; advance. Not
found → status 7 `Ressurection failed` (retail's spelling), abandon. Phase 4:
`p2 −= 1`; when it was nonzero: spray to the feature box, stamp `tick + 300`,
deadline 1, hold; else advance. Phase 5: create the unit (definition p1 at
the goal, owner = mine) and bind it as the target; null → status 7 `Unable
to create any more units`, deadline 300, hold. Re-read the feature at the
goal; gone → abandon. Copy the corpse feature's stored heading pair into the
new unit's heading fields, remove the feature from the grid, and (in the
networked session mode) send the feature-removal message; set the new unit's
remaining fraction to **0.0** and health to **1**; refresh the interface;
advance. Phase 6: status 8 with `Resurrection complete`; resolve command
code 8 (repair) against the new unit and, when resolvable, spawn it at the
head; complete. Other: cancel-all. A resurrected unit is therefore complete
but at 1 health, and the resurrector immediately starts repairing it.

**`Capture`.** Pre-check: target null or satisfied ∩ `0x10008` → status 7
`Capture failed`, abandon. Phase 0: mover and `cancapture` (else
cancel-all); target's definition has `cancapture` → status 7 `That unit
cannot be captured`, abandon; target unfinished → status 7 `That unit is a
cloud of vapor and cannot be captured`, abandon; caption clear with
`Capturing`; the capture budget `p2 = trunc(0.015 · buildcostenergy +
(30/140) · buildcostmetal + 150)` from the target definition, clamped to
1800, then `p2 = p2 · (targetHealth + maxdamage) / (2 · maxdamage)`, then
`p2 = ((targetExperience / 5 + 10) · p2 · 10) / 100`, all integer; release
all slots; rectangle goal on the target footprint; gate = `0x100E8`; advance.
Phase 1: `0x40` → abandon (no caption); reach test; in reach →
`StartBuilding`, advance; else *restart*. Phase 2: `INBUILDSTANCE` wait,
extra `0x10008`. Phase 3: status 11; advance. Phase 4: target has a mover and
state bits 2–3 set → `StopBuilding`, deadline 30, *restart*; p1 < p2 → spray,
stamp `tick + 900`, `p1 += 2`, deadline 2, hold; else advance. Phase 5:
transfer the target to my player (doc 05 owns the transfer), status 16
(`capture`); complete. Other: cancel-all. No resource cost and no decay
(doc 05).

**`BuildingBuild`** is the factory machine of §3.8 [R-P0-09]; the trace
agrees with it. Per visit: satisfied bit 3 → status 7 `Construction stopped`,
p2 −= 1, refresh, *restart*; satisfied bit 1 → the cancel-current refund and
cause-9 kill of §3.3 (skipped when no product is bound), lower edge bits 0
and 3 together, refresh, complete. Phase 0:
clear the smart-reference; state bit 29 required (else cancel-all); p2 > 0 →
raise edge bit 0, advance; else lower it, complete. Phase 1: `INBUILDSTANCE`
wait, extra `0x2`. Phase 2: `QueryBuildInfo` (cell 0 seeded −1) → exit
transform → goal; snap to the product footprint; validate with my mover
mode as the medium argument; illegal → deadline 15, gate |= `0x2`, hold;
create the nanoframe at the goal; created → status 9 `Starting
construction`, attach it as cargo-style carried product, copy my standing
bits 18–21 onto it, insert `GetBuilt` queued, raise edge bit 3
(`StartBuilding`), refresh, advance; else status 7 `Unable to create any
more units`, deadline 300, gate |= `0x2`, hold. Phase 3: with a target, work
step (spray on success); finished → advance; else deadline 1, gate |= `0xA`,
hold; without a target → cancel-all. Phase 4: status 8 (default text
`Nanolathe Complete`), lower edge bit 3 (`StopBuilding`), the completion
transition, clear the reference, p2 −= 1, refresh, *restart*.

**`BuildWeapon`** is doc 05 "Stockpile production"; the trace agrees with
it. p1 = weapon slot, p2 = remaining count, p3 = progress. Phase 0: p2
< 1 → complete; the slot's stock byte > 199 → deadline 300, hold; p3 = 0;
advance. Phase 1: `new = min(p3 + 5, total)` where `total` is the weapon's
compiled reload-time value; admit `trunc(new·metal/total) −
trunc(p3·metal/total)` and the same for energy (the weapon's per-shot
costs); refused → deadline 10, hold; else p3 = new; new < total → deadline 5,
hold; else advance. Phase 2: stock byte += 1, p2 −= 1, refresh; *restart*.
Other: cancel-all. No draw anywhere.

**`GetBuilt`** is §3.8 [R-P0-09]; the trace agrees with its 300/30/wake
states and the rally walk (`QMove`/`QPatrol` records copied in order,
`PARK` when none; the walk and the standing-bit copy run only when the
product has a mover reference and a builder reference). Two details the
earlier text did not have: **(a)** phase 2's wake bit `0x8000` has **no
located producer** in the bounded set (no direct writer, and the only two
indirect raisers pass `0x8` and `0x10000`); the phase is instead driven by
its own deadline — on expiry with `0x8000` absent it sets deadline **11** and
applies the shared work step with a **negative** quantum, `−(11 · buildtime /
buildcostenergy)`, i.e. the nanoframe **decays** by `11 / buildcostenergy`
of its remaining fraction every 11 ticks, with the refund arithmetic of doc
05 (the work helper's negative arm). **Unknown:** what suppresses this decay
while a builder is working — a producer of `0x8000` that the bounded trace
did not find, or nothing (in which case decay competes with construction).
*Decider:* trace the work helper's callers for a wake into the product's
`GetBuilt` record, or measure a timed build against the formula. **(b)** the
standing-bit copy runs only under the double bit-28 / bit-14 guard of §3.8
and copies the experience word only for a computer-owned builder.

### Closed — two gate-bit producers, located [R-ORD-01 §6] (2026-08-29)

[R-UNIT-06 §1] and [R-ORD-01 §0] left the producers of pending bits `0x8`,
`0x10`, and `0x10000` unlocated. Two of the three are found:

* **`0x8` — target removed.** A record's target smart-reference is a small
  header whose first method raises bits into the record's pending word. When
  a unit is destroyed, the removal path walks every reference registered on
  that unit and calls the method with `0x8`, then unlinks the reference. This
  is why the attack, guard, work, and wait pre-checks all treat `0x8` as
  "target lost".
* **`0x10000` — target cloaked.** The unit edge machine's bit 2 is the cloak
  state: on its rising edge it emits status 14 (`Cloaked`) and calls the same
  method with `0x10000` on every reference registered on the cloaking unit;
  on its falling edge it emits status 15 (`Visible`). A record whose target
  cloaks therefore wakes with `0x10000`, and the pump's three unconditional
  slot clears ([R-ORDER-02 §2]) run for it. The *unit capability word* term of
  §3.3 step 2 is a separate field; its bit-16 writer remains **Unknown**
  (decider: static trace of that word's writers).
* **`0x10`** has no located producer: the two raisers above pass only `0x8`
  and `0x10000`, and no direct write of the pending word with bit 4 exists in
  the bounded set. The guards' `0x18` gate is therefore satisfied in practice
  only by `0x8` and by their own deadlines. **Unknown** whether any path
  raises it; *decider:* enumerate every call through the reference header's
  first method (an indirect-call census, not a constant grep). **Closed
  (2026-08-29):** that census is [R-MOV-03 §7] — the damage-intake observer
  notice delivers `0x10` through the same method.

### Closed — the five VTOL work twins [R-ORD-01 §7] (2026-08-29)

The air forms of the work orders are separate handler bodies, not the ground
bodies behind a flight flag. They share the ground vocabulary of [R-ORD-01 §1]
and the air marker family of [R-AIR-01 §4], and they differ from their ground
twins in ways an implementation cannot derive: no `INBUILDSTANCE` wait, no
`StopBuilding` on the out-of-reach arm, different reach tests, different
work constants, and fewer captions. Everything below is **Established** by
direct trace of the five bodies (RWU-04-4).

**The air work preamble.** Phase 0 of all five requires a live mover and
`canfly` (else cancel-all), then: caption clear with the handler's state
text; release all three weapon slots; when the unit is carried, drop it from
its carrier through the attach commit of [R-COB-03 §5] with the third value
**2** (the only engine sites that pass a nonzero third value); raise the
activation edge (edge bit 0, [R-UNIT-06 §2]); and, **only when the mover is
grounded** (mode 1, [R-MOV-01 §8]), set it airborne (mode 2), build an air
marker on the unit's own position with altitude offset `cruisealt / 2`
(signed halving of the 16-bit definition word), install it as the goal
payload, and OR `0xE0` into the gate. The return is *advance* whether or not
the marker was built — an already-airborne unit skips straight to phase 1
with no goal armed and is re-dispatched on its next pump visit. Where a
ground twin emits `StartBuilding`, the air twin's site is named below; none
of the five reads `INBUILDSTANCE`, so an aircraft's script gets no build-stance
wait between arrival and work.

**`VTOL_HelpBuild`.** Pre-check: target null or satisfied `0x8` → status 7
`Construction terminated by hostile action`, refresh the builder interface,
abandon; satisfied `0x2` → refresh, complete. Phase 0: preamble with
`Building`, plus the definition's builder-specific script slot must be
present (else cancel-all). Phase 1: p3 = 0; air marker at the **target's**
position with horizontal arrival radius `builddistance` (the radius-flag
setter, so arrival is `dist < builddistance` in whole units); install; gate
= `0xE0`; advance. Phase 2: satisfied `0x40` → abandon (no caption); target
complete → complete; else emit `StartBuilding` with the **absolute bearing**
from the builder to the target — this is the one site in the image that
passes the raw bearing without subtracting the unit's heading
([R-CB-01 §3]), so an air builder's script receives a world heading, not a
relative one; refresh; advance. Phase 3: on every tick with `tick mod 150 =
0`, rebuild the orbit marker of §10.3 (bearing from the builder to the target
plus `0xDB6E`, radius `builddistance`, heading stored on the marker); then
the work step with quantum `workertime / 30` (integer division, then float);
when it did work, draw the spray from `QueryNanoPiece` to the target's
model box; target unfinished → deadline 1, gate |= `0xA`, hold; finished →
complete. There is no `Building complete` status, no nanolathe-active stamp,
and no phase 4. Other: cancel-all.

**`VTOL_RepairUnit`.** Target null → status 7 `Repairs unsuccessful.`,
abandon (the ground twin *completes*). Leash pre-check as `Attack_Chase`
(over the leash → complete). Target mover mode not grounded (`≠ 1`) → same
caption, complete. Every visit then copies the target's position into the
record goal. Phase 0: mover and `canfly`; the **repair admission** test
(below) fails → status 7 `Repair mission failed`, abandon; preamble with
`Repairing`. Phase 1: air marker at the goal with altitude offset the
**full** `cruisealt`; install; gate = `0xE8`; advance. Phase 2: satisfied
`0x40` → abandon; target mode `≠ 1` → abandon; target state bits 2–3 set →
deadline 15, *restart* (no `StopBuilding` — none was ever emitted); target
health below `maxdamage` (unsigned compare of the 16-bit health against the
definition word) → repair step (doc 05), spray to the target box, deadline 1,
gate |= `0x8`, hold; else advance. Phase 3: status 10 `Unit repaired`;
complete. Other: cancel-all. There is no reach test, no `StartBuilding`, and
no nanolathe stamp: an aircraft repairs from wherever its marker leaves it.

**`VTOL_Reclaim`** (feature). Every visit resolves the feature at the goal
as the ground twin does (none → status 7 `Reclamation failed`, abandon; not
reclaimable → abandon). Phase 0: preamble with `Reclaiming`, plus
`canreclamate`. Phase 1: air marker **on the feature's goal position with no
altitude or radius setter** — arrival is the default `dist ≤ 0.5` world
units of [R-AIR-01 §4] at the goal's own Y; install; gate = `0xE0`; advance.
Phase 2: satisfied `0x40` → abandon; `p1 = trunc(30 + (metal + energy) /
2)` from the feature definition — the constant is **30** where the ground
twin uses 15, so an aircraft takes fifteen more ticks per feature; status 11;
advance. No `StartBuilding`. Phase 3: deadline 2; `p1 −= 2`; p1 > 0 → stamp
`tick + 300`; p1 > 30 → spray twice to the feature box (whose Y is the
terrain height plus the feature's height byte, **no random draw** — the
ground twin's `RNG(featureHeightByte)` is absent here); hold. p1 ≤ 0 →
advance. Phase 4: finish the reclaim (doc 05); complete. Other: cancel-all.

**`VTOL_ReclaimUnit`.** Pre-check: target null or satisfied ∩ `0x10008` →
complete. Phase 0: mover and `canfly` (else cancel-all); no `canreclamate` →
status 7 `Reclamation failed`, cancel-all; the reclaim-admission test fails →
status 7 `That unit cannot be reclaimed`, abandon; preamble with
`Reclaiming`. Phase 1: p1 = work-amount seed with k = 15; p2 = 0; the record
goal is re-centred on the target's footprint (the snap/reverse pair of
[R-ORD-01 §1] using this unit's footprint pair); air marker at the target's
position with no setter; install; gate |= `0x100E8`; status 11; advance.
Phase 2: satisfied `0x40` → *re-arm*; gate |= `0x10008`; reach test `dx² +
dz² ≤ builddistance²` in whole world units (the squares are formed in 64
bits from the 16.16 deltas and shifted down by 32; there is **no model-radius
term**, unlike the ground twin's `builddistance + targetModelRadius`) and the
admission test: both pass → if p2 > 14 apply p1 damage with cause 5 and
p2 = 0; spray; deadline 2; p2 += 2; hold. Either fails → deadline 30,
*restart* (the ground twin waits 15 and emits `StopBuilding`). Other:
cancel-all. No `StartBuilding`, no stamp.

**`VTOL_RepairPatrol`.** Pre-check: satisfied ∩ `0x48` → deadline 30,
*restart*. Phase 0: mover, `canfly`, and the definition's `canreclamate`
mirror bit (the parser copies `canreclamate` into a second bit that this
handler and the repair admission test read); with a target, goal = its
position; the patrol-chain setup of [R-ORD-01 §4]; preamble with
`Patrolling`; advance. Phase 1, in order:

1. Satisfied ∩ `0xE0` → *rotate*.
2. Air marker at the goal with altitude offset the full `cruisealt`; install;
   deadline **45**; gate |= `0xE0`.
3. If health `< (maxdamage >> 2) · 3` (unsigned, strict): list this player's
   units that are `builder` **and** `isairbase` and activated, within 3840
   whole world units (`0xF00`, compared as whole-unit squares — effectively
   any pad on the map); if any: release the payload, draw `RNG(count)`,
   spawn `VTOL_Landing` at that pad at the head, gate = 0, *restart*. This
   is the same seek-a-pad rule [R-AIR-01 §7] gives `VTOL_SeekAttack`.
4. If energy ≥ 20 % of energy storage: enumerate units within
   `sightdistance` through the repair-candidate filter; when the list is
   non-empty draw `RNG(count)` and take that unit `u`: admission passes and
   `u` is complete → the issue helper for command code 8 accepts → *rotate*,
   refuses → *wait*; admission passes and `u` is unfinished → release the
   payload, spawn `VTOL_HelpBuild` on `u` at the head, gate = 0, *wait*.
   (There is no diplomacy test here — the ground twin's "owner not hostile"
   gate is absent; the admission test is the only filter.)
5. Feature pairing over a square of ±120 world units around the unit sampled
   every 48 world units (a **fixed** radius, where the ground twin passes
   `sightdistance`), keeping reclaimable features with nonzero energy and,
   separately, nonzero metal; none → hold. Then the ground twin's decision
   tree verbatim ([R-ORD-01 §4]) with one substitution: every spawn is
   `VTOL_Reclaim` on the feature, inserted at the head with gate = 0, *wait*.

Other phase: cancel-all. Two simulation draws at most per visit (the pad
pick, then the candidate pick), each only when its list is non-empty.

**The repair admission test** (shared by `VTOL_RepairUnit` phase 0 and the
patrol scan): the target exists; my definition carries the `canreclamate`
mirror bit; the target's 16-bit health differs from its `maxdamage`; the
target's mover mode is not airborne (`≠ 2`); and a water clause: `(I am not
canfly, or I am amphibious, or seaLevel ≤ targetY + targetModelHeight)` and
`(I am canfly, or seaLevel − myMaxWaterDepth ≤ targetY + targetModelHeight)`,
with `targetY` the whole part of the target's Y, `targetModelHeight` the
whole part of its definition's model height word ([R-COB-03 §2] port 11),
and `MaxWaterDepth` the movement-class value copied into the definition
(§6.1). For an aircraft the clause reduces to `seaLevel ≤ targetY +
targetModelHeight`: it will not repair a unit whose top is under water.

**Correction to the "Missing and unknown" tail.** The bullet that carried the
five twins as "no per-visit contract yet" is removed; the `0x100E8` /
`0x10008` gates it cited are those of `VTOL_ReclaimUnit` above.

### Closed — command resolution, exactly [R-ORD-02 §1] (2026-08-29)

§3.4's table names the outcomes of the command resolver; this block gives
the tests in the order the resolver runs them, so an implementer chooses
nothing. Everything here is **Established** by direct trace of the
name-form resolver (RWU-04-12) unless a sentence says otherwise. The
resolver takes a command code 1–14, the acting unit, an optional target
unit, and an optional world position; it writes a canonical order name
(looked up by §3.4's case-insensitive search) or the empty name, which is the
reject identity 0.

**Vocabulary.** *Word A* and *word B* are the definition's two capability
words of [R-SPEC-01 §0]; word A carries `builder` (bit 6), `isairbase` (9),
`canfly` (11), `canhover` (12), `hoverattack` (27) and `kamikaze` (28); word
B carries `canattack` (4), `canguard` (5), `canpatrol` (6), `canmove` (7),
`canload` (8), the `canreclamate` mirror bit (9, [R-ORD-01 §7]),
`canreclamate` (10), `canresurrect` (11), `cancapture` (12) and `candgun`
(14) ([02 R-KEYS-01]). *Air twin* means the `VTOL_` form of a name when the
acting unit's definition has `canfly` and the ground form otherwise; every
"or air twin" below is that one test. *Hostile* and *friendly* are set once
at entry: with a target, the acting player's diplomacy byte toward the
target's side (§3.4) equal to 0 is *hostile*, any other value *friendly*;
with no target both are false. A target that exists but lacks the alive bit
28 rejects **every** code before the switch. *Nano-reach* is the repair
admission test of [R-ORD-01 §7] (mirror bit 9 on me; target health differs
from `maxdamage`; target not airborne; the water clause). *Carriable* is
the nine-reject transport admission of §10.2. *Feature at the position*
means: the position's coarse mapping-word tile — `X >> 5` and `(Z − Y/2)
>> 5` in whole world units, the Y halving being the screen-projected pick
— lies inside the map and its word carries the **viewing** player's bit
([03 R-LAYER §1]: the tile is mapped memory for the viewer); and the feature
grid cell at the position (16-unit cells; a non-origin cell of a multi-cell
feature resolves through its parent link, [R-ORD-01 §5]) holds a feature
whose definition is **reclaimable**. Unmapped tiles and non-reclaimable
features count as no feature.

**Code 1 — contextual.** Two variants, selected by the `Interface Type`
option ([07 R-CAM-01 §5]; the `LEFTCLICK` two-stage button).

*`Interface Type = 1`*, in order: (1) `canattack` and hostile → resolve as
code 3; (2) `canreclamate` and hostile → `ReclaimUnit` or air twin; (3)
friendly and nano-reach passes: target unfinished → `HelpBuild` or air
twin, else `RepairUnit` or air twin (**no health test** — a full-health
friendly resolves to a repair); (4) I am `canfly`, friendly, and the target
has `isairbase` → `VTOL_Landing`; (5) carriable → `VTOL_Pickup` or
`Ground_Pickup`; (6) `canguard` and friendly → `VTOL_Follow` or
`Follow_Ground`; (7) `canresurrect`, a position, and a feature at it →
`Resurrect`; (8) `canreclamate`, a position, and a feature at it →
`Reclaim` or air twin; (9) `canmove` and a live mover → `Move_Ground` or
`VTOL_Move`; else reject.

*`Interface Type = 0`* (default), in order: (1) `canattack` and hostile →
code 3; (2) `canreclamate` and hostile → resolve as **code 12** (so the
feature tests below run first and a hostile unit is reclaimed only when no
feature is at the position); (3) with a target: nano-reach passes and the
target is unfinished → resolve as code 8; then, when the target is **my
own** (its owner slot byte equals the local slot), selectable (state bit
5), complete, its post-capture byte is 0 (below), and it is either not
carried or its carrier has state bit 30 → **reject** (a click on one's own
idle unit is a selection, not an order); (4) `canresurrect` + position +
feature → `Resurrect`; (5) `canreclamate` + position + feature → `Reclaim`
or air twin; (6) `canmove` and a live mover → move or air twin; else
reject. The default variant therefore never turns a click on a damaged
friendly into a repair (only an unfinished one into assistance) and never
resolves pickup, follow, or landing contextually; those need the explicit
codes.

**Code 2 — move.** `canmove` required (else reject). No live mover →
`QMove`. With a target, in order: `cancapture` and hostile → `Capture`;
`canreclamate` and hostile → `ReclaimUnit` or air twin; friendly and
nano-reach passes and the target is unfinished → `HelpBuild` or air twin;
friendly and nano-reach passes and the target's 16-bit health is below its
`maxdamage` (unsigned) → `RepairUnit` or air twin; I am `canfly`, friendly,
target `isairbase` → `VTOL_Landing`; carriable → pickup or air twin;
`canguard` and friendly → follow or air twin; else move or air twin.
Without a target: move or air twin.

**Code 3 — attack a unit.** `canattack` required (else reject). The armed
branch runs only when my state word has bit 31 (the armed bit of
[R-ORD-01 §3]); an unarmed unit falls straight to the kamikaze test. Let
*w0* be my weapon slot 0's weapon definition and *W1* my definition's
first-weapon definition; weapon flag names are doc 06's.

* **Not hostile** (friendly or no target): *w0* has `toairweapon` → reject;
  I am not `canfly` → `Suppress`; else *W1* has `dropped` → `AirStrike`,
  else `AirToGround`. A position-only attack from an aircraft is therefore
  always a run, never a hover.
* **Hostile.** First the target-class rejects, in order: the target is not
  airborne (mover mode ≠ 2) and *w0* has `toairweapon` → reject. Let `top` =
  target Y whole part + target model-height whole part ([R-COB-03 §2] port
  11). If `top < seaLevel` (the target is submerged): unless *w0* has
  `waterweapon`, or my slot 1 is enabled (its control byte bit 1) and its
  weapon has `waterweapon` → reject. Otherwise (`top ≥ seaLevel`), when I am
  `canhover`: *w0* `waterweapon` → reject; slot 1 enabled with
  `waterweapon` → reject. (A hovercraft's water weapons cannot be ordered
  onto a surfaced target; a submerged target needs one.) Then the variant:
  I am `canfly` → *W1* `dropped` and target not `canfly` → `AirStrike`;
  *W1* not `dropped` and target `canfly` → `AirToAir`; target not `canfly`
  and I lack `hoverattack` → `AirToGround`; target not `canfly` and
  `hoverattack` → `AirToGroundHover`; a `dropped` *W1* against a flying
  target → reject. I am not `canfly` → a live mover → `Attack_Chase`;
  state bit 29 (immobile) → `Attack_NoMove`; else fall through.
* **Fall-through** (unarmed, or an armed ground unit with neither a mover
  nor bit 29): word A `kamikaze` → `Attack_Kamikaze`; else reject.

**Code 4 — special attack.** `candgun` → `AttackSpecial`; else reject.
**Code 5 — unload.** `canload`, `canfly`, a target, and target `isairbase`
→ `VTOL_Landing`; else `canload` → `VTOL_Unload` or `Ground_Unload`; else
reject. **Code 6 — pick up.** A target that is carriable → pickup or air
twin; else reject. **Code 7 — guard.** `canguard` and friendly → follow or
air twin; else reject. **Code 8 — assist or repair.** Nano-reach must pass
(else reject); target unfinished → `HelpBuild` or air twin, else
`RepairUnit` or air twin — no health test here either. **Code 9 — patrol.**
`canpatrol` required; no live mover → `QPatrol`; mirror bit 9 clear →
`Patrol` or `VTOL_Patrol`; set → `RepairPatrol` or `VTOL_RepairPatrol`.
**Code 10** writes the empty name (§3.4). **Code 11** → `Teleport`, no
gate. **Code 12 — reclaim or resurrect.** `canreclamate` required. Resolve
the feature at the position once; then, with a position: `canresurrect`
and a feature → `Resurrect`; a feature → `Reclaim` or air twin; then a
target → `ReclaimUnit` or air twin; else reject. **Code 13 — capture.**
`cancapture`, a target, and the target's owner differing from mine →
`Capture`. **Correction to §3.4's row 13:** it says "target hostile and
differently owned"; hostility is **not** tested — an allied unit of another
player is capturable by this code. **Code 14 — mobile build.** The
definition's build list is non-empty and a live mover exists →
`MobileBuild` or air twin; else reject.

**The post-capture byte (Unknown).** Code 1's default variant reads a unit
byte that the ownership transfer of doc 05 writes `150` into on a
human-to-computer capture; its decrement site, and therefore what a nonzero
value means for the own-unit reject above, are not traced. *Decider:*
static trace of that byte's writers (doc 05's transfer and the per-tick unit
pass). Until then a reimplementation treats it as always 0.

### Closed — `VTOL_Move`, `VTOL_Patrol`, and `VTOL_MobileBuild` [R-ORD-02 §2] (2026-08-29)

These three air executors were cited to §10.1 and §10.3, which state the
flight integrator and the orbit recurrence but not the phase machines. All
three use the takeoff preamble of [R-AIR-01 §6] (release the latches;
detach from a carrier with request mode 2; raise the activation edge; when
grounded set mode 2, build a point marker on the unit's own position with
altitude offset `cruisealt / 2`, install it and OR `0xE0` into the gate).
**Established** by direct trace.

**`VTOL_Move`.** Phase 0: a live mover and `canfly` (else cancel-all); the
preamble; advance. Phase 1: caption clear; inhibit all three slots; snap the
record's goal X and Z onto **the unit's own** footprint (the snap/reverse
pair of [R-ORD-01 §1] with this unit's footprint-size pair — the goal is
re-centred on the cell the unit would occupy there); build a point marker at
the snapped goal with **no altitude or radius setter** — arrival is the
default `hypot ≤ 0.5` world units at the terrain-derived Y of
[R-AIR-01 §4]; install; gate `= 0xE0`; advance. Phase 2 (any of the three
arrival bits): when this record has no successor, status 6 (`Arrived`);
complete — the goal-release and payload-replaced bits complete the order
exactly as arrival does, and the caption is emitted only for the last
record. Other phase: cancel-all. There is no re-arm: the air move never
returns 9.

**`VTOL_Patrol`.** Phase 0: mover and `canfly` (else cancel-all); the
patrol-chain setup of [R-ORD-01 §4]; caption clear with `Patrolling`; the
preamble; then inhibit all three slots; advance. Phase 1: clear the five
movement pending bits `0x20`–`0x200` from the record's pending word;
advance. Phase 2, in order: satisfied ∩ `0xE0` → *rotate* (the record moves
to the tail with its phase left at 2, so the next visit re-arms the leg);
build a point marker at `goal − offset(bearing(me → goal), 320)` world
units — 320 units short of the waypoint along the approach — with
horizontal arrival radius `0x150` (336; explicit-radius strict test); install;
gate `|= 0xE0`; then, if health `< (maxdamage >> 2) · 3` (unsigned): collect
the base candidates within `0xF00` for my side ([R-AIR-01 §7]); any → release
the payload, draw `RNG(count)`, spawn `VTOL_Landing` at that candidate at
the head, gate = 0, *restart*; then the opportunity scan of [R-STANCE-01 §3]
(fire-at-will only) → a target that the auto-engage issuer accepts without
the force flag → gate = 0, *wait*; else deadline 30, hold. Other phase:
cancel-all. Because the marker stops 320 units short and the arrival radius
is 336, an air patrol leg is satisfied about 320 units before the authored
waypoint; a patrol with waypoints closer than that rotates every visit.

**`VTOL_MobileBuild`.** Pre-checks as the ground twin: satisfied bit 1 →
refresh the builder interface, complete; satisfied `0x8` → status 7
`Construction terminated`, refresh, abandon. Phase 0: mover and `canfly`
(else cancel-all); caption clear with `Building`; the preamble; advance.
Phase 1: p3 = 0; snap the goal onto the **product's** footprint (definition
p1); point marker at the snapped goal with horizontal arrival radius
`builddistance` (strict `<`), no altitude setter; install; gate `= 0xE0`;
advance. Phase 2: satisfied `0x40` → abandon; validate placement of the
product footprint at the snapped cell (§6.4, medium argument 1); illegal →
p3 = 0: status 7 `Waiting for target area to clear`; p3 > 10: status 7
`Target area was blocked`, abandon; p3 += 1, deadline 30, hold. Legal →
prepare the site, create the product as a nanoframe (owner mine), bind it;
not created → status 7 `Unable to create any more units`, **abandon** (the
ground twin waits 300 ticks and holds); created → status 9 `Starting
construction`, insert `GetBuilt` on the product in queued mode, emit
`StartBuilding(bearing(me → product) − heading)` (relative, as the ground
twin; contrast `VTOL_HelpBuild` of [R-ORD-01 §7]), refresh; advance. Phase
3: **poll** the `INBUILDSTANCE` helper with extra `0xA` and **discard its
result** — when the stance byte is clear it writes gate `= 0xE` as a side
effect, but the work body below runs regardless; the phase then falls into
the shared work body. Phase 4: the work body alone. The work body: on every
tick with `tick mod 150 = 0`, build the orbit marker of §10.3 at `product +
offset(bearing(me → product) + 0xDB6E, builddistance)` — note the **plus**,
the marker is on the far side of the bearing helper's axis from every
`unitPos − offset(…)` leg of [R-AIR-01 §8] — with the marker's heading set
to that bearing (flag `0x40`) and no radius setter; install. Then the work
step with quantum `workertime / 30` (integer division, then float); when it
did work draw the spray from `QueryNanoPiece` to the product's model box.
Product finished (remaining fraction `0.0`) → advance; else deadline 1,
gate `|= 0xA`, hold. Phase 5: status 8 `Building complete`; complete. Other
phase: cancel-all. There is no nanolathe-active stamp and no reach test
after arrival: an aircraft that reached its `builddistance` marker builds
from wherever the 150-tick orbit leaves it.

### Closed — `VTOL_Follow` and `VTOL_SeekGuard` [R-ORD-02 §3] (2026-08-29)

[R-AIR-01 §7] left both open. **Established** by direct trace.

**`VTOL_Follow`** (the air guard). Entry, in order: target null or
satisfied ∩ `0x48` → if this record has no successor, allocate
`VTOL_SeekGuard` with this record's target (null when the target is gone)
and goal and **tail-append** it; complete either way. Off-map recovery of
[R-AIR-01 §5] (marker toward the map centre at 800 world units, arrival
radius `0x80`, gate `|= 0xE0`, hold). Every visit then copies the target's
position into the record goal. Phase 0: mover and `canfly` (else
cancel-all); caption clear with `Guarding`; the takeoff preamble; draw
`RNG(0x10000)` into p1 (the orbit bearing) and its low bit into p2; advance.
Phase 1: inhibit all three slots; advance. Phase 2, four legs in order:

1. *Defend the ward.* Let `a` be the ward's recorded attacker reference (the
   reference the damage intake stores on the victim — **Supported
   inference** for the field's identity; decider: doc 06's damage-intake
   trace naming it). When `a` exists, my player's diplomacy byte toward
   `a`'s side is 0, **the satisfied set has `0x10`**, and `a`'s definition
   index is not in my definition's no-chase category bitset: run the
   auto-engage issuer with the force flag ([R-STANCE-01 §3]); success → gate
   = 0, *wait*. Failure with my fire stance not *hold* (state bits
   `0x300000` nonzero): for slots 0, 1, 2 whose control byte has bits 1 and
   4 (enabled and latched) and whose weapon lacks `commandfire`, when the
   slot's target is empty, or the shot gate refuses it, or its definition
   index is in that slot's bad-target bitset → bind the slot to `a`. Because
   [R-ORD-01 §6] found no producer of pending bit `0x10`, this leg is
   reachable only through that open producer; a reimplementation that
   never raises `0x10` never runs it.
2. *Help the ward.* Nano-reach passes for the ward → resolve code 8 against
   it; a non-empty name → release the payload, spawn it at the head, gate =
   0, *wait*.
3. *Copy the ward's work.* Let `h` be the ward's front-queue head. When `h`
   exists with a nonzero identity, I have `builder`, nano-reach passes for
   **`h`'s target**, the ward has `builder`, `h`'s static-mask copy has bit
   20, and `h`'s target is not me: if `h` is `MobileBuild`, `BuildingBuild`
   or `VTOL_MobileBuild` and has a target → spawn `VTOL_HelpBuild` on `h`'s
   target; else if (`h`'s mask has `0x200` and a target) or (`h`'s mask has
   `0x400`) → take `h`'s identity, substituting the air twin for
   `RepairUnit`, `Reclaim`, `ReclaimUnit` and `HelpBuild`, and spawn it with
   `h`'s target and `h`'s goal. Either spawn releases the payload, inserts
   at the head, gate = 0, *wait*. Any other `h` falls to the orbit.
4. *Orbit.* Satisfied ∩ `0xE0` → `p1 −= 0x4000 + RNG(0x2000)` (a quarter
   turn plus up to 45° further, always subtractive). Radius `r` = my slot-0
   weapon `Range + 0xA0` world units when my state word has bit 31, else
   320. Point marker at `wardPos − offset(p1, r)` with horizontal arrival
   radius `0x80`; install; deadline 30; gate `|= 0xF8`; hold.

Other phase: cancel-all. At most two draws per visit (the orbit step only
after an arrival; the phase-0 bearing once).

**`VTOL_SeekGuard`.** Entry: satisfied `0x40` → complete; the off-map
recovery as above. Phase 0: mover and `canfly` (else cancel-all); a goal of
exactly `(0,0,0)` → goal = own position; draw `RNG(0x10000)` into p1 and its
low bit into p2; advance — **no takeoff preamble**: a grounded seeker stays
grounded until something else lifts it. Phase 1, in order: health `<
(maxdamage >> 2) · 3` (unsigned) → collect the base candidates within
`0xF00`; any → release the payload, `RNG(count)`, spawn `VTOL_Landing` at
the pick at the head, gate = 0, *restart*. Then enumerate the units within
`sightdistance` through the **guard-candidate visitor** ([R-ORD-02 §4]);
non-empty → release the payload, resolve code 7 against the **first** listed
unit and spawn the result at the head **whatever it is** — a reject
resolves to identity 0 and is spawned as such — gate = 0, *wait*. Else the
orbit step of `VTOL_Follow` leg 4 with `r = Range + 0xA0` read from slot 0
unconditionally, about the record's **goal** (not a ward); hold. Other
phase: cancel-all.

### Closed — the scan visitors, the tail append, and the velocity marker's heading [R-ORD-02 §4] (2026-08-29)

**Established.** Four helpers the handler bodies call and no section named:

* **The repair-candidate filter** (the visitor behind `RepairPatrol`'s and
  `VTOL_RepairPatrol`'s candidate gather, [R-ORD-01 §4] and [§7]) admits a
  unit `u` when: `u` is not the scanning unit; `u`'s owner's diplomacy byte
  toward my side is nonzero; `u`'s mover mode is grounded (`1`); `u`'s
  16-bit health is below its `maxdamage` (unsigned) **or** `u` is
  unfinished; and **not** (`u`'s last-damage side byte equals my side and
  its last-damage cause byte is 5) — a unit my side is currently reclaiming
  (cause 5 is the reclaim bite, [R-ORD-01 §5]) is never offered for repair.
* **The guard-candidate visitor** (`VTOL_SeekGuard` phase 1) admits `u`
  when `u`'s owner's diplomacy byte toward my side is nonzero, `u` is not
  `canfly`, and `u` is not the seeker. The list is in enumeration order; the
  seeker takes the first entry, so no draw is made.
* **The tail append** used by the patrol-chain setup and by `VTOL_Follow`'s
  hand-off to `VTOL_SeekGuard`: walk the segment the record's rear-segment
  flag selects to its last link and append; the record's owner is set and
  its next link cleared; no flag is inherited (contrast the head insert of
  [R-ORD-01 §1]).
* **The rectangle goal handle's constructor** ([R-ORD-01 §1]'s rectangle
  installer) stores, in footprint cells, the closed rectangle
  `[originX − myFootX, originX + sizeX] × [originZ − myFootZ, originZ +
  sizeZ]` — the low edges are widened by the **installing unit's** own
  footprint-size pair so that the unit's committed cell counts as inside when
  its footprint touches the target's; `origin` and `size` are the packed cell
  pairs the installer receives. Its arrival predicate is a goal-family
  method owned by §8.3 (movement driver), not restated here.
* **The velocity marker's heading supply** ([R-AIR-01 §8]) writes
  `bearing(unitPos → markerGoal)` and returns 1 unconditionally; its arrival
  and goal update are as stated there.

### Corrections and closures recorded by this unit [R-ORD-02 §5] (2026-08-29)

* **[R-AIR-01 §8], `AirToAir` phase 1.** The text says "Otherwise the leg
  gives up and re-issues a seek order." The traced arm — reached when the
  arrival bits are clear and the scratch counter has reached `0x5A` —
  releases the payload, spawns **`VTOL_Evade`** with the same target at the
  head, zeroes the counter and the gate, and returns *restart*. It is an
  evasion, not a seek; the seek re-issue belongs to the shared entry
  sequence (its step 1). The §10 tail bullet on "the `AirToAir` dogfight's
  third leg" is closed by this; the owner of §10 should strike it.
* **[R-AIR-01 §7]'s Unknown** ("`VTOL_SeekGuard`'s per-phase contract, and
  `VTOL_Follow`'s …") is closed by [R-ORD-02 §3]; the matching §10 tail
  bullet should be struck by that section's owner.
* **§3.4 row 13** ("target hostile and differently owned") — corrected in
  [R-ORD-02 §1]: only the owner test exists.
* **§3.4 row 3** ("suppression for the non-air special case") — the
  non-hostile arm is not a special case of the acting unit but of the
  *target*: any position-only or friendly-target attack from a ground unit
  is `Suppress`, from an aircraft a run; [R-ORD-02 §1] has the order.
* **The rows the ledger carried as `PARTIAL` against `R-ORD-01 §10`** (the
  work-amount seed) cite a section that does not exist; the seed is stated
  in [R-ORD-01 §1] and the rows now cite it.

### R-ORD-02 §6 — what this unit leaves open

- *Closed (2026-08-29, RWU-04-13):* the post-capture unit byte read by the
  contextual resolver's own-unit reject is the countdown doc 05's ownership
  transfer writes as 150; the unit sweep decrements it once per unit visit
  ([R-MOV-03 §1] step 6).
- *Closed (2026-08-29, RWU-06-7):* the ward's recorded-attacker reference that
  `VTOL_Follow` leg 1 defends against is the damage dispatcher's damage-time
  attacker pointer, [06 R-WPN-04 §2].
- What the pump does with a spawned record of identity 0 (`VTOL_SeekGuard`
  spawns the code-7 result unchecked) · [R-ORD-02 §3], §3.1 · static read of
  descriptor 0's handler pointer.

### Closed — the special-behavior FBI keys: storage, reader census, contracts [R-SPEC-01 §0] (2026-08-29)

This block closes RWU-04-7. For each of the keys `kamikaze`,
`kamikazedistance`, `teleporter`, `digger`, `healtime`, `shootme`,
`hidedamage`, `sortbias`, `istargetingupgrade`, `immunetoparalyzer`,
`cloakcostmoving`, `onoffable`, `activatewhenbuilt`, `selfdestructcountdown`,
`showplayername`, `canhover`, `amphibious` and `floater` it states where the
unit-definition parser stores the value, every reader of that storage found by
a whole-image census, and either the reader's contract at [§2.2] precision or a
bounded negative. Contracts that other anchors already state are cited, not
restated.

**Method and bound (Established).** The parser stores these keys in two
32-bit *capability words* of the definition record (called **word A** and
**word B** below) and in three 16-bit fields. The census scanned the exported
decompilation of every function for any load of either word or of the three
fields, in all three renderings the decompiler uses (dword mask, shift-and-and,
byte-narrowed mask), and then read every hit. The bound is the export itself:
a reader that reached the word through a copied pointer whose provenance the
decompiler lost would be missed. Two such pointer-copy readers were found for
`cloakcost`/`cloakcostmoving` by doc 05's own trace (§10 below), which is why
that key is cited to doc 05 rather than censused here.

| Key | Parser storage | Default | Readers (beyond the parser and the definition copy) |
|---|---|---|---|
| `kamikaze` | word A bit 28 | 0 | command resolver code 3; `Attack_Kamikaze` handler; shared target search; damage reaction site; HUD range rings — §1 |
| `kamikazedistance` | signed 16-bit | 0 | `Attack_Kamikaze` handler; HUD range rings — §1 |
| `teleporter` | word A bit 13 | 0 | **none** — §2 |
| `digger` | word A bit 30 | 0 | renderer only — §3 |
| `healtime` | signed 16-bit | 0 | per-tick unit pass only — §4 |
| `shootme` | word A bit 15 | **0** | shared target search only — §5 |
| `hidedamage` | word A bit 14 | 0 | unit-information panel (two sites) — §6 |
| `sortbias` | signed 16-bit | 0 | **none** — §7 |
| `istargetingupgrade` | word A bit 10 | 0 | target-registry rebuild; registry area enumeration — §8 |
| `immunetoparalyzer` | word A bit 26 | 0 | damage packet dispatcher (+ one dead twin) — §9 |
| `cloakcostmoving` | single-precision | parsed `cloakcost` | cloak settlement — §10 |
| `onoffable` | word B bit 2 | 0 | `Activate`/`Deactivate` handlers; minimap contact pass — §11 |
| `activatewhenbuilt` | word A bit 18 | 0 | pre-built creation; build completion — §12 |
| `selfdestructcountdown` | word B bits 20–22 | 5 | `SelfDestruct`/`SelfDestructFG` handler only — §13 |
| `showplayername` | word B bit 17 | 0 | **none** — §14 |
| `canhover` | word A bit 12 | 0 | [R-MOV-01 §8a], §8.1, §9.2, resolver hover attack, two dead helpers — §15 |
| `amphibious` | word A bit 21 | 0 | repair admission; a dead placement validator — §15 |
| `floater` | word A bit 19 | 0 | [R-MOV-01 §8a], cargo deck clamp, pitch cap — §15 |

Every flag is stored as `(parsedInteger & 1) << bit`, so an authored value of
`2` stores as 0 — only the low bit of the integer reader's result counts. The
16-bit fields take the integer reader's low 16 bits. The
`selfdestructcountdown` field is the exception: it is read through the bare
string-to-integer conversion and stored as `value & 7`, with the 5 written
only when the key is **absent** (§13).

### Closed — `kamikaze` and `kamikazedistance`: the trigger predicate, who dies, and the damage kind [R-SPEC-01 §1] (2026-08-29)

**Established — how the order is chosen (§3.4 code 3, the attack resolver).**
After the can-attack gate, the resolver's code-3 arm tries the *armed*
variants first, and only when the acting unit's state-word bit 31 (the
"has an aimable weapon" bit that `Attack_Chase` phase 0 also requires,
[R-ORD-01 §3]) is set: the suppression/air variants, then `Attack_Chase` when
the unit has a mover, then `Attack_NoMove` when state bit 29 is set. Only when
none of those returned does the arm test `kamikaze` and resolve
`Attack_Kamikaze`; without `kamikaze` the code-3 request is rejected. So a
definition that authors both a working weapon and `kamikaze` chases and
shoots; the kamikaze variant is reached by definitions whose state bit 31 is
clear — in stock content, the ones with no weapon of their own.

**Established — the trigger predicate is the point-goal arrival test, not a
separate distance compare.** The handler [R-ORD-01 §3] installs a point goal at
the target's *current* position with radius `max(16, kamikazedistance)` (the
16-bit field read signed; a zero or negative value therefore yields 16 world
units), re-issuing it on every visit that finds the target still alive, and
fires only on the goal's *satisfied* bit — the mover's own arrival test
against that radius ([R-ORD-01 §1] point goal). Nothing in the handler measures distance
itself; a kamikaze unit that cannot path to within the radius never fires.

**Established — who dies, and with what damage kind.** On arrival the handler
spawns `SelfDestruct` with p1 = 1 at the head. That record's first visit
applies **30000** damage to the unit itself with damage cause **3**
([R-ORD-01 §2]) through the standard damage funnel: the armored-state
reduction is skipped because it applies only to amounts strictly below 30000
([06 §9.2]); the veterancy scale applies as for any packet, so the self-inflicted
amount is 30000 at zero kills and `30000 × (25 − v) × 4 / 100` with `v` the
unit's kill tier ([06 §9.2]) — never less than 24000, always lethal for a stock
kamikaze definition. Health falls below 1 and the death latch sets; the death
path then resolves the definition's `selfdestructas` weapon **because the
cause is 3** ([R-DMG-01 §5]) and it is that weapon's blast that damages the
target. The target receives nothing from the order itself: no direct damage,
no packet, no callback. A `kamikaze` definition whose `selfdestructas` resolves
to a weapon with no area of effect therefore kills only itself.

**Established — `kamikaze` widens autonomous targeting.** In the shared
unit-level target search ([06 §3.2] "Each picked candidate must then pass"),
test 3's *shooter* flag is `kamikaze`: a kamikaze definition bypasses the
§3.1 physical gate (range, arc, minimum range) for every candidate, so any
registered enemy within `sightdistance` of an idle fire-at-will kamikaze unit
is a candidate. That closes the "authored key behind that bypass flag"
Unknown in [06 §3.2] (cross-doc: doc 06 to cite). The damage-reaction site
also admits a kamikaze victim as if armed ([R-STANCE-01 §3]).

**Established — the HUD range rings.** The per-unit range-ring pass
([R-P0-11 §3]) draws two kamikaze rings. In the labelled mode, when
`kamikazedistance` is non-zero, a ring of that radius labelled with the key
name. In the unlabelled mode, only when `kamikaze` is set **and** the
definition's `explodeas` resolved: a pulsing ring of radius
`clamp(((tick mod 60) × h × 2) / 60, 8, h)` where `h` is half of a 16-bit field
of the `explodeas` weapon record, plus — when the unit has a mover — a
second ring at `kamikazedistance`. **Supported inference:** the weapon field is
`areaofeffect` (the only 16-bit radius-shaped field a self-destruct ring would
want); *decider:* match the weapon parser's store against the ring drawer's
load. Presentation only; no simulation effect.

### Closed — `teleporter` is inert; the `Teleport` order is ungated and free [R-SPEC-01 §2] (2026-08-29)

**Established — reader census: none.** Word A bit 13 is written by the parser
and copied by the definition copy; no function in the image loads it. The
`Teleport` order is produced by command code 11 with **no capability gate**
(§3.4) and its handler [R-ORD-01 §2] reads no definition flag: any unit given
code 11 with a position runs it. There is no pairing — the goal is the
order's position, not another gate — no cost (the handler makes no economy
call and writes no player stock), and the teleporter itself never moves. In
stock content the key marks the Galactic Gate for the front end and mission
scripts only; the engine's teleport behavior does not depend on it.

### Closed — `digger` is presentation only [R-SPEC-01 §3] (2026-08-29)

**Established — reader census: renderer only.** Word A bit 30 is read by the
model composition pass and nowhere else: it adds 75 to every vertex height
key and applies the fixed clip at 125 ([03 §2.4], the R-REN-03A waterline and
digger clipping paragraph). No simulation, movement, targeting or LOS reader
exists; a `digger` definition is hit, seen and pathed exactly as a non-digger.

### Closed — `healtime` [R-SPEC-01 §4] (2026-08-29)

**Established.** The signed 16-bit field has exactly one reader, the per-tick
unit pass's self-heal branch, whose cadence (`tick & 7 == 0`), amount (the
clamped worker quantum from `(healtime × 8) / 30`), energy charge and player
gate are stated in [R-WORK-01 §3] ("`healtime`, the only consumer"). The
branch runs in the general unit update after the water-damage test and before
the cloak settlement of the same pass; it draws no random number.

### Closed — `shootme` gates autonomous targeting by human players; its default is 0 [R-SPEC-01 §5] (2026-08-29)

**Established — the one reader.** Word A bit 15 is read only by the shared
unit-level target search ([06 §3.2]), as test 2 of the per-candidate gate: a
picked candidate is admitted when **its** definition has `shootme`, **or** the
searching unit's owning player has controller type 2 (a computer player),
**or** a session option bit is set. That closes the "authored key behind that
definition flag" Unknown in [06 §3.2] (cross-doc: doc 06 to cite). The
consequence for content: a definition that omits `shootme` is never picked
up by a human player's fire-at-will scan, guard scan, `Wait` scan or
retaliation, but a computer player's units target it freely, and any player
may attack it by explicit order (the resolver does not read the flag).

**Established — the default is 0, not 1.** The parser reads `shootme` with the
integer reader and a default argument of zero. `research/formats/fbi.md`'s
caveat "`ShootMe` behaves as 1 by default" is corrected there; stock
definitions author `ShootMe=1` explicitly, which is why the absence was never
observed.

**Unknown — the option bit.** The third admission is a bit of a session option
byte that no located writer sets; whether a front-end setting or a mission
key produces it is *Unknown* — *decider:* xrefs on the option byte's writers
(the options loader, RWU-02-1).

### Closed — `hidedamage` hides the health bar from other players [R-SPEC-01 §6] (2026-08-29)

**Established — two readers, both in the unit-information panel.** The panel
drawer draws a unit's health bar only when `unit.ownerSlot == localPlayerSlot`
**or** the definition lacks `hidedamage`; the test appears twice, once for the
selected unit and once for the unit under the cursor (the latter also gated on
the local visibility predicate). The bar itself is
`clamp(health, 0, maxdamage) × barWidth / maxdamage` in whole pixels. No other
reader: allies see no bar either (the test is on the local slot, not on
alliance), and nothing in the simulation, targeting or AI reads the flag.
Cross-doc: doc 07 owns the panel layout and should cite this.

### Closed — `sortbias` is inert [R-SPEC-01 §7] (2026-08-29)

**Established — reader census: none.** The signed 16-bit field is parsed and
copied by the definition copy; no function loads it. It does not affect build
menu order, selection order or draw order. (`research/formats/fbi.md`'s row
"Read by the engine; effect unconfirmed" is superseded by this census;
orchestrator to reword.)

### Closed — `istargetingupgrade` lets a player's units acquire radar-only contacts [R-SPEC-01 §8] (2026-08-29)

**Established — the producer.** The per-player target-registry rebuild
([06 §3.1], [08 R-AI-01 §16]; 30-tick cadence, one simulation draw per
rebuild) clears the registry's *targeting-upgrade* flag and then, walking the
whole unit array, sets it when it meets a unit that is alive and not dying,
**owned by that player** (the unit's side relation byte equals the player's
own), **complete** (remaining build fraction exactly 0.0) and **activated**
(state byte bit 0), and whose definition has `istargetingupgrade`. Being
built, being deactivated, or being paralysed (which does not clear bit 0)
therefore matters only through bit 0.

**Established — the consumer.** The registry area enumeration — the routine
that `Wait`, `Guard_NoMove` and the shared target search use to list enemies
within a radius ([R-ORD-01 §1], inclusive `d² ≤ r²` in whole world units) —
scans the registry's *visible* list first. Then, **only if the targeting-upgrade
flag is set and that scan produced no candidate**, it scans the registry's
second list with the same radius test and the same alive/not-dying filter. The
second list is filled by the rebuild with every non-allied unit carrying
state-word bit 8 — the radar-detected bit that the radar emitters set and the
jam callback clears ([R-VIS-01 §5]) — regardless of the visibility predicate.
So with a targeting upgrade active, every autonomous scan of that player falls
back to radar contacts when nothing visible is in range; explicit orders never
consulted the registry and are unchanged. This closes the "its reader is
Unknown" item of [08 R-AI-01 §16] (cross-doc: doc 08 to cite). A second
accessor that returns the flag for a player slot exists but has no caller.

### Closed — `immunetoparalyzer` [R-SPEC-01 §9] (2026-08-29)

**Established.** Word A bit 26 has one live reader, the damage packet
dispatcher's stun-eligibility test 3 ([06 §10]); an uncalled twin of the
paralyze-task installer performs the same test on a second entry point that
nothing reaches. No other reader: the flag does not affect the weapon's
shot admission (a paralyzer still fires at an immune target and still runs
the preliminary side effects), and it is not read by the AI.

### Closed — `cloakcostmoving` [R-SPEC-01 §10] (2026-08-29)

**Established.** Default, selection and conversion are [R-PROD-01 §7]; the
parser detail this unit adds is only that the default is the *integer* just
parsed for `cloakcost` (before that value's own conversion to single
precision), so the two keys can never differ by a fraction. The whole-image
census of this unit found no direct load of the field; the settlement reads it
through a pointer to the definition that the decompiler does not attribute,
and doc 05's trace is the authority.

### Closed — `onoffable` [R-SPEC-01 §11] (2026-08-29)

**Established — three readers.** The `Activate` and `Deactivate` handlers
([R-ORD-01 §2], [R-PROD-01 §2]) and the minimap contact pass's selected-unit
circle gate ([03 §3.9]: circles when active **or** not `onoffable`). Nothing
else: the COB `ACTIVATION` port, the pre-built creation path and the
completion path write the activated bit without consulting `onoffable`.

### Closed — `activatewhenbuilt`: the two completion-time activation sites and their ordering [R-SPEC-01 §12] (2026-08-29)

**Established — two readers, one effect.** Both raise state-byte bit 0
through the shared edge setter, which on the rising edge runs the COB
`Activate` callback (asynchronous, no arguments) and emits status kind 3, then
refreshes the unit's derived state and — for a controlled owner — forwards the
new state byte as a 4-byte network event ([R-UNIT-06 §2]). The sites:

1. **Pre-built creation.** The unit creation service, when called with its
   *already built* argument, runs in this order after the record is placed:
   the mover/attach setup, the Y placement and the occupancy stamp; a
   creation notification gated on one definition byte this unit did not
   identify; **`activatewhenbuilt`
   → raise bit 0**; then `isfeature` → mark the unit as a feature stand-in
   (death cause byte 7, dying bit set); then the per-player unit counters. So a
   pre-built `activatewhenbuilt` unit receives `Activate` before it is counted
   and before any order runs.
2. **Build completion.** The completion service (called from the
   `BuildingBuild` handler [R-P0-09], the build-progress helper and the
   network completion event) requires builder and product alive and the
   builder's definition to have a build list; it then clears the product's
   remaining fraction, sets state-word bit 13, and for a controlled owner:
   if state bit 29 is clear and the product has a carrier link, detaches it;
   if bit 29 is set, refreshes the builder GUI. **Then `activatewhenbuilt` →
   raise bit 0**; then, when the product is the locally selected unit, the
   order panel is refreshed; then `isfeature` as above; then the ownership
   notification for a controlled owner; finally a shared interface flag is
   set when either unit carries state bit 4. `Activate` therefore precedes
   the product's first order pump, and a factory product with
   `activatewhenbuilt` is active before `Ground_Unload`/rally handling begins.

Without the flag neither site touches bit 0 and the unit stays inactive until
an `Activate` order (needs `onoffable`, §11) or the COB `ACTIVATION` port sets
it. `activatewhenbuilt` is not re-read later: a unit deactivated afterwards is
not re-activated.

### Closed — `selfdestructcountdown` and the full self-destruct timeline [R-SPEC-01 §13] (2026-08-29)

**Correction (parser).** [R-ORD-01 §2] left values 6 and 7 *Unknown* with
"decider: the FBI parser's clamp". There is no clamp: when the key is present
the parser stores `value & 7` in word B bits 20–22, and writes 5 only when the
key is **absent**. So `selfdestructcountdown=0` is stored as 0 (immediate),
`8` stores as 0, `9` as 1, and 6 and 7 are stored as authored.

**Established — what 6 and 7 do.** The handler emits status kind `22 − n` for
the remaining count `n`. Kinds 17…22 carry the captions `five`…`zero` with
their `count5`…`count0` sounds. Kind 16 is the *capture* status: no caption
text in the table, the capture sound (what the emitter shows for a textless
kind is not traced here). Kind 15 is the *Visible* status: the caption
`Visible` with the uncloak sound. A countdown of 7 therefore announces
`Visible` (uncloak sound), then plays the capture sound with no caption, then
`five`…`zero`; a countdown of 6 starts at the capture sound. Each step is 30
ticks; nothing else differs.

**Established — the timeline, end to end.** Issue (`d` button → `SelfDestructFG`
on the front segment; script/AI → `SelfDestruct` on the rear segment;
[R-ORD-01 §0], [R-ORDER-02 §1]). Visit 1: p2 initialised from the field; with
p1 = 0 and a non-zero field, the caption for `n` = the field, deadline 30.
Every 30 ticks the count falls by one and the next caption is emitted; at
`n = 0` the caption is `zero`, the deadline is `RNG(15)` (0–14 ticks, one
simulation draw) and p1 becomes 1. The next visit — p1 = 1, or the field was
0 from the start, or the record was spawned with p1 = 1 by `Attack_Kamikaze`
or `Standby_Mine` — applies 30000 self-damage with cause 3 (§1 above for the
funnel arithmetic) and completes. The death path resolves `selfdestructas`
for cause 3 and `explodeas` for every other cause ([R-DMG-01 §5]); the corpse
and score consequences are doc 06's. Re-issuing the order while it counts
sets the cancel-current bit; the next visit emits `Self destruct terminated`
(status 23) unless the record carries auto flag 14, and completes without
damage. A cancelled countdown cannot be resumed; a new order starts from the
field's value. The unit keeps moving, firing and building throughout — the
record blocks nothing on the front segment (rear-segment `SelfDestruct`) or
only its own segment (`SelfDestructFG`).

### Closed — `showplayername` has one reader: the HUD footer name line [R-SPEC-01 §14] (2026-08-29; corrected 2026-08-29)

**Correction.** This section previously said "reader census: none — the key
is a content annotation only". That was wrong: the census had bounded itself
to the unit-information panel. The battle footer's name line is a reader: in
session kind 3 (multiplayer) a hovered unit whose definition has word B bit 17
(`showplayername`) or bit 18 (`commander`) set shows the **owning player's
lobby name** instead of the definition name; in every other session kind, and
for every other unit, the definition name is shown. The arithmetic and the
priority rules are in [07 R-HUD-03 §2]. The `UNITINFOx` panel still never
substitutes the name. Established (direct static trace of the footer writer).

### Closed — `canhover`, `amphibious`, `floater`: readers not previously censused [R-SPEC-01 §15] (2026-08-29)

**`canhover` (Established).** Readers: the Y-placement gate and the four
branches, the hover bob and the pitch-cap halving ([R-MOV-01 §8a],
§8.1's pitch cap); the water-damage exemption (§9.2); the attack resolver's
hover variant (`AirToGroundHover`, §3.4 code 3); plus two helpers with no caller (a Y-placement twin and a
hover-attack twin) that are dead code. No further reader.

**`amphibious` — correction to §9.2.** §9.2 says "the `amphibious` bit is
parsed and stored but has no reader in the bounded recovered movement,
medium, targeting, or transport code". That bound was too narrow: the bit has
two readers. (1) The **repair admission** water clause, already stated in
[R-ORD-01 §7]: an aircraft (`canfly`) that is not `amphibious` will not repair
a target whose top is under water. (2) A **placement validator** variant that
computes the minimum acceptable cell height as `seaLevel − maxWaterDepth` and
raises it to `seaLevel` when the definition is `canfly` and not `amphibious`;
that variant's only caller is itself uncalled, so it is dead in the shipped
image. The movement side of §9.2 stands: no mover, medium or transport code
reads the bit.

**`floater` (Established).** Readers: the Y branch and the pitch-cap halving
([R-MOV-01 §8a], §8.1's pitch cap); the carried-cargo deck clamp — a carried
`floater` never sits below `(seaLevel − waterline) << 16` even when the
carrier's attach piece would put it there (the cargo slaving branch of
§10.2). No further reader; in particular the pathfinder and the target search do not
read it.


## 4. COB loader, VM, threads, and script timing

### 4.1 Loading and binding

**Established fact:** A compiled script file is read whole into one allocation and relocated in place; its header, tables, and record layout are specified in document 02. The loader binds one compiled script object to each unit definition, caches it by file, and stamps it with the content checksum that save/load validation later compares.

**Established fact:** Each live unit receives a COB VM instance. The VM has eight execution threads, per-thread status/PC/stack/sleep/wait/caller/signal state, static variables, and per-piece animation state. Thread stack depth is limited to ten values by the retail layout; no safe expansion behavior is demonstrated.

**Established fact:** The loader allocates script statics and piece states when binding. Save/load validates the expected blob size and a cache/signature value. Static variables and piece animation state are represented in the save payload. Fractional movement deltas are not saved; the next tick resumes from integer state.

**Unknown:** Some anonymous save fields and exact failure behavior of allocation or corrupted piece indexes remain unresolved. Retail may abort through its allocator where a clean implementation should terminate the affected script deterministically.

### 4.2 Thread scheduler

**Established fact:** The unit phase drains each unit's eight script threads. The interpreter runs before per-piece interpolation in that unit's script drain. A move or turn issued by a script therefore affects the same tick's interpolation; a wait that becomes satisfied during interpolation is observed by the next drain.

**Established fact:** The engine reaches scripts through four adapter roots: the zero-argument name-form start, the argument-carrying name-form start, the argument-carrying slot-form start, and the name-form synchronous four-cell query (direct-static: the name-form roots resolve through the shared name-lookup helper and the slot-form roots through the shared slot-lookup helper). The zero-argument name-form start is a name-based zero-argument start (15 direct call sites); the argument-carrying name-form start is name-based, taking up to four arguments, resolving the name, then forwarding to the argument-carrying slot-form start (21 direct call sites); the argument-carrying slot-form start is slot-based, writing four physical cells then setting the logical top to `arity−1` (5 direct call sites: four producers outside the adapter roots plus the argument-carrying name-form forwarding); and the name-form synchronous query forwards to the query interpreter (14 direct call sites). A bounded call census over the code sections counts 55 direct calls to those four roots; one is the internal argument-carrying name-form→slot-form forwarding, so 54 are producer call sites outside the four roots. The adjacent slot-based zero-argument helper has no direct caller in the bounded census (negative-bounded). The fixed-name producer census yields 40 distinct case-sensitive callback names; the packet path `0x0E` (section 5.3) can additionally start an authored slot by index and is not limited to those 40 names.

**Established fact:** Each VM has eight thread slots of `0xA4` bytes. Allocation takes the first inactive slot in ascending order. A new root thread begins runnable at the selected function entry with logical stack top `−1`, no completion receiver, and signal mask `1`; child script starts inherit their parent's current mask. There is no name-level or producer-level duplicate suppression in the adapters — repeated successful starts occupy independent slots, and any repeat suppression lives in the producers' own state caches. Invalid identity (name lookup `−1` or slot out of range) and a full eight-slot pool are the same allocation failure to the adapters: the zero-argument name-form start and the slot-based zero-argument helper return false without notifying a supplied receiver, while the argument-carrying slot-form start and the argument-carrying name-form start return false and, if a receiver is supplied, invoke it with `0`. `HitByWeapon` and `TakeDamage` are independent argument-carrying name-form starts and can fail separately; `Aim*` failures also deliver `0` via that path and therefore leave aim-ready clear (section 5.3).

**Established fact:** Entry modes are D, I, and Q. D (deferred) allocates and initializes a thread then returns without interpreting it. I (immediate) after allocation interprets all eight slots once with delta `0` in slot order, then runs one piece pass with delta `0` — a VM-wide drain, not a drain of only the new callback. Q (synchronous query) interprets only the newly allocated slot with delta `0`, snapshots up to four cells, and returns; it does not scan the other seven slots and does not run a piece pass. The zero-argument name-form start takes zero logical arguments; the argument-carrying slot-form start always writes four physical values to cells `0..3` then sets logical top to `arity−1`; the synchronous query roots treat null cell pointers as seeded zero and excluded from copy-back, while non-null cells are logically exposed. A Q callback is not guaranteed to have returned when the host snapshots it: if it sleeps, waits for move/turn, or waits for another script, the one-slot interpretation stops and the host copies the current four cells immediately; the blocked thread remains active and may resume in a later VM-wide or normal drain, but it has no completion receiver that can revise the already returned host values, so callers must pre-initialize outputs and handle partial results.

**Established fact:** Thread states include idle, running, waiting for turn, waiting for move, sleeping, and waiting for a called script. Signal masks can terminate or suppress matching threads. Calls block the caller until the callee returns.

**Established fact:** `SET_SIGNAL_MASK` (`0x10068000`) replaces the current thread's mask with the popped value. Engine-created root threads begin with mask `1`. `SIGNAL` (`0x10067000`) pops a mask and scans all eight slots; every active thread whose mask intersects it is released, including the signalling thread itself, each decrementing the active count and waking every thread waiting for that slot. Signal termination never invokes a completion receiver; if the signalling thread is among the victims its interpretation stops. An explicit script `return` (`0x10065000`) pops the top value, delivers it to the thread's completion receiver when one is set, releases the slot, and wakes threads waiting for that slot. An invalid-opcode kill clears status and decrements active count but does not invoke a receiver.

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

### Closed — the reserved opcode, exactly [R-COB-04 §6] (2026-08-29)

**Established.** Opcode `0x10063000` reads its count from the second operand
word, then pops that many values from the thread's window into a **four-word
temporary in the interpreter's own stack frame**, filling it from the highest
index downward, and advances the program counter by three. A count of zero or
less pops nothing. The pops do not test the logical top, so a count above the
thread's current depth reads window words below the base (stale slot memory,
[R-COB-01 §1]) and lowers the top below −1; a count above **four** writes
past the temporary into the interpreter's other locals — undefined behavior
in retail, not a thread kill and not a fault the engine detects. The
"Missing and unknown" bullet that asked for this is closed: the bound is
four, and beyond it retail's behavior is unspecified. Nanolathe should treat
a count above four (or above the depth) as a script fault and stop the
thread, recorded as a sanctioned divergence. No shipped script emits the
opcode (`[fmt cob]`, "Reserved / unassigned slots").

### Closed — statics start as raw heap memory [R-COB-04 §7] (2026-08-29)

**Established.** The tagged allocator the program bind uses for the statics
array is a thin wrapper over the C runtime's `malloc`: it takes the runtime's
heap lock, allocates from the small-block heap or the main heap, and loops
through the new-handler on failure. **No path clears the block.** The only
fill it can perform is the runtime's debug-fill hook, which is gated on a
runtime flag that the release runtime leaves at zero. So the initial content
of a unit's script statics is whatever the heap block last held — for a
fresh block, the operating system's page contents; for a recycled block, the
previous tenant's bytes. This closes the [R-COB-01 §1] Unknown: retail
provides **no** defined initial value. Shipped scripts write every static
before reading it, so stock behavior is unaffected; Nanolathe zeroes statics
at bind and records that as a determinism divergence, exactly as
[R-COB-01 §1] already prescribes.

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
20; an identifier outside that range reads zero. **The set is exactly twenty
wide and complete** — the switch is a dense jump table with twenty entries and
no arm above 20, so every port name a later engine or a community header adds
(`MY_ID`, `MIN_ID`, `MAX_ID`, `UNIT_TEAM`, `UNIT_ALLIED`,
`UNIT_BUILD_PERCENT_LEFT`, `VETERAN_LEVEL`, `CURRENT_SPEED`, `IN_WATER`,
`SMOKEUNIT`, …) reads zero in retail rather than doing anything
[R-COB-03 §1]. The value-writing opcode routes into a parallel switch over the
same identifiers, and an identifier with no write arm only sets the unit's
script-touched marker. Every write arm sets that marker in addition to its own
effect, and so does the fall-through for every unbound identifier. The two
remaining read opcodes are transport queries and are not part of this table.

All reads take the script's **own** unit except ports 9, 10 and 11, which take
a unit identifier in the first argument; ports 7 and 8 take a piece index
there, ports 12, 13 and 16 a packed coordinate pair, and ports 14 and 15 two
independent values. Every other port ignores all four argument slots. Values
below are as pushed back on the script stack.

| Id | Name | Read — exact expression, units, default | Write |
|---:|---|---|---|
| 1 | activation | the activation bit of the first state byte, 0 or 1. Zero at unit creation | drives the **activation edge machine**, which fires the Activate and Deactivate callbacks re-entrantly |
| 2 | standing move orders | bits 18–19 of the unit state word, a **two-bit** field, values 0 to 3. Seeded at creation from the definition's `standingmoveorder` (`[fmt fbi]`, default **2**) | ignored |
| 3 | standing fire orders | bits 20–21 of the same word, a **two-bit** field, values 0 to 3. Seeded at creation from `standingfireorder` (`[fmt fbi]`, default **2**) | ignored |
| 4 | health | `(uint32)((int16)health * 100) / (uint32)maxdamage`, an **unsigned** 32-bit division of a signed product, giving 0 to 100. `health` is seeded to `(uint16)maxdamage` for a unit created complete and to 0 for a nanoframe. A definition without `maxdamage` divides by zero — see the edge note below | ignored |
| 5 | in build stance | bit 0 of the second state byte, 0 or 1. Zero at creation | sets the bit from the low bit of the value |
| 6 | busy | bit 1 of the second state byte, 0 or 1. Zero at creation | sets the bit from the low bit of the value |
| 7 | piece position XZ | the piece's world position packed as `(X & 0xffff0000) + (Z >> 16)` — **X in the high half, Z in the low half**, both in whole world units, the low half sign-extended and *added* (see [R-COB-03 §3]). An out-of-range piece index, or a unit with no render table, contributes a zero offset, so the port returns the unit's own packed position | — |
| 8 | piece position Y | the piece's world Y as a raw 16.16 value, not shifted; same zero-offset fallback | — |
| 9 | unit position XZ | the named unit's position packed the same way as port 7. The identifier is masked to 16 bits and indexes the unit pool with **no upper-bound check**; a zero identifier, or a slot whose alive bit is clear, reads zero | — |
| 10 | unit position Y | the same unit's Y as a raw 16.16 value, same gates | — |
| 11 | unit height | the definition height of the unit selected by the identifier, via the same unit-table lookup and alive gate as ports 9 and 10; identifier zero — including the zero-filled argument slot of a bare `get` — reads zero. **Audit note:** this corrects the earlier "own definition's height value" wording [R-P0-10] | — |
| 12 | relative bearing | unpacks the argument (see [R-COB-03 §3]), takes `atan2(X, Z)` scaled to the 65,536-per-circle domain under round-to-nearest, **subtracts the unit's own heading** as a 16-bit signed subtraction, and masks to 16 bits | — |
| 13 | distance | `trunc(hypot(X, Z))` over the unpacked 16.16 halves — a 16.16 result, truncated toward zero | — |
| 14 | arc tangent | `atan2(arg1, arg2)` in the same scaled domain, masked to 16 bits with no heading subtraction | — |
| 15 | hypotenuse | `trunc(hypot(arg1, arg2))` with both arguments taken as signed 32-bit and no unpacking | — |
| 16 | ground height | the shared terrain height query at the unpacked X and Z, **shifted left 16** into 16.16. The query returns −1 off-map, so an off-map read is `−0x10000` (−1.0), not zero | — |
| 17 | build percent left | from the remaining-build fraction *f*: zero when *f* compares exactly equal to `0.0f`, otherwise `1 - trunc(f * -99.0f)`. *f* is `1.0f` for a fresh nanoframe (reads 100) and `0` once complete (reads 0) | ignored |
| 18 | yard open | bit 2 of the second state byte, 0 or 1. Zero at creation | a gated admission — see §4.7 |
| 19 | bugger off | bit 3 of the second state byte, 0 or 1, a plain flag, not a queued request. Zero at creation | sets the bit from the low bit of the value |
| 20 | armored | bit 1 of the first state byte, 0 or 1. Zero at creation | drives the armor edge event |

The two angle ports are the **only** place in the port arithmetic that rounds
rather than truncates: the arc tangent is scaled by 65,536 divided by two pi
(10430.37835047) and stored under the x87 round-to-nearest-even mode.
Everything else truncates toward zero.

**Edges.** Port 4's divisor is the definition's `maxdamage` taken as a full
32-bit value, and the division is unguarded: a definition that omits the key
(parsed default 0) makes the port a processor divide fault, the same retail
outcome class as §4.6's zero tick denominator. Ports 9, 10 and 11 mask the
identifier to 16 bits and scale it by the record stride with no comparison
against the pool capacity (§2.3), so an identifier past the end of the pool is
an out-of-bounds read in retail, admitted or rejected by whatever the alive bit
reads at that address — undefined behavior, not a defined zero. Ports 7 and 8
*do* bounds-check the piece index, unlike the piece-motion opcode family
(§4.6).

The transport queries are: one that pops a unit identifier, walks the unit's
own cargo list, and pushes one or zero; and one that takes no argument and
pushes **the identifier of the unit carrying this unit**, or zero when it is
not being carried. **Audit note:** the second was previously described as
pushing "the first cargo identifier"; it reads the carrier back-pointer, not
the cargo list head [R-COB-03 §5]. Neither appears in shipped content.

**Attach** resolves the cargo identifier through the unit table with the alive
gate, requires the candidate's carrier field to be empty or already this unit,
and then binds it to the named piece. **Drop** requires the candidate's carrier
to be this unit, asks the placement service for permission, and then unbinds
it using the reserved "no piece" index. Both commit through the same relink
step, which is driven by an event record rather than applied inline
[R-COB-03 §5].

**The one-argument effect opcode is presentation only.** It is gated on the
local player being able to see the unit, touches no simulation state, and
dispatches on the effect type. Vector types 0 through 5 build **two** world
points from the piece's first two transformed vertices; point types build one
from the piece's cached world offset. Types 0 and 1 go to one effect family
differing only in a selector value; 2 and 3 go to a second family differing
only in a magnitude; 4 and 5 repeat 2 and 3 with the two points exchanged.
Point types use the piece world position: `0x101` spawns white smoke, `0x102`
black smoke, and `0x103` a third family whose second point is the first with
its height forced to the map sea level. Every other type — vector types from 6
upward, `0x100` itself, and everything from `0x104` up — falls through with no
case and is ignored. **Audit note:** the earlier grouping ("0 and 1 form the
wake pair, 2 and 3 a thrust-class pair") named the wrong pair for each family:
by `SFXTYPE.H` (`[fmt cob]`) 0 and 1 are the VTOL/thrust pair and 2 and 3 are
the wake pair, which is also the order the dispatch groups them in
[R-COB-03 §6].

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

### Closed — the explode opcode's flag bits and debris record, exactly [R-COB-04 §1] (2026-08-29)

**Correction to the paragraph above.** It said the three draws bounded at
3,000 are "the horizontal velocity words" and that the draw bounded at 10 is
"immediately overwritten and therefore dead". Both readings were wrong: the
earlier trail misread two stores to neighbouring stack slots as one. The three
3,000-bounded draws are the debris piece's **per-tick angular rates**, and the
10-bounded draw is its **upward velocity**, stored in its own word and used
every tick. The draw count (six) and order stand.

**Established — which script flag bits the engine reads.** Of the
`[fmt cob]` `EXPTYPE.H` values the adapter tests exactly bits 0 (`SHATTER`),
1 (`EXPLODE_ON_HIT`), 2 (`FALL`), 3 (`SMOKE`), 4 (`FIRE`), 5 (`BITMAPONLY`)
and the six bitmap bits 8–13 (`BITMAP1`–`BITMAP5`, `BITMAPNUKE`). Bits 6, 7
and 14 and above are never examined. There is no "no heat cloud" flag in
retail; that is a later engine's invention.

**Established — the physical branch (bit 5 clear).** The adapter builds a
twelve-word debris record on its stack and, in this order, draws from the
simulation stream: `rate1 = random(3000)`, `rate2 = random(3000)`,
`rate3 = random(3000)`; `vx = (20 − random(40)) · 2^14`; `vy = random(10)
· 2^16`; `vz = (20 − random(40)) · 2^14`; then writes a lifetime of **900**
ticks. In 16.16 world units per tick that is a horizontal velocity in
`−4.75 … +5.00` per axis, an upward velocity in `0 … 9`, and angular rates in
`0 … 2999` of the 65536-per-circle domain. The record also carries the unit
and the piece index, a *shatter* word (1 when bit 0 is set, else 0), and an
**engine flag word** rebuilt from the script bits: engine bit 0 = `FIRE`,
bit 1 = `SMOKE`, bit 2 = `SHATTER`, bit 3 = `FALL`, bit 4 = `EXPLODE_ON_HIT`
when not shattering, bit 5 = `EXPLODE_ON_HIT` when shattering. Engine bits
6–31 are stack residue — the adapter masks the low six bits of an
uninitialized slot and ORs the rest through — but no consumer tests any bit
above 5 (established by reading every consumer below), so the residue is
inert. The record is then handed to the debris spawner ([R-COB-04 §2]),
which takes the shatter path ([R-COB-04 §3]) when engine bit 2 is set.

**Established — the piece afterward.** The spawner's first act, on both the
whole-piece and the shatter paths, is to **clear the piece's draw flag** in
the unit's render-piece record (bit 0 of the flags byte of §4.3) — the
piece is hidden, and only a later `show` restores it. The script's own
piece-animation state is untouched. `BITMAPONLY` explosions do not hide the
piece.

**Established — the bitmap branch (any of bits 8–13).** Independently of the
physical branch, the adapter resolves the piece's world position through the
same piece-transform helper the `PIECE_XZ`/`PIECE_Y` ports use
([R-COB-03 §2]) and, for each set bit in ascending order, spawns one bitmap
explosion ([R-COB-04 §4]) with the animation named by the bit: bit 8
`explosion`, bit 9 `explode2`, bit 10 `explode3`, bit 11 `explode4`, bit 12
`explode5`, bit 13 `nuke1`. These are entries of the common animation set
loaded at startup (doc 03 owns the file), not weapon definitions; **no weapon
definition and no TDF key is involved anywhere in `explode`** — the
`explodepiece` and `CalcedExplosion` strings are allocator tags
([R-COB-04 §5]).

### Closed — whole-piece debris: lifecycle, bounce, and explode-on-hit [R-COB-04 §2] (2026-08-29)

**Established — spawn.** Debris lives in a fixed table of **100** slots
backed by a **100,000-byte** ring arena. The spawner takes the first empty
slot (none → the piece is hidden but no debris exists) and asks the arena for
`vertexCount · 12 + 0x66` bytes. The arena allocator is *evicting*: when the
request does not fit before the wrap point it frees whole older blocks from
its cursor forward — clearing each victim's owning slot, so that debris
vanishes — until the request fits, then wraps to the start when the tail is
too small. It then copies the twelve-word record and the piece's render entry
(the §4.3 record: transformed point list, world offset, angles, flags), points
the copy's point list at its own tail, and adds the unit's X, Y, Z to the
copied world offset so the copy is absolute.

**Established — per-tick step.** The effect phase of the tick (the same phase
as the fixed effect pool, doc 03 §1.3; doc 01 owns the phase order) visits
every occupied slot:

1. Read the lifetime, store `lifetime − 1`, and free the slot when the value
   read was already 0 — a debris piece therefore survives its 900 steps and
   dies on the 901st visit.
2. If the piece's Y is **strictly above** `seaLevel << 16` (the map's sea
   level byte, doc 03): let `h` be the terrain height (whole units) at the
   piece's X/Z. If `vy + Y ≤ h << 16` (it would touch the ground this tick):
   `vy = −(vy >> 1)`, `vx >>= 1`, `vz >>= 1` (arithmetic shifts — a bounce
   with half the energy); then if the new `vy < 0x20000` (under 2 units per
   tick): engine bit 4 clear → free; set → spawn the `explosion` bitmap at
   the piece with the first calculated-frame table and the "above-sea
   flash" ([R-COB-04 §4]), then free. Otherwise (still moving, or not yet
   touching): `X += vx`, `Y += vy`, `Z += vz`; the piece's three angle words
   advance by `rate2`, `rate3`, `rate1` respectively, each **truncated to 16
   bits**; and when engine bit 3 (`FALL`) is set `vy −= gravity`, the map's
   gravity constant in 16.16 units per tick² (doc 03). Survive.
3. Otherwise (at or below sea level): when engine bit 4 is set and the
   session's water-effects word is zero, spawn the `h2oboom2` bitmap, or
   `lavasplash` when the session's lava flag is set (doc 02 owns the flag),
   with no calculated frames and the flash suppressed; in every case free —
   **debris never survives entering water** and never bounces off it.

A piece with `FALL` clear flies in a straight line at its spawn velocity
until it hits terrain or its lifetime ends. Without `EXPLODE_ON_HIT` the
ground bounce is silent and the second, slower touch removes it.

**Established — smoke and fire trails are presentation.** The `SMOKE` and
`FIRE` engine bits are read only by the **draw** pass: every rendered frame
that draws a debris piece emits one smoke puff (bit 1) and/or one fire
particle (bit 0) at the piece's position through the pooled-effect
allocator of [R-COB-03 §6]. The fire particle's spawner draws the **CRT**
stream. Neither touches simulation state or the simulation RNG, and their
cadence is the frame rate, not the tick — a headless simulation emits none.

### Closed — the shatter path [R-COB-04 §3] (2026-08-29)

**Established.** When engine bit 2 is set the spawner hides the piece and,
instead of one debris block, creates one **fragment** per eligible primitive
of the piece's model: a primitive with exactly four vertices, its own flag
bit 0 clear, and not the model's ground-plate primitive. Fragments are
records of the fixed effect pool (cap **300**, shared with bitmap
explosions and weapon impact art) paired with a slot of a fixed table of
**300** fragment geometries. Per eligible primitive, in model order:

1. If the effect pool is full, stop — the remaining primitives are dropped.
2. Claim an effect record at the piece's world position; take the first free
   geometry slot. If none is free the effect record stays claimed **with
   stale contents** and the loop stops (retail relies on the table never
   filling).
3. Set the record's explode-on-hit bit from engine bit 5.
4. Seed the fragment's velocity with **half the unit's mover velocity** on
   each axis (zero for an immobile unit), then draw, in order: `vx += (80 −
   random(160)) · 2^9`, `vz += (80 − random(160)) · 2^9`, `vy += (80 −
   random(160)) · 2^9 + 30 · gravity` (a thirty-tick upward kick against the
   fall), and angular rates `800 − random(1600)` for each of three axes.
5. Copy the quad's four vertices as the front face and the same four in
   reverse as the back face; compute the quad's unit normal in float from
   the first three vertices; then `vx += random(200) · int16(trunc(nx ·
   512))` and `vz −= random(200) · int16(trunc(nz · 512))`; extrude the back
   face by `trunc(n · 65535)` per axis; and centre all eight vertices on
   their mean.
6. Copy the primitive record and, for textured primitives, resolve the
   texture name against the owning side's palette index.

So a shatter makes **eight** simulation draws per fragment actually created,
after the six draws of [R-COB-04 §1], and the count of fragments depends on
the model and on pool occupancy at that moment. Fragment physics, in the same
effect phase: position advances by velocity plus the inherited half
velocity, `vy −= gravity`, angles advance by the rates; on reaching terrain
the position is restored and `vy = −(vy / 2)` (division, truncating); when
the whole part of `vy` is then below 1 the fragment is freed, spawning the
`explosion` bitmap first when its explode-on-hit bit is set; below sea level
it is freed at once, with the `h2oboom2`/`lavasplash` art under the same
session gates as [R-COB-04 §2].

### Closed — bitmap explosions, the calculated frames, and the above-sea flash [R-COB-04 §4] (2026-08-29)

**Established.** A bitmap explosion is a record of the same 300-entry effect
pool: position, an optional named animation, and an optional
*calculated-frame* table index. The allocator refuses silently when the pool
is full. When the caller does not suppress it and the position's whole Y is
**strictly above** the sea level byte, the allocator also spawns one pooled
effect of class 7 with parameter 15 at the position — the flash retail draws
with every above-water explosion (the class identity is now doc 03's
[03 R-FX-01 §3]: the strip-9 smoke emitter, three `smoke 1` puffs; the gate
and parameters are established here). The `explode` bitmap branch passes
calculated table 2 and does not suppress the flash; the debris and fragment
ground hits pass table 0; the water splashes pass no table and suppress it.

The three calculated-frame tables are built once at startup with the **CRT**
stream: table 0 has 12 frames of side 64 shrinking by 4; tables 1 and 2 have
15 frames each, from 128 down toward 16 and from 200 down toward 32 in equal
integer steps. Each frame is a square byte image whose pixels are drawn per
cell from `CRT_rand · 10 / 0x8000`: a narrow window of that value maps to a
palette index near `0x6f`, the rest is transparent. They are procedural art,
generated before any session RNG seed and never redrawn, so they cost the
simulation nothing; doc 03 owns their rendering.

### Closed — the `explodepiece` and `CalcedExplosion` strings [R-COB-04 §5] (2026-08-29)

**Established — reader census.** Both strings have exactly three references
in the image, all in the startup initializer of the effect tables: neither is
compared, parsed, or looked up anywhere. `CalcedExplosion` is the allocation
tag of the three calculated-frame tables. `explodepiece` is written into the
name word of each of the 300 fragment-geometry records (with defaults: free
marker `0xFF`, the values 8 and 6, an all-ones word, and pointers into two
fixed regions holding eight vertex triples and six primitive records per
slot — the storage [R-COB-04 §3] fills). The plan's question "how the
`explode` flags select among these templates" has the answer *they do not*:
the records are identical scratch geometry, selected first-free. There is
**no `explodepiece` TDF key, no per-unit explosion weapon, and no reader**
outside the effect code. Any implementation reading such a key from unit
definitions would be inventing content.

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

### Correction — a scriptless unit crashes retail at creation, not at a later tick [R-COB-04 §8] (2026-08-29)

[R-COB-01 §1] ("Closed — UNIT-04") said that for a definition whose script
file is missing "retail therefore neither rejects the unit nor crashes at
creation nor substitutes a program — it creates a scriptless unit whose
pieces render and animate never", and left the crash as an Unknown at the
wind-generator producer. The scriptless branch of the model bind is as
described — null program, no VM, no `Create` — but the sentence about
creation was wrong, because it stopped at the bind.

**Established — the fault site.** All three unit creators run, immediately
after the model bind, the weapon-slot initializer of [R-CB-01 §4] step 4, and
that initializer runs the synchronous `QueryPrimary` query for slot 0 (with
the "ask the script" piece argument of −1) **unconditionally**, for every
definition, with or without weapons. Every callback starter — the two
name-form starts, the name lookup, and the synchronous query — reads the
program pointer through the VM reference as its **first instruction**, with
no null test. With a null VM reference that read is a null-pointer
dereference, so **retail faults with an access violation while creating the
unit**, before `SetMaxReloadTime`, before the unit is ever ticked, and before
any diagnostic could be printed. There is no retail diagnostic for this
case: the script loader returns null silently when the file is absent
([R-COB-01 §1]), and the crash is the only symptom.

**Established — the guard census.** Exactly three producer sites test the VM
reference for null: the per-unit sweep's script drain, the metal extractor's
creation-time `SetSpeed`, and the unit destructor's VM release. Every other
producer (the wind pair, every query, `Aim*`, `Fire*`, `RockUnit`,
`HitByWeapon`/`TakeDamage`, the movement-rate and medium callbacks, the
transport family, `TargetCleared`, the `StartBuilding`/`StopBuilding` forms,
and the network-mirror lookups) dereferences it directly. So even if the
creation-time query were skipped, the first of those to run would fault.

**Consequence for Nanolathe.** The contract is "a unit definition without a
loadable COB program cannot exist in retail". The right divergence is to
reject the definition at catalog compile time with the standard diagnostic
shape (`nanolathe: unit script missing: logical path scripts/<name>.cob,
providers searched [...], expected COB program`), never to create a
scriptless unit. The `TODO(question)` marker at the creation path should be
retired in favour of that rejection; the "crash policy" bullet leaves the
tail.

### Closed — `SetSpeed`, `SetDirection`, and `MotionControl` units and signs [R-COB-04 §9] (2026-08-29)

**Established (restated from [R-CB-01 §5]; nothing new was traced).** Both
callbacks are engine-to-script starts with one argument, delivered in window
word 0 as a plain integer:

* `SetDirection` — the global wind heading, a 16-bit angle in the
  65536-per-circle domain, **zero-extended** (never negative). The heading is
  drawn as `random(0x10000)` on a re-roll and is not relative to the unit;
  scripts subtract their own heading if they want a relative turn.
* `SetSpeed` (wind form) — the global wind speed shifted **left by four**
  (`speed · 16`), where the speed is `minWindSpeed + random(maxWindSpeed −
  minWindSpeed)` in the map's wind units (doc 03 owns the map keys); always
  non-negative.
* `SetSpeed` (extractor form) — the footprint metal sum of [R-CB-01 §5],
  **sign-extended from 16 bits**, so a sum of `0x8000` or more arrives
  negative.

`MotionControl` **does not exist** in retail: a byte search of the whole
image finds no such string ([R-CB-01 §1]), so no engine site starts it and
a script defining it is never entered by the engine. The same bounded
negative covers `StartUnload`, `RequestState`, `Demoted`, `Promoted`, and
`Go`.

### Closed — the `Aim*` handshake, script side [R-COB-04 §10] (2026-08-29)

**Established (closing the R-1 marker's COB half).** The engine starts
`AimPrimary`/`AimSecondary`/`AimTertiary` deferred with two 16-bit cells —
heading, pitch — and passes the weapon slot's receiver word as the thread's
completion receiver, having first written zero into the slot's aim-ready
word ([R-CB-01 §3]). The script's `return` opcode (§4.3, `0x10065000`) pops
one value and, because the receiver is set, delivers it: the interpreter
calls the receiver's first method with the popped cell, then frees the
thread and wakes any thread blocked on it. The receiver's first method is the
two-branch setter of [R-CB-01 §6]: a **nonzero** cell stores the literal `1`
into the aim-ready word; a zero cell stores nothing. A script that returns
`FALSE` (0), or that is killed by a `signal` before it returns, or whose
`Aim*` name does not resolve, therefore leaves the word at zero and the
weapon never fires from that aim; there is no timeout, and the engine's next
aim start clears the word again and restarts the handshake. The weapons side
— which executor reads the word, together with the issue latch, before it
spawns a projectile — is [R-WPN-03 §6] and is not restated here.

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
edge machine (naming-only; the edge behavior is established).
**Closed (2026-08-28):** the axis naming of the packed position halves — the
question asked whether the word section 4.4 called Z was Z or X, and section
4.4's answer was inverted. The high half is X and the low half is Z, settled by
a static trace, not by the probe the question asked for; see [R-COB-03 §3]. The
`TODO(question)` and its probe are withdrawn. `TODO(question)`
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

### Closed — the engine port set, exactly [R-COB-03 §1] (2026-08-28)

**Established — the port set is closed at twenty.** The read switch subtracts
one from the identifier, rejects anything above nineteen unsigned, and jumps
through a dense twenty-entry table; the rejection arm returns zero. There is no
sparse tail, no second switch, and no arm reachable only from another opcode.
Identifiers 0 and 21 and up therefore read **zero**, and a *write* to them does
nothing but set the script-touched marker (§4.7 above). Any port vocabulary
beyond `[fmt cob]`'s twenty — `MY_ID`, `MIN_ID`, `MAX_ID`, `UNIT_TEAM`,
`UNIT_ALLIED`, `UNIT_BUILD_PERCENT_LEFT`, `VETERAN_LEVEL`, `CURRENT_SPEED`,
`IN_WATER`, `SMOKEUNIT` — does not exist in this executable; a script naming
one reads zero. `SHATTER` and `EXPLODE_ON_HIT` are not ports at all: they are
bits of the `explode` opcode's flags word (§4.5).

**Established — the five read slots and the two write slots.** The
zero-argument read opcode pops the identifier and calls the port reader with
four zero argument slots. The five-argument read opcode pops the four arguments
top-down and then the identifier, and passes them to the same reader in
authored order: identifier, then argument one through four. The write opcode
pops the value first and the identifier second and passes identifier-then-value
— the order §4.7's audit note fixed, now confirmed at the interpreter site as
well as at the compiler.

**Established — two further read opcodes exist that `[fmt cob]` does not
list.** Beside the two value-reading opcodes, the interpreter binds a
one-argument opcode that routes to the cargo-membership query and a
zero-argument opcode that routes to the carrier-identity query (both described
in §4.4). They are ordinary stack opcodes: the first pops one value and pushes
one, the second pops nothing and pushes one. Neither is emitted by shipped
content, which is why the format document's opcode table has no row for them.
The gap is a format-document item, reported to lane 02.

### Closed — port read arithmetic [R-COB-03 §2] (2026-08-28)

Every expression below is the whole of what the arm computes; §4.4's table
carries the same arithmetic in row form and this section states the widths and
the order of operations.

* **Flag ports (1, 5, 6, 18, 19, 20).** Each is a single byte load, a shift and
  a mask to one bit, pushed as 0 or 1. Ports 1 and 20 read bits 0 and 1 of the
  first state byte; ports 5, 6, 18 and 19 read bits 0, 1, 2 and 3 of the second.
  Unit creation zeroes the first state byte outright and clears the low nibble
  of the second, so **all six default to 0** before any script or engine writer
  runs.
* **Port 4 (health).** The health field is loaded **sign-extended from 16 bits**
  and multiplied by 100 as a signed 32-bit product; that product is then used as
  the *unsigned* dividend of a 32-bit divide by the definition's `maxdamage`,
  with the high dividend word cleared. For health in `[0, maxdamage]` the
  quotient is 0..100. The port itself clamps nothing: because the signed product
  is reinterpreted as an unsigned dividend, a negative health value would give a
  very large quotient rather than a negative one. Whether the field is ever
  observably negative when a script reads it is a damage-ordering question for
  document 06, not a property of this port.
* **Ports 7 and 8 (piece position).** The piece world position is recomputed on
  every read: the piece's authored offset plus its animation offset, then the
  chain of parents applied in turn, with the unit's own three angle words folded
  in at the root, and the third component negated on the way out. The result is
  added to the unit's world position. If the unit has no render table, or the
  piece index is outside the piece count, the offset is the zero vector and the
  port returns the unit's own position — the index **is** checked here, unlike
  the piece-motion opcodes (§4.6).
* **Ports 9, 10, 11 (another unit).** The identifier is tested as a 16-bit value
  for zero, then masked to 16 bits and multiplied by the pool record stride and
  added to the pool base. There is no comparison against the pool capacity. The
  resulting record is accepted only if its alive bit is set; otherwise the port
  returns zero.
* **Ports 12–15 (trig).** The angle helper converts both arguments to extended
  precision, takes `atan2(first, second)`, multiplies by the double constant
  65,536 / 2π = 10430.37835047, and stores under **round-to-nearest-even** — the
  only rounding in the port set. Port 12 then subtracts the unit's heading word
  as a 16-bit subtraction and masks to 16 bits; port 14 masks to 16 bits with no
  subtraction. The distance helper is the C-runtime `hypot` on the two arguments
  converted to double, truncated toward zero by the shared float-to-integer
  conversion. Port 13 feeds it the unpacked halves (so its result is 16.16);
  port 15 feeds it the raw arguments unchanged.
* **Port 16 (ground height).** The unpacked halves are written into a
  three-word position record — X first, Z third, the middle word left
  untouched — and handed to the shared terrain height query, whose integer
  result is shifted left 16. The query is bilinear over the terrain cell array
  with 16-world-unit cells, and returns **−1** when either cell index or its
  successor is outside the map, so an off-map read of this port is `−0x10000`.
  The query's own arithmetic belongs to document 03 and is reported to lane 03
  as a finding.
* **Port 17 (build percent left).** The remaining-build fraction is compared to
  the single-precision constant `0.0f`; on exact equality the port pushes 0.
  Otherwise it is multiplied by the single-precision constant `-99.0f`,
  truncated toward zero by the shared conversion, and subtracted from 1. A
  fresh nanoframe holds `1.0f` and reads 100; a unit created complete holds an
  all-zero word and reads 0.

### Closed — packed coordinate halves and the standing-order fields [R-COB-03 §3] (2026-08-28)

**Established — the high half is X, the low half is Z. This reverses §4.4's
previous text**, which read "packed with Z in the high half and X in the low
half". Three independent traces agree and the earlier reading has no support:

1. The ground-height port writes the **high** half into the first word of the
   position record and the **low** half into the third. The height query
   multiplies the *third* coordinate by the terrain row stride and adds the
   first, so the first is the within-row index — that is X.
2. The piece-position helper fills its output triple from the unit's three
   consecutive position words in memory order, and the Y port returns the middle
   one. The first is therefore X and the third Z, and the packing port takes the
   **first** for the high half.
3. The unit-position port packs the same two words in the same roles.

This is also the convention `[fmt cob]` records from the external host ABI, so
the format document needs no change; §4.4 does, and is corrected above. The
`TODO(question)` asking for a controlled probe of this is withdrawn.

**Established — the pack is an addition, not a bitwise OR.** The port computes
`(X & 0xffff0000) + (Z >> 16)` with an **arithmetic** shift, so a negative Z
borrows one from the X half. Every consumer undoes exactly that: it takes
`X' = packed & 0xffff0000`, `Z' = packed << 16`, and then, **when `Z'` is
negative, adds `0x10000` back to `X'`**. Both halves come out as 16.16 values
whose fractional parts are zero. An implementation that packs with an OR and
unpacks with a plain shift agrees with retail for non-negative Z and disagrees
by one whole world unit of X for negative Z — which is every position west or
north of the map origin under retail's signed coordinates. All three consumers
(relative bearing, distance, ground height) share the correction; nothing else
unpacks.

**Established — standing orders are two-bit fields, and the three-bit field
named in the string triage is a different word.** The order handlers for the
two standing-order commands each mask the incoming order value to **two bits**
and deposit it into the unit state word: move orders at bits 18–19, fire orders
at bits 20–21. Ports 2 and 3 read those same two-bit fields. Unit creation
seeds both from one packed definition byte — bits 0–1 for move, bits 2–3 for
fire — which the FBI reader fills from `standingmoveorder` and
`standingfireorder`, **each with a parsed default of 2**. The fire-order handler
additionally clears the three weapon slots' target state when the new value is
0 or 1.

The three-bit field reported by the RWU-00-4 string triage is real but belongs
to the **interface** stance-panel handler, which cycles a 3-bit field at bit 0
(move) and a 3-bit field at bit 12 (fire) of a sixteen-bit engine-root word
before transmitting the command. Because the command handler masks to two bits,
only values 0..2 are ever observable at the port, and value 3 is representable
but unreachable from the panel. There is no contradiction between doc 04's
"two-bit field" and the triage's "three-bit field": they are two different
words on two sides of the command. The stance *semantics* — which value is
hold-fire, return-fire, fire-at-will, and hold-position, maneuver, roam — belong
to §3.4/§3.5 and are RWU-04-6's, not settled here.

### Closed — the write arms, their defaults, and the marker [R-COB-03 §4] (2026-08-28)

**Established — six arms and one unconditional side effect.** The write switch
binds identifiers 1, 5, 6, 18, 19 and 20, exactly as §4.7 states. The
script-touched marker bit is set on **every** path — each of the six arms, and
the fall-through every unbound identifier takes — so a write of an
unimplemented port is observable only through that marker (whose consumer
remains unlocated, §4.7).

**Established — the bit writes.** Ports 5, 6 and 19 write bits 0, 1 and 3 of
the second state byte from the **low bit** of the value, leaving the rest of
the byte untouched; a value of 2 therefore clears the bit. Ports 1 and 20 do
not write a bit directly — they call the shared edge machine with mask 1 and
mask 2 respectively, so the write goes through the diff-then-fire sequence
§4.7 describes and can raise callbacks re-entrantly.

**Established — port 18 is admission-gated and writes nothing when denied.**
The yard write first calls the yard-occupancy admission predicate with the
value. On a zero verdict it returns having written nothing at all — the yard
bit keeps its old level, no occupancy is recomputed, and only the marker (set
by the switch before the arm ran) changes. On a non-zero verdict it commits bit
2 of the second state byte from the low bit of the value, sets the
occupancy-dirty bit of the state word, recomputes occupancy and refreshes the
footprint words. This is exactly the level that `OpenYard`/`CloseYard` poll and
retry against (§4.7's stock choreography).

**Established — creation defaults.** Unit initialization clears the first state
byte and the low nibble of the second, so ports 1, 5, 6, 18, 19 and 20 all read
0 on a newly created unit, before the activation edge machine or any script
runs. It also seeds health and the remaining-build fraction on a branch: a unit
created complete gets health equal to the low sixteen bits of `maxdamage` and a
zero remaining fraction (port 4 reads 100, port 17 reads 0); a nanoframe gets
zero health and a remaining fraction of `1.0f` (port 4 reads 0, port 17 reads
100). The script-touched marker starts clear.

### Closed — transport reads, attach and drop [R-COB-03 §5] (2026-08-28)

**Established — the cargo linkage.** A unit holds a carrier back-pointer, a
first-cargo pointer and a next-cargo link, so cargo forms a singly linked list
hanging off the carrier with new cargo pushed at the head.

**Established — the two queries.** The one-argument query walks this unit's own
cargo list comparing each entry's sixteen-bit identifier against the popped
value and pushes 1 on the first match, 0 if the list is empty or exhausted. The
zero-argument query follows this unit's **carrier back-pointer** and pushes
that unit's identifier, or 0 when the unit is not being carried. §4.4's
previous "pushes the first cargo identifier" is corrected above.

**Established — attach and drop share one commit.** The attach adapter resolves
the cargo identifier through the unit table with the same zero-test and alive
gate as ports 9–11, requires the cargo's carrier field to be empty or already
this unit, and calls the shared commit with (cargo, this, piece, third value).
The drop adapter resolves the identifier the same way, requires the cargo's
carrier to be **this** unit, asks the placement service for permission for the
cargo at its current position, and only on approval calls the same commit with
a null carrier, the reserved "no piece" index and a third value of 1.

**Established — the commit's own gates and its event.** The shared commit
re-checks: the cargo is alive and not marked with the second lifetime bit; the
cargo is not itself carrying anything; and the carrier, when non-null, is alive,
is not the cargo, and is not itself being carried. Only then does it build a
seven-byte event — a kind byte, the cargo identifier, the carrier identifier,
the piece byte and the third value byte — submit it on the event channel and
apply it. The apply step unlinks the cargo from its previous carrier (or from
the world list), stores the piece byte on the cargo, relinks at the new
carrier's list head, and sets a state bit **iff the piece byte is the reserved
"no piece" value** — which is the engine-side meaning of `[fmt cob]`'s
`attach-unit … to 0-1` idiom: cargo that rides the carrier without following a
piece.

**Established — the third `attach-unit` value is consumed.** The apply step
writes the value's **low two bits** into a two-bit field of a record the cargo
unit points at. `[fmt cob]` recorded this value as "always 0 in retail content,
effect unknown"; it is not inert. **Unknown:** what that two-bit field means —
naming only, since every shipped call site passes 0 and therefore clears it.
Decider: static trace of the pointed-to record's other readers. Marked
`TODO(question)`.

### Closed — the effect opcode, engine side [R-COB-03 §6] (2026-08-28)

**Established — the gate.** The effect opcode's first act is the shared
per-player unit-visibility predicate, indexed by the session's **viewing-player**
byte — a slot distinct from the commanding-player byte the order paths use,
though both are seeded from session data at battle entry. When it fails the
opcode returns having done nothing — no allocation, no draw, no state change.
It is therefore not simulation-visible and is correctly outside the
deterministic contract, but it is also **not** unconditional: a script that
relies on it for timing gets nothing on a client that cannot see the unit.

**Established — the geometry.** Before reading anything the opcode refreshes
the unit's cached render transform. That refresh is itself cached with a
tolerance: it re-runs only when any of the unit's three angle words differs
from the cached copy by more than 7 in the 65,536-per-circle domain, so effect
origins can lag the unit's true orientation by up to that much. (The position
ports do not use this cache; they recompute exactly.)

*Vector types* (the effect type's point-based bit clear) read the piece's
**transformed vertex list** and take vertices zero and one. Each becomes a world
point as the unit position plus the vertex, with the third component
**subtracted** rather than added — the same model-Z-versus-world-Z inversion the
piece transform applies. Two points are produced.

*Point types* (the bit set) read the piece's cached world offset triple from the
render entry and produce a single point the same way, third component
subtracted.

**Established — the dispatch.** Types below the point-based bit plus two are
handled first: white smoke is the one point-type in that range, and everything
else falls into a six-way switch over 0..5. Types 0 and 1 call one effect
constructor with the two points, a fixed leading value, a selector that is the
only difference between them, and a trailing byte. Types 2 and 3 call a second
constructor with the two points, a magnitude that is the only difference
between them, and a fixed selector. Types 4 and 5 call that same second
constructor with the **two points exchanged** and the same two magnitudes. Black
smoke and the sub-bubble type are handled after the range test; the sub-bubble
type first overwrites the second point with the first point's X and Z and a
height taken from the map's sea-level byte shifted into 16.16, then calls a
third constructor. Everything else — vector types from 6 up, the bare
point-based value itself, and anything above the sub-bubble type — matches no
case and is ignored.

**Audit note.** §4.4 previously grouped these as "0 and 1 form the wake pair, 2
and 3 a thrust-class pair". That is the wrong assignment in both directions:
`SFXTYPE.H` (`[fmt cob]`) names 0 and 1 VTOL and thrust and 2 and 3 the wake
pair, and the dispatch groups them the same way — 0/1 share one constructor,
2/3 share another, 4/5 are 2/3 reversed. The pairing structure the old sentence
described was right; the names attached to each pair were swapped.

**Closed (2026-08-29) — the effect families themselves.** The visual each of
the three constructors produces, the selector and magnitude meanings, and the
pooled record lifetimes are stated strip by strip in [03 R-FX-01 §3] (flame-
stream trail, impact sprinkle, smoke emitters). The earlier text left this
Unknown pending RWU-03-4.

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

**Start here: [R-CB-01 §2] is the complete table.** Sections 5.1–5.4 grew by
subsystem and each covers part of the callback set; [R-CB-01 §1] enumerates all
forty fixed names from the producers and states the bounded negative, and
[R-CB-01 §2] gives one row per name. [R-CB-01 §3] lists five readings in 5.1
and 5.3 that it withdraws — read it before implementing from those sections.

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

**Established fact:** Lifecycle modes are fixed: `Create` (only when the definition has compiled COB, and VM/script/piece state is attached first) is I (immediate: all eight slots delta 0 plus one piece pass, one start per unit init); `Activate`/`Deactivate`/`StartBuilding` (the building-bit edge helper)/`StopBuilding` are D (deferred) gated on cached-bit rising/falling edges with producer-side suppression of unchanged state; `QueryBuildInfo` (the placement-time call) is Q (one-slot snapshot, no piece pass) with cell 0 starting at `−1` (cells 1..3 at `0`) consumed as piece index transformed to a world build position and network-emitted; the slot-form `StartBuilding` variant is D via direct slot start (see 5.3).

**Established fact:** `TargetCleared` carries the zero-based weapon slot (0–2)
and is emitted only when a stored target is actually cleared: the slot's
commanded heading/pitch words must be non-default (`heading != 0` or
`pitch != 0x8000`) and are reset to `0`/`0x8000` by the clearing itself.
(**Naming corrected by [R-CB-01 §3]:** those two words are the slot's *stored
target descriptor*, not commanded angles — `(0, −0x8000)` is the canonical
"no target" encoding, so this predicate is exactly "a target is stored". The
gate itself is unchanged. There are four producers, not the two this section
implies; see the [R-CB-01 §2] table.) Each
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
an unsigned division of the product. Either starter can fail separately (invalid name or full pool). Heal (`0`) and paralyze (`2`) and non-normal kinds skip this pair entirely (the paralyze kind builds a paralyze order instead); lethal damage against a movement-category-1/2 victim sets the death latch (OR `0x4000` into the unit's death-latch word) and returns without any callbacks.

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
nothing. **The two thresholds are the FBI keys `MoveRate1` and `MoveRate2`**
(fixed-point accessor, 16.16 world units per tick, each defaulting to
`MaxVelocity` shifted left one), and the classified magnitude is the mover's
scalar speed word; the adjacent word tested with it is the signed 16-bit turn
residual [R-MOV-01 §6]. Because committed speed can never exceed
`MaxVelocity`, a definition authoring neither key is permanently category 1
and only ever emits `StartMoving`, `StopMoving` and `MoveRate1`. On change: into category 0 from nonzero issues `StopMoving` (I); into a
nonzero category from 0 issues `StartMoving` FIRST (I) and then the matching
`MoveRateN` (I); other nonzero-to-nonzero changes issue only `MoveRateN` (I). All are
immediate wake-flag starts via the zero-argument name-form adapter (wake 1), so the `StartMoving` drain — all eight slots at
delta 0 plus one piece pass — forms a barrier between it and the `MoveRateN`
that follows; the cache update (the two-bit category shifted left by two into its field) is the final write.

**Established fact:** `setSFXoccupy` is a five-value classifier (run after the rate classifier inside the movement integration, mode I, arity 1, spelling exact with lower-case `s`). Occupancy is `0` when the current movement mode (the low two bits of the movement-mode word) is not `1` or `2`. In those modes the classifier compares signed Y (the signed high word of the unit's 16.16 Y), the map water level (`wt`), the definition's waterline byte (`wl`) and the definition's model-bottom signed word (`mb`), yielding `4` if `wy > wt`; otherwise starting from the cached band it can yield `1` when `wy - wt > -5`, `2` when `wl + wy == wt`, and `3` when `mb + wy < wt`, with later tests overriding earlier ones (`1→2→3`). The value is cached and emitted via the argument-carrying name-form adapter only on change (cell 0 = band `0..4`).

**Supported inference:** Wake effects and medium bands are script-authored behavior. The engine does not need a separate hard-coded wake renderer to reproduce the callback contract.

### 5.3 Weapon and query callbacks

**Established fact:** The engine invokes `QueryPrimary`/`QuerySecondary`/`QueryTertiary` (Q, cell 0 = `0`), `AimFromPrimary`/`AimFromSecondary`/`AimFromTertiary` (Q, cell 0 = `−1`; if still `−1` the engine falls back to the matching `Query*` with default `0`), `SweetSpot` (Q, cell 0 = `0` transformed through the selected piece — but **run on the *target* unit's script, not the shooter's; see [R-CB-01 §3] correction 3**), and `QueryNanoPiece` (Q, cell 0 = `0` → world nano origin) synchronously (mode Q, cells 1..3 null → seeded 0 and excluded from copy-back). `AimPrimary`/`AimSecondary`/`AimTertiary` are asynchronous (D) with heading/pitch arguments. Successful normal, ballistic, and vertical-launch weapon spawners invoke `FirePrimary/Secondary/Tertiary` (D, 0 cells via the zero-argument name-form adapter) and `RockUnit` (D, 2 cells via the argument-carrying name-form adapter); the dropped-family inline allocator does not. Burst clones do not rerun the root Fire/Rock callbacks. A Q script that sleeps/waits leaves the host holding the partial cell-0 value (section 4.2) — the blocked thread lingers with no receiver that can revise it.

**Established fact:** `AimPrimary`/`AimSecondary`/`AimTertiary` carry unsigned
16-bit heading then unsigned 16-bit pitch (arity 2, issued by the weapon update). Two issue forms exist,
both preceded by clearing the slot's aim-state word to `0` and both storing the
commanded angles in the weapon-slot record's commanded heading and commanded pitch words, where the three weapon-slot records are addressed at a fixed stride from the unit record for slot `k=0..2`. The ballistic default computes
relative heading as bearing-to-target minus unit heading and pitch from the
ballistic solver; **a solver returning the `-0x8000` pitch sentinel suppresses
the Aim start entirely**, otherwise the start carries `heading & 0xffff` then
`pitch & 0xffff` via the argument-carrying name-form adapter forwarding to the argument-carrying slot-form adapter (mode D, receiver the slot's completion-receiver word). The fixed-forward branch — **corrected by [R-CB-01 §3]: the selector is the *weapon definition's* flag word, not the slot record's flag byte, and "no live tracked target" is not part of the test** — is entered when the definition's branch bit is clear and its fixed-forward bit set, the ammunition byte is nonzero where a third definition bit demands it, and
the slot's aim issue bit is clear; it starts with `(0, 0)`, the fixed-forward
heading. After either start the engine emits a network event packet type `0x10`
`{u16 unitId, u16 slot, u8 arity=2, heading, pitch}` behind the aim event's global option bit (mask `1`)
and sets the weapon flags byte bit 0 (the issue bit).

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Established fact** (*and see the two supersessions in [R-CB-01 §3]: the
"slot-form heading variant" and the "record-form emission helper" described
below are **one** producer, and its first argument is a bearing, not a record
identity*)**:** `StartBuilding` has an argument-less edge form, issued
on the cached building-bit rising edge (mode D, 0 cells), and a slot-form heading variant that
resolves the name to a weapon slot through the shared name-lookup helper and starts THAT slot directly via the argument-carrying slot-form adapter (mode D, receiver null) with one
argument `heading & 0xffff` (the low 16 bits of the producer heading), plus its network event.
Four weapon-target routines call the `StartBuilding` name lookup while clearing targets but discard the result and are not producers.

**Correction — the slot-form heading variant does not write the
StopBuilding-pending flag (2026-08-28, superseding this section's earlier
text and [R-COB-02 §1]'s initial reading).** *Withdrawn on 2026-08-29 by
[R-CB-01 §3] correction 2: the producer census finds exactly one slot-form
`StartBuilding` start in the code section, it is inside the order-record
emission helper, and that helper does set the flag. The paragraph is kept
because the reversal must stay auditable; do not implement it.* This section previously said the
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
(section 3.3).

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

**Verdict** (*the premise is superseded by [R-CB-01 §3] correction 1: the
first argument is the bearing to the work target, computed with a compiled-in
`65536/2π` scale and rounded to nearest — it is fully deterministic, and the
"cannot reproduce from a pointer" conclusion below is withdrawn. The
stock-script census above is unaffected and now has its explanation: the
scripts read the argument as an angle because it is one*)**.** The record pointer's low 16 bits are consumed by stock content —
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

**Established fact** (*producers named and one reading corrected by
[R-CB-01 §5]: the first pair is the **wind generator**, gated on `WindGenerator
> 0.0` and a global **wind-changed-this-tick** flag — not a "mover-active"
flag, and not per-tick; the second is the **metal extractor**, gated on
`ExtractsMetal > 0.0`, and its argument sums the **plot cells' metal bytes**
plus one per covered cell, not "each occupying unit's size byte"*)**:**
Engine-issued value callbacks convert exactly:
`SetDirection` (issued by the general unit update, guarded on a definition float `>0.0` and a nonzero
global mover-active flag) passes a zero-extended 16-bit direction word from the global direction word;
`SetSpeed` from the general unit update (immediately after) passes the signed global speed word shifted left by four (arithmetic, ×16);
the second `SetSpeed`, from the footprint path (guarded on a different definition float `>0.0`), passes the unsigned 16-bit sum over covered footprint
cells of each occupying unit's size byte plus one — semantic unit not
established; and `SetMaxReloadTime` (issued after `Create` so it lands outside
Create's own immediate drain), scans all three weapon slots for the maximum
authored reload field and reports `trunc(maxReload · 1000 / 30)` (multiply, then signed divide by 30 truncating toward zero) — reload ticks
converted to milliseconds. None of these adapters deduplicates; suppression
can only live in the producers.

**Established fact:** Query seeds are exact: the synchronous four-output
`QueryTransport` (mode Q) pre-seeds output cell 0 to `-1` (remaining outputs passed null, seeded 0 by the query interpreter and excluded from copy-back), so a missing script leaves `-1`, the
root-piece fallback, which is later consumed as the attachment piece index.
Every air-transport selection site seeds all four `QueryLandingPad` outputs
to `-1`, tests candidate pieces in cell order `0..3` against validity/availability,
accepts the first pass, and keeps `-1` otherwise; some paths query the
carrier first, then the transported unit.

**Established fact:** The aim-ready handshake: the producer clears the slot's aim-state word to `0`, stores heading/pitch in the slot's commanded heading and commanded pitch words, and starts `Aim*` deferred (mode D) with the embedded completion receiver (the slot's completion-receiver word); immediately after it sets the weapon flags byte bit 0 (the issue bit) and emits the type `0x10` network packet (see above). The issue bit clears when target acquisition fails (an AND-clearing of the flags byte bit 0) and gates re-issue (a new `Aim*` starts only while bit 0 is clear). The argument-carrying slot-form adapter stores the receiver in the thread's receiver slot; on pool exhaustion or invalid identity it invokes the non-null receiver's closure with value `0`, while an explicit script `return` (`0x10065000`) pops the top value and invokes the receiver's closure with that value — the receiver object is re-read at return time. Signal termination (`0x10067000`) and abnormal termination (invalid opcode kill) never invoke a receiver. A zero delivery has no effect while any NONZERO delivery marks the weapon aim-ready — so name absence, thread-pool exhaustion (both deliver `0` via the argument-carrying slot-form adapter), or an authored zero return each leave the weapon unable to fire. The fire path entered with the issue bit set additionally consults a per-weapon permission function referenced by the weapon-slot record before firing; the issue bit alone authorizes nothing.

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
pattern — **R-1 is now CLOSED by [R-CB-01 §6]: the guess was right about the
shape (a non-indexed three-iteration fill at unit creation) and the target is
a fixed two-entry dispatch table whose first entry stores the literal `1` into
the slot's aim-ready word when the delivered value is nonzero. Document 06
section 3.4 carries the same residual and can drop it.** Behavior when all eight script
slots are occupied is established in section 4.3 and differs by starter
(deferred `Aim*`/`HitByWeapon`/`TakeDamage` deliver `0` through the receiver
path; `Create` via the zero-argument name-form adapter has no receiver).

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
the closure target behind the receiver word is **closed by [R-CB-01 §6]**
(2026-08-29): it is the first entry of a fixed two-entry dispatch table
installed into all three weapon slots at unit creation, and it writes the
literal `1`. The grant rule stated here is confirmed from that function's own
body rather than by inference.

**Cross-references added by [R-CB-01 §2] (2026-08-29).** The table above is a
vertical-slice table and omits fourteen of the forty callback names. The
complete per-name table, with producer roles, phases, argument vectors and
gates, is [R-CB-01 §2]; where the two disagree, [R-CB-01 §3] states which
reading is withdrawn and why. The rows most affected are `StartBuilding`
(heading form), `SetDirection`/`SetSpeed`, and `SweetSpot`.

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

### Closed — the callback name census and the bounded negative [R-CB-01 §1] (2026-08-29)

**Established — forty fixed names, enumerated from the producers.** A census
of every direct call to the four adapter roots of section 4.2, taken over the
whole code section and resolved back to the pushed name operand (including the
four name tables the weapon paths index by slot), yields exactly **forty**
distinct case-sensitive callback names. This is the same forty section 4.2
counts, now enumerated:

`Activate` · `AimFromPrimary` · `AimFromSecondary` · `AimFromTertiary` ·
`AimPrimary` · `AimSecondary` · `AimTertiary` · `BeginTransport` · `Create` ·
`Deactivate` · `EndTransport` · `FirePrimary` · `FireSecondary` ·
`FireTertiary` · `HitByWeapon` · `Killed` · `MoveRate1` · `MoveRate2` ·
`MoveRate3` · `QueryBuildInfo` · `QueryLandingPad` · `QueryNanoPiece` ·
`QueryPrimary` · `QuerySecondary` · `QueryTertiary` · `QueryTransport` ·
`RockUnit` · `SetDirection` · `SetMaxReloadTime` · `SetSpeed` · `SweetSpot` ·
`StartBuilding` · `StartMoving` · `StopBuilding` · `StopMoving` ·
`TakeDamage` · `TargetCleared` · `TransportDrop` · `TransportPickup` ·
`setSFXoccupy` (spelled with a lower-case `s`).

The packet path of section 5.3 additionally starts an authored function **by
slot index**, with no name, and is the only nameless start.

**Established — the bounded negative, taken over the whole image.** A raw byte
search of the executable (not merely of the defined-string table, so a name
concatenated into a larger literal would still be found) reports **zero**
occurrences of `MotionControl`, `StartUnload`, `RequestState`, `Demoted`,
`Promoted`, and `Go`. Those names, and every other callback a later engine or
a community header lists, do not exist in retail: an authored function bearing
one of them is simply never started, exactly as an unbound engine port reads
zero ([R-COB-03 §1]). `Capture` and `Stop` do occur, but only in the order and
interface vocabulary — neither appears at a callback starter.

### Closed — the complete callback table [R-CB-01 §2] (2026-08-29)

**Established.** One row per callback name. "Mode" is section 4.2's letter;
"wake" is the immediate-drain flag. Cells not listed are written as `0`
(all four physical cells are always written, whatever the arity, so a cell
beyond the arity is still physically visible to the script). "Receiver none"
means the starter is given a null completion receiver, so nothing consumes the
script's return value. Failure behavior is uniform and is section 4.2's: a name
miss or a full eight-slot pool fails the start; the zero-argument name-form
notifies nobody, the argument-carrying forms invoke a non-null receiver with
`0`, and a synchronous query returns failure leaving the caller's seeded cells
untouched. Only `Aim*` supplies a receiver, so only `Aim*` can observe the
difference.

| Callback | Producer, by role | Tick phase | Mode | Argument cells | Gate |
|---|---|---|---|---|---|
| `Create` | unit creation, after the model/script bind and the render-piece table | creation site, inline | D + wake 1 | none | only when the definition has a compiled script; a scriptless definition takes the no-VM branch and never gets `Create` |
| `SetMaxReloadTime` | the weapon-slot initializer, immediately after `Create` at every creation site | creation site, inline | D | 1: `trunc(maxReload · 1000 / 30)` — the maximum of the three slot definitions' reload field, times 1000 (as `((x·5)·5)·5 << 3`), then a signed divide by 30 truncating toward zero. The maximum runs over all three definition pointers with no in-use test. | none |
| `QueryPrimary` / `QuerySecondary` / `QueryTertiary` | the muzzle-point helper (transforms the piece to a world point), plus a bare variant returning the raw piece index whose one caller is the shared projectile initializer every spawner runs | weapon-slot init at creation; weapon update; projectile spawn | Q | 1 output, cell 0 seeded `0`; cells 1–3 null (seeded 0, excluded from copy-back) | none |
| `AimFromPrimary` / `AimFromSecondary` / `AimFromTertiary` | the aim-origin helper | weapon-slot init at creation; weapon update | Q | 1 output, cell 0 seeded `−1`; on `−1` the helper immediately re-queries the **matching** `Query*` seeded `0` | none |
| `SweetSpot` | target resolution, run **on the target unit's script** ([R-CB-01 §3]) | weapon update, step 2 | Q | 1 output, cell 0 seeded `0` | only for a live unit target |
| `QueryNanoPiece` | the nanolathe origin helper, fifteen order/work handler call sites | order and construction work, step 4 | Q | 1 output, cell 0 seeded `0` | none |
| `QueryBuildInfo` | factory production placement | order and construction work, step 4 | Q | 1 output, cell 0 seeded `−1`; cells 1–3 null | none |
| `Activate` / `Deactivate` | the activation edge machine, on the rising/falling edge of the state byte's bit 0 | wherever the machine runs; commonly the general unit update, step 1 | D | none | the edge machine's change test ([R-UNIT-06 §2]); engine notification 3 / 4 follows |
| `StartBuilding` (edge form) / `StopBuilding` (edge form) | the same machine, bit 3 | as above | D | none | as above |
| `StartBuilding` (slot form) | the order-record emission helper, nine construction/assist handler call sites | order and construction work, step 4 | D, started by slot index | 1: a heading in the 65536 domain — see [R-CB-01 §3] | none; the helper also emits the network mirror and sets the record's StopBuilding-pending flag |
| `StopBuilding` (slot form) | the record teardown helper (three handler call sites) and the order-record destructor | order and construction work, step 4, and record destruction | D, started by slot index, **arity 0** | none (all four cells `0`) | only when the record's StopBuilding-pending flag is set; the emitter clears it |
| `SetDirection` | the **wind generator** update, in the general unit update | step 1 | D | 1: the global wind heading, zero-extended from 16 bits | definition `WindGenerator > 0.0` **and** the global wind-changed flag — see [R-CB-01 §5] |
| `SetSpeed` (wind) | the same, immediately after `SetDirection` | step 1 | D | 1: the global wind speed, arithmetic-shifted left by 4 (× 16) | same gate |
| `SetSpeed` (extractor) | the **metal-extraction rate** pass, at unit creation only | creation site, inline, after `SetMaxReloadTime` | D | 1: the sign-extended 16-bit metal sum under the footprint — see [R-CB-01 §5] | definition `ExtractsMetal > 0.0`; additionally skipped when the unit has no VM |
| `StartMoving` / `StopMoving` / `MoveRate1..3` | the movement-rate classifier | movement integration, step 5 | D + wake 1 | none | tier change only ([R-MOV-01 §6]) |
| `setSFXoccupy` | the medium-band classifier, after the rate classifier | movement integration, step 5 | D + wake 1 | 1: the band `0..4` | band change only |
| `AimPrimary` / `AimSecondary` / `AimTertiary` | the weapon update, solver branch or fixed-forward branch | weapon update, step 2 | D | 2: aim heading `& 0xffff`, aim pitch `& 0xffff`; the fixed-forward branch issues `(0, 0)`; a solver pitch of the negative sentinel suppresses the start | see [R-CB-01 §3] for the corrected branch selector. **Receiver: a pointer to the weapon slot's dispatch word** — the only callback with a receiver |
| `FirePrimary` / `FireSecondary` / `FireTertiary` | the three weapon spawners, after a successful root allocation and the start sound | weapon update, step 2 | D | none | the dropped-family inline allocator does not issue it; burst clones do not re-issue |
| `RockUnit` | the same three spawners, immediately after the matching `Fire*` | weapon update, step 2 | D | 2: `−cos(rel)·800`, `−sin(rel)·800`, `rel` = the slot's aim heading minus the unit heading | a fourth emitter of the same shape exists with no located caller (orphan) |
| `HitByWeapon` | the damage packet path, after the health subtraction | damage ingress, or the post-unit projectile phase | D | 2: `cos(dir)·400`, `sin(dir)·400` | normal-kind damage on a live, not-yet-dying victim |
| `TakeDamage` | the same path, started independently after `HitByWeapon` | as above | D | 1: the post-hit health percentage, clamped `0..100` | as above; can fail separately |
| `TargetCleared` | four producers: weapon hold, weapon release, an unconditional clear helper, and the per-visit target resolution finding its stored unit target dead | weapon update, step 2 (and wherever hold/release is issued) | D | 1: the zero-based slot index | only when a target is actually stored; each site resolves and discards the `StartBuilding` name first |
| `Killed` (local) | slot-end death handling, before the death packet is built | step 6 | Q | cells 0 and 1 non-null, cells 2 and 3 null: cell 0 is seeded with the engine's computed severity and copied back (the script *receives* it and may overwrite it); cell 1 is the variant and is **not** seeded — see [R-CB-01 §7] | cause bypasses of section 5.1 |
| `Killed` (network replay) | unit finalization | step 6 | D + wake 1 | 1: the signed packet severity byte | only when that byte is positive |
| `QueryTransport` | transport attachment | order and construction work, step 4 | Q | 1 output, cell 0 seeded `−1` | [R-UNIT-06 §3] |
| `BeginTransport` | transport load | step 4 | D + **wake 1** | 1: the cargo definition's model total height | [R-UNIT-06 §3]; a network mirror follows |
| `EndTransport` | five producer sites in the load/unload/landing executors | step 4 | D; **wake 1 at two of the five sites**, wake 0 at the other three | none | [R-UNIT-06 §3]; [R-AIR-01 §6] places the landing executor's pair — phase 1 immediate, phase 6 deferred |
| `TransportPickup` | sea/hover pickup | step 4 | D + wake 1 | 1: the cargo's pool slot identity; engine notification 12 follows | [R-UNIT-06 §3] |
| `TransportDrop` | sea/hover drop | step 4 | D + wake 1 | arity 1, but **two** cells written: cell 0 the cargo identity, cell 1 the packed drop point | [R-UNIT-06 §3] |
| `QueryLandingPad` | five sites: one shared pad selector plus four in-line copies in the landing executors | step 4 | Q | 4 outputs, all seeded `−1` | see below |
| (no name) | the run-script network packet | event ingress | D, started by slot index | four dwords from the packet; the byte arity sets the logical top | section 5.3 |

**Established — the `EndTransport` wake flags differ by site.** Section 5.3's
predecessor text and [R-UNIT-06 §3] describe `EndTransport` as "zero-argument,
deferred". That is right at three of the five producer sites and wrong at two,
which set the wake flag and therefore perform the all-eight-slot delta-zero
drain plus one piece pass inside the caller. An implementation that defers all
five loses a flush point: any callback queued earlier in that visit runs one
drain later than retail. The two immediate sites are in the carrier-side
release path and in the landing executor's carried-unit release; the three
deferred sites are the load-interrupted edge, the unload executor, and the
landing executor's own-cargo release.

**Established — the `QueryLandingPad` acceptance rule.** Every site seeds all
four cells to `−1`, then walks cells `0..3` in ascending order and accepts the
first cell that is both not `−1` and passes the pad predicate; otherwise it
keeps `−1`. The pad predicate is: the candidate carrier must not itself be
carried, and no unit already in its cargo list may carry the same attach-piece
index. The shared selector additionally short-circuits — if the caller passes
in a piece that is not `−1` and that piece already passes the predicate, no
query runs at all. One landing executor queries the executing unit first and,
on failure, the order's target unit; on total failure it advances its orbit
heading by a quarter turn and re-dispatches rather than aborting.

### Correction — three readings in sections 5.1 and 5.3 are wrong [R-CB-01 §3] (2026-08-29)

**Correction 1 — the `StartBuilding` first argument is a bearing, not an order
record's identity.** [R-UNIT-06 §4] stated that "window word 0 = the low 16
bits of the issuing order-record pointer (the FIRST script argument)", and
concluded that "an implementation cannot reproduce the value deterministically
from a pointer (retail reads the low half of a heap address)", with a warning
that any substitution is a divergence. That is wrong, and it is wrong in the
direction that matters: the value is fully deterministic.

The emission helper takes three arguments — the unit, the order record, and a
heading — and pushes **the heading** into cell 0. The order record is used for
one thing only: setting the StopBuilding-pending flag on it. The earlier trail
read the wrong parameter.

All nine handler call sites compute that heading the same way, from a shared
two-argument bearing helper: given two world positions it forms
`dx = selfX − targetX` and `dz = selfZ − targetZ`, computes `atan2(dx, dz)` on
the x87 stack, multiplies by the compiled-in constant `65536 / 2π`
(`10430.37835047`, an f64 in the read-only data), and stores the result with
an x87 integer store — so the conversion **rounds to nearest even**, it does
not truncate. Eight of the nine sites then subtract the unit's own heading and
pass the low 16 bits, giving a heading relative to the unit's facing; the
ninth (an aircraft assist path) passes the absolute bearing with no
subtraction. One site inlines the `atan2` helper on raw axis deltas instead of
going through the two-position wrapper; the arithmetic is identical.

So the contract is: **cell 0 of the record-form `StartBuilding` is the bearing
from the builder to its work target, in the 65536-per-circle domain, relative
to the builder's own heading at eight of the nine producers and absolute at
the ninth.** The stock-script census of [R-UNIT-06 §4] — 49 of 133 shipped
`StartBuilding` bodies consuming the first argument as a build/turret heading,
zero consuming the fourth — is unaffected and now has an explanation: the
scripts read it as an angle because it *is* an angle. The Missing-list item
asking for "a friendly semantic name for the `StartBuilding` first-argument
value" is closed by this: the name is *the relative bearing to the work
target*. A Nanolathe implementation must compute it, not substitute zero, and
this is no longer a divergence.

**Correction 2 — there is one `StartBuilding` slot-form producer, and it does
set the StopBuilding-pending flag.** Section 5.3's 2026-08-28 correction
distinguished a "slot-form heading variant" (production start on a
weapon-capable unit) from "the order-record emission helper", and stated that
the former "is not among those call sites; it starts the slot and emits its
network event without touching the flag". The producer census finds **exactly
one** slot-form `StartBuilding` start in the whole code section, inside the
emission helper, and that helper's last act is to OR the pending flag into the
order record. The two "variants" are one function. The 2026-08-28 correction
is therefore withdrawn; [R-ORDER-02 §2]'s "exactly one writer of that flag"
finding stands and is that same function.

**Correction 3 — `SweetSpot` is queried on the target unit, not the shooter.**
Section 5.3's table groups `SweetSpot` with `Query*` and `AimFrom*` under
"weapon aiming/fire point resolution", implying the shooter's script. Its only
caller is the per-visit target resolution, and it is invoked with the
**target** unit as the receiver, so the aim point comes from the *victim's*
script. That is what makes `SweetSpot` an author-visible way for a unit to
declare where it should be shot; a clone that queries the shooter will aim at
the wrong point on every unit whose script defines one.

**Correction 4 — the fixed-forward `Aim*` gate reads the weapon definition's
flags, not the slot record's flag byte.** Section 5.3 describes the branch as
"the weapon-slot record's flag byte bit 4 set, the aim issue bit clear, and no
live tracked target". The branch selector is a **weapon-definition** flag
word: one bit of it chooses between the fixed-forward family and the aiming
family, and within the fixed-forward family a second bit must be set. The
per-slot conditions are the ammunition byte (consulted only when a third
definition bit is set) and the slot's own aim-issue bit, which must be clear.
"No live tracked target" is not part of this test; target liveness is settled
earlier, and a resolution failure clears the aim-issue bit instead.

**Correction 5 — the weapon slot's first two words are the stored target, not
a commanded heading/pitch pair.** Sections 5.1 and 5.3 call them "the slot's
commanded heading/pitch words", seeded `0` / `0x8000` and reset to `0` /
`0x8000` when a target is cleared. The seeding and the reset are right; the
naming is not. Two independent readers — the per-visit target resolution and
the shot-permission helper — decode the pair as a **target descriptor**:

* when the second word is not the negative sentinel `−0x8000`, the pair is a
  **ground point**: the first word is the X coordinate and the second the Z
  coordinate, each a signed 16-bit whole-world-unit value promoted to 16.16 by
  a left shift of 16, with Y taken as the greater of the map water level and
  the terrain height at that point;
* when the second word **is** `−0x8000`, the first word is a **unit pool slot
  index**, with `0` meaning "no target"; the resolution dereferences that slot
  and, if its identity word is zero (the slot has been freed), resets the pair
  to the no-target encoding and raises `TargetCleared`.

So `(0, −0x8000)` is not "default angles", it is the canonical *no target*
encoding, and the `TargetCleared` gate quoted in section 5.1 — "heading != 0
or pitch != 0x8000" — is exactly the predicate "a target is stored". The
angles the `Aim*` start carries live in two **different** words of the same
slot record, written at aim-issue time; those are genuinely the commanded
heading and pitch. Because the second word's `−0x8000` is reserved, a ground
target whose Z coordinate is exactly `−32768` world units is unrepresentable.

**Established — the rest of the weapon-slot record's per-callback fields.**
Beyond the target pair and the aim heading/pitch pair, the slot carries: the
dispatch word the `Aim*` receiver points at ([R-CB-01 §6]); the aim-ready
word, cleared to zero at every aim issue and set only through that receiver; a
pointer to the slot's weapon definition, filled at creation from the unit
definition's three-entry weapon table; a signed 16-bit reload countdown,
decremented once per visit while nonzero; an ammunition byte; and a flag byte
whose bit 0 is the aim-issue bit, bit 1 the slot-enabled bit, **bits 2–3 the
slot's own index** (the spawners read those two bits to index the `Fire*` and
`Aim*` name tables rather than carrying the index separately), and bit 4 the
weapon-held bit. The four `TargetCleared` producers are exactly: the hold
helper (requires enabled and not held, sets held), the release helper
(requires enabled and held, clears held), an unconditional clear helper, and
the per-visit resolution. The hold and release helpers accept the slot value
`3` as "all three slots", recursing on slots 0 and 1 and falling through on
slot 2.

### Closed — the creation-time callback sequence [R-CB-01 §4] (2026-08-29)

**Established.** All three unit-creation entry points (scenario placement,
the general creator, and the factory-product creator) run the same fixed
sequence, and it issues more callbacks than section 5.1 records. In order,
inside the creation site, before the unit is ever visited by the sweep:

1. **Install the aim receivers.** A three-iteration loop writes the fixed
   dispatch-table address into each of the three weapon slots' receiver words.
   This is what makes the `Aim*` receiver non-null for every unit
   ([R-CB-01 §6]).
2. **Unit state initialization.** Per slot, the target pair is reset to the
   no-target encoding and the hold helper runs. Two simulation random draws
   happen here (the spawn heading and one further word).
3. **Bind and `Create`.** The model and compiled script are bound, the
   render-piece table is built, and `Create` is started **immediate**, so its
   all-eight-slot delta-zero drain plus one piece pass runs here.
4. **Weapon-slot initialization.** Per slot the flag byte, the definition
   pointer and the reload word are written, and then **two synchronous
   queries run per slot**: `Query*` (through the muzzle-point helper) and
   `AimFrom*` (which falls back to `Query*` when it returns `−1`). The
   results are transformed to world points and stored on the slot. After the
   loop, `SetMaxReloadTime` is started deferred.
5. **Extraction rate.** The metal-extraction pass runs and may start
   `SetSpeed` deferred ([R-CB-01 §5]).
6. **Later in the same creation site**, the activation edge machine may run
   and start `Activate` deferred.

Two consequences an implementer must reproduce. First, **up to six
synchronous queries execute during creation**, each allocating a thread slot
and running one slot at delta zero; a script whose `Query*` sleeps therefore
leaves a thread allocated from the moment the unit exists. Second, the
deferred starts of steps 4–6 do **not** run at creation: `Create`'s immediate
drain has already happened when they are queued, so they first execute in the
new unit's first normal drain — which is why section 5.1's "issued after
`Create` so it lands outside Create's own immediate drain" is the right
statement for `SetMaxReloadTime` and is equally true of `SetSpeed` and
`Activate`.

### Closed — `SetDirection` and `SetSpeed` have two unrelated producers [R-CB-01 §5] (2026-08-29)

Section 5.3 described these as "engine-issued value callbacks" gated on "a
definition float `> 0.0`" and "a different definition float `> 0.0`", with the
footprint `SetSpeed` argument's "semantic unit not established" and a
corresponding Missing-list item. Both floats are now named from the FBI parse
order, and both producers with them.

**Established — `SetDirection` and the first `SetSpeed` are the wind
generator.** The gate is the definition's **`WindGenerator`** value greater
than `0.0`, together with a global *wind-changed* flag. The two starts are
consecutive and deferred: `SetDirection` carries the global wind heading as a
zero-extended 16-bit value in the 65536-per-circle domain, and `SetSpeed`
carries the global wind speed shifted left by four (× 16).

The wind phase that writes those globals runs once per tick from the phase
dispatcher and is a **change-detector**: it compares a stored next-change tick
against the current tick and, when the change is not yet due, sets the
wind-changed flag to **zero** and returns. So `SetDirection`/`SetSpeed` are
**not** per-tick callbacks. The wind phase runs *after* the unit sweep in the
authoritative phase order of section 1.1, so the flag a sweep observes was
written by the previous tick's wind phase: a re-roll on tick *N* makes every
wind-generator unit issue the pair exactly once, during tick *N+1*'s sweep,
carrying the values drawn on tick *N*; tick *N+2*'s wind phase clears the flag
again. One re-roll, one callback pair per generator, one tick late.
When the change is due, the phase: advances the next-change
tick by `(CRT_rand · 10 / 0x8000 + 5) · 30` ticks (five to fifteen seconds,
one CRT-stream draw); draws the speed as `minWindSpeed + random(maxWindSpeed −
minWindSpeed)` from the simulation stream (one draw); and, only when that
speed is nonzero, draws the heading as `random(0x10000)` from the simulation
stream (a second draw). It then publishes the wind vector of [R-WIND-01],
publishes a normalized float `speed / 5000` clamped at `1.0` (the divisor is a
compiled-in constant, not authored), and sets the wind-changed flag to one.
Doc 05 owns the generator's energy contract; this section owns only the
callback gate and the draw order. The producer has **no null-VM guard**: a
scriptless definition authoring `WindGenerator` reaches a null dereference —
the same residual [R-COB-01 §1] records.

**Established — the second `SetSpeed` is the metal extractor, and its
argument is the metal sum.** The gate is the definition's **`ExtractsMetal`**
value greater than `0.0`. The pass walks the unit's footprint rectangle —
outer loop over the footprint's Z extent from the unit's stamped Z cell, inner
loop over its X extent from the stamped X cell — and for every **in-bounds**
cell adds `(unsigned metal byte of the plot cell) + 1` into a **16-bit**
accumulator, which therefore wraps modulo 65536. Off-map cells contribute
nothing. It then stores the unit's extraction rate as
`(float)(sum << 16) · ExtractsMetal / 65536` — the shift and the reciprocal
cancel arithmetically, but the intermediate is loaded as a **signed** 32-bit
integer, so a sum of `0x8000` or more yields a negative rate. Finally, only
when the unit has a VM, it starts `SetSpeed` deferred with the accumulator
**sign-extended from 16 bits**.

So the Missing-list item "semantic unit of the footprint-path `SetSpeed`
argument" is closed: it is *the summed metal-map value under the extractor's
footprint*, one metal byte plus one per covered cell, in metal-map units —
which is why stock extractor scripts use it to size their animation rate. The
metal byte is the plot cell's offset-7 byte of [03 §2.2].

**Established — the extractor `SetSpeed` is a creation-time producer only.**
Its three call sites are the three unit creators; a reference census finds no
other caller. The rate and the callback are therefore computed once, when the
extractor is created, and never recomputed — the wind pair, by contrast, is a
per-visit producer behind a per-tick gate. An implementation that re-runs the
extraction pass per tick issues `SetSpeed` callbacks retail never issues.

### Closed — the `Aim*` completion closure target (R-1) [R-CB-01 §6] (2026-08-29)

**Established — the residual is closed; the target is a two-branch setter.**
Section 5.3 and section 5.4 both carried the open marker R-1 for "the
identity of the closure target behind the receiver word", noting that a
bounded indexed-store census found no writer of that word. The census missed
it because the writer is **not indexed**: at every unit creation a
three-iteration loop walks the three weapon slots by repeatedly adding the
slot stride to a running pointer and stores one compiled-in address into each
slot's receiver word. That address is a fixed two-entry dispatch table in
read-only data; it is the same table for every unit, every slot and every
definition, and nothing ever overwrites it.

The receiver the producer passes is therefore a pointer to that word, and the
interpreter's return path — which reads the word, then calls the function its
target's first entry names, passing the returned cell and the receiver itself
as the object — invokes the table's **first entry**. That function is:

```
if (deliveredValue != 0) slot.aimReady = 1;
```

Two branches, an eighteen-byte function, with a store of the literal `1` (not
of the delivered value) into the same slot word the aim producer clears to
zero immediately before each `Aim*` start. It has **no code reference at all**
— its only reference in the whole image is the data reference from the
dispatch table — so this closure is its sole reachable use, which is why the
earlier caller-based searches could not find it.

This confirms, from the target's own body rather than by inference, the grant
rule already stated in [R-COB-02 §1]: zero has no effect, any nonzero value
marks the weapon aim-ready, and the marked value is exactly `1`. It also
settles the failure edges: name absence and pool exhaustion invoke this same
function with `0`, which is a no-op, so the slot's aim-ready word simply stays
at the zero the producer wrote — an implementation may model the failure as
"nothing happens" rather than as a distinct state.

**Established — the table's second entry, and what it is not.** The dispatch
table's second entry is a four-argument stub that returns zero. **Unknown:**
its caller. A bounded indirect-call census over the weapon and unit regions
locates no call through the table's second slot; the "per-weapon permission
function" section 5.3 mentions is a different pointer, held by the **weapon
definition** and called with the unit, the slot record, the resolved target
and the aim point — it is the projectile spawner, not this stub. Decider:
static trace of every indirect call whose target expression is a load from a
weapon-slot word.

### Closed — the unassigned `Killed` variant cell [R-CB-01 §7] (2026-08-29)

Section 5.4's Missing-list item asked what the variant cell serializes when
neither the script assigns it nor the work-fraction gate forces zero.

**Established — it round-trips the caller's uninitialized stack slot.** The
death handler keeps the severity and the variant in two locals of its own
frame. On the two bypass paths both are written before the packet is built
(cause 7 writes severity 0 and variant 1; causes 4, 5, 9 or a still-positive
health value write both zero). On the query path only the **severity** local
is written — it is computed, clamped to `1..100`, and passed as cell 0, so the
script *receives* the engine's severity and may overwrite it. The variant
local is never written before the query. Because the synchronous query seeds
each window word from the caller's cell and copies the window word back
afterwards, a script that does not assign its second parameter causes the
variant local to be read out of the frame, into the script window, and written
straight back — unchanged. The value the packet then carries is whatever
16-byte frame reservation that slot inherited from the previously executing
code, masked to four bits when it is packed as `(cause << 4) | (variant & 0xf)`.

This is indeterminate but **bounded and local**: it is stack residue in the
death handler's own frame, not script state, not heap, and not a pool value.
It is also not observable through the script — the round trip is invisible to
a script that ignores the parameter. Retail's behavior is therefore
"unspecified four-bit value"; a Nanolathe implementation must pick a bounded
substitute (zero is the natural one, and matches both bypass paths) and record
it as a sanctioned divergence rather than pretending the value is defined.

The severity arithmetic on the query path, restated exactly because the
division widths matter: the signed 16-bit health is negated and multiplied by
100 as a signed value, that product is divided by the definition's maximum
damage as an **unsigned** 32-bit divide, the prior-window severity sample byte
is added, and the sum is halved by a **signed** divide truncating toward zero;
the result is clamped to `1..100`. The packet is eleven bytes — a type byte,
the unit identity, a derived dword, the killer's identity or zero, the
severity byte, and the packed cause/variant byte — and it is transmitted only
when the owning player's kind byte is one of two values; the local
finalization consumes it either way.

### R-CB-01 §8 — what this unit leaves open

Three items, each carried as a bullet with its decider in this document's
"Missing and unknown" COB list: the caller of the aim dispatch table's second
entry ([R-CB-01 §6]); the order-form census behind the five `EndTransport`
producer sites ([R-CB-01 §2]); and whether a reused unit pool slot's surviving
weapon-slot flag byte can make the creation-time hold helper raise a
`TargetCleared` for the previous tenant ([R-CB-01 §4]). Nothing else in the
forty-name table is inferred: every mode, cell vector and gate above is
direct-static.

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
expand. The word therefore does not hard-block the A\*.

**Corrected by [R-PATH-01 §2] (§7.1, 2026-08-29):** the "coarse owner/building-mask word"
named in the paragraph above is the **per-player mapping memory** — the explored-terrain
record whose save section is named `Mapping` and whose fill is chosen by the session's
mapping option. Value 2 means "the requesting player has not explored this 2×2 block", and
its index carries a quarter-footprint offset. Buildings are not in that word at all; a
building hard-blocks the A\* through the class layer's **occupant-age gate** (step 2 of the
classifier above) once the class record's revision watermark passes the building's frozen
occupancy-commit tick. The movement commit validator (§8.2) remains a second, independent
gate. See [R-PATH-01 §2] for the full probe and the closure of the tail's building question.

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

**Established fact:** Diagonal movement is endpoint-only: only the destination cell's stamped terrain and building mask are tested, not both cardinal corner cells. A footprint is not swept during expansion because the profile stamp already marked cells that would intersect static blockers. The test's exact encoding and its only-blocking value are in [R-DOC04-B]; the coarse word it consults is corrected in [R-PATH-01 §2] below.

**Closed — duplicate suppression ordering (2026-08-26):** a neighbour that is
already open is re-visited, the new cost is computed FIRST, and the parent/
direction are updated only when the new cost is STRICTLY lower; equal-cost
re-visits never re-parent (matching the heap's strict-less ordering). There
is no suppression before cost computation.

**Closed — greedy-ray meet rule (2026-08-26, extended by [R-PATH-01 §5]):**
the ray is FORWARD-ONLY from the start toward the goal — there is no reverse
search from the goal and no bidirectional meet. It terminates when it reaches a
goal-flagged cell (return 0), when its scaled-heuristic budget reaches 0
(return 0), or when a full wall-follow sweep returns to the starting cell with
the same direction (return the best scaled h seen, which seeds the A* threshold);
the returned value is the minimum scaled h along the ray. Section 7.2's
"bidirectional" wording is superseded. [R-PATH-01 §5] adds the wall-follow's
own arithmetic, which is two-sided.

**Closed — debt-array layout (2026-08-26, superseded in part by [R-PATH-01 §6]):**
the per-player budget state is
two 10-entry globals indexed by player slot — cumulative step counters and
per-player budgets, the latter recomputed every 150 ticks from the counters
as `counter / divisor` tiered to `requestBase × {6, 3, 1}` — plus a
per-request 10-word accumulator array that receives `totalSteps /
playerCount` per tick and whose SUM is the scheduler's per-call step budget.
There is no per-player-direction debt. The claim that the per-player budget
*is* a work slice is corrected by [R-PATH-01 §6]: it is only the heuristic
weight. The heuristic-scale settings string is closed by [R-PATH-01 §10].

### Closed — the search working set, entry array, and touched bitmap [R-PATH-01 §1] (2026-08-29)

There is exactly **one** path-search working set for the whole session,
allocated once at battle setup as a single 201-byte record and torn down at
battle end. It holds **one search at a time**: a single active-request unit
reference, a single publication target, a single bound movement-class record.
When that reference is null the scheduler may admit a new request; while it is
non-null every scheduler slice advances that one search. There is no per-unit
and no per-player search state.

**Established — the per-cell entry array.** At construction the working set
allocates `((mapWidth · mapHeight) + 7) & ~7` **32-bit entries**, one per
attribute cell, indexed `mapWidth · z + x`. The four bytes are:

| byte | contents |
|---|---|
| 0 | status (below) |
| 1 | the direction index 0–7 by which this cell was reached |
| 2–3 | the 16-bit index of this cell's node record, valid only while the status is *open* |

Status byte bits:

* bits 0–1 are a state: **0 untouched**, **1 open**, **2 closed** (written as
  the whole byte `2` when the cell is popped, which also clears bits 2 and 3),
  **3 rejected** (the cell was probed and read impassable). Expansion accepts
  only states 0 and 1; 2 and 3 are dead ends.
* bit 2 (`4`) — **acceptable terminal**. Set by goal enumeration (which writes
  the whole byte as `4`, leaving the state *untouched* so the cell can still be
  opened) and by an expansion whose scaled heuristic is at or below the
  acceptance threshold. Popping a cell with this bit reconstructs and publishes.
* bit 3 (`8`) — **ray-visited**. Set only by the pre-search ray of
  [R-PATH-01 §5]. Its one consumer is the expansion's blocked-cell arm: a cell
  that reads impassable is marked *rejected* and abandoned **unless** this bit
  is set, in which case it is opened anyway. Because the ray only walks cells
  that were passable when it ran, this exemption fires only when the shared
  class layer is restamped between the ray and the expansion — which happens
  whenever a search spans ticks, since the layer is shared with every other
  request of the same movement class ([R-DOC04-B]).

**Established — the two-level touched bitmap.** Alongside the entry array the
constructor allocates one 32-bit word per **256 cells** (`ceil(cells/256)`
words). Cell `i` is covered by **bit `(i >> 3) & 31` of word `i >> 8`** — one
bit per **eight consecutive cells**, one word per 256. Every write to an entry
also sets that bit. This is the structure the engine names in its allocation
label as the search's *touched map entries*.

The clear pass that runs at the head of every request walks the words: for each
set bit it zeroes **byte 0 only** of the corresponding eight entries and then
zeroes the word, so clearing costs one pass over the bitmap plus eight byte
writes per touched group rather than a pass over the whole map. Bytes 1–3 of an
entry are therefore stale garbage until the status byte is written again;
nothing reads them while the state is *untouched*.

The constructor primes the bitmap so the very first clear is correct and
bounded: every word is filled with all-ones, the last word is then zeroed, and
the bits covering the final 256-cell block are re-set only for cells below the
padded cell count. The clear pass additionally bounds-checks each cell of the
last word against the cell count.

**Established — the open list.** The open list is a **binary min-heap of
pointers to node records**, keyed on `f`. A node record is 20 bytes and holds,
in order: its own index in the heap array, the packed cell coordinate as two
signed 16-bit halves, `g`, `f`, a 16-bit stored per-node terrain term
([R-PATH-01 §3]), and a 16-bit straight-run counter. Nodes come from a pool
with a free list (`-1` sentinel) and a high-water mark; when the pool is full
it grows to `capacity + capacity/2 + 16` and both the pool and the heap array
are reallocated and re-pointed.

**Established — tie order.** Sift-up moves a child above its parent only on
strict `<`, and stops as soon as the parent's key is `<=` the moved node's.
Sift-down picks the **left** child when the two children's keys are equal
(the right child is chosen only on strict `<`) and stops when the moved node's
key is `<=` the chosen child's. Equal `f` therefore never reorders an existing
heap, so ties resolve to insertion order, which is the fan order of §7.1.

**Established — the popped-node recycle.** A pop does not immediately free its
node. The scheduler copies the root's fields into locals, sets a *root is
spent* flag, and expands. The first neighbour that is newly opened during that
expansion **overwrites the spent root's record in place** and sifts it down
from index 0, clearing the flag; a relaxation that displaces the spent root
from index 0 instead frees it and compacts the heap. If neither happens, the
next pop frees it first and then reads the new root. One consequence matters
for the exhaustion test: the scheduler treats **heap size equal to the
spent-root flag** (0/0 or 1/1) as "no route", not heap size zero.

### Correction — the coarse word the search tests is mapping memory, not a building mask [R-PATH-01 §2] (2026-08-29)

**What the earlier text said.** [R-DOC04-B]'s "path-search consumption"
paragraph and §7.1's diagonal paragraph describe the search's passability test
as returning `2` when "the requester's bit [is] absent in the coarse
owner/building-mask word (one 16-bit word per 2×2-cell block, bit per player
slot)", and doc 04's tail asks whether a building hard-blocks through that
channel.

**Why it was wrong.** The array is the **per-player mapping memory** — the
explored-terrain record. Three independent sites settle it: the save writer
emits it under the section name `Mapping` with a length of half a byte per
attribute cell (one 16-bit word per 2×2 block); the map-load initializer fills
it with all-ones when the session's *mapping* game-option bit is clear and with
zeroes when it is set (fog on ⇒ nothing explored yet); and the visibility
writer ORs the viewing player's slot bit into the word as blocks become seen.
No building writes it.

**The corrected contract — Established.** The search's passability probe, given
the bound movement-class record and a cell, in order:

1. If the cell is outside the class record's own stamped extent (`x >= width`
   or `z >= height`, unsigned, so negatives fail too) → **0**.
2. Form the mapping-block index `bx = (x >> 1) + (FootPrintX >> 2)`,
   `bz = (z >> 1) + (FootPrintZ >> 2)` — half-resolution coordinates offset by
   a quarter of the class's authored footprint. If `bx >= mapWidth >> 1` or
   `bz >= mapHeight >> 1` → **0**.
3. If the requesting player's slot bit is **absent** from the mapping word for
   that block → **2**, without reading the terrain layer at all.
4. Otherwise return the stamped two-bit terrain value 0/1/3 ([R-DOC04-B]).

So value **2 means "this block is unexplored by the requesting player"**, and
every consumer treats it as passable. Retail units path optimistically straight
through fog; the terrain layer is consulted only where the player has already
mapped the ground. Map-edge handling is the pair of unsigned bound tests above
plus the expansion's own unsigned bound test against the working set's map
width and height — an out-of-range neighbour is skipped without being touched,
marked, or costed.

**Established — where a BUILDING blocks, closing the tail's open item.** The
class layer's classifier reads the attribute cell's occupant slot index — the
same field the movement commit stamps (§8.2) — and blocks the cell when an
occupant is present and that occupant's last occupancy-commit tick is **before**
the class record's revision watermark. The request-init revision pass advances
that watermark to `max(tick, 31) − 30` and restamps the footprint of every live
unit whose commit tick falls in the window just crossed, plus the requester's
own footprint. A building never commits a move, so its commit tick stops
advancing when it is placed; once the watermark passes it, its footprint cells
classify to **0 and hard-block the A\***, through exactly the same occupant-age
channel as a parked mobile unit and with a lag of at most 30 ticks plus one
request. No building-state byte exists and none is needed; the coarse word was
never the channel. The commit validator of §8.2 remains a second, independent
gate for the same footprints.

**Established — the stamp classifies a rectangle, not a cell.** The classifier
form that writes the layer takes the class's authored `FootPrintX × FootPrintZ`
rectangle anchored at the cell and aggregates the gates of [R-DOC04-B] over
every cell of it. When that rectangle comes back *clear*, four further
rectangle classifications run over the one-cell ring around it — the row above
(`x−1 .. x+FootPrintX−1`), the column right (`x+FootPrintX`, `z−1 ..
z+FootPrintZ−1`), the row below, and the column left — and the result is
demoted to *steep* unless every one of them is also clear. [R-DOC04-B]'s
"two-direction contagion pass" is this ring test; its "per attribute cell"
framing understates the rectangle sweep, and an implementation that classifies
single cells will not reproduce the layer for any class with a footprint larger
than 1×1.

### 7.2 A* state and costs

**Established fact:** The search maintains open/closed status, parent direction, a heap, and a packed coordinate per node. The heap key is `f = g + h`. Equal keys preserve insertion order because the heap compares strictly less, not less-or-equal. Equal `g` values do not replace an existing parent.

**Established fact:** Cardinal steps cost 16 and diagonal steps cost 22. A direction-change table adds turn penalties of 0, 40, 60, 80, 100, 80, 60, and 40 indexed by the raw fan offset — equivalently `(candidate − current) & 7` — so the table IS a turn-difference table (label now Established, not inference). A per-neighbour terrain term of 30 and a short-run penalty of 75 also apply; their exact conditions are in [R-PATH-01 §3], which supersedes both the 2026-08-26 "while the heap holds at most one entry" reading and the 2026-08-28 "unconditional on the live path" reading.

**Supported inference:** The eight-entry table is a turn-difference table, not a terrain-state table. The symmetric values and the absence of a terrain branch in the expansion support that interpretation. *(This label is now Established — the table is indexed by the raw fan offset, i.e. the direction difference; the sentence is kept for continuity.)*

**Established fact:** The search is a weighted A\*. Every heuristic value
passes through one scaling pipeline: `hScaled = (h · scale) >> 16`, with the
product formed as a full signed 64-bit multiplication and arithmetically
shifted; there is no floating point anywhere in the search. The scale is the
per-player quantum recomputed every 150-tick replenish from request pressure
as `base × {6, 3, 1}` for pressure tiers below 1, below 2, and at or above 2
respectively (tier = pending per-player pressure divided by a unit-cap
divisor); [R-PATH-01 §6] gives the exact counters and corrects the claim that
this quantum also sizes scheduler work slices — it does not. The base is
compiled in and is `0x18000` (1.5 in 16.16); the only writer is the developer
console ([R-PATH-01 §10]). Scaling never changes which cells read as
"close" (an h of 0 stays 0); it only re-ranks. h is evaluated exactly once
per allocated node and is NOT recomputed on relaxation — relaxation adjusts
`f` by the g delta alone.

**Established fact:** Goal objects form a virtual family, each supplying a
start predicate, a cell enumerator, and the heuristic. Three concrete classes
implement all three and drive ground searches; two more inherit the abstract
base's null implementations and never reach the search ([R-PATH-01 §9]):

- **Point/radius goals** use an inflated octile to the center cell,
  `h = 18·max(|dx|,|dz|) + 7·min(|dx|,|dz|)`, clamped to zero within an
  authored radius R (`h = max(oct − R, 0)`); arrival is a squared-distance
  test against a separately stored quantized radius.
- **Annulus (stand-off) goals** use the same inflated octile with a V-shaped
  zero band: h is zero inside `[inner, outer]`, rises as `oct − outer`
  outward, and as `inner − oct` toward the center — cells closer than `inner`
  are penalized, matching an arrival band that also rejects too-close cells.
- **Rectangle-perimeter goals** are exact outside the rectangle — the
  admissible octile `16·max(dx,dz) + 6·min(dx,dz)` to the rectangle — and
  inside measure `16 · min(distance to each edge)` back out; enumerated goal
  cells are exactly the rectangle border, where h is 0, and arrival requires
  lying on that border.

**Supported inference:** The {18, 7} form exceeds the true minimal geometric
cost `16·max + 6·min` by roughly 12.5–13.6 percent depending on run shape —
an intentionally inadmissible weighted octile that speeds the search and
biases straighter paths, partially masked because real paths also pay the
turn/first-step penalties. The point/annulus/rectangle semantic labels are
inferred from geometry and constructor argument shapes; every formula and
constant is direct.

**Established fact:** Arrival tolerance uses a write-once threshold. Before
seeding, a wall-following ray walk stores the minimum scaled h seen along its
frontier into a single slot that is never updated again. During
expansion, an opened neighbor whose scaled h is at or below that threshold
receives open-plus-goal status, and popping such a node terminates the search
and reconstructs — so EVERY opened cell within the tolerance region is an
acceptable route endpoint, not just enumerated goal cells. Enumerated goal
cells are additionally marked directly; enumeration is bounds-checked and
tracks the cell nearest the start by squared distance for the ray-check
direction choice. (The ray is forward-only; section 7.1's closure note
supersedes the earlier "bidirectional" wording, and [R-PATH-01 §5] gives its
wall-follow.)

**Established fact:** Early exits, in order after goal enumeration: a nonzero
start-satisfies-goal predicate publishes an empty route with completion
status `0x100` ("already satisfied") and stops; an out-of-bounds start cell
notifies status `0x200` and publishes empty; a ray that CONNECTS start to a
goal notifies `0x100` but the search is still seeded and runs; and when the
ray's best scaled h is at or above the start cell's own scaled h, the engine
notifies `0x200` and publishes empty WITHOUT seeding — the A\* starts only
when the ray proved a strictly closer frontier exists. Note that the `0x200`
notify is raised whenever the ray does **not** connect, including the case
where the search then proceeds normally; the bit reports the ray's verdict, not
the request's. These statuses are
path-request results reported to the requester — they are never interpreted as
queue-order completion [R-P0-01] (section 8.3).

### Correction — the per-neighbour 30 is the steep-tier terrain cost [R-PATH-01 §3] (2026-08-29)

**What the earlier text said.** Two readings have stood here. The 2026-08-26
text said "the fixed initial penalty of 30 applies while the heap holds at most
one entry (the start expansion)". The 2026-08-28 revision said "A fixed penalty
of 30 is added to EVERY neighbour in the live main-loop expansion path (the
only code shape where the term is absent has no callers in the bounded
census)", and doc 04's tail carried "the expansion applies no per-edge
terrain-state cost (bounded-negative)".

**Why they were wrong.** Both readings were looking at the wrong value. The
term is produced by a compare-and-borrow idiom over the **saved return value of
the passability probe for the candidate cell**, not over a heap length and not
over a caller-supplied register. The probe result is stashed on the stack
immediately after the call and re-read when the cost is assembled; the borrow
yields `0` when the value is **greater than 1** and `30` when it is `0` or `1`.

**The corrected contract — Established.** A neighbour's cost is

```text
terrainTerm = (passability > 1) ? 0 : 30
g(neighbour) = g(parent)
             + turnPenalty[(candidateDir − currentDir) & 7]      // 0,40,60,80,100,80,60,40
             + stepCost[candidateDir]                            // 16 cardinal, 22 diagonal
             + terrainTerm
             + (candidateDir != currentDir && parent.run < 5 ? 75 : 0)
f(neighbour) = g(neighbour) + hScaled
```

with `passability` the value defined in [R-PATH-01 §2]: `0` blocked, `1` steep,
`2` unexplored, `3` clear. Since a blocked cell is only ever costed when it
carries the ray-visited bit, the term's practical meaning is: **a step onto a
steep-tier cell costs 30 more than a step onto a clear or unexplored cell.**
The tail's bounded-negative "no per-edge terrain-state cost" is withdrawn — this
*is* the per-edge terrain-state cost, and it is the entire behavioural
difference between stamped values 1 and 3 for the search.

**Established — the term is stored, not recomputed.** The value is written into
the node record (the 16-bit field after `f`) when the node is first opened, and
a later relaxation of that node reuses the stored value rather than probing
again. The straight-run counter in the adjacent 16-bit field is set to `1` on
any turn and to `parent.run + 1` on a straight step, and is what the 75 tests.

**Established — evaluation order.** Turn penalty, step cost, parent `g`, and
the terrain term are summed in that order in 32-bit signed arithmetic; the 75
is added afterwards; `hScaled` is added last to form `f`. A relaxation accepts
the new parent only on **strictly lower** `g`, writes the new direction byte,
replaces `g`, adjusts `f` by the `g` delta alone, and rewrites the run counter;
it never re-evaluates `h`.

### Closed — request setup, the seeded start node, and the early-exit ladder [R-PATH-01 §4] (2026-08-29)

The order of operations when the scheduler admits a request is fixed and every
step is observable:

1. Bind the requesting unit's movement-class record (reached through the unit's
   mover) into the working set, store the goal object, and copy the unit's
   **cached committed cell** — not a recomputed quantization of its position —
   as the start cell.
2. Run the class-layer revision pass of [R-DOC04-B].
3. Write the turn-penalty table and the step-cost table into the working set.
   Both are constants of the code, not of the map or the unit: the turn table
   is `0, 40, 60, 80, 100, 80, 60, 40` and the step table is `16` at every even
   direction index and `22` at every odd one.
4. Run the touched-bitmap clear of [R-PATH-01 §1].
5. Ask the goal object to enumerate its cells. For each: if the cell is inside
   the map, write its entry status byte to `4` and set its touched bit; then —
   **whether or not it was inside the map** — compare its squared distance from
   the start cell against the running minimum, keeping the first on a tie. The
   winner is the ray's target. An out-of-bounds enumerated cell therefore
   cannot be reached but can still steer the ray.
6. Ask the goal object's start predicate about the start cell. Nonzero →
   notify `0x100`, publish empty, release the request, return.
7. Compute the start cell's scaled heuristic.
8. If the start cell is outside the map (unsigned compare against map width and
   height) → notify `0x200`, publish empty, release, return.
9. Run the ray ([R-PATH-01 §5]) and store its return as the write-once
   acceptance threshold. Zero → notify `0x100`. Nonzero → notify `0x200`, and
   if the start's own scaled heuristic is **at or below** the threshold,
   publish empty, release, and return without seeding.
10. Reset the heap (size 0, free list empty, spent-root flag clear), set the
    start cell's touched bit, OR `1` (open) into its status byte, and write its
    direction byte as `(heading + 0x1000) >> 13 & 7`.
11. Allocate the start node with `g = 0`, `f = the start's scaled heuristic`,
    and **run = 100**. The stored terrain term of the start node is never
    written; the value is unreadable in practice because the start cell is
    marked *closed* at its own pop before any relaxation can reach it. The run
    of 100 is what keeps the short-run 75 off the first step.
12. Set the fan half-width to 4.

Order matters at two places an implementation can get wrong: the
start-satisfied predicate is tested **before** the start's own bounds check, so
a unit standing off-map inside its goal's radius reports `0x100`, not `0x200`;
and the goal enumeration runs **before** either, so its marks and its nearest
cell survive both early exits.

### Closed — the pre-search wall-following ray [R-PATH-01 §5] (2026-08-29)

The ray is not a plain greedy walk. It is a cardinal-stepping probe with a
two-sided wall follow, and its only product is the acceptance threshold.

```text
best = scaled h of the start cell
if the start cell is impassable: return best
cur  = start cell
loop:
    charge one step to the scheduler slice
    if best == 0: return 0
    d = (goalX < curX) ? west
      : (goalX > curX) ? east
      : (goalZ <= curZ) ? north : south          // cardinal only, x tested first
    next = cur + d
    if next is impassable: go to WALL FOLLOW
    set next's touched bit; next.direction = d; next.status |= ray-visited
    if next.status already had the acceptable-terminal bit: return 0
    best = min(best, scaled h of next)
    cur = next
```

**WALL FOLLOW — Established.** Two cursors start at the last passable cell,
`hit`. One rotates its probe direction **upward** (`+1 mod 8`) starting from
the blocked direction, the other rotates **downward** (`−1 mod 8`) and steps in
the negated direction; they alternate, one probe each, each charging a step.
Each side, on finding a passable cell, marks it exactly as the greedy walk does
(touched bit, direction byte, ray-visited bit) and returns 0 immediately if that
cell already carried the acceptable-terminal bit.

A side ends the whole ray, returning `best`, when it comes back to the cell it
started from with the same probe direction on a second visit, or when its probe
sweeps all eight directions without finding a passable cell.

A side **rejoins the greedy walk** when its candidate lies on the axis-aligned
leg from `hit` to the ray's target. With `DX = goalX − hitX`,
`DZ = goalZ − hitZ`, `dx = candX − hitX`, `dz = candZ − hitZ`, and the sign of
each axis folded so `DX, DZ >= 0` (negating the matching candidate term), the
test is

```text
(dz == 0 && 0 < dx && dx <= DX)  ||  (dx == DX && 0 < dz && dz <= DZ)
```

— that is, the candidate is on the horizontal leg between `hit` and the goal's
column, or on the vertical leg from that column to the goal. On a rejoin the
greedy loop resumes from that cell, and `hit` is re-established at the next
block.

**Established:** the returned value is the minimum scaled heuristic over every
cell the ray touched, including the start's own. Every step of both phases is
charged to the scheduler's step counter for the admitting slice, so a long
wall-follow eats directly into the same budget the pops draw on. The ray draws
no random numbers and never writes a node or the heap.

### Closed — the goal classes, exactly [R-PATH-01 §9] (2026-08-29)

There is one abstract base and **five** concrete classes, not four. The base
supplies a never-satisfied start predicate, an **empty** enumerator, and an
identically-zero heuristic.

| Class | Stored geometry | Start predicate | Enumerated cells | Heuristic |
|---|---|---|---|---|
| Point / radius | centre cell; an octile radius `R`; a squared cell radius `R2` | `dx² + dz² <= R2` | the single centre cell | `oct = 18·max + 7·min`; `oct < R ? 0 : oct − R` |
| Annulus | centre cell; octile `inner`, `outer`; squared `min2`, `max2` | `min2 <= dx² + dz² <= max2` | one cell: `(centreX, centreZ + (inner + outer)/32)`, the divide rounded toward zero | `oct <= outer ? (inner <= oct ? 0 : inner − oct) : oct − outer` |
| Rectangle | `x1, x2, z1, z2` | on the border | exactly the border | outside: `16·max(dxOut,dzOut) + 6·min(...)`; on the x-band: `16·dzOut`; on the z-band: `16·dxOut`; inside: `16 · min(x−x1, x2−x, z−z1, z2−z)` |
| Air work point | a target unit, a piece index, a mode word, a 3-D point; constructed from a 54-byte save record | base (never) | base (none) | base (0) |
| Air moving point | a 3-D point and a per-tick delta | base (never) | base (none) | base (0) |

**Established — the dual-radius contract is two stored fields, not a
conversion.** The point and annulus classes each store the octile radii the
heuristic clamps against **and**, separately, the squared cell radii the arrival
predicate compares against. Neither is derived from the other at query time.
An implementation must carry both; "fixing" the mismatch by deriving one from
the other changes both the heuristic shape and the arrival band [P0-13 A19].

**Established — the two air classes never reach the search.** Their goal
interface is the base's, so a search seeded on one would enumerate nothing and
weight everything zero. They cannot be reached: the **air** route follower's
repath poll returns zero unconditionally, so an aircraft is never admitted to
the ground path scheduler. The doc's earlier "Base/restored-from-save goals
have an identically-zero heuristic and a null start predicate, giving pure-g
(Dijkstra) behavior whose acceptance is decided solely by the enumerated cells"
is **withdrawn**: they enumerate nothing, so no such acceptance exists, and the
Dijkstra branch it described is unreachable. What is true is that these two
classes are serialized and restored — the air work point's constructor reads a
54-byte record — which is where the "restored-from-save goal" label came from.

**Established — the goal object also carries the request's status sink.** Every
class holds a reference to the order record that created it, and the search's
notifications OR bits into that record's pending word: `0x20` when the follower
observes the unit has reached the goal, `0x40` when an empty route is published
while the unit is **not** at the goal, `0x80` when a previous goal object is
released, `0x100` start-already-satisfied or ray-connected, `0x200` start
out-of-bounds or ray-did-not-connect. `0x40` is the "cannot get there" signal;
it is produced by the publisher, not by the search.

**Established — the goal-point query.** Beside the three A\*-facing methods
every class supplies a *goal point* query returning a 16.16 world position. For
a point goal it is `worldX = (2·cellX + FootPrintX) · 8` in 16.16 — the same
cell-to-world conversion route reconstruction uses — with the footprint bias
taken from the unit named by the owning order record. This query is what the
route-acceptance rule of [R-PATH-01 §8] measures against.

### Closed — the heuristic base is compiled in; the `Search` console command is its only writer [R-PATH-01 §10] (2026-08-29)

Doc 04's tail carried "Retail default content of the settings string feeding
the heuristic-weight parse, and any runtime surface that rewrites that base
outside settings application · asset census of the shipped settings". The
premise was wrong: there is no settings string and no shipped asset involved.

**Established.** The working set's constructor writes both tunables:

* the **per-scheduler-call total step allowance** = `1333`;
* the **heuristic base** = `0x18000`, i.e. **1.5** in 16.16.

The only code that overwrites either is the in-game developer console command
named `Search`, one entry in a table of sixteen console commands (`DPrint`,
`Edge`, `Include`, `Mem`, `MemDump`, `Move`, `PrintWeights`, `Profile`,
`Reload`, `ReloadAIProfiles`, `Save`, `SeaLevel`, `Search`, `SelBoxes`,
`Senderror`, `TreeDeath`). Its first argument is read as an integer and, when
nonzero, replaces the step allowance. When the command has exactly three tokens
its second argument is read with the C runtime's `atof`, multiplied by
`65536.0` as a double, and truncated toward zero into the heuristic base. No
registry key, INI file, TDF key or command-line switch reaches either field —
a full reference census of both fields finds the constructor, this handler, and
the readers only.

Consequently the effective heuristic weight in a shipped session is
`1.5 × {6, 3, 1}` in 16.16 — `0x90000`, `0x48000`, or `0x18000` — selected per
player by the tier rule of [R-PATH-01 §6]. The tail item and its open-question
marker are removed.

### 7.3 Scheduler budget, publication, and route storage

**Established fact:** Path work is budgeted. A global scheduler counter replenishes every 150 ticks. Per-player quanta use six-times, three-times, and one-times weighting based on scheduler state. Each active request is limited to 100 heap pops per scheduler call. [R-PATH-01 §6] states the exact counters, the admission walk, and what each charge buys.

**Established — what admits a unit to the scheduler [R-MOV-01 §7]
(2026-08-28).** The section did not say how a unit becomes a candidate. The
scheduler walks players round-robin and, for each unit it reaches, calls the
unit's route follower's request poll. That poll answers "wants a path" only
when the follower's **wants-repath** flag is set **and**
`lastRequestTick + 60 <= currentTick`, and on a yes it stamps the current tick
onto the follower and the scheduler charges **100** to the per-tick budget
before starting the search. The flag itself is armed by the follower's
per-tick service — once per mover tick, whenever a route is installed and
either the mover's blocked flag is set or fewer than two route points remain
([R-MOV-01 §3]) — and is cleared when a route is installed, and also by any
publication including an empty one ([R-PATH-01 §7]). A blocked or
route-exhausted mover therefore re-requests at most once every 60 ticks, with
no retry ceiling.

**Established fact:** Requests are full-or-empty. A route is published only after a goal is reached and reconstructed. Budget exhaustion leaves the heap and request active for later ticks; it does not publish the best partial prefix. Heap exhaustion publishes an empty route.

**Established fact:** Search allocation initializes the request, clears visitation state, enumerates goals, picks the nearest goal for heuristic setup, validates the start, and can perform a direct ray shortcut. Invalid starts and unreachable goals report failure through order-layer status and receive an empty route. Request init also runs the class-layer revision pass of [R-DOC04-B] (§6.1). The exact order is [R-PATH-01 §4].

**Established fact:** Route reconstruction walks predecessor directions
backward from the goal cell, storing the packed cell into a 64-entry ring at
`index & 63` each time the direction CHANGES (wraparound overwrites the
oldest), then appends the start cell. Emission walks masked indices downward —
so the START cell is emitted first and the goal last — converts each cell to
signed world coordinates using the request's half-footprint bias, and publishes
`min(directionChanges + 2, 64)` points. The exact expressions are in
[R-PATH-01 §7], which also corrects an earlier off-by-one in this count.

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
array upward. One further bit precedes the count — see [R-PATH-01 §8].

**Established fact:** The route export helper is NOT an active-route
predicate: for each requested index below the stored count it reads that
point, and for excess indices it REPEATS THE LAST STORED POINT; it performs
no active-bit check, and a zero count selects index −1, reading adjacent
non-point fields rather than yielding an empty result. Callers must gate on
the active bit themselves.

### Correction — the per-player quantum is the heuristic weight, not a work slice [R-PATH-01 §6] (2026-08-29)

**What the earlier text said.** §7.2 said "The scale is the SAME per-player
quantum that sizes scheduler slices … Because the same quantum sizes work
slices and weights the heuristic, a busier player both gets fewer pops and
searches more greedily." §7.1's debt-array note said the same. §7.3 said
"Per-player quanta use six-times, three-times, and one-times weighting based on
scheduler state" without saying what they weight.

**Why it was wrong.** A reference census of the ten-entry quantum array finds
exactly three sites: the constructor seeds it, the 150-tick replenish rewrites
it, and the scheduler copies `quantum[player]` into the request's heuristic
scale when it admits a request. Nothing else reads it. Work slices come from a
different array entirely, and that array is topped up with an **equal share**
per eligible player.

**The corrected contract — Established.** Per scheduler call — and the
scheduler is called once per tick, first, before the per-player unit sweeps:

1. If the session's player count is zero, do nothing.
2. Increment a call counter. When it exceeds 150, zero it and, for each of the
   ten player slots, compute `tier = serviceCount[p] / divisor` and set
   `quantum[p] = base × (tier < 1 ? 6 : tier < 2 ? 3 : 1)`, then zero
   `serviceCount[p]`. The **divisor is the session's per-player unit limit**,
   copied from the lobby unit-limit setting at battle setup. `base` is the
   compiled-in `0x18000` of [R-PATH-01 §10].
3. For each of the ten slots whose player record exists, whose state byte is
   1, 2 or 3, and whose observer byte is not `'\n'`: add
   `stepAllowance / playerCount` (integer division; `stepAllowance` is 1333 by
   default) to that player's **step accumulator**, and add the accumulator's
   new value to a call-local total.
4. While that total is positive, run one *iteration*, then subtract the
   iteration's step charge from both the total and the current player's
   accumulator.

An iteration is one of three things:

* **No request active** — charge 1; advance the round-robin player cursor past
  any player whose accumulator is below 1; increment that player's service
  count; advance that player's unit cursor by one unit, wrapping from the
  player's last unit to the first; and if that unit has a definition, a mover
  and a movement class, ask its route follower's repath poll. On a yes, latch
  the unit and the follower as the active request, charge a further **100**,
  copy `quantum[player]` into the request's heuristic scale, and run the
  request setup of [R-PATH-01 §4] — whose ray charges further steps to the same
  iteration.
* **Request active and the heap is exhausted** (heap size equal to the
  spent-root flag) — publish an empty route, release the class record, clear
  the active request. The charge is zero.
* **Request active with a live heap** — pop, mark the popped cell *closed*,
  expand its fan, set the fan half-width to 2, and repeat **until the
  iteration's step charge reaches 100**. Each pop charges 1.

**Consequences an implementation must reproduce.**

* **One search at a time, globally.** A second unit's request cannot start
  until the first publishes, exhausts, or is cancelled.
* **Fairness is by unit visits, not by search work.** The equal-share
  accumulator buys roughly `1333` unit polls per tick spread across players;
  a player whose accumulator runs out is skipped until the next call. There is
  no starvation guard beyond the round-robin and no priority: a single
  long search consumes 100-step slices from *its own* player's accumulator
  until that accumulator goes non-positive, at which point the loop ends for
  the tick with the request still latched, and resumes next tick.
* **Budget exhaustion returns nothing.** The loop simply ends; the heap, the
  node pool, the acceptance threshold and the fan width are untouched, and the
  follower is not notified. Only heap exhaustion and the early exits publish.
* **A busier player searches *less* greedily, not more.** The tier is
  `serviceCount / unitLimit`, and the service count rises with every scheduler
  visit to that player, so a heavily-visited player drops from `×6` to `×3` to
  `×1` — a *smaller* heuristic weight, i.e. closer to plain Dijkstra and
  therefore a wider, more thorough search. The earlier text asserted the
  opposite ("searches more greedily"); it is withdrawn.
* **Which tier is actually in force is a Supported inference.** The service
  count is incremented once per candidate poll, and the poll rate is on the
  order of 1333 per tick shared across players, so over a 150-tick window the
  count plausibly exceeds any unit limit in the 20–500 range and the steady
  state is `×1` with `×6` only in the first window. That reasoning is
  arithmetic, not traced: it depends on how many iterations actually run, which
  depends on how many units exist and how often searches latch. The tier rule
  itself and the divisor's identity are Established. *Would settle it: a
  manual retail observation of the developer overlay, or a static trace of the
  iteration count under a known unit population.*

### Closed — route reconstruction, world conversion, and the publication contract [R-PATH-01 §7] (2026-08-29)

**Reconstruction — Established.**

```text
ring[0] = terminal cell            // the cell whose pop ended the search
d       = entry[terminal].direction
n       = 1
cur     = terminal
while cur != start:
    d2 = entry[cur].direction
    if d2 != d:  ring[n & 63] = cur;  n = n + 1;  d = d2
    cur.x = cur.x − dx[d]
    cur.z = cur.z − dz[d]
ring[n & 63] = start
total = n + 1
count = min(total, 64)
for i in 0 .. count−1:
    c = ring[(total − 1 − i) & 63]
    out[i].x = ((int16)(c.x · 2) + FootPrintX) · 8
    out[i].z = ((int16)(c.z · 2) + FootPrintZ) · 8
publish(follower, out, count)
```

Three things this fixes in the previous text. **First**, the emitted order is
`(total−1−i) & 63` starting at `i = 0`, which reads the *last* ring slot
written — the start cell — first, so the published route runs **start to goal**,
in travel order; "newest first" was correct but read as if it meant goal-first.
**Second**, the count is `directionChanges + 2`, not `directionChanges + 1`:
the ring holds the terminal cell, one entry per direction change, and the start
cell. A straight route publishes exactly two points. **Third**, the terminal
cell's own direction byte seeds the comparison, so the terminal is never itself
counted as a change, and the start cell's direction byte — which request setup
wrote from the unit's heading — is never read, because the loop tests
`cur != start` before reading it.

**Cell-to-world conversion — Established.** `world = 16·cell + 8·FootPrint`,
evaluated as `((int16)(cell · 2) + FootPrint) · 8` in 16-bit arithmetic, where
`FootPrint` is the bound movement-class record's authored `FootPrintX` /
`FootPrintZ`. Points are stored as signed 16-bit whole world units. The same
conversion appears in the goal-point query ([R-PATH-01 §9]) scaled to 16.16.

**Publication — Established.** The publisher takes the follower, a point array,
and a count.

* **count > 0:** clamp to 20 if it exceeds 19, store the count, copy that many
  4-byte X/Z pairs into the follower's point array, then set the
  **has-waypoint** and **dirty** flags and **clear wants-repath**.
* **count == 0:** if a goal object is installed, ask its "is the unit already at
  the goal" query; if that says no, OR `0x40` into the owning order record's
  pending word. Then clear **has-waypoint** *and* **wants-repath** and set
  **dirty**. Neither the count nor the point array is written.

The previous text said the wants-repath flag "is cleared only when a route is
installed". It is cleared by **every** publication, empty ones included; what an
empty publication does not do is clear the 60-tick throttle, so the follower's
next per-tick service re-arms the flag and the next request is at least 60 ticks
away.

### Closed — the route follower's protocol and the route-acceptance rule [R-PATH-01 §8] (2026-08-29)

The follower's four vtable slots that [R-MOV-01 §3] left unread are now named,
and the route object's own protocol with them is established.

**The route/goal object's side.** The follower calls four of its methods:
*is the unit at the goal* (per-tick service and empty publication), *does this
cell satisfy the goal* (route acceptance, and the search's start test), *give me
the goal point* (route acceptance and the synthetic fallback), and *does the
route persist after arrival* — the last returns 0 for all three A\*-facing
classes, so arrival always detaches the route. Arrival ORs `0x20` into the order
record's pending word before detaching.

**The four previously-unread follower slots — Established.**

| Slot | Contract |
|---|---|
| *needs republication* | Returns true when the dirty flag is set, or when the mover's blocked flag differs from the copy the follower cached at its last serialization. |
| *serialize* | Writes, into a bit stream: **one bit** = the mover's blocked flag; then a **2-bit count** = `has-waypoint ? min(pointCount, 3) : 0`; then, for each of those points, a 16-bit X and a 16-bit Z. It then clears the dirty flag and refreshes the cached blocked bit. The leading blocked bit is only *set* when blocked — the stream word is zeroed on allocation, so an unset bit relies on that pre-zeroing. This is the writer behind §7.3's save representation. |
| *(unnamed fourth)* | An empty method. It exists to fill the slot; no behavior. |
| *debug draw* | Draws the route as `pointCount − 1` line segments between consecutive points, in one of two palette entries chosen by the has-waypoint flag, under the developer overlay only. |

**Established — the route-acceptance rule.** When a newly published route (or a
new goal object) is installed, the follower does, in order:

1. Cancel any in-flight search that belongs to this follower.
2. If a goal object was already installed, OR `0x80` into its order record's
   pending word (release).
3. Clear **has-waypoint**; adopt the new goal object.
4. If the new object is null, also clear **wants-repath** and stop.
5. Otherwise **set wants-repath**, then try three acceptance gates in order:
   1. **Terminal-cell test.** If the follower holds **three or more** points,
      quantize the last stored point to a cell (arithmetic shift right by 4 on
      each signed 16-bit coordinate) and ask the goal's *does this cell satisfy
      the goal* predicate. If yes: clear wants-repath, set has-waypoint, done.
   2. **Half-distance test.** Ask the goal for its goal point; if it declines,
      stop. If the follower holds three or more points, compute
      `dU = trunc(hypot(unitX − goalX, unitZ − goalZ))` on the unit's 16.16
      position and `dP = trunc(hypot((lastPoint.x << 16) − goalX,
      (lastPoint.z << 16) − goalZ))`, both as `hypot` in double precision on
      the raw 16.16 integers, truncated toward zero. If `2·dP < dU`, set
      has-waypoint: the route is **accepted**. The route is therefore rejected
      whenever its terminal point does not get the unit **strictly inside half**
      its current distance to the goal.
   3. **Synthetic fallback.** If still not accepted: if the unit has a current
      order record and that record's retiring flag is clear, overwrite the
      follower's route with exactly **two** points — the unit's own integer
      position and the goal point truncated to whole world units
      (`>> 16`) — set the count to 2, and set has-waypoint. So a rejected route
      does not leave the unit idle; it leaves it walking a straight line at the
      goal.
6. Finally, if the follower's last-request tick is more than 10 ticks old, zero
   it (so the 60-tick repath throttle does not delay the next request), and set
   the dirty flag.

Both point-count gates require **three or more** stored points; a one- or
two-point route skips straight to the synthetic fallback, which would rewrite
it with the straight line.

**Unknown:** the semantic name of the order-record flag that suppresses the
synthetic fallback. It is set by one disposition branch of the queue pump
(§3.3) on an order it is taking down. *Decider: static trace of that
disposition branch.* `TODO(question)`

**Established — aircraft never enter this scheduler.** The air route follower
is a separate class whose repath poll returns zero unconditionally and whose
has-waypoint answer is simply "a target object is installed". Its remaining
slots mirror the ground follower's. Flight steering consumes the goal point
directly (§10) and no A\* runs for it.

### 7.4 Goals, build sites, and revalidation

**Established fact:** Goal objects enumerate one or more goal cells and supply
start/neighbor heuristic functions. The three A\*-facing families of section 7.2
enumerate, respectively: the single packed center cell; a single cell biased
along z by `(inner + outer) / 32` toward the far side of the stand-off ring
(bias intent inferred, arithmetic direct); and exactly the rectangle border.
The two air families enumerate nothing and never reach the search
([R-PATH-01 §9]). A radius-unit
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

**Established fact:** Dynamic blockers update a profile revision. Existing heap entries are not eagerly purged; passability is rechecked lazily when a node is expanded. This can turn a previously open node into a blocked one without rebuilding the whole heap. Because the class record and its layer are shared by every request of that movement class and a search spans ticks, this also means a cell the pre-search ray marked can become impassable before the expansion reaches it — the ray-visited bit of [R-PATH-01 §1] is what lets the expansion open it anyway.

**Established — out-of-bounds goal cells.** Enumerated cells outside the map are
not marked and cannot be reached, but they still take part in the
nearest-enumerated-cell comparison that aims the ray ([R-PATH-01 §4]). An
out-of-bounds *start* cell is a hard `0x200` reject before the ray runs. The
per-order-type census of which goal family and which radius each order
constructs is still open (below).

### 7.5 Smoothing

**Correction (2026-08-29, RWU-04-2) — there is no smoothing pass.** This
section said: "Route reconstruction removes collinear points and then performs
bidirectional ray checks. Each intermediate cell must pass the same passability
test. A shortcut is accepted for legality; the smoother does not compare the
shortcut's integrated cost against the original route."

That sentence fuses two real but unrelated mechanisms and invents a third. A
reference census of the route publisher finds exactly three callers — the
reconstruction, the request-setup early exits, and the scheduler's heap-
exhaustion arm — and the reconstruction is the only producer of points. Nothing
reads or rewrites the published array between reconstruction and the follower.

What actually exists:

* **Collinear removal — Established, and it is inside reconstruction.** The
  backward walk emits a cell only when its stored direction differs from its
  successor's, so a run of same-direction cells collapses to its endpoints
  ([R-PATH-01 §7]). This is the whole of the "collinear removal". It is exact,
  costs nothing, and cannot fail: every emitted point is a cell the search
  already expanded, so no passability re-test is needed or performed.
* **The ray — Established, and it runs BEFORE the search, not after.** The
  wall-following probe of [R-PATH-01 §5] runs during request setup, walks
  cardinally with a two-sided wall follow, and produces exactly one number: the
  acceptance threshold. It never shortcuts a route, never edits a point array,
  and cannot: at the time it runs no route exists. "Bidirectional" described
  its two-sided wall follow, not a start-and-goal meet.
* **A cost comparison — absent, correctly.** The old sentence's last clause is
  the only part that survives, and only vacuously: there is no shortcut
  acceptance step at all, so there is nothing to compare a cost against.

**Established — what an implementation must therefore not do.** Do not
post-process the published route: no line-of-sight shortcutting, no corner
cutting, no re-validation of intermediate cells. The follower receives the
reconstruction's output verbatim (clamped to 20 points), and the only later
edits to it are the waypoint pruning of §7.3 and the route-acceptance rewrite
of [R-PATH-01 §8].

### Closed — the path search draws no random numbers [R-PATH-01 §11] (2026-08-29)

**Established (bounded negative).** Neither the simulation stream nor the CRT
stream is touched anywhere in the path-search call graph: the scheduler, the
request setup, the ray, the expansion, the passability probe, the visitation
clear, all four heap operations, the reconstruction, the publisher, the status
notifier, the working-set constructor, the request canceller, every method of
all five goal classes, and every method of both follower classes. The bound is
the complete callee closure of those functions in the reconciled function set.
The search's outcome is a pure function of the map, the class layer, the
mapping mask, the unit's cell and heading, the goal object, the heuristic
scale, and the step budget — and the step budget affects only *when* a route
appears, never *which* route, because budget exhaustion leaves every piece of
search state untouched.

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

### Closed — the ground mover, exactly [R-MOV-01 §1] (2026-08-28)

The four paragraphs above assert the existence of a heading clamp, an
accelerate-versus-brake choice, and "fixed-point thresholds" without naming a
single one. This closure supplies the arithmetic; the pitch-cap paragraph is
unchanged and is quoted by reference below. Corrections to the other three are
called out where they occur. Every claim here is direct static trace unless it
says otherwise.

**Units used throughout.** World coordinates and velocities are 16.16 signed
fixed point (`wu` = one world unit = `65536`). Angles are unsigned 16-bit,
`65536` per circle, stored and added with 16-bit wraparound. Terrain heights
are unsigned bytes in the same world-unit scale as the coordinate high word, so
the unit's *integer height* is the signed high word of its 16.16 Y and is
directly comparable to the map's sea-level byte. Ticks are the 30 Hz simulation
tick of section 1.1.

**Order of operations — Established.** The per-unit sweep of section 8.3 runs,
for a live unit whose owner's player-state byte is `1` or `2` and whose mover
pointer is non-null, the order pumps and then **the mover tick**, and
immediately after the mover tick the **post-move Y/orientation correction**
(§5). Nothing else runs between them for that unit; the next unit slot is not
visited until both have finished.

The mover tick is exactly five calls in this order:

1. **Route-follower service** — revalidate the installed route, consume the
   reached waypoint, arm the repath-request flag (§3).
2. **Steering** — the ground steering of §2/§4 when the definition's `canfly`
   bit (bit 11) is clear, otherwise the flight integrator of section 10.1. The
   branch is on the definition bit only, never on the mover mode.
3. **Position and occupancy commit** — section 8.2 (and its blocked-mover
   clamp, [R-MOV-01 §7]).
4. **Movement-rate callbacks** — `StartMoving`/`StopMoving`/`MoveRateN`
   (section 5.2), classified from the speed the steering step just wrote (§6).
5. **`setSFXoccupy` band classification** — section 9.1.

**State the tick touches — Established.** The mover instance holds: a velocity
triple (16.16 per axis), a lean-residual triple used only by flight, a scalar
speed word (16.16, never negative), a signed 16-bit turn residual, the tick of
its last committed position, and a state byte whose low two bits are the
movement mode and whose bit 2 is the blocked flag. The unit holds roll,
heading and pitch as three signed 16-bit words, the 16.16 X/Y/Z triple, the
cached committed cell pair, the packed half-cell footprint bias, the carrier
pointer, and a flags word carrying the mover-mode mirror (bits 0–1), the
movement-rate tier (bits 2–3), death-pending (bit 14), transform-dirty
(bit 16), and the live bit (bit 28, set when the definition is bound at spawn).

The definition contributes `MaxVelocity`, `BrakeRate`, `Acceleration`,
`MoveRate1`, `MoveRate2` — all through document 02's **fixed-point** accessor,
so the authored value is multiplied by 65,536 and truncated toward zero and the
compiled field is already 16.16 — and `TurnRate` through the **integer**
accessor, stored into a 16-bit field. `MaxVelocity` is therefore world units
per tick and `Acceleration`/`BrakeRate` world units per tick squared, both
already scaled at compile time; there is no further per-tick division anywhere
in the mover. `TurnRate` is angle units per tick on the 65,536-per-circle
circle: an authored `TurnRate` of 475 is 475/65536 of a turn per tick, about
2.6 degrees per tick or 78 degrees per second. Defaults are `0` for
`MaxVelocity`, `BrakeRate`, `Acceleration` and `TurnRate`; `MoveRate1` and
`MoveRate2` default to `MaxVelocity << 1`.

`TurnRate` is written as a signed 16-bit field but every reader zero-extends
it, so the effective domain is `0..65535` and an authored value is taken
modulo 65,536.

### Closed — desired heading and the turn clamp [R-MOV-01 §2] (2026-08-28)

**The heading helper — Established.** One helper converts a planar offset to an
angle and is shared by the mover, the terrain conform (§5) and the flight
heading of section 10.1:

```text
angleOf(a, b) = round( atan2(a, b) * 65536/(2*pi) )
```

evaluated in x87 extended precision (`fpatan` on the two integers loaded as
integers, multiplied by the stored constant `10430.37835047` = `65536/(2*pi)`,
then stored with `fistp`, i.e. **round to nearest**, not truncation). The
result is used as a signed 16-bit angle.

**Argument order and sign — Established, and this is the non-obvious part.**
The mover computes

```text
desired = angleOf(unitX - targetX, unitZ - targetZ)
```

— the offset is taken **from the target to the unit**, not from the unit to the
target — and the velocity components derived from a heading (§4) are
**negated**. The two sign inversions cancel: a unit whose heading equals
`desired` moves toward the target. The first argument is the X offset and the
second the Z offset, so heading `0` points along `+Z` and increasing heading
rotates toward `+X`.

**The clamp — Established.**

```text
err = (int16)(desired - heading)          // 16-bit wrap, then sign-extended
if err == 0:
    turnResidual = 0                      // and NOTHING else happens
else:
    if      err >=  TurnRate:  turnResidual = +TurnRate
    else if err <= -TurnRate:  turnResidual = -TurnRate
    else:                      turnResidual =  err
    heading += turnResidual                // 16-bit add, wraps
    unit.flags |= transform-dirty
```

The two comparisons are signed 32-bit between the sign-extended `err` and the
zero-extended `TurnRate`. A zero error zeroes the residual **without** setting
the transform-dirty bit and without writing the heading — the same asymmetry
section 10.1 records for the flight integrator, because it is the same rule.
The clamp is a pure saturation: equality at either bound produces the same
value either way.

### Closed — the route follower, lookahead, and waypoint pruning [R-MOV-01 §3] (2026-08-28)

**Correction.** The paragraph above says "Waypoint lookahead, vertical
tolerance, and arrival tolerance are fixed-point thresholds" and "Waypoint
consumption and path publication are synchronous with the mover tick". The
second half is right. The first half is wrong in two ways: the arrival
tolerance is **not** fixed point — it is an integer world-unit squared distance
— and the ground mover has **no vertical tolerance at all**. Section 8.3's
"local settling family" paragraph (threshold `65536` when `(speed & ~3) <
262144`, else `speed >> 2`, compared strictly as `-t < dy < t`) describes the
**flight integrator's** vertical velocity clamp, which section 10.1 states in
the same arithmetic; the ground steering contains no Y term whatsoever and the
ground speed update writes vertical velocity as a literal zero every tick. The
section 8.3 attribution is a duplicate of the section 10.1 rule and should be
read as belonging to flight (owner: section 8.3; recorded here because the
claim is about ground steering).

**The follower — Established.** Each mover owns one route-follower object,
allocated by the mover's constructor and typed by whether the definition can
fly and whether the owner's player state is `3`. Its state is: the installed
route object (or none), the owning unit, an array of route points, a point
count, the tick of its last repath request, and a flags byte with three used
bits — **has-waypoint** (bit 0), **wants-repath** (bit 1), **published/dirty**
(bit 3). The constructor leaves has-waypoint and wants-repath clear and
published set.

**Route points are integer world coordinates — Established.** Each point is a
pair of signed 16-bit world X/Z. The array is ordered
`points[0] = the point just left`, `points[1] = the point being steered to`,
`points[2] = the one after that`. Steering asks the follower for **three**
consecutive points as 16.16 triples; the fill clamps the index, so
`emitted[i] = points[min(i, count-1)]` and each triple is
`(x << 16, 0, z << 16)`. When two points remain, the third emitted triple
repeats the last; when one remains, all three do.

**Waypoint pruning — Established.** Once per mover tick, before steering:

```text
if route is installed and route still accepts this unit:
      refresh the route object; if it reports exhausted, detach it
if count >= 2:
      dx = unitIntegerX - points[1].x
      dz = unitIntegerZ - points[1].z
      if dx*dx + dz*dz <= 25:            // inclusive, signed 32-bit
            shift the array down one; count -= 1
            if count < 2: clear has-waypoint
            set published
if route is installed and (blocked flag set or count < 2):
      set wants-repath
```

`unitIntegerX/Z` are the signed high words of the unit's 16.16 X and Z — the
same domain as the stored points, so the whole test is integer world units.
The **arrival tolerance for consuming a waypoint is therefore a radius of five
world units, inclusive** (`dx*dx + dz*dz <= 25`), measured to `points[1]`, and
exactly one point is consumed per tick. Pruning never writes the order's
satisfied word; order completion is section 8.3's separate tile-versus-goal
test, unchanged.

**The steering gate — Established.** Ground steering first asks the follower
whether it has a waypoint (bit 0). Bit 0 is set only by route installation and
cleared when the count falls below two, so **a follower with fewer than two
points has no waypoint**. With no waypoint the mover zeroes its turn residual
and calls the speed update with a delta of `-BrakeRate`; it does not turn, does
not read the definition's turn rate, and does not touch the heading.

**Waypoint lookahead — Established.** With a waypoint, let `T0`, `T1`, `T2` be
the three emitted triples. Distances here are `hypot` evaluated in double
precision on the raw 16.16 integers and converted back with truncation toward
zero (`__ftol`), so they are 16.16 distances.

```text
d1 = trunc( hypot(T1.x - unitX, T1.z - unitZ) )
if d1 > 0x500000:                          // strictly more than 80.0 wu
    L = trunc( hypot(T1.x - T0.x, T1.z - T0.z) )
    if L >= 0x10000:                       // segment at least 1.0 wu
        ux = ((T1.x - T0.x) << 16) / L     // 64-bit shift, signed divide
        uz = ((T1.z - T0.z) << 16) / L
        t  = min(d1 - 0x500000, L)         // signed
        T1.x -= (ux * t) >> 16             // 64-bit product, arithmetic shift
        T1.z -= (uz * t) >> 16
```

The steering target is thereafter the **modified** `T1`. The effect is a
pure-pursuit carrot: while the unit is more than 80 world units from its
current waypoint, it aims at a point dragged back along the incoming segment,
never past `points[0]`. Both guards fail closed — a shorter distance or a
degenerate segment leaves `T1` at the waypoint itself. The lookahead constant
is `0x500000`, exactly 80 world units, with a **strict** `>` test; the segment
guard is `>= 0x10000`, exactly 1.0 world unit.

**Repath — Established, and see [R-MOV-01 §7].** The wants-repath bit is the
mover's only outward request. The path-request scheduler of section 7.3 polls
each candidate follower once per visit; the poll returns "wants a path" only
when the bit is set **and** `lastRequestTick + 60 <= currentTick`, and it
stamps the current tick on success. A blocked or exhausted mover therefore
re-requests a route at most once every 60 ticks (two seconds).

### Closed — accelerate versus brake, and the speed update [R-MOV-01 §4] (2026-08-28)

**Correction.** The paragraph above says only that "Acceleration versus braking
is selected from the angle-to-waypoint and current speed" and that "Speed is
clamped to the definition's maximum". The first is half right — there are two
tests, and the second reads a *different* target from the first. The second is
misleading: there is no separate maximum-velocity clamp. The pitch table of
this section is the only speed ceiling, and its level-ground entry is 100, so
`MaxVelocity` is enforced *through* the pitch cap.

**The decision — Established.** All squared distances below are formed by
squaring each axis offset into 64 bits, **arithmetic-shifting each product
right by 32 separately**, and then adding — not by shifting the sum. Because a
16.16 offset squared and shifted right 32 is the offset in whole world units
squared, every comparison in this block is in world-units squared, and each
term is independently floored.

```text
A  = ((dx1*dx1) >> 32) + ((dz1*dz1) >> 32)   // dx1,dz1 = modified T1 minus unit
B  = ((dx2*dx2) >> 32) + ((dz2*dz2) >> 32)   // dx2,dz2 = T2 minus unit

turnDist = ((|err| & 0xffff) * speed) / TurnRate           // 64-bit product, signed divide
stopDist = (((speed * speed) >> 16) << 16) / (2 * BrakeRate)

if  A > ((turnDist*turnDist) >> 32) * 4   and   B > ((stopDist*stopDist) >> 32):
        delta = +Acceleration
else:
        delta = -BrakeRate
```

Both tests are **strict**: equality brakes. `|err|` is the absolute value of the
sign-extended heading error of §2, masked to 16 bits (a no-op except for the
`-32768` case). All divisions truncate toward zero.

Read as distances rather than squares, the two conditions are: *the modified
lookahead target is more than twice as far away as the distance I will cover
while finishing this turn*, and *the point two waypoints ahead is farther away
than my braking distance* `speed^2 / (2*BrakeRate)`. The second test is what
makes a mover slow down for its destination: when fewer than three route points
remain the emitted `T2` clamps to the last point, so near the end of a route the
braking test is measured against the final waypoint, while in mid-route it is
measured against the point two ahead.

**Two unguarded divisions — Established (edge, retail fault).** Neither
`TurnRate` nor `BrakeRate` is tested for zero, and both divisions execute on
every ground steering tick that has a waypoint. A mobile unit whose definition
authors `TurnRate = 0` or `BrakeRate = 0` (or omits either — both default to
zero) divides by zero and terminates retail, exactly as the flight integrator's
unguarded `MaxVelocity` division does (section 10.1). Valid mobile content must
author both. Nanolathe may bound this as a sanctioned divergence, but the
contract is a fault.

**The speed update — Established.** The steering step's only output is one call
with the signed `delta`:

```text
speed += delta
if speed < 0: speed = 0

i = pitch >> 11                            // pitch is the unit's signed 16-bit
i = clamp(i, -5, +5)                       //   pitch word; arithmetic shift
cap = table[i] * MaxVelocity / 100         // table as in this section, signed
                                           //   byte; 64-bit product, divide
                                           //   truncates toward zero
if unitIntegerHeight < seaLevel and (definition flags & 0x81000) == 0:
        cap = (cap * 0x8000) >> 16         // exactly half, arithmetic shift
if cap < speed: speed = cap                // strict; the only ceiling

vx = -( (sin[heading] * speed + 0x1000) >> 13 )
vy = 0
vz = -( (cos[heading] * speed + 0x1000) >> 13 )
```

`table` is the eleven-entry pitch table already established above
(`25, 55, 70, 85, 100, 100, 75, 50, 25, 20, 15` for index `-5` through `+5`).
The water half-speed test is strict `<` between the unit's signed integer
height and the map's sea-level byte, and requires both `canhover` (bit 12) and
`floater` (bit 19) to be clear. The vertical velocity component is written as a
literal zero: **a ground mover never has vertical velocity, and there is no
gravity term anywhere in the ground path.** Y changes only through the
post-move correction of §5.

**The trig table — Established.** One table of 512 signed 16-bit entries holds
`round(8192 * sin(2*pi*i/512))`. A component is

```text
component(angle, magnitude) = (table[((angle + 0x20) >> 7) & 0x1ff] * magnitude + 0x1000) >> 13
```

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Closed — the post-move Y, pitch, and roll correction [R-MOV-01 §5] (2026-08-28)

Immediately after the mover tick the sweep runs one correction that owns
everything the mover left alone: the unit's Y, its pitch, and its roll. This is
the "final integrator then applies terrain height, gravity and lean" clause of
this section — **there is no gravity and no lean here**; the ground path has
neither.

**The gate — Established.**

```text
if (unit.flags & transform-dirty) != 0 or definition has canhover:
    clear transform-dirty
    if mover exists and (unit mover-mode mirror) == 1:
        ... one of the four branches below ...
```

Mode `1` is the grounded mode (see [R-MOV-01 §8]), so every ground unit passes
the mode test for its whole life; a unit in flight (mode `2`) is skipped
entirely and its Y is owned by the flight integrator. A `canhover` definition
forces the branch every tick even when nothing moved, which is what animates
the hover bob below while parked.

**The four branches — Established, and this supersedes part of section 9.2.**

* `upright` set (bit 20) and `canhover` clear: `Y = terrainHeight(unitXZ) << 16`.
* `upright` set and `canhover` set: `Y = max(terrainHeight(unitXZ), seaLevel - waterline) << 16`,
  the comparison being `seaLevel - waterline < terrain ? terrain : seaLevel - waterline`.
* `upright` clear and `floater` set (bit 19): `Y = (seaLevel - waterline) << 16`.
  The executable computes this as a wrapping 32-bit expression that is
  algebraically `seaLevel - waterline` scaled by 65,536; section 9.2's
  "`floater` selects the ship surface clamp at `waterline + sea level`" has the
  **sign inverted** and is corrected by [R-MOV-01 §9].
* otherwise: the four-corner terrain conform below.

The first three branches write the whole 16.16 Y with a zero fraction. Only the
fourth writes pitch and roll — **so an `upright` unit and a `floater` unit never
receive a terrain pitch, their pitch word stays at whatever it was (zero for a
unit that has never flown or been carried), and the pitch cap of §4 therefore
always yields 100 percent of `MaxVelocity` for them.** Slope speed penalties
apply to non-`upright`, non-`floater` ground movers only.

**The four-corner terrain conform — Established.** The conform reads the first
four entries of the vertex-index list of the **selection primitive** of the
unit definition's compiled model root object — the "ground plate" of
`[fmt 3do]` "Selection primitive", taken as a plain **index** into the root's
primitive array as it stands after model load — and treats their model-space X
and Z as its ground-contact quad. The stored index is tested for
non-negativity first, so a root that stores `-1` (the two stock cases named in
`[fmt 3do]`) gets **no terrain conform at all**: its height, pitch and roll are
never written by this path. This is the engine-side reason the community lore
in `[fmt 3do]` reports that a vehicle with an inverted ground-plate winding
"flips out on slopes" — the plate's vertex order is what the pitch and roll
expressions below difference. For each corner `i` in `0..3`, with `(mx, mz)`
the vertex's model-space X and Z in 16.16:

```text
(rx, rz) = rotate(mx, mz) by the unit heading
        // float sin/cos of heading * (2*pi/65536), each component rounded to nearest;
        // the rotation is skipped entirely when heading == 0
wx = (int16)((rx + unitX) >> 16)
wz = (int16)((unitZ - rz) >> 16)           // note the subtraction on Z
if (unsigned)(wx >> 4) >= mapWidthCells - 1:  ABANDON the whole correction
if (unsigned)(wz >> 4) >= mapHeightCells - 1: ABANDON the whole correction

cx = wx >> 4 ; cz = wz >> 4 ; fx = wx & 15 ; fz = wz & 15
top    = h(cx, cz)   + trunc16( (h(cx+1, cz)   - h(cx, cz))   * fx )
bottom = h(cx, cz+1) + trunc16( (h(cx+1, cz+1) - h(cx, cz+1)) * fx )
height[i] = top + trunc16( (bottom - top) * fz )
```

`h` is the raw terrain height byte of the attribute cell (not the derived
per-cell minimum/maximum of section 6.1), `trunc16(v)` is `v / 16` truncated
toward zero, and the two divisions are applied in that order — X first on both
rows, then Z between them. The bounds test is unsigned, so a negative world
coordinate fails it; **when any corner is out of bounds the correction returns
without writing height, pitch or roll, and the unit keeps its previous
orientation.** The cell size is 16 world units.

Then, once:

```text
A = (height[0] + height[1]) / 2            // signed, truncating toward zero
B = (height[2] + height[3]) / 2
unitIntegerHeight = (A + B) / 2            // written as the HIGH WORD of Y only;
                                           //   the 16 fractional bits are left alone
pitch = angleOf( B - A, (int16)(|mz0 - mz3| >> 16) )
roll  = angleOf( height[0] - height[1], (int16)(|mx1 - mx2| >> 16) )
```

`angleOf` is the §2 helper. The rise terms are in height-byte units and the run
terms are the **unrotated** model-space spans truncated to signed 16-bit whole
world units, so a model whose selection primitive is degenerate along either
axis produces a run of zero and a pitch or roll of a quarter circle, which the
pitch index of §4 then saturates at `±5`. The pitch this writes is the pitch the
**next** tick's speed cap reads.

**The hover bob — Established, and it is the one non-tick input in the mover
chain.** When the definition has `canhover`, the unit's live bit (bit 28) is
set and its death-pending bit (bit 14) is clear, the per-corner height is
computed differently:

```text
hh = top + trunc16( (bottom - top) * fz )
if hh <= seaLevel: hh = seaLevel                   // hover floats over water
r  = animationCounter & 0x1f
angle = (int16)( ((r + 8*i) << 11) + unit.bobPhase )
v   = min(speed, MaxVelocity / 2)                  // MaxVelocity/2 truncates toward zero
amp = 2 - ( ((v << 16) / (MaxVelocity / 2)) * 2 >> 16 )      // 2, 1 or 0
age = min((unsigned)(currentTick - mover.lastCommitTick), 60)
amp = amp - (amp * age) / 60                       // unsigned divide
height[i] = hh + component(angle, amp)             // the §4 trig-table form
```

`unit.bobPhase` is a per-unit signed 16-bit phase word; the `8*i` term puts the
four corners a quarter circle apart, so the unit rocks rather than heaves. The
amplitude is at most two height units, falls to zero at half `MaxVelocity`, and
fades linearly to zero over the 60 ticks after the mover last committed a
position. `MaxVelocity / 2` is an **unguarded divisor**: a `canhover`
definition with `MaxVelocity` below `2` faults, the same class of edge as §4's.

`animationCounter` is **not** a simulation random draw and not the tick
counter: it is `GetTickCount()` scaled by a configured rate and divided by
1000 — a wall-clock animation counter shared with the presentation layer. It
feeds the corner heights, whose average is written to the unit's authoritative
integer height word, which the medium-band classifier of section 9.1, the
water half-speed test of §4 and the water damage of section 9.2 all read. **A
hovering unit's committed height therefore depends on wall-clock time**
(Supported inference for the consequence — the read and the write chain are
direct; what is open is whether the ±2 perturbation can ever cross one of those
three thresholds in practice, which a retail observation or a bounded numeric
argument would settle). No other part of the mover chain reads a clock or a
random stream.

**Bounded negative — Established.** No simulation-RNG or CRT-RNG entry point
appears in the call graph of the mover tick, the ground steering, the speed
update, the position commit, the movement-rate classifier, the band
classifier, the post-move correction, the terrain conform, or the follower
service. The mover draws no random numbers at all.

### Closed — the movement-rate tiers [R-MOV-01 §6] (2026-08-28)

Section 5.2 states the tier classifier's shape correctly. Its inputs are named
here because they are authored keys the movement docs never resolved:
the two thresholds are the FBI keys **`MoveRate1`** and **`MoveRate2`**, read
through the fixed-point accessor and therefore in 16.16 world units per tick,
each **defaulting to `MaxVelocity << 1`**. Since the committed speed can never
exceed `MaxVelocity`, a unit that authors neither key is always in tier 1 and
only ever emits `StartMoving`, `StopMoving` and `MoveRate1`. The classifier
runs after the position commit and before the band classifier, reads the mover's
scalar speed and turn residual, and forces tier 0 when the blocked flag is set,
when the unit is attached to a carrier, or when speed and turn residual are both
zero.

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

### Correction — the blocked mover does request a new path [R-MOV-01 §7] (2026-08-28)

**What the earlier text said.** The "negative-bounded" paragraph of this
section, and `[R-MOV-02A]`'s "Blocked response and retries" and
"Yield/replan/priority — Unknown outside the bound", state that no
"automatic repath timeout" was recovered and that "a rejected proposal does not
… submit a new path request", with the bound named as "the recovered movement
sweep, mover fan-in, ground commit, footprint validator, path-request
initializer/scheduler/expansion, and ground-order completion call chains".

**Why it was wrong.** The bound was drawn around the wrong object. The request
is not made by the commit path and not by the scheduler walking units; it is
made by the **route follower** the mover owns, through a virtual poll that the
path-request scheduler calls once per candidate visit. Neither end of that call
appears as a direct call from any function in the listed bound, so a call-graph
search anchored on those functions cannot see it.

**The corrected contract — Established.** The follower's per-tick service (the
first call of the mover tick, [R-MOV-01 §3]) sets its **wants-repath** flag
whenever a route is installed **and** either the mover's blocked flag is set or
fewer than two route points remain:

```text
if route installed and (mover.blocked or pointCount < 2):
        follower.flags |= wants-repath
```

The path-request scheduler of section 7.3 polls each candidate follower; the
poll answers "yes" and stamps the current tick only when

```text
(follower.flags & wants-repath) != 0  and  lastRequestTick + 60 <= currentTick
```

— an inclusive comparison against a 60-tick (two-second) throttle, with
`lastRequestTick` zero-initialised so the first request after the flag is armed
is immediate. On a "yes" the scheduler charges 100 to its per-tick budget and
starts a search for that unit. The flag is not cleared by the poll; it is
cleared only when a route is installed (the follower's install path clears
bit 1 before deciding whether to accept the new route).

Everything else in the blocked-mover paragraph stands: the rejected proposal
still does not push, reverse, sidestep, rotate for clearance, or enter a wait
queue; it still caps scalar speed at half the movement definition's
`MaxVelocity`, recomputes velocity along the current heading through the trig
table of [R-MOV-01 §4], and clamps the committed position inside the old
footprint. There is still no blocked-tick counter and no random draw. What
changes is only the absence claim: **a blocked ground mover re-requests a path
at most once every 60 ticks for as long as it stays blocked**, and a mover that
has consumed its route down to one point does the same. The half-speed clamp
and the "reduces its next-step cap" sentence of section 8.1 are the same
mechanism, not two: the cap is the speed cap, applied after the position is
already proposed, so it takes effect from the following tick onward.

The dynamic-yield residual of `[R-MOV-02A]` is narrowed but not closed: no
yield, sidestep, retarget or priority comparison was recovered, and that
remains a bounded negative. What is now established is that the "outer caller
in the reviewed set" the note asked for exists for **replanning**, and is the
follower/scheduler pair above.

### Closed — the commit step, in order [R-COLL-01 §1] (2026-08-29)

This closure and the seven that follow are the RWU-04-9 pass over the
position-and-occupancy commit (call 3 of the mover tick, [R-MOV-01 §1]), its
footprint validator, the occupancy stamp and clear, the blocked flag's writer
and reader census, and the outer yield question left open by [R-MOV-02A].
Every claim is direct static trace unless marked otherwise. Vocabulary:
*cell* is one 13-byte attribute cell of 16 world units ([02 "Terrain file"],
[03 §1]); the *ground word* and *air word* are the cell's first two `uint16`
occupancy planes; a *self identity* is the unit's pool index; *size pair* is
the unit's copy of the definition's `FootPrintX`/`FootPrintZ` cells
([R-P0-02]); the *cached cell pair* is the unit's committed footprint anchor.

**Established — the step has one caller.** The commit is called only from the
mover tick; no order handler, network path or interface path calls it. Its
inputs are the mover's velocity triple and state byte, the unit's 16.16
X/Y/Z, flags word, size pair, cached cell pair, owner pointer, carrier
pointer and definition.

**Established — the carried branch.** When the carrier pointer is non-null the
step takes the carrier's hang position for the cargo ([R-AIR-01 §9]) and
passes it to the *carried-position setter* of §4 with the mover's mode bits;
it then copies the carrier's velocity triple and scalar speed into this mover
(zeroes when the carrier has no mover), clears the transform-dirty bit and
returns. Nothing below runs for cargo.

**Established — the stationary early return.** The proposal is
`proposed = position + velocity` per axis (16.16, 32-bit wraparound) and
`mode = mover.state & 3`. When all three proposed coordinates equal the
current ones **and** `mode` equals the flags-word mode mirror (bits 0–1), the
step returns with **nothing written** — no dirty bit, no tick stamp, no
validator, and the blocked flag untouched. A unit at rest never revalidates.

**Established — the last-proposal tick is written before validation.** On any
other proposal the mover's *last-proposal tick* is set to the current tick
**first**, before the cell test and before the validator. It therefore
records the last tick on which the unit *tried* to change position or mode,
blocked ticks included. It is the age the hover bob of [R-MOV-01 §5] reads.
(This corrects [R-MOV-01 §1]'s "tick of its last committed position" — see
§7.)

**Established — cell quantisation.** With `S = 0x80000` (8 world units, half
a cell) and the size pair `(fx, fz)`:

```text
cellX = (proposedX + S − fx·S) >> 20        (arithmetic shift)
cellZ = (proposedZ + S − fz·S) >> 20
```

i.e. `floor((p + 8 − 8·f) / 16)` in world units — the anchor whose rectangle
centre `(f + 2·cell) << 19` ([R-P0-02]) is the cell-quantised unit position.

**Established — the same-cell fast path.** When `(cellX, cellZ)` equals the
cached cell pair **and** `mode` equals the mode mirror, the step writes the
proposed X, Y and Z, sets transform-dirty (flags bit 16) and returns. No
validator, no clear, no stamp, no change to the blocked flag.

**Established — the validation gate.** Otherwise, when the owner pointer's
player record is active and its state byte is `1` or `2`, the validator of §2
is called with `(definition, self identity, packed cell pair, mode)` and the
mover's blocked flag (state byte bit 2) is **rewritten** with `result == 0`.
When the owner is not in state 1 or 2 the flag keeps its previous value and
the branch below is taken on that stale value. (The sweep of [R-MOV-01 §1]
only runs the mover tick for state-1/2 owners, so in practice the rewrite is
unconditional.)

**Established — the blocked branch.** With `c = (f + 2·cachedCell) << 19` per
axis (the old rectangle's centre) and `H = 0x7ffff` (half a cell minus one
16.16 unit):

```text
X = proposedX > c.x + H ? c.x + H : (proposedX < c.x − H ? c.x − H : proposedX)
Z = proposedZ > c.z + H ? c.z + H : (proposedZ < c.z − H ? c.z − H : proposedZ)
Y = proposedY                                             (never clamped)
half = trunc(MaxVelocity / 2)                             (cdq/sub/sar: toward zero)
if speed > half:                                          (strict)
        speed = half
        vx = −sinq(heading, half);  vy = 0;  vz = −cosq(heading, half)
```

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Established — the success branch, in order.** (1) *clear* the old footprint
(§4) at the cached cell pair; (2) write proposed X, Y, Z; (3) write the new
cell pair into the cached pair and `mode` into the mode-mirror bits;
(4) *stamp* the new footprint (§4) at the new pair and mode; (5) set
transform-dirty; (6) call the LOS coverage wrapper ([03 §3.2 R-VIS-01 §2]),
which floors the Y it publishes at `(seaLevel + 1) << 16`. Steps 1–6 finish
before the sweep visits the next unit slot.

### Closed — the mobile footprint validator, exactly [R-COLL-01 §2] (2026-08-29)

The validator is the shared entry of §6.4 ([R-P0-08] "class split"); this
closure states its mobile side at implementable precision. Arguments:
definition, self identity (0 from every placement caller — census in §6),
packed cell pair (X in the low half, Z in the high half), and a mode.

**Established — bounds first, mode-dependent verdict.** In order:

1. `cellX < 0` → return `mode == 2`.
2. packed pair `< 0` as a signed 32-bit word (i.e. `cellZ < 0`) → return
   `mode == 2`.
3. `cellX + fx ≥ mapWidthCells` → return `mode == 2`.
4. `cellZ + fz ≥ mapHeightCells` → return `mode == 2`.

So an out-of-map rectangle passes only for an airborne mover and fails for
every other mode. The map's last column and last row of cells are never
enterable by a ground footprint (`cellX + fx − 1 ≤ width − 2`).

**Established — class dispatch.** A definition whose `bmcode` is 0 (the
building class; the same byte that selects the yard-map parse and sets flags
bit 29 at creation) is validated by the yard-map placement validator of
[R-P0-08] with self identity 0 and no profile, whatever the mode. Otherwise a
mode other than `1` returns **1 (legal) without scanning a cell**: airborne
units, and the load-only modes 0 and 3, are never blocked by occupancy,
features or terrain once inside the map.

**Established — the mode-1 scan.** Rows Z outer, columns X inner over the
`fz × fx` rectangle; the first failing test returns 0 and every test is
**per cell**, not aggregate:

1. *Feature.* The cell's feature word: `0xFFFF` → not blocking; a value below
   `0xFFFB` is a catalog ordinal: below the catalog count → the definition's
   flag-word bit 6 (`blocking`, [R-FEAT-01 §6]); at or above the count →
   blocking; `0xFFFE` (fringe) → hop to the anchor cell at
   `cell − (dz·mapWidthCells + dx)` cells, where `dz` and `dx` are the
   fringe cell's two offset bytes ([R-FEAT-01 §3]), and read that word: below
   `0xFFFB` → its `blocking` bit **without** the catalog-count check (an
   out-of-range anchored ordinal reads past the catalog — a fault contract);
   otherwise not blocking; `0xFFFB`, `0xFFFC`, `0xFFFD` → blocking.
2. *Occupant.* Blocking feature, **or** ground word nonzero and not equal to
   the self identity → reject. Only the ground word is read; the air word is
   never consulted, so a landed or hovering airborne unit never blocks a
   ground mover through this test.
3. *Deep.* `hmin < seaLevel − MaxWaterDepth` → reject.
4. *Shallow.* `hmax > seaLevel − MinWaterDepth` → reject.
5. *Slope.* `slope = hmax − hmin`; when `slope > MaxSlope`: `hmin ≥ seaLevel`
   (a land cell) → reject; else `slope > MaxWaterSlope` → reject.

`hmax` and `hmin` are the cell's derived maximum (offset 5) and minimum
(offset 6) height bytes; `seaLevel` the map's sea-level byte; `MaxWaterDepth`
and `MinWaterDepth` the signed 16-bit copies and `MaxSlope`/`MaxWaterSlope`
the byte copies that the movement class writes into the definition
([02 "Movement class record"], [R-DOC04-A]). All comparisons are signed
32-bit and **strict** — equality passes. The validator reads the definition's
copies, not the movement-class record, and never reads the packed class layer
of §7.1, an owner, an allegiance, a velocity, a heading, an order, or the
occupant's age.

**Established — what a rejection means.** The returned 0 carries no reason;
the caller cannot tell a map edge from a wall from a parked friend. The
blocked branch of §1 is the same for all of them.

### Closed — the map edge, features and buildings [R-COLL-01 §3] (2026-08-29)

**Established — the map edge is a validator reject, not a clamp of its own.**
There is no edge test in the mover tick outside the validator. A ground mover
whose proposed rectangle leaves `[0, width−1−fx] × [0, height−1−fz]` is
rejected at §2 step 1–4 and receives the ordinary blocked response of §1: its
position is clamped inside the old rectangle (never more than `8 − 1/65536`
world units from the old centre on each axis), its speed capped at half
`MaxVelocity`, its heading untouched. It never bounces, stops its order, or
clears its route; the follower re-arms a path request every 60 ticks for as
long as it stays blocked ([R-MOV-01 §7]), and the order completes only through
its own goal handshake (§8.3). Because the reachable cell range excludes the
last row and column, the visible wall is one cell inside the map's last
attribute cell on the east and south edges and at cell 0 on the north and
west.

**Established — airborne units cross the edge.** An airborne mover (mode 2)
passes the validator out of map; the stamp of §4 then files the unit in the
*off-map sector bucket* and writes no cell, and the clear of §4 skips a unit
filed there. Nothing in the commit path bounds a flying unit's position; any
edge behaviour for aircraft belongs to the flight controller ([R-AIR-01]).

**Established — features.** A feature blocks a ground footprint exactly when
its definition's `blocking` bit is set, resolved through the fringe hop
above; presence alone does not block (metal patches are authored
`blocking=0`, [R-P0-08]). A blocking feature is a permanent wall to the
commit — the mover does not reclaim, crush, or path over it — until the
feature phase removes or replaces it ([R-FEAT-01 §4][R-FEAT-01 §5]).

**Established — buildings.** A finished building-class unit occupies the
ground word of the cells its yard map selects (§4), so a ground mover treats
those cells exactly like cells held by another mobile unit: rejected at §2
step 2 unless the occupant identity is its own. Cells the yard map does not
select in the building's current yard state are free to the commit even
though they lie inside the building's rectangle — this is how a product
leaves a factory whose yard is open (RWU-04-10 owns the exit contract). The
class layer of §7.1 additionally hard-blocks a building's cells for path
search through the occupant-age gate ([R-PATH-01 §2]); the commit does not
use the class layer.

### Closed — stamp and clear, exactly: the two planes, the overlap bits, and the sector list [R-COLL-01 §4] (2026-08-29)

**Established — the two occupancy planes and the three stamp classes.** The
ground word is written by ground movers (mode 1) and by building-class units
(flags bit 29, set at creation from `bmcode == 0`); the air word by airborne
movers (mode 2). Modes 0 and 3 stamp and clear nothing. The building class
selects cells by yard byte: with the unit's *yard-open* bit (bit 2 of the
second state byte, port 18 of §4.7) set, a cell is selected when its compiled
yard byte has **bit 1**; with it clear, when the byte has **bit 2**. Against
the table of [R-P0-08]: `o`, `f`, `w`, `G` are selected in both states, `c`
and `C` only while the yard is **closed**, `O` only while it is **open**, and
`Y`, `y`, `.` never. Additionally every selected-or-not cell whose yard byte
has bit 0 gets bit 1 of the cell's flag byte (offset 12) set on stamp and
cleared on clear — the "structure yard" mark the placement validator's bit-0
test reads ([R-P0-08]).

**Established — the overlap protocol.** Stamping a selected cell that already
holds another identity does **not** fail; it records an overlap on both
units' flags words using bits 26 (*host*: another unit overlaps my cell) and
27 (*intruder*: I overlap a cell I do not hold):

```text
occupant := unit at the cell's word
if occupant's owner is active and in player state 3:
        occupant.flags |= intruder;  self.flags |= host;  cell.word := self
else:
        occupant.flags |= host;      self.flags |= intruder;  (cell keeps occupant)
```

A free cell simply takes the self identity. The protocol is identical for the
ground and air planes and for the building class. Player state 3 is the
eliminated/watch state (Supported inference for the label, [R-MOV-01 §3]);
the arithmetic — a state-3 owner's unit yields its cell to whoever stamps
over it — is Established. This protocol is the **only** place two units'
ownership of one cell is arbitrated, and it never runs for a validated ground
proposal because the validator has already rejected any foreign occupant; it
runs for stamps that bypass validation (creation, transport drop and unload,
`Teleport`, the network unit-state apply, load) and for building stamps.

**Established — clear, in order.** If the unit is filed in the off-map bucket
nothing was stamped and the cell loop is skipped. Otherwise, over the
rectangle at the *cached* cell pair: building class — ground word equal to
self → 0, yard bit 0 → clear the cell's flag-byte bit 1, then recompute the
derived min/max heights over the rectangle grown by one cell on every side
(the loader's derivation, [03 §1]); mode 1 — ground word equal to self → 0;
mode 2 — air word equal to self → 0. Then flags bit 27 is cleared, and if
bit 26 was set both 26 and 27 are cleared and the *overlap scan* runs: every
live unit (and every unit on a live unit's cargo list) filed in a sector
bucket touching the rectangle whose own rectangle intersects it is passed to
the *restamp* below. Finally the class-layer maintenance: for a unit with a
mover, each of the sixteen class-layer records whose watermark exceeds the
mover's *last-stamp tick* reclassifies the rectangle ([R-PATH-01 §2]'s
footprint-aware classifier over the rectangle), and the last-stamp tick is
set to the current tick; for a unit without a mover every active layer
reclassifies it.

**Established — stamp, in order.** The mover's last-stamp tick (the
occupant-age clock of [R-DOC04-B]) is set to the current tick. Then the
bounds test of §2 steps 1–4 on the cached pair: out of map → the unit is
moved to the off-map sector bucket (unlinked from its previous bucket unless
carried) and **no cell is written**. In map → the sector bucket is
`(Z >> 23)·sectorsWide + (X >> 23)` from the committed 16.16 position (128
world-unit sectors), relinked when it changed and the unit is not carried;
then the plane loop above with the overlap protocol; for the building class
the derived-height recompute over the grown rectangle and a reclassification
of the rectangle in every active class layer follow.

**Established — restamp.** Gated on flags bit 27: it clears the bit and
re-runs the stamp loop at the cached pair with the overlap protocol; for the
building class a cell the yard map no longer selects that holds the self
identity is released to 0. Its three callers: the overlap scan above (an
intruder re-claims cells its host just released), the **yard-open port write**
(after the admission gate of §4.7 passes: write the yard bit, set bit 27,
restamp, reclassify the rectangle in every layer — this is the moment a
factory's `c`/`C` cells are released and its `O` cells claimed), and the save
loader's post-load pass.

**Established — writer census of the stamp and clear.** Stamp: unit creation
([04 §2.3]), the commit success branch, the carried-position setter (used by
the commit's carried branch and by `Teleport`, [R-SPEC-01 §2]), the network
unit-state apply and the ally-transfer re-creation (both out of scope, doc 08),
and the restamp. Clear: the commit success branch, the carried-position
setter, unit finalisation at death or free ([04 §6] / [04 §2.3]), and the
same two doc-08 paths. The carried-position setter is the commit's fast path
and success branch without the validator: same-cell-and-mode → write XYZ;
otherwise clear, write XYZ and the new pair and mode, stamp, LOS wrapper;
dirty in both cases. Every writer stamps at the unit's cached pair; there is
no reservation stamp for a proposed position anywhere.

### Closed — the blocked flag: writers, readers, persistence, and the save bit [R-COLL-01 §5] (2026-08-29)

**Established — three writers.** (1) The commit's validation gate (§1) on
every cross-cell or mode-changing proposal by a state-1/2 owner's unit —
the only simulation writer. (2) The follower's stream reader (doc 08's
network/save unit stream), which installs the bit from one stream bit. (3)
The mover box `u%04xmob` loader, which merges the byte's mode and blocked
bits into the live state byte ([08 "Save-file organization"]). Nothing
clears the flag on route install, order completion, arrival, or the
stationary early return.

**Established — four readers.** (1) The follower's per-tick service: blocked
→ *wants-repath* ([R-MOV-01 §7]). (2) The movement-rate classifier: blocked →
tier 0 → `StopMoving` ([R-MOV-01 §6]). (3) The commit's own blocked branch
(§1) when the validator did not run. (4) The follower's stream writer, which
copies the bit into the stream and mirrors it into follower flags bit 2. The
steering step, the hover bob, the order pumps and the path search do not read
it — the hover bob reads the last-proposal tick, not the flag (correction to
this section's tail, §7).

**Established — the flag persists stale by construction.** After a rejection
the clamp leaves the unit inside its old rectangle with capped speed. A later
proposal that stays inside that rectangle (the unit turned toward a new route
point, or slowed) takes the fast path or the stationary return, neither of
which rewrites the bit. The unit is then *reported* blocked — it arms a path
request every 60 ticks and its movement-rate tier reads 0, so `StopMoving`
fires though it is moving within its cell — until its next cross-cell
proposal, when the validator's verdict replaces the stale value. The speed
cap is not re-applied on stale ticks (it lives only in the fresh-rejection
branch), so a stale-blocked unit accelerates normally. No observation is
needed to establish this; what a retail observation could add is the
visible symptom (a unit reporting a fresh path request while circling inside
one cell), which is *Observed*-grade only and changes no contract.

**Established — the save bit.** The mover box's final byte carries mode in
bits 0–1 and the blocked flag in bit 2; the box's one unnamed 32-bit word
(the box is 35 bytes — velocity ×3, lean ×3, speed, 16-bit turn residual,
this word, flag byte [08 R-SAVE-02]; the earlier "second unnamed word"
wording assumed a seven-field layout that does not exist) is the mover's
**last-stamp tick** (the occupant-age clock), and the
last-proposal tick is **not** saved — after load the hover bob's age term
starts from whatever the load path stamps. (Naming for doc 08; RWU-08-4.)

### Closed — the `I can't get there` bit is the empty-route publication [R-COLL-01 §6] (2026-08-29)

The RWU-04-2 open item — "the move and mobile-build rejects test the same
flag bit; who sets it?" — is closed by two closures that landed after it was
raised: the bit is the order record's satisfied-word bit `0x40`, raised by the
route publication when an **empty route** is published while the unit is not
at the goal ([R-PATH-01 §7][R-PATH-01 §9]); the move handlers read it as
"cannot get there" and the work handlers as abandon or re-arm
([R-ORD-01 §4][R-ORD-01 §5]). It is not produced by the commit, the
validator, or the blocked flag: a mover walled in by traffic never raises it
unless the path search itself finds no route. `I can't get there` is caption
slot 7; the placement rejects that print `Waiting for target area to clear`
are the validator's 0 with self identity 0 at the mobile-build and unload
sites, on their own retry counter ([R-ORD-01 §5], doc 08 "General parameter
3").

### Closed — yield, sidestep and retarget: the outer owner does not exist [R-COLL-01 §7] (2026-08-29)

[R-MOV-02A] left "any outer yield owner, retarget, sidestep, reverse, or
wait-queue" as a bounded negative awaiting a retail trace. It is closed here
by **census**, which is stronger than the call-graph bound: the mover state
byte's bit 2 has exactly the three writers of §5; the ground/air planes have
exactly the writers of §4; the validator has exactly eleven callers (the
commit, the carried drop check, the commander respawn placement, the AI
extractor placement, `BuildingBuild`, the two `MobileBuild` fragments, the
two unload executors, and two further order fragments reached through the
handler jump tables — all placement or drop legality, none a movement
decision); and no function in the image compares two units' size, age, order
state, owner, or a random draw to choose which of two colliding movers moves.
Therefore:

* **Who yields: nobody.** Two ground movers contending for a cell are
  resolved by sweep order alone — player slot, then pool slot ([R-MOV-02A]) —
  and the loser receives the clamp and half-speed cap of §1 on that tick. The
  winner is not slowed. There is no size, mass, priority, allegiance or age
  term at the commit.
* **Who waits: whoever is blocked, for exactly as long as the cell stays
  held.** There is no wait counter; the blocked unit re-proposes every tick at
  capped speed and passes the moment the cell's ground word is cleared, in the
  same tick if the holder's slot is visited earlier.
* **Sidestep and retarget: only through a new route.** The single escape is
  the follower's repath request (at most one per 60 ticks while blocked,
  [R-MOV-01 §7]). Whether the new route avoids the blocker is decided by the
  request's revision pass ([R-DOC04-B][R-PATH-01 §2]): an occupant whose
  last-stamp tick is older than the watermark (more than 30 ticks without a
  successful commit) is re-stamped into the class layer and the search routes
  around it; a younger occupant is transparent to the search and the new
  route can run straight through it, to be rejected again at commit.
* **Deadlock resolution is emergent, not owned.** Two movers blocked head-on
  both stop stamping, so after 30 ticks each is a hard block in the other's
  next search; each re-requests at the 60-tick cadence and receives a route
  around the other. The 30-tick age and the 60-tick cadence are Established
  separately; that their combination is what a player sees as the "shuffle
  apart" is a **Supported inference** — a retail observation of two units
  ordered through each other in a one-cell corridor, timing the first
  divergent route against the collision tick, would confirm it (expected:
  neither moves for at least 30 ticks; the first new route arrives between
  tick 30 and tick 60 after the block for the unit whose request fires later).
* **Reverse, push, rotate for clearance, swap: none.** Unchanged from
  [R-MOV-02A]; the census above turns the bounded negative into a whole-image
  negative for the simulation. Interface and network paths that write a
  position (the network unit-state apply) do so by clear-and-stamp without a
  validator and are out of scope.

### Correction — four earlier statements [R-COLL-01 §8] (2026-08-29)

1. **This section's fourth paragraph** said the validator's "aggregate
   height, depth, and slope gates run after the scan". Wrong: in the mobile
   validator every gate is per cell inside the scan, in the order feature →
   occupant → deep → shallow → slope (§2). The aggregate form (`min of mins`,
   `max of maxes`) belongs to the class-layer classifier ([R-DOC04-B]) and to
   the yard-map placement validator ([R-P0-08]), which the earlier text
   conflated with the commit path.
2. **[R-MOV-01 §1]** listed "the tick of its last committed position" among
   the mover's state. The word is written before validation and on blocked
   ticks (§1); it is the last-*proposal* tick. The last-*stamp* tick is a
   different mover word, the one the occupant-age gate and the save box
   carry (§4, §5).
3. **This section's tail** said the blocked flag "gates the movement-rate
   tier, the hover bob, and the repath arm". The hover bob reads the
   last-proposal tick, not the flag (§5); the other two readers stand.
4. **[R-P0-08-A §1]** inferred that "finished buildings never write" the
   mobile occupancy words. Falsified by §4: a building-class unit writes the
   ground word of every cell its yard map selects in its current yard state,
   and the yard-open write restamps. The consequence the inference was
   reaching for — that stock factories can produce although their exit
   rectangle overlaps their body — comes instead from the yard-state
   selection (`c`/`C` released while open) and from the exit validator's
   arguments, which RWU-04-10 owns. Its `TODO(question)` on the exit
   caller's mode argument is narrowed here: `BuildingBuild` passes the
   **producer's own flags-word mode-mirror bits** and self identity 0, and a
   mirror other than 1 makes the mobile validator pass any in-map rectangle
   without a cell scan (§2); what the mirror holds for a building-class
   producer (the allocator's mode argument at its creation) is RWU-04-10's
   question. Also, [R-ORD-01 §1]'s aside calling the committed cell pair
   "the packed half-cell footprint bias of §8" mislabels it: the bias in
   §8.2's quantisation is the size pair; the committed pair is the anchor.

### R-COLL-01 §9 — what this unit leaves open

* Whether a building-class unit can own a mover and so reach the commit's
  `bmcode == 0` dispatch (§2) in play; the branch is Established, its
  reachability is **Unknown** — decider: static census of the allocator's
  mover-allocation condition (RWU-04-10 / lane 04).
* The airborne edge: no commit-path bound exists (§3); whether the flight
  controller bounds position is [R-AIR-01]'s question, not this section's.
* The emergent deadlock timing is a Supported inference (§7); decider: the
  retail observation specified there.

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

**Correction — this family is the flight vertical clamp [R-MOV-01 §3]
(2026-08-28).** The paragraph above attributes the threshold to "the ground
mover ... for steering and braking". It is the **flight** integrator's
per-tick vertical velocity limit, stated in the same arithmetic by section
10.1 (`yLimit = 65536 if (speed & ~3) < 262144 else speed >> 2`, with the
strict middle branch `dy < yLimit`). The ground steering step contains no Y
term at all and the ground speed update writes vertical velocity as a literal
zero every tick, so there is nothing for a vertical threshold to compare. The
blocked-movement half of the sentence is correct and unaffected; and the
"without a replan" clause is superseded by [R-MOV-01 §7].

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

### Correction — mover modes are grounded and airborne, not stopped and moving [R-MOV-01 §8] (2026-08-28)

**What the earlier text said.** The first paragraph of this section reads
"`1` is stopped/parked (the helper that writes `1` zeroes velocity and runs
lean decay) and `2` is active locomotion (the flight integrator runs only for
`2` and the pickup validator rejects candidates whose mode is `2`)". Section
10.2's transport-admission reject 6 repeats the reading as "candidate committed
mover mode is active locomotion (mode 2, moving) — moving cargo is rejected".

**Why it was wrong.** The observations behind that reading are all correct —
writing `1` does zero velocity and run the lean decay, the flight integrator
does require `2`, and the pickup validator does reject `2` — but the label was
inferred from those side effects rather than from the producers. A census of
every runtime writer settles it:

* The mover constructor writes mode `1`. Every unit, ground or air, starts at
  `1`.
* The only runtime mode writer is one two-valued setter, and its only callers
  are the **air** order executors, which pass `2` on takeoff (gated on the
  current mode being `1`) and `1` on landing. No ground order handler writes a
  mode at all.
* Writing `2` raises the unit's activation bit and emits `Activate`; writing
  `1` clears it and emits `Deactivate`, after zeroing velocity and speed and
  running one zero-delta lean-decay step.

**The corrected contract — Established.** Mode `1` is **on the ground or on the
surface** and mode `2` is **airborne**. A ground unit is in mode `1` for its
entire life, moving or not; a `canfly` unit is in mode `1` while landed and `2`
while flying. This is what makes the rest of the machinery coherent: the
post-move Y/orientation correction of `[R-MOV-01 §5]` runs only for mode `1`,
so it owns the height of everything that is not flying and never fights the
flight integrator; the flight integrator zeroes all velocity for any mode but
`2`, which is how a landed aircraft sits still on its pad; and the transport
pickup rejects mode `2` because it will not pick up an **airborne** candidate,
not because it will not pick up a moving one. Section 10.2's reject 6 should be
read as "the candidate is airborne" (owner: section 10.2).

The band classifier below is unaffected in arithmetic: its `mode not in {1,2}`
guard means "not grounded and not airborne", which in practice means only the
save-installed modes `0` and `3`. Those two remain producerless in ordinary
gameplay — the constructor cannot write them and the setter is two-valued — and
reach a unit only through a save file, exactly as this section already states.

### Closed — where the band classifier sits, and hover locomotion [R-MOV-01 §8a] (2026-08-28)

The "Hover and floater units reuse the ground integrator" paragraph below is
confirmed and can be made exact. A `canhover` or `floater` unit takes the same
mover tick, the same follower, the same heading clamp, the same
accelerate-versus-brake decision and the same footprint validator as a tank;
the three places its medium changes anything are:

1. the water half-speed branch of the speed update, which is skipped when
   either `canhover` (bit 12) or `floater` (bit 19) is set
   (`definition flags & 0x81000`, `[R-MOV-01 §4]`);
2. the post-move Y branch it selects (`[R-MOV-01 §5]`, and `[R-MOV-01 §9]`
   below); and
3. for `canhover` only, the per-corner bob and the sea-level floor inside the
   four-corner terrain conform, plus the fact that `canhover` forces that
   correction to run every tick rather than only on a dirty transform.

**Sea-floor behaviour — Established, closing part of this section's Unknown.**
There is no separate sea-floor follower. A mover that is neither `upright` nor
`floater` gets the four-corner terrain conform unconditionally, and that conform
never consults sea level except inside the `canhover` bob. So a submerged
non-hover, non-floater mover simply follows the terrain — the sea floor — with
its integer height word taken from the average of its four contact-quad corner
heights, and it is that height word which puts it in band `2` or `3` and which
triggers the water half-speed branch and the water damage of section 9.2.
`canhover` raises the same conform's floor to sea level, so a hovercraft rides
the surface over water and the terrain over land in one expression.

### 9.2 Flags and damage

**Established fact:** Definition bits and fields participate as: `canhover` is bit 12, `floater` is bit 19, `upright` is bit 20, `amphibious` is bit 21, `hoverattack` is bit 27. `canhover` excludes the unit from water-damage and participates in the stopped-state Y pre-gate; `floater` selects the ship surface clamp at `waterline + sea level`; `upright` keeps the model vertical and computes Y as `max(terrain, sea level minus waterline)`; `hoverattack` selects the gunship attack variant, not hover locomotion. The `amphibious` bit is parsed and stored but has no reader in the bounded recovered movement, medium, targeting, or transport code — parser-only in that bound (bounded absence, not whole-executable impossibility). Shipped amphibious-looking behavior compensates via movement class `TANKHOVER3` values (movement class MaxSlope 12, MaxWaterSlope 255, no depth limits) and the `canhover`/`upright`/`waterline`/`modelBottom`/`movementclass` fields, not via the amphibious bit. Other medium fields are `waterline` byte (draft for band `2`), `movementclass` name resolved to profile, and `modelBottom` signed word (threshold for band `3`).

**Correction (2026-08-29, [R-SPEC-01 §15]).** The "no reader" sentence above is
too broad: `amphibious` is read by the repair-admission water clause
([R-ORD-01 §7]) and by a dead placement-validator variant; the movement,
medium and transport bound stands.

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

**Reconciliation with document 03 (2026-08-26; withdrawn 2026-08-29):**
this paragraph previously reconciled the script-emitted `emit-sfx` wake
effects with "engine-side wake rectangles produced from mover bounds" that
document 03 §5.7 described. [03 R-WATER-01 §1] has since established that
no such rectangle exists — the pass in question draws the selected unit's
footprint quad and reads neither mover bounds nor sea level. The
script-emitted `emit-sfx` wake effects (types 2 through 5, spawned by the
shipped hover scripts via the band classifier) are therefore the **only** wake
mechanism, and this document owns it in full; document 03 owns their
drawing and survival ([03 R-FX-01 §3], [03 R-WATER-01 §1]).

**Unknown:** Complete wake and SFX-piece mapping for every band transition
(the script-emitted wake effects are established as spawner-driven in
section 5.2; the presentation-side wake rectangles of document 03 section 5.7
are that document's artifact — see the reconciliation note above),
sea-floor following, and wake rectangle interpolation beyond the band
arithmetic; the semantic label of mover modes `0` and `3`.

### Correction — the floater and upright height rules [R-MOV-01 §9] (2026-08-28)

**What the earlier text said.** The first paragraph of this section reads
"`floater` selects the ship surface clamp at `waterline + sea level`; `upright`
keeps the model vertical and computes Y as `max(terrain, sea level minus
waterline)`".

**Why it was wrong.** The `floater` expression has its sign inverted, and the
`upright` expression is stated without the gate that actually selects it. The
executable computes the floater surface through a wrapping 32-bit identity
(`waterline` multiplied by an all-ones 16-bit constant, plus sea level, then
scaled by 65,536) which is algebraically `(seaLevel - waterline) * 65536`, not
`(seaLevel + waterline) * 65536`. Read as authored, `waterline` is a **draft**:
a hull with a larger waterline sits lower, which is also the reading the band
classifier below already uses when it tests `waterline + wy == wt` for band `2`.
The `waterline + sea level` form makes a deeper-drafted hull sit **higher**, and
contradicts this section's own band arithmetic.

**The corrected contract — Established.** In the post-move correction of
`[R-MOV-01 §5]`, exactly one of four branches runs, tested in this order:

```text
if upright (bit 20):
        if not canhover (bit 12):  Y = terrainHeight(unitXZ) * 65536
        else:                      Y = max(terrainHeight(unitXZ), seaLevel - waterline) * 65536
else if floater (bit 19):          Y = (seaLevel - waterline) * 65536
else:                              four-corner terrain conform  [R-MOV-01 §5]
```

`seaLevel` and `waterline` are the map header byte and the definition byte, and
the subtraction is done on their zero-extended values; the comparison inside the
`max` is `seaLevel - waterline < terrain ? terrain : seaLevel - waterline`, so
equality takes the sea-surface value (identical either way). The first three
branches write the full 16.16 Y with a zero fraction; the fourth writes only the
high word.

Two consequences the earlier text did not state:

* **`upright` alone is a plain terrain snap.** The `max(terrain, seaLevel -
  waterline)` form is reached only when the definition is `upright` **and**
  `canhover`. An `upright` unit without `canhover` — every stock KBot — is
  snapped straight onto the terrain height with no water term, so it walks along
  the sea floor rather than floating.
* **`upright` and `floater` units never receive a terrain pitch or roll.** Only
  the fourth branch writes the unit's pitch and roll words. A KBot's or a ship's
  pitch word therefore stays at its initial zero unless flight or a carrier
  writes it, and the pitch-table speed cap of section 8.1 consequently always
  reads index `0` and permits 100 percent of `MaxVelocity` for them. The slope
  speed penalty is a vehicle-only effect.

The rest of this section's flag census is unchanged and confirmed: `canhover` is
bit 12, `floater` bit 19, `upright` bit 20, `amphibious` bit 21, `hoverattack`
bit 27; `canhover` and `floater` together form the `0x81000` mask that exempts a
mover from the below-water half-speed branch; `amphibious` still has no reader
in the recovered movement, medium, targeting or transport code, and the bounded
absence now also covers the post-move correction and the whole mover tick.

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

### Closed — the flight command block and its per-tick producer [R-AIR-01 §1] (2026-08-29)

**Established — a mover owns one polymorphic motion controller, chosen once at
construction.** The mover object created for every unit begins by zeroing its
three velocity components, its scalar speed word, its 16-bit turn residual, its
tick stamp and its three-component **lean accumulator**, sets its committed mode
to `1` (grounded), caches the definition's compiled model reference, and
then allocates exactly one controller object from a two-by-two choice:

| Owner's player-slot state byte | `canfly` | Controller |
|---|---|---|
| not `3` | clear | ground route follower (101 bytes) |
| not `3` | set | **flight command block** (40 bytes) |
| `3` | clear | reduced ground controller (28 bytes) |
| `3` | set | reduced flight command block (39 bytes) |

[R-MOV-01 §3] names the same two-way typing for the ground side; the four sizes
and the flight variants are added here. Allocation failure leaves the controller
pointer null and the mover inert; there is no retry. **Supported inference:**
player state `3` is the eliminated/defeated watch state (the label's evidence is
in [R-MOV-01 §3]'s compact-ground-controller closure; the sweep skip itself is
direct), so its reduced controllers exist only to keep saved state loadable —
what would settle the label is the player-phase state machine's own writer
census (lane 08).

**Established — the mover tick is five calls and the flight branch is the
second.** Per unit per tick, in order: the controller's per-tick hook; then the
ground steering integrator when the definition's `canfly` bit is clear, or the
flight integrator when it is set; then the occupancy/carried-cargo commit; then
the movement-rate cache; then the per-tick wrapper. The flight integrator never
reads an order record: it reads only the flight command block.

**Established — the flight command block.** It holds a reference to the order
record's current **goal payload**, a reference to the unit, a **command
position** (three 16.16 components, initialized to the unit's spawn X/Y/Z), a
**command velocity** (three 16.16 components, initialized to zero), a **command
heading** (16-bit, initialized to the unit's spawn heading), and a flags byte
whose bit `0x01` is a "mover mode changed" dirty flag and whose bits `1..2`
mirror the last observed committed mover mode. The integrator's single input
fetch copies out the command position, the command velocity and the command
heading; nothing else crosses that boundary. This is the "order-layer
command-target supply for each non-construction air class" that §10.3's Unknown
list asked for: **there is exactly one supply, shared by every air order, and
the orders differ only in which goal payload they install.**

**Established — the per-tick command producer, exactly.** The controller's
per-tick hook first sets the dirty flag when the committed mover mode differs
from the mirrored copy, then runs the producer. With a null goal payload the
producer does nothing at all — the command block keeps its last values, so an
aircraft whose payload was released continues on its last command. Otherwise,
in this order:

1. Save the old command position. Call the payload's **goal-update** method with
   the command position as the destination; it overwrites the command position
   (and may decline, leaving it unchanged — see [R-AIR-01 §4]).
2. Set the command velocity to the **componentwise difference** new − old. The
   command velocity is therefore a pure consequence of how fast the goal itself
   moved this tick; nothing else writes it.
3. Compute `d = trunc(hypot(unitX − commandX, unitZ − commandZ))` in 16.16 —
   the double-precision `hypot` of the two raw fixed-point differences, then
   `__ftol`.
4. **If `d > 0xA00000` (160 world units)** overwrite the command **Y**:
   `commandY = (cruisealt + sectorHeight) << 16` for an ordinary definition,
   or `commandY = (seaLevelByte + cruisealt) << 16` when the definition's flag
   bit `22` is set. `sectorHeight` is the byte the air sector grid holds for
   the sector the unit is currently linked into ([R-AIR-01 §5]) — **not** the
   four-corner terrain query. This is the rule that makes a long-haul aircraft
   ride a constant clearance over hills — a consequence of the arithmetic, not a
   separate contract: the height it clears is the maximum terrain height of a
   3×3 block of 128-world-unit sectors around it, refreshed every tick, and it
   stops being refreshed inside 160 world units of the goal so the final
   approach can descend.
   Definition flag bit `22` has **no writer anywhere in the recovered function
   set**: a bounded negative over all 4,024 exported functions, where the FBI
   parser writes bits 0–15, 17–21 and 24–30 of that word, the definition
   post-load pass writes bit 23 (the content-version gate) and nothing else, and
   the definition copy constructor copies bit by bit. Bounded absence is not
   universal absence, but on this evidence the sea-level variant is unreachable
   with any authored content and the sector-height variant is the operative
   one.
5. **Heading.** If `d > 0x1400000` (320 world units), or the payload's
   heading-supply method returns zero **and** `d > 0x100000` (16 world units),
   set `commandHeading = bearing(unitPos, commandPos)`. Otherwise the payload's
   suggestion (which that method has already written in place) stands, and
   inside 16 world units with no suggestion the command heading is left
   completely unchanged. Note the ordering: at more than 320 world units the
   payload is not consulted at all.
6. Call the payload's **arrival** test with the unit. If it reports arrival, OR
   the satisfied bit `0x20` into the owning order record's pending word, then
   ask the payload whether it is persistent; if it is not, release it — which
   also ORs the satisfied bit `0x80` into that record's pending word and leaves
   the command block with a null payload.

`bearing(a, b)` throughout section 10 is the shared helper
`round(atan2(aX − bX, aZ − bZ) · 65536 / 2π)`, computed on the x87 stack with
`fpatan` and stored with the retail control word's round-to-nearest. Its
argument order is (self, other) at every air call site; this specification does
not assert which on-screen direction that faces, only the expression, because
the model loader's coordinate negation ([R-REV-02]) is applied to model data and
not to these world coordinates.

**Correction — cruise altitude has two producers, not one.** The §10.1
paragraph above says "Cruise altitude for point and follow commands is
`targetY = (max(sea level, terrain height at target XZ) + signed offset) ×
65536` … where terrain height is the bilinear four-corner query". That
expression is correct for **one** producer: the path marker's altitude setter,
which runs **once, when the marker is built**, and only when the marker carries
the terrain-derived-altitude flag. The second producer is the per-tick rule of
step 4 above, which runs every tick while the aircraft is more than 160 world
units from its goal, uses the **air sector grid's** neighbourhood maximum rather
than the bilinear query, and samples at the **unit's** position rather than the
goal's. The `0x1FF0000` ceiling (about 511 world units) is applied by the
marker's setter and by the marker's goal update, but **not** by the per-tick
rule of step 4, which can therefore command a higher Y than the marker ever
would on a map whose sector height plus `cruisealt` exceeds 511.

### Closed — bank and pitch: the lean accumulator, exactly [R-AIR-01 §2] (2026-08-29)

**Established.** Bank and pitch are computed by one routine, called from the end
of the flight integrator with the tick's velocity delta and from the mover-mode
setter with a zero delta. It maintains a persistent three-component **lean
accumulator** on the mover and writes the unit's two visual angle words:

```
lean.x = (lean.x * 0xF333) >> 16          // 64-bit signed product, arithmetic shift
lean.y = (lean.y * 0xF333) >> 16
lean.z = (lean.z * 0xF333) >> 16
lean  += (dvx, dvy, dvz)                  // this tick's velocity delta, 16.16
(px, pz) = rotate(lean.x, lean.z) by the unit's heading    // identity when heading == 0
L = (gravity << 16) / 0xCCD               // 64-bit signed divide
bank  = round( atan2( (bankscale  * (-px)) >> 16, L ) * 65536 / 2pi )
pitch = round( atan2( (pitchscale * (-pz)) >> 16, L ) * 65536 / 2pi )
```

with `0xF333 = 62259`, i.e. a per-tick decay of `62259/65536` (0.9500 truncated
from `0.95 × 65536 = 62259.2`); `0xCCD = 3277`; `gravity` the runtime word the
map loader fills from the OTA `gravity` key (default `0x1FDB` = 8155);
`bankscale` and `pitchscale` the 16.16 definition words, defaulting to
**`0x10000` (1.0)** and **`0` (0.0)** respectively; and the rotation the shared
`fsincos`-based coordinate-pair rotation, whose two results are stored with
`fistp` under the retail control word (round-to-nearest), leaving the pair
unchanged when the heading is exactly zero. The `>> 16` on each scaled term is
an arithmetic shift of the 64-bit product, and `atan2` is the x87 `fpatan` with
the scaled term as the numerator and `L` as the denominator; the result is
rounded to nearest and stored as a signed 16-bit angle.

`pitchscale`'s default of zero means a definition that authors neither key
banks with unit gain and does not pitch at all. `bankscale` and `pitchscale`
have no other reader.

**Established — bank and pitch are authoritative, not presentation-only.** The
two words feed the piece-angle triple that the occupancy commit builds for every
unit each tick, combining the unit's bank, heading and pitch with the selected
piece's own three angle words. That triple is the frame in which the piece world
transform is evaluated, which is what the transport attach geometry, the landing
pad follow markers and the muzzle/attach piece queries all consume. A
reimplementation must therefore compute them in the simulation, in this order,
with these truncations — they are not a renderer concern.

**Established — the levelling call.** The mover-mode setter calls the same
routine with a zero delta vector when a unit becomes grounded, so bank and pitch
decay by the `0xF333` factor exactly once and are then recomputed from the
decayed accumulator; they are not snapped to zero.

### Closed — mover modes: the setter's side effects, and where 0 and 3 come from [R-AIR-01 §3] (2026-08-29)

**Established — the mode setter.** One routine changes a committed mover mode,
and it is called only from the air order executors and from the reduced flight
controller's save-restore path. Given a requested mode it does nothing when the
current low two bits already equal the request. Otherwise:

* Requested mode `1` (grounded): zero the three velocity components, the scalar
  speed word and the lean-decay input; run the bank/pitch routine with a zero
  delta ([R-AIR-01 §2]); then **clear** bit `0x01` of the unit state byte, which
  raises the `Deactivate` COB callback and notification event `4` on the falling
  edge.
* Any other requested mode: **set** bit `0x01` of the unit state byte, which
  raises the `Activate` COB callback and notification event `3` on the rising
  edge.
* Then write the request's low two bits into the committed mode pair.

The 16-bit turn residual is **not** zeroed by the setter (only by the
integrator's own inactive-mode branch). `Activate`/`Deactivate` are therefore
the engine's takeoff and touchdown script hooks, and they are edge-triggered
through the shared unit-state edge machine of [R-UNIT-06 §2] — a definition that
is already "activated" for another reason sees no callback on takeoff.

**Established — the ordinary producer of mode `0`.** [R-MOV-01 §8] named modes
`1` and `2` and this doc's tail carried "mover modes `0` and `3` still reach a
unit only through a save file" as an open item. That is wrong for mode `0`: the
attachment helper writes the request's low two bits directly into the child's
committed mover-mode pair, and **every ordinary attach passes `0`** — the cargo
in `VTOL_Pickup` phase 4, and the aircraft itself when it parks on a landing pad
in `VTOL_Landing` phase 6. Mode `0` is therefore the **attached/parked** mode:
the integrator's inactive branch zeroes all three velocity components, the
scalar speed and the turn residual every tick and performs no flight update, and
the carried-unit branch of the occupancy commit drives the child's position from
the parent's piece transform instead. Mode `2` is passed on the takeoff
preamble's self-detach, and mode `1` on the `VTOL_Unload` release. Because that
write is direct, an attach or a detach never zeroes velocity through the setter,
never levels bank and pitch, and never raises `Activate`/`Deactivate`.

**Unknown:** mode `3` has no producer in the recovered function set other than
the 2-bit field the reduced flight controller reads out of the save/network
stream · §9.1 · static trace of that stream's writer (RWU-08-4).

### Closed — the air path marker: fields, flags, and the four methods that matter [R-AIR-01 §4] (2026-08-29)

**Established — the marker is the air half of the goal-payload class family.**
Ground orders install a 20-byte goal handle whose arrival test is the tile
threshold of section 8.3; air orders install a `0x36`-byte **path marker** whose
arrival test is the horizontal `hypot` of section 10.1; a third, `0x2C`-byte
**velocity marker** exists for `AirToAir` ([R-AIR-01 §8]). All three derive from
one base whose virtual interface is the same six operations a motion controller
calls: goal update, arrival, heading supply, persistence, release and
serialization. The payload installer clears the record's satisfied bits
`0x20`, `0x40`, `0x80`, `0x100` and `0x200` whenever it installs a non-null
payload, and raises `0x80` on the record whose payload it replaces.

A marker carries: a 16-bit **flags** word; a horizontal **arrival radius** word;
a signed 16-bit **altitude offset**; a 16-bit **heading**; a 16-bit **attach
piece index**; the owning unit; a weak **target handle**; a 16.16 **goal**
triple; and a 16.16 **radial offset** distance. The flag bits are:

| Bit | Meaning |
|---:|---|
| `0x01` | follow the target unit |
| `0x02` | offset the goal radially about the target's heading |
| `0x04` | arrival additionally requires the unit's heading to equal the target's |
| `0x08` | an explicit altitude offset is present |
| `0x10` | an explicit horizontal arrival radius is present |
| `0x20` | the goal Y is terrain-derived |
| `0x40` | an explicit heading is present |
| `0x80` | freeze: skip the follow branch of the goal update |

The five constructors used by the air executors are: **point** (flags `0x20`,
goal = a supplied triple); **follow-unit** (flags `0x01`, or `0x07` with the
radial offset set to the unit's first weapon slot's `Range` in 16.16 — or
`0x640000`, 100 world units, when that `Range` is zero — whenever the **target**
is `canfly`); **follow-unit-piece** (flags `0x05`, with the piece index);
**frozen terrain point** (flags `0xA3`, goal = a supplied triple); and the
save/network reconstructor.

**Established — goal update.** With flag `0x01` clear **or** flag `0x80` set,
and only when flag `0x08` (explicit altitude offset) is clear, the marker
rewrites its goal Y by the same sector-height rule the per-tick producer uses
([R-AIR-01 §1] step 4). Otherwise, with a live follow: if the target is dead or
the target's sector link is the out-of-map sentinel, the update **declines** and
the command position is left at last tick's value; else the goal triple becomes
the target's attach-piece world position (the exit-piece locator transform of
[R-REV-02], including the unit-origin addition), plus — when flag `0x02` is set
— a radial offset of the marker's radial distance at the target's heading, or at
the target's heading plus the marker's own heading when flag `0x40` is also set;
and then the altitude offset, shifted into 16.16, is added to the goal Y. The
result is clamped to `0x1FF0000` in both branches.

**Established — arrival.** In the **explicit-radius** case (flag `0x10`) the
test is strict and horizontal only: arrived iff
`hypot(unitX − goalX, unitZ − goalZ) / 65536 < (int16)arrivalRadius`, evaluated
in double precision on the raw fixed-point differences. Otherwise the default
test is `hypot(...) / 65536 <= 0.5` (half a world unit), and then, in order:
flag `0x01` additionally requires a live target; flag `0x04` additionally
requires the unit's heading to equal the target's heading exactly; flag `0x08`
additionally requires `|unitY − goalY| < 0x10001`.

**Correction — the arrival radius is not a small enumerated family.** §10.1
above says "explicit air arrivals test `hypot(dx,dz) < radius` with radii 48,
128, or 320 world units depending on order (flagged via the arrival-radius
field)" and then adds "The radius-flag family now includes a fourth value, 336".
There is no family. The radius is a **plain 16-bit word each executor leg writes
for the leg it is starting**, and the values observed across the air executors
are `0x10`, `0x30`, `0x40`, `0x80`, `0xA0`, `0x140`, `0x150`, `0x1E0`, `0x3C0`,
`0x80 + random below 0x80`, the unit's first weapon slot's `Range`, and the
computed bomb-release radius `lead + 1 + attackrunlength` of [R-AIR-01 §8]. A
reimplementation must treat it as a per-leg quantity, not as a lookup.

**Established — heading supply and persistence.** The heading method writes
into the destination and returns 1 in four cases, tested in order: with neither
flag `0x04` nor flag `0x80` set, or with no live target, it writes the marker's
own heading and returns 1 if flag `0x40` is set, and otherwise returns 0
(no suggestion); with a live target, flag `0x02` writes `bearing(unitPos,
targetPos)`; else flag `0x40` writes the marker's own heading; else it writes
the target's heading. The persistence method returns 1 exactly when flag `0x01`
is set **and** the target is live — so a follow marker survives arrival and a
point marker is released on arrival.

**Established — the terrain-derived altitude setter.** Setting an altitude
offset always sets flag `0x08` and stores the signed word. If flag `0x20` is
already set it also computes the goal Y immediately as
`(max(seaLevelByte, terrainHeight(goalXZ)) + offset) << 16`, clamped to
`0x1FF0000`, where `terrainHeight` is the bilinear four-corner query over the
13-byte attribute cells (cell side 16 world units, the two fractional parts
taken as sixteenths and each of the three interpolation steps divided by 16 with
the sign-corrected shift `(v + (v >> 31 & 15)) >> 4`), returning `-1` when the
sample is out of bounds. Setting a horizontal arrival radius sets flag `0x10`
and stores the word.

### Closed — the air sector grid and the vertical-bypass sentinel [R-AIR-01 §5] (2026-08-29)

**Established — the grid.** At map load, after the terrain is decoded, the
engine builds a second, coarse grid whose cell is **8 attribute cells on a side,
that is 128 × 128 world units**. Its column and row counts are the map's world
extents rounded **up** to whole 128-unit cells, and the record count is rounded
up again to a multiple of 8. Each record is 10 bytes: a per-cell maximum terrain
height byte; a second, smoothed maximum byte; a 32-bit **edge flag** word; and
the head of a singly-linked list of the units currently inside that cell, which
removal walks from the front to find the predecessor. The
build is four passes:

1. Zero every record, then set edge bit `1` on the whole top row, `2` on the
   whole bottom row, `4` on the whole left column and `8` on the whole right
   column.
2. Initialize every record's first byte to the map's **sea-level byte**.
3. Sweep every attribute cell and raise the owning record's first byte to the
   cell's **derived per-cell maximum byte** where that is higher (corrected
   2026-08-29 against [03 R-TERR-01 §5]: the earlier text read "the cell's
   height byte", i.e. the raw sample; the sweep reads the loader-derived
   maximum).
4. Two separable maximum passes. The row pass writes each cell's second byte as
   the maximum of the first byte over that cell and its two horizontal
   neighbours; the column pass then rewrites the second byte in place as the
   maximum of the second byte over that cell and its two vertical neighbours
   (reading ahead before writing, so the in-place rewrite does not alias). At
   the first and last cell of a row or column only two cells participate, not
   three. The second byte is therefore the **maximum terrain height over the
   3 × 3 block of 128-unit cells centred on this one**, truncated at the map
   edges, floored at sea level.

That second byte is the `sectorHeight` the cruise-altitude rule of
[R-AIR-01 §1] reads.

**Established — the sentinel is the off-map sector.** Alongside the grid the
loader allocates **one extra 10-byte record**, stores its address in a global,
zeroes it and sets its edge-flag word to `0x1F` (all four edge bits plus bit 4).
It is freed and the global nulled when the map is torn down. The occupancy
re-stamp links a unit into the ordinary record
`grid[(Z >> 23) * columns + (X >> 23)]` — the `>> 23` being the 16.16 divide by
128 world units — when the unit's footprint anchor lies inside the attribute
grid, and into **this one extra record** when it does not, testing
`anchorX < 0 || anchorZ < 0 || width <= anchorX + footprintX ||
height <= anchorZ + footprintZ`. Its sector-height bytes stay zero forever
because the build passes never visit it. This closes the doc 04 tail's
"semantic name and domain of the global sentinel": it is the **out-of-bounds
sector record**, its domain is "one per map, allocated at load, never in the
grid array", and the comparison every reader performs is a full 32-bit pointer
equality against that global.

**Established — eight consumers, not one.** The flight integrator skips the
vertical velocity assignment entirely while the unit's sector link equals the
sentinel (established above). The marker goal update declines to follow a
**target** whose sector link equals the sentinel. Six air executors —
`VTOL_LandIfCan`, `VTOL_Follow`, `VTOL_SeekAttack`, `VTOL_SeekGuard`,
`AirToAir` and `AirToGroundHover` — run an identical **off-map recovery leg**
before their phase switch and return from it immediately: build a point marker
at `unitPos + offset` where `offset` is the negated sine/cosine pair of
`bearing(unitPos, mapCentre)` at radius `0x3200000` (800 world units) and
`mapCentre` is `(worldWidth / 2) << 16, (worldHeight / 2) << 16`; give it
horizontal arrival radius `0x80`; OR `0xE0` into the gate word; install it;
return result code 2. `AirToGround` handles the same condition differently and
does **not** build a recovery marker: it sets the record's deadline to the
current tick plus 30, forces its phase to 2, and falls through into its ordinary
phase switch. An aircraft that leaves the map has no vertical control at all
until it re-enters.

### 10.2 Air orders

**Established fact:** Air order dispatch classifies move, attack, DGun, load/unload/pickup, follow/help/repair, patrol, hold, teleport, reclaim/resurrect, capture, and mobile-build cases. Weapon flags and attack-run/hover-attack data choose bomber, fighter, gunship, transport, or related behavior.

**Established fact:** Transport service lifecycle is exact for admission, carry,
unload, pads, and death:

*Admission.* `carrier, candidate` is admitted only if, in this order, none of these nine rejects fires: 1) candidate `cantbetransported` set; 2) carrier lacks `canload`; 3) carried-count (entries in the carrier cargo list whose parent equals the carrier) reaches carrier `transportcapacity` (count, not summed sizes; unauthored `0` therefore blocks loading); 4) carrier `transportsize` below candidate `FootPrintX` (signed compare, FootPrintX is the movement class footprint width); 5) candidate has no mover; 6) candidate committed mover mode is `2`, which is AIRBORNE, not "moving" — an airborne candidate is rejected (corrected below); 7) ground carrier (`canfly` clear) with candidate `MinWaterDepth >= 0`; 8) candidate `Y + modelTop` at or below `sea level × 65536` (submerged); 9) candidate landed-float field not exactly `0.0` (still under construction). Missing `transportcapacity` and `transportsize` default to `0`. The effective boarding range is the first enabled weapon slot's `range` (scanned via the weapon-slot enabled flag); shipped unarmed fallback is weapon record `0` (`NOWEAPON`, Range 16), so shipped unarmed pickup range is `16`. Ownership or alliance is not tested in this predicate, and the two command resolvers contain no alliance gate either (bounded-negative within them), so whether allied cross-owner commands are permitted remains an upstream command-layer question left open (`TODO(question)`).

**Correction — reject 6 is "airborne", not "moving" [R-MOV-01 §8]
(2026-08-28).** The earlier wording read "candidate committed mover mode is
active locomotion (mode 2, moving) — moving cargo is rejected". The mode
label was wrong, not the predicate: [R-MOV-01 §8] establishes from a writer
census that mover mode `1` is grounded/on the surface and mode `2` is
airborne, with a ground unit at `1` for its whole life whether it is moving or
standing still. The admission test therefore does not reject a moving ground
candidate at all — it rejects a candidate that is **in the air**. A rolling
tank is admissible on this gate; a flying gunship is not.

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

### Closed — takeoff, pad landing, and ground landing [R-AIR-01 §6] (2026-08-29)

**Established — one shared takeoff preamble.** Every air executor that must get
the unit off the ground runs the same five steps, in this order; one executor
holds them as a separate shared routine and the rest inline them verbatim:

1. **Release the manual-target latch on all three weapon slots.** The helper
   takes a slot index, treats `3` as "slots 0, 1 and 2 in that order", and for
   each enabled slot whose latch bit `0x10` is set clears that bit and — unless
   the slot's target pair is already the null pair `(0, 0x8000)` — resets the
   pair to `(0, 0x8000)`, stops `StartBuilding`, and fires the one-argument
   `TargetCleared` callback with the slot index in cell 0. Its mirror image sets
   the latch instead, with the same reset and the same two callbacks.
2. If the unit currently has a carrier, detach it (reserved no-piece index
   `0xFF`) requesting mover mode `2`.
3. Set the unit state byte's bit `0x01`, which raises the **`Activate`** COB
   callback and notification event `3` on the rising edge (the edge machine of
   [R-UNIT-06 §2]); this is the engine's takeoff script hook.
4. **Only if** the committed mover mode is `1` (grounded): call the mover-mode
   setter with mode `2` (see [R-AIR-01 §3]); build a fresh point path marker on
   the unit's own current X/Y/Z; set its altitude offset to
   `cruisealt / 2` (signed 16-bit `cruisealt`, C division, truncating toward
   zero); install it as the order record's goal payload; OR `0xE0` into the
   record's dynamic gate word.
5. Return result code `1` (advance the phase).

If the unit is already airborne the marker is **not** built and the phase still
advances, so a mid-air order does not reset the aircraft's climb goal. The
`cruisealt / 2` marker is therefore an *initial climb* goal only.

**Established — `VTOL_Landing` is a seven-phase pad-landing machine.** Its
order record carries a scratch word that the machine reuses for two different
things: a loiter bearing in phases 0–1 and the chosen pad piece index from
phase 3 onward. With a null target reference at entry the executor emits status
cue slot 7 `Landing aborted` and returns 8.

| Phase | Work | Result |
|---:|---|---:|
| 0 | Require a live mover and `canfly` (else 7). Status caption `Landing` (slot 5, announced once). Run the shared takeoff preamble. Then draw one simulation random value below `0x10000` and store it as the loiter bearing. | 1 |
| 1 | Run `QueryLandingPad` on the **target's** script, four outputs all pre-seeded `-1`; take the first candidate `0..3` that is not `-1` and is free (below). Re-test the winner; if it is `-1` or no longer free, run `QueryLandingPad` a **second** time into a fresh four-cell buffer and scan again. If a pad is found, set phase 2 and return 2. If not: build a point marker at the target's position offset by the loiter bearing at a radius equal to the unit's **first weapon slot's `Range`**, give it horizontal arrival radius `0x80` (128), install it, set the gate word to `0xE8`, advance the loiter bearing by `0x4000` (a quarter turn), keep phase 1. | 2 |
| 2 | Build a follow-unit marker on the target with the reserved no-piece index and horizontal arrival radius `0xA0` (160); install; gate `0xE8`. | 1 |
| 3 | `QueryLandingPad` once, same scan. Store the winner in the record's scratch word. If none: status cue slot 7 `Landing failed`, return 0 (reset the phase to zero). Otherwise build a follow-unit-**piece** marker on the target's chosen pad piece with horizontal arrival radius `0x30` (48); install; gate `0xE8`. | 1 |
| 4 | No work. | 1 |
| 5 | If the satisfied set contains the movement-arrival bit `0x20` — the approach marker has been reached — return 1, which advances to phase 6 and does nothing else this visit. Otherwise revalidate the stored pad and, if it is stale, re-query and rescan; if still none, status cue slot 7 `Landing aborted: all pads are occupied`, return 0. Otherwise build the follow-piece marker again with altitude offset `0` when the lander carries nothing, or the **integer part of the cargo definition's model total-height dword** when it does; start the deferred `EndTransport` with the wake flag set; install; set the record's deadline to the current tick plus 15; gate `\|= 0xE8`. | 2 |
| 6 | If the satisfied set contains the goal-release bit `0x40`, return 8. Revalidate the stored pad once; if it is not free, status cue slot 7 `Landing aborted: no pads available`, return 0. Otherwise: **empty lander** — attach the lander itself to the target on the pad piece with request mode `0`, and, when the lander's health is below its definition's `MaxDamage` **and** the pad owner's definition has both `isairbase` and `builder` set **and** the pad owner is not under construction, clear the goal payload and push a `SELFREPAIR` order record on the lander. **Loaded lander** — issue the deferred `EndTransport` (no wake) and attach the **cargo** to the target on the pad piece with request mode `0`. | 5 |
| other | — | 7 |

A pad piece counts as **free** exactly when the pad owner is not itself being
carried and no unit in the pad owner's cargo list records that same attach-piece
index. The four candidates are tried strictly in index order `0,1,2,3`; a
`-1` cell is skipped, not treated as end-of-list.

This supersedes the earlier §10.2 sentence "With no pad the loiter/spiral
heading step is used; no free pad among those tried keeps the order alive for a
next-tick retry or the `30+rand(15)` delayed retry". There is no `30+rand(15)`
retry in this executor: the retry is the phase-1 loiter leg, which re-runs every
time the loiter marker is reached and advances the bearing by exactly a quarter
turn; the only random draw in the whole machine is the single full-circle
bearing draw in phase 0. The two distinct failure messages are the phase-5
`Landing aborted: all pads are occupied` (a pad was found earlier but is now
taken) and the phase-6 `Landing aborted: no pads available` (the reserved pad
was taken between the approach and the touchdown), both returning result code 0,
which resets the phase to zero so the machine restarts from takeoff. The
one-word `Landing aborted` message belongs to the null-target entry guard only.

**Established — `VTOL_LandIfCan` lands on terrain, not on a pad.** Its
three-phase machine is the one an idle aircraft with nowhere to park runs.

* Entry: a satisfied goal-release bit `0x40` returns 5; the off-map recovery leg of
  [R-AIR-01 §5] pre-empts everything else.
* Phase 0: require a live mover and `canfly`. If the record's cached goal is
  exactly `(0,0,0)`, copy the unit's current position into it. Draw one
  simulation random value below `0x10000`; store it as the search bearing and
  store its low bit in a second scratch word. Run the shared takeoff preamble.
  Result 1.
* Phase 1: ask the landing-legality test whether the unit's **current**
  position is landable. If it is: start the asynchronous `EndTransport` with
  the wake flag; build a point marker at the unit's own position whose altitude
  offset is `0` when the terrain height there is at or below sea level and
  `terrainHeight − seaLevel` otherwise — both branches place the marker's
  commanded Y at exactly the terrain height, because the marker's
  terrain-derived altitude rule adds the offset to `max(seaLevel, terrainHeight)`;
  install; gate `0xE0`; **clear** the unit state byte's bit `0x01`, raising the
  `Deactivate` COB callback and notification event `4` — the landing script
  hook. Result 1.
  Otherwise search for a landable spot: for `k = 0,1,…,11`, with
  `span = 0x81 + 0x20·k` and `half = 0x40 + 0x10·k`, draw a random value below
  `span` for X and another below `span` for Z (two draws per iteration, in that
  order), offset the unit's position by `(draw − half)` world units on each
  axis, snap the result to the unit's footprint half-cell anchor, and test it.
  The first landable candidate becomes a plain point marker (gate `0xE0`,
  result 2). If all twelve fail: when the arrival bits `0xE0` are set, advance
  the search bearing by `−0x5555` (about `−120` degrees); build a point marker
  at the record's cached goal offset by that bearing at radius `0xA0` (160
  world units) with horizontal arrival radius `0x40` (64); gate `|= 0xE0`;
  result 2. The search therefore costs up to **24 simulation random draws per
  visit**, and the draw count is data-dependent.
* Phase 2: if the satisfied set does not contain the movement-arrival bit `0x20`, return 8; otherwise
  call the mover-mode setter with mode `1`, which zeroes the velocity and the
  scalar speed and levels bank and pitch ([R-AIR-01 §3]), and return 5.

**Established — `VTOL_GetRepaired` is a two-phase wait.** With a null target it
emits status cue slot 7 `Repair aborted.` and returns 8. Phase 0 returns 1 as
soon as the unit's health has reached its definition's `MaxDamage` (unsigned
compare, `MaxDamage <= health`); otherwise it sets the record's deadline to the
current tick plus 30, ORs `0x8` into the gate word, and returns 2. Phase 1
emits status cue slot 10 `Unit repaired` and returns 5. This is one of the four
`Unit repaired` producers the doc 05 caption sweep is looking for.

### Closed — standby, the idle circle, and the seek states [R-AIR-01 §7] (2026-08-29)

**Established — `VTOL_Standby` decides between parking and circling.** Phase 0
requires a live mover and `canfly`, releases the manual-target latch on all
three weapon slots, ORs `0x10000` into the gate word, sets the record's deadline to
the current tick plus 1, and records the unit's **post**: the integer world X
and Z of its current position, stored in the record's two post words. Result 1.

Phase 1 asks the ordinary autonomous acquisition for a target and, if one is
found **and** accepted, clears the gate word, resets the phase to zero and
returns 3 (the pump's `30 + random below 15` wait). Otherwise result 1.

Phase 2 is the idle decision:

* If the unit is not `canfly`, or the low two bits of its status word are not
  `2`, OR `0x10000` into the gate word, set the deadline to the current tick
  plus `30 + random below 30`, set the phase to 1, return 2.
* Else if the unit **is carrying cargo**: draw a full-circle bearing (random
  below `0x10000`), draw a radius `8 + random below 0x20` world units, and
  build a point marker at the recorded post offset by that bearing and radius,
  with altitude offset the full `cruisealt`; install it; set the deadline to
  the current tick plus `30 + random below 15`; set the phase to 1; return 2.
  **This is the whole of retail's aircraft "circling" behavior**: a fresh
  uniformly random bearing and an 8-to-39 world-unit radius about a fixed post,
  redrawn every 30 to 44 ticks — not a geometric orbit and not a fixed station
  ring. Three simulation draws per visit, in the order bearing, radius, delay.
* Else (no cargo): allocate an order record for `VTOL_LandIfCan` carrying the
  record's cached goal and push it on the unit; return 5. An idle unloaded
  aircraft therefore always tries to land; only a loaded one loiters.

**Established — `VTOL_SeekAttack` is a randomized search orbit.** Entry: a
satisfied goal-release bit `0x40` returns 5, and the off-map recovery of [R-AIR-01 §5]
pre-empts. Phase 0 requires a live mover and `canfly`; with a target already
bound it simply tries to latch it and, on success, clears the gate word and
returns 0; with no target it defaults the cached goal to the unit's position if
that goal is exactly `(0,0,0)`, draws one full-circle bearing (random below
`0x10000`), stores it and its low bit, and runs the shared takeoff preamble.

Phase 1, in this order: set the manual-target latch on all three slots; if the unit's health
is **below three quarters** of its definition's `MaxDamage` (computed as
`(MaxDamage >> 2) * 3`, unsigned, strict `<`), collect the nearby-unit
candidate list within `0xF00` for the unit's ally group and, if it is non-empty,
clear the goal payload, draw one random index over the candidate count, push a
`VTOL_LANDING` order at that candidate, clear the gate word and return 0; then
ask the ordinary acquisition for a target and return 5 if one is latched;
then, if the arrival bits `0xE0` are set, advance the search bearing by
`−(0x5555 + random below 0x2000)`; finally build a point marker at the cached
goal offset by the search bearing at radius `firstWeaponRange + 0xA0` world
units, horizontal arrival radius `0x80`, install, set the deadline to the
current tick plus `30 + random below 30`, OR `0xE0` into the gate word, and
return 2.

The `−0x5555` step is about `−120` degrees, so the search visits three points
per revolution before the random jitter, and the jitter is a *subtractive* term
below `0x2000` (about 45 degrees), never additive.

**Closed (2026-08-29):** `VTOL_SeekGuard`'s and `VTOL_Follow`'s per-phase
contracts are in [R-ORD-02 §3] (four-leg guard, orbit radius, no takeoff
preamble for seek-guard); this line previously listed them as Unknown.

### Closed — attack runs, hover attack, evasion, and the maneuver leash [R-AIR-01 §8] (2026-08-29)

Four separate executors implement air combat, chosen by the command resolver,
not by unit class: `AirStrike` (the bombing run), `AirToGround`
(the strafing run), `AirToGroundHover` (the standoff orbit selected by
`hoverattack`), and `AirToAir`. All four share an entry sequence and then
diverge completely.

**Established — the shared entry sequence.** In this order:

1. If the satisfied set intersects `0x1000A` (`AirStrike`, `AirToGround`) or
   `0x10008` (`AirToGroundHover`, `VTOL_Evade`): when the record has no
   successor marker **and** the unit's status word has either of bits
   `0x300000` set, replace the current order with a fresh `VTOL_SEEKATTACK`
   record carrying the same target and cached goal; return 5 either way.
2. If the target reference is null but the record's `0x200` "cached goal valid"
   bit is set, replace the current order with `VTOL_SEEKATTACK` at the unit's
   own position and return 5.
3. If the target reference is live, **refresh the record's cached goal from the
   target's current position every visit** — the cached goal is a stale-target
   fallback, not a fixed aim point.
4. Off-map recovery ([R-AIR-01 §5]) — `AirToAir` and `AirToGroundHover` take the
   recovery leg and return from it; `AirToGround` instead sets the record's
   deadline to the current tick plus 30, **forces the phase to 2**, and falls
   through; `AirStrike` tests the sentinel nowhere at all.
5. **The maneuver leash.** If the record's leash word is nonzero, compute
   `hypot(unitIntegerX − postX, unitIntegerZ − postZ)` in whole world units
   against the record's two post words, truncate to an integer, and return 5
   when `leash <= distance`. This is the only air-side consumer of the leash
   word that [R-STANCE-01 §4] installs from `maneuverleashlength`; the compare
   is on **integer world units**, not 16.16, and is inclusive, so a leash of `0`
   is "no leash" rather than "never move".

**Established — `AirStrike`: the bombing run, with a ballistic release lead.**

| Phase | Work | Result |
|---:|---|---:|
| 0 | Status caption `Attacking`; shared takeoff preamble. | 1 |
| 1 | Set the manual-target latch on all three slots, then release it on slot 0. Measure `d = hypot(goal − unit)` in 16.16. If `d < 0x1E00000` (480 world units) the bomber is too close to start a run: build a point marker at `unitPos − offset(bearing(unit, goal), 0x8C00000)` — a point 2240 world units from the aircraft along the bearing helper's axis — with horizontal arrival radius `0x3C0` (960), and gate `\|= 0xE2`. Both branches return 1, so the phase advances either way; the test only decides whether a repositioning marker is installed. | 1 |
| 2 | The swing-wide leg. `d = hypot(goal − unit)`; `h = bearing(unit, goal)`; draw one random value below `0x4000` and form `h' = h + draw − 0x2000` (a uniform ±45-degree jitter); build a point marker at `unitPos − offset(h', d/2)` with horizontal arrival radius `0x1E0` (480); gate `= 0x100E8`. | 1 |
| 3 | No work. | 1 |
| 4 | The release-point leg. If the satisfied set contains any of the arrival bits `0xE0`, return 1 immediately (advancing the phase) and do nothing else. Read the map's `gravity`; **if it is zero, return 7 — a cancel-all of the whole queue.** Otherwise compute the release lead exactly as `t = sqrt((2 · cruisealt) / gravity)` in float, `lead = trunc(t · 30.0 · speedInteger)` where `speedInteger` is the signed 16-bit integer part of the mover's scalar speed word, and set the marker's horizontal arrival radius to `lead + 1 + attackrunlength`. The marker is a follow-unit marker on the target when one is bound, else a point marker on the cached goal. Install; deadline `= tick + 1`; gate `\|= 0x100E8`. | 2 |
| 5 | The overfly leg. Release the slot-0 latch; order the weapons to fire at the cached goal position; build a point marker at `unitPos − offset(bearing(unit, goal), (attackrunlength + 0x3C0) << 16)` with horizontal arrival radius `0x3C0`; gate `= 0xE2`. | 1 |
| 6 | The break-off leg. Stop firing; build a point marker at `unitPos − offset(unitHeading, 0x5A00000)` — 1440 world units along the unit's own heading axis — with horizontal arrival radius `0x80`; gate `= 0xE2`. Then, if health is at or above three quarters of `MaxDamage`, set the phase to 3 and return 2 (fly another run). Otherwise collect candidates within `0xF00`, and if any exist clear the payload, draw one random index, push a `VTOL_LANDING` order at that candidate, clear the gate word and return 0; with no candidates return 0. | 2 or 0 |

`attackrunlength` therefore has exactly one gameplay consumer: it lengthens the
bomb-release radius in phase 4 and the overfly distance in phase 5. It is a
horizontal arrival radius in **world units**, added to a physically derived
lead; it is not itself a time or a speed. The `30.0` factor converts the
free-fall time from seconds to ticks against a per-tick speed, so the whole
expression is `speed_per_tick · 30 · sqrt(2·cruisealt/gravity)` world units.
`gravity` is the runtime word the map loader fills from the OTA `gravity` key,
defaulting to `0x1FDB` when neither the OTA nor the TNT header supplies one.

**Established — `AirToGround`: the strafing run.** Six phases.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Established — `AirToGroundHover`: the `hoverattack` standoff.** Phases 0 and 1
match `AirToGround`. Phase 2 releases the slot-0 latch, aims at the target, builds a point marker on the
**target's** current position with horizontal arrival radius equal to the first
weapon slot's `Range`, and zeroes two record scratch words (a side flag and a
miss counter). Phase 3 is the orbit:

* Ask the weapon layer whether the unit can engage the target; if it cannot,
  increment the miss counter.
* If the miss counter exceeds `1`, reset it, draw a full-circle bearing
  (random below `0x10000`), build a point marker at
  `targetPos − offset(bearing, Range)` with horizontal arrival radius `0x80`,
  gate `|= 0x110E8`, return 2.
* Otherwise alternate sides: `h = headingToTarget`, and `h' = h − 0x2000` with
  the side flag set to 1 when the flag was 0, or `h' = h + 0x2000` with the flag
  cleared when it was 1 — a deterministic ±45-degree left/right alternation, no
  random draw. Build a **frozen terrain-relative** marker (its goal is fixed at
  construction and its altitude is `cruisealt` above the four-corner terrain
  height at that point) at `targetPos + offset(h', (Range · 2) / 3)`, with
  horizontal arrival radius `0x10` (16), and gate `= 0x100E8`.
* Then the same health-below-three-quarters find-a-base branch.

`hoverattack` selects this executor at command resolution; it changes no
arithmetic inside the mover.

**Established — `AirToAir` uses a second, velocity-carrying goal payload.**
The dogfight legs do not command a point: they command a *position and a
velocity*, through a second payload class whose per-tick goal update advances
its own position by its own velocity vector each tick (X and Z only; Y is not
advanced) and, when its "steer to a commanded heading" flag is set, rotates the
velocity's horizontal pair toward the commanded heading by at most
`TurnRate >> 3` per tick, zeroing the vertical component whenever it does. Its
arrival test is a **hard-coded 48 world units** of horizontal distance, with the
additional requirement — when that flag is set — that the velocity's bearing
equal the commanded heading exactly. Phase 0 is the takeoff preamble plus a
one-tick deadline. Phase 1 aims at the target and then:

* When the arrival bits `0xE0` are set and the dot product of the
  unit→target bearing vector and the unit's own facing vector (both taken at
  20 world units) is positive, command "straight ahead": position
  `unitPos − offset(unitHeading, MaxVelocity · 30)`, velocity
  `−offset(unitHeading, MaxVelocity)`; deadline `tick + 60 + random below 30`;
  reset the scratch counter; return 2.
* When they are not set and the scratch counter is below `0x5A`, recompute the
  same dot product, add `0x2D` to the counter if it is not positive and zero it
  otherwise; then, if the range to the target exceeds `0xA0` world units,
  command a lead intercept: position `targetPos + targetVelocity · 45`,
  velocity derived from the target's heading at half the target's
  `MaxVelocity`. Deadline `tick + 45`; gate `|= 0x100E8`; return 2.
* Otherwise the leg gives up and re-issues a seek order.

**Established — `VTOL_Evade`.** Entry returns 5 on a null target or when the satisfied set
intersects `0x10008`. Phase 0 requires a live mover and `canfly`, draws
`random below 2` into a record scratch word, and forms
`h = unitHeading + (draw == 0 ? 0x4000 : 0xC000)` — a random 90-degree break
left or right — then builds a point marker at `unitPos − offset(h, Range)`
with horizontal arrival radius `0x80` and gate `0x100E8`. Phase 1 repeats the
same break **on the same side** (the scratch word is re-read, not re-drawn) at
**twice** the radius. Phase 2 returns 5. Exactly one random draw per evasion.

### Closed — the two transport executor pairs, and the corrected hang and drop offsets [R-AIR-01 §9] (2026-08-29)

**Correction — there are two load executors and two unload executors, split by
carrier locomotion, not by order family name.** §10.2 above documents only the
air pair (`VTOL_Pickup` / `VTOL_Unload`). The `Ground_Pickup` / `Ground_Unload`
pair is a separate machine that never moves the cargo itself: it fires a COB
callback and waits for the **script** to perform the attachment or the drop
through the COB transport opcodes ([R-COB-03 §5]).

*`Ground_Pickup`.* Entry: a null target, or a satisfied bit `0x8`, emits
status cue slot 7 `Transport mission failed` and returns 8; a phase above 5
returns 7.

| Phase | Work | Result |
|---:|---|---:|
| 0 | Require a live mover (else 7) and the carrier definition's `canload` bit (else 7). Size gate: the target's cached footprint-X word, compared **signed**, must be at or below the carrier definition's `transportsize` byte zero-extended; otherwise status cue slot 7 `Unit is too large to transport` and return 8. Otherwise set the status caption `Loading unit` (slot 5). | 1 |
| 1, 3 | The shared short-move helper: when the unit's movement-state byte has bit `0x2` set, write gate `0x8 \| 0x4` and return 2; otherwise return 1. | 1 or 2 |
| 2 | Start the asynchronous one-argument `TransportPickup` on the **carrier's** script with cell 0 = the cargo's stable unit identity; emit notification event 12; increment the record's attempt counter; set the deadline to the current tick plus 15. | 1 |
| 4 | If the target now has a carrier, return 5 (the script did the attach). Else if the attempt counter has reached `3`, return 9. Else install a ground goal handle at the target's current position with radius parameter `0`, gate `= 0xE8`. | 1, 5 or 9 |
| 5 | Clear the goal payload. | 0 |

This answers the open question in the RWU-04-8 triage list. The second
executor's flags-word bit is `canload` itself — the same bit the general
admission predicate tests — while the *air* executor gates on `canfly`; and its
two compares read exactly the same two fields as the air executor's, namely the
**target's runtime cached footprint-X word** against the **carrier definition's
`transportsize` byte**. The triage note's guess that it compared "a byte of the
target's definition against a word of the carrier's runtime state" is inverted.
Only the message differs: `Unit is too large to transport` for the ground
carrier, `Unit is too heavy to transport` for the air carrier. Both captions
(`Loading unit` and `Loading`) go through the same one-shot caption setter on
status slot 5; the difference is the text, not the slot. The two executors are
selected by order identity (`Ground_Pickup` versus `VTOL_Pickup`) at command
resolution and never both run for one record.

*`Ground_Unload`.* Entry: a satisfied bit `0x8` emits status cue slot 7
`Unloading process is proceeding non-optimally` and returns 8. Phase 0 requires
a live mover and `canload`, binds the record's target handle to the carrier's
cargo-list head, returns 5 if that head is null, sets the caption `Unloading`,
and starts the asynchronous one-argument `TransportDrop` on the carrier's script
with cell 0 = the cargo's identity and cell 1 = the packed drop point (the
record's goal X truncated to whole world units in the high half, the goal Z
integer part in the low half); it then increments the attempt counter and sets
the deadline to the current tick plus 15. Phase 1 is the same short-move helper.
Phase 2 emits notification event 13 and returns 5 as soon as the cargo's carrier
reference is no longer this carrier; otherwise it returns 9 once the attempt
counter reaches `3`, and otherwise installs a ground goal handle at the record's
goal with radius parameter `trunc(carrierModelZExtentInteger · 1.5)` when the
carrier definition has `canhover` set and `0` when it does not, gate `= 0xE8`.
Phase 3 returns 0.

**Correction — the phase-3 hang offset of `VTOL_Pickup` is measured in the
carrier's model frame, and it is the carrier's goal, not the cargo's.** The
`VTOL_Pickup` phase-3 row above says the executor constructs "the cargo follow
order with altitude offset = NEGATED integer part of that piece's world Y — the
cargo hangs below the piece". Two parts of that are wrong. The transform it
evaluates is the piece-hierarchy evaluator **without** the unit-origin addition
(the inner half of the exit-piece locator of [R-REV-02]), so the value is the
attach piece's Y in the **carrier's own model frame**, not a world Y; and the
marker it builds is a follow-unit marker on the **cargo**, installed as the
**carrier's** movement goal, so the negated offset lowers the *carrier* until
its attach piece meets the cargo. Nothing hangs before the attach. The value
used is the signed 16-bit integer part of that model-frame Y, negated.

**Correction — the `VTOL_Unload` lowering offset is the cargo's model
TOTAL-HEIGHT integer, not its model bottom.** §10.2 above says phase 1 "queues
the lowering command at the same X/Z with signed altitude offset = the cargo
definition's model-bottom value". The word read is the high half of the
definition's **model total-height dword** — the same max-Y dword that
`BeginTransport` carries as its single argument ([R-UNIT-06 §3]) — i.e. the
model's height in whole world units, and it is positive. The marker's
terrain-derived altitude rule then places the carrier at
`max(seaLevel, terrainHeightAtDropPoint) + cargoModelHeight`, which is exactly
the height at which cargo suspended below the carrier touches the ground. The
same word, on the same definition, supplies the altitude offset of
`VTOL_Landing` phase 5 when the lander is carrying something. There is no
authored `model-bottom` key and the definition's min-Y bound is a different
word, which is why the earlier reading could not be implemented.

**Correction — the detach half of the attachment helper takes the mover mode
from the request, and the "becarried" re-arm is gated on player state, not on
computer ownership.** [R-UNIT-06 §3] states the re-arm fires "for computer-owned
children whose parent is not an airbase". The traced predicate is: the child's
**player slot state byte is `1` or `2`** (the two states whose units are pumped
at all, per the per-unit sweep) **and** the parent's definition does **not**
carry `isairbase`. Ownership by a computer player is not tested. The rest of
[R-UNIT-06 §3] re-verifies unchanged, with two additions: the helper also
refuses a child that has cargo of its own (a loaded transport cannot itself be
loaded), and the mover-mode write is a **direct** write of the request's low two
bits into the committed mover-mode pair — it does **not** go through the
mover-mode setter, so attaching or detaching never zeroes velocity, never levels
bank and pitch, and never raises `Activate` or `Deactivate` ([R-AIR-01 §3]).

**Established — request modes actually used.** `0` on every ordinary attach
(`VTOL_Pickup` phase 4 for the cargo, `VTOL_Landing` phase 6 for the lander
itself and for its cargo); `2` on the self-detach a carried carrier performs in
the takeoff preamble; `1` on the `VTOL_Unload` phase-2 release. Mode `0` is
therefore reached in ordinary play by every transported unit and by every
aircraft parked on a pad — see [R-AIR-01 §3].

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

**Unknown:** Ground-following and sea behavior while orbiting, and carrier
collision beyond the ordinary shared-mover rules.

**Correction (2026-08-29, RWU-04-8).** This paragraph previously also listed
"pad reservation beyond the landing-pad selection described in 10.2" and "the
order-layer command-target supply for each non-construction air class beyond
the established altitude authority and integrator arithmetic of section 10.1".
Both are closed. Pad reservation is the four-candidate `QueryLandingPad` scan
plus the free-pad predicate of [R-AIR-01 §6] — a pad is reserved by nothing but
the attach-piece index recorded on the units already in the pad owner's cargo
list, re-tested at every phase transition; there is no separate reservation
table. The command-target supply is the flight command block of
[R-AIR-01 §1]: there is exactly one supply for every air order, and the orders
differ only in which goal payload they install ([R-AIR-01 §4]).

## R-MOV-03 — the unit sweep composed, and the small contracts the ledger left open (RWU-04-13, 2026-08-29)

This unit closed the lane-04 rows of the executable coverage ledger. Most
rows were already stated by an earlier closure and are only cited there; the
sections below carry what no earlier section spelled out. Every claim is
Established by direct static trace unless marked otherwise.

### Closed — the per-player unit sweep, step by step [R-MOV-03 §1] (2026-08-29)

Section 1 states the order of one unit visit; this section pins the gates,
counters and cadences around it so the visit can be written without choosing
anything.

**The player gate.** The sweep zeroes the session's live-unit counter, then
visits player slots 0 through 9 in order. A slot is processed only when its
record exists, its controller byte is 1, 2 or 3, and its state byte is not
the eliminated value 10. Within the slot every unit record of the player's
slice is visited in ascending pool order; a record whose definition index is
zero is skipped. The controller-1/2 test that gates the order pumps and the
mover (section 8.3's "compact ground controller" closure) is re-evaluated per
unit from the **owner** record, not from the slot being swept.

**Per unit, in this order:**

1. the live-unit counter is incremented;
2. the general unit update runs (the wind-generator notifier of
   [05 R-PROD-01 §3] lives here);
3. for an owner of controller 1 or 2 only, the weapon update (doc 06);
4. when the unit has a script VM, the normal drain with tick delta 1
   ([04 §4.6] "drain and lerp order");
5. the minimap blink byte, if nonzero, is decremented as a signed byte
   ([06 R-WPN-04 §2]);
6. the **post-capture countdown**, if nonzero, is decremented by one. This is
   the decrement [R-ORD-02 §6] asked for: doc 05's ownership transfer writes
   150, and the sweep counts it down one per unit visit; the contextual
   resolver's own-unit reject ([R-ORD-02 §1]) and the selection predicates
   read it;
7. **selection maintenance:** a unit carrying the *selected* bit (bit 4 of
   the state word) loses it when it is no longer *ready*, where ready means
   all of: the *selectable* bit (bit 5, the bit the `MakeSelectable` order
   sets) is set, the remaining-build fraction compares exactly equal to
   `0.0f`, the post-capture countdown is zero, and either the unit has no
   carrier or its carrier's state word has bit 30 set;
8. on ticks where `tick mod 30 == 0` the unit's health percentage pair is
   rolled: the previous-percent byte takes the current-percent byte, and the
   current-percent byte is written as `clamp((int16)health · 100 /
   maxdamage, 0, 100)` — the same unsigned division and clamps as the
   `TakeDamage` percent of section 5.1 (the `< 0 → 0` clamp is unreachable
   for an unsigned quotient below `2^31` and is kept only for fidelity);
9. **for an owner of controller 1 or 2:** the water-damage packet of section
   9.2 (its own `tick mod 30 == 0` cadence, the mission's `waterdoesdamage`
   and `waterdamage`, unit integer height `<=` the sea-level byte, and the
   definition's `canhover` exemption); then the `healtime` self-repair step of
   [R-SPEC-01 §4] on ticks where `tick & 7 == 0` while `(uint16)health <
   maxdamage`; then the primary pump and the secondary pump (section 3.3);
   then, when the unit has a mover, the mover tick ([R-MOV-01 §1]) followed
   by the post-move correction gate ([R-MOV-01 §5]);
10. when the death-pending bit (bit 14) is set, the death mark runs with the
    unit's stored death-kind byte (doc 06 [R-DMG-01 §3]).

After a player's units, and only in a networked session whose owner is
controller 1 or 2, the engine emits the unit-state synchronization bitstream
(packet kind 44). That packet, its bit writer and the per-payload stream
writers of the ground follower, the air controller, the path marker and the
velocity marker are **multiplayer transport** and out of Nanolathe's scope;
they write no simulation state other than the player's last-sync tick, which
only the network layer reads.

**The sweep tail — the `+BigBrother` cycle.** When the camera-flags bit that
`+BigBrother` toggles ([07 R-CAM-01 §12]) is set and the camera's hold key is
**not** held, the 16-bit companion counter that closure left with an unlocated
reader is decremented here; when it falls below 1 it is reset to **90** and
two things happen in order: (a) the **next-ready-unit selector** runs, and
(b) the camera's tracked object is re-picked from the selection as the `t`
key does ([07 R-CAM-01 §2]). The selector walks the local player's slice in
ascending order remembering the first *ready* unit (the same four-part
predicate as step 7); if it meets a ready unit that is currently selected it
clears the selected bit and the two selection-companion bits (bits 4, 6, 7)
on **every** unit of the whole pool, closes the command panel pages (doc 07),
and selects the next ready unit after it — wrapping to the remembered first
ready unit when none follows; if no selected ready unit exists it selects the
remembered first ready unit. In every case it raises the interface's
selection-changed flag (Supported inference for the flag's name; the write is
direct). The counter starts from whatever value the toggle wrote, so the first
cycle after enabling is not 90 ticks long. **Unknown:** the identity of the
held key the query tests (doc 07 owns the key table; decider: the camera
held-key census of [07 R-CAM-01 §2]).

**Band-classifier note.** The medium classifier of [R-MOV-01 §8a] evaluates
its tests in a fixed order with later results overriding earlier ones: above
sea level is band 4; otherwise the band starts from the previous value, then
`height − sea > −5` sets 1, then `height + waterline == sea` sets 2, then
`height + modelBottom < sea` sets 3 (with `waterline` the definition byte and
`modelBottom` the signed 16-bit reference-height word). A mover whose mode is
neither grounded nor airborne classifies as band 0. The `setSFXoccupy` start
fires only on a change of band.

### Closed — the goal-payload method table, and the three goal-point queries [R-MOV-03 §2] (2026-08-29)

[R-PATH-01 §9] states the start predicates, enumerators and heuristics of the
goal classes. The remaining entries of the shared method table are:

* an **is-a-search-goal** flag returning 1 for the point, annulus and
  rectangle classes and 0 for the two air classes — the static counterpart of
  the "never reach the search" statement of [R-PATH-01 §9];
* a **satisfied-from-unit** adapter that forwards the unit's committed cell
  pair to the two-argument start predicate (the base and rectangle classes
  share one adapter; point and annulus carry their own);
* the base class's remaining methods return 0 or write nothing;
* a **save-class code** on the air path marker (value 2; the codes are
  [08 R-SAVE-02 §10]'s);
* the **persistence** query — 0 for the base and the velocity marker,
  [R-AIR-01 §4]'s rule for the path marker.

**The goal-point queries — Established.** [R-PATH-01 §9] gives the point
class's query. The other two:

* **Annulus.** The centre cell is converted to world exactly as for the point
  class (`(FootPrint + 2·cell) · 2^19` per axis, footprint from the owning
  order's unit); then `b = bearing(unitPos, centre)` (the section-10 helper,
  argument order self-then-other) and `r = ((inner + outer) / 2) << 16`, the
  divide a signed integer division; the query returns `centre + polar(b, r)`
  on X and Z, with Y untouched. `inner` and `outer` are the octile radii the
  installer supplied.
* **Rectangle.** `X = (FootPrintX + 2·((x1 + x2) / 2)) · 2^19` (signed
  integer division) and `Z = (FootPrintZ + 2·z2) · 2^19` — the middle column
  and the **far** Z edge, not the centre.

**The annulus constructor — Established.** Given world X and Z and the two
octile radii, the centre cell is `(w − FootPrint · 2^19 + 2^19) >> 20` per
axis (arithmetic shift; the footprint snap of [R-ORD-01 §1]); the two squared
cell radii are `((r + (r >> 31 & 15)) >> 4)²` for `outer` and `inner`
respectively — a divide by 16 rounded toward zero for negative values, then
squared. Both pairs are stored ([R-PATH-01 §9]'s dual-radius contract).

**The velocity marker's turn clamp — Established, refining [R-AIR-01 §8].**
The per-tick goal update computes `d = (int16)(commanded − bearing(velocity))`
and `lim = TurnRate >> 3` (the definition word, unsigned shift); the applied
delta is `d` when `−lim < d < lim`, `lim` when `d >= lim`, and `−lim` when
`d <= −lim` — inclusive at both ends, so a delta of exactly `±lim` is not
reduced. The horizontal velocity pair is then rotated by the negated delta
and the vertical component is zeroed; the update returns the position
*before* this tick's advance as the goal.

**The follower's per-tick service — Established, completing [R-MOV-01 §3].**
(1) With a payload installed, ask it whether the unit has arrived; on arrival
raise pending `0x20` on the owning record, ask the payload whether it is
persistent, and if not release it through the follower's owner. (2) Waypoint
pruning: while more than one point remains and the unit's committed cell is
within `dx² + dz² < 26` of the first point's cell (strict, cell domain), drop
the first point (shift the rest down), and when fewer than two remain clear
the follower's "has route" bit; every prune sets the "route changed" bit.
(3) With a payload installed, when the mover's blocked bit is set **or** fewer
than two points remain, arm the repath bit ([R-MOV-01 §7]). The point fill
that feeds steering converts the stored 16-bit cell pair of each point to
16.16 with Y zero and clamps the index to the last point. The route-release
notification zeroes the follower's four route words only when the released
route is the one it currently holds ([R-PATH-01 §8]).

### Closed — the class-layer restamp family, exactly [R-MOV-03 §3] (2026-08-29)

[R-DOC04-B] states the classifier and the revision pass. The stamp geometry:

* the layer is one 32-bit word per **cell column × 16-row band**, indexed
  `width · (z >> 4) + x`; a cell's 2-bit value sits at bit position
  `2 · (z & 15)`;
* a **rectangle restamp** clips the rectangle to the layer (origin at or
  above 0, extent at or below the layer's width and height, the extent being
  `origin + size + 1` exclusive) and rewrites each cell's two bits from the
  footprint classifier;
* the **footprint classifier** classifies the footprint rectangle as one
  block; when the result is *clear* (3) it additionally classifies the four
  one-cell-wide strips just outside the rectangle (the row above, the column
  to the right, the row below, the column to the left, each one cell longer
  than the rectangle at both ends) and demotes the result to *steep* (1)
  when any strip is not clear. This is the restamp-side form of
  [R-DOC04-B]'s contagion pass;
* the **request revision pass** temporarily writes the current tick into the
  requester's commit-tick field while it restamps, restamps the requester's
  own rectangle when its previous commit tick predates the old watermark,
  and — only when the watermark actually advanced — restamps every live unit
  with a mover whose commit tick lies in `[oldWatermark, newWatermark)`;
  the requester's commit tick is restored afterwards. The **release-time
  restamp** run by the scheduler on a finished request restamps the unit's
  rectangle when its commit tick predates the watermark.

### Closed — the model adapter's piece getters and setters [R-MOV-03 §4] (2026-08-29)

Section 4.6 names the adapter's get/set-position and get/set-angle; doc 03
([03 R-COMP-01 §4]) records their presentation effect as an inference. The
writes, exactly:

* **get translation lane / get angle lane** return the raw stored dword /
  word for `(piece, axis)`; no bounds check.
* **set translation lane / set angle lane** write only when the value
  changes; on a change they zero the piece's per-piece stamp word, set the
  model's rebuild flag, and, when the piece's *cache* flag bit is set, clear
  the model's cached-image word.
* **show/hide** (the draw bit) flips the bit only on a change, zeroes the
  stamp word and applies the same cache-conditional clear, but does **not**
  set the rebuild flag.
* **cache** and **shade** (the two other flag bits) are written
  unconditionally and reset a third model word each time.

This confirms doc 03's reading of the setters' second word as the cached-image
validity the cached-body path tests; the reader side stays doc 03's.

### Closed — the get-value case bodies and port 11's field [R-MOV-03 §5] (2026-08-29)

The twenty jump-table bodies of the value-reading switch compute exactly the
row expressions of section 4.4 ([R-COB-03 §2]); each was read against its
row and none differs. One naming precision: port 11's "definition height" is
the definition's **model bounding-box maximum Y** in 16.16 — the same field
the `BeginTransport` argument carries ([R-AIR-01 §9]) and the visibility
predicate compares against sea level (doc 03).

### Closed — queue helpers, the purge, and the static purge-survivor bit [R-MOV-03 §6] (2026-08-29)

* **Descriptor lookup.** A descriptor record is `table + id · 25` bytes; the
  static gate mask is a 32-bit word inside the record (section 3.1).
* **Find by identity.** The lookup of a queued order by descriptor identity
  walks the chain the descriptor's rear-segment flag selects (the same
  `0x40000` bit of the static mask that insertion uses) and returns the first
  record with that identity, or none.
* **Toggle remove-or-add.** The interface's toggling issue (the mobile-build
  toggle of doc 07 is one caller) walks the **front** chain for the first
  record matching the identity, the target when one is supplied, and the goal
  when one is supplied — `|goalX − recX| <= 0x100000` and the same on Z, i.e.
  within 16 world units per axis, inclusive; a match is unlinked from its own
  segment, tombstoned unless it is the front head, cleaned up and freed, and
  nothing is added; with no match the record is added through the ordinary
  counted insertion. Supplying no unit skips the search.
* **The purge.** One helper serves both purges of section 3.3: in
  *keep-survivors* mode it removes every front-chain record whose static-mask
  copy lacks **bit 2** (`0x4`); in *full* mode it removes every record of the
  front chain and then of the rear chain. Every removal tombstones unless the
  record is the front head, runs the cleanup of [R-ORDER-02 §2], and frees.
  The keep-survivors mode is what the damage-reaction auto-engage issue
  ([08 R-AI-01 §11], [R-STANCE-01 §3]) calls before inserting its attack; the
  full mode is the pump's cancel-all (code 7) and unit finalisation. **Static
  bit 2 is therefore the purge-survivor bit** section 3.3 names —
  `MakeSelectable`, `Wait`, `AttackUType`, `WaitForAttack`, `GetBuilt`,
  `BeCarried`, `Paralyze`, `SelfRepair` and `BuildingBuild` survive an
  auto-engage purge. This closes that bit's "no located consumer" entry of
  section 3.1.
* **An identity predicate.** A three-identity predicate returns 0 for
  `Attack_Chase`, `BeCarried` and `Cloak_Off` and 1 for every other identity;
  its only caller is the battle host's event pump (doc 01/07). **Unknown:**
  what the caller does with it; *decider:* the event pump's trace (doc 07).

### Closed — the observer node, and pending bit 0x10's producer [R-MOV-03 §7] (2026-08-29)

The order record's *target smart-reference* (section 3.2) is a 16-byte
**observer node** embedded in the record: a method table, the observed unit,
a link to the next node on that unit's observer list, and a handler
reference. Construction links the node at the **head** of the target's list
when the target is live (definition index nonzero); otherwise the node stays
unlinked. The record constructor sets the handler to the record itself,
clears `0x200` from the static-mask copy when no target was supplied and
`0x400` when no goal was supplied, and — when `0x200` is clear after that —
unlinks the node again, so a record whose descriptor does not carry `0x200`
never observes its target. Relinking to another unit splices the node out of
the old list and pushes it at the head of the new one; the record cleanup
splices it out.

The record's own method table has two entries: the first ORs its argument
into the record's pending word ([R-ORD-01 §6]'s "first method"); the second
is an empty stub. Every observer notification calls the first entry of each
listed node's handler with an event code, so **an event code is a pending
bit**: unit removal delivers `0x8` (target lost, [R-ORD-01 §6]), cloak
delivers `0x10000`, and the damage-intake observer notice of
[06 R-WPN-04 §2] delivers **`0x10`** — every order whose target has just
taken damage wakes with pending bit 4. That is the producer [R-ORD-01 §6]
could not locate, and it is why the guard handlers' `0x18` gate reads
"target lost or target hit". Doc 06's "which task types act on code 16" has
the same answer: all of them, identically, through the pending word.

**Correction to [R-ORD-01 §6].** Its third bullet said `0x10` "has no located
producer" and that the guards' `0x18` gate "is satisfied in practice only by
`0x8` and by their own deadlines". Both are superseded by the paragraph
above: the producer is the damage-intake observer notice.

### Closed — case bodies and fragments cited to their handlers [R-MOV-03 §8] (2026-08-29)

The ledger rows that are jump-table case bodies of already-closed handlers
were each read against the handler's contract and cited there; nothing new
was found. Three details worth stating because the closures name the outcome
but not the mechanism: the **free-pad predicate** of [R-AIR-01 §6] is "the
pad has no carrier and no unit in its cargo list has the queried attach-piece
index recorded"; the pad-landing leg that spawns `SelfRepair` requires the
pad's definition to carry both `isairbase` and `builder`, the pad to be
complete, and the lander's `(int16)health < maxdamage`; and the attack-run
marker's radius is `128 + random below 128` from one simulation draw.

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

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body — most under `R-<id>` headings — and are not restated here.

**Correction (2026-08-28, RWU-00-5).** Most bullets in this tail opened with
an open residual and then recited the surrounding closure — the OTA-FAC-01
bullet spent fifteen lines on established completion order before naming the
one Unknown, and the `StartBuilding` fourth-argument bullet was a closure
narrative with a residual appended. That is exactly backwards for a work list.
The recitals are deleted here only; [R-FAC-01], [R-FAC-01B], [R-FAC-01R],
[R-REV-02], [R-UNIT-06], [R-ORDER-02], [R-COB-01], [R-COB-02], [R-MOV-02A] and
sections 3.3, 3.8, 5.3, 7.2, 8.2 and 8.3 continue to own those findings.

**Correction (2026-08-28, RWU-04-1).** [R-MOV-01] closes three items that this
tail carried as open. The blocked/failed path-request "retry cadence and retry
count" bullet is deleted: the blocked-mover cadence is the follower's 60-tick
repath throttle with no retry count ([R-MOV-01 §7]) and the no-route side is
the order re-arm of §8.3, both now established — which also supplies the
decider `docs/SPEC_CONFLICTS.md` SC22 was waiting on (orchestrator to re-tag).
"Sea-floor behavior" is deleted from the hover bullet: there is no sea-floor
follower, only the ordinary four-corner terrain conform ([R-MOV-01 §8a]). The
"automatic replan" clause is deleted from the [R-MOV-02A] yield bullet, and
the mover-mode bullet is narrowed to modes `0` and `3` because `1` and `2` are
now named. Section 8.1's "vertical tolerance ... fixed-point threshold" claim
is withdrawn in the body ([R-MOV-01 §3]) and was never a tail item; no
replacement bullet is needed because the ground path has no vertical term.

### Simulation and identity

- Out-of-map and mode behavior of the placement validator outside the
  production path · §6.4 · static trace. Marked `TODO(question)`.
- Allocator and slot-reuse cleanup when a dead factory slot is reused · §3.8 ·
  static trace. Marked `TODO(question)`; reclamation and inheritance must not
  be invented.
- Complete lockstep packet ordering, replay state, state-hash contents, and
  resynchronization behavior · doc 08 · static trace. Out of Nanolathe's
  implementation scope (no multiplayer).
- Restore side of pending script state; pending path-request and pending order
  state are established as byte-exact · doc 08 [P1-13] · static trace.
- Same-tick visibility for unit creation callers other than the factory path —
  slot-relative reuse and order-created units · §3.8 [R-P0-09] · static trace.
- Factory egress residuals: whether every stock factory COB opens its yard
  before entering the build stance (so the state-2 exit test never idles on
  the factory's own `c`/`C` stamp) · §3.8 [R-FAC-02 §8] · stock COB census of
  `Activate`/`StartBuilding` for the yard-open port write.
- Aircraft product: first-goal geometry and climb after `Park` re-identifies
  itself as `VTOL_Move` at the product's own position · §10 [R-FAC-02 §8] ·
  static read of the zero-length `VTOL_Move` path in [R-AIR-01 §6] from a
  grounded start.
- Lifetime and cleanup of the product-side builder link after `GetBuilt` (the
  kind-18 link event's consumer) · §3.8 [R-FAC-02 §8] · static trace of the
  event sink's kind-18 handler and the finalisation reference walk.
- Retail confirmation of the composed factory handoff latency (a build that
  finishes under 300 ticks after attach dwells on the pad until tick 301)
  · §3.8 [R-FAC-02 §4] · one timed retail observation; the static composition
  stands regardless.
- Complete player category, side, ally, autonomy, and strategic-AI semantics
  · doc 08 · static trace.

### Orders and queues

- Which front-end setting or mission key writes the session option bit that
  admits every candidate in the shared target search regardless of `shootme`
  · §3.9 [R-SPEC-01 §5], [06 §3.2] · static trace over the option byte's
  writers (RWU-02-1). Until then Nanolathe treats the bit as clear.
- Identity of the 16-bit `explodeas` weapon field the HUD kamikaze ring
  halves (inferred `areaofeffect`) · §3.9 [R-SPEC-01 §1], [R-P0-11 §3] ·
  static trace matching the weapon parser's store to the ring drawer's load.
- Meaning of the one definition byte that gates the creation notification in
  the pre-built creation path, and what the emitter shows for a status kind
  whose caption text is empty (`selfdestructcountdown` 6 and 7) · §3.9
  [R-SPEC-01 §12], [R-SPEC-01 §13] · static trace.
- Per-phase operation-byte values inside the construction/factory handler
  family; the 68-descriptor handler set itself is closed · §3.1 · static
  trace.
- Consumers of the order descriptor's class parameter and of the unnamed
  gate-mask bits (statically: 1, 3–6, 8, 11, 16, 17, 19, 24; bit 2 is
  [R-MOV-03 §6], bit 7 is [06 R-WPN-04 §2]) · §3.1 [R-DOC04-C] · static
  trace. Marked `TODO(question)` at both sites; store the bytes opaque.
- Reader for the acknowledgement-group byte · §3.1 · static trace. Marked
  `TODO(question)`.
- Upstream producers of production-node wake mask 8 (Construction stopped)
  · §3.3, [R-FAC-01B] · static trace. Marked `TODO(T25)`; the handler
  semantics are closed and the producer must not be invented.
- Where the interface and network layers replace or cancel the front order
  · §3.3, doc 07 · static trace. The queue pump itself never does it.
- The standoff value bound by the attack-chase orbit substates; it is produced
  by the weapon-slot engagement-distance helper and remains inference · §8.3,
  doc 06 · static trace.
- The writer of bit 16 in the unit capability word, and the semantic name of
  the weapon-slot control byte's bit 4 · §3.3, [R-ORD-01 §6] · static trace
  of the capability word's writers. (Pending bit `0x10`'s producer is closed:
  [R-MOV-03 §7].)
- Producer of `GetBuilt`'s wake bit `0x8000`, and therefore whether the
  11-tick nanoframe decay of [R-ORD-01 §5] runs while a builder is working
  · [R-ORD-01 §5], doc 05 · static trace of the work helper's callers, or a
  timed build measured against the formula.
- `SelfDestruct` with a `selfdestructcountdown` of 6 or 7 indexes past the
  six-entry countdown caption table · [R-ORD-01 §2] · the FBI parser's clamp
  on the 3-bit field.
- The TDF key behind the feature definition byte that bounds the reclaim and
  resurrect spray height draw · [R-ORD-01 §5] · the feature parser's key list.
- Producer that sets a unit's engagement-target link — the ward-side reference
  both guard handlers attack toward · [R-UNIT-06 §1] · static trace. Not found
  in the bounded decompiled set or by instruction-pattern scan.
- Whether the route-release event fires for a route that was never published
  (unreachable goal as rebind loop versus silent stall) · [R-ORDER-02 §1] ·
  static trace of the movement wrapper's per-tick release-callback states.
- Meaning of the unit state word's low two bits, which `Standby_Mine` compares
  against `1` on the scanned target before it self-destructs · §2.4,
  [R-STANCE-01 §3] · static trace of the writers of those two bits.
- Which renderer path selects frames 4 and 5 of the stance buttons' GAF
  entries; the panel writes only values 0–3 into the gadget status word and
  takes a separate gray path for 4 · [R-STANCE-01 §1], doc 07 §4 · static
  trace of the button draw routine.
- Construction and economy carry, and worktime-under-one-tick behavior
  · doc 05 · static trace.
- What the interface's event pump does with the three-identity predicate
  (`Attack_Chase`, `BeCarried`, `Cloak_Off` → 0) · [R-MOV-03 §6], doc 07 ·
  static trace of that pump's consumer.
- What the pump does with a spawned record of identity 0, which
  `VTOL_SeekGuard` can produce by spawning an unchecked code-7 resolution
  · §3.1, [R-ORD-02 §3] · static read of descriptor 0's handler pointer.

### COB

**Correction (2026-08-29, RWU-04-5).** Three bullets are deleted here because
[R-CB-01] closes them: the `Aim*` closure-target identity (it is the first
entry of a fixed two-entry dispatch table installed into all three weapon
slots at unit creation, storing the literal `1` into the slot's aim-ready word
on a nonzero delivery — [R-CB-01 §6]; document 06 section 3.4 carries the same
residual and the orchestrator should retire it there too); the semantic unit
of the footprint-path `SetSpeed` argument (it is the summed plot-cell metal
byte plus one per covered footprint cell, the metal-extractor rate input —
[R-CB-01 §5]); and the serialization of the unassigned `Killed` variant cell
(it round-trips the death handler's own uninitialized frame slot through the
query and is masked to four bits — [R-CB-01 §7]; a Nanolathe substitute is a
sanctioned divergence, not an open question). Three new bullets replace them.

**Correction (2026-08-29, RWU-04-4).** Four bullets are deleted because
[R-COB-04] closes them: the reserved opcode's over-depth behavior (a
four-word frame temporary; beyond it, undefined — [R-COB-04 §6]); the
initial content of script statics (raw `malloc` memory, no fill —
[R-COB-04 §7]); the scriptless-unit crash policy (retail faults at unit
creation in the weapon-slot initializer's first synchronous query —
[R-COB-04 §8], which corrects [R-COB-01 §1]'s "nor crashes at creation");
and the five VTOL work twins ([R-ORD-01 §7]). The allocation-failure bullet
is narrowed: the statics allocator is the C runtime's `malloc`, whose
failure path is the runtime's new-handler loop, so the only remaining
question is the engine's own pool allocator. Two new bullets are added.

- The first committed retail ARMCK pose, and whether its authored waiting
  `Create` work advances before that publication · [R-P28-COB-01R] · manual
  retail observation (the paired settling probe). Marked `TODO(question)`.
- Deterministic fault policy when the engine's fixed-size pool allocator
  (thread pool, order records, path markers) refuses — the head-insert sites
  dereference a null record ([R-ORD-01 §1]) and the VM's thread pool wedges
  ([R-COB-01 §1]); which of these retail reaches first under exhaustion
  · §4.6, §3.9 · static trace of the pool allocator's failure return at each
  caller. Marked `TODO(question)`.
- Identity of the pooled effect class 7 (parameter 15) that every above-sea
  bitmap explosion spawns, and of the smoke and fire trail classes the debris
  draw pass emits · [R-COB-04 §2], [R-COB-04 §4], doc 03 · doc 03's effect
  class census (RWU-03-4). The gates and parameters are established here.
- Whether the effect-pool records a shatter claims but cannot pair with a
  fragment (fragment table full) advance stale animation words · [R-COB-04 §3]
  · static trace of the effect sim pass against a record with stale name
  words, or manual retail observation of a mass death with shatter flags.
- Caller of the second entry of the weapon slot's aim dispatch table — a
  four-argument stub returning zero, with no located call through that slot
  · §5.3 [R-CB-01 §6] · static trace of every indirect call whose target is a
  load from a weapon-slot word.
- Which authored order form the three `EndTransport` producer sites outside
  the air-landing executor serve; the wake flags (immediate at two of the five
  sites, deferred at three) are established, and [R-AIR-01 §6] already places
  the landing executor's pair (phase 1 immediate, phase 6 deferred) · §5.3
  [R-CB-01 §2] · static trace of the order descriptor table's handler column.
- Whether a reused unit pool slot's surviving weapon-slot flag byte can make
  the creation-time hold helper raise a `TargetCleared` for the previous
  tenant before the new unit's script is bound · [R-CB-01 §4] · static trace
  of the unit allocator's clearing of the record before initialization.
- Consumer of the script-touched marker · §4.7 [R-P0-10] · static trace.
  Write-only in the bounded census. Marked `TODO(question)`.
- Semantic name of the busy bit; the transport-admission half is closed (no
  busy-bit test in the nine gates) · §4.7 [P1-05] · static trace. Marked
  `TODO(question)`.
- Name of the engine-driven cloak-family bit (bit 2 of the first state byte)
  and its writers outside the edge machine · §4.7 · static trace, naming only.
  Marked `TODO(question)`.
- Meaning of the two-bit field the `attach-unit` third value writes on a record
  the cargo unit points at · §4.7 [R-COB-03 §5] · static trace, naming only.
  Every shipped call site passes 0, so the field is always cleared. Marked
  `TODO(question)`.
- Yard-character class matrix inside the yard-open admission gate · §4.7,
  `[fmt tnt]` · static trace. Marked `TODO(question)`.
- Whether mission or third-party content depends on a bare `get UNIT_HEIGHT`
  returning nonzero; retail returns 0 with a zero-filled argument slot · §4.7
  · asset census. Marked `TODO(question)`.
- Unnamed save fields, and complete restore behavior for pending calls, waits,
  and signal masks · doc 08 · static trace.

### Terrain and pathfinding

**Correction (2026-08-29, RWU-04-2).** Six bullets are deleted here because
[R-PATH-01] closes them, and one is deleted because its premise was false.
Closed: the heuristic-weight "settings string" (there is none — the base is the
compiled-in `0x18000` and the developer console command `Search` is its only
writer, [R-PATH-01 §10]); "semantics of terrain state one versus clear state
three" and the "no per-edge terrain-state cost" bounded negative (state 1 costs
30 more per step, [R-PATH-01 §3]); the BUILDING blocker channel (the
occupant-age gate, [R-PATH-01 §2]); Class-D goal usage (the class is an air
work point with the base's null goal interface and never reaches the search,
[R-PATH-01 §9]); the four unread follower slots and the route-acceptance rule
([R-PATH-01 §8], moved here from "Ground movement"); and the `PFSTATE`/`PFABLE`
question, whose premise was wrong — they are the display object's page-flip
state and page-flip availability, printed by the developer overlay, and have no
relation to the route follower (doc 03 owns them; noted below for the
orchestrator).

- Order-layer identity of each goal-family call site: which order types
  construct a point, annulus or rectangle goal, and with which radii · §7.2,
  §7.4 · static trace. The three families' arithmetic is fully established
  ([R-PATH-01 §9]); only the per-order census is open.
- Full static feature and yard-map interaction with the class layer's feature
  gate · §7.1 [R-DOC04-B] · static trace. (The "owner-mask" half of this bullet
  is deleted: that word is mapping memory, [R-PATH-01 §2].)
- Retail perimeter-candidate ranking and expansion at a build-site anchor; the
  half-extent expansion is Nanolathe's placeholder for the established
  stop-outside-the-footprint outcome · §7.4 · static trace. Marked
  `TODO(question)`.
- Out-of-bounds goal handling for every order type; the search's own behaviour
  is established (an out-of-bounds enumerated cell is unmarked but still aims
  the ray; an out-of-bounds start is a `0x200` reject) but which order types can
  produce one is not · §7.4 [R-PATH-01 §4] · static trace.
- Heap OOM policy and integer overflow behavior; the route caps (20 published
  points, 64-point reconstruction ring, 3 saved waypoints) and the node-pool
  growth rule are established · §7.3 [R-PATH-01 §1] · static trace.
- Semantic name of the order-record flag that suppresses the follower's
  synthetic straight-line route; it is set by one disposition branch of the
  queue pump on an order being taken down · §7.3, §3.3 [R-PATH-01 §8] · static
  trace of that branch. Marked `TODO(question)`.
- Which heuristic-weight tier a shipped session actually settles on. The tier
  rule, the divisor's identity (the session per-player unit limit) and the base
  are Established; the steady-state tier is a Supported inference from the
  iteration rate · §7.3 [R-PATH-01 §6] · manual retail observation, or a static
  trace of the iteration count under a known unit population.

### Ground movement

**Regenerated (2026-08-29, RWU-04-9).** Three bullets are deleted because
[R-COLL-01] closes them: the outer yield/retarget/sidestep/wait-queue owner
(none exists; resolution is sweep order plus the follower's 60-tick repath and
the class layer's 30-tick occupant age, [R-COLL-01 §7]); feature, building
and map-edge collision (all validator rejects with one blocked response,
[R-COLL-01 §2][R-COLL-01 §3]); and the blocked flag's stale persistence
(established by writer census, [R-COLL-01 §5]).

- The emergent head-on deadlock timing — first divergent route between 30
  and 60 ticks after the block — is a Supported inference from two
  Established mechanisms · §8.2 [R-COLL-01 §7] · manual retail observation
  of two units ordered through each other in a one-cell corridor.
*(The follower's four unread virtual slots, the route object's protocol, and
the route-acceptance rule are closed by [R-PATH-01 §8]. The `PFSTATE` /
`PFABLE` bullet is deleted: they are the display object's page-flip request
state and page-flip availability, not follower state — doc 03 owns them.)*

### Hover and VTOL

- Ordinary gameplay producer of mover mode `3` · §9.1 [R-AIR-01 §3] · static
  trace of the save/network stream writer that supplies the reduced flight
  controller's 2-bit mode field (with RWU-08-4). Modes `0` (attached/parked),
  `1` (grounded) and `2` (airborne) are named and their writers censused by
  [R-MOV-01 §8] and [R-AIR-01 §3].
- Wake and SFX-piece mapping beyond the `setSFXoccupy` five-band classifier
  · §9.2, doc 03 · static trace. (The classifier's test order is
  [R-MOV-03 §1].)
- Identity of the held key whose release gates the `+BigBrother` 90-tick
  selection cycle in the sweep tail · [R-MOV-03 §1], doc 07 [R-CAM-01 §2] ·
  the camera held-key census.
- Whether the hover bob's per-corner perturbation — at most two height units,
  [R-MOV-01 §5] — can carry a hovering unit's committed integer height across
  the sea-level, waterline, or model-bottom thresholds that the band
  classifier, the below-water half-speed branch, and water damage compare
  against · §9.1, §9.2 [R-MOV-01 §5] · a bounded numeric argument over the
  stock `waterline` and `modelBottom` values, or manual retail observation of
  a hovercraft parked on a shoreline. The wall-clock read and its write path
  into the authoritative height word are established.
- Writer and configured value of the rate field that scales the wall-clock
  animation counter the hover bob samples · doc 01, §9.1 [R-MOV-01 §5] ·
  static trace.
- Can-fly terrain bypass conditions for orders outside the established flight
  selection in the mover fan-in · §10.1 · static trace.
- Carrier collision beyond the shared-mover commit rules · §10.2 · static
  trace. Landing has no descent rate: the vertical limit of §10.1 is the only
  one, every `0x10`/`0x30`/`0x40`/`0x80`/`0xA0`/`0x140`/`0x150`/`0x1E0`/`0x3C0`
  value is a per-leg horizontal arrival radius ([R-AIR-01 §4]), and pad
  reservation and the loiter retry are closed by [R-AIR-01 §6].
- The sign convention of the bearing helper on screen · §10.2
  [R-AIR-01 §8] · manual retail observation. (The `AirToAir` third leg is
  closed: it spawns `VTOL_Evade`, [R-ORD-02 §5]; `VTOL_Follow`/`VTOL_SeekGuard`
  are closed in [R-ORD-02 §3].)
- Construction target-eligibility gates for air construction orders; the
  `150`-tick recurrence at `builddistance << 16` with offset `0xDB6E` and
  build power `work/30` per tick is established · §10.3 · static trace.
