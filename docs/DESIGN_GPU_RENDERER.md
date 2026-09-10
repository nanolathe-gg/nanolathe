# Design — The frame draw list and the GPU renderer

`internal/drawlist` (new), `internal/platform/gpurender` (new), and the
recording side of `internal/client`. One committed-frame walk records one
ordered list of draw commands; two executors replay it. The **classic** executor is the software composer writing palette indices into
a byte surface. The **modern** executor replays through Ebitengine in palette
index space, with conventional GPU model rasterization permitted by the visual
acceptance policy below. Original, GPU Classic, and Enhanced are the intended
three user-facing modes, backed by one CPU and one shared GPU implementation.
For this experimental milestone the only public choices remain
`--renderer=classic|modern`, default classic. All new rendering is behind
`--renderer=modern`; no Enhanced flag or runtime options entry is added yet.
The simulation cannot tell which executor is selected. The current GPU-only
execution contract is §9–§12; it supersedes the historical P1–P3 CPU model bridge
and fallback requirements below.

This document is listed by [ARCHITECTURE.md](ARCHITECTURE.md), which owns
package boundaries. [DESIGN_PRESENTATION_CLIENT.md](DESIGN_PRESENTATION_CLIENT.md)
owns what a frame contains and in what order; this document owns how that
walk is recorded and how the recording reaches pixels. Rules every diff is
reviewed against are in [INVARIANTS.md](INVARIANTS.md); I11's single
sanctioned presentation switch is this one.

## 1. Purpose and boundary

These are implementation decisions approved by the user, not retail findings.
Retail evidence remains in the owning research document.

1. **Original** targets established retail behavior and preserves the software
   rasterizer. Existing research gaps remain explicit; this is not a claim that
   every current pixel has been verified against retail. Rasterization and CPU
   captures need no GPU; the current interactive Ebitengine window still does.
   A GPU-free window backend is separate, deferred platform work.
2. **GPU Classic** targeted the original appearance and composition rules with
   modern GPU techniques while staying in palette index space. It was the
   modern executor through §11 and is retired by §13: an index-exact composite
   needs one render pass per destination read, which is what keeps the
   executor off a 120 Hz cadence. Its measurements and fixtures remain the
   record of what the index-exact device path cost.
3. **Enhanced** is the modern executor from §13 on. It composes in true colour
   with the palette tables' generating arithmetic in place of their
   nearest-palette quantization, presents at the display's refresh rate and
   interpolates committed poses (§13.5). Differences from Original are measured
   and reviewed visually, including in motion; a pixel threshold alone is never
   approval. No enhancement changes authoritative simulation.

One committed-frame ordering remains `drawCommittedFrame` [03 §1]. Neither
executor reads live pools or simulation RNG or writes authoritative state [I6].
A reusable frame packet owns transient model/command data and shares only
immutable resources. Classic model packets own their completed image planes;
modern model packets own geometry, as specified by C-G5.

The projection remains orthographic with the retail half-height shear
[03 §2.5]. Original stays in palette index space through the retail composite:
ALP, LHT, SHD, Gray and Blue are table operations there [03 §4.3]. Enhanced
keeps every *source-side* table lookup exact (a texel remapped before it is
written) and replaces every *destination-side* lookup with the arithmetic the
table was generated from, applied in RGB by the device blend (§13.3). Enhanced
may add auxiliary material/emission data and RGB passes; material lighting
cannot be reconstructed from the expanded image alone.

## 2. Packages, files and key types

### 2.1 `internal/drawlist` — the recording

Pure Go, no Ebitengine, no device. Imported by `internal/client` (recorder and
classic executor) and by `internal/platform/gpurender` (modern executor).

* `List` — one frame's commands in record order, with reusable backing slices
  with allocation and upload costs measured after warm-up. `Reset()` between frames.
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
  projectile model. Its classic packet owns the completed body or staging
  color, coverage and optional key planes; the pre-punched shadow planes; and
  every image placement scalar. Its modern packet carries the face list with
  per-vertex screen position, height key,
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
same package whose methods are the existing blitters. `Frame` records and replays through the classic sink, including cursor and expansion commands. The parity
fixtures that digest the indexed surface see nothing change.

The recorder never batches, reorders or culls beyond what the walk already
does. If two sprites overlap, the list says so in that order.

`ComposeFrameSnapshot` gains the recorded `List` beside `Indexed` and `RGBA`,
which is what the diff tool replays through the GPU executor.

### 2.2.1 Cached and live model lanes

`internal/render.PieceLane` is the bounded consumer selector for a published
piece pose: `Cached` admits visible pieces with `PieceView.DontCache` clear,
`Live` admits visible pieces with it set, and `All` admits every visible piece.
Construction admits every visible piece in every lane. `PieceDraw.DontCache`
copies that published flag; transform composition remains lane-independent.

The client retains a body image per presentation identity. Its image-discard
revision and full-validity revision are separate: `CacheRevision` discards an
image reference, while `CacheValidityRevision` requests a cached-body rebuild.
`UnitView.InstanceID` is a publication-only identity assigned when the
underlying unit object is replaced. It is deliberately distinct from the retail
pool slot and affects neither simulation state nor RNG; it lets presentation
retention reject same-slot replacements.
The image is rebuilt for first use, an orientation delta strictly greater than
seven, a structure construction-state change, a changed full-validity revision,
or a missing image that the structure/key-plane contract requires. An image-less
mobile subject with a valid state and no required key plane draws `All` directly
without changing the cached orientation reference. The cached lane uses the local composition projection;
the direct live lane adds world position before flooring. A keyed body copies
into staging before its live lane and children resolve under the key; a keyless
body commits first and live pieces and children draw directly in painter order.
The retained unit orientation also governs live-piece transform expansion:
sub-threshold turns retain it while current script pose changes still apply.
Live pieces rasterize at 1x into the current union target, including key-colored
texels that erase cached colors. Each attached child uses its own cached/live
present; group waterline and Digger processing follows child composition.
Construction reveal changes a frame-owned copy, preserving the raw retained
body. The direct fill keeps the same exclusive last-row/column clip as the
local rasterizer.
Classic recording consumes these lanes directly. Geometry-only modern recording
retains only cached-lane faces beside the same presentation identity and emits
current live faces every frame. A keyed packet carries both lanes: cached faces
resolve, reveal and outline first, then native unshaded live faces enter the
same key plane. A keyless packet commits its cached faces and records direct
projected live faces as the later Model command. Neither route creates a
classic image or a device image cache.
[03 R-REN-03A §4][03 R-RAST-01 §2][03 §5.2][03 R-COMP-01 §4]

For its own retained-raster memoization, the classic client also records the
effective presentation inputs that alter the cached physical-index image: the
shaded renderer choice, effective structure supersampling, effective camera
scale, and installed palette-table identity. A change rebuilds the local
memoized image before reuse. These inputs are not additional retail
script-validity or image-purge gates; they prevent a Nanolathe retained raster
from surviving a presentation configuration that changes its pixels.

**PERF-REND-04 minimap surface submission.** `DrawMinimapLayout` keeps the
canonical two-step integer sampling and unchanged letterbox bars, clips the
picture to the framebuffer, and records one owned indexed `Surface` at one-to-one
output size. Frame-local byte storage is reused in lockstep with list reset;
multiple packets own disjoint ranges, and retained clones copy their bytes.
The shared scaled-index shader subtracts the destination atlas origin before
integer source mapping; the real-device fixture exposed and now locks this
existing executor correction. Both executors consume that same packet; viewport markers
remain later point commands. This removes per-pixel point/quad submission without
changing the separate PICTURE/MAPPED/FINAL lifecycle [03 R-MM-01 §1]. A
`Surface` may additionally carry a nonzero presentation identity and revision.
That pair identifies immutable source content for the duration of a durable
command: a zero identity retains the dynamic per-call upload behaviour, and a
cloned list preserves both fields while owning its byte slice. The GPU holds a
small bounded set of such textures and writes pixels again only when the
revision (or dimensions) changes; it never hashes a whole source every frame.
MAPPED consumes the committed visibility mapping version, whereas FINAL still
refreshes for committed contacts and blink phase. Acceptance compares the former
sampling result at native and fractional display sizes, both letterbox axes,
clipped/reversed rectangles and retained replay after source reuse.

A scoped warm-submission benchmark on darwin/arm64 Apple M3 Pro at 126×126
measured the former point producer at 31.4 µs and the surface producer at 20.2 µs,
both zero steady-state allocations. One surface replaces 15,876 points and
63,504 solid vertices with one textured quad. This measures command production,
not whole-frame GPU time or fog/minimap recomposition savings; the surface
executor still uploads indexed bytes for each call.

The classic cached/live implementation was independently reviewed and passed
full, installed-retail and GPU-device gates through `dce05045`. A sequential
scene-3 Ashap Plateau comparison against `dd7f58b6` used seed 7, factories,
1920×1080, 30 TPS, 60 warmup draws and 180 measured frames. Metadata and every
frame's census matched (187–198 units, 6–31 projectiles, 73–162 effects).
Classic Record median/p95/max changed from 12.600/13.195/13.909 ms to
10.412/11.389/11.621 ms; allocation increased from 0.906 to 1.124 MB/frame,
and 30-Hz cadence remained 98%. This is a correctness implementation with a
measured allocation cost, not completion of the remaining allocation work.
Modern Record was 3.972/4.567/4.713 ms versus 4.046/4.555/4.731 ms,
allocation 1.640 versus 1.649 MB/frame, cadence 98% versus 97%; no modern
performance improvement is claimed. Its comparison image was byte-identical.
Classic changed 36,332 pixels inside the model region, consistent with the
corrected cached/live, transparency and shadow paths; both final images were
visually inspected. Modern recording now retains cached geometry per
presentation identity and carries current live geometry in its native packet.

Modern cached/live acceptance compares `dd8c9337` with `1e72ddb7` using the
same scene above, native zoom and automatic detail-art generation disabled.
Metadata and all 180 frame censuses match; classic pixels are identical and
modern changes 36,101 model-region pixels. Both final captures were inspected.
Modern Record median/p95/max is 3.828/4.193/4.649 → 5.156/5.751/14.797 ms;
Submit is 5.512/6.460/13.104 → 5.182/6.093/8.289 ms. Allocation is
1.500 → 1.993 MB/frame after reusing existing packet scratch (the initial
implementation allocated 4.723 MB/frame). Cadence is 98% → 97%, with a
73.861 ms maximum interval in the final run. Classic Record is
10.517/11.282/11.706 → 10.211/11.266/14.058 ms, allocation
1.124 → 1.125 MB/frame and cadence 98% → 97%. These results accept the
bounded cost of the corrected stages; they do not establish a performance gain.

The classic mobile/Digger silhouette and child-window clipping comparison
uses `bfdf1d11` → `e553e499` with the same scene-3 native workload above.
Metadata and every census match. Classic changes 12,965 pixels; modern is
byte-identical. Both final battle images, dry/submerged ARMSUB captures and the
clipped editor fixture were inspected. Classic Record median/p95/max is
10.228/11.973/12.590 → 9.294/10.523/10.817 ms, allocation
1.125 → 1.111 MB/frame and cadence 98% in both runs. Modern Record is
5.554/6.227/14.790 → 5.420/6.334/6.702 ms, Submit
5.393/6.463/7.578 → 5.351/6.542/7.555 ms, allocation
2.164 → 1.991 MB/frame and cadence 96% → 94% (maximum interval
62.293 → 67.527 ms). The modern run establishes unchanged pixels, not a
cadence improvement. Scoped clipping also passes the actual GPU lit-sprite
and scaled-surface checks.

### 2.3 `internal/platform/gpurender` — the modern executor

The bitmap/clipping/shadow closeout comparison used pinned `05cd68af` and
`f258705a`, the same native scene-3 workload above. Unit, projectile, build and
production counts match; newly admitted bitmap effects raise the peak effect
count from 162 to 184 and change the shared CRT camera-shake history.
Both final captures were inspected. Classic Record median/p95/max is
10.650/11.451/11.906 → 10.165/12.423/22.228 ms, allocation
1.125 → 1.148 MB/frame and cadence 98% → 97%. Modern Record is
5.276/5.969/8.595 → 5.288/6.645/10.553 ms; Submit is
5.292/6.109/11.644 → 12.534/28.525/45.214 ms, allocation
2.239 → 4.485 MB/frame and cadence 98% → 76% (79.590 ms maximum interval).
The large calculated table-2 flashes are newly reachable in this workload.
Coalescing contiguous equal-light pixels within their existing scheduler phase
reduced Submit from the initial 14.209 ms median and allocation from
5.001 MB/frame, with identical pixels and all frame censuses in both executors.
Residual flash submission cost remains a measured limitation; this closeout
adds no second flash renderer or cache. Artifacts use
`/private/tmp/nanolathe-last-batch-{baseline,spans}-{classic,modern}`.

Imports Ebitengine; joins `internal/platform/ebitenapp`, `internal/audiobackend`
and `cmd/nanolathe` in the architecture test's Ebitengine allowlist. Exposes:

* `New(pal *palette.Tables, w, h int) *Renderer` — uploads the tables
  once: `PAL` as 256×1 RGBA, `ALP` as 256×256, `SHD` and `LHT` as 256×32,
  `Gray` and `Blue` as 256×1, each storing indices in the red channel.
* `(*Renderer).SetDisplayPalette([256][4]byte)` — updates only the final
  colour row when the client's gamma-adjusted palette changes. Index remapping
  tables and source assets remain immutable `[07 R-FE-01 §11]`.
* `(*Renderer).Execute(list *drawlist.List, w, h int) *ebiten.Image` — the
  `Sink`; replays into the frame's indexed offscreen and returns the expanded
  RGB image for `Draw` to present, or for `--shot` to read back.
* Resource caches keyed by pointer identity: tile-set atlases per map, GAF
  frame atlases filled on first use, FNT glyph atlases, the 3DO texture atlas
  with its LOGOS frames, and reusable GPU model composition surfaces (§3 C-G5).

The indexed offscreen is an RGBA8 image whose red channel holds the palette
index. Every shader runs in Kage pixel mode and samples with nearest
filtering, so `index = int(r * 255 + 0.5)` recovers the byte exactly.

Batching is specified by the compiled executor of §11: the scheduler groups
commands into phases wherever that is order-preserving (C-G3). The families that read the
destination pixel — `Tinted`, `Lit`, `LitRect`, `ShadeRect`, the gray and
checker fog fills, the model shadow commit and the waterline tint — cannot be
fixed-function blends because they are table lookups on the destination. The scheduler therefore runs them over a phase snapshot: a run of
destination-reading commands whose screen rectangles are pairwise disjoint
reads one snapshot; a command that overlaps an earlier member of the run opens
the next phase (§11.2).
Two overlapping smoke puffs therefore blend one after the other, exactly as
the byte writers do `[03 R-COMP-01 §2]` `[03 R-FX-02 §3]`.

### 2.4 `internal/platform/ebitenapp` — the switch

The adapter owns one `client.Client` and, lazily, one `gpurender.Renderer`.
`Draw` asks the client for the frame's list and either uploads the classic
bytes as today or executes the list. The active executor is `Options.Renderer`
at startup. Runtime selection, persistence, and the eventual three labels are
deferred until prototypes receive human visual review. Cache warm-up is measured,
not promised to fit one frame. Graphics-device recovery is not a prototype gate.

### 2.5 `cmd/nanolathe` — flags and capture

`--renderer classic|modern` selects the start-up executor and the default
`--shot` executor. `--shot-renderer modern` records geometry, executes it in a
hidden Ebitengine loop, then reads pixels for the PNG. Explicit
`--shot-renderer both` also composes the independent classic reference and
writes both images and their diff. Readback and PNG encoding are capture costs,
excluded from presentation timing (§6). The diff tool is `tools/framediff`.

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
  A quad batch whose `uint16` index scratch reaches its 65,536-vertex domain
  is submitted and restarted before the next quad; this executor limit never
  drops or reorders geometry. A destination-reading run keeps its one
  pre-run snapshot across such chunks, while an overlapping command closes
  the run and snapshots after the earlier write.
* **C-G4 Exact index arithmetic.** Indices live in the red channel of RGBA8
  images, sampled nearest in pixel mode, decoded by rounding. Every table
  operation is an integer texel fetch on the uploaded table. There is no
  linear filtering, no blending arithmetic on indices, and no float
  intermediate that can land between two entries.
