# ProTA 4.8 engine package

## Evidence scope and sources

This document records what the ProTA 4.8 install documents and what its shipped
runtime files implement. It does not establish retail behavior, and it approves
no Nanolathe behavior. Retail rules stay owned by
[retail-executable-spec](../retail-executable-spec/README.md); weapon keys
shared with other sets are owned by
[Non-retail weapon target keys](weapon-target-keys.md).

**Established — artifact identity.** The inspected release is ProTA 4.8 (bundle
dated 29 August 2025), identified by its bundled `ProTA 4.8 changelog.txt` and
`ProTA readme.txt`. Inspected files and SHA-256:

| File | SHA-256 | Role |
|---|---|---|
| `TotalA.exe` | `3b9c0fadabf3dc67ed5f05a70f1e1505a0c65deadd1a3c930adfe30e2a84995e` | stock TA 3.1 executable (version resource `v3.1`, Cavedog) |
| `tdraw.dll` | `6b46046aa0ea2ab6164ac19cf8c1aba9b1ee219e1123d3fb359615830a859e73` | engine-extension renderer ("TA engine v2025.8.29" per `ProTA.ini`) |
| `tplayx.dll` | `b641a7c97389088f93e7270425a24a1b6d0c442a112cd6be47a46c2773efdacf` | TA Demo Recorder network/session DLL |
| `tmusi.dll` | `bfd96f5385984e8082db694f4d0192d65b660c1479df70f1a243a9e959beb202` | MP3 music backend (identical to TA Zero's `zmusi.dll`) |
| `wgmus.dll` | `5ab702665d01390e49fb8c8af34569a83db1342ab7e848f2a7f2794e1931f09c` | alternative music backend (WGMUS 0.0.24) |
| `ProTA.ini` | — | engine preferences, documented as "TA v2025.8.29" defaults |
| `ProTA.gp3` | `ba2ee5c758eaaa1e6409800206f6983515a05ea833d04f40193bda88bd73aa6b` | authored content inspected independently of the executable |

**Established — the shipped executable is unmodified retail.** The executable
is byte-identical to the retail 3.1 baseline used by this project
(`3b9c0fad…`, `TotalAnnihilationOld/TotalA.exe`), and its version resource
still reads `v3.1`. Every engine change below therefore reaches the process
through the shipped DLLs (or through the renderer/music backends), not through
the executable on disk; the bootstrap section below records how the loader
applies them in memory.

**Established — the DLLs are builds of a shared community engine family.** The
content of `tdraw.dll` overlaps heavily with TA: Escalation's `TAESC.dll` and,
to a lesser degree, TA Zero's `zdraw.dll` (shared text: megamap drawing,
whiteboard, challenge/response verification, factory recycling fix, start
position assignment, wind synchronization, crash diagnostics labelled
"Swedish Eye ver 0.8"). The three are separate builds of one lineage with
different feature sets and configuration strings; exact source provenance and
licensing are not established for these shipped builds. **Established (since)
— the lineage's current-generation source is MIT-licensed** (`src/DDraw` of
the TADR repository, [community patch engine behavior](community-patch-engine.md)),
and builds the same artifact family (`tdraw`, `taesc`, `zdraw`, `mdraw`) from
one tree under per-build configuration; whether these shipped builds
correspond to a revision of that tree is a version question the source cannot
answer.

