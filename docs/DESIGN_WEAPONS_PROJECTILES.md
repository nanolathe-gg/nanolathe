# Design — Weapons, projectiles and damage

`internal/combat`. Three weapon slots per unit, the acquisition scan and the
shot-admission gate, the aim handshake with the unit's script, the fixed
300-record projectile pool with its burst anchors and compaction, the motion
families, the impact ladder, the damage funnel, and the death path that turns a
unit into a corpse chain. Stockpiles, interceptors and the meteor shower live
here too, because retail reaches all three through the same pool.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation routing that
makes a bare `[Cn]` in `internal/combat` resolve to the contract list in §3
below. Rules every diff is reviewed against are in
[INVARIANTS.md](INVARIANTS.md); places where the reference install disproves the
written contract are in [SPEC_CONFLICTS.md](SPEC_CONFLICTS.md).

## 1. Purpose and boundary

This package answers three questions for the rest of the simulation.

* **May this unit shoot that, right now?** Two separate predicates, because
  retail has two: the acquisition gate that decides what enters a slot's
  candidate list `[06 §3.1]`, and the shot-admission gate the attack handlers
  and the per-slot pipeline ask before a shot leaves the barrel
  `[06 §3.3]` `[06 R-WPN-05 §9]`.
* **What is in the air, and where is it this tick?** One fixed pool of 300
  records, one creation-family dispatch, one active-motion dispatch, and a
  compactor that repairs exactly the links retail repairs `[06 §5.1]`
  `[06 §5.2]` `[06 §6.2]`.
* **What does the hit do?** One collision ladder, one damage funnel every
  producer feeds — projectile, splash, self-destruct, mission water damage —
  and one death path `[06 §8.1]` `[06 §9.1]` `[06 §12.1]` `[04 §9.2]`.

Everything above is authoritative simulation state. The boundary runs at five
places.

* **The tick belongs to `internal/session`.** Combat is called; it schedules
  nothing. The per-unit weapon step runs inside the second phase's slot visit;
  the projectile phase is the third — interceptor guidance, burst advance,
  motion and collision, then the tail compaction and interceptor detonation;
  the meteor shower is the ninth and the shake driver the tenth
  (ARCHITECTURE §4, `[01 §4.4]`). Neither the pool nor the damage funnel reads
  a clock or logs inside the tick.
* **Scripts belong to `internal/cob`.** Combat queues each slot's
  `TargetCleared` and `Aim*` callbacks in slot order. The session performs the
  unit visit's one normal virtual-machine drain after weapon work, so a return
  from a newly dispatched Aim is available on a later visit; it never runs a
  second drain to chase a pending aim
  `[06 §3.3]` `[06 §3.4]` `[04 R-CB-01 §6]`. Fire callbacks — the weapon's
  `FirePrimary`/`FireSecondary`/`FireTertiary` and `RockUnit` — are dispatched
  through a port, not called into the VM from here `[06 §4.1]`.
* **Orders belong to `internal/orders`.** The attack, guard and suppress
  handlers ask this package's shot-admission gate before binding a slot, and
  the standoff every chase substate is built from is the slot weapon's authored
  `range` `[06 R-WPN-05 §1]` `[04 R-ORD-01 §3]` `[04 R-ORD-01 §7]`. The
  reverse direction — the retaliation order, the observer notice, the
  under-attack message — arrives as function values the session installs, so
  the damage path keeps retail's order and gates without importing the order
  package `[06 R-WPN-04 §2]` `[08 R-AI-01 §11]`.
* **Presentation is not here.** Combat emits an ordered, immutable event
  stream — shake request, hit and water sounds, start/trail/end smoke,
  explosion and water explosion, impact, damage flash, corpse — and publishes
  one `ProjectileView` per live record at the frame boundary. Which GAF entry
  those names resolve to, how the flash disc is generated, and how a shell is
  drawn are presentation `[06 R-WFX-01 §1]` `[06 R-WFX-01 §2]` `[03 §5.4]`
  [I6].
* **Features and terrain are read, never written from the blast.** The area
  walk hands each accepted feature candidate to the feature runtime, which owns
  the ignition test and the two accumulators `[06 §13.1]`
  `[05 R-FEAT-01 §8]`. There is no terrain deformation to apply.

Two things retail does *not* do bound this package as firmly as anything it
does: there is **no impulse or knockback** `[06 §9.4]`, and there is **no
moving-accuracy or aim-rate mechanism** `[06 R-WPN-03 §3]`. Both have been
proposed as "obvious" additions; both are inventions.

`Service.StepAutonomousForPlayer` owns phase-5 target retention and acquisition.
The session binds it between manager task dispatch and strategic refresh.
`StepWeaponsForUnit` only resolves and fires targets already installed at its
phase-2 entry. Per-player cursor order, including wrapped visits and empty
records, belongs to the maintenance pass `[06 §3.2]`.

## 2. Packages and key types

### 2.1 Weapon slots and the per-slot pipeline

Every unit carries exactly three slots — primary, secondary, tertiary
`[06 §1.2]`. `Slot` holds the compiled weapon definition, the reload counter,
stockpile ammunition, the aim latch and its result, the target words with the
sentinel that separates a unit target from a ground point, and the control byte
whose low five bits are named and whose top three are inert
`[06 R-WPN-05 §3]`. The order side reaches the same byte through the unit
record, which is why `weapon_adapter.go` exists at all: it is the seam that
lets an order handler release, inhibit, retarget or stop a slot without
reaching into slot internals.

`PipelineStep` names the fixed per-slot order, and `TickSlot` runs it:
decrement a nonzero reload → validate or resolve the target → dispatch `Aim*`
and wait on readiness → the shot-admission gate → the family spawner → store
reload and ammunition → debit `[06 §4.1]` `[06 §4.2]`. A gate that fails
short-circuits the later steps and reorders none of the earlier ones; the
reload decrement happens whether or not anything downstream succeeds. Admission
tests the decremented signed-16 word. Starts 0, 1, and 2 become 0, 0, and 1
after their first decrement and respectively reach fire admission on visits 1,
1, and 2 when all other gates pass. Any nonzero signed word, including a
negative restored value, blocks admission; decrement wraps in that same signed
16-bit word `[06 §3.3]` `[06 §4.2]`.

`StepWeaponsForUnit` is the authoritative per-unit entry point the session's
slot visit calls. It completes each slot's target resolution, Aim dispatch and
firing decision before visiting the next slot, so an earlier slot's Fire/Rock
starts precede a later slot's query or Aim preparation. The session owns the
one normal drain after all three slots [04 R-MOV-03 §1]. A missing script or an
exhausted thread pool never authorizes a shot `[06 §3.3]` `[06 §3.4]`.

### 2.2 Acquisition and the per-side target registry

`targetRegistry` is the per-side candidate structure of `[06 §3.1]`: a primary
list, a secondary list, and the gate that decides whether the secondary list is
consulted, rebuilt on the 30-tick cadence. The rebuild is one half of a single
retail routine whose other half is the computer player's strategic refresh, so
the cadence word here is a mirror of the planner's gate rather than a second
clock `[06 §3.1]` `[08 R-AI-01 §16]`. A side whose strategic state exists
rebuilds even when it owns nothing, and which sides rebuild does not depend on
which units exist.

The secondary list samples the **seen bit** in each live unit's status word
at that player's rebuild. A viewing-player sensor pass between player visits
is visible to later rebuilds; there is no separate per-tick seen cache.
Previously built lists keep their cadence rather than being retested per candidate `[06 R-WPN-02 §3]` `[06 R-WPN-02 §6]`. The
autonomous scan carries a persistent per-player round-robin cursor rather than
starting from slot zero each visit `[06 §3.2]`.

Hostility and direct visibility classify the registry at rebuild. Registry
hostility reads only the registry owner's alliance-row declaration at the
candidate owner's ally group (the owner slot for a seated single-player row);
it does not combine either player's declarations. This directional test must
not be replaced with a generic symmetric alliance predicate
`[06 §3.1]` `[05 R-SHARE-01 §1]` `[04 R-MOV-03 §11]`. Per-attempt
materialization checks liveness; the planar query determines whether the
primary population is empty before secondary fallback. Acquisition samples
that population in registry order with swap removal, at most fifty picks;
even a smaller population is sampled. Each pick then undergoes physical and
paralyzer rejection before its score, with preferred and fallback minima
updated in pick order `[06 §3.1]` `[06 §3.2]`. The shot-time physical gate has
its own ordered clauses below `[06 R-WPN-05 §9]`.

