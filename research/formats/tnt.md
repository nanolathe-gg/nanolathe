# TNT — Map Terrain (`.tnt`, `.sct`)

## Overview

**Established** `[02 R-MAP-01 §§6–8]`, `[03 R-TERR-01]`.
The `.tnt` file is the binary half of a map (paired with an `.ota`,
[ota.md](ota.md)). It contains the terrain image as a grid of 32×32-pixel
indexed-color tiles, a finer per-16-pixel-cell grid of heights and feature
placements, the embedded feature name list, the sea level, and a
pre-rendered minimap. Pixels are indexes into the shared palette
([pal.md](pal.md)).

`.sct` files ("sections") are editor resources with a different header and
grid dimensions. Their independently described layout is at the end; do not
read them as a short TNT.

Two grid resolutions matter (both little-endian, like everything):

- The **attribute grid**: 1 cell = 16×16 pixels. Holds height and feature
  data. Dimensions: `Width × Height` from the header.
- The **tile grid**: 1 cell = 32×32 pixels. Holds tile indexes. Dimensions:
  `Width/2 × Height/2` (even in the observed corpus; the Nanolathe reader requires even dimensions).

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
| 0x04 | u32 | Width | Map width in 16-pixel units (even in observed files) |
| 0x08 | u32 | Height | Map height in 16-pixel units (even in observed files) |
| 0x0C | u32 | PtrMapData | → tile index map |
| 0x10 | u32 | PtrMapAttr | → attribute map |
| 0x14 | u32 | PtrTileGfx | → tile graphics |
| 0x18 | u32 | Tiles | Number of unique 32×32 tiles |
| 0x1C | u32 | TileAnims | Number of feature records (the historical field name is "tile anims"; the records are feature names) |
| 0x20 | u32 | PtrTileAnims | → feature records |
| 0x24 | u32 | SeaLevel | Water level in height units; cells with height below this are underwater ("waterheight") |
| 0x28 | u32 | PtrMiniMap | → minimap |
| 0x2C | u32 | MinimapPresent | bit 0 set = an embedded minimap follows at PtrMiniMap; `1` in every observed retail map. (Engine reading Established, `[03 R-TERR-01 §1]`; community notes call the word "unknown1".) |
| 0x30–0x3C | u32×4 | unknown/pad | `0` in observed retail maps |

**Established — corpus example.** `maps/The Pass.tnt` uses version
`0x2000`, 224×102 attribute cells (3584×1632 pixels), 2373 tiles, seven
feature records and sea level zero. Its embedded minimap is 252×252. These
values describe that file, not format limits.

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
`[03 R-TERR-01 §1]`.

### Tile index map

`(Width/2) × (Height/2)` × u16, row-major west→east, north→south. Each
value is an index into the tile graphics array (must be `< Tiles`). Tiles
repeat heavily; The Pass's first row starts `1 2 3 4 1 2 3 4 ...`.

### Attribute map

`Width × Height` cells × 4 bytes, row-major:

| Byte | Type | Meaning |
| ---: | --- | --- |
| +0 | u8 | height (0–255). The height *of the cell's corner*; the engine interpolates terrain from these values. Water lies where height < SeaLevel. |
| +1 | u16 | Feature reference (unaligned, bytes 1..2): values `< 0xFFFB` are feature-table indices; `0xFFFC` authors void. `0xFFFE` marks authored footprint fringe and `0xFFFF` empty in the observed corpus. The other reserved words `0xFFFB` and `0xFFFD` are not feature indices. See the reconstruction rule below. |
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

**Established — source words are not runtime occupancy.** The loader starts
with empty feature cells, stamps all authored `0xFFFC` voids first, then
stamps live indices in row-major order through the feature placement service.
That service creates each footprint fringe and its anchor relation from the
feature definition; it does not copy an authored `0xFFFE` into occupancy.
An uncovered `0xFFFE` therefore becomes empty, and covered `0xFFFF` becomes
fringe. Authored `0xFFFB` and `0xFFFD` likewise stamp nothing; the runtime
edge/lava sweep separately creates its own void code. Save restoration skips
the live-feature pass because saved feature state supplies it
`[03 R-TERR-01 §1]`, `[05 R-FEAT-01 §17]`.

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
Attribute cells index table order; every record name is compiled in that
order before placement `[02 R-MAP-01 §8]`. Nanolathe retains the leading
record word for inspection and resolves by table order.

### Minimap

At `PtrMiniMap`:

| Offset | Size | Description |
| ---: | ---: | --- |
| +0 | 4 | u32 width |
| +4 | 4 | u32 height |
| +8 | w×h | palette-index pixels, row-major |

