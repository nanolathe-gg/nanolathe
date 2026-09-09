# Retail executable specification: weapons, projectiles, damage, and effects

This document records the current clean-room behavioral contract recovered from the retail executable. It describes logical state and observable simulation behavior, not binary layout. It contains no executable addresses, memory offsets, decompiler identifiers, or implementation code.

Evidence labels:

- **Established fact** means direct support from static control and data flow in the retail executable, or from a bounded census over it.
- **Supported inference** means the behavior follows from direct call order and field consumers, but a semantic link is not fully proven.
- **Unknown** means the available retail evidence does not close the behavior.

Renderer mechanics are intentionally excluded. Sound, smoke, explosion, feature-fire, and camera/effect events are described only where the simulation emits them.

## 1. Combat prerequisites and phase contract

### 1.1 Simulation time

**Established fact:** Combat runs in the same 30 logical ticks-per-second simulation as units and orders. Weapon timers, burst intervals, projectile durations, smoke delays, flight times, and similar short fields are consumed as tick counts after catalog conversion. The authoritative tick is not a frame count.

**Established fact:** Unit weapon-slot work happens during the unit phase,
before the ordinary projectile phase. A root projectile allocated by a
successful weapon-slot fire is therefore eligible for the projectile phase of
that tick.

**Established fact:** The projectile phase captures the active-span count once
at entry. A zero-burst root allocated by the unit phase is inside that span and
is eligible for displacement and collision in its creation tick, subject to
family-specific launch and expiry state. Records appended while the projectile
phase is iterating, including burst clones, lie outside the captured span and
first move in the following tick. Projectiles emitted by the earlier network
event phase are also inside that tick's captured span; projectiles emitted by
later effect or meteor work wait for the next projectile phase.

### 1.2 Three weapon slots

**Established fact:** Every ordinary unit has up to three weapon slots: primary,
secondary, and tertiary, stored as three contiguous slot records of about
24 bytes of logical state each (28-byte stride in the executable image).
Each record contains a resolved weapon definition pointer, an armed/has-target
flag, an Aim-request latch, a tracking flag, an encoded target (a unit slot
index when the sentinel value -0x8000 is present, otherwise a ground point
with world X and Z words that later resolve to height through the terrain
query), desired yaw and pitch, a signed reload countdown in ticks, and a
stockpile remainder byte where applicable, plus firing and out-of-range status
bits. The slot selects a muzzle piece through a synchronous COB query; the
query path itself — the AimFrom/Query fallback, the deferred Aim command, and
the SweetSpot target query — is specified in §3.4 [R-P0-07].

**Established fact:** The slot pipeline visits slots in numeric order. For a
populated slot it decrements nonzero reload, resolves the current target,
optionally dispatches Aim (choosing the direct line-of-sight solver or the
ballistic solver), and then falls through to shot-time range/medium/ballistic
admission. If reload is zero and the physical gate passes, it prechecks normal
resources or stockpile ammunition and calls the family spawner. On successful
return it stores reload or decrements ammunition, sets the unit firing state,
and finally debits normal-weapon resources. Projectile allocation and, for
families that emit them, Fire/RockUnit callbacks occur inside the spawner
before those success mutations.

**Established fact:** A failed projectile allocation does not call Fire or RockUnit, does not debit firing resources, and does not advance the ordinary reload state. A pool-full failure is therefore observable at the slot level.

**Established fact:** Every latch writer that installs a new target preserves
the previous Aim-request latch; replacement while an Aim is outstanding keeps
the stale yaw and pitch for the next shot rather than clearing them. The next
shot therefore uses the new target position with the old angles for one firing
attempt before the latch is cleared or recomputed.

## 2. Weapon catalog and logical flags

### 2.1 Definition fields

The weapon catalog is data-driven. The executable parses and retains at least the following logical properties:

- model and explosion/water-explosion effect identity;
- weapon velocity, start velocity, and acceleration;
- range and separate interceptor coverage radius;
- reload time and weapon-expiry timer;
- turn rate;
- burst count and burst interval;
- spray angle and random decay;
- projectile duration, smoke delay, flight time, and hold time;
- accuracy, tolerance, and pitch tolerance;
- start, hit, water, and trigger sound identities;
- behavior flags listed below.

**Established fact:** The weapon record's slot in the catalog is selected by its authored `ID` key, read as an integer with a default of -1 before any other field. The section name is stored into the selected record as the weapon's catalog name, and a separate `name` key supplies the display string.

**Established fact:** Catalog numeric values are converted at compile time with the exact conversions given in document 02: velocities are multiplied by 65,536/30 and truncated, giving 16.16 world units per tick; acceleration is multiplied by 65,536/900, giving 16.16 world units per tick squared; every authored duration — reload, weapon timer, burst rate, duration, random decay, smoke delay, flight time, hold time, shake duration — is multiplied by 30 and truncated to whole ticks, so an authored value below one thirtieth of a second compiles to zero; turn rate is multiplied by one thirtieth; and minimum barrel angle is converted from degrees to radians with a default of -11.25 degrees. Range defaults to 32,767.

Most projectile timers are then compared directly with the logical tick. Reload time additionally has a script/UI millisecond conversion path, but authoritative slot reload uses the compiled integer value.

### 2.2 Behavior flags

The following logical flags are established from parser writes and consumers:

- line-of-sight;
- ballistic;
- shell;
- beam;
- vertical-launch;
- meteor;
- no-radar;
- paralyzer;
- dropped;
- start-smoke;
- end-smoke;
- sound-trigger;
- guidance;
- tracks;
- units-only;
- ground-bounce;
- water weapon;
- to-air weapon;
- smoke trail;
- turret;
- self-propelled;
- propeller;
- no-explode;
- burn-blow;
- two-phase;
- cruise;
- command-fire;
- no-auto-range;
- stockpile;
- targetable;
- interceptor.

**Established fact:** These flags are composable predicates, not a one-to-one family enum. Creation and active-record motion use separate ordered tests. Guidance, tracks, cruise, propeller, and command-fire have distinct consumers and must not be collapsed into a single missile subtype.

## 3. Target acquisition, retention, and fire eligibility

### 3.1 Target categories and the per-side candidate lists

**Established fact:** Authored category expressions are compiled to **bitsets
indexed by unit-definition index**, not evaluated as strings at runtime. Each
**unit** definition carries four such bitsets — three indexed by weapon slot
and one for no-chase behavior — and each is an array of 32-bit words tested as

```
inMask = mask[defIndex >> 5] & (1 << (defIndex & 31))
```

where `defIndex` is the **candidate's** definition index, a per-unit word that
is cleared to zero when the unit dies. **Supported inference:** the three
per-slot masks are the compiled form of the authored `wpri_`/`wsec_`/`wspe_`
category expressions and the fourth of `nochasecategory`; the compile step
belongs to `[02 "Unit record"]` and this document has traced only the four
runtime consumers (acquisition bucketing, autonomous retention, the Guard
handler's replacement test, and the sight-distance caller's no-chase filter),
not the parser that fills them. *Decider:* static trace of the unit-record
parser's category compilation. A candidate clear of the slot's mask
enters the preferred bucket; a matching candidate enters the fallback bucket.
Any preferred result wins over fallback. Retention is stricter and rejects a
retained unit whose definition index is in that bad-target mask. Unit traversal
naturally excludes map features because features are stored in a separate
feature system.

**Established fact:** Automatic acquisition never scans the unit array. It
draws from a **per-side target registry** — one object per player slot, holding
two candidate lists plus a per-definition census, a weighted centroid and a
gate flag — which is rebuilt from the whole unit array on a cadence, and from
which each acquisition attempt filters a fresh array. The rebuild is gated by

```
if (registry.lastRebuildTick + 30 <= currentTick) { rebuild; registry.lastRebuildTick = currentTick;
                                                    if (simulationRandom(30) == 0) refreshStrategy() }
```

so the lists are rebuilt **at most once per 30 ticks per side**, from the
per-player phase, and each rebuild consumes exactly **one simulation draw**
(bound 30) whose zero outcome additionally runs the strategic refresh
`[08 "Strategy manager and its task graph"]`. The 30-tick registry **rebuild**
cadence and the independent per-tick round-robin **scan** throttle (§3.2) are
two separate mechanisms `[R-WPN-02 §1]`. An acquisition can therefore see a
list up to thirty ticks stale, including entries for units that died in
between — which is why the per-attempt filter re-tests liveness.

**Established fact:** The rebuild *is* the strategic refresh, and every human
or computer slot takes its draw. `[08 R-AI-01 §16]` names the
30-tick strategic refresh's three input vectors as non-allied live units
passing the ordinary visibility predicate, non-allied units carrying one
further runtime status bit, and own active builder-plus-air-base units —
this section's primary, secondary and third lists — and names that refresh
as the writer of the targeting-upgrade flag, the census and the centroid. The
list builder has exactly one live caller, the per-slot cadence gate above, and
that gate is the routine `[08 R-P0-05 §6]` describes. The per-side target
registry and the strategic state are one object per player slot, the registry
rebuild and the strategic refresh are one routine, and the bound-30 draw here
is the one draw `[08 R-AI-01 §16]` records. (A second copy of the cadence gate
that takes the state pointer directly and omits the null test exists in
the image and has no caller.) An implementation that keeps the lists in a
combat module and the census in an AI module must take that draw exactly
once per side per rebuild.

*Which slots draw.* The per-player phase walks the
ten slots in ascending order **every tick**; a slot is visited when its
record exists, its controller byte is `1`, `2` or `3`, and its own-slot
byte (the alliance-row index of `[08 R-AI-01 §9]`) is not the unassigned
value `10`. For a visited slot, in order: the manager tick runs when the
slot's AI manager record exists (the computer-only gate is inside the
manager, `[08 R-AI-01 §1]`); then the cadence gate runs when the slot's
**strategic state** exists — the gate is null-checked, never
controller-checked; then the slot's per-unit visits. The strategic state
is constructed by the per-player reset for every slot whose controller is
not `3` (remote), human slots included (`[08 R-ENTRY-01 §3]` step 24:
"Humans get an AI record too; only remote peers do not"), and its
constructor seeds `lastRebuildTick` to `0`, so the first rebuild of every
such slot fires at tick 30 — the tick-0 priming finds `0 + 30 <= 0` false
and draws nothing. The local human's slot therefore takes the bound-30
draw exactly as a computer slot does, on the same ticks, and a
single-player game of one human and one computer takes **two** bound-30
draws per thirty ticks, in ascending slot order, each between that slot's
manager tick and its per-unit visits. A remote slot takes none. Taking the
draw only for computer-controlled slots, or once per battle instead of
once per slot, would diverge from retail; taking it from a per-slot
manager tick that runs for every human and computer slot does not.

**Established fact:** One rebuild walks the entire unit array once, in slot
order, and classifies each unit whose alive bit is set and death latch is
clear:

* **hostile** — the candidate's owning player's alliance row, indexed by *this*
  registry's ally group, reads zero:
  * it joins the **primary list** when the direct-visibility predicate below
    accepts it **and** a runtime exclusion status bit is clear — bit 15 of
    the status word, the mission `Immunity` bit, named below;
  * it joins the **secondary list** when its runtime *seen* status bit is set.
    The two tests are independent, so a unit can be on both lists, either, or
    neither.
* **own** — the candidate's owner slot byte equals the registry owner's own
  slot byte — and fully built: it is counted into the
  per-definition census, into an economy counter when its definition carries
  the corresponding scalar, and into the weighted centroid; and it sets the
  registry's **secondary-list gate** when its definition carries
  `istargetingupgrade` and the unit is active ([04 R-SPEC-01 §8], which also
  states the enumeration that reads the gate).

*The third list.* The same own-unit
branch also fills a **third list**, cleared with the other two at every
rebuild: every fully built friendly unit whose definition carries both
`builder` and `isairbase` and whose activation bit is set, in unit-array
order. It is the candidate set of the damaged-aircraft base seek — the
"base candidates within `0xF00`" of [04 R-AIR-01 §7] — and its filter, pick
and callers are [04 R-AIR-01 §11]. No weapon or acquisition path reads it.

**Established fact:** The primary-list exclusion bit is
the mission `Immunity` bit — **bit 15** (`0x8000`) of the 32-bit unit status word — the word whose seen,
sonar and jammed bits are `[03 §3.2]`'s, whose alive bit and death latch
the walk tests first, and whose bit 5 is the selectable bit. Its writers,
by whole-image census: the mission-unit spawner, which copies the placement
record's `Immunity` flag (flag-byte bit 7, `[08 R-TRIG-01 §9]`) into it at
creation; the save loader's unit restore, which rewrites it from the packed
saved status word; and the `MakeSelectable` order handler, which clears it
while setting the selectable bit (`[04 R-ORD-01 §2]`; InitialMission's `s`
verb and its postlude queue that order). The common allocator initializer
leaves it clear, so a unit built during play never carries it. Its readers
are exactly two: this list builder, which keeps an immune hostile off every
side's **primary** list — the seen-bit test for the secondary list is
unaffected, so an immune unit the local observer can see remains a
fallback candidate for a side whose secondary gate is open — and the
computer player's nearest-hostile helper (`[08 R-AI-01 §9]`), which skips
it. The bit is otherwise unread: it is not damage immunity, not a
selection or rendering state, and not the classifier's eligibility bit
(bit 5). Contract: a mission unit placed with `Immunity=1` is not
auto-acquired through the primary list and is never a wave's nearest
hostile until an `s` / `MakeSelectable` order clears the bit.

**Established fact:** The per-attempt filter is much thinner than the rebuild.
Given a centre point and a radius it walks the primary list, keeps every entry
whose **planar** squared distance is at or below the squared radius and whose
alive bit is set and death latch is clear, and appends it. Only when the
registry's secondary-list gate is nonzero **and the resulting array is still
empty** does it repeat the walk over the secondary list. No visibility,
category, sensor, medium, alliance or range test happens at this point — the
visibility predicate ran at rebuild time, and category is applied later, at
bucketing (§3.2). If primary candidates existed but all failed the later
category or scoring steps, the secondary list is **not** retried.

**Established fact:** The squared-distance metric used by the filter, by the
acquisition gate below and by the scoring draw is the same throughout combat:
for each axis the signed 32-bit 16.16 delta is squared into 64 bits and the
**high 32 bits** are taken — the squared whole-world-unit distance, truncated
per axis — and the axis terms are summed as signed 32-bit values. The radius is
squared as a plain 32-bit signed multiply of the authored integer.

**Established fact (secondary-list identity):** The runtime bit that puts a
hostile unit on the secondary list is the
**seen** bit of the unit status word, and it is recomputed every tick by the
sensor bookkeeping phase from the **viewing** player's point of view only —
the local human's slot except in an observer session, which the two must not
be collapsed into one `[03 R-VIS-01 §4]`. That phase is **five** ordered
passes `[03 R-VIS-01 §4]`; four of them write the seen bit: pass 1 clears it
for every unit that is not own/allied-with-shared-vision and sets it (together
with the sonar bit) for those that are; pass 2 sets it for units inside a
**viewing-side** radar circle, whose test radius is
`radardistance + 2 × (the emitter's world Y high word — its altitude in whole
world units, not its model height)`, and sets the sonar bit for units at or
below the water plane inside a sonar circle; pass 3 clears it and sets a jam
bit for units inside a hostile radar-jam circle; pass 4 is the minimum-cloak
proximity scan, which writes no seen bit; and pass 5 sets it for any remaining
unit whose projected tile is lit in the viewing player's line-of-sight state.
So the secondary list is exactly *"hostile units
the local observer can currently see or detect"*. Three consequences are
contracts:

1. it is **radar-like** because radar coverage is one of its four producers,
   but it is not a radar list — allied units, sonar contacts and plain
   line-of-sight all set the same bit;
2. `radardistancejam` **does** have an authoritative effect: it clears the same
   bit and therefore removes the candidate from every side's secondary list
   until the line-of-sight pass or an allied-vision pass sets the bit again
   later in the same tick `[R-WPN-02 §4]`. Jamming has no authoritative effect
   on the minimap surfaces, where overlap is last-writer-wins and never ORs
   into the word mask;
3. because the phase evaluates one observer, every side's secondary list is
   computed from the **local** player's sensors. In single player that is the
   human's view, and a computer opponent's fallback acquisition therefore
   inherits it. **Unknown:** whether any second producer of that bit exists
   outside the recovered sensor phase; *decider:* static trace over the
   unrecovered regions.

**Established fact:** The registry's secondary-list gate is set by owning at
least one **active** own unit whose definition carries the flag bit of the
definition flag word that stores the key **`istargetingupgrade`**
([04 R-SPEC-01 §8]). Its reader is the list builder — not the acquisition and
not the scan. Three properties are contracts:

1. *Nothing is aggregated.* The rebuild clears the gate word and then writes
   the constant `1` into it for each qualifying unit; there is no counter and
   no per-definition total. A clone that keeps a count and tests it for
   nonzero is equivalent; one that reads the **shooter's** definition is not —
   the shooter plays no part. The gate belongs to the registry, i.e. to the
   scanning player, and is the same for every slot of every unit that player
   owns.
2. *The counting branch means the same player, not the same ally group.* The
   census, the economy counter, the centroid, the third list and this gate are
   all reached only when the candidate's owner slot byte **equals the registry
   owner's own slot byte**. A unit of an allied player is neither hostile (its
   alliance-row entry is nonzero) nor own, and is skipped entirely — so an
   ally's targeting-upgrade unit never opens this gate for you. The hostile
   test is the registry owner's alliance row, indexed by the candidate's owner
   slot, reading zero.
3. *The qualifying unit must be alive, not death-latched, complete (build
   fraction exactly zero) and activated (state byte bit 0), with
   `istargetingupgrade` on word A bit 10 of its definition.* Paralysis does
   not clear the activation bit, so a stunned upgrade still counts.

The per-attempt filter consults the secondary list exactly as stated above,
and applies to it the **same** test as the primary walk — planar `d² ≤ r²`
on the truncated whole-unit metric, alive bit set, death latch clear — with no
visibility re-test: the secondary list was populated from the *seen* bit at
rebuild, and that is the only sensor test it ever receives.

**Established fact:** The direct-visibility predicate applied at rebuild time
takes the observing player record and the candidate and answers in this order:

1. the candidate's owning player **is** the observer — accept (own units are
   never hidden from their owner);
2. the candidate's cloak bit is set — reject;
3. form the probe point
   `(unit.X + boundsMinX, unit.Y + boundsMaxY, unit.Z + boundsMinZ)` from the
   definition's model bounding box;
4. if the candidate's **sonar** status bit is clear and the probe's Y is below
   `seaLevelByte << 16` — reject. This is the underwater exemption: an
   undetected submerged unit is invisible regardless of line of sight;
5. probe up to four points, returning true on the first hit:
   `p`, then `p.X += hullSpanX`, then `p.Z += hullSpanZ` with
   `p.Y -= hullSpanY`, then `p.X -= hullSpanX` — the four corners of the
   definition's footprint rectangle, with the far edge lowered. Each probe maps
   to `tileX = Xword >> 5`, `tileZ = (Zword − (Yword >> 1)) >> 5` (arithmetic
   shifts of the signed high words, and the same half-height projection the
   renderer uses), is bounds-checked **unsigned** against the observer's grid
   dimensions, and then tests either the observer's per-player byte grid or the
   global word mask according to the global mapping-mode bit `[03 §3.2]`.

In word-mask mode every probe tests the **local player's** bit, not the
observer's, so in that mode a non-local side's primary list is also built from
the local player's vision. In byte-grid mode the observer's own grid is used
and the predicate is properly per-side.

**Established fact:** Acquisition-time physical admission is a separate gate,
run per candidate at acquisition, and is exactly:

```
non-water weapon:
    reject if (int16)shooter.Yword + shooterDefinition.referenceHeight <= seaLevelByte
    reject if (int16)cand.Yword    + candDefinition.referenceHeight    <= seaLevelByte
    reject if toairweapon and (cand.status & 3) != 2         ; the flying movement class
    reject if ballistic and the §3.3 solver returns its 0x8000 sentinel, called with
           the deltas (shooter − candidate) on all three axes, the weapon velocity
           and minbarrelangle
    accept iff squaredPlanarDistance(shooter, cand) <= range × range   ; INCLUSIVE
water weapon:
    reject if the candidate lacks `floater` and (int16)cand.Yword > seaLevelByte
    reject if the candidate has `canhover` and
           (int16)cand.Yword + (candDefinition.referenceHeight >> 1) > seaLevelByte
    accept iff squaredPlanarDistance(shooter, cand) <= range × range   ; INCLUSIVE
```

The water branch tests neither shooter height, nor to-air status, nor
ballistic feasibility; the non-water branch tests no candidate medium beyond
the sea-level floor. Both height tests are whole-world-unit tests on the high
word plus the definition's reference height word. Some definition and
controller branches can bypass this gate entirely (§3.2). The branch is
selected by the weapon's `waterweapon` flag, bit 16 of the weapon flag word;
the shot-time gate of §3.3 selects on the same bit (`[R-WPN-05 §7]`).

**Established fact:** Retained-target checks do not rerun visibility, sensor,
range, medium, aircraft, or ballistic acquisition tests. Shot-time admission
also does not test category, alliance, radar, sonar, cloak, or jammer. Sensor
state controls list entry; it is not a universal per-shot revelation or
revalidation rule.

#### The radar elevation bonus never widens the search [R-WPN-03 §5]

**Established fact** (from the sensor phase, `[03 §3.4]` / `[R-VIS-01 §4]`).
The radar circle above — radius `radardistance + 2 × (unit height in whole
world units)` — behaves in three ways that matter to an implementer:

1. **The bonus enters only the squared test radius, not the search.** The
   radar pass visits the unit-grid cells within `max(radardistance,
   sonardistance)` of the emitter — the two **authored** integers, unbonused —
   and tests each visited unit's planar squared distance **strictly below**
   `(radardistance + 2 × height)²`. A unit outside the visited rectangle is
   never examined, so the bonus can enlarge detection only up to the sonar
   radius. For the ordinary radar unit with `sonardistance = 0` the elevation
   bonus is **inert**: the radar circle is exactly `radardistance`.
2. **"Height" is the emitter's world Y whole-unit word** — its elevation on the
   map, the high 16 bits of its 16.16 world Y — not the model's authored height
   nor the target's elevation. A radar on a hill reaches further only when its
   sonar radius leaves room for it.
3. **Sonar jamming clears the sonar bit** the same way radar jamming clears the
   seen bit (item 2 above): the sonar-jam pass rewrites the target's status to
   drop the sonar bit and set the jammed bit, with no owner, stealth or extra
   distance test.

The sonar test in the same callback is `unit.Y <= seaLevel` (16.16 compare
against the sea-level byte shifted up) and a strict `d² < sonardistance²`;
both circle tests are strict even though the visitor's own cell-rectangle
admission is inclusive. The complete pass order, the stealth rejection, and
the radar pass's own above-water predicate are doc 03's contract; this
document only carries the consequences for the secondary candidate list.

### 3.2 Manual and autonomous targets

**Established fact:** Manual targets and command-fire orders can supply either
a unit target or a world position. The low-level unit-target and point-target
setters do not validate alliance, category, sensor state, range, medium, air
status, or ballistic feasibility. They also preserve the previous Aim-request
latch and asynchronous Aim result. Validation therefore belongs to the order
handler, later slot processing, or both; it is not inherent in target storage.

**Established fact:** The recovered order handlers use several distinct
admission policies:

- one unit-target path disables autonomous targeting and installs the order's
  target with no local physical, category, hostility, or sensor test;
- another requires a live unit at installation and applies the physical
  acquisition gate only in a later order state;
- a selected-slot path applies the physical acquisition gate first, then
  disables autonomous targeting and installs the unit target, without locally
  testing hostility, category, or sensor state;
- fixed-position attack paths install coordinates without a category, sensor,
  or target-physical gate; and
- contextual replacement paths can retain an existing target only when it is
  physically eligible and preferred, yet install their replacement candidate
  without locally applying those same tests. The Guard handler is the worked
  example: for each slot whose armed/has-target flag and tracking flag are both
  set and whose weapon is not command-fire, it installs the guarded unit's attacker whenever the
  slot has no target, or its target fails the §3.1 physical gate, or its
  target's definition index is in the slot's bad-target mask — and it installs
  the attacker without applying either test to the attacker.

All paths still reach the common shot-time physical gate and the selected
projectile family's readiness gate. Forced/manual installation can therefore
bypass autonomous visibility lists and bad-category preference, but it does
not bypass every firing check.

**Established fact:** The autonomous scan is one pass per player per tick, run
from that player's manager object immediately after its AI task dispatch and
before the strategic registry refresh, LOS sweep and settlement deadline.
The cursor advances only when this manager is called; another player's visit
does not move it. Within a wrap, the end of the slice precedes its beginning. It
visits

```
(uint16)perPlayerUnitLimit / 30 + 1
```

records per call — advancing a persistent cursor through the owning player's
fixed record slice and wrapping to its beginning at the end. The visited record
must have a nonzero definition index, a remaining-build-fraction of exactly
zero, one high status bit set, and its two-bit stance field equal to the
fire-at-will value.

##### The third clause is the armed bit — Established

The third clause's "one high status bit set" is the **armed** bit: bit 31 of
the unit's 32-bit runtime status word, the second of the two high bits named
under `[08 "Classifier eligibility, destinations, and order"]`, set once by the
common allocator initializer from the definition's derived boolean flag and set
unless all three of the definition's resolved weapon slots are empty. The sense
is **set**: a clear bit ends the unit's visit immediately, before the three
weapon slots are looked at.

The scan reads it off the same status-word load that then supplies the stance
field: the word is fetched once, tested against the armed bit, and only then
masked down to the two-bit standing-fire field and compared with the
fire-at-will value. The clause order is therefore definition index, then
remaining-build-fraction, then armed bit, then stance.

An implementation that omits the clause scans a superset that includes every
weaponless unit; one that reads the *first* high bit (building class,
`bmcode == 0`) instead restricts autonomous acquisition to buildings and
silences every mobile unit.

Consequence in practice: the narrowing is a formalization, not a behavior
change, for units created in the running session, because a definition with no
resolved weapon slot also populates no weapon slot, and the per-slot loop the
clause guards would skip all three anyway. It is load-bearing only where the
status word and the slot control bytes can disagree — a save restore, where the
persisted slot control byte is authoritative on its own — and as documentation
of why the clause exists: it is retail's early-out over the weaponless majority
of a player's array.

##### The budget word is the per-player unit limit, and the per-player slice is fixed for the session — Established

The sixteen-bit word the scan divides is the session's **per-player unit
limit** — a setup constant, not a counter — and the vector the cursor walks is
the player's whole fixed record slice, free records included.

The same word sizes the unit-record array at session entry: the array is
allocated as `perPlayerLimit x 10 + 1` records of 280 bytes and zeroed, and
each player's slice is `perPlayerLimit` consecutive records, its first and last
record addresses stored on the player object once and never written again. That
is the same arithmetic `[01 §6.1]` and `[I5]` already record for the pool's
capacity. The word itself is written only at startup, from a registry/INI
integer whose default is 200, and at session entry, from the setup value; no
per-tick writer exists.

Three consequences follow:

- the budget is **constant for the session** — with the stock default,
  `200 / 30 + 1 = 7` records per player per tick — and does not move as units
  are built or die;
- the revisit period is a fixed `ceil(perPlayerLimit / budget)` ticks, close to
  30 by construction;
- the cursor steps over **free records too**, and the clause "nonzero
  definition index" is exactly the free-record test: a never-allocated record
  is zeroed, so its definition-index word is zero. A player owning few units
  therefore spends most of its budget on empty records, and its units are
  revisited on the same fixed period as a player owning many.

**Established fact:** Within a visited unit the three slots are processed in
numeric order, and a slot is skipped unless its enabled flag and its
autonomy flag are both set (the two persisted slot flags of
`[R-SAVE-WEAPON-01]`), its weapon is not `dropped`, and either the owning
player's
controller type is 2 (computer) or the weapon is **not** `commandfire`. The
consequence is a contract, not a nicety: a human player's units never acquire
autonomously with a command-fire weapon and a computer player's do.

**Established fact:** The scan first tries to **retain**. The current slot
target is dropped when its owning player is allied to the scanning player, when
its definition index is in the slot's bad-target mask, or when the slot's
weapon is a paralyzer and the target already carries the stunned bit. A
surviving target ends the slot's work with no re-acquisition and no draws.

**Established fact:** When retention fails, the slot re-acquires by weapon
class: an `interceptor` weapon runs the projectile scan of §11.2 and installs a
**point** target from the winning projectile's current position, and an
ordinary weapon runs the acquisition below — but only while the unit's stance
field still reads fire-at-will — and installs a **unit** target. Either way a
null result clears the slot target through the ordinary TargetCleared path.

**Established fact:** Ordinary acquisition builds its candidate array through
the §3.1 filter, with a radius that depends on the caller: the autonomous scan
passes the slot weapon's authored `range`, while the sight-distance caller
passes the unit definition's sight distance and additionally applies the
`nochasecategory` mask. It then samples that array with swap-remove random
selection: the engine repeatedly draws from the simulation stream with a bound
equal to the remaining candidate count, removes the picked entry by swapping
the last element into its place, and repeats until the array is exhausted or
**50** picks have been made. A draw with bound one returns zero without
advancing the stream, so a set of N at most 50 candidates consumes N draws of
bounds N down to 1, of which N minus one advance the stream; a larger set
consumes 50 draws of bounds N down to N minus 49. The sampled order is always
RNG-driven; the filtered order is never preserved.

**Established fact:** Each picked candidate must then pass, in this order:

1. alive bit set and death latch clear;
2. one definition flag of the candidate, **or** the shooter's owning player is
   a computer controller, **or** one global option bit — any of the three
   admits the candidate. The definition flag is **`shootme`**, default 0
   ([04 R-SPEC-01 §5]).
   **Unknown:** the authored key or writer behind the option bit; *decider:*
   the options loader;
3. one definition flag of the **shooter** bypasses the §3.1 physical gate
   entirely; otherwise that gate must accept. The bypass flag is
   **`kamikaze`** ([04 R-SPEC-01 §1]);
4. for the sight-distance caller only, the candidate's definition index must be
   clear of the `nochasecategory` mask;
5. a paralyzer weapon rejects a candidate already carrying the stunned bit.

