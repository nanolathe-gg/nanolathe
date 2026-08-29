# 3DO — 3D Object Models (`.3do`)

## Overview

`.3do` files hold Total Annihilation's 3D models: units, unit corpses, 3D map
features, and projectile models (bombs, missiles). They live in the
`objects3d/` directory. A unit's model is `<unitname>.3do` and its corpse is
`<unitname>_dead.3do` (referenced indirectly through a feature definition,
see [tdf.md](tdf.md)).

A model is a tree of **objects** ("pieces"). Each piece has its own vertex
and primitive (face) arrays and a translation relative to its parent. Pieces
are what COB scripts animate — `turret`, `barrel1`, `flare2` in a script are
piece names from the 3DO. Pieces with one vertex and no primitives are
common; they serve as attachment/emit points (muzzle flares, smoke, nano
spray, build pads).

There is **no animation data** in a 3DO — all motion comes from the COB
script ([cob.md](cob.md)). There are also **no UV coordinates** — texture
mapping is implied by vertex order (see "Texturing" below).

## Format at a glance

```
offset 0: root Object record (52 bytes)
   ├─ OffsetToObjectName ──────────► "base\0"
   ├─ OffsetToVertexArray ─────────► Vertex[n]        (12 bytes each)
   ├─ OffsetToPrimitiveArray ──────► Primitive[m]     (32 bytes each)
   │      ├─ OffsetToVertexIndexArray ─► u16[k] indexes into this piece's vertices
   │      └─ OffsetToTextureName ──────► "Tredside2\0"  (or 0 = untextured)
   ├─ OffsetToChildObject ─────────► first child Object (same 52-byte layout)
   └─ OffsetToSiblingObject ───────► next sibling Object (0 = end of list)
```

Children of one parent form a linked list through their sibling pointers.
All offsets are absolute file offsets; there is no file header — the root
object simply starts at offset 0.

A real tree (`objects3d/armflash.3do`, the ARM Flash tank):

```
base            @ 0x0000  36 verts, 20 prims
  turret        @ 0x05AB  21 verts, 10 prims   at (0, +6.0, +8.5) from base
    sleeves     @ 0x0872  17 verts, 10 prims
      barrel1   @ 0x0B0A   9 verts,  3 prims   at (-4.5, 0, -5.0)
        flare1  @ 0x0D91   1 vert,   0 prims   (muzzle flash point)
      barrel2   @ 0x0C2A   9 verts,  3 prims   at (+4.5, 0, -5.0)
        flare2  @ 0x0D4A   1 vert,   0 prims
```

## Reference

### Object record (52 bytes, 13 × i32)

