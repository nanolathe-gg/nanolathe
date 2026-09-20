# Non-retail weapon target keys

`nottoair`, `toaironly`, `nottounderwater` and `surfacefire`: four weapon-level
TDF keys that community content sets author and retail Total Annihilation has
no reader for.

## Evidence scope and sources

This document records what three shipped content sets document and author, and
the implementation evidence the inspected Escalation engine build provides. It
does not establish a complete admission predicate for any key, and it approves
no Nanolathe behavior. Retail's own unit-to-unit target gate — the medium
clauses, the `toairweapon` mover-mode clause and their order — stays owned by
[06 R-WPN-05 §1](../retail-executable-spec/06-weapons-projectiles-damage-and-effects.md)
and [06 §3.1](../retail-executable-spec/06-weapons-projectiles-damage-and-effects.md),
and the weapon record's key table by
[02 R-KEYS-01](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md).
No Nanolathe behavior is built on this evidence. The keys are parsed onto the
compiled weapon definition and read by nothing, in both gameplay modes; see
[DESIGN_WEAPONS_PROJECTILES §2.3](../../docs/DESIGN_WEAPONS_PROJECTILES.md#23-the-shot-admission-gate).

**Established — artifact identity.** Three installed content sets were
inspected on 19 September 2026.

| Set | Version | Identifying artifact |
|---|---|---|
| TA: Escalation | Gold 10.2.0 | bundled `ESC_READ_ME.txt` and `GOLD_10_2_0.txt`; weapon sections in `TAESC.tdf` under the set's `weaponE` tree |
| ProTA | 4.8 | bundled `ProTA 4.8 changelog.txt` and the earlier changelogs back to `OTA 3.1 to ProTA 4.3 changelog.txt`; weapon sections in `WEAPONS.TDF` under the set's `weaponP` tree |
| TA Zero | Alpha 5 | bundled `TA Zero Readme.txt` |

No third-party implementation source was read. Every count below is a census
of authored TDF text read through Nanolathe's own loader with the matching
content profile, at commit `1dbc8b84`; retail counts are the same census over
the stock install. In addition, the Escalation Gold 10.2.0 engine DLL and
executable were inspected for the implementation evidence in its own section
below.

**Established — retail authors none of the four keys.** No stock weapon section
authors `nottoair`, `toaironly`, `nottounderwater` or `surfacefire`, and the
retail weapon record has no reader for any of them
[02 R-KEYS-01](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md).
They are therefore inert for retail content under any interpretation.

**Established — TA Zero authors and documents none of them.** The Alpha 5
readme mentions none of the four names, and no Alpha 5 weapon section authors
one. This set contributes no evidence either way.

## Authored census

**Established — Escalation Gold 10.2.0.** Of 240 compiled weapon definitions:

| Key | Weapons | Every one also authors |
|---|---|---|
| `nottoair=1` | 73 | — (25 are `waterweapon`, 21 `ballistic`, none `toairweapon`) |
| `surfacefire=1` | 11 | `waterweapon=1` |
| `nottounderwater=1` | 5 | `waterweapon=1` and `surfacefire=1` |

Every authored value is the integer `1`; no other value appears. The five
`nottounderwater` weapons are `nuke_sub_arm`, `nuke_sub_core`,
`vlaunch_sub_arm`, `vlaunch_sub_core` and `vspam_uw`; all five are also
`vlaunch` and `selfprop`. The eleven `surfacefire` weapons are those five plus
`dgun_arm`, `dgun_core`, `dgun_decoy_arm`, `dgun_decoy_core`, `laser_sub` and
`lightning_sub`. No Escalation weapon authors `toaironly`.

**Established — ProTA 4.8.** Exactly two weapon definitions author
`toaironly=1`: `amd_rocket` and `fmd_rocket`, the two anti-missile interceptor
rockets. Both also author `interceptor=1`, `coverage=2000`, `stockpile=1`,
`vlaunch=1` and `noautorange=1`, and neither authors `toairweapon`. The stock
sections of the same names carry the same keys without `toaironly` (ProTA also
raises their `flighttime` and `turnrate`). No ProTA weapon authors `nottoair`,
`nottounderwater` or `surfacefire`.

## What the sets document

**Established — Escalation states the intent of three effects.**
`GOLD_10_2_0.txt` records, in its change list:

- "Added No AA fire to mulitiple units to prevent some ground units from
  targeting air units (i.e. Arm Merl, Core Hydra, Bastion, Bertha Cannon,
  mobile artillery, etc.)." An earlier working note in the same file reads
  "add no aa tag artillery!", which names the change as a *tag*.
- "Added underwater fire ability to Arm and Core Commander and Decoy dguns
  (note: this version now fully works properly on land or sea)."
- "Added surface fire missiles to Arm Stalker and Core Leviathan, increased
  speed".

`ESC_READ_ME.txt` lists the set's engine enhancements — pathfinding cycles,
identifier limits, effect and model limits, several acquisition changes — and
names none of the four keys.

**Established — the named units mount the keyed weapons.** In the same
installed set: `ARMMERL` weapon 1 is `VLAUNCH_TRUCK_ARM`, `ARMBRTHA` weapon 1
is `CANNON_LRPC_ARM` and `CORINT` weapon 1 is `CANNON_LRPC_CORE`, each carrying
`nottoair=1`. `ARMCOM`/`CORCOM` weapon 3 is `DGUN_ARM`/`DGUN_CORE` and
`ARMDECOM`/`CORDECOM` weapon 3 is `DGUN_DECOY_ARM`/`DGUN_DECOY_CORE`, each
carrying `waterweapon=1` and `surfacefire=1`; the stock `arm_disintegrator`
carries neither. `ARMSSUB` (Stalker) and `CORSSUB` (Leviathan) weapon 3 is
`VSPAM_UW`, carrying `waterweapon=1`, `surfacefire=1` and `nottounderwater=1`.
Each documented sentence therefore lines up with a key on exactly the weapons
the sentence names.

**Established — ProTA documents nothing about `toaironly`.** Its changelogs
carry per-release "Engine notes" sections listing the patched engine's added
features; none of them, and no line of the readme, mentions `toaironly` or any
air-target restriction beyond the retail `NOTAIR` unit category used for
selection hotkeys and bad-target categories.

## Escalation implementation evidence

**Established — the engine DLL parses the three keys as flags.** During weapon
parsing the DLL reads `nottoair` and, when set, marks the weapon record with a
high flag bit; it reads `surfacefire` and `nottounderwater` and, when set,
registers the weapon in two separate parser-side tables. Every authored value
is `1`, and the readers are set-if-nonzero.

**Established — a validator cluster consults the tables.** Per-key validation
hooks look a weapon up in the `surfacefire` or `nottounderwater` table while
other weapon keys are parsed and reject the authored combination with a TDF
error on a hit. One rule was decoded: a weapon registered as
`nottounderwater` whose own geometry can sit at or below the global waterline
is rejected (the weapon's height plus its offset is compared against the
waterline value). The cluster's other rules were not individually decoded.

**Established — the executable's acquisition routine changed.** Retail's
autonomous weapon-acquisition routine tests one high weapon-flag bit as part
of its per-weapon condition; the Escalation build requires an additional bit
there, changes the eligible-slot test from separate flag checks to a combined
mask, and adds a target-validation call whose failure clears the slot's stored
target. The surrounding per-weapon and per-target checks are otherwise
unchanged.

**Supported inference.** The parser marks plus the acquisition change are the
mechanism behind the keys' documented intent (restricting or extending what a
weapon acquires); the validator cluster additionally refuses some authoring
combinations.

