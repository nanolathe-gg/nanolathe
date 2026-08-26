# Retail engine economy, construction, players, and features

## Purpose and evidence boundary

This document specifies the retail engine's player-resource, construction,
factory, reclamation, capture, resurrection, unit-limit, and world-feature
systems. It is written as a clean-room behavioral design. It deliberately
omits executable addresses, in-memory offsets, decompiler variable names, and
source-like translations.

Every affirmative statement is based on static analysis of the retail
executable, its imported API surface, its embedded strings, or a bounded
instruction-level analysis recorded with the decompilation corpus. No behavior
has been filled in from another engine, from a replacement implementation, or
from visible comparison testing.

The evidence is incomplete. The decompilers have not recovered every retail
function, and several older analyses in the corpus are explicitly retracted.
This document uses the corrected construction roots and the corrected
floating-point economy bucket model. Where the executable does not yet support
an exact contract, the behavior is marked **incomplete** or **unknown** instead
of being guessed.

The following terms are used throughout:

- **Established** means direct static data flow or a closed bounded analysis
  supports the behavior.
- **Supported inference** means the evidence strongly favors the behavior but
  one important identity, branch, or caller is still open.
- **Unknown** means the decompilation does not yet provide a safe contract.

## Dependencies and ownership

This category depends on structures defined by the other retail specifications:

- The core runtime owns the authoritative tick, stable pool iteration order,
  shared random stream, floating-point environment, and deferred cleanup.
- The content system creates unit, feature, weapon, mission, and movement
  definitions from retail data files.
- The world system owns terrain cells, metal values, feature anchors and
  filler cells, occupancy, and visibility updates.
- The unit system owns unit instances, orders, scripts, activation state,
  movement, and builder range checks.
- The combat system owns damage, death, corpse severity, feature damage, and
  weapon-created resource costs.

The economy system is authoritative simulation state. Interface counters and
resource bars are consumers of it; they do not define its arithmetic.

## Prerequisite structures

### Player slot

A battle has ten fixed player slots. A player slot contains at least:

- whether the slot exists and participates in the battle;
- a stable slot number and network identity;
- player state and control type;
- current energy and current metal as single-precision floating-point values;
- current energy and metal storage capacities;
- resource-production, resource-consumption, and waste statistics;
- alliance relations to every player slot;
- automatic energy, metal, and sensor-sharing options;
- user-selected reserve thresholds;
- a contiguous range of unit slots owned by the player;
- an auxiliary player-level economy bucket;
- unit-limit accounting enforced only at nanoframe creation; queued products
  hold no reservation;
- optional mission-provided storage bonuses;
- side, team, and status information used when ownership changes.

The current resource stocks retain fractional values. Older decompiler output
misidentified them as integers because the player record was viewed through an
integer pointer. Direct instruction review shows that the engine copies and
accumulates raw 32-bit floating-point values. Production, consumption,
capacity, and waste therefore do not discard their fractional part merely
because they are stored in the player record.

### Unit definition

The economy and construction paths consume these logical unit-definition
fields:

- total energy build cost;
- total metal build cost;
- build time;
- worker time;
- maximum damage;
- energy made while complete and eligible;
- energy used while active;
- metal made while complete and eligible;
- metal-extraction multiplier;
- constant metal-maker flag;
- wind-generator multiplier;
- tidal-generator multiplier;
- energy-storage contribution;
- metal-storage contribution;
- stationary and moving cloak costs;
- footprint dimensions and yard map;
- build distance and build facing rules;
- builder, factory, movement, capture, reclaim, and resurrection capabilities;
- geothermal and other placement requirements expressed by the yard map;
- unit-limit and category information;
- corpse feature name and other death products;
- build-menu and order metadata.

Definitions are immutable after catalog construction except for resolved links
such as names converted to definition identifiers.

### Unit instance economy state

Each live unit has an economy subrecord for energy and metal. Each resource has
four live values:

1. production contributed by the unit during the current settlement pass;
2. requested consumption;
3. accepted new work;
4. carry, representing admitted work that remains unpaid.

The instance also retains archived values from the most recent settlement pass
for reporting and later state decisions. The same shape exists in a
player-level mirror bucket used for non-unit contributions.

**Established fact — what feeds each accumulator.**

| Accumulator | Contributors | Gates |
|---|---|---|
| energy production | authored passive `energymake`; the current wind scalar times `windgenerator`; the map tidal strength times `tidalgenerator`; the refund branch for a negative authored `energyuse` | passive make and storage require a zero remaining-construction fraction; wind and tidal require the unit's operational bit and a secondary state bit |
| metal production | the metal value sampled at placement when `extractsmetal` is positive; a literal one when `makesmetal` is set; authored passive `metalmake` | extraction and maker output require the unit's energy carry to be non-positive at dispatch; passive make requires a zero remaining fraction |
| energy requested | positive authored `energyuse`; the cloak debit; every build admission; repair-family one-resource admission | none — always recorded |
| energy accepted | the same positive `energyuse` when energy carry is non-positive; admitted build demands when both carries are non-positive; repair-family admission when energy carry is non-positive | energy carry non-positive (both carries for the two-resource build path) |
| metal requested | every build admission | none — always recorded |
| metal accepted | admitted build demands | both carries non-positive |
| energy and metal carry | written only by settlement's apply-back step | — |

The negative-`energyuse` refund adds the negated authored value to production
in the ordinary case; under the special player modes it instead subtracts one
half or seven tenths of it.

**Established fact — gather order.** All accumulator feeds are
per-settlement-pass amounts, consumed once per ~30 ticks per player under
ordinary play (settlement cadence above). Players are visited by slot index.
Within a player the settlement walks that player's contiguous unit slice in slot
order, skipping units without the alive flag. Per unit it dispatches upkeep and
generators, applies the idle-gated passive block, performs the cloak and stall
debit, then aggregates in the fixed order energy production, energy requested,
energy accepted, energy carry, metal production, metal requested, metal
accepted, metal carry. Only after the whole slice does it fold in the
player-level mirror bucket and commit the counters.

**Established fact — player-side storage is floating point.** Live stock,
the per-pass produced and requested snapshots, and the capacities are stored
and copied as 32-bit floats, not integers. Closing stock is copied bit-exactly
from the settled residual and staged into the next pass by a plain float
addition, so **fractional production survives across ticks exactly**, subject
only to ordinary single-precision rounding. Cumulative totals and cumulative
waste are doubles. Capacity is recomputed from zero every pass by accumulating
each idle unit's authored storage, plus a bonus term when a player flag is set.
Reserve thresholds are written once at battle setup from capacity and are read
by automatic sharing and by the resource-bar colouring.

Other economy-relevant unit state includes:

- owning player;
- definition;
- current health;
- remaining construction fraction;
- operational, activated, stalled, disabled, dead, and transported flags;
- the metal-extraction amount sampled at placement time;
- cloak state and cloak payment deadline;
- current construction or work order;
- weapon slots and direct per-shot resource debits;
- construction queue heads;
- footprint and occupancy state;
- script object and pending callbacks.

### Construction target state

An unfinished unit is a normal unit-pool entry in a nanoframe state. The
important construction measure is a floating-point **remaining fraction**:

- `1` means no construction work has been completed;
- values strictly between `1` and `0` are partial construction;
- `0` means construction is complete.

The engine does not use an ever-growing normalized completion value for its
authoritative build arithmetic. Presentation may invert the remaining fraction
to show percentage complete.

The target also has health, maximum health, occupancy, sensors, script state,
and normal unit flags. Those systems are progressively enabled or finalized as
construction reaches completion.

### Factory queue

Each live unit owns two singly linked order lists, the primary list and the
secondary list. Which list an order targets is decided solely by one
capability bit in the requested operation's descriptor-table record; there is
no factory-specific queue.

**Established fact — queue roles.** A direct census of all four static
descriptor batches shows the secondary-list bit carried by exactly two
operations: the stockpiled-weapon build order and self-destruct.
Building-build, mobile and aircraft build, get-built, queued move, queued
patrol, and every other operation select the primary list. **Factory products
therefore live in the primary list**, and the secondary list exclusively holds
stockpile-weapon builds and self-destructs.

An order node contains at least:

- operation index and state byte;
- wake/gate mask and a next-eligible absolute tick;
- context unit;
- presentation payload and builder/product payload;
- fixed-point position triple for position-bearing orders;
- product definition identifier;
- remaining build count;
- flags copied from the operation descriptor plus runtime marks;
- creation tick;
- next-node link;
- pending retry mask.

Runtime flag marks observed on nodes: the active-order marker (exactly one
primary node carries it at any time), the auto/default-operation flag, the
queued-issue bit, the non-queued bit, the tombstone applied to non-head nodes
when they are unlinked for cleanup, the stop-building-pending bit, and the
retry mark set by the pump on deferred results. There is **no repetition
flag**: repeats are modeled by the count field plus same-pass state-machine
restarts (below).

### Feature definition

A feature definition supplies at least:

- name;
- footprint;
- 3D object or animated image source;
- maximum/reclaim damage value;
- reclaimable energy and metal pools;
- blocking, indestructible, reclaimable, and auto-reclaimable flags;
- flammability, burn weapon, spark time, and spread chance;
- geothermal flag;
- height and shot-over properties;
- dead, burnt, and reclaimed successor feature links;
- drawing and fog-related flags.

Metal stored on a feature definition is a reclaim reward. It is not the metal
value used by an extractor.

### Feature instance and terrain cell

The world stores feature placement through an attribute-cell grid plus live
feature records. The anchor cell identifies the feature type and state. Other
covered cells use filler sentinels that point conceptually to the same
footprint. A live record carries animation, damage, burning, sinking, and
presentation state as applicable.

Feature placement and removal must update all covered cells as one operation.
The system also notifies derived occupancy and coverage systems after the
footprint changes.

### Geothermal requirement

**There is no geothermal registry.** An earlier reading that invoked a
separate registration helper is contradicted by direct review: the helper in
question is an effect emitter feeding the capped segment ring, not vent
bookkeeping. The geothermal requirement is enforced entirely by the building
footprint validator at placement time, through the yard-map control bytes.

**Established fact — yard-map control bytes.** Yard-map characters map into a
row-major buffer sized by the packed footprint extents:

| character | byte | character | byte |
|---|---|---|---|
| `.` | 0x00 | `c` | 0x2d |
| `C` | 0x35 | `f` | 0x6f |
| `G` | 0x8f | `o` | 0x2f |
| `O` | 0x2b | `w` | 0x37 |
| `Y` | 0x31 | `y` | 0x29 |

Non-building classes do not allocate a yard-map buffer: the definition compiler
parses `YardMap` only when the definition's `BMcode` byte is zero, and stores a
null buffer pointer otherwise. Stock data has `BMcode=0` on buildings and
`BMcode=1` on mobiles.

**Established fact — the parse is not one-to-one.** The compiler walks the
footprint cell by cell and the authored string character by character, and the
two walks are free to fall out of step. A revision of this document that
described the mapping as one-to-one implied a length contract the parser does
not have; forty-six of the 126 stock yard maps disagree with their own
footprint, so that reading makes those buildings unplaceable (see
`docs/SPEC_CONFLICTS.md`). Three rules, all direct from the character loop:

- A character outside the ten-entry table advances the string **without**
  consuming a cell. That is how the spaces stock authors use to lay a yard map
  out in rows disappear (`ARMLAB` writes `yoccoy ooccoo …`), and it also
  silently drops typos rather than rejecting the definition.
- The string pointer advances only when the character **after** the current one
  is not the terminator. Once the string runs out the pointer parks on its final
  character, which therefore repeats for every cell still unfilled. `ARMESTOR`
  authors `YardMap=o` for a 4×4 footprint and gets sixteen `o` cells; `ARMSILO`
  authors nine characters for 5×5 and gets its ninth repeated sixteen times.
