# Design — Units, orders and the COB machine

`internal/units`, `internal/orders`, `internal/cob` and `internal/model`. The
unit pool and the record every other simulation package reaches a unit through,
the death latch and the runtime status word; the sixty-eight order descriptors,
the two queue segments, the pump and its two result-code tables, and the handler
that runs each row; the compiled-script virtual machine, its eight threads, the
engine ports and the engine→script callbacks; and the 3DO piece hierarchy those
scripts animate.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation forms. A bare
`Cn` in these four packages is a contract of §3 below, numbered as its source
plan numbered it.

## 1. Purpose and boundary

These four packages answer four questions:

* **What is a unit?** One fixed pool, one record per live slot, one runtime
  status word, and a death that is a latch before it is a free `[04 §2.3]`
  `[04 §2.4]`.
* **What is a unit doing?** One descriptor table, two queue segments per unit,
  one pump, and one handler body per descriptor row — the order record is the
  only place a unit's intent lives `[04 §3.1]` `[04 §3.3]`.
* **What does the unit's script see?** Twenty engine ports, a fixed callback
  vocabulary, and a virtual machine whose scheduling — eight thread slots,
  sleeps, waits and signals — is itself behavior `[04 §4.2]` `[04 §4.4]`
  `[04 §5.1]`.
* **Where are the unit's pieces?** A piece hierarchy the script rotates and
  translates by name, composed into world transforms that never see the unit's
  position `[03 §2.4]`.

The most dangerous mistake here is conflating a descriptor's **static mask**
with a record's **dynamic gate**. The static mask is insertion metadata — which
segment the record belongs to, whether it was constructed with a target or a
goal, whether the row is nanolathe class `[04 §3.1]` `[04 R-DOC04-C]`. The
dynamic gate is what the record is *waiting for* `[04 §3.3]`. Seeding one from
the other parks a freshly issued order on a gate nothing will ever satisfy, and
because the pump papers over that for whatever the record's own handler clears
first, the failure is silent.

The boundary runs at six places:

* **The tick belongs to `internal/session`.** The per-unit sweep is the second
  phase, including each unit's orders and work pump; player settlement runs in
  the fifth phase `[01 §4.4]` `[04 R-MOV-03 §1]`. `internal/units` publishes the traversal — the
  status refresh after the normal COB drain, the death finalizer at slot end —
  and the session composes the stages between them. None of these packages reads
  a clock.
* **Weapons belong to `internal/combat`.** The unit record carries the three
  weapon slots because the record is where retail keeps them and because
  `internal/combat` imports this package, not the other way round; the slot's
  reload, targeting and firing decisions are combat's `[06 §1.2]`
  `[06 R-WPN-05 §3]`. What `internal/cob` owns of a weapon is the aim
  handshake's completion latch `[04 §5.3]` `[06 §3.3]`.
* **Movement belongs to `internal/movement` and `internal/path`.** An order row
  installs a goal payload through the movement adapter the queue binding
  carries and reads the route's outcome back as pending bits; it never steers
  `[04 §7.2]` `[04 R-PATH-01 §12]` `[04 R-AIR-01 §4]`.
* **Construction belongs to `internal/construction`.** The build rows are order
  records in the primary segment, and the construction service registers their
  bodies on the queue it owns; this package defines no second queue and no
  second record `[04 R-FAC-02 §4]` `[05 "Factory production lifecycle"]`.
* **Presentation is a seam, never a caller.** `emit-sfx`, the piece-flag writes
  and the acknowledgement captions are published through optional sinks; a nil
  sink means no effect and changes no authoritative state `[04 R-COB-03 §6]`
  [I6].
* **The model's draw geometry is `internal/render`'s.** `internal/model` owns
  the hierarchy, the load-time reordering and the transform composition; what
  is done with the resulting vertices is presentation's `[03 R-RAST-01 §2]`
  `[03 R-COMP-02 §3]`.

## 2. Packages and key types

### 2.1 `internal/units`

**The record** (`units.go`, `types.go`). `Unit` is one live instance. Retail's
record has a fixed byte identity; Go stores named fields in a slot-indexed array
parallel to the pool and reproduces no packed layout [I13] `[04 §2.3]`
`[04 R-P0-16-A]`. `World` holds the pool, the ten per-player slices, the
catalog, the terrain and visibility handles, and the creation/death/capture
hooks the session installs.

**The runtime status word.** `Unit.Flags` is retail's one status word and the
constants beside it are its named bits: the classifier-eligibility bit the
allocator initializer sets and death finalization clears, the selection bit,
the building-class and armed bits written once from the definition, the two
occupancy-overlap bookkeeping bits, the mission immunity bit, the cloak-request
bit, the two two-bit standing-order fields, the build-page indicator and page
number, and the static `isairbase` mirror the selection predicate reads off a
*carrier* `[04 R-UNIT-06 §3]` `[04 R-STANCE-01 §2]` `[04 R-COLL-01 §4]`
`[07 §9]`. Adding a bit here without a located writer is how dead branches get
built; every constant in that block names its writer.

**The order-event word.** `Unit.Pending` is the word the pump merges with each
record's own satisfied bits before intersecting the record's gate
`[04 §3.3]` `[04 R-ORD-01 §0]`. Its producers include the weapon layer's
fired/could-not-fire bits, the under-construction wait, and the script-touched marker `[06 R-WPN-05 §6]` `[04 R-MOV-03 §7]`
`[04 R-ORD-01 §11]` `[04 R-COB-06]`. Retail merges it as a zero-extended
sixteen-bit value, which is why gate bit `0x10000` can only ever be satisfied
from a record's own word — the standing question of what writes bit 16 into the
*capability* word answers itself: nothing can `[04 R-ORD-01 §0]`
`[06 R-WPN-05 §6]`.

**Creation and the death latch** (`units.go`, `sweep.go`). `Create` allocates
lowest-free with slot 0 null, seeds the status word from the definition, takes
the allocator's random draws in retail's order — the build angle, then the hover
bob phase — samples the extractor yield over the stamped footprint once, and
binds the script `[01 §6.1]` `[04 R-MOV-01 §5c]` `[04 R-P28-ANG-01R §2]`
`[05 R-PROD-01 §6]`. `Destroy` sets `Dying` and records the cause; `Alive`
survives until the slot-end finalizer, so a unit killed in the projectile phase
is still visible to every later phase of the same tick `[04 §2.4]`.
`DeathCauseFromKind` derives this build's coarse label from retail's damage-kind
byte rather than casting it, because the two enumerations do not share their
numbering `[06 §12.1]` `[08 R-SAVE-02 §6]`.

**The sweep** (`sweep.go`). `VisitActiveSlots` is the one deterministic
traversal: players 0..9 ascending, then slots ascending inside each player's
slice, with the alive flag read at the moment the slot is reached — so a unit
created ahead of the cursor is visited in the same tick and one created behind
it waits `[01 §6.2]` `[04 R-MOV-03 §1]` [I1]. `StepPostCOBStatus` follows the
one normal script drain, after weapons and
before water damage, self-repair, orders and movement. Allocated dying units
receive this refresh, including the health-sample roll consumed by `Killed`;
`FinalizeDeath` remains the slot-end boundary `[04 §5.1]`.

**The script binding** (`cob_binding.go`). One production binding per unit: the
compiled program, the model piece list linked against the script's piece table,
the per-unit port handlers, the callback bridge and the simulation stream. A
definition whose program is unavailable remains in the catalog with a warning.
Unit creation refuses a required missing program: every creation path (including
nanoframes, capture and forced-slot save reconstruction) rejects it before pool
allocation or creation RNG draws. This diagnostic refusal is Nanolathe host
policy: retail faults during creation `[04 R-COB-04 §8]` `[04 R-COB-01 §3]`.
Unrelated definitions remain usable; no scriptless instance is created.

Catalog linking retains both that immutable program and the winning COB
provenance. Unit creation passes this asset into strict binding, which links the
piece names and allocates fresh VM threads, statics and piece state without
rereading or recompiling the script. A definition from another catalog or VFS
overlay carries its own provenance and program; mutable VM state is never
shared `[04 §4.1]`.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The detached unit image
and its restore, including the fields whose consumers belong to later phases and
the three packed status bits retail copies from its writer stack and that no
runtime state can supply — those are an explicit scratch argument, never
silently zeroed `[08 R-SAVE-UNIT-01]` `[08 R-SAVE-02 §6]` `[08 R-SAVE-02 §14]`.

### 2.2 `internal/orders`

**The descriptor table** (`table.go`). `Descriptor` carries the canonical name,
the interface state label, the overlay draw mask, the acknowledgement/icon byte,
the static gate mask, the goal-resolution presentation-helper identity, the
handler, and a `Driver` value that names what advances a row with no handler of
its own `[04 §3.1]` `[04 R-DOC04-C]` `[07 R-P0-11 §3]`. The table is built from
four static batches of 23, 22, 22 and 1 records, re-sorted after each batch;
`Lookup` is a binary search with the same comparator `[04 R-STANCE-01 §9]`.

**The record** (`pump.go`, `node.go`). `Node` is one order record: descriptor
identity, handler-private phase byte, dynamic gate, absolute deadline (−1 for
none), owner and target handles, the 16.16 goal triple, the guard anchor and
cached target position, three general parameters, the static-mask copy with its
runtime bits, the creation tick, the accumulated satisfied word and the flag
bits `[04 §3.2]`. `NewNodeForOrder` and its typed wrappers are the one
constructor every producer enters through — the HUD, the computer player, the
mission spawner, COB, rally inheritance and factory completion — so the queue
modifier, the goal payload and the build product identity are formed in one
place `[04 §3.4]`.

Constructor admission applies both presence clears of `[04 §3.1]` on the
record's own static-mask copy: bit 9 when no target unit was supplied, bit 10
when no goal position was supplied `[04 R-MOV-03 §7]`. Retail reads presence off
two optional constructor arguments. The target handle carries its own answer —
the null handle is "no target" — but the goal is passed here by value as three
fixed-point words, and the fixed-point origin is a legal map position, so
presence cannot be recovered from the triple. The producer therefore states it:
`Node.GoalSupplied` is an insertion-time input alongside the queue modifier,
set by every construction site that supplies a position payload, consumed by the
constructor into the mask copy and cleared on the stored record. Bit 10's one
reader is the guard's leg-4 copy arm, which takes it as "this record has a goal
to copy" `[04 R-ORD-01 §13]`.

The target handle is the record's observer link. Constructor admission clears
it when the issued-target bit is clear; later `BindTarget` calls relink it
independently of that bit, including the stationary guard's acquired target.
`TargetRemoved`, `TargetCloaked` and the damage `ObserverNotice` deliver each
notice to the observing record's `Satisfied` word, so one queue head cannot
consume another record's event. Removal then clears the link; cloak and damage
retain it `[04 R-ORD-01 §6]` `[04 R-MOV-03 §7]`. Loading relinks the saved
unit target without repeating constructor admission `[08 R-SAVE-ORDER-01]`.

**The two segments** (`pump.go`). `Queue` holds a primary and a secondary
slice, the per-queue binding, the per-queue diagnostics, and the per-row owned
handlers. The rear segment is exclusively `BuildWeapon` and `SelfDestruct`,
selected by the descriptor's rear-segment static bit `[04 §3.1]`
`[04 R-ORDER-02 §1]`. Storage is dynamic: the stock corpus reaches queues
longer than any fixed cap (SC17), so the only bound is an out-of-memory guard
far outside stock.

**Producer insertion and the active marker** (`pump.go`). `Push` is the
producer-side insertion. It constructs the record, drops leading auto/default
records when the new record is not rear-segment bound, arms the one-shot
caption-pending flag on a non-queued (Replace) issue only, and then takes one of
two branches on the new record's static-mask copy: head-insert into the segment
the rear-segment bit selects, inheriting the displaced head's auto flag and
writing no marker at all; or insert immediately after the active marker — the
tail when nothing carries it — and move the marker onto the new record
`[04 R-ORD-01 §13]`. The marker has exactly that one writer. Removals do not
hand it on: `releaseMarkerOnRemoval` only clears a duplicate, because freeing the
record that carried it leaves the segment unmarked and the next insertion then
appends at the tail. `PushHead` is the handler-side spawn, which writes the
link, the owner and the inherited auto flag and nothing else.

**Replacement cleanup** (`insert.go`). `PurgeUnprotected` walks the live
primary segment and removes a rejected record before delivering cancellation.
It retains the original head for tombstone decisions and the traversal's
predecessor across callbacks. Cancellation-created records receive the same
survivor test as existing records [04 R-MOV-03 §6]. During synchronous cleanup,
the queue temporarily retains the detached record's successor answer so the
air entry can still decide whether that record was last. A cancelled air
attack appends its seek through `appendTail`; the ongoing replacement purge
removes that seek before the new player order is inserted [04 R-AIR-01 §16].
If a callback removes the retained predecessor itself, the exceptional
`TODO(question)` boundary stops the purge with remaining records pending;
the reachability of that case needs the callback census identified in doc 04's
Missing and unknown list. It is not a retail head-restart rule.

