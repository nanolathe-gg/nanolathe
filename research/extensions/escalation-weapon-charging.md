# Escalation Gold Sentinel weapon charging

## Evidence and scope

**Established — artifact and authored scope.** This bounded contract covers
an unupgraded `ARMHLT` (Sentinel) receiving charging from completed `ARMFIELD`
(Obelisk) units in TA: Escalation Gold **10.2.0**, inspected on 2026-09-26.
The source is `TAESC_GOLD_10_2_0_FULL.rar`, 350859995 bytes, SHA-256
`a9873e551d7fa72ad2f74ca37d8bbc7c873043d178979c842bbf8ed667eea2c3`,
from the project's [downloads page](https://taesc.tauniverse.com/?p=downloads).
The inspected sources in its Step 2 content directory are:

| Archive | Authored source | Role |
|---|---|---|
| `TAESC.gp3` | `unitsE/ARMHLT.fbi`, `scripts/ARMHLT.cob`, `objects3d/ARMHLT.3do` | Sentinel definition, detector, aim/fire programs and model |
| `TAESC.gp3` | `weaponE/TAESC.tdf`, `LASER_HEAVY` section | Sentinel's sole weapon |
| `T3ESC.ufo` | `unitsE/ARMFIELD.fbi`, `scripts/ARMFIELD.cob`, `objects3d/ARMFIELD.3do` | Completed field provider and its model identity |

The Sentinel COB SHA-256 is
`d9ac88d5e27c147a0ab3e0af9de4844eab6f181bfeee6c35b32fa07a33218cd7`;
the field COB SHA-256 is
`945f4d8af095d001c755a27fba628540f363e3ff7a7599d060e87fe233887129`;
the field model SHA-256 is
`ab7075314f451a92a1fb6c61f8f7878a21de1e58f0f5fa1e8787ea81fd046f4d`.
The content profile exposes the renamed FBI and weapon directories through
ordinary `units/` and `weapons/` logical paths.

