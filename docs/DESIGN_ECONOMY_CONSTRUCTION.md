# Design — Economy, construction and features

`internal/economy`, `internal/construction` and `internal/features`. The
per-player resource ledger and its 30-tick settlement, the build request from
button press to finished unit, the factory state machine and its carried
product, nanolathe progress and the work operations that share it — repair,
reclaim, capture, resurrection and reverse construction — and the feature
runtime: placement, reclaim, burning, reproduction, sinking and the wrecks a
death leaves behind.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation routing that
makes a bare `[Cn]` in `internal/economy` or `internal/construction` resolve to
the contract list in §3 below.

## 1. Purpose and boundary

These three packages answer three questions:

* **What does this player own, and can it pay for this?** One ledger per slot,
  settled on that slot's own 30-tick deadline, with a two-stage admission that
  services old debt before new work.
* **How does a thing get built?** One request path — queue, exit or approach,
  nanoframe, progress, completion — shared by a factory producing a unit and a
  mobile builder stamping a structure, driving one construction step.
* **What is lying on the ground?** Trees, rocks, metal patches, geothermal
  vents and wrecks: their footprints in the plot, their reclaim value, their
  fire, their reproduction and their sinking.

The single most dangerous mistake here is treating an authored economy value as
a per-tick rate. Authored `energymake`, `metalmake`, `energyuse` and `metaluse`
are per **settlement pass** — a factor of thirty [I8]. There is no `× 30` or
`/ 30` anywhere in the ledger; the interval appears once, as the deadline
advance.

The boundary runs at five places:

* **The tick belongs to `internal/session`.** The per-player ledger runs inside
  the fifth phase, as the economy half of the same per-player walk that carries
  orders and visibility; the feature lifecycle is the sixth phase; the feature
  motion and reproduction walk rides the fourth `[01 §4.4]`
  `[01 R-CORE-01 §4.4.1]`. Sharing runs once after the phases. None of these
  packages reads a clock or logs inside a tick.
* **The order queue belongs to `internal/orders`.** A build is a typed payload
  on an `orders.Node` in the primary segment; `internal/construction` registers
  handlers on the queue and calls the queue's push, coalesce and cancel API. It
  defines no second queue and no second node `[04 §3.1]` `[04 §3.3]`.
* **Placement legality belongs to `internal/world`.** `CheckPlacement` is the
  one predicate, and construction is a caller of it — at a factory exit, at a
  chosen site, and at a resurrection `[04 §6.4]`. What construction owns after
  a successful check is the *stamp*: the product's occupancy and, for a
  building, the yard cells its current door state selects `[04 R-COLL-01 §3]`.
* **Damage and death belong to `internal/combat`.** Features expose a damage
  entry and an ignition entry that combat calls; combat asks features for the
  corpse definition and the area-damage candidate at a cell. The kill packet a
  cancelled nanoframe sends is combat's ordinary packet, not a private one
  `[06 §12.1]` `[06 §9.3]`.
* **Presentation is a seam, never a caller.** The nanolathe spray, the
  geothermal steam column and the burning-feature smoke puff are published as
  events on the committed frame through optional hooks; a nil hook means no
  effect and changes no authoritative state `[03 §5.5]` `[05 R-P0-06 §5]` [I6].

## 2. Packages and key types

### 2.1 `internal/economy`

**The ledger** (`ledger.go`). `Player` is one slot's record: single-precision
`Stock` and `Capacity` per resource, the player mirror `Bucket`, the three
absolute-tick deadlines (`UpdateTime`, `WinLoseTime`, `DisplayTimer`), the
control byte, observer flag, alliance row, share thresholds and the cumulative
double totals `[05 "Player slot"]`. `Bucket` is the four accumulators —
production, requested, accepted, carry — held per resource per unit and
mirrored at player level `[05 "Unit instance economy state"]`; `ArchivedBucket`
is the two-slot report retained from the previous pass, which is why accepted
and carry are live-only state `[05 R-ECO-01 §5]`.

Stocks, capacities and every accumulator are `float32` because retail's are;
the cumulative totals and the waste counter are double, and the only floating
constants in the ledger are the two special-player scales [I2].

`Service` holds the ten slots, the reference player, the economy mode selector
and two seams the package cannot import: `CloakCost` (the per-unit cloak upkeep
cost) and `EndCondition` (the local slot's victory/defeat block, which rides
this same deadline `[08 R-TRIG-01 §6]`). Per-unit buckets live in a slice
indexed by pool handle with slot 0 null `[01 §6.1]`.

**Alliances** (`alliance.go`). Retail keeps two alliance rows of eleven bytes;
every simulation consumer indexes the first — the row carrying this player's own
declaration toward each other slot. `DeclaresAlliance` is that one-directional
read, and `AllianceRow` projects it into the eleven-byte form the save bank
carries `[05 R-SHARE-01 §1]` `[08 "Player records"]`.

**The deadline block** (`tick.go`). `Tick` walks slots 0..9 ascending and
delegates to `TickPlayer`, which owns the whole per-slot shape: the early
eligibility skip, the two internally 30-paced helpers and the weapon/position
refresh sweep that run whether or not settlement is due, the `beforeDeadline`
callback the session uses to dispatch the computer player without economy
importing it, the unsigned deadline compare and its single conditional advance,
the end-condition block, then the settlement gate chain
`[05 "Authoritative settlement order"]`. `PlayerEliminated` is the elimination
predicate all four player walks share, derived from the two unit counters rather
than a flag `[05 R-ECO-01 §12]` `[08 R-SKIR-01 §3]`.

**Production** (`maker.go`). `PerUnitProductionFills` is the per-unit gather in
player-slice then unit-slot order: passive make and use, the wind and tidal
scalars, constant metal makers and terrain extractors under the strict
positive-energy-carry stall, the negative-energy-use refund, and the
positive-production difficulty discount applied at the contribution
`[05 R-ECO-01 §2]` `[05 R-ECO-01 §3]` `[05 R-PROD-01 §1]`
`[05 R-PROD-01 §5]`. `CreditFeatureReclaim` is the credit half of the
feature-reclaim payout, which is where the computer player's difficulty scaling
is applied — separately to each of the two additions, gated on the *builder's*
record `[05 R-WORK-01 §5]` `[05 R-ECO-01 §11]`.

