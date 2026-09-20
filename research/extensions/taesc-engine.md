# TA: Escalation Gold 10.2.0 engine package

## Evidence scope and sources

This document records what the Escalation Gold 10.2.0 install documents, what
its authored content contains, and what its shipped runtime files implement.
It does not establish retail behavior and approves no Nanolathe behavior.
Retail rules stay owned by [retail-executable-spec](../retail-executable-spec/README.md);
the three weapon target keys shared with other sets are owned by
[Non-retail weapon target keys](weapon-target-keys.md).

**Established — artifact identity.** The inspected release is Gold 10.2.0,
identified by its bundled `ESC_READ_ME.txt`, `GOLD_10_2_0.txt` and
`TAESC.ini`. Inspected files and SHA-256:

| File | SHA-256 | Role |
|---|---|---|
| `TotalA.exe` | `06d9e87f35846c31913f989abae7223f62e8ebc8d3c3d175b561bc9c8f238600` | retail 3.1 image with in-place patches and a rewritten import table |
| `TAESC.dll` | `61783acaa51ae5348e91ca57706540cff39cbf9905ba6fb26020603d394965df` | engine extension DLL (listed as `EDRAW.dll` in the release manifest) |
| `eplayx.dll` | `29e9f66d93e788dbb2cee0cb3c9015094c7cdbe1de38a665be3b5fdc9842967b` | TA Demo Recorder session DLL |
| `emusi.dll` | `b798698f3d818989beeb673011b7a06e9ee54b1c30e55398cbb448ba7b4ef558` | music backend |
| `TAESC.ini` | — | engine preferences |
| `ddraw.dll` | — | optional cnc-ddraw renderer (Step 3) |

**Established — the executable is a rebranded retail build.** Its size equals
retail 3.1's, its code section is retail's with a bounded set of patches, its
version resource now identifies the release (version bytes `10,2` and `GOLD
10.2.0` where retail has `3,1` and `v3.1`), and its import table names
`TAESC.dll`, `EMUSI.dll` and `EPLAYX.dll` in place of `DDRAW.dll`, `WIN32.dll`
and `DPLAYX.dll`. Data strings for content directories, registry path, INI name
and savegame path are renamed (`unitsE`, `weaponE`, `gamedatE`, `unitpicE`,
`downloadsE`, `guie`, `aE`, `Software\TA Esc`, `taesc.ini`). Gameplay changes
therefore live in this patched image plus the engine DLL, not in a separate
loader.

**Established — the engine DLL is a build of the shared community engine
family.** `TAESC.dll` shares large amounts of text and structure with ProTA's
`tdraw.dll` and TA Zero's `zdraw.dll` (megamap drawing, whiteboard,
challenge/response verification, factory recycling fix, start-position
assignment, wind synchronization, "Swedish Eye ver 0.8" diagnostics). It is
the most feature-loaded observed build, adding build-preview, rotation,
veterancy and unit-definition extension machinery absent from the other two.
Exact source provenance and license are not established.

No executable addresses, offsets, disassembly or decompiler output are
recorded here; inspection was performed outside this repository under the
extension evidence policy.

## Global engine changes (not asset-driven)

### Documented release features

**Established — documented.** `ESC_READ_ME.txt` §11 and `GOLD_10_2_0.txt`
describe these engine-level changes; `TAESC.ini` documents the settings.

- **Limit raises**: pathfinding cycles 1333 → 66650; unit identifier table
  512 → 16000; weapon identifier table 256 → 16000 (multiplayer increase off
  by default for replay compatibility); special-effect limit 400 → 20480;
  unit model drawing buffer 600×600 → 1280×1280 (up to 4096 documented in the
  INI comments); simultaneous sounds 8 → "unlimited" (128 mixing buffers).
- **Real-time unit limit**: the per-player limit is applied from `TAESC.ini`
  (`UnitLimit`, documented range 20–6553, default 1000) rather than a
  pre-patched constant.
- **Host/OS fixes**: Alt+Tab without crashing; no-CD; the DirectX
  environment check disabled; developer mode moved from F11 to F10 and gated
  on cheats; the "`+now Film Chris Include Reload Assert`" command disabled.