The service builds one attempt-local candidate snapshot only after its
range-and-liveness query has found an entry. That snapshot is the sampler's
swap-removal storage, so the public query can retain an unmodified input while
the service avoids a second filtered copy. Secondary entries are materialized
only after the empty primary preliminary query selects them. This change retains attempt-local storage rather than adding a retained
service buffer. It removes the redundant copy and unused secondary snapshot;
one allocation remains for a nonempty selected population. Capacity follows
the selected registry list, with no inferred unit cap `[06 §3.1]`
`[06 §3.2]`.

Direct visibility consumes the current candidate unit's runtime status word,
including its constructor seed or restored sensor bits before the next pass:
the sonar bit permits a below-surface hull probe, while an alliance row cannot
stand in for that contact. Its four probes begin at the candidate definition's
min-X/max-Y/min-Z box corner and carry its spans through the shared visibility
predicate `[06 §3.1]` `[03 §3.2]` `[03 R-VIS-01 §5]`.

### 2.3 The shot-admission gate

`CanEngageSlotTarget` is the gate the attack handlers ask, in retail's order
`[06 R-WPN-05 §1]` `[04 R-ORD-01 §7]`:

1. a **water weapon** requires the target in the water — its whole-unit height
   not above sea level, and for a `canhover` target its height plus half its
   model top not above sea level either;
2. a **non-water** weapon requires both ends out of the water: shooter height
   plus model top, and target height plus model top, strictly above sea level;
3. a `toairweapon` additionally requires the target's committed mover mode to
   read airborne `[04 R-MOV-01 §8]`;
4. a `ballistic` weapon additionally requires a trajectory solution that is not
   the no-solution sentinel;
5. the planar range test against the slot weapon's authored `range`.

`ShotTimeAdmitsPoint` is the shooter half of the same gate asked against a
world point instead of a unit — the range test, the shooter-side sea-level
clause, and the ballistic solution. It exists as an exported predicate because
the computer player's rally task needs it and does not carry its operands
`[08 R-AI-01 §19]`. Neither form tests radar, cloak or jamming, and neither
consults reload, ammunition or cost.

### 2.4 Aiming

The composed unit-death observer releases combat's pending Aim tracking before
the pool slot becomes available again. Tracking belongs to the dying unit's
weapon receivers, not to the numeric handle a later unit may reuse
`[04 §2.4]` `[P0-16]` `[06 §3.3]`. The session regression parks an Aim callback,
destroys its unit, reuses the slot, and fires the replacement's ungated weapon.

Outstanding Aim callbacks share the slot's receiver. A fresh dispatch clears
readiness; each later nonzero return grants it, while a zero return leaves its
current value unchanged `[04 R-CB-01 §6]`. Retargeting can overlap those
callbacks, so the completion adapter cannot assign readiness from every return.

Unit save/load connects the saved request latch, readiness word and distance
word to the live slot. `AimSlot` retains a restored readiness word verbatim
until a producer changes it; gameplay uses its zero/nonzero projection. Script
restoration clears completion receivers, so pending requests are preserved
without reconnecting callbacks or adding a timeout `[08 R-SAVE-WEAPON-01]`.

`aim.go` holds three things and no state. The **drift gate** is the angular
tolerance a turret must be inside before it may fire; a weapon that authors no
tolerance falls back to 150 angle units while the shooter's movement tier is
zero and 2000 while it is moving — a movement state, never a unit class
`[06 R-WPN-03 §2]` `[06 R-WPN-01 §1]` `[04 §5.2]`. The **accuracy spread**
bound is computed from the shooter's own health, maximum health and credited
kills; nothing about the target enters it, and it steers ballistic trajectories
only `[06 R-WPN-03 §4]` `[06 R-WPN-05 §5]` `[06 R-WPN-01 §3]`.
`BallisticSolve` is the trajectory solver: the discriminant, the two candidate
angles, and the conversion into the 16-bit angle word.

The **pre-fire lead** is the one place in the whole weapon pipeline where the
target's motion enters the firing solution `[06 R-WPN-03 §3]`. It is applied to
the resolved target point by `target.go`'s `PreFireLeadPoint`, at target-point
resolution and before the aim origin is queried, so the aim solve, the
shot-time gate and the creator all receive the led point. Its five gates — the
slot's armed bit, the weapon not being `cruise`, the target having a movement
record, the shooter's credited kills being **strictly greater than five** on
the unsigned count, and a nonzero `weaponvelocity` — are `PreFireLeadGate`.
The arithmetic is a **three-dimensional** distance (unlike the planar range
test in the same section), divided by `weaponvelocity` into a 16.16 tick count,
scaled by `0xcccc / 65536`, and multiplied per axis by the target mover's
velocity triple `[06 §3.3]`. Projectile guidance never leads — this is the only
lead, and `cruise` suppresses it `[06 §6.7]`.

That velocity triple reaches combat through `units.MoveState`, which
`internal/movement` publishes beside the scalar speed at every commit
`[04 R-MOV-01 §1]` `[04 R-COLL-01 §1]`. It is a different quantity from the
scalar speed word beside it: a magnitude versus a signed per-axis displacement.

For a turret, the yaw handed to the script is **relative** to the unit's heading, and the drift pair is relative on both sides `[06 R-WPN-05 §4]`. Which angle each
velocity build negates, and where the half-turn numbering is crossed, is a
named contract rather than a convention `[06 R-WPN-05 §11]`.

Executor selection follows [06 §3.3], independently of creation and motion
families. The vertical executor dispatches the zero argument pair, with its
stockpile ammunition gate, and skips aim-origin queries and angle solving;
its absolute slot angles are installed only after the fire-time muzzle query.
Turret takes precedence when both flags are authored.

The live slot keeps retail-numbered angles throughout an attempt. The turret
adds heading after the muzzle query; spread then mutates the per-shot copy.
`fireScriptAdapter` reads that same copy for recoil before it is copied back,
including retention on a full pool. The ballistic creator converts only the
yaw numbering at its input boundary and launches from the stored pair; the
ordinary creator continues to solve its flight from muzzle and target. Neither
path applies spread a second time [06 R-WPN-03 §4][06 R-WPN-05 §4][06 R-WPN-05 §5][06 R-WPN-05 §11].

The aim handshake itself is a latch: a new receiver clears readiness before
dispatch; an explicit nonzero return grants it, while every delivered zero
clears it. The request latch is set immediately after dispatch, and there is no timeout
`[06 §3.3]` `[06 §3.4]`. Which of the three readiness ladders a weapon takes is
decided by its executor flags alone — a turret needs the latch and a nonzero
result, vertical launch needs the result only, and the line-of-sight,
self-propelled and dropped families gate on neither. Guidance flags (`tracks`,
`cruise`) are motion-phase flags read by the family dispatch and never change
what the aim gate demands `[06 §3.3]` `[06 §6.2]` `[06 §6.6]`.

### 2.5 The projectile pool

`Service` owns the pool. `Service.Slots` is a `pool.Projectiles` and is the
**sole** allocation, dead-flag and count authority; `Service.Records` is a
parallel array of named `Projectile` values indexed by the same slot. Retail's
record is 107 bytes and that size is record identity, not a layout to
reproduce: Go stores named fields [I13].

A `Projectile` carries the weapon id and owner side, the current point and the
second tail/start/waypoint point, the stored target point, the retained unit
target, the optional projectile-to-projectile link, velocity and speed, the stored
planar muzzle-to-aim distance, yaw and
pitch as 16-bit angle words, the shooter and its side byte, the firing piece
identity, the creation/expiry/smoke/burst deadlines, the phase and latch flags,
the collision cache's cell pair, the cached average floor height, and the
old-index marker compaction uses `[06 §5.1]` `[06 §6.1]`.

Two of those fields are subtler than they look. The **collision cache** pair is
never reset: the pool is zero-filled once at battle start and compaction copies
survivors downward, so a reused record carries its previous occupant's pair
until its own first feature contact overwrites it `[06 R-DMG-01 §13]`. The
**cached floor height** is written by the collision gate on every in-map tick,
after the in-map test and before the unit-slot tests, and no gameplay test
reads it — its only consumer is the ground shadow, through the committed frame
`[06 R-DMG-01 §14]` `[06 R-WPN-02 §7]` `[03 §5.4]`.