**Admission and settlement** (`admission.go`). The five admission helpers are
`AdmitTwoResource`, `AdmitOneResource`, their two mirror twins and
`ImmediateDebit` `[05 R-ECO-01 §7]`; the two-resource form is an all-or-nothing
gate that always records both requests and records both as accepted only when
both carries are non-positive `[05 "Two-resource admission"]`. `Settle` is the
whole pass: rebuild capacity, per-unit production fills, cloak upkeep, gather
both resources and commit the per-pass counters, settle energy and then metal
independently, apply back to units and then the mirror, clamp against the
rebuilt capacity and accrue the overflow as waste.

**Sharing** (`tick.go`). `ShareTick` is the automatic dispatcher: metal and
energy at global ticks that are multiples of 60, sensor sharing at multiples of
450, for the reference player alone, self-gated and outside settlement
`[05 "Allied resource and sensor sharing"]` `[05 R-SHARE-01 §3]`.
`ApplySharePacket` is the receive side, which credits the destination's
production bucket exactly once and never redoes the source's threshold or
capacity tests `[05 R-SHARE-01 §4]`.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The detached per-unit
account image is twelve single-precision words in two consecutive resource
halves, energy before metal `[08 R-SAVE-02 §7]`.

### 2.2 `internal/construction`

**The request** (`queue.go`). `QueueFactoryBuild` and `QueueMobileBuild` are the
two entry points; both push a typed payload onto the primary segment of the
builder's existing queue. `FactoryPayload` carries the canonical definition key,
the stable one-based catalog index, the remaining count and the handler's phase
byte; `MobilePayload` adds the site anchor in world 16.16 and the build angle.
Counted adds coalesce tail-only; `CancelTailMost` and `CancelProductCount` match
the tail-most node and tombstone it `[05 "Queue insertion"]`
`[05 "Queue subtraction"]`. Validation is a preflight on the definition, not a
reservation — nothing is reserved until allocation.

**The state machine** (`factory.go`). One handler drives five phases, and the
phase byte is order-record state, so it survives a save `[08 R-SAVE-ORDER-01]`:

| Phase | Factory (`BuildingBuild`) | Mobile (`MobileBuild`, `VTOL_MobileBuild`) |
|---|---|---|
| 0 | building-class gate, then raise the activate edge on a positive count and advance in the same pass | skip the door handshake, zero the blocked-area counter, advance |
| 1 | wait, as a level test with no timeout, for the script to set the in-build-stance bit | the approach: install the rectangle goal at the site and walk |
| 2 | resolve the exit spot, validate it, allocate the nanoframe | validate the chosen site, allocate the nanoframe |
| 3 | the work visit: one shared construction step per visit, retrying a tick later | the same |
| 4 | lower the building edge, repeat the completion transition idempotently, restart a counted successor in the same pass | the same |

The activate edge is a real rising edge, because the yard-door handshake is
entirely script-owned: the engine raises `Activate` and waits, and nothing but
the script writes the bit phase 1 tests `[05 "Factory production lifecycle"]`
`[04 R-UNIT-06 §2]`.

**Exit spots and the placement seam.** `QueryBuildInfo` runs the factory
script's build-info query synchronously with its output cell pre-initialized to
−1, resolves the returned piece's transform against the factory origin, and
returns a world position; `SnapWorldToCell` biases each coordinate by half its
extent to reach the footprint rectangle's origin `[04 §3.8]` `[04 §6.3]`.
`validatePlacement` is the single call into `world.CheckPlacement` for every
construction site, with a null self identity and the full per-cell terrain gates
`[04 R-FAC-02 §5]` `[04 R-COLL-01 §2]`. `reservePlacement` commits the accepted
footprint afterwards; `stampBuilding` and `YardOpenTransaction` keep a building's
stamped cells in lockstep with its current door state, which is what makes a
factory exit legal — the producer no longer holds the cells its open yard
released `[04 R-COLL-01 §3]`.

**The approach** (`approach.go`). A mobile builder out of nanolathe reach of its
site reads the product's footprint pair, snaps the record's X and Z to that
footprint's centre, zeroes the leash word, installs the rectangle goal of
`[04 R-PATH-01 §12]` with the product's anchor cell and footprint as origin and
size, and advances. There is no candidate generator, no range filter, no sort
and no bounded list: the candidate set is the search's own border-cell
enumeration and the selection is the border cell it closes first
`[04 R-PATH-01 §13]` `[04 R-MOV-03 §9]`. Because the rectangle grows the product
footprint by the mover's own footprint, a builder that arrives is standing clear
of the site it is about to stamp; `mustClearSite` keeps the walk installed until
it is.

A construction aircraft has the same phase with a different goal and no reach
expression. `VTOL_MobileBuild` phase 1 snaps the goal onto the product's
footprint, installs an air point marker there with horizontal arrival radius
`builddistance` and sets the gate to `0xE0`; the placement phase is dispatched
by that marker's outcome and abandons on `0x40` with no caption
`[04 R-ORD-02 §2]`. The marker and the gate belong to the air executor in
`internal/movement` (it owns the marker family), so this service's phase 1
installs nothing for an aircraft and only holds the record open; its phase-1
body advances on a wake the executor confirms is the site marker's — the
takeoff preamble's climb marker reports on the same gate first
`[04 R-AIR-01 §6]` — and a ground rectangle goal is never submitted for an
aircraft. Once the nanoframe exists the aircraft builds from wherever the
150-tick orbit of `[04 §10.3]` leaves it; there is no reach test after arrival.

**The carried product.** A product is cargo. It is attached to its producer at
allocation, sits where the nanoframe sits for every tick of its construction,
and is detached at completion — that detach *is* the release `[04 R-FAC-02 §1]`
`[04 R-FAC-02 §2]` `[04 R-FAC-02 §3]`. The producer and its product are exempt
from each other's collision only through the yard map; there is no push, no
stacking and no force placement, so a product that never moves blocks its
factory indefinitely `[04 R-FAC-02 §5]` `[04 R-FAC-02 §6]`.

