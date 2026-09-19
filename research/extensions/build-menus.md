# Extended build menus

## Evidence scope and sources

This document records authored menu data and documented presentation features.
It does not establish a third-party patch's membership algorithm or approve a
Nanolathe gameplay policy. Retail membership and page behavior remain owned by
[02 R-CAT-01 §8](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md#the-download-menu-compile-and-the-per-builder-list-extension-r-cat-01-8)
and [07 R-HUD-03 §6](../retail-executable-spec/07-interface-input-camera-and-front-end.md#build-pages-name-composition-the-dl-template-nextprev-and-command-button-state-r-hud-03-6).

**Established — artifact identity.** The inspected TA Zero release is Alpha 5
(December 24, 2024), identified by its bundled `TA Zero Readme.txt` and the
[author's version history, Alpha 5](https://zero.tauniverse.com/version-history/).
The archive is `TAZ31.gp3`, SHA-256
`51ef8804ee67883672d311a89d6b37125858f0eb91bb47af1450359c8e1a2ec4`.
The bundled readme has SHA-256
`ddb03b3934e68bf7cc7513473aeab89547d8295bff02f5760b7dbbbf515a4113`.
The installed overlay used original TA assets, TA Zero Base, then Alpha 5;
all menu files discussed below resolve to this Alpha 5 archive. Paths below
are authored archive paths, independent of the machine's install directory.
No patch executable or third-party implementation source was analyzed.

## Twelve authored product slots

**Established — documented presentation.** The author's
[Features, Intuitive Interface](https://zero.tauniverse.com/features/), read
September 18, 2026, describes a combined orders/build sidebar with twelve unit
slots per page. That live page does not name a release; the identified Alpha 5
assets independently establish the twelve-slot layout for that release. The
bundled Alpha 5 readme's installation sections require a resolution of at least
1024 by 768. Neither statement defines a membership limit.

**Established — authored Alpha 5 geometry.** `ZGui/ARMDL.GUI`, `CORDL.GUI`
and `GOKDL.GUI` each contain twelve `IGPATCH` placeholders. Their header starts
at `(0,128)` with width 128 and height 640. `GOKDL.GUI` has SHA-256
`707e1cafd74543659fdbef14f196d3621f80f818813db6528f3dffc7fd9dde05`.
The matching commander page files also provide this tall sidebar geometry.
These are authored rectangles, not evidence that a patch clamps them against
any particular reference viewport.

**Established — authored Alpha 5 placement.** For each of `ArmCommander`,
`CoreCommander`, and `GoKCommander`, the `ZBuildMenu/<commander>_H1.tdf` through
`_H3.tdf` sections author `Menu=2` and, together, `Button=0` through `11`.
The `_H4.tdf` through `_H6.tdf` files author `Menu=3` and buttons 0 through 7,
then 9 through 11; no button 8 is authored in that group. This is file content,
not an inferred page-filling rule. Representative GoK factory entries are:

| Archive path | Section | Menu | Button | Product |
|---|---|---|---|---|
| `ZBuildMenu/GoKCommander_H3.tdf` | `MenuEntry0` | 2 | 8 | `GoKT1AF` |
| same | `MenuEntry1` | 2 | 9 | `GoKT1GF` |
| same | `MenuEntry3` | 2 | 11 | `GoKT2GF` |
| `ZBuildMenu/GoKCommander_H6.tdf` | `MenuEntry0` | 3 | 9 | `GoKT1NF` |

The `_H3.tdf` file has SHA-256
`2dca6f3451ac60ae4348a0ba2d3ee3a6351a93648d6b19a3888958f50026f4c0`;
`_H6.tdf` has SHA-256
`ba518597ef5d4c1b6b484a9b49ae8829565da8e6203e064aa498055e3c2e4095`.

## Repeated membership records without placement keys

**Established — authored Alpha 5 records.** For each of the same three
commanders, the `_A1.tdf`, `_A2.tdf`, and `_D1.tdf` through `_D5.tdf` files
contain 32 sections in total. Every section names its commander with
`UnitMenu` and a product with `UnitName`, but omits both `Menu` and `Button`.
They do not explicitly author `Menu=0`. Products include names ending in
`_AI` and repeated products without that suffix. For example,
`ZBuildMenu/GoKCommander_D2.tdf` contains five sections all naming
`GoKT1GF_AI`; its SHA-256 is
`e86915c24916b3f3d205cbc5676b4a1efc862fdf58dea8cdb545d788402f23af`.
`GoKCommander_D4.tdf` contains five sections naming `GoKT1Geo`, and
`GoKCommander_D5.tdf` contains four more. These duplicate sections are authored
data, not duplicate files shadowed by the overlay.

**Established — bounded Nanolathe reproduction.** In the Nanolathe loader
at commit `724104ed`, inspected with this installed overlay, absent placement keys decode to zero.
With the directory-layout wrapper's sorted enumeration, the 32 `_A`/`_D`
records precede the `_H` records. Applying the retail append cutoff to that
sequence fills the 31-entry commander membership before reaching the visible
factory entries above. All three commanders exhibit this exclusion. This
reproduction diagnoses that Nanolathe configuration; it does not establish the
patch's enumeration order, special handling of zero, or duplicate semantics.
Changing enumeration alone is not evidence of a complete compatibility fix.

## Unknown patch contracts

- **Unknown — membership capacity.** Neither the bounded authored sample nor
  the inspected author documentation establishes a generic replacement for
  the retail append cutoff, or whether the patch changes that cutoff at all.
  Versioned primary patch documentation, appropriately licensed source, or a
  bounded manual observation distinguishing candidate limits would settle it.
- **Unknown — missing/zero placement semantics.** The assets do not establish
  whether the patch specially treats an absent `Menu`, an explicit zero, or
  names with `_AI`. Do not infer an AI-only membership filter from these names.
  Primary documentation or an allowed implementation source must distinguish
  membership from visible placement before such behavior is implemented.
- **Unknown — repeated membership semantics.** Repetition establishes authored
  multiplicity, not why the author chose it. Whether the patch retains,
  deduplicates, weights, or otherwise interprets these entries requires an
  identified patch contract; this evidence does not justify deduplication.

A Nanolathe Modern policy may deliberately retain all resolved authored
membership through the existing construction rules interface, with Strict 3.1
using its retail list. Such a decision belongs in the owning design document
and its tests; it must not be presented as an established patch algorithm.
