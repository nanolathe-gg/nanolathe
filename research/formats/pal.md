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

None of these files has a header — they are raw tables identified by size.

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
blend of colors `a` and `b`. It is a precomputed nearest-color table, not
exact math, so it is not required to be symmetric — feed the operands in the
order the consumer uses them.

**The diagonal is exact.** Measured against the retail `PALETTE.ALP`, all 256
entries of `ALP[i][i]` map to `i`. This **corrects** the previous sentence
"`ALP[i][i] = i` does not hold exactly". The distinction matters: the model
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

### LHT — lighting table (32 × 256, brighten-only)

```
result = LHT[level * 256 + index]          level 0 .. 31, index 0 .. 255
```

Byte-for-byte layout: 8192 raw bytes, no header, row stride 256. The engine
loads the file as a single 8192-byte block and indexes it as

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


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
* Brightening is monotonic across levels. Mean delta rises from +0.00 (row 0)
  through +0.24 (row 1), +1.94 (row 2), +6.83 (row 3), +10.22 (row 4), and so on
  to +51.51 at row 31. White (255) maps to white and black (0) maps to black
  at every level; mid grays and terrain tones brighten smoothly and then
  collapse — the top rows map many distinct sources onto the same bright
  entries (row 31 maps sources 1–6 onto 249–254, the bright orange/yellow
  band).
* The table never darkens. Darkening is the province of `SHD` rows 0–14; `LHT`
  is brighten-only.

Retail usage in the original engine: `LHT` drives the lit-ground halo drawn
around an explosion and around muzzle flashes. The engine precomputes a small
square flash texture (side `N`, stored as `N*N + 24` bytes with a tiny header)
whose bytes encode intensity — the inner core varies around palette index
`0x6F` minus a jittered radial distance, a thin ring is exactly `0x6E`, and
everything outside the disc is `0xFF` transparent — then for each screen pixel
where that texture is opaque the underlying indexed pixel `src` is replaced by
`LHT[level * 256 + src]` at a level derived from the disc intensity. Level
selection, compositing order (after the flat tile pass, before shadows, units,
and fog), and the whole-tick countdown cadence that drives the animation are
presentation-only; the effect draws from the CRT presentation random stream,
not the simulation random stream, and has no authoritative or network-visible
state. The flash therefore never darkens ground and never blends two palette
indices — it is a single-source brighten through the table. Nothing in the
weapon corpus selects an `LHT` row directly; the engine derives the level from
flash geometry. See the presentation contract `[03 §4.3]` and the dedicated
brightening note in `research/retail-executable-spec/03` for the lifecycle
and ordering details.

### SHD — shading table (32 × 256)

Same layout as LHT, but a full brightness ramp rather than a darkening-only
one. Identity sits in the middle: row 15 maps 232 of 256 entries to
themselves (row 14 maps 216). Rows below it darken, reaching near-black at
row 0, which maps only index 0 to itself and drops mean luminance by about
97. Rows above it *brighten* past identity, up to about +54 mean luminance at
row 31 — the top rows do not approach identity, they overshoot it. Used for
shadows, terrain shading, and structure model face lighting; `dont-shade` COB
pieces select the identity row directly.

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

The exact engine semantics of which row is selected when for general
lighting beyond the explosion flash halo remain undocumented; the
row/column layout, the ramp structure above, and the exclusive flash
binding of `LHT` are confirmed directly against the retail tables. The
`level` to flash-intensity mapping and any fading envelope across ticks
are presentation tuning not captured by the file format. Darkening and
full-range lighting use `SHD`, not `LHT` — see that section for the
identity row and the structure-only reach of model face lighting.

The following mean-luminance deltas for `LHT` rows are useful as generation
oracles or as test fixtures; they were measured against the retail palette
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
pin row 15. The row interpolates across the face with the corners, and only
textured faces route through SHD — flat-colored faces keep their resolved
palette color at every orientation. See [3do.md](3do.md) "Face shading".

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
  global palette substitution: select the entry's player frame, apply SHD,
  then resolve the resulting index through this palette ([gaf.md](gaf.md)).
- Weapon definitions reference beam colors by palette index
  (`color=165;` in [tdf.md](tdf.md)).

## Unknowns and caveats

- The fourth PAL byte's intended meaning (flags?) is unknown; it is zero in
  all retail data.
- `ALP` blend semantics and any `ALP` runtime consumer beyond the tag
  `ALPHA TABLE` remain presentation-only unknowns — no consumer is proven in
  the bounded renderer search.
- The exact `discByte → LHT level` mapping for the explosion flash and any
  multi-tick fading envelope are presentation tuning; the disc shape and the
  single-consumer binding are established, the level arithmetic is not.
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
