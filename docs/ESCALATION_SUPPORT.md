# TA: Escalation Gold 10.2.0

Escalation is available as an experimental content package. Use Community
3.9 or Modern gameplay; its scripts require extension queries that Strict 3.1
deliberately disables. The catalog declares this minimum and does not apply
ProTA's distinct controls preset.

The package preserves all eight upstream content archives, icons, active
intro and local music. It excludes the historical engine executables and
DLLs: Nanolathe supplies the engine. Original Total Annihilation assets are
still required. Content identity is verified after ordinary ZIP installation
against the original Gold directory.

## Verified systems

These are tests of the shipped scripts in Nanolathe, not a claim of complete
historical Gold engine parity.

| System | Verified behavior |
|---|---|
| Area shields | Both generators; building coverage, range and alliances; ordinary 75% absorption, non-stacking, hit-triggered energy use, shortage, removal and active-hit save/load. |
| Resource pairing and charging | Nine resource families; authored income amounts, range, completion, directional allies, overlap, removal and save/load. |
| Weapon charging | Sentinel firing cadence doubles with a charging field, does not stack with a second field, and returns to baseline after the last field is removed. |
| Building upgrade | Actual fusion upgrade builds as an empty-footprint mobile product and attaches to its parent through normal callbacks. |
| Teleporter | A receiving gate links by ground attack and transfers an eligible owned unit through the ordinary attachment lifecycle. |
| Surface transport | Automatic loading and unloading, exact mixed-size capacity, excess rejection and ownership filtering. |
| Commander research | Both factions: research eligibility, weapon activation, kinetic armor, redundant sources and removal. |
| Aircraft penalties | Atlas stack armor loss/recovery and off-map cargo restrictions, including save/load and automatic unloading after recovery. |
| Large explosion art | Every root of the three oversized banks decodes; bounded caches support Classic and Modern; a 64-frame explosion exercises ordinary event creation through retirement. |

Engine corrections preserve requested-feature validation, admit researched
empty mobile footprints, and load effect frames on demand. They do not add
an independent aura, income multiplier or upgrade subsystem. Existing scripts
and the central gameplay rules remain the source of the behavior.

## Known limits

Shield generator **self-healing is incomplete**. Gold authors `HealTime=1`,
which produces zero work through the retail passive caller. The historical
caller arithmetic is not established by the available licensed helper source.
A matching licensed source revision, maintainer contract or bounded manual
Gold observation is needed; no replacement rate has been guessed.

Some release prose conflicts with authored content, including fusion bonus
amounts and allied-source gate linking. Nanolathe executes the shipped scripts.
Four dead upgrade references and Aegis upgrade-menu reachability remain
documented content/evidence issues. The representative tests do not certify
every parent/product pair, campaign, multiplayer feature or historical patch
behavior.

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
reference install, and runs the asset-gated checks when this variable is set.
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
