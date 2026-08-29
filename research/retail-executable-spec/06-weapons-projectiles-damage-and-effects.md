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

**Established fact:** The weapon record's slot in the catalog is selected by its authored `ID` key, read as an integer with a default of -1 before any other field. The section name is stored into the selected record as the weapon's catalog name, and a separate `name` key supplies the display string. An earlier reading that `ID` is unread by this executable is incorrect.

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

### 3.1 Target categories

**Established fact:** Unit definitions carry category bitsets for the three
weapon slots and for no-chase behavior. During ordinary acquisition, a
candidate clear of the slot's bad-target-category mask enters a preferred
bucket; a matching candidate can enter a fallback bucket. Any preferred result
wins over fallback. Retention is stricter and rejects a retained unit whose
category is in that bad-target mask. Unit traversal naturally excludes map
features because features are stored in a separate feature system.

**Established fact:** Acquisition-time physical admission is separate from
retention and firing. Non-water weapons require shooter and candidate reference
heights above sea level, enforce the to-air target-status class when requested,
optionally require a ballistic solution, and then test planar range. Water
weapons apply two candidate depth/type predicates and planar range. Some
definition/controller branches can bypass parts of this gate.

**Established fact:** Automatic candidates are drawn from the live per-player
unit lists, which are maintained continuously by allocation and release rather
than rebuilt on any timer. The candidate array itself is built fresh on every
acquisition attempt from those lists, with planar distance and a
direct-visibility predicate as admission. That predicate accepts own-side
units, rejects cloaked units, rejects underwater units without their dedicated
status bit, and samples multiple target-bounds points against the player's
visibility state. The autonomous scan is a round-robin walk over the owning
player's pool that visits about one thirtieth of the pool per tick, so a given
slot is rescanned roughly every 30 ticks; the order-work acquisition path
builds its sight-distance candidate list per call. An earlier reading that the
candidate lists themselves are "rebuilt on a cadence of at least 30 ticks" is
corrected: the 30-tick cadence is the scan throttle and the unrelated
per-unit state refresh, not a candidate-list rebuild.

**Supported inference (unproven sensor gate):** An earlier reading that a
secondary status list is consulted only when the primary in-radius set is
empty and the owning player has an active targeting-upgrade aggregate supplied
by an active allied or same-player unit with a corresponding definition flag
remains unproven: no test of such an aggregate exists in the recovered
acquisition functions (bounded search over the scan, the acquisition, the list
builder, the range helper, and the per-slot pipeline, which performs no
acquisition of its own). Its exact sensor name is not proved.

**Established fact:** Retained-target checks do not rerun visibility, sensor,
range, medium, aircraft, or ballistic acquisition tests. Shot-time admission
also does not test category, alliance, radar, sonar, cloak, or jammer. Sensor
state controls list entry; it is not a universal per-shot revelation or
revalidation rule.

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
  without locally applying those same tests.

All paths still reach the common shot-time physical gate and the selected
projectile family's readiness gate. Forced/manual installation can therefore
bypass autonomous visibility lists and bad-category preference, but it does
not bypass every firing check.

