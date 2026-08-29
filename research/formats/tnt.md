# TNT — Map Terrain (`.tnt`, `.sct`)

## Overview

The `.tnt` file is the binary half of a map (paired with an `.ota`,
[ota.md](ota.md)). It contains the terrain image as a grid of 32×32-pixel
indexed-color tiles, a finer per-16-pixel-cell grid of heights and feature
placements, the embedded feature name list, the sea level, and a
pre-rendered minimap. Pixels are indexes into the shared palette
([pal.md](pal.md)).

`.sct` files ("sections") used by map editors are near-identical small
terrain fragments without map-level fields; they are noted at the end.

Two grid resolutions matter (both little-endian, like everything):

- The **attribute grid**: 1 cell = 16×16 pixels. Holds height and feature
  data. Dimensions: `Width × Height` from the header.
- The **tile grid**: 1 cell = 32×32 pixels. Holds tile indexes. Dimensions:
  `Width/2 × Height/2` (header dimensions are always even).

## Format at a glance

```
+---------------------------+ 0x00
| Header (0x40 bytes)       |
+---------------------------+
| Tile index map            |  u16[ (Width/2) * (Height/2) ]   row-major
| Attribute map             |  4 bytes × Width * Height        row-major
| Tile graphics             |  1024 bytes × Tiles (32x32 pixels each)
| Feature records           |  132 bytes × TileAnims
| Minimap                   |  u32 w, u32 h, w*h pixels
+---------------------------+
```

Section order is not guaranteed — the header carries an absolute pointer to
each section.

## Reference

### Header (0x40 bytes)

| Offset | Type | Name | Description |
| ---: | --- | --- | --- |
| 0x00 | u32 | IDVersion | `0x2000` |
| 0x04 | u32 | Width | Map width in 16-pixel units (always even) |
| 0x08 | u32 | Height | Map height in 16-pixel units (always even) |
| 0x0C | u32 | PtrMapData | → tile index map |
| 0x10 | u32 | PtrMapAttr | → attribute map |
| 0x14 | u32 | PtrTileGfx | → tile graphics |
| 0x18 | u32 | Tiles | Number of unique 32×32 tiles |
| 0x1C | u32 | TileAnims | Number of feature records (the historical field name is "tile anims"; the records are feature names) |
| 0x20 | u32 | PtrTileAnims | → feature records |
| 0x24 | u32 | SeaLevel | Water level in height units; cells with height below this are underwater ("waterheight") |
| 0x28 | u32 | PtrMiniMap | → minimap |
| 0x2C | u32 | MinimapPresent | bit 0 set = an embedded minimap follows at PtrMiniMap; `1` in every observed retail map. (Engine reading established 2026-08-29, `[R-TERR-01 §1]`; formerly listed as "unknown1".) |
| 0x30–0x3C | u32×4 | unknown/pad | `0` in observed retail maps |

Real example — `maps/The Pass.tnt` from `totala2.hpi`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


version 0x2000; 224×102 attribute cells (= 3584×1632 pixels, the OTA says
`size=7 x 4` 512-pixel squares); tile map @ 0x40; attributes @ 0x2CE0;
tile gfx @ 0x191E0; 2373 tiles; 7 features @ 0x26A5E0; sea level 0;
minimap @ 0x26A97C; unknown1 = 1.

### Legacy header (version `0x1020`)

The engine also accepts a legacy version word `0x1020` with the same first
ten slots (version, Width, Height, the three section pointers, Tiles,
TileAnims, PtrTileAnims, SeaLevel) but a different tail — established from
the engine's loader, not from any retail file (no shipped map is legacy):

| Offset | Type | Name | Description |
| ---: | --- | --- | --- |
| 0x28 | u32 | MinWindSpeed | used directly as the map's minimum wind |
| 0x2C | u32 | MaxWindSpeed | used directly as the maximum wind |
| 0x30 | u32 | — | not read |
| 0x34 | u32 | Gravity | authored-unit gravity (same scale as the OTA key); `0` means "use the engine default" |
| 0x38 | u32 | PtrMiniMap | → minimap |
| 0x3C | u32 | MinimapPresent | bit 0 |

