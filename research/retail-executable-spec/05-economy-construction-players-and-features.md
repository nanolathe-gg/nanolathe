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

**Established fact — the width of every economy field in the player slot.**
Per resource the slot holds, all as **single precision**: live stock;
"produced this pass"; "requested this pass"; storage capacity; and a storage
bonus. Per resource it also holds three **double-precision** running totals:
cumulative produced, cumulative requested, and cumulative waste. One byte
carries the storage-bonus enable flag. Three absolute tick deadlines sit side
by side — the settlement deadline (`UpdateTime`), the win/lose deadline
(`WinLoseTime`), and the HUD refresh deadline (`DisplayTimer`). The only
single-to-double conversions in the whole ledger are the six accumulations
into those running totals ([R-ECO-01 §6]). There is no integer stock, no
integer capacity, and no integer per-pass counter anywhere in the settlement
path; an implementation that stores stock or capacity as an integer diverges
on the first pass.

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

**Established fact — one class, six slots per resource, twice.** The
subrecord is a single class with a constructor that zeroes thirteen words and
stores the owning player record. Its layout is, in order: energy production,
energy requested, energy accepted, energy carry, archived energy production,
archived energy requested; then the same six for metal; then the owner
pointer. All twelve accumulators are **single precision**. A unit embeds one
instance; the player-level mirror bucket is a separately allocated instance of
the same class, and every rule below applies to both. There are exactly two
archived slots per resource — production and requested — and the settlement is
their only writer ([R-ECO-01 §5]).

**Established fact — what feeds each accumulator.**

| Accumulator | Contributors | Gates |
|---|---|---|
| energy production | authored passive `energymake`; the current wind scalar times `windgenerator`; the map tidal strength times `tidalgenerator`; the refund branch for a negative authored `energyuse` | passive make and storage require a zero remaining-construction fraction; wind and tidal require the definition's `bmcode` to be zero **and** the unit's activated bit, and are reached only when `extractsmetal` and `makesmetal` are both absent ([R-ECO-01 §2]); every positive contribution is scaled by the difficulty discount when the owner is a computer player ([R-ECO-01 §3]) |
| metal production | the metal value sampled at placement when `extractsmetal` is positive; the **numeric value** of the authored `makesmetal` byte when it is non-zero; authored passive `metalmake` | extraction and maker output require `bmcode` zero, the activated bit, and the unit's energy carry to be non-positive at dispatch; passive make requires a zero remaining fraction; every positive contribution is scaled by the difficulty discount when the owner is a computer player |
| energy requested | positive authored `energyuse`; the cloak debit; every build admission; repair-family one-resource admission | none — always recorded |
| energy accepted | the same positive `energyuse` when energy carry is non-positive; admitted build demands when both carries are non-positive; repair-family admission when energy carry is non-positive | energy carry non-positive (both carries for the two-resource build path) |
| metal requested | every build admission | none — always recorded |
| metal accepted | admitted build demands | both carries non-positive |
| energy and metal carry | written only by settlement's apply-back step | — |

The negative-`energyuse` refund adds the negated authored value to production
in the ordinary case; for a computer-owned unit it instead credits only
one half or seven tenths of it. The engine reaches the scaled credit by
multiplying the negated amount by a negative half or seven-tenths constant
and subtracting the product, so production still grows — by half or seven
tenths of the negated value. An earlier revision of this document read the
special modes as *turning the refund into a subtraction*; the byte-level
arithmetic is a reduced positive credit.

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

#### R-ECO-01 §2 — The per-unit gather, exactly [R-ECO-01] (2026-08-29)

**Established.** A unit is visited only when its status word carries the alive
bit. The visit then takes **one of two mutually exclusive branches**, chosen by
a status bit that is written once at spawn as *"the definition's `bmcode` byte
is zero"* — that is, buildings take the first branch and mobile units the
second. This replaces the previous text's vague "wind and tidal require the
unit's operational bit and a secondary state bit": the secondary bit is the
spawned-in `bmcode`-is-zero bit, and it is never rewritten during play.

*Branch A — `bmcode` zero (buildings).* Runs only when the unit's **activated**
bit is set; otherwise the visit falls straight through to the idle block below.
It performs, in this order:

1. **Upkeep or refund.** Read authored `energyuse`.
   - `energyuse >= 0` (the comparison is "less than zero", so `+0.0` and
     `-0.0` both take this arm): `energy requested += energyuse`; then, **only
     if `energy carry <= 0`**, `energy accepted += energyuse`. Remember that
     carry test as `admitted` for step 2.
   - `energyuse < 0`: negate it and add the negated value to **energy
     production**, through the difficulty discount of [R-ECO-01 §3].
     `admitted` is false on this arm.
2. **Exactly one generator**, selected by a strict if/else-if chain — a unit
   that authors several only ever contributes the first that matches:
   - `extractsmetal > 0` → and `admitted` → add the unit's placement-sampled
     extraction amount to **metal production**;
   - else `makesmetal` byte non-zero → and `admitted` → convert that **byte's
     numeric value** to floating point and add it to **metal production** (the
     previous "a literal one" reading was wrong: the value is whatever the FBI
     authored, converted from the stored byte);
   - else `windgenerator > 0` → add `currentWindScalar × windgenerator` to
     **energy production**;
   - else `tidalgenerator > 0` → add `mapTidalStrength × tidalgenerator` to
     **energy production**.
   Wind and tidal are **not** gated on `admitted`; extraction and the metal
   maker are. All four products are formed as one multiply and one add with no
   intermediate narrowing ([R-ECO-01 §1]).

*Branch B — `bmcode` non-zero (mobile units).* Runs when the activated bit is
set **or** the unit's movement-mode bits are non-zero, and performs step 1
only. A mobile unit therefore never contributes extraction, maker, wind or
tidal output from the settlement, whatever it authors.

*Then, for every alive unit regardless of branch:* the idle block runs when the
remaining construction fraction **compares equal to zero** (a floating-point
equality, so `-0.0` also passes and a NaN fraction does not). It adds
`energymake` to energy production and `metalmake` to metal production, each
through the difficulty discount, and then accumulates this unit's
`metalstorage` into the player's metal capacity and its `energystorage` into
the player's energy capacity — **metal first**, both as ordinary
single-precision adds ([R-ECO-01 §4]).

*Then* the cloak debit ([R-ECO-01 §9]), and finally the eight accumulations
into the pass totals. Each accumulation is `total = float32(unitField + total)`,
so **every running total is re-rounded to single precision after every unit**;
the totals are not kept at register precision across the slice.

#### R-ECO-01 §3 — The discount is difficulty, and the special state is the computer player [R-ECO-01] (2026-08-29)

**Established, and a correction.** The "state-2 selector discount" this
document has carried since the corrected-economy pass is now named on both
halves:

* the player **control byte value 2** is the computer-controlled player
  ([R-AI-01 §12] establishes the same byte from the AI profile loader, which
  runs its per-definition passes for exactly the slots whose control byte
  is 2). The previous text called this "the special second state … the
  computer-policy state per the AI-manager gate inference"; it is no longer an
  inference.
* the **global mode selector** is the **difficulty word**: it is loaded from
  the `Difficulty` registry value, masked to sixteen bits, and also written
  directly with the literals 0, 1 and 2 by three developer entry points.
  [R-AI-01 §12] establishes the same word's vocabulary as `0` easy, `1`
  medium, `2` hard.

So the rule reads: **for every unit owned by a computer player, every positive
production contribution is scaled by 0.5 on easy, 0.7 on medium, and not at
all on hard.** The gate is evaluated per contribution as "the owner's player
record exists **and** its control byte equals 2"; a slot whose record word is
zero takes the undiscounted path.

The arithmetic must be reproduced literally, because the factored form rounds
differently:

```
contribution      : float32 (the authored value, or the product just formed)
K                 : the double constant -0.5 (easy) or -0.7 (medium)
production        : float32
production := float32( production - (contribution * K) )
```

`contribution * K` is a **float × double** multiply evaluated at the x87
working precision of [R-ECO-01 §1]; the subtraction is done at that same
precision and only the store narrows to single. Writing this as
`production + 0.5*contribution`, or rounding the product to single first, is
not bit-identical. The two constants are the only floating-point literals the
whole ledger contains, and their exact bit patterns are the IEEE doubles for
−0.5 (`BFE0000000000000`) and −0.7 (`BFE6666666666666`) — note that −0.7 is
not exactly representable, so the medium-difficulty factor is the nearest
double, not seven tenths.

Sites, exhaustively: passive `energymake`; passive `metalmake`; the extraction
output; the maker output; the wind output; the tidal output; the
negative-`energyuse` refund; the reverse-construction metal refund; and the
direct production credit written at spawn for a unit created outside the
ledger. Storage contributions, the cloak debit, and the two-stage settlement
itself are **not** discounted.

**Correction against document 08.** [R-AI-01 §12] closes with "there is no
production, build-rate, cost, or damage multiplier anywhere in the computer
player's path". The settlement disproves the production half: the eight sites
above are a production multiplier on the computer player's whole economy, not
only on transfers into it. The transfer finding itself stands unchanged — the
same difficulty ladder, the source debited in full — and doc 05's sharing
sections keep it; what must be retracted is the "no production multiplier"
sentence. Doc 08 owns that retraction.

**Established fact — player-side storage is floating point.** Live stock,
the per-pass produced and requested snapshots, and the capacities are stored
and copied as 32-bit floats, not integers. Closing stock is copied bit-exactly
from the settled residual and staged into the next pass by a plain float
addition, so **fractional production survives across ticks exactly**, subject
only to ordinary single-precision rounding. Cumulative totals and cumulative
waste are doubles. Capacity is recomputed from zero every pass by accumulating
each idle unit's authored storage, plus a bonus term when a player flag is set.
Reserve thresholds are written once at battle setup from capacity and are read
by the automatic-sharing dispatcher; a resource-bar colouring read of the
threshold fields is not visible in the reviewed corpus and remains unverified.

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
ledger contains are the two difficulty scale factors of −0.7 and −0.5
([R-ECO-01 §3]), the literal `0.0` every sign test compares against, and the
literal `1.0` both stage ratios are set to when a stage is fully funded. An
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
   field **or** a zero word at its neighbor — preserved literally; the same
   read-only predicate recurs at three other player walks and no writer was
   found in the bounded scan, so it is carried verbatim); the state byte is
   narrowed to one of the two settling states (the third traverses but never
   settles); the game-ended flag bit is clear; and the end-of-game countdown
   is negative.
5. Still inside the deadline block, for the local/human reference slot only:
   the win/lose evaluation that arms and decrements the end-of-game countdown;
   plus interface/view helpers for the local-view slot on the same cadence.

The deadline catch-up edge: because the advance is a single conditional add
rather than a loop, a slot whose deadline fell more than 30 ticks behind the
global tick would settle once per tick on consecutive ticks until it caught
up. No natural runtime path that desynchronizes a slot was identified
(documentary inference, not an established path: the structure admits the
burst; no runtime trigger was found), so this is structural behavior, not
ordinary-play cadence.

