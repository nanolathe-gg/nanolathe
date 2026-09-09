# Design — World and visibility

`internal/world` and `internal/visibility`. Terrain geometry loaded from TNT,
the plot-cell grid and its height queries, the placement validator and its
footprint arithmetic, occupancy and yard maps, feature anchors, wind, and the
per-player line-of-sight, radar and sonar state that gates what every other
subsystem may see, target and build on.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation routing that
makes a bare `[Cn]` in `internal/visibility` resolve to the contract list in §3.2
below.

## 1. Purpose and boundary

These two packages answer three questions for the rest of the simulation:

* **What is the ground here?** A height, a slope pair, a feature reference, an
  occupancy word, a metal byte — per attribute cell, from the map's TNT.
* **May this footprint stand here?** One read-only placement predicate over a
  half-open cell rectangle, shared by the build-cursor preview, the order
  commit, the computer player's siting, and factory exit spots.
* **Can this player see that?** One visibility predicate over the published
  32-pixel grids, plus the sensor phase's per-unit status bits.

Everything above is authoritative simulation state. The boundary runs at three
places:

* **Fog presentation is not here.** This package owns the two-channel fog
  *cache* — a derived per-cell value pair rebuilt from the authoritative grids —
  and publishes it across the frame boundary. Turning those values into pixels
  (the GAF frame selection, the gray-table remap, the viewport clip and the
  hard 32-pixel edges) is `internal/render/fog.go` and `internal/client`, owned
  by [DESIGN_PRESENTATION_CLIENT](DESIGN_PRESENTATION_CLIENT.md). The minimap's
  sensor circles and radar picture are presentation too `[03 §3.9]`
  `[03 §3.10]`.
* **The sensor phase does not rasterize.** It writes unit status bits and
  nothing else; it never authors the visibility word mask `[03 R-VIS-01 §4]`.
* **The tick belongs to `internal/session`.** Visibility publication is a seam
  *inside* the fifth phase of the twelve-phase tick — the path scheduler first,
  then per player in ascending slot order that player's orders and work pump,
  then that player's dirty-checked stamp sweep — not a phase of its own
  (ARCHITECTURE §4, `[01 R-CORE-01 §4.4.1]` `[03 R-SENSOR-01]`). Wind's
  scheduled redraw is the eighth phase. Neither package reads a clock, allocates
  from a random stream, or logs.

Terrain is immutable geometry after load in one strong sense: **there is no
terrain deformation** `[03 R-TERR-01 §3]`. What changes at run time is the
feature word, the two occupancy planes, the metal byte, and the flag byte —
never the height data, and therefore never the line-of-sight height table.

## 2. Packages and key types

### 2.1 `internal/world`

**Coordinates** (`coords.go`). One map pixel is 65,536 world units; one cell is
16 map pixels (`1 << 20`); one tile is 32 map pixels (`1 << 21`) covering 2×2
cells `[03 §2.1]`. `WorldToCell`, `WorldToTile`, `CellToWorld` and `TileToWorld`
are the only conversions in the codebase, and they floor with a sign correction
so world unit −1 lands in cell −1, not cell 0 [I3].

**Terrain** (`terrain.go`). `Load` resolves the map key through the catalog to a
logical `maps/<key>.tnt` path, reads it through the VFS, and hands the bytes to
`formats.LoadTNT`, which owns the version gate and the per-version header slot
map. What comes back becomes a `Terrain`: cell dimensions, the tile-index array
and its 1024-byte 32×32 blocks, the expanded plot, sea level, gravity in both
authored and per-tick forms, the wind bounds, the tidal scalar, the two
acid-water words, the lava-world flag, and the map's own feature-record list
bound name-wise to catalog definitions. Load then runs three post-passes in a
fixed order: the line-of-sight height table, the feature-anchor stamp, and the
void fixup.

Three *different* height queries live on `Terrain` and are not substitutable
`[03 §2.3]` `[03 R-TERR-01 §4]`:

| Query | Shape | Used by |
|---|---|---|
| `HeightAt` | integer bilinear over four neighbouring cell heights on the low four bits of each cell-space coordinate, with the signed right-shift bias; the out-of-range guard returns the raw −1 sentinel rather than clamping | ground sampling, picking, the known-site gate |
| `CoarseHeightAt` | the mean of the cell's derived floor min and max | placement and airborne tests |
| `LOSHeightWord` | the aggregated two-byte word per 32-pixel visibility tile: low byte a maximum, high byte a minimum | the terrain-ray raster only |

The LOS height table is built once during load and never rebuilt: cells are
*scattered* through the beam shear with a carried previous-row pair so the shear
cannot skip a tile, each cell contributing a perspective-scaled value and then
its raw height, and a final pass blends the pair by thirds from the *original*
bytes and floors both at sea level `[03 R-P0-18-A §1]` `[03 R-P0-18-B §2]`
`[03 R-P0-18-B §3]` `[03 R-P0-18-B §4]`. `BuildLOSHeightWordsForTest` and
`SetLOSHeightWord` exist so fixtures can drive the horizon rule from exact byte
pairs; the simulation calls neither.

