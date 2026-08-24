# Retail executable specification: units, orders, scripts, and movement

This document records the current clean-room behavioral contract recovered from the retail executable. It is deliberately written in terms of logical state and observable behavior. It does not reproduce executable addresses, binary offsets, decompiler names, or implementation code.

Evidence labels:

- **Established fact** means the behavior is directly supported by static control and data flow in the retail executable, or by a bounded call, read, or write census over it.
- **Supported inference** means the behavior follows from several direct observations, but one semantic link or caller remains unresolved.
- **Unknown** means that the available retail evidence does not close the behavior. An implementation must keep the gap explicit rather than inventing a rule.

The corrected notes and root-correction ledger take precedence over earlier notes. In particular, the 30 Hz tick, the current path-search reconstruction, the current COB drain root, and the current visibility/effect phase identities are used here. Earlier notes marked superseded or quarantined are not evidence.

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
local `Killed` query. Consequences: a deferred callback queued before the
normal drain executes in that same visit; a deferred callback queued after it
normally waits for the next visit, except that any later immediate-start
callback on the same virtual machine performs an all-slot delta-0 drain that
can execute it earlier [P0-09]. Damage callbacks queued by the post-unit
projectile phase therefore normally land on the next visit, while damage packets
processed during event ingress can queue theirs in time for that tick's normal
pass. Capture is synchronous before trigger polling, build-complete product
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

**Supported inference:** A player's side byte is used for same-side filtering, projectile ownership, and callbacks. Full alliance semantics are not closed by the unit notes; ally checks must remain a dedicated predicate rather than simple side equality.

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

**Established fact:** Save reconstruction preserves unit slot identity and repairs cross-unit references after the units are reconstructed. A failed unit allocation can cause the saved record to be skipped.

**Unknown:** The exact maximum unit count is not resolved. Notes contain two competing limit derivations; implementations must not encode either as a final retail constant without further evidence.

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
| 9 | set a completion flag; if the record is last, reset its phase and set the same randomized deadline `tick + 30 + random below 15` (range 30 to 44, same bound as code 3, resolving the earlier conflict where one description used 30) [P0-08][SC8]; otherwise unlink and free |
| above 9 | delegate to the order expiry helper and return — helper unlinks, cleans, and frees the single record with no random draw and no whole-queue cancel; whole-queue cancel is exclusively code 7 [P0-08] |

Consequences a reimplementation must preserve: one pump call can cascade a
record through several phases in the same tick until a waiting or blocked code
appears; a handler that returns an out-of-range phase code at or below 9
follows that code's row, while above 9 it takes the single-node expiry helper;
waits are quantized to between 30 and 44 ticks for both code 3 and code 9 last
[P0-08][SC8]; and goal writes and slot binds made by a handler are visible to
later handlers in the same cascade, while freed records are invisible
immediately.

**Supported inference and Unknown — queue waits and producer assignment
[P0-07][P0-08]:** The bodies for interrupt wake bits 2 (cancel-current) and 8
(Construction stopped) are known — refund arithmetic plus a kill packet for the
first, decrement and free for the second — but bounded scan found no writer for
those wake bits, so producers remain `TODO(T25)` and must not be invented. The
bounded census for the small class parameter above (3901 boundaries) similarly
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
the list for every modifier.

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

### 3.4 Command resolution

**Established fact:** Player and network commands do not name descriptors
directly. A resolver takes a **command code from 1 to 14**, the acting unit,
an optional target, and an optional ground position, and produces a canonical
command *name*, which is then looked up to obtain the descriptor identity. A
failed capability gate produces the reject identity.

| Code | Meaning | Capability gate | Resolves to |
|---:|---|---|---|
| 1 | contextual, from a right-click | delegates to the codes below | hostile and able to attack becomes an attack order; a damaged or unfinished friendly becomes repair or build assistance; a transportable target becomes a pickup; a followable target becomes follow; a feature at the position becomes resurrect when eligible, otherwise reclaim; otherwise a move |
| 2 | move | can-move | a dead unit target becomes a queued move; a hostile target with the capture or reclaim capability becomes capture or unit-reclaim; a friendly build target becomes build assistance or repair; a landing pad becomes landing; a carriable target becomes pickup; a followable target becomes follow; otherwise ground or air move |
| 3 | attack a unit | can-attack | suppression for the non-air special case; the four air-attack variants chosen by weapon and target class; the kamikaze variant for a unit flagged for it; the no-move variant for a structure; otherwise the chase attack |
| 4 | special attack | special-attack capability | the special attack |
| 5 | unload | can-unload | ground or air unload; a pad target becomes landing |
| 6 | load or pick up | target passes the carriable test | ground or air pickup |
| 7 | guard or follow | can-guard, target friendly | ground or air follow |
| 8 | assist or repair | target is reachable by a nanolathe | build assistance while the target is unfinished, otherwise repair |
| 9 | patrol | can-patrol | a patrol with no target becomes the queued patrol; a builder with the repair-patrol capability becomes the repair patrol; otherwise ground or air patrol |
| 10 | internal | — | an internal command whose label is not established |
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

