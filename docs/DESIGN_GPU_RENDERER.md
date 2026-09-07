# Design — The frame draw list and the GPU renderer

`internal/drawlist` (new), `internal/platform/gpurender` (new), and the
recording side of `internal/client`. One committed-frame walk records one
ordered list of draw commands; two executors replay it. The **classic**
executor is today's software composer writing palette indices into a byte
surface. The **modern** executor replays the same list through Ebitengine
onto the GPU, in palette-index space, and expands to RGB once at the end. The
user picks one; the simulation cannot tell which.

This document is listed by [ARCHITECTURE.md](ARCHITECTURE.md), which owns
package boundaries. [DESIGN_PRESENTATION_CLIENT.md](DESIGN_PRESENTATION_CLIENT.md)
owns what a frame contains and in what order; this document owns how that
walk is recorded and how the recording reaches pixels. Rules every diff is
reviewed against are in [INVARIANTS.md](INVARIANTS.md); I11's single
sanctioned presentation switch is this one.

## 1. Purpose and boundary

Three goals, in priority order, and the order is a rule:

1. **Parity.** Modern mode paints the same pixels classic mode paints. Until
   it does, on the capture matrix in §6, no enhancement is started.
2. **One walk.** There is exactly one committed-frame ordering in the tree,
   `drawCommittedFrame` `[03 §1]`. It is not duplicated for the GPU. It
   records; executors replay.
3. **Room to grow.** Once parity holds, modern mode is where zoom that scales
   the world rather than the pixels, supersampling for every object,
   emissive glow, and lit materials are built. Each is a recorded divergence
   of modern mode (§5), never a change to what classic paints.

The boundary inherits everything from the presentation design's one-way
valve. Neither executor holds a pointer into a live pool, reads the
simulation RNG, or writes authoritative state [I6]. The GPU executor goes
one step further: it never reads the committed frame at all. Its only input
is the recorded list. That is what makes "replay the same list through both
executors and compare" a complete parity test rather than a sample of one.

Two things this design is not:

* **Not a perspective renderer.** Retail's projection is integer
  orthographic with a half-height shear `[03 §2.5]`, and the look of the game
  is that projection. Modern mode keeps it. Camera zoom, when it comes, scales
  the composed world image, not the projection.
* **Not a true-colour pipeline.** Retail's shading rows, its alpha table, its
  light table, and its fog gray table are index-to-index remaps, not
  arithmetic on colour `[03 §4.3]`. Reproducing them in RGB changes the look
  (flashes wash toward pink, fogged terrain goes coarse) and costs the same as
  reproducing them exactly: one table texel per pixel. Modern mode therefore
  stays in index space through the whole retail composite and touches RGB
  only in the expansion pass and in the enhancement stage after it.

## 2. Packages, files and key types

### 2.1 `internal/drawlist` — the recording

Pure Go, no Ebitengine, no device. Imported by `internal/client` (recorder and
classic executor) and by `internal/platform/gpurender` (modern executor).

* `List` — one frame's commands in record order, with reusable backing slices
  so a steady-state frame allocates nothing. `Reset()` between frames.
* `Sink` — the executor interface, one method per command family (§3 C-G3).
  `List.Replay(Sink)` calls them in record order.
* Command families, each a plain struct carrying **physical palette indices**
  and immutable resource references:

  | Family | Carries | Classic executes as |
  |---|---|---|
  | `Terrain` | tile set reference, tile index grid window, camera origin | the tile blitter |
  | `Sprite` | GAF frame reference, position, clip rect, blit kind (`Keyed`, `Tinted`, `Lit(row)`, `Scaled(src,dst)`), transparent key | the keyed, ALP-tinted, LHT-lit and scaled GAF blitters |
  | `Glyphs` | FNT reference, glyph run, baseline, colour byte | the FNT blitter `[03 §7.1]` |
  | `Fill` | rect, index, style (`Solid`, `Outline`, `LitRect(row)`, `ShadeRect(row)`) | `fillIndexedRect`, `frameIndexedRect`, the UI light/shade rects |
  | `Line` | two endpoints, index | `drawIndexedLine` |
  | `Points` | packed `(x, y, index)` triples | nanolathe particles `[03 §5.5]`, flash discs, sprinkle 2×2 fills |
  | `Model` | the projected face list of one composed subject (§2.3), origin, key mode, flags | the model rasterizer and its commit |
  | `Fog` | the op list `render.BuildFogOpsWindowInto` produced, already clipped | the three fog fills and the fog GAF blit `[03 §3.3]` |
  | `Surface` | an indexed byte surface (minimap, radar), destination rect | `UIBlitIndexed` |
  | `Cursor` | GAF frame reference, hot spot | `drawCursor` `[07 §8]` |
  | `Expand` | none | `convertIndexedToRGBA` |

  A `Model` record is the durable form of a unit, feature model or
  projectile model: the face list with per-vertex screen position, height key,
  UV and shade row, the primitive's texture reference (or LOGOS frame choice),
  the flat colour byte, plus the subject flags — key plane or painter order
  `[03 R-REN-03A §2]`, supersample `[03 R-REN-03A §6]`, waterline threshold,
  digger erase `[03 R-REN-03A §8]`, nanoframe reveal bands and outline colour,
  shadow shear, and the attached children with their height deltas
  `[03 R-REN-03A §4]`. `internal/client` already builds every one of these
  values; recording them is the refactor, not computing them.