- Characters past the last cell are never read. `ARMSOLAR` authors twenty-seven
  characters for a 5×5 footprint; the trailing two are ignored.

The buffer is allocated, not zeroed, so a yard map that is empty or wholly
unusable leaves the compiler reading past the string. No stock definition
reaches that state.

**Established fact — control-byte bit roles in the footprint validator.** Per
covered terrain cell: bit 0 gates an enemy-visibility occupancy test; bits 1-2
reject any nonzero occupant other than the passed self identity; bit 3 enables
slope sampling over the footprint; bit 4 enables height tracking; bit 5
requires the cell to be free of blocking features; bit 6 fails when the
resolved feature's catalog entry is flagged indestructible;
**bit 7 (character `G`) is the geothermal requirement.**

**Established fact — bits 5, 6 and 7 all read authored feature flags.** None of
the three tests is satisfied by the mere presence of a feature. Each resolves
the covered cell's reference to a feature definition and reads one bit of that
definition's flag word:

- **Bit 5** passes unless the definition carries `blocking` (flag-word bit 6,
  mask 0x0040). This distinction decides whether an extractor can be placed on
  its own deposit: metal patches are 3×3 features authored `blocking=0`
  (`indestructible=1`, `reclaimable=0`), while trees and rock clutter are
  authored `blocking=1`. Treating presence alone as blocking makes every metal
  extractor unplaceable, since `ARMMEX`/`CORMEX` author an all-`o` yard map and
  `o` carries bit 5.
- **Bit 6** fails when the definition carries `indestructible` (flag-word bit 9,
  mask 0x0200 — the validator tests bit 1 of the flag word's high byte). It is
  not the `reclaimable` flag, which lives at bit 7 of the same word and is never
  read here. A revision of this document that named bit 6 a "non-reclaimable"
  test had the flag wrong (see `docs/SPEC_CONFLICTS.md`).
- **Bit 7** resolves the feature inside its own branch, so a vent under a cell
  whose yard byte is not `G` satisfies nothing.

Bits 6 and 7 reach a flag only through a definition, so a reference that
occupies without resolving to one passes both. Bit 5 is the exception: it
answers "blocking" for an unresolvable reference without consulting any flag.

**Established fact — what bits 3 and 4 sample, and the gate that consumes it.**
The two bits are not per-cell rejections. They accumulate three aggregates over
the footprint, which a single gate then evaluates after the cell walk finishes:

- On a cell whose byte carries **bit 3**, the running minimum of that cell's low
  height (attribute cell `+6`) and the running maximum of its high height
  (`+5`). The minimum starts at 255 and the maximum at 0.
- On a cell whose byte carries **bit 4**, a separate running maximum of the same
  high height, starting at 0.

The gate is:

```
if geothermalRequired and not geothermalFound:      reject
if maxHigh < minLow:                                 // no cell carried bit 3
    siteHeight = SeaLevel - waterline
else:
    if maxHigh - minLow > MaxSlope:                  reject
    siteHeight = minLow
if bit4Max > siteHeight:                             reject
if minLow  < SeaLevel - MaxWaterDepth:               reject
if max(maxHigh, bit4Max) > SeaLevel - MinWaterDepth: reject
publish siteHeight; accept
```

`MaxSlope`, `MaxWaterDepth` and `MinWaterDepth` are the **movement profile's**,
copied into the definition when it compiled from its named `movementclass` or,
for a class-less building, from a profile built out of the definition's own
authored keys — the same copy that supplies the footprint the placement anchor
uses. `waterline` is the definition's own.

The published `siteHeight` is a global the build ghost and the MOBILEBUILD order
both read [07 §9]; it is what makes the placement rectangle sit flat on the
ground the structure will stand on. The `maxHigh < minLow` branch is reachable:
a yard map made only of characters without bit 3 leaves both aggregates at their
initial values, and the site is then referred to the water surface instead of to
the terrain.

Feature references on covered cells resolve through the signed-offset
resolver. **Established:** the empty sentinel `0xFFFF` resolves empty;
identifiers below `0xFFFB` are live feature-table indices and are bounds-checked
against the catalog (out-of-range behaves as blocking for bit 5 and
non-satisfying for bits 6 and 7); `0xFFFD` void, `0xFFFC`, and `0xFFFB` behave as
occupied void thresholds; and the multi-cell fringe sentinel `0xFFFE` does not
store a feature at all — it stores two signed offset bytes (`int8` DX and DZ,
DZ scaled by map width) that locate the anchor cell, and the resolver re-reads
that anchor's feature word before classifying. **A fringe cell whose anchor hop
finds no live feature is not blocking**: the hop yields the same zero an empty
cell yields, and all three feature branches fall out of it — the dead hop is the
one unresolvable case bit 5 lets through. The offset encoding is signed,
not absolute coordinates: absolute coordinates would require 9 bits to address
the 402×408 corpus, so an 8-bit coordinate field could not be literally true.

**Established fact — geothermal validation.** If any covered cell's yard byte
has bit 7 set (character `G`, byte `0x8F`), validation succeeds only when at
least one covered cell holds a feature whose catalog entry carries the
geothermal flag (`geothermal` definition bit). An absent requirement or a
satisfied one passes through to the slope/height/water checks; an unsatisfied
requirement rejects placement. The validator scans the whole footprint and
succeeds on the first geothermal match — multiple vents under one footprint
satisfy the same single check with no extra bonus.

**Established fact — validator split by product class.** The placement
validator first bounds-checks the rectangle against the map. If the produced
definition is building-class it delegates entirely to the yard-map validator
above, which is where the geothermal rule applies. Mobile products instead run
an inline terrain loop — feature blocking, unit occupancy, water and
min-height, max-height, slope limits — only when the caller's mode requests
terrain checking; other modes accept immediately. Factory production state 2
passes its own class/state flag pair as the mode and a null self identity, so
any foreign occupant rejects the spot. Net effect: the geothermal yard-map rule
gates building placement, while factory-produced mobile units are
terrain-checked only.

**Established fact — vent persistence, extractor sampling, and pools.**
The footprint validator is read-only on the terrain feature grid: it never
clears a vent feature. A geothermal vent therefore persists beneath a completed
plant in the terrain grid; destroying the plant leaves the vent in place with
no restore step needed, and the next placement at the same cells can reuse it.
An extractor's metal amount is sampled once at placement as
`extractsmetal × Σ(unsigned(metalByte) + 1)` over its footprint cells, stored
on the unit, and never resampled — terrain edits after placement do not change
it. Feature metal at the definition is reclaim reward only. Feature catalog
entries are `0x100` bytes each, animation slots are `0x800` entries of
`0x30` bytes, and the plot grid is `0xD` bytes per cell; exhausting the
`0x100` catalog, the `0x800` anim pool, or map bounds causes a silent failure
with no placement and no retry beyond the caller's own retry. Missing successor
links (`featuredead`, `featureburnt`, `featurereclamate`) use the sentinel
`0xFFFF` and a missing chain ends with no corpse rather than a substitution.
Burning sequences are forced to non-looping at load — the loader clears the
loop byte for every burn, burn-shadow, death, and reclaim sequence — so shipped
burns have finite lifetimes (46–282 visits) and end only when the animation
pointer clears; the countdown after `sparktime` fires its one-shot spread and
burn-weapon event and then stays inert. Corpse placement from unit death is
stamped through the same feature placement path at the victim's anchor cell
before the trigger poll for the same tick, so a death and its wreck are visible
to the same tick's victory checks.

## Authoritative settlement order

The economy phase scans the ten player slots in fixed slot-index order and,
on each player's own 30-tick settlement deadline, conditionally runs
settlement for active, non-observer participants. Within a settled player,
units are visited in stable unit-slot order. This order matters for direct
debits such as cloak costs, because earlier units may consume live stock
before later units are tested.

**Established fact — no time conversion anywhere in the ledger.** Authored
economy fields are added to the per-unit accumulators exactly as parsed. There
is no division or multiplication by the tick rate at any point in the
settlement, in either direction. The only floating-point constants the whole
ledger contains are the two special-player scale factors of −0.7 and −0.5. An
implementation must not "convert per-second authored rates to per-tick"; the
authored value *is* the per-pass value.

**Established fact — settlement runs on a per-player 30-tick deadline, not
once per tick.** The per-player settlement call sits inside a per-player
deadline block: each player record carries an absolute deadline tick that is
compared unsigned against the global tick counter; while the deadline is
greater than the global tick the remainder of that player's slot processing —
settlement included — is skipped entirely. When the deadline is due it is
advanced by exactly 30 *before* anything else in the block runs, and there is
no code path into the settlement gates that bypasses this compare. The block's
structure per slot index, ascending:

1. Skip the whole slot unless the record exists, the controller/state byte is
   one of the three active states, and the observer byte excludes observers.
   While skipped, nothing in the slot advances, including its deadline.
2. Per-tick work independent of settlement runs regardless of the deadline:
   an auxiliary player-level object update (itself internally paced by a
   private 30-tick counter), a second helper with its own internal 30-tick
   gate, and a sweep of the player's unit range refreshing weapon/position
   state for units of one type family. This per-tick work never touches
   resource stock.
3. The deadline compare and, when due, the unconditional advance by exactly
   30.
4. Still inside the deadline block: the settlement gate chain — all of the
   following must hold before the settlement entry is called: the player
   record exists; the state byte is active; the observer byte excludes
   observers; the status-pair predicate holds (a nonzero halfword at one
   field **or** a zero word at its neighbor — preserved literally, since the
   pair has no located writer and its semantics are open); the state byte is
   narrowed to one of the two settling states (the third traverses but never
   settles); the game-ended flag bit is clear; and the end-of-game countdown
   is negative.
5. Still inside the deadline block, for the local/human reference slot only:
   the win/lose evaluation that arms and decrements the end-of-game countdown;
   plus interface/view helpers for the local-view slot on the same cadence.

The deadline catch-up edge: because the advance is a single conditional add
rather than a loop, a slot whose deadline fell more than 30 ticks behind the
global tick would settle once per tick on consecutive ticks until it caught
up. No natural runtime path that desynchronizes a slot was identified; this
is structural behavior, not ordinary-play cadence.

**Established fact — seeding, setup pass, and persistence.** Battle
initialization seeds every active player's settlement deadline (and two
sibling deadline fields) to the current global tick, so all players share the
same initial phase; mission setup then runs one full pass of the player phase
before starting resources are granted, and spawn credits are written directly
to live stocks outside the ledger — starting resources never flow through the
settlement allocator. There is no mid-game re-seeding path. Each player's
deadline is persisted in saves under the key `UpdateTime` (sibling keys
`WinLoseTime` and `DisplayTimer` for the two sibling fields) and restored
verbatim: deadlines are absolute tick values and are not re-seeded on load,
so each player resumes its own saved settlement phase after a load. Because
the global tick itself is persisted inside the game-time account blob, phases
remain mutually aligned across save/load. The end-of-game freeze pair lives
outside both blobs and restarts open after a load.

**Established fact — rate units.** An authored economy field is a
per-settlement-pass amount, consumed once per settlement pass — that is, once
per ~30 ticks per player under ordinary play. Because deadlines are seeded
identically at battle start, the passes are phase-aligned in practice:
effectively one global 30-tick economy period, delivered as ten per-player
deadlines. Every rate-scale statement derived from "one authored unit is one
tick's worth" was wrong by a factor of thirty.

The high-level pass is:

1. clear pass-local production, request, acceptance, and capacity totals;
2. visit every eligible unit owned by the player;
3. derive passive production, active use, extraction, storage, and cloak
   effects from the unit and its definition;
4. fold the player-level bucket into the same totals;
5. settle old carry before newly accepted work, independently for energy and
   metal;
6. write remaining carry back to each unit and to the player bucket;
7. update current stocks, capacities, per-pass counters, cumulative totals,
   and waste;
8. archive and clear live bucket inputs for the next pass;
9. let later tick phases observe the new economic and operational state.

