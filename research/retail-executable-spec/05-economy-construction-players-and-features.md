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

A battle has ten fixed player slots, stored as the first ten rows of a player
table that is constructed with **eleven** rows (see below). A player slot
contains at least:

- whether the slot exists and participates in the battle;
- a stable slot number and network identity;
- player state and control type;
- current energy and current metal as single-precision floating-point values;
- current energy and metal storage capacities;
- resource-production, resource-consumption, and waste statistics;
- two alliance rows (own declarations, and others' declarations toward
  this slot) indexed by player slot [R-SHARE-01 §1];
- automatic energy, metal, and mapping-sharing option bits;
- two per-resource sharing thresholds, zeroed at battle setup and never
  written again in the reachable image [R-SHARE-01 §3];
- a contiguous range of unit slots owned by the player;
- an auxiliary player-level economy bucket;
- unit-limit accounting enforced only at nanoframe creation; queued products
  hold no reservation;
- optional mission-provided storage bonuses;
- side, team, and status information used when ownership changes.

**Established — the player table has an eleventh, never-occupied row
(2026-09-01, RWU-19-11).** When the battle-state block is allocated it is
zero-filled and the player-row constructor is then run **eleven** times over
contiguous rows of one fixed record size. The constructor writes: the
occupancy word to zero, the control byte to zero, four further words (the
LOS byte-grid pointer and its width and height among them) to zero, the
ally-group byte to `10` (the "no group" value every ally-group test
excludes), and allocates a zeroed side-definition record for the row. Rows
`0..9` are the ten slots above. Row `10` is reachable only by direct index —
it is the row a projectile's neutral side byte `10` selects
(`[06 R-DMG-01 §9]`) — and **nothing ever occupies it**: every walk of the
table during setup, battle, join and save restore covers ten rows (the row-10
base serves two loops as their end sentinel); the network join's free-slot
search scans rows `0..9` and, finding none, uses `10` as its "no free slot"
result and refuses rather than writing row 10; the seat-setup writer is only
called with lobby seat indices and with `0`/`1` for the two-seat mission
setup; the save restore's `Player%i` loop covers ten rows. So for the whole
of a battle row 10 holds occupancy `0`, control `0`, ally group `10`. An
implementation may represent it as an always-unoccupied eleventh row or as a
bound check that treats index `10` as "no player"; the two are
indistinguishable to every reader in the image. (`[06 §12.1]`'s `attacker
side != 10` guard before indexing the kill counters, and doc 08's lead-change
status line "attributed to slot 10", are consumers of the same convention.)
The tail item on which runtime row owns the eleven-byte `Players/Alliances`
save box (doc 08) is unaffected: that box is alliance bytes, not this row.

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

#### R-ECO-01 §11 — The discount's fourteen sites, one pairing, and a reduced credit rather than a debit [R-ECO-01] (2026-08-31)

**Correction to [R-ECO-01 §3].** §3 closes its site list with "Sites,
exhaustively:" and names nine — passive `energymake`, passive `metalmake`, the
extraction output, the maker output, the wind output, the tidal output, the
negative-`energyuse` refund, the reverse-construction metal refund, and the
direct production credit written at spawn. It is not exhaustive, and the
omission mattered twice: it is why an implementation could reasonably conclude
that feature reclaim is credited raw, and why a build-cancel refund could be
written with the wrong sign under a comment defending it.

**Correction to the first issue of this section.** This section first said
**twelve** sites drawn from **four** operand pairs, and reconciled that against
§3 as 5 + 4 + 2 + 1 = 12, minus the two feature sites and the one unit-reclaim
refund, leaving §3's nine. That arithmetic was wrong. The census behind it read
the values of only the operand pairs whose use counts appeared more than once in
a frequency table and never read two operands that appear exactly once each;
those two are a further pair of the same constants, and a second such pair was
missed the same way. Reading **every** distinct multiply operand in the image
against the two bit patterns gives **six** pairs and **fourteen** sites. The two
recovered sites are precisely the two construction refunds, which is why the
error hid the defect it was most needed to find.

**Established — fourteen sites, six operand pairs, one body.** The read-only
data holds six separate copies of the `(-0.7, -0.5)` double pair, each with the
two constants adjacent and the medium factor at the lower address. Their twenty-
eight multiply operands make fourteen sites, and every site is the same
sequence, differing only in which value it has just formed and which accumulator
it targets:

```
record = owner.playerRecord
if (*record == 0)            goto plain      // the record's first word: "exists"
if (record.controlByte != 2) goto plain      // 2 is the computer player
switch (difficultyWord):
  case 0:  production := (float32)( production - contribution * (-0.5) )   // easy
  case 1:  production := (float32)( production - contribution * (-0.7) )   // medium
  default: goto plain                                                       // hard
plain:     production := (float32)( production + contribution )
```

**Established — the pairing is uniform, at all fourteen.** In every site the
selector-0 branch is the jump target and takes the `-0.5` operand, and the
selector-1 fall-through takes the `-0.7` one. **No site inverts the pairing.**
This retires, at the executable rather than by argument, the standing claim that
some member of the family pairs them the other way round — a claim Nanolathe's
construction service carried as an instruction not to reconcile the two.

**Established — the scaled arm is a reduced CREDIT, not a debit.** The constants
are negative and the operation is a subtraction, so `production - contribution x
(-0.5)` **adds half** the contribution. A computer player on easy receives half
of each production contribution, on medium seven tenths, on hard all of it. An
implementation that reads the constant's sign alone and writes
`production += contribution x -0.7` inverts the whole effect: it charges the
player where retail pays. §3's prose already said "scaled by 0.5 on easy" and
never said "debited"; the arithmetic above is what settles it.

**Established — five of the fourteen, by their own contracts.** The
feature-reclaim payout's two additions (energy first, then metal, each through
its own copy of the ladder, gated on the BUILDER's player record —
[R-WORK-01 §5] step 4); the unit-reclaim death-side metal refund
(`(1.0f - victim.remaining) x victim.buildcostmetal`, gated on the killer's
record — [05 "Unit reclaim"]); the **build-cancel refund** in the factory
handler, which forms the same `(1 - remaining) x buildcostmetal`, truncates it
to an integer, credits the builder's metal accumulator and then sends the
cause-9 kill packet; and the **reverse-construction refund**, which negates its
value before the gate and credits the same accumulator.

**Established — §3 undercounts by at least four.** §3 names one construction
refund; there are two, in different handlers, each its own site. Adding the two
feature-reclaim sites and the unit-reclaim refund, at least four of the fourteen
are outside §3's list. The remaining nine sit in the two per-unit
production-gather passes.

**Unknown — which of those nine is which.** The one-to-one mapping of the nine
onto §3's named members (`energymake`, `metalmake`, extraction, maker, wind,
tidal, the negative-`energyuse` refund, the spawn credit) is not re-derived
here, and two of the nine credit a **player-indexed record** rather than a
unit's accumulator, which none of §3's named members obviously is. *Decider:*
name each of the nine by the value it forms and the accumulator it stores to,
and re-issue §3's list as fourteen rows. Until then §3's list should be read as
incomplete rather than as a closed census, and no site should be "harmonized"
away on the strength of it.

**Established — neither reclaim site tests the sign of the contribution.** §3's
prose says "every **positive** production contribution is scaled". Whatever
justifies that word, it is not a compare at the sites examined: the ladder is
entered on the player gate alone, and the negative-`energyuse` site negates its
value immediately before entering it, which is a deliberate negative
contribution taking the discounted path. **Unknown:** whether any site carries a
positivity test. *Decider:* a read of each site for a compare against zero
between forming the contribution and entering the ladder. Until then an
implementation should not add a positivity guard it cannot point at; no shipped
feature authors a negative pool [R-WORK-01 §5-A], so the reclaim sites are
unaffected either way.

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

#### R-ECO-01 §12 — The settlement status pair is the elimination test, and it is not persisted [R-ECO-01] (2026-09-02)

**Established, closing an open item; correction.** "Authoritative settlement
order" step 4 and [R-ECO-01 §1] step 4 carry the gate as "the status-pair
predicate holds (a nonzero halfword at one field **or** a zero word at its
neighbor — preserved literally; … no writer was found in the bounded scan)",
and the "Missing and unknown" list asks for "names of the two fields in the
settlement status pair". Both fields are named. The halfword is the player's
16-bit **live unit count** and the word is the 32-bit **units ever created**
count of [08 R-SKIR-01 §3] "Counters" — the same two record fields, whose
writers are the two unit allocators (both increment both) and the
kill-record handler (decrements the live count). The predicate "halfword
non-zero **or** word zero" therefore reads *the player still has a live unit,
or never had one* — the exact negation of the elimination test in
[08 R-SKIR-01 §3]. The settlement gate and the three other player walks that
repeat the predicate all skip an **eliminated** player; the settlement
deadline still advances for it (the advance precedes the gate chain). Nothing was
wrong in the literal predicate; what was missing was that it is not a
separate status at all. Nanolathe: evaluate "not eliminated" (live count
non-zero, or ever-created zero) where the gate stands, from the same two
counters the world already keeps, and carry no separate status pair.

**Established — neither counter is persisted.** The save writer's per-player
block enumerates its keys: the two stocks, the six cumulative totals
(produced, consumed and wasted, per resource), the two storage values and the
storage-bonus flag, kills, losses, the three deadlines (`UpdateTime`,
`WinLoseTime`, `DisplayTimer`) and the alliance block. Neither counter is among
them, and the restore path rebuilds both through the forced-slot allocator,
one increment of each per restored unit. A slot that had lost its last unit
at save time therefore reloads with both counters zero — "never created" —
and resumes settling. That is retail behaviour, not a Nanolathe omission.

**Established — the end-of-game arms are already placed.** The two
arm/decrement sites of "end-of-game freeze" are the local slot's 30-tick
deadline block (session kind 1: the victory predicate arms the win branch,
otherwise the defeat predicate arms the lose branch; kinds 2 and 3: the defeat
predicate behind the inactive-or-not-watching test) and the post-loop site for
sessions with no human participant ([08 R-SKIR-01 §3] "Defeat detection",
[08 R-TRIG-01]). The semantic names of the flag bits beyond `0x04` stay open
as doc 08 items; the gate itself needs nothing more from this document.

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

### Closed — the building validator's entry bounds and its two published outputs [R-ECO-02 §1] (2026-08-29)

The yard-map validator above is the single function every building placement
— builder order, factory exit, computer-player search, build ghost — passes
through. Three facts about its envelope were never stated; all are
**Established** (direct static).

**Entry bounds are strict on both edges.** Before any cell is visited the
validator requires, on the packed anchor cell `(x, z)` and the definition's
footprint `(fx, fz)` in cells:

```
x >= 1                     // signed 16-bit compare, x > 0
packed(x, z) > 0xFFFF      // unsigned compare of the whole word: z != 0
x + fx < mapWidthCells     // strict
z + fz < mapHeightCells    // strict
```

so a footprint can never cover column 0 or row 0, and its last covered
column and row are at most `mapWidth − 2` and `mapHeight − 2`; the map's
last column and row are unreachable for buildings just as they are for the
ground mover's rectangle ([04 R-COLL-01 §3]). The second test is an unsigned
compare of the packed word, which is `z >= 1` for the non-negative cells
every caller snaps to; a negative `z` (high bit set) would pass it and the
height test alike, so nothing in the validator bounds a negative row — no
caller produces one, since every caller snaps a world position that the
plot lookup has already accepted. Failing any of the four returns "not
placeable" with the two outputs below already zeroed.

**Two outputs are published through globals, not returned.** At entry the
validator zeroes a *site height* word and a *metal sum* word. During the cell
walk it adds every covered cell's plot metal byte to the metal sum — on
**every** cell, before and independently of that cell's yard bits, so the
sum is complete even on a footprint that is later rejected. The site height
is written only on the accept path, immediately before the success return,
with the `siteHeight` of the gate above. The metal sum has exactly two
readers, both one-line getters, and both are read by the computer player's
extractor placement only after a successful verdict ([08 "Placement root
and search helpers"]: "the blocker accumulates each footprint cell's metal
byte"); the site height is the value the build ghost and `MobileBuild`
read, as the section above already says. Neither global is reset anywhere
else: the last validation's values persist until the next call.

**The visibility gate is a map-object argument, not a mode number.** The
"mode" of [04 §6.4] is the presence of a map-object pointer: when one is
passed, the footprint centre `((fx + 2x)·8, (fz + 2z)·8)` in world units is
converted to that object's cell `(cx >> 5, (cz − h/2) >> 5)` with `h` the
terrain height at the anchor cell, the call fails when that cell is outside
the object's extent or the local player's bit is clear in its word, and the
per-cell occupancy rejections (bits 0–2) are then applied only while the
*visible* flag holds — the overlay byte at that cell under the overlay
global, else the same local-player bit (already known set). With a null
object the flag is simply true and every occupancy rejection applies.
[04 §4.7] owns the alias matrix; this paragraph only fixes what the argument
is.

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
   found in the bounded scan, so it is carried verbatim — the two fields are
   the live-unit and units-ever-created counters, so the predicate is "not
   eliminated", [R-ECO-01 §12]); the state byte is
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
   of either field was found — since named: the live-unit count and the
   units-ever-created count, i.e. "not eliminated", [R-ECO-01 §12]);
5. the control byte is **narrowed to 1 or 2** — control byte 3 traverses the
   deadline block, advances its deadline, and never settles;
6. the game-ended flag word's `0x04` bit is clear;
7. the end-of-game countdown halfword is **signed less than zero**.

**Established (2026-09-02, RWU-19-32) — what sits between the advance and
the gate chain.** For the **local** slot only, the end-condition block of
[08 R-TRIG-01 §6] runs there: the victory and defeat polls, the shared
countdown and the end latch. Its 30-tick due is this same `UpdateTime`
word — the trigger poll owns no deadline of its own, and `WinLoseTime` is
not it. The order within one due is therefore: advance; end-condition block
(local slot); gate chain; settlement; reference-slot tail. The end-of-game
freeze paragraph above ("one arm/decrement family sits inside the local
player's 30-tick deadline block") describes this block.

**Established — the HUD deadline is one tick stricter.** The sibling
`DisplayTimer` field is advanced by the same `+30` but by a **strict**
compare, `if (playerDisplayTimer < globalTick)`. Its consumer is the resource
bar ([R-ECO-01 §6]). `UpdateTime` uses `<=`, `DisplayTimer` uses `<`; the
difference is real and is not a transcription slip. This closes half of the
tail's "consumers of the `WinLoseTime` / `DisplayTimer` sibling deadlines
beyond their save keys": `DisplayTimer` has exactly one consumer, the resource
bar's rate latch. `WinLoseTime` is closed too (2026-09-02, RWU-19-31): a
bounded census of every access to that record field finds only the save
reader and writer — it has no gameplay reader ([08 "Player records"]).
*Previous text:* "`WinLoseTime` remains open." *Addendum (2026-09-02,
RWU-19-32):* the end-condition poll that an implementation might expect to
own `WinLoseTime` reads `UpdateTime` instead — see the paragraph after the
gate chain above.

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

There are exactly **three** predicates in the settlement's per-unit path, and
nothing else gates a contribution:

1. **Visit gate (Established).** The unit's status word carries the alive bit.
   That is the *only* test before the branch selection. There is no
   transported test, no disabled test, no paralyzed test and no under-attack
   test anywhere in the pass. *Correction:* the previous text said
   "Transported, dead, disabled, or otherwise ineligible units are skipped or
   handled by their relevant state branch." That was wrong. A transported
   metal maker, a paralyzed solar plant, and a unit in any other non-fatal
   state all keep contributing for as long as the alive bit stands; only the
   alive bit, the branch bit, the activated bit and the remaining fraction are
   read.
2. **Branch gate (Established).** The building/mobile branch is chosen by the
   spawn-time `bmcode`-is-zero status bit, and the chosen branch additionally
   requires the **activated** bit (buildings) or the activated bit **or**
   non-zero movement-mode bits (mobile units). This gates upkeep, the refund
   arm and all four generators — never the passive block. [R-ECO-01 §2] owns
   the branch bodies.
3. **Completion gate (Established).** The passive-make and storage block runs
   for every alive unit, on either branch and whatever its activated bit,
   when its remaining construction fraction **compares equal to zero**. The
   comparison is a floating-point equality, so `-0.0` passes and a NaN
   fraction fails.

So a nanoframe (remaining fraction `1` down to any value above zero) produces
no `energymake`/`metalmake` and adds no storage capacity, while an *activated*
building nanoframe whose definition authors `windgenerator` **does** contribute
wind energy, because the generator chain hangs off the branch gate and not the
completion gate. That asymmetry is retail behavior, not an imprecision here.

#### R-PROD-01 §1 — The economy fields: widths, defaults, and the reader census [R-PROD-01] (2026-08-29)

**Established — parse widths and defaults.** The unit-definition parser reads
these keys once, in this order, from the FBI `UNITINFO` section (`[fmt fbi]`
owns the file grammar, `[02 §5]` the typed-read helpers — the integer reader
is a C `atoi` and the float reader a C `atof`, each returning the stated
default when the key is absent):

| Key | Reader | Default | Stored as |
|---|---|---|---|
| `energymake` | float | `0.0` | single precision |
| `energyuse` | float | `0.0` | single precision |
| `metalmake` | float | `0.0` | single precision |
| `extractsmetal` | float | `0.0` | single precision |
| `makesmetal` | **integer** | `0` | **one byte** — the parsed integer is truncated to eight bits, so an authored `256` stores `0` and an authored `-1` stores `255` |
| `windgenerator` | float | `0.0` | single precision |
| `tidalgenerator` | float | `0.0` | single precision |
| `energystorage` | float | `0.0` | single precision |
| `metalstorage` | float | `0.0` | single precision |
| `cloakcost` | **integer** | `0` | converted to single precision |
| `cloakcostmoving` | **integer** | **the value just parsed for `cloakcost`** | converted to single precision |

The `cloakcostmoving` default is neither zero nor a constant: the parser
converts the freshly parsed `cloakcost` to floating point, stores it, converts
it **back** to an integer through the truncating conversion helper, and passes
that as the default. A definition authoring `cloakcost=25` and omitting
`cloakcostmoving` pays 25 while moving too. Because both cloak keys use the
integer reader, no fraction can reach the settlement's own truncation
([R-ECO-01 §9]), which is therefore idempotent on any FBI-authored value.

**Established (bounded negative) — `solarstrength` has no reader, and no
string.** The OTA key `solarstrength` is authored by every retail map. The
retail image contains **no defined string with that text at all**, so nothing
can look it up: it is not merely unread, it is unparseable by the shipped
executable. A solar collector's output is entirely its own `energymake`, and a
map cannot scale it. The bound is the complete defined-string table plus the
complete decompiled function set; decider for any reversal is a static trace
over the regions the export still misses. `[fmt ota]` carries the same finding
on the format side.

**Established — the gameplay reader census.** Each field was searched across
the complete decompiled function set. Hits inside the debug text overlay, the
frame-rate performance block and the DirectDraw cursor blitter are different
structures at coincident positions and are excluded.

| Field | Gameplay readers |
|---|---|
| `energymake`, `metalmake`, `energystorage`, `metalstorage`, `cloakcost`, `cloakcostmoving` | the settlement pass, and nothing else |
| `energyuse` | the settlement pass; the computer player's net-energy query (below) |
| `windgenerator` | the settlement pass; the wind-generator script notifier ([R-PROD-01 §3]); the net-energy query; the computer player's build-desirability table |
| `tidalgenerator` | the settlement pass; the net-energy query |
| `makesmetal` | the settlement pass; the computer player's metal-maker on/off task; the build-desirability table |
| `extractsmetal` | the creation-time extraction sampler ([R-PROD-01 §6]); the computer player's extractor placement. **The settlement never reads it** — it reads the rate the creator sampled |
| the unit's sampled extraction rate | the settlement pass only |

**Established — the computer player's net-energy query.** One shared helper
answers "what does this definition do to energy". Its result is a signed
per-settlement-pass rate, positive for consumption:

```
if (definition.energyuse != 0.0)     return definition.energyuse;
if (definition.windgenerator > 0.0)  return -(currentWindScalar * definition.windgenerator);
if (definition.tidalgenerator > 0.0) return -(mapTidalStrength  * definition.tidalgenerator);
return 0.0;
```

The first test is inequality against zero, not a sign test, so a definition
authoring a **negative** `energyuse` short-circuits and its generator terms are
never reached by this query — while the settlement, which tests the sign, pays
them anyway. Both callers are computer-player code; doc 08 owns what they do
with the answer.

#### R-PROD-01 §2 — The activated bit's writers, and what `onoffable` gates [R-PROD-01] (2026-08-29)

Which bit sets the activated bit is authored, and it is worth stating because
every generator except passive make depends on it (Established):

* the definition's `activatewhenbuilt` flag makes the completion path — and
  the pre-built creation path — set operational bit 0 immediately; a unit
  without it is created inactive;
* the `Activate` and `Deactivate` order handlers set and clear that bit
  through the transition service of [R-ECO-01 §8], but **each first tests the
  definition's `onoffable` flag and does nothing without it** (both handlers
  still report the order complete, so a non-`onoffable` unit silently accepts
  and discards the order);
* the COB `ACTIVATION` port and the order handlers of doc 04 write the same
  bit; that wider writer set is doc 04's (§4.4, §4.7).

`onoffable` therefore gates *toggling*, not producing: a definition that omits
it and omits `activatewhenbuilt` never activates and never runs any generator,
and a definition that omits it but authors `activatewhenbuilt` runs its
generators forever.

**Established — `energyuse` and `metalmake`/`energymake` on a switched-off
unit.** Because upkeep and all four generators hang off the branch gate and
the passive block off the completion gate, deactivating an `onoffable` unit
stops its `energyuse` charge, its extraction, its maker output and its wind or
tidal output in the same pass, and leaves its `energymake`, `metalmake`,
`energystorage` and `metalstorage` untouched. A metal maker switched off
therefore costs nothing and makes nothing; a solar collector switched off
still makes its full `energymake`, so `onoffable` on a pure passive producer is
a script-side affordance with no economic effect.

### Passive energy and metal

A unit that passes the completion gate adds its definition's `energymake` to
its own energy-production accumulator and its `metalmake` to its own
metal-production accumulator, **energy first**, each through the difficulty
discount of [R-ECO-01 §3] when the owner is a computer player. Neither add is
gated on the activated bit, on the branch bit, or on the maker-stall rule, and
`onoffable` has no effect on either: switching a solar collector off does not
stop its `energymake`.

These authored values are amounts **per settlement pass**, delivered once per
pass — once per ~30 ticks per player under ordinary play. The retail path
neither divides nor multiplies ordinary production and use values by thirty.

Negative authored energy use takes a distinct refund/production path. Special
player states can apply one of two executable-defined discounts through a
global mode selector: selector value 0 credits half the negated amount and
selector value 1 credits seven tenths of it (the engine multiplies the
negated amount by a negative half or seven-tenths double and subtracts the
product, so production grows by the scaled credit; an earlier revision that
described the special modes as subtracting the scaled amount was wrong at
byte level). Both halves of that rule are now named — the special state is the
computer player and the selector is the difficulty word — in [R-ECO-01 §3];
the previous sentence here, which said "the user-facing identity of those
player modes remains open (state 2 is the computer-policy state per the
AI-manager gate inference)", is superseded by that closure. Control-byte
values 1 and 3 remain unnamed.

### Wind generation

The battle holds five wind values, all global and all written only by the wind
phase: an integer **speed**, a 16-bit **heading**, an X and a Z **vector word**
(`[R-WIND-01]` owns those two), a single-precision **scalar**, and an integer
**changed flag**. A sixth value, the scalar's **divisor**, is written once.
The generator contract is one multiply:

```
energy production += currentWindScalar × definition.windgenerator
```

formed as one multiply and one add at the working precision of
[R-ECO-01 §1], through the difficulty discount of [R-ECO-01 §3] when the owner
is a computer player, and reached only through the strict generator chain of
[R-ECO-01 §2] — a definition that also authors `extractsmetal` or a non-zero
`makesmetal` never reaches it. It is **not** gated on the unit's own energy
admission; only extraction and the metal maker are.

#### R-PROD-01 §3 — The wind phase, its draws, and the generator notification [R-PROD-01] (2026-08-29)

**Established — cadence and the change detector.** The wind phase runs once
per tick from the phase dispatcher, **after** the unit sweep and after the
economy phase in the authoritative order of [04 §1.1]. It is a change
detector, not an updater:

```
if (windNextChangeTick >= globalTick) {      // unsigned compare; due only when strictly less
    windChangedFlag = 0
    return                                    // no draws, nothing else written
}
```

Two consequences an implementation must keep. First, the settlement of tick
*N* reads the scalar published by tick *N−1*'s wind phase, because the economy
phase precedes the wind phase inside a tick. Second, the unit sweep of tick *N*
observes the flag the wind phase of tick *N−1* wrote, so a re-roll on tick *N*
makes every wind generator issue its script pair exactly once during tick
*N+1*'s sweep, and tick *N+2* clears the flag again.

**Established — the change body, in order, with the draws.** When the deadline
is due, in exactly this sequence:

1. **Reschedule.** One draw from the **CRT** stream, then
   `windNextChangeTick += ((crtDraw × 10) / 0x8000 + 5) × 30`. The multiply
   and divide are signed 64-bit. `crtDraw` is `0 … 0x7fff`, so the quotient is
   `0 … 9` and the interval is **150 to 420 ticks** — five to fourteen seconds,
   quantized to 30-tick units.
2. **Speed.** `windSpeed = minWindSpeed + simRandom(maxWindSpeed − minWindSpeed)`,
   one draw from the **simulation** stream — but the bounded draw returns zero
   *without advancing the stream* when its bound is less than two (signed), so
   a map with `maxwindspeed − minwindspeed ≤ 1`, or an inverted pair, consumes
   **no simulation draw** and pins the speed at `minWindSpeed`. Doc 01 §7.3's
   census, which lists this as one draw unconditionally, should carry the
   exception.
3. **Heading**, only when the new speed is non-zero: `windHeading =
   simRandom(65536)`, a second simulation draw, stored into a 16-bit field —
   the full `0 … 65535` angle domain of [04 §2]. When the speed is zero the
   previous heading survives unchanged and no draw is taken.
4. **Vectors.** The X and Z words are recomputed from speed and heading as
   `−2 ×` the rounded fixed-point sine and cosine; `[R-WIND-01]` owns the axis
   assignment and the table.
5. **Scalar.** `windScalar = float32( windSpeed ÷ windDivisor )`, computed as
   an x87 divide with a 32-bit **integer** divisor operand at the working
   precision of [R-ECO-01 §1] and narrowed only by the store. Then, and only
   when the stored single is **strictly greater** than `1.0` (compared against
   a `1.0` double), the field is overwritten with the bit pattern for `1.0`.
   The divisor is a compiled-in **5000**, written once at battle entry
   immediately before the deadline is zeroed; it is not authored and no map
   can change it.
6. **Flag.** `windChangedFlag = 1`.

The change takes effect instantly — there is no interpolation and no ramp.

**Established — battle entry consumes no draws.** Battle entry writes the
divisor, zeroes the deadline, and calls the wind phase directly; the global
tick is still zero, so `0 >= 0` fails the strict due test and the phase returns
after clearing the flag. The first real re-roll is the first sub-tick.
*Correction:* the previous text here said "At battle setup the briefing seeds
strength as `CRT() % (max-min+1) + min` and a six-bit direction as
`CRT() & 0x3f`." Those two CRT draws are real, but they are **briefing-screen
display state** with no battle-side reader; [01 §7.3] retracted the battle
reading and doc 08's "Wind initialization" scopes them to the front end. The
battle's initial wind is drawn by the change body above, on the first tick.

**Established — where `minWindSpeed` and `maxWindSpeed` come from.** Both are
integers written once when the map and mission are applied, each by the same
rule, independently of the other:

```
minWindSpeed = (mission.minwindspeed >= 0 && terrainVersion is canonical) ? mission.minwindspeed : fallbackMin
maxWindSpeed = (mission.maxwindspeed >= 0 && terrainVersion is canonical) ? mission.maxwindspeed : fallbackMax
```

The mission fields are the OTA keys read by the integer reader with a parse
default of `0` inside their section and a construction-time sentinel of `−1`
that survives when the section is never reached ([fmt ota]). The fallback pair
depends on the terrain version: for the **canonical** version it is the
compiled constants **100** and **2000**; for the **legacy** version it is two
words of the legacy terrain header, and the legacy version *always* takes the
header pair because the version test fails before the OTA value is consulted.
So an OTA `maxwindspeed=0` on a canonical map is honored as zero (the speed
roll then pins at `minWindSpeed`, with no simulation draw), while an absent
settings section yields `100 … 2000`. The header word positions for the legacy
version are a format finding for `[fmt tnt]` (lane 02); their *meaning* —
wind range — is established here.

**Supported inference — the heading before the first non-zero roll.** The
heading, speed, and changed-flag globals have no writer outside the wind
phase, and the phase writes the heading only on a non-zero speed roll. On a map
whose first roll lands speed zero, the `SetDirection` argument is whatever the
global held after allocation. The inference is that it is zero because the
global block is freshly allocated; the open branch is the block allocator's
zeroing, and a static trace of that allocator settles it. It is unobservable in
energy terms (a zero speed yields a zero scalar), only in script arguments.

**Established — the script notification.** During the unit sweep, a unit whose
definition has `windgenerator > 0.0` **and** for which the global changed flag
is non-zero starts two deferred script calls back to back:

* `SetDirection` with the global wind heading, zero-extended from 16 bits into
  the argument word (so `0 … 65535`, never negative);
* `SetSpeed` with the global wind **speed shifted left by four** — that is,
  sixteen times the integer speed, not the scalar and not the vector.

Neither is issued on a non-change tick, and the producer has **no null-VM
guard**: a scriptless definition authoring `windgenerator` faults, the residual
[04 R-COB-01 §1] records. [04 R-CB-01 §5] owns the callback contract; this
section owns only the energy arithmetic and the phase.

**Correction — there is no "build-assist bonus".** The previous text ended
"the build-assist bonus is disabled when maximum wind is below half its
denominator". There is no such bonus anywhere in the economy. The behavior
that sentence garbled is a **computer-player** one: the per-definition
build-desirability table the AI builds zeroes a definition's score when it
authors a non-zero `windgenerator` and the map's `maxwindspeed` is below the
divisor divided by two (a signed integer divide of the compiled 5000, so
`2500`). It suppresses *building* wind generators on low-wind maps; it changes
no energy. Doc 08 owns the table.

The wind vector words also drive presentation drift (smoke, fire spread); those
consumers are doc 03's, under [R-WIND-01].

### Tidal generation

An eligible tidal generator contributes:

```
energy production += mapTidalStrength × definition.tidalgenerator
```

one multiply and one add at the working precision of [R-ECO-01 §1], through
the difficulty discount of [R-ECO-01 §3] for a computer player, reached only
as the **last** arm of the generator chain of [R-ECO-01 §2] — a definition
that authors `extractsmetal`, a non-zero `makesmetal`, or `windgenerator > 0`
never reaches its `tidalgenerator`. Like wind, it is not gated on the unit's
own energy admission.

#### R-PROD-01 §4 — Where tidal strength comes from, and the absent water gate [R-PROD-01] (2026-08-29)

**Established — the value.** `mapTidalStrength` is one global single-precision
value written once, when the map and mission are applied:

```
mapTidalStrength = (mission.tidalstrength < 0.0) ? 0.5 : mission.tidalstrength
```

The comparison is a strict signed float compare against zero, so `0.0` is
taken as authored and yields **zero tidal energy**, not the fallback. The
`0.5` fallback is reachable only two ways: the map authors a negative value,
or the mission object never reaches the OTA section that parses the key, in
which case its construction-time sentinel of `−1.0` survives. The parse
default within that section is `0.0` ([fmt ota]). Unlike wind and gravity, the
tidal value has **no terrain-file fallback** and no version test — the legacy
and canonical terrain versions take the same path.

**Established (bounded negative) — there is no sea-level, water, or terrain
gate.** The whole reader census for `tidalgenerator` and for the global tidal
strength is the settlement's generator chain plus the computer player's
net-energy query. Neither consults sea level, the cell's water flag, the
unit's height, or any terrain attribute. A tidal plant placed on dry land by a
mission or by a map editor produces exactly as much as one in the sea. If
retail refuses to *place* one on land, that refusal lives in the construction
placement rules and the definition's own footprint/`bmcode`, not here. Bound:
the complete decompiled function set; decider for any reversal is a static
trace over the regions the export still misses.

**Established — related keys the same load path writes.** The same settings
block, read in one pass, also supplies `minwindspeed`, `maxwindspeed` and
`gravity`; the wind pair and the gravity value take a version-dependent
fallback that tidal strength does not — the OTA value wins only when it is
non-negative *and* the terrain file is the canonical version; otherwise the
wind pair falls back to `100 … 2000` on a canonical map and to the legacy
header's own words on a legacy map ([R-PROD-01 §3]), and gravity falls back to
the legacy header's value or, when that is zero, to the compiled `0x1FDB`
([R-AIR-01], [03 §2.2]). [03 §2.2]
lists the tidal fallback beside the gravity one; the two are not parallel —
gravity's fallback is reached when neither source supplies a value, while the
tidal fallback is reached only on a negative or never-parsed value, with no
version test at all.

### Constant metal makers

`makesmetal` is stored as one **byte** ([R-PROD-01 §1]). The generator chain's
second arm tests that byte for **non-zero** — not for a positive float, not for
a specific value — and, when the unit's own energy demand was admitted this
pass, converts the byte's numeric value from an unsigned integer to floating
point and adds it to metal production, through the difficulty discount of
[R-ECO-01 §3]:

```
if (definition.makesmetal != 0 && admitted)
    metal production += (float)(unsigned byte)definition.makesmetal
```

So `makesmetal=1` contributes one metal per settlement pass — once per ~30
ticks per player under ordinary play — and `makesmetal=8` contributes eight.
An earlier revision read this as "a literal one"; [R-ECO-01 §2] records the
correction. The byte truncation at parse time is the only bound: an authored
`300` stores `44`.

The arm is reached only when `extractsmetal` is not positive; a definition
authoring both makes metal only through extraction ([R-ECO-01 §2]).

Energy consumption is a separate authored active-use demand (`energyuse`), and
the two are coupled through the stall rule below rather than through any
conversion ratio. The engine has no metal-per-energy constant.

#### R-PROD-01 §5 — Upkeep timing, the maker byte, and the absent `metaluse` key [R-PROD-01] (2026-08-29)

**Established — when `energyuse` is charged.** There is no per-tick upkeep.
`energyuse` is read exactly once per settlement pass of the owning player —
at the player's own deadline, once per ~30 ticks under ordinary play, in the
stable unit-slot order of [R-ECO-01 §2] — and only for a unit on its branch
gate: an activated building, or a mobile unit that is activated **or** has
non-zero movement-mode bits. The amount is added to the unit's energy
*requested* accumulator, and to its *accepted* accumulator only when its energy
carry is not positive; the arithmetic, the negative-`energyuse` refund arm, and
the carry semantics are [R-ECO-01 §2]'s and [05 "Resource admission and
carry"]'s. Nothing is prorated: a unit that is activated for one tick of a
thirty-tick window pays the whole `energyuse` if it happens to be activated on
the deadline tick, and nothing if it is not. The same is true of every producer
in this section — `energymake`, `metalmake`, wind, tidal, maker and extraction
are all sampled at the deadline tick and never integrated across the window.

**Established (bounded negative) — there is no `metaluse`.** The retail image
contains no defined string `metaluse` (the only string of that shape is a GUI
gadget name), the unit-definition parser reads no such key, and the settlement
has no metal-upkeep arm: the only per-unit metal *demand* the settlement knows
is construction's, and a unit's own definition cannot author a standing metal
drain. A negative `metalmake` is the only authored way to make a unit consume
metal each pass, and it is charged through the passive block (completion gate,
no branch gate, discounted for a computer player — see [R-ECO-01 §5] for how a
negative production term settles). Bound: the complete defined-string table
plus the complete decompiled function set.

**Established — the maker byte is the whole selector.** The generator chain's
second arm tests the `makesmetal` byte for non-zero only ([R-ECO-01 §2]).
`makesmetal=1` and `makesmetal=200` differ only in the amount added; neither
has a different gate, and an authored `256` — which stores `0` — makes the
definition fall through to the wind and tidal arms as if the key were absent.

**Established — the computer player toggles makers, and it is the only
automatic toggler.** No engine path switches a metal maker on or off for
economic reasons. The computer player's economy task does, once every 30 ticks,
for each alive building it owns whose `makesmetal` byte is non-zero: it clears
the activated bit when the owner's live **energy stock is at most twice its
live metal stock**, and otherwise, when the owner's last completed pass had a
positive net energy (archived produced minus archived requested) **and** a
simulation draw `random(5)` is non-zero — a four-in-five chance, one draw per
candidate on that arm only — it sets the activated bit. Both writes go through
the transition service of [R-ECO-01 §8], so they raise `Activate`/`Deactivate`.
Doc 08 owns the task; it is recorded here because it is the only automatic
writer of a maker's activation and because it consumes a simulation draw
inside a per-30-tick pass.

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

Every plot cell carries one **unsigned metal byte**. Extraction reads it once,
when the extractor is created, and never again.

#### R-PROD-01 §6 — Seeding the metal byte, sampling the footprint, and the accumulator [R-PROD-01] (2026-08-29)

**Established — where the byte comes from.** Map loading allocates the plot
grid and seeds every cell's metal byte before anything else writes it:

```
seed = (mission.SurfaceMetal >= 0 && terrainVersion is canonical) ? mission.SurfaceMetal : 0
for every cell:  cell.metalByte = (byte)seed          // narrowing store: 300 seeds 44
```

The mission field's construction-time sentinel is `−1` and the parse default
inside its OTA section is `0`, so an absent section leaves the sentinel and
seeds zero, and a present section with the key absent seeds zero as well.

Then the terrain attribute pass runs, and this is the one place the two
terrain versions differ:

* **canonical version** — the four-byte attribute record supplies the height
  byte and the feature reference only. It never touches the metal byte, so
  every cell keeps the uniform seed. There is **no per-cell metal raster**:
  bounded negative over the loader and over the shipped 171-map corpus, whose
  fourth attribute byte is uniformly zero and is never read as metal.
* **legacy version** — the attribute record is eight bytes and the pass copies
  its **byte 6** into the cell's metal byte (height from byte 0, feature
  reference from byte 2). The legacy version is also the one for which the
  uniform seed was forced to zero above, so on a legacy map the per-cell bytes
  are the whole story. `[fmt tnt]` documents only the canonical four-byte
  record; the eight-byte legacy record is a format finding for lane 02.

**Established — the sampling walk.** At unit creation, and only when the
definition's `extractsmetal` is **strictly greater than zero**, the creator
walks the unit's stamped footprint rectangle: outer loop over the footprint's
Z extent starting at the unit's stamped Z cell, inner loop over its X extent
starting at the stamped X cell. For each coordinate it resolves the plot cell
through the bounds-checked lookup — `0 ≤ x < mapCellWidth` and
`0 ≤ z < mapCellHeight`, else no cell — and **off-map coordinates contribute
nothing at all**, not even the `+1`. For an in-bounds cell:

```
accumulator = (uint16)( accumulator + cell.metalByte + 1 )
```

The accumulator is **sixteen bits** and wraps modulo 65536. Practical bound:
each cell adds at most 256, so wrapping needs Σ(byte + 1) ≥ 32768 — 128 covered
cells at metal byte 255, or 32768 covered cells at metal byte 0, which is a
182×182-cell footprint. No shipped definition comes close; the wrap is a stated
edge, not an observed one, and it is stated because the next step reads the
accumulator **signed**.

**Established — the rate, and its evaluation order.** The rate is stored on the
unit as:

```
unit.extractionRate = float32( ( definition.extractsmetal × (float)(int32)(accumulator << 16) ) × 2⁻¹⁶ )
```

evaluated left to right at the working precision of [R-ECO-01 §1], with `2⁻¹⁶`
a double constant and the only narrowing at the store. The shift and the
reciprocal cancel algebraically, so the value is `extractsmetal × Σ(byte + 1)`
— but the intermediate is loaded as a **signed** 32-bit integer, so an
accumulator of `0x8000` or more yields a **negative** rate, and a negative rate
is a negative metal contribution every pass thereafter. An implementation that
sums into a wider accumulator, or converts unsigned, diverges only in that
corner.

**Established — creation-time only.** The sampler's three call sites are
three unit-creation paths (one of which has no recovered static caller of its
own); a reference census finds no other caller. The rate is
computed once and never recomputed. The settlement reads the stored rate, not
`extractsmetal`. Consequently:

- a cell whose metal byte is zero still contributes one to the footprint sum;
- a larger footprint samples more cells;
- two extractors may sample overlapping cells unless placement and occupancy
  prevent the overlap;
- later terrain deformation, feature changes, or a mission rewriting the
  surface metal do not change an already-sampled rate;
- feature reclaim metal and terrain extraction metal are unrelated fields.

**Established — the script notification.** Immediately after storing the rate,
and only when the unit has a script VM, the creator starts a deferred
`SetSpeed` whose argument is the accumulator **sign-extended from sixteen
bits** — the summed metal-map value under the footprint, one metal byte plus
one per covered cell, in metal-map units. That is what stock extractor scripts
use to size their animation rate. [04 R-CB-01 §5] owns the callback contract.
Unlike the wind pair, this producer *is* guarded against a missing VM.

**Established — the settlement side.** While the extractor is on the building
branch, activated, and its own energy demand was admitted this pass, the stored
rate is added to metal production through the difficulty discount of
[R-ECO-01 §3] and under the maker-stall rule stated with the metal makers
above.

### Storage capacity

At each settlement pass the engine rebuilds player capacity from scratch: both
capacity fields are zeroed at the top of the pass, every alive unit that passes
the **completion gate** adds its definition's `metalstorage` and
`energystorage` — metal first, two plain single-precision adds, no integer
intermediate anywhere — and a bonus term is added once at the end when the
player's bonus-enable flag is set. [R-ECO-01 §4] states the accumulation, the
bonus, and where the `200` floor really lives (on the bonus operands at the
moment they are written, not on capacity), and is the authority for all three.

Two points this section owns:

* **Eligibility is the completion gate and nothing more (Established).** The
  storage adds sit inside the same block as passive make, so they require only
  the alive bit and a remaining construction fraction equal to zero. They do
  **not** require the activated bit, they do not care which branch the unit
  took, and `onoffable` does not affect them: a deactivated, transported, or
  paralyzed storage unit still contributes its full capacity.
* **Reader census (Established).** `energystorage` and `metalstorage` have no
  gameplay reader outside this accumulation — not the HUD, not the computer
  player, not the build UI. Everything that displays capacity reads the
  player's recomputed capacity fields ([R-ECO-01 §6]).

Capacity is single-precision state. It is not an integer total. Destroyed,
unfinished, or ineligible storage units cease contributing at the next pass
because that pass rebuilds the sum from zero — capacity is never decremented.

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
6. if unaffordable, takes the failure transition without a partial payment;
7. if the gate is **not** due — the request bit is clear, the decloak-forced
   bit is set, or the reveal deadline has not been reached — takes the same
   failure transition (bit 2 cleared) with no cost selection and no compare
   ([R-ECO-01 §9] "every exit", 2026-09-02).

Because units are visited in stable order, simultaneous cloak costs are
sequential: an earlier slot can make a later slot fail during the same pass.
The debit executes at the settlement cadence — at most once per 30 ticks per
owning player, in the settlement's stable unit-slot order; authored cloak
costs are per-settlement-pass amounts like every other authored economy
field.

#### R-PROD-01 §6-A — The intermediate's shape, restated for the implementer [R-PROD-01] (2026-09-02)

The pre-closure prose of "Terrain metal extraction" said retail "performs the
intermediate sum with fixed-point-shaped integer arithmetic and then converts
it to a single-precision value" and that "an exact compatibility mode must
preserve that conversion and rounding order", and a code marker still cites
that sentence as unresolved. It is resolved by §6 above (re-verified
RWU-19-22, Established): the sum is a **sixteen-bit** accumulator of
`metalByte + 1` per in-bounds footprint cell; the rate is
`float32( ((float)(int32)(accumulator << 16)) × extractsmetal × 2⁻¹⁶ )`,
evaluated left to right at the x87 working precision with the only narrowing
at the store. Because `accumulator << 16` is exact as a floating value and
`extractsmetal` is a single, the double product rounds once at the store to
the same single as `float32(accumulator) × extractsmetal` for every
accumulator below `0x8000`. There is no other rounding to preserve. The
single corner is the sign: at `0x8000` and above the shifted word is loaded
as a negative 32-bit integer and the rate goes negative; a wider or unsigned
accumulator diverges there and nowhere else, and no shipped footprint reaches
it.

#### R-PROD-01 §7 — Cost selection, the player gate, the can-cloak capability, and the unconditional transition [R-PROD-01] (2026-08-29)

**Established — the player gate, exactly.** The whole cloak block — gate,
payment and transition — is skipped for a unit whose owner's **record-exists
word is non-zero and whose control byte is 3**; it runs for every other
visited unit. The record-exists clause is inert in practice (a unit is only
visited through an existing player's slot list) and is recorded so an
implementation does not add a separate existence test. Control byte 3 remains
unnamed (tail).

**Established — which units can request cloak at all.** The unit-definition
parser derives a per-definition *can-cloak* capability bit at parse time as
`cloakcost > 0.0` — a strict floating-point compare on the value already
converted from the integer reader, evaluated after both cloak keys are read.
The `Cloak_On` and `Cloak_Off` order handlers test that bit before writing the
cloak-requested status bit ([R-ECO-01 §9]). Consequently a definition with
`cloakcost` absent, zero, or negative can never carry the cloak-requested bit,
and the settlement's "cost of zero debits nothing" path is reachable only
through `cloakcostmoving=0` on a definition whose stationary cost is positive
— the moving unit then cloaks for free while its stationary cost is positive.

**Established — the selection, and why the conversion is a no-op in practice.**
The cost is `cloakcostmoving` when the unit's movement-mode bits are non-zero
and `cloakcost` otherwise; the test is on the two runtime movement-mode bits of
the status word, not on a definition flag, so the same unit switches between
the two costs as it starts and stops. Both keys are parsed by the **integer**
reader and only then converted to single precision ([R-PROD-01 §1]), and
`cloakcostmoving` defaults to the parsed `cloakcost` rather than to zero. The
settlement's truncation toward zero ([R-ECO-01 §9]) therefore cannot change any
FBI-authored value: it is there for a fractional cost the FBI reader cannot
produce. The one behavior it does produce — "a fractional cost below 1
truncates to zero and debits nothing" — is unreachable from authored content.

**Established — the transition call is unconditional.** Every visited unit that
passes the player gate above reaches the transition service with the
cloak bit, whatever the gate decided: the service is called with *set* on a
successful payment and with *clear* on every other outcome — cloak not
requested, deadline not due, or payment refused. Because the service suppresses
notifications when the bit does not change ([R-ECO-01 §8]), this is silent for
the overwhelming majority of units, but it means a unit that stops being
cloak-requested has its cloak bit cleared by the next settlement pass without
any other code doing it.

**Established — no draws, no discount.** The cloak debit consumes no random
draws from either stream, and the difficulty discount of [R-ECO-01 §3] does not
apply to it: a computer player pays cloak upkeep in full.

**Established — the full debit predicate.** The debit block runs when the
unit carries the **cloak-requested** status bit, a second status bit is clear,
and the unit's per-unit cloak payment deadline is due — the gate is bit set
**and** bit clear **and** deadline due. An earlier reading that OR-ed a
cooldown bit into the gate was falsified at byte level, and the
owner-control-byte-3 condition that would suppress the whole block is inert
during live play because the settlement caller excludes control byte 3. The
per-unit deadline itself is written by **ten** order-handler sites as the
global tick plus 150 (`SelfRepair`, `RepairUnit`, `RepairUnitNoMove`), 300
(`MobileBuild`, `HelpBuild`, `Reclaim`, `Resurrect`, `VTOL_Reclaim`) or 900
(`Capture`, `ReclaimUnit`), and by two writers outside the order system — the
sensor phase's proximity breach (`+ 90`, [03 R-VIS-01 §6]) and the projectile
fill on every shot (`+ 600`, [06 §4.1]) — all into the **same** word, a later
write always replacing an earlier one (no maximum). Idle cloaked units, which
nothing stamps, therefore pay every pass from the first. **Correction
(2026-09-02, RWU-19-26).** The previous text said "written by nine handler
sites as the global tick plus 150, 300, or 900 (repair, build/get-built/
resurrection, and capture/reclaim respectively)". A store-by-store census of
the field found ten handler sites, not nine — `Resurrect` and `VTOL_Reclaim`
both stamp `+ 300`, while neither `GetBuilt` nor `BuildingBuild` writes the
field at all — and the sentence omitted the two non-handler writers that
share the word. The full census, with readers, is [03 R-VIS-01 §6]
"Writer census of the shared deadline" and [04 R-ORD-01 §5] "The reveal
stamp".

#### R-ECO-01 §9 — Cloak gate, conversion, and the second bit [R-ECO-01] (2026-08-29)

**Established, and a correction to the reach claim.** The first gate bit —
the **cloak-requested** status bit, bit 11 of the unit status word — has
exactly three writers in the recovered image, and one gameplay reader:

* the **unit constructor** seeds it from the definition's `init_cloaked` flag.
  The constructor first clears the bit in a masked store of neighbouring
  bits, then, in the same masked store that copies the definition's two
  standing-order fields into the status word, ORs in `init_cloaked` shifted to
  bit 11. Both creation paths in the image (the shared create service and
  the network-packet create) run this constructor, and nothing after it —
  neither the create service's own tail nor the **build-completion service**
  — touches
  bit 11, the instance cloaked bit, or `init_cloaked` again. An
  `init_cloaked=1` unit is therefore cloak-requested from the tick it is
  placed, as a nanoframe, with no player order ([03 R-VIS-01 §6]);
* the **`Cloak_On`** order handler sets it and the **`Cloak_Off`** order
  handler clears it, each behind the definition's derived can-cloak capability
  (`cloakcost > 0`, [R-PROD-01 §7]) and otherwise a one-instruction set/clear
  ([R-ECO-01 §10] gives their operation bytes);
* the save-game restore rebuilds it from the persisted status word (doc 08).

The only reader is this gate. The two cloak orders are the runtime togglers,
so any unit whose definition carries the cloak capability pays cloak upkeep
for as long as the player leaves cloak on, whether or not it was authored
`init_cloaked`. The first previous text — "the init-cloaked instance bit
(seeded once at spawn from the definition's `init_cloaked`; no runtime
toggler exists in the reviewed image) … authored reach is exactly the mine
family; commanders, spies and snipers carry cloak costs but never enter this
block" — was wrong about the togglers and the reach.

**Correction (2026-09-02, RWU-19-26).** The 2026-08-29 text of this paragraph
over-corrected: it said "The first gate bit is not seeded from
`init_cloaked` … It is cleared at spawn along with its neighbours … 
`init_cloaked` is a separate definition flag bit; its consumer is the
initial-posture path, not this gate." That was wrong. The constructor's
clearing store is followed, in the same function, by the masked store that
copies `init_cloaked` into bit 11 — the seeding is Established at instruction
level, and [03 R-VIS-01 §6] had it right. There is no "initial-posture path":
the phrase came from doc 04 §3.8's description of the completion transition's
capability-bit-24 arm, which is `isfeature` (death cause 7 and the death
latch — [04 R-SPEC-01 §12]), not `init_cloaked` (bit 4); doc 04 §3.8 is
corrected in place. Implementation consequence: the cloak-requested state is
seeded from `init_cloaked` at creation, the completion transition writes
nothing cloak-related, and nothing else consumes `init_cloaked` — the
visibility predicate reads only the instance cloaked bit that this gate's
transition service sets ([03 §3.2], [R-ECO-01 §8]).

**Established — the debit is not gated on completion.** The cloak block sits
after, and outside, the settlement's `remaining fraction == 0` test that
guards the producer and storage contributions: an unfinished unit whose
request bit is set is gated and charged exactly like a finished one. For an
`init_cloaked` definition that means the nanoframe pays from its first
settlement pass and, when the owner can pay, is cloaked while still being
built.

**Correction (2026-09-02, RWU-19-29) — the second bit is not inert.** The
previous text said: "**Established (bounded negative) — the second bit is
inert.** The status bit whose clearness the gate also requires is *read* only
here. A search of the complete decompiled function set found no writer that
sets it, and the spawn initialiser preserves rather than sets it, so the term
is always satisfied in practice. … Its intended meaning is **Unknown** —
decider: static trace over the regions the export still misses." That search
predated the sensor-phase trace and was wrong. The bit is the **decloak-forced**
latch of [03 R-VIS-01 §4] and [03 R-VIS-01 §6]: the sensor phase's first pass
clears it on every live unit every tick, and its proximity-breach pass sets it
again on the same visit that stamps the `tick + 90` deadline. Its meaning is
therefore Established. Because the breach writes the deadline on the same
visit, the bit's own contribution to the gate is observable only on the breach
tick itself (where the deadline term also fails); an implementation that
carries the breach as the deadline alone diverges by nothing, but the term is
real and a clone should keep it.

**Closed — every exit of the cloak block writes the instance bit (2026-09-02,
RWU-19-29).** Established at instruction level. The block's own entry test —
the owner record's existence word is non-zero and its control byte is not the
observer value 3 — is already implied by the settlement gate chain (steps 1
and 5 of "Settlement cadence"), so for every player that settles, the block
runs for every unit of the slice. Inside it the three gate terms are tested in
order — request bit set; decloak-forced bit clear; `currentTick >= deadline`
as an unsigned, inclusive compare — and **every** failure and every success
ends at the same transition-service call ([R-ECO-01 §8]) with bit 2 as the
mask and the outcome as the selector:

| Exit | Bit 2 (instance cloaked) |
|---|---|
| (a) request bit clear — after `Cloak_Off`, or never requested | cleared |
| (b) decloak-forced bit set — the breach tick | cleared |
| (c) deadline not yet reached — after a stroke's `+150`/`+300`/`+900`, a shot's `+600`, or the breach's `+90` | cleared |
| (d) gate due, integerized cost `<=` live energy stock | **set** (stock debited, request recorded) |
| (e) gate due, cost `>` stock | cleared (no partial payment) |

No exit leaves the bit alone, and the block never reads the bit. The pseudocode
above shows only arms (d) and (e); arms (a)–(c) are the `else` of the gate,
with no cost selection and no compare. WU-19-92's implementation assumed
exactly this as a Supported inference — its comment reads: "gate not due at
all — CLEAR bit 2. Supported inference, not a traced arm: `TODO(question)`:
whether the settlement clears the instance bit on a not-due pass, or leaves it
and the reveal happens elsewhere — decider: a trace of the debit block's exit
paths." The decider is met; the inference is confirmed and the marker can
close. Consequences a clone must preserve: `Cloak_Off` decloaks on the
owner's *next settlement pass*, not on the order; a reveal stamp decloaks on
the next pass, so a unit can stay hidden up to one settlement interval (30
ticks) after its last stroke or shot; and since the transition write is
unconditional but its edge notifications are suppressed when the byte did not
change ([R-ECO-01 §8]), arms (a)–(c) and (e) on an already-visible unit raise
nothing and send nothing.

**Established — the instance bit's writers, complete.** Bit 2 of the
operational byte has exactly one *deciding* writer: this block, through the
transition service — the only caller in the image that passes the service a
mask of bit 2 alone. Every other writer copies a byte: the unit constructor
zeroes the whole operational byte at creation; the save-game restore writes
the persisted byte (doc 08); the ownership-transfer service (reached from
`Capture`'s completion and from the network transfer paths) copies the old
instance's whole operational byte onto the replacement unit through two
transition calls — set the bits that are set, then clear the complement — so
a captured cloaked unit arrives with bit 2 set and pays from its new owner's
next pass; and the network state appliers write the received byte on
remote-controlled slots (doc 08). The reveal-stamp sites ([03 R-VIS-01 §6]
census), the sensor breach and first pass, `Cloak_On`/`Cloak_Off`, and the
build-completion service write the status word or the deadline and never the
operational byte.

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

#### R-PROD-01 §8 — Producer draw census and phase placement [R-PROD-01] (2026-08-29)

**Established.** The complete random-draw census of the producer paths this
section owns, for the determinism ledger of [01 §7.3]:

| Site | Stream | Draws |
|---|---|---|
| wind phase, deadline not due | — | none |
| wind phase, deadline due | CRT, then simulation | one CRT draw always; one simulation draw only when `maxWindSpeed − minWindSpeed ≥ 2` (signed); one further simulation draw only when the rolled speed is non-zero |
| extraction sampler (unit creation) | — | none |
| settlement: upkeep, generator chain, passive block, storage, cloak debit | — | none |
| computer player's maker toggle task | simulation | one draw per candidate maker that reaches the switch-on arm (doc 08) |
| computer player's extractor placement | simulation | one draw per placement attempt on an extracting definition (doc 08) |

The producer arithmetic itself is draw-free and discount-only: the only
non-determinism a producer can introduce is the wind phase's own three draws,
and the phase order fixes their position — the wind phase runs after the unit
sweep and after the economy phase of the same tick ([04 §1.1], [R-PROD-01 §3]),
so within one tick the settlement always sees the previous tick's scalar and
the sweep always sees the previous tick's changed flag.

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
- optionally clamp the amount to the source's live stock;
- do nothing for a zero amount;
- debit the source through the corresponding storage object;
- credit the destination's mirror-bucket production slot, with the computer
  player's difficulty discount applied once at credit;
- when requested, emit a deterministic multiplayer command describing source,
  destination, resource kind, and amount.

Energy and metal use parallel but separate paths. The exact arithmetic,
widths, and the producer that drives them are in [R-SHARE-01 §2] and
[R-SHARE-01 §5] below.

**Correction (2026-08-29, RWU-05-4).** The first bullet used to read "clamp
the amount to the source's available share buffer". There is no share buffer:
the clamp compares the requested amount against the source's live stock
(single precision) and takes the smaller. The "share-buffer refill rules" the
tail listed as open therefore do not exist — nothing refills, because nothing
is drawn down except the stock itself.

#### R-SHARE-01 §1 — Control bytes, the two alliance rows, and the alliance predicate [R-SHARE-01] (2026-08-29)

**Established — the three control-byte identities.** The player slot's
control byte is `1` for a locally controlled human, `2` for a computer player,
and `3` for a remote peer. The skirmish setup path writes `1` for the local
human seat and `2` for each computer seat; the network join path writes `3`
for every peer it admits; and the packet sender refuses to emit unless the
source's control byte is `1` or `2` and the destination's is `3`. This closes
the tail's open item on the identities of values `1` and `3` ([R-ECO-01 §3]
had already pinned `2`). Slot initialization also copies the control byte
into the slot's option record as a "kind" byte whenever it is not `3`, so a
remote peer's kind byte is whatever the lobby synchronized (`1` human, `2`
computer) rather than the control byte.

**Established — two alliance rows per slot.** Each player slot carries two
eleven-byte rows indexed by player slot number:

- **row A** — this player's declaration toward each other slot (non-zero =
  allied);
- **row B** — each other slot's declaration toward this player, mirrored.

Slot initialization zeroes both rows and sets the self entry of each to one.
The alliance writer takes `(from, to, value, force)`: when `from` is local
(control `1` or `2`) it writes `from.A[to] = value`, and additionally
`from.B[to] = value` when `to` is a computer player, or a remote peer whose
kind byte says computer, or when `force` is set; when `to` is local it writes
`to.B[from] = value`, and additionally `to.A[from] = value` when `to` is a
computer player or `force` is set. A computer player therefore reciprocates an
alliance instantly; a remote human's reciprocal declaration arrives by packet.
The skirmish setup writes row A for every pair of seats sharing an ally
symbol ([08 "Skirmish configuration"] owns `ALLY%d` and the lobby side); the
mission setup writes only the self entries.

**Established — the predicate each simulation consumer uses.** There is no
shared "is allied" function; each consumer indexes a row directly:

| Consumer | Test | Notes |
|---|---|---|
| Automatic resource sharing ([R-SHARE-01 §3]) | `source.A[candidate] != 0` | one-directional: the giver's own declaration |
| Sensor phase allied disjunct ([03 §3.2 R-VIS-01 §4]) | `owner.A[viewer] != 0` and an option-word bit with no writer | the owner's declaration toward the viewer |
| Victory test (doc 08) | requires both `local.A[i]` and `local.B[i]` | mutual alliance |
| All-enemies-eliminated test (doc 08) | `local.A[i] != 0` skips the slot | one-directional |
| ALLIES screen (doc 07) | displays `B << 1 \| A` per row | presentation only |

Nothing in the simulation reads row B for sharing; the giver shares with
anyone it has declared alliance to, whether or not the declaration is
returned.

#### R-SHARE-01 §2 — The two transfer helpers, exactly [R-SHARE-01] (2026-08-29)

**Established — signature and gates.** Each helper takes `(source slot,
destination slot, amount as single precision, debit flag)`. Either slot equal
to `10` (the "no slot" sentinel) returns without effect. When the debit flag
is set and the source's live stock of that resource is strictly less than the
amount, the amount becomes the live stock. An amount exactly equal to `0.0`
(after the clamp) returns without effect; a negative amount is not rejected
(see the edge below).

**Established — debit.** When the debit flag is set the source's storage
object is asked to pay: if `amount <= live stock` (inclusive, single
precision) the live stock is reduced by the amount and the source's
mirror-bucket "requested this pass" slot for that resource is increased by
it, and the helper returns success; otherwise nothing is paid and it returns
failure. **The transfer helper ignores that result** and credits regardless.
With the clamp above, a debit-flag caller can never reach the failure branch
(after the clamp `amount <= stock` holds), and callers without the flag never
debit; the doc's earlier "whether normal callers can reach a failed deduction"
question is closed — they cannot.

**Established — credit and the recipient discount.** The credit is written to
the destination's **mirror-bucket production slot** for that resource
([R-ECO-01 §2] describes the bucket; [R-ECO-01 §5] folds it at the
destination's next settlement pass), never to the live stock. When the
bucket's owner record exists and its control byte is `2` (computer player),
the credit is scaled by the difficulty selector of [R-ECO-01 §3]:

```
selector 0:  slot = (float)( (double)slot - (double)amount * (-0.5) )
selector 1:  slot = (float)( (double)slot - (double)amount * (-0.7) )
otherwise:   slot = slot + amount                       (single precision add)
```

The two scaled forms multiply the single-precision amount by a
**double-precision** constant (`-0.5`, `-0.7`), subtract that product from the
slot value in double, and store single. The unscaled form is a plain
single-precision add. The mirror-bucket fold during settlement is an unscaled
sum, so a computer recipient is discounted exactly once, at credit, not again
when the bucket is folded. Because the credit lands in the production slot,
the shared amount is subject to the destination's capacity clamp and waste
accounting at its next settlement ([R-ECO-01 §6]), not at the moment of
transfer.

**Established — packet.** With the debit flag set, the helper then emits the
sharing packet (type `0x16`, subtype `1` energy / `2` metal, source and
destination network identities, the clamped amount). The packet emitter itself
refuses unless the session is networked, the source is local, and the
destination is a remote peer, so in a single-player battle the emission is a
no-op ([R-SHARE-01 §4]).

**Established — the negative-amount edge.** A negative amount passes every
gate: the clamp only lowers a too-large positive amount, the zero test is an
exact compare, the debit test `amount <= stock` is true, so the source's stock
*increases* by the magnitude and the destination's production slot *decreases*
by it. Whether the SHARE screen's integer parser can produce a negative value
is **Unknown** (decider: static trace of the shared string-to-integer helper's
sign handling); the helpers themselves do not guard.

### Automatic transfer

Every sixty authoritative ticks, the automatic-sharing dispatcher considers
metal and energy independently for the local player. The corresponding
option-word bit must be set and the local player's current stock must strictly
exceed its per-resource sharing threshold. The thresholds are separate fields
from capacity; they are zeroed at battle setup and nothing in the reachable
image writes them afterwards, so in play the condition is simply "stock
strictly greater than zero".

**Correction (2026-08-29, RWU-05-4).** This section previously said the
thresholds "are written once at battle setup from capacity". That was wrong:
the per-player battle initializer stores zero in both threshold fields, and
the only stores that ever derive a threshold from capacity sit in a console
command handler region that no reachable code references (the `SetShareMetal`
/ `SetShareEnergy` family, whose strings are also unreferenced). The exact
consequence is in [R-SHARE-01 §3].

The dispatcher scans player slots from zero through nine. Every eligible
allied candidate with lower current stock replaces the previous candidate, so
the last qualifying slot wins. Alliance is checked through the giver's row A
([R-SHARE-01 §1]).

**Correction (2026-08-29, RWU-05-4).** The earlier text said "the exact
semantic names of all status and alliance predicates remain partly
unresolved" and omitted a gate that changes the feature's reach entirely: a
candidate must have control byte `3` — a **remote peer** — and its option
record's kind byte must be `1` (human). Computer players and the local human
are never candidates. In a single-player skirmish or mission the automatic
share options are therefore inert; only a networked session with an allied
remote human can receive an automatic transfer.

For metal, the transfer is:

`min(destination capacity - destination current, (source current - source threshold) × 0.33333334)`

For energy, it is:

`min(destination capacity - destination current, (source current - source threshold) × 0.5)`

The amounts are clamped by the destination capacity gap so a transfer never
overfills beyond capacity. The helpers of [R-SHARE-01 §2] then debit the
source and credit the destination and emit the packet; receivers apply the
packet through the same helpers without the debit flag ([R-SHARE-01 §4]).

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

#### R-SHARE-01 §3 — The automatic dispatcher, exactly [R-SHARE-01] (2026-08-29)

**Established — where it runs.** The tick executor calls the dispatcher once
per sub-tick for the **local player's slot only**, after the twelve phases of
[01 §4] and before the presentation flush, and only when the sub-tick is an
advancing one. Inside, the dispatcher returns immediately unless the session
flag word's "networked session" bit is set; that bit is raised when the
front end finds a live network session. Everything below is therefore
multiplayer-only behavior; Nanolathe's single-player build reproduces it as a
no-op.

**Established — the 60-tick resource pass.** When `tick mod 60 == 0`, for
metal then energy:

1. Source gate: option-word bit `1` (metal) / bit `2` (energy) set, and
   `source.threshold < source.stock` (single precision, strict).
2. Candidate scan, slots `0..9` ascending, candidate `c` qualifies when all of:
   the slot exists; control byte in `{1, 2, 3}`; the slot's own index is not
   `10`; the slot is not eliminated (`live unit count != 0` or `total units
   ever created == 0`); **control byte equals `3`**; the candidate's option
   record kind byte equals `1`; `source.A[c] != 0`; and
   `candidate.stock < source.stock` (single precision, strict). The last
   qualifying slot wins.
3. If a candidate was chosen:
   `amount = min(candidate.capacity − candidate.stock,
   (source.stock − source.threshold) × k)` with `k = 0.33333334` (metal) or
   `0.5` (energy), the multiply in single precision; then the resource's
   transfer helper is called with the debit flag set.

The threshold fields are both zero throughout play (see the correction
above), so step 1's compare is `0 < stock` and step 3's product is `stock × k`.
No random draw is consumed.

**Established — the 450-tick mapping pass.** When `tick mod 450 == 0` and
option-word bit `5` is set, every slot passing the same candidate predicate
(plus `source slot != 10`) is sent a type `0x16` subtype `3` packet carrying
the source and destination network identities. The consumer is
[R-SHARE-01 §6].

**Established — what the option bits are.** The simulation reads exactly
three bits of the slot's option word for sharing: bit `1` share metal, bit `2`
share energy, bit `5` share mapping. Their only live writers are whole-word
copies on the lobby/network synchronization paths (doc 08 owns them); the
bit-level toggles (`Toggled ShareMetal to: %s` and siblings) live in the
unreferenced console region noted above.

#### R-SHARE-01 §4 — The receive side and its phase [R-SHARE-01] (2026-08-29)

**Established.** Sharing packets are consumed by the network drain — phase 1 of
the sub-tick ([01 §4]), before any unit work. For packet type `0x16` the
drain resolves both carried network identities to slots (a miss yields the
sentinel `10`, and either sentinel drops the packet), then dispatches on the
subtype dword: `1` → the energy helper, `2` → the metal helper, both with the
carried single-precision amount and the **debit flag clear** — the receiver
credits the destination's bucket but never debits (the sender debited
locally); `3` → the mapping-grid merge of [R-SHARE-01 §6]. No comparison,
threshold, or alliance test is applied on receipt; the doc's earlier
"overwrite-sync with no comparison" wording described this.

**Established — the sender's packet gate.** The packet emitter is a no-op
unless the session flag word's networked bit is set, the source slot's control
byte is `1` or `2`, and the destination's is `3`. Consequently in a
single-player battle every share, manual or automatic, is applied exactly once
by the local helper call and never re-applied by the drain.

#### R-SHARE-01 §5 — The SHARE screen producer and unit sharing [R-SHARE-01] (2026-08-29)

**Established — controls.** `SHARE.GUI` binds a player list (`PLYRLIST`), two
text fields (`METAL`, `ENERGY`), two check controls (`SHARUNIT`, `MAPINFO`), a
confirm button, and `CANCEL` (back to the previous screen). Confirming
resolves the selected list row to a network identity and then to a slot; the
target must exist, have control byte `1`, `2` or `3`, have an assigned slot
index, not carry the rule word's defeated bit, and not be eliminated. **There
is no alliance test** — a player may share with an enemy.

**Established — amounts.** *(Corrected 2026-08-29: `METAL`/`ENERGY` are
gadget-kind-4 **sliders**, not text fields, and the 64-bit integer this
paragraph attributed to a string-to-integer parse is the slider read-back's
`ftol` — [07 R-HUD-03 §9] gives the knob/range arithmetic; the amount
semantics below are unchanged.)* Each slider's read-back value is truncated to
a 32-bit integer and converted to single precision; fractions cannot be
entered. The
metal helper is called first, then the energy helper, both with the local
slot as source, the resolved target, and the debit flag set — so each amount
is clamped to the local live stock ([R-SHARE-01 §2]). Zero fields are no-ops.

**Established — unit sharing.** With `SHARUNIT` checked, the current
selection of the local player is gathered and each selected unit is handed to
the ownership-transfer routine (the same one capture uses, [R-WORK-01 §6])
with the target's player record, **except** units whose status word's low two
bits equal `2`, units with a non-zero transport-attachment reference in either
of the two attachment slots, and units whose definition index is in the
`Commander` category bitset. The transfer re-allocates the unit in the
target's pool slice through the allocator of [R-SHARE-01 §8], so it fails
silently when the target's slice is full or the definition's limit is
reached. What the status word's low two bits denote is doc 04's field
(**Unknown** here; decider: doc 04's status-word census).

**Established — map information.** With `MAPINFO` checked, the mapping-grid
merge of [R-SHARE-01 §6] is applied locally from the local slot to the target
and a subtype `3` packet is emitted.

**Supported inference — timing.** The screen handler runs from the window
message pump, which the main loop services between executor calls
([01 §2.3]); the transfer therefore lands between sub-ticks, never inside a
phase. A static trace of the pump/executor interleaving would settle it.

### Sensor sharing

Every 450 authoritative ticks, the share-mapping option emits a packet for
every allied remote human ([R-SHARE-01 §3]); the receiver merges the sender's
**mapped-memory word grid** bits into its own. Nothing about line of sight,
radar, or the visibility mode word is transferred.

**Correction (2026-08-29, RWU-05-4).** The earlier text said "the visibility
document defines what state is shared". The consumer is now traced
([R-SHARE-01 §6]) and it touches only the mapping (explored-memory) word grid
of [03 §3.1]; doc 03's sensor phase does not read any shared state
([03 §3.2 R-VIS-01 §7]). The old `ShareRadar`/`ShareLOS` console strings are
unreferenced.

#### R-SHARE-01 §6 — The mapping-grid merge [R-SHARE-01] (2026-08-29)

**Established.** Given `(source slot, destination slot)`, the merge walks the
mapping word grid of [03 §3.1] — `(map cell width × map cell height) / 4`
sixteen-bit words, the signed division truncating toward zero — and for every
word whose source bit is set, ORs in the destination bit. Bits are
`1 << (slot & 31)` within a sixteen-bit word, so slots `0..9` map to bits
`0..9`. It is idempotent, copies only in one direction, and consumes no random
draw. It is reached from the 450-tick emitter's packet (phase 1 of the
receiver's sub-tick), from the SHARE screen's `MAPINFO` control (locally and
by packet), and from nowhere else.

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
instant of nanoframe creation**, and only when the definition carries its
creatable bit. Each definition carries a per-definition limit field whose
sentinel of -1 means unlimited; when a finite limit is set, the allocator
counts instances with a matching definition index **within the owning
player's contiguous instance slice only** — there is no cross-player
accounting site — and refuses creation when the count has reached the limit.
Queued factory products hold no reservation: a full queue simply fails each
allocation attempt and retries. Capture and unit sharing transfer ownership
through the same allocator and are refused by the same gate. Failure surfaces
as the production handler's `Unable to create any more units` message plus an
exact 300-tick retry. Exhausting the player's instance slice produces the
same failure path. The exact gate is [R-SHARE-01 §8].

**Correction (2026-08-29, RWU-05-4).** Three claims of the earlier text are
withdrawn. (1) "No parser key or reviewed initializer writes the per-definition
limit field, so stock defaults come from outside the reviewed corpus" — the
definition parser initializes the field to -1 for every definition, and the
multiplayer lobby's restriction dialog is its only other writer
([R-SHARE-01 §9]). (2) "`norestrict` … no reviewed consumer reads it" — two
functions of the restriction dialog read it, to exclude the definition from
the restrictable list; the simulation never reads it ([R-SHARE-01 §9]).
(3) The "numeric pool capacity" paragraph below said the pool is sized from
the catalog definition count and that the mission `maxunits` field has no
allocator reader; both are wrong ([R-SHARE-01 §7]).

**Established fact — numeric pool capacity.** The physical unit pool is
sized once at battle setup as `(per-player unit limit) × 10 + 1` records —
one contiguous slice of exactly `limit` records per player slot, plus slot
zero as the null identity. The per-player unit limit comes from the mission's
`maxunits` in campaign mode and from the lobby in skirmish and multiplayer
mode; it is the only numeric cap on live units, and the allocator's
first-free scan over the slice is what enforces it.

#### R-SHARE-01 §7 — The unit pool is sized by the per-player unit limit [R-SHARE-01] (2026-08-29)

**Established — sizing.** At battle entry the engine copies the session's
per-player unit limit (an unsigned sixteen-bit value) into its runtime copy
and computes `record count = limit × 10 + 1`. It allocates that many unit
records (labelled `UNIT MEMORY`), zero-fills them, stamps each record's own
index and a definition pointer to the catalog base, and assigns slot `i`
(`0..9`) the records `[limit × i + 1, limit × (i + 1)]` inclusive — exactly
`limit` records per slot regardless of how many slots participate. Record `0`
is the null identity. Two side tables — the hot-unit list (`20` bytes per
entry) and the hot-radar-unit list (`100` bytes per entry) — are sized
`limit` entries each. In multiplayer mode the ten slots are first sorted by
network identity before slices are assigned; otherwise they keep slot order.

**Established — the limit's sources.** The session limit is a single global
written by three producers:

| Session mode | Writer | Value |
|---|---|---|
| Campaign / mission | OTA loader, `GlobalHeader` | `maxunits`, default `200` when absent |
| Skirmish | lobby entry copies the lobby value | `totala.ini [Preferences]` `UnitLimit` (not a registry value; [01 R-PLAT-01 §3], [02 §3]), default `250`, clamped to `[20, 500]` (values above 500 become 500, below 20 become 20) |
| Multiplayer | lobby entry copies the lobby value, then overrides it from the host's option record | the host's synchronized unit-limit word |

The registry read and the clamp happen once at lobby entry; [08 "Skirmish
configuration"] owns the ladder of selectable values and the option-record
synchronization (RWU-08-2; this unit records only the simulation-side
consumers).

**Established — reader census of the runtime limit.** Besides the pool sizing
above: the lobby panel prints the number (doc 07 owns the panel); two unit-slot arithmetic
helpers convert a global record index to a slice-relative one by `index mod
limit`; the computer player's construction scorer compares
`(limit >> 1) < live unit count` ([08 R-AI-01 §13]) and one of its cadence
helpers divides by the limit (doc 08); and two option-block bulk copies carry it
with its neighbours. No consumer compares the limit against anything the
allocator does not already enforce through the slice size.

#### R-SHARE-01 §8 — The allocator gate, exactly [R-SHARE-01] (2026-08-29)

**Established — inputs.** The allocator takes the owning player slot, the
definition index (sixteen-bit), the world position triple, a "finished"
flag, the two orientation bits to seed the status word, and an optional
preferred record index (`0` = none). It reads the definition's creatable bit
and its per-definition limit field, the player's slice bounds, and every
record's definition index within the slice.

**Established — order of tests.**

1. `definition index == 0` → refuse (returns the null unit).
2. Definition's creatable bit clear → refuse.
3. If the definition's limit is not `-1`: count the records in the slice
   (inclusive of both ends) whose definition index equals the requested one;
   refuse when `count >= limit` (signed compare; a limit of `0` refuses
   always).
4. With no preferred index: scan the slice from its first record and take
   the first whose definition index is `0`; none free → refuse.
   With a preferred index: take exactly that record if it lies inside the
   slice (unsigned compare against both bounds) and its definition index is
   `0`; otherwise refuse. The preferred path is used by the save-game restore.
5. On success: write the definition index, run the instance initializers
   (base state, script instance, order state, movement state), attach the
   3D model when the definition's `bmcode` byte is `1`, seed the status word's
   low two bits from the orientation argument, register the record with the
   unit grid and the hot lists, and when "finished" is set apply the
   completion-time effects (activation of always-active definitions and the
   flags doc 04 §3 owns); then increment the player's sixteen-bit **live unit
   count** and its 32-bit **units-ever-created** counter, and register the
   record with the session object.

The count in step 3 is a census of **records whose definition index is set**:
it includes nanoframes, completed units, and dead units whose teardown has
not yet cleared the index — teardown zeroes the index and decrements the live
count ([04 §3] owns when teardown runs; the elimination test that fires when
the live count reaches zero is [08 R-AI-01 §13]). Nothing else consults the
limit field.

**Established — the failure sites and their retry.** The allocator's null
return is handled by:

| Caller | On failure |
|---|---|
| `BuildingBuild` production state 2 ([05 "Factory production lifecycle"]) | caption `Unable to create any more units` (category 7), node wait of exactly 300 ticks, node flag bit 1 set, result 2 |
| `MobileBuild` (site placement) | same caption, 300-tick wait, result 2 |
| `Resurrect` completion ([R-WORK-01 §7]) | same caption, 300-tick wait, result 2 |
| the VTOL mobile-build variant | same caption, result 8, **no wait** |
| ownership transfer (capture [R-WORK-01 §6], unit sharing [R-SHARE-01 §5]) | no transfer; the unit stays with its owner |
| start-unit and mission spawns | no unit; the spawner continues |

The VTOL row is a **Supported inference**: the handler fragment is reached
through a jump table that lies inside the `VTOL_MobileBuild` handler's address
span and its body mirrors `MobileBuild`'s; a static trace of that jump table's
owner would settle the name. The caption string is exactly
`Unable to create any more units`; the success caption is
`Starting construction`.

**The creatable bit — identity, default, writers and readers (Established,
2026-09-02, RWU-19-32).** The bit step 2 tests is bit 23 of the
definition's first definition-flags word — the *compatible* flag of
[02 R-CAT-01 §4]. It is a runtime bit: no FBI key parses into it and no
accessor reads it as authored data ([02 "Unit record"]).

*Default.* The catalog loader sets it on the `None` sentinel (index 0) and
on every FBI record whose `Version`, `Copyright` and loose-file gates pass;
a record that fails a gate has it clear and is compacted out at the end of
discovery, and the catalog compiler compacts again (stable remove from
index 1) before sorting ([02 R-CAT-01 §5] step 3). The record-move helper
both compactions use carries the bit with the record. A compiled catalog
therefore carries the bit on **every** record, sentinel included.

*Writers after discovery.* Exactly two: the campaign `UseOnlyUnits` loader
of [08 R-ENTRY-01 §2] step 4 (kind 1 only — clear on index 1 upward, then
set on the first record whose `unitname` matches each `[name]` section) and
the multiplayer restriction apply of §9. The campaign's per-mission unit
lists *are* the `UseOnlyUnits` files; no mission script, trigger,
progression record, AI routine or save item writes the bit, and the save
file does not persist it.

*Readers.* The two compactions, this allocator's step 2, and one
per-definition re-parse helper that nothing calls. The build menus, the
side `CANBUILD` lists, the download-menu compile, the computer player's
class routine and the mission spawner do not read it — they see only the
compacted table.

*Consequence.* Because the battle-entry catalog compile (world-rebuild
step 13, [08 R-ENTRY-01 §3]) runs after the `UseOnlyUnits` loader (§2
step 4) and compacts bit-clear records out, a kind-1 restriction manifests
as absence from the catalog — no unit index, no menu button, no spawn by
name — and step 2 never sees a clear bit for a non-zero index in any
single-player battle; it is reachable only for a bit cleared after the
compile, and no single-player writer does that. The table is rebuilt from
the FBI files by the front end's pre-load state before every battle, so
the removal lasts one battle. An implementation should therefore apply
`UseOnlyUnits` as a catalog filter at battle entry — remove, re-sort,
renumber — and keep the allocator's bit test as the cheap invariant it is
in retail, not as the mechanism. The ordering of the multiplayer apply
against that pre-load rebuild was not traced (out of scope).

#### R-SHARE-01 §9 — The per-definition limit field, its writers, and `norestrict` [R-SHARE-01] (2026-08-29)

**Established — default.** The unit definition parser stores `-1` (unlimited)
into the per-definition limit field of every definition it parses, and sets
the definition's creatable bit for every definition it keeps (definitions
failing the parser's validation lose the bit and are compacted out of the
catalog before any battle; doc 02 owns that validation). In every single-player
session these are the final values. *Correction (2026-09-02, RWU-19-32):*
the previous sentence said "In every single-player session these are the
final values" — not for the creatable bit in a kind-1 battle: a mission's
`UseOnlyUnits` file clears it on every non-sentinel record and re-sets it per
listed name before the battle-entry compile removes the cleared records
([08 R-ENTRY-01 §2] step 4; the creatable-bit paragraphs of §8). The limit
field is untouched by that path.

**Established — the only other writer is the multiplayer restriction
dialog.** The multiplayer lobby's `RESTRICTIONS` button constructs a
restriction tree seeded with one node per catalog definition (keyed by the
definition's identity word) whose limit value is `-1`, or `0` when the
definition's `wacky` flag is set. The `RESTRICT2.GUI` screen edits nodes: its
`COUNT` field accepts a value below `101` as the count and anything else as
`No Limit` (`-1`); closing the screen marks each node "restricted"
(`enable = 1`) when its row value is non-zero and "unrestricted" (`enable =
0`) when the row value is zero, skipping definitions that carry
`norestrict`. When the front end leaves the multiplayer lobby for the battle
it applies the tree to the catalog: for a definition with a node,
`creatable bit = (enable != 0 && synced != 0)` and `limit field = node
limit`; for a definition without a node, `limit field = 0` and the creatable
bit is cleared. The `synced` word is written by the lobby's restriction
synchronization acknowledgement (multiplayer transport, out of scope). The
apply step runs only under the front end's multiplayer-lobby flag, so a
skirmish or campaign battle never executes it.

**Established — `norestrict` reader census.** The capability parses into bit
15 of the definition's second flag word (both parser entry points write it).
Exactly two readers exist, both in the `RESTRICT2.GUI` screen: the picture-list
builder skips such definitions, and the close handler skips them when marking
nodes. The allocator, the settlement, the AI, and every other simulation
consumer never read the bit. A `norestrict` definition therefore keeps its
seeded node (limit `-1`, or `0` for `wacky`) and its `enable` word at the
seed value — an effect the lobby side owns and this document does not state.

**Established — consequence for the single-player build.** With no
restriction tree, the allocator's step 2 always passes and step 3 is skipped
for every definition; the only limit is the slice size of [R-SHARE-01 §7].
An implementation that exposes per-definition limits must treat them as
multiplayer-lobby data with the semantics above, not as an FBI key.

#### R-SHARE-01 §10 — The computer player's gate is not the definition limit [R-SHARE-01] (2026-08-29)

**Established.** The computer player's per-type limit test
([08 R-AI-01 §12]) reads its strategic state's per-type limit table — filled
by the AI profile's `limit` directive — and passes when the entry is `-1` or
when the type's current count is strictly below it. It never reads the
definition's limit field or the session's per-player limit; the per-player
limit reaches the planner only through the half-capacity scoring term of
[08 R-AI-01 §13]. Every unit the computer player builds still passes through
the allocator of [R-SHARE-01 §8], so the slice size and (in multiplayer) the
restriction limits bind it exactly as they bind a human.

### Closed — the second exhaustion-caption site is `VTOL_MobileBuild` [R-ECO-02 §4] (2026-08-29)

**Established.** The tail of this document carried, since [R-SHARE-01 §8], a
Supported inference that the jump-table fragment printing `Unable to create
any more units` with result 8 (abandon) and **no** 300-tick wait belongs to
`VTOL_MobileBuild`. Reading the fragment settles it: it is the in-reach phase
body of the VTOL twin — the same site test and the same allocation call as
`MobileBuild`, but it abandons instead of holding, and it inserts the
upper-case `GETBUILT` node the air twin uses (the ground handler inserts
`getbuilt`). [04 R-ORD-02 §2] states the phase with exactly that outcome
("not created → status 7 `Unable to create any more units`, abandon"). The
caption table of [R-WORK-01 §1] therefore reads: `MobileBuild`,
`BuildingBuild` and `Resurrect` reschedule 300 ticks; `VTOL_MobileBuild`
abandons. The tail bullet is removed.

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

**Correction (2026-09-02, RWU-19-18) — interrupt producers, both located.**
This paragraph previously read: "The bodies of the two construction interrupts
are established — cancel-current computes its refund and issues the cause-9
kill while construction-stopped decrements count and stays — but the upstream
producers of interrupt masks 2 and 8 sit in the UI and network command layers
and remain unidentified. Their effects must be preserved behind those masks
without inventing a producer." The bodies stand; the producers are not in the
UI or network layers at all. **Mask 2 (cancel-current)** is never raised into
the pending word: it is the cleanup notice of [04 R-ORDER-02 §2], delivered by
every record-removal path (the counted cancel, the non-queued purge, the pump's
own removals, death teardown) to a record whose *dynamic* gate still holds bit
1 at removal — which is why the factory's static mask carries no bit 1 and the
handler arms it dynamically while a product is attached. **Mask 8
(construction stopped)** is the *target removed* notice of [04 R-ORD-01 §6]:
the factory record binds its product as the record's target reference when
the product is created (the `Starting construction` visit) and releases the
reference at completion or cancel, so the notice fires — once, through the
pump, since the static mask does carry bit 3 — exactly when the product under
construction is destroyed. No other raiser of the pending word carries bit 3
([04 R-ORD-01 §0], [04 §3.3]). Established.

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

**Established — the engine never reads `BUGGER_OFF` [04 R-COB-05] (2026-08-30).**
The bit the yard scripts toggle here has no simulation consumer: a complete
census of the second state byte's bit 3 finds only the COB get and set port
arms, the creation clear of that byte's low nibble, and the save writer. The
assertion above is therefore entirely script-internal bookkeeping — the flag
tells the engine nothing, and clearing it on the successful branch changes no
engine state beyond the interface-refresh bit the set-port arm raises. This
does not weaken the warning below; it strengthens it. A reimplementation must
attach no movement, collision, scatter or crowd behavior to the flag. The
observable "units in the way of a factory exit" behavior is owned by the
state-2 exit retry, the yard-close admission gate, and the ordinary blocked
mover [04 R-FAC-02 §5][04 R-FAC-02 §6].

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

### Correction — a no-rally product column is a route-publication defect, not retail [R-EGRESS-02] (2026-08-31)

**Established by direct trace of the route follower's two entry points.** This
section reverses the conclusion of `[04 R-EGRESS-01]` and closes the question
its composition left open. It is written here because doc 05 owns the factory
production lifecycle; `[04 R-EGRESS-01]` and `[04 R-PATH-01 §8]` need the
pointer added by doc 04's owner.

**What `[04 R-EGRESS-01]` said, and which part is wrong.** Its step 3 read:
"A short route is replaced by a straight line at that point. Both point-count
gates of the route-acceptance rule require **three or more** stored points; a
one- or two-point route skips to the synthetic fallback, which overwrites the
route with the unit's own position and the goal point." Its conclusion read:
"This is **not** a deadlock and does not block the factory: each product clears
the exit cells before the next is allocated." The gate arithmetic is right; the
place it is applied is wrong, and the conclusion that follows is wrong.

**Established — the acceptance rule belongs to the goal installer, and only to
it.** The ground route follower has two distinct entry points and they do
different things:

* The **goal installer** (the follower method an order handler's phase 0
  reaches when it hands over a new goal object — `Park`'s rectangle installer
  among them) cancels any in-flight search for this follower, releases the
  previous goal payload with `0x80`, adopts the new goal, sets *wants-repath*,
  and then runs the three gates in the order `[04 R-PATH-01 §8]` states:
  terminal-cell test, half-distance test, synthetic fallback. Both point-count
  gates require three or more **currently held** points — which, at the moment
  a goal is installed, is whatever the follower was carrying for the *previous*
  goal, usually nothing. That is what the synthetic fallback is for: it gives
  the mover a provisional straight line to walk while the asynchronous search
  runs, instead of standing still.
* The **publisher** is a separate method, and it is the only writer the path
  search uses — its four call sites are route reconstruction, the two
  request-init early exits, and heap exhaustion. Its whole body is: clamp any
  count above 20 to 20; write the count; copy the points; set *has-waypoint*,
  clear *wants-repath*, set *dirty*. On an empty publication it asks the goal
  whether the unit is at the goal, raises `0x40` if not, clears *has-waypoint*
  and *wants-repath*, sets *dirty*, and writes neither count nor array. There
  is **no** point-count test beyond the 20-clamp, no goal-point query, no
  terminal-cell test, no half-distance test and no synthetic rewrite anywhere
  in it. **A published route is adopted verbatim.**

`[04 R-PATH-01 §8]`'s sentence "When a newly published route (or a new goal
object) is installed, the follower does, in order: …" is therefore wrong in its
parenthetical — it is the goal object alone. Everything else in that section
stands, including the two three-or-more thresholds and the synthetic fallback's
shape.

**Established — the flag that suppresses the synthetic fallback.** The same
trace closes `[04 R-PATH-01 §8]`'s `TODO(question)` ("the semantic name of the
order-record flag that suppresses the synthetic fallback"). The installer emits
the synthetic route only when the unit has a primary queue head **and** that
head's flags word does not carry the **completion flag** — the same bit the
primary pump's code-9 arm sets before it re-arms or removes the record
(`[04 §3.3]`). A record being taken down by a code-9 completion gets no
straight-line consolation route.

**Established — why the column is a deadlock and not a contract.** With the
gates confined to the goal installer, the egress composition of
`[04 R-EGRESS-01]` steps 1, 2 and 4 no longer produces a column, because three
mechanisms it omitted do their work:

1. **The rectangle goal's admissible set is its whole border, not its goal
   point.** The rectangle enumerator appends every cell of the perimeter, and
   request setup marks every in-bounds enumerated cell as an acceptable
   terminal; the search's pop loop stops at the first terminal it pops
   (`[04 R-PATH-01 §4][04 R-PATH-01 §9][04 R-MOV-03 §9]`). For a land product
   of footprint width 2 the rectangle is 16 by 12 cells, so about fifty
   distinct cells satisfy the goal. The far-edge goal point matters only to the
   *installer's* half-distance test and its synthetic route — never to where
   the search terminates, and never to arrival, which is lying on the border
   (`[04 §7.2]`).
2. **A settled neighbour becomes opaque to the next search.** The class layer's
   occupant test blocks a cell whose occupant's last-stamp tick predates the
   layer watermark, and the watermark trails the current tick by 30
   (`[04 R-MOV-03 §3][04 R-COLL-01 §7]`). A product that has arrived and
   stopped committing is a hard obstacle to every request issued more than 30
   ticks later.
3. **The blocked follower re-requests, and its order re-goals.** A blocked
   mover arms *wants-repath* every tick and the scheduler admits it at most
   once per 60 ticks (`[04 R-MOV-01 §7]`). If the search finds a way round, the
   route publishes and is adopted verbatim. If it does not, the empty
   publication raises `0x40`, and `Park`'s phase 1 — no arrival bit, no record
   behind it — arms a 30-tick deadline and returns *restart*, which the pump
   maps to **phase reset to zero** (`[04 §3.3]`). Thirty ticks later phase 0
   installs a **fresh** rectangle centred on the unit's *current* committed
   cell: the rectangle is re-derived per re-arm, not held for the life of the
   order. (Phase 1 never runs in the same cascade as phase 0: phase 0 leaves
   gate `0xE0` and its own goal install has just wiped the record's satisfied
   bits `0x20` through `0x200`, so the pump's walk stops on the unsatisfied
   gate.)

The observable is therefore not a column: products fan out around the shared
rectangle's border, one per cell, and the exit clears behind each of them. A
rally point still replaces `Park` with the producer's own goal
(`[04 R-FAC-02 §4]`); it is a convenience, not the only thing that prevents a
stall.

**What `[04 R-EGRESS-01]`'s prohibitions still forbid, and what they do not.**
Do not make the rectangle goal point per-mover, do not relax the installer's
three-point gates, and do not scatter neighbours: those three remain
contradicted by direct traces, and no push, stacking, force-placement or engine
scatter exists (`[04 R-FAC-02 §6][04 R-COB-05]`). What is *not* forbidden — and
is required — is publishing a search result verbatim.

**Nanolathe divergence this reverses (2026-08-31).** Nanolathe applied the
acceptance gates on every publication as well as on goal install. A no-rally
product blocked behind a parked predecessor repaths on the 60-tick cadence, the
aged predecessor blocks in the class layer, and the search correctly returns a
route around it to a different border cell — but a straight or diagonal run
collapses under collinear removal to exactly **two** points
(`[04 R-PATH-01 §7]`), the misapplied gates rejected it for being under three,
and the synthetic fallback overwrote it with the straight line at the constant
far-edge goal point, walking the mover back into the column. The column grew
backwards into the exit footprint and the factory's exit test never passed
again: a permanent stall with no resource consumption, reproduced with eight
products out of one plant. Confining the gates to the goal installer — the
publisher writing points, count and flags and nothing else — makes the same
scenario fan out and the exit clear. The fix is a movement-layer change owned
by another file this round; this section is the contract it must satisfy.

**Confidence.** Established: direct static trace of the follower's install and
publish methods and their call sites, the rectangle enumerator, the search's
terminal marking and pop test, the class-layer occupant-age gate, the `Park`
handler, and the primary pump's code-0 arm. The only inference is the
attribution of *purpose* to the synthetic route (a provisional line to walk
while the search runs); its arithmetic and call sites are direct.

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

**Correction (2026-09-02, RWU-19-27) — step 4's packet names the factory as
attacker.** Step 4 above gave the packet only as "the ordinary kill packet —
kind-9 damage of exactly 30000", and an implementation took the attacker from
the reverse arm's self form. The packet cancel-current sends is
`damage(attacker = the factory, victim = the product, 30000, kind 9, flag 0)`:
the factory is the attacker, not the product. The self form
`selfKill(target, target, 30000, kind 9)` belongs to the shared work helper's
reverse arm alone ([R-WORK-01 §1]); same kind and amount, not the same packet.
Nothing on the credit side turns on it — cause 9 has no credit branch
([06 §12.1]) — but the recorded-attacker link and the death row hold the
factory's identity for a cancelled product. The order inside the arm is
exactly steps 1, 3, 4, 5: refund; completion transition with the factory as
builder (so a product with `activatewhenbuilt` receives its `Activate` edge
in step 3 and dies in step 4 of the same call); kill packet; then the
activation and building bits lowered in one edge call, the interface refresh,
and result 5. **Established.**
The producers of interrupt masks 2 and 8 are the record-removal cleanup notice
and the product's destruction respectively — see the correction under
"interrupt producers" above (2026-09-02).

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
  if (worker >= 0.0f) target.pendingWord |= 0x8000    // GetBuilt's wake, set
                                                     // before the zero test
                                                     // [04 R-ORD-01 §11]
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
Neither case faults. **Correction (2026-09-02):** the "zero low word" sentence
describes the resurrection delay's conversion ([R-WORK-01 §7]), not the step:
in the step the infinity never reaches an integer conversion — the clamp
absorbs it. [R-WORK-01 §11] gives the zero-`buildtime` and
zero-`buildcostenergy` arms exactly.

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

**Established — what the `Slot` column is, and where these captions go**
(added 2026-08-30, [07 R-HUD-03 §14]). `Slot` is the **sound event slot** of
[03 §8.3]'s static slot table — 7 `cant`, 8 `unitcomplete`, 9 `build` — not a
priority and not a screen position. The caption is raised only for the viewing
player's own live unit, queued on the unit voice/caption queue, and, when
`10 − unitchattext < the slot's priority`, appended to the shared message-line
ring as `"<unit display name>: <caption>"`; the master composer draws that ring
as a column at the top-left of the view. At the shipped `UNITCHAT = Medium`
only the slot-7 captions clear the gate, so `Starting construction` and
`Building complete` are not shown by default. None of this is the footer's
`MISSIONTEXT` field, which is a separate per-frame readout of the hovered
unit's order kind ([07 R-HUD-03 §2]).

**Unknown — the executor-flag bits.** Several of the predicates above and in
[R-WORK-01 §3..§7] test bits of the flag word the order pump passes into an
executor: a terminate/interrupt bit, a cancel bit, an arrival-failure bit, and
a high bit that unit reclaim and capture treat as terminal. Their producers and
names belong to doc 04 §3.1 and are not established here. Decider: static trace
of the pump's writer set for that word.

#### R-WORK-01 §9 — The clamp kill is the only thing that removes an abandoned frame [R-WORK-01] (2026-08-30)

**Established by composition of three traces already in these docs; nothing new
is traced here.** The composition is written down because the reverse arm's
last line reads like bookkeeping and is easy to drop, and dropping it produces
a permanent, silent world defect rather than a visible one.

1. The reverse arm ends `if (newStored >= 1.0f) selfKill(target, target,
   30000, kind 9)` — the no-corpse, no-explosion path, severity zero
   ([R-WORK-01 §1]'s listing above, and [05 "Resurrection"]'s prose statement
   of the same line).
2. A nanoframe with no builder decays: `GetBuilt` phase 2, on a visit no
   admitted work step has deferred, applies that arm with a quantum of
   `−(11 · buildtime / buildcostenergy)` ([04 R-ORD-01 §5]). Nothing else
   advances an unattended frame, so it walks monotonically to the clamp and
   stops there.
3. A frame holds the ground words of its footprint from the allocation call
   itself, and a factory's state-2 area test runs with self identity 0, so any
   non-zero word refuses it ([04 R-FAC-02 §5], [04 R-FAC-02 §6]).

Therefore the clamp kill of (1) is the **only** engine event that ever removes
an abandoned frame from the ground plane. Retail has no push, no stacking, no
force-placement and no alternative allocation site ([04 R-FAC-02 §6]), so a
frame that survives its own clamp is an obstruction with no remover: a frame
left on a factory's exit spot — by the producing factory being destroyed
mid-product, or by a mobile builder abandoning its site — pins that factory in
the silent 15-tick state-2 retry for the rest of the battle. The observable is
a factory with a queue, no progress, and **no resource demand at all**, because
the demand is raised in state 3 and state 3 is never reached.

Health is not the terminator and must not be treated as one: the reverse arm
floors health at zero with no `maxdamage` cap ([R-WORK-01 §1]), so the frame
reaches zero health one or more visits *before* the fraction reaches one, and
sits there at zero health, alive, until the clamp fires.

#### R-WORK-01 §11 — Malformed build numbers through the step and the decay wrapper, exactly [R-WORK-01] (2026-09-02)

**Established.** Three malformed definitions reach the shared step; each is
settled by the x87 compares and the clamp listed in [R-WORK-01 §1], and none
faults. (RWU-19-27, static.)

*Zero `buildtime`, forward arm.* `worker / (float)0` is `+∞`; `old − ∞` is
`−∞`; the clamp's first test (`new80 <= 0.0`) stores `0.0f`. `delta32 = old`,
so the demands are the **whole** remaining cost — `buildcostenergy × old` and
`buildcostmetal × old` — in one admission, and the health gain is
`trunc(maxdamage × old)`. If admission accepts, the stored `0.0` fires the
completion transition on the same call: a zero-`buildtime` product completes
on its first admitted step, paying everything at once. A guard that returns a
new fraction of `0` for `buildtime = 0` is retail-exact. For a **negative**
`buildtime` retail instead inverts the step: the fraction rises, the clamp
stores `1.0f`, the demands go negative, and the unsigned health cap of §1
pins health at `maxdamage` — a frame that never completes. That case is
malformed, unshipped, and not reproduced by a `<= 0` guard.

*Zero `buildcostenergy`, decay wrapper.* `GetBuilt`'s phase-2 quantum is
`−((float)(buildtime × 11) / buildcostenergy)`: a 32-bit signed product
converted to float, divided by the single-precision cost, negated, and passed
as `float32`. Cost `0` with a positive `buildtime` gives `−∞`. The step's
entry compares read `−∞` as negative and as non-zero, so the reverse arm
runs: `new80 = old + ∞` clamps to `1.0f`; `delta32 = old − 1`; the refund is
`buildcostmetal × (1 − old)` — every unit of metal already sunk, credited at
once through the 0.5/0.7 selector ladder; health floors at zero; the stored
fraction is `1.0`; and the arm's last line kills the frame with the self-form
kind-9 packet (no corpse, no explosion). A zero-energy-cost nanoframe is
therefore removed on its **first** decay visit, in full refund, rather than
decaying.

*Both zero.* `0 × 11 / 0.0` is a NaN quantum. Both entry compares are
unordered: the first reads it as negative, so the `0x8000` wake of
[04 R-ORD-01 §11] is **not** raised, and the second reads it as equal to
zero, so the step returns *not committed* having written nothing. Such a
frame never decays and is never killed by the wrapper; a real builder's
positive quantum still divides by the zero `buildtime` as in the first arm,
so the frame completes on the first admitted step of any builder.

For an implementation: the decay quantum must be formed in `float32` exactly
as the wrapper forms it, so that `−∞` and NaN reach the step's own compares;
a `buildcostenergy > 0` guard around the decay is not retail.

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

#### R-WORK-01 §12 — Where the reach test runs, the compare's sign, and what the nano reach is not [R-WORK-01] (2026-09-02)

**Established.** [R-WORK-01 §2] gives the expression; three things about its
use were still read as open at the implementation (RWU-19-27, static).

1. *The compare is signed.* `distWorld − builderPad + targetPad` is a signed
   32-bit value compared `<=` against the zero-extended 16-bit `builddistance`
   with a signed compare: a builder standing inside the target's
   half-diagonal (a negative left side) passes.
2. *Where it runs.* For a mobile builder the test is consulted **only on the
   arrival-failure wake** of the approach phase (satisfied bit `0x40`, the
   "cannot get there" signal of [04 R-ORD-01 §0]). The handler then measures
   from its own position to the record's goal — the site centre snapped to
   the product footprint in phase 0, `(foot + 2·cell) · 2^19` per axis — with
   the builder instance's footprint pair and the **product definition's**
   footprint pair as the two pads, and abandons with `I can't reach the
   construction site` when the test fails. A successful arrival at the
   rectangle goal (bit `0x20`) skips the test entirely: standing on the
   footprint's border ([04 §7.2]) is the reach. The work phase has no range
   test at all — a builder that has started work keeps working at any
   distance until the product completes or its record is removed. Repair's
   approach phase re-issues its goal on failure instead of abandoning
   ([04 R-ORD-01 §5]); the factory-product path has no reach test, the
   product being carried ([04 R-FAC-02 §1]).
3. *What it is not.* The reach involves no nano piece (`QueryNanoPiece` is
   presentation-side, after admitted work — [R-P0-06 §2]), no piece height,
   no Y term, and no model radius: the `(Xextent + Zextent)/3` radius belongs
   to unit reclaim's squared form only. The only footprint terms are the two
   half-diagonals, `trunc(8 · hypot(footX, footZ))` each, subtracted from the
   centre-to-centre distance in whole world units.

This retires a reach of `builddistance` in 16.16 compared against the nearest
point of the site's footprint rectangle: retail's test is centre-to-centre
with both half-diagonals subtracted, and it is only the fallback above.

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
| `RepairPatrol` | 1 plus conditional feature tournaments | one bounded pick from the ordered unit gather; if the handler reaches feature pairing, three picks over the energy-bearing sampled list and then three over the metal-bearing sampled list, each only for a nonempty list |
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
work visit, and the `healtime` path draw nothing. Outside the work visit,
`RepairUnit` phase 1 has the out-of-range `30 + boundedDraw(30)` retry, and
`RepairPatrol` has one pick over the ordered damaged-unit list it gathers
within its sight distance. If no unit action
terminates the visit, feature pairing samples its 48-world-unit lattice and
makes three bounded picks with replacement over each nonempty energy-bearing
and metal-bearing list, in that order. **Correction (2026-08-31,
[01 R-DET-01 §6]):** the previous text assigned those six draws to the unit
gather; the unit gather is one ordered vector and costs no RNG. A
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

#### R-FEAT-01 §15 — The payout guard's two bits, named [R-FEAT-01] (2026-08-29)

**Correction to [R-WORK-01 §5].** Its payout step 1 says the helper "refuses
outright — returning without paying — when the terrain cell's protection
bit **and** the feature definition's protection bit are both set", and the
tail listed the cell bit's meaning as unknown. Both bits are now named: the
cell bit is the anchor's **instance-attached** bit (set by the stamp for 3D
definitions and by ignition and the die/reclaim transitions for sprite
definitions, §3/§5/§9), and the definition bit is flag bit 0, **sprite
(filename-based) definition**. The conjunction therefore means "a sprite
feature that currently has a live animation instance" — one that is burning,
or already playing its death or reclaim animation. The refusal is what
"burning blocks reclaim" under "Feature burning" describes; it never applies
to a 3D wreck (its instance bit is always set but its definition bit is
clear), so a sinking wreck stays reclaimable throughout, as "Feature sinking
and water interaction" states. The item is closed.