Allocation appends at the active-span tail and never fills a hole; a dead
record still consumes capacity until the tail compaction runs
`[06 §5.1]` `[01 §6.1]`. `Compact` is the subtlest routine in the package and
is described contract-by-contract at C12.

### 2.6 Firing and bursts

`TryFire` is the family spawner. It validates the target and trajectory,
runs the forced `Query*` muzzle query, computes the accuracy spread, reserves a record,
initializes it through the creation family, and runs the callbacks — in that
order, so everything before the reservation is retained when the pool is full
`[06 §4.1]` `[06 §4.4]`. `FirePorts` carries the seams the spawner needs and
does not own: the script dispatcher, the presentation event sink, the shooter
record, and the simulation random stream. `FireSpy` records callback order for
tests and nothing branches on it.

The common initializer is also where the shooter's **reveal deadline** is
stamped, at the current tick plus 600, written outright with no maximum. Its
one gameplay reader is the cloak upkeep gate, so a cloaked unit that fires
stops paying — and stays visible — for the next 600 ticks
`[06 §4.1]` `[03 R-VIS-01 §6]` `[05 R-ECO-01 §9]`.

`AdvanceBursts` advances burst anchors. A burst weapon spawns its pellets from
a parked anchor record: while the anchor's remaining count is above zero it
takes the burst branch instead of the motion branch, it re-derives its muzzle
world position from the *stored* firing piece with no COB call, it clones,
and the spray perturbs a scratch heading used only to rebuild the parent's
velocity — the parent's stored yaw is never rewritten, which is why every
pellet scatters about the original aim instead of random-walking
`[06 §4.3]` `[06 R-WPN-01 §2]`. A shooter's death sweeps its anchors, so no
anchor outlives its shooter.

### 2.7 Motion families

Creation dispatch and active-motion dispatch are two different orders, and both
are reproduced: creation is meteor → ballistic → vertical launch →
line-of-sight/self-propelled → dropped, and active motion is self-propelled →
line-of-sight → ballistic → dropped → meteor `[06 §6.2]`. `motion.go` carries
one `Init*` per creation family and one `Advance*` per motion family, plus the
shared common initializer.

Ballistic and dropped records take the map's three global wind words as raw
16.16 increments added straight to position — the published X word is
`−2 · sin(heading) · speed` and the Z word `−2 · cos(heading) · speed`, and the
integrators add them with no shift `[06 §6.4]` `[06 R-WPN-05 §8]`. The
ballistic launch pre-decrements the vertical component by one flight time's
worth of gravity as part of the launch, not as an integrator artefact.

A guided self-propelled record does not steer at the point its aim solve stored
at launch. `GuidanceTargetPoint` is the guidance target-point helper: for a
non-cruise weapon it answers the linked record's current point when the
projectile-to-projectile link is set, else the retained unit target's world
point while that unit's live flag is set, else the stored target point
`[06 §6.7]`. The stored point is the LOST-target fallback `[06 §6.8]` and is
never overwritten. `GuidanceEnv` carries the two lookups from the driver into
`AdvanceSelfProp`; its projectile lookup does not filter dead records, because
retail dereferences the link with no liveness check `[06 §5.2]`.

A `cruise` weapon takes a different helper entirely. `CruiseTargetPoint`
ignores the link and the retained unit and works from the stored target point,
substituting only its altitude: above 1,024 whole world units of
three-dimensional range — a **strict** compare on a signed short, so exactly
1,024 takes the other arm — the steer point sits at a fixed 700 world units of
ABSOLUTE Y, and within it at `max(terrainHeight, seaLevel)` `[06 §6.8]`. The
threshold's narrowing wraps rather than saturates, which is unreachable for
in-bounds geometry [I11]. No stock weapon authors `cruise`, so the helper is
locked by unit test rather than by a battle. Whether that
steering runs at all is `guiding = twophase ? (state bits ≠ 0) : guidance` — a
two-phase weapon does not consult its `guidance` flag `[06 §6.7]`.

Water weapons and torpedoes, beams, lightning, flame and feature fire, cruise
target points and target loss, and the vertical launch's two-phase behaviour
each have their own established rules `[06 §6.6]`–`[06 §6.10]`. Per-record tick
order, the fixed-point integration and the expiry rules are `[06 §7.1]`–
`[06 §7.3]`.

### 2.8 Impact

`service.go` owns the live collision ladder, central impact for pooled and
stack records, and the shared area-damage walk. `impact.go` supplies the
contact, area and `noexplode` predicates. There is no separate simulated
collision ladder for tests. The contact
test has **no radius**: the plot cell's occupancy word is the horizontal gate
and the model top is the vertical band `[06 R-DMG-01 §7]`. The ladder runs the
projectile's own cell's two unit slots first. If neither accepts the shot,
`unitsonly` returns without testing features, terrain, bounce or water; the
projectile phase honors that continued-flight result `[06 §8.1]`. Otherwise
feature resolution follows — with
the cached cell pair consulted only after a feature resolves and its height
test passes — then terrain, ground bounce and water `[06 §8.1]`
`[06 R-DMG-01 §13]`.

`noexplode` gates only the ordinary impact branch's retirement and follow-camera
finalization; shake, sounds, effects and damage always run, and retirements
outside that branch — line-of-sight expiry, burst-root completion, the lava
underwater self-expire, the off-map exit — ignore the flag entirely
`[06 §13.2]`.

**EC-P4 ownership contract.** `TickProjectiles` alone drives pooled motion and
collision. `impactProjectile` applies pooled retirement and then calls
`handleProjectileImpact`; `Service.ImpactStackRecord` calls that same central
path without pooled retirement. `explodeWeaponAt` is their shared area walk;
`Service.ExplodeWeaponAt` intentionally exposes only area damage for feature
burn weapons. Weapon producers select a nominal through `weaponDamageNominal`,
and every fixed or weapon recipient reaches `Service.AcceptDamage`. Packet
serialization remains separate from producer arithmetic. Tests use these real
entries; removed test-only collision, packet and water-sweep alternatives must
not be reinstated [06 §8.1][06 §9.1][06 §9.3][06 §12.2].

### 2.9 Damage

The damage packet is nine bytes: a builder tag, the victim and shooter ids as
16-bit words, the amount, a relative-direction byte, and the kind byte
`[06 §9.1]`. Ids are plain slot numbers with no generation token, so acceptance
is the victim's alive bit plus a clear dead latch and nothing more — a reused
slot accepts a stale packet, and the attacker is not validated at all.

The production weapon producer uses `weaponDamageNominal`/`weaponNominal`
before the shared `Service.AcceptDamage` receiver; fixed producers enter the
receiver with their established nominal directly. The arithmetic order is C20:
falloff conversion retains the low word, a null shooter skips attacker scaling,
and percentage products wrap before division. `ComputeScaledAmount` is a
standalone arithmetic helper that assumes a present shooter. Tests of packet
producers and water damage run through the production intake and phase-2
unit visit; no separate packet builder or whole-world water sweep is retained. The table selected by the weapon producer is the
**weapon's own `[DAMAGE]` block**, keyed
by the target definition's exact name; there is no `armor.tdf` and no armor
category `[06 R-DMG-01 §1]`. The armored gate reads the victim's runtime
posture bit, not the definition's `armoredstate` flag, which has no reader
in retail `[06 R-DMG-01 §8]`. `damagemodifier` and every consumer of the
wrapping kill count are enumerated `[06 R-DMG-01 §2]`.

The direction byte handed to the victim's script is a fresh bearing from the
record's current point minus the victim's heading, so a shot arriving from dead
ahead reads the same value at every heading; it is not derived from the
projectile's yaw `[06 §9.1]` `[06 R-WPN-02 §2]`.

Area damage is a rectangular broad phase of `(radius / 16) + 1` cells around
the impact cell, a blast radius of the authored area shifted right once, and
the quadratic falloff of `[06 §9.3]`, with the shooter itself excluded
`[06 R-WPN-02 §10]`. Every cell inside the blast also offers its feature to the
feature runtime `[06 §13.1]` `[05 R-FEAT-01 §8]`.