### 2.2 `internal/client` — recorder and classic executor

`drawCommittedFrame` keeps its ten barriers and every gate it has today. Each
place that writes into `c.indexed` becomes a record into `c.list`, and the
byte-writing code moves behind `classicSink`, a `Sink` implementation in the
same package whose methods are the existing blitters. `Frame` therefore
becomes: record, replay through the classic sink, cursor, expand. The parity
fixtures that digest the indexed surface see nothing change.

The recorder never batches, reorders or culls beyond what the walk already
does. If two sprites overlap, the list says so in that order.

`ComposeFrameSnapshot` gains the recorded `List` beside `Indexed` and `RGBA`,
which is what the diff tool replays through the GPU executor.

### 2.3 `internal/platform/gpurender` — the modern executor

Imports Ebitengine; joins `internal/platform/ebitenapp`, `internal/audiobackend`
and `cmd/nanolathe` in the architecture test's Ebitengine allowlist. Exposes:

* `New(pal *palette.Tables, opts Options) *Renderer` — uploads the tables
  once: `PAL` as 256×1 RGBA, `ALP` as 256×256, `SHD` and `LHT` as 256×32,
  `Gray` and `Blue` as 256×1, each storing indices in the red channel.
* `(*Renderer).Execute(list *drawlist.List, w, h int) *ebiten.Image` — the
  `Sink`; replays into the frame's indexed offscreen and returns the expanded
  RGB image for `Draw` to present, or for `--shot` to read back.
* Resource caches keyed by pointer identity: tile-set atlases per map, GAF
  frame atlases filled on first use, FNT glyph atlases, the 3DO texture atlas
  with its LOGOS frames, and the per-frame slot atlas for models (§3 C-G5).

The indexed offscreen is an RGBA8 image whose red channel holds the palette
index. Every shader runs in Kage pixel mode and samples with nearest
filtering, so `index = int(r * 255 + 0.5)` recovers the byte exactly.

The executor groups consecutive commands of one family into one Ebitengine
draw where that is order-preserving (C-G3). The families that read the
destination pixel — `Tinted`, `Lit`, `LitRect`, `ShadeRect`, the gray and
checker fog fills, the model shadow commit and the waterline tint — cannot be
fixed-function blends because they are table lookups on the destination. They
run as **layers**: a run of same-family commands whose screen rectangles are
pairwise disjoint is drawn into a scratch overlay, then one table pass folds
the overlay into the world image over the union rectangle. A command that
overlaps an earlier member of the run closes the layer and opens the next.
Two overlapping smoke puffs therefore blend one after the other, exactly as
the byte writers do `[03 R-COMP-01 §2]` `[03 R-FX-02 §3]`.

### 2.4 `internal/platform/ebitenapp` — the switch

The adapter owns one `client.Client` and, lazily, one `gpurender.Renderer`.
`Draw` asks the client for the frame's list and either uploads the classic
bytes as today or executes the list. The active executor is `Options.Renderer`
at start and may be changed at run time through the client's display options;
both executors keep their own caches, so a switch costs one frame of atlas
warm-up and nothing else. There is exactly one such switch in the program.

### 2.5 `cmd/nanolathe` — flags and capture

`--renderer classic|modern` selects the start-up executor. `--shot` composes
through the classic path as today; `--shot-renderer modern` additionally runs
a hidden Ebitengine loop for one frame, executes the recorded list, reads the
pixels back and writes them beside the classic capture; `--shot-renderer both`
writes both and the diff. The diff itself is `tools/framediff` (§6).

## 3. Contracts — C-G1 … C-G11

* **C-G1 One walk.** `drawCommittedFrame` is the only committed-frame
  ordering. It records; it does not know which executor will replay. No
  executor walks the committed frame `[03 §1]` [I6].
