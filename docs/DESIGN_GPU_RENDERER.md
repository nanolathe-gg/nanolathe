# Design — The frame draw list and the GPU renderer

`internal/drawlist` (new), `internal/platform/gpurender` (new), and the
recording side of `internal/client`. One committed-frame walk records one
ordered list of draw commands; two executors replay it. The **classic** executor is the software composer writing palette indices into
a byte surface. The **modern** executor replays through Ebitengine in palette
index space, with conventional GPU model rasterization permitted by the visual
acceptance policy below. Original, GPU Classic, and Enhanced are the intended
three user-facing modes, backed by one CPU and one shared GPU implementation.
The public choices are `--renderer=classic|modern`, default modern, also
selectable on the Nanolathe options page. The default presentation cap is 60 FPS;
both choices persist between windowed runs (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
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

**Source lifetime (host ownership).** The window compares the client's terrain
binding generation after a joined update and before either executor draws. A
changed binding, including teardown to no terrain and reloading the same map,
discards speculative recording and the paused-world image and identity, then
calls `Renderer.ResetSources`. This retires terrain pages, GAF/PCX/FNT source
atlases, model texture and composition pages, fog sources and their dependent
compiled runs, upload identities and frame scratch. Shared images are released
once. Shader programs, palette tables, output surfaces, and renderer settings
survive. Stable bindings, zoom changes and ordinary frames retain their caches.

This is a host resource-lifetime correction, not a retail rendering claim.
Previously the one process-lived renderer retained each newly loaded terrain
and immutable source identity forever. Same-map restarts also loaded new
identities, so memory grew with the number of battles. The terrain reference
also retains its movement-service binding.

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
  Mobile units are not supersampled in retail. This is the classic executor's
  contract. Enhanced replaces it with the subject-wide coverage supersample of
  §17: every subject doubled, live lane included, outline endpoints whole
  pixels, resolved with fractional coverage and no fringe (§22). The GPU recorder retains the cached lane
  and records current live faces separately in both.
* **C-G7 Fog composition.** The recorded fog ops are converted to a per-tile
  grid texture (kind, variant, frame, pattern parity) and may be applied by a combined
  shader over the world image: solid fills write the dark index, gray fills
  write `Gray[dst]`, patterned fills test the same `(x + y + parity) & 1` the
  byte writer tests, and fog GAF frames sample their frame `[03 §3.3]`
  `[03 §4.3.3 R-RR16-A §1]`. The visible result per pixel is the byte writer's,
  subject to Enhanced's approved colour arithmetic (§13). Composite fog uses
  the ordered child path in §13.3 rather than a flattened atlas mask.
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

### 5.4 Lighting (deferred); glow (§19); antialiasing (§17)

Lighting may be palette/tint based or use model geometry/material information;
no technique is selected. Preserve resolved asset and geometry identity rather
than inventing material values. Glow is designed and landed in §19: an
Enhanced-only bloom from beams, lightning, effect and projectile art and the
explosion light, resolved under the fog. Model antialiasing is designed and
landed in §17: subject-wide supersampling with a coverage resolve, Enhanced
only.

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
`--shot-renderer=both` require the modern executor, now the default. Isolated model preview resolves the selected unit's ObjectName,
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

Modern shadows for mobile and Digger subjects are the body's own silhouette,
as retail's are: the recorder emits a faceless shadow packet carrying the body's
box, the shadow anchor and the buried or submerged clip key, and the model lane
reads the body's finished raster at that placement (§22). Classic copies the
finished body image for those subjects, clears transparent colour-key coverage,
flattens to index 0 and applies the inclusive underwater/buried cutoff; the two
executors now agree on the shape. Only a structure projects a separate,
quarter-sheared, punched shadow. The
producer applies the corrected master/vehicle/Digger gates, independently
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

> **Retired in part (2026-09-11).** The model slot stage this section and
> §11.5 "Model slot passes" designed — the per-subject slot atlas, its key,
> body, reveal, clip and resolve passes and the residency table of §13.12 —
> is gone; the model lane of §22 is the modern executor's only model path.
> The 2D families, the scheduler and the allocation policy below stand.

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

Preparation records also reference recorder geometry and earlier arena
allocations. The executor clears used pointer-bearing preparation records after
submission and retires page subject references at the next frame boundary,
including dormant overflow pages. Numeric arenas retain their capacity and
contents. Recorder refill clears removed face references; polygon records drop
lane references only when their backing arrays are replaced. Growth preserves
every earlier slice still in use within the frame. Warm reuse remains free of
allocations. This corrects reachability; it does not attribute a measured
long-match heap footprint to those references.

**Hidden-frame command lifetime.** Ebitengine 2.10.1 is the minimum backend
version: it completes graphics frames even when the window is hidden or
occluded, without presenting them. The earlier release candidate still called
Draw but skipped the queue flush, accumulating ordinary frames' vertex/index
copies until presentation resumed. This is a backend lifetime correction;
background simulation and model geometry are unchanged.

Capture diagnostics count the actual vertex and index slice lengths at every
executor triangle submission, including model padding and overflow passes.
`ModelKeyVertices` and `ModelColourVertices` separate the expensive model
lanes; `MaxSubmissionVertices` identifies a single unusually large draw.
The renderer retains the full statistics and Execute sequence number of the
frame with the most submitted vertices. These counters exclude image-copy
draws, adapter draws and dependency-internal work, and describe submissions,
not retained heap or GPU memory. They require no per-frame allocation and
never enter authoritative state.

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
outline, waterline and digger are unchanged. As first landed only the commit
resolved colour; since §17 the model stage ends in a coverage resolve that
produces colour on the slot page, and the commit copies it.

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
  family. Ordinary stock frames keep one copy pass per frame. If a selected
  frame is composite, extends outside its atlas tile, or lies beyond the atlas's
  frame range, the entire fog command uses an ordered fallback. It walks ops
  and children in their original order and clips each leaf only to the target
  [03 R-COMP-01 §2]. Gray/checker recursion applies the raw gate before each
  parent or child and ignores alternate selectors. Black recursion selects ALP
  for an alternate child and propagates tint through its descendants. Keyed and
  tinted leaves use the existing sprite streams; gray/checker leaves use a
  masked fog shader in the scheduler's snapshot stream. Overlapping gray leaves
  therefore receive separate read copies even though modern desaturation is
  idempotent. The fallback changes ordering representation, retaining this
  section's approved colour arithmetic. Child anchors can reach before the cell
  origin or outside the parent's dimensions without atlas clipping.

  Authored tests lock repeated gray snapshot phases, nested black-child ALP
  order, parent/child raw gates, transparent holes, and child extensions. The
  existing opt-in fog device fixture checks actual pixels and ordinary versus
  composite scroll crops at all three view scales. Setting
  `NANOLATHE_FOG_COMPOSITE_CAPTURE` to an output PNG path saves its authored
  composite scene for visual review.

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

The default cap is 60 FPS. The Nanolathe options page offers 30 / 60 / 120
and previews changes immediately; OK persists them, Cancel restores the entry
value. Explicit `--fps` overrides the saved preference at window startup, with
zero retaining display-refresh presentation. Captures and benchmarks use their
command-line settings independently of saved window preferences.

**Pointer latency.** Ebitengine's public cursor API reads its most recent
Update snapshot, so the window still samples pointer motion at 30 Hz. Modern
positions the recorded software cursor from that snapshot immediately before
GPU replay, after joining the recorder. This removes the additional presented
frame of positional delay from deferred input publication; it does not make
input polling refresh-rate-driven. The cursor's shape and animation, hover,
orders, placement previews and camera continue using the ordinary host step.
The GAF hotspot remains authored [07 §8]. Capture keeps the pointer hidden;
the release frame preserves its saved restore point [07 R-CAM-01 §11].
Original's retained frame and `--shot` keep their existing path. F11 reads the
submitted GPU image, including the cursor at its late-positioned location.

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
| R4 the lit point plane (landed, §13.8) | lit point runs placed once instead of once per pixel; a phase group of points committed as one quad over a per-frame plane atlas; the retained cached model lane copied into the body's own arenas | `internal/platform/gpurender/*`, `internal/client/model_cached_live.go` | both battle.png byte-identical at 180 and 720 frames; M1–M8 byte-identical on both renderers; classic Record not worse; Submit 120 TPS 720 frames 12.1 → 4.5 ms |
| R5 two-stage unit record (landed, §13.9) | per-unit geometry computed on a persistent worker pool into slots indexed by unit, consumed by the unchanged sequential bucket walk; per-worker scratch arenas; orientation and cached-body entries pre-created on the recording goroutine | `internal/client/record_parallel.go` (new), `internal/client/client.go`, `internal/client/frame.go`, `internal/client/model_geometry.go`, `internal/client/world_draw.go` | both battle.png byte-identical at 180 and 720 frames on three runs each; M1–M8 byte-identical to main on **both** renderers; identical list at one and twelve participants; `go test -race` and a race-built 180-frame battle clean; classic Record not worse; Record 120 TPS 720 frames 4.85 → 2.30 ms |

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

### 13.8 The lit point plane (fourth round)

§13.7 left "the lit point volume (one quad per covered pixel of every flash
disc)" as one of the two items above 2% of a frame. Instrumented, it was not
one of two items; it was the frame. A 1080p battle at `--benchmark-tps=120`
covers a **median 179,000 screen pixels per frame** with lit points and a
maximum of 963,000, and the batch compiled into 150,000 quads — **97% of every
vertex the executor handed the device**, about thirty megabytes of vertex
traffic per presented frame, written once by the executor and copied again by
Ebitengine into its command queue. Over a 720-frame run `drawLitPoints` and the
`DrawTrianglesShader32` under it were about 40% of the process, against 15% for
the whole recorder.

The span coalescing already in `drawLitPoints` could not help: the disc's ramp
is jittered per pixel by a CRT draw [06 R-WFX-01 §2], so the LHT row changes
almost every pixel and the average span was 1.19 pixels long.

**Placement per run.** A batch arrives as contiguous horizontal runs, because a
disc is recorded row by row. A run's x values are consecutive and distinct, so
no pixel of a run can depend on another pixel of the same run, and the phase
they must all take is the maximum of their individual answers; stamping the
whole run with that maximum leaves exactly the tag state placing them one at a
time leaves. `placePointSpan` evaluates the cell floor and the owner rectangles
once per *cell* the run crosses, scans the run's contiguous slice of the
cell-major point phase table, and stamps in one pass. Placing a pixel later than
its own dependency requires is always safe — it can only push a write further
behind things it already had to follow — so this is a relaxation of the phase
count, never of the order. `placePoint` survives as the per-pixel definition the
new placement is checked against in `schedule_point_span_test.go`.

**Geometry as a texture.** The runs of one batch that landed in ONE phase are
written into a rectangle of a per-frame RGBA8 atlas, one texel per covered
pixel, and committed as a single quad over their bounding box (`points.go`).
Four bytes per pixel replace two hundred and sixteen. A group too sparse to pay
for its box, too small, or larger than the atlas can serve keeps the quad path.

Two properties make that byte-identical rather than close.

*The lane.* The scale blend forms `dst × (src.rgb + src.a)`, and for every LHT
row `k = 1 + row/30` is at least one, so the low lane is exactly 1 and the whole
of the per-pixel information is the high lane. That lane is a multiple of 2⁻²³:
the CPU forms it as `fl(1 + fl(row/30)) − 1`, a sum whose value lies in [1,2) is
a multiple of 2⁻²³, and the rows the §13.3 clamp caps at 2 leave exactly 1.
Three bytes therefore hold it exactly. The shader reassembles
`r·65536 + g·256 + b` — every term and every partial sum an integer below 2²⁴,
so exact in binary32 in any association order a driver chooses — and scales by
2⁻²³, exact because it is a power of two. `points_test.go` holds all thirty-two
rows to that round trip.

*The box.* Placement is unchanged, so two points of one phase group can never be
the same pixel: a repeat is placed a phase later by construction. The group's
texels are pairwise distinct and one quad brightens each exactly once. An
uncovered texel is zero, whose lane is zero, whose fragment is `(1,1,1,0)`: the
blend forms `dst × 1` and writes the destination back unchanged. That is what
lets one quad cover a whole box, including pixels other commands of the same
phase own, and the box is the union rectangle the per-pixel placements already
grew the phase's destination rectangle to.

The atlas is grouped by phase *number* rather than by scanning a small fixed set
of groups, because a late battle frame's discs overlap each other and each
other's earlier phases and one batch spreads over dozens of phases; and its
regions are shelved by height class, because one shelf shared by every height
made a shelf of ordinary discs as tall as the one tall region on it and wanted
about five times the area its regions needed.

**The recorder's cached lane.** `replaceCachedGeometry` copied the frame-scratch
packet out with `ModelGeometry.Clone` — a fresh packet, four fresh slices and
one fresh vertex slice per face — on every rebuild, and under Enhanced
interpolation every mobile subject rebuilds every presented frame because its
blended pose genuinely moves (§13.5). Each cached body now keeps its own face
and vertex arenas and the packet is copied into them with the same
`copyModelFaces` the rebase path uses.

*Measured*, 1080p Ashap Plateau seed 7, `--benchmark-tps=120`, modern, medians
over three interleaved runs of each build:

| | 180 frames before / after | 720 frames before / after |
|---|---|---|
| Record | 3.12 / 3.05 ms | 4.96 / 4.81 ms |
| Submit | 3.98 / 2.63 ms | 12.11 / 4.48 ms |
| Cadence median | 9.02 / 8.34 ms | 20.78 / 13.16 ms |
| On the 8.3 ms floor | 40% / 70% | 11% / 23% |
| Worst cadence | 41 / 40 ms | 240 / 124 ms |
| Allocation per frame | 2.24 MB, 10.1k objects / 1.41 MB, 8.2k objects | 4.37 MB, 21.3k objects / 1.90 MB, 19.3k objects |
| Point quads per frame | — / 127 | 150,185 / 1,649 |
| Vertices per frame | — / 12,038 | 614,714 / 21,016 |

Draws, phases and passes per frame are unchanged (30/20/13 at 180 frames,
54/53/45 at 720): this round moved the vertices, not the device calls. Classic
is unchanged at 11.2 ms Record with byte-identical captures.

**Dropped.** Merging device runs across phase boundaries (§13.3 makes a segment
one render pass, so consecutive runs of identical state could be one call) was
dropped: the frame already issues only 30–54 draws and `DrawTrianglesShader32`
fell to under 5% once the point vertices went, so a per-segment vertex arena
would risk the byte gate for under 2%.

Deferring the **model overflow barrier** was dropped and is a finding. A subject
the slot atlas cannot fit rasterizes into a fallback page at commit time, and
because the page is reused, that has always been a scheduler barrier — one per
overflowing subject, a median eight per frame. A ring of fallback pages that
lets a body and a shadow reserve their two pages together, and defers the
barrier until the ring is exhausted, was implemented and measured. With a ring
of two — one barrier per subject, the behaviour it replaces — the frame is
byte-identical, so the ring itself is sound. With a ring of four the phase count
falls from 53 to 47 and **the composed frame changes**, while Submit gets
slightly *worse* (4.48 → 4.66 ms median). Merging two segments across a model
overflow therefore changes composited pixels for a reason the placement rules do
not explain, in the same family as the unexplained shadow pixels of the
`TODO(H1)` two-surface alternation (§11.5 status). It is not a performance
lever, and the barrier stays.

**What remains.** The frame is now about evenly split between Record (4.8 ms)
and Submit (4.5 ms) at 720 frames. Above 2% of the process: `runtime.madvise`
at 17%, which is page commits for the 19,000 objects a frame still allocates —
those are Ebitengine's per-Metal-call boxing and `internal/render`'s
`reuseDrawSlice`, not the executor, which allocates only its atlas growth;
`runtime.cgocall` at 11%, the Metal calls themselves on the render thread; and
`unitGeometryPair` at 12%, now spread thinly over `collectDrawPolys`,
`unitDrawFor`, `borrowRebasedModelGeometry` and `configureModelGeometry` with no
single item dominant.

**Cadence spikes.** They are not periodic and not the collector: over a 720-frame
run the Go collector ran twice for 0.9 ms of total pause before this round and
once for 0.08 ms after. The spikes track the effect census — the runs of frames
above 60 ms are consecutive frames at 200–230 live effects and 25–37 fragments,
which is the lit point volume — and this round removed them, from 52 frames
above 60 ms to 3. The one that remains, 124 ms at the frame with 963,000 covered
pixels, is the frame where the plane atlas grows: a new image, a fresh staging
buffer and an upload of every row it uses.

### 13.9 Two-stage unit recording (fifth round)

§13.8 left Record and Submit about even at 4.8 and 4.5 ms over 720 frames, with
`unitGeometryPair` the largest single item inside Record and no dominant piece
within it. That shape is the argument for splitting the work rather than
shaving it: a unit's piece transforms, projection, material resolution, polygon
collection and cached-lane rebase read the committed frame and the unit's own
retained body and touch nothing another unit's does.

**The split.** Recording a frame's units is two stages.

*Stage one* runs after the bucket build, over a persistent pool sized to
`runtime.NumCPU()` participants — the recording goroutine plus `NumCPU()-1`
workers parked on a wake channel between frames, because the frame budget is
8.3 ms and a goroutine per unit per frame would spend a visible part of it on
the scheduler. Its job list is every unit the two passes will present on their
own: the pass-A window and mover-mode split of [03 R-RAST-01 §7] and then
`presentUnit`'s strategic-view, carrier-link and model-name gates, resolved
once so both passes read one slot array. Each job computes the unit's geometry
pair into a slot indexed by that unit's position in the committed unit slice.
Participants take jobs from a shared cursor: per-unit cost varies by an order
of magnitude, so a shared cursor balances better than a fixed stripe.

*Stage two* is the unchanged sequential walk. It visits the buckets in exactly
the order [03 R-RAST-01 §7] fixes and appends the Model commands from those
slots. **Nothing is read from a slot until stage two reaches that unit's place
in the bucket order**, so completion order cannot reach the recorded list and
the list is identical whatever the worker count [I1]. A slot carries the
presentation identity it was computed for and stage two checks it, so a slot
that does not belong to the subject in hand is rebuilt inline rather than used.

**Shared state.** Each worker records through a shallow copy of the recording
client that shares every immutable and read-only field with it and owns its own
model scratch arena, so two workers never receive the same borrowed slot; the
arena is carried across the per-frame refresh, so a steady-state frame still
allocates nothing per unit. Writes a worker makes to its own copy are dropped,
which is safe only because stage one is a pure function of the committed frame
and of cache entries that already exist: every orientation and cached-body map
entry a job can reach is created on the recording goroutine **before** stage
one starts, so workers read those maps and write only through the pointers the
entries hold. It is the insertion, not the entry, that cannot be concurrent —
distinct units own distinct entries. An entry with neither geometry nor image
is indistinguishable from a missing one at every read, so pre-creating one
changes no decision. The per-worker name memo lives in the arena and is a
memo of `strings.ToLower`, so a worker that has not seen a name recomputes the
same string.

Three lanes stay sequential and are named here rather than left to be
rediscovered. **The classic composer** keeps the whole sequential path: its
per-unit work rasterizes palette planes through a different set of borrowed
slots, and the classic benchmark measured no regression from leaving it alone.
**Attached children** are computed in stage two, because a child's forced key
plane depends on whether its carrier turned out to have one — which is known
only after the carrier's own pair exists; a carrier's own pair is the first
geometry call of its present in both branches, so it is precomputed like any
other unit. **A standalone model registry** is excluded, because it loads and
binds models on first use, and that is a write to shared registry maps; a
battle registry has every model bound before the first frame. A parity trace
sink is excluded as a diagnostic path.

**Measured** (1080p Ashap Plateau seed 7, `--benchmark-tps=120`, modern, three
interleaved runs per configuration, medians of the per-run medians):

| | 180 frames before | 180 after | 720 before | 720 after |
|---|---|---|---|---|
| Record | 3.07 ms | 1.16 ms | 4.85 ms | 2.30 ms |
| Submit | 2.65 ms | 2.69 ms | 4.48 ms | 4.60 ms |
| Cadence | 8.33 ms | 8.33 ms | 13.40 ms | 10.90 ms |
| On the 8.3 ms floor | 67% | 71% | 22% | 29% |
| Allocation | 1.5 MB, 8.2k objects | 1.6 MB, 8.2k objects | 1.9 MB, 19.3k objects | 2.1 MB, 19.3k objects |

Record is the whole of the gain: it more than halves, and at 720 frames the
cadence median falls with it. **Submit did not move.** The interleaved batch
above put it 0.1–0.3 ms higher in all three pairs, which reads like worker
threads competing with Ebitengine's render thread for cores; a later run of the
finished build, taken at a higher host load, put Submit at 4.45 ms against the
baseline's 4.48. The rise is therefore host-load noise on a shared machine, not
a cost of the split, and the number to carry forward is "unchanged". The
competition is real in one measurable way: the process keeps 96% of its sample
budget busy where the sequential build kept 78%, and stage one costs more
*total* CPU than the sequential build did (1.2 s → 1.8 s of samples over a
720-frame run) while costing less wall time on the frame's critical path.
Allocation per frame is unchanged in object count and about 8% higher in bytes,
which is the workers' arenas each carrying their own high-water mark.

Classic is unaffected: 10.89 → 11.09 ms Record at 30 TPS, allocation identical,
capture byte-identical. Both battle captures are byte-identical at 180 and 720
frames on every run, and M1–M8 are byte-identical to main on **both** renderers,
which is the check that matters here because the recorder is shared.

### 13.10 The record/submit pipeline (sixth round)

§13.9 left Record at 2.3 ms and Submit at 4.6 ms over 720 frames, both on the
game goroutine, one after the other, inside an 8.3 ms period. This round takes
Record off that critical path entirely rather than making it smaller.

**The idle window.** Ebitengine runs our Update and Draw on the game goroutine
and encodes to the device on a render thread of its own. `Execute` only
*enqueues*: the device's vertices are copied at enqueue, so by the time it
returns the client's draw list and its scratch arenas are nobody's. With VSync
on — the window and the benchmark both set it — the end-of-frame flush is then
**synchronous**: after our Draw returns, the game goroutine sits in the flush
and the swap until the next Update. At the Enhanced rate that idle window is
the remainder of the period, several milliseconds, every frame.

**The shape.** At the end of a modern Draw, after `Execute` and the screen
blit, the client records the **next** frame on one persistent goroutine. The
game goroutine joins that record before it touches client state again — at the
top of Update, before input, and at the common entry to the next Draw, for
both executors. An executor switch cancels the pending speculative record,
including its saved presentation-CRT state, before classic can advance
presentation. The modern Draw tail rechecks the executor after its deferred
Update before launching another record: an F10 handled there may have selected
classic. Recording therefore owns the client for exactly the span the game
goroutine spends in the window layer. The §13.9 worker pool runs
inside it as before, so the pre-record is itself parallel.

Nothing is double-buffered. The list, the point and surface arenas and the
model scratch are reused in place, because the frame that used them has already
been enqueued and copied.

**When the pre-recorded list may be presented.** Only when it is the list a
synchronous record would have produced at this Draw. That is decided by
comparing a `PresentationInputs` digest taken at the launch against one taken
at the Draw that would consume it. The digest is deliberately not a field list
of everything the recorder reads — that list is most of the client and would
drift out of date behind it. It is:

- **the host's mutation epoch**, bumped once per window Update and once per
  benchmark step, which is every point where the host writes client state:
  input and its selection, hover, command page, minimap viewport, pointer
  capture and focus; the simulation step and its publication; the camera the
  scroll pass and the follow glide moved; the executor toggle;
- **the committed frame**, by pointer identity and tick, as a cross-check on
  the epoch;
- **the two blend fractions of §13.5** and whether the camera's is set at all;
- **the camera origin and its two stepped samples**, the surface size, and the
  interpolation and Enhanced switches;
- **the caption ring's producer and display cursors**, because the audio drain
  is the one thing that runs on the game goroutine between a launch and the
  Draw that consumes it, and the ring is what it writes that the recorder reads
  [07 R-HUD-03 §14];
- **the displayed resource pair**, predicted purely for the next presentation
  when launching, compared against the pair advanced by the consuming Draw
  [05 R-ECO-01 §6][07 R-HUD-03 §4].

A mismatch discards the list and records synchronously exactly as before.

**What stayed on the game goroutine.** The audio step —
`UpdateAudioViewportFromCamera` and `TickAudio` — is called from Draw on every
presented frame whether the list was pre-recorded or not, so its cadence and
its thread are unchanged [03 §8.3] C18. It moved *ahead* of the recording pass
rather than into it: `recordFrame` is now the host presentation advance followed by
`recordFrameNoAudio`, and a pre-recorded list was recorded after the previous
frame's drain rather than after this one's. That is why the ring cursors are in
the digest — a drain that wrote a caption discards the pre-record. Resolving
the blend fraction moved with it, into `ResolveTickFraction`, so the digest and
the record read one sample of a wall-clock producer rather than two. The cursor
blit resolves its art and visibility inside the recording pass, covered by the
epoch. The window then replaces only that command's coordinates with the latest
Ebitengine pointer snapshot before replay (§13.5), without publishing input or
changing the recorded world and interface. This also applies to the paused
foreground. No mutation overlaps the pre-record worker.

**Displayed stocks share the host boundary.** `BeginPresentationFrame` steps
the retained stock pair and drains presentation audio once per presented frame,
before `TakePreRecord`. `Frame` and `RecordFrame` call that boundary; the modern
host calls it explicitly. `RecordModernFrame`, `ComposeFrame` and snapshots do
not advance it. The pre-record worker selects a pure next-step value for
`UIFrame.Resources`; it never writes the retained pair. Launch records that
same prediction in the digest. Stock changes therefore continue to hit when
prediction and presentation agree, including multiple frames on one committed
tick. Retried or discarded records need no stock rollback, and a changed
viewer or publication remains covered by the ordinary mutation/frame checks.
The `--shot` entry advances once before either or both executors compose.

**What a discarded record must undo.** A recording pass writes presentation
state, and a discarded one must not leave it advanced twice. Almost all of it
is safe already: the list and arenas are reset by the next pass, the blended
view is rebuilt from scratch, the lazy art caches are memos, and the trail
layer and the feature animation cursors are both guarded against advancing
twice within one committed tick. The exception is the presentation CRT the
segmented projectile pass draws from, which is a stream; the launch snapshots
it and a discard puts it back [03 §2.4.1][I4].

**Benchmark pacing and measurement.** Harness version 2 runs every Draw callback
with Ebitengine updates synchronized to drawing and one explicit host deadline.
If a draw arrives late, the next deadline is based on its arrival; the harness
never catches up with a burst of unpaced draws. A fixed draw-to-tick ratio keeps
30 authoritative ticks per target second at every supported presentation rate;
falling below the target slows wall-clock battle progression without changing
the measured tick sequence [I6]. Renderer warmup covers two simulated seconds.

The cadence timestamp is taken before simulation, after the intentional pacing
wait. The first measured interval is invalid because profiling setup separates
it from warmup. Reports distinguish complete host callback work, explicit pacing
sleep, and time outside the previous callback (including deferred driver work,
queue/display waits and scheduling). The public Ebitengine API exposes neither
GPU completion nor actual presentation timestamps; both are recorded as
unavailable. A low Submit timer plus a long cadence cannot establish GPU
saturation. See BATTLE_BENCHMARK.md for the field meanings and compatibility.

**Predicting the fractions.** The benchmark knows the next frame exactly: the
group's next fraction is `(phase+1)/drawsPerTick`, with one authoritative
tick per `FPS/30` draws. At 60 FPS one of two draws can hit; at 120 FPS three
of four can hit. A draw that publishes a tick records in place, so no pre-record
is launched across it. The comparison is **exact** —
zero tolerance — so a measured frame is byte-identical to a synchronous record.

