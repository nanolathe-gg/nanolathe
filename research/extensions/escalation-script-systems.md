# Escalation Gold upgrades, gates and automatic surface transport

## Evidence and scope

**Established — authored sources.** These independently described programs
come from Gold 10.2.0's eight-archive content mount, whose release identity is
recorded in [Escalation engine package](taesc-engine.md). Inspection on
2026-09-26 used authored COB, FBI and model data, without analyzing a patch
binary. The relevant script identities are:

| Logical script | SHA-256 |
|---|---|
| `scripts/ARMFUS.cob` | `c3649449ae49ce943eb66fccc22fa240af421fa502b99e08429ee15f77ccd83e` |
| `scripts/ARMFUS_UPGRADE.cob` | `399d9f8c307498fa43e7e3c4434a7f70c30d2951841ead87d51e1e97f655ce52` |
| `scripts/ARMAVP.cob` | `28f14091ab9c73d9df856b1593c4ad8cc35f78c1ec3e7ca39f137c2eabc3f5e4` |
| `scripts/ALL_L2.cob` | `1df55dab8fbd32b44346acb5dda9b0ce3a8a38b86f02d0df6dcbb96e3d0ce8d4` |
| `scripts/ARMBULL.cob` | `69c7360ebcc1eb812f8f271e4e539aae10d2d4d771ebe1468e77ecd3f5f9f826` |
| `scripts/ARMFARK.cob` | `87de613603a857afede8a5f9e9bb727f9d273993694d553110c35460821fc9ef` |
| `scripts/ARMGATE.cob` | `5e324ee2fbe238d5e316764862efe5e00d5a7b7786bc525b17920533506492a3` |
| `scripts/ARMTHOVR.cob` | `a85808b7c5e8914c1b19590dd35a893f21fd61239ceef455c42e5e697895d64e` |

**Established — host/interface boundary.** The existing
[extended query ports](script-ports.md) supply identifier bounds, ownership,
completion, directional alliances and controller locality. Target-model
height and position, activation, stance, armor and attachment use the ordinary
COB interfaces. The following observations establish the authored programs
and Nanolathe's current execution, not historical Gold executable parity.

## Fusion upgrade

**Established — authored product.** ARM fusion is a fixed builder whose menu
contains `ARMFUS_UPGRADE`. The product is an invisible mobile definition with
zero footprint, no named movement class, initial cloak and stealth, and zero
damage modifier. Its program sets armor and hides its visual after completion.
The parent lowers its construction-stance gate during work; after the normal
stop-building callback it searches for the completed upgrade's model-height
marker within 62.5 world units, detaches it, attaches it to the parent's
upgrade piece, updates its upgrade state and closes the build stance. These
are normal factory and script operations, not an engine-owned upgrade record.

**Established — required engine correction.** The retail finding
[04 R-P0-08-C](../retail-executable-spec/04-units-orders-scripts-and-movement.md)
allows empty mobile factory products. Their selected footprint remains empty
through placement, creation, carried movement and occupancy; map bounds still
apply. Rejecting such a product for missing ground-class terrain limits, or
substituting a one-cell footprint, prevented this authored upgrade. The
correction applies to the existing general factory lifecycle in every mode.

**Established — bounded acceptance.** `TestEscalationBuildingUpgrade` queues
the actual menu product, supplies resources, advances the normal authoritative
ticks and observes a completed upgrade attached to the parent, closed build
stance, armor and no script diagnostics. No completion, attachment or upgrade
flag is synthesized by the test. The parent armor/health predicate remains
separately owned by [Escalation shields](escalation-shields.md).

**Established — Aegis menu and completion.** The actual non-builder Aegis
menu now feeds ordinary counted production and completes its retained upgrade;
the extended coverage test is owned by [Escalation shields](escalation-shields.md).

**Unknown — other building upgrades.** The engine package contract retains
four dead build-menu references as authored content issues. They are not
silently aliased or repaired. The mobile upgrade case below has a separate
authored path.

## Factory and mobile upgrade

**Established — representative authored wiring.** Gold 10.2.0's ARM advanced
vehicle plant (`ARMAVP`) lists both the Bulldog (`ARMBULL`) and the level-two
upgrade (`ALL_L2`) in its `SIDEDATA` build list. The upgrade is an ordinary
zero-footprint product. Its `Create` sets armor and waits for completion before
moving its decorative strip. There is no definition replacement or engine-owned
factory-upgrade flag.

**Established — factory retention.** `ARMAVP` starts its `Upgrade` watcher from
`Create`. While its scripted yard-open state is set, it scans the identifier
range for a controller-local, unfinished unit with `ALL_L2`'s model-height
identity at the exact build-piece position, including height. After observing
that unit complete, the watcher sleeps once more, attaches the product to its
upgrade piece and moves that piece forward. The outer scan sleeps 1500 ms;
the completion wait sleeps 1 ms. This is ordinary build-piece positioning,
completion querying and attachment. It does not push an upgrade message into
the scripts of existing mobile units.

