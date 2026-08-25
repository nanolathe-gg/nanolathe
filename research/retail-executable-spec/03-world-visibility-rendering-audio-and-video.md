# Retail executable contract: world presentation, visibility, audio, and video

This document is a clean-room presentation contract derived only from static
analysis of the retail executable. It omits executable addresses, memory
offsets, and raw decompiler identifiers. **Established** means direct static
instruction or data-flow evidence; **supported inference** identifies a
reasonable composition whose final detail is not proven; **unknown** is left
for further executable analysis.

Its subject areas are the renderer and its passes, terrain grids and
deformation, visibility and radar, font and interface drawing, audio mixing and
music, and video playback and capture.

## 1. Presentation boundary and frame model

Retail presentation is an 8-bit indexed software renderer with two output
backends: a GDI windowed path and an exclusive DirectDraw path. Both consume a
software framebuffer and palette. The frame composer reads current simulation
state and cached visual state; it does not advance authoritative simulation.
Simulation and presentation are connected by the main pump, not by a proven
render worker thread.

The renderer’s frame work is staged through exactly **ten fixed-order effect
strips** inside the single frame composer, each closed by a barrier call with
the pass number 0 through 9. The contract is the numeric order together with
each pass’s gate; semantic pass naming beyond that order is cosmetic. In
numeric order:

1. Terrain/static preparation, minimap/radar preparation, and viewport clip,
   unconditionally.
2. Strips 0, 1, and 2, unconditionally.
3. Screen-Y bucket build, then the first map-cell/object traversal — the
   feature pass, which owns the unexplored-marker logic of section 3.3.
4. Strips 3 and 4, unconditionally.
5. Intervening unit traversals under mixed internal predicates.
6. Strip 5, unconditionally.
7. Strip 6, the projectile pool, the fixed effect pool, strip 7, and the
   remaining unit auxiliary-draw traversal — all gated on the composer’s
   render-mode argument being nonzero.
8. Strip 8 draws **always**, outside the render-mode gate.
9. A key-controlled overlay under its key predicate; then unit labels (each
   gated on an options byte and on the labeled owner equaling the local
   player slot) followed by strip 9 under the render-mode argument; then two
   optional mode overlays under option bits and the same argument.
10. Fog presentation under the render-mode argument, after all ten strips,
    projectiles, and effects but **before** selection/interface work;
    selection rectangle, interface, diagnostics, and present prep close the
    frame under local predicates.

Consequences a reimplementation must preserve: projectiles and effects sit
between strips 6 and 7 and are not strip objects; strip 8 is unconditional;
fog covers everything world-drawn below it but never selection or interface.
Units reach these loops through per-row screen-Y bucket insertion (bucket row
computed from projected screen Y, appended in enumeration order), so paint
order is Y-sorted rows with in-row enumeration order — there is no depth
test.

**Strip storage and lifecycle.** Each strip is a vector descriptor; a draw
dispatcher forwards each stored object to its draw entry, and an update
dispatcher evaluates removal BEFORE update for every object, destroying and
stably compacting on a positive verdict so survivors keep their order. A
terminal condition created during an update is noticed only on the next
invocation. Producers append at the end; when the pre-insert count exceeds
400 the oldest object is destroyed first, so steady state holds at most 401
records per strip and same-strip order among survivors equals insertion
order.

**Fixed effect pool.** Effects are not strip objects: a separate fixed pool
holds up to 300 fixed-size effect records, and appends at or above the cap
allocate nothing. Rendering walks the whole pool once per embedded animation
category and again for model-bearing records, each draw guarded by buffer
admission. Its tick integrator advances velocity against gravity, can restore
a prior position and invert/halve vertical velocity on terrain/water contact
or clear a record’s model pointer, single-steps both embedded animation
players, clears non-looping sequences’ pointers at termination, and removes
emptied records by stable left compaction within the same updater call — an
animation terminating during its step retires its record that same call,
unlike generic strip objects.

## 2. World coordinates, terrain grids, and projection

### 2.1 Coordinate units

The terrain uses a hierarchy of 16-bit attribute cells and 32-pixel tile
blocks:

- one attribute cell represents 16 by 16 map pixels;
- one tile contains 32 by 32 indexed pixels and covers four attribute cells;
- world coordinates use 16.16 fixed-point, so one map pixel is 65,536 world
  units;
- a cell is therefore 16 times 65,536 world units, and a tile is 32 times
  65,536 world units.

Cell and tile division uses signed, floor-like shifts with a sign correction
before shifting for negative camera/world coordinates. This prevents truncation
toward zero from moving the left/top edge by one cell.

### 2.2 TNT map consumption

The loader accepts exactly two TNT versions and rejects any other version word
with a diagnostic and resource-failure path. Version `0x1020` is legacy: it
reads gravity, minimum wind, and maximum wind from header slots 13, 10, and 11;
its minimap offset is slot 14 and its minimap-present flag is slot 15 bit 0;
and it selects the legacy attribute path. Version `0x2000` is canonical: it
hard-codes gravity 0, minimum wind 100, and maximum wind 2000; its minimap
offset is slot 10 and its flag is slot 11 bit 0; and it selects the four-byte
attribute path. An authored nonnegative OTA `wind` or `gravity` value overrides
the terrain value only for canonical maps; legacy maps retain their own header
values. When neither source supplies gravity the engine falls back to the
constant `0x1FDB`; tidal strength falls back to `0.5`.

The tile map is a row-major array of 16-bit tile indices with dimensions
`cellWidth/2` by `cellHeight/2`. A tile index selects a 1,024-byte block (32 by
32) in the indexed tile set. The tile blitter computes source block plus
intra-tile pixel remainder, clips at map bounds, and handles partial edge
rectangles. It does not use a depth buffer or a textured water mesh.

The loader expands each canonical attribute entry (four bytes: height,
feature `uint16` little-endian, and a zero unknown byte) into a 13-byte plot
cell in row-major order. Document 02 carries the typed layout and sentinel
table. In plain terms, each 13-byte cell holds:

* occupancy words at the first four bytes (two `uint16` mobile planes, zeroed at
  load then stamped per building occupancy);
* height byte at offset 4;
* derived minimum and maximum heights at offsets 5 and 6 (maximum at 5, minimum
  at 6, recomputed over the 2×2 neighbourhood; average is the coarse floor
  query);
* metal byte at offset 7, seeded uniformly from the mission `SurfaceMetal`
  scalar — every cell receives the same signed byte, no per-cell raster;
* feature word at offset 8 with quaternary sentinels: `0xFFFF` empty, `0xFFFE`
  fringe (follow signed offsets), `0xFFFD` void (engine map-edge strips and
  lava-world fill), and `< 0xFFFB` live feature index; `0xFFFB`/`0xFFFC` behave
  as void because consumers test `< 0xFFFB` before dereferencing;
* signed anchor offsets at offsets 10 and 11: the Z delta is the width-scaled
  byte and the X delta is the unscaled byte, both `int8` `-128..127` from fringe
  toward anchor; out-of-range stays zero and leaves the fringe unresolved; at
  the anchor the same two bytes hold the live instance slot index while attached
  or the accumulated blast damage otherwise — never simultaneous;
* flag byte at offset 12, stamped at load as `flags = (flags & 0xD7) | 0x50`
  (preserve bits 0,1,2,7; clear bits 3 and 5; set bits 4 and 6), carrying bit 0
  live instance present, bit 1 building occupied, bit 2 never-seen fog, bits
  3–6 placer nibble (map load passes 10), and bit 7 preserved with no isolated
  reader (`TODO(T23)`).

Fringe-anchor offsets are signed, not absolute coordinates. The retail corpus
contains maps up to 402×408 cells, which needs 9 bits to name an absolute
coordinate, so an 8-bit absolute field could not address the board — only signed
offsets fit. The Z byte is scaled by map width when forming the anchor address
(the row stride multiplies it); the X byte is not. The resolver follows the
signed offsets when the feature is `0xFFFE`, bounds-checks the anchor, and only
returns the anchor's feature when that anchor word is `< 0xFFFB`; otherwise the
fringe remains not-found (blocking for yard-occupancy bit 5, non-satisfying for
geothermal bit 7). No hidden map sections or separate flood-fill geometry exists
beyond this array — a bounded writer census found only the expansion zero and the
derived stamp. Merged fringe blobs and about 5,283 orphaned fringe cells need a
non-local partition rule; the row-major left/above later-wins heuristic that
resolves 83.2% of the 71,916 fringe cells across 275 maps is retained as
`TODO(question)` — full retail partition is not established. Declaration
footprints alone resolve 65.1%.

Void and edge generation runs after the full-map minimum/maximum recompute and
after feature placement. Right columns `Width-2` and `Width-1` are set to
`0xFFFD` for every row unconditionally. Playable insets `PlayRight = WidthPixels
- 32` and `PlayBottom = HeightPixels - 128` are set at that time and gate the
camera clamp. When the mission `lavaworld` flag is set, a bulk sweep sets
`0xFFFD` for every cell where `hmin ≤ SeaLevel` and the feature word is
`0xFFFF` or `0xFFFE`. North and south height-dependent void strips beyond the
right two columns are observed but the exact predicate is `TODO(question)`.
Outside the map rectangle, height returns sentinel `-1` with unsigned
candidate bounds before any terrain read; movement is blocked for generic modes
and allowed only for factory-exit search mode 2; the LOS writer stores an empty
footprint and returns; projectiles do not collide with terrain; and the camera
remains clamped to the playable insets.