- **Cheats and console**: `+atm` fills storage regardless of capacity;
  `+ai`/`+control` re-enabled; `+unitname` spawns the named unit at the
  cursor; INSERT repeats the previous command or cheat; `+logo` cheat-mode
  only; new commands `+autoteam`, `+randomteam`, `+spawnon`, `+spawnoff`,
  `+setsharemetal`, `+setshareenergy`, `+shareall`, `+sharemetal`,
  `+shareenergy`, `+noshake`, and `.ready`/`.autopause`.
- **AI**: per-difficulty resource multipliers doubled relative to retail
  (Easy 1×, Medium 2×, Hard 4×, per the readme's development section); AI
  energy-appliance shutdown on low energy; AI aircraft crash guard; AI squad
  and commander-order corrections; a corrective pass on AI move orders for
  commander-category units.
- **Session/multiplayer**: expanded multiplayer sharing menu; ally and
  spectator resource bars; spectator view switching and camera lock;
  vote-to-reject for timed-out or dropped players (10.2 DLL update);
  share-abuse guard (sharing more than ten units triggers a delay; 10.2);
  start positions assigned from teams/alliances; `+autoteam`; ten-player
  replay support and verification fixes.
- **Presentation/input**: line-of-sight and map-position sharing between
  allies; whiteboard drawing/markers/erasers with `\`; camera-to-marker
  shortcut; double-click select-same-type (configurable); drag-selection
  filters (W/B/Y); `CTRL+S` selects on-screen armed units; `CTRL+F`/`CTRL+B`
  cycle idle factories/builders with `CTRL+SHIFT` variants; build-in-line or
  build-surround with `X` and mouse-wheel spacing; `CTRL+SHIFT`-click orders
  or cancels 100 units; PrintScreen screenshots; expanded battleroom and map
  selection with 128-character descriptions; new side colors.
- **Megamap**: full-screen strategic view with unit-type icons, wheel zoom,
  under-attack flashing, minimap/marker colors, per-sensor range-ring minimum
  thresholds, icon configuration (`Icon/iconcfg.ini`); 10.2 adds
  antialiasing and alpha blending for smoother output. The shared renderer
  interface behind these keys — held view key, zoom steps, ring thresholds,
  icon selection — is recorded in
  [Shared draw-DLL interface](draw-engine-interface.md).
- **Combat/behavior fixes**: reclaim of any unit except commanders;
  "sparking" prevention; wreck value and wreck-passability changes; offscreen
  VTOL penalty and stack-fire/health penalty; Krogoth-clone fix; commander
  minimap reclaim-detection fix; ballistic/`noautorange`/turreted-vlaunch
  weapon acquisition changes; several acquisition fixes while reclaiming,
  capturing, repairing or building; hold-position guarding behavior; healing
  gated on completed construction; minor `setSFXoccupy` fix.

### Documented engine-DLL features

**Established — documented.** `GOLD_10_2_0.txt` records these as changes to
the shipped DLLs.

- **Move queued orders (10.0)**: drag a queued build or move order to a new
  position after queueing.
- **Construction-unit patrol behaviour (10.0)**: on hold position a patrolling
  construction unit only reclaims; on maneuver/roam it also repairs and
  assists. User-selectable per-role options exist in the advanced interface
  menu (strings `ConUnitsPatrol*Option`, `ConUnitsGuard*Option`).
- **Vote-to-reject (10.2)** as above.

**Established — click snap.** The interface hook object holds a mex radius, a
wreck radius and the override key code. The radii are the options-menu integer
fields `MexSnapRadius2` and `WreckSnapRadius2` after clamping, which in this
build caps mex snapping at zero (disabled) and wreck snapping at one cell; the
older menu sliders (`Mex-Snap Radius`, default 8, and `Wreck-Snap Radius`,
default 26, both capped at 200) are still constructed but have no reader.
Snapping runs in the mouse handler only while the override key is not held.
The cursor mode chooses the search: build placement searches for metal or
geothermal spots, reclaim searches for wrecks. Both scan the square of cells
within the radius around the cursor. Wreck candidates must be reclaimable
(positive metal or energy) and carry a reclaim flag; metal-spot candidates are
scored by how much of the current build's footprint covers cells whose
resource density meets its requirement. The candidate with the highest score
wins, ties go to the nearest by squared distance, and the click position is
rewritten to that cell and consumed. The override key is a configurable
virtual key (`ClickSnapOverrideKey`, presented as "Snap Override Key"; the
default value is **Unknown**). No snap-marker drawing call exists in this path
(**Supported inference**).

**Established — nanoframe preview (BuildGhost).** The placement ghost is gated
by the `NanoframePreviewFill` configuration value, read once and cached:
enabled draws a shimmering fill plus frame, disabled draws the frame plus a
scanline with no fill. The renderer caches per (unit type, facing) — facing
indices 0–3 are the same S/E/N/W order the rotation feature uses. The ghost is
composed from the pieces named by the directional `PreviewPiecesS/E/N/W` key
when authored, else the base `PreviewPieces` key, else the unit's whole model.
Animation is a one-second, thirty-tick cycle quantised into 32 palette steps:
the fill mode drives the fill colour from the phase, the no-fill mode drives
the scanline intensity from it. `PreviewObject3D` loads a replacement model
from `objects3d/<name>.3DO`, and `PreviewFaceOpponent` re-orients the ghost
toward the nearest opponent of the local player. Drawing temporarily moves the
game's screen origin to the queued preview position and restores it.

**Established — share-abuse guard.** Two hooks: the `.take`/`.takecmd` command
handler and the transfer path. A take is refused when any player slot other
than the local one holds a commander at less than one hit point — that is, the
trigger is the *other* player's destroyed commander, and the message names the
player ("Cannot take %s: their commander has been destroyed."). Separately,
the transfer path queues a share batch when the pending count plus the new
structures exceeds ten: the batch is stamped with a deadline 900 ticks (thirty
seconds) ahead and the game shows "Sharing %d structures - transfer will
complete in %d seconds."; a per-tick callback performs it at the deadline.

**Unknown — repair-rate exploit fix.** The release notes document it
("abusing low tier cons to fast repair higher tier units is less effective vs.
higher tier cons"), but no repair-related string, definition key or
configuration key exists in the shipped DLLs, and no arithmetic comparing two
unit types' worker or build values could be isolated in the hook bodies.
A runtime trace of the repair step, or a comparison of the repair contribution
between the retail and Escalation executables, would settle it.

**Supported inference — other machinery.** The engine DLL additionally
contains machinery whose documented description is thinner than the
implementation evidence: challenge/response verification of the executable,
DLLs and game files with CRC reporting; a per-player spawn on/off switch; a
factory identity-recycling fix; wind-speed synchronization with host-derived
seeding; crash diagnostics that write an error log; explosion-cap telemetry;
and a "weapon target key" hook (`WeaponTdfHook`). Exact trigger conditions and
arithmetic are not established here.

### Executable patch inventory

**Established — source-level comparison with retail found these behavior
changes in the image** (string, registry, directory, savegame and import
renames excluded):

- **Weapon-slot handling on orders.** The special/disintegrator order
  deactivates only its own weapon slot instead of slots 1 and 3 together, so
  weapons 1–2 keep acquiring targets while the special is used; two further
  order states deactivate one slot chosen by the order's weapon index.
- **Weapons stay active while building.** In the build (nanolathe) order
  handler's placement state, on the builder and immediately before the
  construction site is spawned as a nanoframe, retail switched the builder's
  three weapon/nanolathe slots off and Escalation switches them on, so they
  remain active for the whole build. This is the only such swap in the image;
  it matches the documented "allow weapons to acquire targets while building a
  new unit" (**Supported inference** for that match, established for the
  change).
- **`CantBeTransported`.** Units whose definition sets the flag are excluded
  from VTOL repair-pad landing, from a transport set-up path and from a
  repair/nanolathe path, with cursor fallbacks; this matches the release note
  that such VTOLs ignore repair pads.
- **`+showranges`.** The third weapon's range display tests the third weapon
  slot's "has weapon" flag instead of the first slot's, matching the release
  note about weapon range three on units without a first weapon.
- **Healing.** Self-repair requires a fully built unit (nanoframes no longer
  self-heal), uses the definition's `HealTime` as the repair period instead of
  the fixed eight-frame cadence retail pairs with it
  ([04 R-SPEC-01 §4](../retail-executable-spec/04-units-orders-scripts-and-movement.md)),
  and floors the heal amount at one instead of capping it at one.
- **Spawn classification.** A per-unit spawn class byte is derived from the
  unit's height against the global water level, and the occupancy/SFX
  classifier's boundary case below the waterline was adjusted.
- **Unit-type data.** Yard maps are now parsed for definitions regardless of
  their `bmcode` byte; retail skipped the tag for every type with nonzero
  `bmcode` (the mobiles). The shipped content gives all 316 mobile definitions
  only open yard cells, and the engine's three yard-map consumers are gated on
  the building class, so the patch has no reachable effect for this content
  (**Established** for the mechanism and the content state).
- **AI.** Movement-class selection keys off the definition's Commander flag
  instead of `CanCapture`, matching the release note's move-order correction;
  squad size and the build/queue threshold rise from 6 to 10 and 5 to 10;
  targeting clears a weapon's stale target when it cannot engage; nearest-enemy
  filtering gained extra eligibility tests. The per-difficulty resource
  multipliers become 0.5×/0.7×/1× → 4×/2×/1× and the difficulty name slots are
  swapped, so index 0 selects Hard and index 2 Easy: the documented mapping
  (Easy 1×, Medium 2×, Hard 4×) is confirmed, with the internal index order
  reversed relative to retail.
- **Interface.** The backslash key no longer repeats the last console command
  (Insert does); F10 became an additional developer-mode toggle while F11
  remained bound; several "landing aborted"-type messages moved to full unit
  chat severity; the final scoreboard no longer filters players by activity
  state; team handling no longer clears one ally flag; the multiplayer allies
  button is no longer forced off; and reclaim failures now report and set the
  order state.
- **AI order preservation.** A block in the damage-application path that ran
  when the victim could capture, was AI-owned, and stamped a per-AI 30–330
  frame timer — wiping the victim's entire order queue — is removed, so AI
  units keep their orders when attacked. This matches the documented "prevent
  unit behaviour changing when being attacked"; the stamped timer's consumer
  was not found elsewhere in the image (**Unknown**).
- **Effects and ballistic aiming.** A short fifteen-frame above-water smoke
  call is removed from the effect-creation path (candidate for the documented
  "disable TA-forced endsmoke", **Supported inference**); the weapon
  effect-record animation is no longer skipped for a movement-class flag; the
  shot-lifetime-expired selector keys on a different unit flag with inverted
  sense, changing the end-of-flight effect; and the ballistic arc solver's
  low-arc branch now accepts angles at or above 45° instead of below it, which
  changes the allowed aim-angle range (**Established** change; its documented
  match is **Unknown**).
- **Not in the executable.** The documented pathfinding-cycle increase, the
  unit/weapon-identifier, effect, model-buffer, sound and unit-limit raises,
  Alt+Tab handling, DirectX-check suppression, no-CD and the reclaim-cursor
  fix are not among these patches; they are supplied by the engine DLL and its
  configuration. One AI search-map default constant in the executable was
  lowered instead, with no established connection to the documented pathfinding
  increase.

### Factory direction and build rotation

**Established — direction is a command-fire weapon ability.** Twenty-six
Escalation factory definitions author `Weapon3=LAB_DIR`; the weapon in
`weaponE/TAESC.tdf` is named "Factory Buildpad Direction", is command-fire
with range 768, one-second reload, no model, zero damage, a beep start sound,
`Paralyzer=1`, `Waterweapon=1` and the non-retail `nottoair=1` target key. The
interface exposes command-fire weapons as the special-ability button, so the
player presses the ability key and clicks a point above or below the lab.

**Established — the aim callback snaps the plate.** The factory scripts define
the third-weapon aim callback (for example the shipyard's) as: when the aim
heading lies strictly between 90° and 270°, turn the buildpad piece instantly
to 180°; otherwise turn it to 0°; then, gated on the unit's own visibility
(recorder ports `71` and `75`, see
[Extended script ports](script-ports.md)), briefly show and animate a
dedicated arrow piece as the direction indicator. The plate therefore has
exactly two valid positions and the snapping is the script's, matching the
mechanism traced for TA Zero's identical ability (same display name, range
800). The release readme describes the same user flow ("Direction" button or
`D`, then attack an area above or below the lab) for non-spinning pad
factories.

**Established — placement rotation.** The engine DLL holds a
`RotateBuildKey` virtual-key field (the load message names `/` as the
default), a `BuildMenuRotationOverlay` button, and a "rotate key discovered"
flag. Two GAFs are resolved from the mod directory: `buildrotate.gaf`, whose
first four frames must be the S, E, N, W facings in order (frame sizes are
validated and implausible ones skipped), and `buildrotateclick.gaf`. The
chosen facing reaches placement at unit initialisation: one hook offsets the
new unit's heading by one 90° step per facing index, and a second hook
captures the reverse mapping (engine heading to facing index). The `Rotations`
unit-definition key restricts allowed facings — facing 0 is always allowed,
any other facing requires its letter, case-insensitively, in the unit type's
string — but no Gold 10.2 unit definition authors `Rotations`, and neither
rotation GAF ships in the package (path census), so both the restriction and
the four-frame overlay are dormant in this release.

## Asset-driven extensions

### Unit-definition keys registered by the engine DLL

**Established — registered keys.** The engine DLL registers these extended
unit-definition keys and the authored content uses them. Values below are the
authored forms; the reader semantics follow.

| Key | Registered as | Authored by | Authored values | Reader semantics |
|---|---|---|---|---|
| `VeterancyThresholds` | string | 197 units | five space-separated kill counts, e.g. `10 20 30 40 50`, `20 40 60 80 100`, `50 100 150 200 250`, `100 200 300 400 500` | parsed into a per-unit-type threshold list; absent or empty seeds `5 10 15 20 25`. The level is the index of the highest threshold not above the kill count; above the last threshold it keeps growing at the last interval's spacing; with one threshold it is `kills / threshold`. This level replaces the retail five-kill step in damage, reload, accuracy, veterancy tests and HUD labels |
| `VeterancyAccuracyBuffRate` | integer | 197 units | `24`, `48`, `120`, `240` (tier-scaled) | below 1 it becomes 0; otherwise `kills / rate` replaces the retail fixed `kills / 6` divisor at the accuracy site |
| `PreviewPieces` | string | 15 units | comma-separated piece names, e.g. `body, turret2, sleeve, barrel` | the nanoframe ghost is composed from the named pieces of the unit's own model; absent, the ghost is the whole model |
| `PreviewPiecesS/E/N/W` | string | none observed | — | directional variants selected by a facing index (0 S, 1 E, 2 N, 3 W) with fallback to `PreviewPieces`; registered but unauthored in this release |
| `PreviewObject3D` | string | none observed | — | loads the ghost model from the named 3DO instead of the unit model; registered but unauthored |
| `PreviewFaceOpponent` | integer | 12 units | `1` | the ghost orientation turns toward a nearby opponent chosen by squared distance instead of the build direction |
| `Rotations` | string | none observed | — | the set of allowed build-facing letters (`S`, `E`, `N`, `W`, case-insensitive); consumed by the executable's build-facing feature; registered but unauthored in this release |
| `canbuild` | no reader found | 13 buildings | `1` | an authored marker: no shipped binary contains the plain key name and retail maps the "has a build list" state to `builder`, not `canbuild`; see the upgrade section |
| `canresurrect` / `Resurrect` | retail key | 7 / 5 units | `1` | the retail resurrection-capable flag; stock content authors both spellings on the Necro ([05](../retail-executable-spec/05-economy-construction-players-and-features.md)) |
| `canrepair` | not registered here | 4 units | `1` | authored; the name appears in no shipped binary's key vocabulary — no reader found |
| `canland` | not registered here | 3 units | `0` | authored only as false on three Core VTOLs; the name appears in no shipped binary's key vocabulary — no reader found |
| `ActivateWhenBuild` | typo | 1 unit | `1` | the retail key is `ActivateWhenBuilt`; this spelling appears in no shipped binary — likely inert, purpose **Unknown** |
| `transportmaxunits` | retail key, alternate casing | 1 unit | `255` | case variant of the retail `TransMaxUnits` family |
| `Sccale` | typo | 1 unit | `1` | the retail key is `Scale`; this spelling appears in no shipped binary — likely inert, purpose **Unknown** |

The distinction between "registered here" and the rest matters: keys in the
second group are string-matched by the patched executable or the retail code
paths it retains, so their processing is not confined to the extension DLL.
Which of those are consumed by the Escalation patch and which by retail code
is recorded below where established, and under **Unknown** otherwise.

**Established — registry mechanics.** A key handle is the key's index with two
tag bits distinguishing integer keys from string keys; a lookup tests the tag
and masks the index. A unit's value is looked up for its unit type first and
then in the registry's global table; out-of-range handles yield zero or an
empty string. Numeric string lists (`VeterancyThresholds`) are parsed with a
whitespace-separated integer reader, so a comma terminates the parse — the
authored values are space-separated — and an empty result falls back to the
tier defaults. Piece-name lists (`PreviewPieces*`) split on spaces, tabs,
commas, semicolons and line breaks, lower-case each token and resolve it as a
piece name of the unit's model. The registered set is exactly the ten keys in
the table above.

**Established — veterancy thresholds and the release notes agree.** The
release notes define tier defaults (T1 `5 10 15 20 25`, T2 `10 20 30 40 50`,
T3 `20 40 60 80 100`, T4 `50 100 150 200 250`) and state the patch does not
change what each level does: capture cost, damage taken, damage dealt, target
tracking, reload time and accuracy. The authored values in this release use
those tier shapes with an additional `100 200 300 400 500` shape for its
largest tier, plus the per-unit `VeterancyAccuracyBuffRate`.

**Established — upgrades are authored content over retail mechanisms, not an
engine extension.** Four authored pieces make one upgrade; all four are
retail content wiring:

1. The parent building holds a build list. Twelve of the thirteen parents
author `Builder=1` with `BMcode=0`; the shield generator authors `Builder=0`.
2. The upgrade unit enters the parent's build list either through
`gamedatE/SIDEDATA.tdf` `[CANBUILD]` `<parent>` subsections
(`canbuildN=<unit>`) or through a `downloadsE` menu record (`UNITMENU=<parent>`,
`UNITNAME=<unit>`). Only the shield generator uses the download record; the
other parents use SIDEDATA.
3. The parent's per-unit build-menu layout (`guiE/<UNIT><page>.gui`) names its
build slots after the units they hold — ordinary builders list their products
this way — and every upgrade parent has exactly one named gadget, its upgrade
unit.
4. The upgrade unit is an ordinary definition: mobile but static-looking (zero
footprint, `Upright=1`), invisible (`Init_cloaked=1`, `Stealth=1`), harmless
(`DamageModifier=0`, `TEDClass=SPECIAL`), with its own script supplying the
effects.

**Established — exact-name resolution and four dead upgrade references.** The
list reader resolves entry names through the catalog's by-name search, which
compares the full case-insensitive unit name and skips a name that is no unit
([02 R-CAT-01 §5](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md)).
Four authored SIDEDATA references in Gold 10.2 name no shipped unit and
resolve to nothing:

| Parent | Authored name | Shipped unit |
|---|---|---|
| Arm Big Bertha | `ARMBRTHA_UPGRADE` | `ARMBRTHA_UPG` |
| Arm Guardian | `ARMGUARD_UPGRADE` | `ARMGUARD_UPG` |
| Arm Experimental Fusion | `ARMSBERTHA_UPG` | none |
| Core Experimental Fusion | `CORNOVA_UPGRADE` | none |

The release notes describe upgrades for Big Bertha and Guardian and both
shipped units exist, so those two are authored wiring defects; the other two
names match no unit at all. Every remaining upgrade reference resolves.

**Established — `canbuild=1` has no reader.** The key appears on exactly the
thirteen buildings above and nothing else in the package. No shipped binary
contains the plain key name (the executable has only the indexed
`canbuild0..N`/`CANBUILD` forms used by the SIDEDATA reader), and the retail
key table maps the "is a builder" flag to `builder`, not `canbuild`. The two
paths above are sufficient without it: twelve parents author `Builder=1`, and
the one without it uses the download record. It is therefore recorded as an
authored marker with no established effect, like `Sccale`.

**Established — the upgrade button appears on the parent's own menu.** The
release notes place the upgrade picture on the upgradeable building's build
menu. The retail build-pages contract resolves the retail mechanism: the
`gamedata/SIDEDATA.tdf` `[CANBUILD]` child section named after a
`builder`-flagged definition fills that definition's build-option list, and
the player's build pages are driven by that list together with the per-builder
GUI windows ([07 R-HUD-03 §6](../retail-executable-spec/07-interface-input-camera-and-front-end.md),
[02 R-CAT-01 §5](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md)),
while the download-menu compile can also append items to it
([02 R-CAT-01 §8](../retail-executable-spec/02-content-vfs-formats-and-data-loading.md)).
The per-unit GUI names a build slot after the upgrade unit, so the twelve
`Builder=1` parents show it; the shield generator authors `Builder=0` and is
wired only through a download record, whose reach depends on a build-list head
that the contract allocates for `builder`-flagged definitions — whether the
Aegis upgrade is reachable is **Unknown**.

**Supported inference — `canrepair` and `canland` have no reader.** Both names
appear in no shipped binary, and the retail unit loader matches authored key
names against its literal key vocabulary (the vocabulary the retail
specification's key table documents), so no reader is reachable in these
binaries. `canland=0` is authored only as false on three Core VTOLs, so even
its intended polarity is unproven. A dynamically constructed key name cannot
be excluded, but none is evidenced; a bounded observation (a unit that authors
each key against a stock install) would settle it.

**Established — two authored spellings match no known key.**
`ActivateWhenBuild` (on the minelayer ship) differs from the retail key
`activatewhenbuilt` by one letter, and `Sccale` (on the Core flagship) differs
from the retail key `Scale`; neither spelling occurs in any inspected
executable or DLL string table. They are treated as authored anomalies with
no established effect rather than as engine keys.

### Weapon and feature keys

**Established — authored and implemented in part.** `nottoair`,
`surfacefire` and `nottounderwater` (Escalation documentation and census) are
owned by [Non-retail weapon target keys](weapon-target-keys.md), which now
records the inspected parser marks, the validator cluster and the modified
acquisition routine; the complete predicate remains unknown. Escalation
authors no `toaironly`. The engine DLL contains the literal names, which is
direct evidence that the engine patch — not stock retail — resolves them.
Escalation feature files also author `HitDensity`/`hitDensity` and
`indestructable` (the retail key is lowercase `hitdensity`,
[fmt tdf](../formats/README.md)); the effect of the capitalised spelling and
of `indestructable` is **Unknown**.

### Script extension surface

**Established — new engine ports.** Escalation unit scripts read engine ports
above retail's range: port `32` (only on Arm and Core commanders) and ports
`69`–`75` (across most units). The reads use both the one-argument and the
five-argument forms, and no extended port is written by scripts. The values
are supplied by the recorder DLL (`eplayx.dll`), which installs a
COB-extensions handler over the executable's port reader; the handler is
always on and is shared by all three recorder builds. The mapping from each
port to its meaning is recorded in [Extended script ports](script-ports.md).

**Established — no new script opcodes.** A scan of all compiled unit scripts
found only the retail instruction set; scripts differentiate behavior through
the extended ports and the port-valued `GET`/`GET5` results.

Copyright © 2008–2026 The Registered One and Wotan (as stated by the bundle);
this document claims no rights.

## Unknown

- **Unknown — the Aegis upgrade's reachability.** The shield generator authors
  `Builder=0` and its upgrade is wired only through a download record; the
  build-list head the record appends to is allocated for `builder`-flagged
  definitions, so whether the upgrade reaches the menu is not established.
  A bounded in-game observation would settle it.
- **Unknown — the repair-rate exploit fix.** The release notes document it, but
  no shipped DLL string, definition key or configuration key describes it and
  no matching arithmetic was isolated; a runtime trace or a retail-vs-Escalation
  repair-contribution comparison would settle it.
- **Unknown — the snap override key's default.** The key code is configurable
  and the changelog names a default, but the shipped default value could not be
  read from the configuration surface.
- **Unknown — the older snap sliders.** The options menu still builds the
  `Mex-Snap Radius` and `Wreck-Snap Radius` sliders (defaults 8 and 26), but no
  reader was found for them; whether any menu path still uses them is not
  established.
- **Unknown — executable patch intents.** The build-menu command's quick
  "addbuild" path now keys on the placement type instead of the command's
  argument with an inverted class test, so the opposite class of unit types
  takes the short path; the removed short smoke call, the ballistic aim-angle
  range change and the removed AI timer's consumer likewise have no settled
  documented counterpart.
- **Unknown — the engine DLL's exact `TAESC.dll` vs. `EDRAW.dll` relationship.**
  The manifest names both with the same base address and size; only
  `TAESC.dll` is shipped. Whether they are the same binary is not established.