**Established fact:** Every surviving candidate then receives one score. The
bound for the draw is the sum of the high 32-bit halves of the squared
fixed-point X and Z deltas from shooter to candidate (§3.1's shared metric). If
the bound is below two, the RNG helper returns zero without advancing the
stream. Otherwise the engine draws from the simulation stream with that bound.
Strictly lower score wins, so an equal score preserves the first sampled
candidate in that bucket; both buckets start from the maximum signed 32-bit
value. Candidates are split into preferred (definition index not in the slot's
bad-target mask) and fallback (in the mask); the preferred winner is returned
whenever one exists, otherwise the fallback winner, otherwise nothing. This is
not nearest-target selection; candidate-list order, sampling, and RNG draw
order are authoritative.

**Established fact:** Shot-time admission, which is checked immediately before
the spawner, never tests category, alliance, radar, sonar, cloak, or jammer.
Those gates belong only to candidate-list building, not to the final firing
check, and this absence is established by a bounded search over the
shot-time admission code.

**Established fact:** TargetCleared is emitted for stale/dead resolution,
scanner failure, automatic-targeting enable/disable transitions, STOP, and the
identified order-cancellation path. Replacing a target does not clear the
Aim-request latch, so a replacement can skip a fresh Aim request.
The scanner's clear changes only the encoded target pair and posts the deferred
callback when that pair was nonempty. It does not clear the Aim/control words
or run a VM drain. Failed retention proceeds directly to acquisition in the
same slot visit; a successful replacement avoids the clear callback.

**Established fact:** VTOL tracking/reacquisition uses a randomized delay from 30 through 329 ticks in the documented branch. Ground/non-VTOL fast paths do not use the same delay.

### 3.3 Range, aim, and ballistic solving

Units used throughout: world positions and velocities are 16.16 fixed point
(one world unit = 65,536); one map cell is 16 world units; angles are `uint16`
with 65,536 per circle; times are 30 Hz ticks. `range`, `coverage`,
`areaofeffect`, `accuracy`, `tolerance`, `pitchtolerance`, `burst` and
`sprayangle` are authored as plain integers; `weaponvelocity`, `startvelocity`
and `weaponacceleration` reach the record already scaled to 16.16 per tick and
per tick squared; the timing keys reach it already truncated to whole ticks;
`turnrate` reaches it as angle units per tick; `minbarrelangle` reaches it as
single-precision radians with a default of −11.25 degrees. All of those
conversions are owned by `[02 "Weapon record"]`.

#### Shared trigonometry — Established

Three helpers carry every angle-to-vector conversion in the weapon and
projectile code, and an implementation must reproduce them exactly because
their rounding is visible in world positions.

* **Scaled sine** `sin(angle, magnitude)`: index `((int16)angle + 32) >> 6`
  masked to the even byte offsets `0..1022`, so the table is **512 signed
  16-bit entries per circle** — one entry per 128 angle units
  `[R-WPN-01 §4]`. Entry *k* holds `round(8192 × sin(2πk/512))`.
  The product is 64-bit: `(entry × magnitude + 0x1000) >> 13`, an arithmetic
  shift, i.e. round-to-nearest at 1/8192 resolution.
* **Scaled cosine** `cos(angle, magnitude)`: the same helper with a quarter
  turn added before the index arithmetic (`angle + 0x4020` in place of
  `angle + 32`).
* **Angle from a 2-vector** `atan2q(a, b)`: both operands converted to the x87
  stack as signed 32-bit integers, `atan2(a, b)` taken in extended precision,
  multiplied by 32,768/π, and stored with the **round-to-nearest** integer
  store (not truncation), then kept as the low 16 bits.

Distances use the C runtime `hypot`, and every float-to-integer step named
below is the shared truncate-toward-zero conversion of `[01 §8]`.

##### The fixed trigonometry table is 512 entries, quantizing to 128 angle units — Established [R-WPN-01 §4]

The index arithmetic masks to the even byte offsets up to 1,022, so 512 signed
16-bit entries cover a full circle and one entry spans 128 angle units
(0.703125 degrees). The product form is `(entry × magnitude + 4096) >> 13`.

**Established fact:** Ordinary fire range is a two-dimensional test on X and Z
only. With `s` the shooter's world point and `t` the resolved target point,
both raw 16.16:

```
dx = t.X - s.X                    (signed 32-bit, wrapping)
dz = t.Z - s.Z
a  = (int32)(((int64)dx * dx) >> 32)      ; squared whole-world-unit distance
b  = (int32)(((int64)dz * dz) >> 32)
admit when  a + b <= range * range        ; signed 32-bit compare
```

`range * range` is a 32-bit signed multiply of the authored integer, so an
authored range above 46,340 wraps negative and admits nothing. The Y axis is
not in this test. With `range` zero the test admits only `|dx| < 65,536` and
`|dz| < 65,536` — a target within one world unit on each axis, not a whole
cell. `coverage` is a separate scalar used only by interception and by the
range-circle overlay; it is not this radius `[02 "Weapon record"]`.

**Established fact:** After the range test the shot-time gate applies two more
predicates and nothing else. For a **water weapon** it stops there and admits.
For a **non-water weapon** it requires

```
(int16)(s.Y >> 16) + definitionTopHeight  >  seaLevelByte
```

— the shooter's world Y whole-unit word plus the unit definition's model
top-height word, strictly greater than the map's sea-level byte — and, when
`ballistic` is authored, a ballistic solution that is not the no-solution
sentinel. The gate performs no terrain, hill, visibility or sensor test. It is
evaluated in the unit phase, before the slot executor runs, and its failure
sets the shooter's "could not fire" status bit rather than clearing any latch.

**Established fact:** The slot pipeline walks the three weapon slots in
ascending order once per unit per tick, inside the unit phase, i.e. before the
projectile phase `[01 §4.4]`. For each slot whose armed bit is set it performs,
in this order: decrement the reload timer when it is nonzero; resolve the
target point (§3.2); abandon the slot when no executor pointer was installed;
run the family-specific Aim dispatch below; and then, **only when the reload
timer is now zero**, run the admission gate, the cost precheck, and the
executor. A weapon authored with `reloadtime` of one tick therefore fires on
the tick after the decrement, never on the same tick.

**Established fact:** Which executor a weapon uses is decided once, at catalog
compile time, by the first matching flag in this order: `turret`; else
`vlaunch`; else `lineofsight` or `selfprop`; else `dropped`; else **none**, and
a weapon with no matching flag can never fire. This ordering is not the same as
the family ordering of §6.2 and both must be reproduced independently.

**Established fact:** Aim dispatch is family-specific.

* The **turret** executor's slot dispatches `AimPrimary`, `AimSecondary` or
  `AimTertiary` only when the Aim-request latch is clear. It first solves the
  angles: for a `ballistic` weapon, the yaw is `atan2q` of the AimFrom piece
  minus the target point on X and Z, **minus the unit heading** (a relative
  angle), and the pitch is the ballistic solver's result, rejected when it
  returns the sentinel; for a `lineofsight` weapon, the direct solver below;
  for neither, the dispatch is skipped entirely. On success it writes the
  solved yaw and pitch into the slot, zeroes the four-byte Aim receiver,
  dispatches the deferred Aim callback with those two angles as arguments, and
  then sets the latch.
* The **vertical-launch** executor's slot dispatches the same callback with
  both arguments **zero**, gated only on the latch being clear and — for a
  `stockpile` weapon — on nonzero ammunition. It solves no angles at aim time.
* The **line-of-sight/self-propelled** and **dropped** executors dispatch no
  Aim callback at all and never set or test the latch.

**Established fact:** The latch is an Aim-request latch, not a "physically
aimed" or fire-ready result. It is set immediately after the callback is
dispatched. Completion arrives through the receiver embedded on that deferred
callback: an explicit script return delivers its value, and the dispatcher
delivers zero when the script name is absent, the script identity is invalid,
or all eight COB thread slots are occupied; signal termination and abnormal
termination do not call the receiver. A zero delivery — explicit return or
dispatcher — leaves the latch without permission and does not clear it; a
nonzero delivery grants permission; and no timeout is present, the absence of a
timeout writer being established by a bounded search over the weapon-slot code.
A nil VM or missing script must therefore not set an Aim-ready state for a
family that requires a result: a missing or blocked Aim script delivers zero
through the same receiver.

**Established fact:** Executor readiness rules differ, and the differences are
exactly these:

* the **turret** executor requires both the latch and a nonzero Aim result; it
  then re-solves yaw and pitch from the current geometry, applies the
  angular-drift gate, queries the muzzle, converts the slot yaw from relative
  to absolute by adding the unit heading, applies the accuracy spread of §4.4,
  and dispatches to the ordinary creator when `lineofsight` or `selfprop` is
  authored, otherwise to the ballistic creator when `ballistic` is authored,
  otherwise fires nothing;
* the **vertical-launch** executor requires the Aim result and does **not**
  test the latch; it queries the muzzle, writes absolute yaw and pitch into the
  slot, runs the fire-time interceptor rescan when `interceptor` is authored
  (returning failure when the rescan finds nothing), and dispatches to the
  vertical-launch creator;
* the **line-of-sight/self-propelled** executor gates on neither field; it
  queries the muzzle, writes absolute yaw and pitch, and applies the
  angular-drift gate **against the unit's own heading and pitch** rather than
  against a separately desired direction;
* the **dropped** executor has no Aim gate and no drift gate; it queries the
  muzzle and allocates inline.

Turret geometry that yields no solution, and a failed drift gate, both clear
the latch and return failure; the no-solution case additionally sets the
shooter's "could not fire" status bit. Turret and vertical-launch allocation
failure preserve the ready state, while a successful vertical-launch allocation
clears both the result and the latch and a successful turret allocation clears
both as well. The ballistic no-solution sentinel (the angle-domain value
`0x8000`) suppresses Aim dispatch entirely. A missing Aim script, a zero
completion delivery, or an exhausted projectile pool therefore means the turret
and vertical-launch families cannot fire; the line-of-sight/self-propelled
family's query fallback may still supply a muzzle piece, but no Aim result is
invented on its behalf `[R-P0-07]`.

**Established fact:** The direct (non-ballistic) aim solver takes the AimFrom
piece world point `p` and the target point `t` and computes

```
yaw   = atan2q(p.X - t.X, p.Z - t.Z) - unitHeading        ; relative, int16
dist  = trunc(hypot((double)(p.X - t.X), (double)(p.Z - t.Z)))   ; 16.16
pitch = atan2q(-(int16)((p.Y - t.Y) >> 16), (int16)(dist >> 16)) ; absolute
```

and always reports success. Note that the pitch is solved at **whole
world-unit** resolution: both operands are the high words of 16.16 quantities,
so a target closer than one world unit horizontally quantizes to a pitch of
±90 degrees or zero. The same two expressions appear verbatim in the
line-of-sight executor (writing absolute yaw, without the heading subtraction),
in the ordinary creator, and in projectile guidance.

**Implementation note.** Both operands of the pitch
expression are written in **muzzle-minus-target** order and the vertical term is
then negated, so the value handed to the conversion is `target.Y - muzzle.Y`: a
target above the muzzle aims up, one below aims down. An implementation whose
own delta convention is target-minus-muzzle must drop the negation rather than
keep it. Keeping both inverts every direct-fire pitch, and because a muzzle
piece sits above a ground unit's origin the ordinary case is a target slightly
*below* the muzzle — so an inverted solver makes every shot climb away from its
target and never impact. The operand order also decides the truncation: the
shift is arithmetic, so `-(int16)(dy >> 16)` and `(int16)((-dy) >> 16)` differ
by one whole world unit for every negative delta.

**Established fact:** The angular-drift gate compares the slot's stored angles
against a wanted pair:

```
tolerance != 0:  yawGate = tolerance
                 pitchGate = pitchtolerance != 0 ? pitchtolerance : tolerance
tolerance == 0:  yawGate = pitchGate = (unit is stationary ? 150 : 2000)
pass when |(int16)(slotYaw - wantYaw)| <= yawGate
      and |(int16)(slotPitch - wantPitch)| <= pitchGate
```

Both comparisons are **inclusive** and both errors are the absolute value of a
signed 16-bit difference, so an error of exactly the gate passes. "Stationary"
is the unit's **movement tier being category 0** — the two-bit tier the movement
integrator caches in bits 2–3 of the movement-mode word, whose category 0 covers
a zero scalar speed *and* a mover-inhibited unit *and* a unit attached to a
carrier, and whose transitions raise `StopMoving`/`StartMoving` and
`MoveRate1..3` `[04 §5.2]`. A transported unit therefore aims under the tight
gate. There is no class or category-mask test here: the field is the movement
tier, so the tight gate (150) applies to a stationary (or carried, or
inhibited) unit of any kind, air or ground, and the loose gate (2,000) to any
unit whose tier is 1, 2 or 3 `[R-WPN-01 §1]`. The field identity is
corroborated from the integrator side, which establishes the same two bits and
the same `MoveRate1`/`MoveRate2` thresholds `[04 §5.2]`.

**Established fact:** A pre-fire lead is applied to the resolved target point,
and only there — projectile guidance never leads (§6.7). The lead runs when all
of: the slot's armed bit is set; the weapon is **not** `cruise`; the target unit
has a movement record; the shooter's credited-kill count is **strictly greater
than five**; and `weaponvelocity` is nonzero. Then

```
D  = trunc(sqrt((dX*dX + dY*dY) + dZ*dZ))    ; dX = shooter.X - point.X, etc.,
                                             ; raw 16.16 deltas, x87 extended
T  = ((int64)D << 16) / weaponvelocity       ; signed 64-bit divide, ticks in 16.16
T2 = ((int64)T * 0xcccc) >> 16               ; scale by 52,428/65,536 = 0.79998779…
point.X += (int32)(((int64)mover.velocityX * T2) >> 16)
point.Y += ...velocityY...   point.Z += ...velocityZ...
```

The 0.8 factor and the six-kill threshold are read from the image, not chosen.
The distance here is three-dimensional, unlike the range test.

**Established fact:** The ballistic solver takes the three signed 32-bit deltas
`(dx, dy, dz)` from the aim source to the target, the 16.16 `weaponvelocity`
`V`, and the single-precision `minbarrelangle` `m` in radians, and reads the
gravity global `g`. Every step below is IEEE double except where noted:

```
D    = hypot((double)dx, (double)dz)             ; the 80-bit register value
D2   = D80 * D64                                 ; the register value times its own
                                                 ; double-precision store
S    = (double)dy * (double)dy
B    = (V*V + g*dy) * D2
disc = (S*(g*g) + (V*V + 2*g*dy) * (V*V)) * D2*D2  -  D2*D2*(g*g)*(S + D2)
if (disc < 0 or unordered)  return 0x8000
r1 = (sqrt(disc) + B) / (2*(S + D2))
r2 = (B - sqrt(disc)) / (2*(S + D2))
a1 = (r1 <= 0) ? pi/2 : acos(sqrt(r1) / V)
a2 = (r2 <= 0) ? pi/2 : acos(sqrt(r2) / V)
accept a1 when  m < a1  and  a1 <= pi/4
else accept a2 when  m < a2  and  a2 <= pi/4
else return 0x8000
return trunc(accepted * 32768.0 * (1/pi))        ; two multiplies, in that order
```

`g*g` is formed as a 32-bit signed integer multiply before its conversion to
double, so a large gravity wraps there. `dx`, `dy` and `dz` wrap as signed
32-bit before conversion. The discriminant is compared against exactly zero
with no epsilon guard, and the unordered result of a NaN compares as "less
than", so a NaN discriminant also returns the sentinel. The lower gate is
**strict** and the upper gate at π/4 is **inclusive**; the plus root is always
tested first. `r1` or `r2` at or below zero substitutes π/2, which then fails
the π/4 gate — this is how an unreachable target is rejected rather than by an
arithmetic error. **Unknown:** whether `sqrt(r) / V` can exceed one on
malformed input and, if so, what the runtime's `acos` returns and how the
unordered compares then behave; the surviving evidence shows the gates would
treat an unordered result as acceptable and serialize `trunc(NaN)`. *Decider:*
static trace of the runtime `acos` domain path plus a reachability argument over
the root expression.

**Established fact:** When `weaponvelocity` is zero and the ballistic creator is
reached, the pool record has already been reserved and the live count already
incremented before the unsigned distance-over-velocity division raises the
processor divide exception, and the count is not rolled back (§6.4).

**Cross-reference.** The three visibility-like layers, the minimap radar
surfaces, the jammer circles and the five-pass sensor phase are `[03 §3.2]`'s
and `[03 §3.4]`'s contract; §3.1 above carries the acquisition-facing half —
the identity, gate and writers of the primary and secondary candidate lists.

#### The accuracy family's readers: full census [R-WPN-03 §1]

**Established fact.** `accuracy`, `tolerance` and `pitchtolerance` are not dead
stores: each has exactly one gameplay reader, and the readers sit in the slot
executors rather than in the creators. A whole-image census — every access at
each of the three record offsets in every recovered function, each non-weapon
hit classified — gives exactly one reader per key:

| Authored key | Parser | Stored as | Reader (one each) | Where in the tick |
|---|---|---|---|---|
| `accuracy` | integer getter, default 0 | signed 16-bit, plain integer | the **turret** executor's spread (§4.4, `[R-WPN-03 §4]`) | unit phase, fire time, after the muzzle query, before the creator |
| `tolerance` | integer getter, default 0 | 16-bit, **read zero-extended** | the angular-drift gate (`[R-WPN-03 §2]`) | unit phase, fire time, before the muzzle query |
| `pitchtolerance` | integer getter, default 0 | 16-bit, **read zero-extended** | the angular-drift gate, and only when `tolerance` is nonzero | as above |
| `sprayangle` | integer getter, default 0 | signed 16-bit | the burst scheduler's spray draw (§4.3) | projectile phase, per successful clone |
| `aimrate` | **no key** — the spelling occurs nowhere in the image | — | none can exist (`[R-WPN-01 §9]`) | — |
| `movingaccuracy` | **no key** | — | none can exist (`[R-WPN-01 §9]`) | — |

Every other access at those offsets is a different object: the unit record's
current-health word shares the third offset (it is the operand the reload and
spread arithmetic read *from the unit*), the first offset is a floating-point
field of the unit and of several window objects, and the second is a network
packet field. None of them is reached through a weapon-definition pointer.
No reader of any of the three keys exists in target admission, target
retention, the acquisition scan, projectile launch, projectile motion,
collision or damage; §4.4's spread and §3.3's drift gate are the whole
gameplay surface of the family. The `sprayangle` draw is listed because it is
the only other authored bound handed to the same simulation generator by
weapon code; it shares the generator, not the site.

**Established fact (a second reader of `range`).** The first slot's `range`
has one reader outside the weapon code: the air movement lane's follow-unit
path marker uses it (in 16.16, with a fallback of 100 world units when it is
zero) as the radial offset about a flying target, and the same value as the
loiter radius around a landing target `[R-AIR-01 §4]`. It does not alter the
§3.3 range test; it is listed so that a census of `range` readers is complete.

#### The drift gate at implementable precision [R-WPN-03 §2]

**Established fact.** The widths, edges and callers of the gate block above.

* **Widths.** `tolerance` and `pitchtolerance` are loaded as **unsigned**
  16-bit values and compared as 32-bit signed integers against the absolute
  value of a signed 16-bit difference. The difference is the 16-bit wrap of
  `stored − wanted`, sign-extended, then its absolute value — so the largest
  possible error is 32,768 (the wrap of `0x8000`), and the test is
  `|(int16)(stored − wanted)| <= gate`, inclusive, yaw first, pitch second,
  short-circuiting: the pitch difference is not computed when yaw fails.
* **Negative authored tolerance.** A negatively authored `tolerance` (or
  `pitchtolerance`) is stored as its 16-bit two's complement and read back
  unsigned, so `-1` becomes a gate of 65,535 which every error passes. Stock
  content never authors a negative value.
* **`pitchtolerance` is inert when `tolerance` is zero.** The zero test is on
  `tolerance` alone; when it is zero both gates take the movement-tier
  fallback (150 stationary, 2,000 moving) and an authored `pitchtolerance` is
  never consulted. When `tolerance` is nonzero and `pitchtolerance` is zero the
  pitch gate is `tolerance`. This is the only place the two keys interact.
* **Two callers, two meanings.** The **turret** executor calls the gate with
  the slot's stored angles (written when the Aim callback was dispatched: the
  yaw **relative** to the unit heading, the pitch absolute) against the pair
  it has just re-solved from current geometry in the same form — it therefore
  measures how far the target has moved, in angle, since the Aim request went
  out. The **line-of-sight/self-propelled** executor first overwrites the
  slot's stored angles with the absolute direction from muzzle to target and
  then calls the gate with the unit's own heading and pitch as the wanted pair
  — it therefore measures how far off the unit's facing the target sits, and a
  fixed-forward weapon with `tolerance = 0` fires only when the target is
  within 150 angle units (about 0.8°) of straight ahead while stationary or
  2,000 (about 11°) while moving. The vertical-launch and dropped executors do
  not call the gate.
* **On failure** the turret executor clears the Aim-issued latch and returns
  failure without touching the reload timer, the aim-ready word, or the RNG,
  so its next slot visit re-solves and re-dispatches Aim; the line-of-sight/
  self-propelled executor simply returns failure (it owns no latch), leaving
  the absolute angles it just wrote in the slot.

#### There is no moving-accuracy or aim-rate mechanism [R-WPN-03 §3]

**Established fact.** Retail has no `movingaccuracy` and no `aimrate` field
(`[R-WPN-01 §9]`), so the question "whose motion, and by what predicate" has a
definite answer that is not a field at all. The only aiming term that depends
on motion is the drift gate's zero-tolerance fallback, and it reads the
**firer's** movement tier — category 0 of the two-bit tier the movement
integrator caches from the unit's own scalar speed against its `MoveRate1`/
`MoveRate2` thresholds `[04 §5.2]` — not the target's motion and not the
weapon's. The **target's** motion enters the firing solution in exactly one
place, the pre-fire lead of §3.3 (gated on the shooter's credited kills being
strictly greater than five and on a nonzero `weaponvelocity`), and never in the
spread or the gate. The spread bound of §4.4 varies with the shooter's health
fraction and kill count only. An implementation that scales accuracy by the
firer's or the target's speed, or that slews a turret at an "aim rate", invents
behavior; the turret's angular motion is entirely the COB script's, and the
engine's only angular slew is the projectile guidance of §6.7.

#### The weapon-slot engagement distance, and the order-side shot-admission gate [R-WPN-05 §1]

Two helpers the order handlers of `[04 R-ORD-01 §3]` call by name and defer to
this document. Both are **Established (direct-static)**.

**The engagement distance is the slot's authored `range`.** The weapon-slot
engagement-distance helper takes a unit and a slot index, walks to that slot's
resolved weapon record, and returns its `range` field — the same
whole-world-unit integer §3.3's range test squares, and the same one the air
loiter marker reads off slot 1 `[04 R-AIR-01 §4]`. There is no scaling, no
clamp and no second field: the standoff **is** the weapon's range.

This is the standoff value the attack-chase orbit substates bind
(`[04 §3.9]`), and it fixes the scale of
every radius in `Attack_Chase` phase 2 and of `Suppress`'s `p2`: a unit orbits
at exactly the distance from which its own weapon can reach, closes to half
and then to zero, and bands out to twice its range — all in world units.

**The shot-admission gate.** `canSlotEngage(shooter, target, slot)` answers
"may this slot be pointed at this target right now" and returns a plain
admit/refuse. `Attack_Chase` phases 1 and 3 and `Guard_NoMove` phase 2 branch
on it `[04 R-ORD-01 §3]`. Let `w` be the slot's weapon record and `sea` the
map's sea-level byte. In order:

1. **Water weapon** (`waterweapon`). The target must be in the water. Unless
   the target's definition carries `floater` (word A bit 19), its whole-unit Y
   word must not exceed `sea`; and when the target carries `canhover` (bit 12),
   its whole-unit Y plus **half** its model top-height word must not exceed
   `sea` either. Both compares are signed and reject on strictly greater. The
   gate then goes straight to the range test — no shooter-side test, no air
   test, no ballistic test.
2. **Non-water weapon.** Both ends must be out of the water: `(int16)shooterY
   + shooterModelTop > sea` and `(int16)targetY + targetModelTop > sea`, each
   strictly greater, each rejecting when it fails. The shooter half is §3.3's
   shot-time predicate; the target half belongs to this gate alone.
3. `toairweapon` (flag bit 17): the target's committed mover mode — the low two
   bits of its state word `[04 R-MOV-01 §8]` — must read exactly **2**,
   airborne. The operand of that airborne test (`[02 R-KEYS-01 §2]`) is the
   committed mover mode, not a definition bit and not an altitude.
4. `ballistic` (flag bit 1): the ballistic solver is run on the shooter-minus-
   target delta of the two units' **own positions** on all three axes — no
   piece is queried and no `SweetSpot` transform is applied at this gate —
   with the weapon's `weaponvelocity` and `minbarrelangle`, and the gate
   refuses when it returns the no-solution sentinel. Gravity is not among the
   passed operands; the solver reads the world's.
5. **Range.** `dx` and `dz` are the raw 16.16 planar deltas; the gate admits
   when `((dx·dx) >> 32) + ((dz·dz) >> 32) <= range·range`, an inclusive signed
   32-bit compare against the slot weapon's `range`. This is §3.3's arithmetic
   exactly, including the square-then-shift order — the deltas are **not**
   truncated to whole units before squaring.

The gate performs no terrain, hill, visibility or sensor test, and consults
neither reload nor ammunition nor cost.

#### The slot control byte: bits 0–4 named, bits 5–7 inert, and its two writers [R-WPN-05 §3]

**Established (direct-static).** §1.2 lists "an armed/has-target flag, an
Aim-request latch, a tracking flag" among the slot record's fields;
`[04 R-ORD-01 §7]` names a "slot control byte" whose bits 1 and 4 the order
verbs test and toggle; `[08 R-SAVE-WEAPON-01]` persists a slot flag byte whose
bits 0, 1 and 4 it names and whose bits 2 and 3 it leaves unnamed. Reading
every access to the byte in the decompiled set settles that these are **one
byte**, and names the rest of it:

| Bit | Meaning | Writers | Readers |
|---:|---|---|---|
| 0 | **Aim-request latch** | set by the slot pipeline when it dispatches `Aim*`; cleared by the pipeline on target loss, by the turret executor on no-solution, on drift-gate failure and on a successful shot, and by the vertical-launch executor on a successful shot (§3.3) | the pipeline's dispatch gate; the turret executor's ready gate |
| 1 | **slot enabled** — the slot's weapon definition is active (the definition-side active byte of `[08 R-SAVE-WEAPON-01]` is nonzero) | the **slot initializer**, run once from unit construction; save load restores it wholesale | the pipeline's slot visit, the target resolver, the enabled-slot tests of `[04 R-ORD-01 §7]` and the HUD/AI readers |
| 2–3 | **the slot's own index** (0, 1, 2) | the slot initializer | every muzzle query made through a slot record (the index is the query's slot argument), the three creators (to select `FirePrimary`/`FireSecondary`/`FireTertiary` and to read *that slot's* stored yaw for `RockUnit`), the line-of-sight executor, and the fire packet |
| 4 | **autonomy** (doc 06's "tracking flag", `[04 R-ORD-01 §7]`'s "inhibit latch") | the slot initializer **sets** it, so every slot starts autonomous; thereafter only the two order verbs | the autonomous scan (§3.2), the retaliation offer, the guards, the fire-stance handler (`[04 §5.4]`) |
| 5–7 | inert | the initializer preserves whatever the record held; no other writer in the decompiled set; the save writer and reader discard them | none found (bounded) |

The byte is therefore self-describing — a slot record carries its own index —
which is why the creators and the muzzle queries take a slot pointer alone.
The initializer also zeroes the slot's reload word and stockpile byte, links
the weapon definition from the unit definition's ordered `weapon1..3` list,
and stores an initial value into the slot's distance word — the word the
ballistic creator divides (§6.4). **Established — the expression.** For each
slot the initializer runs the slot's `Query*` callback
(forced, not the fallback form) and then the `AimFrom*` callback with its
`Query*` fallback, each transformed to a world-space point through the piece
transform that includes the unit's orientation at that moment, and stores

```
slotDistance = trunc( 1.25 × (queryPoint.z − aimFromPoint.z) )
```

as a 32-bit integer — a **horizontal Z-axis** difference of two 16.16 world
positions (the unit position cancels; only the rotated piece offsets
differ), scaled by the double constant 1.25 and truncated toward zero. It is
not a length, not a square, and involves neither X nor height. When the script
answers neither query the two points coincide and the stored value is zero.
The word has **no other writer** in the decompiled set (bounded over every
slot-relative access in the weapon region; the fire-time turret executor and
the ballistic solver do not store it; save load restores the record
wholesale), so this creation-time value is what the ballistic creator divides
for the whole life of the unit (§6.4).

**Established (direct-static) — the frame, the order, and the sign.** The delta
is taken at the spawn heading, not at heading zero. The three unit creators are
three routines but one sequence, and each runs the same three calls back to
back, in this order:

1. the **common position/state initializer** of `[04 §2.3b]` — it reads
   `buildangle`, draws once on the simulation stream and writes the resulting
   heading into the unit's orientation triple, zeroing the other two
   components;
2. the **script and model instantiation** — it creates the unit's COB thread
   context and its piece-model instance and dispatches `Create`;
3. the **slot initializer** — weapon links, control byte, reload word,
   stockpile byte, distance word.

So the piece model exists and the spawn heading is already in force when the
two piece queries run: **the delta is taken at the spawn heading**, never at
heading zero. Nothing rewrites the word afterwards — the initializer has
exactly three call sites, all of them step 3 of that sequence, and no
heading-change, build-completion or weapon-reinstall path re-enters it (the
scaling constant 1.25 has exactly one reference in the image, and it is this
routine).

Two consequences follow.

*The two points are world-space, rotated.* Each query returns the unit's own
position plus the piece's composed offset, and the composer walks the piece's
parent chain applying each node's rotation, **adding the unit's orientation
triple to the root node's own rotation words** before it rotates. The offsets
are therefore rotated by the spawn heading, not raw authored model offsets. The
unit position still cancels in the difference, but the rotation does not: the
same unit spawned at two headings stores two different words. It is the **same**
piece-point routine the fire path calls for the muzzle spawn — one routine, one
frame — so an implementation whose muzzle spawn point and whose distance word
disagree about how a composed piece offset becomes a world point has one of the
two wrong; they cannot be reconciled separately. The mission
placer's authored facing angle — copied over the initialized heading *after*
the creator returns `[04 §2.3b]` — arrives too late
to affect it.

*The stock corpus stores a positive word.* A tank's `Query*` piece is the flare
at the end of the barrel and its `AimFrom*` piece the turret behind it, so the
delta is the barrel offset projected onto world Z. Heading `h` faces
`(−sin h, −cos h)` (§4 fact 1), so a muzzle a distance `d` forward of the
aim-from piece gives a world-Z delta of `−d·cos h`, and the spawn band is
`0x8000 ± buildangle/2` — for the stock values (`buildangle` 0 or 4096) that is
within a quarter turn of the half turn, where `cos h < 0` and the delta is
positive. Stock units therefore never store a negative value at a spawn
heading. The negative case remains reachable arithmetic — a script that answers
the two queries the other way round, or a definition with a `buildangle` wide
enough to carry the spawn heading past a quarter turn — and §6.4's unsigned
divide still governs it.

The initializer ends by dispatching `SetMaxReloadTime` with the largest of
the three `reloadtime` values scaled to milliseconds (`ticks × 1000 / 30`,
truncated).

**The one writer of bit 1** (`[04 R-ORD-01 §7]`) is the slot initializer
above; it runs at construction, so a slot's enabled bit never changes during
play except through save load.

**Consequence for an implementation.** The order verbs and the weapon layer
read one byte, not two: *release slot k* / *inhibit slot k* clear and set the
same autonomy bit the autonomous scan requires. Modelling an order-side
control byte separately from the slot's tracking flag models one retail byte
as two, and an implementation that keeps the two in step is equivalent only
while nothing writes one without the other.

#### The aim yaw handed to the script is relative, and the drift pair is relative on both sides [R-WPN-05 §4]

**Established (direct-static).** Three facts fix the sign convention of every
yaw in this document, and they are consistent with each other:

1. **Angle to direction.** A yaw `a` denotes the planar direction
   `(−sin a, −cos a)` in world `(X, Z)`: the ordinary and ballistic creators
   build `velocityX = −sin(yaw, H)` and `velocityZ = −cos(yaw, H)` (§6.3,
   §6.4), and the mover builds its own velocity as `−sin(heading)·speed`,
   `−cos(heading)·speed` (`[04 R-MOV-01 §4]`); the dropped creator gives a
   bomb the dropping unit's motion along `(−sin heading, −cos heading)`
   (§6.4). Yaw and unit heading share **one** convention, so heading 0 faces
   −Z and a quarter turn (`0x4000`) faces −X.
2. **The absolute bearing** is `atan2q(m.X − t.X, m.Z − t.Z)` — muzzle minus
   target, exactly as §3.3 and §6.3 write it. Under fact 1 this is the bearing
   *from* the muzzle *toward* the target (the negations in the velocity build
   undo the operand order), so the two are one expression, not two
   conventions in tension.
3. **The value handed to `Aim*`** is `(bearing − unitHeading) mod 65536` as
   the first argument and the absolute pitch as the second, both passed as
   16-bit unsigned words; the same pair is stored in the slot as the desired
   yaw and pitch and is what the save persists (`[08 R-SAVE-WEAPON-01]`,
   bytes `0x12..0x15`). Zero means *dead ahead*; a positive relative yaw lies
   on the unit's left when facing −Z (toward −X), i.e. counter-clockwise
   seen from above with X east and Z south.

**The turret drift pair.** At fire time the turret executor re-solves the
**relative** yaw from the current muzzle, target and heading, and the drift
gate of `[R-WPN-03 §2]` compares it with the stored relative yaw from the
dispatch tick. With the target still, a unit that turned by Δ since the
dispatch reads a yaw error of −Δ, so a turret whose script has already
finished turning must re-aim when its hull turns further than `tolerance`.
Only after the gate passes does the executor add the *current* heading to the
stored yaw (relative → absolute), apply the spread of §4.4, and call the
creator. Both halves of the comparison are relative; an implementation that
stores the absolute bearing and compares absolute against absolute loses the
hull-turn term and differs whenever the shooter turned while its Aim was
outstanding.

**Implementation note.** An engine whose own bearing helper is written over
target-minus-muzzle deltas and whose velocity build uses positive sine and
cosine obtains an absolute yaw exactly half a turn from retail's
(`atan2(−x, −z) = atan2(x, z) + 0x8000`). Its projectiles fly correctly
because the two sign flips cancel — but the value it hands to `Aim*` must
still be retail's: `(ownYaw + 0x8000 − heading) mod 65536`. Handing the
un-shifted absolute yaw is correct only for a unit whose heading is `0x8000`
(facing +Z), and subtracting the heading from the un-shifted yaw is wrong for
every heading; the authored scripts assume a relative argument with zero
meaning straight ahead.

#### Which angle each velocity build negates, and the two further crossings of the half-turn numbering [R-WPN-05 §11]

Which angle the `−sin`/`−cos` of §4 fact 1 is taken of decides where retail's
numbering and an engine whose bearing helper runs over target-minus-muzzle
deltas (the note above) part company.

**Established (direct-static).** Every velocity build negates the scaled
sine and cosine of an **absolute** angle, and each reads exactly one:

| build | angle read | source of the angle |
|---|---|---|
| ordinary creator (§6.3) | the yaw it has just solved | `atan2q(m.X − t.X, m.Z − t.Z)` over the muzzle and aim points it was handed; stored on the record as it was solved |
| ballistic creator (§6.4) | the slot's stored yaw | the relative aim yaw of §4 fact 3 **after** the turret executor added the unit's current heading back (§4 "The turret drift pair"); copied to the record unchanged |
| ground mover (`[04 R-MOV-01 §4]`) | the unit's heading word | the mover's own heading; the position step is `−sin(heading)·speed`, `−cos(heading)·speed` |

The relative aim yaw is never negated and never enters a velocity: it is
made absolute by adding the heading, and only that absolute value meets the
sign. The self-propelled per-tick rebuild (§6.7) and the burst spray (§4.3)
negate the record's stored absolute yaw the same way. So there is one sign
convention, applied to one kind of angle, and §4 fact 1 stands as written:
yaw and heading share a numbering in which `a` names `(−sin a, −cos a)`.

**Consequence for the half-turn implementation (Established, from the
above).** An engine that solves `atan2(t − m)` and builds `+sin`/`+cos`
carries every *projectile* yaw at `retail + 0x8000` and every *unit* heading
at retail's own value (its mover negates as retail does). Its projectiles fly
correctly and its hulls face their motion; the numbering differs only where a
projectile angle is compared with, added to, or handed to something outside
the projectile arithmetic. §4's note named the `Aim*` argument; the same
shift is owed by `RockUnit`'s recoil direction (§4 fact 3, `slotYaw −
heading`), by the fixed-forward drift compare against the heading
(`[R-WPN-03 §2]`), and by two further sites that carry retail's numbering
unshifted:

1. **The damage packet's direction byte (§9.1).** Byte 7 is the high byte of
   `atan2q(record.X − victim.X, record.Z − victim.Z) − victim.heading`,
   solved afresh at delivery from the record's **current** point (the packet
   builder receives the 16-bit word as its fifth argument and keeps its high
   byte; the caller that delivers projectile damage forms it, and the bearing
   helper's result is used for nothing else). It is *not* the projectile's
   stored yaw: a splash recipient off the line of flight reads the direction
   from the burst point toward itself, and a direct hit reads the bearing
   from the record's point at impact. The number is retail's — no
   engine-side yaw is involved, so an implementation must compute it from
   positions and its heading word and never substitute its stored yaw. With
   the victim facing −Z, a record dead ahead (at −Z) yields `0x80`, one
   behind `0x00`, one at +X `0x40`; the same three values hold at every
   heading for the same relative placement.
2. **The projectile angle block the renderer folds** (`[03 §5.2]`, rendertype
   1 and the model cases). The block handed to the shared vertex rotator is
   `{roll, yaw − 0x8000, pitch − 0x8000}` taken from the record's own
   stored words — retail's yaw — while the unit composition folds the hull's
   bank, heading and pitch with no offset through the same rotator. An
   implementation that stores the half-turn yaw and applies the block's
   `− 0x8000` to it draws every 3DO projectile rotated half a turn about the
   vertical from retail's, i.e. facing away from its motion; the shift
   belongs at the point where the record's yaw is published to
   presentation.

##### Every non-projectile caller passes a zero direction word, and only kind 1 ever reads it — Established

The packet builder takes **five** arguments — attacker, victim, amount, kind,
direction word — and keeps the direction word's **high byte** as packet byte 7,
exactly as the projectile caller's entry above describes. Of the builder's
eleven call sites, exactly **one** — the projectile damage caller — computes a
direction word; it is the one that solves the victim-relative bearing afresh.
Every other site pushes the **immediate constant zero**, across every kind that
reaches the builder: both fixed-30000 kind-3 sites (the self-destruct pair, whose
attacker and victim arguments are the same unit), the two kind-4 sites, the two
kind-5 sites, the kind-9 sites, the kind-10 site, the kind-11 site, and a further
30000-damage site that selects between two kinds at runtime. Every
non-projectile caller passes a direction word, and it is zero.

The direction byte is read at exactly one place in the damage funnel: inside
the `kind == 1` branch that emits `HitByWeapon` and `TakeDamage` (§9.1 step 7).
The funnel body is a single equality test against 1 guarding both starts, with
no second arm for any other kind. A **self-destruct therefore emits neither
`HitByWeapon` nor `TakeDamage`**: the packet is kind 3, health is subtracted,
the death latch is set or health is clamped, and the funnel returns.

#### The accuracy spread reaches only ballistic trajectories; the ordinary creator re-solves from the aim point [R-WPN-05 §5]

**Established (direct-static).** The turret executor hands both creators the
**same** muzzle point and the **same** target point it received from the slot
pipeline — the resolved, lead-adjusted point of §3.4 — and never derives an
aim point from the slot's stored angles. What each creator does with the slot
angles after the spread of §4.4 has been added to them:

* the **ordinary creator** (`lineofsight` or `selfprop`) recomputes yaw and
  pitch from the muzzle and the target point (§6.3) and stores the target
  point as the record's aim point. It reads the slot's stored yaw once, for
  `RockUnit`'s recoil direction (`slotYaw − heading`), and never reads the
  stored pitch;
* the **ballistic creator** copies the slot's stored yaw and pitch into the
  record (§6.4).

So for a turret weapon authored `lineofsight` or `selfprop`, `accuracy`, the
health term and the kill divisor change the recoil direction and **nothing
else**: the shot leaves exactly toward the aim point, at full health or near
death. Only `ballistic` turret weapons scatter. Burst clones inherit the
root's velocity, so a burst of an ordinary weapon is unjittered too until its
own spray (§4.3).

This qualifies `[R-WPN-03 §4]`: a nonzero spread bound steers the shot for
ballistic weapons only. Where that section's "Retention after a full pool"
bullet has the executor add the heading and a second spread to the
already-rewritten angles and fire off-axis by that much, a ballistic shot does
fire off-axis by that much while an ordinary shot's trajectory is unaffected —
only its recoil direction carries the accumulated error. §4.4's "mutated firing
geometry" is likewise the slot's angles, not an ordinary shot's path.

#### The "could not fire" bit is bit 12 of the unit's order-event word [R-WPN-05 §6]

**Established (direct-static).** §3.3 says a failed shot-time gate "sets the
shooter's *could not fire* status bit", and §4.2 says a successful shot sets
`0x400` or `0x800` in the unit's "fired this tick" status word. They are the
same 16-bit word — the unit's **order-event word**, the one the order pump
merges with each record's pending word (`[04 R-ORD-01 §0]`) — and the bits
are:

| Bit | Producer (this document unless cited) |
|---:|---|
| `0x400` | a successful non-`commandfire` shot (§4.2) |
| `0x800` | a successful `commandfire` shot (§4.2) |
| `0x1000` | **could not fire**: the slot pipeline when the shot-time physical gate of §3.3 fails (reload was zero, so a shot was attempted), and the turret executor when its aim geometry yields no solution (the latter also clears the Aim latch) |
| `0x2000`, `0x4000` | the damage-reaction site's feedback bits (`[R-WPN-04 §2]`) |
| `0x8000` | the under-construction wait (`[04 R-ORD-01 §0]`) |

**Who clears it.** Three sites, and nothing else:

1. **The order pump**, once per record it visits: it forms
   `satisfied = (recordPending | unitEventWord) & recordGate`; when the gate is
   nonzero and `satisfied` is empty it stops at that record and the unit word
   is left as it was; otherwise it clears the satisfied bits from **both** the
   unit word and the record's pending word, zeroes the gate, and hands
   `satisfied` to the handler. The bit is therefore consumed by the first
   record whose gate names it, and survives across ticks until one does.
2. **The slot target setters** of §3.2 (bind slot to unit, bind slot to
   point), which clear bits 10–14 of the word (`[04 R-ORD-01 §7]` already
   records this as "clear bits 10–14 of the owner's capability word").
3. Unit construction, which zeroes the word.

**Who reads it.** Only the pump's merge. Handlers see the bit inside their
satisfied set: `Attack_NoMove` phase 2 is reached only when it arrives and
answers by inhibiting all slots and re-arming; `Attack_Chase`'s masks carry
it too (`[04 R-ORD-01 §3]`). The weapon layer never reads it, so its absence
changes no firing decision — it is the attack handlers' *disengage* signal:
"my weapon tried and could not", raised at most once per slot visit and
latched until an order consumes it or a new target is bound.

**A width fact for `[04 R-ORD-01 §0]`.** The unit word is loaded as a
zero-extended 16-bit value when merged, so gate bit `0x10000` can be
satisfied only from the record's own pending word; the standing question
there — "what writes bit 16 into the unit capability word" — has the answer
*nothing can*.

#### The water branch is `waterweapon`; the targeting-upgrade gate is capability word A bit 10 [R-WPN-05 §7]

**Established (direct-static).** Both admission gates — the acquisition-time
gate of §3.1 and the shot-time gate of §3.3 — choose their water branch on
**bit 16 of the weapon definition's flag word**, the bit the weapon parser
writes for the authored key `waterweapon` (`[02 R-KEYS-01]`). `noautorange`
(bit 27) is read only by the expiry rule (§6.3, §7.3) and plays no part in
admission. The candidate-side tests inside the water branch read the
*unit* definition's capability word A: `floater` (bit 19) and `canhover`
(bit 12), as `[04 R-SPEC-01 §0]` has them.

The registry's secondary-list gate of §3.1 reads **word A** bit 10 of the
unit definition's two capability words (`[04 R-SPEC-01 §0]`), the storage of
`istargetingupgrade`; word B is not consulted there.

#### The wind words are raw −2·speed·trig integers added to 16.16 positions [R-WPN-05 §8]

**Established (direct-static).** The three wind globals the ballistic and
dropped integrators add to a record's position (§6.4) are the words the wind
change of `[01 §7.3]` publishes: the X word is `−2 × sin(heading, speed)` and
the Z word `−2 × cos(heading, speed)` using the §3.3 scaled trigonometry with
the **integer wind speed** as the magnitude (so each lies in
`[−2·speed, +2·speed]`), and the Y word has **no writer** in the decompiled
set — it is the zero the battle started with. `[03 R-WIND-01]` names the axes
from the consumer side; this states the scale on the projectile side: the
words are added to the 16.16 position words **as they are**, with no shift, so
a shell drifts by `windX / 65536` world units per tick along X — at the
largest stock `maxwindspeed` of 5,000 that is at most `10,000 / 65,536 ≈ 0.15`
world units per tick, about 4.6 world units per second. The `× 8` of the
smoke family and the `× 2` of the feature fire probe belong to those
contracts (`[03 R-WIND-01]`), not to projectiles. An implementation that
stores the published words as raw 16.16 velocity increments is exact.

#### The two admission gates are two routines, and the per-slot fire gate has no target-side clause [R-WPN-05 §9]

**Established (direct-static).** §3.1's acquisition-time admission, §3.3's
shot-time gate and `[R-WPN-05 §1]`'s order-side gate are exact as written; what
no one of them says in one place is this.

**They are two distinct routines.** The unit-to-unit gate of §3.1 and of
§1 is *one* routine (it takes the shooter unit, the target unit and the slot;
its clauses are §1's list, with the range test last). Its callers are the
autonomous acquisition's per-candidate test, the order handlers
(`Attack_Chase` and `Guard` among them) and the cursor-shape chooser: it runs
when a target is **installed** on a slot, never per shot. The shot-time gate of §3.3 is a *different* routine taking the
shooter, the shooter's position triple, the resolved target **point** and the
slot. Its clauses, in the order they are evaluated:

1. **range** — `((dx·dx) >> 32) + ((dz·dz) >> 32) <= range·range`, inclusive,
   signed 32-bit, on the raw 16.16 deltas from the shooter's position to the
   target point; evaluated **first**, for water and non-water weapons alike;
2. **non-water weapon only**: `(int16)(shooter.Y >> 16) + shooterModelTop >
   sea`, strictly greater, else refuse — the top-height term is the unit
   definition's model total-height whole-unit word (`[03 R-P0-18-A §1]`; the
   same word §3.1 calls `referenceHeight` and §1 `ModelTop`), and it sits
   **here, on the shooter side only**;
3. **non-water and `ballistic` only**: refuse when the solver, given the
   shooter-minus-target deltas on all three axes, returns the no-solution
   sentinel.

A water weapon runs clause 1 and admits. There is **no target-side clause of
any kind**: no target height or model top, no `floater`/`canhover` medium
test, no `toairweapon` mover-mode test, no alliance, no category. The target
contributes only its point — X and Z to clause 1, all three axes to clause 3.
The two routines also order the range test differently (last in the
unit-to-unit gate, first here); both orders are as traced and neither has a
side effect, so only which clause fails first differs.

**One fire path, both target kinds.** The slot pipeline (§3.3) is the only
site that fires, and it does not know whether the slot's target was installed
by an order or by autonomous acquisition: both are resolved to a point by the
same helper, and once the slot's reload counter is zero the pipeline runs the
shot-time gate, then the cost precheck, then the executor. The shot-time
gate's other callers are the computer player's rally admission
(`[08 R-AI-01 §7]`) and a presentation-side cursor-shape chooser; neither
adds a clause.

For the implementation: the shot-time site must carry exactly clauses 1–3
above — with the model-top addend and the whole-unit compare, not a bare
16.16 compare against sea level — and the target-side clauses of §3.1/§1
belong to the acquisition and order-installation gate only.

### 3.4 The weapon-query path [R-P0-07]

**Established fact:** The slot selects its pieces through four query jobs that
must not be collapsed into a single "muzzle piece" lookup:

1. QueryPrimary, QuerySecondary, or QueryTertiary supplies the **muzzle**
   piece — the point a projectile spawns from — and is synchronous. Every
   fire-time executor runs it in the forced form: cell 0 seeded 0, the
   `AimFrom*` entry not consulted.
2. AimFromPrimary, AimFromSecondary, or AimFromTertiary supplies the piece from
   which the weapon **aims** — the origin the angle solvers measure from — and
   is synchronous. Its sentinel is −1; only that sentinel invokes the matching
   Query fallback. The fallback form serves the aim solve and the slot
   initializer's distance word (`[R-WPN-05 §3]`), never the spawn point: on
   most stock models the two entries name different pieces (a Peewee aims from
   its upper arms and fires from its barrel flares).
3. SweetSpot supplies the selected target-piece offset and is synchronous where
   the target path requests it.
4. AimPrimary, AimSecondary, or AimTertiary is a deferred command carrying the
   computed heading and pitch; its return value is delivered through an
   embedded receiver and controls readiness for the weapon families that
   require an Aim result.

The query callbacks are engine-to-COB mode-Q calls. Their output is consumed
immediately by the engine; they are not a presentation event and do not grant
fire permission merely by returning a piece. [04 §5.3]

#### Query callbacks, seeds, and fallback — Established [R-P0-07]

**Established fact:** The callback names are selected by slot index (0, 1, or
2) and are not hardcoded to the primary weapon. The recovered algorithm is:

```text
AimPiece(k):                      // the AIM ORIGIN — §3.3's solvers, the distance word
    piece = -1
    AimFrom[k](piece)             // mode Q, cell 0 seeded -1
    if piece == -1:
        piece = 0
        Query[k](piece)           // mode Q, cell 0 seeded 0
    return piece

MuzzlePiece(k, piece):            // the SPAWN POINT — every fire-time executor, §4.1
    if piece < 0:                 // -1 means "ask the script"; a stored piece (the
        piece = 0                 // burst re-query, §4.3) makes no call at all
        Query[k](piece)           // mode Q, cell 0 seeded 0; AimFrom[k] is NOT consulted
    return piece
```

The two are separate routines with separate callers, and an implementation
that collapses them spawns every shot at the aim origin.

**Established fact:** The mode-Q dispatcher runs the script synchronously and
copies back only cell 0; cells 1 through 3 are seeded zero by the dispatcher
but are not copied to the caller. A script that sleeps or waits leaves the
partial cell-0 value in the host while the Q thread remains live; the engine
does not invent a second piece or retry the query within that call. [04 §4.4]

**Established fact:** The seed distinction is load-bearing. Query starts from
piece 0 while AimFrom starts from −1, so a missing AimFrom entry falls through
to Query, whereas a missing Query entry leaves the already-seeded root piece 0.
A negative or invalid result from either query is handled by the normal
root-muzzle fallback and is never a reason to authorize a shot.

**Established fact:** SweetSpot is a separate synchronous query. Its cell 0
starts at 0 and the selected piece is transformed into a target world offset
before target-point geometry is solved. The target path must not substitute the
shooter's muzzle piece for this query. (§3.3 owns the target-point geometry.)

#### Per-slot and aim-time pipeline — Established [R-P0-07]

The unit sweep visits the three armed slots in ascending order. Each visit
decrements a nonzero reload timer, resolves or retains the target and computes
the target point (including `SweetSpot` on the target where required), abandons
a slot with no installed executor, and then performs the executor's aim-time
work. Only after that work does a zero reload timer admit the visit to the
fire-time pipeline below.

Aim-time work is executor-specific:

- **Turret:** when the Aim-request latch is clear, synchronously runs the
  `AimFrom[k]`/`Query[k]` fallback, transforms that piece to world space,
  solves heading and pitch, and dispatches the deferred `Aim[k]` callback.
  This visit only requests Aim; its result can arrive later through the slot
  receiver.
- **Vertical launch:** when its latch and stockpile-ammunition gates pass,
  dispatches `Aim[k](0, 0)`. It performs no muzzle query and solves no angles
  at aim time.
- **Line-of-sight/self-propelled and dropped:** perform no Aim dispatch and do
  not read or write an Aim latch at this stage.

#### Reload-zero fire-time pipeline — Established [R-P0-07]

When reload is zero, the outer slot path first applies the physical
range/medium admission of §3.3 and then, for a non-stockpile weapon, the
energy/metal precheck of §4.2. Only an admitted attempt that passes its
applicable resource or ammunition gate enters its executor:

- **Turret:** requires both the Aim-request latch and a nonzero Aim result,
  re-solves current geometry from a fresh `AimFrom[k]`/`Query[k]` aim-origin
  query and applies the angular-drift gate. It then runs the forced
  `Query[k]` muzzle query (piece −1: cell 0 seeded 0, `AimFrom[k]` not
  consulted), transforms the selected piece, converts yaw from relative to
  absolute, applies the accuracy spread, and calls the ordinary creator for
  `lineofsight` or `selfprop`, the ballistic creator for `ballistic`, or no
  creator otherwise.
- **Vertical launch:** requires a nonzero Aim result but does not test the Aim
  latch. It runs and transforms the forced `Query[k]` muzzle query, writes
  absolute yaw and pitch, performs the interceptor rescan when authored, and
  calls the vertical-launch creator.
- **Line-of-sight/self-propelled:** uses neither Aim field. It runs and
  transforms the forced `Query[k]` muzzle query, solves absolute yaw and
  pitch from that point, applies its drift gate against the unit's own
  heading and pitch, and calls the ordinary creator. It applies no accuracy
  spread and never consults `AimFrom[k]`.
- **Dropped:** has no Aim or drift gate. It runs and transforms the forced
  `Query[k]` muzzle query and then performs its inline allocation and
  initialization.

The selected muzzle piece is stored on a successful root projectile so a
later burst attempt can refresh its world position from the live piece.
Executor failure leaves costs, reload, ammunition and firing-status mutation
undone; §4.4 states the family-specific work that remains observable after a
full-pool failure.

#### Successful creator initialization and callback pipeline — Established [R-P0-07]

The ordinary, ballistic and vertical-launch creators reserve the root
projectile before initialization. A successful reservation then runs the
common initializer, whose last action emits the start sound, followed by the
matching deferred `Fire[k]` callback, `RockUnit`, and start-smoke work in that
order. A full pool returns before all of those actions. The dropped executor is
the exception: its muzzle query already ran before its inline reservation, and
even on success it emits neither `Fire[k]` nor `RockUnit` nor start smoke.
Burst clones repeat none of the successful-root callback pipeline. Reload,
ammunition, firing status and applicable resource debits are committed only
after the executor reports success (§4.2).

**Established fact:** The piece transform uses the current COB piece state and
the loaded model hierarchy, then adds the unit world position. The loaded 3DO
translation conversion is used as-is; the muzzle query does not apply a second
sign negation, and the screen projection's trailing Z − Y/2 shear is not a
second model-space conversion. The stored muzzle piece is not a substitute for
the synchronous query, and burst clones do not re-query unconditionally.
[03 §2.4]

#### Aim dispatch mechanics — Established [R-P0-07]

**Established fact:** The Aim callback is deferred (mode D) with arity two:
unsigned 16-bit heading followed by unsigned 16-bit pitch. The producer clears
the slot's Aim state, stores the commanded angles, starts the callback with the
receiver embedded at the slot record, sets the issue bit, and emits the type
0x10 aim event when that event channel is enabled. Readiness delivery from the
receiver is specified in §3.3. [04 §5.3]

**Established fact:** Two Aim forms exist. The ordinary/ballistic form computes
relative heading as target bearing minus unit heading and stores the ballistic
pitch; the ballistic no-solution sentinel 0x8000 suppresses the Aim start
entirely. The fixed-forward form starts with (0, 0) when the weapon's
definition flag word selects the vertical-launch family (the `vlaunch` bit set
and the `turret` bit clear), the issue bit is clear, and — for a `stockpile`
weapon — the slot's ammunition byte is nonzero `[R-WPN-03 §6]`.

#### The Aim-completion receiver, the fixed-forward gate, and SweetSpot's script [R-WPN-03 §6]

**Established fact.** The Aim-completion closure's writer and consumer are
both `[R-CB-01 §6]`: the
receiver word in every weapon slot is written once at unit creation with the
address of a fixed two-entry dispatch table, the interpreter's return path
calls that table's first entry with the delivered cell, and that entry is a
two-branch setter that stores the literal `1` into the slot's aim-ready word
when the delivered value is nonzero and does nothing otherwise. The contract
stated in §3.3 — zero delivery leaves the slot without permission, nonzero
grants it, no timeout — is therefore confirmed from the target's own body.
The turret executor's readiness test reads that word for nonzero together
with the issue latch; the vertical-launch executor reads the word alone.

**Established fact.** The fixed-forward branch is chosen by the weapon
**definition's** flag word — the `vlaunch` bit set with the `turret` bit clear
— not by any slot-record status, and its extra condition is the `stockpile`
ammunition test, not a tracked-target test `[R-CB-01 §3]`. A tracked target is
required upstream by the pipeline, which abandons the slot when no target point
resolves.

**Established fact.** `SweetSpot` is dispatched on the **target** unit's
script, not the shooter's `[R-CB-01 §3]`: the target-point resolver runs the
synchronous query against the target's VM with cell 0 seeded to zero and
transforms the returned piece through the target's model to a world offset.

#### The target-point resolver: point-target height, the dead-target clear, and SweetSpot's vertex-box centre [R-WPN-04 §1]

**Established — the resolver's outcomes.** The per-slot target-point resolver
that §3.3 runs on every slot visit before the executor answers from the slot's
encoded target (§1.2), in this order:

* **Point target** (unit sentinel absent): X and Z are the two stored words
  promoted to 16.16 (`word << 16`); Y is
  `max(bilinearTerrainHeight(X, Z), seaLevelByte) << 16` — the bilinear
  interpolation of §12.2 at that point, floored at the sea-level byte, so a
  ground point below the water plane is aimed at the surface. Success, and no
  lead is ever applied to a point target.
* **Unit target, slot index zero:** failure (no target).
* **Unit target whose unit's definition index is now zero** (a freed slot):
  the slot's target words are rewritten to the empty encoding (index zero with
  the unit sentinel) — only when they are not already that — and the deferred
  `TargetCleared` callback is started with one argument, the slot index. The
  resolver then returns failure. (It also performs a name lookup of
  `StartBuilding` in the shooter's script and discards the result: a lookup
  with no dispatch and no observable effect, recorded so that a clone does not
  look for a missing callback.)
* **Live unit target:** the point is the `SweetSpot` transform below, then the
  pre-fire lead of §3.3 when its five gates pass. Success.

**Established — `SweetSpot`'s piece-to-world transform is the piece's vertex
bounding-box centre, untransformed.** `SweetSpot` is dispatched synchronously
on the **target's** script with cell 0 seeded zero (`[R-WPN-03 §6]`); the
returned piece index selects a piece of the target's loaded model, and the
resolver computes, over that piece's own vertex list (`[fmt 3do]`, in the
model's 16.16 units as loaded):

```
minX = minY = minZ = 0 ; maxX = maxY = maxZ = 0     ; seeded at the piece origin, NOT the first vertex
for each vertex v of the piece:  min = min(min, v) ; max = max(max, v)   (per axis, signed)
point = target.position + (max + min) / 2           ; per axis, signed truncating halving
```

Three consequences, each Established from that shape: (1) the box always
contains the piece origin, so a piece whose geometry lies wholly on one side
of its origin gets a centre pulled toward the origin; (2) neither the piece's
offset from its parent nor the current COB piece state (turn, move, hide)
enters — the offset is the piece's *own* vertex cloud about its *own* origin,
added to the unit's world position — so a script that returns a turret piece
aims at the unit position plus that piece's local geometry centre, not at the
turret's animated world position, and the muzzle-side piece transform of §3.4
(which does apply the hierarchy and piece state, and negates Z once on output,
`[03 R-RAST-01 §8]`) is **not** reused here — the model-space triple is added
to the position as-is, with **no** Z negation, so a piece whose box is offset
along Z lands mirrored against where the model pass draws it; (3) a piece with
no vertices yields the unit position exactly. A piece index
outside the model's piece table reads past it; shipped scripts return indices
of their own model.

## 4. Firing callbacks, costs, reload, and bursts

### 4.1 Fire callback order

**Established fact:** The presentation order on the firing side is fixed:
**pool reservation, then the weapon's start sound, then the matching
FirePrimary, FireSecondary, or FireTertiary callback, then RockUnit, then start
smoke.** The start sound is emitted as the last act of the common initializer,
so it precedes the Fire callback rather than following it. The ordinary,
ballistic, and vertical-launch creators all follow this order; the
dropped-family inline allocator emits neither Fire nor RockUnit, and the meteor
creator runs only the common initializer and copies its packet velocity. Burst
clones rerun none of it.

**Established fact:** The pool reservation happens **first** in the ordinary,
ballistic, vertical-launch and meteor creators — before the common initializer
and before any velocity arithmetic — and consists of: test the live count
against the hard cap of 300; take the record at that index; increment the count;
clear the record's dead bit; clear its retained unit target. A full pool returns
failure with nothing else done. The dropped executor is the exception: it runs
its muzzle query before reserving.

**Established fact:** A muzzle piece is queried synchronously before
initialization through the **forced `Query*` form** of §3.4. The engine passes
a piece argument of −1 to mean "ask the script": the query then calls
`QueryPrimary`/`QuerySecondary`/`QueryTertiary` with a single in/out argument
preset to **0** and takes whatever the script leaves there, so a script without
the entry answers piece 0, the root. `AimFromPrimary`/`AimFromSecondary`/
`AimFromTertiary` is **not** consulted here by any of the four executors: the
`AimFrom*`-with-`Query*`-fallback form is the aim origin of §3.3 — the point
the turret and direct solvers measure from and the slot initializer's distance
word subtracts (`[R-WPN-05 §3]`) — and on most stock models it names a
different piece from the muzzle (a Peewee aims from `ruparm`/`luparm` and
fires from `rfire`/`lfire`; a tank aims from its turret and fires from the
flare at the end of its barrel). The resulting piece index is converted
through the unit's current piece transform and added to the unit's world
position; a negative or invalid result resolves through the normal
muzzle-position path and is never a reason to authorize a shot. A non-negative
piece argument (the burst re-query, §4.3) uses that piece directly and makes
no COB call. The root projectile records the muzzle piece identity so a later
burst clone can re-query the muzzle world position `[R-P0-07]`.

**Established — the sense of "added to the unit's world position".** The
composed piece offset is in model space, which is mirrored in Z against world
space, and the piece locator negates the composed Z **once, on output**, so
every simulation consumer forms `world = unitPosition + (x, y, −z)` with no
further sign change `[03 R-RAST-01 §8]`. The muzzle query of this section and
the burst re-query of §4.3 are two of that section's named callers, alongside
the nanolathe source point and the two piece-position COB ports; the factory
build plate resolves the same way (`[05 "Factory production lifecycle"]`). So
the muzzle sits exactly at the barrel the model pass draws, whose vertices
narrow as `hi16(−vz)` against an unnegated unit position `[03 R-RAST-01 §2]`.
An implementation must not negate per consumer, negate inside the composition,
or skip the negation.

**Established fact:** FirePrimary, FireSecondary, or FireTertiary is a deferred
zero-cell callback and RockUnit is a deferred two-cell callback. RockUnit's
recoil arguments are `(-cos(rel, 800), -sin(rel, 800))` in the §3.3
scaled-trigonometry form, where `rel` is the slot's stored commanded barrel
heading minus the unit heading as a signed 16-bit difference `[R-P0-07]`.

**Established fact:** The common initializer, given the record, the weapon
definition, the muzzle point, an optional aim point, the current tick and the
shooter, performs exactly: store the definition; copy the muzzle point into
**both** the current/head point and the second (tail/waypoint/start) point;
copy the aim point into the stored target point **only when it is non-null**,
leaving the previous occupant's stored target point in place otherwise; clear
the beam latch; set the creation tick; clear the burst-remaining count; clear
the projectile link; seed the smoke deadline to the current tick; clear the
retained unit target; clear the two-phase state bits; and then either, for a
null shooter, write the neutral side byte 10 and a null shooter reference, or,
for a real shooter, write the shooter's side byte and reference, capture the
record as the followed projectile when the shooter is the follow-camera unit,
find the shooter's slot index by scanning its three slots for this definition,
resolve and store the firing piece, and stamp the shooter's "fired recently"
deadline at **current tick + 600**. It finally hands the weapon's start-sound
identity and the muzzle point to the audio layer. It does **not** clear the
whole reused record, does not initialize velocity, angles, scalar speed or
expiry, and does not clear the stored target point.

**Established fact:** Fire callbacks are not called when the projectile pool is
full, because the reservation precedes them in every creator that emits them.
Start and trail smoke are separate engine events controlled by weapon flags and
delays (§7.3).

### 4.2 Costs and reload

**Established fact:** `energypershot` and `metalpershot` are single-precision
per-shot amounts with no scaling. They are **prechecked before the executor
runs** — that is, before the Fire callback and before the projectile record
exists — with two inclusive single-precision comparisons against the owning
player's live energy and metal buckets:

```
fire is permitted when  energypershot <= player.energy
                  and   metalpershot  <= player.metal
```

and are **debited after the executor returns success**, that is after the Fire
and RockUnit callbacks have been queued. The debit helper repeats both
comparisons; on success it subtracts the energy, adds it to the shooter's
energy-used accumulator, then re-reads the player and re-tests the metal
comparison before subtracting the metal and adding it to the metal-used
accumulator. The metal test is genuinely re-evaluated, and only the metal half
is skipped if it were to fail. Nothing between the two tests can change the
metal bucket, so no stock outcome differs — the precise shape is recorded
because a clone that folds the two tests into one has silently chosen a
different contract for any future path that could interleave `[R-WPN-01 §7]`. A
`stockpile` weapon takes neither path: it decrements ammunition and performs no
per-launch resource debit.

**Established fact:** On a successful non-stockpile shot the unit's "fired this
tick" status word receives bit `0x800` when `commandfire` is authored and bit
`0x400` otherwise, and the slot's reload timer is written with integer
truncation in this order (all divisions truncate toward zero; `reloadtime` is
already `trunc(authored seconds × 30)` stored as a signed 16-bit tick count):

```
veteran tier   = min(unsigned kills / 5, 5)
veteran reload = ((100 - 6 * veteran tier) * reloadtime) / 100      ; signed
health factor  = 120 - (unsigned)(signed health * 20) / unsigned maximum health
stored reload  = (health factor * veteran reload) / 100             ; signed
```

and the result is stored into the slot as a signed 16-bit tick count. The stored
value is decremented once per tick at the top of the slot's own pass, so a
stored reload of *n* blocks *n* subsequent ticks. Stockpile launch does not
write reload. The zero-maximum-health contract is closed in §9.1 (healing clamps
to zero without dividing; the TakeDamage percentage and this health term perform
unguarded unsigned divisions and must be guarded as an error path). Negative
health and overflow outside ordinary state remain malformed-state unknowns.

### 4.3 Burst state

**Established fact:** A weapon's authored `burst` count is copied into the root
projectile as a signed 16-bit remaining count by the ordinary, ballistic and
vertical-launch creators (the dropped and meteor creators leave it at the zero
the common initializer wrote). The root's creation-tick field doubles as the
burst deadline. A due attempt decrements the remaining count and **adds**
`burstrate` to that deadline rather than re-anchoring it to the current tick.

**Established fact:** Burst spray and random decay consume the **simulation**
RNG, in this order and only when the clone allocation succeeded:

1. **Random-decay draw (first):** when `randomdecay` is nonzero, one simulation
   draw with bound equal to it; the **clone's** expiry becomes
   `expiry + draw - (randomdecay >> 1)` — a centred expiry jitter on the clone
   only, with the halving an unsigned 16-bit shift.
2. **Spray draw (second):** when `sprayangle` is nonzero, one simulation draw
   with bound equal to it. The perturbed heading is
   `a = draw + (int16)(parentYaw - (sprayangle >> 1))`, and the **parent's**
   velocity X and Z are rebuilt from `a` and the parent's **unchanged** pitch
   using the weapon's authored `weaponvelocity` as the magnitude:
   `H = cos(parentPitch, weaponvelocity)`, `velocityX = -sin(a, H)`,
   `velocityZ = -cos(a, H)`. The parent's velocity Y is left alone and pitch is
   never jittered.

**Established fact:** The parent's *stored heading* is not rewritten. `a` is
computed into a register, used for the two velocity components, and discarded,
so the parent's stored yaw keeps its original value for the whole burst and
successive pellets scatter around the **original** aim direction instead of
random-walking away from it. The distinction is observable from the second
pellet onward `[R-WPN-01 §2]`. There is no "wobble field" adjacent to
`sprayangle` — `sprayangle` and `randomdecay` are separated by `duration` in
the record — and the only two-draw yaw-and-pitch site is the turret executor's
accuracy spread of §4.4, whose bound is computed, not authored.

Both draws use the shared simulation generator, which **returns zero without
advancing the stream when its bound is below two**; so an authored `randomdecay`
or `sprayangle` of 1 consumes no randomness at all. The bounds are authored
values and the count is at most two per successful clone, so both are inputs to
the shared simulation sequence: a wrong bound or a wrong count desynchronizes
every later draw in the game, not merely the pellet.

**Established fact:** A root whose remaining burst count is nonzero is a
scheduler/template, not an ordinary moving projectile. It is a stationary anchor
parked at the muzzle: while its count is nonzero it takes the burst branch
**instead of** all motion, collision, expiry, and trail smoke. On each due
attempt the engine, in order:

1. refreshes its position from the live muzzle **when `burstrate` is strictly
   greater than four or the remaining count is odd** — the refresh re-runs the
   piece-to-world conversion with the *stored* firing piece, so it makes no COB
   call and cannot alternate between the Query and AimFrom callbacks
   `[R-P0-07]`;
2. decrements the remaining count and advances the deadline by `burstrate`;
3. reserves a clone from the pool, and stops here if the pool is full;
4. copies the parent's whole 107-byte record into the clone and sets the clone's
   creation tick to the current tick;
5. emits the clone-trigger sound when `soundtrigger` is authored;
6. writes the clone's expiry: `now + weapontimer` when `weapontimer` is nonzero,
   otherwise `now + (storedPlanarDistance + 0x100000) / scalarSpeed` — an
   unsigned division of the root's stored muzzle-to-aim planar distance plus one
   cell (16 world units in 16.16) by the root's scalar speed;
7. applies the random-decay and spray draws above;
8. clears the clone's own remaining burst count, so it is an ordinary moving
   projectile on the next projectile phase.

**Established fact:** The first attempt becomes due when
`(uint32)(creationTick + burstrate) <= currentTick`. An interval of zero can
therefore emit a clone during the root's creation tick, but the captured phase
span still prevents that clone from moving until the next tick. The scheduler
makes at most one emission attempt per projectile phase; it does not loop to
catch up several overdue intervals.

**Established fact:** The clone copy happens before spray is calculated. Spray
changes the parent/template velocity, so it prepares the velocity inherited by
the next successful clone; it does not change the clone just emitted. The first
clone uses the root's initially aimed velocity. Random decay changes the
successful clone's expiry. Burst clones do not rerun the root Fire or RockUnit
callbacks. Every successful attempt consumes a spray sample when spray is
configured, including the final attempt; the final sample is written to the
soon-retired parent and has no later moving clone to inherit it.

**Established fact:** Pool-full failure still consumes that burst attempt: the
parent count and deadline have already advanced. No clone is made, and that
attempt emits no clone-trigger sound and consumes no spray or random-decay RNG.
When the count reaches zero the anchor takes the follow-camera snap and has its
dead flag set **directly, with no removal dispatch**: no explosion, no sound, no
shake, no end smoke, and no damage. With nonnegative authored state and no
failed allocations, the number of moving clones equals the initial remaining
burst count, so an authored burst of N produces N flying pellets plus one
immobile anchor that removes itself silently at the instant pellet N launches. A
root with an initial count of zero follows the ordinary moving-projectile path
instead.

**Established fact:** When a shooter dies, a separate sweep walks the whole pool
and, for every record whose remaining burst count is nonzero and whose shooter
reference is that unit, takes the follow-camera snap, sets the dead bit, and
runs the compaction pass of §5.2 **inside the loop** — so the pool is compacted
once per anchor found while the same ascending walk continues over the moved
records. A unit that dies with two anchors alive therefore compacts twice.

### 4.4 Pool-full allocation retention

**Established fact:** A failed projectile allocation returns failure before any
record is initialized, but only after family-specific pre-allocation work has
already run. The outer slot pipeline mutates reload, ammunition, firing status,
and resources only when the selected executor returns nonzero, so retained work
is observable through simulation-RNG consumption and mutated firing geometry,
not through cost or reload state.

**Established fact:** The accuracy spread lives in the **turret** executor and
only there. Its bound is computed from the parsed `accuracy` field, the
shooter's health and maximum health, and the shooter's credited-kill count:

```
divisor  = (uint16)kills / 12                                   ; unsigned
health   = ((int32)currentHealth << 11) / maximumHealth         ; unsigned divide
bound    = (uint16)(accuracy - health + 0x800)                  ; wraps to 16 bits
if (divisor > 1)  bound = (uint16)bound / divisor               ; signed 32-bit
if ((uint16)bound != 0) {
    half = bound >> 1
    slotYaw   += (int16)(rng(bound) - half)
    slotPitch += (int16)(rng(bound) - half)
}
```

The `<< 11` is the `× 2048` the image performs as a shift. The divisor is only
applied when it exceeds one, i.e. from 24 credited
kills upward. Both draws use the same bound and the shared simulation generator,
which does not advance the stream when the bound is below two. The spread is
applied **after** the muzzle query and after the slot yaw has been converted
from relative to absolute by adding the unit heading, and **before** the creator
call, so the two draws are consumed even when allocation then fails.

**Established fact:** The spread belongs to the executor selected by the
`turret` flag. The non-turret line-of-sight/self-propelled executor also
reaches the ordinary creator and computes no spread and consumes no randomness
at all; the vertical-launch and dropped executors likewise. A weapon without
`turret` therefore fires exactly on its solved angles regardless of `accuracy`
and consumes zero draws — a determinism contract, not only an accuracy one
`[R-WPN-01 §3]`. `accuracy` is the spread's base term; `tolerance` and
`pitchtolerance` belong to the §3.3 drift gate, not to this spread.

**Established fact:** For the turret executor, a failed allocation retains
target and trajectory validation, the synchronous muzzle query, the
relative-to-absolute slot yaw rewrite, and the spread's up-to-two simulation
draws. Suppressed on failure: the record itself, Fire/RockUnit callbacks, start
smoke, the shot packet, the pending-slot clear, reload, ammunition, firing
status, and resource mutation.

**Established fact:** The vertical-launch executor retains the muzzle query, the
slot angle rewrite, and the fire-time interceptor rescan on failure. The
line-of-sight/self-propelled executor retains the muzzle query, the slot angle
rewrite and the drift gate, and draws nothing. The dropped executor retains the
muzzle query only; it has no Fire/RockUnit tail even on success. Meteor
scheduling retains its random geometry and velocity preparation, so a pool-full
storm tick consumes its C-runtime draws, advances the strike timer, and silently
drops the individual meteor with no retry.

**Established fact:** `aimrate`, `movingaccuracy`, `noselfdamage`,
`impulsefactor` and `impulseboost` have **no parser entry**, because their
spellings occur nowhere in the executable's string data at all — a whole-image
search, not a bounded reader census `[R-WPN-01 §9]`. There is no key, so there
is no field, so no reader can exist: nothing in the firing, readiness, spread,
drift or projectile-motion paths can consume them, and the impulse question of
§9.4 is closed from the parser side. `holdtime` is parsed and is read by
exactly five sites, all of them the follow-camera hand-off described in §7.3;
it has no effect on firing, motion, or damage.

#### The accuracy spread at implementable precision [R-WPN-03 §4]

**Established fact.** The spread block above, with its widths and edges made
explicit.

```
healthTerm = (uint32)((int32)(int16)currentHealth << 11) / (uint32)maximumHealth
                                             ; unsigned 32-bit divide; 2048 at full health
raw        = (uint16)(accuracy16 - (uint16)healthTerm)     ; 16-bit subtraction, wraps
bound      = (uint16)(raw + 0x800)                         ; carry into bit 16 discarded
divisor    = (uint32)(uint16)kills / 12                     ; exact integer quotient
if (divisor > 1)  bound = (uint16)((int32)bound / (int32)divisor)   ; signed 32-bit divide
if (bound != 0) {                                           ; 16-bit zero test
    half = bound >> 1                                       ; 16-bit shift
    slotYaw   += (int16)(rng(bound) - half)                 ; first draw
    slotPitch += (int16)(rng(bound) - half)                 ; second draw
}
```

* `rng(b)` is the simulation generator of `[01 §7.1]`: for `b >= 2` one
  Park–Miller step then `state mod b` (unsigned); for `b < 2` it returns zero
  **without advancing**. A bound of exactly 1 therefore passes the nonzero
  test, adds zero to both angles, and consumes no draw. Both draws use the same
  bound; the yaw draw is taken first.
* **The kill divisor** is applied as a **signed 32-bit** division of the
  16-bit-masked bound by the divisor; both operands are nonnegative, so the
  quotient is the same as an unsigned one.
* **Shape of the bound.** `healthTerm` is `2048 × health/maxhealth`,
  truncated, so `bound = accuracy + 2048 × (1 − health/maxhealth)` (mod
  65,536). A full-health shooter with `accuracy = 0` has bound 0 and fires
  exactly on its solved angles with **no draw**; the same shooter at half
  health has bound 1,024 (±512 angle units, about ±2.8° on each axis); a
  shooter near death has bound `accuracy + 2048`. Twenty-four credited kills
  halve the bound, thirty-six divide it by three, and so on. A health word
  above the maximum (never authored by stock) makes `healthTerm` exceed 2048
  and the 16-bit wrap yields a very large bound.
* **Angle units.** The draw is applied directly to the `uint16`
  angle-per-circle slot angles, so a bound of 65,536/360 ≈ 182 corresponds to
  ±0.5°; the authored `accuracy` is in the same units — not degrees, not a
  percentage.
* **Retention after a full pool.** When the creator then fails, the executor
  returns failure **without** clearing the Aim-issued latch or the aim-ready
  word, and the slot's stored yaw is now absolute-plus-spread while the stored
  pitch carries its draw. On the next tick the drift gate of §3.3 compares
  those mutated values against a freshly solved **relative** yaw and unjittered
  pitch, so the yaw error is the unit heading plus the draw: unless the unit
  faces heading zero to within the gate, the gate fails, the latch is cleared,
  and a new Aim request is dispatched on the following slot visit. When the
  gate does pass (a shooter facing within tolerance of heading zero), the
  executor adds the heading and a second spread to the already-rewritten
  angles and fires off-axis by that much. Both outcomes are the code's, not a
  chosen policy; Nanolathe may bound the second as a sanctioned divergence
  only after the projectile pool contract is otherwise reproduced.
* **What is not in the bound.** Neither `tolerance`, `pitchtolerance`, the
  target's motion, the shooter's motion, the weapon's `range`, nor the
  distance to the target enters the spread. The angular error is independent
  of range, so the miss distance grows linearly with it.

## 5. Projectile pool, identity, and lifetime

### 5.1 Pool contract

**Established fact:** The projectile pool contains 300 records of 107 bytes.
Pool-full checks use that fixed capacity.

**Established fact:** Every identified reservation path appends at the current
active-span tail. It checks the count against 300, computes the next packed
record, increments the count, and clears the new record's dead state. It does
not scan for a free hole.

**Established fact:** Retirement sets the dead flag but does not decrement the
active-span count. A dead record continues to consume capacity until
compaction. Consequently, an allocation attempted during projectile iteration
can fail at count 300 even when earlier records in the span have already been
marked dead.

**Established fact:** The projectile updater does not test the dead flag at the
top of its captured-span loop. A record marked dead before its turn can still
enter its burst-scheduling or family-motion branch until compaction removes it.
Individual termination branches normally leave their own control flow before
the next record, but the dead bit by itself is not an iteration filter.

**Established fact:** Projectile records have logical state for:

- weapon definition;
- current and start positions;
- velocity and speed;
- yaw/pitch;
- optional target position and target unit;
- shooter and shooter side;
- muzzle-piece identity;
- burst deadline and remaining count;
- expiry and smoke deadlines;
- phase/latch flags;
- collision cache values;
- dead state.

The exact packed record layout is intentionally not part of this specification.

**Established fact:** Two of those cached values are collision scratch, both
written by the collision gate (§8.1) on every in-map tick and neither read by
the simulation:

* the **quantized cell pair** — the impact cell's X and Z as
  `(v + (v >> 31 & 0xF)) >> 4` of the current point's high words — is written
  only when a feature contact is *selected*, and is read only by the same test
  on a later tick to suppress a repeated contact with the same feature cell;
* the **floor scratch** is overwritten unconditionally with the plot cell's
  `(neighbourhoodMax + neighbourhoodMin) / 2`, an unsigned byte average, and
  its only reader anywhere in the corpus is the projectile draw pass, which
  subtracts half of it from the projectile's screen position `[R-WPN-02 §7]`.
  It is **presentation only**, and a simulation-side implementation may keep it
  purely for the presentation layer.

**Established fact:** The common initializer clears beam/dead/phase flags, copies current and start positions, sets the creation tick and smoke deadline, clears the target-unit link before family-specific assignment, records shooter side and muzzle piece, and extends the shooter's keepalive deadline. A no-shooter projectile receives the neutral side value used by the executable.

**Established fact:** Pool exhaustion returns failure before common initialization and before Fire/RockUnit callbacks. The pool is reset and emptied on a new mission/reset.

**Established fact:** Ordinary battle setup creates an empty projectile pool
before the standard load dispatcher runs. The complete bounded standard
save/load graph contains no projectile-pool reader or writer. Standard battle
save/load therefore discards in-flight projectile records, including burst
schedulers and their transient links. The followed-projectile pointer is not
reconstructed from such a record. Replay and any undiscovered nonstandard
snapshot path remain separate unknowns.

### 5.2 Compaction and reference behavior

**Established fact:** The projectile phase always invokes compaction at its
tail. The compactor scans the current global span, including clones appended
after the phase captured its iteration count. It finds the first dead hole,
copies later survivors downward, preserves their relative order, and publishes
the reduced count only after the scan.

**Established fact:** Before any copy, compaction writes each original
record's old pool index into a marker field inside that record. It updates the
follow-camera pointer when the followed survivor moves. For each moved
survivor carrying a non-null projectile-to-projectile link, compaction records
the moved source's index together with the target's old index; a second repair
pass rewrites the moved source's link ONLY when a live record still carrying
the target's old-index marker exists after compaction.

**Established fact:** Repair eligibility is exactly that bounded. If the
linked target was removed, no repair write happens and the copied source
retains the old raw pointer, which can address a different live record shifted
into the old address or stale bytes past the new active count. Sources
positioned BEFORE the first dead hole are never moved and never enter the
repair table; if such an unmoved source's linked target shifts left, the
source keeps the old pointer even though the target survived. Nothing at the
later dereference — guidance point reads, proximity contact tests, interceptor
reservation deduplication scans — checks generations, active-count bounds,
dead bits, or weapon identity, so stale links can alter guidance targeting,
proximity impacts, and reservation deduplication.

**Established fact:** Unit-death cleanup separately searches for burst
schedulers owned by the dying unit among active-pool records only; it does not
consult the dead bit during the match test, and it is not a general removal of
the dying unit's projectiles. It marks a match dead and compacts immediately
inside the forward scan. The scan then advances rather than revisiting the
record moved into the vacated slot.

**Supported inference:** If two matching burst schedulers can be adjacent in a
reachable state, that immediate-compaction scan skips the scheduler shifted
into the just-vacated slot. Whether ordinary firing can produce the required
adjacency at unit-death time remains to be proved.

**Established fact:** Unit/target references use the retail slot/pointer checks rather than a generation counter. If a unit dies and a slot is reused, stale references can alias according to the executable's validation path.

**Supported inference:** A clean implementation should preserve stale-reference behavior only after the target validity predicates are known; introducing automatic generation invalidation would change the retail contract.

## 6. Family dispatch and projectile motion

### 6.1 Prerequisite projectile state

**Established fact:** All delivery families share one bounded pool of **300**
packed logical records of **107 bytes** each, and the record holds exactly these
fields, all of which an implementation needs:

- weapon definition reference and owner side byte;
- current/head point and a second tail/waypoint/start point, both 16.16 X/Y/Z;
- stored target point, 16.16 X/Y/Z;
- velocity X/Y/Z in 16.16 world units per tick;
- yaw and pitch as `uint16` angles, plus a scalar speed in 16.16 per tick;
- a stored planar muzzle-to-aim distance in 16.16, written by the ordinary
  creator only;
- creation tick (which doubles as the burst deadline), expiry tick, and smoke
  deadline, all 32-bit unsigned tick counts compared with unsigned tests;
- a retained unit target and a separate optional projectile-to-projectile link;
- remaining burst count (signed 16-bit), firing piece, and shooter reference;
- a visual propeller angle and one further visual accumulator used by the meteor
  family;
- a pre-compaction index stamp (§5.2);
- a state byte holding the beam latch in bit 0, the dead flag in bit 1, and the
  two-phase state in bits 4–5.

**Established fact:** Common initialization is specified verbatim in §4.1. Its
two consequences for the families below are that the second point starts equal
to the muzzle point (so a beam has zero length on its creation tick) and that a
creator which passes a null aim point — the ballistic, dropped and meteor
creators do — leaves the **previous** occupant's stored target point in the
record.

**Established fact:** The propeller visual is not a family. When `propeller` is
authored, the record's propeller angle is advanced by exactly **1,024 angle
units per tick** (5.625 degrees, one sixty-fourth of a circle) at the top of the
ordinary motion path, before any family dispatch, wrapping modulo 65,536. It is
consumed only by the renderer and never by motion or collision.

### 6.2 Creation dispatch versus active motion dispatch

**Established fact:** Two different orderings exist and both must be reproduced.

The **live-fire** ordering is decided once at catalog compile time and is the
executor order of §3.3: `turret`, else `vlaunch`, else `lineofsight` or
`selfprop`, else `dropped`, else nothing. Inside the turret executor a second
choice picks the creator: `lineofsight` or `selfprop` selects the ordinary
creator; otherwise `ballistic` selects the ballistic creator; otherwise nothing
is fired.

The **projectile-event reconstruction** path chooses a root creator with a
different ordered predicate list:

1. meteor creates directly from packet velocity and does not require a live unit
   target;
2. other event paths require a non-null live unit target;
3. ballistic selects the ballistic creator;
4. otherwise vertical-launch selects the vertical creator;
5. otherwise line-of-sight or self-propelled selects the ordinary creator;
6. otherwise dropped selects its small inline creator;
7. otherwise no projectile is created.

Beam, guidance, cruise, propeller, burn-blow, no-explode, render type, and
firestarter do not independently select a creator. Guidance alone is not a
projectile creation family.

**Established fact:** An already-created record uses a third ordering, the
motion dispatch. Only a record whose remaining burst count is zero reaches it.
The propeller advance runs first, then the first matching flag owns the tick:

1. `selfprop`;
2. otherwise `lineofsight`;
3. otherwise `ballistic`;
4. otherwise `dropped`;
5. otherwise `meteor`;
6. otherwise no motion, no collision, and no expiry handling at all.

Flags are therefore composable predicates, not a disjoint family enum.

### 6.3 Ordinary/direct creation and motion

**Established fact:** The ordinary creator, given the slot, the shooter, the
muzzle point `m`, the aim point `t` and the target unit, computes

```
yaw   = atan2q(m.X - t.X, m.Z - t.Z)                              ; absolute
dist  = trunc(hypot((double)(m.X - t.X), (double)(m.Z - t.Z)))    ; stored on the record
pitch = atan2q(-(int16)((m.Y - t.Y) >> 16), (int16)(dist >> 16))
speed = startvelocity != 0 ? startvelocity
      : weaponacceleration != 0 ? 0
      : weaponvelocity
velocityY = sin(pitch, speed)
H         = cos(pitch, speed)
velocityX = -sin(yaw, H)
velocityZ = -cos(yaw, H)
```

using the §3.3 scaled trigonometry throughout. The stored planar distance is the
value the burst clone expiry of §4.3 later divides.

**Established fact:** Ordinary expiry is

```
expiry = (weaponvelocity == 0 || noautorange)
       ? currentTick + weapontimer
       : currentTick + (uint32)(range << 16) / weaponvelocity
```

where `range << 16` is a 32-bit signed shift of the authored integer range,
reinterpreted unsigned for the division, and `weaponvelocity` is the 16.16
velocity per tick `[R-WPN-01 §10]`: the numerator is the
range promoted to 16.16 world units and the quotient is a whole tick count,
truncated. An authored range at or above 32,768 makes `range << 16` negative and
the unsigned reinterpretation enormous, so the expiry wraps; stock content does
not author it. The creator then retains the unit target, copies the authored
burst count, starts the selected Fire callback, then RockUnit, then optional
start smoke.

**Established fact:** A live direct record moves and collides only while
`currentTick < expiry`, an unsigned comparison. At equality or later it retires:
it takes the follow-camera snap and sets its dead bit, without moving, without
colliding, and without delivering expiry damage. When `beamweapon` is authored,
an integrating tick additionally advances the beam: while the latch is clear the
tail stays fixed and the latch is set on the first tick where
`creationTick + duration < currentTick` (strict, and the tick that sets it still
leaves the tail fixed); once latched, the second point advances by the same
velocity vector as the head, preserving the segment length.

### 6.4 Ballistic and dropped motion

**Established fact:** The ballistic solver is specified in full in §3.3. Its
result reaches the ballistic creator as the slot's stored pitch; the slot's
stored yaw is the relative aim angle already converted to absolute by the
executor.

**Established fact:** There is no separate ballistic pitch quantization. The
solver of §3.3 works entirely in floating point and converts the chosen arc
angle to the 16-bit pitch as

```
pitch = trunc(theta × 32768.0 × (1/π))      ; two double multiplies, in that order,
                                            ; then the shared truncate-toward-zero
```

— no table, no rounding to any step; the full 65,536-per-circle resolution is
stored. The ballistic creator then rebuilds velocity from that pitch and the
slot yaw through the **same** scaled sine/cosine helpers every family uses
(§3.3 "Shared trigonometry"), whose entry index is `((angle + 32) >> 7) & 511`
— the `+32` is the residue of the table's even-offset masking and shifts the
quantization boundaries by a quarter entry, so an angle of 96 reads entry 1
while 95 reads entry 0. An implementation whose table index omits that pre-add
drifts. The creator and the per-tick self-propelled rebuild (§6.7) are
quantized identically, at 128 angle units per entry, and no per-tick
accumulation of a quantization error occurs because the velocity is rebuilt
from the stored angle, never from the previous velocity.

**Established fact:** The ballistic creator initializes velocity as

```
T0        = (uint32)slotDistance / weaponvelocity        ; unsigned, whole ticks
velocityY = sin(pitch, weaponvelocity) - T0 * gravity    ; 32-bit signed multiply
H         = cos(pitch, weaponvelocity)
velocityX = -sin(yaw, H)
velocityZ = -cos(yaw, H)
```

where `gravity` is the map's per-tick gravity global in 16.16.

**Established fact:** `slotDistance` is not a flight time to the current
target. The slot's distance word is written **once**, by the slot initializer
at unit construction, as `trunc(1.25 × (queryPoint.z − aimFromPoint.z))` — the
Z-axis difference of the slot's `Query*` and `AimFrom*` world points at that
moment ([R-WPN-05 §3]) — and no aim-time or fire-time path rewrites it
(bounded: no other writer of the word exists among the slot-relative accesses
in the weapon region; the turret executor solves into locals and the solver is
pure). `T0` is therefore a per-unit constant,
`(uint32)initialValue / weaponvelocity`, and because the divide is unsigned a
negative initial value (muzzle behind the aim-from piece along world Z at
initialization) yields a very large `T0`.

**Established (direct-static):** stock units do not reach the negative case.
The slot initializer runs *after* the spawn heading is written, so the delta is
taken at the spawn heading, and the spawn band `0x8000 ± buildangle/2` keeps a
forward-mounted muzzle's world-Z delta positive for every stock `buildangle`
([R-WPN-05 §3]). The very-large-`T0` branch below is therefore not a case an
implementation has to make ordinary shots survive.

The divide really is unsigned: the creator zeroes the high
dividend word and issues the *unsigned* 32-bit divide of the stored word by the
weapon velocity, so a word of −1 becomes a quotient near 2^32 / velocity rather
than −1. Two adjacent facts follow. A **zero** word yields `T0 = 0` and the
launch reduces to the bare angle build with no pre-decrement at all — that is
the "script answered neither piece query" case of [R-WPN-05 §3], not an error.
A zero **velocity** raises the processor divide exception, as the malformed
paragraph below already records.

*Why the sign matters so much.* The pre-decrement is `T0 × gravity` subtracted
from the vertical component, so an inverted word does not merely mis-shape the
arc: at a stock light-cannon velocity a word of the right magnitude and the
wrong sign turns a `T0` of five ticks into some eleven thousand, and the
resulting vertical velocity drives the record more than a thousand world units
straight down on its first integration. The shot detonates underground at its
own muzzle on the tick it is created. An implementation that reads its composed
piece points in the wrong frame reproduces exactly that, and it looks like
"ballistic weapons never fire" rather than like a sign error.

The pre-decrement of
the vertical component by one flight-time's worth of gravity is part of the
launch, not an integrator artefact, and must be reproduced. **Unknown:** the
intended geometric meaning of that pre-decrement, and therefore whether an
implementation may simplify it; the arithmetic itself is Established. *Decider:*
a manual retail observation of a stock ballistic weapon's apex against the
literal expression, run as an authored `probes/` scenario.

**Established fact:** Non-burn-blow ballistic lifetime is timer based:
`expiry = currentTick + weapontimer`. Burn-blow lifetime is not gravity-derived;
it is computed from horizontal distance and the horizontal speed component:

```
wideDistance = trunc(hypot((double)(m.X - t.X), (double)(m.Z - t.Z)))
H            = cos(pitch, weaponvelocity)
T            = wideDistance / H            ; signed 64-by-32 divide, truncating
expiry       = currentTick + T
```

Gravity shapes the solved trajectory and the initial vertical velocity but is
not an operand in this deadline division. A positive `T` permits exactly `T`
ballistic integration visits before the expiry visit; `T == 0` impacts at the
muzzle in the creation tick's projectile phase. None of the malformed cases
below is reachable under ordinary positive stock inputs.

**Established fact:** Malformed ballistic arithmetic is closed case by case. The
gravity-squared product wraps as a signed 32-bit integer multiply before its
double conversion. Muzzle/target X and Z delta components wrap as signed 32-bit
values before their double conversion. A distance whose `hypot` exceeds the
signed 32-bit domain is truncated so the creator keeps the low 32 bits treated
as signed for the deadline division. A zero `weaponvelocity` reaching the
ballistic creator raises the processor divide exception on the unsigned
distance-over-velocity division — and because that creator reserves its pool
record **before** any common initialization and velocity arithmetic, the live
count is not rolled back when the exception fires. In the burn-blow deadline
division, a zero horizontal speed component raises the divide exception; a
negative component yields a quotient truncated toward zero and added modulo
2^32, unreachable from a valid positive-velocity solver result; and a quotient
of minimum negative magnitude with divisor negative one raises the signed-divide
overflow exception.

**Established fact:** Burn-blow deadlines wrap modulo 2^32, and the later
unsigned expiry test is not wrap-aware: a wrapped deadline can satisfy
`expiry <= currentTick` immediately and fire on its first test. With both
burn-blow and no-explode authored, the deadline impact does not retire in the
ordinary impact branch, so an already-expired record repeats the FULL central
impact — presentation and damage alike — on every subsequent visit while the
deadline stays expired. The authored corpus contains no weapon combining the two
flags.

**Established fact:** Ballistic motion per tick is:

```
if (weapontimer == 0)                       ; no expiry test at all
    integrate
else if (expiry <= currentTick)             ; unsigned
    burnblow ? central impact and skip collision
             : expiry puff, follow-camera snap, dead bit, no collision
else
    integrate

integrate:
    point   += velocity                      ; all three components
    point.X += windX ; point.Y += windY ; point.Z += windZ
    velocityY -= gravity
    run current-point collision
```

The three wind globals are added **to the position**, not to the velocity, and
all three are applied including the vertical one. They are the raw
`−2 × trig(heading, speed)` integers of `[01 §7.3]`, added with no shift — one
unit of the word is one 65,536th of a world unit per tick — and the vertical
word has no writer (`[R-WPN-05 §8]`).

**Established fact:** On ballistic timer expiry without burn-blow the record
emits its expiry puff — the same effect emitter the smoke trail uses — and
retires without impact damage.

**Established fact:** Dropped motion is the same integrate block with **no
expiry test**: add velocity, add all three wind values to the position, subtract
gravity from the vertical velocity, and collide, every tick, forever, until
collision or removal ends it.

**Established fact:** The dropped executor is also its own creator and its
initial state is not derived from any aim solution. After the muzzle query and
the pool reservation it sets scalar speed to zero, the record yaw to the
**dropping unit's heading**, the vertical velocity to zero, and

```
velocityX = -sin(unitHeading, moverSpeed)
velocityZ = -cos(unitHeading, moverSpeed)
```

where `moverSpeed` is the **dropping unit's mover's current scalar speed**
in 16.16 per tick — the live motion word, not a weapon field and not the unit
definition's `maxvelocity`. It writes no expiry, no burst count and no pitch,
emits no Fire and no RockUnit callback, and emits no start smoke.

**Established (direct-static) — the magnitude is the mover's live speed, and
which word that is.** The creator reaches the magnitude through the unit
record's mover reference, and takes the mover word that the **mover's own
velocity build** reads: the same word the mover clamps against its terrain-
scaled speed cap and then multiplies by `-sin(heading)` and `-cos(heading)` to
produce its own three velocity components (`[04 R-MOV-01 §4]`). Creator and
mover therefore multiply *the same two operands*, so a released bomb carries
exactly the carrier's horizontal velocity at the release tick and separates
under gravity alone — it stays with the aircraft and sinks beneath it, and is
never launched forward past the nose. Three sites share this inline allocator
verbatim — the dropped executor, the dropped arm of the burst-clone
dispatcher, and the third dropped-family entry — and all three read the same
mover word; none of them reads the definition.

This **corrects** the earlier text of this paragraph, which named the operand
`unitMaxVelocity` and glossed it as "the dropping unit definition's maximum
velocity". The definition's `maxvelocity` is read by other code through the
definition reference hanging off the unit record (the `AirToAir` legs of
`[04 R-AIR-01 §8]` are one such reader); the dropped creator does not read it.
The two are easy to conflate and the difference is visible in play: a mover
approaches `maxvelocity` only asymptotically and sheds speed on every turn, so
under the old reading every bomb outran its bomber. The corrected reading is
also the only one that makes the bombing run's release lead physical:
`AirStrike` phase 4 sizes the release radius as
`moverSpeed · 30 · sqrt(2·cruisealt/gravity)` from that same mover speed word
`[04 R-AIR-01 §8]`, which is the horizontal distance a bomb travelling at the
mover's speed covers during the fall from cruise altitude. Sizing the lead from
the live speed while launching the bomb at the definition maximum would make
the lead wrong by construction.

### 6.5 Meteor creation, scheduling, and motion

**Established fact:** Meteor creation copies an explicit velocity vector and
bypasses the ordinary aim solver. It reserves a pool record, runs the common
initializer with a **null aim point and a null shooter** — so the record keeps
the previous occupant's stored target point and takes the neutral side byte 10 —
and writes the three velocity components verbatim. It initializes no expiry, no
angles, and no scalar speed, so complete reused-slot initialization remains a
reachability concern.

**Established fact:** Each meteor tick adds velocity to the current point,
advances two visual orientation accumulators, and performs ordinary
current-point collision. It does not apply wind, gravity, or any expiry test.
Removal therefore depends on collision, leaving the map, or another external
path.

**Established fact:** Meteor render orientation has no stored angular-rate
field. Each tick advances the first accumulator by
`(int16)(velocityX >> 16) * 256` and the second by
`(int16)(velocityZ >> 16) * 256`, both wrapping modulo 65,536 — effectively the
low byte of each velocity's high half times 256. The rates are read fresh from
the velocity components every tick and feed only presentation rotation, never
motion, so any velocity change alters rotation immediately.

**Established fact:** The two accumulators are not meteor-private fields. The first
is the record's **roll word** — the first of the three orientation words the
renderer hands to the model draw (`[R-WFX-01 §4]` type 1), the one no creator
and no common initializer writes. The second is the record's ordinary
**pitch word**, the same word the ballistic and ordinary creators fill from
the slot. The yaw word is untouched by the meteor tick. The increments are
read each tick from the high 16 bits of the velocity X and Z words
respectively (`hi16(velocityX) × 256` into roll, `hi16(velocityZ) × 256` into
pitch), so no stored angular-rate field exists: the tick
reads the velocity components themselves. Because the meteor creator writes
neither word, both start at whatever the reused pool slot last held.

**Established fact:** Storm parameters are installed once, from the mission's
OTA keys or from `gamedata/meteor.tdf`:

```
radius   = MeteorRadius
spacing  = low32(trunc64(30 / storedDensity))
duration = low32(trunc64(storedDuration * 30))
interval = low32(trunc64(storedInterval * 30))
```

**Established (direct static trace) — conversion boundaries.** Density,
duration and interval are each stored as single precision before installation.
The divide and multiplies then run at the runtime's working precision, with
no single-precision result store before signed-64 truncation and retention of
the low 32 bits [01 R-DET-01 §1]. The single-precision operands do not make
the arithmetic result single precision. Non-finite and signed-64 out-of-range
results retain zero in that low word. For example, stored density authored as
`0.3` is slightly greater than three tenths, so its spacing is `99`, not `100`.
Stored duration authored as `0.7` produces a product just below `21`, so its
integer duration is `20`. Rounding either arithmetic result to single
precision would incorrectly produce the larger integer. A duration of
`134217728` seconds yields the signed low-word result `-268435456`.

**Established fact:** Shower resolution tolerates bad data. An **empty**
`MeteorWeapon` name disables scheduling —
and still loads the `meteor.tdf` `[Default]` block over the parameters. A
non-empty name is read together with the four numeric keys; if **any one** of
radius, density, duration or interval is zero, the loader overwrites **all five
fields — the weapon name and all four numbers — from the `[Default]` block**,
and then enables scheduling. A single zero replaces the whole block including
the weapon name, so a mission authoring a custom weapon, radius and duration
but leaving the interval at zero gets the default weapon and the default radius
too `[R-WPN-01 §5]`. **Established (direct static trace):** a missing default
file or section returns without changing the incoming parameter block or
emitting the bogus-data diagnostic. When the section exists but its weapon
key is absent, the string accessor writes an empty weapon name; the loader
emits `Hey, hoser!  The default meteor shower data was bogus!` and terminates
with failure before reading the numeric fields. A present weapon key, including an empty
value, admits all four numeric reads and stores; any zero numeric value then
emits that same diagnostic and terminates with failure. The partial writes
precede process termination; they are not installed as a recoverable storm.
An empty present weapon value alone does not trigger the diagnostic. The enable bit was
selected from the original mission weapon's emptiness before default loading,
and the later parameter installation does not recompute that bit. An unresolved weapon name, or a
resolved weapon lacking the meteor flag, still falls back to weapon index zero
instead of disabling.

**Unknown — empty source weapon with absent defaults.** The empty-name branch
skips the four numeric schema reads before calling the default loader. If the
file or section is absent, installation can therefore consume numeric values
that this path did not initialize. Their concrete values and resulting disabled
scheduler timing have not been established; the decider is a complete lifetime
trace of those incoming numeric values through battle entry. A checked host
may use explicit zero initialization for this unsafe case, but must not describe
those zeros, or otherwise ignored numeric schema keys, as a retail default.
A synthetic session with no mission uses the same explicit host initialization.

**Established fact:** "Weapon index zero" is weapon record 0 of the ID-indexed
table. The weapon name is resolved once,
by the storm reset that runs when a battle starts (the same routine clears the
active flag and sets the first storm-start deadline to the per-hit spacing),
through the ordinary case-insensitive name scan of the 256-record table. A miss,
or a hit whose definition lacks the `meteor` flag, replaces the pointer with the
record at **slot 0** — the record whose authored `ID` is `0`, which in the
stock corpus is `[noweapon]`, the inactive sentinel of `[R-DMG-01 §5]`; when no
definition authors `ID=0` it is the zero-filled slot the catalog loader
pre-formats. There is no "smallest ID" or "first loaded" rule: the table is
addressed by the authored `ID` key, so a catalog with no `ID=0` record has an
empty slot there, not a renumbered one. The scheduler never re-tests the
pointer; every hit attempt spawns through the meteor creator with that
definition. **Supported inference (consequence for stock content):** a record
carrying `[noweapon]` has no motion-family flag, so under the dispatch of §6.2
(rule 6) it neither moves, collides nor expires; each such spawn permanently
occupies a pool record until the pool's 300-record cap starves every weapon.
*Decider:* a manual retail run of a mission authoring a misspelled
`MeteorWeapon`, watching the projectile count.

**Established fact:** The scheduler runs once per tick, **after** the projectile
phase, so scheduler-created meteors first move on the next tick while unit-fired
roots created in the unit phase can move in their creation tick. Its two blocks
are:

```
if (currentTick >= nextStormStart) {
    active         = 1
    stormEnd       = currentTick + duration
    nextStormStart = stormEnd + interval
    nextHit        = currentTick
    targetZ = (crtRand() * mapDepthCells)  / 0x8000        ; draw 1
    targetX = (crtRand() * mapWidthCells)  / 0x8000        ; draw 2
    originZ = targetZ + (crtRand() * 10) / 0x8000 - 15     ; draw 3
    originX = targetX + (crtRand() * 30) / 0x8000 - 15     ; draw 4
    if (!enabled) active = 0
}
if (active && currentTick >= nextHit) {
    nextHit   = currentTick + spacing
    velocityY = -0xF0000                                   ; -15 world units/tick
    velocityX = ((targetX - originX) << 20) / 90           ; cells to 16.16 over 90 ticks
    velocityZ = ((targetZ - originZ) << 20) / 90
    r     = (crtRand() * radius)  / 0x8000                 ; draw 5
    theta = (crtRand() * 0x10000) / 0x8000                 ; draw 6, i.e. crtRand()*2
    start.X = (originX << 20) - sin(theta, r << 16)
    start.Y = -velocityY * 90                              ; = 1350 world units
    start.Z = (originZ << 20) - cos(theta, r << 16)
    spawn the meteor with (start, velocity)
}
if (currentTick >= stormEnd) active = 0
```

All divisions are signed and truncate. `<< 20` is the cell-to-16.16 conversion
(16 world units per cell). The spawn height of **1350 world units is not a
literal**: it is `15 × 90`, the descent speed times the fixed 90-tick flight,
which is why vertical arrival coincides exactly with the horizontal
interpolation and impact lands exactly 90 ticks after spawn. Per-hit spacing of
`trunc(30 / density)` collapses to zero at a density of 31 or more, attempting a
spawn on every storm tick. The origin Z offset spans −15..−6, so the origin is
**always 6 to 15 cells north of the target**; the origin X offset spans
−15..+14.

**Established fact:** Every meteor geometry draw comes from the C-runtime random
stream — `state = state × 214013 + 2531011`, returning bits 16..30, held in
thread-local storage — and **not** from the simulation stream. The census is
**four draws each time the storm-start deadline is reached** — consumed even
when meteors are disabled, because the enable flag is only tested at the end of
that block — and **two draws per hit attempt**, including a hit attempt that
then fails on a full pool. Meteors consume zero simulation-stream draws
`[R-WPN-01 §6]`.

**Established fact:** A pool-full spawn silently drops the individual meteor: the
strike timer has already advanced and there is no retry. Spawn-side common
initialization takes the null-shooter path, giving meteors the neutral side
byte, so meteor explosions credit nobody.

### 6.6 Vertical launch and two-phase behavior

**Established fact:** The vertical-launch creator sets yaw to zero, pitch to
`0x4000` (a quarter turn, straight up), all three velocity components to zero,
and the scalar speed by the same `startvelocity` / `weaponacceleration` /
`weaponvelocity` hierarchy as the ordinary creator. It uses the same
timer-versus-range expiry choice as §6.3, retains the unit target and, when the
interceptor rescan supplied one, the matched-projectile link. It copies the
authored burst count and clears the slot's Aim receiver.

**Established fact:** There is no hard-coded eight-tick vertical-launch delay. A
vertical two-phase projectile can remain at the launch point because its
velocity vector starts at zero and guidance is phase-gated until its first
expiry transition; with zero velocity, zero acceleration and no guidance it
never moves at all. Authored expiry and `flighttime` values control the handoff.

**Established fact:** At or after self-propelled expiry the tick behaves as
follows. With `burnblow`, central impact is invoked and gravity and the
phase-transition work are skipped. Without `burnblow`, gravity is subtracted
from the vertical velocity and then, **if `twophase` is authored and the
two-phase state bits are still zero**, that tick also sets
`expiry = currentTick + (uint16)flighttime`, writes the two-phase state as
`((state & 0xf0) + 0x10) & 0x30` — which from zero produces the first phase
state and does not toggle the rest of the mask — and, **when `tracks` is not
authored**, clears both the projectile link and the retained unit target. In
either case the visible control flow then adds the existing velocity to the
position and runs collision, even after a burn-blow impact normally set the dead
bit. After this one transition, a later expiry without burn-blow continues
gravity and collision rather than performing a second transition or
automatically retiring.

### 6.7 Self-propelled acceleration and guidance

**Established fact:** A self-propelled record's tick, while `currentTick <
expiry`, is exactly:

```
eligible = !waterweapon || (preMotionYWord < seaLevelByte)
if (eligible) {
    if (scalarSpeed < weaponvelocity) {                 ; unsigned
        scalarSpeed += weaponacceleration
        if (scalarSpeed > weaponvelocity) scalarSpeed = weaponvelocity
    }
    guiding = twophase ? (twoPhaseStateBits != 0) : guidance
    if (guiding) {
        p = guidance target point (§6.8)
        if (!steer(record, p)) central impact
    }
    velocityY = sin(pitch, scalarSpeed)
    H         = cos(pitch, scalarSpeed)
    velocityX = -sin(yaw, H)
    velocityZ = -cos(yaw, H)
} else {
    velocityY -= gravity
    pitch      = 0
}
point += velocity
run current-point collision
```

`preMotionYWord` is the record's world Y high word **sampled before this tick's
motion**, and the medium test is strictly below the sea-level byte. Acceleration
is independent of guidance and is skipped entirely once the scalar speed reaches
`weaponvelocity`; the clamp is a plain unsigned overshoot clamp, not a
saturating add. Velocity is fully rebuilt from scalar speed and angles on every
eligible tick, so a self-propelled projectile's velocity magnitude is never
history-dependent.

**Established fact (malformed signs in the acceleration block).** Both
comparisons in the block are **unsigned 32-bit**
compares of the scalar speed word against `weaponvelocity`; the addition is an
ordinary wrapping 32-bit add. Three consequences follow, none reachable from
stock data:

* a **negative scalar speed** (a negatively authored `startvelocity`) reads as
  a value above every positive `weaponvelocity`, so the block is skipped every
  tick: the speed is never accelerated, and the velocity rebuild multiplies the
  trig entries by the negative magnitude, sending the projectile backwards
  along its aim for its whole life;
* a **negative `weaponacceleration`** decrements the speed each tick while it
  stays non-negative; on the tick the sum would cross below zero the wrapped
  value exceeds `weaponvelocity`, the overshoot clamp snaps it **up to
  `weaponvelocity`**, and from then on the gate reads equal and the block is
  skipped — a slow-down, a jump to full speed, then constant speed;
* there is no signed timer anywhere in this path: `weapontimer` is a 16-bit
  unsigned store (§7.3) and the `currentTick < expiry` gate is unsigned, so a
  "negative timer" is a wrapped large positive one.

**Established fact:** A non-water weapon is always propulsion-eligible. A water
weapon at or above sea level skips acceleration and guidance, falls under
gravity, and has its pitch forced to zero — so it levels out and sinks rather
than continuing to climb.

**Established fact:** Non-two-phase guidance requires the `guidance` flag.
Two-phase guidance becomes active only after the two-phase state bits become
nonzero, i.e. after the first expiry transition of §6.6. It then runs every
eligible tick. There is no projectile-side acquisition scan and no random
retargeting.

**Established fact:** The guidance target-point helper distinguishes three
sources, in this order, for a non-cruise weapon: the projectile link, when set,
supplies the linked record's current point; otherwise the retained unit target,
when set **and while that unit's live flag is set**, supplies the unit's world
point; otherwise the record's stored target point is used. No liveness
generation counter exists, so a link into a compacted-away record is read
without validation (§5.2).

**Established fact:** Guidance is pure pursuit of the selected point. It does not
add target-velocity lead during flight; the only lead in the whole weapon
pipeline is the pre-fire lead of §3.3, which `cruise` suppresses.

**Established fact:** Steering computes the wanted angles from the record's
current point to the selected point using the same two expressions as the direct
aim solver (§3.3), then processes **yaw first, then pitch**, each as:

```
e = (int16)(wanted - current)
m = |e|                                   ; 16-bit absolute value
if (m > 27000 && burnblow)  return failure
if ((int32)m < (int32)(uint16)turnrate)  current = wanted
else                                     current += (e < 0 ? -turnrate : +turnrate)
```

The snap test is **strict**, so an error exactly equal to `turnrate` takes the
step branch; the step is exactly one `turnrate`, never a fraction. `turnrate` is
zero-extended from its 16-bit store, so a negatively authored turn rate becomes
a very large unsigned bound and every tick snaps straight to the wanted angle.
The 27,000-unit failure test is strict and applies only with `burnblow`; because
yaw is fully processed first, a pitch failure occurs with the yaw already
updated. The caller invokes central impact on failure and then continues through
the visible velocity/motion code; normal impact retirement controls later work.

### 6.8 Cruise target points and target loss

**Established fact:** The `cruise` flag, not `commandfire`, selects the cruise
waypoint helper. Cruise ignores the projectile-link and retained-unit sources
entirely and works from the stored target point.

**Established fact:** The cruise helper computes

```
dX = current.X - storedTarget.X                 ; raw signed 32-bit 16.16 deltas
dY = current.Y - storedTarget.Y
dZ = current.Z - storedTarget.Z
d  = trunc(sqrt((dX*dX + dY*dY) + dZ*dZ))       ; x87 extended throughout, in that
                                                ; association order, then truncated
if ((int16)(d >> 16) > 1024) {                  ; SIGNED short, strict greater-than
    second point = (storedTarget.X, *, storedTarget.Z) with its Y high word
                   written as 700 (i.e. Y = 700 world units)
    steer toward the second point
} else {
    second point = storedTarget, with Y replaced by
                   max(terrainHeight(storedTarget), seaLevel) << 16
    steer toward the second point
}
```

The threshold is therefore 1,024 **whole world units** (64 cells) of
three-dimensional distance, and the cruise altitude above the threshold is a
fixed 700 world units of absolute world Y — not a height above terrain. A
distance whose high word reaches 32,768 whole world units (2,048 cells) wraps
negative and inverts the branch; that is unreachable for in-bounds map geometry.

**Established fact:** A lost non-cruise unit target falls back to the stored
target point. It does not autonomously reacquire another unit. The exact
stale-reference behavior when slots are reused remains a separate compatibility
issue (§5.2).

### 6.9 Water weapons and torpedoes

**Established fact:** No separate torpedo creator, record type, or motion loop
was found. Torpedo-like behavior is the shared self-propelled family combined
with the `waterweapon`, `guidance` and collision flags. `waterweapon` is a
single flag read at exactly four decision points, and each one is a different
predicate:

1. **Acquisition admission** (§3.1) swaps the whole non-water branch for two
   candidate-medium predicates: reject a candidate that lacks `floater` and
   whose height word is strictly above the sea-level byte, and reject a
   candidate that has `canhover` and whose height word plus **half** its
   definition's reference height is strictly above the sea-level byte. The
   shooter's own height, the to-air class test and the ballistic feasibility
   test are all skipped, and the planar range test is unchanged. Neither
   predicate is a depth band; both are one-sided.
2. **Shot-time admission** (§3.3) is the mirror image: the planar range test
   runs first for every weapon, and a water weapon then passes immediately,
   while a non-water weapon must have its own reference height strictly above
   sea level (and, when ballistic, a valid solution).
3. **Self-propelled motion** (§6.7) gates propulsion and guidance on
   `!waterweapon || preMotionYword < seaLevelByte` — a strict compare of the
   height word saved **before** this tick's displacement. A water projectile at
   or above the plane is treated exactly like an expired one: its vertical
   velocity takes gravity and its pitch is forced to zero, so a torpedo that
   breaches decelerates and falls rather than steering.
4. **Collision** (§8.2) lets a water weapon **return and continue** at or above
   terrain instead of testing the sea plane, so it never self-destructs on
   entering water; it still impacts on terrain penetration, unit slots,
   features and proximity like any other family.

The asymmetry is deliberate and must be reproduced: `waterweapon` is not a
universal medium permission — it restricts target choice at acquisition while
relaxing the shot-time and water-plane gates — and nothing in the four sites
tests the *projectile's* own medium against the target's.

### 6.10 Beams, lightning, flame, and feature fire

**Established fact:** Beam behavior exists only inside the direct
(`lineofsight`) motion branch, and it is two lines of arithmetic on a record
that is otherwise an ordinary direct projectile. While the record is live
(`currentTick < expiry`) the head point advances by the velocity every tick as
usual, and then:

```
if (beamweapon) {
    if (latched)                          secondPoint += velocity
    else if (creationTick + duration < currentTick)  latched = true
}
```

The latch lives in bit 0 of the record's state byte and is cleared by the
common initializer. `duration` is the authored `duration` key already truncated
to whole ticks by the catalog (§2.1), the comparison is **strict**, and the
tick that sets the latch does **not** move the tail — so the tail starts moving
on the tick after the latch. The visible beam therefore grows from zero length
for `duration + 1` ticks and then translates rigidly at constant length: with
`duration = 0` the latch is set on the first tick after creation, and a
`duration` authored below one thirtieth of a second compiles to zero and
behaves the same way. Nothing shortens the beam and nothing re-clears the
latch.

**Established fact:** Collision samples only the moving head (§8.1). It does
not receive the tail or previous point, so the visible beam segment is not a
line-area collision volume. Damage uses the ordinary impact amount with no
duration divisor — a beam deals its full authored damage on every contact tick,
not damage-per-second. Expiry only retires; it does not deliver beam damage.
Normal impact usually ends future contacts, while `noexplode` can leave the
beam live for later repeated contacts.

**Established fact:** The jagged lightning render type constructs randomized
line segments between head and tail only during rendering. Render type is not
consumed by simulation or collision. No lightning chaining or widened lightning
collision was found, and the two points the renderer joins are exactly the head
and the tail this section maintains.

**Established fact:** No flame-specific projectile integrator was found.
Burn-blow controls selected steering-failure and expiry outcomes. End-smoke
changes impact presentation without suppressing damage.

**Established fact (no combat producer on the presentation flame strip, [03 R-LAYER §4]):** the renderer's strip-5 "flame" objects are spawned solely by the Teleport order-state handler as the teleport visual, and the strip-5 burning-feature smoke by the feature-fire walker. No projectile impact class, fire-damage application, or building burning state produces a strip-5 flame event, so flame render types, `firestarter`, and feature fire have no producer on that strip. The authored-relationship unknowns below are unaffected.

**Established fact:** Persistent feature fire is a separate post-damage record
system, not a projectile family, and a weapon reaches it only through the
feature-damage accumulator of §13.1: ignition requires the global feature
option bit, a flammable feature definition, a nonzero weapon `firestarter`, and
a cell with no live instance attached. Active fire records animate, emit smoke,
expire, and can spread to eligible nearby or wind-selected feature cells using
simulation RNG. This is feature-fire spread, not beam or lightning chaining.

## 7. Projectile timers and motion details

### 7.1 Per-record tick order

**Established fact:** The projectile phase runs once per simulation tick, after
the unit phase and before the meteor scheduler `[01 §4.4]`. It walks the pool in
ascending index order over the active count captured once at phase entry.
Each visit consumes one position in that captured span; appending a projectile
does not extend the current pass. The phase finishes by running the compaction
pass of §5.2 exactly once over the then-current count, including new records.
A record whose remaining burst count is nonzero takes the
burst-expansion path of §4.3 *instead of* motion; only a record with a zero
remaining burst count is on the ordinary motion path.

**Established fact:** Within one ordinary record the order is fixed, and an
implementation must reproduce it:

1. advance the visual propeller angle by 1,024 units when `propeller` is
   authored (§6.1);
2. perform the family-specific velocity, heading, and lifetime work in the
   dispatch order of §6.2;
3. add velocity to position, for the families that reach movement at all;
4. run the collision test;
5. if the record's dead bit is still clear, schedule trail smoke and then test
   the downward water crossing (§7.3).

Steps 3 and 4 are inside step 2 for every family — the families that retire
instead of integrating (direct at expiry, ballistic at a non-burn-blow expiry)
skip both — so an implementation that hoists the position update out of the
family switch will move a retiring projectile that retail leaves in place.

### 7.2 Fixed-point integration

**Established fact:** Projectile positions and velocities are integer 16.16
world units, and integration is family-specific rather than universal:

| family | per tick |
|---|---|
| direct (`lineofsight`) | `point += velocity`; beam second point advances too once latched |
| meteor | `point += velocity`; two visual accumulators advance from the velocity high halves |
| ballistic, dropped | `point += velocity`; then `point += (windX, windY, windZ)`; then `velocityY -= gravity` |
| self-propelled, eligible | rebuild all three velocity components from scalar speed, yaw and pitch; then `point += velocity` |
| self-propelled, at medium or expiry | `velocityY -= gravity` (and `pitch = 0` at the medium gate); then `point += velocity` |

The three wind values are global per-tick world-unit offsets applied to the
**position**, not to the velocity, and the vertical one is applied along with
the other two. Gravity is a single global subtracted from the vertical velocity.

**Established fact:** Yaw and pitch are `uint16` angles with 65,536 per circle.
Turn-rate slew is the exact snap-or-step arithmetic given in §6.7: snap when the
absolute signed 16-bit error is **strictly less** than the zero-extended
`turnrate`, otherwise step by exactly `turnrate` toward the wanted angle, yaw
resolved before pitch. The angle-to-velocity conversion is the 512-entry scaled
sine/cosine pair of §3.3, with its 128-unit quantization and its
`(entry × magnitude + 4096) >> 13` rounding; nothing in the projectile path uses
floating-point trigonometry.

### 7.3 Expiry and smoke

**Established fact:** Root expiry is one of two expressions, chosen at creation:
`currentTick + weapontimer` when `weaponvelocity` is zero or `noautorange` is
authored, and `currentTick + (uint32)(range << 16) / weaponvelocity` otherwise
(§6.3). Burn-blow ballistic roots use the horizontal-distance-over-horizontal-
speed deadline of §6.4 instead. Expiry is not a universal death rule: direct
records retire at expiry without moving or colliding; timed ballistic records
either impact through burn-blow or emit a puff and retire without damage;
self-propelled records can impact, transition phase, or continue under gravity;
and dropped and meteor motion never consult expiry at all.

**Established fact:** The trail-smoke cadence is a separate additive deadline
evaluated **after** collision, on a record whose dead bit is still clear:

```
if (smoketrail && currentTick < expiry && smokeDeadline < currentTick) {
    emit the trail puff at the current point
    smokeDeadline += smokedelay
}
```

All three tests are required, the deadline test is **strict**, and the deadline
advances **additively** by `smokedelay` rather than being re-anchored to the
current tick — so a projectile that was blocked from emitting catches up one
puff per tick until the deadline overtakes the clock. The common initializer
seeds the deadline to the creation tick, so the first puff is due on the first
tick after creation. A `smokedelay` of zero makes the deadline permanently
overdue and emits one puff on every live tick.

**Established fact:** `startsmoke` emits one puff at the muzzle point at
creation, as the last act of the ordinary, ballistic and vertical-launch
creators, after the Fire and RockUnit callbacks (§4.1). It uses the same pooled
effect object as the trail puff with a different init argument pair, and the
effect list is capped: a new effect beyond 400 live entries retires the oldest
entry rather than failing. `endsmoke` is read only by the central impact path
and changes impact presentation without suppressing damage; §8 and §13 own it.
The expiry puff a non-burn-blow ballistic record emits at its deadline is the
same emitter as the trail puff.

**Established fact:** The downward water-crossing test runs last, after trail
smoke, and only while the dead bit is clear:

```
if (preMotionYWord > seaLevelByte && postMotionYWord <= seaLevelByte) { ... }
```

— the pre-motion Y high word strictly above the sea-level byte and the
post-motion one at or below it. On a crossing it queries the map cell, and
**spawns the weapon's water-explosion art** (the water/lava holder of
`[R-WFX-01 §1]`, with calculated flash table 0 and the water flag set, so no
land dust puff) when the cell is found, its own height byte is below sea
level, and the session's opaque-liquid mode is zero. No sound is played on a
crossing. Smoke emission is never a collision condition.

The crossing site calls the explosion-art allocator with the weapon's
water/lava art holder, not the sound emitter; `soundwater` is played only by
the central impact's water arm (§13.2) `[R-WFX-01 §2]`.

**Established fact:** `flighttime`, `holdtime`, `burstrate`, `duration`,
`smokedelay`, `randomdecay` and `weapontimer` are consumed as raw logical tick
counts after catalog truncation; they are not multiplied by 30 again at
projectile tick time. Of these, `holdtime` has **no projectile reader at all**:
it is read at exactly five sites — the direct-expiry retirement, the two central
impact retirement paths, the collision retirement path, and the shooter-death
anchor sweep — each of which, when the retiring record is the followed
projectile, freezes the camera's target at that record's last point and loads
`holdtime` into the follow-camera hold counter. The camera update then
decrements that counter once per update while it is nonzero and centres on the
frozen point, resuming normal following at zero. `holdtime` is therefore a
**camera** parameter measured in ticks `[R-WPN-01 §8]`; doc 07 owns the camera
side.

**Established fact:** Catalog float conversions truncate toward zero, so a
negative authored value truncates toward zero rather than flooring; a 16-bit
store then wraps the truncated value modulo 65,536. Enumerated wrap edges: the
remaining burst count is a 16-bit word, so an authored 65,535 makes a burst
anchor attempt one clone per tick until the pool starves; burst deadlines and
expiry wrap modulo 2^32 with the unsigned comparisons tested after wrap; a zero
burst interval makes the first attempt due in the creation tick; the clone
expiry division by scalar speed (when the weapon timer is zero) raises the
divide exception on zero speed; a negatively authored `turnrate` zero-extends to
a bound that always snaps (§6.7); and a `randomdecay` or `sprayangle` of exactly
1 consumes no randomness because the shared generator does not advance below a
bound of two. Stock weapons never author these edges.

**Unknown:** All remaining integer overflow behavior is not fully closed. Retail
lacks several defensive guards.

#### `burstrate`, `duration` and `smokedelay` are read zero-extended, and every consumer's compare is unsigned [R-WPN-05 §12]

[02 R-KEYS-01 §6] owns the store side: all three keys are sixteen-bit words
holding `trunc(authored × 30)`. This is the reader side, one consumer at a
time, so the arithmetic of §4.3, §6.10 and §7.3 can be implemented at the right
width. All three keys have exactly the readers named here and no other (bounded
negative over the whole image).

**Established — `burstrate`, three loads, all in the burst scheduler of §4.3.**

1. The due test is `creationTick + zx(burstrate) <= currentTick`, where `zx`
   is zero-extension of the sixteen-bit word to thirty-two bits, the
   comparison is **unsigned** on the thirty-two-bit sum (§4.3), and the addend
   is `0..65535`.
2. The muzzle-refresh gate "`burstrate` strictly greater than four" compares
   the sixteen-bit word **unsigned** (`>= 5` on the word), so a wrapped word
   such as `65506` (`burstrate=-1`) refreshes the position on every pellet.
3. The deadline advance adds `zx(burstrate)` to the creation-tick field.

**Established — `duration`, one load, the beam latch of §6.10.** The latch
test is `creationTick + zx(duration) < currentTick` with an **unsigned**
thirty-two-bit comparison, strict as §6.10 states.

**Established — `smokedelay`, one load, the trail-smoke deadline of §7.3.**
`smokeDeadline += zx(smokedelay)`; the deadline is a thirty-two-bit field and
the word is added without sign.

**Consequences.** There is no negative interval anywhere in this family: an
authored negative value becomes a large positive count. `burstrate=-1`
(`65506` ticks, about 36 minutes) parks the burst root at the muzzle far
longer than any projectile lives, so the burst never fires a second pellet in
practice; `duration=-1` never latches the beam; `smokedelay=-1` emits the
first trail puff and then none. None of this is authored by stock content
(asset census: every value of the nine tick keys lies in `0..32767/30`
seconds), so the widening is observable only on third-party weapons. The
implementation rule is the one `weapontimer`, `randomdecay` and `flighttime`
already follow: compile as `uint16(trunc(authored × 30))`, widen without sign,
compare unsigned.

## 8. Collision and impact selection

### 8.1 Collision gate

Units and terms used throughout §§8–9: positions are 16.16 (one world unit =
65,536); one plot cell is 16 world units; the terrain, sea-level and feature
height values named here are **unsigned bytes in whole world units**, and the
gate compares them against the **high word** of the projectile's 16.16 vertical
position, so all height tests below are whole-world-unit tests. The plot cell's
byte layout — corner height, neighbourhood maximum, neighbourhood minimum,
metal, feature word, fringe-anchor deltas, flag byte — is owned by
`[03 §2.2]`; this section names those fields but does not
redefine them.

**Established fact:** The resolver takes the projectile's **post-motion**
current point and maps it to exactly one plot cell:

```
cellX = point.X >> 20            ; arithmetic shift of the 16.16 X: /65,536 then /16
cellZ = point.Z >> 20
in-map iff 0 <= cellX < mapWidth and 0 <= cellZ < mapHeight
```

No previous point, beam tail, velocity interval, or segment fraction is
supplied or reconstructed. There is no swept segment, ray, broad candidate
collection, or nearest-contact sort. The shift is arithmetic, so a negative
coordinate floors rather than truncating, but the signed lower-bound test then
rejects it, so the floor/truncate difference is unobservable here (it is *not*
unobservable in area damage, §9.3, which quantizes differently).

**Established fact:** Sufficiently fast projectiles can therefore pass over
intervening cells or thin contact geometry between sampled points. The separate
water-crossing presentation predicate can notice an above-to-at-or-below plane
crossing, but it does not deliver collision or damage.

**Established fact:** An off-map point freezes the follow camera on the
record's last point, loads `holdtime` into the camera hold counter (§7.3),
marks the projectile dead, and returns. It does not call impact, damage,
terrain effects, or splash. This retirement ignores the no-explode flag; the
flag cannot preserve the record.

**Established fact:** For an in-map point the resolver runs this fixed ladder,
and an implementation must reproduce both the order and the early returns:

1. **Projectile-link proximity.** When the record carries a
   projectile-to-projectile link, the metric is the sum over X, Y and Z of the
   **high 32 bits of the 64-bit square** of each signed 32-bit 16.16 delta
   between the two current points — that is, each term is the squared distance
   in whole world units, truncated per axis. Contact is
   `metric < areaofeffect × areaofeffect` (signed 32-bit compare, **strict**,
   the authored area value unhalved). It calls the central impact path with no
   direct unit and **does not return afterwards**, and the resolver NEVER
   rechecks the dead bit, so a second same-call impact — unit slot, feature,
   terrain, or water — is reachable WITHOUT no-explode; the flag additionally
   keeps the record available on future ticks.
2. **Cached floor value.** The record's cached floor scratch is overwritten
   with `(cell.maxHeight + cell.minHeight) / 2` (unsigned division of two
   bytes). It is **not** used by any later test in this ladder: the projectile
   draw pass is its only reader, which subtracts half of it from the screen
   position, so the scratch is presentation-only `[R-WPN-02 §7]`.
3. **Unit slot zero.** Requires a nonzero cell occupant, an owning-player byte
   different from the projectile's side byte, and
   `point.Y < unit.Y + definition.boundsMaxY` — a strict compare of full 16.16
   values, with **no lower bound**.
4. **Unit slot one.** Same nonzero/different-side gates, and
   `unit.Y + definition.boundsMinY <= point.Y <= unit.Y + definition.boundsMaxY`
   — **both ends inclusive**.
   The first successful slot impacts that unit and returns. This fixed slot
   order is the visible unit tie policy; this function does not consult the
   alliance matrix.
5. **Units-only early return.**
6. **Feature or footprint-anchor resolution** with repeated-cell suppression.
7. **Terrain penetration and bounce** (§8.2).
8. **Water/sea continuation or impact** (§8.2).

**Established fact:** The two cell slots are class-assigned by the occupancy
stamper, not by insertion order: ground-class units (movement mode 1) write
cell slot zero, flying-class units (movement mode 2) write cell slot one,
mode-zero records write nothing, and the yard-map branch for feature-unit
records writes slot zero with a water-state-dependent mask. An occupied slot
is overwritten by the later stamper and both units receive mutual-block status
bits. This is the semantic reason for the height-gate asymmetry: slot zero
holds ground occupants whose vertical extent runs base-to-top (no lower gate),
slot one holds the flying class in an altitude band (inclusive lower and upper
gates).

**Established fact:** Units-only returns after both unit slots miss. It
suppresses feature, terrain, bounce, and water-plane impact handling, not only
feature damage.

**Established fact:** Feature resolution reads the cell's feature word `f`:

* `f < 0xFFFB` resolves directly, and only when `f` is also below the live
  feature count; otherwise no feature.
* `f == 0xFFFE` (fringe) resolves through the anchor cell reached by stepping
  back `cell.anchorDeltaZ` rows and `cell.anchorDeltaX` columns — the same
  signed deltas `[03 §2.2]` defines — and takes that cell's
  feature word under the same `< 0xFFFB` test.
* every other value resolves to no feature.

A feature hit requires
`(int16)point.Yword < featureDefinition.heightByte + cell.minHeightByte`
(strict, unsigned bytes summed as ints). The repeated-cell suppression compares
the record's cached cell pair against
`(trunc(point.Xword / 16), trunc(point.Zword / 16))`, each computed as
`(v + (v >> 31 & 0xF)) >> 4` — a **truncate-toward-zero** divide by 16 of the
signed high word, not the arithmetic shift used for the cell lookup above. When
both match, this feature's impact is cancelled and the cache is left alone;
otherwise the cache is overwritten and impact is selected with no direct unit.
The cached-cell suppression cancels only that feature's impact: the
terrain/water ladder later in the SAME resolver call still runs and can select
another impact.

#### The contact test has no radius: the occupancy word is the XY gate and the model top is the vertical band [R-DMG-01 §7]

Which cells hold a unit's identity, where the definition's Y bounds come from,
and why no distance is computed anywhere in the contact path.

**Established — there is no radius.** The contact scan reads exactly one plot
cell, the one under the projectile's post-motion point (`X >> 20`, `Z >> 20`,
arithmetic), and tests its two occupancy words. Nothing in the resolver
computes a planar distance, a footprint rectangle, or a per-candidate radius,
and no candidate list is built: a unit is a candidate iff its pool index is
the value in one of the two words of that one cell. There is no retail radius
constant for an implementation to approximate.

**Established — which cells carry a unit's identity.** The words are written by
the occupancy stamper of `[04 R-COLL-01 §4]` and by nothing else, so the XY
gate is the stamped footprint rectangle:

```
S      = 1 << 19                                 ; half a cell, 8 world units, in 16.16
cellX  = (unit.X + S − footprintX·S) >> 20       ; arithmetic shift   [04 R-COLL-01 §1]
cellZ  = (unit.Z + S − footprintZ·S) >> 20
ground mover (mode 1):     ground word := self over [cellX, cellX+footprintX) × [cellZ, cellZ+footprintZ)
airborne mover (mode 2):   air word    := self over the same rectangle
building class:            ground word := self over the yard-selected cells of that rectangle
modes 0 and 3, and an airborne mover whose rectangle leaves the map: no cell
```

`footprintX`/`footprintZ` are the unit's copy of the definition's footprint
pair in cells (`[04 §6.3]`); the rectangle moves only when a commit changes
the cell pair, since the same-cell fast path leaves the words alone. A
projectile therefore contacts a ground unit when its point's cell lies inside
the unit's committed footprint rectangle — up to `footprintX × footprintZ`
cells, never a disc — and a flying unit when the cell lies inside the
rectangle the airborne mover stamped at its last cross-cell commit. Cells of
a building that its yard map leaves unselected (an open factory yard,
`[04 R-COLL-01 §3]`) hold no identity, so a shell passing over them misses
the building. Where two stamps overlap, the overlap protocol of
`[04 R-COLL-01 §4]` decides whose index the word keeps, and the contact test
sees only the survivor.

**Established — the vertical band's source.** The `boundsMinY`/`boundsMaxY` of
steps 3 and 4 are two 16.16 dwords of the unit definition that the
unit-definition catalog loader writes after the FBI parse and the model load:

```
boundsMinY = 0                                   ; unconditionally
boundsMaxY = modelTop(<objectname>.3do)          ; 16.16, the model-top walk below
spanY      = boundsMaxY − boundsMinY
```

`modelTop` walks the model's piece tree — each piece and its siblings,
recursing into children — taking the maximum over every vertex of
`vertex.Y + (the Y translations of the piece and of each ancestor)`, from an
accumulator that starts at zero; the result is the highest vertex above the
model origin in the model's own 16.16 units (`[03 §2.4]`, `[fmt 3do]`),
floored at zero. (The FBI parser separately writes the X/Z bounds as
`±footprint × 8` world units — half the footprint each side of the origin,
in 16.16 — and pre-computes the spans; the loader then overwrites the Y pair
and recomputes only the Y span. Those X/Z bounds feed the visibility probe of
§3.1, the Teleport box and the nano-box builders, not the contact test.) The
build already computes this exact walk as the model-top helper it uses for
the visibility eye height; the contact test wants the **full 16.16 dword**,
not the whole-unit high word that the LOS builder reads.

Substituting, the two slot tests of steps 3 and 4 read:

```
slot 0 (ground word):  point.Y <  unit.Y + modelTop                  ; strict, no floor
slot 1 (air word):     unit.Y <= point.Y <= unit.Y + modelTop        ; both inclusive
```

all in 16.16, `unit.Y` the unit's current 16.16 height, signed 32-bit
compares. A model with no vertex above its origin has `modelTop = 0`, so its
ground-slot test admits only points strictly below the unit's base and its
air-slot band degenerates to `point.Y == unit.Y`; no stock model is built that
way, but the arithmetic is what it is.

**Established — order and tie policy.** Per
cell: the ground word first, the air word second; the first word whose unit
passes the owner-differs test and its band impacts and returns. The owner test
compares the unit's owner byte with the projectile's side byte — an allied
unit on the cell is a valid contact; only units of the shooter's own side are
exempt. Nothing prefers a nearer unit because no distance exists: the XY test
is membership of the projectile's cell in a stamped rectangle, the ground
slot's band has no floor and its ceiling is the model top, and the selection is
the fixed word order within one cell.

#### The collision cache's cell pair is never reset: reuse inherits the last occupant's pair [R-DMG-01 §13]

**Established** (direct static read of the collision gate's feature step; a
whole-image store census of the record's two cached-cell words; the reservation
and common-initializer field lists of every creator; the pool's one
allocation-time clear).

The feature step above compares the record's cached cell pair against the
current point's quantized cell and, when both match, cancels the feature's
impact without touching the cache. Nothing resets the pair. The only stores to the two cached-cell words anywhere in the
image are the two in the collision gate's feature step — written together,
only when a feature resolves for the cell **and** the height test
`(int16)point.Yword < featureDefinition.heightByte + cell.minHeightByte`
has passed, and only when the pair did not already match. The reservation
of every creator (ordinary, ballistic, vertical-launch, dropped, meteor and
the burst-clone path) writes exactly the dead bit and the retained unit
target, as §4.1 says; the common initializer writes the fields §4.1 lists
and nothing else; no specialized creator writes the pair. The pool itself
is zero-filled **once**, when the 300-record array is allocated at battle
start, and never again; compaction copies survivors downward and leaves the
tail records' bytes where they were (§5.2).

**Consequences, exactly.**

1. A slot used for the first time in a battle starts with pair `(0, 0)` —
   the north-west corner cell, which is in-map, so a feature there can be
   suppressed for a brand-new record on its first contact.
2. A reused slot starts with whatever pair the slot's last occupant wrote —
   its last feature-contact cell — or `(0, 0)` if no occupant ever
   contacted a feature. A shell fired at the same tree cell that the slot's
   previous occupant last hit has its first contact **suppressed**, and the
   terrain/water ladder of the same call runs instead.
3. The pair is a *last-feature-cell* memory, not a *last-tick* memory: it is
   not written on featureless cells or on cells whose feature fails the
   height test, so it survives any number of ticks and, per 2, any number of
   reuses.

An implementation retains the pair across common initialization and consults
it only inside a resolved, height-passing feature branch: comparing and
overwriting it on every in-map tick before looking for a feature reduces it to
a one-tick memory that can never suppress across a gap.

#### Where the gate writes the floor scratch: after the in-map test, before the unit slots [R-DMG-01 §14]

**Established** (direct static read of the collision gate).

Step 2 above states the value — `(cell.maxHeight + cell.minHeight) /
2`, an unsigned division of the plot cell's neighbourhood-maximum and
neighbourhood-minimum bytes — and that the projectile draw pass is its only
reader. Its write position matters to an implementation whose gate and
publisher are separate: the gate first resolves the post-motion
point's plot cell and, when there is none (off-map), freezes the follow
camera, marks the record dead and returns **without** writing the scratch;
then runs the projectile-link proximity step, which never returns; then
writes the scratch; then the two unit slots, the units-only return, the
feature, terrain and water steps. So the scratch is rewritten on every
in-map tick regardless of what the ladder selects, an off-map record
retires with its previous value, and a record the phase has not yet visited
— a burst clone appended after the iteration count was captured (§5.1) —
carries its slot's previous value into presentation, exactly as the draw pass
reads it. An implementation writes the cached floor height at that point and
its publisher copies the stored value into the committed view rather than
recomputing it.

### 8.2 Ground bounce and water

**Established fact:** Terrain contact is
`(int16)point.Yword < cell.minHeightByte` — the projectile's whole-world-unit
height strictly below the cell's **neighbourhood-minimum** height byte (not the
corner height and not the maximum). On contact, `groundbounce` replaces only
the vertical velocity with
`velocityY = -(velocityY >> 2)` (arithmetic shift of the signed 16.16 value,
then negation) and returns; the branch never reaches the central impact path at
all, so no-explode is irrelevant here. There is no position correction,
horizontal damping, authored restitution, bounce counter, retirement, or impact
effect in this branch. Without `groundbounce` the same contact selects impact
with no direct unit.

**Established fact:** Because position is not corrected, the projectile remains
at its already-integrated point and can collide or bounce again on a later
family tick. Units-only prevents the terrain/bounce branch from being reached.

**Established fact:** At or above terrain the ladder ends in three ordered
early returns and one impact:

```
if (waterweapon)                       return          ; continue flying
if (seaLevelByte <= (int16)point.Yword) return          ; at or above the plane
if (opaqueLiquidMode != 0)             return          ; OTA nosealeveltrigger
impact(no direct unit)                                 ; submerged impact
```

The sea-level compare is a signed comparison of the zero-extended sea-level
byte against the signed high word, so submerged impact needs the height
**strictly** below the plane.

**Established fact:** Downward water crossing is a live post-collision
presentation event, not part of this ladder. It requires saved pre-motion
height strictly above sea level, current height at or below sea level, a valid
cell whose neighbourhood-maximum height byte is below sea level, and the
opaque-liquid mode clear. It emits the weapon water art but does not damage,
change velocity, or retire the projectile (§7.3).

**Established fact:** A collision that marks the projectile dead suppresses
crossing splash because the post-collision tail is dead-gated. Bounce and
no-explode contacts can leave it live and therefore can still emit a crossing
splash. Smoke-trail scheduling precedes splash selection.

## 9. Impact, armor, damage, and area effects

### 9.1 Central damage pipeline

**Established fact:** Every impact — proximity, unit slot, feature, terrain,
water, burst-clone, death explosion, burn weapon and interceptor sweep — enters
one central impact routine, which runs this fixed order:

1. classify the impact cell: **water** iff the cell resolves and its
   neighbourhood-maximum height byte is below the sea-level byte;
2. unless `noexplode`, freeze the follow camera on the record's point, load
   `holdtime` into the camera hold counter, and set the record's dead bit;
3. if the opaque-liquid mode is nonzero **and** the cell is water **and** there
   is no direct unit, repeat the retirement and **return** — no shake, no
   sound, no art, no damage;
4. camera shake with the weapon's shake magnitude (passed twice) and shake
   duration;
5. sound, smoke and art selection (§13.2);
6. the damage gate and routing below.

**Established fact:** The damage gate is a property of the **projectile's own
side**, not of the victim: damage is skipped **only** when the player row
named by the record's side byte is occupied **and** its control byte is 3.
An unoccupied row — including the eleventh, never-occupied row that the
neutral side byte 10 selects — passes the gate. Type 3 is the remotely
simulated controller (`[08 "Lobby behavior"]` maps the lobby's
Open/Player/Computer rows onto the runtime controller types); the peer that
owns the shot resolves its damage and sends the packet. In single player only
types 1 and 2 occur, so the gate always passes — for real shooters and for
null-shooter records alike. The two-branch test, and the row count that makes
side 10 an ordinary lookup, are `[R-DMG-01 §9]`.

**Established fact:** Routing is a two-way choice, tested in this order:

```
if (areaofeffect <= 16 && directUnit != null) {
    amount = damageOne(record, directUnit, falloff = 1.0f)   ; §9.2
    if (record.shooter != null) { shooterFeedback(...); }    ; §9.4
    return                                                   ; even when shooter is null
}
areaDamage(record, point)                                    ; §9.3
```

The area value is compared **unsigned** and the shortcut needs a direct unit,
so an interceptor proximity hit, a terrain hit and a death explosion always
take the area path regardless of how small the authored area is. A null shooter
in the shortcut returns without falling through to area enumeration.

**Established fact:** The per-recipient amount is computed by one shared
routine (§9.2) and handed to the **packet builder**, which applies the
defender-side scales and emits a nine-byte packet:

```
byte 0      constant builder tag 0x0B
bytes 1-2   victim id, uint16, 0 when the victim pointer is null
bytes 3-4   attacker id, uint16, 0 when the attacker pointer is null
bytes 5-6   amount, int16 — the scaled amount truncated to 16 bits
byte 7      direction: the HIGH byte of the 16-bit angle
            (atan2q(record.X - victim.X, record.Z - victim.Z) - victim.heading)
byte 8      kind
```

There is NO generation token: a nonzero id converts directly to the unit base
by slot arithmetic with no liveness probe. Victim acceptance additionally
requires the alive status bit AND a clear death latch for every packet kind, so
a reused active slot accepts a delayed packet intended for an earlier occupant
of that slot. The ATTACKER receives no alive, type, or death-latch validation
of any kind. The builder is also the multiplayer publication point: after the
local dispatch, a victim whose controller type is 3 causes the same nine bytes
to be sent to the attacker's player (or to a default destination when the
attacker is null) for every kind except 11.

**Established fact:** On accepted non-heal damage the victim stores the raw
attacker pointer plus a snapshot of the attacker's side taken at damage time;
the packet kind is recorded before health mutation. The death handler later
reconstructs the attacker slot afresh from the serialized id — it does not
preserve the original pointer and performs no liveness or generation check.
Veterancy increments and reclaim-pulse fatal payments can therefore land on an
EMPTY slot's stale field or on a replacement unit; the CREDITED side is always
the earlier damage-time snapshot. A zero attacker id suppresses veterancy and
payment entirely; a nonzero id naming an empty slot is not rejected.

**Established fact:** The dispatcher's order is exact:

1. resolve victim and attacker from their ids by slot arithmetic; the victim
   pointer is dereferenced with no null test, so a packet carrying victim id 0
   faults;
2. reject unless the victim's alive bit is set and its death latch is clear;
3. **kind 10 (heal)** takes an early exit:
   `h = (int32)(int16)health + (uint16)amount; if ((uint32)maxHealth <= (uint32)h) h = maxHealth;`
   stored back as int16. The comparison is unsigned, healing never divides, and
   a zero maximum therefore clamps healed health to zero. No damage flash, no
   reaction, no attacker assignment, no callbacks;
4. otherwise set the damage flash; for every kind except 11 run
   reaction/wake/retarget; record the kind byte on the victim; when the
   attacker is nonzero store the attacker pointer and its side snapshot, and
   raise the "under attack" interface event when either side is the local side;
5. **kind 2 (paralyzer)** branches to §10 and never subtracts health;
6. otherwise `health = (int16)(health - (int16)amount)` — exact 16-bit modular
   subtraction. On a non-positive signed result: if the victim's player record
   exists and its controller type is 1 or 2, set the death latch, **preserve
   the modular health value** and return immediately; otherwise clamp health to
   zero and continue to the callbacks. (The test is the victim's **player**
   controller type, so the mobility, class and definition of the unit are
   irrelevant, and a unit owned by an absent or remote controller never dies
   through this path `[R-WPN-02 §2]`.);
7. **kind 1 only** emits, in order,
   `HitByWeapon(cos(a, 400), sin(a, 400))` with `a = packet.direction << 8` —
   the two scaled-trigonometry helpers of §3.3 at magnitude 400, cosine first —
   and then `TakeDamage(percent)` with
   `percent = clamp((uint32)((int16)health × 100) / (uint32)maxHealth, 0, 100)`,
   a signed 16-to-32 multiply followed by an **unsigned** divide and signed
   clamps. Kind 11 subtracts health but emits neither callback. The division is
   unguarded: a maximum health of zero raises the divide exception. Stock
   definitions never author zero; the unguarded division belongs to the
   malformed-state error policy (TODO(T25)).

**Established fact:** Lethal damage marks the unit for death immediately, so a
later projectile in the same phase observes the death mark and applies no
further damage. Because the unit/death sweep precedes projectile advancement in
a master tick, a projectile lethal is normally consumed by the death handler on
the next master tick. Kill credit is therefore unavailable to later projectiles
in the current phase and cannot change one area traversal partway through its
recipients.

#### What "reaction/wake/retarget" is: the observer notice, the retaliation site, the damage flash, the under-attack notice, and the attacker reference [R-WPN-04 §2]

§9.1 step 4 names five things: "set the damage flash; for every kind except 11
run reaction/wake/retarget; record the kind byte on the victim; when the
attacker is nonzero store the attacker pointer and its side snapshot, and raise
the "under attack" interface event". Each is stated below; the retaliation
arithmetic itself is owned by `[08 R-AI-01 §11]` and `[04 R-STANCE-01 §3]` and
is only cited.

**Established — the damage flash is a 240-tick minimap blink.** The flash is
one byte of the unit record written to 240 by every packet the dispatcher
accepts other than a heal (paralyze included), *before* the reaction step. The
per-unit tick refresh decrements the byte by one while it is nonzero, so it
reaches zero after 240 visits — eight seconds — re-armed to 240 by every hit
`[R-WPN-04 §4]`. Its only reader is the minimap unit-dot pass, which gates the
blip on `blinkSuppressByte == 0 || blinkPhase` ([03 §3.9]), so a nonzero byte
puts the blip into blink-only mode rather than removing it. It is zeroed at
spawn and carried in the unit save record at `0xB1` ([08 R-SAVE-02 §14]).
Presentation only; doc 07 owns the dot pass.

**Established — the reaction step is one routine with four parts, in this
order,** and it runs for every accepted non-heal packet whose kind is not 11,
*before* the kind byte and the attacker fields are rewritten (so parts 3 and 4
still see the **previous** packet's kind and attacker-side snapshot):

1. *Observer notice.* The victim's observer list — the singly linked list of
   observer nodes that every order task links onto its **target** unit at
   construction and unlinks when it retargets (the task record is
   `[04 §3.2]`'s; a node whose unit has a zero definition index is left
   unlinked) — is walked from its head and each node
   carrying a handler receives event code **16**; nodes without a handler are
   skipped. Order tasks install themselves as the handler; the air-movement
   markers construct their nodes unlinked and with no handler. **Established:**
   every order task handles code 16 identically —
   the observer handler ORs the event code into the record's pending word, so
   code 16 *is* pending bit `0x10` and is consumed by the queue pump exactly
   as [04 R-MOV-03 §7] and [04 R-ORD-01 §6] state; no task type has a
   per-type reaction.
2. *Attacker validation.* An attacker whose definition index is zero (a freed
   slot) counts as no attacker for the rest of the routine.
3. *Throttle and retaliation* — exactly `[08 R-AI-01 §11]`: the
   `cancapture`/computer-player construction throttle (one simulation draw of
   bound 300, the only simulation draw anywhere in the damage-intake path),
   then, for a known unallied attacker of a fully built, armed-or-`kamikaze`
   victim owned by a controller of type 1 or 2, the auto-engage order attempt,
   else — when the victim's standing-fire field is nonzero — the per-slot
   offer. The offer's admission for each slot whose armed and tracking bits
   are set is: the §3.1 acquisition physical gate accepts the attacker for that
   slot **and** the weapon is not `commandfire`; the attacker is then installed
   through the unit-target setter (which preserves the Aim latch, §3.2) unless
   the slot's present target exists, passes the same gate, and is clear of the
   slot's bad-target set.
4. *Under-attack notice.* Read the gate-mask word of the victim's front primary
   order (zero when it has none); when its **bit 7** is clear **and** either
   the stored attacker-side snapshot differs from the victim's owner byte or the
   stored last damage kind is 1, request the interface message of kind 2
   (`Under Attack`) for the victim. The message helper posts it only when the
   victim is **not** in the current selection, is owned by the local player, is
   alive and not death-latched; doc 07 owns the queue it enters (per-kind
   throttle deadline, eight entries, duplicate-kind suppression). Because the
   test precedes the field rewrite, the first hit on a fresh unit always
   qualifies (its snapshot is seeded to the neutral value 10 at spawn), while
   the hit that follows an own-side non-weapon packet (a reclaim pulse, a
   cargo cascade) is silent once. Bit 7 of the gate mask is statically set on
   the `Attack_NoMove`, `Attack_Chase` and `AttackSpecial` descriptors and is
   also raised at runtime by the VTOL follow/guard phases (`[04 §3.1]`,
   `[R-ORD-02 §3]`), so a unit already attacking, and an aircraft in those
   phases, never announces `Under Attack`. This is the located consumer doc
   04's static-mask census lists as having no located consumer for bit 7.

**Established — the recorded-attacker reference** (the field doc 04's
`[R-ORD-02 §6]` names). The field the Guard handler and `VTOL_Follow` leg 1 read as
"the ward's attacker" is the dispatcher's **damage-time attacker pointer** —
the raw unit pointer stored by every accepted packet of kind other than 10 when
its attacker id is nonzero, beside the attacker's owner byte as the side
snapshot (§9.1). Its writers are exactly: unit spawn (null, snapshot 10); the
dispatcher (every accepted non-heal packet with a nonzero attacker id, kinds 2
and 11 included); the death handler, which overwrites it from the death
packet's attacker id; and the developer console's kill command (null, snapshot
10, death latch set). Nothing clears it when the attacker dies, so the guard's
re-target and `VTOL_Follow`'s defence can read a stale pointer to a freed or
reused slot; their own predicates (the acquisition gate on a unit whose
definition index is zero) are what reject it, not the field. The reaction
routine above does **not** read this field for retaliation — it uses the
packet's attacker — and reads only the side snapshot for the notice.

#### The death latch and the paralyzer gate read the victim's player controller type [R-WPN-02 §2]

**Established fact.** Neither the death latch of §9.1 step 6 nor the stun
eligibility of §10 tests a movement class, a unit class or a structure flag.
Both read the victim's owning **player record** and require its controller type
to be 1 or 2 — the locally simulated human and computer controllers, as against
3 (remote) and 0 (empty). Written as a movement class the rule inverts for
every structure in the game: retail stuns and kills buildings exactly as it
does tanks. The same predicate appears a third time as the damage gate on the
*projectile's* side (§9.1), where type 3 routes the shot's damage to the owning
peer instead.

### 9.2 Armor and veterancy

**Established fact:** The per-recipient amount is this exact sequence. Inputs:
the weapon's authored default damage (`DAMAGE/default`, read as **uint16**),
the optional per-target override table, the single-precision area falloff from
§9.3 (exactly `1.0f` on the direct-target shortcut), the shooter's kill count,
the target's armored state and definition damage modifier, the target's kill
count, and two global option bits.

```
base = (uint16) weapon.defaultDamage
if (weapon has a damage table) base = overrideLookup(base, target.UnitName)
amount = trunc((double)base * falloff)                 ; __ftol, toward zero
if (record.shooter != null) {
    tier   = min((uint16)shooter.kills / 5, 5)
    amount = (int)((tier * 6 + 100) * amount) / 100     ; signed, truncating
}
if (globalOptions bit 7) amount = amount * 2
if (globalOptions bit 8) amount = amount / 2           ; signed, truncating
                                                       ; doubling precedes halving
-- packet builder, defender side --
if (kind != 10) {
    if (target.armoredState && amount < 30000)         ; strict
        amount = (int32)(((int64)target.definition.damageModifier * amount) >> 16)
    tier   = min((uint16)target.kills / 5, 5)
    amount = (int)((25 - tier) * amount * 4) / 100      ; signed, truncating
}
packet.amount = (int16)amount                          ; modulo 65,536
```

**Established (direct static trace):** the falloff product uses the signed
base and stored single-precision falloff at working precision. Conversion
retains the low 32 bits of signed-64 truncation [01 R-DET-01 §1]. Both percentage
stages form their products in a wrapping signed 32-bit word **before** dividing
by 100 with truncation toward zero. The defender first multiplies by `25-tier`
and then by four; combining those two factors preserves the same low-word
product. Widening either percentage product and dividing before narrowing is
not equivalent for extreme authored overrides or falloff values. The armor
product remains full signed 64-bit arithmetic followed by its arithmetic
right shift, as shown above.

**Established:** a null shooter skips the attacker percentage stage entirely;
a present shooter with zero kills still forms the wrapping multiply by 100
before dividing. These paths can differ at large amounts despite the tier-zero
factor being mathematically one. Both kill-count readers zero-extend the stored
16-bit word before dividing by five and applying the tier cap.

Attacker veterancy therefore scales by `(100 + 6·tier)/100` and defender
veterancy by `(25 − tier)·4/100`, both with the same five-kills-per-tier,
five-tier cap; the defender factor is exactly 1 at tier zero. The armored-state
scale is the definition's **16.16** damage modifier applied as a 64-bit product
shifted right sixteen, gated on the instance's armored-state bit and on the
incoming amount being strictly below 30,000 — which is why the fixed 30,000
self-damage, cargo-cascade and refund packets bypass the armor scale but not
defender veterancy. Healing bypasses both defender scales. Paralyzer packets
use exactly this scaling before their duration credit is queued (§10).

**Established fact:** The override table is a sorted array of
(UnitName, int32 damage) pairs, looked up by a **lower-bound binary search**
using a case-insensitive string compare of the entry key against the target
**definition's UnitName**:

```
lo = table.begin; hi = table.end
while (lo != hi) { mid = lo + (hi - lo)/2                ; halving truncates
                   if (stricmp(mid.key, name) < 0) lo = mid + 1 else hi = mid }
if (lo == table.end)                       -> no override
else if (stricmp(name, lo.key) < 0)        -> no override
else                                        -> base = (int32)lo.value
```

The default is read unsigned 16-bit and an override is signed 32-bit, so an
authored override may exceed 65,535 or be negative where the default cannot.
This is a name lookup, not a category lookup. The table's construction — its
comparator, its duplicate-key and case-variant rules, and the absence of any
`armor.tdf` — is `[R-DMG-01 §1]` below.

**Established fact:** A unit's visible "Veteran" label is a presentation
threshold — the HUD panel prints the kill count while it is below five and the
word `Veteran` from five upward (`[R-DMG-01 §2]`) — so the label first appears
exactly when the numeric damage tier first becomes nonzero; the numeric tier
remains the authoritative arithmetic contract.

**Established fact:** The global double and half gates are bits 7 and 8 of one
16-bit options word, applied in that order (doubling first). The local-command
table's `DoubleShot` and `HalfShot` handlers independently toggle those two
bits; neither handler writes settings, and a fresh battle begins with both
clear. The same word's bit 3 enables feature damage (§13.1) and bit 4
suppresses camera shake. The damage multiply retains the signed low 32 bits
before the signed division, so setting both gates does not necessarily recover
the original amount after overflow `[07 R-CAM-01 §6]`.

**Established fact:** Funnel ownership: the armored-state scale and the
defender veterancy reduction belong to this general pipeline. Document 04
section 9.2 references that funnel for mission water damage specifically; water
damage is a producer of packets into this pipeline, not a separate scaling
path.

#### The armor table is the weapon's `[DAMAGE]` block; there is no `armor.tdf` [R-DMG-01 §1]

**Established — vocabulary.** A whole-image string census for the `armor`
substring (case-insensitive) finds exactly one string, the FBI key
`armoredstate`. There is no `armor.tdf`, no armor-class name, no `armorclass`
and no `armour`; the only `.tdf` file names the executable carries are the
`Weapons\*.tdf` family glob, the generic `*.tdf` glob, `GAMEDATA.TDF`,
`MOVEINFO.TDF`, `SIDEDATA.TDF` and `gamedata\translate.tdf`. Retail has no
armor-class layer at all: the "armor table" is the per-weapon `[DAMAGE]` block
of §9.2, keyed by unit definition name, and nothing else. An implementation
that introduces armor classes invents a mechanism.

**Established — construction.** The weapon-record parser builds the override
table from the section's `[DAMAGE]` child node exactly as follows. All key
comparisons below use the C runtime's case-insensitive string compare (ASCII
case fold, C locale) unless stated otherwise; it is the same comparator the
§9.2 lookup uses, which is why the "is the table sorted under the lookup's
collation" question is closed by construction.

```
node = childSection(record, "DAMAGE")
if (node absent) { defaultDamage = 0 ; override table left as it was }
else {
    defaultDamage = (uint16) intAccessor(node, "default", 0)
    for each key of node, in the node's own key order:            ; [fmt tdf]
        if (stricmp(key, "default") == 0) continue                ; any spelling of default
        value = intAccessor(node, key, 0)                         ; int32
        if (table == null) table = empty table
        lo = lowerBound(table, key)                               ; stricmp(entry.key, key) < 0
        if (lo == end)                       insert (copy of key, value) at lo
        else if (bytes of lo.key != bytes of key)
                                             insert (copy of key, value) at lo
        else                                 lo.value = value      ; overwrite in place
}
```

Consequences, each Established from that shape:

* **Absent block.** No `[DAMAGE]` child stores a default of **0** and leaves
  the table pointer untouched; a block with only `default` allocates no table
  (the pointer stays null and §9.2 keeps the default for every target).
* **Width.** `default` is stored as an unsigned 16-bit value (an authored
  70,000 wraps); every override is a signed 32-bit value. Both go through the
  TDF integer accessor — absent key yields the default named above, otherwise
  the value text is converted by the C runtime's decimal conversion (`atoi`
  semantics: optional leading whitespace and sign, digits up to the first
  non-digit, zero when there are none). A malformed override therefore
  stores 0, not an error, and `default=abc` stores 0 [fmt tdf].
* **Exact-spelling duplicates** (same bytes) in one block collapse at the TDF
  layer to a single last-write-wins entry [fmt tdf]; the weapon parser then
  sees one key. Were the same bytes to reach the loop twice, the second visit
  overwrites the value in place — still last-write-wins.
* **Case-variant duplicates** (`ARMCOM=50; armcom=60;`) survive as two
  entries. Insertion places the later-enumerated variant at the lower-bound
  position, i.e. **before** the earlier one, and the §9.2 lookup — the same
  lower-bound search — returns the first entry of the equal-under-fold run,
  so the **later-enumerated variant wins**. Enumeration order is the TDF
  node's key order, which the format document owns [fmt tdf]; retail data has
  no such duplicate.
* **Entries are (key copy, int32)**, eight bytes each, in a growable array
  whose capacity doubles from one; the parser never sorts after the fact and
  never removes an entry.
* **Same-ID re-parse.** The catalog initializer clears each record's name
  byte and stamps its slot number, but does not clear the override-table
  pointer, so a later section with the same `ID` appends its keys into the
  earlier record's table rather than starting a fresh one — the one
  parser-owned field that does not follow doc 02's same-ID replace-whole
  rule `[02 §5]`. Stock has no same-ID pair (doc 02's stock corpus census),
  so this is a third-party-content edge only.

**Reader census.** `default` — two readers: the per-recipient damage routine
of §9.2 (as the base before the override lookup) and the feature-damage
accumulator of §13.1 (unscaled). The per-name entries — exactly one reader:
the lower-bound lookup of §9.2. No other code walks the table.

The parser uses the lookup's own comparator to place every entry, so the
unsorted case cannot arise, and the duplicate and malformed rules are the
bullets above.

#### `damagemodifier`, `armoredstate`, and every consumer of the kill count [R-DMG-01 §2]

**Established — `damagemodifier`.** Read from the FBI through the fixed-point
accessor with default **1.0** (65,536 in 16.16) and stored as the
definition's 16.16 damage modifier. It has exactly one reader: the packet
builder's armored-state scale in §9.2, `amount = (int32)(((int64)modifier ×
amount) >> 16)`, applied only when the victim's runtime armored bit is set
**and** the incoming 32-bit amount is strictly below 30,000 (signed). Order,
widths and truncation are those already stated in §9.2's listing: name-table
lookup → single-precision falloff product truncated toward zero → attacker
veterancy (signed truncating divide by 100) → global double then half gates →
[builder] armored modifier → defender veterancy (signed truncating divide by
100) → `(int16)` wrap into the packet. Nothing in that chain reads a float
after the falloff product.

**Established — `armoredstate` is two different things.** The runtime armored
bit is bit 1 of the unit's activation-state byte: zero at creation, written
only by COB port 20 (`ARMORED`) through the shared activation edge machine
`[04 §4.4]`, which raises `Activate`/`Deactivate` for bit 0,
`StartBuilding`/`StopBuilding` for bit 3 and the cloak presentation events
for bit 2 but **nothing** for bit 1 — setting or clearing armor raises no
callback and no interface event. Its only reader is the packet builder above.
The FBI key `armoredstate`, by contrast, is parsed by the unit-definition
loader (integer accessor, bit 0 kept) into a definition flag bit, and a
whole-corpus reader census of that definition flag word finds **no reader of
that bit**: the authored key is stored and never consulted. A clone must not
seed the runtime armored bit from it; retail does not.

**Established — the kill count and every consumer.** A unit's kill count is an
unsigned 16-bit field, zeroed at spawn `[04 §2]`, incremented only by the
death handler's full-credit path (§12.1, through the unvalidated attacker
pointer), and wrapping at 65,536. The complete reader census, from a
whole-corpus search over the unit record:

| consumer | expression | owner |
|---|---|---|
| attacker veterancy (per-recipient damage) | `tier = min(kills / 5, 5)`, `amount = (int)((100 + 6·tier) × amount) / 100` | §9.2 |
| defender veterancy (packet builder) | `tier = min(kills / 5, 5)`, `amount = (int)((25 − tier) × amount × 4) / 100` | §9.2 |
| reload after a successful shot | `tier = min(kills / 5, 5)`, reload scaled by `(100 − 6·tier) / 100` | §4.2 |
| turret spread divisor | `div = kills / 12`, spread width divided by `div` when `div > 1` | `[R-WPN-03 §4]` |
| pre-fire lead gate | `kills > 5` (strict, unsigned) | §3.3 |
| capture timer | `((kills / 5) + 10) × t × 10 / 100` on the **target's** kills, no tier cap | `[05 "Capture"]` |
| HUD unit panel | `kills < 5` → `%d kills`, else the word `Veteran` | doc 07 |

The `kills > 5` strict test of the lead gate is the only `>`-form consumer;
every other simulation consumer divides. The panel's `< 5` and the divide's
first nonzero tier coincide at five kills, which is the whole of the "Veteran"
relationship stated in §9.2. The kill *counters on the player record* are a
different family: incremented by §12.1, saved and restored under the `Kills`
key `[08 "Save-file organization"]`, and read by the score and statistics
screens (doc 07); no
simulation path reads them except the leaderboard rank of §12.1.

#### The armored bit's instance field, and the three control-byte gates in the damage intake [R-DMG-01 §8]

Which instance field the step-5 armor gate reads, and where the "owning
player's class" gates read from, confirmed against the packet builder, the
dispatcher and the per-player unit sweep.

**Established — the armor gate reads one bit of one runtime byte.** The
packet builder's step-5 test is bit 1 (mask `0x02`) of the unit's **first
state byte** — the byte whose bit 0 is the `ACTIVATION` posture and whose
bit 1 is the `ARMORED` posture. Its complete writer census is: the unit
constructor (whole byte zeroed at spawn, `[04 §2]`), the shared activation
edge machine through which COB port 1 (`ACTIVATION`, mask 1) and port 20
(`ARMORED`, mask 2) are `set` (`[04 §4.4]`), and the save restore. The COB
`get ARMORED` reads the same bit back. **No path copies the FBI
`armoredstate` flag into it**: that key is parsed into the definition flag
word and has no reader anywhere in the image (`[R-DMG-01 §2]`). The gate is
therefore:

```
armored = (unit.stateByte0 & 0x02) != 0          ; runtime posture only
if (kind != 10 && armored && amount < 30000)      ; strict, signed 32-bit
    amount = (int32)(((int64)definition.damageModifier × amount) >> 16)
```

A build that ORs the definition's `armoredstate` into this test makes every
unit authored `armoredstate=1` permanently armored, which retail never does;
the authored key is inert.

**Established — the "player class" is the player slot's control byte, and it
gates three places.** The byte is the one `[05 R-SHARE-01 §1]` names: `1` a
locally controlled human, `2` a computer player, `3` a remote peer; an
unoccupied slot has no record. It is reached from the unit through the
unit's owning-player record pointer; the unit's own owner byte is the slot
number, not the class. Its readers in the damage path, in the order a hit
meets them:

1. *The damage gate of the central impact routine (§9.1)* — read on the
   **projectile's** side: damage is skipped only when the row named by the
   record's side byte is **occupied and** its control byte is `3`; an
   unoccupied row passes, so a null-shooter record (side 10) always routes
   damage `[R-DMG-01 §9]`. (Shake, sound and art happen either way.)
2. *The death latch of the dispatcher (§9.1 step 6)* — read on the
   **victim's** owner: on a non-positive signed health result, control byte
   `1` or `2` sets the death latch and preserves the modular health;
   anything else (no record, or `3`) clamps health to zero and continues,
   and the unit does not die through this path. The paralyzer branch (§10)
   tests the same two values on the victim's owner before queuing a stun.
3. *The packet builder's publication (§9.1)* — read on the **victim's**
   owner: control byte `3` sends the nine bytes to the attacker's peer for
   every kind except 11. Networking is out of scope; the gate is stated so
   that an implementation sees it is not a damage gate.

**Established — the water-damage gate is the same byte, tested once per unit
by the sweep.** The per-player unit sweep (`[04 R-MOV-03 §1]`) admits slots
whose control byte is `1`, `2` or `3`, and inside each unit's visit gates one
block — water damage, self-repair, the two order pumps, the mover tick and
the post-move correction — on the **owner's** control byte being `1` or `2`.
`[04 §9.2]`'s "only when the owning player's class is 1 or 2" is that test.
In single player every occupied slot is `1` or `2`, so all three gates pass;
an implementation must still read the byte rather than assume it, and must
read `2` as the computer player and `3` as the remote peer
(`[05 R-SHARE-01 §1]`) — under a reading that makes `3` the computer player, a
computer player's units take no water damage and can never be death-latched.

#### The side-10 (null-shooter) record passes the damage gate: the gate's polarity and the eleventh player row [R-DMG-01 §9]

A projectile carrying the neutral side byte 10 — a meteor, a death explosion,
or any null-shooter record — does route damage through the central impact
routine's side-slot gate, because an unoccupied row *passes*. All four findings
below are **Established** by static trace of the central impact routine, the
battle-block allocator, the player-row constructor, every writer of the
occupancy word and control byte, the meteor creator, the common projectile
initializer and the per-tick projectile loop.

**Established — how the side byte is resolved.** The central impact routine
reads the record's side byte, multiplies it by the player-row size and adds
the table base; there is no bound test on the byte. The table it indexes is
constructed with **eleven** rows, not ten: the battle-block allocator
zero-fills the block and then runs the row constructor eleven times over
contiguous rows (`[05 "Player slot"]`). Index 10 is therefore a real,
constructed row — occupancy word `0`, control byte `0`, ally group `10` —
and, because every table walk in the image covers ten rows and every writer
of the occupancy word or control byte is reached only with a seat index below
ten, **row 10 is never occupied at any point of a battle**. (The network
join's free-slot search returns 10 to mean "no free slot" and refuses; it does
not write the row.)

**Established — what the gate tests.** Two reads on the selected row, in this
order, with the routing entered on the first success:

```
row = playerTable[record.side]              ; eleven rows, no bound check
if (row.occupied == 0)  -> route damage     ; unoccupied row PASSES
if (row.control != 3)   -> route damage     ; occupied, not a remote peer: PASSES
otherwise               -> return           ; occupied remote-peer row: no damage
```

The only case that skips damage is an **occupied** row whose control byte is
`3` — a remote peer, whose machine resolves the hit and sends the packet.
For side 10 the first read is the whole test: row 10's occupancy word is
`0`, so a null-shooter record always enters the routing (`areaofeffect <=
16` with a direct unit → per-recipient damage on that unit, shooter feedback
skipped because the shooter is null; otherwise the area enumeration of §9.3).
In single player every occupied row is `1` or `2`, so the gate passes for
every record, real-shooter or neutral; a build that never assigns control
byte `3` may satisfy it trivially, but it must not encode "row must exist".

The test has three branches — *unoccupied → pass; occupied and not remote →
pass; occupied and remote → skip*. An implementation that collapses them into
"exists and not 3 → pass" inverts the absent-row branch, and under that reading
every meteor, every death explosion and every routed burn weapon is harmless to
units, contradicting §12.1 ("the explosion damages every side alike") and
`[R-WPN-02 §5]` (the death explosion record carries no shooter).

**Established — the meteor path does not bypass the central routine.** The
meteor creator takes the next record from the common projectile pool (cap
300; a full pool drops the meteor, §6.5), clears its dead bit and target link,
runs the **common** initializer with a null shooter — which writes side 10,
a null shooter reference and clears the per-record word the per-tick loop
tests before any motion — and then stores the scheduler's velocity vector.
From its first tick a meteor is an ordinary live record of its weapon
definition: it is moved, expired and contact-tested by the same loop as any
shot, and every impact site that loop or the contact test reaches enters the
central impact routine. There is no meteor-specific impact or retirement
path.

**Established — therefore a meteor damages units.** Downstream of the gate
nothing tests the attacker: the area enumeration excludes only the record's
shooter (null never matches a unit) and uses the side byte solely to sort
the damage sums into "friendly" and "enemy" (side 10 equals no owner byte, so
all of it counts as enemy); the per-recipient routine skips only the
veterancy scaling that needs a shooter; the packet builder accepts a null
attacker (attacker id 0 in the packet); the dispatcher subtracts health and
death-latches on the **victim's** owner (§9.1 step 6). A meteor blast
therefore damages, and can kill, every unit of every side within its radius —
the local player's included — and credits nobody, exactly as §12.1 already
pinned. The interceptor sweep's unguarded shooter dereference (§9.3, missing
list) is reachable only for a weapon carrying the interceptor flag, which the
stock meteor weapon does not.

**Implementation rule for gate 1.** `skip = table[side].occupied &&
table[side].control == 3`, where `table` has eleven rows and row 10 is never
occupied — equivalently `side < 10 && slot[side].occupied && slot[side].control
== 3`. Side 10 never skips. Nothing else about the routing changes with the
side byte.

### 9.3 Area damage

**Established fact:** The authoritative blast radius `R` in whole world units
is `(uint16)areaofeffect >> 1`. The broad phase covers
`cells = (R >> 4) + 1` plot cells in each direction around the impact cell,
where the impact cell is
`(trunc(point.Xword / 16), trunc(point.Zword / 16))` computed as
`(v + (v >> 31 & 0xF)) >> 4` — truncation toward zero of the signed high word,
not the arithmetic shift the collision gate uses. Each range is clamped
independently: the low bound to zero, the high bound to the map width or
height. **Established fact:** on each axis the lower bound is
`max(center - cells, 0)` and the exclusive upper bound is
`min(center + cells, mapExtent)`; there is no additional increment on that
upper bound. The position's whole word is read as a signed 16-bit value before
the division by 16. It traverses rows by increasing Z, then cells by increasing
X. Thus a zero-radius span at an unclipped interior center covers two cells
per axis; at the origin it covers only the origin cell. A range whose lower
bound is at or above its upper bound visits nothing.

**Established fact:** Within each cell the order is unit slot zero, unit slot
one, then the feature/terrain candidate.

**Established fact:** A unit candidate must be nonzero **and must not be the
record's shooter** — the shooter is unconditionally excluded from every blast,
before any radius test, which is the whole of retail's self-damage policy
`[R-WPN-02 §10]`. There is no `noselfdamage` key in the image at all
(`[R-WPN-01 §9]`), no owner or alliance test here, and no other self-damage
exemption: a shooter's own other units, and a shooter standing inside its own
blast in a *different* projectile's enumeration, take full damage.

**Established fact:** Unit deduplication happens **before** the radius test,
against a memory of at most 20 unit pointers; a candidate already remembered is
skipped entirely, and a candidate encountered when the memory is full is still
processed but not remembered, so a later occurrence is processed again. An
out-of-radius first sighting therefore consumes a memory entry.

**Established fact:** Unit distance is the three-dimensional distance from the
impact point to the nearest point of the target's **inclusive** model bounding
box, per axis:

```
lo = unit.pos.axis + definition.boundsMin.axis
hi = unit.pos.axis + definition.boundsMax.axis
d.axis = (p.axis <  lo) ? lo - p.axis
       : (p.axis >  hi) ? p.axis - hi
       : 0
d = (int16)( trunc(sqrt((double)dx*dx + (double)dy*dy + (double)dz*dz)) >> 16 )
```

The three deltas are raw 16.16 differences converted to the x87 stack as signed
32-bit integers; the X and Y products are each formed as the 80-bit register
value times its own double-precision store, the Z product is not; the square
root is truncated toward zero by the shared conversion of `[01 §8]` and then
reduced to a **signed 16-bit** whole-world-unit value, so a distance at or
above 32,768 world units wraps negative and passes the acceptance test. A
recipient is accepted only when that value is **strictly less** than `R`. An
impact on or inside the box has distance zero.

**Established fact:** For accepted distance `d` and radius `R` the falloff is
exactly

```
falloff = (d == 0) ? 1.0f
                   : (1.0f - edgeeffectiveness) * f * f + edgeeffectiveness,
          f = (float)d / (float)R - 1.0f
```

with `d` and `R` converted from integers, the intermediate products evaluated
on the x87 stack and the result stored back as single precision. The
zero-distance value is exactly one. The executable does not clamp the authored
edge effectiveness in this path. Base damage is multiplied by this
single-precision falloff and truncated before attacker veterancy (§9.2).

**Established fact:** The feature phase is skipped entirely by `unitsonly`.
Otherwise the cell's fringe deltas resolve the anchor cell exactly as in §8.1,
the anchor's feature word must be below `0xFFFB`, and the reference point is
either the static feature point helper's result (when the cell's flag byte
shows no live instance, or the instance index is zero) or the live animated
instance's position. The distance uses the same truncate-and-narrow form and
the same strict `< R` test, and — unlike units — the **distance is tested
before deduplication** against a memory of at most 64 anchor-cell pointers with
the same never-remembered-when-full behavior. Accepted features enter feature
damage (§13.1).

**Established fact:** After unit and feature enumeration, a weapon carrying the
interceptor flag scans the current projectile prefix in slot order, skips
itself and records whose dead bit is set, and uses the same
sum-of-truncated-squares three-axis metric against the **unhalved** authored
area value with a strict `<`. Each accepted projectile is sent through the
ordinary impact selector with no direct unit target. The loop reloads the live
pool count, so records appended while it scans can be reached. This branch
dereferences the record's shooter without a null test when it accepts a victim,
so an interceptor-flagged weapon fired with no shooter — a death explosion
(§12.2) or a routed burn weapon (§13.1) — faults on its first victim. Stock
content authors no such weapon.

**Established fact:** Area processing accumulates signed enemy-damage and
friendly-damage totals with low-32-bit wrap, classifying by comparing the
record's side byte with each recipient's owning-player byte. Its shooter
feedback helper runs only when the record has a shooter (§9.4).

#### The static feature reference point and the animated-instance distance [R-WPN-04 §3]

**Established.** The feature phase of §9.3 measures from the impact point to a
per-feature reference point. For a cell whose feature has no live animation
instance, the static helper builds it from the resolved anchor cell
`(cx, cz)` (§8.1) and the feature definition's authored `footprintx` /
`footprintz`:

```
X = ((2·cx + footprintx) · 8) << 16    ; = cx·16 + footprintx·8 world units: the footprint's planar centre
Z = ((2·cz + footprintz) · 8) << 16
Y = bilinearTerrainHeight(X, Z) << 16  ; the §12.2 interpolation at that centre; no sea-level floor
```

so an odd footprint centres on a half-cell line and a feature on the sea bed
is measured at the bed, not the surface. For a live animated instance the
reference is the instance's stored position, and its distance goes through a
helper computing `trunc(sqrt((dx·dx + dy·dy) + dz·dz))` with the X and Y
products formed as the 80-bit register value times its own double-precision
store and the Z product plain — the identical form §9.3 gives for units — so
the two branches share one rounding; the caller narrows both to the signed
16-bit whole-unit value and applies the same strict `< R` test.

#### The flash byte's per-visit step is a plain byte decrement [R-WPN-04 §4]

**Established** (raw instruction read of the per-unit tick refresh's step;
caller cadence read; writer and reader census of the byte).

**The step.** The per-unit tick refresh loads the byte, tests it for zero,
and when nonzero stores the byte **decremented by one** — the single-byte
decrement instruction, no sign extension, no widening. Signedness is
immaterial to a decrement: from `0xF0` the byte passes `0xEF`, `0xEE`, …,
`0x01`, `0x00`, which is **240 visits**. The routine runs once per tick from
the per-player unit walk, for every live unit; the only modulus in it (`tick
mod 30`) guards the health-sample roll further down, not this step. So a hit
blinks the unit on the minimap for **240 ticks — eight seconds** — re-armed
to 240 by every hit, and zero is a floor because the nonzero test precedes
the store. Signedness is not observable: the byte's `0xF0` appearance as −16 is
not a sixteen-visit span.

**Readers, re-censused.** The byte's only reader is the minimap contacts
pass (`[03 §3.9]`: `blinkSuppressByte == 0 || blinkPhase`). Three other
routines feed a byte at the same position of a different record to the
simulation RNG as a bound, but that record is a **feature definition**
reached through the feature table, not the unit; they are not readers of it. The
post-capture grace counter, a 32-bit word decremented in the same refresh
immediately after this step, is a separate field (`[04 R-MOV-03 §1]` step
6) and is not carried in the save record (`[08 R-SAVE-02 §14]`), whereas
this byte is — at `0xB1`. An implementation may carry the byte with either
signedness provided it wraps as a byte.

### 9.4 Impulse and pushing absence

**Established fact:** No blast path writes a unit impulse or shove field. The
blast tail calls a shooter-feedback
helper that sets one of two status bits on the SHOOTER unit —
`friendlyTotal × 2 < enemyTotal` sets bit 6, otherwise bit 5 — and that status
byte has no reader anywhere in the bounded corpus. The direct-target shortcut
calls the same helper with the single amount masked to 16 bits placed in the
enemy or friendly slot according to the same side comparison. There is no
impulse or shove field, and no bounded reads turn any blast output into
movement, mass-weighted pushing, or a separate collision resolver. The
`impulsefactor` and `impulseboost` keys do not exist in the image at all
(`[R-WPN-01 §9]`).

**Supported inference:** A clean-room implementation should not add blast
displacement or mass-weighted push behavior. The shooter-status byte is
write-only in the bounded corpus; it may feed presentation or an unrecovered
consumer.

## 10. Paralyzer behavior

**Established fact:** A paralyzer weapon is one whose behavior flag word
carries the paralyzer bit; the only effect of that bit inside the damage
routine is to select packet kind 2 instead of kind 1. The incoming amount is
scaled exactly as ordinary damage (§9.2), including the armored-state modifier
and defender veterancy, before it becomes the stun credit; only healing bypasses
those scales. The credit is therefore the **damage number the weapon would have
dealt**, reinterpreted as ticks.

**Established fact:** A kind-2 packet performs the ordinary preliminary side
effects in the dispatcher's fixed order — damage flash, reaction/wake/retarget,
the recorded kind byte, the attacker pointer and side snapshot, the local-side
"under attack" interface event — **before** any stun eligibility is tested, and
it never subtracts health on any path.

**Established fact:** Stun eligibility is three tests in this order:

1. the victim's alive bit is set and its death latch is clear (re-tested here);
2. the victim's player record exists and its controller type is 1 or 2 — the
   locally simulated controllers, the same predicate that gates the death latch
   in §9.1. This is **not** a movement class, an air/ground distinction, or a
   structure test `[R-WPN-02 §2]`: retail stuns and kills buildings exactly as
   it does tanks, and in single player every unit of a live player is eligible;
3. the victim's definition does not carry `immunetoparalyzer`.

A victim failing any of the three keeps the preliminary side effects and
receives no stun task and no health damage.

**Established fact:** The engine then resolves the task type by the authored
alias `paralyze` and inspects only the **head** of the victim's primary command
list. If the head already carries that task type, the packet's unsigned 16-bit
amount is added to the head's **32-bit** accumulated credit — a plain add, so
repeated hits accumulate with 32-bit wrap and never allocate. Otherwise a task
object is allocated, constructed with the credit as its parameter, and
**prepended**; the linker does not append and does not search the list. If the
allocation fails, the visible linker path is entered with a null task and
dereferences it rather than gracefully dropping the stun.

**Established fact:** The task's first visit — the next unit-phase run of the
victim's primary command list — is exactly:

```
if (credit == 0) { clear the stunned activation bit; complete the task }
if (credit > 1800) credit = 1800          ; SIGNED compare: a 32-bit wrap to a
                                          ; negative credit is NOT capped
stop the unit's current motion
clear all three weapon-slot targets       ; the ordinary TargetCleared path
set the task's wait flag and its absolute resume tick = currentTick + credit
credit = 0
set the stunned activation bit
```

1,800 ticks is 60 seconds. This is a scheduled wait, not a pool decremented
once per tick: nothing counts the stun down, and the task simply becomes
runnable again at the stored tick, at which point the credit is zero and the
first branch clears the flag and removes the task. While the wait is active the
primary command-list runner is blocked, so a hit arriving during the wait
lands on the same head task and extends the **credit**, not the deadline — the
extension takes effect only when the wait expires and the task re-activates,
which is the retail behavior a clone must reproduce rather than adding the
remainder to the deadline.

**Established fact:** The stunned flag is a bit of the unit's activation-state
byte, distinct from the `ACTIVATION` bit in the same byte. The shared setter
raises `Activate`/`Deactivate` for the activation bit, `StartBuilding`/
`StopBuilding` for the build bit and the cloak presentation events for the
cloak bit; it has no case for the stunned bit, so setting or clearing the stun
raises **no COB callback**. The ordinary death latch is a different unit status
flag and must not be used as the stunned state.

**Established fact:** Damage, healing, death, economy settlement, cloak/upkeep,
and the eight-tick automatic-heal path are outside the blocked task runner and
can continue while the unit is stunned. Automatic acquisition and retention
(§3.2) reject a target already carrying the stunned bit **only for a paralyzer
weapon**; ordinary weapons ignore it. Standard save/load serializes the task
and its duration state.

#### What the stunned bit gates, and why the stun is binary [R-DMG-01 §11]

A paralyzed unit is stopped, not slowed, and the stunned bit itself switches
nothing off on its carrier.

**Established (bounded: every reader of the activation-state byte in the
decompiled set).** The stunned bit has exactly two readers besides its setter,
and both test a **candidate**, not the reader: the shared autonomous target
search rejects a candidate carrying the bit when the searching weapon is a
paralyzer (§3.2), and the computer player's target picker applies the same
paralyzer-only rejection. The per-unit weapon phase, the mover step, the
height snap, the two order pumps, the settlement pass and the damage
dispatcher do not read it. The bit is a mark *on* the victim for other
units' benefit; it disables nothing on the victim directly.

**Established — what the task does instead.** The stun task's first visit
(§10) is, in order: the *release* verb on all three weapon slots — the slot
verb of [04 R-UNIT-06 §5] part 3 that clears the autonomy bit and, for a
non-empty target pair, resets it and posts `TargetCleared`; then the
unconditional target clear on each slot (the target-pair reset and
`TargetCleared`, with no control-byte change); then the release of the
order node's goal payload — the object through which a node drives the mover,
which is §10's "stop the unit's current motion": a payload release, not a
speed write; then the wait arm. There is no speed scaling, no
movement-class change and no per-tick decrement anywhere in the path. The
unit is stopped because the wait at the head of its primary list blocks the
list runner (no order can move it, retarget it or run a guard leg), and it
does not fire because every autonomous reader — the autonomous scan, the
retaliation offer, the guard legs — requires the autonomy bit the release verb
just cleared, and the target pairs are empty. The stun is therefore
**binary**: fully stopped and silent for exactly the credited ticks, then the
next order in the list resumes (its own phase 0 returns the slots to
autonomy in the ordinary way).

**Implementation rule.** Model the stun as the head wait task plus the two
slot operations above, and keep the stunned flag as a candidate mark read only
by paralyzer-weapon target selection. A weapon-phase early return keyed on the
flag is redundant with the slot state and must not be relied on as the
mechanism.

## 11. Stockpile and interceptor behavior

### 11.1 Stockpile queue

**Established fact:** MAKENUKE and MAKEANTI order aliases map to BUILDWEAPON queue entries. Stockpile weapons use the secondary weapon-production queue; non-stockpile entries use the ordinary build queue. Queue increments coalesce matching weapon/type entries at the tail; decrements reduce or unlink entries.

**Established fact:** Stockpile production and launch keep two distinct values:
the linked queue node's signed requested count and the weapon slot's byte-sized
completed-round remainder. In the recovered stockpile-production branch, one
completed round increments the slot byte, decrements the queue-node count, and
then requests a selected-unit interface refresh. The refresh helper itself does
not mutate either value and does not clamp an integer queue count into the byte.

**Established fact:** The queue node and the slot are wired as follows, and an
implementation needs the wiring before the arithmetic below means anything. A
stockpile node lives in the unit's **secondary** production queue — a singly
linked list walked from its head through a next link — and is distinguished
from other nodes in that list by one node flag bit. The node carries a signed
requested count, an integer **progress** value, and a **slot index** that is
the caller-supplied build-type argument stored verbatim. That slot index
selects the unit's weapon slot directly, and the weapon record found there
supplies the `reloadtime`, `energypershot` and `metalpershot` the visit uses.
The interface reads the same three fields: the build-page percentage for a
stockpile item is exactly `progress * 100 / reloadtime`, an integer divide, so
progress is denominated in the same units as the compiled reload time and the
cap below is a full round.

**Established fact:** Each stockpile work visit advances a per-node progress
value by five, capped at the selected weapon's compiled reload-time value.
**Established (direct static trace):** this production reader zero-extends the
stored 16-bit reload word, unlike the firing reader's signed interpretation.
A stored all-ones reload word therefore means a production build time of
65535. Both cumulative cost expressions use the stored single-precision cost
at working precision, then retain the low 32 bits of signed-64 truncation
[01 R-DET-01 §1]. Their subtraction wraps in the signed 32-bit result before
that difference is stored as single precision for resource admission. There
is no intervening single-precision quotient store. The
admitted energy and metal demands for that visit are the differences of two
independently truncated cumulative proportional costs:

`energyDelta = trunc(next × energyCost / buildTime) - trunc(old × energyCost /
buildTime)`

`metalDelta = trunc(next × metalCost / buildTime) - trunc(old × metalCost /
buildTime)`

The pair is admitted through the ordinary two-resource helper. Progress is not
advanced when admission is rejected, but both requested amounts are retained.
Failed admission schedules a ten-tick retry deadline; accepted but incomplete
work schedules a five-tick retry. Completion increments the slot byte,
decrements the signed queue count, and requests the selected-unit refresh. A
new round is blocked with a 300-tick wait when the slot byte is already
greater than 199; the ordinary path can reach 200 but does not start a round
beyond it, and assets whose build time is at most five can complete multiple
queued rounds in one visit while admission remains open.

**Established fact:** Launch requires the weapon's stockpile flag and a nonzero
slot remainder. A successful spawner decrements the slot byte and requests the
same selected-unit interface refresh. An empty remainder prevents projectile
allocation. Stockpile launch bypasses the ordinary per-launch energy/metal
debit; its resource cost belongs to the production path. Launch is checked
before production in the same unit slot, so a round completed by the secondary
queue cannot launch until the next simulation tick.

**Established fact:** The queue node's slot index is the caller-supplied
build-type argument stored verbatim by the node constructor; there is no
weapon-id-to-slot translation anywhere in the queue path. The UI alias path
always supplies zero (slot 0), which is where shipped stockpile weapons live.
A malformed build-type of three or greater indexes past the unit record with
no bounds check.

**Unknown:** cancellation interaction with admitted carry, repeat requeue,
and save and load reconstruction beyond the established queue count,
progress, and slot-byte persistence.

#### The stockpile queue's malformed arms: the unarmed slot, the empty stockpile, and the byte's readers and writers [R-WPN-05 §2]

§11.1 states the ordinary production visit. These are the corner cases, from
the production handler, the secondary pump that drives it, the slot pipeline's
launch gate, the slot initializer and the interface percentage.

**Established — the production handler is a three-phase order body.** The
node's slot index is used verbatim to select the weapon slot; the handler
tests neither that the slot's weapon carries `stockpile` nor that it is a
real weapon. Phase 0: requested count below 1 → *complete* (code 5, the
node is unlinked); slot byte above 199 → deadline 300, *hold*; else
progress := 0, *advance*. Phase 1: `next := min(progress + 5, reloadtime)`;
the two deltas of §11.1, each `trunc(next·cost/reloadtime) −
trunc(old·cost/reloadtime)` formed in floating point with the multiply
before the divide; the two-resource admission refused → deadline 10,
*hold*; accepted → progress := next; `next < reloadtime` → deadline 5,
*hold*; else *advance*. Phase 2: slot byte += 1 (no cap, no wrap test),
count −= 1, request the selected-unit refresh, *restart* (phase 0). Any
other phase → *cancel-all*. The codes are [04 §3.3]'s.

**Established — the secondary pump re-dispatches the head after every
result that leaves the record in place.** After a *restart*, *advance*,
*hold* or *complete* the secondary pump reloads the queue **head** and tests
it again in the same visit; only a record whose gate is armed and whose
deadline has not expired is passed over for its successor. So a round whose
`reloadtime` is at most 5 runs phase 0 → 1 → 2 → 0 → … within one visit,
and the chain stops only when the count reaches 0 (*complete*), the
admission refuses (*hold* 10), or the slot byte passes 199 (*hold* 300).
This is the mechanism behind §11.1's "assets whose build time is at most
five can complete multiple queued rounds in one visit". Every
*hold* in the handler arms a deadline first; a *hold* returned with a clear
gate would spin the pump on the head for the rest of the visit.

**Established — an unarmed slot points at weapon record 0, and the order
completes for free.** The slot initializer copies the definition's three
weapon references into the slots unconditionally and zeroes each slot's
byte; a definition with no weapon in a slot holds a reference to weapon
record 0, the `[noweapon]` sentinel of [R-DMG-01 §5] (`reloadtime` 0, both
per-shot costs 0), never a null. A `BUILDWEAPON` node on such a unit — the
HUD alias issues one only where the unit's build page authors a
`MAKENUKE`/`MAKEANTI` button; the mission `Bw` verb and the two network
decoders are the other producers (§11.1) — therefore does not fault. In
phase 1 `next = min(5, 0) = 0`, both quotients are `0·0/0`, an invalid
operation whose truncation is the same indefinite integer on both sides, so
both deltas are exactly 0; the admission accepts a zero request unless the
unit's buckets already carry debt; `0 < 0` is false, so the round *advances*
and phase 2 completes it. The pump then restarts the head in the same visit:
**every queued round completes at once, at no cost**, into that slot's
byte, until the count is 0 or the byte passes 199. Nothing reads the byte
for a weapon without `stockpile` (below), so the only visible trace is the
interface refresh — and, if the node outlives the visit (a count large
enough to hit the 199 gate), the build-page percentage of §11.1,
`progress·100 / reloadtime`, is an integer divide by zero with no guard: a
hovered unit in that state faults. An implementation should refuse to
enqueue a stockpile node against a slot whose weapon is record 0 or lacks
`stockpile`; that is the one behavior that is both safe and
indistinguishable from retail in every shipped case.

**Established — the empty-stockpile fire order.** The slot pipeline's fire
gate for a `stockpile` weapon is "slot byte nonzero" in place of the
per-shot cost test; when it fails, the executor is not run, no reload is
written, no state or firing flag changes, and no order-side text or
cancellation follows — the attack order keeps its target and the slot
re-tests every tick. The vertical-launch aim dispatch is gated the same way
(`not stockpile, or byte nonzero`), so an empty launcher never even aims.
On a successful launch the byte is decremented **after** the nonzero test,
so it cannot underflow; the ordinary reload write and the per-shot debit
are skipped for the stockpile arm (§11.1).

**Established — every reader and writer of the slot byte.** Writers: the
slot initializer (0 at unit creation), the production handler's phase 2
(+1), the successful stockpile launch (−1), and save restoration
([08 "Save-file organization"]). Readers: the handler's phase-0 gate
(`> 199`), the vertical-launch aim gate, the fire gate, and the interceptor
aim scan and fire-time rescan (`≠ 0`, §11.2). Bounded census over the slot
pipeline, the production handler, the slot initializer and the interceptor
scan.

**Established — the byte cannot overflow or wrap.** It is unsigned, the launch
path cannot take it below zero, and the handler cannot take it past 200 — a
value of 200..255 can arrive only from a save, and then blocks every new round
forever (phase 0's `> 199` hold, re-armed every 300 ticks), never wrapping.

### 11.2 Interceptors

**Established fact:** Targetable and interceptor flags are parsed. Interceptor
coverage is a separate authored scalar from ordinary fire range. The automatic
interceptor scan runs from the same per-slot position in the autonomous scan
that ordinary acquisition runs from (§3.2) and is chosen by the slot weapon's
interceptor flag; it requires a nonzero slot ammunition byte and then walks the
packed projectile prefix from index zero to the live count captured at entry,
accepting the first candidate that satisfies, in this order: an owner side byte
different from the interceptor unit's owning-player byte (the alliance matrix
is not consulted); the candidate weapon's `targetable` flag; the inclusive
coverage square below on the candidate's **stored aim point**; and a full
second pass over the same prefix finding no record whose projectile-link field
already names this candidate. A hit installs a **point** target on the slot
from the candidate's current position; a miss clears the slot target through
the ordinary path. Neither pass filters on the dead bit.

**Established fact:** The acquisition coverage is an axis-aligned square, not
a radius circle, and tie order is first unclaimed in projectile-pool order,
not nearest. Each axis is a wrapped unsigned comparison, written out:

```
C = weapon.coverage                                   ; authored world units
accept axis iff (uint32)((interceptor.axis - candidate.storedTarget.axis)
                         + (C << 16)) <= (uint32)(C << 17)
```

evaluated for X and then Z on the 16.16 values, which is `|delta| <= C` in
world units for ordinary nonnegative coverage and is **inclusive** at the
boundary; the unsigned form means an authored coverage at or above 32,768
world units, or a delta that overflows the addition, accepts a different set.
Two different positions play two different roles and must not be conflated:
**the scan metric** is measured on the candidate's *stored aim point* — where
the threat was aimed when created, so interceptors defend the aimed-at ground
point — while **the slot store** written at acquisition packs the candidate's
*current position* into the interceptor unit's fixed target words. The
interceptor spawner rescans immediately before firing; the final candidate's
record pointer stored in the new interceptor's reservation-link field at
spawn time is the authoritative reservation, and that store is what later
scans test when rejecting candidates already claimed by any pool record.

**Established fact:** Non-cruise guidance reads the linked projectile's current
point each tick, so target motion after launch is tracked by the link rather
than by the fixed point written during the earlier aim scan. Linked contact
uses current-point three-dimensional squared distance with a strict `<
areaofeffect²` test, invokes ordinary impact without a direct unit target, and
does not return from the collision resolver; the resolver continues into later
cell contacts. The linked projectile is not directly deleted by the proximity
test; the interceptor missile impacts itself and its interceptor-flagged
explosion sweep performs the projectile victim removals.

**Established fact:** When an interceptor-flagged weapon explodes, after the
ordinary unit and feature area enumeration it scans live non-self projectile
records in pool order using the unhalved authored area value. It does not
filter by side, alliance, or targetable state; friendly projectiles can be
removed. Each qualifying victim is forced through the ordinary impact selector
and a victim signature packet containing its stored target position and weapon
index byte is published; the exploding projectile's own signature is published
once per qualifying victim. The packet receiver scans pool order and impacts the
first record whose stored target position and weapon index byte all match.

**Established fact:** No candidate before firing leaves the shot pending.
The spawner path then performs no ammunition, reload, firing-state, or
resource mutation, matching the ordinary slot pipeline's failure path.

**Established fact:** Neither interceptor scan tests liveness. A
dead-but-uncompacted targetable enemy projectile within coverage and unclaimed
is still selected by the aim-time scan and by the fire-time rescan, and its
frozen current point is still tracked and proximally impacted. The scans cannot
distinguish dead from live candidates; "both dead" prevents a shot only through
the coverage and claim state, never through a dead-bit test.

**Unknown:** The multiplayer reconstruction index-versus-pointer anomaly that
can store a small pool index as a raw pointer, and side effects of other
interceptor-adjacent failure modes remain open.

#### The coverage compare's width, the projectile blast metric, the signature byte, and where `firestarter` is read [R-WPN-05 §10]

From the interceptor scan, the interceptor-flagged explosion sweep, the weapon
loader and the feature-damage helper.

**Established — the coverage compare is 32-bit unsigned, and a negative
coverage inverts it.** `coverage` is a 32-bit integer in the weapon record.
The scan forms `C << 16` and `C << 17` in 32-bit arithmetic and tests, per
axis,

```
(uint32)((interceptorUnit.axis − candidate.storedTarget.axis) + (C << 16))
    <= (uint32)(C << 17)
```

on the 16.16 values, X first and then Z, never Y; the subtraction is the
interceptor **unit's** position (not its weapon piece) minus the candidate's
stored aim point. There is no separate rule for malformed values — the
formula in `uint32` is the whole contract. A negative coverage `−k` accepts
exactly the complement of the open square of half-side `k`: a candidate aimed
farther than `k` world units on both axes, or at exactly `k`, is accepted and
one inside is rejected. A coverage at or above 32,768 wraps `C << 17` and
accepts whatever set the formula then gives. An implementation computes the
expression verbatim in unsigned 32-bit arithmetic and carries no "negative
means none" guard. Neither pass tests the dead bit (§11.2). The scan is
ordinary single-process code; the "index-versus-pointer anomaly" listed as
Unknown belongs to the multiplayer reconstruction path, which Nanolathe does
not implement — it stays Unknown and out of scope.

**Established — the projectile sweep's metric is the sum of three truncated
squares.** §9.3's "same sum-of-truncated-squares three-axis metric" means,
exactly: with `d.axis = exploder.current.axis − victim.current.axis` as raw
16.16 differences,

```
((dx·dx) >> 32) + ((dy·dy) >> 32) + ((dz·dz) >> 32)  <  area × area
```

each square a 64-bit signed product arithmetically shifted right by 32
(whole world units squared, truncated per axis), summed and compared as
signed 32-bit integers against the square of the **unhalved** 16-bit
`areaofeffect`. The Y term is the two projectile records' current heights in
the same 16.16 domain as X and Z — no terrain sample and no separate scale;
the "victim height" is simply the victim record's current Y. Squaring the
whole 16.16 delta and then truncating is not the same as truncating the delta
and squaring it: a delta of 1.9 world units contributes 3, not 1.

**Established — the signature byte is the weapon record's slot index.** Each
accepted victim goes through the ordinary impact selector with no direct unit
target, and then two 14-byte events of kind 14 are emitted to the shooter's
owner's endpoint — one carrying the victim's stored target triple and its
weapon byte, one carrying the exploder's own — where the weapon byte is the
byte the weapon loader stamps into every record with that record's own slot
index (0..255). Because an authored `ID` selects the record slot
([02 "Weapon record"]), the byte **is** the low byte of the authored `ID` for
every record an `ID` selects; `uint8(weaponID & 0xFF)` is exact. The events
are the multiplayer replication of the removal: in a single-process game the
local impact-selector call is the whole effect and the signature has no
consumer.

**Established — `firestarter` is read once, in the feature-damage helper, as
a byte.** The nonzero test sits inside the feature-damage accumulator that
the area-damage feature phase (§9.3) calls for each accepted feature — the
listing of §13.1 — and nowhere in the stockpile, interceptor or projectile
paths. The weapon loader stores the authored integer's **low byte**
([02 "Weapon record"] lists it as 8-bit), so an authored `firestarter` of 256
ignites nothing; the test is on that byte. An implementation keeps the test
beside its feature-damage accumulator, not beside its interceptor code.

### 11.3 No-radar presentation

**Established fact:** The weapon `noradar` flag is presentation-only in the
bounded census. Its sole located gameplay-adjacent reader is the minimap
projectile-dot builder, which suppresses the ordinary dot path for `noradar`
projectiles. Non-`noradar` projectiles draw when their cell is mapped for the
viewer or was fired by the viewer's own side; `noradar` projectiles satisfy
neither condition through that path. Targetable and interceptor projectiles use
a separate classified minimap branch. No gameplay, acquisition, guidance,
collision, or damage reader for `noradar` was found in the reviewed 862-function
corpus plus the flag-mask immediate census. The retail stock reach is one
definition, `EARTHQUAKE`.

## 12. Death, kill credit, corpses, and feature conversion

### 12.1 Unit death

**Established fact:** Unit health is updated by the central damage path. Lethal
damage on a unit whose player controller type is 1 or 2 sets the death latch
and records the damage-packet kind as the death cause (§9.1). The next unit
phase enters the death preamble. Other scenario, teardown, and game-over paths
can enter the same handler with explicit causes.

**Established fact:** The preamble runs on a unit whose alive bit is set and
does, in order:

1. compare the unit's definition `UnitName` case-insensitively with the
   commander name of the side definition named by the unit's owning player; on
   a match, clear the commander-alive bit on that player record. This is the
   marker the campaign and skirmish end conditions poll `[08 "Evaluation"]`;
2. select severity and the corpse-chain variant by cause;
3. build the eleven-byte death packet;
4. when the unit's controller type is 1 or 2, publish that packet to the
   owning player's network destination (multiplayer only);
5. call the central death handler in **local** mode;
6. for a commander death with the commander-death rule word nonzero and a
   locally simulated owner, enter the game-over path `[08 "Evaluation"]`.

**Established fact:** Severity is integer arithmetic on the already-negative
health:

```
severity = clamp( ( (uint32)((int16)health * -100) / (uint32)maxHealth
                    + (uint8)previousSamplePercent ) / 2, 1, 100 )
```

The multiply is a signed 16-to-32 multiply by -100; the divide by maximum
health and the halving are **unsigned**; the clamp bounds are applied with
signed compares. `previousSamplePercent` is the health percentage retained from
the previous 30-tick sampling boundary, not necessarily the health immediately
before the lethal packet.

**Established fact:** The severity/variant bypass map is exact, tested in this
order:

| cause | severity | variant | `Killed` query |
|---|---|---|---|
| 7 (immediate feature conversion) | 0 | 1 | skipped |
| 4, 5, 9, or **any** cause while health is still positive | 0 | 0 | skipped |
| everything else | the expression above | script-selected | **runs** |

Causes 4, 5 and 9 therefore produce no script callbacks, no explosion (severity
is not positive) and no corpse (the variant nibble is zero); cause 7 stamps the
authored `Corpse` feature immediately with no script work. After the query
returns, a nonzero remaining-build-fraction float forces the variant to zero —
the same float gates the death explosion and veterancy and scales the cause-5
bounty.

**Established fact:** The `Killed` query is **synchronous**: it passes severity
and the variant cell as two in/out cells and drains the unit's script threads
inline. Its two cells are the script's two parameters. The variant cell is
**uninitialized** on the full-pipeline path — it holds caller-indeterminate
stack history when no `Killed` body writes it — except where the
build-fraction rule forces zero.

**Established fact (Killed authorship):** A bytecode census of the shipped COB
corpus settles the variant-cell authorship question: every shipped unit with a
`Killed` body (153 of 157 extracted scripts) declares the two-parameter form
and writes its second parameter — the corpse-depth cell — and none reassigns
the severity cell, so the packet severity is always the computed severity and
the depth nibble is always authored. The four remaining shipped scripts (the
two commanders and the two dragon bosses) have no `Killed` body; their variant
cell is deterministic-but-opaque stack history unless the build-fraction rule
forces zero.

**Established fact:** The death packet is eleven bytes:

```
byte 0       constant builder tag 0x0C
bytes 1-2    victim id, uint16
bytes 3-6    the attacker's side, encoded by the shared side-to-word helper
bytes 7-8    attacker id, uint16, 0 when the stored attacker pointer is null
byte 9       severity, signed
byte 10      (cause << 4) | (variant & 0x0F)
```

The HIGH nibble is the death cause, taken from the last damage-kind byte
recorded at damage time, and is not part of the script return at all; the LOW
nibble is the query's second output cell. An explicit script `return` is
delivered to a completion receiver (none is installed for this query) or
dropped, and never rewrites either nibble. This value is not a wreck
probability.

**Established fact:** Local authoritative death does not issue a second
`Killed` callback after the synchronous query. The received-network path calls
the same handler in **replay** mode; only replay mode dispatches `Killed`
asynchronously, with one argument (the packet's severity byte) and only when
that signed byte is positive. Its return value is ignored.

**Established fact:** The central handler resolves the victim from the packet
id by slot arithmetic **with no null test** — a packet naming id zero faults —
and returns immediately unless the victim's alive bit is set. It then, in this
order: raises a 60-tick locator presentation event when the victim's ally group
matches the local viewer's; reconstructs the attacker pointer from the packet
id and the attacker side from the packet's encoded side; runs the fixed
teardown helpers (statistics hook, order/queue release, audio release,
occupancy unstamp with the removal sentinel, and the burst-anchor sweep of
§5.2); detaches the victim from its carrier when it has one; runs the cargo
cascade; dispatches replay-mode `Killed`; runs the credit switch; runs the
leader announcement; applies the cause-5 bounty; fires the death explosion;
places the corpse; and finally tears down the script, mover and definition
references, clears the alive bit and the two low status bits, points the unit
at the shared dead definition, and decrements the owning player's live-unit
count — at zero, multiplayer sessions notify the peer and skirmish sessions run
player elimination.

**Established fact:** The cargo cascade is a `while` loop over the victim's
cargo list head. Each cargo unit receives a **30,000** damage packet through
the ordinary builder — so it is scaled by defender veterancy but not by the
armored-state modifier, whose gate is a strict `< 30,000` — with the attacker
argument set to the **victim's own killer**, and is then detached. The cause
passed is 3 when the carrier's own cause nibble is 3 and 6 otherwise.

**Established fact:** Death credit is a cause-gated switch on the packet's
cause nibble, not merely the presence of an attacker:

* **causes 1 and 6** take the full path;
* **cause 3** takes a partial path: the victim's player unit-loss counter and,
  for a commander, its commander-loss counter are incremented, and only when
  the local player's alliance byte for the victim's ally group is zero. No kill
  credit, no veterancy;
* **cause 5** joins the full path only when the stored attacker side is neither
  the neutral side value 10 nor the victim's own owner byte;
* every other cause credits nobody.

The full path does, in order: increment the victim player's unit-loss counter;
increment the **attacker player's** kill counter when the attacker side is not
10, the victim's remaining-build-fraction float is exactly zero, and the
victim's owner differs from the attacker side; for a commander victim,
increment the attacker player's commander-kill counter (attacker side not 10)
and the victim player's commander-loss counter; increment the **attacker
unit's** wrapping kill word under the same three conditions — through the
reconstructed, unvalidated pointer, which is where a stale slot can be
credited; and raise an interface event when the attacker side is the local
side. None of these counters is a field of the unit definition.

**Established fact (`[R-WPN-02 §9]`):** After a crediting death the engine
maintains a per-player leaderboard rank byte and can broadcast a lead message.
The rank
update runs only when the attacker's player record is active, its controller
type is 1, 2 or 3, its ally byte is not 10, the session mode is skirmish or
multiplayer, and its current rank is not already zero. The compared score is
the player's **commander**-kill counter when the commander-death rule word is
2 and the ordinary kill counter otherwise. It computes `best` as the minimum
rank over the other player records that are present and not excluded by a
runtime bit and whose score is **strictly less** than the attacker's; when
`best` is strictly better than the attacker's current rank, every player whose
rank lies in `[best, myRank)` is pushed down by one and the attacker takes
`best`. Only when `best` is exactly zero — the attacker newly becomes sole
leader — is the localized message `%s has taken the lead with %d kills`
formatted and broadcast on the in-game message channel. Its two arguments are
the player's name and the **unit** kill counter, even in the rule-2 session
where the ranking used commander kills. It is per player, never per team, and
it is re-announced only on a later transition back to rank zero.

**Established fact (`selfdestructcountdown`, `[R-WPN-02 §8]`):**
The authored `selfdestructcountdown` is read from the FBI as a decimal string,
masked to **three bits** (`value & 7`) and packed into a three-bit field of the
definition's second flag word; an absent key stores **5**. The self-destruct
task reads it once, on its first visit, into its own counter (tagged so a
restarted task does not re-seed), and then runs one visit per wait:

```
if (counter == 0) { mark fired; announce message[0]; wait simulationRandom(15) ticks }
else               { announce message[counter]; counter -= 1; wait 30 ticks }
```

and on the visit **after** the fired mark it emits a 30,000-damage packet with
the unit as both attacker and victim and cause 3. So the countdown is `N`
whole seconds of announcements followed by a uniform **0–14 tick** jitter drawn
once from the simulation stream, and the damage lands on the next visit after
that wait. A cancel request during the countdown emits the abort message and
completes the task without damage, provided the death latch is still clear. A
definition whose three-bit field holds 0 skips the announcements entirely and
detonates on the first visit. The announcement table has exactly **six**
entries (counts 5 down to 0), so a field value of 6 or 7 indexes past it on the
first visit; stock content authors no such value. `[04 §3]` owns the order that
installs this task.

**Established fact:** The complete located producer set for death causes is:

- **cause 3 — self-destruct countdown:** packet-builder emission of 30,000
  self damage; propagated to cargo, where a carrier dying with cause 3 gives
  every cargo unit cause 3 and any other carrier cause cascades its cargo as
  cause 6. Credit branch: the partial statistics path — no ordinary
  team-kill/veterancy block.
- **cause 4 — capture/owner replacement:** packet builder invoked with a null
  attacker at both of its call sites. Credit branch: none.
- **cause 5 — reclaim/build-complete pulse:** packet builder invoked with
  attacker, victim, and amount; the separate fatal payment on cause 5 runs
  against the reconstructed attacker slot even when credit fails. Credit
  branch: full only when the stored attacker side byte is neither the neutral
  side nor the victim's side.
- **cause 6 — default cargo cascade:** emitted by the central death handler
  itself during its cargo loop, 30,000 damage per cargo unit while the cargo
  list head is nonzero. Credit branch: full — it shares cause 1's full-credit
  path.
- **cause 7 — immediate feature conversion:** written by a DIRECT store of the
  damage-kind byte plus the death latch, NOT through the packet builder, from
  (a) the spawn-with-parameter branch and (b) the conversion handler gated on
  the definition's is-feature bit. Credit branch: none; severity forced zero
  and corpse-depth nibble forced one (place the authored Corpse).
- **cause 8 — game teardown sweep:** the teardown kill loop dispatches every
  live unit through the dead-latch handler with cause 8. Credit branch: none.
- **cause 9 — construction-fraction deconstruction/refund:** packet builder
  invoked with 30,000 from the two refund paths. Credit branch: none.
- **cause 11 — mission water damage:** dispatched when both mission drowning
  fields are nonzero, on every tick where `tick % 30 == 0`, for units in the
  two mobile movement classes whose height is at or below the mission water
  level and whose definition lacks the corresponding underwater-operation flag
  bit; attacker null. Credit branch: none.
- **causes 12–15:** no local producer anywhere in the direct caller census of
  the packet builder; a cause nibble in this range received by network or
  restored from a save takes the none-credit branch.

Adjacent kinds for completeness: 1 ordinary weapon damage (full credit); 2
paralyze packet (never subtracts health; cannot become a death cause through
its own path); 10 heal (returns before the kind-byte write and attacker
snapshot); 0 scenario/load removal.

Two attribution edges are pinned. **Meteors credit nobody**: their attacker
identity is null (neutral side), so no veterancy increments and no kill
statistics result even though the explosion damages every side alike. **Cargo
killed by carrier death credits the carrier's killer**: the cascade applies its
30000 damage per cargo with the attacker argument set to the carrier's killer.

**Established fact:** The cause-5 bounty credits the reconstructed attacker
unit's metal-production accumulator. Its difficulty gate reads the economy
subrecord's cached owner reference, which construction rebuilds to name the
same player as the unit's ordinary owner. Capture replaces the unit under the
recipient rather than changing these references in place. Document 05
[R-WORK-01 §4] owns the refund arithmetic, its single final rounding boundary,
and the retained accumulator behavior for freed or reused raw attacker slots.

**Established fact:** The unit float that gates the death explosion is the
remaining-build-fraction/landed indicator shared with construction and flight
state — one with health zero under construction, zero when grounded or normal,
nonzero while airborne. The gate compares it **equal to 0.0f**, so units still
under construction do not detonate through this path.

#### The death timeline, `Killed` selection, kill credit, and the absent `Unit destroyed` diagnostic [R-DMG-01 §3]

This composes contracts Established above and in `[R-CB-01 §7]`,
`[R-COB-02 §2]` and `[R-WPN-02 §5]`; it restates no arithmetic — read the cited
paragraph for that.

**Established — the timeline of one weapon death, in tick order.**

1. *Projectile phase, tick N.* The central impact (§9.1) computes the amount
   (§9.2), the packet builder scales it and dispatches synchronously (§9.1):
   the kind byte is recorded as the future death cause, the attacker pointer
   and its side snapshot are stored, health is reduced by exact 16-bit
   modular subtraction, and — for a victim owned by a locally simulated
   player — a non-positive result sets the death latch and returns **without
   clamping**. `HitByWeapon`/`TakeDamage` are not emitted on the lethal
   packet (the return precedes them). Every later packet in this tick is
   rejected by the latch: no further damage, no re-attribution.
2. *Unit sweep, tick N+1, the victim's own slot visit* `[R-COB-02 §2]` step
   6 — the death preamble (above) runs: commander marker, severity from the
   negative health and the previous 30-tick sample, the cause bypass table,
   the **synchronous** `Killed(severity, variantCell)` query drained inline
   (`[R-CB-01 §7]`), the build-fraction override of the variant, the
   eleven-byte packet, then the central handler in local mode.
3. *Same visit, the central handler, in this order:* ally locator event;
   attacker re-resolution from the packet id; the fixed teardown helpers
   (statistics, order/queue release, audio, occupancy unstamp, burst-anchor
   sweep); carrier detach; **cargo cascade** (30,000 packets, attacker = the
   victim's killer, cause 3 or 6); the credit switch; the leaderboard; the
   cause-5 bounty; the **death explosion** (§12.2) — delivered through the
   central impact with a null shooter, so its blast is resolved *inside* the
   unit sweep, before the projectile phase of tick N+1, and any unit it
   kills is latched now and dies in its own later slot visit (a later slot
   this tick, or tick N+2 if its slot already passed); then the **corpse**
   (§12.2); then script/mover teardown, the alive-bit clear and the slot
   free, which makes the slot reusable by a spawn later in the same sweep.

**Established — which cause codes run `Killed`, and with what.** The query
runs for every cause except 7 and except 4, 5 and 9, and never while health
is still positive (§12.1 table). Its two cells are the packet severity (seeded
with the computed severity; no shipped script writes it) and the corpse-depth
nibble (uninitialized on entry; every shipped `Killed` body writes it). The
cause never reaches the script: it is the high nibble of the packet byte,
taken from the last damage kind, and `Killed` is not told whether the death
was a weapon (1), self-destruct (3), cargo cascade (6), teardown (8) or
mission water (11). A script that wants to distinguish them cannot, and a
clone must not pass the cause as a third argument.

**Established — which weapon fires.** The death explosion selects
`selfdestructas` only for cause **3** and `explodeas` for every other cause
that reaches it (§12.2); it fires only when the severity byte is strictly
positive and the remaining-build fraction is exactly zero. Since causes 4, 5,
7, 9 and the positive-health case force severity 0, only weapon deaths
(1), self-destruct (3), cargo cascade (6), teardown (8), water (11) and the
network-only causes can detonate, and a unit under construction never does.

**Established — self-damage.** There is no `noselfdamage` key
(`[R-WPN-01 §9]`) and no exemption in the death explosion: the blast record's
shooter is null, so the shooter-exclusion of §9.3 excludes nobody, and the
dying unit's own side takes full damage from its explosion. The dying unit
itself is already latched (and, by the time the corpse is placed, freed), so
its own blast cannot re-kill it.

**Established — who is credited.** Credit is the cause-gated switch above:
the **attacker unit** named by the damage-time snapshot gets its wrapping
kill word incremented, and the **attacker player** its kill counter, only for
causes 1 and 6 (and 5 when the attacker side is neither neutral nor the
victim's own), only when the victim's build fraction is exactly zero, only
when the attacker side differs from the victim's owner, and only when the
attacker side is not the neutral value 10. A cargo unit killed by its
carrier's death credits the carrier's killer; a death explosion's victims
credit nobody (null shooter); a self-destruct (cause 3) credits nobody and
counts only as a loss. The kills>5 and kills/5 consumers of that word are
the census of `[R-DMG-01 §2]`.

**Established — there is no `Unit destroyed` diagnostic.** A whole-image
string census for `destroyed` finds only the two front-end option labels
`Game ends when commander is destroyed.` and `Game continues after Commander
is destroyed.`, read by the options screen (doc 07); the death path emits no
text of its own beyond the localized leader message of §12.1. An
implementation that logs unit deaths does so as its own diagnostic sink, not
as a retail message.

#### Health clamping, damage on a nanoframe, and resurrection against the death pipeline [R-DMG-01 §4]

**Established — the three health writes and their clamps.** Health is a
signed 16-bit field with three writers in the damage family: (a) the heal
kind, `h = health + (uint16)amount` clamped **unsigned** to the definition's
32-bit maximum (§9.1, so a zero maximum heals to zero); (b) the damage kinds,
`health = (int16)(health − (int16)amount)` with **no** clamp for a
locally-owned victim (the negative value is preserved for the severity
computation of §12.1) and a clamp to exactly **0** for a victim whose owner is
absent or remote, which then continues to the callbacks with health zero and
never dies through this path; (c) the paralyze kind, which never writes
health. Construction's direct progress-health writes are owned by doc 05.
Repair computes its terms there and delivers a kind-10 packet through this
early heal arm [05 R-WORK-01 §3].

**Established — damage on a nanoframe.** The dispatcher has no
build-fraction test: a unit under construction takes damage exactly like a
finished one, and a non-positive result latches it. Its death then differs
only downstream: the preamble runs `Killed` normally, the nonzero build
fraction forces the corpse depth to **0** (no wreck), the explosion gate
fails (no blast), the credit block's `buildFraction == 0.0` tests fail (no
kill credit, no veterancy for the attacker, but the victim's player still
counts the loss under causes 1/6), and the cause-5 bounty is scaled by
`1 − fraction`. There is no separate "damage reduces build progress" rule;
build progress and health are distinct fields, and only doc 05's construction
pump moves the former.

**Established (by composition) — resurrection against death, in the same
tick.** Resurrection is a builder work order `[05 "Resurrection"]`, and doc
05's `[R-WORK-01 §7]` states its phases; this document owns only the ordering
against the death pipeline, which follows from the visit order of
`[R-COB-02 §2]`:

* A resurrection completes in the **builder's** slot visit, step 4; a death
  finalizes at step 6 of the **dying unit's** visit; both are inside the unit
  sweep, which precedes projectile advancement. So within one tick, order is
  by unit slot.
* The resurrected unit is created in the resurrector's phase 5 with health
  **1** and remaining-build fraction **0** — a finished unit at one hit
  point. Any damage packet reaching it afterward in the same tick (a later
  slot's death explosion, or the projectile phase) kills it with full credit
  and a corpse, exactly as any finished unit.
* Phase 5 re-reads the terrain cell and fails (`Resurrection failed`) when
  the feature is gone. A corpse consumed by an **earlier-slot** resurrector,
  reclaimed, or destroyed by the previous tick's projectile phase therefore
  loses; two resurrectors on one corpse resolve in slot order, the earlier
  wins, the later fails. A corpse **placed** by a death earlier in the same
  sweep is visible to a later-slot resurrector in the same tick.
* A resurrector that dies mid-order loses the order in the teardown's
  order/queue release; the feature is untouched, and no unit is created.
* Several reclaimers on one unit: their packets dispatch synchronously in
  slot order, so the first packet whose modular subtraction is non-positive
  sets the latch, and the dispatcher rejects every later one; the fatal pulse
  is the earliest in slot order, and its sender is the credited attacker
  (cause 5, subject to the side test of the credit switch).

### 12.2 Wreckage and feature placement

**Established fact:** The corpse step runs when the packet's variant nibble is
nonzero, and takes the nibble as a **depth**:

```
f = definition.corpseFeature                 ; resolved from the authored Corpse name
while (depth >= 2) {
    if (f > 0xFFFA) return                   ; chain ran into a sentinel: no corpse
    depth = depth - 1
    f = featureDefinition[f].featuredead     ; follow the successor link
}
if (f >= 0xFFFB) return
```

so depth one places the authored `Corpse` feature and depth *n* follows
`featuredead` exactly `n − 1` times, stopping on the no-feature sentinel with
nothing placed. A depth of zero never reaches this step.

**Established fact:** Placement uses the victim's stamped anchor cell and the
common feature stamper, which validates the entire footprint, allocates feature
animation state, stamps blocking/filler cells, and notifies path revision.
Blocked or clipped placement fails silently: the engine does not choose a
fallback heap, shift to a nearby cell, retry, or preserve the dying unit.

**Established fact:** Land and water use the same selected corpse definition;
the branch is decided by the **bilinear terrain height** at the victim's exact
16.16 position — the four surrounding plot cells' corner-height bytes
interpolated with truncating divides by 16 — compared against the sea-level
byte:

* `interpolatedHeight > seaLevelByte` — land: place, and keep the caller's
  ground-notification request;
* otherwise — water: place, and when the placement succeeded and the **dying
  unit's** definition does not carry `isfeature`, patch the placed animation
  state's first two motion words to `-11468` and `0`, which is the fixed
  sinking rate; then force the ground notification off.

The ground notification, when it survives, is the ordinary ground effect event
with the wreck effect id and parameter 900. The caller passes it as false for
cause 7, so an immediate feature conversion never raises it either.

**Established fact:** The death explosion runs when the packet's signed
severity byte is **strictly positive** and the remaining-build-fraction float
is exactly zero. It selects between the unit's TWO resolved death-weapon
fields: the self-destruct field when the cause nibble is 3, the explode field
otherwise. Both resolve from the authored `selfdestructas` and `explodeas`
names at catalog load; an unresolved or absent name resolves to weapon
**record 0**, the inactive sentinel (stock `[noweapon]`), which the explosion
path then fires like any other weapon `[R-DMG-01 §5]`.

**Established fact:** The explosion is delivered by building a
projectile-shaped record on the stack and calling the central impact path with
no direct unit. That record carries only: the selected weapon definition; the
current point and the second point, both set to the dying unit's position; a
null target unit; a **null shooter**; and the dying unit's owning-player byte
as the side. Its velocity words and state byte are left uninitialized and are
never read on this path `[R-WPN-02 §5]`. Three consequences follow
from the null shooter and null direct unit, and a clone must reproduce all
three: the impact always takes the **area** path however small `areaofeffect`
is; attacker veterancy is not applied and no kill credit or veterancy accrues
from the blast; and the shooter-feedback helper is skipped. The blast is
otherwise authoritative — armor-scaled area damage, feature damage, sounds and
effects — and is not merely a presentation event. Because the area path
dereferences the shooter without a null test in its interceptor sweep (§9.3),
an `explodeas` weapon carrying the interceptor flag would fault; no stock
weapon does.

**Established fact:** Feature definitions can carry energy, metal, damage, burn
weapon, spark time, flammability, geothermal, blocking, reclaimable,
autoreclaimable, and indestructible behavior. Of those, this document owns only the
two the weapon path reads — the damage capacity the accumulator of §13.1
compares against, and the flammability and indestructible flags that route it;
`[05 "Feature catalog and placement"]` and `[fmt tdf]` own the rest.

#### The death explosion record carries no velocity and no shooter [R-WPN-02 §5]

**Established fact.** The death explosion's impact record is a stack structure
carrying the selected weapon, the victim's position as both the current and
second point, a null target, a **null shooter** and the victim's owning-player
byte; its velocity words are never written and never read. The null shooter is
the load-bearing part: it forces the area path regardless of `areaofeffect`,
suppresses attacker veterancy and the shooter-feedback bits, and would fault in
the interceptor sweep if such a weapon were authored.

#### `explodeas`/`selfdestructas` never resolve to nothing, and the `corpse` key [R-DMG-01 §5]

**Established.** The unit-definition loader resolves each of `weapon1..3`,
`explodeas` and `selfdestructas` by the runtime name lookup — a linear scan of
the 256-record weapon table comparing catalog names case-insensitively, which
returns not-found both for a name that matches nothing and for an **empty**
name — and replaces not-found with a reference to weapon **record 0**
`[02 §5]`. The two death-weapon fields are therefore never null for any
definition that went through the loader, and the death explosion's only
guard (a null test on the selected field) always passes. "No explosion" is the
observable stock outcome, not the executable's rule.

**Established — what fires instead.** In the stock corpus record 0 is
`[noweapon]` (`ID=0` in `weapons/weapons.tdf`): `range=16`,
`[DAMAGE] default=0`, and every other key at its accessor default —
`areaofeffect` 0, no flags, no explosion art, no sounds. The death explosion
pushes that record through the central impact with a null shooter and no
direct unit (§9.1), so retail performs, for every death whose `explodeas` is
absent or misspelled: the follow-camera freeze with a zero `holdtime` and the
record's dead bit (no `noexplode` flag); a camera shake of magnitude 0 and
duration 0; the sound and art selectors of §13.2 handed the sentinel's empty
names; and the area path with `R = 0`, which enumerates the impact cell's
neighbourhood (one cell each way), accepts a unit only when its box distance
is strictly below zero — never, short of the signed 16-bit wrap of §9.3 — and
accepts no feature for the same reason. No damage results and no credit is
possible. Whether the empty-name sound and art selectors emit a null
presentation event or nothing is §13.2's and doc 03's question, not a
simulation difference. Stock FBIs that name a placeholder explosion not
present in any parsed weapon file are the sentinel case; doc 02 owns that
census.

**Established — the second reader of the two names.** The unit-override
loader also reads `explodeas` and `selfdestructas`, but only to exclusive-or
the named section's raw-text checksum into the definition-identity word
(`[02 §5]`, "unit identity contribution"); it resolves no weapon and writes
neither death-weapon field.

**Established — `corpse`.** The FBI `corpse` key is read as a string (up to
100 bytes) and resolved once, at catalog load, to a feature-definition index
by the feature-name lookup; an absent key leaves the index at the all-ones
value `0xFFFF`, which is at or above the no-feature sentinel `0xFFFB`, so the
chain walk of §12.2 places nothing at any depth. An authored name that
resolves to no feature stores whatever the feature-name lookup returns for a
miss, which is doc 05's contract `[05 "Feature catalog and placement"]`. The
index has exactly one reader, the chain walk; `featuredead` links are
followed from the feature table, never re-resolved by name. Wreck-versus-heap
is therefore never a *choice* the engine makes by cause: the cause reaches
the corpse step only as the `notify` flag (false for cause 7) and, through
the `Killed` script's depth nibble, as however deep the script chose to walk
the `featuredead` chain; land versus water is the bilinear-height test above,
and lava has no rule of its own at this site (the feature-side sinking and
lava behaviors are doc 05's).

#### The death blast runs before the corpse is stamped, and nothing else spares the wreck [R-DMG-01 §10]

The timeline of [R-DMG-01 §3] step 3 puts "the death explosion (§12.2)" before
"the corpse (§12.2)". That order is what protects the wreck, and nothing else
does.

**Established — the order, and its immediacy.** The explosion call and the
corpse call are adjacent in the handler: the explosion gate (`severity > 0`
and build fraction exactly zero) is tested, the death weapon is pushed
through the central impact, and the very next statement is the corpse gate
(variant nibble nonzero) and the corpse walk. No teardown, no queue drain
and no feature-phase work intervenes. The blast is fully resolved before the
corpse cell is written: with a null direct unit the central impact always
takes the area path (§9.1); the area path's feature phase damages features
synchronously through the feature damage entry — the weapon's default
damage word accumulates into the cell and the death transition runs inline
when the capacity is reached ([05 R-FEAT-01 §8] steps 6–7) — and only then
does the handler return to stamp the wreck.

**Established — there is no guard on the corpse cell.** The area path's
feature phase enumerates every cell within the blast radius and tests each
feature's reference point against the radius with no exclusion keyed on the
dying unit, its footprint, or the corpse definition (§9.3; the shooter
exclusion applies to units only, and the shooter is null here). The
victim's own occupancy has already been unstamped by the teardown helpers
earlier in the same handler ([R-DMG-01 §3] step 3), so the corpse stamper's
footprint validation is not blocked by the victim either. A feature already
at or around the victim's cell — an older wreck, a tree, a rock — takes the
weapon's full default damage with no falloff and dies when that reaches its
`damage` capacity; an older wreck destroyed this way frees its cells for the
new corpse in the same instant.

**Consequence.** Were the corpse stamped first it would sit at distance
zero from the impact and die on the spot whenever the explode weapon's
default damage word reaches the corpse definition's capacity
([05 R-FEAT-01 §8] step 6). The wreck survives its own unit's blast **by
order alone**. Whether the order was chosen for that reason is not a
question the executable answers; that it is the only mechanism is
Established. An implementation runs the blast to completion, feature
deaths included, and stamps the corpse afterwards; it must not add a
corpse-cell exemption to the blast, which would spare bystanding features
retail destroys.

#### Cause 7 has exactly two producers, both gated on the definition's `isfeature` bit alone [R-DMG-01 §12]

§12.1 lists cause 7 as "written by a DIRECT store of the damage-kind byte plus
the death latch, NOT through the packet builder, from (a) the
spawn-with-parameter branch and (b) the conversion handler gated on the
definition's is-feature bit". **Established (bounded census over the
reconciled export):** exactly two sites store the kind byte `7`, and no
caller of the damage-packet builder passes kind 7, so the packet path never
produces it locally — a kind 7 arriving by network or restored from a save is
the kill service copying the packet's kind ([R-DMG-01 §3]). The two sites and
their gates:

1. **The unit creator, when asked for a finished unit.** The creator's
   finished flag — the argument that seeds the remaining fraction to `0.0`
   instead of `1.0` — selects a post-construction block that runs the
   `activatewhenbuilt` activation and then, when the definition carries
   `isfeature` (capability word A bit 24), stores kind 7 and raises the death
   latch. A nanoframe creation (flag clear) skips the block entirely. The
   capture replacement of [05 R-WORK-01 §15] is one traced finished creation;
   any other caller creating a complete `isfeature` unit takes the same
   branch, and the unit converts on its next sweep.
2. **The build-completion service** ([05 R-WORK-01 §1]'s completion
   transition, also reached from the factory handler and the network
   build-complete path): after the `activatewhenbuilt` activation and the
   local-selection refresh, the same `isfeature` test stores kind 7 and
   raises the latch.

No other gate exists — not health, not the corpse flag, not whether a corpse
feature resolves; the conversion itself (severity 0, variant 1, no `Killed`
query) is §12.1's. An `isfeature` completion and a finished `isfeature`
creation are the only cause-7 writers, the death resolution reads the stored
kind byte, and there is no "an ordinary kill of an `isfeature` unit becomes
cause 7" rule.

## 13. Weapon-driven feature, fire, audio, and effect events

### 13.1 Feature damage and fire

**Established fact:** Feature damage is a separate, much simpler accumulation
than unit damage, and reuses none of §9.2. It is gated first on bit 3 of the
global options word — the same word that carries the double/half damage gates
(§9.2) — and then on the cell resolving to a live feature whose definition does
not carry the indestructible flag. The amount applied is the weapon's authored
**default damage word** exactly: no area falloff, no armor table, no attacker
or defender veterancy, no global double/half gate. The accumulator is the
feature's own, not the weapon's:

```
if (featureDefinition.flammable && weapon.firestarter != 0 && cell has no live instance)
    ignite this cell                                    ; §13.1 fire records
else if (cell has no live instance)
    acc = (uint16)weapon.defaultDamage + (uint16)cell.accumulatedDamage
    if (acc < featureDefinition.damageCapacity) cell.accumulatedDamage = acc
    else                                        destroy the feature at this cell
else if (!featureDefinition.animatedFlag)
    instance = the cell's live animation instance      ; must still anchor here
    instance.damage += (int16)weapon.defaultDamage
    if (instance.damage >= featureDefinition.damageCapacity) destroy at its anchor
```

The static accumulator is the plot cell's anchor-delta byte pair reused as
accumulated blast damage while no live instance is attached
(`[03 §2.2]`), so a feature that is ignited or animated stops
accumulating there. The comparison against the definition's damage capacity is
`<` for "survives" and therefore `>=` for destruction. Ignition takes
precedence over damage: a flammable feature hit by a weapon with a nonzero
firestarter never accumulates damage on that hit.

**Established fact:** Ignition allocates burn state, selects a random spark deadline through one draw of `simulationRandom(half) + half` where `half = sparktimeTicks >> 1` and `sparktimeTicks` is the parsed `sparktime` **seconds × 30, truncated to a 16-bit integer** — the stock value 5 gives 75..149 visits ([05 R-FEAT-01 §9]) — and emits a treeburn/fire event. The feature phase runs every simulation tick; animation advance and burn countdown decrement run every tick; only smoke emission is gated on `globalTick % 3 == 0`.

**Established fact:** Fire spread uses the candidate's spread chance and simulation RNG, scans at most 48 candidates in a 7 by 7 window excluding the origin in row-major order, and makes five cumulative wind-direction attempts that collapse to no draws at zero wind. Drawing occurs only after every cheap legality check (off-map, empty, already attached, not flammable). Spread consults the candidate's own `spreadchance`, never the burning feature's. Burn weapons route back through the ordinary projectile and area-damage subsystem after both spread passes: the burn weapon's impact is built as a synthetic projectile-shaped record with a NULL shooter and a zeroed side byte and pushed through the ordinary area enumeration, so burn-weapon damage awards no veterancy and no kill credit; the friendly/enemy damage-sum classification compares side zero against each recipient.

**Established fact:** A burn ends only when the burn animation finishes. Advancing past the last frame of a non-looping sequence clears the animation pointer, and the same feature visit clears the burning cell and stamps the `featureburnt` successor when one is linked. The countdown fires one spread and burn-weapon event and then stays at zero; it does not end the burn. A looping sequence would burn forever, but the loader forces the runtime loop byte to zero for every shipped burn sequence, so all 79 shipped `seqnameburn` features have finite lifetimes (46-282 visits). Burning filename-based features cannot be reclaimed and are immune to further blast-damage accumulation.

**Established fact:** Document 05 owns feature lifecycle; the reproduction
consumer is recorded here because its draws come from the simulation RNG. The
walker sits at the TOP of the feature phase and visits exactly one cell per
tick in descending order; on wrap its cursor stores width×height−1 and skips
evaluation, so the LAST cell is never scanned. Eligibility requires the
feature anchor index below the reserved sentinel and the cell's animation/status
byte animated bit clear (GAF features at rest).

**Established fact:** The eligibility roll consumes `simulationRandom(100)` EVEN
WHEN the feature's reproduce fraction is zero — stock-inert but RNG-live, since
every shipped feature authors reproduce=0. A passing roll (below the reproduce
fraction) draws `dx = simulationRandom(reproducearea) − reproducearea/2` and
`dz = simulationRandom(reproducearea) − reproducearea/2`, one draw each, for
the spawn offset; the target cell must be in-bounds and free and the source
cell must still hold the reproducing feature. The spawn goes through the
common feature placement helper with no position/velocity override and the
neutral side.

**Unknown:** No geothermal registry exists (footprint validator enforcement).
Remaining fire unknowns are the fire damage-to-unit interactions beyond the
routed burn weapons, and malformed burn cases.

#### There is no `hitdensity` key [R-DMG-01 §6]

**Established.** There is no `hitdensity` string in the image, in any case
form; the key is not read for features, units or weapons. Feature damage is
the unscaled `default` damage accumulated against the feature's damage
capacity above with no density, armor, falloff or veterancy term. The
feature-side residue — the sinking-rate patch's reader, lava cells,
`featuredead` chain authoring and the reclaim/resurrect race on a freshly
placed wreck — is doc 05's.

### 13.2 Sound and smoke events

**Established fact:** **Three** sound identities are read from the weapon
record (`soundstart`, `soundhit`, `soundwater`, each resolved to a registry
index or the all-ones sentinel `[R-WFX-01 §3]`) and each has exactly one
consumer family: the **start** sound is played by the common projectile
initializer at the muzzle point, before the Fire callback (§4.1), and **again
by every burst clone's creation at the parent's position when the weapon
carries the `soundtrigger` flag** (§4.3); the **hit** sound and the **water**
sound are the two arms of the central impact's sound selection below. All are
played through the same emitter with the impact or muzzle point and a zero
third argument (no network broadcast). Start smoke and smoke trail are emitted
at spawner/tick boundaries; end smoke is selected by the impact path.
Sound-trigger burst emissions are separate from Fire/RockUnit callback cadence.

There is no fourth sound identity: `soundtrigger` is flag bit 11 of the
behavior word (§2.2), not a name, and the burst-clone site plays the weapon's
**start** sound `[R-WFX-01 §3]`.

**Established fact:** Start puff (start-smoke flag) emits ONLY from successful
root creation inside the three ordinary spawners — ordinary/direct,
vertical-launch, and ballistic — after Fire and then RockUnit. Burst clones,
dropped-inline allocation, and meteors never emit it; failed allocation emits
nothing.

**Established fact:** Trail puffs (smoke-trail flag plus smoke delay) emit in
the post-collision block for ALIVE records only, never burst parents, gated on
the record being before expiry AND past its next-trail deadline. The deadline
update is ADDITIVE — the smoke delay is added to the deadline — preserving
accumulated debt, so a delay of zero emits on every eligible tick after the
first. Trail emission precedes the water-crossing splash, and both are skipped
when the collision marked the record dead on that visit.

**Established fact:** Timer expiry WITHOUT burn-blow emits exactly ONE
trail-style puff and then retires silently — a flags-only removal with no
sound, no shake, no explosion art, and no damage. Burn-blow expiry routes into
the full central impact instead.

**Established fact:** End puff (end-smoke) is LAND-BRANCH-ONLY in the central
impact and REPLACES the explosion GAF art; sound and damage are unaffected.
Water-branch deaths ignore end smoke entirely.

**Established fact:** Central-impact event order is fixed: (1) camera shake;
(2) impact-sound selection — the weapon hit sound for land and direct-target
impacts versus the weapon water sound for terrain-only water impacts, EXCEPT
that a direct unit target FORCES the hit sound even at or under water;
(3) end smoke when flagged; (4) the land-or-water explosion GAF holder;
(5) damage routing LAST. Authoritative damage therefore lands only after every
audible and visible impact event.

**Established fact:** Explosion and water-explosion identities are selected at
impact. In the ordinary impact branch, no-explode gates ONLY the normal
projectile retirement and follow-camera finalization block — the follow-camera
finalize/clear plus the dead-bit set; it installs no one-shot latch, and shake,
sounds, effects, and authoritative direct or area damage always execute. It is not a
“suppress every explosion consequence” flag. Retirements outside that block
ignore the flag entirely: line-of-sight-family expiry, burst-root completion,
lava-map underwater self-expire, and the off-map exit. A separate water/hazard
override (opaque liquid mode, water-classified cell, no direct unit argument)
retires the record regardless of no-explode.

#### The presentation keys: parse, storage, art binding, and the loop byte [R-WFX-01 §1]

**Established — the keys and their storage.** The weapon record parser reads
the presentation keys in this order and stores them as follows:

| Key | Accessor, default | Stored as | Consumer |
|---|---|---|---|
| `firestarter` | integer, 0 | byte | §13.1 (nonzero test only) |
| `rendertype` | integer, 0 | **byte** | projectile draw dispatch, `[R-WFX-01 §4]` |
| `color` | integer, 0 | **byte** | beam/lightning colour **and** the render-type-4 sequence selector, `[R-WFX-01 §4]` |
| `color2` | integer, 0 | byte | beam second stroke, `[R-WFX-01 §4]`; read by no other case |
| `soundtrigger` | integer, 0 | flag bit 11 | burst clone start-sound replay (§4.3) |
| `explosiongaf` + `explosionart` | string 256, both required | land art holder (sequence pointer) | central impact land arm |
| `waterexplosiongaf` + `waterexplosionart` **or** `lavaexplosiongaf` + `lavaexplosionart` | string 256, both of a pair required | **one** water/lava art holder | central impact water arm; water-crossing splash (§7.3) |
| `soundstart`, `soundhit`, `soundwater` | string 256 | 16-bit registry index, `0xFFFF` when absent | `[R-WFX-01 §3]` |

The byte stores matter: an authored `rendertype` outside 0..7 (after
truncation to a byte) matches no draw case and the projectile is **invisible**
but still gated, simulated, and audible; an authored `color` of 255 becomes
the selector value −1 that suppresses render type 4 entirely (the stock
`earthquake` meteor weapon authors `color=255` under render type 4 and is
therefore never drawn in flight; it also authors no art, so its impacts show
only the calculated flash of `[R-WFX-01 §2]`).

**Established — the lava/water pair is chosen at catalog parse, not at
impact.** The parser tests the session's `lavaworld` OTA value
(`[02 "Weapon record"]`; the key is in `[02 §6 "Map files"]`): when it is zero the
`waterexplosiongaf`/`waterexplosionart` pair fills the single water-or-lava
holder, otherwise the `lavaexplosiongaf`/`lavaexplosionart` pair does; the
other pair is never read. There is no per-impact lava test — the central
impact's water arm (§13.2) is the same "cell height byte below sea level"
predicate on every map, and on a lava world it shows lava art because the
holder was filled from the lava keys when the catalog compiled. This binds
the weapon catalog's compile to the session (doc 02 owns the load order); a
clone that compiles weapons once per process must recompile or re-bind the
holder per mission. Stock content authors a lava pair on 157 of 198 weapons
(`fx/lavasplash`, `lavasplashsm`, `lavasplashlg`), so a map with `lavaworld=1`
shows lava splashes for those and **nothing** for the other 41 (their holder
stays null — the lava pair is absent and the water pair is not consulted).

**Established — art binding and the two fault contracts.** For each present
pair the parser (1) resolves the GAF bank by name through the shared
animation-bank cache — a case-insensitive linear scan of the loaded banks by
name (44-byte entries, name then bank pointer), and on a miss loads
`anims\<name>.GAF` from the VFS and appends it; (2) finds the entry in that
bank by a case-insensitive linear scan of the bank's entry table (the file's
entry order, `[fmt gaf]`); (3) **clears the entry's loop byte** (the low byte
of the entry header's +2 word, `[fmt gaf]`); and (4) stores the entry pointer
in the holder. Two failures are faults, not fallbacks: a bank file that does
not exist makes the bank loader return null and the cache path then shows a
modal message box whose text is the constructed path (`anims\<name>.GAF`) and
exits the process with code 1; an entry name that matches nothing makes the
entry lookup return null and step (3) writes through address 2 — an access
violation. An **empty** `explosiongaf=` value is the first fault (the path is
`anims\.GAF`); an empty `explosionart=` is the second. A pair with only one
key present is not an error: the holder stays null and the impact draws no art
(`[R-WFX-01 §2]`). Because banks and entries are shared objects, the loop-byte clear is
permanent for that entry for the rest of the process, whichever weapon
cleared it.

**Established — the loop byte and the cursor.** Every GAF entry header carries
`1` in its +2 word in every retail file (`[fmt gaf]`); the executable loads
that word's low byte as the entry's **loop** flag, so every sequence loops by
default and only entries whose byte was cleared — by this parser, by the
startup effect-slot binder below, and by the feature loader for burn sequences
(§13.1) — play once. A playback cursor is `{frame index u16, countdown u16,
loop byte, entry pointer}`; its initializer sets the frame to the requested
start (or 0 when the start is not below the frame count), the countdown to
that frame's reference second word (the per-frame hold, `[fmt gaf]`), and the
loop byte from the entry. Each advance: if the countdown is below 2, the frame
index increments; when it reaches the frame count the cursor either wraps to 0
(loop byte nonzero) or **clears its entry pointer** (loop byte zero — the
sequence is finished); otherwise the countdown is reloaded from the new
frame's hold. If the countdown is 2 or more it is decremented. A frame with
hold `h` is therefore shown for `max(h, 1)` advances. Stock effect holds:
`Explosion`/`Explode2`/`Explode3`/`Nuke1` 2, `Explode4`/`Explode5`/`H2oBoom2`
/`H2o`/`lavasplash`/`lavasplashlg` 3, `h2oboom1`/`lavasplashsm` 2,
`CommBoom` 3, `EMPboom`/`Tronboom` 2 (asset census).

**Established — the fixed engine effect-slot table.** At startup, after the
calculated-frame tables (`[R-WFX-01 §2]`), the engine opens the `fx` bank once
and binds these entries, in this order, clearing the loop byte where marked
(×):

| Entry | Loop cleared | Reader |
|---|---|---|
| `smoke 1` | | smoke-puff particles, selector 0 (`[R-WFX-01 §5]`) |
| `smoke 2` | | smoke-puff particles, selector 1 (`[R-WFX-01 §5]`) |
| `fire1` | | **none** — bound and never read |
| `alfboom1` | × | **none** — bound, cleared, never read |
| `radlogo`, `radlogohigh`, `nuclogo` | | HUD stockpile/radar logos (doc 07) |
| `h2oboom2` | × | debris water landing (`[04 R-COB-04 §2]`) |
| `lavasplash` | × | debris lava landing (`lavaworld` twin of the above) |
| `cannonshell`, `plasmasm`, `plasmamd`, `ultrashell`, `plasmasm` | | render type 4 selectors 0..4 (`color` byte), `[R-WFX-01 §4]` |
| `flamestream` | | render type 5 (`[R-WFX-01 §4]`); the strip-5/7 flame families (`[03 R-STRIP-01]`) |
| `explosion` | × | `explode` opcode bit 8 and debris ground landing (`[04 R-COB-04 §4]`) |
| `explode2`, `explode3`, `explode4`, `explode5`, `nuke1` | × | `explode` opcode bits 9..13 (`[04 R-COB-04 §1]`) |
| `shadow` | | the projectile ground sprite of render types 1, 3, 4, 6 (`[R-WFX-01 §4]`) |

Selector 4 of render type 4 is `plasmasm` again — a second binding of the
same entry, so `color=1` and `color=4` draw the same sequence. Weapon
explosion art never goes through this table: each weapon holds its own entry
pointer.

#### Explosion selection at impact, the explosion pool, and the record-0 answer [R-WFX-01 §2]

**Established — the explosion pool is separate from the effect strips.** Every
impact explosion, water splash, debris landing flash, and `explode`-opcode
bitmap explosion is one record of a fixed **300-record explosion pool** (84
bytes per record, live count, first-free append; `[04 R-COB-04 §4]` from the
opcode side). The allocator refuses **silently** when the count is 300: no
art, no calculated flash, and — because the puff below is inside the same
gate — no dust puff either. This pool is distinct from the 400-capped effect
strips (`[03 R-STRIP-01 §1]`, `[R-WFX-01 §5]`); it is not evicting, it is
dropping.

**Established — what one allocation does,** in order, given
`(point, artHolder, tableSelector, waterFlag)`:

1. count++; record position = the point (three 16.16 words);
2. primary cursor: initialized from `artHolder` at frame 0 when the holder is
   non-null; otherwise the primary entry pointer is null (no art);
3. secondary cursor: when `tableSelector >= 0`, initialized at frame 0 from
   calculated-frame table `tableSelector` (0..2, `[04 R-COB-04 §4]`; the
   procedural strips are described below); a negative selector leaves it
   null;
4. **land dust:** when `waterFlag == 0` **and** the point's whole Y word is
   **strictly greater** than the sea-level byte, one smoke-puff emitter is
   spawned at the point on strip 9 with spawn interval 7 and lifetime 15
   ticks (`[R-WFX-01 §5]`) — this is the "above-sea flash" of `[04 R-COB-04 §4]` and the
   "fixed-effect-pool append side effect" of `[03 R-STRIP-01 §1]`; it is a
   smoke emitter, not a flash;
5. the record's debris-piece pointer is set to null.

The callers and their arguments: the central impact passes `(point, land or
water holder, 0, waterCell)` — so **every** projectile impact, land or water,
draws calculated table 0 under its art; the water-crossing splash (§7.3)
passes `(point, water/lava holder, 0, 1)`; debris landings pass table 0 on
ground and `(h2oboom2 or lavasplash, −1, 1)` on water (`[04 R-COB-04 §2]`);
the `explode` opcode's bitmap bits pass table 2 with `waterFlag = 0`. Nothing
passes table 1: the second calculated strip (15 frames, 128 down to 30) is
built, costs CRT draws at every battle entry (§6), and is **never drawn**.

**Established — the record-0 answer** (the `[R-DMG-01 §5]` residual). A
death whose `explodeas`/`selfdestructas` resolved to weapon record 0
(`[noweapon]`: no art keys, no sound keys) reaches the central impact and:
the three sound ids are `0xFFFF`, which the emitter rejects before touching
the device — **no sound event**; the art holder is null, so the allocator
still **consumes one explosion-pool record** (with a null primary cursor,
calculated table 0 as its secondary, and the land dust puff when the unit
died above sea level on land), and the record is reclaimed by the next
explosion sweep once its calculated strip finishes (24 ticks). The observable
retail result of a misspelled `explodeas` on land is therefore a 24-tick
calculated flash disc plus a smoke puff, no named art, no sound. It is a
presentation event, not nothing.

**Established — the calculated (procedural) explosion frames, exactly.**
Three tables are built once per battle, during the world rebuild of battle
entry and on the loading worker's own CRT block ([08 R-ENTRY-01 §2],
[08 R-ENTRY-01 §10]), not at process start (`[04 R-COB-04 §4]` gives the
shape; this is the pixel expression). For a
frame of side `n`: `H = n / 2` (truncating integer, then converted to
double); the frame's x and y offsets are both `H`; the transparent index is
`0xFF`. For row `y` and column `x` (both `0..n−1`):

```
dy = H − y ;  A = dy·dy·1.33            (double)
dx = H − x
r  = (crtRand() · 10) / 0x8000          (integer 0..9, one CRT draw per pixel)
v  = trunc(((r + sqrt(dx·dx + A)) / H) · 32)
c  = (0x20 − v) as an unsigned byte
pixel = c ≥ 0x22 ? 0xFF (transparent)
      : c ≥ 0x20 ? 0x6E                  (v == 0)
      : 0x6F − v                         (1 ≤ v ≤ 32 → 0x6E .. 0x4F)
```

so each frame is an elliptical disc (the vertical axis compressed by
`sqrt(1.33)`) whose palette index runs from `0x6E` at the centre down the
`0x4F..0x6E` ramp to transparency where `r + distance ≥ 33·H/32`, with the
per-pixel draw `r` giving the fuzzy edge. Table 0: 12 frames, sides 64, 60,
…, 20; table 1: 15 frames, sides 128 down in steps of 7 (128 … 30); table 2:
15 frames, sides 200 down in steps of 11 (200 … 46). Every frame's hold word
is 2, so table 0 plays for 24 ticks and tables 1/2 for 30; every table's loop
byte is 0. The draw counts are 23,456, 107,335 and 260,815 CRT draws
respectively — 391,606 **per battle**, drawn on the loading worker thread's
own CRT state during the world rebuild ([08 R-ENTRY-01 §2]), so they never
touch the main thread's CRT stream (`[R-WFX-01 §6]`). A 22×22
displacement ("lens") frame is built beside them for render type 2
(`[R-WFX-01 §4]`); it consumes no draws.

**Established — update cadence and drawing.** The explosion pool advances in
phase 4 of the tick (`[01 §4.4]`: the "general effects" sweep, immediately
after the projectile phase): per record, the debris physics when the record
carries a piece (`[04 R-COB-04 §2]`), then the primary cursor advance, then
the secondary cursor advance; afterwards one stable compaction pass removes
every record whose piece pointer **and** both cursor entry pointers are null.
A record therefore lives for the longer of its two sequences. The draw pass
(frame composer, after the strip-5 and strip-6 draws and the projectile
renderer, `[03 §1]`) makes two walks over the pool: first every record's
**secondary** (calculated) frame through the flash blitter, then every
record's debris model and **primary** (named art) frame through the ordinary
frame blitter — so the named art always composes over the calculated disc.
The only admission test is the screen rectangle (inclusive on all four
edges) at the projected point `(Xword − viewX + 128, (Zword − Yword/2) −
viewZ + 32)`: **explosion art is drawn with no line-of-sight or coverage
gate**, unlike projectiles, puffs, and sounds. The blitters themselves are doc
03's (`[03 §5.5]`).

#### Impact and fire sounds: registry, selection, and the emitter gates [R-WFX-01 §3]

**Established — the sound registry.** A sound name resolves through a
process-wide registry of up to **255** entries: a 32-byte name per entry
compared with a 32-character bounded compare (names longer than 31 characters
alias), a device handle per entry loaded from `sounds\<name>` on first use,
and a parallel 32-byte alias column used by the sound-alias loader (`[03
§8.3]`; the weapon parser passes no alias). A name not yet registered is appended
and its index returned; when the registry already holds 255 entries the
lookup returns **0** — the 256th distinct sound name in a session plays
whatever sound registered first. An absent key stores `0xFFFF`. An authored
but empty name registers the empty string (the file `sounds\` fails to load
and the handle is null); what the device layer does with a null handle is
**Unknown** (decider: static trace of the device play routine with a null
handle) — no stock weapon authors it.

**Established — the emitter, in order.** `play(id, point, broadcast)`:

1. `id == 0xFFFF` → return, nothing else happens (this is the record-0 path).
2. Under the Windows-sound option the emitter takes an alternate path that
   plays the handle with **no position gate**; the DirectSound path continues.
3. Requires the sound system initialized, the effects-volume field nonzero
   (its low three bits), and DirectSound available; otherwise return.
4. `broadcast != 0` would send an 18-byte network sound packet; every weapon
   caller passes 0, so weapon sounds are never broadcast.
5. The point must resolve to an on-map plot cell (`cellX = X >> 20`,
   `cellZ = Z >> 20`, truncating toward zero for negatives); off-map → no
   sound.
6. **Line-of-sight gate for the local viewing player:** tile
   `(Xword >> 5, (Zword − Yword/2) >> 5)` must be inside the viewer's grid
   and, in byte-grid mode, hold a nonzero current-coverage byte, or in
   word-mask mode hold the viewer's bit — the same one-point gate the
   projectile renderer applies (`[03 §5.4]`). A hit you cannot see is silent.
7. The handle is then played, positionally when the 3-D listener is
   available (listener at the screen centre, world scaled by 16 per cell),
   otherwise flat. `[03 §8.3]` owns mixing and slot arbitration.

**Established — the three weapon sounds' call sites and points.** `soundstart`
at the muzzle point by the common initializer, and at the **parent's current
position** by each burst clone when `soundtrigger` is set (§4.3); `soundhit`
at the impact point in the land/direct arm; `soundwater` at the impact point
in the terrain-only water arm. No other site reads any of the three. A
non-explode impact still sounds; expiry retirements never sound (§7.3). The
in-game sound-alias table and the network receive path (which plays a
received id at a received point) are outside the weapon contract.

#### Projectile render types, exactly, and which weapons author them [R-WFX-01 §4]

**Established — dispatch.** The projectile renderer walks the pool in index
order and, for every record with a zero burst-remaining word, applies the
one-point coverage gate at the record's current point (`[03 §5.4]` states it)
and then dispatches on the definition's `rendertype` **byte** by equality
against 0..7; any other value draws nothing. Screen projection everywhere
below is `sx = Xword − viewX + 128`, `sy = (Zword − Yword/2) − viewZ + 32`
(the composer's half-height shear, `[03 §1]`); "palette(c)" is the
256-entry logical-to-physical remap table of the current palette (`[03 §4.3]`)
indexed by the authored byte.

| Type | Draws | Stock authors (asset census, 198 weapons) |
|---|---|---|
| 0 | line from the current point to the tail point in `palette(color)`; when `color2 != 0` a second one-pixel stroke in `palette(color2)` is drawn **first**, offset one pixel (horizontal-major lines: both endpoints one pixel up after sorting by x; vertical-major: endpoints shifted −1/+1 in x after sorting by y), then the `color` stroke on top | 34 `lineofsight+beamweapon` lasers (colors 96/98, 144/217, 208, 209/211, 232/234) and 4 with no family (`noweapon`, `gasbag`, `treeburn`, `shrubburn`) |
| 1 | the `shadow` frame at `(sx, sy_floor)` where `sy_floor` uses the record's cached average floor height (§8.1, halved) instead of the projectile's own Y — the ground shadow — then the definition's 3DO model at the point with angle block `{roll word, yaw − 0x8000, pitch − 0x8000}` — the roll word is the record's first orientation word, the one meteors accumulate (§6.5) and no creator or the common initializer writes (**Supported inference:** a non-meteor model is drawn with whatever roll its pool slot last held; decider: writer census of that word); when the model has a child piece and `currentTick < expiry`, the child is drawn with the same block, except that under `propeller` the first word is the record's spinning propeller angle (+0x400 per tick, §7.1) | 68: every `selfprop` missile and torpedo (33 `los+selfprop`, 16 `+propeller`, 13 `vlaunch`, 4 `vlaunch+propeller`) and the two model meteors |
| 2 | the 22×22 displacement (lens) frame at `(sx, sy)` through the lens blitter; the screen-rect admission failing here **returns from the whole renderer** (later records are skipped that frame, `[03 §5.4]`) | 1: `mindgun` |
| 3 | `shadow` as type 1, then the model with an angle block this rendering path does not initialize (**Supported inference:** uninitialised stack — decider: a bounded writer census for those orientation values) | 2: the disintegrators |
| 4 | `shadow` as type 1, then frame `(currentTick − creationTick) mod frameCount` of the fx entry selected by the `color` byte: 0 `cannonshell`, 1 `plasmasm`, 2 `plasmamd`, 3 `ultrashell`, 4 `plasmasm`; `color == 255` (−1) suppresses the whole case; 5..254 draw nothing; the modulus is a signed 32-bit remainder | 82: 72 `ballistic` shells (color 0, i.e. `cannonshell`; `armflak_gun`/`corflak_gun`/`emg`-class use 1 or 2), 9 `lineofsight` guns, and `earthquake` (color 255 — never drawn) |
| 5 | frame `f = N − ((expiry − currentTick) · N) / weapontimer` of `flamestream`, `N` its frame count (20), signed 32-bit arithmetic, drawn only while `0 ≤ f < N`; a zero `weapontimer` divides by zero | 1: `flamethrower` |
| 6 | `shadow` as type 1, then the model with the record's orientation words verbatim `{roll word, yaw, pitch}` (no `0x8000` offsets) | 4: the `dropped` bombs |
| 7 | two passes of jagged segments from the tail point to the current point in `palette(color)`, single stroke (`color2` is authored on both stock lightning weapons and **not read**): segment count `n = trunc(dist / 5)` where `dist` is the truncated 16.16 length of the tail→head vector: `nFixed = (dist << 16) / 0x50000` (64-bit, a 16.16 count) and `n` is its whole part; when `n` is zero nothing is drawn; each pass steps `(delta << 16) / nFixed` per axis (64-bit truncating divisions), and after each step **each of the three axes** receives `crtRand() · 11 / 0x8000 − 5` whole world units added to the stepped point's high word — **three CRT draws per generated point, `2·n` points**, so `6·n` draws per lightning record per frame; each jittered point is the end of one segment and the start of the next | 2: `lightning`, `armlatnk_weapon` |

**Established — the families are not the types.** `rendertype` is an authored
byte with no default other than 0 and no relationship enforced against the
behavior flags; the table's right column is what stock content does, not a
rule. A `beamweapon` authored with `rendertype=1` would draw a shadow and a
model (a null model pointer would then fault in the model drawer). The
simulation reads none of `rendertype`, `color`, `color2` (§6.10).

**Established — muzzle flash.** There is no `flash`, `muzzleflash` or similar
key in the image and no muzzle-time sprite producer in any weapon path: the
only muzzle-time presentation events are the `startsmoke` puff
(`[R-WFX-01 §5]`), the `soundstart` sound (`[R-WFX-01 §3]`), and whatever the
unit's `Fire*` script emits through `emit-sfx` (`[04 §4.4]`,
`[03 R-STRIP-01 §1]`).

#### Smoke puff parameters per producer [R-WFX-01 §5]

The puff family's mechanics — pool, 400-cap eviction, per-tick spawn gate,
wind drift, CRT draws — are `[03 R-STRIP-01 §1–§3]`; §7.3 owns the trail
cadence. This section pins the **parameters** each weapon-side producer passes,
which doc 03's census does not itemize. Every weapon puff
goes to **strip 9**. The emitter's init takes `(point, frameCap, spawnInterval,
frameHold, lifetime, smokeSelector)`; particles use `smoke 1` (selector 0, 12
frames, hold 5 in the file — **unused**: the particle's hold comes from the
producer) or `smoke 2` (selector 1); the particle's last frame is
`min(frameCount − 1, frameCap)` when `frameCap` is nonzero; `frameHold` 0
means 7.

| Producer | Args after the point | Effect |
|---|---|---|
| trail puff (§7.3), timer-expiry puff (§7.3), `endsmoke` at impact (§13.2), COB `emit-sfx` `0x101` white smoke (`[04 §4.4]`) | `(0, 1, 0, 0, 0)` | one particle at spawn, all 12 frames of `smoke 1`, hold 7; the emitter's lifetime is 0 so its spawn window closes immediately and it dies when the particle expires |
| COB `emit-sfx` `0x102` black smoke (`[04 §4.4]`) | `(0, 1, 0, 0, 1)` | the same one-shot shape with selector 1: one particle at spawn, every frame of `smoke 2`, hold 7 |
| `startsmoke` (§4.1, muzzle point) | `(3, 1, 30, 0, 0)` | one particle, frames 0..3 of `smoke 1`, hold 30 — a slow four-frame puff |
| land dust of every above-sea explosion (§2) | `(0, 7, 0, 15, 0)` | one particle at spawn and one every 7 ticks while `nextSpawn ≤ now + 15`: three particles, all frames, hold 7 |

**Established** (the emitter's spawn loop and the particle update; the family's
record is `[03 R-FX-02 §3]`, whose reading this states for the weapon-side
producers). Per particle: at spawn the emitter draws one CRT value for the
particle's **last frame**, `crtRand() · (lastFrameBase − 2) / 0x8000 + 2`,
and sets the first countdown to `hold` directly — the first countdown is
**not** random. Every tick the particle moves by `windX · 8`, `+gravity · 4`
in Y (upward), `windZ · 8`, decrements the countdown, and at zero advances one
frame and redraws `crtRand() · (hold/2) / 0x8000 + hold/2`; it is removed when
its frame index reaches its last frame. Each particle is drawn as the selected
frame at its projected point with **no** coverage gate of its own — this
family's draw walk tests nothing before blitting, unlike the flame and
sprinkle families `[03 R-FX-02 §3]`. `lastFrameBase` is the emitter's
frame-limit field: the bound entry's frame count **less one**, clamped by the
table's `frameCap` when that is nonzero — so with `frameCap` 0 a puff's life
is `crtRand · (frameCount − 3) / 0x8000 + 2` frames `[03 R-FX-01 §3]`.
Exhaustion of the shared strip pool drops the puff silently; a root flag byte
disables every strip allocation
(`[03 R-STRIP-01 §1]`).

The Y multiplier here is **4**, not the 16 of the geothermal vent's class: the
two updates are identical instruction for instruction except the shift on the
gravity word — two for the strips-5/9 puffer every producer in the table above
uses, four for the vent — so a weapon-side puff rises at a quarter of a vent
plume's rate. Both vtables are tabulated in [03 R-FX-01 §3 addendum §B].

**Established — how `frameCap` and the selector reach the record.** The
emitter's virtual initializer takes **six** arguments,
`(point, frameCap, spawnInterval, frameHold, lifetime, smokeSelector)`, exactly
the row this section's table gives. The init clamps the bound entry's frame
count less one by `frameCap` when that is nonzero, stores the selector, and
picks `smoke 1` or `smoke 2` from it. There are no producer-side field writes;
every producer in the table passes all six as literals.

**Established (direct static) — which `emit-sfx` smoke arm uses which row.**
The effect opcode's dispatcher ([04 §4.4]) has two smoke arms and each calls
its own producer: `0x101` (white smoke) calls the producer the trail,
timer-expiry and `endsmoke` puffs share, whose six init literals are
`(point, frameCap 0, interval 1, hold 0 → 7, lifetime 0, selector 0)` →
`smoke 1`; `0x102` (black smoke) calls a second producer whose literals differ
from the first in the selector alone — `(0, 1, 0, 0, 1)` → `smoke 2`. Both arms
push strip 9, as doc 03's strip census records ([03 §5.5], strip-9 row).

#### The presentation RNG census [R-WFX-01 §6]

**Established.** Every random draw made by weapon presentation is from the
**CRT** stream (`x' = x·214013 + 2531011`, bits 16..30 — `[01 §7]`); no
presentation path touches the simulation Park–Miller stream:

| Site | Draws |
|---|---|
| calculated explosion frames, once per battle on the loading worker's CRT state ([08 R-ENTRY-01 §2]) | 391,606 (tables 0/1/2: 23,456 / 107,335 / 260,815) |
| lightning (render type 7), per record per **frame** | `6 · trunc(dist/5)` |
| smoke puff, per particle | 1 at spawn, 1 per frame advance |
| camera shake, per sub-tick while active | 2 (`[01 R-CORE-01]`) |
| explosion pool update and draw, sound emitter, render types 0–6 | 0 |

The simulation-stream draws that *look* presentational are not: the `explode`
opcode's debris velocities (`[04 §4.5]`, `[04 R-COB-04 §1–§3]`) and the
feature-fire spread (§13.1) are simulation-side and lockstep-visible. Because
the lightning jitter is drawn per **rendered frame** and the smoke puffs per
**tick**, a clone that renders at a different frame rate diverges in CRT
stream position from retail — which is harmless, since the CRT stream carries
no authoritative state (`[01 §7]`).

### 13.3 Presentation boundary

**Established fact:** The simulation publishes model/effect/sound identifiers, impact positions, feature/fire state, and camera-follow state. The renderer consumes those events later. The explosion pool (300, dropping) and the effect strips (401, evicting) are the two presentation containers those events land in; `[R-WFX-01 §2]` and `[R-WFX-01 §5]` name what each weapon event puts where.

**Unknown:** the lens blitter's pixel mechanics for render type 2, the flash and frame blitters' pixel rules, and 3DO model orientation from the angle blocks are doc 03's (`[03 §5.2]`, `[03 §5.5]`, `[03 §4.4]`); the render-type-3 angle block and the null-handle sound case are the two residuals listed in the tail.

## 14. Evidence basis

The combat sections above were derived only from these areas of the retail
executable:

- the tick-phase, wall-clock, entity-identity, queue, and pool machinery;
- the weapon-slot, targeting, firing, and reload machinery;
- the projectile pool, its family creation and motion dispatchers, trajectory
  integrators, guidance, beams, timers, and compaction;
- the impact, armor, area-damage, veterancy, paralyzer, stockpile,
  interceptor, and death paths;
- the economy settlement path, for stockpile production, remainder, and
  refresh behavior;
- the feature placement, wreckage, fire, and sinking paths;
- the script callback dispatcher, for weapon callback timing only.

For projectile allocation and lifetime, the count-writer census and the raw
allocator, updater and compactor control flow are the evidence of record.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body and are not restated here.

### Catalog and targeting

- **Unknown:** the complete free-writer set for fields read by autonomous
  retention through a stale raw unit target. The raw resolver performs no
  liveness check, but Nanolathe's compact freed record currently preserves
  only death-packet fields and resolves this retention case as absent.
  [06 §3.2] · trace all free/finalizer stores affecting owner, definition index
  and stunned state before extending retained storage.

- The authored key or writer behind the one global option bit of the
  acquisition filter's second admission (the third disjunct beside `shootme`
  and the computer-controller term) · §3.2 · static trace of the options
  loader. The other keys that bullet asked for are named:
  `istargetingupgrade` arms the secondary-list gate ([04 R-SPEC-01 §8]),
  `shootme` is the candidate flag ([04 R-SPEC-01 §5]) and `kamikaze` the
  shooter's bypass ([04 R-SPEC-01 §1]).
