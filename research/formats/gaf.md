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
  (the owner's colour index selects the frame, independently of player slot;
  **Established** [03 R-RAST-01 §3]).
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

**Established:** all offsets are absolute file offsets and all integers are
little-endian [02 R-MALF-01 §6]. Asset examples and surveys below describe
the named reference content, not limits on other GAF files. The earlier
950-file survey and later 958-file survey have different scopes; frame totals
that include subframes must not be compared with top-level frame totals.

## Reference

### File header (12 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| 0x00 | 4 | u32 | version | **Established (reference-asset observation):** ordinary art uses `0x00010100`; `anims/TERRAIN.GAF` and `anims/VISMASKS.GAF` use `0` with the same container layout. Retail ignores this field [02 R-MALF-01 §6]; readers that require the usual version reject those masks. |
| 0x04 | 4 | u32 | entry_count | number of entries. **The executable reads the low 16 bits as a signed value** (`[02 R-MALF-01 §6]`): a count with bit 15 set loads no entries. |
| 0x08 | 4 | u32 | unknown | `0` in all retail files |

Immediately followed by `entry_count` × u32 absolute offsets, one per entry.

Real example — `anims/ARMALAB.GAF` from `totala1.hpi`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

version `0x00010100`, 6 entries, unknown 0, first entry @ 0x5D0.

### Entry header (40 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| +0 | 2 | u16 | frame_count | May be `0` (placeholder entries exist in retail data) |
| +2 | 2 | u16 | unknown1 | `1` in every entry of every retail GAF (11,881 entries surveyed). **The executable reads the low byte of this word as the entry's loop flag — Established (`[06 R-WFX-01 §1]`):** a playback cursor copies it at initialization and, on passing the last frame, wraps to frame 0 when it is nonzero or marks the sequence finished when it is zero. Retail data therefore makes every sequence loop by default; the engine clears the byte in memory for one-shot sequences (weapon explosion art, the bound `explosion`/`explode2..5`/`nuke1`/`alfboom1`/`h2oboom2`/`lavasplash` effects, feature burn sequences) after loading. |
| +4 | 4 | u32 | unknown2 | `0` in every retail entry |
| +8 | 32 | char[32] | name | NUL-terminated, NUL-padded. Lookup is case-insensitive. |

Immediately followed by `frame_count` × 8-byte **frame references**:

| Offset | Size | Type | Description |
| ---: | ---: | --- | --- |
| +0 | 4 | u32 | absolute offset of the frame header |
| +4 | 4 | u32 | small integer, 1–10 in retail data, constant across all frames of an entry. **Per-frame display duration in whole simulation ticks — Established:** the executable's playback cursor loads this value as the per-frame countdown and steps in whole ticks; the loader leaves it untouched. The cursor arithmetic is the primary source, and the spread of values 1–10 matches tick counts. |

The second frame-reference value correlates with animation speed in retail
data — fast-spinning cursors in `anims/CURSORS.GAF` store small values
(`cursorairstrike`, 16 frames, value 1; `cursorpickup`, 24 frames, value
2), slow or static art stores 10 (`cursornormal`, build-menu gadget pics).
These are whole simulation ticks per frame. The cursor's test is "countdown below 2 advances", so a stored value of 0 or 1 shows the frame for one advance and a value `h ≥ 2` for exactly `h` advances (`[06 R-WFX-01 §1]`).

Real example — entry 0 of `ARMALAB.GAF`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

2 frames, unknown1=1, name `ARMACONM`, frame ref 0 = { ptr 0x510,
value 10 }. Note that pixel data is deduplicated: in `ARMALAB.GAF` the
entries `ARMACONM`, `ARMFROG`, and `ARMZEUS` have distinct frame headers
that all point at the same pixel `data_offset` (identical placeholder art).
The earlier survey found no shared frame *headers* across entries,
but readers must not assume pixel extents are uniquely owned.

### Frame header (24 bytes)