`ReactionSeams` binds the damage-intake reaction routine's four parts that this
package cannot reach from inside itself — the observer notice, the retaliation
branch, the damage flash and the under-attack message — while the routine's
order and gates stay here `[06 §9.1]` `[06 R-WPN-04 §2]` `[08 R-AI-01 §11]`.
The flash byte's per-visit step is a plain byte decrement, so it survives 240
visits rather than sixteen, and its only reader is the minimap's unit-dot pass
`[06 R-WPN-04 §4]`.

Mission water damage is a producer into this same funnel, not a second scaling
path: a 30-tick cadence, both mission fields nonzero, an owner control byte of
1 or 2, `canhover` excluded, and the height compared as a signed integer
against the map's sea-level byte `[04 §9.2]` `[06 §9.2]` `[06 R-DMG-01 §8]`.

The paralyzer is binary: the packet scales like any other before its unsigned
16-bit duration is credited, and the stunned bit gates a fixed set of
behaviours rather than scaling anything `[06 §10]` `[06 R-DMG-01 §11]`.

#### EC-04 — common accepted-packet intake

Damage intake is session-bound through composition, while its receiver remains
combat-owned. Producers select a nominal amount; the combat service accepts the
packet, applies defender scaling and state effects, and records a death latch.
The normal session death visit finalizes a latched victim later. This is one
narrow seam, not a second damage framework `[06 §9.1]` `[06 §9.2]` `[06 §12.1]`.

```go
// DamageInput is a locally delivered packet before defender scaling.
type DamageInput struct {
	Victim, Attacker pool.Handle // raw slots; zero attacker is null
	Nominal          int32       // weapon nominal or established fixed amount
	Direction        uint8       // zero for non-projectile callers
	Kind             uint8
}

type DamageResult struct {
	Accepted     bool
	Amount       uint16 // packed post-defender amount; zero is a valid packet value
	DeathLatched bool
}

func (s *Service) AcceptDamage(
	w *units.World, tick uint32, in DamageInput,
) DamageResult
```

`Attacker` is a raw slot identity, not a validated live reference. At accepted
non-heal intake, the receiver reads that slot's current raw owner and snapshots
it beside the attacker link; a freed or reused nonzero slot therefore remains
a provenance source. A zero attacker leaves the previous side snapshot alone.
The later death handler always rewrites its attacker link from the death
packet, including null. Projectile routing side remains separate: neutral side
10 admits a null-shooter impact but is never substituted for raw attacker-side
provenance `[06 §9.1]` `[06 R-WPN-04 §2]` `[06 R-DMG-01 §9]`.

Combat owns an unexported weapon-nominal helper: exact-name weapon damage,
falloff, attacker veterancy and global gates. It must not read defender armor,
modifier or kills. `combat.Service.AcceptDamage` owns strict `< 30000` armored
reduction, defender veterancy and low-16-bit packing for every non-heal kind.
Fixed producers pass their known nominal: 30,000 for kinds 3, 6 and 9; reclaim
pulse for kind 5; water amount for kind 11. Heal takes its early arm before
defender scaling. This retains the armor-boundary rule without inventing a
weapon for a non-weapon producer `[06 §9.2]` `[06 R-DMG-01 §8]`.

The receiver orders its work exactly: reject only null/dead/death-latched
victims; early-return kind 10 after unsigned healing; otherwise scale, flash
240, run prior-provenance reaction except for kind 11, and record kind plus
nonnull raw attacker provenance. Kind 2 then queues stun without health
change; other kinds subtract modular signed-16 health. A local human/computer
non-positive value latches death, preserves that value, and returns from intake;
it does **not** synchronously run the finalizer. The normal victim/death visit
later invokes the existing session finalizer (usually the next master tick for
a projectile lethal). Absent/remote victims clamp to zero and continue. Kind-1
`HitByWeapon` then `TakeDamage` callbacks run only on that continuing,
non-death-latched branch; kinds 3, 5, 6, 9 and 11 never gain those callbacks
`[06 §9.1]` `[06 §10]`.

Consequently, kind-9 nominal 30,000 against a 25-kill victim at 29,000 health
leaves health 5,000 and flash 240, records cause 9, and starts no kind-1
callback. Focused acceptance must separately cover normal, paralyze, heal,
kinds 3/5/6/9/11, stale/reused raw attacker provenance, and dead-latch
rejection `[06 R-WPN-04 §2]`.

`internal/combat` owns the public entry because it already owns
`ReactToDamage`, callback bridging, paralyze-task admission, presentation event
emission and its death-notification guard. Session composition supplies its
world/tick invocation and binds the existing session death finalizer; it does
not make session a second packet receiver. Construction, movement and orders
already depend on combat and receive a session-installed narrow
`func(combat.DamageInput) combat.DamageResult` binding. They must stop
emulating a packet through direct `LastDamage*`, `Health`, `ApplyDamage` and
`DestroyBy` writes. `internal/units` stays below both packages and supplies raw
slot lookup, death latching and the later finalization visit. This fixed input
and result are not a registry or event bus.

**Caller audit at the contract baseline.** Projectile direct and area paths
currently converge in `internal/combat/service.go`'s private
`applyDamageToUnit`; `internal/combat/damage.go` has a second self-destruct
receiver. `internal/session/step.go` writes water cause and health directly;
`internal/construction/reclaim.go` writes kind 5 then calls
`units.World.ApplyDamage`; `internal/construction/factory.go` and
`internal/construction/inheritance.go` each latch kind 9 with `DestroyBy`;
`internal/movement/cargo.go` manually scales, stamps and subtracts its kind
3/6 packet; and `internal/orders/selfdestruct.go` repeats the self-damage arm.
These are the actual migration sites. Capture kind 4 and feature conversion
kind 7 are not packet producers and remain outside EC-04.

The session death finalizer remains the only finalizer. It performs cause-5's
metal-only refund after ordinary cause-5 lethal handling and before death
explosion, corpse placement and slot release; intake neither pays reclaim nor
stamps a corpse. Thus a builder's fatal kind-5 delivery only sets the victim's
death latch. When the later finalizer visits that same victim slot, it computes
and credits the cause-5 refund before releasing the slot `[05 R-WORK-01 §4]`
`[06 §12.1]` `[06 §12.2]`. C26 remains combat's deterministic area-recipient
walk before each intake; C27 keeps shake, sound, smoke and art before damage;
and C28 keeps the existing noexplode/bounce/off-map decisions before any
packet is offered. The new seam does not alter impact selection or presentation
order.

**Phased implementation and ownership.**

1. `internal/combat/damage.go`, `internal/combat/service.go` and focused
   combat tests own `DamageInput`, `DamageResult`, `AcceptDamage` and
   projectile conversion. `internal/session/composition.go` owns installing
   the combat-to-session finalizer binding. Gate C18--C21 plus
   normal/paralyze/heal.
2. `internal/session/session.go` and death-hook tests retain the existing
   normal death visit and own cause-5 finalizer ordering; the
   `internal/units` owner changes a minimal death callback only if necessary.
   There is no synchronous finalizer hand-off from intake. Phase 2 uses the
   raw attacker's owner to select the refund's controller gate: the economy
   reference and ordinary owner share that identity through construction,
   capture replacement and reuse `[05 R-WORK-01 §4]`. Keep the refund in the
   existing wide economy arithmetic until the final accumulator store; remove
   the current premature single-precision refund store. Gate C22--C25 plus
   raw stale/reuse, null-attacker, and before/after victim-slot coverage.
3. `internal/construction/reclaim.go`, `factory.go`, `inheritance.go`,
   `internal/movement/cargo.go`, `internal/orders/selfdestruct.go`,
   `internal/session/step.go`, their tests, and their composition bindings own
   migration of kinds 3/5/6/9/11. Capture kind 4 and feature-conversion kind 7
   remain direct, as established. Gate the fixed-30,000, reclaim-refund, water
   and cargo cases.
4. Remove only superseded private intake helpers after every production caller
   migrates; retain pure arithmetic with live callers. Synchronize main, then
   run `tools/check` and installed-assets `tools/check-retail`.

Tests to replace are the ones treating direct writes as packet proof:
construction reclaim's zero-clamped `World.ApplyDamage`, session water's
manual cause/health store, cargo's manual provenance/subtraction, both
self-destruct paths, and construction kind-9 `DestroyBy` sites in factory and
inheritance. Their successors must exercise the installed binding and assert
acceptance, health/provenance, non-kind-1 callback absence, delayed
finalization, and finalizer order. Existing arithmetic, area and death-order
tests remain independent.

### 2.10 Death