The window predicts from the measured present interval: the camera fraction is
where the next Draw will sit in the current update, and the tick fraction is
extrapolated over the interval that remains at the rate the last two samples
measured. Neither prediction declines at the end of its range; both take the
client's own clamp, because that is where the measured value of the last
presented frame of a period will be too. The classic executor and `--shot`
never launch a pre-record at all.

**The window presents at the fraction it predicted.** When a pre-recorded
list's digest matches on every field but the two fractions, the window presents
it *at the fractions it was recorded for* rather than re-recording it at the
measured ones — it accepts the prediction. That is the pipeline's one
presentation divergence, and it is now stated as what it is: **a pre-recorded
frame is presented at the instant it was predicted for, and the error is
bounded by present jitter and capped at one present interval.** The cap is the
guard rail. The tolerance is computed per Draw as `period × 30 × 65536` quanta,
the amount both fractions advance across one present, and the period is the one
the window **nominally** presents at — the `--fps` cap, the display's own rate,
or the wider of the two, with the last launch's measurement as a last resort.
It is deliberately not the interval this particular prediction was extrapolated
over: a frame that hitched measures a long period, and a tolerance computed
from that period would widen by exactly the lateness it exists to catch. So a
frame that arrived a whole refresh late is further out than any jitter and
takes the exact path instead.

Measured over 4,800 presented frames of a live skirmish, the error the window
actually presents is far inside the guard rail: **mean 1,638 quanta (0.025 of a
tick, 0.8 ms) and a maximum of 20,658 (0.32 of a tick)** against a cap of
32,768 at 60 presented frames per second. The typical error is a single step of
the millisecond producer, which is what the old 2048-quantum tolerance was
sized against and could not meet.

The first form of this rule was a fixed tolerance of 2048 quanta, one
thirty-second of a tick, sized to sit *below* the fraction producer's own
resolution. That could not work. The battle's tick fraction reads a millisecond
source, so at the nominal speed it moves in steps of about 1966 quanta; a
tolerance of 2048 is one step of the number being compared, and one millisecond
of draw jitter — an eighth of a 120 Hz present — puts the measured value in a
neighbouring bucket. Almost every launched window frame missed on
`MissTickFraction` for that reason. A tolerance smaller than a producer's
quantisation cannot be met by a prediction of that producer; the choice is
between accepting the prediction and never pre-recording at all.

**The update body runs in the Draw's idle window.** A pre-record can only serve
a frame if every client write that frame reads happened before the launch, and
the launch is at the end of the *previous* Draw. An Ebitengine Update — input,
the camera the scroll pass moves, the step and its publication — is exactly
such a write, so a frame with an Update in front of it used to be a frame no
pre-record could serve; roughly half of the window's frames never launched for
that reason.

Ebitengine runs a frame as *(zero or more Updates) → Draw → flush and swap*,
and takes a fresh input snapshot immediately before each Update it calls; a
frame with no Update leaves the game-visible input state exactly as the last
tick saw it. The body of an update therefore moves to **the end of the modern
Draw that the Update call precedes**, after `Execute` and before the launch. An
`updateLedger` counts every Update call and guarantees one body per call: the
call defers when the modern executor's Draw tail is alive and nothing is owed
already, and otherwise runs every owed body inline, so the simulation can
neither step twice for one update period nor skip one however the window
behaves. The classic executor never defers; a Draw skipped by `--fps` runs no
tail, and the next Update call runs the body itself. An exit request seen from
a tail cannot return a Termination, so it is recorded and the next Update call
returns it.

Every frame then launches, and the frame that follows an update is the same
kind of frame as any other.

*What it costs is one presented frame of input latency, and that is the floor
rather than an accident of this design.* A list recorded during the previous
frame's flush cannot contain input that arrived after that flush began, so a
pre-recorded frame is always one present behind live input; the body's writes
first reach the screen on the Draw after the one that ran them. Running the
body *early* instead — before the Update call it belongs to, as the first
sketch of this round proposed — is strictly worse: Ebitengine refreshes the
game-visible input snapshot per tick and not per frame, so an early body would
read the previous tick's snapshot, costing a whole update period rather than a
present, and would consume one snapshot twice on entering the regime.

The blend absorbs the shift. The frame that used to present the new tick at
fraction 0 now presents the previous pair at a fraction clamped just under 1,
and `prev + (cur − prev)·f` at `f` just under one is the same pose as the new
pair at zero: the sequence of presented positions is unchanged, only its
labelling.

**Measured** (1080p Ashap Plateau seed 7, `--benchmark-tps=120`, modern, six
interleaved pairs per frame count, medians of the per-run medians; the
untouched branch is the baseline, rebuilt for this round because unit
supersampling had landed):

| | 180 frames before | 180 after | 720 before | 720 after |
|---|---|---|---|---|
| Record (game goroutine) | 1.30 ms | 0.003 ms | 2.41 ms | 0.25 ms |
| PreRecord (off the path) | — | 1.32 ms | — | 1.81 ms |
| Submit | 2.95 ms | 3.00 ms | 4.87 ms | 4.89 ms |
| Cadence | 8.34 ms | 8.33 ms | 9.92 ms | 9.00 ms |
| On the 8.3 ms floor | 74% | 73% | 33% | 40% |
| Pipeline hits | — | 75% | — | 75% |
| Allocation | 1.1–1.6 MB, 8.0k objects | 1.3–1.6 MB, 8.0k objects | 1.90 MB, 12.6k objects | 1.85 MB, 12.6k objects |

Record is gone from the critical path: on a hit it is the join, and the join is
three microseconds because the record finished during the previous frame's
flush. **Where that buys cadence depends on whether there was headroom to buy.**
At 180 frames the scene is light, the run already sits on the 8.3 ms floor, and
removing 1.3 ms of CPU changes nothing measurable — the on-floor share moves
within the baseline's own run-to-run spread, which was 55–76% across six
baseline runs. At 720 frames the battle is heavy and the cadence median falls
0.9 ms with the on-floor share up eight points, consistently across all six
pairs. Submit did not move.

**Window, measured after the two changes above** (live skirmish, Ashap Plateau
seed 7, modern, 1.5× window at 60 presented frames per second, the readout the
window prints every 600 presented frames; the baseline is main's own build run
back to back with it on the same host):

| | before | after |
|---|---|---|
| Pre-recorded frames | 24% steady state | **99.8%** |
| Never launched (the prediction declined) | 47% | 0.1% |
| Missed on the tick fraction | 22% | 0.02% |
| Presented drift, mean / max | — | 1,638 / 20,658 quanta (0.025 / 0.32 tick) |
| Update bodies per second | 30.0 | 30.00 |
| Committed ticks per second | 30.0 | 30.00 |

The benchmark is unmoved by either change, which is the point of it here: it
passes tolerance zero and steps inside its own Draw, so its rows and its
`battle.png` are the regression gate rather than the result. Over three
interleaved pairs at each frame count its medians are within run-to-run spread
(180 frames: Record 0.003 ms both, Submit 3.07 → 3.10 ms, cadence 8.33 ms both;
720 frames: Record 0.14 → 0.23 ms, Submit 5.07 → 5.10 ms, cadence 9.07 →
9.10 ms), and `battle.png` is byte-identical on every run.

The paragraph this replaces recorded the problem, and is kept because it is the
measurement that motivated both changes:

**Window hit rate is much lower than the benchmark's: 20%** over three thousand
presented frames of a live skirmish. Roughly half the frames decline to launch
because the predicted Draw would cross an Update, and most of the rest miss on
the tick fraction, where the producer's millisecond quantisation puts the
prediction in a neighbouring bucket. The benchmark's rate is the ceiling — it
knows the next fraction rather than guessing it — and closing the window's gap
means a finer fraction source, or a prediction that rounds to the producer's
own quantisation, neither of which this round attempted.

**Verification.** `battle.png` is byte-identical to the untouched branch at 180
and 720 frames on every run, which is the check that matters: the benchmark's
tolerance is zero, so any list presented from the pipeline was the list a
synchronous record would have produced. M1–M8 are byte-identical to the
baseline on **both** renderers. `go test -race` over the client and window
packages is clean, and a race-built binary through a 180-frame modern benchmark
reports no race and the same capture.

#### Paused world reuse

The modern window retains the completed world through its fog barrier while
paused. The retained result is one framebuffer-sized GPU colour image; there
is no retained duplicate draw list or model geometry. A matching world skips
recording, interpolation rebuild, model-cache pruning, and world GPU replay.
The ordinary foreground still records and executes every presented frame:
drag selection, strategic markers, HUD, messages, modal panels, build previews,
order overlays and the software cursor. Restoring the world image before that
foreground preserves destination-reading shade/overlay operations and removes
the previous cursor or gesture. Original and `--shot` keep their full-frame
paths.

Pause truth comes directly from the scheduling bridge's `SetPaused` result;
`Frame.Paused` alone is insufficient because a stopped scheduler publishes no
new tick. Battle attach/restore installs the scheduler's truth after binding
the snapshot. Replacing the snapshot clears this mirror. Unpause and executor
switches discard the retained image; resizing replaces its allocation.

`PausedWorldInputs` compares the committed frame identity and tick, frozen tick
fraction, interpolation and Enhanced switches, actual blended camera origin,
viewport/map extents, scale and smooth zoom factor, dimensions, world/asset
binding revision, terrain, detail art, font, palette and display colours, and
the shadow, shading, antialias, fog and damage-bar options. The actual origin
uses §13.5's existing integer blend and teleport snap, so a stationary camera
can reuse across changing camera fractions, while pan/follow/zoom redraw at
their existing cadence. Unit flags, selection and group labels, fog, features,
and model-texture animation remain owned by committed publication and the
stopped phase-7 service. Immutable asset rebinding invalidates the key. The
host mutation epoch is deliberately absent here: input changes covered by that
epoch are rendered freshly in the foreground.

The per-present randomized segmented-projectile family is an exception.
Any published member conservatively disables world reuse, even offscreen;
full recording preserves its CRT draws and painter position [03 §5.4][I4].
A renderer trace also disables reuse. No fixed refresh throttle or altered
animation cadence is introduced. Thus this optimization is not a promise of
minimal paused CPU for every possible scene.

Entering the split joins and cancels speculative recording with the ordinary
CRT rollback. Paused Draws never launch another pre-record, including the
random-projectile fallback. Audio draining and displayed-resource advancement
still run once per presentation before the split, and every Draw still reaches
the existing update-ledger tail. On resume the normal pipeline starts again.
Opt-in `--stats` diagnostics count world recordings and reuses; foregrounds
remain in the ordinary presented-frame and update-body cadence totals.
Periodic and exit pipeline/cadence/cache readouts are disabled by default.
F11's renderer metadata retains pipeline and paused-world counters regardless
of the terminal-statistics setting.

Verification: the client regression compares whole and split indexed rasters,
including a moved/removed gesture and destination-reading UI; key tests cover
stationary and moving camera fractions, zoom, settings and assets, publication,
unpause and session replacement, and speculative CRT rollback. The real-device
fixture compares exact RGBA bytes of whole replay against retained-world replay
with moving/removable foreground and modal shading over model/shadow content.
Live pause/pan/zoom/capture/resume and paired battle performance checks remain
the integration gate; the old paused-process profile is observational evidence,
not a controlled timing baseline for this newer source.

### 13.11 Flash quads and same-stream phases (seventh round)

Measured on the 720-frame modern battle benchmark at 1920×1080 with about 190
units, Submit is all CPU on the game goroutine and its scaling term is the
effect layer. At about 200 live effects the executor writes around a million
lit-point texels per frame; Submit's correlation with the frame's vertex count
is 0.93, and the phase count reaches 113–121 because overlapping
destination-reading commands split phases. Two changes follow. The first is
exact and its captures are byte-identical; the second is an Enhanced-only
approximation the user approved on 2026-09-10 — the modern executor does not
have to match retail's lit brightening of flashes and halos exactly, it has to
look similar. Classic output is byte-identical either way.

**The same-stream rule.** Until this round a destination-reading command opened
the next phase whenever it overlapped another destination-reading command of the
current phase (§11.2). That rule was written when a destination read meant
sampling a per-phase snapshot copy. Since §13.3 it does not: the row families
(`FillLitRect`, `FillShadeRect`, `PointLit`, the trail marks) and the ALP
families (`BlitTinted`, the translucent feature body and shadow, the model
shadow commit, the strategic marker layer) hand the device a fragment and a
fixed-function blend, and the blend is a read-modify-write of the real
attachment. A pass applies its fragments in primitive order, and inside a
phase's destination batch that order **is** record order — runs are appended in
record order and a run's vertices are its commands in record order. Two
overlapping commands of the same blend stream therefore leave exactly the
per-pixel sequence the phase split left.

A destination-reading command now opens the next phase only when

* it overlaps an earlier destination-reading command of a **different** stream,
  or
* either command samples the phase's read copy in its shader.

The streams are the ALP half-blend, the row-family scale blend, and the
read-copy stream. The last has exactly one member: **fog** is the only family
that still binds a read slot (§13.3 "Fog keeps one read copy"), so it splits
from every destination command it overlaps in both directions — the copy is
taken once per phase, and a second command over the same region would read a
state the copy no longer describes. Cross-stream pairs keep the split rather
than an argument about commuting two different pieces of arithmetic. Every other
rule stands: an opaque write over an earlier destination read still opens the
next phase, a lit point still follows any earlier point at its own pixel by a
phase, and the cell grid's forgotten-owner floor stays conservative — it is now
kept per query stream, because what a forgotten owner costs depends on who asks.
A command's stream is derived from the blend and read slot it already binds, so
no family outside the scheduler changed.

*Measured*, 1080p Ashap Plateau seed 7, `--benchmark-tps=120`, modern, phases
per frame: 20 median / 26 max before, 14 / 16 after at 180 frames; 42 / 112
before, 19 / 64 after at 720 frames. M1–M8 and `battle.png` at 180 and 720
frames are byte-identical on both renderers.

**The lit discs as quads.** An explosion's calculated disc [06 R-WFX-01 §2] and
an effect's ground halo [03 §4.3.1] are the same operation the row families
already express — every covered pixel folded through one LHT row — and the
recorder was carrying each of them as one lit point per covered SCREEN pixel.
That is where the million texels a frame came from. Each disc becomes one
command instead:

* `drawlist.Flash` carries the generated frame's identity (table and clamped
  frame index), its `Side` and `Offset`, its texels resolved to LHT rows with
  `FlashTransparentRow` outside the disc, the screen anchor, the view scale and
  the **gate as a rectangle**. The gate is what `terrainScreenCoverage` already
  is: two independent half-open range tests against the map, so the admitted set
  is the map rectangle in screen space and no executor needs a callback.
* `drawlist.Halo` carries the centre, the radius already taken through the view
  scale, the LHT row and the same rectangle.

Both are part of the `Sink` contract rather than an optional hook, because
unlike the trail marks every executor must draw them. The **classic recording
lane is unchanged**: it still emits the points, so classic output is
byte-identical by construction. The classic *sink* implements the two commands
by expanding them back into exactly those points — `Flash.Expand` and
`Halo.Expand` are the definition, and a test locks their output against the
points the classic lane records at all three view scales — so a modern-lane list
replayed through the classic executor composes the same bytes.

In the modern executor a generated frame is packed on first use into one
persistent RGBA8 intensity atlas (`flash.go`), one page of 1024², which is
enough for all three tables at once. A texel stores exactly what a lit point
plane texel stores — the row family's high lane as a 24-bit fixed-point triple,
from the same `pointLaneBytes` table — so the two paths run the same fragment op
(`destOpLaneAtlas`) and the brightening per row cannot drift between them; an
uncovered texel is zero, whose fragment is the identity scale, so the disc's
transparent ring needs no key test and each frame gets a one-texel identity
border for the magnified sampler. The flash is then one quad over its projected
extent, the halo one quad whose fragment recovers the integer `(dx, dy)` from
its interpolated corner lanes and runs the byte writer's own `dx² + dy² ≤ r²`
test. Both are row-stream commands, so under the rule above they never split a
phase among themselves or against the trails and the fills.

**Divergences (Enhanced only).** The user decided on 2026-09-10 that the modern
executor need not match retail exactly here, only look the same.

* The disc is texture-sampled rather than magnified by a per-source-pixel loop.
  At the native and detail scales that is the same pixel set — the loop's span
  for source pixel `c` is `[Project(c−Offset), Project(c−Offset+1))`, which is
  one and two screen pixels exactly — and both were **measured byte-identical**.
  At the 1.5× step the loop's alternating one- and two-wide columns become
  nearest-sample columns, which shifts some ramp columns by a pixel.
  `battle.png` at 180 frames, `--zoom 1.5`: 11,627 of 2,073,600 pixels differ
  (0.56%), mean channel difference 14.7 inside them and maximum 184, and every
  differing pixel lies inside a disc footprint. The maximum is what a pixel
  gaining or losing the brightest ring costs (`dst × 2` against `dst × 1`), not
  a lane error; the halo has no texture and stays exact at every scale.
* Flash and halo pixels that also fall under a later opaque command are still
  overwritten, exactly as before.
* The shader's per-fragment lane and the plane's stored lane are the same bytes
  by construction, not by argument: both read `pointLaneBytes`. The measured
  zero-pixel difference at 1× and 2× is the check.

*Measured*, 1080p Ashap Plateau seed 7, `--benchmark-tps=120`, modern, medians
over three interleaved 720-frame runs of each build:

| | before | after |
|---|---|---|
| Submit | 4.95 ms | 3.24 ms |
| PreRecord | 1.80 ms | 1.26 ms |
| Cadence median | 9.05 ms | 8.34 ms (the 8.3 ms floor) |
| Worst cadence | 127 ms (329 ms on one run) | 40 ms |
| Phases per frame | 42 median, 112 max | 10 median, 14 max |
| Vertices per frame | 22,500 median, 1,186,272 max | 14,362 median, 18,076 max |
| Lit point pixels | 181,538 median, 963,506 max | 0 |
| Lit discs (`Flashes`) | — | 49.5 median, 90 max |
| Allocation per frame | 1.96 MB | 0.75 MB |

Passes (13) and draws (27) are unchanged: this round moved the vertices and the
phases, not the device calls.

**The lit point plane now serves nothing in the modern lane.** `PointLit` had
exactly two producers, the flash disc and the halo, and both are lit-disc
commands now; the benchmark's `PointPixels` is zero on every frame. `points.go`,
`drawLitPoints` and the `PointLit` path stay in place — they are the classic
sink's own family, the fallback for any future `PointLit` producer, and the
definition the disc atlas's lane encoding is checked against — but nothing in
the modern executor exercises them today, and a later round may retire the
per-frame plane atlas if none appears.

### 13.12 Persistent model slots (eighth round)

> **Retired (2026-09-11).** Persistent slots went with the slot stage; the
> model lane of §22 rasterizes every subject each frame into a per-frame
> atlas and needs no residency. Kept as the record of what was measured.

**The problem.** On the 720-frame modern battle benchmark (1920×1080, ~190
units, 120 TPS) `Submit` sat at a 4.9 ms median, of which `prepareModelSlots`
was 2.8 ms median and 3.7 ms p95 — and **flat across the scene**: it did not
move with the effect count, the projectile count or the fire count. The reason
is that the slot atlas was a per-frame product. Every frame reset the shared
page and then re-placed, re-analysed (`modelFacesSupported`) and re-prepared
(`prepareModelFaces`) every subject before rasterizing all of them, although
most subjects are the same retained body the recorder already caches: a
structure, an idle unit, a moving unit between orientation changes. About
1.2 ms of the 2.8 was the page flush — the vertex build and the raster passes —
and the rest was the `VisitModels` walk.

**The decision.** A slot stays where it is until its raster inputs change. A
frame analyses and rasterizes only the subjects it could not find already
resident, and the flat term becomes proportional to what changed.

#### Identity — contract P1

The executor sees a per-frame scratch packet, never the retained body pointer,
so it cannot key on the pointer. `drawlist.ModelCacheKey` is what the recorder
stamps on the packet instead:

* `Body` — a serial the recorder gives a retained `cachedModelBody` the first
  time it stores geometry, unique for the life of the process. It is not the
  presentation identity: `InvalidateModelImages` drops every body at once and
  the replacements would otherwise present an identity the executor still holds
  a raster for.
* `Revision` — incremented on every `replaceCachedGeometry`, which is the one
  place the cached lane is stored. An equal `(Body, Revision)` means literally
  the same retained faces.
* `HalfX`, `HalfY` — the frame's half-pixel offset. The rebase adds it to the
  **doubled** corners alone (§17), so the packet's own origin does not imply it.
  There are four of them, so a moving supersampled subject cycles through four
  rasters and residency holds the ones it is using.
* `Lane` — which of the retained object's rasters this is, body or shadow. The
  two share the serial and keep separate revisions; see "Shadows — contract P4"
  below, which added the field.

A zero `Body` means "not reusable", and it is the value of every packet whose
raster inputs the recorder cannot prove stable: the direct projected lanes,
children, a shadow whose subject has no retained body (P4), and any retained
body carrying a reveal, an outline or a live lane this frame. The recorder
clears the key for those explicitly rather than relying on the executor to
notice.

The executor's slot key adds what it reads off the packet itself: `Width`,
`Height`, `OriginX`, `OriginY` (which fix the rebase delta, and with it every
cached corner's position inside the slot), `Scale`, `KeyPlane`, `Waterline`,
`WaterlineKey`, `Digger`, `DiggerKey`, and whether a `Supersample` lane is
present. It refuses a packet outright when a reveal, an outline, a live lane on
either scale, or a child list is present.

**Established by reading, not assumed.** The team texture is folded in: the
recorder rebuilds the cached lane when `unitTeamColor` changes
(`cachedGeometryMustRebuild`), which bumps the revision. An **animated** model
texture is not: the retained lane keeps the `GAFFrame` it was stored with until
some other gate rebuilds it, so the recorder is already showing a frozen frame
there and reuse changes nothing. That is a pre-existing recorder property, not
one this round introduces.

#### Executor-side inputs — contract P2

The slot page holds resolved **colour** since §17, so the executor's own inputs
to the raster invalidate residency. They are held in one comparable value and a
change retires the whole table rather than being compared per slot:

* the **display palette**, counted by a generation the renderer bumps inside
  `SetDisplayPalette`. The battle benchmark offers a palette every frame;
  `SetDisplayPalette` ignores a repeat of the one already installed, so an
  unchanged palette does not disturb residency.
* the **table atlas** and the shade, blue and alpha tables the raster and
  resolve stages bind.
* the **model page limit** test hook, which changes the packing.

The **model texture atlas** is deliberately not one of them: a frame is packed
once per identity and keeps its page and rectangle for its lifetime
(`model_atlas.go`), so a face's source texels never move. Neither is the
framebuffer size: a slot is rasterized in subject-local coordinates and only its
commit is clipped.

#### Residency — contract P3

The page's shelf and pixels persist. Each frame:

1. Entries no frame has claimed for more than `modelSlotIdleFrames` are retired
   and their regions returned to the free list, in entry order.
2. The previous frame's **transient** regions — the subjects that could not be
   kept — are released. Without this the frontier advances once per shadow per
   frame and the page reaches its row bound in a second.
3. A subject whose key is resident keeps its slot: no admission scan, no face
   preparation, no vertex, no draw.
4. Before allocating, protect every resident key the complete frame will use.
   A new subject early in record order cannot evict a hit appearing later.
   Reserve subjects in descending raster height, with stable record-order ties,
   to reduce wasted shelf height. Replay still commits the original painter
   order; the disjoint raster reservations carry no inter-subject ordering.
5. A new subject takes a region from the free list (smallest that fits, lowest
   index breaking the tie), then the shelf frontier, then by evicting the least
   recently used slot this frame does not use (lowest index breaking the tie).
   Split a larger free rectangle into the requested reservation and at most two
   disjoint remainders. On release, join adjacent free regions only when their
   union is rectangular; never span a still-owned slot. A
   frame that still cannot place a subject takes the per-subject fallback route
   and asks the next frame to start from an empty page — which is exactly what a
   full page did before slots persisted.

Nothing here depends on map order: the residency map is looked up and never
ranged, and every choice among candidates is resolved by an ordered scan [I1].

**Measured packing follow-up (2026-09-11).** In the Great Divide scene-4
benchmark at 30 Hz, 1080p, seed 7, the preceding allocator overflowed in 90 of
180 measured frames and discarded all residency on the following frame. The
policy above reduced overflow frames to 45 and raised median slot reuse from
3.3% to 21.9%; median raster pixels fell from 3,188,520 to 2,293,608 and total
allocation from 1.746 to 1.647 MB/frame. Median Submit stayed near 5.6 ms and
98% of frames stayed on cadence. Metadata and every measured simulation census
matched. Device fixtures check reuse under pressure against a cold raster;
the battle captures were inspected and differed in 50 of 2,073,600 pixels,
within the reviewed placement-sensitive raster policy below. This is a packing
and allocation improvement, not evidence of a higher frame-rate ceiling.

Overflow still requests a rebuild. Keeping fragmented residency indefinitely
was measured separately and rejected: it increased overflow fallback work,
allocations and frame time despite a higher hit count. Page size, pixel inputs,
cache invalidation and simulation behavior are unchanged by this follow-up.

The clears follow: only the reservations a frame allocated are cleared, in one
device draw per plane, and a page with nothing carried over clears its whole
used extent as before. The resolved plane is **seeded** from the page's own
finished raster over those same reservations, which is what the clipping
stage's whole-page copy used to leave there — some commits sample a texel past
their box (§16.3 "Sampling"), and seeding per region makes what they find a
property of the region rather than of the page's history.

Two stage changes follow from persistence. The waterline/Digger stage no longer
exchanges the two page planes: that would move every resident raster to the
plane the resolved colours live on. It clips each subject's own rectangle into
the scratch and copies it back, which costs a frame that clips one extra
destination switch (`modelStageMaxPasses` 8 → 9) and nothing on a frame that
does not. And a page that grows now carries its pixels forward into the larger
planes instead of losing every raster it holds.

#### Outcome

Three interleaved 720-frame runs each on a loaded host (`uptime` 4.7–5.6),
after the same-stream phase work of §13.11:

| | §13.11 executor | persistent slots |
|---|---|---|
| `Submit` median | 3.019 ms | **2.335 ms** |
| `Submit` p95 | 3.58 ms | 2.87 ms |
| `RasterPixels` median | 2,060,810 | 1,519,184 |
| model draws median | 27 | 28 |
| destination switches median | 13 | 13 |

Reuse is a **32% median** of the frame's subjects (99 reused against 235
rasterized), with 27 evictions in a median frame, 255 resident slots and no
fallback subject over the whole run. The ceiling is about 43%: shadows are 138
of the ~360 subjects a battle frame reserves and none of them is retained by the
recorder, so every one is rasterized every frame. Retaining the shadow lane
beside the body lifts that ceiling and is the next subsection.

#### Shadows — contract P4

A shadow is a subject like any other — its own slot, its own raster, its own
commit — and it was the one the recorder could not name. `collectShadowPolys`
walked the model and sheared it again every frame, and the packet carried a zero
`Cache`, so the executor rasterized every shadow of every frame.

**The shadow's inputs are not the body's.** This is the whole difficulty, and it
is established by reading rather than assumed. The body's retained lane is the
*cached* piece lane frozen at its last rebuild: the orientation cache holds the
root angles until an axis moves more than seven units, and the live lane is
rebuilt separately. The shadow projects **every** piece from the **current**
pose. So a subject whose retained body is untouched can have a shadow that
genuinely moved — a turret slewing, a factory pad opening — and gating the
shadow on the body's rebuild would freeze a silhouette the body is not freezing.
The two lanes are retained side by side and gated apart.

What the projection does read is exactly:

* the **model**, by name, as the body's own gate reads it;
* the **folded piece pose** (`UnitDraw.PieceStates`). `BuildUnitDrawInto` derives
  every piece's transform, its hidden verdict and therefore its world corners
  from that slice alone, so an equal pose is an equal projection. Comparing
  `len(pieces)` small comparable structs is what replaces the walk, the shear and
  the winding test;
* the **model scale** (`scaleModelLocal`), and whether the §17 **doubled lane** is
  present at all;
