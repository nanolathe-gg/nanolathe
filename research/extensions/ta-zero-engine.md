# TA Zero Alpha 5 engine package

## Evidence scope and sources

This document records what the TA Zero Alpha 5 release documents, what its
authored content contains, and what its shipped runtime files implement. It
does not establish retail behavior and approves no Nanolathe behavior. Retail
rules stay owned by
[retail-executable-spec](../retail-executable-spec/README.md); the twelve-slot
build menus are owned by [Extended build menus](build-menus.md).

**Established — artifact identity.** The inspected release is TA Zero Alpha 5
(24 December 2024) installed over TA Zero Base (13 December 2019), identified
by the bundled `TA Zero Readme.txt`. Inspected files and SHA-256:

| File | SHA-256 | Role |
|---|---|---|
| `TotalA.exe` (Alpha 5) | `24726c36002f6a25ce59892cf4791aa5dacaa0a3bd491bb0dc2fffec3bbe6dd0` | retail 3.1 image with in-place patches, a rewritten import table and a rebuilt resource section |
| `zdraw.dll` (Base) | `34993ee8f4b2d194df83f61e0fb19fa8833ba7d911de510a2e55b6c34f12968a` | engine-extension renderer |
| `zplayx.dll` (Base) | `44f2d45fefe294110ac5ac7a4dccbc5c1f27f002414b1fb2886b4ec69b092aae` | TA Demo Recorder session DLL (older generation) |
| `zmusi.dll` (Base) | `bfd96f5385984e8082db694f4d0192d65b660c1479df70f1a243a9e959beb202` | music backend (identical to ProTA's `tmusi.dll`) |
| `online.dll` (Base) | `6e3eec29498728fd29b12dc6230b694dd26e5d0f8697c7e2d372b2bdf1e67920` | retail 3.1-era network helper (byte-wise different from the baseline's 1998 copy; imports and strings identical) |
| `TAZ31.gp3` (Alpha 5) | `51ef8804ee67883672d311a89d6b37125858f0eb91bb47af1450359c8e1a2ec4` | content archive (hash from [Extended build menus](build-menus.md)) |
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
replay file. Source provenance and license are not established.

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

**Established — the patches below are the complete behavior inventory found by
source-level comparison with retail.** Only changed code paths are listed;
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

**Established — the `+bigbrother` control's intended behavior** is the dead
code block above: one unit at a time is highlighted and set as the tracked
unit, advancing through the local player's cyclable units in build order on
each press, with all other candidates cleared.

## Asset-driven extensions

**Established — the unit-definition surface stays on retail keys.** A census
of the installed unit definitions found no non-retail unit key with a
demonstrated reader; Alpha 5 authors the same key families as its Base
(including `BadTargetCategory`, `SteeringMode`, `TEDClass`, `UnitNumber`).
Weapon files add `SoundLava` on one weapon (`arm_infvirus`). The retail weapon
key vocabulary contains only `soundstart`, `soundhit` and `soundwater`, and no
shipped binary contains `soundlava`; the key has no reader.

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

## Unknown

- **Unknown — dead-code reachability in other builds.** The new unit-cycling
  block is unreachable in the inspected Alpha 5 files; whether any build
  outside them installs a signature patch that reaches it is not established.
  A debugger check of the loaded process would settle it.
- **Unknown — the AI profile threshold's authoring key.** The changed 5 → 127
  gate reads a per-player AI profile value; which authored key (or initialiser)
  fills it is not established. Tracing the remaining profile commands would
  settle it.
- **Unknown — port `70`/`74`'s remaining questions.** The ports are the
  recorder's shared `COB Extensions` interface, and their arithmetic is
  established; which engine state the iteration range reads and exactly what
  the relation test means remain open in
  [Extended script ports](script-ports.md).
