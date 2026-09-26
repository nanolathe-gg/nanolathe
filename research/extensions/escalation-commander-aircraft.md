# Escalation Gold 10.2.0 commander and aircraft scripts

## Evidence scope

**Established — bounded authored-content contract.** This document describes
the `ARMCOM`, `CORCOM` and `ARMATLAS` programs in Gold 10.2.0, plus their
referenced research definitions. It does not describe every commander decoy,
combat aircraft, transport or historical engine patch. Release identity and
the documented system boundaries belong to
[Escalation engine package](taesc-engine.md); extension query semantics belong
to [Extended script ports](script-ports.md).

**Established — primary evidence.** The release's `ESC_READ_ME.txt` sections
on commander upgrades, `Offscreening`, `Stacking` and `Commanders/Decoys`
document intent. Authored COB control/data flow and FBI/3DO operands were
inspected on 2026-09-25. The relevant content identities are:

| Archive and entry | SHA-256 |
|---|---|
| `TAESC.gp3`, `scripts/ARMCOM.cob` | `ec89ede3c0d21ac674ff84747eef91dc72e445e181f7a1c7a6fa5f1db4646b13` |
| `TAESC.gp3`, `scripts/CORCOM.cob` | `7b1f8196f55cb5450d4984597f8d73244a16de35a54f8640dbdbff7c70d35914` |
| `TAESC.gp3`, `scripts/ARMATLAS.cob` | `43de553dc894bef3bac6f8d7b82f3b0fcc1a6737772bf009e06daf4d90da9d03` |
| `TAESC.gp3`, `unitsE/ARMCOM.fbi` | `478aa1093dfc6f007078be5e52c445060384c0fde9066cdc4a0fef550bf26ebb` |
| `TAESC.gp3`, `unitsE/CORCOM.fbi` | `840d508ce9f27dce6d807a08f05473a22a361cb731bd9f6171c22d491d085103` |
| `TAESC.gp3`, `unitsE/ARMATLAS.fbi` | `e645ca5a97603cfe06041293ff10ad6703696d0c5e88d93a5a2077c07f5e7c1b` |
| `TAESC.gp3`, `objects3d/ARMATLAS.3do` | `67b215a4eb8fe4e3825c9ee9736e736784c094bf935d55d3ec423243e0093df8` |
| `T3ESC.ufo`, `objects3d/ARMTECH.3do` | `fd516d71364d3dc8a3fcd56822b6eb2bb80a3a0a3cc475afb0c332bb784bab52` |
| `T3ESC.ufo`, `objects3d/CORTECH.3do` | `f5e37840e4bb1540aceb63683e372428c72b9f67275397dbcab6de4f4f019827` |

**Established — evidence distinction.** No third-party patch executable or
DLL was analyzed. Source-derived claims below describe the authored programs;
runtime observations describe Nanolathe executing those programs over the
retail assets plus Gold 10.2.0's Step 2 archives. They do not establish every
behavior of the historical Gold engine.

## Commander research discovery

**Established — authored scan.** Each commander's `Create` starts `Detect`.
After a random 0.5–2.5 second delay it reads the inclusive identifier interval
from ports 69 and 70 and caches its own identifier and owner. Repeated scans
count completed research buildings belonging to that cached owner. The
identity test uses the target's full fixed-point model top from port 11:

| Commander | Research definition | Model-top marker (16.16 raw) |
|---|---|---|
| `ARMCOM` | `ARMTECH` | `2919331` |
| `CORCOM` | `CORTECH` | `1732896` |

**Established — membership.** Completion means port 73 returns zero. Owner
equality uses port 72; an alliance alone is insufficient. There is no distance
test for research membership. The opposite faction's marker does not count.
Dead units cease matching the live target-height reader. The owner operand
is captured when `Detect` starts rather than refreshed every scan.

**Established — entry condition and polling.** A positive research count
enters the upgraded branch when the commander's `setSFXoccupy`-supplied class
is nonzero. Both programs wait a random 2.5–5 seconds after each scan. They
also contain a conditional longer sleep for a scan flag that their authored
nonzero Boolean expression sets during the identifier walk; that additional
sleep is not reached in the observed nonempty interval. No engine-level
research registry or ownership broadcast is involved.

