# Community patch rendering

## Evidence scope and version boundary

**Established — implemented behavior of the current source only.** This
reference describes the MIT-licensed TADR `src/DDraw` tree at
[`dcff5ddeb6bd1030e3f452c0f16e5f005850f62f`](https://github.com/tanvanman/TADR/tree/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw),
inspected on 2026-09-21. Source identity, license and the seven build profiles
are owned by [Community patch engine behavior §2–3](community-patch-engine.md#2-evidence-scope-and-sources).
The source's draw version is 2026.8.6.0; this revision also contains subsequent
changes. The contracts below are independently described source behavior,
not retail evidence or authorization to adopt a renderer policy.

**Unknown — equivalence to the older package DLLs.** These contracts do not
prove that ProTA 4.8, TA Zero Alpha 5 or Escalation Gold 10.2.0 shipped the same
algorithms. Their package identities and recorded historical interface remain
in [Shared draw-DLL interface](draw-engine-interface.md) and the linked
package references. A version-matched licensed source revision, primary
release documentation, or a bounded manual observation would settle each
older release's applicability. No patch-binary analysis was used for this pass.

**Established — scope and routing.** `ddraw.cpp` installs `ShadingFix`,
`TeamColorNanolathe`, `ReloadBars` and `UnitStatusCounters` without a profile
conditional; their own settings still apply. Megamap modules require
`USEMEGAMAP`; terrain feature artwork additionally requires
`MEGAMAP_FEATURES`. Those flags are on for the current prota, tazero and
escalation profiles. These are presentation contracts. Rotation placement
and creation remain owned by
[Community patch engine behavior, CP-CON-5](community-patch-engine.md#56-construction-and-builder-behavior),
and author-key registration by its CP-UD-2. Settings and selection controls
remain in that document's §4 rather than being duplicated here.

## Shade-level zero fallback

**Established — `ShadingFix.cpp`, `Install` and `ShadeLevelProc`.**
`ShadingZeroFallbackLevel` defaults to 5 and is clamped to 0–31. Zero disables
the extension. With a nonzero setting, only a computed shade level of exactly
zero is replaced by the configured level; positive levels retain their own
lighting. This does not raise every dark level or rewrite the shade table.
The module describes the replaced zero result as the ordinary source-colour
case that otherwise becomes black. Source:
[ShadingFix.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/ShadingFix.cpp).

**Unknown — historical adoption and pixel equivalence.** The source establishes
this substitution, not the older DLLs' light calculation or complete model
rasterization. A source-matched or observed old-release comparison is needed
before treating the fallback as an old-package requirement.

## Team-coloured construction effects

**Established — `TeamColorNanolathe.cpp`, `LoadColorConfig`,
`ParseColorList`, `SetPendingForSource`, `SetPalette`, `AdvancePalette`,
`MapNanoframeColorInternal`.** `TeamColorNanolathe` defaults off. When enabled,
colour selection uses the owner's logo-colour index, 0–9, rather than player
slot order. `Player1StreamColors` through `Player10StreamColors` therefore
configure colour slots. Each stream accepts 1–15 decimal palette indices;
each corresponding `PlayerNFrameColors` requires exactly 16. Values must be
0–255, separated by commas with optional spaces or tabs; a semicolon ends the
list. Invalid lists retain that slot's built-in default. A trailing comma is
accepted by the parser. Source:
[TeamColorNanolathe.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/TeamColorNanolathe.cpp).

**Established — default stream and frame mappings.** The source's
`kDefaultColorConfigs` defines these ordered lists; repetitions in frame lists
are significant. No interpolation between palette indices is performed.

| Colour slot | Stream indices | Sixteen frame indices, in order |
|---|---|---|
| 1 | 224,225,226,227,228,229 | 224,224,225,225,226,226,227,227,228,228,229,229,230,230,231,231 |
| 2 | 249,201,202,203,204,205 | 201,201,201,202,202,203,203,204,204,205,205,206,206,207,207,207 |
| 3 | 81,82,83,84,85,86,87 | 80,80,81,81,82,82,83,83,84,84,85,85,86,87,88,89 |
| 4 | 233,234,235,236,237,238 | 232,232,233,233,234,234,235,235,236,236,237,237,238,238,239,239 |
| 5 | 103,104,105,106,107,108,109 | 103,103,104,104,105,105,106,106,107,107,108,108,109,109,110,111 |
| 6 | 217,218,219,220,221,222 | 216,216,217,217,218,218,219,219,220,220,221,221,222,222,223,223 |
| 7 | 208,193,194,195,196,197 | 192,192,193,193,194,194,195,195,196,196,197,197,198,198,199,199 |
| 8 | 89,90,91,92,93,94,95 | 88,88,89,89,90,90,91,91,92,92,93,93,94,94,95,95 |
| 9 | 129,130,131,132,133,134,135 | 128,128,129,129,130,130,131,131,132,132,133,133,134,134,135,135 |
| 10 | 65,66,67,68,69,70,71 | 64,65,66,67,68,69,70,71,72,73,74,75,76,77,78,79 |

**Established — ownership and lifetime.** A valid unit currently dispatching an
order in the same game tick supplies the emitter colour. Otherwise the module
finds the nearest live unit with a definition and owner by squared distance
in all three fixed-point coordinates, keeping the first on a tie; it does not
restrict this fallback to constructors. Exact source-position matches can
reuse the resolved colour while their age is strictly below 15 ticks. Forward
and reverse streams resolve from the builder-side endpoint. All particles in
one emission burst retain that resolved colour. Effect tags survive through
their inclusive expiration tick; an untagged, invalid-colour or expired effect
keeps the original colour path. Tag-capacity exhaustion also loses only the
recolouring.

**Established — palette behavior.** Each colour slot has its own cursor,
initially zero. A tagged particle's stream-list index is the incoming
palette-selection offset plus that cursor, modulo the list length; the cursor
advances once at that selection. During the engine's seven-step advancement
path the selected colour is preserved when it belongs to the tagged colour's
stream list. An untagged effect whose current colour appears in any configured
stream list also gets this preservation. Thus the module's advancement guard
is broader than tagged construction particles alone. Nanoframes separately
map incoming palette indices 160–175 to frame-list entries 0–15. Other source
colours, invalid owners, and the disabled preference pass through unchanged.
The placement preview calls the same nanoframe remapper.

**Unknown — imported emitter phase.** The module consumes the engine's incoming
palette-selection offset; it does not define the engine's particle creation
order or that offset's generator. This source therefore does not independently
establish complete particle-by-particle animation equivalence. An owning
retail emitter contract or bounded observation would settle that input.

## Reload bars and unit counters

**Established — `ReloadBars.cpp`, `SelectTrackedWeapon`,
`DrawReloadBarForUnit`, `DrawReloadBar`.** The author-facing `reloadbar` key
uses its integer low bit. Tag matching accepts either the definition identity
or the recorded weapon name. Slots are examined in order 1, 2, 3. A tagged
slot needs a nonzero reload duration and must not be stockpiling; the largest
duration wins, with equal durations retaining the earlier slot. The unit
definition's nonzero duration and nonzero type mask take priority over the
live slot's copies, with fallback independently for each. Source:
[ReloadBars.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/ReloadBars.cpp).

**Established — admission and arithmetic.** The unit-bars draw callback must
be reached, its game-option gate must be set, both supplied screen coordinates
must be nonzero, the unit must be owned by the local human, and its construction
fraction must be zero. With duration `D` and remaining reload `R`, progress is
`D − R` when `R ≤ D`, otherwise zero. Fill length is the integer quotient of
32 times progress divided by `D`. Its source palette index is
`144 − min(6, floor(floor(100 × progress / D) / 15))`; both this and background
index 0 pass through the GUI palette lookup. The bar's top is three pixels
below the callback's supplied vertical anchor; its outer rectangle spans
17 pixels either side of the horizontal anchor and four pixels vertically,
with a one-pixel inset. The extension does not change reload timing.

**Established — `UnitStatusCounters.cpp`, `GetStockpileStatus`,
`GetTransportedUnitCount`, `DrawForUnit`, `DrawReadableText`.** The same bars
callback draws counters before the reload bar. Counters require a positive
health value and local-human ownership, the same game-option and nonzero-anchor
gates, but have no separate construction-fraction test. Stock is summed over
all stockpile slots; a positive background-order queue amount is displayed as
queued stock. Text is `stocked +queued` and is absent only when both are zero.
For transports, text is `loaded/capacity`; it is omitted when empty, when
capacity is zero, or for a flying transport of capacity one. The cargo walk
counts only members whose transporter is this unit and stops at 4096 visits
or a null, self, or return-to-first link. Source:
[UnitStatusCounters.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/UnitStatusCounters.cpp).

**Established — counter placement and appearance.** Each counter is centred
by text advance width, above the anchor by font height plus three pixels. If
both exist, cargo sits a further font height plus one pixel above stock.
Configured colours clamp to 0–255; both counter toggles default on and their
colours and the group-number colour default to 255. Eight black neighbouring
copies produce a one-pixel outline around the configured-colour text. A
single-character group number is centred using measured glyph ink, not advance
width. These paths restore the font's previous colours after drawing.

**Unknown — upstream bar admission.** The module reads one game-option gate
and inherits the engine's unit-bars traversal. This pass does not name that
option or independently establish every upstream visibility/selection gate.
Source-matched documentation or the owning retail bars contract would settle
it; the local-human test must not be generalized to allies.

## Placement ghost and rotation artwork

**Established — `buildghost.cpp`, `GetBuildGhostPreviewStyle`,
`RenderNanoframeGhost`, `RenderGhostAtCurrentBuildSpot`.** Preview style is
cached after configuration becomes available. It defaults to wireframe;
legacy `NanoframePreviewFill` is parsed first, then a recognized
`NanoframePreview` overrides it. Case-insensitive substring matches select
disabled (`disable`, `none`, `off`), full (`full`, `fill`, `true`), then
wireframe (`wire`, `frame`, `false`), in that order; unrecognized input keeps
the prior choice. A disabled value in the legacy boolean key means wireframe,
not disabled. The implementation accepts `false` as wireframe even under the
new key; a source comment claiming otherwise is stale. Source:
[buildghost.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/buildghost.cpp).

**Established — admission.** Disabled preview also suppresses its author-key
registration and rotate-key tip. Otherwise a ghost requires a prepared build,
a valid nonzero definition, `bmcode = 0`, and an accepted build rectangle;
a blocked red rectangle has no model ghost. The ordinary cursor path also
requires the pointer inside the game view. Line-building draws one ghost per
candidate through its separate loop and suppresses the ordinary cursor copy.
The anchor is the build-rectangle midpoint with the terrain elevation's
half-height projection; sprites are clipped to the game view.

**Established — geometry and piece filtering.** `GetNanoframeSprite3D`,
`RotateXZ`, `ProjectToScreen`, `FillConvexPolyZ`, and `RasterEdge` build a static
sprite for each definition/facing. The model uses authored piece offsets and
vertices, without running the COB pose. The per-facing nonempty
`PreviewPiecesS/E/N/W` string replaces the global nonempty `PreviewPieces`;
after parsing, an empty token set falls back to the effect-piece filter.
A nonempty whitelist matches case-folded piece names. Otherwise names
containing flare, flash, muzzle, fire, flame or wake are omitted. Hiding a
parent's faces does not suppress its children. `PreviewObject3D` may substitute
the model; a failed load is remembered and uses the ordinary model, while the
same filtering still applies. `GetPreviewModelRoot` passes the literal
`objects3d` directory to the engine path helper; this pass does not establish
whether that helper redirects it for every renamed mod tree.

The four horizontal transforms, in the source model's X/Z coordinates, are
S: `(-X, -Z)`, E: `(Z, -X)`, N: `(X, Z)`, W: `(-Z, X)`. Projection uses the
integer X coordinate and minus the integer Z coordinate minus half elevation,
with separate arithmetic shifts before summing. Only polygons with strictly
positive projected signed area are retained. Elevation determines frontmost
surface; equal depth replaces the previous pixel. Edges are tested against
that surface with a two-step depth tolerance. Duplicate edges are identified
by projected endpoints, not model identity. A model yielding no retained
faces or a sprite dimension above 1024 produces no ghost. This is the source's
preview approximation, not a substitute retail model-raster contract.

**Established — scan and shimmer.** Vertex elevation and unrotated model-Z
are normalized into 1–255: subtract the minimum, multiply by 254, add half the
integer range, divide by the range, then add one and clamp; a zero range uses
one as divisor. Surface rasterization interpolates these normalized values.
The unrotated coordinate makes the scan follow the model's front axis after
rotating. Elapsed game ticks since user rotation form a
30-tick cycle. For phases 0–14, the scan centre is
`1 + floor(phase × 254 / 14)`; phases 15–29 have no scan. Surface pixels within
an inclusive distance of two coordinate steps receive scan ink. Without team
colours the scan ink is palette 250; with them it uses the frame mapping of
palette 160.

The shimmer phase is `(elapsed + 23) modulo 30`; multiply it by 32, divide by
30, truncate, then add 16. A 32-step triangular palette ramp visits 160–175
and 175–160. Fill uses that ramp position and edges use the position plus 16.
Full style paints the silhouette plus scan; wireframe paints only edges and
scan interior. Fill is submitted first and edges second, so edges win where
they overlap the scan. User rotation resets scan timing; automatic
opponent-facing selection does not itself reset it.

**Established — exact opponent-facing boundary.** `PreviewFaceOpponent` scans
only while the rectangle centre is within the inclusive squared build distance
of at least one selected, live, completed local unit with positive build
distance. It then examines each active, non-watching, non-allied player's
first unit slot, skipping a missing or unused slot. It does not verify that
slot's definition is a commander and does not test enemy LOS, mapping, cloak
or radar admission. The nearest squared horizontal distance wins, with a tie
retaining the earlier player. East/west is selected only when the absolute X
difference is strictly greater than the absolute map-Y difference; equal
magnitudes select north/south. A coincident target selects north. The result
bypasses authored allowed facings. The builder-distance gate limits remote
cursor scanning; it does not prove absence of information about an unseen
enemy inside that permitted region.

**Established — `unitrotate.cpp`, `DrawBuildMenuRotationOverlays`,
`LoadGaf4Frames`, `GetAnimsDirName`.** Build-menu arrows apply to active,
unoccluded buttons matching a structure with at least two allowed facings.
Only allowed facings receive arrows. Any overlap with a higher panel suppresses
the whole button's arrows. The animation directory is the running engine's
configured name, supporting renamed mod trees; its fallback is `anims`.
`buildrotate.gaf` and optional `buildrotateclick.gaf` use the first sequence,
which must contain four frames in S/E/N/W order; each dimension must be 1–1024.
Loads go through the engine's archive-aware reader. Source:
[unitrotate.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/unitrotate.cpp).

A custom idle frame is placed by its hotspot seven pixels inside the relevant
button edge. A successful cardinal click has a strictly less-than-200 ms
feedback window. If the click artwork exists it replaces idle artwork in
that window; custom idle artwork alone has **no** tint-flash fallback, despite
an earlier comment in the file. Without custom idle artwork the built-in
chevrons use palette 251 with a black outline and flash in the HUD highlight
colour. The centre of the button preserves the previously selected facing.
Turning the overlay off forces panel redraw so old arrows disappear.

## Megamap images, palette reduction and composition

**Established — terrain raster and palette subset.** In `MapParse.cpp`,
`MiniMapPicture::StretchTATNTDataToMiniMap` builds the large map from tile
art, excluding the last tile column and last four tile rows. It fits the
result within the available rectangle while retaining the playable map's
aspect. Each output cell averages its covered source pixels' gamma-encoded
RGB channels with integer division. Candidate output colours are the palette
indices present anywhere in the map's tile graphics; unusable tile metadata
falls back to the whole palette. Candidate order is increasing palette index.
`MegamapSnap` chooses minimum squared OKLab distance, keeping the first on a
tie. The source's active compile-time choices enable area averaging and
OKLab selection. Source:
[MapParse.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/MapParse.cpp),
`MegamapAveragePixel`, `BuildMegamapPaletteSubset`, `MegamapSubsetNearestOKLab`.

**Established — diffusion.** `MegamapDither` defaults on. The first row scans
left-to-right and subsequent rows alternate. Before choosing an index, add
rounded incoming error to each averaged RGB channel and clamp to 0–255.
Error is stored in sixteenths and rounds symmetrically: magnitude plus eight,
divided by sixteen, then restore the sign. Subtract the chosen palette RGB
from the clamped input, distribute 7/16 forward on this row, and 3/16 behind,
5/16 below, 1/16 forward on the next row. Selection uses OKLab but error remains
in gamma RGB. Disabling dither, or failing to allocate its working buffers,
keeps area averaging and OKLab selection while dropping error propagation.
These are presentation calculations, independent of simulation arithmetic.

**Established — built-in minimap is a different source image.**
`FullScreenMinimap::RefreshBuiltInMinimap` replaces only the built-in radar's
terrain image; the engine retains its own fog and blips. It uses the map's
stored minimap artwork, not the large map's tile-art raster. The meaningful
stored-image rectangle is the lesser of the stored dimensions and twice the
destination dimensions in each axis, excluding rectangular-map padding.
`TNTtoMiniMap::RenderStoredMinimapAtSize` averages that region and applies the
same palette-subset/OKLab/dither process. Destination dimensions above 126 in
either axis are rejected by the caller. Source:
[fullscreenminimap.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/fullscreenminimap.cpp),
`RefreshBuiltInMinimap`; `MapParse.cpp`, `RenderStoredMinimapAtSize`.

**Established — features precede fog.** `DrawFeaturesIntoMappedBits` in
[UnitMinimap.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/UnitMinimap.cpp)
adds indestructible, non-reclaimable features to the terrain base when
`MEGAMAP_FEATURES` is enabled. It uses the first frame of the resolved GAF
sequence. `REUSE` filenames resolve through a definition with a matching
case-insensitive description and a real filename; if the exact sequence is
unavailable, a loaded same-file sequence with the feature name is preferred,
then that file's first usable sequence. This is a best-effort art fallback.
Definitions outside the 1024-entry sprite cache use the dot fallback too.

Footprint-centred position includes half the terrain elevation as vertical
lift. Size follows art dimensions at the same horizontal world scale, with a
two-pixel minimum major dimension; aspect is retained. The vertical anchor
uses the scaled GAF hotspot, while X uses geometric centre. Box reduction
averages colour only over nontransparent source pixels; alpha is the covered
fraction, rounded to the nearest 0–255 value. Compositing blends that alpha
against terrain and quantizes by **RGB distance over the whole palette**,
not the terrain's restricted OKLab path. Missing art uses a clipped 3×3 dot:
254 for positive-metal features, otherwise 211 for description `Spire`, or
165 for other qualifying features. The static base is rebuilt when terrain
source or feature-map identity changes; in-place feature edits alone do not
invalidate it.

**Established — fog and draw order.** `MappedMap::NowDrawMapped` starts from
that terrain-plus-feature base, applies mapped/LOS masks and the engine's
grey table, and caches the result for the current game tick. `NOMAPPING` and
`Permanent` are source flag names, not a new interpretation of their UI labels:

| `NOMAPPING` | `Permanent` | Terrain treatment in this module |
|---|---|---|
| clear | clear | Base image unchanged |
| set | clear | Black where this player's mapped bit is absent |
| clear | set | Grey where current LOS is absent |
| set | set | Black where unmapped; otherwise grey where current LOS is absent |

The two current-LOS branches start their vertical sampling at minus the
integer quotient of sea level divided by 20, clamping negative sample rows
to zero; the mapped-only branch starts at zero. Scaling uses repeated
floating-point additions before truncating sample coordinates. Source:
[MappedMAP.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/MappedMAP.cpp),
`NowDrawMapped`. `FullScreenMinimap::Blit` then draws projectiles into the
mapped image, copies that into the unit surface and draws unit icons, then
composites UI/order overlays and cursor. Thus terrain features inherit fog
and sit below projectiles and units.

**Unknown — same-tick reuse after view changes.** The fog-image early return
keys only on game time, before checking sight-player or other changed inputs.
Projectiles also write into its working result after this pass. The source
therefore does not establish immediate fog refresh on a same-tick viewpoint
change or clearing of every prior projectile mark between such draws. A
bounded paused/view-switch and moving-projectile observation would establish
the visible consequences; do not claim those caches are transparent in all
host states.

## Megamap icons, contacts and rings

**Established — icon bank, not a packed atlas.** `UnitsMinimap::LoadUnitPicture`
loads PCX pictures and builds three recoloured copies per player: selected,
unselected and hovered. It refreshes them when that player's logo colour
changes. The fill colour is replaced by the configured player-dot colour.
Selected artwork retains its selection ink; unselected artwork converts
`SelectedColor` to the transparent key, and hover artwork converts it to
`HoverColor`, before applying fill replacement. `UseCircleHover` instead
keeps ordinary artwork and draws a circle round the hovered icon. Source:
[UnitMinimap.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/UnitMinimap.cpp),
`LoadUnitPicture`, the three `Init*PicturePlayerColors` methods, `UnitPicture`,
`DrawUnit`. Settings and older package paths are in
[Shared draw-DLL interface, Megamap](draw-engine-interface.md#megamap).

**Established — mask ordering and placeholders.** Custom `[Icon]` entries
use authored category masks, in the order returned by the INI section reader,
after available side-specific commander pictures. The default bank similarly
loads available commander pictures first, then commander, combat aircraft,
air constructor, mobile constructor, mobile combat, factory and building
pictures. It does not load a PCX for every unit definition. The first matching
mask wins for a unit identified by the engine's LOS helper. `UNKNOWN` is the
fallback for an identified unit with no category match. The reserved `nothing`
entry instead supplies the icon for a contact not identified by that helper;
it is a picture, not an unconditional instruction to suppress a contact.
`nukeicon` is a separate projectile picture. Missing custom files and omitted
reserved entries are not repaired into a complete guaranteed icon set by the
loader.

**Established — upstream contact admission.** `NowDrawUnits` walks the
engine's current hot-radar-unit list in order, ignoring entries whose unit
has no definition ID. It does not scan all units or rebuild sensor visibility.
`UnitPicture` only uses the engine LOS helper to choose identified-category
art versus the `nothing` picture. This preserves a distinction between being
an admitted contact and being identified. Selected art wins over hover art;
colour-circle hover is independent of that choice. Attack flashing omits
only icon pixels when recent damage is nonzero and the shared blink phase is
clear; the subsequent hover/ring drawing still runs.

**Established — positioning.** Icon centres subtract half the definition's
footprint from each horizontal unit coordinate and also subtract half unit
elevation from map Y. X and Y then scale independently by the playable extents
`(feature-grid width − 2) × 16` and `(feature-grid height − 8) × 16`, truncating
after floating-point scaling. A nonpositive extent falls back to the parent's
map extent. Icons are clipped to the map image, retain their PCX dimensions,
and use the configured transparent palette index.

**Established — threshold defaults and actual consumers.**
`UnitsMinimap::Init` sets radar, sonar and jammer thresholds to zero and
antinuke to 512. An INI value of −1 preserves that constructor default;
it does not mean a negative effective threshold. The shipped current INI
explicitly sets all thresholds to zero, including antinuke. Sensor rings
require a selected allied unit and an authored distance **strictly greater**
than the threshold. Radar-jammer drawing compares `MegamapRadarMinimum`;
the parsed `MegamapRadarJamMinimum` has no drawing consumer here. Sonar
jamming uses its own threshold. There is no activation-state test in this
ring block.

**Established — weapon and interceptor circles.** Weapon circles require an
allied hovered/range-designated unit and the controller's nonzero Shift-key
state (`MegaMapControl::IsDrawOrder`);
slots are drawn 3, 2, 1 when their weapon-active bit is set and range is nonzero.
`GetFlatWeaponRange` limits ballistic range to the lesser of authored range
and the integer quotient of `velocity² × 30² / (65536² × gravity)` when stored
fixed-point velocity and gravity are both positive. Otherwise it keeps authored
range. Pixel radius is the resulting world range times map-image width divided
by playable world width, using integer division. Interceptor circles instead
require a selected allied antinuke-capable unit, an interceptor weapon and
coverage strictly above the antinuke threshold. Each slot's existing dotted
indicator selects dashed versus solid; the renderer does not itself derive
that indicator from ammunition. All radii use horizontal scale, even on a
rectangle. Source: `UnitMinimap.cpp`, `DrawUnit`, `GetFlatWeaponRange`;
[fullscreenminimap.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/fullscreenminimap.cpp),
`InitMinimap`, for preference inputs;
[MegamapControl.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/MegamapControl.cpp),
`IsDrawOrder`, for the Shift gate.

**Established — projectile markers.** `ProjectileMap::DrawProjectile` first
applies the zero-damage-map-weapon marker suppression owned by CP-WPN-5.
Positions use map Y minus half elevation, including for the LOS-cell lookup.
After its coarse coordinate bounds check, own and allied projectiles pass
the module's visibility test. Others use current
LOS when `Permanent` is set; with it clear, `NOMAPPING` admits all and its
absence requires the player's mapped bit. Cruise/targetable/stockpile weapons
with all three properties, and all interceptors, use the owner-coloured nuke
picture; other admitted projectiles draw a clipped 2×2 marker. Source:
[ProjectileMap.cpp](https://github.com/tanvanman/TADR/blob/dcff5ddeb6bd1030e3f452c0f16e5f005850f62f/src/DDraw/ProjectileMap.cpp),
`IsPosInPlayerLos`, `DrawProjectile`, `DrawNuke`, `DrawWeapon`.

**Unknown — inherited visibility and malformed boundaries.** The full
hot-radar list predicate, LOS-helper semantics and per-slot dotted-state
production belong to the engine; this module alone does not establish them.
The shared attack-flash cadence is the retail radar blink phase, which
toggles every eight sub-ticks [01 R-CORE-03]. In the shipped ProTA 4.8 build
the dotted state is the slot's interceptor flag byte, which the retail minimap
ring also reads [03 §3.9]. That this source's per-slot indicator is the same
byte is **Supported inference** from the matching use; its field offset was
not compared. The projectile bounds predicate rejects coordinates
strictly greater than LOS dimensions, leaving equality admitted to subsequent
checks; its unsigned-coordinate checks do not independently establish safe
negative-edge handling. A retail caller contract or bounded edge observation
would settle those cases. No extra reveal, cloak or safe-clamping rule is
implied by this reference.

**Established — differences from the shipped ProTA 4.8 build.** The
authorized audit of ProTA 4.8's `tdraw.dll`
([Shared draw-DLL interface, ProTA 4.8 shipped megamap](draw-engine-interface.md#prota-48-shipped-megamap))
differs from this pinned source in these contract-level ways:

- **Scale extents.** 4.8 scales icons and projectiles by the TNT-derived
  `(Width − 1) × 16` by `(Height − 4) × 16` extent. This source uses the
  play-area extents.
- **Pointer conversion.** 4.8's has no height-shear search.
- **Hover hit area.** 4.8 tests a box offset by one footprint from the drawn
  icon, where this source tests pixels.
- **Weapon rings.** 4.8 uses raw authored `range` (no ballistic limit) and
  reads Shift as the asynchronous key state.
- **Interceptor rings.** 4.8 keeps retail's `coverage − 512` radius.
- **Nuke marker.** 4.8 requires `twophase`, `cruise` and `targetable`
  together, with no interceptor clause and no zero-damage suppression.
- **Fog image.** 4.8 has no per-tick cache.
- **Features.** 4.8 has no feature layer.
- **Icon clipping.** 4.8 shifts clipped edge icons instead of cutting them.

The thresholds' defaults, the radar-jammer threshold quirk, the fog table,
the projectile admission test, picture choice and the redraw-rate gate match.
This source's view toggle on key release and its wheel-enter/wheel-leave
behaviour also match the shipped build; neither build has zoom steps. None
of these differences is evidence about Escalation or Zero builds.

## Visual acceptance cases

These are proposed checks derived from the **Established** source contracts
above, not reports of completed visual validation or proof about older DLLs.
Record the exact package, source/build profile, content, map, tick, settings,
player colour and viewpoint for comparisons. Keep captures outside the repo.

| Contract | Distinguishing observation |
|---|---|
| Shade fallback | Compare levels 0, 1 and 5 with fallback 0 and 5; only input zero changes. |
| Team colours | Use two owners with swapped logo colours; exercise forward and reverse streams, a nanoframe, and a placement ghost with a visibly nonmonotonic custom frame list. |
| Reload arithmetic | Use two tagged non-stockpile slots of unequal and then equal duration; check remaining reload at zero, full duration and above duration. Stockpile slots and allies receive no reload bar. |
| Counters | Compare stock plus queued stock, a multi-unit transport, and a capacity-one flying transport; inspect simultaneous counters and outlined group-number centring. |
| Preview geometry | Use asymmetric geometry and a hidden parent with an allowed child in all four facings; compare full/wireframe at phases 0, 14 and 15, then reset by user rotation. |
| Preview gates | Move across valid/invalid sites, game-view edge and line-building; compare exactly at and just outside selected-builder range, including an unseen enemy first slot. |
| Rotation artwork | Test renamed animation directories, idle-only and idle-plus-click artwork, missing artwork, upper-panel occlusion and the 200 ms feedback boundary. |
| Terrain colour | Compare grass and saturated terrain with dither on/off; use rectangular stored minimap artwork containing visible padding to distinguish built-in and full-screen paths. |
| Feature composition | Inspect tall indestructible art, metal spots, unresolved GAF fallback and reclaimable wrecks across unmapped, explored and current-LOS regions. |
| Contact identity | Keep an admitted radar-only contact and then reveal it: generic contact art must become the first matching category picture. Change logo colour while preserving player slot. |
| Ring thresholds | Test range equal to and one above threshold; omit antinuke config versus explicit zero; change radar and radar-jammer thresholds independently; compare ballistic and authored limits. |
| Caches and boundaries | Pause and change viewpoint; inspect successive projectile draws and all map edges. Record observations before claiming behavior beyond the source's guards. |

## Remaining boundaries

- **Unknown — release applicability:** exact source revisions for the three
  older shipped draw DLLs; settle with version-matched source, release evidence
  or manual observations, contract by contract.
- **Unknown — renamed preview-model directory:** whether the engine path
  helper redirects the explicit `objects3d` argument in every mod package;
  settle with that helper's contract or a substitute-model observation.
- **Unknown — engine-owned inputs:** particle phase/order, unit-bar traversal,
  contact-list admission and the LOS helper; settle in their owning retail
  contracts or with bounded observations. The blink cadence and the
  interceptor dotted flag are now tied to [01 R-CORE-03] and [03 §3.9].
- **Unknown — full visual equality:** no source-matched runtime captures were
  made in this pass. The acceptance table defines useful comparisons, including
  current-source limitations, rather than claiming finished compatibility.
- **Unknown — host edge behavior:** missing icon artwork, same-tick viewpoint
  changes, projectile image reuse and map-boundary inputs need observations or
  caller contracts before a complete image-equivalence claim.

Any Nanolathe implementation must retain the committed-frame presentation
boundary in [INVARIANTS I6](../../docs/INVARIANTS.md#i6--presentation-boundary)
and keep host rendering preferences separate from gameplay selection. These
findings do not approve changing the existing renderer policies in
[DESIGN_GPU_RENDERER](../../docs/DESIGN_GPU_RENDERER.md).