**Plot cells** (`plot.go`). `PlotCell` is a 13-byte array — the deliberate
exception to [I13] — with typed accessors: the two mobile-occupancy shorts
(ground plane and air plane), the height byte, the derived floor maximum and
minimum, the metal byte, the feature word with its sentinel band, the two
anchor-offset bytes, and the flag byte (live instance, structure-yard mark,
never-seen marker, placer nibble). `ExpandPlot` is the only path that produces
these bytes; `ResolveFeature` is the only resolver that follows a fringe cell's
signed offsets to its anchor.

**Void and playable insets** (`applyVoidFixup`). After the derived floor pair
and the feature stamp exist, the loader converts empty-or-fringe cells to the
void sentinel in four rules: the two right-hand columns; a north strip walked
downward per column that stops at the first row whose sheared projection is
non-negative; a south strip walked upward per column that voids the row *above*
the tested row; and, on a lava world, every convertible cell whose derived
minimum is at or below sea level. The same pass sets the playable insets
(`PlayRight`, `PlayBottom`) the camera clamp and minimap lens read. Nothing else
is written — not a height, not a flag `[03 R-TERR-01 §2]`.

**Placement** (`placement.go`). The typed vocabulary keeps three geometrically
distinct things apart, because retail keeps them apart `[04 R-P0-02]`:
`FootprintExtent` (an authored width/depth pair in cells), `FootprintAnchor`
(the snapped north-west origin), `FootprintRect` (the validated half-open
rectangle), and `ModelWorldPosition` (where the model stands). `MobilePlacement`
derives all of them from a picked point; `FactoryPlacement` keeps the authored
exit transform verbatim as the model position while snapping an independent
validation rectangle from it.

`CheckPlacement` is the single legality predicate. It validates the rectangle,
runs the known-site gate when a viewer record is supplied, then walks the
rectangle in row-major order applying the per-cell yard bits — structure-yard
mark, ground occupancy, blocking feature, indestructible feature, geothermal —
while accumulating the slope and height aggregates, and finally applies the
slope, site-height, and two water-depth gates. `ValidatePlacement` and
`SiteHeight` remain as cell-coordinate entry points for fixtures; production
callers construct a `PlacementQuery`.

**Occupancy.** Retail has one ground-occupancy word per cell, written by ground
movers and by building-class units alike `[04 R-COLL-01 §4]`. Nanolathe stores
its two halves separately — the plot cell's own short and the movement system's
lattice — so `Terrain.Movers` binds the mover half once for the whole battle
rather than per query. A placement check that consulted only the plot half could
not see a unit parked on the rectangle at all, which is exactly the defect that
made the build preview disagree with the commit. Only the ground word is read;
the air plane exists and is written by airborne movers, but the footprint
validator never consults it `[04 R-COLL-01 §2]`.

**Feature anchors and metal** (`feature_stamp.go`, `metal_stamp.go`,
`extractor.go`). `StampFeatureRect` is the low-level rectangle writer shared by
map bootstrap and the runtime feature service: it applies the dense-pack
teardown first, writes the anchor's feature index and the fringe cells'
signed deltas, and refuses a footprint that leaves the plot or whose deltas do
not fit the signed byte fields `[05 R-FEAT-01 §3]`. `ApplySchema` seeds the
uniform metal byte from the mission's schema; `SeedFeatureMetalDeposits` then
raises it under indestructible metal-bearing features, which is what makes
extractor yield vary across a map `[05 R-FEAT-01 §7]`.

**Picking** (`picking.go`). `CursorToWorld` inverts the half-height shear the
way retail does — a bounded southward-to-northward probe with a linear
interpolation between the two bracketing projections — rather than by an
algebraic inverse that would land a move order north of the clicked pixel
`[07 §8]`.

**Wind** (`wind.go`). `Wind` holds the authoritative bounds, strength, 16-bit
heading, published scalar and world vectors. `Jitter` is the complete scheduled
redraw the eighth tick phase calls every sub-tick: a strict deadline gate, then
one CRT interval draw, then the simulation strength draw, then — only when the
strength is nonzero — the simulation heading draw, then the vectors and the
one-tick change flag `[01 §7.3]` `[01 R-CORE-01 §4.4.1]` `[03 R-WIND-01]`.
Battle entry zeroes the deadline and draws nothing, so the first chain fires on
the first sub-tick `[01 R-CORE-02]`. The briefing-screen draws are front-end
display state with no battle-side reader.

**Save boxes** (`retail_save.go`). The detached metal image and the packed
placer-nibble image are produced and restored here so a retail account round-trip
touches no other cell field `[08 R-SAVE-02 §12]`.

### 2.2 `internal/visibility`

**Grid dimensions** (`grids.go`). The word grid is `(cellW/2) × (cellH/2)`
`uint16` cells — one cell per 32 map pixels, ten usable player bits — which is
the same allocation as retail's `cellW × cellH / 2` bytes read as little-endian
words. Beside it sits one `uint8` refcount grid per player slot of the same
dimensions `[03 §3.1]`.

**Mode word.** Four low bits, with explicit accessors so a higher bit cannot
acquire a second meaning: history, current coverage (byte-versus-word), raster
selection (terrain-ray versus sprite-mask), and the fog-cache-valid bit
`[03 §3.1]` `[03 R-VIS-01 §1]`.