**Established fact — seeding, setup pass, and persistence.** Battle
initialization seeds every active player's settlement deadline (and two
sibling deadline fields) to the current global tick, so all players share the
same initial phase; mission setup then runs one full pass of the player phase
before starting resources are granted, and spawn credits are written directly
to live stocks outside the ledger — starting resources never flow through the
settlement allocator. There is no mid-game re-seeding path (bounded negative:
the reset helper's caller chain has exactly one site). Each player's
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
below zero and latches the game-ended flag bits — bit 0x04 always, plus
0x40 and/or 0x10/0x20 depending on the victory/defeat/watch branch. The
network path latches bit 0x04 directly on a game-over message. Nothing ever
clears the game-ended bit once set, so the economy stays frozen for the rest
of the session. The freeze pair's pacing still yields the observed ≈5-step
confirmation delay before the latch. The semantic names of the individual
bits and of the two mission-end predicates remain open; the bit patterns and
the never-cleared property are established.

### R-ECO-01 §1 — Deadline strictness and the floating-point environment [R-ECO-01] (2026-08-29)

**Established — the settlement deadline compare.** Per slot, ascending, the
compare is *unsigned* and reads:

```
if (playerUpdateTime <= globalTick) { playerUpdateTime += 30;  ... }
else                                { skip the rest of this slot }
```

The advance is `+30` — a single conditional add, never a loop, never a
re-seed from the tick — and it happens before the settlement gate chain. Due
is **inclusive**: a deadline equal to the current tick settles this tick. The
gate chain that follows is, in evaluation order and all required:

1. the player record's existence word is non-zero;
2. the control byte is 1, 2 or 3;
3. the observer byte is not the observer value;
4. the status pair holds — *the halfword at the first field is non-zero* **or**
   *the word at the second field is zero* (still carried literally; no writer
   of either field was found);
5. the control byte is **narrowed to 1 or 2** — control byte 3 traverses the
   deadline block, advances its deadline, and never settles;
6. the game-ended flag word's `0x04` bit is clear;
7. the end-of-game countdown halfword is **signed less than zero**.

**Established — the HUD deadline is one tick stricter.** The sibling
`DisplayTimer` field is advanced by the same `+30` but by a **strict**
compare, `if (playerDisplayTimer < globalTick)`. Its consumer is the resource
bar ([R-ECO-01 §6]). `UpdateTime` uses `<=`, `DisplayTimer` uses `<`; the
difference is real and is not a transcription slip. This closes half of the
tail's "consumers of the `WinLoseTime` / `DisplayTimer` sibling deadlines
beyond their save keys": `DisplayTimer` has exactly one consumer, the resource
bar's rate latch. `WinLoseTime` remains open.

**Established — the x87 environment, and what "bit-exact" therefore means.**
The runtime calls `fninit` at startup and immediately sets the precision
control to **53 bits** (the CRT's default-precision helper, `_PC_53`).
Nothing in the economy path changes it back; the only other control-word
write in the whole ledger is the integer-conversion helper, which flips the
*rounding* control to truncate-toward-zero for one instruction and restores
the saved word ([R-ECO-01 §9]). Consequently:

* every intermediate x87 result in the settlement — products, sums,
  quotients, differences — is rounded to a **53-bit significand** with the
  80-bit exponent range, not to 24 bits and not to 64 bits;
* narrowing to single precision happens **only where a value is stored to a
  single-precision field**, and every such store site is named in
  [R-ECO-01 §2], [R-ECO-01 §5] and [R-ECO-01 §6];
* all floating-point exceptions stay masked for the whole run, so a division
  by zero yields a signed infinity rather than trapping, and `0/0` yields a
  quiet NaN.

An implementation reproduces this by computing every intermediate in
double precision and applying an explicit single-precision conversion at each
named store. The exponent-range difference (an intermediate that would
overflow a double does not overflow here) is stated for completeness; no
economy magnitude reaches it, and it is not a traced behavior.

**Established — comparison semantics under exceptional values.** Every sign
test in the settlement is an x87 compare followed by a status-word bit test,
so the NaN outcome is decided by which bits are tested:

| Test as written | Bits tested | NaN outcome |
|---|---|---|
| `value < 0` (the `energyuse` sign test) | "below" only | taken — a NaN `energyuse` takes the refund arm |
| `value <= 0` (the carry gates, the `extractsmetal`/`windgenerator`/`tidalgenerator` sign tests) | "below" or "equal" | taken — a NaN carry admits work |
| `value == 0` (the remaining-fraction completion gate) | "equal" only | not taken — a NaN fraction is never complete |
| `pool > total` (both stage-ratio tests, the storage clamp) | "below" or "equal", branch inverted | the "no clamp / full funding" arm |

Signed zero compares equal to zero everywhere, so `-0.0` passes the
completion gate and the non-positive carry gates exactly as `+0.0` does.

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
by thirty. When the owner is in the special second state, both passive
contributions are additionally scaled by the state-2 selector discount
(above).

Negative authored energy use takes a distinct refund/production path. Special
player states can apply one of two executable-defined discounts through a
global mode selector: selector value 0 credits half the negated amount and
selector value 1 credits seven tenths of it (the engine multiplies the
negated amount by a negative half or seven-tenths double and subtracts the
product, so production grows by the scaled credit; an earlier revision that
described the special modes as subtracting the scaled amount was wrong at
byte level). The user-facing identity of those player modes remains open
(state 2 is the computer-policy state per the AI-manager gate inference;
states 1 and 3 remain unnamed), so a clean-room implementation should
isolate that adjustment behind a compatibility rule.

### Wind generation

The battle holds a current wind strength, a 16-bit wind direction, and a
normalized scalar. An eligible wind generator contributes:

`energy production = current wind scalar × unit wind multiplier`

The wind-generator output is subject to the state-2 selector discount when
the owning player is in the special second state. The generation contract is
closed. At battle setup the briefing seeds strength
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
scalar. The tidal output is subject to the state-2 selector discount when the
owning player is in the special second state.

### Constant metal makers

The constant metal-maker field is stored as a byte. When it is enabled and
the unit passes its active-state gates, the economy path converts the byte to
a floating-point value and contributes that value to metal production (the
contributing maker's output is subject to the state-2 selector discount and
to the stall gate). A
stored value of one therefore contributes one unit of metal per settlement
pass (once per ~30 ticks per player under ordinary play). Energy consumption
is a separate authored active-use demand;
the maker's metal output and energy admission must therefore be evaluated as
two coupled pieces of unit state rather than as an invented conversion ratio.

**Established — the maker stall rule.** A metal maker or an extractor
contributes nothing for a settlement pass when the owning unit's energy carry
is strictly positive. The unit's own upkeep acceptance gates its output at
gather time: the maker's `energyuse` demand must itself be accepted (energy
carry non-positive), otherwise the maker contributes no metal that pass and
its upkeep is request-only — no callback fires. Output resumes when the
stage-A paydown returns the carry to non-positive; sustained shortage keeps
the maker stalled across passes, so metal production drops to passive
`metalmake` only.

### Terrain metal extraction

Terrain attribute cells carry an unsigned metal byte. When the mission
provides a uniform surface-metal value, map loading initializes the cell metal
field from it. There is **no per-cell metal raster** in retail: the loader
copies the uniform scalar into every cell's seed byte and never allocates a
varying per-cell source (bounded negative over the loader and the shipped
171-map corpus, whose per-attribute unknown byte is uniformly zero and is not
read as metal). The only per-cell nuance is the legacy TNT format, whose
per-attribute byte feeds the same seed field.

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
is added directly to its metal-production bucket — subject to the state-2
selector discount when the owning player is in the special second state, and
to the maker-stall gate (the extractor's output also requires the owning
unit's own energy admission, so a positive energy carry stalls extraction
too).

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
player pass recomputes capacity.

#### R-ECO-01 §4 — Capacity accumulation and the bonus, exactly [R-ECO-01] (2026-08-29)

**Established — the per-unit accumulation.** Both capacities are zeroed at the
very top of the pass, before the first unit is visited. Inside the idle block
of [R-ECO-01 §2] — that is, for every alive unit whose remaining construction
fraction compares equal to zero, on either `bmcode` branch and regardless of
its activated bit — the engine performs two ordinary single-precision adds:

```
playerMetalCapacity  = float32( definition.metalstorage  + playerMetalCapacity )
playerEnergyCapacity = float32( definition.energystorage + playerEnergyCapacity )
```

**Metal is accumulated first**, and each add is stored back as a single before
the next unit is visited, so the sum is re-rounded per unit exactly as the
production totals are. There is no integer intermediate and no
float-to-integer conversion anywhere in this accumulation. *Correction:* a
previous revision said "any float-to-integer conversion at the capacity add
truncates toward zero (INVARIANTS I3)". There is no such conversion; the
sentence described a decompiler artifact — the player record had been typed
through an integer pointer, so a plain single-precision add read as an
integer store.

**Established — where the 200 floor really lives.** The floor is **not**
applied to capacity. It is applied by the storage-bonus setter, a small helper
that takes a player and two integer amounts and does exactly three things:
set the player's bonus-enable flag bit; store `max(200, energyAmount)`
converted to single precision into the player's energy bonus field; store
`max(200, metalAmount)` converted to single precision into the metal bonus
field. Both comparisons are signed integer `< 200`. *Correction:* the previous
text said "when the bonus enable bit is set, each resource's capacity is
floored at 200". The floor clamps the **bonus operands** at the moment they
are written, once, outside the settlement; capacity itself is never floored.

**Established — the bonus add inside the pass.** After the unit slice and the
mirror-bucket fold, and before any counter is committed, the settlement tests
the bonus-enable flag bit. When it is set:

```
playerEnergyCapacity = float32( energyBonus + playerEnergyCapacity )
playerMetalCapacity  = float32( metalBonus  + playerMetalCapacity )
```

Both bonus fields are already single precision, so the add is a plain
single-plus-single. When the flag is clear neither add happens and capacity is
the unit sum alone.

**Established — who calls the bonus setter, and when.** Exactly two sites.
(a) The battle-initialisation starting-resource writer, which walks the ten
slots and branches on the session kind: on the **mission** kind it calls the
bonus setter with two integerized floats and then writes both live stocks
directly from the mission's own table; on the two other kinds it writes the
live stocks only — from a per-side authored word times 100 on the skirmish
kind — and **never sets the bonus flag**. (b) The commander-replacement branch
inside the per-player phase, which calls it with the same per-side words times
100 (metal word first, energy word second) alongside the replacement spawn.
*Correction:* the previous text said "the battle-init capacity writer runs the
capacity helper per active player before the spawn-credit grant to preserve
the opening 1000/1000 stocks past the 30-tick settlement clamp". It runs the
bonus setter only on the mission session kind. On a skirmish start the opening
stocks survive the first settlement because the commander's own authored
`energystorage`/`metalstorage` enter the capacity sum in the same pass, not
because of a bonus. The `[OX P1]` observation is consistent with the mission
kind; it does not establish the skirmish path.

### Cloak debit

Cloak upkeep is not admitted through the normal smooth allocation path. When
the cloak gate is due, the engine:

1. chooses the stationary or moving cloak cost;
2. converts the cost to an integer using the retail floating-point conversion
   environment and truncation toward zero;
3. compares that integerized cost with the owner's live energy stock;
4. if affordable, subtracts it immediately and records an energy request;
5. toggles the unit's operational-byte bit 2 (mask value 4) through the normal
   transition helper — this raises status-cue slots 14 and 15 plus a network
   packet, not StartBuilding/StopBuilding and not Activate/Deactivate (an
   earlier revision that named the operational/building bit was imprecise;
   slots 14 and 15 are cue slots raised with no caption text, not COB
   callbacks — see [R-ECO-01 §8]);
6. if unaffordable, takes the failure transition without a partial payment.

Because units are visited in stable order, simultaneous cloak costs are
sequential: an earlier slot can make a later slot fail during the same pass.
The debit executes at the settlement cadence — at most once per 30 ticks per
owning player, in the settlement's stable unit-slot order; authored cloak
costs are per-settlement-pass amounts like every other authored economy
field.

**Established — the full debit predicate.** The debit block runs when the
unit carries the **cloak-requested** status bit, a second status bit is clear,
and the unit's per-unit cloak payment deadline is due — the gate is bit set
**and** bit clear **and** deadline due. An earlier reading that OR-ed a
cooldown bit into the gate was falsified at byte level, and the
owner-control-byte-3 condition that would suppress the whole block is inert
during live play because the settlement caller excludes control byte 3. The
per-unit deadline itself is written by nine handler sites as the global tick
plus 150, 300, or 900 (repair, build/get-built/resurrection, and
capture/reclaim respectively); idle cloaked units therefore pay every pass
from the first.

#### R-ECO-01 §9 — Cloak gate, conversion, and the second bit [R-ECO-01] (2026-08-29)

**Established, and a correction to the reach claim.** The first gate bit is
not seeded from `init_cloaked` and it *does* have runtime togglers. It is
cleared at spawn along with its neighbours, and it is set by the **`Cloak_On`**
order handler and cleared by the **`Cloak_Off`** order handler, each of which
first requires a capability bit on the definition and is otherwise a
one-instruction set/clear of that status bit. The previous text — "the
init-cloaked instance bit (seeded once at spawn from the definition's
`init_cloaked`; no runtime toggler exists in the reviewed image) … authored
reach is exactly the mine family; commanders, spies and snipers carry cloak
costs but never enter this block" — was wrong on both halves: the two cloak
orders are the togglers ([R-ECO-01 §10] gives their operation bytes), so any
unit whose definition carries the cloak capability pays cloak upkeep for as
long as the player leaves cloak on. `init_cloaked` is a separate definition
flag bit; its consumer is the initial-posture path, not this gate.

**Established (bounded negative) — the second bit is inert.** The status bit
whose clearness the gate also requires is *read* only here. A search of the
complete decompiled function set found no writer that sets it, and the spawn
initialiser preserves rather than sets it, so the term is always satisfied in
practice. It is recorded because it is part of the literal predicate; it is
not a behavior an implementation can observe. Its intended meaning is
**Unknown** — decider: static trace over the regions the export still misses.

**Established — cost selection and conversion.** The cost is `cloakcostmoving`
when the unit's movement-mode bits are non-zero and `cloakcost` otherwise.
That single-precision cost is passed through the CRT integer-conversion helper
— save control word, set rounding to truncate toward zero, 64-bit integer
store, restore control word — and then converted **back** to floating point
from the resulting 32-bit integer. Both the affordability compare and the two
writes use that integerized value, not the authored float:

```
cost  := float( truncTowardZero( chosen cloak cost ) )
if (cost <= playerEnergyStock) {           // inclusive; equal is affordable
    playerEnergyStock  = float32( playerEnergyStock - cost )
    unit.energyRequested = float32( cost + unit.energyRequested )
    transition(unit, operationalBit2, set)
} else {
    transition(unit, operationalBit2, clear)
}
```

A fractional authored cloak cost below 1 therefore truncates to zero, is
always affordable, and debits nothing. The debit reads and writes the player's
**live stock** directly, mid-pass, so it is visible to every later unit in the
same slice and to the pool that stage one of [R-ECO-01 §5] later builds.

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
both in full or debits neither. It also records both amounts in the
subrecord's requested accumulators. This path does not create proportional
carry.

### R-ECO-01 §7 — The five admission helpers, as expressions [R-ECO-01] (2026-08-29)

**Established.** All five are small methods on the economy subrecord of
[R-ECO-01 §2] — which means they work identically on a unit's embedded
subrecord and on the player-level mirror bucket, and that the "builder's
subrecord" a caller passes is always a subrecord, never a player record. All
arithmetic is single-precision add and subtract with the store narrowing;
none of them touches carry, and none of them is discounted by difficulty.

*Two-resource admission* (construction). Returns whether the work was
admitted:

```
energyRequested = float32( e + energyRequested )
metalRequested  = float32( m + metalRequested )
if (energyCarry <= 0 && metalCarry <= 0) {
    energyAccepted = float32( e + energyAccepted )
    metalAccepted  = float32( m + metalAccepted )
    return admitted
}
return denied
```

Both requests are recorded **before** the gate and unconditionally, so a
denied transaction still shows up in the pass's requested counter and hence on
the HUD. The gate is `<= 0` on both carries, evaluated as two separate
compares with energy first.

*One-resource admission* (the repair family). Returns whether the work was
admitted:

```
energyRequested = float32( e + energyRequested )
if (energyCarry > 0) return denied
energyAccepted = float32( e + energyAccepted )
return admitted
```

Note the shape: the deny test is written as `energyCarry > 0`, so a NaN carry
falls through and admits. Nothing in the metal half of the subrecord is
touched. The caller's own arithmetic — how the repair energy term is formed
from the target's maximum damage and energy build cost, and the fact that each
integerized term is clamped to exactly 1 whenever it is positive before this
helper is called — belongs to "Repair"; only the helper contract is stated
here.

*Direct energy payment* and *direct metal payment* (two separate helpers of
the same shape). These reach through the subrecord's owner pointer to the
**player's live stock**:

```
if (amount <= playerStockForThisResource) {
    playerStockForThisResource = float32( playerStockForThisResource - amount )
    requestedForThisResource   = float32( amount + requestedForThisResource )
    return paid
}
return refused
```

The compare is inclusive: paying exactly the remaining stock succeeds and
leaves zero. The requested accumulator is credited, the accepted accumulator
is not, so an immediate payment appears in the pass's requested counter but
never becomes carry.

*Direct two-resource payment* (weapon fire and other immediate operations):

```
if (e <= playerEnergyStock && m <= playerMetalStock) {
    playerEnergyStock = float32( playerEnergyStock - e )
    energyRequested   = float32( e + energyRequested )
    // the metal stock is re-read through the owner pointer and re-tested
    // here; it cannot have changed, so the metal half always follows
    playerMetalStock  = float32( playerMetalStock - m )
    metalRequested    = float32( m + metalRequested )
    return paid
}
return refused
```

Both compares are inclusive and both must hold before anything is debited, so
this is genuinely all-or-nothing. The redundant inner re-test is preserved
above because it is what the instructions do; it can never fail.

### R-ECO-01 §10 — The operation-byte table, closed [R-ECO-01] (2026-08-29)

The doc-05 tail has carried "the operation-byte table that dispatches build,
repair, unit reclaim, feature reclaim, capture, and resurrection; the handler
identities are established" as an open item, and doc 04 §3.1 carries the same
residual as "the per-phase operation-byte values inside the construction
handler family". The values are now established. Behavior remains doc 04
§3.1's property; this entry exists because doc 05's admission callers are
selected by these bytes.

**Established — how the byte becomes a handler.** An order node's operation
byte indexes one flat runtime array of fixed-size descriptors; the lookup is a
plain scaled index with the byte zero-extended, so the byte *is* the array
position. Each descriptor carries a status caption, a primary handler, a
secondary handler, two flag words, a small byte, and the authored order name.

**Established — how the array is built, and why the values are what they
are.** One startup routine appends a single descriptor, then appends three
static blocks of 22, 22 and 23 descriptors in that order, and **re-sorts the
whole array after every append**, ascending by the descriptor's authored order
name under a case-insensitive comparison (upper-case letters folded to lower;
`_` therefore sorts before every letter). Total 68, matching doc 04's count.
The indices are consequently the alphabetical positions, not the registration
positions:

| Byte | Order name | Caption |
|---:|---|---|
| 0 | *(empty name)* | `Ready` |
| 12 | `BuildingBuild` | `Nanolathing` |
| 13 | `BuildWeapon` | `Nanolathing` |
| 14 | `Capture` | `Capturing` |
| 15 | `Cloak_Off` | `Decloaking` |
| 16 | `Cloak_On` | `Cloaking` |
| 19 | `GetBuilt` | `Under construction` |
| 23 | `HelpBuild` | `Nanolathing` |
| 25 | `MobileBuild` | `Nanolathing` |
| 32 | `Reclaim` *(feature reclaim)* | `Reclaiming` |
| 33 | `ReclaimUnit` *(unit reclaim)* | `Reclaiming` |
| 34 | `RepairPatrol` | `Repair patrol` |
| 35 | `RepairUnit` | `Repairing` |
| 36 | `RepairUnitNoMove` | `Repairing` |
| 37 | `Resurrect` | `Resurrecting` |
| 40 | `SelfRepair` | `Repairing` |
| 51 | `VTOL_HelpBuild` | `Nanolathing` |
| 54 | `VTOL_MobileBuild` | `Nanolathing` |
| 58 | `VTOL_Reclaim` | `Reclaiming` |
| 59 | `VTOL_ReclaimUnit` | `Reclaiming` |
| 60 | `VTOL_RepairPatrol` | `Repair patrol` |
| 61 | `VTOL_RepairUnit` | `Repairing` |

The full alphabetical list, including the orders outside doc 05's scope, is
`Activate` 1, `AirStrike` 2, `AirToAir` 3, `AirToGround` 4,
`AirToGroundHover` 5, `Attack_Chase` 6, `Attack_Kamikaze` 7, `Attack_NoMove`
8, `AttackSpecial` 9, `AttackUType` 10, `BeCarried` 11, `Deactivate` 17,
`Follow_Ground` 18, `Ground_Pickup` 20, `Ground_Unload` 21, `Guard_NoMove` 22,
`MakeSelectable` 24, `Move_Ground` 26, `Paralyze` 27, `Park` 28, `Patrol` 29,
`QMove` 30, `QPatrol` 31, `SelfDestruct` 38, `SelfDestructFG` 39, `Standby`
41, `Standby_Mine` 42, `Standing_FireOrder` 43, `Standing_MoveOrder` 44,
`Stop` 45, `Suppress` 46, `Teleport` 47, `VTOL_Evade` 48, `VTOL_Follow` 49,
`VTOL_GetRepaired` 50, `VTOL_LandIfCan` 52, `VTOL_Landing` 53, `VTOL_Move` 55,
`VTOL_Patrol` 56, `VTOL_Pickup` 57, `VTOL_SeekAttack` 62, `VTOL_SeekGuard` 63,
`VTOL_Standby` 64, `VTOL_Unload` 65, `Wait` 66, `WaitForAttack` 67.

**Supported inference — index 0.** The one descriptor appended before the
three blocks carries the `Ready` caption and a name pointer into a
statically-zero data slot, i.e. the empty string, which sorts first under the
comparator. Nothing was found that writes a name there before the sort, so
index 0 is the default/idle descriptor. Open branch: a runtime writer of that
name slot ahead of the sort would shift every other index by one. Decider:
static trace of that slot's writer set.

**Established — a distinct byte, not to be confused with this one.** The
unit-reclaim handler contains its own six-way switch on a **phase** byte
stored in the order node beside the operation byte (values 0 through 5,
dispatched through a dense jump table). That is the handler's internal state
machine — reclaim start, work pulse, and four terminals — not the operation
table. Doc 04 §3.1's "per-phase operation-byte values" residual is about these
phase bytes; the table above is the outer dispatch.

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

The new carry for a unit is a re-scaled remainder of both stages. The exact
expression, which is **not** the factored form, is in [R-ECO-01 §5].

The same calculation is applied to the player-level mirror bucket. Energy and
metal use their own pool, totals, and ratios, so one resource can be completely
funded while the other remains short.

The engine performs this work with single-precision state and the retail x87
environment of [R-ECO-01 §1]. Exact compatibility requires preserving
evaluation order and conversion points rather than recomputing the equations
with arbitrary higher precision.

### R-ECO-01 §5 — The two stages and the apply-back, instruction-exact [R-ECO-01] (2026-08-29)

**Established — the stage loop.** Energy runs first, then metal; the two are
one loop body executed twice over adjacent slot pairs, so no metal value can
influence an energy value or the reverse. Per resource, with `P` the pass
production total, `S` the player's live stock at this moment, `D` the summed
carry, `A` the summed accepted work, and `f32(...)` a store that narrows:

```
pool := f32(P + S)                                  // stored; a single

// stage 1 — debt
if (D > pool) { debtRatio := f32(pool / D); take := pool }
else          { debtRatio := 1.0f;          take := D    }
rem := pool - take                                  // register, 53-bit
                                                    // rem is also stored to
                                                    // the pool slot here and
                                                    // that store is dead

// stage 2 — newly accepted work
if (A > rem)  { acceptRatio := f32(rem / A); take2 := rem }
else          { acceptRatio := 1.0f;         take2 := A   }
newStock := f32(rem - take2)                        // this is the closing stock
```

Four things in that are load-bearing:

1. **The min is a strict `>` on the total, not on the ratio.** Equality gives
   `1.0f` exactly and performs **no division**, so a fully-funded stage can
   never introduce a rounding error, and a resource with zero debt and
   non-negative pool takes the `1.0f` arm.
2. **`rem` is consumed from the register, not re-read from memory.** The
   engine does store the post-stage-one remainder into the pool slot as a
   single, but that store is immediately overwritten by the stage-two result
   and is never read. Stage two divides and subtracts using the 53-bit
   register value. Narrowing `rem` to single before stage two is a divergence.
3. **A negative pool with zero debt divides by zero.** `D = 0 > pool` when the
   pool is negative — reachable when a slice's authored `energymake` values
   sum negative — so `debtRatio` becomes negative infinity with the exception
   masked, `take` is the pool itself, and `rem` is exactly zero. The stage-two
   ratio then behaves normally and the closing stock is zero or negative. This
   is retail's outcome, not a fault.
4. **When a stage under-funds, the remainder is exactly zero.** `rem - rem`
   and `pool - pool` are exact, so a shortfall always leaves `+0.0`, never a
   residue.

**Established — the apply-back expression and its operand order.** For every
alive unit in the slice, and then once for the mirror bucket, per resource, in
this instruction order:

```
acceptedTerm := accepted - acceptRatio * accepted     // 53-bit
archivedRequested := requested ;  requested := 0
archivedProduction := production ; production := 0 ;  accepted := 0
carryTerm := carry - debtRatio * carry                // 53-bit
carry := f32( acceptedTerm + carryTerm )
```

*Correction:* the previous text gave this as
`new carry = old carry × (1 - debt ratio) + accepted × (1 - accept ratio)`.
That is the same value in exact arithmetic but not in floating point: retail
computes `x - ratio*x`, never `x * (1 - ratio)`, and the two round
differently. The accepted term is also formed first and is the left operand of
the final add. Only the final store narrows to single; both terms are computed
at the working precision of [R-ECO-01 §1]. An unreferenced out-of-line copy of
exactly this expression exists in the image and corroborates the reading.

**Established — what the apply-back archives and clears.** Production is
copied into the archived-production slot and zeroed; requested is copied into
the archived-requested slot and zeroed; accepted is zeroed; carry receives the
expression above. The two archived slots are written here and nowhere else.
The unit loop skips units without the alive bit, so a unit that died during
the pass keeps its stale accumulators — they were already folded into the
totals during the gather, and the next pass will re-zero them only if the slot
is reused.

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
The consumers of the per-unit archived snapshots remain open as a bounded
negative: a search of the reviewed image (3901 boundaries) found no reader of
the archived slots outside the ledger's own redistribution — treat as no
consumer within the reviewed boundary. The writer-set closure above likewise
rests on that bounded census.

### R-ECO-01 §6 — Commit order, the waste clamp, and the four HUD rates [R-ECO-01] (2026-08-29)

**Established — the commit order, exactly.** After the unit slice, the
mirror-bucket fold and the capacity bonus of [R-ECO-01 §4], and **before** the
live stock is folded into the pool, the pass writes four fields per resource,
energy's before metal's:

```
playerProducedThisPass  := P                            // already a single;
playerRequestedThisPass := R                            // copied, not recomputed
cumulativeProduced  := f64( cumulativeProduced  + P )   // double accumulate
cumulativeRequested := f64( cumulativeRequested + R )
```

`P` and `R` are the pass totals, already single-precision from the per-unit
accumulation of [R-ECO-01 §2], and the per-pass fields receive them unchanged
— one of the four is even a plain word copy rather than a floating-point
store, which is value-identical. The cumulative fields are doubles; the
single is added to the double exactly and only the double store rounds. Then,
and only then:

```
pool := f32(P + playerLiveStock)
```

so the two per-pass counters really do report the pass's activity and never
the funds available to pay it, as this section already said. The requested
counter is copied bit-for-bit — no conversion, no clamp.

**Established — the closing stock and the waste clamp.** After the two stages
of [R-ECO-01 §5] produce a closing value per resource:

```
playerLiveStock := newStock                         // raw bit copy, always
if (newStock > playerCapacity) {
    excess := newStock - playerCapacity             // 53-bit
    playerLiveStock := playerCapacity               // raw bit copy
    cumulativeWaste := f64( cumulativeWaste + excess )
}
```

The stock is written **twice** on the overflow path — first with the
unclamped value, then with the capacity — which is invisible to any observer
inside the pass but is the literal sequence. The clamp is **strictly greater**:
a stock exactly equal to capacity is not clamped and wastes nothing. The
excess is computed from the unclamped value and the capacity at the working
precision, and only the double accumulation rounds, so the fractional part of
the overflow is preserved as this section already claimed. A NaN closing value
takes the "no waste" arm and is written to the stock unchanged. Waste is
accumulated **only** here; there is no other writer of the two waste totals in
the ledger.

**Established — the four floats the HUD reads, and the absence of averaging.**
The resource bar samples four player fields — energy produced this pass,
energy requested this pass, metal produced this pass, metal requested this
pass — through four one-line getters and stores them, unchanged and
unscaled, into its own display record. There is **no averaging, no smoothing
and no rate conversion** on those four values: what the bar shows is the last
settlement pass's totals verbatim. They are re-sampled only when the player's
`DisplayTimer` deadline is due, on the strict compare of [R-ECO-01 §1], and
the deadline is then advanced by 30 — so the numbers change at most once per
30 ticks even though the bar redraws every frame. *This is the whole of the
`DisplayTimer` mechanism.*

What **is** smoothed, every frame rather than every 30 ticks, is the pair of
displayed **stock** values beside them. For each resource the bar holds its
own displayed value and steps it toward the live stock in integer space:

```
i := truncTowardZero(displayedValue)
j := truncTowardZero(playerLiveStock)
d := (j - i) / 8                      // integer divide, truncating toward zero
if (d == 0) d := sign(j - i)          // never stall while a gap remains
displayedValue := float(i + d)
if (displayedValue > matchingCapacity) displayedValue := matchingCapacity
```

so the bar closes an eighth of the remaining integer gap per frame with a
minimum step of one, and is clamped to the same capacity the settlement
clamps the stock to (strictly-greater test again). The displayed value is
presentation state only: nothing in the settlement reads it.

## Activation and stall transitions

The economy and work paths use a common transition service to change operational
bits. It suppresses duplicate transitions and invokes script callbacks such as
activation/deactivation and start/stop building when the relevant bit actually
changes.

A shortage can therefore affect more than numerical work. It can change the
unit's operational state and cause script-visible transitions. The exact
mapping of every bit and callback is specified in the unit-script document;
this document requires only that economic admission use that shared service.

### R-ECO-01 §8 — The transition service, exactly [R-ECO-01] (2026-08-29)

**Established.** The service takes a unit, a bit mask, and a set/clear
selector, and operates on the unit's one-byte operational word:

```
before := unit.operationalByte
after  := (selector != 0) ? (before | mask) : (before & ~mask)
unit.operationalByte := after
if (after == before) return               // no callbacks, no packet, no refresh
newlySet     := ~before & after
newlyCleared := before & ~after
```

The write happens unconditionally; only the notifications are suppressed when
nothing changed. Then, in this fixed order, each guarded by its own bit:

| Edge | Effect |
|---|---|
| bit 0 newly set | raise COB `Activate`, then status cue slot 3 |
| bit 0 newly cleared | raise COB `Deactivate`, then status cue slot 4 |
| bit 3 newly set | raise COB `StartBuilding` (no cue) |
| bit 3 newly cleared | raise COB `StopBuilding` (no cue) |
| bit 2 newly set | raise status cue slot 14 (no caption text), then walk the unit's attachment list and notify each attached presentation object |
| bit 2 newly cleared | raise status cue slot 15 (no caption text) |

An `Activate`/`Deactivate` pair therefore always raises both a script callback
**and** a cue, while the bit-2 (cloak) pair raises **only** cues — the pair
this document previously called "the COB callback pair #14/#15" is a pair of
status-cue slots, not COB callbacks. Bit 3 raises only script callbacks.

After the bit dispatch the service refreshes the unit's interface panel, and
then, if the owning player record exists and its control byte is 1 or 2, sends
a four-byte network event carrying a fixed type tag, the unit's slot number,
and the **new** operational byte. Observer-controlled owners (control byte 3)
change state silently.

The service is the only writer of the operational byte in the economy path;
the cloak debit of [R-ECO-01 §9] reaches it with bit 2, and construction
admission reaches it with bit 0.

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
is credited at one half or seven tenths of its value depending on a global
mode selector, with the same pairing used by the factory cancel-current
refund and the shared work helper's reverse arm (selector 0 → half,
selector 1 → seven tenths). An earlier revision that called this pairing
"inverted relative to the capture refund site" was wrong at byte level, and
the referenced site is a misnomer: there is no capture refund — the site is
the factory cancel-current refund, which shares the same pairing.

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
reviewed consumer reads it — bounded absence over the reviewed corpus; the
limit check does not consult it. No parser key or reviewed initializer writes
the per-definition limit field, so stock defaults come from outside the
reviewed corpus (the -1 sentinel implies unlimited by default); both absences
are recorded as bounded negatives in the factory-contract analysis.