- Whether any writer of the runtime *seen* status bit exists outside the
  recovered five-pass sensor phase (`[03 R-VIS-01 §4]`), and the complete
  sonar and jammer interactions on the presentation surfaces · §3.1,
  doc 03 §3.2/§3.4 · static trace over the unrecovered regions.
- Manual unit and point target encoding, command-fire replacement, and the
  full set of manual-versus-autonomous latch callers · §3.2, doc 07 · static
  trace.
- What a freed unit record reads back as before the next allocation overwrites
  it — specifically whether the death teardown clears the record's definition
  pointer, its definition-index word or its activation byte, or leaves the
  record intact. It decides what the target registry's third list offers for a
  pad destroyed inside a rebuild window, since that list holds raw record
  addresses and the scan re-tests only the three admission flags · §3.1, doc 04
  `[R-AIR-01 §11]` · static trace of the death teardown's record release; the
  world-teardown sweep's "definition index nonzero" test is the only free-record
  marker located so far, and no store that clears it was found.
- Acquisition bypasses and category behavior for non-unit target types · §3.3
  · static trace.
- Whether the ballistic solver's `acos` argument can exceed one on malformed
  authored or network input, what the runtime returns then, and whether the
  resulting unordered angle comparisons really accept and serialize it · §3.3
  · static trace of the runtime `acos` domain path plus a reachability argument
  over the root expression.