**Where the low three bits come from** (`internal/session/composition.go`,
`visibilityModeForSession`, called once while services are bound). The session
kind picks the source and each bit is a plain one-bit copy — no inversion
`[03 R-VIS-01 §1]` `[08 R-ENTRY-01 §2 step 4]`. A skirmish takes the setup
record's `Mapping` / `LineOfSight` / `LOSType`. A **campaign takes the mission's
own OTA `[GlobalHeader]`**: the OTA loader stores `mapping` and `lineofsight`
(each defaulting to 0 on a missing key) into the single-player option words and
forces the companion words — LOSType to 1, commander death to 0 — every time it
parses a `GlobalHeader`, so the registry `Single*` triple is read at start-up and
immediately shadowed and never reaches a battle `[08 R-SKIR-01 §4]`. Bit 2 is
therefore constant 1 for a campaign, and `Circular` line of sight is unreachable
there; `mission.MissionGlobals.LOSType` carries an authored key for diagnostics
only and is not this bit's source. A session with no parsed `GlobalHeader` met
no such writer and keeps the registry default word, `Unmapped + True`
`[03 R-TERR-01 §8]`. A restored campaign save reloads the mission's OTA and
binds its services through the same path, so it derives the same word; the
save `Summary`'s `Mapping` / `LineOfSight` / `LineOfSightType` integers are
written and re-installed on multiplayer saves only `[08 "Summary"]`
`[08 R-SAVE-02 §11]`.

**Publication** (`publish.go`, `shapes.go`). Both rasters start from the same
quantization `q = floor(radius/32)` and both write the same word mask and the
same byte refcount; the mode bit selects only the shape.

* *Sprite-mask*: `idx = clamp(q − 5, 0, 9)` into the ten authored frames of the
  visibility-mask GAF, walked with start-inclusive/end-exclusive clipping,
  negative origins skipped to `max(0, −origin)`, unsigned bounds compares, and
  only opaque mask bytes touching the grids.
* *Terrain-ray*: `g = clamp(q, 0, numtables − 1)` into the declared `LOS.TDF`
  tables, walking `TABLE g − 1` because the table accessor is one-based against
  a zero-based store `[03 R-COMP-02 §1]`. Each authored line is a spoke of
  absolute offsets from the observer, expanded by four 90-degree rotations; step
  distances count from one; the origin cell is admitted unconditionally; each
  step bounds-checks unsigned *before* any terrain read; admission is the strict
  cross-multiplied horizon test against a retained pair that starts at `(−1, 0)`,
  gated by the LOS word's low byte, with the high byte deciding whether the
  retained horizon advances `[03 §3.2]` `[03 R-VIS-01 §3]`.

`Refresh` is the throttled entry point every publisher goes through: it stores
each observer's last raster and recomputes only when the coverage tile moved, or
— in ray mode — the observer height byte moved by more than five, or, in sprite
mode, the quantized shape index changed. Removal is branch-specific, because the
stored coverage byte means the emitter height in one branch and the shape index
in the other `[03 R-VIS-01 §2]`.

A live `LOSType`, `LOS`, `Mapping`, or `NowISee` command uses `RefreshMode`,
not `SetMode`: it always refills eligible current grids, refills history only
for Mapping and NowISee (including a repeated NowISee), and skips every observer
mutation while current coverage is disabled. Circular mode directly stamps each
defined unit under the target raster. The ray branch clears only its saved byte,
retains the saved pair, and enters the normal `<=5` low-emitter throttle; the
saved pair is a sprite frame origin after a Circular transition, while a ray
record holds its terrain tile. The cleared current grids dispose of old coverage
without attempting a retirement through the new raster `[03 R-VIS-01 §1]`
`[03 R-VIS-01 §2]` `[07 R-CAM-01 §6]`.

The observer record itself is built session-side: the unit's world height raised
to at least one above sea level and narrowed to a signed word, plus the model
top as the low byte of the definition's reference height, clamped into a byte;
the two branches then apply *different* shears to reach the coverage tile
`[03 R-P0-18-A §1]` `[03 R-P0-18-A §2]` `[03 R-VIS-01 §2]`.

**The predicate** (`predicate.go`). `IsVisible` is the single gameplay gate:
owner bypass, cloak early-out, the below-sea-level rejection with its status-bit
exemption, then up to four projected samples. `VisiblePoint` (projectiles),
`VisibleExtents` (features) and `AudiblePoint` (positional audio) are the reduced
forms. Gameplay asks through these rather than reading a grid — the audience
gate wants one bit, so the service answers with one bit and the grids stay
private; the raw accessors exist for the publication copy and for diagnostics
`[03 §3.2]` `[03 §8.3]` `[03 R-LAYER §1]`.

`VisibleExtents`'s two-corner form belongs to the world composer's feature
draw gate (`featureVisibleForFrame` in `internal/client/world_draw.go`), keyed
additionally to `nodrawundergray` and the plot placer nibble `[03 §5.1.5]`.
The minimap contacts pass's second walk over the projectile/feature list is a
*different* consumer: it admits each candidate — feature or projectile alike,
from one shared, kind-agnostic list — through a one-point sample at the
candidate's own projected position, with owner-local identity as the only
bypass; it carries neither the `nodrawundergray` nor the placer-nibble term.
`internal/session/publish.go`'s `radarFeatureVisible` and `radarPointVisible`
therefore both call `VisiblePoint`, never `VisibleExtents` — the projectile
form, not the feature-draw form — and the friendly-contact status pair `0x300`
is a term of the minimap's UNIT-pass blip gate only, never of this second pass
`[03 §3.9]`.