**Progress** (`factory.go`). `sharedStep` is the one construction helper every
build, assist, factory-product and deconstruction visit runs, and the *sign* of
the worker quantum picks the arm, because retail has one helper
`[05 R-WORK-01 §1]`. The forward arm raises the under-construction wake bit on
the target before anything else, returns uncommitted on a zero or unordered
quantum, forms the proportional energy and metal cost, puts them through the
two-resource admission, and on acceptance writes health — capped by an
*unsigned* comparison — and the new remaining fraction. The reverse arm is
metal-only, credits the refund direct to the **target's** bucket through the
special-player selector, floors health at zero, and kills the frame when the
fraction clamps back to 1.0 `[05 R-WORK-01 §9]` `[05 R-WORK-01 §11]`.

`QueryNanoPiece` is the synchronous per-visit query for the emitter piece, and
the accepted step — never a refused one — publishes one nanolathe segment event
from that piece's world position to the product's box `[05 R-P0-06 §1]`
`[05 R-P0-06 §2]` `[05 R-P0-06 §3]` `[05 R-WORK-01 §8]`.

**The other work operations.** `Repair` forms a heal packet that shares combat's
early-heal path `[05 R-WORK-01 §3]`; unit reclaim is an order-driven worker
state with its own two-tick cadence, pulse threshold and fifteen-tick restart,
run through the same per-unit construction window but outside the build state
machine `[05 R-WORK-01 §4]`; `capture.go` holds the capture timer, its two-tick
progress and the ownership transfer with its three-test entry gate and death
latch `[05 R-WORK-01 §6]` `[05 R-WORK-01 §15]`; `resurrection.go` holds the wait
delay, the corpse-name truncation and the single simulation draw that is an
approach-point vertical term, not a placement jitter `[05 R-WORK-01 §7]`;
`reverse.go` holds only the refund selector ladder that `sharedStep`'s reverse
arm calls.

**Cancellation.** `handleCancelCurrent` is the highest-priority interrupt:
refund, credit, completion transition, the ordinary kill packet, then the paired
callback edges and the node drop `[05 "Cancel-current and stop interrupts"]`.
The other interrupt prints its message, decrements the count once and lets the
node survive.

### 2.3 `internal/features`

**Instance and catalog** (`service.go`). `Instance` is one live feature: its
immutable definition, its anchor cell, a plot reference, health and reclaim
progress, burn and animation state, and — for a 3D feature — a position and
velocity. Retail's 48-byte record is identity, not layout; Go stores named
fields [I13] `[05 "Feature instance and terrain cell"]`. The live-instance arena
is 2,048 slots allocated once at map load and handed out by a free list; its
occupants are exactly every 3D definition and every *active* sprite event
record. A resting sprite feature takes no slot at all `[05 R-FEAT-01 §2]`
`[05 R-FEAT-01 §3]`.

**Placement.** `PlaceAt`, `PlaceAtWorld` and `PlaceCorpse` all reach the same
stamp helper, which is the sole owner of terrain and plot writes: the dense-pack
teardown first, then the anchor's index and the fringe cells' signed deltas,
then the height snap `[05 R-FEAT-01 §3]` `[05 R-FEAT-01 §3-A]`. `PopulateFromTerrain`
is the map bootstrap; `RestoreAt` is the save path, which places through the same
helper and then copies only the family state words `[08 R-SAVE-FEATURE-01]`.

**Reclaim** (`service.go`, `area.go`). `Reclaim` refuses a cell already carrying
an event record — burning, dying or reclaiming — and refuses a non-reclaimable or
indestructible definition, then returns the definition's two pools **raw**: the
difficulty scaling belongs at the credit, in the ledger, and applying it here as
well would apply it twice `[05 "Feature reclaim"]` `[05 R-WORK-01 §5]`
`[05 R-FEAT-01 §15]`. The cell itself goes through the transition.

**Transition and replacement.** `transitionFeatureAt` plays the definition's
death or reclaim sequence when one is named and replaces immediately when none
is, following the `featuredead` → `reclamate` → `burnt` hop with its sentinels;
`replaceFeatureAt` clears the whole stamped footprint and returns the plot cell
to the free sentinel `[05 "Removal and successor replacement"]`
`[05 R-FEAT-01 §5]`.

**Fire** (`burn.go`). `Ignite` is the ignition entry with its firestarter and
damage gates and its countdown in ticks; `DamageFeature` is the damage entry
`[05 R-FEAT-01 §8]` `[05 R-FEAT-01 §9]`. `burnTick` is the feature phase's
animation pass in order: the burning branch advances the burn cursor, fires the
one-shot burn-weapon event at the footprint centre, and on every third global
tick emits one smoke puff jittered against the current burn frame's geometry by
two CRT draws; the non-burning branch advances a die or reclaim animation's main
then shadow cursor and, when the sequence pointer clears, runs the replacement
`[05 R-FEAT-01 §10]` `[05 R-FEAT-01 §11]` `[05 R-FEAT-01 §16]`.

**Reproduction** (`reproduce.go`) and **sinking** (`sink.go`). The reproduction
walker visits one cell per tick, descending a global cursor from `W×H − 1` with
a wrap-skip that means the last cell is never scanned; the sink state machine
integrates a constant vertical velocity against the derived floor pair and the
water plane `[05 "Feature reproduction"]`
`[05 "Feature sinking and water interaction"]` `[05 R-FEAT-01 §13]`.

**Geothermal** (`geothermal.go`). The vent is terrain, and the placement
validator that requires it is read-only, so a vent persists beneath its plant and
survives the plant's destruction with no restore step `[05 "Geothermal requirement"]`.
The steam column is a presentation seam the stamp calls once per placed
geothermal definition `[05 R-ECO-02 §3]`.

## 3. Contracts

### 3.1 Settlement — C1…C7