| Offset | Type | Name | Description |
| ---: | --- | --- | --- |
| 0x00 | i32 | VersionSignature | Always `1`. Treat as a required signature. |
| 0x04 | i32 | NumberOfVertexes | Vertex count for this piece (may be 0) |
| 0x08 | i32 | NumberOfPrimitives | Primitive count for this piece (may be 0) |
| 0x0C | i32 | OffsetToSelectionPrimitive | Root piece only: reference to the primitive drawn as the ground/selection plate. `-1` = none. Child and sibling pieces always store `-1` (stock data also uses `0` as a no-selection value on non-roots). See "Selection primitive" below. |
| 0x10 | i32 | XFromParent | Piece origin relative to parent origin, signed 16.16 fixed point |
| 0x14 | i32 | YFromParent | ditto (Y is up) |
| 0x18 | i32 | ZFromParent | ditto (**−Z** is the model's front — see "Unknowns and caveats") |
| 0x1C | i32 | OffsetToObjectName | → NUL-terminated piece name |
| 0x20 | i32 | Always_0 | Zero in all observed files |
| 0x24 | i32 | OffsetToVertexArray | → `NumberOfVertexes` × Vertex |
| 0x28 | i32 | OffsetToPrimitiveArray | → `NumberOfPrimitives` × Primitive |
| 0x2C | i32 | OffsetToSiblingObject | → next Object sharing this piece's parent; `0` terminates the sibling list. The root has no siblings. |
| 0x30 | i32 | OffsetToChildObject | → first child Object; `0` = leaf |

Real example — the root object of `objects3d/bomb1.3do` (a projectile,
one-piece model):

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


VersionSignature=1, 9 vertices, 9 primitives, selection = -1, translation
(0,0,0), name @ 0x224 (`base`), vertices @ 0x98, primitives @ 0x104, no
sibling, no child.

### Vertex record (12 bytes)

| Offset | Type | Name |
| ---: | --- | --- |
| +0 | i32 | x (signed 16.16 fixed point) |
| +4 | i32 | y |
| +8 | i32 | z |

First vertex of `bomb1.3do`: raw `(45875, -45875, -91750)` =
`(0.700, -0.700, -1.400)` world units. For scale, a Kbot is roughly 25 units
wide and a big tank ~50; one world unit is on the order of one map pixel.

Vertices are shared within a piece via the primitives' index arrays; they
are never shared across pieces.

### Primitive record (32 bytes, 8 × i32)

| Offset | Type | Name | Description |
| ---: | --- | --- | --- |
| +0x00 | u32 | ColorIndex | Palette index used when `IsColored` explicitly selects a flat-colored face. When a texture is present this field frequently contains values far outside 0–255 (leftover editor data); only canonical `IsColored == 1` plus an in-range index overrides a resolved texture. |
| +0x04 | i32 | NumberOfVertexIndexes | Vertex count = primitive type: 1 point, 2 line, 3 triangle, 4 quad. Across all 761 retail models: 94% quads, 4% triangles, 315 lines, zero points, and n-gons of 5–16 vertices (≈950 total) which should be fan-triangulated. |
| +0x08 | i32 | Always_0 | Zero in observed unit models |
| +0x0C | i32 | OffsetToVertexIndexArray | → `NumberOfVertexIndexes` × u16, each an index into **this piece's** vertex array |
| +0x10 | i32 | OffsetToTextureName | → NUL-terminated texture name (a GAF entry name, no extension); `0` = no texture |
| +0x14 | i32 | Unknown_1 | Editor-only fields per the original note. **Not usually zero:** 631 of the 761 retail models contain at least one primitive with nonzero values here. Ignore; never validate as zero. |
| +0x18 | i32 | Unknown_2 | ditto |
| +0x1C | i32 | IsColored | Face is *clear* (invisible/transparent) when it has no texture **and** `IsColored == 0`. Untextured with nonzero `IsColored` = flat-colored via `ColorIndex`. With a resolved texture, canonical value `1` and an in-range index explicitly select the flat color; other nonzero values occur as editor garbage and do not suppress the texture. |

Real example — a textured quad from `armflash.3do`'s base piece
(primitive record at 0x346):

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


ColorIndex=0x0344E9FF (junk — textured face), 4 vertex indexes @ 0xDE,
texture name @ 0x34 = `Tredside2`, Unknown_1=0, Unknown_2=0, IsColored=0.

And an untextured colored triangle from `bomb1.3do` (record at 0x104):
ColorIndex=69, 3 indexes @ 0x58 = `[8, 3, 1]`, no texture, IsColored≠0.

### Strings and file layout

Object names and texture names are NUL-terminated strings anywhere in the
file. Cavedog's exporter always emits a texture-name pool at offset 0x34
(immediately after the root object), then vertex index arrays, vertices,
primitives, then further objects — e.g. in `armflash.3do` the name block at
0x34 begins `Tredside2\0descamo3\0camoflage\0Tredside1\0...`. Rely on the
pointers, not on this layout.

### Hierarchy semantics

- Piece positions are pure translations. There is no rotation or scale in
  the file; orientation comes entirely from script animation at runtime.
- A piece's world position = sum of `[XYZ]FromParent` up its ancestor chain.
- Traversal must guard against cycles and shared nodes: nothing in the
  format prevents a malicious file from pointing two links at one object.
  Retail files are strict trees.

### Selection primitive ("ground plate")

The root's `OffsetToSelectionPrimitive` designates one primitive of the root
piece drawn as the unit's selection rectangle/footprint. A survey of all
761 retail models shows exactly three encodings on roots:

- a **direct primitive index** into the root's primitive array
  (361 models, values 1 and up);
- `0` (300 models) — ambiguous between "primitive index 0" and "none";
- `-1` (100 models) — none.

The community 3DO note describes the field as a *file offset* to the
primitive record, but **no retail model uses the offset form** — every
positive value is a plain index. (OpenTA's parser accepts a valid aligned
offset first for compatibility with the historical description, then a
bounded index; retail data only ever exercises the index path.) For `0`,
OpenTA treats it as index 0 only when the first primitive actually looks
like a plate (a flat 4-vertex quad on unique vertices).

At draw time the selection primitive is **not rendered as a model face**: the
retail unit rasterizer starts its primitive loop at index 1 for any piece
declaring a selection primitive (after the load-time swap places the plate at
index 0), and unit picking is a 2D bounding-box test, not a mesh raycast. The
plate's remaining live use is identification bookkeeping.

#### Retail unit-model census

A direct cross-reference of every winning retail `units/*.fbi` against its
`ObjectName` gives 278 unit definitions and 278 unique 3DO models. Unlike the
broader 761-model census above, this excludes corpses, projectiles, and map
features.

Every one of those 278 unit models has at least one flat root X/Z
quadrilateral usable as a base/ground plate:

- 167 roots store selection value `0`; primitive 0 is a flat four-vertex
  X/Z plate in all 167;
- 109 roots store a positive primitive index; the selected primitive is a
  flat four-vertex X/Z plate in all 109;
- two roots store `-1`: `armmstor` and `cormine1`. Both still have a flat
  plate at root primitive 0 (`cormine1` has only that primitive).

The selected plate cannot be identified reliably from texture or
`IsColored` flags. Some valid designated plates are textured, and many carry
nonzero editor/color fields. Geometry plus the root selection value is the
reliable retail-unit signal: four unique vertices, one constant Y plane, and
nonzero X/Z extent. Small coordinate asymmetries occur, so collision users
should derive conservative X/Z bounds rather than require a mathematically
perfect axis-aligned rectangle.

For retail unit simulation, selection value `0` is therefore not ambiguous:
it designates primitive 0. The two `-1` cases require an explicit geometric
fallback to primitive 0 if base-plate bounds are desired. Third-party or
non-unit models still require the general missing/ambiguous policy.

The selection primitive is a quad lying in the ground plane. Historical
notes commonly describe it as untextured/invisible, but the retail unit
census above shows that texture and color/editor flags are not consistent
identifiers. Community lore: if a vehicle's ground plate winding is inverted
the unit flips out on slopes, so the plate's facing matters to the engine's
terrain alignment.

### Texturing

Texture names refer to entries in the GAF files under `textures/`
([gaf.md](gaf.md)). There are no UV coordinates or stored `u/v` fields. A
quad maps by corner-index affine 16.16: index order `0→(0,0)`, `1→(1,0)`,
`2→(1,1)`, `3→(0,1)`, with fixed-point interpolation along edges and then
across each scanline. Flat-color drawing accepts only quads; textured polygons
with 5–16 vertices remain affine n-edge polygons rather than pre-triangulated
fans. Bounding extents control clipping and scanline iteration, not texture
coordinates. Sampling is nearest-neighbor, clamped to the last texel, with no
perspective divide; transparent texels also skip shade lookup. Which corner is
"top-left" was established by authoring tools per face by rotating the index
order; renderers replicating classic visuals map as above.
Faces are single-sided; the retail winding convention is counter-clockwise
when viewed from outside — measured, see "Unknowns and caveats" (inverted
faces were a common authoring bug, fixed in tools by "Invert Face").

Team color comes from complete player-specific frames in `LOGOS.GAF`: frame
*n* is the source texture for player *n*. Select that frame before applying
the face's `PALETTE.SHD` lookup, then resolve the shaded index through the
shared palette. Do not approximate this by palette-remapping frame 0; the
frames contain entry-specific pixel and sometimes dimension differences. See
[gaf.md](gaf.md).

### Runtime texture resolution and face dispatch (retail rasterizer)

At load the engine resolves every primitive's texture name against the side's
texture GAF set (`armbldg`/`armcamo`/`armvehic`/`armships` for Arm, `cor*` for
Core), then a fallback set, case-insensitively:

- **Miss** → the primitive is rewritten to a flat color `0xd1` (209) and takes
  the flat-quad path below — a gray placeholder, not an invisible face.
- **1 frame** → static texture.
- **2+ frames** → animated texture: a per-instance animation player is
  created and ticked once per simulation frame; per-frame delays come from the
  GAF frame table. Instances tick independently (two labs built at different
  ticks drift).
- **Exactly 10 frames** → team texture: excluded from animation; the frame is
  selected by owner player at draw time. This is the definitive
  team-texture discriminator.

At draw time the rasterizer dispatches per primitive on its flag bits:

- **Flat-colored primitives** (`IsColored` bit 0 set) render through the
  generic edge-table polygon filler **at any vertex count** — lines,
  triangles, quads and n-gons alike.
- **Textured primitives** (`IsColored` bit 0 clear) render **only when the
  vertex count is exactly 4**. The quad mapper is hard-wired to four corners
  and the dispatcher tests the count before it binds any texture, so a
  textured triangle or n-gon draws nothing.
- A **textured** quad (authored `IsColored` bit 0 clear, exactly four
  vertices) whose team bit is set draws `LOGOS` frame `[colourIndex]`, where
  the index is the owning player's colour byte `0..9`; an out-of-range index
  (e.g. the unassigned `0xFF`) selects no frame and draws nothing. Flat
  primitives never consult the team bits. (Corrected 2026-08-29 against
  [03 R-RAST-01 §3]; the earlier bullet said a *flat* quad fills through the
  LOGOS frame with a per-player *shade byte* — wrong branch and wrong byte.)

**Correction (2026-08-28).** The two bullets above previously said the
opposite — textured at any vertex count, flat quads only. The arities were
transposed. The stock corpus settles it without ambiguity: across all 608 base
`objects3d` models and 50,443 primitives, `IsColored` bit 0 is set on exactly
the 6,598 primitives that carry no texture name and clear on exactly the
43,845 that do, and **every one of those 43,845 textured primitives is a
quad**, while the flat ones occur at vertex counts 2, 3, 4, 5, 6, 7, 8, 10,
12, 13 and 16. The old reading would have discarded 3,312 authored flat
non-quads (2,402 of them triangles) and kept a textured-n-gon path no stock
asset reaches. Behavior is owned by
`research/retail-executable-spec/03` `[R-REN-03A §5]`; this note exists so a
parser author reading only the format doc is not misled about which field
gates which path. Authored `IsColored` bit 1 is never set anywhere in the
stock corpus — the loader writes it, to mark a texture that must be resolved
per draw (animated entries and the LOGOS team textures).

### Face shading (SHD rows)

Shading is not applied to every model. Retail runs its shaded piece renderer
only for a unit whose FBI authors `BMcode=0` (the structure class) and only
while the `Shading` display option is on; every other unit is drawn by a
second piece renderer that maps the same textured faces with no
`PALETTE.SHD` step and never reads the per-piece `dont-shade` bit. So the
rest of this section describes how a structure is lit, and mobile units show
no orientation-dependent shading in retail at all. See
`research/retail-executable-spec/03` `[R-RND-02A]`.

On that path, textured faces shade through `PALETTE.SHD` (32 rows × 256
entries) with the row selected per vertex:

```
row = trunc( dot(N, L) * 5.0 ) mod 32
L default = (-0.8, 1.0, 0.25)   (user-settable light; shipped default)
N = per-vertex smooth normal:
    face normal = normalize(cross(v[b]-v[a], v[b]-v[c]))
      over the polygon's first three vertex indexes;
    degenerate faces (any two equal indexes) use (0, 1, 0);
    vertex normal = average of the normals of all faces touching it
```

Rows are palette remaps, not brightness ramps: row 15 is identity, row 0 maps
most entries toward black, row 31 saturates, and intermediate rows shift hue
differently per entry — this is what gives TA structures their per-face color
variation. COB's `dont-shade` opcode pins a piece to row 15, which stock
factory scripts use to exempt doors, pads, nano beams and landing plates
while the rest of the structure stays shaded.

**Flat faces shade too, on the shaded path.** This corrects the previous
sentence "flat-colored quads never route through SHD (confirmed by a
two-normal 3DO probe: flat colors do not vary with orientation)". The probe
measurement stands but was generalised past its case. There are two flat span
writers, one per renderer: the unshaded renderer's stores the color byte raw,
the shaded renderer's stores `SHD[row*256 + color]` using the same
Gouraud-interpolated row the textured path uses. A flat face therefore does
not vary with orientation on a `BMcode=1` unit, or with `Shading` off — which
is what the probe saw — and does vary on a `BMcode=0` structure with `Shading`
on. See `[R-REN-03A §5]`.