**Established fact — numeric pool capacity.** The physical unit pool is
sized once at battle setup as `(catalog definition count) × 10 + 1` records
per player slice (slot zero of each slice reserved as the null identity);
there is no other numeric cap on live units. The mission logical `maxunits`
field has no allocator reader (bounded negative over the reviewed corpus) —
it is not an allocator gate, so pool exhaustion follows the physical size
alone.

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

Audit note (2026-08-27), see [04 §6.4 R-P0-08-A §1] for the full argument:
"obstructed" cannot include cells stamped by the producing factory itself.
Stock exits sit inside the factory's own footprint, so retail's validator
must be reading an occupancy layer finished buildings do not write; an
implementation that retains a completed product on the same mobile-stomp
shorts its exit validation reads will deadlock every first product. The
"Nanoframe creation … remaining fraction one, health zero" wording below is
the established nanoframe contract; the reservation lifecycle around it
(release at completion, structures registry as the building-mask stand-in)
is Nanolathe-side bookkeeping and carries no retail claim beyond this audit
note.

**Nanoframe creation at the exit spot.** On validation success the allocator
creates the unit *at* the exit spot with owner, product definition, remaining
fraction one, health zero, and build stance cleared; the product pointer is
linked into the node payload. If the allocator refuses (limits or pool
exhaustion), the handler prints "Unable to create any more units", schedules
a retry in exactly 300 ticks (not randomized), and stays in state 2.

### Factory product heading [R-P28-ANG-01R §3]

**Established — construction scope.** A successful factory allocation uses the
common unit initializer's `buildangle` sampler and therefore consumes one
global simulation-stream angle invocation (plus the initializer's separate
full-domain draw for another unit-state field). For the stock `ARMSOLAR` and
`ARMLAB` records, `buildangle=4096`, so the product's initial heading is in the
inclusive circular range `30720..34815`, centered at `32768`; the exact signed
conversion and range arithmetic are specified in [04 §2.3b]. The factory's
`QueryBuildInfo` callback contributes the exit **position** only. No factory
heading offset, slope alignment, or `Ovradjust` read follows it.

The initialized heading is authoritative through nanoframe work and natural
completion: neither the completion transition nor `GetBuilt` rewrites it.
Mobile products can later change heading through ordinary movement integration
when a rally order is inherited, but that is a movement update rather than a
second build-angle draw. The footprint anchor, yard-map validation, occupancy,
and pathing remain axis-aligned and are computed from the authored extents;
the initial heading does not rotate or enlarge those rectangles.

**Established — failure and persistence boundaries.** The factory validates
the snapped product rectangle before invoking the allocator. A blocked exit
therefore consumes no angle draw and retries silently after exactly 15 ticks.
An allocator refusal (per-definition limit or full slice) also returns before
common unit initialization, consumes no angle draw, and takes the distinct
300-tick retry with its diagnostic. No failed placement consumes a speculative
random angle.