**C1 — no time conversion.** Authored fields are added to the accumulators
exactly as parsed; there is no per-tick rate anywhere in the ledger. The only
floating constants in it are the two special-player scales
`[05 "Authoritative settlement order"]` [I8].

**C2 — the deadline block.** The deadline is an absolute tick compared
**unsigned** against the global tick; while it is greater, the rest of that
slot's processing is skipped. When due it is advanced by exactly 30 **before**
anything else in the block runs. Because the advance is a single conditional
add, a slot more than 30 ticks behind settles once per tick until it catches up —
reproduce that, do not loop `[05 "Authoritative settlement order"]`
`[05 R-ECO-01 §1]`.

**C3 — slot structure.** Ascending order; skip unless the record exists, the
control byte is one of the three active states, and the observer byte excludes
observers — and while skipped **nothing advances, including the deadline**. Work
independent of settlement still runs: two internally 30-paced helpers and a
weapon/position refresh sweep, none of which touches stock. After those helpers
and before the deadline compare the optional `beforeDeadline` callback runs; the
session supplies the computer player's dispatch there, so economy never imports
the planner `[05 "Authoritative settlement order"]` `[05 R-SHARE-01 §1]`.

**C4 — the settlement gate chain.** All required: record exists; state active;
not observer; the slot is not eliminated; state narrowed to one of the two
settling states; the game-ended bit clear; the end-of-game countdown negative.
The status pair this contract once kept literal is the elimination test — the
halfword is the record's live unit count and the word its units-ever-created
count, so "halfword non-zero **or** word zero" is the exact negation of
elimination. The deadline advance deliberately precedes the test, so an
eliminated slot keeps advancing its deadline and simply never settles
`[05 "Authoritative settlement order"]` `[05 R-ECO-01 §12]`.

**C5 — seeding and persistence.** Battle entry seeds every active player's
deadline and its two siblings to the current global tick; mission setup runs one
full player-phase pass before starting resources are granted; spawn credits are
written directly to live stock **outside** the ledger. Saves persist all three
deadlines verbatim as absolute ticks and never re-seed on load
`[05 "Authoritative settlement order"]` `[05 "Saving economy, construction, and features"]`.

**C6 — stable slot order.** Within a settled player, units are visited in slot
order, so earlier units consume live stock before later ones are tested
`[05 "Authoritative settlement order"]` [I1].

**C7 — the pass, in order.** Rebuild capacity; per-unit production fills; cloak
upkeep; gather both resources and commit the per-pass counters; settle each
resource; apply back; clamp and accrue waste. Old carry is settled **before**
newly accepted work `[05 "Authoritative settlement order"]`
`[05 "Two-stage settlement algorithm"]`.

### 3.2 Admission and stocks — C8…C14

**C8 — two stages, two independent resources.** There is no combined shortage
ratio. `pool = opening + production`; `debtRatio = min(1, pool / Σdebt)` with
zero debt treated as fully funded; `acceptRatio = min(1, remainingPool /
Σaccepted)`; `newCarry = oldCarry × (1 − debtRatio) + accepted × (1 −
acceptRatio)`. Each unit scales uniformly within a stage; there is no
largest-remainder distribution and no minimum work quantum
`[05 "Two-stage settlement algorithm"]` `[05 R-ECO-01 §5]`.

**C9 — single precision.** All of C8 is computed in single precision with
retail's evaluation order preserved, narrowed at the named stores; it is not
recomputed in `float64` and narrowed at the end `[05 R-ECO-01 §1]`
`[05 R-ECO-01 §5]` [I2].

**C10 — commit order, clamp and waste.** Per-pass counters and cumulative
totals are committed **before** opening stock folds into the pool, so they
report the pass and not available funds. After settlement, stock is clamped to
the rebuilt capacity, the overflow is added to cumulative waste with its
fractional part preserved, and the live buckets are archived and zeroed
`[05 "Stocks, counters, and waste"]` `[05 R-ECO-01 §6]`.

**C11 — the mirror bucket's closed writer set.** Record init and per-pass clear,
save/load overlay, the two admission helpers, the immediate debit path, sharing
transfers, the spawn credit, and the construction termination credit. **There is
no factory queue-draw writer** `[05 "Stocks, counters, and waste"]`.

**C12 — sharing is not settlement.** The dispatcher runs once per tick after the
player phase, for the reference player only, and self-gates: metal and energy at
global ticks that are multiples of 60, sensor sharing at multiples of 450.
Transfers mutate live stock between passes, the candidate scan is last-wins over
slots 0..9, the transfer is the lesser of the destination's capacity gap and the
source's excess over its own threshold scaled by one third for metal and one
half for energy, and the thresholds are zeroed at battle init and stay distinct
from capacity `[05 "Allied resource and sensor sharing"]` `[05 R-SHARE-01 §2]`
`[05 R-SHARE-01 §3]` `[05 R-SHARE-01 §4]`.

**C13 — cloak debit.** A direct sequential debit with truncation, taken in unit
slot order before the pool is formed, so an earlier unit's debit can starve a
later one; the outcome drives the activation/stall transition service, whose
write is unconditional and whose cue notifications are the only conditional part
`[05 "Cloak debit"]` `[05 R-ECO-01 §8]` `[05 R-ECO-01 §9]`.

**C14 — extraction and storage.** An extractor stores `Σ(cellMetal + 1) ×
extractsMetal` sampled over its footprint **once at placement** and never
resamples; makers and extractors both stall on strictly positive energy carry.
Storage capacity is rebuilt from scratch every pass from eligible completed
units plus the start bonus `[05 "Terrain metal extraction"]`
`[05 R-PROD-01 §6]` `[05 "Storage capacity"]` `[05 "Completed-unit eligibility"]`
`[05 R-ECO-01 §4]`.

### 3.3 Construction — C15…C24

**C15 — one queue.** Factory and mobile products live as typed payloads on
`orders.Node` in the **primary** segment, through the queue API the orders
package publishes. This package introduces no second node and no second queue.
The secondary segment is exclusively `BuildWeapon` and `SelfDestruct`
`[05 "Factory queue"]` `[04 §3.1]`.