#### R-WORK-01 §5-A — What the pools are worth in stock content, and the two payout hazards [R-WORK-01] (2026-08-30)

**Established (reference install, `~/TotalAnnihilation`, 1644 compiled feature
definitions).** The two halves of §5's payout are not symmetric in the shipped
data, and the difference is what a player sees:

* 922 definitions carry `reclaimable`; 933 name a `featurereclamate`
  successor. **Every reclaimable stock feature that names one names
  `smudge01`** — a one-cell, non-blocking, non-reclaimable scorch with empty
  pools. Feature reclaim in stock content is therefore never a plain removal:
  it is a replacement, and an implementation that only clears the grid loses
  the scorch.
* 134 definitions carry a positive `energy` and no metal; 902 carry a positive
  `metal` and no energy. The split is by kind, not by chance: **the vegetation
  (`features/trees`, `features/acid`, the plant groups) pays ENERGY and the
  wreckage (`*_dead`, and the metal deposits) pays METAL.** `tree1` is
  `metal 0, energy 250, damage 0, reclaimable 1, blocking 1, height 40,
  featurereclamate smudge01, filename trees`; `armaap_dead` is
  `metal 1768, energy 0, damage 1680, reclaimable 1, blocking 1, height 20,
  featurereclamate smudge01`, with no `filename` because it is a 3D wreck.
  No shipped definition pays both.