Periodic allied sharing is not part of each player's settlement call. The
sharing dispatcher is invoked once per tick after the player phase returns,
for the reference player only, and self-gates its work: metal/energy
transfers run only when the global tick is a multiple of sixty and sensor
sharing when it is a multiple of 450. Sharing therefore mutates live stocks
between settlement passes and affects subsequent passes.

Construction handlers may add requests and accepted work before the
settlement pass that pays them. The precise same-pass relationship varies by
the handler's phase and is specified below where established.

**Established fact — end-of-game freeze (a gate inside the deadline block,
not the cadence).** The countdown is initialized to −1 at session setup, so
the gate starts satisfied. One arm/decrement family sits inside the local
player's 30-tick deadline block behind mission-end predicates and human-
presence checks; a second site sits after the slot loop and decrements every
tick for games with no human participants. Both arm the counter at 4 and
decrement on their own cadence; after roughly five one-second steps it passes
below zero and latches the game-ended flag bits (bit patterns vary by
victory/defeat/watch branch). The network path latches the game-ended bit
directly on a game-over message. Nothing ever clears the game-ended bit once
set, so the economy stays frozen for the rest of the session. The freeze
pair's pacing still yields the observed ≈5-step confirmation delay before the
latch.

## Resource contributions

### Completed-unit eligibility

Passive production and storage contribution require the unit to be alive and
complete. A remaining construction fraction of zero is the direct completion
gate used by the economy pass.

Operational and activation flags further gate active use and generator paths.
Transported, dead, disabled, or otherwise ineligible units are skipped or
handled by their relevant state branch.

### Passive energy and metal

A completed eligible unit adds its definition's passive energy and passive
metal values to its per-pass production buckets. These authored values enter
the ledger as direct per-settlement-pass deltas, delivered once per
settlement pass — once per ~30 ticks per player under ordinary play. The
retail path neither divides nor multiplies ordinary production and use values
by thirty.

Negative authored energy use takes a distinct refund/production path. Special
player states can apply one of two executable-defined discounts through a
global mode selector: selector value 0 scales the amount by 0.5 and selector
value 1 scales it by 0.7 (the refund subtracts the scaled amount instead of
adding it). The user-facing identity of those player modes remains open, so a
clean-room implementation should isolate that adjustment behind a
compatibility rule.

### Wind generation

The battle holds a current wind strength, a 16-bit wind direction, and a
normalized scalar. An eligible wind generator contributes:

`energy production = current wind scalar × unit wind multiplier`

The generation contract is closed. At battle setup the briefing seeds strength
as `CRT() % (max-min+1) + min` and a six-bit direction as `CRT() & 0x3f`.
The next update is scheduled from another CRT draw as
`((CRT() * 10) / 0x8000 + 5) * 30` ticks ahead — **150 to 420 ticks**, about five
to fourteen seconds. At the change, new strength is
`simRand(maxWind-minWind)+minWind` and the new direction, when strength is
nonzero, is `simRand(0x10000)` over the full 16-bit angle domain. The change
takes effect instantly (there is no interpolation), the world X/Z wind vectors
are recomputed from direction and strength, and the normalized scalar fed to generators is
strength divided by a fixed 5000 denominator clamped to one. Wind-generator
units receive `SetDirection`/`SetSpeed` script notifications only on change
ticks — a burst of callbacks to every wind generator, not continuous polling —
and the build-assist bonus is disabled when maximum wind is below half its
denominator. A per-tick jitter phase also nudges particle drift with the same
field; that consumer is presentation-side.

### Tidal generation

An eligible tidal generator contributes:

`energy production = map tidal strength × unit tidal multiplier`

Tidal strength is loaded for the battle and does not use the current wind
scalar.

### Constant metal makers

The constant metal-maker field is stored as a byte. When it is enabled and
the unit passes its active-state gates, the economy path converts the byte to
a floating-point value and contributes that value to metal production. A
stored value of one therefore contributes one unit of metal per settlement
pass (once per ~30 ticks per player under ordinary play). Energy consumption
is a separate authored active-use demand;
the maker's metal output and energy admission must therefore be evaluated as
two coupled pieces of unit state rather than as an invented conversion ratio.

The exact stall interaction for a maker whose energy request cannot be paid is
not fully closed.

### Terrain metal extraction

Terrain attribute cells carry an unsigned metal byte. When the mission provides
a uniform surface-metal value, map loading initializes the cell metal field
from it; maps can also supply per-cell values.

When a metal-extracting unit is placed, the engine samples every cell in its
footprint. For each cell it adds one to the unsigned metal byte before adding
it to the footprint sum. The placement-time result is:

`sampled metal = extracts-metal multiplier × Σ(cell metal byte + 1)`

The executable performs the intermediate sum with fixed-point-shaped integer
arithmetic and then converts it to a single-precision value. The algebraic
result above is the clean-room contract; an exact compatibility mode must also
preserve the retail conversion and rounding order.

The sampled amount is stored on the unit instance. It is not resampled every
economy pass. While the extractor is eligible and active, that stored amount
is added directly to its metal-production bucket.

This produces several important consequences:

- a cell whose stored metal byte is zero still contributes one unit to the
  footprint sum before multiplication;
- a larger footprint samples more cells;
- two extractors may sample overlapping cells unless the placement and
  occupancy rules prevent the overlap;
- later terrain or feature changes do not automatically change an already
  stored extractor amount;
- feature reclaim metal and terrain extraction metal are unrelated fields.

### Storage capacity

At each settlement pass, the engine rebuilds player capacity from scratch by
summing the energy- and metal-storage contributions of eligible completed
units. Optional mission/player bonuses are added when their enable flag is set.
Retail implements this with a capacity helper that applies the storage bonus
and the per-resource floor: when the bonus enable bit is set, each resource's
capacity is floored at 200, and the bonus values are converted to
single-precision and added to the player's live energy and metal capacity
fields after the unit sum (the economy ledger analysis notes pin the add
order, §4.1 and §5).

Capacity is single-precision state. It is not an integer total. Destroyed,
unfinished, or ineligible storage units cease contributing when the next
player pass recomputes capacity. Bonus addition converts the stored integer
bonus to single precision — exact over the 200 floor range — and any
float-to-integer conversion at the capacity add truncates toward zero
(INVARIANTS I3). The battle-init capacity writer runs the capacity helper per
active player before the spawn-credit grant to preserve the opening 1000/1000
stocks past the 30-tick settlement clamp; without the bonus the clamp to zero
would zero both players at the first settlement [OX P1].

### Cloak debit

Cloak upkeep is not admitted through the normal smooth allocation path. When
the cloak gate is due, the engine:

1. chooses the stationary or moving cloak cost;
2. converts the cost to an integer using the retail floating-point conversion
   environment and truncation toward zero;
3. compares that integerized cost with the owner's live energy stock;
4. if affordable, subtracts it immediately and records an energy request;
5. toggles the unit's operational/building bit through the normal transition
   helper;
6. if unaffordable, takes the failure transition without a partial payment.

Because units are visited in stable order, simultaneous cloak costs are
sequential: an earlier slot can make a later slot fail during the same pass.
The debit executes at the settlement cadence — at most once per 30 ticks per
owning player, in the settlement's stable unit-slot order; authored cloak
costs are per-settlement-pass amounts like every other authored economy
field. A separate per-unit cloak payment deadline gates *whether* an
individual unit owes a debit on a given pass (alongside its cooldown state);
it paces eligibility, not ledger visitation. The full predicate that enables
this debit for special player states remains open.

## Resource admission and carry

### Two-resource admission

Construction uses a helper that accepts an energy demand and a metal demand as
one transaction. The helper always records both requested amounts. It records
both as newly accepted work only if the existing carry for both resources is
not positive. If either resource still has positive carry, the new work is
denied as a whole.

This is a hard admission gate, not a partial-work decision. Once work has been
admitted, later settlement may pay only a fraction and carry the rest.

### One-resource admission

Repair uses a related admission path that gates only on the builder's energy
carry and touches only the energy ledger. The repair helper forms its health
term from the target's maximum damage and its resource term from the target's
energy build cost; it then passes the resource term to this helper against the
builder's energy subrecord. The helper always adds the amount to energy
requested and adds it to energy accepted only when energy carry is non-positive.
It does not address the metal subrecord. The precise user-interface identity of
the reversed-argument variant and the behavior for malformed zero or negative
authored inputs remain open, but the energy resource term and the energy-gated
admission are established.

### Direct two-resource payment

Weapon fire and some immediate operations use a different helper that compares
live player stock with an energy amount and a metal amount. It either debits
both in full or debits neither. It also updates the associated storage-object
transaction logs. This path does not create proportional carry.

## Two-stage settlement algorithm

Energy and metal are settled independently. There is no single combined
energy/metal shortage ratio.

For one resource, define:

- `opening` as the player's live stock entering the settlement stage;
- `production` as current-pass production;
- `debt[i]` as each unit's positive carry from earlier admitted work;
- `accepted[i]` as each unit's newly accepted work;
- `pool = opening + production`.

The retail pass first services debt:

`debt ratio = min(1, pool / Σ debt)`

with the zero-debt case treated as fully funded. It then computes the pool
remaining after funded debt and services newly accepted work:

`accept ratio = min(1, remaining pool / Σ accepted)`

Each unit is scaled uniformly within each stage. There is no largest-remainder
distribution and no forced minimum work quantum in the settlement loop.

The new carry for a unit is:

`new carry = old carry × (1 - debt ratio) + accepted × (1 - accept ratio)`

The same calculation is applied to the player-level mirror bucket. Energy and
metal use their own pool, totals, and ratios, so one resource can be completely
funded while the other remains short.

The engine performs this work with single-precision values and the retail x87
environment. Exact compatibility requires preserving evaluation order and
conversion points rather than recomputing the equations with arbitrary higher
precision.

## Stocks, counters, and waste

Per-pass produced and consumed counters and cumulative double-precision totals
are committed before opening stock is folded into the allocation pool. They
therefore report activity for the pass, not the player's total funds available
to pay it.

After settlement:

- the resulting stock is clamped to the rebuilt storage capacity;
- overflow beyond capacity is added to cumulative waste with its fractional
  part preserved;
- current stock remains a single-precision value;
- live unit bucket slots are archived where required and zeroed for reuse;
- the player mirror bucket is handled with the same subrecord shape.

The player-level mirror bucket's writer set is closed: record initialization
and per-pass clearing, the save/load overlay, the two-resource and
one-resource admission helpers, the immediate debit path, the sharing
transfer helpers, the spawn-credit grant, and the construction-termination
credit for metal spent on an unfinished build. Exactly two call sites use the
two-resource helper, there is **no factory queue-draw writer**, and units
spawned directly (mission setup) receive their production credit at spawn.
The consumers of the per-unit archived snapshots remain open.

## Activation and stall transitions

The economy and work paths use a common transition service to change operational
bits. It suppresses duplicate transitions and invokes script callbacks such as
activation/deactivation and start/stop building when the relevant bit actually
changes.

A shortage can therefore affect more than numerical work. It can change the
unit's operational state and cause script-visible transitions. The exact
mapping of every bit and callback is specified in the unit-script document;
this document requires only that economic admission use that shared service.

## Allied resource and sensor sharing

### Manual transfer

Energy and metal transfer helpers:

- reject observer/invalid player slots;
- optionally clamp the amount to the source's available share buffer;
- do nothing for a zero amount;
- debit the source through the corresponding storage object;
- credit the destination, with special-player adjustments where enabled;
- when requested, emit a deterministic multiplayer command describing source,
  destination, resource kind, and amount.

Energy and metal use parallel but separate paths.

### Automatic transfer

Every sixty authoritative ticks, the automatic-sharing dispatcher considers
metal and energy independently for its source player. The corresponding source
option must be enabled, and source current stock must exceed its separate
sharing threshold. These thresholds are written once at battle setup from
capacity but live at distinct fields from capacity; they are not aliases of it.