**Guard assistance triggers [P0-08].** The follow/guard handler evaluates,
top-down on every visit: (a) **build assist** — if the ward has an active
construction op that is friendly per the diplomacy byte and not already latched
in the guarding unit's dedup array, enqueue assistance toward that op;
(b) **auto-fire while holding position** — for each weapon slot with auto-target
enabled whose weapon is not command-fire-only, acquire a candidate and
range-gate it, binding the slot on success, each candidate deduped by id latch;
(c) **repair assist** — when the ward is damaged and the guard can repair,
resolve and enqueue the correct repair order for the ward; (d) **join the ward's
build** — when the ward's own front order is a nanolathe-class build elsewhere,
enqueue help-build toward that order's target; and (e) otherwise **follow
maintenance** — refresh a banded goal around the ward and wait 30 ticks. Each
assist path retries on the 30-tick cadence behind its dedup latch. Per-order
leash and orbit vtables share the same deadline and arrival contract above:
dedup latches prevent immediate re-enqueue, and strict `dist <= 2` with
`abs diff < 3` guards the banded-goal transition [P0-08].

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
Standing-move and standing-fire bits copy from builder to product only when
both units carry the building-class state word bit and NEITHER carries the
auto flag; the experience word copies only for computer-player-owned builders.
The full production lifecycle, refund arithmetic, and completion transition
belong to document 05.

**Supported inference:** The construction operation table is data-driven and
maps an operation byte to handlers. The precise meaning of every operation byte
is not recovered, so an implementation should keep unknown operations
observable and reject or preserve them rather than assigning new behavior.

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
| `w secs[,n]` | Wait for secs×30 ticks carrying trailing integer n |
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

## 4. COB loader, VM, threads, and script timing

### 4.1 Loading and binding

**Established fact:** A compiled script file is read whole into one allocation and relocated in place; its header, tables, and record layout are specified in document 02. The loader binds one compiled script object to each unit definition, caches it by file, and stamps it with the content checksum that save/load validation later compares.

**Established fact:** Each live unit receives a COB VM instance. The VM has eight execution threads, per-thread status/PC/stack/sleep/wait/caller/signal state, static variables, and per-piece animation state. Thread stack depth is limited to ten values by the retail layout; no safe expansion behavior is demonstrated.

**Established fact:** The loader allocates script statics and piece states when binding. Save/load validates the expected blob size and a cache/signature value. Static variables and piece animation state are represented in the save payload. Fractional movement deltas are not saved; the next tick resumes from integer state.

**Unknown:** Some anonymous save fields and exact failure behavior of allocation or corrupted piece indexes remain unresolved. Retail may abort through its allocator where a clean implementation should terminate the affected script deterministically.

### 4.2 Thread scheduler

**Established fact:** The unit phase drains each unit's eight script threads. The interpreter runs before per-piece interpolation in that unit's script drain. A move or turn issued by a script therefore affects the same tick's interpolation; a wait that becomes satisfied during interpolation is observed by the next drain.

**Established fact:** Thread states include idle, running, waiting for turn, waiting for move, sleeping, and waiting for a called script. Signal masks can terminate or suppress matching threads. Calls block the caller until the callee returns.

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
A piece-state entry is addressed as `axis + piece * 19` inside a 76-byte-stride
array.

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
| 11 | unit height | the **own** definition's height value; takes no unit argument | — |
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

**Established fact:** Each piece has independent move, turn, spin, and acceleration state, plus busy markers and dirty flags. Valid angles cover a 16-bit circle. A special all-bits marker means continuous spin rather than a valid angle.

**Established fact:** Piece speed is divided by the VM tick denominator of 30 using signed integer division that truncates toward zero. There is no fractional remainder carry. A positive or negative script speed smaller than 30 can produce a zero per-tick step while still setting the operation dirty/busy state for the current cycle.

**Established fact:** Move and turn operations snap on inclusive arrival. Turn uses shortest-arc logic; exactly opposite angles use a deterministic sign tie. Spin acceleration clamps on reaching or crossing the target speed. Stop-spin with a sub-tick deceleration becomes an immediate stop.

**Established fact:** Sleep converts the script duration as `trunc(30 * milliseconds / 1000)` and stores it as the thread's timer. On every later entry the guard subtracts the tick delta first and wakes the thread only when the result is at or below zero. **A sleep therefore occupies its truncated tick count plus exactly one guard decrement, so its minimum latency is one tick, not zero** — a sleep of 33 milliseconds truncates to zero ticks and wakes on the next drain's guard. A zero or negative duration behaves the same way.