**Unknown — the bit-to-key mapping and the fire-time predicate.** Which flag
bit each key sets, which table entry it consults, and how the acquisition
routine's per-weapon condition reads them are not mapped, and the validator's
waterline comparison need not be the predicate (if any) that admits surface or
submerged targets at fire time. Versioned patch documentation, licensed
source, or a bounded observation would settle it.

## Contracts

**None of the four is Established, and Nanolathe implements none of them.**
Each section below states the best reading the evidence supports, the
confidence, and the observation or document that would settle it. Nanolathe's
loader parses all four onto the compiled weapon definition; no gameplay
decision reads any of them, in either mode, and none may be added on the
strength of a reading recorded here.

### `nottoair` — the weapon does not engage an airborne target

**Supported inference.** Documentation establishes the intent ("prevent some
ground units from targeting air units") and authored content establishes which
weapons carry it, including every weapon on the units the sentence names. What
neither establishes is the operand and the site: whether the patched engine
tests the target's committed mover mode, the target definition's `canfly`, or
something else, and whether the test sits in the acquisition gate alone or also
in the order-installation and damage-reaction paths.

**Missing evidence.** The two candidate operands differ for exactly one case: a
landed aircraft, which reads a non-airborne mover mode while still carrying
`canfly`. A bounded manual observation — an Escalation Big Bertha or Merl with
a landed aircraft in range and no other target — would settle it. Versioned
patch documentation or appropriately licensed source naming the reader would
settle it directly.

### `surfacefire` — a water weapon also engages a target that breaks the surface

**Supported inference.** All eleven weapons that author it also author
`waterweapon`, so the key is only ever seen widening the water branch. The
commander disintegrator is the decisive pair: the stock weapon is not a water
weapon, the Escalation weapon adds `waterweapon=1` and `surfacefire=1`, and the
release note for that change says the result "now fully works properly on land
or sea". A water weapon's retail target clauses require the target to be in the
water [06 R-WPN-05 §1], so a weapon made `waterweapon` to fire while submerged
loses its surface targets unless something restores them; `surfacefire` is the
key the same release added to the same weapons.

**Missing evidence.** The exact surfaced predicate is not established — whether
the patch compares the target's whole-unit height alone, its height plus model
top, or some other quantity — nor what the key does on a weapon that is not a
`waterweapon`, a combination no inspected set authors. Patch documentation,
licensed source, or a bounded observation against a target whose hull is at the
waterline while its model reaches above it would settle the predicate.

### `nottounderwater` — the weapon does not engage a submerged target

**Supported inference.** No release note names this key. All five weapons that
author it are sub-launched missiles that also author `surfacefire`, which reads
as the complementary restriction: having opened a water weapon to surface
targets, the author closes it against submerged ones. The name states the
direction unambiguously, and no inspected content authors it without
`surfacefire`.

**Missing evidence.** The same submerged predicate question as `surfacefire`,
and whether the key means anything on a weapon that is not a `waterweapon` or
not `surfacefire` — no inspected set authors either combination.

### `toaironly` — Unknown

**Established — no reader is reachable in the inspected binaries.** The
literal name appears in no shipped binary of the three packages (executables,
`tdraw`/`tplayx`, `TAESC`/`eplayx`, `zdraw`/`zplayx` were all searched), and
the retail weapon loader matches authored keys against its literal key
vocabulary, so nothing resolves the key in these builds.

**Unknown.** Nothing establishes what this key would admit. ProTA 4.8
documents it nowhere, no other inspected set authors it, and the only two
weapons that carry it are `interceptor` weapons, whose retail target search
scans projectiles rather than units [06 §3.1]. Its name suggests the retail
`toairweapon` clause, but `toairweapon` has two further readers in retail —
the attack-order resolver's return code and the fire-order handler's slot pick
[02 R-KEYS-01 §2] — so an author wanting only a restriction cannot be assumed
to have wanted that key's other effects, and the reverse cannot be assumed
either. Do not supply an admission test for it.

**What would settle it.** Versioned ProTA or TA engine documentation naming the
key, appropriately licensed source, or a bounded manual observation of a ProTA
4.8 anti-missile battery with and without the key, stated with its setup.

## Open questions

- **Unknown — what the keys change outside autonomous acquisition.** The
  executable's autonomous acquisition routine is known to be modified, but
  whether the keys also apply at manual attack-order installation, guard
  replacement and the damage-reaction offer is unestablished. The inspected
  content cannot distinguish these.
- **Unknown — interaction with `toairweapon` and `waterweapon`.** No inspected
  set authors `nottoair` together with `toairweapon`, or `surfacefire` or
  `nottounderwater` without `waterweapon`, so no evidence covers those
  combinations.
- **Unknown — whether a value other than 1 means anything.** Every authored
  occurrence is `1`.
- **Unknown — Escalation's release boundary.** These counts are Gold 10.2.0
  alone. Earlier Escalation releases are not covered, and neither is any other
  engine patch that may read the same names.