The dispatcher scans player slots from zero through nine. Every eligible
allied candidate with lower current stock replaces the previous candidate, so
the last qualifying slot wins. Alliance is checked through a per-player
alliance byte, not through a sharing flag. The exact semantic names of all
status and alliance predicates remain partly unresolved.

For metal, the transfer is:

`min(destination capacity - destination current, (source current - source threshold) × 0.333333343)`

For energy, it is:

`min(destination capacity - destination current, (source current - source threshold) × 0.5)`

The amounts are clamped by the destination capacity gap so a transfer never
overfills beyond capacity. The helpers then debit the source storage object
and credit the destination; when requested they also emit a deterministic
multiplayer sharing packet, and receivers copy the fields overwrite-sync with
no comparison, no threshold, and no abort. A special recipient state can
discount the credited amount. The helpers do not branch on the source-deduction
helper's Boolean result before crediting; whether normal callers can reach a
failed deduction after the outer clamp remains unknown.

**Established fact — maker stall and negative fields.** A metal maker or an
extractor contributes nothing for that settlement pass when the owning unit's
energy carry is strictly positive — the maker stalls. An authored negative
energy use is not a demand but a refund: it is added to energy production, and
when the owning player's control state is the special second state the refund
is scaled by one half or seven tenths depending on a global mode selector, with
the pairing inverted relative to the capture refund site.

### Sensor sharing

Every 450 authoritative ticks, a separate option can emit a radar/sensor share
command for allied players. The visibility document defines what state is
shared; this document defines only the player option, cadence, and networked
transfer trigger.

## Unit creation and limits

### Fixed unit slots

Units occupy a fixed pool and use stable slot numbers without generation
counters. Slot zero is reserved as a null identity. Allocation scans from the
lowest usable slot and takes the first free entry. Failure returns no unit.

The absence of generations means an old slot reference can alias a later unit
after reuse. Systems that retain unit identities must follow the retail
validation rules rather than inventing generation-tagged handles.

### Nanoframe allocation

Creating an unfinished unit allocates a normal unit slot, attaches its
definition and owner, initializes script and order state, marks the unit
alive, sets the remaining construction fraction to one and its health to
zero, and clears the script-owned build-stance bits. A failed allocation does
not create a partial object; the per-definition unit limit is enforced at
exactly this instant (below). Factory-produced units are created directly at
the resolved exit spot by the production state machine (below); other paths
create the nanoframe at the requested position.

Construction then progressively changes health and remaining fraction. The
unit is visible to later phases according to the core tick order even while it
is unfinished, but systems use the remaining-fraction and state flags to decide
which capabilities are active.

### Limit accounting

Limits are enforced at exactly one place: **inside the unit allocator, at the
instant of nanoframe creation**, and only when the definition is flagged
buildable. Each definition carries a per-definition limit field whose sentinel
of -1 means unlimited; when a finite limit is set, the allocator counts live
instances with a matching definition index **within the owning player's
contiguous instance slice only** — there is no cross-player accounting site —
and refuses creation when the count has reached the limit. Queued factory
products hold no reservation: a full queue simply fails each allocation attempt
and retries. Capture transfer validates the same limits before replacing
ownership. Failure surfaces as the production handler's "unable to create any
more units" message plus an exact 300-tick retry. Exhausting the player's
instance pool produces the same failure path.

The `norestrict` capability parses into the definition's capability word but no
reviewed consumer reads it — bounded absence; the limit check does not consult
it. No parser key or reviewed initializer writes the per-definition limit
field, so stock defaults come from outside the reviewed corpus (the -1
sentinel implies unlimited by default).

## Build request and factory queue behavior

### Request creation

A build request is resolved from its authored unit name through the unit
catalog. The request is rejected if the name is invalid, the requester and
resolved owner do not agree, or required order data cannot be constructed.

The request is classified into mobile build, aircraft build, building
placement, stockpiled weapon build, or another small order payload. The
operation descriptor selects the target list and the payload constructor;
factory products always land in the primary list (above).

### Queue insertion

Positive insertion behaves as follows:

1. select the list from the operation descriptor's secondary-list bit;
2. walk to the tail of that list;
3. if the tail matches both the operation byte and the product type, add to
   that tail's remaining count and return — coalescing is **tail-only**: it
   never merges distinct products and never merges an identical product
   separated by other products;
4. otherwise allocate and construct a node (flags seeded from the descriptor
   static mask) and insert it:
   - an ordinary queued primary order is placed immediately **after the
     single active-order marker node** in the primary list (or appended when
     no marker exists, or becomes head on an empty list), so repeated UI adds
     queue FIFO directly behind the active order;
   - secondary-list orders and position-bearing special orders are
     head-inserted at the front of their list, inheriting the old head's
     auto-operation flag;
5. if allocation fails, leave the existing queue unchanged.

Issuing an order in non-queued mode first purges the primary list of every
node lacking the protected flag (all are cleaned up and freed; protected
nodes survive). Issuing any primary order also drops leading auto/default-op
nodes wherever they currently live.

### Queue subtraction

Negative insertion behaves as follows:

1. select the same list used by insertion;
2. find the last (tail-most) node matching operation byte and product type,
   repeatedly until the requested amount is consumed;
3. reduce its count when it holds more than the requested subtraction;
4. otherwise consume its entire count, unlink the node, run its cleanup
   notification, and free it, carrying the remainder into the next match;
5. stop when the subtraction is satisfied or no matching node remains.

A node unlinked while not at the compared head receives the tombstone mark so
its cleanup skips target-cleared notification. The head comparison always
uses the primary anchor even for secondary-list removals, so stockpile-weapon
and self-destruct cancellations are effectively always tombstoned.

Cleanup notifications follow a strict order: restore the node's interface;
invoke the operation handler with the cancel-notification mask when the wake
byte requests it; emit the stop-building script callback plus its network
event when the pending flag is set; release the presentation payload; and
finally — only when the node is *not* tombstoned — clear all three weapon
build targets and fire the target-cleared callback for each that was set.

### Queue pumping and result codes

The primary pump runs per live unit each tick and restarts from the head
after every dispatch:

1. An empty primary list attempts to create one idle default-order node, only
   when the owner's state byte is one of the two settling states and the
   definition names a nonzero default idle op; the created auto-flagged node
   is head-inserted into its own descriptor-selected list.
2. A node whose deadline has arrived clears the deadline and raises its
   retry-pending bit.
3. The combined wake test ANDs the node's pending mask (or the unit's pending
   word) against the node's wake mask. A blocked front order returns without
   dispatching — **a blocked FRONT order blocks every later order in the
   primary list**, which is what serializes factory production.
4. Otherwise the combined bits are consumed from the unit's pending word and
   the node, and the operation handler runs with them.
5. Handler result codes: `0` restarts the state machine at state 0; `1`
   advances the state byte; `2` and `4` stay; `3` schedules a retry at the
   global tick plus a random 15 plus 30 with wake bit 1; `5` and `8` unlink,
   tombstone-if-not-head, clean up, and free the node; `6` unlinks the node
   and re-appends it at the tail (yield; stays queued); `7` cancels all
   primary nodes (tombstoning non-heads) and removes every secondary node via
   the pair-removal helper before returning; `9` sets the retry mark and
   either restarts at state 0 with a randomized 30-plus-random-30 retry when
   no successor exists or drops the node when one does; values above 9
   delegate to the order-expiry helper.

The secondary pump walks front to back and dispatches a node whose wake mask
is zero or whose deadline has arrived, using mask 0; not-yet-due nodes are
*skipped*, not blocking — later entries still run. Its result table differs:
codes `6` and `7` both remove the single node and return (no tail yield, no
cancel-all); codes 0/1/2/3/4/5/8/9 behave as above; above-9 unlinks and
frees. Factory products never reach this pump, so production serialization
rests entirely on the primary pump's front-blocking rule.

### Factory production lifecycle

The building-production handler runs as a five-state machine driven by the
primary pump; interrupt masks are tested before the state machine with
cancel-current first.

**State 0: the count gate — Established (2026-08-26).** State 0 is three
branches long and takes no deadline and no wake bit:

1. clear the order node's presentation goal payload;
2. if the factory's **building-class runtime status bit** is clear, return the
   cancel-all result code — the handler itself never touches the queue, the
   pump performs the cancel;
3. otherwise, if the node's remaining count is greater than zero, raise the
   activate edge and return the advance result code, so the phase increments
   and the pump restarts at the head **in the same pass** — state 1 runs
   immediately, with no one-tick wait;
4. if the count is not greater than zero, lower the edge and return the free
   result code, unlinking and freeing the node.

The building-class test is the runtime status bit the allocator initializer
sets from the definition's authored `bmcode` byte, described under
[08 "Classifier eligibility, destinations, and order"]. It is **not** a
yard-map, footprint, or immobility heuristic; a definition whose yard map,
footprint, and mobility disagree with its bmcode would be classified
differently by such a heuristic.

Audit: an earlier implementation gave state 0 a one-tick deadline and armed
wake bit 2 before entering state 1, and derived building class from a local
yard-map/footprint/immobility predicate. Both were guesses recorded as such at
the site; neither appears in the handler. The invented deadline delayed every
factory product by one tick and armed a gate retail never arms. The same
implementation expressed the cancel-all branch by binding a fresh queue to the
unit, which additionally discarded the queue's service bindings.

**Exit-spot acquisition (state 2).** Exact order:

1. query the factory script's build-info piece (the query argument is
   pre-initialized to −1) to obtain the exit piece index;
2. resolve that piece's transform plus the factory origin to a world
   position;
3. store the position on the order node;
4. load the product definition and snap the position to map cells using the
   packed footprint extents — each coordinate converts from internal
   fixed-point to a cell index biased by half its extent, producing the
   footprint rectangle origin.

**Silent blocked revalidation before allocation.** The snapped rectangle is
area-validated with the factory's class/state flag pair as the mode and a
null self identity. On failure the node schedules a retry in **exactly 15
ticks**, sets wake bit 2, and stays: no product exists yet, so the retry is
silent — no message, no sound, no allocation — and repeats every 15 ticks for
as long as the footprint is obstructed. There is no timeout and no
force-placement.

**Nanoframe creation at the exit spot.** On validation success the allocator
creates the unit *at* the exit spot with owner, product definition, remaining
fraction one, health zero, and build stance cleared; the product pointer is
linked into the node payload. If the allocator refuses (limits or pool
exhaustion), the handler prints "Unable to create any more units", schedules
a retry in exactly 300 ticks (not randomized), and stays in state 2.

**Success epilogue.** Message "Starting construction"; register the builder
link on the product; copy standing-order bits 18-19 (standing move) and 20-21
(standing fire) from the factory's class/state word onto the product's;
resolve the get-built operation and insert a get-built node onto the
**product's own primary queue** (queued mode, zero count); raise the
start-building edge (rising edge fires the COB callback); refresh the builder
interface; advance to state 3.

**State gates around placement.** State 0 clears the presentation payload;
for building-class contexts a positive count raises the activate edge and
waits while a nonpositive count lowers it and frees the node; non-building
contexts fall through to cancel-all. State 1 advances only when the script
has set the in-build-stance bit, otherwise it waits with wake bit 2 — the
yard-door handshake is entirely script-owned, with the engine raising
Activate and waiting.

**Work loop (state 3).** With a product attached, the shared work helper runs
with the floor of the factory definition's worker time divided by thirty;
accepted work emits nano presentation over the product footprint bounds. A
remaining fraction of zero advances to completion; otherwise the node retries
one tick later with wake bits 1 and 3. A node that has lost its product falls
through to result 7 — losing the product cancels **all** of the factory's
orders.

**Completion (state 4) and repetition.** The engine prints no text, lowers
the start-building edge, runs the completion transition (product remaining
fraction to zero, completion flag set, activation per standing-order bits,
cloak/init posture for flagged definitions, selection and interface
refreshes), clears the presentation payload, decrements the node's remaining
count once, refreshes the interface, and returns result 0 — the state machine
restarts at state 0 **within the same pump pass**, so coalesced counts build
back-to-back with no gap; the final unit frees the node via the state-0 drop.

