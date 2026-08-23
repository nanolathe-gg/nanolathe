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
| u16 height   (pixel rows)    |
| u16 unknown  (= 1)           |
+------------------------------+ 0x04
| u16 glyph_offset[256]        |  file offset per character code; 0 = none
+------------------------------+ 0x204
| glyph records:               |
|   u8 width                   |
|   ceil(width*height/8) bytes |  1bpp bitmap, row-major, MSB first
+------------------------------+
```

## Reference

### Header

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| 0x00 | 2 | u16 | glyph height in pixels (all glyphs share it) |
| 0x02 | 2 | u16 | small value 1–3 across the 24 retail fonts, loosely correlating with font size (height ≤ 12 → mostly 1; height 13–17 → 2 or 3). It is **not** bits-per-pixel — glyph record sizes prove all fonts are 1bpp. Possibly descender rows or line spacing; unconfirmed. |
| 0x04 | 512 | u16[256] | absolute file offset of each character's glyph record, indexed by byte value; `0` = character not present |

Since offsets are u16, an FNT file cannot exceed 64 KiB. Retail fonts are a
few KiB (`SMLFONT.FNT` is 2704 bytes, height 11, 222 glyphs present
covering 0x20 through the Windows-1252 high range; most retail fonts carry
only the 94 printable ASCII glyphs).

### Glyph record

| Offset | Size | Description |
| ---: | ---: | --- |
| +0 | 1 | u8 glyph width in pixels (advance width; includes spacing) |
| +1 | ceil(width × height / 8) | bitmap: `width × height` bits, row-major top-to-bottom, left-to-right, most-significant bit first, packed continuously across rows (rows are **not** byte-aligned) |

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
table, kerning, or baseline data.

## Unknowns and caveats

- Header u16 at 0x02 (1–3) has unknown purpose; see the header table.
- Rendering details (inter-character spacing beyond the glyph width, line
  spacing, color selection, drop shadows seen in-game) are GUI-engine
  behavior, not stored in the font.
- Interpretation of codes ≥ 0x80 follows Windows-1252 in retail data
  (matching the localized strings in TDF files), but the font itself just
  maps byte values.

## Sources

- *FNT*, TA Design Guide — role only (one paragraph; no layout):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/fntdesc.htm>
- Layout reverse-engineered and render-verified against
  `fonts/SMLFONT.FNT`, then validated arithmetically against all 24 retail
  fonts (this repository, 2026).