**C16 — exit-spot acquisition.** In order: query the factory script's build-info
piece with the query argument **pre-initialized to −1**; resolve that piece's
transform plus the factory origin to a world position; store it on the order
node; load the product definition and snap to map cells using the packed
footprint extents, each coordinate biased by **half its extent** to give the
footprint rectangle's origin `[05 "Factory production lifecycle"]` `[04 §3.8]`
`[04 §6.3]` `[05 R-P28-ANG-01R §3]`.

**C17 — silent blocked revalidation.** The snapped rectangle is validated with a
**null self identity** and terrain-check mode 1, so the inline per-cell gates —
feature blocking, unit occupancy, water, depth, height and slope — run at the
exit exactly as at a chosen site. The exit's yard cells are released by the
yard-open port write before this phase runs, and the yard-map height maximum is a
yard participation a yardless mobile product never contributes to. On failure
the node retries in **exactly 15 ticks**, sets its wake bit, and stays — no
product exists yet, so the retry is silent: no message, no sound, no allocation,
repeating for as long as the footprint is obstructed. **No timeout, no force
placement** `[05 "Factory production lifecycle"]` `[04 §6.4]`
`[04 R-FAC-02 §5]` `[04 R-FAC-02 §6]`. An invalid *definition* is a distinct
outcome: a permanent diagnostic, not the silent loop.

**C18 — nanoframe allocation.** On success the allocator creates the unit **at
the resolved exit transform, not at the derived anchor**, with the owner, the
product definition, remaining fraction 1, health 0 and the build stance cleared;
the builder/product link is established and `GetBuilt` is queued on the product
`[05 "Nanoframe allocation"]` `[04 §3.8]` `[04 R-P0-09]`.

**C19 — rally inheritance.** The factory's own queued move and patrol nodes are
re-enqueued on the product in queue-traversal order; with none, the product
parks. Standing-order bits copy under the documented gates
`[05 "Rally inheritance"]`.

**C20 — queue insertion.** Insertion goes after the active marker; counted adds
coalesce **tail-only**; cancellation matches the tail-most node and tombstones
it. There is **no repeat flag** in retail — repetition is count plus a same-pass
restart from phase 0 `[05 "Queue insertion"]` `[05 "Queue subtraction"]`.

**C21 — cancel-current.** The highest-priority interrupt, in order
`[05 "Cancel-current and stop interrupts"]`:

1. refund `trunc((1 − remaining) × metalBuildCost)`;
2. credit it to the builder's metal bucket. When the referenced player object is
   in the special second state a global mode selector scales it: value 0 credits
   **one half**, value 1 credits **seven tenths**, any other value the whole
   amount. Every one of the discount family's sites forms
   `accumulator − contribution × (−scale)`, so the constants are negative and
   the operation is a subtraction — the scaled arms **pay**, they do not charge
   `[05 R-ECO-01 §3]` `[05 R-ECO-01 §11]`;
3. run the completion transition;
4. send the ordinary kill packet — kind-9 damage of exactly 30,000, unscaled by
   the armor branch because that scaling requires damage below 30,000 — so wreck
   rules apply, and a cause-9 death skips the killed-severity query entirely:
   severity zero, no explosion, no corpse `[06 §12.1]` `[04 §5.1]`;
5. lower the deactivate and start-building callback bits in one edge call, firing
   both script callbacks together, refresh the interface, and drop the node
   **without decrementing its remaining count**.

With no product attached the same epilogue runs and the node still drops.

**C22 — construction stopped.** The other interrupt prints its message,
decrements the node count **once**, refreshes the interface and returns result 0:
the node **survives** and the state machine restarts
`[05 "Cancel-current and stop interrupts"]`. A product destroyed mid-build
reaches this body through the target-removed interrupt, which the pump tests
ahead of the state machine `[04 R-ORD-01 §6]` `[04 R-ORDER-02 §2]`.

**C23 — per-definition limits.** Enforced **only at nanoframe allocation**:
there is no queue reservation, and capture validates separately. The definition
parser writes −1 into every limit field, so any negative value is the unlimited
sentinel and a written 0 is a genuine zero allowance whose only writer is the
multiplayer restriction step, which never runs in skirmish or campaign.
Exhaustion produces the retry with the verbatim message
`[05 "Unit creation and limits"]` `[05 "Limit accounting"]`
`[05 R-SHARE-01 §8]` `[05 R-SHARE-01 §9]` `[05 R-ECO-02 §4]`.

**C24 — construction arithmetic.** One shared step, and the sign of the worker
quantum picks the arm. The quantum is `(uint16)workertime / 30` as an integer
division performed before the conversion to float. The forward arm ORs the
under-construction wake bit into the target's pending word before the
zero-quantum test and before admission — so a refused step and a zero quantum
both defer the decay — returns uncommitted on a fraction already exactly zero, on
a zero quantum and on an unordered one, and on acceptance caps health with an
**unsigned** comparison against the definition's maximum. The reverse arm is
metal-only, credited to the **target's** bucket, floors health at zero with no
maximum cap, and self-kills the frame when the fraction clamps back to 1.0 —
the clamp kill is the only thing that removes an abandoned frame. The completion
transition is governed by the stored fraction's zero test after **every** exit of
the step, including the admission-refused path, not by the step's committed
return `[05 "Construction arithmetic"]` `[05 "Worker quantum"]`
`[05 "Remaining fraction"]` `[05 "Health gain and fractional carry"]`
`[05 "Multiple builders"]` `[05 "Completion"]` `[05 R-WORK-01 §1]`
`[05 R-WORK-01 §9]` `[05 R-WORK-01 §11]` `[04 R-ORD-01 §11]`.

### 3.4 Features — C25…C28

**C25 — reproduction.** One cell per tick, walking a global cursor
**descending** from `W×H − 1` with wrap-skip, so cell `W×H − 1` is never
scanned. Eligibility is an anchor below the sentinel band with the animation bit
clear. The percentile draw is consumed **even when `reproduce` is 0**; on a
passing roll the two offsets are drawn from the area and biased by half of it
`[05 "Feature reproduction"]` `[05 R-FEAT-01 §12]` [I4].