* `damage` is unrelated to the countdown, and the data says so plainly:
  `tree1` has `damage 0` and still takes `trunc(15 + 250/2) = 140` work, i.e.
  seventy visits and 140 ticks. The retired "damage value is the completion
  threshold" reading that §5 corrects would have made every tree instant.

Two consequences follow for any implementation of §5's phase 5, and both are
worth stating because getting either wrong is worse than not implementing
reclaim at all:

1. **The credit and the grid transition are one event.** The pools are paid
   once, unconditionally, on the visit that removes the feature. Crediting
   without removing pays the same tree forever; removing without crediting
   deletes the map's economy. There is no partial-progress payment anywhere in
   §5 — the countdown is on the order node and is discarded with it, exactly as
   [R-WORK-01 §4] describes for the unit form.
2. **The countdown is the feature's, not the builder's.** `workertime` does not
   appear in `trunc(k + (metal + energy) / 2)`. A commander and a construction
   aircraft take the same number of ticks over the same tree; only `k` differs,
   and only by ground (15) versus air (30) [04 R-ORD-01 §5, §7].

**Established — the truncation is on the whole expression.** §5 gives the form
as "the sum is multiplied by a stored `-0.5f` and then subtracted **from**
`15.0f`", with one float-to-integer conversion at the end. The halving is on
the SUM and the truncation is not distributed over it: for a negative authored
pool sum of −3 the value is `trunc(15 − 1.5) = 13`, where halving first and
truncating each part gives 14. No stock definition authors a negative pool, so
the difference is unobservable on retail content; it is recorded because the
integer form an implementation naturally reaches for is the wrong one.