Its attribute map is **8 bytes per cell**: byte 0 height, byte 2 a one-byte
feature index (`0x00..0xFB` live; `0xFC..0xFF` empty — there is no void
code in this encoding), byte 6 per-cell metal; bytes 1, 3, 4, 5 and 7 are
not read. The legacy version never overrides wind or gravity from the OTA
and the engine does not place OTA `[Features]` on it. Behavior is in
`[R-TERR-01 §1]`.

### Tile index map

`(Width/2) × (Height/2)` × u16, row-major west→east, north→south. Each
value is an index into the tile graphics array (must be `< Tiles`). Tiles
repeat heavily; The Pass's first row starts `1 2 3 4 1 2 3 4 ...`.

### Attribute map

`Width × Height` cells × 4 bytes, row-major:

| Byte | Type | Meaning |
| ---: | --- | --- |
| +0 | u8 | height (0–255). The height *of the cell's corner*; the engine interpolates terrain from these values. Water lies where height < SeaLevel. |
| +1 | u16 | feature reference (unaligned — bytes 1..2 of the cell): `0xFFFF` = none, `0xFFFC` = "void" (unpassable hole, used at map borders), `0xFFFE` = cell covered by a multi-cell feature anchored in another cell (see below), otherwise an index into the feature records |
| +3 | u8 | unknown; `0` in **all** cells of all 171 retail maps |

First cells of The Pass: `(height=1, feature=0xFFFF, unk=0) ...`.

A feature with a footprint larger than one cell stores its record index in
one **anchor cell** (the top-left corner of its footprint) and fills the
remaining covered cells with `0xFFFE`. Real example from
`maps/Coast To Coast.tnt` — a 3×3 `ArchMetal1` metal deposit (feature
values shown per cell):

```
ffff   ffff        ffff   ffff   ffff
ffff   ArchMetal1  fffe   fffe   ffff
ffff   fffe        fffe   fffe   ffff
ffff   fffe        fffe   fffe   ffff
```

`0xFFFE` is common — about 50,000 cells across the retail corpus (every
map with multi-cell features). Pathfinding/occupancy must treat these cells
as feature-covered even though they carry no index.

### Tile graphics

`Tiles` × 1024 bytes: each tile is 32×32 palette indexes, row-major. Tile 0
is drawn where the tile map says 0, etc. There is no per-tile metadata —
animation (if any) is engine-driven via features, not tiles, despite the
historical "TileAnims" field name.

### Feature records

`TileAnims` × 132 bytes:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | index — equals the record's own position (0, 1, 2, …) in observed data |
| +4 | 128 | char[128] | NUL-terminated feature name, matched case-insensitively against feature TDF sections |

The Pass's records: `0 RockMetal3`, `1 Tree1`, `2 Tree2`, `3 Tree3`, …
Attribute cells reference these records by index; the engine then places
the named feature at that cell.

### Minimap

At `PtrMiniMap`:

| Offset | Size | Description |
| ---: | ---: | --- |
| +0 | 4 | u32 width |
| +4 | 4 | u32 height |
| +8 | w×h | palette-index pixels, row-major |

The Pass's minimap is 252×252 even though the map is wide (3584×1632):
the terrain is scaled to fit and the unused bottom rows are filled with a
padding color (index `0x64`, verified against retail files). A 1998
community format note (Saruman & Bobban / DFR Engineering) claims the pad
byte is `0xDD` instead — a direct conflict with our verified value. Since
`0x64` was checked against actual retail minimap bytes and `0xDD` was not
re-verified here, treat `0x64` as authoritative, but this is worth a second
look if a padded minimap ever renders with an unexpected color. Nearly all
retail minimaps are
252×252 regardless of aspect; at least one (`AC08.TNT`, a tall 192×328-unit
campaign map) stores 252×**256**. Don't hard-code the dimensions — read
them. The minimap is a pre-scaled snapshot of the terrain, not regenerated
by the engine.