* **C-G5 Durable model composition.** `drawlist.Model.Classic` owns each
  mutable classic body/staging/shadow plane and placement scalar at record
  time; a retained `List.Clone` deep-copies those planes. The pre-punched
  shadow is built while recording, so classic replay has no `UnitDraw`, client
  model state, or per-frame index lookup. It preserves shadow, one body blit,
  then trace-observer order, including a carried child's earlier shadow-only
  command. The optional trace image and observer are diagnostic-only and cannot
  affect pixels. Modern geometry stays a separate owned packet. Ordinary face pixels
  use a per-subject
  maximum-byte-key pass and a color pass in original face order, so ties retain
  the later face [03 R-REN-03A §2–§3]. Conventional triangle interpolation is an
  intentional GPU approximation (§5); tests isolate key admission from that
  approximation. No scene-wide depth buffer may replace subject painter order.
  Texture key-colored texels still participate in face ownership; composition
  transparency is applied at the image boundary [03 R-REN-03A §5].
  Cached body, live pieces, and attached children are separate stages. Render
  each child independently, then composite children sequentially using the full
  signed shifted-key comparison and wrapped-byte store [03 R-REN-03A §4]. Never
  flatten children into one maximum reduction. Waterline, digger, reveal and
  outline retain their established stage order when implemented. The current
  implementation provides the classic subject stages (§10); invalid geometry
  or unavailable resources remain explicit skips (§9). It never substitutes
  a CPU-rendered model image.
  Classic image copies use a separate pool owned by the recording draw list.
  Every body, shadow and trace copy gets a distinct slot until `List.Reset`;
  composition scratch never aliases the recorded planes. `List.Clone` and
  `ModelCommands` still deep-copy all mutable planes for retained consumers.
  Carrier/factory staging images borrow their own composition scratch slot and
  are copied into the draw list only after child composition finishes.
* **C-G6 Structure supersample.** Preserve the cached/all versus live gate,
  pre-shear doubled projection, ordered ALP color resolve, and top-left key
  resolve [03 R-REN-03A §6–§7]. Live pieces draw at native scale afterward.
  Mobile units are not supersampled in GPU Classic. A full subject-wide MSAA
  or SSAA replacement belongs to Enhanced and needs a separate design.
  The GPU recorder retains the cached lane and records current live faces
  separately. Native execution resolves cached structure faces first, then
  rasterizes the unshaded live lane at 1x (§10).
* **C-G7 Fog composition.** The recorded fog ops are converted to a per-tile
  grid texture (kind, variant, frame, pattern parity) and may be applied by a combined
  shader over the world image: solid fills write the dark index, gray fills
  write `Gray[dst]`, patterned fills test the same `(x + y + parity) & 1` the
  byte writer tests, and fog GAF frames sample their frame `[03 §3.3]`
  `[03 §4.3.3 R-RR16-A §1]`. The visible result per pixel is the byte writer's.
* **C-G8 Expansion last, PAL only.** The final pass maps index to colour
  through `PALETTE.PAL` alone, forcing alpha opaque as the software expansion
  does. Nothing after it in the retail composite exists. Enhanced (§13) has no
  separate expansion pass: it resolves each source index through `PALETTE.PAL`
  at the moment it is written, which is the same lookup applied per fragment
  instead of per frame, and every colour the retail composite would have
  looked up is looked up the same way.
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
  no GPU. Small authored GPU fixtures may run on a device-equipped host without retail
  assets. Backend compilation and pixel execution require real device validation.
* **C-G11 Classic is the reference.** Where modern differs from classic, the
  difference must be diagnosed as a defect or an intentional approximation or
  enhancement in §5, with reproducible visual evidence. A
  divergence in §5 names the classic behaviour it replaces and cites the
  research the classic behaviour implements. Retail parity remains classic
  mode's contract, owned by the presentation design; GPU Classic follows the visual-fidelity policy of §1/§5.1, and Enhanced
  follows its separately designed presentation divergences.

### Retained list camera ownership

`List.Clone` captures each non-nil terrain camera by value, including zoom;
subsequent movement of the live camera cannot change a cloned terrain command.
Nil-camera projection remains distinct. The ordinary recording path retains
its same-frame borrowed camera and adds no snapshot allocation. Modern terrain
already uses the command's copied origin and destination dimensions.

Together with C-G5's owned classic model planes, this makes a cloned list
durable across later recording. Modern geometry packets remain independently
owned. Retained terrain and model tests replay A after recording B with changed
camera or pose and compare actual classic pixels, including zoom and nil-camera
projection.

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

### 5.1 GPU Classic raster approximation (approved scope, visual approval pending)

GPU model faces may be triangulated and attributes interpolated by the graphics
backend instead of the classic authored-polygon fixed-point edge/span walk
[03 R-RAST-01 §1]. This can change face interiors as well as edges. The reason
is to use conventional GPU rasterization while preserving the original look.
Large occlusion errors, unexpected missing subjects, incorrect stage ordering,
and unstable seams are defects, not covered by this allowance. Explicit GPU-only
omissions under §9 are tracked separately from raster approximations. Human review of captures and
motion is the acceptance gate; keep approved recipes and measured differences.

The current prototype uses two geometry preparation paths. Simple untextured
clockwise projected rings use conventional triangles. Textured quads and folded
projected rings are prepared as one-pixel-high
quads from the same ordered decreasing-index left chain and increasing-index
right chain as the established span walk; only rows whose right edge is strictly
right of the left edge become strips. The strips carry interpolated key, UV and
shade lanes to the GPU, which still performs the fragment key reduction and
colour write. This preserves the positive-span topology without uploading a CPU
body image. It is a prototype preparation cost and not a claim of exact
inside-face interpolation parity.

The texture-alignment correction extends two-chain row preparation to textured
quads. Conventional quad triangulation created a visible internal UV bend on
ARMSOLAR, so the original ordered polygon determines each row's endpoints.
Rows sharing a source face are batched up to the index-transport limit. GPU
varyings are adjusted by half a horizontal step so sampling at a pixel center
recovers the integer-column lane, and the texture shader floors U/V before
addressing the texel center. A 2^-20 texel guard corrects observed floating
endpoint residue; this is a chosen GPU numerical tolerance which can also bias
values that close to a texel boundary, not a recovered retail constant.

Folded-row attributes remain private float32 preparation data until the device
fragment stage. Each row uses the last applicable descending edge on the ordered
chains with half-open Y bounds and the established biased fixed-point X walk.
Empty positive-span coverage is skipped. All texture sampling, palette shading,
key comparison and color writes remain on the device. The shared GAF upload
retains raw color indices even for transparent-marked texels; keyed sprite
families still use their independent coverage flag. GPU final model composition
keys index 1 as researched. The CPU target's differing index-1 coverage remains
a documented comparison exception, not a change to classic.

The device raster is also sensitive to where a subject lands in the slot atlas:
moving a subject's slot origin by one pixel, with no other change, moves a few
hundred silhouette-edge pixels across a full battle frame (measured: 452 of
2,073,600 on the seeded benchmark capture, one to three pixels per unit). The
shader arithmetic is translation-invariant; the residue is the device's edge
and varying interpolation at absolute positions, and it is part of this
approximation. A capture comparison between two revisions is therefore only
byte-exact when their slot placement is identical; a placement change is
reviewed on the model preview captures and by inspection, not by pixel count.

### 5.2 Enhanced zoom and strategic view

The detailed view is designed in §14: a view scale in half steps (1×, 1.5×,
2×), 2× terrain and feature art synthesized from the map's own pixels at load
time and resampled to 1.5× by nearest sampling, and model geometry rasterized
at output scale, with every world-space layer, picking, fog and the minimap
sharing the one transform.

Continuous zoom between the steps, and the strategic view below 1×, are **§16**,
and they are modern-only: the classic executor keeps §14's three steps exactly.
Strategic markers show only player-known information — they take the minimap's
own committed contact records and its own admission gate, so the two cannot
disagree (§16.11). Icon art, aggregation and filtering are still unbuilt; the
marker is one team-coloured square per unit. HUD and cursor scale stay
independent of the view scale in every case.

### 5.3 Enhanced interpolation

Target the display's refresh rate — 120 presented frames/s, an 8.3 ms frame
budget — while retaining the 30 Hz authoritative simulation. Rendering more
often without interpolation repeats committed poses. Enhanced interpolation
uses two immutable committed snapshots; it never writes interpolated values
back or consumes simulation RNG. Object identity across slot reuse, spawn and
death, teleportation, child attachment changes, piece animation, input latency
and pause behaviour are specified in §13.5. Original retains committed-tick
sampling; I6 names Enhanced as the one presentation path allowed to read two
committed ticks.

### 5.4 Lighting, glow and antialiasing (deferred)

Lighting may be palette/tint based or use model geometry/material information;
no technique is selected. Preserve resolved asset and geometry identity rather
than inventing material values. Glow's initial intended sources are known lights,
lasers and missile exhaust. A later design must define masks, visibility,
occlusion, blur and color behavior. Enhanced MSAA/SSAA likewise needs its own
resource/performance and compositing design. None blocks the model prototype.

## 6. Verification

Three independent gates replace the old single tolerance gate:

1. **Classic regression:** compare current classic against a baseline from the
   same simulation/content revision. Renderer-only work must preserve classic
   bytes and equal-tick simulation fingerprints. Attribute upstream baseline
   changes before refreshing; never hide them in a tolerance.
2. **GPU comparison:** record once and replay the same ordered list through classic and
   modern, save both images and a diff, report changed pixels/clusters, and fail
   on execution/capture errors. Test the tool rejects a deliberately wrong image.
   Exact non-model fixtures remain exact; GPU model approximations use explicit
   per-scene thresholds only after visual review. No rule that merely connects a
   difference cluster to an edge, and no threshold derived as automatic approval.
3. **Performance:** repeated device-backed replay of one frozen list after
   warm-up. Do not re-record that list in the measured loop: recording can
   consume private presentation RNG and drain audio. Report one-time preparation
   separately, distinguishing modern geometry recording from an explicitly
   requested classic comparison. Screenshot readback and PNG encoding are
   excluded from all rendering/cadence metrics. Submit repeated frames to the
   screen without readback; capture the final PNG only after the measured
   sequence. Report CPU submission duration and observed Draw-to-Draw cadence
   (median/p95/p99), with pacing settings, hardware/backend, resolution, frames
   and scene. Cadence includes device backpressure, presentation and host
   scheduling; submission alone is not GPU time, and neither is an isolated
   GPU timestamp. A frozen-list benchmark excludes simulation and recurring
   preparation, so it cannot establish a universal gameplay frame-rate claim.
   Existing headless `--profile-seconds` measures classic composition only;
   it must not be presented as modern gameplay performance.

The existing matrix is useful history, not exhaustive coverage: its script does
not vary DitheredFog and Ring Atoll does not ensure digger or submerged-hull
pixels. Add authored focused fixtures plus model-rich captures: flat/textured,
shaded/unshaded, equal keys, painter order, cached/live structure parts, children,
waterline/digger, reveal/outline, shadows and overlapping effects. The activated, open ARMSOLAR is a mandatory human-review scene: its
light-colored base must be visible around and between the intersecting panels.
That ownership relationship is a hard correctness gate, not an allowed raster
approximation. This scene must exercise GPU model rasterization and report that
path was used; a CPU-fallback-only capture cannot pass the solar gate. Capture closed and activated/open poses, a close crop and an
ordinary gameplay-scale view, using a reproducible authored pose or session setup.
The solar/lab ownership experiment [03 R-REN-03A §3] isolates this composition
behavior but proves no texture interpolation contract. Inspect sequential frames, resize, clipping and cache
reuse. Backend coverage is reported honestly; one host is not cross-platform proof.

Capture recipes, command settings, revision metadata and policies are versioned;
retail images/assets stay local and uncommitted. Preserve the classic reference
independently; §9 prohibits substituting its output for missing GPU stages.
Unsupported prototype cases must be listed in the handoff, not described as full
GPU parity.

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

## 8. Historical prototype work sequence and public API contract

This section records the earlier P1–P3 staging design. Its model-image adapters,
CPU fallbacks, and native-only structure restriction have been removed. Use
§9–§10 for the current API and behavior; do not reintroduce those adapters.

User-approved milestone: reviewed GPU raster prototypes behind `--renderer=modern`
and a local human-review capture bundle. No new public renderer modes, zoom,
lighting, glow, interpolation, or runtime switch in this milestone. Luna/Terra
implementation units run one at a time in separate worktrees; Sol reviews each;
the orchestrator independently verifies and merges reviewed commits.

| Unit | Scope | Gate |
|---|---|---|
| P0 | Design and invariant alignment | Sol review; existing checks |
| P1 | Reproducible classic/modern comparison and device timing tooling | Actual GPU captures, injected mismatch rejected, exact Phase 2 scenes |
| P2 | Additive immutable model geometry packet; retain classic execution | Classic byte parity; packet lifetime and face ordering fixtures |
| P3 | Conventional GPU model raster prototype, explicit fallback | Real models and focused fixtures rendered; diffs and timing; Sol review |
| Human gate | Review paired images and moving scenes | User decides whether approximations are unobtrusive |

P2 owns the additive `internal/drawlist` model API and client producer. GPU code
must consume that published API without modifying the producer. The packet is
plain Go: subject-local projected ordered polygon vertices with integer X/Y,
key, texture U/V and shade row; resolved immutable texture frame, physical flat
color, shade selector; target dimensions, origin/anchor, key-plane flag and
supersample scale. Preserve polygon order and arity; triangulation is GPU-owned.
Retain per-corner pre-shear information if needed to reproduce structure scaling.
Ordinary geometry packets borrow distinct per-frame vertex and face storage,
valid until the next recording pass. `List.Clone` deep-copies that storage for
consumers that retain a frame.
No client pointer, live camera, or simulation pool is permitted in it. Give the
CPU bridge neutral pixel-plane data rather than making drawlist import client.

**Published model packet ownership.** `drawlist.Model` holds
`Classic *drawlist.ClassicModel` and `Geometry *drawlist.ModelGeometry`.
`ClassicModel` owns the mutable completed body/staging and pre-punched shadow
pixel operands: each plane's colour bytes, coverage bits, optional key bytes,
transparent index, dimensions, origin and framebuffer anchor. Recording builds
the shadow before it releases the composer, so replay retains no live draw,
camera, client model cache, or frame-local lookup. Its optional trace image and
observer are strictly diagnostic and cannot write pixels. `List.Clone`
deep-copies all classic plane slices and modern geometry. `ModelGeometry` carries
ordered `[]ModelFace` and `[]ModelVertex` slices, borrowed for the recording
frame or independently owned by a clone; a vertex holds projected
`X`, `Y`, signed pre-interpolation `Key`, integer texel `U`/`V`, and physical
`Shade` row. A face holds its immutable resolved `*formats.GAFFrame` (or nil),
physical flat `Color`, and `Shaded` selector. The packet carries `Width`,
`Height`, `OriginX/Y`, `AnchorX/Y`, `Scale`, and `KeyPlane`. A GPU consumer
interpolates `Key` before narrowing for its subject-local maximum-key pass; it
must never narrow vertex keys early. `Geometry.Eligible` selects the P2 body
subset. An ineligible packet carries `ModelFallbackReason` (`RevealOrOutline`,
`Supersample`, `WaterlineOrDigger`, `Staging`, or `NoBodyCommit`) so P3 can
report an explicit CPU fallback. Shadows remain CPU fallback in P2. The packet
is emitted only by `RecordFrame`, `ComposeFrameSnapshot`, and
`ModelPreviewRenderer.RecordModel`; ordinary classic `Frame` does not allocate
it. The preview record supplies the classic reference image, model-only list,
physical background index, and palette for a neutral replay plane. Its
`PiecePoses []frame.PieceView` is a static supplied pose, not COB playback;
`ARMSOLAROpenPreviewPose` names stock `dish1`–`dish4` and applies the researched
135-degree Z pose. `ModelPreviewOptions.DisableAntiAlias` suppresses only the
structure's 2x resolve for one preview call and restores the renderer setting
afterward; it leaves the structure/shaded path active. The anti-aliased
structure path remains supersample fallback; a structure preview with this
explicit control is the scale-one solar geometry review subset.

The packet keeps every projected primitive ring that the CPU collector hands to
its span walker. P3 must apply the same winding admission before triangle
rasterization: a ring whose CPU two-chain walk has no positive span contributes
no pixels. It must not reinterpret retained back-facing rings as visible
triangles.

The first packet may describe a safely bounded subset (ordinary completed model
bodies) and mark other subjects ineligible. Explicit eligibility must exclude
any unrepresented child/staging, reveal/outline, live-piece or supersample stage;
those continue through existing CPU composition under modern. P3 must prove it
actually takes the GPU path for review scenes and report GPU/fallback counts.
Classic uses its existing raster arithmetic unchanged. A full all-subject
recording/execution split follows the prototype decision; do not claim the bridge
or its recording-time CPU work has been eliminated before that is measured.

