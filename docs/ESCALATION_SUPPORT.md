# TA: Escalation Gold 10.2.0

Escalation is available as an experimental content package. Install it through
**Mods & Mutators → Get more mods** in Nanolathe. Manually copying the upstream
package over a Total Annihilation install is currently unsupported. Use Community
3.9 or Modern gameplay; its scripts require extension queries that Strict 3.1
deliberately disables. Its Nanolathe config (`nanolathe-mod.json` in the
hosted zip, `modconfigs/escalation-10.2.0/` in this repository) declares this
minimum, carries the renamed directories and raised limits, and recommends no
settings, so ProTA's distinct controls preset is never offered. Its Community
table is the `escalation` build profile's matrix with Gold's historical
HealTime caller and both repair multipliers at one
([passive generator healing](../research/extensions/escalation-shields.md#passive-generator-healing)).
The upstream Gold package has no such file; installed as it is, it mounts as
plain content. It can start with base definitions and shared Escalation art,
without loading Escalation's renamed content families or its rules table.

The package preserves all eight upstream content archives, icons, active
intro and local music. It excludes the historical engine executables and
DLLs: Nanolathe supplies the engine. Original Total Annihilation assets are
still required. Content identity is verified after ordinary ZIP installation
against the original Gold directory.

## Unconfigured upstream installations

**Established — current load path.** At revision `0a00987c5`, mounting the
original Gold `Step_2_Install Main Files` directory without a schema 2 config
uses the retail directory table, definition domains and map/LOS read caps.
The base install can satisfy the startup MOVEINFO/SIDEDATA checks. Those
checks establish readable game data, not which mod's families were selected.
The archive namespace includes these replacements:

| Loader's logical directory | Gold directory | Consequence without the mapping |
|---|---|---|
| `units` | `unitsE` | Reads base FBI definitions rather than Gold's. |
| `weapons` | `weaponE` | Reads the base weapon table. |
| `gamedata` | `gamedatE` | Misses Gold side/build lists, movement and other authored data. |
| `unitpics` | `unitpicE` | Does not search Gold build portraits. |
| `guis` | `guiE` | Does not search Gold panels, fonts and per-unit build buttons. |
| `download` | `downloadsE` | Does not compile Gold's download-menu placements. |
| `ai` | `aE` | Does not load Gold's authored AI profiles. |

The original namespace census observed 549 FBI files, 548 portraits, 389 GUI
files plus five fonts, 169 download TDFs and nine AI text files in these
trees. These counts describe the inspected files; they do not certify their
behavior or prove that every build product has a portrait. Unrenamed families
such as models, animations and textures still overlay the base, so a mixed
result is possible. [Issue #96](https://github.com/nanolathe-gg/nanolathe/issues/96)
reports a GOG install with Escalation copied on top and supplied through
`--root`. That setup can take this path, but this observation alone does not
establish the cause of every blank portrait in its screenshots.

**Established — bounded local comparison, 2026-10-08.** With the reference
retail root below the original Gold directory, a displayless `ashap plateau`
run under Community 3.9 with `--ticks 1` reached the requested bounded-run
limit without donor assets. The same roots with
`--mod-config modconfigs/escalation-10.2.0/nanolathe-mod.json`, and the hosted
install with its own config, produced the following results:

| Mount/config | Reported profile | Catalog SHA-256 | Community table SHA-256 |
|---|---|---|---|
| Original, no config | `retail` | `6b86aff7cb57972bb6eb880911b53488684eeade707a34ff590144ec6430234b` | `19b9eef7faedb487d7df7325d26800f4c569509cc3f3d3c4bce9fd2afb5b760f` |
| Original, repository config | `escalation` | `8c03c39e4d6acec52c0eae6703f94761a8e49596c1c34bc7bd02533963c188c4` | `d398864376254306cf03d54645a25f08b5803c9bd0727da8b3c999d9690c3ff9` |
| Hosted, own config | `escalation` | `8c03c39e4d6acec52c0eae6703f94761a8e49596c1c34bc7bd02533963c188c4` | `d398864376254306cf03d54645a25f08b5803c9bd0727da8b3c999d9690c3ff9` |

This verifies config application and bounded composition, not full campaign,
art or historical engine parity. No assets were changed. The hosted and
repository configs parsed to equal JSON values although their file bytes
differed in formatting. The hosted ZIP adds the schema 2 sidecar to preserved
content: all eight archive files below matched the original byte for byte.
The config raises unit/weapon domains to 16,000 and map/LOS caps to 64/8 MiB,
supplies the seven layout rows and the researched Community table, and
declares Community as the gameplay minimum. Selecting Community alone does
not supply those layout/limit/table facts. Merely mapping the portraits would
therefore leave the rest of this configuration gap.

**Established — reference archive identities for the inspected full release.**
These are authored content archives, not engine binaries. Names or the basic
archive alone do not identify every companion's version/composition.

| Archive | SHA-256 |
|---|---|
| `TAESC.gp3` | `5959dde9d33e12bf0eb874bfe36f7943fff6a50e85a715323886d96e2a15eb09` |
| `TXESC.ufo` | `e62a5834ea9f11bd91268fd2bcebfa1852efa9d44d7bef2c1d4c1ece7071d443` |
| `T2ESC.ufo` | `95b373d68bd38626b8e7cac44db4d3279f1da688e25a53ea6c0b6f3e6107e57a` |
| `T3ESC.ufo` | `ca37fae81491a02157ff93d0aa33dce3dc1b5085e395cd81a0f12022d35e7190` |
| `T4ESC1.ufo` | `9d628dae1c97395db744314ea6005852023bd4d9fc93dd436130140e0cad4429` |
| `T4ESC2.ufo` | `f44e879abeb00da6249a8605a6fdd7a93a86694c934623d6397fee73546e6d21` |
| `T5ESC.ufo` | `a31a071cad79f701a52f243fbeb1198e5fdb678413d0195c979a6eb8d91065fa` |
| `TADEMO.ufo` | `08c73eba383033c634bcf56a30886ee4415e6f7e398122a440a25650f3413ece` |

The readme names optional subsets and an authored version comment exists in
`TAESC.ini`; neither establishes that a subset or modified install has the
full release's contract. Windows executables/DLLs are not required by
Nanolathe and must not be executed to recognize content. See the
[authored package evidence](../research/extensions/taesc-engine.md#authored-package-interface-and-single-player-coverage)
and [layout/admission audit](../research/extensions/mod-engine-compatibility.md#authored-rendering-audit)
for the scope and unresolved resources.

**Installation policy — user-authorized 2026-10-07.** Install Escalation through
**Get more mods**. This is currently the supported route for mods whose
upstream installation modifies `TotalA.exe` or DLL files: the prepared
Nanolathe package supplies its researched config and excludes the historical
engine binaries. The explicit-config comparison above is a diagnostic, not
an installation recommendation. Automatic upstream-package recognition is
deferred; the catalogue format and URL remain unchanged. See
[the installation policy](DESIGN_MODS_MUTATORS.md#421-upstream-installations-that-modify-the-engine).

## Verified systems

These are tests of the shipped scripts in Nanolathe, not a claim of complete
historical Gold engine parity.

| System | Verified behavior |
|---|---|
| Area shields | Both generators; building coverage, range and alliances; ordinary 75% absorption, non-stacking, hit-triggered energy use, shortage, Prophet disruption, removal and active-hit save/load. |
| Generator self-healing | Gold's signed HealTime mask and work quantum, 1× fractional health contributions, construction admission, energy rejection and recovery. Both authored generators match calculated healing through ordinary session ticks. |
| Resource pairing and charging | Nine resource families; authored income amounts, range, completion, directional allies, overlap, removal and save/load. |
| Weapon charging | Sentinel firing cadence doubles with a charging field, does not stack with a second field, and returns to baseline after the last field is removed. |
| Building upgrades | Fusion upgrade attachment; Aegis's actual menu button builds and retains its upgrade, extending coverage beyond the base radius; a constructor-built Guardian completes its Ambusher upgrade; the Core repair pad keeps its finished tower where it was built. |
| Factory upgrade | Advanced vehicle plant retains its upgrade; later Bulldogs gain the third barrel and faster firing, older Bulldogs stay unchanged, and resurrection away from the marker loses the benefit. |
| Teleporter | A receiving gate links by ground attack and transfers an eligible owned unit through the ordinary attachment lifecycle. |
| Surface transport | Automatic loading and unloading, exact mixed-size capacity, excess rejection and ownership filtering. |
| Commander research | Both factions: research eligibility, weapon activation, kinetic armor, redundant sources and removal. |
| Aircraft penalties | Atlas stack armor loss/recovery and off-map cargo restrictions, including save/load and automatic unloading after recovery. |
| Large explosion art | Every root of the three oversized banks decodes; bounded caches support Classic and Modern; a 64-frame explosion exercises ordinary event creation through retirement. |

Engine corrections preserve requested-feature validation, admit researched
empty mobile footprints, allow authored counted-product buttons on non-builder
units, and load effect frames on demand. They do not add
an independent aura, income multiplier or upgrade subsystem. Existing scripts
and the central gameplay rules remain the source of the behavior.

## Known limits

Some release prose conflicts with authored content, including fusion bonus
amounts and allied-source gate linking. Nanolathe executes the shipped scripts.
Four dead upgrade references and resurrection beside a factory-upgrade marker
remain documented content/evidence issues. So does the Arm repair tower
([issue #198](https://github.com/nanolathe-gg/nanolathe/issues/198)): the Arm
pad's script looks for the Core tower's model height, never re-attaches its own
tower, and the finished tower is drawn at the pad centre
([repair pad tower upgrade](../research/extensions/escalation-script-systems.md#repair-pad-tower-upgrade)).
The representative tests do not certify every parent/product pair, campaign,
multiplayer feature or historical patch behavior.

The detailed evidence and remaining boundaries are in
[Escalation engine package](../research/extensions/taesc-engine.md),
[shields](../research/extensions/escalation-shields.md),
[resource adjacency](../research/extensions/escalation-adjacency.md),
[weapon charging](../research/extensions/escalation-weapon-charging.md),
[script systems](../research/extensions/escalation-script-systems.md) and
[commander/aircraft scripts](../research/extensions/escalation-commander-aircraft.md).

## Verification

Set `NANOLATHE_MOD_ROOTS_ESCALATION` to the extracted Gold content directory
or the installed package directory. `tools/check-retail` selects the retail
reference install, and runs the asset-gated checks when this variable is set;
they read Escalation's config from `modconfigs/`. A manual stack names the
same file with `--mod-config modconfigs/escalation-10.2.0/nanolathe-mod.json`.
For a focused session check:

```sh
NANOLATHE_RETAIL_ASSETS="$HOME/TotalAnnihilation" \
  go test -tags retail ./internal/session -run '^TestEscalation'
```

The shared renderer was also checked with the GPU capture matrix and both
live battle renderers. Matching baseline/candidate battle images and workload
censuses were unchanged at native and detail zoom. Simulation benchmarking
checks the factory admission change separately; performance timings are host
observations, not CI limits.