**There is no repeat flag.** A full census of the node flag word across all
descriptor batches, the constructor, insertion, and pump writes contains no
repetition semantic. Repetition works exclusively through the remaining-count
field (filled by tail coalescing, decremented once per completion, cancelled
via subtraction or the stopped interrupt) plus the same-pass state-0 restart.
An engine-level repeat toggle would be an extension with no retail
counterpart.

**Established fact — link lifetime and completion order.** The builder and
product links on the factory node are cleared on normal completion. If the
factory dies or is captured before completion, the links are not walked and the
nodes leak with the dead header — there is no death-time reclamation walk over
the order lists. Completion lowers the StopBuilding edge before running the
completion transition, not after. Health and remaining fraction written by the
shared work helper are visible immediately to later builders visited in the same
unit sweep, so the lowest-slot builder among multiple contributors wins the
final step; a later builder seeing zero remaining simply returns without further
work.

**Established fact — same-tick visibility.** A product's health and remaining
fraction, lowered by the builder that completes it, are therefore authoritative
for every later builder in that same sweep. Its occupancy and line-of-sight
stamp, however, are published only after settlement in the same tick, so the
product is targetable by the projectile phase earlier than its blocking or
visibility is stamped. The GetBuilt rally on the product is dispatched in the
same tick as completion when the product's slot sorts after the builder's slot,
otherwise it waits until the next tick. Trigger polling that checks BuildUnitType
counts runs only for the local player when that player's settlement deadline is
due, so the victory poll can see a just-completed product on the same tick only
when the deadline is due — otherwise it lags up to a full settlement period.

**Established fact — interrupt producers.** The bodies of the two construction
interrupts are established — cancel-current computes its refund and issues the
cause-9 kill while construction-stopped decrements count and stays — but the
upstream producers of interrupt masks 2 and 8 sit in the UI and network command
layers and remain unidentified. Their effects must be preserved behind those
masks without inventing a producer.

### Rally inheritance

