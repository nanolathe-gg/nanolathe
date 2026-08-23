# PCX — Unit Pictures and Backgrounds (`.pcx`)

## Overview

Total Annihilation uses the standard ZSoft PCX image format for
non-palette-shared 2D art:

- `unitpics/<UNITNAME>.PCX` — the 96×96 unit portrait shown by the F1 info
  screen.
- `bitmaps/` — menu backgrounds, logos, glamour (mission victory) images.
- `palettes/GUIPAL.PCX` — a palette shipped in PCX clothing.

PCX is fully documented elsewhere (it predates TA); this page records only
what TA requires. TA reads **8-bit, single-plane, RLE-encoded PCX with a
256-color palette appended at the end of the file** (PCX version 5). Images
in other bit depths do not load.

## Format at a glance

```
+---------------------------+ 0x00
| 128-byte PCX header       |  0x0A, version 5, encoding 1, 8 bpp
+---------------------------+ 0x80
| RLE image data            |  per scanline, bytes_per_line each
+---------------------------+ len-769
| 0x0C marker               |
| 256 × RGB palette         |  768 bytes, 8-bit channels
+---------------------------+
```

## Reference

Header fields TA cares about (full header is 128 bytes):

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| 0x00 | 1 | u8 | manufacturer, `0x0A` |
| 0x01 | 1 | u8 | version, `5` (with 256-color palette) |
| 0x02 | 1 | u8 | encoding, `1` (RLE) |
| 0x03 | 1 | u8 | bits per pixel per plane, `8` |
| 0x04 | 8 | u16×4 | xmin, ymin, xmax, ymax (inclusive: width = xmax−xmin+1) |
| 0x0C | 4 | u16×2 | DPI (ignored) |
| 0x10 | 48 | | 16-color EGA palette (ignored) |
| 0x41 | 1 | u8 | planes, `1` |
| 0x42 | 2 | u16 | bytes per scanline (may exceed width; pad ignored) |
| 0x44 | 2 | u16 | palette info (ignored) |

RLE decoding per scanline: read a byte; if the top two bits are set
(`byte >= 0xC0`), it is a run count `byte & 0x3F` and the next byte is the
pixel value; otherwise it is a literal pixel. Decode `bytes_per_line`
bytes per scanline, then crop to the image width.

Trailer: the byte at `filesize − 769` must be `0x0C`, followed by 256 × 3
bytes of RGB (8-bit channels) — the image's own palette. Unit pictures are
*not* forced to the shared game palette; the engine loads the embedded one
(art was nevertheless usually authored with TA-safe colors).

Real example — `unitpics/ARMFLASH.PCX` from `totala1.hpi`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


manufacturer 0x0A, version 5, RLE, 8 bpp, extent (0,0)–(95,95) → 96×96,
72 DPI; planes=1, bytes_per_line=96; trailer byte at `len−769` = `0x0C`
with the 256-color palette following.

## Conventions

- Unit pictures are 96×96. Other PCX art is 640×480 (backgrounds/glamour)
  or logo-sized.
- Filenames match unit short names; lookup is case-insensitive.

## Unknowns and caveats

- Whether the engine honors `bytes_per_line` padding or assumes
  width == stride for its own art is untested; retail files have
  width == stride.
- Some retail PCX palettes disagree with `PALETTE.PAL`; the F1 screen
  displays them with their own palette (community observation).

## Sources

- ZSoft *PCX File Format Technical Reference* (public standard).
- *PCX*, TA Design Guide — TA usage conventions:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/pcxdesc.htm>
- Verified against `unitpics/ARMFLASH.PCX` from `totala1.hpi`.
- OpenTA parser: `formats/pcx.go`.
