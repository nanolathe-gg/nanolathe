# PAL — Palettes and Color Tables (`.pal`, `.alp`, `.lht`, `.shd`)

## Overview

Total Annihilation renders everything in 8-bit indexed color through one
shared 256-entry palette. The `palettes/` directory holds the palette itself
(`PALETTE.PAL`, plus `GUIPAL.PAL` as the frontend's semantic color-field source table) and three derived lookup tables
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

**Established:** none of these files has a header. Their roles come from
the logical filename and consumer; size describes the expected shape and
cannot distinguish `LHT` from `SHD`, which are both 8,192 bytes [03 §4.3].
Measurements below are bounded observations of the named reference assets,
not requirements on authored replacement tables.

The renderer holds five table slots, not four: the three above plus two
256-entry tables that are **not** shipped as `palettes/` files — a gray table
built at palette-install time from `PALETTE.PAL` (see
`research/retail-executable-spec/03` §4.3.3) and a blue table read only by the
submerged-hull tint (`[R-REN-03A §8]`). The blue table is built from
`PALETTE.PAL` at session init: for each entry the target `(r>>1, g>>1,
(b>>1)+50)` is resolved to the nearest palette colour through the gray
table's sum-sorted search ([03 R-WATER-01 §2]); nothing outside `palettes/`
is read.

## Reference

### PAL — the palette

Retail `.pal` files are 1024 bytes: 256 entries of 4 bytes each, in index
order:

| Byte | Meaning |
| ---: | --- |
| +0 | red, 0–255 |
| +1 | green, 0–255 |
| +2 | blue, 0–255 |
| +3 | observed `0` in every inspected retail palette; retained in the loaded source, but the palette installer generates a zero fourth byte for the display palette (Established) |

Channels are full 8-bit values (0–255), **not** 6-bit VGA values —
`PALETTE.PAL` entry 255 is `(255, 255, 255, 0)`. Community-authored
palettes in plain 768-byte RGB-triplet form are accepted by Nanolathe as an
intentional import extension. This is not the retail byte contract. The
shared parser retains the source bytes, including the reserved fourth bytes,
while its resolved colors are opaque.

Real example — the first 8 entries of `palettes/PALETTE.PAL`
(`totala1.hpi`), which are the classic Windows/VGA primaries:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

index 0 = black, 1 = maroon, 2 = green, 3 = olive, 4 = navy, 5 = purple,
6 = teal, 7 = gray … index 255 = white.

**Established:** transparency is a consumer operation, not a PAL alpha
channel. Ordinary GAF drawing skips raw pixels matching the frame's color
key and RLE skip runs; a literal RLE index 0 remains opaque black. Terrain
tile bytes are ordinary indexed pixels, and model texture spans do not
apply GAF transparency tests. See [fmt gaf] and [03 R-REN-03A §5].

### ALP — blend table (256 × 256)

`result_index = ALP[a * 256 + b]` gives the palette index approximating a
blend of colors `a` and `b`. It is a precomputed nearest-color table, not
exact math, so it is not required to be symmetric — feed the operands in the
order the consumer uses them.

**Established (reference-table observation):** all 256 entries of the
shipped `ALP[i][i]` map to `i`. The retail recovery builder explicitly
preserves each diagonal index, including duplicate RGB colors [03 §4.3.4].
The distinction matters: the model
anti-alias downscale relies on it, since a 2x2 block wholly outside the model
is four copies of the background index and must resolve back to that index or
every building would acquire an opaque box around it.

Consumers: the minimap's 2x supersample composite, translucency effects, and
— the largest one — the structure anti-alias downscale, which reads `ALP`
three times for every output pixel of every `BMcode=0` unit image when the
`Anti_Alias` display option is on. That last consumer is also the source of
retail's red/purple building fringe, because it blends silhouette-straddling
blocks against palette index 1, `(128,0,0)`. See
`research/retail-executable-spec/03` `[R-REN-03A §6]` and `[R-REN-03A §7]`.

### LHT — lighting table (32 × 256, brightening)

```
result = LHT[level * 256 + index]          level 0 .. 31, index 0 .. 255
```

Byte-for-byte layout: 8192 raw bytes, no header, row stride 256. The engine
loads the file as a single 8192-byte block and indexes it as

```
dst = table[level * 256 + src]
```

where `src` is the source palette index after the 256-byte logical-to-physical
lookup and `dst` is again a palette index (resolved to RGB only at present time
through `PALETTE.PAL`). The table is a precomputed nearest-color remap in
`PALETTE.PAL` RGB space — the renderer performs a single byte load, not a
runtime channel-distance search and not arithmetic on `src`.

Row structure measured directly against the retail `PALETTE.LHT` mounted from
`totala1.hpi` (luminance approximated as `0.299R+0.587G+0.114B` on
`PALETTE.PAL`):

* Row 0 is near-identity: 242 of 256 entries map to themselves, the 14
  exceptions are isolated duplicates near palette gaps (for example source 7
  maps to 248, sources 10–15 map to 0, source 208 maps to 251). Mean luminance
  delta is effectively +0.0 at row 0.
* Mean brightening increases across levels. Mean delta rises from +0.00 (row 0)
  through +0.24 (row 1), +1.94 (row 2), +6.83 (row 3), +10.22 (row 4), and so on
  to +51.51 at row 31. White (255) maps to white and black (0) maps to black
  at every level; mid grays and terrain tones brighten smoothly and then
  collapse — the top rows map many distinct sources onto the same bright
  entries (row 31 maps sources 1–6 onto 249–254, the bright orange/yellow
  band).
* **Established (reference-table observation):** this is a brightening
  table, but nearest-color quantization does not guarantee nondecreasing
  luminance for each cell. Under the weighting above, `LHT[3][161] = 66`
  lowers luminance by 10.764 in the installed `totala1.hpi` tables. The
  monotonic mean trend is not a per-pixel guarantee.

**Established:** the explosion flash blitter replaces the existing destination
index through `LHT[(discByte − 0x4F) * 256 + destination]`. The calculated
frame's visible bytes `0x4F..0x6E` select rows `0..31`, while transparent
pixels leave the destination alone. Raw and RLE paths use the same mapping.
The explosion pool draws this secondary flash after the projectile pass and
before its primary art; that is after the unit passes, not before them.
There is no separate muzzle-time halo producer. The generation, animation,
capability gate and composition order are behavioral contracts owned by
[03 §4.3.1], [03 R-FX-01 §4] and [06 R-WFX-01 §2], not tunable PAL fields.

### SHD — shading table (32 × 256)

Same layout as LHT, but a ramp spanning darkening and brightening. Identity sits in the middle: row 15 maps 232 of 256 entries to
themselves (row 14 maps 216). Rows below it darken, reaching near-black at
row 0, which maps only index 0 to itself and drops mean luminance by about
97. Rows above it *brighten* past identity, up to about +54 mean luminance at
row 31 — the top rows do not approach identity, they overshoot it. **Established:** shaded structure model faces use this table; `dont-shade`
COB pieces select row 15 directly. Model shadows use the ALP tint path, not
a shadow-specific SHD row [03 §4.3.2], [03 R-REN-03D §4].

**Which models reach this table.** Retail applies model face shading only to
units whose FBI authors `BMcode=0` — the structure class — and only while the
`Shading` display option is on. Mobile units (`BMcode=1`) are drawn by a
separate piece renderer that maps their textured faces with no SHD step, so
they show no orientation-dependent shading at all; a `dont-shade` in a mobile
unit's script is inert. `canmove` does not select the path and cannot: retail
factories author `canmove=1` so their move order can place an output rally
point, yet have `MaxVelocity=0`, `BMcode=0`, and are shaded like any other
structure. See [fbi.md](fbi.md) for the field distinction and
`research/retail-executable-spec/03` `[R-RND-02A]` for the gate.

The two tables overlap in what they can express: LHT covers roughly the same
brightening range as SHD rows 15–31 at twice the resolution (LHT row 3 and
SHD row 16 both lift mean luminance by 6.8; LHT row 5 and SHD row 17 both by
12.6).

**Established:** consumer-specific row selection is specified in [03 §4.3.1]
for the explosion flash and [03 §4.3.2] for model shading. The table layout
alone does not select a row, animation envelope or render pass.

The following mean-luminance deltas for `LHT` rows are descriptive asset
measurements, not acceptance criteria for modded tables or substitutes for the
recovery algorithm [03 §4.3.4]. They were measured against the retail palette
with the weighting above:

| Row | Mean Δ | Identity |
|----:|-------:|---------:|
| 0 | +0.00 | 242/256 |
| 1 | +0.24 | 230 |
| 3 | +6.83 | 125 |
| 5 | +12.60 | 52 |
| 15 | +31.37 | 8 |
| 31 | +51.51 | 8 |

`LHT` rows 3, 5, 15 approximate `SHD` rows 16, 17, 22 respectively in
mean lift, but the two files are distinct — do not synthesize one from the
other.

`LHT` entries are palette indexes, not RGB triples: a table entry such as
`LHT[31][1] = 249` names a palette slot (the bright orange/yellow band)
rather than an 8-bit channel value. An entry of `0` names black; `255`
names white. The fourth PAL byte is never consulted when building the
table.

For 3DO model face lighting — reached only by `BMcode=0` structures, see
above — the row selection is established: the row is computed per vertex from the face-averaged smooth normal as
`trunc(dot(N, L) * 5.0) mod 32`, with the shipped default light direction
`L = (-0.8, 1.0, 0.25)` (user-settable through three settings written via a
dedicated setter that rebuilds the shadow caches). COB `dont-shade` pieces
pin row 15. The shaded renderer interpolates the row across both textured
and flat-colored faces, selecting `SHD[row*256 + texel]` or
`SHD[row*256 + color]` respectively. The unshaded renderer bypasses SHD for
both. [03 §4.3.2] and [03 R-REN-03A §5] own this dispatch; see also
[3do.md](3do.md) "Face shading".

## Usage notes

- Map tilesets (TNT) were authored against `PALETTE.PAL`; a map's OTA does
  not select a palette. Frontend GUI color fields are authored against the
  same-shaped `GUIPAL.PAL` source table, then retail maps those semantic
  colors into active `PALETTE.PAL` indices before drawing primitives or FNT
  glyphs. GAF/PCX/TNT image bytes are already active `PALETTE.PAL` indices and
  are copied without this GUI map. `GUIPAL.PAL` is not itself the physical
  display palette.
- Team colors occupy palette regions, and `energycolor`/`metalcolor` UI
  values in `gamedata/SIDEDATA.TDF` refer to those shared-palette indexes.
  Model textures use the source-authored player frames in `LOGOS.GAF`, not a
  global palette substitution: select the entry's player frame, apply SHD
  only in the shaded renderer,
  then resolve the resulting index through this palette ([gaf.md](gaf.md)).
- Weapon definitions reference beam colors by palette index
  (`color=165;` in [tdf.md](tdf.md)).

## How the engine loads it

`[02 R-MALF-01 §9]` owns this. The palette loader probes
`palettes\<name>.PAL`; when the file is **missing or zero-length** it decodes
`palettes\<name>.PCX` instead (fatal, path as message, if that fails too),
packs the PCX's 768-byte trailer into 256 four-byte entries with the fourth
byte 0, **writes the 1,024 bytes back to `palettes\<name>.PAL` on the host
file system** (relative to the working directory, result ignored) and
deletes the host files `palettes\PALETTE.ALP`, `.LHT` and `.SHD` so the
derived tables are regenerated. An existing `.PAL` is read whole with no
size or content check.

Nanolathe performs recovery in memory and rebuilds derived tables through
[03 §4.3.4]; it does not write to or delete original asset files. Ordinary
nonempty authored tables remain authoritative. Missing and empty derived
tables are built individually.

## Unknowns and caveats

- **Established (bounded palette-install and table-builder trace):** the
  fourth PAL byte is copied with the source entry but does not provide alpha
  or display flags. Display installation derives RGB and sets the outgoing
  fourth byte to zero; derived-table color searches use RGB only.
  **Unknown:** the original authoring convention for that byte. A period
  palette-authoring specification or producer would settle its intended name,
  but no such evidence was inspected and that name is not needed for playback.
- **Established (calculated-flash lifecycle):** the generated shrinking
  frames and their whole-tick countdown are the entire envelope in the
  explosion-pool path. Allocation binds the secondary cursor, each tick
  advances it, and drawing submits its current frame directly to the LHT
  blitter. Neither advancement nor blitting applies an additional age-based
  intensity, opacity, or fade factor. Frame completion clears the cursor; the
  pool retains the record only while either cursor or its debris piece is
  still live. This closes the former separate-fade question for that path
  [03 §4.3.1], [03 R-FX-01 §4], [06 R-WFX-01 §2].
- The engine never consults 768-byte, three-byte-entry palettes: a `.PAL` is
  loaded whole with no size check and read as 256 four-byte entries, so a
  768-byte file is misread (entries wrong by one byte each, the last 64 taken
  from beyond the block). See "How the engine loads it".

## Sources

- *PAL, ALP, LHT, SHD*, TA Design Guide — roles (256 colors, usage):
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/paldesc.htm>
- Table shapes and channel ranges verified directly against
  `palettes/PALETTE.PAL`, `PALETTE.ALP`, `PALETTE.LHT`, `PALETTE.SHD` from
  `totala1.hpi`. Nanolathe parser: `formats/pal.go`.
- The 3DO format note (`ta-3do-fmtV2.txt`) embeds the full default palette
  as C source, matching `PALETTE.PAL`.