**Retail save-reconstruction finding (future parity only).** When a factory
product is saved after allocation, its saved heading is the authoritative value
restored by the unit-save path; restore does not recompute it from `buildangle`.
A new successful allocation during save reconstruction still enters the common
initializer and consumes its normal draw sequence before the saved heading is
copied back. Mission placement has the analogous allocator-then-authored-angle
overwrite described in [04 §2.3b]. Nanolathe's active boundary currently
returns an explicit unsupported result for in-battle restoration, so this
preserves future allocator draw ordering but does not authorize a live restore
implementation ([PLAN_14 C10]; [INVARIANTS I13]).

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
nodes leak with the dead header — established: the death finalizer's bounded
census shows no queue walk, and capture drops the queues (the ownership
replacement starts with empty queues), so queued products are lost on both
paths rather than transferred or reclaimed. At zero remaining, the work
helper first synchronously invokes the product completion transition, which
can raise the product's deferred `Activate` edge. Factory completion then
lowers the `StopBuilding` edge; state 4 performs the idempotent second product
completion invocation and its count/presentation bookkeeping. Health and
remaining fraction written by the shared work helper are visible
immediately to later builders visited in the same unit sweep, so the
lowest-slot builder among multiple contributors wins the final step; a later
builder seeing zero remaining simply returns without further work.

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

### OTA-FAC-01B targeted release-boundary pass [R-FAC-01B] (2026-08-28)

This second pass corrects the evidence boundary in [R-FAC-01]. The earlier
audit's statement that no stock COB transform census was available is
superseded for the authored exit-piece inputs only. A clean-room census of the
original `totala1.hpi` COB and 3DO assets establishes these synchronous
`QueryBuildInfo` results. The listed translations are authored 3DO local
coordinates before model-loader half-turn normalization, not runtime world
positions ([fmt cob], [fmt 3do]). The callback indices were decoded
independently from each stock COB's `QueryBuildInfo` sequence (literal N is
popped into output local 0); they were not supplied by pairing the model
files. Exact signed 16.16 numerators are retained below, with rounded
decimals only as a reading aid:

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

The targeted boundaries therefore have the following status:

- **Target and rally handoff — split Established/Unknown.** State 2 uses the
  output piece's hierarchy-composed transform for the product position and a
  separately snapped footprint anchor for validation. For these eight selected
  pieces, the parent is the root `base` with zero translation, so the
  hierarchy-composed authored origin equals the listed piece-local
  translation; that equality is not a generic descendant rule. The product receives `GetBuilt` on its
  primary queue; after completion it copies queued move/patrol rallies or
  falls back to `Park` ([05 "Factory production lifecycle"]). Within the
  reviewed factory-handler call chain, no separate factory-owned egress order
  or clearance segment was recovered. Whether an unobserved movement-layer
  path adds one is **Unknown**.
- **Producer/product collision — Unknown after allocation.** The validator
  runs before a product exists and receives no producer/product pair. No
  post-allocation exemption was found. Closing this requires a retail trace
  of an overlapping exit or a complete static call chain through the first
  product movement and occupancy admission.
- **Blocked release and queue/build gating — split Established/Unknown.**
  Primary-queue front blocking, positive count gating, script-owned stance,
  and the 15-tick blocked versus 300-tick allocator retries are Established.
  Since no post-completion release lane was recovered in the reviewed
  factory-handler call chain, its indefinite-block behavior and any
  force-release policy are **Unknown**. Queue serialization plus delayed
  occupancy publication does not prove same-pass no-stacking.
- **Aircraft takeoff — generic Established, factory ordering Unknown.** The
  ordinary VTOL path begins its velocity-limited climb when a first flight
  point/follow goal is installed. The reviewed factory-handler call chain
  contains no factory-specific takeoff state before `GetBuilt`; whether takeoff
  precedes rally handoff for a product remains **Unknown** ([04 §10.1]).
- **Rotated transform Established; no-stacking Unknown.** The stock piece
  indices, names, and authored local translations above are Established data.
  **Correction (2026-08-28):** this item previously said "The exact runtime
  heading arithmetic for those hierarchy translations … remain **Unknown**"
  and asked for a heading-matrix capture. That arithmetic is Established in
  [04 R-REV-02]: the shared piece locator folds the unit's committed
  orientation into the model root node's angles before rotating, so the
  factory heading does reach the exit position and there is no separate
  heading matrix. The 3DO format's lack of an authored heading field is
  consistent — the heading is runtime unit state. A same-pass no-stacking
  guarantee remains **Unknown**; its decider is a blocked multi-product
  trace.

`TODO(question)`: record a retail run with a blocked exit, a completed ground
product, and a completed aircraft product, including first movement,
occupancy, and rally events. This is the evidence needed to distinguish a
hidden release lane from ordinary `GetBuilt`/VTOL handling. Do not promote an
unresolved interpretation into the construction contract.

### OTA-FAC-01C factory-release call-chain continuation [R-FAC-01C] (2026-08-28)

This continuation traces the factory handler through its placement validator,
allocator, primary-queue pump, and product-side `GetBuilt` handler. It narrows
the release boundary without claiming a retail runtime result that the static
call chain cannot show. The earlier wording that treated the state-2 snapped
coordinate as a factory movement goal is corrected here: it is a validation
anchor only.

**Ground target and order representation — split Established/Unknown.** The
factory's state-2 sequence is: synchronously run `QueryBuildInfo` with output
cell 0 seeded to `-1`; resolve the selected hierarchy piece with the factory
origin into a signed 16.16 world position; derive a separate footprint anchor
with the half-extent formula from [04 §6.3]; validate that rectangle; and,
only on success, allocate the product at the resolved world position. The
validator is an area check, not a path request: this sequence emits no
move-to-exit goal, path search, repath, push, or factory-owned release node.
The product receives an ordinary `GetBuilt` node on its own primary queue.
Thus the mandatory ground release target is **not established as a second
target**; the established factory-side target is the direct allocation point.
Whether an unobserved movement-layer consumer adds a product-side clearance
step after allocation remains **Unknown** and requires a retail trace or a
complete first-movement call-chain capture. Do not implement an egress offset
or hidden release order from the footprint anchor.

**Query result and producer identity lifetime — split Established/Unknown.**
The `QueryBuildInfo` result is consumed only to select the hierarchy piece and
form the allocation position; a failed query leaves the seeded sentinel. The
subsequent handling of that invalid sentinel is not evidence for a separately
authored release offset. The factory production node holds
the allocated product through construction and clears that product pointer on
normal completion before the counted node is restarted or freed. The product
also receives a producer link before `GetBuilt` is inserted. `GetBuilt` reads
that link while it waits for completion and while it copies the producer's
queued rally records. No explicit unlink of the product-side producer link was
found in the bounded factory/GetBuilt chain; its lifetime after that handler,
and cleanup when either unit is destroyed or captured, are **Unknown**. The
death/capture path must not be treated as a transfer mechanism.

**Rally and no-rally timing — Established.** `GetBuilt` first waits until the
product's remaining fraction is zero. It then walks the producer's primary
queue and appends the producer's queued move/patrol records to the product in
queue order, preserving patrol identity. If none is found, it appends a normal
`Park` order at that point. `Park` is therefore not issued at allocation and is
not evidence of a release segment. The product-side order can dispatch in the
same unit sweep only when the product's stable slot is still to be visited;
otherwise it waits for the next sweep ([04 §3.8]).

**Next-product gate and blocked behavior — split Established/Unknown.**
Factory production records are primary-queue nodes, and an unsatisfied wake
mask on the primary front blocks later primary nodes ([04 §3.3]). A successful
completion decrements the counted record and returns the pump to state 0 in
the same pass; when the count remains positive, the next product can enter
state 2 without a release-clearance wait. Each candidate is independently
validated before allocation. A blocked footprint retries silently after
exactly 15 ticks, with no product allocated; allocator exhaustion is a
separate exactly-300-tick retry with its diagnostic. No post-allocation
release retry or force-release state appears in this chain. Consequently an
indefinitely blocked product-side release, producer/product collision
exemption, and no-stacking guarantee for same-pass counted products are
**Unknown**. The validator's pre-allocation null self identity cannot establish
an exemption for a product that does not yet exist ([04 §6.4]).

**Rotated factories — split Established/Unknown.** The footprint arithmetic is
Established: for each axis, the snapped cell is
`(p - (f << 19) + (1 << 19)) >> 20` using a signed arithmetic shift, while the
product remains at the independently resolved QueryBuildInfo world position
([04 §6.3]). The factory handler adds no separate heading, yard-map, model
extent, or fixed-cell offset. **Correction (2026-08-28):** the next sentence
here used to read "The exact runtime heading transform applied to the
authored hierarchy translation, including any half-turn normalization,
remains **Unknown**". It is Established in [04 R-REV-02]: the piece locator
walks the selected piece's parent chain, folds the unit's committed
orientation into the model root node's angles, applies its rotations in the
order Rz, Rx, Ry with round-to-nearest narrowing per axis, negates
accumulated Z once on output, and the factory helper then adds the unit's
world origin componentwise. The load-time half-turn is a separate, earlier
conversion owned by [03 §2.4]. The stock piece census in [R-FAC-01B] still
supplies only authored local values.

**Aircraft boundary — generic Established, factory ordering Unknown.** The
ordinary VTOL movement family begins its velocity-limited climb when its first
flight point/follow goal is installed ([04 §10.1]). The factory sequence itself
does not issue that goal or enter a factory-specific takeoff state before
`GetBuilt`; the exact point at which an aircraft product takes off relative to
inherited rally remains **Unknown**. A representative aircraft completion
trace must record allocation, `GetBuilt`, first VTOL goal, vertical movement,
and rally dispatch to close this boundary.

**Implementation boundary.** The established implementation inputs are the
seeded synchronous query, hierarchy-composed 16.16 allocation position,
independent footprint-anchor validation, factory primary-queue front gate,
15/300-tick pre-allocation retries, counted same-pass restart, producer link,
and post-completion `GetBuilt` rally-or-`Park` sequencing. OTA-FAC-02 is
blocked for any explicit release state, producer/product exemption, or
aircraft release step until the Unknown items above are closed by a retail
runtime trace or a complete static chain through first movement and occupancy.
This is a research boundary, not permission to infer a fixed egress target.

### P28-FAC-01R — final increment through factory idle/close [R-FAC-01R] (2026-08-28)

This section closes the engine-side transition order and the stock factory
COB callback choreography after the final admitted construction step. It does
not close a hypothetical product egress lane. Evidence is the five-state
factory handler, the primary-pump result protocol, the shared edge machine,
the fixed phase order in [01 §4.4], and a clean-room census of the stock lab
COBs. No retail runtime trace was available; script animation durations are
therefore stated only where the authored COB supplies them.

**Established — transition table.** Let `T` be the global tick in which the
factory work state accepts the final resource-admitted increment and stores
`remaining = 0`. “Deferred” means the callback thread is allocated at the
edge and normally first executes in that unit's next normal COB drain; it is
not a factory-node deadline.

| Tick/order point | Factory state and mutation | Callback/order consequence |
| --- | --- | --- |
| `T`, state 3 | The shared work helper stores zero remaining and the corresponding difference-of-truncations health gain. Because the new remaining value is zero, the helper synchronously invokes the product completion transition before returning success; that transition can raise the product's deferred `Activate` edge. The factory handler returns advance and the primary pump enters state 4 in the same pass. | The product transition is synchronous engine work, not a deferred factory callback. Any product `Activate` script thread is allocated at this point and runs according to the product's own later normal drain/slot ordering. |
| `T`, state 4, first | Lower the factory building edge. The edge machine writes the new byte before starting callbacks. | Allocate deferred factory `StopBuilding`; the callback is not interpreted yet. |
| `T`, state 4, next | Run the factory completion transition again (its edge effects are suppressed when already set), clear the construction presentation payload, decrement the production node count once, refresh the builder interface, and return result 0. | The first completion transition from the work helper owns the product's zero/complete state and possible `Activate`; state 4's repeated transition does not create a second unchanged activation edge. |
| `T`, same primary-pump pass | Restart the node at state 0. | If count `> 0`, the engine raises activation (normally an unchanged edge), advances through the already-set stance gate, and can allocate the next product in state 2 during this same pass. If count `<= 0`, it lowers activation, allocates deferred `Deactivate`, and returns the node-drop result. |
| `T`, queued-next path | A successful state-2 allocation links the product, copies standing-order fields, inserts product-side `GetBuilt`, raises the next building edge, and advances to work. | The next `StartBuilding` callback is deferred. A blocked exit remains pre-allocation and retries silently after 15 ticks; no product-side release state is entered. |
| `T`, empty path | The production node is unlinked/freed after state 0. | `StopBuilding` was allocated before `Deactivate`; both are normally run in that order in the next normal drain. |
| `T+1` normal drain (ordinary path) | The factory's script slots are drained in ascending allocation order, then piece interpolation runs. | `StopBuilding` stops the authored pad spin and returns. It performs no door operation, piece wait, or sleep. On an empty path, `Deactivate` then signals the state-transition mask, sets its own mask, and enters its authored 5000 ms sleep. On a queued-next path, the deferred `StartBuilding` then starts the authored pad spin and returns; no `Deactivate` edge occurred. |
| after the `Deactivate` sleep | The VM converts 5000 ms to a 150-tick timer. After the normal sleep wake, `Deactivate` starts `RequestState(1)`. | The callback itself has no engine close timer beyond this script sleep. |
| `RequestState(1)` / `Stop` | `Stop` writes `INBUILDSTANCE = 0`, calls `CloseYard`, then runs the selected COB's authored deactivation animation and caches its pieces. | `CloseYard` writes `YARD_OPEN = 0`, polls the level, and clears `BUGGER_OFF` on success. If the gated write is still not effective, it sets `BUGGER_OFF`, sleeps 1500 ms (45 VM timer ticks), and retries. |
| close-animation returns | The selected COB's deactivation script returns after its own authored moves/turns/sleeps. | There is no engine-side GUI timer or factory-node state left to close the yard. The exact final pose and duration are COB data. |

The stock `ARMLAB` callback census gives a concrete authored close duration
after `Stop` begins: its deactivation script uses sleeps of 998 ms, 1008 ms,
and 48 ms, converted by the VM to 29, 30, and 1 timer ticks. These three
waits are separated by authored piece moves/turns; they are not replaceable
by one aggregate timer. The 5000 ms `Deactivate` delay precedes this
animation. `CORLAB` has different activation/deactivation timings, so the
ARM values must not be generalized to a shared factory template. A gated
`CloseYard` retry can add one or more 1500 ms waits, so a universal wall-clock
close duration is not established. Other factory COBs own their animation
constants and must be read as data.

Both successful `OpenYard` and successful `CloseYard` branches clear the
`BUGGER_OFF` port before returning; the port is asserted only around a denied
yard transition's retry loop.

**Established — product movement is a separate boundary.** `GetBuilt` is on
the product's primary queue, not on the factory's close path. Once product
remaining is zero, it copies queued move/patrol rally records from the
producer or inserts `Park`, then removes itself. It can dispatch in the same
unit sweep only when the product's stable slot is still ahead of the
factory's slot; otherwise it waits for the next tick. Any first movement,
VTOL takeoff, occupancy admission, or producer/product collision behavior is
therefore separate from the door callbacks above and remains subject to the
release-boundary Unknowns in [R-FAC-01].

**Established — cancellation boundaries.** Interrupt masks are tested before
the state machine on a production-node visit. Cancel-current therefore wins
over a state-3 final increment only if its wake bit is admitted before that
visit's work handler begins: it refunds the established amount, runs the
completion helper on the frame, sends the cause-9 kill, lowers activation and
building together (deferred `Deactivate` and `StopBuilding` in edge order),
and drops the node without decrementing its count. A work step already
accepted is not interrupted mid-helper; the normal state-4 sequence above
then owns completion. Construction-stopped instead decrements the node count
once, refreshes the interface, and restarts state 0; if that reaches an empty
count, the same-pass state-0 drop performs the activation fall. A node that
has already completed and been freed cannot be cancelled through that
production record. Queue removal through the ordinary cleanup path may emit
the pending `StopBuilding` callback, but it does not establish a product
egress or collision exemption.