### Piece naming conventions

COB scripts and the engine's default helpers assume conventional piece
names. From a survey of all 761 retail models (counts = models containing
the name):

- `base` (509) — the root piece of nearly every unit.
- `turret` (102), `sleeve(s)`, `barrel`/`barrel1`/`barrel2`, `gun`/`gun1`/
  `gun2` — weapon assemblies, animated by the Aim/Fire callbacks.
- `flare`, `flare1..3` — one-vertex, zero-primitive muzzle-flash locators
  returned by `QueryPrimary` and shown/hidden by `FirePrimary`.
- `wake1`–`wake8` (ships), plus `thrust`/`vtol`-style locators on aircraft —
  one-vertex emit points for `emit-sfx`.
- Kbot skeleton: `pelvis`, `torso`, `head`, `lthigh`/`rthigh`,
  `lleg`/`rleg`, `lfoot`/`rfoot`, `luparm`/`ruparm` (walk animations).
- Builders/factories: `beam`/`beam1`/`beam2`, `nano1`/`nano2` (nanolathe
  spray points, `QueryNanoPiece`), `pad` (factory build platform,
  `QueryBuildInfo`), `door1`/`door2`, `plate`, `post`, `slip` (shipyards).
- Corpse models (`*_dead.3do`) conventionally contain `ground`, `wreck`,
  and/or `gp` pieces (65/64/32 models).