P1 adds a separate `tools/gpu-compare` runner rather than changing the meaning of
historical `tools/gpu-parity`. It uses `--renderer=modern --shot-renderer=both`
and the existing `--shot-renderer-max` acceptance argument. Device timing stays
opt-in to capture/profiling and must not alter normal simulation or presentation.
P3's visual difference report mode is not acceptance; human-approved thresholds
are recorded after review. Build/vet/test and classic regression apply to every
unit. Graphics device recovery and backend replacement are deferred.

**P3b status.** The core raster path now preserves fractional folded-row
attributes through device preparation, validates folded materials, and treats
empty folded rings as correctly culled input. `--shot-renderer=modern` and
`--shot-renderer=both` require `--renderer=modern`; the classic default remains
unchanged. Isolated model preview resolves the selected unit's ObjectName,
BMCode structure class, and ZBuffer from the compiled catalog, rejecting an
arbitrary unclassified 3DO. The open ARMSOLAR command route remains a named
synthetic PieceView pose; `--shot-model-pose=activated` separately loads the
compiled ARMSOLAR UnitDef, binds its stock COB through the production unit
port path, settles Create, raises the activation edge, and snapshots the VM
piece lanes. No dish angle is hardcoded. The preview fails clearly if an
unsupported model would require a CPU fallback without a ModelSource. The
opt-in authored device fixture is `NANOLATHE_GPU_DEVICE_TEST=1 go test
./internal/platform/gpurender -run '^TestModelDeviceFixtures$'`; ordinary tests
skip it; its `TestMain` keeps the optional Ebiten loop on the process main
goroutine for macOS. The fixture uses separate spatial regions for equal-key,
transparent texture, wrapped-key interpolation, keyless painter, folded-span,
transparent keyed Sprite, and explicit CPU-fallback/missing-source checks. A
repeatable paired capture matrix is provided by
`tools/gpu-model-preview /private/tmp/nanolathe-gpu-review/p3-final-model`.
Each row writes `<id>.png`, `<id>.modern.png`, `<id>.diff.png`, and `<id>.log`;
the logs include GPU and CPU-fallback counters and separate closed,
synthetic-open, and production-activated ARMSOLAR rows. Device execution and
human review of synthetic and actual activated captures remain acceptance
gates.

The isolated model matrix uses 320×240 surfaces so the activated solar panels
fit at 2x. `--shot-gpu-profile-frames` profiles frozen battle captures only;
model preview captures reject it explicitly instead of silently ignoring it.

## 9. GPU-only modern execution (current user-approved contract)

The user requires `--renderer=modern` to exercise the GPU implementation even
when coverage is incomplete. CPU-rendered model bodies and shadows must never
be substituted into the modern frame. Classic remains unchanged and separate.
This instruction replaces the earlier fallback-first prototype staging policy.

- `Client.RecordFrame` records geometry and the existing non-model draw
  commands without allocating/rasterizing software model color, coverage or
  height planes. CPU transforms, visibility/order decisions, texture decoding,
  and GPU vertex preparation remain legitimate preparation work.
- Structures follow the existing Anti-Alias option through a GPU doubled
  projection and palette resolve (§10). Its scratch surfaces are bounded by
  model dimensions. This preserves the classic stage; it does not enable a
  new Enhanced MSAA/SSAA policy.
- Model shadows, nanoframe reveal/outline, waterline/digger processing, and
  carrier/child composition now have GPU passes (§10). Unsupported geometry or
  missing material resources remain explicit skips. Resolved subjects retain
  selection chrome, and both recording routes preserve per-primitive texture
  cursor registration. A missing carrier still presents valid children.
- Remove the model-image source adapter from the live platform and modern
  screenshot paths. The GPU executor consumes geometry; missing geometry or
  resources records a skip rather than consulting a CPU renderer.
- When `--shot-renderer` is omitted, screenshots follow `--renderer`, so
  `--renderer=modern --shot=frame.png` exercises the GPU. Explicit `classic`
  and `both` remain diagnostic choices. Reject the CPU-only `--profile-seconds`
  path with `--renderer=modern`; use the GPU capture profiler instead.
- An explicit `--shot-renderer=both` comparison may compose the classic
  reference separately as diagnostic work. It does not supply pixels to the
  modern executor. The modern geometry carries the same structure-resolve option as the classic
  reference. Preserve a single
  presentation walk where possible so presentation RNG/audio side effects are
  not repeated. Ordinary modern-only captures use geometry recording only.
- Texture alignment is a correctness gate before this switch: the activated
  and synthetic-open ARMSOLAR panel's blue/black boundary must not develop the
  visibly bent/zig-zag mapping reported at 2x. A mapping correction is driven by
  the original polygon and authored texture coordinates, never a unit-specific
  UV adjustment. Keep a focused authored fixture and real-device comparisons.

Verification adds an AA-enabled structure recording check with no CPU pixel
planes, GPU skip diagnostics for unsupported stages, and real-GPU solar captures
with the classic option enabled. The performance policy is §6: no screenshot
readback or PNG encode time is counted toward presentation performance.

### Current recording API

`Client.RecordFrame()` selects geometry-only model preparation for live modern
presentation. Its returned list remains same-frame data; callers retaining a
frozen diagnostic list call `Clone()`. `ComposeFrameSnapshot()` additionally
composes the classic reference and includes GPU geometry and stage metadata; its
classic image is never a GPU input. `ModelPreviewRenderer.RecordGeometry()`
returns a durable list, palette and background with a nil `Image`, while
`RecordModel()` retains the classic preview image for explicit comparisons.
The model-only `both` recipe disables the classic structure resolve to isolate
native-scale texture mapping and says so in its log. Modern-only previews do
not require disabling the classic Anti-Alias option.

`gpurender.Model` consumes `Model.Geometry` and never reads `Model.Classic`;
that packet belongs only to the classic sink. The former `ModelSource`,
`ModelImage` and client/platform image adapters have been removed. `ModelStats`
reports GPU bodies, GPU shadows, composed groups, skips and geometry/material
failures. Legacy omission counters remain available for explicitly unsupported
packets; trace-only records are not counted as missing bodies. These are
diagnostic counts, not simulation data.

## 10. Approximate visual parity before enhanced features

GPU model shadows, cached/live composition, reveal/outline, waterline/digger
processing, attached-unit staging, and structure resolve are implemented. The capture audit below checks
these stages alongside the existing terrain, feature, weapon, effect, fog and
interface command families. All work stays behind `--renderer=modern`. Enhanced
lighting, glow, camera zoom and interpolation wait for human visual acceptance.
The passes consume immutable geometry and palette tables; none uploads a
software-rendered model image or reads GPU pixels during play.

### Model shadows

A body packet can own a separate shadow geometry packet. The recorder reuses
`collectShadowPolys` and `shadowAnchor`, preserving the current classic
projection, ordering and placement without allocating software image planes.
The GPU rasterizes the silhouette, retains it while rasterizing the body, then
punches the body's coverage out and blends each remaining shadow pixel once
through `ALP[src*256+dst]`. The destination is snapshotted before that commit;
multiple faces of the same silhouette must not repeatedly darken the ground.
The body commits afterward. Clones own both packets independently.

Modern shadows still use structure rerasterization for mobile and Digger
subjects. Classic now copies the finished body silhouette for those subjects,
clears transparent color-key coverage, flattens to index 0 and applies the
inclusive underwater/buried cutoff. The modern approximation remains explicit;
this does not claim identical shadows between executors. The
producer now applies the corrected master/vehicle/Digger gates, independently
of the structure-body `Shading` preference [03 R-REN-03D §1, §4].
Actual GPU shadows and omitted shadows are reported separately. The device
fixture verifies the blend, body punch and overlapping-face behavior; paired
battle captures verify placement against the classic output.

### Feature sprite raster selection

Feature commands retain their already-offset destination and shadow-before-body
order. `FeatureShadows` is a separate client preference populated by display
settings. `Sprite.Trans` carries the selected raster route: definition flags
apply to static/rest-cursor sprites, while a published live event selects opaque
for both commands [03 R-RAST-01 §6][03 §5.3.1]. Both executors use the existing
keyed/tinted primitives for these selections, including the startup palette
capability; there is no separate feature-shadow shader or shade-row policy.
The publisher carries independent `RuntimeLive` and `ShadowEnabled` state.
A live record without a drawable event cursor does not select static rest art.
The retained-slot arena limitation remains documented in the economy design
under EC-G2; it does not require another presentation fallback.

### Reveal, outline and submerged geometry

A geometry packet carries the producer's resolved nanoframe bands, complete
outline rings, and waterline/sonar/owner decision. The body fragment applies the
reveal after material lookup, writing the composition background when erased
while retaining key ownership. Outline geometry consists of the two row
endpoints from each ring, with the same subject key test; no polygon-border
line primitive replaces that pass. The GPU applies BLUE TABLE or erasure at the
inclusive waterline threshold, then the Digger erase. Keyless subjects skip
both clipping passes. The final body coverage punches its shadow, as in classic.
These passes replace the whole-subject omissions from §9. No software image
planes are allocated during recording. [03 R-COMP-01 §3][03 R-WATER-01 §2]

### Attached-unit composition

A keyed carrier owns an ordered list of child geometry and signed height deltas.
Each child keeps its own reveal, outline, waterline and Digger processing. Its
shadow-only command remains before the carrier's shadow/body command, preserving
classic order without uploading a CPU image. The GPU packs the carrier's color
and key planes into red/green channels, including keys under erased pixels,
then merges each finished child using a separate destination image. The signed
shifted key is compared before narrowing to the stored byte. The next child
sees that narrowed key. The carrier's live lane enters before those merges;
after the final child, waterline and Digger process the combined plane once,
then the group commits once. [03 R-REN-03A §4]

A keyless carrier records independent body commands in painter order; a missing
carrier still records each valid child independently. Both diagnostic and
geometry-only recording preserve this behavior and texture cursor registration.
Nested child groups are not emitted by the current presentation walk. A device
fixture covers carrier/child occlusion, signed compare before wrapped store,
later-child ordering, invisible key ownership, and child shadow-only commits.

### Structure resolve

The recorder carries an optional doubled body projection using the same
`placeFaces` operation as classic, including the odd-height shear correction.
Its coordinates target a model-sized local scratch image. The GPU renders that
body and its reveal, then resolves each 2×2 block with the three ordered ALP
lookups; the resolved key is the top-left sample. Transparent background
participates in the filter. The native outline and clipping passes follow the
resolve, and a child's resolved image is what enters carrier composition.
No CPU pixels are involved. [03 R-REN-03A §6–§7]

Only cached faces enter this GPU path. Native live faces follow its resolve at
1x and are unshaded, so the structure filter never includes a live piece.
Model-only `both` captures can continue using their explicitly logged
native-scale comparison recipe; the scene matrix and AA-enabled preview API
cover the normal structure option.

### Weapon, effect and asset coverage review

Weapon and effect producers already emit palette-index draw commands through
both executors. Their modern path uses GPU lines, points, sprites, destination
palette operations, and model geometry. A separate per-weapon GPU implementation
would duplicate the presentation rules and is unnecessary. The device capture
audit supplies authored committed views using installed definition properties,
composes the classic reference, then independently calls `RecordFrame` for the
modern list. It resets the presentation CRT copy for reproducible lightning.
Every case must change visible pixels from an empty terrain frame; model cases
must report GPU body execution and no skips. Screenshot reads happen only after
execution, outside timing.

The audit covers all installed projectile model identities, the published laser,
lightning, flame and plasma paths, all three calculated-flash tables, named
explosion/smoke/flame art, every published strip family, and overlapping smoke
and impacts. The broader audit adds every installed unit definition at two headings, every
unique feature model, and representative feature sprite banks/transparency
modes: 1,000 non-empty cases (556 unit, 404 feature, 26 weapon, 14 effect/strip),
plus an empty-frame control. All cases draw visible pixels without skipped
models after the polygon fix below. Non-model cases match classic exactly on
the tested Metal device; model cases retain edge/shade raster differences.
These static unit poses expose every piece and do not claim to be COB states.
Render type 2's fixed global sprite binding remains unresolved in classic as
well; this milestone does not invent that resource. The capture source and
paired images are retained in the human-review artifact, not as retail fixtures
in the repository.

This is approximate parity with the current classic implementation, not proof
of complete retail behavior or every animated pose. The shared classic gaps
above, the covered-index-1 discrepancy at model commit, and producerless world
passes remain explicit. Live motion, camera movement, team textures, activation,
construction, cargo, waterline and combat remain human review targets. The
existing 30 Hz presentation sampling is unchanged; the 16.7 ms budget remains a
performance acceptance target, not a result established by static captures.

A catalog-wide capture audit exposed three disappearing subjects when a flat
projected ring could not be triangulated. Such a ring now uses the same
positive-row strip preparation as folded geometry, retaining device key and
color passes. One bad ear no longer drops the whole unit. An authored touching
ring checks both positive lobes and its empty pinch on the device; this is a
geometry path, not a CPU image fallback. [03 R-RAST-01 §1]

## 11. Compiled execution (current performance contract)

Implementation policy, not retail behavior. This section replaces the earlier
per-body image cache and page packer (retained only in git history) and the
per-command execution the first modern executor used. Classic is untouched.

### 11.1 Why the first executor was slow

Measured on the seeded Ashap Plateau battle benchmark at 1920×1080 (darwin/arm64,
Metal), the modern executor issued about 2,700 device draws and 59 image clears
per frame: one draw per fog cell (1,177), two per translucent sprite (snapshot
then tint), three per model commit, four to five scratch passes per model cache
miss, and one per solid fill. Ebitengine's Metal driver opens a new render pass
whenever the destination image changes, with a load and store of the whole
target, so the snapshot ping-pong and per-model scratch work produced more than a
thousand full-screen passes per frame. The body image cache missed on every
animated mobile unit because pose is part of identity (143 misses and 146
evictions per frame against a full 128 MiB budget). Textured quads were cut into
one-pixel strips on the CPU (about 21,700 per frame), and per-draw uniform maps
plus per-strip slices allocated 11 MB and 213k objects per frame. The result was
12 ms of CPU submission, a further 12 ms on the render thread, and a 65 ms
cadence against classic's 15 ms of CPU and 33 ms cadence.

### 11.2 Design

The recorded `drawlist.List` is unchanged and remains the contract with the
recorder and the classic sink. The modern executor still implements
`drawlist.Sink`, but its Sink methods **compile** the command into a small number
of batched passes; `Execute` submits those passes after `Replay` returns. Order
is preserved exactly where pixels depend on each other and relaxed everywhere
else (C-G3). Byte semantics of every family are unchanged (C-G2, C-G4, C-G7,
C-G8).

**One table atlas.** `PAL`, `Gray`, `Blue` (256×1 each), `SHD` and `LHT`
(256×32 each) and `ALP` (256×256) are packed into one RGBA8 image with fixed row
offsets, index in red. Every shader receives it as one source image, which frees
Ebitengine's four image slots and lets unrelated families share a shader.

**One scene shader for the 2D families.** Terrain tiles, keyed GAF sprites,
feature sprites, PCX, glyphs, fills, lines, points, indexed surfaces, the cursor
and model body commits are all "opaque" writes: they read no destination. They
draw with one Kage shader whose per-vertex custom attributes select the source
(constant index, GAF atlas texel keyed on the green flag, table row remap for
`BlitLit`, scaled integer mapping for `BlitScaled`/`Surface`). The
destination-reading families (`BlitTinted`, translucent feature body and shadow,
`FillLitRect`, `FillShadeRect`, `PointLit`, the model shadow commit) draw with
one destination shader whose custom attributes select the table (ALP, LHT or
SHD) and row, sampling a snapshot image. No uniforms are used on any per-frame
draw; every parameter rides the vertex. Sources are: the scene GAF atlas (frames
packed on first use into 2048-square pages, keyed by frame identity), the
snapshot, the table atlas and the model slot atlas.

**The scheduler.** `Execute` keeps a coarse screen grid (32 px cells). Each
compiled command has a clipped screen rectangle and a class, opaque or
destination-reading. Commands are appended to *phases* in record order. A phase
is executed as: (1) one draw of its opaque batch, (2) one copy of the offscreen
into the snapshot, (3) one draw of its destination-reading batch. The rules:

* an opaque command joins the current phase unless it overlaps a
  destination-reading command already in the current phase; then it
  opens the next phase (it must overwrite that result, so it must draw after it);