**Unknown — the height byte's TDF key.** §5's phase-1 draw is bounded by "the
feature definition's height byte". The compiled catalog's `height` key is the
only height-shaped field on a feature record [02 "Feature record"] and its
stock range is 0..490, so it is the field this build draws against; whether
retail reads that key or a derived byte is still open. *Decider:* the feature
parser's key list against the draw site's operand.

### Closed — the world-position feature resolver [R-ECO-02 §2] (2026-08-29)

**Established.** The feature-reclaim executor's "the order's stored position
resolves to a feature definition index" ([R-WORK-01 §5]) is one small
resolver, shared with the commander-respawn scan of [08 R-SKIR-01 §3]. Given
a 16.16 world position it computes:

```
x = X >> 20 ; z = Z >> 20                 // 16.16 world -> 16-unit cell, floor
cell = plot(x, z) ; if none (off-map)     -> return NONE (0xFFFF)
if cell.word == 0xFFFE (fringe):          // hop to the anchor
    z -= cell.dzByte ; x -= cell.dxByte   // the two offset bytes of [R-FEAT-01 §3]
    cell = plot(x, z)                     // NOT re-tested for off-map
if cell.word > 0xFFFA                     -> return NONE
    // 0xFFFF empty, 0xFFFB..0xFFFD void thresholds, and a dead hop
if cellOut:  *cellOut = (x, z)            // the anchor cell after the hop
if footOut:  *footOut = catalog[word].(footprintx, footprintz)
return cell.word                          // the live feature index
```

