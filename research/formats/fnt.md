# FNT — Bitmap Fonts (`.fnt`)

## Overview

`.fnt` files in `fonts/` are Total Annihilation's bitmap fonts, used by the
GUI system for all menu and in-game text. A GUI font gadget selects one by
basename (`filename=SMLFONT;` → `fonts/SMLFONT.FNT`, see [gui.md](gui.md)).

The format is a tiny fixed-height, variable-width, 1-bit-per-pixel glyph
atlas for up to 256 character codes. Glyph bits select "on" pixels; color
comes from the GUI layer, not the font.

No public format note for TA `.fnt` survives in our source set — the layout
below was reverse-engineered directly from the retail files and verified by
rendering glyphs (they produce correct letterforms).

## Format at a glance

```
+------------------------------+ 0x00
| u8  height   (pixel rows)    |
| u8  unused   (= 0)           |
| i8  y_offset (1..3 in retail)|  glyph rows start at penY - y_offset
| u8  first_code (= 0)         |  offset table is indexed by code - first_code
+------------------------------+ 0x04
| u16 glyph_offset[256 - first]|  file offset per character code; 0 = none
+------------------------------+ 0x204 (when first_code = 0)
| glyph records:               |
|   u8 advance                 |
|   ceil(advance*height/8) B   |  1bpp bitmap, row-major, MSB first
+------------------------------+
```

## Reference

### Header

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| 0x00 | 1 | u8 | glyph height in pixels (all glyphs share it) |
| 0x01 | 1 | u8 | never read by the retail executable; 0 in every retail font |
| 0x02 | 1 | i8 | **vertical offset**: the executable places the glyph's first row at `penY − y_offset` (it reads the byte as signed). 1–3 across the retail fonts (height ≤ 12 → mostly 1; height 13–17 → 2 or 3) |
| 0x03 | 1 | u8 | **first character code**: the offset table is indexed by `code − first_code`; 0 in every retail font |
| 0x04 | 2 × (256 − first_code) | u16[] | absolute file offset of each character's glyph record, indexed by `code − first_code`; `0` = character not present. The executable applies no upper bound to the index, so a font must carry an entry for every code from `first_code` to 255 |

The header is four *single* bytes, not the two 16-bit words (`u16 height` at
0x00, `u16 unknown` at 0x02) that older community notes describe: the
executable reads the height from byte 0 alone, never reads byte 1, reads byte
2 as the signed vertical offset the rasterizer subtracts from the pen Y, and
reads byte 3 as the first character code that biases the offset table. The
u16 reading appears to work only because bytes 1 and 3 are zero in every
retail font. See [03 §7.1] (`R-FONT-01 §1`, `§4`) for the rasterizer
contract.

Since offsets are u16, an FNT file cannot exceed 64 KiB. Retail fonts are a
few KiB (`SMLFONT.FNT` is 2713 bytes in the installed `totala1.hpi`, height
11, offset 1, 223 glyphs present covering 0x20 through the Windows-1252 high
range; most retail fonts carry only the 94 printable ASCII glyphs).

### Glyph record

| Offset | Size | Description |
| ---: | ---: | --- |
| +0 | 1 | u8 glyph advance in pixels — the pen moves by exactly this amount and the bitmap is exactly this wide; there is no separate bearing or spacing |
| +1 | ceil(advance × height / 8) | bitmap: `advance × height` bits, row-major top-to-bottom, left-to-right, most-significant bit first, packed continuously across rows (rows are **not** byte-aligned) |

Real example — from `fonts/SMLFONT.FNT` (`totala1.hpi`), header
`0B 00 01 00` (height 11), the offset table entry for `A` (code 65) is
`0x0340` (832). The record there is width 8 followed by 11 bytes, which
decode to:

```
........
........
...#....
..#.#...
..#.#...
.#...#..
.#####..
.#...#..
#.....#.
........
........
```

The space glyph (code 32, offset 524) is width 7 with an all-zero bitmap —
spacing is encoded as ordinary blank glyphs; there is no separate metrics
table, kerning, or baseline data beyond the header's vertical offset.

## Unknowns and caveats

- Byte 0x01 is never read; its authored meaning, if any, is unknown (it is
  0 in every retail font). Decider: a font-authoring tool of the period.
- **No validation at all.** The file is read whole
  and used in place; a missing or zero-length font is fatal (path as the
  message) for the two startup fonts and every side font; a truncated font's
  offsets point past the block and the rasterizer reads what follows
  (`[02 R-MALF-01 §9]`, `[03 R-FONT-01 §1]`).
- Rendering (no inter-character spacing beyond the advance, no line
  spacing in the FNT path, colour selection, the caller-drawn shadows seen
  in-game) is engine behaviour, specified in [03 §7.1] (`R-FONT-01`), not
  stored in the font.
- Interpretation of codes ≥ 0x80 follows Windows-1252 in retail data
  (matching the localized strings in TDF files), but the font itself just
  maps byte values and the engine applies no code page.

## Sources

- *FNT*, TA Design Guide — role only (one paragraph; no layout):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/fntdesc.htm>
- Layout reverse-engineered and render-verified against
  `fonts/SMLFONT.FNT`, then validated arithmetically against all 24 retail
  fonts (this repository, 2026).
