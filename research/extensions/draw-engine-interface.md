# Shared draw-DLL interface

## Evidence scope and sources

This document records the interface and behavior of the community "draw DLL"
family — the renderer/extension DLLs that ProTA, TA: Escalation and TA Zero
each ship — as far as the inspected builds establish it. It does not establish
retail behavior and approves no Nanolathe behavior. Per-package identity and
the rest of each package live in [ProTA](prota-engine.md),
[Escalation](taesc-engine.md) and [TA Zero](ta-zero-engine.md).

**Established — inspected builds.** ProTA 4.8 `tdraw.dll`
(`6b46046a…`), Escalation Gold 10.2.0 `TAESC.dll` (`61783aca…`) and TA Zero
Base `zdraw.dll` (`34993ee8…`), plus the two optional Zero `zdraw.dll`
replacements. All are builds of one lineage; this document states per-build
differences where they exist. These historical observations preceded the
licensed-source review. The lineage's current-generation source is available
under MIT (`src/DDraw` of the TADR repository,
[community patch engine behavior](community-patch-engine.md)). The pinned-source
rendering algorithms and visual acceptance cases are owned by
[Community patch rendering](community-patch-rendering.md). They settle only the
*current* line; the inspected package DLLs are older releases whose source
equivalence remains **Unknown**.