**C26 — successor replacement.** The `featuredead` → `reclamate` → `burnt` hop
with its sentinels, played through the named sequence when one exists and taken
immediately when none does; removal clears the whole stamped footprint and
returns the plot cell to the free sentinel
`[05 "Removal and successor replacement"]` `[05 R-FEAT-01 §5]`.

**C27 — sinking.** A wreck below the water plane descends at the fixed vertical
velocity, re-latched every tick, until it settles on the floor derived from the
plot cell's min/max pair `[05 "Feature sinking and water interaction"]`
`[05 R-FEAT-01 §13]`.

**C28 — burning.** Ignition, the spark countdown in ticks, the one-shot spread
and burn-weapon event, the third-tick smoke puff with its two CRT draws and its
frame-geometry jitter, and the animation's end clearing the cell
`[05 "Feature burning"]` `[05 R-FEAT-01 §9]` `[05 R-FEAT-01 §10]`
`[05 R-FEAT-01 §11]` `[05 R-FEAT-01 §16]` `[06 §13.1]`.

### 3.5 Not implemented

* **Automatic resource and sensor sharing never fires in a single-player
  battle.** The dispatcher, its cadences and its arithmetic are implemented, but
  the candidate scan admits only remote-human slots, and the whole path is gated
  on a networked session. This is retail's own gate, not an omission
  `[05 R-SHARE-01 §3]` `[05 R-SHARE-01 §5]`.
* **The mapping-grid merge is not economy's.** A received sensor-share packet
  records no stock here; the grid merge belongs to `internal/visibility`
  `[05 R-SHARE-01 §6]`.
* **The extraction rate is sampled at the placement call sites, not by the unit
  creator.** Retail samples it in the allocator, so a unit created by a path
  that does not go through a placement — a resurrection, for one — yields
  nothing here where retail would yield metal `[05 R-PROD-01 §6]`.
* **The cloak gate's second status bit is inert.** The gate requires it clear and
  nothing anywhere sets it, so the term is unobservable and is implemented as
  always satisfied `[05 R-ECO-01 §9]`.
* **Stockpile production is a cost helper here, not a state machine.** The
  per-tick cost delta is computed in the ledger; the stockpile counter and its
  weapon coupling belong to `internal/combat`
  `[05 "Stockpile production"]`.

## 4. Retail behaviour that is not a bug

* **Units piling up at a factory exit are not waiting for a broadcast.** The
  script port often read as a "bugger off" flag has **no engine reader at all**:
  it is written by the set-port arm, read back by the get-port arm, cleared at
  creation and serialized, and nothing in the simulation consults it
  `[04 R-COB-05]`. What retail does is the silent 15-tick exit retry, the
  refused yard close and an ordinary blocked mover `[04 R-FAC-02 §5]`
  `[04 R-FAC-02 §6]`. Do not build a scatter or crowd-avoidance rule on that
  flag.
* **A column of products behind a factory is a defect, not retail.** Retail's
  no-rally products fan out around the whole border of the shared park
  rectangle. A column forms only if the follower's route-acceptance rule is
  applied at route *publication* instead of at the follower's goal installer:
  publication adopts the search's points verbatim, and running the acceptance
  rule there throws away every two-point route and rewrites it as a straight line
  at the rectangle's far-edge goal point, which walks each product back into the
  column until it covers the exit and the factory stalls permanently
  `[05 R-EGRESS-02]` `[04 R-PATH-01 §8]`. The prohibitions that still stand are
  the rest of `[05 R-EGRESS-02]`: nothing pushes a mover already parked, and the
  goal point is not made per-mover.
* **A no-rally aircraft hovering above its plant is retail.** Park becomes a
  vertical move to the aircraft's own position, so the order completes at the top
  of the initial climb `[04 R-AIR-02]`.
* **A switched-off solar collector still counts its `energymake` — but stock
  solars author a negative `energyuse`, which *is* gated by activation, so
  closing one does stop its income** `[05 R-PROD-01 §2]`.
* **A map cannot scale solar output.** The authored map key has no reader
  `[05 R-PROD-01 §1]`.
* **A build angle of 4096 scatters every freshly built unit's heading by about
  20° around down-screen.** That is the authored field, applied as authored
  `[04 §2.3b]` `[05 R-P28-ANG-01R §3]`.
* **The nanoframe decays while its builder works.** Nothing suppresses the
  decay; a build that is energy-starved goes backwards. What defers it is the
  under-construction wake bit the shared step raises on every forward call, so a
  frame decays only after a whole deadline in which no builder's step touched it
  `[04 R-FAC-02 §4]` `[04 R-ORD-01 §11]`. The fix for a starved base is more
  energy production, not a softer rule.
* **The admission gate is all-or-nothing.** Two-resource admission is a hard
  gate, not a partial-work decision; the proportionality lives in settlement,
  afterwards `[05 "Two-resource admission"]` `[05 "Two-stage settlement algorithm"]`.
* **Reclaiming a feature pays out only at the two segment boundaries, and the
  stock pools are small.** A rock that pays half a metal is authored, not broken
  `[05 R-WORK-01 §5]` `[05 R-WORK-01 §5-A]`.
* **A factory whose product never moves is blocked indefinitely.** There is no
  push, no stacking and no force placement `[04 R-FAC-02 §6]`.

## 5. Divergences

These packages introduce no divergence of their own. Three entries of
[SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) reach them through the seams they use;
all three are owned and closed elsewhere.

* **SC6 — fringe-anchor encoding.** The feature runtime resolves a fringe cell
  by the signed-offset reading, because absolute cell coordinates cannot address
  the reference install's widest maps. Every feature path that starts from a
  covered cell — reclaim, damage, burning, area candidates — hops to the anchor
  through that resolver first `[05 R-FEAT-01 §8]`.
* **SC19 — yard-map parsing and the bit 5/6 flag identities.** Forty-six stock
  yard maps disagree with their own footprint, so the parser fills by retail's
  character loop and returns no length error; blocking reads the feature
  definition's authored blocking flag, and the sixth bit reads the
  indestructible flag. Without this a metal extractor cannot be placed on its own
  deposit and stock solars cannot be placed at all — both of which reach this
  document as construction failures `[05 "Geothermal requirement"]`.