`Session.ViewingOwner` supplies visibility and the published
`Frame.ViewingPlayer`; `Session.LocalOwner` and `Selection.LocalPlayer` retain
command ownership. Entry and restore initialize both from the true-local
player. The queued `View` command changes the observer without invalidating
fog or mapped surfaces. Publication includes observer identity in its retained
coverage-copy key; ordinary visibility writes still own cache invalidation.
HUD resources and footer consume the committed viewing player, while command
pages keep the true-local owner `[07 R-CAM-01 §6]` `[I6]`.

**Sensors** (`sensors.go`). `SensorTick` runs five walks in order over an
immutable per-unit view, mutating only each unit's runtime status word: clear
and friendly marking; radar and sonar emission from the viewing player's own
active units; jam emission from everyone else's; the minimum-cloak proximity
scan against the source side's primary candidate list; and the seen probe. It
rasterizes nothing, consults no alliance row, and draws no random numbers
`[03 §3.4]` `[03 R-VIS-01 §4]` `[03 R-VIS-01 §5]` `[03 R-VIS-01 §6]`.
The session schedules this call in the viewing player's due settlement block,
after settlement gates, using `TickPlayer`'s returned deadline verdict.
Between due passes, stored sensor status remains unchanged; LOS publication
still runs on each eligible player entry `[03 R-SENSOR-01]`.

**Ordered sensor broad phase (PERF-REND-03 implementation contract).**
`Service.SensorTick(tick, playerCount, units)` keeps its public API and owns
any reusable candidate scratch. Build that scratch from the supplied immutable
position snapshot at each due call; do not borrow a movement index whose update
phase differs. Candidate queries return input indexes in their original order
(the production caller supplies ascending unit slots), without duplicates or
omitting any candidate admitted by the existing exact distance predicate.
Keep all five passes, emitter gates, stale primary membership, callback tests,
deadlines and publication at their existing owners [03 R-VIS-01 §4–§5].

The internal index may use the established 128-world-unit cell size. It is an
optimization of candidate enumeration, not a replacement distance metric.
Fractional product truncation and signed coordinate/radius wrapping can admit
points outside a naive geometric square. Prove conservative query bounds;
use the exhaustive walk when those bounds cannot be proved for an input.
No public tuning switch, new dependency or simulation map iteration is needed.

The implementation rebuilds a 128-unit grid from the supplied positions and
uses it only while each raw coordinate span is at most signed-32-bit maximum,
the radius is non-negative and no greater than 32767, and the grid is both
bounded and sparse enough to repay an ordered merge. In that domain the
raw-square visitor cannot wrap: an admitted point has each raw axis delta
strictly below `(radius + 1)` whole world units, so the expanded square is
conservative. Queries merge cell lists back into input-index order before the
exact predicate. A failed condition, including a compact population, chooses
the original exhaustive input walk.

Acceptance compares status words, suppression deadlines and published snapshots
with the exhaustive production baseline across authored fractional, boundary,
wrapped-coordinate/radius, dead, stealth, owner and stale-membership cases.
Measure sparse and dense 500- and 1000-unit populations, including index rebuild
and warm scratch reuse; report allocation and timing without extrapolating to
whole-battle performance. A measured decision to retain an exhaustive path for
a workload is valid; a changed verdict is not an optimization.

**Fog state** (`fog.go`). `FogCache` holds two per-cell channels of 0..15 built
from the authoritative stores — channel one from the viewing player's byte grid
when current coverage is enabled, channel zero from the word grid's history bit —
each value a four-bit corner accumulation, with a variant index derived from cell
parity plus the camera phase `[03 §3.3]` `[03 R-RR16-A §1]`. `RebuildFogWindow`
sizes the cache to the camera viewport plus a one-cell border; `RebuildFog`
builds it map-aligned. The cache never writes the word mask.

The four conditional border fixups that close the map's own edge are anchored to
the **map** border, not to the cache window: cell row −1 and column −1 are the
void lines the seeding reaches, and row `H−1` and column `W−1` are the last
in-map lines. Retail names them by window position because its window overshoots
a crossed edge by exactly one cell; ours carries a wider border, so the window
form put every fixup on a line no tile seeds and the north and west borders drew
partial cloud art instead of the solid unexplored fill `[03 §3.3]` "Map-edge
propagation".

**Publication of the mask.** At the end of each sub-tick the session copies the
mode-selected coverage into the committed frame: the word grid always, the local
player's byte grid only when current coverage is enabled, plus the fog channels
and the sensor snapshot. Presentation samples that committed copy and nothing
else `[03 §2.4]` `[03 R-VIS-01 §8]` [I6].

**Presentation revisions (PERF-REND-04).** `Service.MappingVersion` names the
immutable mapping input selected for the local viewer. It advances only when a
local LOS raster changes a relevant grid, when the selected local slot or mode
changes, or when a bulk rebuild replaces the stores. `Service.FogVersion` names
completed derived fog-cache bytes and advances only after a rebuild, with mode
and local-player changes invalidating the cache first. The two frame-buffer
slots retain their own copied mapping and fog bytes across `Reset`; the session
copies a source only when that slot holds an older revision from the same
presentation-only service identity. Thus a committed
frame never aliases mutable visibility storage, while an unchanged LOS state
does not pay a second map-sized copy on alternating frame slots. Camera is a
presentation input to fog operations and does not advance either mapping
revision `[03 §2.4]` `[03 §3.3]` `[03 §3.6]` `[03 R-VIS-01 §8]`.