* **C-G2 Physical indices only.** Every byte in a record is a physical
  `PALETTE.PAL` index. Logical GUI colours, primitive colours, FNT colours and
  `dcb[]` entries are resolved through the logical map **before** recording,
  as the presentation design's C7 already requires of the byte writers
  `[03 §4.3]`. GAF, PCX and TNT bytes are physical already and pass through.
* **C-G3 Replay preserves order.** `List.Replay` visits commands in record
  order. An executor may merge consecutive same-family commands into one
  device draw only when no merged command's pixels depend on another merged
  command's result: opaque keyed sprites merge freely; destination-reading
  families merge only while their rectangles are pairwise disjoint (§2.3).
* **C-G4 Exact index arithmetic.** Indices live in the red channel of RGBA8
  images, sampled nearest in pixel mode, decoded by rounding. Every table
  operation is an integer texel fetch on the uploaded table. There is no
  linear filtering, no blending arithmetic on indices, and no float
  intermediate that can land between two entries.
* **C-G5 Height key on the GPU.** Retail admits a model pixel when the stored
  key is at most the incoming key, so the pixel's final owner is the **last
  drawn face whose key equals the maximum key at that pixel**
  `[03 R-REN-03A §2]` `[03 R-REN-03A §3]`. Modern mode draws every keyed
  subject in two passes over a per-frame slot atlas: pass one writes each
  face's key into the slot with `max` blending; pass two draws the faces in the
  same order, samples the slot's key, and discards any pixel whose key is
  below the stored maximum. Keys are bytes on both sides. Attached children
  add their height delta before the compare and store the wrapped low byte
  `[03 R-REN-03A §4]`. A subject without a key plane skips pass one and is
  pure painter order. Until Phase 3 of the plan, modern mode obtains a
  subject's image from the classic rasterizer instead and uploads it; the
  record is the same either way.
* **C-G6 Supersample in index space.** A structure under `Anti_Alias` is
  rasterized at twice the size and resolved two-by-two through `ALP` — top
  pair, bottom pair, then the two results — including the background index,
  which is the retail fringe defect and is kept `[03 R-REN-03A §6]`
  `[03 R-REN-03A §7]`. Mobile units are never supersampled.
* **C-G7 Fog is one pass.** The recorded fog ops are converted to a per-tile
  grid texture (kind, variant, frame, pattern parity) and applied by one
  shader over the world image: solid fills write the dark index, gray fills
  write `Gray[dst]`, patterned fills test the same `(x + y + parity) & 1` the
  byte writer tests, and fog GAF frames sample their frame `[03 §3.3]`
  `[03 §4.3.3 R-RR16-A §1]`. The visible result per pixel is the byte writer's.
* **C-G8 Expansion last, PAL only.** The final pass maps index to colour
  through `PALETTE.PAL` alone, forcing alpha opaque as the software expansion
  does. Nothing after it in the retail composite exists. The enhancement stage
  of §5, when it exists, begins after this pass and only in modern mode.
* **C-G9 Model textures resolve once.** The 3DO texture atlas is built from
  the same resolution the classic path performs at load: case-insensitive name
  against the side's texture set then the fallback set, a miss becoming flat
  colour, exactly-ten-frame entries being LOGOS team textures chosen per owner
  at draw time, other multi-frame entries animated by the phase-7 sequence
  cursors `[03 §2.4.1]`. The record carries the resolved frame, so the atlas
  never re-resolves a name.
* **C-G10 No device in tests.** `internal/drawlist` and the recorder are
  covered by ordinary tests with no window. GPU parity runs in the retail
  tier, through `--shot-renderer both` and `tools/framediff`, because CI has
  no GPU. A GPU behaviour that cannot be checked by replaying a recorded list
  has been designed wrong.
* **C-G11 Classic is the reference.** Where modern differs from classic, the
  difference is either a defect against this document or an entry in §5. A
  divergence in §5 names the classic behaviour it replaces and cites the
  research the classic behaviour implements. Retail parity remains classic
  mode's contract, owned by the presentation design; modern mode's contract
  is parity with classic.

## 4. Retail behaviour that is not a bug

Everything the presentation design lists under this heading holds for both
executors, because both replay the same record. Two are worth restating
because a GPU habit would "fix" them:

* **A structure paints over an aircraft in a later row.** Cross-subject order
  is Y-row painter order with no depth test `[03 R-RAST-01 §7]`. The height
  key of C-G5 is a property of one subject's image, never of the scene. The
  slot atlas exists so that no two subjects share a key image.
* **The anti-aliased building has a coloured fringe.** The two-by-two
  resolve blends the background index in `[03 R-REN-03A §7]`. A resolve that
  excludes uncovered texels is cleaner and wrong.

## 5. Divergences

