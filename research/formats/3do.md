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
| 0x00 | i32 | VersionSignature | Always `1` in retail data. **The executable never reads it** (`[02 R-MALF-01 §8]`); a reader may still require it, knowing that retail would accept any value. |
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
| +0x00 | u32 | ColorIndex | Authored palette-index field. In the retail corpus untextured primitives keep it in range, while 868 textured primitives carry values above 255 consistent with editor residue. Runtime use is owned by [03 §2.4.1] and [R-RAST-01 §1]. |
| +0x04 | i32 | NumberOfVertexIndexes | Number of entries in the referenced index array. Across all 761 retail models: 94% are 4, 4% are 3, there are 315 values of 2, no values of 1, and about 950 values from 5 through 16. This distribution is a stored-asset fact, not triangulation advice; retail dispatch is owned by [03 §2.4.1]. |
| +0x08 | i32 | Always_0 | Zero in observed unit models |
| +0x0C | i32 | OffsetToVertexIndexArray | → `NumberOfVertexIndexes` × u16, each an index into **this piece's** vertex array |
| +0x10 | i32 | OffsetToTextureName | → NUL-terminated texture name (a GAF entry name, no extension); `0` = no texture |
| +0x14 | i32 | Unknown_1 | Editor-only fields per the original note. **Not usually zero:** 631 of the 761 retail models contain at least one primitive with nonzero values here. Ignore; never validate as zero. |
| +0x18 | i32 | Unknown_2 | ditto |
| +0x1C | i32 | IsColored | Authored flag field. In the 608-model base corpus, bit 0 is set on all 6,598 primitives without a texture name and clear on all 43,845 primitives with one; noncanonical full-word values also occur as editor residue. Runtime interpretation is owned by [03 §2.4.1]. |

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
- `0` (300 models) — primitive index 0;
- `-1` (100 models) — none.

The community 3DO note describes the field as a *file offset* to the
primitive record, but **no retail model uses the offset form** — every
positive value is a plain index. **Established by the retail loader:** all-ones
is the sole no-selection sentinel and zero designates primitive index zero.
Accepting the historical offset form is an implementation compatibility policy,
not retail-format evidence.

The engine's load-time normalization and draw/picking treatment of this field
are runtime behavior and live in [03 §2.4] and [03 §2.4.1].

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

In shipped unit assets, selection value `0` consistently designates primitive
0. The two `-1` cases still carry base-plate geometry at primitive 0. Whether a
runtime derives bounds from that otherwise-undesignated geometry is behavior
or implementation policy, not a stored-format rule.

The selection primitive is a quad lying in the ground plane. Historical
notes commonly describe it as untextured/invisible, but the retail unit
census above shows that texture and color/editor flags are not consistent
identifiers. Community lore: if a vehicle's ground plate winding is inverted
the unit flips out on slopes, so the plate's facing matters to the engine's
terrain alignment.

### Texturing

Texture names refer to entries in the GAF files under `textures/`
([gaf.md](gaf.md)). There are no UV coordinates or stored `u/v` fields, no
stored normals, and no stored material record beyond the primitive fields
above. Authored winding is discussed under "Unknowns and caveats" because it
is an asset property. UV assignment, culling, texture sampling, team-frame
selection, shading, and span filling are runtime contracts owned by
[03 §2.4.1] and [R-RAST-01 §1–§5].

### Runtime texture resolution and face dispatch

Which raster branch a primitive takes, and how its texture name resolves at
draw time, are runtime contracts owned by [03 §2.4.1] and [R-RAST-01 §1–§3].
This format document records only the stored fields above and the stock
distributions below.

**Established (retail asset census).** Across all 608 base `objects3d` models
and 50,443 primitives, every one of the 43,845 primitives carrying a texture
name has four indexes. The 6,598 primitives without a texture name occur at
index counts 2, 3, 4, 5, 6, 7, 8, 10, 12, 13 and 16. Authored `IsColored` bit
1 is not set anywhere in that corpus. These facts establish stock reachability,
not runtime dispatch.

### Face shading (SHD rows)

3DO stores neither normals nor light values. Runtime normal construction,
renderer selection, `PALETTE.SHD` lookup, and the `dont-shade` effect are owned
by [03 §2.4.1], [R-RND-02A], and [R-RAST-01 §5].

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

### Host decoder safety policy

Retail performs relocation without aggregate geometry or index budgets and
follows sibling pointers recursively `[02 R-MALF-01 §8]`. Nanolathe applies
explicit host-safety budgets to decoded polygon indexes as well as object and
geometry counts. Its sibling-list walk is iterative while child depth remains
bounded. Retail also does not check the selection index or a face's vertex
indexes before the ordering pass, and a zero-index face compared by that pass
reaches signed division by zero. A checked decoder may reject these malformed
inputs rather than reproduce unsafe memory access. These are implementation
limits, not recovered retail behavior or file-format restrictions.

