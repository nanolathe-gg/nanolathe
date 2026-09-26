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

No executable addresses, offsets, disassembly or decompiler output are
recorded here. **Evidence provenance:** the implementation detail below was
decoded from the shipped binaries — a method the
[evidence policy](README.md#evidence-policy) does not permit for third-party
patch binaries. The wording is clean-room and the observations stand as
recorded, but no new contract may be closed by this method: future gaps must
be settled from documentation, authored content, appropriately licensed
source, or a bounded manual observation.

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

**Established — the earlier binary comparison recorded the following patch
inventory.** This is the legacy evidence covered by the provenance note, not
a source-derived completeness guarantee. Only changed code paths are listed;
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
  the definition's `HealTime` value as a frame mask instead of retail's fixed
  mask (retail pairs that key with the heal amount at a fixed cadence,
  [04 R-SPEC-01 §4](../retail-executable-spec/04-units-orders-scripts-and-movement.md)),
  and the damage/health fast path and the damage-smoke/self-repair block gain
  the same "not under construction" gate the selection scan already used: the
  unit's build-progress value must be zero, so a nanoframe neither repairs
  itself, nor emits damage smoke, nor takes the fast health path while it is
  still building. A per-player AI profile threshold changes from 5 to 127,
  letting more candidates through the factory/order placement walk for players
  whose profile value is large; which authored key fills that profile field is
  **Unknown**. A block that could set a randomized target timer and call an
  engine routine is bypassed entirely.
- **Pathfinding budget.** The path-search constructor's cycle budget changes
  from retail's 1333 to 66650 — the value TA Zero's own settings file
  documents — and the optional Fix 10 renderer patches the same constructor, so
  either mechanism can raise it. The per-search movement-class allowance is a
  separate, unchanged value.
- **Build-point arithmetic.** The routine that resolves a unit's `SweetSpot`
  script value into a piece-space point — consumed by the factory build-slot
  aim and exit-point logic and by weapon aiming at that unit — no longer halves
  the summed bounding extents and negates the vertical term, so the returned
  point is twice the box centre with the opposite vertical sign while the
  consumer adds only small offsets. Example: a selected piece whose box centre
  sits ten units above the unit origin yields +10 in retail and +20 with
  mirrored sign here, displacing a factory plate's aim threshold and the
  exit/placement point by a full model height for tall sweet spots.
  **Supported inference** that this is a defect: nothing compensates for it; a
  rendered measurement of a factory exit point or a tall unit's aim point would
  settle it.
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
[Shared draw-DLL interface](draw-engine-interface.md); this build contributes
the held megamap view, the eleven zoom steps, icon configuration from
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

## Unresolved Zero passive self-repair

**Established — documented requirement and implementation mismatch.** The
[Alpha 4b announcement](https://zero.tauniverse.com/2017/06/04/ta-zero-alpha-4b/)
attributes custom self-repair rates to executable changes. The author's
[version history](https://zero.tauniverse.com/version-history/) excludes
self-repair during construction. The Alpha 5 readme raises Thor from 1.88 to
3.75 HP/s; its FBI authors `HealTime=7`. Other definitions author 3, 15, 31,
63 and 127. The [Core guide](https://zero.tauniverse.com/core-units/) also
separately documents effective durability repair and energy use for Hex.

Nanolathe still uses the retail eight-tick caller and integer quantum
`uint16(HealTime)×8/30`. In bounded damaged-unit checks over 240 tick
opportunities, the value-3 commander, Behemoth, Tyrant, Adamant and both
shield generators regained zero HP and requested zero energy. Tested value-7,
15 and 31 definitions each regained 30 HP and requested 30 energy. This
proves that the documented varying rates are not implemented. It does not
establish an alternative resource formula or justify altering authored values.

**Unknown — exact historical caller.** The source search through the pinned
TADR history found no active Zero per-definition healing cadence. A Recorder
construction guard introduced in commit
`b8c0acc09ba4631b7e39ceb6ebc1f706e2d07251` has its registration commented
out at the pin. The current Zero profile disables the separate repair-rate
module. Neither closes the historical executable contract. The older
binary-derived inventory above remains subject to its provenance restriction.
Needed: licensed patch source, precise author documentation, or bounded manual
observations on the identified executable covering cadence/phase, quantum,
energy admission/stall, unfinished units and mask boundaries. Keep the retail
caller and a code TODO until that evidence exists. No passing Zero acceptance
test presents the mismatched repair behavior as supported compatibility.

## Unknown

- **Unknown — complete historical gameplay parity.** Passive self-repair is a
  confirmed mismatch with the unresolved replacement contract above. Shield
  projectile geometry, exhaustive per-unit combat interactions and historical
  renderer comparisons are not established by parser or callback checks.
- **Unknown — factory control-group inheritance timing.** The controls page
  promises products inherit a factory group; Nanolathe does not copy it.
  Allocation versus completion, reassignment during construction and clearing
  need matching source or a bounded manual observation before an existing
  construction rule seam can implement the behavior.
- **Unknown — additional host geometry and bindings.** X line/surround
  placement with wheel spacing and local whiteboard are documented but absent.
  The historical F4 megamap binding conflicts with the current source's Tab
  binding and Nanolathe's score panel. Current-source interceptor ring radius
  also differs from the older scoped ProTA contract. These need their own
  versioned input/presentation contracts; no silent key/radius substitution
  follows from selecting Zero.
- **Unknown — dead-code reachability in other builds.** The new unit-cycling
  block is unreachable in the inspected Alpha 5 files; whether any build
  outside them installs a signature patch that reaches it is not established.
  A matching historical source revision or bounded manual observation of the
  advertised control would settle its applicability.
- **Unknown — the AI profile threshold's authoring key.** The changed 5 → 127
  gate reads a per-player AI profile value; which authored key (or initialiser)
  fills it is not established. Matching licensed historical source or a
  bounded manual comparison using authored profile changes would settle it.
- **Unknown — historical port applicability.** The current recorder source
  settles port `70` as ten times the relevant per-player unit limit, and `74`
  as the reading owner's one-directional ally flag for the target owner; see
  [Extended script ports](script-ports.md#port-table). It does not identify
  the source revision of Base's `zplayx.dll`. A historical source mapping or a
  bounded observation of that DLL is needed before assuming every boundary
  case of the current source applies to Alpha 5 over Base.