Per-cell metal is uniform: every cell's metal byte is seeded from the single
mission `SurfaceMetal` value. The TNT unknown byte is zero corpus-wide and is not
a metal source, and no `Width × Height` metal raster is allocated. An extractor
at placement sums `unsigned(metalByte) + 1` over its footprint and multiplies by
its `extractsmetal` scalar; the stored result is never resampled. Feature metal
is reclaim reward only. North/south void edge height predicates and any varying
per-cell metal file beyond the uniform byte remain `TODO(question)`.

Sea level is copied from the map header as a byte and is compared in world
units by multiplying by 65,536. Water/lava map state and minimum/maximum water
depth/slope thresholds are cached for placement and impact decisions.

### 2.3 Height queries

The world-owned height query samples four neighboring plot-cell heights and
performs bilinear interpolation using the low four bits of each cell-space
coordinate. Signed interpolation uses a right-shift bias (`(val>>31 & 0xF) >>4`)
so negative differences round consistently, and the four corner reads are
guarded by `cx+1 < Width` and `cz+1 < Height` checks that return the sentinel
`-1` when out of range. A separate coarse average `(hmax + hmin) >> 1` over the
two derived bytes is used by some placement/airborne tests; it must not be
substituted for the bilinear query everywhere. Height at offset 4 is the raw
corner sample; `hmax` at offset 5 and `hmin` at offset 6 are the derived 2×2
neighbourhood maximum and minimum that feed the coarse query and the LOS
aggregation.

The LOS writer uses a different, coarser height representation. It quantizes
to 32-pixel visibility tiles and reads a lazily rebuilt `uint16` word per
visibility tile (see section 3.2), aggregated from the terrain heights — not the
four-corner bilinear query. The aggregation is supported inference as
low byte = minimum and high byte = maximum over the four attribute cells that
make one visibility tile, because the low byte gates admission and the high byte
gates horizon advance; the exact derivation formula is `TODO(question)` until the
lazy rebuild is fully traced. A tall feature does not raise the LOS ray height
unless its terrain/plot data itself changes.

### 2.4 3DO model hierarchy

A 3DO object piece has a 52-byte header with vertex/primitive counts, selection
primitive, signed 16.16 parent translation, name, vertex/primitive arrays, and
sibling/child links. Sibling and child links form a depth-first hierarchy.
Vertices are three 16.16 coordinates; primitives contain color, vertex-index,
texture-name, and colored/texture flags.

Model loading resolves object names from unit catalog data, sorts the catalog by
case-insensitive name, and caches model pointers per unit type. The sort makes
piece/type identity independent of provider enumeration order. N-gon primitives
are expanded into triangles by a fan-like operation. Leaf pieces with a vertex
but no primitive are valid attachment/emit points.

After relocation and before any draw, each object reorders its primitives at load
time: if the object declares a selection primitive, that primitive record is
swapped with primitive zero and the selection index is rewritten to zero; the
remaining primitives from index one upward are then bubble-sorted into ascending
order of the integer mean of their vertices' second coordinate. A separate
recursive pass then negates the first and third vertex coordinates and the first
and third parent translations of every object in the hierarchy, a half-turn about
the vertical axis applied to the whole model. Draw order within a piece is
therefore fixed at load time, not recomputed per frame, and a per-frame sort
does not reproduce retail tie order.

**Piece transform composition.** There is no matrix stack and no per-frame
matrix build anywhere in the model path: the renderer applies ordered in-place
rotation+translation passes over vertex arrays, ancestors applied after
descendants, so a leaf vertex receives exactly the chain product

```
world(v) = M_root * ... * M_leaf * v        with        M_i = T(t_i) * R_i
```

— each piece rotates about its own origin FIRST, then translates. The
per-node translation is the componentwise sum of the piece’s script
translation lanes (32-bit 16.16 values) and the model’s authored parent
translation. Rotation composes three unsigned 16-bit accumulators (65,536
units per circle) applied chronologically about Z, then X, then Y:

```
Rz: x' = c*x - s*y ; y' = s*x + c*y      (pair x,y)
Rx: y' = c*y - s*z ; z' = s*y + c*z      (pair y,z)
Ry: x' = c*x - s*z ; z' = s*x + c*z      (pair x,z)
theta = angle * 2*pi / 65536 ; results rounded to nearest integer
```

All three axes share this one rotation template; reproduce the formulas
verbatim rather than adopting a named clockwise/counterclockwise convention.
Rendering evaluates this trigonometry in floating point with round-to-nearest
integer conversion — **not** through the fixed-point trig tables, which serve
simulation velocity integration only. TURN, turn-now, and SPIN converge on
the same accumulators through one script adapter, so there is a single angle
per axis and the last writer wins; no separate aim-versus-spin stage exists.

Residuals: frames sample the accumulators exactly as committed at the current
tick — no interpolation between updates exists in the draw path — and a
vestigial per-piece rebuild-gate counter is never observed holding a nonzero
value, so every dirty frame rebuilds each reachable piece from pristine
coordinates through its full ancestor chain.

### 2.4.1 Model rasterization — face dispatch and shading

The per-unit rasterizer projects every piece vertex with the section 2.5
projection, then walks pieces and primitives with these established rules:

1. **Selection plate exclusion.** When a piece declares a selection
   primitive (swapped to index 0 at load), the primitive loop starts at
   index 1 — the plate is never drawn as a model face. Unit picking is a 2D
   bounding-box test elsewhere and does not consult the mesh.
2. **Flat-colored faces are quads only.** An untextured primitive renders
   solely when its vertex count is exactly 4; untextured triangles and n-gons
   draw nothing. The fill takes the resolved color byte with **no SHD
   shading** — flat colors do not vary with face orientation (confirmed by a
   two-normal 3DO probe). A flat quad carrying the team-color flag
   combination fills through the unit's LOGOS frame with a per-player shade
   byte from the player record instead.
3. **Textured faces** render through the scanline mapper for any vertex
   count, sampling the texture through `PALETTE.SHD` at a row derived from
   geometry:

```
row = trunc( dot(N, L) * 5.0 ) mod 32
L default = (-0.8, 1.0, 0.25)      (user-settable light direction)
N = per-vertex smooth normal:
    face normal = normalize(cross(v[b]-v[a], v[b]-v[c]))
      over the polygon's first three vertex indexes (0-based a,b,c);
    degenerate faces (any two equal indexes) use (0, 1, 0);
    vertex normal = average of the normals of all faces touching the vertex
```

   The row interpolates across the face with the corners. Rows are palette
   remaps, not brightness ramps (row 15 identity; row 0 near-black; row 31
   saturated; intermediate rows shift hue per entry). COB `dont-shade` pins a
   piece to row 15. The light direction is read from three settings as
   integers scaled by 0.01 and written through a dedicated setter that then
   rebuilds the shadow caches.

4. **Texture resolution at load**: each primitive's texture name resolves
   case-insensitively against the side's texture GAF set, then a fallback
   set. A miss rewrites the primitive to flat color `0xd1` (a gray placeholder
   quad, subject to the quads-only rule). One-frame entries are static;
   multi-frame entries become animated textures driven by per-instance
   players ticked once per simulation frame with per-frame delays from the
   GAF table — except exactly-10-frame entries, which are the LOGOS team
   textures: never animated, frame selected by owner player at draw time.

### 2.5 Orthographic screen projection

The established beam/line projection is orthographic and integer based:

```
screenX = (worldX >> 16) - cameraX + 128
screenY = (worldZ >> 16) - ((worldY >> 16) >> 1) - cameraZ + 32
```

Equivalent paths use the same world-to-pixel scale and a half-height shear.
There is no perspective divide, depth buffer, or distance-based line width in
the observed beam renderer. Camera scroll and edge scrolling are integer state;
camera speed is separately configurable.

## 3. Visibility, LOS, radar, and fog presentation

### 3.1 Visibility storage

Retail separates terrain, mode-dependent mapping and sight state, feature
memory, and radar:

1. Terrain tiles are always available to the tile pass.
2. A mode-dependent word grid stores one player bit per 32-pixel coverage or
   mapping tile. Its allocation is exactly *cellWidth* times *cellHeight*
   divided by two bytes, indexed as little-endian 16-bit cells on a grid half
   the cell dimensions in each axis — one cell per 32 world pixels — with ten
   usable player bits. Exactly one routine sets bits in it, and both of the
   per-owner byte-grid routines are separate from it. Its universal meaning is
   not established: its initialization and its consumers are mode-dependent, so
   calling it current line of sight, explored memory, radar, or occupancy would
   overstate the evidence. A separate per-player byte grid stores overlapping
   current-sight coverage as a reference count, incremented and decremented by
   two dedicated routines.
3. Plot cells are primarily terrain and feature-occupancy records. Their flag
   byte carries an instance-present bit, a placement blocker, a
   never-explored/fogged marker, and a placer nibble stamped at feature
   placement time that presentation reads back as an owner-memory accept
   (section 3.3); it is distinct from the mapping grid and is not a general
   current-LOS store.
4. Radar/sonar uses minimap surfaces and blip lists, not the gameplay LOS bitset.

