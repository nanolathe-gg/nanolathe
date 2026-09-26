# Escalation Gold 10.2.0 shields

## Evidence scope and identities

**Established — scope.** This contract describes selected authored building
shield programs in Gold 10.2.0. It does not describe retail behavior, approve
new gameplay, or infer the shipped engine's missing mechanics from its
readme. The release identity and engine boundary are owned by
[Escalation engine package](taesc-engine.md). The eight extension ports used
by this content are owned by [Extended script ports](script-ports.md).

**Established — sources.** The release's `ESC_READ_ME.txt`, section
`SHIELDS`, documents building-only protection, 75% absorption, overlapping
coverage without stacking, hit-triggered generator energy use, and slow
generator self-healing. The independently described control and data flow
below comes from authored COB, FBI and 3DO entries, inspected on 2026-09-25.
The coverage investigation used those authored assets. The later, explicitly
authorized [passive-healing investigation](#passive-generator-healing) also
examined the shipped executable and installed DLL helper. The mount is the
original retail assets followed by Gold 10.2.0's Step 2 directory, using the
Escalation content profile.

| Archive and entry | SHA-256 |
|---|---|
| `T3ESC.ufo`, `scripts/ARMSHGEN.cob` | `fddd3db8cd59909c80a58eb666e30fbad5cb6f45c9882801e351bb1a52f37102` |
| `T3ESC.ufo`, `unitsE/ARMSHGEN.fbi` | `486b0df0309891fa289038887faf4ffab9ab31dc06f287ce916142918a0eb606` |
| `T3ESC.ufo`, `objects3d/ARMSHGEN.3do` | `29d470175106f81459a7a459376b050de045986c665715844459d68d54e84276` |
| `T3ESC.ufo`, `scripts/CORSHGEN.cob` | `328de4126c0fbf40339d06783e9419ef68c2287a9a51a6135230abaf24b3feac` |
| `T3ESC.ufo`, `unitsE/CORSHGEN.fbi` | `f36a2ae31e8a3eb4893345e02542cb4ebeaaffff24c5a1da11517274f11803d1` |
| `T3ESC.ufo`, `objects3d/CORSHGEN.3do` | `c2f69bb02cdecdd893ed4ca8c2ac8f119a0f3fde382f9f530805724dad937e40` |
| `TAESC.gp3`, `scripts/ARMMEX.cob` | `0ba722f3ee487ebb57453e1dd82226ca4502ef9a6a6b7c0e6d0cb4e08e69818e` |
| `TAESC.gp3`, `unitsE/ARMMEX.fbi` | `b92f81f8796c675f28a61a4d8deb920fab2ae31bd352615a21868750e0f80a06` |
| `TAESC.gp3`, `scripts/ARMFUS.cob` | `c3649449ae49ce943eb66fccc22fa240af421fa502b99e08429ee15f77ccd83e` |
| `TAESC.gp3`, `unitsE/ARMFUS.fbi` | `fca0604910eeec56a4d975cc6a842e372746e1fd599717986561b57278c3ce33` |

**Established — implementation evidence has a separate scope.** The
Nanolathe observations below use these authored files, ordinary unit creation
and `Create` callbacks, the authoritative tick, actual combat damage intake,
the real economy service and retail save/load. They establish what Nanolathe
currently does, not what the historical Gold executable did. The current
MIT TADR revision used for extension interfaces is
`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f`; its repair helper's boundary is
recorded under [Community patch engine behavior, CP-DMG-4](community-patch-engine.md).

## Coverage discovery and recipient behavior

**Established — authored discovery.** A generator does not enroll nearby
units in an engine-owned aura. Each inspected recipient's `Detect` callback
searches the inclusive identifier interval supplied by ports 69 and 70. It
tests the reading owner's directional alliance relation through port 74,
uses port 11's full fixed-point target-model height as a type marker, and
uses port 73 to distinguish completed units. It does not query a provider's
energy, activated state or current armored state. A dead slot stops matching
because the ordinary target-height reader returns zero for it.

**Established — authored marker/radius table in ARMMEX and ARMFUS.**
Distances are world units; comparisons include the boundary. The completed
markers contribute one coverage count apiece.

| Target model top (16.16 raw) | Matching compiled definition | Radius |
|---|---|---|
| `10485760` | `ARMSHGEN`, Aegis | 377.5 |
| `10608640` | `CORSHGEN`, Corona | 505 |
| `10649600` | `ARMSHGEN_UPG` | 570 |
| `8192000` or `8601600` | No matching definition in the inspected mounted catalog | 255 |

**Established — incomplete upgrade handling.** An unfinished target with
the `ARMSHGEN_UPG` marker subtracts one coverage count within 377.5 instead
of supplying its completed radius. Ordinary unfinished Aegis and Corona
targets do not supply coverage. ARMMEX also uses unfinished or distant
recognized candidates to choose its next polling delay.

**Established — distance operands.** The recipient subtracts its base-piece
packed XZ position from the target's packed XZ position. Its authored
integer arithmetic normalizes that packed difference, checks a halved
component form against an overflow guard through the ordinary distance port,
substitutes a fixed distant packed point when the guard fires, then asks the
distance port for the resulting length. The radius is not a footprint or
LOS test. Nearby integer-coordinate tests below exercise this actual code;
they do not replace it with an independently calculated circular-area list.

**Established — ARMMEX membership and timing.** `Create` waits for completion
before starting `Detect`. The first scan follows a random 1–3 second delay.
With a coverage count of at least one, an uncovered extractor sets its own
coverage flag, conditionally shows its shield piece, waits 50 milliseconds,
then writes the ordinary armored port. With count exactly zero it hides the
piece and clears the coverage flag, waits the same interval, and clears
armor unless its separate upgrade flag is set. A negative count takes
neither transition; it is not equivalent to zero.

**Established — ARMMEX polling.** A nearby completed Aegis or Corona selects
a random 9–18 second delay after a scan. Other recognized candidate bands
select 20–30, 32–48, 50–75, 80–120 or 128–180 seconds. With no recognized
nearby candidate it selects 200–300 seconds. Thus coverage changes are
eventually observed by a script scan, not immediately when a provider is
created or removed. Removing the final provider after an already-covered
scan is normally noticed within the existing 9–18 second wait; a newly
arriving provider may wait for a much longer previously selected delay.

**Established — ARM fusion is a distinct recipient branch.** ARMFUS `Detect`
sets its coverage flag and visual, but `SmokeUnit` owns its armor write.
With either coverage or upgrade, health below 66 percent clears armor and
health above 66 percent sets it; exactly 66 percent retains the previous
armor state. With both coverage and upgrade it sets armor regardless of
that health comparison. With neither it clears armor. These are ordinary
integer health-port comparisons. Its first scan waits 0.75–7.5 seconds;
subsequent scans wait 7.5–15 seconds. This source does not justify applying
one universal shield transition to every building.

## Absorption, overlap and disruption

**Established — damage mechanism.** The inspected generator and recipient
definitions author `DamageModifier=0.25`. Their COB programs manipulate the
ordinary armored bit. There is no separate shield health pool, projectile
collision surface, intercepted-shot deletion, or damage redirected from a
recipient to its generator in these programs. Ordinary armor scaling and
its exclusions remain owned by
[06 Damage](../retail-executable-spec/06-weapons-projectiles-and-damage.md).
In Nanolathe's normal damage receiver, a zero-veterancy armored unit takes
25 from a nominal ordinary packet of 100. The ordinary high-damage armor
bypass and damage-kind branches still apply; the readme's broad description
does not establish an exception to them.

**Established — overlap.** The ARMMEX coverage count chooses one Boolean
armored state, so two providers do not apply the multiplier twice. Removing
one provider leaves the other counted at the next scan. The count does not
encode the energy state of either generator.

**Established — provider self-armor.** Aegis and Corona start their own
`Detect` loops after completion and a random 0.5–5 second delay. They scan
the same source markers, including themselves. Each completed friendly
source contributes minus two to a disruption score; an unfinished Aegis
upgrade contributes plus two in its smaller radius. Each recognized
completed enemy disruptor contributes plus four. At a score of at least two
they hide the shield and clear their own armor; below two they show it when
local and set armor. Aegis also clears armor while its upgrade/work flag
is set. Their repeat scan delay is 5–10 seconds.

**Established — authored enemy markers.** ARMMEX and ARMFUS subtract one
coverage count for a completed enemy `ARMWALK` or `CORTSAR` marker within
2690, and for `ARMCRAWL`, `CORDECI`, `ARMSCRAM` or the unmatched marker
`1183517` within 1345. Provider self-scans use the same enemy groups and
radii with their plus-four score. The self-scan and recipient-scan weights
are distinct; merely observing a provider's own armor cannot determine all
recipient states.

**Established — bounded disruption acceptance.**
`TestEscalationShieldDisruption` places an enemy Prophet (`ARMCRAWL`)
within the authored short radius of an Aegis and its extractor. With one
provider the ordinary scans clear both armor states and a nominal 100 hit
deals 100 to the extractor. Two completed friendly providers outweigh that
one disruptor and restore 25-damage intake. Removing the second provider
disables protection again; removing the Prophet restores it. No script
variables or detector results are injected.

**Unknown — untested branches.** Other disruptor types, unfinished upgrades,
and marker values with no matching compiled unit still require corresponding
ordinary lifecycle scenarios. Commander kinetic
shields and other recipients' upgrade armor are separate script branches
and are not covered by this contract.

## Hit-triggered energy and visuals

**Established — authored timer.** Aegis and Corona implement the energy
interval in `HitByWeapon`. The callback cancels the earlier thread bearing
its signal mask, installs that mask on the new thread, requests a bitmap
effect, writes activation on, sleeps 1000 milliseconds, then writes
activation off. Aegis gates this sequence while its upgrade/work flag is
set. A later hit therefore restarts one timer; it does not add a second
independent drain. Only hits on the generator run its callback. A hit on a
covered extractor does not activate either provider.

**Established — resource mechanism and shortage.** Aegis authors positive
`EnergyUse=8000`; Corona authors `EnergyUse=10000`; both start deactivated.
The callback uses the ordinary activation/economy path, whose accounting is
sampled by economy settlement. These scripts do not test available energy
or unpaid carry before setting armor, and do not remove armor on shortage.
In the observed Nanolathe session, insufficient energy accumulated unpaid
upkeep, while provider and extractor retained their armor and the timer
continued to its ordinary deactivation. This establishes the script and
current-host behavior; it is not evidence for an undocumented historical
Gold engine shortage override.

**Established — authored visual requests.** Recipient coverage controls the
model piece named `shield`. ARMMEX moves that piece up 120 world units;
Aegis moves its own piece up 150, Corona up 90. The show operation is gated
by port 75 for the reading unit. That port means local controller, including
local AI, not human ownership or LOS; the readme's owner-only wording is
therefore not a complete source contract for the current recorder table.
Provider hit and blink callbacks use ordinary bitmap-only `explode`
requests selecting the existing `explode5`, `explode4` and `explode2` art
entries. The host effect path admits these requests without a source-LOS
gate; its named-art drawing is separate from the visibility check used only
for enhanced explosion lighting.

**Unknown — complete visual equivalence.** The session checks establish
script state and ordinary effect mechanisms, not the appearance, exact
historical owner restriction, or every viewer's final image. A bounded
rendered capture with the authored effect bank and a manual historical Gold
comparison would settle those visual claims. The current port-75 contract
must not be replaced by an invented owner-only query to match prose.

## Save state and verified host paths

**Established — current-host persistence.** Coverage flags and scan delays
live in ordinary COB statics and sleeping threads; the armed and activated
bits live in the unit's operational state. Retail save projection preserves
these, including piece state. Saving an Aegis during its hit interval and
restoring the battle preserved its complete script image, its armor and
activation, and the covered extractor's armor. Advancing the restored
battle expired the pending activation interval without dropping coverage.
No new shield-specific state store or save extension was needed.

**Established — bounded acceptance.**
`TestEscalationShieldScripts` in
`internal/session/escalation_shields_retail_test.go` passed against the
identified Gold assets on 2026-09-25. The session uses Modern gameplay with
the Escalation content profile, a 100-unit player limit, deterministic seeds,
and paused AI planning. It runs the authored scripts rather than a synthetic
shield implementation. Its checks cover:

- Aegis and Corona self-armor and extractor coverage; full-precision model
  marker queries; Aegis integer distances 377/378 and Corona 505/506.
- An unprotected mobile constructor and the recipient owner's directional
  ally relation.
- Actual ordinary combat intake: 100 becomes 25 with one or two providers,
  and becomes 100 after both providers are reclaimed and scans complete.
- The separate unupgraded ARM fusion health gate.
- Funded upkeep and insufficient-energy carry, armor retention, callback
  retriggering and interval expiry; no generator activation from
  recipient-only hits.
- Active-hit save/load and continued callback execution without VM fallback
  diagnostics in the principal inspected programs.

**Established — ordinary Aegis upgrade acceptance.**
`TestEscalationAegisUpgradeFromAuthoredMenu` in `cmd/nanolathe` follows the
real `ARMSHGEN_UPG` gadget through the human command boundary, funded ordinary
production, completed retained cargo and closed build stance. An extractor
500 world units from the parent starts unprotected and becomes armored after
the completed upgrade's detector scan. The original parent remains
`Builder=0`; the required correction is the ordinary named-product HUD path
described in [Escalation engine package](taesc-engine.md), not an authored
definition rewrite or a shield-specific engine mechanic.

**Established — existing engine sites.** `session/bindQueryPorts` supplies
the target-model-height and position readers; `session/bindScriptPorts`
supplies the extension queries; `units` binds activation and armor writes;
`combat/AcceptDamage` applies the existing armor multiplier and starts the
normal hit callbacks; `economy` settles ordinary activation upkeep. These
paths need no additional shield port or engine-owned coverage service for
the scenarios above. Strict 3.1 intentionally disables the extension port
table and is not an Escalation shield compatibility mode.

## Passive generator healing

**Established — specifically authorized investigation.** On 2026-09-25 the
user explicitly requested code inspection or decompilation to settle shield
healing. This authorizes a narrow exception to the usual third-party binary
analysis restriction for this contract. Raw analysis remains outside the
repository. The inspected Gold executable and DLL are the exact artifacts
identified in [Escalation engine package](taesc-engine.md); this does not
establish another release's behavior or retail behavior.

**Established — executable caller.** Read the authored HealTime as a signed
16-bit value, `h`. Skip zero, and skip a unit whose sign-extended health,
compared unsigned, is at least its definition's maximum. The stored remaining
construction fraction must have the all-zero bit pattern. A tick is eligible
exactly when `(uint8(tick) & uint8(h)) == 0`. This is a mask, not a modulus
period: values 1 and 2 each admit half the ticks but in different patterns;
3 admits one tick in four; 256 admits every tick. Eligible calls supply
`trunc(32 × h / 30)` integer work, converted to single precision, with the
unit as both repairer and recipient. The reduction was checked over all
65,536 representable HealTime values. No recent-hit delay, activation test,
shield-state test or random draw occurs in this caller.

For both shipped shield generators `h=1`, so completed, damaged generators
supply one work unit on each even tick. The original Nanolathe caller instead
supplied zero every eighth tick. The resulting health and energy still belong
to the active repair helper; inspecting the executable alone cannot settle a
helper replaced by the shipped DLL.

**Established — shipped DLL contribution.** Normal DLL initialization
installs its repair replacement without a healing preference gate. It matches
the two-bank fractional-contribution algorithm in
[CP-DMG-4](community-patch-engine.md), with **one-times** active and passive
health contributions. The DLL does not distinguish those two callers and has
no health multiplier. The later MIT source commit
[f973336](https://github.com/tanvanman/TADR/commit/f9733364751adbda550b2b89c2a3621946ccb004)
adds caller classification and multipliers; the current-source three-times
Escalation defaults are not the Gold 10.2.0 DLL's behavior.

For positive definition build time `B`, maximum health `H`, caller work `q`
and authored energy cost `E`, energy is
`max(1, trunc(1 + (E × q − 1) / B))` with the established working-precision
arithmetic. Resource admission precedes all bank activity. Nonpositive work
returns successfully after admission without changing health or a bank.
Positive work contributes the signed-64 numerator `H × q`; divide by `B`
toward zero, add the remainder to the target-tagged bank, and carry one health
point if the bank reaches `B`. An award above 65535 is capped before the
ordinary raw healing packet. A refused energy visit neither advances nor
consumes the bank. Full-health and nonpositive-build-time guards precede the
energy calculation. There is no minimum health award for every funded visit:
sub-point work is retained until sufficient credit accumulates.

Each repairer has two ordered target-tagged banks, with the same allocation,
replacement, slot-reuse and fallback behavior as CP-DMG-4. Changing the unit
array base replaces the bank table; death itself does not clear fractions.
The shipped implementation corresponds to the bounded repair algorithm added
in MIT source commit
[f183cb6](https://github.com/tanvanman/TADR/commit/f183cb6be7869f3423dc6b2baa4dd50f57d4b9fc).
This source match is established for the inspected repair path, not for the
whole DLL. The unhooked executable's minimum-one helper does not describe
normal Gold play because the DLL replaces it.

**Established — host configuration.** The Escalation content profile selects
the sourced bitmask caller and overrides both repair multipliers to one.
The separate current-source `escalation` table retains its sourced three-times
defaults. Strict 3.1 ignores the profile's gameplay features and retains the
retail unsigned quantum and eight-tick cadence. Zero's corresponding caller
is a separate contract; no repair-helper change is inferred for that package.

**Established — investigation correction.** Prior Nanolathe observations
correctly found no healing from the retail zero-work caller, but the older
package summary missed the signed work scaling and incorrectly described
HealTime as a period. The present direct caller and installed-hook checks
settle those missing inputs. Earlier completion-only recorder source and
maintainer measurements remain version-scoped supporting history, not the
source of this formula. No shield-specific aura, combat cooldown or invented
minimum award is added.

**Established — bounded host acceptance, 2026-09-26.**
`TestEscalationShieldHealing` completes both authored generators, waits for
their ordinary shield startup, and delivers normal damage. Over 240 funded
ticks (120 eligible visits), Aegis restores four health from authored maximum
6410 and build time 164160; Corona restores three from maximum 6480 and build
time 207360. An additional hit with empty stock establishes unpaid energy
through the actual hit callback. Healing stops while that carry blocks
admission and resumes after energy is replenished. The fast caller and session
tests additionally lock signed conversion, mask patterns, positive-zero
completion, resource rejection before fractional accumulation, full-health
admission, unchanged RNG counts and Strict bypass. These checks exercise the
identified assets and implemented contract, not an automated historical game.