## Commander effects and removal

**Established — authored effects.** Entering the upgraded branch sets the
upgrade state, sets ordinary armor, removes the basic primary-fire script's
800 millisecond post-flash delay, sets the kinetic-hit wait to zero and
switches the basic model pieces for the upgraded pieces. The first switch
requests an ordinary bitmap explosion effect. The upgraded primary aim and
query paths use different body/muzzle pieces. Both definitions already carry
the secondary `VSPAM_COM` weapon; `AimSecondary` waits for the upgrade state
before returning a ready result. The research building does not create a new
weapon slot or replace the immutable unit definition.

**Established — count use.** The inspected research branch distinguishes
zero from at least one. It does not multiply health, damage, movement or fire
delay by the counted number of research buildings. Two buildings preserve the
same upgrade when one is removed. This establishes redundancy in these
programs, not the readme's broader claim that upgrade benefits accumulate.

**Established — removal.** When the next scan counts zero after an upgrade,
the program clears upgrade state, restores the basic primary-fire delay,
emits effects from the upgraded pieces, hides them and reveals the basic
model again. Not every variable assigned during upgrade is reset: the
kinetic-hit wait, initially 5000 milliseconds and set to zero on upgrade,
is not restored in this removal branch. The secondary callback's ready wait
again requires the upgrade state. Removal is observed by polling rather than
an immediate callback from research-building death.

**Established — baseline kinetic armor.** Both commander definitions author
`DamageModifier=0.25`, and `Create` sets armor. Before upgrade, a surviving
ordinary hit can clear armor for 5000 milliseconds, then restore it. A
per-script latch prevents subsequent hits during that interval from starting
another wait; this is a first-hit interval, not a timer restarted by every
hit. The branch excludes the water occupancy class and upgraded commanders.
The ordinary damage receiver scales the first nominal 100 hit to 25 before
the callback clears armor; a second hit during the unarmored interval takes
100 in the observed zero-veterancy session.

**Unknown — broader commander claims.** Decoy programs, capture/transfer
reinitialization, the cumulative-benefit documentation, every movement and
weapon-animation variant, water-class behavior, and behavior of an already
ready weapon at the exact research-removal boundary are not established by
the representative tests below. The cached owner and incompletely restored
kinetic wait must not be silently replaced with inferred rules. These cases
need their authored caller/lifecycle trace and bounded execution scenarios;
historical differences require appropriate source or manual Gold evidence.

## Atlas flight and stack checks

**Established — lifecycle.** `ARMATLAS` starts `TrackStatus` after completion.
An ordinary takeoff raises its activation callback; after 1500 milliseconds
that callback enables and starts `OffScreenCheck`. Deactivation clears the
checking flag. The checking loop sleeps a random 2700–3300 milliseconds
between visits. Its penalty state is held in ordinary COB statics.

**Established — stack predicate.** Every visit first clears the stack flag,
hides `stacker`, and sets armor. It then scans the inclusive port-69/70 range
for a different allied completed unit whose port-11 model top is `3639296`,
the inspected Atlas marker. It compares the target's packed XZ with the
reading transport's base-piece packed XZ, using the authored normalized
packed-distance calculation and its overflow guard. A distance at most
**two raw 16.16 units**, not two world units, sets the stack flag, shows the
indicator and clears armor. Positions have already been quantized by the
packed position queries, so this selects coincident packed horizontal
positions in the bounded scenarios.

**Established — scope of the penalty.** The predicate does not compare Y,
vertical ordering or which identifier is first. Two Atlas transports can
therefore both be penalized. The scan does not clear either aircraft's
health; it removes their ordinary `DamageModifier=0.25` armor protection.
Separation restores armor and hides the indicator on a later scan, rather
than synchronously when positions first diverge. The authored automatic
unload branch requires a clear stack flag; its load branch does not include
that particular gate. This resolves the contradictory readme unload wording
for this inspected automatic-unload branch only.

## Atlas off-map debt and cargo

**Established — authored detection and debt.** The flight check asks port 16
for the ground height beneath four named wake pieces. The ordinary ground
query returns a negative sentinel outside valid terrain. Any negative result
adds three to penalty debt, capped at sixty. If all four samples are
nonnegative the visit subtracts three. When the resulting debt is nonpositive
and the separately sampled near-ground flag is false, it clamps debt to zero
and hides the `naughty` indicator. This is a sample-based mechanism; the
script does not measure elapsed seconds outside the map.