* a destination-reading command joins the current phase unless it overlaps
  another destination-reading command already in the
  current phase; then it opens the next phase (the later one reads the earlier
  one's result);
* a cell tag names the destination-reading commands that covered it — four,
  after which the cell answers every later test conservatively — and the test
  then compares the two rectangles, so sharing a cell without overlapping does
  not split a phase. Both tests cost one grid lookup per cell of the rectangle
  and allocate nothing after warm-up.

One relaxation of the rectangle test is exact, not heuristic: a lit point batch
tags the cell of each visible point rather than the cells of its bounding
rectangle, and two points conflict only when they write the same pixel; the
phase keeps the pixel set that decides it. Any other command sharing a cell with
a point still opens the next phase.

A second relaxation was claimed here and is retracted: that a model body commit
may join the phase of its own shadow commit because the shadow writes only
pixels the body plane leaves uncovered [03 R-REN-03D §4–§5] and the body only
pixels it covers. Measured against the battle capture the two pixel sets are not
exactly complementary — a column of the silhouette at the body's edge belongs to
both — and drawing the body first drops the shadow there. The sequential
scheduler never exercised the exemption (a body commit almost always overlapped
some other destination read of its shadow's phase), so removing it changes no
pixel of that executor; critical-path placement exercises it constantly. The
shadow commit is an ordinary destination read and the body commit that follows
it takes the next phase [03 R-REN-03D §4].

This is C-G3's disjoint-run rule generalised across families. Within one batch,
vertex order is record order, and the device rasterizes primitives of one draw in
order, so overlapping opaque writes in the same batch still resolve to the later
command. The snapshot copy in (2) may be limited to the
union rectangle of the phase's destination-reading batch. Batches whose vertex
count would exceed the 16-bit index domain are split into consecutive draws
without reordering.

Overlapping translucent effects, not the 2D families, set the phase count: at
1920×1080 the battle benchmark compiles about eighty phases per frame, of which
about thirty-five are opened by an overlapping ALP-tinted sprite and about
twenty-five by model shadow and body commits that genuinely chain. Sharing one
snapshot between two phases was measured and rejected: only about ten of those
eighty phases have a destination rectangle that even misses the previous
phase's, which is an upper bound on what a snapshot-validity scheme could save.

**Fog as one pass** (C-G7). The recorded fog ops become a per-cell grid texture
(kind, variant, frame and parity in the four channels) covering the visible
cell range, rebuilt each frame from the record. One full-screen draw reads the
pre-fog snapshot, the grid, the fog GAF atlas (all four variants of both
families packed once) and the table atlas, and writes the fog result in place of
the 1,177 per-cell draws. Per-pixel results equal the byte writers' [03 §3.3].
Fog remains its own phase between the world phases before it and the interface
phases after it, because it reads everything drawn so far.

**Models: per-frame slot atlas, no cache.** Every `Model` command with eligible
geometry is allocated a slot in a per-frame atlas image (2048 wide, grown in
height as needed, at most two pages) sized to its composition box; a body with a
supersample packet gets a 2× slot on a separate 2× page and a native slot. The
slot atlas is cleared once. All subjects' key passes are one draw (max blend,
subject-local because each slot is disjoint), all colour passes are one draw
(each fragment compares the interpolated key with the key stored at its own slot
texel), all shadow silhouettes are one draw into shadow slots, and all 2× resolves
are one draw into native slots. Outline, waterline/Digger clipping and reveal keep
their researched semantics inside the colour pass or in one batched follow-up
draw over the affected slots. The scene commit of a body is then a keyed quad in
the opaque batch sampling the slot; the shadow commit is a destination-reading
quad sampling the shadow slot and the body slot (for the coverage punch) through
`ALP`. Attached-unit groups keep the sequential child merge of §10 over a small
staging image, since there are a handful per frame. The image cache, its LRU,
identity keys, page packer and pinning are removed; `ModelStats` drops the cache
fields and keeps the counters that describe work performed.

**Textured quads without strips.** Retail's span mapper finds, for each row,
the active edge on the decreasing-index chain and on the increasing-index chain
of the quad, interpolates every lane along each edge by the row's parameter,
then interpolates across the row by the column's parameter [03 R-RAST-01 §1].
A textured quad therefore draws as two device triangles whose key and colour
passes evaluate exactly that two-chain mapping per fragment from a per-frame
quad parameter image (twelve RGBA8 texels per quad: corner positions, corner
texel coordinates, corner key and shade), then floor before sampling as before.
This is not a generic inverse-bilinear map, which differs from the retail mapping
on a non-parallelogram. It removes the diagonal bend the strip path was
introduced for [§5.1] without CPU scanline work. Crossed (folded) rings, rings
with no ear and non-quad textured faces keep the strip path, about 300 strips
per frame in the battle benchmark. The activated ARMSOLAR captures are the
acceptance gate; a visible diagonal or zig-zag is a defect.

**Allocation policy.** Steady-state frames allocate nothing in the executor:
vertex and index buffers, phase lists, grid tags, prepared face scratch and the
fog grid bytes are reused across frames; no `map[string]any` uniform maps; no
per-strip or per-face slice literals; texture and glyph atlases are built once
per identity.

### 11.3 Public API

Unchanged: `New`, `NewChecked`, `Execute(list, w, h) *ebiten.Image`,
`ModelStats()` (fields may be removed, never given new meaning), and the
`drawlist.Sink` implementation. `Execute` still visits the list in record order
and still returns the expanded image after the `Expand` marker.

### 11.4 Verification

Gates in addition to §6: the battle benchmark (`docs/BATTLE_BENCHMARK.md`) on
both renderers before and after each unit, comparing modern against its own
baseline capture; the instrumented device-call count (draws and clears per
frame) reported in the unit's commit message; the fog, tinted-overlap,
carrier/child and ARMSOLAR device fixtures; and zero steady-state allocations in
the executor measured by the benchmark's allocation delta. The targets for the
complete redesign at 1920×1080 are about thirty device draws per frame, CPU
submission under 2 ms, and a 33 ms cadence at 30 Hz with headroom for 60 Hz
presentation.

Measured after G5 on the seeded Ashap Plateau battle benchmark at 1920×1080:
cadence 33.3 ms — the 30 Hz vsync floor, and classic's own figure — with CPU
submission about 7.4 ms over roughly 230 device calls (about 80 phases, each a
batched opaque draw, a snapshot copy and a batched destination draw, plus the
model slot passes, the fog pass and the expansion). The device-call and
submission targets are therefore not met: the phase count, not the draw count
within a phase, is what stands between the executor and them, and the phases
that remain are real per-pixel dependencies between overlapping translucent
effects. The executor itself allocates about a hundred objects per frame; the
benchmark's 42,000 are Ebitengine's per-Metal-call boxing (about a hundred
objects per device call, a third of them in opening the render pass a
destination change forces) and the recorder in `internal/client`.

### 11.5 Render passes, not draws (second round)

Measured after G5 with `--benchmark-tps=60` (one simulation step per draw, the
enhanced presentation target): classic holds the 16.7 ms floor with 15 ms of
CPU, modern's cadence leaves it (27.5 ms median, 79% of frames on the 30 Hz
floor but few on the 60 Hz one) with only 13 ms of CPU. Modern is device-bound.
Skip experiments in a throwaway worktree attributed the device time: dropping the
snapshot copies alone put modern on the 60 Hz floor; dropping the
destination-reading draws while keeping the copies did not; dropping the fog
pass or the model slot passes changed nothing; copying into a 512-square
snapshot instead of the full-size one was slower. Ebitengine's Metal driver opens
a render command encoder whenever the destination image changes, loading and
storing the whole attachment, and that switch costs on the order of 70 µs
whatever it draws. Of the roughly 195 passes per frame, about 168 are the
offscreen → snapshot → offscreen alternation of the phases. The unit of cost is
therefore the destination switch, and the executor's job is to issue as few as
possible; the draw count within a pass and the pixels a pass touches are
secondary.

Two changes follow, both preserving the §11.2 order rules exactly.

**Critical-path placement.** A command is placed in the earliest phase its
overlaps permit, not the latest open one. With record order the only order, the
phase of a command C is the maximum, over every earlier-recorded command E whose
clipped rectangle overlaps C's, of E's phase plus one when E reads the
destination and E's phase when it does not; a command overlapping nothing goes to
phase zero. This is exactly the dependency the sequential rules express: a
destination read must follow, by a phase, every earlier destination read and
must be preceded, in the same or an earlier phase, by every earlier opaque write
it covers (the opaque batch of a phase draws before its destination batch); an
opaque write must follow, by a phase, every earlier destination read it covers,
and may share a phase with earlier opaque writes because a batch is drawn in
record order. Each phase therefore keeps its own vertex and index storage, and a
command appends to the storage of the phase it was placed in, so a batch is
still record-ordered. The lit point and body/shadow relaxations of §11.2 carry
over unchanged: they only alter which pairs count as overlapping. The cell grid
keeps, per cell, up to four owners with their phases; the rectangle test decides,
and a saturated cell answers with the largest phase it has seen. Barriers (fog,
a composed group's staging, the clear, the expansion) end a *segment*: every
later command is placed after the barrier's phase. Estimated on the battle
benchmark by placing every command of the current scheduler's stream both ways:
31 phases per frame mean and 54 maximum, against 70 and 102 today.

**One pass per phase.** Two full-size indexed surfaces alternate as the
destination, so the separate snapshot surface and its pass disappear. Phase k
writes surface W_k and its destination batch reads the other, R_k, which is
W_{k-1}. Pass k, in this order and all into W_k: (1) copy R_k over the previous
phase's destination rectangle (the only pixels W_k still lacks: that rectangle
in R_k already holds the later opaque writes over it, which is the later state
anyway); (2) the opaque batch of phase k; (3) the destination batch of phase k,
reading R_k; (4) the opaque batch of phase k+1, so that W_k already holds it
when it becomes R_{k+1}. Every opaque batch is thus drawn twice, once per
surface, and every destination batch once. The invariant is that before pass k,
R_k holds the complete state through phase k-1 plus the opaque batch of phase k,
and W_k holds the complete state through phase k-2 plus the opaque batch of
phase k-1. The clear is an opaque full-surface fill in the first phase, so both
surfaces receive it. Fog is a phase of its own whose destination batch is the
one fog draw over the visible region, reading R (complete by the invariant); the
next pass copies that rectangle like any other. The expansion reads the last
written surface. Model slot pages are built before the phases and are not
involved. A destination-reading run's read surface is resolved at submission,
not at compilation. `ModelStats` gains `Phases` and `Passes` (device
destination switches the executor issued) so the benchmark rows record them.

Expected: passes fall from about 195 to about 30 phase passes plus the model
slot passes, the fog pass and the expansion. The doubled opaque fill (terrain,
sprites and body commits, a few million pixels) is cheap on a tiled device and is
accepted.

*Status.* Critical-path placement is landed. The two-surface alternation is
not: implemented as written, it composes a battle frame whose model shadow
commits lose about 500 of 2,073,600 pixels (the pre-shadow value remains), and
the loss survives every variation tried — copying the full surface instead of
the rectangle, either copy blend, no run merging, fog as a barrier, an exact
grid-free placement — while the same placement over the snapshot submission is
byte-identical, as is drawing every opaque batch into both surfaces under that
submission. Why the alternation itself changes those pixels is unexplained;
the executor keeps the snapshot copy per phase (`TODO(H1)` at the submit site)
until it is, and the phase count alone still more than halves the passes.

Measured after H1–H3 on the battle benchmark, 180 frames: phases 42 per frame
median (against 70 before placement; the retracted exemption costs some of the
estimated 31), device destination switches 104 including the model stage's 6
(against about 195), modern allocation 1.9 MB and 24k objects per frame
(against 3.9 MB and 42k), modern Submit 7.3 ms (against 7.8) and Record 4.8
ms. At 60 TPS modern's cadence median is about 20–23 ms with 2–12% of frames on
the 16.7 ms floor (classic: 17.6 ms, 45%); at 30 TPS both are on the floor.
The device time still tracks the pass count, so the remaining lever is the
two-surface alternation above, or fewer phases.

**Model slot passes.** The slot stage today alternates its destinations per
page: two clears, the key faces into the key plane, the colour faces into the
body plane, reveal, the outline keys back into the key plane, the outline
colours into the body plane, the separate live key and colour passes, then
the clip pass, for each of up to three native
pages and the supersample page, about twenty switches. Stacking every page as a
vertical band of one image per plane (body, key, post; 2048 wide) and ordering
the work by destination — every page's clears, then every page's key work, then
colour, then the follow-ups — brings the stage to a fixed handful of passes
regardless of page count. The researched pass semantics (§10: outline colour
compares against the key plane including the outline keys; clipping follows
colour; reveal) decide which stages may merge; where they must stay ordered, they
stay ordered, and the count is reported.

**CPU.** Both renderers now spend the larger part of their main-thread CPU in
the Go allocator rather than in any renderer code: on this platform every fresh
heap span costs a page re-commit and the heap of a loaded battle grows for
seconds between collections, so per-frame allocation is paid at allocation
time, not at collection. The battle benchmark's 3.9 MB and 42,000 objects per
frame (modern) break down as about 37,000 objects in Ebitengine's per-Metal-call
boxing on the render thread (proportional to passes and draws, which the two
changes above cut), Ebitengine's per-destination temporary vertex and index
buffers (which reallocate on every new high-water mark), the executor's own
scratch (cleared each frame instead of merely reset, and grown slot by slot), and
about 2,500 objects in the recorder and HUD (`internal/client` outline geometry
and model composition, effect draw lists, the minimap surfaces rebuilt and
copied every frame). The policy of §11.2 stands: a steady-state frame allocates
nothing in the executor, and the recorder and HUD retain their per-frame
buffers across frames. The recorded list is the contract and must not change:
classic output stays byte-identical.

## 12. Work units for §11

Each unit is one worktree, one sub-agent, exclusive files; the orchestrator
reviews the diff, re-runs the gates and merges.

| Unit | Scope | Files owned | Gate |
|---|---|---|---|
| G1 fog grid | grid texture, fog GAF atlas, one fog pass | `fog.go`, `fog_shaders.go` (new), `fog_test.go` | fog device fixture; M6 dithered and M2 regions equal to classic bytes; fog draws 1,177 → 2 |
| G2 slot atlas | per-frame model slot atlas, batched key/colour/shadow/resolve passes, cache removal | `models.go`, `model_*.go`, `model_shaders.go`, model tests | model device fixtures; ARMSOLAR and carrier captures; model draws per frame independent of unit count |
| G3 scheduler | table atlas, scene and destination shaders, phase scheduler, all 2D families and model commits through it | `renderer.go`, `draw.go`, `sprites.go`, `deststage.go`, `text.go`, `terrain.go`, `shaders.go`, `tables.go`, `batch.go`, `schedule.go` (new), `atlas.go` (new); model commit call sites by API agreed with G2 | tinted-overlap fixture; M1–M8 non-model regions equal to classic; total draws about thirty |
| G4 bilinear quads | inverse-bilinear textured quads, strips only for folded rings | `model_shaders.go`, prepare functions in `models.go` | ARMSOLAR activated/open captures; strip count about 66 |
| G5 render-pass and allocation sweep | phase count, per-frame uploads, zero steady-state allocation in the executor | `internal/platform/gpurender` | M1–M8 modern byte-identical to the previous revision's modern; device fixtures; benchmark phase, pass and allocation counts reported |

G1 and G2 are independent and run first in parallel. G3 follows G2 because the
model commit goes through the scheduler. G4 follows G2. G5 runs last. After each
merge the orchestrator runs the battle benchmark on both renderers and records
draws per frame, submission, cadence and allocations in the merge commit.

### Second round (§11.5)

| Unit | Scope | Files owned | Gate |
|---|---|---|---|
| H0 benchmark | `--benchmark-tps`, on-cadence share in the report | `cmd/nanolathe/flags.go`, `cmd/nanolathe/battle_benchmark.go`, `internal/platform/ebitenapp/battle_benchmark.go`, `tools/battle-bench-report`, `docs/BATTLE_BENCHMARK.md` | landed (`a4fb00cd`) |
| H1 scheduler | critical-path placement (landed), one pass per phase (not landed, see §11.5 status), `Phases`/`Passes` stats, fog and clear as phases, retained uint32 index buffers | `schedule.go`, `deststage.go`, `renderer.go`, `fog.go`, `draw.go`, `sprites.go`, `terrain.go`, `text.go`, `shaders.go`, `atlas.go`, `models.go` (commit and shadow call sites), their tests | M1–M8 modern byte-identical to the previous revision's modern; fog, tinted-overlap and carrier fixtures; phases about 31 and phase passes equal to phases on the benchmark; 60 TPS on-cadence share reported |
| H2 model stage (landed) | stacked pages, passes ordered by destination, vertices emitted once per page for key and colour, scratch arena without per-frame clearing, uint32 indices, recyclable sub-images | `model_slots.go`, `model_prepare.go`, `model_quads.go`, `model_scratch.go`, `model_atlas.go`, `model_shaders.go`, their tests | model device fixtures; ARMSOLAR and carrier captures; model stage passes ≤ 10 and independent of page count; executor allocations reported |
| H3 recorder (landed) | retained buffers in the recorder and HUD: outline geometry, composition scratch, effect draws, minimap surfaces | `internal/client/model_geometry.go`, `internal/client/model_scratch.go`, `internal/model/model.go` (composition scratch only), `internal/render/effect_view.go`, `internal/render/minimap*.go`, `cmd/nanolathe/battle_hud_minimap.go`, their tests | M1–M8 classic byte-identical; classic and modern battle captures identical to the previous revision's; recorder objects per frame reported before and after |