**Established fact:** Ordinary acquisition builds a filtered candidate set in
stable order: it iterates candidates in ascending player and then pool-slot
order, requires hostility, applies a direct-visibility predicate (own-side
units bypass the check, cloaked units are rejected, underwater units without
the dedicated alias flag are rejected, otherwise a four-point hull sampling
of the target bounds is tested against the player's visibility state), and
then applies physical admission (height above sea level, to-air and
ballistic feasibility, and planar range). Only candidates passing all three
groups enter the filtered set, which retains that stable order.

**Established fact:** The filtered set is then sampled by swap-remove random
selection: the engine repeatedly draws from the simulation stream with a bound
equal to the remaining candidate count, removes the picked entry by swapping
the last element into its place, and repeats until the array is exhausted or
50 picks have been made. A draw with bound one returns zero without advancing
the stream, so a set of N at most 50 candidates consumes N draws of bounds
N down to 1, of which N minus one advance the stream; a larger set consumes 50
draws of bounds N down to N minus 49. An earlier reading that a set of 50 or
fewer candidates consumes no sampling draw and preserves the filtered stable
order is corrected: the sampled order is always RNG-driven. The candidates are
then split into two buckets: preferred (category not intersecting the weapon's
bad-target mask) and fallback (matching the mask). No winner in the preferred
bucket is retried as a failure of the whole acquisition; fallback is only
consulted when preferred yields nothing, and when both exist preferred always
wins over fallback.

**Established fact:** Within each bucket every sampled candidate receives one
score. The bound for the draw is the sum of the high 32-bit halves of the
squared fixed-point X and Z deltas from shooter to candidate; formally it is
the high half of the 64-bit product of each delta with itself, shifted right
by 32, summed across the two axes. If the bound is below two, the RNG helper
returns zero without advancing the stream. Otherwise the engine draws from the
simulation stream with that bound. Strictly lower score wins, so an equal
score preserves the first sampled candidate in that bucket. This is not
nearest-target selection; candidate-list order, sampling, and RNG draw order
are authoritative.

**Established fact:** Shot-time admission, which is checked immediately before
the spawner, never tests category, alliance, radar, sonar, cloak, or jammer.
Those gates belong only to candidate-list building, not to the final firing
check, and this absence is established by a bounded search over the
shot-time admission code.

**Established fact:** The automatic retention scan rechecks hostility,
bad-target-category rejection, and the paralyzer already-stunned exclusion.
It otherwise keeps the current live target without re-running acquisition-time
physical or sensor gates. TargetCleared is emitted for stale/dead resolution,
scanner failure, automatic-targeting enable/disable transitions, STOP, and the
identified order-cancellation path. Replacing a target does not clear the
Aim-request latch, so a replacement can skip a fresh Aim request.

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
conversions are owned by `[02 "Weapon record"]` and were re-derived unchanged
by this pass.

#### Shared trigonometry — Established

Three helpers carry every angle-to-vector conversion in the weapon and
projectile code, and an implementation must reproduce them exactly because
their rounding is visible in world positions.

* **Scaled sine** `sin(angle, magnitude)`: index `((int16)angle + 32) >> 6`
  masked to the even byte offsets `0..1022`, so the table is **512 signed
  16-bit entries per circle** — one entry per 128 angle units, not per 64 (a
  correction, see `[R-WPN-01 §4]`). Entry *k* holds `round(8192 × sin(2πk/512))`.
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
family that requires a result. (Supersedes the earlier reading that completion
depends only on an explicit script return: a missing or blocked Aim script
delivers zero through the same receiver.)

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
gate. **Correction:** earlier text called the 150 case "the non-air class".
There is no class or category-mask test here; the field is the movement tier, so
the tight gate applies to a stationary (or carried, or inhibited) unit of any
kind and the loose gate to any unit whose tier is 1, 2 or 3 `[R-WPN-01 §1]`.

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

**Established fact:** The simulation keeps three distinct visibility-like
layers. The authoritative word mask is a 16-bit word per map tile quarter, each
bit representing one player. The per-player byte grid is a wrapping refcount
per tile quarter. Both are authored only by the line-of-sight publisher. The
minimap radar surfaces (a final image and its temporary and mapped companions)
are wiped every tick and receive radar, sonar, and jammer circles; they are
presentation only. The publisher chooses between a GAF shape indexed by the
floor of radius divided by 32 minus five and a ray shape indexed by radius
divided by 32, selected by a global mode bit. Outer radius for normal circles
is the maximum of radar distance and sonar distance as a single geometric
circle. Jammer circles for radar-jam and sonar-jam distances use separate tables
and are drawn onto the same final surface; overlap is last-writer-wins and
never ORs into the word mask, so jamming has no authoritative effect beyond
presentation.

**Established fact:** The sensor bookkeeping phase runs only when more than one
player is active. When it runs it performs four passes in order: it clears the
decloak bit and sets friendly bits including the dedicated alias that doubles as
the underwater visibility exemption, it publishes normal and jammer circles to
the minimap surfaces only, it scans for cloaked candidates whose distance to any
enemy is less than or equal to minimum cloak distance and on a hit writes a
deadline of global tick plus 90 and sets the decloak runtime bit, and it sets
the seen marker through a four-point line test. The four-point test projects
world X and Z and a half-height adjusted Y into tile coordinates, checks unsigned
bounds, and then tests either the byte grid or the word mask according to the
global mode. Cloaked units are rejected by an early predicate unless the
decloak bit is set, and no firing path clears cloak — the bounded search found
no firing decloak. Allied vision is never ORed: the writer ORs only the source
player's own bit and the reader tests only the local player's bit. Overflow
projectiles from occupancy are capped as described under damage; the same
overflow handling applies here.

**Established fact:** The secondary radar-like list is consulted only when the
primary filtered set is empty and a targeting-upgrade aggregate is present from
an active allied or same-player unit. If primary candidates existed but all
failed category or scoring, the secondary list is not retried, and its sensor
flag name remains a supported inference rather than an established literal.

### 3.4 The weapon-query path [R-P0-07]

**Established fact:** The slot selects its pieces through four query jobs that
must not be collapsed into a single "muzzle piece" lookup:

1. QueryPrimary, QuerySecondary, or QueryTertiary supplies a fallback muzzle
   piece and is synchronous.
2. AimFromPrimary, AimFromSecondary, or AimFromTertiary supplies the piece from
   which the weapon aims and is synchronous. Its sentinel is −1; only that
   sentinel invokes the matching Query fallback.
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
AimPiece(k):
    piece = -1
    AimFrom[k](piece)             // mode Q, cell 0 seeded -1
    if piece == -1:
        piece = 0
        Query[k](piece)           // mode Q, cell 0 seeded 0
    return piece
```

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

#### Per-attempt ordering and piece transform — Established [R-P0-07]

**Established fact:** The recovered weapon path runs the following stages in
order:

```text
resolve/retain target
  -> compute target point (SweetSpot where required)
  -> compute AimFrom[k], then Query[k] only when AimFrom returned -1
  -> transform the selected piece through current COB state and the model hierarchy
  -> solve heading/pitch and physical range/medium admission
  -> start Aim[k] when the weapon family requires it
  -> reserve the root projectile
  -> start sound
  -> start Fire[k]
  -> start RockUnit
  -> start smoke/trail presentation
```

The muzzle query is synchronous before root projectile initialization, and the
selected piece identity is stored on the root projectile so a later burst
attempt can refresh its world position from the live piece. Allocation failure
does not retroactively call Fire or RockUnit. The physical admission stage is
the squared-planar-range and medium gate of §3.3; it is not a visibility or
terrain-hill test.

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
fixed-forward flag is set, the issue bit is clear, and there is no live tracked
target (or the adjacent slot status selects the fixed branch).

**TODO(question):** Locate the Aim-completion closure writer/consumer at the
weapon-slot receiver. The zero/nonzero delivery and no-timeout contract is
established, but the exact closure object installed in the slot record and the
instruction that consumes its nonzero value were not located in the bounded
census; the stored-result mutation should remain named as a receiver operation
until that writer is recovered.

Regression fixtures: the query path is locked by the [R-P0-07]-citing tests
around the weapon-slot query/fallback in `internal/combat/service.go` and the
mode-Q/mode-D dispatcher in `internal/cob/bridge.go`.

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
initialization through the §3.4 AimFrom/Query fallback. The engine passes a
piece argument of −1 to mean "ask the script": the query then calls
`AimFromPrimary`/`AimFromSecondary`/`AimFromTertiary` with a single in/out
argument preset to −1, and falls back to `QueryPrimary`/`QuerySecondary`/
`QueryTertiary` with the argument preset to **0** when the script left the
AimFrom value at −1. The resulting piece index is converted through the unit's
current piece transform and added to the unit's world position; a negative or
invalid result resolves through the normal muzzle-position path and is never a
reason to authorize a shot. A non-negative piece argument (the burst
re-query, §4.3) uses that piece directly and makes no COB call. The root
projectile records the muzzle piece identity so a later burst clone can
re-query the muzzle world position `[R-P0-07]`.

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
accumulator. (Earlier text said the helper "debits both or neither"; the metal
test is genuinely re-evaluated, and only the metal half is skipped if it were to
fail. Nothing between the two tests can change the metal bucket, so the
observable outcome is unchanged — the precise shape is recorded because a
faithful clone must not fold the two tests into one `[R-WPN-01 §7]`.) A
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

**Correction.** Earlier text said the parent's *stored heading* is rewritten to
`heading - sprayAngle/2 + draw`. It is not: `a` is computed into a register,
used for the two velocity components, and discarded. The parent's stored yaw
keeps its original value for the whole burst, so successive pellets scatter
around the **original** aim direction instead of random-walking away from it.
The distinction is observable after two or more pellets `[R-WPN-01 §2]`. The
earlier "spray sampling shape" reading — two draws bounded by `sprayangle` and a
"wobble field adjacent to it", both applied to yaw and pitch — remains
superseded: there is no wobble field (`sprayangle` and `randomdecay` are
separated by `duration` in the record), and the two-draw yaw-and-pitch site is
the turret executor's accuracy spread of §4.4, whose bound is computed, not
authored.

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
if (divisor > 1)  bound = (uint16)bound / divisor               ; 64-bit unsigned
if ((uint16)bound != 0) {
    half = bound >> 1
    slotYaw   += (int16)(rng(bound) - half)
    slotPitch += (int16)(rng(bound) - half)
}
```

The `<< 11` is the `× 2048` of the earlier text written as the shift the image
performs. The divisor is only applied when it exceeds one, i.e. from 24 credited
kills upward. Both draws use the same bound and the shared simulation generator,
which does not advance the stream when the bound is below two. The spread is
applied **after** the muzzle query and after the slot yaw has been converted
from relative to absolute by adding the unit heading, and **before** the creator
call, so the two draws are consumed even when allocation then fails.

**Correction.** Earlier text attributed this spread to "the ordinary/ballistic
slot executor". It belongs to the executor selected by the `turret` flag; the
non-turret line-of-sight/self-propelled executor also reaches the ordinary
creator and computes no spread and consumes no randomness at all. A weapon
without `turret` therefore has perfectly accurate fire regardless of `accuracy`
`[R-WPN-01 §3]`. The earlier reading that the spread does not consume the parsed
fields is also corrected: `accuracy` is its base term (`tolerance` and
`pitchtolerance` belong to the §3.3 drift gate, not this spread).

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
search, not a bounded reader census. Nothing in the firing, readiness, spread,
drift or projectile-motion paths can consume them. `holdtime` is parsed and is
read by exactly five sites, all of them the follow-camera hand-off described in
§7.3; it has no effect on firing, motion, or damage.

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
velocity per tick. That is the whole of the "shifted by the fixed-point
fraction" expression the previous text left unwritten: the numerator is the
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

**Established fact:** The ballistic creator initializes velocity as

```
T0        = (uint32)slotDistance / weaponvelocity        ; unsigned, whole ticks
velocityY = sin(pitch, weaponvelocity) - T0 * gravity    ; 32-bit signed multiply
H         = cos(pitch, weaponvelocity)
velocityX = -sin(yaw, H)
velocityZ = -cos(yaw, H)
```

where `slotDistance` is the distance the slot recorded when it solved, and
`gravity` is the map's per-tick gravity global in 16.16. The pre-decrement of
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
all three are applied including the vertical one.

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
velocityX = -sin(unitHeading, unitMaxVelocity)
velocityZ = -cos(unitHeading, unitMaxVelocity)
```

where `unitMaxVelocity` is the **dropping unit definition's maximum velocity**
in 16.16 per tick, not any weapon field. It writes no expiry, no burst count and
no pitch, emits no Fire and no RockUnit callback, and emits no start smoke.

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
motion, so any velocity change alters rotation immediately. An earlier reading
that orientation advanced by dedicated stored angular-rate shorts is superseded.

**Established fact:** Storm parameters are installed once, from the mission's
OTA keys or from `gamedata/meteor.tdf`:

```
radius   = MeteorRadius                      ; integer key, verbatim
spacing  = trunc(30.0f / MeteorDensity)      ; single-precision divide, ticks
duration = trunc(MeteorDuration * 30.0f)     ; ticks
interval = trunc(MeteorInterval * 30.0f)     ; ticks
```

**Established fact:** Shower resolution tolerates bad data, but not in the way
earlier text described. An **empty** `MeteorWeapon` name disables scheduling —
and still loads the `meteor.tdf` `[Default]` block over the parameters. A
non-empty name is read together with the four numeric keys; if **any one** of
radius, density, duration or interval is zero, the loader overwrites **all five
fields — the weapon name and all four numbers — from the `[Default]` block**,
and then enables scheduling. **Correction:** the previous text said "each zero
substitutes the corresponding `gamedata/METEOR.TDF` default value"; a single
zero replaces the whole block including the weapon name, so a mission that
authors three good values and one zero does not keep the three
`[R-WPN-01 §5]`. If the default block itself is missing or incomplete, the
loader emits the diagnostic `Hey, hoser!  The default meteor shower data was
bogus!` and leaves the parameters as they were. An unresolved weapon name, or a
resolved weapon lacking the meteor flag, still falls back to weapon index zero
instead of disabling.

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
thread-local storage — and **not** from the simulation stream. **Correction:**
earlier text said "six draws per meteor counting scheduling". The correct census
is **four draws each time the storm-start deadline is reached** — consumed even
when meteors are disabled, because the enable flag is only tested at the end of
that block — and **two draws per hit attempt**, including a hit attempt that
then fails on a full pool. Meteors consume zero simulation-stream draws
`[R-WPN-01 §6]`. The earlier reading that meteor geometry drew from the shared
simulation stream is superseded.

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

**Established fact:** No separate torpedo creator, record type, or motion loop was found. Torpedo-like behavior is the shared self-propelled family combined with water-weapon, guidance, and collision flags.

**Established fact:** Runtime medium behavior is asymmetric. A water self-propelled projectile accelerates and steers below the water plane; at or above it, propulsion/guidance are disabled and gravity is applied. Collision permits a water weapon to continue when it is not below terrain. Separate pre-fire water eligibility predicates still restrict target choice, so water-weapon is not a universal medium permission.

### 6.10 Beams, lightning, flame, and feature fire

**Established fact:** Beam behavior exists only inside the line-of-sight motion branch. The head moves on every live tick. Before the latch, the tail stays fixed. The latch is set only when `creation tick + duration < current tick`; the tick that sets it still leaves the tail fixed. Starting on the next live tick, an already-latched beam moves head and tail by the same velocity and preserves the resulting length.

**Established fact:** Collision samples only the moving head. It does not receive the tail or previous point, so the visible beam segment is not a line-area collision volume. Damage uses ordinary impact amount with no duration divisor. Expiry only retires; it does not deliver beam damage. Normal impact usually ends future contacts, while no-explode can leave the beam live for later repeated contacts.

**Established fact:** The jagged lightning render type constructs randomized line segments between head and tail only during rendering. Render type is not consumed by simulation or collision. No lightning chaining or widened lightning collision was found.

**Established fact:** No flame-specific projectile integrator was found. Burn-blow controls selected steering-failure and expiry outcomes. End-smoke changes impact presentation without suppressing damage.

**Cross-reference — no combat producer on the presentation flame strip (2026-08-28, established in [03 "R-LAYER §4"]):** the renderer's strip-5 "flame" objects are spawned solely by the Teleport order-state handler as the teleport visual, and the strip-5 burning-feature smoke by the feature-fire walker. No projectile impact class, fire-damage application, or building burning state produces a strip-5 flame event, so flame render types, `firestarter`, and feature fire have no producer on that strip. The authored-relationship unknowns below are unaffected.

**Established fact:** Persistent feature fire is a separate post-damage record system, not a projectile family. Radial feature damage can ignite only when feature fire is globally enabled, the feature type is flammable, and the weapon firestarter value is nonzero. Active fire records animate, emit smoke, expire, and can spread to eligible nearby or wind-selected feature cells using simulation RNG. This is feature-fire spread, not beam or lightning chaining.

## 7. Projectile timers and motion details

### 7.1 Per-record tick order

**Established fact:** The projectile phase runs once per simulation tick, after
the unit phase and before the meteor scheduler `[01 §4.4]`. It walks the pool in
ascending index order from index 0 to the live count minus one, re-reading the
live count each iteration, and finishes by running the compaction pass of §5.2
exactly once. A record whose remaining burst count is nonzero takes the
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
post-motion one at or below it. On a crossing it queries the map cell, and emits
the weapon's water sound when the cell is found, its own height byte is below
sea level, and the session's opaque-liquid mode is zero. Smoke emission is never
a collision condition.

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
frozen point, resuming normal following at zero. This closes `holdtime` as a
**camera** parameter measured in ticks; doc 07 owns the camera side.

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

### R-WPN-01 — weapon arithmetic pass, corrections and closures

Seven corrections and three closures from the 2026-08-28 arithmetic pass over
§3.3, §4, §6.1–6.8 and §7. Each states what the previous text said and why it
was wrong, so the reversal is auditable.

**§1 — the drift gate's zero-tolerance fallback is a movement state, not a unit
class.** Previous text: "falling back to 2000, or 150 for the non-air class".
There is no class or category test in the gate. The field it reads is bits 2–3
of the movement-mode word — the **movement tier** the integrator caches from the
mover's scalar speed against the FBI keys `MoveRate1` and `MoveRate2`, whose
category 0 also covers a mover-inhibited unit and a unit attached to a carrier
`[04 §5.2]`. So the tight gate (150) applies to any unit in tier 0 and the loose
gate (2000) to any unit in tiers 1–3, air or ground. Written as "non-air class"
the rule inverts for a hovering aircraft and for a stopped tank, which is the
whole population it governs. The field identity is independently corroborated:
the movement lane established the same two bits and the same thresholds from the
integrator side `[04 §5.2]`.

**§2 — burst spray does not rewrite the parent's stored heading.** Previous
text: "the PARENT's stored heading is rewritten as `heading - sprayAngle/2 +
draw`". The perturbed angle is computed into a register, used as the angle
argument for the two velocity-component rebuilds, and discarded; the record's
yaw is not written. The observable difference appears from the second pellet
onward: retail scatters every pellet around the original aim direction, whereas
a rewrite would make the pellet stream random-walk. Both readings agree on the
first pellet, which is why the error survived.

**§3 — the accuracy spread is turret-only.** Previous text: "For the
ordinary/ballistic slot executor…". The spread is in the executor selected by
the `turret` flag. The non-turret line-of-sight/self-propelled executor also
reaches the ordinary creator, and it computes no spread and consumes no
simulation randomness; the vertical-launch and dropped executors likewise. A
weapon without `turret` fires exactly on its solved angles no matter what
`accuracy` says, and consumes zero draws — which is a determinism contract, not
only an accuracy one.

**§4 — the fixed trigonometry table is 512 entries, quantizing to 128 angle
units.** Previous text: "angle quantization in 64-unit steps". The index
arithmetic masks to the even byte offsets up to 1,022, so 512 signed 16-bit
entries cover a full circle and one entry spans 128 angle units (0.703125
degrees). The `(entry × magnitude + 4096) >> 13` product form in the previous
text is correct.

**§5 — one zero OTA meteor parameter replaces the whole default block.**
Previous text: "each zero substitutes the corresponding `gamedata/METEOR.TDF`
default value". The loader tests all four numeric keys together and, on any
zero, reloads the `[Default]` section over the weapon name and all four numbers.
A mission authoring a custom weapon, radius and duration but leaving the
interval at zero gets the default weapon and the default radius too.

**§6 — the meteor CRT draw census is four per storm, two per hit.** Previous
text: "six draws per meteor counting scheduling". The four target/origin draws
are consumed once per storm, when the storm-start deadline is reached, and are
consumed even when meteors are disabled because the enable flag is tested only
at the end of that block. The radius and angle draws are consumed once per hit
attempt, including an attempt that then fails on a full pool. The stream is the
C-runtime generator; the simulation stream is untouched by meteors.

**§7 — the per-shot debit re-tests metal after debiting energy.** Previous text:
"the post-spawn debit helper rechecks both and debits both or neither". The
helper tests both, debits energy, re-reads the player and tests metal again
before debiting metal. Nothing between the two tests can move the metal bucket,
so no stock outcome differs; the shape is recorded because a clone that folds
the two tests into one has silently chosen a different contract for any future
path that could interleave.

**§8 — closure: `holdtime` is the follow-camera hold, in ticks.** Previously
listed among the weapon keys with no described consumer (`[fmt tdf]` records
"exact effect unknown"). Its five readers are the projectile retirement paths;
each loads it into the follow-camera hold counter along with the retiring
record's frozen last point. §7.3 states the contract; doc 07 owns the camera
update that decrements it.

**§9 — closure: five weapon keys have no parser entry at all.** `aimrate`,
`movingaccuracy`, `noselfdamage`, `impulsefactor` and `impulseboost` do not
occur anywhere in the executable's string data. This is stronger than the
bounded reader census `[02 "Weapon record"]` and `[fmt tdf]` record for
`aimrate`: there is no key, so there is no field, so no reader can exist. It
also closes the impulse question of §9.4 from the parser side.

**§10 — closure: the expiry expression is written out.** Every citation of the
previous phrasing "current tick plus integer `range shifted by the fixed-point
fraction / weapon velocity`" now resolves to
`currentTick + (uint32)(range << 16) / weaponvelocity`, an unsigned truncating
division of the authored range promoted to 16.16 by the 16.16 per-tick velocity
(§6.3).

## 8. Collision and impact selection

### 8.1 Collision gate

**Established fact:** Collision maps only the post-motion current X/Z point to one map cell. It uses arithmetic division by the cell scale and rejects coordinates outside map width/height. No previous point, beam tail, velocity interval, or segment fraction is supplied or reconstructed. There is no swept segment, ray, broad candidate collection, or nearest-contact sort in this resolver.

**Established fact:** Sufficiently fast projectiles can therefore pass over intervening cells or thin contact geometry between sampled points. The separate water-crossing presentation predicate can notice an above-to-at-or-below plane crossing, but it does not deliver collision or damage.

**Established fact:** An off-map point clears followed-projectile state when applicable, marks the projectile dead, and returns. It does not call impact, damage, terrain effects, or splash. This retirement ignores the no-explode flag; the flag cannot preserve the record.

**Established fact:** For an in-map point, contact order is fixed:

1. optional projectile-link proximity;
2. cell-height cache update;
3. unit slot zero;
4. unit slot one;
5. units-only early return;
6. feature or footprint-anchor resolution with repeated-cell suppression;
7. terrain penetration and bounce;
8. water/sea continuation or impact.

**Established fact:** Projectile-link proximity uses squared three-dimensional current-point distance and a strict `< radius²` test. It calls impact without a direct unit but does not return afterward, and the resolver NEVER rechecks the dead bit. A second same-call impact — unit slot, feature, terrain, or water — is therefore reachable WITHOUT no-explode; the flag additionally keeps the record available on future ticks.

**Established fact:** Unit slot zero requires a nonzero unit, a side byte different from the projectile side, and projectile height strictly below the unit upper/reference bound. It has no lower-bound test. Unit slot one uses the same nonzero/different-side gates and requires height inclusively between lower and upper bounds. The first successful slot impacts that unit and returns. This fixed slot order is the visible unit tie policy; this function does not consult the alliance matrix.

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

**Established fact:** Units-only returns after both unit slots miss. It suppresses feature, terrain, bounce, and water-plane impact handling, not only feature damage.

**Established fact:** Feature values below the reserved range resolve directly; the footprint sentinel resolves through its anchor cell. A feature hit requires projectile height strictly below feature top. A cached quantized cell pair suppresses repeated feature contact in the same cached cell; a new cell updates the cache and selects impact without a direct unit. The cached-cell suppression cancels only that feature's impact: the terrain/water ladder later in the SAME resolver call still runs and can select another impact.

### 8.2 Ground bounce and water

**Established fact:** Terrain contact requires projectile height strictly below the cell terrain height. Ground-bounce then replaces only vertical velocity with the negation of its signed arithmetic right shift by two and returns; the branch never reaches the central impact path at all, so no-explode is irrelevant here. There is no position correction, horizontal damping, authored restitution, bounce counter, retirement, or impact effect in this branch.

**Established fact:** Because position is not corrected, the projectile remains at its already-integrated point and can collide or bounce again on a later family tick. Units-only prevents the terrain/bounce branch from being reached.

**Established fact:** At or above terrain, a water weapon returns and continues. A non-water weapon at or above sea level also continues. A non-water weapon below sea level impacts unless an opaque terrain/liquid mode suppresses that branch.

**Established fact:** Downward water crossing is a live post-collision presentation event. It requires saved pre-motion height strictly above sea level, current height at or below sea level, a valid cell with its relevant depth/height byte below sea level, and the opaque terrain/liquid mode clear. It emits the weapon water art but does not damage, change velocity, or retire the projectile.

**Established fact:** A collision that marks the projectile dead suppresses crossing splash because the post-collision tail is dead-gated. Bounce and no-explode contacts can leave it live and therefore can still emit a crossing splash. Smoke-trail scheduling precedes splash selection.

## 9. Impact, armor, damage, and area effects

### 9.1 Central damage pipeline

**Established fact:** Projectile impact reaches a central damage path. It
applies weapon-specific armor lookup and veterancy scaling, then defender
modifiers and health/death handling. Normal nonlethal damage and eligible
stationary lethal damage invoke HitByWeapon followed by TakeDamage. Mobile or
hover lethal damage, healing, and paralyzer paths can skip that normal callback
pair. Killed dispatch is cause-specific rather than one universal deferred
callback rule.

**Established fact:** The nine-byte damage packet carries a builder tag,
unsigned 16-bit victim and shooter ids where zero means null, a signed 16-bit
amount, a one-byte armor/direction value, and a kind byte. There is NO
generation token: a nonzero id converts directly to the unit base by slot
arithmetic with no liveness probe. Victim acceptance additionally requires the
alive status bit AND a clear death latch for every packet kind, so a reused
active slot accepts a delayed packet intended for an earlier occupant of that
slot. The ATTACKER receives no alive, type, or death-latch validation of any
kind.

**Established fact:** On accepted non-heal damage the victim stores the raw
attacker pointer plus a snapshot of the attacker's side taken at damage time;
the packet kind is recorded before health mutation. The death handler later
reconstructs the attacker slot afresh from the serialized id — it does not
preserve the original pointer and performs no liveness or generation check.
Veterancy increments and reclaim-pulse fatal payments can therefore land on an
EMPTY slot's stale field or on a replacement unit; the CREDITED side is always
the earlier damage-time snapshot. A zero attacker id suppresses veterancy and
payment entirely; a nonzero id naming an empty slot is not rejected.

**Established fact:** Damage is integer/truncated at several boundaries. Lethal
damage marks the unit for death immediately, so a later projectile in the same
phase observes the death mark and applies no further damage. Because the unit/
death sweep precedes projectile advancement in a master tick, a projectile
lethal is normally consumed by the death handler on the next master tick.
Kill credit is therefore unavailable to later projectiles in the current
phase and cannot change one AOE traversal partway through its recipients.

**Established fact:** The packet dispatcher first rejects a missing, non-live,
or already death-marked target. The stun flag is not a damage guard. Healing
takes an early path that adds the packet's unsigned 16-bit amount to signed
current health in 32-bit arithmetic, compares against maximum health as
unsigned, and clamps when required. Healing produces no damage flash,
reaction, attacker assignment, or damage callbacks. Every non-healing packet
sets damage flash and enters reaction/wake/retarget handling except for packet
kind 11. Paralyzer packets therefore produce the preliminary non-heal side
effects even when the target later fails stun eligibility.

**Established fact:** Normal damage performs exact 16-bit modular subtraction
from health. On a non-positive signed result, the two mobile controller classes
set the death latch, preserve that modular health value, and return immediately;
stationary units clamp health to zero and can continue to normal callbacks.
Packet kind 11 subtracts health but skips reaction and callbacks. For ordinary
kind-1 damage, callbacks are emitted in the order HitByWeapon and then
TakeDamage. HitByWeapon receives two 400-radius trigonometric components
derived from the packet direction byte, not a weapon ID or raw heading.
TakeDamage receives remaining-health percentage, computed with low-32-bit
multiply, unsigned division by maximum health, and a clamp to 0 through 100.
The division is unguarded: a maximum health of zero raises the divide
exception. Healing never divides: it clamps the sum to the unsigned maximum,
so a zero maximum clamps healed health to zero. Stock definitions never author
zero; the unguarded divisions belong to the malformed-state error policy
(TODO(T25)).
Healing and paralyzer packets do not emit this pair.

### 9.2 Armor and veterancy

**Established fact:** Weapons can carry a sorted damage override table keyed by
the target definition's exact UnitName string. The default damage is read as
unsigned 16-bit; a matched override is signed 32-bit. The lookup is a
case-insensitive binary search over the sorted table. It is not a category
lookup.

**Established fact:** Shooter veterancy scales outgoing damage by 6 percent per tier, with tier equal to kills divided by five and capped at tier five. Defender veterancy reduces incoming damage using the observed `(25 - tier) * 4 / 100` factor. Kill credit is delivered through the Killed path, and the kill counter is a wrapping word in the recovered state.

**Supported inference:** A unit's visible “Veteran” label threshold is presentation/data behavior; the numeric damage tier is the bounded arithmetic contract. Do not conflate the two.

**Established fact:** A global difficulty modifier and an armor damage modifier are read by the damage path. Their exact named configuration aliases are not completely recovered, but they multiply/adjust damage before health application.

**Established fact:** The arithmetic order for a projectile recipient is:

1. select the UnitName override or default damage;
2. multiply by area falloff and truncate toward zero;
3. apply attacker veterancy and truncate the integer percentage;
4. apply the recovered global double/half gates;
5. if the target is in its armored state and the incoming amount is below
   30,000, apply its fixed-point damage modifier;
6. apply defender veterancy and truncate the integer percentage;
7. pack the low 16 bits into the damage packet, modulo 65,536.

The packet stores target ID, attacker ID, the modulo amount, a one-byte
relative direction, and a kind byte. Healing bypasses the armor and
defender-veterancy branch. Paralyzer packets use the ordinary incoming scaling
before their unsigned 16-bit duration credit is queued.

Funnel ownership: the armored-state scale (step 5 — the target definition's
fixed-point damage modifier, applied as the definition scale shifted right 16
when the instance's armored-state bit is set and the incoming amount is below
30,000) and the defender veterancy reduction belong to this general damage
pipeline in this document. Document 04 section 9.2 references that funnel for
mission water damage specifically; water damage is a producer of packets into
this pipeline, not a separate scaling path.

### 9.3 Area damage

**Established fact:** Impact uses the direct-target shortcut only when a direct
unit is supplied and the unsigned authored area value is at most 16. That
shortcut applies full falloff, one, to the direct target. If the recorded
shooter is null, the resolver returns after this direct damage rather than
falling through to AOE. Every other case enters area enumeration. Damage is
suppressed when the projectile's owner/controller record has controller type
value three; the semantic name of that type remains unresolved.

**Established fact:** The authoritative blast radius in world units is the
unsigned authored area value shifted right once. The rectangular broad phase
extends `(radius divided by 16) + 1` terrain cells around the impact cell and
is clamped to the map. It traverses rows by increasing Z, then cells by
increasing X. Within each cell it visits unit slot zero, unit slot one, then
the feature/terrain candidate. Both upper map bounds are exclusive.

**Established fact:** Unit distance is three-dimensional distance from the
impact point to the nearest point of the target's inclusive model/footprint
bounding box. The square root is truncated toward zero and reduced to the
retail signed 16-bit world-distance value. A recipient is accepted only when
that value is strictly less than the radius. An impact on or inside the box
has distance zero.

**Established fact:** For accepted nonzero distance `d` and radius `R`, the
falloff is exactly:

`(1 - edgeEffectiveness) * (d / R - 1)^2 + edgeEffectiveness`

The zero-distance value is exactly one. The executable does not clamp the
authored edge effectiveness in this path. Base damage is multiplied by this
single-precision falloff and truncated before attacker veterancy.

**Established fact:** The local memories hold at most 20 unit pointers and 64
feature-cell pointers, but they are deduplication memories rather than
processing caps. A new candidate encountered after its memory is full is still
processed; because it is not remembered, a later occurrence can be processed
again. Unit deduplication happens before its radius test, so an out-of-radius
first sighting can consume a unit-memory entry. Feature distance is checked
before feature deduplication.

**Established fact:** Units-only suppresses the feature/terrain phase, not unit
damage. Feature damage uses feature definition damage, flammability, and
reclaim state and can enter ignition or burn behavior.

**Established fact:** After unit and feature enumeration, a weapon with the
interceptor-chain flag scans the current projectile prefix in slot order,
skips itself and retired records, and uses a strict three-axis distance test
against the unhalved authored area value. Each accepted projectile is sent
through the ordinary impact selector with no direct unit target. The loop
reloads the live pool count, so records appended while it scans can be reached.

**Established fact:** Area processing accumulates signed enemy-damage and
friendly-damage totals with low-32-bit wrap. Its shooter feedback helper sets
one result flag when enemy damage is greater than twice friendly damage and a
different result flag otherwise. This helper does not apply impulse.

### 9.4 Impulse and pushing absence

**Established fact:** An earlier reading that blast paths write a unit
impulse/shove field is corrected: the blast tail calls a shooter-feedback
helper that sets one of two status bits on the SHOOTER unit — bit 6 when enemy
damage exceeds twice friendly damage, bit 5 otherwise — and that status byte
has no reader anywhere in the bounded corpus. There is no impulse or shove
field, and no bounded reads turn any blast output into movement,
mass-weighted pushing, or a separate collision resolver.

**Supported inference:** A clean-room implementation should not add blast
displacement or mass-weighted push behavior. The shooter-status byte is
write-only in the bounded corpus; it may feed presentation or an unrecovered
consumer.

## 10. Paralyzer behavior

**Established fact:** Paralyzer weapons use a distinct damage packet kind. The
incoming amount first passes the target's armored modifier when applicable and
then defender veterancy. Healing is the exception and bypasses those scales.

**Established fact:** A paralyzer packet performs ordinary damage-flash,
under-attack, packet-kind, and attacker-memory side effects before checking
stun eligibility. Only units in the two supported mobile movement classes that
do not have the immunity flag receive a paralyzer task. Structures, aircraft,
and immune units receive those preliminary side effects but no stun task and
no health damage.

**Established fact:** The task is prepended to the unit's primary command list.
If the current head is already the same paralyzer task, another hit adds its
unsigned 16-bit duration credit to the head's 32-bit credit with wrap instead
of allocating another task. The linker prepends a newly allocated task; it
does not append or search the whole list. A paralyzer hit never subtracts
health. If allocation fails, the visible linker path dereferences the null
entry rather than gracefully dropping the stun.

**Established fact:** The new or extended task is first handled by the next
unit phase. On activation it caps positive signed accumulated credit above
1,800 ticks; wrapped-negative credit is not capped. It clears
all weapon-slot targets through the normal TargetCleared path, sets the unit's
stunned activation flag, and turns the credit into an absolute resume tick.
This is a scheduled wait, not a pool decremented once per tick. While the wait
is active, the primary command-list runner is blocked. At expiry it clears the
activation flag and removes the task.

**Established fact:** Damage, healing, death, economy settlement, cloak/upkeep,
and the eight-tick automatic-heal path are outside that blocked task runner and
can continue while the unit is stunned. Paralyzer weapon targeting explicitly
rejects a target already carrying the stunned activation flag. Standard
save/load serializes the task and its duration state.

**Established fact:** No dedicated stun or wake COB callback and no per-tick
duration decrementer exist in the bounded image. The ordinary death latch is a
different unit status flag and must not be used as the stunned state.

## 11. Stockpile and interceptor behavior

### 11.1 Stockpile queue

**Established fact:** MAKENUKE and MAKEANTI order aliases map to BUILDWEAPON queue entries. Stockpile weapons use the secondary weapon-production queue; non-stockpile entries use the ordinary build queue. Queue increments coalesce matching weapon/type entries at the tail; decrements reduce or unlink entries.

**Established fact:** Stockpile production and launch keep two distinct values:
the linked queue node's signed requested count and the weapon slot's byte-sized
completed-round remainder. In the recovered stockpile-production branch, one
completed round increments the slot byte, decrements the queue-node count, and
then requests a selected-unit interface refresh. The refresh helper itself does
not mutate either value and does not clamp an integer queue count into the byte.

**Established fact:** Each stockpile work visit advances a per-node progress
value by five, capped at the selected weapon's compiled reload-time value. The
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

**Unknown:** Byte overflow or wrap for malformed preexisting slot values,
cancellation interaction with admitted carry, repeat requeue, and save and
load reconstruction beyond the established queue count, progress, and
slot-byte persistence remain open.

### 11.2 Interceptors

**Established fact:** Targetable and interceptor flags are parsed. Interceptor
coverage is a separate scalar from ordinary fire range. Automatic interceptor
acquisition requires nonzero slot ammunition and scans the current packed
projectile prefix in increasing order. It selects the first candidate whose
owner byte differs (alliance is not consulted), whose weapon is targetable,
whose stored X and Z
coordinates are each inside inclusive coverage bounds, and which is not
already referenced by any projectile's reservation link.

**Established fact:** The acquisition coverage is an axis-aligned square, not
a radius circle, and tie order is first unclaimed in projectile-pool order,
not nearest. Each axis is implemented as a wrapped unsigned comparison around
coverage in fixed-point units; the square/absolute-distance description
applies to ordinary nonnegative coverage without overflow. Two different
positions play two different roles and must not be conflated: **the scan
metric** is measured on the candidate's *stored aim point* — where the
threat was aimed when created, so interceptors defend the aimed-at ground
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
frozen current point is still tracked and proximally impacted. The earlier
"dead-candidate behavior between the two scans" question is closed: the scans
cannot distinguish dead from live candidates; "both dead" prevents a shot only
through the coverage and claim state, never through a dead-bit test.

**Unknown:** The multiplayer reconstruction index-versus-pointer anomaly that
can store a small pool index as a raw pointer, and side effects of other
interceptor-adjacent failure modes remain open.

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

**Established fact:** Unit health is updated by the central damage path. Mobile
lethal damage sets the death latch and records the damage-packet kind as the
death cause. The next unit phase enters the death preamble. Other scenario,
teardown, and game-over paths can enter the same handler with explicit causes.

**Established fact:** For the full lethal pipeline, severity is integer
arithmetic:

`clamp((floor((-health) * 100 / maxDamage) + previousSamplePercent) / 2, 1, 100)`

The division by two truncates. `previousSamplePercent` is the health percentage
retained from the previous 30-tick sampling boundary, not necessarily the
health immediately before the lethal packet.

**Established fact:** The synchronous Killed query receives severity and a
mutable corpse-chain value and drains the unit's script threads inline. The
death packet packs `(cause << 4) | (variant & 0x0F)`; an earlier reading that
"the death packet stores the low four bits of the returned value as a
corpse-chain depth and the high four bits as the death cause" is corrected:
the HIGH nibble is the death cause, taken from the packet's cause argument —
the last damage-kind byte recorded at damage time — and is not part of the
script return at all. The LOW nibble is the query's second output cell, which
shipped Killed bodies write through their second parameter (see §12.1's
authorship note below); an explicit script `return` is delivered to a
completion receiver (none is installed for this query) or dropped, and never
rewrites either nibble. This value is not a wreck
probability. If the returned depth is zero, no corpse is placed; depth one
selects the authored Corpse feature; larger depths follow that feature's
featuredead link exactly `depth - 1` times, stopping on the no-feature
sentinel.

**Established fact:** Local authoritative death does not issue a second Killed
callback after the synchronous query. It passes the completed death packet to
the central handler in local mode. The received-network path calls that same
handler in replay mode; only replay mode dispatches Killed asynchronously, and
only when signed packet severity is positive. Its return value is ignored.
Cause-specific branches can bypass script work, explosion, or corpse
production; not every removal follows the full lethal pipeline.

**Established fact:** The severity bypass map is exact. Causes 4, 5, and 9 skip
the synchronous Killed query entirely: severity forced zero,
variant/corpse-depth nibble forced zero, no script callbacks, no explosion,
and no corpse. Cause 7 skips the query with severity forced zero and variant
forced one; the authored Corpse feature stamps immediately. A request arriving
while health is still positive skips the query and uses severity and variant
zero. A nonzero remaining-build-fraction float forces the selected variant to
zero AFTER any query ran; the same float gates the death explosion and
veterancy and scales the cause-5 bounty. The full pipeline — any cause outside
{4, 5, 7, 9} with health at or below zero — pushes the computed severity into
the query, drains all of the unit's script threads inline, and copies back
severity plus the script-selected variant; absent a Killed body, the variant
cell keeps its caller-indeterminate value except where the build-fraction rule
forces zero.

**Established fact (Killed authorship):** A bytecode census of the shipped COB
corpus settles the variant-cell authorship question: every shipped unit with a
Killed body (153 of 157 extracted scripts) declares the two-parameter form and
writes its second parameter — the corpse-depth cell — and none reassigns the
severity cell, so the packet severity is always the computed severity and the
depth nibble is always authored. The four remaining shipped scripts (the two
commanders and the two dragon bosses) have no Killed body; their variant cell
is deterministic-but-opaque stack history unless the build-fraction rule
forces zero.

**Established fact:** Death credit is a cause-gated switch, not merely the
presence of an attacker. The ordinary and cargo causes share the full-credit
path; one special cause joins conditionally, one has a partial path, and all
other observed causes credit nobody. The full path updates controller/player
statistics and increments the attacker's wrapping kill word only when the
attacker and side gates pass. That death statistic is not a field of the unit
definition.

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

Supersession: an older mapping that read cause 3 as water/drowning and causes
4/5/9 as writerless is wrong — cause 3 is self-destruct, cause 11 is mission
water damage, and causes 4 and 9 have the writers named above. An intermediate
label describing the mission-water dispatch site as a self-destruct gate is
likewise corrected: that site is the drowning/water-damage gate.

Two attribution edges are pinned. **Meteors credit nobody**: their attacker
identity is null (neutral side), so no veterancy increments and no kill
statistics result even though the explosion damages every side alike. **Cargo
killed by carrier death credits the carrier's killer**: the cascade applies its
30000 damage per cargo with the attacker argument set to the carrier's killer.

**Established fact:** The unit float that gates the death explosion is the
remaining-build-fraction/landed indicator shared with construction and flight
state — one with health zero under construction, zero when grounded or normal,
nonzero while airborne. The gate compares it **equal to 0.0f**, so units still
under construction do not detonate through this path.

### 12.2 Wreckage and feature placement

**Established fact:** Corpse creation enters the common feature stamper at the
victim's anchor cell. It validates the entire footprint, allocates feature
animation state, stamps blocking/filler cells, and notifies path revision.
Blocked or clipped placement fails silently: the engine does not choose a
fallback heap, shift to a nearby cell, retry, or preserve the dying unit.

**Established fact:** Land and water use the same selected corpse definition.
Successful land placement can emit the ordinary ground notification. Water
placement instead patches the placed animation state for sinking and suppresses
that notification.

**Established fact:** When the cause and severity gates request a death
explosion — packet severity positive and the remaining-build-fraction float
exactly zero — the handler selects between the unit's TWO resolved
death-weapon fields: the self-destruct field when the cause nibble is 3, the
explode field otherwise. Both resolve from the authored `selfdestructas` and
`explodeas` names at catalog load; an unresolved or absent name produces no
explosion at all (there is no third default candidate in the executable). It
constructs an ordinary projectile-shaped impact record at the
victim's position with its current velocity, and calls the central impact
path. The explosion is authoritative: it can deal armor-scaled direct/area
damage and trigger the normal feature and sound effects. It is not merely a
presentation event.

**Established fact:** Feature definitions can carry energy, metal, damage, burn weapon, spark time, flammability, geothermal, blocking, reclaimable, autoreclaimable, and indestructible behavior.

## 13. Weapon-driven feature, fire, audio, and effect events

### 13.1 Feature damage and fire

**Established fact:** Feature damage reads feature damage and weapon firestarter/burn weapon data. Flammable features can allocate burn state, select a random spark deadline through one draw of `simulationRandom(sparktime / 2) + (sparktime / 2)`, and emit a treeburn/fire event. The feature phase runs every simulation tick; animation advance and burn countdown decrement run every tick; only smoke emission is gated on `globalTick % 3 == 0`.

**Established fact:** Fire spread uses the candidate's spread chance and simulation RNG, scans at most 48 candidates in a 7 by 7 window excluding the origin in row-major order, and makes five cumulative wind-direction attempts that collapse to no draws at zero wind. Drawing occurs only after every cheap legality check (off-map, empty, already attached, not flammable). Spread consults the candidate's own `spreadchance`, never the burning feature's. Burn weapons route back through the ordinary projectile and area-damage subsystem after both spread passes: the burn weapon's impact is built as a synthetic projectile-shaped record with a NULL shooter and a zeroed side byte and pushed through the ordinary area enumeration, so burn-weapon damage awards no veterancy and no kill credit; the friendly/enemy damage-sum classification compares side zero against each recipient. An earlier inference that attribution equals the igniting projectile's side is corrected.

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

### 13.2 Sound and smoke events

**Established fact:** Start, hit, water, and trigger sounds are selected from weapon definition fields. Start smoke and smoke trail are emitted at spawner/tick boundaries; end smoke is selected by the expiry/impact path. Sound-trigger burst emissions are separate from Fire/RockUnit callback cadence.

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

### 13.3 Presentation boundary

**Established fact:** The simulation publishes model/effect/sound identifiers, impact positions, feature/fire state, and camera-follow state. The renderer consumes those events later.

**Unknown:** Exact renderer interpolation, frame lifetime, particle pooling, and visual ordering are intentionally outside this document.

## 14. Evidence basis and correction boundaries

The combat sections above were derived only from these areas of the retail executable:

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

Earlier readings that a later re-derivation corrected are not promoted as
affirmative behavior. For projectile allocation and lifetime, the complete count-writer
census and raw allocator/updater/compactor control flow outrank the older pool
ledger and close its former contradiction.

## Missing and unknown

Open items only. Each bullet states what is unknown, the section that owns it,
and the decider that would close it. Findings that closed an item live in the
body and are not restated here.

**Correction (2026-08-28, RWU-00-5).** This tail listed many closed contracts
as "missing" — unit-grid insertion rules, the opaque terrain/liquid mode, the
double/half damage gates, packet-kind producers, zero-maximum-health behavior,
`Killed` second-slot authorship, blast feedback, kill attribution,
remaining-build-fraction writers, the slot-to-node mapping, dead-candidate
interceptor behavior, sound-trigger burst cadence, and corpse-creation
ordering each opened a bullet with "is closed" and then recited the finding.
Those recitals are deleted here only; §§8–13 continue to own them.

### Catalog and targeting

- Semantic identity and writers of the secondary radar-like candidate list,
  and the complete sonar and jammer interactions outside the closed primary
  direct-visibility list · §3.1, doc 03 §3.3 · static trace.
- Whether the "targeting-upgrade aggregate" gate has any reader; no recovered
  acquisition function reads it · §3.1 · static trace over the unrecovered
  regions.
- Manual unit and point target encoding, command-fire replacement, and the
  full set of manual-versus-autonomous latch callers · §3.2, doc 07 · static
  trace.
- Acquisition bypasses and category behavior for non-unit target types · §3.3
  · static trace.
- Whether the ballistic solver's `acos` argument can exceed one on malformed
  authored or network input, what the runtime returns then, and whether the
  resulting unordered angle comparisons really accept and serialize it · §3.3
  · static trace of the runtime `acos` domain path plus a reachability argument
  over the root expression.
- The Aim-completion closure writer and consumer in the weapon-slot record;
  the zero/nonzero delivery and the no-timeout contract are established
  · §3.4 [R-P0-07], doc 04 §5.3 · static trace. Marked `TODO(question)`;
  the store census belongs to the units/COB lane.
- Target replacement during an outstanding Aim, and malformed-state
  interactions around the closed family readiness gates · §3.4 · static trace.
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
- Consumer of the projectile record's cached average-height scratch value
  outside the projectile family; the layout hole is preserved for it · §5 ·
  static trace.

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
- Renderer algorithms for projectile render types outside the closed
  line-versus-jagged-lightning distinction · doc 03 §5.4 · static trace.

### Collision and damage

- Footprint-anchor coordinate conventions and malformed sentinel behavior
  · §8.1 · static trace.
- Quantization and overflow of the repeated-feature-cell cache at negative or
  extreme coordinates · §8.2 · static trace.
- Sign and scale conventions for vertical velocity, terrain height, and sea
  level outside ordinary map ranges · §8.3 · static trace.
- Malformed-name, duplicate-key, and signed-overflow behavior in damage-table
  construction and lookup · §9.1 · static trace.
- The configuration alias that names the global double/half damage bits; the
  reader is direct and the full-image census found no writer · §9.1 · static
  trace over the unrecovered regions.
- A guarded error path for the unguarded unsigned divisions when maximum
  health is zero; stock never authors zero · §9.1 · static trace. Marked
  `TODO(T25)` at two sites — Nanolathe guards the divisions as declared
  policy.
- Practical reachability of signed 16-bit AOE distance wrap, and of more than
  20 unique unit or 64 unique feature-cell candidates, in accepted retail maps
  · §9.3 · asset census over the map corpus (the AOE dedup map probe).
- Resurrection interaction with the death pipeline, and the ordering among
  several concurrent reclaimers issuing simultaneous repair and damage
  packets — which pulse becomes fatal · §12, doc 05 · static trace.

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
- Renderer interpolation and visual lifetime for weapon-driven effects · doc
  03 · intentionally outside this document's scope.