Engine wake passes run all eight slots with a tick delta of zero, so they never advance a timer, but they do wake any thread whose timer is already at or below zero.

**Established fact:** Drain order is: execute all eight threads in fixed slot order, then interpolate all piece axes with the same delta. Immediate move and turn operations commit during interpretation and are visible to later script slots and later simulation phases.

A wait therefore observes an arrival one guard after the interpolation that produced it. There is a **slot-order effect**: when the thread that issued the motion occupies a higher slot number than the waiting thread, the waiter has already been guarded that tick and sees the arrival one tick later still. The drain has no second scan, so this asymmetry is part of the contract.

**Established fact:** A synchronous query nests a full interpreter run inside its caller. Re-entrancy is possible and retail places no guard against querying a unit that is mid-drain.

**Supported inference:** Model-coordinate handedness and any one-time 3DO conversion belong to model loading, not per-tick COB arithmetic. The script axis mapping itself appears direct.

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

**Established fact:** On a normal-kind damage packet applied to an active,
not-yet-dying victim, health is subtracted FIRST, then `HitByWeapon` starts
asynchronously with two arguments `(cos(dir)·400, sin(dir)·400)` where `dir`
is the packet direction byte shifted left by eight into the 65536-domain, and
`TakeDamage` starts independently immediately after with one argument, the
post-hit health percentage `clamp(health·100/maxHealth, 0, 100)` computed as
an unsigned division of the product. Either starter can fail separately.
Heal and paralyze kinds skip this pair entirely (the paralyze kind builds a
paralyze order instead); lethal damage against a movement-category-1/2 victim
sets the death latch and returns without any callbacks.

**Established fact:** Local authoritative death runs a synchronous four-cell
`Killed` query with outputs severity and variant before the death packet is
built:

```
severity = ((−health · 100) / maxHealth + priorSample) / 2   // unsigned divide
severity = clamp(severity, 1, 100)
```

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### 5.2 Movement and medium callbacks

**Established fact:** Edge-triggered callbacks include StartMoving, StopMoving, MoveRate1, MoveRate2, MoveRate3, and setSFXoccupy with a medium-band value. Start/stop callbacks are issued when movement transitions; rate callbacks are issued on movement-tier changes; occupancy callbacks are issued only when the computed band changes.

**Established fact:** The movement tier is a signed, inclusive threshold
classification of one 32-bit magnitude word against two definition
thresholds: category 1 iff `magnitude <= A`; category 2 iff `A < magnitude <=
B`; category 3 above `B` — all comparisons signed 32-bit. Category 0 overrides
when the mover inhibit bit is set, the unit is attached to a carrier (carrier
dword nonzero), or both magnitude words are zero. The category is cached in
two bits of the unit's class/state word and an unchanged category emits
nothing. On change: into category 0 from nonzero issues `StopMoving`; into a
nonzero category from 0 issues `StartMoving` FIRST and then the matching
`MoveRateN`; other nonzero-to-nonzero changes issue only `MoveRateN`. All are
immediate wake-flag starts, so the `StartMoving` drain — all eight slots at
delta 0 plus one piece pass — forms a barrier between it and the `MoveRateN`
that follows; the cache update is the final write.

**Supported inference:** Wake effects and medium bands are script-authored behavior. The engine does not need a separate hard-coded wake renderer to reproduce the callback contract.

### 5.3 Weapon and query callbacks

**Established fact:** The engine invokes QueryPrimary, QuerySecondary,
QueryTertiary, AimFrom*, SweetSpot, and QueryNanoPiece synchronously.
AimPrimary, AimSecondary, and AimTertiary are asynchronous callbacks with
heading/pitch arguments. Successful normal, ballistic, and vertical-launch
weapon spawners invoke FirePrimary/Secondary/Tertiary and RockUnit; the
dropped-family inline allocator does not. Burst clones do not rerun the root
Fire/Rock callbacks.

