# TA Zero Alpha 5 engine package

## Evidence scope and sources

This document records what the TA Zero Alpha 5 release documents, what its
authored content contains, and what its shipped runtime files implement. It
does not establish retail behavior and approves no Nanolathe behavior. Retail
rules stay owned by
[retail-executable-spec](../retail-executable-spec/README.md); the twelve-slot
build menus are owned by [Extended build menus](build-menus.md).

**Established — artifact identity.** The inspected release is TA Zero Alpha 5
(24 December 2024) installed over TA Zero Base. The official download listing
dates Base 14 December 2019; earlier local inventory called it 13 December.
The file hashes below identify the inspected package independently of that
date difference. The bundled `TA Zero Readme.txt` identifies Alpha 5. Inspected files and SHA-256:

| File | SHA-256 | Role |
|---|---|---|
| `TotalA.exe` (Alpha 5) | `24726c36002f6a25ce59892cf4791aa5dacaa0a3bd491bb0dc2fffec3bbe6dd0` | retail 3.1 image with in-place patches, a rewritten import table and a rebuilt resource section |
| `zdraw.dll` (Base) | `34993ee8f4b2d194df83f61e0fb19fa8833ba7d911de510a2e55b6c34f12968a` | engine-extension renderer |
| `zplayx.dll` (Base) | `44f2d45fefe294110ac5ac7a4dccbc5c1f27f002414b1fb2886b4ec69b092aae` | TA Demo Recorder session DLL (older generation) |
| `zmusi.dll` (Base) | `bfd96f5385984e8082db694f4d0192d65b660c1479df70f1a243a9e959beb202` | music backend (identical to ProTA's `tmusi.dll`) |
| `online.dll` (Base) | `6e3eec29498728fd29b12dc6230b694dd26e5d0f8697c7e2d372b2bdf1e67920` | retail 3.1-era network helper (byte-wise different from the baseline's 1998 copy; imports and strings identical) |
| `TAZ31.gp3` (Alpha 5) | `51ef8804ee67883672d311a89d6b37125858f0eb91bb47af1450359c8e1a2ec4` | content archive (rechecked against the authored archive) |
| `TA_Features_2013.ccx` (Base) | `7f555c233f4fce568d57a5f269561fe5b282989fda7d61ea7aaea0305297962b` | shared map-feature dependency |
| `TAZero.ini` | — | engine preferences |

Two optional replacements exist in the Alpha 5 "Fixes" subfolders. Both are
documented by their own readmes as launch/rendering compatibility builds, and
their string surfaces differ only in wrapper, runtime and manifest material:
the **Fix 10** `zdraw.dll` (`4eba5a70…`) loads a bundled DirectDraw wrapper
(`pdraw.dll`, `aqrit.cfg`) and is described as making the megamap usable on
newer machines; the **Hotfix** `zdraw.dll` (`f9d500e1…`) is a launch fix adding
DPI awareness and a video-memory preference. Neither documents a gameplay or
engine-feature change, so this document covers the shipped default renderer and
treats the fixes as rendering substitutes.

**Established — the executable is a rebranded retail build with a small,
specific patch set.** It keeps retail's version resource and text-section
layout and rewrites its imports
to `ZDRAW.dll`, `ZMUSI.dll` and `ZPLAYX.dll` in place of `DDRAW.dll`,
`WIN32.dll` and `DPLAYX.dll`. Content directories, registry path
(`Software\TA Zero`), INI name (`tazero.ini`), savegame extension (`.zsv`) and
front-end assets (`FrontendZ`, `logoz.GAF`, `LoadgameZbg`, `singleZbg`) are
renamed; the bundled settings file's AI profile directory is renamed too, and
the application icon is replaced. Most of the file's 36,864-byte size increase
is a rebuilt and enlarged resource section; after realigning the shifted data,
roughly three kilobytes differ from retail, about 800 bytes of which are code
patches and 400 bytes a new code block in otherwise unused mapped space. The
release history documents the intent: "TA Zero now uses custom data structure,
dlls, registry entries, and savegame format and will no longer conflict with
any other mods or patches in any way" (Alpha 3b).

**Established — the engine renderer is the 2013-generation build of the shared
community DLL family.** `zdraw.dll` shares its "draw DLL" identity with
ProTA's `tdraw.dll` and Escalation's `TAESC.dll` (megamap drawing, whiteboard,
circle selection, `IsLost`, `MegamapRadarColor`), but carries far fewer
extension strings: no build-preview, veterancy, vote or challenge-response
machinery, and no construction-unit option set. Its own diagnostics address a
community author by handle and ask the user to send `Errorlog.txt` and the
replay file. The mapping from that shipped build to a source revision is not
established. The current MIT source does build a `tazero` profile; its
separate scope is recorded below.

No executable addresses, disassembly, decompiler output, generated names or
executable structure layouts are recorded here. **Evidence provenance:** the
user explicitly authorized source research or decompilation of TA Zero on
26 September 2026 UTC. That authorization is a TA Zero-specific exception to
the general [extension evidence policy](README.md#evidence-policy). Raw
analysis and reproducibility records remain in the private analysis corpus;
this document contains independently written contracts. Rechecked historical
contracts below identify the actual shipped artifacts. Older inventory claims
remain scoped to their evidence and are corrected where the new trace differs.

## Documented engine-level behavior

**Established — documented.** Sources: the bundled readme, the in-game help
file `gamedata/HELP.TDF` (which retains stock text and is stale for changed
keys; the author's controls page is newer and is the citation used here), and
the author's version history.

- **Engine settings** (`TAZero.ini`): unit limit (default 1500), pathfinding
  cycles (66650), special-effect limit (20480), model buffer 1280×1280, unit
  and weapon identifier tables 16000 each with the multiplayer weapon increase
  off for replay compatibility, double-click selection on, expanded sharing
  menu on, megamap on, 3D sound, unlimited mixing buffers, normal game speed,
  `SwitchAlt` group keys, ten skirmish players, random music. The version
  history records these as Alpha 3b changes.
- **Directable factory build plates.** Every factory's exit/build plate can be
  pointed at a direction ("Direct" button or `D` while the factory is idle);
  the mechanism is detailed in the section below. AI ground and naval
  factories set the build direction from their script's own sampling of
  nearby units (Alpha 3/3c notes).
- **Megamap**: wheel zoom, double-click-to-zoom option, under-attack icon
  flashing, per-sensor minimum ring distances, icon configuration
  (`ZIcon/iconcfg.ini`), player icon/line colors.
- **Selection and build input**: double-click selects same-type on screen
  (configurable); drag filters `W` (armed mobiles), `B` (construction units),
  `Y` (factories); `X` builds a line or surrounds an existing unit with
  wheel-adjusted spacing; `SHIFT`-click orders five units and
  `CTRL+SHIFT`-click orders a hundred; `ALT+1‑9` selects build pages while
  `,`/`.` (or `Z`/`.`) step pages; assigning a factory to a control group
  assigns future units built by that factory.
- **Selection categories** (author's controls page; the engine maps
  `CTRL`+letter either to a hardcoded behavior or to the authored
  `CTRL_<letter>` unit category): `CTRL+A` all units; `CTRL+C` commander;
  `CTRL+B` cycle idle construction units; `CTRL+SHIFT+B` all construction
  units; `CTRL+F` cycle idle factories; `CTRL+SHIFT+F` all factories;
  `CTRL+S` on-screen armed units; `CTRL+SHIFT+S` all on-screen units;
  `CTRL+Z` same type as selection; `CTRL+G` mobile ground; `CTRL+L` frontline
  mobile ground; `CTRL+O` supporting mobile ground; `CTRL+K` kbots;
  `CTRL+T` vehicles; `CTRL+H` hovercraft; `CTRL+V` mobile air; `CTRL+Y`
  fighters; `CTRL+M` bombers; `CTRL+P` gunships; `CTRL+W` mobile water;
  `CTRL+N` mobile surface water; `CTRL+U` mobile underwater; `CTRL+X`
  experimental; `CTRL+Q` long-ranged structures; `CTRL+R` radar/sonar;
  `CTRL+J` radar/sonar jamming.
- **Session tooling**: whiteboard with `\` (draw, dot and text markers, wipe
  and spot erase, camera to newest ally marker), camera bookmarks
  (`CTRL+F5‑F8`), spectator/allied resource bars and view switching,
  `+shareall`/`+sharemetal`/`+shareenergy`/`+setshare*`, `.sharemappos`,
  `.take`/`.takecmd`, `.cmdwarp` with spawn arrows, `.syncon`, `.votego`,
  `.autopause`/`.ready`, `+clock`, `+bps`, `+screenchat`, `+bigbrother`,
  `+lostype`, `+contour`, `+logo`, `+view`, `+control`, `+ai`, `+los`,
  `+radar`, `+mapping`, `+nowisee`, `+doubleshot`/`+halfshot`, `+meteor`,
  `+nometal`/`+noenergy`, and `+makeposter` (full-map BMP captures).
- **Cheats/console**: `+atm` fills resources, `+unitname [player]` spawns at
  the cursor, `INSERT` repeats the previous command, PrintScreen or `CTRL+F9`
  saves a PCX screenshot, developer mode toggled with `F10` in single player
  (Alpha 3c), `+ai`/`+control` enabled in skirmish (Alpha 3).
- **Stability/behavior fixes documented in the version history**: `\` no
  longer crashes; `CTRL+F2` settings persist; reclaim cursor no longer
  discloses enemy commanders; damaged aircraft emit smoke; all wreckage
  passability and durability adjustments; projectile shadows removed;
  "SPECIAL" replaces "DIRECT" as the default caption when mixed selections
  include special-ability units; NoCD music executable.
- **Recorder note**: the readme states the recorder feature is not included in
  zip installs, yet `zplayx.dll` is shipped and provides multiplayer; see
  [TA Demo Recorder](ta-demo-recorder.md) for the recorder DLL's own feature
  list and generation differences.

## Directable factory build plates

**Established — "Direct" is the factory's third weapon.** Sixteen factory
definitions (all Arm and Core production labs/factories) author
`Weapon3=FactoryDir`; the weapon is a command-fire ability named "Factory
Buildpad Direction" (`CommandFire=1`, range 800, one-second reload, zero
damage, a beep start sound, no model). The user interface exposes command-fire
weapons as the special-ability button, so the player presses the ability key
and clicks a map point in range.

**Established — the plate is script-driven.** The build plate is the factory
model's buildpad piece. The factory script answers the engine's aim query with
that piece and, in its aim callback, snaps the plate to 0° when the aim
heading falls outside 90°–270° and to 180° when it lies between them — there
are exactly two valid positions, and the snapping is the script's, not an
engine table. The script's fire callback shows and animates a dedicated arrow
piece as the direction indicator.

**Established — what the engine does with the chosen point.** The factory's
per-tick update walks its three build slots; each slot stores either a unit id
plus a sentinel or a raw ground point. It resolves the slot target to a world
point (a raw point, or the target unit's sweet-spot centre plus a small offset
scaled by that unit's definition), calls the definition's build method with
that point, derives the build timer from health over maximum damage, aims the
factory's weapon at the point through the script callback — which rotates the
plate — and finally calls the move/exit helper with the same point. The
player's click is stored as the slot's raw-point form, and the
"not while building" restriction is the engine refusing to retarget a busy
slot (**Supported inference** for both).

**Established — the AI variants steer their own plate.** The AI factory
scripts read the recorder's extended ports `70` and `74`: port `70` returns
the unit-id iteration bound (scaled by ten, matching the script's
divide-by-ten) and port `74` compares two units' owners. The script steps a
sample of unit ids, finds the first non-allied unit and flips the plate to its
180° position when that unit lies in the western half-plane. The release note
describes AI factories as setting the build direction toward the enemy; which
way the plate's 180° position faces relative to that wording is a
**Supported inference** (see [Extended script ports](script-ports.md)).

**Unknown — savegame persistence.** The direction lives in per-unit slot
targets and the script's piece transform. The save format serialises each unit
field by field (the archive is a compressed container of per-object records
beginning with the type name and roughly a hundred bytes of state), but
whether the three build-slot blocks are among the serialised fields is not
established; a save/load test with a pointed plate would settle it. Either way
the plate re-aims each tick from whatever target the slot holds after load.

## Executable patch inventory

**Established — earlier comparison inventory, with corrections below.** This
inventory is not a completeness guarantee; the new trace corrects specific
field identities, target arithmetic and host controls in their owning sections. Only changed code paths are listed;
string, registry, directory, savegame-tag and import renames are described
above.

- **Session limits.** The requested game speed keeps retail's floor but loses
  its upper clamp, so speeds above the retail maximum are accepted. The
  per-player unit-limit preference cap rises from 500 to 5000 (the shipped
  default is 1500).
- **Factory build slots and order handling.** Two order handlers that cleared
  two of a factory's three build slots now clear one slot chosen by a value
  stored in the order record, so a new order no longer wipes the queued item on
  the other plate or plays its "target cleared" feedback. A new order sub-state
  was inserted into a build/cancel handler so the right-click cancel path
  records its state before cancelling, and a second handler advances state 2
  into a new state 3 with an extra effect call. One order-resolution guard now
  fires whenever its target argument is present instead of also requiring a
  helper predicate. The selection scan tests different unit flag bits, consults
  the build-slot validity predicate and clears a plate's stored target when the
  predicate fails, so stale plate targets are dropped. The player-visible
  consequences are a **Supported inference**; the code changes are
  established.
- **Unit behavior.** Self-repair becomes data-driven: the repair period uses
  the low byte of the definition's `HealTime` value as a frame mask instead of retail's fixed
  mask (retail pairs that key with the heal amount at a fixed cadence,
  [04 R-SPEC-01 §4](../retail-executable-spec/04-units-orders-scripts-and-movement.md)),
  and the damage/health fast path and self-repair block gain
  the same "not under construction" gate the selection scan already used: the
  unit's build-progress value must be zero, so a nanoframe neither repairs
  itself nor takes the fast health path while it is
  still building. The capture-capable construction placement cutoff changes from five to
  127 completed own builders. This is a live strategic census, not an authored
  profile key; see the corrected contract below. A block that could set a randomized target timer and call an
  engine routine is bypassed entirely.
- **Pathfinding budget.** The path-search constructor's cycle budget changes
  from retail's 1333 to 66650 — the value TA Zero's own settings file
  documents — and the optional Fix 10 renderer patches the same constructor, so
  either mechanism can raise it. The per-search movement-class allowance is a
  separate, unchanged value.
- **Target-point arithmetic.** The `SweetSpot` vertex-box transform changes
  all three offsets and negates model Z, not vertical Y. The exact signed
  arithmetic and its live-unit targeting scope are established below. The
  earlier interpretation as a factory exit-point defect is withdrawn.
- **Selection.** The selection scan that walks a unit's three build slots now
  tests different flag fields and bits, tests a single parameter bit rather
  than the whole parameter, and, after resolving a slot's unit, consults the
  factory build-slot predicate and clears a slot field when it fails.
- **Interface and front end.** The DirectX probe's warning now appears on that
  path regardless of the probe result; the `+atm` cheat adds an effectively
  unbounded resource amount, satisfying its documented "fills storage"
  behavior; `+ai` and `+control` move from the developer-only command class
  into the class available whenever a skirmish or multiplayer game runs; one
  vtable assignment is removed; the front-end layout file is renamed; the
  player-setup fallback side index moves from 2 to 3 (consistent with the
  third faction); two enumeration values move from 2 to 3; an animation
  counter cycles 1–5 instead of 1–7; and two object initialisers change.
- **Dead new code.** About 400 bytes of new code in otherwise unused mapped
  space are unreachable in the shipped file: nothing references them, the
  shipped renderer (which patches a small fixed set of executable locations at
  load) does not target them, and the appended bytes at the file end are a new
  application icon. Their logic is an automatic unit-cycling highlighter
  matching the documented `+bigbrother` control: it keeps an auto-cycling
  state bit (elsewhere toggled by a hotkey case) and a private high-water
  index, then walks the local player's units and selects the first unit past
  that index that carries the same "cyclable" flag bit as the built-in
  next-unit helper, sets that record's highlight bits and stores it in the
  tracked-unit global used by the built-in track/cycle helpers, clears the
  highlight on every other matching record, and remembers the index so
  successive passes advance through the list in build order — exactly one unit
  is highlighted per pass. It ends by jumping back into the two cut sites (the
  `CTRL_<letter>` hotkey case and the by-name command helper), so it was meant
  to run inside those paths. Whether a build outside the inspected files
  reaches it is **Unknown**.

**Established — verified unchanged.** Savegame serialization (only its
extension and header tag changed), the CD check, the set of preference keys
read by the executable, the entry point, the section table and the TLS
directory, and all retail cheat/command names are unmodified.

**Established — most advertised engine features live in the engine DLL, not
the executable.** `zdraw.dll` owns the preference-driven knobs and the
presentation logic: per-player unit limit; unit-type and weapon-type table
sizes; special-effect limit; pathfinding map entries; model composite buffer
width and height; the multiplayer weapon-identifier patch; double-click
selection and circle select; the whiteboard; the megamap (full-screen enable,
wheel zoom, zoom-to-cursor, double-click-to-zoom, under-attack icon flash,
minimum ring distances for radar, sonar, radar jam, sonar jam and anti-nuke,
icon configuration file, per-player dot colours, marker icons, nuke icon) and
its contact colours including radar; chat macro on `F11`; an autoclick key; a
model-sanity path with bad-unit diagnostics; and a per-unit visibility test
used while drawing. The shipped build patches only five executable locations
at load — three restorations of bytes that are already correct and two
immediate-constant changes — and reads the executable's globals directly for
everything else; the optional Fix 10 build patches more (including the
pathfinding-search constructor and the unit-limit site). Its megamap,
whiteboard and preference-key handling is recorded in
[Shared draw-DLL interface](draw-engine-interface.md); the earlier held-view and eleven-step description is withdrawn by the
[shipped host audit](#shipped-host-controls-and-geometry). This build supplies
icon configuration from
`ZIcon/iconcfg.ini`, the nine layer toggles, `UnderAttackFlash` and the
sensor-ring thresholds. Sound mode, mixing buffers, game speed and player
count are read by the executable itself, so the ten-player skirmish default is
a preference rather than a code constant.

**Supported inference — correspondence to `+bigbrother`.** The earlier
observation of unreachable unit-cycling logic does not establish the command's
runtime behavior. The author's controls documentation establishes the
advertised control; a bounded observation of this exact package is needed to
connect it to the recorded candidate mechanism.

## Asset-driven extensions

**Established — the unit-definition surface stays on retail keys.** A census
of the installed unit definitions found no non-retail unit key with a
demonstrated reader; Alpha 5 authors the same key families as its Base
(including `BadTargetCategory`, `SteeringMode`, `TEDClass`, `UnitNumber`).
Weapon files add `SoundLava` on one weapon (`arm_infvirus`). The retail weapon
key vocabulary contains only `soundstart`, `soundhit` and `soundwater`, and no
shipped binary contains `soundlava`; no reader is established. Absence from a
literal string census alone does not prove that no reader can exist.

**Established — feature `ShootMe` is retail.** Crystals author `ShootMe=1`;
the key and its readers are retail ([fmt tdf](../formats/README.md),
[04](../retail-executable-spec/04-units-orders-scripts-and-movement.md)).

**Established — scripts use retail opcodes and a small new port surface.**
A scan of all compiled unit scripts found only retail instructions; some
scripts read engine ports `70` and `74` (19 and 13 units respectively, both
also read one-argument and five-argument forms). The observed units are
construction aircraft, dropships, factories/labs and their AI variants; the AI
factory variants use the two reads to sample unit ids and compare owners while
choosing their build-plate side (see the Direct section above). No script
writes an extended port. These are recorder-provided ports shared by all three
packages; their semantics are recorded in
[Extended script ports](script-ports.md).

**Established — `UnitControl` is a script convention, not an engine command.**
258 units define a script named `UnitControl`, and the unit `Create` scripts
start it with the ordinary `start-script` instruction; the body uses ordinary
retail instructions and reads both retail ports (for example build and health
percentages) and, in the units listed above, the extended ports `70`/`74`.
The engine does not call this name.

## Version boundary: current TADR profile

**Established — source.** MIT TADR at
`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f` selects `TDRAW_CONFIG_TAZERO` in
[`config_tazero.h`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/config_tazero.h).
The [`release workflow`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/.github/workflows/compile.yml)
packages a newly built `tdraw.dll` with `totala.ini`, feature text and
`dist/tazero/zplayx.dll` in `tdraw-tazero.zip`; the committed recorder
distribution is `2026.9.9`. Its `tdraw.txt` installation table lists
"Zero Alpha5-060322" and tells users to rename the draw DLL and INI to
`zdraw.dll` and `tazero.ini`. That table is not evidence that the archive is
the December 2024 Alpha 5 release or the 2019 Base's renderer/recorder pair.

**Established — distinct profile choices.** The current `tazero` build enables
the guarding/patrolling constructor changes and construction-site kick-out
that the historical package does not document. It enables wind synchronization
and the clock overlay, but hides wind and tidal rows. Unlike current `prota`,
it disables air-stack splash overflow and the contested-cell tie-break. The
repair module, falling aircraft wrecks, extended weapon-ID protocol,
share-abuse guard, local mute, percentage-share commands and allied queued
build display are also off. Its off-map targeting margin is one tile, its
metal/geothermal snap radius three cells and wreck snap radius one. The shared
startup offers rotation and veterancy readers and, unless the host disables
nanoframe preview, preview-key readers even though the historical Alpha 5
authored unit-key census does not use them. See
[Community patch engine behavior](community-patch-engine.md#31-feature-matrix-at-the-pinned-revision)
for the implementation contracts. These are source-profile facts, not
permission to substitute this gameplay for Alpha 5's historical rules.

## Authored package, factions and single-player coverage

**Established — installation dependencies.** The Alpha 5 `TA Zero Readme.txt`
requires Base, the Alpha 5 overlay, and Map Pack Version 1f for the ZIP path;
it also names original TA archives to copy, including `totala1.hpi` and
`totala2.hpi`, with expansion maps dependent on the player's installed
expansions. It requires at least 1024×768 resolution. The older Alpha 4
installer route additionally names TA Patch Resources and then the Alpha 5
and map-pack overlays. These are different install compositions. Base plus
Alpha 5 alone provides neither a base-game replacement nor the required map
pack. The locally inspected Base/Alpha 5 pair contains no map directory.
The initial unit/model audit excluded Map Pack 1f; the separately identified
map-pack audit below now covers that artifact without changing the earlier
audit's composition or results.

**Established — authored namespace and faction order.** `TAZ31.gp3` uses
`ZUnits`, `ZWeapon`, `ZGameDat`, `ZUnitPic`, `ZBuildMenu`, `ZGui` and `ZI`.
Models, scripts, sounds, textures and animations retain their ordinary shared
directories. `ZGameDat/SIDEDATA.TDF` orders the factions as follows:

| Side index | Name / commander | Interface GAF | Button font | Energy / metal color index |
|---|---|---|---|---|
| 0 | `GOK` / `GoKCommander` | `GOKINT` | `armbutt` | 117 / 117 |
| 1 | `ARM` / `ArmCommander` | `ARMINT` | `armbutt` | 68 / 68 |
| 2 | `CORE` / `CoreCommander` | `CORINT` | `corbutt` | 102 / 102 |

The first side is GoK, not Arm. The archive authors all three faction main
panels, per-builder menus and commander pages in `ZGui`, including
`GoKCommander0.gui`, `1.gui` and `2.gui`. Side count, order, commander name,
interface art, palette indices and menu-page origin must therefore come from
content. A third selectable name attached to a retail Arm panel is not the
authored interface. The twelve-slot/repeated-membership contract remains in
[Extended build menus](build-menus.md).

**Established — presentation dependencies beyond model loading.** Alpha 5
ships replacement `Palettes/PALETTE.ALP` and `PALETTE.SHD`; its inspected
archive does not supply `PALETTE.PAL` or `GUIPAL.PAL`. The Base/Alpha 5
composition therefore relies on original assets for the missing base palette
resources while retaining these mod lookup-table overrides. Its front-end
bitmaps include `FrontendZ`, `LoadGameZbg` and `SingleZbg`, with corresponding
localized variants. `TAZero.ini` selects `ZIcon/iconcfg.ini` and changes player
5 and 9 minimap colors to palette indices 198 and 36. The Alpha 5 readme's
change list separately names revised build pictures, textures, explosion
animations, weapon sounds, factory animations and giblet fixes. The current
Nanolathe loader/resource audit, including limitations of successful catalog
compilation, is owned by
[Mod engine-package compatibility](mod-engine-compatibility.md).

**Established — authored frontend and team-colour details, rechecked against
Alpha 5 on 26 September 2026.** `Textures/LogoZ.gaf` contains the ten-frame
`32xlogos` entry and ten-frame Arm/Core/GoK colour-texture entries. It is a
team bank, not the main-menu title. The three renamed PCX backgrounds above
also exist with the same basenames under `Bitmaps-French`, `Bitmaps-German`,
`Bitmaps-Italian` and `Bitmaps-Spanish`. `Anims/skirmish.gaf`'s `SIDEx` entry
has six frames: GoK, Arm, Core, Watch, pressed, disabled. Only the first three
correspond to the authored faction list; Watch is not a fourth faction.
Selecting a side from a fixed two-stage retail UI prevents choosing Core.
These observations establish resources and ordering; they do not establish
legacy observer/session behavior. The shipping Nanolathe UI consumes the
faction count and the profile's explicit resource paths at composition.

**Established — authored behavior reaches beyond new engine keys.** The Alpha
5 readme documents a one-tick wait in anti-air primary aim scripts so the
third weapon can select anti-air mode, 33 ms script sleeps, adjusted weapon
damage charts including AI variants, and revised factory opening/closing
animations. It documents plasma-shield recharge changes for Core Titan and
Thor and shield-energy changes for the GoK commander's VSOC ability. These
are concrete callback-order, timing, targeting, effects and resource
acceptance cases even though the opcode census is retail. The authored
source and compiled-script audit below now settles the listed callback
contracts. It does not establish every historical projectile collision or
rendering edge case. No generic shield formula follows from the word
"shield" or from the later `tazero` engine name.

**Established — AI and music are independently authored.** `ZI` supplies
Acid, AirBattle, Default, Hover, Metal, SeaBattle, Urban and WaterWrld
profiles. Alpha 5 documents increasing the AI's preference and allowance for
air factories; its archive also includes GUI pages for named AI-only factory
and economy variants. AI compatibility must cover those authored choices,
not merely the three human build trees. Base's `tamus/_README_TAMUS.txt`
documents local MP3 playback of tracks `2.mp3` through `17.mp3`, while
`1.mp3` is a bonus intro theme excluded from ordinary in-game track selection.
That readme specifically distinguishes installing the music-capable patch
from installing Patch Resources alone. The music files and `zmusi.dll`
are part of the inspected Base, independently of Alpha 5's gameplay content.

### Map Pack 1f: composition and authored map requirements

**Established — separately identified artifact.** The supplied
`TA_Zero_Map_Pack_v1f.zip` contains only `TA_Zero_Maps.ufo` and
`Map Pack Readme.txt`. The readme identifies Version 1f, dated 24 December
2024, and requires `TA_Features_2013.ccx` or newer in the game folder before
adding the map archive. Base already supplies the identified 2013 feature
archive. This pack is distinct from the ProTA-specific map pack mentioned by
the readme; they must not be treated as interchangeable versions.

| Artifact | SHA-256 |
|---|---|
| `TA_Zero_Map_Pack_v1f.zip` | `3017e4322a502d7b42ac86f520a910dbb7a8664815b7cf44542d8a6547f3f3f8` |
| `TA_Zero_Maps.ufo` | `26e25843d1ef885b367c7f49e31e9e206fcf45e874420ba549dc476c0a96c50a` |
| `Map Pack Readme.txt` | `b8dbad697d3d28cee0bf76f0ba972dc6fe1abf2f90e210db375d2da239015d22` |

**Established — authored namespace and map selection.** The archive contains
15 matching OTA/TNT pairs under `Maps`, 25 feature-definition files under
`Features/TAZ_*`, and 25 feature GAFs under `Anims`. It supplies no new units,
scripts, weapon definitions, palettes, faction interfaces or campaign files.
Every OTA has one `Network 1` schema, no placed-unit/feature sections and
`SurfaceMetal=0`; map features are instead referenced by the TNT feature
table and grid. The filename prefixes `2P` and `4P` do not give the number of
authored starts: each schema authors four to ten positions. For example
`2P Hiemal Duel` and `4P Gathering` both contain `StartPos1` through `StartPos10`.
Use the schema's actual starts for setup, retain the map-file identity, and
read terrain dimensions from TNT rather than the textual size or name
([OTA](../formats/ota.md), [TNT](../formats/tnt.md)). All maps select
`DEFAULT` AI except `2P Scramble`, which selects `AirBattle`; Zero's `ZI`
directory must supply that choice through the content layout.

**Established — weather depends on Alpha 5 weapons.** The following are raw
schema authoring values, in the order radius, density, duration, interval;
their conversion and runtime scheduling belong to the OTA/meteor contracts.
All three referenced definitions resolve in Alpha 5's `ZWeapon/System.tdf`.

| Maps (filename prefixes omitted) | Meteor weapon | Authored parameters | Required authored weapon resources |
|---|---|---|---|
| Brimstone Steppes, Heated Argument, Highland Hellscape, River of Flame | `FireRain` | `4000, 15, 2, 5` | `ModMagma1` model; `ModFX2/Weather_FireRain1`; `ModFX/Water_Splash5`; `MagmaHit1` sound |
| Hiemal Duel | `Hailstorm` | `2000, 20, 10, 1` | `ModHail1` model; `ModFX2/Weather_Blizzard1`; `ModFX/Water_Splash5` and `Lava_Splash3`; `HailHit1`/`HailHit2` sounds |
| Raindance | `Tempest` | `4000, 30, 3, 1` | `ModFX3/Weather_Tempest1` for ground, water and lava impacts |
| Gathering | `Tempest` | `6000, 60, 3, 1` | same Tempest resources, with different schema scheduling values |

The pack readme warns about six maps requiring Zero weather, but omits Hiemal
Duel; its authored `Hailstorm` reference establishes a seventh dependency.
The weapon definitions author default damage 10 for FireRain, 1 for
Hailstorm and 0 for Tempest; FireRain also authors `FireStarter=100` and
Tempest `Paralyzer=1`. Weather cannot be replaced by a generic decorative
overlay while claiming these authored contracts. This records content, not
proof that all historical engine branches match the current community patch's
map-weapon behavior. The weather graphics come from Alpha 5 rather than this map archive.
The earlier loader audit is recorded in
[Mod engine-package compatibility](mod-engine-compatibility.md); the later
bounded frame-cache acceptance is recorded in
[TA_ZERO_SUPPORT](../../docs/TA_ZERO_SUPPORT.md).

**Established — map-specific feature presentation and economy.** The pack
authors animated vents, lava, sparks, forges and power-core art, separately
named shadow sequences with `ShadTrans=1`, permanent metal deposits with
`Metal=254`, and geothermal markers. Metallurgy/Power Core use definitions
in the `Invisible` category with their own named GAF sequences; the readme
documents these as invisible deposits/vents rather than a substitute stock
feature. Crystal Gorge actually places `TAZ_Gorge_pCrystal43`, whose death
and reclamation successors are `TAZ_Gorge_pCrystal42` then
`TAZ_Gorge_pCrystal41`, with distinct
art, height and resource values. The final stage is nonblocking. Sources:
the pack's `TAZ_Metallurgy_Invisible.tdf`, `TAZ_Gorge_Crystals.tdf` and
corresponding GAFs. The 1f readme additionally documents revised start positions and
reclaimable-resource placement, especially on Brimstone Steppes and River of
Flame. Correct tiles alone do not establish feature animation, shadows,
reclamation transitions, resource placement or geothermal admission.

**Established — bounded production-parser check, 22 September 2026.** A
temporary probe used Nanolathe's VFS and default `LoadOTA`, `LoadTNT`,
`ParseTDF` and `LoadGAF` readers on the identified map archive. All 15
OTA/TNT pairs, 25 feature TDFs (173 sections) and 25 feature GAFs decoded
without error. All terrain files use canonical TNT; the largest is Highland
Hellscape at 52,253,828 bytes, below Zero's existing 64 MiB TNT file limit.
Every stored minimap is 252×252, including strongly nonsquare terrain such
as Howling Abyss (140×392 cells) and Brimstone Steppes (514×200 cells).
With original retail assets, Base, Alpha 5 and this pack mounted in that
order, every TNT feature-table name found an authored feature section.
The pack's own feature sprite/shadow names and death/reclamation successors
also resolved to its own GAF entries/feature definitions. No donor files or
increased decoder limits were used. This is parse and reference evidence,
not full catalog/session admission, correct feature placement, a weather
simulation comparison or visual validation. At that stage the runtime/visual cases had not been exercised. The later
world acceptance below closes the stated runtime cases, with its explicit
visual and historical-engine limits.

### Package acceptance cases

These requirements are based on the established authored/documented surfaces
above. The bounded checks in the later audit sections and
[TA_ZERO_SUPPORT](../../docs/TA_ZERO_SUPPORT.md) record which portions pass;
the list itself is not a completeness certificate:

- Mount original data, Base, Alpha 5 and the identified Map Pack 1f in the
  declared order. Preserve `Z*` winners and the shared feature/palette
  dependencies; keep the map-pack check distinct from the earlier unit/model
  audit.
- Start one human and one AI participant from each authored faction. Verify
  the exact commander and interface GAF, all three command panels, commander
  page zero and later twelve-slot pages, hotkeys and portraits at 1024×768 or
  above. Check the custom front-end backgrounds as well as the battle HUD.
- Run the anti-air primary/tertiary mode handoff, busy versus idle factory
  direction commands, aircraft/factory AI variants, one plasma-shield unit
  and the GoK commander's ability. Verify script timing, targeting, resources,
  displayed effects and save/load continuity against settled per-unit
  contracts; list the unresolved portions rather than inventing them.
- Render large explosion and death sequences using the actual mod GAF,
  shade and translucency resources, retaining explicit load failures. Show
  the custom minimap colors and megamap icons.
- Select the map pack's `Network 1` schemas using their authored start-position
  counts; inspect the tall Howling Abyss and wide Brimstone Steppes minimaps
  for correct aspect and coordinate correspondence. Exercise Gathering and
  Raindance's distinct Tempest settings, Hiemal Duel's hail, and a FireRain
  map with the authored impact art and sound. Check Crystal Gorge's feature
  successor chain and Metallurgy's invisible resource markers separately
  from terrain rendering.
- Exercise local music track boundaries separately from the intro theme.
  Legacy `.zsv` interoperability and campaign availability require their own
  evidence; neither is proven by starting a skirmish or recognizing the
  filename extension.

## Current release and source completeness audit

**Established — public release scope, checked 26 September 2026 UTC.** The
[official downloads listing](https://zero.tauniverse.com/ta-zero/) still
identifies Alpha 5 and Map Pack 1f, both dated 24 December 2024, as current.
The [release announcement](https://zero.tauniverse.com/2024/12/24/ta-zero-alpha-5-and-map-pack-1f/)
and bundled readmes agree. This check used indexed official pages because
direct page requests were refused; it is not a fresh archive download or a
claim about unpublished versions. The hashes above and in the map section
identify the local corpus actually tested.

**Established — bounded source correspondence.** The inspected MIT TADR
history has an explicit Zero profile starting at commit
`dbc88b02bc6516bead45346e458aa202cbebab03` (27 July 2026), also the first
[`tdraw-tazero.zip` release](https://github.com/tanvanman/TADR/releases/tag/v2026.7.27)
found in its published release list. None of the Base renderer/recorder or the
two optional renderer replacements matches a reachable Git blob in the
inspected history through `dcff5ddeb6bd1030e3f452c0f16e5f005850f62f`.
This rules out claiming an exact source match from that repository search;
it does not prove no historical source exists. Nanolathe's current `tazero`
feature-table values agree with the pinned configuration. They remain a
separate target from Alpha 5's patched executable and Base's older DLLs.
A source-to-binary manifest or reproducible matching licensed source build
would settle that correspondence. No patch disassembly was used in this audit.

**Established — complete parser/key census, bounded runtime coverage.** The
identified combined package over the reference retail install compiled with
zero donor substitutions: 269 unit definitions, 195 weapons, 4,737 features
(including shared/base content), 22 movement classes, 154 sound categories,
78 build menus, 1,047 placements, eight AI profiles and 290 maps (275 base
maps plus 15 from this pack). Every unit passed admission and every compiled
unit script loaded; the opcode and extended-port census above is unchanged.
These totals describe this mount composition, not hard-coded engine limits.
The inventory tool's tolerant mode supplied no replacements in this run.

The FBI keys outside the typed reader are already scoped in [FBI](../formats/fbi.md):
`TEDClass`, `UnitNumber`, `Designation`, `SteeringMode`, and unprefixed
`BadTargetCategory` do not establish missing simulation readers. Feature
`Permanent`, `Category` and `World` similarly do not override the established
[feature keys](../formats/tdf.md). `SoundLava` remains unresolved, rather than
being assigned an invented sound event.

**Established — unresolved authored references.** `GoKT1AirPad` names
`SoundCategory=GoKPlatform1`, which is absent from this package's sound table.
The existing retail name/decimal fallback selects category ordinal zero for
this nonnumeric miss ([02 "Cross-reference failure policy"]); do not invent
an alias or claim the author's intended voice. The two previously identified
missing model textures remain `armcolormeta4_1` and `goksphere2_5`.
The AI profiles contain unmatched `GOKT1GUNHSIP` and `GOKT2CONTANK_AI` tokens;
neither names a unit nor a category. Exact-name/category matching leaves
them inactive. No unit has the documented `CTRL_J` category. Corrected author
content or an author statement is needed before supplying any substitute.

## Authored combat abilities and callback contracts

**Established — authored Alpha 5 behavior.** Evidence is the identified
`TAZ31.gp3`'s `Scripts/*.bos` and `.cob`, `ZUnits/*.fbi` and
`ZWeapon/*.tdf`, plus its readme (SHA-256
`ddb03b3934e68bf7cc7513473aeab89547d8295bff02f5760b7dbbbf515a4113`).
The source scripts are authored content, not engine-patch source. Runtime
checks execute the released compiled scripts; a source literal is not assumed
to survive compilation unchanged. Nanolathe callback/damage checks below
establish bounded implementation acceptance, not a historical-engine trace.

### Core plasma and adaptive armour

All thirteen plasma-shield definitions enable the ordinary `ARMORED` port
and use their authored `DamageModifier`. Ordinary health damage is applied
before deferred `HitByWeapon` consumes the ready shield, displays its directed
impact piece for 100 ms, and begins its recharge counter. A hit while unready
does not restart that basic counter. No shield-hit script explicitly charges
energy. Control starts after construction finishes. Ordinary recharge visits
occur on alternate 100 ms control iterations; the counter is tested before
it is decremented, so reaching zero does not rearm until the next visit.

| Definitions | Modifier | Restart counter / special boundary |
|---|---:|---|
| `CoreCommander` | 0.5 | 20; every control iteration subtracts 1 while position changes, 2 while stationary |
| `CoreT2BTank`, `CoreT2HDTurret`, `CoreT2LasKbot` | 0.5 | 5 |
| `CoreT2GF`, `CoreT2GF_AI` | 0.5 | 5; opening disables shield/recharge, closing restores them through the authored timed stages |
| `CoreT2Gunship` | 0.5 | 5; highest movement-rate callback disables shield until a slower tier permits recharge |
| `CoreT2Mex`, `CoreT2PGen`, `CoreT2PGen_AI` | 0.5 | 10; a shielded extractor hit returns before its separate extraction-shutdown branch |
| `CoreT2Radar` | 0.5 | 10; active radar disables shield, deactivation immediately arms it |
| `CoreT2Shield`, `CoreT2Shield2` | 0 | 4; activation waits for the yard to close |

Patron and Bastion are large 20×20 and 31×31 definitions. Their scripts do
not assign armour to neighbours. Impact opens the yard, hides shield rings
and drops armour. Reactivation requests yard closure, repeatedly requests
ordinary clearance and sleeps 200 ms while the yard stays open; only closure
restores armour and the rings. Manual activation has two 500 ms stages.
Deactivation holds the disabled state. Their authored active energy use is
25/50. An ordinary zero-damage hit still invokes the script and collapses
the shield. A bounded ordinary-projectile check for all four Core/GoK generators seeds
an authored Marine EMG projectile at an outer occupied yard cell: a closed
shield consumes it with a hit callback and no health loss; after deactivation
the same contact point admits it without a callback. This establishes that
contact boundary, not complete aiming/travel or rendered perimeter geometry.
A neighbour-wide invulnerability aura is not justified.

Adaptive armour belongs to `CoreT2AAGunship`, `CoreT2AmpTank`,
`CoreT2AsKbot` and `CoreT2PDTurret`, all with modifier 0.75. A hit starts or
refreshes a timer (9 for the first three, 4 for the turret). On alternate
control visits, positive enables armour then decrements; zero disables it
and becomes −1. The first hit precedes activation. Raider also has a stowed
posture: it starts armoured with adaptive handling disabled, then its primary
aim callback leaves that posture and enables adaptation. Its inactivity
sequence restores stowed armour. Tests enter the correct posture before
asserting the first 100-damage hit and later 75-damage hits.

### GoK commander reserve and VSOC

The commander starts its completed arrival with 1,000 reserve and a 0.25
armour modifier. Every fifth 100 ms control iteration clamps negative reserve
to zero, adds 12 while enabled (another 12 during boost), and caps at 1,000.
The BOS literal is 12.5, but the released COB operand is 12. At 30 Hz these
pulses are 15 ticks apart. Charge indicators split at 750 and 500; disabled
shield rearms at reserve ≥250 and displays the low band on that visit.

The hit callback compares current integer health percentage with its stored
previous percentage. While ready it spends 36 times that decrease, with a
minimum of 10; values above 100 become 100 plus half the excess, truncated.
It updates the previous percentage, shows the directed hit effect, and drops
armour when reserve ≤0 unless boosted. The control loop also samples health,
so callback/poll ordering can produce the minimum charge. No hidden engine
accumulator may replace that script ordering.

`FireTertiary` arms the shield, enables boost, adds 200 reserve clamped to
200…1,000, sleeps 6,000 ms (180 ticks), then ends boost. Ending boost alone
does not drop armour; a later hit makes that decision. For twelve uncapped
boosted recharge pulses the integer gain is 200 + 12×24 = 488. The readme's
nominal total of 500 must not override the actual program; pulse inclusion
also depends on alignment and callback order.

`GoK_ComVSOC` is a command-fire, vertical-launch paralyzer: energy per shot
1,000, range 200, burst 24, burst rate 0.233, reload 30, area 400, edge
factor 0.25 and weapon timer 0.066, with two-phase/burnblow behavior and
unit-name damage overrides. It references `GoKComVSOC` sound and
`ModFX/Weapon_VSOC1` effects. Ordinary root launch pays once and invokes
`FireTertiary` once; burst clones repeat neither charge nor callback.
The committed checks cover reserve depletion/rearm, boost expiry, indicator
state, real root payment and save/restore continuation. A real root and all 24 authored burst impacts
stun nearby Arm infantry without health damage, repeated root payment or
repeated ability callbacks. It is one target class,
not exhaustive target-override or explosion-geometry coverage.

### All GoK void-shield formula families

**Established — authored content.** An exhaustive Alpha 5 script census finds 69
GoK unit definitions with shield-ready/reserve state: the commander and 68
others. All names appear in the table below or the extended-generator pair. The
matched compiled COB was checked for the numeric constants in each shield
assignment and comparison; this matters because fractional BOS literals truncate
individually before arithmetic. The following are compiled integer values, not
nominal percentages inferred from HP.

Ordinary light shields spend max(minimum, coefficient times the change in
integer health percent), without the heavy-shield reduction. Heavy shields
additionally replace costs above the listed pivot by pivot plus half the excess,
truncating toward zero. Every ordinary shield uses the health port and periodic
preceding-health sample described for the commander, so callback order remains
relevant. The table lists recharge increment per five-loop pulse, capacity,
disabled-to-ready threshold, gem upper/middle cutoffs, and authored enabled
initialization reserve. These reserve assignments do not imply that armour is
already active immediately upon creation or construction completion:
construction polling, boot animations, activation and factory state gates still
run before the enabled state is reached. Except where specified below, pulses
are each 15 normal drains, negative reserve is floored before replenishment,
reserve is capped afterward, and an unboosted hit at reserve<=0 drops armour.
Modifier is the FBI value narrowed by the normal fixed-point loader, so 0.05 is
not exact one-twentieth in the damage funnel.

| Unit definitions | Modifier | Loss coefficient | Min cost | Heavy pivot | Recharge | Cap | Rearm at | Gem cutoffs | Enabled initialization |
|---|---:|---:|---:|---:|---:|---:|---:|---|---:|
| `GoKCommander` | 0.25 | 36 | 10 | 100 | 12+12 | 1000 | 250 | 750,500 | 1000 |
| `GoKT1AAHover` | 0.25 | 7 | 5 | — | 2 | 100 | 50 | 75,50 | 100 |
| `GoKT1AATurret`, `GoKT1FAATurret`, `GoKT1Sonar` | 0.25 | 12 | 5 | — | 3 | 150 | 75 | 112,75 | 150 |
| `GoKT1AF`, `GoKT1AF_AI` | 0.25 | 45 | 5 | — | 9 | 750 | 187 | 562,375 | 750 |
| `GoKT1ATHover` | 0.25 | 12 | 5 | — | 5 | 200 | 100 | 150,100 | 200 |
| `GoKT1AirCon`, `GoKT1AirCon_AI` | 0.25 | 4 | 5 | — | 2 | 75 | 50 | 56,37 | 75 |
| `GoKT1AirPad` | 0.25 | 45 | 5 | — | 12 | 500 | 250 | 375,250 | 500 |
| `GoKT1AsKbot` | 0.25 | 3 | 5 | — | 2 | 100 | 50 | 75,50 | 100 |
| `GoKT1Bomber`, `GoKT1Gunship` | 0.25 | 5 | 5 | — | 2 | 75 | 50 | 56,37 | 75 |
| `GoKT1ConHover`, `GoKT1ConHover_AI`, `GoKT2Fighter` | 0.25 | 7 | 5 | — | 3 | 150 | 75 | 112,75 | 150 |
| `GoKT1ConSub`, `GoKT1ConSub_AI`, `GoKT1Geo`, `GoKT1Sub` | 0.25 | 27 | 5 | — | 6 | 250 | 125 | 187,125 | 250 |
| `GoKT1Destroyer` | 0.25 | 54 | 5 | — | 12 | 500 | 250 | 375,250 | 500 |
| `GoKT1Dropship` | 0.25 | 4 | 5 | — | 3 | 150 | 75 | 112,75 | 150 |
| `GoKT1ES`, `GoKT1ES_AI` | 0.25 | 6 | 5 | — | 5 | 200 | 100 | 150,100 | 200 |
| `GoKT1FEHover`, `GoKT1FEHover_AI` | 0.25 | 4 | 5 | — | 3 | 150 | 75 | 112,75 | 100 |
| `GoKT1FSNode`, `GoKT1SNode` | 0.05 | 10 | 10 | — | 3 | 300 | 75 | 225,150 | 300 |
| `GoKT1FTLTurret` | 0.25 | 36 | 5 | — | 6 | 250 | 125 | 187,125 | 250 |
| `GoKT1GF`, `GoKT1GF_AI`, `GoKT1NF`, `GoKT1NF_AI` | 0.25 | 60 | 5 | — | 12 | 1000 | 250 | 750,500 | 1000 |
| `GoKT1HDTurret` | 0.25 | 18 | 5 | — | 6 | 250 | 125 | 187,125 | 250 |
| `GoKT1HSKbot` | 0.25 | 9 | 5 | — | 2 | 100 | 50 | 75,50 | 100 |
| `GoKT1MS`, `GoKT1MS_AI` | 0.25 | 21 | 5 | — | 3 | 150 | 75 | 112,75 | 150 |
| `GoKT1PGen`, `GoKT1PGen_AI` | 0.25 | 5 | 5 | — | 3 | 150 | 75 | 112,75 | 150 |
| `GoKT1Radar`, `GoKT1SupHover` | 0.25 | 12 | 5 | — | 6 | 250 | 125 | 187,125 | 250 |
| `GoKT2AALauncher` | 0.25 | 42 | 5 | — | 7 | 300 | 150 | 225,150 | 300 |
| `GoKT2AARpod` | 0.25 | 24 | 5 | — | 7 | 300 | 150 | 225,150 | 300 |
| `GoKT2AATurret` | 0.25 | 27 | 5 | — | 11 | 450 | 225 | 337,225 | 450 |
| `GoKT2AF`, `GoKT2AF_AI` | 0.25 | 67 | 5 | — | 18 | 1500 | 375 | 1125,750 | 1500 |
| `GoKT2ATRpod` | 0.25 | 33 | 10 | 100 | 15 | 600 | 300 | 450,300 | 600 |
| `GoKT2AirCon`, `GoKT2AirCon_AI`, `GoKT2AirJammer` | 0.25 | 15 | 5 | — | 5 | 200 | 100 | 150,100 | 200 |
| `GoKT2ArtRpod` | 0.25 | 27 | 5 | — | 7 | 300 | 150 | 225,150 | 300 |
| `GoKT2ArtTurret` | 0.25 | 60 | 5 | — | 7 | 300 | 150 | 225,150 | 300 |
| `GoKT2ConHover`, `GoKT2ConHover_AI` | 0.25 | 13 | 5 | — | 5 | 200 | 100 | 150,100 | 200 |
| `GoKT2GF`, `GoKT2GF_AI` | 0.25 | 120 | 5 | — | 25 | 2000 | 500 | 1500,1000 | 2000 |
| `GoKT2Gunship` | 0.25 | 18 | 10 | 100 | 15 | 600 | 300 | 450,300 | 600 |
| `GoKT2JumpKbot` | 0.25 | 12 | 5 | — | 2+2 | 200 | 100 | 150,100 | 200 |
| `GoKT2Mex` | 0.25 | 30 | 5 | — | 12 | 500 | 250 | 375,250 | 500 |
| `GoKT2PDTurret` | 0.25 | 54 | 10 | 100 | 15 | 600 | 300 | 450,300 | 600 |
| `GoKT2PGen`, `GoKT2PGen_AI` | 0.25 | 54 | 5 | — | 7 | 300 | 150 | 225,150 | 300 |
| `GoKT2Radar` | 0.25 | 18 | 5 | — | 12 | 500 | 250 | 375,250 | 500 |
| `GoKT2Stealth` | 0.25 | 36 | 5 | — | 10 | 400 | 200 | 300,200 | 400 |
| `GoKT2SupHover` | 0.25 | 21 | 5 | — | 17 | 700 | 350 | 525,350 | 700 |
| `GoKT3SupRpod` | 0.25 | 150 | 10 | 150 | 37 | 3000 | 750 | 2250,1500 | 3000 |

Special boundaries:

- GoKT1AsKbot (Disciple) adds 15 reserve in FireSecondary; the periodic
  controller provides the later capacity clamp. The attack callback itself does
  not cap the gain.
- GoKT2JumpKbot (Valkyrie) adds 2 each recharge pulse and another 2 unless its
  motion mode is antigravity hover. That mode is selected when the base piece is
  more than three world units above queried ground. The ground-height and
  piece-height reads are therefore part of this ability.
- GoKT1FEHover and GoKT1FEHover_AI start with 100 reserve although their later
  capacity is 150. Do not normalize the initializer to capacity.
- GoKT1Radar and GoKT1Sonar retain a separate disabled state while activated;
  HitByWeapon blocks only in state 1, not merely any nonzero state. Their
  deactivate animation eventually returns the state to0, after which the
  controller may recharge/rearm. The GoK factory scripts (T1GF/T1AF/T1NF and
  T2GF/T2AF, including each _AI variant) also use state 2 while opened and state 0
  after closing. This differs from a generic always-on shield.
- GoKT3SupRpod (Matriarch) begins Create with shield disabled and reserve 0. It
  waits for construction completion and a lengthy boot animation before starting
  its control threads and assigning enabled reserve 3000 and armour 1; the earlier
  matching initializer inside a block comment is not operative. Energize is a
  later energy-piece animation, not this initialization. It uses a heavy
  pivot 150, not the commander/Immortal/Radiant/Pillar pivot 100; its restart
  threshold is 750 of 3000. GoKT1SNode and GoKT1FSNode use modifier 0.05,
  coefficient 10, minimum 10, and no heavy compression. These are distinct shield
  formulas.
- GoKT2Shield (Haven) and GoKT2Shield2 (Sanctuary) are a separate
  extended-generator family: reserve starts/caps at 40, a blocked ordinary hit
  costs 2 regardless of its damage, active recharge adds 1 per fifth 100 ms loop,
  and rearm requires at least 20 plus successful yard closure. DamageModifier=0,
  EnergyUse40/60, footprint20x20/31x31. The impact keeps armour/perimeter until
  reserve<=0, when it opens the yard and drops armour/rings. Activation uses a
  lower reserve>=10 admission check before its yard/animation steps; the later
  steady controller still requires20 to rearm. Their display thresholds are30
  and20. Deactivation disables recharge and places the shield in the distinct
  state 2. Their three gem groups and two ring pieces follow those states. Their
  small per-impact reserve is not the ordinary HP-percentage shield formula.

This establishes the authored formula census. Runtime representatives cover
light, heavy, Matriarch, node and extended-generator arithmetic; they do not
establish every per-unit interaction or historical timing edge case.


### Anti-air primary/tertiary handoff

Thirteen scripts put a one-tick sleep in primary aiming before the later
anti-air/reload wait. Tertiary aiming sets the shared anti-air flag first;
the resumed primary withholds readiness, tertiary can fire, and its fire
callback clears the flag so primary can resume. These are the released
Arm T1 AA hover/turret and T2 AA tank/turret; Core T1 AA ship/tank/turret and
T2 AA spider/turret; GoK T1 AA hover/turret and T2 AA launcher/ARpod.
Actual callback tests enqueue primary before tertiary in the same visit and
check this ordering, then primary resumption.

**Established — authored exception.** `ArmT1FAATurret`, `CoreT1FAATurret`
and `GoKT1FAATurret` omit that initial sleep in both BOS and COB. The same
fixture grants both aim callbacks. The readme's blanket fix does not match
these three released scripts. Preserve the content; do not add a generic
weapon suppression rule to conceal this discrepancy.

## Authored world and AI acceptance

**Established — authored content and bounded Nanolathe observations.** The
identified Map Pack 1f feature/TNT data and Alpha 5 definitions/scripts were
loaded through the production readers. The following contracts now have
installed-content acceptance in `internal/session/zero_world_retail_test.go`:

- The actually placed `pCrystal43 → pCrystal42 → pCrystal41` chain succeeds
  through both damage and reclamation. Its successive reclaim pools are
  60 metal/300 energy, 60/300 and 30/150; the last stage is nonblocking and
  then disappears. Empty death/reclaim animations make these transitions
  immediate. The unprefixed crystal chain also exists but is not the TNT's
  chosen art variant.
- Both Metallurgy and Power Core place `TAZ_Metallurgy_Metal1`: its 3×3
  footprint seeds metal byte 254 into every cell; ordinary extraction reads
  byte plus one. It is indestructible and nonreclaimable. The shared invisible
  vent admits all three faction T1 geothermal plants under their central 3×3
  `G` cells inside a 5×5 yard. A vent under an outside corner is insufficient.
- Actual neutral meteor projectiles apply FireRain's 10 damage and Hailstorm's
  1 damage without resource debit. Tempest supplies zero paralyze credit and
  causes no lasting stun. Projectile-only stepping isolates these contacts
  from self-repair. All four FireRain maps have no flammable placed features
  in this corpus; the ordinary firestarter-to-feature path is retained rather
  than adding vegetation or decorative-only weather.
- All six faction T1 human/AI construction-aircraft variants create the
  corresponding economy nanoframe through ordinary construction orders.
  A human construction aircraft loads and unloads ordinary cargo through the
  human command path; its AI counterpart intentionally lacks that capability.
- Scramble binds its authored AirBattle manager. All three T1 AI air factories
  receive Default weight 40 and easy/medium/hard limits 2/4/4; AirBattle gives
  weight 60 and limits 4/6/6. The committed test exercises easy and hard
  boundaries; the complete eight-profile sweep also checked medium.

**Established — further bounded probes, not additional permanent tests.**
All three T1 dropships and all three human T1 construction aircraft loaded and
unloaded an `ArmT1InfKbot`. All six T2 human/AI construction aircraft and all
three faction T1 AI ground and air factories raised their first authored
products without script diagnostics. These are successful production starts,
not a full tree or long-match AI acceptance. Direct allocation of a factory
must align its centre to its footprint: the Arm air factory's 6×5 footprint
needs different x/z parity; a misaligned convenience fixture can collide with
its own yard and does not prove a content defect.

**Established — definitions beyond the runtime sample.** Human T1 aircraft
builders author capacity 1/size 3, worker 60/range 150; T2 author capacity
1/size 4, worker 120/range 150. AI variants keep construction rates but omit
transport capability, capacity/size and weapons. T1 dropships are capacity
1/size 3. ArmT2Gunship and CoreT1Gunship author capacity 1/size 2;
GoKT2AirJammer authors worker 60/range 150 without transport. T2 transport,
those gunships' transport and AirJammer construction were not separately run.
The two unresolved ZI tokens above remain inactive rather than receiving
speculative aliases. Neither this sweep nor the parser census establishes
historical AI threshold semantics, long-match strength or visual/audio parity.

## Historical passive self-repair caller

**Established — Alpha 5 executable behavior.** The identified Alpha 5
executable's ordinary per-unit update retains the retail player-control gate
and update position: after water damage and before cloak settlement. It admits
a passive repair visit only when all of the following hold:

1. Read `HealTime` as a signed 16-bit value `h`; it must be nonzero.
2. Sign-extend the unit's stored 16-bit health, then compare it unsigned with
   maximum health. It must be below the maximum. Negative stored health fails.
3. The stored construction fraction must have all bits zero. Negative zero is
   rejected too; this is stricter than a floating-point equality to zero.
4. The low eight bits of the global tick and `h` must have no common set bit:
   `(uint8(tick) & uint8(h)) == 0`.

On admission the caller forms signed integer work `trunc(32 × h / 30)`,
converts that integer to single precision, and calls the ordinary repair helper
with the unit as both repairer and target. Exhaustive arithmetic comparison
across all 65,536 signed-16 inputs verified this independently written formula
against the shipped caller's integer operations. It is neither a modulo test
nor a literal interval for arbitrary authored values: `h=5` admits low-byte
ticks 0, 2, 8, 10, …; `h=256` admits every tick. The released positive masks
3, 7, 15, 31, 63 and 127 are the regular-period subset.

**Established — contribution and resources.** The helper's complete body is
byte-identical to the retail helper described by [05 R-WORK-01 §3](../retail-executable-spec/05-economy-construction-players-and-features.md).
It retains the signed health/max-health entry check, the existing repair-term
arithmetic, positive-term clamp to one, one-resource energy admission, and
ordinary healing packet. Thus a normal positive authored definition gains one
stored HP and requests one energy per admitted visit; a positive energy carry
refuses both. Full health stops visits, no repair is banked, and no random
number is drawn. This is distinct from the newer proportional `RepairRate`
module, which the current Zero source table disables.

| Authored `HealTime` | Work | Regular period in ticks | Stored HP and energy per second at 30 Hz |
|---|---:|---:|---:|
| 3 | 3 | 4 | 7.5 |
| 7 | 7 | 8 | 3.75 |
| 15 | 16 | 16 | 1.875 |
| 31 | 33 | 32 | 0.9375 |
| 63 | 67 | 64 | 0.46875 |
| 127 | 135 | 128 | 0.234375 |

These rates assume damaged, completed units whose energy admission succeeds.
Effective durability under armour is a separate damage conversion; it does
not multiply the energy charge. The author's [Alpha 4b announcement](https://zero.tauniverse.com/2017/06/04/ta-zero-alpha-4b/),
[version history](https://zero.tauniverse.com/version-history/) and Alpha 5
Thor change corroborate the construction exclusion and varying rates.
The former eight-tick Nanolathe caller's value-3 zero-healing result was an
implementation mismatch, not an unresolved replacement contract.

## Historical Classic AI construction cutoff

**Established — Alpha 5 executable and unchanged census producer.** The
construction task's placement pass admits capture-capable builders while the
player's completed build-capable count is **below 127**, replacing retail's
five. The counter is recomputed during the ordinary strategic refresh: count
completed own live units whose compiled builder list exists, irrespective of
whether that list has entries. It is not a profile parameter, unit-type limit,
factory queue size or an unknown authored key. Its producer and accessor are
unchanged from retail [08 R-AI-01 §3 and §16](../retail-executable-spec/08-sessions-campaign-ai-network-save-and-replay.md).

Only the placement comparison changes. The second, independent reposition
pass still admits a capture-capable builder at **five or more**. Counts 5
through 126 can therefore reach both passes in one invocation. The damage
throttle, candidate/placement decisions, group order, 90-tick reschedule and
existing random draws remain those of the retail task. This contract concerns
the Classic planner; Nanolathe's independently chosen Modern AI is separate.

**Established — implementation.** Zero's profile now explicitly selects 127
through the existing Community feature vocabulary and planner boundary.
Focused tests cover 4/5/126/127, the unchanged reposition pass, and Strict's
zero-feature bypass. ProTA's separate five/ten shortcut and authored ZI files
retain their contracts.

## Historical SweetSpot target arithmetic

**Established — Alpha 5 executable.** The script query and piece selection
retain retail's contract [06 R-WPN-04 §1](../retail-executable-spec/06-weapons-projectiles-damage-and-effects.md).
The chosen piece's own loaded vertices are scanned with both extrema seeded
at zero. Parent offsets, animation state and unit rotation are not applied.
For each axis form the wrapping signed-32 sum `s = min + max`, then
`d = s + 1` when `s < 0`, otherwise `d = s`. Add `dX` and `dY` to the target
position, and subtract `dZ` from it, with ordinary 32-bit wrapping. This is the
retail signed-halving correction retained after the division was removed;
it is not exactly twice the old centre for every odd sum. A negative even
sum also retains the extra one raw fixed-point unit.

The consumer is the ordinary weapon-slot live-unit target resolver, before
its existing lead calculation. It is not a factory-only exit transformation.
Z is the map-plane axis that the model-to-world convention negates; Y remains
the vertical axis. The old claim of an inverted vertical term is corrected.
No compensation is added in the unchanged query or resolver. Whether an author
intended each resulting aim point is outside what the binary establishes.

**Established — implementation gap.** Nanolathe's target resolver still uses
the retail halved centre. A future implementation must select this arithmetic
through the existing combat rules and preserve both the cached and uncached
resolver contracts, Strict's centre, and the odd/negative/empty-piece cases.


## Historical recorder ports used by Alpha 5

**Established — shipped Base and matching historical source.** Base's
`zplayx.dll` identified above reports version 3.9.2.0. Direct inspection and
MIT TADR revision `03257756f4876f7a6b1b08ec1de5bda669f15ff1` (9 July 2013),
[`COB_extensions.pas`](https://github.com/tanvanman/TADR/blob/03257756f4876f7a6b1b08ec1de5bda669f15ff1/src/Recorder/plugins/COB_extensions.pas)
and [`TA_MemoryLocations.pas`](https://github.com/tanvanman/TADR/blob/03257756f4876f7a6b1b08ec1de5bda669f15ff1/src/Recorder/TAMem/TA_MemoryLocations.pas),
agree on this bounded subset. This is not a claim that this exact revision
built the whole DLL. Its ordinary plugin registration admits the Alpha 5 host
signature; neither AI, local-player, network nor playback status gates these
reads. Both getter forms reach the same dispatcher. No script setter is
registered by this historical DLL.

- **Port 70:** multiply the selected unsigned 16-bit per-player limit by ten.
  With the old altered-limit selector clear, select the configured maximum;
  with it set, select the effective lobby limit. Ignore all arguments. Current
  TADR instead selects using menu/game state and a separate mission limit.
- **Port 74:** for valid populated records, return exactly 0 or 1 from the
  reading owner's directional alliance entry for the target owner. Use the
  complete unsigned 32-bit argument-one pattern, not its low word. The old
  lookup accepts zero through configured-maximum × ten, inclusive; its bound
  need not equal port 70's chosen limit. Zero denotes the sentinel record,
  not the reading unit. There is no live-unit, visibility or reciprocal gate.
- **Invalid paths:** out-of-range IDs and rejected/missing owner references
  have uninitialized historical returns and unchecked subsequent reads.
  This establishes no safe deterministic fallback. Nanolathe's bounds
  hardening is retained; reproducing undefined state is not a compatibility
  requirement. Current TADR's low-word narrowing and initialized fallback
  must not be attributed to this Base DLL.

The compiled census's 19 port-70 and 13 port-74 consumers is corroborated by
authored sources. Ten AI factory scripts use both in Create; the three T2
AirCon scripts use both in Activate. T1 AirCon and dropship scripts use only
70; similarly worded alliance checks in their comments do not execute. They
sample positive first-slot IDs at a stride of port70/10. Factories orient
toward the first sampled non-allied owner; T2 AirCon restricts its nearby
sampled-unit drop check to non-allied owners. These are not full unit-pool
searches.

**Established — Nanolathe boundary.** Its ordinary valid-target alliance
lookup agrees, and its reported limit agrees when the session's selected
limit equals Base's selected limit. It does not model separate historical
configured/lobby selector state. Its zero/high-word/invalid-target treatment
is separately bounded; no exhaustive lobby/campaign/save equivalence follows.
The exact DLL-wide source revision is still unknown but no longer blocks
these port contracts. All 33 registered patch sites were also checked: none
replaces the passive-healing caller or shared repair helper.

## Factory groups and automatic camera cycling

**Established — retail mechanism; corrected implementation.** The previously
unresolved group handoff is the ordinary completed-product GetBuilt path,
independently established from retail assignment/recall and kill consumers
[04 §3.8][04 R-FAC-02 §4]. A mobile product with a retained producer copies
that producer's current control group at its completed GetBuilt visit, after
queuing rallies, under the alive/not-dying guard and an occupied human owner
row. Zero clears a group. Later producer changes do not propagate. Nanolathe
had copied kills at that point; the field identification is corrected in the
owning retail contract and implementation. This is a baseline correction in
all gameplay modes, not a Zero-only rule.

**Supported inference — historical Zero applicability.** The existing
Alpha 5 difference inventory records no change to that handler, consistent
with the author's promise. Fresh verification of every installed historical
DLL hook was blocked by automatic approval review; a matching historical hook
source or loaded-image observation would close that remaining applicability
boundary. The retail correction does not depend on it.

**Established — distinct BigBrother mechanisms.** Retail already implements
automatic camera cycling, with a 90-tick counter and Shift pause
[04 R-MOV-03 §1][07 R-CAM-01 §12]. The old inventory's unreachable added block
therefore does not show that the advertised command is inoperative. Existing
reports identify no caller into that extra block. Its connection to a loaded
historical runtime remains **Unknown**; do not replace the established retail
command with a guessed implementation of the unused block.

## Shipped host controls and geometry

**Established — versioned implementation audit.** The following contracts are
from Base `zdraw.dll` and the optional Alpha 5 replacements identified above.
Full replacement hashes: Fix10 `4eba5a70eb7862a465d2098bf179ff8164beb58dbf4cb540e60d564322edc6bc`;
Hotfix `f9d500e13d6bd56f8266c8d24f2e06fdf920351397bec038f4766ad30ec5ad80`.
The author’s [controls](https://zero.tauniverse.com/controls/) describe intent.
Current MIT TADR at `dcff5dd` provides comparison source in
[`tahook.cpp`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/tahook.cpp),
[`whiteboard.cpp`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/whiteboard.cpp),
[`elementhandler.cpp`](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/elementhandler.cpp),
and the megamap control/ring modules. The binary contracts below take
precedence over older untraced descriptions of these exact Zero artifacts.
Base and Hotfix agree in the inspected handlers and calculations; Fix10
differences are stated explicitly. This is not whole-DLL equivalence.

### Shared host ownership and preferences

**Established — Base and Hotfix.** The window dispatcher gives earlier handlers a chance to consume events. The relevant order is whiteboard, advanced dialog/other host handlers, X placement, then megamap, before the original game procedure. Therefore an event consumed by whiteboard or X never reaches the later megamap handler. X spacing wheel events take precedence over megamap wheel switching. This is not evidence that all mouse messages are consumed: the individual handlers deliberately return unhandled for several messages listed below.

**Established — all three packaged DLLs.** Advanced options load these values from the current user's `Software\TA Patch\Eye` registry key: autoclick `KeyCode` defaults to X; `WhiteboardKey` defaults to backslash; `MegamapKey` defaults to Tab; `OptimizeDT` and `FullRings` default enabled. A saved registry value overrides the default. The Alpha 5 `TAZero.ini` does not supply any of these key overrides. The current source options defaults agree on the keys. No direct hard-coded F4 megamap case was found in the inspected draw handlers; references to F4 in the key-name formatter are not bindings.

**Boundary.** This proves the DLL's clean-registry binding, not a particular user's saved binding or every installer composition. The author page's F4 instruction cannot be silently substituted for Tab. An observed F4 installation needs its actual advanced setting/registry or installer evidence. No Nanolathe Tab-to-F4 change is justified by the inspected package.

### X placement: established shipped contract

The following applies to Base and Hotfix. Fix10 independently retains the same event rules and geometry, compiled differently. It is a host command generator, not a simulation placement rule.

#### State and events

- The handler runs only during battle. Initial spacing is zero and remains a host value until changed; clamp it to 0..10 after each spacing change.
- Releasing the configured X key clears both line and surround modes and restores the normal build rectangle. Character messages are consumed while X and line mode are active.
- Holding X with PageUp increments spacing by one; PageDown decrements it by one. Each refreshes an active line or surround preview and is consumed.
- While X is held, the wheel takes **one** step per message. The implementation compares the **unsigned** high word with 120: values greater than 120 decrement; values at most 120 increment. Thus ordinary +120 increases and −120 decreases, but +240 also decreases and zero increases. This is not proportional wheel accumulation. The message is consumed, even if no preview is active.
- To start a line, a left-button **press** requires the prepared build order, X held, and screen X greater than 127. Product footprints equal to zero are replaced with one for this line path. Cache the product footprint, hide the normal build rectangle, set start and end to the current game map-pointer position, clear the candidate list, and calculate it.
- While a line is active, mouse movement updates its end from the game map pointer and rebuilds candidates. The button need not remain down.
- The next left-button **press** while X is held refreshes the cached product footprints, records the current end, dispatches the already cached candidate list, then sets a new start to the current map pointer and clears the candidates. There is **no recalculation on that press**. Line mode remains active, allowing another segment.
- Left release does not commit a line. If X is no longer held it clears line mode; an active surround consumes left release. Releasing X abandons the uncommitted preview.
- When no line is active, mouse movement with X and a prepared build order asks the ordinary unit picker for the hovered unit. A nonzero result generates surround candidates and enables surround mode. No extra owner or unit-category predicate is present in this layer. No unit clears the surround preview and restores the normal build rectangle. Other mouse movement clears surround mode.
- With surround active, the next left press dispatches its cached list and is consumed; that branch does not recheck the build order or X state. Holding X consumes right press. Right release clears surround mode without a consume return.

The author's drag wording is therefore insufficient to define a release-to-commit gesture. Implementing release commit would be a new host policy, not the traced shipped event contract.

#### Line geometry

Let start be `(sx,sy)`, end `(ex,ey)`, product footprints `(w,h)` in map cells, and spacing `s`. Coordinate units below are world-map pixels; one cell is 16 units. Arithmetic divisions truncate toward zero.

1. Compute `dx=trunc((ex−sx)/16)`, `dy=trunc((ey−sy)/16)`. Save each sign as a minor increment of +16 or −16, then take absolute magnitudes.
2. X is the major axis only when `abs(dx)>abs(dy)`; ties choose Y.
3. For X-major, set `n=abs(dx)/(w+s)`, major increment to `sign(dx)*16*(w+s)`, and minor distance `m=abs(dy)`. For Y-major exchange X/Y and use `h+s`.
4. If `n>=1000`, return without replacing the previous candidates. Otherwise emit `n+1` candidates, beginning at the unsnapped start. Candidate coordinates are narrowed to signed 16-bit values in the shipped implementation.
5. Initialize error to `2*m−n`. After each emission, while error is nonnegative and `n!=0`, subtract `2*n` and move one minor increment. Then add `2*m` and move one major increment.
6. `n=0` emits only the start. There is no separate forced end-point insertion.

These are the exact major-axis and rounding rules; merely describing this as a standard line rasterizer would lose the footprint division and comparison order.

#### Surround geometry

Let hovered unit's grid origin be `(gx,gy)`, its footprints `(A,B)`, the product footprints `(w,h)`, and spacing `s`. The product footprints here are read directly; the line path's zero-to-one normalization is absent.

Set `P=(16*(gx−s),16*(gy−s))`, `a=A+2*s`, `b=B+2*s`.

Set `nx=trunc(a/w)+1+extraX`, `ny=trunc(b/h)+1+extraY`. `extraX` is one only if FullRings is enabled, both product footprints are less than 3, and `a%w!=0`; otherwise zero. `extraY` uses `b%h` with the same gates.

Generate these four edge runs, in order:

| Edge | Indices | Candidate centre |
|---|---|---|
| Top | `i=0..nx−1` | `(Px+8*w+16*w*i, Py−8*h)` |
| Right | `i=0..ny−1` | `(Px+16*a+8*w, Py+8*h+16*h*i)` |
| Bottom | `i=0..nx−1` | `(Px+16*a−8*w−16*w*i, Py+16*b+8*h)` |
| Left | `i=0..ny−1` | `(Px−8*w, Py+16*b−8*h−16*h*i)` |

This yields `2*nx+2*ny` candidate centres before command-order optimization. Spacing expands the enclosing rectangle; it does **not** add gaps between adjacent products along an edge. There is no merge with nearby units, circular distance test, corner deduplication, or path search. Narrowing is again signed 16-bit.

#### Command order, admission, and historical hazards

**Established.** Both paths share an emitter. If OptimizeDT is enabled and the **cached line product footprints** are 2×2, it runs the line-order optimizer before dispatch. For stored last index `n>2`, scan `i=1..n−2`. Compare the coordinate on the minor axis: if candidate `i` equals `i+2`, swap those whole candidates and skip two additional indices; otherwise, if it equals `i+1`, swap those and skip one additional index. The ordinary loop increment then applies. The first candidate stays first; the final candidate can move. No candidate is invented or relocated by this optimization.

**Established hazard.** Surround generation does not refresh the stored line direction/last-index metadata or cached product footprint. A surround emitted after a previous line can therefore be reordered using that line's metadata. Do not promise unconditional clockwise command order. The constructor initializes the cached product footprint to 2×2 but does not initialize both optimizer metadata values in the inspected path; first-use behavior that depends on these values is not a defined portable algorithm. A safe Nanolathe policy must explicitly define the bounds/state rather than reproduce uninitialized memory. Likewise the historical large-list storage hazards are not permission to copy unsafe storage.

**Established.** Each candidate is fed to the ordinary build-spot validator followed by the ordinary map-click command, with queue/Shift semantics forced on. Pointer X/Y is saved and restored around each candidate. The host generator does not reserve the whole shape or abort the whole batch on one invalid site. The native admission path remains authoritative. The preview also invokes the native validator per candidate; Base uses its existing build-valid flag to choose palette index 234 versus 214. Current source adds preview/rotation/snap work around the old generator; those later additions are not evidence for shipped Zero behavior.

**Unknown boundary.** The exact ordinary-picker eligibility, final site snapping, and edge behavior belong to the called engine routines, not the DLL formulas. The formulas and call order are established; whole-game tests on uneven terrain, edges, or unusual zero-footprint definitions would settle user-visible corner cases. Simultaneous megamap/X input also needs a bounded manual test before promising pointer-update timing across both handlers.

### Whiteboard: local contract and corrections

#### Base and Hotfix

**Established.** The handler is battle-only and does nothing while megamap is shown; in these two DLLs that early return does not clear existing paint/move/editor/held-key state. Backslash press enables the held state unless the local player is a watcher; Ctrl on that press still requests a move toward the last received marker. Backslash release clears held state. These key messages are consumed. No game-area bounds predicate is present on the mouse branches.

Mouse positions become board positions by adding the camera top-left and subtracting screen offsets `(128,32)`. There is no terrain-height correction in this board transform.

| Input while held | Local effect | Consumed by whiteboard? |
|---|---|---|
| Left press on nearby text/dot | Grab that marker for movement | Yes |
| Left press elsewhere | Start freehand painting and remember screen point/camera origin | Yes |
| Mouse movement while painting | Add a line from previous to current screen point, using the previous captured camera origin for both; then refresh the point/origin | No |
| Mouse movement with right held, when not painting | Erase around the current transformed point | No |
| Mouse movement while a marker is grabbed | Queue its move and replace/move its local marker; this executes independently after painting/erase handling | No |
| Left release | Clear paint and move state | No |
| Left double-click | Open text creation/editing at the point | Yes |
| Middle press on empty marker neighbourhood | Create an empty text marker (dot) | No |
| Right press/release | Own those button messages | Yes |
| Right double-click | Small-area delete | Yes |

There is no middle-release creation branch and no right-click text-editor branch. The old shared draw-interface prose does not describe these shipped Zero handlers correctly.

Marker hit tests examine text-marker anchors in an inclusive box within five units on each axis. They return the last matching text encountered; there is no ownership/colour filter. Do not describe editing as restricted to the player's own markers. Lines are stored by their first point, not by segment/box intersection for erase.

**Established — text input.** Left double-click copies existing text when found, captures the point and camera origin, and opens the editor. Enter press commits edited or new text and clears the buffer. Escape press clears it without committing; Enter/Escape release closes the editor. Backspace deletes one byte. Accepted characters are space, ASCII 33..90, and ASCII 97..122; the check permits another byte while current length is below 51, so the maximum is 51 bytes. This is not a Unicode text editor. While the editor is open its handled key/character messages are consumed even after the hold key is released.

**Established — erase bug, verified against instructions.** Small deletion requests bounds `(x−10,y−10)..(x+10,y+10)`; wiping requests ±50. Base/Hotfix compute endpoint bucket indices by arithmetic shifting each coordinate right eight bits and masking with 63, exchange bucket endpoints if necessary, and scan the resulting inclusive rectangle of buckets. Within a bucket, the delete predicate checks only `anchorX>=lowerX && anchorY>=lowerY`; it omits both upper bounds. Thus the documented 20×20/100×100 descriptions are intended areas, not exact shipped clipping. Anchors to the right/bottom can also be removed within the buckets traversed. Marker search, in contrast, does apply both inclusive upper bounds.

**Established — local distribution boundary.** These DLLs do have an outgoing operation queue and serializer into the recorder's shared outgoing buffer. The drawing pass processes receive/send work. Local create/edit/move/delete paths both update local state and enqueue operations. The prior claim that no local packet builder exists is incorrect for these identified DLLs. Received text/dot markers update the last-marker coordinates, announce `New marker added: ` followed by text, and add a temporary animated minimap marker. Local creation does not update the remembered received-marker location.

Ctrl+backslash subtracts half the game viewport (screen width minus128, screen height minus64) from that remembered received position, then clamps the desired camera target to map scroll bounds. It requests camera movement rather than rewriting the current camera origin directly. Single-player has no established incoming-ally marker to jump to before receipt. End-to-end peer routing is outside the requested single-player scope and is not claimed here.

#### Fix10 and current-source differences

**Established — Fix10.** It keeps the mouse actions, coordinate transform, no game-area bounds check, 51-byte limit, and missing erase upper bounds. However, showing megamap clears painting, movement and editor state and resamples the physical whiteboard key. A watcher outside replay clears all those states and exits the handler; a replay watcher is admitted. Base/Hotfix instead gate initial key activation as above.

Fix10's store uses endpoint indices `(coordinate arithmetic-shift-right 20) & 399` with 400 buckets per axis. This surprising deletion arithmetic was independently confirmed against instructions; the insertion and query decompilations use the same bucket mapping. It is not `coordinate/20` and not a 400-cell modulo. On ordinary nonnegative map coordinates below1,048,576, it puts all such anchors into one bucket; with the missing upper bounds, erasure can therefore remove every anchor to the right/bottom of the requested lower corner. Do not substitute the current-source data structure for this build and call it parity.

**Established — current MIT source.** At the pinned revision, whiteboard has game-area predicates, state clearing during megamap, the watcher/replay gate, character-entry ownership protection, a 50-byte limit, and erase checks that include exclusive upper bounds. Its renderer/store also has later line-retrieval work. Those are later source contracts, not evidence that Base had these safeguards. The X geometry functions retain the arithmetic above, but the surrounding source handler now adds build rotation, snap/drag and chat-wheel ownership; those are separately versioned features.

**Unknown.** This pass establishes local operations, not whiteboard persistence across map changes/load/save or a replay's complete behavior. A lifecycle trace of all store-reset/serialization callers or a two-map/load observation would settle persistence. A historical source/build map would settle why Fix10 changed its bucket constants. Its byte-level arithmetic itself is established.

### Megamap: shipped binding and rings

**Established — all inspected packaged DLLs.** The configured key consumes press and release; only release toggles. FullScreenMinimap must be enabled and a battle active. The Alpha 5 INI enables it. Wheel backward with WheelZoom enabled opens a hidden megamap; wheel forward with it enabled closes a shown megamap, stops camera-following, and with WheelMoveMegaMap enabled moves the camera toward the pointer first. Wheel events themselves are not consumed by this handler. PageUp/PageDown do not implement a megamap zoom level in this handler; with X held, they belong to placement spacing. Key exit does not perform the wheel camera move. Double-click movement is a separate option; Alpha 5 disables it. Enter/leave use the Options/Previous sound aliases.

**Established — Base/Hotfix radius calculation.** Unit icons must pass the existing image admission/clipping before this ring block. Selected units admitted by the local LOS/alliance helper may draw sensor and interceptor rings. Sensor ranges are unsigned authored values; draw only when strictly greater than their respective effective minima. Radius is integer truncation of `range*bufferWidth/mapWidth`. Radar-jammer drawing uses the **radar** minimum rather than the separately parsed radar-jammer minimum. Sensor/jammer minima default zero; interceptor minimum defaults512, and absent-reader sentinel −1 retains the default. The Alpha 5 INI sets sonar minimum500 and other sensor/jammer minima0, interceptor512.

For each of three weapon slots, the interceptor ring requires the unit's antiweapons capability, that weapon's interceptor capability, and **raw coverage strictly greater than the minimum**. Then radius is `trunc((coverage−512)*bufferWidth/mapWidth)`. Do not subtract before the threshold comparison or use full coverage. There is no extra clamp after subtraction; the default minimum avoids a nonpositive radius, but a user can lower it. Each slot's existing dot indicator chooses solid when zero versus dashed when nonzero. Base/Hotfix pass32 and the current antinuke animation phase to the dotted-circle helper. The meaning and production of that indicator are not re-derived by this renderer audit.

The historical map dimensions are `(TNT width−1)*16` and `(TNT height−4)*16`; the radius denominator is the former. Image width is aligned down to a multiple of four in the drawing path before radius arithmetic. Fix10 independently retains all three coverage subtractions, strict minima, radar-jammer quirk, and historical map width denominator.

**Established — current-source difference.** Current `UnitMinimap` uses full coverage for interceptors and a feature-map-based usable width `(featureMapWidth−2)*16` (with fallback); its height equivalent subtracts8. That does not prove a shipped Zero bug. Nanolathe's existing older ProTA-style coverage subtraction agrees with the inspected shipped Zero arithmetic. The present pass gives no basis for a Zero-specific full-coverage correction.


## Unknown

- **Unknown — complete historical gameplay parity.** The historical passive caller is now established above. Shield
  projectile geometry, exhaustive per-unit combat interactions and historical
  renderer comparisons are not established by parser or callback checks.
- **Known implementation work — host controls.** X line/surround and local
  whiteboard are absent, but their versioned algorithms are established above.
  Remaining unknowns are the normal picker's edge eligibility, simultaneous
  megamap pointer timing, whiteboard map/load lifetime and undefined surround
  optimizer state. These need bounded caller/lifecycle analysis or observations;
  unsafe historical state needs an explicit safe host policy, not a guess.
- **Unknown — dead-code reachability in other builds.** The new unit-cycling
  block is unreachable in the inspected Alpha 5 files; whether any build
  outside them installs a signature patch that reaches it is not established.
  A matching historical source revision or bounded manual observation of the
  advertised control would settle its applicability.
- **Unknown — complete historical recorder build identity.** The used 70/74
  contracts are now settled above. The exact whole-DLL revision and every
  historical session-limit combination remain outside that bounded match.