H1, H2 and H3 are independent and run in parallel; H1 owns `models.go` and H2
must report, not make, any change it needs there. After each merge the
orchestrator runs the battle benchmark on both renderers at 30 and 60 TPS and
records phases, passes, submission, cadence, on-cadence share and allocations in
the merge commit.

## 13. True-colour composite and refresh-rate presentation (third round)

These are implementation decisions approved by the user on 2026-09-08, not
retail findings. The goal of this round is 120 presented frames per second
with the 30 Hz simulation interpolated, staying visually close to Original
without requiring index-exact pixels.

### 13.1 Why the index-exact executor cannot get there

Measured on main `eb7a6df1` (1080p battle benchmark, 180 frames, M3 Pro):

| | per presented frame |
|---|---|
| Budget at 120 Hz | 8.3 ms |
| Modern Submit (CPU) at 30 TPS | 6.4 ms |
| Destination switches (render passes) | 104, about 70 µs each on the device |
| Ebitengine with 20 draws of 2,000 quads, vsync on, TPS 30 | 120.2 fps, 1.8 ms CPU per Draw |
| Ebitengine with 60 such draws | 121 fps, 2.9 ms |
| Ebitengine with 150 such draws | 100 fps, 6.1 ms |

Ebitengine calls Draw at the display's refresh rate with vsync on and Update at
TPS, which is exactly the interpolation model; it holds 120 Hz on this display
up to roughly sixty device draws per frame. The passes are the problem, and
every pass exists for one reason: the framebuffer holds palette indices, the
destination-reading families remap the destination index through a table, and
a Kage shader cannot read its render target. Each destination read therefore
costs a snapshot copy and a pass. Optimising the pass structure further (§11.5)
bottoms out near fifty passes, which is still 3.5 ms of device time before a
pixel is drawn. The composite has to stop reading the destination.

### 13.2 The tables are their formulas

The destination-side tables are not authored art. Research establishes their
builders [03 §4.3.4], and measured against the shipped retail tables every one
of them is its builder's arithmetic followed by the nearest-palette search:

| table | generating arithmetic (RGB, per channel) | mean RGB distance, table vs formula | nearest-palette floor |
|---|---|---|---|
| ALP[src][dst] | floor((src + dst) / 2) | 19.2 | 19.1 |
| LHT row r | trunc(c · (1 + r/30)), clamp 255 | 0.0 (r=0) … 16.1 (r=30) | 0.0 … 15.7 |
| SHD row r | trunc(c · 0.06875·r), clamp 255 | 0.0 (r=0), 4.1 (r=15), 16.7 (r=31) | 0.0, 4.1, 16.1 |
| GRAY | avg = floor((r+g+b)/3) | 5.6 | 5.6 |
| BLUE | (r/2, g/2, b/2 + 50) | 17.8 | 17.6 |

(The floor is the mean distance from the formula's colour to the nearest
palette entry; where the two columns agree the table adds nothing beyond
quantization.) So an executor that evaluates the arithmetic in RGB reproduces
the retail composite up to palette rounding, and the rounding is the only
thing it loses. That is the whole of the visual change in this round.

### 13.3 The Enhanced composite

The recorded `drawlist.List` is unchanged; the classic sink and the recorder
are untouched. Sources stay indexed: GAF frames, tiles, glyph strips, PCX and
the model slot pages carry the index in red exactly as before (C-G4 applies to
every source and to the model stage). What changes is the framebuffer and the
destination-reading families.

**Framebuffer.** One RGBA8 composite surface holding colour. Every opaque
family's fragment resolves its index through the PAL row of the table atlas
before writing (C-G8 as amended). The expansion pass disappears; `Expand`
remains a barrier and the composite is what `Execute` returns. The clear
writes PAL[0].

**Source-side lookups stay exact.** `BlitLit` (source through one LHT row) and
the model stage's SHD, ALP resolve, Gray-free paths and BLUE waterline all
remap a texel before it is written; they keep their integer texel fetches and
resolve through PAL at the end. The model slot atlas, key plane, reveal,
outline, waterline, digger and supersample resolve are unchanged; only the
commit resolves colour.

**Destination-side lookups become blends** with the arithmetic of §13.2:

* *ALP families* — `BlitTinted`, translucent feature body and shadow, and the
  model shadow commit — write `mix(dst, PAL[src], 1/2)`: source-over with the
  premultiplied fragment `(PAL[src]/2, 1/2)`. The shadow commit keeps its body
  punch in the shader (a shadow texel under the body's own coverage is skipped)
  so overlapping silhouette faces of one subject darken once; overlapping
  shadows of different subjects darken twice, as the byte writers do
  [03 R-REN-03D §4–§5].
* *Row families* — `FillLitRect`, `FillShadeRect`, `PointLit` — scale the
  destination: LHT row r by `1 + r/30`, SHD row r by `0.06875·r`. One blend
  serves both: source factor destination-colour, destination factor
  source-alpha, so `out = dst · (src.rgb + src.a)`; the fragment carries
  `(min(k,1), min(k,1), min(k,1), max(k−1, 0))`. A factor above 2 clamps at 2
  (SHD row 31 is 2.13; the difference is below the quantization floor).
* *Fog* keeps one read copy. The gray remap is a desaturation, which no
  fixed-function blend expresses, so the fog command stays a shader run over a
  copy of its region: luminance `floor((r+g+b)/3)` for the gray fills and gray
  GAF, PAL[dark] for the solid and checker fills, keyed copies for the black
  family. That is one copy pass per frame, not one per phase.

**The scheduler keeps its placement and loses its snapshots.** Critical-path
placement (§11.5) still decides order among overlapping commands — the blends
are order-dependent exactly where the table lookups were. A phase now submits
its opaque runs and then its destination runs into the same surface, with no
copy between them; a run is keyed by images, shader and blend, and within one
phase's destination batch the runs may be grouped by blend because the batch's
rectangles are pairwise disjoint. A whole segment is therefore one render pass.
Passes per 1080p battle frame as landed: model stage 6, fog copy 2, composite
1, attached-unit staging 2 — eleven on every benchmark frame, against 104.
The staging figure needed one change beyond this section's first draft:
composing each attached-unit group on the shared staging pair cost three
passes per group (9 + 3 × groups, 21–33 on the benchmark with four to eight
factories building), so R1 also placed every group of the frame on one
staging atlas ordered by destination, with one pass for every group's
background and parent and one pass per child index across all groups. The
merge order inside a group is unchanged and the captures are byte-identical
with and without it.

**Blend classes.** `schedOpaque` draws with source-over and alpha 1 or 0 as
today. `schedDest` splits into source-over (ALP families) and scale (row
families); the fog run binds its own shader and read slot as it does now.

### 13.4 Verification

* Device fixtures: for every fixture whose commands are all opaque, the modern
  surface equals the classic bytes expanded through PAL exactly. For fixtures
  with blended commands, pixels no blended command covers are exact, and the
  covered pixels' mean RGB distance from the classic expansion is at or below
  the §13.2 floor for that family plus 5 units; the fixture states which
  family's floor it uses.
* Captures M1–M8 through `tools/gpu-compare --report-only`, viewed by the
  orchestrator beside classic; `--shot-renderer both` pixel counts are
  reported, not gated, because every blended pixel now differs by design.
* Battle benchmark at 30 and 60 TPS: passes per frame ≤ 12, phases unchanged,
  Submit and on-cadence share reported in the merge commit.
* Ebitengine allowlist and `docs/INVARIANTS.md` checks unchanged.

### 13.5 Refresh-rate presentation and interpolation

The composite of §13.3 draws one recorded list in a few passes, so the modern
window can record and replay a list every presented frame. Interpolation then
needs no new draw-list family: the recorder is handed a blended view of the
two most recent committed ticks and records it exactly as it records a
committed tick today. Everything below is an Enhanced presentation rule, not
retail behaviour; Original keeps committed-tick sampling.

**Cadence.** The window's Update stays at 30 per second. Everything the
battle step does per host frame — input edges, the follow-camera glide's
per-frame step [07 R-CAM-01 §12], the scroll pass [07 §10], the sub-tick
budget [01 §4.2] — keeps the cadence retail gives it, unchanged. Draw is
called at the display's refresh rate regardless of TPS; Original presents
only after an Update, as today, and Enhanced presents on every Draw, so the
presentation-only work between two Updates is one recording and one replay
per frame. `--fps N` caps how often Enhanced presents: a Draw that arrives
sooner than the cap's interval (less an eighth of it, the vsync jitter
allowance) returns without recording and the retained screen keeps the last
frame. Draw still sits on the display's vsync grid, so the cap lands on the
nearest refresh multiple below it — 60 on a 120 Hz display presents every
second refresh — which is what makes a 120 Hz display a stand-in for a 60 Hz
one. Original ignores the cap; it presents once per Update.

**Fraction.** Read at Draw time, when the modern path records. The
scheduler's time source is the scaled timebase floor(milliseconds × 30 /
1000) [01 §4.1], so its delta is a whole number of thirtieths and, at the
nominal speed, the budget's carry is identically zero after every step: the
carry alone never resolves a position inside a tick. The client therefore
takes a `TickFraction func() float32` option beside `Step`, and the battle
supplies it as the time since the most recent tick actually fired: the
controller notes the host millisecond, the carry and the global tick after
every session step, stamping them only when the global tick moved, and the
fraction is `carry at the fire + elapsed seconds × 30 × eff` with `eff` the
clock's effective speed (active × 0.1 [01 §4.2]), clamped to [0, 1]. While
paused the budget does not run and the battle returns the value it last
returned unpaused, so the blend is frozen. The benchmark at 120 sets the
four fractions explicitly.