None of this is enforced by the format — the linkage is by name between the
3DO and its COB — but tooling and reimplementations should expect these
names, and `SweetSpot`/`SMOKEPIECE` defaults target `base`.

### Model statistics (retail corpus)

761 models: hierarchy depth reaches 10 (`armmav.3do` in Core Contingency);
391 models are a single piece (projectiles, simple features, heaps).
Version signature is 1 and object-level `Always_0` is 0 in every file.
868 textured primitives carry `ColorIndex` values above 255 (garbage);
no untextured primitive does.

## Unknowns and caveats

- `Unknown_1`/`Unknown_2` are editor leftovers and are nonzero somewhere in
  ~83% of retail models (garbage also appears in `IsColored`/`ColorIndex`
  positions — `bomb1.3do` stores `IsColored = 0x782911`). Parsers must not
  require zeros; a strict mode that does will reject most retail content.
- Exact classic UV corner assignment and winding are re-derived by
  observation, not documented by the original note. **Winding was measured,
  not inferred**: summing each piece's signed volume over the local install
  (exact for a closed piece, and independent of any handedness convention)
  gives 2029 pieces whose authored vertex order has an outward right-handed
  normal against 90 inward, across 608 models — 600 models to 2. So the
  authored order is counter-clockwise seen from outside, and the 90 are the
  "Invert Face" authoring bug the modeling notes describe. This document
  previously stated the opposite, and three separate renderers inherited the
  error, so the measurement is kept executable rather than in prose alone:
  `TestRetailModelWindingIsOutward` in `internal/render` re-runs it against
  a local install and skips when none is present.