`Cause` is the death-cause nibble; the packed death byte is cause in the high
four bits and corpse-chain depth in the low four `[06 §12.1]` `[04 §5.1]`.
`ResolveDeath` is the shared path: severity, the synchronous `Killed` query
where the cause calls for one, the corpse chain, and the death explosion.

`explodeas` and `selfdestructas` resolve to record 0 rather than to nothing, so
a unit with no authored death weapon still explodes; cause 3 prefers
`selfdestructas` `[06 R-DMG-01 §5]`. The death blast runs **before** the corpse
is stamped, and nothing else spares the wreck `[06 R-DMG-01 §10]`. Cause 7 has
exactly two producers, both gated on the definition's feature bit
`[06 R-DMG-01 §12]`. The death timeline, `Killed` selection and kill credit are
one established sequence `[06 R-DMG-01 §3]`; health clamping, damage on a
nanoframe and resurrection ordering are another `[06 R-DMG-01 §4]`.

### 2.11 Stockpile and interceptors

`stockpile.go` carries both, because retail reaches both through the weapon
slot. A stockpile weapon keeps a signed queue count, a byte of completed rounds
and per-node progress; each work visit advances progress by five up to the
weapon's build time and requests the delta between independently truncated
cumulative metal and energy costs `[06 §11.1]`. The malformed arms — an
unarmed slot, an empty stockpile, and the byte's readers and writers — are
established rather than defended against `[06 R-WPN-05 §2]`.

Interceptor acquisition scans the current projectile prefix ascending for the
first unclaimed enemy-owned targetable record whose stored **aim point** lies
inside a separate inclusive axis-aligned coverage square; the slot stores that
candidate's current position, firing rescans, and the spawn writes the
authoritative reservation link `[06 §11.2]`. The coverage compare's width, the
blast metric and the victim signature byte are named
`[06 R-WPN-05 §10]`. Neither scan tests the dead bit, so a dead candidate is
indistinguishable from a live one and only coverage and claim state prevent a
shot.

### 2.12 Meteors

`meteor.go` is the meteor shower's arithmetic: the per-hit delay, the fixed
spawn height and fall velocity that make impact land exactly 90 ticks later,
and the origin band and lateral spread tables. Session startup selects the
mission's schema record, fixes its enabled bit from the original weapon name,
then uses a present `METEOR.TDF` `[Default]` only when that name is empty or
any numeric value is zero. That selection replaces all five values, including
the weapon name; it does not recompute enabled. A missing file or `[Default]`
returns without changing the incoming loader record. The numeric lanes after
an original empty name and no default remain Unknown; the checked host uses an
explicitly non-retail all-zero disabled record there. A present invalid
default is fatal only if selected, before a storm is installed
`[02 §6]` `[06 §6.5]`.
The three floating inputs are stored as `float32`, then converted at working
precision with signed-64 low-word truncation `[01 R-DET-01 §1]`.
Meteor geometry draws from the **CRT** stream, not the simulation stream, and
the census is four draws per storm and two per hit `[06 R-WPN-01 §6]` [I4].
Records spawned through the null-shooter path carry the neutral side byte so
their explosions credit nobody, and that side still passes the damage gate
`[06 R-DMG-01 §9]`.

### 2.13 What crosses the publication boundary

Two streams leave this package, and both are copies.

**The event stream.** `Service.Events` receives an ordered `Event` per
authoritative occurrence — shake, hit or water sound, start/trail/end smoke,
explosion or water explosion, impact, damage flash, killed, corpse. The event
carries the two halves of one authored art identity, the GAF entry name and the
bank that holds it, because a weapon's explosion art is `explosionart` inside
`explosiongaf` and neither key substitutes for the other; when either is absent
the impact draws no art `[06 R-WFX-01 §1]`. It also carries the procedurally
generated flash disc's table cursor, which every impact draws whether or not it
has art `[06 R-WFX-01 §2]`, and the weapon's smoke flag that decides whether
the impact variant appends a smoke object `[03 R-STRIP-01 §1]`.

The session's sink is where those events become presentation state: the shake
request routes to the authoritative shake phase rather than into the event
stream; sounds route to the audio emitters `[06 R-WFX-01 §3]`; the smoke events
append strip objects with the puff parameters their producer establishes
`[06 R-WFX-01 §5]`; the explosion events reach the effect pool the fourth phase
advances `[03 R-FX-01 §1]` `[03 R-FX-02 §1]`. The damage flash deliberately
stops at the sink, because its reader takes the level from the unit's own
published byte and a second emitter would double-draw it.

