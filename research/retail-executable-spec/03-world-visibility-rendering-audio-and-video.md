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
strips** inside the single frame composer, each closed by a barrier invocation
carrying the pass number 0 through 9. The contract is the numeric order together with
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
order. Both dispatchers are now identified (2026-08-27, [R-STRIP-01]): the
update dispatcher is the per-tick sweep of document 01 §4.4 phase 11, and the
draw dispatcher is the frame composer itself, which walks each strip in the
staged order of this section and invokes each object's draw entry with the
framebuffer descriptor; the object's draw entry forwards the call to each of
its sub-records together with the camera origin, which is how sub-records
acquire their screen positions ([R-STRIP-01 §2]).

**Strip producer census (closed 2026-08-27, [R-STRIP-01 §1]).** An earlier
bounded census (promoted 2026-08-25, re-verified 2026-08-26) searched a
decompile corpus for a `push` immediately preceding a producer call and
concluded: literal-index producers for strips 2, 4, 6, 7, and 9 (2 shockwave,
4 crater/decal with "seventeen" strip-6 sites, 7 lightning/flame, 9
smoke/splash twelve sites) and no producer for strips 0, 1, 3, 5, 8. That
census is superseded. Its method was wrong: the retail producers take the
strip index as the low word of a stack argument that is pushed FIRST (often
many bytes ahead of the call, as the first argument rather than the last), so
the `push`-adjacent-to-`call` pattern missed real sites (strip 5) and its
"crater/decal literal 4" finding has no producer anywhere in the image. A
complete image-wide census — every reader of the strip-table root word, every
append invocation, and every call site of all twelve producer functions —
replaces it; the producer table and per-strip events are [R-STRIP-01 §1]
below. The strip-2 and strip-9 site counts of the old census were confirmed
(4 and 12); strip 6 has sixteen literal sites in the reference graph (the old
"seventeen" was not reproduced); strip 7 has three.

#### R-STRIP-01 §1 — producer census and per-strip events

Every strip object enters its strip through one of twelve producer functions,
each of which allocates from one shared fixed pool (exhaustion silently drops
the object; a root flag byte disables all strip allocation when set), runs the
family's init virtual, evicts the oldest object when the pre-insert count
exceeds 400, and appends at the vector end. The strip index is a literal
argument at every call site. The complete strip → producer/event map:

| Strip | Producer events (Established, direct-static) | Sites |
|---|---|---|
| 0 | none — no producer exists anywhere in the image | always empty |
| 1 | none | always empty |
| 2 | COB emit-sfx vector types 2–5 (the "impact-effect switch" — see the re-verification note below): one jittered smoke puff (three CRT draws of `rand×7/0x8000 − 3` per axis) per spawn, spawn interval 1 tick with the per-site spacing parameter (16 or 8) scaling puff lifetime; palette colors `0x61`/`0x67` | 4 |
| 3 | none | always empty |
| 4 | none — the retired "crater/decal literal 4" is retracted (see §3.7) | always empty |
| 5 | flame-weapon area scan (1 site): for every other unit inside the attacker's definition-relative box, a 30-tick flame-stream object that lays one animated segment every 10 ticks with a random start frame, plus an ignition callback; burning-feature smoke (1 site, phase 6 of doc 01 §4.4): one wind-drifted smoke puff every 3rd tick with two CRT jitter draws at the call site | 2 |
| 6 | construction/reclaim nanolathe emitters: a source point and a target box, five particles per spawn tick over a two-tick spawn window (six CRT draws per particle) | 16 |
| 7 | flame-stream trail (2 sites): one animated flame segment per tick over a 6–7 tick flight from source to target; smoke sprinkle variant (1 site): the strip-2 family with 8-tick spacing and a 7-tick life | 3 |
| 8 | none (the composer still draws the strip, unconditionally) | always empty |
| 9 | impact smoke: the authoritative impact dispatcher under a weapon-definition flag (1), the projectile phase's trail-window and impact branches (2), the land/water/lava impact effect variants under a second weapon flag (3), the emit-sfx smoke point cases — white `0x101` and black `0x102`, two sites (see the re-verification note below), the fixed-effect-pool append side effect when the effect lands above sea level (1), and the sinking-wreck path's long-lived (900-tick) smoke column (1) | 12 |

The old census's strip-2 count (4 sites) and strip-9 count (12 sites) are
confirmed; strip 6's count is sixteen sites in the reference graph, one short
of the old census's seventeen (the extra site was not reproduced and is not
assumed to exist).

**Re-verification — the "impact-effect switch" is the COB emit-sfx type
dispatch (2026-08-28, direct-static).** The strip-2 producer named above as
the "weapon impact-effect switch" is the COB `emit-sfx` opcode's type-byte
dispatch, not the impact dispatcher: vector types 2–5 run the strip-2/7
sprinkle family (types 2/3 source→target, 4/5 with the endpoints swapped;
the swapped pair needs the piece's second effect vertex, whose derivation is
`TODO(question)`), type `0x101` is a white smoke point and `0x102` a black
smoke point (both strip 9), and type `0x103` is the strip-7 water-line
sub-bubble sprinkle. The earlier partition counted the black smoke point
under the sinking-wreck path, which contributes one site, not two; the
strip-9 total of 12 is unchanged.