**Residual Unknowns (P28-FAC-01R).** The exact result when `YARD_OPEN` remains
blocked, the number of `CloseYard` retries under each occupancy condition,
and any runtime observation that would couple product release to producer
clearance are not established by the static evidence. The same is true of
producer/product collision exemptions, no-stacking, and aircraft
takeoff-before-rally ordering. These release/egress behaviors must remain
`TODO(question)` at any implementation site; do not convert the authored
`BUGGER_OFF` retry into a generic movement or collision rule. The narrower
idle-closure implementation is not blocked: it may implement
`StopBuilding`/`Deactivate`, the 150-tick delay, `RequestState(1)`,
`Stop`/`CloseYard`, and the selected COB's authored animation. Only the
release/egress behavior remains blocked pending a retail trace that records
allocation, first movement, occupancy admission, and the factory callback
timeline together.

### OTA-FAC-01 bounded factory-release audit [R-FAC-01] (2026-08-28)

This audit separates the factory production contract from the still-unclosed
question of how a completed product clears the producer. The evidence is the
factory handler, the primary/secondary descriptor tables, the product-side
`GetBuilt` handler, and the shared VTOL movement handlers. No retail runtime
probe or stock COB transform census was available for this audit; where that
evidence is required, the result remains **Unknown**.

**Target derivation — Established.** State 2 runs the factory script's
`QueryBuildInfo` query with output cell 0 pre-initialized to `-1`, resolves the
returned piece transform together with the factory origin, and stores that
world position on the production record. It separately derives a snapped
footprint anchor for validation. Successful allocation uses the resolved
piece position, not the snapped anchor. No independent factory-heading,
yard-map, model-extent, or fixed-cell-offset term is read by this path. The
local transform authored by each stock factory, and whether it coincides with
the model center, remain the data question already marked in [04 §6.3].

**Movement/order form — Established at the factory boundary; release behavior
Unknown.** The factory success epilogue creates a real `GetBuilt` node on the
product's primary queue. The bounded handler has no additional factory-owned
release node, protected egress order, or direct movement integration between
allocation and `GetBuilt`; its states are only the count gate, script stance
wait, exit validation/allocation, work, and completion. Whether another
movement-layer mechanism not identified in this call graph supplies a
post-completion release remains **Unknown**. `Park` must therefore remain a
normal no-rally fallback, not be described as a proven release operation.

**Rally sequencing — Established.** `GetBuilt` waits for the product's
remaining fraction to reach zero, then walks the producer's primary queue and
appends copied `QMove`/`QPatrol` records to the product, preserving order and
patrol identity. It uses `Park` only when no such rally record exists. Thus
inherited rally is a post-completion handoff; no intermediate factory-release
segment was recovered, and no evidence says that the inherited rally itself
must clear the producer footprint. Same-sweep dispatch still depends on the
product slot sorting after the producer, as stated above.

**Producer/product collision — Pre-allocation Established; post-allocation
Unknown.** Before allocation there is no product, so the state-2 validator can
only see existing terrain, feature, building, and mobile-occupancy state. The
factory call supplies a null self identity and no producer/product pair to a
post-allocation movement exemption. The bounded evidence finds no dedicated
factory-product collision exemption after allocation. The supported
occupancy-layer inference and its limits are recorded in [04 §6.4
R-P0-08-A §1]; it must not be expanded into a global collision bypass.

**Link lifetime — split contract.** The factory production record's product
pointer is cleared on normal completion and the record is freed when its count
is exhausted (**Established**). The product-side auxiliary builder link has no
separate unlink in the bounded factory call graph; product teardown and
factory death/capture cleanup beyond the paths already described remain
**Unknown**. In particular, the death/capture path does not establish
transfer of a pending product or queue to a replacement factory.

**Blocked lane — split contract.** A blocked snapped exit is retried silently
every 15 ticks before allocation, without a timeout or force-placement. An
allocator refusal is a distinct 300-tick retry with its established message.
Those are the only factory release-adjacent retry states recovered. Behavior
of a hypothetical post-completion release lane when its destination stays
blocked is **Unknown**, because no such lane is present in the bounded factory
handler evidence.

**Aircraft takeoff — generic movement Established; factory sequencing
Unknown.** The VTOL movement family takes off implicitly when its first VTOL
point/follow goal is installed: vertical rise is velocity-limited and uses the
ordinary flight mover. The factory product path itself does not issue a
factory-specific takeoff callback or state before `GetBuilt`; the descriptor
set distinguishes `VTOL_MobileBuild`, but the exact aircraft factory handoff
to that movement path is not established by the reviewed evidence. Therefore
the order “takeoff before rally” remains **Unknown** for factory products.

**Queue and heading boundaries — Established/Unknown.** Production records
use the primary queue, whose blocked front stalls later records; the positive
count gate, script-owned in-build stance, silent 15-tick blocked retry, and
tail-only count coalescing are established above. Each successful product is
revalidated before allocation, but the same-pass occupancy publication window
means that a no-stacking guarantee for multiple coalesced products is not
established. A rotated factory contributes orientation through the resolved
`QueryBuildInfo` piece transform — specifically, the locator folds the
factory's committed orientation into the model root node before rotating
([04 R-REV-02]) — and no separate product-heading offset is read. The
product's independent initial heading is the common allocator's `buildangle`
result, now established in [R-P28-ANG-01R §3]. The remaining **Unknown** is
narrower still: whether a rotated producer's stock exit transform coincides
with its geometric/model center, which is a question about the authored
models and does not change the product-heading contract.