## 3. Contracts

### 3.1 Terrain and placement — W1…W13

These are the contracts the world plan numbered C1…C13; they keep their order
under `W`-prefixed headings so they cannot collide with the visibility numbers
that Go comments cite bare. A comment reading `PLAN_04 C6` is W6 here.

**W1 — one coordinate helper set.** Every world↔cell/tile conversion goes
through `coords.go`, which floors with a sign correction; no caller writes the
shift inline `[03 §2.1]` [I3].

**W2 — version gate.** Only the two header words are accepted and anything else
is rejected with a diagnostic. The legacy attribute-record width is not decoded
as the canonical four-byte record, and header slots are resolved per version
because a slot's meaning differs between them `[03 §2.2]` `[fmt tnt]` [I9].

**W3 — per-version header sources.** Legacy takes its wind bounds and its
gravity word from its own header, and the OTA overrides do not apply to it; its
gravity integer converts through the same per-tick expression as the OTA key.
Canonical takes wind and gravity from the mission globals, and its minimap and
flag words from the canonical slots `[03 §2.2]` `[03 R-TERR-01 §1]`
`[03 R-TERR-01 §6]`.

**W4 — the OTA override and what "absent" means.** On a canonical map a
non-negative authored `wind`/`gravity` is used as authored. A key that is merely
*omitted* from a parsed global header is not absent: the parser stores the key's
own zero default, which passes the non-negative test exactly like an authored
value, so a canonical map that omits `gravity` runs at gravity zero. The
fallbacks — wind 100/2000, the per-tick equivalent of authored gravity 112, and
tidal one half — apply only to a negative authored value or to a map with no
parsed global header at all `[03 §2.2]` `[02 R-MAP-01 §6]` `[fmt ota]`.

**W5 — tile map.** Tile indices form a `cellW/2 × cellH/2` row-major `uint16`
array and each index selects a 1024-byte 32×32 block `[03 §2.2]`.

**W6 — plot expansion.** Every attribute record expands to one 13-byte plot cell
in row-major order through the single expansion path. The height byte and the
feature word (including every sentinel) are preserved verbatim; the floor
maximum and minimum are derived over the 2×2 neighbourhood with edge guards; the
metal byte is left for the schema seed; the fringe anchor offsets are left for
the feature stamper; the two occupancy shorts are zeroed; the flag byte is
stamped with the map-load placer value, preserving the live-instance,
structure-yard, never-seen and residual bits `[03 §2.2]` `[03 R-TERR-01 §1]`
`[fmt tnt]`.

**W7 — height and slope.** `HeightAt` is integer bilinear over four neighbouring
heights using the low four bits of each cell-space coordinate with the signed
right-shift bias — never a float lerp — and reproduces retail's guard, returning
the raw −1 sentinel on the last row or column instead of clamping. The footprint
validator aggregates the minimum of the cells' low bytes and the maximum of
their high bytes over the sampled cells, selects the movement class's slope or
water-slope limit by the site's water state, and compares strictly so equality
passes `[03 §2.3]` `[04 §6.1]` `[04 R-SLOPE-01 §1]`.

**W8 — three queries, not one.** `CoarseHeightAt` and `LOSHeightWord` are
separate queries and must not be substituted for `HeightAt`; a tall feature does
not raise the LOS word, only terrain data does `[03 §2.3]`
`[03 R-TERR-01 §4]`.

**W9 — sea level.** The header sea-level byte is compared in world units as
`byte × 65536` `[03 §2.2]`.

**W10 — yard-map control bytes.** The ten control bytes are fixed, and their
per-cell bits drive the structure-yard mark, occupancy, slope sampling, height
sampling, feature-free, blocked-class and geothermal requirement. The parse is
retail's character loop, not a one-to-one fill: a character outside the table
advances the string without consuming a cell, the final character repeats for
every unfilled cell, characters past the last cell are never read, and a
building with no yard map takes the all-open default. Only building-class
definitions carry a yard map at all `[04 §6.2]` `[05 "Geothermal requirement"]`
(SC19).

**W11 — geothermal.** The requirement is the yard bit validated against the
covered cell's *resolved* feature carrying the definition's geothermal flag,
satisfied by at least one such cell, with no registry. The validator is
read-only: a vent persists beneath its plant and survives the plant's
destruction with no restore step `[05 "Geothermal requirement"]`.

**W12 — metal.** The per-cell metal byte is seeded uniformly from the mission
schema's surface-metal scalar on a canonical map, or from the legacy attribute
record on a legacy one, and is then raised under indestructible metal-bearing
features by the deposit pass — the only writer after the seed. An extractor
samples `Σ(cell byte + 1) × extractsmetal` over its footprint **once at
placement**, stores the rate, and never resamples; off-map cells of a partly
off-map rectangle contribute nothing while the in-bounds cells still accumulate;
the sixteen-bit accumulator is also handed to the unit's script. A feature's own
metal field is a reclaim reward, not extractor yield. Sampling before the schema
seed is an error, not a plausible zero `[05 "Terrain metal extraction"]`
`[05 R-PROD-01 §6]` `[05 R-FEAT-01 §7]`.

**W13 — the never-seen marker is presentation state.** The plot flag bit is
stored and round-tripped, and no simulation path gates on it `[03 §3.3]`.

### 3.2 Visibility — C1…C16