**Established fact:** `AimPrimary`/`AimSecondary`/`AimTertiary` carry unsigned
16-bit heading then unsigned 16-bit pitch (arity 2). Two issue forms exist,
both preceded by clearing the slot's aim-state word to 0 and both storing the
commanded angles in the weapon-slot record. The ballistic default computes
relative heading as bearing-to-target minus unit heading and pitch from the
ballistic solver; **a solver returning the `-0x8000` pitch sentinel suppresses
the Aim start entirely**, otherwise the start carries `heading & 0xffff` then
`pitch & 0xffff`. The fixed-forward branch — a weapon-record flag bit set,
the aim issue bit clear, and no live tracked target (target inactive or the
slot's adjacent status word nonzero) — starts with `(0, 0)`, the fixed-forward
heading. After either start the engine emits a network event packet
`{u16 unitId, u16 slot, u8 arity=2, heading, pitch}` behind a global option
bit and sets the weapon flags byte's issue bit.

**Established fact:** `RockUnit` follows its matching `Fire*` start in the
same producer with arity 2 and arguments `(-cos(rel)·800, -sin(rel)·800)`,
where `rel = (int16)(commanded barrel direction − unit heading)` evaluated
through the shared sine table of section 5.1 with round-to-nearest products;
both signs are negative and there is no completion receiver.

**Established fact:** `StartBuilding` has an argument-less edge form, issued
on the cached building-bit rising edge, and a slot-form heading variant that
resolves the name to a weapon slot and starts THAT slot directly with one
argument `heading & 0xffff`, plus its network event; the slot form sets the
production record's StopBuilding-pending flag consumed by cleanup (section
3.3).

**Established fact:** Engine-issued value callbacks convert exactly:
`SetDirection` (guarded on a positive definition float field and a nonzero
global mover-active word) passes a zero-extended 16-bit direction word;
`SetSpeed` from the general update passes a signed dword shifted left by 4;
the second `SetSpeed`, from the footprint path (guarded on a different
positive definition float), passes the 16-bit sum over covered footprint
cells of each occupying unit's size byte plus one — semantic unit not
established; and `SetMaxReloadTime`, issued after Create so it lands outside
Create's own immediate drain, scans all three weapon slots for the maximum
authored reload and reports `trunc(maxReload · 1000 / 30)` — reload ticks
converted to milliseconds. None of these adapters deduplicates; suppression
can only live in the producers.

**Established fact:** Query seeds are exact: the synchronous four-output
`QueryTransport` pre-seeds output cell 0 to `-1` (remaining outputs default 0
and are excluded from copy-back), so a missing script leaves `-1`, the
root-piece fallback, which is later consumed as the attachment piece index.
Every air-transport selection site seeds all four `QueryLandingPad` outputs
to `-1`, tests candidate pieces in cell order against validity/availability,
accepts the first pass, and keeps `-1` otherwise; some paths query the
carrier first, then the transported unit.

**Established fact:** The aim-ready handshake: the producer clears the aim
state AND sets the weapon-slot issue bit immediately after starting `Aim*`
with the embedded completion receiver; the issue bit clears when target
acquisition fails and gates re-issue (a new `Aim*` starts only while the bit
is clear). The adapter invokes the completion receiver ONLY on an explicit
script return, passing the popped return value; signal termination and
abnormal termination never invoke it. A zero delivery has no effect while any
NONZERO delivery marks the weapon aim-ready — so name absence, thread-pool
exhaustion (both deliver zero), or an authored zero return each leave the
weapon unable to fire. The fire path entered with the issue bit set
additionally consults a per-weapon permission function before firing; the
issue bit alone authorizes nothing.

**Established fact:** The run-script network dispatch (incoming packet case
`0xE`) resolves the `u16` unit identifier at packet offset +1 through the unit
table (requiring the active bit), then starts an authored function with:
identity = SIGNED 16-bit script slot from packet +3 (negative or out-of-range
rejected); receiver = none; deferred mode; arity = unsigned byte from packet
+5; and four 32-bit dwords from packets +6/+10/+14/+18. All four physical
values are always written to window words 0–3 but only the byte arity sets
the logical top (`depth = arity − 1`). There is no fixed callback name for
this path.

**Unknown:** The closure object installed as the `Aim*` completion receiver
and the exact write it performs remain unlocated; the zero/nonzero readiness
grant itself is established above. Behavior when all eight script slots are
occupied is established in section 4.3 and differs by starter.

## 6. Terrain and movement prerequisites

### 6.1 Terrain classification

**Established fact:** Movement profiles are movement class records compiled from `CLASS` sections. Each class reads eight keys in parse order — `FootPrintX`, `FootPrintZ`, `MaxWaterDepth`, `MinWaterDepth`, `MaxSlope`, `BadSlope`, `MaxWaterSlope`, `BadWaterSlope` — where `FootPrintX/Z` default 0, depth and slope fields default to the class's prior value (preserved), and `BadSlope`/`BadWaterSlope` default to half (`>>1`) of the `MaxSlope`/`MaxWaterSlope` value just read. Three unsigned-byte clamps then run unconditionally on every class: MaxWaterSlope caps MaxSlope, the resulting MaxSlope caps BadSlope, and MaxWaterSlope caps BadWaterSlope. Stock ship data only authors `MaxWaterSlope = 255` for hover classes (`TANKHOVER3/4`); the other thirteen classes omit it yet remain land-passable, so the stock movement template must carry a large `MaxWaterSlope` (255) before parsing so an omitted key preserves 255 and the clamps are identity — `TODO(question)` for the template writer. Nanolathe currently gates the first and third clamps on whether `MaxWaterSlope` was authored; this is a deliberate, install-compatible divergence retained with `TODO(question)` until the template initialization is proven.