- Malformed-state interactions around the closed family readiness gates ·
  §3.4 · static trace. Target replacement during an outstanding Aim is closed
  by [04 R-CB-01 §6]: the aim issue clears the slot's aim-state word and
  restarts the deferred callback, readiness is granted only by a nonzero value
  through the slot's completion receiver, and the issue bit gates re-issue,
  not readiness.
- Boundary between the general muzzle query and the per-family dropped/meteor
  muzzle paths, and the side effects of the shared muzzle fallback on
  malformed piece indices · §3.4 [R-P0-07] · static trace. Medium confidence
  today.

### Projectile pool and phase

- Any nonstandard snapshot policy for projectiles, burst state, and
  follow-camera references; standard battle save/load is closed · §5, doc 08 ·
  static trace.
- Failure side effects of specialized allocation callers outside the reviewed
  creation dispatch · §5 · static trace over the unrecovered regions.
- Gameplay consequences of the unit-death cleanup skip when adjacent burst
  schedulers share one shooter · §5 · static trace.
- Reachability and effects of a record marked dead before its captured-span
  turn; the updater has no dead-bit filter at loop entry · §5 · static trace.
- Stale unit and target pointer validation, and slot-reuse aliases outside the
  closed damage-packet identity rules · §5, §9 · static trace.