The first form of this rule read the wall clock's own phase, `(milliseconds
× 30 mod 1000) / 1000`, as the elapsed part of the scaled unit. That is
right for the budget but wrong for the blend: ticks are released only inside
the window's 30 Hz Update, whose timing drifts against that phase, so an
Update landing just before the phase wrapped released no tick while the
phase reset to zero, and every blended pose slid back toward the previous
tick for a whole Update before jumping two ticks forward. Slowly moving
units hid it; COB pieces animating at speed showed it as a jiggle. Measured
from the fire, the fraction cannot move backwards within one tick: an
Update that releases nothing saturates it at one and holds the pose.

**Camera.** The camera moves in the 30 Hz step, so Enhanced blends it too:
the client samples the camera origin (X, Z) at every Step, keeps the previous
sample, and while recording an interpolated frame presents the origin blended
with integer truncation, restoring the live origin after the record. The
camera's fraction is not the tick fraction: the camera advances on the
window's Update grid, which is not phase-aligned with the simulation's scaled
units, so blending it by the tick fraction would snap it back whenever a tick
fired mid-update. The window adapter timestamps each Update and hands the
client `(now − lastUpdate) × 30`, clamped to [0, 1), before each modern Draw
(`SetCameraFraction`); this is platform time in the adapter, where the input
timestamps already live, and never reaches the client's clock or the sim. A
jump larger than the viewport in either axis (a minimap click or a bookmark
recall) snaps rather than sweeps. Zoom is not blended.

**Two committed ticks.** `frame.Buffer` gains `Previous()`: the slot published
immediately before `Current()`, or nil before the second publication or while
a write is in progress. Presentation reads both slots on the main goroutine
between Updates, when no write is in progress. A nil previous is a snap.

**Blended view.** `RecordFrame` in the Enhanced path records a shallow copy
of the current `Frame` whose `Units`, `Projectiles` and `Effects` slices are
replaced by blended copies held in retained client buffers; every other slice
and scalar (fog, visibility, selection, orders, events, HUD readouts, `Tick`)
is the current tick's. Blending is `prev + (cur − prev)·f` with the fraction
as 16.16 and truncation toward zero for `numeric.Fixed`, and along the
shortest arc for `uint16` angles (the signed 16-bit difference scaled by f).
Blended fields:

* unit `X, Y, Z`, `Heading, Pitch, Bank`, and each piece's `Tx, Ty, Tz`,
  `RotX, RotY, RotZ`;
* projectile `X, Y, Z`, `StartX..Z`, `TailX..Z`, `Yaw, Pitch, Roll`,
  `PropellerRoll`, `MeteorPitch`;
* effect `X, Y, Z`.

**Identity and snap.** A published unit match first requires equal nonzero
`InstanceID` values. Publication assigns that presentation-only identity to a
live object and changes it when a pool slot is reused; it is not authoritative
state. Frames that both carry zero retain the fixture fallback: pool handles
carry no generation [01 §6.1], so the match is a handle plus consistency. A
unit then requires the same `Slot` with equal `DefID` and `Owner`, unchanged
`Carrier` and `MoverMode`, the same number of pieces, and a horizontal
displacement of at most 64 world units in the tick. If only one unit has a
usable identity, it takes the current pose. A projectile first requires an
equal nonzero `PresentationID`, then retains the existing equal `WeaponID`,
`Shooter`, and `CreationTick` continuity checks. Combat assigns one process-local,
presentation-only admission identity for every root and burst clone, carries it
in a parallel array through stable pool compaction, and publishes it without
changing the authoritative packed handle, RNG, or retail save state. A zero-ID
fixture projectile always takes the current pose; it never falls back to the
packed `Handle`. An effect matches on
`PresentationID` when nonzero, else on `ID`, `EventSeq` and `StartTick`.
Anything else takes the current pose. The 64-unit bound is a presentation
constant chosen above any retail movement rate; it is not a retail datum.

**Never interpolated.** Sprite and animation frame indices, the nanoframe
reveal band, damage flashes, palette rows, fog, visibility, selection, the
cursor and the HUD. A piece hidden in either tick is drawn as the current tick
says. A COB `turn` with no speed sweeps over one tick instead of jumping; this
is accepted.

**Benchmark.** `--benchmark-tps=120` runs one authoritative step every fourth
Draw and presents the four frames at fractions 0, ¼, ½ and ¾, so the report
measures the interpolated presentation; 30 and 60 keep one step per Draw.

**I6.** Amended for Enhanced only: the presentation may read the two most
recent committed ticks and the clock's carry; it writes nothing back and
consumes no simulation RNG. `--shot` and Original never blend.

**Per-frame model differences are the blend, not the atlas.** A report of a
"static structure that renders differently on every presented frame" was
traced against the 1080p battle benchmark at `--benchmark-tps=120` with the
four presented frames of three consecutive ticks dumped. Hashing every
recorded `ModelGeometry` per subject per frame separates the two sides. Of
145 subjects, 64 recorded a byte-identical packet across all twelve frames,
and every one of those rendered identical pixels except where a moving
neighbour crossed its box; a device fixture now holds the executor to that
premise (`checkModelSlotNeighbourIndependence` — uncommitted subjects added
to the page move every later subject to a different page origin and parity
and must not change one committed byte). The subjects that did differ per
frame were mobile units whose blended pose genuinely moved; the screen box
the report named holds seven of them and no structure. Original snaps to the
committed tick, so the same subjects step once per tick there — the contrast
is the blend working, not a defect.

The real defect the investigation did find is on the recording side and is
not per-frame: `unitGeometryPair` never read the cached-image discard a
`cache`/`shade` script setter raises [03 R-COMP-01 §4][04 R-MOV-03 §4], so a
piece whose cache bit came back was in the retained lane's past and neither
lane's present and stopped being drawn. It now applies the same three-term
gate the classic composer applies, and the cached lane's membership can no
longer go stale across an animation that toggles cache bits.

### 13.6 Work units

| Unit | Scope | Files owned | Gate |
|---|---|---|---|
| R1 composite (landed) | true-colour surface, PAL resolve in the scene shader, blend classes for the ALP and row families, shadow commit blend, fog over one read copy, scheduler without per-phase snapshots, expansion removed, attached-unit staging on one atlas, device fixtures rewritten to §13.4 | `internal/platform/gpurender/*` | §13.4 fixtures; M1–M8 captures viewed; passes 11 on every benchmark frame |
| R2 cadence and interpolation | Update stays at 30, modern presents on every Draw; `Buffer.Previous`; draw-time tick fraction from the un-floored millisecond source; blended camera origin; blended view with the identity and snap rules of §13.5; `--benchmark-tps=120` | `internal/frame/frame.go`, `internal/client/interpolate.go` (new) and the client entry points it needs, `internal/platform/ebitenapp/app.go`, `internal/platform/ebitenapp/battle_benchmark.go`, `cmd/nanolathe/battle.go`, `cmd/nanolathe/battle_benchmark.go`, `cmd/nanolathe/flags.go`, `docs/BATTLE_BENCHMARK.md` | `--shot` captures byte-identical on both renderers; classic benchmark rows unchanged; 120 TPS benchmark on-cadence share reported; motion viewed |

| R3a executor CPU (landed) | lit points placed from a cached cell with a stamped pixel table; batch storage pooled by size class and written in place; ring self-intersection tested once per face with triangle and quad fast paths; faces prepared by pointer; slot planes from the recyclable sub-image pool | `internal/platform/gpurender/*` | M1–M8 modern and both battle.png byte-identical; Submit 120 TPS 3.9 → 2.9 ms, 30 TPS 6.6 → 4.9 ms; passes 11 |
| R3b recorder CPU (landed) | piece chain collapsed to its rotating nodes with a reference-equivalence test, vertices applied in bulk, piece draws and polygons written in place, hidden pieces resolved once, projectile scratch retained | `internal/client/*`, `internal/render/*`, `internal/model/*` | `--shot` and M1–M8 byte-identical on both renderers; Record 120 TPS 2.8 → 2.0 ms; classic 30 Record not worse |

R1 and R2 are independent (R2 never edits `internal/platform/gpurender`) and
ran in parallel; R3a and R3b followed, also in parallel, once the 120 TPS
benchmark showed the frame CPU-bound (§13.7).

### 13.7 Outcome

Measured on main after R3 (1080p battle benchmark, 180 frames, M3 Pro):

| | modern before this round (main `eb7a6df1`) | modern after (main after R3) |
|---|---|---|
| Passes per frame | 104 | 11 |
| Phases per frame | 42 | 24 (30 TPS), 8 (120 TPS) |
| 30 TPS Record / Submit | 4.3 / 6.4 ms | 3.4 / 4.9 ms |
| 60 TPS cadence median, on the 16.7 ms floor | 19–23 ms, 2–22% | 16.7 ms, 79% |
| 120 TPS Record / Submit / cadence median, on the 8.3 ms floor | not reachable | 2.3 / 2.8 / 8.3 ms, 71% |
| Allocation per frame (30 TPS) | 1.9 MB, 24k objects | 1.4 MB, 12.5k objects |

Classic is unchanged at 13.1 ms Record and 93% on the 30 Hz floor.

What remains above 2% of a frame is Ebitengine's per-draw vertex conversion
(about 200,000 vertices per frame, half of them the model stage's key and
colour planes) and the lit point volume (one quad per covered pixel of every
flash disc [03 R-FX-01 §4]); a human motion review at the window is still
owed, because the agents that built this could not inject input.

## 14. The detail view: 1.5× and 2× steps and load-time remaster

### 14.1 Decision

The view scale is one of three steps: 1×, 1.5× and 2×. `camera.Scale`
[F-P1-008] is a `camera.ViewScale`, the scale in half steps — `ViewScaleNative`
(2), `ViewScaleMid` (3) and `ViewScaleDetail` (4), zero reading as native — and
the free fractional zoom that the camera once clamped to 0.25..4 stays
retired, with every `float32` in the projection. Retail has one scale, and
the point of the magnified views is that each is *the same view drawn from
more pixels*. The projection is integer at every step,

    screenX = Project(worldX − camX) + originX
    screenY = Project(worldZ − (worldY >> 1) − camZ) + originY
    Project(v) = ceil(v·s / 2)

and the inverse used for picking, `world = cam + floor(2·(screen − origin) / s)`,
is its exact inverse at every step: every screen pixel names one world pixel,
and every world pixel projects to the first screen pixel that picks it. At 1×
and 2× both reduce to the multiply and the floor divide the detail view was
built on, so a 2× asset lands on the pixel grid one-to-one and a 1× asset
doubled by nearest sampling lands on the same grid; nothing composed at scale
1 changes by a pixel from the build before this section, and nothing at 2×
from the build before the 1.5× step. At 1.5× consecutive world pixels are
alternately two and one screen pixel apart, which is nearest sampling at 3/2,
and the arithmetic is the named type's: `Project` for a position, `Px` —
`v·s/2` rounded half away from zero — for an extent or an authored offset, and
`Inverse` for a picked pixel. The type is distinct from `int32` so that a
plain multiply against a pixel count does not compile.

Continuous zoom is not lost by this, and §16 has since built it for the modern
executor: the recorder still emits at an integer step and the executor scales
the recording by the live factor over that step, so this section's integer
projection is what the free factor is built ON rather than something it
replaces. The classic executor keeps these three steps and only these three.
The 1.5× step exists because a 1080p window is too small at 1× and shows too
little at 2×, and it is the window's default above 800×600 (§14.6); in the
modern executor 1.5× is now the 2× step shrunk by three quarters rather than
the 1.5× variant art set, which is a classic-only path from §16 on.

The simulation never reads the scale [I6]. Movement, orders, physics, the
tick fingerprint and the save image are identical at 1× and 2× by
construction; the only thing that differs is which pixels present the same
committed frame. That is also the test: a 2× capture of a seed is a capture of
the same committed tick as the 1× capture.

### 14.2 The view transform — contract D1

Every world-space command is recorded in screen space by the recorder, and the
recorder is the one place the scale is applied. The executors replay recorded
coordinates and never rescale; both replay the same list, so the parity gate
of §6 applies at 2× exactly as at 1×. The rule per layer:

At 1.5× every row below holds with `Px` in place of `×s` and the 1.5× variant
of §14.3 in place of the 2× one: terrain tiles are 48 pixels, fog cells 48,
the health bar's half-extents `Px(17)` and `Px(2)`, the shadow's five-pixel
step `Px(5) = 8`, the flash disc's pixels the one- or two-pixel spans their
world pixels project to, and a model's local offsets `Px` each (Enhanced) or
its native image blitted over those spans (Original). A pre-scaled sprite
cannot be phase-exact at a fractional factor — a source column covers two
screen pixels or one depending on where its anchor projects — so a 1.5×
sprite may sit one pixel off the terrain's own sampling phase; that is the
cost of drawing variants rather than sampling on the device, and it is
invisible at 1× and 2×.

| Layer | Position | Size and art at s = 2 |
|---|---|---|
| Terrain | tile origin through `WorldToScreen` | 64×64 tiles from the detail tile set (§14.3), or the 32×32 tile doubled by nearest sampling when there is none; the record carries the scale and the detail tiles |
| Feature sprites, normal and shadow, opaque and ALP-tinted | anchor through `WorldToScreen`, authored offsets ×s | the frame's 2× variant (§14.3), drawn one-to-one through the same blit kind |
| Effect and projectile sprites | anchor through `WorldToScreen`, offsets ×s | the 2× variant; the load-time remaster covers features only, so these are nearest-doubled variants |
| Models: units, 3DO features, projectiles, the build ghost | anchor through `WorldToScreen` | Enhanced: projected local offsets ×s (`scaleModelLocal`, integer) and the image rasterized at output scale from the scaled geometry, textures sampling nearest so each texel covers s×s pixels; shadow, outline, reveal and waterline use the same scaled geometry. Original: the image is rasterized at native size exactly as at 1× and the classic blit doubles it about the anchor (`ClassicModelImage.Blit`), the shadow's five-pixel step included, so the classic frame is a pure nearest upscale. Height keys, the digger erase threshold and the sea level are world heights and do not scale in either |
| Fog | cell rectangle through the same projection | cell edge 32·s; the gray-remap and solid fills cover the scaled rectangle; the fog GAF cell (the cloud-edge variants) is drawn once from its 2× variant. The dither checker is a destination-pixel test inside the blit, so it stays one pixel at any scale on its own — tiling the 32×32 frame s×s times, the first draft here, stamped four cloud edges per cell and drew a cross through every boundary cell |
| Fills: health bars, selection plate, footprint and drag rectangles | through `WorldToScreen` | extents ×s |
| Lit points (flash discs) | centre through `WorldToScreen` | radius ×s; the point count grows with s², a known cost (§14.5) |
| Lines: nano beams, lasers, selection quad, dotted paths | endpoints through `WorldToScreen` | one pixel wide at every scale — a divergence accepted for now; a scaled width needs a width on the record in both executors |
| World-anchored text: group digits, labels | anchor through `WorldToScreen` | glyphs unscaled; text is interface, not world |
| Cursor, HUD, minimap, messages, menus | unchanged | unscaled; the minimap's viewport rectangle comes from `EffectiveView`, the view divided by s |

Picking goes through `ScreenToWorld` and the viewport transform, which
already funnel every pointer conversion through the camera: unit hover and
selection hulls are projected from world corners, so they scale with the
projection; the selection rectangle converts to a world rectangle with floor
division; the terrain cursor resolve runs on the world point. The camera
clamp measures the view in world pixels (`EffectiveView`, insets divided by
s, integer division). Scrolling and the follow glide move in world pixels per
host frame as retail does, so the screen moves twice as fast at 2×; that is
the retail behaviour at twice the magnification, not a defect. Middle-drag
converts the screen delta by 1/s so the world stays under the pointer.

The audit that lands D1 is the recorder's emission inventory: every
`emitSprite`, `emitFill`, `emitLine`, `emitPoints`, `emitModel`, `emitFog` and
`emitTerrain` site whose coordinates come from the world, in
`internal/client/{world_draw,effect_draw,projectile_draw,healthbar,flash_disc,
strip_draw,selection_quad,selection_overlay,selection_plate,hover_hull,
model_compose,model_shadow_pass,model_staging,frame,terrain}.go` and the fog
op builder in `internal/render/fog.go`. A site that draws in HUD space is left
alone. The build before this section scaled only terrain and model geometry,
which is why a 2× capture showed fog, sprites and bars at 1× positions.

### 14.3 Detail art — contract D2

The client takes one detail-art provider, installed by the command layer at
battle entry and cleared with the terrain:

* a detail tile set — one 64×64 index tile per entry of `Terrain.TileSet`, in
  the same order — recorded on the terrain command beside the scale; and
* detail sprite banks — for a feature GAF bank named by the map's feature
  definitions, a second bank with the same entry names and frame counts whose
  frames are 2×: width, height and the authored anchor offsets doubled, the
  same colour key, pixels and transparency at four times the count.

The client maps a loaded frame to its 2× variant by entry and frame index, not
by content, so the remastered bank and the loaded bank need not share
pointers. The provider is consulted only while the Enhanced executor
presents: the synthesized art is an Enhanced feature, and Original draws the
authored tiles and frames at every scale, so switching to classic at 2×
shows the original art nearest-doubled. The adapter tells the client which
executor presents on every switch; the capture and benchmark routes tell it
for the executor they drive (a "both" capture records for the modern one).
Where no variant exists — an effect, a projectile sprite, a bank the
remaster did not cover, the whole provider when `--auto-remaster=false`, or
any frame while Original presents — the client builds the nearest-doubled
frame on first use and keeps it for the life of the client, keyed by the
source frame's pointer (frames are immutable after load). The doubled frame is a plain frame: composites with alternate
children are doubled leaf by leaf. Executors see only frames; a variant is
just another frame to the sprite atlas.

**The 1.5× variants.** `formats.(*GAFFrame).Resampled(num, den)` is the
general nearest resample — sizes `ceil(v·num/den)`, anchors rounded half away
from zero, output pixel `j` reading source `floor(j·den/num)` — of which
`Doubled` is the 2/1 case. At 1.5× the client draws the provider's 2× variant
resampled at 3/4 when one exists (the remaster loses every fourth row and
column, which reads better than the authored frame at 3/2 with its uneven
columns) and the authored frame at 3/2 otherwise; both are built once per
source frame and kept, in caches separate from the doubled ones. The detail
tile set is decimated the same way, 64×64 to 48×48 by nearest sampling, once
per provider, and recorded on the terrain command in place of the 64×64 set:
the record's tiles are always at the screen tile size of its scale, stored
with that side as their row stride, so an executor copies a detail tile
one-to-one and resamples the 32×32 tile through `Inverse` only when there is
none. The fog cloud frames, never remastered, take the 3/2 variant in both
executors; the queue overlay's dash and icon art take it through the same
helper.

### 14.4 Load-time remaster — contract D3

`internal/upscale` holds the two synthesizers that lived in
`tools/mapupscale/patchmatchgo` and `tools/mapupscale/featupscale`, moved
without changing what they compute: the terrain tool and the sprite tool
become thin wrappers over the package, and on a fixed input the wrapper's
output is byte-identical to the tool's output before the move — that is the
move's gate. The premise and the algorithm are documented in
`tools/mapupscale/patchmatchgo/README.md` and the sprite tool's README; this
section records only the engine contract.

* **Inputs and outputs.** Terrain: the tile set, the tile map, the palette
  and the ALP table in; one 64×64 index tile per source tile out. Sprites: a
  query bank, its example banks, the palette and the ALP in; a parallel 2×
  bank out. Every option keeps the tools' shipped defaults; the engine passes
  none.
* **Determinism.** Seeds derive from tile and sample indices and tiles are
  independent, so worker count does not change the result. The cache below
  relies on that: a cached result and a fresh one are identical.
* **Coverage.** Terrain, and the feature banks the map's plot cells name
  through their feature definitions' `Filename`. The queries are the entries
  those definitions name — the rest sequence and the burn, die and reclaim
  sequences — not every entry of the bank: a bank can hold art no feature
  uses (`trees.gaf` carries three 640×480 `treegrow` frames that cost 3 s
  each, and frames of one entry are seeded from the previous frame so they
  cannot run in parallel), and synthesizing it would triple the first load
  for nothing on screen. The examples are the bank's own entries less the
  tool's default exclusions (fire, explosion, smoke and reclaim art, whose
  colours leak into idle art); the package expresses that as a filtered bank
  passed in `examples`, while `skip` names entries not synthesized at all.
  Shadow sequences — the entries a definition names as a shadow twin — are
  flat two-colour art the synthesizer handles badly (README); they are
  skipped and nearest-doubled by the client. An entry skipped and an entry
  the definitions do not name both leave nil frame slots, which the client
  doubles itself (D2). 3DO features and effect banks are not remastered.
* **Cache.** `os.UserCacheDir()/nanolathe/upscale/<format version>/<key>`
  where the key is a SHA-256 over the algorithm version and every input byte
  (tiles, tile map, palette, ALP; or bank bytes, example bank bytes, palette,
  ALP). One file per result with a magic and a version; a file that fails to
  parse is recomputed and rewritten. The cache is derived retail art and is
  never committed or shipped.
* **When.** On the loader goroutine after the session composes, before the
  battle is adopted, for the frontend load and the `--map` direct route; the
  capture route runs it inline and only at `--zoom 2`; a save restore adopts
  on the render thread and runs it inline too, blocking on a cold cache once
  for that map. Every windowed load pays it, even at 1×, because F9 can
  raise the scale later. First load of a map
  costs seconds (the READMEs' numbers: 2–5 s for terrain on twelve workers,
  0.2–1 s per bank plus 10–20 ms per frame); a cached load costs a file read.
  Progress is reported through the loading screen's progress callback under
  its own family so the bar moves. A synthesis failure is reported on stderr
  and the client falls back to nearest doubling; it never fails the load.
* **Switches.** `--auto-remaster` (default on) enables it; `--remaster <dir>`
  is unchanged — hand-authored 1× overrides mounted above retail are what
  the synthesizer then sees, and are remastered like retail art.

### 14.5 The modern executor at 2× — contract D4

The executor replays the recorded coordinates, so most layers need nothing.
The terrain pass builds one atlas per (tile set, scale): 64×64 tiles from the
record's detail tiles when present, else the 32×32 tiles doubled, and samples
it exactly as the native atlas at the native scale. The largest retail tile
set is Lava & Two Hills at 11,561 tiles (a scan of all 276 retail maps; the
first draft here named Painted Desert's 6,875): a 108×108 grid, 6,912² pixels
at 64×64, 182 MB as the RGBA8 index texture the atlas is today. That is one
page under this device's 16,384 maximum image size and three under a 4,096
one, so the atlas pages against `ebiten.MaxImageSize()` and draws one pass
per page. Storing the index in one channel would cut the texture to a
quarter; that is a follow-up.
Model geometry arrives in screen space and rasterizes as at 1×. The lit-point
volume grows with s² and was already the second lever of §13.7; if 2× costs
the 120 Hz budget, the disc becomes one quad with the disc test in the
fragment, which is a follow-up, not part of this section.

### 14.6 Runtime switches

* **F9** cycles the view scale 1× → 1.5× → 2× → 1× about the viewport centre
  (`ViewScale.Next`).
* **F10** toggles the executor between classic and modern. The client
  publishes the requested executor; the adapter switches at the next Update,
  turning interpolation and the synthesized art off when classic takes over
  (§14.3), and the retained screen bridges the swap. Neither key is a retail binding; retail's dispatcher does
  not read them.
* `--zoom 1|1.5|2` sets the scale at battle entry and applies to captures
  too; it replaces `--shot-zoom`, whose free fractional values are gone.
  Left unset, the window opens at 1.5× when its framebuffer exceeds 800×600
  in either dimension — the retail 640×480 and 800×600 modes stay native, a
  1024×768 or 1080p window opens magnified — and a restart keeps the scale
  the player was on. A capture and the battle benchmark take no such default:
  unset is native there, so every existing capture and benchmark scene is
  unchanged and a run's scale is always the one on its command line.
  `--shot-focus` stays. `--fps` is §13.5. The battle benchmark accepts
  `--zoom` and records it in the scene metadata as the factor (1, 1.5 or 2).

### 14.7 Verification

1. **1× unchanged.** Every capture of the §6 matrix and the battle scene is
   byte-identical to main on both executors; the 6000- and 54000-tick
   fingerprints are unchanged.
2. **The list relationship.** A test records one committed frame at scale 1
   and again at scale 2 with the camera on the same world origin and asserts,
   for every world-space command, that the scale-2 coordinates are the
   scale-1 coordinates doubled about the beam origin and the extents doubled;
   HUD commands are identical between the two lists. This is the automated
   form of the §14.2 audit.
3. **Picking.** For every screen pixel of a viewport at scale 2, the
   round trip screen → world → screen lands on the pixel's own 2×2 block, and
   a drag rectangle at scale 2 converts to the same world rectangle as the
   scale-1 rectangle over the same world.
4. **Parity at 2×.** `--shot-renderer both --zoom 2` on the matrix: the two
   executors differ only in the model raster approximation of §5.1, reported
   as a count, terrain, sprites, fog and fills identical.
5. **The remaster move.** The terrain tool's output on one exported map, and
   the sprite tool's output on one bank, are byte-identical before and after
   the move to the package; the cache round trip returns the computed bytes.
6. **Viewed.** 2× captures with the remaster and with nearest doubling,
   beside the 1× capture of the same tick, at least the M7/M8 battle scenes
   and one sprite-heavy map; then the window itself, both executors, both
   scales, toggled with F9 and F10 during motion.

### 14.8 Work units

| Unit | Scope | Files owned | Gate |
|---|---|---|---|
| U1 upscale package (landed) | move both synthesizers into `internal/upscale` with the tools as wrappers; the cache; the bank and tile-set APIs of §14.4 | `internal/upscale/*` (new), `tools/mapupscale/patchmatchgo/*`, `tools/mapupscale/featupscale/*` | byte-identical tool output before and after (9 of 9 files on `ac01` and `trees`); cache round trip; first load 1.35 s wall for a 1,984-tile map, 3 ms cached |
| D1 client view scale (landed) | integer camera scale; every world-space layer of §14.2; the detail-art provider and nearest-doubled fallback of §14.3; terrain record with scale and detail tiles; `--zoom` replaces `--shot-zoom` | `internal/camera/*`, `internal/client/*`, `internal/render/fog*.go`, `internal/drawlist/*`, `formats/gaf_variant.go` (new), `cmd/nanolathe/shot.go`, `cmd/nanolathe/flags.go` | §14.7 items 1–3; a classic 2× capture with every layer in place, viewed |
| D4 modern executor (landed) | detail tile atlas per scale; everything else verified rather than changed | `internal/platform/gpurender/*` | §14.7 items 1 and 4 |
| W1 wiring (landed) | load-time remaster on the loader goroutine with the cache and progress; the provider installed at battle entry and for captures; F9/F10; `--auto-remaster`; benchmark `--zoom` | `cmd/nanolathe/*`, `internal/platform/ebitenapp/app.go`, `docs/BATTLE_BENCHMARK.md` | §14.7 items 5 and 6; first-load and cached-load times reported |

U1 and D1 are independent and run in parallel. D4 and W1 follow D1 (D4 needs
the terrain record's scale and detail tiles; W1 needs the provider API) and
run in parallel with each other; W1 also needs U1.

### 14.9 Outcome

All four units landed 2026-09-09 (main `dd8c9337`). Measured:

| | value |
|---|---|
| 1× captures, both executors, four scenes | byte-identical to main before the round |
| First load with the remaster (terrain + feature banks) | Ashap Plateau 4.8 s, Great Divide 2.2 s, Arm mission 1 2.5 s |
| Cached load | 13–90 ms |
| 2× classic-vs-modern diff, Ashap Plateau / Great Divide | 1.4% / 1.9% of pixels, all fog-table and ALP rounding of the §13.3 composite (max channel delta 11–92); at 1× the same scenes differ by 0.6% / 1.2% with the §5.1 model raster (max delta 236–240) |
| 120 TPS modern benchmark, 1× | cadence 8.33 ms median, 71% on the floor (unchanged) |
| 120 TPS modern benchmark, 2× | cadence 8.86 ms median, 48% on the floor; Submit 4.1 ms against 2.5 ms |
| Largest 64×64 tile atlas | Lava & Two Hills, 6,912² pixels, 182 MB RGBA8 |

`--shot-renderer both` records the classic indexed image and the modern
geometry list separately, from one unchanged committed frame, camera and
presentation configuration. The modern half uses the same geometry-only
recording path as `--shot-renderer modern`; it never replays a
classic-inclusive packet or falls back to CPU model planes. This keeps the
combined route a valid parity tool after cached/live model composition.

Owed: a human look at the window with `--renderer=modern --zoom 2`, F9 and
F10 during motion, and at projectiles, effects, halos and health bars at
2×, which no capture scene produced.

## 15. Trails: Enhanced ground marks

### 15.1 Decision

Mobile ground units leave fading marks on the terrain: alternating footprints
for legged units, a pair of track segments for tracked ones. Retail leaves no
marks, so this is a Nanolathe presentation feature under the Enhanced umbrella
of §14.3: on while the modern executor presents, absent from Original, and
never a simulation input [I6]. The classic executor is unchanged and stays
the byte-exact reference.

The marks are geometry, not art: an oval and a segment whose coverage the
shader evaluates, darkening whatever terrain is under them. There is nothing
to author and the look is right on every tile set, because the terrain's own
colour is what fades.

### 15.2 Placement — contract T1

`internal/client/trails.go` keeps one ring of at most 4,096 marks and one
tracker per unit, keyed by the publication identity the orientation cache
uses and pruned with the live unit set. Once per committed tick, only while
Enhanced presents, every unit the classifier accepts is compared with the
point where it last laid a mark:

- **Class.** The FBI's `TEDClass` word decides: `KBOT` and `COMMANDER` lay
  feet, `TANK` lays tracks, the fixed and flying classes lay nothing. The
  movement class names describe footprint size and terrain rules (most kbots
  ride `TANKSH2`), so they only exclude: a `HOVER` or `BOAT` class lays
  nothing. `CNSTR` and `SPECIAL` cover both walkers and vehicles and fall back
  to the model: a piece named for a leg makes it a walker. The class is
  cached per definition.
- **Gate.** The unit must be mobile (BMcode), on the ground (mode mirror 1),
  complete, within two world pixels of the terrain under it, above sea level,
  and visible to the local player by the painter's own gate: a trail is the
  memory of a walk that was watched, never a sensor. A unit that fails the
  gate restarts its stride where it next qualifies.
- **Stride.** Feet every 10 world pixels, tracks every 8, laid along the
  straight line from the last mark to the current position, several per tick
  if the unit is fast. A step above eight strides is a move (factory exit,
  transport drop, restore), not a walk, and bridges nothing. Feet alternate
  sides at each mark.
- **Age.** A mark lives 300 committed ticks and its strength fades linearly
  to zero over that life. Ages are tick differences, never wall time.

### 15.3 Recording and drawing — contract T2

Recording projects each live mark through the view transform of §14.2 at the
terrain height under it, culls to the viewport plus a margin, and records the
whole frame as ONE `drawlist.Trails` batch between the terrain record and
strip 0, so features, shadows, units and the fog composite draw over it. The
batch rides the optional `drawlist.TrailSink` hook: a sink without it (the
classic executor, every test collector) replays the frame unchanged.

Mark geometry at view scale s, with the direction of travel mapped straight
onto the screen plane:

| mark | centre | half-length | half-width | peak darkening |
|---|---|---|---|---|
| footprint | ±2·FootX·s px across the path, alternating | 4·s px along the path | 2·s px | 0.4 |
| track (two per mark) | ±(4·FootX + 2)·s px across the path | 4·s px (the stride, so segments join) | 1.75·s px | 0.3 |

The modern executor draws the batch as one destination command over the
union of its marks under the row families' scale blend (§13.3): each mark is
a rotated quad whose fragment is `1 − strength × coverage`, the coverage a
soft oval for a footprint and a soft-sided, hard-ended segment for a track,
evaluated by the scene destination shader's `destOpTrail` from the quad's
local coordinates. Multiplies commute, so marks that overlap need no phase
ordering between them, and the scheduler still places the batch after the
terrain it darkens and before everything drawn over it.

The capture route observes every tick it advances (`Client.ObserveCommittedTick`)
so a `--shot` shows the marks the window would.

### 15.4 Verification

- `internal/client/trails_test.go`: the classifier table; a walker laying two
  alternating footprints along its step, nothing on first sighting, nothing
  re-recording the same tick, fading and expiry, nothing in Original; and no
  marks for airborne, elevated or moved units.
- `internal/platform/gpurender/trails_test.go`: the optional-sink replay, and
  the device fixture (`NANOLATHE_GPU_DEVICE_TEST=1`): a full-strength
  footprint darkens its centre to near zero and leaves the field untouched
  beyond its half-width and half-length; a half-strength track halves the
  field along its whole length and not beside or past it.
- Captures: `--renderer=modern --shot-renderer=modern` at 1× and 2× on the
  seeded Ashap Plateau scene, viewed.
- 120 TPS modern benchmark at 1× against main on the same machine state:
  Record and Submit within noise.

## 16. Smooth zoom and the strategic view (modern)

### 16.1 Decision

The modern executor's view scale becomes a free factor. §14's three half steps
stay exactly what they are for the **classic** executor — 1×, 1.5× and 2×,
switched by F9 and `--zoom`, with the 1.5× variant art set and nothing else —
and nothing in this section changes a classic pixel. In the modern executor the
factor is continuous: the mouse wheel over the battle viewport moves it, it
eases toward its target on the host Update grid, it snaps onto a rest step when
the wheel goes quiet near one, and below half scale the world becomes the
**strategic view** — terrain, fog and selection with the units drawn as
markers.

This is the §5.2 item, and it is a Nanolathe presentation feature under the
Enhanced umbrella. Retail has one world scale and no wheel zoom; nothing here
is a retail finding, and the simulation cannot tell what the factor is [I6].

The invariant everything below rests on:

> **The recorder emits at an integer step; the executor scales what it emitted.**

At 1× and 2× the two are the same number, the executor's transform is the
identity, and the output is byte-for-byte what the build before this section
composed. That is what keeps the §6 parity gate intact while the view in
between them is free.

### 16.2 The factor and the step — contract Z1

`camera.Zoom` is the live factor in 1/1024 units: `ZoomUnit` is 1×, `ZoomMax`
2×. It is the free generalization of `camera.ViewScale`, and at the three rest
factors (1024, 1536, 2048) its `Project`, `Inverse` and `Px` answer exactly what
the matching half step's own arithmetic answers — the unit is 1/1024 rather than
16.16 precisely so those three are exact small integers.

`Camera` now carries both:

* `Scale` is the **record step** the recorder projects at. `WorldToScreen`,
  the terrain record, the detail-art selection and the fog op builder all read
  it and are unchanged.
* `Zoom` is the **live factor** the player sees. `EffectiveView`, `clampInsets`,
  `BattleView`, `Drag`, `ScreenToWorld` and the minimap's viewport rectangle all
  read it, because they measure the view in world pixels and the view is what is
  on screen.

A zero `Zoom` reads as the step's own factor, so a camera that never sets one
behaves exactly as it did before this section — which is the classic executor's
permanent state.

The record step follows the factor for the modern executor (`Zoom.Step`): the
2× step above 1×, the native step at or below it. The 1.5× variant art of §14.3
is therefore a **classic-only** path from here on: modern's 1.5× is the 2× step
shrunk by three quarters. `Camera.AtRestStep` reports the identity case.

The classic executor does not derive its step from a factor: a factor handed to
it is one of the three views and is set through `SetScaleAbout`, which writes
the step and the factor together (`ViewScaleForZoom` names the step). Deriving
it instead was a defect caught by the capture gate — it turned a classic 1.5×
capture into a 2× one.

### 16.3 The transform and the record extent — contract Z2

**The world region.** The recorder brackets its world commands with a
`drawlist.WorldSpace` marker carrying the factor, the step, the record extent
and the battle viewport. It reaches an executor through the optional
`drawlist.WorldSink` interface, exactly as the trail family reaches one, so the
classic executor and every existing fixture are unaffected. The region opens
after the clear and closes before the chrome; the strategic marker layer sits
between them.

**The rule the region encodes.** A draw positioned from WORLD coordinates
belongs inside a world region; a draw positioned from POINTER or framebuffer
coordinates belongs outside one. A pick test compares like with like: presented
pointer against presented positions, or record pointer (`ScreenToRecord`)
against record positions. The first build of this section broke the rule in
both directions and a play test found each:

* The **drag-selection rectangle** takes the pointer's own framebuffer corners
  and was recorded before the close marker, so the executor scaled it and the
  rubber band came away from the cursor. It now records after the close, and its
  clip is the framebuffer's rather than the record extent's.
* The **build ghost** and the **order-queue overlay** are world-positioned but
  are composed in the UI stage, after the close marker, so they were left at
  record coordinates while the world under them shrank — at half scale the ghost
  sat twice as far from the framebuffer origin as its site. The client therefore
  exposes `BeginWorldOverlay`/`EndWorldOverlay`, a **second** world region the UI
  stage brackets those two with. The executor already submits its schedule at
  every boundary, so a second region costs two more submissions on the frames
  that open one, and the battle opens one only when a placement is armed or
  Shift is held.

Inside an overlay region the UI helpers that bake a framebuffer bound into the
recorded command — `UIBlit`'s clip and `UIText`'s default control width — take
the record extent instead, so a world overlay near the right or bottom edge is
not clipped away before the executor has shrunk it. At a rest factor the two
extents are equal, so none of this changes a composed pixel there.

**The transform.** Inside the region the modern executor's scheduler scales
every rectangle it places and every vertex it appends by *factor / step's
factor*, about the **surface origin**. It is a pure scale with no translation
term because the recorder projects the world from the framebuffer's own
top-left — the camera origin is drawn there and the chrome is painted over it —
so record x = step·(worldX − camX) and screen x = factor·(worldX − camX) differ
by exactly this ratio, and the world under the viewport's corner therefore stays
put on its own. The factor is never above the step's, so the transform only ever
shrinks.

It is applied at the scheduler's intake — `beginBlended`, `beginPoint`, `quad`
and `quadCorners` — so the overlap tests that decide a command's phase compare
what actually lands on the composite. At a rest step it is disarmed and every
device call is the one the build before this section made.

**The record extent.** Below the step the recorded world has to cover more
pixels than the framebuffer has: `recordW = Project_step(Inverse_factor(width))`
plus a two-step pad for the rounding of the two conversions, and the same on the
other axis. `internal/client` keeps it in `recordW`/`recordH`, refreshed once per
recorded frame, and **equal to the framebuffer whenever the factor is on the
step** — which is always, in classic. Every world emission site of §14.2's D1
inventory clips against it; interface sites keep `c.width`/`c.height`, because
the chrome is drawn in framebuffer pixels at every factor. The modern executor
clips world commands against the extent the marker carries (`clipW`/`clipH`) and
interface commands against the framebuffer.

The classic byte writer for point batches bounds its own store, because a
`--shot-renderer both` capture replays one list through both executors and the
list may be recorded past the framebuffer.

**Fog.** The fog composite is the one family the generic transform cannot carry.
It reads the pre-fog copy of the composite 1:1 under each fragment, so its
source coordinates have to equal its destination coordinates in screen space,
and its shader recovers a fog cell from the fragment's own position, so it has
to be told which record pixel that position is. Its region is therefore
transformed by hand, the generic transform is held off for that one command, and
the screen-per-record factor rides the colour lane the fog quad never used. The
shader's cell lattice and atlas tile stay in record pixels; its dither checker
stays a test on the destination pixel, and so stays one screen pixel wide, as it
already did at every view scale.

**Sampling — contract Z10.** A palette index cannot be interpolated, so every
index-space lookup is a nearest texel fetch and stays one (C-G4). Filtering, when
it happens, happens **after** the palette resolve: four texels, each resolved
through PAL, blended in colour.

The terrain takes that path whenever the world transform is not the identity.
One screen pixel then covers more than one tile texel, and a nearest fetch picks
an arbitrary one of them, so the map's noise crawls as the view eases and
sparkles between adjacent factors. The four taps ride the tile atlas's own
border, so they need no clamp of their own, and the choice rides a vertex lane
(`Custom0` of `sceneOpTerrain`) rather than a second shader, so the terrain still
merges into the frame's own opaque run. At a rest step the lane is zero and the
fetch is the nearest one this pass has always made, which is what keeps the §6
parity gate exact.

Measured on the seeded Ashap Plateau scene at 1024×768 over a 200×150 terrain
region: the mean absolute difference between captures at 0.70× and 0.71× falls
from 10.75 to 7.83, and the region's high-frequency energy — each pixel against
its own 3×3 mean, which is what "sparkle" is — from 7.12 to 4.42, a 38%
reduction. The cost did not register: submission time is unchanged (1.35 ms
against 1.37 ms at 0.7×) because the lane is one more float, and the draw cadence
sits on the display's 16.68 ms floor both ways at 1024×768 **and** at 3840×2160,
so the extra fragment work has headroom to spare on a Metal host.

Sprites, model commits and the fog atlas stay nearest. `TODO(question)`: a keyed
source cannot be blended the same way — the four taps straddle the colour key, so
the blend has to weight by coverage and hand the composite a fractional alpha,
which is an antialiased sprite edge and a look to approve rather than a
correctness fix. Whether the Enhanced view wants that is a human call.

**One screen pixel, exactly one.** A world quad that would shrink below one
screen pixel is given a span of exactly 1.0, not "at least one". The world is
full of one-pixel primitives — the selection quad's lines, a dotted path's dots,
a lit point's row span — and without the floor they fall between two pixel
centres and vanish at an arbitrary subset of factors; with a floor that rounded
up any further they would cover one centre at some factors and two at others, and
a one-pixel line would flicker in width as the view eased. A span of exactly 1.0
contains exactly one pixel centre wherever it starts. Nothing already wider is
touched, so the terrain's tiles and every sprite still tile the plane exactly and
seamlessly, and since the transform only ever shrinks, a one-record-pixel
primitive can never arrive wider than one screen pixel to begin with.

**Every atlas cell carries a border — contract Z9.** Nearest sampling is exact
only while the source coordinate lands strictly inside the quad's source
rectangle, and under the transform it does not always. The rectangle's source
span is longer than its destination span, so the fragment nearest the far edge
maps to within a fraction of a texel of the source rectangle's far edge, and the
interpolator's own rounding is enough to floor it one texel over. Whether it does
depends on where the quad's screen edge falls relative to a pixel centre, which
is a function of the factor and of the camera modulo the tile — so it happens for
a periodic subset of tile columns at some factors and for none at others. That is
the intermittent **tile seam** the play test reported, and the same read at a
sprite's or a model slot's edge is the stray foreign pixel beside it.

The fix is a border of duplicated edge texels around every packed entry, which is
the clamp the sampler will not do for us: Ebitengine does not confine a sample to
a sub-rectangle of a bound image, and a per-quad clamp would need four more
vertex lanes than the scene shader has. Three atlases carry one:

* the **tile atlas** pads each cell by one texel of the tile's own edge
  (`tileAtlasPad`), at 13% of the atlas at the native tile and 6% at the detail
  one;
* the **scene atlas** reserves and fills the same one-texel border around every
  packed GAF frame, glyph strip, PCX and surface (`sceneAtlasPad`);
* the **model slot atlas** leaves a two-texel gutter around every slot
  (`modelSlotGutter`, a multiple of `modelSlotAlign` so slot origins stay even
  [03 R-REN-03A §7]). The page is cleared to the composition background over its
  whole used extent, and the commit skips that index, so a read into the gutter
  is the skip it should be.

An entry's recorded placement stays its first **inner** texel in all three, so no
recorded source rectangle changes and a rest step composes exactly what it
composed before. Measured on the seeded Ashap Plateau scene at 1024×768: the
borders change nothing at 1× or 2× and nothing at most factors, and they remove
between three and 2,314 pixels at 0.75×, 0.78×, 0.8×, 0.82×, 0.92×, 1.25× and
1.75× — every one of them a run along a tile, sprite or slot edge.

### 16.4 Picking — contract Z3

Screen to world is `cameraOrigin + floor((screen − viewportOrigin) / factor)`,
integer throughout: the exact inverse of the projection at a rest factor and the
obvious floor in flight. Every pointer conversion already funnels through
`Camera.ScreenToWorld`, so the terrain cursor, order targets and the minimap
follow it for nothing.

The two pick tests that cannot be expressed as a world point — the hover hull
polygon and the drag rectangle's containment test — compare the pointer against
corners produced by `WorldToScreen`, which projects at the **record step**.
`Camera.ScreenToRecord` bridges them: it composes the live inverse with the
record projection, and it is the identity at every rest factor, so nothing about
picking changes there.

### 16.5 Zoom about a point — contract Z4

`Camera.SetZoomAbout(mx, my, f)` is `SetScaleAbout` generalized: the world point
under (mx, my) is computed through the old factor, the new origin is that point
less the same quantity at the new factor, the record step is re-derived, and the
camera is clamped. The world point under the anchor does not move.

The anchor is in **beam pixels** — the framebuffer point plus the viewport
offset `(128, 32)` — which is the space `ScreenToWorld` takes, because the
recorder stores a world point at its beam position less that offset [03 §2.5].
The battle session's zoom writers (`beamAnchor` in `cmd/nanolathe`) convert the
pointer, the viewport centre and `--shot-focus` from framebuffer pixels at one
seam, so the test of the contract is the one that matters to the player: a
world point drawn at framebuffer pixel P before the zoom is drawn at P after
it. The first build of this section anchored on the raw framebuffer point,
which held the world 128/f pixels left of and 32/f above the pointer fixed
instead, and the map slid under the cursor.

### 16.6 The wheel, the steps and the ease — contract Z5

`camera.ZoomController` is the state machine, driven once per host Update from
the battle's camera pass. Its clock is host Updates, supplied by the platform
layer; no simulation tick is read [I6].

* **The steps.** `ZoomSteps` is the ascending list {0.25, 0.5, 0.75, 1, 1.25,
  1.5, 1.75, 2}: five detail views at and above 1× and three tactical views
  below it. The target is always one of them, or the map's floor (§16.7) when
  the lowest steps fall under it.
* **The wheel** moves the *target* one step per notch: every `ZoomWheelNotch`
  of travel (a thousandth-units count, so a notched mouse's whole unit is one
  step) goes to the next step up for a scroll up and the next step down for a
  scroll down. A trackpad's fractions bank until they are worth a notch; a
  reversal discards what is banked, so drift does not step. From a factor
  between steps — the ease in flight, a free `--zoom` — the wheel goes to the
  nearest step in its direction of travel, so it always lands on a step. The
  gesture is anchored at the pointer, and the anchor is kept for the whole
  animation.
* **The ease** closes `ZoomEaseFraction` of the remaining gap per Update, moves
  at least one unit so an integer factor cannot stall, and settles outright
  inside `ZoomSettleEpsilon`. It is what makes a notch a glide rather than a
  cut, and it is the only time the live factor is off a step.

The first build of this section had a free log-scale wheel with an idle snap
onto the detail steps and nothing below 1×. The play test preferred discrete
steps with the glide between them, on both sides of 1×, and that is what stands.
Every one of the names above is a **feel-tuning knob**, not a derived value, and
they live together at the top of `internal/camera/zoomfeel.go` so they can be
tuned by hand.

The wheel binding is Nanolathe's, not retail's. Retail leaves the wheel to the
active GUI list under the pointer [07 §2][07 §10], and the UI boundary still
consumes it first: the camera pass sees only a wheel the chrome did not want,
and takes it only over the battle viewport, only outside TALK, only with no
modal open and the pointer off the minimap, and only in the executor that can
present a free factor.

### 16.7 The minimum factor — contract Z6

`Camera.MinZoom` is `max(viewW/mapW, viewH/mapH)` over the battle viewport and
the playable map, rounded up: the factor at which the view in world pixels
exactly covers the map in whichever axis runs out first, so the clamp never has
to letterbox. Targets below it are clamped, both at the controller and at the
camera. The viewport span is taken in framebuffer pixels — the chrome does not
move with the zoom, so the span being fitted is a constant of the window.

The step writer deliberately does **not** apply this floor: a step is always at
least 1×, and clampAxis's view-larger-than-map domain stays exactly where
[07 §10] left it.

### 16.8 Runtime switches

* **F9** in classic is the unchanged 1× → 1.5× → 2× step cycle about the
  viewport centre. In modern it is the same three factors as animated zoom
  targets; a factor the wheel left between them cycles to the first step above
  it, so the key always lands on a step.
* **The wheel** is §16.6.
* **`--zoom`** accepts any factor in the free range for the modern executor and
  only 1, 1.5 or 2 for classic — the executor's restriction is applied after
  parsing, because `--renderer` may follow `--zoom` on the command line. A
  capture follows `--shot-renderer` when one is given. The map-derived floor is
  applied at battle entry, where the map is known. The resolution default is
  unchanged: unset opens at 1.5× above 800×600 and natively at or below it, and
  captures and the benchmark stay native. A restart keeps the factor the player
  was on. Battle entry, a restart and a capture take the factor outright rather
  than easing it: there is no motion to smooth.
* **F10** is unchanged (§14.6).

### 16.9 Gating summary

| Factor | World |
|---|---|
| 2× … 1× | everything, recorded at the 2× step |
| 1× … 0.625× | everything, recorded at the 1× step (2× above 1×) |
| 0.625× … 0.5× (exclusive) | everything, plus the marker layer fading in |
| 0.5× and below | terrain, fog, features and their shadows, selection fills, the build ghost and queue overlay, drag rectangle, dotted paths, markers |

### 16.10 What the strategic view drops — contract Z7

At and below `strategicModelCut` (0.5×) — inclusive, so the 0.5× wheel step of
§16.6 is a marker view — the recorder does not emit unit models,
projectiles, effect strips, trails, unit labels or health bars. Terrain, fog,
**the features** — sprite and 3DO alike, with their shadows — the selection
quad, the build ghost, the order-queue overlay, the drag rectangle and the
dotted paths still record: they are the map, and the map is what the strategic
view is for.

Features were originally dropped with the units. A play test found that jarring
and it is: a marker stands in for a unit, and nothing stands in for a rock, a
tree or a wreck, so the map lost its landmarks at exactly the factor a player
pulls out to read them by. They cost one sprite quad each and the transform
shrinks them like everything else, so they stay.

Terrain below 0.5× is the 1× tile set sampled down by the transform.
`TODO(question)`: a half-resolution tile set would sample better and cost a
quarter of the atlas; whether the load-time cost is worth it is unmeasured.

The recorder emits many more tiles and fog cells at a low factor. That path is
allocation-free per frame as the rest of the recorder is — the record extent is
two integers, the marker batch is a reused arena — but the per-frame tile and
cell counts do grow with 1/factor², which is the cost of the view.

### 16.11 The marker layer — contract Z8

Below `strategicMarkerOn` (0.625×) each unit becomes one filled square of
`strategicMarkerSize` (4) **framebuffer** pixels.

* **Its records and its gate are the minimap's own.** The markers come from the
  committed radar contact list and pass exactly `render.MinimapBlipAdmitted`,
  the gate the minimap's dots take [03 §3.9][03 R-MM-01 §3]. Visibility is not
  re-derived: a unit the minimap will not show has no marker either.
* **Its colour is the minimap's own.** The client is handed the `radlogo` blip
  art at battle entry and takes each player colour's marker index from that
  colour's own frame — its most common opaque index, resolved once per colour.
  Naming a colour instead would be a second table to keep in step with the art.
* **Selection** adds a one-pixel outline in the selection colour, which fades
  with the layer rather than appearing at full strength first.
* **The fade** is linear from alpha 0 at 0.625× to 255 at 0.5×. Models are
  hard-cut at 0.5×; a model cross-fade needs an alpha lane on the model commit,
  whose `sceneOpModelCommit` fragment is opaque today, and is a follow-up
  (`TODO(question)` at `Client.markerAlpha`).
* **No projectile markers.** A shot is an event, not a thing on the map.
* Markers are recorded **outside** the world region, already positioned through
  the live factor, because a fixed-pixel mark must not be scaled by the world
  transform. They draw through a new destination op (`destOpMarker`) as a
  premultiplied flat index at the layer's alpha.
* **Hover** picks the nearest marker within its square: picking runs on the
  world point under the pointer as always, and at these factors one screen pixel
  is several world pixels, so a hull test already admits the pointer anywhere
  over the marker.

### 16.12 Verification

1. **The rest steps are untouched.** Classic captures at 1×, 1.5× and 2× and
   modern captures at 1× and 2× are byte-identical to the build before this
   section, on the seeded Ashap Plateau scene at 1024×768.
2. **The camera.** `internal/camera/zoom_test.go`: the free factor agrees with
   the half steps at each of them; picking is the exact inverse at rest and the
   floor in flight; zooming about a point leaves that point fixed; the minimum
   factor is tight against the map; the step writer keeps the classic camera on
   its step; `ScreenToRecord` is the identity at rest.
3. **The state machine.** `internal/camera/zoomfeel_test.go`: the wheel composes
   on the log scale; the snap waits for the idle delay and fires only inside the
   band and only at or above 1×; the ease terminates.
4. **The recorder.** `internal/client/world_zoom_test.go`: the record extent;
   one world region per frame with the right operands; the strategic drops, the
   surviving selection fills and the surviving features; the drag rectangle
   recorded outside every region; the marker count and the alpha ramp at 0.625×,
   0.5625× and 0.5×; the minimap gate.
5. **The UI stage's own region.** `cmd/nanolathe/battle_world_overlay_test.go`:
   at 0.5× and at 1.3× an armed placement records two open/close pairs, the
   ghost's fills lie inside a region, and the recorded rectangle put through
   factor/step is the presented `f·(site − camera)` to within a pixel.
6. **The executor.** `internal/platform/gpurender/world_test.go`: a rest step
   compiles byte-identical geometry, a non-rest factor places a known sprite at
   the expected scaled rectangle, and a sub-pixel world primitive compiles to a
   span of exactly one screen pixel while a wider one is untouched.
   `atlas_pad_test.go`: each of the three atlases keeps its border, and the tile
   border holds the cell's own edge.
7. **Viewed.** Modern captures at 1.3×, 0.7×, 0.45× and 0.3× on the same scene:
   terrain seamless, HUD unscaled, features present at every factor, markers
   present and unit models absent below 0.5×.
8. **The seam sweep.** Modern captures at 38 factors from 0.45× to 2×, before and
   after the atlas borders: identical at every rest step and at 31 of the others,
   and a run of foreign pixels removed at 0.75×, 0.78×, 0.8×, 0.82×, 0.92×, 1.25×
   and 1.75×.

### 16.13 Owed

A human look at the window itself: the wheel in motion, the snap's feel, F9
across the executors, and the strategic view on a map with many units — no
capture route produces motion, and the feel knobs are meant to be tuned against
it. The filtered terrain is part of that look: it is a taste call as well as a
measurement, and a player who wants the crunchy nearest-sampled map back should
be heard before the lane becomes permanent.

The known follow-ups: a keyed source's filtered sampling (`TODO(question)` in
`gpurender/schedule.go`), the half-resolution tile set for the strategic range
(`TODO(question)` in `gpurender/terrain.go`), and the model cross-fade at the
strategic cut (`TODO(question)` at `Client.markerAlpha`). The capture route
cannot arm a build placement — `--shot-select` disarms one — so the ghost's
position is held by its test rather than by a capture.