The Pass's minimap is 252×252 even though the map is wide (3584×1632):
the terrain is scaled to fit and the unused bottom rows are filled with a
padding color (index `0x64`, verified against retail files). The inherited community note gives `0xDD`; that is not the padding
observed in these retail samples. **Unknown:** whether another editor or
content corpus uses that value; byte inspection of such a file would settle
it. Readers should use the stored dimensions and documented crop geometry,
not search for a universal padding-color sentinel. Nearly all
retail minimaps are
252×252 regardless of aspect; at least one (`AC08.TNT`, a tall 192×328-unit
campaign map) stores 252×**256**. Don't hard-code the dimensions — read
them. The minimap is a pre-scaled snapshot of the terrain, not regenerated
by the engine.

**How the used sub-rectangle is sized — Established for the samples below.**
The stored `width x height` from the header above is the *allocated* bitmap,
not necessarily the real image's extent: on a non-square map only a top-left
sub-rectangle holds terrain, and the rest of the short axis is the `0x64`
fill. The boundary is exact — every row/column is either entirely fill or
entirely real, never a blended edge — and its size is the same long-side fit
`camera.LayoutMinimap` already uses for the on-screen radar rectangle, with
the *stored* bitmap dimension standing in for that function's 126-pixel
canvas constant and `PlayRight`/`PlayBottom` (`Width*16-32`, `Height*16-128`,
not the raw pixel dimensions) as the map shape:

```
if PlayRight < PlayBottom:  usedW = PlayRight*storedW/PlayBottom (trunc);  usedH = storedH
else:                       usedW = storedW;  usedH = PlayBottom*storedH/PlayRight (trunc)
```

Verified byte-for-byte against five shipped maps of three different aspect
ratios (`The Pass` wide, `Great Divide` tall, and `The Bayou`/`Crystal
Cracked`/`Polar Range`, all 640×640 cells — square in cells but not in
`PlayRight`/`PlayBottom`, since `-32` and `-128` differ): in every case the
formula's predicted last used row/column lands on exactly the same pixel
where the file's `0x64` fill begins. A reader that resamples the full stored
bitmap without first cropping to this sub-rectangle stretches the real image
and pulls the fill color into the visible output — on a markedly non-square
map this reads as the image being shifted toward one corner with a solid
band of the fill color filling the rest, which is what a play-test report
described as a "blue stripe" on a tall map (`0x64` maps to a blue palette
entry in the stock install). See `[03 §3.7]` for how the cropped
sub-rectangle then re-enters the same generic ALP resize used for every
other picture-build path.

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
- **No bound is checked anywhere** (`[02 R-MALF-01 §7]`): the ten reads'
  counts are summed but not compared with the size; every header pointer
  is biased without a bound; the tile map, attribute array, feature-name
  table and tile set are copied by their declared counts. A cell's feature
  index below the void threshold is used as a catalog index **without a
  comparison against the compiled table's count**. A zero-length file reads
  its version word from an empty allocation (fatal `Unknown TNT version`
  in practice). The plot grid, tile storage and minimap
  use declared counts for allocation, so absurd values can reach the
  out-of-memory abort.
- The map identity hash used by the lobby is computed over the 64-byte
  header, the raw attribute map and the raw feature records
  `[02 R-MAP-01 §3]`.

## SCT sections

`.sct` editor sections use the same tile/height concepts at small scale
with a seven-word little-endian header. **Established — bounded editor-file
observation**, reflected in `formats/sct.go` (not a retail runtime loader):

```text
u32 version                 # observed 2 or 3
u32 section_preview_offset  # 128×128 palette-indexed preview
u32 tile_count
u32 tile_graphics_offset   # tile_count × 1024 bytes, 32×32 palette indices
u32 width                   # tile-grid columns, NOT TNT attribute cells
u32 height                  # tile-grid rows
u32 tile_index_offset      # width × height little-endian u16 tile indices
```

**Established (Annihilator source):** immediately after the tile-index grid,
at `tile_index_offset + 2*width*height`, the editor reads a height grid of
`2*width` columns by `2*height` rows. Records are row-major. Version 2 uses
eight bytes per height record; version 3 uses four. In both versions the
first byte is an unsigned height, copied directly into the editor's height
grid. The inspected section loader ignores the other bytes of each record;
its section-copy and map-fill paths preserve the two-to-one grid dimensions.

| Version | Record count | Record size | Established field |
| --- | ---: | ---: | --- |
| 2 | `4*width*height` | 8 bytes | byte +0: height; bytes +1…+7 ignored by this editor loader |
| 3 | `4*width*height` | 4 bytes | byte +0: height; bytes +1…+3 ignored by this editor loader |

The inspected standalone SCT writer emits version 3 records containing a
height byte, signed 16-bit `-1`, then a zero byte. Its HPI export path instead
emits `+1` in that middle word. These are writer-specific defaults, not proof
of a universal feature or occupancy meaning.