- Integer overflow, negative timer, and zero-speed burst combinations outside
  the closed ballistic cases, the §7.3 wrap edges (which now include the
  zero-extended negative `turnrate`, the sub-two RNG bound, and the range
  promotion overflow) and the pool-full retention matrix · §7 · static trace.

### Projectile families

- Malformed network pitch and velocity inputs to the ballistic creator, and
  exceptional floating-point inputs beyond the enumerated integer cases · §6.1
  · static trace.
- Semantic names of the two-phase state bits, and any writer besides the
  §6.6 expiry transition and the common initializer's clear; plus unusual flag
  combinations and wrapping flight-time deadlines · §6.6 · static trace.
- The geometric intent of the ballistic launch's vertical pre-decrement by one
  whole flight time's worth of gravity, and therefore whether an implementation
  may restate it · §6.4 · manual retail observation of a stock ballistic
  weapon's apex against the literal expression, run as an authored `probes/`
  scenario. The arithmetic itself is Established.
- Stale-reference behavior for retained units, and every producer and lifetime
  invariant of the optional projectile-to-projectile link · §6.3 · static
  trace.
- External meteor-removal paths beyond ordinary collision and map exit · §6.5
  · static trace.
- Malformed beam duration and deadline arithmetic · §6.4 · static trace.
- The later-tick contact cadence for no-explode records across changing
  geometry and family states · §6.4 · static trace. Predicate-driven with no
  latch, so this is a state-search bound rather than a contract gap.