No executable addresses, offsets, disassembly or decompiler output are recorded
here. **Evidence provenance:** the implementation detail below was decoded from
the shipped binaries — a method the [evidence policy](README.md#evidence-policy)
does not permit for third-party patch binaries. The wording is clean-room and
the observations stand as recorded, but no new contract may be closed by this
method: future gaps must be settled from documentation, authored content,
appropriately licensed source, or a bounded manual observation.

## Engine bootstrap

**Established — the shipped `dplayx.dll` is the loader and gate.** On load it
verifies the host executable's version signature and the patch-site bytes it
expects (a mismatch exits with "Incompatible game files detected"), applies
its engine patches (failure exits with "Failed to apply game patches"), loads
`tdraw.dll`, requires that DLL's DirectDraw creation export, and redirects two
executable call sites to it; missing or incompatible `tdraw.dll` exits with
the corresponding message. It then exposes the DirectPlay entry points as
forwarders into `tplayx.dll`. The executable itself stays byte-identical to
retail because every engine change is applied in memory at load — which is
what the changelogs call "`.exe` hacks".

## Global engine changes (not asset-driven)

**Established — documented.** The following are described by the bundle's own
changelogs. Sources: `OTA 3.1 to ProTA 4.3 changelog.txt`,
`ProTA 4.4` through `ProTA 4.8 changelog.txt`.

- **Megamap.** Full-screen strategic minimap with unit icons, mouse-wheel
  zoom, under-attack flashing, and player-icon/line colors; configured through
  `ProTA.ini` (`FullScreenMinimap`, `WheelZoom`, `WheelMoveMegaMap`,
  `DoubleClickMoveMegamap`, `UnderAttackFlash`, `MegamapFPSLimit`,
  `MegaMapConfig`, per-sensor minimum ring distances, `PlayerNDotColors`,
  `PlayerMarkerPcx`). The icon configuration lives in `Icon/iconcfg.ini`; the
  release adds custom megamap icons. The shared renderer interface behind
  these keys — held view key, eleven zoom steps, ring thresholds, icon
  selection — is recorded in
  [Shared draw-DLL interface](draw-engine-interface.md).
- **Click snap.** `ClickSnap` snaps a reclaim command to the nearest reclaimable
  feature; release notes also name mex/geo snapping and an override key
  (`ClickSnapOverrideKey` in the DLL, configurable in the ctrl-f2 menu).
- **Whiteboard / ally map tools.** Allied line drawing, dot and text markers,
  erase, camera move to newest marker, ally resource bars, map-position
  sharing, and an expanded multiplayer sharing menu (`+shareall`,
  `+sharemetal`, `+shareenergy`, `+setsharemetal/-energy`); release notes add
  `+noshake` and `.ready` to the sharing menu, and `.autopause` to the
  battleroom.
- **Selection improvements.** Double-click to select same-type units on
  screen (`DoubleClick` preference); drag-selection filters added by the
  community engine (`W` weapons, `B` builders, `Y` factories per the ESC and
  Zero documentation of the same family); `CTRL+S` selects on-screen armed
  units; `CTRL+B`/`CTRL+F` idle-builder/factory cycling with `CTRL+SHIFT`
  variants. ProTA's own notes additionally state that `CTRL+F` centres the
  view on the selected factory and that `CTRL+B` does not select aircraft
  carriers.
- **Construction-unit behaviour options.** Per-unit-definition builder
  schedules became user-configurable: the ctrl-f2 menu exposes
  `ConUnitsGuard/HoldPos|Maneuver|Roam` and
  `ConUnitsPatrol/HoldPos|Maneuver|Roam` (strings `GUARDING CONSTRUCTION
  UNITS`, `PATROLLING CONSTRUCTION UNITS`). Release notes describe the
  intent: guarding builders assist/repair, patrolling builders reclaim when
  held and reclaim/repair/assist when mobile.
- **Build queue interaction.** Queued orders can be dragged to a new
  position; build orders may be queued under the player's own mobile units
  with automatic kick-out, and the build square previews yellow when a
  kick-out will occur (4.8 notes). The earlier 4.6 list also names the fix
  for "units exploding in factories" by holding a destroyed unit's identity
  for a fixed delay.
- **Multiplayer / session features.** `+autoteam`/`+randomteam` team
  assignment, `.exereport`/`.tdreport`/`.tpreport`/`.gp3report`/`.crcreport`
  CRC reports, challenge/response verification of executable, DLLs and game
  data, start positions derived from battleroom teams, wind-speed
  synchronization across clients, ten-player replay support, and a fixed
  "ghost commander" first-seconds artifact. All are documented in the 4.6/4.7
  release notes; the DLL strings corroborate the mechanisms (challenge
  response, start-position assignment, wind synchronization, ten-player
  funnel).
- **Runtime executable patches documented as "`.exe` hacks".** AI
  resource/feature-reclamation income multiplied by difficulty
  (Hard 4.0×, Medium 1.0×, Easy 0.5×; 4.8), AI nuke and anti-nuke build/fire
  behaviour and stockpile-queue limiting (4.5), scoreboard completeness,
  reclaim-sound fixes, the Necro "Resurrection failed" text, and the AI
  builder-count threshold (4.5). The shipped executable is stock, so these
  are applied at runtime by the engine DLL; the exact patch sites are outside
  this document.
- **Display and hosting defaults.** `ProTA.ini` documents engine defaults that
  differ from retail 3.1: unit limit 1500, pathfinding cycles 66650, effect
  limit 20480, unit model buffer 1280×1280, unit and weapon identifier limits
  both 16000 (multiplayer weapon-limit increase off because of replay
  compatibility), ten skirmish players, 3D sound with 128 mixing buffers,
  default game speed normal, `SwitchAlt` group keys, and menu/sound/music
  registry defaults. These require the DLL's limit-raising hooks; they are
  documented defaults, not engine arithmetic.

**Supported inference.** The engine also raises executable-side limits and
installs hooks at load: strings name limit adjusters for pathfinding map
entries, composite buffer, effect limit, unit count and unit/weapon type
tables. Which retail constant each adjusts, and the exact ordering of the
adjustment, is not established in this document.

## Asset-driven extensions

**Established — authored key census.** A census of the installed unit and
weapon text found no non-retail unit-definition key with a demonstrated
reader. ProTA's FBI files author the
retail key set plus localized/mission metadata (for example `Designation`,
`NoAutoFire`, `Ovradjust`, `SteeringMode`, `TEDClass`, `ThreeD`, `UnitNumber`,
`altfromsealevel`, `resurrect`, `TransMaxUnits`, `Scale`, `ai_limit`), which
are the same keys stock content uses; several are simply not yet compiled by
Nanolathe's catalog. This establishes the inspected content's vocabulary,
not that the DLL cannot read other keys. The current source's common startup
offers rotation and veterancy readers for the `prota` build too, and registers
preview keys unless the host disables nanoframe preview;
their absence from ProTA 4.8 content does not remove those readers from the
later source (see the version boundary below).

**Established — scripts use retail opcodes and retail ports.** A scan of all
compiled unit scripts found only the retail instruction set and only the
retail engine port identifiers (reads 4, 7, 8, 9, 11, 12, 14, 15, 16, 17, 18;
writes 1, 5, 6, 18, 19, 20), matching
[04](../retail-executable-spec/04-units-orders-scripts-and-movement.md)'s port
table. The shipped recorder DLL exposes the shared extended ports (see
[Extended script ports](script-ports.md)), but ProTA content authors no
extended-port reads. Apparent unknown opcode words in the census are trailing
Scriptor banner text after a script's return, not executable instructions.

**Established — one non-retail weapon key is authored with no
established reader.** `toaironly=1` appears on the two anti-missile
interceptor rockets (`amd_rocket`, `fmd_rocket`); see
[Non-retail weapon target keys](weapon-target-keys.md) for the census and the
unknown reader. The engine DLLs of this build do not contain the literal key
name.

**Established — rotated exits are content, not engine scripting.** ProTA 4.4
adds separate shipyard units for each exit direction (for example the east,
north and west shipyard variants) rather than an engine command; their
scripts are ordinary retail scripts.

## Version boundaries

The engine is updated release by release and versioned by date
("TA engine v2024.3.25", "v2024.12.05", "v2025.8.29" in the inspected
bundles). Capabilities are therefore bounded by the engine build each ProTA
release ships; the 4.6 change list in particular is the source for start
position/team handling, click snap, queue dragging, patrol behaviour, the
factory identity-recycle fix, whiteboard refinements, wind synchronization and
ten-player support. Earlier ProTA releases are not covered here.

### Current source profile is a separate target

**Established — source, not a reconstruction of 4.8.** The MIT TADR tree at
`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f` selects `TDRAW_CONFIG_PROTA` in
[`config_prota.h`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/config_prota.h).
Its [`compile.yml`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/.github/workflows/compile.yml)
packages a newly compiled `tdraw.dll`, generic `totala.ini`, feature text and
the committed `dist/prota/tplayx.dll` as `tdraw-prota.zip`. The recorder
distribution is `2026.9.9`. This is a later engine-pair distribution, not the
ProTA 4.8 content archive or its 2025 engine binaries. The installation table
in that revision's `tdraw.txt` still names ProTA 4.6 and instructs users to
rename configuration files to the mod's names; its version label does not
certify a match to 4.8.

**Established — selected source behavior.** This profile enables guarding and
patrolling construction changes, construction-site kick-out, air-stack splash
handling and the contested-cell tie-break. Its off-map aircraft margin is one
tile, metal/geothermal snap radius is three cells and wreck snap radius one.
It disables the proportional repair module, aircraft-wreck fall, extended
weapon-ID protocol, share-abuse guard, local mute and percentage-share
commands; allied queued-build display also remains disabled. Common startup
and the host's preview setting, not the `prota` name, govern the shared
unit-key readers. Exact contracts and effective fallback values belong to
[Community patch engine behavior](community-patch-engine.md#31-feature-matrix-at-the-pinned-revision).
Neither these switches nor the `prota` name establish the historical 4.8
switches, authorize Nanolathe gameplay changes, or follow from choosing a
content directory profile.

## Authored package, interface and single-player coverage

**Established — archive namespace.** The identified `ProTA.gp3` uses
`gamedatP`, `guiP`, `unitpicsP` and `weaponP`; it also declares an empty
`downloadP` directory. Unit definitions remain under `units`, AI profiles under
`ai`, and models, scripts, textures, sounds and animation keep their shared
names. Therefore "retail content directories" is incorrect for this release,
even though its executable on disk matches retail. Empty `downloadP` is not
evidence that retail `download` records should be imported. Sources: the
archive directory and `gamedatP/SIDEDATA.TDF`; the latter supplies `[CANBUILD]`
lists including the separately named rotated shipyards.

**Established — authored interface.** `SIDEDATA` orders Arm then Core, with
commanders `ARMCOM` and `CORCOM`, interface GAFs `ARMINT` and `CORINT`, and
button fonts `armbutt` and `corbutt`. `guiP` contains the faction main panels,
per-builder pages and front-end windows, including campaign and save/load
windows. The 3.1-to-4.3 changelog documents twelve build icons per page,
orders on the same panel, build hotkeys and their overlays. These are required
interface inputs; recognizing the unit definitions alone does not exercise
them. The 4.6 changelog further specifies that idle-builder/factory shortcuts
prefer authored `CTRL_B`/`CTRL_F` categories over heuristics, and armed-unit
selection uses `CTRL_W` with `NOTAIR`/`NAIR`. It does not provide the complete
order/build hotkey table.

**Established — palette and artwork overrides.** The archive supplies
`palettes/PALETTE.PAL`, `GUIPAL.PAL`, `GUIPAL.PCX`, `PALETTE.SHD`,
`PALETTE.LHT` and `PALETTE.ALP`, as well as replacement textures, models,
cursors, build pictures and fonts. The 4.8 changelog documents pink/slate
replacing white/black team colors and revised minimap colors; the shipped
`Icon/iconcfg.ini` and `ProTA.ini` configure megamap art separately. A stock
palette, stock interface or stock icon set cannot stand in for these assets
when assessing ProTA rendering.

**Established — campaign and AI are part of the package.** The 4.4 changelog
documents enabling retail and Core Contingency campaigns; 4.5 documents
campaign build-menu, campaign AI and Core mission 1 display fixes; 4.7 documents
correcting Core Contingency mission 6 maximum wind from 9 to 900. The archive
supplies campaign descriptors, briefs and `camps/useonly` restrictions,
`ai/MISSIONS.txt` alongside skirmish profiles, `maps/CC01.TNT` and
`maps/EXP1CC06.OTA`. Those are overlays on original mission assets, not a
self-contained campaign distribution. The 4.5 AI changes also include nuke
and anti-nuke use, stockpile queue limiting, low-energy appliance shutdown
and a higher builder threshold. Authored AI files and documented engine
changes are separate requirements; loading an `ai` file proves neither the
patched decision logic nor campaign progression.

**Established — music configuration is separate from content layout.** The
4.8 notes include WGMUS 0.0.24; `wgmus.ini` selects MP3 files, folder playback
and `MusicFolder=music`. The bundled WGMUS readme instead describes `tamus`
as its default and warns that filename ordering depends on consistent number
padding. No music directory or tracks appear in the inspected 4.8 package
alone. Both `tmusi.dll` and `wgmus.dll` ship, so their coexistence and this INI
do not prove which backend is active. The active backend, fallback and track
ordering for this exact package remain **Unknown** without primary loader
documentation or a bounded manual observation. No third-party music code is
required to reproduce the authored music-directory contract independently.

### Package acceptance cases

These are acceptance requirements derived from the established sources above,
not reported passing tests or new gameplay authorization:

- Mount original assets followed by the identified 4.8 archive; resolve
  `SIDEDATA`, GUI pages, pictures and weapons from the renamed trees while
  preserving winning archive provenance. Show the new Spark, Blaze and Apex
  entries and both faction commanders from their authored definitions.
- Capture the Arm and Core twelve-slot build menus, hotkey overlays, page
  changes, unit portraits and pink/slate team colors with the package's own
  palette/shade/interface assets. Exercise the distinct shipyard exit variants
  rather than introducing one generic rotation command.
- Enter an original campaign and a Core Contingency mission, apply their
  use-only restrictions, issue build orders through the campaign GUI and
  advance to the next mission. Check the documented Core mission 1 layout and
  Core Contingency mission 6 wind overlay with their actual base maps.
- Exercise campaign and skirmish AI separately, including low-energy
  shutdown, stockpiles and the named difficulty resource factors; do not infer
  the patched AI behavior from successful catalog loading.
- Verify music configuration with an explicitly identified backend and
  user-supplied tracks. A silent launch without that backend evidence is not
  evidence of music compatibility.

## Unknown

- **Unknown — hotkey assignment table.** The bundled readme does not list the
  extended hotkeys, and the stock in-game help file is unchanged. Which
  letters the engine reserves (idle-builder/factory cycling, on-screen weapon
  selection, centring) versus which select authored `CTRL_x` categories is
  not established for 4.8; the changelog only names individual letters.
  ProTA's own current documentation or a bounded in-game observation would
  settle it.
- **Unknown — runtime patch boundaries.** Which documented "`.exe` hack" is
  applied by `tdraw.dll`, by `tplayx.dll`, or by a cooperating backend, and
  the exact arithmetic of the AI difficulty multipliers, are not established.
- **Unknown — engine-family provenance (settled for the current line).** The
  relationship between `tdraw.dll`, TA: Escalation's `TAESC.dll` and TA Zero's
  `zdraw.dll` is established for the current generation: one MIT-licensed
  source tree builds all of them under per-build configuration (see the
  evidence scope above). What remains unknown is whether the three shipped
  builds correspond to revisions of that tree, and no Nanolathe implementation
  may rely on their behavior as that tree's behavior.