**Established — indicator details.** A debt of at least three shows the
warning on a fully in-bounds visit. The partial-boundary show expression
checks the first three sample results, with the third repeated; it does not
include the fourth. A fully outside visit can accumulate debt without showing
the warning until a later boundary/inside sample. `TrackStatus` supplies the
near-ground flag by testing whether the base piece is less than 12.5 world
units above its queried terrain height. These authored details preclude
treating the icon as an exact outside-map or elapsed-time measurement.

**Established — cargo gates.** `TrackStatus` visits its mode branches every
500 milliseconds after its ordinary pose work. Automatic load uses standing
fire state zero; automatic unload uses state one. Both require exactly zero
off-map debt and the script's low-movement-rate flag. The unload branch also
requires no stack penalty and existing cargo. The low-rate flag is set by
`StopMoving` and `MoveRate1`, and cleared by `MoveRate2`/`MoveRate3`.
Local-controller queries gate the actual attach/drop requests. Automatic
loading performs its own owner check, model-height size accounting and
capacity check; the bounded runtime test uses one own `ARMFLASH`, not an
assumed universal cargo size.

**Established — current-host result.** An Atlas automatically loaded that
unit, carried it outside the map under an ordinary move command, then
returned with a visible penalty. After an on-map selection and unload-stance
command, it retained cargo while the debt recovered. It later hid the warning
and automatically unloaded. No test injected a port answer, started a private
callback, changed the penalty static, or manually attached/detached cargo.
The selection was issued after return because an off-map selection
replacement does not select this unit in the observed host interface.

## Persistence and acceptance

**Established — Atlas persistence.** Saving during the returned aircraft's
active debt interval preserved its complete ordinary COB image and cargo
linkage. The restored aircraft retained cargo while its warning remained,
then cleared the warning and unloaded as the restored timer/scan proceeded.
The script's debts, stack flag, speed flag and pending calls need no separate
engine-owned penalty record or new save extension.

**Established — reproducible Nanolathe checks.** The retail-tagged tests in
`internal/session/escalation_commander_aircraft_retail_test.go` use Gold
10.2.0 over the reference retail install, `ashap plateau`, Modern gameplay
with the Escalation profile, a 100-unit player limit, deterministic seeds and
disabled AI planning. They exercise ordinary commands and the real unit,
COB, movement, combat and save services:

- `TestEscalationCommanderResearch`: both factions reject wrong-faction,
  allied and unfinished research; baseline kinetic armor drops and recovers;
  completed own research switches geometry and allows the secondary weapon
  to fire during an ordinary attack; upgrade armor survives a hit; removing
  one of two research buildings preserves the upgrade, and removing the last
  restores the basic model.
- `TestEscalationAtlasStackAndRecovery`: two coincident, commanded transports
  lose armor and display stack indicators; separate destinations restore
  armor and remove the indicators; actual nominal 100 damage changes from
  100 during the penalty to 25 after recovery.
- `TestEscalationAtlasOffMapCargoAndSave`: ordinary automatic loading,
  off-map movement, return with retained penalty, deferred unloading and
  continuation across a retail save/load boundary.

**Established — engine coverage.** These checks require no additional engine
port or callback. Existing target-height, packed-position and ground-height
queries, the recorder's adopted query ports, activation/armor writes,
movement callbacks, aim completion and cargo attachment carry the authored
behavior. Strict 3.1's closed extension-port table is intentionally outside
this compatibility mode. The current-source air-stack splash fix is a
separate combat rule, not the mechanism tested here.

**Unknown — remaining aircraft scope.** T3/T4 combat aircraft's weapon gates,
radar/jammer suppression, other transport programs, near-ground debt behavior
and the warning's final rendered appearance remain outside this bounded
acceptance. The Atlas checks establish automatic cargo blocking/recovery and
armor loss, not those broader readme claims. Each missing branch needs its
actual authored program/definition and a corresponding ordinary session
scenario; no universal off-map penalty or first-aircraft exemption is inferred.