Two sprinkle-family mechanics the original row compressed: the 16-or-8 value
is the per-site **spacing** parameter, which scales puff lifetime (puffs live
`spacing×6` ticks); the spawn interval itself is 1 tick, and a sprinkle
container holds two puffs (one at construction, one at the single gate fire —
the object's window closes one tick after creation). The smoke family's
animation delay defaults to 7 when a producer passes zero.

#### R-STRIP-01 §2 — object families, update work, and terminal state

Every strip object is a pooled container record holding a dynamic vector of
fixed-stride sub-records (particles or segments). The per-tick update dispatcher
of document 01 §4.4 phase 11 evaluates, per object in insertion order, a
removal verdict virtual BEFORE the update virtual; the update advances each
sub-record's position by its velocity, advances its animation state, removes
expired sub-records (each carries its own expiry tick) with stable in-place
compaction, and may spawn new sub-records through a spawn gate (next-spawn tick
compared against both the object's window end and the global tick). The removal
verdict is "the internal list is empty" (the container object dies once its
last particle/segment expires; one family additionally requires its window to
have passed). One family's sub-records also expire early when the terrain
height beneath them falls below sea level — its marks die on water. The draw
entry invoked by the composer's per-strip walk forwards each sub-record to a
per-sub-record draw that applies the ordinary projection with the half-height
shear, gates on the local player's mode-selected coverage at the projected
tile, and blits either a GAF frame or a two-by-two filled rectangle (fill
colors: the nano ramp `0xa1..0xa7`; the impact-sprinkle palette colors `0x61`
and `0x67`). Asset bindings: the flame families blit the flame-stream GAF
entry; the smoke family blits one of two smoke GAF entries selected by an init
flag. Sub-record strides are 52 bytes (flame segments of strip 5), 48 bytes
(nano particles of strip 6), 60 bytes (strip 7 trail segments), 68 bytes
(strips 2/7 sprinkle puffs), and 32 bytes (strips 5/9 smoke puffs); container
records are 68, 76, 68, 72, and 56 bytes respectively. The nano particle
carries the unexplained word set to `0x100` at spawn noted in §5.5.

#### R-STRIP-01 §3 — random draws inside the sweep (CRT stream)

The phase-11 sweep consumes no draws at the dispatcher level, but its objects
do, all from the CRT presentation stream — this quantifies doc 01 §7.2's
"object-internal" census row for phase 11: the nano emitters spend thirty
draws per spawning record per spawn tick (five particles × six coordinate
draws); the smoke puffs spend one draw per spawned puff (start frame) plus one
draw per animation-frame advance (the next frame's delay, drawn as half to
full of the authored delay); the flame-stream segments spend one draw per
segment (random start frame); the impact sprinkle spends three draws per spawn
(per-axis jitter); the strip-7 trail spends none. The composer-time draw
entries consume no draws. All of this randomness is presentation-stream only;
the simulation Park–Miller stream is never touched by phase 11.

#### R-WIND-01 — the wind direction vector: which table feeds which axis

Document 01 §7.3 (phase 8) records that the wind direction pair is computed as
−2 × the fixed-point trig of the heading, scaled by the speed; the producer
side is doc 01's territory. This section names the axes from the consumer
side (Established, direct-static, 2026-08-27): the first word of the pair is
the **X** term and the second word is the **Z** term. The first word is −2 ×
speed × **sin**(heading), the second word is −2 × speed × **cos**(heading),
where the trigonometry is one shared 512-entry sine table of signed 16-bit
entries whose entry *k* is 8192·sin(2π·k/512); the cosine is the same table
read a quarter turn (128 entries) ahead. Both helpers round the product to
the nearest whole world unit (half-up bias), and the phase-8 producer stores
each result multiplied by −2. Verified in two independent consumer families:
the strip-5/9 smoke drift applies the first word to the world-X velocity term
and the second to the world-Z term, and the feature fire-spread probe
accumulates the first word into its X-cell coordinate and the second into its
Z-cell coordinate while walking plot cells X-major. A reimplementation should
therefore publish `windX = −2·round(speed·sin(h))` and
`windZ = −2·round(speed·cos(h))`; the smoke family multiplies the published
words by a further 8 per tick and the fire probe by 2 per probe step, both
factors belonging to those contracts' own scales.

**Fixed effect pool.** Effects are not strip objects: a separate fixed pool
holds up to 300 fixed-size effect records, and appends at or above the cap
allocate nothing. Rendering walks the whole pool once per embedded animation
category and again for model-bearing records, each draw guarded by buffer
admission. Its tick integrator advances velocity against gravity, can restore
a prior position and invert/halve vertical velocity on terrain/water contact
or clear a record’s model pointer, single-steps both embedded animation
players, clears non-looping sequences’ pointers at termination, and removes
emptied records by stable left compaction within the same updater invocation — an
animation terminating during its step retires its record that same invocation,
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
derived stamp. **Fringe partition is the placement order, not a heuristic.** The
feature stamper writes every `0xFFFE` cell at stamp time with its anchor→fringe
offset (`0..footX-1` in the X byte, `0..footZ-1` in the width-scaled Z byte) as it
stamps the feature's footprint rectangle, in the loader's row-major attribute
order. A TNT-authored `0xFFFE` that no footprint rectangle covers is never
stamped and ends up `0xFFFF` after load — such cells are not fringe at all, they
disappear (measured: about 5,283 such cells across the 275-map corpus). Merged
blobs resolve by last-stamp-wins: a later footprint overwrites an earlier one's
fringe cells with its own offsets, which is why the retired 83.2% row-major
left/above later-wins heuristic misassigned the seam cells of overlapping blobs
(the heuristic had no adjacency path to a later anchor that was not left/above a
fringe cell). Declaration footprints alone resolve 65.1% of raw fringe; the
sequential stamp resolves 100% of footprint-covered fringe by construction.
`TODO(question)` remains only for the dense-pack rule — whether a footprint that
overlaps a live anchor cell is rejected or silently overwrites — which the
stamper's occupancy guard decides per consumer.

Void and edge generation runs after the full-map minimum/maximum recompute and
after feature placement. Right columns `Width-2` and `Width-1` are set to
`0xFFFD` where the feature word is empty or fringe — live features and anchors
in those columns survive (an earlier copy of this sentence said "every row
unconditionally"; superseded by document 02 §6's traced edge rules). Playable
insets `PlayRight = WidthPixels
- 32` and `PlayBottom = HeightPixels - 128` are set at that time and gate the
camera clamp. When the mission `lavaworld` flag is set, a bulk sweep sets
`0xFFFD` for every cell where `hmin ≤ SeaLevel` and the feature word is
`0xFFFF` or `0xFFFE`. North and south height-dependent void strips are
established in document 02 §6 (north `z*16 < height>>1` on raw height,
south `(Height-1-z)*16 + (height>>1) < 112`; empty-or-fringe cells only) —
an earlier copy of this paragraph carried the predicate as `TODO(question)`.
Outside the map rectangle, height returns sentinel `-1` with unsigned
candidate bounds before any terrain read; movement is blocked for generic modes
and allowed only for factory-exit search mode 2; the LOS writer stores an empty
footprint and returns; projectiles do not collide with terrain; and the camera
remains clamped to the playable insets.

Per-cell metal is uniform on canonical maps: every cell's metal byte is seeded
from the single mission `SurfaceMetal` value (non-negative, canonical version
only; negative or legacy seed 0). The TNT unknown byte is zero corpus-wide and
is not a metal source, and no `Width × Height` metal raster is allocated. The
legacy terrain version seeds each cell from its 8-byte attribute record's
per-cell metal byte — there is no varying metal file; the legacy attribute
byte is the only per-cell source (document 02 §6, superseding the earlier
`TODO(question)`). An extractor
at placement sums `unsigned(metalByte) + 1` over its footprint and multiplies by
its `extractsmetal` scalar; the stored result is never resampled. Feature metal
is reclaim reward only.

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
to 32-pixel visibility tiles and reads a `uint16` word per visibility tile (see
section 3.2), aggregated from the terrain heights at map load — not the
four-corner bilinear query, and not rebuilt during a battle. The builder is now
traced and the aggregation is
established — and it is the REVERSE of what was inferred here: the table is
seeded low = `0x00` / high = `0xFF` and updated with `low = max`, `high = min`,
so the **low byte is the neighbourhood MAXIMUM and the high byte its MINIMUM**.
Cells are scattered into it through the same height shear the observer's
coverage tile uses, carrying a perspective-scaled value, and a tail pass blends
the pair by thirds and floors both at sea level. See §3.5
`[R-P0-18-B §1–§4]` for the derivation; the earlier
(minimum, maximum) reading is the maximally occlusive pairing and produces
false shadows on ground retail leaves visible. A tall feature does not raise
the LOS ray height — nothing but the map-load build does, since the word is
never invalidated.

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
the vertical axis applied to the whole model. The trailing sign on Z seen in projection helpers is the `Z - Y/2` orthographic shear — the high word of Z is transiently negated in place, then half of Y is subtracted, with the `+32` viewport bias, and the result is never stored back — not a second model-space sign fixup; the load-time half-turn (negating X and Z of each vertex and each parent translation) remains the sole persistent conversion (`H_A` net `-X,-Z` established, `H_C` net `-X` rejected, `H_B` already rejected). Child `flare` and piece translations queried at muzzle reuse the pristine post-load vectors without a second negation; a flare authored at `(2,1,-30)` appears at `(-2,1,+30)` world plus unit origin (direct-static for `H_A` vs `H_C` via store-path data-flow, bounded-negative for a second store; heading-zero nose mapping remains
supported inference, probe-pending — the `ta_probe_xz` fixture (child
translation signs at headings 0/90/180/270) is designed to settle it, see
rr-06 §4). Draw order within a piece is
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
   saturated; intermediate rows shift hue per entry). `dont-shade` (definition
   flags bit 2) forces row `0x0F` (15); else `row = trunc(dot*5.0) & 0x1F`
   wrapping negatives to `27..31` (not clamped); the gouraud interpolant is
   `rowStep = (rowR-rowL)/width` in signed 16.16 fixed point for both the fixed
   and mobile textured scanline paths, and each pixel samples
   `SHD[row*256+texel]`; the flat path fills the span directly with no `SHD`
   lookup (direct-static). The light direction is read from three settings as
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

Equivalent paths use the same world-to-pixel scale and a half-height shear. The transient negation of Z before high-word extraction in bounds/raster helpers is the shear term above, not a persistent vertex store — a bounded-negative census finds no second stored negation outside the load-time half-turn (direct-static).
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
terrain-ray raster, and bit 3 marks the fog-cache-valid state — the
composer's two-byte fog/minimap overlay cache (section 3.3) is rebuilt when it
is clear and the bit is set again after the rebuild. It is cleared at map load,
on camera moves, and by LOS publication (any local coverage change), so a
visibility change or camera pan triggers one fog-cache rebuild — it does not
gate the LOS terrain-height word, which is built once at map load and never
invalidated (section 3.5). The
byte-grid path treats any nonzero `uint8` count as visible; its increment is a
plain wrapping `uint8` addition with no clamp (256 overlapping observers wraps to
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
visibility tile (`TileW × TileH` words, two bytes per tile) that was built once
at map load from the terrain heights and is never rebuilt during a battle
(invalidation by deformation does not exist; only the fog cache is
dirty-tracked). The low byte is tested for admission and the
high byte is tested to advance the retained horizon — identical strict
comparisons, but the low byte decides whether the cell is seen and the high byte
decides whether the horizon rises. The full builder contract is **Established**
(see §3.5 [R-P0-18-B]); the earlier reading recorded here — low = minimum and
high = maximum over the four attribute cells of one visibility tile, with the
exact formula open as `TODO(question)` — was **inverted**: the low byte is the
**maximum** of raw heights over the scattered neighbourhood and the high byte
the **minimum** of perspective-scaled values, with the pair blended by thirds
and floored at sea level in a final pass. The old pairing is the maximally
occlusive one and scatters false shadows across ground retail leaves fully
visible. The builder fills the whole word array in one pass at load; the lazy
cache that rebuilds "when the cache-valid mode bit is clear" is the fog/minimap
overlay cache of section 3.3, not this table.

**Spoke geometry.** Each LOS.TDF line is expanded into four mirrored quadrants
by 90-degree rotation. The authored offsets are **absolute positions from the
observer, not cumulative deltas** (established): the ray stepper applies each
rotated pair directly to the origin cell (`x = tileX + dx`, `y = tileY + dy`)
with no running accumulation, and the step-distance counter used by the horizon
test counts from one — both only cohere if the authored pairs are absolute
positions along the spoke. The earlier `TODO(question)` on absolute versus
cumulative is closed.

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

The observer's emitter height byte is `clamp(worldY_high + modelTop, 0, 255)`
with the world Y first raised to at least `(SeaLevel+1) << 16`, and its coverage
tile is `tileX = worldX_high >> 5`, `tileZ = (worldZ_high - emitter/2) >> 5`
(arithmetic shifts). `modelTop` is the high word of the model-top dword computed
once at model load as the maximum of `vertexY + pieceY` over the piece
hierarchy, floored at zero, so the eye sits at the top of the unit's 3DO model
rather than on the ground — see §3.5 [R-P0-18-A] for the full builder contract
and the Z-shear term.

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
one-point form; feature drawing uses a **two-corner form** over footprint
extents — first corner at the cell origin (sheared), then a single corner
displaced by the footprint offsets; the earlier four-corner reading in §5.1.5
was wrong and is corrected there; the sensor phase's
final pass inlines a single-point test.

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

Compatibility anchors retained from the folded fog addendum:

| Anchor | Finding in this section |
|---|---|
| `[R-RR16-A §1]` | A fully current-fogged cell remaps existing pixels through the gray palette table. |
| `[R-RR16-A §2]` | Dithered current fog writes black checker pixels instead of a constant fog color. |
| `[R-RR16-A §3]` | GAF frame offsets anchor each fog shape at its visibility-cell corner. |
| `[R-RR16-A §4]` | Each fog cell combines the four visibility tiles that meet at that corner. |
| `[R-RR16-A §5]` | The four-way variant selector is world-anchored and camera-independent. |
| `[R-RR16-A §6]` | A missing GAF entry leaves the destination untouched. |
| `[R-RR16-A §7]` | Conditional border fixups propagate corner bits when the cache crosses a map edge. |
| `[R-RR16-A §8]` | Black, gray, and dithered-gray families share mask geometry but differ in pixel effect. |

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
between an in-map void tile and an out-of-map clipped region (probe-pending:
a capture at the map edge distinguishes them and shows whether the backbuffer
persists stale bytes beyond the play rect), but high that
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
- Else channel one == 15: **a per-pixel palette-LUT remap** — `dst[i] =
  grayTable[dst[i]]` over the clipped cell — which desaturates the terrain
  already under the cell through the nearest gray palette entries, preserving
  texture; it never writes a constant color [R-RR16-A]. When the options-storage
  dither bit is set, the same state instead writes literal
  palette index 0 (black) at checker positions `(x + y + parity) & 1 == 1` with
  `parity = (camX + camZ) & 1` — black dots over whatever is on screen, never
  the fog color.
- Else channel one in 1..14: GAF frame `value - 1` drawn from a four-way
  variant family selected by `(cellX + cellY + cameraPhaseSum) & 3`, blitted
  plain or parity-seeded patterned per the same option bit.
- Then channel zero in 1..14: frame `value - 1` from a second four-way family
  via the plain blitter. Channel one therefore renders BEFORE channel zero.

This overlay sits at the compositor position after all strips/effects and
before selection/interface (section 1). The cached channel derivation is now established (direct-static): hi accumulates the per-player byte-grid (`cur==0`) only when mode bit 1 is set else zeroed, lo accumulates the word-grid history mask `1<<player` regardless; each holds a 4-bit nibble `0..15` via four bounded bit-OR sites for masks `1,2,4,8` (`0` transparent, `15` solid dark, `1..14` index `value-1` into the Gray=hi=current and Black=lo=history four-way variant families with `variant=(col+row+camPhase)&3` deterministically from `floorMod(camera,32)` residues `0..31` via `offX/offZ=(res<16?-16:+16)-res` and `rect=[vpLeft+offX+col*32, vpTop+offZ+row*32, +31]` inclusive), edge rows/cols reached by conditional border fixups when the viewport extends beyond the map (see the fixup table below — never unconditional 15 stores). The bit→cache-cell geometry is direct-static (re-exported 2026-08-26): a fogged tile ORs bit 1 into the cache cell of its own tile, bit 2 into the west cell, bit 4 into the north cell, bit 8 into the northwest cell — the cell accumulates from the four tiles around its centre corner. Corner→bit `1=NW,2=NE,4=SW,8=SE` (which GAF quarter each bit paints) remains supported inference pending asymmetric fog.gaf probe; retail’s hard fog edge must not be softened to improve image metrics.

**Fog art and family behavior [R-RR16-A].** The four-way variant selector is
world-anchored, not camera-relative: substituting the cache-relative column
into `variant = (col + row + camPhase) & 3` with `camPhase = floorDiv(camX+16,32)
+ floorDiv(camZ+16,32)` collapses identically to `(gx + gy + 2) & 3` — the
camera phase cancels exactly, and the tiling must not rotate as the camera
pans. The 32×32 fog cell is centered on the corner where the four
visibility tiles `(gx,gy)`, `(gx−1,gy)`, `(gx,gy−1)`, `(gx−1,gy−1)` meet
(`sx = vpLeft + offX + col*32` with `startX = floorDiv(camX−16,32)` reduces for
every camera residue to map pixel `gx*32+16`; `floorDiv(x+16,32) −
floorDiv(x−16,32) == 1` identically), which is exactly the four tiles whose
fogged state the cell's nibble accumulates. A fully fogged tile gets one 16×16
cloud quarter in each of the four cells around its bottom-right corner.

The GAF fog frames carry their placement in the frame header's signed
`XOffset`/`YOffset` words — destination `(x − XOffset, y − YOffset)` before
clipping — and every shipped frame uses exactly two pixel values: palette
index 9 is the transparent color key and palette index 0 is the cloud.
**Correction:** an earlier reading that the clouds are bright blue
`(84,84,252)` by palette was inverted; drawing them that way puts blue blobs
along every fog edge (see `[fmt gaf]` frame header +8). Frame `n` covers the
cell area implied by nibble value `n+1`: value 1 → 16×16 quarter at `(0,0)`,
2 → 16×16 at `(-16,0)`, 4 → 16×16 at `(0,-16)`, 8 → 16×16 at `(-16,-16)`;
intermediate values are unions with sizes `16×16..33×21` and matching offsets.
A missing GAF entry leaves the cell untouched — terrain stays visible through
it; there is no solid-fill fallback.

**Family behavior.** Mask geometry is identical across the three fog blitters —
non-key pixels covered, key pixels skipped — but what lands differs: the black
family (channel zero/history) is a plain keyed copy of source pixel 0 (paints
black); the gray family (channel one/current) is a masked LUT remap that never
writes the source pixel; the dithered gray family steps x by two and stores
literal palette index 0 at checker positions. The gray frames are geometrically
LARGER than the black frames for the same nibble value (gray frame-1 is 19×19
where black is 16×16), and the gray channel draws before the black channel, so
a boundary cell gets a desaturated fringe surrounding the black cloud — the
grey rocky border around the explored area in retail screenshots is fog art,
not terrain. It reads grey only if the gray table itself is built correctly
(§4.3.3): the nearest-color search must walk retail's sum-sorted permutation,
or the fringe comes out red and yellow.

**Map-edge propagation [R-RR16-A].** The four producer border fixups run only
when the cache window crosses the map edge and apply conditional bit ORs, never
unconditional stores — **correcting** the earlier reading that "border loops
force edge rows/cols to 15":

| Fixup | Triggers when | Effect |
|---|---|---|
| Top (void row 0, all columns) | window crosses north edge | `bit4 → |= 1`, `bit8 → |= 2`; `hi` gated by mode bit 0x2, `lo` always |
| Bottom (row h−2, all columns) | window crosses south edge | `bit1 → |= 4`, `bit2 → |= 8` |
| Left (void column 0, all rows) | window crosses west edge | `bit8 → |= 4`, `bit2 → |= 1` |
| Right (column w−2, all rows) | window crosses east edge | `bit4 → |= 8`, `bit1 → |= 2` |

The passes run in order top, bottom, left, right on shared bytes, so corner
cells compound (an unexplored bottom-right in-map corner reaches 15 via bottom
`1→4` then right `4→8` then `1→2`). Void cells north/west of the map can draw
partial clouds or reach the unexplored solid-black short-circuit; south/east
void cells stay zero. **Supported inference:** the fixup rows/cols `h−2`/`w−2`
are relative to the viewport+border cache, so the exact camera band where
bottom/right thickening is active depends on the `+2 or +3` border width;
anchoring to the window end minus two and gating on the window crossing the map
edge is visually equivalent (the thickened 16px strip is only on screen when
the window crosses).

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

The radar picture is built from the terrain tile set or an optional baked minimap.
Its aspect ratio preserves the map shape with a fixed long side. When generated,
the renderer supersamples to twice the radar dimensions, maps each output sample
back to map/tile coordinates, and **samples the tile-set pixel bytes directly**
— there is no height read and no terrain radar table (the earlier sentence in
this section is corrected by §3.7 [minimap]): the fill picks the tile from the
tile map and copies the indexed pixel at `(z & 31)*32 + (x & 31)` within the
tile block. It then creates radar-picture, mapped, and final surfaces.

Each tick, radar blips are projected from unit/world coordinates into radar
coordinates. Radar and sonar range circles, jammer circles, and weapon-range
circles are rasterized onto the radar surface using distinct palette colors.
The radar-mapped surface is wiped and rebuilt each tick, while the authoritative
LOS mask persists untouched by this path. **The effect of jammers on
authoritative contact state is closed: there is none.** No reader ORs the
jammer (or sensor-circle) surfaces into the mapping word grid, and the gameplay
visibility predicate never samples them — jammer influence is presentation-only
distortion (bounded-negative over the sensor and predicate families).

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
**Sensor/jammer arbitration is partially closed:** the three callback tables
rasterize onto the minimap presentation surface sequentially — radar/sonar outer
circle first, then the two jam circles — with last-writer-wins per pixel
(plain pixel stores, not OR), each circle family in its own palette index, and
nothing reaching the mapping word grid or the per-player byte grids; the
per-table palette mapping (which table draws the radar versus the jammer index)
remains supported inference. The unresolved gameplay side is the identity of the
secondary radar-like candidate list that targeting may consult when the primary
in-radius set is empty — that flag's authored name is not proved, and targeting
is owned by document 06.

**Sensor callback gate correction (Established).** “Active” in the sensor
phase means the unit instance's activation/on-state bit is set. A live unit
whose radar or sonar distance is nonzero emits its outer circle only after
that activation test. The cloak/hidden instance bit is not consulted by this
circle-callback gate; it belongs to the separate visibility and decloak paths.
The selected-unit circle presentation has an additional definition test
documented in §3.9.

**Sensor phase placement in the tick (Established, 2026-08-27,
[R-SENSOR-01]; closes the DET-06 seam question).** The sensor phase is not a
phase of its own and does not run at composer time: it executes inside the
per-player pass of document 01 §4.4 phase 5, in the LOCAL viewing player's
iteration, after that player's order dispatch, per-player work, LOS stamp
sweep, per-tick minimap contacts pass, and 30-tick victory/defeat block — and
immediately before the mapped-minimap surface rebuild, which runs in the same
iteration behind its dirty bit. The phase is gated on the player count being
greater than one. Because the per-player pass walks players in ascending
order, the sensor/deadline work runs AFTER the local player's visibility
stamps but BEFORE every higher-indexed player's stamps within the same tick.
Its unit walks (friendly-contact status, sensor-circle emission, jam circles,
the minimum-cloak proximity scan writing `tick + 90` deadlines and the
decloak status bit, and the final seen-marker pass) cover the whole unit pool
in that one placement, once per tick — Nanolathe should schedule the
sensor/deadline work as a single per-tick pass keyed to the local viewing
slot, positioned after the local player's stamp sweep, not as a separate
tick phase and not adjacent to the composer. Residual: the writer that
clears the per-unit seen marker (status bit `0x100`) between passes was not
located; the set site is the final pass above.

### 3.5 LOS observer height, coverage tile, and the terrain height word [R-P0-18-A] [R-P0-18-B]

Status: every finding below is **Established** (direct static evidence). This
section folds R-P0-18-A and R-P0-18-B; it supersedes the supported-inference
paragraph "Terrain height word for the ray" in §3.2 and closes its
`TODO(question)` entries: the emitter-height addend provenance, the coverage
tile's Z term, and the aggregated two-byte height-word derivation. The
aggregation formula was re-verified instruction-by-instruction against the map-load
builder in 2026-08-26 (including the per-column carry, which appears in no
earlier note); the only claim corrected since the previous revision is §4's
"lazy rebuild", which misidentified the fog-cache builder as this table's
producer — the terrain word is built once per map load and never rebuilt.

#### R-P0-18-A §1 — Observer emitter height and model-top provenance

The LOS observer footprint builder fills one record per unit with: the
definition's `sightdistance`; the definition's model-top field (as a byte); the
unit's previously stored height byte (the value the refresh throttle compares);
and the unit's current world X, Y, Z as 16.16 fixed-point. Before the emitter
byte is formed, the record's world Y is raised to at least
`(SeaLevel + 1) << 16` — the map's sea-level byte, incremented by one and
scaled to 16.16 — so this clamp only moves the value upward.

The emitter byte is then formed from the record's high words:

```
heightByte = clamp(modelTop + (worldY >> 16), 0, 255)
```

with the world-Y high word taken as a signed 16-bit value (the signed high
word of the 16.16 coordinate). A sum below zero saturates to 0; a sum above
255 saturates to 255.

##### Model-top provenance

The model-top byte is not a definition field of its own. At model load a single
pass computes the model's top extent once and stores it as a dword; the LOS
record consumes its high word, so the emitter addend is the model top in whole
world units. The walk visits each piece and its sibling chain and accumulates
the maximum of `vertexY + pieceY` over the piece's vertices — each vertex
record is three 16.16 coordinates, Y the second — then adds the recursive
result of the child subtree plus this piece's own Y translation (sibling/child
links per §2.4). The accumulator starts at zero, so the result is floored at
zero. The same load-time site also derives the model height as the model top
minus a separate definition field; the LOS path never consumes that value.

Measured on the reference install (whole world units): ARMCOM 39, CORCOM 38,
ARMPW 26, CORAK 26, ARMSOLAR 38, ARMLLT 46.

##### Why the model top matters

The terrain-ray horizon test admits a step only when its slope STRICTLY
exceeds the retained horizon (§3.2). An observer whose height equals the
ground beneath it retains slope `(0, 1)` after its first step, and on flat
terrain every later step ties and is rejected — every unit's sight collapses
to a single ring of roughly one cell. Sighting from the model's top gives the
ray a negative slope to spend, so flat ground stays open and a laser tower
(model top 46) genuinely outranges a peewee (model top 26) at equal
`sightdistance`. This is also the mechanism behind the attested "a tank in a
valley sees less than a radar tower on a hill".

#### R-P0-18-A §2 — Coverage tile shear

Immediately after the clamp the same builder computes the coverage tile with
arithmetic (flooring) shifts:

```
tileX = (worldX >> 16) >> 5
tileZ = ((worldZ >> 16) - emitter/2) >> 5
```

`emitter/2` is itself an arithmetic shift by one. Both shifts are arithmetic —
floor, not truncation toward zero. The Z subtraction is the same beam shear the
camera projection applies: a tall observer's LOS footprint sits half its height
north of its ground position. The record's stored tile X, tile Z, and stored
height byte are exactly what the refresh throttle of §3.2 compares: recompute
only when tile X changed, or tile Z changed, or the height byte moved by more
than 5.

#### R-P0-18-B §1 — Terrain height-word polarity

**Polarity: the low byte is the maximum, the high byte the minimum.** The
reading recorded in §3.2 — low byte = minimum and high byte = maximum over the
four attribute cells of one visibility tile — is inverted. The table is
allocated as `((TileW * TileH) + 7) & ~7` sixteen-bit words with
`TileW = CellW/2` and `TileH = CellH/2` (arithmetic shifts), i.e. one word per
visibility tile, and each word is seeded low byte `0x00`, high byte `0xFF`.
Every update then applies, per scattered value:

```
if (value > low)  low  = value
if (value < high) high = value
```

A 0-seeded accumulator that keeps the larger is a **maximum**; a 255-seeded
one that keeps the smaller is a **minimum**. So: **low byte = MAXIMUM, high
byte = MINIMUM**, with strictly-greater values replacing low and strictly-
smaller values replacing high.

The pair is not symmetric in the horizon rule of §3.2: admission tests
`retainedNum * stepDist < candidateDiff * retainedDen` with
`candidateDiff = low - emitter`, and the horizon advances on
`highDiff = high - emitter`. The old reading was the maximally occlusive
pairing of the two — low as the minimum makes the candidate difference more
negative (harder to admit) and high as the maximum makes the retained horizon
shallower (harder to admit afterwards) — and it scatters false shadows across
ground retail leaves fully visible.

#### R-P0-18-B §2 — Cell scatter through the beam shear

The builder walks
columns then rows — outer loop over CellW, inner loop over CellH — with the row
coordinate carried in pixels and stepped by 16 (one attribute cell) per row.
For each attribute cell it reads the raw height byte at plot-cell offset 4 (per
§2.2 layout) and computes

```
zs    = zPixels - height/2     (arithmetic shift by one)
tileZ = zs >> 5                (arithmetic shift)
if (tileZ <= -1) skip the projection
```

— the same height shear the observer's own coverage tile uses. The cell
projects onto tile columns `(x-1) >> 1` and `x >> 1` at row `tileZ`.

Two values reach the table per cell. First the perspective-scaled

```
value = ((tileZ*32 + 31) * height) / (zs + 31)      (truncating division)
```

is scattered into the two tiles the PREVIOUS row's cell in this column
resolved to (a carried pair, zeroed when out of bounds) and then into this
cell's own two tiles. Then the raw `height` is scattered into this cell's own
two tiles; on the skipped-`tileZ` path only the carried pair is updated, and
with the raw height.

The carry is what stops tiles being missed where the shear jumps a row. Since
`value <= height` for every non-skipped `tileZ`, the net effect is: the low
byte ends up the maximum of raw heights over the scattered neighbourhood and
the high byte the minimum of scaled values.

#### R-P0-18-B §3 — Tail blend and sea-level floor

A final pass over every
word blends, both divisions by three truncating (executed through a
fixed-point reciprocal multiplier, not an arithmetic divide):

```
newLow  = (high + 2*low)  / 3
newHigh = (low  + 2*high) / 3
if (newLow  <= SeaLevel) newLow  = SeaLevel
if (newHigh <= SeaLevel) newHigh = SeaLevel
```

Both results are computed from the ORIGINAL pair — not chained. The blend pulls
the two bytes a third of the way toward each other (on flat ground they
converge); the floor is the map's sea-level byte.

#### R-P0-18-B §4 — Build lifetime: once per map load, no lazy rebuild

The table is built exactly once per map load: the TNT loader calls the builder
after the derived height pass and before the void fixup, and the array is freed
at battle teardown. There is **no** lazy rebuild and no invalidation during a
battle — terrain deformation does not refresh it, a tall feature never does,
and the mode word's bit 3 is not a terrain-word dirty bit: it is the
fog-cache-valid bit of section 3.1, whose lazy rebuild (the fog/minimap overlay
cache) was the routine misidentified in an earlier research pass as this
table's producer. (This paragraph corrects the previous text, which claimed
the table is "torn down and rebuilt behind the mode word's cache-dirty bit
(bit 3, §3.1), filling the whole word array at once. Terrain deformation —
plot-data changes — is the event that invalidates it".) A reimplementation must
build the word once at map load and keep it stale for the battle.

Regression fixtures locking this contract: `internal/session/los_emitter_test.go`;
`internal/world/terrain_test.go` (`TestLOSHeightAggregates`, locking the
polarity rather than the old inference); the three horizon-rule tests in
`internal/visibility/raster_test.go`.

### 3.6 Minimap surfaces and lifecycle

Retail holds four indexed surfaces for the minimap rather than one framebuffer region:

- **Radar picture** — the terrain picture, aspect-fitted to the fixed 126-pixel
  long side, built once and cached. Its source is either a baked minimap shipped
  in the TNT header or a generated sampling of the tile set. It never contains
  units or fog.
- **Radar mapped** — the picture masked by the authoritative LOS grids (the
  mapping word mask and the per-player byte grids of section 3.1); this is
  where unexplored and fogged terrain appears on the minimap. Rebuilt only when
  its dirty bit is set.
- **Radar final** — the composited minimap the player sees: mapped wiped onto
  final as the background, then unit blips, feature dots, sensor circles, and
  the viewport marker. Rebuilt every tick from mapped.
- **Radar temp** — a transient 2× supersampled buffer used only while
  generating the picture, freed immediately afterwards.

Panel chrome lives on two further surfaces: the flip surface, sized by the GUI
root (640×480 and so on), and a fixed 300×480 backup surface cleared to index
0. The main-view fog overlay (the Gray/Black GAF families of section 3.3,
viewport-sized) is a distinct surface; the minimap path and the fog overlay
share only a dirty bit.

Radar, sonar, and jammer presentation never authors the gameplay LOS word mask
— circles reach the final surface only through the sensor callback tables of
section 3.4. Fog and unexplored territory on the minimap is mapped masking, not
the viewport fog cache.

**Lifecycles.** The panel surfaces live from battle enter until battle exit. The
radar surfaces' lifetimes are tied to map load; the temp buffer is the only
radar surface explicitly freed — the free path for the picture, mapped, and
final surfaces is bounded-negative in the traced corpus (no free site observed)
and remains `TODO(T23)`.

**Cadence.** A radar dirty word carries the schedule: bit 0 is the blink phase,
bit 1 is final dirty (set by the mapped composite and by the contacts pass),
bit 2 is mapped dirty (set by surface allocation and by placement invalidation,
cleared by the mapped composite after it checks). A countdown byte counting 7
down to 0 drives the blink phase. The picture is built once, dirty-triggered;
mapped composites when dirty; final is rebuilt every tick after the sensor
phase.

**Save/load.** The final surface is serialized into the save blob. The picture,
mapped, and temp are rebuilt through the dirty bits on load; the mapping word
mask and the per-player byte grids are serialized as the Mapping blob. The fog
cache is not saved and is rebuilt lazily when its cache-valid mode bit is clear.

### 3.7 Radar picture build: baked versus generated

**TNT baked slot, version-gated** `[fmt tnt]`. The baked minimap lives in the
TNT header with version-specific slot positions: legacy version 0x1020 stores
the present flag in header word 15 bit 0 and the offset in header word 14;
canonical version 0x2000 stores the flag in header word 11 bit 0 and the offset
in header word 10. When the flag is clear there is no baked picture; when set,
a surface is allocated for the shipped bytes (tagged "TED GENERATED PIC"). The
stock corpus uses the generated path; both legs are traced.

**Generated picture, 2× supersampled.** When no baked image exists, retail
allocates the temp surface at 2·RadarW × 2·RadarH, samples terrain at doubled
resolution, then blends down through the ALP table:

```
for y in 0 .. 2*RadarH-1, x in 0 .. 2*RadarW-1:
    worldX = PlayRight * x / (2*RadarW)     truncating
    worldZ = PlayBottom * y / (2*RadarH)    truncating
    tileX = floor(worldX / 32)              signed floor (sign-corrected shift)
    tileZ = floor(worldZ / 32)
    tileIdx = uint16 tile map[tileZ*TileW + tileX]
    if tileIdx >= tileSetCount: tileIdx = 0   guard
    pix = tileSet[tileIdx*1024 + (worldZ & 31)*32 + (worldX & 31)]
    store index pix via the single-pixel blitter (no palette translation at build time)
```

The tile map and tile-set pixels never mutate after load (bounded-negative: no
writer outside the map loader). **Correction (2026-08-27):** the next sentence
previously read "Crater decals go through the strip-4 path only". That clause
is retracted — the complete producer census [R-STRIP-01 §1] finds no writer
for strip 4 anywhere in the image, so no crater decal can be a strip-4 strip
object. How ground scorch marks are actually authored (a direct map/tile edit
versus another store) is therefore unknown again: `TODO(question)` — trace the
crater writer; the placement-invalidates-the-mapped-surface behavior that
followed the clause is unaffected and stands. Picture bytes are PALETTE.PAL
indices — no LHT brightening or SHD shading — resolved to RGB only at
presentation.

**Downsample: two-level ALP blend, row-first.** The 2×2 downsample uses the
palette's 256×256 ALP table (64 KiB: an ordered pair of palette indices maps to
the nearest-color palette index):

```
blendTop    = ALP[ p00*256 + p01 ]
blendBottom = ALP[ p10*256 + p11 ]
out         = ALP[ blendTop*256 + blendBottom ]
```

**Established** (direct-static). The pairing is row-first horizontal: p00 =
(2x, 2y), p01 = (2x+1, 2y), p10 = (2x, 2y+1), p11 = (2x+1, 2y+1) — the top-row
pair is blended first, the bottom-row pair second, and the two blend results
combine through a third lookup of the same table (both results are palette
indices, so the second level is well-formed). Column-first and diagonal
pairings are rejected by exhaustive search (bounded-negative).
`TODO(question):` whether the left/right orientation within the row is mirrored
on screen remains open; an asymmetric-palette probe (row-first, column-first,
and diagonal orderings yielding distinct results) settles it.

**Correction to the prior paragraph (Established, direct-static).** The prior
text left non-integral baked sampling as a supported inference because the
asset dimensions alone did not settle it. A bounded clean-room trace of the
shared picture resampler shows that both picture legs enter one generic routine
with independent source and destination dimensions. For each destination axis
it truncates the source coordinate ratio, blends that sample with its adjacent
source sample, and applies the same row-first three-lookup ALP sequence above.
There is no nearest-neighbor path. This includes the authored 252×252 and
252×256 TNT minimaps when the destination lens is 126×126; no exact
`2*destination` dimension predicate is part of the gate. Confidence is
**Established** for the ratio/truncation and ALP order; the source bytes and
dimensions remain authored by the TNT format [fmt tnt].

**Aspect and letterbox.** The play area is window width minus 32 by window
height minus 128, derived from the mode maxima, not from the raw window
dimensions. RadarW/RadarH are 1..126:

```
if playW < playH:  RadarW = playW*126/playH;  RadarH = 126;  originX = (126-RadarW)/2; originY = 0
else:              RadarW = 126;              RadarH = playH*126/playW; originX = 0; originY = (126-RadarH)/2
```

All divisions truncate; the centering halvings round toward negative infinity
for odd remainders. The minimap hit rect is inclusive: left = originX, top =
originY, right = originX + RadarW − 1, bottom = originY + RadarH − 1.

**Established.** The picture allocation is exactly RadarW × RadarH bytes plus
its descriptor header — there are no heap bytes beyond the radar rect that
could leak into the letterbox bars. The blend overwrites every destination
byte, and the surface allocator never memsets the pixel region. The bars are
therefore HUD canvas outside the radar rect, not picture heap.
**Supported inference.** The bar pixels read 0 (black), consistent with the
panel clear and the dark fog-fill index; a canvas-capture probe settles it.
`TODO(question):` bar fill color.

Surface descriptors hold width, height, and a pointer to the w×h pixel block;
the pixel pitch is (w+3) & ~3 (DWORD-aligned), while allocation is w×h rather
than pitch×h. Drawing clips against the descriptor bounds.

### 3.8 Mapped composite: fog and unexplored on the minimap

Gate: mapped dirty bit clear means return; otherwise clear the bit and rebuild:

```
mapW2 = cellWidth/2;  mapH2 = cellHeight/2
for y in 0 .. h-1, x in 0 .. w-1:
    visIdx = (y*mapH2/h)*mapW2 + (x*mapW2/w)       truncating integer scale
    word = mapping word mask[visIdx]
    if word bit (1 << (localPlayer & 31)) is set:
        if local byte grid[visIdx] == 0:  out = guiRemap[src]   explored but currently unseen → tinted
        else:                             out = src             currently visible → raw picture byte
    else:
        out = fogFillIndex                                     unexplored → solid fill
    write out; advance both src and dst
```

The order is word → byte → remap: the word bit decides explored, the byte grid
decides currently seen, and explored-but-unseen cells pass through the GUI
remap table (a 256-entry index-translation table populated at palette load)
instead of drawing raw. Unexplored cells take the fog-fill palette index, the
same dark index the viewport fog cache uses as its default fill. The composite
shares the LOS grids with gameplay visibility but is a distinct presentation:
it never writes the word mask, and it sets the final-dirty bit when done.

### 3.9 Contacts pass on the minimap

Layer order on the final surface (later layers overwrite; no blending):

1. wipe final from mapped;
2. regular unit blip from the FX `radlogohigh` GAF, drawn when the visibility
   gate passes AND the unit's per-instance blink-suppress byte reads zero OR
   the blink phase bit is set — so the blip draws when
   `blinkSuppressByte == 0 || blinkPhase`. The byte is a per-unit countdown,
   decremented each tick while nonzero, that forces the blip into blink-only
   mode while it runs; the earlier "(hidden byte nonzero)" wording was
   inverted — it is the byte reading zero that admits the blip;
3. commander blip from the FX `nuclogo` GAF, frame 0, when the unit's identity
   matches the commander slot held in engine root state;
4. sensor circles (radar/sonar outer, jammer) in their distinct palette indices
   via the solid-circle rasterizer (2,048 angular steps over 32 segments);
5. weapon/interceptor rings (below);
6. projectile dot, 1×1 pixel, in the projectile palette index, when the
   projectile's runtime status has bits 29 and 30 clear and its 0x40 bit clear,
   LOS-gated; otherwise a feature marker from the FX `h2oboom2` GAF.

The three GAF handles above are loaded from the FX archive during battle-data
initialization. A regular unit's owning-player record supplies the frame
selector for `radlogohigh`; the feature branch uses the same owning-player
selector for `h2oboom2`. The commander marker always selects frame 0 of
`nuclogo`. The selected frame bytes are copied as indexed pixels through the
GAF blitter, so they already refer to the active `PALETTE.PAL` and are not
recolored through `GUIPAL.PAL` or a separate blit color argument. **Established.**

**Blip gate.** The unit blip draws when any of: a global options word bit 9 is
set, the minimap mode word's low two bits are zero, the unit carries the
friendly-contact status bits (mask 0x300), or the unit's owner is the local
player.

**Projection.** Each unit's 16.16 world position is first narrowed to its
signed 16-bit map-pixel components (the same narrowing contract as section
3.2), then:

```
rx = unitMapX * RadarW / PlayRight         truncating
ry = (unitMapZ - (unitMapY >> 1)) * RadarH / PlayBottom   half-height shear
```

A second pass repeats the same projection over the projectile/feature list.
The list is the entry-captured projectile/feature span (pointer and count in
engine root state, fixed-size records): one shared list written by the
projectile/feature capture path and read by the sensor first pass, the contacts
pass, and the pool walker — it is not partitioned per consumer
(bounded-negative). A candidate is admitted through the mode-selected local
player visibility source at its projected cell; when that source does not
admit it, the candidate's owner-local identity is the bypass. After admission,
the runtime status mask selects the art family: a zero value for bits 29 and
30 takes the projectile-dot path (which is then suppressed if bit 0x40 is set),
while any bit in that mask takes the feature-marker path. **Established.**

**HOT list.** Every unit visited in pool (ascending-slot) order appends a
10-byte entry — id, originX + rx, originY + ry, and two pad shorts — to the
HOT RADAR list. The list allocation is 100 bytes per scenario unit-definition
(10-byte stride, so capacity is maxDefs·10 entries; stock maxDefs ≈ 250 gives
2,500). **Bounded-negative.** No capacity check precedes the append; retail
relies on the active unit count never reaching capacity by construction. A
reimplementation must enforce the cap itself rather than reproduce the
unbounded write.

**Blink.** A per-host-frame ticker decrements the countdown 7..0, reloading to
7, and toggles the blink phase bit every 8 frames. Units whose per-instance
blink-suppress byte is nonzero (a per-tick-decremented countdown) show their
blip only while the blink phase bit is set.

**Weapon/interceptor rings.** A unit-definition flags dword bit 29 enables the
ring loop, which walks the unit's three weapon slots; per slot, a
weapon-definition flags dword bit 30 admits the ring. Radius:

```
ringRadius = RadarW * (weaponRange - 512) / PlayRight     truncating
```

with the authored per-slot range reduced by the constant 512 bias before
scaling (short-range weapons can therefore produce a negative radius, which
clipping drops). When the slot's interceptor flag byte is zero the ring is
solid via the solid-circle rasterizer; otherwise it is dashed, and the
dashed-circle routine receives the same radius, the ring palette index, a
literal 32, and the blink phase bit. **Established.** Both variants use the
ring palette index. **Established.** The literal 32 is the segment count: the
dashed routine divides the full circle into `0x10000 / 32 = 0x800`-unit angular
steps and draws alternating one-segment dashes and one-segment gaps — a
segment is emitted only when `(segmentIndex + blinkPhase) & 1 == 1`, so the
dash parity is seeded by the blink phase bit and flips per segment. The dash
pattern is fully determined; the earlier `TODO(question)` is closed.

**Selected-unit circle gate correction (Established).** The previous
“no-radar” label was wrong. In the contact pass, the selected/range-status bit
must be set, and circles are then drawn when the unit instance is active OR
the definition's `onoffable` bit is clear. The unit parser stores `onoffable`
in definition flags bit 2 (`0x04`); it does not load a unit `noradar` key.
Thus an on/off-capable unit must be active for this selected-unit circle
branch, while a unit without `onoffable` may draw its selected-unit circles
regardless of activation state. The cloak/hidden instance bit and the
definition `stealth` flag are not this callback gate. The blip gate remains
independent: no `onoffable` or cloak test suppresses a blip once its visibility
and blink conditions pass.

**Contact layering and ring-only cases (Established).** The ascending unit
pass draws at most one regular blip and, for the commander identity, one
additional commander marker; it does not draw duplicate regular blips. Circles
and weapon/interceptor rings are emitted later in that same unit iteration, so
they overwrite earlier contact pixels where opaque. There is no independent
ring-only contact list. A visible unit can appear ring-only when its regular
blip is suppressed by the per-unit blink countdown on a non-blink phase, while
the range/ring branches still run. Every admitted unit still contributes one
HOT entry after those presentation branches.

**Start-position markers.** **Bounded-negative.** No start-marker GAF and no
START string literal is found near the minimap build or contacts paths. The
logical layer between the mapped wipe and the unit blips — so that contacts
overwrite markers — is **supported inference**. `TODO(question):` the marker
blit site has not been traced.

### 3.10 Sensor circles on the minimap

The per-tick sensor phase (semantics in section 3.4) runs only when the active
player count is at least 2. Its minimap output: the outer radar/sonar circle at
max(radar, sonar) in the radar palette index, plus separate radar-jam and
sonar-jam circles in the jammer palette index, all three emitted through their
callback tables. On the minimap the radii scale as `RadarW * distance /
PlayRight` (truncating), the circles land on the final surface and are wiped
with it each tick, and the phase never ORs the mapping word mask.

### 3.11 Lens: minimap ↔ world mapping

World → radar: `rx = worldX * RadarW / PlayRight`, `ry = (worldZ - worldY/2) *
RadarH / PlayBottom`. Radar → world: `worldX = (radarX - originX) * PlayRight /
RadarW`, `worldZ = (radarY - originY) * PlayBottom / RadarH` — both truncating.

Click handling hit-tests the inclusive minimap rect first. When the click
misses the rect, or the drag-mode flag is set, the **drag branch** applies:
clamp the mouse to the viewport rectangle, then new camera = stored camera +
(clamped mouse − viewport origin) — the camera moves by the mouse delta.
Otherwise the **lens branch** writes the projected world point directly as the
new camera origin: `cameraX = (mouse − rect origin) · PlayRight / RadarW` and
`cameraZ = (mouse − rect origin) · PlayBottom / RadarH`. Either way the result
is clamped to the play area and the terrain height queried at the result. The
lens inverts the minimap projection directly; it does not reuse the main
view's cursor-to-world projection.
**Correction to the prior wording (Established).** An earlier sentence in this
section said that the lens branch recentered by subtracting half the viewport.
The direct-static trace resolves that as incorrect: the lens branch performs
only `(mouseX − letterboxOriginX) · PlayRight / RadarW` and
`(mouseY − letterboxOriginY) · PlayBottom / RadarH`, truncating each operation;
there is no `−viewSize/2` term and no mapWidth/mapHeight scale. The drag branch
alone adds the clamped viewport delta to the stored camera. Document 07's
recenter formula is the corresponding stale reading and is corrected there.

### 3.12 Viewport marker (composer-time)

The camera marker on the minimap is drawn at composer time, not in the contacts
pass. When the minimap mode byte equals 2, two 1-pixel Bresenham lines are
drawn in the viewport palette index: a horizontal segment and a vertical
segment crossing at the camera-derived radar position offset by the constants
+128 in X and +32 in Y, each segment spanning ±2 pixels about the crossing
(five pixels long). The lines clip to the inclusive minimap rect. **Established.**
The two line calls are `(cx+126, cy+32) → (cx+130, cy+32)` and `(cx+128, cy+30)
→ (cx+128, cy+34)` where `cx = cameraCenterX − camX` and `cy = cameraCenterZ −
(cameraCenterY >> 1) − camZ` — the camera centre projected with the same
half-height shear as world drawing — so the figure is exactly the five-pixel
cross of two one-pixel lines, and the endpoints are literals, not an inference.
The color is the ring/viewport palette index held in engine root state — the
same index the weapon/interceptor rings use — distinct
from the radar-circle and jammer indices. **Bounded-negative.** The minimap rect
is written by exactly one routine (the surface allocator); the composer only
reads it — no hidden second writer. A capture probe is retained only for visual
confirmation of the figure, which the calls fully determine; the earlier
`TODO(question)` on figure and thickness is closed, and thickness claims of 6/4
pixels belong to the selection brackets, not this marker (the marker is 1-pixel
lines). (Corpus trail: minimap note §5.5, viewport note §2, and the composer
decompile; the corresponding resolution note is r03-03 §4.)

**Mode-byte source (Unknown).** The composer-time marker condition is
established as an equality test against value 2, but the available clean-room
writer census found no simulation, session, mission, or map input that writes
this distinct minimap mode byte. The existing battle input mode is a separate
UI routing value and is not evidence for the marker condition. Nanolathe keeps
the value as an explicit authoritative session input and publishes it unchanged
through the frame; zero is therefore an explicit mode-off value until a traced
writer is available. `TODO(question):` identify the retail mode-byte writer or
the authoritative setup value that selects 2; do not infer it from HUD state.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

| Mode (W×H) | Viewport `left,top,right,bottom` incl | Viewport `W×H` | Pitch `(W+3)&~3` | Status |
|---|---|---|---|---|
| 640×480 | 128,32,639,447 | 512×416 (`W-128 × H-64`) | 640 | Established (direct-static) |
| 800×600 | 128,32,799,567 | 672×536 | 800 | Established |
| 1024×768 | 128,32,1023,735 | 896×704 | 1024 | Established |
| any×any hidden | predicted 0,0,W-1,H-1 → W×H if hidden expanded, else 128,32,W-1,H-33 if retained | — | — | `TODO(T23)` (bounded-negative, no writer) |

### 4.2 DirectDraw fullscreen backend

The fullscreen path calls `DirectDrawCreate`, sets cooperative level on the game
window, requests the configured width/height at 8 bits per pixel, creates a
primary/backbuffer surface, attaches/installs a palette, composites into the
surface, and presents with DirectDraw surface blit/flip calls. The observed
surface descriptor is the legacy 108-byte form with primary/backbuffer flags;
exact flag naming remains medium-confidence, but lost-surface recovery is
documented: the surface blit/copy wrappers retry on the lost-surface error
through a restore callback held in the surface record, so a lost surface is
restored and the blit replayed rather than dropped.

The GDI DIBSection descriptor (`w,h,pitch,bits`) and a primary lock descriptor
mirroring the OFFSCREEN surface record are held in presentation state; GDI
present clones the descriptor through one routine, DirectDraw fullscreen locks
through the surface vtable's lock entry and verifies the descriptor's negotiated
width/height match before presenting (direct-static for the clone path,
bounded-negative for a second viewport-subrect writer; hidden-panel paths
remain `TODO(T23)`).

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

`LHT` never darkens; darkening is through `SHD` rows 0–14. The `discByte→level` mapping is established (direct-static for the byte thresholds — the flash-disc precompute was re-exported and verified in 2026-08-26): the disc canvas is filled per pixel with one CRT draw each, `R = trunc(CRT*10/0x8000)` in `0..9`, radial distance `sqrt(1.33*dx*dx + dy*dy)` (floating-point square root, the 1.33 ellipticity factor applied to the x-axis term), `q = trunc((R + sqrt)*32.0)`, then the stored byte is `0x6F − q` while `(0x20 − q) mod 256 < 0x20`, `0x6E` while that byte-compare lies in `0x20..0x21`, and `0xFF` from `0x22` up — the 0xFF band is what makes the disc's outer area transparent, the visible region being the band where `q mod 256` lies in `0..31`. The halo level is `clamp(discByte − 0x50, 0, 31) = 31 − q` bright centre to rim, and the disc is drawn to screen as radial spokes from angle `0x800` through `0x10000` in steps of `0x800` through the sin/cos helper pair (the same angular step as the minimap circle rasterizers). (This corrects the earlier parenthetical "0x6E ring for alpha==0, 0xFF for alpha>=33" and "q = trunc((R+sqrt)/cx*32)": the ring condition is the byte-compare window `0x20..0x21` on `(0x20−q) mod 256`, the 0xFF threshold is that compare at `0x22`, and the multiplier is ×32 with no division.) Multi-tick fading envelope remains presentation tuning; the 32-row/256-column layout, the near-identity row 0, the +51.51 bright end, and the exclusive flash binding are direct. The two brightening ramps overlap: `LHT` row 3 and `SHD` row 16
both lift mean luminance by +6.83, `LHT` row 5 and `SHD` row 17 both by +12.60,
but the files are distinct and neither is synthesized from the other.

#### 4.3.2 SHD shading

Indexed texture pixels may be passed through an `SHD` row for model face
lighting; flat-colored 3DO primitives and laser lines bypass `SHD`. Team/logo
textures select the player-specific frame before palette/shading lookup. `SHD`
rows 0–14 darken (row 0 near-black, only index 0 survives; mean −97.59),
row 15 is near-identity (232 of 256 self, mean +0.17), and rows 16–31 brighten
past identity to +53.53 at row 31 — a full signed ramp that `LHT` does not
replicate. The `SHD` row selection is now established (direct-static):
`dont-shade` pins row `15`, else `row = trunc(dot*5.0) & 0x1F` with
`L=(-0.8,1,0.25)`, wrapping negatives; the row interpolates gouraud-style as
`(rowR-rowL)/width` for both fixed and mobile textured paths, sampling
`SHD[row*256+texel]` per pixel; the flat path bypasses `SHD` and fills the span
directly. Identity row `15` and the 32-row layout remain direct; the `1=NW`
corner→bit mapping remains supported inference pending probe.

#### 4.3.3 Gray table construction [R-RR16-A §1]

**Established.** The gray table is a 256-entry shared block ("GRAY TABLE",
alongside the 8,192-byte "SHADE TABLE" and "LIGHT TABLE" sibling blocks) filled
by the palette-install builder:

- For each palette index `i` with RGB `(r,g,b)` (1024-byte PALETTE.PAL,
  stride 4): `avg = (r+g+b)/3` truncated toward zero.
- Target color is `(avg,avg,avg)`. The builder scans candidate indices `0..255`
  in order, restricted to entries whose RGB sum lies in `[3*avg−40, 3*avg+40]`
  (below the window: skip; above: stop the scan entirely), keeping the strictly
  smaller squared RGB distance `dr²+dg²+db²`; ties keep the lowest index. If no
  candidate fell inside the window, the exit loop counter is kept.
- The result is stored as `grayTable[i]`.

Net effect: fogged-but-explored tiles show their terrain desaturated through
the nearest gray palette entries; texture is preserved (see §3.3 [R-RR16-A]).

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
signed 16-bit tick delta and can cross multiple frames in one invocation while
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
provide anchors, height, and footprint metadata for feature sprites and
shadows. This section is the consolidated retail contract for feature
presentation: definitions, placement sources, composer staging, screen
anchoring, the visibility/memory/fog gates, animation, and shadows. The
authoritative feature lifecycle — reclaim, capture, burning, sinking, economy
yields — is owned by document 05 ("Feature reclaim", "Feature catalog and
placement", "Feature burning").

#### 5.1.1 Definitions and asset resolution

Feature definitions are authored in TDF under `features/<group>/*.tdf`, one
section per type. Discovery is recursive; the retail install ships 17 groups:
`acid`, `all worlds`, `archi`, `corpses`, `crystal`, `desert`, `green`, `ice`,
`lava`, `lush`, `mars`, `metal`, `moon`, `slate`, `urban`, `water`,
`wetdesert`. Every file contributes one catalog entry per top-level section;
compiled records sit at a fixed 256-byte stride with a fixed field vocabulary
(the Feature record, `[02 "Feature record"]`). Linking of successor hops
`featuredead`, `featurereclamate`, and `featureburnt` runs as a second pass
after all sections are parsed; a missing link stores the sentinel `0xFFFF` and
ends the chain rather than substituting. Definitions are compiled once and
never mutated thereafter except for late resolution of names to indices.

Keys that matter for rendering: `object` vs `filename` (mutually exclusive
asset selector), `seqname` / `seqnameshad` (idle sprite/shadow), the
`seqnameburn` family, `seqnamedie`, the `seqnamereclamate` families,
`animating`, `animtrans`, `shadtrans`, `height`, `footprintx`/`footprintz`,
`nodrawundergray`, `sparktime`, `burnweapon`, `flamable`, plus the placement
vocabulary (`blocking`, `reclaimable`, `autoreclaimable`, `indestructible`,
`geothermal`, `spreadchance`, `reproduce`, `reproducearea`,
`metal`/`energy`/`damage`).

Two visual families exist:

- **Sprite / GAF class** — indicated by a `filename` string with no `object`.
  The loader resolves `filename` through the GAF set alias (for example
  `trees`, `rocks`, `greenvents` / `geotherm`) and then resolves each named
  sequence (`seqname`, `seqnameshad`, the `seqnameburn` family, and so on) to
  a handle that identifies the entry. When `animating` is set the handle is
  copied into a per-instance cursor store and advanced each sim tick;
  otherwise the handle is used statically. All stock metal deposits and all
  trees/shrubs on Great Divide are of this family.
- **3DO / model class** — indicated by an `object` name with no `filename`.
  The loader resolves the name through the object directory. At draw time the
  shared FeatureUnit placeholder is filled with the feature's model pointer
  and world position and dispatched through the 3DO model path. None of Great
  Divide's initially-placed features use this path; corpses and later wrecks
  do.

Missing assets do not crash: a null GAF handle or null object pointer causes
the per-cell dispatch to return without drawing. Stock GAFs are valid; only a
malformed install exercises the early-out.

#### 5.1.2 Placement sources and the single stamper

Retail funnels every feature placement through a single stamping service. It
validates the anchor rectangle against map bounds (`anchorX + footX ≤ mapW`,
and the same for Z), checks footprint collision by attempting a conditional
teardown of any overlapping occupant (guarded by `indestructible`), stamps the
anchor `feature = featureId` and the fringe cells `0xFFFE` with signed anchor
deltas, and notifies derived occupancy. That service is the only writer of the
plotted footprint; the invoking paths are responsible for ordering.

Four sources feed the stamper:

- **TNT tile attributes** — the map file's attribute grid supplies height and a
  feature reference per cell. The four-byte on-disk cell (`height:u8`,
  `feature:u16`, `unk:u8`) expands into the runtime 13-byte plot cell with the
  feature word at offset 8 and sentinels `0xFFFF` empty, `0xFFFE` fringe,
  `0xFFFD` void hole, and `< 0xFFFB` live index. Fringe offsets are signed
  8-bit deltas scaled by map width where required. On **Great Divide** this is
  the dominant source: 2,552 real cells out of 40,960 attribute cells, drawn
  from a 17-entry feature table.
- **OTA mission features** — `Number of Normal / Animating / 3D Features`
  partitions are stamped after the type-name indirection `[Feature Type Names]`
  maps OTA-local indices to global catalog indices. Great Divide carries none;
  campaign maps exercise this path.
- **Unit-death corpses** — the dying unit's `Corpse=` FBI link followed by the
  `featuredead` chain (depth derived from kill severity) is stamped at the
  victim's cell. The placement is subject to the same bounds/collision checks
  as map features and fails silently when blocked.
- **Burn / reclaim / damage successors** — a live feature transitions
  atomically to its successor (`featureburnt` after a fire finishes,
  `featuredead` after lethal weapon damage, `featurereclamate` after
  successful reclaim). The transition is removal followed by re-stamping at
  the same anchor, preserving world position when the predecessor had an
  animation slot. Sinking wrecks pass their submerged descent velocity to the
  successor.

**Reproduction** uses a global cursor descending from `mapW*mapH-1`, visiting
one cell per sim tick; each visited cell draws one simulation-RNG value even at
probability zero. Stock maps are inert because every shipped definition
authors `reproduce=0`.

#### 5.1.3 Composer staging and pool membership

The frame composer stages through ten fixed-order barriers (section 1). Feature
work sits outside the strip abstraction:

- Strips `0..2` (unconditionally) and strips `3..4` (unconditionally) flank a
  feature pass that owns the unexplored-marker state. That pass loops the plot
  row-major, clears the never-seen marker, and dispatches visible features
  through the per-cell dispatcher. A second, interleaved pass walks screen-Y
  bucket rows mixing soft units and deferred tall/shadow features so that
  feature–unit overlap is painter-ordered by projected Y, not by map order.
- The ten-strip strip objects are confined to the strip dispatcher and capped
  per strip; the fixed 300-record effect pool sits between strips 6 and 7
  behind the same mode gate. Features are not strip objects and are not
  subject to the 401-record eviction rule; their fixed-slot pool is capped at
  2,048 (`0x800`) entries and allocated through a dedicated freelist.
- Fog is staged last, after all strips, projectiles, and effects but before
  selection and interface, so fog tints world drawing including features but
  never the selection rectangle.

#### 5.1.4 Screen anchor, footprint centering, and height averaging

A feature's screen anchor is derived from its footprint center plus its cell
origin:

```
screenX = footX*16/2 + (cellX + 8)*16 - cameraX
screenY = footZ*16/2 - (h0+h1+h2+h3)/8 + (cellZ + 2)*16 - cameraZ
```

Heights `h0..h3` are the terrain height bytes of the covered cells (current
cell, next-X, next-Z, diagonal). The `>>3` average of the four height bytes is
the mean height halved — the same half-height shear the unit path applies as
`worldY >> 1`, at map-pixel scale. The `+8` on X and `+2` on Z fold in the
viewport left and top offsets (128 and 32, §2.5, §4.1). Footprint
centering (`foot*16/2`) puts a `3×3` metal deposit centered over its anchor
rather than straddled on it; `1×1` trees are pinned to their anchor cell. The
GAF frame's own `xOff`/`yOff` are then applied and clipped to an inclusive
rectangle; fully off-screen features are culled whole.

#### 5.1.5 Visibility, memory, and fog interaction

Per-frame visibility for features is a predicate-consensus:

- Short features (`height < 10`) are tested against memory/fog. Features that
  do not carry the `nodrawundergray` flag draw unconditionally once explored.
  Features that do (Great Divide has none of these on its native table, but
  wall/fort types do) draw only when the plot's placer nibble equals the local
  player slot or when the two-corner LOS test passes — first corner at the
  sheared cell origin, then a single corner displaced by the footprint offsets.
  (This corrects the earlier "four-corner LOS test" reading in this section:
  the feature predicate evaluates two corners, per the traced predicate; the
  section 3.2 cross-reference is now consistent.) The placer nibble is
  stamped as `(placer & 0xF) << 3`, with a nibble of 10 for map-authored
  features and the owning player's slot for corpses, so owned wrecks are
  remembered. This corrects the older §5.1 reading of a "team-memory nibble":
  the memory accept is keyed to the placer nibble and applies only to features
  whose definition carries `nodrawundergray`, which is also the answer to the
  open question in "Missing and unknown" about which feature-definition flag
  gates the owner-memory accept.
- Tall features (`height >= 10`) force the never-seen marker — bit 2 (value
  `0x04`) of the plot cell's flag byte at offset 12 — regardless of LOS,
  casting a persistent unexplored shadow over their own cells. Their drawing
  still passes through the same memory-or-LOS gate.

These mechanics are the ones section 3.3 adjudicates for the flag byte
(live-instance bit 0, authored blocker bit 1, never-seen bit 2, placer nibble
bits 3..6); the consolidated contract adds the height threshold of 10 and the
`nodrawundergray` gating of the memory accept. The draw-versus-marker order is:
the feature pass **clears** the never-seen marker per cell before evaluating; a
skipped hidden feature leaves it set; tall features re-mark their own cells
unconditionally; then drawing is decided by the memory-or-LOS gate. (The older
§5.1 reading — check visibility, then set a "marker for a later frame" — had
the clear/draw order inverted.)

Fog presentation afterwards draws a hard 32-pixel overlay from the visibility
grid, leaving unexplored cells dark (section 3.3). The feature passes themselves
are the authors of the per-cell never-seen bit. The corner-count
`TODO(question)` that pitted the two-corner form of section 3.2 against the
four-corner reading here is resolved: the feature predicate is two-corner, and
this section now records it as such.

#### 5.1.6 Animation and the burning lifecycle

- **Static (`animating=0`)** — normal and shadow are the first frame of their
  respective sequences. No cursor is stepped. This matches every tree/shrub
  and metal rock on Great Divide.
- **Animated (`animating=1`)** — the loader pre-wires two 12-byte cursors
  (normal and shadow) from the idle sequences and the global tick advances
  both via the single-step helper each sim tick. Geothermal vents on Great
  Divide are of this family and therefore cycle independently of view. Ship
  burned-death and reclaim sequences are forced non-looping at load so the
  shipped 46–282-tick finite lifetimes are honored.
- **Burning** — ignition selects the burn and burn-shadow sequences with a
  one-shot countdown derived from `sparktime`. The burn animation is advanced
  every tick until its cursor clears, at which point the cell is cleared and
  the `featureburnt` successor is stamped if one exists. The countdown is the
  established one-shot `simulationRandom(sparktime / 2) + (sparktime / 2)`
  (one Park-Miller draw of `sparktime/2` plus `sparktime/2`), owned by
  document 05's fire contract (`[05 "Feature burning"]`, mirrored in `[06]`
  weapon firestarter handling); the earlier deferral `TODO(question)` here is
  closed.
- **Clipping and transparency** — GAF drawing composes subframes clipped to
  the parent canvas; later opaque pixels overwrite earlier ones; skip commands
  are the sole transparency mechanism. Shadow drawing may select the
  translucent blitter when `shadtrans` is set.

#### 5.1.7 Great Divide reference world

Header `0x2000` canonical, 160×256 cells (2,560×4,096 map pixels), sea level
45, 2,820 tiles, 17-entry table. OTA is schema-only; no added features. Table:
six tree variants (`Tree1..Tree6`), three shrubs (`Shrub1..3`), two pre-burnt
trees (`Tree1Dead`, `Tree2Dead`), four metal deposits (`RockMetal`,
`RockMetal1..3`), one geothermal vent (`Geothermal`), one hurt rock fragment
(`Rock1a`, exercising a 4×3 footprint). Definitions live in `features/green/`
(green-world). No initially-placed feature carries a 3DO `object`; all are
sprite-class (`filename` present) except later corpses.

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

Texture mapping is corner-index affine 16.16 (direct-static): quads map index order `0→(0,0) 1→(1,0) 2→(1,1) 3→(0,1)`; n-gons 5–16 are `n`-edge affine polygons through the edge-table scanline mappers (ten-dword edge records) with per-edge `(dx<<16)/dy`, per-scanline `(uR-uL)/width` and `rowStep=(rowR-rowL)/width`, sampling `SHD[row*256+texel]` per pixel; the flat path is quads-only and fills the span directly. No stored UVs, no perspective divide (bounded-negative), clamp to `w-1/h-1`, nearest sample; transparent holes skip `SHD`. Face row is `trunc(dot(N,L)*5.0)&0x1F` with `L=(-0.8,1,0.25)`; `dont-shade` (definition flags bit 2) pins the identity row `15`; the gouraud row interpolates as `row delta/width` for both fixed and mobile textured paths (direct-static); the flat path bypasses `SHD`. Row `0x0F` is identity, rows `0..14` darken, `16..31` brighten (see §4.3.2).

#### Nanoframe reveal [R-P0-19-N]

An unfinished unit is composed exactly like a finished one, into the unit's own
offscreen indexed image, and then recoloured in place before the image is
blitted. Everything below is **Established (direct-static)**.

**The height key.** While the model rasterizes, every vertex carries a third
component beside its two screen coordinates: `key = trunc(vertexY/2) + bias`,
where `vertexY` is the vertex's whole-world-unit height above the unit origin
and `bias` is `50`, or `125` when one unit-definition flag bit is set
(`TODO(question)`: the authored name of that bit is not identified; every stock
path observed takes the `50` branch). The span filler interpolates the key in
16.16 across each scanline and writes it, truncated to a byte, into the image's
second plane — the same plane it uses as the unit's own depth test: a pixel is
written only when the stored key is less than or equal to the incoming one, so
the highest face at each pixel wins and ties go to the later face. That second
plane is what the reveal reads.

**The reveal.** With `p = trunc(remaining × 255)` from the construction
remaining fraction (`1` at request, `0` at completion, so `p` counts down), the
pass derives a sweep line `t` and a four-deep band `[max(t-4,0), t)` beneath it,
all in byte arithmetic, and assigns each composed pixel one of three verdicts by
where its height key falls: **below** the band, **inside** it, or **at or above**
the line. A verdict is either a palette index, *erase* (write the image's
background index, so the pixel does not appear), or *keep* (leave the composed
texture or flat colour). Five stages, in build order:

| `p` | sweep line `t` | below band | in band | at/above line |
|---|---|---|---|---|
| `> 235` | `(p-235)×255/20` | erase | pulse A | erase |
| `200 < p ≤ 235` | `(p-200)×255/35` | erase | pulse A | erase |
| `115 < p ≤ 200` | `(115-p)×255/85 − 1` | pulse A | pulse B | erase |
| `30 < p ≤ 115` | `(30-p)×255/85 − 1` | keep | pulse B | pulse A |
| `≤ 30` | `p×255/30` | keep | pulse A | keep |

Divisions truncate toward zero and the line is consumed as a byte, so the two
negative-line stages wrap into an ascending line. The visible result is: an
empty body swept twice by a bright line, then a solid green fill rising from the
model's base, then the texture rising from the base with solid green still above
it, then the finished texture swept once more.

**The pulses.** Two colours ping-pong across the sixteen-entry green ramp based
at `0xa0`: `pulse A` from `(unitID ^ 5) + tick×33/30` and `pulse B` from
`(unitID ^ 9) + tick×57/30`, each folded as `value & 0x10 ? 0xaf - (value & 0xf)
: 0xa0 + (value & 0xf)`. `unitID` is the unit's own sixteen-bit identifier — the
one its diagnostic text formats — so two adjacent nanoframes do not pulse
together.

**The outline.** After the recolour, every primitive of every visible piece is
overdrawn as a closed polyline in `pulse B`, with the load-time selection
primitive the one exclusion — the same primitive the raster pass skips. The
outline is not depth-tested, so the whole wireframe shows through the body. This
is why a nanoframe reads as a pulsing wireframe at the start of construction:
the body is entirely erased and only the outline remains.

**Not the reveal.** The construction fraction also forces the mobile image-cache
path and suppresses one shadow branch. Neither changes the soft/hard draw
classification or the bucket key.

### 5.3 Projected shadows and feature shadows

Options distinguish master shadows, feature shadows, vehicle shadows, and a
dithered-fog/shadow option. Unit definitions provide a `noshadow`-like control
and shadow-capability flags. The shadow GAF entry is used for feature/sprite
shadows; model shadows are projected through a separate ground pass.

The exact composer staging is (superseding the older high-level "draw terrain,
prepare feature shadows, draw projected shadows, draw units and features"
order): the feature pass runs between strips 0–2 and strips 3–4, followed by a
second interleaved pass over screen-Y bucket rows mixing soft units and
deferred tall/shadow features, painter-ordered by projected Y; per feature the
shadow blit precedes the normal blit at the same anchor (section 5.1.3).
SHD palette rows and optional dither participate in the shadow/light
pass. **Shadow presentation is now established** (this replaces the stale
"Projection coefficients, exact shadow footprint clipping, and whether all
shadow categories share one stencil are not established"):

- **Option bits.** One options word (engine root state) carries six visual
  bits: bit 1 Anti_Alias (which also gates the model-shadow stencil doubling),
  bit 2 Shadows master, bit 3 VehicleShadows (unit model shadows), bit 4
  FeatureShadows (feature/sprite shadows — read directly by the feature
  shadow gate), bit 5 Shading (enables the tinted shadow blitter), bit 6
  DitheredFog (shadow dither parity). The bulk INI key writes bit 4 then fans
  out bit 4→bit 3→bit 2 so one toggle makes all three shadow bits equal;
  the per-category keys set only their own bit, so feature and vehicle
  shadows toggle independently.
- **Per-unit suppression.** The FBI `noshadow` key writes a per-type flag bit;
  the model-shadow stencil additionally requires a runtime instance status
  bit. Ships and structures authored with `noshadow` therefore suppress the
  stencil even with the global bits set; aircraft are not exempt — no altitude
  test exists in the shadow path (bounded-negative), so air units cast model
  shadows when vehicle shadows are enabled.
- **Projection.** Model shadows use the same orthographic projection as
  bodies: `screenX = worldX − camX`, `screenY = worldZ − (worldY >> 1) − camZ`
  with the viewport constants folded in — the height dependence enters as a
  palette-index term, base 0x32 (50) on land, 0x32 + 0x4B = 0x7D (125) on
  water, plus half the world Y. Feature sprite shadows sample the terrain
  height under the anchor: the four plot height bytes (current cell, its
  +X neighbour, the anchor cell, the anchor's +X neighbour) are averaged with
  `>> 3` (the sum of four bytes divided by 8 — the half-height shear at
  map-pixel scale) and subtracted into the screen Y.
- **Two raster families.** (A) GAF sprite shadows (features) blit the shadow
  frame through the opaque or the tinted blitter (the tinted path selected by
  the definition's translucent flag and gated on the Shading bit), clipped by
  an inclusive-rect intersect. (B) Model shadows build a doubled stencil image
  and compose it over the ground with a per-pixel depth compare
  `dstDepth <= srcDepth + bias` — equal-height overlaps coalesce to a single
  darken rather than double-darkening — and darken through an `SHD` row
  (`result = SHD[row*256 + groundIndex]`, near-black row); the dither variant
  seeds a checker stencil with the 0x01010101 pattern and a screen parity
  term. ALP is not used in either shadow family (bounded-negative).
- **Order.** Per bucket row the shadow is drawn before the body for both the
  soft and hard unit traversals, and per feature the shadow GAF precedes the
  body GAF; all shadow work sits between the terrain tiles and the units/
  features, and the fog overlay (after all strips) covers shadows like all
  world drawing. The feature-memory marker makes a tall feature's shadow
  persist independent of current line of sight.
- `TODO(question)` residuals retained: the exact water-flag bit semantics
  (the 0x4B offset's source definition bit), the exact darken row identity
  within SHD, the interplay between the dither option and the shading gate,
  and aircraft altitude versus ground-projection distortion.

#### 5.3.1 Feature shadow selection and blit order

Shadows are a global option packing (master shadow plus feature/vehicle
shadows) and a per-definition `shadtrans` flag. When enabled, each feature may
emit up to two blits — a shadow frame followed by a normal frame — at the same
anchor. Static features select `seqnameshad`; animated features use the
runtime shadow cursor copy pre-wired at load; burning instances use the slot's
shadow cursor. The shadow path respects the same clipping as the normal path;
`shadtrans=1` selects the translucent darkening blitter. On Great Divide
trees, rocks, and the vent all carry `shadtrans=1`, so their shadows are
translucent. This refines the statement above with the exact selection rules;
the earlier `TODO(question)` on projection coefficients, footprint clipping,
and stencil sharing is closed by the subsection above (the two raster
families are separate primitives; the model family's inclusive-rect clipping
and per-pixel depth compare are direct evidence).

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
positions.

**Correction (2026-08-27).** This section previously read "the segment color is
the fixed palette index 6 (established, direct-static) for both reclaim/capture
and build-assist emissions, while the per-segment fade/lifetime remains
`TODO(question)`." That was wrong on both counts. The literal `6` those producers
pass is the **strip selector**, not a colour — the same number doc 05 closes as
"selector 6 and strip 6 are one number" — and the record carries no colour field
at all. There is also no fade: a nano record is a particle emitter whose
particles carry their own colours and lifetimes, described next. Nothing in the
executable writes a palette index 6 for a nano segment.

**The nanolathe spray [R-P0-19-P].** Established (direct-static). A nano segment
record is an emitter, not a line. It is constructed from a source **point** (the
`QueryNanoPiece` world position, passed as a degenerate box) and a target
**box** (the target's world bounding box: the target's position plus the two
bounding-corner triples its definition stores next to the model top). Both boxes
are immediately narrowed, per axis, to the span between their `4/11` and `7/11`
interpolants and stored as origin plus extent — so a particle's landing point is
drawn from the middle three elevenths of the target's box, and that narrowed box
is what gives the spray its cone.

Every tick, a record spawns **five particles**, each costing **six CRT draws** —
three to pick a point in the source box and three to pick a point in the target
box, each as `origin + rand()×extent/0x8000`. The draws come from the **CRT
presentation stream**, never the simulation stream, so nano presentation cannot
perturb lockstep. A particle's lifetime is `trunc(distance/4)` ticks, taken as a
signed sixteen-bit count from a floating-point distance: it travels four whole
world units per tick, and a zero-length hop is discarded before the particle is
written. Its colour is `0xa0 | nibble`, the nibble starting at `1 + (spawn index
mod 7)` and advancing by one every tick, wrapping seven back to one — a shimmer
up the green ramp `0xa1..0xa7`, never `0xa0`.

The record's own spawn window closes one tick after creation, so each accepted
work step contributes **ten particles over two ticks**; continuous construction
therefore holds two live records and ten new particles per tick. Per tick the
record's update advances each particle, drops the ones whose expiry tick has
passed, and the record itself is destroyed once its particle list empties.
(2026-08-27 cross-confirmation [R-STRIP-01 §2]: the spray record is the
strip-6 container object of the strip lifecycle — the emitter is a pooled
strip object whose update advances, expires, and refills its internal
particle list, and the emitters evict under the common 401-record rule. Every
number above was re-verified against that path, including the `0x100` word.)

Each particle draws at its world position through the ordinary projection,
gated by the local player's coverage at its own projected tile — the same
one-point gate the projectile path uses. The draw fills the rectangle from the
particle's pixel to one pixel right and down, and the rectangle filler is
**inclusive on both edges** (its span width is `right - left + 1` and it runs
`bottom - top + 1` rows; the clipper's reject tests are inclusive to match), so
a particle's mark is **two by two**, not one pixel. At one pixel the spray reads
as a thin dotted line rather than the dense cone retail draws — the four-fold
difference in coverage is what makes it look like a spray at all. The particle
record carries one further field, set to `0x100` at spawn and read by nothing
observed; its purpose is `TODO(question)`.

**Nanolathe presentation pipeline [R-P0-19]:** Construction and reclaim work
producers route their nano events through the beam-family strip-6 identity and
mark the segment geometry authoritative (source = `QueryNanoPiece` world
position, target = product/footprint anchor). The fixed effect pool preserves
the strip destination, so the client's strip-6 nanolathe draw branch fires
instead of skipping the beam. One segment is emitted per accepted work step
(mobile/factory construction and reclaim), matching the per-path cadence of
[R-P0-06 §1]; build assist's two-segment cadence is separate.

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
cameraX += rand() * sx / 0x8000 - (sx / 2)     (sx / 2: signed, truncating)
cameraY += rand() * sy / 0x8000 - (sy / 2)     (sy / 2: signed, truncating)
remaining -= 1
```

**Correction (2026-08-27).** The two jitter lines previously read
`- (abs(sx) >> 1)` and `- (abs(sy) >> 1)`. That magnitude form is wrong: the
binary forms the half term with the compiler's signed truncating-halving idiom
(add the sign mask, then arithmetic-shift right by one), which is `sx / 2`
rounding toward zero. The two readings differ exactly when the amplitude term
is negative and odd — `abs(sx) >> 1` floors while `sx / 2` truncates, an
off-by-one on the negative side. Established (re-verified by direct
instruction read, 2026-08-27, confirming the 2026-08-27 R-CORE-01 packet's
finding). Consequence 1's "-abs(s)/2" phrase below must be read as "-s/2":
for odd negative displacement terms the noise band is asymmetric by one unit;
all other claims in this section were re-verified in the same read and stand
(request blending, exactly two CRT draws per active tick and none on the
expiry tick, the linear-decay envelope, the in-place permanent camera
mutation, and the clamp + follow-glide damping).

Three consequences matter:

1. The envelope is a **linear decay with uniform white noise**, not a sinusoid
   and not an exponential. The `-s/2` term (signed truncating, see the
   correction above) centres each axis.
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
rendering invocation census, not proof that every un-decompiled helper lacks one.

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

Initialization failure handling is exact: a device-allocated failure
(`DSERR_ALLOCATED`) disables the DirectSound path and selects the waveOut
backend; a waveOut initialization failure shows the message box "Sound system
initialization failed."; the `UseWindowsSound` key sets the no-DirectSound flag
as well, so every cue thereafter plays through the Windows sound API. CD audio
initialization is always attempted independently of the sound-system result,
including after a failed waveOut initialization.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

### 8.2 WAV decoding and cache

**Container detection.** The loader reads magic words at fixed file offsets and
classifies each sample as one of three kinds:

1. **DIGI** — when the DIGI magic words are present at file offsets 0, 8, and
   32. The rate word is read at file offset 22; a rate of 11,000 is remapped to
   11,025. The sample payload is the file size minus the 10-byte wrapper, read
   from the SDAT region; the format is 8-bit mono.
2. **RIFF/WAVE** — when the RIFF and WAVE magics are present at file offsets 0
   and 8. The chunk walk starts at file offset 12 and advances chunk to chunk
   by the 8-byte header plus the chunk size, with odd chunk sizes padded to
   even (one pad byte), bounded by the chunk-list end. The `fmt ` chunk must
   carry at least 16 bytes or the decode fails; its fields are little-endian:
   format tag at offset 0, channels at offset 2, sample rate at offset 4, byte
   rate at offset 8, block align at offset 12, bits per sample at offset 14. A
   matching walk locates the `data` chunk; a missing or empty `data` chunk
   fails the decode. A data size that is not a multiple of the block align is
   not validated — retail still plays, truncated.
3. **Raw** — any file matching neither detector plays as unsigned 8-bit mono
   audio at 11,025 Hz; a malformed RIFF with a bad magic therefore decodes as
   raw noise rather than failing.

Every failure path (unreadable file, undersized `fmt `, missing `data`,
truncated chunks) closes the handle and returns silence — a failed decode never
crashes.

**Allocation modes.** Each decoded sample is dispatched by mode: mode 0 builds
an in-memory PCM blob (the PlaySound path), mode 1 preloads a sound buffer,
mode 2 streams (returning a sentinel handle rather than a real buffer). The
three dispatch sites and the fail-to-silence rule are established; which
aliases use which mode is a bounded-negative gap (no per-alias mode field was
found at registration) — keep `TODO(question): per-alias mode selection`.

**Caching.** Samples are cached at the alias level: one decoded PCM blob per
alias, retained for the life of the session, with no eviction beyond the alias
cap of 8.3. The secondary buffer is freed and recreated for each load — there
is no pool or LRU beyond "free the old buffer first". (A FIFO-255 presentation
sample cache in Nanolathe is a documented divergence, not retail
secondary-buffer eviction.)

### 8.3 Sound categories and eight-slot arbitration

The sound-category catalog's authored grammar, its fixed event list, and its
variant-gathering rule are specified in document 02. Each category record is
352 bytes: a name of up to 64 bytes followed by 24 event rows of twelve bytes
each, indexed by event slot, where slot zero is an unused sentinel and slots 1
through 23 are the events. A row holds a variant count and two parallel arrays
of 64-byte strings: the sound alias and its speech caption.

**Static slot table.** Alongside the categories the executable carries one
static record per slot, holding the slot index, a **priority**, a **cooldown
multiplier** (the window is `multiplier × 30` frames — the earlier "seconds"
reading coincides numerically at 30 Hz but states the wrong mechanism), the
authored key name, a default speech caption, and a mutable *next-allowed
frame* cache. Priorities and cooldowns are per slot and global across every
unit, not per category:

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
2. Draw the variant uniformly: `idx = (uint64)rand15 × count ÷ 0x8000`
   (32768), truncating toward zero, where `rand15` is the Microsoft CRT stream
   (`x' = x × 214013 + 2531011`) sampled at bits 16..30. The draw happens on
   **every** resolve — including silent ones — before any gate, so the CRT
   stream advances with queue pops, not with audible successes. Count zero
   produces no pick and the cue is silent.
3. If audible, the crowding gate `10 - audioThreshold < priority` (signed byte
   compare) passes, the row has at least one variant, the audible flag is set,
   and sound-flags bit 6 is set, play the chosen alias and set the slot's
   next-allowed frame to `frame + cooldownMult × 30`. The dispatch layer
   additionally requires the master gates — effects volume nonzero,
   sound-flags bits 0..2 nonzero, DirectSound enabled. The re-arm happens
   **only on an audible pass** — never on a silent resolve — and the codec
   performs no empty-path check, so the re-arm happens even when the resolved
   path is empty.
4. If showing text and the gate `10 - speechThreshold < priority` (signed byte
   compare) passes, take the entry's override line or the row's caption for
   the chosen variant, and print it as `"%s: %s"` prefixed by the unit name
   when the unit is still alive (its chat-enable latch bit set) and the
   caption is non-empty. Otherwise nothing prints.

When the second reentry flag (the honk/sing flag) is set, the alias is
replaced by a fixed cue: `sing` unless `(frame / 30) & 7 == 0`, then `honk`.
The writer of that flag is not located in the bounded corpus — **Unknown**, low
impact (`TODO(T23): locate the honk/sing flag writer`).

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

**Variant gathering.** For each slot the loader reads the bare key and then the
numbered keys `key1`, `key2`, ... until a number is missing. Each present key's
alias string (up to 64 bytes) and its parallel `key` + `text` caption (up to 64
bytes) are appended to the row's two arrays and the count increments. The
numbered loop runs **regardless of whether the bare key is present** — a
divergence from the spec letter in document 02, which gates on the bare key
first. The stock corpus ships no bare forms at all for the core cues (120
`select1`, 76 `ok1`, 76 `cant1`, 63 `arrived1`), and retail still counts one
variant for each via the numbered path; a strict bare-gate would mute them.
Nanolathe therefore gathers numbered keys regardless of the bare key's presence
(SC7, see `docs/SPEC_CONFLICTS.md`). The reference install's `sound.tdf`
declares 120 categories. When a variant's authored caption is absent, the
caption falls back to the slot's static default speech caption.

**Alias registration.** Aliases are registered into a flat, session-lifetime
table of 256 slots, each holding a 32-byte name and a 64-byte path: the
registry is deduplicated (a new alias whose name matches an existing entry,
case-insensitive, up to 32 bytes, returns the existing identity); the cap is
255 entries, and the 256th registration is rejected without eviction and
returns the zero identity; each alias is probe-loaded at registration time
through the VFS and the WAV decode path (8.2) with the `sounds/` prefix and the
canonical candidate tries; the resulting handle, name, and path are stored and
the count increments even if the probe yields nothing. A name lookup miss
returns the sentinel id 0xFFFF, which silences the cue. A 33-byte authored
alias truncates to its stored 32 bytes; lookups stay case-insensitive over the
32-byte field. **Precedence is VFS mount order — first provider wins** (loose
directory, then GP3/CCX, then UFO/HPI, then CD-ROM); the archive flag does not
alter precedence, and duplicate aliases on a later mount are suppressed by the
case-insensitive canonical full-path compare. A separate global alias loader
enumerates the children of `gamedata/allsound` and registers each child's
`sound` key as an alias through the same registry. Unit definitions map their
category names to category identities, with numeric fallback when a name is
absent.

**Crowding thresholds.** The two thresholds are separate bytes, both scaled by
5 from the menu gauge (range 0..10): one for audio (audible gate) and one for
speech (text gate). The gate is the signed compare `10 − threshold < priority`.
They are not per family, per alliance, or per player — the 24 slots share the
two global thresholds, and "by family" only describes how priorities group
(countdown slots 17..23 at priority 10 versus load/arrived at 7/3). Example
arithmetic: `select` (priority 10) always passes — at threshold 10 the gate is
`0 < 10`, true — while `working` (priority 2) fails at threshold 5 (`5 < 2`,
false). `underattack` (priority 9, 600-frame cooldown) and `working` (priority
2, 30-frame cooldown) exhibit different windows under the same threshold.

**Producer census.** The direct-caller census over the bounded corpus is
closed:

- **Positional/weapon** cues: six sites — the weapon root start, the water and
  land impact variants, the burst clone, the named-alias forward, and the
  network replay.
- **Named positional** (feature ignition): one site.
- **Underattack**: one site, on the damage path — emitted once per
  non-paralyzer normal damage event to a unit owned by the local player, gated
  on selection state, fixed category 2.
- **Unit voice**: 82 sites across orders, AI, and selection — category mapping:
  1 select, 2 underattack, 3/4 activate/deactivate, 5 ok (about thirty
  order-acceptance sites, gated on a runtime status bit 0x2000), 6 arrived,
  7 cant (construction failures), 8 unitcomplete, 9 build start, 10 repair,
  11 working, 12/13 load/unload, 14/15 cloak/uncloak (state-bit gated),
  17..22 countdown, 23 canceldestruct (self-destruct tick).
- **Bounded absence:** no producer exists on the unit sinking/removal path.

Global master and effects volume is observable through the wave and auxiliary
volume calls. The helper that plays positional world cues adds two established
presentation behaviors on top of that mixer. First, **audience gating**: the
source position is floor-divided by 65,536 (sign-corrected) to a plot cell; an
off-map cell is silent locally. The mode word's bit 1 then selects the
visibility source — the local viewing player's explored-memory byte grid
(nonzero byte visible, tested with the half-height shear and the grid bounds)
or the LOS word mask tested at the bit `1 << (localSlot & 31)`. The local
player slot only — no ally OR anywhere. Named feature cues are forwarded
through the same gate. Second, **viewport-relative placement**: when the sound
backend reports stereo capability the helper computes the viewport-relative
pan vector with the half-height shear:

```
dx = pixelX − ((viewW/2) << 4) − viewLeft
dy = viewTop + ((viewH/2) << 4) + (pixelY >> 1) − pixelZ
```

(the 16.16 position is narrowed to its signed pixel component first), stores
(dx, 0, dy) as a stereo mixer offset — not a 3D position — and updates the
mixer reference center to `((mapW + mapH) / 2) << 4`. Attenuation is **binary,
never a curve**: in-view sources play at −585 in the DirectSound
centibel-style volume encoding, off-screen sources at −1585 — exactly the same
two levels, with the viewport border inclusive (on the border is in-view).
Off-screen events are quieter, never discarded; a source at the viewport
center yields a zero pan vector (mixed as centered) at the same in-view level.

**Random-stream contract.** Audio uses the CRT stream only — never the
simulation Park-Miller stream. Variant picks and CD random picks share the CRT
stream, so their interleaving matters: a CD random track consumes one draw that
would otherwise be a variant pick (presentation-level; must not leak into
simulation RNG). Silent resolves still draw, so the draw count is deterministic
with queue pops, not audible successes. The only sim-stream audio-adjacent draw
is the self-destruct detonation delay (0..15 frames), which is Park-Miller and
fires only on destruction — the two streams are distinct.

Projectile start, hit, and water sounds are queued synchronously with
projectile creation and collision events. A failed projectile reservation emits
no start event. Collision ordering is: screen shake, hit or water sound,
optional end smoke, selected land or water animation, then damage and area
effect; the visual and audio event is therefore created before the final damage
mutation in the observed path.

Positional world cues can also be emitted as a network packet carrying the alias
identity and position. A nonzero broadcast flag emits a packet carrying the
opcode, sub-opcode, alias id, and three signed 32-bit position words (18 bytes
total). The bounded direct-caller census shows every in-game
gameplay producer — weapon start and burst, impact, and named-feature ignition
forwarding — passes `broadcast = 0` and arises independently from lockstep
simulation with local audience gating; the inbound dispatcher replays the packet
with broadcast cleared and does not echo — a zero flag byte re-enters the
positional path, a nonzero flag byte replays through the backend-only path;
either way the broadcast flag is cleared before replay, so every peer still
passes its own local gates. Observed nonzero-broadcast callers are
frontend and options paths. Only dynamically or externally reached callers
outside the bounded direct-invocation census remain unknown for broadcast behavior.

### 8.4 Music and CD/MCI

Music uses WinMM MCI strings for `cdaudio` open, close, stop, status, play, and
pause.

**Open and probe.** CD initialization opens the MCI `cdaudio` device once. The
open failure path is established: on a failed `open cdaudio`, the engine
enumerates windows and retries the open with the enumeration-supplied window
handle; a second failure disables CD playback. On success it issues
`stop cdaudio`, sets the time format to milliseconds (a failure here stops and
closes the device and disables CD playback), and queries the track count with
`status cdaudio number of tracks`, parsed as a decimal integer. A failed count
query leaves the count at zero and the per-frame tick idles. The probe also
builds the 100-entry track-category table with the four repeating categories
`(trackIndex mod 4) + 1` (Red Book CDs carry at most 99 tracks), initializes
the current/next track and mode/status fields, and registers the
`MM_MCINOTIFY` handler on the engine's notification window.

**Play modes and track transitions.** The CD tick runs once per frame and
returns early when the track count is zero or playback is paused. It polls
`status cdaudio mode` and compares the reply exactly against `playing` — a
failed poll is treated as not-playing and triggers a transition. There are
exactly five modes:

| Mode | Behavior |
|---|---|
| 0 idle | no play; a detected stop resets state |
| 1 sequential | advance `next` by one, wrapping modulo the track count (`(track mod numTracks) + 1`), and play |
| 2 random | play `random_draw mod numTracks + 1` |
| 3 single | play the requested track (a requested track of zero stops playback) |
| 4 category-shuffle | stop and reset, then scan up to `(draw & 15 + 1) × numTracks` candidate tracks forward with wrap for the first whose category equals the desired category; none found → stop |

Mode 4's "shuffle" is therefore a category-filtered forward scan, not a general
shuffle; a history ring of recent tracks is used to deduplicate shuffle picks
(writer details not fully traced).

The play primitive deduplicates a request for the track already playing,
applies the CD volume, and issues `play cdaudio from %i` — appending ` to %i`
when a stored playhead offset keeps the end track below the track count — plus
` notify`, with the notification window handle. An MCI error at play leaves
the status at playing; the next poll detects the failure and retries. After
any transition the CD volume is re-applied and status is set to playing.

**Pause, notify, volume, and mission media.**

- **Pause/resume** is UI-driven: pausing issues `pause cdaudio` and sets the
  paused status (the tick then holds, no transitions); resuming queries the
  position with `status cdaudio position/track %i` and re-issues play from the
  saved position.
- **Volume restoration:** CD volume is applied through the auxiliary volume
  control, wave audio through the waveOut volume control; restoration is
  flagged on shutdown.
- **Mission media are not CD tracks.** The mission fields `brief`, `narration`,
  `glamoursound`, and similar are separate WAV aliases under paths like
  `camps/briefs/`, dispatched through the same decode path of 8.2. CD tracks
  are Red Book audio, independent of mission type; the campaign front-end
  reissues stop/close/open on mode changes via the CD re-init path.
- CD enable/disable is a configuration mask whose bit 0 gates the play
  primitive; the front tick re-applies it.

The missing-CD failure chain above (window-enumeration retry, then give up;
time-format failure stops and closes; track-count failure idles the tick) and
the `MM_MCINOTIFY` handler registration are now established; only history
persistence across saves remains unknown.

## 9. Smacker cinematics and movie capture

The imports and invocation census establish Smacker DLL ordinal invocations for
opening, decoding, frame access, and teardown, but the ordinal-to-Smack API
mapping is not fully recovered. The cinematic path:

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
The contract here is the import/invocation census: Smacker DLL ordinals for
open/decode/frame-access/teardown, the cinematic message-loop ownership, and
the 30-Hz capture gate are all that is evidenced; everything below that line
is out of scope for Nanolathe's single-player scope and is listed in "Missing
and unknown" without a resolution plan.

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
  same-invocation compaction are established. The producer census is closed
  for every strip ([R-STRIP-01 §1]): only strips 2, 5, 6, 7, and 9 ever
  receive objects; strips 0, 1, 3, 4, and 8 have no producer anywhere in the
  image and are always empty (the retired census's "crater/decal literal 4"
  was a misreading — see §3.7). The strip objects are pooled container
  records over internal fixed-stride particle/segment lists
  ([R-STRIP-01 §2]), and the phase-11 sweep's object-internal CRT draws are
  enumerated in [R-STRIP-01 §3]. The wind direction pair's axis assignment is
  closed ([R-WIND-01]): first word = −2·speed·sin(heading) on X, second word
  = −2·speed·cos(heading) on Z.
- The LOS terrain-height word contract is established end to end: built once
  per map load from raw plot heights with the weighted shear aggregation
  (low = max, high = min, blend by thirds, sea clamp, per-column carry),
  never rebuilt mid-battle, read by the ray raster's strict two-byte horizon
  test; the fog/minimap overlay cache (bit 3 of the mode word) is the only
  lazily rebuilt grid.
- All eight projectile rendertype presentations are established, including the
  whole-renderer abort on case-2 admission failure and randomized segmented
  lines for case 7.
- Fog presents through a lazily rebuilt two-byte-per-cell cache with the
  channel rules of section 3.3; plot-flag semantics (instance-present,
  no-build, never-explored marker, placer nibble) are adjudicated;
  post-load visibility publication is synchronous with unit reconstruction.
  Fringe-anchor partition is placement order (sequential rectangle stamps,
  last-stamp-wins; orphaned raw fringe vanishes), replacing the retired
  row-major heuristic.
- Shadow presentation is established: option-bit gates, per-unit `noshadow`,
  orthographic projection with the 0x32/0x7D palette base and four-byte
  terrain average, the GAF-sprite and model-stencil families (per-pixel depth
  compare, SHD darken, 0x01010101 dither), inclusive-rect clipping, and
  shadow-before-body order. The minimap viewport marker is a five-pixel cross
  of two 1-pixel Bresenham lines at +128/+32 from the sheared camera centre
  in the ring palette index.
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

- SHD row selection and the semantic naming of individual strips remain medium
  (the numeric strip order and the complete producer census are established,
  [R-STRIP-01 §1]; the names used there come from each family's asset
  bindings, not from any engine-side label).
  GAF frame-duration interpretation for the
  simulation-tick and wall-clock countdown cursors is established (section 4.4);
  sequence-flag naming and families not shown to use that cursor remain medium.
- In-map void rendering versus out-of-map clipped regions is less certain than
  the hard cull; the exact distinction and the persistent backbuffer behavior
  are probe-pending (a capture at the map edge settles which pixels the
  backbuffer retains beyond the play rect).
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
   402×408 — SC6 resolved), and the fringe partition rule is established as
   sequential placement-order rectangle stamps (anchor→fringe offsets written
   at stamp time; last-stamp-wins on overlap; raw fringe no footprint covers
   ends as `0xFFFF`); the retired 83.2% row-major heuristic is replaced. Plot
   flag bit 7 and whether any unexported code writes placer-nibble values into
   it remain `TODO(question)`; flag bits 0,1,2 and 3–6 are adjudicated, and the
   owner-memory accept is closed: it is gated by the definition's
   `nodrawundergray` flag (§5.1.5). The feature-memory LOS test is the
   two-corner predicate (first corner plus one footprint-displaced corner);
   the four-corner reading was wrong and is corrected. The dense-pack rule for
   a footprint overlapping a live anchor cell remains `TODO(question)`. Tall
   features do not raise LOS ray height.
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
   feeding sight shapes is now **Established** (§3.5 [R-P0-18-B]): low byte =
   MAXIMUM, high byte = MINIMUM over the scattered neighbourhood, seeded
   `0x00`/`0xFF` with strict max/min updates, cells scattered through the height
   shear (skip when `tileZ <= -1`) with a per-column carry, perspective-scaled
   value then raw height, tail blend by thirds with both floored at sea level,
   built once at map load — the earlier reading recorded here (low = minimum,
   high = maximum) was inverted, and the earlier "lazy rebuild behind the
   cache-dirty mode bit" was a mis-identification of the fog-cache builder
   (bit 3 gates the fog/minimap overlay cache; the terrain word is never
   rebuilt mid-battle). Fog-cache channel values
   `0/15` vs `1..14→value-1` are now **established** as `Gray=hi=current` (byte-grid gated `mode&2`) and `Black=lo=history` (word-grid `1<<player`), `variant=(col+row+camPhase)&3` via `offX/offZ` residues, collapsing to the world-anchored `(gx+gy+2)&3`; the `hi==15` state is a gray-table LUT remap (not a fill), the border fixups are conditional bit ORs gated on the cache window crossing the map edge (not unconditional 15 stores), and `1=NW` corner→bit remains supported inference pending asymmetric fog.gaf probe [R-RR16-A]. The vismasks shape count (10
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
   word mask) — separation is closed, and the arbitration among the three
   tables is partially closed (sequential last-writer-wins per pixel on the
   presentation surface; per-table palette mapping remains inference). The
   sensor phase's placement in the tick is closed ([R-SENSOR-01]): it runs
   inside the per-player pass after the local viewing player's stamp sweep and
   30-tick cadence block, once per tick, gated on player count > 1. Still
   open: the writer that clears the per-unit seen marker (status bit `0x100`)
   between sensor passes, the full stealth/init-cloak spawn state walk (the
   init-cloaked spawn
    writer is bounded-negative only — one candidate site, not closed),
   gameplay radar-versus-sonar contact rules beyond the presentation circles
   (owned by document 06, including the secondary candidate list's authored
   flag name), and whether any unresolved identity path shares visibility
   grids across players — multiplayer LOS sharing is closed as never-OR
   through the mask itself.
- Exact edge behavior for unexplored in-map void cells, map border clipping,
  and persistent backbuffer pixels — probe-pending (capture at the map edge
  distinguishes in-map void from out-of-map clipping and shows whether the
  backbuffer retains stale bytes beyond the play rect).

### Renderer

- Full windowed/fullscreen mode transitions, DirectDraw surface flags,
  palette-loss recovery, and exact blit/flip error policy; lost-surface
  recovery at the blit wrappers is documented (retry through the restore
  callback).
- Complete pass table is closed: ten fixed-order strips inside the single frame
  composer with Y-bucket insertion and no depth test, exact numeric order,
  render-mode gates, removal-before-update lifecycle, the 401 steady eviction
  bound, and the 300-record effect pool (section 1). Strip producers are
  closed for every strip ([R-STRIP-01 §1], superseding the 2026-08-25/26
  census): strips 2, 5, 6, 7, 9 have enumerated producers and per-site
  gameplay events; strips 0, 1, 3, 4, 8 have no producer anywhere in the
  image and are always empty. Still open: the weapon-class dispatch selector
  that reaches the strip-5 flame scan (owned by document 06), the identity of
  the root flag byte that disables all strip allocation when set, semantic
  strip naming beyond the GAF/asset bindings of [R-STRIP-01 §2], and the
  ground-scar/crater authoring mechanism (§3.7 `TODO(question)`).
- SHD/LHT row/index formula is now established for model `SHD` (`dont-shade→15`, `row=trunc(dot*5)&0x1F`, gouraud `rowStep=(rowR-rowL)/width`) and halo `LHT` (disc precompute verified: per-pixel CRT draw, `q = trunc((R+sqrt(1.33·dx²+dy²))·32)`, byte `0x6F−q` / ring `0x6E` / transparent `0xFF` on the `(0x20−q) mod 256` compare, level `31−q`); remaining open is ALP usage by any non-LOS UI/fade path — bounded-negative over the renderer cluster (ALP loads only in the minimap picture downsample).
- Model lighting normals (`normalize(cross(b-a,b-c))` over first three indexes, degenerate `(0,1,0)`, per-vertex `avg/cnt` no renormalize) and texture coordinate policy (corner-index affine 16.16 through the edge-table scanline mapper and per-pixel `SHD` sampler, no stored UVs, flat direct-fill only, clamp/nearest/no perspective) and flat-color quads-only are now established (direct-static); remaining open is exact team/logo per-player dimension deltas and pitch/bank naming.
- Shadow presentation is established (option bits, per-unit `noshadow`,
  projection with the 0x32/0x7D palette base and four-byte terrain average,
  GAF-sprite versus model-stencil families, inclusive clipping, dither
  stencil, shadow-before-body order); remaining `TODO(question)`: the exact
  water-flag bit semantics behind the 0x4B offset, the identity of the SHD
  darken row, the dither-option/shading-gate interplay, and aircraft altitude
  versus ground projection.
- Water wake rectangle interpolation, underwater tint, splash timing, and proof
  that no hidden animated-water surface writer exists.
- Cursor hotspot metadata, subframe lifetime, animation speed for families not
   shown to use the authored countdown cursor, and sequence-flag naming.
   Effect-strip owner registration, per-family lifetime, and terminal-frame
   behavior are closed by [R-STRIP-01 §1–§3] (per-strip producers, container
   lifecycles, expiry rules, and CRT draw costs); the residuals left open are
   listed under the Renderer bullet above.
- FNT baseline, glyph advance/kerning, two-byte header fields, clipping edge,
  and any shell path that uses GDI text directly.
- Input repeat/focus/activation rules, key-token translation, cursor capture,
  gadget hit-testing, and complete HUD/minimap palette composition.

### Projectiles and effects

- Projectile rendertype dispatch is closed (all eight cases, section 5.4).
  The case-7 segmentation constant 327680 is exact (direct-static); its
  presentation RNG draws are CRT-stream only, zero simulation draws
  (closed); the physical units of the constant (map-space versus
  projected-space) are probe-pending. Also open: the semantic of the low 16
  bits of the stored projectile orientation fed to the Z rotation slot.
- Beam fixed-point scale/lifetime edge cases, collision ordering at map borders,
  and line-color remap initialization.
- Missile target invalidation and smoke/effect-strip lifetime and fade. Trail
  cadence is closed (additive deadline; zero-delay emits every tick). Nanolathe
  segment fade/lifetime is closed and the earlier "fixed palette index 6" colour
  claim is retracted — see §5.5 "The nanolathe spray" [R-P0-19-P]; the one field
  still open there is the particle word set to `0x100` at spawn.
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
