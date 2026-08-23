# GAF — Image and Animation Containers (`.gaf`)

## Overview

GAF files are Total Annihilation's indexed-color image containers. One GAF
holds many named **entries**; each entry holds one or more **frames**
(making it a still image or an animation). All pixels are 8-bit indexes into
the shared palette ([pal.md](pal.md)).

Three conventional uses:

- **Animations** (`anims/`): explosions, feature/reclaim animations, UI art,
  the mouse cursors (`anims/CURSORS.GAF`), menu graphics (an entry per
  button, looked up by GUI gadget name — see [gui.md](gui.md)), and
  build-menu "gadget" pictures (`<unitname>_gadget.gaf`, one 64×64
  three-frame entry per unit: normal / highlighted / disabled).
- **Textures** (`textures/`): model textures referenced by name from 3DO
  primitives. Multi-frame texture entries animate (flashing lights);
  ten-frame entries in `textures/LOGOS.GAF` are the team-color textures
  (frame *n* is drawn for player *n*).
- **Build pictures inside menu GAFs** (e.g. `anims/ARMALAB.GAF` contains a
  64×64 entry per unit the ARM Kbot Lab can build).

## Format at a glance

```
+-----------------------------+ 0x00
| Header: version, entry count|
+-----------------------------+ 0x0C
| u32 entry_offsets[count]    |──► Entry (40 bytes: frame count + name)
+-----------------------------+       |
| Entries, frame tables,      |       └─► FrameRef[frames] (8 bytes each)
| frame headers, pixel data   |               |
| (pointer-linked, unordered) |               └─► Frame header (24 bytes)
+-----------------------------+                     └─► pixels (raw or RLE)
                                                      or subframe ptr table
```

All offsets are absolute file offsets. All integers little-endian.

## Reference

### File header (12 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| 0x00 | 4 | u32 | version | `0x00010100` in 945 of 950 retail GAFs. The exceptions are `anims/TERRAIN.GAF` and `anims/VISMASKS.GAF` (engine-internal mask data), which store `0` here but keep the same container structure — readers that hard-require the version will reject them. |
| 0x04 | 4 | u32 | entry_count | number of entries |
| 0x08 | 4 | u32 | unknown | `0` in all retail files |

Immediately followed by `entry_count` × u32 absolute offsets, one per entry.

Real example — `anims/ARMALAB.GAF` from `totala1.hpi`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


version `0x00010100`, 6 entries, unknown 0, first entry @ 0x5D0.

### Entry header (40 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| +0 | 2 | u16 | frame_count | May be `0` (placeholder entries exist in retail data) |
| +2 | 2 | u16 | unknown1 | `1` in every entry of every retail GAF (11,881 entries surveyed) |
| +4 | 4 | u32 | unknown2 | `0` in every retail entry |
| +8 | 32 | char[32] | name | NUL-terminated, NUL-padded. Lookup is case-insensitive. |

Immediately followed by `frame_count` × 8-byte **frame references**:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | absolute offset of the frame header |
| +4 | 4 | u32 | small integer, 1–10 in retail data, constant across all frames of an entry. Very likely the per-frame display duration/rate (see below); not confirmed by any primary source. |

The second frame-reference value correlates with animation speed in retail
data — fast-spinning cursors in `anims/CURSORS.GAF` store small values
(`cursorairstrike`, 16 frames, value 1; `cursorpickup`, 24 frames, value
2), slow or static art stores 10 (`cursornormal`, build-menu gadget pics).
A plausible reading is "game ticks (1/30 s) per frame". Treat as a strong
hypothesis, not established fact.

A competing historical reading, from the 1998–2001 community `GAFBuilder`
tool's source: it types this field `Animated As Long` and its load/save code
only ever reads/writes exactly `2` ("animated") or `10` ("fixed") — a 2-state
flag, not a continuous duration. Retail data's spread of small values (not
just 2/10) doesn't cleanly fit either reading; both remain unconfirmed.

Real example — entry 0 of `ARMALAB.GAF`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


2 frames, unknown1=1, name `ARMACONM`, frame ref 0 = { ptr 0x510,
value 10 }. Note that pixel data is deduplicated: in `ARMALAB.GAF` the
entries `ARMACONM`, `ARMFROG`, and `ARMZEUS` have distinct frame headers
that all point at the same pixel `data_offset` (identical placeholder art).
Retail files never share the frame *headers* themselves across entries,
but readers must not assume pixel extents are uniquely owned.

### Frame header (24 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| +0 | 2 | u16 | width | pixels, > 0 |
| +2 | 2 | u16 | height | pixels, > 0 |
| +4 | 2 | i16 | x_offset | signed placement offset (see below) |
| +6 | 2 | i16 | y_offset | |
| +8 | 1 | u8 | unknown1 | `9` in **all 48,519 retail frames** — a constant, historically mislabelled "palette index"; meaning unknown. |
| +9 | 1 | u8 | compressed | `0` = raw pixels, `1` = per-row RLE (only these two values occur in retail data) |
| +10 | 2 | u16 | subframe_count | if nonzero, this frame is composed of subframes (see below). Composition is common: roughly half of retail frames are composed. |
| +12 | 4 | u32 | unknown2 | `0` in all retail frames |
| +16 | 4 | u32 | data_offset | → pixel data, or subframe pointer table when `subframe_count > 0` |
| +20 | 4 | u32 | unknown3 | Historically called "timing" — specifically, the 1998–2001 `GAFBuilder` tool names this exact offset `FPS` and exposes it as a user-editable, save-round-tripped field — but ~27% of retail frames carry nonzero garbage here (in `ARMALAB.GAF`: mostly 0, one frame `480`, one `7025344`). Ignore; the plausible timing value lives in the frame *reference* record instead. |

