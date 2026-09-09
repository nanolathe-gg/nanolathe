# PCX — Unit Pictures and Backgrounds (`.pcx`)

## Overview

Total Annihilation uses the ZSoft PCX image format for indexed 2D art:

- `unitpics/<UNITNAME>.PCX` — the 96×96 unit portrait shown by the F1 info
  screen.
- `bitmaps/` — menu backgrounds, logos, glamour (mission victory) images.
- `palettes/GUIPAL.PCX` — a palette shipped in PCX clothing.

PCX is fully documented elsewhere (it predates TA); this page records only
the authored format and the retail reader separately. Stock images use
**8-bit, single-plane, RLE-encoded PCX with a 256-color palette appended at the
end of the file** (PCX version 5). Retail validates the manufacturer and version
bytes; it does not reject by the depth, planes or encoding fields
[02 R-MALF-01 §9].

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
| 0x42 | 2 | u16 | bytes per scanline (may exceed width). **Never read by the executable**, which decodes exactly `width` pixels per row (`[02 R-MALF-01 §9]`). |
| 0x44 | 2 | u16 | palette info (ignored) |

RLE decoding per scanline: read a byte; if the top two bits are set
(`byte >= 0xC0`), it is a run count `byte & 0x3F` and the next byte is the
pixel value; otherwise it is a literal pixel. Standard PCX uses
`bytes_per_line` bytes per row. Retail instead decodes exactly the image width,
ignores the stride, and clamps each run at the row edge; see "How the engine
decodes it" below [02 R-MALF-01 §9].

Trailer: standard PCX places `0x0C` at `filesize − 769`, followed by 256 × 3
bytes of RGB (8-bit channels). **Established:** retail reads the last 768 bytes
without checking the marker [02 R-MALF-01 §9]. These embedded palette bytes
belong to the decoded asset; their presence does not establish that a consumer
installs them as the active display palette. Frontend PCX backgrounds retain
their indexed pixels and display through `PALETTE.PAL`, with GUI semantic-color
mapping handled separately [07 §5 "Retail palette contract"]. The F1 consumer's
palette-installation behavior remains Unknown below.

Real example — `unitpics/ARMFLASH.PCX` from `totala1.hpi`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

manufacturer 0x0A, version 5, RLE, 8 bpp, extent (0,0)–(95,95) → 96×96,
72 DPI; planes=1, bytes_per_line=96; trailer byte at `len−769` = `0x0C`
with the 256-color palette following.

## Conventions

- Unit pictures are 96×96. Other PCX art is 640×480 (backgrounds/glamour)
  or logo-sized.
- Filenames match unit short names; lookup is case-insensitive.

## How the engine decodes it

Owned by `[02 §7]` and `[02 R-MALF-01 §9]`. Checks: the 128-byte header
must read completely, byte 0 must be `0x0A` and byte 1 must be `5`; nothing
else (encoding, depth, planes, stride, marker) is examined; failure returns
0 to the caller with no message. Dimensions are `xmax − xmin + 1` by
`ymax − ymin + 1` from the 16-bit extents and are allocated as read. Rows
are decoded as `width` pixels: a byte with both top bits set is a run of
`byte & 0x3F` copies of the next byte, clamped so a row never overflows;
any other byte is one literal pixel. A truncated file is not detected —
the one-byte read returns nothing and the previous byte is reused as every
further command and value (a stale `0xC0` never advances and hangs the
loader). A file shorter than 768 bytes seeks to a negative palette offset;
the seek fails and the palette bytes are read from the current position.

## Unknowns and caveats

- The engine never reads `bytes_per_line`: each row is decoded as exactly
  `width` pixels, so a padded file shears (each row starts in the previous
  row's padding) without any error.
- **Unknown:** whether the F1 unit-picture consumer installs its PCX trailer
  palette. A trace of that consumer's palette installation calls would settle
  it [07 R-HUD-03 §8]. Do not generalize from the established frontend
  background path or treat a decoded trailer as proof of display selection.

## Sources

- ZSoft *PCX File Format Technical Reference* (public standard).
- *PCX*, TA Design Guide — TA usage conventions:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/pcxdesc.htm>
- Verified against `unitpics/ARMFLASH.PCX` from `totala1.hpi`.
- OpenTA parser: `formats/pcx.go`.

**Writer.** Retail's screenshot writer emits PCX with 63-byte RLE runs and literal bytes below `0xC0` — see `[01 R-PLAT-02 §6]`.