This audit supersedes no established lifecycle text. It narrows the earlier
release gap: the engine-side production and rally handoff are closed, while a
retail post-completion egress/takeoff mechanism, its collision exemption, and
its blocked-lane policy are not. Implementations must retain these as
`TODO(question)` until executable or authored-data evidence closes them.

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
   selector decides: selector value 0 credits **half** the amount, selector
   value 1 credits **seven tenths** of it, any other value adds the full
   amount. The engine reaches the scaled credits by multiplying the amount by
   a negative half (selector 0) or negative seven-tenths (selector 1) double
   and subtracting the product, so the bucket grows by 0.5× or 0.7× the
   amount. **Correction (2026-08-26):** an earlier revision of this document
   stated that selector 0 subtracts seven tenths, selector 1 subtracts one
   half, and that this site's pairing is inverted relative to the ledger's
   negative-energy-use refund site; both claims were wrong at byte level.
   The selector mapping is selector 0 → 0.5, selector 1 → 0.7 at all three
   sites (ledger refund, this cancel-current site, and the shared work
   helper's reverse arm) — the pairing is the same, nothing is inverted, and
   both special modes are reduced positive credits.
3. run the completion transition;
4. send the ordinary kill packet — kind-9 damage of exactly 30000 through
   the normal death flow, unscaled by the armor branch because scaling
   requires damage below 30000. Cause-9 deaths skip the killed-severity
   script query entirely: severity is zero, so there is no explosion and no
   corpse — the product simply vanishes. (An earlier revision's phrase "so
   wreck rules apply" was a leftover of an earlier reading and is
   withdrawn; severity zero means no wreck is produced.)
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

Every ordinary construction caller — the mobile builder's work state, the
assist state, the building-factory work state, and their VTOL twins — derives
the same integer worker quantum from the **builder's** definition:

`worker = (uint16)workertime / 30`

The division is integer division on the zero-extended 16-bit authored value,
performed **before** the value is converted to `float` and before the
remaining-fraction calculation. A definition whose `workertime` is below thirty
therefore produces a zero quantum in this path, and the shared step returns
without doing anything (see [R-WORK-01 §1]). Two callers supply a different
value: the self-repair path derives its own quantum from `healtime`
([05 "Repair"]), and the under-construction wait path supplies a negative
quantum ([05 "Resurrection"], reverse arm).

`workertime`, `buildtime`, `maxdamage` and `builddistance` are read from the
unit FBI as, respectively, an unsigned 16-bit integer, a signed 32-bit integer,
a signed 32-bit integer, and an unsigned 16-bit integer;
`buildcostenergy`/`buildcostmetal` are parsed as integers and stored as
single-precision floats [fmt fbi].

### Remaining fraction

Let:

- `old` be the target's remaining construction fraction (a stored `float32`);
- `worker` be the quantum supplied by the caller (a `float32` argument);
- `buildTime` be the **target** definition's `buildtime` (signed 32-bit).

The progress helper computes:

`new = clamp(old - worker / (float)buildTime, 0, 1)`

The resource demands admitted for this step are proportional to the decrease:

- `energy demand = buildcostenergy × (old - new)`;
- `metal demand  = buildcostmetal  × (old - new)`.

The helper commits the step only when the two-resource admission service
accepts both resource buckets [05 "Two-resource admission"]. If admission
fails, it does not advance the remaining fraction, does not change health, and
emits no nano segment. [R-WORK-01 §1] gives the evaluation order, the
narrowing points, and the clamp's exact shape.

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

The completion test is on the **stored** field, immediately after the step
writes it: if the target's remaining fraction compares equal to `0.0f`, the
helper calls the completion transition with the builder and the target. The
test runs on both arms of the helper and even on the path where admission was
refused (there the field is unchanged, so it can only fire if the field was
already zero — which the helper's entry guard has already excluded).

When the remaining fraction reaches zero, the engine finalizes the unit's
construction state, occupancy, sensors, economic eligibility, order state,
and relevant script callbacks. Some presentation changes, such as replacing
the nanoframe reveal with the complete model, are consumed by the renderer.
The helper clamps the new remaining value between zero and one, so no fraction
escapes that range. For a factory product, the helper synchronously runs the
product completion transition (including any possible product Activate edge)
when it stores zero; the factory state-4 handler then lowers StopBuilding and
performs the idempotent second transition before its count/presentation
bookkeeping.

The exact order of every completion side effect beyond that is closed by the
construction-completion corpus analysis [P0-14 §3]: the helper's product
transition precedes the factory StopBuilding edge, while occupancy and
line-of-sight stamping happen after settlement in the same tick; AI
completed-counts refresh at the next 30-tick strategic refresh (up to 30 ticks
of lag); and trigger polling that checks completed-unit counts runs only for
the local player when that player's settlement deadline is due. The
remaining-fraction transition and health arithmetic are established; the
completion side-effect ordering beyond those steps is closed as cited.

#### R-WORK-01 §1 — The shared construction step, instruction-exact [R-WORK-01] (2026-08-29)

**Established.** One helper implements every build, assist, factory-product and
deconstruction step. It takes a builder, a target, and a single-precision
worker quantum, and returns whether work was committed. All arithmetic below is
x87; the narrowing points are where the code stores to memory, and they matter.

```
step(builder, target, worker):                       // worker is float32
  if (target.remaining == 0.0f) return notCommitted  // exact float compare
  if (worker >= 0.0f) setWorkedThisTickFlag(target)  // set before the zero test
  if (worker == 0.0f) return notCommitted

  def       = target.definition
  new80     = old - worker / (float)def.buildtime    // extended precision;
                                                     // buildtime is int32 -> x87
  if (new80 <= 0.0) new80 = 0.0
  if (new80 >= 1.0) newStored = 1.0f
  else if (new80 <= 0.0) newStored = 0.0f
  else                   newStored = (float32)new80  // narrowed here

  delta32   = (float32)(old - newStored)             // narrowed, then re-read
  energyDemand = (float32)(def.buildcostenergy * delta32)
  metalDemand  = (float32)(def.buildcostmetal  * delta32)   // metal multiplied
                                                            // second, stored first
  gain      = trunc(maxDamageF * old) - trunc(maxDamageF * newStored)
              // maxDamageF is def.maxdamage widened through a 64-bit integer
              // load whose high word is zero, so a negative authored maxdamage
              // reads as a large positive value
```

Forward arm (`worker >= 0`):

```
  if (!admitTwoResource(builder.subrecord, energyDemand, metalDemand))
      return notCommitted                            // nothing else is written
  h = health + gain
  if ((unsigned)h >= (unsigned)def.maxdamage) h = def.maxdamage   // UNSIGNED
  target.health    = (int16)h
  target.remaining = newStored
  target.status   |= dirtyBit          // the same presentation-dirty bit both
                                       // arms raise
  return committed
```

The maximum-health cap is an **unsigned** comparison, so a `health + gain` that
went negative is clamped up to `maxdamage`, not down to zero. `old` is the
stored `float32` read into an x87 register, so the two products that feed the
truncations are exact functions of the two stored fractions.

Reverse arm (`worker < 0`) — see [05 "Resurrection"] for its caller:

```
  refund = -(def.buildcostmetal * delta32)           // positive, delta32 < 0
  target.metalProduction += refund                   // or ×0.5 / ×0.7 under the
                                                     // special-player selector
  h = health + gain                                  // gain is negative here
  target.health    = (int16)(h >= 1 ? h : 0)         // signed floor at zero,
                                                     // no maxdamage cap
  target.remaining = newStored
  target.status   |= dirtyBit
  if (newStored >= 1.0f) selfKill(target, target, 30000, kind 9)
```

Then, on **both** arms and also on the admission-refused path:

```
  if (target.remaining == 0.0f) completionTransition(builder, target)
```

**Established — the worker quantum's integer shape.** Every ordinary caller
computes `(uint16)workertime / 30` with a 32-bit signed integer division and
then converts the quotient to `float`. It is not a floating-point division
followed by truncation, and the two orders are not equivalent for the callers
that reuse the quotient (the resurrection delay divides by it, see
[R-WORK-01 §7]).

**Established — malformed inputs.** `buildtime = 0` makes the division produce
an infinity; the retail conversion helper's out-of-range result has a zero low
word, and the callers here consume only that low word [01 §7], so the derived
integers become `0` rather than a large magnitude. `buildtime < 0` inverts the
sign of the step, driving the fraction the wrong way through the same clamp.
Neither case faults.

**Established — the build-order caption census.** The order-state machinery is
doc 04 §3's property; the captions are listed here because they are the
user-visible edges of the arithmetic above. All are raised on the builder.

| Caption | Slot | Producer and predicate |
|---|---:|---|
| `Starting construction` | 9 | `MobileBuild` and `BuildingBuild`, once the site test passed and the nanoframe was allocated |
| `Building complete` | 8 | `MobileBuild`'s terminal phase |
| `Construction stopped` | 7 | `BuildingBuild` when the order pump raises the terminate/interrupt executor-flag bit; it also decrements the factory queue count |
| `Construction terminated` | 7 | `HelpBuild` when the order's target handle is null |
| `Construction terminated by hostile action` | 7 | `VTOL_HelpBuild` for **either** a null target handle **or** the terminate/interrupt executor-flag bit — the air twin merges the two ground terminals under one caption |
| `Unable to create any more units` | 7 | `MobileBuild`, `BuildingBuild` and `Resurrect` when the unit allocation fails; each then reschedules exactly 300 ticks without advancing |
| `Waiting for target area to clear` | 7 | `MobileBuild`'s site test failing with a retry count of zero; the retry is 30 ticks |
| `Target area was blocked` | 7 | the same site test once the retry count exceeds 10; terminal |
| `I can't reach the construction site` | 7 | `MobileBuild`'s approach phase when the arrival-failure executor-flag bit is set and the range test of [R-WORK-01 §2] still fails |

**Unknown — the executor-flag bits.** Several of the predicates above and in
[R-WORK-01 §3..§7] test bits of the flag word the order pump passes into an
executor: a terminate/interrupt bit, a cancel bit, an arrival-failure bit, and
a high bit that unit reclaim and capture treat as terminal. Their producers and
names belong to doc 04 §3.1 and are not established here. Decider: static trace
of the pump's writer set for that word.

#### R-WORK-01 §2 — Build-distance range test and approach radii [R-WORK-01] (2026-08-29)

**Established.** The range test shared by mobile construction, repair and the
VTOL twins is two-dimensional in X and Z, ignores Y entirely, and is
footprint-aware on both ends. All three magnitudes go through a double-precision
two-argument hypotenuse helper and then through the truncating conversion:

```
distFixed  = trunc( hypot(builder.x - target.x, builder.z - target.z) )  // 16.16
distWorld  = (int16)(distFixed >> 16)     // the signed high word, not a shift
                                          // of the full 32-bit value
builderPad = trunc(  8.0 * hypot(builder.footprintX, builder.footprintZ) )
targetPad  = trunc( -8.0 * hypot(target.footprintX,  target.footprintZ ) )
inRange    = (distWorld - builderPad + targetPad) <= (uint16)builddistance
```

`targetPad` is computed with a **negative** eight, so both pads subtract: the
test is centre distance minus each end's half-footprint diagonal, in world
units, against the builder definition's `builddistance`. The factor eight is
half of the sixteen world units per footprint cell. `builddistance` is compared
inclusively.

The footprint words are the unit instance's copies of the definition's
`FootPrintX` / `FootPrintZ`. When the target is a construction **site** rather
than a live unit — the mobile builder's approach state — the same expression
substitutes the product definition's footprint words for the target instance's.

Unit reclaim uses a different, squared form of the same idea and adds a
per-target-definition reach term:

```
r  = (uint16)builderDef.builddistance + (int16)targetDef.reclaimReach
in = ((dx*dx) >> 32) + ((dz*dz) >> 32) <= r*r      // each square truncated
                                                    // separately, 64-bit multiply
```

**Established, recorded as instructions — the assist approach radius.** The
assist state asks the mover to close to
`builddistance + half` where

`half = trunc(16.0 × sqrt(footprintX² + footprintZ + footprintZ)) / 2`

taken from the **target's** definition. The summand really is
`footprintX * footprintX + footprintZ + footprintZ`: the two additions are
against the same operand and neither squares it. This is dimensionally odd and
is reproduced here as the instructions compute it, because it is an approach
radius rather than an authoritative gate. **Unknown:** whether this is a retail
defect or an intended asymmetry — decider: manual retail observation of an
assist approach with a footprint that is much longer in Z than in X.

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

**Closed residuals — byte wrap, cancellation, and repeat requeue.** The slot
byte is a uint8: completion increments wrap 255→0, and the fire gate prevents
launch underflow (a launch only decrements a nonzero byte); a saved or
otherwise malformed value in 200–255 blocks new production with the 300-tick
wait until launches drain it below 200. Cancelling a stockpile node does not
refund its already-accepted carry: the admitted amounts remain in the
builder's buckets and are paid at the next settlement — the carry is wasted,
not credited. Repeat requeue is the established retry-10-on-admission-failure
and state machine 0/1/2 restart; there is no refund path in the state machine.

**Unknown for stockpile:** the exact weapon-id-to-slot translation performed
when the queue node is created (the node stores a slot index 0..2 that is
read without a bounds check), and presentation behavior beyond the selected-
unit refresh call.

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
| Build assist | the same shared construction work helper accepts the assister's worker quantum | once after accepted work | one | unfinished target retries after one tick |
| Unit reclaim / capture | target is valid and in range, and the operation is admitted | once for the visit | one | operation schedules the next visit two ticks later |
| Feature reclaim | the order's work counter is still above 15 after the visit's decrement | once for the visit | **two** | operation schedules the next visit two ticks later |
| Resurrection | the order's wait counter has not reached zero | once for the visit | one | wait state retries after **one** tick |
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

- the two-segment call and the recovered `15` gate belong to **feature
  reclaim**, not to the build-assist path — see the correction in
  [R-WORK-01 §8];
- unit reclaim's order counter advances by two per visit and the operation
  schedules the next visit on a two-tick cadence, emitting one segment each
  visit; and
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
established.

**Established — the record constructor and allocator epilogue [R-P0-06 §5
addendum].** The submission helper constructs the 76-byte segment record
inline: a one-time lazy zeroing of the record template, the vtable pointer,
the selector byte (the same value that selects the strip), then the geometry
initializer that copies the caller's source and target triples and derives
the per-tick 4/11 and 7/11 interpolation deltas. The selector value is the
strip index: nano records append to **strip 6** of the shared ten-strip
family — "selector 6" and "strip 6" are one number, closing the strip-
ownership question. The record comes from a fixed pool; exhaustion makes the
append a silent no-op with no query side effect and no rollback of already
committed work. The pre-insert count check evicts the oldest record when the
count exceeds 400, keeping the steady bound at 401. The record's per-tick
update draws its position jitter from the **CRT** random stream (six draws
per iteration, five iterations — 30 draws per record per tick), never from
the simulation stream, so nano presentation cannot perturb the sim RNG.

**Closed (2026-08-27).** This addendum previously carried a standing question
reading "the renderer-side consumers of strip 6 — the per-segment fade curve and
the logical-to-palette color mapping — remain open (presentation lane); the
engine-side record carries positions, interpolation deltas, and a value of
0xa1 + (iteration % 7) whose consumer was not traced." Both are now traced and
the framing was wrong: there is no fade curve, because the record is a particle
emitter rather than a drawn segment, and the `0xa1 + (iteration % 7)` value is
the particle's own palette index, advanced one step up the green ramp every
tick. The `4/11` and `7/11` factors narrow the source and target boxes rather
than animating a segment. The full contract — box narrowing, five particles and
six CRT draws per tick, the four-world-units-per-tick travel and its
`trunc(distance/4)` lifetime, the colour cycle, and the single-pixel LOS-gated
draw — is written up at [03 §5.5 "The nanolathe spray"] as `[R-P0-19-P]`.

### R-WORK-01 §8 — Emission producers, direction, geometry, and the corrected assist gate [R-WORK-01] (2026-08-29)

**Correction — which handler owns the two-segment call and the `15` gate.**
[R-P0-06 §1] and [R-P0-06 §3] attributed both to the build-assist path
("the build-assist path makes two segment calls when its remaining work counter
sits above the recovered `15` gate"). Neither belongs there. The assist
executor's work phase is byte-for-byte the ordinary construction step: one
worker quantum, one admission, one nano query, **one** segment, retry after one
tick. The two-call site and the `>15` counter gate are in the **feature
reclaim** executor's work phase, where the counter in question is the order
node's countdown of [R-WORK-01 §5]. Feature reclaim is the only two-segment
producer in the engine.

**Established — the complete producer census.** Each row is one accepted work
visit. "Direction" says which end of the segment is the source.

| Producer | Segments per visit | Direction | Retry |
|---|---:|---|---|
| Mobile build, building/factory build, build assist, and the VTOL twins | 1 | builder nano piece → target box | 1 tick |
| Repair (`RepairUnit`, `RepairUnitNoMove`, `SelfRepair`, `VTOL_RepairUnit`) | 1 | builder nano piece → target box | 1 tick |
| Resurrection wait | 1 | builder nano piece → feature box | 1 tick |
| Unit reclaim | 1 | target box → builder nano piece | 2 ticks |
| Capture | 1 | target box → builder nano piece | 2 ticks |
| Feature reclaim (only while the counter exceeds 15) | 2 | feature box → builder nano piece | 2 ticks |

The two directions are two entry points into the **same** 297-byte submission
routine. They differ only in which argument is expanded into a degenerate box
and which is taken as the six-word box; both write the same record with the
same selector byte, and the selector byte is the strip index
[R-P0-06 §5 addendum]. Capture and reclaim are therefore "reversed" in exactly
one sense: the source end is the target's footprint box and the destination end
is the builder's nano piece.

**Established — the six-word box, exactly.** For a unit target the box is the
target's world position plus the six signed model/footprint extents of its
definition, in the order

```
x1 = target.x + extent[0]      x2 = target.x + extent[3]
y1 = target.y + extent[1]      y2 = target.y + extent[4]
z1 = target.z + extent[2]      z2 = target.z + extent[5]
```

Most of the executors — `SelfRepair`, `BuildingBuild`'s work phase, both
ground `RepairUnit` variants, `ReclaimUnit` and `Capture` — build that box but
write `y1 = target.y` with the **first Y extent omitted**, while still using
extent[4] for `y2`. `MobileBuild` and `HelpBuild` add extent[1] as written
above. The two forms are not reconciled anywhere in the image; because the box
is presentation geometry, the divergence shows only as a slightly different
spray origin plane on the majority form. **Unknown:** which form the four VTOL
work executors use — decider: static trace of their work phases (the two
ground forms are established).

For a feature target — feature reclaim and resurrection — the box is built from
the cell instead:

```
x1 = cellX << 20                      x2 = x1 + featureDef.footprintX << 20
y1 = terrainHeight(cell) << 16        y2 = y1 + featureDef.height << 16
z1 = cellZ << 20                      z2 = z1 + featureDef.footprintZ << 20
```

The `<< 20` is the sixteen world units per cell folded into the 16.16
representation; the height byte is already in world units.

**Established — nothing in the emission path is authoritative.** The nano query
and the segment submission happen after the authoritative transition in every
producer, the submission is a silent no-op when the record pool is exhausted,
and neither draws from the simulation stream. A rejected work step emits
nothing and must not call `QueryNanoPiece` speculatively [R-P0-06 §6].

**Established — the work-order randomness census.** Across every handler in
this document:

| Handler | Simulation draws | Where |
|---|---:|---|
| Build (all forms), assist, deconstruction | 0 | — |
| Repair helper and all four executors | 0 | — |
| `RepairUnit` approach | 1 | `30 + boundedDraw(30)` out-of-range retry |
| `RepairPatrol` | 1 | choosing a candidate from the gathered list |
| `healtime` self-repair | 0 | — |
| Unit reclaim | 0 | — |
| Feature reclaim | 1 | phase 1 walk-target height |
| Capture | 0 | — |
| Resurrection | 1 | phase 1 walk-target height |

The bounded-draw helper returns zero **without advancing the seed** when its
bound is below two, so a feature of height 0 or 1 and a single-candidate patrol
list cost no draw at all [01 §8]. The nano record's own per-tick particle
jitter uses the CRT stream and never the simulation stream
[R-P0-06 §5 addendum].

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
passes `(uint16)workertime / 30` as the worker and passes the energy
resource term to the one-resource helper against the builder's energy subrecord.
That helper always adds the amount to energy requested and adds it to energy
accepted only when energy carry is non-positive; it does not touch the metal
subrecord. Only after admission succeeds does the helper emit the separately
computed heal term as kind-10 healing through the ordinary damage path.

**Established fact — repair nano cadence.** Repair emits one nano segment per
accepted repair visit, and an unfinished repair target retries the work state
one tick later, so the presentation follows accepted repair work rather than a
free-running timer [R-P0-06 §3].

#### R-WORK-01 §3 — Repair, exactly: the helper, the executors, and the captions [R-WORK-01] (2026-08-29)

**Established — the helper, instruction-exact.** The step is one small helper
taking a builder, a target, and a single-precision worker quantum:

```
repairStep(builder, target, worker):
  def = target.definition
  if ((int32)def.maxdamage <= (int32)(int16)target.health) return notCommitted
        // signed compare; a target already at or above full health is refused

  healTerm     = trunc( 1 + (def.maxdamage * worker  - 1) / def.buildtime )
  resourceTerm = trunc( 1 + (def.buildcostenergy * worker - 1) / def.buildtime )
        // both chains evaluate identically: widen the numerator source and
        // the build time to the wide floating stack, multiply by worker,
        // subtract a stored 1.0f, divide by the build time, subtract a
        // stored -1.0f, then convert with truncation toward zero

  if (healTerm     >= 1) healTerm     = 1
  if (resourceTerm >= 1) resourceTerm = 1
        // "clamp to exactly 1 whenever positive" - the compare is >= 1 on the
        // already-integerised term, so 0 and negative values survive unchanged

  if (!admitOneResourceEnergy(builder.subrecord, (float)resourceTerm))
      return notCommitted
  damagePacket(builder, target, healTerm, kind 10, flag 0)
  return committed
```

Only `eax` of the conversion is consumed, so `buildtime = 0` yields terms of
`0` rather than a large magnitude [01 §7]: the helper then requests zero
energy, is admitted whenever energy carry is non-positive, and applies a
zero-magnitude kind-10 packet. That is the whole of the malformed-input
behavior the doc-05 tail previously listed as open for this family.

**Established — the four repair executors and which arguments they pass.**

| Order (operation byte) | Builder passed | Target passed | Worker |
|---|---|---|---|
| `RepairUnit` 35, `RepairUnitNoMove` 36, `VTOL_RepairUnit` 61 | the unit running the order | the order's target | that unit's `workertime`/30 |
| `SelfRepair` 40 | the order's **target** | the unit running the order | the **order target's** `workertime`/30 |
| the `healtime` tick path | the unit itself | the unit itself | `(healtime × 8) / 30` |

`SelfRepair` is the **reversed-argument variant** the earlier text recorded as
an open identity ("a variant reverses the context and target arguments; its
user-interface identity … remain open"). It is closed: the order lives on the
*patient*, the repairer is the handle stored in the order node, and the helper
is therefore called with the two arguments swapped relative to `RepairUnit`.
The repairer's energy is billed, the patient is healed, and the patient's own
cloak-payment deadline is pushed to the current tick plus 150 [R-ECO-01 §9].
Its phase 0 admits the order only when the **repairer's** definition carries the
build/assist capability bit and the repairer's own remaining fraction is zero
(a nanoframe cannot repair) and the patient carries an instance permission
byte's low bit; otherwise it returns without work. Its phase 1 first advances
out of the state when `(unsigned)maxdamage <= (unsigned)health`.

`VTOL_GetRepaired` 50 is *not* a repair executor: it performs no work and never
calls the helper. Its phase 0 returns "advance" when
`(unsigned)maxdamage <= (unsigned)health` and otherwise reschedules 30 ticks;
its phase 1 emits the caption. The healing comes from the pad's own
`RepairUnit`-family order. (Confirmed independently by the flight lane's
`R-AIR-01`; the only refinement is that the unsigned test lives in phase 0 and
the caption in phase 1.)

**Established — `healtime`, the only consumer.** The per-tick unit pass calls
the same repair helper on a unit against itself when all of

* the definition's `healtime` is non-zero;
* `(unsigned)health < (unsigned)maxdamage`;
* `currentTick mod 8 == 0` (tested as `tick & 7`);
* the owner is an ordinary or computer player,

hold. The worker it passes is `((uint16)healtime × 8) / 30` — the eight is the
cadence, so `healtime` is expressed on the same per-30-tick scale as
`workertime`. Because both terms are clamped to exactly one whenever positive,
the observable effect is **one health point and one energy unit per eight
ticks** for any `healtime` large enough to make the quotient non-zero
(`healtime >= 4` for the eight-tick scaling); a `healtime` of 1 to 3 produces a
zero quantum, hence zero terms, hence no healing and no charge. The unit pays
its own energy through the ordinary one-resource admission, so a stalled player
stops self-healing. This is the whole of `healtime`'s behavior; it appears
nowhere else in the image.

**Established — range, leash and interrupt gates in `RepairUnit`.** Before the
phase switch the executor refuses the order outright, with
`Repairs unsuccessful.` on cue slot 7, when the order's target handle is null,
or when the target's low two status bits — the mover movement-mode mirror
[04 §2] — are not exactly `1`. When the order carries a leash radius it also
returns terminally once the truncated hypotenuse from the leash origin reaches
that radius. Phase 1 is the range test of [R-WORK-01 §2]; out of range, it
re-points the mover at the target and waits `30 + boundedDraw(30)` ticks — the
one simulation draw in this executor. Phase 3 is the work visit: if the target
is at or above full health (**unsigned** compare) it advances; if either of the
target's movement-mode bits is set it re-approaches and waits 15 ticks;
otherwise it pushes the builder's cloak deadline to `tick + 150`, calls the
helper, and on acceptance queries the nano piece and submits one segment before
rescheduling one tick later. `RepairUnitNoMove` 36 is the same work visit with
no approach phases and with only the null-target entry guard.

**Established — the caption census.** Every status cue this family raises, with
its producing predicate:

| Caption | Slot | Producer and predicate |
|---|---:|---|
| `Unit repaired` | 10 | Four sites, all terminal phases: `SelfRepair` phase 2, `RepairUnit` phase 4, `VTOL_RepairUnit`'s terminal phase, and `VTOL_GetRepaired` phase 1. Each is entered from a work phase that returned "advance" because the target reached `health >= maxdamage`. |
| `Repair aborted.` | 7 | Two sites, both the **null-target guard** of a patient-side order: `SelfRepair` and `VTOL_GetRepaired`. The repairer handle stored in the order node went dead while the patient was waiting. |
| `Repairs unsuccessful.` | 7 | The two builder-side ground executors' entry guard. `RepairUnit` raises it for a null target handle **or** a target whose low two status bits are not `1`; `RepairUnitNoMove` raises it for a null handle only. |
| `Repair mission failed` | 7 | `VTOL_RepairUnit` phase 0 only, after the definition's air-work capability bit passes but the air repair-eligibility predicate on the target fails. It is the air twin of `Repairs unsuccessful.`, not of `Repair aborted.`. |

**Established — repair's randomness.** The helper itself, every executor's
work visit, and the `healtime` path draw nothing. Two draws exist in the
family, both outside the work visit: `RepairUnit` phase 1's out-of-range
`30 + boundedDraw(30)` retry, and `RepairPatrol`'s single draw to choose a
candidate from the damaged-unit list it gathers within its sight distance. A
bounded draw whose bound is below two returns zero **without advancing the
seed**, so a single-candidate list costs no draw [01 §8].

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
already-observed 0.5 or 0.7 scaling family instead of the ordinary addition
(the same selector pairing as the refund family: selector 0 → half,
selector 1 → seven tenths); their user-facing mode names remain open (state 2
is the computer-policy state per the AI-manager gate inference; states 1 and
3 remain unnamed). The payment is a death-side event
after ordinary cause-5 lethal handling and before death explosion, corpse
placement, and final teardown. The builder whose kind-5 packet is fatal
supplies the recipient.

**Established fact — multiple reclaimers.** Reclaim and repair events apply
synchronously during ascending unit-slot traversal, and the damage-packet
receiver rejects packets against already dead-latched targets, so the first
lethal event is authoritative. Repair bills admitted energy even when the
heal is discarded, and the victim's slot relation decides finalization order.

#### R-WORK-01 §4 — Unit reclaim, exactly [R-WORK-01] (2026-08-29)

**Established — the eligibility predicate, and a correction.** The gate the
executor consults at start and re-checks on every work visit is:

```
eligible(builder, target) =
      builder.definition.canreclamate                 // capability bit
   && (target.status & 3) != 2                        // mover movement-mode
                                                      // mirror [04 §2]
   && !target.definition.cancapture                   // NOT a separate
                                                      // "capture-immunity" bit
```

The earlier text said the handler requires "a target definition without the
capture-immunity bit". That is the same bit the **builder** side reads for its
own capture capability: the definition key is `cancapture`, and the target
predicate simply demands it be clear. A unit authored `cancapture=1` is
therefore un-reclaimable *and* un-capturable ([R-WORK-01 §6] shows the capture
executor rejecting the same bit), which in stock content is exactly the
commanders. The three keys `canreclamate`, `canresurrect` and `cancapture`
occupy three consecutive bits of one definition capability word; nothing else
is a "capture-immunity" flag.

**Established — phase structure and the two counters.** The order node carries
two accumulators. Phase 1 stores the damage pulse in the first and zeroes the
second; the second is the visit counter.

```
phase 0  start   : eligible? -> caption "Reclaiming", latch, advance
                   else the caption pair below, terminate
phase 1  arm     : pulse := reclaimPulse(builder, target, 15)
                   counter := 0 ; reschedule 15 ticks
phase 2  approach: face and move to the target
phase 3  move    : shared approach step
phase 4  arrive  : cue slot 11 with no text, advance
phase 5  work    : the visit below
```

**Established — the work visit, in order.**

```
dx = builder.x - target.x ; dz = builder.z - target.z          // 16.16
r  = (uint16)builder.definition.builddistance
   + (int16)target.definition.reclaimReach
if ( ((dx*dx) >> 32) + ((dz*dz) >> 32) > r*r  ||  !eligible ) {
    reschedule 15 ticks ; re-approach ; return
}
if (counter > 14) { damagePacket(builder, target, pulse, kind 5, 0)
                    counter = 0 }
builder.cloakDeadline = tick + 900                            // [R-ECO-01 §9]
source = queryNanoPiece(the builder handle stored in the order node)
submitSegment(targetFootprintBox, source, selector 6)         // reversed
reschedule 2 ticks
counter += 2
```

The pulse test precedes the increment, so from a zeroed counter the sequence of
pre-check values is 2, 4, … 16 and the pulse fires on the visit that sees 16;
after the reset the counter is immediately raised to 2 again, so the steady
period is **eight qualifying visits = sixteen ticks**. The nano cadence is
one segment every two ticks regardless.

**Established — the pulse, instruction-exact.**

```
pulse(builder, target, k):                       // k is 15 at the only call
  costM = max(target.definition.buildcostmetal, 10.0f)
  n     = (int32)( (uint16)builder.definition.workertime
                 * ((int32)((uint16)builder.kills + 5) / 5)
                 * (int32)target.definition.maxdamage
                 * k )                            // 32-bit signed product
  v     = trunc( (double)n / (costM * 300.0f) )   // n widened through a 64-bit
                                                  // integer load with a zero
                                                  // high word
  return (v <= 1) ? 1 : v
```

The kill divisor is a signed integer division by five. The 32-bit product can
overflow silently for large `workertime × maxdamage`; because it is then
re-read as an *unsigned* 32-bit quantity, an overflowed product becomes a very
large positive pulse rather than a negative one.

**Established — the caption census.** `That unit cannot be reclaimed` and
`Reclamation failed` are raised together, in that order, on cue slot 7, when
the builder has the capability but `eligible` fails on the target; the executor
then terminates. `Reclamation failed` alone is raised when the builder's own
`canreclamate` bit is clear. The air twin `VTOL_ReclaimUnit` has the same two
predicates but raises only one caption each: `Reclamation failed` for the
missing capability, `That unit cannot be reclaimed` for the failed target
predicate. That accounts for all three `Reclamation failed` sites and both
`That unit cannot be reclaimed` sites in the image.

**Established — what a partially reclaimed unit is left as.** Nothing is
restored and nothing is paid. The pulses are ordinary kind-5 damage packets, so
an abandoned reclaim leaves the target simply damaged, indistinguishable from
weapon damage and repairable by any repair order; the reclaimer receives no
metal at all, because the whole refund is a death-side event on the lethal
pulse and is computed from `(1 - remaining fraction) × buildcostmetal` — the
target's full build value — rather than from the damage already dealt. The
order node's two accumulators are freed with the node, so re-issuing the order
re-derives the pulse and restarts the visit counter from zero.

**Established — no randomness.** Neither the pulse computation, the eligibility
predicate, nor the work visit draws from either random stream.

## Feature reclaim

Feature reclaim is an order-driven countdown on the order node, not a share of
the reclaimer's work rate. Its payout is a one-time completion event; the
completion helper does not divide the feature pools into a per-tick drip.

#### R-WORK-01 §5 — Feature reclaim, exactly, and a correction [R-WORK-01] (2026-08-29)

**Correction.** The previous text read: "Feature reclaim uses a progress counter
associated with the feature and the reclaimer's work contribution. The feature
definition's damage value acts as the completion threshold." Both halves are
wrong. The counter lives on the **order node**, not on the feature; it is
seeded from the feature definition's **energy and metal pools**, not from its
damage; and it is decremented by a fixed two per visit, so the reclaimer's
`workertime` has **no effect at all** on how long a feature takes. The payout
does re-check a protection bit, which is what the damage-threshold reading was
probably reaching for.

**Established — the executor.** The order's stored position resolves to a
feature definition index; `0xffff` means "no feature here" and terminates the
order with `Reclamation failed` on cue slot 7. The definition must carry its
reclaimable flag bit, or the executor terminates silently.

```
phase 0  start   : builder alive and builder.definition.canreclamate
                   -> record the target position, advance
phase 1  arm     : work := trunc( 15.0f + (featureEnergy + featureMetal) * 0.5f )
                   walk target = the feature cell, with
                   y = terrainHeight(cell) + boundedDraw(featureHeight)
                   ; one simulation draw
phase 2  move    : shared approach step
phase 3  arrive  : cue slot 11 with no text, FALLS THROUGH into phase 4
phase 4  work    : reschedule 2 ticks
                   work -= 2
                   if (work <= 0) advance to phase 5
                   builder.cloakDeadline = tick + 300
                   if (work > 15) {
                       source = queryNanoPiece(builder)
                       submitSegment(featureBox, source, selector 6)   // twice
                       submitSegment(featureBox, source, selector 6)
                   }
phase 5  payout  : the completion helper below ; terminate
```

The `15.0f + (E + M) × 0.5f` form is what the instructions compute: the sum is
multiplied by a stored `-0.5f` and then subtracted **from** `15.0f`. So the work
counter is `trunc(15 + (energy + metal) / 2)`, the visit cadence is two ticks,
and the number of visits is `ceil(work / 2)`. A feature with zero pools still
costs the fixed fifteen, i.e. eight visits and sixteen ticks.

The `> 15` guard is why the last eight visits of every feature reclaim emit no
nano at all, and it is the only two-segment producer in the engine. Phase 3
deliberately falls through into phase 4, so the visit that raises the arrival
cue also performs the first decrement.

**Established — the payout.** The completion helper resolves the world position
back to a terrain cell using the rounding fixup `v + ((v >> 31) & 0xfffff)`
before the shift, follows the multi-cell anchor link when the cell stores the
"linked" sentinel, and then:

1. refuses outright — returning without paying — when the terrain cell's
   protection bit **and** the feature definition's protection bit are both set;
2. adds the feature definition's whole `energy` value to the builder's
   **energy production** accumulator;
3. adds the feature definition's whole `metal` value to the builder's
   **metal production** accumulator;
4. applies the special-player scaling to each addition separately — selector 0
   halves, selector 1 takes seven tenths — with the same pairing as every other
   member of that family [R-ECO-01 §3];
5. replaces the feature with its reclaimed successor, or removes it when no
   successor exists;
6. emits the deterministic multiplayer state command when required;
7. updates the affected footprint and derived world state.

Neither addition passes through an admission helper: the credit is unconditional
and lands in the production accumulators the settlement pass reads
[R-ECO-01 §2].

**Established — randomness.** One bounded simulation draw, in phase 1 only,
whose bound is the feature definition's height byte; it contributes only the
vertical component of the walk target. A height byte below two returns zero
without advancing the seed [01 §8]. The work visits and the payout draw
nothing.

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

**Closed residuals — counts, limits, and failure messages.** Capture carries
no resource cost (bounded: no admission call in the handler). The ownership
replacement increments the new owner's slice count at allocation, with a
transient double-count until the old victim's death teardown decrements it.
The failure-message mapping is closed: capture immunity → "That unit cannot
be captured"; a non-idle victim → "That unit is a cloud of vapor and cannot
be captured"; a failed transfer → "Capture failed" with the node freed; a
per-definition limit or pool failure inside the transfer is silent but the
node still frees. The multi-captor rule is first-wins as described; a later
captor's stale target handle can alias a reused slot (documented stale-
handle risk).

#### R-WORK-01 §6 — Capture, exactly [R-WORK-01] (2026-08-29)

**Established — the admission predicates, in evaluation order.** Phase 0 tests,
and stops at the first failure:

1. the order's target handle is non-null — otherwise `Capture failed` on cue
   slot 7 (the same terminal the executor-flag interrupts use);
2. the builder is still linked — otherwise a silent terminal;
3. the **builder's** definition carries `cancapture` — otherwise a silent
   terminal;
4. the **target's** definition does **not** carry `cancapture` — otherwise
   `That unit cannot be captured` on slot 7;
5. the target's remaining construction fraction compares equal to `0.0f` —
   otherwise `That unit is a cloud of vapor and cannot be captured` on slot 7.

Test 5 answers what the second capture reject means: **"a cloud of vapor" is a
target that is still under construction** — a nanoframe or a partly built unit.
It is not a mid-death or mid-resurrection state. Test 4 uses the same
definition bit as test 3, so any unit that can capture cannot be captured; the
same bit also blocks reclaim [R-WORK-01 §4].

**Established — the capture timer, instruction-exact, with a correction to the
clamp.**

```
base_f = 0.015 * target.definition.buildcostenergy
       + 0.2142857142857 * target.definition.buildcostmetal
       + 150.0                                  // all three float32 constants
base   = trunc(base_f)
if (base >= 1800) base = 1800                   // UPPER clamp only, signed

healthScaled = (uint32)( ((int32)(int16)target.health
                        + (int32)target.definition.maxdamage) * base )
             / (uint32)( 2 * target.definition.maxdamage )
               // the product is a signed 32-bit multiply; the division is
               // UNSIGNED

killsFactor = (int32)(uint16)target.kills / 5    // signed, truncating
timer       = ((killsFactor + 10) * healthScaled * 10) / 100   // signed
```

The previous text wrote the first step as `clamp(trunc(150 + …), 0, 1800)`.
There is no lower clamp: the comparison is a single signed test against 1800
and nothing bounds the value below. It cannot go negative for non-negative
authored costs, but a negative authored `buildcostenergy`/`buildcostmetal`
would drive `base` negative, and the unsigned division that follows would then
produce an enormous `healthScaled` rather than a small one. There is no cap on
the experience factor.

At full health the middle step is the identity (`(maxdamage + maxdamage) ×
base / (2 × maxdamage) = base`), so a fresh, un-veteran target's timer is
`base × 10 / 100`, i.e. one tenth of `base`; a damaged target captures faster
in proportion to `(health + maxdamage) / (2 × maxdamage)`.

**Established — the progress phase, in order.**

```
if (target still linked && (target.status & 0xc) != 0) {
      re-approach ; reschedule 30 ticks ; return             // the target moved
}
if (progress >= timer) advance to the transfer phase
source = queryNanoPiece(the builder handle stored in the order node)
submitSegment(targetFootprintBox, source, selector 6)         // reversed, like
                                                              // reclaim
builder.cloakDeadline = tick + 900                            // [R-ECO-01 §9]
progress += 2
reschedule 2 ticks
```

The completion test is `progress >= timer`, evaluated **before** the increment,
so the number of qualifying visits is `ceil(timer / 2)` and the elapsed time is
twice that in ticks. Capture makes no admission call and debits nothing; the
progress counter never decays.

**Established — the transfer phase.** It calls the central ownership-transfer
path with the target and the **builder's player record**, then raises cue slot
16 with no caption text on the builder, and terminates the order.

**Established — no randomness.** No phase of the capture executor draws from
either stream.

**Correction — the failure-message mapping.** The "Closed residuals" paragraph
above reads "capture immunity → `That unit cannot be captured`; a non-idle
victim → `That unit is a cloud of vapor and cannot be captured`; a failed
transfer → `Capture failed` with the node freed". Two thirds of that is wrong.
The second reject is the under-construction test of predicate 5, not an
idleness test — a moving, firing or otherwise busy finished unit is captured
normally. And `Capture failed` is not a transfer-failure message: it is the
executor's shared terminal, raised before the phase switch when the order's
target handle is null or when the order pump raises the terminate/interrupt
executor-flag bits, and again on the approach phase's arrival-failure branch.
The transfer phase itself has no failure path: it calls the transfer and then
unconditionally raises cue slot 16 with no text. What remains true is that
capture immunity is the first reject and that the node is freed in every
terminal.

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
shrinking it: with a negative worker factor, `newRem = clamp(oldRem −
worker/buildTime, 0, 1)` rises by `|worker|/buildTime` per visit. The arm
credits only metal, directly to the target unit's metal production bucket (no
admission, no energy credit): `refund = metal build cost × (newRem − oldRem)`.
When the target's owner player is in the special second state, the global
mode selector scales the credit — selector 0 → half, selector 1 → seven
tenths — with the same pairing and sign family as the ledger refund and the
factory cancel-current refund; nothing is inverted. If the remaining fraction
is clamped to one, the unit kills itself with a kind-9 30000 packet — the
no-corpse, no-explosion path (severity zero). The arm draws no RNG. In the
sole real caller the builder and target are the same unit (the GetBuilt
under-construction wait path passes a negative factor of `−11 × buildTime /
energyCost` on its 11-tick wake cadence), so "the builder's bucket" and "the
victim's bucket" coincide. Both arms share the same health
difference-of-truncations, but their resource paths are distinct.

**Closed residuals — owner selection, corpse eligibility, exhaustion.**
Resurrection has no ledger cost (bounded: no admission call in the handler);
its owner is the builder's owner byte; corpse eligibility is the catalog
corpse flag plus the underscore-truncated name lookup (a corpse name without
an underscore fails the lookup with the misspelled failure string); slot or
per-definition exhaustion prints "Unable to create any more units" with an
exact 300-tick retry; the feature is removed before the new unit is marked
alive (remaining zero, health one); one simulation draw is consumed for
placement jitter.

#### R-WORK-01 §7 — Resurrection, exactly [R-WORK-01] (2026-08-29)

**Established — the phases.** The order's stored position resolves to a feature
definition index before the phase switch (for phases 0 through 5); `0xffff`
terminates with `Resurrection failed` on cue slot 7, and a definition without
its reclaimable flag bit terminates silently.

```
phase 0  start   : builder linked and builder.definition.canresurrect
                   -> record the target position, advance ; else terminate
phase 1  approach: walk target = the feature cell, with
                   y = terrainHeight(cell) + boundedDraw(featureHeight)
                   ; one simulation draw, the executor's only one
phase 2  move    : shared approach step
phase 3  resolve : copy the feature record's 64-byte name, truncate it at the
                   first '_' (0x5f), look the result up in the unit catalogue.
                   Index 0 -> `Ressurection failed` on slot 7, terminate.
                   Otherwise store the index and compute the delay below;
                   raise cue slot 11 with no text.
phase 4  wait    : v = delay ; delay = v - 1
                   if (v == 0) advance to phase 5
                   source = queryNanoPiece(builder)
                   submitSegment(source, featureBox, selector 6)   // FORWARD
                   builder.cloakDeadline = tick + 300
                   reschedule 1 tick
phase 5  create  : allocate, transplant, remove the feature, advance
phase 6  finish  : `Resurrection complete` on cue slot 8, then classify and
                   build the successor order node
```

Note the cadence: unlike every other work order in this document, resurrection
reschedules **one** tick, not two, and its nano segment runs in the ordinary
construction direction (builder → feature), not the reversed reclaim direction.
It emits one segment per waiting tick.

**Established — the delay.**

```
q     = (uint16)builder.definition.workertime / 30       // integer division
delay = trunc( (double)resurrected.definition.buildtime * 0.3 / (float)q )
```

The `0.3` is a stored double and belongs to this state alone; it is not a
general construction-speed, repair, reclaim or capture multiplier. The delay is
decremented once per subsequent visit, i.e. once per tick.

**Established — the sub-thirty `workertime` edge.** A builder whose
`workertime` is below thirty makes `q` zero, the division produces an infinity,
and the retail conversion helper's out-of-range result has a zero low word,
which is the only word the caller consumes [01 §7]. The delay is therefore
**zero**, phase 4's `v == 0` test fires on the first visit, and the
resurrection completes immediately with no nano emitted at all. A negative
authored `buildtime` gives a negative delay, which never equals zero, so the
wait state repeats forever, emitting one segment per tick.

**Established — the transplant.** Phase 5 allocates a unit of the resolved
definition at the feature's recorded position and owner byte. If allocation
fails — slot pool or per-definition limit — it prints
`Unable to create any more units` on slot 7 and reschedules exactly 300 ticks
without advancing. On success it re-reads the terrain cell, refuses when the
cell's feature id is at or above the reserved-sentinel range, copies two fields
of the live feature record (a position word and a facing word) into the new
unit, removes the feature, emits the deterministic multiplayer state command
when the session requires one, and then sets the new unit's remaining fraction
to `0` and its health to `1`. The unit is therefore *finished* but at one hit
point; nothing repairs it as part of the order.

**Established — the caption ordering.** `Resurrection complete` is raised in
phase 6, i.e. **after** phase 5 has already allocated the replacement unit and
removed the feature, and **before** phase 6 allocates the successor order node.
The string-triage note that it is "emitted before the replacement object is
allocated" refers to that successor order node, not to the unit.

**Established — the two spellings.** The image contains two distinct failure
strings for this order: `Resurrection failed` for "there is no feature at the
recorded position", and `Ressurection failed` — with retail's doubled `s` — for
"the corpse name did not resolve to a unit definition". Both must be reproduced
verbatim, including the misspelling.

**Established — cost and randomness.** Resurrection carries no energy or metal
debit or refund; there is no admission call anywhere in the executor. Exactly
one bounded simulation draw is consumed, in phase 1, for the vertical component
of the walk target; it is an approach-point draw, not a placement draw, and it
is skipped without advancing the seed when the feature's height byte is below
two [01 §8]. Earlier text describing it as "placement jitter" placed it in the
wrong phase.

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
partially closed: burning instances reject reclaim and ignore further blast
(no applicable accumulation branch), blast damage accumulates on
instance-less cells against the definition's hit points, and a dead hop
(fringe whose anchor resolves empty) is the one unresolvable case the
blocking test lets through. The full precedence ordering when damage, burn
completion, and reclaim arrive in the same tick beyond those rules is not
fully closed.

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

**Established fact — shipped burn lifetimes are finite (asset census).** The
loader forces the runtime loop byte to zero for every resolved burn,
burn-shadow, death, and reclaim sequence, so the source GAF loop byte does
not control looping after the standard load path — that forcing is
executable-side and established. The lifetime list below is a **data-side
asset census** of the shipped GAF corpus, not executable control flow: all
79 shipped `seqnameburn` features resolve and their
burn GAF entries have finite lifetimes between 46 and 282 feature-phase visits;
distinct lifetimes observed are 46, 56, 58, 70, 84, 92, 114, 120, 122, 126, 141,
144, 159, 186, 192, 196, 200, 204, 208, 258, 264, 276, and 282 visits. Only
`Shrub1`, `Shrub2`, and `Shrub3` lack a `featureburnt` successor; the other 76
have one. Malformed or missing burn sequences and non-filename object/fire
combinations remain separate loader edges.

## Feature reproduction

**Established fact — per-tick reproduction walk (RNG-live even though
stock-inert).** The feature phase runs a reproduction walker every tick: a
rotating cursor is decremented per tick and wraps, visiting **one cell per
tick in descending order** (the wrap tick stores `W×H−1` and skips
evaluation, so the last cell is never scanned). A cell is eligible when its
anchor holds a feature index below the catalog sentinel and the cell has no
attached animation instance (GAF features at rest). For every eligible visit
the engine draws `simulationRandom(100)` — **the draw is consumed even when
the feature authors `reproduce = 0`** — and only if the draw is below the
definition's `reproduce` value does it continue: two more simulation draws
give `dx`/`dz` offsets in `±reproducearea/2`, the target cell must be
in-bounds, the source cell's occupancy word must be zero, the target cell's
feature word must be exactly empty, and the spawn then goes through the
ordinary placement service.

The shipped corpus authors `reproduce = 0` on every feature (with
`reproducearea = 6` where present), so **stock reproduction is inert as
placement, but the RNG draw is not**: one simulation draw per eligible cell
per tick is consumed regardless. A faithful implementation must keep the
walk and the draw-before-check order, or every later simulation draw after
the feature phase shifts. This walk is the sole per-tick feature-phase RNG
consumer beyond burning and meteors.

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
- The special-player discount family is identical at every positive
  production contribution in the ledger (passive makes, extraction, maker,
  wind, tidal, and the negative-energy-use refund) and at the two refund
  sites (factory cancel-current and the shared work helper reverse arm):
  selector 0 credits half, selector 1 credits seven tenths, any other value
  credits the full amount — reduced positive credits, never subtractions,
  with no inverted pairing.
- The feature phase's reproduction walk consumes one simulation draw per
  eligible cell per tick (draw before the `reproduce` comparison), even
  though every shipped feature authors `reproduce = 0`.
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

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body — most under `R-<id>` headings — and are not restated here.

**Correction (2026-08-28, RWU-00-5).** Roughly half of this tail's bullets
described closed work: the same-tick settlement order, the metal-maker stall
rule, negative ordinary economy fields, the unit-pool capacity, factory
order-node leaks on death or capture, terrain-metal sampling, the nano-segment
record constructor, and the special-player discount family were all listed as
"missing" while being fully established in the body. One was actively stale:
it said a `TODO(question)` remained for the nano-segment fade curve and the
logical-to-palette mapping, but [R-P0-06 §6] recorded on 2026-08-27 that both
were traced and that the framing was wrong — there is no fade curve, the
record is a particle emitter, and the contract lives at
[03 §5.5 "The nanolathe spray"] as `[R-P0-19-P]`. The closure narratives are
deleted here only; no finding left the document.

**Correction (2026-08-29, RWU-05-1).** Four bullets are re-cut against
[R-ECO-01]. The operation-byte bullet said the table's values were unknown
while the handler identities were established; the values are listed in
[R-ECO-01 §10], and what remains open is only whether the empty-named
descriptor holds index 0. The special-player bullet said state 2 was the
computer-policy state "by inference" and left the selector unnamed; both are
named in [R-ECO-01 §3], so the bullet narrows to control bytes 1 and 3. The
sibling-deadline bullet asked for consumers of both `WinLoseTime` and
`DisplayTimer`; `DisplayTimer`'s sole consumer is closed in [R-ECO-01 §6].
The bit-exact floating-point bullet asked for exceptional values, overflow,
signed zero and NaN; all four are stated in [R-ECO-01 §1] and [R-ECO-01 §5],
and only the untested exponent-range edge survives. One bullet is added: the
cloak gate's second status bit has no writer anywhere in the recovered image.

**Correction (2026-08-29, RWU-05-3).** The reversed-argument repair bullet is
removed: the variant is the `SelfRepair` order and its malformed-input behavior
is stated in [R-WORK-01 §3], so nothing about it is open. Four bullets replace
it, all raised by the work-handler pass and none of them a restatement of a
closed finding.

- Whether the build-assist approach radius's summand
  `footprintX × footprintX + footprintZ + footprintZ` is a retail defect or an
  intended asymmetry; the instructions are established and reproduced, only the
  intent is open · [R-WORK-01 §2] · manual retail observation of an assist
  approach against a footprint much longer in Z than in X.
- Which of the two nano-box Y forms the four VTOL work executors use — the
  majority form omits the first Y extent, `MobileBuild` and `HelpBuild` add it
  · [R-WORK-01 §8] · static trace of the VTOL work phases.
- Producers and names of the order-pump executor-flag bits that the work
  handlers test as terminate/interrupt, cancel, arrival-failure and the high
  bit unit reclaim and capture treat as terminal · doc 04 §3.1 · static trace
  of that word's writer set. Every handler-side consequence is established in
  [R-WORK-01 §1..§7].
- Meaning of the terrain-cell protection bit that feature reclaim's payout
  tests together with the feature definition's own protection bit; only the
  conjunction's effect (refuse and pay nothing) is established
  · [R-WORK-01 §5] · static trace of that cell bit's writer set.
- Response when a malformed factory product node bypasses queue preflight and
  reaches state 2 — cancellation, retry, or termination · "Factory queue" ·
  static trace. The current admission boundary records a bounded diagnostic
  and leaves the node unchanged.
- Whether the empty-named descriptor really holds operation byte 0: nothing
  was found that writes its name slot before the startup sort, but a writer
  there would shift every operation byte in [R-ECO-01 §10] by one
  · "Resource admission and carry" · static trace of that slot's writer set.
- Factory release and egress: the post-completion release target and order
  form, producer/product collision exemption, indefinitely-blocked-release
  policy, aircraft takeoff-before-rally handoff, and a no-stacking guarantee
  for same-pass coalesced products · [R-FAC-01][R-FAC-01B][R-FAC-01R] ·
  manual retail observation (one run with a blocked exit, a completed ground
  product, and a completed aircraft product, recording first movement,
  occupancy, and rally events). Marked `TODO(question)` at three sites; the
  authored `BUGGER_OFF` retry must not be generalized into a movement or
  collision rule.
- Semantic meaning of the game-ended flag bits and of the two mission-end
  predicates behind the confirmation delay; the bit patterns and the
  freeze-on-settlement effect are established · doc 08 · static trace.
- User-facing identities of player control-byte values **1** and **3**; value
  2 is the computer player and the 0.5 / 0.7 selector is the difficulty word,
  both closed by [R-ECO-01 §3] · "Unit instance economy state" · static trace.
- Names of the two fields in the settlement status pair, and the consumer of
  the `WinLoseTime` sibling deadline beyond its save key; `DisplayTimer`'s
  sole consumer is closed by [R-ECO-01 §6] · "Authoritative settlement order"
  · static trace.
- Whether any reader of the per-unit archived economy snapshots exists outside
  the ledger's own redistribution; bounded-negative in the reviewed image
  · "Authoritative settlement order" · static trace over the unrecovered
  regions.
- Meaning of the second status bit the cloak gate requires to be clear: it has
  no writer in the complete decompiled function set, so the term is inert and
  the behavior is unobservable · "Cloak debit" · static trace over the regions
  the export still misses. The rest of the gate, the truncation, and the
  inclusive affordability compare are closed by [R-ECO-01 §9].
- Whether any settlement intermediate can reach the range where the 53-bit
  precision control's wider exponent differs from a double's: no economy
  magnitude was found that does, and the claim is a stated edge rather than a
  traced one · "Two-stage settlement algorithm" · static trace, or manual
  retail observation with an authored extreme-cost probe. Exceptional-value
  behavior, signed zero, the truncation sites, and the float-versus-double
  widths are closed by [R-ECO-01 §1] and [R-ECO-01 §5].
- Sharing residuals: the source share-buffer refill rules, the state-2
  recipient discount scalar in the transfer helpers, and the names of the
  remaining status/alliance predicates · "Automatic transfer" · static trace.
  Threshold initialization, destination over-cap clamping, and packet
  application are closed.
- Writer or initializer of the per-definition limit field (absent from the
  reviewed corpus; the `-1` sentinel implies unlimited), and any consumer of
  the parsed-but-unread `norestrict` capability bit · "Unit creation and
  limits" · static trace over the unrecovered regions. Both are bounded
  negatives today.
- Upstream UI and network producers of the factory production interrupts —
  cancel-current (mask 2) and "Construction stopped" (mask 8) · doc 04 §3.3,
  doc 07 · static trace. Handler-side semantics for both are established.
- Trigger for the deadline catch-up burst, which is structurally present in
  the pre-gameplay setup pass with no natural trigger identified
  · "Authoritative settlement order" · static trace.
- Stockpile weapon-id-to-slot translation at node creation, and stockpile
  presentation beyond the refresh call · "Stockpile production" · static trace.
- Meanings of the carried live-record animation fields beyond position and
  velocity in the feature save records · doc 03 · static trace.
- Full same-tick precedence when several feature damage or successor causes
  land in one tick; the burning-rejects-reclaim, instance-less blast
  accumulation, and dead-hop fringe cases are closed · "Removal and successor replacement" ·
  static trace.
- Loader behavior for malformed or missing burn animations and for
  non-filename object/fire combinations · doc 02 §5, "Feature burning" ·
  static trace. Marked `TODO(question)`.
- Presentation clipping of sunken wrecks · doc 03 · static trace.
- Which statistic and UI fields derive from the economy, and which are
  authoritative totals versus presentation-only cached values · doc 07 ·
  static trace. The live-stock and pass-counter HUD readers are partially
  enumerated.
- Ordering when the feature catalog and the animation pool exhaust in the same
  tick · "Catalog construction" · static trace. Marked `TODO(question)`;
  exhaustion itself is an established silent failure with no retry.
- Wreck transitions at geothermal vent cells · "Geothermal requirement" ·
  static trace. Marked `TODO(question)`; vent persistence, multi-vent
  at-least-one satisfaction, and post-destruction persistence are established.