**The queued-order toggle.** `CancelFrontMost` is the removal half of retail's
Shift-click duplicate test: it walks the primary segment front to back, unlinks
the first whole matching node — no count decrement — and reports that one went,
in which case the producer issues nothing `[07 R-P0-11 §6]`. The match itself is
the session's, which supplies the resolved order identity, the optional target
and a whole-cell inclusive per-axis tolerance.

**The pump** (`pump.go`). `Pump` runs the primary walk, then the secondary walk;
the two are adjacent calls with no test between them, and a blocked front head
stops only its own segment `[04 R-ORD-01 §10]`. The primary walk is head-only: after every
non-returning result code it reloads the front record and re-applies the gate
test, so "continue" never means "visit the record behind this one"
`[04 R-ORD-01 §10]`. Dispatch is one seam — the descriptor's handler, or the
owned handler a subsystem registered on this queue — and the pump applies the
gate test, the two-sided bit consumption and the gate clear identically for both.
`applyPrimaryResultCode` and `applySecondaryResultCode` are deliberately
separate tables: the segments share the code values and not their effects.

**Handler registration** (`table.go`, `queue_handlers.go`). Retail compiles a
handler into every static descriptor. This build assembles the same table from
files that cannot all initialise before the table does, so each family owns an
installer and `handlerInstallers` is the single ordered list that runs them — a
slice, not a map, because registration order is a contract [I1]. Every installer
is idempotent, and all but one reach that by assigning only where a descriptor's
handler is still nil, so the list's order is what settles a row two families
both name. The chase/guard installer is the exception: it assigns its three rows
unconditionally and is made idempotent by a one-shot latch on whether
`Attack_Chase` already carries a handler. `installHandlers` runs once, from
`buildTable`; the pump does not re-run it, and the chase/guard family's own
lazy retry inside `Resolve` is what covers an init order that ran before the
table existed.

**Order registry queries.** `WeaponAdapter.TargetsInRadius` connects `Wait`
and the stationary `Guard_NoMove` scan to
`combat.Service.TargetsInRadius(owner uint8, x, z numeric.Fixed, radius int32,
world *units.World) []pool.Handle`. Session composition supplies the owning
player and unit world. Combat returns a caller-owned slice in cached registry
order using the primary list, with secondary fallback behind the player's
targeting-upgrade gate. The query consumes no RNG; the guard owns its one
index draw. It preserves registry cadence and does not repeat visibility,
hostility or weapon scoring [04 R-SPEC-01 §8][06 §3.1]. The guard queries
around its saved target position; `Wait` queries around its own unit. A live
unit-pool scan cannot substitute for this query.

Rows whose bodies belong to a package `internal/orders` cannot import register
on the queue instead. `OwnedHandler` returns a result code and a boolean: `true`
means it ran the row's body and the pump applies the code through the ordinary
epilogue; `false` means the registering subsystem advances the record from its
own per-unit step and owns the record's phase, gate and deadline, so the pump
writes none of them and ends the pass. That second form is not a courtesy —
applying a result code over a live state machine parks a factory's record for
30 to 44 ticks mid-build. `SetGetBuiltHandler` is the named form for the one row
whose owner always advances it inside the pump visit; `SetExternallyDrivenHandler`
is the second form's shorthand. An owned record is not a missing handler and is
not diagnosed as one.

The two unit-reclaim rows use that seam within `construction.StepUnit`:
an earlier queue visit forwards its accepted event set onto the same node's
pending and gate fields without executing work. The construction window then
resumes the primary pump through `ContinueUnitReclaim` with its owned handler
active, so the ordinary gate consumes the event and applies the phase/result
code. The secondary segment runs only in the earlier ordinary queue visit. No event is retained on the
service or transferred to a replacement record. `GroundUnitReclaimSetup` owns
the ground callback, stance-wait and work-cue phases; `AirUnitReclaimSetup`
owns the air preamble and marker. The ground work phase follows
`StartBuilding` → script-touched stance wait → work cue. The air row never
emits `StartBuilding` or a reveal stamp and has no stance wait
`[04 R-ORD-01 §5]` `[04 R-ORD-01 §7]` `[04 R-COB-06]`.

**The handler families.** `standing.go` (the trivial, standing, cloak, wait,
paralyze, teleport and standby rows), `selfdestruct.go`, `work.go` (capture,
reclaim, resurrect, assist and the repair trio), `vtolwork.go` (the five VTOL
work twins), `combat.go` (the stationary attack rows and the stationary guard),
`vtolair.go` (the air executors and the four air-attack rows), `patrol.go` (the
queued-move pair, both ground patrols and the two air moves), `transport.go`,
`park.go`, `stop.go`, `activation.go`, `selectable.go` and the stockpile row.
`resolve.go` owns command resolution and the chase/guard rows; `scans.go` owns
the deterministic visitors those rows share; `callbacks.go` owns the removal
cleanup callbacks.

**Idle refill** (`pump.go`). A primary segment that empties is handed the
definition's `defaultmissiontype` as an auto-flagged head insert, gated on the
owner's controller state being one of the two active values, and the pump
returns — the record is dispatched on the next visit, never the same one
`[04 §3.3]` `[02 R-KEYS-01 §1]`. Every stock aircraft authors `VTOL_Standby`
there, which is how an idle aircraft comes home.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The writer walks the
primary segment then the secondary, continuing one sequence across both, and
carries the handler-private phase byte verbatim because handlers own its
interpretation `[08 R-SAVE-ORDER-01]` `[08 R-SAVE-02 §6]`.

### Modern Hold Fire

**Nanolathe Modern policy (user-authorized).** The central `gameplay.Mode`
selects the order package's rule set, `orders.Rules`, which the queue carries
on its binding; the queue asks it `HoldsFire` at the join, at the stance write
and at the stationary guard's takeover. `orders.StrictRules`
answers the retail way, so Strict 3.1 is preserved by the seam's zero-size
default. Modern Hold Fire refuses the guard's forced combat join even though retail's
force flag bypasses both standing-order fields `[04 R-STANCE-01 §3]`
`[04 R-UNIT-06 §1]`. The guard retains its existing follow/assistance order and
continues its ordinary movement/repair behavior. Standing move still has its
retail force bypass, and Return Fire keeps its existing behavior.