A mode word governs which raster and which predicate are active. Bit 0
selects history versus always-visible mapping, bit 1 selects the byte-grid
predicate versus the word-mask predicate, bit 2 selects sprite-mask versus
terrain-ray raster, and bit 3 marks the terrain-word cache dirty. The
byte-grid path treats any nonzero `uint8` count as visible; its increment is a
plain wrapping `uint8` add with no clamp (256 overlapping observers wraps to
zero and reads as fogged) and its decrement is the matching plain subtract. The
word path stores one `uint16` per visibility tile with ten usable player bits;
its update is an idempotent bit OR — a cell receives its owner's bit only when
absent, never decremented, rebuilt by reset instead. The global word map is
reset and rebuilt when the visibility mode or map state requires it. This
establishes that the word grid gates a form of visibility or mapping without
proving one universal semantic name. The byte grid is rebuilt by walking
current sight sources.

### 3.2 Sight shape and terrain occlusion

Sight distance quantizes differently in the two raster algorithms selected by
mode-word bit 2. In sprite-mask mode the sight radius quantizes to
`floor(radius / 32) - 5`, clamped into the authored visibility-mask shape
range; the selected shape supplies width, height, anchor offsets, a
transparent palette sentinel, and row-major mask bytes. In terrain-ray mode
the radius quantizes by signed division by 32 **without** the -5 offset and
clamps into the parsed LOS.TDF table range. Both paths write the same word
mask; bit 2 only changes which shape is ORed in.

**The sight-shape table is an authored GAF resource.** The sprite-mask shapes
are not synthesized: the engine holds a handle to the visibility-mask GAF file
`anims/vismasks.gaf` and indexes the entry named `vismask` by the quantized
value. **Established:** that GAF entry carries ten frames with sides 11, 13,
15, 17, 19, 21, 23, 25, 27, and 29, each anchored at the frame centre. A frame's
opaque (non-transparent) pixels are the covered tiles; its width, height, anchor
offsets and transparent palette index are the shape fields. This GAF binding is
not a first-match search over multiple candidates — the ten-frame handle is the
authoritative shape table. An older file `anims/vismask.gaf` exists in the
archive but is a distinct cursor set and is not the table that the shape fetcher
counts; its frame counts (22 entries, varying opacity) do not match the counted
ten-frame table.

**Quantization is common, then biased differently.** Both rasters start from
the same floor `q = floor(sightdistance / 32)` computed with signed floor
division. Sprite-mask mode then forms `idx = clamp(q - 5, 0, nsMask-1)` where
`nsMask` is the ten-frame count; terrain-ray mode forms `g = clamp(q, 0,
nsRay-1)` where `nsRay` is the declared LOS.TDF table count. The `-5` bias is
therefore sprite-only.

**The -5 is an index bias, not a radius reduction.** Shape *k* has radius
`k + 5` tiles, so the subtraction that selects the frame is undone by the frame
geometry: a unit whose `sightdistance` quantizes to index *k* covers `k + 5`
tiles, i.e. `floor(radius / 32)` tiles. Reading the index as the radius shrinks
every unit's sight by five tiles. The clamp is into `0 .. ns-1` where `ns` is the
shape count carried by the resource, and radii below the first shape clamp up to
index 0 rather than publishing nothing.

The terrain-ray group index is the unbiased `q` clamped into `0 .. nsRay-1`
over the parsed LOS.TDF tables, and the spokes walked for that group are that
table's authored line list — line counts grow with the table index (a radius-9
table carries fourteen lines, radius-10 sixteen). LOS.TDF declares
`numtables = 9` but ships twelve table sections; the clamp uses the declared
nine and the three excess tables are unreachable authoring residue.
Neither raster uses a synthesized circle or a fixed spoke set.

**Terrain height word for the ray.** The LOS reader does not use the per-cell
`hmax`/`hmin` at per-step granularity; it reads a dense `uint16` word per
visibility tile (`TileW × TileH` words, two bytes per tile) that was rebuilt
lazily from the terrain heights. The low byte is tested for admission and the
high byte is tested to advance the retained horizon — identical strict
comparisons, but the low byte decides whether the cell is seen and the high byte
decides whether the horizon rises. The aggregation that produces those two bytes
is **supported inference** as low = minimum and high = maximum over the four
attribute cells that form one visibility tile, because only a minimum can let
sight through a partially blocked tile and only a maximum can occlude behind it;
the exact builder formula is `TODO(question)` until the lazy rebuild is fully
traced. The rebuild is triggered when the cache-valid mode bit is clear and
fills the whole word array at once.

**Spoke geometry.** Each LOS.TDF line is expanded into four mirrored quadrants
by 90-degree rotation. Whether authored offsets are absolute from the observer
or cumulative along the spoke is `TODO(question)` — pairs cohere as absolute
offsets after rotation, but the retail stepper's stride interpretation remains
open.

**Jammer separation is closed.** The sensor phase's jammer circles are drawn onto
separate radar-presentation surfaces that are wiped each tick and never affect
the gameplay LOS word mask or the per-player byte grids. Radar, sonar, and
jammer presentation never authors the LOS mask.

**The per-player byte grid's increment has no upper clamp.** It is a plain
byte increment with no comparison against 255, so a cell covered by 256
simultaneous observers wraps to zero and reads as fogged. The decrement is the
matching plain decrement; the word mask is never decremented and is instead
reset and rebuilt.

Sprite-mask publication clips start-inclusive/end-exclusive: right/bottom
ends clip to the half-resolution grid bounds, negative left/top origins skip
to `max(0, -origin)`, every bounds compare is unsigned so signed underflow
cannot wrap into border cells, and only mask bytes unequal to the transparent
sentinel touch the mask or its reference state. Accumulation is idempotent:
a cell receives its owner’s player-slot bit only when absent — the writer
never decrements. When any cell changed for the LOCAL player it clears the
fog-cache-valid mode bit and wakes the presentation composer; remote players’
changes dirty nothing locally.

In terrain-ray mode the origin cell is admitted unconditionally before any
spoke is walked. Each spoke step bounds-checks the candidate cell (unsigned,
before any terrain read) and admits it only when its height-relative slope
STRICTLY exceeds the retained horizon slope — equality fails. Exactly, with
the observer height byte clamped to 0..255, a retained numerator/distance
pair initialized to (-1, 0) per spoke line, the candidate difference taken
from the LOW byte of the aggregated two-byte terrain word, and step distances
counted from one: admit iff

```
retainedNumerator * stepDistance < candidateDiff * retainedDistance
```

(the implementation tests inequality-from-zero first, so an exact tie never
admits). After admission, the HIGH byte of the same terrain word is tested
with the identical strict comparison against the same retained pair; only
then does the pair become that high-byte difference and step distance.

Footprint refresh is throttled: in terrain-ray mode nothing is recomputed
unless the coverage tile X changed OR tile Y changed OR the observer height
byte moved by more than 5 since the stored footprint. On refresh the old
current footprint is removed (only when current coverage is enabled and the
old height byte is nonzero), the new origin/height is stored, an out-of-bounds
new origin stores an empty footprint and returns, then current coverage
publishes and history accumulates. Sprite-mask mode refreshes on tile X, tile
Y, or a changed stored quantized-radius byte instead.

A full rebuild refills both stores from scratch: when the history mode is
DISABLED the word grid fills with all-bits-set cells (otherwise zero); when
the current-coverage mode is DISABLED every eligible player’s byte grid fills
with 1 (otherwise zero). It then republishes all active units’ footprints and
wakes presentation.

**Gameplay visibility predicate.** One four-point gate serves unit auxiliary
draw preparation, tick-time enumeration into target buckets, and weapon
targeting — targeting queries with the attacker’s own owner identity, so the
self bypass admits same-owner targets. The query argument IS a player record,
not a raw pixel grid. Evaluation order:

1. Owner identity bypass: when the queried record equals the candidate unit’s
   owning player record, visible immediately — you always see your own units.
2. Instance-state early false: the hidden/cloaked instance bit in the unit’s
   instance-state byte returns not-visible at once.
3. Base point: center plus definition extents in 16.16 world units. Unless
   the unit’s 32-bit runtime status field carries the underwater-exemption bit
   (mask 0x200), a base height below sea level returns not-visible. Because
   the sensor phase’s friendly marking sets that same bit on owned and allied
   units (section 3.4), those units are implicitly exempt. **Sea level here is
   the map header byte scaled to world units — the same `byte × 65,536`
   comparison as section 2.2, not a comparison against zero.**
4. Each sample projects with the half-height shear (`v = (Z - (Y >> 1)) >> 5`,
   `u = X >> 5`, pixel components) and unsigned bounds against the queried
   record’s grid dimensions; the mode-selected source is that record’s
   current-coverage byte grid (any nonzero byte visible) or, otherwise, the
   word grid tested at the LOCAL player’s bit.

   **"Pixel components" is load-bearing.** Each 16.16 world coordinate is
   first narrowed to its signed 16-bit high word — the map-pixel component —
   and the shift by five is applied to *that*. Shifting the 16.16 value
   directly is wrong by a factor of 65,536, and the narrowing to a signed
   16-bit quantity is itself part of the contract: coordinates beyond ±32,768
   map pixels wrap rather than saturate.
5. Hull diamond: center, then east (definition X extent added), then north
   (definition Z extent added, half-height subtracted from the height), then
   west (X extent subtracted again). Any admitted sample returns visible.

   **The four samples accumulate; they are not independent offsets from the
   center.** One coordinate triple is carried through all four tests and each
   step mutates it, which is why step four subtracts the X extent "again":

   | sample | X | Y | Z |
   |---|---|---|---|
   | 0 center | `X` | `Y` | `Z` |
   | 1 east | `X + ex` | `Y` | `Z` |
   | 2 north | `X + ex` | `Y - ey` | `Z + ez` |
   | 3 west | `X` | `Y - ey` | `Z + ez` |

   The height decrement `ey` is its own definition field, distinct from the Z
   extent `ez`; the two are not the same value and neither is half of the
   unit's height. The resulting quadrilateral is a rectangle in projected
   space, not a diamond centered on the base point — the historical "hull
   diamond" label describes the sampling order, not the figure.