**Rally inheritance is the factory's own queued orders.** When a product
completes, its get-built order walks the *builder's* primary queue front to
back and re-enqueues every queued-move or queued-patrol node on the product
as a real move or patrol order (operation class resolved by name; position
triple copied from the factory's node payload), so multiple waypoints are
inherited in factory-queue traversal order and patrol rallies reproduce as
patrols. The factory itself never moves. While the product is still under
construction the get-built order retries (300 ticks at state 0, 30 at state
1, or waiting on its presentation wake bit at state 2) and proceeds in the
same dispatch once built.

Standing-order bits are additionally gated: both product and builder must
carry the mobile/class flag and neither may carry the auto flag before
standing-move (bits 18-19) and standing-fire (bits 20-21) are copied; the
experience word copies only for computer-owned builders. If nothing was
inherited, the product receives the park operation. The get-built node then
drops itself.

### Cancel-current and stop interrupts

**Cancel-current** (interrupt mask bit 1, highest priority) with a product
attached:

1. compute the refund `trunc((1 - remaining fraction) × metal build cost)`;
2. normally **add** that amount to the builder's metal bucket; but when the
   referenced player object is in the special second state, a global mode
   selector decides: selector value 0 subtracts seven tenths of the amount,
   selector value 1 subtracts one half of it, any other value falls back to
   adding. (This site's pairing is inverted relative to the ledger's
   negative-energy-use refund site; both pairings are verified.)
3. run the completion transition;
4. send the ordinary kill packet — kind-9 damage of exactly 30000 through
   the normal death flow, unscaled by the armor branch because scaling
   requires damage below 30000 — so wreck rules apply; note cause-9 deaths
   skip the killed-severity script query entirely (severity zero, no
   explosion, no corpse);
5. lower the deactivate and start-building callback bits in one edge call
   (firing both COB callbacks together), refresh the interface, and return
   the drop result — the whole node drops **without decrementing its
   remaining count**.

With no product attached the same epilogue runs and the node still drops.
The producers of interrupt masks 2 and 8 sit upstream in the UI/network
command layer and remain unidentified.

The other interrupt (**mask bit 3, "Construction stopped"**) prints its
message, decrements the node count **once**, refreshes the interface, and
returns result 0 — the node survives and the state machine restarts.

## Construction arithmetic

### Worker quantum

The normal construction callers derive an integer worker quantum:

`worker = floor(builder worker time / 30)`

The division is performed before the main remaining-fraction calculation. A
definition whose worker time is smaller than thirty can therefore produce a
zero quantum in this path unless a distinct caller supplies another value.

### Remaining fraction

Let:

- `old` be the target's remaining construction fraction;
- `worker` be the integer quantum supplied by the caller;
- `buildTime` be the target definition's build time.

The progress helper computes:

`new = clamp(old - worker / buildTime, 0, 1)`

The resource demands admitted for this step are proportional to the decrease:

- `energy demand = total energy cost × (old - new)`;
- `metal demand = total metal cost × (old - new)`.

The helper commits the step only when the two-resource admission service
accepts both resource buckets. If admission fails, it does not advance the
remaining fraction.

### Health gain and fractional carry

Health is derived from the remaining fraction rather than accumulated from a
separately rounded per-tick rate:

`health gain = trunc(maxDamage × old) - trunc(maxDamage × new)`

This difference-of-truncations preserves sub-health-point progress across
calls. It also means a clean implementation must update the remaining fraction
and health in the retail order; independently rounding `maxDamage × delta`
does not have identical edge behavior.

### Multiple builders

Multiple eligible builders may work on one target. Each builder is range- and
state-checked, then contributes through the same construction helper, where the
new remaining fraction is clamped between zero and one and health is updated as
the difference of truncations described above. Because requests are created and
settled in stable player and then pool-slot ascending order and every write to
remaining and health is immediately visible, the lowest-slot builder that brings
remaining to zero is the winner; later builders in the same sweep see zero and
do no further work. Clamping is therefore authoritative for same-tick
cooperation.

### Completion

When the remaining fraction reaches zero, the engine finalizes the unit's
construction state, occupancy, sensors, economic eligibility, order state,
and relevant script callbacks. Some presentation changes, such as replacing
the nanoframe reveal with the complete model, are consumed by the renderer.
The helper clamps the new remaining value between zero and one, so no fraction
escapes that range. Completion of a factory product lowers StopBuilding before
the transition helper runs, as noted above.

The exact order of every completion side effect beyond that is not fully typed. The
remaining-fraction transition and health arithmetic are established; callback,
yard, sensor, and factory-exit ordering remain incomplete.

### Stockpile production

**Established fact — state machine and progress.** A stockpile queue node
carries a selected weapon slot index, a signed remaining-round count, and a
current-round progress value. Each work visit that reaches the production state
advances progress by five, capped at the selected weapon's compiled reload-time
value. For energy and metal separately the admitted demand for that visit is
the difference of two independently truncated cumulative proportional costs:

`energyDelta = trunc(next × energyCost / buildTime) - trunc(old × energyCost / buildTime)`

`metalDelta  = trunc(next × metalCost  / buildTime) - trunc(old × metalCost  / buildTime)`

Metal is computed before energy on the floating-point stack and the pair is
admitted through the ordinary two-resource helper. Progress is not advanced on
rejection, but both requested amounts have already been recorded.

**Established fact — retry and completion.** Failed admission schedules a
ten-tick retry deadline; accepted but incomplete work schedules a five-tick
retry. Completion increments the selected slot's byte-sized completed-ammunition
remainder, decrements the queue node's signed count, and requests a
selected-unit presentation refresh that does not itself mutate count or progress.
Starting another round is blocked with a 300-tick wait when the slot byte is
already greater than 199; the ordinary path can reach 200 but does not start a
round beyond it. Assets whose build time is at most five can complete multiple
queued rounds in one unit visit while admission remains open. The queue driver
redispatches immediately after the state that increments progress, so the
production state can enter the completion state in the same visit.

Eight stockpile weapon definitions are shipped (`amd_rocket`, `armemp_weapon`,
`armscab_weapon`, `cormabm_weapon`, `cortron_weapon`, `crblmssl`,
`fmd_rocket`, `nuclear_missile`); the behavior above is therefore stock-visible
rather than malformed-only.

**Unknown for stockpile:** all-slot mapping validation for every weapon
combination, byte wrap for malformed preexisting values above 200,
cancellation interaction with admitted carry, repeat requeue, and presentation
behavior beyond the refresh call.

## Construction nano cadence and admission [R-P0-06]

This section closes the construction nano-cadence contract: retail has **no
independent "nano every N ticks" presentation timer**. Nano output is admitted
by the construction/reclaim work paths, so the visual pulse follows accepted
work, not a free-running clock.

### R-P0-06 §1 — Work-admission gating and emission producers

The emission producers, their admission gates, and their cadences:

| Path | Work/admission condition | Query timing | Segment count | Next work timing |
| --- | --- | --- | --- | --- |
| Mobile construction | shared construction work helper accepts the builder's worker quantum | once after accepted work in state 3 | one | unfinished target retries after one tick |
| Factory product construction | same two-resource work helper accepts the factory worker quantum | once after accepted work in state 3 | one | unfinished product retries after one tick |
| Build assist | assist state/counter admits the visit; the direct counter gate keeps the counter above 15 | once for the visit | two | assist state continues under its own counter/deadline |
| Reclaim/capture | target is valid and in range, and the operation is admitted | once per emitted segment | one | operation schedules the next visit two ticks later |
| Repair | repair work helper admits the visit | once after accepted work | one | unfinished repair retries after one tick |

An unfinished target retries its work state one tick later, so ordinary
construction cadence follows accepted work visits rather than a visual clock.
**No query and no segment is emitted when the two-resource construction
admission rejects the work step.** The presentation contract is therefore:
publish a nano event only for an accepted work transition, carrying the
builder/source, the target/site, the mode, and the resolved source piece.

### R-P0-06 §2 — Synchronous QueryNanoPiece contract

`QueryNanoPiece` is a mode-Q, zero-argument COB callback [04 §4.4; fmt cob
"QueryNanoPiece"]. The engine seeds its cell-0 output to piece index `0`,
passes no cells 1-3 for copy-back, and waits for the synchronous script call
to finish. The returned piece index is then transformed through the unit's
piece hierarchy and added to the unit's world position to produce the nano
origin. A script that does not overwrite cell 0 therefore leaves the seeded
piece 0; stock builders can select or alternate their spray pieces through
script output, not through an engine-side alternation counter.

The recovered helper is conceptually:

```text
piece = 0
piece = QueryNanoPiece(piece)     // synchronous Q, cell 0
offset = worldPieceTransform(unit, piece)
nanoWorld = unitWorldPosition + offset
```

The query is presentation-side data acquisition. It does not itself modify
construction fraction, health, resources, occupancy, or order state [04 §4.4].

### R-P0-06 §3 — Worker quantum, visit schedule, and counter gates

The mobile and factory state-3 paths call the shared construction work helper
with the floor of worker time divided by 30 — the worker quantum of [05
"Construction arithmetic"]. On nonzero admission they query the builder's or
factory's nano piece and submit one segment; if the remaining fraction is not
zero they reschedule one tick later. The factory path uses the same contract
for the product's build work [05 "Factory production lifecycle"].

The direct static emission census recovers the remaining cadence boundaries:

- the build-assist path makes two segment calls when its remaining work
  counter sits above the recovered `15` gate;
- reclaim's nano counter advances by two per visit and the operation schedules
  the next visit on a two-tick cadence; and
- capture follows the same one-segment-per-visit pattern, also at two ticks
  per visit.

These are operation-specific paths, not a universal construction timer [03
§5.5; 05 "Repair"; 05 "Unit reclaim"; 05 "Capture"].

### R-P0-06 §4 — Segment geometry and selector

Every recovered nano-segment producer passes the selector value `6` to the
segment helper. This is an effect/visual selector, not a weapon `sprayangle`
field and not a simulation RNG bound.

The line endpoints are built from:

1. the world position returned by `QueryNanoPiece`; and
2. the target/product position plus the definition's resolved footprint/model
   extents (the paired X/Y/Z offsets around the target).

Construction therefore draws from the builder/factory nano piece toward the
target footprint bounds. Reclaim reverses the direction so the segment travels
from the target toward the builder. The exact presentation projection is the
ordinary world-to-screen line path; the target bounds, not an invented target
center, are the source data [03 §5.7; 05 "Factory production lifecycle"].

The event-facing payload justified by this contract is:

```text
tick, stable event identity
builder/source handle
target or construction-site handle
QueryNanoPiece piece index
source nano world position
target footprint-bound endpoints
build, assist, repair, reclaim, or capture mode
team/palette identity where the presentation layer already owns it
selector = 6
```

An event is emitted once per admitted segment call. A two-segment assist visit
emits two ordered events. A rejected work step emits none and must not call
`QueryNanoPiece` merely to draw a speculative spray.

### R-P0-06 §5 — Shared effect admission and lifetime boundary

Nano segments append to the shared variable-length effect/sequence strip
family. Producers append in event order. The effect allocator is guarded by
the recovered presentation allocation gate; an allocation failure admits no
segment record. The strip update pass removes an object before updating it,
then stably compacts survivors. The oldest record is evicted when the
pre-insert count exceeds `400`, giving the documented steady bound of at most
`401` records for a strip [01 §6.1; 03 §1 "Strip storage and lifecycle"; 03
§5.5].

The renderer later consumes the staged strips in its fixed compositor order.
Nano construction work is presentation-only after the authoritative work step:
rendering can be disabled without changing remaining fraction, health, stock,
occupancy, or RNG state [03 §5.5; 03 §5.7].

The direct census identifies a shared segment allocator and the fixed selector
value, but does not close every effect-family ownership detail. In particular,
the packet does not assign a strip number to nano segments solely from an
adjacent producer, and it does not assume that all segment families share the
same fade.

### R-P0-06 §6 — Strict admission and ordering

The authoritative ordering is:

```text
check operation/target/range/state
  -> admit two-resource construction or operation work
  -> update authoritative construction/economy state
  -> synchronously QueryNanoPiece
  -> transform piece to source world position
  -> append selector-6 segment if effect allocation admits it
  -> schedule the operation's next state visit
```

For construction work, resource admission is all-or-nothing for energy and
metal [05 "Two-resource admission"]. Rejected work leaves remaining fraction
and health unchanged and does not emit a nano segment. Accepted work updates
the remaining fraction and health in the established difference-of-truncations
order [05 "Health gain and fractional carry"] before the presentation segment
is requested.

The query/segment path must not be moved before admission, and an
effect-pool failure must not roll back already committed work. The visual
event is a consumer of an accepted authoritative transition, not its gate.

**Confidence.** Query mode/seed, accepted-work gating, ordinary construction
cadence, selector value 6, endpoint ownership, and cap/order behavior are
established. The allocator gate's complete failure side effects and the
assignment of nano records to one particular strip among the shared effect
families are of medium confidence and remain open.

```text
TODO(question): Recover the complete nano-segment record constructor and
allocator epilogue: exact strip ownership, allocation-failure side effects,
logical-to-palette color mapping, random presentation draws, and per-segment
fade/lifetime remain open. Do not invent these fields from the selector value
6 alone.
```

## Repair

**Established fact — repair helper terms.** The helper forms two truncated
terms from the target definition, build time, and a worker factor:

`heal term = trunc(1 + (maxDamage × worker - 1) / buildTime)`

`resource term = trunc(1 + (buildCostEnergy × worker - 1) / buildTime)`

The heal term is derived from maximum damage and the resource term from energy
build cost; the call order to the one-resource admission helper proves the
resource term is the energy amount. Each term is replaced with one when it is at
least one; a lower value survives. For positive inputs that make both terms at
least one, this yields one health point and one energy unit per accepted repair
call. It is not an unconditional minimum-one rule for arbitrary inputs.

**Established fact — repair admission is energy-only.** The normal handler
passes `floor(builder worker time / 30)` as the worker and passes the energy
resource term to the one-resource helper against the builder's energy subrecord.
That helper always adds the amount to energy requested and adds it to energy
accepted only when energy carry is non-positive; it does not touch the metal
subrecord. Only after admission succeeds does the helper emit the separately
computed heal term as kind-10 healing through the ordinary damage path.

**Established fact — repair nano cadence.** Repair emits one nano segment per
accepted repair visit, and an unfinished repair target retries the work state
one tick later, so the presentation follows accepted repair work rather than a
free-running timer [R-P0-06 §3].

A variant reverses the context and target arguments; its user-interface
identity and the behavior for zero or negative authored values remain open.

## Unit reclaim

Unit reclaim is a distinct state machine, not the feature-completion helper.
It reduces the target and returns resources over time, subject to builder
capability, range, ownership, and target restrictions.

**Established fact — capability gate.** The handler requires the builder's
reclaim capability bit, a target whose state is not the disallowed owner state,
and a target definition without the capture-immunity bit.

**Established fact — pulse size.** When the order is set up, a single integer
damage pulse is computed once and stored on the order node:

```
pulse = max(1, trunc(
    target.maxdamage
  * builder.workertime
  * floor((builder.kills + 5) / 5)
  * 15
  / (max(10, target.buildcostmetal) * 300)))
```

The target's authored metal cost is clamped up to 10 before the division, and
the result is clamped up to 1.

**Established fact — cadence.** The order node's second accumulator is a
cadence counter, not a resource fraction. Each successful work visit adds 2;
when it exceeds 14 the pulse is emitted and the counter resets to zero. A
reclaim therefore delivers one integer damage pulse every eight qualifying
work visits.

**Established fact — nano cadence.** Reclaim emits one nano segment per
qualifying work visit, and the operation schedules each next visit two ticks
later — a one-segment/two-tick presentation cadence, distinct from the
damage-pulse gate above [R-P0-06 §3].

**Established fact — application.** The pulse is applied through the ordinary
damage packet path, so the target's death, its corpse, and any resulting
feature are produced downstream by the normal death pipeline rather than by
the reclaim handler.

**Established fact — fatal refund is metal-only and placement is at death.**
No metal is paid per pulse. When a kind-5 reclaim pulse is lethal, the
cause-5 branch of the synchronous death finalizer computes the refund as

`refund = (1.0 - victim remaining fraction) × victim metal build cost`

and, in the ordinary player-state branch, adds that floating-point amount to
the killing attacker's metal production bucket. The branch contains no
corresponding energy credit. The two special player-mode branches apply the
already-observed 0.5 or 0.7 scaling family instead of the ordinary addition;
their user-facing mode names remain open. The payment is a death-side event
after ordinary cause-5 lethal handling and before death explosion, corpse
placement, and final teardown. The builder whose kind-5 packet is fatal
supplies the recipient.

**Established fact — multiple reclaimers.** Reclaim and repair events apply
synchronously during ascending unit-slot traversal, and the damage-packet
receiver rejects packets against already dead-latched targets, so the first
lethal event is authoritative. Repair bills admitted energy even when the
heal is discarded, and the victim's slot relation decides finalization order.

Whether every target class uses the same pulse basis remains open. Feature
reclaim, described next, must not be used as a substitute.

## Feature reclaim

Feature reclaim uses a progress counter associated with the feature and the
reclaimer's work contribution. The feature definition's damage value acts as
the completion threshold. When progress reaches the threshold, the completion
helper:

1. verifies that the feature is reclaimable and not protected by its
   indestructible state;
2. adds the full feature energy pool to the builder's energy-production bucket;
3. adds the full feature metal pool to the builder's metal-production bucket;
4. applies the special-player scaling path where required;
5. replaces the feature with its reclaimed successor, or removes it when no
   successor exists;
6. emits the deterministic multiplayer state command when required;
7. updates the affected footprint and derived world state.

The payout is a one-time completion event. The static completion helper does
not divide the feature pools into a per-tick drip.

## Capture

Capture is an order-driven work state with capability and target gates. On
success the reviewed handler invokes the central ownership-transfer path
rather than mutating only the visible team color.

**Established fact — capture timer.** The initial state computes a timer from
the target's authored costs, its current health, and its experience:

```
base         = clamp(trunc(150
                         + 0.015              * target.buildcostenergy
                         + 0.2142857142857    * target.buildcostmetal),
                     0, 1800)
healthScaled = floor((target.health + target.maxdamage) * base
                     / (2 * target.maxdamage))
killsFactor  = target.kills / 5
timer        = floor((killsFactor + 10) * healthScaled * 10 / 100)
```

There is no observed cap on the experience factor in this handler. A later
state advances the progress counter by 2 per visit until it reaches the timer;
that progression is the capture's own timing, not a resource admission.

**Established fact — no decay or cost while capturing.** The capture order
itself carries no per-tick resource debit and no decay of the progress
counter; it advances by two per visit on a fixed cadence until the timer is
reached, independently of the ledger.

**Established fact — capture visit cadence and nano.** Each qualifying visit
emits one nano segment, and the operation schedules the next visit two ticks
later — the same one-segment/two-tick pattern as reclaim [R-P0-06 §3].

**Established fact — ownership transfer.** The transfer path validates old and
new ownership and the unit limits, removes the old relation, allocates a
finished replacement where one is needed, and copies a narrow table: health,
remaining fraction, veteran experience, and visual piece and facing fields are
carried, while alliances, orders, queued work, group membership, and other
player-level permissions are not. The decremented experience factor for the
next capture is derived from the target's kill count divided by five using
integer truncation. Multiple captors operate independently; each has its own
node and timer, and the first to reach lethal progress wins the transfer.

**Unknown:** exact updates to player counts and limits beyond the table above,
and the full failure-message mapping, remain open. The resource cost is
established as none, and the multi-captor rule is first-wins as described.

## Resurrection

Resurrection is a distinct builder state. It resolves a wreck or feature back
to a unit definition, allocates a new unit, and advances through multiple
states before completion.

**Established fact — delay.** The third state computes

```
delay = trunc(float(resurrected.buildtime) * 0.3
              / float(trunc(builder.workertime / 30)))
```

The `0.3` constant belongs to this resurrection state alone. It is not a
general construction-speed, repair, reclaim, or capture multiplier.

**Established fact — no ledger cost, only delay and name handling.** Resurrection
carries no energy or metal debit or refund; its only cost is the integer delay
above, which is the sole use of the 0.3 multiplier in the executable. The
corpse feature name is truncated at the first underscore character to obtain
the unit name, then looked up in the definition catalog. One simulation-RNG
draw is consumed for placement jitter, and the feature is removed before the
new unit is made alive with remaining fraction zero and health one.

**Established fact — completion.** The integer delay is decremented once per
subsequent work visit. When it reaches zero, the next state allocates a unit
from the feature's resolved unit name, removes the feature as just described,
sets the new unit's remaining fraction to zero and its health to one, and the
state after that reports completion. Only then is the order classifier invoked,
and only to build a successor order node.

**Established fact — reverse and deconstruction.** The shared construction
helper has a distinct reverse arm that grows the remaining fraction instead of
shrinking it. That arm credits only metal, through a different admission path
that writes the builder's metal bucket directly, with no corresponding energy
credit. If the remaining fraction is clamped to one, the victim is killed with
cause-9, which is the no-corpse, no-explosion path (severity zero). Both arms
share the same health difference-of-truncations, but their resource paths are
distinct.

**Unknown:** exact owner selection and full corpse-chain eligibility beyond the
underscore truncation and slot-exhaustion response remain open, but the ledger
is established as not involved and the metal-only refund is established as
above.

## Feature catalog and placement

### Catalog construction

Feature definitions are parsed from the retail feature data. Named successor
links for death, burning, and reclaim are resolved in a later pass. Missing
links use sentinel values and do not allocate invented features.

Definitions can select either a 3D object or an animated image sequence.
Presentation choice does not change the authoritative footprint, blocking,
reclaim, or damage fields.

### Placement

Feature placement:

1. resolves the anchor terrain cell;
2. validates map bounds and conflicting occupancy;
3. writes the anchor feature identity and state;
4. stamps filler values over the remaining footprint cells;
5. creates any needed live animation or 3D state;
6. invalidates or rebuilds affected occupancy and coverage state.

There is no geothermal registration step: geothermal gating happens only
when a building placement is validated against covered-cell feature flags
(above).

Features can originate in the terrain file, the mission file, unit death,
burning, reclaim successor transitions, or other simulation effects. All
sources converge on the same placement service.

### Removal and successor replacement

Removal clears the whole stamped footprint and releases associated live state.
Successor replacement performs removal and placement as one logical transition
at the same world location. A missing successor means final removal.

The successor used depends on cause:

- ordinary destruction uses the dead successor;
- burning uses the burnt successor;
- reclaim completion uses the reclaimed successor.

Cause-specific priority when multiple transitions occur during the same tick is
not fully closed.

### Definition flags, teardown, and the Great Divide partition

**Established — flag defaults.** A feature's economy yield is independent from
its extraction role. Orthogonal booleans govern reclaim (see "Feature
reclaim"):

| Flag | TDF default | Retail meaning |
|---|---|---|
| `reclaimable` | `0` | command-gated; only when `1` can a builder enter the reclaim state |
| `autoreclaimable` | `1` | reuse-suppression; protects the cell from being implicitly cleared by a colliding stamp unless honored explicitly |
| `indestructible` | `0` | weapon-damage and teardown guard; when `1` damage is ignored and teardown returns without clearing |
| `blocking` | `0` | pathway/yard map predicate; when `1` the footprint is treated as blocked for generic placement |
| `geothermal` | `0` | footprint-class flag; a `YardMap 'G'` requirement is satisfied only by a covered cell holding this flag (yard-map bit 7, `[04 §6.4]`) |

**Established — teardown frees the footprint.** Teardown clears the anchor to
`0xFFFF` and each fringe to `0xFFFF` with the signed offset bytes poisoned,
then notifies derived occupancy — the cleared cells become free. If a
successor was stamped (typically a `1×1` smudge for trees/shrubs) the newly
stamped residue may itself be non-blocking and non-reclaimable, so a reclaimed
tree line opens a corridor. The same transition applies when a wreck sinks or
burns to completion.

**Established — the Great Divide partition** (derived from the catalog, not
from the TNT table alone):

- **Reclaimable energy (blocking, flamable, damagable):** `Tree1..Tree6` (250
  energy), `Shrub1..3` (20 energy), `Rock1a` (metal 100). Footprint blocks
  pathing and building. Reclaim completion pays the full `metal` + `energy`
  pools as a one-time credit and atomically swaps to the `featurereclamate`
  successor (smudges) or clears. While burning they reject reclaim.
- **Reclaimable rock chain with successors:** `Rock1a → Rock1b → rockgone`
  (successive `featuredead` links) and generic rock variants. Damage
  accumulation against `damage` thresholds traverses the same chain as
  reclaim.
- **Non-reclaimable, non-blocking metal deposits:** `RockMetal*` variants,
  `3×3`, metal 86–223 but `reclaimable=0`, `indestructible=1`, `height=4`.
  They cannot be reclaimed or destroyed and never change. They are the source
  of **extractor economy** ("Terrain metal extraction") rather than reclaim
  economy.
- **Geothermal vents:** `Geothermal`, `1×1`, `animating=1`, `geothermal=1`,
  `indestructible=1`. Not reclaimable. Their only interaction is footprint
  validation for buildings that carry `YardMap 'G'`.
- **Non-reclaimable remnants:** generated smudges (`Smudge01..Smudge04`),
  `Tree1Dead`/`Tree2Dead`, and the indestructible rock debris all carry
  `reclaimable=0` and serve as inert residues.

Geothermal validation is read-only: placing a building that covers a vent does
not clear the vent feature, so destroying the building leaves the stamp intact
and another plant can reuse the same cell without a restore step. Feature
metal at the definition is reclaim reward only and does not drive extraction
("Terrain metal extraction").

## Wreckage and corpse production

Unit death asks the unit script for a severity result and uses the unit's corpse
definition plus feature successor chain to create the resulting wreckage.
Death, damage, and corpse severity are defined in the combat specification;
this category owns the created feature's placement, blocking, reclaim pools,
and successor lifetime.

The corpse feature is stamped at the victim's anchor cell subject to terrain
and full-footprint rules. If death-path placement is out of bounds, blocked, or
cannot allocate its required state, that path fails silently. It does not
shift the wreck, retry, or substitute a later featuredead successor.

## Feature burning

**Established fact — ignition predicate.** A weapon impact on a feature cell
first checks a global settings bit; if it is clear, nothing burns. It then
rejects an empty cell and an indestructible definition. In a multiplayer game
a client that lacks the authority bit sends an ignition request instead of
acting locally.

Ignition is a candidate when the feature definition's flammable flag is set
**and** the weapon's `firestarter` value is nonzero. **There is no probability
roll against `firestarter` anywhere in the executable** — the only reader is
the nonzero test. Any nonzero authored percentage ignites deterministically,
so the shipped values of 100 and 70 behave identically.

If the cell is an ignition candidate and has no instance attached, the feature
ignites and the impact deals no blast damage. Otherwise the impact accumulates
damage: for a cell with no attached instance, the weapon damage is added to the
cell's accumulated damage, and the feature dies when the total reaches the
definition's hit points; for a cell whose instance is attached and whose
definition is object-based, the damage accumulates on the instance instead.

**Established fact — ignite.** Ignition requires the definition to name a burn
animation sequence; without one nothing happens. It also refuses when the cell
already has an instance attached. It then takes a slot from the burning-feature
free list; **if no slot is free the ignition is a silent complete no-op** with
no broadcast. On success it binds the cell to the slot, starts the burn
animation and, when named, the burn shadow animation, marks the instance
burning, records the tile, plays the burn sound at the tile's world position,
and draws the burn countdown as

```
countdown = simulationRandom(sparktime / 2) + (sparktime / 2)
```

with a **single** draw from the simulation stream. The shipped spark time of 5
therefore yields a countdown of 2 or 3. A remotely-triggered ignition sets a
suppression flag that prevents this instance from spreading.

**Established fact — burning tick.** The feature phase computes one smoke flag
per call, true when the global tick is a multiple of three, and shares it
across every burning instance in the pass. For each burning instance:

1. if the smoke flag is set, emit a smoke particle at the footprint centre,
   jittered by the **presentation** random stream, not the simulation stream;
2. advance the burn animation, and the shadow animation when present;
3. if the burn animation has finished, clear the cell — which releases the
   instance and its animations back to the free list — and spawn the
   `featureburnt` successor when one is linked;
4. otherwise, if the countdown is nonzero and the instance is not
   remote-suppressed, decrement it, and when it reaches zero fire the burn
   event exactly once.

**Established fact — burn event.** The event runs three passes in order:

1. **Neighbourhood spread, exactly 48 candidates.** The 7 by 7 window around
   the origin is scanned row-major ascending, skipping the origin tile before
   any legality test, so at most 48 evaluations occur. A candidate is skipped
   when it is off-map, empty, already has an instance attached, or its own
   definition is not flammable. Only after every one of those checks does it
   draw `simulationRandom(100)` and ignite when the draw is below the
   **candidate's** own `spreadchance` — never the burning feature's.
2. **Wind embers, exactly five steps.** A probe walks in 16.16 tile space from
   the origin, adding twice each wind component per step, and tests the tile at
   each step with the same legality chain and the same draw rule. Zero wind
   collapses all five probes onto the origin tile, where they are skipped, so
   no draws happen at all.
3. **Burn weapon.** After both spread passes and regardless of their results,
   if the definition names a `burnweapon`, an ordinary weapon request is fired
   at the footprint centre, at the sampled terrain height.

**Established fact — animation-driven completion.** A burn ends **only** when
the burn animation pointer is cleared. Advancing past the last frame of a
non-looping sequence clears that pointer, and the same feature visit then
clears the burning cell and stamps the `featureburnt` successor when one is
linked. The countdown plays no part in ending the burn: after firing its
single spread and burn-weapon event it stays at zero and is inert. A looping
sequence would therefore burn forever.

**Established fact — determinism.** The spread draws come from the simulation
stream and are made only after all cheap rejections, so the number of draws
depends on how many candidates survive the checks. The smoke jitter comes from
the presentation stream and must never be allowed to perturb the simulation
stream.

**Established fact — burning blocks reclaim and is immune to further blast.**
The feature-reclaim helper rejects a cell whose instance is attached and whose
definition is filename-based, which covers every shipped ignitable feature, so
a burning tree cannot be reclaimed until the burn completes. Further blast
damage has no applicable accumulation branch for that same attached
filename-based burning instance and is therefore ignored. The feature phase
runs every simulation tick; only smoke emission is gated on `globalTick % 3
== 0`. Animation advance and countdown decrement run every tick, and the
countdown event fires only once per burn.

**Established fact — shipped burn lifetimes are finite.** The loader forces the
runtime loop byte to zero for every resolved burn, burn-shadow, death, and
reclaim sequence, so the source GAF loop byte does not control looping after
the standard load path. All 79 shipped `seqnameburn` features resolve and their
burn GAF entries have finite lifetimes between 46 and 282 feature-phase visits;
distinct lifetimes observed are 46, 56, 58, 70, 84, 92, 114, 120, 122, 126, 141,
144, 159, 186, 192, 196, 200, 204, 208, 258, 264, 276, and 282 visits. Only
`Shrub1`, `Shrub2`, and `Shrub3` lack a `featureburnt` successor; the other 76
have one. Malformed or missing burn sequences and non-filename object/fire
combinations remain separate loader edges.

## Feature sinking and water interaction

Sinking **is corpse placement below the waterline, plus a constant-rate
descent — not a gravity-driven fall**. The state machine is closed:

* **Start predicate.** Every unit death whose `Corpse` chain resolves stamps a
  corpse feature at the dying unit's position; there is no separate sink
  decision. The chain starts at the definition's corpse index and follows the
  successor link depth-minus-one times; a sentinel aborts silently with no
  corpse. Medium classification uses the interpolated terrain height under the
  victim against the sea-level byte — never the unit's own elevation. Both
  paths stamp the corpse at the unit's current position triple.
* **Submerged start.** When the terrain is at or below sea level and the dying
  definition lacks the isfeature flag, the fresh instance's vertical velocity
  latches to the fixed constant −11468 fixed-point (−0.175 world units per
  tick) with zero horizontal velocity; isfeature corpses get no velocity and
  never descend. The land path instead emits a particle-strip effect when the
  death cause permits notification; underwater stamps are silent (no splash,
  no sound on the wreck path).
* **Per-tick descent.** Each feature-phase tick integrates position by the
  velocity triple; while strictly above the sampled floor and strictly below
  the water plane the −11468 vertical latch re-applies every tick, so descent
  is constant at 5.25 world units per second and underwater gravity never
  applies. Above the surface gravity accelerates the fall until either the
  floor clamp or water entry resets it to the constant rate.
* **Settling.** The bottom test compares against the average of the two derived
  floor bytes of whatever cell the wreck currently occupies; at or below it the
  Y hard-snaps to that average (fraction discarded), all velocities zero, and
  the next tick's settled test moves the instance to a dormant list.
* **Occupancy and reclaim.** Sinking releases nothing: footprint cells stay
  stamped from placement until teardown, and the wreck remains fully
  reclaimable during and after descent. A destroyed or reclaimed sinking wreck
  hands its submerged position — and typically its stale downward velocity —
  to its successor, which continues descending until its own floor clamp.

Water/lava splash art belongs to debris records and projectile water entry, not
to sinking wrecks. There is no depth-triggered removal: a settled sunken wreck
stays forever unless damaged, reclaimed, or replaced by a successor.

## Saving economy, construction, and features

Save writers cover player stocks/capacities/statistics, unit slots, remaining
construction state, order records, script/COB records, ownership, and live
features. Exact script-thread locals, operand stacks, waits, signals, and
callbacks are not closed by this category.

**Established fact — per-player account persistence.** Each non-empty player
section stores live energy and metal (as floats), the cumulative
production/consumption totals and waste (doubles), the storage mirrors, the
storage-bonus flag, kill/loss counters, alliance bytes, the controller state,
and three absolute tick deadlines: the **settlement deadline** under the key
`UpdateTime` plus sibling keys `WinLoseTime` and `DisplayTimer`. Deadlines
are restored verbatim, not re-seeded to the loaded tick, so each player
resumes its saved settlement phase after a load. The global tick counter is
persisted inside the game-time account blob that gates scalar restoration;
scalar restoration is skipped entirely when that blob read fails or is short.

Features are serialized in three groups: normal, animated, and 3D. Each group
uses its own fixed record shape. A feature-type-name table maps the save's local
feature identifiers back to the global feature catalog during loading. Loading
reconstructs features through the normal placement service rather than copying
unvalidated pointers.

Construction queue nodes are serialized with the owning unit and recreated by
name and payload during unit reconstruction. Slot identity is preserved where
possible because other saved subsystems refer to unit slots.

**Established fact — construction and stockpile persistence.** Each unit's three
slot-level completed-ammunition bytes are stored inside the per-unit persistent
record and are restored for all three slots. Each order node is saved as a
packed record containing the operation and state bytes, wait mask and absolute
wake tick, weapon slot index, signed remaining count, in-progress progress
value, and catalog and runtime flags including the secondary-list selector.
The loader restores those values, enumerates saved nodes in save order, and
appends each to the primary or secondary chain according to the restored
selector flag, preserving traversal order. The node creation-tick field is
reinitialized to the current tick rather than loaded, but the stockpile state
machine does not use it. Queue cancellation after load acts on the restored
order and progress.

**Established fact — economy carry persistence.** Unit economy subrecords are
persisted as two consecutive raw blocks covering produced, requested, accepted,
carry, and both archives for both resources; the owner pointer is excluded and
is reconstructed on load. Pending unit carry and admitted work therefore survive
save and load exactly. The player-level mirror bucket is not persisted by the
player save path and is reinitialized; a save made after sharing but before
the next settlement can therefore lose the pending mirror-side transfer value.

The sessions/save specification defines file framing and subsystem order. This
category requires its loader to restore logical fields and rebuild all derived
links, counts, occupancy, and registrations.

## Required implementation invariants

A clean-room implementation conforming to the established retail behavior must
preserve these invariants:

- Player stocks and economy buckets retain single-precision fractions.
- Energy and metal shortages are settled independently.
- Old carry is serviced before newly accepted work.
- Two-resource construction admission is all-or-nothing at admission time.
- Direct two-resource payments debit both resources or neither.
- Capacity is rebuilt from eligible completed units each pass.
- Waste accumulates fractional overflow beyond rebuilt capacity.
- Each player's settlement runs on a per-player 30-tick deadline (persisted
  as `UpdateTime`), gated inside the deadline block; authored economy values
  are per-settlement-pass amounts.
- Ordinary per-pass economy values are not automatically divided by thirty.
- Cloak debit is integerized, immediate, and ordered by unit slot.
- An unfinished unit stores remaining fraction from one down to zero.
- Construction health uses a difference of truncated cumulative health values.
- The normal builder quantum is the floor of worker time divided by thirty.
- Factory queue insertion coalesces only with a matching tail request.
- Unit slots are stable indices without generations, with slot zero reserved.
- Terrain extraction samples `cell metal + 1` across the footprint at placement.
- Feature reclaim pays the full feature pools once at completion.
- Feature successors are cause-specific and data-driven.
- Feature footprints are stamped and cleared as complete regions.
- Geothermal placement gating is yard-map control-byte bit 7 validated
  against covered-cell feature geothermal flags at building placement; no
  registry exists.
- Factory products occupy the primary order list; the secondary list carries
  only stockpile-weapon builds and self-destructs.
- Automatic resource sharing runs every sixty authoritative ticks.
- Sensor-sharing commands run every 450 authoritative ticks.
- Repair admission is energy-only: the energy resource term is always added to
  energy requested and is added to energy accepted only when energy carry is
  non-positive, with no metal ledger effect.
- Nano presentation has no free-running timer: build and repair emit one
  segment per accepted work visit and retry one tick later, build assist emits
  two segments per visit while its counter sits above the 15 gate, and
  reclaim/capture emit one segment per visit on a two-tick cadence; rejected
  work emits no query and no segment [R-P0-06].
- Unit reclaim's fatal payment is metal-only: `(1 - remaining fraction) ×
  metal build cost` credited to the killer's metal production bucket at death
  finalization, with no per-pulse payment and no energy credit.
- Stockpile production advances progress by five capped at the weapon's
  reload-time, computes per-visit cost as a difference of truncated cumulative
  proportional costs through the two-resource admission, retries after ten ticks
  on rejection and five ticks on accepted-but-incomplete work, completes one
  round by incrementing the slot byte and decrementing the queue signed count,
  and blocks a new round with a 300-tick wait when the slot byte exceeds 199.
- Unit economy carry and stockpile queue count, progress, and completed
  ammunition survive save and load; the player mirror bucket does not.
- Feature fire neighborhood is 48 non-origin candidates in a 7 by 7 window;
  only smoke emission is gated on `tick % 3`; burn animation and countdown run
  every tick; burn ends only when the burn animation pointer clears; shipped
  burns are finite and immune to reclaim and further blast while burning.

## Missing and unknown

The following work remains necessary before this category is a complete retail
contract:

- Reconcile every unrecovered early construction and order-handler boundary.
- Close the exact operation-byte table that dispatches build, repair, unit
  reclaim, feature reclaim, capture, and resurrection.
- Establish the precise same-tick order between each work handler, economy
  admission, settlement, operational callback, occupancy change, and
  presentation event; the nano-event admission/ordering contract is closed in
  [R-P0-06 §6].
- Confirm the semantic names of the game-ended flag bits and of the two
  mission-end predicates behind the confirmation delay. The gate's effect on
  settlement — a freeze at end of game, not a cadence — is established.
- Find the consumers of the per-unit archived economy snapshots. The mirror
  bucket's writer set, its read side, and the absence of any factory-draw or
  external request/accept writer are established.
- Name the user-facing identities of the special player modes behind the
  0.5/0.7 scaling selector, and close the full enabling predicate for the
  cloak debit under those modes.
- Prove the metal-maker stall rule when energy is insufficient.
- Prove whether negative ordinary economy fields use refund, demand, or
  undefined behavior in every branch.
- Specify all floating-point evaluation points needed for bit-exact economy
  settlement, including exceptional values, overflow, signed zero, and NaN.
- Close threshold initialization, source share-buffer refill, the remaining
  status/alliance predicate names, destination over-cap behavior, state-2
  credit discounts, and resource-share packet application.
- Reconcile the numeric unit-pool capacity and any mission overrides; the
  per-definition limit check (allocator-only, owner slice, -1 sentinel,
  no queue reservation, 300-tick retry, capture validation) is established.
- Locate the writer or initializer of the per-definition limit field (absent
  from the reviewed corpus; -1 implies unlimited by default) and any consumer
  of the parsed-but-unread `norestrict` capability bit.
- Identify the upstream UI/network producers of the factory production
  interrupts: cancel-current (mask 2) versus "Construction stopped" (mask 8).
  Handler-side semantics for both are established.
- Determine whether order nodes queued on a factory are reclaimed when the
  factory dies outside the pump/cancel/purge paths (no death-time node
  reclamation site was found).
- Close the settlement status-pair semantics: no writer of the pair was
  found in exported code, so its predicate is preserved literally; also name
  the three controller states (settlement excludes the third) and find
  consumers of the `WinLoseTime`/`DisplayTimer` sibling deadlines beyond
  their save keys.
- Determine whether the pre-gameplay setup pass can perform a real
  settlement (depends on unit placement and slot states at that instant),
  and whether the deadline catch-up burst (one pass per tick until caught up)
  is reachable without a hand-edited save.
- Close stockpile all-slot mapping validation for every weapon combination,
  byte-wrap for malformed preexisting values, cancellation interaction with
  admitted carry, and presentation beyond the selected-unit refresh;
  stockpile progress step, cost timing, retry deadlines, completion
  mutations, and the 200-round start block are established.
- Establish the complete construction-completion side-effect order beyond
  the closed completion transition: occupancy, yard state, sensors, and
  network event ordering relative to the fraction/health/callback steps.
- Resolve the user-interface identity of the reversed-argument repair variant
  and malformed zero or negative input behavior; the ordinary repair energy term
  derived from energy build cost and its energy-only admission gated on energy
  carry are established.
- Close the reversed/deconstruction refund branch details: the negative
  (work-increasing) arm of the shared construction work helper and the
  production handler's interrupt arms — refund formula, cadence,
  bucket/player destination, callback edges, and interaction with the
  cause-9 killed-severity bypass.
- Close capture costs, resistance if any, multi-captor behavior, and callback
  order; the capture timer equation, the ownership-transfer steps, and identical
  limit validation at transfer are established.
- Close resurrection resource costs, owner selection, corpse eligibility,
  slot-exhaustion response, and callbacks. The delay equation, the completion
  sequence, and the restored health are established.
- Vent persistence beneath a completed geothermal plant, multi-vent
   at-least-one satisfaction, and persistence after destruction are established
   as read-only validator behavior with no registry and no restore step; wreck
   transitions beyond ordinary feature placement remain narrow `TODO(question)`.
- Terrain metal is sampled once at placement as `extractsmetal × Σ(byte+1)` and
   never resampled; any varying per-cell metal source file beyond the uniform
   `SurfaceMetal` byte remains `TODO(question)` — the retail corpus shows only
   the uniform byte and no varying raster.
- Feature allocation limits are established: catalog `0x100` bytes per entry,
   animation pool `0x800` slots of `0x30` bytes, plot cell `0xD` bytes, and
   map-bounds checks — exhaustion is a silent failure with no retry;
   narrow edge cases for simultaneous exhaustion remain `TODO(question)`.
- Reconcile feature-definition table size, serialized copy size, live-record
   size, and all unknown fields without conflating the structures.
- Close feature damage, indestructibility, reclaimability, and successor
   precedence when multiple causes occur in one tick.
- For shipped filename-based burns the looping rule (forced non-looping at
   load, finite 46-282 visits), neighbourhood size (48), smoke-only gating,
   animation-driven extinction, one-shot countdown after `sparktime`, reclaim
   rejection, and blast immunity are established; malformed or missing burn
   animations and non-filename object/fire combinations remain
   `TODO(question)`.
- Presentation clipping of sunken wrecks and slot-velocity inheritance across
  teardown-reuse in practice; the sinking state machine itself is closed.
- Close feature save/load record fields for all normal, animated, and 3D
  variants, including malformed counts, unknown type names, and allocation
  failures.
- Identify every statistic/UI field derived from the economy and distinguish
  authoritative totals from presentation-only cached values.
- Close the nano-segment record constructor and allocator epilogue — exact
  effect-strip ownership, allocation-failure side effects,
  logical-to-palette color mapping, random presentation draws, and
  per-segment fade/lifetime — `TODO(question)` in [R-P0-06 §5]. Emission
  cadences, accepted-work gating, selector 6, endpoint ownership, and strip
  cap/order are established.