**Established — Bulldog birth admission.** `ARMBULL`'s `Create` initializes the
ordinary two-barrel appearance and waits 1 ms, then makes one identifier scan.
It accepts a completed unit with the same owner whose model height matches
`ALL_L2` or `ARMNOMAD`, within 62.5 world units of the Bulldog's base piece.
The proximity calculation uses the authored packed-coordinate distance idiom.
The scan runs before the script waits for the Bulldog's own construction to
finish. It neither checks a producing-factory identity nor repeats after
creation. A mobile born without a qualifying marker therefore remains
unupgraded when a factory upgrade completes later. `ARMNOMAD` is an alternate
authored marker, not evidence that every mobile factory propagates this benefit.

**Established — Bulldog benefit.** Successful admission changes the visible
turret pieces and enables a third barrel in the primary firing cycle. It also
removes the normal 1050 ms readiness delay following the shot's 150 ms flash
interval. `AimPrimary` waits while that firing readiness flag is set. The FBI's
`CANNON_BULL` definition and the unit's health definition stay the same; this
script does not multiply health or weapon damage. Gold's compiled weapon has
a nominal 28-tick reload, so the extra script wait can limit the unupgraded
weapon without changing that nominal reload.

**Established — bounded session acceptance.**
`TestEscalationFactoryUpgrade` builds a Bulldog, then the actual `ALL_L2`
product, then another Bulldog through ordinary factory queues with funded
resources under Modern with the Escalation content profile. It observes the
retained completed marker, unchanged older mobile,
upgraded later mobile and their distinct visible turret pieces. Ordinary ground
attacks produce repeated shot intervals of 36 ticks before upgrade and 28 ticks
after upgrade with the same weapon definition. An ARM Rector then resurrects
the upgraded Bulldog's actual wreck away from the marker; the replacement
starts with the unupgraded pieces. The programs, ports, callback scheduling,
construction completion and resurrection phase are never overridden by the
test. This is Nanolathe execution of the identified content, not a historical
patched-executable observation.

**Unknown — blanket resurrection exclusion and other pairs.** The readme says
resurrected mobiles do not benefit, but the inspected Bulldog birth scan has no
resurrection exclusion: it evaluates the same owner, completion, height and
distance predicates on a fresh script instance. The acceptance above proves
loss of the prior upgrade when resurrected away from a marker; it does not
establish the historical result of resurrection next to one. A bounded manual
Gold 10.2.0 observation of that setup would settle the wording discrepancy.
Other factories, mobile types, tier-three upgrades, repeated upgrade products,
capture and save continuation remain outside this representative contract;
their own authored paths need inspection before any generic propagation rule
is claimed.

## Receiving gate

**Established — authored link command.** ARMGATE uses its ordinary primary
ground-attack aim callback to locate a completed source gate near the aimed
ground position, recognizing gates by model height. It rejects itself and
requires the candidate to have the destination's owner, in addition to the
alliance query. It stores the source identifier and returns an unsuccessful
weapon aim result: the link command is not a projectile shot.

**Established — authored transfer.** While activated and linked to a live
completed source, the receiving program scans identifiers for eligible cargo
near that source. Its distance band is greater than 2.5 and at most 150 world
units. It requires the receiver's owner and an accepted model-height identity,
with controller-locality checks. Ordinary attach, short sleep, piece attach
and detach operations perform the transfer; authored obstruction handling can
change the final release position. Loss of the source marker clears the link.

**Established — bounded acceptance.** `TestEscalationTeleporter` links two
actual gates using a human ground-attack command, then observes an owned Flash
jump from the source area to the receiver area in one simulation tick and be
released alive. It checks the real script for diagnostics. It does not invoke
the teleport function directly or inject query results.

**Unknown — allied-source discrepancy and remaining cases.** The readme says
an allied source can feed an owned receiver, while the inspected link callback
also tests equal owners. Do not remove that authored test to imitate the
readme. A bounded historical Gold observation would settle the discrepancy.
Mobile receivers, obstruction extremes, retained orders, re-linking and a
save taken during transfer are outside this representative test.

## Automatic surface transport

**Established — authored admission.** ARMTHOVR's automatic mode operates when
activated and stationary. Fire stance zero selects loading and stance one
selects unloading. Its scan visits identifiers in order, with an initial ally
query and a greater-than-2.5, at-most-150 world-unit proximity band. The pickup
callback additionally requires the transport's exact owner. A private script
table maps model heights to weights; Flashes consume 600 and Stumpies 1200.
The accepted sum cannot exceed 7200 and the script's stored cargo list cannot
exceed 50. An unmatched height receives a rejecting weight of 100000.
There is no universal engine count or footprint-size substitution for this
authored capacity calculation.

**Established — bounded acceptance.**
`TestEscalationAutomaticSurfaceTransport` uses ordinary selection, stance and
activation commands. Four Stumpies and four Flashes fill the authored weight
exactly; another Flash remains outside, as does an allied player's Flash.
Changing stance unloads every carried unit and empties the ordinary attachment
list without script diagnostics. The test gives no direct pickup/drop calls.

**Unknown — broader transport behavior.** This case does not establish every
height entry, release ordering within one tick, naval terrain admission,
transport death, or surface-cargo save continuation. Aircraft loading and
off-map penalties have separate scripts and must not inherit this capacity
or stance behavior by assumption.