- Authored relationships among beam, lightning render type, flame,
  firestarter, burn-blow, and no-explode; no separate lightning or flame
  collision integrator was found · §6.4 · static trace.
- The render-type-3 (disintegrator) model angle block: the recovered draw
  case passes a block no instruction writes · `[R-WFX-01 §4]` · disassembly
  of that case for a store into the block (Supported inference today:
  uninitialised stack).
- The roll word of non-meteor projectile records, read by the model render
  types and written by no creator · `[R-WFX-01 §4]` · writer census of the
  record's first orientation word.

### Collision and damage

- Malformed feature-sentinel behavior at the collision gate's fringe
  resolution, when the anchor deltas address a cell outside the map · §8.1,
  doc 03 · static trace.
- Reachability of the interceptor sweep's unguarded shooter dereference: it
  faults for any impact record with no shooter whose weapon carries the
  interceptor flag (a death explosion or a routed burn weapon), and no stock
  weapon authors that combination · §9.3, §12.2 · asset census over the weapon
  corpus, then static trace if one exists.
- Quantization and overflow of the repeated-feature-cell cache at negative or
  extreme coordinates · §8.2 · static trace.
- Sign and scale conventions for vertical velocity, terrain height, and sea
  level outside ordinary map ranges · §8.2 · static trace.