**Evidence provenance.** Much of the behavior below was decoded from these
shipped binaries — a method the [evidence policy](README.md#evidence-policy)
does not permit for third-party patch binaries. The wording is clean-room and
the observations stand as recorded, but no new contract may be closed by this
method: future gaps must be settled from documentation, authored content,
appropriately licensed source (now available for this family), or a bounded
manual observation unless explicitly authorized by the user. The ProTA selection
follow-up and the [ProTA 4.8 shipped megamap](#prota-48-shipped-megamap) audit
below use the user's 22 September 2026 authorization, scoped and recorded in
[ProTA's evidence statement](prota-engine.md#evidence-scope-and-sources).

## Megamap

**Corrected (2026-09-24) — the view is toggled, not held, and has no zoom
steps.** The earlier text here described a view held while `MegamapKey` was
down, activated by a left click, with an eleven-step zoom counter driven by
the wheel and Page Up/Down. The authorized audit of the ProTA 4.8 `tdraw.dll`
disproves that for this build. Its handler toggles the view on release of the
configured key, has no step counter and no Page Up/Down handling, and uses
the wheel only to enter or leave the view; see
[ProTA 4.8 shipped megamap](#prota-48-shipped-megamap). Those earlier
observations were never tied to a traced routine, and they are now disproved
for one of the three builds. The corresponding Escalation `TAESC.dll` and
Zero `zdraw.dll` behaviour is therefore **Unknown**, not established. The
current source (`MegamapControl.cpp`, pinned `dcff5dd`) also toggles on key
release. The built-in default key is Tab. TA Zero's controls page documents
F4 as its megamap key, but the inspected preferences do not set `MegamapKey`,
so the shipped effective binding there is **Unknown**.

**Established — wheel and double-click preferences (names and defaults).**
`WheelZoom` (default on) lets the wheel enter and leave the view;
`WheelMoveMegaMap` (default on) makes leaving by wheel move the camera to the
pointer; `DoubleClickMoveMegamap` (default off) enables the double-click
variant. Their ProTA 4.8 semantics are under the shipped subsection below.

**Established — redraw limiting.** `MegamapFpsLimit` (default 60, `0` =
unlimited) gates the megamap surface's *redraw rate* only, never the game tick.
It exists in `tdraw` and `TAESC`; the 2013 `zdraw` has no such key, and the
Escalation 10.2 INI comments it out as disabled in that release.

**Established — layer toggles.** Nine preference booleans, all default on,
gate what the megamap draws: `DrawBackground`, `DrawMapped`, `DrawProjectile`,
`DrawUnits`, `DrawMegamapRect`, `DrawMegamapBlit`, `DrawSelectAndOrder`,
`DrawMegamapCursor`, `DrawMegamapTAStuff`.

**Established — under-attack flash.** `UnderAttackFlash` (default off)
hides a damaged unit's megamap icon in the phases where the engine's global
blink bit is clear. The cadence is that engine phase, not a DLL timer. For
ProTA 4.8 the reader and the bit are identified below. The bit is retail's
radar blink phase, which toggles once every eight simulation sub-ticks
[01 R-CORE-03].

**Established — icon choice.** Each unit definition carries a numeric category
identifier. The loader walks the unit-type table and loads
`<type-name>.PCX` per type from the game directory, plus the fixed names
`COMMANDER`, `MOBILECOMBAT`, `CONS`, `FACTORY`, `BUILDING`,
`AIRCRAFTCOMBAT`, `AIRCONS`, `NONE`, `UNKNOWN` and `NUKEICON`. At draw time the
icon list is walked in order and the first entry whose category set contains
the unit's category wins; a unit matching nothing draws `UNKNOWN`.

**Established — current-source correction and boundary.** The pinned source
loads category-mask pictures, preceded by available side-specific commander
pictures, rather than one picture for every definition. Its reserved `nothing`
picture is used for admitted contacts outside the identifying LOS helper; it
does not inherently mean no drawing. `UNKNOWN` is for an identified unit with
no matching category. The previous blanket interpretation of `NONE` as hidden
is therefore withdrawn; the corresponding older-build choice is **Unknown**.
See [Megamap icons, contacts and rings](community-patch-rendering.md#megamap-icons-contacts-and-rings)
for source symbols, ordering, recolouring and visibility boundaries.

**Established — icon configuration.** `MegaMapConfig` names an icon-config
INI (ProTA and Escalation ship `Icon/iconcfg.ini`, TA Zero `ZIcon/iconcfg.ini`),
resolved relative to the working directory; `UseDefaultIcon` (default true)
suppresses it. Its `[Option]` keys with defaults are `FillColor` 0,
`TransparentColor` 9, `SelectedColor` 89, `HoverColor` 84, `UseCircleHover`
false; `[Icon]` lines are `name=file.PCX`, where the name is a category name
resolved by the engine's category lookup. The reserved names `nothing`,
`unknow` and `nukeicon` map to `NONE`, `UNKNOWN` and `NUKEICON.PCX`.

**Established — custom load and colour order.** With `UseDefaultIcon=false`,
existing commander-specific PCX files from the configured directory are added
before the `[Icon]` rows. The section rows retain authored order. At draw time
the first non-reserved row whose category membership contains the unit
definition wins; `unknow`, `nothing` and `nukeicon` never participate in that
walk. Failure to match selects `unknow`, while the hidden-unit path selects
`nothing`. `UseDefaultIcon=true` takes the built-in branch before enumerating
the custom section or opening its PCX paths.

For a selected unit, `FillColor` pixels become the player's colour and other
pixels are retained. For an ordinary unselected unit, `SelectedColor` pixels
first become `TransparentColor`, then `FillColor` becomes the player colour.
Hover with `UseCircleHover=false` instead changes `SelectedColor` to
`HoverColor`, followed by the same player-colour replacement. With
`UseCircleHover=true`, hover keeps the unselected image and draws a separate
`HoverColor` circle centred on the icon, with radius equal to the truncated
distance from its centre to a corner. **Confidence: Established** from the MIT
source at pinned commit `dcff5dd`, inspected 2026-09-22.

**Established — current-source ring thresholds.** `MegamapRadarMinimum`,
`MegamapSonarMinimum`, `MegamapRadarJamMinimum`, `MegamapSonarJamMinimum`
and `MegamapAntiNukeMinimum` filter rings using a strict greater-than
comparison. Reading an absent key as −1 preserves constructor defaults:
zero for sensors/jammers and **512 for antinuke**. The earlier apparent
conflict between −1 and 512 compared a reader sentinel with an effective
threshold. The shipped current INI explicitly sets all five to zero.
Radar-jammer drawing actually consumes the radar threshold, not its separately
parsed jammer threshold. Antinuke dashed/solid choice consumes an existing
per-slot indicator; this module does not independently establish that the
indicator means armed. Source:
[Community patch rendering, Megamap icons, contacts and rings](community-patch-rendering.md#megamap-icons-contacts-and-rings).
For ProTA 4.8 the defaults, the radar-jammer threshold quirk and the
indicator are established under
[ProTA 4.8 shipped megamap](#prota-48-shipped-megamap). The Escalation and
Zero builds remain **Unknown** until matched source or manual observations
establish them.

**Established — colours.** `MegamapRadarColor`, `MegamapSonarColor`,
`MegamapRadarJamColor`, `MegamapSonarJamColor`, `MegamapAntinukeColor` and
`MegamapWeapon1/2/3Color`; absent values fall back to the engine's own minimap
colours. Player dots are `Player1DotColors` … `Player10DotColors`, defaults
227, 212, 80, 235, 108, 219, 208, 93, 130, 67. The per-player marker graphics
use `PlayerMarkerPcx`, `PlayerMarkerBackground`, `PerPlayerMarkerWidth` and
`PerPlayerMarkerHeight`.

**Established — full-screen replacement.** `FullScreenMinimap` (default off)
enables the DLL's full-screen megamap replacement, and `MegamapFpsLimit`
applies only while it is active; `MenuWidth`, `MenuHeight` and `MenuResolution`
size the minimap-menu replacement, and `ShareDialogExpand` the expanded share
dialog. `MegamapDither` (default on) and `EnhancedBuiltInMinimap` (default on)
exist only in the Escalation build; the first dithers the megamap and built-in
minimap, the second patches the engine's built-in minimap.

### ProTA 4.8 shipped megamap

**Evidence scope.** This subsection comes from the 24 September 2026 static
audit of the identified ProTA 4.8 `tdraw.dll` (`6b46046a…`), done under the
user's authorization recorded in
[ProTA's evidence statement](prota-engine.md#evidence-scope-and-sources). It
describes that build only. Where the pinned current source
(`MegamapControl.cpp`, `fullscreenminimap.cpp`, `UnitMinimap.cpp`,
`ProjectileMap.cpp`, `MappedMAP.cpp` at `dcff5dd`) differs, the difference is
stated. The current-source contracts in
[Community patch rendering](community-patch-rendering.md#megamap-icons-contacts-and-rings)
are not substituted for this build.

**Established — availability.** The megamap exists only when
`FullScreenMinimap` is true (DLL default false; ProTA 4.8's INI sets it true).
Its input handler runs only while a battle is in progress. `MegamapFpsLimit`
has a DLL default of 60, and ProTA's INI sets 0. INI reads are Windows
private-profile reads in `[Preferences]`, so the INI's `MegamapFPSLimit`
spelling matches the key. Two further keys exist in this build: `MaxIconWidth`
and `MaxIconHeight` (default 22 pixels each, the hover search box), and
`UseSurfaceCursor` (default off).

**Established — entering and leaving.**

| Input | Condition | Effect |
|---|---|---|
| Megamap key pressed | configured key (default Tab, virtual-key 9; editable as "Megamap Key" in the Ctrl+F2 dialog) | consumed; no other effect |
| Megamap key released | same | consumed; leaves the view if it is shown, otherwise enters it |
| Wheel back (negative delta) | `WheelZoom` on, view hidden | enters the view |
| Wheel forward (positive delta) | `WheelZoom` on, view shown | stops camera unit-following; with `WheelMoveMegaMap` on, centres the camera on the pointer's map point; leaves the view |
| Double-click in the map image | `DoubleClickMoveMegamap` on, and the own-unit double-click below did not apply | centres the camera on the clicked map point; leaves the view |

The wheel does not depend on the pointer position. Wheel messages are never
consumed, so the game also receives them. Leaving by the key never moves the
camera. Leaving by wheel with `WheelMoveMegaMap` off keeps the camera where it
was before the view opened, which is what the INI's "previous camera
location" wording describes. A camera move clamps the pointer to the map
image, converts it to a world point (below), centres the game view on it and
clamps the result to the map's scroll limits. Entering plays sound alias
`Options` and leaving plays `Previous`, through the retail alias routine; in
ProTA's `ALLSOUND.TDF` these are `butoptn` and `button1`. Entering clears the
engine's hovered-unit word and any pending box-selection state, and records
the pointer for the cursor overlay. Nothing pauses the simulation.

**Established — what the view shows.** While shown, the renderer suppresses
the ordinary game-view composition. It fills the game-view rectangle with a
redrawn map image, fitted to the playable-map aspect and centred, with any
spare margin greater than two pixels split evenly. Both working surfaces are
colour-filled with palette index 95 when they are created or restored, and
that fill shows in the margins; this is the "grey background" of the 4.6
note. Each redraw composes, in order, each layer behind its toggle:

1. the terrain picture (`DrawBackground`);
2. fog (`DrawMapped`), using the same four-way mapping/LOS table as the
   current source's `NowDrawMapped`: black where unmapped and the engine's
   grey table where current LOS is absent, with the same sea-level row offset.
   This build has **no** per-game-tick cache; a mutex guards the image
   instead. If the fog step fails, projectiles and units are skipped for that
   redraw;
3. projectiles (`DrawProjectile`);
4. unit icons (`DrawUnits`);
5. the composite into the view (`DrawMegamapRect`), the blit
   (`DrawMegamapBlit`), the renderer's interface and selection/order overlay
   (`DrawMegamapTAStuff`, `DrawSelectAndOrder`; not re-traced here) and the
   cursor (`DrawMegamapCursor`), drawn when the pointer lies in the drawable
   area.

The image is redrawn when `MegamapFpsLimit` is 0, or when the time since the
last redraw is strictly more than `1000 / limit` milliseconds by the system
tick. Between redraws the previous image is reused. The limit never affects
the simulation. There is **no feature layer**: nothing in this build draws
features into the megamap, unlike the current source's `MEGAMAP_FEATURES`
module.

**Established — map scale.** Icons, projectiles, rings and pointer
conversion share one extent: `(Width − 1) × 16` by `(Height − 4) × 16` world
units, from the TNT header's width and height in 16-pixel units [fmt tnt].
This is also the aspect used to fit the terrain picture. A world point `(x, z)`
with height `y` is drawn at `trunc(x × imageW / extentW)`,
`trunc((z − y/2) × imageH / extentH)`, with floating-point scale factors. The
reverse conversion divides pointer coordinates by the same factors and
truncates. It does **not** correct for the half-height shear: the world point
gets the terrain height at that spot, or sea level when no terrain height is
available. The current source instead scales icons and projectiles by the
play-area extents and searches for a sheared height.

**Established — unit icons.** The icons are exactly the units in the engine's
HOT radar list from the retail minimap contacts pass [03 §3.9] that have a
nonzero definition index. The megamap therefore shows the same admitted
contacts as the minimap. Picture choice follows the source's order:

- A contact the engine's LOS helper does not identify uses the `nothing`
  picture.
- An identified unit uses the first `[Icon]` category row containing its
  definition, or `unknow` when no row matches.
- Selected art wins over hover art, and hover art is used only when
  `UseCircleHover` is off.
- Pictures are recoloured per player logo colour (below).
- If no icon pictures loaded at all, the engine's own minimap blip art for
  the owner's logo colour is used instead.

An icon is placed with its centre at the unit position less half its
footprint in each horizontal axis, and less half its height in the vertical
screen axis. The row pitch used for clipping is rounded down to a multiple of
four. Clipping at the left and top edges moves the destination start to zero
without skipping the matching source pixels, so edge icons are drawn shifted
rather than cut; this is a quirk of the shipped build.

**Established — under-attack flash.** With `UnderAttackFlash` on (ProTA 4.8
sets it on), a unit whose per-unit blink-suppression byte is nonzero has its
icon pixels skipped whenever the retail blink phase bit is clear. This is the
same byte and bit the retail minimap uses for contact blips [03 §3.9]. The
phase toggles once every eight simulation sub-ticks, so the icon shows for 8
sub-ticks and hides for 8 [01 R-CORE-03]. Hover circles and rings are still
drawn while the icon is hidden. This settles the cadence for this build.

**Established — hover.** While the pointer is inside the map image and no box
drag is in progress, the renderer looks for a hovered unit. It converts the
pointer to a world point and walks the HOT list. It picks the first unit
whose reference point lies strictly inside a box around that point (half of
`MaxIconWidth`/`MaxIconHeight` converted to world units), and then strictly
inside a box sized to the unit's current picture. That unit becomes the
engine's hovered-unit word; if none qualifies, the word is set to zero. The
reference point is the unit position **plus** half its footprint, while the
icon is drawn at position **minus** half its footprint. The hit area is
therefore offset from the drawn icon by one footprint; the current source's
pixel-exact test does not have this offset. With `UseCircleHover` on, the
hovered unit gets a `HoverColor` circle whose radius is the truncated
distance from the icon centre to its corner.

**Established — rings.** Each ring is centred on the icon. Its radius is
`distance × rowPitch / extentW` in integer division, where the row pitch is
the four-aligned image width. "Allied" below is the renderer's ally test
against the viewing (LOS) player: the unit's owner is that player or is in
that player's alliance row. The test answers yes for every unit while a
replay plays or the local slot is a watcher, the same shortcut the current
source documents for `IsPlayerAllyUnit`.

- *Sensor and jammer rings* are drawn for a **selected** unit that is allied
  with the viewing player. The unit's activation state is not tested, unlike
  the retail minimap's selected-unit circle gate [03 §3.9]. The radar ring
  needs `radardistance > MegamapRadarMinimum`, and the sonar ring needs
  `sonardistance > MegamapSonarMinimum`. The radar-jammer ring needs
  `radardistancejam > MegamapRadarMinimum`: it compares against the radar
  threshold, and `MegamapRadarJamMinimum` is read but never used. The
  sonar-jammer ring needs `sonardistancejam > MegamapSonarJamMinimum`. All
  comparisons are strict.
- *Interceptor rings* are drawn for the same selected allied unit when its
  definition has `antiweapons`. For each slot in order 1, 2, 3 whose weapon
  has `interceptor` and `coverage > MegamapAntiNukeMinimum`, the radius is
  `(coverage − 512) × rowPitch / extentW`. The 512 bias is retail's minimap
  ring rule [03 §3.9]; the current source dropped it. The ring is solid when
  the slot's interceptor flag byte is zero, and otherwise dashed with 32
  segments seeded by the blink byte, as on the retail minimap.
- *Weapon rings* are drawn for the hovered unit or the engine's
  show-range unit, each only when allied with the viewing player, and only
  while Shift is physically down (an asynchronous key-state read). They are
  drawn for slots 3, 2, 1 whose enabled bit is set and whose authored `range`
  is nonzero, using that raw range. There is no ballistic flat-range limit
  and no message-derived Shift state; both are current-source changes.

Thresholds default to 0, 0, 0, 0 and 512 (radar, sonar, sonar-jam, radar-jam,
antinuke) when a key is absent (read as −1). ProTA 4.8's INI sets all five
to 0, so its interceptor rings need only positive coverage. Ring colours
default to engine minimap colour-table entries: weapon 1 uses entry 6,
radar/sonar entry 10, both jammers entry 12 and antinuke entry 15, while
weapons 2 and 3 default to palette index 1. Explicit `Megamap*Color` keys
override these.

**Established — projectiles.** Every projectile in the pool is visited. It is
admitted if it lies within the viewing player's LOS grid (after dividing its
x and `z − y/2` by 32) and passes the same allied / current-LOS / mapped test
as the current source's `IsPosInPlayerLos`. A weapon whose flags include
`twophase`, `cruise` and `targetable` together draws the `nukeicon` picture
in the owner's logo colour. If that picture is missing, the engine's contact
nuke marker art for the owner's colour is drawn instead. Every other admitted
projectile draws a 2×2 block, clipped, in the engine's projectile colour
index. The current source instead requires `cruise`, `targetable` and
`stockpile`, adds every `interceptor` weapon, and hides zero-damage map
weapons. None of those rules is in this build.

**Established — input while shown.** Mouse messages are handled only while
the view is shown. A message is consumed when the pointer is inside the game
view and not over the top active interface control. Clicks outside the game
view reach the game as usual.

- *Box selection.* A box can start only while no order is prepared (the
  neutral prepared order). A left press inside the map image starts it, and
  the box is clamped to the image when the pointer leaves it. The release
  selects only when both screen extents are at least 9 pixels. Candidates are
  the local player's own completed, selectable units with an owner link and
  a reference point `(x, z − y/2)` strictly inside the converted world
  rectangle. Without Shift the selection is replaced, and units outside the
  box are deselected; with Shift each unit inside the box is toggled. The
  W/B/Y drag filters of the ordinary game view
  ([ProTA shipped selection audit](prota-engine.md#shipped-selection-and-hotkey-audit))
  are not applied here.
- *Left release with a prepared order.* If the pointer is inside the map
  image, the selection is nonempty and a non-neutral order is prepared, the
  release is passed to the retail world-click handler [07 §9], with the Shift
  flag, and the megamap's own click logic below does not run. That handler
  issues the order at the engine's pointer world point, which the megamap
  keeps updated from the pointer. A build order is issued only when the
  engine's site-valid bit is set. **Unknown — build placement from the
  megamap:** entering the view clears that bit's byte, and the megamap never
  runs the placement validator itself. Whether the engine's pointer update
  revalidates the site while the game view is suppressed decides whether a
  build can be placed here or always plays `notoktobuild`. A manual 4.8
  observation of a build click on the megamap would settle it.
- *Other left releases* (not a box, not at the last double-click position).
  With the select cursor showing: an own hovered unit is selected; without
  Shift the old selection is cleared first, and with Shift the unit's
  membership is toggled. Only selectable, completed units change. With any
  other cursor and a nonempty selection: in the right-click interface the
  click clears the selection; in the left-click interface the prepared
  order type, still neutral at this point, is sent to the pointer's world
  point with the Shift queue flag.
- *Right release.* A prepared non-neutral order is cancelled back to
  neutral. Otherwise, in the left-click interface, a nonempty selection is
  cleared. In the right-click interface, a nonempty selection is sent the
  prepared order type (neutral, or Guard when the select cursor shows) at
  the pointer's world point. **Unknown:** which concrete order the engine's
  order sender makes of a neutral-typed send here; this audit did not trace
  that sender. Right press and right double-click do nothing.
- *Double-click.* With the selection extension's `DoubleClick` switch on and
  an own unit hovered, the selection becomes that unit, and the retail
  Ctrl+Z same-definition selection [07 R-CAM-01 §2] extends it across the
  whole map; the view stays open. For a foreign hovered unit the interface is
  refreshed. In the left-click interface the handler then stops; in the
  right-click interface it falls through to `DoubleClickMoveMegamap`, as
  does a double-click with no hovered unit.

**Established — player colours and markers.** `Player1DotColors` …
`Player10DotColors` (DLL defaults 227, 212, 80, 235, 108, 219, 208, 93, 130,
67; ProTA 4.8 sets 227, 249, 18, 250, 67, 149, 208, 117, 210, 34) fill a
single ten-entry table. The table is indexed by a player's **logo colour**,
not by player slot, so a `+logo` change moves the colour. It is read by:

- the megamap icon recolouring, where `FillColor` becomes this colour;
- the whiteboard's freehand lines and markers;
- the allied resource bars;
- the shared-map-position rectangles.

The retail minimap's own contact blips still come from its `radlogo` art
[03 §3.9], and no reader of the table was found on that path. ProTA's
"improved minimap dot colours" therefore come from its content, not these
keys. `PlayerMarkerPcx` names the whiteboard dot-marker strip: ten
`PerPlayerMarkerWidth` × `PerPlayerMarkerHeight` (default 10 × 10) frames,
with `PlayerMarkerBackground` (default 9) as the transparent index. The value
is cut at the first `;`, resolved against the working directory, and used
only if that file exists; otherwise the built-in drawn markers are used. It
does not affect megamap unit icons.

## Whiteboard

**Established — key and modes.** `WhiteboardKey`, default `\`, is a hold key:
the whiteboard's modes are live only while it is down.

**Established — interactions.** Left-drag on empty space paints a freehand
stroke; left-click on one of your own text markers grabs it and makes it follow
the mouse; right-drag while grabbing erases everything in a small box at the
cursor with a delete animation; middle-click erases at the cursor;
middle-click release on empty space adds an empty text marker; right-click on
your own marker opens its edit box, where Enter commits, Escape cancels, and
Backspace edits. Holding the whiteboard key with Ctrl moves the camera to the
newest marker's stored map position, clamped to the map.

**Established — distribution.** Markers arrive through the ordinary game chat
stream as a control-prefixed message (a leading control byte, then position,
colour/player index and text); the DLL parses it, creates the marker and echoes
the chat line. Marker visibility therefore follows chat reach — allies in team
games. The local creation paths create markers directly; no packet builder for
announcing a locally created marker was found, so how a marker reaches peers is
**Unknown** (a two-client test would settle it). Text markers announce as
`*<player>: <text>` and dot markers as `*<player> added a new marker`.

**Supported inference — markers do not persist.** Nothing serialises the marker
store, which is created once at engine init.

## Selection

**Established — ProTA 4.8 selection, authorized follow-up audit.** The
shipped draw DLL contains the selection algorithms themselves, not merely
category masks: both-button double-click selection, Ctrl+B/F idle cycling,
Ctrl+S on-screen armed selection, and W/B/Y filtering are traced in
[ProTA's shipped selection audit](prota-engine.md#shipped-selection-and-hotkey-audit).
That versioned contract replaces the earlier assertion that those paths were
only in the executable. It also corrects the constructor/factory fallback:
empty authored categories derive builders excluding airbases and the
commander/decoy mask, not the `CTRL_W` set. Double-click defaults on, uses the
prior selection's definition identities, and admits either mouse button.

**Established — version boundary.** The current-source contracts in
[Community patch engine behavior §4.2](community-patch-engine.md#42-data-driven-switches-outside-totalaini)
remain separate. In particular, the historical ProTA cycles stop at live unit
count and omit slot zero, while the current source scans the full slot block;
the historical menu refresh also preserves a prepared order. The follow-up
user authorization applies to the identified ProTA artifacts. Corresponding
older Escalation and Zero algorithms have not been reverified by this audit.

## Preference keys

**The inventory is owned elsewhere.** The complete preference-file key
inventory and per-key scope live in
[community patch engine behavior](community-patch-engine.md) §4.1, and the
control options (including `ClickSnapOverrideKey`, the autoclick key,
`WhiteboardKey` and `MegamapKey`) in its §4.2; this section keeps only what
differs across the inspected builds. Two placements worth keeping straight:
`UseDefaultIcon` is an `[Option]` key of the icon-config file named by
`MegaMapConfig` (above), not a preference-file key, and
`DisableDeInterlaceMovie` is a registry-backed host toggle.

**Established — per-build differences.** Keys read by some builds only:

| Key | ProTA `tdraw` | Escalation `TAESC` | Zero `zdraw` |
|---|---|---|---|
| `MegamapFpsLimit` | yes | yes (disabled in 10.2's INI) | — |
| `MegamapDither` | — | yes | — |
| `EnhancedBuiltInMinimap` | — | yes | — |
| `NanoframePreviewFill` | — | yes | — |
| `UnitType` / `X_CompositeBuf` / `Y_CompositeBuf` | yes | yes | yes |
| `WeaponType` / `MultiGameWeapon` | — (weapon tables pinned by patch code) | — (same) | yes |

## Unknown

- **Settled for ProTA 4.8 — the megamap flash cadence.** The flash reads the
  retail radar blink phase, which toggles every eight sub-ticks
  [01 R-CORE-03]; see [ProTA 4.8 shipped megamap](#prota-48-shipped-megamap).
  The Escalation and Zero readers are not reverified.
- **Unknown — Escalation/Zero megamap entry and zoom.** The held-key and
  eleven-step claims were disproved for ProTA 4.8 (above). The other two
  historical builds need their own evidence.
- **Unknown — ProTA 4.8 megamap build placement and neutral right-click
  orders.** See the two Unknowns in the input list of
  [ProTA 4.8 shipped megamap](#prota-48-shipped-megamap).
- **Unknown — local marker distribution.** How a marker created locally is
  announced to peers.
- **Unknown — older Escalation/Zero `CTRL+S` and cycle rules.** ProTA 4.8 is
  now traced separately above. Neither its behavior nor current source proves
  equivalence to the other historical package DLLs.
- **Unknown — older Escalation/Zero drag-filter fallback semantics.** The
  ProTA correction above is version-specific; the other historical builds need
  equivalent caller and field checks.
- **Unknown — TA Zero's effective megamap key.** Its controls page documents
  F4; the DLL's built-in default is Tab and the inspected preferences do not
  override it.