The remaining consumers apply equivalent tests rather than calling this gate:
weapon placement/order validation inlines a word-grid-first reject plus the
same mode-selected source test at the projected cell; projectiles use the
one-point form; feature drawing uses a two-corner form over footprint
extents; the sensor phase’s final pass inlines a single-point test.

**Ally semantics are closed: never OR’d.** The writer ORs only the source
unit’s own player-slot bit into each cell, and every reader tests only the
local player’s bit. No routine merges an alliance group into a cell before
test, and allied owners hold distinct player records, so the owner bypass
cannot fire cross-owner. Allied vision sharing does not exist through this
mechanism; the only residual question is whether some unresolved identity
path shares grids by other means.

**Cloak is a predicate early-out, not a mask edit.** Cloaking does not erase or
dim the LOS mask; the visibility predicate returns not-visible for cloaked
units until an exception applies — most notably proximity breach within the
cloaking unit's authored minimum-cloak distance (`mincloakdistance`, the
definition field whose stock-typical values compare squared horizontal distance
against it). Stealth and init-cloaked definition flags feed the same predicate
state.

Radar, sonar, and jammers never author this mask: the sensor phase rasterizes
range and jam circles onto separate presentation surfaces that are wiped each
tick, while the LOS mask persists.

### 3.3 Fog and unexplored edges

The frame composer draws terrain tiles before visibility gates. Explored terrain
therefore remains as tile art under fog. Unit, feature, projectile, and other
sprite/model passes use the hard visibility predicate and are either drawn or
skipped; no observed intermediate opacity is applied at the LOS edge.

The edge is aligned to 32-pixel visibility tiles. GAF transparency is binary
RLE skip/opaque copy, not a fog blend. No observed LOS path indexes the 256 by
256 ALP blend table. The SHD table and dither option affect shadow/lighting
passes, not a soft fog edge. DitheredFog is therefore not permission to add
checkerboard fog to the LOS mask.

Unexplored map borders/voids can remain black where no valid tile blit reaches
the backbuffer. The clean-room evidence is medium for the exact distinction
between an in-map void tile and an out-of-map clipped region, but high that
visibility culling itself is binary and hard-edged.

The mapping word grid is serialized in a save blob; the transient byte sight
grid, dirty flags, eyeball queue, and radar surfaces are not all serialized.

**Two-channel fog cache.** A dirty-triggered composer wakes on the
presentation dirty bit, clears it, and rebuilds the fog/minimap byte surfaces
by sampling the local player’s history bit and mode-selected current grid; it
is a pure consumer and never writes the word mask. The final overlay lazily
rebuilds a two-byte-per-cell cache (pointer, width, and height held in engine
root state) whenever its cache-valid mode bit is clear, aligning the cache to
the camera in 32-pixel cells including signed residues. Per cell:

- Channel zero == 15: fill the whole 32x32 cell with the default dark palette
  entry; nothing else is processed for that cell (short-circuit).
- Else channel one == 15: solid dark fill, or — when the options-storage
  dither bit is set — a patterned/checker fill seeded with camera parity
  (`(cameraX + cameraY) & 1`).
- Else channel one in 1..14: GAF frame `value - 1` drawn from a four-way
  variant family selected by `(cellX + cellY + cameraPhaseSum) & 3`, blitted
  plain or parity-seeded patterned per the same option bit.
- Then channel zero in 1..14: frame `value - 1` from a second four-way family
  via the plain blitter. Channel one therefore renders BEFORE channel zero.

This overlay sits at the compositor position after all strips/effects and
before selection/interface (section 1). Residual: the engine-side conversion
that PRODUCES the cached channel values from history/current coverage —
including what gradient values 1..14 encode and the aggregated terrain-word
table feeding sight shapes — is unresolved; retail’s hard fog edge must not
be softened to improve image metrics.

**Unexplored-versus-fogged mechanism (adjudicated).** The plot flag byte’s
0x04 bit marks a cell never-explored/fogged. The composer’s feature pass
clears it per cell before evaluating; a skipped hidden feature leaves it set,
and a cell whose anchor feature exists with feature height >= 10 is re-marked
immediately — tall features cast a permanent unexplored shadow over their own
cells independent of current line of sight. Drawing them is instead decided
each frame by the acceptance tests below, admission under the strict horizon
rule effectively requiring observation from ground high enough to see over
the intervening terrain. Pass two redraws a fogged cell’s feature only when
the feature quick-accepts or the two-corner predicate admits; otherwise the
marker stays set.

Full flag-byte semantics (write model verified; this adjudication SUPERSEDES
the older occupied-visited/LOS-level-nibble reading of the same byte, which
had no supporting writer):

- Bit 0 — live feature-instance present: set exactly when the feature stamper
  allocates an animation slot, cleared by teardown, and read by the feature
  reproduction walker as “no live instance”.
- Bit 1 — authored no-build/placement blocker.
- Bit 2 — the never-explored/fogged marker above.
- Bits 3..6 — placer nibble, written at stamp time as `(placer & 0xF) << 3`
  with bits 0,1,2,7 preserved. Map load stamps placer value 10 everywhere;
  corpse stamping passes the dying unit’s OWNER PLAYER SLOT, so your own
  wrecks carry your slot while map-authored features (nibble 10) can never
  match a real slot. The composer accepts a fogged feature for drawing
  without a line-of-sight test exactly when `(flags >> 3) & 0xF` equals the
  local player slot, for features whose definition carries the memory-accept
  flag.
- Bit 7 — unobserved.

**Post-load visibility timing (exact).** The visibility rebuild runs BEFORE
the serialized Mapping blob is read. That rebuild prefills both stores —
history cells all-set when history mode is disabled, current grids 1 when
current mode is disabled — and republishes any units already present. Unit
reconstruction then publishes each reconstructed unit’s footprint
synchronously to every mode-enabled store before the loader returns: there is
NO empty-coverage first frame. A missing or size-mismatched Mapping blob
leaves the array unchanged while publication proceeds. The earlier possibility
that current-sight coverage could stand empty for one frame/tick after load
is RETRACTED; a deferred next-frame rebuild must not be implemented.

### 3.4 Radar and sonar

The radar picture is built from terrain height data or an optional baked minimap.
Its aspect ratio preserves the map shape with a fixed long side. When generated,
the renderer supersamples to twice the radar dimensions, maps each output sample
back to map/tile coordinates, reads height, and chooses a palette value from the
terrain radar table. It then creates radar-picture, mapped, and final surfaces.

Each tick, radar blips are projected from unit/world coordinates into radar
coordinates. Radar and sonar range circles, jammer circles, and weapon-range
circles are rasterized onto the radar surface using distinct palette colors.
The radar-mapped surface is wiped and rebuilt each tick, while the authoritative
LOS mask persists untouched by this path. The exact effect of jammers on
authoritative contact state remains incomplete and must not be inferred solely
from the minimap drawing path.

**Sensor and proximity phase.** The per-tick sensor phase runs only when more
than one player is present. An ownership/status first pass walks the indexed
unit list, clears the decloak-timer status bit for every unit, then sets the
friendly status bits (mask 0x300) on own units and alliance/sensor-qualified
units while clearing that bit group otherwise. Active units with either a
radar or sonar distance defined emit ONE geometric circle whose outer radius
is the LARGER of the two distances; nonzero radar-jam and sonar-jam distances
each emit their circles through two further, separate callback tables — three
callback tables in all. Circles rasterize onto backing surfaces whose
dimensions are held in engine root state; positions project at a shift of 23,
one surface cell per 128 world units. The phase itself never writes the word
mask.

Minimum-cloak proximity: qualifying cloaked units search the indexed unit
list by squared planar distance up to their authored minimum-cloak distance;
a hit writes a decloak deadline of current tick + 90 into the struck unit and
sets its runtime status bit 0x1000. Final visibility pass: walking world
units, those neither already carrying the seen-marker nor holding the hidden
instance bit are projected with the standard half-height shear and tested
through the mode-selected source — the local player’s current byte grid when
enabled, else the word grid at the local bit; admission sets runtime status
bit 0x100, the per-frame “seen” marker. Status-field roles: 0x100 =
seen-marker, 0x300 = friendly contact (whose upper bit doubles as the
underwater-rejection exemption of section 3.2), 0x1000 = decloak timer.
Residual: arbitration among the three sensor/jammer callback tables — what
overlapping marks write to the backing surfaces — remains open.

## 4. Indexed renderer, palettes, and asset layers

### 4.1 GDI windowed backend

The windowed path obtains a window DC, creates a compatible memory DC, and
creates a top-down 8-bit `CreateDIBSection`. Rows use a four-byte-aligned stride
`(width + 3) & ~3`. The software renderer writes indexed pixels to the DIB
backbuffer.

For presentation, it obtains the window DC, calls `SelectPalette` and
`RealizePalette`, copies the memory DC with `BitBlt` using `SRCCOPY`, and
releases the DC. Palette installation creates a 256-entry Windows logical
palette and calls `SetDIBColorTable`.

### 4.2 DirectDraw fullscreen backend

The fullscreen path calls `DirectDrawCreate`, sets cooperative level on the game
window, requests the configured width/height at 8 bits per pixel, creates a
primary/backbuffer surface, attaches/installs a palette, composites into the
surface, and presents with DirectDraw surface blit/flip calls. The observed
surface descriptor is the legacy 108-byte form with primary/backbuffer flags;
exact flag naming and all lost-surface recovery are medium-confidence.