**The projectile view.** At tick end the session copies one `ProjectileView`
per live record: position, the weapon id, the shooter, yaw in retail's own
numbering (combat's stored yaw is half a turn from it `[06 R-WPN-05 §11]`),
pitch, the creation and expiry ticks, the burst remainder, the muzzle piece,
the target handle and point, the cached floor height, and the presentation
fields resolved from the weapon record — model, render type, lifetime, smoke
trail, and the two authored colour bytes with the signed reading of the first
that render type 4 uses as a sequence selector `[06 R-WFX-01 §4]`.

Presentation resolves art from that copy and nothing else: the client's
resolver maps the fixed engine sequence slots and the render-type dispatch
suppresses any record whose art identity is not published rather than
substituting a plausible sprite `[03 §5.4]` [I6] [I9]. Which sprite is drawn,
and how, belongs to
[DESIGN_PRESENTATION_CLIENT](DESIGN_PRESENTATION_CLIENT.md).

## 3. Contracts

These keep the numbers the weapons plan gave them, because `internal/combat`
cites them bare as `[Cn]` and `Cn`.

### 3.1 Slots and firing — C1–C9

**C1 — three slots, one pipeline order.** Each unit has three weapon slots. The
per-slot pipeline is: decrement reload → resolve target → `Aim*` →
range/medium/ballistic admission → family spawner → store reload and ammunition
→ debit. Slots are visited in ascending order `[06 §1.2]` `[06 §4.1]` [I1].

**C2 — the fire callback order is fixed.** Root allocation → the weapon's start
sound → the matching `FirePrimary`/`FireSecondary`/`FireTertiary` → `RockUnit`
→ start smoke. The start sound comes from the common initializer, so it
precedes the `Fire` callback. The dropped-family inline allocator emits neither
`Fire` nor `RockUnit`; the direct meteor path runs only the common initializer;
burst clones replay only `soundstart` when `soundtrigger` is authored, at the
refreshed parent position after the successful copy `[06 §4.1]` `[06 §4.3]`
`[06 R-WFX-01 §3]`.

**C3 — the muzzle piece is queried once and stored.** A muzzle piece is queried
synchronously before initialization and its identity is stored on the record so
a later burst clone can re-derive the muzzle world position without a second
script call. The query is the **forced `Query*`** form — cell 0 seeded 0,
`AimFrom*` never consulted — and it is a different routine from the
`AimFrom*`-with-fallback aim origin the angle solvers measure from; the two
name different pieces on most stock models (a Peewee aims from its upper arms
and fires from its flares), so the aim-time visit keeps its aim-origin piece
local and never writes the slot's muzzle word. A missing or negative result
falls back to the normal muzzle path `[06 §3.4]` `[06 §4.1]` `[06 R-P0-07]`.

**C4 — a full pool suppresses the callbacks.** Fire callbacks are not called
when the pool is full `[06 §4.1]` `[06 §5.1]`.

**C5 — a full pool still retains its side effects.** A failed allocation
retains target and trajectory validation, the muzzle query, the slot-angle
mutation, the accuracy calculation, and up to two gameplay random draws when
the spread term is nonzero. Vertical launch also retains its slot-angle rewrite
and interceptor rescan; the dropped family retains the muzzle query; meteor
scheduling retains its geometry draws. The draw count is behaviour
`[06 §4.4]` [I4].

**C6 — both costs or neither.** The debit happens only after a successful
spawner return. Both costs are prechecked, then the pipeline rechecks and
debits both or neither; the per-shot debit re-tests metal after debiting energy
`[06 §4.2]` `[06 R-WPN-01 §7]`. A stockpile launch decrements ammunition and
performs no per-launch debit.

**C7 — the reload expression, truncated in this order** `[06 §4.2]` [I3]:
`tier = min(floor(kills / 5), 5)`;
`veteranReload = floor((100 − 6·tier) × authoredReload / 100)`;
`healthFactor = 120 − floor(20 × health / maxHealth)`;
`storedReload = floor(healthFactor × veteranReload / 100)`.
The kill division is unsigned. A stockpile launch does not write reload.

**C8 — a burst is N pellets plus one anchor.** While the remaining count is
above zero the record takes the burst branch instead of the motion branch. The
muzzle is re-derived when the interval exceeds 4 or the remaining count is odd;
the clone is made before the spray and the spray prepares the next; a pool-full clone consumes
the attempt with no spray and no random draw; the anchor dies silently, with no
explosion, sound, shake, end smoke or damage. The spray perturbs a scratch
heading and never rewrites the parent's stored yaw `[06 §4.3]`
`[06 R-WPN-01 §2]`. A successful copy emits its authored trigger sound,
then sets expiry from the weapon timer when nonzero, otherwise from the
retained planar distance and scalar speed under `[06 §4.3]`; random decay
adjusts that expiry even when it wrapped to zero. The ordinary creator alone
writes the stored muzzle-to-aim distance; all other creator families retain
the previous occupant's value `[06 §6.1]` `[06 §6.3]`.

**C9 — aim-ready is granted only on an explicit nonzero return.** A new dispatch
first clears readiness; a delivered zero leaves it unchanged and an explicit
nonzero grants it. Revisiting a held request does not reset its receiver
or synthesize a delivery; its deferred completion still may arrive. The request
latch is set immediately after dispatch and a zero delivery does not clear it;
there is no timeout, and a missing script or an exhausted thread pool never authorizes fire
`[06 §3.3]` `[06 §3.4]` `[06 R-P0-07]`.

### 3.2 The pool — C10–C12

**C10 — 300 records, one authority.** Retail has exactly 300 records whose
107-byte size is record identity. Nanolathe keeps named `Projectile` records
parallel to `pool.Projectiles` and defines no second allocator.
`pool.Projectiles` is the sole count and dead authority: allocation appends at
the active-span tail and never fills a hole; retirement marks a record dead
without changing the count `[06 §5.1]` `[01 §6.1]` [I5] [I13].

**C11 — the phase captures its span once.** The projectile phase captures the
span count once at entry — **before** its ascending walk begins. Each record in
that captured span takes either the burst branch or the motion branch; there
are no separate burst and motion passes. A clone appended during the scan waits
for the next phase: it is created on tick *n* and first integrates its velocity
on tick *n+1*, including the zero-interval case where the root emits a clone
during its own creation tick. The tail compactor is the exception — it reads
the **current** count and therefore does include the clone `[06 §4.3]`
`[06 §5.1]` `[01 §6.2]`.

**C12 — compaction repairs exactly what retail repairs.** `Compact` writes each
original record's old pool index into its marker field before any copy;
delegates the stable metadata move to the pool; moves the parallel records
identically; and then repairs links in a second pass. A moved source's link is
rewritten **only** when a live record still carrying the target's old marker
exists after compaction. A removed target leaves a stale raw pointer. Sources
before the first hole never move and therefore never enter the repair table,
even when their target survives. Nothing is checked at dereference
`[06 §5.2]` [I11].

### 3.3 Aim and motion — C13–C17

**C13 — the ballistic solver.** The discriminant is compared against exactly
zero with no epsilon band; the candidate order is the plus root then the minus
root, accepting an angle strictly above `minbarrelangle` and at most a quarter
turn; the angle converts by truncating `angle × 32768 / π` `[06 §3.3]`
`[06 §6.4]` [I3].

**C14 — the malformed cases are reproduced, not defended against.** The squared
gravity term wraps as a signed 32-bit multiply; a zero `weaponvelocity` divides
by zero **after** the pool reservation, so the live count is not rolled back; a
zero horizontal component raises on divide; a negative one truncates toward
zero modulo 2³²; hypotenuse overflow keeps the low 32 bits; the most negative
integer divided by −1 raises; a wrapped deadline makes the unsigned expiry test
fire immediately; and `burnblow` with `noexplode` repeats the full expiry
impact on every visit `[06 §6.4]` `[06 §7.3]` `[06 §13.2]` [I11].

**C15 — two dispatch orders, not one.** Creation dispatch is meteor →
ballistic → vertical launch → line-of-sight/self-propelled → dropped; active
motion dispatch is self-propelled → line-of-sight → ballistic → dropped →
meteor `[06 §6.2]`.

**C16 — expiry differs per family.** Direct retires at expiry; ballistic uses
its own timer; dropped has no expiry; self-propelled expiry advances a phase.
Timer expiry without burn-blow emits exactly one trail-style puff and retires
silently — no sound, no shake, no explosion art, no damage `[06 §6.3]`
`[06 §6.4]` `[06 §7.3]`.

**C17 — the meteor shower.** Session startup reads only the selected schema;
it chooses enabled from the original weapon's emptiness and then, if that name
is empty or any numeric input is zero, replaces all five values from a present
valid `METEOR.TDF` `[Default]`. Missing defaults leave the incoming loader
record; the numeric lanes after an original empty name without a default are
Unknown, so the checked host uses a non-retail all-zero disabled record.
A selected present invalid block terminates startup or restore before partial
state can install. The source floating values are single precision, while
`trunc(30 / density)`, duration and interval conversion use working precision
and signed-64 low-word truncation. A fixed
vertical speed of −15 world units per tick from a spawn height of 1350, so
impact lands exactly 90 ticks later; the origin band 6–15 to the north; lateral
spread from the fixed sine tables; geometry from the **CRT** stream, four draws
per storm and two per hit; an unresolved weapon falls back to weapon 0; a full
pool drops the meteor silently `[06 §6.5]` `[06 R-WPN-01 §5]`
`[06 R-WPN-01 §6]` `[01 R-DET-01 §1]` [I4]. Meteor orientation is derived from
the high halves of velocity shifted left eight, not from a stored rate.

### 3.4 Damage and death — C18–C28

**C18 — the packet's acceptance gate.** Nine bytes with 16-bit ids, zero as
null and no generation token. Acceptance requires the victim's alive bit and a
clear dead latch, so a reused slot accepts a stale packet. The attacker
receives no validation `[06 §9.1]` `[06 §5.1]` [I11].

**C19 — the armor table is the weapon's own block.** The override table is
keyed by the **target definition's exact name string**, not by an armor
category, and there is no `armor.tdf`. Default damage is read as an unsigned
16-bit value; a matched override is signed 32-bit; the lookup is a
case-insensitive binary search over the table sorted at catalog compile time
`[06 §9.2]` `[06 R-DMG-01 §1]`.

**C20 — the funnel's step order** `[06 §9.2]`:

1. select the name override or the default damage;
2. multiply by area falloff, truncating toward zero;
3. apply attacker veterancy — 6 % per tier, `tier = min(kills / 5, 5)` —
   truncating the integer percentage;
4. apply the recovered global double and half gates;
5. if the target is in its armored state **and** the incoming amount is below
   30,000, apply its fixed-point damage modifier;
6. apply defender veterancy, `((25 − tier) × 4) / 100`, truncating;
7. pack the low 16 bits, modulo 65,536.

Healing bypasses steps 5 and 6. A paralyzer packet uses the ordinary incoming
scaling before its unsigned 16-bit duration credit is queued. The kill counter
is a wrapping word. Mission water damage is a producer into this pipeline, not
a separate scaling path `[04 §9.2]`. The armored term reads the victim's
runtime posture bit, never the definition's `armoredstate` flag
`[06 R-DMG-01 §8]` `[06 R-DMG-01 §2]`.

**C21 — what the packet stores.** Target id, attacker id, the modulo amount, a
one-byte relative direction, and a kind byte. The direction is a fresh bearing
from the record's current point minus the victim's heading `[06 §9.1]`
`[06 R-WPN-02 §2]`.

**C22 — death severity.**
`clamp((floor((−health) × 100 / maxDamage) + previousSamplePercent) / 2, 1, 100)`
with a truncating division by two, where the second term is the health
percentage retained from the **previous 30-tick sampling boundary** — not the
health immediately before the lethal packet `[06 §12.1]` `[04 §5.1]`.

**C23 — the `Killed` return is a corpse-chain depth.** The synchronous query
returns a value whose low four bits are the corpse-chain depth and whose high
four bits are the death cause. It is **not** a wreck probability. Depth 0
places no corpse; depth 1 selects the authored `Corpse` feature; a larger depth
follows that feature's `featuredead` link exactly `depth − 1` times, stopping
at the no-feature sentinel `[06 §12.1]` `[06 §12.2]` `[06 R-DMG-01 §5]`.

**C24 — which causes skip the query.** Causes 4, 5 and 9 skip the severity
query entirely, giving severity and variant zero; cause 7 gives severity zero
and variant one; a request made while health is positive skips the query; and a
nonzero construction fraction forces variant zero after any query
`[06 §12.1]`.

**C25 — no second callback.** Local authoritative death does not issue a second
`Killed` callback after the synchronous query `[06 §12.1]`
`[06 R-DMG-01 §3]`.

**C26 — area damage, and the absence of impulse.** Each cell discovers and
applies its first unit hit, second unit hit, then feature hit before advancing;
later discovery observes earlier damage and footprint replacement. Each call
owns its bounded memories: 20 unit entries before the radius test and 64
feature anchors after it. Overflow candidates remain unremembered and are
processed on each subsequent encounter. The blast radius is the
authored area shifted right once; the broad phase is `(radius / 16) + 1` cells
around the impact cell; falloff is
`(1 − edgeEffectiveness) · (d/R − 1)² + edgeEffectiveness` with zero distance
exactly one and no clamp on the authored edge value; the shooter is excluded
from its own blast. There is **no** impulse or pushing — do not add knockback
`[06 §9.3]` `[06 §9.4]` `[06 R-WPN-02 §10]`.

**C27 — presentation ordering at impact.** Shake → hit or water sound (a direct
target forces the hit sound even underwater) → end smoke → the land or water
explosion → **damage last** `[06 §13.2]` `[06 R-WFX-01 §3]`.

**C28 — the `noexplode` refinements.** A cached-cell feature contact suppresses
only that impact while the ladder continues; a ground bounce never reaches the
central impact; an off-map exit retires regardless; ballistic burn-blow expiry
repeats the full impact; and a linked-proximity plus second same-call impact is
reachable because the resolver never rechecks the dead bit `[06 §13.2]` [I11].

### 3.5 Stockpile and interceptors — C29

**C29 — the stockpile queue and the interceptor scan.** `BUILDWEAPON` retains a
distinct signed queue count, byte-sized completed rounds, and per-node
progress; each visit adds 5 up to the build time and requests the delta between
independently truncated cumulative metal and energy costs. Rejected admission
retries in 10 ticks, accepted incomplete work in 5, and a slot above 199 waits
300; launch is checked before production, and only a successful spawn
decrements ammunition. Interceptor acquisition scans the current projectile
prefix ascending for the first unclaimed enemy-owned targetable record whose
stored **aim point** lies within the separate inclusive axis-aligned coverage
square; the slot stores that candidate's current position, firing rescans, and
the spawn writes the authoritative reservation link `[06 §11.1]` `[06 §11.2]`
`[06 R-WPN-05 §2]` `[06 R-WPN-05 §10]`.

### 3.6 Not implemented

* **The multiplayer reconstruction path's interceptor index-versus-pointer
  anomaly.** Out of scope, and Unknown in the research
  `[06 §11.2]` `[06 R-WPN-05 §10]`.

## 4. Retail behaviour that is not a bug

Each of these has been reported as a defect and is not one. Do not "fix" them.

* **There is no moving-accuracy or aim-rate mechanism** `[06 R-WPN-03 §3]`.
  Units do not become less accurate while moving. `aimrate` and
  `movingaccuracy` are not keys at all — the spellings occur nowhere, so no
  reader can exist `[06 R-WPN-01 §9]`. The keys that *are* parsed have exactly
  one reader each: `accuracy` feeds the turret executor's spread, `tolerance`
  and `pitchtolerance` feed the angular-drift gate, and `sprayangle` feeds the
  burst spray draw `[06 R-WPN-03 §1]`. An earlier reading that called all three
  dead stores was retracted; they are read, just not where a moving-accuracy
  mechanism would need them.
* **A weapon with `tolerance = 0` fires within 150 angle units of yaw while its
  shooter is stationary and 2000 while it is moving** `[06 R-WPN-03 §2]`. A
  moving tank fires well before its turret is aligned. That is the gate, not a
  bug — and the fallback keys off the movement tier, so it applies identically
  to a hovering aircraft and a stopped tank `[06 R-WPN-01 §1]`.
* **A map with `gravity = 0` cancels every `AirStrike` order** (SC23,
  `[04 R-AIR-01 §8]`). Bombers idle there in retail too. No stock map authors
  it, so the bound is unreachable on shipped content — but substituting a
  default gravity to avoid it would invent behaviour.
* **The radar elevation bonus never widens the search**
  `[06 R-WPN-03 §5]`. It enters the squared radar radius only; a unit on a hill
  does not acquire targets further away.
* **A stale damage packet lands on a reused slot, and a compacted projectile
  can keep a stale link.** Both follow from ids with no generation token and
  from a repair table that only covers moved sources `[06 §5.2]` `[06 §9.1]`
  [I11]. They are the pool's identity model, not a missing guard.

## 5. Divergences

The weapons plan recorded **none**, and that still holds for the arithmetic:
the unguarded divides, the wrapping multiplies and the stale pointers above are
retail, reproduced deliberately [I11]. Three entries in
[SPEC_CONFLICTS.md](SPEC_CONFLICTS.md) touch this package.

* **SC6 — the fringe-anchor encoding.** The impact ladder resolves a fringe
  cell to its anchor through the world package's signed-offset reading, which
  is the reading the stamp-time writer contract settles. Combat consumes that
  resolver and takes no position of its own; the conflict is owned by
  [DESIGN_WORLD_VISIBILITY](DESIGN_WORLD_VISIBILITY.md).
* **SC18 — constructor initialization and reuse.** Allocator policy is closed.
  Projectile reservation clears only the dead bit and retained unit target;
  creator and common writes govern every other field. Actual unanswered
  constructor fields remain individual research questions, including the
  non-meteor roll writer `[06 §4.1]` `[06 §6.1]`.
* **SC23 — `gravity = 0` cancels every `AirStrike` order.** Closed as
  unreachable on stock content and kept as the clone-retail contract for any
  map that does author it (§4).

One further entry is adjacent but content-owned: closed SC24 records that retail
never parses loose `weapons\*.tdf`. Catalog discovery drops the loose VFS
winner without reopening a shadowed archive copy. Combat consumes that catalog;
the loading contract belongs to [DESIGN_CONTENT_VFS](DESIGN_CONTENT_VFS.md).

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Three weapon slots, the slot container, deterministic slot order | `[06 §1.2]` |
| Weapon definition fields and behaviour flags | `[06 §2.1]`, `[06 §2.2]`, `[02 "Weapon record"]` |
| Target categories, the per-side candidate lists, the rebuild cadence | `[06 §3.1]`, `[06 R-WPN-02 §1]`, `[08 R-AI-01 §16]` |
| The secondary list is the seen set; visibility is tested at rebuild | `[06 R-WPN-02 §3]`, `[06 R-WPN-02 §6]` |
| Manual and autonomous targets, the round-robin scan cursor | `[06 §3.2]` |
| Range, aim readiness, the drift gate, ballistic solving | `[06 §3.3]`, `[06 R-WPN-03 §2]`, `[06 R-WPN-01 §1]` |
| The weapon-query path: query callbacks, seeds, the aim-time and fire-time pipelines, creator initialization, aim dispatch | `[06 §3.4]`, `[06 R-P0-07]` |
| The `Aim*` completion receiver, the fixed-forward gate and `SweetSpot` | `[06 R-WPN-03 §6]`, `[04 R-CB-01 §6]` |
| The target-point resolver: `SweetSpot` on the target's script and its vertex-box centre, the dead-target clear, the point-target height | `[06 R-WPN-04 §1]`, `[06 R-WPN-03 §6]` |
| The static feature reference point and the animated-instance distance | `[06 R-WPN-04 §3]` |
| The engagement distance and the order-side shot-admission gate | `[06 R-WPN-05 §1]`, `[04 R-ORD-01 §7]` |
| Two admission gates, two routines; the fire gate has no target-side clause | `[06 R-WPN-05 §9]` |
| The slot control byte: bits 0–4 named, 5–7 inert, and its two writers | `[06 R-WPN-05 §3]` |
| The relative aim yaw and the relative drift pair | `[06 R-WPN-05 §4]` |
| Which angle each velocity build negates; the half-turn crossings | `[06 R-WPN-05 §11]` |
| The accuracy family's readers, and the spread's precision | `[06 R-WPN-03 §1]`, `[06 R-WPN-03 §4]` |
| The spread reaches ballistic trajectories only; the ordinary creator re-solves | `[06 R-WPN-05 §5]`, `[06 R-WPN-01 §3]` |
| The "could not fire" bit in the unit's order-event word | `[06 R-WPN-05 §6]` |
| The water branch and the targeting-upgrade gate | `[06 R-WPN-05 §7]` |
| Fire callback order, the muzzle query, the reveal-deadline stamp | `[06 §4.1]`, `[03 R-VIS-01 §6]`, `[05 R-ECO-01 §9]` |
| Costs, the reload expression, the metal re-test after the energy debit | `[06 §4.2]`, `[06 R-WPN-01 §7]` |
| Burst state, the anchor, the spray that does not rewrite the parent's yaw | `[06 §4.3]`, `[06 R-WPN-01 §2]` |
| Pool-full retention, including the retained random draws | `[06 §4.4]` |
| `burstrate`, `duration` and `smokedelay` are zero-extended, compared unsigned | `[06 R-WPN-05 §12]` |
| The pool contract: 300 records, tail allocation, dead but counted | `[06 §5.1]`, `[01 §6.1]` |
| Compaction, the old-index marker, and which links are repaired | `[06 §5.2]`, `[01 §6.2]` |
| Prerequisite projectile state | `[06 §6.1]` |
| Creation dispatch versus active-motion dispatch | `[06 §6.2]` |
| Ordinary/direct, ballistic and dropped creation and motion | `[06 §6.3]`, `[06 §6.4]` |
| Wind words are raw −2·speed·trig integers added to 16.16 positions | `[06 R-WPN-05 §8]` |
| Meteor creation, scheduling and motion; the default-block substitution | `[06 §6.5]`, `[06 R-WPN-01 §5]`, `[06 R-WPN-01 §6]` |
| Vertical launch and two-phase behaviour | `[06 §6.6]` |
| Self-propelled acceleration and guidance; cruise target points and loss | `[06 §6.7]`, `[06 §6.8]` |
| Water weapons and torpedoes | `[06 §6.9]` |
| Beams, lightning, flame and feature fire | `[06 §6.10]` |
| Per-record tick order, fixed-point integration, expiry | `[06 §7.1]`, `[06 §7.2]`, `[06 §7.3]`, `[06 R-WPN-01 §10]` |
| The collision gate and impact selection | `[06 §8.1]`, `[06 §8.2]` |
| The contact test has no radius: occupancy word plus model-top band | `[06 R-DMG-01 §7]` |
| The collision cache pair is never reset; where the floor scratch is written | `[06 R-DMG-01 §13]`, `[06 R-DMG-01 §14]`, `[06 R-WPN-02 §7]` |
| The damage packet, its acceptance gate and the direction byte | `[06 §9.1]`, `[06 R-WPN-02 §2]` |
| The scaling order, the name-keyed override table, healing and paralyzer | `[06 §9.2]`, `[06 R-DMG-01 §1]` |
| `damagemodifier`, `armoredstate` and the kill-count consumers | `[06 R-DMG-01 §2]` |
| The armored bit's instance field and the control-byte gates in the intake | `[06 R-DMG-01 §8]` |
| The null-shooter side passes the damage gate | `[06 R-DMG-01 §9]` |
| Area damage: blast radius, broad phase, falloff, the shooter exclusion | `[06 §9.3]`, `[06 R-WPN-02 §10]` |
| There is no impulse | `[06 §9.4]` |
| The paralyzer, and what the stunned bit gates | `[06 §10]`, `[06 R-DMG-01 §11]` |
| Stockpile production, its queue, and its malformed arms | `[06 §11.1]`, `[06 R-WPN-05 §2]` |
| Interceptor acquisition, coverage, claim and the victim signature | `[06 §11.2]`, `[06 §11.3]`, `[06 R-WPN-05 §10]` |
| Unit death, severity, the corpse chain, cause producers | `[06 §12.1]`, `[06 §12.2]`, `[04 §5.1]` |
| The death timeline, `Killed` selection and kill credit | `[06 R-DMG-01 §3]` |
| Health clamping, damage on a nanoframe, resurrection ordering | `[06 R-DMG-01 §4]` |
| `explodeas`/`selfdestructas` resolve to record 0; the `corpse` key | `[06 R-DMG-01 §5]` |
| The death blast runs before the corpse is stamped | `[06 R-DMG-01 §10]` |
| Cause 7's two producers | `[06 R-DMG-01 §12]` |
| `hitdensity` does not exist | `[06 R-DMG-01 §6]` |
| The death explosion record carries no velocity and no shooter | `[06 R-WPN-02 §5]` |
| `selfdestructcountdown` | `[06 R-WPN-02 §8]` |
| Weapon-driven feature fire and the ignition test | `[06 §13.1]`, `[05 R-FEAT-01 §8]` |
| Impact effect, sound and smoke ordering | `[06 §13.2]`, `[06 §13.3]` |
| The reaction step: observer notice, retaliation, flash, under-attack notice | `[06 R-WPN-04 §2]`, `[04 R-MOV-03 §7]`, `[08 R-AI-01 §11]`, `[04 R-STANCE-01 §3]` |
| The flash byte's per-visit decrement — 240 visits, not sixteen | `[06 R-WPN-04 §4]` |
| Mission water damage as a producer into the funnel | `[04 §9.2]` |
| Presentation keys, art binding and the loop byte | `[06 R-WFX-01 §1]` |
| Explosion selection at impact, the explosion pool, the record-0 answer | `[06 R-WFX-01 §2]` |
| Impact and fire sounds: registry, selection, emitter gates | `[06 R-WFX-01 §3]` |
| Projectile render types, and which weapons author them | `[06 R-WFX-01 §4]` |
| Smoke puff parameters per producer | `[06 R-WFX-01 §5]`, `[03 R-STRIP-01 §1]` |
| The presentation random-draw census | `[06 R-WFX-01 §6]` |
| Where combat's events land in the effect pool and its strips | `[03 R-FX-01 §1]`, `[03 R-FX-02 §1]` |
| What presentation may read of a projectile, and its shadow anchor | `[03 §5.4]`, `[03 §2.4]` |
| Evidence basis for this category | `[06 §14]` |

## 7. Not implemented and open

This section lists open combat contracts; it is not a census claiming that
`internal/combat` has no source-question markers. The retained freed-target
state and non-meteor roll writer questions are recorded in [06 "Missing and
unknown"]. Two comments point at markers that no longer stand — one in
`slots.go` referring to malformed-state TODOs on the reload computation, and
one in `damage.go` referring to a TODO on the water-damage eligibility test.
Both sites now state their behaviour inline. SC18 settles allocator policy;
projectile reuse follows the explicit reservation and creator writes.

Open items the contracts above carry:

* **An out-of-range movement-state operand reaching the reload formula.** The
  health half of the reload plan's original question is closed — health is a
  signed 16-bit field, the heal kind clamps unsigned to the definition's 32-bit
  maximum, the damage kinds preserve a negative result for a locally owned
  victim and clamp to zero otherwise, and the paralyze kind never writes health
  `[06 R-DMG-01 §4]`. What an out-of-range state value does at the reload site
  is still open; the decider is a static trace of that site's state operand
  `[06 §4.2]`.
* **A zero maximum health divides by zero in the reload's health factor.** The
  fault is reproduced rather than guarded, after the pool reservation, so the
  count is not rolled back `[06 §4.2]` [I11].
* **Whether any stock unit stores a negative slot-distance word.** The
  ballistic creator's flight-time divide is unsigned, so a negative word yields
  a very large flight time rather than a negative one. The arithmetic is
  Established; the population is Unknown, and the code reproduces the unsigned
  divide without defending against it `[06 §6.4]` [I11].
* **The geometric meaning of the ballistic launch's gravity pre-decrement.**
  Reproduced as written; whether an implementation may simplify it is Unknown
  `[06 §6.4]`.
* **The interceptor scans cannot distinguish a dead candidate from a live one**
  because neither scan tests the dead bit; only coverage and claim state
  prevent a shot. That is the established behaviour, not a gap
  `[06 §11.2]` `[06 R-WPN-05 §10]`.