**Established fact:** The map terrain grid uses fixed-size attribute cells. The plot expansion derives per-cell MinHeight and MaxHeight as the minimum and maximum of up to four height bytes (cell, east, south, southeast, with edge guards) — slope is computed from these derived values, not a single sample. Height queries use bilinear interpolation of the four corner heights with low-four-bit fractions and signed-bias correction.

**Established fact:** A movement profile classifies a footprint rectangle against map bounds, blocking features, terrain height span, sea level, slope, and water-depth thresholds. The footprint validator aggregates `min of mins` and `max of maxes` across the rectangle, selects the water-vs-land slope branch by whether the footprint is entirely above water (sea level at or below the footprint minimum chooses movement class MaxSlope, otherwise MaxWaterSlope), and tests passability with strict `<` (`slope == limit` and depth == limit pass). `BadSlope` and `BadWaterSlope` do not block in the validator — they are soft cost tiers.

The classifier yields three terrain states:

- blocked;
- passable but steep/edge-conditioned;
- clear.

The path reader adds a fourth state for building occupancy. The steep and clear values are both passable to the current search expansion; the notes do not establish a separate per-edge cost for them.

**Established fact:** Water legality is folded into profile thresholds by comparing terrain against sea level. There is no independently proven water-cost table in the path expansion.

**Established fact:** A packed two-bit terrain layer is stamped for profile rectangles. Dynamic placement/removal causes rectangle restamping and revision updates. Building occupancy is an overlay tested separately from the packed terrain value.

### 6.2 Footprints and yard maps

**Established fact:** Footprint dimensions are baked into profile terrain stamps. The path search validates the unit's center cell; it does not sweep a footprint at each path edge. Placement validation separately uses the unit yard map and footprint rectangle.

**Supported inference:** This explains why a path can be geometrically valid while a later placement validator rejects the exact build position. The two checks must remain separate.

## 7. Ground path search

### 7.1 Grid and passability

**Established fact:** Ground path search uses the TNT attribute-cell lattice,
with one cell covering 16 map pixels. Waypoints are generated from cell
coordinates plus the movement profile's half-footprint bias.

**Established fact:** The eight neighbor directions are visited in this order: north, northwest, west, southwest, south, southeast, east, northeast. The first expansion is deliberately wide: it attempts nine entries, which covers all eight directions plus one harmless duplicate. Subsequent expansions use a directed five-entry fan centered on the parent travel direction. The exact duplicate-direction position (which direction is duplicated) and the start fan's centre direction provenance are `TODO(question)`.

**Established fact:** Diagonal movement is endpoint-only: only the destination cell's stamped terrain and building mask are tested, not both cardinal corner cells. A footprint is not swept during expansion because the profile stamp already marked cells that would intersect static blockers. Whether duplicate neighbours are suppressed before or after cost computation and the greedy ray's exact bidirectional meet rule remain `TODO(question)`; debt-array layout and the heuristic-scale settings string value are also `TODO(question)`.

### 7.2 A* state and costs

**Established fact:** The search maintains open/closed status, parent direction, a heap, and a packed coordinate per node. The heap key is `f = g + h`. Equal keys preserve insertion order because the heap compares strictly less, not less-or-equal. Equal `g` values do not replace an existing parent.

**Established fact:** Cardinal steps cost 16 and diagonal steps cost 22. A direction-change table adds turn penalties of 0, 40, 60, 80, 100, 80, 60, and 40 for the eight directional differences. The fixed initial penalty of 30 applies while the heap holds at most one entry (the start expansion), and the short-run penalty of 75 applies when the parent chain's straight-run length is below five and a parent exists.

**Supported inference:** The eight-entry table is a turn-difference table, not a terrain-state table. The symmetric values and the absence of a terrain branch in the expansion support that interpretation.

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
seeding, a greedy bidirectional ray walk stores the minimum scaled h seen
along its frontier into a single slot that is never updated again. During
expansion, an opened neighbor whose scaled h is at or below that threshold
receives open-plus-goal status, and popping such a node terminates the search
and reconstructs — so EVERY opened cell within the tolerance region is an
acceptable route endpoint, not just enumerated goal cells. Enumerated goal
cells are additionally marked directly; enumeration is bounds-checked and
tracks the cell nearest the start by squared distance for the ray-check
direction choice.