These keep the numbers the visibility plan gave them, because
`internal/visibility` cites them bare as `[Cn]`.

**C1** The word grid is `(cellW/2) × (cellH/2)` `uint16` cells with ten usable
player bits; the byte grid is one `uint8` refcount per player per cell of the
same dimensions `[03 §3.1]`.

**C2** The mode bit selects the raster shape only; **both** paths write the same
word mask and the same byte refcount. Both start from `q = floor(radius/32)` by
signed floor division. Sprite-mask forms `idx = clamp(q − 5, 0, 9)` into the ten
authored visibility-mask frames; terrain-ray forms `g = clamp(q, 0,
numtables − 1)` into the **declared** table count and then walks `TABLE g − 1`,
so a sight distance in `[32(k+1), 32(k+2))` walks `TABLE k`, the highest
reachable table is `numtables − 2`, and group 0 reads a record whose content is
Unknown — an empty line list there is the sanctioned divergence `[03 §3.2]`
`[03 R-COMP-02 §1]` (SC9).

**C3** Sprite-mask publication clips start-inclusive/end-exclusive, skips
negative origins to `max(0, −origin)`, compares bounds unsigned so a signed
underflow cannot wrap into border cells, and touches only mask bytes unequal to
the transparent sentinel `[03 §3.2]`.

**C4** Accumulation is idempotent: a cell receives the owner's slot bit only
when absent, and the word writer never decrements. The byte refcount increments
and decrements with plain `uint8` wrap and no saturation `[03 §3.1]`
`[03 §3.2]`.

**C5** Terrain-ray: the origin cell is admitted unconditionally; each spoke step
bounds-checks unsigned before any terrain read; the retained numerator/distance
pair starts at `(−1, 0)` per spoke and step distances count from one; admission
is `retainedNumerator × stepDistance < candidateDifference × retainedDistance`,
strictly, so an exact tie never admits. The candidate difference comes from the
LOS word's low byte; the high byte is then tested with the identical comparison
and only then does the retained pair advance. Authored quadrant offsets are
absolute positions from the observer, expanded by four rotations, with axis
spokes duplicated where the table carries both orientations `[03 §3.2]`
`[03 R-VIS-01 §3]` `[03 R-P0-18-B §1]`.

**C6** Refresh throttle: terrain-ray recomputes only when the coverage tile X or
Y changed or the observer height byte moved by more than five; sprite-mask
substitutes a changed quantized shape index for the height test. On a refresh
the old footprint is removed first — current coverage must be enabled, and in
the ray branch the stored height byte must have been nonzero, a guard the sprite
branch does not carry — an out-of-bounds new origin stores an empty footprint
and returns, and only then does the new raster publish `[03 §3.2]`
`[03 R-VIS-01 §2]`.

**C7** Full rebuild: with history disabled the word grid fills all ten bits set,
otherwise zero; with current coverage disabled every player's byte grid fills
with one, otherwise zero. Then every active footprint republishes and the stored
footprint table is replaced, so no stale record can throttle a republication
`[03 §3.2]`. Live commands use a separate refresh contract. It always resets
current coverage. With current coverage enabled, Circular mode reconstructs
each defined observer footprint; terrain-ray mode clears the saved byte then
re-enters the ordinary throttle, so an unchanged saved tile with an emitter
byte in `0..5` may remain unstamped after the reset. With current coverage
disabled, it preserves observer fields and performs no immediate unit raster.
Removed units do not republish. `LOSType` and `LOS` preserve word-grid history,
while `Mapping` and `NowISee` refill it from the new mapping bit before
publication `[03 R-VIS-01 §1]` `[07 R-CAM-01 §6]`.

**C8** The predicate evaluates in this order `[03 §3.2]`:
1. owner identity bypass — the queried player record equals the unit's owner ⇒
   visible, ahead of the cloak test, so a player always sees its own cloaked
   units;
2. the hidden/cloaked instance bit ⇒ not visible. The decloak timer is not a
   visibility-predicate bypass;
3. the first hull probe's height below sea level — the header byte in world
   units, not zero ⇒ not visible unless the completed sensor phase's runtime
   sonar status bit is set. The direct unit adapter starts that probe at the
   definition box's min X/max Y/min Z, so a model whose top crosses the surface
   is not rejected as fully submerged;
4. each sample projects `u = X >> 5`, `v = (Z − (Y >> 1)) >> 5` on the signed
   map-pixel components, bounds-checked unsigned, and tests the mode-selected
   source: any nonzero byte in the queried record's grid, or the word grid at
   the **local** player's bit;
5. the four hull samples **accumulate** one coordinate triple — centre, then
   east by the X extent, then north by the Z extent with the Y extent
   subtracted, then west by the X extent again — so the figure swept is a
   rectangle in projected space, not a diamond about the base. Any admitted
   sample ⇒ visible.

**C9** **Ally vision is never OR'd.** The writer sets only the source unit's own
slot bit, every reader tests one bit, and allied owners hold distinct player
records so the owner bypass cannot fire cross-owner `[03 §3.2]`
`[03 R-VIS-01 §7]`.

**C10** Cloak is a predicate early-out, never a mask edit. The exception is the
proximity breach within the authored minimum cloak distance, compared as a
squared horizontal distance, which is written by the sensor phase as a status
bit and a deadline rather than by editing coverage `[03 §3.2]`
`[03 R-VIS-01 §6]`.