* the **shadow gate** — `CastsShadow` with the palette the projection requires.

The subject's **position is not an input**: `PieceDraw.WorldVertices` are the
transformed corners plus `worldPos` and `shadowLocalVertex` subtracts `worldPos`
straight back off in fixed point, so the cancellation is exact. Neither is the
terrain under the unit, the waterline or the Digger clip: those reach the modern
shadow through its *placement* (`shadowPlacement`, whose Y is sheared by
`GroundY` [03 R-REN-03D §3]) and through the body packet's own clip fields, never
through the projected corners. A `DontShadow` piece flag exists in the pose and
is compared with it, though the projection does not yet consult it — a
pre-existing gap this section does not close.

So the retained lane is stored with **no placement at all** — anchor zero,
half-pixel offset zero — and the rebase supplies both, exactly as it does for the
body: the anchor is written on the packet and the half-pixel offset is added to
the doubled corners alone. A draw whose pose does not describe every piece of its
model is outside the derivation above and keeps the per-frame projection.

**Identity.** `drawlist.ModelCacheKey` gains a `Lane`. The shadow shares the
body's serial — it is the same retained object — and keeps a revision of its own,
bumped on every reprojection. The lane is what separates the two: without it a
shadow and a body whose `Width`, `Height`, `Origin`, `Scale` and flags happened
to agree could answer to each other's slot. The executor needed no other change;
the lane rides the key it already compares, and `modelSlotKeyFor` now admits a
shadow on exactly the terms it admits a body. The shadow commit is indifferent to
either slot's provenance: its body punch reads the body slot's *box* on the page,
not how that raster came to be there.

A shadow whose subject has no retained body — no presentation identity, no
scratch arena, the direct projected route — keeps the per-frame projection and a
zero key, as before.

**Outcome.** Three interleaved 720-frame runs each, 1920×1080, 120 TPS, on a
loaded host (`uptime` 4.9–6.2):

| | persistent slots | retained shadows |
|---|---|---|
| `Submit` median | 2.542 ms | **2.219 ms** |
| `PreRecord` median | 1.295 ms | 1.248 ms |
| slot reuse median | 29.3% | **47.8%** |
| `SlotsReused` median | 104 | 164 |
| `SlotsRasterized` median | 248 | 208 |
| `RasterPixels` median | 1,557,314 | 1,327,618 |

Shadow reuse alone is a **47.7% median** (76 of 168 shadow subjects). The
recorder's saving is real but small — `PreRecord` moves about 0.05 ms, because a
reprojecting subject now pays the projection *and* a copy into its store — and
the executor's is the one that matters.

Two costs come with it. The residency table roughly doubles, 247 to 408 resident
slots, and the page is under more pressure: `SlotOverflows` p95 rises from 1 to 4
and body reuse falls from 104 to 88 as shadows compete for the same shelf. The
frame is still well ahead, but the page size is now the binding constraint rather
than the recorder's identity, which is where the next round should look.

**Verification.** B0 the executor before this section, B1 with it. M1–M8
`.modern.png` and `.png` **byte-identical**, and the 2× `--shot` byte-identical.
`battle.png` differs by **1** pixel of 2,073,600 at 180 frames and **8** at 720,
every one an isolated texel at a model's own silhouette edge — the same
placement-rounding residual §13.12's verification names, of the same order as the
3 and 4 recorded there, and for the same reason: a slot that is kept sits where
an earlier frame put it rather than where this frame would have. Both builds are
self-deterministic; three 720-frame runs each produce byte-identical `battle.png`
within a build. `go test -race ./internal/platform/gpurender ./internal/client` is
clean, and `checkModelSlotShadowResidency` joins the opt-in device fixtures: the
same list executed twice reuses every shadow slot for the same bytes, and a
bumped shadow revision rasterizes exactly that shadow.

**Since the model lane's silhouette shadows.** Only a structure's shadow is
projected and retained as above. A Digger or mobile subject's shadow is a
faceless packet (`ModelGeometry.Silhouette`) with the body's box, the shadow
anchor and a clip key, and the executor reads the body's own raster at that
placement (§22): nothing is projected, retained or keyed for it. The pose gate,
the shadow store and the lane in the cache key remain the structure path's.

#### The defect this round exposed, and its fix

Persistent slots first came out **not** byte-identical to the executor they
replaced: 1,567 pixels of 2,073,600 differed on a 720-frame battle capture, all
at model edges, with a one-pixel column along many subjects' right edge. The
isolating experiments, each with reuse disabled so only the packing changed:

| experiment (reuse disabled throughout) | pixels changed |
|---|---|
| the branch's clip and clear restructuring, packing unchanged | 5 |
| every slot translated 32 px across the page | 1 |
| slot gutter widened from 2 to 8, then to 16 | 16, then 266 |
| **only the page's allocated height forced from 768 to 1280 rows** | **67,166** |

The last row is the mechanism, and it is not a packing question at all: with the
packing, the record and every subject untouched, making the page's planes
*taller* moved three per cent of the frame. An Ebitengine image is by default a
**region of a shared texture**, and which region it gets depends on its own size
and on what else is on that atlas. The model passes address a page by whole page
pixels — the body pass reads the key stored at its own destination texel, the
resolve reads the two-by-two block under a native pixel, a commit samples its
slot's box — and that addressing does not survive the page moving. The first
hint was a capture whose page planes were read back with `ReadPixels`, which
takes an image off the atlas: it agreed with the tall page instead of the short
one.

The fix is one line of allocation policy: the three slot page planes are
allocated **unmanaged**, so they are their own textures at every size. The same
height experiment then moves **no** pixel, which is exactly the property
persistent slots need, because a page whose slots survive is a page that grows.
It is a defect correction in its own right — the current executor's model pixels
already depend on how tall its page happens to be — and it is verified below
against the executor without residency.

Two device fixtures lock the property: `checkModelSlotNeighbourBleed` varies a
neighbour's colour, shape, texture and scale, and moves the subject's own slot
with a filler recorded before it; `checkModelSlotPageSizeIndependence` grows the
page past the atlas threshold with committed-off-screen fillers. Both assert
that the finished frame does not change. Neither reproduces the defect at
fixture scale — an eighty-by-forty-eight frame with a handful of subjects does
not put enough on the atlas — so the failing evidence is the table above, which
the 180-frame benchmark reproduces in two minutes.

#### Verification structure

Three binaries, all built outside the repository: **B0** the executor before
this round, **B1** B0 plus the page-plane allocation alone, **B2** the full
branch.

* **B2 against B1** — M1–M8 `.modern.png` byte-identical, the 2× `--shot`
  byte-identical, `battle.png` 3 pixels of 2,073,600 at 180 frames and 4 at 720.
* **B1 against B0** — M1–M8 and the 2× shot byte-identical, `battle.png`
  identical at 180 frames and 1,077 pixels at 720, all at model edges and all
  darker in B1: the run has to be long enough for the page to grow past the
  atlas threshold before the defect can show.

The four remaining pixels are the executor's residual sensitivity to where a
slot sits, not a residency fault: B1 alone, with every slot moved a thousand
rows down its page and residency still disabled, differs from B1 by **1** pixel
over the same 720-frame capture. A subject is rasterized at absolute page
coordinates, so the device's own interpolation and edge rules are evaluated
there, and a lane sitting exactly on a rounding boundary can flip when the slot
moves. It is a few texels per two-megapixel capture, it scales with how far a
slot moves, and it belongs to the executor's raster passes rather than to
anything this round changed.

Both builds are **self-deterministic**: repeated 180- and 720-frame runs produce
byte-identical `battle.png`. `go test -race ./internal/platform/gpurender
./internal/client` is clean and the opt-in device fixtures pass, including the
residency case — the same list executed twice reuses every eligible subject and
produces the same bytes, and a bumped revision rasterizes exactly the changed
subject.


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

Fog edge clipping correction (2026-09-11): both executors keep the projected
cell origin for GAF placement and clip destination writes only after the
frame offset is applied [03 §3.3][R-RR16-A §3]. Previously the classic sink
clamped the origin before calling its blitter, and the GPU shader duplicated
that clamp. A partially offscreen top or left cell therefore pinned its art
to the screen edge; at 2× it could displace the cloud by nearly 64 pixels.
Fill rectangles remain clipped. Regression fixtures pan black, gray and
dithered gray masks past both edges at 1×, 1.5× and 2× and compare the result
with a crop of the unpanned image, including actual GPU readback.

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
  its own family so the Terrain bar moves. A synthesis failure is reported on
  stderr and the client falls back to nearest doubling; it never fails the load.
* **Remaster status popup (Nanolathe presentation).** After 350 ms of remaster
  work on the loading screen, a centered, non-interactive popup uses the
  installed MSGBOX panel art, frontend font, and unscaled LIGHTBAR grille.
  It labels preparation, map tiles, and sprites separately. Its extra bar
  reports completed unique tiles during terrain synthesis, then frame progress
  across the sorted sprite banks with equal weight per bank. This measures work,
  not estimated time; the phase change may reset the percentage. Animated dots
  and elapsed seconds continue during example preparation and cache I/O.
  Each part reports its boundary even on a cache hit or fallback, and the
  overall family's completion removes the popup. Quick cached loads finish
  before the delay, and disabled remastering never opens it. The worker
  publishes immutable phase/percentage snapshots; elapsed time and painting
  remain on the render thread. This extends the existing loading screen only;
  inline save restoration and capture paths retain their existing behavior.
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

These are the original detail-view controls, retained by classic. Modern
uses the defaults and controls in §16.8.

* **F9** cycles the view scale 1× → 1.5× → 2× → 1× about the viewport centre
  (`ViewScale.Next`).