## How the engine loads it

The file is read whole (missing or zero-length → **fatal**, the box shows
the path, wherever a unit, weapon or feature names a model); the name and
auxiliary offsets are biased when nonzero, the vertex and primitive array
offsets always, the sibling and child offsets when nonzero with recursion
and no cycle check, and each primitive's three offsets; nothing is bounded,
so a truncated model faults during relocation. A texture name that no
texture GAF holds turns the primitive into flat colour index 209
(`[03 §2.4]`). Full outcome table: `[02 R-MALF-01 §2]`.

Primitive compilation is behavioral rather than a byte-layout transformation.
`[02 "Model archive (3DO)"]` owns selection normalization, stable ordering,
exact arithmetic and malformed-input reachability; `[03 §2.4]` owns the
compiled order's consumers. A lossless parser retains authored order and the
selection field; the runtime model compiler creates the ordered view.

### The one bound measured from the geometry: the model-top walk

Exactly one number is derived from a loaded model's vertices, and it is a
maximum. Immediately after the model is loaded, relocated and texture-bound,
the engine walks the hierarchy once and takes

```
modelTop = max over every piece P, every vertex V of P:
               V.y + sum of P.y-translation and those of P's ancestors
```

starting from an accumulator of **0** at the root, with the accumulator never
lowered. Concretely, one recursive pass over a piece's sibling chain: for each
piece, the maximum of `vertex.y + piece.translation.y` over its own vertex
array, and, when it has a child chain, the maximum against
`walk(firstChild) + piece.translation.y`. The result is in the file's native
16.16 world units, like every other coordinate here.

Three consequences of the zero seed, all load-bearing:

- a model whose vertices all sit below its origin measures **0**, not a
  negative;
- a subtree hanging entirely below its parent contributes 0 rather than
  lowering the answer; and
- the walk applies **no** minimum-vertex-count gate and reads no orientation —
  it runs once, on the authored bind pose.

**There is no min-Y counterpart.** No inverted walk exists: the definition's
minimum-Y bound is zeroed by the loader just before this walk runs and is never
written from geometry, and the horizontal bounds beside it come from the
authored footprint rather than the model. A model bottom is not something
retail measures. The definition-side arithmetic, the store, and the list of
consumers of the resulting word are `[02 R-CAT-01 §7]`; `[07 R-REV-01 §7]`
traces the same pass from the selection-hover side; the behavioral contract
that most looks like it wants a model bottom — the `setSFXoccupy` band-3
"fully submerged" test — reads this maximum instead, `[04 R-MOV-01 §8b]`.

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
  "Invert Face" authoring bug the modeling notes describe. The census
  establishes authored winding only; runtime culling is owned by
  [R-RAST-01 §1].
- **Model facing is −Z, not +Z.** The TA Design Guide under "Sources"
  describes the modeling convention as +Z-forward, but retail data disagrees:
  muzzle locators (`flare*`) sit at negative Z from the barrel they are
  mounted on 135 times against 13. The engine heading convention runs the same
  way: the position step is `vx = -(sin[h]·speed …)`, `vz = -(cos[h]·speed …)`,
  so heading 0 travels toward **−Z** and increasing heading runs
  −Z → −X → +Z → +X ([04 R-MOV-01 §4], Established). A −Z-facing model ends up
  nose-first along the travel direction because the projection negates the
  model-relative Z as well ([R-RAST-01 §2]); do not negate the folded heading
  in the renderer to compensate.
  Convert source Z with `z = -z` before piece rotations/translations and
  then apply the engine heading directly. An added half turn makes the nose
  face the right direction only by rotating unconverted data; an asymmetric
  commander comparison shows that this leaves source X visibly mirrored. The
  reflection reverses polygon winding, so submission must swap the final two
  triangle indices. Apply the same source-Z conversion to piece translations
  and simulation query/muzzle points. The persistent import transform and the
  separate screen shear are specified in [03 §2.4] and [R-RAST-01 §2].
- Stock models contain noncanonical `IsColored` values and out-of-range
  `ColorIndex` values beside texture names. A lossless parser must preserve
  both raw fields; their runtime precedence and shading behavior are owned by
  [03 §2.4.1] and [R-RAST-01 §1–§5].
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
  `totala1.hpi` and a structural survey of all 761 retail models (base game,
  rev31, Core Contingency, Battle Tactics).