* **SC21 — `BMcode` marks structures, not factories.** A stock factory authors
  `CanMove = 1`, so a factory-versus-mobile branch keyed on mobility is wrong.
  The state machine's phase-0 gate reads the building-class status bit the
  allocator sets from the authored `BMcode`, and the interface's
  placement-versus-queue branch keys on the **product's** `BMcode`
  `[04 §6.2]` `[07 §9]`.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Player record, buckets, the four accumulators | `[05 "Player slot"]`, `[05 "Unit instance economy state"]` |
| The per-player deadline block, gate chain, catch-up edge, the pass | `[05 "Authoritative settlement order"]`, `[05 R-ECO-01 §1]` |
| Deadline strictness, the floating-point environment, the exact apply-back | `[05 R-ECO-01 §1]`, `[05 R-ECO-01 §5]` |
| The per-unit gather, exactly | `[05 R-ECO-01 §2]` |
| The difficulty discount, the special state, and the discount's site family | `[05 R-ECO-01 §3]`, `[05 R-ECO-01 §11]` |
| Capacity accumulation and the start bonus | `[05 R-ECO-01 §4]`, `[05 "Storage capacity"]` |
| Commit order, the waste clamp, the four displayed rates | `[05 R-ECO-01 §6]`, `[05 "Stocks, counters, and waste"]` |
| The five admission helpers as expressions; the operation-byte table | `[05 R-ECO-01 §7]`, `[05 R-ECO-01 §10]`, `[05 "Resource admission and carry"]` |
| The activation and stall transition service | `[05 R-ECO-01 §8]`, `[05 "Activation and stall transitions"]` |
| Cloak gate, conversion, truncation, the second bit | `[05 R-ECO-01 §9]`, `[05 "Cloak debit"]` |
| The settlement status pair is the elimination test | `[05 R-ECO-01 §12]`, `[08 R-SKIR-01 §3]` |
| Two-stage admission and the hard two-resource gate | `[05 "Two-stage settlement algorithm"]`, `[05 "Two-resource admission"]`, `[05 "One-resource admission"]`, `[05 "Direct two-resource payment"]` |
| Economy fields, widths, defaults and their reader census | `[05 R-PROD-01 §1]`, `[05 "Unit definition"]` |
| The activated bit's writers and what `onoffable` gates | `[05 R-PROD-01 §2]`, `[05 "Completed-unit eligibility"]` |
| Wind and tidal sources, the wind phase and its draws | `[05 R-PROD-01 §3]`, `[05 R-PROD-01 §4]`, `[05 "Wind generation"]`, `[05 "Tidal generation"]` |
| Upkeep timing, the maker byte, passive make and use | `[05 R-PROD-01 §5]`, `[05 "Passive energy and metal"]`, `[05 "Constant metal makers"]` |
| Seeding the metal byte, sampling the footprint, the accumulator | `[05 R-PROD-01 §6]`, `[05 "Terrain metal extraction"]` |
| Cost selection, the player gate, the can-cloak capability | `[05 R-PROD-01 §7]`, `[05 R-PROD-01 §8]` |
| Alliance rows and the alliance predicate | `[05 R-SHARE-01 §1]`, `[08 "Player records"]` |
| The two transfer helpers, the dispatcher, the receive side | `[05 R-SHARE-01 §2]`, `[05 R-SHARE-01 §3]`, `[05 R-SHARE-01 §4]`, `[05 "Manual transfer"]`, `[05 "Automatic transfer"]` |
| The share screen's producer; the mapping-grid merge | `[05 R-SHARE-01 §5]`, `[05 R-SHARE-01 §6]`, `[05 "Sensor sharing"]` |
| The unit pool's size, the allocator gate, the limit field, `norestrict` | `[05 R-SHARE-01 §7]`, `[05 R-SHARE-01 §8]`, `[05 R-SHARE-01 §9]`, `[05 R-SHARE-01 §10]`, `[05 "Fixed unit slots"]`, `[05 "Limit accounting"]` |
| Nanoframe allocation and the exhaustion caption's two sites | `[05 "Nanoframe allocation"]`, `[05 "Unit creation and limits"]`, `[05 R-ECO-02 §4]` |
| Request creation, insertion, subtraction, pump result codes | `[05 "Request creation"]`, `[05 "Queue insertion"]`, `[05 "Queue subtraction"]`, `[05 "Queue pumping and result codes"]`, `[05 "Build request and factory queue behavior"]` |
| The factory phases, the exit query, the silent retry | `[05 "Factory production lifecycle"]`, `[05 "Construction target state"]`, `[05 "Factory queue"]` |
| Factory completion, activation and rally inheritance, in order | `[04 §3.8]`, `[04 R-P0-09]`, `[05 "Rally inheritance"]` |
| The exit validator's mode and the inline per-cell gates | `[04 §6.4]`, `[04 R-FAC-02 §5]`, `[04 R-FAC-02 §6]` |
| The product is cargo: attach, carry, detach, the order form after release | `[04 R-FAC-02 §1]`, `[04 R-FAC-02 §2]`, `[04 R-FAC-02 §3]`, `[04 R-FAC-02 §4]` |
| Release-boundary audits and the final increment through factory idle | `[05 R-FAC-01]`, `[05 R-FAC-01B]`, `[05 R-FAC-01C]`, `[05 R-FAC-01R]` |
| A no-rally product column is a route-publication defect | `[05 R-EGRESS-02]` |
| Factory product heading | `[05 R-P28-ANG-01R §3]`, `[04 §2.3b]` |
| Cancel-current and stop interrupts | `[05 "Cancel-current and stop interrupts"]`, `[04 R-ORDER-02 §2]` |
| The shared construction step, instruction-exact, and both arms | `[05 R-WORK-01 §1]`, `[05 "Construction arithmetic"]` |
| Build-distance range test and the approach radii | `[05 R-WORK-01 §2]`, `[05 R-WORK-01 §12]`, `[05 R-WORK-01 §13]` |
| Repair, unit reclaim, feature reclaim, capture, resurrection | `[05 R-WORK-01 §3]`, `[05 R-WORK-01 §4]`, `[05 R-WORK-01 §5]`, `[05 R-WORK-01 §6]`, `[05 R-WORK-01 §7]`, `[05 R-WORK-01 §15]` |
| Emission producers, direction, geometry, the assist gate | `[05 R-WORK-01 §8]` |
| The clamp kill; malformed build numbers through the step | `[05 R-WORK-01 §9]`, `[05 R-WORK-01 §11]` |
| What the reclaim pools are worth in stock content | `[05 R-WORK-01 §5-A]`, `[05 "Feature reclaim"]`, `[05 "Unit reclaim"]` |
| Nano cadence, the synchronous piece query, admission and ordering | `[05 R-P0-06 §1]`, `[05 R-P0-06 §2]`, `[05 R-P0-06 §3]`, `[05 R-P0-06 §4]`, `[05 R-P0-06 §5]`, `[05 R-P0-06 §6]` |
| The under-construction wake bit and the decay window | `[04 R-ORD-01 §11]` |
| The mobile-build approach: no candidate generator, a rectangle goal | `[04 R-PATH-01 §12]`, `[04 R-PATH-01 §13]`, `[04 R-MOV-03 §9]` |
| One ground word per cell; the null self identity at every site | `[04 R-COLL-01 §2]`, `[04 R-COLL-01 §3]` |
| The building validator's entry bounds and its two outputs | `[05 R-ECO-02 §1]` |
| The world-position feature resolver; the geothermal steam producer | `[05 R-ECO-02 §2]`, `[05 R-ECO-02 §3]`, `[05 "Geothermal requirement"]` |
| The feature parser, catalog build order and the successor pass | `[05 R-FEAT-01 §1]`, `[05 R-FEAT-01 §2]`, `[05 "Feature catalog and placement"]`, `[05 "Feature definition"]` |
| The stamp service, the dense-pack rule, the height snap, teardown | `[05 R-FEAT-01 §3]`, `[05 R-FEAT-01 §3-A]`, `[05 R-FEAT-01 §4]`, `[05 "Placement"]` |
| Transition, replacement and same-tick precedence | `[05 R-FEAT-01 §5]`, `[05 "Removal and successor replacement"]` |
| The flag reader census; metal deposits and vents versus wrecks | `[05 R-FEAT-01 §6]`, `[05 R-FEAT-01 §7]`, `[05 "Definition flags, teardown, and the Great Divide partition"]` |
| The damage entry, ignition, the feature phase in order, the burn event | `[05 R-FEAT-01 §8]`, `[05 R-FEAT-01 §9]`, `[05 R-FEAT-01 §10]`, `[05 R-FEAT-01 §11]`, `[05 "Feature burning"]` |
| The reproduction walk and its axis defect | `[05 R-FEAT-01 §12]`, `[05 "Feature reproduction"]` |
| Sinking, slot reuse, the payout guard's two bits, the smoke puff | `[05 R-FEAT-01 §13]`, `[05 R-FEAT-01 §14]`, `[05 R-FEAT-01 §15]`, `[05 R-FEAT-01 §16]` |
| Bootstrap fringe synthesis over covered cells | `[05 R-FEAT-01 §17]` |
| Wreckage and corpse production at a death | `[05 "Wreckage and corpse production"]`, `[06 §12.1]`, `[04 §5.1]` |
| Feature area-damage candidates and the burn weapon | `[06 §9.3]`, `[06 R-WPN-04 §3]`, `[06 §13.1]` |
| The economy, construction and feature save boxes | `[05 "Saving economy, construction, and features"]`, `[08 R-SAVE-02 §7]`, `[08 R-SAVE-FEATURE-01]`, `[08 R-SAVE-ORDER-01]` |
| The end-condition block rides the settlement deadline | `[08 R-TRIG-01 §6]` |
| The nanolathe spray as presentation | `[03 §5.5]`, `[03 R-RAST-01 §8]` |