* **F10** toggles the executor between classic and modern. The client
  publishes the requested executor; the adapter switches at the next Update,
  turning interpolation and the synthesized art off when classic takes over
  (§14.3), and the retained screen bridges the swap. Neither key is a retail binding; retail's dispatcher does
  not read them. F10 also updates the shell preference and persists only the
  renderer field. The Nanolathe options page uses the same swap cleanup for
  live previews and Cancel restoration (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
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

**Lit points.** The explosion and muzzle-flash halos are point batches that
READ the destination and brighten it through an `LHT` row, so a screen pixel
must receive each batch at most once. Scaled quad by quad, a shrinking
transform lands two or three record points on one screen pixel and brightens it
two or three times, and the halo drew as a lattice of over-lit pixels at the
1.5× default. The executor therefore resamples a lit batch under the transform:
each screen pixel is lit by exactly the record point nearest sampling chooses
for its centre, the same rule the terrain and sprites follow, and the point is
placed in screen pixels with the transform held off. At a rest step the batch
takes the path it always took.

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
the battle's camera pass. Easing uses host Updates and scroll cooldown uses
the supplied monotonic host milliseconds; neither reads simulation time [I6].

* **The steps.** `ZoomSteps` is the ascending list {0.25, 1, 2}: a tactical
  overview, the default native view, and the detail view. Fractional stops
  above 1× were removed after visual feedback on uneven sprite/model scaling
  and its mismatch with filtered terrain (§16.3). The 0.25× overview shows
  four times the native span on each axis when the map is large enough. Smaller maps clamp
  to the minimum factor that fills the viewport (§16.7), such as 0.5×; the
  floor need not be one of the named steps. These are presentation choices,
  not retail findings.
* **The wheel** requires `ZoomScrollThreshold` of accumulated travel:
  1000 thousandths, or one Ebitengine wheel unit, restoring one conventional
  mouse click per step. A call can move at most one stop, even for a large
  scroll delta. After an accepted step, `ZoomScrollCooldownMillis` discards
  further scroll input for 500 host milliseconds. Discarded input neither
  accumulates nor extends the deadline, and the triggering event's excess is
  discarded too. Thus a burst from 2× first targets 1× and cannot queue a
  second jump to 0.25×. Continuing scroll after the hold can take another step.
  This is a presentation feel choice, independent of game speed and pause.
  F9 and pinch bypass the cooldown and clear pending wheel state.
  Below-threshold fractions accumulate; reversing direction clears that
  remainder. A step refused at a zoom limit does not start a cooldown.
  From a free factor the wheel takes the nearest stop in its direction of
  travel. Zoom stays anchored at the pointer throughout the animation.
* **macOS two-finger scrolling** pans both axes in Enhanced. A local AppKit
  monitor uses `hasPreciseScrollingDeltas` to distinguish point-based touch
  scrolling from conventional wheel events; Magic Mouse touch scrolling also
  pans. Raw point deltas follow the user's macOS scrolling direction, convert
  through the window's letterbox scale into logical pixels, then divide by
  the live zoom. Fractional world-pixel remainders carry between direct
  deltas, so slow scrolling still moves at 2×. Panning clears camera follow.
  `momentumPhase != 0` events never pan or zoom: movement stops on finger
  lift instead of continuing through the inertial tail. GUI controls retain
  all scroll events in Ebitengine wheel units (precise points × 0.1).
* **macOS pinch** accumulates signed magnification deltas until their net
  magnitude reaches `pinchThreshold` (0.12), then requests one adjacent zoom
  stop, anchored at the pointer sampled when the gesture began. Even a large,
  reversed, or long-held pinch cannot step again until a new gesture begins.
  An attempt at a zoom limit also spends the gesture. End/cancel events retire
  the gesture; cancellation does not undo an already accepted zoom target.
  Ordered pinch events survive host batching and the semantic input copy,
  including complete gestures occurring between two polls. Blocked camera
  input cancels the active pinch; later change events cannot reactivate it.
* **The ease** closes `ZoomEaseFraction` of the remaining gap per Update, moves
  at least one unit so an integer factor cannot stall, and settles outright
  inside `ZoomSettleEpsilon`. It is what makes a notch a glide rather than a
  cut, and it is the only time the live factor is off a step.

The native monitor implements these presentation choices using
[Apple's gesture and scroll-event semantics](https://developer.apple.com/library/archive/documentation/Cocoa/Conceptual/EventOverview/HandlingTouchEvents/HandlingTouchEvents.html).
It returns events unchanged. Empty native polls stay empty rather than replaying
Ebiten's copy. Other platforms and VM guests retain Ebitengine wheel zoom; no
device classification is guessed from delta magnitude or event timing.

The first build of this section had a free log-scale wheel with an idle snap
onto the detail steps and nothing below 1×. The play test preferred discrete
steps with the glide between them, on both sides of 1×, and that is what stands.
Every one of the names above is a **feel-tuning knob**, not a derived value, and
wheel/ease knobs live at the top of `internal/camera/zoomfeel.go`;
`pinchThreshold` lives in `cmd/nanolathe/trackpad.go`.

The wheel binding is Nanolathe's, not retail's. Retail leaves the wheel to the
active GUI list under the pointer [07 §2][07 §10], and the UI boundary still
consumes it first: the camera pass sees only a wheel the chrome did not want,
and takes it only over the battle viewport, only outside TALK, only with no
modal open and the pointer off the minimap, and only in the executor that can
present a free factor. Trackpad controls share these gates and additionally
require window focus and no command palette or unit-info ownership. Skipping
the camera pass (including modal and result screens) clears gesture state.

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
  viewport centre. Modern cycles 1× → 2× → 0.25× → 1× as animated targets,
  sharing the wheel's step list. A free factor cycles to the first step above
  it, wrapping to 0.25× at the top; the map floor still applies.
* **The wheel** is §16.6.
* **`--zoom`** accepts any factor in the free range for the modern executor and
  only 1, 1.5 or 2 for classic — the executor's restriction is applied after
  parsing, because `--renderer` may follow `--zoom` on the command line. A
  capture follows `--shot-renderer` when one is given. The map-derived floor is
  applied at battle entry, where the map is known. Modern defaults to 1× at
  every resolution. Classic keeps its resolution default: 1.5× above 800×600
  and native at or below it. Captures and the benchmark stay native. A restart
  keeps the factor the player was on. Battle entry, a restart and a capture take the factor outright rather
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

At and below `strategicModelCut` (0.5×) — inclusive, so the 0.25× tactical target of
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

This original generic-marker contract is superseded by §18 for generated
icons and Enhanced team color. Below `strategicMarkerOn` (0.625×) each unit becomes one filled square of
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
strategic cut (`TODO(question)` at `Client.markerAlpha`). The capture route now supports a prospective build placement through
`--shot-build` (§20), after `--shot-select` has published its selection.

## 17. Model antialiasing: subject-wide supersampling with a coverage resolve (Enhanced)

> **Superseded (2026-09-11).** The recorder's doubled lane and half-pixel
> positioning (§17.2, §17.3) stand and feed the model lane of §22, which
> resolves coverage in its commit fragment; the executor stages of §17.4–§17.5
> went with the slot stage.

### 17.1 Decision

Every model subject the Enhanced executor draws — mobile unit, structure, the
live lane, the nanoframe outline, debris, and the shadow silhouette — is
rasterized at twice the record step's resolution and resolved two-to-one with
**fractional coverage**: a resolved pixel's colour is the mean of its covered
samples' palette colours and its alpha the fraction of samples covered. An
uncovered sample contributes nothing. The result commits over the composite by
source-over, so an edge pixel blends with whatever is under it in proportion to
how much of it the subject covers.

This replaces, in Enhanced only, retail's structure supersample of
[03 R-REN-03A §6–§7], which doubled structures alone, drew the live lane at
native scale afterwards, and resolved through the ordered ALP table with the
composition background index blended in — the coloured fringe of §4. The
classic executor keeps that behaviour exactly (C-G6); the Anti-Alias display
option keeps its name and gates both: on, Enhanced supersamples everything and
classic supersamples structures; off, both rasterize at native scale.

The fringe is not preserved. The user's decision is that it was a defect of the
original rather than a look to keep, and Enhanced is the mode that is allowed to
diverge (§1, §13). The classic executor remains the retail reference for it.

The subject is also placed at **half-pixel precision**: the doubled raster is
offset inside its slot by the half pixel the subject's interpolated position
actually lands on, which the two-to-one resolve turns into a genuine half-pixel
step on screen. Enhanced presents interpolated positions at the display rate
(§13.5), and without this every unit still moved in whole pixels.

MSAA was considered and rejected: Ebitengine exposes no multisampled render
target, its `AntiAlias` draw option is a stencil path for solid vector fills,
and the model passes are index-space shaders with a key-plane discard that such
a path cannot express. Supersampling reuses the doubled slot the executor
already had for structures.

### 17.2 The recorder — contract S1

`Client.supersampleGeometry` is the gate: the Anti-Alias option and a palette,
nothing about the subject. When it holds, every recorded `ModelGeometry`
carries a `Supersample` packet at scale 2 holding the subject's own doubled
raster: its `Faces` (the cached lane, or every lane for a direct subject), its
`LiveFaces` and its `Reveal`, all in the doubled packet's local image
coordinates; the outline endpoints stay on the native packet, which the model
lane draws as whole pixel blocks (§22). The outer packet keeps its native faces, box, origin and
anchor; the executor rasterizes none of the native faces when a doubled raster
is present, but the box remains the commit rectangle and the anchor the
subject's screen position.

The doubled projection is retail's: corner `(x, y)` of the native local
projection lands at `2·(x + originX) + hx`, `2·(y + originY) − odd + hy`, where
`odd` is the low bit of the corner's model-relative height, so the doubled
shear is `2z − y` rather than `2·(z − (y >> 1))` [03 R-REN-03A §6], and
`(hx, hy)` is the subject's half-pixel offset (§17.3). The shadow's doubled
quarter shear moves the corner one raster pixel right as well as up when the
second bit of the height is set, which is the same identity applied to
`ry >> 2`. At a magnified view scale the correction is the view's own pixel
(`ViewScale.Px(1)`), because the scaled native offset already lost the half it
restores. The doubled packet is filled straight from the unplaced polygons, so
the lane costs no copy of their corner lanes.

The cached lane is retained without the half-pixel offset; the offset is added
when the retained lane is rebased into the frame's packet, so a subject that
moves without changing pose keeps its cache. A keyed subject whose pieces are
all live retains an empty doubled lane so its live faces have a raster to join.
The geometry cache's identity carries this gate separately from the classic
image's structure-only one, so toggling the option rebuilds the right lane.

A direct-projected subject's packet used to declare the whole record extent as
its box; it now declares its corners' own extent with retail's two-pixel
margin, origin at the box pixel of screen (0,0) and anchor at screen (0,0), so
its slot is the subject's size and can be doubled.

### 17.3 Half-pixel positioning — contract S2

`Camera.WorldToScreenDoubled` is `WorldToScreen` carried in 16.16 at twice the
record step: `floor(2·s·(x − cameraX))` and `floor(s·(2z − y − 2·cameraZ))`,
viewport origin included, with `s` the record step. A supersampled subject is
anchored on that result's whole pixel (`sx2 >> 1`, `sy2 >> 1`) and its doubled
raster is offset inside the slot by the half (`sx2 & 1`, `sy2 & 1`), so the
offset is 0 or 1 on both axes at every view scale and the composition box's
margin always holds it. The anchor can sit one pixel from `WorldToScreen`'s,
because retail's shear halves the whole part of the height while the doubled
projection halves the height itself; that is the subject drawn where it is
rather than where retail's truncation put it, and the classic executor keeps
retail's pixel. At 2× the retail anchor was always an even screen pixel, so
units moved in two-pixel steps; the doubled projection restores the missing
step. The shadow projects the ground point, which is what its anchor projects
[03 R-REN-03D §3], with shadowAnchor's five-pixel X offset on the whole pixel.

A direct-projected subject (debris, a keyless live lane) has no shared
offset: its corners are projected one by one, so each carries its own exact
doubled position (`screenPoly.x2, y2`) and the doubled lane takes those. Its
box counts the doubled corners' whole pixels too, so it holds the lane at any
scale.

### 17.4 The executor — contract S3

One page, one stage, every stage in index space until the last:

```
img   clear
key   clear; every subject's key faces
img   every subject's colour faces
post  the nanoframe reveal (ping-pong)
key   the outline keys, then the live keys, max-blended over the body keys
img   the revealed regions copied back, then the outline colours, then the
      live colours
post  waterline/Digger clipping, then the planes swap
post  THE RESOLVE: every subject's raster → its native box, as colour
```

The outline and live lanes share two passes: with a max-key plane the order of
an outline endpoint and a live face is decided by key whether the endpoint's
colour was written before the live key was raised or after, and the colour
runs still draw the outline before the live faces so an equal key goes to the
later one [03 R-REN-03A §3].

A supersampled subject reserves only its doubled slot; its resolved native box
is that slot's top-left quadrant on the resolved plane, which nothing reads
once the raster is finished, so the page holds no native slot for it and its
rows are the doubled rasters alone. A native subject's raster is its native
slot, and the same resolve pass copies it one-to-one through PAL at alpha 1. A
page of mixed subjects is one pass, and the stage is at most eight destination
switches whatever the frame holds. The resolved plane is `post`; the finished
index plane is `img`. A commit samples the resolved plane. A group merge
samples the index plane at the parent's scale. Only the raster that draws is
admitted: a supersampled subject's native faces are never tested or prepared.

The resolve shader reads the block under each native pixel — one sample for a
native raster, four for a doubled one — and for every sample whose index is not
the composition transparent index 1 looks the index up through PAL and counts
it. It writes `(Σ colour / 4, n / 4)`, premultiplied. Colours are averaged
after the PAL lookup, never indices (C-G4). Index 1 is exactly the texel the
keyed commit skipped [03 R-REN-03A §5], so a native raster resolves to the same
skip and colour the commit used to produce.

The outline of a supersampled subject is drawn two raster pixels wide, so the
resolved line keeps a whole pixel's weight where it aligns with the block and
splits into two half-weight pixels where it does not, instead of resolving to a
quarter-weight line.

Slots stay even-aligned with even dimensions (§11.2), so the block a native
pixel reads is the block its doubled raster wrote.

### 17.5 Commits, shadows and groups — contract S4

* **Body commit.** `sceneOpModelCommit` copies the resolved texel: an
  uncovered texel is the key skip, a covered one is the colour at its coverage,
  and the opaque batch's source-over blends it. Ordering is unchanged: within a
  batch quads draw in record order and source-over honours it, and the
  scheduler already places an opaque command after any destination command it
  overlaps.
* **Shadow commit.** Both planes are resolved ones. The silhouette's fragment
  is half its premultiplied colour at half its coverage, so a partly covered
  silhouette edge darkens the ground by its coverage. The punch skips a shadow
  texel only where the body's coverage is whole: under a partly covered body
  edge the shadow stays and the body's own blend restores the shadowed ground
  in proportion, which is the composite the byte writers' opaque punch
  approximated [03 R-REN-03D §4–§5].
* **Attached-unit groups.** A group composes at its parent's raster scale, on
  index planes, with the unchanged child merge and clip shaders
  [03 R-REN-03A §4], then one resolve pass turns every group into colour on
  the staging atlas' out plane, which the commit samples. A child whose scale
  differs from its parent's cannot join the plane and takes the fallback path,
  where it is an explicit omission. The fallback path does the same for one
  group on its own three images.

### 17.6 Divergences

* Edge pixels of every subject are blended with the composite by coverage;
  face boundaries inside a subject are averaged. Neither exists in retail.
* Structures lose the ALP fringe.
* The live lane and the outline are supersampled with the body; retail draws
  both at native scale after the structure resolve.
* A subject's doubled raster sits at its half-pixel position, so a subject can
  present half a pixel from where the classic executor puts it, and its
  resolved silhouette differs by that.

### 17.7 Verification

1. **Device fixture.** `model_fixture_test.go`: the supersampled subject's
   left pixel resolves the mean of its four covered samples with the live lane
   having joined the doubled raster; its right pixel resolves three covered
   samples at three-quarter coverage over the background after the child merge
   and the carrier's waterline; the one-sample edge subject resolves at a
   quarter over the background with no fringe. The stage pass bound is eight.
   The neighbour-independence and fallback-route checks are unchanged and pass.
2. **CI tier.** `go test ./...` green; the recorder's direct-route tests now
   assert the tight box and screen anchor.
3. **Captures.** The M2 commander and the M8 mission units through
   `--renderer=modern` before and after, magnified: silhouettes smooth, no
   fringe, shadows intact.
4. **Benchmark.** The modern battle benchmark, 1080p, 180 frames, main
   against this branch, run back to back. At 120 TPS: Record 3.0 → 3.3 ms
   median, Submit 2.6 → 3.0, cadence 8.32 → 8.34, on the 8.3 ms floor 73% →
   68%; passes 13 both, raster pixels 0.87M → 1.8M, one page band both. The
   floor share moves by ten points between runs of one binary, so it is read
   from adjacent runs only. At 30 TPS both sit on the floor. Two rounds of the stage design got there: the first landing cost
   sixteen passes, a second page band and a 24% floor share, from a separate
   native slot per subject, four outline/live passes, a doubled admission and a
   copied polygon lane per subject; each is gone. The 30 TPS main run also
   showed an older pathology this branch removes: direct-projected subjects
   (debris, keyless live lanes) declared framebuffer-sized boxes, overflowed
   the atlas and cost that run 81 passes and 41M raster pixels a frame.

### 17.8 Owed

A human look at motion in the window: the half-pixel step at 120 Hz, and
whether the two-raster-pixel outline reads right on a rising nanoframe. The
Anti-Alias option's menu text still describes the retail structure behaviour;
its Enhanced meaning is wider now and the text is owed a line.

## 18. Generated strategic icons (modern)

### 18.1 Scope and visual direction

**Implemented.** Extend §16.11's four-pixel contact squares with a small,
consistently generated symbol vocabulary for identified units in the modern
strategic view.
This is an Enhanced presentation feature under I6/I11, not recovered retail
behavior. This section supersedes §16.11 for identified units; unidentified
contacts retain its generic square.
Classic, simulation, unit definitions and their authoritative identities retain
their existing behavior.

The reference is BAR's public [Strategic Icons guide](https://www.beyondallreason.info/guide/strategic-icons)
(accessed 2026-09-11): it combines family shapes, role symbols and level marks.
Adopt that general visual organization with independently authored geometry;
BAR's implementation, artwork, balance roles and tier meanings are not inputs.

Generate icons deterministically from simple vector geometry, rasterized into
one shared atlas at load time. A role icon should remain legible when the unit
model would be only a few pixels wide. Model thumbnails and per-unit generated
paintings are poor first choices for that purpose. No network generation,
randomness or image generation occurs during play. Units with the same verified
family and role may share an icon; a unique symbol for every definition is not
a first-release requirement. Exact identity remains available through the
existing permitted hover information.

Vocabulary used by the generated review sheet:

| Component | Meaning | Starting design |
|---|---|---|
| Outer contour | Family | circle for kbot, diamond for vehicle, triangle for aircraft, trapezoid for hovercraft, hull for ship, capsule for submarine, square for structure |
| Inner symbol | Primary purpose | construction tool, factory, extractor, energy, storage, sensor, jammer, transport, weapon or generic support |
| Small secondary mark | Useful supported distinction | manufactured family on a factory; weapon subtype where verified |
| Color | Owner | dominant opaque shade of the published lobby color's `32xlogos` frame |
| Halo | Selection / hover | separate from role and ownership; retain contrast over bright and dark terrain |

The accepted size is 24 framebuffer pixels after play-test feedback on 20px.
It stays 24 pixels at every camera zoom, including the 0.25× tactical target
and map-clamped intermediate factors; picking uses the same fixed footprint.
The review sheet retains 16/20/24 comparisons; these are design choices, not
retail constants. Keep icons upright, centered on the current marker
projection, clipped to the battle viewport and outside the scaled world region.
Keep the existing 0.625× fade-in / 0.5× full-icon transition for the first pass;
retuning zoom thresholds and model crossfade are separate decisions.

Do not reinterpret category levels as universal tech tiers. The reviewed
construction-only LEVEL1/LEVEL2 distinction (§18.2) uses a single/crossed tool
glyph; other level tokens remain audit metadata.

### 18.2 What the installed data establishes

**Established — implementation and asset observations.** On 2026-09-11, a
read-only audit at main `f0673b3` used `vfs.MountGameDirectory` and
`content.Compile` on `~/TotalAnnihilation`, then walked `SortedUnitKeys`,
compiled weapon links, build menus and retained inert FBI keys. It found 278
loaded definitions, 139 per side. This is an observation of this mount, not a
required count, a test fixture or a statement about all installs. Raw audit
output stays outside the repository. Reproduce via the same catalog path;
reading loose FBI files or scanning archives independently would bypass the
winning-provider and admission rules [02 R-CAT-01 §4][SC24].

Available inputs: `BMCode`, `Category`, capabilities and economy fields,
resolved movement data, `Weapon1Def..Weapon3Def`, and final build-menu products.
`TEDClass` is retained in `UnitDef.Unknown`; it is inert in retail gameplay
[fmt fbi]. Reading it as authored editor metadata for this new presentation
feature does not give it simulation behavior. The current trail implementation
already reads it, but its movement-name and model-piece heuristics are not
proof of an icon family.

| Observed examples | Implication for classification |
|---|---|
| `ARMPW`: `KBOT`; `ARMFLASH`: `TANK` in Category and TEDClass | Strong explicit evidence for their family contours |
| `ARMCK`: `KBOT CONSTR`; `ARMCV`: `TANK CONSTR`, TEDClass `CNSTR`; `ARMCA`: `VTOL CONSTR`; `ARMCS`: `SHIP CONSTR` | Construction is a role independent of mobility family; TEDClass alone loses the vehicle constructor's family |
| `ARMVP`, `ARMLAB`, `ARMAP`, `ARMSY`, `ARMHP`: structure BMCode, Builder and CanMove set, nonempty product menus | Do not classify buildings with `!CanMove`. Use structure identity, then the build products [SC21] |
| `ARMASP`, `CORASP`: structure builders, empty product menus, TEDClass `SPECIAL` | Builder alone does not establish a factory; preserve repair/support distinctions |
| `ARMSOLAR`: EnergyMake zero, EnergyUse negative; `ARMWIN`: WindGenerator; `ARMTIDE`: TidalGenerator | Energy sources require the actual authored economy vocabulary, not a positive EnergyMake test |
| Constructors and factories carry incidental resource production/storage; `ARMRAD` has EnergyMake | A positive resource field is insufficient to assign an economy role |
| `ARMMOHO`: ExtractsMetal; `ARMMAKR` and `ARMMMKR`: MakesMetal; `ARMESTOR`: STORAGE and EnergyStorage | Separate extraction, conversion and storage; do not confuse incidental storage with dedicated storage |
| `ARMFIG` and `ARMJETH` linked missiles have ToAirWeapon false | That flag alone cannot identify fighter / anti-air roles. Weapon family and targeting preferences need a bounded evidence review |
| `ARMPT` has laser and missile slots; `ARMLATNK` has two weapon slots | Do not choose the first weapon as the unit's entire role |
| `CORNECRO`: CanResurrect, Builder, category WEAPON, inactive weapon slots | Capability and resolved active weapon links must qualify broad category tags |
| `ARMSUB`: UNDERWATER; `ARMATL`: structure, TORP and WaterWeapon; `ARMTIDE`: TEDClass WATER | Water-related metadata does not establish a mobile submarine |
| `ARMAMPH`: KBOT and CanHover; `CORSCORP`: Category TANK, TEDClass SPECIAL | A single mobility flag or editor class is not an exhaustive physical-family taxonomy |
| `ARMCOM` / `CORCOM`: Commander true, LEVEL10; decoy commanders: TEDClass COMMANDER, Commander false | True-command capability and visual disguise are separate; enemy art must not expose the difference |
| `ARMMOHO`, `ARMBRTHA`, `ARMAMD`: LEVEL3; `CORSSUB`: bare LEVEL; six definitions have no level token | No BAR T1/T2/T3 reinterpretation, inferred cost tiers, or guessed missing levels |
| `ARMDRAG`: IsFeature, category METAL, TEDClass FORT | It is not a metal producer; after conversion its feature stays in the existing feature layer |

The audit also found five mobile definitions without any of the usual explicit
family tokens: the two decoy commanders, `ARMSCAB`, `CORMABM`, and `ARMSCORP`.
TEDClass narrows some of these, but a rule must report its fallback rather than
silently treating SPECIAL as a tank. Case-insensitive whole-token comparison
is required; `NOTAIR` is not an aircraft token and `NOTSUB` is not a submarine
token [02 "Unit record"][fmt fbi].

**Established — constructor asset audit.** The 20 ordinary constructors in the
reference mount each author CONSTR and one LEVEL1/LEVEL2 token, corroborated by
their authored basic/advanced descriptions. ARM/CORE CV/ACV, CK/ACK and CA/ACA
pairs cover vehicles, kbots and aircraft. CSA seaplanes, CH hovercraft and CS
ships use LEVEL1; ACSUB submarines use LEVEL2. ARMMLV's LEVEL2 and CORMLV's
LEVEL1 do not make minelayers ordinary constructors: neither authors CONSTR.
ARMFARK's CONSTR LEVEL2 and empty product menu retain the assist classification.
The icon policy applies basic/advanced only to a construction-role definition
with CONSTR and a single reviewed level token. Missing, conflicting or other
levels retain the unqualified tool and an audit unknown; no costs or localized
names participate in classification.

### 18.3 Classification contract

**Implementation policy.** Build a presentation-only descriptor table from the final
compiled catalog, indexed by canonical definition identity within that catalog.
Each descriptor stores its family, role, optional subtype and evidence:
source logical path/provider, relevant authored fields, rule identifier, and
whether the result is established input, a reviewed presentation mapping, or
unresolved. Icon geometry and classification rules have their own revision;
neither changes the authoritative definition hash.

1. Establish structure versus mobile from the existing compiled BMCode
   convention. Partition capabilities from role rather than collapsing both
   into one enum. Use explicit category family tokens where available,
   compatible TEDClass metadata next; contradictory or absent evidence keeps a
   generic family until reviewed. Do not infer legs from a TANK movement name.
2. Resolve specific purpose before incidental stats: feature conversion,
   commander appearance, resurrection/construction, manufacturing/repair,
   dedicated economy and sensors, then combat/general support. A classifier
   must retain all supported capabilities even when the icon displays one.
   Confirm precedence against the full audit rather than baking this list into
   a gameplay rule.
3. Derive factory product badges from resolved final build-menu products,
   including downloads. Mixed or unresolved product families receive a generic
   factory symbol. Never infer products from a unit-name prefix.
4. Use explicit economy/sensor category tags plus capability fields to
   distinguish dedicated functions. Energy generation must include negative
   EnergyUse, wind and tide as documented inputs [02 "Unit record"]. A
   constructor's production, a plant's storage or a gun's radar does not
   replace its primary role.
5. Use only active resolved weapon slots, never ExplodeAs or SelfDestructAs,
   to describe armament. Interceptor, dropped, water-weapon and paralyzer flags
   are evidence for capabilities; interpreting them as a primary unit role
   still requires a reviewed mapping. Do not infer scout/artillery/heavy/AA
   from arbitrary speed, range, cost, damage, or weapon-name thresholds.
6. Audit unresolved cases using authored descriptions, build relationships,
   weapon data and original model/build art. Runtime classification never
   parses localized display names. A small explicit presentation mapping is
   acceptable only with recorded evidence and applicability to the relevant
   definition content; an unknown/modded definition gets a generic fallback,
   never the role of an unrelated stock unit with the same name.

**Unknown / design follow-ups.** The complete AA/fighter/artillery distinction,
ambiguous physical families, exact-unit differentiation, and constructor classes
with missing/conflicting/unreviewed level tokens remain audit unknowns. Existing retail facts
need no new executable analysis; any new claim about targeting or disguise
behavior must first be established and recorded in its owning research category.
Missing evidence is shown in the audit and becomes `TODO(question)` at the
classifier site. A generic symbol is a complete supported fallback.

### 18.4 Visibility and interaction contract

**Established — implementation inputs.** Committed UnitViews carry model
visibility, owner color and selection. RadarContactViews also contain concealed
definition metadata; contact presence, Visible, Graphic and Commander are not
identification proofs. The painter gates enemies by explored fog at the anchor
and the committed hull visibility predicate [03 §3.2][03 §3.3]. Attached models
inherit the admission of their carrier through its published Cargo list;
piece-less cargo is omitted [03 R-RAST-01 §7].

**Implementation policy.** Visible-unit icons use that world-painter admission,
independently of minimap contact status and damage blinking. Iterate the committed
units even when a radar record is absent. Own visible icons remain steady during
combat. A same-publication slot lookup suppresses duplicate radar marks and
resolves attachment links; missing links and nested cargo cannot reveal attached units.
Sensor-only contacts retain MinimapBlipAdmitted, blink included, and never expose
definition art or a UnitView hit. Identities are not remembered across visibility
loss; retained hover identity uses InstanceID rather than a reusable pool slot.

Team ink comes from the most frequent opaque index in the published lobby
selector's authored `textures/logos.gaf` `32xlogos` frame. The retail `radlogo`
contains eight gray border pixels and four team-color interior pixels, so its
majority index is gray even for blue/red teams. Colors are resolved once per
art/selector binding; no owner-slot-to-color assumption or alternate RGB team
table is introduced. Enhanced sensor dots use the same logo tint when available.
Missing team art/color gives visible-unit icons neutral ink, never invisibility;
generic contact admission still requires its published radar art/selector.

Disguise is an additional information boundary even for visible units. Until
its retail disclosure contract is verified, use a shared commander-looking
symbol for visible non-owned commanders and decoy commanders, with no truthful
commander/decoy badge. This is a conservative new presentation policy, not a
claim that retail icons existed. Restrict any exact-role embellishment or
new tooltip information accordingly. Disguise protection covers the complete
descriptor: contour, role, secondary badges, size and newly exposed hover
metadata. Unresolved commander-looking definitions keep that shared appearance
until reviewed. Visibility loss and changes of viewer, camera, viewport or
mode must invalidate presented icon/hit-test lists immediately; never reuse a
prior projection or frame's typed list just because its texture remains cached.

**Established — implementation discrepancy.** §16.11 says marker hover picks
the nearest square; `PickSnapshotUnit` actually tests model hulls and scores by
size. Enlarging icons without a new hit test would make the displayed target
and clickable target disagree.

**Implementation policy.** At/below the existing 0.5× model cutoff, visible typed icons
use the same screen bounds for drawing and picking. Keep normal hull picking
above that cutoff during the fade. At/below the cutoff, selection uses the icon's
contour halo alone: suppress the world-space ground footprint quad to avoid a
second, offset selection cue. Keep the ground quad during the fade, at normal
zoom, and for presentation without generated icons. Selection membership,
orders and drag-selection behavior do not change.
Resolve overlaps by drawing ordinary icons
with sensor contacts before visible units in stable publication order, selected icons last, and hit-testing in reverse draw
order; a hover halo does not itself reorder icons. Share this decision across
cursor, click selection, tooltips and command targeting. Generic contacts retain
existing command/knowledge restrictions; a hit must not hand hidden UnitView
metadata to a tooltip or enable a new target action. Keep drag selection's
existing visible projected-center policy. No aggregation, displacement or
cluster counts in the first release; inspect dense overlap before designing any.

### 18.5 Implementation boundaries

Implementation contract:

- A client-owned `StrategicIconCatalog` compiles immutable descriptors from
  `content.Catalog`; returns descriptor plus evidence and a generic fallback.
  No sim package imports it; no change to `content.UnitDef` is needed.
- Client layout takes the committed frame, viewer, live camera, viewport,
  catalog, radar option flags, bound logo/radlogo/palette resources and reusable
  storage. Together with the frame mapping/blink fields, those explicit
  presentation inputs preserve current admission and color resolution, including
  the current missing-art and unknown-palette behavior. It
  returns admitted icon instances and the corresponding restricted hit targets
  for the same presented frame.
- Drawlist icon records carry immutable atlas identity/UV bounds, screen
  bounds, tint, alpha, selection and clip. They own or safely retain referenced
  data through `List.Clone`, replay and asynchronous record/submit buffering.
- The modern executor uploads the generated atlas once and batches lightweight
  quads. Do not allocate an image, scan definitions, rasterize geometry or
  upload a texture per unit per frame. Outline/foreground masks can share the
  atlas; any extra passes must be counted and measured.

Classification, geometry, layout and evidence live in
`internal/client/strategic_icon_{catalog,art,layout}.go`. The existing
`strategic_markers.go` records the shared layout. `internal/drawlist/world.go`
and `list.go` own records and lifetime; `internal/platform/gpurender/world.go`
and `strategic_icons.go` execute them. Battle setup, cursor, selection and input
bind the catalog and share presented picking. The app's successful submission
hooks preserve the exact accepted camera projection for input.

### 18.6 Stages and acceptance gates

1. **Catalog audit and design sheet.** First produce an independently generated,
   labeled sheet of every loaded definition, grouped by family/role,
   plus an evidence table and unresolved/collision list. Include both factions,
   expansion/download units, factories, nanoframes, decoys and support units.
   Show multiple sizes over bright/dark terrain. Human review chooses the
   visual vocabulary and resolves or explicitly accepts every generic fallback.
   The current planning audit establishes inputs and pitfalls; it does not
   claim all 278 definitions already have reviewed icons.
2. **Atlas and recorder integration.** Implement the accepted catalog and art,
   generic-contact boundary and fixed-screen layer with the existing zoom
   transition. Keep model omission, feature persistence, build ghosts, queue
   overlays and unscaled HUD from §16. Defer icon picking until stage 3 is ready
   to land with it; do not ship enlarged icons with the old hull-only picker.
3. **Interaction and verification.** Land shared draw/pick bounds, selection
   and overlap rules, then inspect motion and dense scenes. Small authored
   fixtures lock radar-only fallback, cloak/visibility loss, decoy appearance,
   immediate slot reuse, classification traps, clip/zoom boundaries, retained
   list replay and draw/pick agreement. Retail tests skip without assets and
   assert relationships rather than a fixed catalog census.
4. **Review and performance.** Run `go build ./...`, `go vet ./...`,
   `gofmt -l .`, `go test ./...` and the applicable GPU device fixtures.
   View captures at 1×, 0.625×, inside the fade, 0.5× and a lower allowed zoom;
   include buildings under construction, aircraft/submarines, selected dense
   armies, radar-only contacts and fog boundaries. Run the live battle benchmark
   sequentially for classic and modern per BATTLE_BENCHMARK.md, plus matching
   modern before/after runs at 0.5×. Compare identical scene metadata, inspect
   feature census and captures, and report CPU time, allocations, atlas uploads,
   quad/pass counts and frame times. Re-run checks after integration with main.

Generated review sheets, local audit output and captures stay outside the repo;
committed assets are our authored geometry/rules and light synthetic fixtures.
The implementation outcome and accepted fallbacks are recorded below.


### 18.7 Implementation outcome and accepted fallbacks

The current release uses 24 framebuffer pixels with a 32-pixel supersampled
source tile, an upright family contour, white role glyph, authored team-color
ink, dark backing and a selection/hover halo. These are Enhanced design choices,
not retail constants. The reviewed sheet shows 16/20/24 alternatives; aircraft
interiors are tight at 16. `go run ./tools/strategic-icon-sheet -root <install>
-out <external-directory>` emits `index.html`, `catalog.png`, `vocabulary.png` and
`audit.json` and a constructor-only `constructors.png` comparison from the loaded catalog and the same masks the GPU uses.

The current reference mount yields 81 semantic symbols across 278 definitions
after adding crossed tools for advanced constructors (single tool for basic).
These are audit observations, not test expectations. Ten primary-role fallbacks
are accepted: ARMPEEP, CORFINK, ARMBEAC, CORBEAC, ARMDEV1, CORDEV1, ARMUWES,
ARMUWMS, CORBUILD and CORTRUCK. ARMSCORP and CORTHOVR retain a generic physical
family. Seven mixed/unresolved factory menus use an unqualified factory badge.
Combat symbols describe the flags of every active weapon slot; AA, fighter,
artillery and heavy-role interpretations remain explicitly unresolved. The
fallbacks are visible in the audit; no runtime name/description guessing or
stock-name override table was added. Teleporters use the literal Teleporter
capability. Commander-looking definitions share their complete art, including
true commanders and decoys; icon audit evidence is never new hover information.

`StrategicIconCatalog` lives in the client and is bound once at battle entry.
The renderer uploads the immutable mask atlas once per resource identity and
uses a bounded four-entry cache, retired on source reset. R/G/B/A hold disjoint
team/white/selected-halo/black coverage, not a premultiplied RGBA image. Bilinear
filtering clamps samples inside each tile; the fragment combines coverage with
the live display palette and applies the layer fade to both color and opacity.
List clones retain the immutable atlas while owning their marker slices.

Layout rebuilds world-unit and sensor-contact admission plus a same-publication
slot lookup on every
record/pick, in reusable storage. Input uses the projection of the last
successfully submitted list when frame, tick, viewer, zoom, viewport, catalog
and camera samples still match. The record/submit pipeline can accept a predicted
camera fraction within its tolerance; retaining its actual submitted origin
prevents a subsequently measured fraction from moving the clickable rectangle.
Speculative records cannot replace this origin. Changed publication or projection
invalidates it, and visibility/identity are always rechecked. Icon hover and
art are foreground state and do not invalidate the paused terrain/fog cache.
Normal zoom keeps the existing battle-camera hull picker; at/below 0.5× icons
and clicks share bounds with selected-last, reverse-hit ordering.

Initial 20px validation on the reference install passed build, vet, formatting, the full
Go test suite, `tools/check-retail` (including staticcheck, deadcode and tagged
retail tests), and real Metal device fixtures. Native 1× classic and modern
before/after captures were byte-identical. Selected commander captures at
0.625×, 0.5625×, 0.5× and 0.3× verified the transition and fixed-size icon.
Dense live-battle captures covered aircraft, factories, nanoframes, mixed typed
and radar-only contacts, and fog boundaries. The generated sheet covers the
naval families; synthetic layout/device fixtures cover overlap priority,
visibility loss, clipping, selected halos and camera/publication changes.

Sequential classic and modern live benchmarks completed. Matching Comet Catcher
modern runs at 0.5× (seed 7, 180 measured draws, target 30 Hz) had identical
scene metadata and every frame's census: the final frame contained 316 units,
198 moving units, eight nanoframes and 92 features. Median record time was
0.928 → 1.007 ms, submission 1.495 → 1.653 ms, and total draw work
6.189 → 6.588 ms; cadence stayed 33.333 ms. These are host measurements from
one pair, not GPU timings or a general performance guarantee. Measured
allocation bytes were 105,061,368 → 105,111,152 (about +0.05%), with
726,738 → 728,801 allocation calls. Median executor counters remained seven
passes, three phases, six draws and 31,868 vertices.

An initial implementation fragmented batches when typed icons alternated with
generic contacts. The final path binds the shared icon atlas for both and uses
a flat-palette shader branch for generic squares, retaining their existing base
and four outline primitives. A 100-contact device fixture proves one scheduled
run, byte-identical output against singleton legacy-dot batches, and one atlas
upload across replay. Palette changes do not upload the masks. This removed
the initial excess graphics-driver allocations without reordering contacts.
### 18.8 Play-test feedback correction

The 24px revision corrects three related visibility/color defects: world icons
no longer depend on the minimap's stale contact latch or its damage-blink term,
and their team ink comes from HUD logo art instead of radlogo's gray border.
Visibility tests cover enemies with LOS but no admitted/published radar contact,
both damage-blink phases, owner selection, radar-only fallback, unexplored fog,
24px hit edges, missing team color, direct attached models and hidden/nested
cargo. The constructors sheet verifies basic/advanced glyphs across both
factions and physical families against the authored categories.

Sequential Comet Catcher live benchmarks (seed 7, modern 0.5×, 180 measured
draws at 30 Hz) retain identical scene metadata and every frame's simulation
census. Median record time was 0.983 → 1.075 ms, submission 1.522 → 1.510 ms,
total draw work 6.021 → 6.255 ms, cadence 33.335 → 33.334 ms. Measured allocation
bytes were 105,112,192 → 107,592,640; allocation calls 728,826 → 732,206.
The corrected view draws more visible units: median vertices 31,868 → 32,230,
with six draws, seven passes and three phases unchanged. These are one pair's
host measurements, not GPU timings. Classic also completed the live benchmark;
its capture and the before/after modern captures were visually inspected.
Build, vet, formatting, full tests, real GPU device fixtures and
`tools/check-retail` passed on the integrated revision. Selected-unit captures
at 0.625×, 0.5625×, 0.5× and 0.3× were inspected; native 1× classic and modern
before/after captures were byte-identical.

## 19. Glow: Enhanced bloom from the world's light sources

### 19.1 Decision

Retail's composite has no light. A laser is a one-pixel Bresenham line in a
palette colour, an explosion is animated art with a brightening of the
pixels under it, and nothing spills past its own pixels. The glow layer gives
the world's light sources a halo: an Enhanced presentation feature under the
umbrella of §14.3, on by default while the modern executor presents, absent
from Original, and never a simulation input [I6]. The classic executor is
unchanged and stays the byte-exact reference; with the layer switched off the
modern executor's output is byte-identical to the executor without it
(§19.5).

The user's brief relaxed the requirement that Enhanced match retail's
software composite, so the layer is designed for the look rather than for a
retail-derived arithmetic. Two things are still not invented: which pixels
are a light, which the recording already knows, and what colour a light adds,
which is either the palette colour retail draws or the brightening retail
applies.

### 19.2 The sources — contract L1

The recording marks its light sources and nothing else changes in it.
`drawlist.Line` and `drawlist.Sprite` carry an `Emissive` flag; the recorder
sets it on:

- the beam and segment strokes of the projectile renderer (lasers and
  lightning) [06 R-WFX-01 §4], never on the selection quad or a path;
- the effect, projectile (plasma shells, flares) and strip (fire, explosion
  animation, smoke) sprites, never on unit, feature, chrome or shadow art.

The flag is metadata: every executor draws the flagged command exactly as it
did, and the classic sink and every parity fixture ignore it. The explosion
flash disc and the ground halo (§13.11) need no flag — they are modern-only
commands and always light sources.

What each source emits into the glow plane, `internal/platform/gpurender/glow.go`:

| source | emission | notes |
|---|---|---|
| stroke | `PAL[index] × glowGain` over a quad `glowLineWidth` screen px wide (scaled by the world transform), extended by its half-width at both ends | a one-pixel line has too little energy to survive the blur; the quad gives it some |
| sprite | the texel's `PAL` colour × `smoothstep(glowThreshold, 1, max channel)` × `glowSpriteGain`; a tinted strip sprite at half that | fire and flares emit, smoke and debris do not; only the sprite's keyed texels |
| flash disc | the composite colour under the fragment × the disc atlas lane `max(k−1, 0)` × `glowLightGain` | the brightening the disc applied, read from the same atlas texel the disc used (§13.11), so the glow's colour is the lit ground's |
| halo | the composite colour under the fragment × the row's high lane × `glowLightGain`, inside the same disc test `destOpHalo` runs | as above |

The composite is bound as a source image while the glow plane is the
destination, so reading it there is legal and reads the frame as replayed so
far. The emission fragment's alpha is its largest channel, so the plane stays
a valid premultiplied image through the linear-filtered shrinks.

### 19.3 The resolve — contract L2

The batch is additive and order-free, so it rides no phase: sources append
quads (with the world transform of §16.3 applied to their vertices exactly
as the scheduler applies it) to runs keyed by image bindings, and the
resolve runs at most once per frame, at the first of:

1. the fog command, before it compiles — so the grey composite dims the
   glow and the black one hides it, exactly as they treat the light sources
   themselves;
2. the close of the world region — so a frame recorded without a fog
   composite still resolves before the chrome is painted over the world.

A frame with neither (a front-end frame) has no emissive commands and drops
nothing. The resolve submits the scheduler first, so it is a barrier costing
one segment, then:

1. clears the full-frame emission plane and draws the runs into it under
   `BlendLighter`;
2. shrinks it to a half and a quarter of the frame with linear filtering,
   blurs the quarter plane with a separable nine-tap Gaussian (σ = 2 texels,
   ping-pong), shrinks that to an eighth and blurs again;
3. adds the quarter plane (×4, weight `glowNearWeight`) and the eighth plane
   (×8, weight `glowFarWeight`) onto the composite with linear magnification
   under a **screen** blend, `out = src + dst × (1 − src)`. Screen rather
   than additive is what keeps a fireball's own art: an already-white core
   stays white instead of clipping, and the halo shows where the ground is
   darker.

That is nine device passes per frame with something glowing and about 2.4
frames of fill, most of it the two magnified adds; a frame with nothing
emissive costs nothing. The planes are allocated once per frame size; the
emission plane is unmanaged so its texels never depend on an atlas placement
(§13.12 "The defect this round exposed"). Steady-state frames allocate no
options, no uniform map and no geometry here.

The knobs (`glowLineWidth` 4, `glowGain` 1, `glowThreshold` 0.65,
`glowSpriteGain` 0.6, `glowLightGain` 0.35, `glowNearWeight` 0.65,
`glowFarWeight` 0.5, σ 2 over four taps) are presentation choices tuned by
eye on the battle benchmark capture; the first pass at 0.8/0.5 with an
additive composite and no sprite gain blew every fireball to a white blob,
which is the case the screen blend and the sprite threshold exist for.

### 19.4 The switch

`settings.Display.Glow` (`display.glow`, default 1) is a Nanolathe option with
no retail bit. The shell and the capture route apply it with the other
display bits (`applyVisualOptions` → `Client.SetGlow`), the `+glow` chat
command toggles and persists it beside `+antialias` and `+dither`, and every
modern executor site copies the client's switch to the renderer
(`Renderer.SetGlow`) before `Execute`, next to the display palette. A settings
file that omits the key keeps the default because the loader decodes over the
defaults [02 "Settings"]. Off, no source appends and the resolve is a no-op.

### 19.5 Verification

1. **Device fixture.** `glow_test.go` (`NANOLATHE_GPU_DEVICE_TEST=1`): a flat
   field with one emissive stroke inside a rest-factor world region. Off, the
   frame is the exact classic expansion and the counters are zero. On, the
   stroke's own pixels are unchanged (screen leaves white white), the field
   beside it is clearly brighter, the brightening falls off with distance, and
   the far corner is the field to within the blur's last tap.
2. **CI tier.** Unit tests lock the normalized kernel, run-relative indices
   and run splitting of the batch, the stroke quad's geometry, and the
   recorder's emissive marks on beam and segment strokes; `tools/check` green.
3. **Byte-identical off.** The modern battle benchmark (1080p, 120 TPS, 180
   frames) with `display.glow` 0 on this branch against main:
   `battle.png` identical; the M-matrix captures of quiet frames identical
   with the switch on (nothing emissive in frame).
4. **Look.** The benchmark capture with the switch on beside the same frame
   off, cropped at 1:1 around an explosion cluster and around a laser: the
   fireballs keep their texture with a soft warm halo, the Leveler's green
   beam glows, burning trees glow, smoke does not, and the fogged half of the
   map shows the glow greyed.
5. **Cost.** Same benchmark, glow off → on, adjacent runs on a host at load
   ~5: Submit 5.21 → 5.33 ms median, cadence 8.53 → 8.61 ms, OutsideDraw 2.76
   → 2.94 ms, on the 8.8 ms floor 58% → 53%, passes 13 → 22 median, ~570
   emissive quads and 2.2k more Ebitengine objects per frame. GPU time is not
   observable through Ebitengine (docs/BATTLE_BENCHMARK.md), so the outside-
   draw delta is the upper bound on what the nine passes cost the device.

### 19.6 Owed

A human look at the window in motion, where the glow is interpolated with
the sources. Nanolathe spray glow and local illumination are prototyped in
§23.5. Unit-mounted lights still need a material or piece-name signal the
model lane does not carry. The two
magnified adds could be one shader pass sampling both octaves, and the half
plane could go if Ebitengine's mipmapped shrink proves cheaper than a pass;
neither was needed at the measured cost.

## 20. Alt/Option tactical range guides (Enhanced)

### 20.1 Presentation policy

Holding Alt/Option at any modern-renderer zoom, including 1× and 2×, draws
ranges for selected own units and the hovered identified unit.
An armed build product also shows its prospective ranges at the snapped site,
including an invalid site while the player repositions it. This is an Enhanced
UI choice, not a retail hotkey or an authoritative coverage calculation. It
uses the product definition, footprint centre and validated preview height;
arming or displaying a guide never submits a construction order.

Releasing the key, losing focus, switching to classic, opening a modal/result
or entering chat hides the guides. Alt's existing command-group handling stays
intact. A small on-screen legend names only the categories present:

| Guide | Ink | Radius source |
|---|---|---|
| Weapon | Orange, solid | Each independently enabled, active weapon slot's `Range` |
| Radar | Cyan, solid | `RadarDistance` |
| Sonar | Blue, dashed | `SonarDistance` |
| Radar jammer | Purple, dashed | `RadarDistanceJam` |
| Sonar jammer | Pink, dashed | `SonarDistanceJam` |
| Build | Green, dashed | Builder's `BuildDistance` |
| Interceptor guide | Yellow, dashed | Interceptor weapon's `Coverage` |

Equal weapon radii on one unit collapse to one ring. Sonar is dashed so radar
and sonar remain visible when their authored distances coincide. Preview products use all
active authored slots; live units use the committed independently enabled slot
bits. Record-zero NOWEAPON links are omitted even if they author a range.
Stockpiling alone does not select interception coverage. Sensor guides for an
inactive switchable unit become dashed; they describe its nominal capability.

### 20.2 Data boundary and limits

**Established source contracts:** ordinary `Range` is in whole world units
[06 §3.3]; definition activity and independent slot bits are [06 R-WPN-05 §3].
Sensor/jammer readers sign-extend their stored 16-bit fields, while construction
reach zero-extends its 16-bit field [07 R-P0-11 §3]. No unit-name lookup table or
invented weapon range is involved.

**Enhanced guide policy:** these are planning circles. Actual firing also tests
terrain, target restrictions, firing arcs and ballistic feasibility [06 §3.3].
Actual interceptor acquisition uses an inclusive X/Z square about the incoming
projectile's stored aim point [06 §11.2]; its explicitly named coverage circle
is a visual guide. Actual build/repair reach includes footprint terms and differs
from reclaim reach [05 R-WORK-01 §2]. Sensor coverage also depends on activation,
terrain, altitude, water and detection/jamming gates [03 §3.4][03 R-VIS-01 §4–5].
The HUD therefore calls these range guides rather than guaranteed coverage.

At icon zoom the client admits targets through the existing committed strategic
icon layout. At model zoom it applies the same committed visibility and carrier
checks directly, using projected world anchors with the same screen margin.
The latter path does not need an icon catalog or a nonzero marker alpha.
Hidden units, radar-only contacts, carried passengers excluded by that layout,
and off-screen unit anchors expose no definition to the overlay. Selected own units
remain subject to the same friendly visibility policy as §18. Commander-looking
enemy units are omitted so differences in truthful ranges cannot identify a
decoy through otherwise identical icon art. This is deliberately conservative
presentation disclosure, not a simulation change.

### 20.3 Rendering and verification

`TacticalOverlayStage` runs after the committed world, outside its scale region,
before the icons and HUD. The same live zoom and camera origin as icons project
the ring. Each terrain endpoint uses the greater of centre height and sampled
ground height [07 R-P0-11 §3]. One-pixel palette-coloured segments are clipped to
the battle viewport before recording. Wide integer coordinates avoid long-range
wrap; only clipping ratios use transient floating point. Screen-adaptive
32..512 chords per ring bound tessellation, including huge authored ranges.
These bounds, dash pattern and colors are Enhanced presentation constants.
The Alt-off path does not resolve colors, visit targets or record range lines.

Focused tests cover inactive placeholder weapons, independent slot admission,
interceptor versus stockpile data, deduplication, field narrowing, held-key
release/focus loss, prospective product/site selection, invalid placement,
classic fallback, all-zoom committed visibility, terrain projection,
viewport clipping, bounded long-range geometry and pre-icon draw ordering.
`--shot-alt --shot-select` captures selected ranges; `--shot-build armllt`
previews a named product beside the first selection (or at the world viewport
centre without a selection), without an order. The
capture switches apply after simulation and zoom setup, so matched scenes retain
the same simulation state. Full checks and visual results are recorded below.

Initial strategic-only validation on the retail install (2026-09-11):

- Full `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...`
  passed after integrating current main. Independent review's retained-hover
  finding was fixed and covered: drag selection clears hover guides while
  selected own ranges remain.
- Viewed modern 1280×720, 0.5× Ashap Plateau captures with selected commander,
  ARM light laser tower preview and ARM radar preview. The weapon ring is
  centred on the valid snapped footprint; radar's larger cyan guide clips at
  the viewport. Sonar dashes preserve coincident cyan radar segments. Icons
  and the selection halo remain above guides; the category legend stays legible.
- Matched Alt-off strategic, classic and modern native captures are
  byte-identical to main. Both live battle captures are also byte-identical
  before/after, with matching scene metadata and per-frame workload census:
  332..338 units, eight builds and 411..416 visible sprite features.
- Sequential 180-frame, 120-TPS live battle checks at native 1920×1080:
  classic median record 12.259 → 12.254 ms and cadence 14.551 → 14.554 ms;
  modern median submit 5.284 → 5.294 ms and cadence 8.663 → 8.512 ms.
  These exercise the unchanged normal-view path, not active guide cost.
- Active-guide frozen-scene timing remains unmeasured: the repeated-frame
  capture route crashed in the host Metal drawable/texture call on this branch
  twice and on unchanged main. Single-frame modern capture and both live
  benchmark executors completed. This does not establish active-overlay cost
  for a large selection; per-ring tessellation is bounded by the geometry test.

Review artifacts are outside the repository in
`/private/tmp/nanolathe-tactical-review/`.

## 21. Modern resource construction input

The user-requested modern-only resource double-click shortcut is specified in
DESIGN_INTERFACE_HUD_INPUT §3.10. The active executor gates input recognition;
ordinary session build commands and placement validation own all resulting
construction. This is an explicit input convenience beyond visual differences,
not alternate economy, construction or simulation behavior.

All-zoom extension validation (2026-09-11): unit admission and placement tests
now exercise 0.5×, 1×, 1.5× and 2×, with terrain/projection also tested at 0.375×.
The model-view admission test removes the icon catalog and still admits only
the committed visible unit, excluding hidden, radar-only and off-screen targets.
Viewed retail ARM commander and laser-tower placement captures at 1×, 1.5× and
2×; guides remain centred and clipped, with their one-pixel stroke and HUD
legend independent of model scale. Alt-off captures at all four zooms are
byte-identical to the prior implementation.

Sequential native live battle runs (180 frames, target 120 TPS) have matching
scene metadata and byte-identical before/after captures for both executors.
The feature census remains 2547..2549 features, 411..416 visible sprite features,
and eight builds. Classic median record is 11.468 → 11.514 ms; modern median
submission is 4.973 → 4.940 ms. These are Alt-off regression checks. Full build,
vet, formatting and test checks pass after integrating current main. Artifacts
are in `/private/tmp/nanolathe-all-zoom-review/` outside the repository.
## 22. The model lane

### 22.1 What it is

The modern executor's one model path. Its predecessor, the slot-atlas stage
of §11.2, §13.12 and §17, reproduced retail's per-unit composition image
exactly through a key pass, a body pass, reveal, outline, clipping, a
coverage resolve and a residency table, and was the executor's largest CPU
term. The lane keeps what retail's picture needs and drops the rest.
`internal/platform/gpurender/model_direct.go` is the whole of it, plus three
ops in the scene and destination shaders, `scheduler.tris`, the outline row
walk (`model_prepare.go`), the parameter packing (`model_quads.go`) and
the texture page (`model_atlas.go`).

Before Replay, every subject of the frame and its shadow are given a region
of a per-frame 2× atlas page — 4096 × 4096 texels, two planes, shelf-packed
tallest first, no residency — and their faces are fan-triangulated straight
from the packet's projected corners. A 1080p battle frame uses about 1,300
rows of the first page and the 2× detail view about 4,000; a second page
opens when the first is full (128 MiB of device memory a page, allocated
on demand and retained), and only a frame that fills both takes the
fallback. The doubled lane is the recorder's own packet when it carries
one, half-pixel offset included (§17.3); a packet without one has its
native corners doubled here. An attached-unit group composes in one region
over the union of its bounds: the carrier's faces, then each mergeable
child's with its signed height delta added to the keys, saturating at the
byte's range where retail would wrap [03 R-REN-03A §4]. Two passes over ONE
vertex batch draw the whole frame's subjects at once:

1. **Key.** Each face's height key, narrowed to a byte as the span writers
   narrow it, into a key plane under a MAX blend, so a texel holds the
   highest key drawn there. A four-corner face, flat or textured, takes its
   key from the span writer's two-chain mapping of its corners, evaluated
   per fragment from the parameter image; any other ring interpolates its
   lanes linearly. The key shader reads positions, the key lane and the
   parameter image, so the batch is the colour pass's own.
2. **Colour.** Each face's texel where its own key is not below the stored
   one — retail's `stored ≤ incoming` admission [03 R-REN-03A §2] — into a
   colour plane, faces in RECORDED order so a tie goes to the later-drawn
   face as retail's does. That tie is what puts a solar collector's base rim
   over its open panels, which lie at its height; a painter's sort by mean
   key lost it. A mapped face's key is the same mapping the key pass wrote,
   so the passes never disagree about a texel. Shadow silhouettes draw in
   their index with no key test. The nanoframe reveal, the waterline tint
   and the Digger erase are verdicts on the height key
   [03 §5.2][03 R-WATER-01 §2][03 R-REN-03A §8]; the fragment evaluates
   them on the PIXEL's key — the stored key at the block's top-left texel,
   which is the key retail's 1× image holds after the 2:1 resolve samples
   it [03 R-REN-03A §6] — so all four texels under a pixel take one verdict
   and a band one key wide resolves to whole pixels. A replaced reveal
   index is written flat, as retail rewrites its plane after shading. A
   carried child's own verdicts read its own key (the entry carries the
   delta, taken back off at the fragment) and the carrier's waterline and
   Digger then clip the child on the shifted key, which is what the
   staging image's passes do [03 R-REN-03A §4]; the classic composer runs
   the child's own pass and then the carrier's, and the lane follows it.
   The subject's verdicts ride the parameter image beside the mapped
   faces, eight texels of a twelve-texel entry. Outline endpoints come
   from the span writer's own row walk over the NATIVE packet
   (`prepareModelOutline`) and draw as one pixel block each, key-tested
   once against the pixel's key, between the cached and live lanes in
   retail's order [03 R-COMP-01 §3][03 R-REN-03A §4]; the doubled lane's
   own rows would put an endpoint at a doubled column, straddling two
   pixels, so the recorder no longer builds them.

Replay then compiles, per subject and in record order through the scheduler,
a shadow commit and a body commit. A structure's shadow commit
(`destOpModelDirectShadow`) resolves the four silhouette texels under a
pixel from the shadow's page, punches a pixel whose body block — read from
the body's page, which may be the other one — is wholly covered, and
composites the ALP half-colour fragment [03 R-REN-03D §4–§5]. A Digger's or
a mobile's shadow is retail's copy of the finished body: the recorder emits
a faceless packet (`Silhouette`) with the body's box, the shadow anchor and
the buried or submerged clip key, and its commit
(`destOpModelSilhouetteShadow`) resolves the body's own colour texels at the
shadow's placement — cached lane, live lane and staged children, as the
classic copy holds them — erasing a texel at or below the clip key read from
the key page, and composites the half-colour of index 0 [03 R-REN-03D §1].
It spends no region, no faces and no projection; it binds the colour page in
the projected shadow's slots so the two kinds share a run. The body
commit (`sceneOpModelDirectCommit`) box-resolves the four texels under a
pixel: colour the mean of the covered ones, alpha their share — §17's
coverage resolve done in the commit, for models only; sprites and terrain
are untouched, as terrain is authored to be drawn as is.

| retail | model lane |
|---|---|
| per-pixel height key, `stored ≤ incoming` | the same test against a max-blended key plane; ties by recorded order |
| back faces culled by ring winding | same rule, same sign |
| SHD row lookup per texel | `0.06875 × row` interpolated across the face (§13.2); the table's nearest-index rounding is the visible difference |
| textured quad by two-chain span mapping | the same mapping, evaluated per fragment from the parameter image (§11.2 "Textured quads without strips"); flat quads mapped the same way for their key and shade |
| inclusive span fill | corners on the far side of the face centroid pushed one 2× texel (the span's inclusive right and bottom ends); a linear textured face clamps its texel to its authored bounds |
| composition transparent index 1 | dropped at the fragment |
| reveal, waterline, Digger over the 1× image after the resolve | the same verdicts per texel on the pixel's nearest-sampled key |
| outline endpoints written at 1× after the resolve, key-tested once | native rows drawn as pixel blocks, key-tested once against the pixel's key |
| structure supersample (§17), ALP downscale blending with index 1 | every subject at 2×, resolved in the commit fragment by coverage: an edge or a thin feature is a coverage alpha over what is beneath, never the red/purple fringe [03 R-REN-03A §7]; a mobile subject is supersampled too, where retail draws it at 1× |
| structure shadow punched by body coverage | same punch, both planes resolved from the pages |
| Digger and mobile shadow: the finished body image copied, flattened, clipped, blitted at the ground point five pixels right | the body's own raster read at that placement in the commit fragment; a mobile is never punched, as retail's is not |
| child composed alone, its erased pixels transparent, then `prior > key + delta` keeps prior, wrapped store | child faces in the group region under the same admission with the shifted key; the sum saturates instead of wrapping; a child texel its own reveal or clip erases stays a hole where retail shows the carrier through it |

A subject no page can hold falls back to painter-order native triangles
straight on the composite (no key, no supersample, no reveal, no children)
and its shadow is omitted; the benchmark never overflows at either view.
The parameter image grows to what a frame uses up to 2,048 rows (174k
entries); a frame past it draws its remaining faces linearly. The commit
quads bind only the colour plane, so they share a run with the sprites
around them.

The recorder feeds the lane once per display frame. Two of its per-unit costs
went in the same round as the silhouette shadow: every cached unit projected
all of its faces again each frame only to measure its composition box, which
is now the retained lane's envelope unioned with the live pieces' extent
(`retainedModelExtent`), and every face resolved its texture through a
name-key map, a lowercase scan and two index lookups, which is now a
per-model table (`modelTexRefs`) keyed on the compiled model and rebuilt when
the client installs new indices — units and features only, because a
projectile or debris draw is one piece copied into a scratch model every such
draw reuses.

The isolated model preview exercises the lane's verdicts without a session:
`--shot-model-build-remaining` poses a nanoframe; `--shot-model-world-height`
sets the world height, and as the preview has no map its sea level is zero,
so a negative height submerges the model and runs the waterline erase;
`--shot-model-underwater-exempt` sets the sonar-contact bit that turns the
erase into the blue tint; a Digger definition (`armamb`, `cortoast`,
`corvipe`) brings its own clip. The preview's list opens with its clear and
background fill, so both executors compose over the same background, and
`--shot-renderer=both` keeps the classic structure supersample: the
Enhanced classic image is the like-for-like reference.

### 22.2 Measured (battle benchmark, 1080p, 120 TPS, 180 frames, one binary, back to back)

The last pair before the slot stage was removed, host load 3–4:

| median | slot stage | model lane |
|---|---|---|
| Submit | 5.25 ms (p95 6.70, max 7.27) | 4.39 ms (p95 4.68, max 4.89) |
| Cadence | 8.71 ms (p95 21.0, max 22.8) | 8.45 ms (p95 12.0, max 12.9) |
| OutsideDraw | 3.08 ms (p95 9.8) | 2.35 ms (p95 3.0) |
| on the 8.8 ms floor | 52% | 75% |
| alloc / objects per frame | 1.34 MB / 29.5k | 0.81 MB / 11.2k |
| device draws / passes | ~250 / 24 | 102 / 14 |

Pixel diff against the slot stage: 4.0%, all on model bodies and shadows;
isolated tank models differ on 0.2–0.4% of pixels, a solar collector on 2%,
all of it shade rounding. At 4× the silhouettes are as smooth, texture rows
are straight, the solar collector's base rim cuts through its open panels,
shadows fall where retail puts them, and a nanoframe's fill, outline and
band match. The capture after the removal is byte-identical to the last
prototype run's.

Stages measured on the way, each a back-to-back pair: bodies only on a keyed
atlas (load 12) Submit 5.65 → 4.89 ms; shadows on the lane 5.26 → 4.63;
reveal and clipping 5.41 → 4.53; native painter's triangles on the composite
(no key, no supersample) 4.84 → 4.23. Ebitengine's triangle `AntiAlias`
option was tried and rejected: cadence 43 ms, OutsideDraw 37 ms.

The follow-up round (pages, mapped keys in both passes for every quad,
block-key verdicts, native-row endpoints, the group clip; host load 3.3):

| median | before | after |
|---|---|---|
| Submit | 4.37 ms (p95 4.67, max 4.85) | 4.14 ms (p95 4.54, max 4.76) |
| Cadence | 8.56 ms (p95 11.6) | 8.58 ms (p95 11.7) |
| OutsideDraw | 2.30 ms (p95 3.0) | 2.35 ms (p95 3.1) |
| on the 8.8 ms floor | 72% | 70% |
| alloc / objects per frame | 0.81 MB / 11.0k | 0.78 MB / 10.4k |
| device draws / lane faces / vertices | 101 / 24.7k / 222k | 85 / 22.3k / 203k |

Evaluating the two-chain mapping in the key pass as well, and for flat
quads, shows in none of the cadence figures; the native-row endpoints halve
the outline quads, and the endpoint run now binds the texture page and
merges with the faces' runs, which is the draw count. The 2× detail view
peaks at 4,040 rows of the first page on the benchmark scene, so the second
page is exercised by the fixture, not the benchmark.

The recorder round, two pairs each against the binary before it (host load
3–4). The box measurement and the per-model texture table:

| median | before | after |
|---|---|---|
| PreRecord | 3.15 ms (p95 3.90) | 2.72 ms (p95 3.51) |
| miss Record | p95 3.42 | p95 3.18 |
| Cadence | 8.46 ms (p95 12.1) | 8.46 ms (p95 11.6) |
| alloc per frame | 0.81 MB | 0.79 MB |

The silhouette shadow (Digger and mobile shadows from the body's raster):

| median | before | after |
|---|---|---|
| Submit | 4.09 ms (p95 4.47, max 4.69) | 3.01 ms (p95 3.26, max 3.45) |
| PreRecord | 2.66 ms (p95 3.28) | 1.97 ms (p95 2.52) |
| Cadence | 8.46 ms (p95 10.9, max 12.8) | 8.40 ms (p95 9.2, max 9.8) |
| OutsideDraw | 2.13 ms | 1.64 ms |
| alloc per frame | 0.79 MB | 0.72 MB |
| device draws / lane faces / vertices / atlas rows | 86 / 22.3k / 203k / 1,160 | 86 / 13.3k / 131k / 736 |

The capture changes on 1.9% of pixels, all shadows of mobiles, and moves
closer to the classic capture of the same frame (pixels differing from it
by more than 40 levels: 39.8k → 38.4k); run to run it is byte-identical. The
first cut bound the key page in the shadow commit's slot 2 and the second in
slot 0, which split the destination runs (108 and 238 draws); the commit
binds the colour page in the projected shadow's slots and takes the key page
only for a clipped silhouette. The first cut also overwrote its scratch
packet wholesale, dropping the face arena a live-lane packet retained in the
same slot, which regrew every frame (+0.26 MB per frame).

### 22.3 Verification

`model_direct_test.go`: a device fixture (in the hidden loop) locks the key
test against draw order, the tie rule, the reveal verdicts written flat, the
waterline erase and the blue tint at and below their key, the Digger erase,
a carried child's reveal on its own key under its carrier's clip on the
shifted key, an outline endpoint drawn whole against the pixel's key (and
rejected under a higher body key), the shadow half-blend beside its body and
the half-covered far edge, and a body whose shadow is its own silhouette
placed beside it with a clip key (the low half casts nothing, the high half
the half-blend of index 0, no region spent) — run once plainly and once behind a page-wide
filler so every subject draws and commits from the second page; a unit test
locks the shelf packer and its page turn. `model_quads_test.go` locks the
parameter packing. The classic executor remains the byte-exact reference for
retail's composition; the lane's departures are the table above.

Isolated previews against classic, `--shot-renderer=both`: a submerged
submarine (`armsub` at world height −6) erased and, with the exemption bit,
blue-tinted; a submerged solar collector; and the pop-up `armamb` erased at
and below its origin — inside each model the two images agree, and every
difference of more than a few levels sits on an edge or a thin feature,
where the lane's coverage alpha stands against classic's fringe blend (a
structure) or its 1× raster (a mobile). The ARMLAB nanoframe's "two missing
band pixels" of the first landing were of that kind: half-covered texels of
the doubled geometry at the reveal notch, which classic without the
supersample leaves empty and classic with it blends with index 1.

### 22.4 Owed

1. The first explosion/model/smoke lighting prototype is specified in §23.
   Richer material response and per-pixel lighting remain future work.
2. A carried child texel that the child's own reveal or clip erases stays a
   hole where retail shows the carrier through it, because the child's key
   reached the group's key plane; reproducing that needs the child composed
   in its own region and merged under the staging admission. Only a
   transport's cargo is carried in this build and it is a finished unit, so
   no stock scene reaches it.
3. The recorder still builds the doubled packet's faces. A packet with a
   doubled lane has its native faces read only by the fallback and the
   bounds; the doubled corners of a direct projection are exact rather than
   native × 2 plus an offset, so the offset alone cannot replace them.

## 23. Battle lighting prototype (Enhanced)

### 23.1 Scope and inputs

This is a user-authorized presentation design, not retail evidence. The first
prototype adds coloured diffuse light to model faces and soft illumination to
smoke around visible explosions and weapon impacts. It keeps the projection,
existing shade, silhouette, composition key, fog, effect lifetime and simulation
unchanged. Shadows from point lights, terrain relighting, material masks and
reflections are later work. The local brainstorm is intentionally uncommitted.

**Contract BL1 — sources.** Named art for explicit explosion, impact and water
impact events contributes only while the primary animation is active and its
frame resolves. Source metadata additionally requires PointVisible at the event
position for the current viewing player; absent visibility fails closed. This
extra gate changes light emission alone, not the original art draw. The
calculated secondary flash and generic glow flag are not additional sources:
counting both layers would double an explosion and the glow flag also marks
smoke. Smoke-puff and vent-steam strip families are receivers; art colour does
not identify their producer. Existing effect and strip composition remains
[03 R-FX-01], [03 R-FX-02], with light applied beneath the existing fog boundary.

**Contract BL2 — physical coordinates.** Model faces carry outward normals in
world X, world Z, height axes. The producer computes the standard cross product
from transformed vertices, then mirrors model Z to match the visual projection
[03 §2.5]. Corners carry model-relative height in recording-scale pixels,
independent of the wrapping composition key. Every packet refreshes its current
absolute origin height, including retained, direct and attached subjects.
Supersampling doubles raster coordinates only: physical height and normals stay
unchanged. Adding half the absolute height to projected Y recovers unsheared Y
for receiver-minus-source distances. The same final world transform applies to
the lit subject and source, so lighting is evaluated in record coordinates.

### 23.2 Bounded lighting and composition

**Contract BL3 — artistic response.** Before model preparation, the executor
borrows source metadata recorded once before composite art is decomposed into
leaf blits. It caches each immutable art frame's emission colour:
covered texels above maximum-channel brightness 0.45 receive squared weights
((brightness − 0.45) / 0.55)². Their weighted mean RGB is scaled by the square
root of mean weight across all covered texels. Thus dark trailing animation
frames lose energy, and colour comes from the displayed palette. Palette changes
clear the cache; source reset releases it. Frames below 0.015 peak emission are
ignored. Composite frames are measured on a bounded 32×32 sampling grid,
compositing their ordered leaves over black with the authored keyed/half-alpha
selection. Overwritten bright leaves do not emit, and one composite consumes
one light budget slot. This is an approximate intrinsic emission measurement,
independent of the ground behind a translucent effect. These thresholds are presentation choices, not authored material data.

The source radius is 1.4 times the larger frame dimension, clamped to 48–192
world pixels at record scale, then extended by 50% (72–288 world pixels).
This extends both model and smoke illumination without changing their gains
or the light budgets. The point is lifted one quarter of the frame
height above the event, with its ground position fixed, to represent the bright
volume above an impact. At most 64 sources survive per frame, retaining the
strongest with stable ties. Each subject chooses at most eight nearby sources
using a conservative projected bound and distance-weighted source strength.
Each face evaluates those sources at its physical centroid, with outward
Lambert response and squared radial falloff (1 − distance² / radius²)², gain
3.25 (30% stronger than the initial prototype). Back-facing faces receive zero;
distance at or beyond the radius receives zero. Contributions sum and are
capped at two per channel before storage.

**Contract BL4 — existing passes.** A constant numeric RGB code carries the
face's contribution through the otherwise unused fourth custom vertex lane in
the atlas colour pass. This is three base-128 digits for [0,2] channel values,
not a float bitcast. Its 21-bit maximum leaves device-float rounding
headroom at channel carry boundaries. The native overflow fallback carries the
same code in its unused third custom lane. The shader adds albedo times incident
light to existing shaded colour and clamps to one. Zero contribution uses the
previous colour expression exactly. Outline endpoints and shadow silhouettes
receive no light; key, reveal and waterline processing retain their order.
No new model pass, texture or per-frame uniform map is needed.

Smoke retains gain 2.5 and uses the same nearby sources and radial falloff with
a soft response of 0.8 independent of facing. Its four clipped corners carry RGB contributions
through the existing tinted-sprite vertices; interpolation supplies the interior.
The fragment adds (0.2 + 0.8 × source colour) times light to the smoke colour,
clamps it, and preserves the original one-half premultiplied alpha. The source
transparency mask, blend order and fog are unchanged. Smoke is never promoted to
a light source. This approximates scattering without volumetric geometry.

### 23.3 Verification and limits

`SetBattleLighting` is an executor-level comparison control; Enhanced enables
the prototype by default. It adds no simulation mode or saved setting.
`BattleLights`, `LitModelFaces`, and `LitSmokeSprites` are frame diagnostics.

The 50% radius extension was visually checked against the preceding 3.25-gain
build in the matching 2× Great Divide battle (60 FPS, nearest-doubled art).
Median submission time was 3.592 → 3.689 ms, p95 3.931 → 4.138 ms; median
cadence was 16.694 → 16.775 ms. Median lit model faces rose from 1,089.5 to
2,038.5. Scene metadata and all frame censuses matched. Classic's matching
native capture remained pixel-identical. These are one pair of host timings,
not GPU timing or a guarantee for every battle. Build, renderer vet and the
existing GPU device fixtures passed. Captures and timing data are under
`/private/tmp/nanolathe-lighting-{radius-before-modern,radius50-modern,radius50-classic}`.

The synthetic tier verifies outward roof winding, record-scale heights across
retention and supersampling, explicit source/family classification, missing or
hidden visibility exclusion, directional falloff, common translation/scale,
owned list replay and shader compilation. The existing opt-in real-device loop
also checks lit versus back-facing model surfaces, lit versus untagged smoke,
unchanged output after disabling the prototype, and can write comparison PNGs
through `NANOLATHE_LIGHTING_SHOTS`. Run `tools/check`, `tools/check-retail`, and
`NANOLATHE_GPU_DEVICE_TEST=1 go test ./internal/platform/gpurender` before landing.
Use sequential matching classic/modern live battle benchmarks and inspect their
census, captures and host timings; GPU duration is unavailable through this API.

The face-centroid response is intentionally coarse and has no light occlusion
between visible objects. Smoke uses flat sprite depth. Material-specific
reflectance, per-pixel normals and additional emitter families remain outside
this prototype. The source visibility gate prevents hidden events from lighting
visible receivers; ordinary final fog still controls receiver presentation.

### 23.4 Prototype outcome

Reviewed on Apple M3 Pro / darwin-arm64 using the scene-version-4 Great Divide
battle, seed 7, 1920×1080, factories enabled, 300 pre-ticks, 60 target draws/s,
120 warmup draws and 180 measured draws, automatic detail art disabled. Baseline
is `bca5042`; final executable is `9d79216`. Metadata and every frame's simulation
census match within each pair, including feature/fire, movement, construction
and camera state. The classic capture is byte-identical. Modern changes 57,818
pixels at native zoom and 87,923 at 2×; both before/after views were inspected.

| View | Submit median / p95 / max, ms (before → after) | Cadence median, ms | Allocation MB/frame |
|---|---|---|---|
| Modern native | 3.319 / 3.752 / 4.309 → 3.692 / 4.161 / 4.585 | 16.681 → 16.786 | 0.817 → 0.868 |
| Classic native | 0.461 / 0.639 / 0.767 → 0.461 / 0.644 / 0.730 | 17.156 → 17.189 | 1.163 → 1.166 |
| Modern 2× | 3.163 / 3.384 / 3.677 → 3.528 / 3.809 / 4.153 | 16.673 → 16.696 | 0.996 → 1.005 |

Modern native Record p95 is 2.946 → 3.122 ms (max 3.181 → 6.931); classic
Record median/p95/max is 12.144/16.244/23.400 → 12.624/15.952/18.412 ms.
These short samples show bounded added submission work, not GPU execution time
or a performance improvement. Native frames contain 63–64 selected sources,
723–1,316 illuminated model faces and 173–250 illuminated smoke sprites;
the 2× view contains 704–1,317 illuminated faces and 87–176 illuminated smoke
sprites. Artifacts are outside the repository in
`/private/tmp/nanolathe-lighting-{before-modern,final-modern,before-classic,final-classic,detail-before,detail-after}`.

`tools/check`, full retail-tagged vet/tests with installed assets, and the
complete real-device fixture loop pass. `tools/check-retail` stops at its lint
stage on four existing unused content helpers (`buildModelCatalog`,
`requiredModelPaths`, `fillBuildPages`, `sortedUnitKeys`); `tools/lint` on
unchanged main reproduces the same failures. Retail tests were therefore run
separately and passed. The lighting code introduces no new lint finding.

### 23.5 Nanolathe glow and local illumination prototype

User-authorized Enhanced presentation design; these are artistic choices, not
retail evidence. The source remains the committed strip-6 particles of
[03 §5.5], with their established two-pixel marks, palette ramp, motion,
coverage gate and draw order. The nanolathe event itself adds no synthetic
beam or additional particle lifetime.

**NL1 — source admission.** Only fills explicitly tagged by the nano strip
family emit. The tag is attached after the existing PointVisible gate and
carries absolute particle height, recording scale and recording viewport.
Ordinary fills and impact sprinkles never emit, even with the same palette
colour. A particle whose core misses the recorded viewport contributes neither
bloom nor lighting. The viewport travels with the fill because source gathering
precedes world-region replay; fractional zoom can record beyond the device's
pixel extent. Fill ownership, clone and reset retain or release the metadata
with its pixels.

**NL2 — spray glow.** The existing glow source pass receives a palette-coloured
quad extending two world pixels beyond each side of the particle core, at gain
0.45. The existing two blur octaves resolve it beneath fog and interface.
The live world transform applies once, just as for the particle. No shader,
render target or extra blur pass is added. The existing glow switch controls it.

**NL3 — local lighting.** Before model preparation, visible particles join the
nearest existing cluster within 24 world pixels in unsheared physical space.
Clusters follow the arithmetic mean of their particles' positions, with no
screen-grid snapping. Each particle adds 0.06 times its displayed palette RGB;
the completed cluster is uniformly scaled down if its peak exceeds 0.7.
Its radius is 80 world pixels. These parameters are presentation tuning.
At most 64 clusters occupy fixed scratch storage; later particles may join an
existing cluster but cannot open a 65th one. Clusters then compete with
explosions for the existing 64-light budget by peak energy and stable ties.
Subject selection, outward face response, smoke scattering and radial falloff
are the existing BL3–BL4 path. Dense overlapping clusters may add together;
there is no per-builder brightness normalization. All source state is rebuilt
from the current recorded particles, so energy varies with the looping palette
shimmer and disappears when those particles expire. The battle-lighting comparison
switch disables both explosion and nano lighting; glow remains independent.

The same limitations as §23.3 apply: face-centroid lighting, no occlusion
between models and no terrain relighting. Particle grouping is bounded and
record-order dependent, so overloaded scenes may drop distant construction
sources. No new simulation behavior, RNG calls, asset requirements or saved
settings are introduced.

### 23.6 Nanolathe prototype verification

The synthetic and real-device fixtures check visibility admission, palette
classification, physical height and scale, clustering, recorded-viewport
clipping, clone/reset, green illumination on a facing model surface, an unlit
back face, a halo beside the particle cores, and restoration when the effects
are disabled. Independent read-only review found no implementation issues;
the description was corrected to call the looping particle ramp a shimmer.

Matching scene-version-4 Great Divide runs used seed 7, 1920×1080, native zoom,
60 target draws/s, 300 pre-ticks, 120 warmup draws and 180 measured draws,
with factories enabled and auto-remaster disabled. Every frame's census and
all scene metadata matched within each before/after renderer pair. Features,
fire and active factory construction were present; both captures were visually
inspected. The classic capture is byte-identical. Modern adds green light to
the factory bays and nearby surfaces while keeping the existing particle cores.

| Executor | Submit median / p95, ms (before → after) | Cadence median / p95, ms (before → after) |
|---|---|---|
| Modern | 3.744 / 4.073 → 3.508 / 3.808 | 16.664 / 17.017 → 16.661 / 17.549 |
| Classic | 0.458 / 0.643 → 0.475 / 0.591 | 17.204 / 19.669 → 17.210 / 20.917 |

This single pair shows no measurable submission regression; the lower modern
submission time is host variation, not an optimization claim. Device GPU time
is unavailable. The source budget stays at 64 total; the final modern frame
adds 254 glow quads and lights 2,169 model faces versus 1,929 before.
Artifacts are in `/private/tmp/nanolathe-nano-{before,after}-{modern,classic}`.

### 23.7 Metallic glint (Enhanced)

This user-requested prototype is an Enhanced presentation choice, not retail
material evidence. It adds a small directional highlight using the existing
outward face normals. Classic and authoritative data are unaffected. No
material identity is inferred as fact from an asset's colour.

One fixed unit half-vector, (-0.35, -0.15, 0.9246621) in world X/Z/height axes,
defines an artistic overhead key. The clamped normal dot product is squared
five times (power 32), once per rendered face. There is no camera-position,
clock, RNG, per-pixel normal, point-light loop or additional geometry. Rotating
panels change their response; a stationary panel keeps its highlight.

The face-constant ColorG attribute holds the original palette byte plus 256
times the rounded 0–255 highlight weight. Both the atlas body shader and the
native overflow shader decode those sixteen numeric bits; the existing packed
RGB battle light retains its original precision. Shadows and outline endpoints
carry zero highlight. Construction bands that replace material colour suppress
it. Palette lookup, waterline, transparency and composition ownership retain
order, with the highlight applied to surviving model colour under final fog.

The shader masks dark seams with a brightness smoothstep from 0.12 to 0.35,
and saturated paint with one minus a saturation smoothstep from 0.2 to 0.65.
Its highlight tint is 35% white plus 65% albedo, with peak gain 0.48; RGB clamps
to one and keeps the original coverage. These are tunable artistic choices,
not authored metalness or roughness. Neutral painted panels can consequently
look metallic too. There is no shadow occlusion or map-specific sun direction.
No passes, textures, uniforms, normal buffers or per-frame allocations are added.

The effect starts enabled in the modern renderer. `NANOLATHE_METAL_GLINT=0` selects the
original appearance for comparisons. Ctrl+Shift+G toggles it during modern
window play and benchmark viewing; the terminal reports the state. Paused-world
reuse is invalidated immediately. The setting is temporary and is never saved.
`tools/try-metal-glint` opens the game; `tools/try-metal-glint battle` watches a
20-second seeded benchmark battle with the same toggle. Additional arguments
pass through (for example `--zoom=2`). Benchmark metadata records initial state
and rows record the actual state, so manually toggled runs remain identifiable.

Validation: synthetic facing and byte-packing checks; a real-device neutral,
saturated, dark and transparent material fixture; exact off/on/off restoration;
matching classic and modern Great Divide battle captures at native and 2× zoom.
The open and closed synthetic ARMSOLAR captures both exercised the GPU lane
(one subject, no skipped or overflow subjects); the open base remains visible
between the panels. An injected-input hidden window run confirmed one toggle
per held chord. A host input test also checks that releasing modifiers first
cannot leak the still-held G to game/chat input. A second reviewer inspected
the diff and reran renderer/host/docs tests and the real-device fixtures.

Measured on Apple M3 Pro, Metal, darwin-arm64, using scene-version-4 Great
Divide, seed 7, 1920×1080, 300 pre-ticks, 60 target draws/s, 120 warmup draws,
180 measured draws and nearest-doubled detail art. Repeated runs are sequential.

| View | Submit median / p95 / max, ms (off → on) | Cadence median, ms (off → on) |
|---|---|---|
| Native pair 1 | 3.767 / 3.971 / 4.123 → 3.803 / 4.044 / 9.514 | 16.666 → 16.695 |
| Native pair 2 | 3.712 / 4.015 / 4.468 → 3.755 / 3.953 / 4.051 | 16.667 → 16.688 |
| Detail 2× | 3.658 / 3.838 / 3.951 → 3.622 / 3.868 / 5.273 | 16.661 → 16.634 |

Every frame's census and renderer counters match within each off/on pair:
no extra draw calls, passes, atlas pages or geometry. The native disabled
capture is pixel-identical to unchanged main; the final classic capture is
also pixel-identical to main with matching censuses. Comparing main and the
final enabled native build gives 3.813 → 3.797 ms median submission and
16.761 → 16.685 ms cadence. These are short host measurements, not GPU time
or evidence of a speedup; the few-hundredths-of-a-millisecond median changes
are within run variation. The isolated 9.514 ms submission maximum in the
first on-run did not recur in the second or final native runs. Per-frame
allocation measurements overlap (0.843–0.885 MB native); no effect-specific
per-frame allocation is introduced by the implementation.

The visual result is a modest facet highlight, more legible at detail zoom.
It is worth human evaluation as inexpensive polish, but is not a physical
material model: texture neutrality is an aesthetic proxy and a static roof
can keep a fixed sheen. `tools/check`, `tools/check-retail`, and the real-device
gate passed. The final shortcut adjustment passed the host package tests.
The separate frozen-list timing gate is blocked on this host: the supported
battle capture route (`--map="ashap plateau" --shot-ticks=60 --shot-select
--shot-size=1920x1080 --shot-gpu-profile-frames=180 --renderer=modern --zoom=2`)
crashes while acquiring the Metal drawable texture, both with the prototype
disabled and in the unchanged-main binary. Its logs are
`/private/tmp/glint-frozen-{off,main}.log`. The isolated model capture route
explicitly rejects profiling, so no frozen-list timings are claimed. Resolving
the existing capture-driver failure would settle that remaining gate; live
battle runs and unprofiled model captures completed normally.
Local evidence is in `/private/tmp/glint-{off,on}-native-{1,2}`,
`/private/tmp/glint-{off,on}-detail`, `/private/tmp/glint-main-{native,classic}`,
and `/private/tmp/glint-final-{native,classic}`; screenshots remain uncommitted.
Landing verification also integrated the subsequent vegetation-wind removal.
Fast, retail and real-device gates passed on that combined tree. Sequential
matching native Great Divide runs retain equal frame censuses and renderer
counters; classic and disabled modern captures remain pixel-identical to the
updated main baseline. Modern submission median/p95/max was
3.550/4.011/5.930 ms disabled and 3.767/3.999/4.174 ms enabled in this pair;
both renderer captures were inspected. Evidence is local in
`/private/tmp/glint-land2-{base-modern,base-classic,off-modern,on-modern,on-classic}`.

The user visually approved this appearance and authorized its inclusion in main.
The comparison controls remain available; classic rendering is unchanged.

## 24. Wind in vegetation (removed prototype)

Removed at the user's request after live review. The initial filtered sprite
bend blurred the foliage; replacing it with integer row shifts preserved colors
but looked glitchy in motion. Vegetation now uses the ordinary static sprite
path. The prototype's name heuristic, presentation filter, sprite displacement,
GPU operation and dedicated wind snapshot payload were removed with that
prototype. The coastal treatment in §26 separately publishes the existing wind
for water motion. The simulation's wind behavior remains unchanged.

The abandoned implementations and their visual/performance checks remain in
Git history. Future vegetation animation would need a separate art decision;
this section does not prescribe a replacement effect.

Removal checks include the normal GPU device fixtures and matching native
Great Divide battle runs: seed 7, 1920×1080, 60 draws/s, 300 pre-ticks,
120 warmup draws and 180 measured draws, factories and auto-remaster enabled.
Metadata, every frame's census and final session diagnostics match within both
renderer pairs; classic captures are byte-identical. Both restored captures
were inspected. Modern submit median was 3.674 → 4.043 ms; a reverse-order
repeat was 6.534 → 3.746 ms. These short pairs are too variable for a performance
conclusion. Artifacts are outside the repository at
`/private/tmp/wind-removed-{before,after}-{modern,classic}` and
`/private/tmp/wind-removed-repeat-{before,after}-modern`.

## 25. Explosion distortion prototype

This is a user-authorized modern presentation experiment, not retail evidence.
It uses §23's resolved, player-visible primary explosion/impact art sources.
The recorder adds elapsed ticks since the published StartTick (plus the existing
presentation fraction when interpolation is enabled) and the maximum authored
animation extent in world pixels, cached once per immutable entry. Classic ignores this metadata.
No persistent emitter history, wall clock, simulation mutation or RNG is used;
replaying a list, pausing, changing cameras and restarting cannot restart a wave.
A finished or newly hidden primary animation stops contributing immediately.

Art at least 64 world pixels across produces a ring lasting 15 simulation ticks
(half a second at normal speed). Its radius grows linearly from 12 pixels to
2.5 times the art extent, clamped to 120–320 pixels. Band half-width is
10 + 0.12 times art extent. Displacement strength is min(age, 1) times
(1 − age/15)² times min(extent/16, 7). All lengths take recording scale and the
same final zoom transform as the source. These are artistic tuning choices.
The bipolar radial profile has zero displacement at either band edge; bilinear
sampling lets fractional displacement fade smoothly instead of snapping off.

The executor retains at most 32 strongest rings with stable source-order ties.
It flushes the existing glow/world work, copies the world once into the existing
fog scratch surface and draws clipped ring quads in one batch before fog and
chrome. Tree heat shares that copy and batch, appended after every ring (§27.2). Shader discards leave every pixel outside a ring untouched. Sampling is
clamped to the source's world clip. Overlapping bands read the same snapshot;
the last submitted band wins where they overlap, without recursive refraction.
There is no extra copy or draw in frames without either visible active rings
or tree heat, and no new full-frame image. BlastWaves reports the submitted ring count.
SetBlastDistortion is an executor-level comparison switch, enabled by default.

Verification: source admission/age/scale and bounded lifetime checks, shader
compilation, and the existing opt-in GPU device loop exercise the ring against
a patterned background, including clipping, replay, disabling and expiration.
Run tools/check, tools/check-retail, the GPU device loop and sequential matching
classic/modern battle benchmarks; inspect captures and source counts as well as
host timings. GPU execution timing is unavailable through Ebitengine's API.

### 25.1 Prototype verification

Implemented on the blast-distortion branch, with main's nanolathe lighting
integrated. The source-size scan uses the entire animation because the installed
large fireballs open at only 4–22 pixels and grow to 66–126 pixels; measuring
only frame zero excluded them. Source gathering carries scalar metadata into
the owned draw list. Budget selection happens after final viewport clipping,
so offscreen explosions cannot suppress visible rings; removing the latest
weakest source preserves earlier ties and survivor composition order.

The fast gate, full retail gate (including lint), and real-GPU fixture loop
passed on the integrated implementation. Independent review checked the source
cache lifetime, timing and scaling, shader coordinates, fog ordering and budget
selection. Synthetic GPU captures show the full attack/expansion/fade, unchanged
pixels beyond the clip and HUD, identical repeated replay and exact expiration.
Native and 2× battle captures were visually inspected.

The final comparison used main dbf410c and integrated code 992171f: scene
version 4, Great Divide, seed 7, 1920×1080, factories enabled, 300 pre-ticks,
60 target draws/s, 120 warmup draws and 180 measured draws, auto-remaster off.
All scene metadata and every frame's census matched within each renderer pair.
Both retained sprite features, fire, movement, damage, projectiles, effects,
construction and nanolathe activity. Classic pixels are identical. The modern
2× capture changes 55,591 pixels and the measured frames contain 0–8 waves;
the earlier native comparison contained 0–11 waves.

| Executor | Submit median / p95 / max, ms (before → after) | Cadence median / p95 / max, ms (before → after) |
|---|---|---|
| Modern 2× | 3.571 / 4.061 / 8.687 → 3.622 / 3.912 / 4.016 | 16.664 / 17.113 / 17.606 → 16.658 / 17.062 / 17.641 |
| Classic native | 0.457 / 0.530 / 0.690 → 0.458 / 0.498 / 0.706 | 17.152 / 19.113 / 21.972 → 17.182 / 20.246 / 33.248 |

These are short host measurements, not GPU timings or a performance guarantee.
Earlier runs varied substantially, including frames with no active wave; the
final runs were repeated after verification workloads finished. The prototype
adds one world copy and one clipped wave batch only when needed. Captures and
profiles are under `/private/tmp/distortion-integrated-{before,after}-{modern,classic}`;
the comparison crop is `/private/tmp/distortion-final-comparison.png` and the
synthetic motion preview is `/private/tmp/distortion-wave.gif`.

## 26. Coastal water prototype (Enhanced)

This is an authored Nanolathe presentation experiment, not a retail behavioral
claim. Original water is painted terrain plus script-emitted strip-2 sprinkles
[03 R-WATER-01 §1]. Simulation, script sprinkles, collision, LOS and RNG remain
unchanged. The prototype adds quiet drifting water, soft shoreline/building foam,
and land hovercraft particles that lightly brighten the ground. Original
blue/white script particles remain the water wakes; the added white movement
strokes were removed after visual review. Above-water model pieces and admitted projectiles also receive faint, rippled
reflections.

### 26.1 Public API and ownership

`frame.WindView { Heading uint16; Strength int32 }` and `Frame.Wind` copy the
existing session wind at publication [01 §7.3][I6]. Reset clears the value.
The renderer never reads the live wind service.

`drawlist.WaterSurface { Enabled bool; Tick uint32; Fraction16 int32;
WindHeading uint16; WindStrength int32; DriftX, DriftZ, Energy float32 }` is the
value field `Terrain.Water`.
The terrain recorder enables it only for Enhanced non-strategic world drawing,
using committed tick/wind and the presentation tick fraction. Paused captures
must retain their phase. Classic ignores the metadata.

`drawlist.SurfaceWake { X, Y, AxisX, AxisY, CrossX, CrossY, Age, Alpha float32;
Dust, Foam bool }` describes a rotated quad: centre and half-vectors in recording
pixels, age in [0,1], opacity in [0,1]. It is immutable until the next list
reset. `SurfaceWakes { Marks []SurfaceWake }`, `List.RecordSurfaceWakes` and
optional `SurfaceWakeSink.SurfaceWakes(SurfaceWakes)` carry batches after
terrain and before objects. Clone owns its marks; Reset releases their live
length. World transforms apply exactly once in the executor.

Client wake ownership: `water_wakes.go`, its tests, the new `Client.wakes`
state, hooks in `world_draw.go` and `trails.go`. The hooks observe every committed
tick through the session publication observer (including catch-up ticks),
reset with trails at battle/source/renderer changes,
and record the batch immediately after terrain. The producer uses authored
`CanHover`, footprint and committed movement. Hidden, carried, airborne,
unfinished and teleported units
must not bridge wake history. Every emitted mark starts at a player-visible
position; existing fog composites cover the batch. A bounded ring holds the
recent path; zero movement emits nothing. Hovercraft emit discrete skirt-side
particles on dry terrain, with each particle retaining its birth height and
direction. Grounded mode admits hovercraft without comparing model Y to the
centre terrain height: the four-corner conform can differ from that sample
[04 R-MOV-01 §5]. Hot or
damaging liquid receives no water foam in this first experiment. Geometry
and lifetimes are artistic presentation constants.

GPU ownership: `water.go`, its shader/fixtures, renderer lifecycle and the
terrain hook. Cache a conservative water/shore mask in painted map coordinates
using the terrain inverse projection [07 §8][03 §2.5]. Never treat a negative
height sentinel as water. Preserve painted colours. Draw the water treatment
before objects and fog; clip wake fragments to the matching wet/dry mask.
No postprocess may displace units, HUD or fog. Cache resources are released on
map replacement and disposal. Visible-region work and history are bounded;
benchmark classic and modern sequentially with matching metadata.

### 26.2 Validation gate

Build/vet/test with `tools/check`; targeted real-device renderer fixtures with
`NANOLATHE_GPU_DEVICE_TEST=1 go test ./internal/platform/gpurender -count=1`.
Inspect actual coastal captures at multiple ticks, native and fractional zoom;
check clone/replay, fog, shoreline clipping, pause and classic identity.
Use the existing battle benchmark for regression. Verify movement after loading
a real save and issuing normal orders; staged placement is a presentation
diagnostic and cannot establish that the gameplay producer works. The opt-in
`TestCoastalSavedGameParticles` accepts `NANOLATHE_COASTAL_SAVE` and retail assets
to exercise normal and two-tick catch-up cadence without writing the save.

### 26.3 Surface treatment and cost bounds

The GPU builds an RGBA mask once per terrain identity. Red identifies ordinary
water, green encodes inward shore distance, and blue independently identifies
valid dry ground; excluded liquid and invalid terrain belong to neither medium.
The mask starts at one painted map pixel per texel and doubles that step until
its largest side is at most 2,048 texels (at most 16 MiB of GPU pixels). Two
integer chamfer sweeps approximate distance up to 32 world pixels. A separable
nine-tap blur smooths the distance channel without changing wet/dry labels.
Its sample spacing is at least two world pixels to round height-grid corners
even on the finest mask level.
Bilinear sampling softens the mask while conservative coverage clips both foam
and dust. Source reset releases the mask. A future height-editing path must
invalidate this cache as well as the painted terrain sources.

A 128-pixel block index skips the water pass when no water intersects the view.
Otherwise the scheduler copies the terrain composite and applies a viewport
water shader before objects. Every constant in that shader is an authored
Nanolathe presentation choice, not a retail behavioural claim.

Three layers of smooth value noise perturb the terrain by up to 3.4 world pixels
per axis, and none of them scrolls on a velocity of its own: each travels only
on the integrated wind drift, the broad ripple at six times that drift, the fine
ripple at eleven and a coarse gust layer at twenty-two. The earlier treatment
added a fixed scroll on top of the drift, and in calm wind that scroll — about
2.6 world pixels per second — matched the drift itself, so the whole surface slid
as one sheet in a direction unrelated to the wind. With the fixed scroll gone the
direction of travel is always the wind, and the three multiples give the surface
parallax rather than a single sliding sheet.

Translation alone still reads as a moving tile, so the ripple is deformed as well
as moved. A slow domain warp — two more noise evaluations on a lattice about four
times coarser than the broad ripple, advancing roughly 0.08 of a cell per second
— offsets the broad lattice by up to 0.8 of its cells and the fine lattice by up
to 0.45 of its own. The warp lattice is the only term that advances on time
alone, and because it is a deformation rather than a translation, ripple cells
stretch, split and merge in place instead of marching past. The fine lattice is
additionally rotated 37 degrees about the map origin — one fixed rotation applied
once, not a wind-following one — so its cell rows never coincide with the broad
lattice's and the pair stops reading as a grid.

The coarse layer is a gust patch, a cat's paw: it glides downwind fastest, and
where it passes it both roughens the ripple, by up to a fifth more amplitude and
displacement, and darkens the water by up to five percent at full wind energy. In
a dead calm the darkening vanishes and only the mild roughening remains.

The field is translated by the wind and never oriented by it. Retail re-rolls the
wind heading to a fresh random value every 150 to 420 ticks
[05 "The wind phase, its draws, and the generator notification"], so crests
aligned to the heading would swing through a new angle every few seconds; and any
rotation about a fixed point sweeps distant pixels across the screen at a speed
proportional to their distance from it. Translation has neither defect.

Moving brightness and blue highlights make that motion readable against fine
painted texture. Bilinear terrain sampling prevents displacement from snapping
between original pixels. Shore fronts travel toward the coast along the blurred
distance field on an approximately four-second cycle, with spatially varying
phase and opacity. Broad crests fade across the last seven world pixels before
the wet/dry boundary to avoid outlining its grid. Surface brightness now varies
between −16.5 and +11.5 percent of the painted colour at full wind strength
inside a gust, and between −6.7 and +6.7 percent in a dead calm; the blue
highlight blend is still bounded to eight percent at full wind strength. These
subdued lighting coefficients preserve readable refraction; wave timing,
displacement and wind drift are independent of highlight strength. A wet pixel
costs six value-noise evaluations — gust, two warp, broad, fine and the shore
patch — against seven while the surface carried the sun glitter of §32.1, and
three before §32 added anything. No new pass, texture, uniform or allocation.

`water_motion.go` observes committed wind, using its negative sine/cosine
components [R-WIND-01]. Strength is normalized against 5,000 and clamped to [0,1].
The target drift speed is 0.4–2 world pixels per second; velocity and visual
strength approach the target by 1/90 of the remaining difference each tick.
The surface shader scales that integrated drift by six, eleven and twenty-two,
one multiple per layer, for visible movement.
Integrating the velocity preserves pattern position when wind changes, including
heading wrap and reversals. The field is never rotated about the map origin.
Recording interpolates the previous/current visual values with the permitted
presentation fraction. Repeated ticks do nothing; source/renderer changes reset
the state, as do tick rewinds and observation gaps exceeding 300 ticks.
These coefficients
describe this experiment, not retail arithmetic. The snapshot and water draw
add two render passes; cost depends primarily on viewport pixels, not map area.

Particle history is bounded to 8,192 marks and 4,096 tracked unit identities.
Land particles emit every six travelled world pixels, alternate around the
rear skirt, starting near its outer edge, and live for 45 ticks. Their initial
opacity is 0.45. They spread and drift sideways with quadratic
opacity decay. The soft lobed profile composites white at low opacity, so it
can brighten terrain without darkening it or requiring another snapshot.
The additional
work scales with visible marks and their overdraw. Renderer changes clear
history; no simulation service or gameplay RNG is consulted. `water_buildings.go`
records broken elliptical ripples beneath visible, completed floating
buildings, bounded to 1,024 visible rings. Admission uses wet terrain, a model
top reaching the surface, and committed base height equal to sea minus authored
waterline [05 "Geothermal requirement"]. It does not require the FBI `Floater`
flag: stock water-yard buildings such as tidal generators do not set it.
Two staggered rings expand and dissolve inside each quad, avoiding a squared
footprint outline. This approximates displacement around the base, not the
model's exact waterline intersection; the shared mask clips it to water.

Validation includes real-device native and fractional zoom fixtures for phase
replay, animation, dry/wet clipping, opaque object preservation and source
reset, plus publication and history lifecycle tests. Actual saved-game testing
found that observing only during recording lost history whenever two simulation
ticks preceded a draw. Per-publication observation fixes that while retaining
the reset on genuinely missing history; loading itself preserves the needed
unit metadata. A staged Coast to Coast
diagnostic uses actual ARMPT, CORPT, ARMSH and ARMTIDE models with prescribed trajectories
and a deliberate wind reversal; it demonstrates the effects but does not measure naval gameplay.
The matching land-battle benchmark showed no measurable modern regression and
an identical classic capture. It does not establish the cost of a large naval
battle's active water/particle cost.

Two later corrections keep the treatment steady in time. The hover-dust age and
the building-foam ring phase now add the presentation tick fraction, as the
scorch and blast ages do, so both advance at display rate instead of stepping at
30 Hz on a faster display; the dust lifetime, the foam cycle length and the
recording at fraction zero are unchanged, and the foam tick is still wrapped
before the fraction is added. The surface shader's value noise also reduces its
integer lattice coordinate onto a 289-cell period before hashing it. Elapsed
time and the integrated drift scroll that lattice without bound, and an
unbounded sine argument eventually leaves float precision, degrading the pattern
on some devices; a modulo on time itself would make the pattern jump, whereas
wrapping every lattice corner on one period leaves the field continuous. The
noise becomes tile-periodic, and at the scales used here that tile is thousands
of world pixels across, wider than any viewport. Both are implementation choices
for this prototype, not retail evidence.


### 26.4 Above-water screen-space reflections

This is an Enhanced presentation approximation. `ModelGeometry.ReflectWater`
admits a visible body whose origin is over valid ordinary water;
`ReflectionSea` is absolute sea height in recording-scale pixels, alongside
`WorldHeight`.
Vertex `Height` remains physical relative height, including in
supersampled geometry. `Sprite.ReflectWater/ReflectionHeight` and
`Line.ReflectWater/ReflectionHeight0/ReflectionHeight1` carry signed above-sea
height for admitted projectile bodies and endpoints. Ground-shadow sprites do
not opt in. Classic ignores these value fields; cloned lists retain them.

The direct model lane captures front-facing faces while preparing its normal
atlas. Each reflected vertex samples that existing resolved colour at its
original atlas position. The original key plane and quad mapper reject source
pixels belonging to an obscuring piece. Physical height, independent of the
retail comparison key, clips fragments at and below sea. Reflect only physical
height, preserving the hull's ground footprint: the camera subtracts half height
[03 §2.5], so reflected screen Y is source screen Y plus its above-water height.
Equal-height points retain their screen-space direction at every heading. Low
hulls can obscure much of their own reflection; do not move or flip the whole
image to force it into view. No extra model atlas or alternative scene camera
is built.
The atlas already applies materials, construction reveal and waterline tint.
Models omitted from that atlas do not receive fallback reflections.

Projectile model pieces share this path. GAF projectiles are reflected
billboards about their anchor's water-plane projection; their art has no
per-pixel physical depth. Beam/segment endpoints use committed heights and
interpolate the waterline clip across each stroke. No effect, UI glyph or
projectile ground shadow is inferred to be reflective from its brightness.
§32 lifts that for named explosion and impact art alone, which the recorder now
admits explicitly over water; UI glyphs and ground shadows still never reflect,
and nothing is inferred from brightness.

A retained viewport RGBA plane receives the reflection source, capped at 32,768
vertices per frame, reserving 4,096 for projectile sprites and strokes. A water-only resolve introduces two irregular horizontal
ripple frequencies, a three-sample softening, a cool tint and 25 percent opacity.
Sources fade between 64 and 160 world pixels above sea. Wave phase uses the same
committed time/fraction as the water and freezes on pause. World zoom applies
once, to destination coordinates; source atlas positions remain unchanged.
The resolve is after painted water and before objects, wakes and fog. Thus
reflections cannot paint over foreground units, shore or UI, and black fog
covers the result. Only bodies already admitted by presentation can reflect.
This does not reconstruct offscreen or hidden surfaces, trace rays, or solve
inter-unit reflected depth ordering; overlapping reflected subjects remain an
approximation. The source plane is allocated only when needed and released on
source reset; draw work is skipped without visible water or admitted geometry.
`SetWaterReflections` is a capture-only comparison switch. Shore foam's opacity
is additionally reduced by one quarter; its shape and timing are unchanged.

A later correction gives the resolve the same shoreline treatment the surface
and wake shaders already use: it samples the shared mask bilinearly in their
coordinate convention, converts it to coverage with the same smoothstep, and
multiplies its premultiplied result by that coverage, keeping the early-out
where coverage is zero. Rejecting a nearest sample below full coverage had made
the reflection end on a whole mask texel, which is several world pixels wide on
a large map, so the edge read as a stepped line rather than a shore. Both
compiled resolve variants share the one shader source, so both fade. This is an
implementation choice, not retail evidence.


At 1920×1080, the reflection source has 7.9 MiB of logical RGBA pixels. The
current Ebitengine Metal backend rounds each texture dimension up to a power of
two, allocating a 2048×2048 texture (16 MiB), plus the bounded vertex/index
buffers. This corrects a logical-pixel-only estimate of device storage.

An isolated M3 Pro probe at 1080p alternated reflections on/off on the same
committed frame, warmed both paths, and forced completion with an identical
full-frame readback after every Execute. Two runs of 120 pairs measured median
paired overhead of 0.2–0.6 ms for three water bodies, a dry hovercraft and a
projectile stroke, and about 0.8 ms for a dense 64-body water scene plus a stroke.
CPU submission overhead was about 0.03 ms and 0.32 ms respectively; total device
draws increased by two in both scenes. These are renderer/completion deltas,
not isolated GPU timestamps or full-game FPS estimates. Scene overlap, visible
water area, device and resolution affect cost. Raising opacity from 20 to 25
percent changes a shader coefficient without adding geometry, passes or storage.

### 26.5 Integration verification

The user approved the final water, foam, hover dust and physical-height
reflections, including 25 percent reflection opacity, for main. Integration
retains metallic glints, nanolathe illumination and explosion distortion;
vegetation remains static. Independent read-only review found no implementation
issues. The fast, retail and real-device gates passed, as did the saved-game
hover movement check at ordinary and two-tick catch-up cadence. Coastal captures
at native and 2× recording scale, before and after a staged wind change, were
inspected; the device fixtures also exercise fractional zoom and replay.

Sequential Great Divide baseline/integrated runs used scene version 4, seed 7,
1920×1080, 60 draws/s, 300 pre-ticks, 120 warmup draws and 180 measured draws,
with factories enabled and auto-remaster disabled. Modern used 2× zoom; classic
used native. Within each renderer pair all scene metadata and frame censuses
match, and final RGB captures are identical. Both captures contain fire,
moving units, projectiles and active factory construction and were inspected.

| Executor | DrawWork median / p95 / maximum, ms (baseline → integrated) |
|---|---|
| Modern 2× | 5.500 / 8.501 / 9.985 → 5.562 / 8.439 / 10.187 |
| Classic native | 13.989 / 17.874 / 26.168 → 13.766 / 17.788 / 20.340 |

These short host samples show no clear regression. The dry battle is a
regression control, not a measurement of active water or reflection GPU cost;
§26.4 records the separate active-water completion probe. Artifacts remain
outside the repository in `/private/tmp/coastal-land-{base,after}-{modern,classic}`
and `/private/tmp/coastal-land-multiframe`; gate logs use the same
`/private/tmp/coastal-land-` prefix.


### 26.6 Aircraft and boat water reflections

The user approved landing this Enhanced treatment after blue-water skirmish
review. It combines height-dependent aircraft fading and softening with gentle
boat reflection wobble.
It is an implementation choice, not a retail behavioral claim. Model sources
now fade smoothly between 64 and 320 world pixels above sea, instead of ending
at 160. This lets aircraft at flight altitude leave a faint reflected image.
The distance applies to all model sources, including tall model pieces and model
projectiles; sprite and beam sources keep their 64–160 fade. The 25 percent
resolve opacity, ripple, tint, water mask, fog order and physical-height
projection of §26.4 are retained. Values are artistic choices for this prototype.

The second visual iteration weakens and breaks up high model reflections. A
smooth 64–160-world-pixel height ramp multiplies model opacity by 1 down to 0.7;
world-anchored horizontal transmission bands multiply it by another 1 down to
0.7 at full ramp strength. Averaged over the bands, a fighter at height 110 is
about 20 percent fainter than the first prototype and a bomber at 200 about
40 percent fainter. These are relative artistic opacity changes, not measured
luminance or retail behavior.

The same ramp introduces a horizontal displacement of the already recorded
reflection vertices: two world-anchored sine waves, amplitudes 3 and 1.05 world
pixels. Their original atlas coordinates are retained, so this does not sample
neighboring model slots or require another camera. Shared corners follow the
same continuous displacement. The distortion uses committed water time/fraction,
freezes on pause, and scales once with zoom. The third iteration extends this
wobble to boats: the displacement ramp has a minimum strength of 0.35, giving
low model corners wave amplitudes of 1.05 and 0.3675 world pixels. The stronger
height-dependent displacement still applies above that floor. The floor is
applied after clamping the normalized height to 0–1, so submerged corners do not
produce an unbounded displacement. This is an authored water treatment, not a
retail behavioral claim.

Boat opacity stays unchanged: the distance fade and transmission-band ramp
still depend only on physical height, so boat-height corners do not inherit
aircraft dimming. Existing water-only resolve ripples continue to soften the
whole reflection. Sprites and beams keep their previous treatment. Tall
non-aircraft models follow the same height rule; no classification is added.

Through the third iteration, geometry admission, vertex budget, source texture
allocation, render passes and texture sample count are unchanged. That increment
is vertex arithmetic and model-fragment arithmetic in the existing source pass. No underside,
offscreen body or reflected depth is reconstructed. Top-surface reuse remains
an intentional approximation for human review.

The fourth iteration makes the same 64–160 height ramp reduce model opacity
from 1 to 0.35 instead of 0.7. Relative to iteration three, a height-110 model
is about 20 percent fainter and heights at or above 160 are 50 percent fainter,
before filtering. The 64–320 terminal fade and transmission bands remain.

Blur strength uses a separate smooth height ramp from 64 to 200. Each source
texel blends its old horizontal filter (centre 0.5, sides 0.25) with a filled
cross kernel: centre 0.25; horizontal pairs at distances one, two, three and
four weigh 0.12, 0.09, 0.06 and 0.03 each; vertical pairs at distances one and
two weigh 0.06 and 0.015 each. Both kernels sum to one. The wide kernel uses
nearest texels on a fixed one-screen-pixel grid: four screen pixels horizontally
and two vertically. This keeps even one-pixel details connected at every zoom;
the existing narrow contribution remains bilinear and scales with zoom. The
wide contribution can advance by whole pixels with water motion, an intentional
sampling approximation to keep this faint screen-space treatment inexpensive.

The source shader also writes a viewport-sized RGBA8 height buffer using the
same admitted reflection geometry, atlas samples and hidden-piece checks.
Red stores blur strength times source alpha; alpha stores source coverage after
fading. Normal premultiplied composition preserves a weighted strength when
reflections overlap. The resolve recovers strength as red/alpha and weights
each source texel before interpolation or summation. Adjacent boats cannot inherit
an aircraft's blur strength; exact overlap has the combined source strength.
Final water masking and foreground/fog composition retain their existing order.

The executor marks a coarse grid of 64×64 recording pixels from elevated triangle bounds,
expanded by the full filter reach, water displacement and sampling margin. Only
marked cells run the heavier filter; unmarked cells retain the old three samples.
Screen bounds and margins are converted back to recording coordinates before
marking, so fractional zoom does not transform the grid twice. Adjacent same-kind
cells share horizontal quads. Cheap and soft cells use separate compile-time
shader variants in two non-overlapping groups; ordinary water avoids the larger
shader's register cost as well as its samples.
Triangles entirely below 64 or above 320, and bounds outside the viewport, mark
nothing. Height-range intersection is conservative: a face spanning below 64
to above 320 still has eligible interior fragments. A frame without marked cells
skips the metadata render and retains the old single resolve quad.
The buffer allocates lazily, is reused, is resized when next needed and is
retired on source reset. Its logical storage is 4.69 MiB at 1280×960 or 7.91 MiB
at 1920×1080, excluding backend padding. Active frames add one geometry
submission per existing reflection run. The resolve has a fixed maximum of
three bilinear positions for the narrow contribution and 13 nearest positions
for the wide contribution (25 colour texel reads) within marked cells, with one
metadata read for each nonempty colour texel, versus 12 colour reads before. The larger sample count
requires measurement: this is a bounded prototype, not a claim of free blur.
There is no new scene camera, model rasterization or simulation work.

First-iteration validation passed `tools/check` and the Metal device fixtures, including
an above-water model at height 180 at native and fractional zoom. Before/after
Great Divide battle runs (seed 7, 1920×1080, 180 measured draws at 30 FPS) have
identical captures and per-tick censuses in each renderer. This dry battle is a
regression control, not an active-reflection cost measurement.

A frozen staged Seven Islands scene with a fighter at height 160, a bomber at
200 and a dry hovercraft was replayed at 1280×960, 2× zoom, on an M3 Pro/Metal.
Two baseline and two prototype runs each used 60 warmup and 180 measured draws,
VSync on, with no readback in the timed window. Baseline median CPU submission
was 0.31–0.42 ms; prototype was 0.35 ms. Median draw cadence remained 8.31–8.32 ms.
All four reported 252 reflection vertices, seven passes and eleven device draws.
This small scene showed no measurable median regression; it does not establish
isolated GPU cost or dense-fleet performance. An initial VSync-off scratch
harness failed in baseline Metal presentation and supplied no timing result.

Second-iteration checks passed the fast gate and Metal device fixtures, including
frozen replay of the high reflection at native and fractional zoom. Actual
saved-skirmish captures and a 2× staged capture were visually inspected. The
boat control is byte-identical at native and 2× zoom. Matching classic and modern
battle captures and per-tick censuses remain byte/value-identical to the first
prototype. Two paired frozen-aircraft runs with the settings above measured
median CPU submission 0.240–0.243 ms before and 0.268–0.276 ms after, roughly
0.03 ms more in this small scene. Median cadence stayed 8.31–8.33 ms and counts
remained 252 reflection vertices, seven passes and eleven device draws. This is
CPU submission and paced cadence, not an isolated GPU-time measurement. Exact
second-iteration artifacts are in the `revision2` subdirectory.

Third-iteration checks passed the fast gate and Metal device fixtures. Native
and 2× blue-water captures were visually inspected; a Ring Atoll save contains
eight aircraft and four boats, verified moving before and after production save
reload. Classic and modern battle metadata, per-tick censuses and captures
match the second iteration. Two paired frozen boat runs (same display settings
and measurement window above) measured median CPU submission 0.207–0.298 ms
before and 0.224–0.321 ms after. Paired differences were 0.017 and 0.023 ms;
run-to-run variation is larger, so this is only a small-scene indication.
Median cadence stayed 8.31–8.33 ms. All runs retained 548 reflection vertices,
seven passes and ten device draws. Third-iteration artifacts are in `revision3`.

Fourth-iteration validation passed the fast gate and Metal fixtures. Authored
one- and four-pixel stripes at 0.75×, 1× and 2× retain connected footprints,
increase their horizontal second moment with height, do not increase peak
opacity, and stay within the energy tolerance. A nearby high reflection does
not alter a low reflection's colour contribution. Height fading, frozen replay,
water/foreground clipping, stale metadata, resource retirement, ramp-crossing
bounds and off-origin fractional culling are covered. Saved Ring Atoll captures
at native and 2× zoom were visually inspected; independent code review passed.

Final classic and modern Great Divide live-battle checks match iteration three
in scene metadata, every per-tick census and exact PNG bytes. They retain active
movement/combat, burning sprite features and factory nanoframes. Median
record/submit/cadence in milliseconds is 13.843/0.454/33.333 for classic and
3.133/4.997/33.334 for modern. These dry controls do not measure water cost.
The paced active-water pair records median CPU submission 0.209 ms before and
0.278 ms after, with median cadence 8.337 and 8.325 ms respectively.

The final frozen active-water comparison uses the same M3 Pro/Metal, 1280×960,
2× zoom, 60 warmup and 180 measured frames as above. Four alternating runs with
a synchronous one-pixel readback after Execute measured median completion
latency 5.757 and 5.981 ms before, 6.294 and 6.645 ms after. Paired increments
are 0.537 and 0.664 ms; median cadence remains 8.33 ms. Completion includes
CPU submission, driver synchronization and readback, not isolated GPU timestamps.
The scene retains 252 admitted reflection vertices; pass count rises from seven
to eight and device draws from eleven to thirteen. This small scene does not
establish dense-fleet cost. The first fully bilinear whole-viewport attempt
added approximately 1.4–1.6 ms; the final filter reduces that cost with fewer
samples and bounded shader regions. Final artifacts use the `final-` prefix in
`revision4`; earlier intermediate results remain there for reproducibility.

Local captures, exact diagnostic source, logs and playtest artifacts are under
`/private/tmp/nanolathe-air-reflection-review`. The four prototype iterations
above record the visual tuning and cost measurements behind the approved result.

## 27. Burning vegetation heat shimmer prototype

This is a user-requested modern GPU presentation experiment, not a retail
behavior claim. The user visually approved it and authorized landing with the
shared distortion pass and tree-heat overlap priority described in §27.2.
The recorder tags the resolved body art of burning sprite features using the
committed IsBurning flag, with an additional anchor LOS check. No new fire,
simulation state, RNG calls or asset edits are introduced. Classic ignores the
metadata. Missing art, hidden anchors and finished burning emit no shimmer.

Time comes from the committed tick modulo 3600, plus the existing presentation
fraction when enabled, with a stable cell-derived phase offset. The shader's
frequencies wrap at that tick period. Replaying a list and pausing freeze it.

The plume starts 45% down the resolved body's art and extends upward. Its
half-width is 55% of the art width clamped to 14–38 world pixels; height is 125%
of the art height clamped to 56–112 pixels. Two upward-travelling waves, a small
sideways drift and a squared soft envelope create up to about 2.4 world pixels
of horizontal refraction. These numbers are artistic tuning, not retail facts.
Recording scale and final smooth zoom apply to all lengths together.

The executor culls against the world viewport before admitting at most 128
plumes in source order. It shares one world copy and one distortion batch with
explosion rings, appending the heat quads last before fog/chrome (§27.2). Bilinear sampling is clamped to the source clip. Outside the soft
plume envelope pixels are untouched. Overlaps read the same snapshot; the last
plume wins rather than recursively amplifying displacement. Tree heat also
wins wherever it overlaps an explosion ring. With neither visible plumes nor
explosion rings, the shared pass performs no copy or draw. HeatPlumes counts the submitted plumes.
SetTreeHeat is an executor-only comparison control.

Verification uses shader compilation and the existing real-device fixture loop
for motion, frozen replay, clipping and source removal, followed by sequential
live battle captures and timing checks. The prototype does not claim physical
refraction or exact occlusion against foreground units within the plume.


### 27.1 Prototype verification

The affected client/draw-list/GPU/doc packages, tools/check and the real-device
fixture loop passed. The source test covers modern/classic, visible/hidden,
burning/reclaiming and missing art. The device check covers frozen replay,
changed time, clip boundaries and source removal. These initial prototype
checks preceded the full pre-landing retail gate.

Sequential Great Divide scene-v4 runs used seed 7, native 1920×1080, 300
pre-ticks, 30 draws/s and 180 measured frames, with factory production enabled.
The measured modern frames submitted 7–11 visible plumes. The endpoint contained
15 burning features, 13 with in-view anchors, 160 moving units, 61 projectiles,
300 effects, 8 nanoframes and 4 nanolathe events. Before/after censuses matched.
The classic endpoint PNGs were byte-identical. Modern captures were inspected.

| Renderer | Revision | Host draw work median / p95 / max (ms) | Host submission median / p95 / max (ms) |
|---|---|---|---|
| Modern | Before | 9.083 / 10.239 / 15.855 | 4.238 / 4.803 / 7.771 |
| Modern | Prototype | 9.585 / 10.575 / 11.847 | 4.670 / 5.116 / 5.444 |
| Classic | Before | 15.650 / 19.216 / 28.068 | 0.469 / 0.543 / 0.704 |
| Classic | Prototype | 15.522 / 19.374 / 22.092 | 0.460 / 0.512 / 0.663 |

Median host cadence remained approximately 33.33 ms. These are single short
host samples, not GPU timings or a performance guarantee. Comparison outputs
are in /private/tmp/tree-heat-{before,after}-{modern,classic}. A separate live
capture used temporary readback instrumentation; its timings are excluded and
the instrumentation is absent from the prototype. The resulting motion preview
is /private/tmp/nanolathe-tree-heat-preview.mp4.


### 27.2 Shared explosion and tree-heat pass

The user explicitly chose tree heat to overwrite explosion distortion wherever
both occur, accepting the small overlap change. The two families now append to
one retained vertex/index batch and use one shader, one immutable world copy
and one draw. All explosion quads precede all heat quads regardless of source
recording order. A heat fragment outside its plume discards, preserving the
blast beneath; a covered heat fragment replaces it with a heat-only sample of
the original world. Heat does not refract an already distorted explosion image.

The source-coordinate Y offset selects the shader formula: negative for a
blast, one plus scaled heat amplitude for heat (§28). X carries blast strength
or heat time. Each quad has one selector throughout, and both formulas retain
their previous arithmetic, clipping, bilinear sampler and independent budgets.
The existing per-family comparison controls remain independent. A GPU fixture
checks overlap priority, unaffected blast pixels outside the plume, frozen
replay, and exactly two extra submissions (copy plus draw) with either or both
families, compared with neither. No extra render target is allocated.

Shared-pass verification: tools/check and the real-GPU fixture loop passed.
The same scene/settings as §27.1 were run twice for modern in the order
separate, shared, shared, separate, followed by shared/classic and
separate/classic. All frame censuses matched. Of 180 modern frames, 167 had
both sources active: each saved exactly two device submissions; the other 13
saved none. Classic captures remained byte-identical. The shared modern capture
was inspected; the intentional overlap change preserves heat priority.

| Modern pass layout / run | Host draw work median / p95 / max (ms) | Host submission median / p95 / max (ms) |
|---|---|---|
| Separate / 1 | 10.059 / 10.689 / 23.863 | 4.693 / 5.077 / 9.369 |
| Shared / 1 | 10.089 / 10.717 / 12.615 | 4.725 / 5.108 / 5.409 |
| Shared / 2 | 9.993 / 10.619 / 12.268 | 4.716 / 5.079 / 5.337 |
| Separate / 2 | 10.026 / 10.743 / 12.716 | 4.769 / 5.106 / 5.368 |

The repeated host medians are effectively unchanged; these measurements do not
establish a CPU speedup. Median cadence stayed near 33.33 ms. The eliminated
copy/draw is confirmed by device-submission counts; GPU duration and bandwidth
were not measured. Classic host draw work was 15.384 / 18.404 / 20.861 ms
separate and 15.579 / 18.609 / 20.853 ms shared (median / p95 / max).
Artifacts are /private/tmp/tree-heat-shared-{before,after}-{modern,classic}-{1,2}
(the classic pair has run 1 only). The original launchers now use the shared-pass
build. These measurements preceded the full pre-landing retail checks.


### 27.3 Landing review

The independent landing review found that retained heat-source sprites could
keep decoded art alive across a map reset. Preparation now copies only scalar
geometry, clip, time and scale into its scratch buffer; no GAF-frame reference
escapes the borrowed list. Source reset clears that buffer and preserves the
comparison toggle, covered by the existing lifecycle test. This changes resource
ownership only; plume geometry and the shared shader remain the approved design.


Landing integrates the approved coastal water/reflection implementation while
retaining both renderer states, shader families, counters and reset paths. Heat
uses §27 so the coastal §26 anchors remain unchanged. The frozen battle capture
route was retried on the heat candidate and unchanged pre-coastal main; both
failed while acquiring a Metal drawable texture, reproducing the existing §23.7
host limitation. No frozen-list timing or paired-capture success is claimed for
that route. Logs are /private/tmp/tree-heat-land-frozen-{candidate,main}.log;
real-device fixtures and live battle comparisons are the available visual and
performance evidence.

## 28. Fresh wreck cooling prototype

User-requested Enhanced presentation experiment, extending §27's shared heat
pass. This is artistic tuning, not a claim about retail temperature or damage.
A successful violent unit-death corpse placement records the returned feature
instance's birth tick in session publication state. Only that exact live
instance publishes a known birth; map features, restored features, nonviolent
feature conversions and newly discovered features do not acquire heat. The
metadata is presentation-only and is neither simulation input nor saved state.

The modern 3DO feature recorder samples committed age and the existing optional
tick fraction. Replaying a list cannot advance age. Anchor LOS is required;
underwater origins suppress both effects. Removal/reclamation removes the
source with the feature. A birth tick of zero is valid; an unknown birth is
explicit. Packet operands are cleared before each visibility/age decision.

The material begins pale orange for six ticks, loses its pale component, then
cools through orange/red to its original texture by 180 ticks. A weaker shimmer
fades quadratically over 300 ticks. These durations and colours are prototype
choices. The emission is computed once per wreck on the CPU and carried in
unused vertex RGB lanes of its existing body composite. A screen blend preserves
texture variation and premultiplied model coverage. There is no extra material
draw, render target, texture lookup, bloom blur, or dynamic light. The atlas
capacity fallback currently omits emission; normal atlas bodies carry it.

Wreck plumes use the recorded model bounds, capped to 40 scaled pixels in half
width and 64 in height, with a 32-visible-plume budget applied after clipping.
They append after explosion rings and before tree plumes to the same distortion
mesh; trees still win overlapping pixels. The tree budget remains 128. All
three families share the existing world copy and single distortion draw; a
frame containing only wreck shimmer still needs that copy/draw pair. Distortion
amplitude is encoded with a positive bias so a nearly cold source cannot round
into the explosion selector. No GPU readback or extra full-screen pass is added.

### 28.1 Prototype verification

Fast and retail gates passed, as did the real-device fixture loop and an
independent read-only review with fresh affected-package tests. Device checks
cover glow with unchanged submission/pass counts, transparent model holes,
removal/cooling, vanishing amplitude, the 32-plume cap, and the existing shared
blast/tree ordering. Publication checks cover tick zero, successful versus
rejected death placement, conversion, replacement/reclaim/restore, expiry and
reset, unchanged saved feature bytes, and unchanged RNG consumption.

Sequential live-battle comparisons against the integrated tree-heat build
`9e61946` used Great Divide, seed 7, 1920×1080, native zoom, 300 preticks and
180 measured draws at 30 Hz. Every matching census agreed. The classic final
capture was byte-identical. Modern showed 6–12 visible wreck plumes, and every
measured frame had exactly the baseline draw count; added plume geometry was
four vertices and six indices per wreck. Two stable repeat pairs measured
median CPU DrawWork 9.768→9.838 ms and 9.702→9.664 ms; CPU Submit was
4.587→4.611 ms and 4.764→4.503 ms. Median cadence stayed 33.33 ms. An earlier
pair was inconsistent across all CPU phases (DrawWork 7.038→9.865 ms,
simulation Step 1.304→2.111 ms), prompting those reverse-order and forward-order
repeats. These are host timings, not GPU timestamps; the repeats show no
resolved CPU frame-cost increase and cannot establish GPU execution time.

Artifacts are outside the repository under `/private/tmp/wreck-heat-*`.
Live footage covers 180 draws with 1–6 wreck plumes after 120 preticks; its
PNG readbacks were diagnostic instrumentation, removed after building a
separate capture binary and excluded from all timing comparisons. The material
contact sheet uses the real `armstump_dead` model with supplied ages
0/6/30/90/180/300 ticks, drawn on the actual GPU. The existing frozen `--shot`
Metal tooling failure is recorded in §27.3; the real-device fixture and live
capture paths remain the visual evidence for this prototype.

## 29. Metal/paint finishes and fading scorch marks

The user selected material polish and cooling/scorch from the four experiments.
Unit emission/halo and brief dust/spark/water impact accents are rejected and
are not part of this implementation. Existing glow, blast art, coastal wakes
and the independent fresh-wreck treatment of §28 keep their own contracts.
All new coefficients and texture annotations are authored presentation choices,
not retail material/temperature evidence.

### 29.1 Materials

A curated texture-name table annotates textured unit faces as default, metal or
paint. It does not classify feature or wreck faces. Metal receives a broad cool
response beneath the existing glint; paint receives a weaker rough highlight.
The coefficients preserve authored dark seams and panel hue. Untagged faces
retain their existing shading. The team-colour panel textures `colorslt`,
`colorsmd`, `colorsdk` and `colordk2` use the metal finish for every player
frame, preserving the selected team hue. This is an authored presentation
choice; team logos retain their existing classification.

The table is authored data, not Go source. `internal/client/materials/
materials.tdf` is a TDF file with one `[materials]` section whose keys are 3DO
texture names and whose values are `metal` or `paint`; it is embedded in the
binary and parsed once into a lowercase map, and an unrecognised value annotates
nothing. A mounted install may replace it wholesale by supplying the logical
path `nanolathe/materials.tdf` — a remaster pack or a loose gamedata directory —
which the command layer installs after mounting. A missing override is the
ordinary case; one that cannot be read is reported in the standard diagnostic
shape and leaves the embedded table in force, because presentation art never
fails a load. Both the embedded file and any override are authored presentation
choices, not retail material evidence: the retail executable carries no material
classification for model textures, and nothing here reaches authoritative
state.

`ModelFace.Material` carries the annotation. The color vertex lane retains its
low 16 bits for palette index and glint, followed by two material bits and three
quantized normal-response bits: at most 21 bits, exactly representable as a
float32 integer. Shadow geometry carries no finish. The existing color shader
applies the finish after palette lighting, sharing its key, reveal and waterline
verdicts; reveal replacements clear the finish. There is no additional model
pass, emission atlas or render target. The independent wreck composite of §28
is unchanged.

### 29.2 Cooling and fading scorch

The client observes every committed publication, including ticks between draws.
A nonzero effect ID paired with its event sequence deduplicates primary impact
art at birth; unresolved, hidden and pre-existing effects cannot later create a
mark. Both the event and its ground anchor must be visible. Valid dry terrain
within 12 world-height pixels of the impact admits a mark, sized to 0.55 times
the resolved art extent and clamped to 8–64 world pixels. These thresholds and
the procedural appearance are artistic presentation choices.

A FIFO retains at most 256 world-space marks. A separate 512-identity budget
bounds work within one publication. Marks reset on source/load, viewer change
or tick rewind; they are not saved. Camera projection produces screen-space
`ScorchMark` records with radius, committed age plus the optional draw fraction,
and a stable variant. Drawlist cloning owns a copy of the batch.

The warm center cools through 90 ticks. Whole-mark opacity then smoothly fades
from tick 90 to 450: fading starts at three simulated seconds and the mark is
gone at fifteen. Shared drawlist constants define these ages and the cap.
At expiry the CPU removes the mark and the GPU rejects it without submission.
The GPU also caps externally supplied batches to 256 marks per frame.

A smooth uneven procedural quad draws immediately above terrain, below objects
and fog. It reuses the coastal mask's dry channel, including when water animation
is disabled, and has no persistent GPU history or additional render target.
No dust, spark or water-splash accent is included.

Both effects default enabled. Diagnostic `SetMaterials`/`SetScorch` controls and
`NANOLATHE_MODEL_MATERIALS=0` / `NANOLATHE_SCORCH=0` disable them independently.
`ModelStats.MaterialFaces` and `ScorchQuads` report the submitted workload.

### 29.3 Verification

The fast and retail gates and real Metal device fixture loop passed. Independent
review covered both implementations and their integration. Device readbacks
verify material key/reveal/waterline behavior, unchanged model submissions and
image allocation, and compatibility with the independent wreck composite.
Scorch checks cover monotonic fading, exact disabled-output equality at expiry,
zero expired submissions, object/HUD and wet masking, cloned replay, view scale,
reset and bounded public batches.

Stock-model captures confirm restrained metal/paint response. Staged impacts on
Comet Catcher were observed through 511 ticks: early warm centers become dark
marks, fade, and leave images byte-identical to the disabled output after every
mark expires. Artifacts remain outside the repository under
`/private/tmp/selected-material-capture`, `/private/tmp/selected-scorch-fade` and
`/private/tmp/selected-scorch-fixtures`.

Sequential live battles compared against main `774f79c` on Metal, using Great
Divide, seed 7, 1920×1080, 2× detail, 300 preticks, 60 warmup draws and 180
measured draws at 30 Hz. Two modern pairs ran in opposite order. They submitted
2,439–3,387 annotated faces and 39–143 scorch quads per draw, with 3–6 existing
wreck plumes and no model overflow. Every matching census agreed, and the
classic final captures were byte-identical. GPU image storage was unchanged
at 526,532,608 bytes for modern and 66,732,032 bytes for classic.

Host DrawWork timing in milliseconds (median / p95 / maximum):

| Run | Baseline | Selected |
|---|---|---|
| Modern pair 1 | 6.901 / 8.312 / 19.126 | 7.092 / 7.849 / 9.827 |
| Modern pair 2 | 9.656 / 10.314 / 11.162 | 9.793 / 10.634 / 11.543 |
| Classic | 22.559 / 33.811 / 42.436 | 22.159 / 31.286 / 40.617 |

Modern median Submit changed 3.549→3.657 ms and 4.799→4.825 ms. Median cadence
remained 33.33 ms. The combined effects added about 0.14–0.19 ms to median host
DrawWork in these pairs. Absolute host speed varied between pairs; these CPU
measurements do not establish GPU execution cost. The small observed host cost,
unchanged image storage and inspected captures support keeping both effects.
Profiles, full percentile/maxima data, census and captures are under
`/private/tmp/selected-materials-scorch-battles`.

## 30. Player controls for Enhanced effects

The Enhanced effects reached this point as prototypes with executor comparison
switches, environment variables, or nothing a player could reach. Five persisted
switches now cover them, beside the glow switch of §19.4. They are Nanolathe
presentation preferences, not retail evidence: the classic executor composes
identical pixels whatever they say, and nothing here is visible to the
simulation or to any committed frame [I6].

`settings.Presentation` stores them as `water`, `lighting`, `finish`,
`distortion` and `marks`, each defaulting to 1. They are integers for the same
reason the display bits are: a stored 0 is "off" and is kept, only a negative
value is repaired, and a file that omits a key keeps the default because the
loader decodes over the defaults [02 "Settings"]. `internal/drawlist.Effects` is
the value type both sides read; `cmd/nanolathe` converts the stored integers to
it, so `internal/settings` remains a leaf.

| Switch | Recorder gate | Executor gate |
|---|---|---|
| Water | the water phase (§26.1), the wake, foam and water-motion producers, and reflection site admission (§26.4) | `SetWaterEffects`, `SetWaterReflections` |
| Lighting | none — lighting kinds are always recorded | `SetBattleLighting` (§23) |
| Finish | none — face material and normals are always recorded | `SetMetalGlint` (§23.7), `SetMaterials` (§29.1) |
| Distortion | blast ring metadata (§25), the burning-feature heat tag (§27), and the fresh-wreck emission and shimmer (§28) | `SetBlastDistortion`, `SetTreeHeat` |
| Marks | the scorch observer and draw (§29.2) and the trail layer (§15) | `SetScorch` |

`Client.SetEffects` retires the trail, wake, water-motion and scorch histories
whenever the selection changes, the way an executor swap does, so a switch that
was off leaves no stale marks and a switch turned back on starts from the
current tick. It also advances the paused-world revision, because a changed
selection is a different world raster (§13.10).

`Renderer.SetEffects` early-returns on a selection equal to the last one it
applied. That is load-bearing: the host calls it once per presented frame beside
`SetGlow`, and without the early return it would overwrite the local comparison
controls every frame — the Ctrl+Shift+G glint shortcut would revert on the next
Draw instead of holding until the player changes a setting. The three
environment overrides (`NANOLATHE_MODEL_MATERIALS`, `NANOLATHE_METAL_GLINT`,
`NANOLATHE_SCORCH`) are read once at construction and AND-ed with the player's
switch on every application, so a developer's `…=0` keeps its family off
whatever the options page selects. A source reset retires images only; the
applied selection and the per-family switches survive it.

The window polls the shell's committed preference each update
(`RunOptions.Effects`) and hands the executor the client's selection beside the
display palette on every present, on the running, paused and benchmark paths;
the benchmark keeps every effect on so two runs measure the same work. A
`--shot` capture reads the same settings file, so it composes under the player's
switches. The options page rows and the `+water`, `+lights`, `+finish`, `+heat`
and `+marks` chat commands are described in
[DESIGN_INTERFACE_HUD_INPUT.md](DESIGN_INTERFACE_HUD_INPUT.md) §3.4.1.

## 31. Fire, projectile and ground lighting (Enhanced)

Two extensions of the battle lighting prototype of §23, both user-authorized
Enhanced presentation design rather than retail evidence: more of the world's
light sources emit, and the light they emit now reaches the ground. Classic
composes the same pixels whatever this section says, nothing here is visible to
the simulation or to a committed frame [I6], and the player's Lighting switch
(§30) gates every source and the ground pass with it.

### 31.1 The added sources — contract BL5

The five families that now feed the budget are the two of §23 plus three more.
Every one of them is already admitted by its producer's own visibility gate, so
an unseen event never lights a visible receiver (BL1); the kind is recorded
whatever the switches say and the executor gates emission.

| Source | Recorder | Colour | Radius (world px at record scale) |
|---|---|---|---|
| burning feature | the feature's own burning art, tagged where the §27 heat tag is taken, behind the same extra LOS gate | measured from the art like an explosion (§23.2) × 1.6, with a warm fallback hue below measured peak 0.25 | `1.4 × art`, clamped 128–160 |
| flame stream | the flame and flame-trail strip families, after their one-point coverage gate [03 R-FX-02 §2] | as above | as above |
| projectile body | the keyed projectile sprite, never its ground shadow | measured from the art | `1.4 × art`, clamped 56–96 |
| beam / lightning | an `Emissive` stroke of the projectile renderer [06 R-WFX-01 §4] | `PAL[index] × 0.8` — a stroke has no art to measure, so its colour is the colour it is drawn in | `0.6 × length`, clamped 48–160 |
| fresh wreck | a model packet whose §28 cooling emission is non-zero | that emission × 0.6, so it fades on the wreck's own cooling curve | 80 |

Muzzle-flash art is an effect record the explosion path already counts, so
nothing is doubled. Smoke stays a receiver and is never promoted (§23.1).

A burning feature's light is placed at its frame ANCHOR: feature art records the
top-left the blitter writes from [03 §5.3.1] while effect, projectile and strip
art record the anchor, so the executor recovers one placement from the authored
offsets and every emitter then shares one position and one clip test. A stroke
carries the committed ABSOLUTE height of each endpoint in record-scale pixels,
in fields of its own: the reflection heights beside them are relative to sea
(§26) and cannot stand in for a physical height. Its light sits at the stroke's
midpoint at the mean of the two heights.

**Flicker.** A fire's emitted strength is multiplied by
`0.75 + 0.25 × (0.5 + 0.5 sin(2π t / 15 + phase))`, a bounded [0.75, 1] wave of
about half a second, where `t` is the committed tick plus the presentation
fraction and `phase` is an integer hash of the recorded position. No wall clock
and no RNG stream is read, so a replayed or paused frame reproduces the frame
exactly, and neighbouring fires do not pulse together. Sources that do not
flicker keep their measured or authored strength.

**Fire energy.** Measured on the reference install's burning-tree art, a fire
frame's peak emission is 0.39–0.51 — well above the 0.25 fallback threshold, so
the installed art carries its own hue, but well below an explosion's, which is
what the rest of the prototype was tuned against. A fire's measured colour is
therefore multiplied by 1.6 before the flicker. Without it a fire's light is
technically present and visually unreadable: the first build's pools showed
only under an 8× amplified difference.

**The warm fallback.** The fallback `(0.95, 0.55, 0.18)` did not engage in the
reviewed scene. It exists for art too dark to carry a hue at all, and it scales
by the measured peak, so a dying fire still fades rather than jumping to
orange.

### 31.2 Budget — contract BL6

The budget stays 64 sources. Each kind now has a **cap**, the most it may hold,
and a **reserve**, the slots it cannot be evicted below:

| kind | cap | reserve |
|---|---|---|
| explosion | 64 | 24 |
| nanolathe | 64 | 12 |
| fire | 24 | 12 |
| projectile | 24 | 12 |
| wreck | 16 | 4 |

The reserves partition the budget exactly (a compile-time check holds them to
it). A kind at its cap competes only with itself, keeping its own strongest.
When the budget is full the slot is taken from the kind furthest ABOVE its
reserve, and within one kind the stronger source wins with stable, record-order
ties. So a field of burning trees cannot starve explosions, and a volley of
explosions cannot push the fires already selected below their reserve. A source
whose reach misses the recorded viewport never reaches the budget at all; the
extent comes from the world record rather than the framebuffer, because
gathering precedes replay and the record extent is wider than the framebuffer
below a rest factor (§16.3).

`ModelStats.BattleLightKinds` counts the selection by family in that order, and
a `--shot` capture prints it beside `GroundLights`.

### 31.3 Ground illumination — contract BL7

Terrain received no coloured light before this: an explosion over open ground
left the ground exactly as the map painted it. The terrain pass now ends, after
`drawWater` and `drawWaterReflections`, with one pass over the visible lights:

1. if no light is selected, or the Lighting switch is off, nothing happens at
   all — no copy, no batch, no cost;
2. otherwise the scheduler is submitted (one barrier), the composite is copied
   into the existing scratch surface the refraction batch uses, and one clipped
   quad per light is drawn in ONE additive batch.

The fragment is **base × light**, not a flat wash: it samples the copied
composite under the pixel — the ground albedo already carrying the map's painted
lighting — and outputs `min(base × colour × falloff × 0.9, 1 − base)` with alpha
zero. Multiplying by the albedo keeps every painted detail (a dark rock stays
darker than the sand beside it); the per-channel clamp against `1 − base` is
what stops a bright source from flattening the ground to white; the additive
blend makes overlapping pools sum as `base × (1 + Σ L)`. The gain 2.0 stays well
below the model-face gain of §23.2 — terrain already carries the map's own
lighting — but it has to be far enough above zero for the pool to read without
amplification, which the first 0.9 was not.

The falloff is the radial law of §23.2 **with the square dropped**, and
`distance² = dx² + dy² + h²`. A model face is a small target and wants a tight
core; a ground pool is read as a shape and wants a body. Squared, the pool was a
bright point inside a wide invisible skirt; linear in `d²/r²` it carries light
out to most of its radius and still reaches zero at the edge. As for the faces:
the ground point beneath a light is its UNSHEARED position, because terrain is
drawn with a zero shear term, so a ground pixel's screen row IS its world row
[03 §2.5]. `h` is the light's height above that row. The quad covers the light's
full radius rather than the smaller disc a lifted light actually reaches: the
shader's own distance test discards the difference, so the cover is conservative
and needs no square root [I2]. The world transform of §16.3 applies exactly
once, here, as it does for the refraction batch.

Placement is deliberate. The copy is taken AFTER the water and reflection
resolve, so the pools brighten the water surface too; and the pass runs BEFORE
objects, wakes and scorch, so units are drawn over it and the ordinary fog
composite covers it (§26.3).

### 31.4 Cost and verification

Cost is two extra submissions — the barrier's copy and the batch — in a frame
with a light in view, and nothing in a frame without one. Measured on Apple M3
Pro / darwin-arm64 with the scene-version-4 Great Divide live battle benchmark,
seed 7, 1920×1080, `--zoom 2`, 300 pre-ticks, 180 measured draws, factories on.

Against the executor without the pass: modern `Submit` median/p95 4.798/5.100 →
4.875/5.282 ms, `DrawWork` median 10.130 → 10.157 ms, `Cadence` median unchanged
at 33.333 ms, device passes per frame 16 → 18 and phases 16 → 17 with the same
vertex count.

Against the first, too-faint build of this section, the tuning above is free:
`Submit` median 4.893 → 4.890 ms, `DrawWork` median 10.207 → 10.304 ms,
`Cadence` median 33.333 ms both, passes 18 and phases 17 both. It is worth
what it costs visually: over the whole frame the mean per-channel difference
rises from 0.43/255 to 2.12/255, and in a 500×280 crop at 2× the mean is
5.2/255 around a burning tree, 6.1/255 around an explosion on open ground and
10.6/255 over a smoky volley, with a maximum of 73. The first build's pools
were legible only under an 8× amplified difference; these read in the raw crop.

Every frame's census and all scene metadata match within each pair (338 → 315
units, 74 → 61 projectiles, 2,546 features, 15 burning, ticks 361 → 540). The
classic pair's `battle.png` is byte-identical and its timings are unchanged.
These are single host pairs, not GPU execution time. The final tuned modern
frame selected 45 explosion, 7 fire, 8 projectile and 4 wreck sources and
batched 24 ground discs. Artifacts are outside the repository in
`/private/tmp/d-bench-*` and `/private/tmp/d2-bench-*`.

The synthetic tier checks admission and rejection per kind, the radius clamps,
the flicker's bounds, determinism, period and per-source phase, the warm
fallback's hue and energy, the stroke midpoint/height/colour, the wreck's
emission and its shadow packet, the anchor recovery for feature art, viewport
culling of sources, the per-kind caps and reserves in both arrival orders, the
ground quads' clipping to the viewport and the empty-batch cases. The opt-in
real-device fixture checks that a stroke brightens the terrain beneath it while
terrain beyond its reach is untouched, that a flame sprite lights a facing model
face but not a back-facing one, and that disabling Lighting restores the
composite exactly.

### 31.5 Known limits

There is no occlusion: a fire lights the ground on the far side of a wall, and
a unit standing between a light and the ground casts no shadow into the pool.
Terrain has no normals here, so a hillside takes the same light as flat ground —
the pool is a screen-space disc, not a projection onto relief. The ground pass
reads one copy of the composite, so a light is applied to whatever the terrain
pass has already drawn, including the water surface, and never to the objects
drawn over it. A fresh wreck's light borrows the §28 cooling emission, which the
Distortion switch owns: with Distortion off a wreck emits no light even when
Lighting is on. The flicker's phase hash is a position hash, so two fires at the
same recorded position pulse together, and a fire that moves changes phase.

## 32. Reflected explosions, shoreline band, and the removed sun glitter (Enhanced)

Three user-requested additions to the coastal water of §26, one of which
(§32.1) was later removed on review and is kept here only as a record. They are
authored Nanolathe presentation choices, not retail behavioral claims: every
constant below is artistic, the classic executor composes identical pixels, and
the player's Water switch (§30) gates the surviving two — with it off the
recorder marks no water surface and admits no reflection site, and the composed
frame is byte-identical to the same build without them.

### 32.1 Sun glitter

**Removed.** The surface shader briefly added a narrow specular lobe on wave
tops, driven by a pseudo-normal differenced from the drifting height field. It
landed at a peak of 0.35, was cut to 0.08 on the first review as too strong for a
surface that must stay subtle, and was removed entirely on the second: the user
judged it was not earning its keep — "I don't think it's doing much" — and asked
for motion that reads as living water instead of a tiled sheet sliding across the
screen. The lobe, the half-vector it met, and the shared two-layer height
function it needed are all gone; the four value-noise evaluations they cost went
with them.

The replacement is in §26.3: no fixed scroll, three downwind speeds, a slow
domain warp so the ripple field churns in place, a rotated fine lattice, and
coarse gust patches that darken and roughen the water they cross. Nothing else in
§32 changed, and the Water switch still gates the whole treatment.

### 32.2 Reflected explosions and impacts

Named explosion and impact art now records `ReflectWater` and
`ReflectionHeight` like a projectile billboard does, from the same
`reflectionWaterAt`/`reflectionHeight` pair, so the Water switch already
governs admission. The reflection preparation admits a billboard at height
zero, where it previously required a strictly positive height, and the source
shader's waterline clip makes the same distinction: a model face or beam stroke
carries per-fragment physical height and is still cut at and below sea, while a
billboard's height is one constant for the whole quad and its admission has
already happened on the recording side.

Height zero is the case that matters. A surface impact's anchor sits at sea
level, so mirroring about that anchor sends the opaque upper half of the
fireball below the anchor, where the art itself is keyed out — which is where
the reflection becomes visible. Reflected fire is drawn before objects, so the
explosion composites over its own reflection afterwards; that ordering is
intended. The resolve keeps its cool tint and 25 percent opacity for fire as
well: the reflection reads as water rather than as a second fireball, and
nothing about bright art is treated as a special case.

A projectile billboard resting exactly at the surface is admitted on the same
terms. Nothing else changes: the 4,096-vertex sprite/stroke reservation, the
32,768-vertex frame cap, the source plane, the passes and the mask-clipped
resolve are all as §26.4 and §26.6 left them.

### 32.3 Damp shoreline band and shallow tint

Dry ground the water has just washed keeps a darker tone. The mask cannot carry
a dry-side distance field — its alpha is opaque everywhere and Ebitengine
images are premultiplied, and a dry-side green would corrupt the wet-side
`deep` and `shore` terms wherever bilinear filtering crossed the boundary — so
the shader measures nearness by sampling instead. Eight taps on a ring of eight
world pixels, at the mask step, give a maximum and a mean. The maximum is
converted with a 0.2-to-1.0 smoothstep as the band's admission; the mean shapes
its falloff through a 0.10-to-0.45 smoothstep, because the red channel is
binary and the maximum alone would end the band on a whole texel. Both taps are
bilinear, which is what makes the band resolution-independent: the mask step
grows with map size, and on a large map, where one texel is eight world pixels,
the ring spans a single texel and only the filtered reading still resolves the
band's width.

The result multiplies a dry gate — a 0.05-to-0.5 smoothstep on the mask's blue
channel, times one minus water coverage. The gate is deliberately low: its only
job is to exclude terrain that is neither medium, since invalid ground and
excluded liquid carry no dry flag at all, and a higher threshold would suppress
the band exactly at the waterline, where it belongs. The band darkens the
painted colour by up to twelve percent, half of that steady and half pulsing on
the lap phase the shore foam carries at the boundary, so the ground darkens as
a wave front arrives. All of this runs before the shader's dry early-out,
because the band lives on dry texels; a pixel with no water on its ring returns
the painted colour unchanged, and the added taps cost nothing on wet pixels.

On the water side, the result mixes eight percent toward a pale cyan
(0.62, 0.80, 0.84), scaled by one minus a 0.0-to-0.35 smoothstep of the shore
distance, so water lightens as the bottom rises. Deep water is untouched.

Because the band lies beside the water rather than on it, the 128-pixel block
index now queries one block of slack on each side of the viewport, so a coast
just past the viewport edge still runs the pass and the band reaches the
visible strip.

### 32.4 Verification

Real-device fixtures (`NANOLATHE_GPU_DEVICE_TEST=1`) cover the surviving
additions. The surface fixture uses an authored flat-grey terrain and a straight
authored coast, drawn through the shipped shader source and through variants that
replace exactly one term with a constant, so any difference is that term: the
composed surface is byte-identical on a held phase and different as the field
advances; a gust patch changes water texels and no dry texel, and with the gust
held flat the surface still differs between two phases, so the churn comes from
the domain warp and not from the gust alone; the damp band darkens only dry
texels, never brightens, leaves covered water untouched and leaves ground beyond
the ring byte-identical; the shallow tint changes near-shore wet texels only. No
census is pinned for the gust — its lattice is far coarser than the fixture, so
how much of the fixture one patch covers is an accident of the authored extent.
The reflection fixture adds a surface impact at height zero whose upper half
alone is opaque, and checks that it casts a reflection below its anchor, that the
mask holds it off dry ground, and that the same art standing on dry ground is
never admitted.

`tools/check` and `tools/check-retail` pass. Modern captures at 1280x960 native
and at 2x zoom were taken from main and from this branch on Brain Coral, which
opens on open sea, and Ring Atoll, which opens on a beach. Open water now carries
broad light and dark patches a few hundred world pixels across, drifting over a
finer diagonal ripple; the same water on main is an even speckle with no
structure above the ripple scale. At 2x the ripple reads as irregular swirls
rather than aligned rows. The beach still shows a continuous darker strip along
the waterline and a paler band of water inside it.

Motion was measured rather than asserted. Thirty consecutive captures two ticks
apart, cropped to a 400x300 window of open water, differ from their predecessor
by a mean of 1.51 levels of 255 per channel — minimum 1.49, maximum 1.53 — so
every frame moves and no frame jumps; main's figure on the same window is 1.21.
The churn alone was measured on the drift-free device fixture, where nothing but
the domain warp can move: the surface decorrelates by about 0.4 levels per second
and about 1.3 over four seconds, against roughly 3 at full decorrelation. Four
times slower warp speeds left the field visually static over the same interval
and eight times faster began to shimmer, so the landed speeds are the middle of
that bracket.

**Gap.** No human has watched the surface in a live window; the evidence above is
captures and frame arithmetic. Whether the churn reads as "alive" rather than as
a slow wobble, and whether the gust patches read as wind, are judgements only a
live viewing can settle. The eight-times bracket above was also judged from a
numeric decorrelation rate, not from watching it.

**Gap.** No live capture of an explosion over water was obtained: `--shot` runs
a skirmish with no opponent, so nothing fires, and the battle benchmark places
its armies on dry ground. The device fixture is the only evidence for the
reflected effect; an AI opponent reachable from a capture flag, or a staged
coastal diagnostic like the one §26.3 describes, would settle it.