Modern mode has none while the plan is in its parity phases. Enhancements
land here, one entry each, in the shape `what modern does — what classic does
and cites — why`. The candidates, in the order they are expected:

* **World-space zoom.** Compose at native scale into an offscreen sized to the
  effective view and scale that image to the viewport, instead of the classic
  path's per-object scaling under `Camera.Scale` `[F-P1-008]`.
* **Supersampling for every subject.** The 2× raster and `ALP` resolve of
  C-G6 applied to mobile units and sprites as well as structures.
* **Emissive glow.** Beams, flashes, nanolathe and authored effect frames
  drawn a second time into an emissive layer after expansion, blurred, and
  added in RGB. Eight-bit only; there is no float target in Ebitengine.
* **Lit materials.** Per-pixel lighting on models from face normals and
  authored or remastered material maps, replacing the unshaded texel write
  and the shaded `SHD` row walk `[03 §2.4.1]` for the subjects that opt in.

## 6. Verification

**Capture matrix.** The parity gate is a fixed list of `--shot` invocations
kept in the plan and, once stable, in `tools/check-retail`: at least two maps,
one early and one mid-battle tick count, `640x480` and `1024x768`, the side
rail open (`--shot-select`), one modal, `Shading` and `Anti_Alias` on and
off, and `DitheredFog` on and off. Zoom stays at 1 in the matrix; classic
zoom is not a parity target.

**Two gates.**

1. *Recording refactor* (plan Phase 1): the classic capture of every matrix
   entry is byte-identical to its pre-refactor baseline, and the parity
   fixtures' digests do not move.
2. *Modern executor* (plan Phase 2): `tools/framediff` reports **zero**
   differing pixels between classic and modern for every matrix entry. Phase
   3, which moves rasterization onto the GPU, is allowed a stated tolerance
   in the plan because triangle fill rules differ from the 16.16 edge walk; the
   tolerance is measured, not assumed, and confined to face edges.

**Ordering evidence for C-G5.** The geometry-only ownership experiment in
`[03 R-REN-03A §3]` (`ARMSOLAR` rest and dishes open, `ARMLAB` rest) is
repeated against the GPU key pass; the piece that owns each pixel must agree
with the classic rasterizer except on face-edge pixels.

**Performance.** `--profile-seconds` reports classic and modern side by side.
Modern must not present slower than classic at `1024x768`, and the design's
reason to exist is measured at `1600x1200` and above.

## 7. Research map

| Behaviour | Owning research |
|---|---|
| The ten barriers and the strip order | `[03 §1]` |
| Orthographic projection and half-height shear | `[03 §2.5]` |
| Y-row painter order, no scene depth test | `[03 R-RAST-01 §6]` `[03 R-RAST-01 §7]` |
| Composition image, key plane, admission rule, piece walk | `[03 R-REN-03A §1]` `[03 R-REN-03A §2]` `[03 R-REN-03A §3]` |
| Staging image, attached children, height delta | `[03 R-REN-03A §4]` |
| Face dispatch, four span writers, LOGOS frames | `[03 §2.4.1]` `[03 R-REN-03A §5]` |
| Structure supersample and its fringe | `[03 R-REN-03A §6]` `[03 R-REN-03A §7]` |
| Waterline and digger erase | `[03 R-REN-03A §8]` |
| Shadow raster, punch-out, tinted commit | `[03 R-REN-03D]` |
| Palette, `ALP`, `LHT`, `SHD`, gray table | `[03 §4.3]` `[03 §4.3.3 R-RR16-A §1]` |
| Tinted blitter family for strips | `[03 R-COMP-01 §2]` `[03 R-FX-01 §3]` `[03 R-FX-02 §2]` `[03 R-FX-02 §3]` |
| Fog composite tiles and variants | `[03 §3.3]` |
| Nanolathe particles | `[03 §5.5]` |
| FNT text | `[03 §7.1]` |
| Software cursor | `[07 §8]` |

## 8. Not implemented and open

* Everything in this document is design until the plan's phases land; the
  plan (`PLAN_GPU.md`, kept out of the tree) tracks which phase is current.
* Ebitengine is pinned at `v2.10.0-rc.3`, the first version whose macOS and
  Linux window and Metal paths build with `CGO_ENABLED=0`. Move to the final
  2.10 when it ships; nothing here depends on a release-candidate API.
* The run-time renderer switch needs a home in the battle options screen. The
  flag comes first; the option entry is interface work owned by
  [DESIGN_INTERFACE_HUD_INPUT.md](DESIGN_INTERFACE_HUD_INPUT.md).
* Classic zoom's known faults (fog not scaling with the world, the map-edge
  band) are not parity targets and are not fixed by this design; world-space
  zoom in modern mode is the intended replacement.
