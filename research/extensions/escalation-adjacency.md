# Escalation Gold resource adjacency and charging

## Evidence and scope

**Established — artifact and authored scope.** This contract describes the
resource recipients in TA: Escalation Gold **10.2.0**, inspected on
2026-09-26. The upstream `TAESC_GOLD_10_2_0_FULL.rar` is 350859995 bytes,
SHA-256 `a9873e551d7fa72ad2f74ca37d8bbc7c873043d178979c842bbf8ed667eea2c3`,
from the project's [downloads page](https://taesc.tauniverse.com/?p=downloads).
The source root is its `Step_2_Install Main Files (copy all contents into your
main TA folder)` directory, with all eight authored content archives retained.
The relevant sources are:

- `ESC_READ_ME.txt`, “ADJACENCY / PAIRING BONUS” and “CHARGING”, in Step 1;
- `TAESC.gp3`: `unitsE/` FBI definitions and `scripts/` COB programs for
  `ARMFUS`, `CORFUS`, `ARMUWFUS`, `CORUWFUS`, `ARMESTOR`, `CORESTOR`,
  `ARMUWES` and `CORUWES`;
- `T2ESC.ufo`: the corresponding FBI/COB pairs for `ARMSES`, `CORSES`,
  `ARMUWCS`, `CORUWCS`, `ARMUWMFUS` and `CORUWMFUS`;
- `T3ESC.ufo`: the FBI/COB pairs for `ARMSFUS`, `CORSFUS`, `ARMFORGE`,
  `CORVAULT` and `ARMFIELD`; the catalog's authored models identify the
  providers listed below, including the T4 carriers from `T4ESC2.ufo`.

