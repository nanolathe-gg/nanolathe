# PAL — Palettes and Color Tables (`.pal`, `.alp`, `.lht`, `.shd`)

## Overview

Total Annihilation renders everything in 8-bit indexed color through one
shared 256-entry palette. The `palettes/` directory holds the palette itself
(`PALETTE.PAL`, plus `GUIPAL.PAL` for menus) and three derived lookup tables
used by the software renderer for blending, lighting, and shading
(`PALETTE.ALP`, `PALETTE.LHT`, `PALETTE.SHD`). Every GAF frame, TNT tile,
minimap, FNT glyph color, and 3DO face color is an index into this palette.

## Format at a glance

```
PALETTE.PAL   1024 bytes = 256 × { u8 red, u8 green, u8 blue, u8 zero }
PALETTE.ALP  65536 bytes = 256 rows × 256   alpha/blend table
PALETTE.LHT   8192 bytes =  32 rows × 256   lighting table
PALETTE.SHD   8192 bytes =  32 rows × 256   shading table
```

None of these files has a header — they are raw tables identified by size.

## Reference

### PAL — the palette

Retail `.pal` files are 1024 bytes: 256 entries of 4 bytes each, in index
order:

| Byte | Meaning |
| ---: | --- |
| +0 | red, 0–255 |
| +1 | green, 0–255 |
| +2 | blue, 0–255 |
| +3 | observed `0` in every entry of every retail palette; presumably a flags/padding byte (the layout matches a Windows `PALETTEENTRY`) |

Channels are full 8-bit values (0–255), **not** 6-bit VGA values —
`PALETTE.PAL` entry 255 is `(255, 255, 255, 0)`. Community-authored
palettes in plain 768-byte RGB-triplet form also exist in the wild; accept
by file size.

Real example — the first 8 entries of `palettes/PALETTE.PAL`
(`totala1.hpi`), which are the classic Windows/VGA primaries:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


index 0 = black, 1 = maroon, 2 = green, 3 = olive, 4 = navy, 5 = purple,
6 = teal, 7 = gray … index 255 = white.

Transparency is **not** a palette property: GAF/TNT data encode
transparency structurally (RLE skip commands, feature cells), and index 0
is an ordinary opaque black. (In practice index 0 doubles as the "empty"
value in many places, but a literal stored 0 pixel is drawn black.)

### ALP — blend table (256 × 256)

`result_index = ALP[a * 256 + b]` gives the palette index approximating a
blend of colors `a` and `b`. Used for translucency effects (shadows,
explosion glow) in the software renderer. Verified structure from retail
data: row *a* maps each *b* to a mix; `ALP[i][i] = i` does not hold exactly
(it is a precomputed nearest-color table, not exact math).

### LHT — lighting table (32 × 256)

32 light levels × 256 indexes: `result = LHT[level * 256 + index]`.
Level 0 is approximately the identity mapping (242 of 256 entries map to
themselves in retail data — the rest hit nearest-color duplicates), and
higher levels remap toward brighter palette entries, monotonically: mean
luminance rises from +0.0 at row 0 to +51.6 at row 31. Used to brighten
sprites/terrain — this is the table behind the disc of lit ground retail
draws around an explosion. Nothing in the weapon corpus selects a row; the
engine chooses it.

### SHD — shading table (32 × 256)

Same layout as LHT, but a full brightness ramp rather than a darkening-only
one. Identity sits in the middle: row 15 maps 232 of 256 entries to
themselves (row 14 maps 216). Rows below it darken, reaching near-black at
row 0, which maps only index 0 to itself and drops mean luminance by about
97. Rows above it *brighten* past identity, up to about +54 mean luminance at
row 31 — the top rows do not approach identity, they overshoot it. Used for
shadows, terrain shading, and model face lighting; `dont-shade` COB pieces
select the identity row directly.

For model lighting, `canmove` in an FBI cannot by itself select the mobile
shading path. Retail factories set it so their move order can place an output
rally point, yet have `MaxVelocity=0` and remain fixed structures. Their
shaded pieces use fixed-structure vertex interpolation through this full
signed SHD ramp; actual locomoting units use the mobile flat-face path. See
[fbi.md](fbi.md) for the source-field distinction.

The two tables overlap in what they can express: LHT covers roughly the same
brightening range as SHD rows 15–31 at twice the resolution (LHT row 3 and
SHD row 16 both lift mean luminance by 6.8; LHT row 5 and SHD row 17 both by
12.6).

The exact engine semantics of which row is selected when (light levels,
shadow depth) are not documented; the row/column layout and the ramp
structure above are confirmed directly against the retail tables.

## Usage notes

- Map tilesets (TNT) were authored against `PALETTE.PAL`; a map's OTA does
  not select a palette. Menu art uses `GUIPAL.PAL` (identical size/layout).
- Team colors occupy palette regions, and `energycolor`/`metalcolor` UI
  values in `gamedata/SIDEDATA.TDF` refer to those shared-palette indexes.
  Model textures use the source-authored player frames in `LOGOS.GAF`, not a
  global palette substitution: select the entry's player frame, apply SHD,
  then resolve the resulting index through this palette ([gaf.md](gaf.md)).
- Weapon definitions reference beam colors by palette index
  (`color=165;` in [tdf.md](tdf.md)).

## Unknowns and caveats

- The fourth PAL byte's intended meaning (flags?) is unknown; it is zero in
  all retail data.
- ALP/LHT/SHD semantics beyond the verified table shapes (which effects use
  which rows, row selection math) are engine-internal and undocumented.
- Whether the engine ever consults 768-byte 3-byte-entry palettes is
  unconfirmed; retail archives contain only the 1024-byte form.

## Sources

- *PAL, ALP, LHT, SHD*, TA Design Guide — roles (256 colors, usage):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/paldesc.htm>
- Table shapes and channel ranges verified directly against
  `palettes/PALETTE.PAL`, `PALETTE.ALP`, `PALETTE.LHT`, `PALETTE.SHD` from
  `totala1.hpi`. OpenTA parser: `formats/pal.go`.
- The 3DO format note (`ta-3do-fmtV2.txt`) embeds the full default palette
  as C source, matching `PALETTE.PAL`.