## How the engine loads it

The load pipeline is `[02 R-MAP-01 §6–§8]`; the per-cell semantics are
`[03 R-TERR-01 §1–§8]`. Facts a reader of this format should know:

- The terrain path is `Maps\<name>.TNT`, taken from the OTA/mission name
  (a localized `Maps-<language>\` copy wins when it exists). A file that
  cannot be opened is **fatal** (a message box showing the path, then exit);
  so is any version word other than `0x1020` / `0x2000` (`Unknown TNT
  version:  0x%08x`).
- The whole file is read into memory in ten equal reads (plus a remainder),
  advancing the loading bar to 90 %; all header pointers are then treated
  as offsets from the start of that block.
- The tile map, tile graphics and (when `MinimapPresent` bit 0 is set) the
  minimap are copied out verbatim for presentation and never modified. When
  the bit is clear the engine generates the minimap from the tiles instead.
- Every feature record name must resolve to a section in the mounted
  feature TDFs; a name that does not is fatal (`Record "%s" missing from
  feature files`). Names match case-insensitively.
- The file carries no palette; pixels index the global palette ([pal.md](pal.md)).
- The map identity hash used by the lobby is computed over the 64-byte
  header, the raw attribute map and the raw feature records
  `[02 R-MAP-01 §3]`.

## SCT sections

`.sct` editor sections use the same tile/height concepts at small scale
with a reduced seven-word little-endian header:

```text
u32 version                 # observed 2 or 3
u32 section_preview_offset  # 128×128 palette-indexed preview
u32 tile_count
u32 tile_graphics_offset   # tile_count × 1024 bytes, 32×32 palette indices
u32 width
u32 height
u32 tile_index_offset      # width × height little-endian u16 tile indices
```

Retail v2 and v3 sections validate against this layout. The bytes between
the tile-index grid and the next known block are retained as opaque
version-specific metadata; no gameplay or height semantics are assigned to
them. Sections are consumed by map editors (Annihilator, TAE), not by the
game, and do not carry the runtime TNT map's sea-level, feature-table, or
minimap structures.

## Retail corpus notes

All 171 retail maps (base, campaign, Core Contingency, Battle Tactics)
agree on: version `0x2000`, header word 0x2C = `1`, words 0x30–0x3C = `0`,
attribute byte +3 = `0`. Sea levels range 0 (dry/lava maps) to ~75; the
most common retail value is 75.

## Unknowns and caveats

- Header words 0x30–0x3C (always `0` in canonical files) are not read by
  the engine on the canonical path; attribute byte +3 is not read either.
- `0xFFFC` "void" is stored by the engine as `0xFFFC` and treated exactly
  like the engine's own edge-strip void `0xFFFD`: blocked to placement and
  movement, invisible to rendering (the hole look is the tile art). See
  `[R-TERR-01 §1]`, `[R-TERR-01 §2]`.
- The exact orientation convention (which array axis is which map axis)
  matters: data is row-major with rows advancing southward; this matches
  the minimap and the OTA start positions but heights/features should be
  validated visually when implementing.
- Whether height 255 scaling interacts with anything besides SeaLevel
  (e.g. camera) is engine behavior, not format.

## Sources

- Scott "me22" McMurray et al., *Total Annihilation TNT Map Format*,
  Stratlas wiki, 2003 — header and section layout (`ta-tnt-fmt.txt`
  lineage): <https://sourceforge.net/p/stratlas/wiki/TNT/>
- *TNT* and *Map Design Guide* pages, TA Design Guide — role, waterheight
  behavior, OTA pairing:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/tntdesc.htm>,
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/mapdsgn.htm>
- Verified against `maps/The Pass.tnt` from `totala2.hpi`.
- OpenTA parser: `formats/tnt.go`.