- A guarded error path for the unguarded unsigned divisions when maximum
  health is zero; stock never authors zero · §9.1 · static trace. Marked
  `TODO(T25)` at two sites.
- Whether ordinary local reclaim can reach a freed or reused fatal attacker
  slot; raw reconstruction and payment behavior are established · §12.1,
  doc 05 [R-WORK-01 §4] · static analysis of reclaim and death scheduling.
- Practical reachability of signed 16-bit AOE distance wrap, and of more than
  20 unique unit or 64 unique feature-cell candidates, in accepted retail maps
  · §9.3 · asset census over the map corpus (the AOE dedup map probe).
- What the sound device layer does with the null handle that an authored
  **empty** sound name registers (the record-0 sentinel is not this case: its
  ids are `0xFFFF` and are rejected before the device) · `[R-WFX-01 §3]` ·
  static trace of the device play routine with a null handle; no stock weapon
  authors an empty name.
- The feature-name lookup's return for an authored `corpse` name that
  resolves to no feature (stored once at catalog load, read only by the
  chain walk) · §12.2, doc 05 · static trace of the feature-name lookup's
  miss path (doc 05 owns the lookup).

### Stockpile and interceptor

- Malformed slot-byte overflow, cancellation interaction with admitted carry,
  repeat requeue, and save/load beyond the established queue count, progress,
  and slot-byte persistence · §11 · static trace.
- The multiplayer index-versus-pointer anomaly and other interceptor failure
  modes beyond the closed single-process coverage square, reservation at
  spawn, current-point tracking, linked contact, explosion sweep, and
  pending-shot failure path · §11 · static trace. Out of Nanolathe's
  implementation scope (no multiplayer).

### Features and effects

- Remaining geothermal and malformed burn cases beyond the established shipped
  filename-based extinction, finite lifetimes, 48-candidate neighborhood,
  smoke-only gating, one-shot event, reclaim rejection, blast immunity, and
  the §13.1 reproduction walker contract · §13.1, doc 05 · static trace.
- Feature damage and armor interaction, and burn damage to units, beyond the
  closed burn-weapon attribution · §13.1 · static trace.
- Pixel rules of the lens (render type 2), flash, and frame blitters, and 3DO
  orientation from the angle blocks · doc 03 §4.4, §5.2, §5.5 · doc 03's
  scope; `[R-WFX-01]` closes what each weapon event passes to them.