## 7. Not implemented and open

No `TODO(question)`, `TODO(T23)` or `TODO(T25)` marker remains in these three
packages. The questions the contracts above still carry are these, each with the
observation that would settle it:

* Whether the build-assist approach radius's summand is a retail defect or an
  intended asymmetry: the instructions are established and reproduced, only the
  intent is open. A manual retail observation of an assist approach against a
  footprint much longer in Z than in X settles it `[05 R-WORK-01 §2]`.
* Which of the two nanolathe-box vertical forms the four vertical-takeoff work
  executors use — the majority form omits the first extent, two of them add it
  `[05 R-WORK-01 §8]`.
* What a malformed factory product node does if it bypasses queue preflight and
  reaches the placement phase: cancellation, retry or termination. The admission
  boundary records a bounded diagnostic and leaves the node unchanged
  `[05 "Factory queue"]`.
* Whether every stock factory script opens its yard before entering the build
  stance; a census of the stock factory scripts' activate and start-building
  bodies for the yard-open port write settles it `[04 R-FAC-02 §8]`.
* Whether the empty-named order descriptor really holds operation byte 0: a
  writer there would shift every operation byte by one
  `[05 R-ECO-01 §10]`.
* Whether any reader of the per-unit archived economy snapshots exists outside
  the ledger's own redistribution. Bounded negative in the reviewed image
  `[05 "Authoritative settlement order"]`.
* What the cloak gate's second status bit means: it has no writer anywhere, so
  the term is inert and the behaviour unobservable `[05 R-ECO-01 §9]`.
* Whether any settlement intermediate can reach the range where the wider
  exponent of retail's precision control differs from a double's: no economy
  magnitude was found that does `[05 R-ECO-01 §1]` `[05 R-ECO-01 §5]`.
* How retail partitions a live-anchor overlap between two feature footprints;
  the stamper's dense-pack teardown and last-write-wins ordering is what the
  established stamp service states `[05 R-FEAT-01 §3]`.
