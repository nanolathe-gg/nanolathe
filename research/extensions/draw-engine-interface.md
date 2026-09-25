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
[ProTA's evidence statement](prota-engine.md#evidence-scope-and-sources). A
same-day follow-up under the same authorization added the terrain picture,
the orders sent, build placement, the ring-colour keys, the overlay, the
projectile gate and the dot-colour readers. Where the DLL calls into the
unchanged retail executable, that side was read through the retail analysis,
and the retail contracts are cited. It describes that build only. Where the
pinned current source (`MegamapControl.cpp`, `fullscreenminimap.cpp`,
`UnitMinimap.cpp`, `ProjectileMap.cpp`, `MappedMAP.cpp` at `dcff5dd`)
differs, the difference is stated. The current-source contracts in
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

1. the terrain picture (`DrawBackground`; see "Terrain picture" below);
2. fog (`DrawMapped`), using the same four-way mapping/LOS table as the
   current source's `NowDrawMapped`: black where unmapped and the engine's
   grey table where current LOS is absent, with the same sea-level row offset.
   This build has **no** per-game-tick cache; a mutex guards the image
   instead. If the fog step fails, projectiles and units are skipped for that
   redraw;
3. projectiles (`DrawProjectile`);
4. unit icons (`DrawUnits`);
5. the composite into the view (`DrawMegamapRect`), the blit
   (`DrawMegamapBlit`), the renderer's interface pass
   (`DrawMegamapTAStuff`), the selection and order overlay
   (`DrawSelectAndOrder`; see "Selection and order overlay" below) and the
   cursor (`DrawMegamapCursor`), drawn when the pointer lies in the drawable
   area.

The image is redrawn when `MegamapFpsLimit` is 0, or when the time since the
last redraw is strictly more than `1000 / limit` milliseconds by the system
tick. Between redraws the previous image is reused. The blit, both overlay
passes and the cursor are outside that gate: they are drawn on every
presented frame over the image, and the blit covers the whole game-view
rectangle, margins included. The limit never affects the simulation. There
is **no feature layer**: nothing in this build draws features into the
megamap, unlike the current source's `MEGAMAP_FEATURES` module.

**Established — terrain picture.** The picture is built once per battle,
when the battle's terrain file has been loaded at battle entry, at the size
of the game-view rectangle at that moment. Nothing rebuilds it during the
battle: feature changes, terrain changes and view changes do not reach it.
It is a copy of the map's own tile art, point-sampled, holding the tiles'
palette indices unchanged:

- The covered area is the retail play area [fmt tnt]: `(Width/2 − 1)`
  tile columns by `(Height/2 − 4)` tile rows of 32-pixel tiles, that is
  `Width × 16 − 32` by `Height × 16 − 128` map pixels. The last tile column
  and the last four tile rows are excluded.
- The picture is fitted to that area's aspect in single-precision floats,
  in tile units. Let the game view be `w × h`, `cols = Width/2 − 1` and
  `rows = Height/2 − 4`. If `cols` exceeds `(w / h) × rows`, the width stays
  `w` and the height becomes `trunc(w / cols × rows)`. If it is less, the
  height stays `h` and the width becomes `trunc(h / rows × cols)`. If the two
  are exactly equal, the picture becomes a square of the smaller of `w` and
  `h`, a quirk of the shipped build.
- The source step in each axis is the covered pixel extent divided by the
  picture dimension, and is never less than one source pixel. Output column
  `c` samples source column `trunc(c × stepX)`, and output row `r` samples
  source row `trunc(r × stepY)`; both start at zero. Each sample reads the
  tile index from the map's tile grid at `(column / 32, row / 32)` and copies
  the byte at `(column mod 32, row mod 32)` of that tile's 32×32 graphic.
- There is no averaging, no palette reduction or colour matching, and no
  dithering. The picture is drawn through the battle palette like the game
  view. The build allocates a 256-entry palette record alongside the picture,
  but nothing reads it. `MegamapDither` does not exist in this build.
- When the fitted picture is larger than the covered area in an axis, the
  step stays at one pixel. The picture then continues past the covered area.
  It shows the excluded edge tiles first. Beyond the map width, the
  tile-grid index runs on into the next tile row. Beyond the tile grid's last
  row, the reads leave the grid entirely, and this audit does not define
  their content.

The current source's area-averaged, OKLab-matched and dithered reduction
([Community patch rendering](community-patch-rendering.md#megamap-images-palette-reduction-and-composition))
is therefore a later change, not this build's picture.

**Established — map scale.** Icons, projectiles, rings, the overlay and
pointer conversion share one extent: `(Width − 1) × 16` by `(Height − 4) × 16`
world units, from the TNT header's width and height in 16-pixel units
[fmt tnt]. **Corrected (2026-09-24):** this is *not* the terrain picture's
extent. The picture covers and is fitted to `(Width − 2) × 16` by
`(Height − 8) × 16` (above), while everything drawn over it is scaled to the
larger extent. On one image the overlays are therefore compressed towards the
top-left relative to the terrain: at the far edge the offset is 16 map
pixels horizontally and 64 vertically, scaled to the image. A world point `(x, z)`
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
- *Weapon rings* are drawn for the hovered unit, and for the unit whose
  command page is open (the command-page subject of [07 R-P0-11 §3]; the
  earlier "show-range unit" wording named the same engine word). Each must
  be allied with the viewing player, and Shift must be physically down (an
  asynchronous key-state read). They are
  drawn for slots 3, 2, 1 whose enabled bit is set and whose authored `range`
  is nonzero, using that raw range. There is no ballistic flat-range limit
  and no message-derived Shift state; both are current-source changes.

Thresholds default to 0, 0, 0, 0 and 512 (radar, sonar, sonar-jam, radar-jam,
antinuke) when a key is absent (read as −1). ProTA 4.8's INI sets all five
to 0, so its interceptor rings need only positive coverage.

**Established — `Megamap*Color` keys.** Eight integer keys in `[Preferences]`
colour the rings, one each:

| Key | Ring | Default |
|---|---|---|
| `MegamapWeapon1Color` | weapon slot 1 | colour-map entry 6 |
| `MegamapWeapon2Color` | weapon slot 2 | palette index 1 |
| `MegamapWeapon3Color` | weapon slot 3 | palette index 1 |
| `MegamapRadarColor` | radar | colour-map entry 10 |
| `MegamapSonarColor` | sonar | colour-map entry 10 |
| `MegamapRadarJamColor` | radar jammer | colour-map entry 12 |
| `MegamapSonarJamColor` | sonar jammer | colour-map entry 12 |
| `MegamapAntinukeColor` | interceptor coverage | colour-map entry 15 |

They are read at every battle entry, when the megamap is set up, through the
Windows private-profile integer reader, which ignores key case. A key that
is absent reads −1 and keeps the default. Any other value, including other
negative values, replaces the default and is passed to the circle primitive
as its palette index; this build does not range-check it. A colour-map
default is the physical index that the runtime logical-to-physical map
([03 R-MM-01 §2]) gives for that entry at battle entry. The two literal
defaults for weapon slots 2 and 3 are raw palette index 1, not a colour-map
entry. ProTA 4.8's `ProTA.ini` sets none of the eight keys, so its rings use
the defaults. No other megamap layer reads these keys: the selection box and
the order overlay use fixed colour-map entries (below).

**Established — projectiles.** Every projectile in the pool is visited. Its
cell is `x / 32` and `(z − y/2) / 32` in whole world units, each a signed
division truncated toward zero (`y/2` is truncated first). A negative cell
coordinate becomes a huge unsigned value and is rejected. A cell is also
rejected when either coordinate is **strictly greater** than the viewing
player's LOS-grid width or height, so a coordinate equal to the width or
height passes. An in-range projectile is then admitted when:

1. its owner is the viewing (LOS) player, or the viewing player's alliance
   row marks the owner; otherwise
2. when bit 1 of the visibility mode word is set (current-sight tracking on,
   [03 R-VIS-01 §1]), the viewing player's current-sight byte at the cell is
   nonzero; otherwise
3. when bit 0 is set (Unmapped), it is admitted unconditionally; otherwise
4. (Mapped with Permanent sight) the viewing player's bit is set in the
   mapping word at the cell. That grid is all ones in this mode, so in
   practice every projectile passes.

Thus with current sight on, a foreign projectile shows only inside current
sight. With Permanent sight it always shows, including over unexplored
ground in the Unmapped case, where the retail one-point coverage gate of
[03 R-FX-01 §3] would test the mapping word instead. The flag names
`NOMAPPING` and `Permanent` in the current source's `IsPosInPlayerLos` are
bit 0 and bit 1 of this word, and that function has the same four steps. A weapon whose flags include
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
  engine's site-valid bit is set; see "Build placement from the megamap"
  below.
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
  the pointer's world point; "What a megamap send becomes" below gives the
  resulting orders. Right press and right double-click do nothing.
- *Double-click.* With the selection extension's `DoubleClick` switch on and
  an own unit hovered, the selection becomes that unit, and the retail
  Ctrl+Z same-definition selection [07 R-CAM-01 §2] extends it across the
  whole map; the view stays open. For a foreign hovered unit the interface is
  refreshed. In the left-click interface the handler then stops; in the
  right-click interface it falls through to `DoubleClickMoveMegamap`, as
  does a double-click with no hovered unit.

**Established — the engine's pointer update keeps running.** Retail runs
its pointer update once per host frame from its own pointer record
[07 R-CAM-01 §1]. It classifies that record against the minimap and view
rectangles and resolves the pointer world point. Then, with the pointer in
the view and a build prepared, it runs the placement preview. Otherwise it
picks the hovered unit and chooses the cursor shape [07 §8]. The megamap
does not stop this update. It redirects three steps while the last mouse
message it handled lay inside the game view and off the top control, which
is the same test that makes it consume messages:

- the world-point resolver returns without writing, so the engine keeps
  the megamap's pointer world point. The megamap writes that point on every
  pointer move inside the image, with the conversion under "Map scale";
- the hover pick returns the current hovered-unit word, which is the
  megamap's own hover result;
- the feature lookup at the pointer cell reports no feature.

The classification still derives the placement cell from that world point,
and still sets the in-view and in-minimap bits from retail's own pointer
record. With the in-view bit set and a build prepared, the placement
preview therefore runs at the megamap point, snapped by the footprint as
usual [07 §9]. Otherwise, with either bit set, the cursor chooser runs over
the megamap's hovered unit. With neither bit set, the cursor shape is forced
to normal and no preview runs.

**Supported inference — what retail's pointer record holds.** The record
is filled only from mouse messages the game's own window procedure receives
[07 §2]. While the view is shown, the draw DLL consumes every message inside
the game view before the game sees it. The record should therefore hold the
last position the game itself saw. That is the pointer position when the
view opened, until the pointer leaves the game view. After that, it is the
last position outside the game view, for example over the build menu. The
in-view bit then stays clear until the view is closed, and it is also clear
from the start if the view was opened with the pointer outside the game
view. No other writer of the record was found in the draw DLL. The missing
evidence is a runtime confirmation: see the build-placement test below.

**Established — what a megamap send becomes.** An order the megamap sends
itself goes through the retail selection broadcast [04 R-STANCE-01 §5]. The
prepared order type is the numeric command code, the hovered-unit word the
target, the engine's pointer world point the position, and Shift the queue
flag. Because of the redirection above, the target and point are the
megamap's own. The broadcast's own rules apply unchanged, including its
command-target exclusion and the nearby-offset spreading of positioned move
and patrol results.

- A **neutral** send is command code 1, the contextual order. Each selected
  unit of the local player resolves it separately through the
  `Interface Type` variant of [04 R-ORD-02 §1] ("Code 1 — contextual"). This
  is the same resolution an ordinary game-view click with an idle latch gets.
  In the left-click interface, a hostile hovered unit becomes an attack for
  units that can attack. A friendly unfinished unit in nanolathe reach
  becomes assistance. A reclaimable feature at the point, on ground the
  viewer has mapped, becomes resurrect or reclaim for units able to do it,
  and open ground becomes a move. Units that resolve nothing get no order.
- **Guard** (right-click interface, select cursor over the hovered unit) is
  command code 7. Each selected unit with `canguard` gets the ground or air
  follow order on the hovered unit; the others get nothing. The contextual
  assist, repair, pickup or landing that retail's own right click could
  choose there is never produced.
- After the Guard send the prepared order **stays Guard**. The megamap's
  post-send tidy-up performs retail's latch-to-idle side effects: it clears
  the Shift persistence bit and resets the Stop radio group
  [07 R-HUD-04 §3]. It then restores the latch. The next right release
  cancels the Guard. A left release in the image with a selection passes
  it to the world-click handler, like any prepared order.
- The select-cursor tests in both interfaces read the engine's cursor shape
  from retail's chooser, not a megamap test. When retail's pointer record
  lies outside both the view and the minimap, the shape is forced to normal.
  A click then does not select an own unit: the left-click interface sends
  the neutral order instead, and the right-click interface never sends
  Guard. When that state occurs rests on the pointer-record inference
  above.

**Build placement from the megamap.** **Established:** the megamap never
runs the placement validator. Entering clears the whole pointer-flags byte,
including the site-valid bit [07 R-CAM-01 §14]. After that, only two paths
write the bit: retail's per-frame preview at the megamap point, which needs
the in-view bit above, and the click-snap handler below. A left release with
a build prepared and a nonempty selection goes to the world-click handler
[07 §9], which reads the bit as last written:

- set: each selected builder gets the mobile-build order at the snapped
  site and `oktobuild` plays. The placement stays prepared with Shift, and
  otherwise returns to neutral;
- clear: `notoktobuild` plays and nothing else happens. No order is issued,
  and the placement stays prepared.

The ghost in the overlay (below) shows the same bit.

**Supported inference:** from the pointer-record inference, a megamap build
works when the pointer has stayed inside the game view since the view
opened, for example with the building chosen by hotkey. Once the pointer has
crossed the build menu or any other area outside the game view, every
megamap build click plays `notoktobuild` until the view is closed. A manual
4.8 test would settle it. Open the megamap with the pointer over the
battlefield, choose a building by hotkey and click a legal site; the
inference predicts it builds. Then choose a building from the build menu
and click a legal site; the inference predicts `notoktobuild`. In the second
state, a left click on an own unit is predicted not to select it.

**Unknown — click snapping on the megamap.** The mex- and wreck-snap handler
(`MexSnapRadius_`, `WreckSnapRadius_` and `ClickSnapOverrideKey`) sees mouse
messages before the megamap. Its build-snapping branch does not check
whether the view is shown. When it holds a snapped site, it takes the left
press itself and runs the preview and the world-click handler at that site.
A snapped build would therefore be validated whatever the pointer record
holds. Whether its search finds a site from the megamap's point was not
traced. A 4.8 observation with a nonzero Mex-Snap radius would settle it.

**Established — selection and order overlay.** With `DrawSelectAndOrder`
on, the overlay is drawn on every presented frame. Positions are image
coordinates, offset into the view, and use the "Map scale" extent. Outlines
go through retail's one-pixel rectangle-outline primitive [03 R-MM-01 §1],
and sprites through its GAF frame blitter. Colour-map entries are resolved
through the logical-to-physical map; `Megamap*Color` does not apply here.

1. **Selection box.** While a box drag is in progress and both screen
   extents exceed 8 pixels, one outline joins the press point and the
   current pointer (clamped to the image) in **colour-map entry 15**. There
   is only this single frame, with no inner frame. Retail's world drag
   rectangle, by contrast, adds an inner frame in entry 0 [03 R-SEL-02A].
2. **Placement ghost.** With a build prepared and the pointer in the image,
   the outline has the armed definition's footprint of `footX × 16` by
   `footZ × 16` world units. It is centred on the projection of the pointer
   world point with the half-height shear, so it sits above the pointer by
   half the terrain height there. It is moved back inside the image when it
   would cross an edge. The colour is entry 10 when the site-valid bit is
   set and entry 4 when it is clear. While the extension's row-building mode
   is active, the overlay instead draws each queued row position's
   footprint, each checked by the retail preview. Valid positions use raw
   palette index 240, or 234 under an engine code-byte condition this audit
   did not identify, and invalid ones 214.
3. **Queued orders**, only while Shift is physically held (an asynchronous
   key-state read, as in retail's overlay [07 R-P0-11 §3]). The walk covers
   the **local** player's units in slot order that are in play and not
   death-marked. *Focus* units are the hovered unit, the camera-tracked unit
   and the command-page subject. Another unit is drawn only if it is
   selected, or if a *builder context* exists: some focus unit is allied
   with the local player and its definition has a build list. For each
   node of the unit's order list, starting from the unit's position as the
   running anchor, the order descriptor's draw-mask bits select:
   - bit 1: the build-site outline of the order's definition at the order
     position, with the ghost's geometry. It is entry 10 when the unit is
     selected and entry 1 otherwise. A focus unit also gets a dash chain from
     the running anchor. The anchor becomes the site;
   - bit 2: the order anchor is resolved. For an order with a target unit,
     that is the target's `(x, z − y/2)`, refreshed while retail's unit
     visibility test passes for the local player and cached otherwise. For
     other orders it is the order position. Focus and selected units draw
     the order icon there unless an icon was already drawn at exactly that
     point this frame. A focus unit then gets a dash chain, and the anchor
     advances;
   - bit 8: the same icon, under the same once-per-point rule, with no chain;
   - bit 16: for a cloaked focus unit, a circle in entry 15 whose radius is
     the definition's minimum cloak distance times the horizontal scale,
     truncated. It is drawn at every such node, not once per unit.

   Builder-context units therefore draw only build-site outlines, and
   selected units that are not focus units draw outlines and icons but no
   chains. The icon is the cursor-art entry named by the descriptor's icon
   byte (1 to 20), at frame `(tick / (2 × ticksPerFrame)) mod frames`. A dash
   chain places `pathicon` sprites along the segment from the running anchor
   to the new anchor, both taken as `(x, z − y/2)`:
   - the spacing is the world length of a 20 × 20 image-pixel diagonal,
     `√(trunc(20 / scaleX)² + trunc(20 / scaleY)²)`, and nothing is drawn
     unless the segment is longer than one spacing;
   - with `age` the game tick minus the order's birth tick (at least zero),
     the first sprite sits `(age mod 20) × spacing / 20` along the segment,
     at frame `(age / ticksPerFrame) mod frames`, and each later sprite takes
     the next frame;
   - the segment's direction and length use endpoints clamped to the map
     extent, while sprite positions start from the unclamped anchor.

   Compared with retail's overlay [07 R-P0-11 §3], the megamap draws no range
   rings other than the cloak circle, no bit-4 target circles, no kamikaze
   pulse and no build-site sweep. Its chain spacing is fixed in image pixels
   rather than retail's 48 world units, and its phase wraps every 20 ticks
   rather than 30.

The megamap draws no other selection mark: selection shows only through the
icons' selected art.

**Established — player colours and markers.** `Player1DotColors` …
`Player10DotColors` (DLL defaults 227, 212, 80, 235, 108, 219, 208, 93, 130,
67; ProTA 4.8 sets 227, 249, 18, 250, 67, 149, 208, 117, 210, 34) fill a
single ten-entry table. The table is indexed by a player's **logo colour**,
not by player slot, so a `+logo` change moves the colour. It is read by:

- the megamap icon recolouring, where `FillColor` becomes this colour;
- the whiteboard's freehand lines and markers;
- the allied resource bars;
- the shared-map-position rectangles.

The keys are read once, when the DLL creates its whiteboard at engine
start. That happens only under the DLL mode switch that also enables its
input handlers; with the switch off, the table is never filled. The megamap
copies the table at each battle entry.

**Established — allied resource bars.** The allied income panel draws a row
for each listed player. The panel is the one placed by `IncomePosX` and
`IncomePosY`. Each row begins with an 8 × 8 filled square in that player's
table colour, 36 pixels right of and 1 pixel below the row's origin. The
square is the only use of the table in the panel. Names, numbers and bars
use fixed colours.

The lookup takes the player's logo colour from the engine's player record
and indexes the table with it. With no engine player record available, it
uses the logo colour the DLL recorded for that player instead. A player
index above 9 gives palette index 0.

The retail minimap's own contact blips still come from its `radlogo` art
[03 §3.9], and no reader of the table was found on that path. ProTA's
"improved minimap dot colours" therefore come from its content, not these
keys.

**Established — `PlayerMarkerPcx` is whiteboard-only.** The key names the
whiteboard dot-marker strip: ten `PerPlayerMarkerWidth` ×
`PerPlayerMarkerHeight` (default 10 × 10) frames, with
`PlayerMarkerBackground` (default 9) as the transparent index. The value is
cut at the first `;`, resolved against the working directory, and used only
if that file exists; otherwise the built-in drawn markers are used. Its only
reader is the whiteboard's marker drawing, which places each marker at its
game-view position relative to the camera. The megamap never draws the
strip, markers or whiteboard lines. In each frame the whiteboard pass runs
before the megamap blit, and the blit covers the whole game-view rectangle,
so while the view is shown, whiteboard markers and lines inside the game
view are hidden under it.

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
- **Settled for ProTA 4.8 (2026-09-24) — the megamap's neutral order, the
  terrain picture, the ring colours, the overlay, the projectile gate and the
  dot-colour readers.** See
  [ProTA 4.8 shipped megamap](#prota-48-shipped-megamap).
- **Supported inference — ProTA 4.8 megamap build placement and select
  cursor.** Placement is revalidated only through retail's per-frame
  preview, which needs retail's own pointer record inside the game view. The
  inference that the record is frozen at the last position the game saw,
  and its build and selection consequences, need the manual test under
  "Build placement from the megamap".
- **Unknown — ProTA 4.8 click snapping on the megamap.** Whether the mex- and
  wreck-snap handler finds and places snapped sites from the megamap's point.
- **Unknown — the row-building ghost's alternate valid colour.** Which
  engine code-byte condition selects palette index 234 instead of 240.
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