**Established fact:** Early exits, in order after goal enumeration: a nonzero
start-satisfies-goal predicate publishes an empty route with completion
status `0x100` ("already satisfied") and stops; an out-of-bounds start cell
notifies status `0x200` and publishes empty; a ray that CONNECTS start to a
goal notifies `0x100` but the search is still seeded and runs; and when the
ray's best scaled h is at or above the start cell's own scaled h, the engine
notifies `0x200` and publishes empty WITHOUT seeding — the A\* starts only
when the ray proved a strictly closer frontier exists.

### 7.3 Scheduler budget, publication, and route storage

**Established fact:** Path work is budgeted. A global scheduler counter replenishes every 150 ticks. Per-player quanta use six-times, three-times, and one-times weighting based on scheduler state. Each active request is limited to 100 heap pops per scheduler call.

**Established fact:** Requests are full-or-empty. A route is published only after a goal is reached and reconstructed. Budget exhaustion leaves the heap and request active for later ticks; it does not publish the best partial prefix. Heap exhaustion publishes an empty route.

**Established fact:** Search allocation initializes the request, clears visitation state, enumerates goals, picks the nearest goal for heuristic setup, validates the start, and can perform a direct ray shortcut. Invalid starts and unreachable goals report failure through order-layer status and receive an empty route.

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
dirty bit sets.

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
mismatch is real and unresolved: the annulus h clamps compare RAW authored
radii while its arrival predicate uses `>>4`-quantized squared radii — two
unit systems coexist in one family.

**Established fact:** Build-site generation enumerates perimeter candidates around a footprint, filters by range and placement validation, sorts a bounded list of candidates, and passes a selected point goal into path search.

**Established fact:** Dynamic blockers update a profile revision. Existing heap entries are not eagerly purged; passability is rechecked lazily when a node is expanded. This can turn a previously open node into a blocked one without rebuilding the whole heap.

### 7.5 Smoothing

**Established fact:** Route reconstruction removes collinear points and then performs bidirectional ray checks. Each intermediate cell must pass the same passability test. A shortcut is accepted for legality; the smoother does not compare the shortcut's integrated cost against the original route.

## 8. Ground steering and occupancy

### 8.1 Desired heading and speed

**Established fact:** The mover selects a waypoint, computes a desired heading, wraps the heading on a 16-bit circle, and clamps heading change by the definition's turn rate. The pending heading and dirty movement flag are updated before integration.

**Established fact:** Acceleration versus braking is selected from the angle-to-waypoint and current speed. Speed is clamped to the definition's maximum. There is no normal reverse-speed branch.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Established fact:** Waypoint lookahead, vertical tolerance, and arrival tolerance are fixed-point thresholds. A blocked mover reduces its next-step cap. Waypoint consumption and path publication are synchronous with the mover tick.

### 8.2 Static and mobile collision

**Established fact:** Static collision uses an axis-aligned footprint rectangle. Path search and movement commit use the same footprint/profile family but at different points: path search uses pre-stamped static cells, while movement commit checks the current rectangle.

**Established fact:** Mobile occupancy is committed synchronously in sweep order. A unit that claims a cell first can prevent a later unit from entering. A vacated cell can be reused earlier in the same sweep. Head-on swaps block; no special simultaneous swap resolution was found. The sweep finishes one unit's clear/commit/stamp sequence before advancing to the next slot, so a later unit immediately observes earlier same-tick occupancy mutations.

**Established fact:** Each mover tick proposes a new X/Z by adding velocity to position and quantizes the proposed footprint anchor with signed arithmetic and the instance's packed half-cell bias. If the resulting cell pair and proposed mover mode equal the committed cached pair and mode, the engine takes a same-cell fast path: it commits the proposed transform and dirty state without calling the footprint validator or restamping occupancy. For a cross-cell or mode-changing proposal in an active local simulation the engine calls the footprint validator once and rewrites the mover blocked flag with its result.

**Established fact:** The footprint validator scans the proposed rectangle row-major — Z outer, X inner — and returns immediately on the first rejecting per-cell predicate; aggregate height, depth, and slope gates run after the scan. A blocked result does not try X-only or Z-only movement and does not move another unit: it caps scalar speed at movement definition MaxVelocity/2 if higher, recomputes horizontal velocity at that capped speed and current heading via the fixed sine/cosine table with rounding, clamps X and Z against the old footprint's centre within half-cell minus one (`±524287`, half-cell `524288` minus one) and commits the clamped self position, marking transform dirty without clearing or restamping occupancy.