The shifts are arithmetic on the signed 16.16 words, so a position west or
north of the map floors to a negative cell and the plot lookup reports
off-map. The fringe hop uses the stored signed offset bytes exactly as the
validator does, but it does **not** null-test the hopped cell: a fringe whose
offsets point off the map dereferences a null cell record — a fault the
stamp of [R-FEAT-01 §3] never produces, since fringes are only written for
cells inside the footprint. The two optional outputs are the anchor cell pair
and the definition's footprint pair; feature reclaim uses the footprint to
build the nano-box target of [R-WORK-01 §5] and prints `Reclamation failed`
on NONE, and the respawn scan uses only the NONE test to reject a candidate
point on a feature. **Supported inference (naming):** the resolver has two
further table-dispatched callers with no live reference in the image; they
are the dead reclaim variants of [R-FEAT-01 §4]'s census, and nothing in play
reaches them.

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
**Correction (2026-09-02, [R-WORK-01 §11]):** the kill count is **not**
copied — "veteran experience" is not carried — and the conditional copy is the
per-slot stockpiled-round byte; the exact list is in that section.

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

#### R-WORK-01 §10 — The "cloud of vapor" sentinel is the remaining-build fraction compared with literal zero [R-WORK-01] (2026-09-01)

RWU-19-5 asked which victim field predicate 5 of [R-WORK-01 §6] compares, and
against what, because the build tests `Remaining == 0` as a proxy for an
"idle sentinel".