**Established — documentation boundary.** Step 1's `ESC_READ_ME.txt`,
“CHARGING”, names heavy laser towers among chargeable weapons. It gives no
universal weapon cadence or damage multiplier. This contract derives the
Sentinel's behavior from its authored `Create`, `Detect`, `AimPrimary` and
`FirePrimary` programs. It complements the resource-only
[resource adjacency contract](escalation-adjacency.md#evidence-and-scope).

**Established — method boundary.** These are independently worded descriptions
of authored FBI, COB and model data, followed by observations in Nanolathe.
No third-party patch executable or DLL was inspected. Extended getter meanings
come from [Extended script ports, Port table](script-ports.md#port-table),
with that document's pinned current MIT TADR source scope; retail COB and
query arithmetic remain owned by the retail specification and
[COB format](../formats/cob.md). This does not establish historical Gold DLL
implementation equivalence.

## Sentinel detection and field eligibility

**Established — authored scan.** `Create` initializes the unupgraded state,
sets the firing wait to 1150 milliseconds, and starts `Detect` after the
recipient reports completed construction. The detector first sleeps a random
750–2250 milliseconds, reads the minimum and maximum unit identifiers through
ports 69 and 70, and retains those bounds. Each pass clears its charge score
and scans that inclusive identifier range in ascending order.

**Established — field predicate.** In the field branch a candidate must pass
the recipient's directional alliance query through port 74, have the full
16.16 model-height value **2184856** through port 11, and report zero
unfinished construction through port 73. The authored field model supplies
that height. A candidate within **377.5 world units**, inclusive, of the
recipient's base piece contributes **two** charge points. Distance is the
script's packed-position subtraction, signed-component repair and guarded
port-13 query, as described in
[Resource adjacency, Distance and rounding](escalation-adjacency.md#pairing-families-and-range).
The field branch does not read activation, energy balance or visibility.
An incomplete field contributes no charge.

**Established — one charge latch.** For the unupgraded Sentinel, a score of at
least one turns on a single charged latch and, after the authored one-millisecond
sleep, changes the firing wait to **550 milliseconds**. A zero score clears an
existing latch and restores **1150 milliseconds** after the same short sleep.
Two eligible fields contribute four points but select the same charged state
as one field's two points. Removing one of two fields therefore preserves the
benefit; removing the last restores the ordinary wait on the next scan.
The charge indicator is shown only when the controller-locality query through
port 75 succeeds; that display test does not gate the firing-wait change.

**Established — polling in this fixture.** With no recognized nearby providers,
the detector sleeps a random **150000–225000 milliseconds** between passes.
A qualifying nearby completed field selects **6750–13500 milliseconds**.
Thus inserting a field after an isolated scan need not take effect promptly,
and removing the last field is sampled at the next pass. These are the two
selected pacing branches in the acceptance fixture; other distance bands and
other provider families are outside its coverage.

## Firing behavior

**Established — authored readiness delay.** The baseline Sentinel alternates
its two muzzle flashes. `FirePrimary` sets a firing-busy latch, shows the
selected flash for **50 milliseconds**, hides it, sleeps the current firing
wait, and clears the latch. `AimPrimary` performs its turret and gun turns,
then polls that latch with **50-millisecond** sleeps before returning ready.
Charging changes that ordinary aim/fire handshake. These branches neither
replace the weapon nor edit its definition's reload or damage.

**Established — authored weapon.** The FBI names only `LASER_HEAVY`; the
compiled weapon has six ticks of nominal reload, range 600, energy cost 150
per shot and default damage 300. Its ordinary reload remains shorter than
the script's readiness wait in both tested states. These are Sentinel-specific
values, not a general formula for charged defenses.

## Session acceptance

**Established — Nanolathe observation.** The asset-tagged
`TestEscalationWeaponCharging` loads the actual Gold content over retail,
binds the existing Modern rule set and profile sources, and uses an isolated
fixture on Expanded Confluence with fixed simulation/CRT seeds of 7. The
complete Sentinel receives one ordinary human ground-attack command aimed
300 world units away. The player is kept funded and the initial computer
planner is parked. Real field scripts run normally. No detection query,
callback, script static, deadline or weapon definition is substituted.

A read-only callback lifecycle sink records actual `FirePrimary` starts,
which follow accepted projectile allocation. Four-hundred-tick samples after
natural detector waits show these constant consecutive-shot intervals:

| Fixture state | Observed interval |
|---|---:|
| Isolated Sentinel | 36 ticks |
| One completed field 200 world units away | 18 ticks |
| Two completed fields 200 world units away | 18 ticks |
| First field removed, second remains | 18 ticks |
| Last field removed | 36 ticks |

The test checks repeated firing, the retained ground target and weapon,
unchanged nominal reload, ordinary finalization of removed providers, and
absence of COB diagnostics. Reclaim damage removes providers through the
normal accepted-damage/unit lifecycle without their nuclear death explosions
obscuring the test. The initial insertion allows the longest isolated scan
sleep; subsequent transitions allow the longest nearby-field sleep plus a
shot crossing the transition. It introduces no engine API or gameplay policy.

## Unknown and untested scope

**Unknown — other weapon recipients and upgrades.** This acceptance does not
establish charged behavior for Core weapons, artillery, other tower families
or the Sentinel's upgraded form. Each needs its own authored program and
weapon audit followed by an appropriate session observation; this result
must not become a universal charging multiplier.

**Unknown — broader combinations and historical implementation.** Carrier
providers, hostile disruption, shields, mixed ownership, save/load during a
charging transition and simultaneous upgrading are outside this fixture.
Their complete interactions require bounded source audits and acceptance
cases. Exact historical Gold DLL semantics require appropriately licensed
source, primary documentation or permitted manual observation of that version.
No missing engine behavior was exposed by the tested Sentinel/field sequence.