Both paths share the indexed software framebuffer and serialize present work
through the renderer’s global MAIN lock. There is no observed 16/24/32-bit
fallback in the retail renderer.

### 4.3 Palette tables and color indirection

`PALETTE.PAL` is 1,024 bytes: 256 entries of four bytes (RGB plus zero
reserved byte) and is the shared active display palette for the indexed
renderer, including GUI/HUD surfaces. `GUIPAL.PAL` is an authored frontend
source palette used to build a GUI-to-display lookup; it is not installed as
the physical UI palette. The auxiliary tables are:

- `ALP`: 65,536 bytes, a 256-by-256 nearest-color blend table;
- `LHT`: 8,192 bytes, 32 rows by 256 entries for brightening;
- `SHD`: 8,192 bytes, 32 rows by 256 entries for shading/darkening.

No table has a header; identification is by size alone. Each of `LHT` and
`SHD` is loaded as a raw 8192-byte block and is retained for the life of the
session. Only `PALETTE.PAL` participates in the `LOGPALETTE`/`CreatePalette`/
`SetDIBColorTable` install; `ALP`/`LHT`/`SHD` never enter the OS palette and
are consulted only by the indexed software renderer via a single byte lookup.

The palette install helper copies RGB entries, creates a `LOGPALETTE` with
version 0x300 and 256 entries, and installs either DirectDraw palette entries
or the GDI palette/DIB color table. A 256-byte logical-to-physical lookup maps
TDF/UI indices into the live `PALETTE.PAL` display palette. During GUI
bootstrap, `GUIPAL.PAL` is copied into a separate window record and its RGB
entries are compared with all 256 display entries using the sum of absolute
channel differences; the first entry on a tie wins. That map resolves GUI
semantic color fields before GUI primitives or FNT glyphs are written. It is
not applied to image bytes: the retail GAF blitter copies opaque frame bytes
directly, and PCX/TNT bytes are likewise already indexed for `PALETTE.PAL`.
Every final indexed pixel is resolved to RGB only at present time through
`PALETTE.PAL`.

#### 4.3.1 LHT brightening

**Layout and formula.** `LHT` is 32 rows × 256 columns, row stride 256:

```
result = LHT[level * 256 + index]      level 0 .. 31 clamped, index 0 .. 255
```

`index` is the source palette index after the logical-to-physical map;
`result` is again a palette index. The engine evaluates a single byte load —
no channel arithmetic and no interpolation between rows.

**Table shape (established, measured against the retail tables).**

* Row 0 is near-identity: 242 of 256 entries map to themselves; the 14
  exceptions are isolated duplicates near palette gaps. Mean luminance delta
  is effectively ±0.0 at row 0 and rises monotonically to +51.51 at row 31.
  White (255) maps to white and black (0) maps to black at every level.
* Brightening is monotonic and nearest-color: each row remaps every source
  index to the palette entry whose RGB is nearest the brightened color, not
  to `src + level*step`. The top rows therefore collapse many sources onto the
  same bright band (row 31 maps sources 1–6 onto 249–254, the bright
  orange/yellow band).

**When it is used (established).** `LHT` drives exactly one presentation
family — the lit-ground halo drawn around an explosion and around muzzle
flashes. The engine precomputes a small square flash texture (`N×N` plus a
24-byte header) whose bytes encode intensity — the inner core varies around
index `0x6F` minus a jittered radial distance, a thin ring is exactly `0x6E`,
and outside the disc the byte is `0xFF` transparent — then for each screen
pixel where that texture is opaque the underlying indexed pixel `src` is
replaced by `LHT[level * 256 + src]` at a level derived from the disc
intensity. The halo is composited after the flat tile pass and before shadows,
units, and fog; its whole-tick countdown cadence is shared with the explosion
animation, and the disc itself is seeded from the CRT presentation random
stream (`*214013+2531011`), not the simulation stream. The effect is
presentation-only, not authoritative, not hashed, and not save/loaded.

`LHT` never darkens; darkening is through `SHD` rows 0–14. The exact
`discByte → level` mapping and any multi-tick fading envelope are not
established and remain presentation tuning; the 32-row/256-column layout, the
near-identity row 0, the +51.51 bright end, and the exclusive flash binding
are direct. The two brightening ramps overlap: `LHT` row 3 and `SHD` row 16
both lift mean luminance by +6.83, `LHT` row 5 and `SHD` row 17 both by +12.60,
but the files are distinct and neither is synthesized from the other.

#### 4.3.2 SHD shading

Indexed texture pixels may be passed through an `SHD` row for model face
lighting; flat-colored 3DO primitives and laser lines bypass `SHD`. Team/logo
textures select the player-specific frame before palette/shading lookup. `SHD`
rows 0–14 darken (row 0 near-black, only index 0 survives; mean −97.59),
row 15 is near-identity (232 of 256 self, mean +0.17), and rows 16–31 brighten
past identity to +53.53 at row 31 — a full signed ramp that `LHT` does not
replicate. The exact `SHD` row selection formula is not established, although
the identity mid-row and the existence of 32 rows are direct.

### 4.4 GAF sprites and animation

The GAF loader keeps entries by case-insensitive name and allocates a frame
canvas with a small header plus `width * height` pixels. A frame may be raw or
RLE-compressed. RLE rows use a command bit for transparent skip, a repeat-byte
command, and a literal-copy command. Palette index zero is not inherently
transparent; transparency comes from skip commands.

Subframes are placed at their offsets relative to a parent canvas, clipped, and
composited in order, with later opaque pixels overwriting earlier pixels. The
loader wires named entries for smoke, fire, explosions, water/lava impacts,
shadows, cursors, victory/defeat/pause art, logos, and GUI panels. Optional
entry lookup failure leaves the relevant visual absent rather than inventing a
replacement.

A GAF frame reference is eight bytes: a frame-header offset and a 32-bit
authored duration in whole simulation ticks (or in scaled wall-clock units for
cursor playback). A playback cursor is a 12-byte non-owning view over shared
sequence data: current frame index, countdown, loop-or-hold flag, and entry
pointer. It does not free sequence storage on termination. Binding a cursor
clamps an out-of-range start index to zero, loads that frame's authored
duration into the countdown, and copies the sequence's loop byte. Each
simulation-tick step decrements the countdown; when the countdown is below two
it advances to the next frame, wraps to zero for looping sequences or clears the
entry pointer for non-looping sequences, and loads the new frame's duration.
Multiple simulation ticks in one present advance the cursor multiple times;
a pause with no logical ticks freezes it. A separate delta step subtracts a
signed 16-bit tick delta and can cross multiple frames in one call while
accumulating each newly selected frame's duration. Single-frame entries never
advance. The registered sequence players for model textures are stepped once per
simulation tick. Cursor sequences are stepped from a 30-unit-per-second scaled
wall-clock delta and accumulate authoring countdowns in those scaled units. The
bounded producer that drives model-texture players is the per-tick walker that
single-steps every registered model-texture player; the bounded cursor driver is
the wall-clock delta path that subtracts the scaled delta via the multi-frame
countdown stepper. Multi-frame model texture entries receive per-instance
playback cursors so separately created model instances need not share phase.

## 5. World render passes and object presentation

### 5.1 Terrain and features

The tile pass fetches and clips indexed 32-by-32 tiles. Feature plot cells then
provide anchors, height, and footprint metadata for feature sprites and shadows.
The renderer checks feature height/visibility and either draws the feature/GAF
sprite or sets the explored/fog marker for a later frame. Feature memory may
keep a last-seen sprite when the plot’s team-memory nibble permits it.

### 5.2 Units and 3DO models

Units are bucketed by projected vertical/screen position so that the software
renderer can process them in a deterministic order. Visibility gates are applied
before drawing hidden units. A visible model traverses the 3DO sibling/child
tree, resolves fixed-point piece transforms, triangulates primitives, applies
player/team palette selection, and optionally renders a shadow pass.

The unit catalog’s case-insensitive sort gives stable model/piece indices. COB
piece animation updates state before rendering. Per drawn unit the renderer
compares its cached orientation triple against the unit’s bank, heading, and
pitch; when ANY axis differs by more than 7 angle units it refreshes the cache
and schedules a rebuild. A dirty frame resets affected subtrees from pristine
model vertices and reapplies transforms ancestor-after-descendant (composition
contract in section 2.4). The unit’s bank, heading, and pitch fold into the
ROOT piece’s Z, Y, and X angle slots respectively, composing as the outermost
factor of the chain. Unit position never enters piece math: pieces transform
around the model origin, and position enters only the final screen placement.
Projectile models reuse the identical rotation helper — yaw feeds the Y slot,
pitch the X slot, each with a constant negative half-circle (180-degree)
authored model-facing offset — and a propeller-style variant feeds its spin
angle through the same slot machinery.

### 5.3 Projected shadows and feature shadows

Options distinguish master shadows, feature shadows, vehicle shadows, and a
dithered-fog/shadow option. Unit definitions provide a `noshadow`-like control
and shadow-capability flags. The shadow GAF entry is used for feature/sprite
shadows; model shadows are projected through a separate ground pass.

The established high-level order is: draw the flat terrain, prepare feature
shadow work, draw projected model/feature shadows, then draw visible units and
features. SHD palette rows and optional dither participate in the shadow/light
pass. Projection coefficients, exact shadow footprint clipping, and whether all
shadow categories share one stencil are not established.