**C11** Radar, sonar and jammers never author the word mask. The sensor phase
rasterizes nothing: its contact callbacks are one-line status-bit writers walked
by a radius visitor, and the arbitration between passes is their write order —
jam clears the seen or sonar bit and sets the jam bit, and the seen probe runs
afterwards, so line of sight restores a jammed unit's seen bit in the same tick.
The minimap's sensor circles are drawn by presentation `[03 §3.4]`
`[03 R-VIS-01 §4]` `[03 R-VIS-01 §5]`.

**C12** The sensor phase runs **only when more than one player is active**; in a
one-player session the status bits keep whatever construction gave them. The
radius visitor searches the larger of the two authored distances, unbonused,
while the elevation bonus enters the squared radar radius only; the two contact
comparisons are strict and the visitor's own distance test is inclusive. It
subtracts signed raw 16.16 coordinate words, takes each square's high 32 bits,
and adds those two terms at signed 32-bit width; it does not truncate each axis
before multiplying.
Definition stealth suppresses radar and sonar outright; the proximity pass
writes the decloak bit and a deadline ninety ticks ahead; the final pass sets the
seen bit from the mode-selected test. The activation bit gates emission with a
closed writer census, so several stock definitions author a sensor range that is
dead data in retail as well `[03 §3.4]` `[03 R-VIS-01 §4]` `[03 R-VIS-01 §9]`.

**C13** Fog is presentation state. The cache never writes the word mask and
rebuilds when the cache-valid bit clears. Channel one accumulates the viewing
player's byte grid only when current coverage is enabled; channel zero
accumulates the word grid's history bit unconditionally; each is a four-bit
corner accumulation where 0 is transparent and 15 is solid, and 1..14 index the
value minus one into two four-way variant families keyed by cell parity plus the
camera phase, channel one rendering first `[03 §3.3]` `[03 R-RR16-A §1]`.

**C14** The never-explored marker is the plot flag bit: set when a hidden
feature is skipped, cleared when drawn, and set immediately for tall features
`[03 §3.3]`. See "Not implemented" below.

**C15** Only a **local player** cell change clears the fog-cache-valid bit and
wakes the composer; a remote player's change dirties nothing `[03 §3.2]`.

**C16** Post-load, the rebuild runs **before** the serialized mapping word grid
is installed, and every restored unit publishes its footprint synchronously
before the loader returns, so no empty-coverage frame can escape. The mapping
box carries history no observer can regenerate, so it is written on top of the
rebuild fills and underneath the observer publication `[03 §3.3]`
`[08 R-ENTRY-01 §7]` `[08 R-SAVE-02 §12]`.

### 3.3 Not implemented

* **C14, the plot-flag never-explored marker.** The set/clear helpers exist and
  the flag byte round-trips, but no production path writes it: presentation
  answers "never explored" from the fog cache's channel-zero solid value at the
  object's tile instead, and an invalid or missing fog view fails closed. The
  tall-feature immediate case has no writer at all `[03 §3.3]`.
* **Terrain-ray group 0.** A sight distance below 32 selects a record whose
  content is Unknown; the implementation admits the origin cell and no spoke.
  Every stock definition authors a sight distance of at least 55, so no shipped
  unit reaches it `[03 R-COMP-02 §1]`.
* **Allied sensor sharing.** The friendly pass's allied disjunct is implemented
  as never firing, because no writer anywhere sets the option bit it would gate
  on — an established absence, not an omission `[03 R-VIS-01 §7]`.

## 4. Divergences

* **SC6 — fringe-anchor encoding.** The two documents read the same two bytes as
  signed offsets and as absolute cell coordinates; absolute coordinates cannot
  address the reference install's widest maps, and the stamp-time writer
  contract settles it. Both readings are exposed as named accessors, the
  signed-offset reading is what `ResolveFeature` follows, and overlapping stamps
  are last-write-wins.
* **SC9 — `LOS.TDF` declares nine tables and supplies twelve.** The terrain-ray
  clamp uses the **declared** count, never the number of parsed sections; the
  catalog keeps all twelve so nothing is lost, and the sprite-mask path keeps
  its own, unrelated count from the visibility-mask GAF.
* **SC19 — yard-map parsing and the bit 5/6 flag identities.** Forty-six stock
  yard maps disagree with their own footprint, so the parser fills by retail's
  character loop and returns no length error; bit 5 blocks on the feature
  definition's authored blocking flag rather than on any feature's presence, and
  bit 6 reads the indestructible flag rather than the reclaimable one. Rejecting
  the mismatch left stock buildings permanently unplaceable.

Two behaviours once recorded as divergences are established retail and are now
contracts, not departures: the void and playable-inset derivation (§2.1,
`[03 R-TERR-01 §2]`) and `HeightAt`'s −1 sentinel on the map's last row and
column (W7, `[03 R-TERR-01 §4]`).

## 5. Research map