**Established fact:** A successful result clears the old footprint, commits X/Y/Z, packed anchor, and low mode bits, stamps the new footprint, marks transform dirty, and calls the coverage wrapper that updates visibility for the local player. The clear-then-stamp sequence finishes before the next unit slot is visited, so a vacated cell is reusable in the same tick — a pipeline of one cell per tick — and head-on swaps where each proposes the other's cell both block, with no reservation or simultaneous-swap resolution. There is no second validator call for axis sliding and no collision-candidate list or candidate cap.

**Established fact (negative-bounded):** No mass-weighted pushing, impulse-based movement resolver, axis-slide resolver, automatic repath timeout, or yielding/wait-queue was recovered within the bounded mover call graph. Absence is contract: a blocked mover only receives the half-speed clamp and dirty clamp above; it does not push, slide, or wait.

**Supported inference:** Mobile units are hard blockers at the movement commit stage even though they are not inserted into the static A* layer. Feature/building and owner-mask details remain separate predicates.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Supported inference:** The engine does not need a separate hard-coded wake
renderer to reproduce the callback contract; wake effects and medium bands in
shipped content are script-authored behavior gated on the bands above.

**Unknown:** Complete wake and SFX-piece mapping for every band transition,
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

**Established fact:** Cruise altitude for point and follow commands is `targetY = (max(sea level, terrain height at target XZ) + signed offset) × 65536` capped at `0x1FF0000` (about 511 world units), where terrain height is the bilinear four-corner query and the offset is `cruisealt` (full altitude) or `cruisealt/2` for the initial climb, or the negated attach-piece world Y for a hanging cargo. Sea level is the terrain header byte; terrain height is sampled at the cursor or at the followed unit's piece world position; there is no lower clamp. Arrival radii are horizontal and strict: explicit air arrivals test `hypot(dx,dz) < radius` with radii 48, 128, or 320 world units depending on order (flagged via the arrival-radius field), while the default test is `dx*dx+dz*dz <= 0.25` (0.5 world units) and for explicit-altitude commands also `|dy| < 65537` (one world unit plus one subunit).

**Supported inference:** Can-fly movers bypass some ordinary ground footprint checks during travel, but air order admission and landing-pad checks still use separate validators.

### 10.2 Air orders

**Established fact:** Air order dispatch classifies move, attack, DGun, load/unload/pickup, follow/help/repair, patrol, hold, teleport, reclaim/resurrect, capture, and mobile-build cases. Weapon flags and attack-run/hover-attack data choose bomber, fighter, gunship, transport, or related behavior.

**Established fact:** Transport service lifecycle is exact for admission, carry,
unload, pads, and death:

*Admission.* `carrier, candidate` is admitted only if, in this order, none of these nine rejects fires: 1) candidate `cantbetransported` set; 2) carrier lacks `canload`; 3) carried-count (entries in the carrier cargo list whose parent equals the carrier) reaches carrier `transportcapacity` (count, not summed sizes; unauthored `0` therefore blocks loading); 4) carrier `transportsize` below candidate `FootPrintX` (signed compare, FootPrintX is the movement class footprint width); 5) candidate has no mover; 6) candidate committed mover mode is active locomotion (mode 2, moving) — moving cargo is rejected; 7) ground carrier (`canfly` clear) with candidate `MinWaterDepth >= 0`; 8) candidate `Y + modelTop` at or below `sea level × 65536` (submerged); 9) candidate landed-float field not exactly `0.0` (still under construction). Missing `transportcapacity` and `transportsize` default to `0`. The effective boarding range is the first enabled weapon slot's `range` (scanned via the weapon-slot enabled flag); shipped unarmed fallback is weapon record `0` (`NOWEAPON`, Range 16), so shipped unarmed pickup range is `16`. Ownership or alliance is not tested in this predicate; whether allied cross-owner commands are permitted is an upstream command-layer question left open (`TODO(question)`).

*Load executor entry gates.* Independent of admission, every phase of the
canonical load executor re-checks four gates in order before doing work: the
order's target reference must be non-null; the executor flags word must hold
none of mask `0x10048`; the target's Y plus its definition's model-top value
must be SIGNED greater than sea level shifted into 16.16; and the carrier's
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
| 3 | Start asynchronous one-argument `BeginTransport` with the exact 32-bit target-definition model-top value, mirrored through the network forwarder; fetch the selected piece's world transform; construct the cargo follow order with altitude offset = NEGATED integer part of that piece's world Y — the cargo hangs below the piece; status `= 0x100EA`. | 1 |
| 4 interrupted | Interrupt-flag combination present (`flags & 0x42`): start the deferred zero-argument `EndTransport` and return WITHOUT attaching. | 8 |
| 4 success | Attach the target to the carrier on the queried piece; emit event code 12; queue the climb-away point command at the carrier's current X/Z with altitude `cruisealt`, no radius; status `|= 0xE0`. | 1 |
| 5 | No work. | 5 |
| other | No work. | 7 |