| Offset | Size | Type | Name | Description |
| ---: | ---: | --- | --- | --- |
| +0 | 2 | u16 | width | pixels; zero is accepted |
| +2 | 2 | u16 | height | pixels; zero is accepted |
| +4 | 2 | i16 | x_offset | signed placement offset (see below) |
| +6 | 2 | i16 | y_offset | |
| +8 | 1 | u8 | color_key | `9` in **all 48,519 retail frames**. On the raw path it is the transparent palette index: the frame draw passes this byte to the blitter, which skips every matching source pixel. The RLE path carries transparency in skip runs and does not consume this key. Community notes mislabel it "palette index" and list it as unknown. |
| +9 | 1 | u8 | compressed | `0` = raw pixels, `1` = per-row RLE (only these two values occur in retail data) |
| +10 | 2 | u16 | subframe_count | if nonzero, this frame is composed of subframes (see below). Composition is common: roughly half of retail frames are composed. **The executable reads only the low byte** (loader and compositor alike), so the effective count is `subframe_count & 0xFF`; on a *subframe* header a nonzero **high byte** (offset +11) makes the compositor draw that subframe through the tinted ALP blit path, requiring the window's alpha-blend capability, enabled independently of Shading (`[02 R-MALF-01 §6]`, `[03 R-REN-03D §4]`). Retail data: maximum count 12, high byte always 0 (census of all 958 GAFs, 123,294 frames). |
| +12 | 4 | u32 | unknown2 | `0` in all retail frames |
| +16 | 4 | u32 | data_offset | → pixel data, or subframe pointer table when `subframe_count > 0` |
| +20 | 4 | u32 | unknown3 | Historically called "timing" — specifically, the 1998–2001 `GAFBuilder` tool names this exact offset `FPS` and exposes it as a user-editable, save-round-tripped field — but ~27% of retail frames carry nonzero garbage here (in `ARMALAB.GAF`: mostly 0, one frame `480`, one `7025344`). The bounded retail loader/cursor/blitter traces do not read it; animation hold lives in the frame *reference* record instead (Established). |

Real example — frame @ 0x510 of `ARMALAB.GAF`:

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

64×64, offset (281, 171), unknown1=9, compressed=1, no subframes, data @
0x24, trailing u32 = 0.

#### Placement offsets

`x_offset`/`y_offset` position the frame relative to the animation's anchor
point: the pixel at `(x_offset, y_offset)` within the frame lands on the
anchor. This lets frames of one animation differ in size while staying
registered (explosions grow around their center). Two caveats from retail
data: GUI gadget art can carry offsets unrelated to the menu using it (the
engine places gadgets by GUI coordinates instead), and composed subframes
may extend outside the parent canvas. **Established:** the ordinary retail
compositor clips to the destination, not the parent dimensions; only a
parent-sized host raster necessarily clips those extensions (see below).

### Raw pixels (`compressed = 0`)

`data_offset` points at `width × height` bytes, row-major, one palette
index per pixel. Raw frames carry no skip runs. The ordinary keyed blitter
compares each pixel against the frame's `color_key` (header byte +8). Other
consumers can use a different key or sample storage directly; their contracts
are listed under "Unknowns and caveats" and [03 R-FONT-01 §6].

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

**Publication omission:** The retail-derived example is omitted from this
edition. The surrounding format description retains its stated evidence and
confidence.

**Established** (`[02 R-MALF-01 §6]`): nonempty rows decode until
`width` pixels have been covered. Skip, repeat and literal runs are clamped to
the remaining width. A zero-length skip consumes its command byte and does
nothing else. A repeated run consumes one palette byte; a literal consumes
only the bytes copied after clamping. The discarded suffix of an overflowing
literal is not read. This corrects the earlier full-authored-count source
advance claim for the ordinary row output path.

The stored payload count does not bound command decoding. A short row
continues into following bytes until its width is covered; extra payload
bytes after that point are ignored. Regardless of how many command bytes were
consumed, the next row begins at `row + 2 + payload_count`. A payload count of
zero leaves the row untouched (transparent).

**Established:** a leaf with zero width or height draws nothing and does not
read its pixel or RLE stream. A composite still traverses its children even
when its own dimensions are zero; the child geometry determines drawing.