Real example — frame @ 0x510 of `ARMALAB.GAF`:

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


64×64, offset (281, 171), unknown1=9, compressed=1, no subframes, data @
0x24, trailing u32 = 0.

#### Placement offsets

`x_offset`/`y_offset` position the frame relative to the animation's anchor
point: the pixel at `(x_offset, y_offset)` within the frame lands on the
anchor. This lets frames of one animation differ in size while staying
registered (explosions grow around their center). Two caveats from retail
data: GUI gadget art can carry offsets unrelated to the menu using it (the
engine places gadgets by GUI coordinates instead), and composed subframes
may extend slightly outside the parent canvas (clip when compositing).

### Raw pixels (`compressed = 0`)

`data_offset` points at `width × height` bytes, row-major, one palette
index per pixel. Every pixel is opaque — raw frames have no transparency
encoding.

### RLE pixels (`compressed = 1`)

Each of the `height` rows is stored as:

| | |
| --- | --- |
| u16 | row payload byte count (may be 0 = fully transparent row) |
| bytes | payload, exactly that many bytes |

The payload is a sequence of command bytes, decoded until exactly `width`
pixels have been produced:

- If bit 0 of the command is `1`: emit `command >> 1` **transparent**
  pixels.
- Else if bit 1 is `1`: read one byte, repeat it `(command >> 2) + 1`
  times.
- Else: copy the next `(command >> 2) + 1` literal bytes.

Transparency exists *only* via the skip command (and uncovered pixels of
composed frames). A literal or repeated palette index 0 is an opaque black
pixel, not transparency.

Real example — first row of the 80×40 `Credits` frame in
`anims/MAINMENU.GAF` (row payload is 52 bytes):

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


If a row's commands would exceed `width` pixels, or the payload runs out
early, the file is malformed. After producing `width` pixels the payload
must be fully consumed.

### Composed frames (`subframe_count > 0`)

When `subframe_count` is nonzero, `data_offset` points at
`subframe_count` × u32 — absolute offsets of further frame headers.
The frame's own image is produced by compositing the subframes in table
order onto a `width × height` canvas: each subframe is placed at
`(subframe.x_offset - parent.x_offset, subframe.y_offset - parent.y_offset)`
(clipped to the canvas), later subframes overwriting earlier ones where
opaque. Subframes can themselves be composed; cycles are malformed. Canvas
pixels never covered by an opaque subframe pixel are transparent.

## Unknowns and caveats

- Frame header byte +8 (constant 9) and the trailing u32 (garbage) have no
  confirmed semantics. The frame-reference u32 is very likely per-frame
  duration but unconfirmed. Preserve all three, interpret cautiously.
- Controlled retail model probes establish that a 10-frame `LOGOS.GAF`
  entry stores ten complete player-specific indexed textures: select frame
  *n* for player *n*, sample that frame's indexes, then apply the model's
  `PALETTE.SHD` row and resolve the result through the shared palette. This
  is **not** equivalent to deriving one global index substitution from frame
  0. Frames contain entry-specific spatial differences, and some corresponding
  frames even differ in dimensions (for example 32x32 versus 32x33).
  For reference, retail `textures/LOGOS.GAF` holds 18 entries; all are
  10-frame team textures except the ordinary 2-frame `onoff01`
  (`colorslt`, `colorsmd`, `colorsdk`, `colordk2`, `Solid1a/2a/3a/3b`,
  `Solgradb`, `32xlogos`, `32XGouraud`, `Arm32Lt/Dk`, `Core32Lt/Dk`,
  `ArmLogoGouraud`, `CoreLogoGouraud`). The behavior is retail-measured but
  not documented by Cavedog.
- Entries with `frame_count = 0` occur (`ARMCHEM`, `ARMFIDO` in
  `ARMALAB.GAF`) and must be tolerated.
- `anims/TERRAIN.GAF` and `anims/VISMASKS.GAF` use version 0 (see header
  table); their pixel payloads are engine masks, not palette art.

## Sources

- *GAF File Format Document 1.0* — structures and RLE scheme:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/ta-gaf-fmt.txt>
- *GAF File Content Description*, TA Design Guide — roles, gadget
  conventions, LOGOS speculation:
  <https://units.tauniverse.com/tutorials/tadesign/tadesign/gafdesc.htm>
- Verified against `anims/ARMALAB.GAF` and `anims/MAINMENU.GAF` from
  `totala1.hpi`, a field survey of all 950 retail GAFs (48,519 frames), and
  OpenTA's decoder (`formats/gaf.go`).