| Behaviour | Owning research |
|---|---|
| Coordinate hierarchy, cell/tile sizes, the signed floor | `[03 §2.1]` |
| TNT consumption, version gate, header slots, plot expansion, sea level | `[03 §2.2]`, `[03 R-TERR-01 §1]`, `[fmt tnt]` |
| Map-global block: sources, conversions, defaults | `[03 R-TERR-01 §6]`, `[02 R-MAP-01 §6]` |
| Feature-name resolution and the miss policy | `[02 R-MAP-01 §8]` |
| Void strips, the row-above rule, playable insets | `[03 R-TERR-01 §2]` |
| Terrain deformation does not exist | `[03 R-TERR-01 §3]` |
| The three height queries and the sentinel each caller gets | `[03 §2.3]`, `[03 R-TERR-01 §4]` |
| Height byte into the slope test; the footprint aggregate rule | `[04 R-SLOPE-01 §1]`, `[04 §6.1]` |
| Terrain classification and footprint/yard maps | `[04 §6.1]`, `[04 §6.2]` |
| Build footprint anchor versus model centre — distinct values | `[04 §6.3]`, `[04 R-P0-02]` |
| Placement footprint legality, the inline terrain-check mode | `[04 §6.4]`, `[04 R-P0-08]` |
| Yard bit 0: the structure-yard mark and the known-site gate | `[04 R-P0-08-B §1]` |
| The mobile footprint validator and the two occupancy planes | `[04 R-COLL-01 §2]`, `[04 R-COLL-01 §4]` |
| Factory exit spots run the inline gates | `[04 R-FAC-02 §5]`, `[04 R-FAC-02 §6]` |
| Geothermal requirement, control-byte roles, the yard parse | `[05 "Geothermal requirement"]` |
| Metal extraction: the seed, the sample, the accumulator | `[05 "Terrain metal extraction"]`, `[05 R-PROD-01 §6]` |
| Metal-deposit features raise the cell byte; the feature stamper | `[05 R-FEAT-01 §7]`, `[05 R-FEAT-01 §3]` |
| Mapping array, the rectangle-plus-ring stamp | `[03 R-TERR-01 §7]` |
| Wind: the scheduled redraw, its draw order, the published scalar | `[01 §7.3]`, `[03 R-WIND-01]`, `[01 R-CORE-01 §4.4.1]`, `[01 R-CORE-02]` |
| World picking, the cursor inverse | `[07 §8]` |
| Visibility storage, the mode word, ten player bits | `[03 §3.1]`, `[03 R-VIS-01 §1]` |
| Sight shape, both rasters, the horizon rule | `[03 §3.2]`, `[03 R-VIS-01 §3]` |
| LOS stamping: observer record, throttle, which publisher runs | `[03 R-VIS-01 §2]` |
| Observer emitter height and model-top provenance; coverage-tile shear | `[03 R-P0-18-A §1]`, `[03 R-P0-18-A §2]` |
| Terrain height-word polarity, the scatter, the tail blend, build lifetime | `[03 R-P0-18-B §1]`, `[03 R-P0-18-B §2]`, `[03 R-P0-18-B §3]`, `[03 R-P0-18-B §4]`, `[03 §3.5]` |
| One-based LOS table accessor; eyeball expiry through the byte grid | `[03 R-COMP-02 §1]`, `[03 R-COMP-02 §2]` |
| The word grid's consumer census — every non-presentation reader | `[03 R-LAYER §1]` |
| The sensor phase's five passes; the seam is inside the fifth tick phase | `[03 §3.4]`, `[03 R-VIS-01 §4]`, `[03 R-SENSOR-01]` |
| The radius visitor and the three contact callbacks | `[03 R-VIS-01 §5]` |
| Cloak, stealth, the decloak deadline | `[03 R-VIS-01 §6]` |
| Allied sensor sharing: the bounded absence | `[03 R-VIS-01 §7]` |
| What the sensor and LOS phases publish to presentation | `[03 R-VIS-01 §8]`, `[03 §2.4]` |
| The dead sensor definitions and the activation-bit writer census | `[03 R-VIS-01 §9]` |
| Fog, unexplored edges, the two-channel cache | `[03 §3.3]`, `[03 R-RR16-A §1]` |
| Fog and sensor circles on the minimap (presentation) | `[03 §3.8]`, `[03 §3.9]`, `[03 §3.10]` |
| Positional-audio audience gating uses the reduced predicate | `[03 §8.3]` |
| The seen set as the secondary targeting candidate list | `[06 R-WPN-02 §6]` |
| Visibility and mapping rebuild at battle entry; the eyeball producer | `[08 R-ENTRY-01 §7]`, `[08 R-SESS-01 §3]` |
| The metal, plotmap and mapping save boxes | `[08 R-SAVE-02 §12]` |

## 6. Not implemented and open

Markers in these packages:

* `TODO(T23)` in `internal/world/plot.go` — plot flag bit 7 is preserved by the
  stamp mask but has no isolated reader or writer; bits 0–6 are established
  `[03 §2.2]` `[03 §3.3]` `[03 R-TERR-01 §1]`.

Open questions carried by the contracts above rather than by a marker:

* Group 0 of the terrain-ray table — what retail's one-based accessor reads
  before the table list — is Unknown; the empty line list is the sanctioned
  divergence and is unreachable on stock content `[03 R-COMP-02 §1]`.
* The fog cache's corner-to-bit assignment is a supported inference pending an
  asymmetric probe `[03 §3.3]`.
* How retail partitions a live-anchor overlap between two feature footprints is
  unresolved; the stamper's dense-pack teardown and last-write-wins ordering is
  what the established stamp service states `[05 R-FEAT-01 §3]`.
* The word grid's universal semantic name is deliberately not established, so
  the code calls it the word mask and never `explored` or `radar`; its consumer
  census is closed `[03 §3.1]` `[03 R-LAYER §1]`.