**Established (bounded asset census):** `worlds.hpi` contains 689 sections:
459 version 2 and 230 version 3. All of their height-record spans fit the
files. Version 2 places the preview immediately after these records;
version 3 places the tile graphics there, followed by the preview. Every
observed version-2 record has trailing bytes `01 FF 00 00 00 00 00`; every
version-3 record has `FF FF 00`. These constants do not settle their authored
meaning. Header pointers remain authoritative for the separately located
blocks; the inspected standalone writer uses a different packing order.

Sections are consumed by map editors (Annihilator, TAE), not by the game,
and do not carry the runtime TNT map's sea-level, feature-table, or minimap
structures. The editor evidence establishes stored heights without assigning
the unused bytes runtime TNT semantics.

## Retail corpus notes

**Established — historical sample.** A 171-map survey (base, campaign,
Core Contingency, Battle Tactics) recorded: version `0x2000`, header word 0x2C = `1`, words 0x30–0x3C = `0`,
attribute byte +3 = `0`. Sea levels range 0 (dry/lava maps) to ~75; the
most common retail value is 75.

## Reader policy and unknowns

**Established — Nanolathe policy.** `formats.LoadTNTWithLimits` validates
section spans, nonzero/even dimensions, allocation budgets, tile indices and
live feature indices. It retains reserved source feature words and the
canonical fourth attribute byte; `internal/world` performs the reconstruction
above. Legacy empty codes are normalized to the canonical empty word. With
an absent-minimap flag, the parser does not dereference the minimap pointer.
These are checked decoding and representation choices, not retail malformed-
input guarantees. `formats.LoadSCTWithLimits` additionally rejects overlapping
known blocks, including the complete version-specific height-record span.
`SCT.Heights` exposes the unsigned heights in row-major order over a
`2*Width` by `2*Height` grid. `AttributeData` preserves exactly those records,
including ignored bytes; `Raw` retains the complete file and any intervening
padding. Authored tests cover both versions, nonsquare grids, independent
graphics/preview placement and rejected incomplete or overlapping records.
This decoder does not assign meanings to ignored record bytes.

- **Unknown:** the authored meanings of the SCT record bytes ignored by the
  inspected editor, and any version beyond the observed 2/3 pair. A writer
  or consumer that assigns those bytes a role, or version-matched source and
  files for another version, would settle these editor-only residuals.
- Header words 0x30–0x3C (always `0` in canonical files) are not read by
  the engine on the canonical path; attribute byte +3 is not read either.
- `0xFFFC` "void" is stored by the engine as `0xFFFC` and treated exactly
  like the engine's own edge-strip void `0xFFFD`: blocked to placement and
  movement, invisible to rendering (the hole look is the tile art). See
  `[03 R-TERR-01 §1]`, `[03 R-TERR-01 §2]`.
- The exact orientation convention (which array axis is which map axis)
  matters: data is row-major with rows advancing southward; this matches
  the minimap and the OTA start positions but heights/features should be
  validated visually when implementing.
- Whether height 255 scaling interacts with anything besides SeaLevel
  (e.g. camera) is engine behavior, not format. **Settled for passability
  (`[04 R-SLOPE-01]`).** The height byte reaches the
  runtime plot cell **verbatim** — no scaling, shift or height-scale between
  this byte and the engine's derived per-cell minimum/maximum, which are the
  min/max over the cell, its east, south and south-east neighbours (edge
  guarded). A measurement that appears to contradict this ("the start
  plateaus' rims classify at slope 17 while the stock vehicle classes author
  `MaxSlope=15`", a 2735-cell pocket on `ashap plateau`) is an artefact of
  aggregating heights over the class footprint: retail classifies each cell
  on its own 2×2 corner quad and takes the minimum tier over the footprint,
  under which every cell of those rim anchors is at or below the limit (the
  three sampled anchors: per-cell slopes 4–13) and the pocket is 53370 cells.
  The derived min/max pair is Established; aggregating heights over the
  footprint is not retail's movement rule. Reproducer:
  `internal/session/ai_terrain_pocket_probe_test.go`.

## Sources

- Scott "me22" McMurray et al., *Total Annihilation TNT Map Format*,
  Stratlas wiki, 2003 — header and section layout (`ta-tnt-fmt.txt`
  lineage): <https://sourceforge.net/p/stratlas/wiki/TNT/>
- *TNT* and *Map Design Guide* pages, TA Design Guide — role, waterheight
  behavior, OTA pairing:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/tntdesc.htm>,
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/mapdsgn.htm>
- Verified against `maps/The Pass.tnt` from `totala2.hpi`.
- Kinboat's TA tools source archive, Annihilator `classSection.cls`
  section loader, section-copy/map-fill paths, and standalone/HPI writers;
  `modSections.bas` header definitions. Cross-checked against all 689 SCT
  files independently enumerated from the installed `worlds.hpi`.
- Nanolathe readers and authored tests: `formats/tnt.go`, `formats/sct.go`,
  `formats/tnt_test.go`, `formats/sct_test.go`, `formats/source_test.go`;
  footprint reconstruction: `internal/world/feature_stamp_test.go`.