**Nanolathe host policy:** both metadata and pixel readers use the same checked
row walk. Row headers, stored row extents and bytes actually consumed must fit
within the file; zero-size leaves still require an in-file data location.
Out-of-file data rejects the bank, without substitute frames. An aggregate
command budget bounds zero-progress skips and overlapping short-row scans;
this is a host work limit, not an authored or retail restriction.

### Composed frames (`subframe_count > 0`)

When the low byte of `subframe_count` is nonzero, `data_offset` points at
`(subframe_count & 0xFF)` × u32 — absolute offsets of further frame headers.
**Established:** the general blitter draws these children in table order at
the same pen position. Each leaf's destination origin is
`(pen_x - leaf.x_offset, pen_y - leaf.y_offset)`. A parent-sized host canvas
therefore places a child at `(parent.x_offset - child.x_offset,
parent.y_offset - child.y_offset)`, but that canvas is a host representation:
the general retail blitter clips only to its destination, not to the parent
frame's dimensions. Alternate children use destination-dependent ALP blending,
so flattening against a fixed background cannot preserve that operation.
`[03 R-COMP-01 §2]` owns composition and `[03 R-REN-03D §4]` owns tinting.

The drawing routines recurse into composite children, but the ordinary loader
relocates only one child level. **Unknown:** supported nested file layouts;
see `[02 R-MALF-01 §6]`. Cycles and excessive depth are rejected by the checked
host reader, independently of retail's unsafe relocation.

### Host decoder safety policy

Retail imposes no aggregate allocation or composition-depth bound during its
pointer relocation `[02 R-MALF-01 §6]`. Nanolathe therefore applies explicit
host-safety budgets to decoded pixels and frame references, and bounds composed
frame depth. A separate aggregate RLE-command budget counts every command
visited in each unique frame, including zero-length skips and repeated reads
caused by short rows; its default is 128 Mi commands. These are implementation
limits rather than file-format or retail rules. Repeated entry-table pointers
share their immutable decoded reference
table, so a repeated pointer does not consume the reference budget again.

## Nanolathe metadata index

**Established (Nanolathe host-boundary policy).** `LoadGAFMetadata` and its
VFS form retain the file/entry fields, every frame-reference pointer and delay
word, each frame's geometry and signed origin, and the complete composite
child graph including the low-byte child count and high-byte alternate-blitter
selector. They allocate no decoded pixel or transparency planes. `LoadGAF`
uses that same validated index before it materializes pixels, so metadata and
pixel consumers have one signed entry-count rule, first-match lookup, alias,
malformed-payload, aggregate reference/pixel-geometry and composite-depth
policy. In particular, a malformed raw or RLE payload rejects the whole bank
for both readers; metadata does not turn a presentation failure into a
simulation-only success. The pixel-geometry budget remains an acceptance bound
for the metadata reader even though it has no pixel allocation, keeping the
two readers on one corrupt-content policy.

## Nanolathe on-demand source

**Established (Nanolathe host storage policy).** `GAFSource` retains encoded
bytes and the same fully validated metadata graph as the eager reader. Its
explicit whole-bank limits bound unique geometry, reference count, depth,
expanded traversal and RLE command work even for frames never requested. The
byte-slice constructor copies its input; the VFS constructor takes ownership
of a bounded private read. Metadata is borrowed and immutable. A digest binds
later source-cache reloads to the bytes whose timing was first accepted.

`Frame` first bounds the unique and expanded geometry reachable from one
selected root, then runs the existing materializer with a fresh alias cache. Ordered children,
raw/RLE dispatch, alternate selectors, origins, plain compatibility rasters
and direct-storage views retain the eager reader's contracts. Returned trees
are immutable and remain valid after the source or a cache entry is retired.
The source retains no decoded tree. Its `Transient` frame hint is host-only,
propagates to children and doubled variants, and is never serialized.
`GAFFrameBytes` accounts frame headers, child-reference capacity and owned
pixel-plane capacity once per shared node for host cache accounting.