### 5.4 Projectiles and laser beams

The projectile pool is a 300-record array. Simulation walks the entry-captured
active span once per sub-tick, marks retired records dead, and performs stable
tail compaction before presentation consumes the resulting pool. Records
appended during the scan are not simulated until the next projectile phase but
are visible to that tail compaction. Document 06 owns the complete lifetime and
burst-scheduling contract.

**Draw gate.** The renderer skips inactive records; each active record passes
one visibility test BEFORE rendertype dispatch: when the mode word enables
per-owner current-coverage byte grids, the projected half-resolution cell must
hold a nonzero current-sight byte in the local player’s grid; otherwise the
same cell goes through the one-point word-grid test at the local player’s bit.
The gate evaluates once per record — not per presentation case.

**Rendertype dispatch.** Dispatch selects presentation from the weapon
definition’s rendertype byte; all eight cases are established:

- 0 — line from the current endpoint to the tail endpoint (the beam geometry
  below). Colors are the definition’s primary and secondary color bytes
  remapped through the live palette lookup; a zero secondary byte draws one
  one-pixel stroke, otherwise two adjacent strokes ordered endpoint-swapped,
  secondary first and primary on top.
- 1 — common base sprite (frame 0 of the shared projectile GAF), then the
  definition’s model oriented from the projectile record; an optional
  secondary model appears while a definition flag is set and the current tick
  precedes the record’s expiry deadline, with directly computed versus
  record-stored rotation chosen by that same flag.
- 2 — a fixed global GAF entry drawn at the projected point; a failed
  draw-buffer admission executes an immediate return that ABORTS THE ENTIRE
  PROJECTILE RENDERER, not just this record — the only control-flow exit
  spanning later records.
- 3 — common base sprite plus definition model through a distinct orientation
  path with a separately built angle block.
- 4 — the definition’s selector byte picks one of five global GAF sequences;
  selector -1 suppresses the branch entirely; frame =
  `(currentTick - spawnTick) mod frameCount`.
- 5 — one fixed global sequence; frame = `frameCount -
  ((expiryTick - currentTick) * frameCount) / (16-bit definition lifetime
  field)`, lifetime-scaled, drawn only while `0 <= frame < frameCount`.
- 6 — common base sprite plus definition model using the orientation stored
  verbatim in the projectile record.
- 7 — two randomized segmented-line passes. Segment count derives from the
  endpoint span divided by the literal constant 327680 (0x50000), skipped when
  zero; each generated point receives integer per-axis jitter of
  `rand() * 11 / 0x8000 - 5` applied to X, height, and Z before each
  sub-segment projects through the standard line path.

Beam geometry is direct evidence:

- project the head and tail with the orthographic formula above;
- if `color2` is zero, draw one one-pixel Bresenham line;
- otherwise draw two parallel one-pixel lines, using color2 as the outer stroke
  and color as the inner stroke;
- do not anti-alias, alpha-blend, perspective-scale, or widen with distance.

The beam simulation keeps the tail fixed while the head advances through its
configured duration, then advances both ends to maintain a constant-length
beam until expiry. Collision/damage is tested at the head on each live tick,
not continuously along the line. A collision queues land, water, or lava impact
effects and sound, then removes the projectile. Expiry without collision simply
removes it; no impact GAF, sound, area damage, or screen shake is generated.

Model missiles use 3DO rendering and a projected shadow. Homing turns by a
per-tick turn-rate limit. The recovered target-loss branch supports an
inference that a missile coasts toward its last known point rather than
immediately reacquiring, but exact dead-target validation and reacquisition are
open. Trail smoke cadence is closed: a trail emitter keeps an additive next-
emission deadline, so a delayed emitter preserves any owed puffs and a
**zero-delay definition emits one puff every tick**; burst parents never emit
trail smoke, and expiry without collision leaves one final trail-style puff for
timer families without the burn-blow flag.

### 5.5 Effects, flashes, nanolathe, and cursors

Impact, smoke, wake, construction, and sequence events append to effect strips
that later rendering reads and compacts. Reclaim/capture nanolathe work emits
one segment every two ticks, while build-assist work emits two segments per
tick. Effect strips evict their oldest record whenever the pre-insert count
exceeds 400, holding at most 401 in steady state (section 1). Explosion flash
discs are precomputed once per
sequence: the per-frame cadence is the frame reference's second word — a 32-bit
tick count stored at the frame-reference array entry (frame base plus index
times eight, plus four) — consumed by the standard countdown cursor, so flash
and explosion animation timing is simulation-tick countdown ticks like every
other sequence family. Build/reclaim direction uses the builder and target
positions; exact color variation and per-segment fade/lifetime are not fully
recovered.

The cursor is software-drawn. Cursor GAF entries are loaded into a table;
`GetCursorPos` and configured hotspots determine placement. The renderer saves
and restores dirty cursor rectangles, changes cursor icon/mode for move, attack,
repair, patrol, build, and other order states, and uses the same logical-to-
physical palette mapping. GUI queue lines/icons are also software-drawn.

### 5.6 Screen shake

Screen shake is presentation, but it is driven from the authoritative impact
dispatcher, so every peer requests the same shakes from the same weapon data.

**Request.** If the preference bit `0x10` is set, the request returns with state
untouched; which authored setting drives that bit is not established. Otherwise
if inactive the two amplitude accumulators are cleared to zero while the
duration accumulator is left unchanged. The new duration is
`trunc((duration + authoredDuration)/2)` with signed truncation toward zero,
`remaining` is set to `duration`, the signed magnitudes are added into the two
amplitude accumulators, and if `duration > 0` active is set. There is no queue,
maximum, or distance falloff. A first request with a zeroed duration state
therefore runs for half its authored duration; later activations blend against
the stale duration left by the prior shake. The sole observed direct caller is
the authoritative impact dispatcher; it passes the same authored magnitude on
both axes.

**Consume**, once per simulation tick, from the camera update that runs after
the projectile phase — so a shake requested during the projectile phase jitters
in the **same** tick:

```
if not active: return
if remaining <= 0: active = false; return
sx = amplitudeX * remaining / duration      (signed, truncating)
sy = amplitudeY * remaining / duration
cameraX += rand() * sx / 0x8000 - (abs(sx) >> 1)
cameraY += rand() * sy / 0x8000 - (abs(sy) >> 1)
remaining -= 1
```

Three consequences matter:

1. The envelope is a **linear decay with uniform white noise**, not a sinusoid
   and not an exponential. The `-abs(s)/2` term centres each axis.
2. The two draws come from the **CRT presentation random stream, not the
   simulation stream**. Shake costs exactly two presentation draws per active
   tick and never touches lockstep state.
3. The jitter is added **in place into the global camera origin** — the same
   integers every renderer subtracts to get screen coordinates. There is no
   separate render-time offset. So the world, fog, selection rectangle, and
   cursor mapping all shake together; and **the displacement is permanent**:
   when a shake ends, the camera rests at a random-walk offset from where it
   started and nothing restores it.

The camera clamp that runs immediately afterwards holds both axes inside the
map, so shake cannot push the view off-map, and the follow camera's
glide-halfway-to-target behavior damps it while tracking a unit.

### 5.7 Construction and water wakes

The construction/nanolathe path uses target footprint and builder position to
build short line segments. Mover bounds produce wake rectangles, which are
filled with a palette tint and suppressed when the relevant fog/visibility gate
is active. Wakes are not a water surface simulation.

## 6. Water, lava, and media-dependent impacts

Strict retail presentation has no independently allocated animated water mesh.
Water is terrain tile art plus wakes, splashes, impact GAFs, underwater height/
visibility behavior, and palette-tinted quads. Sea level is a world/simulation
threshold, not a renderer-only decoration.

Each weapon definition can provide land explosion art, water explosion art, lava
explosion art, and corresponding sound aliases. Impact selection tests map type
and sea-level/height. Missing optional GAF lookups leave the pointer empty and
the impact visual absent without crashing. Water weapons and water damage are
separate definition flags from the visual media choice.

The absence of a water mesh is high-confidence within the current bounded
rendering call census, not proof that every un-decompiled helper lacks one.

## 7. Fonts, text, GUI, and input-owned presentation

### 7.1 FNT software glyphs

The FNT path loads a bitmap font, measures strings, clips/truncates to a width,
and rasterizes glyph rows directly into the indexed framebuffer. Glyph bits are
1-bit, most-significant-bit first; set bits write the current text color. Newline
is byte value 10. The active font and primary/secondary/shadow color state are
software-renderer state.

The renderer therefore does not require GDI `TextOut` for gameplay text. GDI
remains present for window/palette presentation and may be used by shell dialogs;
the current evidence does not prove every UI label avoids GDI. Two-byte glyph
header/layout details and baseline/kerning behavior are medium-confidence.

### 7.2 GUI and HUD

GUI parsing consumes side-data and GAF panels, with a gadget/control structure
and a 640-by-480 shell coordinate system. Battle chrome uses panel-top,
panel-side, panel-bottom, integer GAFs, and per-side data. The HUD has selection,
resource, health, order queue, minimap, and chat/end-game components; these are
software compositions over the indexed framebuffer.

Input is polled through `GetAsyncKeyState`, `GetKeyState`, cursor position/focus
queries, and `SetCursorPos`. There is no DirectInput or raw-input contract.
Clipboard operations use `OpenClipboard`, `GetClipboardData`, and
`GlobalLock`. Edge scrolling compares cursor position with viewport borders and
changes integer camera scroll by configured speed. The complete key-token map,
repeat rate, focus-loss behavior, and all gadget hit-testing are not settled.