**Established — the operand and the constant.** The predicate loads the
target's remaining-build fraction — the single-precision field the
construction step of [R-WORK-01 §1] drives from `1.0` toward `0.0`, seeded by
the constructor with integer zero for a finished unit and `1.0` for a
nanoframe — and compares it with a literal single-precision `0.0` held in the
executable's constant data. The comparison is a floating-point compare whose
only accepted outcome is *equal*: less, greater and unordered all take the
reject branch (`That unit is a cloud of vapor and cannot be captured`, cue
slot 7). Negative zero compares equal and is accepted; a NaN in the field
would be rejected, but no writer produces one. There is no idleness sentinel,
no order-state test and no health test in this predicate.

**Established — `Remaining == 0` is equivalent, not a proxy.** A `float32`
`== 0` has exactly this semantics — true for `+0` and `−0`, false for every
other value including NaN — so the build's test is the retail test and its
marker can be retired. Two other things the build's admission does are
resolved by [R-WORK-01 §6] itself: the "victim immunity" it could not locate
is predicate 4, the **target's** own `cancapture` definition bit; and the
same-owner and dying-victim rejects it adds are not in the phase-0 ladder,
which has exactly the five predicates listed there. **Unknown:** whether the
order-side target validation doc 04 owns excludes a same-owner or
death-latched capture target before the executor runs; the transfer path's
own validation of old and new ownership ([05 "Capture"], "ownership
transfer") is the only later refusal established here. *Decider:* static
trace of the capture order's target admission in the order builder.

#### R-WORK-01 §11 — Capture's order-side admission, and the transfer's exact copy list [R-WORK-01] (2026-09-02)

**The order-side admission — Established.** [R-WORK-01 §10]'s Unknown asked
whether the order builder excludes a same-owner or death-latched target
before the executor's five-predicate ladder runs. The command resolver's code
13 ([04 R-ORD-02 §1]) is the whole order-side test: the actor's `cancapture`,
a target, and **the target's owner record differing from the actor's** — a
same-owner target never becomes a `Capture` order. Before any code's switch
the resolver rejects a target lacking the alive bit, but it does **not** read
the death latch (bit 14 of the state word, [04 §5.1]), and the issue helper
that queues the resolved order reads neither. A target killed this tick —
latch set, alive bit still set until the next sweep's finalizer — therefore
passes the resolver, passes phase 0's ladder (which tests none of owner, latch
or health), and is refused only by the transfer path: its entry gate is
`owner ≠ new owner`, alive bit set, **death latch clear**, and a refusal there
is silent (the executor still raises cue slot 16 with no text, §6). So the
same-owner exclusion belongs at command resolution, the latch exclusion at the
transfer, and the executor's ladder stays at five.