Successful-load callback order is exactly `QueryTransport` (synchronous) →
`BeginTransport` (asynchronous) → attachment → event code 12. No successful
load runs `EndTransport`: that callback fires on later release or on the
phase-4 interruption edge only. The `BeginTransport` argument is the
definition model-top field, NOT the attach-piece Y; the negated attach-piece
Y value belongs solely to the cargo follow-order hang height of phase 3.

*Load and carry.* A successful air load performs synchronous
`QueryTransport` with four outputs (first cell retained as attach piece) and
then asynchronous one-argument `BeginTransport` with the exact 32-bit value from
the target definition's model-top field, mirrored through the network forwarder;
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
behavior.

**Unknown:** Exact travel time between generated waypoints, ground-following
and sea behavior while orbiting, pad reservation beyond the landing-pad
selection described in 10.2, carrier collision beyond the ordinary shared-mover
rules, and the order-layer command-target supply for each non-construction
air class beyond the established altitude authority and integrator arithmetic
of section 10.1.

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

- Complete lockstep packet ordering, replay state, state-hash contents, and resynchronization behavior.
- Exact unit pool capacity and all slot-limit failure paths.
- Complete serialization of transient order queues, script callbacks, pending path requests, and medium state.
- Exact same-tick visibility for every possible unit creation caller, especially slot-relative reuse and order-created units.
- Complete player category, side, ally, autonomy, and strategic-AI semantics.

### Orders and queues

- Meaning of every construction/factory operation selector and retry mask.
- Consumers for the descriptor class parameter and for the unnamed gate-mask
  bits.
- Where the interface and network layers replace or cancel the front order,
  which the queue pump itself never does.
- Exact attack, reclaim, guard, patrol, and factory-exit goal predicates.
- Order behavior when a path is empty, stale, blocked, or budget-delayed.
- The label of the one internal command code whose resolved name is not
  established.
- Consumer-side meaning of the `Wait` trailing integer and of the `bw`/`b`
  stockpile count passed by InitialMission tokens (section 3.6); both live in
  their handlers.
- Exact construction/economy carry, worktime-under-one-tick behavior, completion ordering, and callbacks.

### COB

- The operand shape of the legacy two-argument effect opcode, though that opcode is a no-op on units. The per-piece flag polarity is now established in section 4.3.
- Reserved opcode `0x10063000`: behavior when a synthetic or corrupted script emits it.
- Independent verification of the conventionally-named bitwise opcodes.
- Stack overflow, invalid piece index, allocation failure, and corrupted-save fault policy.
- The `Aim*` ready-writer closure object and the exact write it performs; the
  zero/nonzero readiness grant and the full callback argument catalog are
  established in section 5. Thread-slot exhaustion behavior is established in
  section 4.3.
- Semantic unit of the footprint-path `SetSpeed` argument, and serialization
  of the unassigned `Killed` variant cell when the script neither assigns it
  nor the work-fraction gate forces zero.
- Unnamed save fields and complete restore behavior for pending calls, waits, and signal masks.

### Terrain and pathfinding

- Retail default content of the settings string feeding the heuristic-weight
  parse (the ×65536 fixed-point form and {6, 3, 1} tiers are established), and
  any runtime surface that rewrites that base besides settings application.
- Unit-system reconciliation between the annulus goal's raw-radius h clamps
  and its `>>4`-quantized squared arrival radii (section 7.4).
- Class-D restored-from-save goal usage frequency, and whether loaded saves
  ever re-issue fresh point/annulus/rectangle goals replacing them.
- Exact order-layer identity of each point-goal wrapper call site beyond the
  classified exemplars; heuristic family, formulas, scaling, threshold, and
  goal constructors are established in section 7.2.
- Per-player debt-array layout and any path-budget edge cases.
- Exact semantics of terrain state one versus clear state three beyond passability.
- Full static feature/yard/owner-mask interaction.
- Whether any order adds dynamic mobile occupancy to search before movement commit.
- Exact out-of-bounds goal handling for every order type.
- Heap OOM policy and integer overflow behavior; route caps are established
  (20 published points, 64-point reconstruction ring, 3 saved waypoints).

### Ground movement

- Braking, blocked-state, and waypoint arrival behavior at zero speed or a
  moving target and its interaction with the exact pitch and below-water caps.
- Collision behavior for simultaneous multi-unit contacts beyond the established
  sequential sweep commit and row-major footprint scan, including interactions
  with features, buildings, and map boundaries; the per-unit validator and the
  same-cell fast path are established.
- Any hidden pushing, separation, or repath behavior outside the bounded mover
  call graph.

### Hover and VTOL

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.