**Established — method boundary.** The descriptions below independently
express the authored `Create` and `Detect` programs, their FBI resource
fields, and their model-height comparisons. No patch executable or DLL was
disassembled. The port meanings are those recorded in
[Extended script ports, Port table](script-ports.md#port-table), scoped to
the pinned MIT TADR source revision there; retail ports and COB arithmetic
are separately owned by [04 R-COB-03](../retail-executable-spec/04-units-orders-scripts-and-movement.md)
and [the COB format](../formats/cob.md). These sources establish authored
behavior and the identified interface, not every behavior of the historical
Gold DLLs. The tests below establish Nanolathe observations on this content.

**Established — weapon boundary.** This contract covers resource income only.
[Sentinel weapon charging](escalation-weapon-charging.md) separately records
its authored eligibility and firing waits, including non-stacking and removal.
Other weapon recipients and upgrade interactions remain **Unknown** without
their own authored audits; there is no universal charging multiplier.

## Recipient scan and eligibility

**Established — authored recipient-driven detection.** Each inspected
recipient starts its own detector after its `Create` program observes that
construction is finished. Its initial random delay precedes reading the
minimum and maximum unit identifiers from ports 69 and 70. Those bounds are
retained for subsequent passes. Every pass clears its accumulated charge
score, then visits every identifier from the lower bound through the upper
bound **inclusively**, ascending. The recipient does not maintain a provider
registration list, perform a geometric footprint-contact test, or request a
generic engine adjacency multiplier.

**Established — positive eligibility.** A candidate must pass the
recipient-owner's directional alliance read through port 74 and report zero
unfinished construction through port 73. Port 11's full fixed-point model
height identifies its class: the script compares exact authored height
values, rather than unit names, faction, category, resource fields or weapon
identity. Position reads reject dead units through the ordinary retail
lookup; the height read also returns zero for an absent unit. The charge
branches do not inspect provider activation, available energy, visibility,
health percentage, or the recipient's current boost state. A provider's
separate script and upkeep may change its own state without changing these
recipient predicates.

**Established — self and faction.** The scan does not exclude the recipient
itself. A completed ordinary recipient contributes its own pairing weight,
because its height is in its accepted family and its distance to its base
piece is within range. This is why a threshold of twice that weight means
one additional matching neighbor. Both factions' heights are accepted; an
allied opposite-faction provider can qualify. The one-way alliance predicate
is from recipient owner toward provider owner, not the reverse declaration.

**Established — ownership and removal are sampled.** Every pass rereads
alliance, completion, model height and position. A destroyed provider loses
its live height; a moved or transferred provider is reclassified on the next
pass. No remembered pairing partner or permanent boost is created. Slot reuse
is subject to the ordinary lookup of whichever live unit occupies that slot
when visited. No uninspected capture-transfer script behavior is implied.

## Charging providers

**Established — authored resource-provider predicates.** All inspected
resource families have the following positive provider branches. Distances
are world units, compared inclusively through the distance calculation below.

| Provider definitions resolved by the Gold catalog | Height identity, in 16.16 words | Reach |
|---|---|---|
| `ARMFIELD`, `CORFIELD` | 2184856 | 377.5 |
| `ARMVCAR`, `CORVCAR` | 4659200, 4238108 | 672.5 |
| `ARMSCAR`, `CORSCAR` | 4959195, 4135472 | 672.5 |
| `ARMFSCAR`, `CORFSCAR` | 1960681, 3407872 | 672.5 |
| `ARMUSCAR`, `CORUSCAR` | 5609472, 5570560 | No distance test |

**Established — provider weights.** Each qualifying provider contributes
two points to T1/T2 storage, mini-fusion and ordinary fusion recipients;
it contributes three to heavy fusions and Forge/Vault. Combined with the
recipient's self contribution, one provider is sufficient for every inspected
resource family. Multiple providers increase the score, but the output is
still the single activated/deactivated state below. Fleet-carrier reach is
unbounded in these authored branches; it must not be replaced with the
readme's general field-coverage description.

**Established — unmatched authored identities.** The carrier branch also
accepts heights 3754666 and 2899215, but neither identifies a definition in
the inspected full catalog. This is an established comparison with no live
match, not evidence for an additional shipped carrier. The corresponding
intended units are **Unknown**; matching older content or author documentation
would settle them.

## Pairing families and range

**Established — authored score table.** The notation `ARM/COR` below means
the two named faction counterparts. Every family accepts itself, so its
ordinary isolated self score is the listed neighbor weight. Thresholds do
not scale production: they choose one state. A mixed-tier pair need not
benefit both buildings; for example, a heavy fusion qualifies for an
ordinary fusion, but the heavy fusion accepts only another heavy fusion.

| Recipient | Accepted live pairing families and reach | Weight per neighbor | Threshold |
|---|---|---:|---:|
| `ARM/COR ESTOR` | `ESTOR`: 69; `SES`: 87.5; `ARMFORGE`/`CORVAULT`: 150.5 | 1 | 2 |
| `ARM/COR UWES` | `UWES`: 69; `UWCS`: 108.5 | 1 | 2 |
| `ARM/COR SES` | `SES`: 106; `ARMFORGE`/`CORVAULT`: 169 | 2 | 4 |
| `ARM/COR UWCS` | `UWCS`: 148 | 2 | 4 |
| `ARM/COR UWMFUS` | `UWMFUS`: 69; `UWFUS`: 77.625 | 1 | 2 |
| `ARM/COR FUS` | `FUS`: 86.25; `SFUS`: 135.625 | 2 | 4 |
| `ARM/COR UWFUS` | `UWFUS`: 86.25 | 2 | 4 |
| `ARM/COR SFUS` | `SFUS`: 185 | 3 | 6 |
| `ARMFORGE`, `CORVAULT` | `ARMFORGE`/`CORVAULT`: 232 | 3 | 6 |

**Established — fixed authored distances.** These distances come from the
script's sums of constants; they are not recomputed from the current FBI
footprints. In particular Forge/Vault use a 116-world-unit contribution on
each side despite their different authored footprints. Some branches repeat
the same height twice in an OR condition; that is one matching branch and
does not double-count a candidate.

**Established — dormant pairing comparisons.** The scripts retain further
height comparisons with no matching full-catalog definition: 9999999 and
3600001 in storage families; 6970681 and 6136734 in ordinary land fusions;
1964769 in underwater fusions and mini-fusions. Their intended historical
definitions are **Unknown**. They are not aliases for existing units and
must not be silently converted into a generic “similar resource” category.

**Established — distance and rounding.** The detector subtracts the recipient's
packed piece-zero X/Z position from the candidate's packed X/Z position. It uses
the absolute signed packed difference, decomposes it into the quotient and
remainder by 65536, and repairs a remainder above 32767 by complementing that
remainder and increasing the quotient when the packed difference is positive.
A preliminary port-13 distance on the halved components guards large deltas:
when that answer exceeds the authored integer 707333111, the final distance
query receives the authored packed constant 500333222 instead. Otherwise it
receives the absolute packed difference. The final port-13 result is compared
with the inclusive fixed-point bound. These integers are authored script
operands, not executable locations. Preserve the script and retail port
arithmetic rather than replacing this with floating-point world-coordinate
distance or a footprint-overlap approximation. The tested axial boundaries
are 86 versus 87 for ordinary fusion pairing, 377 versus 378 for fields,
and 672 versus 673 for command carriers.

## Income and precedence

**Established — authored income mechanism.** The detector changes retail
activation port 1. It does not edit a definition, write a resource stock,
or call a higher-numbered economy-extension port. A completed unit's passive
`EnergyMake`/`MetalMake` continues independently. Activation admits negative
`EnergyUse` refunds or a `MakesMetal` branch; for storage, the beneficial
state is **inactive**, which removes upkeep equal to passive energy output.
Ordinary economy accounting and the owning player's applicable income rules
remain in force. The table gives normal full-income contributions per
settlement pass, before unrelated cloak or work spending.

| Family | Isolated output | Boosted output | Detector's boosted activation |
|---|---|---|---|
| Ordinary land fusion | 1000 energy | 1200 energy | On |
| Underwater fusion | 1200 energy | 1600 energy | On |
| Heavy fusion | 5000 energy | 6250 energy | On |
| T1 land/underwater energy storage | 25 energy produced and 25 requested | 25 produced and zero requested | Off |
| T2 advanced/combined underwater storage | 125 energy produced and 125 requested | 125 produced and zero requested | Off |
| Underwater mini-fusion | 100 energy, 1 metal | 100 energy, 2 metal | On |
| Forge/Vault | 500 energy, 20 metal | 500 energy, 30 metal | On |

**Established — initial activation is distinct.** The table describes the
state after detection. Forge/Vault author `ActivateWhenBuilt=1`, so their
maker branch can operate before the first detector clears activation for an
isolated unit. The first scan is not an admission-time income rewrite.

**Established — independent baseline details.** Arm heavy fusion also has
passive metal production of 2; Core heavy fusion does not. Mini-fusion's
bonus is the authored maker amount **1**, not the readme's +2 metal, and it
has no charging energy bonus. Forge/Vault's extra 10 metal and mini-fusion's
extra 1 use the ordinary maker admission/carry rules. Integer script scores
undergo no percentage rounding; resource conversion and accumulation remain
the ordinary economy contract. These results resolve the readme's conflicting
“150%” paragraph and differing tier examples for this exact artifact only.

**Established — pairing and charging have no ordering precedence.** Both
feed the same score during the same scan. The final threshold sets one state,
so a pair plus a field, or several fields, cannot stack extra production.
If fields disappear while enough pairing weight remains, the next pass keeps
the same state. If all qualifying support disappears, the next pass restores
the unboosted state. This follows recomputation, not a saved priority choice.

**Established — hostile disruption shares the score.** Completed hostile
height identities for `ARMWALK`/`CORTSAR` subtract the family provider weight
within 4035 world units. `ARMCRAWL`, `CORDECI` and `ARMSCRAM` do so within
2017.5; the same branch also accepts unmatched height 1183517. These are
separate from the shield-score reductions in the same detector. The charge
score is not clamped before its threshold tests. The intended unit for the
unmatched disruption identity is **Unknown**.

## Timing, state and save behavior

**Established — authored delays.** Each `Create` waits for its own construction
to finish using one-second sleeps, then starts `Detect`. The delay ranges
below are inclusive random milliseconds drawn through ordinary COB random;
the scan bounds are captured after the first delay. Animation and shield
branches can insert further sleeps, so this is not an exact promise of a
fixed wall-clock activation latency.

| Recipient tier | Initial detector delay | Delay between completed scans |
|---|---:|---:|
| T1 land/underwater storage | 1000–10000 ms | 10000–20000 ms |
| T2 storage, ordinary/underwater fusion, underwater mini-fusion | 750–7500 ms | 7500–15000 ms |
| Heavy fusion and Forge/Vault | 500–5000 ms | 5000–10000 ms |

**Established — tick and account boundary.** Sleep conversion follows the
ordinary COB contract, including the guard decrement after the truncated
tick count. The activation write affects subsequent production fills, and
the archived account updates on the ordinary player settlement deadline.
Reading the display immediately after a provider is created or removed is
not evidence that no change occurs. The script uses the existing simulation
RNG; there is no independent adjacency generator or deterministic fixed delay.

**Established — retained state.** Detector scores and visual latches occupy
the unit's ordinary COB statics; scan position and cached identifier bounds
are thread locals. Current activation is ordinary unit state. No separate
adjacency manager, pairing graph, economy multiplier, or additional save
record is required by these inspected scripts.

**Established — Nanolathe save observation.** The focused real-content test
below saves an active fusion pair after the charging fields have been removed,
restores activation and all detector statics, advances normal ticks and
observes 1200 energy per survivor pass. Removing the restored neighbor makes
the next completed detection return to 1000. This proves this continuation
in Nanolathe through existing unit/COB serialization. Historical Gold DLL
save compatibility, and exact RNG continuation relative to a retail process,
remain **Unknown** without a matching bounded manual observation; the
Nanolathe result is not represented as one.

## Verification and remaining boundaries

**Established — Nanolathe acceptance.**
`internal/session/escalation_adjacency_retail_test.go` mounts the actual Gold
package over the reference retail install, compiles the Escalation profile,
enters ordinary sessions and allocates the authored units with their real
scripts. It does not replace extension-port values or implement another
detector. Its checks cover each resource family before pairing, after pairing
and after neighbor removal; field/carrier range boundaries; global fleet
carrier reach; completed versus unfinished providers; allied, hostile and
reverse-only alliances; overlapping sources, fallback to pairing, save/load
continuation and Strict 3.1's extension-port bypass. Settlement is observed
through the real per-unit archived production and request records.

Reproduce with `NANOLATHE_RETAIL_ASSETS` pointing to the reference install
and `NANOLATHE_MOD_ROOTS_ESCALATION` pointing to the Gold content root:

```sh
go test -tags retail ./internal/session -run '^TestEscalation(ResourcePairing|ChargingAdmission|PairingChargingOverlapAndSave|ResourceRuleModes)$'
```

**Established — mode boundary.** The mode check observes the boost in both
Community 3.9 and Modern, and baseline fusion output in Strict 3.1, where
the adopted extension ports answer zero. It does not override the mod's
advertised minimum gameplay selection in the player interface.

**Established — scope of placement.** These tests place completed units via
the ordinary allocator to isolate their scripts and income. They do not
certify builder placement legality, naval map choice, placement rotation,
the visual coverage indicators, or charging effects on weapon recipients.
No simulation implementation change is needed for the resource cases proved
here; existing COB, activation, alliance, economy and save interfaces suffice.

**Unknown — remaining acceptance.** Hostile disruptor interactions are
established from the authored detector but are not exercised by the focused
test. Visual coverage and locality exposure, weapon charging beyond the
separate Sentinel case, capture while a detector sleeps, and extreme
packed-coordinate cases need corresponding
authored-case tests or bounded observations. Keep these distinct from the
resource income and save continuation cases already verified.