**The transfer's copy list — Established, correcting the "ownership
transfer" paragraph above.** That paragraph says the replacement receives
"health, remaining fraction, veteran experience, and visual piece and facing
fields". The local branch (new owner's control byte 1 or 2, [R-SHARE-01 §1])
creates the replacement through the ordinary creator as a **finished** unit
with the old unit's definition, position and movement-mode bits and the new
owner's side; clears state bits 18–21 (both standing-order pairs, so the
replacement starts with neither stance rather than the definition defaults
the creator had just written); then copies, in order: the 16-bit health, the
remaining fraction, the orientation triple (bank, heading, pitch), and — for
each of the three weapon slots, **only when the replacement's slot control
byte has its enabled bit** — the slot's **stockpiled-round byte** (the
completed-ammunition byte of "Stockpile production" above). That gated
per-slot byte is the whole of the "cargo copied conditionally": stockpiled
rounds follow the unit, slot by slot, wherever the new record has that slot
enabled. **Nothing else is copied. The kill count is not** — the replacement
is a fresh record with zero kills, so "veteran experience" is not carried and
the next capture's kills factor restarts from zero; the transported-cargo
list, alliances, orders and groups are not carried either. The old unit is
then killed with a cause-4 packet and a null attacker ([06 §12.1]), and the
replacement's operational edge bits (activated, cloaked, …) are replayed from
the old unit's operational byte — the bits it had are set, the bits it lacked
cleared — through the state-edge setter, so an activated or cloaked unit
stays so across the transfer. The other branch (old owner control 1 or 2,
**new owner control 3 — a remote peer** by [R-SHARE-01 §1], not a computer
player as [04 R-ORD-02 §1]'s "human-to-computer" gloss reads it) creates
nothing locally: it writes the 150-tick post-capture countdown, clears the
selected bit, emits the transfer packet (the same fields, the three stockpile
bytes gated on slot 0's enabled bit alone) and kills the old unit with the
same cause-4 packet; the replacement is the peer's. That branch is
multiplayer transport and out of Nanolathe's scope.

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

#### R-FEAT-01 §1 — The feature parser, exactly: fields, widths, defaults, and the key census [R-FEAT-01] (2026-08-29)

**Established — one parser, one record shape.** Every feature definition is
compiled by a single parser that takes a section name, finds the section in
the loaded feature TDF set (a linear scan of every loaded file, first match
wins), grows the catalog by one fixed-size record, and returns the new
record's ordinal. The catalog is a reallocated array — there is no fixed
catalog cap; "catalog exhaustion" is not a retail failure mode. If the name is
in no loaded file the parser prints `Record "%s" missing from feature files`
through the diagnostic sink and then continues into the field reads with a
null section handle; the record is still appended. What the field reads do
with a null section is **Unknown** (decider: static trace of the TDF getter's
null-handle path). Nanolathe should treat it as a fault.

The parser reads exactly these keys, in this order, with these widths and
defaults. Numeric keys go through the integer getter (default shown) unless
noted; string keys through the string getter with an empty default.

| Key | Stored as | Default | Reader census (who consumes the stored value) |
|---|---|---|---|
| section name | 128-byte name | — | successor resolution, save/load name box `[R-SAVE-FEATURE-01]`, the dragons-teeth/fortification name test below |
| `Description` | 20-byte string | empty | presentation only (hover text); no simulation reader |
| `footprintx`, `footprintz` | int16 each | 0 | placement, teardown, collision, reclaim box, blast centre, occupancy notify (§3, §4, `[R-WORK-01 §5]`) |
| `height` | byte | 0 | reclaim walk-target draw bound `[R-WORK-01 §5]`; the projectile feature-collision test `projectileHeight < terrainHeightByte + height` (doc 06); renderer fog-memory rule (`height < 10`, doc 03) |
| `object` | model handle | — | present ⇒ 3D feature: the flag word's bit 0 is **cleared** and every `seqname*` key below is **skipped** |
| `filename` | GAF bank handle | — | sprite features only (bit 0 set); the bank is shared: if an earlier catalog record already loaded the same 16-character name the handle is reused (`REUSE`), else `anims\<filename>.gaf` is loaded |
| `seqname`, `seqnameshad` | sequence handle | 0 | renderer (rest image and shadow); when `animating=1` they also seed the per-definition rest cursors (§10) |
| `seqnameburn`, `seqnameburnshad` | sequence handle | 0 | ignition (§9): a definition without `seqnameburn` **cannot ignite** |
| `seqnamedie`, `seqnamedieshad` | sequence handle | 0 | death transition (§5) |
| `seqnamereclamate`, `seqnamereclamateshad` | sequence handle | 0 | reclaim transition (§5) |
| `spreadchance` | byte | 0 | burn event, read on the **candidate** (§11) |
| `reproduce`, `reproducearea` | byte each | 0 | reproduction walk (§12) |
| `metal`, `energy` | `float(uint16(value))` — the integer is masked to 16 bits, then converted | 0 | reclaim payout `[R-WORK-01 §5]`; metal-byte seeding `[R-PROD-01 §6]`; the area-reclaim candidate scan (§6) |
| `damage` | int16 | 0 | feature hit points (§8) |
| `animating` | flag bit 1 | 0 | renderer; per-definition rest-cursor advance (§10) |
| `animtrans` | flag bit 2 | 0 | renderer only (transparent blit selector) |
| `shadtrans` | flag bit 3 | 0 | renderer only (shadow blit selector) |
| `flamable` | flag bit 4 | 0 | damage entry (§8) and burn spread (§11) |
| `geothermal` | flag bit 5 | 0 | yard-map bit 7 `[04 §6.4]`; steam-strip producer at placement (§3) |
| `blocking` | flag bit 6 | 0 | the passability classifier `[R-DOC04-B]` and yard-map bit 5 `[04 §6.4]` — **no other reader**; see §6 |
| `reclaimable` | flag bit 7 | 0 | reclaim executor start gate `[R-WORK-01 §5]`, order-target classification (doc 04 §3), the area-reclaim scan (§6) |
| `autoreclaimable` | flag bit 8 | **1** | the area-reclaim candidate scan only (§6) — **not** teardown |
| `indestructible` | flag bit 9 | 0 | damage entry (§8), teardown honor test (§4), yard-map bit 6 `[04 §6.4]`, metal-byte seeding `[R-PROD-01 §6]` |
| `nodisplayinfo` | flag bit 10 | 0 | presentation only (hover-info suppression) |
| `nodrawundergray` | flag bit 11 | 0 | renderer fog-memory rule (doc 03); also **forced set** when the section name equals `DragonsTeeth`, `DragonsTeeth_Core`, `Fortification`, or `Fortification_Core` (case-insensitive) |
| `sparktime` | read through the **float** getter (default `0.0`), truncated toward zero, stored int16 | 0 | ignition countdown (§9) |
| `burnweapon` | weapon handle by name | 0 | burn event (§11) |

**Established — keys with no reader (reader census: none).** The executable
contains no string for `permanent`, `hitdensity`, `burnmin`, `burnmax`,
`sinktime`, `world`, or `category` as a feature key (`category` exists only
as a unit-FBI key read by the unit parser; the only `Permanent` string is a
lobby line-of-sight option label). All seven are inert: authoring them
changes nothing. `burnmin`/`burnmax` in particular do not set burn duration —
the burn lifetime is the animation length (§10).

**Established — successor keys are read in a separate pass.** `featuredead`,
`featureburnt` and `featurereclamate` are not read by the parser at all; see
§2.

**Established — the loop byte is forced.** For each of the six event
sequences (`seqnameburn`, `seqnameburnshad`, `seqnamedie`, `seqnamedieshad`,
`seqnamereclamate`, `seqnamereclamateshad`) that resolves, the parser writes
zero into the sequence's loop byte. `seqname`/`seqnameshad` keep the GAF loop
byte. A `seqname*` value that is an empty string stores 0 (treated as absent).
What the bank lookup returns for a name that is **not** in the bank is
**Unknown** (decider: static trace of the sequence lookup's miss path); the
shipped corpus has no such case (asset census under "Feature burning").

**Established — the rest cursors.** When `animating=1` and `seqname` resolved,
the parser initialises a per-**definition** animation cursor from `seqname`
at frame 0, and a second one from `seqnameshad` when that resolved. These are
what the feature phase advances every tick (§10); they are shared by every
placed instance of the definition, so all copies of an animating feature are
always on the same frame.

**Established (reference census) — what `animating=1` actually animates.**
`animating=1` is a parser flag, not a promise of motion, and the shipped corpus
makes that distinction load-bearing. Counted over the compiled feature catalog
of the reference install (I14): **82** definitions carry `animating=1` with a
`seqname` that resolves in its named GAF, and of those exactly **ten** have a
sequence with more than one frame — `acidplant01`…`acidplant05` and their `b`
variants, the gas plants of the acid worlds, each **20 frames at 4 ticks per
frame** (an 80-tick loop). Every other animating definition, including the
whole `*vent*` family across the acid, arch, crystal, dry, green, ice, lava,
lush, mars, metal, slate and wet sets, resolves to a **single-frame** entry,
and a single-frame entry never advances ([03 §4.4]). The visible motion a
player associates with a vent is therefore not in the feature at all.

`geothermal` is the extreme case, and it is worth naming because it looks like
a missing asset and is not one: it compiles `animating=1`, `seqname=geotherm`,
`filename=geotherm`, and `anims/geotherm.gaf` holds one entry, `geotherm`, of
one frame, **one pixel by one pixel**. A `geothermal` feature is the placement
marker the geothermal-plant build test reads, not artwork; the vent a player
sees under it is the map's own tile art. Arm campaign `AC04` (`MISSION3`)
places three `geothermal` instances and no other vent-family feature, so that
mission renders no vent sprite whatever the presentation layer does. The
multi-frame gas plants are placed by nine stock maps, of which `Gasbag Forests`
(1026 instances) and `Gasplant Plain` (615) are the dense ones — those are the
maps on which a feature animation is observable at all.

#### R-FEAT-01 §2 — Catalog build order and the successor pass [R-FEAT-01] (2026-08-29)

**Established.** At map load the loader allocates the live-instance arena
(2048 slots of 48 bytes, zero-filled once; three doubly-linked lists — active,
dormant, free — with the free list initially holding every slot in ascending
order) and one dummy "feature unit" record that stands in as the owner of
every feature-fired weapon; it then compiles one definition per entry of the
map's feature-name table, in table order. The terrain-cell feature words
reference these ordinals directly.

The successor pass runs later in session start, after all map and mission
features are already stamped. For every catalog record `i` (re-reading the
count each iteration, so records appended during the pass are themselves
processed) it re-finds the section and reads `featuredead`, then
`featurereclamate`, then `featureburnt`. Each is resolved by a
case-insensitive scan of the whole catalog; an absent key stores the empty
sentinel `0xFFFF`; a present key naming a definition not yet compiled calls
the parser for it **on demand** and stores the new ordinal. Because the pass
appends while iterating, a chain of successors (`Rock1a → Rock1b → rockgone`)
is compiled transitively even when only the head is named by the map. A
missing name at the end of such a chain reaches the parser's diagnostic path
above. The loading progress byte is advanced per record as `100 × (i + 1) /
count` (integer division). Nothing consumes the successor words between
placement and this pass.

**Reader census for the successor words.** `featuredead`: death replacement
(§5), unit-corpse chain walk (`[05 "Feature sinking and water interaction"]`);
`featurereclamate`: reclaim replacement (§5); `featureburnt`: burn completion
(§10). Nothing else reads them.

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

#### R-FEAT-01 §3 — The stamp service, exactly: the dense-pack rule, height snap, and the pool edge [R-FEAT-01] (2026-08-29)

**Established.** Every feature creation — terrain file, mission file, corpse,
successor, reproduction, save reload — goes through one stamp routine taking
`(anchorCell, ordinal, position3 or null, orientation3 or null, placerNibble)`
and returning the live-instance record or 0. In order:

1. `ordinal == 0xFFFF` → return 0 with no effect. `ordinal == 0xFFFC` → write
   the **void** marker `0xFFFC` into the anchor's feature word and return 0
   (the terrain loader uses this for cells the TNT marks `-4`).
2. Resolve the anchor's cell coordinates `(x, z)` from its position in the
   plot grid. Bounds are **inclusive at the far edge**: the stamp fails when
   `x + footprintx > mapWidth` or `z + footprintz > mapHeight`
   (`x + footprintx == mapWidth` passes).
3. **Dense-pack rule — collision teardown.** For every cell of the footprint
   (row-major, z outer), if the cell's feature word is not `0xFFFF` the
   teardown (§4) is called on it **without honor**. If that teardown returns
   0 the stamp returns 0 **immediately, leaving already-torn cells torn**.
   Consequences, all established: a new feature replaces any non-indestructible
   feature it overlaps (walking fringe cells back to their anchor and clearing
   that whole footprint); an indestructible feature under any covered cell
   (`RockMetal*`, `Geothermal`, the fortification family) vetoes the stamp; a
   void cell (`0xFFFC`) vetoes the stamp; a stale fringe whose anchor resolves
   empty also vetoes it (the teardown's sentinel test). There is no
   "already occupied" failure other than these — two blocking trees stamped
   on the same cell do not coexist; the later one wins.
4. **3D definitions (flag bit 0 clear) take a live slot.** Pop the free list
   head; if the free list is empty (`-1`) the stamp returns 0 — **after** step
   3 already tore down whatever was under it. The popped slot is moved to the
   active list and its burning bit cleared. The slot receives: the ordinal;
   accumulated damage `:= 0`; anchor `(x, z)`; position — the supplied triple
   verbatim, or when null the footprint centre with the terrain height snapped
   under it:

   ```
   worldX = ((footprintx + 2·x) · 8) << 16          ; 16.16, i.e. (x + footprintx/2) · 16 world units
   worldZ = ((footprintz + 2·z) · 8) << 16
   worldY = bilinearHeight(worldX, worldZ) << 16   ; the four-corner query of [03 §2.3], integer result
   ```

   the orientation triple — the supplied six bytes verbatim, or zero when
   null; and a fresh model-instance handle for the definition's object. **The
   velocity triple is not written by the stamp** (see §14 for what that
   implies). The anchor cell stores the ordinal, the slot index, and sets its
   instance-attached bit.
5. **Sprite definitions (bit 0 set)** take no slot: the anchor stores the
   ordinal, a zero in the slot/accumulator word, and a cleared instance bit.
6. The anchor's control byte gets the placer nibble: `bits 3..6 :=
   placerNibble & 0xF`, all other bits preserved. Terrain-file, mission-file,
   successor, reproduction and reload stamps pass nibble 10; a corpse passes
   the dying unit's owner player index.
7. **Fringe stamping.** Every covered cell other than the anchor gets feature
   word `0xFFFE`, the byte pair `(dz, dx)` = its offset from the anchor, and
   its instance bit cleared. The anchor itself keeps `(0, 0)` untouched from
   whatever was there; every reader of a fringe walks back by `dz·mapWidth +
   dx` cells.
8. If the definition's geothermal bit is set, the steam-strip producer of
   `[R-STRIP-01 §1]` is started at the same centre/height position as step
   4 (computed the same way when no position was supplied).
9. The occupancy-listener notify is called with the anchor `(x, z)` and the
   footprint pair. Each registered occupancy map re-classifies the cells
   `x .. x+footprintx` by `z .. z+footprintz` **inclusive** — one cell of
   margin beyond the footprint on the +X and +Z sides — through the
   passability classifier `[R-DOC04-B]`, which reads the blocking bit through
   the fringe hop. That classifier, not the stamp, is the feature-to-terrain
   blocking mask; the stamp only triggers it. Buildings are not features and
   never enter this path.

**Established — map-load placement and anchoring.** The terrain loader
stamps every TNT feature cell in row-major cell order (`-4` → void marker,
any ordinal below the table count → stamp with a null position, so the
Y snap above applies), then the mission file's feature list in file order.
A mission-file entry names a feature and a cell; for a **3D** definition the
anchor is `(cellX − footprintx/2, cellZ − footprintz/2)` with truncating
division (the entry is centre-referenced), for a sprite definition the anchor
is the cell itself. A mission-file name not yet compiled is compiled on the
spot. Both loaders pass nibble 10. The reload path is `[R-SAVE-FEATURE-01]`.

#### R-FEAT-01 §3-A — Dense pack over a fringe cell, restated [R-FEAT-01] (2026-09-02)

A code marker asks what retail does when a footprint covers a *partial* cell
— a `0xFFFE` fringe cell — of a different live feature. Step 3 above already
answers it and was re-verified RWU-19-22 (Established): the covered-cell
test is "feature word not `0xFFFF`", so a fringe cell is torn down like an
anchor. The teardown (§4) walks the fringe back to its anchor and applies
its rule to the **anchor's** definition: an indestructible anchor returns 0
and the stamp fails at once, leaving the cells torn so far torn; any other
anchor is freed and its whole footprint — anchor and every `0xFFFE` cell —
cleared to `0xFFFF`, and the new feature's stamp proceeds. Two consequences
for a loader that derives fringe ownership itself: a later footprint that
overlaps an earlier non-indestructible feature **replaces** it entirely (the
earlier anchor does not survive with a truncated fringe), and a later
footprint that overlaps an indestructible feature's fringe is **not
stamped** (its own anchor cell is not written). Skipping the contested cell
and keeping both anchors is neither of retail's outcomes.

#### R-FEAT-01 §4 — Teardown, exactly, and two corrections [R-FEAT-01] (2026-08-29)

**Correction.** The flag table under "Definition flags, teardown, and the
Great Divide partition" said `autoreclaimable` is a "reuse-suppression;
protects the cell from being implicitly cleared by a colliding stamp unless
honored explicitly". That is the wrong bit: the teardown's honor test reads
the **indestructible** flag (bit 9), and `autoreclaimable` (bit 8) has
exactly one reader, the area-reclaim candidate scan (§6). The same passage
also said the cleared fringe cells have "the signed offset bytes poisoned";
they are not touched — only the feature word and the instance bit are
written.

**Established — the routine.** `teardown(cell, honor)`:

1. If the cell holds `0xFFFE`, walk back to the anchor.
2. If the (anchor) word is `≥ 0xFFFB` — empty, void, or any other sentinel —
   return 0.
3. If `honor == 0` and the definition is indestructible, return 0. Every
   caller in the executable passes `honor == 0`; the honoring variant is
   never used, so **an indestructible feature is never removed by any path**.
4. If the anchor's instance bit is set: for a 3D definition release the model
   instance; then move the slot from whichever list holds it to the **head**
   of the free list (LIFO).
5. Write `0xFFFF` into the anchor's word and clear its instance bit.
6. For every cell of the definition's footprint rectangle from the anchor,
   **only if that cell currently holds `0xFFFE`**, write `0xFFFF` and clear
   its instance bit. Cells the footprint covers that meanwhile hold something
   else are left alone.
7. Notify the occupancy listeners with the same inclusive-margin rectangle
   as the stamp. Return 1.

The accumulated-damage word of an instance-less anchor is not cleared here;
the next stamp on that cell overwrites it (§3 step 5), so damage never leaks
to a successor.

### Closed — the geothermal steam producer, and a correction to the strip census [R-ECO-02 §3] (2026-08-29)

**Established.** Step 8 above names "the steam-strip producer of
[R-STRIP-01 §1]". Reading the producer fixes what step 8 left to the census,
and finds the census wrong on one row:

- The producer is called with the centre/height position of step 4 and the
  literal strip index **4**. [03 R-STRIP-01 §1] lists strip 4 as "none — the
  retired crater/decal literal 4 is retracted … always empty". That row is
  wrong: the retired census's "literal 4" was this site, and every placed
  geothermal feature appends one object to strip 4. *Correction* to be
  applied in doc 03 (cross-document; this document only records the
  finding).
- The producer follows the common producer shape of [R-STRIP-01 §1]: it
  returns without effect when the pool-disable byte is set (never, in
  retail — that byte has no writer), allocates one 52-byte object from the
  shared strip pool (exhaustion drops the steam silently), constructs it as
  the **smoke-puff** class of [03 R-FX-01 §3], calls the class's init virtual
  with the position and the three literals `5`, `0`, `150`, evicts the oldest
  object of strip 4 when the pre-insert count exceeds 400, and appends.

  **Correction (2026-08-31).** This paragraph called the class "the
  flame-family class". It is not: the producer constructs the smoke-puff
  class, whose vtable is the one holding the three-argument init below. The
  two families differ in what they blit and how their sub-records expire, so
  the misattribution would have given a vent flame segments marching toward a
  target point instead of puffs rising in place.

  **Correction (2026-09-01) — the third literal is not a lifetime, and the
  plume is perpetual.** The paragraph below closes with "A vent therefore
  produces thirty-one puffs over five seconds — one from the constructor and
  one every fifth tick while the next-spawn tick is still inside the window —
  and then stops. There is no perpetual plume." Both sentences are withdrawn.
  A retail capture shows a vent's plume still running eighteen seconds in, and
  the class's own virtuals say why: its removal verdict is a body that returns
  a constant false, and its spawn predicate is a bare "next-spawn tick at or
  before the global tick" with no window term. The stored `currentTick + 150`
  is read by nothing but the spawn's capacity reservation. A vent lays one puff
  at construction and one every fifth tick for the rest of the battle; what
  thins the plume to a handful of puffs is each puff retiring when its own
  animation cursor reaches its own randomly drawn last frame. Full derivation
  and the corrected update in [03 R-FX-01 §3 addendum]. Everything else in the
  paragraph — the interval, the frame hold, the class, the single call site —
  stands.

  **Scope of that correction (2026-09-01).** It is about the vent's own class
  and no other. The strips-5/9 smoke puffer is a **different class** whose
  removal verdict and spawn gate both keep the window term, so its producers'
  stored deadlines are real lifetimes; do not carry this paragraph's reading
  across to impact, muzzle, trail or burning-feature smoke. The vent also
  drifts upward four times as fast as those do. See
  [03 R-FX-01 §3 addendum §B] for the two vtables side by side.

  **Closed (2026-08-31) — the three literals.** The Unknown recorded here
  asked which init parameter each literal binds to. Reading the class's init
  virtual settles it: the first argument after the position is the **spawn
  interval** (5 ticks), the second is the **animation frame hold** (0, which
  the constructor defaults to 7, exactly as [R-STRIP-01 §1]'s tail note
  says of the smoke family), and the third is the container **lifetime**
  (150 ticks), stored as `deadline = currentTick + lifetime`. The init also
  stores the bound entry's frame count less one, and spawns one puff of its
  own before returning. A vent therefore produces thirty-one puffs over five
  seconds — one from the constructor and one every fifth tick while the
  next-spawn tick is still inside the window — and then stops. There is no
  perpetual plume.
- The producer is reached only from the feature stamp (step 8); a wreck or
  reload that re-stamps a geothermal definition produces a new steam object
  each time, and nothing removes the old one except its own lifetime, so a
  vent stamped repeatedly in one session accumulates strip-4 objects up to
  the 401 cap.

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

#### R-FEAT-01 §5 — Transition and replacement, exactly, and same-tick precedence closed [R-FEAT-01] (2026-08-29)

**Established — two routines.** A *transition* `(x, z, isReclaim)` is what
damage death (§8), the reclaim payout `[R-WORK-01 §5]`, the multiplayer
state commands, and save reload call. A *replacement* `(x, z, isReclaim)` is
the atomic teardown-plus-stamp. The transition:

1. walks a fringe back to its anchor; a word `≥ 0xFFFB` returns silently;
2. for a **sprite** definition selects the event sequence: `seqnamedie` (+
   `seqnamedieshad`) when `isReclaim == 0`, `seqnamereclamate` (+ shadow)
   when 1; for a **3D** definition the selection is always "none";
3. **no sequence ⇒ immediate replacement** (step 6);
4. sequence present and the cell already has an instance attached ⇒
   **return with no effect** — the death or reclaim is dropped, not queued
   (see precedence below);
5. sequence present and no instance ⇒ pop a slot (empty pool ⇒ return, no
   effect — the feature simply stays), attach it to the cell, start the
   sequence at frame 0 (and the shadow when named), record the anchor, and
   set the instance's mode bits: burning `:= 0`, reclaim-animation `:=
   isReclaim`, plus a separate copy of `isReclaim` in bit 4 (no reader found
   for that copy — reader census: none). The feature phase (§10) drives it
   to completion.
6. Replacement: successor `:= isReclaim ? featurereclamate : featuredead`; if
   the cell has an instance whose reclaim-animation bit is set the successor
   is **`featurereclamate` regardless** of the argument. Then
   `teardown(anchor, 0)`; then `stamp(anchor, successor, instance ? &position
   : null, instance ? &orientation : null, 10)`. A successor word of `0xFFFF`
   makes the stamp a no-op, i.e. final removal. Because the teardown pushes
   the instance's slot on the free list and the stamp pops the head, **the
   successor of a 3D feature reuses the same slot** and thereby inherits
   every word the stamp does not write (§14).

An indestructible definition never reaches the replacement's stamp with a
cleared cell: its teardown returns 0, the stamp's collision loop then calls
teardown again on the still-occupied anchor and fails. The damage entry
already rejects indestructible definitions, so in practice this only guards
the reclaim/reload paths.

**Established — same-tick precedence, closed.** The earlier text left "the
full precedence ordering when damage, burn completion, and reclaim arrive in
the same tick" open. It is now closed by construction, because every cause is
serialized through the anchor's **instance-attached bit** and the phase order
of `[01 §4.4]`:

* Within a tick, weapon impacts (projectile phase) run before the feature
  phase; order handlers (reclaim payout) run in the unit phase, also before
  the feature phase. Multiple impacts on one feature in one tick apply in
  projectile order, each seeing the previous one's result.
* **Sprite feature, no instance:** first cause wins. Ignition attaches an
  instance; a death with a `seqnamedie` attaches one; a death without one, or
  a reclaim payout, replaces the feature outright, after which the cell holds
  the successor (or nothing) and later causes act on *that*.
* **Sprite feature with an instance (burning, dying, or reclaiming):**
  every further cause is inert — the damage entry has no accumulation branch
  for it (§8), a second ignition refuses (§9), a death or reclaim transition
  returns at step 4, and the reclaim executor's payout refuses (§15). Only the
  animation's end (feature phase, §10) changes the cell.
* **3D feature:** it always has an instance, so ignition never applies;
  damage accumulates on the instance until `damage` is reached and then
  replaces immediately; a reclaim payout replaces immediately; whichever
  runs first in the tick's phase order wins and the other finds the successor.
* Burn completion and the reproduction spawn are feature-phase events and
  therefore always come after every same-tick impact and order.

There is no queue, no priority word, and no deferred resolution anywhere in
these paths.

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

#### R-FEAT-01 §6 — Flag reader census, and the two flags that do less than their names [R-FEAT-01] (2026-08-29)

**Established — `blocking` has exactly one consumer.** The passability
classifier of `[R-DOC04-B]` (called from the occupancy notify of §3/§4 and
from the map-load stamp) and the yard-map validator's bit 5 `[04 §6.4]` read
flag bit 6 through the fringe hop; nothing else in the executable does.
`blocking` therefore affects ground pathing and building placement and
nothing else — it does not affect projectiles, reclaim, damage, fire, or
which cell a corpse may take. A non-blocking feature (`RockMetal*`, smudges)
is invisible to movement. An ordinal at or beyond the catalog count, and the
`0xFFFB..0xFFFD` sentinels, classify as blocking; an empty fringe hop
classifies as not blocking.

**Established — `autoreclaimable` has exactly one consumer.** The
area-reclaim candidate scan: given a centre and a radius it walks the square
of cells `centre ± radius/2`, resolves each cell's feature through the fringe
hop, and lists the cell as an **energy** candidate when the definition has
`reclaimable=1` **and** `autoreclaimable=1` and `energy ≠ 0`, and as a
**metal** candidate under the same two flags when `metal ≠ 0` (a feature with
both pools appears in both lists). The two lists are consumed by the two
handlers that own area reclaim — the builder's area-reclaim order body and
the computer player's — whose identities are a Supported inference (they are
table-dispatched with no direct caller); the predicate itself is
established. `autoreclaimable=0` therefore only hides a feature from area
reclaim; a direct reclaim order on it still works, and it is still cleared
by a colliding stamp.

**Established — `indestructible` has four consumers**: the damage entry
(return before anything else, §8), the teardown honor test (§4), yard-map bit
6 `[04 §6.4]`, and the metal-byte seeding (§7). It does **not** stop
ignition by itself — but every shipped indestructible feature is also
non-flammable and the ignition entry is reached only through the damage
entry or spread, both of which test `flamable`.

**Established — `reclaimable` has three consumers**: the reclaim executor's
start gate `[R-WORK-01 §5]`, the order-target classifier that decides whether
a reclaim cursor/command applies to the thing under it (doc 04 §3.4 family),
and the area-reclaim scan above.

**Established — `geothermal` has two consumers**: yard-map bit 7 `[04 §6.4]`
and the steam-strip start at placement (§3). No wreck-transition rule reads
it; see §7 for what happens to a corpse over a vent.

#### R-FEAT-01 §7 — Metal deposits seed the metal byte, and vents versus wrecks [R-FEAT-01] (2026-08-29)

**Correction to [R-PROD-01 §6].** That section said, of the canonical
terrain version, "It never touches the metal byte, so every cell keeps the
uniform seed. There is **no per-cell metal raster**". The bounded negative
over the *terrain loader* stands, but the conclusion does not: a separate
map-load pass, run after every terrain-file and mission-file feature has
been stamped, walks every plot cell and, for each **anchor** cell whose
definition has `metal ≠ 0` **and** `indestructible = 1`, writes

```
cell.metalByte := (uint8) trunc( definition.metal )      ; float → int, low byte
```

into every cell of that definition's footprint (bounds-checked per cell;
off-map cells skipped). This is the only writer of the metal byte after the
uniform seed, and it is why `RockMetal*` deposits authored `metal=86..223`,
`indestructible=1` are the extractor economy: the extractor's footprint sum
`[R-PROD-01 §6]` reads these bytes. A reclaimable rock with `metal=100`
(`Rock1a`, `indestructible=0`) does **not** seed — its metal is reclaim
reward only. The sequence is: uniform `SurfaceMetal` seed → feature stamps →
this pass, so a deposit overrides the uniform seed inside its footprint and
nowhere else. Removing or replacing a feature later never rewrites the byte
(the deposit is indestructible anyway), and a mission-placed deposit seeds
exactly like a terrain-file one because the pass runs after both.

**Established — wrecks at geothermal vents.** There is no vent-specific
rule. A corpse whose footprint would cover a vent cell fails at the stamp's
collision teardown (§3 step 3: the vent is indestructible) and the death
path neither retries nor shifts it — the wreck is silently not created. A
corpse adjacent to a vent is unaffected. The vent itself is never damaged
(indestructible), never ignites (`flamable=0`, and the damage entry gates
ignition on that), never reclaims (`reclaimable=0`), never burns by spread
(the spread test reads the candidate's `flamable`), and is never torn down by
any stamp; so its cell's feature word is constant for the whole session and
the `YardMap 'G'` test is stable. This closes the "wreck transitions at
geothermal vent cells" item.

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

#### R-FEAT-01 §8 — The damage entry, exactly: gates, accumulation, and the network codes [R-FEAT-01] (2026-08-29)

**Established — how impacts reach features.** The projectile impact
dispatcher walks the cells inside the blast radius; for each cell whose
feature word resolves (fringe hop included) it measures the distance from the
impact point to the **instance position** when the cell has an instance,
otherwise to the definition's footprint centre at that anchor, truncates it,
and requires it to be **strictly less** than the blast radius. Anchors are
de-duplicated through a 64-entry list so one blast damages one feature once;
if more than 64 distinct features fall inside a blast, the 65th and later are
**not** de-duplicated and can be hit once per covered cell. The entry is then
called with the **anchor** cell and anchor coordinates. A weapon with
`unitsonly=1` skips the feature walk entirely (also `[06 §9.3]`).

**Established — the entry `(anchorCell, x, z, weapon)`, in order.**

1. A global settings word's bit 3 must be set. The only writer sets it
   unconditionally during startup and nothing clears it, so the gate is
   always open in retail; it is not a lobby or mission option.
2. Feature word `≥ 0xFFFB` → return.
3. `indestructible` → return (before any ignition or accumulation).
4. In a multiplayer session (mission type 3) a client without the
   authority bit sends a 6-byte feature-damage request `(0x0F, weapon
   ordinal byte, x, z)` and returns; the authoritative peer performs the
   steps below and, when the result code exceeds `0xFC`, broadcasts a state
   command `(0x0F, code, x, z)`. Codes: `0xFD` death transition, `0xFE`
   ignition (sent by the ignition routine itself), `0xFF` reclaim transition
   (sent by the payout). Receivers apply the transition or a **remote**
   ignition directly (§9).
5. **Ignition test:** `flamable` **and** `weapon.firestarter ≠ 0` (the
   byte's only reader; no roll). If true and the cell has **no instance**:
   ignite `(x, z, remote = 0)` and return — no damage is dealt. If true but
   an instance is attached, fall through to step 7.
6. **Not an ignition, no instance:** `sum := uint32(weapon.default) +
   uint32(cell.word)` where `cell.word` is the anchor's accumulator (the
   same 16-bit field that holds the slot index when an instance is attached;
   the stamp zeroes it). If `sum < damage` (unsigned 16-bit compare) store
   `uint16(sum)`; else death transition `(x, z, 0)`, code `0xFD`. `damage = 0`
   therefore dies on the first hit of any strength, including a zero-damage
   weapon; return.
7. **3D definition (an instance is always attached):** if the instance's
   recorded anchor differs from `(x, z)` return (a sanity guard — callers
   always pass the anchor); `instance.accumulator (int16) += weapon.default`
   (16-bit wrap); if `damage ≤ instance.accumulator` (unsigned 16-bit) death
   transition at the instance's anchor, code `0xFD`.
8. **Sprite definition with an instance** (burning, dying, or reclaiming):
   nothing — there is no branch, so the impact is discarded.

`weapon.default` is the `[DAMAGE] default` entry of the weapon definition
(int16 as stored), not the per-unit-class entries; a feature never selects a
damage class. No armour, no `edgeeffectiveness` falloff, no minimum: the
full default value lands regardless of distance inside the radius (the
falloff computed for units in the same loop is not applied here).

#### R-FEAT-01 §9 — Ignition, exactly, and the spark-time correction [R-FEAT-01] (2026-08-29)

**Correction.** The "Established fact — ignite" paragraph above says the
countdown is `simulationRandom(sparktime / 2) + (sparktime / 2)` and that
"the shipped spark time of 5 therefore yields a countdown of 2 or 3". That
skipped the parser's scaling: `sparktime` is read as a float, **multiplied
by 30.0** (a double constant), truncated toward zero and stored as int16
ticks — the same seconds-to-ticks convention as every other authored time
`[02 "Feature record"]`. The shipped value of 5 stores 150, and the countdown
below is 75..149 feature-phase visits, i.e. 2.5 to 5 seconds. Everything
else in that paragraph stands.

**Established — `ignite(x, z, remote)`.**

1. The cell must exist and its word be `< 0xFFFB`; the definition must have a
   resolved `seqnameburn`; the cell must have **no** instance. Any failure
   returns silently. There is no `flamable` test here — it is the callers'
   (§8, §11) — and no indestructible test. A 3D definition never has
   `seqnameburn` (the parser skips it), so **3D features never burn**.
2. Pop a free slot (empty pool ⇒ silent return, no broadcast); move it to the
   active list; clear its burning bit.
3. Bind: slot ordinal `:= cell.word`; cell slot index `:= slot`; set the
   cell's instance bit. Start the burn cursor at frame 0; when
   `seqnameburnshad` resolved start the shadow cursor and set the
   shadow-present bit, else clear it. Set the burning bit. Record the anchor
   `(x, z)`.
4. Countdown, one simulation draw:

   ```
   half      = sparkTicks >> 1                      ; uint16 shift of trunc(sparktime × 30)
   countdown = uint8( boundedDraw(half) + half )    ; [01 §7.3]: half < 2 draws nothing and returns 0
   ```

   stored as a **byte**. Consequences: `sparktime < 1/15 s` (ticks 0 or 1)
   gives countdown 0, so the burn event never fires and the feature burns
   without spreading or firing its `burnweapon`; a `sparktime` whose ticks
   reach 256 or more can wrap the byte (an authored edge, absent from the
   shipped corpus whose maximum is 150 ticks).
5. `remote` is stored in the instance's remote bit (bit 3): a remotely
   commanded ignition never runs the burn event (§10 step 4).
6. Play the named sound `treeburn` at the tile **corner** `(x·16, z·16)` in
   16.16 — not the footprint centre.
7. If `remote == 0`, broadcast the 6-byte state command `(0x0F, 0xFE, x, z)`
   (only meaningful in a multiplayer session; the send is unconditional).

Save reload re-ignites a saved burning feature through this routine with
`remote = 0`, so a reloaded burn draws a **fresh** countdown and re-sends the
command; the saved countdown is not restored (the reload copies the saved
accumulator word over the instance after ignition) `[R-SAVE-FEATURE-01]`.

#### R-FEAT-01 §10 — The feature phase, in order [R-FEAT-01] (2026-08-29)

**Established.** Phase 6 of `[01 §4.4]` runs, every tick, these four passes
in this order:

1. **Rest cursors.** For every catalog record, in catalog order, with
   `animating=1`: advance the definition's rest cursor, then its rest-shadow
   cursor (the advance is a no-op for a cursor with no sequence). Advance
   semantics: if the cursor's delay is `< 2`, step the frame; when the frame
   reaches the sequence's frame count, a loop byte of 0 **clears the
   cursor's sequence pointer** (the cursor is finished), otherwise the frame
   wraps to 0; then reload the delay from the new frame's per-frame delay
   word. Otherwise decrement the delay. Rest sequences keep their GAF loop
   byte, so a stock vent loops; a rest sequence authored non-looping would
   stop on its last frame and then vanish from the renderer (its pointer is
   null) — nothing in the simulation reacts.
2. **Reproduction walk** (§12) — at most three simulation draws and one stamp.
3. **Active instance walk**, from the active-list head following each
   slot's next link (the link is read before the slot is processed, so a
   slot that moves lists mid-visit does not break the walk). The active
   list is LIFO: the most recently stamped or ignited instance is visited
   first. Per slot, by definition class and mode bits:
   * **3D instance:** if all three velocity words are zero → move to the
     dormant list (never visited again until re-stamped). Otherwise the
     sinking/falling integration of "Feature sinking and water interaction"
     (§13).
   * **Sprite instance, burning bit clear** (a die or reclaim animation):
     advance the main cursor, then the shadow cursor if present; if the main
     cursor's sequence pointer is now null → `replace(anchorX, anchorZ, 0)`
     (§5 step 6, which promotes to `featurereclamate` through the
     reclaim-animation bit).
   * **Sprite instance, burning:**
     a. if `globalTick % 3 == 0`, emit one smoke particle (`[03 §5.5]` smoke
        producer) at the footprint centre, terrain height, jittered by two
        **CRT-stream** draws `[01 §7.2]` scaled by the current burn frame's
        width and height: `x += (draw·(w/2))/32768 − frame.xoff + w/4`,
        `y += 2·(frame.yoff − (draw·(h/2))/32768) − 2·(h/4)` (integer parts,
        16-bit truncation). Presentation-only; no simulation draw. A third
        CRT draw, the puff's last frame, is taken inside the producer at
        this call — the puff's parameters and the draw order are §16.
     b. advance the burn cursor, then the shadow cursor if present;
     c. if the burn cursor's sequence pointer is now null: look up the
        anchor cell; `teardown(anchor, 0)`; if `featureburnt ≠ 0xFFFF`,
        `stamp(anchor, featureburnt, null, null, 10)`. This is **not** the
        replacement routine: no position or orientation is carried, and the
        successor is placed at the snapped footprint centre.
     d. else if `countdown ≠ 0` and the remote bit is clear: `countdown -= 1`;
        when it reaches 0, run the burn event (§11) once.
4. Nothing else — there is no separate sinking pass; it is the 3D branch of
   pass 3.

**Established — completion is animation-driven, restated with the exact
trigger.** A burn (or a die/reclaim animation) ends on the visit in which the
cursor advance clears the sequence pointer, i.e. the visit after the last
frame's delay expires; the lifetime in visits is Σ over frames of `max(delay, 1)` with the
per-frame delay words from the GAF entry — the asset census under "Feature
burning" already lists the shipped totals.

#### R-FEAT-01 §11 — The burn event, exactly, with the wind-probe skip rule [R-FEAT-01] (2026-08-29)

**Correction.** "Wind embers, exactly five steps" above says "Zero wind
collapses all five probes onto the origin tile, where they are skipped".
The actual rule is more general: a probe is skipped when it lands on the
**same tile as the previous probe** (the origin for the first), so with any
wind slower than half a tile per probe the repeated tiles are skipped and
draws happen only when the tile changes. The five probes therefore make
between zero and five draws depending on wind speed, and a fast wind that
jumps a tile never tests the skipped tile.

**Established — `burnEvent(definition, anchor)`.**

1. **Neighbourhood.** `for dz in −3..+3: for dx in −3..+3:` skip `(0, 0)`;
   the cell must exist, hold a word `< 0xFFFB`, have **no instance**, and its
   definition (no fringe hop — a fringe cell's word is `0xFFFE` and is
   rejected by the sentinel test, so **only anchor cells can catch fire**)
   must be `flamable`; then one simulation draw `boundedDraw(100)`; ignite
   when `draw < candidate.spreadchance` (signed compare of the byte). At
   most 48 draws.
2. **Wind.** `pos := (x << 16, z << 16)`; five times: `pos += 2·wind` per
   axis (the global 16.16 wind vector of `[R-WIND-01]`, doubled by a
   64-bit multiply/shift); `tile := (pos.x >> 16, pos.z >> 16)` as signed
   16-bit; if `tile == previous` skip, else `previous := tile` and apply the
   same legality chain and draw rule as step 1 on that tile (the tile may be
   off-map, which the cell lookup rejects). At most five draws.
3. **Burn weapon.** If `burnweapon` resolved, fire it at the footprint
   centre `((footprintx + 2x)·8, (footprintz + 2z)·8)` at the bilinear
   terrain height, owned by the dummy feature unit (§2), through the
   ordinary weapon request. Unconditional on the spread results.

The event runs once per burn (the countdown stays 0). Draw order within the
tick is fixed by the active-list order of pass 3 and, within the event, by
the loops above, so the simulation stream's advance is fully determined.

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

#### R-FEAT-01 §12 — The reproduction walk's exact target arithmetic, including its axis defect [R-FEAT-01] (2026-08-29)

**Established.** The paragraph above is correct as far as it goes; the
target expression it leaves as "offsets in `±reproducearea/2`" is:

```
cursor    -= 1 ; if cursor < 0 { cursor = W·H − 1 ; return }     ; W, H = map cell width/height
cell       = plot[cursor]
if cell.word < 0xFFFB and cell has no instance:
    if boundedDraw(100) < reproduce                                ; signed compare of the byte; draw always taken
        area = reproducearea
        dx = boundedDraw(area) − (area >> 1)                       ; two more draws, in this order
        dz = boundedDraw(area) − (area >> 1)
        tx = cursor % W + dx
        tz = cursor / H + dz                                        ; sic: divided by the map HEIGHT
        target = plot[tx, tz] (bounds-checked lookup)
        if target exists and cell.occupancyWord == 0 and target.word == 0xFFFF
            stamp(target, cell.word, null, null, 10)
```

The Z of the target is derived from `cursor / mapHeight`, not
`cursor / mapWidth` — confirmed at instruction level (the two divisions use
different divisors). On a square map the two agree; on any other map the
offspring is placed at a Z unrelated to the parent (for a map wider than it
is tall, `cursor / H` exceeds the parent's row and can run off the bottom,
where the bounds check rejects it). Because every shipped feature authors
`reproduce = 0` the defect has no stock effect, but an implementation that
enables reproduction must reproduce it or declare a sanctioned divergence.
`boundedDraw(area)` with `area < 2` returns 0 without a draw `[01 §7.3]`, so
`reproducearea` 0 or 1 costs one draw per eligible visit, not three. The
source cell's occupancy word is the mobile-occupant word `[R-DOC04-B]`, so a
unit standing on the parent suppresses the spawn; the target must be
**exactly** empty — a fringe or void cell rejects. The stamp then applies the
dense-pack rule of §3 to the offspring's whole footprint (which may tear down
neighbours since the target anchor was empty but its fringe need not be).

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

#### R-FEAT-01 §13 — Sinking, exactly: the integration step, the two height queries, and lava [R-FEAT-01] (2026-08-29)

**Established — the 3D branch of the feature phase (§10 pass 3).** For an
active 3D instance whose velocity triple is not all zero:

```
pos.x += vel.x ; pos.y += vel.y ; pos.z += vel.z            ; 16.16 adds, no clamp
floor  = coarseHeight(pos)                                   ; the (hmax + hmin) >> 1 query of [03 §2.3] on the cell now under pos, integer
if (floor << 16) < pos.y                                     ; strictly above the floor
    if pos.y < (seaLevelByte << 16)                          ; strictly below the water plane
        vel.x = 0 ; vel.y = −11468 ; vel.z = 0               ; the latch (−0.175 world units per tick)
    else
        vel.y −= gravity                                     ; the map's 16.16 per-tick gravity global [06 §5.1]; vel.x, vel.z untouched
else
    pos.y = floor << 16 ; vel = (0, 0, 0)                    ; hard snap, fraction discarded
```

The dormant move (all-zero velocity) is tested **before** the integration on
each visit, so a snapped instance is retired on the visit after it lands.
`pos.y == floor << 16` counts as landed (the compare is strict the other
way). `pos.y == seaLevel << 16` exactly is treated as *above* water, so an
instance resting on the plane falls under gravity for one tick before the
latch takes it. Horizontal velocity is preserved while falling in air and
zeroed only by the water latch or the landing snap; nothing in the corpse
path sets a horizontal velocity, so this matters only for the slot-reuse
case in §14.

**Established — the two writers of the velocity, and the reader.** The
only writers are the corpse creator's submerged branch (`vel.y = −11468,
vel.z = 0`, when the dying definition lacks `isfeature`; `vel.x` is **not**
written there) and this branch. The only reader is this branch. Doc 06's
"sinking-rate patch" is that write; its reader is the block above.

**Established — lava has no rule here.** None of the corpse creator, the
stamp, the teardown, or the feature phase reads the world's lava flag; the
only medium test anywhere in the feature paths is `seaLevelByte`, so on a
lava world a corpse whose terrain is at or below the "sea" level sinks into
the lava exactly as into water — silently (no splash, no smoke column, no
sound), at the constant rate, to the coarse floor — and a wreck above it gets
the ordinary land smoke column `[R-LAYER §3]`. Lava-specific presentation
(the strip producers' medium selection) is doc 03's; there is no lava-side
simulation difference for features. `sinktime` is not a key the executable
knows (reader census: none); the rate is the constant above.

**Established — the corpse creator's chain and stamp** (restating the
handoff from `[06 §12.2]` at feature precision). The corpse ordinal is the
unit definition's resolved `corpse` (§2's lookup); the creator follows
`featuredead` `depth − 1` times (depth from the `Killed` script's result),
stopping silently on any sentinel; then stamps at the unit's plot cell with
the unit's exact position triple and orientation triple and the owner's
player index as the placer nibble. The dense-pack rule applies: the corpse
tears down every non-indestructible feature under its footprint and is
silently not created over an indestructible one, a void cell, or when the
slot pool is empty (§3). There is one attempt.

#### R-FEAT-01 §14 — Slot reuse: what a new 3D instance inherits [R-FEAT-01] (2026-08-29)

**Established.** The stamp writes an instance's ordinal, accumulator,
anchor, position, orientation and model handle, and nothing else; the pool
is zero-filled once at session start; the free list is LIFO; teardown pushes
without clearing. Therefore a newly stamped 3D instance carries, in every
word the stamp does not write, whatever the slot's previous occupant left:

* **Velocity.** A successor stamped by the replacement routine (§5) or by
  the burn completion reuses the slot its predecessor just freed and so
  keeps the predecessor's velocity — this is the mechanism behind the
  sentence in "Feature sinking and water interaction" that a destroyed
  sinking wreck "hands its submerged position — and typically its stale
  downward velocity — to its successor", which stands. A settled (dormant)
  wreck's successor starts with zero velocity, is retired on its first
  visit, and rests where the predecessor rested.
* **Sprite cursors overlap the velocity words.** A burning, dying, or
  reclaiming sprite instance keeps its main cursor in the bytes a 3D
  instance uses for the model handle and position X/Y (all rewritten by the
  stamp), and its shadow cursor in the bytes used for position Z
  (rewritten), `vel.x` (its loop byte and padding) and `vel.y` (the shadow's
  sequence pointer); `vel.z` is outside both cursors. A finished shadow cursor has a null pointer; a
  shadow cursor whose owner was **torn down mid-animation** (a corpse or
  reproduction stamp colliding with a burning tree) leaves the pointer in
  place. The colliding stamp pops that very slot. On a land cell the corpse
  path does not write `vel.y`, so the corpse starts with `vel.y` equal to a
  code-space pointer value — a large positive 16.16 velocity — and rises
  under gravity until it falls back and snaps. **Supported inference** as to
  the observable (a wreck that briefly launches upward when it lands on a
  burning tree with a shadow sequence); established as to every step of the
  mechanism. Decider: manual retail observation with an authored probe (a
  unit killed over a burning `Tree1`), or a static trace showing a writer of
  those words that this unit did not find. Nanolathe must zero the velocity
  triple at stamp unless it chooses to reproduce this.
* **Mode bits.** The stamp clears only the burning bit; the reclaim-animation
  and remote bits persist from the previous occupant. For a 3D instance the
  reclaim-animation bit is read by the replacement routine (§5 step 6), so a
  3D wreck placed into a slot last used by a *reclaim* animation would, on
  its later death, be replaced by `featurereclamate` instead of
  `featuredead`. Established mechanism; the observable is a Supported
  inference with the same deciders.

#### R-FEAT-01 §16 — The burning-feature smoke puff: its parameters, its three draws, and where the jitter lands [R-FEAT-01] (2026-09-02)

§10 pass 3a gives the jitter arithmetic; this closes the three things it
left open (RWU-19-16). Doc 03's open-list item "the strip-5 burning-feature
smoke producer's puff parameters (variant, life)" is closed by it.

**Established — the producer and its parameters.** The phase-6 site calls
the strips-5/9 smoke-puff producer of `[03 R-STRIP-01 §1]` with the strip
literal **5** and the init row `(frameCap 0, spawnInterval 1, frameHold 0,
lifetime 0, selector 0)` — the same row `[06 R-WFX-01 §5]` gives the trail
puff, the timer-expiry puff and the impact `endsmoke`. In words: the puff is
`smoke 1` (selector 0); it may use every frame of that entry (no cap); its
hold is the default 7; and the container's lifetime is 0, so the container
is a one-shot — its constructor spawns exactly one puff, its spawn gate never
fires again (`nextSpawn = tick + 1` is past the deadline `tick`), and the
removal verdict retires the container as soon as that puff reaches its last
frame `[03 R-STRIP-01 §2]`. Every third tick of a burn therefore adds one
puff in one fresh container, never a container that keeps spawning.

**Established — three CRT draws per emission, all in phase 6.** In order:
the horizontal jitter draw, the vertical jitter draw (both at the call site,
§10 pass 3a), then — inside the producer, because the family's init calls
its spawn virtual — the puff's last-frame draw
`crt · (frameCount − 2) / 0x8000 + 2` of `[03 R-STRIP-01 §2]`. The per-tick
hold redraws that follow are phase-11 work and are counted there.
**Correction to `[01 §7.5]`'s phase-6 CRT row**, which read "2 per
fire-effect emission (position jitter)" and cited §12: the row counted the
call site only and missed that the constructor's draw is taken at the call,
not at the puff's first phase-11 visit; it now reads 3 and cites §10 and
this section.

**Established — the jitter moves the container's world X and world height.**
The site builds the position triple (X, Y, Z) from the footprint centre
`((footprintx + 2·anchorX)·8, (footprintz + 2·anchorZ)·8)` in whole units,
takes Y from the bilinear terrain height at that centre (the same height
helper the corpse stamper's land/water test uses, `[06 §12.2]`), then adds
the first addend of §10 pass 3a to the **whole part of X** and rewrites
**Y** as the 16-bit truncation of `terrainHeight + 2·(frame.yoff −
draw₂·(frameHeight/2)/32768) − 2·(frameHeight/4)`; Z is passed through
untouched. It is the container's world position that is jittered — the
puff spawns there, and its own update then drifts it by the wind and gravity
words `[03 §5.5]`. The reading recorded at the implementation site as
*Supported inference* (X and Y are the two jittered words, Z stays at the
centre, the factor of two on Y is the half-height projection shear) is
confirmed and is now Established; the `TODO(question)` there is retired.

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

**Correction (2026-08-29, RWU-05-2).** The producer questions this unit
owned — the `solarstrength` reader census, the wind range's source and the
wind phase's cadence and draws, the tidal source, the exact extraction walk
and accumulator, the maker byte and `onoffable`, upkeep timing, the cloak
debit's cost selection and player gate, and storage eligibility — are closed
under [R-PROD-01 §1]–[R-PROD-01 §8]. Two bullets that would otherwise have
been added are cross-document handoffs, not unknowns: the legacy terrain
attribute record's eight-byte layout (metal byte at offset 6) and the legacy
header's wind-range words belong to `[fmt tnt]`, and the wind speed roll's
no-draw exception when the range is below two belongs in [01 §7.3]'s census.
One bullet is added below for the wind heading's pre-roll value.

**Correction (2026-08-29, RWU-05-3).** The reversed-argument repair bullet is
removed: the variant is the `SelfRepair` order and its malformed-input behavior
is stated in [R-WORK-01 §3], so nothing about it is open. Four bullets replace
it, all raised by the work-handler pass and none of them a restatement of a
closed finding.

**Correction (2026-08-29, RWU-05-4).** Three bullets are removed as closed
under [R-SHARE-01]. The sharing-residuals bullet asked for share-buffer refill
rules that do not exist (the clamp is against live stock), for the state-2
discount scalar (0.5 / 0.7 in double, applied once at credit), and for the
predicate names (row A of the giver, remote-human candidates only) —
[R-SHARE-01 §1]–[R-SHARE-01 §4]. The limit-field bullet's two "bounded
negatives" were both wrong: the parser writes the -1 default and the
multiplayer restriction dialog is the writer; `norestrict` has two lobby
readers — [R-SHARE-01 §9]. The control-byte bullet is closed: 1 is the local
human and 3 the remote peer — [R-SHARE-01 §1]. Two claims in the body were
corrected in place: the unit pool is sized by the per-player unit limit, not
the catalog count, and `maxunits` is that limit's campaign source
[R-SHARE-01 §7]; and the automatic-share thresholds are zero, not derived
from capacity [R-SHARE-01 §3]. Four bullets are added below.

**Correction (2026-08-29, RWU-05-5).** Five feature bullets are removed as
closed under [R-FEAT-01]: the reclaim payout's two "protection" bits are the
anchor's instance-attached bit and the definition's sprite bit
[R-FEAT-01 §15]; same-tick precedence is closed by the instance-bit
serialization and the phase order [R-FEAT-01 §5]; the "malformed burn
animation" loader question narrows to the bank lookup's miss return (kept
below) because a definition without `seqnameburn` simply cannot ignite and a
3D definition never has one [R-FEAT-01 §1][R-FEAT-01 §9]; "catalog
exhaustion" does not exist (the catalog is reallocated per record) and the
pool-exhaustion order is established — collision teardown precedes the pool
check [R-FEAT-01 §3]; and wreck transitions at vents are governed by the
dense-pack rule alone [R-FEAT-01 §7]. Two claims elsewhere in this document
were corrected in place: the ignition countdown's spark time is in ticks
(`sparktime × 30`, truncated), not in authored seconds [R-FEAT-01 §9], and
`autoreclaimable` is not the teardown honor bit [R-FEAT-01 §4]. One claim in
[R-PROD-01 §6] was corrected by [R-FEAT-01 §7]: indestructible metal
features seed the plot metal byte after the uniform seed. The bullets below
are the residue.

**Correction (2026-08-29, RWU-05-6).** One bullet is removed as closed: the
jump-table fragment that prints `Unable to create any more units` with
result 8 and no wait is `VTOL_MobileBuild`'s in-reach phase, traced and
named in [R-ECO-02 §4]. No bullet is added: the ledger-closure pass wrote
its findings inline ([R-ECO-02 §1]–[R-ECO-02 §3]) and its one open question
— which init parameter each of the steam producer's three literals binds to
— is a doc 03 effects-class question recorded in [R-ECO-02 §3] with its
decider, not an economy unknown. The "per-tick order of produced, consumed
and wasted accumulation" question that older plans list against this
document is closed by [R-ECO-01 §6] (energy before metal, the four per-pass
fields before the pool fold, waste only at the strictly-greater clamp) and
has no bullet here.

**Correction (2026-09-01, RWU-19-5).** No bullet is removed or added:
the capture executor's "cloud of vapor" predicate was never listed here.
[R-WORK-01 §10] states its operand (the remaining-build fraction) and its
constant (literal single-precision zero), retiring the build's proxy marker,
and records one narrower unknown inline — whether the order-side target
validation excludes same-owner and death-latched capture targets before the
executor's phase 0 runs.

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
- ~~The consumer of the `WinLoseTime` sibling deadline beyond its save key~~ — closed 2026-09-02 (RWU-19-31): none exists beyond the save reader/writer ([08 "Player records"]);
  the end-condition poll's due is `UpdateTime` (RWU-19-32, [R-ECO-01 §1]);
  `DisplayTimer`'s sole consumer is closed by [R-ECO-01 §6], and the two
  settlement status-pair fields are named by [R-ECO-01 §12] (the live-unit and
  units-ever-created counters) · "Authoritative settlement order" · static
  trace.
- Whether any reader of the per-unit archived economy snapshots exists outside
  the ledger's own redistribution; bounded-negative in the reviewed image
  · "Authoritative settlement order" · static trace over the unrecovered
  regions.
- Meaning of the second status bit the cloak gate requires to be clear: it has
  no writer in the complete decompiled function set, so the term is inert and
  the behavior is unobservable · "Cloak debit" · static trace over the regions
  the export still misses. The rest of the gate, the truncation, and the
  inclusive affordability compare are closed by [R-ECO-01 §9].
- Value of the global wind heading before the first non-zero speed roll — it
  reaches wind-generator scripts through `SetDirection` on a map whose first
  roll is zero; inferred zero from fresh allocation · "Wind generation",
  [R-PROD-01 §3] · static trace of the global block's allocator zeroing.
- Whether any settlement intermediate can reach the range where the 53-bit
  precision control's wider exponent differs from a double's: no economy
  magnitude was found that does, and the claim is a stated edge rather than a
  traced one · "Two-stage settlement algorithm" · static trace, or manual
  retail observation with an authored extreme-cost probe. Exceptional-value
  behavior, signed zero, the truncation sites, and the float-versus-double
  widths are closed by [R-ECO-01 §1] and [R-ECO-01 §5].
- Meaning of the unit status word's low two bits, which unit sharing tests
  against `2` to skip a selected unit · [R-SHARE-01 §5] · doc 04's status-word
  census.
- Effect of a `norestrict` definition's untouched restriction node on the
  lobby side (seeded limit `-1`, or `0` when `wacky`; `enable` word at its
  seed value) · [R-SHARE-01 §9] · static trace of the node seed's `enable`
  word and of the synchronization acknowledgement; multiplayer lobby, out of
  scope for the simulation.
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
- Presentation clipping of sunken wrecks · doc 03 · static trace.
- Which statistic and UI fields derive from the economy, and which are
  authoritative totals versus presentation-only cached values · doc 07 ·
  static trace. The live-stock and pass-counter HUD readers are partially
  enumerated.

- What the feature parser's field reads do with a null section handle after
  `Record "%s" missing from feature files` — the record is appended and the
  parser continues; a fault is the likely outcome but is not traced
  · "Catalog construction", [R-FEAT-01 §1] · static trace of the TDF getters'
  null-handle path. Reached by a bad `featuredead`/`featurereclamate`/
  `featureburnt` name, a bad FBI `corpse` name, or a bad mission-file feature
  name.
- What the GAF bank lookup returns for a `seqname*` value naming an entry
  absent from the bank (zero, which the code treats as "no sequence", or a
  fault) · "Catalog construction", [R-FEAT-01 §1] · static trace of the
  sequence lookup's miss path; the shipped corpus has no such case.
- Identities of the two table-dispatched handlers that consume the
  area-reclaim candidate scan (the builder's area-reclaim order body and the
  computer player's) — the scan's predicate is established, the consumers
  are a Supported inference · [R-FEAT-01 §6] · static trace of the order
  descriptor table (doc 04 §3.1) and the AI task table (doc 08).
- The reader of the copy of the reclaim flag the death/reclaim transition
  stores in the instance's bit 4 — none found · [R-FEAT-01 §5] · static
  trace over the unrecovered regions.
- Whether the slot-reuse observables — a land corpse launched upward by a
  stale shadow-cursor pointer, and a 3D wreck replaced by `featurereclamate`
  because its slot's previous occupant was a reclaim animation — occur in
  play; every step of the mechanism is established · [R-FEAT-01 §14] ·
  manual retail observation with an authored probe (a unit killed over a
  burning `Tree1` that has `seqnameburnshad`), or a static trace finding a
  velocity-word writer this unit missed.
- Meaning of the per-hit feature flag-word copy the projectile hit test
  stores in a global for the script layer (it is the whole flag word; which
  bits the consumer reads is a doc 04 COB question) · [R-FEAT-01 §1]
  `blocking` census · static trace of that global's script-port consumer.

#### R-FEAT-01 §17 — Bootstrap fringe synthesis: every covered cell becomes fringe, whatever the TNT word says [R-FEAT-01] (2026-09-02)

§3 step 7 says every covered cell other than the anchor receives the fringe
mark `0xFFFE`. A repository test encoded the opposite for map bootstrap — that
a covered cell whose authored TNT word is `0xFFFF` (empty) stays empty and
only cells the TNT itself marks `0xFFFE` become fringe. Traced RWU-19-25
(static: the terrain loader's plot allocation and its two attribute passes;
the stamp's fringe loop). Everything below is **Established**.

**The loader never copies the TNT feature word into the plot.** The plot
grid is allocated with every cell's feature word set to `0xFFFF`, its slot
word zero and its metal byte seeded. Pass 1, row-major over the TNT attribute
array: the cell's height byte is copied; the control byte's bits are set to
the loader's constant; and when the authored word is the void value the void
marker is stamped (§3 step 1). Pass 2, row-major again and **skipped when a
save file is open** (the reload path stamps from the save instead,
[R-SAVE-FEATURE-01]): when the authored word is **strictly below the format's
reserved band** — `0xFFFB` for the word-attribute TNT layout, `0xFC` for the
byte-attribute layout — the stamp is called with that ordinal, a null
position and nibble 10. Then the mission-file list (§3). The authored words
`0xFFFE`, `0xFFFF` and the rest of the reserved band are never examined.

*Correction to §3 "map-load placement".* Previous text: "any ordinal below
the table count → stamp with a null position". The bound is the reserved
band, not the compiled table count; a word below `0xFFFB` (or `0xFC`) but at
or above the count is handed to the stamp unchecked. What the stamp does
with such a word is outside this trace (Unknown; no stock map was checked
for one).

**The stamp's fringe write is unconditional.** After the dense-pack loop
(§3 step 3) and the anchor write (steps 4–6), the stamp walks the footprint
`dz` outer, `dx` inner, and for every cell except `(0, 0)` writes: feature
word `:= 0xFFFE`, offset bytes `:= (dz, dx)`, instance bit cleared. It reads
nothing from the cell first — neither its current feature word nor anything
authored. The per-cell rule is exactly: *covered and not the anchor → fringe,
always.*

**Consequences at bootstrap.**

1. Because the grid starts all-empty and the TNT word is never copied, a
   covered cell authored `0xFFFF` is stamped `0xFFFE` exactly like one
   authored `0xFFFE`. The authored fringe words are redundant data; retail
   derives fringe entirely from the anchors' footprints.
2. An authored `0xFFFE` that no stamped footprint covers is never written and
   stays `0xFFFF` — the same outcome an implementation reaches by clearing
   uncovered raw fringe after the pass.
3. A covered cell whose authored word is itself an ordinal is a second
   anchor, stamped later in row-major order from the attribute array (not
   from the grid, which by then holds the first feature's fringe); its
   dense-pack loop tears the first feature down (§3-A). The later anchor
   wins, as §3 says.

An implementation that leaves an authored-empty covered cell empty diverges
from retail in every reader that hops a fringe to its anchor — the
passability classifier `[R-DOC04-B]`, the reclaim scan (§6), the damage
entry (§8) and the teardown (§4): on such a map those cells are neither
blocked, reclaimable nor cleared with their feature. The repository test
that asserts the empty outcome encodes a non-retail premise and its
bootstrap must write fringe over every covered cell.