Hold Fire suppresses what a unit would do on its own, never an explicit order.
An attack, attack-ground, command-fire or launch order issued to a held unit is
admitted as usual, takes its weapon slot through the ordinary release verb —
which clears the slot control byte's autonomy bit `[04 R-ORD-01 §1]` — and
fires: that cleared bit is the provenance the launch gate reads
([DESIGN_WEAPONS_PROJECTILES.md §2.6.1](DESIGN_WEAPONS_PROJECTILES.md#261-modern-hold-fire),
which also owns the suppression of own-slot launches and their burst
remainders). Changing fire stance likewise retains explicit attack orders and
their bound targets.

**Automatic combat is retired at the stance write.** Three producers in this
package take a slot the same way an explicit attack does, so the launch gate
would let them fire on through Hold Fire:

1. an attack record of the auto-engage issuer — the opportunity scans of
   patrol, standby, the mine and the air seek, retaliation, and the guard's
   combat join — which Modern tags at insertion;
2. the Modern danger response, when it is an attack rather than a withdrawal
   or a wait;
3. the stationary `Guard_NoMove` record — the idle default of the definitions
   that author it — whose phase 1 takes over a target the unit acquired
   `[04 R-ORD-01 §3]`.

*Strict 3.1:* the standing-fire handler deposits the field and, for its exact
unmasked parameters zero and one, clears the targets of autonomous slots only
`[04 R-STANCE-01 §2]`; it touches no record and no order-held slot, so such an
engagement continues under its own handler's rules `[04 R-STANCE-01 §3]`.

*Modern:* when the deposit leaves the field at zero, and before that retail
slot walk, the handler removes every record of kinds 1 and 2 from the front
segment through the ordinary unlink — goal payloads released, cancel
notification delivered, a danger response's suspended assignment restarted —
and restarts a kind-3 record in place (phase zero, gate, satisfied set and
target reference cleared) because it is the unit's standing assignment, not a
task. The ordinary unlink returns weapon slots only for the front record
`[04 R-ORDER-02 §2]`, and the record that was running is not the front one at
that moment — the standing record is — so when the running record was one of
the three, all three slots are handed back by the same walk its removal would
have run: autonomy bit set, target cleared, `TargetCleared` raised
`[04 R-UNIT-06 §5 part 3]`. The unit therefore launches nothing from its next
weapon visit on; shots already in the air continue.

A held unit is kept from re-entering the same state: the auto-engage issuer
refuses every engagement, forced or not; a danger response issues and keeps an
attack only while the field is non-zero; the stationary guard's scan returns
nothing; and its phase 1 declines to take a slot, holding on its ordinary
30-tick deadline without its attempt-budget draw.

Left alone, wherever they sit in the queue: explicit attack, suppress,
command-fire and launch records; type-constrained `AttackUType` children; the
return move a maneuver engagement left beneath itself, which now simply walks
the unit back to its post; a withdrawal or wait response; and any attack whose
producer is unknown — restored from a save or inserted directly — because the
producer tag is transient and guessing it would cancel a player's order. The
write draws no randomness and spends nothing in either mode.

Tests (`modern_hold_fire_test.go`) preserve queued attack/guard records,
verify that an explicit attack issued while held takes its slot, verify ground
and aircraft guard combat-join suppression, and lock the stance write in both
modes: a fire-at-will unit's automatic attack is retired alone with an
explicit attack queued behind it untouched, its slot comes back empty and it
does not re-engage, while Strict keeps record, slot and target; the stationary
guard is restarted rather than removed and declines its takeover while held,
while Strict takes the slot and draws; a danger attack ends and its assignment
restarts while a withdrawal stays; Return Fire is covered by the join cases.

### Modern danger response

**Nanolathe Modern policy (user-authorized prototype).** `orders.Rules` owns
observed danger, response visits, the damage-purge protection, automatic repair
admission and command precedence. The reserved Strict implementation leaves
new danger state untouched and retains retail's immediate damage retaliation.
Modern defers that order insertion until the next ordinary per-unit order visit;
the damage path may still offer the attacker to autonomous weapon slots.
Retail's baseline is a damage-event edge with no retaliation memory or timer
[04 R-STANCE-01 §3], and maneuver's existing attack/return pair and inclusive
leash remain [04 R-STANCE-01 §4]. The following memory and decisions are
approved policy, not historical claims.

An identified hostile launch or damage notice is accepted only with current
observer contact visibility. Each victim queue remembers at most four attacker
identities and last observed positions for 180 ticks (six seconds). Another
notice refreshes that attacker's age; when full, the oldest observation is
replaced, with array order breaking ties. Continuing visibility may refresh
position but never extends the age. A lost contact supplies only its frozen
position to withdrawal scoring, never a live pursuit target. Identity includes
the unit object as well as its handle so slot reuse cannot inherit danger.
There is no global danger grid or shared hidden-target tracker.

A received projectile hit can also establish anonymous danger when its attacker
is unseen. `ObserveImpact(unit, bearing, tick)` receives only the victim and
world direction opposite the projectile's horizontal motion at impact. This
uses the locally received projectile, never the unseen shooter's position. A
direct projectile can collide past the victim center, so the packet's impact
point bearing alone could send withdrawal toward the shooter. If horizontal
motion is zero or the packet has no projectile observation, combat instead
combines the packet's quantized impact direction with the victim heading
[06 §9.1]. That fallback is an impact-side heuristic, not an inferred shooter
location; a centered stationary explosion carries no useful source direction.
The original packet direction and COB callbacks remain unchanged. Only
nonzero accepted hostile damage other than the no-reaction kind supplies the
anonymous cue. Orders receives no attacker identity or position. Modern places a frozen hazard point 256 world units along
that direction from the victim's position at impact, using the shared integer
trig (zero bearing is +Z). This distance is prototype tuning, not an estimate
of the attacker's range. At most four anonymous points are retained separately
from identified contacts. Repeated hits in the same of eight compass sectors
refresh that sector's point; an empty or expired entry is reused first,
otherwise the oldest is replaced, with array order breaking ties. Sector
boundaries lie halfway between compass directions. Each point expires after
180 ticks; later victim motion and hidden-unit motion, death or slot reuse
cannot update it. These bounds and grouping are Modern prototype policy.

Anonymous points count as danger for withdrawal scoring and automatic repair
suspension. A suitable visible remembered threat can still be answered first;
an anonymous point can never supply an attack target or ground-fire order.
An eligible Roam or Maneuver unit with no effective response uses the existing
locally feasible withdrawal, while Hold Position, active construction, manual
repair, control tasks, carried units and progressing explicit movement retain
their existing protection. Expiry resumes the retained assignment and Maneuver
returns to its anchor. New commands and load clear anonymous points along with
other danger state; switching to Strict leaves already staged ordinary orders
to finish and the Strict observation hook writes no state or RNG. No new save
bytes or resource charges are introduced. `danger_impact_test.go` checks ordinary
movement installation without any attacker lookup/acquisition, bounded frozen
points, stance and work protection, repair resumption, expiry and Strict bypass.
Combat tests cover direct impacts centered on and beyond the victim at different
hull headings; session tests exercise the composed movement response, unchanged
RNG/resources and an authored Flash under an out-of-sight rocket tower's fire.

The normal session order sweep calls `StepDangerResponse` before `PumpUnit`.
New decisions run at most once per 30 ticks, while current response visibility,
expiry, path failure and stance are checked every visit. Movement failure reads
the order record's published no-route event, including when diagnostic movement
state remains en route [04 R-PATH-01 §7][04 R-PATH-01 §9][04 R-COLL-01 §6].
Arrival, payload release and search-setup status alone do not establish failure.
Suspension and resumption clear consumed movement notifications and diagnostics
while preserving the assignment's destination. The queue retains a
stable visible response instead of replacing it on every launch. At Fire at
Will, each 30-tick visit asks the existing combat acquisition port for its best
currently engageable candidate; a changed answer replaces only the automatic
reaction. Combat owns threat ranking. Return Fire does not perform that
opportunity acquisition. Explicit attack orders and explicit/scripted weapon
targets are retained. A progressing manual move is also retained; a blocked
manual move may suspend and later restart with its original destination.
Every new primary producer command, including a queued command, movement
stance change or cloak toggle, clears danger memory and supersedes the automatic
reaction. The movement-stance and cloak producers use ordinary head insertion
without a replacement purge: leading automatic records are removed, caption
admission applies, and the assigned mission survives [04 R-ORD-01 §13].
Fire stance retains its existing boundary because the narrower **Modern Hold
Fire** contract above preserves withdrawal/wait responses and retires automatic
attacks at the stance write. Converting that producer to ordinary insertion
remains separate work: it must preserve that policy while reconciling the
leading-auto and caption bookkeeping required by the retail contract.
`internal/session/command_producer_test.go` checks movement stance and both cloak
toggles in Modern and Strict, mission retention, leading-auto removal, caption
admission, danger cleanup and unchanged RNG/resources. Its response case checks
that movement stance and cloak supersede a withdrawal while Hold Fire retains
it through the stance write. A response
displaced by a temporary control/task head, especially `Paralyze`, cannot
retarget or insert another response ahead of that head; a stunned unit likewise
cannot begin a new response. Contact timers still expire and expired reactions
are removed. A maneuver return after expiry waits until the controlling head
has released the unit.

Modern also reconsiders already owned automatic ground targets without requiring
an incoming danger notice. The same 30-tick visit queries combat for stationary
`Guard_NoMove` and ground `Attack_Chase`/`Attack_NoMove` records explicitly tagged
by the automatic engagement producer. Patrol and guard joins use that producer;
direct attacks, restored unknown attacks and type-constrained `AttackUType`
children do not. Only Fire at Will permits opportunity acquisition. The current
head and stun checks apply, and a chase's original maneuver anchor, leash,
return record and queued assignment survive retargeting. A candidate outside the
chase leash is rejected for both ordinary automatic attacks and danger
reaction retargets, including equality at the boundary. Automatically issued aircraft attacks retain their
existing pass behavior; this periodic extension covers grounded attacks only.

The stationary guard's own scan also asks combat rather than choosing a random
registry entry, and a retained target does not take a random restart that would
clear the weapon. Strict keeps its scan, restart draws and slot release behavior
[04 R-ORD-01 §3]. In Modern, changing a grounded automatic target rebinds the
same record and restarts its targeting phase without cancelling its Aim script.
Retaining the target leaves its phase and Aim unchanged. Taking ownership of an
already targeted slot likewise omits `TargetCleared` only for the exact current
automatic/danger/guard head and its selected slot. It still clears autonomy.
This prevents cancellation of the outstanding script while its request latch
remains set. A changed target uses the normal setter's asynchronous Aim
semantics; combat's drift check requests a fresh aim when the new geometry
requires it [06 R-WPN-05 §3][06 R-WPN-05 §4]. Explicit orders keep their release
callbacks. Producer tags and reconsideration timers add no retail save bytes;
Strict neither marks these producers nor visits the new periodic policy.

Current construction, factories, construction assistance and direct repair
are protected from both reaction insertion and the AI damage purge. Temporary
control records and an approach child cannot conceal an already started work
parent. Future queued construction/repair behind a patrol or guard does not
protect the unrelated current assignment. Repair provenance is an explicit
transient producer tag set by patrol and guard repair issuers, including the
guard's copied repair and Modern nearby-repair leg. `FlagAutoOp` is never used
as evidence of repair origin: direct and restored repairs can inherit it.
Unmarked/unknown repairs are protected. Construction assistance is protected
even when an automatic producer selected it.

An automatic repair may end while retaining the exact patrol/guard assignment
and successors underneath it; its automatically inserted return move ends with
that repair. The suspended assignment releases its owned movement payload and
resets its handler to admission so no lost arrival wake can strand it. Automatic
repair producers remain inhibited while danger is remembered or a reaction is
active. This avoids repair/retreat oscillation. Resumption selects work through
the original assignment; it does not force the old patient to remain valid.

Response first chooses a visible remembered attacker with a suitable weapon,
using nearest distance and then lower handle for ties. Suitability comes from
combat, per slot, without a range requirement; the unit's no-chase category
still applies. A currently engageable primary weapon can use `Attack_NoMove`,
including Hold Position and stationary structures. Other grounded slots use
`Attack_Chase`'s existing selected-slot parameter; air attack executors use the
primary slot. Hold Fire never creates an attack. Maneuver pursuit keeps the
initial response anchor and authored leash, inserts the ordinary return move,
and does not pursue a target outside that leash; Roam permits ordinary pursuit.
The existing chase/flight handlers own route execution and their own combat
maneuvers. If the exact tracked reaction returns cancel-all, Modern converts
that result to removal of the reaction alone, preserving the retained assignment
and excluding that failed target for 90 ticks. Explicit orders keep their normal
failure results, and Strict returns every result unchanged. This policy adds no
general kiting controller.

Only when no effective response is available does a mobile unit consider
withdrawal. It evaluates eight fixed directions at 64, 32 and 16 world units,
with diagonal components 45, 22 and 11 respectively, maximizing minimum separation from all
remembered threat positions. Ties retain traversal order, and a candidate must
strictly improve that minimum. All candidates first use the movement-owned
`DangerStepFeasible` straight-corridor query. Any admitted direct escape wins
over every detour. Only when no safer straight choice exists are the same
candidates ranked again using `DangerRouteFeasible`, which can admit a bounded
local ground detour, described in DESIGN_MOVEMENT_PATH
"Modern danger escape". Intermediate motion may approach a hazard to get around
a friendly crowd; the destination must still improve separation. Ordinary
ground/air movement owns route requests, collision and completion. Maneuver
bounds the withdrawal destination by the original leash; the ordinary route can
detour on its way there. Hold Position never withdraws. A blocked pursuit is
excluded for 90 ticks before retry. Once withdrawal has begun, a lack of further
safe progress uses an ordinary 30-tick `Wait` instead of resuming the assignment
into remembered fire. When memory expires, Maneuver returns to the original
post and the retained assignment resumes. These constants are prototype tuning,
not retail constants; the policy does not promise a globally safe route.

Selection, memory and protection draw no RNG and debit no resources. Executing
an inserted ordinary order retains that handler's established RNG behavior and
any resulting combat costs. Suspended repair stops making its ordinary repair
charges. Strict's new hooks draw nothing and write no danger state. Rule
objects remain zero-size; all mutable state belongs to the victim queue.

No retail save bytes are added. Loading creates empty danger memory and unknown,
protected repair provenance. Already issued reaction orders save as their
ordinary attack/move/wait rows, with the retained assignment already reset to a
restartable phase. Switching to Strict likewise leaves ordinary staged orders
to finish under normal handlers, so the Modern memory timeout no longer cancels
an existing attack; that attack completes on its normal target/route conditions.
Switching back revalidates retained observations and expires old ones. New
Modern commands remove the queue's tracked reaction and return move. No handler
requires a transient provenance bit to complete after loading or switching.

`automatic_target_modern_test.go` locks producer provenance, ground/guard
opportunity retargeting, the retained leash and assignment, same-target Aim and
callback preservation, control/stun/explicit/Strict bypasses and guard scan
selection. `danger_modern_test.go` locks Strict no-write/no-draw behavior, active work
protection despite inherited auto flags, future queued work, automatic producer
provenance, blocked manual-move resumption, contact loss and slot reuse,
multi-threat and short-corridor withdrawal, maneuver return, secondary weapons,
Fire at Will reconsideration versus Return Fire, queue isolation, mode switching
and retail save restoration. The session owns visibility/feasibility composition
and the integration, visual and performance gates.

### Modern guard assistance

**Nanolathe Modern policy (user-authorized).** The central `gameplay.Mode`
selects the order package's rule set, `orders.Rules`, whose three guard
decisions — `GuardSeeksPad`, `GuardWorksNearby` and `GuardResumesFromPad` — the
guard legs ask in place of a mode test; `orders.StrictRules` answers all three
the retail way, so Strict 3.1 is preserved. This applies to the unit executing `Follow_Ground` or `VTOL_Follow`,
with its original Guard record and queued successors retained. It does not
turn the guarded unit into an area-work command. The stationary
`Guard_NoMove` keeps its existing behavior.

Strict 3.1 defends and assists the ward, copies its work, then follows or
orbits; it does not scan surrounding allies or wrecks, and `VTOL_Follow` does
not seek pads `[04 R-UNIT-06 §1]` `[04 R-ORD-02 §3]`.

In both modes Guard is a standing order with no success exit: only a missing
ward (code 5), a flying ward for the ground row (code 8) or cancel-all
(code 7) ends it `[04 R-ORD-01 §8]`. Because the pump runs only the front head
`[04 §3.3]`, a record Shift-queued behind a guard — including a second Guard —
never starts while the ward lives, and a plain issue purges the guard, which
lacks the purge-survivor bit `[04 R-MOV-03 §6]`. One unit therefore never
guards two wards. Modern assistance temporarily prepends work but never
completes the guard. `guard_queue_test.go` locks replacement, the blocked
successor and the never-admitted second guard in both modes.

Modern adds these branches at the existing guard maintenance cadence:

* Aircraft below the patrol repair threshold first try the existing allied
  air-base registry, range, activation and builder admission. This includes
  carrier pads. Selection and landing use the patrol helpers unchanged
  `[04 R-AIR-01 §11]`; the landing executor decides whether a pad piece is
  available. A successful selection temporarily prepends `VTOL_Landing`.
* After combat support and direct ward assistance, mobile builders scan for
  nearby work. Repair requires energy at least one fifth of storage, as in
  repair patrol. Candidates use the patrol visitor's inclusive sight radius,
  friendship, grounded state, damage/build progress and active-reclaim
  exclusions `[04 R-ORD-01 §4]` `[04 R-ORD-02 §4]`. The first candidate in
  unit-slot order that resolves command 8 receives the ordinary repair or
  build-assist order. Its existing water and capability admission still apply.
* If no repair is issued, a `canresurrect` builder selects the first reclaimable
  feature in the feature service's stable anchor order within the same
  inclusive sight radius whose corpse-name prefix resolves to a unit. The
  read-only `WorkAdapter.CanResurrectFeature` query uses the same catalog
  resolution as resurrection `[05 R-WORK-01 §7]`. This scan adds no energy or
  metal requirement: resurrection itself has no ledger cost. It issues the
  existing `Resurrect` order, including its subsequent repair of the revived
  unit. It does not reclaim trees or infer wreck ownership.

Each branch releases the guard's movement payload, arms the ordinary 30-tick
maintenance deadline and prepends one ordinary work order. An immediately
failed or unreachable job therefore cannot be selected repeatedly in one pump
visit. Resumption waits at most that maintenance interval. Its ward identity,
follow offset/orbit parameters and queued successors survive; the same guard
resumes when the
work completes or abandons. Aircraft detach from an air-base attachment through
the existing takeoff preamble when Guard resumes, preserving their orbit
parameters rather than repeating the initial bearing draw. Ordinary transport
cargo retains the carried-guard rejection. There is no extra return-move record
or standing move gate, matching the guard's direct ward-assistance behavior. The guard
can select another nearby job on resumption before following again.

Selection changes neither resources, health, feature state nor worker state.
Nearby repair and resurrection selection consume no RNG; pad selection keeps
patrol's one bounded pick (no draw for fewer than two candidates). Existing
landing, repair, construction and resurrection executors retain their resource
admission, costs, effects and RNG. Strict bypass returns before any added
query or draw. `guard_modern_test.go` covers mode bypass, range and resource
boundaries, carrier selection, stable work priority, retained queue identity
and resumption; the session composition tests exercise the central mode and
catalog-query wiring.

### 2.3 `internal/cob`

**The program** (`load.go`). `Program` is the immutable compiled script: the
opcode word array, the script-name map, the script index array in table order,
the ordered piece-name table, the static count and the content checksum
`[fmt cob]` `[02 "Compiled script archive (COB)"]` `[04 §4.1]`.
The declared static count has an explicit host-safety byte budget because it is
not backed by a file span; parser, external binding and restore validate it
before mutable VM storage is allocated. The budget is not a retail constant
`[fmt cob "Header"]`.

**The machine** (`vm.go`). `VM` is one unit's instance: eight `Thread` records,
the piece animation state, the statics, the bound ports and sinks. A thread
carries its status, its word-indexed program counter, its window, its stack
pointer, its sleep counter, the piece and axis it waits on, the thread it is
blocked behind and its signal mask `[04 §4.2]` `[04 §4.3]`. `Start` allocates
the lowest free slot; `Call`/`CallQuery` is the synchronous query form that
leaves a blocked thread allocated; `Signal` terminates threads by mask; `Drain`
runs due threads in fixed slot order and then one piece-interpolation pass
`[04 §4.6]` `[04 R-COB-02 §2]`.

**Ports** (`ports.go`). `PortTable` is the twenty engine ports, verbatim, read
column and write column `[04 §4.4]` `[04 R-COB-03 §1]` `[04 R-COB-03 §2]`. Reads
outside 1..20 answer zero. Writes bind exactly six arms — activation, in-build
stance, busy, yard open, bugger off, armored — and an identifier with no arm
still sets the script-touched marker, as does every arm `[04 §4.7]`
`[04 R-P0-10]` `[04 R-COB-03 §4]`. That marker is not opaque: it is the unit's
pending bit `0x4`, and the order pump's satisfied merge is its only consumer, so
a script's engine write is the only thing that re-polls the in-build-stance and
transport-busy waits `[04 R-COB-06]`.

**Callbacks** (`ports.go`, `bridge.go`). `CallbackBridge` is the one bridge per
unit, retained for its life so that sinks a caller installed survive a callback
run. The arithmetic is here: the rock and hit-by-weapon impulse arguments
through the shared fixed-point trig table, the local death-severity query, the
reload-time conversion, and the two synchronous query seeds `[04 §5.1]`
`[04 §5.3]` `[04 R-CB-01 §2]` `[04 R-CB-01 §3]` `[04 R-CB-01 §4]`.

**Explosion admission** (`explode.go`, `bridge.go`). `ExplosionSink` is an
immediate, typed hand-off from one COB `explode` instruction to the
session-owned physical/effect arenas; it is not a retained event queue. A
physical instruction first forms its six-draw kinematics, hides its source
piece, then offers exactly one `WholePieceExplosion` or `ShatterExplosion` to
the sink. The sink's boolean is the arena's admission decision and cannot undo
the hide or the six draws. Each requested bitmap bit is then offered, in
ascending bit order, as a `BitmapExplosion`; bitmap-only skips the physical
offer and leaves its source shown. `ExplosionPieceIdentity` (the COB piece
index and declared geometry name), `ExplosionPieceState` (the copied script
transform and current render flags), and `ExplosionKinematics` are separate
concepts so a future arena can resolve unit/world state and model geometry
without making a COB VM own either. Session must bind the sink with the source
unit identity, model-piece mapping/geometry, current unit transform and mover
velocity, and must allocate whole debris or each shatter fragment at the
researched admission boundary. It owns the 100-slot debris ring, 300 effect
slots, 300 fragment geometries and effect-phase stepping; the client only sees
their committed snapshot `[04 R-COB-04 §1]`–`[04 R-COB-04 §4]` [I4] [I5] [I6].

**The aim handshake** (`ports.go`). `AimSlot` is the seam between the script and
the weapon: an issue bit set immediately after an `Aim*` start, which gates
re-issue and authorizes nothing, and a ready latch granted only by a nonzero
value delivered to the completion receiver. A failed start delivers zero through
the same receiver; a signal or abnormal termination never invokes it
`[04 R-CB-01 §6]` `[04 R-COB-04 §9]` `[06 §3.3]` `[06 R-P0-07]`.

Ports 2 and 3 read the unit's current two-bit standing-move and standing-fire
fields, including changes after binding. They have no write arm: an engine
write leaves the stance unchanged and raises only the ordinary script-touched
marker `[04 R-COB-03 §3]` `[04 R-COB-03 §4]`.

**The binding** (`binding.go`). `BindingRequest` is the strict production bind:
program, model, piece list, required entry points, streams and sinks. Its
diagnostics are coded, not prose-matched, so composition and unit creation can
classify a missing program, a piece-count mismatch or a failed `Create` start
without parsing text.
Duplicate model-piece names remain valid: every COB name maps to the first
matching piece in model order, so stock models such as ARMCH bind and allocate
normally `[02 R-MALF-01 §2]`.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The per-piece image and
the thread windows. The writer persists each piece's current draw, cache and
shade flags alongside its animation state, position and angles. The reader
restores those flags with positive polarity; serialization has no caller-owned
scratch argument or replacement flag defaults `[08 R-SAVE-02 §9]`.

### 2.4 `internal/model`

`Piece` is one compiled 3DO object: name, parent, children, the authored parent
translation and the vertices, both in 16.16 after the half-turn pass, and the
primitives after `internal/model`'s one-time selection swap and stable mean-Y
order. `formats` retains the authored primitive order and selection word for
source-facing tools `[02 "Model archive (3DO)"]` `[03 §2.4]` `[fmt 3do]`.
`Model` is the immutable hierarchy. `PieceState` carries the three
`uint16` rotation accumulators and the script translation lanes; `Compose`
builds a piece's world transform by composing ancestors after descendants. A
leaf with a vertex and no primitive is a valid attachment and emission point,
not a defect.

## 3. Contracts

### 3.1 Units — C1…C3

**C1 — record identity, not layout.** Retail's unit record has a fixed byte
identity; Nanolathe uses named Go fields in a slot-indexed array parallel to the
pool and never packs or reinterprets a Go struct as that record. Allocation is
lowest-free with slot 0 null, no generation tags, immediate reuse
`[01 §6.1]` `[04 §2.3]` [I13].

**C2 — the sweep order, and alive versus dying.** The tick sweep visits players
ascending then slots ascending `[01 §6.2]`. Alive state and the death mark are
separate fields: `Destroy` sets the mark and the slot-end finalizer clears alive,
fires the death hook exactly once and frees the slot `[04 §2.4]`
`[04 R-MOV-03 §1]`.

**C3 — remaining, not progress.** Build progress is a `remaining` fraction
running 1 → 0. It is `float32` on the I2 allowlist, and `internal/construction`
is its only writer `[04 §2.3]` `[05 "Construction target state"]`.

### 3.2 Orders — C4…C9

**C4 — the descriptor table.** Four static batches of 23, 22, 22 and 1 records;
after every batch the whole table re-sorts ascending by canonical name, and an
order's identity is its index in the final sorted table. The empty name sorts to
0 and is the reject sentinel `[04 §3.1]` `[04 R-DOC04-C]`. Names, labels,
classes, acknowledgement bytes and gate masks are transcribed, never derived.

The comparator is **case-insensitive**, the C runtime's fold, and this corrects
the contract as PLAN 06 wrote it: it said a case-sensitive byte comparison, and
that put seven rows in the wrong order — the `Attack*` cluster and the `Build*`
pair invert under the fold, because lowercasing moves the underscore below every
letter instead of between the two letter ranges. A case-sensitive sort under a
case-insensitive search does not merely disagree: the search cannot find
`Attack_Chase` at all, which disables the chase attack `[04 R-STANCE-01 §9]`
`[04 §3.5]`.

The small class byte's reader is also now known, which retires PLAN 06's
standing question about it: it is the order-queue overlay's draw mask, and the
Shift-gated overlay walker dispatches five helpers off it — the build-site
marker, the travelling dash chain, the segmented circle, the queued-order icon
and the labeled range rings. Every observed value lies inside the five-bit mask
`[04 §3.1]` `[07 R-P0-11 §3]`. The acknowledgement byte is the same field
`[07 R-P0-11 §3]` calls the descriptor's icon byte; icon 0 encodes "no icon".

**C5 — the order record.** Retail's record has a fixed byte identity; Go uses
the named fields and per-family parameter meanings of `[04 §3.2]`, not a packed
layout [I13]. A unit keeps a primary segment and a rear segment, the rear
exclusively for `BuildWeapon` and `SelfDestruct`, each with its own anchor
`[04 §3.1]` `[04 R-ORDER-02 §1]`.

**C6 — the pump's gate test.** An arrived deadline clears and raises the record's
lowest satisfied bit `[04 R-ORD-01 §0]`. Satisfied is `(record.satisfied |
unit.pending) & record.gate`; a **nonzero gate with nothing satisfied stops the
walk for this tick**, stalling everything behind it in its own segment; the
rear segment's walk follows regardless `[04 R-ORD-01 §10]`. Otherwise the delivered bits are consumed from both
words and the dynamic gate is cleared. A satisfied interruption clears every
weapon target without changing slot posture before the handler runs
`[04 R-ORD-01 §10]` `[04 R-ORDER-02 §3]`.

The record constructor does **not** seed the dynamic gate from the descriptor's
static mask. They are different fields with different meanings, and conflating
them parked every row whose handler did not happen to clear the gate first. With
the constructor correct, a fresh record's gate is zero and step 3 passes on its
own; the pump's former phase-0 pre-dispatch clear is gone with it, because
`[04 §3.3]` gives the pump exactly one gate clear — step 4's, on the record it is
about to dispatch — and clearing at phase 0 would discard a wait a handler armed
`[04 §3.1]` `[04 §3.3]` `[04 R-ORD-01 §1]`.

**C7 — the primary result-code table.** Code 0 resets the phase; 1 advances it;
2 and 4 continue, which under the head-only walk means reload the front record
`[04 R-ORD-01 §10]`; 3 arms the lowest gate bit and deadline `tick + 30 +
rand(15)`; 5 and 8 unlink and free; 6 rotates the record to the segment tail as
a link move only, writing no flag — the marker travels with the record that
holds it; 7 frees every record on both segments and returns; 9 sets the
completion flag and, on a record with nothing after it in the segment, resets
the phase and re-arms with `tick + 30 + rand(30)`, otherwise unlinks; anything
above 9 is the single-node expiry helper, with no draw and no whole-queue cancel
`[04 §3.3]` `[04 R-P0-01]`.

"Last" means having no record after it, which is not the same as being the only
record: a handler that head-inserted a spawned record is behind that record and
can still be the tail `[04 R-ORD-01 §1]`. Both wait and last-record retry
reload the actual head after re-arming the dispatched record, so a head-inserted
order runs in the same pass `[04 R-ORD-01 §10]`.

**C8 — the secondary segment and its table.** The rear segment holds only
`BuildWeapon` (gate `0xc0140`) and `SelfDestruct` (gate `0x40040`); factory
products live in the **primary** segment. The walk reloads the rear head after
each non-returning dispatch, and advances only past records whose gate is
non-empty and whose deadline has not arrived — the compare
is unsigned, so the −1 sentinel reads as not due — and the handler is invoked
with an **empty** satisfied set: no expiry bit, no capability-word read
`[04 R-ORDER-02 §1]` `[04 R-ORD-01 §10]`. A zero final self-destruct delay
therefore executes its damage arm in the same pass; positive delays still wait
for their deadline `[04 R-SPEC-01 §13]`. Codes 6 and 7 both remove the single
record and return,
with no tail-yield and no cancel-all; 5, 8, 9 and above 9 are plain
unlink-and-free removals that continue the walk, and secondary code 9 never
re-arms and never draws `[04 §3.3]` `[05 "Queue pumping and result codes"]`.

**C9 — insertion, coalescing, cancellation and cleanup.** Producer insertion goes
after the active marker and moves the marker onto the new record; the
head-insert branch, taken on the head-insert or rear-segment static bits, writes
no marker at all `[04 R-ORD-01 §13]` `[04 R-ORD-01 §16]`. Counted adds coalesce
tail-only; `CancelTailMost` matches the tail-most record and tombstones it;
`CancelFrontMost` is the queued-order duplicate removal, front-most first, whole
node, no decrement `[07 R-P0-11 §6]`.

Cleanup order on every removal path is restore → handler cancel notification →
`StopBuilding` → release payload → `TargetCleared` **only if not tombstoned**.
The tombstone is set on every freed record except the one standing as the
primary segment's front head at that moment, and a rear-segment record is always
tombstoned because the test compares against the front anchor regardless of
segment — so `BuildWeapon` and `SelfDestruct` removals never emit
`TargetCleared` `[04 R-ORDER-02 §2]`. The cancel notification is arbitrary work
and can re-enter the queue, so the unlink re-locates the record by identity
after the cleanup rather than trusting an index taken before it.

In queue-modifier terms Replace is a non-queued purge, Append and Shift-queue
both insert after the marker without purging, and the pump's idle refill is the
auto head insert `[04 §3.3]` `[04 §3.4a]`. Purge survivorship is the static
gate's second bit `[04 R-MOV-03 §6]`.

### 3.3 COB — C10…C19, C25, C26

**C10 — instruction encoding.** One 32-bit word; bit 28 is always set; the
dispatch key is the word masked with `0x100FF000`, which selects bit 28 plus the
eight bits the tabulated opcode values vary. The low twelve bits are ignored
except by push and pop, where the low three are the addressing mode `[04 §4.3]`.

**C11 — the dispatch set.** Exactly 57 dispatched values. An unmatched key takes
the kill path: clear the thread's status, decrement the instance's active thread
count, and yield without waking call-script waiters or invoking a completion
receiver. The scheduler does not poll callee liveness to release those waits;
explicit return and signal supply the wake events `[04 §4.2]`.
There is no default handler and no retail diagnostic
`[04 §4.3]` `[04 R-COB-01 §1]`. Nanolathe implements this set with one
switch whose default enters that kill path; it does not keep a second lookup
table before executing the handler.

**C12 — operand shapes.** Six shapes, and the program counter is a **word**
index `[04 §4.3]`.

**C13 — threads.** Eight thread records per unit; the lowest clear thread-mask
bit is selected and the scan order is fixed. Each record carries 32 physical
window words shared by authored stack/local operations and save/restore. A `sleep 0` still costs
one tick: the sleep occupies its truncated tick count plus one guard decrement
`[01 §6.1]` `[04 §4.2]` `[04 §4.6]`.

**C14 — retail's undefined behavior is reproduced, not defended.** An unknown
pop addressing mode pops nothing and advances; divide has no zero and no
minimum-integer guard and faults; there is no modulo and no shift opcode;
`start-script` with a full pool retains its arguments; `call-script` with no free
slot waits on a sentinel and leaks `[04 §4.3]` `[04 §4.4]` `[04 R-COB-01 §1]`.
The fault census is closed: retail kills a thread only for an unrecognized
opcode value and for the thread-return opcode, which also fires the completion
receiver; a piece index is never bounds-checked.

**C15 — ports and callbacks.** Engine ports are numbered 1 to 20, and the write
opcode binds exactly six arms — there is no stand-ground, no per-weapon and no
cloaked write port; the compiled form pushes identifier then value, so the
opcode pops the value first `[04 §4.4]` `[04 §4.7]` `[04 R-P0-10]`
`[04 R-COB-03 §1]`. The callback arithmetic `[04 §5.1]` `[04 §5.3]`
`[04 R-CB-01 §2]`:

* the rock impulse is `(−cos(rel) × 800, −sin(rel) × 800)`, both negative, with
  no completion receiver;
* the hit impulse is `(cos(dir) × 400, sin(dir) × 400)`, applied before the
  damage callback, where `dir` is the packet's direction byte shifted into the
  full angle domain;
* the local death query's severity is `((−health × 100) / maxHealth + prior) / 2`
  as an unsigned divide, clamped to 1..100, where `prior` is the **previous**
  30-tick window's health sample — the unit tick keeps two adjacent sample bytes
  and shifts the current into the prior at each boundary, so the query consumes
  the older of the two;
* the maximum reload time is reported as `trunc(maxReload × 1000 / 30)`, scanned
  over all three slots and issued after `Create` so it lands outside `Create`'s
  own immediate drain;
* the transport query seeds one negative sentinel and three zeros; the landing-pad
  query seeds four negative sentinels, candidates are tried in order and the
  first free one wins.

**C16 — aim-ready.** Granted **only** by a nonzero value delivered to the `Aim*`
completion receiver. A failed start — script name absent, invalid identity, all
eight thread slots occupied — delivers zero through the same receiver; signal or
abnormal termination never invokes it. Zero neither grants nor revokes. There is
no timeout: a weapon whose `Aim*` never completes nonzero stays unable to fire
until the producer clears the aim state and re-issues. This is retail
`[04 §5.3]` `[04 R-CB-01 §6]` `[04 R-COB-04 §9]` `[06 §3.3]`.

PLAN 06 listed "an absent script" as a third zero case; that case cannot arise.
Every creator runs the synchronous primary-weapon query through the VM reference
with no null test, so retail faults at unit creation instead. Nanolathe rejects
required use at unit creation with the standard diagnostic shape,
while retaining the definition with a catalog warning (§2.1). It never creates
a scriptless unit `[04 R-COB-04 §8]` `[04 R-COB-01 §3]`.

**C17 — same-tick windows.** In order: unit update (the phase-2 session visit
calls the wind-generator notifier after the player gate, which queues the
direction and speed callbacks) → weapon update (queues target-cleared, the aim and fire
callbacks, the rock callback) → the normal drain (delta 1, eight thread slots
then one piece pass) → orders and build work → movement integration (the
immediate move-rate and occupancy callbacks) → slot-end death handling. Deferred
callbacks produced before the normal pass run in the same visit; an immediate
wake start performs its own all-slot delta-zero pass, which can run a later one
earlier `[04 R-MOV-03 §1]` `[05 R-PROD-01 §3]` `[04 §5.3]`
`[04 R-COB-02 §2]` [I7].

**C18 — move-rate tiers.** Category 0 when the inhibit bit is set, the unit is
attached, or both magnitudes are zero; otherwise category 1 at or below the lower
definition threshold, category 2 above the lower and at or below the upper, and
category 3 above both. The start-moving callback is emitted before the tier
callback, with an immediate-drain barrier `[04 §5.3]` `[04 R-CB-01 §5]`.

**C19 — the effect vocabulary.** Vector types 0 to 5 are piece-direction
effects; the three point types are white smoke, black smoke and sub-bubbles;
anything above them is ignored. Presentation-only and visibility-gated
`[04 R-COB-03 §6]` [I6].

**C25 — two trig implementations, never shared.** Callback arguments use the
fixed-point path: the 512-entry table, cosine a quarter turn ahead, products
rounded to nearest before truncation `[04 §5.1]`. The model draw path uses float
trig (C21). Sharing one implementation between them is wrong in both directions
[I2].

**C26 — the normal-kind damage packet's callback order.** Against an active,
not-yet-dying victim: **health is subtracted first**; then the hit callback
starts asynchronously with the impulse of C15; then the damage callback starts
independently with the post-hit percentage `clamp(health × 100 / maxHealth, 0,
100)` as an unsigned division. Either starter can fail separately. Heal and
paralyze kinds skip the pair entirely, and lethal damage against a
movement-category-1 or -2 victim sets the death latch and returns with **no**
callbacks `[04 §5.1]` `[04 R-CB-01 §3]`.

**C27 — explosion admission boundary.** `explode` synchronously offers typed
whole-piece, shatter and bitmap requests to an optional sink; nil means the
currently explicit no-consumer path. The physical record consumes exactly six
simulation draws in the order 3000, 3000, 3000, 40, 10, 40 before the source
hide and before the sink may refuse allocation. A shatter sink must test each
fragment admission before its eight fragment draws; a whole-piece sink admits
its debris slot and ring allocation after the six draws. The source hide occurs
for either physical kind even on refusal. Bitmap requests consume no simulation
draws and do not hide a bitmap-only source. The sink runs synchronously and
retains no record; session owns all bounded arena storage and effect-phase
updates. Smoke/fire select the established strip-9 smoke and flame-stream
classes, and the draw-side producer is wired end to end: the two engine bits
publish on `frame.DebrisView`, and the client makes the two CONTAINERS at the
piece, taking the smoke puff's last frame and the fire particle's four values
from its private presentation CRT copy — never the session stream — so frame
cadence cannot reach the tick. Their **persistence** is now reproduced: the
containers live on a presentation-owned store that steps them on the committed
tick (the puff's animation clock and its wind and gravity drift, the fire
container's `lifetime + 1` coincident segments) and draws them at barrier 9, so
a falling piece leaves the smoke and flame trail behind it that retail's
surviving containers draw. Both reductions `TODO(RT08)` carried are closed. One
divergence remains and is stated rather than hidden: retail admits one container
per RENDERED frame and this build admits one per burning piece per COMMITTED
tick, because it renders several frames per tick while the containers step by
the tick. All of it is owned by `DESIGN_PRESENTATION_CLIENT.md` C2.2. The stale
claimed-effect case remains open `[04 R-COB-04 §1]`–`[04 R-COB-04 §4]`
[I4] [I5] [I6].

**C27.1 — whole-piece admission position. Approved departure (2026-09-16).**

*Retail.* `[04 R-COB-04 §2]` requires the source unit's position plus the
piece's **last retained** translation, and explicitly forbids recomposing the
current pose at admission. `[04 R-COB-04 §3]` names the writers of that retained
value: allocation, and the deferred rebuild performed by **model drawing** and by
the **viewing-player-visible** effect-opcode refresh visit; COB transform setters
only request the rebuild. Both refresh sites are presentation events, so retail's
retained translation is as stale as the last time that unit was drawn or looked
at, and the admitted debris position is a function of render cadence and of which
player is viewing. That research stands as written and is not restated here.

*This build.* The adapter samples the **current recomposed piece pose** at
admission: `cob.Binding.ComposePiece` from live VM state, through the same
`pieceWorldPos` locator the bitmap and emit-sfx ports use. The admitted position
feeds the bounce, the impact sink and the effect admissions those produce, so
importing retail's value would put render cadence and the viewing player's
identity inside the authoritative tick [I4] [I6]. The user authorized this on
**2026-09-16** for that reason — "Don't put render cadence into the
authoritative tick" — extending to whole-piece debris the current-pose sampling
already approved for the **shatter** adapter in this section (2026-09-09).

*Gating.* Unconditional, in the shatter departure's exact form: it is **not**
selected through the central `gameplay.Mode`, and no new mechanism is introduced.
**Strict 3.1 samples the current pose too.** Retail's retained value is written
by drawing and by a viewing-player-dependent refresh, so no tick-ordered value
reproduces it; a Strict-only path could only invent a rebuild cadence, which
rule 1 forbids. This is a layering departure — where a value is read from — not
a gameplay rule, which is why it takes the shatter form rather than a
`gameplay.Mode` policy and is not listed among CLAUDE.md's gameplay policies
[I11].

*Boundary.* Admission **position** only. The six physical draws and their
3000/3000/3000/40/10/40 order, the velocity and angle-rate seed built from them,
the added source mover velocity, the lifetime, the four fall/on-hit/smoke/fire
flags, the source hide, the pool's allocation charge and every step rule are
unchanged, and both readings consume identical simulation draws. Nanolathe keeps
no materialized point list at all, so nothing else in the tick reads a retained
translation.

*Tests.* `TestWholePieceAdmissionUsesCurrentPiecePoseNotRetainedTranslation` in
`internal/session` locks the case where retail would differ — a piece a script
translated after spawn is admitted at the recomposed pose, not at the pose the
unit had when it was last composed — and asserts the six-draw census across the
admission. `TestCOBWholePieceExplosionPublishesDetachedSlot` continues to lock
the copied pose, source slot and publication. The shatter counterpart is
`TestShatterSamplesSimulationPoseAndPublishesDetachedGeometry`
`[04 R-COB-04 §2]` `[04 R-COB-04 §3]` [I4] [I6] [I11].

**DebrisPool API — whole-piece prerequisite.** `internal/render.NewDebrisPool`
owns a fixed 100-slot, 100,000-charge arena. `Admit(DebrisRequest) bool` takes
one already-seeded whole-piece request: immutable model/primitive identity,
copied point and render state, absolute world offset, the six-draw velocity and
angle-rate seed, lifetime, and the four semantic fall/on-hit/smoke/fire flags.
It scans slots in ascending order for the first empty one; its allocation
charge is 12 per vertex plus 110 fixed charges. Its cursor-forward allocation clears whole blocks
(and their slots): when the tail is too short it clears cursor-to-tail, wraps,
then clears from the start until the request fits. A remainder below nine
charges joins the new block; otherwise it remains the cursor's next free
block. Admission does no random work and retains no queue.
`Step(DebrisStepContext, DebrisImpactSink)` walks occupied slots in slot
order, applies the lifetime, terrain/sea, bounce, velocity and angle rules of
`[04 R-COB-04 §2]`, and synchronously calls the typed ground or water impact
method when an on-hit record requires one. `SnapshotInto` provides detached
inspection copies, while `SnapshotViewsInto` publishes only model metadata and
never duplicates the arena's mutable point storage. Session adapts the COB
request and binds this pool; frame publication and renderer consumption remain outside this API
`[04 R-COB-04 §1]`–`[04 R-COB-04 §2]` [I4] [I5] [I6]. The production adapter
retains the source raw slot, steps this pool before the fixed effect pool, and
publishes an immutable model identity, piece, pose and current source-owner
palette; the direct draw rebuilds the selected original piece with stepped
angles before the fixed effect category walks.

**Shatter core API.** `render.FixedEffectPool.AdmitShatter(FragmentRequest,
func(uint32) uint32) bool` takes the session-owned draw operation for this
synchronous call and claims one shared effect record and one first-free
fragment-geometry slot for each already eligible quad, stopping before the next
quad when the 300-record effect pool is full. It calls the scalar material
freezer only after that paired claim; unavailable art returns an invalid frozen
material but does not cancel geometry or its eight simulation draws. A fragment
record carries a one-based `FragmentSlot`, copied into its immutable
`frame.EffectView`; `FragmentMetadataInto` enumerates the slot-owned geometry,
and publication joins it to the stable effect view order. `SetFragmentStepContext`
installs terrain, sea and synchronous impact admission. The normal per-record
fixed-effect update owns fragment movement, contact callback and compaction;
the callback runs before geometry and record release, so its own attempted
effect admission observes the source record still consuming capacity. The core
accepts posed quads. **Approved departure (2026-09-09):** the session samples
the current simulation pose when COB explodes the piece. It remaps live COB
states to model pieces, folds unit orientation into the root, and uses the
existing model transform for every eligible quad and the piece origin.
This replaces retail's retained drawing/effect-refresh history deliberately:
rendering never feeds fragment physics. C27.1 extends the same reasoning, in the
same unconditional form, to whole-piece debris admission. The half mover velocity, paired pool
admission, eight draws, contacts and frozen material rules remain unchanged.
The battle-owned phase-7 texture registry supplies only scalar material identity;
it advances independently of rendering. The frame publishes detached vertices,
angles and the concrete admission-time texture frame to both renderers. Vertex centering changes
only copied geometry, never the fragment's already written world position `[04
R-COB-04 §3]` [I2] [I4] [I5] [I6]. The core uses portable binary64 for the
normal helper's transient square/sum/square-root/divide work and stores the
researched binary32 boundaries; the retained-list input range that could expose
a difference from retail's wider working precision remains a documented question.

**Bitmap production adapter.** The session binds its explosion sink before
`Create`. Each selected bitmap is admitted synchronously into the existing
fixed effect pool with named art and calculated table 2. Successful admission
above the signed whole-unit sea boundary also invokes the existing land-dust
producer. Binding authored timing after `Create` activates unresolved primary
players in place, preserving identity, order and secondary animation; it does
not restart resolved players or add a per-frame retry. Whole-piece requests
enter the DebrisPool; the shatter session adapter supplies eligible quads,
current source context and frozen material to the paired fixed-effect core
described above `[04 R-COB-04 §1, §3]`. Ground debris impacts use calculated table 0; the
direct draw adapter rebuilds original vertices with the stepped angles
`[04 R-COB-04 §2]` `[03 R-COMP-02 §6]`.

**Whole-piece pool validation.** The bounded pool passed independent review,
`tools/check`, `tools/check-retail`, and the GPU device fixtures after integration.
A sequential scene-version-3 Ashap Plateau comparison used seed 7, factories,
1920×1080, 30 TPS, 60 warm-up draws and 180 measured draws. Both renderers kept
identical per-frame censuses and byte-identical captures against the accepted
baseline; this comparison preceded the production adapter. Classic record
median/p95/max changed from 13.330/18.385/21.022 to 13.335/14.870/15.980 ms;
modern from 4.271/4.701/5.339 to 4.298/6.236/13.081 ms. Classic allocations were
0.891→0.892 MB/frame and cadence share 96→98%; modern allocations were
1.805→1.867 MB/frame and cadence share 91→88%. These individual runs establish
no performance improvement. The workload included 187–198 units, 6–31
projectiles, 73–162 effects, 4–8 nanoframes/nanolathe events, and four shake
frames. The whole-piece adapter now connects admission, publication and both
renderer paths. Shatter session admission, publication and drawing now use the approved
simulation-pose source described above. A shatter during initial battle `Create`
can precede registry binding and therefore retains invalid material while its
physics still runs; no later drawing pass retries or changes that admission.
Ordinary in-battle creation uses the already bound registry. The
smoke/fire producers are wired at the client draw, from the presentation CRT
copy, and their containers persist on the presentation store and draw at
barrier 9; RT08 is closed — see C27 and `DESIGN_PRESENTATION_CLIENT.md` C2.2.

**Shatter production validation.** Sequential classic and modern runs of the
same scene-version-3 Ashap Plateau benchmark (seed 7, factories, 1920×1080,
30 TPS, 60 warm-up draws, 180 measured draws) published identical simulation
censuses: 10–61 fragments, 189–198 units and 4–8 active construction effects.
Both captures were inspected. Classic recording median/p95/max was
8.920/11.723/15.227 ms, with 1.110 MB allocated per frame and 99% cadence share;
modern was 4.448/7.587/10.224 ms, 9.550 MB/frame and 68%. Bounding fragment
GPU targets reduced modern allocations from the initial viewport-sized
implementation's 18.265 MB/frame. The pre-shatter baseline was 5.219 MB/frame
and 52% cadence share in modern; admitting fragments changes simulation RNG
consumption and therefore the battle workload, so this is not an isolated
performance comparison. GPU allocation overhead remains measurable. The target-size
change preserved the classic capture exactly; modern differed at 59 pixels
(58 shadow-edge brightness changes and one colour texel), with no shifted
fragment bodies. Exact modern pixel neutrality is not claimed.

### 3.4 Model — C20…C24

**C20 — load-time order is fixed at load.** After relocation and before any
draw: if the object declares a selection primitive, swap it with primitive zero
and rewrite the selection index to zero; bubble-sort the remaining primitives
ascending by the integer mean of their vertices' second coordinate. Then a
recursive pass negates the first and third vertex coordinates and the first and
third parent translations of every object — a half-turn about the vertical axis.
A per-frame sort does not reproduce retail's tie order `[03 §2.4]`.

**C21 — composition.** `world(v) = M_root · … · M_leaf · v` with `M_i = T(t_i) ·
R_i`: each piece rotates about its own origin **first**, then translates. The
per-node translation is the componentwise sum of the script translation lanes and
the authored parent translation. Rotation composes three `uint16` accumulators
applied Z, then X, then Y, evaluated in floating point with round-to-nearest —
**not** through the fixed-point trig tables, which serve simulation velocity
integration only `[03 §2.4]` [I2].

**C22 — one adapter for three verbs.** Turn, turn-now and spin converge on the
same accumulators: one angle per axis, last writer wins, no separate aim stage
`[03 §2.4]` `[04 §4.6]`.

**C23 — vertex-only leaves are real.** A leaf piece with a vertex and no
primitive is a valid attachment and emission point `[03 §2.4]`.

**C24 — the unit's attitude folds into the root piece.** Bank, heading and pitch
occupy the root piece's Z, Y and X angle slots respectively and compose as the
outermost factor of the chain. **Unit position never enters piece math** —
pieces transform around the model origin, and position enters only at final
screen placement. Projectile models reuse the identical helper with yaw in the Y
slot and pitch in the X slot, each carrying a constant negative half-circle
authored model-facing offset `[03 §2.4]` `[03 §5.2]`.

### 3.5 The handler census

Retail compiles a handler into all 68 descriptors. This table is the same census
for this build: 67 named rows plus the sentinel, each named exactly once, and no
row without an owner. A package test walks the built table and fails on any
named descriptor carrying neither a descriptor handler nor an owning subsystem's
queue registration, so the census is checked rather than asserted.

Two of the four in the last group carry a zero static gate and therefore
dispatch on sight — `SelfDestructFG`, which a live mission reaches through the
`InitialMission` interpreter, and `VTOL_Evade`.

| Owner | Descriptors |
|---|---|
| `standing.go` | `Cloak_Off`, `Cloak_On`, `Paralyze`, `Standby`, `Standby_Mine`, `Standing_FireOrder`, `Standing_MoveOrder`, `Teleport`, `Wait`, `WaitForAttack` |
| `work.go` | `Capture`, `HelpBuild`, `Reclaim`, `RepairUnit`, `RepairUnitNoMove`, `Resurrect`, `SelfRepair` |
| `vtolwork.go` | `VTOL_HelpBuild`, `VTOL_Reclaim`, `VTOL_RepairPatrol`, `VTOL_RepairUnit` |
| `combat.go` | `AttackSpecial`, `AttackUType`, `Attack_Kamikaze`, `Attack_NoMove`, `Guard_NoMove`, `Suppress` |
| `vtolair.go` | `AirStrike`, `AirToAir`, `AirToGround`, `AirToGroundHover`, `VTOL_Evade`, `VTOL_GetRepaired`, `VTOL_LandIfCan`, `VTOL_Landing`, `VTOL_SeekAttack`, `VTOL_SeekGuard` |
| `patrol.go` | `Patrol`, `QMove`, `QPatrol`, `RepairPatrol`, `VTOL_Move`, `VTOL_Patrol` |
| `transport.go` | `BeCarried`, `Ground_Pickup`, `Ground_Unload`, `VTOL_Pickup`, `VTOL_Unload` |
| `resolve.go` | `Attack_Chase`, `Follow_Ground`, `VTOL_Follow` |
| `selfdestruct.go` | `SelfDestruct`, `SelfDestructFG` |
| `pump.go` | `Move_Ground` |
| `park.go` | `Park` |
| `stop.go` | `Stop` |
| `activation.go` | `Activate`, `Deactivate` |
| `selectable.go` | `MakeSelectable` |
| the stockpile file | `BuildWeapon` |
| `internal/construction`, per queue, inside the pump visit | `GetBuilt` |
| `internal/construction`, per queue, from its own per-unit step | `BuildingBuild`, `MobileBuild`, `VTOL_MobileBuild`, `ReclaimUnit`, `VTOL_ReclaimUnit` |
| `internal/movement`, per queue, inside the ordinary pump visit | `VTOL_Standby` |
| none — the reject sentinel | the empty name |

Notes the table cannot carry:

* Scripted attachment and the air pickup completion share `RearmBeCarried`.
  It replaces eligible local cargo's unprotected front orders while preserving
  protected and rear records; airbase attachment leaves orders alone
  `[04 R-UNIT-06 §3]` `[04 R-AIR-01 §9]`. `BeCarried` releases slots through
  the guarded release helper. Air unload keeps its observed order target
  separate from the live cargo-list head used for lowering and release
  `[04 R-ORD-01 §7]` `[04 R-AIR-01 §10]`.
* `ContinuePrimaryWork` resumes construction's admitted primary visit without
  repeating secondary effects. Air mobile build applies pump result codes in
  that window, keeping the record as its only phase owner
  `[04 R-ORD-02 §2]` `[08 R-SAVE-ORDER-01]`.
* Autonomous combat and repair-patrol issuers retain their return move beneath
  the temporary attack or assistance record. Each issuer applies its own stance
  admission and leash rules; completion exposes the saved move through the
  ordinary queue pump `[04 R-STANCE-01 §4]`.
* Work cancellation is handled before the phase body can contribute another
  work step. Moving-target restarts and aircraft arrival failures use each
  row's own result and deadline, rather than inheriting a ground or factory
  retry `[04 R-ORD-01 §5]` `[04 R-ORD-01 §7]`.
* `Move_Ground` alone is installed by the move-family installer. It once
  installed that one row's body on all eight names of the family, which is why a
  `Patrol` walked to its first waypoint and completed and a `RepairPatrol`
  neither patrolled nor repaired: a family installer assigns only where the
  handler is still nil, and that list ran first `[04 R-ORD-01 §4]`
  `[04 R-ORD-02 §2]`.
* `QMove` and `QPatrol` are rally markers, not moves: their body is a 60-tick
  delayed tail rotate with no goal binding, and the factory's `GetBuilt` copies
  them onto each finished product `[04 R-ORD-01 §2]` `[04 §3.8]`
  `[04 R-P0-09]`.
* The two mobile-build rows carry an owned handler rather than the
  externally-driven shorthand, because their approach phase parks on the gate
  `0xE0` — the follower reached the goal, an empty route was published away from
  it, a goal object was released — and the pump's satisfied set *is* that wake.
  The body still reports it did not advance the record on every arm but the
  row's abandon `[04 R-ORD-01 §0]` `[05 R-WORK-01 §13]`.
* `internal/orders` publishes the mobile-build blocked-area budget as two pure
  helpers the construction service calls, because the reach test measures against
  the product footprint: a blocked visit notifies "Waiting for target area to
  clear", increments the record's counter and waits **exactly 30 ticks with no
  random draw** while the counter is at most 10, and the first blocked visit past
  that notifies "Target area was blocked" and abandons; an approach the search
  cannot satisfy ends only on the route publisher's "cannot get there" bit, with
  the text "I can't reach the construction site" `[04 R-ORDER-02 §1]`
  `[04 R-ORD-01 §5]`.

`RepairPatrol` runs the bound repair-candidate scan and resource-gated feature
pairing; `VTOL_Patrol` runs pad selection and its opportunity scan. These use
the queue binding's enumerators and simulation RNG, including the no-candidate
arms `[04 R-ORD-01 §4]` `[04 R-ORD-02 §2]` [I4]. The implementations live in
`internal/orders/patrol.go` and the shared scan helpers. Existing
`TestPatrolScansKeepSlotOrderAndDrawOnlyAfterGates`,
`TestOpportunityScanIsFireAtWillOnly` and `TestVTOLPatrolSeeksAPadOnlyWhenHurt`
lock the scan ordering, gates and pad-selection boundaries.

The command-owned `ResolvePos.InterfaceType` captures the issuing session’s
live option, also read by cursor dispatch. This avoids a process-global
resolver setting while retaining both contextual ladders. Code 3 uses runtime
weapon slots, the committed mover mode, and the queue binding’s sea level for
target-class admission. AI group broadcasts bind fresh units before resolving
their first order [04 R-ORD-02 §1] [07 R-CAM-01 §5].

### 3.6 Scope and remaining work

* **The empty-name descriptor's handler is the reject sentinel, and nothing
  else.** Retail's row 0 has a handler; its body is not a behavior any producer
  can reach, because `Lookup` returns 0 exactly on a miss `[04 §3.1]`
  `[04 R-ORD-01 §12]`. We nevertheless install retail's trivial
  complete-and-free handler on row 0 as a guard, so that if a row-0 node ever
  did reach the pump it would complete silently as retail does rather than
  fall into the nil-handler park, which draws RNG and would be a determinism
  divergence. Row 0's state label is not inert either: `Ready` is the idle
  footer caption a unit with no head order record shows
  `[07 R-HUD-03 §2]`, so the label is carried, not blanked.
* **The stock ARMCK lifecycle diagnostic observes normal `Create` completion.**
  `internal/units/p28_cob_pose_trace_test.go` installs its observer before
  `Create` starts, so a child reusing the completed thread's slot cannot erase
  the observed exit. The remaining Unknown is the first committed retail pose;
  the lifecycle observation alone does not establish its timing
  `[04 R-P28-COB-01R]` [04 "Missing and unknown"].
* **`canstop` has no simulation reader, and `teleporter` is inert.** The
  `Teleport` order is ungated and free, and the interface latch that would arm it
  has no writer anywhere in retail — it is consumer-only `[04 R-SPEC-01 §2]`
  `[04 R-STANCE-01 §8]` `[07 §9]`.
* **The bugger-off port is a stored bit with no engine reader.** It is written by
  the write arm, read back by the read arm, cleared at creation and serialized,
  and nothing in the simulation consults it `[04 R-COB-05]`.
* **The unit-record fields whose retail consumers are still Unknown are carried,
  not interpreted.** They round-trip through the save boundary under neutral
  names and no code branches on them `[08 R-SAVE-02 §6]`
  `[08 R-SAVE-UNIT-01]`.

## 4. Retail behaviour that is not a bug

* **In Strict 3.1, units piling up at a factory exit are not waiting for a broadcast.** The
  script port usually read as a "bugger off" flag has no engine reader at all
  `[04 R-COB-05]`. What retail does is the silent 15-tick exit retry, the
  refused yard close and an ordinary blocked mover `[04 R-FAC-02 §5]`
  `[04 R-P0-08]`. Do not build a scatter or crowd-avoidance rule on that flag.
  Modern independently requests ordinary moves from eligible idle blockers,
  as specified in [DESIGN_ECONOMY_CONSTRUCTION "Modern factory-exit yielding"](DESIGN_ECONOMY_CONSTRUCTION.md#modern-factory-exit-yielding).
* **A column of products behind a factory is a defect, not retail.**
  `[04 R-EGRESS-01]` composed the column from four traces and concluded it was
  retail; **`[05 R-EGRESS-02]` supersedes it on that verdict**. Retail's no-rally
  products fan out around the whole border of the shared park rectangle; a column
  forms only when the follower's route-acceptance rule is applied at route
  *publication* instead of at the follower's goal installer `[04 R-PATH-01 §8]`.
  The prohibitions that still stand are the rest of `[05 R-EGRESS-02]`: nothing
  pushes a mover already parked, and the goal point is not made per-mover.
* **A no-rally aircraft hovering above its plant is retail.** Park becomes a
  vertical move to the aircraft's own position, so the order completes at the top
  of the initial climb `[04 R-AIR-02]`.
* **A map authoring zero gravity cancels every `AirStrike` order** (SC23,
  `[04 R-AIR-01 §8]`). Bombers idle there in retail too.
* **A weapon whose `Aim*` returns zero can never fire.** There is no timeout and
  no fallback; the latch is granted only by a nonzero completion. Treating a
  missing `Aim*` as success inverts the contract and turns every script-less
  turret into an always-ready one `[04 R-CB-01 §6]` `[06 §3.3]`.
* **A tight non-yielding script loop wedges the drain.** There is no per-visit
  iteration cap, and retail wedges the same way. So does a handler that loops
  through the continuing result codes: every handler returning 2 or 4 has first
  armed a gate or changed the segment head, or the walk does not terminate
  `[04 §4.3]` `[04 R-ORD-01 §10]`.
* **A blocked primary record stalls only its own segment.** The secondary
  walk runs after the primary returns, skips gated rear records, and reloads
  its head after dispatch `[04 R-ORD-01 §10]`.
* **A build angle of 4096 scatters every freshly built unit's heading by about
  20° around down-screen.** That is the authored field, applied as authored
  `[04 §2.3b]` `[04 R-P28-ANG-01R §2]`.
* **A paralyzed unit is stopped by its order row, not by its flag.** The stun is
  binary — fully stopped for exactly the credited ticks, no speed scaling, no
  movement-class change, no per-tick decrement — and what stops the unit is the
  head wait task plus the release verb and unconditional target clear on all
  three slots that the `Paralyze` row performs. The flag's only two readers test
  a *candidate*, in the shared autonomous target search and the computer player's
  picker `[04 R-ORD-01 §2]` `[06 R-DMG-01 §11]`.
* **A construction KBot's initial pose comes from its own script, not from a
  pose the engine imposes** `[04 R-P28-COB-01R]`.
* **`sortbias` is inert and `digger` is presentation only.** Neither has a
  simulation reader `[04 R-SPEC-01 §3]` `[04 R-SPEC-01 §7]`.
* **A restored order's phase byte is meaningless outside its handler.** The save
  carries it verbatim precisely because handlers own its interpretation; do not
  normalise it on load `[08 R-SAVE-ORDER-01]`.

## 5. Divergences

* **SC8 — the two code-9 re-arm jitters are distinct arms.** Doc 04's result-code
  table gives code 3 the base 30 plus a draw below 15; doc 05 gives code 9's
  last-record arm the base 30 plus a draw below 30. Both are right about their own
  arm: retail reaches the shared deadline calculation through two paths with
  different bounds `[04 R-P0-01]` `[04 §3.3]`. Both pumps use the same split, and
  it is observable only as the re-arm cadence of a completed last order.
* **SC16 — the active-state bit and the empty current-task field.** Closed. The
  bit is the classifier-eligibility bit of the runtime status word, written by the
  allocator initializer, cleared by death finalization and by the `InitialMission`
  postlude, and set again by `MakeSelectable`; nothing collides with it. The
  "empty current-task field" is the **remaining-build fraction**, not an order
  field — no routine of the order subsystem stores to it — so the selection
  predicate is the eligibility bit plus construction complete, and reads no order
  state at all `[04 §3.6]` `[07 R-WGT-01 §10]` `[08 R-TRIG-01 §3]`.
* **SC17 — the order-queue caps were inside stock behavior.** A census of the
  reference install finds stock `InitialMission` scripts producing well over the
  former 64-record primary cap, so that cap changed a retail mission. Both caps
  are replaced by dynamic storage matching retail's heap-linked list; the only
  remaining divergence is an out-of-memory guard set far outside anything stock
  or any plausible player shift-queue, and a pump iteration guard the same census
  shows is unreachable [I11].
* **SC21 — `BMcode` marks structures, not factories.** A stock factory authors
  `CanMove = 1`, so any factory-versus-mobile branch keyed on mobility is wrong.
  `BMcode == 0` and "has a yard map" are the same set. The building-class status
  bit is set from the authored `BMcode` at creation, and the interface's
  placement-versus-queue branch keys on the **product's** `BMcode` `[04 §6.2]`
  `[07 §9]`.
* **SC18 — deterministic initialization.** The default allocator does not fill
  and allocation failure terminates the process `[01 R-PLAT-01 §5]`. Go zeroes
  are host policy. Unit creation implements the common initializer's named
  writes and the weapon initializer's reload, stockpile and control-bit writes
  `[04 R-UNIT-06 §7]` `[06 R-WPN-05 §3]`; there is no generic fill length to
  recover. Whether commanded weapon yaw/pitch can be read before a later writer
  initializes them remains a concrete first-reader question. The COB loader's
  malformed-input checks are a separate I11 boundary `[04 §4.1]`.
* **The interpreter bounds-checks the authored stack.** Pushes, pops and local
  indices are checked against the 32-word physical thread window instead of
  writing outside it the way retail's unchecked frame arithmetic does. A Go slice
  write cannot reproduce memory unsafety. This is the I11-sanctioned bounds-check
  exception, and thread-kill
  recovery is not claimed as retail behavior `[04 §4.3]` `[04 R-COB-01 §1]`.
* **Two divides retail leaves unguarded are clamped.** The death-severity query
  and the health-percentage read divide by the definition's maximum-damage field
  with no guard, so an authored zero faults retail's process outright. Both clamp
  here, named as I11 divergences rather than traced behavior `[04 §5.1]`
  `[04 §4.3]`.

### Self-destruct countdowns 6 and 7

**A deliberate handling of undefined retail behaviour — not a Modern gameplay
policy.** It does not go through `gameplay.Mode` and does not vary by mode:
Strict 3.1 and Modern behave identically here, because there is no retail
behaviour to be strict about.

Retail's countdown announce is a six-entry table built in the handler's own
stack frame, holding the status kinds 22, 21, 20, 19, 18 and 17 for the
remaining counts 0 through 5, indexed directly by the remaining count with no
upper bounds check `[04 R-ORD-01 §14]`. Remaining counts of 6 and 7 are
reachable, because the authored `selfdestructcountdown` is masked to three bits
with no clamp, and retail then reads a word from beyond the table and passes it
to the cue emitter as a kind. No cue kind is defined for those two counts; the
result is undefined `[04 R-SPEC-01 §13]`. The emitter is gated on the unit
belonging to the local player, so on any other player's unit those counts
already pass silently in retail.

**This port publishes no status cue for remaining counts 6 and 7.** Counts 0
through 5 keep the tabulated kinds unchanged, and the countdown itself is
untouched at 6 and 7: the same number of visits, the same 30-tick spacing, the
same remaining-count sequence, the same gate bit, and the same single draw at
the count-0 step. Nothing else in the retail handler varies with the count
beyond its zero test, so silence is the whole of the difference.
`internal/orders/selfdestruct_test.go` locks all three halves of that — the six
tabulated kinds, the silence above them, and the unchanged timing and draw.

Three reasons for silence rather than an invented cue. A Go port cannot
reproduce memory unsafety, so retail's actual outcome is not available to copy;
continuing the arithmetic past the table (the port's earlier `22 − count`, which
produced the `Visible` and the caption-less capture kinds) was extrapolation
presented as retail behaviour, and is exactly what `[04 R-SPEC-01 §13]` retracts;
and silence is already an outcome retail itself produces for these counts, on
every unit the local player does not own.

The edge is unreachable from shipped content. A census of the reference install
— 821 unit FBI sections across every mounted archive, shadowed copies included —
finds twelve sections authoring the key at all, all of them mines in the Core
Contingency data archive, with the values 1 and 2; nothing authors 6 or 7, and
nothing authors a value that wraps onto them. The remaining sections leave the
key absent, which stores the default field value 5. Only third-party content can
reach this path. The runtime symptom on retail — a fault, or a garbage caption —
remains **Unknown**, and settling it needs one manual retail observation with an
authored unit; the choice above does not depend on which it turns out to be.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Player slots, definitions, instance state, flags and transitions | `[04 §2]`, `[04 §2.3]`, `[04 §2.4]` |
| Player-slice order at battle entry | `[04 R-P0-16-A]` |
| The per-player unit sweep, step by step; queue helpers and the purge-survivor bit; the target-hit observer bit | `[04 R-MOV-03 §1]`, `[04 R-MOV-03 §6]`, `[04 R-MOV-03 §7]` |
| Unit initialization: the draw order, the build angle, the bob phase | `[04 R-P28-ANG-01R §2]`, `[04 R-MOV-01 §5c]` |
| The special-behavior FBI keys and their reader census | `[04 R-SPEC-01 §0]`–`[04 R-SPEC-01 §15]` |
| Guard assistance: retargeting, sizing, the wake producers | `[04 R-UNIT-06 §1]`, `[04 R-UNIT-06 §5]`, `[04 R-ORD-01 §8]` |
| The activation edge and its two callbacks | `[04 R-UNIT-06 §2]` |
| The `isairbase` mirror and the carrier clause | `[04 R-UNIT-06 §3]` |
| Stance values, the writer chain, the standing gates, defaults, inheritance, save, RNG | `[04 R-STANCE-01 §1]`–`[04 R-STANCE-01 §8]` |
| The descriptor table's sort comparator and lookup | `[04 R-STANCE-01 §9]` |
| The 68-descriptor table, verbatim, and the static mask's census | `[04 §3.1]`, `[04 R-DOC04-C]` |
| The order record and its per-family parameter meanings | `[04 §3.2]` |
| The queue pump, both result-code tables, the idle refill | `[04 §3.3]`, `[04 §3.4a]`, `[05 "Queue pumping and result codes"]` |
| The code-3 and code-9 deadline draws | `[04 R-P0-01]`, `[04 §8.3]` |
| The head-only primary walk; the secondary walk's skip | `[04 R-ORD-01 §10]` |
| The pending word's bits and who arms them | `[04 R-ORD-01 §0]`, `[04 R-ORD-01 §6]` |
| The shared handler vocabulary every body is written in | `[04 R-ORD-01 §1]` |
| Trivial, standing and wait handlers; combat; ground movement; work; the VTOL twins | `[04 R-ORD-01 §2]`, `[04 R-ORD-01 §3]`, `[04 R-ORD-01 §4]`, `[04 R-ORD-01 §5]`, `[04 R-ORD-01 §7]` |
| The weapon-slot control byte and the attack-chase slot pick | `[04 R-ORD-01 §7]`, `[06 R-WPN-05 §3]` |
| One bound payload per mover, and where a rebind's release bit lands | `[04 R-ORD-01 §9]` |
| The under-construction wake bit and the decay window | `[04 R-ORD-01 §11]` |
| Six handler details re-read: the annihilate reject, descriptor 0, self-repair, the guard bit, the repair arm, the assist footprint | `[04 R-ORD-01 §12]` |
| The record constructor, the four runtime bits of the static-mask copy, and their writers | `[04 R-ORD-01 §13]` |
| The self-destruct row's count word | `[04 R-ORD-01 §14]` |
| The factory product's own queue, and the head-insert branch | `[04 R-ORD-01 §15]` |
| The static head-insert column, row by row | `[04 R-ORD-01 §16]` |
| `VTOL_HelpBuild`'s third precondition; the build rows' unchecked product index | `[04 R-ORD-01 §17]`, `[04 R-ORD-01 §18]` |
| Command resolution exactly; the VTOL rows; the scan visitors and tail append | `[04 §3.4]`, `[04 §3.5]`, `[04 R-ORD-02 §1]`–`[04 R-ORD-02 §7]` |
| Handler retry and pre-reject mapping; tombstone, target-clear and stop-building cleanup | `[04 R-ORDER-02 §1]`, `[04 R-ORDER-02 §2]` |
| Factory completion, activation and rally inheritance | `[04 §3.8]`, `[04 R-P0-09]` |
| The product is cargo: attach, carry, detach, and the order form after release | `[04 R-FAC-02 §1]`–`[04 R-FAC-02 §4]` |
| The blocked exit's silent retry and the placement predicate the build rows wait on | `[04 §6.4]`, `[04 R-P0-08]`, `[04 R-FAC-02 §5]` |
| Release-boundary audits at the factory | `[04 R-FAC-01]`, `[04 R-FAC-01B]` |
| Why no-rally products queue at an exit — superseded on the verdict by `[05 R-EGRESS-02]` | `[04 R-EGRESS-01]` |
| The queued-order duplicate toggle and the overlay draw mask | `[07 R-P0-11 §3]`, `[07 R-P0-11 §6]` |
| The selection eligibility predicate | `[07 R-WGT-01 §10]`, `[07 §9]` |
| Goal families and the work rows' approach gate | `[04 §7.2]`, `[04 R-PATH-01 §12]`, `[05 R-WORK-01 §13]` |
| The air marker family, the takeoff preamble, standby and the seek states | `[04 R-AIR-01 §4]`, `[04 R-AIR-01 §6]`, `[04 R-AIR-01 §7]`, `[04 R-AIR-01 §8]` |
| COB loading, relocation, the script and piece tables | `[04 §4.1]`, `[fmt cob]`, `[02 "Compiled script archive (COB)"]` |
| The thread scheduler, the eight slots, sleeps and waits | `[04 §4.2]`, `[04 §4.6]` |
| Opcode encoding, the six operand shapes, the dispatch set, the kill path | `[04 §4.3]` |
| VM initialization, engine-call frames, the random-draw census, the single script boundary | `[04 R-COB-01 §1]`, `[04 R-COB-01 §2]`, `[04 R-COB-01 §3]` |
| The engine port set, read arithmetic, packed halves, write arms, transport reads, the effect opcode | `[04 §4.4]`, `[04 R-COB-03 §1]`–`[04 R-COB-03 §6]` |
| Engine-port write semantics, the factory stance handshake, the thread-start masks | `[04 §4.7]`, `[04 R-P0-10]` |
| The script-touched marker is order gate bit `0x4` | `[04 R-COB-06]` |
| The bugger-off port has no engine reader | `[04 R-COB-05]` |
| Explode, debris, shatter, bitmap explosions, the reserved opcode, statics as raw heap | `[04 R-COB-04 §6]`, `[04 R-COB-04 §7]`, `[04 R-COB-04 §1]`–`[04 R-COB-04 §5]` |
| The scriptless crash at creation; the speed and direction units; the aim handshake | `[04 R-COB-04 §8]`, `[04 R-COB-04 §9]` |
| The complete callback table, three corrected readings, the creation sequence, the aim completion target | `[04 §5.1]`, `[04 §5.3]`, `[04 R-CB-01 §2]`, `[04 R-CB-01 §3]`, `[04 R-CB-01 §4]`, `[04 R-CB-01 §6]` |
| The two unrelated producers of the direction and speed callbacks | `[04 R-CB-01 §5]` |
| Vertical-slice callback and port mappings; the same-tick integration trace | `[04 R-COB-02 §1]`, `[04 R-COB-02 §2]` |
| The construction-KBot initial pose boundary | `[04 R-P28-COB-01R]` |
| The weapon-query path, the query seeds, aim dispatch | `[06 R-P0-07]`, `[06 §3.3]` |
| The slot control byte and the "could not fire" bit | `[06 §1.2]`, `[06 R-WPN-05 §3]`, `[06 R-WPN-05 §6]` |
| The damage-intake observer notice and the damage flash | `[06 R-WPN-04 §2]` |
| What the stunned bit gates, and why the stun is binary | `[06 R-DMG-01 §11]` |
| Death causes and the death packet | `[06 §12.1]` |
| The stockpile row's encoding and launch-before-production ordering | `[06 §11.1]` |
| The piece hierarchy, load-time reordering, the half-turn pass, composition | `[03 §2.4]`, `[fmt 3do]` |
| Projectile model composition | `[03 §5.2]` |
| Render piece state and the composition-cache purge; the vertex pipeline floors | `[03 R-COMP-02 §3]`, `[03 R-RAST-01 §2]` |
| The default-mission-type key | `[02 R-KEYS-01 §1]` |
| The unit, order and script save boxes | `[08 R-SAVE-UNIT-01]`, `[08 R-SAVE-ORDER-01]`, `[08 R-SAVE-02 §6]`, `[08 R-SAVE-02 §9]`, `[08 R-SAVE-02 §14]` |
| The selection and eligibility clauses the trigger layer spells out | `[08 R-TRIG-01 §3]` |

## 7. Not implemented and open

One `TODO` marker stands in these four packages, and two more stand at the
producer that drives this package's queued-order toggle.

* `TODO(question)` at the queued-order duplicate toggle's producer
  (`internal/session/commands.go`, calling `Queue.CancelFrontMost`): whether the
  world-click producer receives a goal point alongside a target handle. The match
  rule states both arguments optional and does not say which the click supplies
  for a target-click order; the click is documented as issuing at the pointer's
  world point, so this boundary supplies both and the goal term participates. If
  retail passes no goal there, a repeat Shift-attack-click on a target that has
  moved more than a cell would remove the order where this build re-queues it. A
  trace of the world-click handler's call into the producer settles it
  `[07 R-P0-11 §6]` `[07 §9]`.
* `TODO(question)` at the same site: whether the interface's non-world-click
  queued issues share that producer. The rule is scoped to "every world order the
  interface issues", and the two world-click boundaries are its only callers here;
  the side panel's own buttons issue no world point, and nothing says whether a
  Shift-held press of one runs the test — which would make a second Shift-press
  cancel the first. A trace of those button handlers settles it `[07 R-P0-11 §6]`.

The questions the contracts above still carry, each with the observation that
would settle it:

* Which static gate-mask bits beyond the four named ones mean anything. The
  remaining bits have no located reader in the census; the raw mask is stored
  verbatim and never interpreted. Adding a reader needs a new finding, not a
  guess `[04 §3.1]` `[04 R-DOC04-C]`.
* Whether newly allocated or reused weapon slots expose commanded yaw/pitch
  before an aim or restore writer. The initializer bodies do not write those
  fields; their first-reader census remains open (SC18) `[04 R-UNIT-06 §7]`.
* The exact first committed retail ARMCK pose. The lifecycle diagnostic now
  observes `Create` before it starts, so its normal return survives same-drain
  thread-slot reuse; this closes the former abnormal-finish question without
  settling publication timing `[04 R-P28-COB-01R]`.

## 8. State-machine transition regressions

These retail contracts apply in both gameplay modes:

* Both guard variants retarget slots only inside the hostile recorded-attacker,
  damage-wake and no-chase gates, after forced attack insertion fails. Retaining
  an existing target requires full shot admission, including medium, air and
  ballistic restrictions `[04 R-UNIT-06 §1]`. Tests distinguish each gate and
  preserve admitted targets; Modern Hold Fire retains its separate policy.
* Invalid-opcode termination leaves call-script waiters blocked across later
  drains and slot reuse until an explicit return or signal supplies a wake
  event `[04 §4.2]`. Tests cover both abnormal termination and real wake events.
* The callback bridge exposes `DeferredArgs` and `DeferredWakeArgs` for logical
  arity separate from four physical argument cells. `TransportDrop` uses arity
  one with cargo identity, packed drop point and two zeros. Cleanup's
  `StopBuilding` uses arity zero with four zeros, while the name-form edge
  callback preserves stale cells `[04 R-CB-01 §2]`. Producer tests inspect entry
  state before interpretation can overwrite the cells and check the immediate
  wake barrier separately.

The COB corrections address exact execution and argument contracts; the audit
did not establish visible stock-unit symptoms for those three discrepancies.

### Modern crowded arrival

**Nanolathe Modern policy (user-authorized prototype).** A terminal ground
positional move may finish near its destination when a stationary friendly
crowd prevents further local progress. Strict 3.1 retains the ordinary point
arrival predicate and retry machine [04 R-ORD-01 §4][04 R-PATH-01 §9]. A failed
search, exhausted route or nearby unit alone does not establish arrival.

The existing `orders.Rules.CrowdedMoveArrival` decision admits only a sole
primary `Move_Ground`, without a target or automatic work/danger provenance.
The mover must be alive, complete, unstunned, uncarried, stationary and within
96 world units of its stored destination. Movement must confirm same-owner,
stationary mobile occupancy of the destination footprint, a statically clear
local corridor, and no immediately closer free footprint anchor. The same
committed anchor and destination must satisfy those conditions for 90 ticks
(three seconds). These limits are prototype tuning, not retail constants.

Completion raises ordinary arrival and releases the controller goal, route
and pending search. A phase-zero retry is advanced to the existing phase-one
arrival branch and its arrival gate is armed, so an unexpired retry deadline
cannot swallow the notification or reinstall the old destination. The normal
primary pump removes the move and performs its ordinary idle refill. No new
queue teardown, teleport, route search or resource/RNG operation is introduced.

Construction and repair approaches with a queued parent, explicit attacks,
Patrol/Guard chains, control heads, danger escape/return moves and all moves
with successors are excluded. The policy covers both ordinary terminal user
moves and inherited factory waypoints; retail rally inheritance leaves no
persistent factory provenance [05 "Rally inheritance"]. It does not claim a
globally closest reachable point or finish a distant blocked route.

Dwell is private transient node state, with no new retail save bytes. Restored
moves can qualify after a fresh 90-tick dwell, without a newly published path.
Progress, changed goals, cleared occupancy or a gap in eligible observations
restart the dwell. Strict calls are pure no-ops, including RNG and node state;
returning from Strict after an unobserved tick therefore starts a fresh dwell.
Tests cover the captured Flash crowd and inactive status-512 phase-zero retry,
ordinary cleanup, Strict bypass, protected assignments, local free steps,
invalid blockers, extreme coordinates and save restoration. Movement owns the
[local footprint proof](DESIGN_MOVEMENT_PATH.md#modern-crowded-arrival).