## 8. Audio backends and event arbitration

### 8.1 Backend selection

Startup chooses among three established paths:

1. DirectSound via `DirectSoundCreate`, cooperative level, a primary buffer,
   and a PCM format observed as 11,025 Hz, 16-bit, stereo;
2. WinMM `waveOut`/`aux` fallback when DirectSound is disabled, unavailable, or
   reports the allocated-device failure; and
3. Win32 `PlaySoundA` when `UseWindowsSound` is selected. This path is used for
   legacy/frontend or configured Windows sounds and forces DirectSound off.

Initialization failure can fall back to another backend or show a sound-system
error dialog. CD audio initialization is attempted independently.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### 8.2 WAV decoding and cache

The WAV loader accepts the retail raw/DIGI/HSHD/SDAT forms and RIFF WAVE. RIFF
format/data chunks are parsed with the observed odd-padding behavior. Raw audio
defaults to 11,025 Hz, mono, 8-bit when no format header supplies a different
description. Sound samples are resolved through the archive/VFS and cached by
alias.

### 8.3 Sound categories and eight-slot arbitration

The sound-category catalog's authored grammar, its fixed event list, and its
variant-gathering rule are specified in document 02. Each category record is
352 bytes: a name of up to 64 bytes followed by 24 event rows of twelve bytes
each, indexed by event slot, where slot zero is an unused sentinel and slots 1
through 23 are the events. A row holds a variant count and two parallel arrays
of 64-byte strings: the sound alias and its speech caption.

**Static slot table.** Alongside the categories the executable carries one
static record per slot, holding the slot index, a **priority**, a **cooldown in
seconds**, the authored key name, a default speech caption, and a mutable
*next-allowed frame* cache. Priorities and cooldowns are per slot and global
across every unit, not per category:

| Slot | Priority | Cooldown | Key | Default speech |
|---:|---:|---:|---|---|
| 1 | 10 | 0 | `select` | — |
| 2 | 9 | 20 | `underattack` | Under Attack |
| 3 | 4 | 2 | `activate` | — |
| 4 | 4 | 2 | `deactivate` | — |
| 5 | 5 | 1 | `ok` | — |
| 6 | 3 | 4 | `arrived` | Arrived |
| 7 | 8 | 1 | `cant` | Cannot Comply |
| 8 | 3 | 3 | `unitcomplete` | Nanolathe Complete |
| 9 | 4 | 2 | `build` | — |
| 10 | 3 | 1 | `repair` | — |
| 11 | 2 | 1 | `working` | — |
| 12 | 7 | 1 | `load` | — |
| 13 | 7 | 1 | `unload` | — |
| 14 | 7 | 1 | `cloak` | Cloaked |
| 15 | 7 | 1 | `uncloak` | Visible |
| 16 | 4 | 1 | `capture` | — |
| 17 | 10 | 0 | `count5` | five |
| 18 | 10 | 0 | `count4` | four |
| 19 | 10 | 0 | `count3` | three |
| 20 | 10 | 0 | `count2` | two |
| 21 | 10 | 0 | `count1` | one |
| 22 | 10 | 0 | `count0` | zero |
| 23 | 10 | 0 | `canceldestruct` | Self destruct terminated |

**Queue.** Cues do not play directly. They enter an eight-entry queue whose
header carries a count and a base time; each entry holds the slot, the frame it
was enqueued, the unit, an optional override speech line, and the slot's
priority.

**Insert(slot, unit, text):**

1. If the current frame is before the slot's next-allowed frame, drop the cue.
2. If any queued entry already holds that slot, drop the cue — duplicate slots
   never queue twice.
3. If the queue is full, resolve the last entry **silently**, printing its
   speech line but playing no sound, free its text, and shift it out.
4. Insert so the queue stays sorted by descending priority, placing the new
   entry *after* entries of equal priority, which makes equal priorities
   first-in-first-out.

**Resolve(entry, audible, showText):**

1. Look up the acting unit's sound category, then that category's row for the
   entry's slot.
2. Draw the variant uniformly from the row's variant count using the
   fifteen-bit random draw scaled by the count.
3. If audible, the crowding gate `10 - audioThreshold < priority` passes, the
   row has at least one variant, and the sound-enable flag is set, play the
   chosen alias and set the slot's next-allowed frame to the current frame plus
   its cooldown times 30.
4. If showing text and the gate `10 - speechThreshold < priority` passes, take
   the entry's override line or the row's caption for the chosen variant, and
   print it prefixed by the unit name when the unit is still alive.

**Drain**, once per rendered frame and outside the simulation: if the queue is
empty do nothing; if the current frame is within 30 frames of the base time,
resolve the head silently; otherwise resolve the head audibly and reset the
base time. Then free the head's text and shift it out.

Consequences a reimplementation must preserve:

- **At most one voice is audible per 30 frames.** The sorted eight-deep queue
  decides which pending cue wins that window.
- A cue popped inside the window still prints its speech line but is silent.
- Full-queue eviction resolves the *last* entry silently rather than dropping
  it or playing it immediately.
- Cooldowns are per slot and global across all units, and re-arm only on actual
  audible playback, never on a silent resolve.
- Priorities come solely from the static slot table. The two crowding
  thresholds are separate menu-scaled bytes, one for audio and one for speech.

Aliases are registered in a flat cache capped at 255 entries. Unit definitions
map their category names to category identities, with numeric fallback when a
name is absent.

Global master and effects volume is observable through the wave and auxiliary
volume calls. The helper that plays positional world cues adds two established
presentation behaviors on top of that mixer. First, **audience gating**: the
source position is mapped to a cell and tested against the viewing machine's
current mode-selected visibility grid — the per-cell explored bitset when the
mapping history mode is active, otherwise the per-cell current-sight mask for
the local player slot; off-map sources and sources that fail the local gate are
silent locally, and named feature cues are forwarded through the same gate.
Second, **viewport-relative placement**: when the sound backend reports stereo
capability the helper computes viewport-relative horizontal and vertical offsets
and passes them through the mixer together with a reference-center update; this
is screen-relative stereo placement, not world-space 3D positioning and not a
distance attenuation curve. Without that capability a viewport bounds test
chooses full volume (`-585` in the DirectSound centibel-style encoding) for
in-view sources and an attenuated step (`-1585`) for off-screen sources;
off-screen events are quieter, never discarded.

Projectile start, hit, and water sounds are queued synchronously with
projectile creation and collision events. A failed projectile reservation emits
no start event. Collision ordering is: screen shake, hit or water sound,
optional end smoke, selected land or water animation, then damage and area
effect; the visual and audio event is therefore created before the final damage
mutation in the observed path.

Positional world cues can also be emitted as a network packet carrying the alias
identity and position. The bounded direct-caller census shows every in-game
gameplay producer — weapon start and burst, impact, and named-feature ignition
forwarding — passes `broadcast = 0` and arises independently from lockstep
simulation with local audience gating; the inbound dispatcher replays the packet
with broadcast cleared and does not echo. Observed nonzero-broadcast callers are
frontend and options paths. Only dynamically or externally reached callers
outside the bounded direct-call census remain unknown for broadcast behavior.

### 8.4 Music and CD/MCI

Music uses WinMM MCI strings for `cdaudio` open, close, stop, status, play, and
pause. Track probing builds a 100-entry track table and assigns four repeating
track categories (`(trackIndex mod 4) + 1`). Playback modes include idle,
sequential, random, shuffle/history, and single/category selections. Status,
pause/resume, notification, and volume restoration are polled from the main
application activity.

The exact `MM_MCINOTIFY` message handler, failure behavior on a missing CD, and
history persistence across saves are unresolved.

## 9. Smacker cinematics and movie capture

The imports and call census establish Smacker DLL ordinal calls for opening,
decoding, frame access, and teardown, but the ordinal-to-Smack API mapping is
not fully recovered. The cinematic path:

- opens a configured movie and reports “Could not open movie file” on failure;
- prepares a DirectDraw/display path and reports setup failure when that cannot
  be created;
- checks supported pixel format and shows a Smacker Error dialog for an
  unsupported format;
- runs a separate `PeekMessageA`/`TranslateMessage`/`DispatchMessage` loop,
  including quit handling, while frames are consumed; and
- uses window/DC, palette, client-to-screen, and window-position calls around
  playback. A debug string says movies require fullscreen.

The configuration contains `PlayMovie`, `nomovie`, and `Movie Output Rate`.
The capture path writes files named `MOVIE%03i` under the configured image
output directory. A wall-clock dispatcher gates capture at a 30-Hz base divided
by the output-rate setting, and separate capture/encode helpers are present.
Whether capture is raw indexed frames or a secondary encoder output, the exact
Smacker pixel formats, frame timing, dropped-frame policy, palette handoff,
fullscreen transition, and movie/audio synchronization are not established.

## 10. Established facts, supported inference, and confidence

### Established facts

- Terrain cells are 16-pixel, tiles are indexed 32-by-32, and world positions
  are 16.16 fixed-point with the half-height orthographic projection described
  above.
- TNT maps, 13-byte plot cells, 3DO sibling/child hierarchies, GAF RLE/subframe
  composition, FNT software glyphs, and indexed GDI/DirectDraw presentation are
  directly evidenced.