- **Model facing is −Z, not +Z.** The TA Design Guide under "Sources"
  describes the modeling convention as +Z-forward, but retail data disagrees:
  muzzle locators (`flare*`) sit at negative Z from the barrel they are
  mounted on 135 times against 13. Engine heading 0 travels toward +Z and
  increases toward +X (pinned by `movement.TestHeadingUsesTAWorldConvention`),
  so convert source Z with `z = -z` before piece rotations/translations and
  then apply the engine heading directly. An added half turn makes the nose
  face the right direction only by rotating unconverted data; an asymmetric
  commander comparison shows that this leaves source X visibly mirrored. The
  reflection reverses polygon winding, so submission must swap the final two
  triangle indices. Apply the same source-Z conversion to piece translations
  and simulation query/muzzle points. Trailing `-Z` in screen helpers is the `Z - Y/2` shear (`NEG; SAR 0x10; SAR 1; SUB` transient) not a second conversion; load-time `−X,−Z` remains sole persistent sign fixup (`H_A` established, `H_C` rejected per rr-06_addendum, direct-static via register-vs-store and `SUB` vs `ADD`).
- Canonical `IsColored == 1` plus `ColorIndex < 256` takes precedence over a
  resolved texture name. The controlled asymmetric Oracle carries both fields
  and retail draws its synthetic index-56 box face instead of `colorsmd`.
  A second controlled face pair uses one explicit index on two different
  normals: retail keeps both at the same resolved palette color, rather than
  applying the textured-face `PALETTE.SHD` rows. Preserve flat colors after
  player/team resolution; directional SHD lookup belongs to textured pixels.
  Stock models also contain malformed noncanonical `IsColored` values and
  out-of-range color indexes beside valid textures; treat those as editor
  garbage and keep the texture. If no texture exists, any nonzero flag with an
  in-range index retains the historical flat-color behavior.
- Fixed-point scale: the 16.16 interpretation matches all stock data, but no
  vendor document states it. Note that BOS/COB linear script values use a
  different scale (1 BOS unit = 2.5 model units; see [cob.md](cob.md)).

## Sources

- Dan Melchione (rev. Dark Rain), *Unofficial .3do* format note v0.9.1 —
  structures, worked `armsy.3do` example:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/ta-3do-fmtV2.txt>
  (historically also at `www.tauniverse.com/~visual-ta/`).
- *3DO, DXF, and LWO*, TA Design Guide — modeling conventions,
  Y-up/+Z-forward, piece hierarchy, ground plate lore (its forward axis
  disagrees with retail data — see "Unknowns and caveats"):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/3dodesc.htm>
- Verified against `objects3d/bomb1.3do` and `objects3d/armflash.3do` from
  `totala1.hpi`, a structural survey of all 761 retail models (base game,
  rev31, Core Contingency, Battle Tactics), and OpenTA's parser
  (`formats/three_do.go`).