The ordinary `LoadGAF` and `LoadGAFMetadata` defaults are unchanged. Effect
presentation selects its separate source and root budgets in
[DESIGN_PRESENTATION_CLIENT](../../docs/DESIGN_PRESENTATION_CLIENT.md#on-demand-effect-art).
This changes storage, not the authored image or playback cadence, and does not
establish supported nested layouts or any still-unknown consumer operation.

## Nanolathe direct-raster view

**Established (Nanolathe host-boundary policy).** `GAFFrame.DirectRaster`
returns a checked immutable view for consumers that read storage directly
`[02 R-MALF-01 §6]`. Raw leaves reuse their decoded bytes, which preserve the
key byte. For RLE leaves the pixel reader preserves a separate, at-most
`width × height` copy starting at the authored data location; this is encoded
storage, including row counts and commands, not the expanded image. This
adds at most one byte per accepted decoded pixel to pixel-reader storage;
the metadata reader remains pixel-free. A file span too short for this view
leaves ordinary RLE decode available but rejects the direct consumer.

**Unknown:** a portable result for direct reading of composite child-table
storage after runtime relocation. File offsets are not relocated pointers and
host pointer bytes must never substitute for retail bytes. The direct view
therefore rejects composites, including those whose child table would fit the
requested raster. The visibility-mask compiler reports this with resource
provenance; the projectile model adapter omits the unsupported textured face.
This is a host safety policy, not a claim that retail silently skips it.

## How the engine loads it

`[02 §6]` and `[02 R-MALF-01 §6]` own the behaviour. Byte-level facts: the
file is read whole (a missing or zero-length file is null — fatal with the
path for the effects/anims cache, silent for a feature `filename`); the
version word at 0 is **never read**; the loader biases every entry offset,
frame-header offset, frame data offset, subframe pointer and subframe data
offset by the block address and **writes them back**, with no bound — a
truncated or corrupt GAF faults during load. Entry lookup by name is a
linear case-insensitive scan, first match wins; a name that is absent is a
null sequence with no message. **Established (inspected executable):** the
comparison folds `A..Z` to `a..z` and leaves all other bytes, including
`128..255`, unchanged. The comparison helper has a locale-dependent branch,
but its locale selector starts zero and the inspected executable reference
census contains reads only, with no writer or address-taking reference to
that selector. The default byte comparison therefore supplies the contract;
it does not apply Unicode case folding.

## Unknowns and caveats

- **Established (bounded consumer traces):** the alternate-child byte is a
  dispatch choice of a particular consumer, not a universal property of a
  flattened image. The ordinary compositor selects ALP tint for alternate
  children; the tinted compositor propagates tint to all descendants.
  Gray and dithered fog compositors recurse into every child in table order
  without testing the alternate byte, preserving their own grayscale or
  dither operation and their raw-frame gate. A nonzero-mode GAF glyph
  compositor sends **every** child through the ALP compositor, independently
  of its alternate byte. The calculated-flash compositor likewise recurses
  with its own LHT operation. See [02 R-MALF-01 §6],
  [03 R-COMP-01 §2], [03 R-FONT-01 §6] and [03 R-FX-01 §4].
- **Established (bounded raw-consumer traces):** the precomputed visibility
  mask walkers and the projectile-model texture span read the selected
  frame's data directly as a row-major raster. They do not expand composite
  children or test the alternate byte. Supplying a composite to those paths
  makes the child-table storage become raster input, not an ordinary-child
  composition. The frame-to-surface adapter also wraps the data directly.
  The dormant scaled keyed compositor does dispatch alternate children to
  its scaled ALP partner; the inspected caller census finds no live caller
  for either scaled helper. These findings do not establish a general scaled
  image or model-texture flattening rule.
- **Unknown:** alternate-child handling in remaining feature-mask and
  structure-texture consumers not included in the bounded traces above.
  Trace each selected frame from its caller through its actual pixel reader
  before assigning one of these operations. Nanolathe's compatibility raster
  still includes ordinary children only, clipped to the parent canvas, and
  leaves alternate-only coverage transparent. That host fallback is not the
  retail result for the closed consumers above. The matching
  `TODO(question)` in `formats/gaf_metadata.go` remains for feature-mask and
  structure-texture traces. Fog and glyph compositors now traverse their own
  child graphs; direct masks and projectile textures use `DirectRaster`.
  Modern fog uses ordered child operations when a selected frame cannot use
  its ordinary atlas path, preserving repeated destination reads and alternate
  black-child tinting without a parent-sized intermediate raster.
- **Established (bounded loader, cursor and blitter traces):** frame header
  byte +8 is the raw path's color key; the trailing frame-header word is not
  read by those consumers. Animation hold comes from the frame-reference
  record, not that trailing word. **Unknown:** original author intent for the
  trailing word and other preserved unused fields. A third-party editor
  labeling the field `FPS` does not establish a retail reader. Preserve the
  bytes without deriving timing from them.
- In ordinary keyed rendering, the surveyed frames' constant key `9` makes
  index 9 transparent. This is not a universal raster-consumer rule. Across the 958 GAFs in the reference install only 274 of
  6,068 raw frames contain it at all, and three of them account for 99.9% of
  those pixels: `anims/fog.gaf` (30%), `anims/fogtiles.gaf` (40%) and
  `anims/vismasks.gaf` (23%). Those three are mask families drawn entirely
  from key pixels and index 0 — decoding them without the key inverts them
  into solid rectangles of palette 9 (bright blue, `84,84,252`) and fills every
  sight shape to its bounding box. The handful of stray key pixels elsewhere
  (4-8 per file in a few unit textures) is noise.
- **A correct decode of the interface side panels looks like coloured static —
  Established.** The panel entries of the side interface
  GAFs (`anims/ARMINT.GAF` `PANELSIDE` and `PANELSIDE2`, both 129×480, RLE) are
  authored as a per-pixel dither drawn almost entirely from the *darkest* entry
  of many different palette ramps — `PALETTE.PAL` is 16 ramps of 16 shades,
  bright at `base+0` and darkest at `base+15`, and roughly 90% of the panel's
  pixels are a `base+15` index (`159`, `95`, `63`, `143`, `47`, `127` …, 41
  distinct indexes in all, every one of them under RGB `(23,19,39)`). Decoded
  and rendered at 1:1 the panel is near-black with a faint mottled texture;
  brightened, it is dense multi-hue noise. **This is the art, not a decode
  defect** — three independent checks:
  1. The RLE stream is exactly self-consistent. All 480 rows decode to exactly
     129 pixels and consume their payload to the byte, and the last row ends
     precisely at the next frame's `data_offset`. A misread stream cannot land
     on the width 480 times in a row.
  2. `PANELSIDE2` — same file, same size, same RLE path, decoded by the same
     code — resolves under the same dither to a clean, coherent ARM diamond
     emblem with unbroken diagonal highlight strokes. A row-stride or
     run-length error would shear that emblem; it does not.
  3. Retail's own rendering of the panel background uses exactly this index
     set, in the same rank order (`159` most common, then `95`, `63`, `143`,
     `47`, `127`), and none of it is brighter than the values above.
  A reader that "fixes" this into something that looks like a metal panel has
  invented art. The engine draws the widgets — minimap frame, order and build
  buttons — *over* this background; a HUD showing bare dark static is missing
  those widgets, not mis-decoding the panel.
- **Established** [03 R-RAST-01 §3]: a 10-frame `LOGOS.GAF` entry stores ten
  complete colour-specific indexed textures. The owner's colour index selects
  the frame independently of player slot. Sample that frame's indexes; the
  shaded structure branch applies its interpolated `PALETTE.SHD` row, while
  the unshaded branch uses the indexes directly [03 R-RND-02A]. This
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
  Nanolathe's metadata/pixel readers (`formats/gaf_metadata.go`,
  `formats/gaf.go`) and authored GAF fixtures. The later subframe census is
  bounded explicitly by [02 R-MALF-01 §6].