- LOS is quantized to 32-pixel visibility tiles, with a word mask and a
  reference-count byte-grid mode; radar surfaces are separate. Sprite-mask
  sight quantizes `floor(radius/32) - 5` while terrain-ray sight divides by 32
  without the offset; ray admission requires a strictly steeper
  height-relative slope (equality fails), and the terrain word’s high byte
  controls horizon replacement.
- The gameplay visibility gate is established: owner-identity bypass, cloak
  early-out, underwater rejection with the status-field exemption bit, then a
  four-point hull diamond over half-height-sheared tiles; weapon placement
  inlines an equivalent test, and ally bits are never merged.
- The frame composer’s ten-strip numeric order with render-mode gates,
  removal-before-update strip lifecycle, oldest-first eviction past 400
  records (steady bound 401), and the separate 300-record effect pool with
  same-call compaction are established.
- All eight projectile rendertype presentations are established, including the
  whole-renderer abort on case-2 admission failure and randomized segmented
  lines for case 7.
- Fog presents through a lazily rebuilt two-byte-per-cell cache with the
  channel rules of section 3.3; plot-flag semantics (instance-present,
  no-build, never-explored marker, placer nibble) are adjudicated;
  post-load visibility publication is synchronous with unit reconstruction.
- Piece transforms compose as ordered in-place rotate-then-translate passes in
  Z, X, Y chronological order using floating-point trigonometry, with
  bank/heading/pitch injected into the root piece’s Z/Y/X slots; there is no
  matrix stack and no interpolation.
- Fog sprite/model culling is hard and binary; ALP is not used by the observed
  LOS edge, and DitheredFog belongs to shadow work.
- Beam geometry is one or two one-pixel Bresenham lines with fixed orthographic
  projection; collision damage is at the beam head.
- Water/lava impacts select per-weapon GAF and sound media; no independent
  retail water mesh is proven.
- DirectSound, waveOut, PlaySound, eight-slot sound arbitration, MCI CD audio,
  and Smacker cinematic/capture paths are present.

### Supported inference

- The staged effect strips act as a software painter’s pipeline in which later
  opaque layers overwrite earlier indexed pixels, rather than as a depth-buffer
  renderer.
- The byte-grid reference count exists to make overlapping sight circles safe
  to remove one source at a time; the separate word bitset is a compact,
  mode-dependent owner coverage/mapping grid whose universal meaning remains
  open.
- The sound ring is a crowd-control policy intended to keep important events
  audible on a small number of legacy channels, not a general mixer.
- The separate cinematic message loop and DirectDraw setup are a compatibility
  mode that temporarily owns presentation; they are not ordinary game-frame
  rendering.

### Confidence limits

- SHD row selection and the semantic naming of individual strips remain medium.
  GAF frame-duration interpretation for the
  simulation-tick and wall-clock countdown cursors is established (section 4.4);
  sequence-flag naming and families not shown to use that cursor remain medium.
- In-map void rendering versus out-of-map clipped regions is less certain than
  the hard cull;
  tile clipping and persistent backbuffer behavior need focused executable
  analysis.
- Audio buffer flags, category cooldown units, channel field meanings, CD
  notification behavior, and Smacker ordinal names are incomplete.
- The current absence of a water mesh and 3D audio is a bounded negative census,
  not a theorem over unrecovered functions.

## Missing and unknown

### World and visibility

- Meaning of the legacy attribute-byte encodings beyond the canonical four-byte
   attribute stride remains `TODO(question)`; the canonical attribute layout
   (height, feature `uint16`, zero unknown) and the 13-byte plot-cell stride
   are established. Fringe-anchor encoding is established as signed offsets
   (`int8` DX and DZ, DZ scaled by width, threshold `0xFFFB`, width proof to
   402×408 — SC6 resolved); merged-blobs and orphaned fringe partition beyond
   the 83.2% row-major heuristic remains `TODO(question)`. Plot flag bit 7 and
   whether any unexported code writes placer-nibble values into it, and which
   feature-definition flag gates the owner-memory accept, remain
   `TODO(question)`; flag bits 0,1,2 and 3–6 are adjudicated. Tall features do
   not raise LOS ray height.
- Bilinear height at map edges returns sentinel `-1` and the four-corner
   read guards `cx+1 < Width` and `cz+1 < Height`; outside the rectangle,
   movement is blocked for generic modes and allowed only for factory-exit
   search mode 2, the LOS writer stores an empty footprint, projectiles do not
   collide, and the camera clamps to `PlayRight`/`PlayBottom`. Void fill is
   established for right columns `W-2,W-1` always and for lava-world bulk flood
   when `lavaworld` is set and `hmin ≤ SeaLevel`; north/south height-dependent
   strips beyond the right two columns remain `TODO(question)`. Deformation
   update ordering (derived `hmax`/`hmin` recompute then occupancy notify) is
   established; exact per-tick sequencing with movers remains narrow
   `TODO(question)`.
- The engine-side derivation of the aggregated two-byte terrain-word table
   feeding sight shapes is **supported inference** as low = minimum and high =
   maximum over the four attribute cells that form one visibility tile — low
   gates admission, high gates horizon advance; the exact builder formula is
   `TODO(question)` until the lazy rebuild is traced. Fog-cache channel values
   1..14 encoding remains `TODO(question)`. The vismasks shape count (10
   frames in `anims/vismasks.gaf` entry `vismask`) versus LOS.TDF declared
   table count (9) mismatch is closed: the sprite path clamps to the 10-frame
   count and the ray path clamps to the declared 9, with three excess LOS.TDF
   sections unreachable.
- 3DO primitive color/texture precedence and selection-primitive picking
  behavior beyond the established load-time swap of the declared selection
  primitive with primitive zero and the subsequent bubble sort of remaining
  primitives by mean second-coordinate; the swap, rewrite, sort, and hierarchy
  mirroring themselves are established (see sections 2.4 and 2.4.1 and
  document 02), as is plate exclusion from the unit draw pass (the
  rasterizer's primitive loop starts at index 1 for pieces declaring a
  selection primitive) and the SHD row formula for textured faces. Still open:
  whether any outside code smooths pieces remains open.
- Cloak/stealth early-outs (including the minimum-cloak-distance proximity
   breach) are established; jammer circles are established as minimap-only
   (three callback tables onto separate surfaces wiped each tick, never the LOS
   word mask) — separation is closed. Still open are the full stealth/init-cloak
   spawn state walk, gameplay radar-versus-sonar contact rules beyond the
   presentation circles, backing-surface layout plus mark arbitration among the
   three sensor/jammer callback tables, and whether any unresolved identity path
   shares visibility grids across players — multiplayer LOS sharing is closed as
   never-OR through the mask itself.
- Exact edge behavior for unexplored in-map void cells, map border clipping,
  and persistent backbuffer pixels.

### Renderer

- Full windowed/fullscreen mode transitions, DirectDraw surface flags, lost
  surface recovery, palette-loss recovery, and exact blit/flip error policy.
- Complete pass table is closed: ten fixed-order strips inside the single frame
  composer with Y-bucket insertion and no depth test, exact numeric order,
  render-mode gates, removal-before-update lifecycle, the 401 steady eviction
  bound, and the 300-record effect pool (section 1). Still open: semantic strip
  names, every effect-strip owner/flush point beyond the reviewed producer
  family, and full producer coverage.
- SHD/LHT/ALP row/index formula in every consumer and whether ALP is used by any
  non-LOS UI/fade path.
- Model lighting normals, texture coordinate policy, flat-color handling in all
  primitive cases, and exact team/logo frame selection edge cases.
- Shadow projection coefficients, feature/model stencil geometry, shadow
  clipping, dither pattern, and shadow-vs-fog interaction at boundaries.
- Water wake rectangle interpolation, underwater tint, splash timing, and proof
  that no hidden animated-water surface writer exists.
- Cursor hotspot metadata, subframe lifetime, animation speed for families not
  shown to use the authored countdown cursor, sequence-flag naming, and complete
  effect-strip owner registration, lifetime, and terminal-frame behavior.
- FNT baseline, glyph advance/kerning, two-byte header fields, clipping edge,
  and any shell path that uses GDI text directly.
- Input repeat/focus/activation rules, key-token translation, cursor capture,
  gadget hit-testing, and complete HUD/minimap palette composition.

### Projectiles and effects

- Projectile rendertype dispatch is closed (all eight cases, section 5.4).
  Still open: the physical calibration of the case-7 segmentation constant 327680
  (map-space versus projected-space units) and whether its presentation RNG
  draws must interleave deterministically with other presentation consumers;
  also the semantic of the low 16 bits of the stored projectile orientation fed
  to the Z rotation slot.
- Beam fixed-point scale/lifetime edge cases, collision ordering at map borders,
  and line-color remap initialization.
- Missile target invalidation, smoke/effect-strip lifetime and fade, and
  construction/nanolathe colors. Trail cadence is closed (additive deadline;
  zero-delay emits every tick).
- Flash/explosion animation cadence is closed for families using the countdown
  cursor: per-frame duration is the frame reference's second word (32-bit tick
  count at frame base + index*8 + 4), simulation-tick countdown ticks; cursor
  families use scaled wall-clock accumulation.

### Audio and music

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### Video and capture

- Smacker ordinal/API mapping, supported pixel formats, palette transfer, frame
  timing, dropped-frame handling, and audio synchronization.
- Fullscreen requirement enforcement, window restoration after a movie, quit/
  close handling, and movie-message-loop ownership of the main renderer lock.
- `Movie Output Rate` exact units, capture frame numbering, file format/encoder,
  capture failure behavior, and whether captured frames include GUI/cursor or
  only the world framebuffer.
